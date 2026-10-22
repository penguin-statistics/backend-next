package middlewares

import (
	"strings"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
	"github.com/vmihailenco/msgpack/v5"

	"exusiai.dev/backend-next/internal/pkg/pgerr"
	"exusiai.dev/backend-next/internal/util/rekuest"
)

type IdempotencyConfig struct {
	// Lifetime is the maximum lifetime of an idempotency key.
	Lifetime time.Duration

	// KeyHeader is the name of the header that contains the idempotency key.
	KeyHeader string

	// KeepResponseHeaders is a list of headers that should be kept from the original response.
	// By default, all headers are kept.
	KeepResponseHeaders []string

	keepResponseHeadersMap map[string]struct{}

	// Redis stores the response of an idempotency key under <KeyPrefix>:<key>, and its lock under
	// mutex:idempotency-request:<key>.
	Redis     redis.Scripter
	KeyPrefix string

	// Next defines a function to skip this middleware when returned true.
	//
	// Optional. Default: nil
	Next func(c *fiber.Ctx) bool
}

type deprecatedIdempotencyResponse struct {
	StatusCode int
	Headers    map[string]string
	Body       []byte
}

type idempotencyResponse struct {
	StatusCode int
	Headers    map[string][]string
	Body       []byte
}

func Idempotency(config *IdempotencyConfig) fiber.Handler {
	config.keepResponseHeadersMap = make(map[string]struct{})
	for _, header := range config.KeepResponseHeaders {
		config.keepResponseHeadersMap[strings.ToLower(header)] = struct{}{}
	}
	return func(c *fiber.Ctx) error {
		// Don't execute middleware if Next returns true
		if config.Next != nil && config.Next(c) {
			return c.Next()
		}

		// Don't execute middleware if the idempotent key is missing
		key := c.Get(config.KeyHeader)
		if key == "" {
			if l := log.Trace(); l.Enabled() {
				l.
					Str("evt.name", "http.idempotency.no_key").
					Msg("idempotency key is missing. Skipping middleware.")
			}
			return c.Next()
		}

		// Validate idempotency key
		if err := rekuest.Validate.Var(key, "max=128,alphanum"); err != nil {
			if l := log.Trace(); l.Enabled() {
				l.
					Err(err).
					Str("evt.name", "http.idempotency.invalid_key").
					Msg("idempotency key is invalid. Returning error.")
			}
			return pgerr.ErrInvalidReq.Msg("invalid idempotency key: idempotency key can only be at most %d characters, consist of only alphanumeric characters", constant.IdempotencyKeyLengthLimit)
		}

		// Save idempotency key to context
		c.Locals(constant.IdempotencyKeyLocalsKey, key)

		lock := &idempotencyLock{
			redis: config.Redis,
			keys:  [2]string{config.KeyPrefix + ":" + key, "mutex:idempotency-request:" + key},
			token: newIdempotencyLockToken(),
			ctx:   spanOnly(c.UserContext()),
		}

		// Look for the saved response and lock the key in one round trip, retrying while another request holds the
		// lock.
		var (
			response []byte
			locked   bool
		)
		for try := 0; ; try++ {
			if try != 0 {
				time.Sleep(idempotencyLockRetryDelay)
			}
			var err error
			response, locked, err = lock.acquire(try == 0)
			if response != nil {
				break
			}
			if try == 0 {
				// Idempotency key not empty and not found in storage. Lock the key.
				// Logged at info: dashboards count lock + hit as the opt-in requests.
				if l := log.Info(); l.Enabled() {
					l.
						Str("evt.name", "http.idempotency.lock").
						Str("key", key).
						Msg("idempotency key not found in storage. Locking key.")
				}
			}
			if locked {
				break
			}
			if err == nil {
				err = errIdempotencyLockTaken
			} else {
				// the script may have taken the lock before its reply was lost
				_, _ = lock.release()
			}
			if try == idempotencyLockTries-1 {
				log.Err(err).
					Str("evt.name", "http.idempotency.lock.failed").
					Str("key", key).
					Msg("failed to lock idempotency key. Returning error.")
				return pgerr.ErrInternalError.Msg("failed to lock idempotency key: idempoency key is locked by another request; are you sending the same request concurrently or retrying with little or no backoff?")
			}
		}

		unlocked := false
		defer func() {
			if locked && !unlocked {
				lock.unlock(key)
			}
		}()

		if response != nil {
			// Idempotency key found in storage. Return the response.
			// Logged at info: dashboards count the hits.
			if l := log.Info(); l.Enabled() {
				l.
					Str("evt.name", "http.idempotency.hit").
					Str("key", key).
					Msg("idempotency key found in storage")
			}
			return unmarshalResponseToFiberResponse(c, config, response)
		}

		// Execute the request handler
		err := c.Next()
		if err != nil {
			// If the request handler returned an error, return it and skip idempotency
			if l := log.Trace(); l.Enabled() {
				l.
					Str("evt.name", "http.idempotency.handler.error").
					Msg("request handler returned an error. Skipping saving the idempotency response.")
			}
			return err
		}

		// Marshal response to bytes
		responseBytes, err := marshalResponseToBytes(c, config)
		if err != nil {
			log.Error().
				Str("evt.name", "http.idempotency.response.marshal.failed").
				Err(err).
				Msg("error marshaling response to bytes. Skipping saving the idempotency response.")
			return err
		}

		// Store the response in the storage and unlock the key
		released, err := lock.save(responseBytes, config.Lifetime)
		if err != nil {
			log.Error().
				Str("evt.name", "http.idempotency.response.save.failed").
				Err(err).
				Msg("error saving the idempotency response. Skipping saving the idempotency response.")
			return err
		}
		unlocked = true

		// Add idempotency header
		c.Set(constant.IdempotencyHeader, "saved")

		if l := log.Debug(); l.Enabled() {
			l.
				Str("evt.name", "http.idempotency.saved").
				Str("key", key).
				Msg("idempotency response saved")
		}

		if !released {
			log.Err(errIdempotencyLockLost).
				Str("evt.name", "http.idempotency.unlock.failed").
				Str("key", key).
				Msg("failed to unlock idempotency key.")
		}

		return nil
	}
}

func marshalResponseToBytes(c *fiber.Ctx, conf *IdempotencyConfig) ([]byte, error) {
	var response idempotencyResponse

	// Get status code
	response.StatusCode = c.Response().StatusCode()

	// Get headers
	if conf.KeepResponseHeaders == nil {
		response.Headers = c.GetRespHeaders()
	} else {
		response.Headers = make(map[string][]string)
		headers := c.GetRespHeaders()
		for header := range headers {
			if _, ok := conf.keepResponseHeadersMap[strings.ToLower(header)]; ok {
				response.Headers[header] = headers[header]
			}
		}
	}

	// Get body
	if c.Response().Body() != nil {
		response.Body = c.Response().Body()
	}

	return msgpack.Marshal(response)
}

func unmarshalResponseToFiberResponse(c *fiber.Ctx, conf *IdempotencyConfig, responseBytes []byte) error {
	var response idempotencyResponse
	if err := msgpack.Unmarshal(responseBytes, &response); err != nil {
		var deprecatedResponse deprecatedIdempotencyResponse
		if err := msgpack.Unmarshal(responseBytes, &deprecatedResponse); err != nil {
			return err
		}

		// Deprecated response format. Convert to new format
		response.StatusCode = deprecatedResponse.StatusCode
		for header, values := range deprecatedResponse.Headers {
			response.Headers[header] = []string{values}
		}
		response.Body = deprecatedResponse.Body
	}

	// Set status code
	c.Status(response.StatusCode)

	// Set headers
	for header, values := range response.Headers {
		c.Append(header, values...)
	}

	// Add idempotency marker
	c.Set(constant.IdempotencyHeader, "hit")

	// Set body
	if len(response.Body) > 0 {
		return c.Send(response.Body)
	}

	return nil
}

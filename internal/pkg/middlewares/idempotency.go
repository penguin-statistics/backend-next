package middlewares

import (
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
	"github.com/vmihailenco/msgpack/v5"

	"github.com/penguin-statistics/backend-next/internal/constant"
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

	// Storage is the storage backend for the idempotency key & its response data.
	Storage fiber.Storage

	// Next defines a function to skip this middleware when returned true.
	//
	// Optional. Default: nil
	Next func(c *fiber.Ctx) bool
}

type idempotencyResponse struct {
	StatusCode int
	Headers    map[string]string
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
			log.Trace().Msg("IdempotencyMiddleware: idempotency key is missing. Skipping middleware.")
			return c.Next()
		}

		// Idempotency key not empty. Check if it is in the storage
		response, err := config.Storage.Get(key)
		if err == nil && response != nil {
			// Idempotency key found in storage. Return the response
			log.Debug().
				Str("key", key).
				Msg("IdempotencyMiddleware: idempotency key found in storage. Returning saved response.")
			return unmarshalResponseToFiberResponse(c, config, response)
		}

		// Execute the request handler
		err = c.Next()
		if err != nil {
			// If the request handler returned an error, return it and skip idempotency
			log.Trace().Msg("IdempotencyMiddleware: request handler returned an error. Skipping saving the idempotency response.")
			return err
		}

		// Marshal response to bytes
		responseBytes, err := marshalResponseToBytes(c, config)
		if err != nil {
			log.Error().Err(err).Msg("IdempotencyMiddleware: error marshaling response to bytes. Skipping saving the idempotency response.")
			return err
		}

		// Store the response in the storage
		if err := config.Storage.Set(key, responseBytes, config.Lifetime); err != nil {
			log.Error().Err(err).Msg("IdempotencyMiddleware: error saving the idempotency response. Skipping saving the idempotency response.")
			return err
		}

		// Add idempotency header
		c.Set(constant.IdempotencyHeader, "saved")

		log.Debug().
			Str("key", key).
			Msg("IdempotencyMiddleware: Idempotency Key given and no response was saved. Executed request handler and saved returned response in storage.")

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
		response.Headers = make(map[string]string)
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
		return err
	}

	// Set status code
	c.Status(response.StatusCode)

	// Set headers
	for header, value := range response.Headers {
		c.Set(header, value)
	}

	// Add idempotency marker
	c.Set(constant.IdempotencyHeader, "hit")

	// Set body
	if len(response.Body) > 0 {
		return c.Send(response.Body)
	}

	return nil
}

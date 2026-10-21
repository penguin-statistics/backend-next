package middlewares

import (
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"

	"exusiai.dev/backend-next/internal/pkg/pgerr"
)

// RetryAfter makes sure every 429 Too Many Requests response carries a Retry-After header, so that clients back off
// instead of retrying at once. A handler or limiter that knows when its window resets sets the header itself (fiber's
// limiter does); RetryAfter only fills in fallback, in whole seconds, when nothing did.
//
// It also covers a 429 returned as a *fiber.Error or *pgerr.PenguinError that the ErrorHandler has not written yet.
func RetryAfter(fallback time.Duration) fiber.Handler {
	seconds := strconv.Itoa(int(fallback.Seconds()))

	return func(c *fiber.Ctx) error {
		err := c.Next()

		if statusOf(c, err) == fiber.StatusTooManyRequests && len(c.Response().Header.Peek(fiber.HeaderRetryAfter)) == 0 {
			c.Set(fiber.HeaderRetryAfter, seconds)
		}

		return err
	}
}

// statusOf returns the status the response will be sent with, once httpserver.ErrorHandler has handled err. It
// mirrors that handler: a *fiber.Error or *pgerr.PenguinError keeps its own code, any other error becomes a 400
// (fs.PathError) or a 500, and without an error the response keeps the status the handler set.
func statusOf(c *fiber.Ctx, err error) int {
	switch e := err.(type) {
	case nil:
		return c.Response().StatusCode()
	case *fiber.Error:
		return e.Code
	case *pgerr.PenguinError:
		return e.StatusCode
	default:
		// not a 429 in any case
		return fiber.StatusInternalServerError
	}
}

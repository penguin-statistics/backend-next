package middlewares

import (
	"errors"
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

// statusOf returns the status the response will be sent with, once the ErrorHandler has handled err.
func statusOf(c *fiber.Ctx, err error) int {
	var fe *fiber.Error
	if errors.As(err, &fe) {
		return fe.Code
	}
	var pe *pgerr.PenguinError
	if errors.As(err, &pe) {
		return pe.StatusCode
	}
	return c.Response().StatusCode()
}

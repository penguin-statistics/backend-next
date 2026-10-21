package middlewares

import (
	"time"

	"github.com/gofiber/fiber/v2"
)

// ConcurrencyLimit lets at most max requests run the handlers after it at the same time, across the whole process.
// A request finding all of them busy waits up to wait for one to finish, and is answered by limitReached otherwise.
func ConcurrencyLimit(max int, wait time.Duration, limitReached fiber.Handler) fiber.Handler {
	slots := make(chan struct{}, max)
	return func(c *fiber.Ctx) error {
		timer := time.NewTimer(wait)
		select {
		case slots <- struct{}{}:
			timer.Stop()
		case <-timer.C:
			return limitReached(c)
		}
		defer func() { <-slots }()
		return c.Next()
	}
}

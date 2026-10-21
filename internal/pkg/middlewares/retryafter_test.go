package middlewares

import (
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/limiter"

	"exusiai.dev/backend-next/internal/pkg/pgerr"
)

func TestRetryAfter(t *testing.T) {
	app := fiber.New(fiber.Config{
		// like httpserver.ErrorHandler, which writes a PenguinError with its own status
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			if e, ok := err.(*pgerr.PenguinError); ok {
				return c.Status(e.StatusCode).JSON(e)
			}
			if e, ok := err.(*fiber.Error); ok {
				return fiber.DefaultErrorHandler(c, e)
			}
			return c.SendStatus(fiber.StatusInternalServerError)
		},
	})
	app.Use(RetryAfter(time.Minute))

	// answers 429 itself without a Retry-After
	app.Get("/bare-429", func(c *fiber.Ctx) error {
		return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"code": "TOO_MANY_REQUESTS", "message": "slow down"})
	})
	// sets its own Retry-After, which must be kept
	app.Get("/own-retry-after", func(c *fiber.Ctx) error {
		c.Set(fiber.HeaderRetryAfter, "5")
		return c.SendStatus(fiber.StatusTooManyRequests)
	})
	// 429 errors the ErrorHandler has yet to write
	app.Get("/fiber-error", func(c *fiber.Ctx) error {
		return fiber.ErrTooManyRequests
	})
	app.Get("/penguin-error", func(c *fiber.Ctx) error {
		return pgerr.New(fiber.StatusTooManyRequests, "TOO_MANY_REQUESTS", "slow down")
	})
	// fiber's limiter sets Retry-After to the seconds left in the window
	app.Get("/limited", limiter.New(limiter.Config{Max: 1, Expiration: 5 * time.Minute}), func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})
	app.Get("/ok", func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})
	// sets 429 but then fails: the ErrorHandler answers 500, which needs no Retry-After
	app.Get("/429-then-error", func(c *fiber.Ctx) error {
		c.Status(fiber.StatusTooManyRequests)
		return errors.New("boom")
	})
	// a wrapped 429 error: the ErrorHandler only matches the bare type, so this becomes a 500 too
	app.Get("/wrapped-429", func(c *fiber.Ctx) error {
		return fmt.Errorf("wrapped: %w", fiber.ErrTooManyRequests)
	})

	get := func(t *testing.T, path string) (int, string) {
		t.Helper()
		resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, path, nil))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode, resp.Header.Get(fiber.HeaderRetryAfter)
	}

	tests := []struct {
		path       string
		retryAfter string
	}{
		{"/bare-429", "60"},
		{"/own-retry-after", "5"},
		{"/fiber-error", "60"},
		{"/penguin-error", "60"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			status, retryAfter := get(t, tt.path)
			if status != fiber.StatusTooManyRequests {
				t.Errorf("status = %d, want 429", status)
			}
			if retryAfter != tt.retryAfter {
				t.Errorf("Retry-After = %q, want %q", retryAfter, tt.retryAfter)
			}
		})
	}

	t.Run("/limited", func(t *testing.T) {
		if status, retryAfter := get(t, "/limited"); status != fiber.StatusOK || retryAfter != "" {
			t.Errorf("first request: status %d, Retry-After %q; want 200 without Retry-After", status, retryAfter)
		}
		status, retryAfter := get(t, "/limited")
		if status != fiber.StatusTooManyRequests {
			t.Fatalf("second request: status = %d, want 429", status)
		}
		// the limiter's own value (about 300 s left in the window), not the 60 s fallback
		if retryAfter == "" || retryAfter == "60" {
			t.Errorf("second request: Retry-After = %q, want the limiter's", retryAfter)
		}
	})

	for _, path := range []string{"/429-then-error", "/wrapped-429"} {
		t.Run(path, func(t *testing.T) {
			if status, retryAfter := get(t, path); status != fiber.StatusInternalServerError || retryAfter != "" {
				t.Errorf("status %d, Retry-After %q; want 500 without Retry-After", status, retryAfter)
			}
		})
	}

	t.Run("/ok", func(t *testing.T) {
		if _, retryAfter := get(t, "/ok"); retryAfter != "" {
			t.Errorf("Retry-After = %q on a 200, want none", retryAfter)
		}
	})
}

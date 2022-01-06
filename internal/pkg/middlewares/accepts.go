package middlewares

import (
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/pkg/errors"
)

func Accepts(mimes ...string) func(ctx *fiber.Ctx) error {
	return func(ctx *fiber.Ctx) error {
		if ctx.Accepts(mimes...) != "" {
			return nil
		}

		return errors.ErrInvalidRequest.WithMessage("Invalid or missing Accept header. Accepts: %s", strings.Join(mimes, ", "))
	}
}

var AcceptsJSON = Accepts("application/json")

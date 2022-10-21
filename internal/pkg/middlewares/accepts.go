package middlewares

import (
	"strings"

	"github.com/gofiber/fiber/v2"

	"exusiai.dev/backend-next/internal/pkg/pgerr"
)

func Accepts(mimes ...string) func(ctx *fiber.Ctx) error {
	return func(ctx *fiber.Ctx) error {
		if ctx.Accepts(mimes...) != "" {
			return nil
		}

		return pgerr.ErrInvalidReq.Msg("Invalid or missing Accept header. Accepts: %s", strings.Join(mimes, ", "))
	}
}

var AcceptsJSON = Accepts("application/json")

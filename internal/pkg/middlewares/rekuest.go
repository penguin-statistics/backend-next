package middlewares

import (
	"github.com/gofiber/fiber/v2"

	"exusiai.dev/backend-next/internal/pkg/pgerr"
	"exusiai.dev/backend-next/internal/util/rekuest"
)

func InjectValidBody[T any]() func(*fiber.Ctx) error {
	return func(ctx *fiber.Ctx) error {
		var dest T
		if err := ctx.BodyParser(dest); err != nil {
			return pgerr.ErrInvalidReq.Msg("invalid request: %s", err)
		}

		if err := rekuest.ValidateStruct(ctx, dest); err != nil {
			return pgerr.NewInvalidViolations(err)
		}

		ctx.Locals("body", dest)

		return ctx.Next()
	}
}

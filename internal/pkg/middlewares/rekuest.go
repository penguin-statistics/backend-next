package middlewares

import (
	"github.com/gofiber/fiber/v2"

	"exusiai.dev/backend-next/internal/pkg/flog"
	"exusiai.dev/backend-next/internal/pkg/pgerr"
	"exusiai.dev/backend-next/internal/util/rekuest"
)

func InjectValidBody[T any]() func(*fiber.Ctx) error {
	return func(ctx *fiber.Ctx) error {
		var dest T
		if err := ctx.BodyParser(&dest); err != nil {
			flog.WarnFrom(ctx, "middlewares.inject_valid_body.body_parser").
				Err(err).
				Msg("invalid request")
			return pgerr.ErrInvalidReq.Msg("invalid request: %s", err)
		}

		if err := rekuest.ValidateStruct(ctx, dest); err != nil {
			truncatedBody := string(ctx.Body())
			if len(truncatedBody) > 5000 {
				truncatedBody = truncatedBody[:5000] + "..."
			}

			flog.WarnFrom(ctx, "middlewares.inject_valid_body.validate_struct").
				Str("http.request.body", truncatedBody).
				Any("errors", err).
				Msg("invalid request")
			return pgerr.NewInvalidViolations(err)
		}

		ctx.Locals("body", dest)

		return ctx.Next()
	}
}

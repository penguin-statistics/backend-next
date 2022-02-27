package httpserver

import (
	"github.com/gofiber/contrib/fibersentry"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"

	"github.com/penguin-statistics/backend-next/internal/pkg/errors"
)

func ErrorHandler(ctx *fiber.Ctx, err error) error {
	// Use custom error handler to return JSON error responses
	if e, ok := err.(*errors.PenguinError); ok {
		log.Warn().
			Err(err).
			Str("method", ctx.Method()).
			Str("path", ctx.Path()).
			Msg(e.Message)

		// Provide error code if errors.PenguinError type
		body := fiber.Map{
			"code":    e.ErrorCode,
			"message": e.Message,
		}

		// Add extra details if needed
		if e.Extras != nil && len(*e.Extras) > 0 {
			for k, v := range *e.Extras {
				body[k] = v
			}
		}

		return ctx.Status(e.StatusCode).JSON(body)
	}

	// Return default error handler
	// Default 500 statuscode
	code := fiber.StatusInternalServerError

	if e, ok := err.(*fiber.Error); ok {
		// Overwrite status code if fiber.Error type & provided code
		code = e.Code
	}

	log.Error().
		Stack().
		Err(err).
		Str("method", ctx.Method()).
		Str("path", ctx.Path()).
		Int("status", code).
		Msg("Internal Server Error")

	fibersentry.GetHubFromContext(ctx).CaptureException(err)

	return ctx.Status(code).JSON(fiber.Map{
		"code":    errors.CodeInternalError,
		"message": "internal server error",
	})
}

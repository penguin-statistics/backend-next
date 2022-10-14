package httpserver

import (
	"context"
	"io/fs"
	"strconv"

	"github.com/davecgh/go-spew/spew"
	"github.com/getsentry/sentry-go"
	"github.com/gofiber/fiber/v2"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"

	"github.com/penguin-statistics/backend-next/internal/pkg/pgerr"
	"github.com/penguin-statistics/backend-next/internal/pkg/pgid"
)

var sentryHubKey = "sentry-hub"

func HandleCustomError(ctx *fiber.Ctx, e *pgerr.PenguinError) error {
	// Provide error code if pgerr.PenguinError type
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

func ErrorHandler(ctx *fiber.Ctx, err error) error {
	defer func() {
		// Recover from panic: ErrorHandler panics will not be handled by fasthttp
		// as the request pipeline might not yet reach any middlewares yet.
		// e.g. a 431 Request Header Fields Too Large error occurs.
		if r := recover(); r != nil {
			log.Error().
				Str("recovered", spew.Sdump(r)).
				Str("method", ctx.Method()).
				Str("path", ctx.Path()).
				Msg("Recovered from panic")

			sentry.CaptureMessage(spew.Sdump(r))

			ctx.Status(500).JSON(fiber.Map{
				"code":    pgerr.CodeInternalError,
				"message": pgerr.ErrInternalErrorImmutable.Message,
			})
		}
	}()

	// Return default error handler
	// Default 500 statuscode
	re := pgerr.ErrInternalErrorImmutable

	if e, ok := err.(*fiber.Error); ok {
		// Use default error handler if not a custom error
		return fiber.DefaultErrorHandler(ctx, e)
	}

	if _, ok := err.(*fs.PathError); ok {
		// File not found
		return HandleCustomError(ctx, pgerr.ErrInvalidReq)
	}

	if e, ok := err.(*pgerr.PenguinError); ok {
		// Use custom error handler if it's a custom error
		return HandleCustomError(ctx, e)
	}

	// must be an unexpected runtime error then
	log.Error().
		Stack().
		Err(err).
		Str("method", ctx.Method()).
		Str("path", ctx.Path()).
		Int("status", re.StatusCode).
		Msg("Internal Server Error")

	reportSentry(ctx, err, re.StatusCode)

	return HandleCustomError(ctx, &re)
}

func reportSentry(ctx *fiber.Ctx, err error, status int) {
	// pre-filter: ignore context cancelled
	if errors.Is(err, context.Canceled) {
		return
	}

	hub := gentlelyGetHubFromContext(ctx)

	if hub != nil {
		hub.Scope().SetTag("status", strconv.Itoa(status))
		if u := pgid.Extract(ctx); u != "" {
			hub.Scope().SetUser(sentry.User{
				ID: u,
			})
		}
		hub.CaptureException(err)
	} else {
		sentry.CaptureException(err)
	}
}

func gentlelyGetHubFromContext(ctx *fiber.Ctx) *sentry.Hub {
	if hub, ok := ctx.Locals(sentryHubKey).(*sentry.Hub); ok {
		return hub
	}
	return nil
}

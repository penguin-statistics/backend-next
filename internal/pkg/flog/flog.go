// Package flog provides a set of fiber.Ctx helpers for zerolog.
package flog

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/xid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// FromFiberCtx gets the logger in the request's context.
// This is a shortcut for log.Ctx(r.Context())
func FromFiberCtx(r *fiber.Ctx) *zerolog.Logger {
	return log.Ctx(r.UserContext())
}

// NewHandlerMiddleware injects log into requests context.
func NewHandlerMiddleware(log zerolog.Logger) func(*fiber.Ctx) error {
	return func(ctx *fiber.Ctx) error {
		// Create a copy of the logger (including internal context slice)
		// to prevent data race when using UpdateContext.
		l := log.With().Logger()
		// ctx.SetUserContext(context.WithValue(ctx.UserContext(), idKey{}, l))
		injectedCtx := l.WithContext(ctx.UserContext())
		ctx.SetUserContext(injectedCtx)
		return ctx.Next()
	}
}

// URLHandler adds the requested URL as a field to the context's logger
// using fieldKey as field key.
func URLHandler(fieldKey string) func(ctx *fiber.Ctx) error {
	return func(ctx *fiber.Ctx) error {
		log := zerolog.Ctx(ctx.UserContext())
		log.UpdateContext(func(c zerolog.Context) zerolog.Context {
			return c.Str(fieldKey, ctx.Path())
		})
		return ctx.Next()
	}
}

// MethodHandler adds the request method as a field to the context's logger
// using fieldKey as field key.
func MethodHandler(fieldKey string) func(ctx *fiber.Ctx) error {
	return func(ctx *fiber.Ctx) error {
		log := zerolog.Ctx(ctx.UserContext())
		log.UpdateContext(func(c zerolog.Context) zerolog.Context {
			return c.Str(fieldKey, ctx.Method())
		})
		return ctx.Next()
	}
}

// RequestHandler adds the request method and URL as a field to the context's logger
// using fieldKey as field key.
func RequestHandler(fieldKey string) func(ctx *fiber.Ctx) error {
	return func(ctx *fiber.Ctx) error {
		log := zerolog.Ctx(ctx.UserContext())
		log.UpdateContext(func(c zerolog.Context) zerolog.Context {
			return c.Str(fieldKey, ctx.Method()+" "+ctx.Path())
		})
		return ctx.Next()
	}
}

// RemoteAddrHandler adds the request's remote address as a field to the context's logger
// using fieldKey as field key.
func RemoteAddrHandler(fieldKey string) func(ctx *fiber.Ctx) error {
	return func(ctx *fiber.Ctx) error {
		log := zerolog.Ctx(ctx.UserContext())
		log.UpdateContext(func(c zerolog.Context) zerolog.Context {
			return c.Str(fieldKey, ctx.IP())
		})
		return ctx.Next()
	}
}

// UserAgentHandler adds the request's user-agent as a field to the context's logger
// using fieldKey as field key.
func UserAgentHandler(fieldKey string) func(ctx *fiber.Ctx) error {
	return func(ctx *fiber.Ctx) error {
		log := zerolog.Ctx(ctx.UserContext())
		log.UpdateContext(func(c zerolog.Context) zerolog.Context {
			return c.Str(fieldKey, ctx.Get("User-Agent"))
		})
		return ctx.Next()
	}
}

// RefererHandler adds the request's referer as a field to the context's logger
// using fieldKey as field key.
func RefererHandler(fieldKey string) func(ctx *fiber.Ctx) error {
	return func(ctx *fiber.Ctx) error {
		log := zerolog.Ctx(ctx.UserContext())
		log.UpdateContext(func(c zerolog.Context) zerolog.Context {
			return c.Bytes(fieldKey, ctx.Request().Header.Referer())
		})
		return ctx.Next()
	}
}

type idKey struct{}

// IDFromFiberCtx returns the unique id associated to the *fiber.Ctx if any.
func IDFromFiberCtx(r *fiber.Ctx) (id xid.ID, ok bool) {
	if r == nil {
		return
	}
	return IDFromCtx(r.UserContext())
}

// IDFromCtx returns the unique id associated to the context if any.
func IDFromCtx(ctx context.Context) (id xid.ID, ok bool) {
	id, ok = ctx.Value(idKey{}).(xid.ID)
	return
}

// FiberCtxWithID adds the given xid.ID to the UserContext of *fiber.Ctx
func SetFiberCtxWithID(ctx *fiber.Ctx, id xid.ID) {
	ctx.SetUserContext(CtxWithID(ctx.UserContext(), id))
}

// CtxWithID adds the given xid.ID to the context
func CtxWithID(ctx context.Context, id xid.ID) context.Context {
	return context.WithValue(ctx, idKey{}, id)
}

// RequestIDHandler returns a handler setting a unique id to the request which can
// be gathered using IDFromFiberCtx(req). This generated id is added as a field to the
// logger using the passed fieldKey as field name. The id is also added as a response
// header if the headerName is not empty.
//
// The generated id is a URL safe base64 encoded mongo object-id-like unique id.
// Mongo unique id generation algorithm has been selected as a trade-off between
// size and ease of use: UUID is less space efficient and snowflake requires machine
// configuration.
func RequestIDHandler(fieldKey, headerName string) fiber.Handler {
	return func(ctx *fiber.Ctx) error {
		id, ok := IDFromFiberCtx(ctx)
		if !ok {
			id = xid.New()
			SetFiberCtxWithID(ctx, id)
		}
		if fieldKey != "" {
			log := FromFiberCtx(ctx)
			log.UpdateContext(func(c zerolog.Context) zerolog.Context {
				return c.Str(fieldKey, id.String())
			})
		}
		if headerName != "" {
			ctx.Set(headerName, id.String())
		}
		return ctx.Next()
	}
}

// CustomHeaderHandler adds given header from request's header as a field to
// the context's logger using fieldKey as field key.
func CustomHeaderHandler(fieldKey, header string) func(ctx *fiber.Ctx) error {
	return func(ctx *fiber.Ctx) error {
		log := zerolog.Ctx(ctx.UserContext())
		log.UpdateContext(func(c zerolog.Context) zerolog.Context {
			return c.Str(fieldKey, ctx.Get(header))
		})
		return ctx.Next()
	}
}

// AccessHandler returns a handler that call f after each request.
func AccessHandler(f func(ctx *fiber.Ctx, duration time.Duration)) fiber.Handler {
	return func(ctx *fiber.Ctx) error {
		start := time.Now()
		err := ctx.Next()
		f(ctx, time.Since(start))
		return err
	}
}

// Logger Level Method Helpers
func TraceFrom(ctx *fiber.Ctx) *zerolog.Event {
	return FromFiberCtx(ctx).Trace()
}

func DebugFrom(ctx *fiber.Ctx) *zerolog.Event {
	return FromFiberCtx(ctx).Debug()
}

func InfoFrom(ctx *fiber.Ctx) *zerolog.Event {
	return FromFiberCtx(ctx).Info()
}

func WarnFrom(ctx *fiber.Ctx) *zerolog.Event {
	return FromFiberCtx(ctx).Warn()
}

func ErrorFrom(ctx *fiber.Ctx) *zerolog.Event {
	return FromFiberCtx(ctx).Error()
}

func FatalFrom(ctx *fiber.Ctx) *zerolog.Event {
	return FromFiberCtx(ctx).Fatal()
}

func PanicFrom(ctx *fiber.Ctx) *zerolog.Event {
	return FromFiberCtx(ctx).Panic()
}

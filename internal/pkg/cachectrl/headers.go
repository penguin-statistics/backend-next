package cachectrl

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
)

func OptIn(ctx *fiber.Ctx, lastModifiedAt time.Time) {
	offset := time.Minute * 5
	OptInCustom(ctx, lastModifiedAt, offset)
}

func OptInCustom(ctx *fiber.Ctx, lastModifiedAt time.Time, offset time.Duration) {
	ctx.Set("Cache-Control", "public, max-age="+strconv.Itoa(int(offset.Seconds())))

	ctx.Response().Header.SetLastModified(lastModifiedAt)
}

// NotModified reports whether the response may be sent as 304 Not Modified, without a body: whether it has a
// Last-Modified (see OptIn) and the request is a conditional GET with an If-Modified-Since not before it. Responses
// without Last-Modified, such as personal ones, never may.
//
// fiber's ctx.Fresh() is not used: as of v2.50.0, it reports any request with If-Modified-Since (and without
// If-None-Match) as fresh, whatever Last-Modified is.
func NotModified(ctx *fiber.Ctx) bool {
	if ctx.Method() != fiber.MethodGet && ctx.Method() != fiber.MethodHead {
		return false
	}
	// If-Modified-Since is ignored along with If-None-Match (RFC 9110 13.1.3), which never matches: no response
	// has an ETag
	if ctx.Get(fiber.HeaderIfNoneMatch) != "" {
		return false
	}
	lastModified, err := http.ParseTime(string(ctx.Response().Header.Peek(fiber.HeaderLastModified)))
	if err != nil {
		return false
	}
	modifiedSince, err := http.ParseTime(ctx.Get(fiber.HeaderIfModifiedSince))
	// a date in the future is invalid, e.g. from another server whose clock is ahead
	if err != nil || modifiedSince.After(time.Now()) {
		return false
	}
	return !lastModified.After(modifiedSince)
}

func OptOut(ctx *fiber.Ctx) {
	ctx.Set("Cache-Control", "no-cache, no-store, must-revalidate")
	ctx.Set("Pragma", "no-cache")
	ctx.Set("Expires", "0")
}

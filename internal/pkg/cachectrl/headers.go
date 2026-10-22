package cachectrl

import (
	"bytes"
	"net/http"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/valyala/fasthttp"
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

var dateHeaderLine = []byte("\r\n" + fasthttp.HeaderDate + ": ")

// NotLaterThanDate tells if lastModified, sent as Last-Modified, is not later than the Date sent with the response.
// That Date is not time.Now(): fasthttp sends the time it formats once a second, which can be a second behind. It is
// read here from a blank response header, whose only header is that Date: the response is written later, so with
// that Date or a later one. Both headers are in whole seconds. False if the Date cannot be read.
func NotLaterThanDate(lastModified time.Time) bool {
	var header fasthttp.ResponseHeader
	raw := header.Header()
	i := bytes.Index(raw, dateHeaderLine)
	if i < 0 {
		return false
	}
	value := raw[i+len(dateHeaderLine):]
	if end := bytes.Index(value, []byte("\r\n")); end >= 0 {
		value = value[:end]
	}
	date, err := fasthttp.ParseHTTPDate(value)
	if err != nil {
		return false
	}
	return !lastModified.Truncate(time.Second).After(date)
}

package v2

import (
	"bytes"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/valyala/fasthttp"

	"exusiai.dev/backend-next/internal/model/cache"
	"exusiai.dev/backend-next/internal/pkg/cachectrl"
)

// lastModifiedTime returns the last modified time recorded for the cached response of key, or the zero time if
// there is none (the response was never calculated, or the time was flushed). It must be read before the response:
// if the response is recalculated in between, it then looks older than it is, and is sent in full again next time.
// Read after, an older response could be sent with the time of a newer one, and kept by clients as not modified.
func lastModifiedTime(key string) time.Time {
	var lastModified time.Time
	if err := cache.LastModifiedTime.Get(key, &lastModified); err != nil {
		return time.Time{}
	}
	return lastModified
}

// sendCacheable sends body as JSON, cacheable for 5 minutes and last modified at lastModified (see
// lastModifiedTime). Without a last modified time, body is sent without caching headers rather than with a time that
// could be newer than it. So is it with a last modified time later than the response's Date (a change recorded in the
// next second, see cache.SetLastModifiedTime), which Last-Modified must not be (RFC 9110 8.8.2.1).
func sendCacheable(ctx *fiber.Ctx, lastModified time.Time, body any) error {
	if lastModified.IsZero() || !notLaterThanDate(lastModified) {
		return ctx.JSON(body)
	}
	cachectrl.OptIn(ctx, lastModified)
	return ctx.JSON(body)
}

var dateHeaderLine = []byte("\r\n" + fasthttp.HeaderDate + ": ")

// notLaterThanDate tells if lastModified, sent as Last-Modified, is not later than the Date sent with the response.
// That Date is not time.Now(): fasthttp sends the time it formats once a second, which can be a second behind. It is
// read here from a blank response header, whose only header is that Date: the response is written later, so with
// that Date or a later one. Both headers are in whole seconds. False if the Date cannot be read.
func notLaterThanDate(lastModified time.Time) bool {
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

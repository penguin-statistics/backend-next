package v2

import (
	"bytes"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/valyala/fasthttp"

	"exusiai.dev/backend-next/internal/model/cache"
	"exusiai.dev/backend-next/internal/pkg/cachectrl"
)

// recordedTime is the last modified time recorded for the cached response of key when it was read, or the zero time
// if there was none (the response was never calculated, or the time was flushed).
type recordedTime struct {
	key  string
	time time.Time
}

// lastModifiedTime returns the last modified time recorded for the cached response of key. It must be read before the
// response: if the response is recalculated in between, it then looks older than it is, and is sent in full again next
// time. Read after, an older response could be sent with the time of a newer one, and kept by clients as not modified.
func lastModifiedTime(key string) recordedTime {
	return recordedTime{key: key, time: cache.GetLastModifiedTime(key)}
}

// sendCacheable sends body as JSON, cacheable for 5 minutes and last modified at lastModified (see
// lastModifiedTime), or 304 Not Modified without a body if the client's copy is not older. Without a last modified
// time, body is sent without caching headers rather than with a time that could be newer than it. So is it with a
// last modified time later than the response's Date (a change recorded in the next second, see
// cache.SetLastModifiedTime), which Last-Modified must not be (RFC 9110 8.8.2.1).
//
// The response may have been recalculated, changed, since lastModified was read (by this request, or concurrently):
// lastModified is then the time of the previous response, which a client's copy as recent is, so it is not answered
// with 304 then. The time is read again for that: as every change caches the response, then records a later time
// (cache.GetLastModifiedTime waits for it), it is the same only if body is the response recorded at lastModified.
func sendCacheable(ctx *fiber.Ctx, lastModified recordedTime, body any) error {
	if lastModified.time.IsZero() || !notLaterThanDate(lastModified.time) {
		return ctx.JSON(body)
	}
	cachectrl.OptIn(ctx, lastModified.time)
	if cachectrl.NotModified(ctx) && cache.GetLastModifiedTime(lastModified.key).Equal(lastModified.time) {
		return ctx.SendStatus(fiber.StatusNotModified)
	}
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

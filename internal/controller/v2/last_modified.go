package v2

import (
	"time"

	"github.com/gofiber/fiber/v2"

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
// could be newer than it. So is it with a last modified time in the future (see cache.SetLastModifiedTime), which
// Last-Modified must not be (RFC 9110 8.8.2.1).
func sendCacheable(ctx *fiber.Ctx, lastModified time.Time, body any) error {
	if lastModified.IsZero() || lastModified.After(time.Now()) {
		return ctx.JSON(body)
	}
	cachectrl.OptIn(ctx, lastModified)
	return ctx.JSON(body)
}

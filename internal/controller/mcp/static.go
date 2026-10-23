package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// static is a response made once, at startup, and sent as it is: requests copy its bytes, with no work of their own.
type static struct {
	contentType  string
	cacheControl string
	body         []byte
	// etag is the weak entity tag of contentType and body: W/ and the first 64 bits of their SHA-256, in hex. Weak, as
	// the compress middleware sends it unchanged on the gzip and br responses too, and a strong tag has to differ
	// between encodings (RFC 9110, 8.8.3.3). With the type, the same bytes sent as another type get another tag.
	etag string
}

func newStatic(contentType, cacheControl string, body []byte) *static {
	sum := sha256.Sum256(append([]byte(contentType+"\x00"), body...))
	return &static{contentType: contentType, cacheControl: cacheControl, body: body, etag: `W/"` + hex.EncodeToString(sum[:8]) + `"`}
}

// send answers a GET or a HEAD with s, or with 304 Not Modified to a request whose If-None-Match has its entity tag.
func (s *static) send(c *fiber.Ctx) error {
	c.Set(fiber.HeaderContentType, s.contentType)
	c.Set(fiber.HeaderCacheControl, s.cacheControl)
	c.Set(fiber.HeaderETag, s.etag)
	if etagMatches(c.Get(fiber.HeaderIfNoneMatch), s.etag) {
		c.Status(fiber.StatusNotModified)
		return nil
	}
	return c.Send(s.body)
}

// etagMatches tells whether the If-None-Match header ifNoneMatch has etag, or is "*", comparing the tags weakly, as RFC
// 9110 (13.1.2) has it for If-None-Match: a tag matches with or without W/.
func etagMatches(ifNoneMatch, etag string) bool {
	opaque := strings.TrimPrefix(etag, "W/")
	for _, tag := range strings.Split(ifNoneMatch, ",") {
		tag = strings.TrimSpace(tag)
		if tag == "*" || strings.TrimPrefix(tag, "W/") == opaque {
			return true
		}
	}
	return false
}

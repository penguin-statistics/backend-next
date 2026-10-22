package seo

import (
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"

	"exusiai.dev/backend-next/internal/pkg/cachectrl"
)

// stamps gives the data the responses are made of (the catalog, the matrix of each server, the shell) the times their
// Last-Modified is the latest of. The times of the data themselves are not enough: they come from different clocks,
// and Last-Modified has a precision of seconds. A change in the same second as the latest time (the catalog replaced
// in the second the matrix was published), or with an older time (the matrix of another server, for the index of the
// sitemaps; the index.html of a release deployed before the matrix was published, for the pages), would not change
// the Last-Modified, and clients having the previous response be answered that it is not modified. So data replacing
// other data is given its own time, but in a later second than any time given before: changes in quick succession
// can thus be given times a few seconds ahead of now, as by cache.SetLastModifiedTime. The data at hand after a start
// keep their own times, so that a start moves no Last-Modified.
type stamps struct {
	mu sync.Mutex
	// bySlot holds the data seen last in each slot (catalogSlot, matrixSlot, shellSlot), and the time it was given
	bySlot map[any]stamp
	// latest is the latest time given
	latest time.Time
}

type stamp struct {
	of any
	at time.Time
}

// the slots of the data
type (
	catalogSlot struct{}
	matrixSlot  struct{ server string }
	shellSlot   struct{}
)

// at returns the time of of (comparable: a pointer to the data, or its time if that tells it apart), the data in
// slot, whose own time is t: t if it is the first data seen in slot, the time given before if it is the same, else t
// but in a later second than the latest time given. Zero if t is.
func (s *stamps) at(slot, of any, t time.Time) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, ok := s.bySlot[slot]
	if ok && prev.of == of {
		return prev.at
	}
	if ok && !t.IsZero() && !t.Truncate(time.Second).After(s.latest.Truncate(time.Second)) {
		t = s.latest.Truncate(time.Second).Add(time.Second)
	}
	if s.bySlot == nil {
		s.bySlot = make(map[any]stamp)
	}
	s.bySlot[slot] = stamp{of: of, at: t}
	if t.After(s.latest) {
		s.latest = t
	}
	return t
}

// setLastModified sets the Last-Modified of the response to t, unless t is later than the Date of the response, which
// Last-Modified must not be (RFC 9110 8.8.2.1): a change given a later second (see stamps and
// cache.SetLastModifiedTime) is sent without one until then. It tells whether it did, and so whether the response may
// be answered as not modified.
func setLastModified(c *fiber.Ctx, t time.Time) bool {
	if !cachectrl.NotLaterThanDate(t) {
		return false
	}
	c.Response().Header.SetLastModified(t)
	return true
}

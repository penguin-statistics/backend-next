package cache

import (
	"crypto/sha256"
	"sync"
	"time"

	"github.com/goccy/go-json"
	"github.com/rs/zerolog/log"
)

// lastModifiedRecord is the last modified time recorded for a key, and the digest of the value it is for
type lastModifiedRecord struct {
	digest [sha256.Size]byte
	time   time.Time
}

var (
	// lastModifiedRecords holds the last modified time of each LastModifiedTime key, which survives flushes of
	// LastModifiedTime; guarded by lastModifiedMu
	lastModifiedMu      sync.Mutex
	lastModifiedRecords = make(map[string]lastModifiedRecord)
)

// SetLastModifiedTime caches value by calling cacheValue, and records now as the last modified time of key, for value:
// unless value is the same as the one the recorded time is for, which is then kept. The last modified time of a value
// that expires and is recalculated unchanged thus stays the same, so that clients revalidating their copy with
// If-Modified-Since can be answered with 304 Not Modified.
//
// cacheValue is called while recording, so that of concurrent recalculations, the value cached last is the one
// recorded last: otherwise an older value could stay cached with the time of a newer one, and clients holding it would
// be answered that it is not modified, even after the newer value is cached, until the next change. cacheValue is nil
// for a value MutexGetSet has cached, which caches a recalculated value only if no other was cached meanwhile.
func SetLastModifiedTime(key string, value any, cacheValue func()) {
	b, err := json.Marshal(value)
	if err != nil {
		// whether value changed is unknown: record it as changed (no value has the zero digest)
		log.Warn().Err(err).Str("evt.name", "cache.last_modified.digest").Str("key", key).Msg("failed to marshal the value to compare it with the previous one")
	}
	var digest [sha256.Size]byte
	if err == nil {
		digest = sha256.Sum256(b)
	}

	lastModifiedMu.Lock()
	defer lastModifiedMu.Unlock()
	if cacheValue != nil {
		cacheValue()
	}
	previous, ok := lastModifiedRecords[key]
	if ok && err == nil && previous.digest == digest {
		LastModifiedTime.Set(key, previous.time, 0)
		return
	}
	now := time.Now()
	// Last-Modified has a precision of seconds: a change in the same second as the previous one is recorded in the
	// next second, or clients having the previous value would be answered that it is not modified. Changes in quick
	// succession can thus be recorded a few seconds ahead of now.
	if ok && !now.Truncate(time.Second).After(previous.time.Truncate(time.Second)) {
		now = previous.time.Truncate(time.Second).Add(time.Second)
	}
	lastModifiedRecords[key] = lastModifiedRecord{digest: digest, time: now}
	LastModifiedTime.Set(key, now, 0)
}

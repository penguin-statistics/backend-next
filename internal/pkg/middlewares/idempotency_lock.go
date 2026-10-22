package middlewares

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel/trace"
)

// The lock of an idempotency key is taken as redsync took it with WithExpiry(time.Minute), WithTries(5) and
// WithRetryDelay(250 ms): SET NX with a token of the request, released only by the holder of the token. The keys and
// the saved response are those of the redsync version, so during a rolling deploy each version respects the locks
// and replays the responses of the other.
const (
	idempotencyLockExpiry     = time.Minute
	idempotencyLockTries      = 5
	idempotencyLockRetryDelay = 250 * time.Millisecond
)

// the constant arguments of acquireIdempotencyScript, boxed once
var (
	idempotencyLockExpiryArg interface{} = strconv.FormatInt(idempotencyLockExpiry.Milliseconds(), 10)
	idempotencyFirstTryArg   interface{} = "1"
	idempotencyLaterTryArg   interface{} = "0"
)

var (
	errIdempotencyLockTaken = errors.New("idempotency lock is taken by another request")
	errIdempotencyLockLost  = errors.New("idempotency lock has expired or been taken by another request")
)

// acquireIdempotencyScript returns the saved response, or 1 when it took the lock, or 0 when another request holds
// the lock. KEYS: response, lock. ARGV: token, lock expiry in ms, "1" on the first try.
//
// A lock that already holds the token was taken by this request: go-redis runs a command again when its reply is
// lost, so a try whose first run took the lock must not count the lock as another request's.
//
// It runs the GET, lock, GET sequence it replaces in one round trip: the first try looks for the response before
// taking the lock, and every try looks for it after taking the lock. A retry that finds the response keeps the lock
// until the response is sent, and a retry does not look for the response before taking the lock, so duplicates that
// arrive together still queue on the lock.
var acquireIdempotencyScript = redis.NewScript(`
if ARGV[3] == "1" then
	local response = redis.call("GET", KEYS[1])
	if response then
		return response
	end
end
if not redis.call("SET", KEYS[2], ARGV[1], "NX", "PX", ARGV[2]) and redis.call("GET", KEYS[2]) ~= ARGV[1] then
	return 0
end
local response = redis.call("GET", KEYS[1])
if response then
	return response
end
return 1
`)

// saveIdempotencyScript saves the response and then releases the lock if the token still holds it, returning 1 if
// it did. KEYS: response, lock. ARGV: response, lifetime in ms (0 for none), token.
var saveIdempotencyScript = redis.NewScript(`
if ARGV[2] == "0" then
	redis.call("SET", KEYS[1], ARGV[1])
else
	redis.call("SET", KEYS[1], ARGV[1], "PX", ARGV[2])
end
if redis.call("GET", KEYS[2]) == ARGV[3] then
	return redis.call("DEL", KEYS[2])
end
return 0
`)

// releaseIdempotencyScript releases the lock if the token holds it, returning 1 if it did. KEYS: lock. ARGV: token.
var releaseIdempotencyScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
	return redis.call("DEL", KEYS[1])
end
return 0
`)

// idempotencyLock is the lock of an idempotency key, run on the request's goroutine with one round trip per call.
type idempotencyLock struct {
	redis redis.Scripter
	keys  [2]string   // the response, the lock
	token interface{} // a string, boxed once for the scripts
	// ctx is the context of the scripts (nil: context.Background()): it carries the request's span if it is sampled
	// (spanOnly), so that the scripts are traced in the request's trace
	ctx context.Context
}

// context returns the context of the scripts.
func (l *idempotencyLock) context() context.Context {
	if l.ctx == nil {
		return context.Background()
	}
	return l.ctx
}

// spanOnly returns a context that carries the span of ctx and nothing else of it, if the span is recording, and
// context.Background() otherwise, without an allocation: the lock's scripts are traced as children of the request's
// span, but do not end with the request, as they did not before (the lock is released after the handler).
func spanOnly(ctx context.Context) context.Context {
	if span := trace.SpanFromContext(ctx); span.IsRecording() {
		return trace.ContextWithSpan(context.Background(), span)
	}
	return context.Background()
}

// idempotencyLockTokenPrefix makes the tokens of this process differ from those of other processes, and
// idempotencyLockTokenCount the tokens of this process from one another, without reading 16 random bytes per request
// as redsync did.
var (
	idempotencyLockTokenPrefix = func() string {
		var b [16]byte
		_, _ = rand.Read(b[:])
		return base64.StdEncoding.EncodeToString(b[:]) + ":"
	}()
	idempotencyLockTokenCount atomic.Uint64
)

func newIdempotencyLockToken() string {
	var b [48]byte
	token := append(b[:0], idempotencyLockTokenPrefix...)
	return string(strconv.AppendUint(token, idempotencyLockTokenCount.Add(1), 36))
}

// acquire returns the saved response if there is one and whether it took the lock. Only a retry takes the lock when
// it finds the response: on the first try the script looks for the response before taking the lock, and it cannot
// appear in between.
func (l *idempotencyLock) acquire(first bool) (response []byte, locked bool, err error) {
	firstArg := idempotencyLaterTryArg
	if first {
		firstArg = idempotencyFirstTryArg
	}
	res, err := acquireIdempotencyScript.Run(l.context(), l.redis, l.keys[:], l.token, idempotencyLockExpiryArg, firstArg).Result()
	if err != nil {
		return nil, false, err
	}
	switch res := res.(type) {
	case string:
		return []byte(res), !first, nil
	case int64:
		return nil, res == 1, nil
	}
	return nil, false, errors.New("idempotency lock: unexpected reply")
}

// save saves the response for lifetime and releases the lock, reporting whether the lock was still held.
func (l *idempotencyLock) save(response []byte, lifetime time.Duration) (released bool, err error) {
	n, err := saveIdempotencyScript.Run(l.context(), l.redis, l.keys[:], response, lifetimeMs(lifetime), l.token).Int()
	return n == 1, err
}

// release releases the lock, reporting whether it was still held.
func (l *idempotencyLock) release() (bool, error) {
	n, err := releaseIdempotencyScript.Run(l.context(), l.redis, l.keys[1:], l.token).Int()
	return n == 1, err
}

// unlock releases the lock and logs when it was not held any more.
func (l *idempotencyLock) unlock(key string) {
	released, err := l.release()
	if released {
		return
	}
	if err == nil {
		err = errIdempotencyLockLost
	}
	log.Err(err).
		Str("evt.name", "http.idempotency.unlock.failed").
		Str("key", key).
		Msg("failed to unlock idempotency key.")
}

// lifetimeMs formats lifetime as go-redis's SET does: whole milliseconds, at least 1, or 0 for no expiry.
func lifetimeMs(lifetime time.Duration) string {
	if lifetime <= 0 {
		return "0"
	}
	if lifetime < time.Millisecond {
		return "1"
	}
	return strconv.FormatInt(lifetime.Milliseconds(), 10)
}

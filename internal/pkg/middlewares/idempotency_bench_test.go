//go:build unix

package middlewares

import (
	"bytes"
	"context"
	"io"
	"net"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"

	"exusiai.dev/gommon/constant"
	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
)

// countingHook counts the commands a Redis client sends and, while recording, the ones sent from a goroutine other
// than the one serving the request.
type countingHook struct {
	cmds atomic.Int64

	recording   atomic.Bool
	mu          sync.Mutex
	requestG    uint64
	otherGoroID map[uint64]struct{}
}

func (h *countingHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *countingHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		h.cmds.Add(1)
		if h.recording.Load() {
			h.record()
		}
		return next(ctx, cmd)
	}
}

func (h *countingHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		h.cmds.Add(int64(len(cmds)))
		if h.recording.Load() {
			h.record()
		}
		return next(ctx, cmds)
	}
}

func (h *countingHook) record() {
	g := goroutineID()
	h.mu.Lock()
	defer h.mu.Unlock()
	if g != h.requestG {
		h.otherGoroID[g] = struct{}{}
	}
}

// cpuTime returns the CPU time the process has used, in all its threads.
func cpuTime() int64 {
	var ru syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	return ru.Utime.Nano() + ru.Stime.Nano()
}

func goroutineID() uint64 {
	var buf [64]byte
	b := buf[:runtime.Stack(buf[:], false)]
	b = bytes.TrimPrefix(b, []byte("goroutine "))
	id, _ := strconv.ParseUint(string(b[:bytes.IndexByte(b, ' ')]), 10, 64)
	return id
}

// BenchmarkIdempotency serves a POST through the idempotency middleware and a trivial handler on the benchmark's
// goroutine, against PENGUIN_TEST_REDIS_URL (use a database of its own, e.g. redis://127.0.0.1:16379/12). miss is
// a request with a new key, hit a duplicate of a saved one. Besides the time and allocations per request it
// reports the CPU time of the process (getrusage, so it leaves out Redis but counts every thread of the test), the
// Redis commands sent (each one a round trip) and the goroutines spawned to send them.
func BenchmarkIdempotency(b *testing.B) {
	client := newTestIdempotencyRedis(b)
	hook := &countingHook{}
	client.AddHook(hook)
	prefix := testIdempotencyPrefix(b, client)
	if err := client.Ping(context.Background()).Err(); err != nil {
		b.Fatal(err)
	}

	prev := log.Logger
	log.Logger = zerolog.New(io.Discard).Level(zerolog.InfoLevel)
	b.Cleanup(func() { log.Logger = prev })

	app := fiber.New()
	app.Post("/", Idempotency(testIdempotencyConfig(client, prefix)), func(c *fiber.Ctx) error {
		c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		return c.SendString(`{"reportHash":"0123456789abcdef0123456789abcdef-0123456789"}`)
	})
	handler := app.Handler()
	keyBase := prefix[len("test-idempotency-"):]

	var fctx fasthttp.RequestCtx
	fctx.Init(&fasthttp.Request{}, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)}, nil)
	serve := func(b *testing.B, key string) {
		fctx.Request.Reset()
		fctx.Response.Reset()
		fctx.Request.Header.SetMethod(fiber.MethodPost)
		fctx.Request.SetRequestURI("/")
		fctx.Request.Header.Set(constant.IdempotencyKeyHeader, key)
		handler(&fctx)
		if status := fctx.Response.StatusCode(); status != fiber.StatusOK {
			b.Fatalf("status %d: %s", status, fctx.Response.Body())
		}
	}

	// report the goroutines that sent commands in n more requests, outside of the timing
	reportGoroutines := func(b *testing.B, n int, key func(i int) string) {
		hook.mu.Lock()
		hook.requestG, hook.otherGoroID = goroutineID(), map[uint64]struct{}{}
		hook.mu.Unlock()
		hook.recording.Store(true)
		for i := 0; i < n; i++ {
			serve(b, key(i))
		}
		hook.recording.Store(false)
		b.ReportMetric(float64(len(hook.otherGoroID))/float64(n), "redis-goroutines/op")
	}

	var runs int
	b.Run("miss", func(b *testing.B) {
		runs++
		run := strconv.Itoa(runs) + "x"
		b.ReportAllocs()
		hook.cmds.Store(0)
		b.ResetTimer()
		cpu := cpuTime()
		for i := 0; i < b.N; i++ {
			serve(b, keyBase+"miss"+run+strconv.Itoa(i))
		}
		b.StopTimer()
		b.ReportMetric(float64(cpuTime()-cpu)/float64(b.N), "cpu-ns/op")
		b.ReportMetric(float64(hook.cmds.Load())/float64(b.N), "redis-cmds/op")
		reportGoroutines(b, 100, func(i int) string { return keyBase + "missg" + run + strconv.Itoa(i) })
	})

	b.Run("hit", func(b *testing.B) {
		key := keyBase + "hit"
		serve(b, key)
		b.ReportAllocs()
		hook.cmds.Store(0)
		b.ResetTimer()
		cpu := cpuTime()
		for i := 0; i < b.N; i++ {
			serve(b, key)
		}
		b.StopTimer()
		b.ReportMetric(float64(cpuTime()-cpu)/float64(b.N), "cpu-ns/op")
		b.ReportMetric(float64(hook.cmds.Load())/float64(b.N), "redis-cmds/op")
		reportGoroutines(b, 100, func(int) string { return key })
	})
}

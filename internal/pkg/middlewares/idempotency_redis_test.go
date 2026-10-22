package middlewares

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/go-redsync/redsync/v4"
	"github.com/go-redsync/redsync/v4/redis/goredis/v9"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"exusiai.dev/backend-next/internal/pkg/pgerr"
)

// The tests below run the idempotency middleware against PENGUIN_TEST_REDIS_URL, a disposable Redis (see
// internal/workers/calcwkr/lock_test.go). Each test keeps its keys under a prefix of its own.

const testIdempotencyLifetime = 2 * time.Hour

var testIdempotencyLockedMessage = "failed to lock idempotency key: idempoency key is locked by another request; are you sending the same request concurrently or retrying with little or no backoff?"

func newTestIdempotencyRedis(t testing.TB) *redis.Client {
	t.Helper()
	url := os.Getenv("PENGUIN_TEST_REDIS_URL")
	if url == "" {
		t.Skip("PENGUIN_TEST_REDIS_URL is not set")
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(opts)
	t.Cleanup(func() { client.Close() })
	return client
}

// testIdempotencyPrefix returns a key prefix of the test's own and deletes its keys when the test ends.
func testIdempotencyPrefix(t testing.TB, client *redis.Client) string {
	t.Helper()
	prefix := "test-idempotency-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	t.Cleanup(func() {
		ctx := context.Background()
		for _, pattern := range []string{prefix + ":*", "mutex:idempotency-request:" + prefix + "*"} {
			iter := client.Scan(ctx, 0, pattern, 1000).Iterator()
			for iter.Next(ctx) {
				client.Del(ctx, iter.Val())
			}
		}
	})
	return prefix
}

func testIdempotencyConfig(client *redis.Client, prefix string) *IdempotencyConfig {
	return &IdempotencyConfig{
		Lifetime:            testIdempotencyLifetime,
		KeyHeader:           constant.IdempotencyKeyHeader,
		KeepResponseHeaders: []string{fiber.HeaderContentType, "X-Kept"},
		Redis:               client,
		KeyPrefix:           prefix,
	}
}

// idempotencyTestApp serves POST / with the middleware in front of handler and records the errors the
// middleware chain returns.
type idempotencyTestApp struct {
	app *fiber.App

	mu   sync.Mutex
	errs []error
}

func newIdempotencyTestApp(config *IdempotencyConfig, handler fiber.Handler) *idempotencyTestApp {
	a := &idempotencyTestApp{}
	a.app = fiber.New(fiber.Config{ErrorHandler: func(c *fiber.Ctx, err error) error {
		a.mu.Lock()
		a.errs = append(a.errs, err)
		a.mu.Unlock()
		status := fiber.StatusInternalServerError
		var pe *pgerr.PenguinError
		if errors.As(err, &pe) {
			status = pe.StatusCode
		}
		return c.Status(status).SendString(err.Error())
	}})
	a.app.Post("/", Idempotency(config), handler)
	return a
}

type testIdempotencyResponse struct {
	status int
	header http.Header
	body   string
}

func (a *idempotencyTestApp) post(t *testing.T, key string) testIdempotencyResponse {
	t.Helper()
	req := httptest.NewRequest(fiber.MethodPost, "/", nil)
	req.Header.Set(constant.IdempotencyKeyHeader, key)
	// no test timeout: a duplicate waits for the lock for about a second
	resp, err := a.app.Test(req, -1)
	if err != nil {
		t.Error(err)
		return testIdempotencyResponse{}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Error(err)
	}
	return testIdempotencyResponse{status: resp.StatusCode, header: resp.Header, body: string(body)}
}

func (a *idempotencyTestApp) errors() []error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]error(nil), a.errs...)
}

// captureIdempotencyLogs sends the global logger to a buffer at level and returns the evt.name and level of each
// line logged.
func captureIdempotencyLogs(t *testing.T, level zerolog.Level) func() map[string][]string {
	t.Helper()
	var (
		mu  sync.Mutex
		buf bytes.Buffer
	)
	prev := log.Logger
	log.Logger = zerolog.New(syncWriter{&mu, &buf}).Level(level)
	t.Cleanup(func() { log.Logger = prev })
	return func() map[string][]string {
		mu.Lock()
		defer mu.Unlock()
		levels := map[string][]string{}
		sc := bufio.NewScanner(bytes.NewReader(buf.Bytes()))
		for sc.Scan() {
			var line struct {
				Level string `json:"level"`
				Event string `json:"evt.name"`
			}
			if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
				t.Fatalf("log line %q: %v", sc.Text(), err)
			}
			levels[line.Event] = append(levels[line.Event], line.Level)
		}
		return levels
	}
}

type syncWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (w syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.w.Write(p)
}

func testIdempotencyHandler(calls *atomic.Int32) fiber.Handler {
	return func(c *fiber.Ctx) error {
		n := calls.Add(1)
		c.Set("X-Kept", "kept")
		c.Set("X-Dropped", "dropped")
		c.Status(fiber.StatusCreated)
		return c.JSON(fiber.Map{"call": n})
	}
}

func TestIdempotencyReplaysTheSavedResponse(t *testing.T) {
	client := newTestIdempotencyRedis(t)
	prefix := testIdempotencyPrefix(t, client)
	var calls atomic.Int32
	app := newIdempotencyTestApp(testIdempotencyConfig(client, prefix), testIdempotencyHandler(&calls))
	key := prefix[len("test-idempotency-"):] + "replay"

	first := app.post(t, key)
	if first.status != fiber.StatusCreated || first.body != `{"call":1}` {
		t.Fatalf("first: %d %q, want 201 {\"call\":1}", first.status, first.body)
	}
	if got := first.header.Get(constant.IdempotencyHeader); got != "saved" {
		t.Errorf("first: %s = %q, want saved", constant.IdempotencyHeader, got)
	}

	ctx := context.Background()
	if ttl := client.PTTL(ctx, prefix+":"+key).Val(); ttl <= testIdempotencyLifetime-time.Minute || ttl > testIdempotencyLifetime {
		t.Errorf("saved response expires in %v, want %v", ttl, testIdempotencyLifetime)
	}
	if n := client.Exists(ctx, "mutex:idempotency-request:"+key).Val(); n != 0 {
		t.Errorf("lock still held after the request")
	}

	second := app.post(t, key)
	if second.status != fiber.StatusCreated || second.body != `{"call":1}` {
		t.Fatalf("second: %d %q, want the saved 201 {\"call\":1}", second.status, second.body)
	}
	if got := second.header.Get(constant.IdempotencyHeader); got != "hit" {
		t.Errorf("second: %s = %q, want hit", constant.IdempotencyHeader, got)
	}
	if got := second.header.Get("X-Kept"); got != "kept" {
		t.Errorf("second: X-Kept = %q, want the kept header", got)
	}
	if got := second.header.Get("X-Dropped"); got != "" {
		t.Errorf("second: X-Dropped = %q, want it left out", got)
	}
	// the replay appends the saved Content-Type to the default one of fasthttp
	if got, want := second.header.Get(fiber.HeaderContentType), "text/plain; charset=utf-8, "+first.header.Get(fiber.HeaderContentType); got != want {
		t.Errorf("second: Content-Type = %q, want %q", got, want)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("handler ran %d times, want once", n)
	}
}

func TestIdempotencyHoldsTheLockForAMinute(t *testing.T) {
	client := newTestIdempotencyRedis(t)
	prefix := testIdempotencyPrefix(t, client)
	key := prefix[len("test-idempotency-"):] + "expiry"
	var ttl time.Duration
	app := newIdempotencyTestApp(testIdempotencyConfig(client, prefix), func(c *fiber.Ctx) error {
		ttl = client.PTTL(c.Context(), "mutex:idempotency-request:"+key).Val()
		return c.SendString("ok")
	})

	if resp := app.post(t, key); resp.status != fiber.StatusOK {
		t.Fatalf("status %d, want 200", resp.status)
	}
	if ttl <= 55*time.Second || ttl > time.Minute {
		t.Errorf("lock expires in %v while the handler runs, want a minute", ttl)
	}
}

// awaitIdempotencyHandler waits for the first request to enter the handler, failing the test with its response if it
// answered without entering it (e.g. it could not take the lock), and failing it after 10 s rather than hanging.
func awaitIdempotencyHandler(t *testing.T, entered <-chan struct{}, firstDone <-chan testIdempotencyResponse) {
	t.Helper()
	select {
	case <-entered:
	case first := <-firstDone:
		t.Fatalf("first request answered %d %q without entering the handler", first.status, first.body)
	case <-time.After(10 * time.Second):
		t.Fatal("first request did not enter the handler within 10 s")
	}
}

// A duplicate that arrives while the first request runs waits for its lock, retrying 4 times 250 ms apart, and
// answers with the saved response once the first one is done.
func TestIdempotencyDuplicateWaitsForTheRequestInFlight(t *testing.T) {
	client := newTestIdempotencyRedis(t)
	prefix := testIdempotencyPrefix(t, client)
	key := prefix[len("test-idempotency-"):] + "inflight"
	var calls atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	app := newIdempotencyTestApp(testIdempotencyConfig(client, prefix), func(c *fiber.Ctx) error {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		return c.SendString("first")
	})

	firstDone := make(chan testIdempotencyResponse, 1)
	go func() { firstDone <- app.post(t, key) }()
	awaitIdempotencyHandler(t, entered, firstDone)

	time.AfterFunc(400*time.Millisecond, func() { close(release) })
	start := time.Now()
	second := app.post(t, key)
	elapsed := time.Since(start)
	first := <-firstDone

	if first.status != fiber.StatusOK || first.body != "first" || first.header.Get(constant.IdempotencyHeader) != "saved" {
		t.Errorf("first: %d %q %s=%q", first.status, first.body, constant.IdempotencyHeader, first.header.Get(constant.IdempotencyHeader))
	}
	if second.status != fiber.StatusOK || second.body != "first" || second.header.Get(constant.IdempotencyHeader) != "hit" {
		t.Errorf("second: %d %q %s=%q, want the saved response", second.status, second.body, constant.IdempotencyHeader, second.header.Get(constant.IdempotencyHeader))
	}
	if elapsed < 400*time.Millisecond || elapsed > 900*time.Millisecond {
		t.Errorf("second answered after %v, want at the first retry after the first request finished (500 ms)", elapsed)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("handler ran %d times, want once", n)
	}
}

// A duplicate gives up with a 500 when the first request still runs after its 5 tries, about a second.
func TestIdempotencyDuplicateFailsWhenTheRequestInFlightTakesTooLong(t *testing.T) {
	client := newTestIdempotencyRedis(t)
	prefix := testIdempotencyPrefix(t, client)
	key := prefix[len("test-idempotency-"):] + "toolong"
	var calls atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	app := newIdempotencyTestApp(testIdempotencyConfig(client, prefix), func(c *fiber.Ctx) error {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		return c.SendString("first")
	})
	logs := captureIdempotencyLogs(t, zerolog.InfoLevel)

	firstDone := make(chan testIdempotencyResponse, 1)
	go func() { firstDone <- app.post(t, key) }()
	awaitIdempotencyHandler(t, entered, firstDone)

	start := time.Now()
	second := app.post(t, key)
	elapsed := time.Since(start)
	close(release)
	first := <-firstDone

	if second.status != fiber.StatusInternalServerError {
		t.Errorf("second: status %d, want 500", second.status)
	}
	errs := app.errors()
	if len(errs) != 1 {
		t.Fatalf("errors %v, want the lock error", errs)
	}
	var pe *pgerr.PenguinError
	if !errors.As(errs[0], &pe) || pe.StatusCode != fiber.StatusInternalServerError || pe.ErrorCode != pgerr.CodeInternalError || pe.Message != testIdempotencyLockedMessage {
		t.Errorf("error %#v, want the internal lock error", errs[0])
	}
	if elapsed < 950*time.Millisecond || elapsed > 1500*time.Millisecond {
		t.Errorf("second gave up after %v, want after 4 delays of 250 ms", elapsed)
	}
	if first.status != fiber.StatusOK || first.body != "first" {
		t.Errorf("first: %d %q", first.status, first.body)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("handler ran %d times, want once", n)
	}
	levels := logs()
	if got := levels["http.idempotency.lock.failed"]; len(got) != 1 || got[0] != zerolog.LevelErrorValue {
		t.Errorf("http.idempotency.lock.failed: logged at %v, want once at error", got)
	}
	if got := levels["http.idempotency.lock"]; len(got) != 2 {
		t.Errorf("http.idempotency.lock: logged %d times, want once per request", len(got))
	}
}

// Many duplicates at once run the handler once. Each of the others answers with the saved response once it gets the
// lock, or with the lock error when it does not get it within its 5 tries: a duplicate takes the lock before it looks
// for the saved response, so duplicates that arrive together queue on the lock even after the response is saved.
func TestIdempotencyConcurrentDuplicatesRunTheHandlerOnce(t *testing.T) {
	client := newTestIdempotencyRedis(t)
	prefix := testIdempotencyPrefix(t, client)
	key := prefix[len("test-idempotency-"):] + "concurrent"
	var calls atomic.Int32
	app := newIdempotencyTestApp(testIdempotencyConfig(client, prefix), func(c *fiber.Ctx) error {
		calls.Add(1)
		time.Sleep(100 * time.Millisecond)
		return c.SendString("once")
	})

	const n = 16
	var wg sync.WaitGroup
	resps := make([]testIdempotencyResponse, n)
	for i := range resps {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resps[i] = app.post(t, key)
		}(i)
	}
	wg.Wait()

	saved, hit, refused := 0, 0, 0
	for i, resp := range resps {
		switch {
		case resp.status == fiber.StatusOK && resp.body == "once" && resp.header.Get(constant.IdempotencyHeader) == "saved":
			saved++
		case resp.status == fiber.StatusOK && resp.body == "once" && resp.header.Get(constant.IdempotencyHeader) == "hit":
			hit++
		case resp.status == fiber.StatusInternalServerError:
			refused++
		default:
			t.Errorf("request %d: %d %q", i, resp.status, resp.body)
		}
	}
	var pe *pgerr.PenguinError
	for _, err := range app.errors() {
		if !errors.As(err, &pe) || pe.Message != testIdempotencyLockedMessage {
			t.Errorf("error %v, want the lock error", err)
		}
	}
	if saved != 1 || saved+hit+refused != n {
		t.Errorf("%d saved, %d hits, %d refused, want 1 saved", saved, hit, refused)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("handler ran %d times, want once", got)
	}
	t.Logf("%d saved, %d hits, %d refused", saved, hit, refused)
}

// The request releases only its own lock: when its lock has expired and another request has taken the key, the
// response is still saved but the other request's lock is left alone.
func TestIdempotencyReleasesOnlyItsOwnLock(t *testing.T) {
	client := newTestIdempotencyRedis(t)
	prefix := testIdempotencyPrefix(t, client)
	key := prefix[len("test-idempotency-"):] + "owner"
	lockKey := "mutex:idempotency-request:" + key
	app := newIdempotencyTestApp(testIdempotencyConfig(client, prefix), func(c *fiber.Ctx) error {
		// the lock expired and another request took it
		client.Set(c.Context(), lockKey, "another-token", time.Minute)
		return c.SendString("ok")
	})
	logs := captureIdempotencyLogs(t, zerolog.InfoLevel)

	resp := app.post(t, key)
	if resp.status != fiber.StatusOK || resp.header.Get(constant.IdempotencyHeader) != "saved" {
		t.Errorf("status %d %s=%q, want 200 saved", resp.status, constant.IdempotencyHeader, resp.header.Get(constant.IdempotencyHeader))
	}
	ctx := context.Background()
	if got := client.Get(ctx, lockKey).Val(); got != "another-token" {
		t.Errorf("lock holds %q, want the other request's token kept", got)
	}
	if got := client.Exists(ctx, prefix+":"+key).Val(); got != 1 {
		t.Errorf("response not saved")
	}
	if got := logs()["http.idempotency.unlock.failed"]; len(got) != 1 || got[0] != zerolog.LevelErrorValue {
		t.Errorf("http.idempotency.unlock.failed: logged at %v, want once at error", got)
	}
}

// replayFirstLockHook runs the first command that takes the lock of key twice and keeps the second reply, as go-redis
// does when the reply of the first run is lost.
type replayFirstLockHook struct {
	lockKey  string
	replayed atomic.Bool
}

func (h *replayFirstLockHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *replayFirstLockHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func (h *replayFirstLockHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		name := cmd.Name()
		if (name == "evalsha" || name == "eval") && len(cmd.Args()) > 4 && cmd.Args()[4] == h.lockKey &&
			h.replayed.CompareAndSwap(false, true) {
			if err := next(ctx, cmd); err != nil {
				return err
			}
		}
		return next(ctx, cmd)
	}
}

// A try whose reply is lost and that go-redis runs again finds the lock holding its own token: it took the lock, so
// the request runs instead of failing as locked by another request.
func TestIdempotencyLockRunAgainAfterALostReply(t *testing.T) {
	client := newTestIdempotencyRedis(t)
	prefix := testIdempotencyPrefix(t, client)
	key := prefix[len("test-idempotency-"):] + "lostreply"
	lockKey := "mutex:idempotency-request:" + key
	hook := &replayFirstLockHook{lockKey: lockKey}
	client.AddHook(hook)
	var calls atomic.Int32
	app := newIdempotencyTestApp(testIdempotencyConfig(client, prefix), testIdempotencyHandler(&calls))

	resp := app.post(t, key)
	if !hook.replayed.Load() {
		t.Fatalf("the lock command was not run again (status %d %q)", resp.status, resp.body)
	}
	if resp.status != fiber.StatusCreated || resp.header.Get(constant.IdempotencyHeader) != "saved" || calls.Load() != 1 {
		t.Errorf("status %d %s=%q after %d handler calls, want 201 saved after 1", resp.status, constant.IdempotencyHeader, resp.header.Get(constant.IdempotencyHeader), calls.Load())
	}
	if got := client.Exists(context.Background(), lockKey).Val(); got != 0 {
		t.Error("the lock is still held")
	}
	if resp := app.post(t, key); resp.status != fiber.StatusCreated || resp.header.Get(constant.IdempotencyHeader) != "hit" {
		t.Errorf("retry: status %d %s=%q, want 201 hit", resp.status, constant.IdempotencyHeader, resp.header.Get(constant.IdempotencyHeader))
	}
}

// A request whose handler fails saves nothing and releases its lock, so a retry runs the handler again.
func TestIdempotencyHandlerErrorSavesNothing(t *testing.T) {
	client := newTestIdempotencyRedis(t)
	prefix := testIdempotencyPrefix(t, client)
	key := prefix[len("test-idempotency-"):] + "handlererr"
	var calls atomic.Int32
	app := newIdempotencyTestApp(testIdempotencyConfig(client, prefix), func(c *fiber.Ctx) error {
		if calls.Add(1) == 1 {
			return pgerr.ErrInvalidReq
		}
		return c.SendString("second")
	})

	if resp := app.post(t, key); resp.status != fiber.StatusBadRequest {
		t.Errorf("first: status %d, want the handler's 400", resp.status)
	}
	ctx := context.Background()
	if n := client.Exists(ctx, prefix+":"+key, "mutex:idempotency-request:"+key).Val(); n != 0 {
		t.Errorf("%d of response and lock left, want none", n)
	}
	if resp := app.post(t, key); resp.status != fiber.StatusOK || resp.body != "second" || resp.header.Get(constant.IdempotencyHeader) != "saved" {
		t.Errorf("retry: %d %q, want the handler run again", resp.status, resp.body)
	}
}

// Without Redis the request fails closed: it is refused with the lock error after the 5 tries, and the handler
// does not run.
func TestIdempotencyRefusesTheRequestWithoutRedis(t *testing.T) {
	if os.Getenv("PENGUIN_TEST_REDIS_URL") == "" {
		t.Skip("PENGUIN_TEST_REDIS_URL is not set")
	}
	// nothing listens on port 1
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	t.Cleanup(func() { client.Close() })
	var calls atomic.Int32
	app := newIdempotencyTestApp(testIdempotencyConfig(client, "test-idempotency-down"), func(c *fiber.Ctx) error {
		calls.Add(1)
		return c.SendString("ok")
	})
	logs := captureIdempotencyLogs(t, zerolog.InfoLevel)

	start := time.Now()
	resp := app.post(t, "down")
	elapsed := time.Since(start)

	if resp.status != fiber.StatusInternalServerError {
		t.Errorf("status %d, want 500", resp.status)
	}
	var pe *pgerr.PenguinError
	if errs := app.errors(); len(errs) != 1 || !errors.As(errs[0], &pe) || pe.Message != testIdempotencyLockedMessage {
		t.Errorf("errors %v, want the lock error", errs)
	}
	if elapsed < 950*time.Millisecond {
		t.Errorf("refused after %v, want after the 5 tries", elapsed)
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("handler ran %d times, want never", n)
	}
	levels := logs()
	if got := levels["http.idempotency.lock"]; len(got) != 1 {
		t.Errorf("http.idempotency.lock: logged %v, want once", got)
	}
	if got := levels["http.idempotency.lock.failed"]; len(got) != 1 {
		t.Errorf("http.idempotency.lock.failed: logged %v, want once", got)
	}
}

// During a rolling deploy the middleware shares its keys with the redsync one it replaced: each respects the lock
// of the other.
func TestIdempotencySharesTheLockWithRedsync(t *testing.T) {
	client := newTestIdempotencyRedis(t)
	prefix := testIdempotencyPrefix(t, client)
	key := prefix[len("test-idempotency-"):] + "redsync"
	rs := redsync.New(goredis.NewPool(client))
	var redsyncErr error
	app := newIdempotencyTestApp(testIdempotencyConfig(client, prefix), func(c *fiber.Ctx) error {
		redsyncErr = rs.NewMutex("mutex:idempotency-request:"+key, redsync.WithTries(1)).Lock()
		return c.SendString("ok")
	})

	held := rs.NewMutex("mutex:idempotency-request:"+key, redsync.WithExpiry(time.Minute))
	if err := held.Lock(); err != nil {
		t.Fatal(err)
	}
	if resp := app.post(t, key); resp.status != fiber.StatusInternalServerError {
		t.Errorf("while redsync holds the lock: status %d, want the 500 lock error", resp.status)
	}
	if ok, err := held.Unlock(); !ok {
		t.Fatalf("redsync unlock: %v", err)
	}

	if resp := app.post(t, key); resp.status != fiber.StatusOK || resp.header.Get(constant.IdempotencyHeader) != "saved" {
		t.Errorf("status %d %s=%q, want 200 saved", resp.status, constant.IdempotencyHeader, resp.header.Get(constant.IdempotencyHeader))
	}
	if !errors.Is(redsyncErr, redsync.ErrFailed) && !errors.As(redsyncErr, new(*redsync.ErrTaken)) {
		t.Errorf("redsync locked the key while the request held it: %v", redsyncErr)
	}
}

// failingScripter fails the scripts with the given SHA1s.
type failingScripter struct {
	*redis.Client
	fail map[string]bool
}

func (s failingScripter) EvalSha(ctx context.Context, sha1 string, keys []string, args ...interface{}) *redis.Cmd {
	if s.fail[sha1] {
		cmd := redis.NewCmd(ctx)
		cmd.SetErr(errors.New("failing script"))
		return cmd
	}
	return s.Client.EvalSha(ctx, sha1, keys, args...)
}

// When saving the response fails the request returns the error and releases its lock, so a retry runs the handler
// again.
func TestIdempotencyReleasesTheLockWhenSavingFails(t *testing.T) {
	client := newTestIdempotencyRedis(t)
	prefix := testIdempotencyPrefix(t, client)
	key := prefix[len("test-idempotency-"):] + "savefails"
	config := testIdempotencyConfig(client, prefix)
	config.Redis = failingScripter{client, map[string]bool{saveIdempotencyScript.Hash(): true}}
	var calls atomic.Int32
	app := newIdempotencyTestApp(config, testIdempotencyHandler(&calls))
	logs := captureIdempotencyLogs(t, zerolog.InfoLevel)

	if resp := app.post(t, key); resp.status != fiber.StatusInternalServerError {
		t.Errorf("status %d, want 500", resp.status)
	}
	if errs := app.errors(); len(errs) != 1 || errs[0].Error() != "failing script" {
		t.Errorf("errors %v, want the saving error", errs)
	}
	if got := logs()["http.idempotency.response.save.failed"]; len(got) != 1 || got[0] != zerolog.LevelErrorValue {
		t.Errorf("http.idempotency.response.save.failed: logged at %v, want once at error", got)
	}
	ctx := context.Background()
	if n := client.Exists(ctx, prefix+":"+key, "mutex:idempotency-request:"+key).Val(); n != 0 {
		t.Errorf("%d of response and lock left, want none", n)
	}

	config.Redis = client
	if resp := app.post(t, key); resp.status != fiber.StatusCreated || resp.body != `{"call":2}` {
		t.Errorf("retry: %d %q, want the handler run again", resp.status, resp.body)
	}
}

// A handler that panics releases the lock as it unwinds.
func TestIdempotencyReleasesTheLockWhenTheHandlerPanics(t *testing.T) {
	client := newTestIdempotencyRedis(t)
	prefix := testIdempotencyPrefix(t, client)
	key := prefix[len("test-idempotency-"):] + "panics"
	app := fiber.New()
	app.Use(recover.New())
	app.Post("/", Idempotency(testIdempotencyConfig(client, prefix)), func(c *fiber.Ctx) error {
		panic("handler panics")
	})

	req := httptest.NewRequest(fiber.MethodPost, "/", nil)
	req.Header.Set(constant.IdempotencyKeyHeader, key)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != fiber.StatusInternalServerError {
		t.Errorf("status %d, want 500", resp.StatusCode)
	}
	if n := client.Exists(context.Background(), prefix+":"+key, "mutex:idempotency-request:"+key).Val(); n != 0 {
		t.Errorf("%d of response and lock left, want none", n)
	}
}

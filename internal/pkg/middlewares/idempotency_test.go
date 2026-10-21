package middlewares

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-redsync/redsync/v4"
	redsyncredis "github.com/go-redsync/redsync/v4/redis"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// lockedPool is a redsync pool whose mutexes can always be acquired and released.
type lockedPool struct{}

func (lockedPool) Get(context.Context) (redsyncredis.Conn, error) { return lockedConn{}, nil }

type lockedConn struct{}

func (lockedConn) Get(string) (string, error)                        { return "", nil }
func (lockedConn) Set(string, string) (bool, error)                  { return true, nil }
func (lockedConn) SetNX(string, string, time.Duration) (bool, error) { return true, nil }
func (lockedConn) Eval(*redsyncredis.Script, ...interface{}) (interface{}, error) {
	return int64(1), nil
}
func (lockedConn) PTTL(string) (time.Duration, error) { return time.Minute, nil }
func (lockedConn) Close() error                       { return nil }

// mapStorage is an in-memory fiber.Storage.
type mapStorage struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (s *mapStorage) Get(key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[key], nil
}

func (s *mapStorage) Set(key string, val []byte, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = val
	return nil
}

func (s *mapStorage) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, key)
	return nil
}

func (s *mapStorage) Reset() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m = map[string][]byte{}
	return nil
}

func (s *mapStorage) Close() error { return nil }

// The dashboards count http.idempotency.lock and http.idempotency.hit, so both
// must be logged at the production level (info); the other idempotency details
// stay at debug.
func TestIdempotencyLogsLockAndHitAtInfo(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Logger
	log.Logger = zerolog.New(&buf).Level(zerolog.InfoLevel)
	t.Cleanup(func() { log.Logger = prev })

	app := fiber.New()
	app.Use(Idempotency(&IdempotencyConfig{
		Lifetime:  time.Hour,
		KeyHeader: "X-Penguin-Idempotency-Key",
		Storage:   &mapStorage{m: map[string][]byte{}},
		RedSync:   redsync.New(lockedPool{}),
	}))
	app.Post("/", func(c *fiber.Ctx) error { return c.SendString("ok") })

	// the first request locks the key and saves the response; the second hits it
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(fiber.MethodPost, "/", nil)
		req.Header.Set("X-Penguin-Idempotency-Key", "testkey1")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("request %d: status %d, want %d", i, resp.StatusCode, fiber.StatusOK)
		}
	}

	levels := map[string][]string{}
	sc := bufio.NewScanner(&buf)
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

	for _, evt := range []string{"http.idempotency.lock", "http.idempotency.hit"} {
		if got := levels[evt]; len(got) != 1 || got[0] != zerolog.LevelInfoValue {
			t.Errorf("%s: logged at levels %v, want once at info", evt, got)
		}
	}
	if got := levels["http.idempotency.saved"]; len(got) != 0 {
		t.Errorf("http.idempotency.saved: logged at levels %v at info, want it kept at debug", got)
	}
}

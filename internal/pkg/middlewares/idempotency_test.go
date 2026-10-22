package middlewares

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"exusiai.dev/gommon/constant"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// The dashboards count http.idempotency.lock and http.idempotency.hit, so both
// must be logged at the production level (info); the other idempotency details
// stay at debug.
func TestIdempotencyLogsLockAndHitAtInfo(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Logger
	log.Logger = zerolog.New(&buf).Level(zerolog.InfoLevel)
	t.Cleanup(func() { log.Logger = prev })

	client := newTestIdempotencyRedis(t)
	prefix := testIdempotencyPrefix(t, client)
	app := fiber.New()
	app.Use(Idempotency(testIdempotencyConfig(client, prefix)))
	app.Post("/", func(c *fiber.Ctx) error { return c.SendString("ok") })

	// the first request locks the key and saves the response; the second hits it
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(fiber.MethodPost, "/", nil)
		req.Header.Set(constant.IdempotencyKeyHeader, prefix[len("test-idempotency-"):]+"logs")
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

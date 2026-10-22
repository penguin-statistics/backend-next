//go:build compressbench

// Compression benchmark harness: runs the largest JSON responses the frontend loads through fiber's compress
// middleware at each of its levels, for brotli and gzip, and reports the CPU time and the size of each compressed
// body (B/resp). The bodies are downloaded from production when the benchmark starts, so nothing large is committed.
//
//	go test -tags compressbench -run '^$' -bench . -benchtime 5x -count 5 ./internal/server/httpserver/
//
// BestCompression's brotli 11 takes seconds a body and dominates a full run (about 2 minutes per count); for a quick
// comparison of the other two levels add -bench 'CompressLevels/.*/(BestSpeed|Default)/'.
//
// Env: COMPRESSBENCH_BASE (API base, default https://penguin-stats.io/PenguinStats/api/v2), COMPRESSBENCH_DIR (read
// <name>.json from this directory instead of downloading, e.g. to compare runs on the same bodies).
package httpserver

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/compress"
	"github.com/valyala/fasthttp"
)

// compressBenchPayloads are the responses the frontend loads at boot (CN, global, all sources), plus the public
// matrix API clients use
var compressBenchPayloads = []struct{ name, path string }{
	{"matrix", "/_private/result/matrix/CN/global/all"},
	{"pattern", "/_private/result/pattern/CN/global/all"},
	{"trend", "/_private/result/trend/CN"},
	{"stages", "/stages?server=CN"},
	{"items", "/items"},
	{"zones", "/zones"},
	{"public-matrix", "/result/matrix?server=CN"},
}

var compressBenchLevels = []struct {
	name  string
	level compress.Level
}{
	{"BestSpeed", compress.LevelBestSpeed},
	{"Default", compress.LevelDefault},
	{"BestCompression", compress.LevelBestCompression},
}

func loadCompressBenchPayload(b *testing.B, name, path string) []byte {
	b.Helper()
	if dir := os.Getenv("COMPRESSBENCH_DIR"); dir != "" {
		body, err := os.ReadFile(filepath.Join(dir, name+".json"))
		if err != nil {
			b.Fatal(err)
		}
		return body
	}

	base := os.Getenv("COMPRESSBENCH_BASE")
	if base == "" {
		base = "https://penguin-stats.io/PenguinStats/api/v2"
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, base+path, nil)
	if err != nil {
		b.Fatal(err)
	}
	// ask for the identity body; net/http would otherwise ask for gzip and decode it
	req.Header.Set(fiber.HeaderAcceptEncoding, "identity")
	// benchmarks run after go test's -timeout alarm is stopped, so a stalled download would hang forever
	resp, err := (&http.Client{Timeout: time.Minute}).Do(req)
	if err != nil {
		b.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b.Fatalf("GET %s: %s", path, resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		b.Fatal(err)
	}
	return body
}

func BenchmarkCompressLevels(b *testing.B) {
	for _, p := range compressBenchPayloads {
		payload := loadCompressBenchPayload(b, p.name, p.path)
		b.Logf("%s: %d B uncompressed", p.name, len(payload))

		for _, l := range compressBenchLevels {
			app := fiber.New()
			app.Use(compress.New(compress.Config{Level: l.level}))
			app.Get("/", func(ctx *fiber.Ctx) error {
				ctx.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
				return ctx.Send(payload)
			})
			handler := app.Handler()

			for _, encoding := range []string{"br", "gzip"} {
				b.Run(fmt.Sprintf("%s/%s/%s", p.name, l.name, encoding), func(b *testing.B) {
					var size int
					b.SetBytes(int64(len(payload)))
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						var fctx fasthttp.RequestCtx
						fctx.Request.SetRequestURI("/")
						fctx.Request.Header.Set(fiber.HeaderAcceptEncoding, encoding)
						handler(&fctx)
						if got := string(fctx.Response.Header.ContentEncoding()); got != encoding {
							b.Fatalf("Content-Encoding %q, want %q", got, encoding)
						}
						size = len(fctx.Response.Body())
					}
					b.ReportMetric(float64(size), "B/resp")
				})
			}
		}
	}
}

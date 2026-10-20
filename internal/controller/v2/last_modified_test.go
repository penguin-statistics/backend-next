package v2

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"exusiai.dev/backend-next/internal/model/cache"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
	"exusiai.dev/backend-next/internal/pkg/middlewares"
	"exusiai.dev/backend-next/internal/service"
)

func newCacheTestApp(t *testing.T) *fiber.App {
	t.Helper()
	cache.Initialize()
	t.Cleanup(func() {
		cache.ShimStages.Flush()
		cache.ShimGlobalDropMatrix.Flush()
		cache.LastModifiedTime.Flush()
	})
	app := fiber.New()
	app.Use(middlewares.InjectI18n()) // for the validation of the query
	return app
}

func get(t *testing.T, app *fiber.App, target string, modifiedSince string) (*http.Response, string) {
	t.Helper()
	req := httptest.NewRequest(fiber.MethodGet, target, nil)
	if modifiedSince != "" {
		req.Header.Set(fiber.HeaderIfModifiedSince, modifiedSince)
	}
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(body)
}

// the stages are served from the cache, which the tests fill instead of a database
func TestGetStagesWithoutLastModified(t *testing.T) {
	app := newCacheTestApp(t)
	c := &Stage{StageService: &service.Stage{}}
	app.Get("/stages", c.GetStages)
	cache.ShimStages.Set("CN", []*modelv2.Stage{{ArkStageID: "main_01-07", Code: "1-7"}}, time.Hour)

	tests := []struct {
		name         string
		lastModified time.Time
	}{
		// cached, but its last modified time was flushed: whatever time is sent could be newer than the stages
		{"flushed", time.Time{}},
		// changed again in the same second
		{"in the future", time.Now().Add(time.Second)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := cache.LastModifiedTime.Flush(); err != nil {
				t.Fatal(err)
			}
			if !tt.lastModified.IsZero() {
				cache.LastModifiedTime.Set("[shimStages#server:CN]", tt.lastModified, 0)
			}

			resp, body := get(t, app, "/stages?server=CN", time.Now().UTC().Format(http.TimeFormat))
			if resp.StatusCode != fiber.StatusOK || !strings.Contains(body, `"1-7"`) {
				t.Fatalf("status %d, body %s; want 200 with the stages", resp.StatusCode, body)
			}
			for _, header := range []string{fiber.HeaderLastModified, fiber.HeaderCacheControl} {
				if got := resp.Header.Get(header); got != "" {
					t.Errorf("%s %q, want none", header, got)
				}
			}
		})
	}
}

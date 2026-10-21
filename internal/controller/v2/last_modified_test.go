package v2

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"exusiai.dev/gommon/constant"
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

// getCacheable is get, repeated while the response has no Last-Modified, for up to 5 seconds: a response calculated a
// moment ago is sent without one until the Date reaches its last modified time (see sendCacheable).
func getCacheable(t *testing.T, app *fiber.App, target string) (*http.Response, string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, body := get(t, app, target, "")
		if resp.Header.Get(fiber.HeaderLastModified) != "" || time.Now().After(deadline) {
			return resp, body
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// the stages are served from the cache, which the tests fill instead of a database
func TestGetStagesNotModified(t *testing.T) {
	app := newCacheTestApp(t)
	c := &Stage{StageService: &service.Stage{}}
	app.Get("/stages", c.GetStages)
	server := testServer()
	target := "/stages?server=" + server
	stages := func(code string) []*modelv2.Stage {
		return []*modelv2.Stage{{ArkStageID: "main_01-07", Code: code}}
	}
	// what GetShimStages does when it (re)calculates the stages
	calculate := func(code string) {
		cache.SetLastModifiedTime("[shimStages#server:"+server+"]", stages(code), func() {
			cache.ShimStages.Set(server, stages(code), time.Hour)
		})
	}

	calculate("1-7")
	resp, body := getCacheable(t, app, target)
	lastModified := resp.Header.Get(fiber.HeaderLastModified)
	if resp.StatusCode != fiber.StatusOK || !strings.Contains(body, `"1-7"`) || lastModified == "" {
		t.Fatalf("first request: status %d, Last-Modified %q, body %s", resp.StatusCode, lastModified, body)
	}

	resp, body = get(t, app, target, lastModified)
	if resp.StatusCode != fiber.StatusNotModified || body != "" {
		t.Fatalf("revalidation: status %d, body %q; want 304 without a body", resp.StatusCode, body)
	}

	// the stages expire and are recalculated unchanged
	calculate("1-7")
	if resp, _ = get(t, app, target, lastModified); resp.StatusCode != fiber.StatusNotModified {
		t.Fatalf("revalidation after an unchanged recalculation: status %d, want 304", resp.StatusCode)
	}

	// the stages change, even in the same second
	calculate("1-8")
	resp, body = get(t, app, target, lastModified)
	if resp.StatusCode != fiber.StatusOK || !strings.Contains(body, `"1-8"`) {
		t.Fatalf("revalidation of a stale copy: status %d, body %s; want 200 with the new stages", resp.StatusCode, body)
	}
	if got := resp.Header.Get(fiber.HeaderLastModified); got == lastModified {
		t.Errorf("the changed stages have the same Last-Modified %q", got)
	}
}

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

// fasthttp sends a Date it formats once a second, which can be a second behind the time Last-Modified is checked
// against: within the second of a change, Last-Modified must be omitted until the Date has caught up, not sent later.
func TestSendCacheableLastModifiedNotLaterThanDate(t *testing.T) {
	app := fiber.New()
	var lastModified time.Time
	app.Get("/", func(ctx *fiber.Ctx) error {
		return sendCacheable(ctx, recordedTime{key: "test", time: lastModified}, fiber.Map{"ok": true})
	})

	tests := []struct {
		name         string
		lastModified func() time.Time
	}{
		// changed in the current second: the Date can still be the previous one
		{"current second", func() time.Time { return time.Now().Truncate(time.Second) }},
		// changed in the next second (see cache.SetLastModifiedTime): sent once the Date reaches it
		{"next second", func() time.Time { return time.Now().Truncate(time.Second).Add(time.Second) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lastModified = tt.lastModified()
			sent := false
			// beyond the next refresh of the Date, and the second after it
			for deadline := time.Now().Add(2500 * time.Millisecond); time.Now().Before(deadline); {
				resp, body := get(t, app, "/", "")
				if resp.StatusCode != fiber.StatusOK || body != `{"ok":true}` {
					t.Fatalf("status %d, body %s; want 200 with the body", resp.StatusCode, body)
				}
				header := resp.Header.Get(fiber.HeaderLastModified)
				if header == "" {
					if got := resp.Header.Get(fiber.HeaderCacheControl); got != "" {
						t.Fatalf("Cache-Control %q without Last-Modified, want none", got)
					}
					continue
				}
				sent = true
				got, err := http.ParseTime(header)
				if err != nil {
					t.Fatal(err)
				}
				date, err := http.ParseTime(resp.Header.Get(fiber.HeaderDate))
				if err != nil {
					t.Fatal(err)
				}
				if got.After(date) {
					t.Fatalf("Last-Modified %s later than Date %s", header, resp.Header.Get(fiber.HeaderDate))
				}
				if !got.Equal(lastModified) {
					t.Fatalf("Last-Modified %s, want %s", header, lastModified.UTC().Format(http.TimeFormat))
				}
			}
			if !sent {
				t.Error("Last-Modified never sent, want it once the Date has reached it")
			}
		})
	}
}

func TestGetDropMatrixNotModified(t *testing.T) {
	app := newCacheTestApp(t)
	c := &Result{DropMatrixService: &service.DropMatrix{}}
	app.Get("/result/matrix", c.GetDropMatrix)
	key := "CN" + constant.CacheSep + "false" + constant.CacheSep + constant.SourceCategoryAll
	matrix := modelv2.DropMatrixQueryResult{Matrix: []*modelv2.OneDropMatrixElement{{StageID: "main_01-07", ItemID: "30012", Quantity: 1, Times: 2}}}
	cache.ShimGlobalDropMatrix.Set(key, matrix, time.Hour)
	cache.LastModifiedTime.Set("[shimGlobalDropMatrix#server|showClosedZones|sourceCategory:"+key+"]", time.Now().Add(-time.Minute), 0)

	resp, _ := get(t, app, "/result/matrix?server=CN", "")
	lastModified := resp.Header.Get(fiber.HeaderLastModified)
	if resp.StatusCode != fiber.StatusOK || lastModified == "" {
		t.Fatalf("first request: status %d, Last-Modified %q", resp.StatusCode, lastModified)
	}
	if resp, _ = get(t, app, "/result/matrix?server=CN", lastModified); resp.StatusCode != fiber.StatusNotModified {
		t.Fatalf("revalidation: status %d, want 304", resp.StatusCode)
	}
	stale := time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)
	if resp, _ = get(t, app, "/result/matrix?server=CN", stale); resp.StatusCode != fiber.StatusOK {
		t.Fatalf("revalidation of a stale copy: status %d, want 200", resp.StatusCode)
	}
}

// sendCacheable does not answer with 304 when the response changed after its last modified time was read, whether
// recalculated by the request or concurrently: the time read is then the one of the client's copy
func TestSendCacheableChangedMeanwhile(t *testing.T) {
	app := newCacheTestApp(t)
	key := "[test#TestSendCacheableChangedMeanwhile:" + testServer() + "]"
	record := func(code string) []*modelv2.Stage {
		stages := []*modelv2.Stage{{ArkStageID: "main_01-07", Code: code}}
		cache.SetLastModifiedTime(key, stages, func() {})
		return stages
	}
	var change string // the stages recalculated after the time is read, if any
	app.Get("/stages", func(ctx *fiber.Ctx) error {
		lastModified := lastModifiedTime(key)
		stages := []*modelv2.Stage{{ArkStageID: "main_01-07", Code: "1-7"}}
		if change != "" {
			stages = record(change)
		}
		return sendCacheable(ctx, lastModified, stages)
	})

	record("1-7")
	resp, _ := getCacheable(t, app, "/stages")
	lastModified := resp.Header.Get(fiber.HeaderLastModified)
	if lastModified == "" {
		t.Fatal("no Last-Modified")
	}

	change = "1-7" // recalculated unchanged
	if resp, _ = get(t, app, "/stages", lastModified); resp.StatusCode != fiber.StatusNotModified {
		t.Errorf("recalculated unchanged: status %d, want 304", resp.StatusCode)
	}
	change = "1-8"
	resp, body := get(t, app, "/stages", lastModified)
	if resp.StatusCode != fiber.StatusOK || !strings.Contains(body, `"1-8"`) {
		t.Fatalf("recalculated changed: status %d, body %s; want 200 with the new stages", resp.StatusCode, body)
	}
	if got := resp.Header.Get(fiber.HeaderLastModified); got != lastModified {
		t.Errorf("recalculated changed: Last-Modified %q, want the one read before, %q", got, lastModified)
	}
}

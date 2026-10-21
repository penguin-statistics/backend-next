package v2

import (
	"io"
	"net/http/httptest"
	"testing"

	"exusiai.dev/gommon/constant"
	"github.com/gofiber/fiber/v2"

	"exusiai.dev/backend-next/internal/pkg/pgid"
	"exusiai.dev/backend-next/internal/server/svr"
)

// The /result group caches the responses to requests with itemFilter or stageFilter by URL. A route added to the
// group after RegisterResult runs behind the same middlewares, and answers with the PenguinID of the request.
func TestResultCacheKeepsNoPersonalResponse(t *testing.T) {
	app := fiber.New()
	v2 := &svr.V2{Router: app.Group("/PenguinStats/api/v2")}
	RegisterResult(v2, Result{})
	app.Get("/PenguinStats/api/v2/result/whoami", func(ctx *fiber.Ctx) error {
		return ctx.SendString(pgid.Extract(ctx))
	})

	get := func(query, penguinId string) (body, cacheStatus string) {
		t.Helper()
		req := httptest.NewRequest(fiber.MethodGet, "/PenguinStats/api/v2/result/whoami?"+query, nil)
		if penguinId != "" {
			req.Header.Set(fiber.HeaderAuthorization, "PenguinID "+penguinId)
		}
		resp, err := app.Test(req, -1)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return string(b), resp.Header.Get(constant.CacheHeader)
	}

	// the global responses are shared, whoever asks
	if body, _ := get("itemFilter=30012", "11111111"); body != "11111111" {
		t.Fatalf("global: first response %q", body)
	}
	if body, cacheStatus := get("itemFilter=30012", "22222222"); body != "11111111" || cacheStatus != "hit" {
		t.Fatalf("global: second response %q (cache %q), want the first one from the cache", body, cacheStatus)
	}

	for _, query := range []string{"is_personal=true&itemFilter=30012", "is_personal=1&stageFilter=main_01-07"} {
		for _, penguinId := range []string{"11111111", "22222222", ""} {
			if body, cacheStatus := get(query, penguinId); body != penguinId {
				t.Errorf("%s as %q: response for %q (cache %q)", query, penguinId, body, cacheStatus)
			}
		}
	}
}

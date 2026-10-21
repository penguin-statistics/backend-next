package svr_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/gofiber/fiber/v2"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/server/httpserver"
	"exusiai.dev/backend-next/internal/server/svr"
)

func TestEndpointGroups(t *testing.T) {
	adminKey := strings.Repeat("k", 64)
	conf := &appconfig.Config{ConfigSpec: appconfig.ConfigSpec{
		DevMode:                   true,
		AdminKey:                  adminKey,
		HTTPServerShutdownTimeout: time.Second,
	}}
	app := httpserver.CreateServiceApp(conf)
	v2, admin, meta := svr.CreateEndpointGroups(app, conf)
	ok := func(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) }
	v2.Get("/items", ok)
	admin.Get("/items", ok)
	meta.Get("/items", ok)

	tests := []struct {
		name       string
		path       string
		headers    map[string]string
		wantStatus int
		wantHeader map[string]string
	}{
		{
			name:       "v2 sets the shim compatibility header",
			path:       "/PenguinStats/api/v2/items",
			wantStatus: fiber.StatusOK,
			wantHeader: map[string]string{constant.ShimCompatibilityHeaderKey: constant.ShimCompatibilityHeaderValue},
		},
		{
			name:       "admin without the key",
			path:       "/api/admin/items",
			wantStatus: fiber.StatusUnauthorized,
		},
		{
			name:       "admin with the key",
			path:       "/api/admin/items",
			headers:    map[string]string{fiber.HeaderAuthorization: "Bearer " + adminKey},
			wantStatus: fiber.StatusOK,
		},
		{
			name:       "meta",
			path:       "/api/_/items",
			wantStatus: fiber.StatusOK,
		},
		// the v3alpha API is removed (#617). These cases cover only the group middleware: nothing rejects
		// or annotates /api/v3alpha requests any more, whether or not a client opts in. They register no
		// controllers, so they do not show that no v3alpha route exists; that the v3 controllers and the V3
		// group are gone is enforced at compile time, and TestOptionsGraphIsComplete in internal/app checks
		// that nothing left in the fx graph still needs them.
		{
			name:       "v3alpha without the opt-in header",
			path:       "/api/v3alpha/items",
			wantStatus: fiber.StatusNotFound,
			wantHeader: map[string]string{"X-Penguin-Notes": ""},
		},
		{
			name:       "v3alpha with the opt-in header",
			path:       "/api/v3alpha/items",
			headers:    map[string]string{fiber.HeaderAccept: "application/vnd.penguin.v3+json"},
			wantStatus: fiber.StatusNotFound,
			wantHeader: map[string]string{"X-Penguin-Notes": ""},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tt.wantStatus {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}
			for k, v := range tt.wantHeader {
				if got := resp.Header.Get(k); got != v {
					t.Errorf("header %s = %q, want %q", k, got, v)
				}
			}
		})
	}
}

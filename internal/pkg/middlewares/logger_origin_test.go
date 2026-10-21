package middlewares

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func TestLoggerRecordsOriginAndCORSTier(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Logger
	log.Logger = zerolog.New(&buf)
	t.Cleanup(func() { log.Logger = prev })

	app := fiber.New()
	app.Use(CORS(testCredentialedOrigins))
	Logger(app)
	app.Post("/report", func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})

	tests := []struct {
		name        string
		origin      string
		wantTier    string
		wantIgnored bool
	}{
		{"no origin, e.g. MAA", "", "", false},
		{"allowlisted", "capacitor://localhost", "credentialed", false},
		{"foreign", "https://evil.example", "public", true},
		{"long", "https://" + strings.Repeat("a", 400) + ".example", "public", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf.Reset()
			req := httptest.NewRequest(fiber.MethodPost, "/report", nil)
			req.Header.Set("Cookie", "userID=11111111")
			if tt.origin != "" {
				// a page on another site; without either header it is a client like MAA
				req.Header.Set("Sec-Fetch-Site", "cross-site")
				req.Header.Set(fiber.HeaderOrigin, tt.origin)
			}
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()

			var entry map[string]any
			for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
				var e map[string]any
				if json.Unmarshal([]byte(line), &e) == nil && e["evt.name"] == "http.request" {
					entry = e
				}
			}
			if entry == nil {
				t.Fatalf("no http.request log in %q", buf.String())
			}

			origin, _ := entry["http.origin"].(string)
			wantOrigin := tt.origin
			if len(wantOrigin) > maxLoggedOriginLength {
				wantOrigin = wantOrigin[:maxLoggedOriginLength]
			}
			if origin != wantOrigin {
				t.Errorf("http.origin = %q, want %q", origin, wantOrigin)
			}
			if tier, _ := entry["http.cors_tier"].(string); tier != tt.wantTier {
				t.Errorf("http.cors_tier = %q, want %q", tier, tt.wantTier)
			}
			if ignored, _ := entry["usr.cookie_ignored"].(bool); ignored != tt.wantIgnored {
				t.Errorf("usr.cookie_ignored = %v, want %v", ignored, tt.wantIgnored)
			}
			// the ignored cookie does not show up as the user either
			if usr, _ := entry["usr.id"].(string); tt.wantIgnored && usr != "" {
				t.Errorf("usr.id = %q, want none", usr)
			}
		})
	}
}

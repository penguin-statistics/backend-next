package pgid

import (
	"io"
	"net/http/httptest"
	"testing"

	"exusiai.dev/gommon/constant"
	"github.com/gofiber/fiber/v2"
)

func TestIgnoreCookie(t *testing.T) {
	tests := []struct {
		name          string
		ignore        bool
		authorization string
		wantID        string
		wantCookie    bool
		wantIgnored   bool
	}{
		{name: "cookie used", wantID: "11111111", wantCookie: true},
		{name: "cookie ignored", ignore: true, wantID: "", wantCookie: false, wantIgnored: true},
		{name: "header still used", ignore: true, authorization: "PenguinID 22222222", wantID: "22222222", wantCookie: false, wantIgnored: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := fiber.New()
			app.Post("/", func(c *fiber.Ctx) error {
				if tt.ignore {
					IgnoreCookie(c)
				}
				if got := IgnoredCookie(c); got != tt.wantIgnored {
					t.Errorf("IgnoredCookie = %v, want %v", got, tt.wantIgnored)
				}
				Inject(c, "33333333")
				return c.SendString(Extract(c))
			})

			req := httptest.NewRequest(fiber.MethodPost, "/", nil)
			req.Header.Set("Cookie", constant.PenguinIDCookieKey+"=11111111")
			if tt.authorization != "" {
				req.Header.Set(fiber.HeaderAuthorization, tt.authorization)
			}
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			body, _ := io.ReadAll(resp.Body)
			if string(body) != tt.wantID {
				t.Errorf("Extract = %q, want %q", body, tt.wantID)
			}
			if got := resp.Header.Get(fiber.HeaderSetCookie) != ""; got != tt.wantCookie {
				t.Errorf("Set-Cookie sent = %v, want %v", got, tt.wantCookie)
			}
			// the header is always set: only a page allowed to read it can
			if got := resp.Header.Get(constant.PenguinIDSetHeader); got != "33333333" {
				t.Errorf("%s = %q, want 33333333", constant.PenguinIDSetHeader, got)
			}
		})
	}
}

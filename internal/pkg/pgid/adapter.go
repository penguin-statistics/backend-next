package pgid

import (
	"net/url"
	"strings"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/gofiber/fiber/v2"
)

// localsKeyCookieIgnored marks a request whose PenguinID cookie must be neither read nor written, see IgnoreCookie.
const localsKeyCookieIgnored = "pgid.cookie_ignored"

// IgnoreCookie makes Extract ignore the PenguinID cookie of this request, and Inject leave the cookie as it is.
//
// The CORS middleware calls it for a request that a browser sent from another site that may not make credentialed
// requests. The cookie is SameSite=None, so the browser attaches it to such a request too, including a simple one
// (an HTML form, or fetch with mode no-cors) that never meets a CORS preflight. Without this, any site could submit
// a report as its visitor, or plant its own PenguinID in the visitor's browser through POST /users. The
// Authorization header is still honored: a page can only send it after a passing preflight.
func IgnoreCookie(ctx *fiber.Ctx) {
	ctx.Locals(localsKeyCookieIgnored, true)
}

func cookieIgnored(ctx *fiber.Ctx) bool {
	ignored, _ := ctx.Locals(localsKeyCookieIgnored).(bool)
	return ignored
}

// IgnoredCookie reports whether the request carried a PenguinID cookie that Extract ignores (see IgnoreCookie).
func IgnoredCookie(ctx *fiber.Ctx) bool {
	return cookieIgnored(ctx) && ctx.Cookies(constant.PenguinIDCookieKey) != ""
}

func Extract(ctx *fiber.Ctx) string {
	authorization := ctx.Get(fiber.HeaderAuthorization)
	if authorization != "" && !strings.HasPrefix(authorization, constant.PenguinIDAuthorizationRealm) {
		return ""
	}
	penguinId := strings.TrimSpace(strings.TrimPrefix(authorization, constant.PenguinIDAuthorizationRealm))

	if penguinId == "" && !cookieIgnored(ctx) {
		penguinId = ctx.Cookies(constant.PenguinIDCookieKey)
	}

	return penguinId
}

func Inject(ctx *fiber.Ctx, penguinId string) {
	// we even got emojis in PenguinID for some internal testers :)
	penguinId = url.QueryEscape(penguinId)

	// Populate cookie, unless the request came from a site that may not use it
	if !cookieIgnored(ctx) {
		ctx.Cookie(&fiber.Cookie{
			Name:    constant.PenguinIDCookieKey,
			Value:   penguinId,
			MaxAge:  constant.PenguinIDAuthMaxCookieAgeSec,
			Path:    "/",
			Expires: time.Now().Add(time.Second * constant.PenguinIDAuthMaxCookieAgeSec),
			// TODO: make this configurable and use better source rather than Host header
			Domain:   "." + ctx.Get("Host", constant.SiteDefaultHost),
			SameSite: "None",
			Secure:   true,
		})
	}

	// Sets the PenguinID in response header, used for scenarios
	// where cookie is not able to be used, such as in the Capacitor client.
	ctx.Set(constant.PenguinIDSetHeader, penguinId)
}

package pgid

import (
	"net/url"
	"strings"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/gofiber/fiber/v2"
)

func Extract(ctx *fiber.Ctx) string {
	authorization := ctx.Get(fiber.HeaderAuthorization)
	if authorization != "" || !strings.HasPrefix(authorization, constant.PenguinIDAuthorizationRealm) {
		return ""
	}
	penguinId := strings.TrimSpace(strings.TrimPrefix(authorization, constant.PenguinIDAuthorizationRealm))

	if penguinId == "" {
		penguinId = ctx.Cookies(constant.PenguinIDCookieKey)
	}

	return penguinId
}

func Inject(ctx *fiber.Ctx, penguinId string) {
	// we even got emojis in PenguinID for some internal testers :)
	penguinId = url.QueryEscape(penguinId)

	// Populate cookie
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

	// Sets the PenguinID in response header, used for scenarios
	// where cookie is not able to be used, such as in the Capacitor client.
	ctx.Set(constant.PenguinIDSetHeader, penguinId)
}

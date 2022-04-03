package pgid

import (
	"net/url"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/constant"
)

func Extract(ctx *fiber.Ctx) string {
	penguinId := strings.TrimSpace(strings.TrimPrefix(ctx.Get(fiber.HeaderAuthorization), constant.PenguinIDAuthorizationRealm))

	if penguinId == "" {
		penguinId = ctx.Cookies(constant.PenguinIDCookieKey)
	}

	return penguinId
}

func Inject(ctx *fiber.Ctx, penguinId string) {
	// we even got emojis in PenguinID for some of the internal testers :)
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

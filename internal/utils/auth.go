package utils

import (
	"net/url"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/constants"
)

func GetPenguinIDFromRequest(ctx *fiber.Ctx) string {
	penguinId := strings.TrimSpace(strings.TrimPrefix(ctx.Get(fiber.HeaderAuthorization), constants.PenguinIDAuthorizationRealm))

	if penguinId == "" {
		penguinId = ctx.Cookies(constants.PenguinIDCookieKey)
	}

	return penguinId
}

func SetPenguinIDToResponse(ctx *fiber.Ctx, penguinId string) {
	// we even got emojis in PenguinID for some of the internal testers :)
	penguinId = url.QueryEscape(penguinId)

	// Populate cookie
	ctx.Cookie(&fiber.Cookie{
		Name:     constants.PenguinIDCookieKey,
		Value:    penguinId,
		MaxAge:   constants.PenguinIDAuthMaxCookieAgeSec,
		Path:     "/",
		Expires:  time.Now().Add(time.Second * constants.PenguinIDAuthMaxCookieAgeSec),
		Domain:   "." + ctx.Get("Host", constants.DefaultHost),
		SameSite: "None",
		Secure:   true,
	})

	// Sets the PenguinID in response header, used for scenarios
	// where cookie is not able to be used, such as in the Capacitor client.
	ctx.Set(constants.PenguinIDSetHeader, penguinId)
}

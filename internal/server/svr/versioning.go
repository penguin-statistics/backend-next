package svr

import (
	"crypto/subtle"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"

	"github.com/penguin-statistics/backend-next/internal/config"
	"github.com/penguin-statistics/backend-next/internal/constant"
)

type V2 struct {
	fiber.Router
}

type V3 struct {
	fiber.Router
}

type Admin struct {
	fiber.Router
}

type Meta struct {
	fiber.Router
}

func CreateVersioningEndpoints(app *fiber.App, conf *config.Config) (*V2, *V3, *Admin, *Meta) {
	v2 := app.Group("/PenguinStats/api/v2", func(c *fiber.Ctx) error {
		// add compatibility versioning header for v2 shims
		c.Set(constant.ShimCompatibilityHeaderKey, constant.ShimCompatibilityHeaderValue)
		return c.Next()
	})

	v3 := app.Group("/api/v3")

	admin := app.Group("/api/admin", func(c *fiber.Ctx) error {
		if len(conf.AdminKey) < 64 {
			log.Error().Msg("admin key is not set or is too short (at least should be 64 chars long), and a request has reached")
			return c.SendStatus(fiber.StatusInternalServerError)
		}
		key := strings.TrimSpace(strings.TrimPrefix(c.Get(fiber.HeaderAuthorization), "Bearer"))

		// use constant time comparison to prevent timing attacks
		if subtle.ConstantTimeCompare([]byte(key), []byte(conf.AdminKey)) != 1 {
			return c.SendStatus(fiber.StatusUnauthorized)
		}
		return c.Next()
	})

	meta := app.Group("/api/_")

	return &V2{Router: v2}, &V3{Router: v3}, &Admin{Router: admin}, &Meta{Router: meta}
}

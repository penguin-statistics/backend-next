package server

import (
	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/constants"
)

type V2 struct {
	fiber.Router
}

type V3 struct {
	fiber.Router
}

func CreateVersioningEndpoints(app *fiber.App) (*V2, *V3) {
	v2 := app.Group("/PenguinStats/api/v2", func(c *fiber.Ctx) error {
		// add compatibility versioning header for v2 shims
		c.Set(constants.ShimCompatibilityHeaderKey, constants.ShimCompatibilityHeaderValue)
		return c.Next()
	})
	v3 := app.Group("/api/v3")

	return &V2{Router: v2}, &V3{Router: v3}
}

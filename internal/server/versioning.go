package server

import (
	"github.com/gofiber/fiber/v2"
)

type V2 struct {
	fiber.Router
}
type V3 struct {
	fiber.Router
}

func CreateVersioningEndpoints(app *fiber.App) (*V2, *V3) {
	root := app.Group("/api")

	v2 := root.Group("/v2")
	v3 := root.Group("/v3")

	return &V2{Router: v2}, &V3{Router: v3}
}

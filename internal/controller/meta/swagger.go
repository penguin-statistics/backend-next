package meta

import (
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/swagger"

	"github.com/penguin-statistics/backend-next/docs"
	"github.com/penguin-statistics/backend-next/internal/pkg/bininfo"
)

func RegisterSwagger(app *fiber.App) {
	docs.SwaggerInfo.Version = bininfo.Version
	app.Get("/swagger/*", swagger.HandlerDefault) // default
}

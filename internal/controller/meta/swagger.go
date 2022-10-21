package meta

import (
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/swagger"

	"exusiai.dev/backend-next/docs"
	"exusiai.dev/backend-next/internal/pkg/bininfo"
)

func RegisterSwagger(app *fiber.App) {
	docs.SwaggerInfo.Version = bininfo.Version
	app.Get("/swagger/*", swagger.HandlerDefault) // default
}

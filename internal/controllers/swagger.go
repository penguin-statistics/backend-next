package controllers

import (
	"github.com/gofiber/fiber/v2"
	swagger "github.com/penguin-statistics/fiber-swagger/v3"

	_ "github.com/penguin-statistics/backend-next/docs"
)

type SwaggerController struct {
}

func RegisterSwaggerController(app *fiber.App) {
	app.Get("/swagger/*", swagger.Handler) // default

	// app.Get("/swagger/*", swagger.New(swagger.Config{ // custom
	// 	DeepLinking: false,
	// 	// Expand ("list") or Collapse ("none") tag groups by default
	// 	DocExpansion: "none",
	// 	// Prefill OAuth ClientId on Authorize popup
	// 	OAuth: &swagger.OAuthConfig{
	// 		AppName:  "OAuth Provider",
	// 		ClientId: "21bb4edc-05a7-4afc-86f1-2e151e4ba6e2",
	// 	},
	// 	// Ability to change OAuth2 redirect uri location
	// 	OAuth2RedirectUrl: "http://localhost:8080/swagger/oauth2-redirect.html",
	// }))
}

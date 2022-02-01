package controllers

import (
	"github.com/gofiber/fiber/v2"
	"go.uber.org/fx"

	"github.com/penguin-statistics/backend-next/internal/server"
	"github.com/penguin-statistics/backend-next/internal/service"
)
type TestController struct {
	fx.In
	DropMatrixService	  *service.DropMatrixService
	DropInfoService       *service.DropInfoService
}

func RegisterTestController(v3 *server.V3, c TestController) {
	v3.Get("/refresh/:server", c.RefreshAllDropMatrixElements)
}

func (c *TestController) RefreshAllDropMatrixElements(ctx *fiber.Ctx) error {
	server := ctx.Params("server")
	return c.DropMatrixService.RefreshAllDropMatrixElements(ctx, server)
}

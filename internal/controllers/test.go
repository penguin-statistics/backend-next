package controllers

import (
	"strconv"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/fx"
	"gopkg.in/guregu/null.v3"

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
	v3.Get("/matrix/:server", c.GetGlobalDropMatrix)
	v3.Get("/personal/:server/:accountId", c.GetPersonalDropMatrix)
}

func (c *TestController) RefreshAllDropMatrixElements(ctx *fiber.Ctx) error {
	server := ctx.Params("server")
	return c.DropMatrixService.RefreshAllDropMatrixElements(ctx, server)
}

func (c *TestController) GetGlobalDropMatrix(ctx *fiber.Ctx) error {
	server := ctx.Params("server")
	accountId := null.NewInt(0, false)
	globalDropMatrix, err := c.DropMatrixService.GetMaxAccumulableDropMatrixElementsMap(ctx, server, &accountId)
	if err != nil {
		return err
	}
	return ctx.JSON(globalDropMatrix)
}

func (c *TestController) GetPersonalDropMatrix(ctx *fiber.Ctx) error {
	server := ctx.Params("server")
	accountIdStr := ctx.Params("accountId")
	accountIdNum, err := strconv.Atoi(accountIdStr)
	if err != nil {
		return err
	}
	accountIdNull := null.IntFrom(int64(accountIdNum))
	personalDropMatrix, err := c.DropMatrixService.GetMaxAccumulableDropMatrixElementsMap(ctx, server, &accountIdNull)
	if err != nil {
		return err
	}
	return ctx.JSON(personalDropMatrix)
}

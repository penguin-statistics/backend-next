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
	DropMatrixService    *service.DropMatrixService
	DropInfoService      *service.DropInfoService
	PatternMatrixService *service.PatternMatrixService
	TrendService         *service.TrendService
}

func RegisterTestController(v3 *server.V3, c TestController) {
	v3.Get("/refresh/matrix/:server", c.RefreshAllDropMatrixElements)
	v3.Get("/global/matrix/:server", c.GetGlobalDropMatrix)
	v3.Get("/personal/matrix/:server/:accountId", c.GetPersonalDropMatrix)

	v3.Get("/refresh/pattern/:server", c.RefreshAllPatternMatrixElements)
	v3.Get("/global/pattern/:server", c.GetGlobalPatternMatrix)
	v3.Get("/personal/pattern/:server/:accountId", c.GetPersonalPatternMatrix)

	v3.Get("/refresh/trend/:server", c.RefreshAllTrendElements)
	v3.Get("/global/trend/:server", c.GetTrend)
}

func (c *TestController) RefreshAllDropMatrixElements(ctx *fiber.Ctx) error {
	server := ctx.Params("server")
	return c.DropMatrixService.RefreshAllDropMatrixElements(ctx, server)
}

func (c *TestController) GetGlobalDropMatrix(ctx *fiber.Ctx) error {
	server := ctx.Params("server")
	accountId := null.NewInt(0, false)
	globalDropMatrix, err := c.DropMatrixService.GetSavedDropMatrixResults(ctx, server, &accountId)
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
	personalDropMatrix, err := c.DropMatrixService.GetSavedDropMatrixResults(ctx, server, &accountIdNull)
	if err != nil {
		return err
	}
	return ctx.JSON(personalDropMatrix)
}

func (c *TestController) RefreshAllPatternMatrixElements(ctx *fiber.Ctx) error {
	server := ctx.Params("server")
	return c.PatternMatrixService.RefreshAllPatternMatrixElements(ctx, server)
}

func (c *TestController) GetGlobalPatternMatrix(ctx *fiber.Ctx) error {
	server := ctx.Params("server")
	accountId := null.NewInt(0, false)
	globalPatternMatrix, err := c.PatternMatrixService.GetSavedPatternMatrixResults(ctx, server, &accountId)
	if err != nil {
		return err
	}
	return ctx.JSON(globalPatternMatrix)
}

func (c *TestController) GetPersonalPatternMatrix(ctx *fiber.Ctx) error {
	server := ctx.Params("server")
	accountIdStr := ctx.Params("accountId")
	accountIdNum, err := strconv.Atoi(accountIdStr)
	if err != nil {
		return err
	}
	accountIdNull := null.IntFrom(int64(accountIdNum))
	personalPatternMatrix, err := c.PatternMatrixService.GetSavedPatternMatrixResults(ctx, server, &accountIdNull)
	if err != nil {
		return err
	}
	return ctx.JSON(personalPatternMatrix)
}

func (c *TestController) RefreshAllTrendElements(ctx *fiber.Ctx) error {
	server := ctx.Params("server")
	return c.TrendService.RefreshTrendElements(ctx, server)
}

func (c *TestController) GetTrend(ctx *fiber.Ctx) error {
	server := ctx.Params("server")
	trend, err := c.TrendService.GetSavedTrendResults(ctx, server)
	if err != nil {
		return err
	}
	return ctx.JSON(trend)
}

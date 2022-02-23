package shims

import (
	"encoding/json"
	"fmt"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/fx"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/server"
	"github.com/penguin-statistics/backend-next/internal/service"
)

type TestController struct {
	fx.In
	DropMatrixService    *service.DropMatrixService
	PatternMatrixService *service.PatternMatrixService
	TrendService         *service.TrendService
	SiteStatsService     *service.SiteStatsService
	GamedataService      *service.GamedataService
}

func RegisterTestController(v2 *server.V2, c TestController) {
	v2.Get("/refresh/matrix/:server", c.RefreshAllDropMatrixElements)
	v2.Get("/refresh/pattern/:server", c.RefreshAllPatternMatrixElements)
	v2.Get("/refresh/trend/:server", c.RefreshAllTrendElements)
	v2.Get("/refresh/sitestats/:server", c.RefreshAllSiteStats)

	v2.Get("/test", c.Test)
}

func (c *TestController) RefreshAllDropMatrixElements(ctx *fiber.Ctx) error {
	server := ctx.Params("server")
	return c.DropMatrixService.RefreshAllDropMatrixElements(ctx.Context(), server)
}

func (c *TestController) RefreshAllPatternMatrixElements(ctx *fiber.Ctx) error {
	server := ctx.Params("server")
	return c.PatternMatrixService.RefreshAllPatternMatrixElements(ctx.Context(), server)
}

func (c *TestController) RefreshAllTrendElements(ctx *fiber.Ctx) error {
	server := ctx.Params("server")
	return c.TrendService.RefreshTrendElements(ctx.Context(), server)
}

func (c *TestController) RefreshAllSiteStats(ctx *fiber.Ctx) error {
	server := ctx.Params("server")
	_, err := c.SiteStatsService.RefreshShimSiteStats(ctx.Context(), server)
	return err
}

func (c *TestController) Test(ctx *fiber.Ctx) error {
	importStages, err := c.GamedataService.FetchLatestStages([]string{"act15side_zone1"})
	if err != nil {
		return err
	}
	fmt.Println("\nimportStages:", len(importStages))
	for _, stage := range importStages {
		fmt.Printf("%s, ", stage.StageID)
	}

	var ss *models.Stage
	for _, gamedataStage := range importStages {
		stage, dropInfos, err := c.GamedataService.GenStageAndDropInfosFromGameData(ctx.Context(), "CN", gamedataStage, 0, nil)
		if err != nil {
			return err
		}
		fmt.Printf("\nstageId: %s\n", stage.ArkStageID)
		stageJson, _ := json.Marshal(stage)
		fmt.Printf("stage: %s\n", stageJson)
		for _, dropInfo := range dropInfos {
			dropInfoJson, _ := json.Marshal(dropInfo)
			fmt.Printf("dropInfo: %s\n", dropInfoJson)
		}
	}
	return ctx.JSON(ss)
}

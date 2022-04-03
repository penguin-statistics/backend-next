package controller

import (
	"github.com/gofiber/fiber/v2"
	"go.uber.org/fx"

	"github.com/penguin-statistics/backend-next/internal/server/svr"
	"github.com/penguin-statistics/backend-next/internal/service"
)

type StageController struct {
	fx.In

	StageService *service.StageService
}

func RegisterStageController(v3 *svr.V3, c StageController) {
	v3.Get("/stages", c.GetStages)
	v3.Get("/stages/:stageId", c.GetStageById)
}

func (c *StageController) GetStages(ctx *fiber.Ctx) error {
	stages, err := c.StageService.GetStages(ctx.Context())
	if err != nil {
		return err
	}

	return ctx.JSON(stages)
}

func (c *StageController) GetStageById(ctx *fiber.Ctx) error {
	stageId := ctx.Params("stageId")

	stage, err := c.StageService.GetStageByArkId(ctx.Context(), stageId)
	if err != nil {
		return err
	}

	return ctx.JSON(stage)
}

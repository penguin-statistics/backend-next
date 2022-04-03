package v2

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/fx"

	"github.com/penguin-statistics/backend-next/internal/model/cache"
	"github.com/penguin-statistics/backend-next/internal/pkg/cachectrl"
	"github.com/penguin-statistics/backend-next/internal/server/svr"
	"github.com/penguin-statistics/backend-next/internal/service"
)

type Stage struct {
	fx.In

	StageService *service.Stage
}

func RegisterStage(v2 *svr.V2, c Stage) {
	v2.Get("/stages", c.GetStages)
	v2.Get("/stages/:stageId", c.GetStageByArkId)
}

// @Summary      Get All Stages
// @Tags         Stage
// @Produce      json
// @Success      200     {array}  v2.Stage{existence=model.Existence,code_i18n=model.I18nString}
// @Failure      500     {object}  pgerr.PenguinError "An unexpected error occurred"
// @Router       /PenguinStats/api/v2/stages [GET]
func (c *Stage) GetStages(ctx *fiber.Ctx) error {
	server := ctx.Query("server", "CN")

	stages, err := c.StageService.GetShimStages(ctx.Context(), server)
	if err != nil {
		return err
	}
	var lastModifiedTime time.Time
	if err := cache.LastModifiedTime.Get("[shimStages#server:"+server+"]", &lastModifiedTime); err != nil {
		lastModifiedTime = time.Now()
	}
	cachectrl.OptIn(ctx, lastModifiedTime)
	return ctx.JSON(stages)
}

// @Summary      Get an Stage with ID
// @Tags         Stage
// @Produce      json
// @Param        stageId  path      int  true  "Stage ID"
// @Success      200     {object}  v2.Stage{existence=model.Existence,code_i18n=model.I18nString}
// @Failure      400     {object}  pgerr.PenguinError "Invalid or missing stageId. Notice that this shall be the **string ID** of the stage, instead of the internally used numerical ID of the stage."
// @Failure      500     {object}  pgerr.PenguinError "An unexpected error occurred"
// @Router       /PenguinStats/api/v2/stages/{stageId} [GET]
func (c *Stage) GetStageByArkId(ctx *fiber.Ctx) error {
	stageId := ctx.Params("stageId")
	server := ctx.Query("server", "CN")

	stage, err := c.StageService.GetShimStageByArkId(ctx.Context(), stageId, server)
	if err != nil {
		return err
	}
	return ctx.JSON(stage)
}

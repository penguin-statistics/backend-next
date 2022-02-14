package shims

import (
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/models/cache"
	"github.com/penguin-statistics/backend-next/internal/server"
	"github.com/penguin-statistics/backend-next/internal/service"
)

type StageController struct {
	StageService *service.StageService
}

func RegisterStageController(v2 *server.V2, stageService *service.StageService) {
	c := &StageController{
		StageService: stageService,
	}

	v2.Get("/stages", c.GetStages)
	v2.Get("/stages/:stageId", c.GetStageByArkId)
}

// @Summary      Get All Stages
// @Tags         Stage
// @Produce      json
// @Success      200     {array}  shims.Stage{existence=models.Existence,code_i18n=models.I18nString}
// @Failure      500     {object}  errors.PenguinError "An unexpected error occurred"
// @Router       /PenguinStats/api/v2/stages [GET]
// @Deprecated
func (c *StageController) GetStages(ctx *fiber.Ctx) error {
	server := ctx.Query("server", "CN")

	stages, err := c.StageService.GetShimStages(ctx, server)
	if err != nil {
		return err
	}
	var lastModifiedTime time.Time
	if err := cache.LastModifiedTime.Get("[shimStages#server:"+server+"]", &lastModifiedTime); err != nil {
		lastModifiedTime = time.Now()
	}
	ctx.Response().Header.SetLastModified(lastModifiedTime)
	return ctx.JSON(stages)
}

// @Summary      Get an Stage with ID
// @Tags         Stage
// @Produce      json
// @Param        stageId  path      int  true  "Stage ID"
// @Success      200     {object}  shims.Stage{existence=models.Existence,code_i18n=models.I18nString}
// @Failure      400     {object}  errors.PenguinError "Invalid or missing stageId. Notice that this shall be the **string ID** of the stage, instead of the internally used numerical ID of the stage."
// @Failure      500     {object}  errors.PenguinError "An unexpected error occurred"
// @Router       /PenguinStats/api/v2/stages/{stageId} [GET]
// @Deprecated
func (c *StageController) GetStageByArkId(ctx *fiber.Ctx) error {
	stageId := ctx.Params("stageId")
	server := ctx.Query("server", "CN")

	stage, err := c.StageService.GetShimStageByArkId(ctx, stageId, server)
	if err != nil {
		return err
	}
	return ctx.JSON(stage)
}

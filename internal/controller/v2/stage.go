package v2

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/fx"

	"exusiai.dev/backend-next/internal/model/cache"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
	"exusiai.dev/backend-next/internal/pkg/cachectrl"
	"exusiai.dev/backend-next/internal/pkg/middlewares"
	"exusiai.dev/backend-next/internal/server/svr"
	"exusiai.dev/backend-next/internal/service"
)

var _ modelv2.Stage

type Stage struct {
	fx.In

	StageService *service.Stage
}

func RegisterStage(v2 *svr.V2, c Stage) {
	v2.Get("/stages", middlewares.ValidateServerAsQuery, c.GetStages)
	v2.Get("/stages/:stageId", middlewares.ValidateServerAsQuery, c.GetStageByArkId)
}

// @Summary  Get All Stages
// @Tags     Stage
// @Produce  json
// @Success  200  {array}   modelv2.Stage{existence=model.Existence,code_i18n=model.I18nString}
// @Failure  500  {object}  pgerr.PenguinError  "An unexpected error occurred"
// @Router   /PenguinStats/api/v2/stages [GET]
func (c *Stage) GetStages(ctx *fiber.Ctx) error {
	server := ctx.Query("server", "CN")

	stages, err := c.StageService.GetShimStages(ctx.UserContext(), server)
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

// @Summary  Get a Stage with ID
// @Tags     Stage
// @Produce  json
// @Param    stageId  path      int  true  "Stage ID"
// @Success  200      {object}  modelv2.Stage{existence=model.Existence,code_i18n=model.I18nString}
// @Failure  400      {object}  pgerr.PenguinError  "Invalid or missing stageId. Notice that this shall be the **string ID** of the stage, instead of the internally used numerical ID of the stage."
// @Failure  500      {object}  pgerr.PenguinError  "An unexpected error occurred"
// @Router   /PenguinStats/api/v2/stages/{stageId} [GET]
func (c *Stage) GetStageByArkId(ctx *fiber.Ctx) error {
	stageId := ctx.Params("stageId")
	server := ctx.Query("server", "CN")

	stage, err := c.StageService.GetShimStageByArkId(ctx.UserContext(), stageId, server)
	if err != nil {
		return err
	}
	return ctx.JSON(stage)
}

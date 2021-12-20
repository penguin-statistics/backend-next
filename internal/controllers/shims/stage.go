package shims

import (
	"github.com/penguin-statistics/backend-next/internal/models/shims"
	"github.com/penguin-statistics/backend-next/internal/repos"
	"github.com/penguin-statistics/backend-next/internal/server"

	"github.com/tidwall/gjson"

	"github.com/gofiber/fiber/v2"
)

type StageController struct {
	repo *repos.StageRepo
}

func RegisterStageController(v2 *server.V2, repo *repos.StageRepo) {
	c := &StageController{
		repo: repo,
	}

	v2.Get("/stages", c.GetStages)
	v2.Get("/stages/:stageId", c.GetStageByArkId)
}

func (c *StageController) applyShim(stage *shims.Stage) {
	codeI18n := gjson.ParseBytes(stage.CodeI18n)
	stage.Code = codeI18n.Map()["zh"].String()

	if stage.Zone != nil {
		stage.ArkZoneID = stage.Zone.ArkZoneID
	}

	for _, i := range stage.DropInfos {
		if i.Item != nil {
			i.ArkItemID = i.Item.ArkItemID
		}
		if i.Stage != nil {
			i.ArkStageID = i.Stage.ArkStageID
		}
	}
}

// @Summary      Get all Stages
// @Tags         Stage
// @Produce      json
// @Success      200     {array}  shims.Stage{existence=models.Existence,code_i18n=models.I18nString}
// @Failure      500     {object}  errors.PenguinError "An unexpected error occurred"
// @Router       /PenguinStats/v2/stages [GET]
// @Deprecated
func (c *StageController) GetStages(ctx *fiber.Ctx) error {
	server := ctx.Query("server", "CN")

	items, err := c.repo.GetShimStages(ctx.Context(), server)
	if err != nil {
		return err
	}

	for _, i := range items {
		c.applyShim(i)
	}

	return ctx.JSON(items)
}

// @Summary      Get an Stage with ID
// @Tags         Stage
// @Produce      json
// @Param        stageId  path      int  true  "Stage ID"
// @Success      200     {object}  shims.Stage{existence=models.Existence,code_i18n=models.I18nString}
// @Failure      400     {object}  errors.PenguinError "Invalid or missing stageId. Notice that this shall be the **string ID** of the stage, instead of the internally used numerical ID of the stage."
// @Failure      500     {object}  errors.PenguinError "An unexpected error occurred"
// @Router       /PenguinStats/v2/stages/{stageId} [GET]
// @Deprecated
func (c *StageController) GetStageByArkId(ctx *fiber.Ctx) error {
	stageId := ctx.Params("stageId")
	server := ctx.Query("server", "CN")

	item, err := c.repo.GetShimStageByArkId(ctx.Context(), stageId, server)
	if err != nil {
		return err
	}

	c.applyShim(item)

	return ctx.JSON(item)
}

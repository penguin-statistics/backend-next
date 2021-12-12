package controllers

import (
	"github.com/penguin-statistics/backend-next/internal/repos"
	"github.com/penguin-statistics/backend-next/internal/server"

	"github.com/go-redis/redis/v8"
	"github.com/gofiber/fiber/v2"
)

type StageController struct {
	repo  *repos.StageRepo
	redis *redis.Client
}

func RegisterStageController(v3 *server.V3, repo *repos.StageRepo, redis *redis.Client) {
	c := &StageController{
		repo:  repo,
		redis: redis,
	}

	v3.Get("/stages", c.GetStages)
	v3.Get("/stages/:stageId", c.GetStageById)
}

// @Summary      Get all Stages
// @Tags         Stage
// @Produce      json
// @Success      200     {array}  models.Stage{existence=models.Existence,code=models.I18nString}
// @Failure      500     {object}  errors.PenguinError "An unexpected error occurred"
// @Router       /v3/stages [GET]
func (c *StageController) GetStages(ctx *fiber.Ctx) error {
	stages, err := c.repo.GetStages(ctx.Context())
	if err != nil {
		return err
	}

	return ctx.JSON(stages)
}

// @Summary      Get an Stage with ID
// @Tags         Stage
// @Produce      json
// @Param        stageId  path      int  true  "Stage ID"
// @Success      200     {object}  models.Stage{existence=models.Existence,code=models.I18nString}
// @Failure      400     {object}  errors.PenguinError "Invalid or missing stageId. Notice that this shall be the **string ID** of the stage, instead of the internally used numerical ID of the stage."
// @Failure      500     {object}  errors.PenguinError "An unexpected error occurred"
// @Router       /v3/stages/{stageId} [GET]
func (c *StageController) GetStageById(ctx *fiber.Ctx) error {
	stageId := ctx.Params("stageId")

	stage, err := c.repo.GetStageByArkId(ctx.Context(), stageId)
	if err != nil {
		return err
	}

	return ctx.JSON(stage)
}

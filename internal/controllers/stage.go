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

// GetStages godoc
// @Summary      Get all stages
// @Description  Get all stages
// @Tags         Stage
// @Produce      json
// @Success      200     {array}  models.PStage{existence=models.Existence,code=models.I18nString}
// @Failure      500     {object}  errors.PenguinError "An unexpected error occurred"
// @Router       /v3/stages [GET]
func (c *StageController) GetStages(ctx *fiber.Ctx) error {
	stages, err := c.repo.GetStages(ctx.Context())
	if err != nil {
		return err
	}

	return ctx.JSON(stages)
}

// GetStageById godoc
// @Summary      Get an Stage with numerical ID
// @Description  Get an Stage using the stage's numerical ID
// @Tags         Stage
// @Produce      json
// @Param        stageId  path      int  true  "Numerical Stage ID"
// @Success      200     {object}  models.PStage{existence=models.Existence,code=models.I18nString}
// @Failure      400     {object}  errors.PenguinError "Invalid or missing stageId. Notice that this shall be the **numerical ID** of the stage, instead of the previously used string form **arkStageId** of the stage."
// @Failure      500     {object}  errors.PenguinError "An unexpected error occurred"
// @Router       /v3/stages/{stageId} [GET]
func (c *StageController) GetStageById(ctx *fiber.Ctx) error {
	stageId := ctx.Params("stageId")

	stage, err := c.repo.GetStageById(ctx.Context(), stageId)
	if err != nil {
		return err
	}

	return ctx.JSON(stage)
}

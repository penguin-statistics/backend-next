package service

import (
	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/shims"
	"github.com/penguin-statistics/backend-next/internal/repos"
)

type StageService struct {
	StageRepo *repos.StageRepo
}

func NewStageService(stageRepo *repos.StageRepo) *StageService {
	return &StageService{
		StageRepo: stageRepo,
	}
}

func (s *StageService) GetStages(ctx *fiber.Ctx) ([]*models.Stage, error) {
	return s.StageRepo.GetStages(ctx.Context())
}

func (s *StageService) GetStageById(ctx *fiber.Ctx, stageId int) (*models.Stage, error) {
	return s.StageRepo.GetStageById(ctx.Context(), stageId)
}

func (s *StageService) GetStageByArkId(ctx *fiber.Ctx, stageArkId string) (*models.Stage, error) {
	return s.StageRepo.GetStageByArkId(ctx.Context(), stageArkId)
}

func (s *StageService) GetShimStages(ctx *fiber.Ctx, server string) ([]*shims.Stage, error) {
	return s.StageRepo.GetShimStages(ctx.Context(), server)
}

func (s *StageService) GetShimStageByArkId(ctx *fiber.Ctx, stageId string, server string) (*shims.Stage, error) {
	return s.StageRepo.GetShimStageByArkId(ctx.Context(), stageId, server)
}

func (s *StageService) GetStageExtraProcessTypeByArkId(ctx *fiber.Ctx, arkStageId string) (string, error) {
	return s.StageRepo.GetStageExtraProcessTypeByArkId(ctx.Context(), arkStageId)
}

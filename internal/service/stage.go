package service

import (
	"github.com/ahmetb/go-linq/v3"
	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/models"
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

func (s *StageService) GetStagesMap(ctx *fiber.Ctx) (map[int]*models.Stage, error) {
	stages, err := s.StageRepo.GetStages(ctx.Context())
	if err != nil {
		return nil, err
	}
	stagesMap := make(map[int]*models.Stage)
	linq.From(stages).
		ToMapByT(
			&stagesMap,
			func(stage *models.Stage) int { return stage.StageID },
			func(stage *models.Stage) *models.Stage { return stage })
	return stagesMap, nil
}

func (s *StageService) GetStageById(ctx *fiber.Ctx, stageId int) (*models.Stage, error) {
	return s.StageRepo.GetStageById(ctx.Context(), stageId)
}

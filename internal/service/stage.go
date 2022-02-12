package service

import (
	"github.com/gofiber/fiber/v2"
	"github.com/tidwall/gjson"

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

// Cache: Stages, 24hrs
func (s *StageService) GetStages(ctx *fiber.Ctx) ([]*models.Stage, error) {
	return s.StageRepo.GetStages(ctx.Context())
}

// Cache: StageById#{stageId}, 24hrs
func (s *StageService) GetStageById(ctx *fiber.Ctx, stageId int) (*models.Stage, error) {
	return s.StageRepo.GetStageById(ctx.Context(), stageId)
}

// Cache: StageByArkId#{stageArkId}, 24hrs
func (s *StageService) GetStageByArkId(ctx *fiber.Ctx, stageArkId string) (*models.Stage, error) {
	return s.StageRepo.GetStageByArkId(ctx.Context(), stageArkId)
}

// Cache: ShimStages#{server}, 24hrs
func (s *StageService) GetShimStages(ctx *fiber.Ctx, server string) ([]*shims.Stage, error) {
	stages, err := s.StageRepo.GetShimStages(ctx.Context(), server)
	if err != nil {
		return nil, err
	}
	for _, i := range stages {
		s.applyShim(i)
	}
	return stages, nil
}

// Cache: ShimStageByArkId#{server}|{stageId}, 24hrs
func (s *StageService) GetShimStageByArkId(ctx *fiber.Ctx, stageId string, server string) (*shims.Stage, error) {
	stage, err := s.StageRepo.GetShimStageByArkId(ctx.Context(), stageId, server)
	if err != nil {
		return nil, err
	}
	s.applyShim(stage)
	return stage, nil
}

func (s *StageService) GetStageExtraProcessTypeByArkId(ctx *fiber.Ctx, arkStageId string) (string, error) {
	return s.StageRepo.GetStageExtraProcessTypeByArkId(ctx.Context(), arkStageId)
}

func (s *StageService) applyShim(stage *shims.Stage) {
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

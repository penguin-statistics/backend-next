package service

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/tidwall/gjson"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/cache"
	"github.com/penguin-statistics/backend-next/internal/models/shims"
	"github.com/penguin-statistics/backend-next/internal/pkg/errors"
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

// Cache: (singular) stages, 24hrs
func (s *StageService) GetStages(ctx *fiber.Ctx) ([]*models.Stage, error) {
	var stages []*models.Stage
	err := cache.Stages.Get(&stages)
	if err == nil {
		return stages, nil
	}

	stages, err = s.StageRepo.GetStages(ctx.Context())
	go cache.Stages.Set(stages, 24*time.Hour)
	return stages, err
}

func (s *StageService) GetStageById(ctx *fiber.Ctx, stageId int) (*models.Stage, error) {
	stagesMapById, err := s.GetStagesMapById(ctx)
	if err != nil {
		return nil, err
	}
	stage, ok := stagesMapById[stageId]
	if !ok {
		return nil, nil
	}
	return stage, nil
}

// Cache: stage#arkStageId:{arkStageId}, 24hrs
func (s *StageService) GetStageByArkId(ctx *fiber.Ctx, arkStageId string) (*models.Stage, error) {
	var stage models.Stage
	err := cache.StageByArkId.Get(arkStageId, &stage)
	if err == nil {
		return &stage, nil
	}

	dbStage, err := s.StageRepo.GetStageByArkId(ctx.Context(), arkStageId)
	go cache.StageByArkId.Set(arkStageId, dbStage, 24*time.Hour)
	return dbStage, err
}

func (s *StageService) SearchStageByCode(ctx *fiber.Ctx, code string) (*models.Stage, error) {
	return s.StageRepo.SearchStageByCode(ctx.Context(), code)
}

// Cache: shimStages#server:{server}, 24hrs; records last modified time
func (s *StageService) GetShimStages(ctx *fiber.Ctx, server string) ([]*shims.Stage, error) {
	var stages []*shims.Stage
	err := cache.ShimStages.Get(server, &stages)
	if err == nil {
		return stages, nil
	}

	stages, err = s.StageRepo.GetShimStages(ctx.Context(), server)
	if err != nil {
		return nil, err
	}
	for _, i := range stages {
		s.applyShim(i)
	}
	if err := cache.ShimStages.Set(server, stages, 24*time.Hour); err == nil {
		cache.LastModifiedTime.Set("[shimStages#server:"+server+"]", time.Now(), 0)
	}
	return stages, nil
}

// Cache: shimStage#server|arkStageId:{server}|{arkStageId}, 24hrs
func (s *StageService) GetShimStageByArkId(ctx *fiber.Ctx, arkStageId string, server string) (*shims.Stage, error) {
	var stage shims.Stage
	err := cache.ShimStageByArkId.Get(arkStageId, &stage)
	if err == nil {
		return &stage, nil
	}

	dbStage, err := s.StageRepo.GetShimStageByArkId(ctx.Context(), arkStageId, server)
	if err != nil {
		return nil, err
	}
	s.applyShim(dbStage)
	go cache.ShimStageByArkId.Set(arkStageId, dbStage, 24*time.Hour)
	return dbStage, nil
}

func (s *StageService) GetStageExtraProcessTypeByArkId(ctx *fiber.Ctx, arkStageId string) (string, error) {
	return s.StageRepo.GetStageExtraProcessTypeByArkId(ctx.Context(), arkStageId)
}

// Cache: (singular) stagesMapById, 24hrs
func (s *StageService) GetStagesMapById(ctx *fiber.Ctx) (map[int]*models.Stage, error) {
	var stagesMapById map[int]*models.Stage
	cache.StagesMapById.MutexGetSet(&stagesMapById, func() (interface{}, error) {
		stages, err := s.GetStages(ctx)
		if err != nil {
			return nil, err
		}
		stagesMapById := make(map[int]*models.Stage)
		for _, stage := range stages {
			stagesMapById[stage.StageID] = stage
		}
		return stagesMapById, nil
	}, 24*time.Hour)
	return stagesMapById, nil
}

// Cache: (singular) stagesMapByArkId, 24hrs
func (s *StageService) GetStagesMapByArkId(ctx *fiber.Ctx) (map[string]*models.Stage, error) {
	var stagesMapByArkId map[string]*models.Stage
	cache.StagesMapByArkId.MutexGetSet(&stagesMapByArkId, func() (interface{}, error) {
		stages, err := s.GetStages(ctx)
		if err != nil {
			return nil, err
		}
		stagesMapByArkId := make(map[string]*models.Stage)
		for _, stage := range stages {
			stagesMapByArkId[stage.ArkStageID] = stage
		}
		return stagesMapByArkId, nil
	}, 24*time.Hour)
	return stagesMapByArkId, nil
}

func (s *StageService) GetGachaBoxStages(ctx *fiber.Ctx) ([]*models.Stage, error) {
	stages, err := s.StageRepo.GetGachaBoxStages(ctx.Context())
	if err == errors.ErrNotFound {
		return make([]*models.Stage, 0), nil
	} else if err != nil {
		return nil, err
	}
	return stages, nil
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

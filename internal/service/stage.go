package service

import (
	"context"
	"time"

	"github.com/ahmetb/go-linq/v3"
	"github.com/tidwall/gjson"
	"gopkg.in/guregu/null.v3"

	"github.com/penguin-statistics/backend-next/internal/constants"
	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/cache"
	modelv2 "github.com/penguin-statistics/backend-next/internal/models/v2"
	"github.com/penguin-statistics/backend-next/internal/pkg/pgerr"
	"github.com/penguin-statistics/backend-next/internal/repo"
)

type StageService struct {
	StageRepo *repo.StageRepo
}

func NewStageService(stageRepo *repo.StageRepo) *StageService {
	return &StageService{
		StageRepo: stageRepo,
	}
}

// Cache: (singular) stages, 24hrs
func (s *StageService) GetStages(ctx context.Context) ([]*models.Stage, error) {
	var stages []*models.Stage
	err := cache.Stages.Get(&stages)
	if err == nil {
		return stages, nil
	}

	stages, err = s.StageRepo.GetStages(ctx)
	go cache.Stages.Set(stages, 24*time.Hour)
	return stages, err
}

func (s *StageService) GetStageById(ctx context.Context, stageId int) (*models.Stage, error) {
	stagesMapById, err := s.GetStagesMapById(ctx)
	if err != nil {
		return nil, err
	}
	stage, ok := stagesMapById[stageId]
	if !ok {
		return nil, pgerr.ErrNotFound
	}
	return stage, nil
}

// Cache: stage#arkStageId:{arkStageId}, 24hrs
func (s *StageService) GetStageByArkId(ctx context.Context, arkStageId string) (*models.Stage, error) {
	var stage models.Stage
	err := cache.StageByArkID.Get(arkStageId, &stage)
	if err == nil {
		return &stage, nil
	}

	dbStage, err := s.StageRepo.GetStageByArkId(ctx, arkStageId)
	if err != nil {
		return nil, err
	}
	go cache.StageByArkID.Set(arkStageId, *dbStage, 24*time.Hour)
	return dbStage, nil
}

func (s *StageService) SearchStageByCode(ctx context.Context, code string) (*models.Stage, error) {
	return s.StageRepo.SearchStageByCode(ctx, code)
}

// Cache: shimStages#server:{server}, 24hrs; records last modified time
func (s *StageService) GetShimStages(ctx context.Context, server string) ([]*modelv2.Stage, error) {
	var stages []*modelv2.Stage
	err := cache.ShimStages.Get(server, &stages)
	if err == nil {
		return stages, nil
	}

	stages, err = s.StageRepo.GetShimStages(ctx, server)
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
func (s *StageService) GetShimStageByArkId(ctx context.Context, arkStageId string, server string) (*modelv2.Stage, error) {
	var stage modelv2.Stage
	err := cache.ShimStageByArkID.Get(arkStageId, &stage)
	if err == nil {
		return &stage, nil
	}

	dbStage, err := s.StageRepo.GetShimStageByArkId(ctx, arkStageId, server)
	if err != nil {
		return nil, err
	}
	s.applyShim(dbStage)
	go cache.ShimStageByArkID.Set(arkStageId, *dbStage, 24*time.Hour)
	return dbStage, nil
}

func (s *StageService) GetStageExtraProcessTypeByArkId(ctx context.Context, arkStageId string) (null.String, error) {
	return s.StageRepo.GetStageExtraProcessTypeByArkId(ctx, arkStageId)
}

// Cache: (singular) stagesMapById, 24hrs
func (s *StageService) GetStagesMapById(ctx context.Context) (map[int]*models.Stage, error) {
	var stagesMapById map[int]*models.Stage
	cache.StagesMapByID.MutexGetSet(&stagesMapById, func() (map[int]*models.Stage, error) {
		stages, err := s.GetStages(ctx)
		if err != nil {
			return nil, err
		}
		s := make(map[int]*models.Stage)
		for _, stage := range stages {
			s[stage.StageID] = stage
		}
		return s, nil
	}, 24*time.Hour)
	return stagesMapById, nil
}

// Cache: (singular) stagesMapByArkId, 24hrs
func (s *StageService) GetStagesMapByArkId(ctx context.Context) (map[string]*models.Stage, error) {
	var stagesMapByArkId map[string]*models.Stage
	cache.StagesMapByArkID.MutexGetSet(&stagesMapByArkId, func() (map[string]*models.Stage, error) {
		stages, err := s.GetStages(ctx)
		if err != nil {
			return nil, err
		}
		s := make(map[string]*models.Stage)
		for _, stage := range stages {
			s[stage.ArkStageID] = stage
		}
		return s, nil
	}, 24*time.Hour)
	return stagesMapByArkId, nil
}

func (s *StageService) GetGachaBoxStages(ctx context.Context) ([]*models.Stage, error) {
	stages, err := s.StageRepo.GetGachaBoxStages(ctx)
	if err == pgerr.ErrNotFound {
		return make([]*models.Stage, 0), nil
	} else if err != nil {
		return nil, err
	}
	return stages, nil
}

func (s *StageService) applyShim(stage *modelv2.Stage) {
	codeI18n := gjson.ParseBytes(stage.CodeI18n)
	stage.Code = codeI18n.Map()["zh"].String()

	if stage.Zone != nil {
		stage.ArkZoneID = stage.Zone.ArkZoneID
	}

	if !stage.Sanity.Valid {
		stage.Sanity = null.NewInt(constants.DefaultNullSanity, true)
	}

	recognitionOnlyArkItemIds := make([]string, 0)
	linq.From(stage.DropInfos).
		WhereT(func(dropInfo *modelv2.DropInfo) bool {
			if dropInfo.DropType == constants.DropTypeRecognitionOnly {
				extras := gjson.ParseBytes(dropInfo.Extras)
				if !extras.IsObject() {
					return false
				}
				recognitionOnlyArkItemIds = append(recognitionOnlyArkItemIds, extras.Get("arkItemId").Value().(string))
			}
			return dropInfo.DropType != constants.DropTypeRecognitionOnly
		}).
		ToSlice(&stage.DropInfos)
	stage.RecognitionOnly = recognitionOnlyArkItemIds

	for _, i := range stage.DropInfos {
		if i.Item != nil {
			i.ArkItemID = i.Item.ArkItemID
		}
		if i.Stage != nil {
			i.ArkStageID = i.Stage.ArkStageID
		}
		i.DropType = constants.DropTypeReversedMap[i.DropType]
	}
}

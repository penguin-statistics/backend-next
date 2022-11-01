package service

import (
	"context"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/ahmetb/go-linq/v3"
	"github.com/tidwall/gjson"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/cache"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
	"exusiai.dev/backend-next/internal/pkg/pgerr"
	"exusiai.dev/backend-next/internal/repo"
)

type Stage struct {
	StageRepo *repo.Stage
}

func NewStage(stageRepo *repo.Stage) *Stage {
	return &Stage{
		StageRepo: stageRepo,
	}
}

// Cache: (singular) stages, 1 hr
func (s *Stage) GetStages(ctx context.Context) ([]*model.Stage, error) {
	var stages []*model.Stage
	err := cache.Stages.Get(&stages)
	if err == nil {
		return stages, nil
	}

	stages, err = s.StageRepo.GetStages(ctx)
	cache.Stages.Set(stages, time.Minute*5)
	return stages, err
}

func (s *Stage) GetStageById(ctx context.Context, stageId int) (*model.Stage, error) {
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

// Cache: stage#arkStageId:{arkStageId}, 1 hr
func (s *Stage) GetStageByArkId(ctx context.Context, arkStageId string) (*model.Stage, error) {
	var stage model.Stage
	err := cache.StageByArkID.Get(arkStageId, &stage)
	if err == nil {
		return &stage, nil
	}

	dbStage, err := s.StageRepo.GetStageByArkId(ctx, arkStageId)
	if err != nil {
		return nil, err
	}
	cache.StageByArkID.Set(arkStageId, *dbStage, time.Minute*5)
	return dbStage, nil
}

func (s *Stage) SearchStageByCode(ctx context.Context, code string) (*model.Stage, error) {
	return s.StageRepo.SearchStageByCode(ctx, code)
}

// Cache: shimStages#server:{server}, 1 hr; records last modified time
func (s *Stage) GetShimStages(ctx context.Context, server string) ([]*modelv2.Stage, error) {
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
	cache.ShimStages.Set(server, stages, time.Minute*5)
	cache.LastModifiedTime.Set("[shimStages#server:"+server+"]", time.Now(), 0)
	return stages, nil
}

// Not Cached
func (s *Stage) GetShimStagesForFakeTime(ctx context.Context, server string, t time.Time) ([]*modelv2.Stage, error) {
	stages, err := s.StageRepo.GetShimStagesForFakeTime(ctx, server, t)
	if err != nil {
		return nil, err
	}
	for _, i := range stages {
		s.applyShim(i)
	}
	return stages, nil
}

// Cache: shimStage#server|arkStageId:{server}|{arkStageId}, 1 hr
func (s *Stage) GetShimStageByArkId(ctx context.Context, arkStageId string, server string) (*modelv2.Stage, error) {
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
	cache.ShimStageByArkID.Set(arkStageId, *dbStage, time.Minute*5)
	return dbStage, nil
}

func (s *Stage) GetStageExtraProcessTypeByArkId(ctx context.Context, arkStageId string) (null.String, error) {
	return s.StageRepo.GetStageExtraProcessTypeByArkId(ctx, arkStageId)
}

// Cache: (singular) stagesMapById, 1 hr
func (s *Stage) GetStagesMapById(ctx context.Context) (map[int]*model.Stage, error) {
	var stagesMapById map[int]*model.Stage
	err := cache.StagesMapByID.MutexGetSet(&stagesMapById, func() (map[int]*model.Stage, error) {
		stages, err := s.GetStages(ctx)
		if err != nil {
			return nil, err
		}
		s := make(map[int]*model.Stage)
		for _, stage := range stages {
			s[stage.StageID] = stage
		}
		return s, nil
	}, time.Minute*5)
	if err != nil {
		return nil, err
	}
	return stagesMapById, nil
}

// Cache: (singular) stagesMapByArkId, 1 hr
func (s *Stage) GetStagesMapByArkId(ctx context.Context) (map[string]*model.Stage, error) {
	var stagesMapByArkId map[string]*model.Stage
	err := cache.StagesMapByArkID.MutexGetSet(&stagesMapByArkId, func() (map[string]*model.Stage, error) {
		stages, err := s.GetStages(ctx)
		if err != nil {
			return nil, err
		}
		s := make(map[string]*model.Stage)
		for _, stage := range stages {
			s[stage.ArkStageID] = stage
		}
		return s, nil
	}, time.Minute*5)
	if err != nil {
		return nil, err
	}
	return stagesMapByArkId, nil
}

func (s *Stage) GetGachaBoxStages(ctx context.Context) ([]*model.Stage, error) {
	stages, err := s.StageRepo.GetGachaBoxStages(ctx)
	if err == pgerr.ErrNotFound {
		return make([]*model.Stage, 0), nil
	} else if err != nil {
		return nil, err
	}
	return stages, nil
}

func (s *Stage) applyShim(stage *modelv2.Stage) {
	codeI18n := gjson.ParseBytes(stage.CodeI18n)
	stage.Code = codeI18n.Map()["zh"].String()

	if stage.Zone != nil {
		stage.ArkZoneID = stage.Zone.ArkZoneID
	}

	if !stage.Sanity.Valid {
		stage.Sanity = null.NewInt(constant.DefaultNullSanity, true)
	}

	recognitionOnlyArkItemIds := make([]string, 0)
	linq.From(stage.DropInfos).
		WhereT(func(dropInfo *modelv2.DropInfo) bool {
			if dropInfo.DropType == constant.DropTypeRecognitionOnly {
				extras := gjson.ParseBytes(dropInfo.Extras)
				if !extras.IsObject() {
					return false
				}
				recognitionOnlyArkItemIds = append(recognitionOnlyArkItemIds, extras.Get("arkItemId").Value().(string))
			}
			return dropInfo.DropType != constant.DropTypeRecognitionOnly
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
		i.DropType = constant.DropTypeReversedMap[i.DropType]
	}
}

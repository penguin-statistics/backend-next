package service

import (
	"context"

	"github.com/ahmetb/go-linq/v3"
	"github.com/uptrace/bun"

	"github.com/penguin-statistics/backend-next/internal/constants"
	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/cache"
	"github.com/penguin-statistics/backend-next/internal/models/gamedata"
	"github.com/penguin-statistics/backend-next/internal/repos"
)

type AdminService struct {
	DB            *bun.DB
	ZoneRepo      *repos.ZoneRepo
	ActivityRepo  *repos.ActivityRepo
	TimeRangeRepo *repos.TimeRangeRepo
	StageRepo     *repos.StageRepo
	DropInfoRepo  *repos.DropInfoRepo
}

func NewAdminService(db *bun.DB, zoneRepo *repos.ZoneRepo, activityRepo *repos.ActivityRepo, timeRangeRepo *repos.TimeRangeRepo, stageRepo *repos.StageRepo, dropInfoRepo *repos.DropInfoRepo) *AdminService {
	return &AdminService{
		DB:            db,
		ZoneRepo:      zoneRepo,
		ActivityRepo:  activityRepo,
		TimeRangeRepo: timeRangeRepo,
		StageRepo:     stageRepo,
		DropInfoRepo:  dropInfoRepo,
	}
}

func (s *AdminService) SaveRenderedObjects(ctx context.Context, objects *gamedata.RenderedObjects) error {
	var innerErr error
	s.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		zones := []*models.Zone{objects.Zone}
		if err := s.ZoneRepo.SaveZones(ctx, tx, &zones); err != nil {
			innerErr = err
			return err
		}
		zoneId := zones[0].ZoneID

		activities := []*models.Activity{objects.Activity}
		if err := s.ActivityRepo.SaveActivities(ctx, tx, &activities); err != nil {
			innerErr = err
			return err
		}

		timeRanges := []*models.TimeRange{objects.TimeRange}
		if err := s.TimeRangeRepo.SaveTimeRanges(ctx, tx, &timeRanges); err != nil {
			innerErr = err
			return err
		}
		rangeId := timeRanges[0].RangeID

		linq.From(objects.Stages).ForEachT(func(stage *models.Stage) {
			stage.ZoneID = zoneId
		})
		if err := s.StageRepo.SaveStages(ctx, tx, &objects.Stages); err != nil {
			innerErr = err
			return err
		}
		stageIdMap := make(map[string]int)
		linq.From(objects.Stages).ToMapByT(&stageIdMap, func(stage *models.Stage) string { return stage.ArkStageID }, func(stage *models.Stage) int { return stage.StageID })

		dropInfosToSave := make([]*models.DropInfo, 0)
		for arkStageId, dropInfos := range objects.DropInfosMap {
			stageId := stageIdMap[arkStageId]
			for _, dropInfo := range dropInfos {
				dropInfo.StageID = stageId
				dropInfo.RangeID = rangeId
				dropInfosToSave = append(dropInfosToSave, dropInfo)
			}
		}
		if err := s.DropInfoRepo.SaveDropInfos(ctx, tx, &dropInfosToSave); err != nil {
			innerErr = err
			return err
		}

		return nil
	})

	// if no error, purge cache
	if innerErr == nil {
		// zone
		cache.Zones.Delete()
		cache.ShimZones.Delete()

		// activity
		cache.Activities.Delete()
		cache.ShimActivities.Delete()

		// timerange
		cache.TimeRanges.Delete(objects.TimeRange.Server)
		cache.TimeRangesMap.Delete(objects.TimeRange.Server)
		cache.MaxAccumulableTimeRanges.Delete(objects.TimeRange.Server)

		// stage
		cache.Stages.Delete()
		cache.StagesMapById.Delete()
		cache.StagesMapByArkId.Delete()
		for _, server := range constants.Servers {
			cache.ShimStages.Delete(server)
		}
	}

	return innerErr
}

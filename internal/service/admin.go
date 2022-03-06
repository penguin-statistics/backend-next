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
	DB        *bun.DB
	AdminRepo *repos.AdminRepo
}

func NewAdminService(db *bun.DB, adminRepo *repos.AdminRepo) *AdminService {
	return &AdminService{
		DB:        db,
		AdminRepo: adminRepo,
	}
}

func (s *AdminService) SaveRenderedObjects(ctx context.Context, objects *gamedata.RenderedObjects) error {
	var innerErr error
	s.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var zoneId int
		var zones []*models.Zone
		if objects.Zone != nil {
			zones = []*models.Zone{objects.Zone}
			if err := s.AdminRepo.SaveZones(ctx, tx, &zones); err != nil {
				innerErr = err
				return err
			}
			zoneId = zones[0].ZoneID
		}

		if objects.Activity != nil {
			activities := []*models.Activity{objects.Activity}
			if err := s.AdminRepo.SaveActivities(ctx, tx, &activities); err != nil {
				innerErr = err
				return err
			}
		}

		var rangeId int
		var timeRanges []*models.TimeRange
		if objects.TimeRange != nil {
			timeRanges = []*models.TimeRange{objects.TimeRange}
			if err := s.AdminRepo.SaveTimeRanges(ctx, tx, &timeRanges); err != nil {
				innerErr = err
				return err
			}
			rangeId = timeRanges[0].RangeID
		}

		stageIdMap := make(map[string]int)
		if len(objects.Stages) > 0 {
			linq.From(objects.Stages).ForEachT(func(stage *models.Stage) {
				stage.ZoneID = zoneId
			})
			if err := s.AdminRepo.SaveStages(ctx, tx, &objects.Stages); err != nil {
				innerErr = err
				return err
			}
			linq.From(objects.Stages).
				ToMapByT(&stageIdMap,
					func(stage *models.Stage) string { return stage.ArkStageID },
					func(stage *models.Stage) int { return stage.StageID },
				)
		}

		if len(objects.DropInfosMap) > 0 {
			dropInfosToSave := make([]*models.DropInfo, 0)
			for arkStageId, dropInfos := range objects.DropInfosMap {
				stageId := stageIdMap[arkStageId]
				for _, dropInfo := range dropInfos {
					dropInfo.StageID = stageId
					dropInfo.RangeID = rangeId
					dropInfosToSave = append(dropInfosToSave, dropInfo)
				}
			}
			if err := s.AdminRepo.SaveDropInfos(ctx, tx, &dropInfosToSave); err != nil {
				innerErr = err
				return err
			}
		}

		return nil
	})

	// if no error, purge cache
	if innerErr == nil {
		// zone
		if objects.Zone != nil {
			cache.Zones.Delete()
			cache.ShimZones.Delete()
		}

		// activity
		if objects.Activity != nil {
			cache.Activities.Delete()
			cache.ShimActivities.Delete()
		}

		// timerange
		if objects.TimeRange != nil {
			cache.TimeRanges.Delete(objects.TimeRange.Server)
			cache.TimeRangesMap.Delete(objects.TimeRange.Server)
			cache.MaxAccumulableTimeRanges.Delete(objects.TimeRange.Server)
		}

		// stage
		if len(objects.Stages) > 0 {
			cache.Stages.Delete()
			cache.StagesMapById.Delete()
			cache.StagesMapByArkId.Delete()
			for _, server := range constants.Servers {
				cache.ShimStages.Delete(server)
			}
		}
	}

	return innerErr
}

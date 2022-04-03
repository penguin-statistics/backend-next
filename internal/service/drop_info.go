package service

import (
	"context"
	"strconv"
	"time"

	"github.com/ahmetb/go-linq/v3"
	"gopkg.in/guregu/null.v3"

	"github.com/penguin-statistics/backend-next/internal/constants"
	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/cache"
	"github.com/penguin-statistics/backend-next/internal/repo"
)

type DropInfoService struct {
	DropInfoRepo     *repo.DropInfoRepo
	TimeRangeService *TimeRangeService
}

func NewDropInfoService(dropInfoRepo *repo.DropInfoRepo, timeRangeService *TimeRangeService) *DropInfoService {
	return &DropInfoService{
		DropInfoRepo:     dropInfoRepo,
		TimeRangeService: timeRangeService,
	}
}

func (s *DropInfoService) GetDropInfosByServer(ctx context.Context, server string) ([]*models.DropInfo, error) {
	return s.DropInfoRepo.GetDropInfosByServer(ctx, server)
}

func (s *DropInfoService) GetDropInfosWithFilters(ctx context.Context, server string, timeRanges []*models.TimeRange, stageIdFilter []int, itemIdFilter []int) ([]*models.DropInfo, error) {
	return s.DropInfoRepo.GetDropInfosWithFilters(ctx, server, timeRanges, stageIdFilter, itemIdFilter)
}

// Cache: itemDropSet#server|stageId|rangeId:{server}|{stageId}|{rangeId}, 24 hrs
func (s *DropInfoService) GetItemDropSetByStageIdAndRangeId(ctx context.Context, server string, stageId int, rangeId int) ([]int, error) {
	var itemDropSet []int
	key := server + constants.CacheSep + strconv.Itoa(stageId) + constants.CacheSep + strconv.Itoa(rangeId)
	err := cache.ItemDropSetByStageIDAndRangeID.Get(key, &itemDropSet)
	if err == nil {
		return itemDropSet, nil
	}

	itemDropSet, err = s.DropInfoRepo.GetItemDropSetByStageIdAndRangeId(ctx, server, stageId, rangeId)
	if err != nil {
		return nil, err
	}

	go cache.ItemDropSetByStageIDAndRangeID.Set(key, itemDropSet, 24*time.Hour)
	return itemDropSet, nil
}

// Cache: itemDropSet#server|stageId|startTime|endTime:{server}|{stageId}|{startTime}|{endTime}, 24 hrs
func (s *DropInfoService) GetItemDropSetByStageIdAndTimeRange(ctx context.Context, server string, stageId int, startTime *time.Time, endTime *time.Time) ([]int, error) {
	var itemDropSet []int
	key := server + constants.CacheSep + strconv.Itoa(stageId) + constants.CacheSep + strconv.Itoa(int(startTime.UnixMilli())) + constants.CacheSep + strconv.Itoa(int(endTime.UnixMilli()))
	err := cache.ItemDropSetByStageIdAndTimeRange.Get(key, &itemDropSet)
	if err == nil {
		return itemDropSet, nil
	}

	timeRange := &models.TimeRange{
		StartTime: startTime,
		EndTime:   endTime,
	}
	dropInfos, err := s.DropInfoRepo.GetDropInfosWithFilters(ctx, server, []*models.TimeRange{timeRange}, []int{stageId}, nil)
	if err != nil {
		return nil, err
	}
	linq.From(dropInfos).
		SelectT(func(dropInfo *models.DropInfo) null.Int { return dropInfo.ItemID }).
		WhereT(func(itemID null.Int) bool { return itemID.Valid }).
		SelectT(func(itemID null.Int) int { return int(itemID.Int64) }).
		Distinct().
		ToSlice(&itemDropSet)

	go cache.ItemDropSetByStageIdAndTimeRange.Set(key, itemDropSet, 24*time.Hour)
	return itemDropSet, nil
}

func (s *DropInfoService) GetAppearStageIdsByServer(ctx context.Context, server string) ([]int, error) {
	dropInfos, err := s.DropInfoRepo.GetDropInfosByServer(ctx, server)
	if err != nil {
		return nil, err
	}
	var stageIds []int
	linq.From(dropInfos).SelectT(func(dropInfo *models.DropInfo) int { return dropInfo.StageID }).Distinct().ToSlice(&stageIds)
	return stageIds, nil
}

func (s *DropInfoService) GetCurrentDropInfosByServer(ctx context.Context, server string) ([]*models.DropInfo, error) {
	dropInfos, err := s.DropInfoRepo.GetDropInfosByServer(ctx, server)
	if err != nil {
		return nil, err
	}
	currentTimeRanges, err := s.TimeRangeService.GetCurrentTimeRangesByServer(ctx, server)
	if err != nil {
		return nil, err
	}
	currentTimeRangesMap := make(map[int]*models.TimeRange)
	for _, timeRange := range currentTimeRanges {
		currentTimeRangesMap[timeRange.RangeID] = timeRange
	}
	linq.From(dropInfos).WhereT(func(dropInfo *models.DropInfo) bool {
		return currentTimeRangesMap[dropInfo.RangeID] != nil
	}).ToSlice(&dropInfos)
	return dropInfos, nil
}

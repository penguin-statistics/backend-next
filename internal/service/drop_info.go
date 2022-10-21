package service

import (
	"context"
	"strconv"
	"time"

	"github.com/ahmetb/go-linq/v3"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/cache"
	"exusiai.dev/backend-next/internal/repo"
	"exusiai.dev/gommon/constant"
)

type DropInfo struct {
	DropInfoRepo     *repo.DropInfo
	TimeRangeService *TimeRange
}

func NewDropInfo(dropInfoRepo *repo.DropInfo, timeRangeService *TimeRange) *DropInfo {
	return &DropInfo{
		DropInfoRepo:     dropInfoRepo,
		TimeRangeService: timeRangeService,
	}
}

func (s *DropInfo) GetDropInfosByServer(ctx context.Context, server string) ([]*model.DropInfo, error) {
	return s.DropInfoRepo.GetDropInfosByServer(ctx, server)
}

func (s *DropInfo) GetDropInfosWithFilters(ctx context.Context, server string, timeRanges []*model.TimeRange, stageIdFilter []int, itemIdFilter []int) ([]*model.DropInfo, error) {
	return s.DropInfoRepo.GetDropInfosWithFilters(ctx, server, timeRanges, stageIdFilter, itemIdFilter)
}

// Cache: itemDropSet#server|stageId|rangeId:{server}|{stageId}|{rangeId}, 24 hrs
func (s *DropInfo) GetItemDropSetByStageIdAndRangeId(ctx context.Context, server string, stageId int, rangeId int) ([]int, error) {
	var itemDropSet []int
	key := server + constant.CacheSep + strconv.Itoa(stageId) + constant.CacheSep + strconv.Itoa(rangeId)
	err := cache.ItemDropSetByStageIDAndRangeID.Get(key, &itemDropSet)
	if err == nil {
		return itemDropSet, nil
	}

	itemDropSet, err = s.DropInfoRepo.GetItemDropSetByStageIdAndRangeId(ctx, server, stageId, rangeId)
	if err != nil {
		return nil, err
	}

	cache.ItemDropSetByStageIDAndRangeID.Set(key, itemDropSet, time.Minute*5)
	return itemDropSet, nil
}

// Cache: itemDropSet#server|stageId|startTime|endTime:{server}|{stageId}|{startTime}|{endTime}, 24 hrs
func (s *DropInfo) GetItemDropSetByStageIdAndTimeRange(ctx context.Context, server string, stageId int, startTime *time.Time, endTime *time.Time) ([]int, error) {
	var itemDropSet []int
	key := server + constant.CacheSep + strconv.Itoa(stageId) + constant.CacheSep + strconv.Itoa(int(startTime.UnixMilli())) + constant.CacheSep + strconv.Itoa(int(endTime.UnixMilli()))
	err := cache.ItemDropSetByStageIdAndTimeRange.Get(key, &itemDropSet)
	if err == nil {
		return itemDropSet, nil
	}

	timeRange := &model.TimeRange{
		StartTime: startTime,
		EndTime:   endTime,
	}
	dropInfos, err := s.DropInfoRepo.GetDropInfosWithFilters(ctx, server, []*model.TimeRange{timeRange}, []int{stageId}, nil)
	if err != nil {
		return nil, err
	}
	linq.From(dropInfos).
		SelectT(func(dropInfo *model.DropInfo) null.Int { return dropInfo.ItemID }).
		WhereT(func(itemID null.Int) bool { return itemID.Valid }).
		SelectT(func(itemID null.Int) int { return int(itemID.Int64) }).
		Distinct().
		ToSlice(&itemDropSet)

	cache.ItemDropSetByStageIdAndTimeRange.Set(key, itemDropSet, time.Minute*5)
	return itemDropSet, nil
}

func (s *DropInfo) GetAppearStageIdsByServer(ctx context.Context, server string) ([]int, error) {
	dropInfos, err := s.DropInfoRepo.GetDropInfosByServer(ctx, server)
	if err != nil {
		return nil, err
	}
	var stageIds []int
	linq.From(dropInfos).SelectT(func(dropInfo *model.DropInfo) int { return dropInfo.StageID }).Distinct().ToSlice(&stageIds)
	return stageIds, nil
}

func (s *DropInfo) GetCurrentDropInfosByServer(ctx context.Context, server string) ([]*model.DropInfo, error) {
	dropInfos, err := s.DropInfoRepo.GetDropInfosByServer(ctx, server)
	if err != nil {
		return nil, err
	}
	currentTimeRanges, err := s.TimeRangeService.GetCurrentTimeRangesByServer(ctx, server)
	if err != nil {
		return nil, err
	}
	currentTimeRangesMap := make(map[int]*model.TimeRange)
	for _, timeRange := range currentTimeRanges {
		currentTimeRangesMap[timeRange.RangeID] = timeRange
	}
	linq.From(dropInfos).WhereT(func(dropInfo *model.DropInfo) bool {
		return currentTimeRangesMap[dropInfo.RangeID] != nil
	}).ToSlice(&dropInfos)
	return dropInfos, nil
}

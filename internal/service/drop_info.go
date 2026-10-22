package service

import (
	"context"
	"strconv"
	"time"

	"exusiai.dev/gommon/constant"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/cache"
	"exusiai.dev/backend-next/internal/repo"
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
	// the distinct item ids in the order they first appear, nil for none
	seen := make(map[int]struct{})
	for _, dropInfo := range dropInfos {
		if !dropInfo.ItemID.Valid {
			continue
		}
		itemId := int(dropInfo.ItemID.Int64)
		if _, ok := seen[itemId]; ok {
			continue
		}
		seen[itemId] = struct{}{}
		itemDropSet = append(itemDropSet, itemId)
	}

	cache.ItemDropSetByStageIdAndTimeRange.Set(key, itemDropSet, time.Minute*5)
	return itemDropSet, nil
}

func (s *DropInfo) GetAppearStageIdsByServer(ctx context.Context, server string) ([]int, error) {
	dropInfos, err := s.DropInfoRepo.GetDropInfosByServer(ctx, server)
	if err != nil {
		return nil, err
	}
	// the distinct stage ids in the order they first appear, nil for none
	var stageIds []int
	seen := make(map[int]struct{})
	for _, dropInfo := range dropInfos {
		if _, ok := seen[dropInfo.StageID]; ok {
			continue
		}
		seen[dropInfo.StageID] = struct{}{}
		stageIds = append(stageIds, dropInfo.StageID)
	}
	return stageIds, nil
}

// GetOpeningStageIdsByServer returns the distinct ids of the stages of server with a drop info in a current time
// range, reading only those stage ids instead of every drop info of server.
func (s *DropInfo) GetOpeningStageIdsByServer(ctx context.Context, server string) ([]int, error) {
	currentTimeRanges, err := s.TimeRangeService.GetCurrentTimeRangesByServer(ctx, server)
	if err != nil {
		return nil, err
	}
	rangeIds := make([]int, 0, len(currentTimeRanges))
	for _, timeRange := range currentTimeRanges {
		rangeIds = append(rangeIds, timeRange.RangeID)
	}
	return s.DropInfoRepo.GetStageIdsByServerAndRangeIds(ctx, server, rangeIds)
}

func (s *DropInfo) CloneDropInfosFromCN(ctx context.Context, originRangeId int, destRangeId int, server string) error {
	return s.DropInfoRepo.CloneDropInfosFromCN(ctx, originRangeId, destRangeId, server)
}

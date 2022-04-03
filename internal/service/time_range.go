package service

import (
	"context"
	"strconv"
	"time"

	"github.com/ahmetb/go-linq/v3"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/cache"
	"github.com/penguin-statistics/backend-next/internal/repo"
)

type TimeRangeService struct {
	TimeRangeRepo *repo.TimeRange
	DropInfoRepo  *repo.DropInfo
}

func NewTimeRangeService(timeRangeRepo *repo.TimeRange, dropInfoRepo *repo.DropInfo) *TimeRangeService {
	return &TimeRangeService{
		TimeRangeRepo: timeRangeRepo,
		DropInfoRepo:  dropInfoRepo,
	}
}

// Cache: timeRanges#server:{server}, 24hrs
func (s *TimeRangeService) GetTimeRangesByServer(ctx context.Context, server string) ([]*models.TimeRange, error) {
	var timeRanges []*models.TimeRange
	err := cache.TimeRanges.Get(server, &timeRanges)
	if err == nil {
		return timeRanges, nil
	}

	timeRanges, err = s.TimeRangeRepo.GetTimeRangesByServer(ctx, server)
	if err != nil {
		return nil, err
	}
	go cache.TimeRanges.Set(server, timeRanges, 24*time.Hour)
	return timeRanges, nil
}

// Cache: timeRange#rangeId:{rangeId}, 24hrs
func (s *TimeRangeService) GetTimeRangeById(ctx context.Context, rangeId int) (*models.TimeRange, error) {
	var timeRange models.TimeRange
	err := cache.TimeRangeByID.Get(strconv.Itoa(rangeId), &timeRange)
	if err == nil {
		return &timeRange, nil
	}

	slowTimeRange, err := s.TimeRangeRepo.GetTimeRangeById(ctx, rangeId)
	go cache.TimeRangeByID.Set(strconv.Itoa(rangeId), *slowTimeRange, 24*time.Hour)
	return slowTimeRange, err
}

func (s *TimeRangeService) GetCurrentTimeRangesByServer(ctx context.Context, server string) ([]*models.TimeRange, error) {
	timeRanges, err := s.TimeRangeRepo.GetTimeRangesByServer(ctx, server)
	if err != nil {
		return nil, err
	}

	linq.From(timeRanges).WhereT(func(timeRange *models.TimeRange) bool {
		return timeRange.StartTime.Before(time.Now()) && timeRange.EndTime.After(time.Now())
	}).ToSlice(&timeRanges)

	return timeRanges, nil
}

// Cache: timeRangesMap#server:{server}, 24hrs
func (s *TimeRangeService) GetTimeRangesMap(ctx context.Context, server string) (map[int]*models.TimeRange, error) {
	var timeRangesMap map[int]*models.TimeRange
	err := cache.TimeRangesMap.Get(server, &timeRangesMap)
	if err == nil {
		return timeRangesMap, nil
	}

	timeRanges, err := s.TimeRangeRepo.GetTimeRangesByServer(ctx, server)
	if err != nil {
		return nil, err
	}
	timeRangesMap = make(map[int]*models.TimeRange)
	linq.From(timeRanges).
		ToMapByT(
			&timeRangesMap,
			func(timeRange *models.TimeRange) int { return timeRange.RangeID },
			func(timeRange *models.TimeRange) *models.TimeRange { return timeRange })
	go cache.TimeRangesMap.Set(server, timeRangesMap, 24*time.Hour)
	return timeRangesMap, nil
}

// Cache: maxAccumulableTimeRanges#server:{server}, 24hrs
func (s *TimeRangeService) GetMaxAccumulableTimeRangesByServer(ctx context.Context, server string) (map[int]map[int][]*models.TimeRange, error) {
	var maxAccumulableTimeRanges map[int]map[int][]*models.TimeRange
	err := cache.MaxAccumulableTimeRanges.Get(server, &maxAccumulableTimeRanges)
	if err == nil {
		return maxAccumulableTimeRanges, nil
	}

	dropInfos, err := s.DropInfoRepo.GetDropInfosByServer(ctx, server)
	if err != nil {
		return nil, err
	}
	timeRangesMap, err := s.GetTimeRangesMap(ctx, server)
	if err != nil {
		return nil, err
	}
	maxAccumulableTimeRanges = make(map[int]map[int][]*models.TimeRange)
	var groupedResults []linq.Group
	linq.From(dropInfos).
		WhereT(func(dropInfo *models.DropInfo) bool { return dropInfo.ItemID.Valid }).
		GroupByT(
			func(dropInfo *models.DropInfo) int { return dropInfo.StageID },
			func(dropInfo *models.DropInfo) *models.DropInfo { return dropInfo },
		).
		ToSlice(&groupedResults)
	for _, el := range groupedResults {
		stageId := el.Key.(int)
		var groupedResults2 []linq.Group
		linq.From(el.Group).
			GroupByT(
				func(dropInfo any) int { return int(dropInfo.(*models.DropInfo).ItemID.Int64) },
				func(dropInfo any) *models.DropInfo { return dropInfo.(*models.DropInfo) },
			).
			ToSlice(&groupedResults2)
		maxAccumulableTimeRangesForOneStage := make(map[int][]*models.TimeRange)
		for _, el := range groupedResults2 {
			itemId := el.Key.(int)
			var sortedDropInfos []*models.DropInfo
			linq.From(el.Group).
				DistinctByT(
					func(dropInfo *models.DropInfo) int { return dropInfo.RangeID },
				).
				SortT(
					func(a, b *models.DropInfo) bool {
						return timeRangesMap[a.RangeID].StartTime.After(*timeRangesMap[b.RangeID].StartTime)
					}).
				ToSlice(&sortedDropInfos)
			startIdx := len(sortedDropInfos) - 1
			endIdx := 0
			timeRanges := make([]*models.TimeRange, 0)
			for idx, dropInfo := range sortedDropInfos {
				if !dropInfo.Accumulable {
					startIdx = idx
					if idx != 0 {
						startIdx = idx - 1
					}
					break
				}
			}
			for i := endIdx; i <= startIdx; i++ {
				timeRanges = append(timeRanges, timeRangesMap[sortedDropInfos[i].RangeID])
			}
			if len(timeRanges) > 0 {
				maxAccumulableTimeRangesForOneStage[itemId] = timeRanges
			}
		}
		if len(maxAccumulableTimeRangesForOneStage) > 0 {
			maxAccumulableTimeRanges[stageId] = maxAccumulableTimeRangesForOneStage
		}
	}
	go cache.MaxAccumulableTimeRanges.Set(server, maxAccumulableTimeRanges, 24*time.Hour)
	return maxAccumulableTimeRanges, nil
}

func (s *TimeRangeService) GetLatestTimeRangesByServer(ctx context.Context, server string) (map[int]*models.TimeRange, error) {
	dropInfos, err := s.DropInfoRepo.GetDropInfosByServer(ctx, server)
	if err != nil {
		return nil, err
	}
	timeRangesMap, err := s.GetTimeRangesMap(ctx, server)
	if err != nil {
		return nil, err
	}
	var groupedResults []linq.Group
	linq.From(dropInfos).
		WhereT(func(dropInfo *models.DropInfo) bool { return dropInfo.ItemID.Valid }).
		GroupByT(
			func(dropInfo *models.DropInfo) int { return dropInfo.StageID },
			func(dropInfo *models.DropInfo) *models.DropInfo { return dropInfo },
		).
		ToSlice(&groupedResults)
	results := make(map[int]*models.TimeRange)
	for _, el := range groupedResults {
		stageId := el.Key.(int)
		latestDropInfo := linq.From(el.Group).
			Distinct().
			SortT(
				func(a, b *models.DropInfo) bool {
					return timeRangesMap[a.RangeID].StartTime.After(*timeRangesMap[b.RangeID].StartTime)
				}).
			First().(*models.DropInfo)
		results[stageId] = timeRangesMap[latestDropInfo.RangeID]
	}
	return results, nil
}

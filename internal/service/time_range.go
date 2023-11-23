package service

import (
	"context"
	"strconv"
	"time"

	"github.com/ahmetb/go-linq/v3"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/cache"
	"exusiai.dev/backend-next/internal/repo"
)

type TimeRange struct {
	TimeRangeRepo *repo.TimeRange
	DropInfoRepo  *repo.DropInfo
}

func NewTimeRange(timeRangeRepo *repo.TimeRange, dropInfoRepo *repo.DropInfo) *TimeRange {
	return &TimeRange{
		TimeRangeRepo: timeRangeRepo,
		DropInfoRepo:  dropInfoRepo,
	}
}

// Cache: timeRanges#server:{server}, 1 hr
func (s *TimeRange) GetTimeRangesByServer(ctx context.Context, server string) ([]*model.TimeRange, error) {
	var timeRanges []*model.TimeRange
	err := cache.TimeRanges.Get(server, &timeRanges)
	if err == nil {
		return timeRanges, nil
	}

	timeRanges, err = s.TimeRangeRepo.GetTimeRangesByServer(ctx, server)
	if err != nil {
		return nil, err
	}
	cache.TimeRanges.Set(server, timeRanges, time.Minute*5)
	return timeRanges, nil
}

// Cache: timeRange#rangeId:{rangeId}, 1 hr
func (s *TimeRange) GetTimeRangeById(ctx context.Context, rangeId int) (*model.TimeRange, error) {
	var timeRange model.TimeRange
	err := cache.TimeRangeByID.Get(strconv.Itoa(rangeId), &timeRange)
	if err == nil {
		return &timeRange, nil
	}

	slowTimeRange, err := s.TimeRangeRepo.GetTimeRangeById(ctx, rangeId)
	cache.TimeRangeByID.Set(strconv.Itoa(rangeId), *slowTimeRange, time.Minute*5)
	return slowTimeRange, err
}

func (s *TimeRange) GetCurrentTimeRangesByServer(ctx context.Context, server string) ([]*model.TimeRange, error) {
	timeRanges, err := s.TimeRangeRepo.GetTimeRangesByServer(ctx, server)
	if err != nil {
		return nil, err
	}

	linq.From(timeRanges).WhereT(func(timeRange *model.TimeRange) bool {
		return timeRange.StartTime.Before(time.Now()) && timeRange.EndTime.After(time.Now())
	}).ToSlice(&timeRanges)

	return timeRanges, nil
}

// Cache: timeRangesMap#server:{server}, 5 min
func (s *TimeRange) GetTimeRangesMap(ctx context.Context, server string) (map[int]*model.TimeRange, error) {
	var timeRangesMap map[int]*model.TimeRange
	err := cache.TimeRangesMap.Get(server, &timeRangesMap)
	if err == nil {
		return timeRangesMap, nil
	}

	timeRanges, err := s.TimeRangeRepo.GetTimeRangesByServer(ctx, server)
	if err != nil {
		return nil, err
	}
	timeRangesMap = make(map[int]*model.TimeRange)
	linq.From(timeRanges).
		ToMapByT(
			&timeRangesMap,
			func(timeRange *model.TimeRange) int { return timeRange.RangeID },
			func(timeRange *model.TimeRange) *model.TimeRange { return timeRange })
	cache.TimeRangesMap.Set(server, timeRangesMap, time.Minute*5)
	return timeRangesMap, nil
}

// Cache: maxAccumulableTimeRanges#server:{server}, 5 min
func (s *TimeRange) GetMaxAccumulableTimeRangesByServer(ctx context.Context, server string) (map[int]map[int][]*model.TimeRange, error) {
	var maxAccumulableTimeRanges map[int]map[int][]*model.TimeRange
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
	maxAccumulableTimeRanges = make(map[int]map[int][]*model.TimeRange)
	var groupedResults []linq.Group
	linq.From(dropInfos).
		WhereT(func(dropInfo *model.DropInfo) bool { return dropInfo.ItemID.Valid }).
		GroupByT(
			func(dropInfo *model.DropInfo) int { return dropInfo.StageID },
			func(dropInfo *model.DropInfo) *model.DropInfo { return dropInfo },
		).
		ToSlice(&groupedResults)
	for _, el := range groupedResults {
		stageId := el.Key.(int)
		var groupedResults2 []linq.Group
		linq.From(el.Group).
			GroupByT(
				func(dropInfo any) int { return int(dropInfo.(*model.DropInfo).ItemID.Int64) },
				func(dropInfo any) *model.DropInfo { return dropInfo.(*model.DropInfo) },
			).
			ToSlice(&groupedResults2)
		maxAccumulableTimeRangesForOneStage := make(map[int][]*model.TimeRange)
		for _, el := range groupedResults2 {
			itemId := el.Key.(int)
			var sortedDropInfos []*model.DropInfo
			linq.From(el.Group).
				DistinctByT(
					func(dropInfo *model.DropInfo) int { return dropInfo.RangeID },
				).
				SortT(
					func(a, b *model.DropInfo) bool {
						return timeRangesMap[a.RangeID].StartTime.After(*timeRangesMap[b.RangeID].StartTime)
					}).
				ToSlice(&sortedDropInfos)
			startIdx := len(sortedDropInfos) - 1
			endIdx := 0
			timeRanges := make([]*model.TimeRange, 0)
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
	cache.MaxAccumulableTimeRanges.Set(server, maxAccumulableTimeRanges, time.Minute*5)
	return maxAccumulableTimeRanges, nil
}

// This function will combine time ranges together if they are adjacent
// Cache: allMaxAccumulableTimeRanges#server:{server}, 5 min
func (s *TimeRange) GetAllMaxAccumulableTimeRangesByServer(ctx context.Context, server string) (map[int]map[int][]*model.TimeRange, error) {
	var maxAccumulableTimeRanges map[int]map[int][]*model.TimeRange
	err := cache.AllMaxAccumulableTimeRanges.Get(server, &maxAccumulableTimeRanges)
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
	maxAccumulableTimeRanges = make(map[int]map[int][]*model.TimeRange)
	var groupedResults []linq.Group
	linq.From(dropInfos).
		WhereT(func(dropInfo *model.DropInfo) bool { return dropInfo.ItemID.Valid }).
		GroupByT(
			func(dropInfo *model.DropInfo) int { return dropInfo.StageID },
			func(dropInfo *model.DropInfo) *model.DropInfo { return dropInfo },
		).
		ToSlice(&groupedResults)
	for _, el := range groupedResults {
		stageId := el.Key.(int)
		var groupedResults2 []linq.Group
		linq.From(el.Group).
			GroupByT(
				func(dropInfo any) int { return int(dropInfo.(*model.DropInfo).ItemID.Int64) },
				func(dropInfo any) *model.DropInfo { return dropInfo.(*model.DropInfo) },
			).
			ToSlice(&groupedResults2)
		maxAccumulableTimeRangesForOneStage := make(map[int][]*model.TimeRange)
		for _, el := range groupedResults2 {
			itemId := el.Key.(int)
			var sortedDropInfos []*model.DropInfo
			linq.From(el.Group).
				DistinctByT(
					func(dropInfo *model.DropInfo) int { return dropInfo.RangeID },
				).
				SortT(
					func(a, b *model.DropInfo) bool {
						return timeRangesMap[a.RangeID].StartTime.Before(*timeRangesMap[b.RangeID].StartTime)
					}).
				ToSlice(&sortedDropInfos)

			timeRanges := make([]*model.TimeRange, 0)
			for _, dropInfo := range sortedDropInfos {
				timeRange := timeRangesMap[dropInfo.RangeID]
				if len(timeRanges) == 0 || !dropInfo.Accumulable {
					pureRange := &model.TimeRange{
						StartTime: timeRange.StartTime,
						EndTime:   timeRange.EndTime,
					}
					timeRanges = append(timeRanges, pureRange)
					continue
				}
				lastAddedTimeRange := timeRanges[len(timeRanges)-1]
				if !lastAddedTimeRange.EndTime.Before(*timeRange.StartTime) {
					if lastAddedTimeRange.EndTime.Before(*timeRange.EndTime) {
						lastAddedTimeRange.EndTime = timeRange.EndTime
					}
				} else {
					pureRange := &model.TimeRange{
						StartTime: timeRange.StartTime,
						EndTime:   timeRange.EndTime,
					}
					timeRanges = append(timeRanges, pureRange)
				}
			}
			if len(timeRanges) > 0 {
				maxAccumulableTimeRangesForOneStage[itemId] = timeRanges
			}
		}
		if len(maxAccumulableTimeRangesForOneStage) > 0 {
			maxAccumulableTimeRanges[stageId] = maxAccumulableTimeRangesForOneStage
		}
	}
	cache.AllMaxAccumulableTimeRanges.Set(server, maxAccumulableTimeRanges, time.Minute*5)
	return maxAccumulableTimeRanges, nil
}

// Cache: latestTimeRanges#server:{server}, 5 min
func (s *TimeRange) GetLatestTimeRangesByServer(ctx context.Context, server string) (map[int]*model.TimeRange, error) {
	var results map[int]*model.TimeRange
	err := cache.LatestTimeRanges.Get(server, &results)
	if err == nil {
		return results, nil
	}

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
		WhereT(func(dropInfo *model.DropInfo) bool { return dropInfo.ItemID.Valid }).
		GroupByT(
			func(dropInfo *model.DropInfo) int { return dropInfo.StageID },
			func(dropInfo *model.DropInfo) *model.DropInfo { return dropInfo },
		).
		ToSlice(&groupedResults)
	results = make(map[int]*model.TimeRange)
	for _, el := range groupedResults {
		stageId := el.Key.(int)
		latestDropInfo := linq.From(el.Group).
			Distinct().
			SortT(
				func(a, b *model.DropInfo) bool {
					return timeRangesMap[a.RangeID].StartTime.After(*timeRangesMap[b.RangeID].StartTime)
				}).
			First().(*model.DropInfo)
		results[stageId] = timeRangesMap[latestDropInfo.RangeID]
	}
	cache.LatestTimeRanges.Set(server, results, time.Minute*5)
	return results, nil
}

func (s *TimeRange) GetTimeRangeByServerAndName(ctx context.Context, server string, name string) (*model.TimeRange, error) {
	return s.TimeRangeRepo.GetTimeRangeByServerAndName(ctx, server, name)
}

package service

import (
	"time"

	"github.com/ahmetb/go-linq/v3"
	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/repos"
)

type TimeRangeService struct {
	TimeRangeRepo *repos.TimeRangeRepo
	DropInfoRepo  *repos.DropInfoRepo
}

func NewTimeRangeService(timeRangeRepo *repos.TimeRangeRepo, dropInfoRepo *repos.DropInfoRepo) *TimeRangeService {
	return &TimeRangeService{
		TimeRangeRepo: timeRangeRepo,
		DropInfoRepo:  dropInfoRepo,
	}
}

func (s *TimeRangeService) GetAllTimeRangesByServer(ctx *fiber.Ctx, server string) ([]*models.TimeRange, error) {
	timeRanges, err := s.TimeRangeRepo.GetTimeRangesByServer(ctx.Context(), server)
	if err != nil {
		return nil, err
	}
	return timeRanges, nil
}

func (s *TimeRangeService) GetCurrentTimeRangesByServer(ctx *fiber.Ctx, server string) ([]*models.TimeRange, error) {
	timeRanges, err := s.TimeRangeRepo.GetTimeRangesByServer(ctx.Context(), server)
	if err != nil {
		return nil, err
	}

	linq.From(timeRanges).WhereT(func(timeRange *models.TimeRange) bool {
		return timeRange.StartTime.Before(time.Now()) && timeRange.EndTime.After(time.Now())
	}).ToSlice(&timeRanges)

	return timeRanges, nil
}

func (s *TimeRangeService) GetTimeRangesMap(ctx *fiber.Ctx, server string) (map[int]*models.TimeRange, error) {
	timeRanges, err := s.TimeRangeRepo.GetTimeRangesByServer(ctx.Context(), server)
	if err != nil {
		return nil, err
	}
	timeRangesMap := make(map[int]*models.TimeRange)
	linq.From(timeRanges).
		ToMapByT(
			&timeRangesMap,
			func(timeRange *models.TimeRange) int { return timeRange.RangeID },
			func(timeRange *models.TimeRange) *models.TimeRange { return timeRange })
	return timeRangesMap, nil
}

func (s *TimeRangeService) GetMaxAccumulableTimeRangesByServer(ctx *fiber.Ctx, server string) (map[int]map[int][]*models.TimeRange, error) {
	dropInfos, err := s.DropInfoRepo.GetDropInfosByServer(ctx.Context(), server)
	if err != nil {
		return nil, err
	}
	timeRangesMap, err := s.GetTimeRangesMap(ctx, server)
	if err != nil {
		return nil, err
	}
	maxAccumulableTimeRanges := make(map[int]map[int][]*models.TimeRange, 0)
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
				func(dropInfo interface{}) int { return int(dropInfo.(*models.DropInfo).ItemID.Int64) },
				func(dropInfo interface{}) *models.DropInfo { return dropInfo.(*models.DropInfo) },
			).
			ToSlice(&groupedResults2)
		maxAccumulableTimeRangesForOneStage := make(map[int][]*models.TimeRange)
		for _, el := range groupedResults2 {
			itemId := el.Key.(int)
			var sortedDropInfos []*models.DropInfo
			linq.From(el.Group).
				Distinct().
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
	return maxAccumulableTimeRanges, nil
}

func (s *TimeRangeService) GetLatestTimeRangesByServer(ctx *fiber.Ctx, server string) (map[int]*models.TimeRange, error) {
	dropInfos, err := s.DropInfoRepo.GetDropInfosByServer(ctx.Context(), server)
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

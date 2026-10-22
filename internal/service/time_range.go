package service

import (
	"context"
	"sort"
	"strconv"
	"time"

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

	// filtered in place, as the repo returns a new slice
	currentTimeRanges := timeRanges[:0]
	for _, timeRange := range timeRanges {
		if timeRange.StartTime.Before(time.Now()) && timeRange.EndTime.After(time.Now()) {
			currentTimeRanges = append(currentTimeRanges, timeRange)
		}
	}
	timeRanges = currentTimeRanges

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
	for _, timeRange := range timeRanges {
		timeRangesMap[timeRange.RangeID] = timeRange
	}
	cache.TimeRangesMap.Set(server, timeRangesMap, time.Minute*5)
	return timeRangesMap, nil
}

// GetMaxAccumulableTimeRangesByServer returns, for every stage and item of server, the time ranges of its latest
// accumulable segment (see accumulableSegments), newest first. The personal drop matrices are calculated over them.
// Cache: maxAccumulableTimeRanges#server:{server}, 5 min
func (s *TimeRange) GetMaxAccumulableTimeRangesByServer(ctx context.Context, server string) (map[int]map[int][]*model.TimeRange, error) {
	var maxAccumulableTimeRanges map[int]map[int][]*model.TimeRange
	err := cache.MaxAccumulableTimeRanges.Get(server, &maxAccumulableTimeRanges)
	if err == nil {
		return maxAccumulableTimeRanges, nil
	}

	segmentsByStageAndItem, err := s.getAccumulableSegmentsByServer(ctx, server)
	if err != nil {
		return nil, err
	}
	maxAccumulableTimeRanges = make(map[int]map[int][]*model.TimeRange, len(segmentsByStageAndItem))
	for stageId, segmentsByItem := range segmentsByStageAndItem {
		maxAccumulableTimeRangesForOneStage := make(map[int][]*model.TimeRange, len(segmentsByItem))
		for itemId, segments := range segmentsByItem {
			latest := segments[len(segments)-1]
			timeRanges := make([]*model.TimeRange, len(latest))
			for i, timeRange := range latest {
				timeRanges[len(latest)-1-i] = timeRange
			}
			maxAccumulableTimeRangesForOneStage[itemId] = timeRanges
		}
		maxAccumulableTimeRanges[stageId] = maxAccumulableTimeRangesForOneStage
	}
	cache.MaxAccumulableTimeRanges.Set(server, maxAccumulableTimeRanges, time.Minute*5)
	return maxAccumulableTimeRanges, nil
}

// GetAllMaxAccumulableTimeRangesByServer returns, for every stage and item of server, all of its accumulable
// segments (see accumulableSegments) in chronological order, each as one time range from the start of its first
// time range to the latest end of its time ranges. The global drop matrix is calculated over the last one, and the
// drop matrix elements of a day are rendered per segment.
// Cache: allMaxAccumulableTimeRanges#server:{server}, 5 min
func (s *TimeRange) GetAllMaxAccumulableTimeRangesByServer(ctx context.Context, server string) (map[int]map[int][]*model.TimeRange, error) {
	var maxAccumulableTimeRanges map[int]map[int][]*model.TimeRange
	err := cache.AllMaxAccumulableTimeRanges.Get(server, &maxAccumulableTimeRanges)
	if err == nil {
		return maxAccumulableTimeRanges, nil
	}

	segmentsByStageAndItem, err := s.getAccumulableSegmentsByServer(ctx, server)
	if err != nil {
		return nil, err
	}
	maxAccumulableTimeRanges = make(map[int]map[int][]*model.TimeRange, len(segmentsByStageAndItem))
	for stageId, segmentsByItem := range segmentsByStageAndItem {
		maxAccumulableTimeRangesForOneStage := make(map[int][]*model.TimeRange, len(segmentsByItem))
		for itemId, segments := range segmentsByItem {
			timeRanges := make([]*model.TimeRange, 0, len(segments))
			for _, segment := range segments {
				timeRanges = append(timeRanges, mergeSegment(segment))
			}
			maxAccumulableTimeRangesForOneStage[itemId] = timeRanges
		}
		maxAccumulableTimeRanges[stageId] = maxAccumulableTimeRangesForOneStage
	}
	cache.AllMaxAccumulableTimeRanges.Set(server, maxAccumulableTimeRanges, time.Minute*5)
	return maxAccumulableTimeRanges, nil
}

func (s *TimeRange) getAccumulableSegmentsByServer(ctx context.Context, server string) (map[int]map[int][][]*model.TimeRange, error) {
	dropInfos, err := s.getDropInfosByServer(ctx, server)
	if err != nil {
		return nil, err
	}
	timeRangesMap, err := s.GetTimeRangesMap(ctx, server)
	if err != nil {
		return nil, err
	}
	return accumulableSegments(dropInfos, timeRangesMap), nil
}

// accumulableSegments splits the time ranges of every stage and item into the segments whose drops are counted
// together, the same way for the global and the personal drop matrices (#794).
//
// The time ranges of a stage and item are those of its drop infos, once each, ordered by start time (then by range
// id). DropInfo.Accumulable decides whether the drops of a time range are carried into the time ranges after it:
//   - consecutive accumulable time ranges form one segment;
//   - a time range that is not accumulable is a segment on its own: it is neither merged into the time range before
//     it nor carried into the one after it.
//
// A gap between two time ranges does not split a segment. Every stage and item gets at least one segment, and the
// last one is its latest window: A false, B and C true gives [A] [B C]; A and B false, C true gives [A] [B] [C]; A
// and B true, C false gives [A B] [C]; a single time range that is not accumulable is the latest window by itself.
//
// Segments hold the time ranges of timeRangesMap in chronological order; dropInfos is not modified.
func accumulableSegments(dropInfos []*model.DropInfo, timeRangesMap map[int]*model.TimeRange) map[int]map[int][][]*model.TimeRange {
	segmentsByStageAndItem := make(map[int]map[int][][]*model.TimeRange)
	for stageId, dropInfosForOneStage := range groupDropInfosWithItemByStageId(dropInfos) {
		dropInfosByItemId := make(map[int][]*model.DropInfo)
		for _, dropInfo := range dropInfosForOneStage {
			itemId := int(dropInfo.ItemID.Int64)
			dropInfosByItemId[itemId] = append(dropInfosByItemId[itemId], dropInfo)
		}
		segmentsByItem := make(map[int][][]*model.TimeRange, len(dropInfosByItemId))
		for itemId, dropInfosForOneItem := range dropInfosByItemId {
			sortedDropInfos := distinctDropInfosByRangeId(dropInfosForOneItem)
			sortDropInfos(sortedDropInfos, func(a, b *model.DropInfo) bool {
				startA, startB := timeRangesMap[a.RangeID].StartTime, timeRangesMap[b.RangeID].StartTime
				if !startA.Equal(*startB) {
					return startA.Before(*startB)
				}
				return a.RangeID < b.RangeID
			})

			segments := make([][]*model.TimeRange, 0, 1)
			for i, dropInfo := range sortedDropInfos {
				timeRange := timeRangesMap[dropInfo.RangeID]
				if i == 0 || !dropInfo.Accumulable || !sortedDropInfos[i-1].Accumulable {
					segments = append(segments, []*model.TimeRange{timeRange})
					continue
				}
				segments[len(segments)-1] = append(segments[len(segments)-1], timeRange)
			}
			segmentsByItem[itemId] = segments
		}
		segmentsByStageAndItem[stageId] = segmentsByItem
	}
	return segmentsByStageAndItem
}

// mergeSegment returns a new time range from the start of the first time range of segment to the latest end of its
// time ranges, without a range id.
func mergeSegment(segment []*model.TimeRange) *model.TimeRange {
	merged := &model.TimeRange{
		StartTime: segment[0].StartTime,
		EndTime:   segment[0].EndTime,
	}
	for _, timeRange := range segment[1:] {
		if timeRange.EndTime.After(*merged.EndTime) {
			merged.EndTime = timeRange.EndTime
		}
	}
	return merged
}

// Cache: latestTimeRanges#server:{server}, 5 min
func (s *TimeRange) GetLatestTimeRangesByServer(ctx context.Context, server string) (map[int]*model.TimeRange, error) {
	var results map[int]*model.TimeRange
	err := cache.LatestTimeRanges.Get(server, &results)
	if err == nil {
		return results, nil
	}

	dropInfos, err := s.getDropInfosByServer(ctx, server)
	if err != nil {
		return nil, err
	}
	timeRangesMap, err := s.GetTimeRangesMap(ctx, server)
	if err != nil {
		return nil, err
	}
	results = make(map[int]*model.TimeRange)
	for stageId, dropInfosForOneStage := range groupDropInfosWithItemByStageId(dropInfos) {
		sortDropInfos(dropInfosForOneStage, func(a, b *model.DropInfo) bool {
			return timeRangesMap[a.RangeID].StartTime.After(*timeRangesMap[b.RangeID].StartTime)
		})
		results[stageId] = timeRangesMap[dropInfosForOneStage[0].RangeID]
	}
	cache.LatestTimeRanges.Set(server, results, time.Minute*5)
	return results, nil
}

func (s *TimeRange) GetTimeRangeByServerAndName(ctx context.Context, server string, name string) (*model.TimeRange, error) {
	return s.TimeRangeRepo.GetTimeRangeByServerAndName(ctx, server, name)
}

// Cache: dropInfos#server:{server}, 1 min
// The time range calculations above all start from every drop info of a server; the stats worker runs them back
// to back for each server, so they share one load. Only StageID, ItemID, RangeID and Accumulable are filled.
// Callers must not modify the returned slice.
func (s *TimeRange) getDropInfosByServer(ctx context.Context, server string) ([]*model.DropInfo, error) {
	var dropInfos []*model.DropInfo
	if err := cache.DropInfosByServer.Get(server, &dropInfos); err == nil {
		return dropInfos, nil
	}
	dropInfos, err := s.DropInfoRepo.GetDropInfoRangesByServer(ctx, server)
	if err != nil {
		return nil, err
	}
	cache.DropInfosByServer.Set(server, dropInfos, time.Minute)
	return dropInfos, nil
}

// The helpers below keep the semantics of the go-linq pipelines they replaced: group members keep their input order,
// DistinctBy keeps the first occurrence, and sorting uses sort.Sort, so ties are resolved exactly as before.

// groupDropInfosWithItemByStageId groups the drop infos that have an item by stage id into newly allocated slices.
func groupDropInfosWithItemByStageId(dropInfos []*model.DropInfo) map[int][]*model.DropInfo {
	grouped := make(map[int][]*model.DropInfo)
	for _, dropInfo := range dropInfos {
		if !dropInfo.ItemID.Valid {
			continue
		}
		grouped[dropInfo.StageID] = append(grouped[dropInfo.StageID], dropInfo)
	}
	return grouped
}

func distinctDropInfosByRangeId(dropInfos []*model.DropInfo) []*model.DropInfo {
	seen := make(map[int]struct{}, len(dropInfos))
	results := make([]*model.DropInfo, 0, len(dropInfos))
	for _, dropInfo := range dropInfos {
		if _, ok := seen[dropInfo.RangeID]; ok {
			continue
		}
		seen[dropInfo.RangeID] = struct{}{}
		results = append(results, dropInfo)
	}
	return results
}

type dropInfoSorter struct {
	dropInfos []*model.DropInfo
	less      func(a, b *model.DropInfo) bool
}

func (s dropInfoSorter) Len() int { return len(s.dropInfos) }
func (s dropInfoSorter) Swap(i, j int) {
	s.dropInfos[i], s.dropInfos[j] = s.dropInfos[j], s.dropInfos[i]
}
func (s dropInfoSorter) Less(i, j int) bool { return s.less(s.dropInfos[i], s.dropInfos[j]) }

func sortDropInfos(dropInfos []*model.DropInfo, less func(a, b *model.DropInfo) bool) {
	sort.Sort(dropInfoSorter{dropInfos: dropInfos, less: less})
}

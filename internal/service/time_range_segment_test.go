package service

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/cache"
)

// accumulableCase describes the drop infos of one stage and item over time ranges given in days after
// segmentTestBase.
type accumulableCase struct {
	name string
	// [start day, end day] by range id
	ranges map[int][2]int64
	// (range id, accumulable) of the drop infos, in database order
	dropInfos []accumulableDropInfo
	// the segments by range id, in chronological order: the last one is the latest window
	segments [][]int
	// the merged segments, [start day, end day]
	merged [][2]int64
}

type accumulableDropInfo struct {
	rangeId     int
	accumulable bool
}

// accumulable or not, for short
const (
	accT = true
	accF = false
)

var segmentTestBase = time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)

func segmentTestTime(day int64) *time.Time {
	t := segmentTestBase.Add(time.Duration(day) * 24 * time.Hour)
	return &t
}

var accumulableCases = []accumulableCase{
	{
		name:      "all accumulable",
		ranges:    map[int][2]int64{1: {0, 10}, 2: {10, 20}, 3: {20, 30}},
		dropInfos: []accumulableDropInfo{{1, accT}, {2, accT}, {3, accT}},
		segments:  [][]int{{1, 2, 3}},
		merged:    [][2]int64{{0, 30}},
	},
	{
		name:      "leading not accumulable",
		ranges:    map[int][2]int64{1: {0, 10}, 2: {10, 20}, 3: {20, 30}},
		dropInfos: []accumulableDropInfo{{1, accF}, {2, accT}, {3, accT}},
		segments:  [][]int{{1}, {2, 3}},
		merged:    [][2]int64{{0, 10}, {10, 30}},
	},
	{
		name:      "several leading not accumulable",
		ranges:    map[int][2]int64{1: {0, 10}, 2: {10, 20}, 3: {20, 30}, 4: {30, 40}},
		dropInfos: []accumulableDropInfo{{1, accF}, {2, accF}, {3, accT}, {4, accT}},
		segments:  [][]int{{1}, {2}, {3, 4}},
		merged:    [][2]int64{{0, 10}, {10, 20}, {20, 40}},
	},
	{
		name:      "not accumulable in the middle",
		ranges:    map[int][2]int64{1: {0, 10}, 2: {10, 20}, 3: {20, 30}, 4: {30, 40}, 5: {40, 50}},
		dropInfos: []accumulableDropInfo{{1, accT}, {2, accT}, {3, accF}, {4, accT}, {5, accT}},
		segments:  [][]int{{1, 2}, {3}, {4, 5}},
		merged:    [][2]int64{{0, 20}, {20, 30}, {30, 50}},
	},
	{
		name:      "trailing not accumulable",
		ranges:    map[int][2]int64{1: {0, 10}, 2: {10, 20}, 3: {20, 30}},
		dropInfos: []accumulableDropInfo{{1, accT}, {2, accT}, {3, accF}},
		segments:  [][]int{{1, 2}, {3}},
		merged:    [][2]int64{{0, 20}, {20, 30}},
	},
	{
		name:      "two trailing not accumulable",
		ranges:    map[int][2]int64{1: {0, 10}, 2: {10, 20}, 3: {20, 30}, 4: {30, 40}},
		dropInfos: []accumulableDropInfo{{1, accT}, {2, accT}, {3, accF}, {4, accF}},
		segments:  [][]int{{1, 2}, {3}, {4}},
		merged:    [][2]int64{{0, 20}, {20, 30}, {30, 40}},
	},
	{
		name:      "single not accumulable",
		ranges:    map[int][2]int64{1: {0, 10}},
		dropInfos: []accumulableDropInfo{{1, accF}},
		segments:  [][]int{{1}},
		merged:    [][2]int64{{0, 10}},
	},
	{
		name:      "single accumulable",
		ranges:    map[int][2]int64{1: {0, 10}},
		dropInfos: []accumulableDropInfo{{1, accT}},
		segments:  [][]int{{1}},
		merged:    [][2]int64{{0, 10}},
	},
	{
		// a gap does not split a segment, as the one of US between ranges 22 and 46 on 2020-06-30
		name:      "gap between accumulable",
		ranges:    map[int][2]int64{1: {0, 10}, 2: {12, 20}, 3: {20, 30}},
		dropInfos: []accumulableDropInfo{{1, accT}, {2, accT}, {3, accT}},
		segments:  [][]int{{1, 2, 3}},
		merged:    [][2]int64{{0, 30}},
	},
	{
		name:      "duplicate drop infos of a range",
		ranges:    map[int][2]int64{1: {0, 10}, 2: {10, 20}, 3: {20, 30}},
		dropInfos: []accumulableDropInfo{{1, accF}, {2, accT}, {1, accF}, {3, accT}, {2, accT}, {3, accT}},
		segments:  [][]int{{1}, {2, 3}},
		merged:    [][2]int64{{0, 10}, {10, 30}},
	},
	{
		name:      "drop infos out of order",
		ranges:    map[int][2]int64{1: {0, 10}, 2: {10, 20}, 3: {20, 30}, 4: {30, 40}},
		dropInfos: []accumulableDropInfo{{4, accT}, {2, accF}, {3, accT}, {1, accT}},
		segments:  [][]int{{1}, {2}, {3, 4}},
		merged:    [][2]int64{{0, 10}, {10, 20}, {20, 40}},
	},
	{
		// a segment ends with the latest end of its time ranges
		name:      "overlapping time ranges",
		ranges:    map[int][2]int64{1: {0, 25}, 2: {10, 20}, 3: {30, 40}},
		dropInfos: []accumulableDropInfo{{2, accT}, {1, accT}, {3, accF}},
		segments:  [][]int{{1, 2}, {3}},
		merged:    [][2]int64{{0, 25}, {30, 40}},
	},
	{
		// equal start times are ordered by range id
		name:      "equal start times",
		ranges:    map[int][2]int64{1: {0, 10}, 2: {0, 20}, 3: {20, 30}},
		dropInfos: []accumulableDropInfo{{2, accT}, {1, accF}, {3, accT}},
		segments:  [][]int{{1}, {2, 3}},
		merged:    [][2]int64{{0, 10}, {0, 30}},
	},
}

func (c accumulableCase) build(stageId, itemId int) ([]*model.DropInfo, map[int]*model.TimeRange) {
	timeRangesMap := make(map[int]*model.TimeRange, len(c.ranges))
	for rangeId, days := range c.ranges {
		timeRangesMap[rangeId] = &model.TimeRange{RangeID: rangeId, StartTime: segmentTestTime(days[0]), EndTime: segmentTestTime(days[1])}
	}
	dropInfos := make([]*model.DropInfo, 0, len(c.dropInfos))
	for _, d := range c.dropInfos {
		dropInfos = append(dropInfos, &model.DropInfo{StageID: stageId, ItemID: null.IntFrom(int64(itemId)), RangeID: d.rangeId, Accumulable: d.accumulable})
	}
	return dropInfos, timeRangesMap
}

func segmentRangeIds(segments [][]*model.TimeRange) [][]int {
	ids := make([][]int, 0, len(segments))
	for _, segment := range segments {
		segmentIds := make([]int, 0, len(segment))
		for _, timeRange := range segment {
			segmentIds = append(segmentIds, timeRange.RangeID)
		}
		ids = append(ids, segmentIds)
	}
	return ids
}

func TestAccumulableSegments(t *testing.T) {
	for _, c := range accumulableCases {
		t.Run(c.name, func(t *testing.T) {
			dropInfos, timeRangesMap := c.build(1, 2)
			// a drop info of the stage itself, without an item, has no segments
			dropInfos = append(dropInfos, &model.DropInfo{StageID: 1, RangeID: dropInfos[0].RangeID})
			before := append([]*model.DropInfo(nil), dropInfos...)

			segments := accumulableSegments(dropInfos, timeRangesMap)
			if len(segments) != 1 || len(segments[1]) != 1 {
				t.Fatalf("segments %v, want those of stage 1 and item 2 only", segments)
			}
			if got := segmentRangeIds(segments[1][2]); !reflect.DeepEqual(got, c.segments) {
				t.Errorf("segments %v, want %v", got, c.segments)
			}
			for i := range before {
				if before[i] != dropInfos[i] {
					t.Fatalf("the (cached) drop infos were modified at %d", i)
				}
			}
		})
	}
}

// TestMaxAccumulableTimeRangesShareSegments checks that the time ranges of the global drop matrix (every segment,
// merged) and those of the personal one (the time ranges of the latest segment) come from the same segments.
func TestMaxAccumulableTimeRangesShareSegments(t *testing.T) {
	cache.Initialize()
	ctx := context.Background()
	s := &TimeRange{}
	for i, c := range accumulableCases {
		t.Run(c.name, func(t *testing.T) {
			server := fmt.Sprintf("SEGMENTS%d", i)
			invalidateTimeRangeCaches(server)
			t.Cleanup(func() { invalidateTimeRangeCaches(server) })
			dropInfos, timeRangesMap := c.build(1, 2)
			cache.DropInfosByServer.Set(server, dropInfos, time.Minute)
			cache.TimeRangesMap.Set(server, timeRangesMap, time.Minute)

			all, err := s.GetAllMaxAccumulableTimeRangesByServer(ctx, server)
			if err != nil {
				t.Fatal(err)
			}
			merged := make([][2]int64, 0)
			for _, timeRange := range all[1][2] {
				if timeRange.RangeID != 0 {
					t.Errorf("a merged segment has range id %d", timeRange.RangeID)
				}
				merged = append(merged, [2]int64{
					int64(timeRange.StartTime.Sub(segmentTestBase) / (24 * time.Hour)),
					int64(timeRange.EndTime.Sub(segmentTestBase) / (24 * time.Hour)),
				})
			}
			if !reflect.DeepEqual(merged, c.merged) {
				t.Errorf("all max accumulable time ranges %v, want %v", merged, c.merged)
			}

			latest, err := s.GetMaxAccumulableTimeRangesByServer(ctx, server)
			if err != nil {
				t.Fatal(err)
			}
			// the time ranges of the latest segment, newest first
			lastSegment := c.segments[len(c.segments)-1]
			want := make([]int, 0, len(lastSegment))
			for j := len(lastSegment) - 1; j >= 0; j-- {
				want = append(want, lastSegment[j])
			}
			if got := segmentRangeIds([][]*model.TimeRange{latest[1][2]})[0]; !reflect.DeepEqual(got, want) {
				t.Errorf("max accumulable time ranges %v, want %v", got, want)
			}
		})
	}
}

// TestLatestWindowOfMain15_04 is the case of #794: main_15-04 (CN) drops its items in range 602 main_15_event,
// which does not accumulate, then in range 609 after_main_15_event_to. The global drop matrix counted both since
// 2025-04-07; its latest window starts with range 609, on 2025-04-20 20:00 UTC.
func TestLatestWindowOfMain15_04(t *testing.T) {
	cache.Initialize()
	ctx := context.Background()
	server := "SEGMENTS_MAIN_15_04"
	invalidateTimeRangeCaches(server)
	t.Cleanup(func() { invalidateTimeRangeCaches(server) })
	millis := func(ms int64) *time.Time {
		t := time.UnixMilli(ms)
		return &t
	}
	cache.TimeRangesMap.Set(server, map[int]*model.TimeRange{
		602: {RangeID: 602, Name: null.StringFrom("main_15_event"), StartTime: millis(1744012800000), EndTime: millis(1745179200000)},
		609: {RangeID: 609, Name: null.StringFrom("after_main_15_event_to"), StartTime: millis(1745179200000), EndTime: millis(62141368179000)},
	}, time.Minute)
	cache.DropInfosByServer.Set(server, []*model.DropInfo{
		{StageID: 1450, ItemID: null.IntFrom(91), RangeID: 602, Accumulable: false},
		{StageID: 1450, ItemID: null.IntFrom(91), RangeID: 609, Accumulable: true},
	}, time.Minute)

	s := &TimeRange{}
	all, err := s.GetAllMaxAccumulableTimeRangesByServer(ctx, server)
	if err != nil {
		t.Fatal(err)
	}
	timeRanges := all[1450][91]
	if latest := timeRanges[len(timeRanges)-1]; latest.StartTime.UnixMilli() != 1745179200000 || latest.EndTime.UnixMilli() != 62141368179000 {
		t.Errorf("latest window %d~%d, want 1745179200000~62141368179000", latest.StartTime.UnixMilli(), latest.EndTime.UnixMilli())
	}
	personal, err := s.GetMaxAccumulableTimeRangesByServer(ctx, server)
	if err != nil {
		t.Fatal(err)
	}
	if got := personal[1450][91]; len(got) != 1 || got[0].RangeID != 609 {
		t.Errorf("personal latest window %v, want range 609", segmentRangeIds([][]*model.TimeRange{got}))
	}
}

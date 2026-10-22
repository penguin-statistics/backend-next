package service

import (
	"math/rand"
	"sort"
	"testing"
	"time"

	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/cache"
)

// randomDropInfos generates drop infos with many duplicate ranges and equal start times, so that sorting and
// de-duplication have plenty of ties to resolve.
func randomDropInfos(r *rand.Rand) ([]*model.DropInfo, map[int]*model.TimeRange) {
	timeRangesMap := make(map[int]*model.TimeRange)
	base := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	for rangeId := 1; rangeId <= 12; rangeId++ {
		start := base.Add(time.Duration(r.Intn(4)) * 24 * time.Hour)
		end := start.Add(time.Duration(1+r.Intn(3)) * 24 * time.Hour)
		timeRangesMap[rangeId] = &model.TimeRange{RangeID: rangeId, StartTime: &start, EndTime: &end}
	}
	dropInfos := make([]*model.DropInfo, 0)
	for i := 0; i < 300; i++ {
		dropInfo := &model.DropInfo{
			DropID:      i,
			StageID:     1 + r.Intn(6),
			RangeID:     1 + r.Intn(12),
			Accumulable: r.Intn(4) != 0,
		}
		if r.Intn(5) != 0 {
			dropInfo.ItemID = null.IntFrom(int64(1 + r.Intn(5)))
		}
		dropInfos = append(dropInfos, dropInfo)
	}
	return dropInfos, timeRangesMap
}

// anySorter is the sorter go-linq's Sort used, on a copy of the items as []any.
type anySorter struct {
	items []any
	less  func(a, b any) bool
}

func (s anySorter) Len() int           { return len(s.items) }
func (s anySorter) Swap(i, j int)      { s.items[i], s.items[j] = s.items[j], s.items[i] }
func (s anySorter) Less(i, j int) bool { return s.less(s.items[i], s.items[j]) }

// TestDropInfoHelpersMatchGoLinq checks the helpers against what the go-linq pipelines they replaced did: a group has
// the drop infos with an item of its stage in their input order, DistinctBy keeps the first drop info of a range,
// and Sort ran sort.Sort on a copy of them, which resolves ties in the same way only for the same input order.
func TestDropInfoHelpersMatchGoLinq(t *testing.T) {
	for seed := int64(0); seed < 200; seed++ {
		r := rand.New(rand.NewSource(seed))
		dropInfos, timeRangesMap := randomDropInfos(r)
		lessByStart := func(a, b *model.DropInfo) bool {
			return timeRangesMap[a.RangeID].StartTime.Before(*timeRangesMap[b.RangeID].StartTime)
		}

		// grouping by stage, then distinct by range and sorting, as GetAllMaxAccumulableTimeRangesByServer does
		grouped := groupDropInfosWithItemByStageId(dropInfos)
		stages := make(map[int]bool)
		for _, dropInfo := range dropInfos {
			if dropInfo.ItemID.Valid {
				stages[dropInfo.StageID] = true
			}
		}
		if len(grouped) != len(stages) {
			t.Fatalf("seed %d: %d stage groups, want %d", seed, len(grouped), len(stages))
		}
		for stageId := range stages {
			var group []*model.DropInfo
			for _, dropInfo := range dropInfos {
				if dropInfo.ItemID.Valid && dropInfo.StageID == stageId {
					group = append(group, dropInfo)
				}
			}
			if len(grouped[stageId]) != len(group) {
				t.Fatalf("seed %d stage %d: %d drop infos, want %d", seed, stageId, len(grouped[stageId]), len(group))
			}
			for i := range group {
				if grouped[stageId][i] != group[i] {
					t.Fatalf("seed %d stage %d: position %d is drop %d, want drop %d", seed, stageId, i, grouped[stageId][i].DropID, group[i].DropID)
				}
			}

			var distinct []any
			seen := make(map[int]bool)
			for _, dropInfo := range group {
				if !seen[dropInfo.RangeID] {
					seen[dropInfo.RangeID] = true
					distinct = append(distinct, dropInfo)
				}
			}
			sort.Sort(anySorter{items: distinct, less: func(a, b any) bool { return lessByStart(a.(*model.DropInfo), b.(*model.DropInfo)) }})

			actual := distinctDropInfosByRangeId(grouped[stageId])
			sortDropInfos(actual, lessByStart)
			if len(actual) != len(distinct) {
				t.Fatalf("seed %d stage %d: %d drop infos, want %d", seed, stageId, len(actual), len(distinct))
			}
			for i := range distinct {
				if actual[i] != distinct[i] {
					t.Fatalf("seed %d stage %d: position %d is drop %d, want drop %d",
						seed, stageId, i, actual[i].DropID, distinct[i].(*model.DropInfo).DropID)
				}
			}
		}
	}
}

func TestSortDropInfosDoesNotTouchInput(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	dropInfos, timeRangesMap := randomDropInfos(r)
	before := append([]*model.DropInfo(nil), dropInfos...)
	for _, group := range groupDropInfosWithItemByStageId(dropInfos) {
		sortDropInfos(group, func(a, b *model.DropInfo) bool {
			return timeRangesMap[a.RangeID].StartTime.After(*timeRangesMap[b.RangeID].StartTime)
		})
	}
	for i := range before {
		if before[i] != dropInfos[i] {
			t.Fatalf("the (cached) input slice was modified at %d", i)
		}
	}
}

func TestInvalidateTimeRangeCachesDropsTheSharedDropInfos(t *testing.T) {
	cache.Initialize()
	// warm every cache a time range calculation reads, as a calculation within the last minute would have
	cache.DropInfosByServer.Set("CN", []*model.DropInfo{{StageID: 1}}, time.Minute)
	cache.DropInfosByServer.Set("US", []*model.DropInfo{{StageID: 2}}, time.Minute)
	cache.LatestTimeRanges.Set("CN", map[int]*model.TimeRange{}, time.Minute)
	cache.AllMaxAccumulableTimeRanges.Set("CN", map[int]map[int][]*model.TimeRange{}, time.Minute)

	// metadata of CN saved
	invalidateTimeRangeCaches("CN")

	var dropInfos []*model.DropInfo
	if err := cache.DropInfosByServer.Get("CN", &dropInfos); err == nil {
		t.Error("the drop infos of CN are still cached: the next calculation would rebuild time ranges from them")
	}
	var latest map[int]*model.TimeRange
	if err := cache.LatestTimeRanges.Get("CN", &latest); err == nil {
		t.Error("the latest time ranges of CN are still cached")
	}
	var allMax map[int]map[int][]*model.TimeRange
	if err := cache.AllMaxAccumulableTimeRanges.Get("CN", &allMax); err == nil {
		t.Error("the max accumulable time ranges of CN are still cached")
	}
	if err := cache.DropInfosByServer.Get("US", &dropInfos); err != nil {
		t.Error("the drop infos of another server were dropped")
	}
}

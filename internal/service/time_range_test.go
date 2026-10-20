package service

import (
	"math/rand"
	"testing"
	"time"

	"github.com/ahmetb/go-linq/v3"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model"
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

func TestDropInfoHelpersMatchGoLinq(t *testing.T) {
	for seed := int64(0); seed < 200; seed++ {
		r := rand.New(rand.NewSource(seed))
		dropInfos, timeRangesMap := randomDropInfos(r)
		lessByStart := func(a, b *model.DropInfo) bool {
			return timeRangesMap[a.RangeID].StartTime.Before(*timeRangesMap[b.RangeID].StartTime)
		}

		// grouping by stage, then item, then distinct by range and sorting, as GetAllMaxAccumulableTimeRangesByServer does
		var linqGroups []linq.Group
		linq.From(dropInfos).
			WhereT(func(dropInfo *model.DropInfo) bool { return dropInfo.ItemID.Valid }).
			GroupByT(
				func(dropInfo *model.DropInfo) int { return dropInfo.StageID },
				func(dropInfo *model.DropInfo) *model.DropInfo { return dropInfo },
			).
			ToSlice(&linqGroups)
		grouped := groupDropInfosWithItemByStageId(dropInfos)
		if len(grouped) != len(linqGroups) {
			t.Fatalf("seed %d: %d stage groups, go-linq has %d", seed, len(grouped), len(linqGroups))
		}
		for _, group := range linqGroups {
			stageId := group.Key.(int)
			var expected []*model.DropInfo
			linq.From(group.Group).
				DistinctByT(func(dropInfo *model.DropInfo) int { return dropInfo.RangeID }).
				SortT(lessByStart).
				ToSlice(&expected)

			actual := distinctDropInfosByRangeId(grouped[stageId])
			sortDropInfos(actual, lessByStart)
			if len(actual) != len(expected) {
				t.Fatalf("seed %d stage %d: %d drop infos, go-linq has %d", seed, stageId, len(actual), len(expected))
			}
			for i := range expected {
				if actual[i] != expected[i] {
					t.Fatalf("seed %d stage %d: position %d is drop %d, go-linq has drop %d",
						seed, stageId, i, actual[i].DropID, expected[i].DropID)
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

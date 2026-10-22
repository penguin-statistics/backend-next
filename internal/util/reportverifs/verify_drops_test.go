package reportverifs

import (
	"reflect"
	"testing"
	"time"

	"github.com/pkg/errors"
	"github.com/samber/lo"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/model/types"
)

// main0107ExtraItems are the EXTRA items of main_01-07 on CN, item 1 first: it drops every time.
var main0107ExtraItems = []int{1, 5, 6, 10, 14, 18, 22, 26}

// main0107DropInfos returns the drop infos of main_01-07 on CN (as in testdata/golden_reports.json), split as
// repo.DropInfo.GetForCurrentTimeRangeWithDropTypes does: each time gives 1 REGULAR item (item 7) and 1 or 2 EXTRA
// items, item 1 and maybe one of the 7 others.
func main0107DropInfos() (itemDropInfos, typeDropInfos []*model.DropInfo) {
	item := func(dropType string, itemID, lower, upper int) *model.DropInfo {
		return &model.DropInfo{DropType: dropType, ItemID: null.IntFrom(int64(itemID)), Bounds: &model.Bounds{Lower: lower, Upper: upper}}
	}
	itemDropInfos = append(itemDropInfos, item("REGULAR", 7, 1, 2), item("EXTRA", 1, 1, 2))
	for _, itemID := range main0107ExtraItems[1:] {
		itemDropInfos = append(itemDropInfos, item("EXTRA", itemID, 0, 1))
	}
	itemDropInfos = append(itemDropInfos, item("FURNITURE", 38, 0, 1))
	for _, d := range []struct {
		dropType     string
		lower, upper int
	}{{"REGULAR", 1, 1}, {"SPECIAL", 0, 0}, {"EXTRA", 1, 2}} {
		typeDropInfos = append(typeDropInfos, &model.DropInfo{DropType: d.dropType, Bounds: &model.Bounds{Lower: d.lower, Upper: d.upper}})
	}
	return itemDropInfos, typeDropInfos
}

// main0107Report returns a report of main_01-07 for times, with the REGULAR item and kinds kinds of EXTRA items: item 1
// once per time, and each other one once.
func main0107Report(times, kinds int) *types.ReportTaskSingleReport {
	report := &types.ReportTaskSingleReport{FragmentStageID: types.FragmentStageID{StageID: "main_01-07"}, Times: times}
	report.Drops = append(report.Drops, &types.Drop{DropType: "REGULAR", ItemID: 7, Quantity: times})
	for i, itemID := range main0107ExtraItems[:kinds] {
		quantity := 1
		if i == 0 {
			quantity = times
		}
		report.Drops = append(report.Drops, &types.Drop{DropType: "EXTRA", ItemID: itemID, Quantity: quantity})
	}
	return report
}

func TestAdjustDropInfosByTimesTypeBounds(t *testing.T) {
	for _, c := range []struct {
		times      int
		extraUpper int
	}{
		// each time gives item 1 and at most 1 other kind of EXTRA items, and the stage has 8
		{0, 2}, {1, 2}, {2, 3}, {3, 4}, {4, 5}, {5, 6}, {6, 7}, {7, 8}, {10, 8},
	} {
		itemDropInfos, typeDropInfos := main0107DropInfos()
		adjustDropInfosByTimes(itemDropInfos, typeDropInfos, c.times)
		want := map[string]model.Bounds{
			"REGULAR": {Lower: 1, Upper: 1},
			"SPECIAL": {Lower: 0, Upper: 0},
			"EXTRA":   {Lower: 1, Upper: c.extraUpper},
		}
		for _, dropInfo := range typeDropInfos {
			if got := *dropInfo.Bounds; got.Lower != want[dropInfo.DropType].Lower || got.Upper != want[dropInfo.DropType].Upper {
				t.Errorf("times %d, drop type %s: bounds %+v, want %+v", c.times, dropInfo.DropType, got, want[dropInfo.DropType])
			}
		}
	}
}

// TestAdjustDropInfosByTimesNeverLower checks, over a grid of upper bounds, item counts, guaranteed items (lower bound
// above 0) and times, that the upper bound of a drop type is never lower than max(oldUpper, min(times, itemTypeCount)),
// which the verification had before: no report it accepted is rejected. It is the same for times 0 and 1, and above it
// for times above 1 only by the kinds that times times can give: guaranteed + times * (oldUpper - guaranteed), at most
// itemTypeCount.
func TestAdjustDropInfosByTimesNeverLower(t *testing.T) {
	for oldUpper := 0; oldUpper <= 4; oldUpper++ {
		for itemTypeCount := 0; itemTypeCount <= 8; itemTypeCount++ {
			for guaranteed := 0; guaranteed <= itemTypeCount; guaranteed++ {
				for times := 0; times <= 10; times++ {
					var itemDropInfos []*model.DropInfo
					for i := 0; i < itemTypeCount; i++ {
						lower := 0
						if i < guaranteed {
							lower = 1
						}
						itemDropInfos = append(itemDropInfos, &model.DropInfo{DropType: "EXTRA", ItemID: null.IntFrom(int64(i + 1)), Bounds: &model.Bounds{Lower: lower, Upper: 1}})
					}
					typeDropInfo := &model.DropInfo{DropType: "EXTRA", Bounds: &model.Bounds{Lower: 0, Upper: oldUpper}}
					adjustDropInfosByTimes(itemDropInfos, []*model.DropInfo{typeDropInfo}, times)
					got := typeDropInfo.Bounds.Upper

					before := times
					if itemTypeCount < before {
						before = itemTypeCount
					}
					if oldUpper > before {
						before = oldUpper
					}
					want := before
					if times > 1 {
						possible := guaranteed + times*(oldUpper-guaranteed)
						if itemTypeCount < possible {
							possible = itemTypeCount
						}
						if possible > want {
							want = possible
						}
					}

					if got < before {
						t.Errorf("upper %d, %d items, %d guaranteed, times %d: upper bound %d, below %d", oldUpper, itemTypeCount, guaranteed, times, got, before)
					}
					if times <= 1 && got != before {
						t.Errorf("upper %d, %d items, %d guaranteed, times %d: upper bound %d, want %d as before", oldUpper, itemTypeCount, guaranteed, times, got, before)
					}
					if got != want {
						t.Errorf("upper %d, %d items, %d guaranteed, times %d: upper bound %d, want %d", oldUpper, itemTypeCount, guaranteed, times, got, want)
					}
				}
			}
		}
	}
}

// TestVerifyDropsMultipleTimes checks main_01-07 for times 1 to 6: every count of EXTRA kinds a run can give, 1 to
// times + 1 (item 1 and one other per time), passes; times 1 still rejects the counts it cannot give, and times 2 to 6
// reject times + 2 kinds, one above what they can give (#580: 4 kinds at times 2).
func TestVerifyDropsMultipleTimes(t *testing.T) {
	verify := func(report *types.ReportTaskSingleReport) []error {
		itemDropInfos, typeDropInfos := main0107DropInfos()
		adjustDropInfosByTimes(itemDropInfos, typeDropInfos, report.Times)
		return append(verifyDropType(report, typeDropInfos), verifyDropItem(report, itemDropInfos)...)
	}

	for times := 1; times <= 6; times++ {
		for kinds := 1; kinds <= times+1; kinds++ {
			if errs := verify(main0107Report(times, kinds)); len(errs) > 0 {
				t.Errorf("times %d, %d kinds of EXTRA items: rejected: %v", times, kinds, errs)
			}
		}
	}

	for _, c := range []struct{ times, kinds int }{
		{1, 0}, {1, 3}, {1, 4}, {1, 8},
		// the stage has 8 EXTRA items, so from times 7 on no count is above the upper bound
		{2, 4}, {3, 5}, {4, 6}, {5, 7}, {6, 8},
	} {
		errs := verify(main0107Report(c.times, c.kinds))
		if !lo.SomeBy(errs, func(err error) bool { return errors.Is(err, ErrInvalidDropType) }) {
			t.Errorf("times %d, %d kinds of EXTRA items: not rejected by the drop type bounds: %v", c.times, c.kinds, errs)
		}
	}
}

// TestCopyDropInfos checks that adjusting the copies of drop infos for times leaves the drop infos as they were.
func TestCopyDropInfos(t *testing.T) {
	items, types := main0107DropInfos()
	items[1].Bounds.Exceptions = []int{1}
	types[2].Bounds.Exceptions = []int{1}
	wantItems, wantTypes := main0107DropInfos()
	wantItems[1].Bounds.Exceptions = []int{1}
	wantTypes[2].Bounds.Exceptions = []int{1}

	for times := 1; times <= 3; times++ {
		itemCopies, typeCopies := copyDropInfos(items, types)
		if !reflect.DeepEqual(itemCopies, items) || !reflect.DeepEqual(typeCopies, types) {
			t.Fatalf("times %d: the copies differ from the drop infos", times)
		}
		adjustDropInfosByTimes(itemCopies, typeCopies, times)
		// the item copies have no room to append to: it would overwrite the type copies
		_ = append(itemCopies, &model.DropInfo{})
		if !reflect.DeepEqual(items, wantItems) || !reflect.DeepEqual(types, wantTypes) {
			t.Fatalf("times %d: adjusting the copies changed the drop infos", times)
		}
		if typeCopies[0].DropType != "REGULAR" {
			t.Fatalf("times %d: appending to the item copies changed the type copies", times)
		}
	}

	if itemCopies, typeCopies := copyDropInfos(nil, types); itemCopies != nil || len(typeCopies) != len(types) {
		t.Errorf("copies of no item drop infos: %v, %d type drop infos", itemCopies, len(typeCopies))
	}
	if itemCopies, typeCopies := copyDropInfos(items, nil); len(itemCopies) != len(items) || typeCopies != nil {
		t.Errorf("copies of no type drop infos: %d item drop infos, %v", len(itemCopies), typeCopies)
	}
}

func TestDropInfosExpiry(t *testing.T) {
	now := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name  string
		until time.Time
		want  time.Time
	}{
		{"no time range ends", time.Time{}, now.Add(cacheTTL)},
		{"a time range ends after the TTL", now.Add(cacheTTL + time.Second), now.Add(cacheTTL)},
		{"a time range ends with the TTL", now.Add(cacheTTL), now.Add(cacheTTL)},
		{"a time range ends within the TTL", now.Add(time.Second), now.Add(time.Second)},
		{"a time range has ended", now.Add(-time.Second), now.Add(-time.Second)},
	} {
		if got := dropInfosExpiry(now, c.until); !got.Equal(c.want) {
			t.Errorf("%s: expiry %v, want %v", c.name, got, c.want)
		}
	}
}

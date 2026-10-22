package reportverifs

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"exusiai.dev/gommon/constant"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/repo"
)

// TestDropInfosForVerificationGolden checks, for the stage and server of every report of the fixture, that the drop
// infos the verification reads have the item, drop type and bounds of the whole drop infos of the stage.
func TestDropInfosForVerificationGolden(t *testing.T) {
	db, f := loadGolden(t)
	r := repo.NewDropInfo(db)
	ctx := context.Background()

	keys := func(dropInfos []*model.DropInfo) []string {
		s := make([]string, len(dropInfos))
		for i, d := range dropInfos {
			s[i] = fmt.Sprintf("%v %s %+v", d.ItemID, d.DropType, d.Bounds)
		}
		sort.Strings(s)
		return s
	}

	queried := map[repo.DropInfoQuery]bool{}
	for i := range f.Tasks {
		task := f.task(i)
		for _, report := range task.Reports {
			query := repo.DropInfoQuery{Server: task.Server, ArkStageId: report.StageID}
			if queried[query] {
				continue
			}
			queried[query] = true

			all, err := r.GetForCurrentTimeRange(ctx, &query)
			if err != nil {
				t.Fatal(err)
			}
			var wantItems, wantTypes []*model.DropInfo
			for _, d := range all {
				if d.DropType == constant.DropTypeRecognitionOnly {
					continue
				}
				if d.ItemID.Valid {
					wantItems = append(wantItems, d)
				} else {
					wantTypes = append(wantTypes, d)
				}
			}
			gotItems, gotTypes, err := r.GetForCurrentTimeRangeWithDropTypes(ctx, &query)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(keys(gotItems), keys(wantItems)) || !reflect.DeepEqual(keys(gotTypes), keys(wantTypes)) {
				t.Errorf("%+v: got %v and %v, want %v and %v", query, keys(gotItems), keys(gotTypes), keys(wantItems), keys(wantTypes))
			}
		}
	}
	t.Logf("drop infos of %d stages and servers compared", len(queried))
}

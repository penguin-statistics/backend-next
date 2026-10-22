package util

import (
	"reflect"
	"testing"

	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model"
)

func TestDropInfoMaps(t *testing.T) {
	item := func(stageId, itemId int) *model.DropInfo {
		return &model.DropInfo{StageID: stageId, ItemID: null.IntFrom(int64(itemId))}
	}
	noItem := func(stageId int) *model.DropInfo { return &model.DropInfo{StageID: stageId} }
	dropInfos := []*model.DropInfo{noItem(5), item(2, 30), item(1, 7), item(2, 4), noItem(2), item(2, 30), item(1, 7), item(1, 3)}

	// the distinct items of each stage with an item, in the order they first appear
	if got, want := GetStageIdItemIdMapFromDropInfos(dropInfos), map[int][]int{2: {30, 4}, 1: {7, 3}}; !reflect.DeepEqual(got, want) {
		t.Errorf("stage id item id map %v, want %v", got, want)
	}
	if got := GetStageIdItemIdMapFromDropInfos(nil); got == nil || len(got) != 0 {
		t.Errorf("stage id item id map of nil %v, want empty", got)
	}

	// the distinct stages, with or without an item, in the order they first appear; empty, not nil, for none
	if got, want := GetStageIdsFromDropInfos(dropInfos), []int{5, 2, 1}; !reflect.DeepEqual(got, want) {
		t.Errorf("stage ids %v, want %v", got, want)
	}
	if got := GetStageIdsFromDropInfos(nil); got == nil || len(got) != 0 {
		t.Errorf("stage ids of nil %v, want empty", got)
	}
}

package util

import (
	"exusiai.dev/backend-next/internal/model"
)

// GetStageIdItemIdMapFromDropInfos maps the id of each stage with an item drop info to the distinct ids of its items,
// in the order they first appear in dropInfos.
func GetStageIdItemIdMapFromDropInfos(dropInfos []*model.DropInfo) map[int][]int {
	stageIdItemIdMap := make(map[int][]int)
	seen := make(map[[2]int]struct{})
	for _, dropInfo := range dropInfos {
		if !dropInfo.ItemID.Valid {
			continue
		}
		itemId := int(dropInfo.ItemID.Int64)
		key := [2]int{dropInfo.StageID, itemId}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		stageIdItemIdMap[dropInfo.StageID] = append(stageIdItemIdMap[dropInfo.StageID], itemId)
	}
	return stageIdItemIdMap
}

// GetStageIdsFromDropInfos returns the distinct stage ids of dropInfos in the order they first appear, empty (not nil)
// for none.
func GetStageIdsFromDropInfos(dropInfos []*model.DropInfo) []int {
	stageIds := make([]int, 0)
	seen := make(map[int]struct{})
	for _, dropInfo := range dropInfos {
		if _, ok := seen[dropInfo.StageID]; ok {
			continue
		}
		seen[dropInfo.StageID] = struct{}{}
		stageIds = append(stageIds, dropInfo.StageID)
	}
	return stageIds
}

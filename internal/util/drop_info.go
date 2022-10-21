package util

import (
	"github.com/ahmetb/go-linq/v3"

	"exusiai.dev/backend-next/internal/model"
)

func GetStageIdItemIdMapFromDropInfos(dropInfos []*model.DropInfo) map[int][]int {
	stageIdItemIdMap := make(map[int][]int)
	var groupedResults []linq.Group
	linq.From(dropInfos).
		WhereT(func(dropInfo *model.DropInfo) bool { return dropInfo.ItemID.Valid }).
		GroupByT(
			func(dropInfo *model.DropInfo) int { return dropInfo.StageID },
			func(dropInfo *model.DropInfo) int { return int(dropInfo.ItemID.Int64) },
		).ToSlice(&groupedResults)
	for _, groupedResult := range groupedResults {
		stageId := groupedResult.Key.(int)
		itemIds := make([]int, 0)
		linq.From(groupedResult.Group).Distinct().ToSlice(&itemIds)
		if len(itemIds) > 0 {
			stageIdItemIdMap[stageId] = itemIds
		}
	}
	return stageIdItemIdMap
}

func GetStageIdsFromDropInfos(dropInfos []*model.DropInfo) []int {
	stageIds := make([]int, 0)
	linq.From(dropInfos).SelectT(func(dropInfo *model.DropInfo) int { return dropInfo.StageID }).Distinct().ToSlice(&stageIds)
	return stageIds
}

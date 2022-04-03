package util

import (
	"github.com/ahmetb/go-linq/v3"

	"github.com/penguin-statistics/backend-next/internal/models"
)

func GetStageIdItemIdMapFromDropInfos(dropInfos []*models.DropInfo) map[int][]int {
	stageIdItemIdMap := make(map[int][]int)
	var groupedResults []linq.Group
	linq.From(dropInfos).
		WhereT(func(dropInfo *models.DropInfo) bool { return dropInfo.ItemID.Valid }).
		GroupByT(
			func(dropInfo *models.DropInfo) int { return dropInfo.StageID },
			func(dropInfo *models.DropInfo) int { return int(dropInfo.ItemID.Int64) },
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

func GetStageIdsFromDropInfos(dropInfos []*models.DropInfo) []int {
	stageIds := make([]int, 0)
	linq.From(dropInfos).SelectT(func(dropInfo *models.DropInfo) int { return dropInfo.StageID }).Distinct().ToSlice(&stageIds)
	return stageIds
}

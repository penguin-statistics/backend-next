package util

import (
	"github.com/ahmetb/go-linq/v3"
	"github.com/rs/zerolog/log"

	"exusiai.dev/backend-next/internal/model"
)

func CombineQuantityAndTimesResults(
	quantityResults []*model.TotalQuantityResultForDropMatrix, timesResults []*model.TotalTimesResult,
	quantityUniqCountResults []*model.QuantityUniqCountResultForDropMatrix, timeRange *model.TimeRange,
) []*model.CombinedResultForDropMatrix {
	combinedResults := make([]*model.CombinedResultForDropMatrix, 0)

	var firstGroupResults []linq.Group
	linq.From(quantityResults).
		GroupByT(
			func(result *model.TotalQuantityResultForDropMatrix) int { return result.StageID },
			func(result *model.TotalQuantityResultForDropMatrix) *model.TotalQuantityResultForDropMatrix {
				return result
			}).
		ToSlice(&firstGroupResults)
	quantityResultsMap := make(map[int]map[int]int)
	for _, firstGroupElements := range firstGroupResults {
		stageId := firstGroupElements.Key.(int)
		resultsMap := make(map[int]int)
		linq.From(firstGroupElements.Group).
			ToMapByT(&resultsMap,
				func(el any) int { return el.(*model.TotalQuantityResultForDropMatrix).ItemID },
				func(el any) int { return el.(*model.TotalQuantityResultForDropMatrix).TotalQuantity })
		quantityResultsMap[stageId] = resultsMap
	}

	var quantityUniqCountGroupResultsByStageID []linq.Group
	linq.From(quantityUniqCountResults).
		GroupByT(func(result *model.QuantityUniqCountResultForDropMatrix) int { return result.StageID },
			func(result *model.QuantityUniqCountResultForDropMatrix) *model.QuantityUniqCountResultForDropMatrix {
				return result
			}).
		ToSlice(&quantityUniqCountGroupResultsByStageID)
	quantityUniqCountResultsMap := make(map[int]map[int]map[int]int)
	for _, quantityUniqCountGroupElements := range quantityUniqCountGroupResultsByStageID {
		stageId := quantityUniqCountGroupElements.Key.(int)
		var quantityUniqCountGroupResultsByItemID []linq.Group
		linq.From(quantityUniqCountGroupElements.Group).
			GroupByT(func(result *model.QuantityUniqCountResultForDropMatrix) int { return result.ItemID },
				func(result *model.QuantityUniqCountResultForDropMatrix) *model.QuantityUniqCountResultForDropMatrix {
					return result
				}).ToSlice(&quantityUniqCountGroupResultsByItemID)
		oneItemResultsMap := make(map[int]map[int]int)
		for _, quantityUniqCountGroupElements2 := range quantityUniqCountGroupResultsByItemID {
			itemId := quantityUniqCountGroupElements2.Key.(int)
			subMap := make(map[int]int)
			linq.From(quantityUniqCountGroupElements2.Group).
				ToMapByT(&subMap,
					func(el any) int { return el.(*model.QuantityUniqCountResultForDropMatrix).Quantity },
					func(el any) int { return el.(*model.QuantityUniqCountResultForDropMatrix).Count })
			oneItemResultsMap[itemId] = subMap
		}
		quantityUniqCountResultsMap[stageId] = oneItemResultsMap
	}

	var secondGroupResults []linq.Group
	linq.From(timesResults).
		GroupByT(
			func(result *model.TotalTimesResult) int { return result.StageID },
			func(result *model.TotalTimesResult) *model.TotalTimesResult { return result }).
		ToSlice(&secondGroupResults)
	for _, secondGroupResults := range secondGroupResults {
		stageId := secondGroupResults.Key.(int)
		quantityResultsMapForOneStage := quantityResultsMap[stageId]
		quantityUniqCountResultsMapForOneStage := quantityUniqCountResultsMap[stageId]
		for _, el := range secondGroupResults.Group {
			times := el.(*model.TotalTimesResult).TotalTimes
			for itemId, quantity := range quantityResultsMapForOneStage {
				quantityBuckets := quantityUniqCountResultsMapForOneStage[itemId]
				if !validateQuantityBucketsAndTimes(quantityBuckets, times) {
					log.Warn().Msgf("quantity buckets and times are not matched for stage %d, item %d, please check drop pattern", stageId, itemId)
				}
				combinedResultForDropMatrix := &model.CombinedResultForDropMatrix{
					StageID:         stageId,
					ItemID:          itemId,
					Quantity:        quantity,
					QuantityBuckets: quantityBuckets,
					Times:           times,
				}
				if timeRange != nil {
					combinedResultForDropMatrix.TimeRange = timeRange
				}
				combinedResults = append(combinedResults, combinedResultForDropMatrix)
			}
		}
	}
	return combinedResults
}

func validateQuantityBucketsAndTimes(quantityBuckets map[int]int, times int) bool {
	sum := 0
	for _, quantity := range quantityBuckets {
		sum += quantity
	}
	return sum <= times
}

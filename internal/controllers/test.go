package controllers

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ahmetb/go-linq/v3"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
	"github.com/uptrace/bun"
	"go.uber.org/fx"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/repos"
	"github.com/penguin-statistics/backend-next/internal/server"
)

type TestController struct {
	fx.In

	DB                    *bun.DB
	DropMatrixElementRepo *repos.DropMatrixElementRepo
	DropInfoRepo          *repos.DropInfoRepo
}

func RegisterTestController(v3 *server.V3, c TestController) {
	v3.Get("/test/:server", c.Test)
}

func (c *TestController) Test(ctx *fiber.Ctx) error {
	queryServer := ctx.Params("server")

	c.RefreshGlobalDropMatrix(ctx, queryServer)

	return ctx.SendStatus(fiber.StatusNoContent)
}

func (c *TestController) RefreshGlobalDropMatrix(ctx *fiber.Ctx, server string) error {
	toSave := []models.DropMatrixElement{}
	ch := make(chan []models.DropMatrixElement, 15)
	var wg sync.WaitGroup

	go func() {
		for {
			m := <-ch
			toSave = append(toSave, m...)
			wg.Done()
		}
	}()

	usedTimeMap := sync.Map{}

	limiter := make(chan struct{}, 7)
	wg.Add(220)
	// iterate over all time ranges
	for i := 1; i <= 220; i++ {
		limiter <- struct{}{}
		go func(i int) {
			fmt.Println("<   :", i)
			startTime := time.Now()

			rangeIds := []int{i}
			var results []map[string]int
			// get drop matrix calc results
			if err := c.calcDropMatrixForTimeRanges(ctx, server, rangeIds, nil, nil, &results); err != nil {
				return
			}

			stageTimesMap := map[int]int{} // save stage times for later use

			// grouping results by stage id
			var groupedResults []linq.Group
			linq.From(results).
				GroupByT(
					func(el map[string]int) int { return el["stageId"] },
					func(el map[string]int) map[string]int { return el }).ToSlice(&groupedResults)

			currentBatch := []models.DropMatrixElement{}
			for _, el := range groupedResults {
				stageId := el.Key.(int)

				// get all item ids which are dropped in this stage and in this time range
				dropItemIds, _ := c.DropInfoRepo.GetItemDropSetByStageIdAndRangeId(ctx.Context(), server, stageId, i)
				// if err != nil {
				// 	return err
				// }
				// use a fake hashset to save item ids
				dropSet := make(map[int]struct{}, len(dropItemIds))
				for _, itemId := range dropItemIds {
					dropSet[itemId] = struct{}{}
				}

				for _, el2 := range el.Group {
					itemId := el2.(map[string]int)["itemId"]
					quantity := el2.(map[string]int)["quantity"]
					times := el2.(map[string]int)["times"]
					dropMatrixElement := models.DropMatrixElement{
						StageID:  stageId,
						ItemID:   itemId,
						RangeID:  i,
						Quantity: quantity,
						Times:    times,
						Server:   server,
					}
					currentBatch = append(currentBatch, dropMatrixElement)
					delete(dropSet, itemId)        // remove existing item ids from drop set
					stageTimesMap[stageId] = times // record stage times into a map
				}
				// add those items which do not show up in the matrix (quantity is 0)
				for itemId := range dropSet {
					dropMatrixElementWithZeroQuantity := models.DropMatrixElement{
						StageID:  stageId,
						ItemID:   itemId,
						RangeID:  i,
						Quantity: 0,
						Times:    stageTimesMap[stageId],
						Server:   server,
					}
					currentBatch = append(currentBatch, dropMatrixElementWithZeroQuantity)
				}
			}
			ch <- currentBatch
			<-limiter

			usedTimeMap.Store(i, int(time.Since(startTime).Microseconds()))
			fmt.Println("   > :", i, "@", time.Since(startTime))
		}(i)
	}

	wg.Wait()

	log.Debug().Msgf("toSave length: %v", len(toSave))

	c.DropMatrixElementRepo.BatchSaveElements(ctx.UserContext(), toSave, server)
	return nil
}

func (c *TestController) calcDropMatrixForTimeRanges(
	ctx *fiber.Ctx, queryServer string, rangeIds []int, stageIdFilter []int, itemIdFilter []int, results *[]map[string]int) error {
	timeRangesMap := make(map[int]models.TimeRange)
	if err := c.getTimeRangesMap(ctx, queryServer, &timeRangesMap); err != nil {
		return err
	}

	var dropInfos []models.DropInfo
	if err := c.getDropInfos(ctx, queryServer, rangeIds, stageIdFilter, itemIdFilter, &dropInfos); err != nil {
		return err
	}

	var dropInfosByRangeId []linq.Group
	linq.From(dropInfos).
		GroupByT(
			func(dropInfo models.DropInfo) int { return dropInfo.RangeID },
			func(dropInfo models.DropInfo) models.DropInfo { return dropInfo }).
		ToSlice(&dropInfosByRangeId)

	for _, el := range dropInfosByRangeId {
		rangeId := el.Key.(int)

		timeRange := timeRangesMap[rangeId]
		// fmt.Printf("timeRange = %s\n", timeRange.Name.String)

		var dropInfosByStageId []linq.Group
		linq.From(dropInfos).GroupByT(
			func(dropInfo models.DropInfo) int { return dropInfo.StageID },
			func(dropInfo models.DropInfo) models.DropInfo { return dropInfo }).
			ToSlice(&dropInfosByStageId)

		quantityResults := []map[string]interface{}{}
		if err := c.calcTotalQuantity(ctx.Context(), queryServer, timeRange, dropInfosByStageId, &quantityResults); err != nil {
			return err
		}
		timesResults := []map[string]interface{}{}
		if err := c.calcTotalTimes(ctx.Context(), queryServer, timeRange, dropInfosByStageId, &timesResults); err != nil {
			return err
		}

		var combinedResults []map[string]int
		combineQuantityAndTimesResults(&quantityResults, &timesResults, &combinedResults)

		*results = combinedResults
	}
	return nil
}

func (c *TestController) getTimeRangesMap(ctx *fiber.Ctx, server string, results *map[int]models.TimeRange) error {
	var timeRanges []models.TimeRange
	if err := c.DB.NewSelect().
		Model(&timeRanges).
		Where("tr.server = ?", server).
		Scan(ctx.Context()); err != nil {
		return err
	}
	linq.From(timeRanges).
		ToMapByT(
			results,
			func(timeRange models.TimeRange) int { return timeRange.RangeID },
			func(timeRange models.TimeRange) models.TimeRange { return timeRange })
	return nil
}

func (c *TestController) getDropInfos(ctx *fiber.Ctx, server string, rangeIds []int, stageIdFilter []int, itemIdFilter []int, results *[]models.DropInfo) error {
	var whereBuilder strings.Builder
	fmt.Fprintf(&whereBuilder, "di.server = ? AND di.range_id IN (?) AND di.drop_type != ? AND di.item_id IS NOT NULL")

	if stageIdFilter != nil && len(stageIdFilter) > 0 {
		fmt.Fprintf(&whereBuilder, " AND di.stage_id IN (?)")
	}
	if itemIdFilter != nil && len(itemIdFilter) > 0 {
		fmt.Fprintf(&whereBuilder, " AND di.item_id IN (?)")
	}
	if err := c.DB.NewSelect().TableExpr("drop_infos as di").Column("di.stage_id", "di.item_id", "di.range_id", "di.accumulable").
		Where(whereBuilder.String(), server, bun.In(rangeIds), "RECOGNITION_ONLY", bun.In(stageIdFilter), bun.In(itemIdFilter)).
		Join("JOIN time_ranges AS tr ON tr.range_id = di.range_id").
		Scan(ctx.Context(), results); err != nil {
		return err
	}
	return nil
}

func (c *TestController) calcTotalQuantity(ctx context.Context, server string, timeRange models.TimeRange, dropInfosByStageId []linq.Group, quantityResults *[]map[string]interface{}) error {
	var b strings.Builder
	fmt.Fprintf(&b, "dr.created_at >= timestamp with time zone '%s'", timeRange.StartTime.Time.Format(time.RFC3339))
	if timeRange.EndTime.Valid {
		fmt.Fprintf(&b, " AND dr.created_at <= timestamp with time zone '%s'", timeRange.EndTime.Time.Format(time.RFC3339))
	}
	b.WriteString(" AND (")
	for idx, el := range dropInfosByStageId {
		stageId := el.Key.(int)
		var itemIds []int
		linq.From(el.Group).
			SelectT(func(dropInfo models.DropInfo) int {
				return int(dropInfo.ItemID.Int64)
			}).
			ToSlice(&itemIds)

		fmt.Fprintf(&b, "dr.stage_id = %d AND dpe.item_id", stageId)
		if len(itemIds) == 1 {
			fmt.Fprintf(&b, " = %d", itemIds[0])
		} else {
			var itemIdsStr []string
			for _, itemId := range itemIds {
				itemIdsStr = append(itemIdsStr, strconv.FormatInt(int64(itemId), 10))
			}
			fmt.Fprintf(&b, " IN (%s)", strings.Join(itemIdsStr, ","))
		}
		if idx != len(dropInfosByStageId)-1 {
			b.WriteString(" OR ")
		}
	}
	b.WriteString(")")

	*quantityResults = make([]map[string]interface{}, 0)
	if err := c.DB.NewSelect().
		TableExpr("drop_reports AS dr").
		Column("dr.stage_id", "dpe.item_id").
		ColumnExpr("SUM(dpe.quantity) AS total_quantity").
		Join("JOIN drop_pattern_elements AS dpe ON dpe.drop_pattern_id = dr.pattern_id").
		Where("deleted = false AND dr.server = ? AND "+b.String(), server).
		Group("dr.stage_id", "dpe.item_id").
		Scan(ctx, quantityResults); err != nil {
		return err
	}
	return nil
}

func (c *TestController) calcTotalTimes(ctx context.Context, server string, timeRange models.TimeRange, dropInfosByStageId []linq.Group, timesResults *[]map[string]interface{}) error {
	var b strings.Builder
	fmt.Fprintf(&b, "dr.created_at >= timestamp with time zone '%s'", timeRange.StartTime.Time.Format(time.RFC3339))
	if timeRange.EndTime.Valid {
		fmt.Fprintf(&b, " AND dr.created_at <= timestamp with time zone '%s'", timeRange.EndTime.Time.Format(time.RFC3339))
	}
	b.WriteString(" AND dr.stage_id")
	var stageIds []int
	linq.From(dropInfosByStageId).
		SelectT(func(group linq.Group) int { return group.Key.(int) }).
		Distinct().
		SortT(func(a int, b int) bool { return a < b }).
		ToSlice(&stageIds)
	if len(stageIds) == 1 {
		fmt.Fprintf(&b, "= %d", stageIds[0])
	} else {
		var stageIdsStr []string
		for _, stageId := range stageIds {
			stageIdsStr = append(stageIdsStr, strconv.FormatInt(int64(stageId), 10))
		}
		fmt.Fprintf(&b, " IN (%s)", strings.Join(stageIdsStr, ","))
	}

	*timesResults = make([]map[string]interface{}, 0)
	if err := c.DB.NewSelect().
		TableExpr("drop_reports AS dr").
		Column("dr.stage_id").
		ColumnExpr("COUNT(*) AS total_times").
		Where("deleted = false AND dr.server = ? AND "+b.String(), server).
		Group("dr.stage_id").
		Scan(ctx, timesResults); err != nil {
		return err
	}
	return nil
}

func combineQuantityAndTimesResults(quantityResults *[]map[string]interface{}, timesResults *[]map[string]interface{}, combinedResults *[]map[string]int) {
	var firstGroupResults []linq.Group
	*combinedResults = make([]map[string]int, 0)
	linq.From(*quantityResults).
		GroupByT(
			func(result map[string]interface{}) interface{} { return result["stage_id"] },
			func(result map[string]interface{}) map[string]interface{} { return result }).
		ToSlice(&firstGroupResults)
	quantityResultsMap := make(map[int]map[int]int)
	for _, firstGroupElements := range firstGroupResults {
		stageId := int(firstGroupElements.Key.(int64))
		resultsMap := make(map[int]int, 0)
		linq.From(firstGroupElements.Group).
			ToMapByT(&resultsMap,
				func(el interface{}) int { return int(el.(map[string]interface{})["item_id"].(int64)) },
				func(el interface{}) int { return int(el.(map[string]interface{})["total_quantity"].(int64)) })
		quantityResultsMap[stageId] = resultsMap
	}

	var secondGroupResults []linq.Group
	linq.From(*timesResults).
		GroupByT(
			func(result map[string]interface{}) interface{} { return result["stage_id"] },
			func(result map[string]interface{}) map[string]interface{} { return result }).
		ToSlice(&secondGroupResults)
	for _, secondGroupResults := range secondGroupResults {
		stageId := int(secondGroupResults.Key.(int64))
		quantityResultsMapForOneStage := quantityResultsMap[stageId]
		for _, el := range secondGroupResults.Group {
			times := int(el.(map[string]interface{})["total_times"].(int64))
			for itemId, quantity := range quantityResultsMapForOneStage {
				*combinedResults = append(*combinedResults, map[string]int{
					"stageId":  stageId,
					"itemId":   itemId,
					"times":    times,
					"quantity": quantity,
				})
			}
		}
	}
}

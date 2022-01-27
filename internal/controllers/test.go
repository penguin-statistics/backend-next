package controllers

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ahmetb/go-linq/v3"
	"github.com/gofiber/fiber/v2"
	"github.com/penguin-statistics/fiberotel"
	"github.com/uptrace/bun"
	"go.opentelemetry.io/otel/attribute"
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
	v3.Get("/test", c.Test)
}

func (c *TestController) Test(ctx *fiber.Ctx) error {
	queryServer := "CN"

	c.RefreshGlobalDropMatrix(ctx, queryServer)

	results := []map[string]int{}
	return ctx.JSON(&results)
}

func (c *TestController) RefreshGlobalDropMatrix(ctx *fiber.Ctx, server string) error {
	toSave := make([]models.DropMatrixElement, 0)
	// iterate over all time ranges
	for i := 1; i <= 220; i++ {
		timeRangeIDs := []int{i}
		var results []map[string]int
		// get drop matrix calc results
		if err := c.calcDropMatrixForTimeRanges(ctx, server, timeRangeIDs, make([]int, 0), make([]int, 0), &results); err != nil {
			return err
		}

		stageTimesMap := map[int]int{} // save stage times for later use

		// grouping results by stage id
		var groupedResults []linq.Group
		linq.From(results).
			GroupByT(
				func(el map[string]int) int { return el["stageID"] },
				func(el map[string]int) map[string]int { return el }).ToSlice(&groupedResults)
		for _, el := range groupedResults {
			stageID := el.Key.(int)

			// get all item ids which are dropped in this stage and in this time range
			dropItemIDs, err := c.DropInfoRepo.GetItemDropSetByStageIDAndRangeID(ctx.UserContext(), server, stageID, i)
			if err != nil {
				return err
			}
			// use a fake hashset to save item ids
			dropSet := map[int]struct{}{}
			for _, itemID := range dropItemIDs {
				dropSet[itemID] = struct{}{}
			}

			for _, el2 := range el.Group {
				itemID := el2.(map[string]int)["itemID"]
				quantity := el2.(map[string]int)["quantity"]
				times := el2.(map[string]int)["times"]
				dropMatrixElement := models.DropMatrixElement{
					StageID:  stageID,
					ItemID:   itemID,
					RangeID:  i,
					Quantity: quantity,
					Times:    times,
					Server:   server,
				}
				toSave = append(toSave, dropMatrixElement)
				delete(dropSet, itemID)        // remove existing item ids from drop set
				stageTimesMap[stageID] = times // record stage times into a map
			}
			// add those items which do not show up in the matrix (quantity is 0)
			for itemID := range dropSet {
				dropMatrixElementWithZeroQuantity := models.DropMatrixElement{
					StageID:  stageID,
					ItemID:   itemID,
					RangeID:  i,
					Quantity: 0,
					Times:    stageTimesMap[stageID],
					Server:   server,
				}
				toSave = append(toSave, dropMatrixElementWithZeroQuantity)
			}
		}
	}
	c.DropMatrixElementRepo.DeleteByServer(ctx.UserContext(), server)
	c.DropMatrixElementRepo.BatchSaveElements(ctx.UserContext(), toSave)
	return nil
}

func (c *TestController) calcDropMatrixForTimeRanges(
	ctx *fiber.Ctx, queryServer string, timeRangeIDs []int, stageIDFilter []int, itemIDFilter []int, results *[]map[string]int) error {
	timeRangesMap := make(map[int]models.TimeRange)
	if err := c.getTimeRangesMap(ctx, queryServer, &timeRangesMap); err != nil {
		return err
	}

	var dropInfos []models.DropInfo
	if err := c.getDropInfos(ctx, queryServer, timeRangeIDs, stageIDFilter, itemIDFilter, &dropInfos); err != nil {
		return err
	}

	var dropInfosByTimeRangeID []linq.Group
	linq.From(dropInfos).
		GroupByT(
			func(dropInfo models.DropInfo) int { return dropInfo.RangeID },
			func(dropInfo models.DropInfo) models.DropInfo { return dropInfo }).
		ToSlice(&dropInfosByTimeRangeID)

	ctxI1, span := fiberotel.StartTracerFromCtx(ctx, "calcDropMatrixForTimeRanges")
	defer span.End()

	for _, el := range dropInfosByTimeRangeID {
		timeRangeID := el.Key.(int)

		ctxI2, spanouter := fiberotel.Tracer.Start(ctxI1, "calcDropMatrixForTimeRanges.loop")
		spanouter.SetAttributes(attribute.Int("timeRangeID", timeRangeID))

		timeRange := timeRangesMap[timeRangeID]
		fmt.Printf("timeRange = %s\n", timeRange.Name.String)

		_, span1 := fiberotel.Tracer.Start(ctxI2, "calcDropMatrixForTimeRanges.loop.getDropInfosByTimeRangeID")

		var dropInfosByStageID []linq.Group
		linq.From(dropInfos).GroupByT(
			func(dropInfo models.DropInfo) int { return dropInfo.StageID },
			func(dropInfo models.DropInfo) models.DropInfo { return dropInfo }).
			ToSlice(&dropInfosByStageID)

		span1.End()

		ctxI3, span2 := fiberotel.Tracer.Start(ctxI2, "calcDropMatrixForTimeRanges.loop.getQuantityResults")

		quantityResults := []map[string]interface{}{}
		if err := c.calcTotalQuantity(ctxI3, queryServer, timeRange, dropInfosByStageID, &quantityResults); err != nil {
			return err
		}

		span2.End()

		_, span3 := fiberotel.Tracer.Start(ctxI2, "calcDropMatrixForTimeRanges.loop.getTimesResults")

		timesResults := []map[string]interface{}{}
		if err := c.calcTotalTimes(ctx.UserContext(), queryServer, timeRange, dropInfosByStageID, &timesResults); err != nil {
			return err
		}

		span3.End()

		var combinedResults []map[string]int
		combineQuantityAndTimesResults(&quantityResults, &timesResults, &combinedResults)

		*results = combinedResults

		spanouter.End()
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

func (c *TestController) getDropInfos(ctx *fiber.Ctx, server string, timeRangeIDs []int, stageIDFilter []int, itemIDFilter []int, results *[]models.DropInfo) error {
	var whereBuilder strings.Builder
	fmt.Fprintf(&whereBuilder, "di.server = ? AND di.time_range_id IN (?) AND di.drop_type != ? AND di.item_id IS NOT NULL")

	if len(stageIDFilter) > 0 {
		fmt.Fprintf(&whereBuilder, " AND di.stage_id IN (?)")
	}
	if len(itemIDFilter) > 0 {
		fmt.Fprintf(&whereBuilder, " AND di.item_id IN (?)")
	}
	if err := c.DB.NewSelect().TableExpr("drop_infos as di").Column("di.stage_id", "di.item_id", "di.time_range_id", "di.accumulable").
		Where(whereBuilder.String(), server, bun.In(timeRangeIDs), "RECOGNITION_ONLY", bun.In(stageIDFilter), bun.In(itemIDFilter)).
		Join("JOIN time_ranges AS tr ON tr.range_id = di.time_range_id").
		Scan(ctx.Context(), results); err != nil {
		return err
	}
	return nil
}

func (c *TestController) calcTotalQuantity(ctx context.Context, server string, timeRange models.TimeRange, dropInfosByStageID []linq.Group, quantityResults *[]map[string]interface{}) error {
	var b strings.Builder
	fmt.Fprintf(&b, "dr.created_at >= timestamp with time zone '%s'", timeRange.StartTime.Time.Format(time.RFC3339))
	if timeRange.EndTime.Valid {
		fmt.Fprintf(&b, " AND dr.created_at <= timestamp with time zone '%s'", timeRange.EndTime.Time.Format(time.RFC3339))
	}
	b.WriteString(" AND (")
	for idx, el := range dropInfosByStageID {
		stageID := el.Key.(int)
		var itemIDs []int
		linq.From(el.Group).
			SelectT(func(dropInfo models.DropInfo) int {
				return int(dropInfo.ItemID.Int64)
			}).
			ToSlice(&itemIDs)

		fmt.Fprintf(&b, "dr.stage_id = %d AND dpe.item_id", stageID)
		if len(itemIDs) == 1 {
			fmt.Fprintf(&b, " = %d", itemIDs[0])
		} else {
			var itemIDsStr []string
			for _, itemID := range itemIDs {
				itemIDsStr = append(itemIDsStr, strconv.FormatInt(int64(itemID), 10))
			}
			fmt.Fprintf(&b, " IN (%s)", strings.Join(itemIDsStr, ","))
		}
		if idx != len(dropInfosByStageID)-1 {
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

func (c *TestController) calcTotalTimes(ctx context.Context, server string, timeRange models.TimeRange, dropInfosByStageID []linq.Group, timesResults *[]map[string]interface{}) error {
	var b strings.Builder
	fmt.Fprintf(&b, "dr.created_at >= timestamp with time zone '%s'", timeRange.StartTime.Time.Format(time.RFC3339))
	if timeRange.EndTime.Valid {
		fmt.Fprintf(&b, " AND dr.created_at <= timestamp with time zone '%s'", timeRange.EndTime.Time.Format(time.RFC3339))
	}
	b.WriteString(" AND dr.stage_id")
	var stageIDs []int
	linq.From(dropInfosByStageID).
		SelectT(func(group linq.Group) int { return group.Key.(int) }).
		Distinct().
		SortT(func(a int, b int) bool { return a < b }).
		ToSlice(&stageIDs)
	if len(stageIDs) == 1 {
		fmt.Fprintf(&b, "= %d", stageIDs[0])
	} else {
		var stageIDsStr []string
		for _, stageID := range stageIDs {
			stageIDsStr = append(stageIDsStr, strconv.FormatInt(int64(stageID), 10))
		}
		fmt.Fprintf(&b, " IN (%s)", strings.Join(stageIDsStr, ","))
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
		stageID := int(firstGroupElements.Key.(int64))
		resultsMap := make(map[int]int, 0)
		linq.From(firstGroupElements.Group).
			ToMapByT(&resultsMap,
				func(el interface{}) int { return int(el.(map[string]interface{})["item_id"].(int64)) },
				func(el interface{}) int { return int(el.(map[string]interface{})["total_quantity"].(int64)) })
		quantityResultsMap[stageID] = resultsMap
	}

	var secondGroupResults []linq.Group
	linq.From(*timesResults).
		GroupByT(
			func(result map[string]interface{}) interface{} { return result["stage_id"] },
			func(result map[string]interface{}) map[string]interface{} { return result }).
		ToSlice(&secondGroupResults)
	for _, secondGroupResults := range secondGroupResults {
		stageID := int(secondGroupResults.Key.(int64))
		quantityResultsMapForOneStage := quantityResultsMap[stageID]
		for _, el := range secondGroupResults.Group {
			times := int(el.(map[string]interface{})["total_times"].(int64))
			for itemID, quantity := range quantityResultsMapForOneStage {
				*combinedResults = append(*combinedResults, map[string]int{
					"stageID":  stageID,
					"itemID":   itemID,
					"times":    times,
					"quantity": quantity})
			}
		}
	}
}

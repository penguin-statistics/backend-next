package controllers

import (
	"fmt"
	. "github.com/ahmetb/go-linq/v3"
	"github.com/gofiber/fiber/v2"
	. "github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/server"
	"github.com/uptrace/bun"
	"strconv"
	"strings"
	"time"
)

type TestController struct {
	db *bun.DB
}

func RegisterTestController(v3 *server.V3, db *bun.DB) {
	c := &TestController{
		db: db,
	}

	v3.Get("/test", c.Test)
}

func (c *TestController) Test(ctx *fiber.Ctx) error {
	queryServer := "CN"

	var stageIds []int64
	//for i := 1; i <= 507; i++ {
	//	stageIds = append(stageIds, int64(i))
	//}

	//stageIds := []int64{18, 103, 356}

	//startTime := time.Date(2019, time.May, 1, 2, 0, 0, 0, time.UTC)
	//endTime := null.Time{Time: time.Date(2021, time.November, 28, 11, 18, 0, 0, time.UTC), Valid: true}

	timeRangeIDs := []int64{1, 2, 3, 4, 15, 43, 86, 87, 129, 175, 180, 121}
	var itemIDs []int64
	//itemIDs = append(itemIDs, 7)
	//itemIDs = append(itemIDs, 12)

	var results []map[string]interface{}
	if err := c.calcDropMatrixForTimeRanges(ctx, queryServer, timeRangeIDs, stageIds, itemIDs, &results); err != nil {
		return err
	}

	return ctx.JSON(&results)
}

func (c *TestController) calcDropMatrixForTimeRanges(
	ctx *fiber.Ctx, queryServer string, timeRangeIDs []int64, stageIDFilter []int64, itemIDFilter []int64, results *[]map[string]interface{}) error {
	timeRangesMap := make(map[int64]TimeRange)
	if err := c.getTimeRangesMap(ctx, queryServer, &timeRangesMap); err != nil {
		return err
	}

	var dropInfos []DropInfo
	if err := c.getDropInfos(ctx, queryServer, timeRangeIDs, stageIDFilter, itemIDFilter, &dropInfos); err != nil {
		return err
	}

	var dropInfosByTimeRangeID []Group
	From(dropInfos).
		GroupByT(
			func(dropInfo DropInfo) int64 { return dropInfo.TimeRangeID },
			func(dropInfo DropInfo) DropInfo { return dropInfo }).
		ToSlice(&dropInfosByTimeRangeID)

	for _, el := range dropInfosByTimeRangeID {
		timeRangeID := el.Key.(int64)
		timeRange := timeRangesMap[timeRangeID]
		fmt.Printf("timeRange = %s\n", timeRange.Name.String)

		var dropInfosByStageID []Group
		From(dropInfos).GroupByT(
			func(dropInfo DropInfo) int64 { return dropInfo.StageID },
			func(dropInfo DropInfo) DropInfo { return dropInfo }).
			ToSlice(&dropInfosByStageID)

		quantityResults := make([]map[string]interface{}, 0)
		if err := c.calcTotalQuantity(ctx, queryServer, timeRange, dropInfosByStageID, &quantityResults); err != nil {
			return err
		}

		timesResults := make([]map[string]interface{}, 0)
		if err := c.calcTotalTimes(ctx, queryServer, timeRange, dropInfosByStageID, &timesResults); err != nil {
			return err
		}

		*results = timesResults
	}
	return nil
}

func (c *TestController) getTimeRangesMap(ctx *fiber.Ctx, server string, results *map[int64]TimeRange) error {
	var timeRanges []TimeRange
	if err := c.db.NewSelect().
		Model(&timeRanges).
		Where("tr.server = ?", server).
		Scan(ctx.Context()); err != nil {
		return err
	}
	From(timeRanges).
		ToMapByT(
			results,
			func(timeRange TimeRange) int64 { return timeRange.RangeID },
			func(timeRange TimeRange) TimeRange { return timeRange })
	return nil
}

func (c *TestController) getDropInfos(ctx *fiber.Ctx, server string, timeRangeIDs []int64, stageIDFilter []int64, itemIDFilter []int64, results *[]DropInfo) error {
	var whereBuilder strings.Builder
	fmt.Fprintf(&whereBuilder, "di.server = ? AND di.time_range_id IN (?) AND di.drop_type != ? AND di.item_id IS NOT NULL")
	if stageIDFilter != nil && len(stageIDFilter) != 0 {
		fmt.Fprintf(&whereBuilder, " AND di.stage_id IN (?)")
	}
	if itemIDFilter != nil && len(itemIDFilter) != 0 {
		fmt.Fprintf(&whereBuilder, " AND di.item_id IN (?)")
	}
	if err := c.db.NewSelect().TableExpr("drop_infos as di").Column("di.stage_id", "di.item_id", "di.time_range_id", "di.accumulable").
		Where(whereBuilder.String(), server, bun.In(timeRangeIDs), "RECOGNITION_ONLY", bun.In(stageIDFilter), bun.In(itemIDFilter)).
		Join("JOIN time_ranges AS tr ON tr.range_id = di.time_range_id").
		Scan(ctx.Context(), results); err != nil {
		return err
	}
	return nil
}

func (c *TestController) calcTotalQuantity(ctx *fiber.Ctx, server string, timeRange TimeRange, dropInfosByStageID []Group, quantityResults *[]map[string]interface{}) error {
	var b strings.Builder
	fmt.Fprintf(&b, "dr.created_at >= timestamp with time zone '%s'", timeRange.StartTime.Time.Format(time.RFC3339))
	if timeRange.EndTime.Valid {
		fmt.Fprintf(&b, " AND dr.created_at <= timestamp with time zone '%s'", timeRange.EndTime.Time.Format(time.RFC3339))
	}
	b.WriteString(" AND (")
	for idx, el := range dropInfosByStageID {
		stageID := el.Key.(int64)
		var itemIDs []int64
		From(el.Group).SelectT(func(dropInfo DropInfo) int64 { return dropInfo.ItemID.Int64 }).ToSlice(&itemIDs)

		fmt.Fprintf(&b, "dr.stage_id = %d AND dpe.item_id", stageID)
		if len(itemIDs) == 1 {
			fmt.Fprintf(&b, " = %d", itemIDs[0])
		} else {
			var itemIDsStr []string
			for _, itemID := range itemIDs {
				itemIDsStr = append(itemIDsStr, strconv.FormatInt(itemID, 10))
			}
			fmt.Fprintf(&b, " IN (%s)", strings.Join(itemIDsStr, ","))
		}
		if idx != len(dropInfosByStageID)-1 {
			b.WriteString(" OR ")
		}
	}
	b.WriteString(")")

	*quantityResults = make([]map[string]interface{}, 0)
	if err := c.db.NewSelect().
		TableExpr("drop_reports AS dr").
		Column("dr.stage_id", "dpe.item_id").
		ColumnExpr("SUM(dpe.quantity) AS total_quantity").
		Join("JOIN drop_pattern_elements AS dpe ON dpe.drop_pattern_id = dr.pattern_id").
		Where("deleted = false AND dr.server = ? AND "+b.String(), server).
		Group("dr.stage_id", "dpe.item_id").
		Scan(ctx.Context(), quantityResults); err != nil {
		return err
	}
	return nil
}

func (c *TestController) calcTotalTimes(ctx *fiber.Ctx, server string, timeRange TimeRange, dropInfosByStageID []Group, timesResults *[]map[string]interface{}) error {
	var b strings.Builder
	fmt.Fprintf(&b, "dr.created_at >= timestamp with time zone '%s'", timeRange.StartTime.Time.Format(time.RFC3339))
	if timeRange.EndTime.Valid {
		fmt.Fprintf(&b, " AND dr.created_at <= timestamp with time zone '%s'", timeRange.EndTime.Time.Format(time.RFC3339))
	}
	b.WriteString(" AND dr.stage_id")
	var stageIDs []int64
	From(dropInfosByStageID).
		SelectT(func(group Group) int64 { return group.Key.(int64) }).
		Distinct().
		SortT(func(a int64, b int64) bool { return a < b }).
		ToSlice(&stageIDs)
	if len(stageIDs) == 1 {
		fmt.Fprintf(&b, "= %d", stageIDs[0])
	} else {
		var stageIDsStr []string
		for _, stageID := range stageIDs {
			stageIDsStr = append(stageIDsStr, strconv.FormatInt(stageID, 10))
		}
		fmt.Fprintf(&b, " IN (%s)", strings.Join(stageIDsStr, ","))
	}

	*timesResults = make([]map[string]interface{}, 0)
	if err := c.db.NewSelect().
		TableExpr("drop_reports AS dr").
		Column("dr.stage_id").
		ColumnExpr("COUNT(*) AS total_times").
		Where("deleted = false AND dr.server = ? AND "+b.String(), server).
		Group("dr.stage_id").
		Scan(ctx.Context(), timesResults); err != nil {
		return err
	}
	return nil
}

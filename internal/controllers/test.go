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
	"github.com/penguin-statistics/backend-next/internal/server"
)

type TestController struct {
	fx.In

	DB *bun.DB
}

func RegisterTestController(v3 *server.V3, c TestController) {
	v3.Get("/test", c.Test)
}

func (c *TestController) Test(ctx *fiber.Ctx) error {
	queryServer := "CN"

	var stageIds []int

	timeRangeIDs := []int{1, 2, 3, 4, 15, 43, 86, 87, 129, 175, 180, 121}
	var itemIDs []int

	var results []map[string]interface{}
	if err := c.calcDropMatrixForTimeRanges(ctx, queryServer, timeRangeIDs, stageIds, itemIDs, &results); err != nil {
		return err
	}

	return ctx.JSON(&results)
}

func (c *TestController) calcDropMatrixForTimeRanges(
	ctx *fiber.Ctx, queryServer string, timeRangeIDs []int, stageIDFilter []int, itemIDFilter []int, results *[]map[string]interface{}) error {
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
			func(dropInfo models.DropInfo) int { return dropInfo.TimeRangeID },
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

		*results = timesResults

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

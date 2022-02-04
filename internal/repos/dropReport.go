package repos

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"

	"github.com/penguin-statistics/backend-next/internal/models"
)

type DropReportRepo struct {
	DB *bun.DB
	locMap map[string] *time.Location
}

func NewDropReportRepo(db *bun.DB) *DropReportRepo {
	return &DropReportRepo{
		DB: db, 
		locMap: map[string] *time.Location {
			"CN": time.FixedZone("UTC+8", +8*60*60),
			"US": time.FixedZone("UTC-7", -7*60*60),
			"JP": time.FixedZone("UTC+9", +9*60*60),
			"KR": time.FixedZone("UTC+9", +9*60*60),
		},
	}
}

func (s *DropReportRepo) CreateDropReport(ctx context.Context, tx bun.Tx, dropReport *models.DropReport) error {
	_, err := tx.NewInsert().
		Model(dropReport).
		ExcludeColumn("created_at").
		Exec(ctx)
	return err
}

func (s *DropReportRepo) CalcTotalQuantityForDropMatrix(ctx context.Context, server string, timeRange *models.TimeRange, stageIdItemIdMap map[int][]int, accountId *null.Int) ([]map[string]interface{}, error) {
	results := make([]map[string]interface{}, 0)
	if len(stageIdItemIdMap) == 0 {
		return results, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "dr.created_at >= timestamp with time zone '%s'", timeRange.StartTime.Format(time.RFC3339))
	fmt.Fprintf(&b, " AND dr.created_at <= timestamp with time zone '%s'", timeRange.EndTime.Format(time.RFC3339))
	stageConditions := make([]string, 0)
	for stageId, itemIds := range stageIdItemIdMap {
		var stageB strings.Builder
		fmt.Fprintf(&stageB, "dr.stage_id = %d AND dpe.item_id", stageId)
		if len(itemIds) == 1 {
			fmt.Fprintf(&stageB, " = %d", itemIds[0])
		} else {
			var itemIdsStr []string
			for _, itemId := range itemIds {
				itemIdsStr = append(itemIdsStr, strconv.FormatInt(int64(itemId), 10))
			}
			fmt.Fprintf(&stageB, " IN (%s)", strings.Join(itemIdsStr, ","))
		}
		stageConditions = append(stageConditions, stageB.String())
	}
	fmt.Fprintf(&b, " AND (%s)", strings.Join(stageConditions, " OR "))

	query := s.DB.NewSelect().
		TableExpr("drop_reports AS dr").
		Column("dr.stage_id", "dpe.item_id").
		ColumnExpr("SUM(dpe.quantity) AS total_quantity").
		Join("JOIN drop_pattern_elements AS dpe ON dpe.drop_pattern_id = dr.pattern_id")
	if accountId.Valid {
		query = query.Where("dr.account_id = ?", accountId.Int64)
	} else {
		query = query.Where("dr.reliable = true")
	}
	query = query.Where("dr.deleted = false AND dr.server = ? AND "+b.String(), server)
	if err := query.
		Group("dr.stage_id", "dpe.item_id").
		Scan(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *DropReportRepo) CalcTotalQuantityForPatternMatrix(ctx context.Context, server string, timeRange *models.TimeRange, stageIds []int, accountId *null.Int) ([]map[string]interface{}, error) {
	results := make([]map[string]interface{}, 0)
	if len(stageIds) == 0 {
		return results, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "dr.created_at >= timestamp with time zone '%s'", timeRange.StartTime.Format(time.RFC3339))
	fmt.Fprintf(&b, " AND dr.created_at <= timestamp with time zone '%s'", timeRange.EndTime.Format(time.RFC3339))
	b.WriteString(" AND dr.stage_id")
	if len(stageIds) == 1 {
		fmt.Fprintf(&b, "= %d", stageIds[0])
	} else {
		var stageIdsStr []string
		for _, stageId := range stageIds {
			stageIdsStr = append(stageIdsStr, strconv.FormatInt(int64(stageId), 10))
		}
		fmt.Fprintf(&b, " IN (%s)", strings.Join(stageIdsStr, ","))
	}

	query := s.DB.NewSelect().
		TableExpr("drop_reports AS dr").
		Column("dr.stage_id", "dr.pattern_id").
		ColumnExpr("COUNT(*) AS total_quantity")
	if accountId.Valid {
		query = query.Where("dr.account_id = ?", accountId.Int64)
	} else {
		query = query.Where("dr.reliable = true")
	}
	query = query.Where("dr.deleted = false AND dr.server = ? AND dr.times = ? AND "+b.String(), server, 1)
	if err := query.
		Group("dr.stage_id", "dr.pattern_id").
		Scan(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *DropReportRepo) CalcTotalTimes(ctx context.Context, server string, timeRange *models.TimeRange, stageIds []int, accountId *null.Int, excludeNonOneTimes bool) ([]map[string]interface{}, error) {
	results := make([]map[string]interface{}, 0)
	if len(stageIds) == 0 {
		return results, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "dr.created_at >= timestamp with time zone '%s'", timeRange.StartTime.Format(time.RFC3339))
	fmt.Fprintf(&b, " AND dr.created_at <= timestamp with time zone '%s'", timeRange.EndTime.Format(time.RFC3339))
	b.WriteString(" AND dr.stage_id")
	if len(stageIds) == 1 {
		fmt.Fprintf(&b, "= %d", stageIds[0])
	} else {
		var stageIdsStr []string
		for _, stageId := range stageIds {
			stageIdsStr = append(stageIdsStr, strconv.FormatInt(int64(stageId), 10))
		}
		fmt.Fprintf(&b, " IN (%s)", strings.Join(stageIdsStr, ","))
	}

	query := s.DB.NewSelect().
		TableExpr("drop_reports AS dr").
		Column("dr.stage_id").
		ColumnExpr("COUNT(*) AS total_times")
	if accountId.Valid {
		query = query.Where("dr.account_id = ?", accountId.Int64)
	} else {
		query = query.Where("dr.reliable = true")
	}
	if excludeNonOneTimes {
		query = query.Where("dr.times = 1")
	}
	query = query.Where("dr.deleted = false AND dr.server = ? AND "+b.String(), server)
	if err := query.
		Group("dr.stage_id").
		Scan(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *DropReportRepo) CalcTotalQuantityForTrend(
	ctx context.Context, server string, startTime *time.Time, intervalLength_hrs int, intervalNum int, stageIdItemIdMap map[int][]int, accountId *null.Int) ([]map[string]interface{}, error) {
	results := make([]map[string]interface{}, 0)
	if len(stageIdItemIdMap) == 0 {
		return results, nil
	}

	gameDayStart := s.getGameDateStartTime(server, *startTime)
	lastDayEnd := gameDayStart.Add(time.Hour * time.Duration(intervalLength_hrs * (intervalNum + 1)))

	var b strings.Builder
	fmt.Fprintf(&b, "dr.created_at >= to_timestamp(%d)", gameDayStart.Unix())
	fmt.Fprintf(&b, " AND dr.created_at <= to_timestamp(%d)", lastDayEnd.Unix())
	stageConditions := make([]string, 0)
	for stageId, itemIds := range stageIdItemIdMap {
		var stageB strings.Builder
		fmt.Fprintf(&stageB, "dr.stage_id = %d AND dpe.item_id", stageId)
		if len(itemIds) == 1 {
			fmt.Fprintf(&stageB, " = %d", itemIds[0])
		} else {
			var itemIdsStr []string
			for _, itemId := range itemIds {
				itemIdsStr = append(itemIdsStr, strconv.FormatInt(int64(itemId), 10))
			}
			fmt.Fprintf(&stageB, " IN (%s)", strings.Join(itemIdsStr, ","))
		}
		stageConditions = append(stageConditions, stageB.String())
	}
	fmt.Fprintf(&b, " AND (%s)", strings.Join(stageConditions, " OR "))

	var subQueryExprBuilder strings.Builder
	fmt.Fprintf(&subQueryExprBuilder, "to_timestamp(?) + (n || ' hours')::interval AS interval_start, ")
	fmt.Fprintf(&subQueryExprBuilder, "to_timestamp(?) + ((n + ?) || ' hours')::interval AS interval_end, ")
	fmt.Fprintf(&subQueryExprBuilder, "(n / ?) AS group_idx")	
	subQuery := s.DB.NewSelect().
		TableExpr("generate_series(?, ? * ?, ?) AS n", 0, intervalLength_hrs, intervalNum, intervalLength_hrs).
		ColumnExpr(subQueryExprBuilder.String(), 
			gameDayStart.Unix(), 
			gameDayStart.Unix(), 
			intervalLength_hrs, 
			intervalLength_hrs,
		)
	query := s.DB.NewSelect().
		With("intervals", subQuery).
		TableExpr("drop_reports AS dr").
		Column("sub.group_idx", "sub.interval_start", "sub.interval_end", "dr.stage_id", "dpe.item_id").
		ColumnExpr("SUM(dpe.quantity) AS total_quantity").
		Join("JOIN drop_pattern_elements AS dpe ON dpe.drop_pattern_id = dr.pattern_id").
		Join("RIGHT JOIN intervals AS sub").
		JoinOn("dr.created_at >= sub.interval_start AND dr.created_at < sub.interval_end")
	if accountId.Valid {
		query = query.Where("dr.account_id = ?", accountId.Int64)
	} else {
		query = query.Where("dr.reliable = true")
	}
	query = query.Where("dr.deleted = false AND dr.server = ? AND "+b.String(), server)
	if err := query.
		Group("sub.group_idx", "sub.interval_start", "sub.interval_end", "dr.stage_id", "dpe.item_id").
		Scan(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *DropReportRepo) CalcTotalTimesForTrend(
	ctx context.Context, server string, startTime *time.Time, intervalLength_hrs int, intervalNum int, stageIds []int, accountId *null.Int) ([]map[string]interface{}, error) {
	results := make([]map[string]interface{}, 0)
	if len(stageIds) == 0 {
		return results, nil
	}

	gameDayStart := s.getGameDateStartTime(server, *startTime)
	lastDayEnd := gameDayStart.Add(time.Hour * time.Duration(intervalLength_hrs * (intervalNum + 1)))

	var b strings.Builder
	fmt.Fprintf(&b, "dr.created_at >= to_timestamp(%d)", gameDayStart.Unix())
	fmt.Fprintf(&b, " AND dr.created_at <= to_timestamp(%d)", lastDayEnd.Unix())
	b.WriteString(" AND dr.stage_id")
	if len(stageIds) == 1 {
		fmt.Fprintf(&b, "= %d", stageIds[0])
	} else {
		var stageIdsStr []string
		for _, stageId := range stageIds {
			stageIdsStr = append(stageIdsStr, strconv.FormatInt(int64(stageId), 10))
		}
		fmt.Fprintf(&b, " IN (%s)", strings.Join(stageIdsStr, ","))
	}

	var subQueryExprBuilder strings.Builder
	fmt.Fprintf(&subQueryExprBuilder, "to_timestamp(?) + (n || ' hours')::interval AS interval_start, ")
	fmt.Fprintf(&subQueryExprBuilder, "to_timestamp(?) + ((n + ?) || ' hours')::interval AS interval_end, ")
	fmt.Fprintf(&subQueryExprBuilder, "(n / ?) AS group_idx")	
	subQuery := s.DB.NewSelect().
		TableExpr("generate_series(?, ? * ?, ?) AS n", 0, intervalLength_hrs, intervalNum, intervalLength_hrs).
		ColumnExpr(subQueryExprBuilder.String(), 
			gameDayStart.Unix(), 
			gameDayStart.Unix(), 
			intervalLength_hrs, 
			intervalLength_hrs,
		)
	query := s.DB.NewSelect().
		With("intervals", subQuery).
		TableExpr("drop_reports AS dr").
		Column("sub.group_idx", "sub.interval_start", "sub.interval_end", "dr.stage_id").
		ColumnExpr("SUM(dr.times) AS total_times").
		Join("RIGHT JOIN intervals AS sub").
		JoinOn("dr.created_at >= sub.interval_start AND dr.created_at < sub.interval_end")
	if accountId.Valid {
		query = query.Where("dr.account_id = ?", accountId.Int64)
	} else {
		query = query.Where("dr.reliable = true")
	}
	query = query.Where("dr.deleted = false AND dr.server = ? AND "+b.String(), server)
	if err := query.
		Group("sub.group_idx", "sub.interval_start", "sub.interval_end", "dr.stage_id").
		Scan(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *DropReportRepo) getGameDateStartTime(server string, t time.Time) time.Time {
	loc := s.locMap[server]
	t = t.In(loc)
	fmt.Println(t)
	if t.Hour() < 4 {
		t = t.AddDate(0, 0, -1)
	}
	newT := time.Date(t.Year(), t.Month(), t.Day(), 4, 0, 0, 0, loc)
	return newT
}

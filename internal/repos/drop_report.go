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
	"github.com/penguin-statistics/backend-next/internal/models/shims"
	"github.com/penguin-statistics/backend-next/internal/utils/pquery"
)

type DropReportRepo struct {
	DB     *bun.DB
	locMap map[string]*time.Location
}

func NewDropReportRepo(db *bun.DB) *DropReportRepo {
	return &DropReportRepo{
		DB: db,
		locMap: map[string]*time.Location{
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

func (s *DropReportRepo) CalcTotalQuantityForDropMatrix(
	ctx context.Context, server string, timeRange *models.TimeRange, stageIdItemIdMap map[int][]int, accountId *null.Int,
) ([]*models.TotalQuantityResultForDropMatrix, error) {
	results := make([]*models.TotalQuantityResultForDropMatrix, 0)
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
				itemIdsStr = append(itemIdsStr, strconv.Itoa(itemId))
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
		query = query.Where("dr.reliability >= 0 AND dr.account_id = ?", accountId.Int64)
	} else {
		query = query.Where("dr.reliability = 0")
	}
	query = query.Where("dr.server = ? AND "+b.String(), server)
	if err := query.
		Group("dr.stage_id", "dpe.item_id").
		Scan(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *DropReportRepo) CalcTotalQuantityForPatternMatrix(
	ctx context.Context, server string, timeRange *models.TimeRange, stageIds []int, accountId *null.Int,
) ([]*models.TotalQuantityResultForPatternMatrix, error) {
	results := make([]*models.TotalQuantityResultForPatternMatrix, 0)
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
			stageIdsStr = append(stageIdsStr, strconv.Itoa(stageId))
		}
		fmt.Fprintf(&b, " IN (%s)", strings.Join(stageIdsStr, ","))
	}

	query := s.DB.NewSelect().
		TableExpr("drop_reports AS dr").
		Column("dr.stage_id", "dr.pattern_id").
		ColumnExpr("COUNT(*) AS total_quantity")
	if accountId.Valid {
		query = query.Where("dr.reliability >= 0 AND dr.account_id = ?", accountId.Int64)
	} else {
		query = query.Where("dr.reliability = 0")
	}
	query = query.Where("dr.server = ? AND dr.times = ? AND "+b.String(), server, 1)
	if err := query.
		Group("dr.stage_id", "dr.pattern_id").
		Scan(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *DropReportRepo) CalcTotalTimes(
	ctx context.Context, server string, timeRange *models.TimeRange, stageIds []int, accountId *null.Int, excludeNonOneTimes bool,
) ([]*models.TotalTimesResult, error) {
	results := make([]*models.TotalTimesResult, 0)
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
			stageIdsStr = append(stageIdsStr, strconv.Itoa(stageId))
		}
		fmt.Fprintf(&b, " IN (%s)", strings.Join(stageIdsStr, ","))
	}

	query := s.DB.NewSelect().
		TableExpr("drop_reports AS dr").
		Column("dr.stage_id").
		ColumnExpr("COUNT(*) AS total_times")
	if accountId.Valid {
		query = query.Where("dr.reliability >= 0 AND dr.account_id = ?", accountId.Int64)
	} else {
		query = query.Where("dr.reliability = 0")
	}
	if excludeNonOneTimes {
		query = query.Where("dr.times = 1")
	}
	query = query.Where("dr.server = ? AND "+b.String(), server)
	if err := query.
		Group("dr.stage_id").
		Scan(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *DropReportRepo) CalcTotalQuantityForTrend(
	ctx context.Context, server string, startTime *time.Time, intervalLength_hrs int, intervalNum int, stageIdItemIdMap map[int][]int, accountId *null.Int,
) ([]*models.TotalQuantityResultForTrend, error) {
	results := make([]*models.TotalQuantityResultForTrend, 0)
	if len(stageIdItemIdMap) == 0 {
		return results, nil
	}

	gameDayStart := s.getGameDateStartTime(server, *startTime)
	lastDayEnd := gameDayStart.Add(time.Hour * time.Duration(intervalLength_hrs*(intervalNum+1)))

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
	fmt.Fprintf(&subQueryExprBuilder, "(n / ?) AS group_id")
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
		Column("sub.group_id", "sub.interval_start", "sub.interval_end", "dr.stage_id", "dpe.item_id").
		ColumnExpr("SUM(dpe.quantity) AS total_quantity").
		Join("JOIN drop_pattern_elements AS dpe ON dpe.drop_pattern_id = dr.pattern_id").
		Join("RIGHT JOIN intervals AS sub").
		JoinOn("dr.created_at >= sub.interval_start AND dr.created_at < sub.interval_end")
	if accountId.Valid {
		query = query.Where("dr.reliability >= 0 AND dr.account_id = ?", accountId.Int64)
	} else {
		query = query.Where("dr.reliability = 0")
	}
	query = query.Where("dr.server = ? AND "+b.String(), server)
	if err := query.
		Group("sub.group_id", "sub.interval_start", "sub.interval_end", "dr.stage_id", "dpe.item_id").
		Scan(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *DropReportRepo) CalcTotalTimesForTrend(
	ctx context.Context, server string, startTime *time.Time, intervalLength_hrs int, intervalNum int, stageIds []int, accountId *null.Int,
) ([]*models.TotalTimesResultForTrend, error) {
	results := make([]*models.TotalTimesResultForTrend, 0)
	if len(stageIds) == 0 {
		return results, nil
	}

	gameDayStart := s.getGameDateStartTime(server, *startTime)
	lastDayEnd := gameDayStart.Add(time.Hour * time.Duration(intervalLength_hrs*(intervalNum+1)))

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
	fmt.Fprintf(&subQueryExprBuilder, "(n / ?) AS group_id")
	subQuery := s.DB.NewSelect().
		TableExpr("generate_series(?, ? * ?, ?) AS n", 0, intervalLength_hrs, intervalNum-1, intervalLength_hrs).
		ColumnExpr(subQueryExprBuilder.String(),
			gameDayStart.Unix(),
			gameDayStart.Unix(),
			intervalLength_hrs,
			intervalLength_hrs,
		)
	query := s.DB.NewSelect().
		With("intervals", subQuery).
		TableExpr("drop_reports AS dr").
		Column("sub.group_id", "sub.interval_start", "sub.interval_end", "dr.stage_id").
		ColumnExpr("SUM(dr.times) AS total_times").
		Join("RIGHT JOIN intervals AS sub").
		JoinOn("dr.created_at >= sub.interval_start AND dr.created_at < sub.interval_end")
	if accountId.Valid {
		query = query.Where("dr.reliability >= 0 AND dr.account_id = ?", accountId.Int64)
	} else {
		query = query.Where("dr.reliability = 0")
	}
	query = query.Where("dr.server = ? AND "+b.String(), server)
	if err := query.
		Group("sub.group_id", "sub.interval_start", "sub.interval_end", "dr.stage_id").
		Scan(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *DropReportRepo) getGameDateStartTime(server string, t time.Time) time.Time {
	loc := s.locMap[server]
	t = t.In(loc)
	if t.Hour() < 4 {
		t = t.AddDate(0, 0, -1)
	}
	newT := time.Date(t.Year(), t.Month(), t.Day(), 4, 0, 0, 0, loc)
	return newT
}

func (s *DropReportRepo) CalcTotalSanityCostForShimSiteStats(ctx context.Context, server string) (sanity int, err error) {
	err = pquery.New(
		s.DB.NewSelect().
			TableExpr("drop_reports AS dr").
			ColumnExpr("SUM(st.sanity * dr.times)").
			Where("dr.reliability = 0 AND dr.server = ?", server),
	).
		UseStageById("dr.stage_id").
		Q.Scan(ctx, &sanity)
	return sanity, err
}

func (s *DropReportRepo) CalcTotalStageQuantityForShimSiteStats(ctx context.Context, server string, isRecent24h bool) ([]*shims.TotalStageTime, error) {
	results := make([]*shims.TotalStageTime, 0)

	err := pquery.New(
		s.DB.NewSelect().
			TableExpr("drop_reports AS dr").
			Column("st.ark_stage_id").
			ColumnExpr("SUM(dr.times) AS total_times").
			Where("dr.reliability = 0 AND dr.server = ?", server).
			Apply(func(sq *bun.SelectQuery) *bun.SelectQuery {
				if isRecent24h {
					return sq.Where("dr.created_at >= now() - interval '24 hours'")
				} else {
					return sq
				}
			}).
			Group("st.ark_stage_id"),
	).
		UseStageById("dr.stage_id").
		Q.Scan(ctx, &results)
	if err != nil {
		return nil, err
	}
	return results, nil
}

func (s *DropReportRepo) CalcTotalItemQuantityForShimSiteStats(ctx context.Context, server string) ([]*shims.TotalItemQuantity, error) {
	results := make([]*shims.TotalItemQuantity, 0)

	err := pquery.New(
		s.DB.NewSelect().
			TableExpr("drop_reports AS dr").
			Column("it.ark_item_id").
			ColumnExpr("SUM(dpe.quantity) AS total_quantity").
			Join("JOIN drop_pattern_elements AS dpe ON dpe.drop_pattern_id = dr.pattern_id").
			Where("dr.reliability = 0 AND dr.server = ?", server).
			Group("it.ark_item_id"),
	).
		UseItemById("dpe.item_id").
		Q.Scan(ctx, &results)
	if err != nil {
		return nil, err
	}
	return results, nil
}

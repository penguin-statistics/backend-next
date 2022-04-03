package repo

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"

	"github.com/penguin-statistics/backend-next/internal/constants"
	"github.com/penguin-statistics/backend-next/internal/models"
	modelv2 "github.com/penguin-statistics/backend-next/internal/models/v2"
	"github.com/penguin-statistics/backend-next/internal/pkg/pgqry"
	"github.com/penguin-statistics/backend-next/internal/utils"
)

type DropReportRepo struct {
	DB *bun.DB
}

func NewDropReportRepo(db *bun.DB) *DropReportRepo {
	return &DropReportRepo{
		DB: db,
	}
}

func (s *DropReportRepo) CreateDropReport(ctx context.Context, tx bun.Tx, dropReport *models.DropReport) error {
	_, err := tx.NewInsert().
		Model(dropReport).
		ExcludeColumn("created_at").
		Exec(ctx)
	return err
}

func (s *DropReportRepo) DeleteDropReport(ctx context.Context, reportId int) error {
	_, err := s.DB.NewUpdate().
		Model((*models.DropReport)(nil)).
		Set("reliability = ?", -1).
		Where("report_id = ?", reportId).
		Exec(ctx)
	return err
}

func (s *DropReportRepo) CalcTotalQuantityForDropMatrix(
	ctx context.Context, server string, timeRange *models.TimeRange, stageIdItemIdMap map[int][]int, accountId null.Int,
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
	ctx context.Context, server string, timeRange *models.TimeRange, stageIds []int, accountId null.Int,
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
	ctx context.Context, server string, timeRange *models.TimeRange, stageIds []int, accountId null.Int, excludeNonOneTimes bool,
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
		ColumnExpr("SUM(dr.times) AS total_times")
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
	ctx context.Context, server string, startTime *time.Time, intervalLength time.Duration, intervalNum int, stageIdItemIdMap map[int][]int, accountId null.Int,
) ([]*models.TotalQuantityResultForTrend, error) {
	results := make([]*models.TotalQuantityResultForTrend, 0)
	if len(stageIdItemIdMap) == 0 {
		return results, nil
	}

	gameDayStart := utils.GetGameDayStartTime(server, *startTime)
	lastDayEnd := gameDayStart.Add(time.Hour * time.Duration(int(intervalLength.Hours())*(intervalNum+1)))

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
		TableExpr("generate_series(?, ? * ?, ?) AS n", 0, int(intervalLength.Hours()), intervalNum, int(intervalLength.Hours())).
		ColumnExpr(subQueryExprBuilder.String(),
			gameDayStart.Unix(),
			gameDayStart.Unix(),
			int(intervalLength.Hours()),
			int(intervalLength.Hours()),
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
	ctx context.Context, server string, startTime *time.Time, intervalLength time.Duration, intervalNum int, stageIds []int, accountId null.Int,
) ([]*models.TotalTimesResultForTrend, error) {
	results := make([]*models.TotalTimesResultForTrend, 0)
	if len(stageIds) == 0 {
		return results, nil
	}

	gameDayStart := utils.GetGameDayStartTime(server, *startTime)
	lastDayEnd := gameDayStart.Add(time.Hour * time.Duration(int(intervalLength.Hours())*(intervalNum+1)))

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
		TableExpr("generate_series(?, ? * ?, ?) AS n", 0, int(intervalLength.Hours()), intervalNum-1, int(intervalLength.Hours())).
		ColumnExpr(subQueryExprBuilder.String(),
			gameDayStart.Unix(),
			gameDayStart.Unix(),
			int(intervalLength.Hours()),
			int(intervalLength.Hours()),
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

func (s *DropReportRepo) CalcTotalSanityCostForShimSiteStats(ctx context.Context, server string) (sanity int, err error) {
	err = pgqry.New(
		s.DB.NewSelect().
			TableExpr("drop_reports AS dr").
			ColumnExpr("SUM(st.sanity * dr.times)").
			Where("dr.reliability = 0 AND dr.server = ?", server),
	).
		UseStageById("dr.stage_id").
		Q.Scan(ctx, &sanity)
	return sanity, err
}

func (s *DropReportRepo) CalcTotalStageQuantityForShimSiteStats(ctx context.Context, server string, isRecent24h bool) ([]*modelv2.TotalStageTime, error) {
	results := make([]*modelv2.TotalStageTime, 0)

	err := pgqry.New(
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

func (s *DropReportRepo) CalcTotalItemQuantityForShimSiteStats(ctx context.Context, server string) ([]*modelv2.TotalItemQuantity, error) {
	results := make([]*modelv2.TotalItemQuantity, 0)

	types := []string{constants.ItemTypeMaterial, constants.ItemTypeFurniture, constants.ItemTypeChip}
	err := pgqry.New(
		s.DB.NewSelect().
			TableExpr("drop_reports AS dr").
			Column("it.ark_item_id").
			ColumnExpr("SUM(dpe.quantity) AS total_quantity").
			Join("JOIN drop_pattern_elements AS dpe ON dpe.drop_pattern_id = dr.pattern_id").
			Where("dr.reliability = 0 AND dr.server = ? AND it.type IN (?)", server, bun.In(types)).
			Group("it.ark_item_id"),
	).
		UseItemById("dpe.item_id").
		Q.Scan(ctx, &results)
	if err != nil {
		return nil, err
	}
	return results, nil
}

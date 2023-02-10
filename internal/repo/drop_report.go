package repo

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/uptrace/bun"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
	"exusiai.dev/backend-next/internal/pkg/gameday"
	"exusiai.dev/backend-next/internal/pkg/pgqry"
	"exusiai.dev/backend-next/internal/repo/selector"
)

type DropReport struct {
	db  *bun.DB
	sel selector.S[model.DropReport]
}

func NewDropReport(db *bun.DB) *DropReport {
	return &DropReport{
		db:  db,
		sel: selector.New[model.DropReport](db),
	}
}

func (r *DropReport) CreateDropReport(ctx context.Context, tx bun.Tx, dropReport *model.DropReport) error {
	_, err := tx.NewInsert().
		Model(dropReport).
		Exec(ctx)
	return err
}

func (r *DropReport) DeleteDropReport(ctx context.Context, reportId int) error {
	_, err := r.db.NewUpdate().
		Model((*model.DropReport)(nil)).
		Set("reliability = ?", -1).
		Where("report_id = ?", reportId).
		Exec(ctx)
	return err
}

func (r *DropReport) UpdateDropReportReliability(ctx context.Context, tx bun.Tx, reportId int, reliability int) error {
	_, err := tx.NewUpdate().
		Model((*model.DropReport)(nil)).
		Set("reliability = ?", reliability).
		Where("report_id = ?", reportId).
		Exec(ctx)
	return err
}

func (r *DropReport) CalcTotalQuantityForDropMatrix(
	ctx context.Context, server string, timeRange *model.TimeRange, stageIdItemIdMap map[int][]int, accountId null.Int, sourceCategory string,
) ([]*model.TotalQuantityResultForDropMatrix, error) {
	results := make([]*model.TotalQuantityResultForDropMatrix, 0)
	if len(stageIdItemIdMap) == 0 {
		return results, nil
	}

	subq1 := r.db.NewSelect().
		TableExpr("drop_reports AS dr").
		Column("dr.stage_id", "dr.source_name", "dpe.item_id", "dpe.quantity").
		Join("JOIN drop_pattern_elements AS dpe ON dpe.drop_pattern_id = dr.pattern_id")
	r.handleAccountAndReliability(subq1, accountId)
	r.handleCreatedAtWithTimeRange(subq1, timeRange)
	r.handleServer(subq1, server)
	r.handleStagesAndItems(subq1, stageIdItemIdMap)

	mainq := r.db.NewSelect().
		TableExpr("(?) AS a", subq1).
		Column("stage_id", "item_id").
		ColumnExpr("SUM(quantity) AS total_quantity")
	r.handleSourceName(mainq, sourceCategory)

	if err := mainq.
		Group("stage_id", "item_id").
		Scan(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}

func (r *DropReport) CalcTotalQuantityForPatternMatrix(
	ctx context.Context, server string, timeRange *model.TimeRange, stageIds []int, accountId null.Int, sourceCategory string,
) ([]*model.TotalQuantityResultForPatternMatrix, error) {
	results := make([]*model.TotalQuantityResultForPatternMatrix, 0)
	if len(stageIds) == 0 {
		return results, nil
	}

	subq1 := r.db.NewSelect().
		TableExpr("drop_reports AS dr").
		Column("dr.source_name", "dr.stage_id", "dr.pattern_id")
	r.handleAccountAndReliability(subq1, accountId)
	r.handleCreatedAtWithTimeRange(subq1, timeRange)
	r.handleServer(subq1, server)
	r.handleStages(subq1, stageIds)
	r.handleTimes(subq1, 1)

	mainq := r.db.NewSelect().
		TableExpr("(?) AS a", subq1).
		Column("stage_id", "pattern_id").
		ColumnExpr("COUNT(*) AS total_quantity")
	r.handleSourceName(mainq, sourceCategory)

	if err := mainq.
		Group("stage_id", "pattern_id").
		Scan(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}

func (r *DropReport) CalcTotalTimes(
	ctx context.Context, server string, timeRange *model.TimeRange, stageIds []int, accountId null.Int, excludeNonOneTimes bool, sourceCategory string,
) ([]*model.TotalTimesResult, error) {
	results := make([]*model.TotalTimesResult, 0)
	if len(stageIds) == 0 {
		return results, nil
	}

	subq1 := r.db.NewSelect().
		TableExpr("drop_reports AS dr").
		Column("dr.source_name", "dr.stage_id", "dr.times")
	r.handleAccountAndReliability(subq1, accountId)
	if excludeNonOneTimes {
		r.handleTimes(subq1, 1)
	}
	r.handleCreatedAtWithTimeRange(subq1, timeRange)
	r.handleServer(subq1, server)
	r.handleStages(subq1, stageIds)

	mainq := r.db.NewSelect().
		TableExpr("(?) AS a", subq1).
		Column("stage_id").
		ColumnExpr("SUM(times) AS total_times")
	r.handleSourceName(mainq, sourceCategory)

	if err := mainq.
		Group("stage_id").
		Scan(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}

func (r *DropReport) CalcQuantityUniqCount(
	ctx context.Context, server string, timeRange *model.TimeRange, stageIdItemIdMap map[int][]int, accountId null.Int, sourceCategory string,
) ([]*model.QuantityUniqCountResultForDropMatrix, error) {
	results := make([]*model.QuantityUniqCountResultForDropMatrix, 0)
	if len(stageIdItemIdMap) == 0 {
		return results, nil
	}

	subq1 := r.db.NewSelect().
		TableExpr("drop_reports AS dr").
		Column("dr.source_name", "dr.stage_id", "dpe.item_id", "dpe.quantity").
		Join("JOIN drop_pattern_elements AS dpe ON dpe.drop_pattern_id = dr.pattern_id")
	r.handleAccountAndReliability(subq1, accountId)
	r.handleCreatedAtWithTimeRange(subq1, timeRange)
	r.handleServer(subq1, server)
	r.handleStagesAndItems(subq1, stageIdItemIdMap)

	mainq := r.db.NewSelect().
		TableExpr("(?) AS a", subq1).
		Column("stage_id", "item_id", "quantity").
		ColumnExpr("COUNT(*) AS count")
	r.handleSourceName(mainq, sourceCategory)

	if err := mainq.
		Group("stage_id", "item_id", "quantity").
		Scan(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}

func (r *DropReport) CalcTotalQuantityForTrend(
	ctx context.Context, server string, startTime *time.Time, intervalLength time.Duration, intervalNum int, stageIdItemIdMap map[int][]int, accountId null.Int, sourceCategory string,
) ([]*model.TotalQuantityResultForTrend, error) {
	results := make([]*model.TotalQuantityResultForTrend, 0)
	if len(stageIdItemIdMap) == 0 {
		return results, nil
	}

	gameDayStart := gameday.StartTime(server, *startTime)
	lastDayEnd := gameDayStart.Add(time.Hour * time.Duration(int(intervalLength.Hours())*(intervalNum+1)))

	subq1 := r.db.NewSelect().
		With("intervals", r.genSubQueryForTrendSegments(gameDayStart, intervalLength, intervalNum)).
		TableExpr("drop_reports AS dr").
		Column("dr.source_name", "sub.group_id", "sub.interval_start", "sub.interval_end", "dr.stage_id", "dpe.item_id", "dpe.quantity").
		Join("JOIN drop_pattern_elements AS dpe ON dpe.drop_pattern_id = dr.pattern_id").
		Join("RIGHT JOIN intervals AS sub").
		JoinOn("dr.created_at >= sub.interval_start AND dr.created_at < sub.interval_end")
	r.handleAccountAndReliability(subq1, accountId)
	r.handleCreatedAtWithTime(subq1, gameDayStart, lastDayEnd)
	r.handleServer(subq1, server)
	r.handleStagesAndItems(subq1, stageIdItemIdMap)

	mainq := r.db.NewSelect().
		TableExpr("(?) AS a", subq1).
		Column("group_id", "interval_start", "interval_end", "stage_id", "item_id").
		ColumnExpr("SUM(quantity) AS total_quantity")
	r.handleSourceName(mainq, sourceCategory)

	if err := mainq.
		Group("group_id", "interval_start", "interval_end", "stage_id", "item_id").
		Scan(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}

func (r *DropReport) CalcTotalTimesForTrend(
	ctx context.Context, server string, startTime *time.Time, intervalLength time.Duration, intervalNum int, stageIds []int, accountId null.Int, sourceCategory string,
) ([]*model.TotalTimesResultForTrend, error) {
	results := make([]*model.TotalTimesResultForTrend, 0)
	if len(stageIds) == 0 {
		return results, nil
	}

	gameDayStart := gameday.StartTime(server, *startTime)
	lastDayEnd := gameDayStart.Add(time.Hour * time.Duration(int(intervalLength.Hours())*(intervalNum+1)))

	subq1 := r.db.NewSelect().
		With("intervals", r.genSubQueryForTrendSegments(gameDayStart, intervalLength, intervalNum)).
		TableExpr("drop_reports AS dr").
		Column("dr.source_name", "sub.group_id", "sub.interval_start", "sub.interval_end", "dr.stage_id", "dr.times").
		Join("RIGHT JOIN intervals AS sub").
		JoinOn("dr.created_at >= sub.interval_start AND dr.created_at < sub.interval_end")
	r.handleAccountAndReliability(subq1, accountId)
	r.handleCreatedAtWithTime(subq1, gameDayStart, lastDayEnd)
	r.handleServer(subq1, server)
	r.handleStages(subq1, stageIds)

	mainq := r.db.NewSelect().
		TableExpr("(?) AS a", subq1).
		Column("group_id", "interval_start", "interval_end", "stage_id").
		ColumnExpr("SUM(times) AS total_times")
	r.handleSourceName(mainq, sourceCategory)

	if err := mainq.
		Group("group_id", "interval_start", "interval_end", "stage_id").
		Scan(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}

func (r *DropReport) CalcTotalSanityCostForShimSiteStats(ctx context.Context, server string) (sanity int, err error) {
	err = pgqry.New(
		r.db.NewSelect().
			TableExpr("drop_reports AS dr").
			ColumnExpr("SUM(st.sanity * dr.times)").
			Where("dr.reliability = 0 AND dr.server = ?", server),
	).
		UseStageById("dr.stage_id").
		Q.Scan(ctx, &sanity)
	return sanity, err
}

func (r *DropReport) CalcTotalStageQuantityForShimSiteStats(ctx context.Context, server string, isRecent24h bool) ([]*modelv2.TotalStageTime, error) {
	results := make([]*modelv2.TotalStageTime, 0)

	err := pgqry.New(
		r.db.NewSelect().
			TableExpr("drop_reports AS dr").
			Column("st.ark_stage_id").
			ColumnExpr("SUM(dr.times) AS total_times").
			Where("dr.reliability = 0 AND dr.server = ? AND st.ark_stage_id != ?", server, constant.RecruitStageID).
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

func (r *DropReport) CalcTotalItemQuantityForShimSiteStats(ctx context.Context, server string) ([]*modelv2.TotalItemQuantity, error) {
	results := make([]*modelv2.TotalItemQuantity, 0)

	types := []string{constant.ItemTypeMaterial, constant.ItemTypeFurniture, constant.ItemTypeChip}
	err := pgqry.New(
		r.db.NewSelect().
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

func (r *DropReport) CalcRecentUniqueUserCountBySource(ctx context.Context, duration time.Duration) ([]*modelv2.UniqueUserCountBySource, error) {
	results := make([]*modelv2.UniqueUserCountBySource, 0)
	subq := r.db.NewSelect().
		TableExpr("drop_reports AS dr").
		Column("dr.source_name", "dr.account_id")
	r.handleCreatedAtWithTime(subq, time.Now().Add(-duration), time.Now())
	subq = subq.Group("dr.source_name", "dr.account_id")

	mainq := r.db.NewSelect().
		TableExpr("(?) AS a", subq).
		Column("source_name").
		ColumnExpr("COUNT(*) AS count").
		Group("source_name")

	if err := mainq.
		Scan(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}

func (r *DropReport) handleStagesAndItems(query *bun.SelectQuery, stageIdItemIdMap map[int][]int) {
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
	query.Where(strings.Join(stageConditions, " OR "))
}

func (r *DropReport) handleStages(query *bun.SelectQuery, stageIds []int) {
	var b strings.Builder
	b.WriteString("dr.stage_id")
	if len(stageIds) == 1 {
		fmt.Fprintf(&b, "= %d", stageIds[0])
	} else {
		var stageIdsStr []string
		for _, stageId := range stageIds {
			stageIdsStr = append(stageIdsStr, strconv.Itoa(stageId))
		}
		fmt.Fprintf(&b, " IN (%s)", strings.Join(stageIdsStr, ","))
	}
	query.Where(b.String())
}

func (r *DropReport) handleAccountAndReliability(query *bun.SelectQuery, accountId null.Int) {
	if accountId.Valid {
		query = query.Where("dr.reliability >= 0 AND dr.account_id = ?", accountId.Int64)
	} else {
		query = query.Where("dr.reliability = 0")
	}
}

func (r *DropReport) handleCreatedAtWithTimeRange(query *bun.SelectQuery, timeRange *model.TimeRange) {
	if timeRange.StartTime != nil {
		query = query.Where("dr.created_at >= timestamp with time zone ?", timeRange.StartTime.Format(time.RFC3339))
	}
	if timeRange.EndTime != nil {
		query = query.Where("dr.created_at < timestamp with time zone ?", timeRange.EndTime.Format(time.RFC3339))
	}
}

func (r *DropReport) handleCreatedAtWithTime(query *bun.SelectQuery, start time.Time, end time.Time) {
	query = query.Where("dr.created_at >= to_timestamp(?)", start.Unix())
	query = query.Where("dr.created_at < to_timestamp(?)", end.Unix())
}

func (r *DropReport) handleServer(query *bun.SelectQuery, server string) {
	query = query.Where("dr.server = ?", server)
}

func (r *DropReport) handleTimes(query *bun.SelectQuery, times int) {
	query = query.Where("dr.times = ?", times)
}

func (r *DropReport) handleSourceName(query *bun.SelectQuery, sourceCategory string) {
	if sourceCategory == constant.SourceCategoryManual {
		query = query.Where("source_name IN (?)", bun.In(constant.ManualSources))
	} else if sourceCategory == constant.SourceCategoryAutomated {
		query = query.Where("source_name NOT IN (?)", bun.In(constant.ManualSources))
	}
}

func (r *DropReport) genSubQueryForTrendSegments(gameDayStart time.Time, intervalLength time.Duration, intervalNum int) *bun.SelectQuery {
	var subQueryExprBuilder strings.Builder
	fmt.Fprintf(&subQueryExprBuilder, "to_timestamp(?) + (n || ' hours')::interval AS interval_start, ")
	fmt.Fprintf(&subQueryExprBuilder, "to_timestamp(?) + ((n + ?) || ' hours')::interval AS interval_end, ")
	fmt.Fprintf(&subQueryExprBuilder, "(n / ?) AS group_id")
	return r.db.NewSelect().
		TableExpr("generate_series(?, ? * ?, ?) AS n", 0, int(intervalLength.Hours()), intervalNum, int(intervalLength.Hours())).
		ColumnExpr(subQueryExprBuilder.String(),
			gameDayStart.Unix(),
			gameDayStart.Unix(),
			int(intervalLength.Hours()),
			int(intervalLength.Hours()),
		)
}

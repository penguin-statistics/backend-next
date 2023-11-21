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
	"exusiai.dev/backend-next/internal/util"
)

type DropReport struct {
	DB *bun.DB
}

func NewDropReport(db *bun.DB) *DropReport {
	return &DropReport{
		DB: db,
	}
}

func (s *DropReport) CreateDropReport(ctx context.Context, tx bun.Tx, dropReport *model.DropReport) error {
	_, err := tx.NewInsert().
		Model(dropReport).
		Exec(ctx)
	return err
}

func (s *DropReport) DeleteDropReport(ctx context.Context, reportId int) error {
	_, err := s.DB.NewUpdate().
		Model((*model.DropReport)(nil)).
		Set("reliability = ?", -1).
		Where("report_id = ?", reportId).
		Exec(ctx)
	return err
}

func (s *DropReport) UpdateDropReportReliability(ctx context.Context, tx bun.Tx, reportId int, reliability int) error {
	_, err := tx.NewUpdate().
		Model((*model.DropReport)(nil)).
		Set("reliability = ?", reliability).
		Where("report_id = ?", reportId).
		Exec(ctx)
	return err
}

// only filtered by stage_id, not item_id, needs post-filtering
func (s *DropReport) CalcQuantityUniqCount(
	ctx context.Context, queryCtx *model.DropReportQueryContext,
) ([]*model.QuantityUniqCountResultForDropMatrix, error) {
	results := make([]*model.QuantityUniqCountResultForDropMatrix, 0)

	subq1 := s.DB.NewSelect().
		TableExpr("drop_reports AS dr").
		Column("dr.stage_id", "dpe.item_id", "dpe.quantity").
		Join("JOIN drop_pattern_elements AS dpe ON dpe.drop_pattern_id = dr.pattern_id")
	if queryCtx.SourceCategory != constant.SourceCategoryAll {
		subq1 = subq1.Column("dr.source_name")
	}
	s.handleAccountAndReliability(subq1, queryCtx.AccountID)
	if queryCtx.ExcludeNonOneTimes {
		s.handleTimes(subq1, 1)
	}
	s.handleCreatedAtWithTime(subq1, queryCtx.StartTime, queryCtx.EndTime)
	s.handleServer(subq1, queryCtx.Server)
	s.handleStages(subq1, queryCtx.GetStageIds())

	mainq := s.DB.NewSelect().
		TableExpr("(?) AS a", subq1).
		Column("stage_id", "item_id", "quantity").
		ColumnExpr("COUNT(*) AS count")
	s.handleSourceName(mainq, queryCtx.SourceCategory)

	if err := mainq.
		Group("stage_id", "item_id", "quantity").
		Scan(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *DropReport) CalcTotalTimes(
	ctx context.Context, queryCtx *model.DropReportQueryContext,
) ([]*model.TotalTimesResult, error) {
	results := make([]*model.TotalTimesResult, 0)

	subq1 := s.DB.NewSelect().
		TableExpr("drop_reports AS dr").
		Column("dr.source_name", "dr.stage_id", "dr.times")
	s.handleAccountAndReliability(subq1, queryCtx.AccountID)
	if queryCtx.ExcludeNonOneTimes {
		s.handleTimes(subq1, 1)
	}
	s.handleCreatedAtWithTime(subq1, queryCtx.StartTime, queryCtx.EndTime)
	s.handleServer(subq1, queryCtx.Server)
	stageIds := queryCtx.GetStageIds()
	if len(stageIds) > 0 {
		s.handleStages(subq1, stageIds)
	}

	mainq := s.DB.NewSelect().
		TableExpr("(?) AS a", subq1).
		Column("stage_id").
		ColumnExpr("SUM(times) AS total_times")
	s.handleSourceName(mainq, queryCtx.SourceCategory)

	if err := mainq.
		Group("stage_id").
		Scan(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *DropReport) CalcTotalQuantityForPatternMatrix(
	ctx context.Context, queryCtx *model.DropReportQueryContext,
) ([]*model.TotalQuantityResultForPatternMatrix, error) {
	results := make([]*model.TotalQuantityResultForPatternMatrix, 0)

	subq1 := s.DB.NewSelect().
		TableExpr("drop_reports AS dr").
		Column("dr.source_name", "dr.stage_id", "dr.pattern_id")
	s.handleAccountAndReliability(subq1, queryCtx.AccountID)
	if queryCtx.ExcludeNonOneTimes {
		s.handleTimes(subq1, 1)
	}
	s.handleCreatedAtWithTime(subq1, queryCtx.StartTime, queryCtx.EndTime)
	s.handleServer(subq1, queryCtx.Server)
	s.handleStages(subq1, queryCtx.GetStageIds())
	s.handleTimes(subq1, 1)

	mainq := s.DB.NewSelect().
		TableExpr("(?) AS a", subq1).
		Column("stage_id", "pattern_id").
		ColumnExpr("COUNT(*) AS total_quantity")
	s.handleSourceName(mainq, queryCtx.SourceCategory)

	if err := mainq.
		Group("stage_id", "pattern_id").
		Scan(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *DropReport) CalcTotalQuantityForTrend(
	ctx context.Context, server string, startTime *time.Time, intervalLength time.Duration, intervalNum int, stageIdItemIdMap map[int][]int, accountId null.Int, sourceCategory string,
) ([]*model.TotalQuantityResultForTrend, error) {
	results := make([]*model.TotalQuantityResultForTrend, 0)
	if len(stageIdItemIdMap) == 0 {
		return results, nil
	}

	gameDayStart := gameday.StartTime(server, *startTime)
	lastDayEnd := gameDayStart.Add(time.Hour * time.Duration(int(intervalLength.Hours())*(intervalNum+1)))

	subq1 := s.DB.NewSelect().
		With("intervals", s.genSubQueryForTrendSegments(gameDayStart, intervalLength, intervalNum)).
		TableExpr("drop_reports AS dr").
		Column("dr.source_name", "sub.group_id", "sub.interval_start", "sub.interval_end", "dr.stage_id", "dpe.item_id", "dpe.quantity").
		Join("JOIN drop_pattern_elements AS dpe ON dpe.drop_pattern_id = dr.pattern_id").
		Join("RIGHT JOIN intervals AS sub").
		JoinOn("dr.created_at >= sub.interval_start AND dr.created_at < sub.interval_end")
	s.handleAccountAndReliability(subq1, accountId)
	s.handleCreatedAtWithTime(subq1, &gameDayStart, &lastDayEnd)
	s.handleServer(subq1, server)
	s.handleStagesAndItems(subq1, stageIdItemIdMap)

	mainq := s.DB.NewSelect().
		TableExpr("(?) AS a", subq1).
		Column("group_id", "interval_start", "interval_end", "stage_id", "item_id").
		ColumnExpr("SUM(quantity) AS total_quantity")
	s.handleSourceName(mainq, sourceCategory)

	if err := mainq.
		Group("group_id", "interval_start", "interval_end", "stage_id", "item_id").
		Scan(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *DropReport) CalcTotalTimesForTrend(
	ctx context.Context, server string, startTime *time.Time, intervalLength time.Duration, intervalNum int, stageIds []int, accountId null.Int, sourceCategory string,
) ([]*model.TotalTimesResultForTrend, error) {
	results := make([]*model.TotalTimesResultForTrend, 0)
	if len(stageIds) == 0 {
		return results, nil
	}

	gameDayStart := gameday.StartTime(server, *startTime)
	lastDayEnd := gameDayStart.Add(time.Hour * time.Duration(int(intervalLength.Hours())*(intervalNum+1)))

	subq1 := s.DB.NewSelect().
		With("intervals", s.genSubQueryForTrendSegments(gameDayStart, intervalLength, intervalNum)).
		TableExpr("drop_reports AS dr").
		Column("dr.source_name", "sub.group_id", "sub.interval_start", "sub.interval_end", "dr.stage_id", "dr.times").
		Join("RIGHT JOIN intervals AS sub").
		JoinOn("dr.created_at >= sub.interval_start AND dr.created_at < sub.interval_end")
	s.handleAccountAndReliability(subq1, accountId)
	s.handleCreatedAtWithTime(subq1, &gameDayStart, &lastDayEnd)
	s.handleServer(subq1, server)
	s.handleStages(subq1, stageIds)

	mainq := s.DB.NewSelect().
		TableExpr("(?) AS a", subq1).
		Column("group_id", "interval_start", "interval_end", "stage_id").
		ColumnExpr("SUM(times) AS total_times")
	s.handleSourceName(mainq, sourceCategory)

	if err := mainq.
		Group("group_id", "interval_start", "interval_end", "stage_id").
		Scan(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *DropReport) CalcTotalStageQuantityForShimSiteStats(ctx context.Context, server string, isRecent24h bool) ([]*modelv2.TotalStageTime, error) {
	results := make([]*modelv2.TotalStageTime, 0)

	err := pgqry.New(
		s.DB.NewSelect().
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

func (s *DropReport) CalcRecentUniqueUserCountBySource(ctx context.Context, duration time.Duration) ([]*modelv2.UniqueUserCountBySource, error) {
	results := make([]*modelv2.UniqueUserCountBySource, 0)
	subq := s.DB.NewSelect().
		TableExpr("drop_reports AS dr").
		Column("dr.source_name", "dr.account_id")
	start := time.Now().Add(-duration)
	end := time.Now()
	s.handleCreatedAtWithTime(subq, &start, &end)
	subq = subq.Group("dr.source_name", "dr.account_id")

	mainq := s.DB.NewSelect().
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

/**
 * Only return drop reports under one stage.
 */
func (s *DropReport) GetDropReports(ctx context.Context, queryCtx *model.DropReportQueryContext) ([]*model.DropReport, error) {
	results := make([]*model.DropReport, 0)
	query := s.DB.NewSelect().
		TableExpr("drop_reports AS dr").
		Column("pattern_id", "created_at", "account_id", "source_name", "version", "times").
		Order("created_at")
	s.handleServer(query, queryCtx.Server)
	s.handleCreatedAtWithTime(query, queryCtx.StartTime, queryCtx.EndTime)

	if queryCtx.HasItemIds() {
		query = query.Join("JOIN drop_pattern_elements AS dpe ON dpe.drop_pattern_id = dr.pattern_id")
		s.handleStagesAndItems(query, *queryCtx.StageItemFilter)
	} else {
		s.handleStages(query, queryCtx.GetStageIds())
	}

	s.handleAccountAndReliability(query, queryCtx.AccountID)
	s.handleSourceName(query, queryCtx.SourceCategory)

	if queryCtx.ExcludeNonOneTimes {
		s.handleTimes(query, 1)
	} else if queryCtx.Times.Valid {
		s.handleTimes(query, int(queryCtx.Times.Int64))
	}

	if err := query.
		Scan(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *DropReport) GetDropReportsForArchive(ctx context.Context, cursor *model.Cursor, date time.Time, limit int) ([]*model.DropReport, model.Cursor, error) {
	start := time.UnixMilli(util.GetDayStartTime(&date, "CN")) // we use CN server's day start time across all servers for archive
	end := start.Add(time.Hour * 24)
	results := make([]*model.DropReport, 0)
	query := s.DB.NewSelect().
		Model(&results).
		Where("created_at >= to_timestamp(?)", start.Unix()).
		Where("created_at < to_timestamp(?)", end.Unix()).
		Order("report_id").
		Limit(limit)
	if cursor != nil && cursor.Start > 0 {
		query = query.Where("report_id > ?", cursor.Start)
	}
	if err := query.
		Scan(ctx); err != nil {
		return nil, model.Cursor{}, err
	}
	return results, newCursor(results), nil
}

func (s *DropReport) handleStagesAndItems(query *bun.SelectQuery, stageIdItemIdMap map[int][]int) {
	stageConditions := make([]string, 0)
	for stageId, itemIds := range stageIdItemIdMap {
		var stageB strings.Builder
		fmt.Fprintf(&stageB, "dr.stage_id = %d", stageId)
		if len(itemIds) == 1 {
			fmt.Fprintf(&stageB, " AND dpe.item_id = %d", itemIds[0])
		} else if len(itemIds) > 1 {
			var itemIdsStr []string
			for _, itemId := range itemIds {
				itemIdsStr = append(itemIdsStr, strconv.Itoa(itemId))
			}
			fmt.Fprintf(&stageB, " AND dpe.item_id IN (%s)", strings.Join(itemIdsStr, ","))
		}
		stageConditions = append(stageConditions, stageB.String())
	}
	if len(stageConditions) > 0 {
		query.Where(strings.Join(stageConditions, " OR "))
	}
}

func (s *DropReport) handleStages(query *bun.SelectQuery, stageIds []int) {
	if len(stageIds) == 0 {
		return
	}
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

func (s *DropReport) handleAccountAndReliability(query *bun.SelectQuery, accountId null.Int) {
	if accountId.Valid {
		query = query.Where("dr.reliability >= 0 AND dr.account_id = ?", accountId.Int64)
	} else {
		query = query.Where("dr.reliability = 0")
	}
}

func (s *DropReport) handleCreatedAtWithTimeRange(query *bun.SelectQuery, timeRange *model.TimeRange) {
	if timeRange.StartTime != nil {
		query = query.Where("dr.created_at >= timestamp with time zone ?", timeRange.StartTime.Format(time.RFC3339))
	}
	if timeRange.EndTime != nil {
		query = query.Where("dr.created_at < timestamp with time zone ?", timeRange.EndTime.Format(time.RFC3339))
	}
}

func (s *DropReport) handleCreatedAtWithTime(query *bun.SelectQuery, start *time.Time, end *time.Time) {
	if start != nil {
		query = query.Where("dr.created_at >= to_timestamp(?)", start.Unix())
	}
	if end != nil {
		query = query.Where("dr.created_at < to_timestamp(?)", end.Unix())
	}
}

func (s *DropReport) handleServer(query *bun.SelectQuery, server string) {
	query = query.Where("dr.server = ?", server)
}

func (s *DropReport) handleTimes(query *bun.SelectQuery, times int) {
	query = query.Where("dr.times = ?", times)
}

func (s *DropReport) handleSourceName(query *bun.SelectQuery, sourceCategory string) {
	if sourceCategory == constant.SourceCategoryManual {
		query = query.Where("source_name IN (?)", bun.In(constant.ManualSources))
	} else if sourceCategory == constant.SourceCategoryAutomated {
		query = query.WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
			return q.Where("source_name NOT IN (?)", bun.In(constant.ManualSources)).WhereOr("source_name IS NULL")
		})
	}
}

func (s *DropReport) genSubQueryForTrendSegments(gameDayStart time.Time, intervalLength time.Duration, intervalNum int) *bun.SelectQuery {
	var subQueryExprBuilder strings.Builder
	fmt.Fprintf(&subQueryExprBuilder, "to_timestamp(?) + (n || ' hours')::interval AS interval_start, ")
	fmt.Fprintf(&subQueryExprBuilder, "to_timestamp(?) + ((n + ?) || ' hours')::interval AS interval_end, ")
	fmt.Fprintf(&subQueryExprBuilder, "(n / ?) AS group_id")
	return s.DB.NewSelect().
		TableExpr("generate_series(?, ? * ?, ?) AS n", 0, int(intervalLength.Hours()), intervalNum, int(intervalLength.Hours())).
		ColumnExpr(subQueryExprBuilder.String(),
			gameDayStart.Unix(),
			gameDayStart.Unix(),
			int(intervalLength.Hours()),
			int(intervalLength.Hours()),
		)
}

func newCursor(reports []*model.DropReport) model.Cursor {
	if len(reports) == 0 {
		return model.Cursor{}
	}
	return model.Cursor{
		Start: reports[0].ReportID,
		End:   reports[len(reports)-1].ReportID,
	}
}

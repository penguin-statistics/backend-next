package repo

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
	"exusiai.dev/backend-next/internal/pkg/gameday"
	"exusiai.dev/backend-next/internal/pkg/pgqry"
	"exusiai.dev/backend-next/internal/repo/selector"
	"exusiai.dev/backend-next/internal/util"
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

// DeleteDropReport marks the report as recalled (reliability -1) and returns the number of rows affected: 0 when the
// report does not exist.
func (r *DropReport) DeleteDropReport(ctx context.Context, reportId int) (int64, error) {
	res, err := r.db.NewUpdate().
		Model((*model.DropReport)(nil)).
		Set("reliability = ?", -1).
		Where("report_id = ?", reportId).
		Exec(ctx)
	if err != nil {
		return -1, err
	}
	return res.RowsAffected()
}

func (r *DropReport) UpdateDropReportReliability(ctx context.Context, tx bun.Tx, reportId int, reliability int) error {
	_, err := tx.NewUpdate().
		Model((*model.DropReport)(nil)).
		Set("reliability = ?", reliability).
		Where("report_id = ?", reportId).
		Exec(ctx)
	return err
}

// only filtered by stage_id, not item_id, needs post-filtering
func (r *DropReport) CalcQuantityUniqCount(
	ctx context.Context, queryCtx *model.DropReportQueryContext,
) ([]*model.QuantityUniqCountResultForDropMatrix, error) {
	results := make([]*model.QuantityUniqCountResultForDropMatrix, 0)

	subq1 := r.db.NewSelect().
		TableExpr("drop_reports AS dr").
		Column("dr.stage_id", "dpe.item_id", "dpe.quantity").
		Join("JOIN drop_pattern_elements AS dpe ON dpe.drop_pattern_id = dr.pattern_id")
	if queryCtx.SourceCategory != constant.SourceCategoryAll {
		subq1 = subq1.Column("dr.source_name")
	}
	r.handleAccountAndReliability(subq1, queryCtx.AccountID)
	if queryCtx.ExcludeNonOneTimes {
		r.handleTimes(subq1, 1)
	}
	r.handleCreatedAtWithTime(subq1, queryCtx.StartTime, queryCtx.EndTime)
	r.handleServer(subq1, queryCtx.Server)
	r.handleStages(subq1, queryCtx.GetStageIds())

	mainq := r.db.NewSelect().
		TableExpr("(?) AS a", subq1).
		Column("stage_id", "item_id", "quantity").
		ColumnExpr("COUNT(*) AS count")
	r.handleSourceName(mainq, queryCtx.SourceCategory)

	if err := mainq.
		Group("stage_id", "item_id", "quantity").
		Scan(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}

func (r *DropReport) CalcTotalTimes(
	ctx context.Context, queryCtx *model.DropReportQueryContext,
) ([]*model.TotalTimesResult, error) {
	results := make([]*model.TotalTimesResult, 0)

	subq1 := r.db.NewSelect().
		TableExpr("drop_reports AS dr").
		Column("dr.source_name", "dr.stage_id", "dr.times")
	r.handleAccountAndReliability(subq1, queryCtx.AccountID)
	if queryCtx.ExcludeNonOneTimes {
		r.handleTimes(subq1, 1)
	}
	r.handleCreatedAtWithTime(subq1, queryCtx.StartTime, queryCtx.EndTime)
	r.handleServer(subq1, queryCtx.Server)
	stageIds := queryCtx.GetStageIds()
	if len(stageIds) > 0 {
		r.handleStages(subq1, stageIds)
	}

	mainq := r.db.NewSelect().
		TableExpr("(?) AS a", subq1).
		Column("stage_id").
		ColumnExpr("SUM(times) AS total_times")
	r.handleSourceName(mainq, queryCtx.SourceCategory)

	if err := mainq.
		Group("stage_id").
		Scan(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}

// CalcDropMatrixForIntervalsBySourceCategories computes what CalcTotalTimes and CalcQuantityUniqCount compute, for
// every query context (time interval and stages) and several source categories at once, with a single scan of
// drop_reports. Reports are counted per pattern (and classified as manual or automated sources) and per segment
// between consecutive interval boundaries; an interval's aggregations add up the segments it covers. All query
// contexts must share server, account and times filter; their SourceCategory is ignored. Results are returned in
// the order of queryCtxs.
func (r *DropReport) CalcDropMatrixForIntervalsBySourceCategories(
	ctx context.Context, queryCtxs []*model.DropReportQueryContext, sourceCategories []string,
) ([]map[string][]*model.TotalTimesResult, []map[string][]*model.QuantityUniqCountResultForDropMatrix, error) {
	if len(queryCtxs) == 0 {
		return nil, nil, nil
	}

	// interval boundaries in seconds, as handleCreatedAtWithTime compares them
	boundarySet := make(map[int64]struct{})
	stageIdSet := make(map[int]struct{})
	// an interval without stage filter needs every stage
	allStages := false
	for _, queryCtx := range queryCtxs {
		boundarySet[queryCtx.StartTime.Unix()] = struct{}{}
		boundarySet[queryCtx.EndTime.Unix()] = struct{}{}
		intervalStageIds := queryCtx.GetStageIds()
		if len(intervalStageIds) == 0 {
			allStages = true
		}
		for _, stageId := range intervalStageIds {
			stageIdSet[stageId] = struct{}{}
		}
	}
	boundaries := make([]int64, 0, len(boundarySet))
	for boundary := range boundarySet {
		boundaries = append(boundaries, boundary)
	}
	sort.Slice(boundaries, func(i, j int) bool { return boundaries[i] < boundaries[j] })
	thresholds := make([]time.Time, len(boundaries))
	segmentByBoundary := make(map[int64]int, len(boundaries))
	for i, boundary := range boundaries {
		thresholds[i] = time.Unix(boundary, 0)
		// width_bucket numbers the segment starting at thresholds[i] as i+1
		segmentByBoundary[boundary] = i + 1
	}
	stageIds := make([]int, 0, len(stageIdSet))
	if !allStages {
		for stageId := range stageIdSet {
			stageIds = append(stageIds, stageId)
		}
	}
	first, last := thresholds[0], thresholds[len(thresholds)-1]

	var rows []struct {
		Kind     string `bun:"kind"`
		StageID  int    `bun:"stage_id"`
		ItemID   int    `bun:"item_id"`
		Quantity int    `bun:"quantity"`
		IsManual bool   `bun:"is_manual"`
		Segment  int    `bun:"segment"`
		Value    int    `bun:"value"`
	}

	// There are far fewer distinct patterns than reports (CN: ~100k vs ~1.8M a day), so patterns are only
	// expanded into their elements after counting.
	perPattern := r.db.NewSelect().
		TableExpr("drop_reports AS dr").
		Column("dr.stage_id", "dr.pattern_id").
		ColumnExpr(isManualSourceExpr, bun.In(constant.ManualSources)).
		ColumnExpr("width_bucket(dr.created_at, ?::timestamptz[]) AS segment", pgdialect.Array(thresholds)).
		ColumnExpr("COUNT(*) AS count").
		ColumnExpr("SUM(dr.times) AS times")
	r.handleAccountAndReliability(perPattern, queryCtxs[0].AccountID)
	if queryCtxs[0].ExcludeNonOneTimes {
		r.handleTimes(perPattern, 1)
	}
	r.handleCreatedAtWithTime(perPattern, &first, &last)
	r.handleServer(perPattern, queryCtxs[0].Server)
	r.handleStages(perPattern, stageIds)
	perPattern = perPattern.Group("dr.stage_id", "dr.pattern_id", "is_manual", "segment")

	if err := r.db.NewRaw(`WITH a AS MATERIALIZED (?)
		SELECT 'quantity' AS kind, a.stage_id, dpe.item_id, dpe.quantity, a.is_manual, a.segment, SUM(a.count) AS value
		FROM a JOIN drop_pattern_elements AS dpe ON dpe.drop_pattern_id = a.pattern_id
		GROUP BY a.stage_id, dpe.item_id, dpe.quantity, a.is_manual, a.segment
		UNION ALL
		SELECT 'times' AS kind, a.stage_id, 0, 0, a.is_manual, a.segment, SUM(a.times) AS value
		FROM a
		GROUP BY a.stage_id, a.is_manual, a.segment`, perPattern).
		Scan(ctx, &rows); err != nil {
		return nil, nil, err
	}

	timesResults := make([]map[string][]*model.TotalTimesResult, len(queryCtxs))
	quantityUniqCountResults := make([]map[string][]*model.QuantityUniqCountResultForDropMatrix, len(queryCtxs))
	for i, queryCtx := range queryCtxs {
		fromSegment := segmentByBoundary[queryCtx.StartTime.Unix()]
		toSegment := segmentByBoundary[queryCtx.EndTime.Unix()]
		intervalStageIds := queryCtx.GetStageIds()
		intervalStageIdSet := make(map[int]struct{}, len(intervalStageIds))
		for _, stageId := range intervalStageIds {
			intervalStageIdSet[stageId] = struct{}{}
		}

		timesRows := make([]dropMatrixTimesRow, 0)
		quantityRows := make([]dropMatrixQuantityRow, 0)
		for _, row := range rows {
			if row.Segment < fromSegment || row.Segment >= toSegment {
				continue
			}
			if _, ok := intervalStageIdSet[row.StageID]; len(intervalStageIds) > 0 && !ok {
				continue
			}
			if row.Kind == "times" {
				timesRows = append(timesRows, dropMatrixTimesRow{row.StageID, row.IsManual, row.Value})
			} else {
				quantityRows = append(quantityRows, dropMatrixQuantityRow{row.StageID, row.ItemID, row.Quantity, row.IsManual, row.Value})
			}
		}
		timesResults[i], quantityUniqCountResults[i] = splitDropMatrixRowsBySourceCategories(timesRows, quantityRows, sourceCategories)
	}
	return timesResults, quantityUniqCountResults, nil
}

type dropMatrixTimesRow struct {
	StageID    int
	IsManual   bool
	TotalTimes int
}

type dropMatrixQuantityRow struct {
	StageID  int
	ItemID   int
	Quantity int
	IsManual bool
	Count    int
}

// splitDropMatrixRowsBySourceCategories sums classified drop matrix rows up per source category. Rows of the same
// stage (and item and quantity) are added up, e.g. those of several segments.
func splitDropMatrixRowsBySourceCategories(
	timesRows []dropMatrixTimesRow, quantityRows []dropMatrixQuantityRow, sourceCategories []string,
) (map[string][]*model.TotalTimesResult, map[string][]*model.QuantityUniqCountResultForDropMatrix) {
	timesBySourceCategory := make(map[string][]*model.TotalTimesResult, len(sourceCategories))
	for _, sourceCategory := range sourceCategories {
		times := make(map[int]int)
		stageIdsInOrder := make([]int, 0)
		for _, row := range timesRows {
			if !inSourceCategory(sourceCategory, row.IsManual) {
				continue
			}
			if _, ok := times[row.StageID]; !ok {
				stageIdsInOrder = append(stageIdsInOrder, row.StageID)
			}
			times[row.StageID] += row.TotalTimes
		}
		results := make([]*model.TotalTimesResult, 0, len(stageIdsInOrder))
		for _, stageId := range stageIdsInOrder {
			results = append(results, &model.TotalTimesResult{StageID: stageId, TotalTimes: times[stageId]})
		}
		timesBySourceCategory[sourceCategory] = results
	}

	type key struct{ stageId, itemId, quantity int }
	quantityUniqCountsBySourceCategory := make(map[string][]*model.QuantityUniqCountResultForDropMatrix, len(sourceCategories))
	for _, sourceCategory := range sourceCategories {
		counts := make(map[key]int)
		keys := make([]key, 0)
		for _, row := range quantityRows {
			if !inSourceCategory(sourceCategory, row.IsManual) {
				continue
			}
			k := key{row.StageID, row.ItemID, row.Quantity}
			if _, ok := counts[k]; !ok {
				keys = append(keys, k)
			}
			counts[k] += row.Count
		}
		results := make([]*model.QuantityUniqCountResultForDropMatrix, 0, len(keys))
		for _, k := range keys {
			results = append(results, &model.QuantityUniqCountResultForDropMatrix{
				StageID:  k.stageId,
				ItemID:   k.itemId,
				Quantity: k.quantity,
				Count:    counts[k],
			})
		}
		quantityUniqCountsBySourceCategory[sourceCategory] = results
	}
	return timesBySourceCategory, quantityUniqCountsBySourceCategory
}

func (r *DropReport) CalcTotalQuantityForPatternMatrix(
	ctx context.Context, queryCtx *model.DropReportQueryContext,
) ([]*model.TotalQuantityResultForPatternMatrix, error) {
	results := make([]*model.TotalQuantityResultForPatternMatrix, 0)

	subq1 := r.db.NewSelect().
		TableExpr("drop_reports AS dr").
		Column("dr.source_name", "dr.stage_id", "dr.pattern_id")
	r.handleAccountAndReliability(subq1, queryCtx.AccountID)
	if queryCtx.ExcludeNonOneTimes {
		r.handleTimes(subq1, 1)
	}
	r.handleCreatedAtWithTime(subq1, queryCtx.StartTime, queryCtx.EndTime)
	r.handleServer(subq1, queryCtx.Server)
	r.handleStages(subq1, queryCtx.GetStageIds())
	r.handleTimes(subq1, 1)

	mainq := r.db.NewSelect().
		TableExpr("(?) AS a", subq1).
		Column("stage_id", "pattern_id").
		ColumnExpr("COUNT(*) AS total_quantity")
	r.handleSourceName(mainq, queryCtx.SourceCategory)

	if err := mainq.
		Group("stage_id", "pattern_id").
		Scan(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}

// CalcTotalQuantityForPatternMatrixBySourceCategories is CalcTotalQuantityForPatternMatrix for several source
// categories at once: drop_reports is scanned once and every report is classified as a manual or automated source.
// queryCtx.SourceCategory is ignored.
func (r *DropReport) CalcTotalQuantityForPatternMatrixBySourceCategories(
	ctx context.Context, queryCtx *model.DropReportQueryContext, sourceCategories []string,
) (map[string][]*model.TotalQuantityResultForPatternMatrix, error) {
	var rows []struct {
		StageID       int  `bun:"stage_id"`
		PatternID     int  `bun:"pattern_id"`
		IsManual      bool `bun:"is_manual"`
		TotalQuantity int  `bun:"total_quantity"`
	}

	subq1 := r.db.NewSelect().
		TableExpr("drop_reports AS dr").
		Column("dr.stage_id", "dr.pattern_id").
		ColumnExpr(isManualSourceExpr, bun.In(constant.ManualSources))
	r.handleAccountAndReliability(subq1, queryCtx.AccountID)
	r.handleCreatedAtWithTime(subq1, queryCtx.StartTime, queryCtx.EndTime)
	r.handleServer(subq1, queryCtx.Server)
	r.handleStages(subq1, queryCtx.GetStageIds())
	r.handleTimes(subq1, 1)

	if err := r.db.NewSelect().
		TableExpr("(?) AS a", subq1).
		Column("stage_id", "pattern_id", "is_manual").
		ColumnExpr("COUNT(*) AS total_quantity").
		Group("stage_id", "pattern_id", "is_manual").
		Scan(ctx, &rows); err != nil {
		return nil, err
	}

	type key struct{ stageId, patternId int }
	resultsBySourceCategory := make(map[string][]*model.TotalQuantityResultForPatternMatrix, len(sourceCategories))
	for _, sourceCategory := range sourceCategories {
		quantities := make(map[key]int)
		keys := make([]key, 0)
		for _, row := range rows {
			if !inSourceCategory(sourceCategory, row.IsManual) {
				continue
			}
			k := key{row.StageID, row.PatternID}
			if _, ok := quantities[k]; !ok {
				keys = append(keys, k)
			}
			quantities[k] += row.TotalQuantity
		}
		results := make([]*model.TotalQuantityResultForPatternMatrix, 0, len(keys))
		for _, k := range keys {
			results = append(results, &model.TotalQuantityResultForPatternMatrix{
				StageID:       k.stageId,
				PatternID:     k.patternId,
				TotalQuantity: quantities[k],
			})
		}
		resultsBySourceCategory[sourceCategory] = results
	}
	return resultsBySourceCategory, nil
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
	r.handleCreatedAtWithTime(subq1, &gameDayStart, &lastDayEnd)
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
	r.handleCreatedAtWithTime(subq1, &gameDayStart, &lastDayEnd)
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
					return sq.Where("dr.created_at >= ?", util.Now().Add(-24*time.Hour))
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

func (r *DropReport) CalcRecentUniqueUserCountBySource(ctx context.Context, duration time.Duration) ([]*modelv2.UniqueUserCountBySource, error) {
	results := make([]*modelv2.UniqueUserCountBySource, 0)
	subq := r.db.NewSelect().
		TableExpr("drop_reports AS dr").
		Column("dr.source_name", "dr.account_id")
	start := time.Now().Add(-duration)
	end := time.Now()
	r.handleCreatedAtWithTime(subq, &start, &end)
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

/**
 * Only return drop reports under one stage.
 */
func (r *DropReport) GetDropReports(ctx context.Context, queryCtx *model.DropReportQueryContext) ([]*model.DropReport, error) {
	results := make([]*model.DropReport, 0)
	query := r.db.NewSelect().
		TableExpr("drop_reports AS dr").
		Column("pattern_id", "created_at", "account_id", "source_name", "version", "times").
		Order("created_at")
	r.handleServer(query, queryCtx.Server)
	r.handleCreatedAtWithTime(query, queryCtx.StartTime, queryCtx.EndTime)

	if queryCtx.HasItemIds() {
		query = query.Join("JOIN drop_pattern_elements AS dpe ON dpe.drop_pattern_id = dr.pattern_id")
		r.handleStagesAndItems(query, *queryCtx.StageItemFilter)
	} else {
		r.handleStages(query, queryCtx.GetStageIds())
	}

	r.handleAccountAndReliability(query, queryCtx.AccountID)
	r.handleSourceName(query, queryCtx.SourceCategory)

	if queryCtx.ExcludeNonOneTimes {
		r.handleTimes(query, 1)
	} else if queryCtx.Times.Valid {
		r.handleTimes(query, int(queryCtx.Times.Int64))
	}

	if err := query.
		Scan(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}

func (r *DropReport) GetDropReportsForArchive(ctx context.Context, cursor *model.Cursor, date time.Time, limit int) ([]*model.DropReport, model.Cursor, error) {
	start := time.UnixMilli(util.GetDayStartTime(&date, "CN")) // we use CN server's day start time across all servers for archive
	end := start.Add(time.Hour * 24)
	results := make([]*model.DropReport, 0, limit)
	query := r.db.NewSelect().
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

// DeleteDropReportsForArchive deletes drop reports for archive.
// returns number of rows affected and error
func (r *DropReport) DeleteDropReportsForArchive(ctx context.Context, tx bun.Tx, date time.Time) (int64, error) {
	start := time.UnixMilli(util.GetDayStartTime(&date, "CN")) // we use CN server's day start time across all servers for archive
	end := start.Add(time.Hour * 24)
	res, err := tx.NewDelete().
		Model((*model.DropReport)(nil)).
		Where("created_at >= to_timestamp(?)", start.Unix()).
		Where("created_at < to_timestamp(?)", end.Unix()).
		Exec(ctx)
	if err != nil {
		return -1, err
	}
	return res.RowsAffected()
}

func (r *DropReport) handleStagesAndItems(query *bun.SelectQuery, stageIdItemIdMap map[int][]int) {
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

func (r *DropReport) handleStages(query *bun.SelectQuery, stageIds []int) {
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

func (r *DropReport) handleCreatedAtWithTime(query *bun.SelectQuery, start *time.Time, end *time.Time) {
	if start != nil {
		query = query.Where("dr.created_at >= to_timestamp(?)", start.Unix())
	}
	if end != nil {
		query = query.Where("dr.created_at < to_timestamp(?)", end.Unix())
	}
}

func (r *DropReport) handleServer(query *bun.SelectQuery, server string) {
	query = query.Where("dr.server = ?", server)
}

func (r *DropReport) handleTimes(query *bun.SelectQuery, times int) {
	query = query.Where("dr.times = ?", times)
}

// isManualSourceExpr classifies a report the same way handleSourceName filters it: NULL source_name is automated.
const isManualSourceExpr = "COALESCE(dr.source_name IN (?), false) AS is_manual"

// inSourceCategory tells whether a report classified by isManualSourceExpr belongs to sourceCategory.
func inSourceCategory(sourceCategory string, isManual bool) bool {
	switch sourceCategory {
	case constant.SourceCategoryManual:
		return isManual
	case constant.SourceCategoryAutomated:
		return !isManual
	default:
		return true
	}
}

func (r *DropReport) handleSourceName(query *bun.SelectQuery, sourceCategory string) {
	if sourceCategory == constant.SourceCategoryManual {
		query = query.Where("source_name IN (?)", bun.In(constant.ManualSources))
	} else if sourceCategory == constant.SourceCategoryAutomated {
		query = query.WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
			return q.Where("source_name NOT IN (?)", bun.In(constant.ManualSources)).WhereOr("source_name IS NULL")
		})
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

func newCursor(reports []*model.DropReport) model.Cursor {
	if len(reports) == 0 {
		return model.Cursor{}
	}
	return model.Cursor{
		Start: reports[0].ReportID,
		End:   reports[len(reports)-1].ReportID,
	}
}

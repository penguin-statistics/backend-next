package repo

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/pkg/errors"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/coldstore"
	"exusiai.dev/backend-next/internal/model"
)

// ColdDropReport runs the drop report aggregations of DropReport on the archived reports, in the local cold storage
// (#752), with the same results. Callers split the time ranges at the hot boundary of a View (see
// coldstore.View.Split), query the part before it here with that View and the rest with DropReport, and add the
// two up.
type ColdDropReport struct {
	reader *coldstore.Reader
}

// NewColdDropReport returns nil unless ColdstoreQueriesEnabled: the queries then read PostgreSQL alone.
func NewColdDropReport(conf *appconfig.Config, reader *coldstore.Reader) (*ColdDropReport, error) {
	if !conf.ColdstoreQueriesEnabled {
		return nil, nil
	}
	if reader == nil {
		return nil, errors.New("PENGUIN_V3_COLDSTORE_QUERIES_ENABLED requires PENGUIN_V3_COLDSTORE_ENABLED")
	}
	return &ColdDropReport{reader: reader}, nil
}

// View returns the current view of the cold storage. Take the split of a time range and the files of a query from
// the same View.
func (r *ColdDropReport) View() *coldstore.View {
	return r.reader.View()
}

// CalcDropMatrixForIntervalsBySourceCategories is DropReport.CalcDropMatrixForIntervalsBySourceCategories on the
// archived reports of view: every interval must end at the latest at view's hot boundary (the reports after it are
// not read). It counts the reports per segment between interval boundaries in the same way, from these files:
//
//   - the reports of an account (AccountID set): drop_reports, sorted by server and account so that the filter skips
//     most row groups;
//   - the other queries: the whole UTC hours of each segment from rollup (reliability = 0 reports counted per hour),
//     and only the hours a segment starts or ends within from drop_reports;
//   - the recruit stage (which the other files leave out), when the query needs it: recruit, in either case.
func (r *ColdDropReport) CalcDropMatrixForIntervalsBySourceCategories(
	ctx context.Context, view *coldstore.View, queryCtxs []*model.DropReportQueryContext, sourceCategories []string,
) (_ []map[string][]*model.TotalTimesResult, _ []map[string][]*model.QuantityUniqCountResultForDropMatrix, err error) {
	if len(queryCtxs) == 0 {
		return nil, nil, nil
	}
	plan := newDropMatrixPlan(queryCtxs)
	if end := plan.thresholds[len(plan.thresholds)-1]; end.After(view.HotBoundary()) {
		return nil, nil, errors.Errorf("coldstore: drop matrix interval ends at %s, after the hot boundary %s", end.UTC(), view.HotBoundary().UTC())
	}
	var queryFiles coldstore.QueryFiles
	query, ok := coldDropMatrixQuery(&queryFiles, view, plan, queryCtxs[0])
	if !ok {
		timesResults, quantityUniqCountResults := plan.results(queryCtxs, nil, sourceCategories)
		return timesResults, quantityUniqCountResults, nil
	}

	started := time.Now()
	defer func() { coldstore.ObserveQuery("drop_matrix", started, err) }()
	args := []any{queryCtxs[0].Server}
	if queryCtxs[0].AccountID.Valid {
		args = append(args, queryCtxs[0].AccountID.Int64)
	}
	rows, err := r.reader.Query(ctx, &queryFiles, query, args...)
	if err != nil {
		return nil, nil, errors.Wrap(err, "coldstore: drop matrix")
	}
	defer rows.Close()
	var results []dropMatrixRow
	for rows.Next() {
		var row dropMatrixRow
		if err := rows.Scan(&row.Kind, &row.StageID, &row.ItemID, &row.Quantity, &row.IsManual, &row.Segment, &row.Value); err != nil {
			return nil, nil, errors.Wrap(err, "coldstore: drop matrix")
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, errors.Wrap(err, "coldstore: drop matrix")
	}
	timesResults, quantityUniqCountResults := plan.results(queryCtxs, results, sourceCategories)
	return timesResults, quantityUniqCountResults, nil
}

// CalcTotalQuantityForPatternMatrixBySourceCategories is DropReport.CalcTotalQuantityForPatternMatrixBySourceCategories
// on the archived reports of view: the interval must end at the latest at view's hot boundary. It counts the reports
// with times = 1 per stage and pattern from the same files as CalcDropMatrixForIntervalsBySourceCategories.
func (r *ColdDropReport) CalcTotalQuantityForPatternMatrixBySourceCategories(
	ctx context.Context, view *coldstore.View, queryCtx *model.DropReportQueryContext, sourceCategories []string,
) (_ map[string][]*model.TotalQuantityResultForPatternMatrix, err error) {
	// the pattern matrix counts only the reports with times = 1, whatever queryCtx says
	oneTimeCtx := *queryCtx
	oneTimeCtx.ExcludeNonOneTimes = true
	plan := newDropMatrixPlan([]*model.DropReportQueryContext{&oneTimeCtx})
	if end := plan.thresholds[len(plan.thresholds)-1]; end.After(view.HotBoundary()) {
		return nil, errors.Errorf("coldstore: pattern matrix interval ends at %s, after the hot boundary %s", end.UTC(), view.HotBoundary().UTC())
	}
	if len(plan.thresholds) < 2 {
		// an empty interval
		return splitPatternMatrixRowsBySourceCategories(nil, sourceCategories), nil
	}
	var queryFiles coldstore.QueryFiles
	counts, ok := coldReportCounts(&queryFiles, view, plan, &oneTimeCtx)
	if !ok {
		return splitPatternMatrixRowsBySourceCategories(nil, sourceCategories), nil
	}

	started := time.Now()
	defer func() { coldstore.ObserveQuery("pattern_matrix", started, err) }()
	args := []any{queryCtx.Server}
	if queryCtx.AccountID.Valid {
		args = append(args, queryCtx.AccountID.Int64)
	}
	rows, err := r.reader.Query(ctx, &queryFiles, counts+`
		SELECT stage_id, pattern_id, is_manual, sum(cnt)::BIGINT AS total_quantity
		FROM a
		GROUP BY stage_id, pattern_id, is_manual`, args...)
	if err != nil {
		return nil, errors.Wrap(err, "coldstore: pattern matrix")
	}
	defer rows.Close()
	var results []patternMatrixRow
	for rows.Next() {
		var row patternMatrixRow
		if err := rows.Scan(&row.StageID, &row.PatternID, &row.IsManual, &row.TotalQuantity); err != nil {
			return nil, errors.Wrap(err, "coldstore: pattern matrix")
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Wrap(err, "coldstore: pattern matrix")
	}
	return splitPatternMatrixRowsBySourceCategories(results, sourceCategories), nil
}

// CalcTotalQuantityForTrend is DropReport.CalcTotalQuantityForTrendWithin on the archived reports of view: [from, to)
// must end at the latest at view's hot boundary. It counts the reports per bucket of the trend from the same files as
// CalcDropMatrixForIntervalsBySourceCategories, with the bucket boundaries as the segments.
func (r *ColdDropReport) CalcTotalQuantityForTrend(
	ctx context.Context, view *coldstore.View, from, to time.Time, server string, startTime *time.Time, intervalLength time.Duration, intervalNum int, stageIdItemIdMap map[int][]int, accountId null.Int, sourceCategory string,
) (_ []*model.TotalQuantityResultForTrend, err error) {
	results := make([]*model.TotalQuantityResultForTrend, 0)
	if len(stageIdItemIdMap) == 0 {
		return results, nil
	}
	stageIds := make([]int, 0, len(stageIdItemIdMap))
	for stageId := range stageIdItemIdMap {
		stageIds = append(stageIds, stageId)
	}
	sort.Ints(stageIds)
	trend, err := newColdTrendPlan(view.HotBoundary(), from, to, server, *startTime, intervalLength, intervalNum, stageIds)
	if err != nil {
		return nil, err
	}
	if len(trend.plan.thresholds) < 2 {
		return results, nil
	}
	queryCtx := &model.DropReportQueryContext{Server: server, AccountID: accountId}
	var queryFiles coldstore.QueryFiles
	counts, ok := coldReportCounts(&queryFiles, view, trend.plan, queryCtx)
	if !ok {
		return results, nil
	}

	// the filter of handleStagesAndItems
	stageConditions := make([]string, 0, len(stageIds))
	for _, stageId := range stageIds {
		condition := "a.stage_id = " + strconv.Itoa(stageId)
		if itemIds := stageIdItemIdMap[stageId]; len(itemIds) > 0 {
			condition += " AND dpe.item_id IN (" + joinInts(itemIds) + ")"
		}
		stageConditions = append(stageConditions, "("+condition+")")
	}

	started := time.Now()
	defer func() { coldstore.ObserveQuery("trend_quantity", started, err) }()
	rows, err := r.reader.Query(ctx, &queryFiles, counts+`
		SELECT a.segment, a.stage_id, dpe.item_id, sum(a.cnt * dpe.quantity)::BIGINT AS total_quantity
		FROM a JOIN `+queryFiles.ReadParquet([]string{view.DimFile(coldstore.DropPatternElementsFile)})+` AS dpe
			ON dpe.drop_pattern_id = a.pattern_id
		WHERE (`+strings.Join(stageConditions, " OR ")+`)`+coldSourceCategoryFilter(sourceCategory)+`
		GROUP BY a.segment, a.stage_id, dpe.item_id`, coldTrendArgs(queryCtx)...)
	if err != nil {
		return nil, errors.Wrap(err, "coldstore: trend quantity")
	}
	defer rows.Close()
	for rows.Next() {
		var segment int
		result := &model.TotalQuantityResultForTrend{}
		if err := rows.Scan(&segment, &result.StageID, &result.ItemID, &result.TotalQuantity); err != nil {
			return nil, errors.Wrap(err, "coldstore: trend quantity")
		}
		bucket, err := trend.bucket(segment)
		if err != nil {
			return nil, err
		}
		result.GroupID, result.IntervalStart, result.IntervalEnd = bucket.groupId, &bucket.start, &bucket.end
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Wrap(err, "coldstore: trend quantity")
	}
	return results, nil
}

// CalcTotalTimesForTrend is DropReport.CalcTotalTimesForTrendWithin on the archived reports of view, as
// CalcTotalQuantityForTrend.
func (r *ColdDropReport) CalcTotalTimesForTrend(
	ctx context.Context, view *coldstore.View, from, to time.Time, server string, startTime *time.Time, intervalLength time.Duration, intervalNum int, stageIds []int, accountId null.Int, sourceCategory string,
) (_ []*model.TotalTimesResultForTrend, err error) {
	results := make([]*model.TotalTimesResultForTrend, 0)
	if len(stageIds) == 0 {
		return results, nil
	}
	trend, err := newColdTrendPlan(view.HotBoundary(), from, to, server, *startTime, intervalLength, intervalNum, stageIds)
	if err != nil {
		return nil, err
	}
	if len(trend.plan.thresholds) < 2 {
		return results, nil
	}
	queryCtx := &model.DropReportQueryContext{Server: server, AccountID: accountId}
	var queryFiles coldstore.QueryFiles
	counts, ok := coldReportCounts(&queryFiles, view, trend.plan, queryCtx)
	if !ok {
		return results, nil
	}

	started := time.Now()
	defer func() { coldstore.ObserveQuery("trend_times", started, err) }()
	rows, err := r.reader.Query(ctx, &queryFiles, counts+`
		SELECT a.segment, a.stage_id, sum(a.times)::BIGINT AS total_times
		FROM a
		WHERE true`+coldSourceCategoryFilter(sourceCategory)+`
		GROUP BY a.segment, a.stage_id`, coldTrendArgs(queryCtx)...)
	if err != nil {
		return nil, errors.Wrap(err, "coldstore: trend times")
	}
	defer rows.Close()
	for rows.Next() {
		var segment int
		result := &model.TotalTimesResultForTrend{}
		if err := rows.Scan(&segment, &result.StageID, &result.TotalTimes); err != nil {
			return nil, errors.Wrap(err, "coldstore: trend times")
		}
		bucket, err := trend.bucket(segment)
		if err != nil {
			return nil, err
		}
		result.GroupID, result.IntervalStart, result.IntervalEnd = bucket.groupId, &bucket.start, &bucket.end
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Wrap(err, "coldstore: trend times")
	}
	return results, nil
}

// coldTrendPlan counts the reports of a part of a trend per bucket: segment i+1 of plan lies in buckets[i].
type coldTrendPlan struct {
	plan    dropMatrixPlan
	buckets []coldTrendBucket
}

// bucket returns the bucket of a segment.
func (p coldTrendPlan) bucket(segment int) (coldTrendBucket, error) {
	if segment < 1 || segment > len(p.buckets) {
		return coldTrendBucket{}, errors.Errorf("coldstore: trend segment %d out of %d buckets", segment, len(p.buckets))
	}
	return p.buckets[segment-1], nil
}

// coldTrendBucket is a bucket of a trend query, as genSubQueryForTrendSegments numbers it (group_id).
type coldTrendBucket struct {
	groupId    int
	start, end time.Time
}

// newColdTrendPlan returns the plan of the reports of the stages created in [from, to), cut to whole seconds and to
// the buckets of the trend, which must end at the latest at the hot boundary. Its thresholds are from, the bucket
// boundaries in between and to.
func newColdTrendPlan(
	hotBoundary, from, to time.Time, server string, startTime time.Time, intervalLength time.Duration, intervalNum int, stageIds []int,
) (coldTrendPlan, error) {
	hours := int(intervalLength.Hours())
	if hours <= 0 {
		return coldTrendPlan{}, errors.Errorf("coldstore: trend interval length %s is shorter than an hour", intervalLength)
	}
	length := time.Duration(hours) * time.Hour
	gameDayStart, lastDayEnd := TrendTimeRange(server, startTime, intervalLength, intervalNum)
	// as handleCreatedAtWithTime compares them
	from, to = time.Unix(from.Unix(), 0), time.Unix(to.Unix(), 0)
	if from.Before(gameDayStart) {
		from = gameDayStart
	}
	if to.After(lastDayEnd) {
		to = lastDayEnd
	}
	trend := coldTrendPlan{plan: dropMatrixPlan{stageIds: stageIds}}
	if !from.Before(to) {
		return trend, nil
	}
	if to.After(hotBoundary) {
		return coldTrendPlan{}, errors.Errorf("coldstore: trend ends at %s, after the hot boundary %s", to.UTC(), hotBoundary.UTC())
	}
	firstGroupId := int(from.Sub(gameDayStart) / length)
	trend.plan.thresholds = []time.Time{from}
	trend.plan.bucketStart, trend.plan.bucketLength = gameDayStart.Add(length*time.Duration(firstGroupId)), length
	for groupId := firstGroupId; trend.plan.thresholds[len(trend.plan.thresholds)-1].Before(to); groupId++ {
		bucket := coldTrendBucket{groupId: groupId, start: gameDayStart.Add(length * time.Duration(groupId))}
		bucket.end = bucket.start.Add(length)
		trend.buckets = append(trend.buckets, bucket)
		end := bucket.end
		if end.After(to) {
			end = to
		}
		trend.plan.thresholds = append(trend.plan.thresholds, end)
	}
	return trend, nil
}

// coldSourceCategoryFilter is handleSourceName on a(is_manual) of coldReportCounts, as a condition to append.
func coldSourceCategoryFilter(sourceCategory string) string {
	switch sourceCategory {
	case constant.SourceCategoryManual:
		return " AND a.is_manual"
	case constant.SourceCategoryAutomated:
		return " AND NOT a.is_manual"
	default:
		return ""
	}
}

// coldTrendArgs returns the parameters of coldReportCounts: the server and the account, if any.
func coldTrendArgs(queryCtx *model.DropReportQueryContext) []any {
	args := []any{queryCtx.Server}
	if queryCtx.AccountID.Valid {
		args = append(args, queryCtx.AccountID.Int64)
	}
	return args
}

// coldDropMatrixQuery returns the DuckDB query of CalcDropMatrixForIntervalsBySourceCategories, with the server as
// $1 and the account, if any, as $2, over the files of view it adds to queryFiles; or false when no file holds
// reports of the plan.
func coldDropMatrixQuery(queryFiles *coldstore.QueryFiles, view *coldstore.View, plan dropMatrixPlan, queryCtx *model.DropReportQueryContext) (string, bool) {
	counts, ok := coldReportCounts(queryFiles, view, plan, queryCtx)
	if !ok {
		return "", false
	}
	return counts + `
		SELECT 'quantity' AS kind, a.stage_id, dpe.item_id, dpe.quantity, a.is_manual, a.segment, sum(a.cnt)::BIGINT AS value
		FROM a JOIN ` + queryFiles.ReadParquet([]string{view.DimFile(coldstore.DropPatternElementsFile)}) + ` AS dpe
			ON dpe.drop_pattern_id = a.pattern_id
		GROUP BY a.stage_id, dpe.item_id, dpe.quantity, a.is_manual, a.segment
		UNION ALL
		SELECT 'times' AS kind, a.stage_id, 0, 0, a.is_manual, a.segment, sum(a.times)::BIGINT AS value
		FROM a
		GROUP BY a.stage_id, a.is_manual, a.segment`, true
}

// coldReportCounts returns the WITH clause of a DuckDB query, with the server as $1 and the account, if any, as $2,
// that counts the reports of the plan that queryCtx's filters (account and reliability, server, times, stages)
// keep: a(stage_id, pattern_id, is_manual, segment, cnt, times) holds the number of reports and their sum of times.
// It reads files of view, which it adds to queryFiles, and returns false when no file holds reports of the plan.
func coldReportCounts(queryFiles *coldstore.QueryFiles, view *coldstore.View, plan dropMatrixPlan, queryCtx *model.DropReportQueryContext) (string, bool) {
	first, last := plan.thresholds[0], plan.thresholds[len(plan.thresholds)-1]
	personal := queryCtx.AccountID.Valid

	// the filters of handleAccountAndReliability, handleServer, handleTimes and handleStages
	filters := []string{"server = $1"}
	if personal {
		filters = append(filters, "reliability >= 0", "account_id = $2")
	} else {
		filters = append(filters, "reliability = 0")
	}
	if queryCtx.ExcludeNonOneTimes {
		filters = append(filters, "times = 1")
	}
	stageSet := make(map[int]bool, len(plan.stageIds))
	for _, stageId := range plan.stageIds {
		stageSet[stageId] = true
	}
	allStages := len(plan.stageIds) == 0
	stageFilter := ""
	if !allStages {
		stageFilter = " AND stage_id IN (" + joinInts(plan.stageIds) + ")"
	}

	// recruit reports are in files of their own, grouped here by the recruit stage id of their month (the same in
	// every month so far), and left out of the other files
	recruit := map[int][]string{}
	var recruitStageIds []int
	for _, mf := range view.MonthFiles(coldstore.KindRecruit, coldstore.TimeRange{Start: first, End: last}) {
		stageId := mf.Month.RecruitStageID
		if _, ok := recruit[stageId]; !ok {
			recruitStageIds = append(recruitStageIds, stageId)
		}
		recruit[stageId] = append(recruit[stageId], mf.Path)
	}
	// the other files are read unless the query needs only the recruit stage (of every month it reads)
	otherStages := allStages || len(recruitStageIds) != 1
	for _, stageId := range plan.stageIds {
		if len(recruitStageIds) == 1 && stageId != recruitStageIds[0] {
			otherStages = true
		}
	}

	var parts []string
	reportsPart := func(stageIdExpr string, files []string, ranges []coldstore.TimeRange, stageFilter string) string {
		return `SELECT ` + stageIdExpr + ` AS stage_id, pattern_id, ` + coldIsManualSourceExpr + ` AS is_manual,
				created_at AS ts, 1::BIGINT AS cnt, times::BIGINT AS times
			FROM ` + queryFiles.ReadParquet(files) + `
			WHERE ` + strings.Join(filters, " AND ") + stageFilter + ` AND ` + timeFilter("created_at", first, last, ranges)
	}
	if otherStages {
		raw, rollup := []coldstore.TimeRange{{Start: first, End: last}}, []coldstore.TimeRange(nil)
		if !personal {
			raw, rollup = splitWholeHours(plan.thresholds)
		}
		if files := filesFor(view, coldstore.KindDropReports, raw); len(files) > 0 {
			parts = append(parts, reportsPart("stage_id", files, raw, stageFilter))
		}
		if files := filesFor(view, coldstore.KindRollup, rollup); len(files) > 0 {
			rollupFilters := `server = $1` + stageFilter
			if queryCtx.ExcludeNonOneTimes {
				rollupFilters += ` AND one_time`
			}
			parts = append(parts, `SELECT stage_id, pattern_id, is_manual, hour AS ts, cnt, times
				FROM `+queryFiles.ReadParquet(files)+`
				WHERE `+rollupFilters+` AND `+timeFilter("hour", first, last, rollup))
		}
	}
	for _, stageId := range recruitStageIds {
		if allStages || stageSet[stageId] {
			parts = append(parts, reportsPart(strconv.Itoa(stageId), recruit[stageId], nil, ""))
		}
	}
	if len(parts) == 0 {
		return "", false
	}

	// every report is in the segment that starts last at or before its time (they are contiguous from first to last)
	segments := make([]string, 0, len(plan.thresholds)-1)
	for i := 0; i+1 < len(plan.thresholds); i++ {
		segments = append(segments, "("+strconv.Itoa(i+1)+", "+timestamptz(plan.thresholds[i])+")")
	}
	for i, part := range parts {
		if plan.bucketLength > 0 {
			// the bucket of a report, computed: three times faster than the join over the tens of millions of rollup
			// rows of a stage over years (no report is before bucketStart, so the division rounds down)
			parts[i] = `SELECT p.stage_id, p.pattern_id, p.is_manual,
					(1 + (epoch_us(p.ts) - ` + strconv.FormatInt(plan.bucketStart.UnixMicro(), 10) + `) // ` + strconv.FormatInt(plan.bucketLength.Microseconds(), 10) + `)::INTEGER AS segment,
					p.cnt, p.times
				FROM (` + part + `) AS p`
			continue
		}
		parts[i] = `SELECT p.stage_id, p.pattern_id, p.is_manual, seg.segment, p.cnt, p.times
			FROM (` + part + `) AS p ASOF JOIN seg ON p.ts >= seg.seg_start`
	}
	return `WITH seg(segment, seg_start) AS (VALUES ` + strings.Join(segments, ", ") + `),
		a AS MATERIALIZED (
			SELECT stage_id, pattern_id, is_manual, segment, sum(cnt)::BIGINT AS cnt, sum(times)::BIGINT AS times
			FROM (` + strings.Join(parts, "\nUNION ALL\n") + `)
			GROUP BY stage_id, pattern_id, is_manual, segment)`, true
}

// coldIsManualSourceExpr is isManualSourceExpr in DuckDB; the rollup's is_manual is computed with it.
var coldIsManualSourceExpr = func() string {
	quoted := make([]string, len(constant.ManualSources))
	for i, s := range constant.ManualSources {
		quoted[i] = quoteDuckDB(s)
	}
	return "COALESCE(source_name IN (" + strings.Join(quoted, ", ") + "), false)"
}()

// splitWholeHours splits the segments between consecutive thresholds into the time ranges to read from the rollup,
// the whole UTC hours within a segment, and those to read from the reports, the rest: the part of the first and of
// the last hour of a segment that it does not cover in full. Adjacent ranges are merged.
func splitWholeHours(thresholds []time.Time) (raw, rollup []coldstore.TimeRange) {
	for i := 0; i+1 < len(thresholds); i++ {
		start, end := thresholds[i], thresholds[i+1]
		fromHour := start.Truncate(time.Hour)
		if fromHour.Before(start) {
			fromHour = fromHour.Add(time.Hour)
		}
		toHour := end.Truncate(time.Hour)
		if !fromHour.Before(toHour) {
			raw = appendRange(raw, start, end)
			continue
		}
		raw = appendRange(raw, start, fromHour)
		rollup = appendRange(rollup, fromHour, toHour)
		raw = appendRange(raw, toHour, end)
	}
	return raw, rollup
}

// appendRange appends [start, end) to ranges, extending the last range if it ends at start; an empty range is
// left out.
func appendRange(ranges []coldstore.TimeRange, start, end time.Time) []coldstore.TimeRange {
	if !start.Before(end) {
		return ranges
	}
	if n := len(ranges); n > 0 && ranges[n-1].End.Equal(start) {
		ranges[n-1].End = end
		return ranges
	}
	return append(ranges, coldstore.TimeRange{Start: start, End: end})
}

// filesFor returns the files of a kind of the months that overlap any of ranges, each once.
func filesFor(view *coldstore.View, kind coldstore.Kind, ranges []coldstore.TimeRange) []string {
	var files []string
	seen := make(map[string]bool)
	for _, r := range ranges {
		for _, f := range view.Files(kind, r) {
			if !seen[f] {
				seen[f] = true
				files = append(files, f)
			}
		}
	}
	return files
}

// timeFilter restricts column to [first, last) and, if not nil, to ranges within it. The range of the whole
// lets DuckDB skip row groups by their statistics.
func timeFilter(column string, first, last time.Time, ranges []coldstore.TimeRange) string {
	f := column + " >= " + timestamptz(first) + " AND " + column + " < " + timestamptz(last)
	if len(ranges) == 0 || (len(ranges) == 1 && ranges[0].Start.Equal(first) && ranges[0].End.Equal(last)) {
		return f
	}
	conds := make([]string, len(ranges))
	for i, r := range ranges {
		conds[i] = "(" + column + " >= " + timestamptz(r.Start) + " AND " + column + " < " + timestamptz(r.End) + ")"
	}
	return f + " AND (" + strings.Join(conds, " OR ") + ")"
}

// timestamptz returns a DuckDB TIMESTAMPTZ literal of t, to the microsecond.
func timestamptz(t time.Time) string {
	return "TIMESTAMPTZ '" + t.UTC().Format("2006-01-02 15:04:05.999999") + "+00'"
}

func quoteDuckDB(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func joinInts(ints []int) string {
	s := make([]string, len(ints))
	for i, v := range ints {
		s[i] = strconv.Itoa(v)
	}
	return strings.Join(s, ",")
}

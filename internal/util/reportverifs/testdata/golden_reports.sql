-- Exports golden_reports.json, the fixture of TestReliabilityGolden, from a copy of the production database:
--
--	psql -qAt -v top=<the highest report_id> < golden_reports.sql > golden_reports.json
--
-- The reports are the last 1000 and those with a reliability above 0 among the last 20000, with the drop infos of
-- the time ranges current when they were created and the reject rules. Every read is by primary key or index.
--
-- No personal data: account ids are replaced by their rank of appearance, IP addresses in the reject rules by
-- 192.0.2.1/32 (a documentation address) and the account ids they name by the rank (0 for an account with no report
-- here); the IP addresses of the reports and their metadata are left out (the metadata is null for all of them).
--
-- drop_reports stores the drops merged by item (the pattern), not their drop types: each drop gets the drop type of
-- the one drop info of its item for the stage, or REGULAR if it has none (an unknown item is a violation whatever its
-- type). The export checks that no item has two drop infos, and that the time ranges of a stage do not change among
-- its reports, so that the test may treat every exported time range as current.

SET statement_timeout = '60s';
SET default_transaction_read_only = on;

WITH picked AS (
	SELECT * FROM drop_reports
	WHERE (report_id > :top - 1000 OR (report_id > :top - 20000 AND reliability > 0)) AND report_id <= :top
		AND reliability >= 0 -- -1 is a recalled report
), accounts AS (
	SELECT account_id, row_number() OVER (ORDER BY min(report_id)) AS n FROM picked GROUP BY account_id
), report_ranges AS (
	SELECT DISTINCT p.report_id, p.server, p.stage_id, di.range_id FROM picked p
	JOIN drop_infos di ON di.stage_id = p.stage_id AND di.server::text = p.server::text
	JOIN time_ranges tr ON tr.range_id = di.range_id AND tr.start_time <= p.created_at AND tr.end_time > p.created_at
), ranges AS (
	SELECT DISTINCT server, stage_id, range_id FROM report_ranges
), unstable AS (
	SELECT server, stage_id FROM (
		SELECT server, stage_id, report_id, array_agg(range_id ORDER BY range_id) AS s FROM report_ranges GROUP BY 1, 2, 3
	) x GROUP BY 1, 2 HAVING count(DISTINCT s) > 1
), drop_infos AS (
	SELECT di.* FROM drop_infos di JOIN ranges r ON r.range_id = di.range_id AND r.stage_id = di.stage_id AND r.server::text = di.server::text
), drops AS (
	SELECT p.report_id, e.item_id, e.quantity, coalesce(min(di.drop_type::text), 'REGULAR') AS drop_type, count(di.drop_id) AS candidates
	FROM picked p
	JOIN drop_pattern_elements e ON e.drop_pattern_id = p.pattern_id
	LEFT JOIN drop_infos di ON di.stage_id = p.stage_id AND di.server::text = p.server::text AND di.item_id = e.item_id
		AND di.drop_type <> 'RECOGNITION_ONLY'
	GROUP BY 1, 2, 3
), checks AS (
	SELECT (SELECT count(*) FROM unstable) AS unstable, (SELECT count(*) FROM drops WHERE candidates > 1) AS ambiguous
), reports AS (
	SELECT p.*, a.n AS account, st.ark_stage_id,
		(SELECT json_agg(json_build_object('dropType', d.drop_type, 'itemId', d.item_id, 'quantity', d.quantity) ORDER BY d.item_id)
			FROM drops d WHERE d.report_id = p.report_id) AS drops
	FROM picked p JOIN accounts a USING (account_id) JOIN stages st USING (stage_id)
), tasks AS (
	SELECT json_build_object(
		'createdAt', (extract(epoch FROM created_at) * 1000000)::bigint,
		'server', server, 'source', source_name, 'version', version, 'accountId', account,
		'reports', json_agg(json_build_object(
			'stageId', ark_stage_id, 'times', times, 'drops', coalesce(drops, '[]'), 'reliability', reliability
		) ORDER BY report_id)
	) AS j, min(report_id) AS first
	FROM reports GROUP BY account, created_at, server, source_name, version
), rules AS (
	SELECT rule_id, status, with_reliability,
		regexp_replace(
			regexp_replace(expr, '\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}(/\d+)?', '192.0.2.1/32', 'g'),
			'Task\.AccountID == \d+',
			'Task.AccountID == ' || coalesce((SELECT n FROM accounts WHERE account_id = substring(expr FROM 'Task\.AccountID == (\d+)')::bigint), 0),
			'g'
		) AS expr
	FROM reject_rules
)
SELECT CASE WHEN c.unstable > 0 OR c.ambiguous > 0 THEN
	'the export does not hold: ' || c.unstable || ' stages whose time ranges change, ' || c.ambiguous || ' ambiguous drops'
ELSE '{
"stages": [
' || (SELECT string_agg(json_build_object('stageId', stage_id, 'arkStageId', ark_stage_id)::text, E',\n' ORDER BY stage_id)
	FROM stages WHERE stage_id IN (SELECT stage_id FROM picked)) || '
],
"timeRanges": [
' || (SELECT string_agg(json_build_object('rangeId', range_id, 'server', server, 'startTime', start_time, 'endTime', end_time)::text, E',\n' ORDER BY range_id)
	FROM time_ranges WHERE range_id IN (SELECT range_id FROM ranges)) || '
],
"dropInfos": [
' || (SELECT string_agg(json_build_object('dropId', drop_id, 'server', server, 'stageId', stage_id, 'itemId', item_id,
		'dropType', drop_type, 'rangeId', range_id, 'accumulable', accumulable, 'bounds', bounds)::text, E',\n' ORDER BY drop_id)
	FROM drop_infos) || '
],
"rejectRules": [
' || (SELECT string_agg(json_build_object('ruleId', rule_id, 'status', status, 'withReliability', with_reliability, 'expr', expr)::text, E',\n' ORDER BY rule_id)
	FROM rules) || '
],
"tasks": [
' || (SELECT string_agg(j::text, E',\n' ORDER BY first) FROM tasks) || '
]
}' END
FROM checks c;

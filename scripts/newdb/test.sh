#!/usr/bin/env bash
# Runs migrate.sh from a synthetic old database to a new one and checks the result. Both databases are wiped: use
# disposable ones, e.g.
#
#   docker run -d --name newdb-old -e POSTGRES_PASSWORD=test -p 127.0.0.1:55521:5432 postgres:14-alpine
#   docker run -d --name newdb-new -e POSTGRES_PASSWORD=test -p 127.0.0.1:55527:5432 postgres:17-alpine
#   OLD_DSN=postgres://postgres:test@127.0.0.1:55521/postgres NEW_DSN=postgres://postgres:test@127.0.0.1:55527/postgres \
#     scripts/newdb/test.sh
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
: "${OLD_DSN:?OLD_DSN is required}" "${NEW_DSN:?NEW_DSN is required}"
export PGTZ=UTC

oldq() { PGOPTIONS='-c default_transaction_read_only=off' psql "$OLD_DSN" -X -q -v ON_ERROR_STOP=1 -At "$@"; }
newq() { psql "$NEW_DSN" -X -q -v ON_ERROR_STOP=1 -At "$@"; }
failed=0
check() { # check NAME GOT WANT
	if [[ $2 == "$3" ]]; then
		echo "ok   $1"
	else
		echo "FAIL $1: got '$2', want '$3'"
		failed=1
	fi
}

for q in oldq newq; do
	$q -c "ALTER DATABASE \"$($q -c 'SELECT current_database()')\" RESET default_transaction_read_only"
	$q -c "SET client_min_messages = warning" -c "DROP SCHEMA public CASCADE" -c "CREATE SCHEMA public"
done
oldq -f "$here/testdata/old_schema.sql"
oldq -f "$here/testdata/seed.sql" > /dev/null
oldq -c "ANALYZE"
# read-only, as in the maintenance window (T1)
oldq -c "ALTER DATABASE \"$(oldq -c 'SELECT current_database()')\" SET default_transaction_read_only = on"

export WORKDIR=$(mktemp -d -t newdb-test.XXXXXX) JOBS=3 HOT_FROM=2026-08
# the reports start in 2026-06, but for two before the coldstore epoch (2019-05) that only the "pre" archive holds: a
# cold store without 2026-06, without 2026-07 or without "pre" is refused before anything is created
check "reports before the coldstore epoch" "$(oldq -c "SELECT count(*) FROM drop_reports WHERE created_at < '2019-05-01 00:00:00+00'")" "2"
echo '{"months": [{"month": "pre"}, {"month": "2026-07"}]}' > "$WORKDIR/manifest-without-first.json"
echo '{"months": [{"month": "pre"}, {"month": "2026-06"}]}' > "$WORKDIR/manifest-without-last.json"
echo '{"months": [{"month": "2026-06"}, {"month": "2026-07"}]}' > "$WORKDIR/manifest-without-pre.json"
check "schema refuses a cold store without the oldest month" "$(COLDSTORE_MANIFEST=$WORKDIR/manifest-without-first.json \
	"$here/migrate.sh" schema 2>&1 | grep -c 'lacks months before HOT_FROM 2026-08: 2026-06$')" "1"
check "schema refuses a cold store without the month before HOT_FROM" "$(COLDSTORE_MANIFEST=$WORKDIR/manifest-without-last.json \
	"$here/migrate.sh" schema 2>&1 | grep -c 'lacks months before HOT_FROM 2026-08: 2026-07$')" "1"
check "schema refuses a cold store without pre" "$(COLDSTORE_MANIFEST=$WORKDIR/manifest-without-pre.json \
	"$here/migrate.sh" schema 2>&1 | grep -c 'lacks months before HOT_FROM 2026-08: pre$')" "1"
check "nothing created on the new database" "$(newq -c "SELECT count(*) FROM pg_class WHERE relnamespace = 'public'::regnamespace")" "0"
echo '{"months": [{"month": "pre"}, {"month": "2026-06"}, {"month": "2026-07"}]}' > "$WORKDIR/manifest.json"
export COLDSTORE_MANIFEST=$WORKDIR/manifest.json
check "schema accepts a cold store with pre and every month" "$("$here/migrate.sh" schema 2>&1 |
	grep -c 'the cold store holds every month before 2026-08$')" "1"
"$here/migrate.sh" small hot
# every step again on top, as after a failure: no duplicate key, the same result
"$here/migrate.sh"
# post again after it completed: indexes.sql is skipped, the sequences are set again
"$here/migrate.sh" post verify

# verify fails when it cannot list what it compares (through a psql that fails on one query): the hot months, which
# leaves no report to compare, or the other tables, while the comparison of the reports succeeds
mkdir "$WORKDIR/bin"
cat > "$WORKDIR/bin/psql" <<EOF
#!/usr/bin/env bash
[[ \$* == *"\$PSQL_FAIL_ON"* ]] && { echo "psql: failure injected by test.sh" >&2; exit 3; }
exec $(command -v psql) "\$@"
EOF
chmod +x "$WORKDIR/bin/psql"
for fail in "generate_series(timestamp:cannot list the hot months" "relname NOT LIKE:cannot list the tables of the new database"; do
	fail_on=${fail%%:*} reason=${fail#*:}
	out=$(PATH=$WORKDIR/bin:$PATH PSQL_FAIL_ON=$fail_on "$here/migrate.sh" verify 2>&1) && status=0 || status=$?
	check "verify fails when it $reason" "$status $(grep -c " $reason$" <<< "$out") $(grep -c 'verification FAILED' <<< "$out")" "1 1 1"
done
check "the reports verified when only the tables cannot be listed" "$(grep -c '^[^ ]* verified ' <<< "$out")" \
	"$((2 * (12 * ($(date -u +%Y) - 2026) + 10#$(date -u +%m) - 8 + 1)))"

# what migrate.sh verified per day, counted again per month
for m in 2026-08 2026-09 2026-10; do
	lo="$m-01 00:00:00+00" hi="$(date -u -d "$m-01 +1 month" +%Y-%m-01) 00:00:00+00"
	check "$m drop_reports" "$(newq -c "SELECT count(*) FROM drop_reports WHERE created_at >= '$lo' AND created_at < '$hi'")" \
		"$(oldq -c "SELECT count(*) FROM drop_reports WHERE created_at >= '$lo' AND created_at < '$hi'")"
	check "$m drop_report_extras" "$(newq -c "SELECT count(*) FROM drop_report_extras WHERE created_at >= '$lo' AND created_at < '$hi'")" \
		"$(oldq -c "SELECT count(*) FROM drop_reports r JOIN drop_report_extras e USING (report_id)
			WHERE r.created_at >= '$lo' AND r.created_at < '$hi'")"
done
check "nothing before HOT_FROM" "$(newq -c "SELECT count(*) FROM drop_reports WHERE created_at < '2026-08-01 00:00:00+00'")" "0"
check "reports on the first instant of HOT_FROM" "$(newq -c "SELECT count(*) FROM drop_reports WHERE created_at = '2026-08-01 00:00:00+00'")" "2"
# recruitment reports (stage 9) are reports like the others: in drop_reports with their pattern, and their extras
check "recruitment reports in drop_reports" "$(newq -c "SELECT count(*) || ',' || count(DISTINCT pattern_id) FROM drop_reports WHERE stage_id = 9")" \
	"$(oldq -c "SELECT count(*) || ',' || count(DISTINCT pattern_id) FROM drop_reports WHERE stage_id = 9 AND created_at >= '2026-08-01 00:00:00+00'")"
check "extras of recruitment reports" "$(newq -c "SELECT count(*) FROM drop_report_extras e JOIN drop_reports r USING (report_id, created_at) WHERE r.stage_id = 9")" \
	"$(oldq -c "SELECT count(*) FROM drop_reports r JOIN drop_report_extras e USING (report_id)
		WHERE r.created_at >= '2026-08-01 00:00:00+00' AND r.stage_id = 9")"
# HOT_FROM up to RUNWAY_MONTHS (3) after the month of the latest report or the current month
check "partitions" "$(newq -c "SELECT min(c.relname) || ' ' || max(c.relname) || ' ' || count(*) FROM pg_inherits i
	JOIN pg_class c ON c.oid = i.inhrelid WHERE i.inhparent = 'drop_reports'::regclass")" \
	"drop_reports_2026_08 drop_reports_$(date -u -d "$(date -u +%Y-%m-01) +3 month" +%Y_%m) $((3 + 12 * ($(date -u +%Y) - 2026) + 10#$(date -u +%m) - 8 + 1))"
check "extras carry the created_at of their report" "$(newq -c "SELECT count(*) FROM drop_report_extras e
	LEFT JOIN drop_reports r ON r.report_id = e.report_id AND r.created_at = e.created_at WHERE r.report_id IS NULL")" "0"
old_seq=$(oldq -c "SELECT last_value FROM drop_reports_report_id_seq")
check "report ids continue after the old database" "$(newq -c "SELECT nextval('drop_reports_report_id_seq')")" "$((old_seq + 1))"
check "accounts continue" "$(newq -c "SELECT nextval('accounts_account_id_seq')")" "51"
check "indexes of drop_reports" "$(newq -c "SELECT string_agg(indexname, ' ' ORDER BY indexname) FROM pg_indexes WHERE schemaname = current_schema() AND tablename = 'drop_reports'")" \
	"drop_reports_account_id_created_at drop_reports_created_at drop_reports_pkey"
check "foreign keys" "$(newq -c "SELECT count(*) FROM pg_constraint WHERE contype = 'f' AND conrelid = 'drop_reports'::regclass")" "3"

# reports of now and of the next month can be written, not those of a month without partition
newq -c "INSERT INTO drop_reports (stage_id, pattern_id, times, reliability, server, account_id) VALUES (1, 2, 1, 0, 'CN', 1)"
newq -c "INSERT INTO drop_reports (stage_id, pattern_id, times, created_at, reliability, server, account_id)
	VALUES (9, 41, 1, date_trunc('month', now()) + interval '1 month 1 day', 0, 'CN', 1)"
check "a report of a month without partition fails" \
	"$(newq -c "INSERT INTO drop_reports (stage_id, pattern_id, times, created_at, reliability, server, account_id)
		VALUES (1, 2, 1, '2100-01-01 00:00:00+00', 0, 'CN', 1)" 2>&1 | grep -c 'no partition of relation')" "1"

# the bounds of the coldstore epoch: a report in its last microsecond alone still needs pre
oldq -c "DELETE FROM drop_reports WHERE created_at < '2019-04-01 00:00:00+00'"
check "schema refuses a cold store without pre for a report at 2019-04-30 23:59:59.999999" \
	"$(COLDSTORE_MANIFEST=$WORKDIR/manifest-without-pre.json \
		"$here/migrate.sh" schema 2>&1 | grep -c 'lacks months before HOT_FROM 2026-08: pre$')" "1"
# without reports before the coldstore epoch, the cold store does not need pre
oldq -c "DELETE FROM drop_reports WHERE created_at < '2019-05-01 00:00:00+00'"
check "schema accepts a cold store without pre when no report is before 2019-05" "$(COLDSTORE_MANIFEST=$WORKDIR/manifest-without-pre.json \
	"$here/migrate.sh" schema 2>&1 | grep -c 'the cold store holds every month before 2026-08$')" "1"
# a report at 2019-05-01 00:00:00 is in the first month of the cold store, not in pre
oldq -c "INSERT INTO drop_reports (stage_id, pattern_id, times, created_at, reliability, server, account_id)
	VALUES (1, 2, 1, '2019-05-01 00:00:00+00', 0, 'CN', 4)"
check "schema refuses a cold store without 2019-05 for a report at 2019-05-01 00:00:00" \
	"$(COLDSTORE_MANIFEST=$WORKDIR/manifest-without-pre.json \
		"$here/migrate.sh" schema 2>&1 | grep -c 'lacks months before HOT_FROM 2026-08: 2019-05 2019-06 .* 2026-05$')" "1"
oldq -c "SELECT json_build_object('months', json_agg(json_build_object('month', to_char(m, 'YYYY-MM')) ORDER BY m))
	FROM generate_series(timestamp '2019-05-01', '2026-07-01', interval '1 month') m" > "$WORKDIR/manifest-from-epoch.json"
check "schema accepts a cold store from 2019-05 on without pre for a report at 2019-05-01 00:00:00" \
	"$(COLDSTORE_MANIFEST=$WORKDIR/manifest-from-epoch.json \
		"$here/migrate.sh" schema 2>&1 | grep -c 'the cold store holds every month before 2026-08$')" "1"

oldq -c "ALTER DATABASE \"$(oldq -c 'SELECT current_database()')\" RESET default_transaction_read_only"
if [[ $failed == 1 ]]; then
	echo "test FAILED"
	exit 1
fi
echo "test passed"

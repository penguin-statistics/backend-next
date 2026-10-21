#!/usr/bin/env bash
# Moves the database to the new PostgreSQL 17 host in the maintenance window: steps T2-T5 of #752.
#
#   OLD_DSN=postgres://...  NEW_DSN=postgres://...  HOT_FROM=YYYY-MM  scripts/newdb/migrate.sh [STEP...]
#
# The old database must be read-only (default_transaction_read_only = on, T1) and the new one empty. The steps, all
# by default, in this order:
#
#   schema  T2  checks that the cold store holds every month before HOT_FROM (COLDSTORE_MANIFEST), then schema.sql on
#               the new database, and the partitions of the hot months and of the RUNWAY_MONTHS after them
#   small   T2  every table but drop_reports and drop_report_extras: pg_dump -Fd --data-only, pg_restore
#   hot     T3  the reports created since HOT_FROM: drop_reports (recruitment reports included, as today) and their
#               drop_report_extras (with the created_at of the report)
#   post    T4  indexes.sql (secondary indexes, foreign keys), ANALYZE, every sequence set to at least the old value
#               and the highest id that uses it
#   verify  T5  per day of the hot months, row count and content hash of both report tables, old against new;
#               row counts of the other tables
#
# Environment:
#   OLD_DSN, NEW_DSN  connection strings (libpq URI or key=value) of the old and the new database
#   HOT_FROM          first hot month, YYYY-MM: older reports are in the cold store and are not copied
#   COLDSTORE_MANIFEST
#                     the manifest.json of the cold store. schema stops unless it lists every month from its first one
#                     through the month before HOT_FROM, the first one being at most the month of the oldest report
#                     (from 2019-05) of the old database, and "pre" (the archive of the reports before 2019-05, the
#                     coldstore epoch) when the old database has such reports: a month in neither store would be lost
#                     without any error, as reads send the months before the first partition to the cold store. "skip"
#                     skips the check.
#   JOBS              parallel jobs for pg_dump, pg_restore, the copy and the verification (default 4)
#   WORKDIR           directory for the dump and the verification outputs (default: a new temporary directory)
#   VERIFY_HASH       1 (default) compares a hash of every column, 0 only the row counts
#   RUNWAY_MONTHS     months of partitions created after the last hot month (default 3). There is no default
#                     partition: a report of a month without partition fails to store, so the months after these must
#                     be created ahead of time (SELECT create_report_partitions(date)).
#
# Use the client tools of the new version (pg_dump 17 can dump PostgreSQL 14). Every step logs its duration. The
# Parquet export of the hot months (also part of T3) is not done here: the hot months stay in the new database, and
# are exported from there when they are archived.
#
# Re-running after a failure: every step can be run again on its own, e.g. `migrate.sh hot post verify` after a copy
# job failed. schema skips schema.sql when the new database already has it (it is applied in one transaction) and
# only creates the missing partitions; small empties the tables it restores first; each copy job of hot empties its
# partition first, in the transaction of its COPY; post applies indexes.sql in one transaction, skipped when the new
# database already has it, and its ANALYZE and sequence updates can run any number of times. small cannot run again
# after post, whose foreign keys forbid emptying the tables that drop_reports references. To start over from an empty
# new database instead:
#   psql "$NEW_DSN" -c 'DROP SCHEMA public CASCADE' -c 'CREATE SCHEMA public'
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
: "${OLD_DSN:?OLD_DSN is required}" "${NEW_DSN:?NEW_DSN is required}" "${HOT_FROM:?HOT_FROM (YYYY-MM) is required}"
[[ $HOT_FROM =~ ^[0-9]{4}-(0[1-9]|1[0-2])$ ]] || { echo "HOT_FROM must be YYYY-MM, got '$HOT_FROM'" >&2; exit 2; }
export OLD_DSN NEW_DSN HOT_FROM
export JOBS=${JOBS:-4} VERIFY_HASH=${VERIFY_HASH:-1} RUNWAY_MONTHS=${RUNWAY_MONTHS:-3} COLDSTORE_MANIFEST=${COLDSTORE_MANIFEST:-}
[[ $RUNWAY_MONTHS =~ ^[1-9][0-9]*$ ]] || { echo "RUNWAY_MONTHS must be a positive integer, got '$RUNWAY_MONTHS'" >&2; exit 2; }
if [[ -z ${WORKDIR:-} ]]; then WORKDIR=$(mktemp -d -t newdb-migrate.XXXXXX); fi
export WORKDIR
# both sides print timestamps, compute days and month bounds in UTC, in the same format (the content hashes compare
# the text of the rows)
export PGTZ=UTC PGDATESTYLE=ISO

oldq() { psql "$OLD_DSN" -X -q -v ON_ERROR_STOP=1 -At "$@"; }
newq() { psql "$NEW_DSN" -X -q -v ON_ERROR_STOP=1 -At "$@"; }
log() { echo "$(date -u +%FT%TZ) $*" >&2; }

# the hot months: HOT_FROM up to the month of the latest report (or the current month), as YYYY-MM
hot_months() {
	oldq -c "SELECT to_char(m, 'YYYY-MM') FROM generate_series(timestamp '$HOT_FROM-01',
		date_trunc('month', greatest(now(), (SELECT max(created_at) FROM drop_reports)))::timestamp, interval '1 month') m"
}

# month_bounds YYYY-MM prints the UTC bounds [lo, hi) of the month
month_bounds() {
	echo "$1-01T00:00:00+00" "$(date -u -d "$1-01 +1 month" +%Y-%m-01)T00:00:00+00"
}

# old_select KIND MONTH prints the query reading the rows of a hot month of KIND (reports, extras) from the
# old database, in the column order of new_select. The report ids of the month bound drop_report_extras by its primary
# key, so the join reads a range of it instead of all of it.
old_select() {
	local kind=$1 lo hi ids
	read -r lo hi < <(month_bounds "$2")
	local month="r.created_at >= '$lo' AND r.created_at < '$hi'"
	case $kind in
	reports)
		echo "SELECT r.report_id, r.stage_id, r.pattern_id, r.times, r.created_at, r.reliability, r.server, r.account_id,
			r.source_name, r.version
			FROM drop_reports r WHERE $month" ;;
	extras)
		ids=$(oldq -c "SELECT coalesce(min(report_id), 0) || ' AND ' || coalesce(max(report_id), 0) FROM drop_reports r WHERE $month") ||
			return 1
		echo "SELECT e.report_id, e.ip, e.metadata, e.md5, r.created_at
			FROM drop_reports r JOIN drop_report_extras e ON e.report_id = r.report_id AND e.report_id BETWEEN $ids
			WHERE $month" ;;
	esac
}

# new_select KIND MONTH prints the query reading what was copied of KIND for a month from the new database
new_select() {
	local suffix=${2/-/_}
	case $1 in
	reports) echo "SELECT report_id, stage_id, pattern_id, times, created_at, reliability, server, account_id, source_name,
		version FROM drop_reports_$suffix" ;;
	extras) echo "SELECT report_id, ip, metadata, md5, created_at FROM drop_report_extras_$suffix" ;;
	esac
}

# new_partition KIND MONTH prints the partition of the new database that a copy job of KIND fills for a month
new_partition() {
	case $1 in
	reports) echo "drop_reports_${2/-/_}" ;;
	extras) echo "drop_report_extras_${2/-/_}" ;;
	esac
}

new_columns() {
	case $1 in
	reports) echo "$(new_partition "$@") (report_id, stage_id, pattern_id, times, created_at, reliability, server, account_id, source_name, version)" ;;
	extras) echo "$(new_partition "$@") (report_id, ip, metadata, md5, created_at)" ;;
	esac
}

# per_day QUERY prints day, row count and (with VERIFY_HASH=1) the sum of a hash of the text of every row
per_day() {
	local hash="0"
	[[ $VERIFY_HASH == 1 ]] && hash="sum(hashtext(t::text)::bigint)"
	echo "COPY (SELECT date_trunc('day', t.created_at), count(*), $hash FROM ($1) t GROUP BY 1 ORDER BY 1) TO STDOUT"
}

# jobs run by xargs: _copy KIND MONTH, _verify KIND MONTH
#
# A copy job owns its partition: it empties it and fills it in one transaction, so it can run again after a failure.
# (If the old side dies mid-stream, the new side sees the end of the data and commits what it received: the job fails
# all the same, through pipefail, and verify would see the gap.)
job_copy() {
	local start=$SECONDS query
	query=$(old_select "$1" "$2")
	oldq -c "COPY ($query) TO STDOUT" |
		newq -1 -c "TRUNCATE $(new_partition "$1" "$2")" -c "COPY $(new_columns "$1" "$2") FROM STDIN"
	log "copied $1 $2 in $((SECONDS - start))s"
}

job_verify() {
	local out=$WORKDIR/verify/$1_$2 query
	query=$(old_select "$1" "$2")
	oldq -c "$(per_day "$query")" > "$out.old"
	newq -c "$(per_day "$(new_select "$1" "$2")")" > "$out.new"
	if cmp -s "$out.old" "$out.new"; then
		log "verified $1 $2: $(awk -F '\t' '{n += $2} END {print n + 0}' "$out.new") rows"
	else
		log "MISMATCH $1 $2:"
		diff "$out.old" "$out.new" >&2 || true
		return 1
	fi
}

# run_jobs NAME runs "NAME KIND MONTH" for both kinds and every hot month, JOBS at a time. The months are read before
# the loop: a for loop ignores the status of the command that lists its words, and run_jobs runs where set -e does not
# apply (run_jobs _verify || ...).
run_jobs() {
	local months m kind
	months=$(hot_months) || { log "cannot list the hot months"; return 1; }
	for m in $months; do
		for kind in reports extras; do echo "$kind $m"; done
	done | xargs -P "$JOBS" -n 2 "$0" "$1"
}

# require_new_schema stops unless NEW_DSN has the schema of schema.sql: the steps after schema empty tables there
require_new_schema() {
	local relkind
	relkind=$(newq -c "SELECT relkind FROM pg_class WHERE oid = to_regclass('drop_reports')")
	if [[ $relkind != p ]]; then
		echo "NEW_DSN does not have the schema of schema.sql (a partitioned drop_reports): run the schema step first" >&2
		exit 1
	fi
}

# check_coldstore stops unless the cold store manifest lists every month before HOT_FROM (see COLDSTORE_MANIFEST)
check_coldstore() {
	if [[ $COLDSTORE_MANIFEST == skip ]]; then
		log "COLDSTORE_MANIFEST=skip: not checking that the cold store holds the months before $HOT_FROM"
		return
	fi
	[[ -n $COLDSTORE_MANIFEST ]] || { echo "COLDSTORE_MANIFEST is required (the cold store's manifest.json, or skip)" >&2; exit 2; }
	[[ -r $COLDSTORE_MANIFEST ]] || { echo "cannot read COLDSTORE_MANIFEST $COLDSTORE_MANIFEST" >&2; exit 2; }
	local oldest pre missing
	oldest=$(oldq -c "SELECT coalesce(to_char(min(created_at), 'YYYY-MM'), '') FROM drop_reports WHERE created_at >= '2019-05-01 00:00:00+00'")
	# the reports before the coldstore epoch are in no month of the cold store but in its "pre" archive (those before
	# HOT_FROM, should it be earlier, as the hot copy has the others)
	pre=$(oldq -c "SELECT EXISTS (SELECT FROM drop_reports
		WHERE created_at < least(timestamptz '2019-05-01 00:00:00+00', timestamptz '$HOT_FROM-01 00:00:00+00'))")
	# parsed by psql, so the script needs no JSON tool; read with \set (the manifest can be larger than an argument)
	missing=$(oldq -v path="$COLDSTORE_MANIFEST" -v hot_from="$HOT_FROM" -v oldest="$oldest" -v pre="$pre" <<-'SQL'
		\set manifest `cat -- :'path'`
		WITH entries AS (
			SELECT m ->> 'month' AS month FROM jsonb_array_elements((:'manifest')::jsonb -> 'months') m),
		months AS (SELECT month FROM entries WHERE month ~ '^[0-9]{4}-[0-9]{2}$'),
		want AS (
			SELECT 'pre' AS month WHERE :'pre'::boolean
			UNION ALL
			SELECT to_char(m, 'YYYY-MM') FROM generate_series(
				(least((SELECT min(month) FROM months), nullif(:'oldest', ''), :'hot_from') || '-01')::timestamp,
				(:'hot_from' || '-01')::timestamp - interval '1 month', interval '1 month') m)
		SELECT coalesce(string_agg(month, ' ' ORDER BY month = 'pre' DESC, month), '') FROM want
		WHERE month NOT IN (SELECT month FROM entries WHERE month IS NOT NULL)
	SQL
	)
	if [[ -n $missing ]]; then
		echo "the cold store manifest $COLDSTORE_MANIFEST lacks months before HOT_FROM $HOT_FROM: $missing" >&2
		exit 1
	fi
	log "the cold store holds every month before $HOT_FROM"
}

step_schema() {
	# every query whose output decides what to do is read on its own, so that set -e stops on its failure instead of
	# taking it for an empty output
	local months existing m last i
	months=$(hot_months)
	if [[ -z $months ]]; then
		echo "no hot month: HOT_FROM $HOT_FROM is after the latest report and the current month" >&2
		exit 1
	fi
	check_coldstore
	existing=$(newq -c "SELECT to_regclass('drop_reports')")
	if [[ -n $existing ]]; then
		require_new_schema
		log "the new database already has schema.sql: creating the missing partitions only"
	else
		newq -1 -f "$here/schema.sql"
	fi
	for m in $months; do
		newq -c "SELECT create_report_partitions(date '$m-01')" > /dev/null
		last=$m
	done
	# the months after: the first reports after the window may already belong to the next one, and the later ones
	# leave time to create the partitions of the months after them
	for ((i = 1; i <= RUNWAY_MONTHS; i++)); do
		newq -c "SELECT create_report_partitions((date '$last-01' + interval '$i month')::date)" > /dev/null
	done
}

step_small() {
	require_new_schema
	# emptied first, so that the step can run again
	newq -c "DO \$\$ BEGIN EXECUTE (SELECT 'TRUNCATE ' || string_agg(c.oid::regclass::text, ', ') FROM pg_class c
		WHERE c.relkind = 'r' AND NOT c.relispartition AND c.relnamespace = current_schema()::text::regnamespace); END \$\$"
	rm -rf "$WORKDIR/small"
	pg_dump "$OLD_DSN" -Fd -j "$JOBS" --data-only --schema=public \
		--exclude-table-data=public.drop_reports --exclude-table-data=public.drop_report_extras -f "$WORKDIR/small"
	pg_restore -d "$NEW_DSN" -j "$JOBS" --data-only --no-owner --no-privileges --exit-on-error "$WORKDIR/small"
}

step_hot() {
	require_new_schema
	local months
	months=$(hot_months)
	log "hot months ${months//$'\n'/ }"
	run_jobs _copy
}

step_post() {
	require_new_schema
	# one transaction, so indexes.sql is either all there or not at all: its last statement is the marker
	local marker
	marker=$(newq -c "SELECT count(*) FROM pg_constraint WHERE conname = 'trend_elements_stage_id_fkey'")
	if [[ $marker == 1 ]]; then
		log "the new database already has indexes.sql"
	else
		newq -1 -f "$here/indexes.sql"
	fi
	newq -c "ANALYZE"
	# Every sequence of the new database is set to the greatest of its value in the old database and the highest
	# value of the columns that take their default from it, and for drop_reports_report_id_seq also of the highest
	# report id of the old database.
	{
		oldq -c "COPY (SELECT sequencename, last_value FROM pg_sequences WHERE schemaname = 'public' AND last_value IS NOT NULL) TO STDOUT"
		oldq -c "COPY (SELECT 'drop_reports_report_id_seq', max(report_id) FROM drop_reports HAVING max(report_id) IS NOT NULL) TO STDOUT"
	} > "$WORKDIR/sequences.tsv"
	newq -v ON_ERROR_STOP=1 <<-SQL
		CREATE TEMPORARY TABLE old_sequences (name text, value bigint);
		\copy old_sequences FROM '$WORKDIR/sequences.tsv'
		DO \$\$
		DECLARE
			s record;
			c record;
			v bigint;
			col_max bigint;
		BEGIN
			FOR s IN SELECT seq.oid::regclass AS seq, seq.relname AS name FROM pg_class seq
					WHERE seq.relkind = 'S' AND seq.relnamespace = current_schema()::text::regnamespace LOOP
				SELECT max(value) INTO v FROM old_sequences WHERE name = s.name;
				FOR c IN SELECT t.oid::regclass AS tbl, a.attname AS col
						FROM pg_attrdef d
						JOIN pg_class t ON t.oid = d.adrelid AND t.relkind IN ('r', 'p') AND NOT t.relispartition
						JOIN pg_attribute a ON a.attrelid = d.adrelid AND a.attnum = d.adnum
						WHERE pg_get_expr(d.adbin, d.adrelid) = format('nextval(%L::regclass)', s.seq) LOOP
					EXECUTE format('SELECT max(%I) FROM %s', c.col, c.tbl) INTO col_max;
					v := greatest(v, col_max);
				END LOOP;
				IF v IS NOT NULL AND v >= (SELECT seqmin FROM pg_sequence WHERE seqrelid = s.seq) THEN
					PERFORM setval(s.seq, v);
					RAISE NOTICE 'sequence % set to %', s.seq, v;
				END IF;
			END LOOP;
		END
		\$\$;
	SQL
}

step_verify() {
	mkdir -p "$WORKDIR/verify"
	local failed=0 tables t old new
	run_jobs _verify || failed=1
	# read before the loop, whose words would otherwise hide the failure as no table at all
	if ! tables=$(newq -c "SELECT c.relname FROM pg_class c WHERE c.relkind = 'r' AND NOT c.relispartition
			AND c.relnamespace = current_schema()::text::regnamespace AND c.relname NOT LIKE 'pg\_%' ORDER BY 1"); then
		log "cannot list the tables of the new database"
		failed=1
	fi
	for t in $tables; do
		if ! old=$(oldq -c "SELECT count(*) FROM $t") || ! new=$(newq -c "SELECT count(*) FROM $t"); then
			log "cannot count the rows of $t"
			failed=1
		elif [[ $old != "$new" ]]; then
			log "MISMATCH $t: $old rows in the old database, $new in the new one"
			failed=1
		fi
	done
	if [[ $failed == 1 ]]; then
		log "verification FAILED, outputs in $WORKDIR/verify"
		exit 1
	fi
	log "verification passed"
}

case ${1:-} in
_copy) shift; job_copy "$@"; exit ;;
_verify) shift; job_verify "$@"; exit ;;
esac

steps=("$@")
[[ ${#steps[@]} -gt 0 ]] || steps=(schema small hot post verify)
for step in "${steps[@]}"; do
	case $step in
	schema | small | hot | post | verify) ;;
	*) echo "unknown step '$step'" >&2; exit 2 ;;
	esac
done
log "workdir $WORKDIR"
for step in "${steps[@]}"; do
	start=$SECONDS
	log "step $step"
	"step_$step"
	log "step $step done in $((SECONDS - start))s"
done

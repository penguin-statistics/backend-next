#!/usr/bin/env bash
# Creates the scratch database of the report consumer benchmark (internal/workers/reportwkr/consumerbench_test.go):
# the schema of a restore of production, the reference tables the consumer reads, synthetic accounts for the foreign key
# of drop_reports, and a sample of the shapes of recent reports (stage, server, times, source, version, pattern; no
# account or IP) that the fixture generator draws tasks from.
#
# The source database is only read. The benchmark writes drop_reports, drop_report_extras and, for a pattern it has not
# seen, drop_patterns and drop_pattern_elements of the scratch database, which this script drops first when it exists.
#
# Env (defaults for the local restore of the maintainers):
#   PGPORT=5434 SOURCE_DB=penguinprod BENCH_DB=penguin_cpubench BENCH_ROLE=cpubench BENCH_PASSWORD=cpubench
#   PSQL="sudo -n -u postgres psql" PG_DUMP="sudo -n -u postgres /usr/lib/postgresql/14/bin/pg_dump"
#   SAMPLE_BEFORE='2026-10-20 06:00+00' SAMPLE_SIZE=200000 ACCOUNTS=5000
set -euo pipefail
cd /tmp # psql as postgres cannot read the working directory under a home directory

PGPORT=${PGPORT:-5434}
SOURCE_DB=${SOURCE_DB:-penguinprod}
BENCH_DB=${BENCH_DB:-penguin_cpubench}
BENCH_ROLE=${BENCH_ROLE:-cpubench}
BENCH_PASSWORD=${BENCH_PASSWORD:-cpubench}
PSQL=${PSQL:-sudo -n -u postgres psql}
PG_DUMP=${PG_DUMP:-sudo -n -u postgres /usr/lib/postgresql/14/bin/pg_dump}
SAMPLE_BEFORE=${SAMPLE_BEFORE:-2026-10-20 06:00+00}
SAMPLE_SIZE=${SAMPLE_SIZE:-200000}
ACCOUNTS=${ACCOUNTS:-5000}

if [[ "$BENCH_DB" == "$SOURCE_DB" ]]; then
	echo "BENCH_DB must not be the source database" >&2
	exit 1
fi

src() { $PSQL -p "$PGPORT" -d "$SOURCE_DB" -v ON_ERROR_STOP=1 -X "$@"; }
dst() { $PSQL -p "$PGPORT" -d "$BENCH_DB" -v ON_ERROR_STOP=1 -X "$@"; }

$PSQL -p "$PGPORT" -d postgres -v ON_ERROR_STOP=1 -X -q <<SQL
DROP DATABASE IF EXISTS $BENCH_DB;
DO \$\$ BEGIN
	IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '$BENCH_ROLE') THEN
		CREATE ROLE $BENCH_ROLE LOGIN PASSWORD '$BENCH_PASSWORD';
	END IF;
END \$\$;
CREATE DATABASE $BENCH_DB OWNER $BENCH_ROLE;
SQL

echo "schema of $SOURCE_DB"
$PG_DUMP -p "$PGPORT" --schema-only --no-owner --no-privileges "$SOURCE_DB" | dst -q >/dev/null
# a publication of the managed database the restore came from; the benchmark has no use for it
dst -qAtc "SELECT 'DROP PUBLICATION ' || quote_ident(pubname) FROM pg_publication" | dst -q >/dev/null

# in foreign key order
for t in zones stages items time_ranges drop_infos reject_rules activities properties drop_patterns drop_pattern_elements; do
	src -qc "\\copy (SELECT * FROM $t) TO STDOUT" | dst -qc "\\copy $t FROM STDIN"
	echo "$t: $(dst -Atc "SELECT count(*) FROM $t") rows"
done

# synthetic accounts 1..ACCOUNTS: drop_reports.account_id references accounts
dst -qc "INSERT INTO accounts (account_id, penguin_id, weight, created_at)
	SELECT i, 'bench' || i, 1, now() FROM generate_series(1, $ACCOUNTS) AS i"

# the shapes of the latest reports before SAMPLE_BEFORE
dst -qc "CREATE TABLE consumerbench_report_sample (
	ark_stage_id text NOT NULL, server text NOT NULL, times integer NOT NULL, source_name text, version text,
	pattern_id bigint NOT NULL, ip_family integer NOT NULL, created_at timestamptz NOT NULL)"
# \copy takes a single line
sample=$(tr '\n\t' '  ' <<SQL
SELECT st.ark_stage_id, r.server, r.times, r.source_name, r.version, r.pattern_id, family(e.ip), r.created_at
FROM (SELECT * FROM drop_reports WHERE created_at < '$SAMPLE_BEFORE' ORDER BY created_at DESC LIMIT $SAMPLE_SIZE) r
JOIN stages st USING (stage_id)
JOIN drop_report_extras e USING (report_id)
SQL
)
src -qc "\\copy ($sample) TO STDOUT" | dst -qc "\\copy consumerbench_report_sample FROM STDIN"
echo "consumerbench_report_sample: $(dst -Atc "SELECT count(*) FROM consumerbench_report_sample") rows"

# the sequences after the copied rows
dst -qAtc "SELECT format('SELECT setval(%L, max(%I)) FROM %I HAVING max(%I) IS NOT NULL;',
		pg_get_serial_sequence(table_name, column_name), column_name, table_name, column_name)
	FROM information_schema.columns
	WHERE table_schema = 'public' AND pg_get_serial_sequence(table_name, column_name) IS NOT NULL" | dst -q >/dev/null

dst -qc "GRANT ALL ON ALL TABLES IN SCHEMA public TO $BENCH_ROLE;
	GRANT ALL ON ALL SEQUENCES IN SCHEMA public TO $BENCH_ROLE;
	GRANT USAGE, CREATE ON SCHEMA public TO $BENCH_ROLE"
dst -qc "VACUUM ANALYZE"
echo "done: postgres://$BENCH_ROLE:$BENCH_PASSWORD@127.0.0.1:$PGPORT/$BENCH_DB?sslmode=disable"

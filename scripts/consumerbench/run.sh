#!/usr/bin/env bash
# Runs the report consumer benchmark (internal/workers/reportwkr/consumerbench_test.go), see README.md.
#
#   scripts/consumerbench/run.sh bench [variant...]   interleaved runs of the variants, then benchstat of them
#   scripts/consumerbench/run.sh profile [variant]    CPU and allocation profiles of ingest, and their attribution
#
# Variants (default: prod logoff traceoff off): prod (logs to a counting io.Discard, tracing as in production),
# devnull (logs to /dev/null), logoff, traceoff, off (both off), trace1 (every task sampled).
#
# Env: OUT (output directory, default a new temporary one), COUNT (runs per variant, default 10), BENCHTIME (tasks per
# run, default 2000x), PROFILE_BENCHTIME (default 10000x), BENCH (default BenchmarkConsumer), the CONSUMERBENCH_* of
# the harness for the databases. A line per run in $OUT/runs.log records the load average and the queries other
# sessions had active on the cluster when it started, to tell noisy runs apart.
set -euo pipefail

ROOT=$(git -C "$(dirname "$0")" rev-parse --show-toplevel)
PKG=$ROOT/internal/workers/reportwkr
OUT=${OUT:-$(mktemp -d -t consumerbench.XXXXXX)}
COUNT=${COUNT:-10}
BENCHTIME=${BENCHTIME:-2000x}
PROFILE_BENCHTIME=${PROFILE_BENCHTIME:-10000x}
BENCH=${BENCH:-BenchmarkConsumer}
PGPORT=${PGPORT:-5434}
PSQL=${PSQL:-sudo -n -u postgres psql}
mkdir -p "$OUT"
OUT=$(cd "$OUT" && pwd) # the binary is built from the repository root and run from the package

variant_env() {
	case $1 in
	prod) echo "CONSUMERBENCH_LOG=discard CONSUMERBENCH_TRACE=prod" ;;
	devnull) echo "CONSUMERBENCH_LOG=devnull CONSUMERBENCH_TRACE=prod" ;;
	logoff) echo "CONSUMERBENCH_LOG=off CONSUMERBENCH_TRACE=prod" ;;
	traceoff) echo "CONSUMERBENCH_LOG=discard CONSUMERBENCH_TRACE=off" ;;
	off) echo "CONSUMERBENCH_LOG=off CONSUMERBENCH_TRACE=off" ;;
	trace1) echo "CONSUMERBENCH_LOG=discard CONSUMERBENCH_TRACE=prod CONSUMERBENCH_TRACE_RATE=1" ;;
	*) echo "unknown variant $1" >&2; exit 1 ;;
	esac
}

foreign_queries() {
	(cd /tmp && $PSQL -p "$PGPORT" -d postgres -XAtc "SELECT count(*) FROM pg_stat_activity
		WHERE state = 'active' AND pid <> pg_backend_pid() AND application_name <> 'penguin-consumerbench'" 2>/dev/null) || echo "?"
}

echo "building the test binary into $OUT"
(cd "$ROOT" && go test -c -tags consumerbench -o "$OUT/reportwkr.test" ./internal/workers/reportwkr)

# run VARIANT ARGS...: one process of the test binary, in the package directory for its testdata
run() {
	local variant=$1
	shift
	echo "$(date -Is) $variant load=$(cut -d' ' -f1-3 /proc/loadavg) foreign_pg_active=$(foreign_queries)" >>"$OUT/runs.log"
	(cd "$PKG" && env $(variant_env "$variant") "$OUT/reportwkr.test" -test.run '^$' "$@")
}

case ${1:-bench} in
bench)
	shift || true
	variants=("$@")
	[[ ${#variants[@]} -gt 0 ]] || variants=(prod logoff traceoff off)
	for i in $(seq "$COUNT"); do
		for v in "${variants[@]}"; do
			run "$v" -test.bench "$BENCH" -test.benchtime "$BENCHTIME" -test.count 1 | grep '^Benchmark' | tee -a "$OUT/$v.txt"
		done
	done
	files=()
	for v in "${variants[@]}"; do files+=("$v.txt"); done
	(cd "$OUT" && go run golang.org/x/perf/cmd/benchstat@latest "${files[@]}" | tee benchstat.txt)
	;;
profile)
	v=${2:-prod}
	# two runs: the finer memory profile rate of the second would show in the CPU profile (runtime.mProf_Malloc).
	# The profiles cover the warm-up of the harness too (the same code path, a task per fixture).
	run "$v" -test.bench "$BENCH/ingest" -test.benchtime "$PROFILE_BENCHTIME" -test.count 1 \
		-test.cpuprofile "$OUT/cpu.pprof" | grep '^Benchmark'
	run "$v" -test.bench "$BENCH/ingest" -test.benchtime "$PROFILE_BENCHTIME" -test.count 1 \
		-test.memprofile "$OUT/mem.pprof" -test.memprofilerate 16384 | grep '^Benchmark'
	cd "$OUT"
	# the consumer path: the samples under ingestPreprocess (without the scheduler and the background GC)
	focus=(-focus 'reportwkr.\(\*Worker\).ingestPreprocess')
	go tool pprof -top -cum -nodecount 40 "${focus[@]}" reportwkr.test cpu.pprof >cpu-top-cum.txt
	go tool pprof -top -nodecount 40 "${focus[@]}" reportwkr.test cpu.pprof >cpu-top-flat.txt
	go tool pprof -top -cum -nodecount 40 "${focus[@]}" -sample_index alloc_objects reportwkr.test mem.pprof >alloc-top-cum.txt
	go tool pprof -top -nodecount 40 "${focus[@]}" -sample_index alloc_objects reportwkr.test mem.pprof >alloc-top-flat.txt
	go tool pprof -traces reportwkr.test cpu.pprof | python3 "$ROOT/scripts/consumerbench/attribute.py" cpu | tee attribution-cpu.txt
	go tool pprof -traces -sample_index alloc_objects reportwkr.test mem.pprof | python3 "$ROOT/scripts/consumerbench/attribute.py" allocs | tee attribution-allocs.txt
	go tool pprof -traces -sample_index alloc_space reportwkr.test mem.pprof | python3 "$ROOT/scripts/consumerbench/attribute.py" bytes | tee attribution-bytes.txt
	echo "profiles and reports in $OUT"
	;;
*)
	echo "usage: $0 bench [variant...] | profile [variant]" >&2
	exit 1
	;;
esac

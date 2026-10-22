#!/usr/bin/env bash
# Tests of backfill.sh's orchestration (markers, resuming, upload checks), with a fake app binary and an rclone stub:
# no database, DuckDB or bucket needed. Run: scripts/coldstore/backfill_test.sh (needs bash, GNU coreutils, jq, flock).
set -euo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
BACKFILL=${BACKFILL:-$HERE/backfill.sh}
ROOT=$(mktemp -d)
trap 'rm -rf "$ROOT"' EXIT
FAILED=0

# --- fakes ---------------------------------------------------------------------------------------------------------

mkdir -p "$ROOT/bin"
# The app binary: coldstore-export writes the files of a month (content: month, kind and $FAKE_GEN) and records them
# in the manifest; coldstore-verify checks them against the manifest and, without --no-source, fails with status 65
# for the months in $FAKE_SOURCE_FAIL. Every call is appended to $FAKE_LOG.
cat >"$ROOT/bin/backend" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
shift # run-script
cmd=$1
shift
key='' dims=0 out='' replace=0 source=1
while [[ $# -gt 0 ]]; do
	case $1 in
	--month) key=$2; shift ;;
	--dims) dims=1 ;;
	--out | --dir) out=$2; shift ;;
	--replace) replace=1 ;;
	--no-source) source=0 ;;
	--grace | --work-dir | --threads | --memory-limit) shift ;;
	esac
	shift
done
[[ $dims == 1 ]] && key=dims
echo "$cmd $key source=$source" >>"$FAKE_LOG"
manifest=$out/manifest.json
paths() {
	if [[ $1 == dims ]]; then
		echo dims/drop_pattern_elements.parquet
	else
		printf '%s\n' "drop_reports/drop_reports_$1.parquet" "drop_report_extras/extras_$1.parquet" \
			"recruit/recruit_$1.parquet" "rollup/rollup_$1.parquet"
	fi
}
case $cmd in
coldstore-export)
	exec 9>"$manifest.lock"
	flock 9
	[[ -f $manifest ]] || echo '{"schema_version":1,"dims":[],"months":[]}' >"$manifest"
	if [[ $key != dims && $replace == 0 ]] && jq -e --arg m "$key" '.months[] | select(.month == $m)' "$manifest" >/dev/null; then
		echo "month $key is already archived" >&2
		exit 1
	fi
	files='[]'
	for p in $(paths "$key"); do
		mkdir -p "$out/$(dirname "$p")"
		echo "$key $p generation ${FAKE_GEN:-1}" >"$out/$p"
		sha=$(sha256sum "$out/$p" | cut -d' ' -f1)
		files=$(jq -c --arg p "$p" --arg s "$sha" --argjson b "$(stat -c %s "$out/$p")" '. + [{path: $p, sha256: $s, bytes: $b, rows: 1}]' <<<"$files")
	done
	if [[ $key == dims ]]; then
		jq --argjson f "$files" '.dims = $f' "$manifest" >"$manifest.tmp"
	else
		jq --arg m "$key" --argjson f "$files" '.months = ([.months[] | select(.month != $m)] + [{month: $m, files: $f}] | sort_by(.month))' \
			"$manifest" >"$manifest.tmp"
	fi
	mv "$manifest.tmp" "$manifest"
	;;
coldstore-verify)
	if [[ $key == dims ]]; then
		lines=$(jq -r '.dims[] | "\(.sha256)  \(.path)"' "$manifest")
	else
		lines=$(jq -r --arg m "$key" '.months[] | select(.month == $m) | .files[] | "\(.sha256)  \(.path)"' "$manifest")
	fi
	[[ -n $lines ]] || { echo "$key is not in the manifest" >&2; exit 1; }
	(cd "$out" && sha256sum --check --strict --quiet - <<<"$lines") || { echo "$key differs from the manifest" >&2; exit 1; }
	if [[ $source == 1 && " ${FAKE_SOURCE_FAIL:-} " == *" $key "* ]]; then
		echo "$key differs from PostgreSQL" >&2
		exit 65
	fi
	;;
*)
	echo "unknown command $cmd" >&2
	exit 2
	;;
esac
EOF

# rclone: copyto copies, check compares bytes (as the hashes would) and writes the combined report and the summary
# the way rclone does, at NOTICE level, which RCLONE_LOG_LEVEL, RCLONE_QUIET or --log-level lower or raise. With
# STUB_HASHLESS=1 the destination has no hashes: every file present counts as matching, as rclone counts it, and the
# summary says that the hashes could not be checked. A remote "name:path" is the directory
# $STUB_REMOTES/<name>/<endpoint>/path, the endpoint being RCLONE_CONFIG_<NAME>_ENDPOINT if it is set (even empty),
# else RCLONE_S3_ENDPOINT, as rclone takes them; the remote "bk:path" is $STUB_ALIAS_TARGET/path if that is set (an
# alias remote, whose target is in rclone's config file). $STUB_BEFORE_MANIFEST, an executable, runs once just before
# the manifest is copied. Every call is appended to $STUB_LOG.
cat >"$ROOT/bin/rclone" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
echo "$*" >>"$STUB_LOG"
resolve() {
	local p=$1 name var
	if [[ $p == /* || $p != *:* ]]; then
		echo "$p"
		return
	fi
	name=${p%%:*}
	if [[ $name == bk && -n ${STUB_ALIAS_TARGET:-} ]]; then
		echo "$STUB_ALIAS_TARGET/${p#*:}"
		return
	fi
	var=RCLONE_CONFIG_${name^^}_ENDPOINT
	if [[ -n ${!var+set} ]]; then
		endpoint=${!var:-default}
	else
		endpoint=${RCLONE_S3_ENDPOINT:-default}
	fi
	echo "$STUB_REMOTES/$name/$(tr -c 'a-zA-Z0-9\n' _ <<<"$endpoint")/${p#*:}"
}
cmd=$1
shift
case $cmd in
config)
	echo '{}'
	exit 0
	;;
copyto)
	if [[ $1 == */manifest.json && -n ${STUB_BEFORE_MANIFEST:-} && -x $STUB_BEFORE_MANIFEST ]]; then
		"$STUB_BEFORE_MANIFEST"
		rm -f "$STUB_BEFORE_MANIFEST"
	fi
	dst=$(resolve "$2")
	mkdir -p "$(dirname "$dst")"
	cp "$1" "$dst"
	exit 0
	;;
check)
	src=$1 dst=$(resolve "$2")
	shift 2
	list='' report='' level=''
	while [[ $# -gt 0 ]]; do
		case $1 in
		--files-from) list=$2; shift ;;
		--combined) report=$2; shift ;;
		--log-level) level=$2; shift ;;
		esac
		shift
	done
	if [[ -n $level && -n ${RCLONE_QUIET:-} ]]; then
		echo "Can't set -q and --log-level" >&2
		exit 1
	fi
	[[ -n $level ]] || level=${RCLONE_LOG_LEVEL:-}
	[[ -n $level || -z ${RCLONE_QUIET:-} ]] || level=ERROR
	level=${level:-NOTICE}
	notice() { [[ $level == ERROR ]] || echo "NOTICE: $dst: $*" >&2; }
	: >"$report"
	matches=0 differences=0 missing=0 nohash=0
	while read -r p; do
		if [[ ! -f $dst/$p ]]; then
			echo "+ $p" >>"$report"
			missing=$((missing + 1))
		elif [[ ${STUB_HASHLESS:-0} == 1 ]]; then
			echo "= $p" >>"$report"
			matches=$((matches + 1))
			nohash=$((nohash + 1))
		elif cmp -s "$src/$p" "$dst/$p"; then
			echo "= $p" >>"$report"
			matches=$((matches + 1))
		else
			echo "* $p" >>"$report"
			differences=$((differences + 1))
		fi
	done <"$list"
	[[ ${STUB_HASHLESS:-0} == 0 ]] || echo "ERROR : No common hash found - not using a hash for checks" >&2
	((missing == 0)) || notice "$missing files missing"
	notice "$((differences + missing)) differences found"
	((nohash == 0)) || notice "$nohash hashes could not be checked"
	((matches == 0)) || notice "$matches matching files"
	((differences + missing == 0))
	;;
*)
	echo "rclone stub: unknown command $cmd" >&2
	exit 2
	;;
esac
EOF
chmod +x "$ROOT/bin/backend" "$ROOT/bin/rclone"

# --- helpers -------------------------------------------------------------------------------------------------------

MONTHS=(pre 2019-05)

# setup starts a test: a fresh cold storage directory, state, remotes and logs, and a clean environment.
setup() {
	T=$ROOT/$1
	mkdir -p "$T"
	OUT=$T/out
	export FAKE_LOG=$T/app.log STUB_LOG=$T/rclone.log STUB_REMOTES=$T/remotes
	unset FAKE_SOURCE_FAIL FAKE_GEN STUB_HASHLESS STUB_ALIAS_TARGET STUB_BEFORE_MANIFEST RCLONE_LOG_LEVEL RCLONE_QUIET \
		RCLONE_S3_ENDPOINT COLDSTORE_REMOTE COLDSTORE_S3_BUCKET COLDSTORE_S3_ACCESS_KEY_ID COLDSTORE_S3_SECRET_ACCESS_KEY \
		COLDSTORE_S3_ENDPOINT
	: >"$FAKE_LOG"
	: >"$STUB_LOG"
}

# backfill runs backfill.sh with the fakes; its output goes to $T/run.<n>.log.
RUNS=0
backfill() {
	RUNS=$((RUNS + 1))
	LAST=$T/run.$RUNS.log
	PATH=$ROOT/bin:$PATH "$BACKFILL" --bin "$ROOT/bin/backend" --out "$OUT" --jobs 2 "$@" >"$LAST" 2>&1
}

fail() {
	echo "FAIL [$TEST]: $*" >&2
	[[ -z ${LAST:-} ]] || sed 's/^/    /' "$LAST" >&2
	FAILED=1
}

expect_ok() { backfill "$@" || fail "backfill $* failed"; }
expect_fail() { if backfill "$@"; then fail "backfill $* succeeded"; fi; }

# remote_has_all DIR checks that DIR holds the manifest and every file it lists, identical to the local ones.
remote_has_all() {
	local dir=$1 p
	cmp -s "$OUT/manifest.json" "$dir/manifest.json" || { fail "$dir: manifest.json missing or different"; return; }
	for p in $(jq -r '.dims[].path, .months[].files[].path' "$OUT/manifest.json"); do
		cmp -s "$OUT/$p" "$dir/$p" || fail "$dir: $p missing or different"
	done
}

count() { grep -c -- "$1" "$2" || true; }

# --- tests ---------------------------------------------------------------------------------------------------------

TEST="upload, then resume"
setup upload
export COLDSTORE_REMOTE=$T/remote-a
expect_ok --upload "${MONTHS[@]}"
remote_has_all "$T/remote-a"
: >"$STUB_LOG"
expect_ok --upload "${MONTHS[@]}"
[[ $(count copyto "$STUB_LOG") == 1 ]] || fail "resume uploaded data files again: $(cat "$STUB_LOG")"
[[ $(count "coldstore-export" "$FAKE_LOG") == 3 ]] || fail "resume exported again: $(cat "$FAKE_LOG")"

TEST="corrupted after verification: not uploaded"
setup corrupt
expect_ok "${MONTHS[@]}"
echo corrupted >>"$OUT/drop_reports/drop_reports_2019-05.parquet"
export COLDSTORE_REMOTE=$T/remote-a
expect_fail --upload "${MONTHS[@]}"
grep -q 'differ from their sha256 in the manifest' "$OUT.state/logs/2019-05.log" || fail "no local sha256 failure in the log"
[[ ! -e $T/remote-a/drop_reports/drop_reports_2019-05.parquet ]] || fail "the corrupted file was uploaded"
[[ ! -e $T/remote-a/manifest.json ]] || fail "the manifest was uploaded"

TEST="new destination: everything uploaded again"
setup destination
export COLDSTORE_REMOTE=$T/remote-a
expect_ok --upload "${MONTHS[@]}"
export COLDSTORE_REMOTE=$T/remote-b
expect_ok --upload "${MONTHS[@]}"
remote_has_all "$T/remote-b"
# the built-in S3 remote: another endpoint is another destination
unset COLDSTORE_REMOTE
export COLDSTORE_S3_BUCKET=bucket COLDSTORE_S3_ACCESS_KEY_ID=key COLDSTORE_S3_SECRET_ACCESS_KEY=secret
export COLDSTORE_S3_ENDPOINT=http://one.example
expect_ok --upload "${MONTHS[@]}"
remote_has_all "$STUB_REMOTES/coldstore/http___one_example/bucket/coldstore/v1"
export COLDSTORE_S3_ENDPOINT=http://two.example
expect_ok --upload "${MONTHS[@]}"
remote_has_all "$STUB_REMOTES/coldstore/http___two_example/bucket/coldstore/v1"
! grep -rq secret "$OUT.state/uploaded" || fail "the secret key is in the state"
# rclone's own RCLONE_S3_ENDPOINT does not override the built-in remote's (empty) endpoint
export RCLONE_S3_ENDPOINT=http://three.example
unset COLDSTORE_S3_ENDPOINT
expect_ok --upload "${MONTHS[@]}"
[[ ! -e $STUB_REMOTES/coldstore/http___three_example ]] || fail "RCLONE_S3_ENDPOINT changed the built-in remote's endpoint"
remote_has_all "$STUB_REMOTES/coldstore/default/bucket/coldstore/v1"

# The markers are keyed by what destination_identity sees of a named remote; when something else changes where it
# writes, the markers say uploaded although the new destination has nothing: the manifest must not be uploaded alone.
TEST="alias remote retargeted: manifest not uploaded alone"
setup alias
export COLDSTORE_REMOTE=bk:cs STUB_ALIAS_TARGET=$T/A
expect_ok --upload "${MONTHS[@]}"
remote_has_all "$T/A/cs"
export STUB_ALIAS_TARGET=$T/B
expect_fail --upload "${MONTHS[@]}"
grep -q 'markers dropped' "$LAST" || fail "no failure about the markers"
[[ ! -e $T/B/cs/manifest.json ]] || fail "the manifest was uploaded without the files"
expect_ok --upload "${MONTHS[@]}"
remote_has_all "$T/B/cs"

TEST="RCLONE_S3_ENDPOINT changed: manifest not uploaded alone"
setup s3env
export COLDSTORE_REMOTE=bk2:bucket/cs RCLONE_S3_ENDPOINT=http://one.example
expect_ok --upload "${MONTHS[@]}"
remote_has_all "$STUB_REMOTES/bk2/http___one_example/bucket/cs"
unset RCLONE_S3_ENDPOINT
expect_fail --upload "${MONTHS[@]}"
[[ ! -e $STUB_REMOTES/bk2/default/bucket/cs/manifest.json ]] || fail "the manifest was uploaded without the files"
expect_ok --upload "${MONTHS[@]}"
remote_has_all "$STUB_REMOTES/bk2/default/bucket/cs"

TEST="patched while the manifest is uploaded: the bucket's manifest matches its files"
setup race
export COLDSTORE_REMOTE=$T/remote-a STUB_BEFORE_MANIFEST=$T/patch.sh
cat >"$STUB_BEFORE_MANIFEST" <<EOF
#!/usr/bin/env bash
FAKE_GEN=2 "$ROOT/bin/backend" run-script coldstore-export --month 2019-05 --replace --out "$OUT"
EOF
chmod +x "$STUB_BEFORE_MANIFEST"
expect_fail --upload "${MONTHS[@]}"
grep -q 'manifest changed during the upload' "$LAST" || fail "no failure about the changed manifest"
for p in $(jq -r '.dims[].path, .months[].files[].path' "$T/remote-a/manifest.json"); do
	want=$(jq -r --arg p "$p" '.dims[], .months[].files[] | select(.path == $p) | .sha256' "$T/remote-a/manifest.json")
	[[ $(sha256sum "$T/remote-a/$p" | cut -d' ' -f1) == "$want" ]] || fail "the bucket's manifest does not match its $p"
done
expect_ok --upload "${MONTHS[@]}"
remote_has_all "$T/remote-a"
grep -q 'generation 2' "$T/remote-a/drop_reports/drop_reports_2019-05.parquet" || fail "the patched file was not uploaded"

TEST="failed source comparison stays failed on resume"
setup source
export FAKE_SOURCE_FAIL=2019-05
expect_fail "${MONTHS[@]}"
expect_fail "${MONTHS[@]}"
[[ $(count "coldstore-export 2019-05" "$FAKE_LOG") == 1 ]] || fail "exported again: $(cat "$FAKE_LOG")"
[[ $(count "coldstore-verify 2019-05 source=1" "$FAKE_LOG") == 2 ]] || fail "the resume did not compare with PostgreSQL again: $(cat "$FAKE_LOG")"
[[ ! -e $OUT.state/verified/2019-05 ]] || fail "2019-05 marked verified"
# the operator opts out explicitly: verified against the manifest, the comparison still pending
expect_ok --no-verify-source "${MONTHS[@]}"
grep -q 'still pending' "$OUT.state/logs/2019-05.log" || fail "no pending warning"
expect_fail "${MONTHS[@]}"
# PostgreSQL agrees: verified without exporting again
unset FAKE_SOURCE_FAIL
expect_ok "${MONTHS[@]}"
[[ $(count "coldstore-export 2019-05" "$FAKE_LOG") == 1 ]] || fail "exported again: $(cat "$FAKE_LOG")"
[[ ! -e $OUT.state/pending-source/2019-05 ]] || fail "the comparison is still pending"
# a month that was archived without this state is still only verified against the manifest
rm -rf "$OUT.state"
expect_ok "${MONTHS[@]}"
[[ $(tail -n 3 "$FAKE_LOG" | grep -c 'source=0') == 3 ]] || fail "an archived month was compared with PostgreSQL: $(cat "$FAKE_LOG")"

TEST="destination without hashes, quiet rclone"
for quiet in RCLONE_LOG_LEVEL=ERROR RCLONE_QUIET=true; do
	setup "hashless-${quiet%%=*}"
	export COLDSTORE_REMOTE=$T/remote-a STUB_HASHLESS=1 "${quiet?}"
	expect_fail --upload "${MONTHS[@]}"
	grep -q 'has no MD5' "$OUT.state/logs/dims.log" || fail "$quiet: no missing-hash failure in the log"
	[[ ! -e $T/remote-a/manifest.json ]] || fail "$quiet: the manifest was uploaded"
done

TEST="patched month: verified and uploaded again"
setup patched
export COLDSTORE_REMOTE=$T/remote-a
expect_ok --upload "${MONTHS[@]}"
# what coldstore-patch does: new files and their manifest entry
FAKE_GEN=2 PATH=$ROOT/bin:$PATH "$ROOT/bin/backend" run-script coldstore-export --month 2019-05 --replace --out "$OUT"
: >"$FAKE_LOG"
expect_ok --upload "${MONTHS[@]}"
[[ $(count "coldstore-verify 2019-05 source=0" "$FAKE_LOG") == 1 ]] || fail "the patched month was not verified again: $(cat "$FAKE_LOG")"
[[ $(count "coldstore-export" "$FAKE_LOG") == 0 ]] || fail "exported again: $(cat "$FAKE_LOG")"
remote_has_all "$T/remote-a"
grep -q 'generation 2' "$T/remote-a/drop_reports/drop_reports_2019-05.parquet" || fail "the patched file was not uploaded"

TEST="dims exported again when months are"
setup dims
expect_ok pre
# a later run that archives more months: the patterns they refer to may be newer than the archived dims
expect_ok pre 2019-05
[[ $(count "coldstore-export dims" "$FAKE_LOG") == 2 ]] || fail "dims not exported again with a new month: $(cat "$FAKE_LOG")"
[[ $(count "coldstore-verify dims source=1" "$FAKE_LOG") == 2 ]] || fail "the new dims were not verified: $(cat "$FAKE_LOG")"
# nothing to export: the dims are kept
expect_ok pre 2019-05
[[ $(count "coldstore-export dims" "$FAKE_LOG") == 2 ]] || fail "dims exported again without a month to export: $(cat "$FAKE_LOG")"
expect_ok --redo 2019-05
[[ $(count "coldstore-export dims" "$FAKE_LOG") == 3 ]] || fail "dims not exported again with --redo: $(cat "$FAKE_LOG")"

if [[ $FAILED == 0 ]]; then
	echo "backfill_test.sh: all tests passed"
fi
exit "$FAILED"

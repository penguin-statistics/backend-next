#!/usr/bin/env bash
# Backfill of the cold storage (#752): exports, verifies and optionally uploads the archived months, a few months at
# a time, with the app binary's run-script commands (the SQL of the monthly archiving). Resumable: a month whose
# files were verified is not exported again, and a month is uploaded again only when its files changed (e.g. after
# run-script coldstore-patch) or the destination did. A month already in the manifest is never exported again
# without --redo: once its partition is dropped, the files are the only copy. The bucket is a write-mostly backup:
# the local files are checked against the manifest's sha256 before and after each upload, and uploads are confirmed
# by comparing hashes with the bucket's, never by downloading the files back. See docs/coldstore.md.
#
# Needs: bash, GNU date, jq, rclone (with --upload), and the environment the app's run-script commands read
# (PENGUIN_V3_POSTGRES_DSN of the database to export from, PENGUIN_V3_COLDSTORE_EXTENSION_DIRECTORY, and the other
# required settings; dummy values are fine for those).
set -euo pipefail

usage() {
	cat <<'EOF'
usage: backfill.sh --bin BIN --out DIR [options] [MONTH...]

Exports, verifies and (with --upload) uploads every month from --from to --to, or the MONTHs given (YYYY-MM or
'pre'), and the drop pattern elements; uploads the manifest last, once every month in it is uploaded.

  --bin BIN             app binary (built with CGO_ENABLED=1, or /app/backend from the image)   [$COLDSTORE_BIN]
  --out DIR             cold storage directory: manifest.json, dims/, drop_reports/, ...
  --state DIR           markers and per-month logs (default: DIR.state, next to --out)
  --from MONTH          first month (default: pre, then 2019-05 on)
  --to MONTH            last month to archive (required unless MONTHs are given)
  --grace-days N        days after its end before a month counts as over (default 3): reports are dated when they
                        are accepted, so a month still gains reports while the report queue drains
  --jobs N              months processed at a time (default 3)
  --threads N           DuckDB threads per month (default 4)
  --memory-limit SIZE   DuckDB memory limit per month (default 4GB)
  --work-dir DIR        DuckDB database and spill files (default: STATE/work); needs a few GB per job
  --no-verify-source    verify the files against the manifest only, not against PostgreSQL again; without it, a
                        month whose comparison with PostgreSQL failed or was interrupted is compared again
  --upload              upload to the bucket (COLDSTORE_S3_* or COLDSTORE_REMOTE, see below), then compare the
                        MD5 of every uploaded file with the bucket's (rclone check, nothing is downloaded)
  --redo                export the months again from PostgreSQL, replacing the archived files; only while
                        PostgreSQL still holds the whole months (refused if it has fewer reports than the archive)

Bucket (rclone, S3 API): COLDSTORE_S3_BUCKET, COLDSTORE_S3_ACCESS_KEY_ID, COLDSTORE_S3_SECRET_ACCESS_KEY,
COLDSTORE_S3_PROVIDER (default AWS), COLDSTORE_S3_REGION (default ap-southeast-1), COLDSTORE_S3_ENDPOINT (default:
none, AWS's endpoint of the region), COLDSTORE_S3_PREFIX (default coldstore/v1), COLDSTORE_S3_STORAGE_CLASS of the
data files (default STANDARD_IA, empty for the server's default; the manifest is always STANDARD). Or
COLDSTORE_REMOTE: any rclone path, e.g. myremote:bucket/coldstore/v1 or a local directory.
EOF
}

die() {
	echo "backfill: $*" >&2
	exit 1
}

next_month() {
	if [[ $1 == pre ]]; then
		echo 2019-05
	else
		date -u -d "$1-01 +1 month" +%Y-%m
	fi
}

valid_month() {
	[[ $1 == pre ]] || { [[ $1 =~ ^[0-9]{4}-[0-9]{2}$ ]] && [[ $(date -u -d "$1-01" +%Y-%m 2>/dev/null) == "$1" ]] && [[ ! $1 < 2019-05 ]]; }
}

# manifest_lines KEY [MANIFEST] prints "sha256  path" of the files of a month (or of the dims, for KEY dims) in the
# manifest (default: OUT's).
manifest_lines() {
	local manifest=${2:-$OUT/manifest.json}
	[[ -f $manifest ]] || return 0
	if [[ $1 == dims ]]; then
		jq -r '.dims[] | "\(.sha256)  \(.path)"' "$manifest"
	else
		jq -r --arg m "$1" '.months[] | select(.month == $m) | .files[] | "\(.sha256)  \(.path)"' "$manifest"
	fi
}

# app runs a run-script command of the binary in a directory of its own, where it writes its ./logs.
app() {
	local key=$1
	shift
	mkdir -p "$STATE/run/$key"
	(cd "$STATE/run/$key" && "$BIN" run-script "$@")
}

# month_over MONTH tells whether MONTH ended at least GRACE_DAYS days ago.
month_over() {
	[[ $1 == pre ]] || (($(date -u +%s) >= $(date -u -d "$(next_month "$1")-01 +$GRACE_DAYS days" +%s)))
}

duckdb_flags() {
	echo --work-dir "$WORK" --threads "$THREADS" --memory-limit "$MEMORY"
}

# process KEY exports, verifies and uploads one month (or the dims, for KEY dims). Its output goes to the month's log.
process() {
	local key=$1 what verify_flags=() lines path archived=0
	if [[ $key == dims ]]; then what=(--dims); else what=(--month "$key"); fi
	[[ $VERIFY_SOURCE == 1 ]] || verify_flags=(--no-source)
	# a month exported by this backfill whose comparison with PostgreSQL has not passed yet (failed or interrupted)
	local pending=$STATE/pending-source/$key
	echo "== $key: start $(date -u +%FT%TZ)"

	lines=$(manifest_lines "$key")
	if [[ -n $lines ]] && all_exist "$OUT" "$lines"; then archived=1; fi
	# the dims are exported again whenever months are, so that they hold the patterns of every month exported
	if [[ $key == dims && $REFRESH_DIMS == 1 ]]; then archived=0; fi
	if [[ $REDO == 0 && $archived == 1 ]] && marker_is "$STATE/verified/$key" "$lines" &&
		[[ $VERIFY_SOURCE == 0 || ! -e $pending ]]; then
		# the marker holds the manifest lines it verified: a month patched since then is verified again
		echo "== $key: already exported and verified"
	elif [[ $REDO == 0 && $archived == 1 ]]; then
		rm -f "$STATE/verified/$key"
		if [[ -e $pending && $VERIFY_SOURCE == 1 ]]; then
			# exported by this backfill, but not compared with PostgreSQL yet: that comparison is still required,
			# without exporting again (--no-verify-source opts out)
			# shellcheck disable=SC2046
			app "$key" coldstore-verify "${what[@]}" --dir "$OUT" $(duckdb_flags)
			rm -f "$pending"
			echo "$lines" >"$STATE/verified/$key"
			echo "== $key: exported earlier, now verified against PostgreSQL"
		else
			# archived, without a marker for these files (e.g. killed before it was written, another --state, a copy
			# rsynced from another machine, or patched since): PostgreSQL may no longer hold the month, so check the
			# files against the manifest only
			# shellcheck disable=SC2046
			app "$key" coldstore-verify "${what[@]}" --dir "$OUT" --no-source $(duckdb_flags)
			echo "$lines" >"$STATE/verified/$key"
			if [[ -e $pending ]]; then
				echo "== $key: verified against the manifest only (--no-verify-source); its comparison with PostgreSQL is still pending"
			else
				echo "== $key: already archived, verified against the manifest"
			fi
		fi
	else
		rm -f "$STATE/verified/$key"
		# temporary files of an export that was killed
		if [[ $key != dims ]]; then
			rm -f "$OUT"/*/*_"$key".parquet.*.tmp
		fi
		local export_flags=()
		if [[ $key != dims ]]; then
			export_flags=(--grace "$((GRACE_DAYS * 24))h")
			# coldstore-export refuses to replace an archived month without it
			[[ $REDO == 0 ]] || export_flags+=(--replace)
		fi
		# the export records the month in the manifest before it is compared with PostgreSQL: until that passes, a
		# resumed run must compare it again rather than take it for an archived month
		[[ $VERIFY_SOURCE == 0 ]] || touch "$pending"
		# shellcheck disable=SC2046
		app "$key" coldstore-export "${what[@]}" "${export_flags[@]}" --out "$OUT" $(duckdb_flags)
		# shellcheck disable=SC2046
		app "$key" coldstore-verify "${what[@]}" --dir "$OUT" "${verify_flags[@]}" $(duckdb_flags)
		[[ $VERIFY_SOURCE == 0 ]] || rm -f "$pending"
		lines=$(manifest_lines "$key")
		[[ -n $lines ]] || die "$key is not in the manifest after its export"
		echo "$lines" >"$STATE/verified/$key"
		echo "== $key: exported and verified"
	fi

	[[ $UPLOAD == 1 ]] || return 0
	# upload markers are kept per destination, and hold the manifest lines that were uploaded and checked
	local marker=$UPLOADED/$key
	if marker_is "$marker" "$lines"; then
		echo "== $key: already uploaded to this destination"
		return 0
	fi
	rm -f "$marker"
	# the bytes uploaded must be the manifest's, whatever happened to the files since they were verified
	local_check "$lines"
	local storage_class=()
	[[ -z $STORAGE_CLASS ]] || storage_class=(--s3-storage-class "$STORAGE_CLASS")
	while read -r _ path; do
		# rclone checks the upload against the file's MD5 (Content-MD5 of the request or of every part)
		rclone copyto "$OUT/$path" "$REMOTE/$path" "${RCLONE_FLAGS[@]}" "${storage_class[@]}"
	done <<<"$lines"
	local list=$STATE/run/$key/upload.list
	while read -r _ path; do echo "$path"; done <<<"$lines" >"$list"
	remote_check "$list"
	# and the files did not change while they were uploaded: the bucket holds the manifest's bytes
	local_check "$lines"
	echo "$lines" >"$marker"
	echo "== $key: uploaded, MD5 checked against the bucket"
}

# marker_is FILE LINES tells whether the marker FILE holds LINES.
marker_is() {
	[[ -f $1 && $(cat "$1") == "$2" ]]
}

# local_check LINES checks the files of LINES ("sha256  path", from the manifest) against their sha256 in the
# manifest. It reads only the local files, so it needs no PostgreSQL.
local_check() {
	(cd "$OUT" && sha256sum --check --strict --quiet - <<<"$1") ||
		die "the local files differ from their sha256 in the manifest: not uploading (verify them with coldstore-verify --no-source)"
}

# remote_check LIST [DIR] compares the files of LIST (paths relative to DIR, default OUT) with their objects in the
# bucket by size and MD5, without downloading anything: rclone takes the bucket's MD5 from a HEAD of each object (S3: the ETag, or for a
# multipart upload the MD5 that rclone stored in the object's metadata). Every file must be in the bucket with a
# matching MD5; a file whose MD5 the bucket does not know fails the check.
remote_check() {
	local list=$1 src=${2:-$OUT} report=$1.check log status=0 n
	rm -f "$report"
	# --download=false and --size-only=false: never fall back to downloading or to sizes only, whatever RCLONE_*
	# variables are set; --files-from with --no-traverse: a HEAD per file, no listing of the bucket. rclone reports
	# the files it could not compare by hash only in its NOTICE-level summary: the log level is forced, and the
	# variables that could lower it or send the log elsewhere are unset (rclone refuses -q or -v with --log-level)
	log=$(env -u RCLONE_LOG_LEVEL -u RCLONE_QUIET -u RCLONE_VERBOSE -u RCLONE_LOG_FILE -u RCLONE_USE_JSON_LOG \
		-u RCLONE_LOG_FORMAT -u RCLONE_SYSLOG -u RCLONE_LOG_SYSTEMD \
		rclone check "$src" "$REMOTE" --one-way --files-from "$list" --no-traverse --download=false --size-only=false \
		--combined "$report" "${RCLONE_FLAGS[@]}" --log-level NOTICE 2>&1) || status=$?
	echo "$log"
	[[ $status == 0 ]] || die "rclone check of the upload failed (status $status), see $report"
	# the summary must be there and count every file as matching: without it, nothing says the hashes were checked
	n=$(grep -c . "$list")
	grep -Eq "(^|[^0-9])$n matching files\$" <<<"$log" || die "rclone check did not report $n matching files: not confirmed"
	# rclone counts a file without a hash on either side as matching, and says so in its summary
	! grep -Eq 'hashes could not be checked|No common hash found' <<<"$log" || die "the bucket has no MD5 for some files: not confirmed"
	[[ $(grep '^= ' "$report" | cut -c3- | sort) == "$(sort "$list")" ]] || die "rclone check did not match every file, see $report"
}

# destination_identity prints what identifies the upload destination, without credentials: the rclone path and, for
# a named remote, its type, provider, region and endpoint (from the environment and from rclone's config file). It
# keys the upload markers, which only save uploads: other settings can change where a named remote writes (e.g. the
# remote of an alias, or RCLONE_S3_ENDPOINT) without changing it, so the manifest is uploaded only after every file
# it lists was found in the bucket.
destination_identity() {
	local name var field config
	if [[ $REMOTE != *:* || $REMOTE == /* || $REMOTE == ./* ]]; then
		echo "path=$(realpath -m "$REMOTE")"
		return
	fi
	echo "remote=$REMOTE"
	name=${REMOTE%%:*}
	[[ -n $name ]] || return 0
	# only these fields are kept from the dump, which also holds the keys
	config=$(rclone config dump 2>/dev/null | jq -c --arg n "$name" '.[$n] // {} | {type, provider, region, endpoint}' 2>/dev/null) || config=
	echo "config=$config"
	for field in type provider region endpoint; do
		var=RCLONE_CONFIG_$(tr '[:lower:]-' '[:upper:]_' <<<"$name")_${field^^}
		echo "env.$field=${!var:-}"
	done
}

all_exist() {
	local dir=$1 path
	while read -r _ path; do
		[[ -f $dir/$path ]] || return 1
	done <<<"$2"
}

# run KEY runs process in a process of its own (so that set -e stops it at the first failure), with its output in
# the month's log, and records a failure.
run() {
	local key=$1 started=$SECONDS
	rm -f "$STATE/failed/$key"
	if "$SELF" __process "$key" >>"$STATE/logs/$key.log" 2>&1; then
		echo "backfill: $key done in $((SECONDS - started))s"
	else
		touch "$STATE/failed/$key"
		echo "backfill: $key FAILED after $((SECONDS - started))s, see $STATE/logs/$key.log" >&2
		return 1
	fi
}

# --checksum: skip an object only if its MD5 is the same; --ignore-checksum=false, --size-only=false and
# --s3-disable-checksum=false: always verify the upload's MD5 and store it with a multipart upload, whatever RCLONE_*
# variables are set
RCLONE_FLAGS=(--checksum --ignore-checksum=false --size-only=false --s3-disable-checksum=false
	--retries 5 --low-level-retries 10 --stats 0)
# internal commands, run by xargs and run; the configuration comes from the environment
case ${1:-} in
__run)
	run "$2"
	exit ;;
__process)
	process "$2"
	exit ;;
esac

BIN=${COLDSTORE_BIN:-}
OUT='' STATE='' FROM=pre TO='' GRACE_DAYS=3 JOBS=3 THREADS=4 MEMORY=4GB WORK=
UPLOAD=0 VERIFY_SOURCE=1 REDO=0
MONTHS=()
while [[ $# -gt 0 ]]; do
	case $1 in
	--bin) BIN=$2; shift ;;
	--out) OUT=$2; shift ;;
	--state) STATE=$2; shift ;;
	--from) FROM=$2; shift ;;
	--to) TO=$2; shift ;;
	--grace-days) GRACE_DAYS=$2; shift ;;
	--jobs) JOBS=$2; shift ;;
	--threads) THREADS=$2; shift ;;
	--memory-limit) MEMORY=$2; shift ;;
	--work-dir) WORK=$2; shift ;;
	--no-verify-source) VERIFY_SOURCE=0 ;;
	--upload) UPLOAD=1 ;;
	--redo) REDO=1 ;;
	-h | --help) usage; exit 0 ;;
	-*) usage >&2; die "unknown option $1" ;;
	*) MONTHS+=("$1") ;;
	esac
	shift
done

[[ -n $BIN ]] || die "pass --bin"
BIN=$(command -v "$BIN") || die "binary not found"
BIN=$(realpath "$BIN")
[[ -n $OUT ]] || die "pass --out"
command -v jq >/dev/null || die "jq is required"
mkdir -p "$OUT"
OUT=$(realpath "$OUT")
STATE=${STATE:-$OUT.state}
mkdir -p "$STATE"/{logs,run,verified,uploaded,failed,pending-source}
STATE=$(realpath "$STATE")
WORK=${WORK:-$STATE/work}
mkdir -p "$WORK"
WORK=$(realpath "$WORK")
[[ $JOBS =~ ^[1-9][0-9]*$ ]] || die "invalid --jobs $JOBS"
[[ $GRACE_DAYS =~ ^[0-9]+$ ]] || die "invalid --grace-days $GRACE_DAYS"

if [[ ${#MONTHS[@]} -eq 0 ]]; then
	[[ -n $TO ]] || die "pass --to or MONTHs"
	valid_month "$FROM" || die "invalid --from $FROM"
	if ! valid_month "$TO" || [[ $TO == pre ]]; then die "invalid --to $TO"; fi
	m=$FROM
	while [[ $m == pre || ! $m > $TO ]]; do
		MONTHS+=("$m")
		m=$(next_month "$m")
	done
fi
for m in "${MONTHS[@]}"; do
	valid_month "$m" || die "invalid month $m"
	month_over "$m" || die "month $m is not over yet (or ended less than --grace-days $GRACE_DAYS days ago)"
	[[ $REDO == 0 ]] || rm -f "$STATE/verified/$m"
done
# The drop pattern elements are small and append-only, and a month's reports refer to patterns created up to its
# end: whenever this run exports a month, they are exported again first (a superset of the archived file; refused if
# PostgreSQL holds fewer), so that they cover it. A run that only verifies or uploads archived months keeps them.
REFRESH_DIMS=$REDO
for m in "${MONTHS[@]}"; do
	lines=$(manifest_lines "$m")
	if [[ -z $lines ]] || ! all_exist "$OUT" "$lines"; then REFRESH_DIMS=1; fi
done
rm -f "$STATE"/failed/*

REMOTE=${COLDSTORE_REMOTE:-}
STORAGE_CLASS=${COLDSTORE_S3_STORAGE_CLASS-STANDARD_IA}
if [[ $UPLOAD == 1 ]]; then
	command -v rclone >/dev/null || die "rclone is required to upload"
	if [[ -z $REMOTE ]]; then
		[[ -n ${COLDSTORE_S3_BUCKET:-} && -n ${COLDSTORE_S3_ACCESS_KEY_ID:-} && -n ${COLDSTORE_S3_SECRET_ACCESS_KEY:-} ]] ||
			die "set COLDSTORE_S3_BUCKET, COLDSTORE_S3_ACCESS_KEY_ID and COLDSTORE_S3_SECRET_ACCESS_KEY, or COLDSTORE_REMOTE"
		# an rclone remote named "coldstore" configured from the environment: the keys never appear on a command line
		export RCLONE_CONFIG_COLDSTORE_TYPE=s3
		export RCLONE_CONFIG_COLDSTORE_PROVIDER=${COLDSTORE_S3_PROVIDER:-AWS}
		export RCLONE_CONFIG_COLDSTORE_REGION=${COLDSTORE_S3_REGION:-ap-southeast-1}
		# empty for AWS: rclone uses the region's endpoint
		export RCLONE_CONFIG_COLDSTORE_ENDPOINT=${COLDSTORE_S3_ENDPOINT:-}
		export RCLONE_CONFIG_COLDSTORE_ACCESS_KEY_ID=$COLDSTORE_S3_ACCESS_KEY_ID
		export RCLONE_CONFIG_COLDSTORE_SECRET_ACCESS_KEY=$COLDSTORE_S3_SECRET_ACCESS_KEY
		export RCLONE_CONFIG_COLDSTORE_ACL=private
		# the keys may be scoped to the bucket, without the right to list or create buckets
		export RCLONE_CONFIG_COLDSTORE_NO_CHECK_BUCKET=true
		REMOTE=coldstore:$COLDSTORE_S3_BUCKET/${COLDSTORE_S3_PREFIX:-coldstore/v1}
	fi
	# the upload markers are only valid for the destination they were checked against: another remote, bucket,
	# prefix or endpoint gets markers of its own, so that everything is uploaded to it
	identity=$(destination_identity)
	UPLOADED=$STATE/uploaded/$(sha256sum <<<"$identity" | cut -c1-16)
	mkdir -p "$UPLOADED"
	echo "$identity" >"$UPLOADED/destination"
fi

SELF=$(realpath "$0")
UPLOADED=${UPLOADED:-}
export SELF BIN OUT STATE WORK THREADS MEMORY UPLOAD VERIFY_SOURCE REMOTE STORAGE_CLASS REDO GRACE_DAYS UPLOADED REFRESH_DIMS
echo "backfill: ${#MONTHS[@]} months (${MONTHS[0]} .. ${MONTHS[-1]}), $JOBS at a time, into $OUT; logs in $STATE/logs"

# the dims first: every month's queries join them
"$SELF" __run dims || die "dims failed"
failed=0
printf '%s\n' "${MONTHS[@]}" | xargs -P "$JOBS" -I{} "$SELF" __run {} || failed=1

failures=$(ls "$STATE/failed")
if [[ -n $failures ]]; then
	die "failed: $(echo "$failures" | tr '\n' ' ')(logs in $STATE/logs; run again to resume)"
fi
[[ $failed == 0 ]] || die "xargs failed"

if [[ $UPLOAD == 1 ]]; then
	# The manifest goes last, and only when every file it lists is in the bucket: readers take it as the list of
	# files. A coldstore-patch or export may commit a new manifest entry meanwhile, so the manifest is copied once,
	# and that copy is what is checked and uploaded.
	snapshot=$STATE/run/manifest
	rm -rf "$snapshot"
	mkdir -p "$snapshot"
	cp "$OUT/manifest.json" "$snapshot/manifest.json"
	missing=() all_lines=''
	for key in dims $(jq -r '.months[].month' "$snapshot/manifest.json"); do
		lines=$(manifest_lines "$key" "$snapshot/manifest.json")
		marker_is "$UPLOADED/$key" "$lines" || missing+=("$key")
		all_lines+=$lines$'\n'
	done
	[[ ${#missing[@]} -eq 0 ]] || die "not uploading the manifest: not uploaded to this destination yet: ${missing[*]} (pass them to --upload)"
	all_lines=${all_lines%$'\n'}
	# The markers alone do not prove that the files are in this bucket (see destination_identity): every file the
	# manifest lists is compared with its object (a HEAD each, nothing downloaded), and the local files with the
	# manifest before and after, so that the bucket holds the bytes the manifest lists.
	local_check "$all_lines"
	while read -r _ path; do echo "$path"; done <<<"$all_lines" >"$STATE/run/all.list"
	if ! (remote_check "$STATE/run/all.list"); then
		# the markers of this destination are wrong: drop them, so that the next run uploads what is missing
		find "$UPLOADED" -mindepth 1 -maxdepth 1 ! -name destination -delete
		die "not uploading the manifest: the bucket does not hold every file it lists although they were marked uploaded (the destination changed?); markers dropped, run again to upload them"
	fi
	local_check "$all_lines"
	# the manifest is small and read whenever the bucket is used: STANDARD, not the data files' class
	rclone copyto "$snapshot/manifest.json" "$REMOTE/manifest.json" "${RCLONE_FLAGS[@]}" --s3-storage-class STANDARD
	echo manifest.json >"$STATE/run/manifest.list"
	remote_check "$STATE/run/manifest.list" "$snapshot"
	echo "backfill: manifest uploaded to $REMOTE, MD5 checked"
	cmp -s "$snapshot/manifest.json" "$OUT/manifest.json" ||
		die "the manifest changed during the upload (coldstore-patch or export?): the bucket has the earlier one, which matches its files; run again to upload the new one"
fi
echo "backfill: done"

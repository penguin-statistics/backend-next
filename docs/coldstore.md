# Cold storage operations

The cold storage (#752) keeps the historical drop reports as Parquet files, one set of files per month, indexed by
a manifest. This runbook covers the backfill from a restore of production, verification, upload, the check against
production, and fixing archived months afterwards. The file layout and formats are described in `internal/coldstore`
(package doc of `layout.go`).

Where the files live:

- The cold store lives on the local disk of the box that runs the app. The app and DuckDB read only those local
  files and never read S3.
- A private AWS S3 bucket (`ap-southeast-1`) keeps a backup copy. It is written far more often than it is read.
  Only three things touch it: the uploads at archive time (`backfill.sh --upload` here, the monthly archiving
  afterwards), wal-g, and manual restores (`coldstore-pull`, see Restoring files from the bucket). Nothing
  downloads from it to check an upload. Uploads are confirmed by comparing hashes with the bucket's (see Upload and
  Monthly archiving).
- The box's local cold store is first filled by an rsync from the backfill machine, not by a download from the
  bucket.

```
coldstore/v1/
  manifest.json                                  months, row counts, content hashes, sha256 of every file
  dims/drop_pattern_elements.parquet             pattern_id -> items, joined by every cold query
  drop_reports/drop_reports_YYYY-MM.parquet      non-recruit reports, ORDER BY server, account_id, created_at
  drop_report_extras/extras_YYYY-MM.parquet      their ip/metadata/md5, ORDER BY report_id
  recruit/recruit_YYYY-MM.parquet                recruit reports with ip/metadata/md5, ORDER BY created_at
  rollup/rollup_YYYY-MM.parquet                  non-recruit reliability = 0, per UTC hour
```

The months archived from the new database (see Monthly archiving) have exactly the same files, columns and manifest
entries as the backfilled ones: its `drop_reports` still holds the recruitment reports, with their `pattern_id`, and
the same SQL exports them.

Months are UTC calendar months from `2019-05`. The month `pre` holds the 60,226 reports created before 2019-05
(mostly dated 1970-01-01).

## Tools

All of them are `run-script` commands of the app binary, so the backfill writes the files with the same SQL as the
monthly archiving.

| Command | What it does |
|---|---|
| `coldstore-export --month M [--dims] --out DIR` | Reads a month from PostgreSQL, writes its four files, checks them against what was read (full-column content hashes), records the month in `DIR/manifest.json`. Refuses a month that is already in the manifest unless `--replace`, and a month that ended less than `--grace` (default 72h) ago. |
| `coldstore-verify --month M [--dims] --dir DIR [--no-source]` | Checks the files against the manifest (sha256, size, content hashes, rollup totals) and, unless `--no-source`, against the month read again from PostgreSQL. |
| `coldstore-manifest --dir DIR [--out FILE] [--force]` | Builds a manifest from existing files, without writing to them. Replacing an existing `DIR/manifest.json` needs `--force`; while it indexes the files and writes `DIR/manifest.json`, it holds the lock of every month and of the dims, so that no export or patch runs meanwhile, and it refuses a directory with a patch that stopped partway. |
| `coldstore-patch --dir DIR ...` | Applies an admin fix (reports deleted, reliability changed) to archived months and rewrites their files. |
| `coldstore-recover --dir DIR [--month M]` | Completes or undoes a patch that stopped partway, from its journal and the manifest, without PostgreSQL. |
| `coldstore-prodcheck --dir DIR` | Counts every archived month in the production database by UTC day and lists the months whose count differs from the manifest. |
| `coldstore-pull --dir DIR --month M \| --dims \| --all` | Restores missing or corrupt files of the local copy from the bucket, checking their sha256 before moving them into place. The only command that reads the bucket. |
| `coldstore-archive [--dry-run]` | Runs the monthly archiving once, as the app does (see Monthly archiving). |
| `scripts/coldstore/backfill.sh` | Runs export, verify and upload for many months in parallel, resumably. |

The commands read the app's configuration from the environment without starting the app (no Redis or NATS needed).
They need:

- `PENGUIN_V3_POSTGRES_DSN`: the database to read from, in the app's format. Add `sslmode=disable` for a local
  database.
- `PENGUIN_V3_COLDSTORE_EXTENSION_DIRECTORY`: where DuckDB's postgres extension is installed. The default is
  `/opt/duckdb/extensions`, as in the image. The binary never downloads extensions. For another directory, lay it
  out as `<dir>/v2.0.0/linux_amd64/postgres_scanner.duckdb_extension`, from
  `https://extensions.duckdb.org/v2.0.0/linux_amd64/postgres_scanner.duckdb_extension.gz` (gunzip it). The version
  must be the DuckDB version the binary links (see `ARG DUCKDB_VERSION` in the Dockerfile).

They log to stdout and to `./logs` in the working directory, like the app.

Every DuckDB job uses its own database file and temp directory under `--work-dir` (default: the system temp
directory), and removes them when it ends. Do not point several jobs at one DuckDB temp directory by hand. During
the prototype, concurrent processes sharing one silently wrote other processes' data into their output. An
in-memory database runs out of memory on the large months.

### Getting the binary

Either build it on a glibc system (cgo is required, DuckDB is linked statically):

```sh
CGO_ENABLED=1 go build -o backend .
```

or take the binary and the extension directory from the image:

```sh
id=$(docker create ghcr.io/penguin-statistics/backend-next:<tag>)
docker cp $id:/app/backend ./backend
docker cp $id:/opt/duckdb/extensions ./duckdb-extensions   # PENGUIN_V3_COLDSTORE_EXTENSION_DIRECTORY
docker rm $id
```

## Backfill

Do the backfill on a big machine with a restore of production, never against production itself. The restore of
2026-10-20 05:49 UTC holds every month that has to be archived as long as the migration happens before 2026-12-19.
The last archived month must end before (migration day − 60 days).

Measured on the restore (DuckDB 1.5.6, local NVMe): the prototype over the whole history, and these commands on
single months:

| | |
|---|---|
| Reports | 3,123,844,300 (`drop_reports` 590 GB + `drop_report_extras` 203 GB in PostgreSQL, with indexes) |
| Files | 40.86 GB: drop_reports 18.22 GB, recruit 14.96 GB, extras 7.21 GB, rollup 0.42 GB |
| Largest month | 2023-10: 148.5M reports, 1.13 GB of drop_reports |
| Export | 40 min with 3 months at a time reading the local PostgreSQL (+ 17 min of conversion, which the commands no longer need) |
| Verify against PostgreSQL | about the time of the export: it reads every month again (2022-08, 5.7M reports, in 16 s) |
| Manifest of all 91 months | 4.5 min |
| `coldstore-export` of 2026-09 (48.6M reports) | 2 min 5 s (93 s reading PostgreSQL), 4.6 GB RSS with `--memory-limit 4GB`, 6.0 GB peak in the work directory |
| `coldstore-verify --no-source` of 2026-09 | 5 s |
| `coldstore-patch` of 2026-09 | 41 s |
| Whole history with these commands (estimate from 2026-09) | about 2 h 10 min of export for one job at a time, so under an hour with `--jobs 3`, plus as much again for the verification against PostgreSQL |

Run it:

```sh
export PENGUIN_V3_POSTGRES_DSN='postgres://user:pass@127.0.0.1:5434/penguinprod?sslmode=disable'
export PENGUIN_V3_COLDSTORE_EXTENSION_DIRECTORY=$PWD/duckdb-extensions

scripts/coldstore/backfill.sh --bin ./backend --out /data/coldstore-v1 --to 2026-08 \
    --jobs 3 --threads 4 --memory-limit 6GB
```

- The months are `pre` and 2019-05 through `--to` (or the months given as arguments). The drop pattern elements
  are exported first, and again by every run that exports a month (e.g. a later run with a larger `--to`), so that
  they hold the patterns of every archived month. They are append-only: an export from a PostgreSQL that holds fewer
  of them than the archived file (an older restore) is refused. A run that only verifies or uploads archived months
  keeps them.
- Every month runs `coldstore-export`, then `coldstore-verify` against PostgreSQL. `--no-verify-source` checks only
  against the manifest. The export already compares the files with the rows it read, so the second read guards
  against a bad read.
- `--jobs` months run at a time. Each one gets `--threads` and `--memory-limit` (its RSS stays slightly above
  the limit). Its DuckDB database under `--work-dir` (default `<out>.state/work`) holds the month's rows, about
  125 bytes per report: 6 GB for 2026-09, about 18 GB for 2023-10. With `--jobs 3 --threads 4 --memory-limit 6GB`,
  plan for 12 cores, 20 GB of RAM and 50 GB of free work space, plus 41 GB for the files.
- State and logs are kept in `<out>.state` (`--state`), outside the cold storage directory:
  `logs/<month>.log` holds the output of the month's commands, and `verified/`, `pending-source/` and
  `uploaded/<destination>/` hold the markers. A marker holds the manifest's sha256 of the files it covers, so it
  only counts for those files: a month patched since is verified (against the manifest) and uploaded again.
- It can be resumed. Run the same command again after a failure or an interruption: months already verified are
  skipped. A month that is in the manifest with all its files but has no marker for them (killed before the marker
  was written, another `--state`, a copy rsynced from another machine, patched since) is not exported again: its
  files are checked against the manifest only (`coldstore-verify --no-source`), since PostgreSQL may no longer hold
  the month.
- The export records a month in the manifest before `coldstore-verify` compares it with PostgreSQL, so the backfill
  marks the month in `pending-source/` until that comparison passes. A month whose comparison failed or was
  interrupted is compared with PostgreSQL again when the backfill is resumed, without being exported again: it is
  not taken for an archived month. `--no-verify-source` opts out (the month is then checked against the manifest
  only, and the comparison stays pending for later runs).
- `--redo` exports the given months again from PostgreSQL and replaces their files (`coldstore-export --replace`).
  Use it only while PostgreSQL still holds the whole months, i.e. before their partitions are dropped. The export
  refuses to replace a month with fewer reports than the archive; after the migration, fix archived months with
  `coldstore-patch` only.
- A month counts as over `--grace-days` (default 3) days after its end. `created_at` is the time the report was
  accepted, not the time it was stored, so a month still gains reports while the report queue drains. Months that
  are not over are refused.
- It exits non-zero and lists the failed months if any month failed.

Then check the queries against PostgreSQL on the restore (the query cases of #752), comparing result hashes.

## Upload

`backfill.sh --upload` uploads to the backup bucket with [rclone](https://rclone.org) (S3 API). It uploads each
month once the month is verified, then compares every uploaded file with its object in the bucket by size and MD5.
The manifest goes last, and only once every file it lists is in the bucket, because a restore takes the manifest as
the list of files.

```sh
export COLDSTORE_S3_BUCKET=<bucket>
export COLDSTORE_S3_ACCESS_KEY_ID=<key> COLDSTORE_S3_SECRET_ACCESS_KEY=<secret>
# defaults: COLDSTORE_S3_PROVIDER=AWS COLDSTORE_S3_REGION=ap-southeast-1 (endpoint derived from the region)
#           COLDSTORE_S3_PREFIX=coldstore/v1 COLDSTORE_S3_STORAGE_CLASS=STANDARD_IA
scripts/coldstore/backfill.sh --bin ./backend --out /data/coldstore-v1 --to 2026-08 --upload
```

- The keys go to rclone through the environment (an rclone remote named `coldstore` defined by
  `RCLONE_CONFIG_COLDSTORE_*`), never on a command line. Any other `RCLONE_CONFIG_COLDSTORE_*` variable you set is
  passed through. `COLDSTORE_S3_ENDPOINT` and `COLDSTORE_S3_PROVIDER` override the AWS defaults.
  `COLDSTORE_REMOTE` replaces the whole remote with any rclone path, e.g. a configured remote or a local directory
  for a dry run.
- Objects are uploaded `private`. The bucket is never listed or created (`no_check_bucket`, and the check reads each
  object with a HEAD request), so keys that can only put and get objects under the prefix are enough.
- The data files are stored as `STANDARD_IA` (`COLDSTORE_S3_STORAGE_CLASS`, empty for the server's default). The
  manifest is always `STANDARD`.
- How an upload is confirmed, without downloading anything:
  - rclone sends the file's MD5 with the upload (`Content-MD5`), and S3 rejects a body that does not match. A file
    above rclone's multipart cutoff (200 MiB) is sent in parts, each with its own `Content-MD5`. rclone also stores
    the whole file's MD5 in the object's metadata (`X-Amz-Meta-Md5chksum`), since a multipart ETag is not an MD5.
    The script forces this with `--s3-disable-checksum=false`.
  - After a month's files are uploaded, `rclone check --one-way --files-from <the month's files> --no-traverse`
    compares the local files with the objects by size and MD5. The MD5 comes from a HEAD of each object (the ETag,
    or the metadata MD5 of a multipart upload), so no object is read. `--download=false --size-only=false` makes
    sure that neither a download nor a size-only check is used, even if `RCLONE_*` variables say otherwise. rclone
    counts a file whose MD5 the bucket does not report as matching, so the script fails when rclone reports
    `hashes could not be checked`. It also fails unless every file is reported identical. The manifest is checked
    the same way after its upload.
  - Before the manifest is uploaded, the script copies it once (a `coldstore-patch` or export may commit a new entry
    meanwhile) and checks every file that copy lists the same way: against the copy's sha256 locally, and against
    its object in the bucket (a HEAD per file). Only then is that copy uploaded and checked. If the local manifest
    changed in the meantime, the run fails after uploading the copy (which matches the bucket's files): run it
    again to upload the new one.
  - The files' sha256 and content hashes in the manifest are checked on the local copy (`coldstore-verify`). Right
    before a month is uploaded, and again after its check, the local files are hashed and compared with their
    sha256 in the manifest (no PostgreSQL needed), so that a file changed or corrupted since its verification is
    never uploaded or recorded as uploaded.
  - rclone reports the files it could not compare by hash only in its NOTICE-level summary. The check forces
    `--log-level NOTICE` to its captured output and unsets `RCLONE_LOG_LEVEL`, `RCLONE_QUIET`, `RCLONE_VERBOSE`,
    `RCLONE_LOG_FILE` and the other logging variables. It also requires the summary to count every file as
    matching, so a missing summary fails the upload too.
- rclone skips objects that are already there with the same MD5.
- A month is uploaded again only when its files changed (its sha256 in the manifest differs from the one recorded
  at its last upload), e.g. after `coldstore-patch`, or when the destination changed. Upload markers are kept per
  destination (`uploaded/<id>/`, with the destination in `uploaded/<id>/destination`): the rclone path (remote,
  bucket and prefix) and, for a named remote, its type, provider, region and endpoint, never the keys. Another
  destination starts without markers, so everything is uploaded to it before its manifest. The markers only save
  uploads: other settings can change where a named remote writes without changing its markers (e.g. the `remote` of
  an alias, or `RCLONE_S3_ENDPOINT`). Then the check of every file before the manifest fails, the run drops this
  destination's markers and stops, and the next run uploads everything (rclone skips what is there). This needs the
  backfill's `--state`. With a fresh state (e.g. on another machine's copy), every month is checked and uploaded
  again (rclone skips the unchanged objects), and the manifest is only uploaded once every month in it has been
  passed to `--upload`.
- Cost, from AWS's price list for `ap-southeast-1` (October 2026, before tax):
  - Storage: Standard-IA is $0.0138 per GB-month, so the 41 GB cost about $0.6 a month. Standard-IA bills at least
    30 days per object and 128 KB per object. A patched month's replaced files are billed for the rest of their
    30 days.
  - Uploads: inbound transfer is free. PUT requests cost $0.01 per 1,000, and the backfill makes a few hundred.
  - Upload checks: one HEAD per file ($0.01 per 10,000 requests). Nothing is retrieved.
  - A full restore: about 41 GB × $0.12 of transfer out to the internet (AWS's free 100 GB a month may cover it),
    plus $0.01 per GB of Standard-IA retrieval, so about $5–6.
- To test against a local S3 server (`rclone serve s3 --auth-key KEY,SECRET DIR` or MinIO):
  `COLDSTORE_S3_PROVIDER=Other COLDSTORE_S3_ENDPOINT=http://127.0.0.1:<port> RCLONE_CONFIG_COLDSTORE_FORCE_PATH_STYLE=true`.
  MinIO accepts only `STANDARD` and `REDUCED_REDUNDANCY`: add `COLDSTORE_S3_STORAGE_CLASS=STANDARD`.

## Production check

Production may have changed after the restore was taken. Before the migration, while production still holds every
month, compare every archived month with it. Two kinds of change are possible, and the default check finds only the
first:

- rows added or removed outside the app (manual SQL, a restore, a late insert into an archived month). The app
  itself never deletes a report (the old jsonl archiver, removed since, was disabled in production,
  `PENGUIN_V3_DROP_REPORT_ARCHIVE_ENABLED=false`; the monthly archiving below only drops the partitions of the new
  database), and dates new reports when it accepts them, so it adds reports
  only to the current month (and to the previous one while the report queue drains, see `--grace-days`).
- reliability changes. These are the only changes the app makes to existing reports, and they keep the row count:
  - a recall (`reliability = -1`) works only within 24 hours of the report (the recall key's TTL), and only on a
    report submitted in the last 7 days, so it never reaches an archived month after the restore;
  - a reject-rules reevaluation (`POST /api/admin/rejections/reject-rules/reevaluation/apply`) sets the
    reliability of every matching report in its `reevaluateRange`, in any month. This is the change to look for.

The count check:

```sh
backend run-script coldstore-prodcheck --dir /data/coldstore-v1        # or --manifest manifest.json
```

- It runs one `count(*)` per UTC day (`created_at >= day AND created_at < day + 1`). The `drop_reports_created_at`
  index answers it with an index-only scan, on one connection, with the session `default_transaction_read_only`,
  without parallel workers or JIT, and with `--statement-timeout` (default 60 s). `--pause` waits between
  statements.
- On the restore it took 12 s for the largest month (2023-10, 148.5M reports) and 14 s for 2026-09. Production's
  network disk is slower. Run it at the lowest-traffic hours (see Datadog), and run it in parts with `--month` if
  needed.
- Run it where production's database is reachable. Either run it in a backend pod (copy the manifest in with
  `kubectl cp` and run `/app/backend run-script coldstore-prodcheck --manifest /tmp/manifest.json`), or run it on the
  backfill machine through a port-forward with production's DSN. With `--dir` pointing at the files, it also lists
  the days that differ.
- It lists the months that differ and exits non-zero.
- Only counts are compared, so a reliability change goes unnoticed (see above). `--reliability` also counts the
  reliable rows, but that reads the table rather than only the index: the whole history means reading most of the
  590 GB. Run it only for the months found below.

To find the reliability changes made after the restore (2026-10-20 05:49 UTC):

1. List the reevaluation applies since the restore: search the access logs (Datadog) for
   `POST /api/admin/rejections/reject-rules/reevaluation/apply`, and ask the admins. The request body, which holds
   the `ruleId` and the `reevaluateRange`, is not logged, so get the ranges from whoever ran them.
2. For every month an applied range overlaps, run
   `coldstore-prodcheck --reliability --month M --dir /data/coldstore-v1`. A month whose reliable rows differ is
   handled like any other mismatch (below). A reevaluation that changes reliabilities in both directions can keep
   the count; when in doubt, run `coldstore-verify --month M --dir /data/coldstore-v1` against production, which
   compares every column (it reads the whole month).
3. Until the migration, every reevaluation apply that reaches an archived month must be followed by step 2 for its
   months. After the migration an apply changes only the hot database. A reevaluation of an archived range has to
   be applied to the archive with `coldstore-patch`, from the ids of the changed reports, which the apply does not
   return today.

For each month that differs, either:

- patch it, if the changes are known (see below), or
- export it again from production, at the lowest-traffic hours:
  `PENGUIN_V3_POSTGRES_DSN=<production> scripts/coldstore/backfill.sh --bin ./backend --out /data/coldstore-v1 --redo --upload M`.
  This reads the whole month from production once to export it and once more to verify it. It is refused if
  production holds fewer reports of the month than the archive: then delete the extra reports from the archive with
  `coldstore-patch --delete`.

## Fixing an archived month

When an admin deletes reports or changes their reliability in a month that is already archived, the month's files
have to be generated again. This is the only way once the month's partition is gone.

```sh
# what would change
backend run-script coldstore-patch --dir /data/coldstore-v1 --reliability 123456=-1,123457=-1 --dry-run
# apply: rewrites every file of the affected months (rollup included) and updates the manifest
backend run-script coldstore-patch --dir /data/coldstore-v1 --reliability 123456=-1,123457=-1
backend run-script coldstore-patch --dir /data/coldstore-v1 --delete 123458 --file changes.txt
```

- `--delete ID,...` removes reports and their extras. `--reliability ID=R,...` sets reliabilities (the app's
  "delete" sets -1). `--file` reads one change per line, `<report_id> <reliability>` or `<report_id> delete`, with
  `#` comments.
- The months are found from the manifest's report_id ranges and the files (or given with `--month`). If a report is
  in no archived month, nothing is changed, unless `--ignore-missing` is passed (e.g. a list that also covers hot
  months, which are fixed in PostgreSQL).
- Before changing anything, it checks the month's files against the manifest (sha256 and content hashes). It writes
  the new files with the export's SQL and checks them against the patched rows. The result is byte for byte what an
  export of the fixed rows would write.
- The files may be the only copy of the month, so the new generation is published so that it can always be completed
  or undone without PostgreSQL. The new files are staged next to the old ones (`<file>.next`), then a journal
  (`.journal/month-M.json` in the directory) records the month's old and new manifest entries. Then the manifest
  entry is replaced (the commit point), and each old file is kept as `<file>.prev` while its `.next` replaces it.
  The `.prev` files and the journal are removed last. A failure after the journal is written is recovered at once.
  If the patch is killed, `coldstore-recover --dir DIR` (or the next `coldstore-patch`, which recovers before it
  looks for the reports, or the next `coldstore-export` of the month) completes it if the manifest has the new
  entry, and puts the old files back otherwise. Every file is compared by sha256 with the journal's entries before
  it is used. `coldstore-verify` and `--dry-run` refuse a month with a journal. Patches and exports of one month
  hold the month's lock (`.locks/` in the directory), so they never run at the same time. `--dry-run` only reads:
  it takes a shared lock if the lock file exists and creates nothing, so it works on a read-only copy.
- Applying a reliability patch again changes nothing. A delete patch cannot be applied twice: its reports are gone,
  so a second run stops because they are "in no archived month". The months are patched one after the other, and
  each month's files and manifest entry are published as soon as it is done (the log says `patched month` for each).
  If a run stops partway, run it again with `--month` for each month not yet patched, or with `--ignore-missing`,
  after checking that the missing reports are exactly those of the months already patched.
- While PostgreSQL still holds the month, make the same fix in PostgreSQL (through the app), then
  `coldstore-verify --month M --dir ...` against it: the files and the database must agree.
- Patch the copy that the box serves from (run the patch on the box, or rsync the month's files and the manifest to
  it). The app never reads the bucket, so uploading alone does not change what it serves.
- Then upload: `scripts/coldstore/backfill.sh --bin ./backend --out /data/coldstore-v1 --upload M`. Patched months
  are recognized and uploaded again, then the manifest.

## Serving the cold storage (the app)

With `PENGUIN_V3_COLDSTORE_ENABLED=true`, the app opens the local copy in `PENGUIN_V3_COLDSTORE_DIRECTORY` at startup
(`coldstore.Reader`). With `PENGUIN_V3_COLDSTORE_QUERIES_ENABLED=true` as well, the personal drop and pattern
matrices, the drop matrix queries of `/result/advanced` and the customized trend (`/result/advanced` with an
interval) read the archived part of their time ranges from it (see below).

| Setting (`PENGUIN_V3_...`) | Default | |
|---|---|---|
| `COLDSTORE_ENABLED` | `false` | |
| `COLDSTORE_DIRECTORY` | | the local cold storage directory; required when enabled |
| `COLDSTORE_THREADS` | `2` | DuckDB threads |
| `COLDSTORE_MEMORY_LIMIT` | `1GB` | DuckDB memory limit |
| `COLDSTORE_TEMP_DIRECTORY` | system temp | DuckDB's spill files (in a directory of its own) and the time of the last integrity check |
| `COLDSTORE_RELOAD_INTERVAL` | `1m` | how often the manifest is checked for a change; `0` never |
| `COLDSTORE_INTEGRITY_CHECK_INTERVAL` | `168h` | how often every file is hashed again; `0` never |
| `COLDSTORE_QUERIES_ENABLED` | `false` | read the archived part of the drop matrix, pattern matrix and customized trend queries from the cold storage; requires `COLDSTORE_ENABLED` |
| `COLDSTORE_SHADOW_ENABLED` | `false` | shadow mode (below); requires `COLDSTORE_ENABLED`, excludes `COLDSTORE_QUERIES_ENABLED` |
| `COLDSTORE_SHADOW_TIMEOUT` | `30s` | bound of each background computation of shadow mode; its PostgreSQL statements get a `statement_timeout` a tenth (at most 1 s) shorter |
| `COLDSTORE_SHADOW_MAX_IN_FLIGHT` | `2` | background computations of shadow mode at once; queries beyond it are not compared |

- The app never reads the bucket. DuckDB is confined to the cold storage directory and its temp directory: external
  access is off (`enable_external_access = false`, with only those two in `allowed_directories`), extensions are
  never installed or loaded, and the configuration is locked, so a query cannot read `s3://`, `http://` or any
  other path. Symbolic links in the directory (the directory itself and its `drop_reports`, `recruit`, ... subdirectories)
  are resolved when the app starts; restart the app after pointing them elsewhere. The files themselves must not be
  links: DuckDB would refuse to read them, so the app does not start, and `coldstore-manifest` refuses to index them.
- At startup, every file of the manifest must be in the directory with the manifest's size (a `stat` of each file,
  no reading), the manifest must hold `pre` and every month from 2019-05 to its last month without a gap, and no
  patch may have stopped partway. Otherwise the app does not start, and the error names each bad month and the
  `coldstore-pull` command that restores it.
- The hot boundary is the first instant after the last archived month. Queries read `[start, boundary)` from the
  cold storage and `[boundary, end)` from PostgreSQL, and add the counts. After the migration, PostgreSQL's
  earliest partition starts at the boundary: drop a month's partition only after the app serves a manifest that
  holds the month (`penguinbackend_coldstore_hot_boundary_timestamp_seconds` has moved past it). Before the migration
  (shadow mode on the old database), PostgreSQL holds every month, more than the queries read from it.
- How the drop matrix queries read the cold storage (`repo.ColdDropReport`): the reports of an account (personal
  matrix, personal advanced queries) from `drop_reports` files, whose sort by server and account skips most row
  groups; the other queries the whole UTC hours of their time ranges from `rollup` and only the first and last
  partial hours from `drop_reports`. The recruit stage, which those files leave out, is read from `recruit` in
  both cases when the query covers it (every personal matrix does): its files are sorted by time, so a personal
  query reads the account column of every recruit file of its range (about 1.5 s for a typical account, 5 s of the
  8 s of the heaviest account over the full history on the prototype). The counts are those of PostgreSQL for the
  same reports, and are added to PostgreSQL's for the part from the boundary on. Query durations:
  `penguinbackend_coldstore_query_duration_seconds{query="drop_matrix"}`.
- The pattern matrix queries (the personal pattern matrix, and the pattern matrix job's day when it is archived)
  count the reports with `times = 1` per stage and pattern from the same files (`one_time` rows of `rollup`), one
  query per time range, and never need the recruit stage, which the pattern matrix leaves out. On the prototype, a
  personal pattern matrix (161 time ranges of CN, up to seven years each) takes 6 to 7 s
  from the files, 35 to 45 ms per range, also for the heaviest account. Query durations:
  `penguinbackend_coldstore_query_duration_seconds{query="pattern_matrix"}`.
- The customized trend (quantity and times per bucket of a stage and its items) splits the range of its buckets at
  the boundary. A bucket that straddles the boundary keeps its interval and is counted from both sides. The cold part
  reads the same files as the drop matrix: whole UTC hours from `rollup`, the partial first and last hours and the
  reports of an account from `drop_reports`, and the recruit stage from `recruit`. On the prototype (2 threads,
  1 GB), quantity + times of a CN stage take 0.4 + 0.25 s for 60 daily buckets, 3.4 + 2.9 s for seven years of
  weekly buckets, 0.17 + 0.07 s for 14 days of hourly buckets; 0.3 s for an account's 60 daily buckets (6.2 s on
  PostgreSQL 14); 2.2 + 2.1 s for the recruit stage's 60 daily buckets (no rollup for recruit). Query durations:
  `penguinbackend_coldstore_query_duration_seconds{query="trend_quantity"}` and `{query="trend_times"}`.
- Shadow mode (`COLDSTORE_SHADOW_ENABLED`), for the week before the migration while PostgreSQL still holds every
  report: the queries of `COLDSTORE_QUERIES_ENABLED` are answered from PostgreSQL alone, as with the cold storage
  off. Each of them that reaches back before the boundary is also computed split at the boundary, in the background,
  at the same time; the response never waits for it. Once both are done, their counts are compared whatever the
  order of the rows. `penguinbackend_coldstore_shadow_comparisons_total{query, result}` counts `match`,
  `mismatch`, `error`, `timeout` (longer than `COLDSTORE_SHADOW_TIMEOUT`, or cancelled by PostgreSQL for its
  statement timeout) and `skipped` (every slot busy, or PostgreSQL failed); `penguinbackend_coldstore_shadow_duration_seconds{query, result}` is the duration of the split
  computation. A mismatch logs `coldstore.shadow.mismatch` with the query (`drop_matrix`, `pattern_matrix`,
  `trend_quantity`, `trend_times`), server, time range, hot boundary, whether an account was queried (never which),
  the number of differences and the first ten. Expect no `mismatch` before turning on `COLDSTORE_QUERIES_ENABLED`.
  A mismatch whose differences are all in parts that end less than an hour before the query (an advanced query
  without `end`, the latest time range of a personal matrix, the last bucket of a trend up to today) counts as
  `mismatch_open` instead, logged with `open_end: true`: PostgreSQL alone and the hot part of the split are two
  reads of PostgreSQL, and a report written in between is counted by one of them only. A few such mismatches on busy
  stages are expected; their differences are a few reports. Each interval of a matrix and each bucket of a trend is
  classified by its own end, so a difference in an older interval or bucket stays a `mismatch` (the log's
  `closed_differences` counts them) even when the same query reaches to now.
  The PostgreSQL statements of a background computation run with a `statement_timeout` that ends just before
  `COLDSTORE_SHADOW_TIMEOUT`: the driver does not cancel a statement on the server when it gives up on it, so a
  computation that timed out never leaves its scan running.
  The background computations add load: the cold storage, and PostgreSQL again for the part from the boundary on.
- The manifest is checked every `COLDSTORE_RELOAD_INTERVAL`. A changed manifest (a month archived, a month patched)
  is served once all its files pass the startup checks. Until then the app keeps serving the previous one, logs
  `coldstore.reader.reload_failed` and counts `penguinbackend_coldstore_manifest_reload_failures_total` at every
  attempt. Put the files in place before the manifest (the export and the patch do).
- A patch, a pull or an export replaces a month's files under the month's lock (`.locks/`), a patch one file after
  the other once its manifest entry is committed. Each query holds a shared lock on the months (and the dims) whose
  files it reads, from its start until its rows are read, so it never reads two generations of a month: a
  publication waits for the queries of its month to end, and the queries that start meanwhile wait for it (until
  their context ends). While a patch of a month has stopped partway (its journal is in `.journal/`), the queries
  that read the month fail and name the `coldstore-recover` command; the other months are served. The app creates
  the lock files, so `.locks/` must be writable by it.
- The integrity check hashes every file, one at a time, on a thread at the lowest CPU and I/O priority, and compares
  it with the manifest's sha256. It never repairs or downloads anything: it logs `coldstore.integrity.bad_file`
  with the restore command and counts `penguinbackend_coldstore_integrity_files_total{result="mismatch"|"missing"}`;
  `penguinbackend_coldstore_integrity_last_bad_files` is the count of the last completed check. Alert on it.
  Each month (and the dims) is hashed under the queries' shared lock and against `manifest.json` as it is then, not
  the manifest the app serves: a month patched since the last reload is not reported, and a publication of the
  month waits for its hashing (and the hashing for a running publication). A month whose patch stopped partway is
  skipped (`coldstore.integrity.skipped`). A check stopped by a shutdown, even within a file, is not completed:
  neither the metrics nor the recorded time change. A check that fails (`coldstore.integrity.failed`, e.g. an
  unreadable manifest) is not recorded either and runs again an hour later; if the time cannot be recorded, the
  process still waits the interval. The first check after a start runs an hour later, unless the
  last check recorded in the temp directory is more recent.

## Restoring files from the bucket

`coldstore-pull` is the only thing that reads the bucket, and only an operator runs it.

```sh
export COLDSTORE_S3_BUCKET=<bucket>
export COLDSTORE_S3_ACCESS_KEY_ID=<key> COLDSTORE_S3_SECRET_ACCESS_KEY=<secret>
backend run-script coldstore-pull --dir /data/coldstore-v1 --month 2023-10    # or --dims, or --all
```

- The local manifest says what the files must be. Files already in place (size and sha256) are skipped; the others
  are downloaded next to their destination, hashed while they are written, and moved into place only if they match.
  A bucket copy that does not match the local manifest (a month patched and not uploaded again) is refused.
- Without a local manifest (a new machine), `--all` downloads the bucket's manifest and every file it lists, and
  installs the manifest last, so a half-restored directory is never served. Prefer an rsync from a machine that has
  the files: it costs nothing.
- Before downloading, it refuses if a disk would be left with less than `--min-free-gib` (default 10) GiB free:
  each disk the files are written to, the one of `--dir` or, for a subdirectory that is a link, the one it leads to.
  The app refuses to start without its files rather than reading them remotely: grow the disk instead.
- A file that is itself a symbolic link is replaced by the file, even when the link leads to the right content: the
  app refuses linked files.
- Each month is restored under the month's lock, so a patch or an export of the month never runs alongside, and
  it refuses a month whose patch stopped partway (run `coldstore-recover`).
- `COLDSTORE_S3_REGION` (default `ap-southeast-1`), `COLDSTORE_S3_PREFIX` (default `coldstore/v1`) and
  `COLDSTORE_S3_ENDPOINT` (an S3-compatible server, path-style) are read as in `backfill.sh`. Without the two key
  variables, the AWS SDK's default credentials are used.
- A running app picks a restored file up by itself (DuckDB checks the cached footers against the files); a restored
  manifest is picked up at the next reload.

## Monthly archiving

On the new database (`scripts/newdb`: `drop_reports`, the recruitment reports included, and `drop_report_extras`
partitioned by month of `created_at`), the app archives the months itself once they
are old enough, and drops their partitions. It replaces the jsonl.gz archiver of `internal/pkg/archiver` (one
`drop_reports` and one `drop_report_extras` file per day, then a `DELETE`), which is removed, with its
`archive_drop_reports` run-script, its admin endpoint (`POST /admin/archive`) and its settings
(`PENGUIN_V3_DROP_REPORT_ARCHIVE_*`, `PENGUIN_V3_NO_ARCHIVE_DAYS`, `PENGUIN_V3_DELETE_DROP_REPORT_AFTER_ARCHIVE`).
It was disabled in production.

It runs in the app process that has `PENGUIN_V3_COLDSTORE_ARCHIVE_ENABLED=true` (default off), once a minute after
the start and then every `PENGUIN_V3_COLDSTORE_ARCHIVE_INTERVAL` (default 1h), holding the Redis lock
`mutex:coldstore-archive`. Turn it on only in the process that runs on the machine holding the cold storage
directory. `run-script coldstore-archive` runs it once by hand, with the same settings and lock (`--dry-run` lists
the month partitions and which are due). On today's database there are no partitions, and it does nothing.

| Setting (`PENGUIN_V3_...`) | Default | |
|---|---|---|
| `COLDSTORE_DIRECTORY` | | The cold storage directory the app serves from, on the local disk. Required. |
| `COLDSTORE_WORK_DIRECTORY` | system temp | DuckDB database and spill files, about the size of the month's partitions. |
| `COLDSTORE_ARCHIVE_AFTER` | `1440h` | A month is archived once it ended this long ago (60 days: the database keeps 60 to 90 days). At least 31 days. |
| `COLDSTORE_ARCHIVE_DROP_DELAY` | `15m` | How long a month stays both in the manifest and in PostgreSQL before its partitions are dropped. Longer than the time every app reading the directory takes to load a new manifest (`COLDSTORE_RELOAD_INTERVAL`, 1m by default). |
| `COLDSTORE_ARCHIVE_MIN_FREE_BYTES` | 10 GiB | Free space that must be left after the month's files and work files. |
| `COLDSTORE_ARCHIVE_THREADS`, `COLDSTORE_ARCHIVE_MEMORY_LIMIT` | 2, `2GB` | Limits of the DuckDB jobs. |
| `COLDSTORE_S3_BUCKET`, `AWS_ACCESS_KEY`, `AWS_SECRET_KEY` | | The backup bucket and its keys. Required. |
| `COLDSTORE_S3_REGION` | `ap-southeast-1` | |
| `COLDSTORE_S3_PREFIX` | `coldstore/v1` | |
| `COLDSTORE_S3_STORAGE_CLASS` | `STANDARD_IA` | Storage class of the data files. The manifests are always `STANDARD`. |
| `COLDSTORE_S3_ENDPOINT` | | An S3-compatible server instead of AWS (tests). |

A missing or unwritable directory, a missing or unreadable manifest, a manifest that does not hold `pre` and every
month from 2019-05 to its last one without a gap, or a missing bucket setting fails the start of the app. The
archiving only adds months to a directory that already holds the earlier ones (the backfill's, or one restored with
`coldstore-pull --all`): an empty directory (e.g. a mount point without its disk) would otherwise get a manifest with
one month, which would replace the bucket's.

Each run first creates the partitions of the current month and of the next two that are missing
(`create_report_partitions`, waiting at most 10 s for the lock of the partitioned tables). There is no default
partition, so a month without partitions would refuse every report. Then, for every month whose partitions ended more
than `COLDSTORE_ARCHIVE_AFTER` ago, oldest first:

0. The local manifest must still hold `pre` and every month up to its last one, and either the month already or the
   month just before it. Otherwise the run stops with an error (e.g. a month missing between the backfill and the
   partitions of the new database must be backfilled first).
1. Free space: the directory must keep `COLDSTORE_ARCHIVE_MIN_FREE_BYTES` free after the month's files (estimated as a
   tenth of the partitions' size) and the work directory after the DuckDB database (about the partitions' size; both
   are counted against one file system when they share it). Otherwise the month is refused (error log,
   `penguinbackend_coldstore_archive_runs_total{outcome="insufficient_space"}`), and nothing is written.
2. The drop pattern elements are exported again (append-only, so a superset) and verified against PostgreSQL.
3. The month is exported into the directory with the export's SQL (`coldstore-export`'s, so the files are those
   the backfill writes for the month, recruit file included): files written to temporary names, checked against
   the rows read, then renamed into place. They are not in the manifest yet, so the app does not read them. A month
   already in the manifest (a run that stopped after step 6) is checked against the manifest instead.
4. The month's files and the drop pattern elements are uploaded with `x-amz-checksum-sha256`: S3 computes the SHA-256
   of what it receives and rejects the upload unless it matches. Each local file is hashed against its manifest
   entry right before its upload, and a `HEAD` of each object must then report the same SHA-256 and size. Nothing is
   downloaded. An object already in the bucket with the same SHA-256 is not uploaded again (a `HEAD` per file). The
   other files of the local manifest go through the same check, so a month patched since its upload is uploaded
   too: the bucket's manifest never lists an object the bucket does not hold. The drop pattern elements and a
   patched month are overwritten in place, so the bucket's previous manifests (under `manifests/`) can list an older
   SHA-256 for them.
5. In one transaction, the month's partitions are locked in `SHARE` mode: they can still be read, but no row of them
   can change until they are dropped. The month is read again from PostgreSQL and compared with the files
   (full-column content hashes). PostgreSQL rows of the month that would not be archived (a row of the month's
   `drop_report_extras` partition without a report of the month) fail the comparison too. An `UPDATE` through
   `drop_reports` by `report_id` alone locks every partition, so it waits for this step (the read time of a month,
   about a minute and a half for 50M reports). A recall is not one: it looks only at the reports submitted in the
   last 7 days (`repo.RecallMaxAge`), so it never locks the partitions of a month old enough to archive.
6. The new `manifest.json` with the month is uploaded to the bucket (after the one it replaces is copied to
   `manifests/manifest-<its LastModified>-<its sha256>.json` in the bucket), then the local manifest is replaced
   atomically (written to a temporary file, then renamed). Every file it lists must be in the bucket with its
   SHA-256. Then, the month being in the local manifest (the app can load it from then on), the time is recorded
   as its `recorded_at`, in both manifests the same way. So the delay below starts once the month is in the local
   manifest, however long the uploads took. A month that is in the local manifest without `recorded_at` (a run
   that stopped in between) gets it at the next run. A month already recorded is left as it is, but the bucket's
   manifest is still replaced if it is not the local one.
7. If the month was recorded at least `COLDSTORE_ARCHIVE_DROP_DELAY` ago, by an earlier run, its partitions of
   `drop_reports` and `drop_report_extras` (the only partitioned tables, and the only partitions it creates) are
   detached and dropped in the same transaction, and it commits. Otherwise the transaction rolls back and the
   partitions are kept: the run that adds a month to the manifest or records its time never drops it. The first run
   after the delay (the next one, with the default interval) goes through the steps again, checking the files
   instead of exporting them and uploading nothing, and drops the partitions. Until then, runs skip the month.

The lock of step 5 and `DETACH` (the lock of the partitioned tables, which every query of them waits behind) each
wait at most 10 s. A query through `drop_reports` (an `UPDATE` by `report_id` alone) locks the partitioned table
before the partitions, so one that waits for the locks of step 5 holds the lock `DETACH` waits for: if a query waits
for them before a `DETACH` (looked up before each one, since a query can come while the one before waits for its
lock), that `DETACH` is not issued. On any of these conflicts the transaction rolls back, which releases its locks
so that the query finishes and undoes a partition already detached, and steps 5 to 7 are tried again (the month
locked and compared with PostgreSQL again) after 10 s, 6 times in all. If such queries kept coming more often than
once per comparison, every try would fail, and so would the run (the next run tries again). The only one the app
sends often, the recall, never locks a month old enough to archive (see step 5); the others are rare (a reject-rules
reevaluation, a manual query).

The delay is there because of how the app reads (`coldstore.Reader`): it serves the months of the manifest it last
loaded, and PostgreSQL from the end of their last month on (the hot boundary). It loads a changed manifest within
`COLDSTORE_RELOAD_INTERVAL` (1m by default), and keeps the previous one while a new one fails its checks. Until it
loads the manifest with the month, it reads the month from PostgreSQL, so the partitions must still be there. While
the month is in both, the queries read it from the cold storage once the manifest is loaded, and from PostgreSQL
before. A reader that does not reload (`COLDSTORE_RELOAD_INTERVAL=0`), or keeps failing to (see
`coldstore_manifest_reload_failures_total`), loses the month from its queries once the partitions are dropped, until
it restarts.

Any failure rolls the transaction back, so no partition is dropped unless every step passed, and the run stops at
that month (`outcome` `error` or `verification_failed`; it is tried again at the next run). Every step can run again
after a crash: a month still in PostgreSQL is exported to the same bytes, objects already uploaded are kept, and a
month already in the local manifest is checked instead of exported.

When a month fails the comparison with PostgreSQL, look at what changed (an admin reevaluation, a manual fix).
While its partitions are there, PostgreSQL is the source: export the month again with
`coldstore-export --month M --out <dir> --replace` and let the next run archive it.

Metrics: `penguinbackend_coldstore_archive_runs_total{outcome}` (`ok`, `error`, `verification_failed`,
`insufficient_space`, `lock_not_acquired`), `penguinbackend_coldstore_archived_months_total`,
`penguinbackend_coldstore_archive_last_success_timestamp_seconds` and `penguinbackend_coldstore_free_bytes{dir}`
(`store`, `work`).

### How the app picks up a new month

The archiving writes into the directory the app reads, so a new month needs no copy: once the local manifest is
replaced, the files it lists are complete and checked. A reader keeps using the files of the manifest it loaded, and
picks up the new month by loading `manifest.json` again when it changes (the file is only ever replaced by a rename,
never written in place). Files are only added or replaced by a rename, so an open file keeps its content. Another
copy of the directory (e.g. on a second machine) is not updated by the archiving: sync it from the bucket (manifest
last), or rsync it from this one, within `COLDSTORE_ARCHIVE_DROP_DELAY` of the recording, or its app misses the month
from the drop on until the copy has it.

## Verification, summarized

| Check | Where | Catches |
|---|---|---|
| Files vs rows read from PostgreSQL (count, sums, bounds, `sum(hash(all columns))` of reports and extras), rollup totals vs reliable reports | every export and patch, before the files are renamed into place | a bad write, a corrupted temp file |
| Files vs manifest (sha256, size, content hashes, rows, rollup totals) | `coldstore-verify --no-source`, on a copy without a marker for its files, before a patch | a changed or truncated file, a bad copy |
| Local files vs manifest (sha256 only, no PostgreSQL) | `backfill.sh --upload`, before and after every month's upload | a file changed or corrupted after its verification |
| Uploaded objects vs local files (size, MD5 from a HEAD, nothing downloaded) | `backfill.sh --upload`, after every month and after the manifest | a bad or missing upload, an object without an MD5 |
| Uploads vs local files (`x-amz-checksum-sha256` checked by S3, then the SHA-256 from a HEAD) | monthly archiving, every file and the manifest | a bad or missing upload |
| Files vs PostgreSQL read again, the partitions locked | monthly archiving, right before the manifest and the drop | a bad read, a change in PostgreSQL after the export, rows that would not be archived |
| Files vs PostgreSQL read again | `coldstore-verify` | a bad read, a change in PostgreSQL after the export |
| Daily counts vs production | `coldstore-prodcheck` | reports added or removed in production after the restore, outside the app; not reliability changes |
| Daily reliable counts vs production | `coldstore-prodcheck --reliability`, for the months of the reevaluations applied after the restore | reliability changes that change the number of reliable reports |
| Local files vs manifest (size) | the app's startup and every reload of the manifest | a missing or truncated file, before it is served |
| Local files vs manifest (sha256 only) | the app's integrity check (weekly), `coldstore-pull` | a file corrupted on the box's disk |

Row counts alone are never enough. A changed value, or another process's data mixed into a file, keeps every count
and is caught only by the content hashes.

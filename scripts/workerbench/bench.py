#!/usr/bin/env python3
"""Noise-aware benchmark driver for the calcwkr worker harness.

Measures the wall time of one full calcwkr batch (all servers: drop matrix job, pattern
matrix job, site stats refresh) via internal/workers/calcwkr/workerbench_test.go, which runs
the batch through the worker itself with the clock pinned so every run does identical work.

Skew control (the matrix tables are effectively a cache the worker rewrites every run):
  * the rows of the days the batch may rewrite (reported by the harness) are backed up once
    into schema `workerbench` (restore with --restore);
  * drop_matrix_elements / pattern_matrix_elements are VACUUMed before every run so dead
    tuples from previous runs never accumulate;
  * Redis is flushed once per invocation, then a warmup run re-populates it, so measured
    runs see the warm, steady state of a long-running worker;
  * an environment fingerprint (indexes on the tables the worker touches, key planner
    GUCs, cgroup caps of the cluster) is compared with the baseline's; a mismatch exits
    with status env_changed. Indexes named wb_* are created by experiments and excluded.

Noise control (shared machine, other sessions use the same cluster; no other backend-next instance may run
against the cluster while measuring, since the bench's connections are recognised by its application name):
  * before each run, waits (up to WORKERBENCH_QUIET_WAIT_S) until no foreign query is
    active on the cluster;
  * during the timed batch of each run records foreign_cores (CPU used by processes other
    than the bench and the cluster) and foreign_pg (peak active foreign queries on the cluster);
  * spread = (max - min) / median across measured runs.
A result is NOISY when any of those exceed thresholds. Reruns are budgeted (--rerun,
default budget 10, tracked in the state dir).

Exit codes: 0 clean, 1 crash, 2 output hash mismatch, 3 noisy, 4 rerun budget exhausted,
5 environment changed vs baseline.
"""
import argparse
import json
import os
import re
import statistics
import subprocess
import sys
import threading
import time

REPO = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
STATE = os.environ.get("WORKERBENCH_STATE", os.path.join(REPO, ".workerbench"))
PG_PORT = os.environ.get("WORKERBENCH_PG_PORT", "5434")
PG_DB = os.environ.get("WORKERBENCH_PG_DB", "penguinprod")
PG_UNIT = os.environ.get("WORKERBENCH_PG_UNIT", "postgresql@14-main.service")
REDIS_PORT = os.environ.get("WORKERBENCH_REDIS_PORT", "16379")
HARNESS_PKG = "internal/workers/calcwkr"
HZ = os.sysconf("SC_CLK_TCK")

FOREIGN_CORES_MAX = float(os.environ.get("WORKERBENCH_FOREIGN_CORES_MAX", "4.0"))
FOREIGN_PG_MAX = int(os.environ.get("WORKERBENCH_FOREIGN_PG_MAX", "0"))
SPREAD_MAX = float(os.environ.get("WORKERBENCH_SPREAD_MAX", "0.10"))
RERUN_BUDGET = int(os.environ.get("WORKERBENCH_RERUN_BUDGET", "10"))
QUIET_WAIT_S = int(os.environ.get("WORKERBENCH_QUIET_WAIT_S", "1800"))

MATRIX_TABLES = ["drop_matrix_elements", "pattern_matrix_elements"]
FINGERPRINT_TABLES = MATRIX_TABLES + [
    "drop_reports", "drop_pattern_elements", "drop_patterns", "drop_infos",
    "time_ranges", "stages", "items",
]
FINGERPRINT_GUCS = [
    "work_mem", "effective_cache_size", "shared_buffers", "maintenance_work_mem",
    "max_parallel_workers", "max_parallel_workers_per_gather", "max_worker_processes",
    "random_page_cost", "effective_io_concurrency", "jit",
]


def log(msg):
    print(msg, flush=True)


def psql(sql, db=PG_DB, check=True):
    r = subprocess.run(
        ["sudo", "-n", "-u", "postgres", "psql", "-p", PG_PORT, "-d", db, "-v", "ON_ERROR_STOP=1",
         "-tAc", sql],
        # run from / so the postgres user can enter the working directory
        capture_output=True, text=True, cwd="/",
    )
    if check and r.returncode != 0:
        raise RuntimeError(f"psql failed: {sql[:120]}: {r.stderr.strip()}")
    return r.stdout.strip()


# ---------------------------------------------------------------- noise sampling

def total_jiffies():
    with open("/proc/stat") as f:
        vals = [int(x) for x in f.readline().split()[1:]]
    # user nice system irq softirq steal (idle/iowait excluded)
    return vals[0] + vals[1] + vals[2] + vals[5] + vals[6] + vals[7]


def proc_jiffies(pid):
    try:
        with open(f"/proc/{pid}/stat") as f:
            data = f.read()
        fields = data[data.rindex(")") + 2:].split()
        return int(fields[11]) + int(fields[12]) + int(fields[13]) + int(fields[14])
    except (OSError, ValueError, IndexError):
        return 0


def cluster_pids():
    """PIDs in the cluster's systemd cgroup (postmaster and all backends)."""
    cg = subprocess.run(["systemctl", "show", PG_UNIT, "-p", "ControlGroup", "--value"],
                        capture_output=True, text=True).stdout.strip()
    try:
        with open(f"/sys/fs/cgroup{cg}/cgroup.procs") as f:
            return {int(x) for x in f.read().split()}
    except OSError:
        return set()


def bench_client_ports(pid):
    out = subprocess.run(["ss", "-tnpH", "state", "established", f"dport = :{PG_PORT}"],
                         capture_output=True, text=True).stdout
    ports = set()
    for line in out.splitlines():
        if f"pid={pid}," in line:
            ports.add(int(line.split()[2].rsplit(":", 1)[1]))
    return ports


def foreign_pg_active(bench_pid=None):
    # list active backends before the bench's sockets: a connection the bench opens in between would otherwise
    # look foreign
    out = psql("select coalesce(client_port, -1) || '|' || coalesce(application_name, '') || '|' || "
               "left(regexp_replace(query, '\\s+', ' ', 'g'), 80) from pg_stat_activity where state <> 'idle' "
               "and pid <> pg_backend_pid() and backend_type = 'client backend'", db="postgres",
               check=False)
    ports = bench_client_ports(bench_pid) if bench_pid else set()
    foreign = 0
    for line in out.splitlines():
        port, _, desc = line.partition("|")
        if not port.lstrip("-").isdigit() or int(port) in ports:
            continue
        # the bench's own connections are not always visible to ss in time (connections opened concurrently);
        # while measuring, no other backend instance may run against the cluster, so its application name is ours
        if bench_pid and desc.startswith(BACKEND_APP_NAME + "|"):
            continue
        foreign += 1
        FOREIGN_SEEN.add(desc)
    return foreign


def foreign_cores_now(sample_s=5.0):
    """Average CPU cores used by everything except the cluster over a short sample (bench is not running)."""
    pgs = cluster_pids()
    t0, o0 = total_jiffies(), sum(proc_jiffies(p) for p in pgs)
    time.sleep(sample_s)
    t1, o1 = total_jiffies(), sum(proc_jiffies(p) for p in pgs)
    return max((t1 - t0) - (o1 - o0), 0) / HZ / sample_s


FOREIGN_SEEN = set()
BACKEND_APP_NAME = "penguin-backend"


def wait_quiet():
    """Block until no foreign query is active on the cluster and foreign CPU load is low, for 20 consecutive seconds."""
    deadline = time.time() + QUIET_WAIT_S
    quiet_since = None
    waited = False
    while time.time() < deadline:
        if foreign_pg_active() == 0 and foreign_cores_now() < FOREIGN_CORES_MAX / 2:
            quiet_since = quiet_since or time.time()
            if time.time() - quiet_since >= 20:
                return True
        else:
            if not waited:
                log("waiting for foreign load (cluster queries / CPU) to clear...")
            waited = True
            quiet_since = None
        time.sleep(2)
    return False


class Monitor:
    """Samples foreign CPU and foreign Postgres activity during the timed batch (between begin and end)."""

    def __init__(self, bench_pid):
        self.bench_pid = bench_pid
        self.stop = threading.Event()
        self.timing = threading.Event()
        self.foreign_pg_peak = 0
        self.pgs = cluster_pids()
        self.window = None

    def _ours(self):
        return sum(proc_jiffies(p) for p in self.pgs | {self.bench_pid})

    def _snapshot(self):
        self.pgs |= cluster_pids()
        return time.time(), total_jiffies(), self._ours()

    def begin(self):
        self.start = self._snapshot()
        self.timing.set()

    def end(self):
        self.timing.clear()
        self.window = (self.start, self._snapshot())

    def run(self):
        while not self.stop.wait(1.0):
            if self.timing.is_set():
                self.pgs |= cluster_pids()
                self.foreign_pg_peak = max(self.foreign_pg_peak, foreign_pg_active(self.bench_pid))

    def result(self):
        (t0, total0, ours0), (t1, total1, ours1) = self.window
        foreign = (total1 - total0) - (ours1 - ours0)
        return {"foreign_cores": round(max(foreign, 0) / HZ / max(t1 - t0, 1e-6), 2),
                "foreign_pg": self.foreign_pg_peak}


# ---------------------------------------------------------------- state / skew control

def batch_days(binary):
    """(server, day_num) pairs a batch at the pinned clock may rewrite, as reported by the harness."""
    env = dict(os.environ, WORKERBENCH_PRINT_DAYS="1")
    r = subprocess.run([binary, "-test.run", "TestWorkerBench", "-test.count=1"],
                       cwd=os.path.join(REPO, HARNESS_PKG), env=env, capture_output=True, text=True)
    days = []
    for line in r.stdout.splitlines():
        if m := re.match(r"BENCH days server=(\w+) from=(\d+) to=(\d+)", line):
            days += [(m[1], d) for d in range(int(m[2]), int(m[3]) + 1)]
    if r.returncode != 0 or not days:
        log(r.stdout + r.stderr)
        raise SystemExit("could not get the days a batch rewrites from the harness")
    return days


def ensure_backup(binary):
    """Back up the matrix rows of every day a batch may rewrite, once; --restore puts them back."""
    days = batch_days(binary)
    values = ", ".join(f"('{server}', {day})" for server, day in days)
    if psql("select to_regclass('workerbench.backup_days') is not null") == "t":
        backed_up = psql("select string_agg(server || ':' || day_num, ',' order by server, day_num) from workerbench.backup_days")
        wanted = ",".join(f"{server}:{day}" for server, day in sorted(days))
        if backed_up != wanted:
            raise SystemExit(f"a backup of other days exists ({backed_up}); run --restore first")
        return
    log("backing up the matrix rows the bench overwrites into schema workerbench ...")
    psql("create schema if not exists workerbench")
    for t in MATRIX_TABLES:
        psql(f"drop table if exists workerbench.{t}")
        psql(f"create table workerbench.{t} as select * from {t} where (server::text, day_num) in ({values})")
    # recorded last: its presence marks a complete backup, and restore uses it to also remove rows the bench
    # added for days that had none
    psql(f"create table workerbench.backup_days as select * from (values {values}) as d(server, day_num)")


def restore_backup():
    if psql("select to_regclass('workerbench.backup_days') is not null") != "t":
        raise SystemExit("no complete backup to restore")
    for t in MATRIX_TABLES:
        psql(f"""begin;
            delete from {t} x using workerbench.backup_days d where x.server::text = d.server and x.day_num = d.day_num;
            insert into {t} select * from workerbench.{t};
            commit;""")
        log(f"restored {t}")
    # the backup has served its purpose; a later run, possibly pinned to other days, takes a new one
    psql("drop schema workerbench cascade")
    log("dropped the backup")


def vacuum_matrix_tables():
    for t in MATRIX_TABLES:
        psql(f"vacuum {t}")


def flush_redis():
    subprocess.run(["redis-cli", "-p", REDIS_PORT, "flushdb"], capture_output=True, check=True)


def fingerprint():
    tables = ",".join(f"'{t}'" for t in FINGERPRINT_TABLES)
    # indexes created by migrations in this repo are part of the code under test, not the environment
    owned = set()
    mig_dir = os.path.join(REPO, "scripts", "migrations")
    for f in sorted(os.listdir(mig_dir)) if os.path.isdir(mig_dir) else []:
        owned |= set(re.findall(r"CREATE INDEX (?:CONCURRENTLY )?(?:IF NOT EXISTS )?(\w+)", open(os.path.join(mig_dir, f)).read()))
    excluded = ",".join(f"'{i}'" for i in sorted(owned)) or "''"
    idx = psql(f"select string_agg(indexrelid::regclass::text || ' ' || pg_get_indexdef(indexrelid), "
               f"E'\\n' order by 1) from pg_index where indrelid::regclass::text in ({tables}) "
               f"and indexrelid::regclass::text not in ({excluded}) and indisvalid")
    gucs = ",".join(f"'{g}'" for g in FINGERPRINT_GUCS)
    settings = psql(f"select string_agg(name || '=' || setting, ' ' order by name) from pg_settings "
                    f"where name in ({gucs})")
    caps = subprocess.run(["systemctl", "show", PG_UNIT, "-p", "CPUQuotaPerSecUSec", "-p", "MemoryMax"],
                          capture_output=True, text=True).stdout.strip().replace("\n", " ")
    # new drop reports change what the jobs compute (today's matrix reads up to "now")
    watermark = psql("select max(report_id) from drop_reports")
    return {"indexes": idx.splitlines(), "settings": settings, "caps": caps, "data_watermark": watermark}


# ---------------------------------------------------------------- runs

def build(binary):
    r = subprocess.run(["go", "test", "-c", "-tags", "workerbench", "-o", binary, f"./{HARNESS_PKG}/"],
                       cwd=REPO, capture_output=True, text=True)
    if r.returncode != 0:
        log(r.stdout + r.stderr)
        log("RESULT status=crash reason=build_failed")
        sys.exit(1)


def run_once(binary, dump, timeout_s):
    vacuum_matrix_tables()
    if not wait_quiet():
        log("cluster never went quiet; running anyway (will be flagged)")
    env = dict(os.environ, WORKERBENCH_DUMP=dump)
    proc = subprocess.Popen([binary, "-test.run", "TestWorkerBench", "-test.count=1", "-test.timeout", "3h"],
                            cwd=os.path.join(REPO, HARNESS_PKG), env=env,
                            stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    mon = Monitor(proc.pid)
    th = threading.Thread(target=mon.run, daemon=True)
    th.start()
    killer = threading.Timer(timeout_s, proc.kill)
    killer.start()
    lines = []
    for line in proc.stdout:
        lines.append(line)
        if line.startswith("BENCH timed_start"):
            mon.begin()
        elif line.startswith("BENCH timed_end"):
            mon.end()
    proc.wait()
    killer.cancel()
    mon.stop.set()
    th.join()
    if proc.returncode != 0 or mon.window is None:
        log("".join(lines[-40:]))
        if proc.returncode == -9:
            log(f"run exceeded {timeout_s}s, killed")
        return None
    res = {"phases": {}}
    for line in lines:
        if m := re.match(r"BENCH phase_total=(\w+) ms=(\d+)", line):
            res["phases"][m[1]] = int(m[2])
        elif m := re.match(r"BENCH total_ms=(\d+)", line):
            res["total_ms"] = int(m[1])
        elif m := re.match(r"BENCH hash=(\w+)", line):
            res["hash"] = m[1]
        elif m := re.match(r"BENCH lines=(\d+)", line):
            res["lines"] = int(m[1])
    res.update(mon.result())
    return res


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--runs", type=int, default=int(os.environ.get("WORKERBENCH_RUNS", "3")))
    ap.add_argument("--set-baseline", action="store_true", help="record reference hash + environment")
    ap.add_argument("--rerun", action="store_true", help="consume one unit of rerun budget")
    ap.add_argument("--restore", action="store_true", help="restore backed-up matrix rows and exit")
    ap.add_argument("--accept-env", action="store_true", help="accept the current environment as baseline env")
    args = ap.parse_args()

    os.makedirs(STATE, exist_ok=True)
    if args.restore:
        restore_backup()
        return

    budget_file = os.path.join(STATE, "reruns_used")
    used = int(open(budget_file).read()) if os.path.exists(budget_file) else 0
    if args.rerun:
        if used >= RERUN_BUDGET:
            log(f"RESULT status=refused reason=rerun_budget_exhausted used={used}/{RERUN_BUDGET}")
            sys.exit(4)
        used += 1
        with open(budget_file, "w") as f:
            f.write(str(used))
        log(f"rerun budget: {used}/{RERUN_BUDGET} used")

    baseline_file = os.path.join(STATE, "baseline.json")
    base = json.load(open(baseline_file)) if os.path.exists(baseline_file) else None
    env_fp = fingerprint()
    if base and not args.set_baseline:
        if args.accept_env:
            base["env"] = env_fp
            json.dump(base, open(baseline_file, "w"), indent=2)
        elif base.get("env") != env_fp:
            for k in env_fp:
                if base["env"].get(k) != env_fp[k]:
                    log(f"env diff [{k}]:\n  baseline: {base['env'].get(k)}\n  now:      {env_fp[k]}")
            log("RESULT status=env_changed reason=environment_differs_from_baseline")
            sys.exit(5)

    binary = os.path.join(STATE, "wb.test")
    build(binary)
    ensure_backup(binary)
    dump = os.path.join(STATE, "last.dump")
    timeout_s = int(base["median_total_ms"] / 1000 * 2 + 120) if base else 3 * 3600

    flush_redis()
    log("warmup...")
    if run_once(binary, dump, timeout_s) is None:
        log("RESULT status=crash reason=warmup_failed")
        sys.exit(1)

    runs = []
    for i in range(args.runs):
        r = run_once(binary, dump, timeout_s)
        if r is None or "total_ms" not in r:
            log("RESULT status=crash reason=run_failed")
            sys.exit(1)
        log(f"run {i + 1}: total_ms={r['total_ms']} phases={r['phases']} "
            f"foreign_cores={r['foreign_cores']} foreign_pg={r['foreign_pg']} hash={r['hash'][:12]}")
        runs.append(r)

    totals = [r["total_ms"] for r in runs]
    median = statistics.median(totals)
    spread = (max(totals) - min(totals)) / median if median else 0
    summary = {
        "median_total_ms": median, "spread": round(spread, 4),
        "phase_median_ms": {p: statistics.median(r["phases"][p] for r in runs) for p in runs[0]["phases"]},
        "foreign_cores_max": max(r["foreign_cores"] for r in runs),
        "foreign_pg_max": max(r["foreign_pg"] for r in runs),
        "hash": runs[0]["hash"], "lines": runs[0]["lines"], "runs": totals,
    }

    if len({r["hash"] for r in runs}) != 1:
        log(f"RESULT status=broken reason=nondeterministic_hash {json.dumps(summary)}")
        sys.exit(2)
    if args.set_baseline:
        summary["env"] = env_fp
        json.dump(summary, open(baseline_file, "w"), indent=2)
        os.replace(dump, os.path.join(STATE, "baseline.dump"))
    elif base["hash"] != summary["hash"]:
        log(f"diff: diff {os.path.join(STATE, 'baseline.dump')} {dump}")
        log(f"RESULT status=broken reason=hash_mismatch {json.dumps(summary)}")
        sys.exit(2)

    reasons = []
    if summary["foreign_cores_max"] > FOREIGN_CORES_MAX:
        reasons.append(f"foreign_cores={summary['foreign_cores_max']}>{FOREIGN_CORES_MAX}")
    if summary["foreign_pg_max"] > FOREIGN_PG_MAX:
        reasons.append(f"foreign_pg={summary['foreign_pg_max']}>{FOREIGN_PG_MAX}")
    if spread > SPREAD_MAX:
        reasons.append(f"spread={spread:.3f}>{SPREAD_MAX}")
    for desc in sorted(FOREIGN_SEEN):
        log(f"foreign query seen: {desc}")
    status = "noisy" if reasons else "clean"
    log(f"RESULT status={status} metric={median:.0f} reasons={','.join(reasons) or '-'} "
        f"reruns_used={used}/{RERUN_BUDGET} {json.dumps(summary)}")
    sys.exit(3 if reasons else 0)


if __name__ == "__main__":
    main()

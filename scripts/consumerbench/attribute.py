#!/usr/bin/env python3
"""Attributes the samples of a consumer benchmark profile to the parts of the consumer.

Reads `go tool pprof -traces` of a CPU or memory profile on stdin, and prints the share of each part. A sample goes to
the first part, in the order of RULES, that its stack matches; so the SQL queries of the verification count
as SQL, and the logging inside the verification as logging. The allocations of a part, and the GC assists they cause,
count for the part; the background GC and the scheduler count for themselves. The publishing of the harness is left
out.

usage: go tool pprof -traces [-sample_index ...] reportwkr.test cpu.pprof | attribute.py cpu|allocs|bytes
"""
import re
import sys

# (part, needles): the first part with a needle matching the stack takes the sample. A needle is a substring of a frame,
# or substrings joined by & that must each be in a frame of the stack.
RULES = [
    ("harness (publish, excluded)", ["(*consumerBench).publish", "loadConsumerBenchFixtures"]),
    ("tracing (otel sdk, bunotel)", ["go.opentelemetry.io/otel", "bun/extra/bunotel", "opentelemetry-go-extra"]),
    ("logging (zerolog + json of Interface)", ["github.com/rs/zerolog"]),
    ("metrics (prometheus)", ["prometheus/client_golang"]),
    ("redis (go-redis)", ["redis/go-redis"]),
    ("nats (fetch, ack, client)", ["nats-io/nats"]),
    # the work of persist between its queries, all of it inside RunInTx: before the SQL client
    ("persist in-process (pattern hash, drop merge, stage cache)", ["reportutil", "calculateDropPatternHash",
                                                                     "internal/pkg/cache", "patrickmn/go-cache"]),
    ("verify SQL client: drop infos, reject rules (bun, pgdriver)", ["reportverifs&github.com/uptrace/bun",
                                                                     "reportverifs&database/sql"]),
    ("persist SQL client: tx, pattern, report, extra (bun, pgdriver)", ["github.com/uptrace/bun", "database/sql"]),
    ("verify in-process (reject rules expr, drop checks)", ["reportverifs", "antonmedv/expr"]),
    ("decode (goccy json)", ["goccy/go-json"]),
    ("reportwkr other", ["internal/workers/reportwkr"]),
    ("runtime: background GC", ["runtime.gcBgMarkWorker", "runtime.bgsweep", "runtime.bgscavenge"]),
    ("runtime: scheduler, netpoll, timers", ["runtime.mcall", "runtime.schedule", "runtime.findRunnable", "runtime.netpoll",
                                              "runtime.park_m", "runtime.goexit0", "runtime.mstart", "runtime.sysmon"]),
]
OTHER = "other"

UNITS = {"ms": 1e-3, "s": 1, "us": 1e-6, "µs": 1e-6, "ns": 1e-9, "min": 60, "h": 3600, "": 1,
         "B": 1, "kB": 1024, "KB": 1024, "MB": 1024 ** 2, "GB": 1024 ** 3}


def parse_value(s):
    m = re.fullmatch(r"([0-9.]+)([a-zA-Zµ]*)", s)
    if not m:
        return None
    num, unit = m.groups()
    return float(num) * UNITS.get(unit, 1)


def main():
    kind = sys.argv[1] if len(sys.argv) > 1 else "cpu"
    totals, syscalls = {}, {}
    stack, value = [], None

    def flush():
        if value is None:
            return
        part = OTHER
        for name, needles in RULES:
            if any(all(any(n in frame for frame in stack) for n in needle.split("&")) for needle in needles):
                part = name
                break
        totals[part] = totals.get(part, 0) + value
        if any(f.startswith(("syscall.", "internal/runtime/syscall.", "runtime.futex")) for f in stack):
            syscalls[part] = syscalls.get(part, 0) + value

    for line in sys.stdin:
        if line.startswith("-----------+"):
            flush()
            stack, value = [], None
            continue
        if value is None:
            m = re.match(r"\s*(\S+)\s+(\S.*)$", line)
            if m and parse_value(m.group(1)) is not None:
                value = parse_value(m.group(1))
                stack.append(m.group(2).strip())
            continue
        stack.append(line.strip())
    flush()

    excluded = {k: v for k, v in totals.items() if k.startswith("harness")}
    counted = {k: v for k, v in totals.items() if k not in excluded}
    total = sum(counted.values()) or 1
    unit = {"cpu": "s", "allocs": "objects", "bytes": "MB"}[kind]
    scale = 1 / 1024 ** 2 if kind == "bytes" else 1
    print(f"{kind}: {total * scale:.2f} {unit} attributed (harness excluded: {sum(excluded.values()) * scale:.2f})")
    if kind == "cpu":
        print("  share   seconds  in syscalls  part")
    for part, v in sorted(counted.items(), key=lambda kv: -kv[1]):
        sc = f"  {100 * syscalls.get(part, 0) / v:9.0f}%" if kind == "cpu" else ""
        print(f"  {100 * v / total:5.1f}%  {v * scale:8.2f}{sc}  {part}")


if __name__ == "__main__":
    main()

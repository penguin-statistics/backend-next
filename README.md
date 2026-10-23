<img src="https://penguin.upyun.galvincdn.com/logos/penguin_stats_logo.png"
     alt="Penguin Statistics - Logo"
     width="96px" />

# Penguin Statistics - V3 Backend

[![License](https://img.shields.io/github/license/penguin-statistics/backend-next)](https://github.com/penguin-statistics/backend-next/blob/main/LICENSE)
[![Last Commit](https://img.shields.io/github/last-commit/penguin-statistics/backend-next)](https://github.com/penguin-statistics/backend-next/commits/dev)
[![GitHub Actions Status](https://github.com/penguin-statistics/backend-next/actions/workflows/build-release.yml/badge.svg)](https://github.com/penguin-statistics/backend-next/actions/workflows/build-release.yml)
[![go.mod Version](https://img.shields.io/github/go-mod/go-version/penguin-statistics/backend-next)](https://github.com/penguin-statistics/backend-next/blob/main/go.mod)
[![FOSSA Status](https://app.fossa.com/api/projects/git%2Bgithub.com%2Fpenguin-statistics%2Fbackend-next.svg?type=shield)](https://app.fossa.com/projects/git%2Bgithub.com%2Fpenguin-statistics%2Fbackend-next?ref=badge_shield)

![Alt](https://repobeats.axiom.co/api/embed/ae259f962b4d5fc542de420d31a237986d282765.svg "Repobeats analytics image")

The re-designed [Penguin Statistics](https://penguin-stats.io/?utm_source=github) v3 Backend, built with Go.

## Using the data

The drop data is public:

- **REST API** at `https://penguin-stats.io/PenguinStats/api/v2/`: see the
  [developer docs](https://developer.penguin-stats.io/public-api) and the
  [OpenAPI (Swagger 2.0) spec](https://penguin-stats.io/swagger/doc.json).
- **MCP server** for AI assistants (Claude, ChatGPT, Cursor, VS Code, Cherry Studio and others) at
  `https://mcp.penguin-stats.io/mcp`: Streamable HTTP, no authentication, read-only. In Claude Code:

  ```sh
  claude mcp add --transport http --scope user penguin-stats https://mcp.penguin-stats.io/mcp
  ```

  The steps for every other app, in English and Chinese, are at https://mcp.penguin-stats.io/.
- **Plugin and skill** for coding agents, the MCP server with a skill on reading, citing and querying the numbers:
  https://github.com/penguin-statistics/agent-skills

The drop data is licensed [CC BY-NC 4.0](https://creativecommons.org/licenses/by-nc/4.0/): non-commercial use, with
credit. Credit it as `Data: Penguin Statistics (penguin-stats.io), CC BY-NC 4.0`. The code of this repository is
[MIT](LICENSE).

## Architecture

This Go project mainly uses:

- [gofiber/fiber](https://github.com/gofiber/fiber)
- [uber-go/fx](https://github.com/uber-go/fx)
- [uptrace/bun](https://github.com/uptrace/bun)

This project mainly follows a RESTful API design.

## Operations

### Report tasks in the dead-letter stream

The report worker moves a report task out of the `penguin-reports` stream into `penguin-reports-dead` (subjects
`REPORT_DEAD.*`) when it can never be processed (it is not a task, or a stage of it does not exist) or when it failed at
each of its 30 deliveries, which take about 97 minutes of retry delays. Each move is logged at error level
(`reportwkr.malformed`, `reportwkr.parked` or `reportwkr.dead`). The copy keeps the payload as published, with these
headers:

- `Penguin-Original-Subject`: the subject it was published under, like `REPORT.SINGLE`
- `Penguin-Original-Sequence`: its sequence in `penguin-reports`
- `Penguin-Deliveries`: its deliveries when it moved
- `Penguin-Last-Error`: the error that moved it

A task too close to the maximum payload to fit with these headers moves without them, and its subject under
`REPORT_DEAD.*` names its original subject; its move is logged as `reportwkr.dead.bare`, with the sequence and the error.

Nothing in the stream expires. With the [nats CLI](https://github.com/nats-io/natscli), once the cause is fixed:

```sh
# list the tasks, with their headers
nats stream view penguin-reports-dead
# show one, by its sequence in penguin-reports-dead
nats stream get penguin-reports-dead 42
# replay it under its original subject: the worker processes it as a new task
nats stream get penguin-reports-dead 42 --json | jq -r .data | base64 -d \
  | nats pub REPORT.SINGLE --force-stdin
# remove it once replayed
nats stream rmm penguin-reports-dead 42 -f
```

A task that moved after failing at each delivery may have been stored by one of them whose commit outcome was unknown
(for example the connection dropped during the commit); check `drop_reports` for its account and time before replaying
it.

## Benchmarks

### Report consumer CPU

`internal/workers/reportwkr/consumerbench_test.go` (build tag `consumerbench`) drives the report consumer through its
real code path: each task is published to a JetStream stream of its own, fetched and handed to
`Worker.ingestPreprocess` (`BenchmarkConsumer/ingest`), which stores it in PostgreSQL and sets its recall key in Redis,
with logging and tracing configured as in production. `BenchmarkConsumer/process` runs `Worker.process` alone. Besides
wall time and allocations it reports, per task, the CPU time of the process (`cpu-ns/op`, user + system from
`getrusage`, which a shared machine disturbs far less than the wall time) and the time spent waiting on PostgreSQL
(`pg-wait-ns/op`, `pg-wait-%`) and Redis.

The tasks (`internal/workers/reportwkr/testdata/consumerbench/tasks.jsonl.gz`) are encoded as the report handler
publishes them, drawn from the stages, servers, times, sources, versions and drop patterns of recent production reports,
with synthetic accounts, IPs and task ids.

It needs a scratch database, Redis and NATS with JetStream. The scratch database holds the schema and the reference
tables of a restore of production (`penguinprod`, only read) and synthetic accounts; the benchmark truncates its
`drop_reports` and `drop_report_extras`:

```sh
scripts/consumerbench/setup-db.sh   # creates penguin_cpubench and the role cpubench on 127.0.0.1:5434
# regenerate the tasks, if needed: CONSUMERBENCH_TASKS, CONSUMERBENCH_BATCH_PERMILLE, CONSUMERBENCH_SEED
CONSUMERBENCH_GENERATE=1 go test -tags consumerbench -run TestConsumerBenchGenerate ./internal/workers/reportwkr/
```

The defaults point at `penguin_cpubench` on 127.0.0.1:5434, Redis DB 14 on 127.0.0.1:16379 and NATS on 127.0.0.1:4222
(`CONSUMERBENCH_POSTGRES_DSN`, `CONSUMERBENCH_REDIS_URL`, `CONSUMERBENCH_NATS_URL`). Then:

```sh
# one run
go test -tags consumerbench -run '^$' -bench BenchmarkConsumer -benchtime 2000x ./internal/workers/reportwkr/
# interleaved runs of variants, compared with benchstat: logging to a counting io.Discard (prod), to /dev/null
# (devnull), disabled (logoff); tracing as in production at 10% (prod), off (traceoff), at 100% (trace1); both off (off)
COUNT=10 scripts/consumerbench/run.sh bench prod logoff traceoff off devnull trace1
# CPU and allocation profiles of the ingestion, the top functions and the share of each part of the consumer
scripts/consumerbench/run.sh profile prod
```

Outputs go to `$OUT` (default a new temporary directory). Each run logs the load average and the queries other sessions
had active on the cluster to `$OUT/runs.log`. The PostgreSQL connection runs over loopback, where a write system call
also does the receiving side's TCP work, so the system calls weigh more than against a remote server.

## Maintainers

This project has mainly being maintained by the following contributors (in alphabetical order):

- [AlvISsReimu](https://github.com/AlvISsReimu)
- [GalvinGao](https://github.com/GalvinGao)

> The full list of active contributors of the _Penguin Statistics_ project can be found at the [Team Members page](https://penguin-stats.io/about/members) of the website.

## Contributors

This project is made possible by the following contributors. Contributions are always welcome!

[![Contributors](https://contrib.rocks/image?repo=penguin-statistics/backend-next)](https://github.com/penguin-statistics/backend-next/graphs/contributors)


## License
[![FOSSA Status](https://app.fossa.com/api/projects/git%2Bgithub.com%2Fpenguin-statistics%2Fbackend-next.svg?type=large)](https://app.fossa.com/projects/git%2Bgithub.com%2Fpenguin-statistics%2Fbackend-next?ref=badge_large)
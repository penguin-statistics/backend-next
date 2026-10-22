//go:build consumerbench

package reportwkr

// Consumer CPU benchmark: drives the report consumer on the tasks of consumerBenchFixtures through its real code path
// and dependencies (PostgreSQL, Redis, a JetStream stream of NATS), with logging and tracing configured as in
// production, and reports the CPU time of the process per task besides the wall time and the allocations.
//
//   - BenchmarkConsumer/ingest fetches each task from a stream of its own and hands it to Worker.ingestPreprocess, as
//     Worker.consume does: metadata, decode, span, process, the log of the processed task, AckSync, metrics. The tasks
//     are published before the timer starts (publishing is the report handler's work).
//   - BenchmarkConsumer/process runs Worker.process on tasks decoded before the timer starts, under a root span as
//     ingestPreprocess starts it: no NATS, no decode, no reportwkr.processed log.
//
// Metrics, per task: cpu-ns (user+sys CPU time of the whole process, from getrusage: every goroutine, the GC and the
// NATS, PostgreSQL and Redis clients included; the servers run in other processes), allocs and B (b.ReportAllocs,
// process-wide), pg-wait-ns and redis-wait-ns (wall time blocked reading from the PostgreSQL and Redis connections:
// the server's time and the network), pg-wait-% (its share of the wall time), pg-flushes (writes to the PostgreSQL
// connection, about one per round trip), log-B and log-lines (the JSON logged).
//
// Env:
//
//	CONSUMERBENCH_POSTGRES_DSN  default postgres://cpubench:cpubench@127.0.0.1:5434/penguin_cpubench?sslmode=disable
//	                            (scripts/consumerbench/setup-db.sh); drop_reports and drop_report_extras are truncated
//	CONSUMERBENCH_REDIS_URL     default redis://127.0.0.1:16379/14; the recall keys of the fixtures are deleted after
//	CONSUMERBENCH_NATS_URL      default nats://127.0.0.1:4222; the streams of the run are deleted after
//	CONSUMERBENCH_LOG           discard (default: the production JSON logger, to a byte counter), devnull (the same
//	                            to /dev/null: a write system call per line, as to stdout in production), off (level
//	                            disabled)
//	CONSUMERBENCH_TRACE         prod (default: a tracer provider configured as in production, with a batcher whose
//	                            exporter drops the spans, and the bunotel query hook), off (no provider, no hook)
//	CONSUMERBENCH_TRACE_RATE    the sample rate of prod, default 0.1 as in production; also the share of the tasks
//	                            published with a sampled trace context (each task carries one, as service.Report
//	                            publishes it in the trace of its request, which is sampled at that rate)
//
// See scripts/consumerbench/run.sh to run it, and README.md.

import (
	"context"
	"database/sql"
	"encoding/binary"
	"io"
	"math/rand"
	"net"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/goccy/go-json"
	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/rs/zerolog/pkgerrors"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
	"github.com/uptrace/bun/extra/bunotel"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/resource"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
	"go.opentelemetry.io/otel/trace"

	"exusiai.dev/backend-next/internal/infra"
	"exusiai.dev/backend-next/internal/model/cache"
	"exusiai.dev/backend-next/internal/model/types"
	"exusiai.dev/backend-next/internal/pkg/jetstream"
	"exusiai.dev/backend-next/internal/pkg/observability"
	"exusiai.dev/backend-next/internal/repo"
	"exusiai.dev/backend-next/internal/service"
)

const (
	defaultBenchDSN      = "postgres://cpubench:cpubench@127.0.0.1:5434/penguin_cpubench?sslmode=disable"
	defaultBenchRedisURL = "redis://127.0.0.1:16379/14"
	defaultBenchNatsURL  = "nats://127.0.0.1:4222"
)

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// waitCounter sums the time spent blocked reading from connections, and counts the writes to them.
type waitCounter struct {
	wait   atomic.Int64
	writes atomic.Int64
}

type waitConn struct {
	net.Conn
	c *waitCounter
}

func (c waitConn) Read(p []byte) (int, error) {
	start := time.Now()
	n, err := c.Conn.Read(p)
	c.c.wait.Add(int64(time.Since(start)))
	return n, err
}

func (c waitConn) Write(p []byte) (int, error) {
	c.c.writes.Add(1)
	return c.Conn.Write(p)
}

// SyscallConn keeps the health check of go-redis, which needs the raw connection.
func (c waitConn) SyscallConn() (syscall.RawConn, error) {
	return c.Conn.(syscall.Conn).SyscallConn()
}

func (c *waitCounter) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	d := net.Dialer{Timeout: 5 * time.Second, KeepAlive: 5 * time.Minute}
	conn, err := d.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	return waitConn{Conn: conn, c: c}, nil
}

// countingWriter counts what is logged, and passes it on.
type countingWriter struct {
	w     io.Writer
	bytes atomic.Int64
	lines atomic.Int64
}

func (w *countingWriter) Write(p []byte) (int, error) {
	w.bytes.Add(int64(len(p)))
	w.lines.Add(1)
	return w.w.Write(p)
}

// discardExporter drops the spans the batcher exports. Production exports them over OTLP gRPC, whose encoding and
// sending is left out.
type discardExporter struct{}

func (discardExporter) ExportSpans(context.Context, []tracesdk.ReadOnlySpan) error { return nil }
func (discardExporter) Shutdown(context.Context) error                             { return nil }

// prefixSampler is the sampler of production (httpserver.alwaysSampleNamePrefix).
type prefixSampler struct {
	prefix   string
	fallback tracesdk.Sampler
}

func (s prefixSampler) ShouldSample(p tracesdk.SamplingParameters) tracesdk.SamplingResult {
	if len(p.Name) >= len(s.prefix) && p.Name[:len(s.prefix)] == s.prefix {
		return tracesdk.AlwaysSample().ShouldSample(p)
	}
	return s.fallback.ShouldSample(p)
}

func (s prefixSampler) Description() string { return "prefixSampler" }

// openBenchDB opens CONSUMERBENCH_POSTGRES_DSN as infra.Postgres does, its connections counted by pg if not nil. It
// refuses the production database.
func openBenchDB(t testing.TB, pg *waitCounter) *bun.DB {
	dsn := envOr("CONSUMERBENCH_POSTGRES_DSN", defaultBenchDSN)
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if u.Path == "/penguinprod" {
		t.Fatal("refusing to write to penguinprod: point CONSUMERBENCH_POSTGRES_DSN at a scratch database")
	}
	opts := []pgdriver.Option{
		pgdriver.WithReadTimeout(5 * time.Minute), pgdriver.WithDSN(dsn), pgdriver.WithApplicationName("penguin-consumerbench"),
	}
	if pg != nil {
		opts = append(opts, func(cfg *pgdriver.Config) { cfg.Dialer = pg.dial })
	}
	db := bun.NewDB(sql.OpenDB(pgdriver.NewConnector(opts...)), pgdialect.New())
	var name string
	if err := db.QueryRow("SELECT current_database()").Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name == "penguinprod" {
		t.Fatal("refusing to write to penguinprod")
	}
	return db
}

type consumerBench struct {
	w        *Worker
	sub      *nats.Subscription
	js       nats.JetStreamContext
	nc       *nats.Conn
	conf     consumerConfig
	fixtures []consumerBenchFixture
	next     int // the next fixture to publish
	// the share of the tasks published with a sampled trace context
	traceRate float64

	pg, redis waitCounter
	logs      *countingWriter
	tp        *tracesdk.TracerProvider
}

var (
	benchMu  sync.Mutex
	theBench *consumerBench
)

func TestMain(m *testing.M) {
	code := m.Run()
	if theBench != nil {
		theBench.close()
	}
	os.Exit(code)
}

// getConsumerBench sets the benchmark up once per process.
func getConsumerBench(b *testing.B) *consumerBench {
	benchMu.Lock()
	defer benchMu.Unlock()
	if theBench == nil {
		theBench = newConsumerBench(b)
	}
	return theBench
}

func newConsumerBench(b *testing.B) *consumerBench {
	cb := &consumerBench{fixtures: loadConsumerBenchFixtures(b)}

	// logging: logger.Configure of production (LogJsonStdout, not DevMode)
	zerolog.TimeFieldFormat = time.RFC3339Nano
	zerolog.DurationFieldUnit = time.Nanosecond
	zerolog.ErrorStackMarshaler = pkgerrors.MarshalStack
	level := zerolog.InfoLevel
	var out io.Writer = io.Discard
	switch mode := envOr("CONSUMERBENCH_LOG", "discard"); mode {
	case "discard":
	case "devnull":
		f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		if err != nil {
			b.Fatal(err)
		}
		out = f
	case "off":
		level = zerolog.Disabled
	default:
		b.Fatalf("CONSUMERBENCH_LOG=%s", mode)
	}
	cb.logs = &countingWriter{w: out}
	log.Logger = zerolog.New(cb.logs).With().Timestamp().Caller().Logger().Level(level)

	db := openBenchDB(b, &cb.pg)
	db.SetMaxOpenConns(20) // the defaults of appconfig
	db.SetMaxIdleConns(20)

	// tracing: httpserver.Create and infra.Postgres of production (TracingEnabled)
	rate, err := strconv.ParseFloat(envOr("CONSUMERBENCH_TRACE_RATE", "0.1"), 64)
	if err != nil {
		b.Fatal(err)
	}
	cb.traceRate = rate
	switch mode := envOr("CONSUMERBENCH_TRACE", "prod"); mode {
	case "prod":
		hostname, _ := os.Hostname()
		cb.tp = tracesdk.NewTracerProvider(
			tracesdk.WithResource(resource.NewWithAttributes(
				semconv.SchemaURL,
				semconv.ServiceNameKey.String("pgbackend"),
				semconv.ServiceVersionKey.String("consumerbench"),
				semconv.ServiceInstanceIDKey.String(hostname),
				semconv.DeploymentEnvironmentKey.String("prod"),
			)),
			tracesdk.WithSampler(tracesdk.ParentBased(prefixSampler{prefix: "calcwkr.", fallback: tracesdk.TraceIDRatioBased(rate)})),
			tracesdk.WithBatcher(discardExporter{}),
		)
		otel.SetTracerProvider(cb.tp)
		db.AddQueryHook(bunotel.NewQueryHook(bunotel.WithDBName("penguin-postgres"), bunotel.WithAttributes(semconv.DBSystemPostgreSQL)))
	case "off":
	default:
		b.Fatalf("CONSUMERBENCH_TRACE=%s", mode)
	}

	ctx := context.Background()
	// the rows of earlier runs: the tables stay the size of a fresh run
	if _, err := db.ExecContext(ctx, "TRUNCATE drop_reports, drop_report_extras"); err != nil {
		b.Fatal(err)
	}

	redisOpts, err := redis.ParseURL(envOr("CONSUMERBENCH_REDIS_URL", defaultBenchRedisURL))
	if err != nil {
		b.Fatal(err)
	}
	redisOpts.Dialer = cb.redis.dial
	rdb := redis.NewClient(redisOpts)
	if err := rdb.Ping(ctx).Err(); err != nil {
		b.Fatal(err)
	}

	cb.nc, err = nats.Connect(envOr("CONSUMERBENCH_NATS_URL", defaultBenchNatsURL), infra.NATSOptions()...)
	if err != nil {
		b.Fatal(err)
	}
	cb.js, err = cb.nc.JetStream()
	if err != nil {
		b.Fatal(err)
	}
	id := strconv.FormatInt(time.Now().UnixNano(), 36)
	cb.conf = defaultConsumerConfig
	cb.conf.stream = "CPUBENCH_" + id
	cb.conf.durable = "cpubench-pull"
	cb.conf.legacyDurable = "cpubench-push"
	cb.conf.subject = "CPUBENCH" + id + ".*"
	cb.conf.deadSubject = "CPUBENCHDEAD" + id + "."
	cb.conf.workers = 1
	for _, cfg := range []*nats.StreamConfig{
		{Name: cb.conf.stream, Subjects: []string{cb.conf.subject}, Retention: nats.WorkQueuePolicy, Storage: nats.FileStorage, MaxAge: time.Hour},
		{Name: cb.conf.stream + "_DEAD", Subjects: []string{cb.conf.deadSubject + "*"}, Storage: nats.FileStorage, MaxAge: time.Hour},
	} {
		if _, err := cb.js.AddStream(cfg); err != nil {
			b.Fatal(err)
		}
	}

	cache.Initialize()
	cb.w = newWorker(WorkerDeps{
		DB:                     db,
		Redis:                  rdb,
		NatsJS:                 cb.js,
		NatsConn:               cb.nc,
		StageService:           service.NewStage(repo.NewStage(db)),
		DropReportRepo:         repo.NewDropReport(db),
		DropPatternRepo:        repo.NewDropPattern(db),
		DropReportExtraRepo:    repo.NewDropReportExtra(db),
		DropPatternElementRepo: repo.NewDropPatternElement(db),
		DropInfoRepo:           repo.NewDropInfo(db),
		RejectRuleRepo:         repo.NewRejectRule(db),
	}, cb.conf)
	if err := cb.w.subscribe(); err != nil {
		b.Fatal(err)
	}
	cb.sub = cb.w.subs[0]

	// warm up: connections, the stage cache, the page cache of the server
	cb.publish(b, len(cb.fixtures))
	cb.ingest(b, len(cb.fixtures))
	return cb
}

func (cb *consumerBench) close() {
	ctx := context.Background()
	for _, stream := range []string{cb.conf.stream, cb.conf.stream + "_DEAD"} {
		_ = cb.js.DeleteStream(stream)
	}
	cb.nc.Close()
	for _, fixture := range cb.fixtures {
		var task types.ReportTask
		if json.Unmarshal(fixture.Data, &task) == nil {
			cb.w.Redis.Del(ctx, constant.ReportRedisPrefix+task.TaskID)
		}
	}
	cb.w.Redis.Close()
	if cb.tp != nil {
		_ = cb.tp.Shutdown(ctx)
	}
	cb.w.DB.Close()
}

// publish publishes the next n fixtures, cycling, and waits until the stream stored them.
func (cb *consumerBench) publish(b *testing.B, n int) {
	futures := make([]nats.PubAckFuture, 0, n)
	prefix := cb.conf.subject[:len(cb.conf.subject)-1]
	for i := 0; i < n; i++ {
		fixture := cb.fixtures[cb.next%len(cb.fixtures)]
		cb.next++
		subject := prefix + fixture.Subject[len("REPORT."):]
		// the trace context of the publish span, as service.Report sets it: a trace of its own per task, a share
		// traceRate of them sampled
		var flags trace.TraceFlags
		if int(float64(cb.next)*cb.traceRate) > int(float64(cb.next-1)*cb.traceRate) {
			flags = trace.FlagsSampled
		}
		var traceID trace.TraceID
		var spanID trace.SpanID
		binary.BigEndian.PutUint64(traceID[:8], rand.Uint64())
		binary.BigEndian.PutUint64(traceID[8:], rand.Uint64())
		binary.BigEndian.PutUint64(spanID[:], rand.Uint64()|1)
		msg := &nats.Msg{Subject: subject, Data: fixture.Data, Header: nats.Header{}}
		jetstream.InjectTraceContext(trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
			TraceID: traceID, SpanID: spanID, TraceFlags: flags,
		})), msg.Header)
		f, err := cb.js.PublishMsgAsync(msg)
		if err != nil {
			b.Fatal(err)
		}
		futures = append(futures, f)
	}
	for _, f := range futures {
		select {
		case <-f.Ok():
		case err := <-f.Err():
			b.Fatal(err)
		case <-time.After(30 * time.Second):
			b.Fatal("publish timed out")
		}
	}
}

// ingest fetches n tasks and ingests each, as Worker.consume does, and fails if one was not stored.
func (cb *consumerBench) ingest(b *testing.B, n int) {
	stored := testutil.ToFloat64(observability.ReportConsumeTotal.WithLabelValues("stored", "other"))
	for i := 0; i < n; i++ {
		msgs, err := cb.sub.Fetch(1, nats.MaxWait(10*time.Second))
		if err != nil {
			b.Fatal(err)
		}
		cb.w.ingestPreprocess(msgs[0])
	}
	if got := testutil.ToFloat64(observability.ReportConsumeTotal.WithLabelValues("stored", "other")) - stored; got != float64(n) {
		b.Fatalf("%v of %d tasks stored", got, n)
	}
}

type benchSnapshot struct {
	cpu, pgWait, redisWait, pgWrites, logBytes, logLines int64
	wall                                                 time.Time
}

func (cb *consumerBench) snapshot() benchSnapshot {
	var ru syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	return benchSnapshot{
		cpu:       ru.Utime.Nano() + ru.Stime.Nano(),
		pgWait:    cb.pg.wait.Load(),
		redisWait: cb.redis.wait.Load(),
		pgWrites:  cb.pg.writes.Load(),
		logBytes:  cb.logs.bytes.Load(),
		logLines:  cb.logs.lines.Load(),
		wall:      time.Now(),
	}
}

func (cb *consumerBench) report(b *testing.B, start benchSnapshot) {
	end := cb.snapshot()
	n := float64(b.N)
	b.ReportMetric(float64(end.cpu-start.cpu)/n, "cpu-ns/op")
	b.ReportMetric(float64(end.pgWait-start.pgWait)/n, "pg-wait-ns/op")
	b.ReportMetric(100*float64(end.pgWait-start.pgWait)/float64(end.wall.Sub(start.wall)), "pg-wait-%")
	b.ReportMetric(float64(end.redisWait-start.redisWait)/n, "redis-wait-ns/op")
	b.ReportMetric(float64(end.pgWrites-start.pgWrites)/n, "pg-flushes/op")
	b.ReportMetric(float64(end.logBytes-start.logBytes)/n, "log-B/op")
	b.ReportMetric(float64(end.logLines-start.logLines)/n, "log-lines/op")
}

func BenchmarkConsumer(b *testing.B) {
	cb := getConsumerBench(b)

	b.Run("ingest", func(b *testing.B) {
		cb.publish(b, b.N)
		runtime.GC()
		b.ReportAllocs()
		b.ResetTimer()
		start := cb.snapshot()
		cb.ingest(b, b.N)
		b.StopTimer()
		cb.report(b, start)
	})

	b.Run("process", func(b *testing.B) {
		tasks := make([]*types.ReportTask, b.N)
		for i := range tasks {
			tasks[i] = &types.ReportTask{}
			if err := json.Unmarshal(cb.fixtures[(cb.next+i)%len(cb.fixtures)].Data, tasks[i]); err != nil {
				b.Fatal(err)
			}
		}
		cb.next += b.N
		runtime.GC()
		b.ReportAllocs()
		b.ResetTimer()
		start := cb.snapshot()
		for _, task := range tasks {
			ctx, cancel := context.WithTimeout(context.Background(), cb.conf.taskTimeout)
			ctx, span := tracer.Start(ctx, "reportwkr.ConsumeTask", trace.WithSpanKind(trace.SpanKindConsumer))
			err := cb.w.process(ctx, task)
			span.End()
			cancel()
			if err != nil {
				b.Fatal(err)
			}
		}
		b.StopTimer()
		cb.report(b, start)
	})
}

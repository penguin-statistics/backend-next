package infra

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/redis/go-redis/v9"

	"exusiai.dev/backend-next/internal/app/appconfig"
	"exusiai.dev/backend-next/internal/pkg/observability"
)

// useTestRegistry makes the constructors register their metrics on a registry of the test's own.
func useTestRegistry(t *testing.T) *prometheus.Registry {
	t.Helper()
	reg := prometheus.NewPedanticRegistry()
	old := metricsRegisterer
	metricsRegisterer = reg
	t.Cleanup(func() { metricsRegisterer = old })
	return reg
}

// gatheredValue returns the value of the first series of the metric called name.
func gatheredValue(t *testing.T, g prometheus.Gatherer, name string) float64 {
	t.Helper()
	families, err := g.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range families {
		if mf.GetName() != name {
			continue
		}
		m := mf.GetMetric()[0]
		if m.GetGauge() != nil {
			return m.GetGauge().GetValue()
		}
		return m.GetCounter().GetValue()
	}
	t.Fatalf("metric %s was not gathered", name)
	return 0
}

// TestPostgresPoolMetrics needs PENGUIN_TEST_POSTGRES_DSN, a disposable database (see internal/repo/integration_test.go).
// It only takes a connection, and reads and writes nothing.
func TestPostgresPoolMetrics(t *testing.T) {
	dsn := os.Getenv("PENGUIN_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PENGUIN_TEST_POSTGRES_DSN is not set")
	}
	reg := useTestRegistry(t)

	db, err := Postgres(&appconfig.Config{ConfigSpec: appconfig.ConfigSpec{
		PostgresDSN: dsn, PostgresMaxOpenConns: 3, PostgresMaxIdleConns: 3,
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if v := gatheredValue(t, reg, "penguinbackend_go_sql_max_open_connections"); v != 3 {
		t.Fatalf("max open connections = %v, want 3", v)
	}
	if v := gatheredValue(t, reg, "penguinbackend_go_sql_in_use_connections"); v != 0 {
		t.Fatalf("in use connections = %v, want 0", v)
	}

	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v := gatheredValue(t, reg, "penguinbackend_go_sql_in_use_connections"); v != 1 {
		t.Fatalf("in use connections while holding a connection = %v, want 1", v)
	}
	conn.Close()
	if v := gatheredValue(t, reg, "penguinbackend_go_sql_idle_connections"); v != 1 {
		t.Fatalf("idle connections after releasing it = %v, want 1", v)
	}
}

// TestRedisPoolMetrics needs PENGUIN_TEST_REDIS_URL, a disposable Redis (see internal/workers/calcwkr/lock_test.go).
// It only sends PINGs.
func TestRedisPoolMetrics(t *testing.T) {
	url := os.Getenv("PENGUIN_TEST_REDIS_URL")
	if url == "" {
		t.Skip("PENGUIN_TEST_REDIS_URL is not set")
	}
	reg := useTestRegistry(t)

	client, err := Redis(&appconfig.Config{ConfigSpec: appconfig.ConfigSpec{RedisURL: url}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	// the boot ping uses a client of its own, so the first PING opens the pool's first connection
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	if v := gatheredValue(t, reg, "penguinbackend_redis_pool_total_conns"); v != 1 {
		t.Fatalf("total conns = %v, want 1", v)
	}
	hits := gatheredValue(t, reg, "penguinbackend_redis_pool_hits_total")
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	if v := gatheredValue(t, reg, "penguinbackend_redis_pool_hits_total"); v != hits+1 {
		t.Fatalf("hits after reusing the idle connection = %v, want %v", v, hits+1)
	}
}

type fakeRedisPool struct{ stats redis.PoolStats }

func (f *fakeRedisPool) PoolStats() *redis.PoolStats { return &f.stats }

func TestRedisPoolCollector(t *testing.T) {
	pool := &fakeRedisPool{}
	c := newRedisPoolCollector(pool)

	pool.stats = redis.PoolStats{Hits: 5, Misses: 2, Timeouts: 1, TotalConns: 4, IdleConns: 3, StaleConns: 6}
	want := `
# HELP penguinbackend_redis_pool_hits_total Number of times a free connection was found in the Redis pool
# TYPE penguinbackend_redis_pool_hits_total counter
penguinbackend_redis_pool_hits_total 5
# HELP penguinbackend_redis_pool_idle_conns Number of idle connections in the Redis pool
# TYPE penguinbackend_redis_pool_idle_conns gauge
penguinbackend_redis_pool_idle_conns 3
# HELP penguinbackend_redis_pool_misses_total Number of times a free connection was not found in the Redis pool
# TYPE penguinbackend_redis_pool_misses_total counter
penguinbackend_redis_pool_misses_total 2
# HELP penguinbackend_redis_pool_stale_conns_total Number of stale connections removed from the Redis pool
# TYPE penguinbackend_redis_pool_stale_conns_total counter
penguinbackend_redis_pool_stale_conns_total 6
# HELP penguinbackend_redis_pool_timeouts_total Number of times waiting for a Redis pool connection timed out
# TYPE penguinbackend_redis_pool_timeouts_total counter
penguinbackend_redis_pool_timeouts_total 1
# HELP penguinbackend_redis_pool_total_conns Number of connections in the Redis pool
# TYPE penguinbackend_redis_pool_total_conns gauge
penguinbackend_redis_pool_total_conns 4
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(want)); err != nil {
		t.Fatal(err)
	}
}

type fakeNatsConn struct {
	status nats.Status
	stats  nats.Statistics
}

func (f *fakeNatsConn) Status() nats.Status    { return f.status }
func (f *fakeNatsConn) Stats() nats.Statistics { return f.stats }

// natsConnMetrics is the expected exposition of the NATS connection collector.
func natsConnMetrics(state string, reconnects int) string {
	var b strings.Builder
	b.WriteString("# HELP penguinbackend_nats_connection_state 1 for the current state of the NATS connection, 0 for the other states\n")
	b.WriteString("# TYPE penguinbackend_nats_connection_state gauge\n")
	for _, s := range []string{"closed", "connected", "connecting", "disconnected", "draining_pubs", "draining_subs", "reconnecting"} {
		v := "0"
		if s == state {
			v = "1"
		}
		b.WriteString(`penguinbackend_nats_connection_state{state="` + s + `"} ` + v + "\n")
	}
	b.WriteString("# HELP penguinbackend_nats_reconnects_total Number of NATS reconnect attempts that reached the server\n")
	b.WriteString("# TYPE penguinbackend_nats_reconnects_total counter\n")
	b.WriteString("penguinbackend_nats_reconnects_total " + strconv.Itoa(reconnects) + "\n")
	return b.String()
}

func TestNatsConnCollector(t *testing.T) {
	conn := &fakeNatsConn{status: nats.CONNECTED}
	c := newNatsConnCollector(conn)

	if err := testutil.CollectAndCompare(c, strings.NewReader(natsConnMetrics("connected", 0))); err != nil {
		t.Fatal(err)
	}

	conn.status = nats.RECONNECTING
	if err := testutil.CollectAndCompare(c, strings.NewReader(natsConnMetrics("reconnecting", 0))); err != nil {
		t.Fatal(err)
	}

	conn.status = nats.CONNECTED
	conn.stats.Reconnects = 1
	if err := testutil.CollectAndCompare(c, strings.NewReader(natsConnMetrics("connected", 1))); err != nil {
		t.Fatal(err)
	}

	conn.status = nats.CLOSED
	if err := testutil.CollectAndCompare(c, strings.NewReader(natsConnMetrics("closed", 1))); err != nil {
		t.Fatal(err)
	}
}

// TestNatsConnectMetrics needs PENGUIN_TEST_NATS_URL, a disposable NATS server. It only connects: NATS() itself
// is not called, as it creates the penguin-reports stream.
func TestNatsConnectMetrics(t *testing.T) {
	url := os.Getenv("PENGUIN_TEST_NATS_URL")
	if url == "" {
		t.Skip("PENGUIN_TEST_NATS_URL is not set")
	}
	reg := useTestRegistry(t)

	nc, err := natsConnect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()

	names := []string{"penguinbackend_nats_connection_state", "penguinbackend_nats_reconnects_total"}
	if err := testutil.GatherAndCompare(reg, strings.NewReader(natsConnMetrics("connected", 0)), names...); err != nil {
		t.Fatal(err)
	}
	nc.Close()
	if err := testutil.GatherAndCompare(reg, strings.NewReader(natsConnMetrics("closed", 0)), names...); err != nil {
		t.Fatal(err)
	}
}

func TestNatsErrorHandlerCountsErrors(t *testing.T) {
	before := testutil.ToFloat64(observability.NatsAsyncErrors)
	natsErrorHandler(nil, &nats.Subscription{Subject: "REPORT.test"}, nats.ErrSlowConsumer)
	if v := testutil.ToFloat64(observability.NatsAsyncErrors); v != before+1 {
		t.Fatalf("async errors = %v, want %v", v, before+1)
	}
}

// nats.go reports connection-level errors without a subscription, see processPermissionsViolation and
// processAuthError.
func TestNatsErrorHandlerWithoutSubscription(t *testing.T) {
	before := testutil.ToFloat64(observability.NatsAsyncErrors)
	natsErrorHandler(nil, nil, nats.ErrAuthorization)
	if v := testutil.ToFloat64(observability.NatsAsyncErrors); v != before+1 {
		t.Fatalf("async errors = %v, want %v", v, before+1)
	}
}

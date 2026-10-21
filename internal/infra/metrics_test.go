package infra

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/redis/go-redis/v9"

	"exusiai.dev/backend-next/internal/app/appconfig"
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

	// the constructor's PING opened the first connection
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

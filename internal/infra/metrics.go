package infra

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"

	"exusiai.dev/backend-next/internal/pkg/observability"
)

// metricsRegisterer is the default registry, which is served at /metrics on the http port.
// Tests replace it with their own registry.
var metricsRegisterer prometheus.Registerer = prometheus.DefaultRegisterer

// registerMetrics registers c on reg. A failure is only logged: metrics must never stop the app from
// starting, e.g. when a second app is built in the same process by a test.
func registerMetrics(name string, reg prometheus.Registerer, c prometheus.Collector) {
	if err := reg.Register(c); err != nil {
		log.Warn().
			Err(err).
			Str("evt.name", "infra.metrics.register_failed").
			Str("collector", name).
			Msg("failed to register metrics collector")
	}
}

// redisPoolStater is implemented by *redis.Client.
type redisPoolStater interface {
	PoolStats() *redis.PoolStats
}

// redisPoolCollector exports the connection pool stats of a Redis client on every scrape.
type redisPoolCollector struct {
	client redisPoolStater

	hits, misses, timeouts, totalConns, idleConns, staleConns *prometheus.Desc
}

func newRedisPoolCollector(client redisPoolStater) *redisPoolCollector {
	desc := func(name, help string) *prometheus.Desc {
		return prometheus.NewDesc(prometheus.BuildFQName(observability.ServiceName, "redis_pool", name), help, nil, nil)
	}
	return &redisPoolCollector{
		client:     client,
		hits:       desc("hits_total", "Number of times a free connection was found in the Redis pool"),
		misses:     desc("misses_total", "Number of times a free connection was not found in the Redis pool"),
		timeouts:   desc("timeouts_total", "Number of times waiting for a Redis pool connection timed out"),
		totalConns: desc("total_conns", "Number of connections in the Redis pool"),
		idleConns:  desc("idle_conns", "Number of idle connections in the Redis pool"),
		staleConns: desc("stale_conns_total", "Number of stale connections removed from the Redis pool"),
	}
}

func (c *redisPoolCollector) Describe(ch chan<- *prometheus.Desc) {
	prometheus.DescribeByCollect(c, ch)
}

func (c *redisPoolCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.client.PoolStats()
	ch <- prometheus.MustNewConstMetric(c.hits, prometheus.CounterValue, float64(s.Hits))
	ch <- prometheus.MustNewConstMetric(c.misses, prometheus.CounterValue, float64(s.Misses))
	ch <- prometheus.MustNewConstMetric(c.timeouts, prometheus.CounterValue, float64(s.Timeouts))
	ch <- prometheus.MustNewConstMetric(c.totalConns, prometheus.GaugeValue, float64(s.TotalConns))
	ch <- prometheus.MustNewConstMetric(c.idleConns, prometheus.GaugeValue, float64(s.IdleConns))
	ch <- prometheus.MustNewConstMetric(c.staleConns, prometheus.CounterValue, float64(s.StaleConns))
}

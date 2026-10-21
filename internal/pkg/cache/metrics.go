package cache

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"exusiai.dev/backend-next/internal/pkg/observability"
)

// metrics records the metrics of a cache instance, labelled with the name the cache is constructed with. The hit
// and miss counters are resolved once, since lookups are on the path of most requests.
type metrics struct {
	name   string
	hits   prometheus.Counter
	misses prometheus.Counter
}

func newMetrics(name string) metrics {
	return metrics{
		name:   name,
		hits:   observability.CacheLookups.WithLabelValues(name, "hit"),
		misses: observability.CacheLookups.WithLabelValues(name, "miss"),
	}
}

func (m metrics) lookup(hit bool) {
	if hit {
		m.hits.Inc()
	} else {
		m.misses.Inc()
	}
}

func (m metrics) calculated(start time.Time) {
	observability.CacheCalcDuration.WithLabelValues(m.name).Observe(time.Since(start).Seconds())
}

func (m metrics) write(op string) {
	observability.CacheWrites.WithLabelValues(m.name, op).Inc()
}

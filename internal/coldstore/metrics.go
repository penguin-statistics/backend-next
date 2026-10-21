package coldstore

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"exusiai.dev/backend-next/internal/pkg/observability"
)

var (
	metricHotBoundary = promauto.NewGauge(prometheus.GaugeOpts{
		Name: prometheus.BuildFQName(observability.ServiceName, "coldstore", "hot_boundary_timestamp_seconds"),
		Help: "First instant that is not archived in the cold storage the app serves (0: nothing archived)",
	})
	metricReloadFailures = promauto.NewCounter(prometheus.CounterOpts{
		Name: prometheus.BuildFQName(observability.ServiceName, "coldstore", "manifest_reload_failures_total"),
		Help: "Changed cold storage manifests that could not be served (a file missing or of the wrong size, a month missing)",
	})
	metricIntegrityFiles = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName(observability.ServiceName, "coldstore", "integrity_files_total"),
		Help: "Cold storage files hashed by the integrity check by result (ok, mismatch, missing)",
	}, []string{"result"})
	metricIntegrityLastMismatches = promauto.NewGauge(prometheus.GaugeOpts{
		Name: prometheus.BuildFQName(observability.ServiceName, "coldstore", "integrity_last_bad_files"),
		Help: "Cold storage files that were missing or did not match the manifest's SHA-256 in the last completed integrity check",
	})
	metricIntegrityLastCompleted = promauto.NewGauge(prometheus.GaugeOpts{
		Name: prometheus.BuildFQName(observability.ServiceName, "coldstore", "integrity_last_completed_timestamp_seconds"),
		Help: "Time the last integrity check of the cold storage completed",
	})
	metricQueryDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    prometheus.BuildFQName(observability.ServiceName, "coldstore", "query_duration_seconds"),
		Help:    "Duration of the queries on the cold storage by query and result (ok, error) in seconds",
		Buckets: prometheus.ExponentialBuckets(0.01, 2, 14),
	}, []string{"query", "result"})
)

// ObserveQuery records the duration of a query on the cold storage that started at started and ended with err.
func ObserveQuery(query string, started time.Time, err error) {
	result := "ok"
	if err != nil {
		result = "error"
	}
	metricQueryDuration.WithLabelValues(query, result).Observe(time.Since(started).Seconds())
}

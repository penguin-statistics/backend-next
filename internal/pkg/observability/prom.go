package observability

import (
	"exusiai.dev/gommon/constant"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	ServiceName = "penguinbackend"
)

var (
	ReportVerifyDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    prometheus.BuildFQName(ServiceName, "report", "verify_duration_seconds"),
		Help:    "Duration of report verification in seconds",
		Buckets: prometheus.ExponentialBuckets(0.01, 2, 10),
	}, []string{"verifier"})
	ReportConsumeDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    prometheus.BuildFQName(ServiceName, "report", "consume_duration_seconds"),
		Help:    "Duration of report consumption in seconds",
		Buckets: prometheus.ExponentialBuckets(0.01, 2, 10),
	}, []string{})
	ReportConsumeMessagingLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    prometheus.BuildFQName(ServiceName, "report", "consume_messaging_latency_seconds"),
		Help:    "Messaging latency of report consumption in seconds",
		Buckets: prometheus.ExponentialBuckets(0.01, 2, 10),
	}, []string{})
	ReportReliability = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName(ServiceName, "report", "reliability"),
		Help: "Reliability distribution of report consumption",
	}, []string{"reliability", "source_name"})
	ReportConsumeTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName(ServiceName, "report", "consume_total"),
		Help: "Consumed report task messages by outcome (stored, failed_unmarshal, failed_process) and subject",
	}, []string{"outcome", "subject"})
	ReportConsumeNumDelivered = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    prometheus.BuildFQName(ServiceName, "report", "consume_num_delivered"),
		Help:    "Delivery count of each consumed report task message",
		Buckets: []float64{1, 2, 3, 5, 10, 20},
	})
	ReportConsumeAckFailures = promauto.NewCounter(prometheus.CounterOpts{
		Name: prometheus.BuildFQName(ServiceName, "report", "consume_ack_failures_total"),
		Help: "Acks of consumed report task messages that failed",
	})
	ReportPublishDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    prometheus.BuildFQName(ServiceName, "report", "publish_duration_seconds"),
		Help:    "Duration of publishing a report task until it is acknowledged, fails or is abandoned in seconds",
		Buckets: prometheus.ExponentialBuckets(0.001, 2, 15),
	})
	ReportPublishTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName(ServiceName, "report", "publish_total"),
		Help: "Report task publishes by result (ok, error, timeout, ctx_cancelled)",
	}, []string{"result"})
	ReportStoredTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName(ServiceName, "report", "stored_total"),
		Help: "Stored reports by source category (maa, frontend, other)",
	}, []string{"source"})
	WorkerCalcDuration = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: prometheus.BuildFQName(ServiceName, "worker", "calc_duration_seconds"),
		Help: "Duration of last worker calculation in seconds",
	}, []string{"service", "server"})
	NatsAsyncErrors = promauto.NewCounter(prometheus.CounterOpts{
		Name: prometheus.BuildFQName(ServiceName, "nats", "async_errors_total"),
		Help: "Number of asynchronous errors reported by the NATS connection",
	})
	// the cache label is the name a cache is constructed with (a constant per cache instance), so it stays bounded
	CacheLookups = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName(ServiceName, "cache", "lookups_total"),
		Help: "Cache lookups by result: hit (served from cache) or miss (not found, or calculated by MutexGetSet)",
	}, []string{"cache", "result"})
	CacheCalcDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    prometheus.BuildFQName(ServiceName, "cache", "calc_duration_seconds"),
		Help:    "Duration of the value calculation of MutexGetSet on a miss in seconds",
		Buckets: prometheus.ExponentialBuckets(0.001, 4, 7),
	}, []string{"cache"})
	CacheWrites = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName(ServiceName, "cache", "writes_total"),
		Help: "Cache Set, Delete and Flush calls",
	}, []string{"cache", "op"})
	AdvancedQueryInFlight = promauto.NewGauge(prometheus.GaugeOpts{
		Name: prometheus.BuildFQName(ServiceName, "advanced_query", "in_flight_requests"),
		Help: "Number of advanced query requests being handled, including any waiting in middlewares before the handler",
	})
	AdvancedQueryDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    prometheus.BuildFQName(ServiceName, "advanced_query", "duration_seconds"),
		Help:    "Duration of advanced query requests in seconds",
		Buckets: prometheus.ExponentialBuckets(0.05, 2, 12),
	}, []string{"outcome"})
	AdvancedQueryRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName(ServiceName, "advanced_query", "requests_total"),
		Help: "Number of advanced query requests handled, by outcome (ok, client_error, error)",
	}, []string{"outcome"})
	AdvancedQueryQueriesPerRequest = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    prometheus.BuildFQName(ServiceName, "advanced_query", "queries_per_request"),
		Help:    "Number of queries in a valid advanced query request",
		Buckets: prometheus.LinearBuckets(1, 1, 5),
	})
)

// ReportSubjectLabel bounds the subject label of the report metrics to the subjects reports are published to.
func ReportSubjectLabel(subject string) string {
	switch subject {
	case "REPORT.SINGLE", "REPORT.BATCH":
		return subject
	default:
		return "other"
	}
}

// ReportSourceLabel bounds the source label of the report metrics to a few categories, as the source of a report is
// free-form.
func ReportSourceLabel(source string) string {
	switch source {
	case "MaaAssistantArknights", constant.MeoAssistant:
		return "maa"
	case constant.FrontendV2, constant.FrontendV1, constant.FrontendV1Internal:
		return "frontend"
	default:
		return "other"
	}
}

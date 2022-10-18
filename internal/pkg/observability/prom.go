package observability

import (
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
	WorkerCalcDuration = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: prometheus.BuildFQName(ServiceName, "worker", "calc_duration_seconds"),
		Help: "Duration of last worker calculation in seconds",
	}, []string{"service", "server"})
)

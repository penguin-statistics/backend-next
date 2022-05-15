package observability

import "github.com/prometheus/client_golang/prometheus"

const (
	ServiceName = "penguinbackend"
)

var (
	ReportVerifyDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    prometheus.BuildFQName(ServiceName, "report", "verify_duration_seconds"),
		Help:    "Duration of report verification in seconds",
		Buckets: prometheus.ExponentialBuckets(0.01, 2, 10),
	}, []string{"verifier"})
	ReportConsumeDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    prometheus.BuildFQName(ServiceName, "report", "consume_duration_seconds"),
		Help:    "Duration of report consumption in seconds",
		Buckets: prometheus.ExponentialBuckets(0.01, 2, 10),
	}, []string{})
)

func Launch() {
	prometheus.MustRegister(ReportVerifyDuration)
	prometheus.MustRegister(ReportConsumeDuration)
}

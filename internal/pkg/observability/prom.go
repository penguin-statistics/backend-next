package observability

import (
	"strconv"
	"strings"
	"sync"

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
	// ReportReliability counts the stored reports by reliability (0 is accepted; see reportverifs.Verifier), source
	// and server. source_name is the source as the client sent it, bounded by reportSourceNames.
	ReportReliability = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName(ServiceName, "report", "reliability"),
		Help: "Reliability distribution of report consumption",
	}, []string{"reliability", "source_name", "server"})
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
		Help: "Acks, naks and terms of consumed report task messages that failed",
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
		Help: "Stored reports by source category (maa, frontend, other) and server",
	}, []string{"source", "server"})
	// ReportDeadLetteredTotal counts the report tasks moved to the dead-letter stream by reason: malformed (not a
	// report task), not_found (a stage of the task does not exist), max_deliveries (failed at its last delivery) and
	// over_delivered (delivered after it was moved already).
	ReportDeadLetteredTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName(ServiceName, "report", "dead_lettered_total"),
		Help: "Report tasks moved to the dead-letter stream by reason (malformed, not_found, max_deliveries, over_delivered)",
	}, []string{"reason"})
	// ReportAccounts counts how the report routes resolve the account of a request: existing (the PenguinID sent
	// names an account), created (none sent: a new account) or recreated (the PenguinID sent names no account, and a
	// new one replaces it), or error (creating it failed).
	ReportAccounts = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName(ServiceName, "report", "accounts_total"),
		Help: "Accounts of report requests by outcome (existing, created, recreated, error)",
	}, []string{"outcome"})
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
		Help: "Number of advanced query requests being handled, not counting any waiting for a slot of the concurrency limit",
	})
	AdvancedQueryDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    prometheus.BuildFQName(ServiceName, "advanced_query", "duration_seconds"),
		Help:    "Duration of advanced query requests in seconds",
		Buckets: prometheus.ExponentialBuckets(0.05, 2, 12),
	}, []string{"outcome"})
	AdvancedQueryRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName(ServiceName, "advanced_query", "requests_total"),
		Help: "Number of advanced query requests handled, by outcome (ok, client_error, error, rejected, timeout)",
	}, []string{"outcome"})
	AdvancedQueryQueriesPerRequest = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    prometheus.BuildFQName(ServiceName, "advanced_query", "queries_per_request"),
		Help:    "Number of queries in a valid advanced query request",
		Buckets: prometheus.LinearBuckets(1, 1, 5),
	})
	// HTTPResponseSize also counts responses: its _count series is the number of
	// responses by route, method and status class. The route is the last matched
	// route as logged in http.route: see middlewares.requestLogger.
	HTTPResponseSize = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    prometheus.BuildFQName(ServiceName, "http", "response_size_bytes"),
		Help:    "Size of HTTP response bodies in bytes by matched route, method and status class",
		Buckets: prometheus.ExponentialBuckets(1024, 8, 5),
	}, []string{"route", "method", "status_class"})
	// HTTPRequestDuration times every request from the request logger, unsampled (the traces are sampled), by the
	// same route and method as HTTPResponseSize and the exact status code (see HTTPStatusCode). The buckets run from
	// 1ms to 16s, past the 10s that a report publish waits for NATS at most.
	HTTPRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    prometheus.BuildFQName(ServiceName, "http", "request_duration_seconds"),
		Help:    "Duration of HTTP requests in seconds by matched route, method and status code",
		Buckets: prometheus.ExponentialBuckets(0.001, 2, 15),
	}, []string{"route", "method", "status_code"})
	// WorkerCalcBatchDuration and WorkerCalcBatches cover the batches of the calc worker. outcome is one of ok,
	// error, timeout and lock_not_acquired; a batch that did not acquire the lock has no duration.
	WorkerCalcBatchDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    prometheus.BuildFQName(ServiceName, "worker", "calc_batch_duration_seconds"),
		Help:    "Duration of calc worker batches in seconds",
		Buckets: prometheus.ExponentialBuckets(5, 2, 8),
	})
	WorkerCalcBatches = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName(ServiceName, "worker", "calc_batches_total"),
		Help: "Calc worker batches by outcome",
	}, []string{"outcome"})
	// WorkerCalcPhaseDuration covers the microtasks of a batch for a server: phase is one of dropMatrix,
	// patternMatrix and siteStats. The top bucket, 512s, sits close to the 10 minute default timeout of a
	// batch.
	WorkerCalcPhaseDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    prometheus.BuildFQName(ServiceName, "worker", "calc_phase_duration_seconds"),
		Help:    "Duration of calc worker phases in seconds",
		Buckets: prometheus.ExponentialBuckets(0.5, 2, 11),
	}, []string{"phase", "server"})
	// WorkerRefreshDuration and WorkerRefreshes cover the background refreshes of the global matrices that follow
	// the phases: refresh is one of globalDropMatrixRefresh and globalPatternMatrixRefresh, outcome one of ok, error
	// and panic. The top bucket, 1024s, is past the 10 minute default timeout of a refresh.
	WorkerRefreshDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    prometheus.BuildFQName(ServiceName, "worker", "refresh_duration_seconds"),
		Help:    "Duration of background global matrix refreshes in seconds",
		Buckets: prometheus.ExponentialBuckets(0.25, 2, 13),
	}, []string{"refresh", "server"})
	WorkerRefreshes = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName(ServiceName, "worker", "refreshes_total"),
		Help: "Background global matrix refreshes by outcome",
	}, []string{"refresh", "server", "outcome"})
	// GlobalMatrixRefreshPaths counts the calculations of the global matrices of a kind (drop), server and source
	// category with GlobalMatrixIncremental on or verify by path: incremental (a base extended by the days since and
	// the changed targets), rebuild (a base aggregated from every day) or full (every day, without a base: the triggers
	// of the day versions are missing or not firing always, or the versions cannot be read). reason is why it did not
	// take the path before: none for incremental; no_base, fingerprint (a day of the base was written, or the triggers
	// changed), day_back (the base is of a later day), max_age or changed_stages for rebuild; triggers_missing for full.
	GlobalMatrixRefreshPaths = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName(ServiceName, "worker", "global_matrix_refresh_path_total"),
		Help: "Calculations of the incremental global matrices by kind, server, source category, path and reason",
	}, []string{"kind", "server", "source_category", "path", "reason"})
	// GlobalMatrixChangedStages is the number of stages whose target time range changed since the base, of each
	// incremental calculation of a source category (also those that rebuild for it).
	GlobalMatrixChangedStages = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    prometheus.BuildFQName(ServiceName, "worker", "global_matrix_changed_stages"),
		Help:    "Stages whose target time range changed since the base, per incremental global matrix calculation",
		Buckets: []float64{0, 1, 2, 5, 10, 20, 50, 100, 200, 500},
	}, []string{"kind", "server"})
	// GlobalMatrixVerifyMismatches counts the verifications of the incremental global matrices (GlobalMatrixIncremental
	// verify, or GlobalMatrixVerifyEvery) that found other results than the reference calculation in the same
	// snapshot: the reference results are served, and the base is dropped. Must stay 0.
	GlobalMatrixVerifyMismatches = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName(ServiceName, "worker", "global_matrix_verify_mismatches_total"),
		Help: "Verifications of the incremental global matrices that differed from the reference calculation",
	}, []string{"kind", "server", "source_category"})
	// CacheSyncBumps counts the advances of the Redis generations by which the calc worker tells every process
	// that it refreshed the cached global results of a kind (globalDropMatrix, globalPatternMatrix, shimTrend or
	// siteStats; see cachesync), by outcome: ok or error (the other processes then miss that refresh, unless Redis
	// applied the advance and only its reply was lost).
	CacheSyncBumps = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName(ServiceName, "cache_sync", "bumps_total"),
		Help: "Advances of the cache sync generations after a calc worker refresh by kind and outcome",
	}, []string{"kind", "outcome"})
	// CacheSyncPolls counts the reads of the generations by outcome: ok or error.
	CacheSyncPolls = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName(ServiceName, "cache_sync", "polls_total"),
		Help: "Reads of the cache sync generations by outcome",
	}, []string{"outcome"})
	// CacheSyncRefreshDuration and CacheSyncRefreshes cover the refreshes a process runs after the generation of a
	// kind changed, of the results of a server it serves: outcome is one of ok, error, panic and cancelled (as the
	// process stops). The buckets are those of WorkerRefreshDuration.
	CacheSyncRefreshDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    prometheus.BuildFQName(ServiceName, "cache_sync", "refresh_duration_seconds"),
		Help:    "Duration of the refreshes following a calc worker refresh in seconds",
		Buckets: prometheus.ExponentialBuckets(0.25, 2, 13),
	}, []string{"kind", "server"})
	CacheSyncRefreshes = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName(ServiceName, "cache_sync", "refreshes_total"),
		Help: "Refreshes following a calc worker refresh by kind, server and outcome",
	}, []string{"kind", "server", "outcome"})
	// ColdstoreArchiveRuns covers the runs of the monthly archiving of the cold storage (#752): outcome is one of ok
	// (also when there was nothing to archive), error, verification_failed, insufficient_space and
	// lock_not_acquired. No partition is dropped by a run that did not end ok.
	ColdstoreArchiveRuns = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName(ServiceName, "coldstore", "archive_runs_total"),
		Help: "Runs of the monthly archiving of the cold storage by outcome",
	}, []string{"outcome"})
	ColdstoreArchivedMonths = promauto.NewCounter(prometheus.CounterOpts{
		Name: prometheus.BuildFQName(ServiceName, "coldstore", "archived_months_total"),
		Help: "Months archived to the cold storage whose partitions were dropped",
	})
	ColdstoreArchiveLastSuccess = promauto.NewGauge(prometheus.GaugeOpts{
		Name: prometheus.BuildFQName(ServiceName, "coldstore", "archive_last_success_timestamp_seconds"),
		Help: "Unix time of the end of the last run of the monthly archiving that ended ok",
	})
	// ColdstoreFreeBytes is the free space the monthly archiving found before its last month: dir is store (the cold
	// storage directory) or work (its DuckDB work directory).
	ColdstoreFreeBytes = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: prometheus.BuildFQName(ServiceName, "coldstore", "free_bytes"),
		Help: "Free bytes of the file systems of the cold storage directory and of its work directory",
	}, []string{"dir"})
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

// HTTPStatusClass returns the class of an HTTP status code ("2xx", "4xx", ...),
// or "other" for a code outside 100-599, to keep metric labels bounded.
func HTTPStatusClass(code int) string {
	switch code / 100 {
	case 1:
		return "1xx"
	case 2:
		return "2xx"
	case 3:
		return "3xx"
	case 4:
		return "4xx"
	case 5:
		return "5xx"
	default:
		return "other"
	}
}

// statusCodes holds the label of every status code from 100 to 599, so that labelling a response allocates nothing.
var statusCodes = func() (codes [600]string) {
	for code := 100; code < len(codes); code++ {
		codes[code] = strconv.Itoa(code)
	}
	return codes
}()

// HTTPStatusCode returns the status code as a label ("200", "304", ...), or "other" for a code outside 100-599, to
// keep metric labels bounded.
func HTTPStatusCode(code int) string {
	if code < 100 || code >= len(statusCodes) {
		return "other"
	}
	return statusCodes[code]
}

// ReportServerLabel bounds the server label of the report metrics to the servers of the game. A report task's server
// is validated (arkserver) as it is accepted, so other is not expected.
func ReportServerLabel(server string) string {
	switch server {
	case "CN", "US", "JP", "KR":
		return server
	default:
		return "other"
	}
}

// maxReportSourceNames bounds the distinct values of the source_name label of ReportReliability. The source of a
// report is free-form (up to 128 printable characters), so without a bound any client could create a series per
// report, which every process would keep in memory for good. A handful of sources send nearly all reports.
const maxReportSourceNames = 64

var reportSourceNames = boundedLabel{max: maxReportSourceNames}

// ReportSourceNameLabel returns source as the source_name label of ReportReliability: itself while it is one of the
// first maxReportSourceNames sources this process has seen, and other after that.
func ReportSourceNameLabel(source string) string {
	return reportSourceNames.label(source)
}

// boundedLabel keeps the first max values of a label, and maps the others to "other".
type boundedLabel struct {
	max    int
	mu     sync.RWMutex
	values map[string]struct{}
}

func (b *boundedLabel) label(v string) string {
	b.mu.RLock()
	_, ok := b.values[v]
	full := len(b.values) >= b.max
	b.mu.RUnlock()
	if ok {
		return v
	}
	if full {
		return "other"
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.values[v]; ok {
		return v
	}
	if len(b.values) >= b.max {
		return "other"
	}
	if b.values == nil {
		b.values = make(map[string]struct{}, b.max)
	}
	// a copy: v may be backed by a buffer that is reused, e.g. a decoded message
	v = strings.Clone(v)
	b.values[v] = struct{}{}
	return v
}

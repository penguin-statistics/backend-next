package appconfig

import (
	"time"

	"exusiai.dev/backend-next/internal/app/appcontext"
)

type ConfigSpec struct {
	// ServiceAddress is the listen address would listen on for serving normal service requests.
	ServiceAddress string `required:"true" split_words:"true" default:"localhost:9010"`

	// DevOpsAddress is the listen address would listen on for serving devops requests.
	// Leaving this empty will disable devops server.
	// This address is only intended to be used in intra-cluster devops requests, and is not intended to be exposed to the public.
	DevOpsAddress string `split_words:"true"`

	// LogJsonStdout is whether to log JSON logs (instead of pretty-print logs) to stdout for the ease of log collection.
	LogJsonStdout bool `split_words:"true" default:"false"`

	// TrustedProxies is a list of trusted proxies that are trusted to report a real IP via the X-Forwarded-For header.
	TrustedProxies []string `required:"true" split_words:"true" default:"::1,127.0.0.1,10.0.0.0/8"`

	// CORSCredentialedOrigins lists the browser origins that may send credentialed (cookie-carrying) cross-origin
	// requests and read their responses: they get their Origin echoed back with Access-Control-Allow-Credentials.
	// Every other origin gets Access-Control-Allow-Origin: * without credentials. An entry is an exact origin
	// (scheme://host[:port]) or scheme://*.domain for exactly one subdomain label. See middlewares.CORS.
	//
	// The defaults are the first-party clients that call the API cross-origin with withCredentials: penguin-stats.io
	// and .cn, the iOS/macOS Capacitor app, and the frontend-v2 deployments on Cloudflare Pages (production and
	// previews) and frontend-legacy.exusiai.dev. Do not add wildcards over hosts that serve anything but the frontend
	// (e.g. *.exusiai.dev, which includes an HTTP echo service); wildcards over public suffixes (*.pages.dev) are
	// rejected. https://*.frontend-v2-8db.pages.dev covers every preview the Pages project ever built, which never
	// expire, including branch aliases anyone with push access can create; Pages does not build pull requests from
	// forks there (2026-10: same-repo PRs get a Cloudflare Pages check run, fork PRs #588 and #592 none). Drop the
	// Pages entries once frontend-v2 stops sending withCredentials.
	//
	// Setting PENGUIN_V3_CORS_CREDENTIALED_ORIGINS replaces the whole list; keep capacitor://localhost in it, or the
	// iOS/macOS app fails on every request.
	CORSCredentialedOrigins []string `split_words:"true" default:"https://penguin-stats.io,https://penguin-stats.cn,capacitor://localhost,https://frontend-v2-8db.pages.dev,https://*.frontend-v2-8db.pages.dev,https://frontend-legacy.exusiai.dev"`

	// DevMode to indicate development mode. When true, the program would spin up utilities for debugging and
	// provide a more contextual message when encountered a panic. See internal/server/httpserver/http.go for the
	// actual implementation details.
	DevMode bool `split_words:"true"`

	// TracingEnabled to indicate whether to enable OpenTelemetry tracing.
	TracingEnabled bool `split_words:"true"`

	// TracingExporters to indicate which exporters to use for tracing.
	// Valid values are: jaeger, otlp, stdout (for debug).
	TracingExporters []string `split_words:"true" default:"jaeger"`

	// TracingSampleRate to indicate the sampling rate for tracing.
	// Valid values are: 0.0 (disabled), 1.0 (all traces), or a value between 0.0 and 1.0 (sampling rate).
	TracingSampleRate float64 `split_words:"true" default:"1.0"`

	// infrastructure components connection instructions

	// PostgresDSN is the data source name for the PostgreSQL database. See
	// https://bun.uptrace.dev/postgres/#pgdriver for more details on how to construct a PostgreSQL DSN.
	PostgresDSN string `required:"true" split_words:"true"`

	PostgresMaxOpenConns    int           `split_words:"true" default:"10"`
	PostgresMaxIdleConns    int           `split_words:"true" default:"2"`
	PostgresConnMaxLifeTime time.Duration `split_words:"true" default:"5m"`
	PostgresConnMaxIdleTime time.Duration `split_words:"true" default:"5m"`

	BunDebugVerbose bool `split_words:"true"`

	// NatsURL is the URL of the NATS server. See https://pkg.go.dev/github.com/nats-io/nats.go#Connect
	// for more information on how to construct a NATS URL.
	NatsURL string `required:"true" split_words:"true" default:"nats://127.0.0.1:4222"`

	// RedisURL is the URL of the Redis server, and by default uses redis db 1, to avoid potential collision
	// with the previous running backend instance. See https://pkg.go.dev/github.com/redis/go-redis/v9#ParseURL
	// for more information on how to construct a Redis URL.
	RedisURL string `required:"true" split_words:"true" default:"redis://127.0.0.1:6379/1"`

	// SentryDSN is the DSN of the Sentry server. See https://pkg.go.dev/github.com/getsentry/sentry-go#ClientOptions
	SentryDSN string `split_words:"true"`

	// DatadogProfilerEnabled to indicate whether to enable Datadog profiler.
	DatadogProfilerEnabled bool `split_words:"true" default:"false"`

	// DatadogProfilerAgentAddress is the address of the Datadog profiler agent.
	DatadogProfilerAgentAddress string `split_words:"true" default:"localhost:8126"`

	// UpyunOperatorName is the operator name of the Upyun account.
	UpyunOperatorName string `split_words:"true"`

	// UpyunOperatorPassword is the operator password of the Upyun account.
	UpyunOperatorPassword string `split_words:"true"`

	// UpyunUserContentBucket is the bucket name of user content.
	UpyunUserContentBucket string `split_words:"true"`

	// UpyunUserContentDomain is the domain name of user content.
	UpyunUserContentDomain string `split_words:"true"`

	// UpyunNotifyURLPrefix is the notify URL
	UpyunNotifyURLPrefix string `split_words:"true"`

	// UpyunUserContentSignatureSecret is the signature secret of the Upyun account.
	UpyunUserContentSignatureSecret string `split_words:"true"`

	// RecognitionEncryptionPrivateKey is the private key used to decrypt the recognition data.
	// Normal contributors should not need to change this: when left empty, recognition report is simply disabled.
	RecognitionEncryptionPrivateKey []byte `split_words:"true"`

	// RecognitionEncryptionIV is a pre-defined IV used to encrypt the recognition data.
	// Normal contributors should not need to change this: when left empty, recognition report is simply disabled.
	RecognitionEncryptionIV []int `split_words:"true"`

	// HTTPServerShutdownTimeout is the timeout for the HTTP server to shut down gracefully.
	HTTPServerShutdownTimeout time.Duration `required:"true" split_words:"true" default:"60s"`

	// WorkerInterval describes the interval in-between different batches
	WorkerInterval time.Duration `required:"true" split_words:"true" default:"10m"`

	// WorkerSeparation describes the separation time in-between different microtasks
	WorkerSeparation time.Duration `required:"true" split_words:"true" default:"5s"`

	// WorkerTimeout describes the timeout for a single batch to run
	WorkerTimeout time.Duration `required:"true" split_words:"true" default:"10m"`

	// WorkerHeartbeatURL is the map of URLs to ping to check if the worker is alive.
	// The key is the name of the worker, and the value is the URL.
	// Possible keys are: "main", "trend"
	WorkerHeartbeatURL WorkerHeartbeatURLMap `split_words:"true"`

	// WorkerEnabled is a flag to indicate whether to enable the worker.
	WorkerEnabled bool `split_words:"true"`

	// AdminKey is the key used to authenticate the admin API.
	AdminKey string `split_words:"true"`

	// MatrixWorkerSourceCategories is a list of categories that the matrix worker will run for.
	// Available categories are: all, automated, manual.
	MatrixWorkerSourceCategories []string `required:"true" split_words:"true" default:"all"`

	// For PatternMatrix query api, if showAllPatterns is false, then only show the top 50 patterns for all stages
	// We don't want to show all patterns because it will be too many. So we set a limit here (default 19)
	PatternMatrixLimit int `split_words:"true" default:"19"`

	DropReportArchiveEnabled   bool `split_words:"true" default:"false"`
	DropReportArchiveBatchSize int  `split_words:"true" default:"1000"`

	DropReportArchiveS3Bucket string `required:"true" split_words:"true"`
	DropReportArchiveS3Region string `required:"true" split_words:"true"`
	AWSAccessKey              string `required:"true" split_words:"true"`
	AWSSecretKey              string `required:"true" split_words:"true"`

	NoArchiveDays int `split_words:"true" default:"60"`

	DeleteDropReportAfterArchive bool `split_words:"true" default:"false"`

	// SEOPagesEnabled serves the stats pages of the frontend (/{zh,en,ja,ko}/result/...) from here: the frontend's
	// index.html with the page's title, description, canonical and hreflang links, structured data and a plain HTML
	// table of its data, for search engines and link previews. The ingress has to route those paths here. When
	// false, requests to those paths are redirected back to the frontend (the same path with the language in upper
	// case, which the ingress does not route here).
	SEOPagesEnabled bool `split_words:"true" default:"false"`

	// SEOShellURL is the URL of the frontend's index.html, which the stats pages are made of.
	SEOShellURL string `split_words:"true" default:"http://frontend-rollout.default.svc.cluster.local/"`

	// SEOShellTTL is how often the index.html is fetched again (revalidated with ETag/Last-Modified). Keep it well
	// below the time the frontend keeps its old pods after a release (30s): a copy from before a release refers to
	// scripts the release removed. The last copy fetched is served while a fetch fails. Not positive: 15s.
	SEOShellTTL time.Duration `split_words:"true" default:"15s"`

	// SEOShellFetchTimeout is the timeout of a single fetch of the index.html. Not positive: 3s.
	SEOShellFetchTimeout time.Duration `split_words:"true" default:"3s"`

	// SEOSiteURL is the origin of the canonical URLs of the stats pages, whichever host the request was sent to.
	SEOSiteURL string `split_words:"true" default:"https://penguin-stats.io"`

	// SEOPageMaxAge is the max-age of the stats pages. Pages without data (404s, a matrix not calculated yet) are
	// cached for a minute at most.
	SEOPageMaxAge time.Duration `split_words:"true" default:"5m"`

	// MCPEnabled serves a read-only MCP server of the drop data at /mcp (Streamable HTTP, stateless, JSON responses,
	// no authentication), made of the data of the stats pages, which it loads also while the pages are off. The
	// ingress has to route /mcp here. When false, /mcp is not routed (404).
	MCPEnabled bool `split_words:"true" default:"false"`

	// MCPRateLimitMax is how many requests to /mcp a client (an IP, an IPv6 /64) may send per MCPRateLimitWindow to
	// each replica; past it, it is answered 429 with a Retry-After. Not positive: 120.
	MCPRateLimitMax int `split_words:"true" default:"120"`

	// MCPRateLimitWindow is the sliding window of MCPRateLimitMax. Not positive: 1m.
	MCPRateLimitWindow time.Duration `split_words:"true" default:"1m"`
}

type Config struct {
	// ConfigSpec is the configuration specification injected to the config.
	ConfigSpec

	// AppContext is the application context
	AppContext appcontext.Ctx
}

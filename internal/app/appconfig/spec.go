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
	// with the previous running backend instance. See https://pkg.go.dev/github.com/go-redis/redis/v8#ParseURL
	// for more information on how to construct a Redis URL.
	RedisURL string `required:"true" split_words:"true" default:"redis://127.0.0.1:6379/1"`

	// SentryDSN is the DSN of the Sentry server. See https://pkg.go.dev/github.com/getsentry/sentry-go#ClientOptions
	SentryDSN string `split_words:"true"`

	// LiveHouseEnabled to indicate whether to enable LiveHouse reporting.
	LiveHouseEnabled bool `split_words:"true" default:"false"`

	// LiveHouseGRPCAddress is the address of the LiveHouse gRPC server.
	LiveHouseGRPCAddress string `split_words:"true" default:"localhost:9015"`

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

	// WorkerTrendInterval describes the interval in-between different batches
	WorkerTrendInterval time.Duration `required:"true" split_words:"true" default:"6h"`

	// WorkerSeparation describes the separation time in-between different microtasks
	WorkerSeparation time.Duration `required:"true" split_words:"true" default:"3s"`

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
}

type Config struct {
	// ConfigSpec is the configuration specification injected to the config.
	ConfigSpec

	// AppContext is the application context
	AppContext appcontext.Ctx
}

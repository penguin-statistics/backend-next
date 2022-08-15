package config

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	_ "github.com/joho/godotenv/autoload"
	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	// Address is the listen address would listen on.
	Address string

	TrustedProxies []string `required:"true" split_words:"true" default:"::1,127.0.0.1,10.0.0.0/8"`

	// DevMode to indicate development mode. When true, the program would spin up utilities for debugging and
	// provide a more contextual message when encountered a panic. See internal/server/httpserver/http.go for the
	// actual implementation details.
	DevMode bool `split_words:"true"`

	// TracingEnabled to indicate whether to enable OpenTelemetry tracing.
	TracingEnabled bool `split_words:"true"`

	// infrastructure components connection instructions

	// PostgresDSN is the data source name for the PostgreSQL database. See
	// https://bun.uptrace.dev/postgres/#pgdriver for more details on how to construct a PostgreSQL DSN.
	PostgresDSN string `required:"true" split_words:"true"`

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

type WorkerHeartbeatURLMap map[string]string

func (m *WorkerHeartbeatURLMap) Decode(value string) error {
	*m = WorkerHeartbeatURLMap{}
	for _, pair := range strings.Split(value, ",") {
		kv := strings.Split(pair, ":")
		if len(kv) != 2 {
			return fmt.Errorf("invalid heartbeat URL map: expect a `:` separated key pair for each element, but got: %s", value)
		}
		val, err := base64.StdEncoding.DecodeString(strings.TrimSpace(kv[1]))
		if err != nil {
			return fmt.Errorf("invalid value in worker heartbeat URL map: base64 decoding failed: %s (%w)", val, err)
		}
		(*m)[kv[0]] = string(val)
	}
	return nil
}

func Parse() (*Config, error) {
	var config Config
	err := envconfig.Process("penguin_v3", &config)
	if err != nil {
		_ = envconfig.Usage("penguin_v3", &config)
		return nil, fmt.Errorf("failed to parse configuration: %w. More info on how to configure this backend is located at https://pkg.go.dev/github.com/penguin-statistics/backend-next/internal/config#Config", err)
	}

	return &config, nil
}

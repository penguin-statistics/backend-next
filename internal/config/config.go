package config

import (
	"fmt"
	"time"

	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	// Address is the listen address would listen on.
	Address string

	// DevMode to indicate development mode. When true, the program would spin up utilities for debugging and
	// provide a more contextual message when encountered a panic. See internal/server/http/http.go for the
	// actual implementation details.
	DevMode bool `split_words:"true"`

	// EnableTracing to indicate whether to enable OpenTelemetry tracing.
	EnableTracing bool `split_words:"true"`

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

	// RecognitionEncryptionPrivateKey is the private key used to decrypt the recognition data.
	// Normal contributors should not need to change this: when left empty, recognition report is simply disabled.
	RecognitionEncryptionPrivateKey []byte `split_words:"true"`

	// RecognitionEncryptionIV is a pre-defined IV used to encrypt the recognition data.
	// Normal contributors should not need to change this: when left empty, recognition report is simply disabled.
	RecognitionEncryptionIV []int `split_words:"true"`

	// HttpServerShutdownTimeout is the timeout for the HTTP server to shutdown gracefully.
	HttpServerShutdownTimeout time.Duration `required:"true" split_words:"true" default:"60s"`

	// GeoIPDBPath is the path to the GeoIP2 database.
	GeoIPDBPath string `required:"true" split_words:"true" default:"vendors/maxmind/assets/geolite2/GeoLite2-Country.mmdb"`
}

func Parse() (*Config, error) {
	var config Config
	err := envconfig.Process("penguin_v3", &config)
	if err != nil {
		err = fmt.Errorf("failed to parse configuration: %w. More info on how to configure this backend is located at https://pkg.go.dev/github.com/penguin-statistics/backend-next/internal/config#Config", err)
		return nil, err
	}

	return &config, nil
}

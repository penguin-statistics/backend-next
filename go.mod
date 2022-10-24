module exusiai.dev/backend-next

go 1.19

require (
	exusiai.dev/gommon v0.0.4
	github.com/ahmetb/go-linq/v3 v3.2.0
	github.com/ansrivas/fiberprometheus/v2 v2.4.1
	github.com/antonmedv/expr v1.9.0
	github.com/avast/retry-go/v3 v3.1.1
	github.com/davecgh/go-spew v1.1.1
	github.com/dchest/uniuri v1.2.0
	github.com/getsentry/sentry-go v0.14.0
	github.com/go-playground/locales v0.14.0
	github.com/go-playground/universal-translator v0.18.0
	github.com/go-playground/validator/v10 v10.11.1
	github.com/go-redis/redis/v8 v8.11.5
	github.com/go-redsync/redsync/v4 v4.6.0
	github.com/goccy/go-json v0.9.11
	github.com/gofiber/contrib/fibersentry v0.0.0-20221024221520-cac581acacd3
	github.com/gofiber/contrib/otelfiber v0.0.0-20221020220652-4b53fd7edccb
	github.com/gofiber/fiber/v2 v2.39.0
	github.com/gofiber/helmet/v2 v2.2.18
	github.com/gofiber/swagger v0.1.7
	github.com/jinzhu/copier v0.3.5
	github.com/joho/godotenv v1.4.0
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/nats-io/nats.go v1.18.0
	github.com/oschwald/geoip2-golang v1.8.0
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.13.0
	github.com/rs/xid v1.4.0
	github.com/rs/zerolog v1.28.0
	github.com/samber/lo v1.33.0
	github.com/stretchr/testify v1.8.1
	github.com/swaggo/swag v1.8.7
	github.com/tidwall/gjson v1.14.3
	github.com/uptrace/bun v1.1.8
	github.com/uptrace/bun/dialect/pgdialect v1.1.8
	github.com/uptrace/bun/driver/pgdriver v1.1.8
	github.com/uptrace/bun/extra/bundebug v1.1.8
	github.com/uptrace/bun/extra/bunotel v1.1.8
	github.com/vmihailenco/msgpack/v5 v5.3.5
	github.com/zeebo/xxh3 v1.0.2
	go.opentelemetry.io/otel v1.11.1
	go.opentelemetry.io/otel/exporters/jaeger v1.11.1
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.11.1
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.11.1
	go.opentelemetry.io/otel/exporters/stdout/stdouttrace v1.11.1
	go.opentelemetry.io/otel/sdk v1.11.1
	go.uber.org/fx v1.18.1
	golang.org/x/exp v0.0.0-20220823124025-807a23277127
	golang.org/x/mod v0.6.0-dev.0.20220419223038-86c51ed26bb4
	golang.org/x/text v0.4.0
	google.golang.org/grpc v1.50.1
	google.golang.org/protobuf v1.28.1
	gopkg.in/DataDog/dd-trace-go.v1 v1.43.0
	gopkg.in/guregu/null.v3 v3.5.0
	gopkg.in/natefinch/lumberjack.v2 v2.0.0
)

require (
	github.com/DataDog/datadog-go/v5 v5.0.2 // indirect
	github.com/DataDog/gostackparse v0.5.0 // indirect
	github.com/Microsoft/go-winio v0.5.1 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cenkalti/backoff/v4 v4.1.3 // indirect
	github.com/gofiber/adaptor/v2 v2.1.25 // indirect
	github.com/golang/protobuf v1.5.2 // indirect
	github.com/google/pprof v0.0.0-20210423192551-a2663126120b // indirect
	github.com/google/uuid v1.3.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.7.0 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/mattn/go-runewidth v0.0.14 // indirect
	github.com/matttproud/golang_protobuf_extensions v1.0.1 // indirect
	github.com/prometheus/client_model v0.2.0 // indirect
	github.com/prometheus/common v0.37.0 // indirect
	github.com/prometheus/procfs v0.8.0 // indirect
	github.com/rivo/uniseg v0.2.0 // indirect
	github.com/swaggo/files v0.0.0-20220728132757-551d4a08d97a // indirect
	go.opentelemetry.io/contrib v1.11.1 // indirect
	go.opentelemetry.io/otel/exporters/otlp/internal/retry v1.11.1 // indirect
	go.opentelemetry.io/proto/otlp v0.19.0 // indirect
	google.golang.org/genproto v0.0.0-20211118181313-81c1377c94b1 // indirect
)

require (
	github.com/KyleBanks/depth v1.2.1 // indirect
	github.com/andybalholm/brotli v1.0.4 // indirect
	github.com/cespare/xxhash/v2 v2.1.2 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/fatih/color v1.13.0 // indirect
	github.com/go-logr/logr v1.2.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-openapi/jsonpointer v0.19.5 // indirect
	github.com/go-openapi/jsonreference v0.20.0 // indirect
	github.com/go-openapi/spec v0.20.7 // indirect
	github.com/go-openapi/swag v0.22.3 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/klauspost/compress v1.15.11 // indirect
	github.com/klauspost/cpuid/v2 v2.0.11 // indirect
	github.com/leodido/go-urn v1.2.1 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.16 // indirect
	github.com/nats-io/nats-server/v2 v2.7.3 // indirect
	github.com/nats-io/nkeys v0.3.0 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	github.com/oschwald/maxminddb-golang v1.10.0 // indirect
	github.com/patrickmn/go-cache v2.1.0+incompatible
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.0 // indirect
	github.com/tmthrgd/go-hex v0.0.0-20190904060850-447a3041c3bc // indirect
	github.com/uptrace/opentelemetry-go-extra/otelsql v0.1.15 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/valyala/fasthttp v1.40.0
	github.com/valyala/tcplisten v1.0.0 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	go.opentelemetry.io/otel/metric v0.31.0 // indirect
	go.opentelemetry.io/otel/trace v1.11.1
	go.uber.org/atomic v1.7.0 // indirect
	go.uber.org/dig v1.15.0 // indirect
	go.uber.org/multierr v1.6.0 // indirect
	go.uber.org/zap v1.16.0 // indirect
	golang.org/x/crypto v0.0.0-20220926161630-eccd6366d1be // indirect
	golang.org/x/net v0.0.0-20221002022538-bcab6841153b // indirect
	golang.org/x/sys v0.0.0-20220928140112-f11e5e49a4ec // indirect
	golang.org/x/tools v0.1.12 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	honnef.co/go/tools v0.1.3 // indirect
	mellium.im/sasl v0.3.0 // indirect
)

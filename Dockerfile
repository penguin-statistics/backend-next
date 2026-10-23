FROM golang:1.27.3-bookworm AS base
WORKDIR /app

# builder
FROM base AS builder
ENV GOOS=linux
ENV GOARCH=amd64

# build-args
ARG VERSION

# modules: utilize build cache
COPY go.mod ./
COPY go.sum ./

# RUN go env -w GO111MODULE=on && go env -w GOPROXY=https://goproxy.cn,direct
RUN go mod download
COPY . .

# the full SHA of the commit built (the image is built from a Git context without .git): spans and profiles carry it
# for Datadog's source code integration. Declared here, as a change of it invalidates the cache of the steps after it.
ARG GIT_COMMIT_SHA

# inject versioning information & build the binary
# appsec: datadog ASM; duckdb: links the prebuilt DuckDB static library (glibc), so cgo is required
RUN export BUILD_TIME=$(date -u +"%Y-%m-%dT%H:%M:%SZ"); export CGO_ENABLED=1; go build -v -tags appsec -o backend -ldflags "-X exusiai.dev/backend-next/internal/pkg/bininfo.Version=$VERSION -X exusiai.dev/backend-next/internal/pkg/bininfo.BuildTime=$BUILD_TIME -X exusiai.dev/backend-next/internal/pkg/bininfo.GitCommit=$GIT_COMMIT_SHA" .

# duckdb extensions: the app never installs extensions at runtime (see internal/pkg/duckdbx), so the ones it loads
# are preinstalled here. The version must be the DuckDB release of github.com/duckdb/duckdb-go/v2 in go.mod
# (v2.20000.0 -> v2.0.0); the extension files are signed, and DuckDB checks the signature on LOAD.
FROM base AS duckdb-extensions
ARG DUCKDB_VERSION=v2.0.0
RUN dir=/opt/duckdb/extensions/$DUCKDB_VERSION/linux_amd64 && mkdir -p $dir && \
    curl -fsSL https://extensions.duckdb.org/$DUCKDB_VERSION/linux_amd64/postgres_scanner.duckdb_extension.gz \
    | gunzip > $dir/postgres_scanner.duckdb_extension

# check that the preinstalled extensions load into the DuckDB the binary links
FROM builder AS duckdb-check
COPY --from=duckdb-extensions /opt/duckdb/extensions /opt/duckdb/extensions
RUN CGO_ENABLED=1 PENGUIN_TEST_DUCKDB_EXTENSION_DIR=/opt/duckdb/extensions \
    go test -count=1 -run 'TestOpenAppliesConfig|TestLoadPostgres' ./internal/pkg/duckdbx && touch /duckdb-checked

# runner
FROM debian:bookworm-slim AS runner
RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates libstdc++6 tini tzdata && \
    rm -rf /var/lib/apt/lists/*
# Tini is now available at /usr/bin/tini
WORKDIR /app

COPY --from=duckdb-check /duckdb-checked /opt/duckdb/checked
COPY --from=duckdb-extensions /opt/duckdb/extensions /opt/duckdb/extensions
COPY --from=builder /app/backend /app/backend
EXPOSE 8080

ENTRYPOINT ["/usr/bin/tini", "--"]
CMD [ "/app/backend", "start" ]

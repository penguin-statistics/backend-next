FROM golang:1.17.6-alpine AS base
WORKDIR /app

# builder
FROM base AS builder
ENV GOOS linux
ENV GOARCH amd64

# modules: utilize build cache
COPY go.mod ./
COPY go.sum ./

# RUN go env -w GO111MODULE=on && go env -w GOPROXY=https://goproxy.cn,direct
RUN go mod download
COPY . .

# Inject versioning information & build the binary
RUN GIT_COMMIT=$(git rev-parse HEAD) \
    GIT_TAG=$(git describe --tags) \
    GIT_BRANCH=$(git rev-parse --abbrev-ref HEAD) \
    BUILD_TIME=$(date -u +"%Y-%m-%dT%H:%M:%SZ") \
    go build -o backend -ldflags "-X github.com/penguin-statistics/backend-next/internal/pkg/bininfo.GitCommit=${GIT_COMMIT} -X github.com/penguin-statistics/backend-next/internal/pkg/bininfo.GitTag=${GIT_TAG} -X github.com/penguin-statistics/backend-next/internal/pkg/bininfo.GitBranch=${GIT_BRANCH} -X github.com/penguin-statistics/backend-next/internal/pkg/bininfo.BuildTime=${BUILD_TIME}" .

# runner
FROM base AS runner
RUN apk add --no-cache libc6-compat

ENV TINI_VERSION v0.19.0
ADD https://github.com/krallin/tini/releases/download/${TINI_VERSION}/tini /tini
RUN chmod +x /tini

COPY --from=builder /app/backend /app/backend
EXPOSE 8080

ENTRYPOINT ["/tini", "--"]
CMD [ "/app/backend" ]

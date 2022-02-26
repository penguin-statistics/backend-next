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

RUN apk update && apk add bash git openssh

# Inject versioning information & build the binary
RUN GIT_COMMIT=$(git rev-parse HEAD) \
    GIT_TAG=$(git describe --tags) \
    GIT_BRANCH=$(git rev-parse --abbrev-ref HEAD) \
    BUILD_TIME=$(date -u +"%Y-%m-%dT%H:%M:%SZ") \
    go build -o backend -ldflags "-X github.com/penguin-statistics/backend-next/internal/pkg/bininfo.GitCommit=$(echo -n $GIT_COMMIT) -X github.com/penguin-statistics/backend-next/internal/pkg/bininfo.GitTag=$(echo -n $GIT_TAG) -X github.com/penguin-statistics/backend-next/internal/pkg/bininfo.GitBranch=$(echo -n $GIT_BRANCH) -X github.com/penguin-statistics/backend-next/internal/pkg/bininfo.BuildTime=$(echo -n $BUILD_TIME)" .

# runner
FROM base AS runner
RUN apk add --no-cache libc6-compat

RUN apk add --no-cache tini
# Tini is now available at /sbin/tini

COPY --from=builder /app/backend /app/backend
EXPOSE 8080

ENTRYPOINT ["/sbin/tini", "--"]
CMD [ "/app/backend" ]

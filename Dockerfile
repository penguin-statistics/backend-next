FROM golang:1.17.6-alpine AS base
WORKDIR /app

# builder
FROM base AS builder
ENV GOOS linux
ENV GOARCH amd64

# build-args
ARG GIT_COMMIT
ARG GIT_TAG
ARG GIT_BRANCH

# modules: utilize build cache
COPY go.mod ./
COPY go.sum ./

# RUN go env -w GO111MODULE=on && go env -w GOPROXY=https://goproxy.cn,direct
RUN go mod download
COPY . .

RUN apk update && apk add bash git openssh

# Inject versioning information & build the binary
RUN BUILD_TIME=$(date -u +"%Y-%m-%dT%H:%M:%SZ") go build -o backend -ldflags "-X github.com/penguin-statistics/backend-next/internal/pkg/bininfo.GitCommit=${GIT_COMMIT} -X github.com/penguin-statistics/backend-next/internal/pkg/bininfo.GitTag=${GIT_TAG} -X github.com/penguin-statistics/backend-next/internal/pkg/bininfo.GitBranch=${GIT_BRANCH} -X github.com/penguin-statistics/backend-next/internal/pkg/bininfo.BuildTime=$(echo -n $BUILD_TIME)" .

# tf is going on
RUN echo "GitCommit: $GIT_COMMIT; GitTag: $GIT_TAG; GitBranch: $GIT_BRANCH; BuildTime: $BUILD_TIME"

# runner
FROM base AS runner
RUN apk add --no-cache libc6-compat

RUN apk add --no-cache tini
# Tini is now available at /sbin/tini

COPY --from=builder /app/backend /app/backend
EXPOSE 8080

ENTRYPOINT ["/sbin/tini", "--"]
CMD [ "/app/backend" ]

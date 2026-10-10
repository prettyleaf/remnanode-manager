# syntax=docker/dockerfile:1

# Frontend and Go stages run on the build host and cross-compile, so
# multi-arch images build without QEMU emulation.
FROM --platform=$BUILDPLATFORM node:24-alpine AS web
WORKDIR /src/web
RUN npm install -g pnpm@11.21.0
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS
ARG TARGETARCH
# Build metadata shown in the web UI (Build info), passed by CI.
ARG VERSION=dev
ARG COMMIT=""
ARG BRANCH=""
ARG BUILD_TIME=""
ARG REPOSITORY=prettyleaf/remnanode-manager
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY --from=web /src/internal/webui/dist ./internal/webui/dist
RUN BI=github.com/prettyleaf/remnanode-manager/internal/buildinfo && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -buildvcs=false \
    -ldflags "-s -w -X $BI.Version=${VERSION} -X $BI.Commit=${COMMIT} -X $BI.Branch=${BRANCH} -X $BI.BuildTime=${BUILD_TIME} -X $BI.Repository=${REPOSITORY}" \
    -o /out/remnanode-manager ./cmd/remnanode-manager

FROM alpine:3.24
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/remnanode-manager /usr/local/bin/remnanode-manager

ENV APP_HOST=0.0.0.0 \
    APP_PORT=3100 \
    DATA_DIR=/app/data
WORKDIR /app
VOLUME ["/app/data"]
EXPOSE 3100

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["remnanode-manager", "healthcheck"]

ENTRYPOINT ["remnanode-manager"]

# syntax=docker/dockerfile:1
# UI build stage: compile React display shell (web/ui -> /src/web/static/ui)
FROM --platform=$BUILDPLATFORM mirror.gcr.io/library/node:22-alpine AS ui-builder

WORKDIR /src/web/ui
COPY --link web/ui/package.json web/ui/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm \
    npm ci --no-audit --no-fund

COPY --link web/ui/ ./
RUN npm run build

# Build stage: compile static Go binary using official Go 1.27 toolchain via mirror.gcr.io to prevent unauthenticated Docker Hub rate limits
FROM --platform=$BUILDPLATFORM mirror.gcr.io/library/golang:1.27.3-alpine AS builder

WORKDIR /src

# Download dependencies using bind-mounted manifests and BuildKit cache mount
RUN --mount=type=bind,source=go.mod,target=go.mod \
    --mount=type=bind,source=go.sum,target=go.sum \
    --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Copy Go source tree and compile minimal static Linux executable
ARG TARGETOS TARGETARCH
COPY --link go.mod go.sum ./
COPY --link cmd/ ./cmd/
COPY --link internal/ ./internal/
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -cover -covermode=atomic -trimpath -ldflags="-s -w -extldflags '-static'" -o /app/server ./cmd/server

# Runtime stage: minimal hardened Alpine 3.24 environment via mirror.gcr.io
FROM mirror.gcr.io/library/alpine:3.24

# Install runtime certificates and timezone database
RUN --mount=type=cache,target=/etc/apk/cache,sharing=locked \
    apk add ca-certificates tzdata \
    && addgroup -g 10001 -S appgroup \
    && adduser -u 10001 -S appuser -G appgroup \
    && mkdir -p /config /data /coverage /app/web /app/widgets \
    && chown -R 10001:10001 /config /data /coverage /app/web /app/widgets

WORKDIR /app

# Copy compiled executable, web templates/static assets, and built React UI bundle (widgets/ are volume-mounted at runtime)
COPY --from=builder --link --chown=10001:10001 /app/server /app/server
COPY --link --chown=10001:10001 web/templates/ /app/web/templates/
COPY --link --chown=10001:10001 web/static/ /app/web/static/
COPY --from=ui-builder --link --chown=10001:10001 /src/web/static/ui/ /app/web/static/ui/

# Switch to unprivileged non-root user
USER 10001:10001

EXPOSE 8080

# Native Busybox wget probe to avoid IPv6 musl localhost delays and extra dependencies
HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --start-interval=2s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://127.0.0.1:8080/healthz || exit 1

# PID 1 execution ensures instant SIGTERM handling and graceful shutdown
ENTRYPOINT ["/app/server"]

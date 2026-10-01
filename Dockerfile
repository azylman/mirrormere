# syntax=docker/dockerfile:1
# Build stage: compile static Go binary using official Go 1.24 toolchain
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS builder

WORKDIR /src

# Download dependencies using bind-mounted manifests and BuildKit cache mount
RUN --mount=type=bind,source=go.mod,target=go.mod \
    --mount=type=bind,source=go.sum,target=go.sum \
    --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Copy source tree and compile minimal static Linux executable
ARG TARGETOS TARGETARCH
COPY --link . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -extldflags '-static'" -o /app/server ./cmd/server

# Runtime stage: minimal hardened Alpine 3.21 environment
FROM alpine:3.21

# Install runtime certificates and timezone database
RUN --mount=type=cache,target=/etc/apk/cache,sharing=locked \
    apk add ca-certificates tzdata \
    && addgroup -g 10001 -S appgroup \
    && adduser -u 10001 -S appuser -G appgroup \
    && mkdir -p /config /data /app/web /app/widgets \
    && chown -R 10001:10001 /config /data /app/web /app/widgets

WORKDIR /app

# Copy compiled executable from builder stage
COPY --from=builder --link --chown=10001:10001 /app/server /app/server

# Switch to unprivileged non-root user
USER 10001:10001

ENV PORT=8080 \
    HOST=0.0.0.0

EXPOSE 8080

# Native Busybox wget probe to avoid IPv6 musl localhost delays and extra dependencies
HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --start-interval=2s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://127.0.0.1:8080/healthz || exit 1

# PID 1 execution ensures instant SIGTERM handling and graceful shutdown
ENTRYPOINT ["/app/server"]

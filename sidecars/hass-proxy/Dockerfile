# syntax=docker/dockerfile:1
# Build stage: compile static Go binary using official Go 1.27 toolchain
FROM --platform=$BUILDPLATFORM golang:1.27.1-alpine AS builder

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
    go build -cover -covermode=atomic -trimpath -ldflags="-s -w -extldflags '-static'" -o /app/hass-proxy ./sidecars/hass-proxy

# Runtime stage: minimal hardened Alpine 3.21 environment
FROM alpine:3.21

# Install runtime certificates and create non-root user
RUN --mount=type=cache,target=/etc/apk/cache,sharing=locked \
    apk add ca-certificates \
    && addgroup -g 10001 -S appgroup \
    && adduser -u 10001 -S appuser -G appgroup \
    && mkdir -p /coverage \
    && chown -R 10001:10001 /coverage

WORKDIR /app

# Copy compiled executable from builder stage
COPY --from=builder --link --chown=10001:10001 /app/hass-proxy /app/hass-proxy

# Switch to unprivileged non-root user
USER 10001:10001

EXPOSE 8095

# Native Busybox wget probe for Docker health monitoring
HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --start-interval=2s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://127.0.0.1:8095/healthz || exit 1

# PID 1 execution ensures instant SIGTERM handling and graceful shutdown
ENTRYPOINT ["/app/hass-proxy"]

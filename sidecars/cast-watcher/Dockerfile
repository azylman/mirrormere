# Build stage: compile static Go binary using official Go 1.24 toolchain
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS builder

WORKDIR /src

# Copy module definitions first for optimal layer caching
COPY go.mod go.sum* ./
RUN go mod download

# Copy source tree and compile minimal static Linux executable
ARG TARGETOS TARGETARCH
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -extldflags '-static'" -o /app/cast-watcher ./sidecars/cast-watcher

# Runtime stage: minimal hardened Alpine 3.21 environment
FROM alpine:3.21

# Install runtime certificates and create non-root user
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -g 10001 -S appgroup \
    && adduser -u 10001 -S appuser -G appgroup

WORKDIR /app

# Copy compiled executable from builder stage
COPY --from=builder /app/cast-watcher /app/cast-watcher

# Switch to unprivileged non-root user
USER 10001:10001

ENV CONTROL_PORT=8090

EXPOSE 8090

# Native Busybox wget probe for Docker health monitoring
HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://127.0.0.1:8090/healthz || exit 1

# PID 1 execution ensures instant SIGTERM handling and graceful shutdown
ENTRYPOINT ["/app/cast-watcher"]

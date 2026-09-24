# Build stage: compile static Go binary using official Go 1.24 toolchain
FROM golang:1.24-alpine AS builder

WORKDIR /src

# Copy module definitions first for optimal layer caching
# Wildcard ensures build succeeds even when go.sum does not yet exist
COPY go.mod go.sum* ./
RUN go mod download

# Copy source tree and compile minimal static Linux executable
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -extldflags '-static'" -o /app/server ./cmd/server

# Runtime stage: minimal hardened Alpine 3.21 environment
FROM alpine:3.21

# Install runtime certificates and timezone database
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -g 10001 -S appgroup \
    && adduser -u 10001 -S appuser -G appgroup

WORKDIR /app

# Copy compiled executable from builder stage
COPY --from=builder /app/server /app/server

# Switch to unprivileged non-root user
USER 10001:10001

ENV PORT=8080 \
    HOST=0.0.0.0

EXPOSE 8080

# Native Busybox wget probe to avoid IPv6 musl localhost delays and extra dependencies
HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://127.0.0.1:8080/healthz || exit 1

# PID 1 execution ensures instant SIGTERM handling and graceful shutdown
ENTRYPOINT ["/app/server"]

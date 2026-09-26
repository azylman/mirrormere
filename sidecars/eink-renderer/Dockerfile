# sidecars/eink-renderer/Dockerfile - Headless E-Ink Snapshot Sidecar
# Reference: SPEC-003 §4-§5, SPEC-009 §3

FROM node:20-alpine

# Install headless Chromium, fonts (Noto for typography & icons), and CA certificates
RUN apk add --no-cache \
    chromium \
    font-noto \
    font-noto-cjk \
    ca-certificates \
    tzdata \
    wget \
    && addgroup -g 10001 -S appgroup \
    && adduser -u 10001 -S appuser -G appgroup \
    && mkdir -p /app /tmp/chromium-eink \
    && chown -R 10001:10001 /app /tmp/chromium-eink

WORKDIR /app

# Copy application files (zero external npm dependencies required)
COPY package.json ./
COPY src/ ./src/
COPY entrypoint.sh ./

RUN chmod +x ./entrypoint.sh \
    && chown -R 10001:10001 /app

USER 10001:10001

ENV PORT=8081 \
    NODE_OPTIONS="--experimental-websocket" \
    CHROMIUM_BIN=/usr/bin/chromium-browser \
    CORE_URL=http://mirrormere-core:8080 \
    DISPLAY_URL=http://mirrormere-core:8080/display \
    CDP_URL=http://127.0.0.1:9222

EXPOSE 8081

HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://127.0.0.1:8081/healthz || exit 1

ENTRYPOINT ["./entrypoint.sh"]

# syntax=docker/dockerfile:1
# sidecars/eink-renderer/Dockerfile - Headless E-Ink Snapshot Sidecar
# Reference: SPEC-003 §4-§5, SPEC-009 §3

FROM node:20-alpine

# Install headless Chromium, fonts (Noto for typography & icons), and CA certificates
RUN --mount=type=cache,target=/etc/apk/cache,sharing=locked \
    apk add \
        chromium \
        font-noto \
        font-noto-cjk \
        ca-certificates \
        tzdata \
    && rm -rf /usr/local/lib/node_modules/npm /opt/yarn* /usr/local/bin/npm /usr/local/bin/npx /usr/local/bin/yarn* /usr/local/bin/corepack \
    && addgroup -g 10001 -S appgroup \
    && adduser -u 10001 -S appuser -G appgroup \
    && mkdir -p /app /tmp/chromium-eink \
    && chown -R 10001:10001 /app /tmp/chromium-eink

WORKDIR /app

# Copy application files (zero external npm dependencies required)
COPY --link --chown=10001:10001 package.json ./
COPY --link --chown=10001:10001 src/ ./src/
COPY --link --chown=10001:10001 --chmod=0755 entrypoint.sh ./

USER 10001:10001

ENV PORT=8081 \
    NODE_OPTIONS="--experimental-websocket" \
    CHROMIUM_BIN=/usr/bin/chromium-browser \
    CORE_URL=http://mirrormere-core:8080 \
    DISPLAY_URL=http://mirrormere-core:8080/display \
    CDP_URL=http://127.0.0.1:9222

EXPOSE 8081

HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --start-interval=2s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://127.0.0.1:8081/healthz || exit 1

ENTRYPOINT ["./entrypoint.sh"]

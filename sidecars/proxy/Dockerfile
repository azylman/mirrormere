# syntax=docker/dockerfile:1
# sidecars/proxy/Dockerfile - Standalone Unprivileged Nginx Reverse Proxy Sidecar

FROM nginxinc/nginx-unprivileged:1.27-alpine

ENV PROXY_PORT=8080 \
    CORE_HOST=mirrormere-core:8080 \
    REMOTE_HOST=mirrormere-remote:8092 \
    GO2RTC_HOST=mirrormere-go2rtc:1984

COPY nginx.conf /etc/nginx/nginx.conf
COPY templates/default.conf.template /etc/nginx/templates/default.conf.template

EXPOSE 8080

HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --start-interval=2s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://127.0.0.1:8080/healthz || exit 1

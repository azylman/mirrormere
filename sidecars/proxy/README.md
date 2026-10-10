# Mirrormere Proxy Sidecar

A standalone, unprivileged Nginx reverse proxy sidecar for Mirrormere kiosk deployments. It terminates incoming client HTTP and WebSocket traffic, manages trailing slash redirects, and routes traffic cleanly across Mirrormere Core, Mirrormere Remote, and go2rtc.

## Architecture

The proxy sidecar is containerized using `nginxinc/nginx-unprivileged:1.27-alpine` to run safely in rootless container environments:
- Runs as an unprivileged user (UID 101) with internal pid file at `/tmp/nginx.pid` and buffer directories in `/tmp/`.
- Uses official Nginx template substitution (`/etc/nginx/templates/default.conf.template`) on container startup to evaluate environment variables without requiring custom entrypoint scripts.
- Disables proxy buffering and enforces long connection timeouts (86400s) to guarantee uninterrupted Server-Sent Events (SSE) and WebSocket streams.
- Manages Connection and Upgrade headers dynamically via an HTTP upgrade map.

## Routing Table

- `GET = /healthz`
  - Purpose: Container health probe.
  - Action: Returns HTTP 200 `OK\n` (text/plain) with access logging disabled.

- `GET = /remote`
  - Purpose: Convenience redirect.
  - Action: HTTP 301 redirect to `/remote/`.

- `GET = /kiosk/remote`
  - Purpose: Convenience redirect.
  - Action: HTTP 301 redirect to `/kiosk/remote/`.

- `GET = /kiosk`
  - Purpose: Convenience redirect.
  - Action: HTTP 301 redirect to `/kiosk/`.

- `/remote/*`
  - Purpose: Bluetooth HID and remote control REST/WebSocket endpoints.
  - Action: Rewrites `/remote/(.*)` to `/$1`, proxies to `http://${REMOTE_HOST}` with proxy buffering disabled.

- `/kiosk/remote/*`
  - Purpose: Alternate kiosk remote routing shortcut.
  - Action: Rewrites `/kiosk/remote/(.*)` to `/$1`, proxies to `http://${REMOTE_HOST}` with proxy buffering disabled.

- `/kiosk/*`
  - Purpose: Kiosk display interface and static assets.
  - Action: Rewrites exact `/kiosk/` to `/display` and `/kiosk/(.*)` to `/$1`, proxies to `http://${CORE_HOST}` with SSE buffering off, 86400s timeouts, and WebSocket upgrade headers.

- `/webrtc/*`
  - Purpose: WebRTC video streaming gateway.
  - Action: Rewrites `/webrtc/(.*)` to `/$1`, proxies to `http://${GO2RTC_HOST}` with WebSocket upgrade headers and 86400s timeouts.

- `/*`
  - Purpose: Mirrormere Core default catch-all route (dashboard, API, SSE event stream).
  - Action: Proxies directly to `http://${CORE_HOST}` with proxy buffering disabled, 86400s timeouts, and WebSocket upgrade headers.

## Environment Variables

- `PROXY_PORT`: Port on which Nginx listens inside the container (default: `8080`).
- `CORE_HOST`: Target host and port for Mirrormere Core service (default: `mirrormere-core:8080`).
- `REMOTE_HOST`: Target host and port for Mirrormere Remote sidecar (default: `mirrormere-remote:8092`).
- `GO2RTC_HOST`: Target host and port for go2rtc WebRTC gateway (default: `mirrormere-go2rtc:1984`).

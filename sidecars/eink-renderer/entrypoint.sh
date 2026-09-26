#!/bin/sh
set -e

CHROMIUM_BIN="${CHROMIUM_BIN:-/usr/bin/chromium}"
if [ ! -x "$CHROMIUM_BIN" ] && [ -x "/usr/bin/chromium-browser" ]; then
    CHROMIUM_BIN="/usr/bin/chromium-browser"
fi

START_LOCAL_CHROMIUM=true
if [ -n "$CDP_URL" ]; then
    case "$CDP_URL" in
        *127.0.0.1*|*localhost*)
            START_LOCAL_CHROMIUM=true
            ;;
        *)
            START_LOCAL_CHROMIUM=false
            ;;
    esac
fi

if [ "$START_LOCAL_CHROMIUM" = true ]; then
    echo "[eink-renderer] starting headless chromium..."
    mkdir -p /tmp/chromium-eink
    "$CHROMIUM_BIN" \
        --headless=new \
        --remote-debugging-port=9222 \
        --remote-debugging-address=127.0.0.1 \
        --no-sandbox \
        --disable-gpu \
        --disable-dev-shm-usage \
        --disable-software-rasterizer \
        --hide-scrollbars \
        --window-size=800,480 \
        --user-data-dir=/tmp/chromium-eink &
    CHROME_PID=$!

    echo "[eink-renderer] waiting for chromium CDP on 127.0.0.1:9222..."
    count=0
    until wget -q -O - http://127.0.0.1:9222/json/version >/dev/null 2>&1; do
        sleep 0.1
        count=$((count + 1))
        if [ "$count" -ge 100 ]; then
            echo "[eink-renderer] warning: chromium CDP failed to start within 10s"
            break
        fi
    done
    if [ "$count" -lt 100 ]; then
        echo "[eink-renderer] chromium CDP is ready."
    fi
fi

exec node --experimental-websocket src/server.js

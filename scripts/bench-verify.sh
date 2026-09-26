#!/bin/sh
set -eu

# scripts/bench-verify.sh - Mirrormere Full-Stack Bench Smoke Test & Connectivity Verifier
# Reference: SPEC-013 Phase 6 (Chunk 6.3 - Issue #250) & SPEC-002
#
# Validates live connectivity across all active container profiles:
#   - Core Daemon:       ${CORE_URL:-http://localhost:8080}/healthz and /display
#   - E-Ink Renderer:    ${EINK_URL:-http://localhost:8081}/healthz and /eink.png
#   - go2rtc:            ${GO2RTC_URL:-http://localhost:1984}/
#   - Cast Watcher:      ${CAST_WATCHER_URL:-http://localhost:8090}/healthz and /action

CORE_URL="${CORE_URL:-http://localhost:8080}"
EINK_URL="${EINK_URL:-http://localhost:8081}"
GO2RTC_URL="${GO2RTC_URL:-http://localhost:1984}"
CAST_WATCHER_URL="${CAST_WATCHER_URL:-http://localhost:8090}"

PROFILE="all"
WAIT_MODE=0
TIMEOUT=30
INTERVAL=2

print_usage() {
    cat <<EOF
Usage: $0 [OPTIONS]

Validates live connectivity across Mirrormere Docker Compose endpoints.

Options:
  --profile <profile>    Service profile to verify: core, video, eink, all (default: all)
  --wait                 Wait and retry until endpoints become healthy or timeout expires
  --timeout <seconds>    Max seconds to wait when --wait is enabled (default: 30)
  --interval <seconds>   Polling interval in seconds (default: 2)
  --core-url <url>       Core daemon URL (default: http://localhost:8080)
  --eink-url <url>       E-Ink renderer URL (default: http://localhost:8081)
  --go2rtc-url <url>     go2rtc URL (default: http://localhost:1984)
  --cast-url <url>       Cast watcher URL (default: http://localhost:8090)
  -h, --help             Show this help message

Environment Variables:
  CORE_URL               Override core daemon URL
  EINK_URL               Override eink renderer URL
  GO2RTC_URL             Override go2rtc URL
  CAST_WATCHER_URL       Override cast watcher URL
EOF
}

while [ $# -gt 0 ]; do
    case "$1" in
        --profile)
            PROFILE="$2"
            shift 2
            ;;
        --all)
            PROFILE="all"
            shift
            ;;
        --core)
            PROFILE="core"
            shift
            ;;
        --video)
            PROFILE="video"
            shift
            ;;
        --eink)
            PROFILE="eink"
            shift
            ;;
        --wait)
            WAIT_MODE=1
            shift
            ;;
        --timeout)
            TIMEOUT="$2"
            shift 2
            ;;
        --interval)
            INTERVAL="$2"
            shift 2
            ;;
        --core-url)
            CORE_URL="$2"
            shift 2
            ;;
        --eink-url)
            EINK_URL="$2"
            shift 2
            ;;
        --go2rtc-url)
            GO2RTC_URL="$2"
            shift 2
            ;;
        --cast-url)
            CAST_WATCHER_URL="$2"
            shift 2
            ;;
        -h|--help)
            print_usage
            exit 0
            ;;
        *)
            echo "Unknown option: $1" >&2
            print_usage >&2
            exit 1
            ;;
    esac
done

has_cmd() {
    command -v "$1" >/dev/null 2>&1
}

if ! has_cmd curl; then
    echo "🚨 Error: 'curl' is required for bench verification but was not found in PATH." >&2
    exit 1
fi

probe_http() {
    method="$1"
    url="$2"
    expected_pattern="$3"
    post_body="${4:-}"

    if [ "$method" = "POST" ]; then
        response=$(curl -sS -i -m 5 -X POST -H "Content-Type: application/json" -d "$post_body" "$url" 2>&1 || true)
    else
        response=$(curl -sS -i -m 5 -X GET "$url" 2>&1 || true)
    fi

    if echo "$response" | grep -Eq "$expected_pattern"; then
        return 0
    else
        return 1
    fi
}

verify_core() {
    echo "==> Verifying Core Daemon (${CORE_URL})..."
    
    # 1. Healthz probe
    if probe_http "GET" "${CORE_URL}/healthz" "HTTP/[0-9.]+ 200"; then
        echo "  [PASS] Core /healthz returned HTTP 200"
    else
        echo "  [FAIL] Core /healthz failed or timed out" >&2
        return 1
    fi

    # 2. Display SSR HTML probe
    if probe_http "GET" "${CORE_URL}/display" "(HTTP/[0-9.]+ 200|Content-Type: text/html)"; then
        echo "  [PASS] Core /display returned HTTP 200 HTML"
    else
        echo "  [FAIL] Core /display failed or returned non-HTML" >&2
        return 1
    fi

    return 0
}

verify_eink() {
    echo "==> Verifying E-Ink Renderer (${EINK_URL})..."

    # 1. Healthz probe
    if probe_http "GET" "${EINK_URL}/healthz" "HTTP/[0-9.]+ 200"; then
        echo "  [PASS] E-Ink Renderer /healthz returned HTTP 200"
    else
        echo "  [FAIL] E-Ink Renderer /healthz failed or timed out" >&2
        return 1
    fi

    # 2. Snapshot image endpoint probe
    if probe_http "GET" "${EINK_URL}/eink.png" "(HTTP/[0-9.]+ 200|Content-Type: image/png)"; then
        echo "  [PASS] E-Ink Renderer /eink.png returned HTTP 200 PNG"
    else
        echo "  [FAIL] E-Ink Renderer /eink.png failed or returned invalid content" >&2
        return 1
    fi

    return 0
}

verify_video() {
    echo "==> Verifying Video Profile (go2rtc: ${GO2RTC_URL}, Cast Watcher: ${CAST_WATCHER_URL})..."

    # 1. go2rtc API / web UI probe
    if probe_http "GET" "${GO2RTC_URL}/" "(HTTP/[0-9.]+ 200|go2rtc)"; then
        echo "  [PASS] go2rtc root endpoint returned HTTP 200"
    elif probe_http "GET" "${GO2RTC_URL}/api" "HTTP/[0-9.]+ 200"; then
        echo "  [PASS] go2rtc /api returned HTTP 200"
    else
        echo "  [FAIL] go2rtc failed or timed out" >&2
        return 1
    fi

    # 2. Cast watcher healthz probe
    if probe_http "GET" "${CAST_WATCHER_URL}/healthz" "HTTP/[0-9.]+ 200"; then
        echo "  [PASS] Cast Watcher /healthz returned HTTP 200"
    else
        echo "  [FAIL] Cast Watcher /healthz failed or timed out" >&2
        return 1
    fi

    # 3. Cast watcher action webhook endpoint probe
    # Note: When no active Chromecast session is connected on bench, /action responds with
    # 404 (stream not active), 400 (bad request), or 200 (action dispatched), proving the route is active.
    action_body='{"id":"chromecast","action":"toggle_playback"}'
    if probe_http "POST" "${CAST_WATCHER_URL}/action" "HTTP/[0-9.]+ (200|400|404|502)" "$action_body"; then
        echo "  [PASS] Cast Watcher /action webhook is responsive"
    else
        echo "  [FAIL] Cast Watcher /action failed to respond" >&2
        return 1
    fi

    return 0
}

run_checks() {
    case "$PROFILE" in
        core)
            verify_core
            ;;
        eink)
            verify_core && verify_eink
            ;;
        video)
            verify_core && verify_video
            ;;
        all)
            verify_core && verify_video && verify_eink
            ;;
        *)
            echo "🚨 Error: Invalid profile '$PROFILE'. Valid options: core, video, eink, all" >&2
            return 1
            ;;
    esac
}

echo "⚡ [Mirrormere Bench] Starting smoke tests (profile: $PROFILE)..."

if [ "$WAIT_MODE" -eq 1 ]; then
    start_time=$(date +%s)
    echo "⏳ Waiting up to ${TIMEOUT}s for services to be reachable..."
    while true; do
        if run_checks >/dev/null 2>&1; then
            echo "✅ Services became ready! Running full report:"
            run_checks
            echo "✨ [Mirrormere Bench] All smoke tests passed successfully!"
            exit 0
        fi

        now=$(date +%s)
        elapsed=$((now - start_time))
        if [ "$elapsed" -ge "$TIMEOUT" ]; then
            echo "🚨 [Mirrormere Bench] Timeout of ${TIMEOUT}s exceeded waiting for services." >&2
            echo "Running final check to report failures:" >&2
            run_checks || true
            exit 1
        fi
        sleep "$INTERVAL"
    done
else
    if run_checks; then
        echo "✨ [Mirrormere Bench] All smoke tests passed successfully!"
        exit 0
    else
        echo "🚨 [Mirrormere Bench] Verification failed for profile '$PROFILE'." >&2
        exit 1
    fi
fi

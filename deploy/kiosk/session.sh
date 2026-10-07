#!/usr/bin/env bash
# Mirrormere Touch Kiosk Stage 2 Session (Inside Cage Wayland Compositor)
# Supervised child process of Cage; enforces bidirectional lifecycle coupling,
# strict file descriptor isolation, and hardware-accelerated Chrome presentation.
set -euo pipefail

# Source configuration overrides if present
if [[ -f /etc/default/mirrormere-kiosk ]]; then
    # shellcheck source=/dev/null
    source /etc/default/mirrormere-kiosk
fi

export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
export WAYLAND_DISPLAY="${WAYLAND_DISPLAY:-wayland-0}"
export XCURSOR_THEME="${XCURSOR_THEME:-transparent}"
export XCURSOR_SIZE="${XCURSOR_SIZE:-24}"

DISPLAY_URL="${MIRRORMERE_DISPLAY_URL:-http://localhost:8080/display}"

# Identify device voice node id if configured or readable from ear config
EAR_CONFIG="${MIRRORMERE_EAR_CONFIG:-/etc/mirrormere/voice.yaml}"
NODE_ID="${MIRRORMERE_NODE_ID:-}"
if [[ -z "$NODE_ID" && -r "$EAR_CONFIG" ]]; then
    NODE_ID="$(sed -n -E 's/^[[:space:]]*node_id:[[:space:]]*["'"'"']?([A-Za-z0-9._-]+)["'"'"']?[[:space:]]*(#.*)?$/\1/p' "$EAR_CONFIG" | head -n 1 || true)"
fi
NODE_ID="$(printf '%s' "$NODE_ID" | tr -cd 'A-Za-z0-9._-')"
if [[ -n "$NODE_ID" && "$DISPLAY_URL" != *"node="* ]]; then
    if [[ "$DISPLAY_URL" == *"?"* ]]; then
        DISPLAY_URL="${DISPLAY_URL}&node=${NODE_ID}"
    else
        DISPLAY_URL="${DISPLAY_URL}?node=${NODE_ID}"
    fi
fi

# Append hide_cursor=true to display URL if enabled and not already explicitly set
HIDE_CURSOR="${MIRRORMERE_HIDE_CURSOR:-true}"
if [[ "$HIDE_CURSOR" == "true" && "$DISPLAY_URL" != *"hide_cursor="* ]]; then
    if [[ "$DISPLAY_URL" == *"?"* ]]; then
        DISPLAY_URL="${DISPLAY_URL}&hide_cursor=true"
    else
        DISPLAY_URL="${DISPLAY_URL}?hide_cursor=true"
    fi
fi

# Cage compositor is our parent process
CAGE_PID="$PPID"
CHROME_PID=""
WATCHDOG_PID=""
SWAYIDLE_PID=""
KANSHI_PID=""

# Helper: close inherited file descriptors > 2 in background subshells to prevent
# leaking Cage's internal pipe and causing compositor hangs on exit.
isolate_fds() {
    for fd in /proc/self/fd/*; do
        local f="${fd##*/}"
        if [[ "$f" =~ ^[0-9]+$ ]] && [ "$f" -gt 2 ] && [ "$f" -ne 3 ]; then
            eval "exec ${f}>&-" 2>/dev/null || true
        fi
    done
}

# Master cleanup trap to ensure bidirectional lifecycle coupling
cleanup() {
    trap - EXIT INT TERM HUP
    echo "[Mirrormere Session] Lifecycle cleanup initiated..."

    # 1. Stop background watchdog
    if [[ -n "${WATCHDOG_PID:-}" ]] && kill -0 "$WATCHDOG_PID" 2>/dev/null; then
        kill "$WATCHDOG_PID" 2>/dev/null || true
    fi

    # 2. Stop helper daemons
    if [[ -n "${SWAYIDLE_PID:-}" ]] && kill -0 "$SWAYIDLE_PID" 2>/dev/null; then
        kill "$SWAYIDLE_PID" 2>/dev/null || true
    fi
    pkill -u "$(id -u)" swayidle 2>/dev/null || true

    if [[ -n "${KANSHI_PID:-}" ]] && kill -0 "$KANSHI_PID" 2>/dev/null; then
        kill "$KANSHI_PID" 2>/dev/null || true
    fi
    pkill -u "$(id -u)" kanshi 2>/dev/null || true

    # 3. Ensure browser is terminated if Cage exited first or we received a signal
    if [[ -n "${CHROME_PID:-}" ]] && kill -0 "$CHROME_PID" 2>/dev/null; then
        echo "[Mirrormere Session] Stopping browser (PID $CHROME_PID)..."
        kill -TERM "$CHROME_PID" 2>/dev/null || true
        for _ in $(seq 1 15); do
            kill -0 "$CHROME_PID" 2>/dev/null || break
            sleep 0.1 2>/dev/null || python3 -c "import time; time.sleep(0.1)" 2>/dev/null || true
        done
        kill -KILL "$CHROME_PID" 2>/dev/null || true
    fi

    # 4. If Chrome exited first, terminate Cage so systemd Restart=always respawns the stack
    if [[ -n "${CAGE_PID:-}" ]] && kill -0 "$CAGE_PID" 2>/dev/null; then
        echo "[Mirrormere Session] Browser terminated; stopping Cage compositor (PID $CAGE_PID)..."
        kill -TERM "$CAGE_PID" 2>/dev/null || true
    fi
}
trap cleanup EXIT INT TERM HUP

# Wait for Wayland display socket with bounded timeout (max 5.0 seconds)
SOCKET_READY=0
for _ in $(seq 1 50); do
    if [[ -S "${XDG_RUNTIME_DIR}/${WAYLAND_DISPLAY}" ]]; then
        SOCKET_READY=1
        break
    fi
    sleep 0.1 2>/dev/null || python3 -c "import time; time.sleep(0.1)" 2>/dev/null || true
done

if [[ "$SOCKET_READY" -ne 1 ]]; then
    echo "[Mirrormere Session] Error: Wayland display socket (${XDG_RUNTIME_DIR}/${WAYLAND_DISPLAY}) timed out after 5s" >&2
    exit 1
fi

# Launch kanshi daemon with FD isolation if configuration exists
if [[ -f "${HOME}/.config/kanshi/config" ]] && command -v kanshi >/dev/null 2>&1; then
    (
        isolate_fds
        exec kanshi >/tmp/kanshi.log 2>&1
    ) &
    KANSHI_PID=$!
fi

# Auto-detect display output if not explicitly configured (wait up to 5s for DRM readiness)
OUTPUT="${MIRRORMERE_OUTPUT:-}"
for _ in $(seq 1 25); do
    if [[ -z "$OUTPUT" ]] && command -v wlr-randr >/dev/null 2>&1; then
        OUTPUT="$(wlr-randr 2>/dev/null | awk '/^[A-Za-z0-9-]+ / { print $1; exit }' || true)"
    fi
    if [[ -n "$OUTPUT" ]]; then
        break
    fi
    sleep 0.2 2>/dev/null || python3 -c "import time; time.sleep(0.2)" 2>/dev/null || true
done
OUTPUT="${OUTPUT:-HDMI-A-1}"

# Apply display output scaling
SCALE_FACTOR="${MIRRORMERE_SCALE_FACTOR:-1.0}"
if command -v wlr-randr >/dev/null 2>&1; then
    wlr-randr --output "$OUTPUT" --scale "$SCALE_FACTOR" 2>/dev/null || true
fi

# Wake and set DDC brightness if hardware tool is available (VCP 0x10 is display luminance)
DDC_BRIGHTNESS="${MIRRORMERE_DDC_BRIGHTNESS:-100}"
if command -v ddcutil >/dev/null 2>&1; then
    ddcutil setvcp 10 "$DDC_BRIGHTNESS" 2>/dev/null &
fi

# Start swayidle supervisor loop for daytime inactivity blanking with strict FD isolation
IDLE_TIMEOUT_SECONDS="${MIRRORMERE_IDLE_TIMEOUT:-600}"
run_swayidle() {
    isolate_fds
    while true; do
        swayidle -w \
            timeout "$IDLE_TIMEOUT_SECONDS" "wlr-randr --output ${OUTPUT} --off" \
            resume "wlr-randr --output ${OUTPUT} --on" || true
        sleep 0.5 2>/dev/null || python3 -c "import time; time.sleep(0.5)" 2>/dev/null || true
    done
}
if [[ "$IDLE_TIMEOUT_SECONDS" -gt 0 ]] && command -v swayidle >/dev/null 2>&1; then
    run_swayidle &
    SWAYIDLE_PID=$!
fi

# Clean up any stale Chrome singleton locks from past crashes
CHROME_USER_DATA_DIR="${HOME}/.config/mirrormere-chrome"
rm -f "${CHROME_USER_DATA_DIR}/Singleton"* "${HOME}/.config/google-chrome/Singleton"* "${HOME}/.config/chromium/Singleton"* 2>/dev/null || true

# Locate Google Chrome or Chromium binary
CHROME_BIN=""
for candidate in "/opt/google/chrome/chrome" "google-chrome-stable" "google-chrome" "chromium" "chromium-browser"; do
    if command -v "$candidate" >/dev/null 2>&1 || [[ -x "$candidate" ]]; then
        CHROME_BIN="$candidate"
        break
    fi
done

if [[ -z "$CHROME_BIN" ]]; then
    echo "[Mirrormere Session] Error: Neither Google Chrome nor Chromium binary found on system" >&2
    exit 1
fi

CHROME_FLAGS=(
    --kiosk
    --noerrdialogs
    --ozone-platform=wayland
    "--enable-features=UseOzonePlatform,OverlayScrollbar"
    --force-device-scale-factor="${SCALE_FACTOR}"
    --no-first-run
    --no-default-browser-check
    --disable-infobars
    --disable-session-crashed-bubble
    --disable-translate
    --disable-features=Translate
    --touch-events=enabled
    --autoplay-policy=no-user-gesture-required
    --password-store=basic
    --disable-background-networking
    --disable-component-update
    --disable-sync
    --user-data-dir="${CHROME_USER_DATA_DIR}"
)

if [[ -n "${MIRRORMERE_REMOTE_DEBUGGING_PORT:-}" ]]; then
    CHROME_FLAGS+=(
        "--remote-debugging-port=${MIRRORMERE_REMOTE_DEBUGGING_PORT}"
        --remote-debugging-address=0.0.0.0
        '--remote-allow-origins=*'
    )
fi

echo "[Mirrormere Session] Executing Chrome supervisor (scale=${SCALE_FACTOR}, output=${OUTPUT})..."
"$CHROME_BIN" "${CHROME_FLAGS[@]}" "$DISPLAY_URL" &
CHROME_PID=$!

# Start parent-death watchdog with FD isolation: if Cage compositor dies unexpectedly, kill Chrome
(
    isolate_fds
    while kill -0 "$CAGE_PID" 2>/dev/null; do
        sleep 0.5 2>/dev/null || python3 -c "import time; time.sleep(0.5)" 2>/dev/null || true
    done
    if [[ -n "${CHROME_PID:-}" ]] && kill -0 "$CHROME_PID" 2>/dev/null; then
        echo "[Mirrormere Watchdog] Cage compositor disappeared; terminating Chrome..."
        kill -TERM "$CHROME_PID" 2>/dev/null || true
        sleep 1.0 2>/dev/null || python3 -c "import time; time.sleep(1.0)" 2>/dev/null || true
        kill -KILL "$CHROME_PID" 2>/dev/null || true
    fi
) &
WATCHDOG_PID=$!

# Wait synchronously for Chrome to exit
wait "$CHROME_PID" || true

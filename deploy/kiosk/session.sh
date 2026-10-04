#!/usr/bin/env bash
# Mirrormere Touch Kiosk Stage 2 Session (Inside Cage Wayland Compositor)
# Launched directly by Cage; Wayland display socket is active.
set -euo pipefail

# Source configuration overrides if present
if [[ -f /etc/default/mirrormere-kiosk ]]; then
    # shellcheck source=/dev/null
    source /etc/default/mirrormere-kiosk
fi

export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
export WAYLAND_DISPLAY="${WAYLAND_DISPLAY:-wayland-0}"
DISPLAY_URL="${MIRRORMERE_DISPLAY_URL:-http://localhost:8080/display}"

# Identify this device to the server so per-display widgets (chat-log) show this
# kiosk's own conversation. The node id is the ear's node_id: taken from
# MIRRORMERE_NODE_ID, else read from the ear config, so it is set once per device.
# A URL that already carries node= is left alone; no node id means no parameter.
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
IDLE_TIMEOUT_SECONDS="${MIRRORMERE_IDLE_TIMEOUT:-600}"

# Auto-detect display output if not explicitly configured
OUTPUT="${MIRRORMERE_OUTPUT:-}"
if [[ -z "$OUTPUT" ]]; then
    # Detect first connected output via wlr-randr
    OUTPUT="$(wlr-randr 2>/dev/null | awk '/^[A-Za-z0-9-]+ / { print $1; exit }' || true)"
fi
OUTPUT="${OUTPUT:-HDMI-A-1}"

echo "[Mirrormere Session] Active output: ${OUTPUT}, Target URL: ${DISPLAY_URL}"

# Start swayidle supervisor loop for daytime inactivity blanking with capacitive touch resume.
# Runs as a supervised background child of the Cage session; respawns if restarted by dpms.sh on.
run_swayidle() {
    while true; do
        swayidle -w \
            timeout "$IDLE_TIMEOUT_SECONDS" "wlr-randr --output ${OUTPUT} --off" \
            resume "wlr-randr --output ${OUTPUT} --on" || true
        # Brief backoff before respawning if swayidle was terminated to reset state
        sleep 0.5
    done
}
run_swayidle &
SWAYIDLE_SUB_PID=$!

# Trap signals to cleanly terminate background swayidle supervisor and daemon on session shutdown
cleanup() {
    echo "[Mirrormere Session] Terminating session child processes..."
    trap - EXIT INT TERM
    if [[ -n "${SWAYIDLE_SUB_PID:-}" ]]; then
        kill "$SWAYIDLE_SUB_PID" 2>/dev/null || true
    fi
    pkill -u "$(id -u)" swayidle 2>/dev/null || true
}
trap cleanup EXIT INT TERM

# Hardware-accelerated Chromium kiosk flags
CHROMIUM_FLAGS=(
    --kiosk
    --noerrdialogs
    --disable-infobars
    --disable-session-crashed-bubble
    --disable-translate
    --check-for-update-interval=31536000
    --enable-features=OverlayScrollbar
    --use-gl=egl
    --ozone-platform=wayland
    --enable-wayland-ime
    --autoplay-policy=no-user-gesture-required
    --password-store=basic
    --disable-background-networking
    --disable-component-update
    --disable-sync
)

echo "[Mirrormere Session] Executing Chromium in kiosk mode..."
chromium "${CHROMIUM_FLAGS[@]}" "$DISPLAY_URL"

#!/usr/bin/env bash
# Mirrormere Touch Display DPMS & Power Management Helper
# Dispatches wlr-randr power commands and manages nightly browser restarts.
# Callable by root (systemd timers) or the unprivileged 'display' user.
set -euo pipefail

# Source configuration overrides if present
if [[ -f /etc/default/mirrormere-display ]]; then
    # shellcheck source=/dev/null
    source /etc/default/mirrormere-display
elif [[ -f /etc/default/mirrormere-kiosk ]]; then
    # shellcheck source=/dev/null
    source /etc/default/mirrormere-kiosk
fi

DISPLAY_USER="${MIRRORMERE_DISPLAY_USER:-${MIRRORMERE_KIOSK_USER:-display}}"
if [[ "$DISPLAY_USER" == "display" ]] && ! id -u display >/dev/null 2>&1 && id -u kiosk >/dev/null 2>&1; then
    DISPLAY_USER="kiosk"
fi
DISPLAY_UID="$(id -u "$DISPLAY_USER" 2>/dev/null || echo 1000)"
if [[ "$(id -u)" -ne "$DISPLAY_UID" ]]; then
    XDG_RUNTIME_DIR="/run/user/${DISPLAY_UID}"
else
    XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/${DISPLAY_UID}}"
fi
WAYLAND_DISPLAY="${WAYLAND_DISPLAY:-wayland-0}"

# Run command inside display user Wayland context
run_display_wayland() {
    if [[ "$(id -u)" -eq "$DISPLAY_UID" ]]; then
        env XDG_RUNTIME_DIR="$XDG_RUNTIME_DIR" WAYLAND_DISPLAY="$WAYLAND_DISPLAY" "$@"
    else
        runuser -u "$DISPLAY_USER" -- env XDG_RUNTIME_DIR="$XDG_RUNTIME_DIR" WAYLAND_DISPLAY="$WAYLAND_DISPLAY" "$@"
    fi
}
run_kiosk_wayland() {
    run_display_wayland "$@"
}

# Resolve active display output
get_output() {
    local out="${MIRRORMERE_OUTPUT:-}"
    if [[ -z "$out" ]]; then
        out="$(run_display_wayland wlr-randr 2>/dev/null | awk '/^[A-Za-z0-9-]+ / { print $1; exit }' || true)"
    fi
    echo "${out:-HDMI-A-1}"
}

# Wait up to 10s for Wayland socket and wlr-randr responsiveness
wait_for_wayland() {
    local socket="${XDG_RUNTIME_DIR}/${WAYLAND_DISPLAY}"
    local attempts=20
    for ((i=1; i<=attempts; i++)); do
        if [[ -S "$socket" ]] && run_display_wayland wlr-randr >/dev/null 2>&1; then
            return 0
        fi
        sleep 0.5
    done
    echo "[Mirrormere DPMS] Warning: Wayland socket not ready after ${attempts} attempts" >&2
    return 1
}

# Trigger swayidle to transition immediately into idle state (fires timeout command)
trigger_swayidle_idle() {
    if pgrep -u "$DISPLAY_USER" swayidle >/dev/null 2>&1; then
        echo "[Mirrormere DPMS] Triggering idle state via swayidle SIGUSR1"
        pkill -USR1 -u "$DISPLAY_USER" swayidle 2>/dev/null || true
    fi
}

# Reset swayidle to active state by terminating current process; session supervisor respawns fresh timer
reset_swayidle_active() {
    if pgrep -u "$DISPLAY_USER" swayidle >/dev/null 2>&1; then
        echo "[Mirrormere DPMS] Resetting swayidle timeout via SIGTERM"
        pkill -TERM -u "$DISPLAY_USER" swayidle 2>/dev/null || true
    fi
}

cmd="${1:-status}"

case "$cmd" in
    off)
        if wait_for_wayland; then
            output="$(get_output)"
            echo "[Mirrormere DPMS] Powering off output: ${output}"
            run_display_wayland wlr-randr --output "${output}" --off
            trigger_swayidle_idle
        fi
        ;;
    on)
        if wait_for_wayland; then
            output="$(get_output)"
            echo "[Mirrormere DPMS] Powering on output: ${output}"
            run_display_wayland wlr-randr --output "${output}" --on
            reset_swayidle_active
        fi
        ;;
    status)
        if wait_for_wayland; then
            run_display_wayland wlr-randr
            if pgrep -u "$DISPLAY_USER" swayidle >/dev/null 2>&1; then
                echo "[Mirrormere DPMS] swayidle daemon is active (PID: $(pgrep -d, -u "$DISPLAY_USER" swayidle))"
            else
                echo "[Mirrormere DPMS] swayidle daemon is not active"
            fi
        else
            echo "[Mirrormere DPMS] Compositor offline or socket inaccessible"
            exit 1
        fi
        ;;
    night-restart)
        # Fix for Issue #257 & #280: Restart display service, re-assert DPMS off, and trigger idle state
        echo "[Mirrormere DPMS] Restarting mirrormere-display.service for nightly memory refresh..."
        systemctl restart mirrormere-display.service 2>/dev/null || systemctl restart mirrormere-kiosk.service
        if wait_for_wayland; then
            output="$(get_output)"
            echo "[Mirrormere DPMS] Re-asserting night blackout on output: ${output}"
            run_display_wayland wlr-randr --output "${output}" --off
            # Wait briefly for swayidle to initialize, then trigger idle state so night touches wake screen
            for ((i=1; i<=10; i++)); do
                if pgrep -u "$DISPLAY_USER" swayidle >/dev/null 2>&1; then
                    trigger_swayidle_idle
                    break
                fi
                sleep 0.2
            done
        else
            echo "[Mirrormere DPMS] Warning: Could not re-assert night blackout after restart" >&2
        fi
        ;;
    *)
        echo "Usage: $0 {on|off|status|night-restart}" >&2
        exit 1
        ;;
esac

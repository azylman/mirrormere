#!/usr/bin/env bash
# Mirrormere Touch Display Stage 1 Launcher (Host -> Cage Compositor)
# Runs as unprivileged display user under systemd mirrormere-display.service.
set -euo pipefail

# Source configuration overrides if present (with fallback to legacy kiosk config)
if [[ -f /etc/default/mirrormere-display ]]; then
    # shellcheck source=/dev/null
    source /etc/default/mirrormere-display
elif [[ -f /etc/default/mirrormere-kiosk ]]; then
    # shellcheck source=/dev/null
    source /etc/default/mirrormere-kiosk
fi

# Ensure user runtime directory exists
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
if [[ ! -d "$XDG_RUNTIME_DIR" ]]; then
    echo "[Mirrormere Display] XDG_RUNTIME_DIR ($XDG_RUNTIME_DIR) does not exist" >&2
    exit 1
fi

export WAYLAND_DISPLAY="${WAYLAND_DISPLAY:-wayland-0}"
export XDG_SESSION_TYPE="wayland"
export XDG_CURRENT_DESKTOP="Wayland"
export LIBSEAT_BACKEND="${LIBSEAT_BACKEND:-seatd}"
export XCURSOR_THEME="${XCURSOR_THEME:-transparent}"
export XCURSOR_SIZE="${XCURSOR_SIZE:-24}"

# Wlroots compositor stability flags (prevents Intel KMS deadlocks and direct scanout fence collisions)
export WLR_SCENE_DISABLE_DIRECT_SCANOUT="${WLR_SCENE_DISABLE_DIRECT_SCANOUT:-1}"
export WLR_DRM_NO_ATOMIC="${WLR_DRM_NO_ATOMIC:-1}"

if [[ -n "${WLR_DRM_DEVICES:-}" ]]; then
    export WLR_DRM_DEVICES
fi
if [[ -n "${WLR_LIBINPUT_NO_DEVICES:-}" ]]; then
    export WLR_LIBINPUT_NO_DEVICES
fi

# Resolve session runner path
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SESSION_SCRIPT="${SCRIPT_DIR}/session.sh"

if [[ ! -x "$SESSION_SCRIPT" ]]; then
    echo "[Mirrormere Display] Session runner not found or not executable: $SESSION_SCRIPT" >&2
    exit 1
fi

echo "[Mirrormere Display] Launching Cage compositor on seat0..."
exec cage -d -s -- "$SESSION_SCRIPT"

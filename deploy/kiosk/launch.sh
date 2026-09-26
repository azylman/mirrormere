#!/usr/bin/env bash
# Mirrormere Touch Kiosk Stage 1 Launcher (Host -> Cage Compositor)
# Runs as unprivileged 'kiosk' user under systemd mirrormere-kiosk.service.
set -euo pipefail

# Source configuration overrides if present
if [[ -f /etc/default/mirrormere-kiosk ]]; then
    # shellcheck source=/dev/null
    source /etc/default/mirrormere-kiosk
fi

# Ensure user runtime directory exists
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
if [[ ! -d "$XDG_RUNTIME_DIR" ]]; then
    echo "[Mirrormere Kiosk] XDG_RUNTIME_DIR ($XDG_RUNTIME_DIR) does not exist" >&2
    exit 1
fi

export WAYLAND_DISPLAY="${WAYLAND_DISPLAY:-wayland-0}"
export XDG_SESSION_TYPE="wayland"
export XDG_CURRENT_DESKTOP="cage"

# Resolve session runner path
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SESSION_SCRIPT="${SCRIPT_DIR}/session.sh"

if [[ ! -x "$SESSION_SCRIPT" ]]; then
    echo "[Mirrormere Kiosk] Session runner not found or not executable: $SESSION_SCRIPT" >&2
    exit 1
fi

echo "[Mirrormere Kiosk] Launching Cage compositor on seat0..."
exec cage -s -- "$SESSION_SCRIPT"

#!/usr/bin/env bash
# Mirrormere Display Container Entrypoint (Alpine 3.24 Wayland Kiosk)
set -euo pipefail

# Ensure user/group kiosk (10001:10001) and /usr/bin/Xwayland stub exist
getent group kiosk >/dev/null 2>&1 || groupadd -g 10001 kiosk 2>/dev/null || addgroup -g 10001 kiosk 2>/dev/null || true
id -u kiosk >/dev/null 2>&1 || useradd -u 10001 -g 10001 -m -d /home/kiosk -s /bin/bash -G video,input,audio,render,seat kiosk 2>/dev/null || adduser -u 10001 -G kiosk -h /home/kiosk -s /bin/bash -D kiosk 2>/dev/null || true

if [[ ! -x /usr/bin/Xwayland ]]; then
    printf '#!/bin/sh\nexit 0\n' > /usr/bin/Xwayland
    chmod 0755 /usr/bin/Xwayland
fi

# Set character device permissions without touching /dev/snd/by-* directories
chmod 0666 /dev/tty0 /dev/tty1 /dev/dri/card* /dev/dri/renderD* /dev/input/event* /dev/snd/pcm* /dev/snd/control* /dev/snd/timer /dev/snd/seq 2>/dev/null || true

# Run pre-init drop-in hooks (root stage)
for hook in /etc/kiosk/init.d/*.sh; do
    if [[ -f "$hook" ]]; then
        echo "[Mirrormere Display] Executing pre-init hook: $hook"
        KIOSK_INIT_STAGE=pre bash "$hook" || true
    fi
done

# Prepare runtime and chrome profile directories, and clean stale sockets
CHROME_PROFILE_DIR="${MIRRORMERE_CHROME_USER_DATA_DIR:-/data/chrome-profile}"
mkdir -p /run/kiosk /run/kiosk/pulse "${CHROME_PROFILE_DIR}/Default" /home/kiosk
rm -f /run/seatd.sock /run/kiosk/wayland-0* /run/kiosk/pipewire-0* /run/kiosk/pulse/native /run/kiosk/pulse/pid /run/kiosk/bus 2>/dev/null || true
chown -R 10001:10001 /run/kiosk "${CHROME_PROFILE_DIR}" /home/kiosk
chmod 0755 /run/kiosk /run/kiosk/pulse

# Auto-resolve WLR_DRM_DEVICES if unset or missing
if [[ -z "${WLR_DRM_DEVICES:-}" || ! -e "$WLR_DRM_DEVICES" ]]; then
    if [[ -e /dev/dri/by-path/pci-0000:00:02.0-card ]]; then
        export WLR_DRM_DEVICES="$(readlink -f /dev/dri/by-path/pci-0000:00:02.0-card 2>/dev/null || echo /dev/dri/by-path/pci-0000:00:02.0-card)"
    elif [[ -e /dev/dri/card1 ]]; then
        export WLR_DRM_DEVICES=/dev/dri/card1
    elif [[ -e /dev/dri/card0 ]]; then
        export WLR_DRM_DEVICES=/dev/dri/card0
    else
        export WLR_DRM_DEVICES=""
    fi
fi

# Start seatd daemon and wait up to 3s for socket
seatd -u kiosk -g kiosk >/tmp/seatd.log 2>&1 &
for _ in $(seq 1 30); do
    if [[ -S /run/seatd.sock ]]; then
        chmod 0666 /run/seatd.sock 2>/dev/null || true
        break
    fi
    sleep 0.1
done

# Drop privileges to kiosk user and launch session
exec runuser -u kiosk -- env \
    HOME=/home/kiosk \
    USER=kiosk \
    LOGNAME=kiosk \
    XDG_RUNTIME_DIR=/run/kiosk \
    LIBSEAT_BACKEND=seatd \
    WLR_DRM_DEVICES="${WLR_DRM_DEVICES:-}" \
    WLR_SCENE_DISABLE_DIRECT_SCANOUT="${WLR_SCENE_DISABLE_DIRECT_SCANOUT:-1}" \
    WLR_DRM_NO_ATOMIC="${WLR_DRM_NO_ATOMIC:-1}" \
    MIRRORMERE_DISPLAY_URL="${MIRRORMERE_DISPLAY_URL:-http://localhost:8080/display}" \
    MIRRORMERE_NODE_ID="${MIRRORMERE_NODE_ID:-}" \
    MIRRORMERE_SCALE_FACTOR="${MIRRORMERE_SCALE_FACTOR:-1.0}" \
    MIRRORMERE_CHROME_USER_DATA_DIR="${MIRRORMERE_CHROME_USER_DATA_DIR:-/data/chrome-profile}" \
    MIRRORMERE_REMOTE_DEBUGGING_PORT="${MIRRORMERE_REMOTE_DEBUGGING_PORT:-9222}" \
    KIOSK_AUDIO_SINK="${KIOSK_AUDIO_SINK:-}" \
    KIOSK_AUDIO_SOURCE="${KIOSK_AUDIO_SOURCE:-}" \
    PULSE_SERVER="${PULSE_SERVER:-unix:/run/kiosk/pulse/native}" \
    bash -c '
set -euo pipefail

dbus-daemon --session --address=unix:path=/run/kiosk/bus --nofork >/tmp/dbus.log 2>&1 &
export DBUS_SESSION_BUS_ADDRESS=unix:path=/run/kiosk/bus
sleep 0.3

pipewire >/tmp/pipewire.log 2>&1 &
sleep 0.3
wireplumber >/tmp/wireplumber.log 2>&1 &
pipewire-pulse >/tmp/pipewire-pulse.log 2>&1 &

for _ in $(seq 1 30); do
    if [[ -S /run/kiosk/pulse/native ]]; then
        chmod 0666 /run/kiosk/pulse/native 2>/dev/null || true
        break
    fi
    sleep 0.1
done

(
    sleep 2
    for hook in /etc/kiosk/init.d/*.sh; do
        if [[ -f "$hook" ]]; then
            echo "[Mirrormere Display] Executing audio hook: $hook"
            KIOSK_INIT_STAGE=audio bash "$hook" || true
        fi
    done
    if [[ -n "${KIOSK_AUDIO_SINK:-}" ]]; then
        pactl set-default-sink "$KIOSK_AUDIO_SINK" 2>/dev/null || true
    fi
    if [[ -n "${KIOSK_AUDIO_SOURCE:-}" ]]; then
        pactl set-default-source "$KIOSK_AUDIO_SOURCE" 2>/dev/null || true
    fi
) &

unset WAYLAND_DISPLAY
export XDG_SESSION_TYPE=wayland
export XDG_CURRENT_DESKTOP=Wayland

exec cage -d -s -- /opt/mirrormere/display/session.sh
'

#!/usr/bin/env bash
# Mirrormere Touch Kiosk Host Provisioning Script (Debian 12 / Ubuntu 24.04 Server)
# Bootstraps Wayland Cage confinement, Chromium kiosk, and systemd power management.
set -euo pipefail

if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
    echo "[Mirrormere Install] Error: This script must be run as root (or via sudo)" >&2
    exit 1
fi

export DEBIAN_FRONTEND=noninteractive

echo "[Mirrormere Install] Updating package index..."
apt-get update -y

echo "[Mirrormere Install] Installing Wayland, graphics, audio, and power dependencies..."
apt-get install -y \
    cage \
    chromium \
    swayidle \
    wlr-randr \
    pipewire \
    wireplumber \
    pipewire-alsa \
    libinput-bin \
    mesa-va-drivers \
    intel-media-va-driver

echo "[Mirrormere Install] Configuring unprivileged kiosk user and hardware access groups..."
getent group render >/dev/null 2>&1 || groupadd -r render
getent group video >/dev/null 2>&1 || groupadd -r video
getent group input >/dev/null 2>&1 || groupadd -r input
getent group audio >/dev/null 2>&1 || groupadd -r audio

if ! id -u kiosk >/dev/null 2>&1; then
    useradd -m -s /bin/bash -G video,input,audio,render kiosk
    echo "[Mirrormere Install] Created system user: kiosk"
else
    usermod -aG video,input,audio,render kiosk
    echo "[Mirrormere Install] User kiosk already exists, updated group memberships"
fi

# Enable systemd user lingering so /run/user/<uid> stays alive headless for PipeWire and Wayland
if command -v loginctl >/dev/null 2>&1; then
    loginctl enable-linger kiosk || true
    echo "[Mirrormere Install] Enabled systemd logind lingering for kiosk user"
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TARGET_DIR="/opt/mirrormere/kiosk"

echo "[Mirrormere Install] Installing kiosk scripts to ${TARGET_DIR}..."
mkdir -p "${TARGET_DIR}"
install -m 755 "${SCRIPT_DIR}/launch.sh" "${TARGET_DIR}/launch.sh"
install -m 755 "${SCRIPT_DIR}/session.sh" "${TARGET_DIR}/session.sh"
install -m 755 "${SCRIPT_DIR}/dpms.sh" "${TARGET_DIR}/dpms.sh"

echo "[Mirrormere Install] Installing environment defaults..."
if [[ ! -f /etc/default/mirrormere-kiosk ]]; then
    install -m 644 "${SCRIPT_DIR}/default-mirrormere-kiosk" /etc/default/mirrormere-kiosk
fi

echo "[Mirrormere Install] Installing systemd services and timers..."
install -m 644 "${SCRIPT_DIR}/mirrormere-kiosk.service" /etc/systemd/system/
install -m 644 "${SCRIPT_DIR}/mirrormere-kiosk-sleep.service" /etc/systemd/system/
install -m 644 "${SCRIPT_DIR}/mirrormere-kiosk-sleep.timer" /etc/systemd/system/
install -m 644 "${SCRIPT_DIR}/mirrormere-kiosk-wake.service" /etc/systemd/system/
install -m 644 "${SCRIPT_DIR}/mirrormere-kiosk-wake.timer" /etc/systemd/system/
install -m 644 "${SCRIPT_DIR}/mirrormere-kiosk-restart.service" /etc/systemd/system/
install -m 644 "${SCRIPT_DIR}/mirrormere-kiosk-restart.timer" /etc/systemd/system/

if command -v systemctl >/dev/null 2>&1; then
    echo "[Mirrormere Install] Reloading systemd daemon and enabling units..."
    systemctl daemon-reload
    systemctl enable \
        mirrormere-kiosk.service \
        mirrormere-kiosk-sleep.timer \
        mirrormere-kiosk-wake.timer \
        mirrormere-kiosk-restart.timer
fi

echo "[Mirrormere Install] Kiosk host provisioning completed successfully!"

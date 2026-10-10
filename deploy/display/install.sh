#!/usr/bin/env bash
# Mirrormere Touch Display Host Provisioning Script (Debian 12 / Ubuntu 24.04 Server)
# Bootstraps Wayland Cage confinement, native Google Chrome, seatd, and systemd management.
set -euo pipefail

if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
    echo "[Mirrormere Install] Error: This script must be run as root (or via sudo)" >&2
    exit 1
fi

export DEBIAN_FRONTEND=noninteractive

# Purge legacy Snap Chromium to prevent AppArmor/sandbox hangs and loop mount leaks
if command -v snap >/dev/null 2>&1; then
    echo "[Mirrormere Install] Purging legacy snap chromium if present..."
    snap remove chromium 2>/dev/null || true
fi

# Configure official Google Chrome APT repository
echo "[Mirrormere Install] Bootstrapping repository prerequisites (curl, gnupg, ca-certificates)..."
apt-get update -y
apt-get install -y curl gnupg ca-certificates

echo "[Mirrormere Install] Configuring Google Chrome official APT repository..."
install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://dl.google.com/linux/linux_signing_key.pub | gpg --dearmor --yes -o /etc/apt/keyrings/google-chrome.gpg
cat <<'EOF' > /etc/apt/sources.list.d/google-chrome.list
deb [arch=amd64 signed-by=/etc/apt/keyrings/google-chrome.gpg] https://dl.google.com/linux/chrome-stable/deb/ stable main
EOF

echo "[Mirrormere Install] Updating package index..."
apt-get update -y

echo "[Mirrormere Install] Installing Wayland, Chrome, graphics, audio, and seat dependencies..."
apt-get install -y \
    cage \
    google-chrome-stable \
    swayidle \
    wlr-randr \
    pipewire \
    wireplumber \
    pipewire-alsa \
    libinput-bin \
    mesa-va-drivers \
    intel-media-va-driver \
    seatd \
    kanshi \
    ddcutil

echo "[Mirrormere Install] Configuring system hardware access groups..."
for grp in render video input audio seat; do
    getent group "$grp" >/dev/null 2>&1 || groupadd -r "$grp"
done

# Read optional display user override if environment defaults already exist
DISPLAY_USER="${MIRRORMERE_DISPLAY_USER:-${MIRRORMERE_KIOSK_USER:-}}"
if [[ -z "$DISPLAY_USER" ]]; then
    if [[ -f /etc/default/mirrormere-display ]]; then
        DISPLAY_USER="$(sed -n -E 's/^[[:space:]]*MIRRORMERE_DISPLAY_USER=[[:space:]]*["'"'"']?([A-Za-z0-9._-]+)["'"'"']?[[:space:]]*$/\1/p' /etc/default/mirrormere-display | head -n 1 || true)"
    elif [[ -f /etc/default/mirrormere-kiosk ]]; then
        DISPLAY_USER="$(sed -n -E 's/^[[:space:]]*MIRRORMERE_KIOSK_USER=[[:space:]]*["'"'"']?([A-Za-z0-9._-]+)["'"'"']?[[:space:]]*$/\1/p' /etc/default/mirrormere-kiosk | head -n 1 || true)"
    fi
fi
if [[ -z "$DISPLAY_USER" ]]; then
    if id -u kiosk >/dev/null 2>&1; then
        DISPLAY_USER="kiosk"
    else
        DISPLAY_USER="display"
    fi
fi

if ! id -u "$DISPLAY_USER" >/dev/null 2>&1; then
    useradd -m -s /bin/bash -G video,input,audio,render,seat "$DISPLAY_USER"
    echo "[Mirrormere Install] Created system user: $DISPLAY_USER"
else
    usermod -aG video,input,audio,render,seat "$DISPLAY_USER"
    echo "[Mirrormere Install] User $DISPLAY_USER already exists, updated group memberships"
fi

# Enable systemd user lingering so /run/user/<uid> stays active headless for PipeWire and Wayland
if command -v loginctl >/dev/null 2>&1; then
    loginctl enable-linger "$DISPLAY_USER" || true
    echo "[Mirrormere Install] Enabled systemd logind lingering for $DISPLAY_USER"
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TARGET_DIR="/opt/mirrormere/display"

echo "[Mirrormere Install] Installing display scripts to ${TARGET_DIR}..."
mkdir -p "${TARGET_DIR}"
install -m 755 "${SCRIPT_DIR}/launch.sh" "${TARGET_DIR}/launch.sh"
install -m 755 "${SCRIPT_DIR}/session.sh" "${TARGET_DIR}/session.sh"
install -m 755 "${SCRIPT_DIR}/dpms.sh" "${TARGET_DIR}/dpms.sh"

echo "[Mirrormere Install] Creating convenience host symlinks in /usr/local/bin..."
ln -sf "${TARGET_DIR}/launch.sh" /usr/local/bin/mirrormere-display-run.sh
ln -sf "${TARGET_DIR}/launch.sh" /usr/local/bin/mirrormere-kiosk-run.sh
ln -sf "${TARGET_DIR}/session.sh" /usr/local/bin/mirrormere-browser.sh
ln -sf "${TARGET_DIR}/dpms.sh" /usr/local/bin/mirrormere-dpms.sh

echo "[Mirrormere Install] Installing environment defaults..."
if [[ ! -f /etc/default/mirrormere-display ]]; then
    if [[ -f /etc/default/mirrormere-kiosk ]]; then
        echo "[Mirrormere Install] Migrating existing /etc/default/mirrormere-kiosk to /etc/default/mirrormere-display..."
        cp /etc/default/mirrormere-kiosk /etc/default/mirrormere-display
    else
        install -m 644 "${SCRIPT_DIR}/default-mirrormere-display" /etc/default/mirrormere-display
    fi
fi

echo "[Mirrormere Install] Installing systemd services and timers..."
install -m 644 "${SCRIPT_DIR}/mirrormere-display.service" /etc/systemd/system/
install -m 644 "${SCRIPT_DIR}/mirrormere-display-sleep.service" /etc/systemd/system/
install -m 644 "${SCRIPT_DIR}/mirrormere-display-sleep.timer" /etc/systemd/system/
install -m 644 "${SCRIPT_DIR}/mirrormere-display-wake.service" /etc/systemd/system/
install -m 644 "${SCRIPT_DIR}/mirrormere-display-wake.timer" /etc/systemd/system/
install -m 644 "${SCRIPT_DIR}/mirrormere-display-restart.service" /etc/systemd/system/
install -m 644 "${SCRIPT_DIR}/mirrormere-display-restart.timer" /etc/systemd/system/

# Adjust user/group in systemd unit if non-default user configured
if [[ "$DISPLAY_USER" != "display" ]]; then
    sed -i "s/^User=display/User=${DISPLAY_USER}/" /etc/systemd/system/mirrormere-display.service
    sed -i "s/^Group=display/Group=${DISPLAY_USER}/" /etc/systemd/system/mirrormere-display.service
fi

if command -v systemctl >/dev/null 2>&1; then
    echo "[Mirrormere Install] Reloading systemd daemon and enabling units..."
    # Disable legacy mirrormere-kiosk units if active
    if systemctl is-active --quiet mirrormere-kiosk.service 2>/dev/null; then
        systemctl stop mirrormere-kiosk.service 2>/dev/null || true
    fi
    systemctl disable mirrormere-kiosk.service mirrormere-kiosk-sleep.timer mirrormere-kiosk-wake.timer mirrormere-kiosk-restart.timer 2>/dev/null || true

    systemctl daemon-reload
    systemctl enable seatd.service 2>/dev/null || true
    systemctl enable \
        mirrormere-display.service \
        mirrormere-display-sleep.timer \
        mirrormere-display-wake.timer \
        mirrormere-display-restart.timer
fi

echo "[Mirrormere Install] Display host provisioning completed successfully!"

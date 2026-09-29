#!/usr/bin/env bash
set -euo pipefail

# setup.sh - Installer for Mirrormere Edge Voice Daemon on Linux Kiosk / Raspberry Pi
# Follows SPEC-011 for edge dock deployment.

INSTALL_DIR="/opt/mirrormere/voice"
CONFIG_PATH="/etc/mirrormere/voice.yaml"
SERVICE_NAME="mirrormere-voice.service"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "🎙️  Installing Mirrormere Voice Daemon to ${INSTALL_DIR}..."

sudo mkdir -p "${INSTALL_DIR}" "${INSTALL_DIR}/models" "/etc/mirrormere"
sudo cp "${SCRIPT_DIR}/client.py" "${INSTALL_DIR}/client.py"
sudo cp "${SCRIPT_DIR}/config.py" "${INSTALL_DIR}/config.py"
sudo cp "${SCRIPT_DIR}/requirements.txt" "${INSTALL_DIR}/requirements.txt"

if [ ! -f "${CONFIG_PATH}" ]; then
    echo "📄 Creating default config at ${CONFIG_PATH}..."
    sudo cp "${SCRIPT_DIR}/config.example.yaml" "${CONFIG_PATH}"
fi

# Ensure python3-venv and ALSA capture tools are present
if command -v apt-get >/dev/null 2>&1; then
    sudo apt-get update -qq && sudo apt-get install -y -qq python3-venv python3-pip alsa-utils
fi

# Set up Python virtual environment
if [ ! -d "${INSTALL_DIR}/venv" ]; then
    echo "🐍 Creating virtual environment at ${INSTALL_DIR}/venv..."
    sudo python3 -m venv "${INSTALL_DIR}/venv"
fi

echo "📦 Installing Python dependencies..."
sudo "${INSTALL_DIR}/venv/bin/pip" install --upgrade pip -q
sudo "${INSTALL_DIR}/venv/bin/pip" install -r "${INSTALL_DIR}/requirements.txt" -q

# Install systemd service
echo "⚙️  Configuring systemd service..."
sudo cp "${SCRIPT_DIR}/mirrormere-voice.service" "/etc/systemd/system/${SERVICE_NAME}"
sudo systemctl daemon-reload
sudo systemctl enable "${SERVICE_NAME}"

echo "✨ Mirrormere Voice Daemon installed successfully!"
echo "   Start with: sudo systemctl start ${SERVICE_NAME}"
echo "   View logs:  journalctl -u ${SERVICE_NAME} -f"

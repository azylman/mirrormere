# syntax=docker/dockerfile:1
# sidecars/remote/Dockerfile - Unified Bluetooth HID & Android TV Remote Sidecar

FROM python:3.11-slim

# Install system dependencies required for BlueZ D-Bus and C extension building
RUN apt-get update && apt-get install -y --no-install-recommends \
        bluez \
        libglib2.0-0 \
        ca-certificates \
        gcc \
        python3-dev \
        linux-libc-dev \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Install Python dependencies
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt \
    && apt-get purge -y --auto-remove gcc python3-dev \
    && rm -rf /var/lib/apt/lists/*

# Copy application files
COPY . /app/

ENV HTTP_PORT=8092 \
    CHROMECAST_HOST="" \
    CERT_DIR=/data/certs \
    TOUCH_DEVICE=/dev/input/event3 \
    ADVERT_NAME="Cockpit Touch" \
    AUTO_CONFIRM_PAIRING=true


EXPOSE 8092

# Python standard library probe ensures zero external curl/wget dependencies for healthchecks
HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --start-interval=2s --retries=3 \
  CMD python3 -c "import urllib.request; urllib.request.urlopen('http://127.0.0.1:8092/healthz', timeout=2)" || exit 1

ENTRYPOINT ["python3", "daemon.py"]

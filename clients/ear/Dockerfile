# syntax=docker/dockerfile:1
FROM python:3.11-slim AS builder

WORKDIR /build

RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc \
    python3-dev \
    && rm -rf /var/lib/apt/lists/*

RUN python -m venv /opt/venv
ENV PATH="/opt/venv/bin:$PATH"

COPY requirements.txt ./
RUN --mount=type=cache,target=/root/.cache/pip \
    pip install --upgrade pip && \
    pip install --no-cache-dir -r requirements.txt

FROM python:3.11-slim

WORKDIR /app

RUN apt-get update && apt-get install -y --no-install-recommends \
    alsa-utils \
    libasound2 \
    libgomp1 \
    && rm -rf /var/lib/apt/lists/*

RUN groupadd -g 10001 mirrormere && \
    useradd -u 10001 -g mirrormere -G audio -m -s /bin/bash mirrormere

COPY --from=builder /opt/venv /opt/venv

ENV PATH="/opt/venv/bin:$PATH" \
    PYTHONUNBUFFERED=1 \
    MIRRORMERE_VOICE_CONFIG="/config/voice.yaml" \
    MIRRORMERE_HEARTBEAT_FILE="/tmp/ear.heartbeat"

COPY --link client.py config.py vad.py ./

RUN python -m compileall -b -q /app

USER mirrormere:mirrormere

HEALTHCHECK --interval=15s --timeout=3s --start-period=30s --start-interval=2s --retries=3 \
    CMD python3 -c "import time, os, sys; p = os.environ.get('MIRRORMERE_HEARTBEAT_FILE', '/tmp/ear.heartbeat'); sys.exit(0 if os.path.exists(p) and (time.time() - os.path.getmtime(p) < 30) else 1)"

ENTRYPOINT ["python", "client.py"]

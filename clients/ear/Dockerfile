# syntax=docker/dockerfile:1
FROM python:3.11-slim AS builder

WORKDIR /build

RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc \
    python3-dev \
    curl \
    ca-certificates \
    bzip2 \
    && rm -rf /var/lib/apt/lists/*

RUN python -m venv /opt/venv
ENV PATH="/opt/venv/bin:$PATH"

COPY requirements.txt ./
RUN --mount=type=cache,target=/root/.cache/pip \
    pip install --upgrade pip && \
    pip install --no-cache-dir -r requirements.txt

# Bundle pre-trained Sherpa-ONNX Zipformer KWS INT8 bundle and Silero VAD
RUN mkdir -p /opt/mirrormere/voice/sherpa && \
    curl -SL -o /tmp/sherpa-kws.tar.bz2 \
      https://github.com/k2-fsa/sherpa-onnx/releases/download/kws-models/sherpa-onnx-kws-zipformer-gigaspeech-3.3M-2024-01-01.tar.bz2 && \
    tar -xjf /tmp/sherpa-kws.tar.bz2 -C /tmp && \
    cp /tmp/sherpa-kws-zipformer-gigaspeech-3.3M-2024-01-01/tokens.txt /opt/mirrormere/voice/sherpa/ && \
    cp /tmp/sherpa-kws-zipformer-gigaspeech-3.3M-2024-01-01/encoder.int8.onnx /opt/mirrormere/voice/sherpa/ && \
    cp /tmp/sherpa-kws-zipformer-gigaspeech-3.3M-2024-01-01/decoder.int8.onnx /opt/mirrormere/voice/sherpa/ && \
    cp /tmp/sherpa-kws-zipformer-gigaspeech-3.3M-2024-01-01/joiner.int8.onnx /opt/mirrormere/voice/sherpa/ && \
    rm -rf /tmp/sherpa-kws* && \
    curl -SL -o /opt/mirrormere/voice/sherpa/silero_vad.onnx \
      https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/silero_vad.onnx

FROM python:3.11-slim

WORKDIR /app

RUN apt-get update && apt-get install -y --no-install-recommends \
    alsa-utils \
    libasound2 \
    libasound2-plugins \
    libgomp1 \
    && rm -rf /var/lib/apt/lists/*

RUN echo 'pcm.!default { type pulse }\nctl.!default { type pulse }' > /etc/asound.conf

RUN groupadd -g 10001 mirrormere && \
    useradd -u 10001 -g mirrormere -G audio -m -s /bin/bash mirrormere

COPY --from=builder /opt/venv /opt/venv
COPY --from=builder --chown=mirrormere:mirrormere /opt/mirrormere/voice/sherpa /opt/mirrormere/voice/sherpa


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

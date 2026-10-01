# syntax=docker/dockerfile:1
# sidecars/voice-fingerprinter/Dockerfile - CPU Speaker-Embedding Sidecar
# Reference: SPEC-011 Speaker Identification / Embedding endpoint
#
# CPU-only build: torch/torchaudio come from the PyTorch CPU wheel index,
# never the default PyPI index (which resolves a multi-GB CUDA build).
# The ECAPA model (~80MB, speechbrain/spkrec-ecapa-voxceleb) is NOT baked
# into the image — it is downloaded on first run into /models, which MUST
# be a persistent volume (see deploy/compose.yml's voice-fingerprinter
# service) or every container restart re-downloads it.

FROM python:3.11-slim AS base

RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt,sharing=locked \
    apt-get update && apt-get install -y --no-install-recommends \
        libsndfile1 \
        ca-certificates \
        wget \
    && groupadd -g 10001 appgroup \
    && useradd -u 10001 -g appgroup -M -s /usr/sbin/nologin appuser

WORKDIR /app

RUN --mount=type=bind,source=requirements.txt,target=requirements.txt \
    --mount=type=cache,target=/root/.cache/pip \
    pip install \
        --extra-index-url https://download.pytorch.org/whl/cpu \
        -r requirements.txt \
    && rm -rf \
        /usr/local/lib/python3.11/site-packages/torch/test \
        /usr/local/lib/python3.11/site-packages/torch/include \
        /usr/local/lib/python3.11/site-packages/pip \
    && find /usr/local/lib/python3.11/site-packages/torch/bin -type f ! -name torch_shm_manager -delete \
    && python -m compileall -b -q /usr/local/lib/python3.11/site-packages

COPY --link --chown=10001:10001 app.py enroll.py ./

RUN python -m compileall -b -q /app && mkdir -p /models && chown -R 10001:10001 /app /models

USER 10001:10001

ENV MDE_PORT=9096 \
    MDE_BIND=0.0.0.0 \
    MDE_DEVICE=cpu \
    MDE_SAVEDIR=/models/speechbrain-ecapa \
    HF_HOME=/models/hf-cache

EXPOSE 9096

VOLUME ["/models"]

HEALTHCHECK --interval=15s --timeout=3s --start-period=30s --start-interval=2s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://127.0.0.1:9096/health || exit 1

ENTRYPOINT ["python", "app.py"]

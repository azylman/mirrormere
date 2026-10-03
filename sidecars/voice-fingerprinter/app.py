#!/usr/bin/env python3
"""voice-fingerprinter — CPU speaker-embedding sidecar for Mirrormere's Voice
Hub speaker-ID feature (SPEC-011 §Speaker Identification, §Embedding
endpoint).

Ported from the karakos household's desktop_embed.py (GPU, desktop-only)
to run as a small standalone container on Alex's Intel N150 edge box, CPU
only. The contract is unchanged: `internal/voice/speaker.go`'s
DefaultEmbedClient POSTs raw WAV bytes to `embed_url` and expects
`{"embedding": [...floats], "model": "..."}` back; any model that produces
stable, comparable vectors satisfies it.

Endpoints:
    GET  /health  -> {"status": "ok"|"error", "model": ..., "device": ...,
                       "loaded": bool, "load_error": str|None,
                       "load_ms": float|None, "warmup_ms": float|None}
    POST /embed   raw WAV body (any sample rate/channel count — downmixed
                  and resampled to 16kHz mono defensively) ->
                  {"embedding": [192 floats], "model": "speechbrain/spkrec-ecapa-voxceleb"}
                  400 on an empty, undecodable, or too-short (<0.5s) body.

The model (`speechbrain/spkrec-ecapa-voxceleb`) is loaded once at process
start, synchronously, before the server starts accepting connections, and a
single warmup encode runs right after load so the first real request
doesn't pay for lazy backend/kernel warmup. There is no fallback engine, so
nothing is gained by loading it in the background.

Inference is serialized with a lock (not documented as safe for concurrent
calls) and runs in Starlette's threadpool so the asyncio event loop is
never blocked while an embed is in flight.

Configuration (env vars, MDE_* prefix, matching the karakos original):
    MDE_PORT           bind port (default 9096)
    MDE_BIND           bind address (default 0.0.0.0 — container default;
                        the karakos original defaulted to 127.0.0.1, a
                        desktop-only posture that doesn't apply inside a
                        container)
    MDE_MODEL_SOURCE   HF model id (default speechbrain/spkrec-ecapa-voxceleb)
    MDE_DEVICE         torch device (default cpu — this build is CPU-only,
                        no CUDA in the image; a non-"cpu" value that isn't
                        available falls back to cpu, same as the original)
    MDE_SAVEDIR        model cache dir (default /models/speechbrain-ecapa —
                        a mountable path, never a home directory, so the
                        container can run as a non-root, homeless user and
                        so re-runs don't re-download the ~80MB model)
    MDE_THREADS        torch intra-op thread count (default: os.cpu_count())

Measured on an Intel N150-class CPU (4 threads): ~76/105/211 ms per
embedding for 3/5/10 second clips, ~830 MB RSS (mostly torch/speechbrain).
"""

from __future__ import annotations

import io
import os
import sys
import threading
import time
from pathlib import Path
from typing import Optional

from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse
from starlette.concurrency import run_in_threadpool
try:
    import yaml
except ImportError:
    yaml = None


def load_config_file(path: str | None = None) -> dict:
    candidates = []
    if path:
        candidates.append(path)
    if os.environ.get("CONFIG_PATH"):
        candidates.append(os.environ["CONFIG_PATH"])
    candidates.extend([
        "/config/config.yaml",
        "/config/config.yml",
        "/share/aerial-config/services/mirrormere/voice-fingerprinter.yaml",
        "/app/config.yaml",
    ])
    for c in candidates:
        if os.path.exists(c):
            if yaml is not None:
                with open(c, "r", encoding="utf-8") as f:
                    data = yaml.safe_load(f)
                    if isinstance(data, dict):
                        return data
            else:
                try:
                    res = {}
                    with open(c, "r", encoding="utf-8") as f:
                        for line in f:
                            line = line.strip()
                            if not line or line.startswith("#") or ":" not in line:
                                continue
                            k, v = line.split(":", 1)
                            res[k.strip()] = v.strip().strip("\"'")
                    return res
                except Exception:
                    pass
    return {}


_file_cfg = load_config_file()
PORT = int(os.environ["MDE_PORT"]) if "MDE_PORT" in os.environ else (int(_file_cfg["port"]) if "port" in _file_cfg and _file_cfg["port"] is not None else 9096)
BIND = os.environ.get("MDE_BIND") if "MDE_BIND" in os.environ else (_file_cfg.get("bind") or "0.0.0.0")

MODEL_SOURCE = os.environ.get("MDE_MODEL_SOURCE") or _file_cfg.get("model_source") or "speechbrain/spkrec-ecapa-voxceleb"
MODEL_ID = MODEL_SOURCE  # the model string the hub's speakers.json must match exactly
DEVICE = os.environ.get("MDE_DEVICE") or _file_cfg.get("device") or "cpu"
SAVEDIR = Path(os.environ.get("MDE_SAVEDIR") or _file_cfg.get("savedir") or "/models/speechbrain-ecapa")
THREADS = int(os.environ.get("MDE_THREADS") or _file_cfg.get("threads") or str(os.cpu_count() or 1))
TARGET_SAMPLE_RATE = 16000
MIN_DURATION_SEC = 0.5
WARMUP_SEC = 1.0  # 1s of near-silence, just enough to exercise the full path

_model = None
_model_lock = threading.Lock()
_load_error: Optional[str] = None
_load_ms: Optional[float] = None
_warmup_ms: Optional[float] = None
_active_device: Optional[str] = None  # the device _load_model() actually used


def _load_model() -> None:
    """Loads the ECAPA encoder once and runs a warmup encode. Sets
    _load_error instead of raising, so a bad image or missing model
    produces a 503 from /health rather than crashing the whole process."""
    global _model, _load_error, _load_ms, _warmup_ms, _active_device
    try:
        import torch  # noqa: E402 (deliberately lazy import)
        from speechbrain.inference.speaker import EncoderClassifier  # noqa: E402
    except Exception as exc:  # pragma: no cover - only hit with a broken image
        _load_error = f"speechbrain/torch not importable: {exc}"
        print(f"[voice-fingerprinter] {_load_error}", file=sys.stderr, flush=True)
        return

    torch.set_num_threads(max(1, THREADS))

    device = DEVICE if (not DEVICE.startswith("cuda") or torch.cuda.is_available()) else "cpu"
    if device != DEVICE:
        print("[voice-fingerprinter] CUDA not available, falling back to cpu", flush=True)
    _active_device = device

    try:
        t0 = time.time()
        SAVEDIR.mkdir(parents=True, exist_ok=True)
        model = EncoderClassifier.from_hparams(
            source=MODEL_SOURCE,
            savedir=str(SAVEDIR),
            run_opts={"device": device},
        )
        _load_ms = (time.time() - t0) * 1000
        print(f"[voice-fingerprinter] loaded {MODEL_SOURCE} on {device} in {_load_ms:.0f}ms "
              f"(threads={THREADS})", flush=True)

        t0 = time.time()
        warmup_samples = torch.zeros((1, int(TARGET_SAMPLE_RATE * WARMUP_SEC)))
        with _model_lock:
            model.encode_batch(warmup_samples)
        _warmup_ms = (time.time() - t0) * 1000
        print(f"[voice-fingerprinter] warmup encode in {_warmup_ms:.0f}ms", flush=True)
        _model = model
    except Exception as exc:
        _load_error = f"model load/warmup failed: {exc}"
        print(f"[voice-fingerprinter] {_load_error}", file=sys.stderr, flush=True)


def _decode_and_resample(body: bytes):
    """Returns a 1-D float32 numpy array at TARGET_SAMPLE_RATE, mono.
    Raises ValueError on anything not decodable as audio."""
    import numpy as np
    import soundfile as sf
    import torch
    import torchaudio

    try:
        data, sample_rate = sf.read(io.BytesIO(body), dtype="float32", always_2d=True)
    except Exception as exc:
        raise ValueError(f"could not decode audio: {exc}") from exc

    if data.size == 0:
        raise ValueError("empty audio")

    mono = data.mean(axis=1)  # downmix any channel count to 1

    duration = len(mono) / float(sample_rate)
    if duration < MIN_DURATION_SEC:
        raise ValueError(f"audio too short: {duration:.2f}s < {MIN_DURATION_SEC}s")

    if sample_rate != TARGET_SAMPLE_RATE:
        tensor = torch.from_numpy(mono).unsqueeze(0)  # (1, N)
        tensor = torchaudio.functional.resample(tensor, sample_rate, TARGET_SAMPLE_RATE)
        mono = tensor.squeeze(0).numpy()

    return mono.astype(np.float32)


def _embed_sync(body: bytes) -> dict:
    """Runs entirely off the event loop (called via run_in_threadpool)."""
    import torch

    if _model is None:
        raise RuntimeError(_load_error or "model not loaded")

    samples = _decode_and_resample(body)
    tensor = torch.from_numpy(samples).unsqueeze(0)  # (1, N)

    t0 = time.time()
    with _model_lock:
        emb = _model.encode_batch(tensor)
    ms = (time.time() - t0) * 1000

    vector = emb.squeeze(0).squeeze(0).detach().cpu().tolist()
    print(f"[voice-fingerprinter] /embed ok in {ms:.0f}ms, {len(vector)} dims", flush=True)
    return {"embedding": vector, "model": MODEL_ID}


app = FastAPI()


@app.get("/health")
async def health():
    ok = _model is not None
    return JSONResponse(
        {
            "status": "ok" if ok else "error",
            "model": MODEL_ID,
            "device": _active_device or DEVICE,
            "loaded": ok,
            "load_error": _load_error,
            "load_ms": _load_ms,
            "warmup_ms": _warmup_ms,
        },
        status_code=200 if ok else 503,
    )


@app.post("/embed")
async def embed(request: Request):
    body = await request.body()
    if not body:
        return JSONResponse({"error": "empty body"}, status_code=400)
    if _model is None:
        return JSONResponse({"error": _load_error or "model not loaded"}, status_code=503)
    try:
        result = await run_in_threadpool(_embed_sync, body)
    except ValueError as exc:
        return JSONResponse({"error": str(exc)}, status_code=400)
    except Exception as exc:
        return JSONResponse({"error": f"embed failed: {exc}"}, status_code=500)
    return JSONResponse(result, status_code=200)


def main():
    import uvicorn

    _load_model()
    print(f"[voice-fingerprinter] listening on {BIND}:{PORT}", flush=True)
    uvicorn.run(app, host=BIND, port=PORT, workers=1, log_level="warning")


if __name__ == "__main__":
    main()

"""Unit tests for the voice-fingerprinter sidecar's FastAPI app (app.py).

The model is fully mocked via conftest.py's fake torch/torchaudio/
speechbrain modules — no network access, no model download, no real
torch/speechbrain import. Audio fixtures are synthesized in-process with
the stdlib `wave` module; nothing is ever played.
"""
from __future__ import annotations

import importlib
import io
import math
import struct
import sys
import wave
from pathlib import Path

import pytest
from fastapi.testclient import TestClient

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))


def _make_wav_bytes(seconds: float, sample_rate: int = 16000, channels: int = 1,
                     freq: float = 220.0) -> bytes:
    """A synthesized sine-wave WAV — never audio played or recorded."""
    n_frames = int(seconds * sample_rate)
    buf = io.BytesIO()
    with wave.open(buf, "wb") as w:
        w.setnchannels(channels)
        w.setsampwidth(2)
        w.setframerate(sample_rate)
        frames = bytearray()
        for i in range(n_frames):
            sample = int(3000 * math.sin(2 * math.pi * freq * i / sample_rate))
            for _ in range(channels):
                frames += struct.pack("<h", sample)
        w.writeframes(bytes(frames))
    return buf.getvalue()


@pytest.fixture()
def app_module(monkeypatch):
    """Fresh import of app.py per test, with the model force-loaded via
    the fake speechbrain module (no network, no download)."""
    monkeypatch.setenv("MDE_SAVEDIR", "/tmp/voice-fingerprinter-test-savedir")
    sys.modules.pop("app", None)
    mod = importlib.import_module("app")
    mod._load_model()
    assert mod._load_error is None, f"fake model load failed: {mod._load_error}"
    assert mod._model is not None
    yield mod
    sys.modules.pop("app", None)


@pytest.fixture()
def client(app_module):
    return TestClient(app_module.app)


def test_health_ok(client):
    resp = client.get("/health")
    assert resp.status_code == 200
    body = resp.json()
    assert body["status"] == "ok"
    assert body["loaded"] is True
    assert body["model"] == "speechbrain/spkrec-ecapa-voxceleb"
    assert body["device"] == "cpu"


def test_health_error_when_model_not_loaded(app_module, client):
    app_module._model = None
    app_module._load_error = "boom"
    resp = client.get("/health")
    assert resp.status_code == 503
    assert resp.json()["status"] == "error"
    assert resp.json()["load_error"] == "boom"


def test_embed_happy_path_16k_mono(client):
    wav = _make_wav_bytes(3.0, sample_rate=16000, channels=1)
    resp = client.post("/embed", content=wav)
    assert resp.status_code == 200
    body = resp.json()
    assert body["model"] == "speechbrain/spkrec-ecapa-voxceleb"
    assert isinstance(body["embedding"], list)
    assert len(body["embedding"]) == 192
    assert all(isinstance(x, float) for x in body["embedding"])


def test_embed_empty_body_is_400(client):
    resp = client.post("/embed", content=b"")
    assert resp.status_code == 400
    assert "empty body" in resp.json()["error"]


def test_embed_garbage_bytes_is_400(client):
    resp = client.post("/embed", content=b"not a wav file at all")
    assert resp.status_code == 400
    assert "error" in resp.json()


def test_embed_too_short_is_400(client):
    wav = _make_wav_bytes(0.1, sample_rate=16000, channels=1)
    resp = client.post("/embed", content=wav)
    assert resp.status_code == 400
    assert "too short" in resp.json()["error"]


def test_embed_model_not_loaded_is_503(app_module, client):
    app_module._model = None
    app_module._load_error = "not loaded yet"
    wav = _make_wav_bytes(2.0)
    resp = client.post("/embed", content=wav)
    assert resp.status_code == 503


def test_embed_downmixes_stereo(client):
    wav = _make_wav_bytes(2.0, sample_rate=16000, channels=2)
    resp = client.post("/embed", content=wav)
    assert resp.status_code == 200
    assert len(resp.json()["embedding"]) == 192


def test_embed_resamples_non_16k(app_module, client):
    # 44.1kHz input must be resampled to 16kHz before hitting the encoder —
    # verify indirectly by checking decode+resample produces a plausible
    # 16kHz-length array and the endpoint still succeeds.
    wav = _make_wav_bytes(2.0, sample_rate=44100, channels=1)
    samples = app_module._decode_and_resample(wav)
    assert 16000 * 2 * 0.95 <= len(samples) <= 16000 * 2 * 1.05

    resp = client.post("/embed", content=wav)
    assert resp.status_code == 200
    assert len(resp.json()["embedding"]) == 192


def test_embed_48k_stereo_downmix_and_resample(app_module):
    wav = _make_wav_bytes(1.5, sample_rate=48000, channels=2)
    samples = app_module._decode_and_resample(wav)
    expected_len = int(1.5 * 16000)
    assert abs(len(samples) - expected_len) <= expected_len * 0.05

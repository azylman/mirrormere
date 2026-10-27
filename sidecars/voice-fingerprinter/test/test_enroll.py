"""Unit tests for enroll.py — the speaker fingerprint enrollment CLI.

No network calls: embed_wav is monkeypatched to a deterministic fake so
these tests never hit a real (or even a local) HTTP server. Audio
fixtures are synthesized with the stdlib `wave` module; nothing is ever
played.
"""
from __future__ import annotations

import importlib
import json
import math
import struct
import sys
import wave
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))
enroll = importlib.import_module("enroll")


def _make_wav_file(path: Path, seconds: float, sample_rate: int = 16000,
                    loud: bool = True) -> None:
    n_frames = int(seconds * sample_rate)
    amplitude = 8000 if loud else 50
    with wave.open(str(path), "wb") as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(sample_rate)
        frames = bytearray()
        for i in range(n_frames):
            sample = int(amplitude * math.sin(2 * math.pi * 220.0 * i / sample_rate))
            frames += struct.pack("<h", sample)
        w.writeframes(bytes(frames))


def _fake_embed_wav_factory(dim=192, model="speechbrain/spkrec-ecapa-voxceleb"):
    calls = []

    def fake_embed_wav(wav_bytes: bytes, embed_url: str):
        calls.append((wav_bytes, embed_url))
        # Deterministic per-call vector so centroid math is checkable.
        seed = len(calls)
        vec = [(seed + i) * 0.001 for i in range(dim)]
        return vec, model

    fake_embed_wav.calls = calls
    return fake_embed_wav


def test_split_wav_into_chunks(tmp_path):
    wav_path = tmp_path / "speaker.wav"
    _make_wav_file(wav_path, seconds=10.0)
    chunks = enroll.split_wav_into_chunks(wav_path, chunk_seconds=4.0)
    # 10s / 4s -> two full 4s chunks, a trailing 2s chunk kept (>= MIN_CHUNK_SECONDS=1s)
    assert len(chunks) == 3
    for wav_bytes, rms in chunks:
        assert isinstance(wav_bytes, bytes)
        assert rms > 0


def test_filter_loud_enough_drops_quiet_chunks(tmp_path):
    loud_path = tmp_path / "loud.wav"
    quiet_path = tmp_path / "quiet.wav"
    _make_wav_file(loud_path, seconds=4.0, loud=True)
    _make_wav_file(quiet_path, seconds=4.0, loud=False)

    loud_chunks = enroll.split_wav_into_chunks(loud_path)
    quiet_chunks = enroll.split_wav_into_chunks(quiet_path)
    mixed = loud_chunks + quiet_chunks

    kept = enroll.filter_loud_enough(mixed)
    # The quiet chunk's RMS is far below 10% of the loud chunk's RMS.
    assert len(kept) == len(loud_chunks)


def test_build_centroid_averages_and_normalizes(monkeypatch):
    fake = _fake_embed_wav_factory()
    monkeypatch.setattr(enroll, "embed_wav", fake)

    chunk_wavs = [b"chunk1", b"chunk2", b"chunk3"]
    centroid, model, stats = enroll.build_centroid(chunk_wavs, "http://fake/embed")

    assert model == "speechbrain/spkrec-ecapa-voxceleb"
    assert len(centroid) == 192
    norm = math.sqrt(sum(x * x for x in centroid))
    assert abs(norm - 1.0) < 1e-6
    assert stats["chunks"] == 3
    assert 0.0 <= stats["cosine_to_centroid_min"] <= 1.0001


def test_build_centroid_requires_minimum_chunks(monkeypatch):
    monkeypatch.setattr(enroll, "embed_wav", _fake_embed_wav_factory())
    with pytest.raises(RuntimeError, match="usable chunk"):
        enroll.build_centroid([b"only", b"two"], "http://fake/embed")


def test_build_centroid_rejects_inconsistent_models(monkeypatch):
    calls = {"n": 0}

    def flaky_embed(wav_bytes, embed_url):
        calls["n"] += 1
        model = "model-a" if calls["n"] == 1 else "model-b"
        return [0.1] * 192, model

    monkeypatch.setattr(enroll, "embed_wav", flaky_embed)
    with pytest.raises(RuntimeError, match="inconsistent models"):
        enroll.build_centroid([b"c1", b"c2", b"c3"], "http://fake/embed")


def test_merge_speaker_new_file():
    merged = enroll.merge_speaker(None, "speaker-1", [0.1, 0.2], "model-x", force=False)
    assert merged == {"model": "model-x", "speakers": [{"id": "speaker-1", "embedding": [0.1, 0.2]}]}


def test_merge_speaker_adds_to_existing():
    existing = {"model": "model-x", "speakers": [{"id": "speaker-2", "embedding": [0.5]}]}
    merged = enroll.merge_speaker(existing, "speaker-1", [0.1, 0.2], "model-x", force=False)
    ids = {s["id"] for s in merged["speakers"]}
    assert ids == {"speaker-2", "speaker-1"}


def test_merge_speaker_replaces_same_id():
    existing = {"model": "model-x", "speakers": [{"id": "speaker-1", "embedding": [9.9]}]}
    merged = enroll.merge_speaker(existing, "speaker-1", [0.1, 0.2], "model-x", force=False)
    assert merged["speakers"] == [{"id": "speaker-1", "embedding": [0.1, 0.2]}]


def test_merge_speaker_refuses_model_mismatch_without_force():
    existing = {"model": "model-old", "speakers": [{"id": "speaker-2", "embedding": [0.5]}]}
    with pytest.raises(RuntimeError, match="refusing to write a mixed-model"):
        enroll.merge_speaker(existing, "speaker-1", [0.1, 0.2], "model-new", force=False)


def test_merge_speaker_force_drops_other_speakers():
    existing = {"model": "model-old", "speakers": [{"id": "speaker-2", "embedding": [0.5]}]}
    merged = enroll.merge_speaker(existing, "speaker-1", [0.1, 0.2], "model-new", force=True)
    assert merged == {"model": "model-new", "speakers": [{"id": "speaker-1", "embedding": [0.1, 0.2]}]}


def test_write_and_read_speakers_roundtrip(tmp_path):
    path = tmp_path / "speakers.json"
    data = {"model": "model-x", "speakers": [{"id": "speaker-1", "embedding": [0.1, 0.2, 0.3]}]}
    enroll.write_speakers(path, data)
    assert path.exists()
    read_back = enroll.read_speakers(path)
    assert read_back == data
    # Atomic write leaves no temp file behind.
    leftovers = list(tmp_path.glob(".*"))
    assert leftovers == []


def test_write_and_read_yaml_config_roundtrip_preserves_existing_settings(tmp_path):
    cfg_path = tmp_path / "config.yaml"
    cfg_path.write_text(
        "# Header comment\n"
        'timezone: "America/Los_Angeles"\n'
        "voice_hub:\n"
        "  enabled: true\n"
        "  speaker_id:\n"
        '    embed_url: "http://voice-fingerprinter:9096/embed"\n'
        "    threshold: 0.75\n"
        "  fast_path:\n"
        '    url: "http://hass-proxy:8095/intent"\n',
        encoding="utf-8",
    )
    assert enroll.read_speakers(cfg_path) is None

    data1 = {"model": "speechbrain/spkrec-ecapa-voxceleb", "speakers": [{"id": "speaker-1", "embedding": [0.25, 0.5]}]}
    enroll.write_speakers(cfg_path, data1)
    assert enroll.read_speakers(cfg_path) == data1

    text = cfg_path.read_text(encoding="utf-8")
    assert "# Header comment" in text
    assert 'timezone: "America/Los_Angeles"' in text
    assert "threshold: 0.75" in text
    assert 'url: "http://hass-proxy:8095/intent"' in text

    data2 = {
        "model": "speechbrain/spkrec-ecapa-voxceleb",
        "speakers": [
            {"id": "speaker-1", "embedding": [0.25, 0.5]},
            {"id": "speaker-2", "embedding": [0.75, -0.25]},
        ],
    }
    enroll.write_speakers(cfg_path, data2)
    assert enroll.read_speakers(cfg_path) == data2


def test_read_speakers_missing_file_returns_none(tmp_path):
    assert enroll.read_speakers(tmp_path / "does-not-exist.json") is None
    assert enroll.read_speakers(tmp_path / "does-not-exist.yaml") is None


def test_read_speakers_bad_json_raises(tmp_path):
    path = tmp_path / "speakers.json"
    path.write_text("{not json", encoding="utf-8")
    with pytest.raises(RuntimeError, match="not valid JSON"):
        enroll.read_speakers(path)


def test_cmd_enroll_end_to_end_writes_yaml_and_json(tmp_path, monkeypatch, capsys):
    fake = _fake_embed_wav_factory()
    monkeypatch.setattr(enroll, "embed_wav", fake)

    wav_path = tmp_path / "speaker-1.wav"
    _make_wav_file(wav_path, seconds=12.0)  # enough chunks to clear MIN_SURVIVING_CHUNKS
    yaml_output = tmp_path / "config.yaml"

    rc = enroll.main([
        "speaker-1", "--wav", str(wav_path), "--output", str(yaml_output),
        "--embed-url", "http://fake/embed",
    ])
    assert rc == 0

    parsed = enroll.read_speakers(yaml_output)
    assert parsed["model"] == "speechbrain/spkrec-ecapa-voxceleb"
    assert len(parsed["speakers"]) == 1
    assert parsed["speakers"][0]["id"] == "speaker-1"

    json_output = tmp_path / "speakers.json"
    rc_json = enroll.main([
        "speaker-1", "--wav", str(wav_path), "--output", str(json_output),
        "--embed-url", "http://fake/embed",
    ])
    assert rc_json == 0
    written = json.loads(json_output.read_text())
    assert written["model"] == "speechbrain/spkrec-ecapa-voxceleb"
    assert written["speakers"][0]["id"] == "speaker-1"


def test_cmd_enroll_dry_run_does_not_write(tmp_path, monkeypatch):
    monkeypatch.setattr(enroll, "embed_wav", _fake_embed_wav_factory())
    wav_path = tmp_path / "speaker-1.wav"
    _make_wav_file(wav_path, seconds=12.0)
    output = tmp_path / "config.yaml"

    rc = enroll.main(["speaker-1", "--wav", str(wav_path), "--output", str(output), "--dry-run"])
    assert rc == 0
    assert not output.exists()


def test_cmd_enroll_model_mismatch_refused_via_cli(tmp_path, monkeypatch, capsys):
    output = tmp_path / "config.yaml"
    enroll.write_speakers(output, {"model": "old-model", "speakers": [{"id": "speaker-2", "embedding": [0.1]}]})

    monkeypatch.setattr(enroll, "embed_wav", _fake_embed_wav_factory(model="new-model"))
    wav_path = tmp_path / "speaker-1.wav"
    _make_wav_file(wav_path, seconds=12.0)

    rc = enroll.main(["speaker-1", "--wav", str(wav_path), "--output", str(output)])
    assert rc == 1

    # Original file untouched.
    data = enroll.read_speakers(output)
    assert data["model"] == "old-model"
    assert data["speakers"] == [{"id": "speaker-2", "embedding": [0.1]}]


def test_cmd_list_and_remove(tmp_path, capsys):
    output = tmp_path / "config.yaml"
    enroll.write_speakers(output, {
        "model": "model-x",
        "speakers": [{"id": "speaker-1", "embedding": [0.1, 0.2]}, {"id": "speaker-2", "embedding": [0.3]}],
    })

    rc = enroll.main(["--list", "--output", str(output)])
    assert rc == 0
    out = capsys.readouterr().out
    assert "speaker-1" in out and "speaker-2" in out

    rc = enroll.main(["--remove", "speaker-1", "--output", str(output)])
    assert rc == 0
    data = enroll.read_speakers(output)
    assert [s["id"] for s in data["speakers"]] == ["speaker-2"]

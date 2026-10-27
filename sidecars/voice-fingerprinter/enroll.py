#!/usr/bin/env python3
"""enroll.py — enroll (or remove/list) a speaker fingerprint for Mirrormere's
Voice Hub speaker-ID feature, via the voice-fingerprinter sidecar.

Calls the sidecar's /embed endpoint (default http://localhost:9096/embed) for
one or more WAV files, L2-normalizes and averages the resulting embeddings into
a centroid, and merges the result directly into Mirrormere's `config.yaml` under
`voice_hub.speaker_id.model` and `voice_hub.speaker_id.speakers` (or into a
standalone JSON file when `--output` ends with `.json`).

Usage:
  enroll.py <id> --wav f1.wav f2.wav ... [--output config.yaml] [--dry-run]
  enroll.py --list [--output config.yaml]
  enroll.py --remove <id> [--output config.yaml]

Each WAV is split into ~4s chunks; a chunk whose RMS is under a relative
floor of the loudest chunk in the file is dropped as near-silent. Each
surviving chunk is embedded, L2-normalized, averaged into a centroid, and
the centroid is L2-normalized again. The embedding `model` string enrolled
is whatever the embed server itself reports (and checked for consistency
across every chunk) — never a hardcoded constant — so this CLI and the
server can't drift out of sync.

The target config.yaml (or JSON file) is written atomically (temp file +
os.replace in the same directory) and merged: the target id is replaced or
added under `voice_hub.speaker_id.speakers`, `voice_hub.speaker_id.model` is
set/verified, and all other configuration keys and comments are preserved.
Refuses to write if the existing file's `model` differs from the newly enrolled
embedding's model, unless --force (which also drops every OTHER enrolled
speaker, since their embeddings are meaningless under a different model, and
prints a loud warning about it). --dry-run prints what would be written instead
of writing it.
"""
from __future__ import annotations

import argparse
import io
import json
import math
import os
import re
import struct
import sys
import tempfile
import wave
from pathlib import Path
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

DEFAULT_EMBED_URL = "http://localhost:9096/embed"
DEFAULT_OUTPUT = "config.yaml"
DEFAULT_DOCKER_EMBED_URL = "http://voice-fingerprinter:9096/embed"
EMBED_TIMEOUT_SEC = 30.0

CHUNK_SECONDS = 4.0
MIN_CHUNK_SECONDS = 1.0  # a trailing partial chunk shorter than this is dropped
RMS_RELATIVE_FLOOR = 0.1  # drop chunks quieter than 10% of the loudest chunk
MIN_SURVIVING_CHUNKS = 3


# --------------------------------------------------------------------------
# WAV chunking and loudness filtering (stdlib only — the embed server does
# its own decode/downmix/resample per chunk).
# --------------------------------------------------------------------------

def _chunk_rms(frames: bytes, sampwidth: int) -> float:
    """Generic RMS over raw PCM bytes, any (mono or interleaved) channel
    layout — used only as a relative loudness heuristic."""
    if sampwidth == 1:
        count = len(frames)
        samples = [s - 128 for s in struct.unpack(f"<{count}B", frames)]
    elif sampwidth == 2:
        count = len(frames) // 2
        samples = struct.unpack(f"<{count}h", frames[: count * 2])
    elif sampwidth == 4:
        count = len(frames) // 4
        samples = struct.unpack(f"<{count}i", frames[: count * 4])
    else:
        raise ValueError(f"unsupported sample width: {sampwidth}")
    if not samples:
        return 0.0
    return math.sqrt(sum(s * s for s in samples) / len(samples))


def split_wav_into_chunks(path: Path, chunk_seconds: float = CHUNK_SECONDS) -> list:
    """Splits a WAV file into ~chunk_seconds (wav_bytes, rms) tuples (same
    sample rate/channels/width as the source). A trailing partial chunk
    under MIN_CHUNK_SECONDS is dropped."""
    chunks = []
    with wave.open(str(path), "rb") as w:
        framerate = w.getframerate()
        channels = w.getnchannels()
        sampwidth = w.getsampwidth()
        chunk_frames = int(chunk_seconds * framerate)
        min_frames = int(MIN_CHUNK_SECONDS * framerate)
        while True:
            frames = w.readframes(chunk_frames)
            if not frames:
                break
            n_frames = len(frames) // (channels * sampwidth)
            if n_frames < min_frames:
                break
            buf = io.BytesIO()
            with wave.open(buf, "wb") as out:
                out.setnchannels(channels)
                out.setsampwidth(sampwidth)
                out.setframerate(framerate)
                out.writeframes(frames)
            chunks.append((buf.getvalue(), _chunk_rms(frames, sampwidth)))
    return chunks


def filter_loud_enough(chunks: list) -> list:
    """Drops chunks quieter than RMS_RELATIVE_FLOOR times the loudest
    chunk's RMS — a relative floor rather than an absolute one, since
    recording levels vary a lot by mic and distance."""
    if not chunks:
        return []
    max_rms = max(rms for _, rms in chunks)
    if max_rms <= 0:
        return []
    floor = max_rms * RMS_RELATIVE_FLOOR
    return [wav_bytes for wav_bytes, rms in chunks if rms >= floor]


# --------------------------------------------------------------------------
# Embedding + centroid math
# --------------------------------------------------------------------------

def embed_wav(wav_bytes: bytes, embed_url: str):
    req = Request(embed_url, data=wav_bytes, headers={"Content-Type": "audio/wav"}, method="POST")
    try:
        with urlopen(req, timeout=EMBED_TIMEOUT_SEC) as resp:
            data = json.loads(resp.read())
    except HTTPError as exc:
        raise RuntimeError(f"embed server returned {exc.code}: {exc.read()[:300]}") from exc
    except URLError as exc:
        raise RuntimeError(f"embed server unreachable at {embed_url}: {exc}") from exc
    embedding = data.get("embedding")
    model = data.get("model")
    if not embedding or not model:
        raise RuntimeError(f"embed server response missing embedding/model: {data}")
    return embedding, model


def _l2_normalize(vec: list) -> list:
    norm = math.sqrt(sum(x * x for x in vec))
    if norm == 0:
        raise ValueError("zero-length embedding")
    return [x / norm for x in vec]


def _cosine_similarity(a: list, b: list) -> float:
    dot = sum(x * y for x, y in zip(a, b))
    na = math.sqrt(sum(x * x for x in a))
    nb = math.sqrt(sum(y * y for y in b))
    if na == 0 or nb == 0:
        raise ValueError("zero-length vector")
    return dot / (na * nb)


def build_centroid(chunk_wavs: list, embed_url: str):
    """Embeds every chunk, L2-normalizes each, averages into a centroid,
    L2-normalizes the centroid, and reports chunk-to-centroid cosine
    min/mean as a quality signal. Returns (centroid, model, stats)."""
    if len(chunk_wavs) < MIN_SURVIVING_CHUNKS:
        raise RuntimeError(
            f"only {len(chunk_wavs)} usable chunk(s) survived filtering "
            f"(need at least {MIN_SURVIVING_CHUNKS}) — record more, or "
            "closer to the mic, and try again"
        )

    normalized = []
    model = None
    for wav_bytes in chunk_wavs:
        embedding, chunk_model = embed_wav(wav_bytes, embed_url)
        if model is None:
            model = chunk_model
        elif chunk_model != model:
            raise RuntimeError(
                f"embed server reported inconsistent models across chunks: "
                f"{model!r} vs {chunk_model!r} — is it mid-restart?"
            )
        normalized.append(_l2_normalize(embedding))

    dim = len(normalized[0])
    centroid = [sum(v[i] for v in normalized) / len(normalized) for i in range(dim)]
    centroid = _l2_normalize(centroid)

    sims = [_cosine_similarity(v, centroid) for v in normalized]
    stats = {
        "chunks": len(normalized),
        "cosine_to_centroid_min": round(min(sims), 4),
        "cosine_to_centroid_mean": round(sum(sims) / len(sims), 4),
    }
    return centroid, model, stats


# --------------------------------------------------------------------------
# Config YAML & Fingerprints JSON persistence on disk
# --------------------------------------------------------------------------

def _is_yaml_path(path: Path) -> bool:
    return path.suffix.lower() in (".yaml", ".yml")


def _strip_yaml_comment(val: str) -> str:
    in_single = False
    in_double = False
    for idx, ch in enumerate(val):
        if ch == "'" and not in_double:
            in_single = not in_single
        elif ch == '"' and not in_single:
            in_double = not in_double
        elif ch == "#" and not in_single and not in_double:
            if idx == 0 or val[idx - 1].isspace():
                return val[:idx].strip()
    return val.strip()


def _parse_yaml_scalar(raw: str) -> str:
    cleaned = _strip_yaml_comment(raw)
    if len(cleaned) >= 2 and ((cleaned[0] == '"' and cleaned[-1] == '"') or (cleaned[0] == "'" and cleaned[-1] == "'")):
        if cleaned[0] == '"':
            try:
                return str(json.loads(cleaned))
            except Exception:
                return cleaned[1:-1]
        return cleaned[1:-1]
    return cleaned


def _find_block_bounds(lines: list[str], start_idx: int, parent_indent: int) -> int:
    end_idx = len(lines)
    for idx in range(start_idx + 1, len(lines)):
        stripped = lines[idx].strip()
        if not stripped or stripped.startswith("#"):
            continue
        indent = len(lines[idx]) - len(lines[idx].lstrip(" "))
        if indent <= parent_indent:
            end_idx = idx
            break
    while end_idx > start_idx + 1 and (not lines[end_idx - 1].strip() or lines[end_idx - 1].lstrip().startswith("#")):
        end_idx -= 1
    return end_idx


def _read_yaml_speakers(path: Path) -> dict | None:
    text = path.read_text(encoding="utf-8")
    stripped_text = text.strip()
    if not stripped_text:
        return None
    if stripped_text.startswith("{"):
        try:
            obj = json.loads(stripped_text)
            sid = (obj.get("voice_hub") or {}).get("speaker_id") or obj
            if "model" in sid or "speakers" in sid:
                return {"model": sid.get("model"), "speakers": sid.get("speakers") or []}
            return None
        except Exception as exc:
            raise RuntimeError(f"{path} is not valid YAML: {exc}") from exc

    lines = text.splitlines()
    vh_idx = None
    for i, line in enumerate(lines):
        if re.match(r"^voice_hub:\s*(?:#.*)?$", line):
            vh_idx = i
            break
    if vh_idx is None:
        return None

    vh_end = _find_block_bounds(lines, vh_idx, 0)
    sid_idx = None
    for i in range(vh_idx + 1, vh_end):
        if re.match(r"^  speaker_id:\s*(?:#.*)?$", lines[i]):
            sid_idx = i
            break
    if sid_idx is None:
        return None

    sid_end = _find_block_bounds(lines, sid_idx, 2)
    model = None
    speakers: list[dict] = []
    has_speakers_key = False

    i = sid_idx + 1
    while i < sid_end:
        line = lines[i]
        stripped = line.strip()
        if not stripped or stripped.startswith("#"):
            i += 1
            continue
        m_model = re.match(r"^    model:\s*(.+)$", line)
        if m_model:
            model = _parse_yaml_scalar(m_model.group(1))
            i += 1
            continue
        m_spk = re.match(r"^    speakers:\s*(.*)$", line)
        if m_spk:
            has_speakers_key = True
            rest = _strip_yaml_comment(m_spk.group(1))
            i += 1
            if rest == "[]":
                continue
            current: dict = {}
            while i < sid_end:
                sub = lines[i]
                sub_s = sub.strip()
                if not sub_s or sub_s.startswith("#"):
                    i += 1
                    continue
                indent = len(sub) - len(sub.lstrip(" "))
                if indent < 4 or (indent == 4 and not sub_s.startswith("-")):
                    break
                if sub_s.startswith("-"):
                    if "id" in current and "embedding" in current:
                        speakers.append(current)
                    current = {}
                    sub_s = sub_s[1:].strip()
                if sub_s.startswith("id:"):
                    current["id"] = _parse_yaml_scalar(sub_s[3:].strip())
                elif sub_s.startswith("embedding:"):
                    raw_emb = _strip_yaml_comment(sub_s[len("embedding:"):].strip())
                    try:
                        current["embedding"] = [float(x) for x in json.loads(raw_emb)]
                    except Exception as exc:
                        raise RuntimeError(f"{path} has invalid speaker embedding list: {exc}") from exc
                i += 1
            if "id" in current and "embedding" in current:
                speakers.append(current)
            continue
        i += 1

    if model is None and not has_speakers_key:
        return None
    return {"model": model, "speakers": speakers}


def _format_speaker_id_entries(data: dict, indent: int = 4) -> list[str]:
    pad = " " * indent
    model_val = json.dumps(data.get("model") or "")
    out = [f"{pad}model: {model_val}"]
    speakers = data.get("speakers") or []
    if not speakers:
        out.append(f"{pad}speakers: []")
    else:
        out.append(f"{pad}speakers:")
        for sp in speakers:
            sp_id = json.dumps(sp["id"])
            emb_str = "[" + ", ".join(repr(float(x)) for x in sp["embedding"]) + "]"
            out.append(f"{pad}  - id: {sp_id}")
            out.append(f"{pad}    embedding: {emb_str}")
    return out


def _build_yaml_with_speakers(existing_text: str, data: dict) -> str:
    lines = existing_text.splitlines() if existing_text.strip() else []
    vh_idx = None
    for i, line in enumerate(lines):
        if re.match(r"^voice_hub:\s*(?:#.*)?$", line):
            vh_idx = i
            break

    entry_lines = _format_speaker_id_entries(data, indent=4)

    if vh_idx is None:
        if lines and lines[-1].strip():
            lines.append("")
        lines.extend([
            "voice_hub:",
            "  enabled: true",
            "  speaker_id:",
            f'    embed_url: "{DEFAULT_DOCKER_EMBED_URL}"',
            *entry_lines,
        ])
        return "\n".join(lines) + "\n"

    vh_end = _find_block_bounds(lines, vh_idx, 0)
    sid_idx = None
    for i in range(vh_idx + 1, vh_end):
        if re.match(r"^  speaker_id:\s*(?:#.*)?$", lines[i]):
            sid_idx = i
            break

    if sid_idx is None:
        new_sid = [
            "  speaker_id:",
            f'    embed_url: "{DEFAULT_DOCKER_EMBED_URL}"',
            *entry_lines,
        ]
        lines[vh_end:vh_end] = new_sid
        return "\n".join(lines) + "\n"

    sid_end = _find_block_bounds(lines, sid_idx, 2)
    kept: list[str] = []
    has_embed_url = False
    i = sid_idx + 1
    while i < sid_end:
        line = lines[i]
        if re.match(r"^    embed_url:\s*", line):
            has_embed_url = True
        if re.match(r"^    model:\s*", line):
            i += 1
            continue
        m_spk = re.match(r"^    speakers:\s*(.*)$", line)
        if m_spk:
            rest = _strip_yaml_comment(m_spk.group(1))
            i += 1
            if rest != "[]":
                while i < sid_end:
                    sub = lines[i]
                    sub_s = sub.strip()
                    if not sub_s or sub_s.startswith("#"):
                        i += 1
                        continue
                    indent = len(sub) - len(sub.lstrip(" "))
                    if indent < 4 or (indent == 4 and not sub_s.startswith("-")):
                        break
                    i += 1
            continue
        kept.append(line)
        i += 1

    if not has_embed_url:
        kept.insert(0, f'    embed_url: "{DEFAULT_DOCKER_EMBED_URL}"')

    lines[sid_idx + 1 : sid_end] = kept + entry_lines
    return "\n".join(lines) + "\n"


def read_speakers(path: Path):
    """Returns the parsed speaker_id dict {'model': ..., 'speakers': [...]},
    or None if the file (or voice_hub.speaker_id block) does not exist."""
    if not path.exists():
        return None
    if _is_yaml_path(path):
        return _read_yaml_speakers(path)
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except Exception as exc:
        raise RuntimeError(f"{path} is not valid JSON: {exc}") from exc


def write_speakers(path: Path, data: dict) -> None:
    """Atomic write: temp file in the same directory, then os.replace."""
    if _is_yaml_path(path):
        existing_text = path.read_text(encoding="utf-8") if path.exists() else ""
        payload = _build_yaml_with_speakers(existing_text, data)
    else:
        payload = json.dumps(data, indent=2) + "\n"
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, tmp_name = tempfile.mkstemp(dir=str(path.parent), prefix=f".{path.name}.", suffix=".tmp")
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as f:
            f.write(payload)
        os.replace(tmp_name, path)
    finally:
        if os.path.exists(tmp_name):
            os.unlink(tmp_name)


def merge_speaker(existing: dict | None, speaker_id: str, embedding: list, model: str, force: bool) -> dict:
    if existing is None:
        return {"model": model, "speakers": [{"id": speaker_id, "embedding": embedding}]}

    existing_model = existing.get("model")
    speakers = existing.get("speakers") or []

    if existing_model and existing_model != model and not force:
        raise RuntimeError(
            f"fingerprints file model is {existing_model!r} but this embed "
            f"server reports {model!r} — refusing to write a mixed-model "
            f"file. Pass --force to switch models (this drops every OTHER "
            f"enrolled speaker, since their embeddings are meaningless "
            f"under a different model)."
        )

    if existing_model and existing_model != model and force:
        print(
            f"WARNING: --force is switching the fingerprints file's model "
            f"from {existing_model!r} to {model!r}. Every other enrolled "
            f"speaker ({[s.get('id') for s in speakers if s.get('id') != speaker_id]}) "
            "is being dropped — their embeddings are not comparable under "
            "the new model."
        )
        speakers = []

    speakers = [s for s in speakers if s.get("id") != speaker_id]
    speakers.append({"id": speaker_id, "embedding": embedding})
    return {"model": model, "speakers": speakers}


# --------------------------------------------------------------------------
# CLI
# --------------------------------------------------------------------------

def cmd_list(output: Path) -> int:
    data = read_speakers(output)
    if data is None:
        print(f"No enrolled speakers found in {output}.")
        return 0
    print(f"model: {data.get('model')}")
    for sp in data.get("speakers") or []:
        dim = len(sp.get("embedding") or [])
        print(f"  {sp.get('id')}  ({dim}-dim embedding)")
    return 0


def cmd_remove(output: Path, speaker_id: str, dry_run: bool) -> int:
    data = read_speakers(output)
    if data is None:
        print(f"No enrolled speakers found in {output} — nothing to remove.")
        return 1
    speakers = data.get("speakers") or []
    if not any(s.get("id") == speaker_id for s in speakers):
        print(f"No speaker {speaker_id!r} enrolled.")
        return 1
    new_data = {"model": data.get("model"), "speakers": [s for s in speakers if s.get("id") != speaker_id]}
    if dry_run:
        print(f"[dry-run] would remove {speaker_id!r}, leaving:")
        print(json.dumps(new_data, indent=2))
        return 0
    write_speakers(output, new_data)
    print(f"Removed {speaker_id!r} from {output}.")
    return 0


def cmd_enroll(args) -> int:
    all_chunks = []
    for p in args.wav:
        wav_path = Path(p)
        chunks_with_rms = split_wav_into_chunks(wav_path, CHUNK_SECONDS)
        loud_enough = filter_loud_enough(chunks_with_rms)
        print(f"{wav_path}: {len(chunks_with_rms)} chunk(s), {len(loud_enough)} above the loudness floor")
        all_chunks.extend(loud_enough)

    centroid, model, stats = build_centroid(all_chunks, args.embed_url)
    print(
        f"Embedded {stats['chunks']} chunks with model {model!r}. "
        f"Chunk-to-centroid cosine: min={stats['cosine_to_centroid_min']}, "
        f"mean={stats['cosine_to_centroid_mean']}"
    )
    if stats["cosine_to_centroid_min"] < 0.70:
        print(
            "WARNING: chunk-to-centroid minimum is below 0.70 — this "
            "recording may be too inconsistent (background noise, "
            "distance from the mic, multiple speakers) for a reliable "
            "fingerprint. Consider re-recording."
        )

    existing = read_speakers(args.output)
    merged = merge_speaker(existing, args.speaker_id, centroid, model, args.force)

    if args.dry_run:
        print(f"[dry-run] would write to {args.output}:")
        preview = dict(merged)
        preview["speakers"] = [
            {"id": s["id"], "embedding": f"<{len(s['embedding'])} floats>"} for s in merged["speakers"]
        ]
        print(json.dumps(preview, indent=2))
        return 0

    write_speakers(args.output, merged)
    print(f"Enrolled {args.speaker_id!r} to {args.output}.")
    return 0


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("speaker_id", nargs="?", help="speaker id to enroll (e.g. speaker-1)")
    parser.add_argument("--wav", nargs="+", metavar="FILE", help="WAV file(s) of the speaker talking")
    parser.add_argument("--embed-url", default=DEFAULT_EMBED_URL,
                         help=f"voice-fingerprinter server URL (default {DEFAULT_EMBED_URL})")
    parser.add_argument("--output", "--config", dest="output", default=DEFAULT_OUTPUT, type=Path,
                         help=f"Mirrormere config.yaml (or fingerprints JSON) path to read/write (default {DEFAULT_OUTPUT}) "
                              "— updates voice_hub.speaker_id.model and voice_hub.speaker_id.speakers in-place")
    parser.add_argument("--dry-run", action="store_true", help="print what would be written, don't write it")
    parser.add_argument("--force", action="store_true",
                         help="allow switching the configured model (drops every other enrolled speaker)")
    parser.add_argument("--list", action="store_true", help="list enrolled speakers and exit")
    parser.add_argument("--remove", metavar="ID", help="remove a speaker id and exit")
    args = parser.parse_args(argv)

    if args.list:
        return cmd_list(args.output)
    if args.remove:
        return cmd_remove(args.output, args.remove, args.dry_run)

    if not args.speaker_id:
        parser.error("speaker_id is required unless --list or --remove is given")
    if not args.wav:
        parser.error("--wav is required")

    try:
        return cmd_enroll(args)
    except RuntimeError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())

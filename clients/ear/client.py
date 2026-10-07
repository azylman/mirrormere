"""Mirrormere Edge Voice Daemon

Listens to microphone input (ALSA / PipeWire), performs local wake word detection
via openWakeWord, relays interaction states to Mirrormere Core HUD, and streams
captured speech utterances to the LAN Voice Hub.

Optionally runs in "ambient" or "both" wake mode (see `wake_mode` in
`config.py`): the existing energy VAD cuts every speech segment and posts it
to a classifier-gated `ambient_url` endpoint instead of (or alongside) the
openWakeWord wake phrase.
"""

import base64
from collections import deque
import glob
import io
import json
import logging
import math
import os
import shutil
import signal
import struct
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.request
import uuid
import wave
from typing import Any, Dict, List, Optional

try:
    import numpy as np
except ImportError:
    np = None

try:
    from .config import VoiceConfig, load_config
    from .vad import build_detector
except (ImportError, ValueError):
    from config import VoiceConfig, load_config
    from vad import build_detector

logger = logging.getLogger("mirrormere-voice")

HEARTBEAT_FILE = os.environ.get("MIRRORMERE_HEARTBEAT_FILE", "/tmp/ear.heartbeat")


def _touch_heartbeat(path: Optional[str] = None) -> None:
    """Touches local heartbeat timestamp file for container healthcheck."""
    target_path = path or os.environ.get("MIRRORMERE_HEARTBEAT_FILE", HEARTBEAT_FILE)
    try:
        os.makedirs(os.path.dirname(os.path.abspath(target_path)), exist_ok=True)
        with open(target_path, "w", encoding="utf-8") as f:
            f.write(str(int(time.time())))
    except Exception:
        pass


def post_voice_state(
    mirrormere_url: str,
    state: str,
    transcript: Optional[str] = None,
    reply: Optional[str] = None,
    tts_engine: Optional[str] = None,
    timeout: float = 1.0,
) -> bool:
    """Relays voice interaction lifecycle state to Mirrormere Core for HUD updates."""
    if not mirrormere_url:
        return False
    url = f"{mirrormere_url.rstrip('/')}/api/voice/state"
    payload = {
        "state": state,
        "transcript": transcript,
        "reply": reply,
        "tts_engine": tts_engine,
    }
    data = json.dumps(payload).encode("utf-8")
    req = urllib.request.Request(
        url,
        data=data,
        headers={"Content-Type": "application/json", "Accept": "application/json"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            status = getattr(resp, "status", 200)
            logger.debug("Posted state '%s' to %s: HTTP %d", state, url, status)
            return status == 200
    except Exception as e:
        logger.debug("Failed to post voice state to %s: %s", url, e)
        return False


def _heartbeat_url(hub_url: str) -> str:
    """Resolves the heartbeat endpoint URL from hub_url."""
    if not hub_url:
        return ""
    if hub_url.endswith("/api/voice/heartbeat"):
        return hub_url
    if "/api/voice/interact" in hub_url:
        return hub_url.replace("/api/voice/interact", "/api/voice/heartbeat")
    if hub_url.endswith("/interact"):
        return hub_url[:-len("/interact")] + "/heartbeat"
    return f"{hub_url.rstrip('/')}/api/voice/heartbeat"


def post_voice_heartbeat(
    hub_url: str,
    node_id: str,
    db: float,
    false_wakes: int = 0,
    last_playback_sec: float = 0.0,
    timeout: float = 2.0,
) -> bool:
    """Relays edge liveness, ambient RMS, false wake counts, and playback duration to Mirrormere Voice Hub."""
    if not hub_url:
        return False
    url = _heartbeat_url(hub_url)
    payload = {
        "node_id": node_id,
        "ambient_rms_dbfs": round(float(db), 1),
        "false_wakes": int(false_wakes),
        "last_playback_sec": round(float(last_playback_sec), 2),
    }
    data = json.dumps(payload).encode("utf-8")
    req = urllib.request.Request(
        url,
        data=data,
        headers={"Content-Type": "application/json", "Accept": "application/json"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            status = getattr(resp, "status", 200)
            logger.debug("Posted heartbeat to %s: HTTP %d", url, status)
            return status == 200
    except (urllib.error.URLError, Exception) as e:
        logger.warning("Failed to post voice heartbeat to %s: %s", url, e)
        return False


def tokenize_keyword_phrase(
    phrase: str,
    bpe_model_path: str = "/opt/mirrormere/voice/sherpa/bpe.model",
    tokens_path: Optional[str] = None,
) -> str:
    """Encodes a plain text wake phrase into Sherpa BPE tokens."""
    phrase = phrase.strip()
    if not phrase or "▁" in phrase:
        return phrase
    if not os.path.exists(bpe_model_path) or not os.access(bpe_model_path, os.R_OK):
        raise RuntimeError(f"Sherpa BPE tokenizer model missing or unreadable at {bpe_model_path}")
    try:
        import sentencepiece as spm
        sp = spm.SentencePieceProcessor()
        sp.load(bpe_model_path)
        return " ".join(sp.encode_as_pieces(phrase.upper()))
    except ImportError:
        try:
            import sherpa_onnx
            if hasattr(sherpa_onnx, "SentencePieceTokenizer"):
                return " ".join(sherpa_onnx.SentencePieceTokenizer(bpe_model_path).encode(phrase.upper()))
        except Exception as e:
            raise RuntimeError(f"Failed to tokenize wake phrase '{phrase}': {e}") from e
    raise RuntimeError(f"No SentencePiece tokenizer available to tokenize keyword '{phrase}'")


def format_sherpa_keyword_line(
    raw_line: str,
    default_score: float = 1.0,
    default_threshold: float = 0.25,
    bpe_model_path: str = "/opt/mirrormere/voice/sherpa/bpe.model",
    tokens_path: Optional[str] = None,
) -> str:
    """Parses a raw keyword line, strips score/threshold annotations, tokenizes to BPE, and re-attaches annotations."""
    line = raw_line.strip()
    if not line or line.startswith("#"):
        return ""
    tokens, score, threshold = [], default_score, default_threshold
    for tok in line.split():
        if tok.startswith(":"):
            try: score = float(tok[1:])
            except ValueError: tokens.append(tok)
        elif tok.startswith("#"):
            try: threshold = float(tok[1:])
            except ValueError: tokens.append(tok)
        elif not tok.startswith("@"):
            tokens.append(tok)
    text = " ".join(tokens)
    if not text:
        return ""
    tokenized = tokenize_keyword_phrase(text, bpe_model_path=bpe_model_path, tokens_path=tokens_path)
    return f"{tokenized} :{score} #{threshold}"


class VoiceDaemon:
    """Manages audio capture, wake word detection, and utterance forwarding."""

    # Minimum voiced span (seconds) an ambient-mode segment must contain
    # before it's worth POSTing to the classifier.
    AMBIENT_MIN_SPEECH_SECONDS = 0.4

    def __init__(self, cfg: VoiceConfig, model: Optional[Any] = None) -> None:
        self.cfg = cfg
        self.running = True
        self.state = "idle"
        self.cooldown_until = 0.0
        self.utterance_buffer: List[bytes] = []
        self.pre_roll: deque[bytes] = deque(maxlen=12)  # ~1.0s of 80ms frames @ 16kHz
        self.silence_start: Optional[float] = None
        self.record_start: Optional[float] = None
        self.has_spoken = False
        self.max_seen = {}
        self.current_worker: Optional[threading.Thread] = None

        # Telemetry tracking attributes
        self.last_wake_eval_ms: float = 0.0
        self.last_speech_duration_ms: float = 0.0
        self.last_silence_duration_ms: float = 0.0
        self.false_wakes_count: int = 0
        self.last_playback_sec: float = 0.0

        # Ambient (classifier-gated, no wake word) capture state.
        self.ambient_buffer: List[bytes] = []
        self.ambient_record_start: Optional[float] = None
        self.ambient_silence_start: Optional[float] = None
        self.ambient_first_voice_at: Optional[float] = None
        self.ambient_last_voice_at: Optional[float] = None
        self.ambient_inflight = False

        # Busy tracking so ambient mode never sends the daemon's own reply
        # audio back to the classifier. Set for the duration of every hub
        # interaction (wake-triggered or ambient-engaged) and for
        # cooldown_seconds afterwards.
        self.busy = False
        self.busy_lock = threading.Lock()
        self.reply_ended_at = 0.0

        self.sherpa_stream = None
        if model is not None:
            self.model = model
            if self.cfg.wake_engine == "sherpa-onnx":
                self.active_models = ["sherpa"]
                if hasattr(model, "create_stream"):
                    self.sherpa_stream = model.create_stream()
            elif hasattr(model, "models") and isinstance(model.models, dict):
                available = list(model.models.keys())
                configured_targets = self.cfg.wake_models
                configured_names = (
                    {os.path.basename(os.path.splitext(t)[0]) for t in configured_targets}
                    | {os.path.splitext(t)[0] for t in configured_targets}
                    | set(configured_targets)
                )
                self.active_models = [m for m in available if m in configured_names]
                if not self.active_models:
                    logger.error(
                        "No configured wake models %s matched loaded openWakeWord models %s; "
                        "failing closed (wake word triggers disabled)",
                        configured_targets,
                        available,
                    )
            else:
                self.active_models = list(cfg.wake_models)
        elif not cfg.wake_mode_uses_wake_word():
            # Ambient-only mode never needs any wake engine: skip loading its
            # models entirely to keep CPU and memory usage low.
            self.model = None
            self.active_models = []
        elif cfg.wake_engine == "sherpa-onnx":
            self.model, self.sherpa_stream = self._init_sherpa()
            self.active_models = ["sherpa"]
        else:
            self.model, self.active_models = self._init_openwakeword()

        for m in self.active_models:
            self.max_seen[m] = 0.0

        self.speech_detector = build_detector(
            cfg.vad,
            cfg.vad_threshold,
            cfg.speech_threshold_db,
            self.compute_db,
            wake_engine=cfg.wake_engine,
            sherpa_model_dir=cfg.sherpa_model_dir,
        )

    def _init_openwakeword(self):
        """Discovers custom models and instantiates the openWakeWord Model."""
        from openwakeword.model import Model

        custom_models = []
        if os.path.exists(self.cfg.models_dir):
            custom_models = glob.glob(os.path.join(self.cfg.models_dir, "*.onnx"))

        if custom_models:
            logger.info("Discovered custom wake models: %s", custom_models)
            model = Model(wakeword_model_paths=custom_models, vad_threshold=0.5)
        else:
            model = Model(vad_threshold=0.5)

        available = list(model.models.keys())
        logger.info("Loaded openWakeWord models: %s", available)

        configured_targets = self.cfg.wake_models
        configured_names = (
            {os.path.basename(os.path.splitext(t)[0]) for t in configured_targets}
            | {os.path.splitext(t)[0] for t in configured_targets}
            | set(configured_targets)
        )
        active = [m for m in available if m in configured_names]
        if not active:
            logger.error(
                "No configured wake models %s matched loaded openWakeWord models %s; "
                "failing closed (wake word triggers disabled)",
                configured_targets,
                available,
            )

        logger.info("Active wake word triggers: %s (threshold=%.2f)", active, self.cfg.threshold)
        return model, active

    def _init_sherpa(self):
        """Discovers base Zipformer model, tokenizes keywords, and instantiates sherpa-onnx KeywordSpotter."""
        try:
            import sherpa_onnx
        except ImportError as e:
            raise RuntimeError(f"sherpa_onnx is not installed: {e}") from e

        model_dir = self.cfg.sherpa_model_dir
        if not os.path.exists(model_dir):
            raise RuntimeError(f"Sherpa model directory not found: {model_dir}")

        tokens = os.path.join(model_dir, "tokens.txt")
        bpe_model = os.path.join(model_dir, "bpe.model")

        def _find_model(prefix):
            p = os.path.join(model_dir, f"{prefix}.int8.onnx")
            if os.path.exists(p): return p
            c = glob.glob(os.path.join(model_dir, f"{prefix}*.int8.onnx")) + glob.glob(os.path.join(model_dir, f"{prefix}*.onnx"))
            return c[0] if c else os.path.join(model_dir, f"{prefix}.onnx")

        encoder, decoder, joiner = _find_model("encoder"), _find_model("decoder"), _find_model("joiner")

        for p in (tokens, encoder, decoder, joiner):
            if not os.path.exists(p) or not os.access(p, os.R_OK):
                raise RuntimeError(f"Sherpa model file missing or unreadable: {p}")

        num_threads = max(1, min(int(self.cfg.sherpa_num_threads), 2))

        # Collect raw keywords from file, cfg.keyword, or cfg.wake_models fallback
        raw_keywords = []
        if os.path.exists(self.cfg.keywords_file) and os.access(self.cfg.keywords_file, os.R_OK):
            logger.info("Using Sherpa keywords file: %s", self.cfg.keywords_file)
            with open(self.cfg.keywords_file, "r", encoding="utf-8") as f:
                for line in f:
                    s = line.strip()
                    if s and not s.startswith("#"):
                        raw_keywords.append(s)
        elif self.cfg.keyword:
            logger.info("Using Sherpa inline keyword: %s", self.cfg.keyword)
            raw_keywords.append(self.cfg.keyword)
        elif self.cfg.wake_models:
            for m in self.cfg.wake_models:
                raw_keywords.append(" ".join(m.replace("-", "_").split("_")))
            logger.info("Using Sherpa keywords from wake_models fallback: %s", raw_keywords)
        else:
            raise RuntimeError(
                f"No wake keywords configured for Sherpa (keywords_file '{self.cfg.keywords_file}' not found and cfg.keyword is empty)"
            )

        formatted_lines = []
        for raw in raw_keywords:
            line = format_sherpa_keyword_line(
                raw,
                default_score=self.cfg.keywords_score,
                default_threshold=self.cfg.keywords_threshold,
                bpe_model_path=bpe_model,
                tokens_path=tokens,
            )
            if line:
                formatted_lines.append(line)

        if not formatted_lines:
            raise RuntimeError("No valid wake keywords could be formatted for Sherpa")

        # Materialize tokenized keywords into a file for KeywordSpotter (compatible across all sherpa versions)
        kw_file = os.path.join(model_dir, "generated_keywords.txt")
        try:
            with open(kw_file, "w", encoding="utf-8") as f:
                f.write("\n".join(formatted_lines) + "\n")
        except OSError:
            with tempfile.NamedTemporaryFile("w", suffix=".txt", delete=False, encoding="utf-8") as tf:
                tf.write("\n".join(formatted_lines) + "\n")
                kw_file = tf.name

        self._sherpa_keywords_file = kw_file

        kwargs = {
            "tokens": tokens,
            "encoder": encoder,
            "decoder": decoder,
            "joiner": joiner,
            "num_threads": num_threads,
            "keywords_file": kw_file,
            "keywords_score": self.cfg.keywords_score,
            "keywords_threshold": self.cfg.keywords_threshold,
            "provider": "cpu",
        }

        try:
            spotter = sherpa_onnx.KeywordSpotter(**kwargs)
            stream = spotter.create_stream()
            logger.info("Sherpa-ONNX KeywordSpotter initialized successfully (threads=%d, keywords=%d)", num_threads, len(formatted_lines))
            return spotter, stream
        except Exception as e:
            raise RuntimeError(f"Failed to initialize Sherpa KeywordSpotter: {e}") from e

    def stop(self, signum=None, frame=None) -> None:
        """Signal handler to stop main loop gracefully."""
        logger.info("Shutdown signal received, stopping voice daemon...")
        self.running = False

    def reset_wake_engine(self) -> None:
        """Flushes/resets internal decoder state for the active wake engine."""
        if self.cfg.wake_engine == "sherpa-onnx":
            if self.model is not None and self.sherpa_stream is not None:
                try:
                    if hasattr(self.model, "reset_stream"):
                        self.model.reset_stream(self.sherpa_stream)
                    elif hasattr(self.sherpa_stream, "reset"):
                        self.sherpa_stream.reset()
                except Exception as e:
                    logger.debug("Error resetting Sherpa stream: %s", e)
        else:
            self.flush_openwakeword()

    def flush_openwakeword(self) -> None:
        """Flushes feature buffers and prediction histories in openWakeWord."""
        try:
            if hasattr(self.model, "reset"):
                self.model.reset()
            if hasattr(self.model, "preprocessor"):
                prep = self.model.preprocessor
                if hasattr(prep, "raw_data_buffer") and hasattr(prep.raw_data_buffer, "clear"):
                    prep.raw_data_buffer.clear()
                if hasattr(prep, "feature_buffer") and hasattr(prep.feature_buffer, "fill"):
                    prep.feature_buffer.fill(0)
                if hasattr(prep, "melspectrogram_buffer") and hasattr(prep.melspectrogram_buffer, "fill"):
                    prep.melspectrogram_buffer.fill(0)
            logger.debug("Flushed openWakeWord feature and prediction buffers.")
        except Exception as e:
            logger.debug("Error flushing openWakeWord buffers: %s", e)

    @staticmethod
    def compute_db(raw_bytes: bytes) -> float:
        """Calculates RMS level in dBFS for a PCM int16 chunk."""
        if not raw_bytes:
            return -100.0
        if np is not None:
            chunk = np.frombuffer(raw_bytes, dtype=np.int16)
            rms = math.sqrt(np.mean(chunk.astype(np.float64) ** 2))
        else:
            n_samples = len(raw_bytes) // 2
            if n_samples == 0:
                return -100.0
            samples = struct.unpack(f"<{n_samples}h", raw_bytes)
            sum_sq = sum(s * s for s in samples)
            rms = math.sqrt(sum_sq / n_samples)
        return 20.0 * math.log10(rms / 32768.0) if rms > 0 else -100.0

    def wake_mode_uses_wake_word(self) -> bool:
        return self.cfg.wake_mode_uses_wake_word() and self.model is not None and bool(self.active_models)

    # Backward-compatible alias
    wake_mode_uses_openwakeword = wake_mode_uses_wake_word

    def wake_mode_uses_ambient(self) -> bool:
        return self.cfg.wake_mode in ("ambient", "both")

    def _check_wake_word(self, raw_bytes: bytes) -> Optional[str]:
        """Runs wake word prediction pass on the configured engine."""
        if not self.active_models or self.model is None:
            return None
        if self.cfg.wake_engine == "sherpa-onnx":
            return self._check_sherpa_wake_word(raw_bytes)
        return self._check_oww_wake_word(raw_bytes)

    def _check_sherpa_wake_word(self, raw_bytes: bytes) -> Optional[str]:
        if self.sherpa_stream is None or not raw_bytes:
            return None
        t0 = time.perf_counter()
        # Sherpa accept_waveform strictly requires 1-D float32 normalized to [-1.0, 1.0]
        if np is not None:
            float_samples = np.frombuffer(raw_bytes, dtype=np.int16).astype(np.float32) / 32768.0
        else:
            import array
            n_samples = len(raw_bytes) // 2
            samples = struct.unpack(f"<{n_samples}h", raw_bytes)
            float_samples = array.array("f", (s / 32768.0 for s in samples))

        self.sherpa_stream.accept_waveform(self.cfg.sample_rate, float_samples)

        decode_steps = 0
        while (
            hasattr(self.model, "is_ready")
            and self.model.is_ready(self.sherpa_stream)
            and decode_steps < 10
        ):
            if hasattr(self.model, "decode_stream"):
                self.model.decode_stream(self.sherpa_stream)
            elif hasattr(self.model, "decode"):
                self.model.decode(self.sherpa_stream)
            decode_steps += 1


        result = self.model.get_result(self.sherpa_stream) if hasattr(self.model, "get_result") else None
        self.last_wake_eval_ms = (time.perf_counter() - t0) * 1000.0

        detected_keyword = None
        if result:
            kw = getattr(result, "keyword", result) if not isinstance(result, str) else result
            if kw:
                detected_keyword = str(kw)
                logger.info("*** WAKE WORD DETECTED (sherpa): %s ***", detected_keyword)
                self.reset_wake_engine()

        return detected_keyword


    def _check_oww_wake_word(self, raw_bytes: bytes) -> Optional[str]:
        """Runs one openWakeWord prediction pass and returns the triggered model name, if any."""
        t0 = time.perf_counter()
        chunk = np.frombuffer(raw_bytes, dtype=np.int16) if np is not None else raw_bytes
        preds = self.model.predict(chunk)
        self.last_wake_eval_ms = (time.perf_counter() - t0) * 1000.0
        triggered_model = None

        for m in self.active_models:
            score = float(preds.get(m, 0.0))
            if score > self.max_seen[m]:
                self.max_seen[m] = score
            if score >= 0.05:
                logger.debug("[WAKE SCORE] %s: %.3f (threshold=%.2f)", m, score, self.cfg.threshold)
            if score >= self.cfg.threshold:
                logger.info("*** WAKE WORD DETECTED: %s (score: %.3f) ***", m, score)
                triggered_model = m
                break

        return triggered_model


    def _start_listening(self, now: float) -> None:
        """Transitions into wake-triggered recording, mirroring the wake-word flow."""
        self.state = "listening"
        post_voice_state(self.cfg.mirrormere_url, "listening")
        # Prepend pre-roll buffer so starting speech phonemes are never clipped
        self.utterance_buffer = list(self.pre_roll)
        self.record_start = now
        self.silence_start = None
        self.has_spoken = False
        self.speech_detector.reset()

    def _ambient_ready(self, now: float) -> bool:
        """True when it's safe to start/send an ambient segment: not mid-reply, not in the post-reply cooldown."""
        if self.busy:
            return False
        if now < self.reply_ended_at + self.cfg.cooldown_seconds:
            return False
        return True

    def _wake_ready(self, now: float) -> bool:
        """True when it's safe to evaluate wake words: not mid-reply, not in post-reply or utterance cooldown."""
        if self.busy:
            return False
        if now < self.reply_ended_at + self.cfg.cooldown_seconds:
            return False
        if now < self.cooldown_until:
            return False
        return True

    def _reset_ambient_state(self) -> None:
        self.ambient_buffer = []
        self.ambient_record_start = None
        self.ambient_silence_start = None
        self.ambient_first_voice_at = None
        self.ambient_last_voice_at = None

    def _start_ambient_segment(self, now: float) -> None:
        self.state = "ambient_listening"
        # Prepend pre-roll buffer, same rationale as the wake-word path.
        self.ambient_buffer = list(self.pre_roll)
        self.ambient_record_start = now
        self.ambient_silence_start = None
        self.ambient_first_voice_at = now
        self.ambient_last_voice_at = now
        self.speech_detector.reset()

    def process_frame(self, raw_bytes: bytes) -> Optional[str]:
        """Processes a single audio frame (1280 samples = 80ms @ 16kHz)."""
        now = time.time()

        if self.state == "idle":
            self.pre_roll.append(raw_bytes)

            if self.wake_mode_uses_wake_word() and self._wake_ready(now):
                if self._check_wake_word(raw_bytes):
                    self._start_listening(now)
                    return "wake_detected"

            if self.wake_mode_uses_ambient() and self._ambient_ready(now):
                if self.speech_detector.is_speech(raw_bytes):
                    self._start_ambient_segment(now)
                    return "ambient_segment_started"

            return None

        elif self.state == "ambient_listening":
            if self.wake_mode_uses_wake_word() and self._wake_ready(now) and self._check_wake_word(raw_bytes):
                # Wake word fired mid-segment ("both" mode): hand off to the
                # normal wake-triggered flow and discard the ambient buffer
                # so this segment never reaches the classifier.
                self._reset_ambient_state()
                self._start_listening(now)
                return "wake_detected"

            self.ambient_buffer.append(raw_bytes)
            elapsed = now - (self.ambient_record_start or now)
            is_speech = self.speech_detector.is_speech(raw_bytes)

            if is_speech:
                if self.ambient_first_voice_at is None:
                    self.ambient_first_voice_at = now
                self.ambient_last_voice_at = now
                self.ambient_silence_start = None
            else:
                if self.ambient_silence_start is None:
                    self.ambient_silence_start = now

            silence_duration = (now - self.ambient_silence_start) if self.ambient_silence_start else 0.0
            silence_limit = self.cfg.silence_ms / 1000.0

            if silence_duration >= silence_limit or elapsed >= self.cfg.max_record_seconds:
                frames = self.ambient_buffer
                voiced_span = 0.0
                if self.ambient_first_voice_at is not None and self.ambient_last_voice_at is not None:
                    voiced_span = self.ambient_last_voice_at - self.ambient_first_voice_at

                self._reset_ambient_state()
                self.state = "idle"

                if voiced_span >= self.AMBIENT_MIN_SPEECH_SECONDS and self._ambient_ready(now):
                    self._dispatch_ambient_classification(frames)
                    return "ambient_segment_dispatched"
                return "ambient_segment_dropped"

            return None

        elif self.state == "listening":
            self.utterance_buffer.append(raw_bytes)
            elapsed = now - (self.record_start or now)

            is_speech = self.speech_detector.is_speech(raw_bytes)
            if is_speech:
                self.has_spoken = True
                self.silence_start = None
            else:
                if self.silence_start is None:
                    self.silence_start = now

            # False wake abort: If wake fired but zero speech occurred within 3.0s, abort back to idle
            if not self.has_spoken and elapsed >= 3.0:
                logger.info("False wake trigger (no speech detected after 3.0s). Aborting to idle...")
                self.reset_wake_engine()
                self.cooldown_until = now + 1.0
                self.state = "idle"
                post_voice_state(self.cfg.mirrormere_url, "idle")
                self.utterance_buffer = []
                self.false_wakes_count += 1
                return "wake_aborted"

            silence_duration = (now - self.silence_start) if self.silence_start else 0.0
            silence_limit = self.cfg.silence_ms / 1000.0

            if (self.has_spoken and silence_duration >= silence_limit) or elapsed >= self.cfg.max_record_seconds:
                pre_roll_sec = (len(self.pre_roll) * self.cfg.chunk_samples) / float(self.cfg.sample_rate)
                elapsed = (now - (self.record_start or now)) + pre_roll_sec
                silence_duration = (now - self.silence_start) if self.silence_start else 0.0
                self.last_speech_duration_ms = max(0.0, elapsed - silence_duration) * 1000.0
                self.last_silence_duration_ms = silence_duration * 1000.0

                logger.info(
                    "Utterance complete (%.2fs, silence=%.2fs). Forwarding to Voice Hub...",
                    elapsed,
                    silence_duration,
                )
                self.save_utterance(self.utterance_buffer)
                if self.cfg.hub_url:
                    # Claim busy now, not when the worker thread starts, so
                    # an ambient engage can't slip into the gap.
                    with self.busy_lock:
                        self.busy = True
                    self.dispatch_hub_interaction(self.cfg.save_path)
                else:
                    post_voice_state(self.cfg.mirrormere_url, "idle")

                self.reset_wake_engine()
                self.cooldown_until = time.time() + self.cfg.cooldown_seconds
                self.max_seen = {m: 0.0 for m in self.active_models}
                self.state = "idle"
                self.utterance_buffer = []
                return "utterance_saved"

        return None

    def save_utterance(self, buffer: List[bytes]) -> bool:
        """Saves accumulated PCM audio frames into a 16 kHz mono WAV file."""
        try:
            os.makedirs(os.path.dirname(os.path.abspath(self.cfg.save_path)), exist_ok=True)
            with wave.open(self.cfg.save_path, "wb") as wf:
                wf.setnchannels(1)
                wf.setsampwidth(2)
                wf.setframerate(self.cfg.sample_rate)
                wf.writeframes(b"".join(buffer))
            duration = (len(buffer) * self.cfg.chunk_samples) / float(self.cfg.sample_rate)
            logger.info("Saved utterance to %s (%.2fs)", self.cfg.save_path, duration)
            return True
        except Exception as e:
            logger.error("Failed to save utterance WAV: %s", e)
            return False

    def _frames_to_wav_bytes(self, frames: List[bytes]) -> bytes:
        """Encodes accumulated PCM audio frames into an in-memory 16 kHz mono WAV."""
        buf = io.BytesIO()
        with wave.open(buf, "wb") as wf:
            wf.setnchannels(1)
            wf.setsampwidth(2)
            wf.setframerate(self.cfg.sample_rate)
            wf.writeframes(b"".join(frames))
        return buf.getvalue()

    def _classify_ambient(self, wav_bytes: bytes) -> Optional[Dict[str, Any]]:
        """POSTs a captured segment to ambient_url and returns the decoded JSON response, or None on failure."""
        if not self.cfg.ambient_url:
            return None

        boundary = f"----AmbientBoundary{uuid.uuid4().hex}"
        body = bytearray()
        body.extend(f"--{boundary}\r\n".encode("utf-8"))
        body.extend(b'Content-Disposition: form-data; name="node_id"\r\n\r\n')
        body.extend(f"{self.cfg.node_id}\r\n".encode("utf-8"))
        body.extend(f"--{boundary}\r\n".encode("utf-8"))
        body.extend(b'Content-Disposition: form-data; name="audio"; filename="segment.wav"\r\n')
        body.extend(b"Content-Type: audio/wav\r\n\r\n")
        body.extend(wav_bytes)
        body.extend(b"\r\n")
        body.extend(f"--{boundary}--\r\n".encode("utf-8"))

        req = urllib.request.Request(
            self.cfg.ambient_url,
            data=bytes(body),
            headers={
                "Content-Type": f"multipart/form-data; boundary={boundary}",
                "Accept": "application/json",
            },
            method="POST",
        )
        try:
            with urllib.request.urlopen(req, timeout=self.cfg.ambient_timeout_seconds) as resp:
                raw = resp.read()
            data = json.loads(raw.decode("utf-8"))
            if not isinstance(data, dict):
                logger.debug("Ambient classifier returned non-object JSON from %s", self.cfg.ambient_url)
                return None
            return data
        except Exception as e:
            logger.debug("Ambient classification request to %s failed: %s", self.cfg.ambient_url, e)
            return None

    def _dispatch_ambient_classification(self, frames: List[bytes]) -> None:
        """Sends a captured segment to the ambient classifier on a background worker thread.

        At most one classification request is in flight at a time; segments
        that arrive while one is pending are dropped rather than queued.
        """
        if self.ambient_inflight:
            logger.debug("Ambient classification already in flight; dropping segment.")
            return
        self.ambient_inflight = True
        t = threading.Thread(target=self._ambient_worker, args=(list(frames),), daemon=True)
        t.start()

    def _ambient_worker(self, frames: List[bytes]) -> None:
        """Classifies a segment and, on engage, runs the same hub interaction a wake word would."""
        try:
            self.last_wake_eval_ms = 0.0
            wav_bytes = self._frames_to_wav_bytes(frames)
            result = self._classify_ambient(wav_bytes)
            if not result or result.get("engage") is not True:
                if result:
                    logger.debug(
                        "Ambient classifier declined segment (score=%s, classifier=%s): '%s'",
                        result.get("score"),
                        result.get("classifier"),
                        result.get("transcript"),
                    )
                return

            # Claim the daemon before anything else, so a wake word ("both"
            # mode) can't start a second interaction in the gap before the
            # hub worker thread runs. If a wake-word turn already holds it,
            # that turn wins and this segment is dropped.
            with self.busy_lock:
                if self.busy or self.state == "listening":
                    logger.info(
                        "Ambient classifier engaged but an interaction is already in progress; dropping segment: '%s'",
                        result.get("transcript"),
                    )
                    return
                self.busy = True

            self.last_wake_eval_ms = 0.0
            logger.info(
                "Ambient classifier engaged (score=%s, classifier=%s): '%s'",
                result.get("score"),
                result.get("classifier"),
                result.get("transcript"),
            )
            # Only relay a state once we know we're engaging - not for every
            # ambient segment - to avoid flashing the HUD all day.
            post_voice_state(self.cfg.mirrormere_url, "listening")

            dispatched = False
            try:
                if self.save_utterance(frames) and self.cfg.hub_url:
                    # Same code path a wake word triggers today, on the same WAV.
                    self.dispatch_hub_interaction(self.cfg.save_path)
                    dispatched = True
            finally:
                if not dispatched:
                    # Nothing will clear busy for us: release it here.
                    self.busy = False
                    post_voice_state(self.cfg.mirrormere_url, "idle")
        finally:
            self.ambient_inflight = False

    def _start_pcm_stream(self, sample_rate: int = 24000, channels: int = 1) -> Optional[subprocess.Popen]:
        """Spawns a persistent player subprocess for streaming raw PCM audio."""
        player = shutil.which("pw-play") or shutil.which("aplay") or shutil.which("mpv")
        if not player:
            logger.warning("No audio player found for PCM stream")
            return None
        # Playback buffer for the persistent PCM player. The aplay default
        # (~0.5 s) underruns at sentence starts on slow speakers.
        try:
            buffer_us = int(os.environ.get("EAR_PCM_BUFFER_US", "800000"))
        except ValueError:
            buffer_us = 800000
        if buffer_us <= 0:
            buffer_us = 800000
        if "pw-play" in player:
            cmd = [
                player, "--format=s16", f"--rate={sample_rate}", f"--channels={channels}",
                f"--latency={max(1, buffer_us // 1000)}ms", "-",
            ]
        elif "aplay" in player:
            cmd = [player, "-f", "S16_LE", "-r", str(sample_rate), "-c", str(channels), "-B", str(buffer_us), "-"]
        else:
            cmd = [
                player,
                "--demuxer-rawaudio-format=s16le",
                f"--demuxer-rawaudio-rate={sample_rate}",
                f"--demuxer-rawaudio-channels={channels}",
                "-",
            ]
        try:
            return subprocess.Popen(cmd, stdin=subprocess.PIPE, stderr=subprocess.DEVNULL)
        except Exception as e:
            logger.warning("Failed to start PCM player subprocess (%s): %s", player, e)
            return None

    def play_audio(self, audio_data: bytes) -> bool:
        """Plays audio bytes (MP3 or WAV) using a local audio player."""
        if not audio_data:
            return False
        t0 = time.perf_counter()
        try:
            player = shutil.which("pw-play") or shutil.which("aplay") or shutil.which("mpv")
            if player:
                p = subprocess.Popen([player, "-"], stdin=subprocess.PIPE, stderr=subprocess.DEVNULL)
                p.communicate(input=audio_data, timeout=30)
                self.last_playback_sec = time.perf_counter() - t0
                return p.returncode == 0
        except Exception as e:
            logger.debug("Audio playback failed: %s", e)
        return False

    def dispatch_hub_interaction(self, wav_path: str) -> None:
        """Dispatches hub streaming interaction on a background worker thread."""
        t = threading.Thread(target=self._hub_stream_worker, args=(wav_path,), daemon=True)
        t.start()
        self.current_worker = t

    def _hub_stream_worker(self, wav_path: str) -> None:
        """Consumes Server-Sent Events stream from Voice Hub (POST /api/voice/interact)."""
        if not os.path.exists(wav_path):
            self.busy = False
            self.reply_ended_at = time.time()
            post_voice_state(self.cfg.mirrormere_url, "idle", timeout=2.0)
            return
        # Mark busy for the full interaction (plus cooldown_seconds afterwards)
        # so ambient mode never sends the daemon's own reply audio back to
        # the classifier.
        self.busy = True
        pcm_proc = None
        turn_playback_start = None
        turn_playback_total = 0.0
        reply_received = False
        reply_text = ""

        def close_pcm_proc():
            nonlocal pcm_proc, turn_playback_start
            if pcm_proc:
                try:
                    if pcm_proc.stdin:
                        try:
                            pcm_proc.stdin.close()
                        except Exception:
                            pass
                    pcm_proc.wait(timeout=5.0)
                except subprocess.TimeoutExpired:
                    logger.warning("PCM player timed out waiting for drain; killing process")
                    try:
                        pcm_proc.kill()
                        pcm_proc.wait(timeout=2.0)
                    except Exception:
                        pass
                except Exception as e:
                    logger.debug("PCM player wait error: %s", e)
                finally:
                    if turn_playback_start is not None:
                        self.last_playback_sec = max(0.0, time.perf_counter() - turn_playback_start)
                    pcm_proc = None

        try:
            with open(wav_path, "rb") as f:
                wav_data = f.read()

            boundary = f"----VoiceHubBoundary{uuid.uuid4().hex}"
            body = bytearray()

            def add_field(name: str, val: str) -> None:
                body.extend(f"--{boundary}\r\n".encode("utf-8"))
                body.extend(f'Content-Disposition: form-data; name="{name}"\r\n\r\n'.encode("utf-8"))
                body.extend(f"{val}\r\n".encode("utf-8"))

            add_field("node_id", self.cfg.node_id)
            add_field("wake_eval_ms", f"{self.last_wake_eval_ms:.1f}")
            add_field("speech_duration_ms", f"{self.last_speech_duration_ms:.1f}")
            add_field("silence_duration_ms", f"{self.last_silence_duration_ms:.1f}")

            body.extend(f"--{boundary}\r\n".encode("utf-8"))
            body.extend(b'Content-Disposition: form-data; name="audio"; filename="utterance.wav"\r\n')
            body.extend(b"Content-Type: audio/wav\r\n\r\n")
            body.extend(wav_data)
            body.extend(b"\r\n")
            body.extend(f"--{boundary}--\r\n".encode("utf-8"))

            req = urllib.request.Request(
                self.cfg.hub_url,
                data=bytes(body),
                headers={
                    "Content-Type": f"multipart/form-data; boundary={boundary}",
                    "Accept": "text/event-stream, application/json",
                },
                method="POST",
            )
            logger.info("Streaming utterance to Voice Hub at %s...", self.cfg.hub_url)
            with urllib.request.urlopen(req, timeout=120.0) as resp:
                current_event = None
                for raw_line in resp:
                    line = raw_line.decode("utf-8").strip()
                    if not line:
                        continue
                    if line.startswith("event:"):
                        current_event = line.split(":", 1)[1].strip()
                    elif line.startswith("data:"):
                        data_str = line.split(":", 1)[1].strip()
                        try:
                            payload = json.loads(data_str)
                        except Exception:
                            payload = {}

                        if current_event == "transcript":
                            transcript = payload.get("transcript", "")
                            logger.info("Received transcript: '%s'", transcript)
                        elif current_event == "thinking":
                            elapsed = payload.get("elapsed_seconds")
                            logger.debug("Received thinking keep-alive from Hub (elapsed: %ss)", elapsed)
                        elif current_event == "status":
                            status_text = payload.get("status", "")
                            logger.info("Agent tool status: %s", status_text)
                        elif current_event == "reply":
                            reply_received = True
                            reply_text = payload.get("reply", "")
                            logger.info("Received reply: '%s'", reply_text)
                        elif current_event == "audio_chunk":
                            fmt = (payload.get("format") or "wav").lower()
                            audio_b64 = payload.get("data", "")
                            is_final = payload.get("is_final") is True
                            audio_bytes = b""
                            if audio_b64:
                                try:
                                    audio_bytes = base64.b64decode(audio_b64)
                                except Exception as e:
                                    logger.warning("Failed to decode audio chunk: %s", e)

                            if fmt == "pcm":
                                if audio_bytes:
                                    if pcm_proc is None:
                                        sample_rate = int(payload.get("sample_rate") or 24000)
                                        channels = int(payload.get("channels") or 1)
                                        pcm_proc = self._start_pcm_stream(sample_rate, channels)
                                        turn_playback_start = time.perf_counter()
                                    if pcm_proc and pcm_proc.stdin:
                                        try:
                                            pcm_proc.stdin.write(audio_bytes)
                                            pcm_proc.stdin.flush()
                                        except Exception as e:
                                            logger.warning("Failed to write audio to PCM stream: %s", e)
                                if is_final:
                                    close_pcm_proc()
                            else:
                                if audio_bytes:
                                    if turn_playback_start is None:
                                        turn_playback_start = time.perf_counter()
                                    t0 = time.perf_counter()
                                    self.play_audio(audio_bytes)
                                    turn_playback_total += (time.perf_counter() - t0)
                                    self.last_playback_sec = turn_playback_total
                        elif current_event == "done":
                            logger.info("Hub interaction complete.")
                            break
                        elif current_event == "error":
                            logger.warning("Hub returned error: %s", payload.get("error"))
                            break
        except Exception as e:
            logger.info("Voice Hub stream connection failed: %s", e)
        finally:
            close_pcm_proc()
            self.busy = False
            self.reply_ended_at = time.time()
            self.reset_wake_engine()
            self.pre_roll.clear()
            if reply_received and turn_playback_start is None:
                # Text-only reply (quiet hours or disabled TTS): hold caption toast
                # on screen so user can read it, but allow new wake-words to interrupt.
                words = len(reply_text.split())
                reading_delay = min(max(float(words) * 0.3, 5.0), 15.0)
                time.sleep(reading_delay)
            # Only post idle if a new wake word has not already started a new turn
            if self.state == "idle":
                post_voice_state(self.cfg.mirrormere_url, "idle", timeout=2.0)

    def _send_heartbeat(self, hub_url: str, payload: Dict[str, Any]) -> None:
        """Dispatches heartbeat payload to Voice Hub asynchronously."""
        try:
            post_voice_heartbeat(
                hub_url=hub_url,
                node_id=payload.get("node_id", self.cfg.node_id),
                db=payload.get("ambient_rms_dbfs", 0.0),
                false_wakes=payload.get("false_wakes", 0),
                last_playback_sec=payload.get("last_playback_sec", 0.0),
                timeout=2.0,
            )
        except (urllib.error.URLError, Exception) as e:
            logger.warning("Heartbeat background dispatch failed: %s", e)

    def post_voice_heartbeat(
        self,
        hub_url: str,
        node_id: str,
        db: float,
        false_wakes: int = 0,
        last_playback_sec: float = 0.0,
        timeout: float = 2.0,
    ) -> bool:
        """Helper method to invoke post_voice_heartbeat."""
        return post_voice_heartbeat(
            hub_url=hub_url,
            node_id=node_id,
            db=db,
            false_wakes=false_wakes,
            last_playback_sec=last_playback_sec,
            timeout=timeout,
        )

    def run(self, audio_source=None) -> None:
        """Starts main daemon audio capture and processing loop."""
        signal.signal(signal.SIGINT, self.stop)
        signal.signal(signal.SIGTERM, self.stop)

        post_voice_state(self.cfg.mirrormere_url, "idle")
        logger.info(
            "Starting audio capture from device '%s' (threshold=%.2f)...",
            self.cfg.audio_device,
            self.cfg.threshold,
        )

        chunk_bytes = self.cfg.chunk_samples * 2
        proc = None
        restart_delay = 1.0
        last_heartbeat = time.time()
        _touch_heartbeat()

        try:
            while self.running:
                if audio_source is not None:
                    raw = audio_source.read(chunk_bytes)
                else:
                    if proc is None or proc.poll() is not None:
                        if proc is not None:
                            exit_code = proc.poll()
                            stderr_out = ""
                            if proc.stderr:
                                try:
                                    stderr_out = proc.stderr.read().decode("utf-8", errors="replace")
                                except Exception:
                                    pass
                            logger.warning(
                                "arecord exited with code %s (stderr: '%s'). Retrying in %.1fs...",
                                exit_code,
                                stderr_out.strip(),
                                restart_delay,
                            )
                            time.sleep(restart_delay)
                            restart_delay = min(restart_delay * 2.0, 30.0)

                        cmd = [
                            "arecord",
                            "-D", self.cfg.audio_device,
                            "-f", "S16_LE",
                            "-r", str(self.cfg.sample_rate),
                            "-c", "1",
                            "-t", "raw",
                        ]
                        proc = subprocess.Popen(
                            cmd,
                            stdout=subprocess.PIPE,
                            stderr=subprocess.PIPE,
                        )
                        restart_delay = 1.0

                    raw = proc.stdout.read(chunk_bytes)

                if not raw or len(raw) < chunk_bytes:
                    if audio_source is not None:
                        break
                    continue

                self.process_frame(raw)

                # 10s heartbeat logging and telemetry emission
                if time.time() - last_heartbeat > 10.0:
                    db = self.compute_db(raw)
                    scores_str = ", ".join(f"{k}={v:.3f}" for k, v in self.max_seen.items())
                    vad_mean_ms, vad_count = self.speech_detector.drain_inference_stats()
                    vad_str = f" | VAD infer: {vad_mean_ms:.2f}ms avg (n={vad_count})" if vad_count else ""
                    logger.info("[Heartbeat] RMS: %.1f dBFS | Max scores: %s%s", db, scores_str, vad_str)
                    self.max_seen = {m: 0.0 for m in self.active_models}
                    last_heartbeat = time.time()
                    _touch_heartbeat()

                    if self.cfg.hub_url:
                        payload = {
                            "node_id": self.cfg.node_id,
                            "ambient_rms_dbfs": round(db, 1),
                            "false_wakes": self.false_wakes_count,
                            "last_playback_sec": round(self.last_playback_sec, 2),
                        }
                        self.false_wakes_count = 0
                        self.last_playback_sec = 0.0
                        threading.Thread(
                            target=self._send_heartbeat,
                            args=(self.cfg.hub_url, payload),
                            daemon=True,
                        ).start()

        finally:
            if proc:
                try:
                    proc.terminate()
                    proc.wait(timeout=2.0)
                except Exception:
                    try:
                        proc.kill()
                    except Exception:
                        pass
            post_voice_state(self.cfg.mirrormere_url, "idle")
            logger.info("Voice daemon shutdown complete.")


def main():
    """CLI entrypoint."""
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s [%(levelname)s] %(message)s",
        datefmt="%Y-%m-%d %H:%M:%S",
    )
    cfg = load_config()
    daemon = VoiceDaemon(cfg)
    daemon.run()


if __name__ == "__main__":
    main()

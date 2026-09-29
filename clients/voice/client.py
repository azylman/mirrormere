"""Mirrormere Edge Voice Daemon (SPEC-011)

Listens to microphone input (ALSA / PipeWire), performs local wake word detection
via openWakeWord, relays interaction states to Mirrormere Core HUD, and streams
captured speech utterances to the LAN Voice Hub.

Optionally runs in "ambient" or "both" wake mode (see `wake_mode` in
`config.py`): the existing energy VAD cuts every speech segment and posts it
to a classifier-gated `ambient_url` endpoint instead of (or alongside) the
openWakeWord wake phrase. See SPEC-011 "Ambient (Classifier-Gated) Wake Mode".
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
except (ImportError, ValueError):
    from config import VoiceConfig, load_config

logger = logging.getLogger("mirrormere-voice")


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

        if model is not None:
            self.model = model
            self.active_models = list(cfg.wake_models)
        elif cfg.wake_mode == "ambient":
            # Ambient-only mode never needs openWakeWord: skip loading its
            # models entirely to keep CPU usage low.
            self.model = None
            self.active_models = []
        else:
            self.model, self.active_models = self._init_openwakeword()

        for m in self.active_models:
            self.max_seen[m] = 0.0

    def _init_openwakeword(self):
        """Discovers custom models and instantiates the openWakeWord Model."""
        from openwakeword.model import Model

        custom_models = []
        if os.path.exists(self.cfg.models_dir):
            custom_models = (
                glob.glob(os.path.join(self.cfg.models_dir, "*.onnx"))
                + glob.glob(os.path.join(self.cfg.models_dir, "*.tflite"))
            )

        if custom_models:
            logger.info("Discovered custom wake models: %s", custom_models)
            model = Model(wakeword_model_paths=custom_models, vad_threshold=0.5)
        else:
            model = Model(vad_threshold=0.5)

        available = list(model.models.keys())
        logger.info("Loaded openWakeWord models: %s", available)

        configured_targets = self.cfg.wake_models
        active = [m for m in available if any(tgt in m for tgt in configured_targets)]
        if not active:
            active = available

        logger.info("Active wake word triggers: %s (threshold=%.2f)", active, self.cfg.threshold)
        return model, active

    def stop(self, signum=None, frame=None) -> None:
        """Signal handler to stop main loop gracefully."""
        logger.info("Shutdown signal received, stopping voice daemon...")
        self.running = False

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

    def wake_mode_uses_openwakeword(self) -> bool:
        return self.cfg.wake_mode in ("openwakeword", "both") and self.model is not None

    def wake_mode_uses_ambient(self) -> bool:
        return self.cfg.wake_mode in ("ambient", "both")

    def _check_wake_word(self, raw_bytes: bytes) -> Optional[str]:
        """Runs one openWakeWord prediction pass and returns the triggered model name, if any."""
        chunk = np.frombuffer(raw_bytes, dtype=np.int16) if np is not None else raw_bytes
        preds = self.model.predict(chunk)
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

    def _ambient_ready(self, now: float) -> bool:
        """True when it's safe to start/send an ambient segment: not mid-reply, not in the post-reply cooldown."""
        if self.busy:
            return False
        if now < self.reply_ended_at + self.cfg.cooldown_seconds:
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

    def process_frame(self, raw_bytes: bytes) -> Optional[str]:
        """Processes a single audio frame (1280 samples = 80ms @ 16kHz)."""
        now = time.time()

        if self.state == "idle":
            self.pre_roll.append(raw_bytes)

            if self.wake_mode_uses_openwakeword() and now >= self.cooldown_until:
                if self._check_wake_word(raw_bytes):
                    self._start_listening(now)
                    return "wake_detected"

            if self.wake_mode_uses_ambient() and self._ambient_ready(now):
                db = self.compute_db(raw_bytes)
                if db > self.cfg.speech_threshold_db:
                    self._start_ambient_segment(now)
                    return "ambient_segment_started"

            return None

        elif self.state == "ambient_listening":
            if self.wake_mode_uses_openwakeword() and self._check_wake_word(raw_bytes):
                # Wake word fired mid-segment ("both" mode): hand off to the
                # normal wake-triggered flow and discard the ambient buffer
                # so this segment never reaches the classifier.
                self._reset_ambient_state()
                self._start_listening(now)
                return "wake_detected"

            self.ambient_buffer.append(raw_bytes)
            elapsed = now - (self.ambient_record_start or now)
            db = self.compute_db(raw_bytes)
            is_speech = db > self.cfg.speech_threshold_db

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
            db = self.compute_db(raw_bytes)

            is_speech = db > self.cfg.speech_threshold_db
            if is_speech:
                self.has_spoken = True
                self.silence_start = None
            else:
                if self.silence_start is None:
                    self.silence_start = now

            # False wake abort: If wake fired but zero speech occurred within 3.0s, abort back to idle
            if not self.has_spoken and elapsed >= 3.0:
                logger.info("False wake trigger (no speech detected after 3.0s). Aborting to idle...")
                self.flush_openwakeword()
                self.cooldown_until = now + 1.0
                self.state = "idle"
                post_voice_state(self.cfg.mirrormere_url, "idle")
                self.utterance_buffer = []
                return "wake_aborted"

            silence_duration = (now - self.silence_start) if self.silence_start else 0.0
            silence_limit = self.cfg.silence_ms / 1000.0

            if (self.has_spoken and silence_duration >= silence_limit) or elapsed >= self.cfg.max_record_seconds:
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

                self.flush_openwakeword()
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

    def play_audio(self, audio_data: bytes) -> bool:
        """Plays audio bytes (MP3 or WAV) using a local audio player."""
        if not audio_data:
            return False
        try:
            player = shutil.which("pw-play") or shutil.which("aplay") or shutil.which("mpv")
            if player:
                p = subprocess.Popen([player, "-"], stdin=subprocess.PIPE, stderr=subprocess.DEVNULL)
                p.communicate(input=audio_data, timeout=30)
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
            return
        # Mark busy for the full interaction (plus cooldown_seconds afterwards)
        # so ambient mode never sends the daemon's own reply audio back to
        # the classifier.
        self.busy = True
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
            with urllib.request.urlopen(req, timeout=30.0) as resp:
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
                        elif current_event == "status":
                            status_text = payload.get("status", "")
                            logger.info("Agent tool status: %s", status_text)
                        elif current_event == "reply":
                            reply = payload.get("reply", "")
                            logger.info("Received reply: '%s'", reply)
                        elif current_event == "audio_chunk":
                            audio_b64 = payload.get("data", "")
                            if audio_b64:
                                try:
                                    audio_bytes = base64.b64decode(audio_b64)
                                    self.play_audio(audio_bytes)
                                except Exception as e:
                                    logger.warning("Failed to decode/play audio chunk: %s", e)
                        elif current_event == "done":
                            logger.info("Hub interaction complete.")
                            break
                        elif current_event == "error":
                            logger.warning("Hub returned error: %s", payload.get("error"))
                            break
        except Exception as e:
            logger.info("Voice Hub stream connection failed: %s", e)
        finally:
            self.busy = False
            self.reply_ended_at = time.time()

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

                # 10s heartbeat logging
                if time.time() - last_heartbeat > 10.0:
                    db = self.compute_db(raw)
                    scores_str = ", ".join(f"{k}={v:.3f}" for k, v in self.max_seen.items())
                    logger.info("[Heartbeat] RMS: %.1f dBFS | Max scores: %s", db, scores_str)
                    self.max_seen = {m: 0.0 for m in self.active_models}
                    last_heartbeat = time.time()

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

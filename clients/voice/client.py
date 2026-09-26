"""Mirrormere Edge Voice Daemon (SPEC-011)

Listens to microphone input (ALSA / PipeWire), performs local wake word detection
via openWakeWord, relays interaction states to Mirrormere Core HUD, and streams
captured speech utterances to the LAN Voice Hub.
"""

from collections import deque
import glob
import json
import logging
import math
import os
import signal
import struct
import subprocess
import threading
import time
import urllib.error
import urllib.request
import uuid
import wave
from typing import Any, List, Optional

try:
    import numpy as np
except ImportError:
    np = None

from .config import VoiceConfig, load_config

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

        if model is not None:
            self.model = model
            self.active_models = list(cfg.wake_models)
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

    def process_frame(self, raw_bytes: bytes) -> Optional[str]:
        """Processes a single audio frame (1280 samples = 80ms @ 16kHz)."""
        now = time.time()

        if self.state == "idle":
            self.pre_roll.append(raw_bytes)
            if now < self.cooldown_until:
                return None

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

            if triggered_model:
                self.state = "listening"
                post_voice_state(self.cfg.mirrormere_url, "listening")
                # Prepend pre-roll buffer so starting speech phonemes are never clipped
                self.utterance_buffer = list(self.pre_roll)
                self.record_start = now
                self.silence_start = None
                self.has_spoken = False
                return "wake_detected"

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
                    "Utterance complete (%.2fs, silence=%.2fs). Transitioning to transcribing...",
                    elapsed,
                    silence_duration,
                )
                self.state = "transcribing"
                post_voice_state(self.cfg.mirrormere_url, "transcribing")

                self.save_utterance(self.utterance_buffer)
                if self.cfg.hub_url:
                    self.dispatch_hub_interaction(self.cfg.save_path)

                self.flush_openwakeword()
                self.cooldown_until = time.time() + self.cfg.cooldown_seconds
                self.max_seen = {m: 0.0 for m in self.active_models}
                self.state = "idle"
                post_voice_state(self.cfg.mirrormere_url, "idle")
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

    def dispatch_hub_interaction(self, wav_path: str) -> None:
        """Dispatches hub streaming interaction on a background worker thread."""
        t = threading.Thread(target=self._hub_stream_worker, args=(wav_path,), daemon=True)
        t.start()
        self.current_worker = t

    def _hub_stream_worker(self, wav_path: str) -> None:
        """Consumes Server-Sent Events stream from Voice Hub (POST /api/voice/interact)."""
        if not os.path.exists(wav_path):
            return
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
            add_field("session_id", str(int(time.time())))

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
                            post_voice_state(self.cfg.mirrormere_url, "thinking", transcript=transcript)
                        elif current_event == "thinking":
                            logger.debug("Hub thinking heartbeat pulse received.")
                        elif current_event == "reply":
                            reply = payload.get("reply", "")
                            engine = payload.get("tts_engine", "kokoro")
                            logger.info("Received reply: '%s'", reply)
                            post_voice_state(
                                self.cfg.mirrormere_url,
                                "speaking",
                                reply=reply,
                                tts_engine=engine,
                            )
                        elif current_event == "done":
                            logger.info("Hub interaction complete.")
                            post_voice_state(self.cfg.mirrormere_url, "idle")
                            break
                        elif current_event == "error":
                            logger.warning("Hub returned error: %s", payload.get("error"))
                            post_voice_state(self.cfg.mirrormere_url, "error")
                            break
        except Exception as e:
            logger.info("Voice Hub stream connection failed: %s", e)

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

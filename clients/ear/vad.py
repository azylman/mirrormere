"""Speech/silence detectors for end-of-speech (VAD) gating.

Two implementations behind one small interface:

- ``EnergyDetector``: the original dBFS energy gate (``speech_threshold_db``).
  Unchanged behaviour, kept as the default so existing deployments are
  unaffected.
- ``SileroDetector``: wraps Silero VAD (a small recurrent ONNX model) for a
  neural speech/non-speech decision. Silero's ONNX model is already bundled
  with openWakeWord (``openwakeword`` is a pinned dependency of this client,
  used for wake word detection) and openWakeWord already loads it via
  ``openwakeword.vad.VAD`` to screen its own wake-word predictions. This
  module reuses that same bundled ONNX file so no new dependency is added.

  A *separate* ``openwakeword.vad.VAD()`` instance is created for
  end-of-speech detection rather than reusing the instance openWakeWord's
  ``Model`` builds for itself (``Model(vad_threshold=...)`` creates
  ``self.vad`` internally when ``vad_threshold > 0``). Silero VAD is
  recurrent (LSTM hidden/cell state carried frame to frame): openWakeWord's
  own instance is fed during wake-word evaluation (idle-state frames, in
  ``wake_mode`` "openwakeword"/"both"), and this module's instance is fed
  during utterance/ambient-segment recording (listening / ambient_listening
  state frames). In "both" mode those two phases interleave on the same
  daemon, and this module resets its detector's state at the start of every
  new utterance or ambient segment (see ``SileroDetector.reset``) — sharing
  a single VAD instance for both roles would mean that reset also
  corrupting openWakeWord's own gating state (and vice versa: openWakeWord
  never resets ``self.vad`` on its own ``Model.reset()``, so the shared
  state would go stale). A second instance is a small onnxruntime session
  over a ~1-2MB model, cheap enough on a Pi 4 that this is the simpler and
  safer choice over trying to synchronize one shared instance's state
  across both roles.
"""

import logging
import os
import time
from typing import List, Optional, Tuple

try:
    import numpy as np
except ImportError:
    np = None

logger = logging.getLogger("mirrormere-voice")

# Silero VAD (as shipped by openWakeWord) operates on 16kHz PCM in fixed
# 512-sample (32ms) windows.
SILERO_WINDOW_SAMPLES = 512


class SpeechDetector:
    """Common interface for the two speech/silence decision strategies."""

    def is_speech(self, raw_bytes: bytes) -> bool:
        raise NotImplementedError

    def reset(self) -> None:
        """Called at the start of a new utterance/segment."""

    def drain_inference_stats(self) -> Tuple[float, int]:
        """Returns (mean_inference_ms, sample_count) since the last drain, and resets it.

        Energy detection has no inference cost worth tracking, so the base
        implementation (and EnergyDetector) always reports (0.0, 0).
        """
        return 0.0, 0


class EnergyDetector(SpeechDetector):
    """The original dBFS energy gate. Byte-for-byte the prior behaviour."""

    def __init__(self, speech_threshold_db: float, compute_db) -> None:
        self._speech_threshold_db = speech_threshold_db
        self._compute_db = compute_db

    def is_speech(self, raw_bytes: bytes) -> bool:
        return self._compute_db(raw_bytes) > self._speech_threshold_db


class SileroDetector(SpeechDetector):
    """Silero VAD end-of-speech detection.

    The ear's audio frames are 1280 samples (80ms @ 16kHz); Silero's window
    is 512 samples, so each frame is re-sliced into as many whole 512-sample
    windows as it contains, carrying the remainder across frames. A frame
    counts as speech if any window scored within it is >= vad_threshold.
    """

    def __init__(self, vad_model, threshold: float) -> None:
        self._vad = vad_model
        self._threshold = threshold
        self._remainder = np.array([], dtype=np.int16) if np is not None else []
        self._infer_ms_sum = 0.0
        self._infer_count = 0

    def reset(self) -> None:
        self._remainder = np.array([], dtype=np.int16) if np is not None else []
        if hasattr(self._vad, "reset_states"):
            self._vad.reset_states()

    def is_speech(self, raw_bytes: bytes) -> bool:
        if np is None:
            return False

        chunk = np.frombuffer(raw_bytes, dtype=np.int16)
        samples = np.concatenate([self._remainder, chunk]) if len(self._remainder) else chunk

        n_windows = len(samples) // SILERO_WINDOW_SAMPLES
        speech = False
        for i in range(n_windows):
            window = samples[i * SILERO_WINDOW_SAMPLES:(i + 1) * SILERO_WINDOW_SAMPLES]
            t0 = time.perf_counter()
            score = float(self._vad.predict(window, frame_size=SILERO_WINDOW_SAMPLES))
            self._infer_ms_sum += (time.perf_counter() - t0) * 1000.0
            self._infer_count += 1
            if score >= self._threshold:
                speech = True

        self._remainder = samples[n_windows * SILERO_WINDOW_SAMPLES:]
        return speech

    def drain_inference_stats(self) -> Tuple[float, int]:
        count = self._infer_count
        mean_ms = (self._infer_ms_sum / count) if count else 0.0
        self._infer_ms_sum = 0.0
        self._infer_count = 0
        return mean_ms, count


class SherpaSileroDetector(SpeechDetector):
    """Silero VAD end-of-speech detection using sherpa-onnx.

    Converts raw 16kHz int16 PCM into float32 normalized to [-1.0, 1.0], passes
    to sherpa-onnx VoiceActivityDetector, and drains internal detected segments
    to prevent memory growth.
    """

    def __init__(self, vad_model) -> None:
        self._vad = vad_model
        self._infer_ms_sum = 0.0
        self._infer_count = 0

    def reset(self) -> None:
        if hasattr(self._vad, "clear"):
            self._vad.clear()
        elif hasattr(self._vad, "reset"):
            self._vad.reset()

    def is_speech(self, raw_bytes: bytes) -> bool:
        if not raw_bytes:
            return False

        if np is not None:
            chunk = np.frombuffer(raw_bytes, dtype=np.int16).astype(np.float32) / 32768.0
        else:
            import array
            import struct
            n_samples = len(raw_bytes) // 2
            samples = struct.unpack(f"<{n_samples}h", raw_bytes)
            chunk = array.array("f", (s / 32768.0 for s in samples))

        t0 = time.perf_counter()
        self._vad.accept_waveform(chunk)
        speech = self._vad.is_speech_detected()
        self._infer_ms_sum += (time.perf_counter() - t0) * 1000.0
        self._infer_count += 1

        # Drain detected segment queue to eliminate memory leak
        while hasattr(self._vad, "empty") and not self._vad.empty():
            self._vad.pop()

        return speech

    def drain_inference_stats(self) -> Tuple[float, int]:
        count = self._infer_count
        mean_ms = (self._infer_ms_sum / count) if count else 0.0
        self._infer_ms_sum = 0.0
        self._infer_count = 0
        return mean_ms, count


def load_silero_vad_model():
    """Loads a fresh ``openwakeword.vad.VAD`` instance from openWakeWord's bundled ONNX model.

    Raises whatever openwakeword/onnxruntime raise on failure (import error,
    missing model file, onnxruntime session errors, ...).
    """
    from openwakeword.vad import VAD

    return VAD()


def load_sherpa_silero_vad(sherpa_model_dir: str, vad_threshold: float):
    """Loads a fresh ``sherpa_onnx.VoiceActivityDetector`` using silero_vad.onnx.

    Raises RuntimeError if sherpa_onnx is not installed or model file is missing/unreadable.
    """
    try:
        import sherpa_onnx
    except ImportError as e:
        raise RuntimeError(f"sherpa_onnx is not installed: {e}") from e

    model_path = os.path.join(sherpa_model_dir, "silero_vad.onnx")
    if not os.path.exists(model_path) or not os.access(model_path, os.R_OK):
        raise RuntimeError(f"Sherpa Silero VAD model missing or unreadable at {model_path}")

    try:
        config = sherpa_onnx.VadModelConfig(
            silero_vad=sherpa_onnx.SileroVadModelConfig(
                model=model_path,
                threshold=vad_threshold,
                min_silence_duration=0.05,
                min_speech_duration=0.1,
            ),
            sample_rate=16000,
        )
        return sherpa_onnx.VoiceActivityDetector(config, buffer_size_in_seconds=5)
    except Exception as e:
        raise RuntimeError(f"Failed to initialize Sherpa Silero VAD: {e}") from e


def build_detector(
    vad_mode: str,
    vad_threshold: float,
    speech_threshold_db: float,
    compute_db,
    wake_engine: str = "openwakeword",
    sherpa_model_dir: str = "/opt/mirrormere/voice/sherpa",
) -> SpeechDetector:
    """Builds the configured SpeechDetector.

    If vad_mode == "silero", loads the appropriate model for the active wake engine
    and raises RuntimeError if dependencies or model files cannot be loaded.
    """
    if vad_mode == "silero":
        if wake_engine == "openwakeword":
            try:
                vad_model = load_silero_vad_model()
                logger.info("VAD: silero (openwakeword, threshold=%.2f)", vad_threshold)
                return SileroDetector(vad_model, vad_threshold)
            except Exception as e:
                raise RuntimeError(
                    f"Silero VAD failed to load for openwakeword: {e}"
                ) from e
        elif wake_engine == "sherpa-onnx":
            try:
                vad_model = load_sherpa_silero_vad(sherpa_model_dir, vad_threshold)
                logger.info("VAD: silero (sherpa-onnx, threshold=%.2f)", vad_threshold)
                return SherpaSileroDetector(vad_model)
            except Exception as e:
                raise RuntimeError(
                    f"Silero VAD failed to load for sherpa-onnx: {e}"
                ) from e
        else:
            raise RuntimeError(f"Unknown wake engine '{wake_engine}' for silero VAD")

    logger.info("VAD: energy (threshold=%.1f dBFS)", speech_threshold_db)
    return EnergyDetector(speech_threshold_db, compute_db)


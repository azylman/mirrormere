import struct
import unittest
from unittest.mock import MagicMock, patch

try:
    import numpy  # noqa: F401
    HAS_NUMPY = True
except ImportError:
    HAS_NUMPY = False

from clients.ear.vad import (
    EnergyDetector,
    SileroDetector,
    SherpaSileroDetector,
    build_detector,
    SILERO_WINDOW_SAMPLES,
)
from clients.ear.client import VoiceDaemon


def _frame(amplitude: int, n_samples: int = 1280) -> bytes:
    return struct.pack(f"<{n_samples}h", *([amplitude] * n_samples))


class MockSileroVAD:
    """Deterministic stand-in for openwakeword.vad.VAD."""

    def __init__(self, score_sequence=None):
        self.score_sequence = list(score_sequence) if score_sequence else []
        self.reset_calls = 0
        self.predict_calls = []

    def predict(self, window, frame_size=None):
        self.predict_calls.append((len(window), frame_size))
        if self.score_sequence:
            return self.score_sequence.pop(0)
        return 0.0

    def reset_states(self, batch_size=1):
        self.reset_calls += 1


class TestEnergyDetector(unittest.TestCase):
    def test_matches_original_threshold_semantics(self):
        det = EnergyDetector(speech_threshold_db=-30.0, compute_db=VoiceDaemon.compute_db)
        loud = _frame(10000)
        silent = _frame(0)
        self.assertTrue(det.is_speech(loud))
        self.assertFalse(det.is_speech(silent))

    def test_reset_and_drain_are_no_ops(self):
        det = EnergyDetector(speech_threshold_db=-30.0, compute_db=VoiceDaemon.compute_db)
        det.reset()  # must not raise
        mean_ms, count = det.drain_inference_stats()
        self.assertEqual(mean_ms, 0.0)
        self.assertEqual(count, 0)


# SileroDetector needs numpy for window slicing, and so does Silero itself
# (openwakeword depends on numpy), so it can never run without it. CI's
# system Python has no numpy; skip there rather than test a path that
# cannot exist in production.
@unittest.skipUnless(HAS_NUMPY, "numpy not installed")
class TestSileroDetector(unittest.TestCase):
    def test_frame_resliced_into_512_sample_windows_with_remainder_carried(self):
        mock_vad = MockSileroVAD(score_sequence=[0.0] * 10)
        det = SileroDetector(mock_vad, threshold=0.5)

        # 1280 samples -> 2 whole 512-sample windows this call, 256 left over.
        det.is_speech(_frame(100, n_samples=1280))
        self.assertEqual(len(mock_vad.predict_calls), 2)
        for n, frame_size in mock_vad.predict_calls:
            self.assertEqual(n, SILERO_WINDOW_SAMPLES)
            self.assertEqual(frame_size, SILERO_WINDOW_SAMPLES)

        # Next call: 256 carried + 1280 new = 1536 -> 3 whole windows, 0 left.
        mock_vad.predict_calls.clear()
        det.is_speech(_frame(100, n_samples=1280))
        self.assertEqual(len(mock_vad.predict_calls), 3)

    def test_threshold_behavior(self):
        mock_vad = MockSileroVAD(score_sequence=[0.9, 0.1])
        det = SileroDetector(mock_vad, threshold=0.5)
        self.assertTrue(det.is_speech(_frame(100, n_samples=1280)))

        mock_vad2 = MockSileroVAD(score_sequence=[0.1, 0.2])
        det2 = SileroDetector(mock_vad2, threshold=0.5)
        self.assertFalse(det2.is_speech(_frame(100, n_samples=1280)))

    def test_reset_resets_remainder_and_model_state(self):
        mock_vad = MockSileroVAD(score_sequence=[0.0])
        det = SileroDetector(mock_vad, threshold=0.5)

        # Leave a remainder behind.
        det.is_speech(_frame(100, n_samples=600))
        self.assertEqual(len(det._remainder), 600 - SILERO_WINDOW_SAMPLES)

        det.reset()
        self.assertEqual(len(det._remainder), 0)
        self.assertEqual(mock_vad.reset_calls, 1)

    def test_inference_stats_drain_and_reset(self):
        mock_vad = MockSileroVAD(score_sequence=[0.0] * 10)
        det = SileroDetector(mock_vad, threshold=0.5)
        det.is_speech(_frame(100, n_samples=1280))

        mean_ms, count = det.drain_inference_stats()
        self.assertGreaterEqual(mean_ms, 0.0)
        self.assertEqual(count, 2)

        # A second drain with no intervening calls reports nothing.
        mean_ms2, count2 = det.drain_inference_stats()
        self.assertEqual(mean_ms2, 0.0)
        self.assertEqual(count2, 0)


class MockSherpaVAD:
    """Deterministic stand-in for sherpa_onnx.VoiceActivityDetector."""

    def __init__(self, speech_detected: bool = False):
        self._speech_detected = speech_detected
        self.accept_calls = []
        self.clear_calls = 0
        self.queue = []

    def accept_waveform(self, samples):
        self.accept_calls.append(samples)

    def is_speech_detected(self) -> bool:
        return self._speech_detected

    def empty(self) -> bool:
        return len(self.queue) == 0

    def pop(self):
        if self.queue:
            return self.queue.pop(0)

    def clear(self):
        self.clear_calls += 1
        self.queue.clear()


@unittest.skipUnless(HAS_NUMPY, "numpy not installed")
class TestSherpaSileroDetector(unittest.TestCase):
    def test_float32_normalization_and_speech_detection(self):
        mock_vad = MockSherpaVAD(speech_detected=True)
        mock_vad.queue = ["seg1", "seg2"]
        det = SherpaSileroDetector(mock_vad)

        raw_frame = _frame(16384, n_samples=1280)
        is_speech = det.is_speech(raw_frame)
        self.assertTrue(is_speech)
        self.assertEqual(len(mock_vad.accept_calls), 1)

        samples = mock_vad.accept_calls[0]
        # Must be float32 normalized [-1.0, 1.0]
        self.assertEqual(samples.dtype, numpy.float32)
        self.assertAlmostEqual(float(samples[0]), 0.5, places=2)
        # Segments queue must be drained
        self.assertEqual(len(mock_vad.queue), 0)

    def test_reset_clears_vad(self):
        mock_vad = MockSherpaVAD()
        det = SherpaSileroDetector(mock_vad)
        det.reset()
        self.assertEqual(mock_vad.clear_calls, 1)

    def test_drain_inference_stats(self):
        mock_vad = MockSherpaVAD()
        det = SherpaSileroDetector(mock_vad)
        det.is_speech(_frame(100, n_samples=1280))
        mean_ms, count = det.drain_inference_stats()
        self.assertGreaterEqual(mean_ms, 0.0)
        self.assertEqual(count, 1)


class TestBuildDetector(unittest.TestCase):
    def test_energy_mode_builds_energy_detector(self):
        det = build_detector("energy", 0.5, -31.0, VoiceDaemon.compute_db)
        self.assertIsInstance(det, EnergyDetector)

    @patch("clients.ear.vad.load_silero_vad_model")
    def test_silero_mode_builds_silero_detector(self, mock_load):
        mock_load.return_value = MockSileroVAD()
        det = build_detector("silero", 0.5, -31.0, VoiceDaemon.compute_db, wake_engine="openwakeword")
        self.assertIsInstance(det, SileroDetector)

    @patch("clients.ear.vad.load_silero_vad_model", side_effect=RuntimeError("onnx load failed"))
    def test_silero_load_failure_raises_runtime_error_for_oww(self, mock_load):
        with self.assertRaises(RuntimeError) as ctx:
            build_detector("silero", 0.5, -31.0, VoiceDaemon.compute_db, wake_engine="openwakeword")
        self.assertIn("Silero VAD failed to load for openwakeword", str(ctx.exception))

    @patch("clients.ear.vad.load_sherpa_silero_vad")
    def test_sherpa_silero_mode_builds_sherpa_detector(self, mock_load):
        mock_load.return_value = MockSherpaVAD()
        det = build_detector("silero", 0.5, -31.0, VoiceDaemon.compute_db, wake_engine="sherpa-onnx")
        self.assertIsInstance(det, SherpaSileroDetector)

    @patch("clients.ear.vad.load_sherpa_silero_vad", side_effect=RuntimeError("missing silero_vad.onnx"))
    def test_sherpa_silero_load_failure_raises_runtime_error(self, mock_load):
        with self.assertRaises(RuntimeError) as ctx:
            build_detector("silero", 0.5, -31.0, VoiceDaemon.compute_db, wake_engine="sherpa-onnx")
        self.assertIn("Silero VAD failed to load for sherpa-onnx", str(ctx.exception))

    def test_load_sherpa_silero_vad_invokes_correct_classes(self):
        import os
        import tempfile
        import shutil
        tmp_dir = tempfile.mkdtemp()
        try:
            model_path = os.path.join(tmp_dir, "silero_vad.onnx")
            with open(model_path, "w") as f:
                f.write("mock")
            mock_sherpa = MagicMock()
            mock_detector = MagicMock()
            mock_sherpa.VoiceActivityDetector.return_value = mock_detector
            mock_vad_config = MagicMock()
            mock_sherpa.VadModelConfig.return_value = mock_vad_config
            mock_silero_config = MagicMock()
            mock_sherpa.SileroVadModelConfig.return_value = mock_silero_config

            with patch.dict("sys.modules", {"sherpa_onnx": mock_sherpa}):
                from clients.ear.vad import load_sherpa_silero_vad
                det = load_sherpa_silero_vad(tmp_dir, 0.5)
                self.assertEqual(det, mock_detector)
                mock_sherpa.VadModelConfig.assert_called_once()
                self.assertEqual(mock_sherpa.VadModelConfig.call_args[1]["sample_rate"], 16000)
                mock_sherpa.VoiceActivityDetector.assert_called_once_with(mock_vad_config, buffer_size_in_seconds=5)
        finally:
            shutil.rmtree(tmp_dir, ignore_errors=True)



class TestVoiceDaemonAmbientNoSpeechNotSent(unittest.TestCase):
    """Ambient segments with no speech, in silero mode, are never dispatched."""

    @patch("clients.ear.client.urllib.request.urlopen")
    def test_ambient_segment_with_no_silero_speech_is_dropped(self, mock_urlopen):
        import tempfile
        import os
        import time
        from clients.ear.config import VoiceConfig
        from unittest.mock import MagicMock as MM

        mock_resp = MM()
        mock_resp.status = 200
        mock_urlopen.return_value.__enter__.return_value = mock_resp

        temp_dir = tempfile.mkdtemp()
        save_path = os.path.join(temp_dir, "u.wav")
        cfg = VoiceConfig(
            save_path=save_path,
            wake_mode="ambient",
            vad="silero",
            silence_ms=50,
            ambient_url="http://test-ambient/classify",
        )

        # openwakeword.vad.VAD never gets a real speech score here: everything
        # reads as non-speech, so no segment should ever start.
        with patch("clients.ear.vad.load_silero_vad_model", return_value=MockSileroVAD()):
            daemon = VoiceDaemon(cfg)

        silent_frame = _frame(0)
        with patch.object(daemon, "_dispatch_ambient_classification") as mock_dispatch:
            for _ in range(5):
                daemon.process_frame(silent_frame)
            self.assertEqual(daemon.state, "idle")
            mock_dispatch.assert_not_called()

        if os.path.exists(save_path):
            os.unlink(save_path)
        os.rmdir(temp_dir)


if __name__ == "__main__":
    unittest.main()

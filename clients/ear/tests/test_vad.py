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


class TestBuildDetector(unittest.TestCase):
    def test_energy_mode_builds_energy_detector(self):
        det = build_detector("energy", 0.5, -31.0, VoiceDaemon.compute_db)
        self.assertIsInstance(det, EnergyDetector)

    @patch("clients.ear.vad.load_silero_vad_model")
    def test_silero_mode_builds_silero_detector(self, mock_load):
        mock_load.return_value = MockSileroVAD()
        det = build_detector("silero", 0.5, -31.0, VoiceDaemon.compute_db)
        self.assertIsInstance(det, SileroDetector)

    @patch("clients.ear.vad.load_silero_vad_model", side_effect=RuntimeError("onnx load failed"))
    def test_silero_load_failure_falls_back_to_energy(self, mock_load):
        det = build_detector("silero", 0.5, -31.0, VoiceDaemon.compute_db)
        self.assertIsInstance(det, EnergyDetector)


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

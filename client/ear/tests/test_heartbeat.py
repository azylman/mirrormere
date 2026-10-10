import io
import os
import tempfile
import time
import unittest
from unittest.mock import patch, MagicMock

from client.ear.client import _touch_heartbeat, HEARTBEAT_FILE, VoiceDaemon
from client.ear.config import VoiceConfig


class TestHeartbeat(unittest.TestCase):
    def test_touch_heartbeat_default_and_custom_path(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            target = os.path.join(tmpdir, "test.heartbeat")
            t_before = int(time.time())
            _touch_heartbeat(target)
            t_after = int(time.time())

            self.assertTrue(os.path.exists(target))
            with open(target, "r", encoding="utf-8") as f:
                content = int(f.read().strip())
            self.assertTrue(t_before <= content <= t_after)

    def test_touch_heartbeat_env_override(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            target = os.path.join(tmpdir, "env.heartbeat")
            with patch.dict(os.environ, {"MIRRORMERE_HEARTBEAT_FILE": target}):
                _touch_heartbeat()
                self.assertTrue(os.path.exists(target))
                with open(target, "r", encoding="utf-8") as f:
                    content = int(f.read().strip())
                self.assertTrue(content > 0)

    def test_touch_heartbeat_unwritable_path_graceful(self):
        # Should gracefully catch exceptions and not crash
        _touch_heartbeat("/root/nonexistent_dir/forbidden.heartbeat")

    @patch("client.ear.client.build_detector")
    def test_daemon_touches_heartbeat_in_run(self, mock_build_detector):
        mock_detector = MagicMock()
        mock_detector.drain_inference_stats.return_value = (1.5, 10)
        mock_build_detector.return_value = mock_detector

        mock_model = MagicMock()
        mock_model.prediction_buffer = {}
        mock_model.predict.return_value = {}

        cfg = VoiceConfig(chunk_samples=160, sample_rate=16000, silence_ms=200, cooldown_seconds=0.1)
        daemon = VoiceDaemon(cfg, model=mock_model)

        with tempfile.TemporaryDirectory() as tmpdir:
            target = os.path.join(tmpdir, "daemon.heartbeat")
            with patch.dict(os.environ, {"MIRRORMERE_HEARTBEAT_FILE": target}):
                # Provide 2 chunks of silence then EOF
                dummy_pcm = b"\x00" * (160 * 2 * 2)
                audio_stream = io.BytesIO(dummy_pcm)

                daemon.run(audio_source=audio_stream)

                self.assertTrue(os.path.exists(target))
                with open(target, "r", encoding="utf-8") as f:
                    ts = int(f.read().strip())
                self.assertTrue(ts > 0)


if __name__ == "__main__":
    unittest.main()

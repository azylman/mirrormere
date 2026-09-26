import os
import tempfile
import unittest
from clients.voice.config import VoiceConfig, load_config


class TestVoiceConfig(unittest.TestCase):
    def test_default_config(self):
        cfg = VoiceConfig()
        self.assertEqual(cfg.node_id, "touch-kiosk-kitchen")
        self.assertEqual(cfg.threshold, 0.35)
        self.assertEqual(cfg.silence_ms, 800)
        self.assertEqual(cfg.sample_rate, 16000)
        self.assertEqual(cfg.chunk_samples, 1280)
        self.assertIn("hey_jarvis", cfg.wake_models)

    def test_load_from_yaml(self):
        yaml_content = """
voice:
  node_id: "test-node"
  threshold: 0.45
  silence_ms: 1200
  speech_threshold_db: -28.0
  wake_models:
    - "hey_aerial"
"""
        with tempfile.NamedTemporaryFile("w", suffix=".yaml", delete=False) as f:
            f.write(yaml_content)
            temp_path = f.name

        try:
            cfg = load_config(temp_path)
            self.assertEqual(cfg.node_id, "test-node")
            self.assertEqual(cfg.threshold, 0.45)
            self.assertEqual(cfg.silence_ms, 1200)
            self.assertEqual(cfg.speech_threshold_db, -28.0)
            self.assertEqual(cfg.wake_models, ["hey_aerial"])
        finally:
            if os.path.exists(temp_path):
                os.unlink(temp_path)

    def test_load_nonexistent_file(self):
        cfg = load_config("/path/to/nonexistent/voice.yaml")
        self.assertEqual(cfg.node_id, "touch-kiosk-kitchen")
        self.assertEqual(cfg.threshold, 0.35)

    def test_env_overrides(self):
        os.environ["MIRRORMERE_NODE_ID"] = "env-node-1"
        os.environ["MIRRORMERE_URL"] = "http://env-host/kiosk"
        os.environ["MIRRORMERE_HUB_URL"] = "http://env-hub:9000/api/voice/interact"
        os.environ["MIRRORMERE_AUDIO_DEVICE"] = "hw:1,0"

        try:
            cfg = load_config("/path/to/nonexistent.yaml")
            self.assertEqual(cfg.node_id, "env-node-1")
            self.assertEqual(cfg.mirrormere_url, "http://env-host/kiosk")
            self.assertEqual(cfg.hub_url, "http://env-hub:9000/api/voice/interact")
            self.assertEqual(cfg.audio_device, "hw:1,0")
        finally:
            del os.environ["MIRRORMERE_NODE_ID"]
            del os.environ["MIRRORMERE_URL"]
            del os.environ["MIRRORMERE_HUB_URL"]
            del os.environ["MIRRORMERE_AUDIO_DEVICE"]


if __name__ == "__main__":
    unittest.main()

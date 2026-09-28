import os
import tempfile
import unittest
from clients.voice.config import VoiceConfig, load_config


class TestVoiceConfig(unittest.TestCase):
    def test_default_config(self):
        cfg = VoiceConfig()
        self.assertEqual(cfg.device_name, "kitchen-display")
        self.assertEqual(cfg.node_id, "kitchen-display")
        self.assertEqual(cfg.threshold, 0.35)
        self.assertEqual(cfg.silence_ms, 800)
        self.assertEqual(cfg.sample_rate, 16000)
        self.assertEqual(cfg.chunk_samples, 1280)
        self.assertIn("hey_jarvis", cfg.wake_models)

    def test_load_from_yaml(self):
        yaml_content = """
voice:
  device_name: "test-device"
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
            self.assertEqual(cfg.device_name, "test-device")
            self.assertEqual(cfg.node_id, "test-device")
            self.assertEqual(cfg.threshold, 0.45)
            self.assertEqual(cfg.silence_ms, 1200)
            self.assertEqual(cfg.speech_threshold_db, -28.0)
            self.assertEqual(cfg.wake_models, ["hey_aerial"])
        finally:
            if os.path.exists(temp_path):
                os.unlink(temp_path)

    def test_load_legacy_node_id_from_yaml(self):
        yaml_content = """
voice:
  node_id: "legacy-node"
"""
        with tempfile.NamedTemporaryFile("w", suffix=".yaml", delete=False) as f:
            f.write(yaml_content)
            temp_path = f.name

        try:
            cfg = load_config(temp_path)
            self.assertEqual(cfg.device_name, "legacy-node")
            self.assertEqual(cfg.node_id, "legacy-node")
        finally:
            if os.path.exists(temp_path):
                os.unlink(temp_path)

    def test_load_nonexistent_file(self):
        cfg = load_config("/path/to/nonexistent/voice.yaml")
        self.assertEqual(cfg.device_name, "kitchen-display")
        self.assertEqual(cfg.node_id, "kitchen-display")
        self.assertEqual(cfg.threshold, 0.35)

    def test_env_overrides(self):
        os.environ["MIRRORMERE_DEVICE_NAME"] = "env-device-1"
        os.environ["MIRRORMERE_URL"] = "http://env-host/kiosk"
        os.environ["MIRRORMERE_HUB_URL"] = "http://env-hub:9000/api/voice/interact"
        os.environ["MIRRORMERE_AUDIO_DEVICE"] = "hw:1,0"

        try:
            cfg = load_config("/path/to/nonexistent.yaml")
            self.assertEqual(cfg.device_name, "env-device-1")
            self.assertEqual(cfg.node_id, "env-device-1")
            self.assertEqual(cfg.mirrormere_url, "http://env-host/kiosk")
            self.assertEqual(cfg.hub_url, "http://env-hub:9000/api/voice/interact")
            self.assertEqual(cfg.audio_device, "hw:1,0")
        finally:
            del os.environ["MIRRORMERE_DEVICE_NAME"]
            del os.environ["MIRRORMERE_URL"]
            del os.environ["MIRRORMERE_HUB_URL"]
            del os.environ["MIRRORMERE_AUDIO_DEVICE"]

    def test_env_override_node_id(self):
        os.environ["MIRRORMERE_NODE_ID"] = "env-node-legacy"
        try:
            cfg = load_config("/path/to/nonexistent.yaml")
            self.assertEqual(cfg.device_name, "env-node-legacy")
            self.assertEqual(cfg.node_id, "env-node-legacy")
        finally:
            del os.environ["MIRRORMERE_NODE_ID"]


if __name__ == "__main__":
    unittest.main()

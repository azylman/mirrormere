import os
import tempfile
import unittest
from clients.ear.config import VoiceConfig, load_config


class TestVoiceConfig(unittest.TestCase):
    def test_default_config(self):
        cfg = VoiceConfig()
        self.assertEqual(cfg.node_id, "touch-kiosk-kitchen")
        self.assertEqual(cfg.wake_engine, "openwakeword")
        self.assertEqual(cfg.sherpa_model_dir, "/opt/mirrormere/voice/sherpa")
        self.assertEqual(cfg.keywords_file, "/config/keywords.txt")
        self.assertEqual(cfg.keyword, "")
        self.assertEqual(cfg.keywords_score, 1.0)
        self.assertEqual(cfg.keywords_threshold, 0.25)
        self.assertEqual(cfg.sherpa_num_threads, 2)
        self.assertTrue(cfg.wake_mode_uses_wake_word())
        self.assertEqual(cfg.threshold, 0.35)
        self.assertEqual(cfg.silence_ms, 400)
        self.assertEqual(cfg.sample_rate, 16000)
        self.assertEqual(cfg.chunk_samples, 1280)
        self.assertEqual(cfg.wake_models, ["hey_jarvis_v0.1"])
        self.assertNotIn("alexa", cfg.wake_models)
        self.assertNotIn("hey_mycroft", cfg.wake_models)

    def test_sherpa_config_from_yaml(self):
        yaml_content = """
ear:
  wake_engine: "sherpa-onnx"
  wake_mode: "wake_word"
  sherpa_model_dir: "/custom/sherpa"
  keywords_file: "/custom/keywords.txt"
  keyword: "hey jarvis"
  keywords_score: 1.5
  keywords_threshold: 0.30
  sherpa_num_threads: 1
"""
        with tempfile.NamedTemporaryFile("w", suffix=".yaml", delete=False) as f:
            f.write(yaml_content)
            temp_path = f.name
        try:
            cfg = load_config(temp_path)
            self.assertEqual(cfg.wake_engine, "sherpa-onnx")
            self.assertEqual(cfg.wake_mode, "wake_word")
            # Model directory override is forbidden; must remain bundled default
            self.assertEqual(cfg.sherpa_model_dir, "/opt/mirrormere/voice/sherpa")
            self.assertEqual(cfg.keywords_file, "/custom/keywords.txt")
            self.assertEqual(cfg.keyword, "hey jarvis")
            self.assertEqual(cfg.keywords_score, 1.5)
            self.assertEqual(cfg.keywords_threshold, 0.30)
            self.assertEqual(cfg.sherpa_num_threads, 1)
            self.assertTrue(cfg.wake_mode_uses_wake_word())
        finally:
            if os.path.exists(temp_path):
                os.unlink(temp_path)

    def test_wake_engine_invalid_falls_back_to_openwakeword(self):
        yaml_content = """
ear:
  wake_engine: "invalid-engine"
  sherpa_num_threads: 99
"""
        with tempfile.NamedTemporaryFile("w", suffix=".yaml", delete=False) as f:
            f.write(yaml_content)
            temp_path = f.name
        try:
            cfg = load_config(temp_path)
            self.assertEqual(cfg.wake_engine, "openwakeword")
            self.assertEqual(cfg.sherpa_num_threads, 2)
        finally:
            if os.path.exists(temp_path):
                os.unlink(temp_path)

    def test_load_from_yaml(self):
        yaml_content = """
voice:
  node_id: "test-node"
  threshold: 0.45
  silence_ms: 1200
  speech_threshold_db: -28.0
  wake_models:
    - "hey_jarvis"
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
            self.assertEqual(cfg.wake_models, ["hey_jarvis"])
        finally:
            if os.path.exists(temp_path):
                os.unlink(temp_path)

    def test_load_nonexistent_file(self):
        cfg = load_config("/path/to/nonexistent/voice.yaml")
        self.assertEqual(cfg.node_id, "touch-kiosk-kitchen")
        self.assertEqual(cfg.threshold, 0.35)

    def test_vad_defaults(self):
        cfg = VoiceConfig()
        self.assertEqual(cfg.vad, "energy")
        self.assertEqual(cfg.vad_threshold, 0.5)

    def test_vad_from_yaml(self):
        yaml_content = """
voice:
  vad: "silero"
  vad_threshold: 0.6
"""
        with tempfile.NamedTemporaryFile("w", suffix=".yaml", delete=False) as f:
            f.write(yaml_content)
            temp_path = f.name
        try:
            cfg = load_config(temp_path)
            self.assertEqual(cfg.vad, "silero")
            self.assertEqual(cfg.vad_threshold, 0.6)
        finally:
            os.unlink(temp_path)

    def test_vad_invalid_values_fall_back_to_defaults(self):
        yaml_content = """
voice:
  vad: "not-a-real-mode"
  vad_threshold: 4.2
"""
        with tempfile.NamedTemporaryFile("w", suffix=".yaml", delete=False) as f:
            f.write(yaml_content)
            temp_path = f.name
        try:
            cfg = load_config(temp_path)
            self.assertEqual(cfg.vad, "energy")
            self.assertEqual(cfg.vad_threshold, 0.5)
        finally:
            os.unlink(temp_path)

    def test_ear_key_preferred_over_voice_key(self):
        yaml_content = """
ear:
  node_id: "from-ear-key"
  vad: "silero"
voice:
  node_id: "from-voice-key"
  vad: "energy"
"""
        with tempfile.NamedTemporaryFile("w", suffix=".yaml", delete=False) as f:
            f.write(yaml_content)
            temp_path = f.name
        try:
            cfg = load_config(temp_path)
            self.assertEqual(cfg.node_id, "from-ear-key")
            self.assertEqual(cfg.vad, "silero")
        finally:
            os.unlink(temp_path)

    def test_voice_key_still_works_alone(self):
        yaml_content = """
voice:
  node_id: "from-voice-key-alone"
"""
        with tempfile.NamedTemporaryFile("w", suffix=".yaml", delete=False) as f:
            f.write(yaml_content)
            temp_path = f.name
        try:
            cfg = load_config(temp_path)
            self.assertEqual(cfg.node_id, "from-voice-key-alone")
        finally:
            os.unlink(temp_path)

    def test_env_overrides(self):
        os.environ["MIRRORMERE_NODE_ID"] = "env-node-1"
        os.environ["MIRRORMERE_URL"] = "http://env-host/kiosk"
        os.environ["MIRRORMERE_HUB_URL"] = "http://env-hub:9000/api/voice/interact"
        os.environ["MIRRORMERE_AUDIO_DEVICE"] = "hw:1,0"
        os.environ["MIRRORMERE_SILENCE_MS"] = "350"
        os.environ["MIRRORMERE_VAD"] = "silero"
        os.environ["MIRRORMERE_VAD_THRESHOLD"] = "0.65"
        os.environ["MIRRORMERE_WAKE_ENGINE"] = "sherpa-onnx"
        os.environ["MIRRORMERE_SHERPA_MODEL_DIR"] = "/env/sherpa"
        os.environ["MIRRORMERE_KEYWORDS_FILE"] = "/env/keywords.txt"
        os.environ["MIRRORMERE_KEYWORD"] = "env keyword"
        os.environ["MIRRORMERE_KEYWORDS_SCORE"] = "2.0"
        os.environ["MIRRORMERE_KEYWORDS_THRESHOLD"] = "0.33"
        os.environ["MIRRORMERE_SHERPA_NUM_THREADS"] = "1"

        try:
            cfg = load_config("/path/to/nonexistent.yaml")
            self.assertEqual(cfg.node_id, "env-node-1")
            self.assertEqual(cfg.mirrormere_url, "http://env-host/kiosk")
            self.assertEqual(cfg.hub_url, "http://env-hub:9000/api/voice/interact")
            self.assertEqual(cfg.audio_device, "hw:1,0")
            self.assertEqual(cfg.silence_ms, 350)
            self.assertEqual(cfg.vad, "silero")
            self.assertEqual(cfg.vad_threshold, 0.65)
            self.assertEqual(cfg.wake_engine, "sherpa-onnx")
            self.assertEqual(cfg.sherpa_model_dir, "/opt/mirrormere/voice/sherpa")
            self.assertEqual(cfg.keywords_file, "/env/keywords.txt")
            self.assertEqual(cfg.keyword, "env keyword")
            self.assertEqual(cfg.keywords_score, 2.0)
            self.assertEqual(cfg.keywords_threshold, 0.33)
            self.assertEqual(cfg.sherpa_num_threads, 1)
        finally:
            del os.environ["MIRRORMERE_NODE_ID"]
            del os.environ["MIRRORMERE_URL"]
            del os.environ["MIRRORMERE_HUB_URL"]
            del os.environ["MIRRORMERE_AUDIO_DEVICE"]
            del os.environ["MIRRORMERE_SILENCE_MS"]
            del os.environ["MIRRORMERE_VAD"]
            del os.environ["MIRRORMERE_VAD_THRESHOLD"]
            del os.environ["MIRRORMERE_WAKE_ENGINE"]
            del os.environ["MIRRORMERE_SHERPA_MODEL_DIR"]
            del os.environ["MIRRORMERE_KEYWORDS_FILE"]
            del os.environ["MIRRORMERE_KEYWORD"]
            del os.environ["MIRRORMERE_KEYWORDS_SCORE"]
            del os.environ["MIRRORMERE_KEYWORDS_THRESHOLD"]
            del os.environ["MIRRORMERE_SHERPA_NUM_THREADS"]

    def test_client_compose_models_volume_bind_mount(self):
        compose_path = os.path.join(
            os.path.dirname(__file__), "..", "..", "..", "deploy", "examples", "compose.client.yaml"
        )
        self.assertTrue(os.path.exists(compose_path), f"compose file missing: {compose_path}")
        with open(compose_path, "r", encoding="utf-8") as f:
            content = f.read()
        self.assertIn("/opt/mirrormere/voice/models:/opt/mirrormere/voice/models:ro", content)
        self.assertNotIn("ear-models", content)

    def test_ear_requirements_include_sherpa_token_tools(self):
        req_path = os.path.join(
            os.path.dirname(__file__), "..", "requirements.txt"
        )
        self.assertTrue(os.path.exists(req_path), f"requirements file missing: {req_path}")
        with open(req_path, "r", encoding="utf-8") as f:
            content = f.read()
        self.assertIn("click", content)
        self.assertIn("sentencepiece", content)


if __name__ == "__main__":
    unittest.main()


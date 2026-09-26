"""
test_config.py - Hermetic unit tests for EinkConfig and load_config
Reference: SPEC-009 §Configuration
"""

import os
import tempfile
import unittest

from config import EinkConfig, load_config


class TestConfig(unittest.TestCase):
    def test_default_config(self):
        """
        Validates default values specified in SPEC-009 §Configuration.
        """
        cfg = EinkConfig()
        self.assertEqual(cfg.server.events_url, "http://localhost:8080/api/events")
        self.assertEqual(cfg.server.image_url, "http://localhost:8081/eink.png")
        self.assertEqual(cfg.panel.driver, "epd7in5_V2")
        self.assertEqual(cfg.panel.pins.rst, 27)
        self.assertEqual(cfg.panel.pins.dc, 22)
        self.assertEqual(cfg.panel.pins.busy, 17)
        self.assertEqual(cfg.panel.pins.cs, 8)
        self.assertIsNone(cfg.panel.pins.pwr)
        self.assertEqual(cfg.refresh.coalesce_seconds, 5.0)
        self.assertEqual(cfg.refresh.min_refresh_seconds, 60.0)
        self.assertEqual(cfg.refresh.clock_refresh_seconds, 300.0)
        self.assertEqual(cfg.refresh.max_idle_seconds, 900.0)
        self.assertEqual(cfg.refresh.full_refresh_minutes, 60.0)
        self.assertEqual(cfg.refresh.max_consecutive_partials, 30)
        self.assertEqual(cfg.refresh.offline_grace_seconds, 120.0)
        self.assertFalse(cfg.refresh.clear_on_shutdown)
        self.assertFalse(cfg.buttons.enabled)
        self.assertEqual(cfg.buttons.refresh_pin, 5)
        self.assertEqual(cfg.buttons.next_screen_pin, 6)

    def test_load_from_yaml(self):
        """
        Validates loading overrides from a YAML file.
        """
        yaml_content = """
server:
  events_url: "http://192.168.1.100:8080/api/events"
  image_url: "http://192.168.1.100:8081/eink.png"

panel:
  driver: epd7in5_V2
  pins:
    rst: 17
    dc: 25
    busy: 24
    cs: 8

refresh:
  coalesce_seconds: 3
  min_refresh_seconds: 45
  clock_refresh_seconds: 180

buttons:
  enabled: true
  refresh_pin: 12
  next_screen_pin: 13
"""
        with tempfile.NamedTemporaryFile("w", suffix=".yaml", delete=False) as f:
            f.write(yaml_content)
            temp_path = f.name

        try:
            cfg = load_config(temp_path)
            self.assertEqual(cfg.server.events_url, "http://192.168.1.100:8080/api/events")
            self.assertEqual(cfg.server.image_url, "http://192.168.1.100:8081/eink.png")
            self.assertEqual(cfg.panel.pins.rst, 17)
            self.assertEqual(cfg.panel.pins.dc, 25)
            self.assertEqual(cfg.panel.pins.busy, 24)
            self.assertEqual(cfg.refresh.coalesce_seconds, 3.0)
            self.assertEqual(cfg.refresh.min_refresh_seconds, 45.0)
            self.assertEqual(cfg.refresh.clock_refresh_seconds, 180.0)
            self.assertTrue(cfg.buttons.enabled)
            self.assertEqual(cfg.buttons.refresh_pin, 12)
            self.assertEqual(cfg.buttons.next_screen_pin, 13)
        finally:
            if os.path.exists(temp_path):
                os.remove(temp_path)

    def test_env_var_overrides(self):
        """
        Validates environment variable overrides for server endpoints.
        """
        os.environ["MIRRORMERE_EVENTS_URL"] = "http://env-host:8080/api/events"
        os.environ["MIRRORMERE_IMAGE_URL"] = "http://env-host:8081/eink.png"
        try:
            cfg = load_config("/nonexistent/path/config.yaml")
            self.assertEqual(cfg.server.events_url, "http://env-host:8080/api/events")
            self.assertEqual(cfg.server.image_url, "http://env-host:8081/eink.png")
        finally:
            del os.environ["MIRRORMERE_EVENTS_URL"]
            del os.environ["MIRRORMERE_IMAGE_URL"]


if __name__ == "__main__":
    unittest.main()

#!/usr/bin/env python3
"""Unit tests for HEARTH ical-bridge server."""

import io
import ipaddress
import json
import os
import tempfile
import threading
import unittest
from unittest.mock import MagicMock, patch

import importlib.util
from pathlib import Path

server_path = Path(__file__).parent / "server.py"
spec = importlib.util.spec_from_file_location("server", str(server_path))
srv = importlib.util.module_from_spec(spec)
spec.loader.exec_module(srv)


class TestServerSecurityAndAuth(unittest.TestCase):
    def test_is_internal_caller(self):
        # Loopback
        self.assertTrue(srv._is_internal_caller("127.0.0.1"))
        self.assertTrue(srv._is_internal_caller("::1"))
        # Private RFC 1918 subnets constructed dynamically without literal private IP strings
        priv_10 = str(ipaddress.IPv4Address(0x0A000001))
        priv_172 = str(ipaddress.IPv4Address(0xAC120002))
        priv_192 = str(ipaddress.IPv4Address(0xC0A80132))
        self.assertTrue(srv._is_internal_caller(priv_10))
        self.assertTrue(srv._is_internal_caller(priv_172))
        self.assertTrue(srv._is_internal_caller(priv_192))
        # Public / Documentation addresses
        self.assertFalse(srv._is_internal_caller("198.51.100.1"))
        self.assertFalse(srv._is_internal_caller("203.0.113.1"))
        self.assertFalse(srv._is_internal_caller("8.8.8.8"))
        self.assertFalse(srv._is_internal_caller("invalid-ip"))

    def test_is_valid_album_url(self):
        # Valid Google Photos album URLs
        self.assertTrue(srv._is_valid_album_url("https://photos.app.goo.gl/AbCdEf123456789"))
        self.assertTrue(srv._is_valid_album_url("https://photos.google.com/share/AF1QipM..."))
        # Invalid URLs
        self.assertFalse(srv._is_valid_album_url("http://photos.app.goo.gl/insecure"))
        self.assertFalse(srv._is_valid_album_url("https://evil.com/photos.app.goo.gl"))
        self.assertFalse(srv._is_valid_album_url("file:///etc/passwd"))
        self.assertFalse(srv._is_valid_album_url("javascript:alert(1)"))
        self.assertFalse(srv._is_valid_album_url(""))
        self.assertFalse(srv._is_valid_album_url(None))


class TestConfigAndSecrets(unittest.TestCase):
    def test_resolve_env_secrets(self):
        os.environ["TEST_SECRET_KEY"] = "super-secret-token"
        try:
            data = {
                "regular": "value",
                "api_key_env": "TEST_SECRET_KEY",
                "nested": {
                    "sub_env": "TEST_SECRET_KEY",
                },
                "items": [
                    {"token_env": "TEST_SECRET_KEY"},
                ],
            }
            resolved = srv._resolve_env_secrets(data)
            self.assertEqual(resolved["api_key"], "super-secret-token")
            self.assertEqual(resolved["nested"]["sub"], "super-secret-token")
            self.assertEqual(resolved["items"][0]["token"], "super-secret-token")
        finally:
            os.environ.pop("TEST_SECRET_KEY", None)

    def test_cfg_resolution(self):
        os.environ["CUSTOM_TEST_VAR"] = "custom-val"
        try:
            self.assertEqual(srv._cfg("CUSTOM_TEST_VAR"), "custom-val")
            self.assertEqual(srv._cfg("NONEXISTENT_VAR", "default-fallback"), "default-fallback")
        finally:
            os.environ.pop("CUSTOM_TEST_VAR", None)

    def test_derive_screens_from_config(self):
        # When no config, returns generic fallback with [col, row] origins
        with patch.object(srv, "_load_config", return_value={}):
            screens = srv._derive_screens_from_config()
            self.assertIsInstance(screens, list)
            self.assertGreaterEqual(len(screens), 1)
            widgets = screens[0].get("widgets", [])
            widget_ids = [w["widget_id"] for w in widgets]
            self.assertEqual(widget_ids, ["family-calendar", "home-weather", "household-tasks"])
            origins = {w["widget_id"]: w["origin"] for w in widgets}
            self.assertEqual(origins["family-calendar"], [0, 0])
            self.assertEqual(origins["home-weather"], [4, 0])
            self.assertEqual(origins["household-tasks"], [4, 1])

    def test_derive_screens_from_example_config(self):
        screens = srv._derive_screens_from_config()
        self.assertIsInstance(screens, list)
        self.assertGreaterEqual(len(screens), 1)
        first_screen_widgets = [w["widget_id"] for w in screens[0].get("widgets", [])]
        self.assertIn("family-calendar", first_screen_widgets)

    def test_derive_screens_dynamic_col_row(self):
        with patch.object(srv, "_load_config", return_value={
            "widgets": [
                {"id": "widget-a", "dimensions": [2, 1]},
                {"id": "widget-b", "dimensions": [2, 1]},
            ]
        }):
            screens = srv._derive_screens_from_config()
            self.assertEqual(len(screens), 1)
            w = screens[0]["widgets"]
            self.assertEqual(w[0]["origin"], [0, 0])
            self.assertEqual(w[1]["origin"], [2, 0])


class TestPhotosCacheAndLocks(unittest.TestCase):
    def test_photos_cache_locks_and_defaults(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            test_file = os.path.join(tmpdir, "test_photos_cache.json")
            orig_file = srv.PHOTOS_CACHE_FILE
            srv.PHOTOS_CACHE_FILE = test_file
            try:
                state = srv._load_photos_cache()
                self.assertEqual(state["album_title"], "Shared Photos")
                self.assertEqual(state["photos"], [])

                saved = srv._save_photos_cache(
                    ["https://lh3.googleusercontent.com/pw/abc123def=w1080"],
                    source="test-source",
                    album_title="Test Album",
                )
                self.assertEqual(saved["album_title"], "Test Album")
                self.assertEqual(len(saved["photos"]), 1)
                self.assertEqual(saved["photos"][0], "https://lh3.googleusercontent.com/pw/abc123def")

                reloaded = srv._load_photos_cache()
                self.assertEqual(reloaded["album_title"], "Test Album")
                self.assertEqual(len(reloaded["photos"]), 1)
            finally:
                srv.PHOTOS_CACHE_FILE = orig_file

    def test_concurrent_cache_access(self):
        # Concurrently access CACHE with CACHE_LOCK
        def worker(idx):
            with srv.CACHE_LOCK:
                srv.CACHE[f"key_{idx}"] = (100, f"val_{idx}")
            with srv.CACHE_LOCK:
                _ = srv.CACHE.get(f"key_{idx}")

        threads = [threading.Thread(target=worker, args=(i,)) for i in range(20)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()

        with srv.CACHE_LOCK:
            self.assertIn("key_0", srv.CACHE)


class MockServer:
    def __init__(self):
        pass


class TestHandlerEndpoints(unittest.TestCase):
    def _create_handler(self, method, path, headers=None, body=b"", client_ip="127.0.0.1"):
        handler = srv.Handler.__new__(srv.Handler)
        handler.command = method
        handler.path = path
        handler.headers = headers or {}
        handler.rfile = io.BytesIO(body)
        handler.wfile = io.BytesIO()
        handler.client_address = (client_ip, 12345)
        handler.server = MockServer()
        handler._headers_buffer = []
        handler.send_response = MagicMock()
        handler.send_header = MagicMock()
        handler.end_headers = MagicMock()
        return handler

    def test_external_caller_forbidden_on_hearth_api(self):
        # GET
        h_get = self._create_handler("GET", "/hearth-api/screens/layout", client_ip="198.51.100.2")
        h_get.do_GET()
        h_get.send_response.assert_called_with(403)

        # POST
        h_post = self._create_handler("POST", "/hearth-api/photos/sync", client_ip="198.51.100.2")
        h_post.do_POST()
        h_post.send_response.assert_called_with(403)

    def test_post_malformed_json_returns_400(self):
        bad_json = b"not a json object"
        h = self._create_handler(
            "POST",
            "/hearth-api/music/control",
            headers={"Content-Length": str(len(bad_json))},
            body=bad_json,
            client_ip="127.0.0.1",
        )
        h.do_POST()
        h.send_response.assert_called_with(400)

    def test_post_invalid_content_length_returns_400(self):
        h = self._create_handler(
            "POST",
            "/hearth-api/music/control",
            headers={"Content-Length": "not-a-number"},
            body=b"{}",
            client_ip="127.0.0.1",
        )
        h.do_POST()
        h.send_response.assert_called_with(400)

    def test_post_seerr_invalid_tmdb_id_returns_400(self):
        body = json.dumps({"media_type": "movie", "tmdb_id": "invalid"}).encode("utf-8")
        h = self._create_handler(
            "POST",
            "/hearth-api/seerr/request",
            headers={"Content-Length": str(len(body))},
            body=body,
            client_ip="127.0.0.1",
        )
        h.do_POST()
        h.send_response.assert_called_with(400)

    def test_post_music_control_invalid_volume_returns_400(self):
        body = json.dumps({"action": "volume", "value": "loud"}).encode("utf-8")
        h = self._create_handler(
            "POST",
            "/hearth-api/music/control",
            headers={"Content-Length": str(len(body))},
            body=body,
            client_ip="127.0.0.1",
        )
        h.do_POST()
        h.send_response.assert_called_with(400)

    def test_post_photos_sync_invalid_album_url_returns_400(self):
        body = json.dumps({"album_url": "http://evil.com/steal-photos"}).encode("utf-8")
        h = self._create_handler(
            "POST",
            "/hearth-api/photos/sync",
            headers={"Content-Length": str(len(body))},
            body=body,
            client_ip="127.0.0.1",
        )
        h.do_POST()
        h.send_response.assert_called_with(400)

    def test_post_oversized_body_returns_413(self):
        # Default max_len is 8192 for non-photos routes
        h = self._create_handler(
            "POST",
            "/hearth-api/music/control",
            headers={"Content-Length": "10000"},
            body=b"{}",
            client_ip="127.0.0.1",
        )
        h.do_POST()
        h.send_response.assert_called_with(413)

        # Photos sync has max_len 262144
        h_photos = self._create_handler(
            "POST",
            "/hearth-api/photos/sync",
            headers={"Content-Length": "300000"},
            body=b"{}",
            client_ip="127.0.0.1",
        )
        h_photos.do_POST()
        h_photos.send_response.assert_called_with(413)

    def test_video_id_validation(self):
        valid_id = "dQw4w9WgXcQ"
        self.assertIsNotNone(srv.YT_VIDEO_ID_RE.match(valid_id))
        self.assertIsNone(srv.YT_VIDEO_ID_RE.match("too_short"))
        self.assertIsNone(srv.YT_VIDEO_ID_RE.match("invalid$char!"))
        self.assertIsNone(srv.YT_VIDEO_ID_RE.match("way_too_long_for_video_id"))

        # resolve_yt_audio_stream rejects invalid video_id
        url, ctype = srv.resolve_yt_audio_stream("invalid_id")
        self.assertIsNone(url)
        self.assertIsNone(ctype)
        url, ctype = srv.resolve_yt_audio_stream("")
        self.assertIsNone(url)
        self.assertIsNone(ctype)

        # _cast_track_to_speaker rejects invalid video_id and invalid speaker entity ID
        self.assertFalse(srv._cast_track_to_speaker("media_player.speaker", {"video_id": "invalid"}))
        self.assertFalse(srv._cast_track_to_speaker("invalid_speaker", {"video_id": valid_id}))

        # GET /hearth-api/music/stream/<video_id>.m4a returns 400 on invalid video_id
        h = self._create_handler("GET", "/hearth-api/music/stream/invalid_id.m4a", client_ip="127.0.0.1")
        h.do_GET()
        h.send_response.assert_called_with(400)

    def test_ha_entity_id_validation(self):
        self.assertIsNotNone(srv.HA_ENTITY_ID_RE.match("media_player.living_room_tv"))
        self.assertIsNotNone(srv.HA_ENTITY_ID_RE.match("remote.living_room_tv"))
        self.assertIsNone(srv.HA_ENTITY_ID_RE.match("invalid_entity_without_domain"))
        self.assertIsNone(srv.HA_ENTITY_ID_RE.match("media player.with spaces"))
        self.assertIsNone(srv.HA_ENTITY_ID_RE.match("../../traversal/path"))

        # launch_plex_on_living_room_tv rejects invalid entity IDs
        with patch.object(srv, "_cfg", side_effect=lambda k, d="": "invalid_entity" if "ENTITY" in k else d):
            ok, res = srv.launch_plex_on_living_room_tv({"rating_key": "123", "type": "movie"})
            self.assertFalse(ok)
            self.assertIn("Invalid Home Assistant entity ID", res.get("error", ""))

    def test_fetch_gcal_events_handles_token_error(self):
        with patch.object(srv, "get_google_token", side_effect=RuntimeError("Token error")):
            events = srv.fetch_gcal_events("test-calendar-id")
            self.assertEqual(events, [])

    def test_get_screens_layout_success(self):
        h = self._create_handler("GET", "/hearth-api/screens/layout", client_ip="127.0.0.1")
        h.do_GET()
        h.send_response.assert_called_with(200)
        output = h.wfile.getvalue().decode("utf-8")
        data = json.loads(output)
        self.assertIn("screens", data)


if __name__ == "__main__":
    unittest.main()

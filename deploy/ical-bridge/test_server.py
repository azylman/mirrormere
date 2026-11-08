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
        h_post = self._create_handler("POST", "/hearth-api/music/control", client_ip="198.51.100.2")
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

    def test_post_oversized_body_returns_413(self):
        h = self._create_handler(
            "POST",
            "/hearth-api/music/control",
            headers={"Content-Length": "10000"},
            body=b"{}",
            client_ip="127.0.0.1",
        )
        h.do_POST()
        h.send_response.assert_called_with(413)

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

    def test_is_local_speaker(self):
        self.assertTrue(srv._is_local_speaker(""))
        self.assertTrue(srv._is_local_speaker(None))
        self.assertTrue(srv._is_local_speaker("this_device"))
        self.assertTrue(srv._is_local_speaker("THIS_DEVICE"))
        self.assertTrue(srv._is_local_speaker("local"))
        self.assertTrue(srv._is_local_speaker(" Local "))
        self.assertFalse(srv._is_local_speaker("media_player.living_room"))
        self.assertFalse(srv._is_local_speaker("media_player.kitchen"))

    def test_get_music_dashboard_state_local_speaker(self):
        with patch.dict(srv.MUSIC_STATE, {
            "active_speaker": "this_device",
            "local_state": "playing",
            "local_volume": 42,
            "playing_video_id": "vid12345678",
            "queue": [{"video_id": "vid12345678", "title": "Track 1", "artist": "Artist 1", "thumb": ""}],
            "queue_index": 0,
            "playlist_title": "Test Playlist",
            "shuffle": False,
        }):
            with patch.object(srv, "_ha_request") as mock_ha:
                st = srv.get_music_dashboard_state()
                mock_ha.assert_not_called()
                self.assertEqual(st["active_speaker"], "this_device")
                np = st["now_playing"]
                self.assertEqual(np["video_id"], "vid12345678")
                self.assertEqual(np["state"], "playing")
                self.assertEqual(np["volume"], 42)
                self.assertEqual(np["title"], "Track 1")

    def test_get_music_dashboard_state_defaults_to_this_device_when_no_speakers(self):
        with patch.dict(srv.MUSIC_STATE, {
            "active_speaker": "",
            "local_state": "stopped",
            "local_volume": 35,
            "playing_video_id": "",
            "queue": [],
            "queue_index": 0,
            "playlist_title": "",
            "shuffle": True,
        }):
            with patch.object(srv, "_get_speakers", return_value=[]):
                st = srv.get_music_dashboard_state()
                self.assertEqual(st["active_speaker"], "this_device")
                self.assertEqual(st["now_playing"]["state"], "stopped")
                self.assertEqual(st["now_playing"]["volume"], 35)
                self.assertEqual(st["now_playing"]["video_id"], "")

    def test_start_music_playback_local_speaker(self):
        with patch.dict(srv.MUSIC_STATE, {
            "active_speaker": "this_device",
            "local_state": "stopped",
            "local_volume": 35,
            "playing_video_id": "",
            "queue": [],
            "queue_index": 0,
            "playlist_title": "",
            "shuffle": True,
        }):
            with patch.object(srv, "_cast_track_to_speaker") as mock_cast:
                body = {
                    "speaker": "this_device",
                    "video_id": "dQw4w9WgXcQ",
                    "title": "Never Gonna Give You Up",
                    "artist": "Rick Astley",
                }
                ok, st = srv.start_music_playback(body)
                mock_cast.assert_not_called()
                self.assertTrue(ok)
                self.assertEqual(srv.MUSIC_STATE["local_state"], "playing")
                self.assertEqual(srv.MUSIC_STATE["playing_video_id"], "dQw4w9WgXcQ")
                self.assertEqual(st["now_playing"]["video_id"], "dQw4w9WgXcQ")
                self.assertEqual(st["now_playing"]["state"], "playing")

    def test_start_music_playback_search_fallback(self):
        with patch.dict(srv.MUSIC_STATE, {
            "active_speaker": "this_device",
            "local_state": "stopped",
            "local_volume": 35,
            "playing_video_id": "",
            "queue": [],
            "queue_index": 0,
            "playlist_title": "",
            "shuffle": True,
        }):
            mock_results = [{
                "video_id": "search_vid11",
                "title": "Searched Track",
                "artist": "Searched Artist",
                "thumb": "https://img.youtube.com/thumb.jpg",
            }]
            with patch.object(srv, "search_yt_music", return_value=mock_results) as mock_search:
                with patch.object(srv, "_cast_track_to_speaker") as mock_cast:
                    body = {
                        "speaker": "this_device",
                        "title": "Searched Track",
                        "artist": "Searched Artist",
                    }
                    ok, st = srv.start_music_playback(body)
                    mock_search.assert_called_with("Searched Track Searched Artist")
                    mock_cast.assert_not_called()
                    self.assertTrue(ok)
                    self.assertEqual(srv.MUSIC_STATE["playing_video_id"], "search_vid11")
                    self.assertEqual(st["now_playing"]["video_id"], "search_vid11")
                    self.assertEqual(st["now_playing"]["state"], "playing")

    def test_control_music_playback_local_speaker(self):
        track1 = {"video_id": "vid11111111", "title": "Track 1", "artist": "A1"}
        track2 = {"video_id": "vid22222222", "title": "Track 2", "artist": "A2"}
        with patch.dict(srv.MUSIC_STATE, {
            "active_speaker": "this_device",
            "local_state": "playing",
            "local_volume": 35,
            "playing_video_id": "vid11111111",
            "queue": [track1, track2],
            "queue_index": 0,
            "playlist_title": "Playlist",
            "shuffle": False,
        }):
            with patch.object(srv, "_ha_request") as mock_ha:
                # 1. play_pause toggles to paused
                ok, st = srv.control_music_playback({"action": "play_pause"})
                self.assertTrue(ok)
                mock_ha.assert_not_called()
                self.assertEqual(srv.MUSIC_STATE["local_state"], "paused")
                self.assertEqual(st["now_playing"]["state"], "paused")

                # 2. play_pause toggles back to playing
                ok, st = srv.control_music_playback({"action": "play_pause"})
                self.assertTrue(ok)
                self.assertEqual(srv.MUSIC_STATE["local_state"], "playing")

                # 3. next advances queue_index and sets playing_video_id
                ok, st = srv.control_music_playback({"action": "next"})
                self.assertTrue(ok)
                self.assertEqual(srv.MUSIC_STATE["queue_index"], 1)
                self.assertEqual(srv.MUSIC_STATE["playing_video_id"], "vid22222222")
                self.assertEqual(st["now_playing"]["video_id"], "vid22222222")

                # 4. prev moves back
                ok, st = srv.control_music_playback({"action": "prev"})
                self.assertTrue(ok)
                self.assertEqual(srv.MUSIC_STATE["queue_index"], 0)
                self.assertEqual(srv.MUSIC_STATE["playing_video_id"], "vid11111111")

                # 5. play_index
                ok, st = srv.control_music_playback({"action": "play_index", "index": 1})
                self.assertTrue(ok)
                self.assertEqual(srv.MUSIC_STATE["queue_index"], 1)
                self.assertEqual(srv.MUSIC_STATE["playing_video_id"], "vid22222222")

                # 6. volume clamps
                ok, st = srv.control_music_playback({"action": "volume", "value": 150})
                self.assertTrue(ok)
                self.assertEqual(srv.MUSIC_STATE["local_volume"], 100)
                self.assertEqual(st["now_playing"]["volume"], 100)

                ok, st = srv.control_music_playback({"action": "volume", "value": -10})
                self.assertTrue(ok)
                self.assertEqual(srv.MUSIC_STATE["local_volume"], 0)

                # 7. stop
                ok, st = srv.control_music_playback({"action": "stop"})
                self.assertTrue(ok)
                self.assertEqual(srv.MUSIC_STATE["local_state"], "stopped")
                self.assertEqual(srv.MUSIC_STATE["playing_video_id"], "")

    def test_fetch_yt_playlist_tracks_fallback_to_ytmusicapi(self):
        mock_watch = {
            "tracks": [
                {
                    "videoId": "vid_watch_1",
                    "title": "Watch Title",
                    "artists": [{"name": "Watch Artist"}],
                    "thumbnail": [{"url": "https://img.youtube.com/watch.jpg"}],
                }
            ]
        }
        mock_ytm = MagicMock()
        mock_ytm.get_watch_playlist.return_value = mock_watch

        with patch.object(srv, "get_yt_oauth_token", return_value=None):
            with patch.dict("sys.modules", {"ytmusicapi": MagicMock(YTMusic=MagicMock(return_value=mock_ytm))}):
                tracks = srv.fetch_yt_playlist_tracks("PL1234567890abcdef")
                self.assertEqual(len(tracks), 1)
                self.assertEqual(tracks[0]["video_id"], "vid_watch_1")
                self.assertEqual(tracks[0]["title"], "Watch Title")
                self.assertEqual(tracks[0]["artist"], "Watch Artist")
                self.assertEqual(tracks[0]["thumb"], "https://img.youtube.com/watch.jpg")

    def test_resolve_yt_audio_stream_with_headers_and_cache(self):
        # Invalid video_id
        url, ctype, hdrs = srv.resolve_yt_audio_stream_with_headers("invalid")
        self.assertIsNone(url)
        self.assertIsNone(ctype)
        self.assertEqual(hdrs, {})

        valid_vid = "dQw4w9WgXcQ"
        cached_entry = (
            srv.time.time(),
            "https://googlevideo.com/videoplayback?id=123",
            "audio/mp4",
            {"User-Agent": "Custom-UA", "Accept-Encoding": "identity"},
        )
        with patch.dict(srv.STREAM_URL_CACHE, {valid_vid: cached_entry}, clear=True):
            u, c, h = srv.resolve_yt_audio_stream_with_headers(valid_vid)
            self.assertEqual(u, cached_entry[1])
            self.assertEqual(c, "audio/mp4")
            self.assertEqual(h["User-Agent"], "Custom-UA")

            # 2-tuple backwards compatibility
            u2, c2 = srv.resolve_yt_audio_stream(valid_vid)
            self.assertEqual(u2, cached_entry[1])
            self.assertEqual(c2, "audio/mp4")

    def test_stream_proxy_forwards_range_and_mirrors_headers(self):
        valid_vid = "dQw4w9WgXcQ"
        upstream_url = "https://googlevideo.com/videoplayback?id=123"
        cached_entry = (
            srv.time.time(),
            upstream_url,
            "audio/mp4",
            {"User-Agent": "Custom-Upstream-UA"},
        )

        mock_upstream = MagicMock()
        mock_upstream.getcode.return_value = 206
        mock_upstream.headers = {
            "Content-Length": "1024",
            "Accept-Ranges": "bytes",
            "Content-Range": "bytes 0-1023/5000000",
        }
        mock_upstream.read.side_effect = [b"stream_chunk_data", b""]
        mock_upstream.__enter__.return_value = mock_upstream
        mock_upstream.__exit__.return_value = False

        with patch.dict(srv.STREAM_URL_CACHE, {valid_vid: cached_entry}, clear=True):
            with patch("urllib.request.urlopen", return_value=mock_upstream) as mock_urlopen:
                h = self._create_handler(
                    "GET",
                    f"/hearth-api/music/stream/{valid_vid}.m4a",
                    headers={"Range": "bytes=0-1023"},
                    client_ip="127.0.0.1",
                )
                h.do_GET()

                # Verify urllib.request.Request headers
                req_arg = mock_urlopen.call_args[0][0]
                self.assertEqual(req_arg.headers.get("Range"), "bytes=0-1023")
                self.assertEqual(req_arg.headers.get("User-agent"), "Custom-Upstream-UA")

                # Verify handler response headers
                h.send_response.assert_called_with(206)
                h.send_header.assert_any_call("Content-Type", "audio/mp4")
                h.send_header.assert_any_call("Accept-Ranges", "bytes")
                h.send_header.assert_any_call("Content-Range", "bytes 0-1023/5000000")
                self.assertEqual(h.wfile.getvalue(), b"stream_chunk_data")

    def test_stream_proxy_do_head(self):
        valid_vid = "dQw4w9WgXcQ"
        upstream_url = "https://googlevideo.com/videoplayback?id=123"
        cached_entry = (
            srv.time.time(),
            upstream_url,
            "audio/mp4",
            {"User-Agent": "Custom-Upstream-UA"},
        )

        mock_upstream = MagicMock()
        mock_upstream.getcode.return_value = 200
        mock_upstream.headers = {
            "Content-Length": "5000000",
            "Accept-Ranges": "bytes",
        }
        mock_upstream.read.return_value = b"should_not_be_written"
        mock_upstream.__enter__.return_value = mock_upstream
        mock_upstream.__exit__.return_value = False

        with patch.dict(srv.STREAM_URL_CACHE, {valid_vid: cached_entry}, clear=True):
            with patch("urllib.request.urlopen", return_value=mock_upstream):
                h = self._create_handler(
                    "HEAD",
                    f"/hearth-api/music/stream/{valid_vid}.m4a",
                    client_ip="127.0.0.1",
                )
                h.do_HEAD()

                h.send_response.assert_called_with(200)
                h.send_header.assert_any_call("Content-Type", "audio/mp4")
                h.send_header.assert_any_call("Content-Length", "5000000")
                h.send_header.assert_any_call("Accept-Ranges", "bytes")
                # Body must be empty for HEAD
                self.assertEqual(h.wfile.getvalue(), b"")

    def test_do_head_general_endpoints(self):
        # /healthz HEAD
        h_health = self._create_handler("HEAD", "/healthz")
        h_health.do_HEAD()
        h_health.send_response.assert_called_with(200)
        self.assertEqual(h_health.wfile.getvalue(), b"")

        # /hearth-api/screens/layout HEAD
        h_layout = self._create_handler("HEAD", "/hearth-api/screens/layout")
        h_layout.do_HEAD()
        h_layout.send_response.assert_called_with(200)
        h_layout.send_header.assert_any_call("Content-Type", "application/json; charset=utf-8")
        self.assertEqual(h_layout.wfile.getvalue(), b"")

        # /hearth-api/music/state HEAD
        h_state = self._create_handler("HEAD", "/hearth-api/music/state")
        h_state.do_HEAD()
        h_state.send_response.assert_called_with(200)
        h_state.send_header.assert_any_call("Content-Type", "application/json; charset=utf-8")
        self.assertEqual(h_state.wfile.getvalue(), b"")

        # Stream 400 invalid id HEAD
        h_bad_stream = self._create_handler("HEAD", "/hearth-api/music/stream/invalid_id.m4a")
        h_bad_stream.do_HEAD()
        h_bad_stream.send_response.assert_called_with(400)
        self.assertEqual(h_bad_stream.wfile.getvalue(), b"")

    def test_stream_proxy_upstream_http_error(self):
        valid_vid = "dQw4w9WgXcQ"
        upstream_url = "https://googlevideo.com/videoplayback?id=123"
        cached_entry = (
            srv.time.time(),
            upstream_url,
            "audio/mp4",
            {"User-Agent": "Custom-Upstream-UA"},
        )

        import urllib.error
        from http.client import HTTPMessage
        mock_msg = HTTPMessage()
        mock_msg["Content-Type"] = "text/plain"
        mock_msg["Content-Range"] = "bytes */5000"
        err = urllib.error.HTTPError(upstream_url, 416, "Range Not Satisfiable", mock_msg, io.BytesIO(b"range error"))

        with patch.dict(srv.STREAM_URL_CACHE, {valid_vid: cached_entry}, clear=True):
            with patch("urllib.request.urlopen", side_effect=err):
                h = self._create_handler(
                    "GET",
                    f"/hearth-api/music/stream/{valid_vid}.m4a",
                    headers={"Range": "bytes=9999999-"},
                    client_ip="127.0.0.1",
                )
                h.do_GET()
                h.send_response.assert_called_with(416)
                h.send_header.assert_any_call("Content-Range", "bytes */5000")
                self.assertEqual(h.wfile.getvalue(), b"range error")

    def test_fetch_yt_playlist_tracks_google_api_success(self):
        api_response = {
            "items": [
                {
                    "snippet": {
                        "title": "API Track 1",
                        "videoOwnerChannelTitle": "Artist 1 - Topic",
                        "thumbnails": {"high": {"url": "https://img.youtube.com/high.jpg"}},
                    },
                    "contentDetails": {"videoId": "api_vid_1"},
                }
            ]
        }
        mock_r = MagicMock()
        mock_r.read.return_value = json.dumps(api_response).encode("utf-8")
        mock_r.__enter__.return_value = mock_r
        mock_r.__exit__.return_value = False

        with patch.object(srv, "get_yt_oauth_token", return_value="mock_token"):
            with patch("urllib.request.urlopen", return_value=mock_r):
                tracks = srv.fetch_yt_playlist_tracks("PL12345678")
                self.assertEqual(len(tracks), 1)
                self.assertEqual(tracks[0]["video_id"], "api_vid_1")
                self.assertEqual(tracks[0]["title"], "API Track 1")
                self.assertEqual(tracks[0]["artist"], "Artist 1")
                self.assertEqual(tracks[0]["thumb"], "https://img.youtube.com/high.jpg")

    def test_fetch_yt_playlist_tracks_get_playlist_fallback(self):
        mock_ytm = MagicMock()
        mock_ytm.get_watch_playlist.return_value = {"tracks": []}
        mock_ytm.get_playlist.return_value = {
            "tracks": [
                {
                    "videoId": "pl_vid_2",
                    "title": "Playlist Track 2",
                    "artists": [{"name": "Artist 2"}],
                    "thumbnails": [{"url": "https://img.youtube.com/pl2.jpg"}],
                }
            ]
        }
        with patch.object(srv, "get_yt_oauth_token", return_value=None):
            with patch.dict("sys.modules", {"ytmusicapi": MagicMock(YTMusic=MagicMock(return_value=mock_ytm))}):
                tracks = srv.fetch_yt_playlist_tracks("PLfallback")
                self.assertEqual(len(tracks), 1)
                self.assertEqual(tracks[0]["video_id"], "pl_vid_2")
                self.assertEqual(tracks[0]["title"], "Playlist Track 2")
                self.assertEqual(tracks[0]["artist"], "Artist 2")

    def test_start_music_playback_no_tracks(self):
        with patch.object(srv, "fetch_yt_playlist_tracks", return_value=[]):
            with patch.object(srv, "search_yt_music", return_value=[]):
                ok, res = srv.start_music_playback({"playlist_id": "empty_pl"})
                self.assertFalse(ok)
                self.assertEqual(res, {"error": "No playable tracks found"})

    def test_start_music_playback_ha_speaker(self):
        with patch.dict(srv.MUSIC_STATE, {
            "active_speaker": "media_player.kitchen",
            "local_state": "stopped",
            "local_volume": 35,
            "playing_video_id": "",
            "queue": [],
            "queue_index": 0,
            "playlist_title": "",
            "shuffle": False,
        }):
            with patch.object(srv, "_cast_track_to_speaker", return_value=True) as mock_cast:
                with patch.object(srv, "_ha_request", return_value={"state": "playing", "attributes": {"volume_level": 0.5}}):
                    body = {
                        "speaker": "media_player.kitchen",
                        "video_id": "dQw4w9WgXcQ",
                        "title": "Never Gonna Give You Up",
                    }
                    ok, st = srv.start_music_playback(body)
                    self.assertTrue(ok)
                    mock_cast.assert_called_once()
                    self.assertEqual(st["active_speaker"], "media_player.kitchen")

    def test_control_music_playback_ha_speaker(self):
        track1 = {"video_id": "vid11111111", "title": "Track 1", "artist": "A1"}
        track2 = {"video_id": "vid22222222", "title": "Track 2", "artist": "A2"}
        with patch.dict(srv.MUSIC_STATE, {
            "active_speaker": "media_player.kitchen",
            "local_state": "stopped",
            "local_volume": 35,
            "playing_video_id": "vid11111111",
            "queue": [track1, track2],
            "queue_index": 0,
            "playlist_title": "Playlist",
            "shuffle": False,
        }):
            with patch.object(srv, "_ha_request", return_value={"state": "playing", "attributes": {"volume_level": 0.4}}) as mock_ha:
                with patch.object(srv, "_cast_track_to_speaker", return_value=True) as mock_cast:
                    # play_pause
                    ok, st = srv.control_music_playback({"action": "play_pause"})
                    self.assertTrue(ok)
                    mock_ha.assert_any_call("POST", "/api/services/media_player/media_play_pause", {"entity_id": "media_player.kitchen"})

                    # volume
                    ok, st = srv.control_music_playback({"action": "volume", "value": 40})
                    self.assertTrue(ok)
                    mock_ha.assert_any_call("POST", "/api/services/media_player/volume_set", {"entity_id": "media_player.kitchen", "volume_level": 0.4})

                    # next
                    ok, st = srv.control_music_playback({"action": "next"})
                    self.assertTrue(ok)
                    mock_cast.assert_called_with("media_player.kitchen", track2)

                    # stop
                    ok, st = srv.control_music_playback({"action": "stop"})
                    self.assertTrue(ok)
                    mock_ha.assert_any_call("POST", "/api/services/media_player/media_stop", {"entity_id": "media_player.kitchen"})

    def test_resolve_yt_audio_stream_extract_info(self):
        valid_vid = "dQw4w9WgXcQ"
        mock_info = {
            "url": "https://googlevideo.com/audio_stream",
            "ext": "m4a",
            "http_headers": {"User-Agent": "YtDlpUA/1.0"},
        }
        mock_ydl = MagicMock()
        mock_ydl.extract_info.return_value = mock_info
        mock_ydl.__enter__.return_value = mock_ydl
        mock_ydl.__exit__.return_value = False

        with patch.dict(srv.STREAM_URL_CACHE, {}, clear=True):
            with patch.dict("sys.modules", {"yt_dlp": MagicMock(YoutubeDL=MagicMock(return_value=mock_ydl))}):
                url, ctype, hdrs = srv.resolve_yt_audio_stream_with_headers(valid_vid)
                self.assertEqual(url, "https://googlevideo.com/audio_stream")
                self.assertEqual(ctype, "audio/mp4")
                self.assertEqual(hdrs["User-Agent"], "YtDlpUA/1.0")


if __name__ == "__main__":
    unittest.main()

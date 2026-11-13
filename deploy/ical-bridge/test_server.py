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

    def test_fetch_yt_playlists_surfaces_native_supermix_and_liked_music(self):
        lm_resp = {
            "items": [
                {
                    "id": "LM",
                    "snippet": {
                        "title": "Liked Music",
                        "thumbnails": {"high": {"url": "https://img.youtube.com/lm.jpg"}},
                    },
                    "contentDetails": {"itemCount": 25},
                }
            ]
        }
        mine_resp = {
            "items": [
                {
                    "id": "PLcustom1",
                    "snippet": {
                        "title": "Custom Playlist",
                        "thumbnails": {"high": {"url": "https://img.youtube.com/pl1.jpg"}},
                    },
                    "contentDetails": {"itemCount": 10},
                }
            ]
        }

        def fake_urlopen(req, timeout=8):
            url = req.full_url if hasattr(req, "full_url") else str(req)
            payload = lm_resp if "id=LM" in url else mine_resp
            mock_r = MagicMock()
            mock_r.read.return_value = json.dumps(payload).encode("utf-8")
            mock_r.__enter__.return_value = mock_r
            mock_r.__exit__.return_value = False
            return mock_r

        with patch.dict(srv.CACHE, {}, clear=True):
            with patch.object(srv, "get_yt_oauth_token", return_value="mock_tok"):
                with patch("urllib.request.urlopen", side_effect=fake_urlopen):
                    with patch.object(
                        srv,
                        "fetch_yt_playlist_tracks",
                        return_value=[{"video_id": "sm1", "title": "T1", "artist": "A1", "thumb": "https://img.youtube.com/sm.jpg"}],
                    ):
                        playlists = srv.fetch_yt_playlists()
                        self.assertEqual(len(playlists), 3)
                        self.assertEqual(playlists[0]["playlist_id"], srv.YT_NATIVE_SUPERMIX_ID)
                        self.assertEqual(playlists[0]["title"], "My Supermix")
                        self.assertEqual(playlists[1]["playlist_id"], srv.YT_NATIVE_LIKED_MUSIC_ID)
                        self.assertEqual(playlists[1]["title"], "Liked Music")
                        self.assertEqual(playlists[1]["count"], 25)
                        self.assertEqual(playlists[2]["playlist_id"], "PLcustom1")

    def test_fetch_yt_playlist_tracks_normalizes_legacy_aliases(self):
        api_response = {
            "items": [
                {
                    "snippet": {
                        "title": "Supermix Track",
                        "videoOwnerChannelTitle": "Artist SM",
                        "thumbnails": {"high": {"url": "https://img.youtube.com/sm.jpg"}},
                    },
                    "contentDetails": {"videoId": "sm_vid_001"},
                }
            ]
        }
        captured_urls = []

        def fake_urlopen(req, timeout=8):
            captured_urls.append(req.full_url if hasattr(req, "full_url") else str(req))
            mock_r = MagicMock()
            mock_r.read.return_value = json.dumps(api_response).encode("utf-8")
            mock_r.__enter__.return_value = mock_r
            mock_r.__exit__.return_value = False
            return mock_r

        with patch.dict(srv.CACHE, {}, clear=True):
            with patch.object(srv, "get_yt_oauth_token", return_value="mock_tok"):
                with patch("urllib.request.urlopen", side_effect=fake_urlopen):
                    tracks = srv.fetch_yt_playlist_tracks(srv.YT_LEGACY_TYPO_SUPERMIX_ID)
                    self.assertEqual(len(tracks), 1)
                    self.assertTrue(any(srv.YT_NATIVE_SUPERMIX_ID in u for u in captured_urls))
                    tracks_lm = srv.fetch_yt_playlist_tracks("LM_LIKED_SONGS")
                    self.assertEqual(len(tracks_lm), 1)
                    self.assertTrue(any(f"playlistId={srv.YT_NATIVE_LIKED_MUSIC_ID}" in u for u in captured_urls))

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

    def test_control_music_playback_shuffle_toggles_and_shuffles_remaining_queue(self):
        t0 = {"video_id": "vid00000000", "title": "Track 0", "artist": "A0"}
        t1 = {"video_id": "vid11111111", "title": "Track 1", "artist": "A1"}
        t2 = {"video_id": "vid22222222", "title": "Track 2", "artist": "A2"}
        t3 = {"video_id": "vid33333333", "title": "Track 3", "artist": "A3"}
        with patch.dict(srv.MUSIC_STATE, {
            "active_speaker": "this_device",
            "local_state": "playing",
            "local_volume": 35,
            "playing_video_id": "vid11111111",
            "queue": [t0, t1, t2, t3],
            "queue_index": 1,
            "playlist_title": "Test Playlist",
            "shuffle": False,
        }):
            with patch("random.shuffle", side_effect=lambda lst: lst.reverse()) as mock_shuffle:
                ok, st = srv.control_music_playback({"action": "shuffle"})
                self.assertTrue(ok)
                self.assertTrue(srv.MUSIC_STATE["shuffle"])
                self.assertTrue(st["now_playing"]["shuffle"])
                mock_shuffle.assert_called_once()
                # Tracks up to queue_index (t0, t1) stay in place; remaining tracks (t2, t3) are shuffled
                self.assertEqual(srv.MUSIC_STATE["queue"], [t0, t1, t3, t2])
                self.assertEqual(st["queue"], [t0, t1, t3, t2])

                # Toggling shuffle off disables shuffle without reshuffling
                mock_shuffle.reset_mock()
                ok2, st2 = srv.control_music_playback({"action": "shuffle"})
                self.assertTrue(ok2)
                self.assertFalse(srv.MUSIC_STATE["shuffle"])
                self.assertFalse(st2["now_playing"]["shuffle"])
                mock_shuffle.assert_not_called()
                self.assertEqual(srv.MUSIC_STATE["queue"], [t0, t1, t3, t2])

    def test_camera_catalog_and_eve_cam_mapping(self):
        target = srv.resolve_camera_target("eve_cam")
        self.assertIsNotNone(target)
        self.assertEqual(target["id"], "eve_cam")
        self.assertEqual(target["entity_id"], "camera.bedroom_bedroom_camera")
        self.assertEqual(target["title"], "Eve Cam")

        alias_target = srv.resolve_camera_target("camera.bedroom_bedroom_camera")
        self.assertIsNotNone(alias_target)
        self.assertEqual(alias_target["id"], "eve_cam")

        mock_states = [
            {
                "entity_id": "camera.bedroom_bedroom_camera",
                "state": "streaming",
                "attributes": {"friendly_name": "Bedroom camera"},
            },
            {
                "entity_id": "camera.rosie_cam",
                "state": "streaming",
                "attributes": {"friendly_name": "Rosie Cam"},
            },
        ]
        with patch.dict(srv.CACHE, {}, clear=True):
            with patch.object(srv, "_ha_request", return_value=mock_states):
                h = self._create_handler("GET", "/hearth-api/cameras", client_ip="127.0.0.1")
                h.do_GET()
                h.send_response.assert_called_with(200)
                body = json.loads(h.wfile.getvalue().decode("utf-8"))
                self.assertIn("cameras", body)
                eve_entries = [c for c in body["cameras"] if c["id"] == "eve_cam"]
                self.assertEqual(len(eve_entries), 1)
                self.assertEqual(eve_entries[0]["entity_id"], "camera.bedroom_bedroom_camera")
                self.assertEqual(eve_entries[0]["state"], "streaming")
                self.assertEqual(eve_entries[0]["title"], "Eve Cam")

    def test_camera_snapshot_and_webrtc_endpoints(self):
        fake_jpeg = b"\xff\xd8\xff\xe0fake_jpeg_bytes"
        with patch.object(srv, "fetch_camera_snapshot", return_value=(fake_jpeg, "image/jpeg")):
            h_snap = self._create_handler("GET", "/hearth-api/cameras/eve_cam/snapshot", client_ip="127.0.0.1")
            h_snap.do_GET()
            h_snap.send_response.assert_called_with(200)
            self.assertEqual(h_snap.wfile.getvalue(), fake_jpeg)

            h_stream = self._create_handler("GET", "/hearth-api/cameras/eve_cam/stream", client_ip="127.0.0.1")
            h_stream.do_GET()
            h_stream.send_response.assert_called_with(200)
            self.assertEqual(h_stream.wfile.getvalue(), fake_jpeg)

        with patch.object(srv, "exchange_camera_webrtc", return_value=(True, "v=0\r\nsdp_answer", 200)):
            offer = b"v=0\r\nsdp_offer"
            h_rtc = self._create_handler(
                "POST",
                "/hearth-api/cameras/eve_cam/webrtc",
                headers={"Content-Length": str(len(offer)), "Content-Type": "application/sdp"},
                body=offer,
                client_ip="127.0.0.1",
            )
            h_rtc.do_POST()
            h_rtc.send_response.assert_called_with(200)
            self.assertEqual(h_rtc.wfile.getvalue(), b"v=0\r\nsdp_answer")

    def test_camera_last_good_frame_retained_on_nest_placeholder(self):
        good_jpeg = b"\xff\xd8\xff\xe0good_camera_frame_bytes\xff\xd9"
        placeholder_png = b"\x89PNG" + (b"\x00" * (srv.HA_NEST_PLACEHOLDER_LEN - 4))
        self.assertEqual(len(placeholder_png), srv.HA_NEST_PLACEHOLDER_LEN)

        cfg_vals = {
            "HA_BASE_URL": "http://198.51.100.10:8123",
            "HA_ACCESS_TOKEN": "test_ha_tok",
            "GO2RTC_BASE_URL": "http://198.51.100.10:1984",
        }

        def make_resp(payload, ctype="image/jpeg"):
            r = MagicMock()
            r.read.return_value = payload
            r.headers = {"Content-Type": ctype}
            r.__enter__.return_value = r
            r.__exit__.return_value = False
            return r

        with patch.dict(srv.CAMERA_SNAP_CACHE, {}, clear=True):
            with patch.dict(srv.CAMERA_LAST_GOOD_FRAME, {}, clear=True):
                with patch.object(srv, "_cfg", side_effect=lambda k, d="": cfg_vals.get(k, d)):
                    # 1. First fetch succeeds with a real JPEG frame and populates both caches
                    with patch("urllib.request.urlopen", return_value=make_resp(good_jpeg, "image/jpeg")):
                        data1, ctype1 = srv.fetch_camera_snapshot("doorbell", force_refresh=True)
                        self.assertEqual(data1, good_jpeg)
                        self.assertEqual(ctype1, "image/jpeg")
                        self.assertIn("doorbell", srv.CAMERA_LAST_GOOD_FRAME)
                        self.assertEqual(srv.CAMERA_LAST_GOOD_FRAME["doorbell"][1], good_jpeg)

                    # 2. Subsequent fetch gets 2689-byte Nest placeholder from HA, go2rtc times out,
                    # and camera_proxy_stream fails -> returns retained CAMERA_LAST_GOOD_FRAME
                    def cold_urlopen(req, timeout=5):
                        url = req.full_url if hasattr(req, "full_url") else str(req)
                        if "/api/camera_proxy/" in url:
                            return make_resp(placeholder_png, "image/png")
                        raise TimeoutError("upstream timeout")

                    with patch("urllib.request.urlopen", side_effect=cold_urlopen):
                        data2, ctype2 = srv.fetch_camera_snapshot("doorbell", force_refresh=True)
                        self.assertEqual(data2, good_jpeg)
                        self.assertEqual(ctype2, "image/jpeg")
                        self.assertNotEqual(len(data2), srv.HA_NEST_PLACEHOLDER_LEN)

    def test_camera_proxy_stream_multipart_jpeg_fallback(self):
        placeholder_png = b"\x89PNG" + (b"\x00" * (srv.HA_NEST_PLACEHOLDER_LEN - 4))
        stream_jpeg = b"\xff\xd8\xff\xe0multipart_extracted_jpeg_payload\xff\xd9"
        multipart_stream = (
            b"--frame\r\n"
            b"Content-Type: image/jpeg\r\n"
            b"Content-Length: " + str(len(stream_jpeg)).encode("ascii") + b"\r\n\r\n"
            + stream_jpeg
            + b"\r\n--frame\r\n"
        )

        cfg_vals = {
            "HA_BASE_URL": "http://198.51.100.10:8123",
            "HA_ACCESS_TOKEN": "test_ha_tok",
            "GO2RTC_BASE_URL": "http://198.51.100.10:1984",
        }

        def stream_urlopen(req, timeout=5):
            url = req.full_url if hasattr(req, "full_url") else str(req)
            if "/api/camera_proxy/" in url:
                r = MagicMock()
                r.read.return_value = placeholder_png
                r.headers = {"Content-Type": "image/png"}
                r.__enter__.return_value = r
                r.__exit__.return_value = False
                return r
            if "/api/frame.jpeg" in url:
                raise TimeoutError("go2rtc timeout")
            if "/api/camera_proxy_stream/" in url:
                bio = io.BytesIO(multipart_stream)
                r = MagicMock()
                r.read.side_effect = bio.read
                r.headers = {"Content-Type": "multipart/x-mixed-replace; boundary=frame"}
                r.__enter__.return_value = r
                r.__exit__.return_value = False
                return r
            raise RuntimeError(f"Unexpected URL: {url}")

        with patch.dict(srv.CAMERA_SNAP_CACHE, {}, clear=True):
            with patch.dict(srv.CAMERA_LAST_GOOD_FRAME, {}, clear=True):
                with patch.object(srv, "_cfg", side_effect=lambda k, d="": cfg_vals.get(k, d)):
                    with patch("urllib.request.urlopen", side_effect=stream_urlopen):
                        data, ctype = srv.fetch_camera_snapshot("front_yard_camera", force_refresh=True)
                        self.assertEqual(data, stream_jpeg)
                        self.assertEqual(ctype, "image/jpeg")
                        self.assertIn("front_yard_camera", srv.CAMERA_LAST_GOOD_FRAME)
                        self.assertEqual(srv.CAMERA_LAST_GOOD_FRAME["front_yard_camera"][1], stream_jpeg)

    def test_warm_camera_snapshots_once_staggered(self):
        fake_cams = [
            {"id": "doorbell", "entity_id": "camera.doorbell", "battery": True, "event_driven": True, "event_entities": ["event.doorbell_motion"]},
            {"id": "front_yard_camera", "entity_id": "camera.front_yard_camera", "event_driven": True, "event_entities": ["event.front_yard_camera_motion"]},
            {"id": "rosie_cam", "entity_id": "camera.rosie_cam", "event_driven": True, "event_entities": ["event.rosie_cam_motion"]},
        ]
        good_jpeg = b"\xff\xd8\xff\xe0warm_jpeg\xff\xd9"
        called = []

        def fake_fetch(cam_id, force_refresh=False):
            called.append((cam_id, force_refresh))
            return good_jpeg, "image/jpeg"

        srv.CAMERA_WARMER_STOP_EVENT.clear()
        with patch.dict(srv.CAMERA_LAST_GOOD_FRAME, {}, clear=True):
            with patch.object(srv, "_get_configured_cameras", return_value=fake_cams):
                with patch.object(srv, "_has_camera_event_changed", return_value=False):
                    with patch.object(srv, "fetch_camera_snapshot", side_effect=fake_fetch):
                        with patch.object(srv.CAMERA_WARMER_STOP_EVENT, "wait", return_value=False) as mock_wait:
                            # On cold start with unchanged events: doorbell (battery=True) is skipped,
                            # while wired cameras with no cached frame yet are warmed once.
                            warmed = srv.warm_camera_snapshots_once(stagger_sec=1.5)
                            self.assertEqual(warmed, ["front_yard_camera", "rosie_cam"])
                            self.assertEqual(
                                called,
                                [
                                    ("front_yard_camera", True),
                                    ("rosie_cam", True),
                                ],
                            )
                            self.assertEqual(mock_wait.call_count, 1)
                            mock_wait.assert_called_with(1.5)

    def test_battery_camera_event_gated_snapshot_and_warmer_skip(self):
        target = srv.resolve_camera_target("doorbell")
        self.assertIsNotNone(target)
        self.assertTrue(target["battery"])
        self.assertTrue(target["event_driven"])
        self.assertEqual(target["event_entities"], ["event.doorbell_motion", "event.doorbell_chime"])

        good_jpeg_1 = b"\xff\xd8\xff\xe0doorbell_frame_1\xff\xd9"
        good_jpeg_2 = b"\xff\xd8\xff\xe0doorbell_frame_2\xff\xd9"
        placeholder_png = b"\x89PNG" + (b"\x00" * (srv.HA_NEST_PLACEHOLDER_LEN - 4))

        cfg_vals = {
            "HA_BASE_URL": "http://198.51.100.10:8123",
            "HA_ACCESS_TOKEN": "test_ha_tok",
            "GO2RTC_BASE_URL": "http://198.51.100.10:1984",
        }

        motion_ts = ["2026-11-12T18:00:00+00:00"]
        chime_ts = ["2026-11-12T17:00:00+00:00"]
        active_stream_hits = []
        current_frame = [good_jpeg_1]

        def make_resp(payload, ctype="image/jpeg"):
            r = MagicMock()
            r.read.return_value = payload
            r.headers = {"Content-Type": ctype}
            r.__enter__.return_value = r
            r.__exit__.return_value = False
            return r

        def fake_urlopen(req, timeout=5):
            url = req.full_url if hasattr(req, "full_url") else str(req)
            if "/api/states/event.doorbell_motion" in url:
                return make_resp(json.dumps({"state": motion_ts[0]}).encode("utf-8"), "application/json")
            if "/api/states/event.doorbell_chime" in url:
                return make_resp(json.dumps({"state": chime_ts[0]}).encode("utf-8"), "application/json")
            if "/api/camera_proxy/camera.doorbell" in url:
                return make_resp(placeholder_png, "image/png")
            if "/api/frame.jpeg" in url or "/api/camera_proxy_stream/" in url:
                active_stream_hits.append(url)
                return make_resp(current_frame[0], "image/jpeg")
            raise RuntimeError(f"Unexpected URL: {url}")

        with patch.dict(srv.CAMERA_SNAP_CACHE, {}, clear=True):
            with patch.dict(srv.CAMERA_LAST_GOOD_FRAME, {}, clear=True):
                with patch.dict(srv.CAMERA_LAST_EVENT_STATE, {}, clear=True):
                    with patch.object(srv, "_cfg", side_effect=lambda k, d="": cfg_vals.get(k, d)):
                        with patch("urllib.request.urlopen", side_effect=fake_urlopen):
                            # 1. Cold start: first check records initial HA event state and returns False
                            # so stale historical timestamps do NOT wake the battery doorbell!
                            data0, ctype0 = srv.fetch_camera_snapshot("doorbell", force_refresh=False)
                            self.assertEqual(data0, placeholder_png)
                            self.assertEqual(ctype0, "image/png")
                            self.assertEqual(len(active_stream_hits), 0)

                            # 2. Event timestamp changes -> fetches fresh snapshot via go2rtc
                            motion_ts[0] = "2026-11-12T18:02:00+00:00"
                            data1, ctype1 = srv.fetch_camera_snapshot("doorbell", force_refresh=False)
                            self.assertEqual(data1, good_jpeg_1)
                            self.assertEqual(ctype1, "image/jpeg")
                            self.assertEqual(len(active_stream_hits), 1)

                            # Expire short-lived CAMERA_SNAP_CACHE to simulate next frontend poll (15s/30s later)
                            srv.CAMERA_SNAP_CACHE.clear()
                            current_frame[0] = good_jpeg_2

                            # 3. Repeated fetch_camera_snapshot when event.doorbell_motion has NOT changed:
                            # must return retained frame and NEVER hit go2rtc or /api/camera_proxy_stream/
                            data2, ctype2 = srv.fetch_camera_snapshot("doorbell", force_refresh=False)
                            self.assertEqual(data2, good_jpeg_1)
                            self.assertEqual(ctype2, "image/jpeg")
                            self.assertEqual(len(active_stream_hits), 1)

                            # 4. warm_camera_snapshots_once when event has NOT changed:
                            # must skip battery camera and NEVER hit go2rtc or /api/camera_proxy_stream/
                            srv.CAMERA_WARMER_STOP_EVENT.clear()
                            with patch.object(srv, "_get_configured_cameras", return_value=[srv.DEFAULT_CAMERAS[0]]):
                                warmed = srv.warm_camera_snapshots_once(stagger_sec=0)
                                self.assertEqual(warmed, [])
                                self.assertEqual(len(active_stream_hits), 1)

                            # 5. Event timestamp changes -> fetch_camera_snapshot fetches a fresh snapshot
                            motion_ts[0] = "2026-11-12T18:05:00+00:00"
                            srv.CAMERA_SNAP_CACHE.clear()
                            data3, ctype3 = srv.fetch_camera_snapshot("doorbell", force_refresh=False)
                            self.assertEqual(data3, good_jpeg_2)
                            self.assertEqual(ctype3, "image/jpeg")
                            self.assertEqual(len(active_stream_hits), 2)

                            # 6. Event timestamp changes again -> warmer also refreshes battery camera
                            motion_ts[0] = "2026-11-12T18:10:00+00:00"
                            with patch.object(srv, "_get_configured_cameras", return_value=[srv.DEFAULT_CAMERAS[0]]):
                                warmed2 = srv.warm_camera_snapshots_once(stagger_sec=0)
                                self.assertEqual(warmed2, ["doorbell"])
                                self.assertEqual(len(active_stream_hits), 3)

    def test_all_nine_nest_cameras_event_driven_passive_caching(self):
        expected_events = {
            "doorbell": ["event.doorbell_motion", "event.doorbell_chime"],
            "front_yard_camera": ["event.front_yard_camera_motion"],
            "sideyard_stairs": ["event.sideyard_stairs_motion"],
            "deck_camera": ["event.deck_camera_motion"],
            "top_of_the_hill": ["event.top_of_the_hill_motion"],
            "rosie_cam": ["event.rosie_cam_motion"],
            "isaac_cam": ["event.isaac_cam_motion"],
            "eve_cam": ["event.bedroom_bedroom_camera_motion", "event.eve_cam_motion"],
            "kitchen_hub": ["event.kitchen_hub_motion", "event.living_room_hub_motion"],
        }

        for cam_id, ents in expected_events.items():
            target = srv.resolve_camera_target(cam_id)
            self.assertIsNotNone(target, f"Missing target for {cam_id}")
            self.assertTrue(target["event_driven"], f"{cam_id} must be event_driven")
            self.assertEqual(target["event_entities"], ents, f"Mismatched event_entities for {cam_id}")
            self.assertEqual(target["battery"], cam_id == "doorbell")

        cached_jpeg = b"\xff\xd8\xff\xe0cached_wired_frame\xff\xd9"
        fresh_jpeg = b"\xff\xd8\xff\xe0fresh_motion_frame\xff\xd9"
        placeholder_png = b"\x89PNG" + (b"\x00" * (srv.HA_NEST_PLACEHOLDER_LEN - 4))

        cfg_vals = {
            "HA_BASE_URL": "http://198.51.100.10:8123",
            "HA_ACCESS_TOKEN": "test_ha_tok",
            "GO2RTC_BASE_URL": "http://198.51.100.10:1984",
        }

        rosie_motion_ts = ["2026-11-12T18:00:00+00:00"]
        upstream_hits = []

        def make_resp(payload, ctype="image/jpeg"):
            r = MagicMock()
            r.read.return_value = payload
            r.headers = {"Content-Type": ctype}
            r.__enter__.return_value = r
            r.__exit__.return_value = False
            return r

        def fake_urlopen(req, timeout=5):
            url = req.full_url if hasattr(req, "full_url") else str(req)
            if "/api/states/event.rosie_cam_motion" in url:
                return make_resp(json.dumps({"state": rosie_motion_ts[0]}).encode("utf-8"), "application/json")
            if "/api/camera_proxy/" in url:
                upstream_hits.append(url)
                return make_resp(placeholder_png, "image/png")
            if "/api/frame.jpeg" in url or "/api/camera_proxy_stream/" in url:
                upstream_hits.append(url)
                return make_resp(fresh_jpeg, "image/jpeg")
            raise RuntimeError(f"Unexpected URL: {url}")

        with patch.dict(srv.CAMERA_SNAP_CACHE, {}, clear=True):
            with patch.dict(srv.CAMERA_LAST_GOOD_FRAME, {"rosie_cam": (srv.time.time() - 120, cached_jpeg, "image/jpeg")}, clear=True):
                with patch.dict(srv.CAMERA_LAST_EVENT_STATE, {}, clear=True):
                    with patch.object(srv, "_cfg", side_effect=lambda k, d="": cfg_vals.get(k, d)):
                        with patch("urllib.request.urlopen", side_effect=fake_urlopen):
                            # 1. First check with existing CAMERA_LAST_GOOD_FRAME: records baseline event timestamp
                            # and returns cached_jpeg immediately without hitting camera_proxy, go2rtc, or camera_proxy_stream
                            data1, ctype1 = srv.fetch_camera_snapshot("rosie_cam", force_refresh=False)
                            self.assertEqual(data1, cached_jpeg)
                            self.assertEqual(ctype1, "image/jpeg")
                            self.assertEqual(upstream_hits, [])

                            # 2. Second check with unchanged event timestamp: still zero upstream camera hits
                            data2, ctype2 = srv.fetch_camera_snapshot("rosie_cam", force_refresh=False)
                            self.assertEqual(data2, cached_jpeg)
                            self.assertEqual(upstream_hits, [])

                            # 3. Warmer loop with unchanged event timestamp and cached frame present: skips camera
                            rosie_cfg = [c for c in srv.DEFAULT_CAMERAS if c["id"] == "rosie_cam"]
                            with patch.object(srv, "_get_configured_cameras", return_value=rosie_cfg):
                                warmed = srv.warm_camera_snapshots_once(stagger_sec=0)
                                self.assertEqual(warmed, [])
                                self.assertEqual(upstream_hits, [])

                            # 4. Motion event timestamp changes -> fetches fresh frame and updates CAMERA_LAST_GOOD_FRAME
                            rosie_motion_ts[0] = "2026-11-12T18:03:00+00:00"
                            data3, ctype3 = srv.fetch_camera_snapshot("rosie_cam", force_refresh=False)
                            self.assertEqual(data3, fresh_jpeg)
                            self.assertEqual(ctype3, "image/jpeg")
                            self.assertGreaterEqual(len(upstream_hits), 1)

    def test_active_refresh_hybrid_wired_vs_battery_doorbell(self):
        cached_wired_jpeg = b"\xff\xd8\xff\xe0cached_wired_frame\xff\xd9"
        fresh_wired_jpeg = b"\xff\xd8\xff\xe0fresh_active_wired_frame\xff\xd9"
        cached_doorbell_jpeg = b"\xff\xd8\xff\xe0cached_doorbell_frame\xff\xd9"
        fresh_doorbell_jpeg = b"\xff\xd8\xff\xe0fresh_doorbell_event_frame\xff\xd9"
        placeholder_png = b"\x89PNG" + (b"\x00" * (srv.HA_NEST_PLACEHOLDER_LEN - 4))

        cfg_vals = {
            "HA_BASE_URL": "http://198.51.100.10:8123",
            "HA_ACCESS_TOKEN": "test_ha_tok",
            "GO2RTC_BASE_URL": "http://198.51.100.10:1984",
            "CAMERA_ACTIVE_INTERVAL_SEC": 25.0,
        }

        doorbell_motion_ts = ["2026-11-12T18:00:00+00:00"]
        doorbell_chime_ts = ["2026-11-12T18:00:00+00:00"]
        rosie_motion_ts = ["2026-11-12T18:00:00+00:00"]
        upstream_hits = []

        def make_resp(payload, ctype="image/jpeg"):
            r = MagicMock()
            r.read.return_value = payload
            r.headers = {"Content-Type": ctype}
            r.__enter__.return_value = r
            r.__exit__.return_value = False
            return r

        def fake_urlopen(req, timeout=5):
            url = req.full_url if hasattr(req, "full_url") else str(req)
            if "/api/states/event.doorbell_motion" in url:
                return make_resp(json.dumps({"state": doorbell_motion_ts[0]}).encode("utf-8"), "application/json")
            if "/api/states/event.doorbell_chime" in url:
                return make_resp(json.dumps({"state": doorbell_chime_ts[0]}).encode("utf-8"), "application/json")
            if "/api/states/event.rosie_cam_motion" in url:
                return make_resp(json.dumps({"state": rosie_motion_ts[0]}).encode("utf-8"), "application/json")
            if "/api/camera_proxy/" in url:
                upstream_hits.append(url)
                return make_resp(placeholder_png, "image/png")
            if "/api/frame.jpeg" in url or "/api/camera_proxy_stream/" in url:
                upstream_hits.append(url)
                if "doorbell" in url:
                    return make_resp(fresh_doorbell_jpeg, "image/jpeg")
                return make_resp(fresh_wired_jpeg, "image/jpeg")
            raise RuntimeError(f"Unexpected URL: {url}")

        now = srv.time.time()
        with patch.dict(srv.CAMERA_SNAP_CACHE, {}, clear=True):
            with patch.dict(
                srv.CAMERA_LAST_GOOD_FRAME,
                {
                    "rosie_cam": (now - 10.0, cached_wired_jpeg, "image/jpeg"),
                    "doorbell": (now - 120.0, cached_doorbell_jpeg, "image/jpeg"),
                },
                clear=True,
            ):
                with patch.dict(srv.CAMERA_LAST_EVENT_STATE, {}, clear=True):
                    with patch.object(srv, "_cfg", side_effect=lambda k, d="": cfg_vals.get(k, d)):
                        with patch("urllib.request.urlopen", side_effect=fake_urlopen):
                            # 1. Wired camera with active_refresh=True when cached snapshot is fresh (< 25s old):
                            # serves cached bytes without re-waking the stream
                            d1, c1 = srv.fetch_camera_snapshot("rosie_cam", force_refresh=False, active_refresh=True)
                            self.assertEqual(d1, cached_wired_jpeg)
                            self.assertEqual(c1, "image/jpeg")
                            self.assertEqual(upstream_hits, [])

                            # 2. Age wired camera cache past active_interval_sec (30s old >= 25s):
                            # active_refresh=True wakes wired camera and captures a fresh frame even with no HA motion event
                            srv.CAMERA_LAST_GOOD_FRAME["rosie_cam"] = (now - 30.0, cached_wired_jpeg, "image/jpeg")
                            srv.CAMERA_SNAP_CACHE.clear()
                            d2, c2 = srv.fetch_camera_snapshot("rosie_cam", force_refresh=False, active_refresh=True)
                            self.assertEqual(d2, fresh_wired_jpeg)
                            self.assertEqual(c2, "image/jpeg")
                            self.assertGreaterEqual(len(upstream_hits), 1)
                            self.assertEqual(srv.CAMERA_LAST_GOOD_FRAME["rosie_cam"][1], fresh_wired_jpeg)

                            # 3. Subsequent active_refresh=True call right after refresh serves newly cached frame without re-waking
                            upstream_hits.clear()
                            srv.CAMERA_SNAP_CACHE.clear()
                            d3, c3 = srv.fetch_camera_snapshot("rosie_cam", force_refresh=False, active_refresh=True)
                            self.assertEqual(d3, fresh_wired_jpeg)
                            self.assertEqual(upstream_hits, [])

                            # 4. Battery camera (front_doorbell / doorbell) with active_refresh=True and stale cache (120s old):
                            # MUST NOT wake stream on timer when no HA event occurred
                            upstream_hits.clear()
                            srv.CAMERA_SNAP_CACHE.clear()
                            d_bat1, c_bat1 = srv.fetch_camera_snapshot("front_doorbell", force_refresh=False, active_refresh=True)
                            self.assertEqual(d_bat1, cached_doorbell_jpeg)
                            self.assertEqual(c_bat1, "image/jpeg")
                            self.assertEqual(upstream_hits, [])

                            # Repeated active_refresh=True with unchanged HA event timestamps still never wakes battery camera
                            d_bat2, c_bat2 = srv.fetch_camera_snapshot("doorbell", force_refresh=False, active_refresh=True)
                            self.assertEqual(d_bat2, cached_doorbell_jpeg)
                            self.assertEqual(upstream_hits, [])

                            # 5. When HA doorbell chime/motion event advances, battery camera wakes and captures new snapshot
                            doorbell_chime_ts[0] = "2026-11-12T18:05:00+00:00"
                            d_bat3, c_bat3 = srv.fetch_camera_snapshot("front_doorbell", force_refresh=False, active_refresh=True)
                            self.assertEqual(d_bat3, fresh_doorbell_jpeg)
                            self.assertGreaterEqual(len(upstream_hits), 1)

        # 6. Verify HTTP handler parses ?active=1 on both /hearth-api/cameras/<key>/snapshot and /hearth-api/camera/snapshot/<key>
        with patch.object(srv, "fetch_camera_snapshot", return_value=(fresh_wired_jpeg, "image/jpeg")) as mock_fetch:
            h1 = self._create_handler("GET", "/hearth-api/cameras/rosie_cam/snapshot?active=1", client_ip="127.0.0.1")
            h1.do_GET()
            h1.send_response.assert_called_with(200)
            mock_fetch.assert_called_with("rosie_cam", force_refresh=False, active_refresh=True)

            h2 = self._create_handler("GET", "/hearth-api/camera/snapshot/front_doorbell?active=true", client_ip="127.0.0.1")
            h2.do_GET()
            h2.send_response.assert_called_with(200)
            mock_fetch.assert_called_with("front_doorbell", force_refresh=False, active_refresh=True)

            h3 = self._create_handler("GET", "/hearth-api/cameras/rosie_cam/snapshot?active=0", client_ip="127.0.0.1")
            h3.do_GET()
            h3.send_response.assert_called_with(200)
            mock_fetch.assert_called_with("rosie_cam", force_refresh=False, active_refresh=False)


if __name__ == "__main__":
    unittest.main()

#!/usr/bin/env python3
"""Unit tests for HEARTH ical-bridge server."""

import importlib.util
import io
import ipaddress
import json
import os
import unittest
from pathlib import Path
from unittest.mock import MagicMock, patch

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
        with patch.object(
            srv,
            "_load_config",
            return_value={
                "widgets": [
                    {"id": "widget-a", "dimensions": [2, 1]},
                    {"id": "widget-b", "dimensions": [2, 1]},
                ]
            },
        ):
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
        self.assertFalse(
            srv._cast_track_to_speaker("media_player.speaker", {"video_id": "invalid"})
        )
        self.assertFalse(srv._cast_track_to_speaker("invalid_speaker", {"video_id": valid_id}))

        # GET /hearth-api/music/stream/<video_id>.m4a returns 400 on invalid video_id
        h = self._create_handler(
            "GET", "/hearth-api/music/stream/invalid_id.m4a", client_ip="127.0.0.1"
        )
        h.do_GET()
        h.send_response.assert_called_with(400)

    def test_ha_entity_id_validation(self):
        self.assertIsNotNone(srv.HA_ENTITY_ID_RE.match("media_player.living_room_tv"))
        self.assertIsNotNone(srv.HA_ENTITY_ID_RE.match("remote.living_room_tv"))
        self.assertIsNone(srv.HA_ENTITY_ID_RE.match("invalid_entity_without_domain"))
        self.assertIsNone(srv.HA_ENTITY_ID_RE.match("media player.with spaces"))
        self.assertIsNone(srv.HA_ENTITY_ID_RE.match("../../traversal/path"))

        # launch_plex_on_living_room_tv rejects invalid entity IDs
        with patch.object(
            srv, "_cfg", side_effect=lambda k, d="": "invalid_entity" if "ENTITY" in k else d
        ):
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
        with (
            patch.dict(
                srv.MUSIC_STATE,
                {
                    "active_speaker": "this_device",
                    "local_state": "playing",
                    "local_volume": 42,
                    "playing_video_id": "vid12345678",
                    "queue": [
                        {
                            "video_id": "vid12345678",
                            "title": "Track 1",
                            "artist": "Artist 1",
                            "thumb": "",
                        }
                    ],
                    "queue_index": 0,
                    "playlist_title": "Test Playlist",
                    "shuffle": False,
                },
            ),
            patch.object(srv, "_ha_request") as mock_ha,
        ):
            st = srv.get_music_dashboard_state()
            mock_ha.assert_not_called()
            self.assertEqual(st["active_speaker"], "this_device")
            np = st["now_playing"]
            self.assertEqual(np["video_id"], "vid12345678")
            self.assertEqual(np["state"], "playing")
            self.assertEqual(np["volume"], 42)
            self.assertEqual(np["title"], "Track 1")

    def test_get_music_dashboard_state_defaults_to_this_device_when_no_speakers(self):
        with (
            patch.dict(
                srv.MUSIC_STATE,
                {
                    "active_speaker": "",
                    "local_state": "stopped",
                    "local_volume": 35,
                    "playing_video_id": "",
                    "queue": [],
                    "queue_index": 0,
                    "playlist_title": "",
                    "shuffle": True,
                },
            ),
            patch.object(srv, "_get_speakers", return_value=[]),
        ):
            st = srv.get_music_dashboard_state()
            self.assertEqual(st["active_speaker"], "this_device")
            self.assertEqual(st["now_playing"]["state"], "stopped")
            self.assertEqual(st["now_playing"]["volume"], 35)
            self.assertEqual(st["now_playing"]["video_id"], "")

    def test_start_music_playback_local_speaker(self):
        with (
            patch.dict(
                srv.MUSIC_STATE,
                {
                    "active_speaker": "this_device",
                    "local_state": "stopped",
                    "local_volume": 35,
                    "playing_video_id": "",
                    "queue": [],
                    "queue_index": 0,
                    "playlist_title": "",
                    "shuffle": True,
                },
            ),
            patch.object(srv, "_cast_track_to_speaker") as mock_cast,
        ):
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
        with patch.dict(
            srv.MUSIC_STATE,
            {
                "active_speaker": "this_device",
                "local_state": "stopped",
                "local_volume": 35,
                "playing_video_id": "",
                "queue": [],
                "queue_index": 0,
                "playlist_title": "",
                "shuffle": True,
            },
        ):
            mock_results = [
                {
                    "video_id": "search_vid11",
                    "title": "Searched Track",
                    "artist": "Searched Artist",
                    "thumb": "https://img.youtube.com/thumb.jpg",
                }
            ]
            with (
                patch.object(srv, "search_yt_music", return_value=mock_results) as mock_search,
                patch.object(srv, "_cast_track_to_speaker") as mock_cast,
            ):
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
        with (
            patch.dict(
                srv.MUSIC_STATE,
                {
                    "active_speaker": "this_device",
                    "local_state": "playing",
                    "local_volume": 35,
                    "playing_video_id": "vid11111111",
                    "queue": [track1, track2],
                    "queue_index": 0,
                    "playlist_title": "Playlist",
                    "shuffle": False,
                },
            ),
            patch.object(srv, "_ha_request") as mock_ha,
        ):
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

        with (
            patch.object(srv, "get_yt_oauth_token", return_value=None),
            patch.dict(
                "sys.modules", {"ytmusicapi": MagicMock(YTMusic=MagicMock(return_value=mock_ytm))}
            ),
        ):
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

        with (
            patch.dict(srv.STREAM_URL_CACHE, {valid_vid: cached_entry}, clear=True),
            patch("urllib.request.urlopen", return_value=mock_upstream) as mock_urlopen,
        ):
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

        with (
            patch.dict(srv.STREAM_URL_CACHE, {valid_vid: cached_entry}, clear=True),
            patch("urllib.request.urlopen", return_value=mock_upstream),
        ):
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
        err = urllib.error.HTTPError(
            upstream_url, 416, "Range Not Satisfiable", mock_msg, io.BytesIO(b"range error")
        )

        with (
            patch.dict(srv.STREAM_URL_CACHE, {valid_vid: cached_entry}, clear=True),
            patch("urllib.request.urlopen", side_effect=err),
        ):
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

        with (
            patch.object(srv, "get_yt_oauth_token", return_value="mock_token"),
            patch("urllib.request.urlopen", return_value=mock_r),
        ):
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
        with (
            patch.object(srv, "get_yt_oauth_token", return_value=None),
            patch.dict(
                "sys.modules", {"ytmusicapi": MagicMock(YTMusic=MagicMock(return_value=mock_ytm))}
            ),
        ):
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

        with (
            patch.dict(srv.CACHE, {}, clear=True),
            patch.object(srv, "get_yt_oauth_token", return_value="mock_tok"),
            patch("urllib.request.urlopen", side_effect=fake_urlopen),
            patch.object(
                srv,
                "fetch_yt_playlist_tracks",
                return_value=[
                    {
                        "video_id": "sm1",
                        "title": "T1",
                        "artist": "A1",
                        "thumb": "https://img.youtube.com/sm.jpg",
                    }
                ],
            ),
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

        with (
            patch.dict(srv.CACHE, {}, clear=True),
            patch.object(srv, "get_yt_oauth_token", return_value="mock_tok"),
            patch("urllib.request.urlopen", side_effect=fake_urlopen),
        ):
            tracks = srv.fetch_yt_playlist_tracks(srv.YT_LEGACY_TYPO_SUPERMIX_ID)
            self.assertEqual(len(tracks), 1)
            self.assertTrue(any(srv.YT_NATIVE_SUPERMIX_ID in u for u in captured_urls))
            tracks_lm = srv.fetch_yt_playlist_tracks("LM_LIKED_SONGS")
            self.assertEqual(len(tracks_lm), 1)
            self.assertTrue(
                any(f"playlistId={srv.YT_NATIVE_LIKED_MUSIC_ID}" in u for u in captured_urls)
            )

    def test_start_music_playback_no_tracks(self):
        with (
            patch.object(srv, "fetch_yt_playlist_tracks", return_value=[]),
            patch.object(srv, "search_yt_music", return_value=[]),
        ):
            ok, res = srv.start_music_playback({"playlist_id": "empty_pl"})
            self.assertFalse(ok)
            self.assertEqual(res, {"error": "No playable tracks found"})

    def test_start_music_playback_ha_speaker(self):
        with (
            patch.dict(
                srv.MUSIC_STATE,
                {
                    "active_speaker": "media_player.kitchen",
                    "local_state": "stopped",
                    "local_volume": 35,
                    "playing_video_id": "",
                    "queue": [],
                    "queue_index": 0,
                    "playlist_title": "",
                    "shuffle": False,
                },
            ),
            patch.object(srv, "_cast_track_to_speaker", return_value=True) as mock_cast,
            patch.object(
                srv,
                "_ha_request",
                return_value={"state": "playing", "attributes": {"volume_level": 0.5}},
            ),
        ):
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
        with (
            patch.dict(
                srv.MUSIC_STATE,
                {
                    "active_speaker": "media_player.kitchen",
                    "local_state": "stopped",
                    "local_volume": 35,
                    "playing_video_id": "vid11111111",
                    "queue": [track1, track2],
                    "queue_index": 0,
                    "playlist_title": "Playlist",
                    "shuffle": False,
                },
            ),
            patch.object(
                srv,
                "_ha_request",
                return_value={"state": "playing", "attributes": {"volume_level": 0.4}},
            ) as mock_ha,
            patch.object(srv, "_cast_track_to_speaker", return_value=True) as mock_cast,
        ):
            # play_pause
            ok, st = srv.control_music_playback({"action": "play_pause"})
            self.assertTrue(ok)
            mock_ha.assert_any_call(
                "POST",
                "/api/services/media_player/media_play_pause",
                {"entity_id": "media_player.kitchen"},
            )

            # volume
            ok, st = srv.control_music_playback({"action": "volume", "value": 40})
            self.assertTrue(ok)
            mock_ha.assert_any_call(
                "POST",
                "/api/services/media_player/volume_set",
                {"entity_id": "media_player.kitchen", "volume_level": 0.4},
            )

            # next
            ok, st = srv.control_music_playback({"action": "next"})
            self.assertTrue(ok)
            mock_cast.assert_called_with("media_player.kitchen", track2)

            # stop
            ok, st = srv.control_music_playback({"action": "stop"})
            self.assertTrue(ok)
            mock_ha.assert_any_call(
                "POST",
                "/api/services/media_player/media_stop",
                {"entity_id": "media_player.kitchen"},
            )

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

        with (
            patch.dict(srv.STREAM_URL_CACHE, {}, clear=True),
            patch.dict(
                "sys.modules", {"yt_dlp": MagicMock(YoutubeDL=MagicMock(return_value=mock_ydl))}
            ),
        ):
            url, ctype, hdrs = srv.resolve_yt_audio_stream_with_headers(valid_vid)
            self.assertEqual(url, "https://googlevideo.com/audio_stream")
            self.assertEqual(ctype, "audio/mp4")
            self.assertEqual(hdrs["User-Agent"], "YtDlpUA/1.0")

    def test_camera_catalog_and_configured_alias_mapping(self):
        fake_cams = [
            {
                "id": "entry_cam",
                "entity_id": "camera.entry_way_camera",
                "go2rtc_src": "entry_way_camera",
                "aliases": [
                    "entry_cam",
                    "camera.entry_cam",
                    "entry_way_camera",
                    "camera.entry_way_camera",
                ],
                "title": "Entry Cam",
                "group": "indoor",
            },
            {
                "id": "yard_cam",
                "entity_id": "camera.yard_cam",
                "go2rtc_src": "yard_cam",
                "title": "Yard Cam",
                "group": "exterior",
            },
        ]
        mock_states = [
            {
                "entity_id": "camera.entry_way_camera",
                "state": "streaming",
                "attributes": {"friendly_name": "Entry camera"},
            },
            {
                "entity_id": "camera.yard_cam",
                "state": "streaming",
                "attributes": {"friendly_name": "Yard Cam"},
            },
        ]
        with (
            patch.dict(srv.CACHE, {}, clear=True),
            patch.object(srv, "_get_configured_cameras", return_value=fake_cams),
            patch.object(srv, "_ha_request", return_value=mock_states),
        ):
            target = srv.resolve_camera_target("entry_cam")
            self.assertIsNotNone(target)
            self.assertEqual(target["id"], "entry_cam")
            self.assertEqual(target["entity_id"], "camera.entry_way_camera")
            self.assertEqual(target["title"], "Entry Cam")

            alias_target = srv.resolve_camera_target("camera.entry_way_camera")
            self.assertIsNotNone(alias_target)
            self.assertEqual(alias_target["id"], "entry_cam")
            self.assertIsNone(srv.resolve_camera_target("unconfigured_cam"))
            self.assertIsNone(srv.resolve_camera_target("camera.unconfigured_cam"))

            h = self._create_handler("GET", "/hearth-api/cameras", client_ip="127.0.0.1")
            h.do_GET()
            h.send_response.assert_called_with(200)
            body = json.loads(h.wfile.getvalue().decode("utf-8"))
            self.assertIn("cameras", body)
            entry_items = [c for c in body["cameras"] if c["id"] == "entry_cam"]
            self.assertEqual(len(entry_items), 1)
            self.assertEqual(entry_items[0]["entity_id"], "camera.entry_way_camera")
            self.assertEqual(entry_items[0]["state"], "streaming")
            self.assertEqual(entry_items[0]["title"], "Entry Cam")

        with patch.object(srv, "_get_configured_cameras", return_value=[]):
            fallback = srv.resolve_camera_target("fallback_cam")
            self.assertIsNotNone(fallback)
            self.assertEqual(fallback["id"], "fallback_cam")

    def test_camera_snapshot_and_webrtc_endpoints(self):
        fake_jpeg = b"\xff\xd8\xff\xe0fake_jpeg_bytes"
        with patch.object(srv, "fetch_camera_snapshot", return_value=(fake_jpeg, "image/jpeg")):
            h_snap = self._create_handler(
                "GET", "/hearth-api/cameras/entry_cam/snapshot", client_ip="127.0.0.1"
            )
            h_snap.do_GET()
            h_snap.send_response.assert_called_with(200)
            self.assertEqual(h_snap.wfile.getvalue(), fake_jpeg)

            h_stream = self._create_handler(
                "GET", "/hearth-api/cameras/entry_cam/stream", client_ip="127.0.0.1"
            )
            h_stream.do_GET()
            h_stream.send_response.assert_called_with(200)
            self.assertEqual(h_stream.wfile.getvalue(), fake_jpeg)

        with patch.object(
            srv, "exchange_camera_webrtc", return_value=(True, "v=0\r\nsdp_answer", 200)
        ):
            offer = b"v=0\r\nsdp_offer"
            h_rtc = self._create_handler(
                "POST",
                "/hearth-api/cameras/entry_cam/webrtc",
                headers={"Content-Length": str(len(offer)), "Content-Type": "application/sdp"},
                body=offer,
                client_ip="127.0.0.1",
            )
            h_rtc.do_POST()
            h_rtc.send_response.assert_called_with(200)
            self.assertEqual(h_rtc.wfile.getvalue(), b"v=0\r\nsdp_answer")

    def test_camera_routes_reject_non_internal_caller(self):
        get_paths = [
            "/hearth-api/cameras",
            "/cameras",
            "/hearth-api/cameras/snapshot?id=entry_cam",
            "/cameras/snapshot?id=entry_cam",
            "/hearth-api/cameras/entry_cam/snapshot",
            "/hearth-api/cameras/entry_cam/stream",
            "/cameras/entry_cam/snapshot",
            "/hearth-api/camera/snapshot/entry_cam",
            "/hearth-api/camera/stream/entry_cam",
        ]
        for path in get_paths:
            with self.subTest(method="GET", path=path):
                h = self._create_handler("GET", path, client_ip="198.51.100.1")
                h.do_GET()
                h.send_response.assert_called_with(403)

        post_paths = [
            "/hearth-api/cameras/entry_cam/webrtc",
            "/hearth-api/camera/webrtc/entry_cam",
            "/cameras/entry_cam/webrtc",
            "/camera/webrtc/entry_cam",
        ]
        for path in post_paths:
            with self.subTest(method="POST", path=path):
                h = self._create_handler("POST", path, client_ip="198.51.100.1")
                h.do_POST()
                h.send_response.assert_called_with(403)

    def test_camera_proxy_stream_multipart_jpeg_fallback(self):
        placeholder_png = b"\x89PNG" + (b"\x00" * (srv.HA_NEST_PLACEHOLDER_LEN - 4))
        stream_jpeg = b"\xff\xd8\xff\xe0multipart_extracted_jpeg_payload\xff\xd9"
        multipart_stream = (
            b"--frame\r\n"
            b"Content-Type: image/jpeg\r\n"
            b"Content-Length: "
            + str(len(stream_jpeg)).encode("ascii")
            + b"\r\n\r\n"
            + stream_jpeg
            + b"\r\n--frame\r\n"
        )
        fake_cams = [
            {
                "id": "yard_cam",
                "entity_id": "camera.yard_cam",
                "go2rtc_src": "yard_cam",
                "event_driven": False,
            }
        ]

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

        with (
            patch.dict(srv.CAMERA_SNAP_CACHE, {}, clear=True),
            patch.dict(srv.CAMERA_LAST_GOOD_FRAME, {}, clear=True),
            patch.dict(srv.CAMERA_FAILURE_COOLDOWN, {}, clear=True),
            patch.dict(srv.CAMERA_FAILURE_STREAK, {}, clear=True),
            patch.object(srv, "_get_configured_cameras", return_value=fake_cams),
            patch.object(srv, "_cfg", side_effect=lambda k, d="": cfg_vals.get(k, d)),
            patch("urllib.request.urlopen", side_effect=stream_urlopen),
        ):
            data, ctype = srv.fetch_camera_snapshot("yard_cam", force_refresh=True)
            self.assertEqual(data, stream_jpeg)
            self.assertEqual(ctype, "image/jpeg")
            self.assertIn("yard_cam", srv.CAMERA_LAST_GOOD_FRAME)
            self.assertEqual(srv.CAMERA_LAST_GOOD_FRAME["yard_cam"][1], stream_jpeg)

    def test_warm_camera_snapshots_once_staggered(self):
        fake_cams = [
            {
                "id": "front_door",
                "entity_id": "camera.front_door",
                "battery": True,
                "event_driven": True,
                "event_entities": ["event.front_door_motion"],
            },
            {
                "id": "yard_cam",
                "entity_id": "camera.yard_cam",
                "event_driven": True,
                "event_entities": ["event.yard_cam_motion"],
            },
            {
                "id": "patio_cam",
                "entity_id": "camera.patio_cam",
                "event_driven": True,
                "event_entities": ["event.patio_cam_motion"],
            },
        ]
        good_jpeg = b"\xff\xd8\xff\xe0warm_jpeg\xff\xd9"
        called = []

        def fake_fetch(cam_id, force_refresh=False):
            called.append((cam_id, force_refresh))
            return good_jpeg, "image/jpeg"

        srv.CAMERA_WARMER_STOP_EVENT.clear()
        with (
            patch.dict(srv.CAMERA_LAST_GOOD_FRAME, {}, clear=True),
            patch.dict(srv.CAMERA_FAILURE_COOLDOWN, {}, clear=True),
            patch.dict(srv.CAMERA_FAILURE_STREAK, {}, clear=True),
            patch.object(srv, "_get_configured_cameras", return_value=fake_cams),
            patch.object(srv, "_has_camera_event_changed", return_value=False),
            patch.object(srv, "fetch_camera_snapshot", side_effect=fake_fetch),
            patch.object(srv.CAMERA_WARMER_STOP_EVENT, "wait", return_value=False) as mock_wait,
        ):
            # On cold start with unchanged events: front_door (battery=True) is skipped,
            # while wired cameras with no cached frame yet are warmed once.
            warmed = srv.warm_camera_snapshots_once(stagger_sec=1.5)
            self.assertEqual(warmed, ["yard_cam", "patio_cam"])
            self.assertEqual(
                called,
                [
                    ("yard_cam", True),
                    ("patio_cam", True),
                ],
            )
            self.assertEqual(mock_wait.call_count, 1)
            mock_wait.assert_called_with(1.5)

    def test_battery_camera_event_gated_snapshot_and_warmer_skip(self):
        front_door_cfg = [
            {
                "id": "front_door",
                "entity_id": "camera.front_door",
                "go2rtc_src": "front_door",
                "battery": True,
                "event_driven": True,
                "event_entities": ["event.front_door_motion", "event.front_door_chime"],
            }
        ]

        good_jpeg_1 = b"\xff\xd8\xff\xe0front_door_frame_1\xff\xd9"
        good_jpeg_2 = b"\xff\xd8\xff\xe0front_door_frame_2\xff\xd9"
        placeholder_png = b"\x89PNG" + (b"\x00" * (srv.HA_NEST_PLACEHOLDER_LEN - 4))

        cfg_vals = {
            "HA_BASE_URL": "http://198.51.100.10:8123",
            "HA_ACCESS_TOKEN": "test_ha_tok",
            "GO2RTC_BASE_URL": "http://198.51.100.10:1984",
        }

        motion_ts = ["2026-11-12T18:00:00+00:00"]
        chime_ts = ["2026-11-12T17:00:00+00:00"]
        active_stream_hits = []
        ha_proxy_frame = [None]

        def make_resp(payload, ctype="image/jpeg"):
            r = MagicMock()
            r.read.return_value = payload
            r.headers = {"Content-Type": ctype}
            r.__enter__.return_value = r
            r.__exit__.return_value = False
            return r

        def fake_urlopen(req, timeout=5):
            url = req.full_url if hasattr(req, "full_url") else str(req)
            if "/api/states/event.front_door_motion" in url:
                return make_resp(
                    json.dumps({"state": motion_ts[0]}).encode("utf-8"), "application/json"
                )
            if "/api/states/event.front_door_chime" in url:
                return make_resp(
                    json.dumps({"state": chime_ts[0]}).encode("utf-8"), "application/json"
                )
            if "/api/camera_proxy/camera.front_door" in url:
                if ha_proxy_frame[0] is not None:
                    return make_resp(ha_proxy_frame[0], "image/jpeg")
                return make_resp(placeholder_png, "image/png")
            if "/api/frame.jpeg" in url or "/api/camera_proxy_stream/" in url:
                active_stream_hits.append(url)
                return make_resp(good_jpeg_1, "image/jpeg")
            raise RuntimeError(f"Unexpected URL: {url}")

        with (
            patch.dict(srv.CAMERA_SNAP_CACHE, {}, clear=True),
            patch.dict(srv.CAMERA_LAST_GOOD_FRAME, {}, clear=True),
            patch.dict(srv.CAMERA_LAST_EVENT_STATE, {}, clear=True),
            patch.object(srv, "CAMERA_PENDING_SNAPSHOT_REFRESH", set()),
            patch.object(srv, "_get_configured_cameras", return_value=front_door_cfg),
            patch.object(srv, "_cfg", side_effect=lambda k, d="": cfg_vals.get(k, d)),
            patch("urllib.request.urlopen", side_effect=fake_urlopen),
        ):
            target = srv.resolve_camera_target("front_door")
            self.assertIsNotNone(target)
            self.assertTrue(target["battery"])
            self.assertTrue(target["event_driven"])
            self.assertEqual(
                target["event_entities"], ["event.front_door_motion", "event.front_door_chime"]
            )

            # 1. Cold start: first check records initial HA event state and returns False.
            # HA camera_proxy returns placeholder, and zero active stream hits occur.
            data0, ctype0 = srv.fetch_camera_snapshot("front_door", force_refresh=False)
            self.assertEqual(data0, placeholder_png)
            self.assertEqual(ctype0, "image/png")
            self.assertEqual(len(active_stream_hits), 0)

            # 2. Event timestamp changes: HA camera_proxy serves a non-placeholder frame.
            # Returns fresh frame and stores it in CAMERA_LAST_GOOD_FRAME without hitting go2rtc.
            motion_ts[0] = "2026-11-12T18:02:00+00:00"
            ha_proxy_frame[0] = good_jpeg_1
            data1, ctype1 = srv.fetch_camera_snapshot("front_door", force_refresh=False)
            self.assertEqual(data1, good_jpeg_1)
            self.assertEqual(ctype1, "image/jpeg")
            self.assertEqual(len(active_stream_hits), 0)
            self.assertIn("front_door", srv.CAMERA_LAST_GOOD_FRAME)

            # Expire short-lived CAMERA_SNAP_CACHE to simulate next frontend poll (15s/30s later)
            srv.CAMERA_SNAP_CACHE.clear()
            ha_proxy_frame[0] = None

            # 3. Repeated fetch_camera_snapshot when event.front_door_motion has NOT changed:
            # must return retained frame and NEVER hit go2rtc or /api/camera_proxy_stream/
            data2, ctype2 = srv.fetch_camera_snapshot("front_door", force_refresh=False)
            self.assertEqual(data2, good_jpeg_1)
            self.assertEqual(ctype2, "image/jpeg")
            self.assertEqual(len(active_stream_hits), 0)

            # 4. warm_camera_snapshots_once when event has NOT changed:
            # must skip battery camera and NEVER hit go2rtc or /api/camera_proxy_stream/
            srv.CAMERA_WARMER_STOP_EVENT.clear()
            warmed = srv.warm_camera_snapshots_once(stagger_sec=0)
            self.assertEqual(warmed, [])
            self.assertEqual(len(active_stream_hits), 0)

            # 5. Event timestamp changes, but HA camera proxy returns placeholder:
            # must return retained CAMERA_LAST_GOOD_FRAME and NEVER hit go2rtc or stream!
            motion_ts[0] = "2026-11-12T18:05:00+00:00"
            ha_proxy_frame[0] = None
            srv.CAMERA_SNAP_CACHE.clear()
            data3, ctype3 = srv.fetch_camera_snapshot("front_door", force_refresh=True)
            self.assertEqual(data3, good_jpeg_1)
            self.assertEqual(ctype3, "image/jpeg")
            self.assertEqual(len(active_stream_hits), 0)

            # 6. Event timestamp changes again and HA camera proxy provides new frame:
            # warmer passively refreshes battery camera without any active stream hits
            motion_ts[0] = "2026-11-12T18:10:00+00:00"
            ha_proxy_frame[0] = good_jpeg_2
            warmed2 = srv.warm_camera_snapshots_once(stagger_sec=0)
            self.assertEqual(warmed2, ["front_door"])
            self.assertEqual(srv.CAMERA_LAST_GOOD_FRAME["front_door"][1], good_jpeg_2)
            self.assertEqual(len(active_stream_hits), 0)

    def test_event_driven_camera_passive_caching(self):
        cached_jpeg = b"\xff\xd8\xff\xe0cached_wired_frame\xff\xd9"
        fresh_jpeg = b"\xff\xd8\xff\xe0fresh_motion_frame\xff\xd9"
        placeholder_png = b"\x89PNG" + (b"\x00" * (srv.HA_NEST_PLACEHOLDER_LEN - 4))
        yard_cfg = [
            {
                "id": "yard_cam",
                "entity_id": "camera.yard_cam",
                "go2rtc_src": "yard_cam",
                "event_driven": True,
                "event_entities": ["event.yard_cam_motion"],
            }
        ]

        cfg_vals = {
            "HA_BASE_URL": "http://198.51.100.10:8123",
            "HA_ACCESS_TOKEN": "test_ha_tok",
            "GO2RTC_BASE_URL": "http://198.51.100.10:1984",
        }

        yard_motion_ts = ["2026-11-12T18:00:00+00:00"]
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
            if "/api/states/event.yard_cam_motion" in url:
                return make_resp(
                    json.dumps({"state": yard_motion_ts[0]}).encode("utf-8"), "application/json"
                )
            if "/api/camera_proxy/" in url:
                upstream_hits.append(url)
                return make_resp(placeholder_png, "image/png")
            if "/api/frame.jpeg" in url or "/api/camera_proxy_stream/" in url:
                upstream_hits.append(url)
                return make_resp(fresh_jpeg, "image/jpeg")
            raise RuntimeError(f"Unexpected URL: {url}")

        with (
            patch.dict(srv.CAMERA_SNAP_CACHE, {}, clear=True),
            patch.dict(
                srv.CAMERA_LAST_GOOD_FRAME,
                {"yard_cam": (srv.time.time() - 120, cached_jpeg, "image/jpeg")},
                clear=True,
            ),
            patch.dict(srv.CAMERA_LAST_EVENT_STATE, {}, clear=True),
            patch.object(srv, "CAMERA_PENDING_SNAPSHOT_REFRESH", set()),
            patch.object(srv, "_get_configured_cameras", return_value=yard_cfg),
            patch.object(srv, "_cfg", side_effect=lambda k, d="": cfg_vals.get(k, d)),
            patch("urllib.request.urlopen", side_effect=fake_urlopen),
        ):
            # 1. First check with existing CAMERA_LAST_GOOD_FRAME: records baseline event timestamp
            # and returns cached_jpeg immediately without hitting camera_proxy, go2rtc, or camera_proxy_stream
            data1, ctype1 = srv.fetch_camera_snapshot("yard_cam", force_refresh=False)
            self.assertEqual(data1, cached_jpeg)
            self.assertEqual(ctype1, "image/jpeg")
            self.assertEqual(upstream_hits, [])

            # 2. Second check with unchanged event timestamp: still zero upstream camera hits
            data2, ctype2 = srv.fetch_camera_snapshot("yard_cam", force_refresh=False)
            self.assertEqual(data2, cached_jpeg)
            self.assertEqual(upstream_hits, [])

            # 3. Warmer loop with unchanged event timestamp and cached frame present: skips camera
            warmed = srv.warm_camera_snapshots_once(stagger_sec=0)
            self.assertEqual(warmed, [])
            self.assertEqual(upstream_hits, [])

            # 4. Motion event timestamp changes -> fetches fresh frame and updates CAMERA_LAST_GOOD_FRAME
            yard_motion_ts[0] = "2026-11-12T18:03:00+00:00"
            data3, ctype3 = srv.fetch_camera_snapshot("yard_cam", force_refresh=False)
            self.assertEqual(data3, fresh_jpeg)
            self.assertEqual(ctype3, "image/jpeg")
            self.assertGreaterEqual(len(upstream_hits), 1)

    def test_active_refresh_hybrid_wired_vs_battery_camera(self):
        cached_wired_jpeg = b"\xff\xd8\xff\xe0cached_wired_frame\xff\xd9"
        fresh_wired_jpeg = b"\xff\xd8\xff\xe0fresh_active_wired_frame\xff\xd9"
        cached_battery_jpeg = b"\xff\xd8\xff\xe0cached_battery_frame\xff\xd9"
        fresh_battery_jpeg = b"\xff\xd8\xff\xe0fresh_battery_event_frame\xff\xd9"
        placeholder_png = b"\x89PNG" + (b"\x00" * (srv.HA_NEST_PLACEHOLDER_LEN - 4))
        fake_cams = [
            {
                "id": "yard_cam",
                "entity_id": "camera.yard_cam",
                "go2rtc_src": "yard_cam",
                "event_driven": True,
                "event_entities": ["event.yard_cam_motion"],
            },
            {
                "id": "front_door",
                "entity_id": "camera.front_door",
                "go2rtc_src": "front_door",
                "aliases": ["door_entry", "camera.door_entry"],
                "battery": True,
                "event_driven": True,
                "event_entities": ["event.front_door_motion", "event.front_door_chime"],
            },
        ]

        cfg_vals = {
            "HA_BASE_URL": "http://198.51.100.10:8123",
            "HA_ACCESS_TOKEN": "test_ha_tok",
            "GO2RTC_BASE_URL": "http://198.51.100.10:1984",
            "CAMERA_ACTIVE_INTERVAL_SEC": 25.0,
        }

        front_door_motion_ts = ["2026-11-12T18:00:00+00:00"]
        front_door_chime_ts = ["2026-11-12T18:00:00+00:00"]
        yard_motion_ts = ["2026-11-12T18:00:00+00:00"]
        upstream_hits = []
        ha_battery_proxy_payload = [placeholder_png]

        def make_resp(payload, ctype="image/jpeg"):
            r = MagicMock()
            r.read.return_value = payload
            r.headers = {"Content-Type": ctype}
            r.__enter__.return_value = r
            r.__exit__.return_value = False
            return r

        def fake_urlopen(req, timeout=5):
            url = req.full_url if hasattr(req, "full_url") else str(req)
            if "/api/states/event.front_door_motion" in url:
                return make_resp(
                    json.dumps({"state": front_door_motion_ts[0]}).encode("utf-8"),
                    "application/json",
                )
            if "/api/states/event.front_door_chime" in url:
                return make_resp(
                    json.dumps({"state": front_door_chime_ts[0]}).encode("utf-8"),
                    "application/json",
                )
            if "/api/states/event.yard_cam_motion" in url:
                return make_resp(
                    json.dumps({"state": yard_motion_ts[0]}).encode("utf-8"), "application/json"
                )
            if "/api/camera_proxy/camera.front_door" in url:
                upstream_hits.append(url)
                ctype = (
                    "image/jpeg" if ha_battery_proxy_payload[0] != placeholder_png else "image/png"
                )
                return make_resp(ha_battery_proxy_payload[0], ctype)
            if "/api/camera_proxy/" in url:
                upstream_hits.append(url)
                return make_resp(placeholder_png, "image/png")
            if "/api/frame.jpeg" in url or "/api/camera_proxy_stream/" in url:
                upstream_hits.append(url)
                if "front_door" in url:
                    return make_resp(fresh_battery_jpeg, "image/jpeg")
                return make_resp(fresh_wired_jpeg, "image/jpeg")
            raise RuntimeError(f"Unexpected URL: {url}")

        now = srv.time.time()
        with (
            patch.dict(srv.CAMERA_SNAP_CACHE, {}, clear=True),
            patch.dict(
                srv.CAMERA_LAST_GOOD_FRAME,
                {
                    "yard_cam": (now - 10.0, cached_wired_jpeg, "image/jpeg"),
                    "front_door": (now - 120.0, cached_battery_jpeg, "image/jpeg"),
                },
                clear=True,
            ),
            patch.dict(srv.CAMERA_LAST_EVENT_STATE, {}, clear=True),
            patch.object(srv, "CAMERA_PENDING_SNAPSHOT_REFRESH", set()),
            patch.object(srv, "_get_configured_cameras", return_value=fake_cams),
            patch.object(srv, "_cfg", side_effect=lambda k, d="": cfg_vals.get(k, d)),
            patch("urllib.request.urlopen", side_effect=fake_urlopen),
        ):
            # 1. Wired camera with active_refresh=True when cached snapshot is fresh (< 25s old):
            # serves cached bytes without re-waking the stream
            d1, c1 = srv.fetch_camera_snapshot("yard_cam", force_refresh=False, active_refresh=True)
            self.assertEqual(d1, cached_wired_jpeg)
            self.assertEqual(c1, "image/jpeg")
            self.assertEqual(upstream_hits, [])

            # 2. Age wired camera cache past active_interval_sec (30s old >= 25s):
            # active_refresh=True wakes wired camera and captures a fresh frame even with no HA motion event
            srv.CAMERA_LAST_GOOD_FRAME["yard_cam"] = (
                now - 30.0,
                cached_wired_jpeg,
                "image/jpeg",
            )
            srv.CAMERA_SNAP_CACHE.clear()
            d2, c2 = srv.fetch_camera_snapshot("yard_cam", force_refresh=False, active_refresh=True)
            self.assertEqual(d2, fresh_wired_jpeg)
            self.assertEqual(c2, "image/jpeg")
            self.assertGreaterEqual(len(upstream_hits), 1)
            self.assertEqual(srv.CAMERA_LAST_GOOD_FRAME["yard_cam"][1], fresh_wired_jpeg)

            # 3. Subsequent active_refresh=True call right after refresh serves newly cached frame without re-waking
            upstream_hits.clear()
            srv.CAMERA_SNAP_CACHE.clear()
            d3, c3 = srv.fetch_camera_snapshot("yard_cam", force_refresh=False, active_refresh=True)
            self.assertEqual(d3, fresh_wired_jpeg)
            self.assertEqual(upstream_hits, [])

            # 4. Battery camera (door_entry / front_door) with active_refresh=True and stale cache (120s old):
            # MUST NOT wake stream on timer when no HA event occurred
            upstream_hits.clear()
            srv.CAMERA_SNAP_CACHE.clear()
            d_bat1, c_bat1 = srv.fetch_camera_snapshot(
                "door_entry", force_refresh=False, active_refresh=True
            )
            self.assertEqual(d_bat1, cached_battery_jpeg)
            self.assertEqual(c_bat1, "image/jpeg")
            self.assertEqual(upstream_hits, [])

            # Repeated active_refresh=True with unchanged HA event timestamps still never wakes battery camera
            d_bat2, c_bat2 = srv.fetch_camera_snapshot(
                "front_door", force_refresh=False, active_refresh=True
            )
            self.assertEqual(d_bat2, cached_battery_jpeg)
            self.assertEqual(upstream_hits, [])

            # 5. When HA battery camera chime/motion event advances:
            # battery camera only checks HA /api/camera_proxy/ and NEVER hits go2rtc or /api/camera_proxy_stream/
            front_door_chime_ts[0] = "2026-11-12T18:05:00+00:00"
            upstream_hits.clear()
            # Case A: HA camera_proxy returns placeholder -> retains cached_battery_jpeg, zero go2rtc hits
            d_bat3, c_bat3 = srv.fetch_camera_snapshot(
                "door_entry", force_refresh=False, active_refresh=True
            )
            self.assertEqual(d_bat3, cached_battery_jpeg)
            self.assertEqual(c_bat3, "image/jpeg")
            self.assertEqual(
                upstream_hits,
                ["http://198.51.100.10:8123/api/camera_proxy/camera.front_door"],
            )
            self.assertFalse(
                any(
                    "/api/frame.jpeg" in u or "/api/camera_proxy_stream/" in u
                    for u in upstream_hits
                )
            )

            # Case B: HA camera_proxy returns fresh frame -> serves fresh_battery_jpeg, zero go2rtc hits
            front_door_motion_ts[0] = "2026-11-12T18:06:00+00:00"
            upstream_hits.clear()
            ha_battery_proxy_payload[0] = fresh_battery_jpeg
            srv.CAMERA_SNAP_CACHE.clear()
            d_bat4, c_bat4 = srv.fetch_camera_snapshot(
                "door_entry", force_refresh=False, active_refresh=True
            )
            self.assertEqual(d_bat4, fresh_battery_jpeg)
            self.assertEqual(c_bat4, "image/jpeg")
            self.assertEqual(
                upstream_hits,
                ["http://198.51.100.10:8123/api/camera_proxy/camera.front_door"],
            )
            self.assertFalse(
                any(
                    "/api/frame.jpeg" in u or "/api/camera_proxy_stream/" in u
                    for u in upstream_hits
                )
            )

        # 6. Verify HTTP handler parses ?active=1 on both /hearth-api/cameras/<key>/snapshot and /hearth-api/camera/snapshot/<key>
        with patch.object(
            srv, "fetch_camera_snapshot", return_value=(fresh_wired_jpeg, "image/jpeg")
        ) as mock_fetch:
            h1 = self._create_handler(
                "GET", "/hearth-api/cameras/yard_cam/snapshot?active=1", client_ip="127.0.0.1"
            )
            h1.do_GET()
            h1.send_response.assert_called_with(200)
            mock_fetch.assert_called_with("yard_cam", force_refresh=False, active_refresh=True)

            h2 = self._create_handler(
                "GET",
                "/hearth-api/camera/snapshot/door_entry?active=true",
                client_ip="127.0.0.1",
            )
            h2.do_GET()
            h2.send_response.assert_called_with(200)
            mock_fetch.assert_called_with("door_entry", force_refresh=False, active_refresh=True)

            h3 = self._create_handler(
                "GET", "/hearth-api/cameras/yard_cam/snapshot?active=0", client_ip="127.0.0.1"
            )
            h3.do_GET()
            h3.send_response.assert_called_with(200)
            mock_fetch.assert_called_with("yard_cam", force_refresh=False, active_refresh=False)

            h4 = self._create_handler(
                "GET", "/hearth-api/cameras/yard_cam/stream?t=123456", client_ip="127.0.0.1"
            )
            h4.do_GET()
            h4.send_response.assert_called_with(200)
            mock_fetch.assert_called_with("yard_cam", force_refresh=False, active_refresh=True)

    def test_get_cameras_status_auto_live_60s_cap_and_reset(self):
        fake_cams = [
            {
                "id": "front_door",
                "entity_id": "camera.front_door",
                "title": "Front Door",
                "battery": True,
                "event_driven": True,
                "event_entities": ["event.front_door_motion"],
            },
            {
                "id": "yard_cam",
                "entity_id": "camera.yard_cam",
                "title": "Yard Cam",
                "event_driven": True,
                "event_entities": ["event.yard_cam_motion"],
            },
            {
                "id": "patio_cam",
                "entity_id": "camera.patio_cam",
                "title": "Patio Cam",
                "event_driven": True,
                "event_entities": ["event.patio_cam_motion"],
            },
            {
                "id": "entry_cam",
                "entity_id": "camera.entry_cam",
                "title": "Entry Cam",
                "event_driven": True,
                "event_entities": ["event.entry_cam_motion"],
            },
        ]
        mock_states = [
            {
                "entity_id": "camera.front_door",
                "state": "idle",
                "attributes": {"friendly_name": "Front Door"},
            },
            {
                "entity_id": "camera.yard_cam",
                "state": "streaming",
                "attributes": {"friendly_name": "Yard Cam"},
            },
            {
                "entity_id": "camera.patio_cam",
                "state": "streaming",
                "attributes": {"friendly_name": "Patio Cam"},
            },
            {
                "entity_id": "camera.entry_cam",
                "state": "streaming",
                "attributes": {"friendly_name": "Entry Cam"},
            },
        ]
        t0 = 1794585000.0
        with (
            patch.dict(srv.CACHE, {}, clear=True),
            patch.dict(srv._CAMERA_EVENT_EPOCH, {}, clear=True),
            patch.dict(srv.CAMERA_LAST_EVENT_STATE, {}, clear=True),
            patch.object(srv, "_get_configured_cameras", return_value=fake_cams),
            patch.object(srv, "_ha_request", return_value=mock_states),
        ):
            # 1. Single camera motion within 60s -> auto_live: True
            srv._CAMERA_EVENT_EPOCH["patio_cam"] = t0 - 15.0
            with patch.object(srv.time, "time", return_value=t0):
                cams = srv.get_cameras_status()
                by_id = {c["id"]: c for c in cams}
                self.assertTrue(by_id["patio_cam"]["auto_live"])
                self.assertEqual(by_id["patio_cam"]["last_event_epoch"], t0 - 15.0)
                self.assertFalse(by_id["front_door"]["auto_live"])
                self.assertEqual(by_id["front_door"]["last_event_epoch"], 0.0)

            # 2. 3+ cameras with motion within 60s -> caps auto_live: True at the 2 most recent cameras (including battery camera).
            # Battery camera 'front_door' has most recent motion (t0 - 5.0), and yard_cam has (t0 - 10.0).
            srv._CAMERA_EVENT_EPOCH["front_door"] = t0 - 5.0
            srv._CAMERA_EVENT_EPOCH["yard_cam"] = t0 - 10.0
            srv._CAMERA_EVENT_EPOCH["patio_cam"] = t0 - 20.0
            srv._CAMERA_EVENT_EPOCH["entry_cam"] = t0 - 30.0
            with patch.object(srv.time, "time", return_value=t0):
                cams = srv.get_cameras_status()
                by_id = {c["id"]: c for c in cams}
                auto_live_ids = [c["id"] for c in cams if c["auto_live"]]
                self.assertEqual(len(auto_live_ids), 2)
                self.assertCountEqual(auto_live_ids, ["front_door", "yard_cam"])
                self.assertTrue(by_id["front_door"]["auto_live"])
                self.assertEqual(by_id["front_door"]["last_event_epoch"], t0 - 5.0)
                self.assertTrue(by_id["yard_cam"]["auto_live"])
                self.assertFalse(by_id["patio_cam"]["auto_live"])
                self.assertFalse(by_id["entry_cam"]["auto_live"])
                self.assertEqual(by_id["patio_cam"]["last_event_epoch"], t0 - 20.0)

                # Explicitly verify is_camera_auto_live returns True for battery camera
                is_front_door_live, front_door_epoch = srv.is_camera_auto_live("front_door", now=t0)
                self.assertTrue(is_front_door_live)
                self.assertEqual(front_door_epoch, t0 - 5.0)

                is_yard_live, yard_epoch = srv.is_camera_auto_live("yard_cam", now=t0)
                self.assertTrue(is_yard_live)
                self.assertEqual(yard_epoch, t0 - 10.0)

            # 3. Once 60 seconds pass without new motion -> resets auto_live: False
            with patch.object(srv.time, "time", return_value=t0 + 61.0):
                cams_expired = srv.get_cameras_status()
                for c in cams_expired:
                    self.assertFalse(
                        c["auto_live"],
                        f"Expected auto_live False for {c['id']} after >60s",
                    )

    def test_check_camera_motion_updated_seeds_recent_iso_and_updates_epoch_and_headers(self):
        t0 = 1794585000.0
        recent_iso = srv.datetime.datetime.fromtimestamp(t0 - 12.0, tz=srv.UTC).isoformat()
        stale_iso = srv.datetime.datetime.fromtimestamp(t0 - 300.0, tz=srv.UTC).isoformat()
        next_iso = srv.datetime.datetime.fromtimestamp(t0 + 5.0, tz=srv.UTC).isoformat()
        fake_cams = [
            {
                "id": "entry_cam",
                "entity_id": "camera.entry_cam",
                "event_driven": True,
                "event_entities": ["event.entry_cam_motion"],
            },
            {
                "id": "patio_cam",
                "entity_id": "camera.patio_cam",
                "event_driven": True,
                "event_entities": ["event.patio_cam_motion"],
            },
            {
                "id": "yard_cam",
                "entity_id": "camera.yard_cam",
                "event_driven": True,
                "event_entities": ["event.yard_cam_motion"],
            },
        ]

        with (
            patch.dict(srv.CAMERA_LAST_EVENT_STATE, {}, clear=True),
            patch.dict(srv._CAMERA_EVENT_EPOCH, {}, clear=True),
            patch.object(srv, "CAMERA_PENDING_SNAPSHOT_REFRESH", set()),
            patch.object(srv, "_get_configured_cameras", return_value=fake_cams),
        ):
            with patch.object(srv.time, "time", return_value=t0):
                # First seed with recent ISO (<60s) populates _CAMERA_EVENT_EPOCH
                changed_recent = srv.check_camera_motion_updated(
                    "entry_cam",
                    ha_states_map={"event.entry_cam_motion": {"state": recent_iso}},
                )
                self.assertFalse(changed_recent)
                self.assertEqual(srv._CAMERA_EVENT_EPOCH.get("entry_cam"), t0)

                # First seed with stale ISO (>60s) does NOT populate _CAMERA_EVENT_EPOCH
                changed_stale = srv.check_camera_motion_updated(
                    "patio_cam",
                    ha_states_map={"event.patio_cam_motion": {"state": stale_iso}},
                )
                self.assertFalse(changed_stale)
                self.assertNotIn("patio_cam", srv._CAMERA_EVENT_EPOCH)

            # Subsequent change updates _CAMERA_EVENT_EPOCH to new time.time()
            with patch.object(srv.time, "time", return_value=t0 + 5.0):
                changed_next = srv.check_camera_motion_updated(
                    "patio_cam",
                    ha_states_map={"event.patio_cam_motion": {"state": next_iso}},
                )
                self.assertTrue(changed_next)
                self.assertEqual(srv._CAMERA_EVENT_EPOCH.get("patio_cam"), t0 + 5.0)

                # Verify GET /cameras and GET /cameras/snapshot headers
                fake_jpeg = b"\xff\xd8\xff\xe0test_snap\xff\xd9"
                with patch.object(
                    srv, "fetch_camera_snapshot", return_value=(fake_jpeg, "image/jpeg")
                ):
                    h_snap = self._create_handler(
                        "GET",
                        "/cameras/snapshot?camera=patio_cam",
                        client_ip="127.0.0.1",
                    )
                    h_snap.do_GET()
                    h_snap.send_response.assert_called_with(200)
                    h_snap.send_header.assert_any_call("X-Camera-Auto-Live", "1")
                    h_snap.send_header.assert_any_call(
                        "X-Camera-Last-Event-Epoch", str(int(t0 + 5.0))
                    )

                    h_snap_inactive = self._create_handler(
                        "GET",
                        "/hearth-api/cameras/yard_cam/snapshot",
                        client_ip="127.0.0.1",
                    )
                    h_snap_inactive.do_GET()
                    h_snap_inactive.send_response.assert_called_with(200)
                    h_snap_inactive.send_header.assert_any_call("X-Camera-Auto-Live", "0")
                    h_snap_inactive.send_header.assert_any_call("X-Camera-Last-Event-Epoch", "0")

                with (
                    patch.dict(srv.CACHE, {}, clear=True),
                    patch.object(srv, "_ha_request", return_value=[]),
                ):
                    h_cams = self._create_handler("GET", "/cameras", client_ip="127.0.0.1")
                    h_cams.do_GET()
                    h_cams.send_response.assert_called_with(200)
                    payload = json.loads(h_cams.wfile.getvalue().decode("utf-8"))
                    by_id = {c["id"]: c for c in payload["cameras"]}
                    self.assertTrue(by_id["patio_cam"]["auto_live"])
                    self.assertTrue(by_id["entry_cam"]["auto_live"])
                    self.assertFalse(by_id["yard_cam"]["auto_live"])

    def test_catalog_poll_preserves_pending_snapshot_refresh(self):
        cached_jpeg = b"\xff\xd8\xff\xe0cached_catalog_frame\xff\xd9"
        fresh_jpeg_1 = b"\xff\xd8\xff\xe0fresh_after_catalog_1\xff\xd9"
        fresh_jpeg_2 = b"\xff\xd8\xff\xe0fresh_after_catalog_2\xff\xd9"
        placeholder_png = b"\x89PNG" + (b"\x00" * (srv.HA_NEST_PLACEHOLDER_LEN - 4))

        cfg_vals = {
            "HA_BASE_URL": "http://198.51.100.10:8123",
            "HA_ACCESS_TOKEN": "test_ha_tok",
            "GO2RTC_BASE_URL": "http://198.51.100.10:1984",
        }

        motion_ts = ["2026-11-12T18:00:00+00:00"]
        ha_proxy_payload = [placeholder_png]
        snapshot_payload = [fresh_jpeg_1]
        upstream_snap_hits = []

        def make_resp(payload, ctype="image/jpeg"):
            r = MagicMock()
            r.read.return_value = payload
            r.headers = {"Content-Type": ctype}
            r.__enter__.return_value = r
            r.__exit__.return_value = False
            return r

        def fake_ha_request(method, path, timeout=5):
            return [
                {
                    "entity_id": "camera.entry_cam",
                    "state": "streaming",
                    "attributes": {"friendly_name": "Entry Cam"},
                },
                {
                    "entity_id": "event.entry_cam_motion",
                    "state": motion_ts[0],
                },
            ]

        def fake_urlopen(req, timeout=5):
            url = req.full_url if hasattr(req, "full_url") else str(req)
            if "/api/states/event.entry_cam_motion" in url:
                return make_resp(
                    json.dumps({"state": motion_ts[0]}).encode("utf-8"), "application/json"
                )
            if "/api/camera_proxy/" in url:
                upstream_snap_hits.append(url)
                ctype = "image/jpeg" if ha_proxy_payload[0] != placeholder_png else "image/png"
                return make_resp(ha_proxy_payload[0], ctype)
            if "/api/frame.jpeg" in url or "/api/camera_proxy_stream/" in url:
                upstream_snap_hits.append(url)
                return make_resp(snapshot_payload[0], "image/jpeg")
            raise RuntimeError(f"Unexpected URL: {url}")

        entry_cfg = [
            {
                "id": "entry_cam",
                "entity_id": "camera.entry_cam",
                "go2rtc_src": "entry_cam",
                "event_driven": True,
                "event_entities": ["event.entry_cam_motion"],
            }
        ]
        t0 = 1794585000.0
        with (
            patch.dict(srv.CACHE, {}, clear=True),
            patch.dict(srv.CAMERA_SNAP_CACHE, {}, clear=True),
            patch.dict(srv.CAMERA_FAILURE_COOLDOWN, {}, clear=True),
            patch.dict(srv.CAMERA_FAILURE_STREAK, {}, clear=True),
            patch.dict(
                srv.CAMERA_LAST_GOOD_FRAME,
                {"entry_cam": (t0 - 120.0, cached_jpeg, "image/jpeg")},
                clear=True,
            ),
            patch.dict(srv.CAMERA_LAST_EVENT_STATE, {}, clear=True),
            patch.dict(srv._CAMERA_EVENT_EPOCH, {}, clear=True),
            patch.object(srv, "CAMERA_PENDING_SNAPSHOT_REFRESH", set()),
            patch.object(srv, "_get_configured_cameras", return_value=entry_cfg),
            patch.object(srv, "_cfg", side_effect=lambda k, d="": cfg_vals.get(k, d)),
            patch.object(srv, "_ha_request", side_effect=fake_ha_request),
            patch("urllib.request.urlopen", side_effect=fake_urlopen),
        ):
            # 1. Cold catalog poll seeds baseline event state without marking refresh pending
            with patch.object(srv.time, "time", return_value=t0):
                srv.fetch_cameras_catalog()
            self.assertNotIn("entry_cam", srv.CAMERA_PENDING_SNAPSHOT_REFRESH)

            # 2. Motion event advances and catalog poll detects it first;
            # catalog poll must keep snapshot refresh pending instead of consuming it.
            motion_ts[0] = "2026-11-12T18:02:00+00:00"
            srv.CACHE.clear()
            with patch.object(srv.time, "time", return_value=t0 + 15.0):
                catalog = srv.fetch_cameras_catalog()
            self.assertTrue(catalog[0]["auto_live"])
            self.assertIn("entry_cam", srv.CAMERA_PENDING_SNAPSHOT_REFRESH)

            # 3. If fetch_camera_snapshot only sees a placeholder frame (and stream fallback fails),
            # pending refresh must remain set so the subsequent poll still fetches the real frame.
            def placeholder_only_urlopen(req, timeout=5):
                url = req.full_url if hasattr(req, "full_url") else str(req)
                if "/api/states/event.entry_cam_motion" in url:
                    return make_resp(
                        json.dumps({"state": motion_ts[0]}).encode("utf-8"), "application/json"
                    )
                if "/api/camera_proxy/" in url:
                    return make_resp(placeholder_png, "image/png")
                raise TimeoutError("upstream timeout")

            with patch("urllib.request.urlopen", side_effect=placeholder_only_urlopen):
                d_retained, c_retained = srv.fetch_camera_snapshot("entry_cam", force_refresh=False)
            self.assertEqual(d_retained, cached_jpeg)
            self.assertEqual(c_retained, "image/jpeg")
            self.assertIn("entry_cam", srv.CAMERA_PENDING_SNAPSHOT_REFRESH)

            # 4. Subsequent fetch_camera_snapshot succeeds with a genuine frame and clears pending refresh
            srv.CAMERA_SNAP_CACHE.clear()
            d_fresh, c_fresh = srv.fetch_camera_snapshot("entry_cam", force_refresh=False)
            self.assertEqual(d_fresh, fresh_jpeg_1)
            self.assertEqual(c_fresh, "image/jpeg")
            self.assertGreaterEqual(len(upstream_snap_hits), 1)
            self.assertNotIn("entry_cam", srv.CAMERA_PENDING_SNAPSHOT_REFRESH)

            # 5. Subsequent snapshot fetch with unchanged event uses cached frame (no upstream hit)
            upstream_snap_hits.clear()
            srv.CAMERA_SNAP_CACHE.clear()
            d_cached, _ = srv.fetch_camera_snapshot("entry_cam", force_refresh=False)
            self.assertEqual(d_cached, fresh_jpeg_1)
            self.assertEqual(upstream_snap_hits, [])

            # 6. Advance motion event again, poll catalog first, then verify warm_camera_snapshots_once still refreshes
            motion_ts[0] = "2026-11-12T18:04:00+00:00"
            snapshot_payload[0] = fresh_jpeg_2
            srv.CACHE.clear()
            with patch.object(srv.time, "time", return_value=t0 + 30.0):
                srv.fetch_cameras_catalog()
            self.assertIn("entry_cam", srv.CAMERA_PENDING_SNAPSHOT_REFRESH)

            srv.CAMERA_WARMER_STOP_EVENT.clear()
            warmed = srv.warm_camera_snapshots_once(stagger_sec=0)
            self.assertEqual(warmed, ["entry_cam"])
            self.assertEqual(srv.CAMERA_LAST_GOOD_FRAME["entry_cam"][1], fresh_jpeg_2)
            self.assertNotIn("entry_cam", srv.CAMERA_PENDING_SNAPSHOT_REFRESH)

            # 6. Immediate second warmer pass skips camera once good frame was stored
            warmed_again = srv.warm_camera_snapshots_once(stagger_sec=0)
            self.assertEqual(warmed_again, [])

    def test_camera_go2rtc_404_cooldown_suppresses_go2rtc_and_ha_stream(self):
        import urllib.error
        from http.client import HTTPMessage

        placeholder_png = b"\x89PNG" + (b"\x00" * (srv.HA_NEST_PLACEHOLDER_LEN - 4))
        recovered_jpeg = b"\xff\xd8\xff\xe0recovered_entry_frame\xff\xd9"
        fake_cams = [
            {
                "id": "entry_cam",
                "entity_id": "camera.entry_cam",
                "go2rtc_src": "entry_cam",
                "event_driven": False,
            }
        ]
        cfg_vals = {
            "HA_BASE_URL": "http://198.51.100.10:8123",
            "HA_ACCESS_TOKEN": "test_ha_tok",
            "GO2RTC_BASE_URL": "http://198.51.100.10:1984",
            "CAMERA_FAILURE_COOLDOWN_SEC": 300.0,
        }
        go2rtc_hits = []
        stream_hits = []
        go2rtc_should_404 = [True]

        def fake_urlopen(req, timeout=5):
            url = req.full_url if hasattr(req, "full_url") else str(req)
            if "/api/camera_proxy/" in url:
                r = MagicMock()
                r.read.return_value = placeholder_png
                r.headers = {"Content-Type": "image/png"}
                r.__enter__.return_value = r
                r.__exit__.return_value = False
                return r
            if "/api/frame.jpeg" in url:
                go2rtc_hits.append((url, req.get_header("Connection")))
                if go2rtc_should_404[0]:
                    raise urllib.error.HTTPError(
                        url, 404, "Not Found", HTTPMessage(), io.BytesIO(b"stream not found")
                    )
                r = MagicMock()
                r.read.return_value = recovered_jpeg
                r.headers = {"Content-Type": "image/jpeg"}
                r.__enter__.return_value = r
                r.__exit__.return_value = False
                return r
            if "/api/camera_proxy_stream/" in url:
                stream_hits.append(url)
                raise TimeoutError("blocking stream should not be called on 404")
            raise RuntimeError(f"Unexpected URL: {url}")

        t0 = 1794590000.0
        srv.CAMERA_WARMER_STOP_EVENT.clear()
        with (
            patch.dict(srv.CAMERA_SNAP_CACHE, {}, clear=True),
            patch.dict(srv.CAMERA_LAST_GOOD_FRAME, {}, clear=True),
            patch.dict(srv.CAMERA_FAILURE_COOLDOWN, {}, clear=True),
            patch.dict(srv.CAMERA_FAILURE_STREAK, {}, clear=True),
            patch.object(srv, "_get_configured_cameras", return_value=fake_cams),
            patch.object(srv, "_cfg", side_effect=lambda k, d="": cfg_vals.get(k, d)),
            patch("urllib.request.urlopen", side_effect=fake_urlopen),
        ):
            # 1. First warmer pass hits go2rtc once, gets HTTP 404, enters 300s cooldown,
            # and does NOT fall through to HA camera_proxy_stream.
            with patch.object(srv.time, "time", return_value=t0):
                warmed1 = srv.warm_camera_snapshots_once(stagger_sec=0)
                self.assertEqual(warmed1, [])
                self.assertEqual(len(go2rtc_hits), 1)
                self.assertEqual(go2rtc_hits[0][1], "close")
                self.assertEqual(stream_hits, [])

            # 2. Subsequent warmer passes and direct force_refresh calls within 300s make
            # zero go2rtc calls and zero HA stream calls.
            with patch.object(srv.time, "time", return_value=t0 + 60.0):
                warmed2 = srv.warm_camera_snapshots_once(stagger_sec=0)
                self.assertEqual(warmed2, [])
                data_cd, _ = srv.fetch_camera_snapshot("entry_cam", force_refresh=True)
                self.assertEqual(len(data_cd), srv.HA_NEST_PLACEHOLDER_LEN)
                self.assertEqual(len(go2rtc_hits), 1)
                self.assertEqual(stream_hits, [])

            # 3. After 300s cooldown expires, go2rtc is probed again and recovers cleanly.
            go2rtc_should_404[0] = False
            with patch.object(srv.time, "time", return_value=t0 + 301.0):
                warmed3 = srv.warm_camera_snapshots_once(stagger_sec=0)
                self.assertEqual(warmed3, ["entry_cam"])
                self.assertEqual(len(go2rtc_hits), 2)
                self.assertEqual(stream_hits, [])
                self.assertEqual(srv.CAMERA_LAST_GOOD_FRAME["entry_cam"][1], recovered_jpeg)

    def test_camera_timeout_backoff_suppresses_repeated_go2rtc_and_stream_calls(self):
        placeholder_png = b"\x89PNG" + (b"\x00" * (srv.HA_NEST_PLACEHOLDER_LEN - 4))
        recovered_jpeg = b"\xff\xd8\xff\xe0recovered_after_timeout\xff\xd9"
        fake_cams = [
            {
                "id": "yard_cam",
                "entity_id": "camera.yard_cam",
                "go2rtc_src": "yard_cam",
                "event_driven": False,
            },
            {
                "id": "patio_cam",
                "entity_id": "camera.patio_cam",
                "go2rtc_src": "patio_cam",
                "event_driven": False,
            },
        ]
        cfg_vals = {
            "HA_BASE_URL": "http://198.51.100.10:8123",
            "HA_ACCESS_TOKEN": "test_ha_tok",
            "GO2RTC_BASE_URL": "http://198.51.100.10:1984",
            "CAMERA_FAILURE_COOLDOWN_SEC": 300.0,
        }
        go2rtc_hits = []
        stream_hits = []
        go2rtc_timeout_mode = [True]

        def fake_urlopen(req, timeout=5):
            url = req.full_url if hasattr(req, "full_url") else str(req)
            if "/api/camera_proxy/" in url:
                r = MagicMock()
                r.read.return_value = placeholder_png
                r.headers = {"Content-Type": "image/png"}
                r.__enter__.return_value = r
                r.__exit__.return_value = False
                return r
            if "/api/frame.jpeg" in url:
                go2rtc_hits.append(url)
                if go2rtc_timeout_mode[0]:
                    raise TimeoutError("go2rtc read timeout")
                r = MagicMock()
                r.read.return_value = recovered_jpeg
                r.headers = {"Content-Type": "image/jpeg"}
                r.__enter__.return_value = r
                r.__exit__.return_value = False
                return r
            if "/api/camera_proxy_stream/" in url:
                stream_hits.append(url)
                raise TimeoutError("ha stream read timeout")
            raise RuntimeError(f"Unexpected URL: {url}")

        t0 = 1794595000.0
        srv.CAMERA_WARMER_STOP_EVENT.clear()
        with (
            patch.dict(srv.CAMERA_SNAP_CACHE, {}, clear=True),
            patch.dict(srv.CAMERA_LAST_GOOD_FRAME, {}, clear=True),
            patch.dict(srv.CAMERA_FAILURE_COOLDOWN, {}, clear=True),
            patch.dict(srv.CAMERA_FAILURE_STREAK, {}, clear=True),
            patch.object(srv, "_get_configured_cameras", return_value=fake_cams),
            patch.object(srv, "_cfg", side_effect=lambda k, d="": cfg_vals.get(k, d)),
            patch("urllib.request.urlopen", side_effect=fake_urlopen),
        ):
            # 1. Background warmer pass 1: go2rtc times out #1; warmer skips blocking HA stream fallback.
            with patch.object(srv.time, "time", return_value=t0):
                self.assertEqual(srv.warm_camera_snapshots_once(stagger_sec=0), [])
                self.assertEqual(len(go2rtc_hits), 2)
                self.assertEqual(stream_hits, [])

            # 2. Background warmer pass 2: go2rtc times out #2 -> triggers 300s cooldown.
            with patch.object(srv.time, "time", return_value=t0 + 120.0):
                self.assertEqual(srv.warm_camera_snapshots_once(stagger_sec=0), [])
                self.assertEqual(len(go2rtc_hits), 4)
                self.assertEqual(stream_hits, [])

            # 3. During 300s cooldown (from t0+120 to t0+420), both warmer and foreground
            # snapshot calls suppress go2rtc and HA stream calls.
            with patch.object(srv.time, "time", return_value=t0 + 200.0):
                self.assertEqual(srv.warm_camera_snapshots_once(stagger_sec=0), [])
                srv.fetch_camera_snapshot("yard_cam", force_refresh=True)
                self.assertEqual(len(go2rtc_hits), 4)
                self.assertEqual(stream_hits, [])

            # 4. After 300s cooldown expires, go2rtc recovers cleanly.
            go2rtc_timeout_mode[0] = False
            with patch.object(srv.time, "time", return_value=t0 + 425.0):
                data_ok, ctype_ok = srv.fetch_camera_snapshot("yard_cam", force_refresh=True)
                self.assertEqual(data_ok, recovered_jpeg)
                self.assertEqual(ctype_ok, "image/jpeg")
                self.assertEqual(len(go2rtc_hits), 5)
                self.assertEqual(stream_hits, [])

            # 5. Foreground fetch on a stalled camera where both go2rtc and HA stream time out:
            # suppresses HA stream immediately after the first stream timeout, and puts go2rtc
            # into cooldown once go2rtc reaches the 2-timeout threshold.
            go2rtc_timeout_mode[0] = True
            go2rtc_hits.clear()
            stream_hits.clear()
            with patch.object(srv.time, "time", return_value=t0 + 500.0):
                srv.fetch_camera_snapshot("patio_cam", force_refresh=True)
                self.assertEqual(len(go2rtc_hits), 1)
                self.assertEqual(len(stream_hits), 1)
            with patch.object(srv.time, "time", return_value=t0 + 530.0):
                srv.fetch_camera_snapshot("patio_cam", force_refresh=True)
                self.assertEqual(len(go2rtc_hits), 2)
                self.assertEqual(len(stream_hits), 1)
            with patch.object(srv.time, "time", return_value=t0 + 560.0):
                srv.fetch_camera_snapshot("patio_cam", force_refresh=True)
                self.assertEqual(len(go2rtc_hits), 2)
                self.assertEqual(len(stream_hits), 1)

    def test_camera_warmer_defaults_and_cached_frame_throttling_with_battery_guard(self):
        t0 = 1794600000.0
        cached_jpeg = b"\xff\xd8\xff\xe0cached_60s_test\xff\xd9"
        fresh_jpeg = b"\xff\xd8\xff\xe0fresh_60s_test\xff\xd9"
        cfg_vals = {
            "HA_BASE_URL": "http://198.51.100.10:8123",
            "HA_ACCESS_TOKEN": "test_ha_tok",
            "GO2RTC_BASE_URL": "http://198.51.100.10:1984",
        }
        fake_cams = [
            {
                "id": "front_door",
                "entity_id": "camera.front_door",
                "battery": True,
                "event_driven": True,
                "event_entities": ["event.front_door_motion"],
            },
            {
                "id": "yard_cam",
                "entity_id": "camera.yard_cam",
                "event_driven": True,
                "event_entities": ["event.yard_cam_motion"],
            },
            {
                "id": "patio_cam",
                "entity_id": "camera.patio_cam",
                "event_driven": False,
            },
        ]

        # Verify active_refresh uses 60s default interval when not overridden.
        with (
            patch.dict(srv.CAMERA_SNAP_CACHE, {}, clear=True),
            patch.dict(
                srv.CAMERA_LAST_GOOD_FRAME,
                {"yard_cam": (t0 - 40.0, cached_jpeg, "image/jpeg")},
                clear=True,
            ),
            patch.dict(srv.CAMERA_FAILURE_COOLDOWN, {}, clear=True),
            patch.dict(srv.CAMERA_FAILURE_STREAK, {}, clear=True),
            patch.object(srv, "_get_configured_cameras", return_value=fake_cams),
            patch.object(srv, "_cfg", side_effect=lambda k, d="": cfg_vals.get(k, d)),
            patch.object(srv.time, "time", return_value=t0),
            patch("urllib.request.urlopen") as mock_urlopen,
        ):
            # 40s old (< 60s default) -> served from cache without waking upstream
            d_cached, _ = srv.fetch_camera_snapshot(
                "yard_cam", force_refresh=False, active_refresh=True
            )
            self.assertEqual(d_cached, cached_jpeg)
            mock_urlopen.assert_not_called()

        # Verify background warmer skips cached non-battery cameras (< 300s old), refreshes stale
        # non-battery cameras (>= 300s old), respects CAMERA_WARMER_ENABLED=false, and NEVER wakes
        # battery camera on cache age regardless of staleness.
        fetch_calls = []

        def fake_fetch(cam_id, force_refresh=False):
            fetch_calls.append((cam_id, force_refresh))
            return fresh_jpeg, "image/jpeg"

        with (
            patch.dict(
                srv.CAMERA_LAST_GOOD_FRAME,
                {
                    "front_door": (t0 - 3600.0, cached_jpeg, "image/jpeg"),
                    "yard_cam": (t0 - 120.0, cached_jpeg, "image/jpeg"),
                    "patio_cam": (t0 - 120.0, cached_jpeg, "image/jpeg"),
                },
                clear=True,
            ),
            patch.dict(srv.CAMERA_FAILURE_COOLDOWN, {}, clear=True),
            patch.dict(srv.CAMERA_FAILURE_STREAK, {}, clear=True),
            patch.object(srv, "_get_configured_cameras", return_value=fake_cams),
            patch.object(srv, "_has_camera_event_changed", return_value=False),
            patch.object(srv, "fetch_camera_snapshot", side_effect=fake_fetch),
            patch.object(srv.time, "time", return_value=t0),
        ):
            # Disabled flag returns immediately
            with patch.object(
                srv,
                "_cfg",
                side_effect=lambda k, d="": (
                    "false" if k == "CAMERA_WARMER_ENABLED" else cfg_vals.get(k, d)
                ),
            ):
                self.assertEqual(srv.warm_camera_snapshots_once(stagger_sec=0), [])
                self.assertEqual(fetch_calls, [])

            # Cached frames < 300s old on non-battery cameras (and 3600s old on battery camera) are all skipped
            with patch.object(srv, "_cfg", side_effect=lambda k, d="": cfg_vals.get(k, d)):
                self.assertEqual(srv.warm_camera_snapshots_once(stagger_sec=0), [])
                self.assertEqual(fetch_calls, [])

                # Age non-battery cameras past 300s background refresh threshold -> refreshes wired cameras ONLY;
                # battery camera remains strictly untouched.
                srv.CAMERA_LAST_GOOD_FRAME["yard_cam"] = (
                    t0 - 305.0,
                    cached_jpeg,
                    "image/jpeg",
                )
                srv.CAMERA_LAST_GOOD_FRAME["patio_cam"] = (t0 - 305.0, cached_jpeg, "image/jpeg")
                warmed = srv.warm_camera_snapshots_once(stagger_sec=0)
                self.assertEqual(warmed, ["yard_cam", "patio_cam"])
                self.assertEqual(
                    fetch_calls,
                    [("yard_cam", True), ("patio_cam", True)],
                )

    def test_control_music_playback_shuffle_toggles_and_shuffles_remaining_queue(self):
        t0 = {"video_id": "vid00000000", "title": "Track 0", "artist": "A0"}
        t1 = {"video_id": "vid11111111", "title": "Track 1", "artist": "A1"}
        t2 = {"video_id": "vid22222222", "title": "Track 2", "artist": "A2"}
        t3 = {"video_id": "vid33333333", "title": "Track 3", "artist": "A3"}
        with (
            patch.dict(
                srv.MUSIC_STATE,
                {
                    "active_speaker": "this_device",
                    "local_state": "playing",
                    "local_volume": 35,
                    "playing_video_id": "vid11111111",
                    "queue": [t0, t1, t2, t3],
                    "queue_index": 1,
                    "playlist_title": "Test Playlist",
                    "shuffle": False,
                },
            ),
            patch("random.shuffle", side_effect=lambda lst: lst.reverse()) as mock_shuffle,
        ):
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

    def test_control_music_playback_shuffle_atomic_lock_and_index_clamping(self):
        t0 = {"video_id": "vid00000000", "title": "Track 0", "artist": "A0"}
        t1 = {"video_id": "vid11111111", "title": "Track 1", "artist": "A1"}
        t2 = {"video_id": "vid22222222", "title": "Track 2", "artist": "A2"}
        t3 = {"video_id": "vid33333333", "title": "Track 3", "artist": "A3"}

        class _CountingLock:
            def __init__(self, inner):
                self._inner = inner
                self.enter_count = 0

            def __enter__(self):
                self.enter_count += 1
                return self._inner.__enter__()

            def __exit__(self, exc_type, exc_val, exc_tb):
                return self._inner.__exit__(exc_type, exc_val, exc_tb)

        counting_lock = _CountingLock(srv.MUSIC_STATE_LOCK)
        with (
            patch.object(srv, "MUSIC_STATE_LOCK", counting_lock),
            patch.dict(
                srv.MUSIC_STATE,
                {
                    "active_speaker": "this_device",
                    "local_state": "playing",
                    "local_volume": 35,
                    "playing_video_id": "vid00000000",
                    "queue": [t0, t1, t2, t3],
                    "queue_index": -2,
                    "playlist_title": "Test Playlist",
                    "shuffle": False,
                },
            ),
            patch("random.shuffle", side_effect=lambda lst: lst.reverse()),
            patch.object(srv, "get_music_dashboard_state", return_value={"ok": True}),
        ):
            ok, st = srv.control_music_playback({"action": "shuffle"})
            self.assertTrue(ok)
            self.assertEqual(st, {"ok": True})
            # Single lock acquisition covers reading live queue/index and mutating shuffle/queue
            self.assertEqual(counting_lock.enter_count, 1)
            # Negative queue_index clamps to 0, preserving t0 and shuffling remaining [t1, t2, t3]
            self.assertEqual(srv.MUSIC_STATE["queue"], [t0, t3, t2, t1])

    def test_exchange_camera_webrtc_closes_http_error_and_falls_through(self):
        import urllib.error
        from http.client import HTTPMessage

        target = {
            "id": "entry_cam",
            "go2rtc_src": "primary_src",
            "aliases": ["camera.alias_src"],
        }
        closed_flags = []

        class TrackedHTTPError(urllib.error.HTTPError):
            def close(self):
                closed_flags.append(True)
                super().close()

        def fake_urlopen(req, timeout=10):
            url = req.full_url if hasattr(req, "full_url") else str(req)
            if "src=primary_src" in url:
                raise TrackedHTTPError(
                    url, 404, "Not Found", HTTPMessage(), io.BytesIO(b"not found")
                )
            if "src=entry_cam" in url:
                r = MagicMock()
                r.read.return_value = b"v=0\r\nsdp_answer_ok"
                r.__enter__.return_value = r
                r.__exit__.return_value = False
                return r
            raise RuntimeError(f"Unexpected URL: {url}")

        with (
            patch.object(srv, "resolve_camera_target", return_value=target),
            patch.object(srv, "_get_go2rtc_base_url", return_value="http://198.51.100.10:1984"),
            patch("urllib.request.urlopen", side_effect=fake_urlopen),
        ):
            ok, payload, status = srv.exchange_camera_webrtc("entry_cam", "v=0\r\noffer")
            self.assertTrue(ok)
            self.assertEqual(payload, "v=0\r\nsdp_answer_ok")
            self.assertEqual(status, 200)
            self.assertEqual(closed_flags, [True])

        # When all candidates raise HTTPError, all errors are closed and 502 is returned
        closed_flags.clear()

        def fail_all_urlopen(req, timeout=10):
            url = req.full_url if hasattr(req, "full_url") else str(req)
            raise TrackedHTTPError(url, 500, "Upstream Boom", HTTPMessage(), io.BytesIO(b"boom"))

        with (
            patch.object(srv, "resolve_camera_target", return_value=target),
            patch.object(srv, "_get_go2rtc_base_url", return_value="http://198.51.100.10:1984"),
            patch("urllib.request.urlopen", side_effect=fail_all_urlopen),
        ):
            ok, payload, status = srv.exchange_camera_webrtc("entry_cam", "v=0\r\noffer")
            self.assertFalse(ok)
            self.assertIn("500", payload)
            self.assertEqual(status, 502)
            self.assertEqual(len(closed_flags), 3)

    def test_go2rtc_snapshot_timeout_default_and_override(self):
        good_jpeg = b"\xff\xd8\xff\xe0timeout_test_frame\xff\xd9"
        placeholder_png = b"\x89PNG" + (b"\x00" * (srv.HA_NEST_PLACEHOLDER_LEN - 4))
        fake_cams = [
            {
                "id": "yard_cam",
                "entity_id": "camera.yard_cam",
                "go2rtc_src": "yard_cam",
                "event_driven": False,
            }
        ]
        cfg_vals = {
            "HA_BASE_URL": "http://198.51.100.10:8123",
            "HA_ACCESS_TOKEN": "test_ha_tok",
            "GO2RTC_BASE_URL": "http://198.51.100.10:1984",
        }
        observed_timeouts = []

        def fake_urlopen(req, timeout=5):
            url = req.full_url if hasattr(req, "full_url") else str(req)
            if "/api/camera_proxy/" in url:
                r = MagicMock()
                r.read.return_value = placeholder_png
                r.headers = {"Content-Type": "image/png"}
                r.__enter__.return_value = r
                r.__exit__.return_value = False
                return r
            if "/api/frame.jpeg" in url:
                observed_timeouts.append(timeout)
                r = MagicMock()
                r.read.return_value = good_jpeg
                r.headers = {"Content-Type": "image/jpeg"}
                r.__enter__.return_value = r
                r.__exit__.return_value = False
                return r
            raise RuntimeError(f"Unexpected URL: {url}")

        with (
            patch.dict(srv.CAMERA_SNAP_CACHE, {}, clear=True),
            patch.dict(srv.CAMERA_LAST_GOOD_FRAME, {}, clear=True),
            patch.dict(srv.CAMERA_FAILURE_COOLDOWN, {}, clear=True),
            patch.dict(srv.CAMERA_FAILURE_STREAK, {}, clear=True),
            patch.object(srv, "_get_configured_cameras", return_value=fake_cams),
            patch.object(srv, "_cfg", side_effect=lambda k, d="": cfg_vals.get(k, d)),
            patch("urllib.request.urlopen", side_effect=fake_urlopen),
        ):
            data, ctype = srv.fetch_camera_snapshot("yard_cam", force_refresh=True)
            self.assertEqual(data, good_jpeg)
            self.assertEqual(ctype, "image/jpeg")
            self.assertEqual(observed_timeouts, [20.0])

            cfg_vals["GO2RTC_SNAPSHOT_TIMEOUT_SEC"] = "27.5"
            data2, _ = srv.fetch_camera_snapshot("yard_cam", force_refresh=True)
            self.assertEqual(data2, good_jpeg)
            self.assertEqual(observed_timeouts, [20.0, 27.5])

            cfg_vals["GO2RTC_SNAPSHOT_TIMEOUT_SEC"] = "invalid"
            srv.fetch_camera_snapshot("yard_cam", force_refresh=True)
            self.assertEqual(observed_timeouts, [20.0, 27.5, 20.0])

    def test_camera_exponential_backoff_and_reset_on_success(self):
        good_jpeg = b"\xff\xd8\xff\xe0recovered_backoff\xff\xd9"
        placeholder_png = b"\x89PNG" + (b"\x00" * (srv.HA_NEST_PLACEHOLDER_LEN - 4))
        fake_cams = [
            {
                "id": "yard_cam",
                "entity_id": "camera.yard_cam",
                "go2rtc_src": "yard_cam",
                "event_driven": False,
            }
        ]
        cfg_vals = {
            "HA_BASE_URL": "http://198.51.100.10:8123",
            "HA_ACCESS_TOKEN": "test_ha_tok",
            "GO2RTC_BASE_URL": "http://198.51.100.10:1984",
            "CAMERA_FAILURE_COOLDOWN_SEC": 300.0,
            "CAMERA_FAILURE_COOLDOWN_MAX_SEC": 3600.0,
        }
        go2rtc_hits = []
        should_timeout = [True]

        def fake_urlopen(req, timeout=5):
            url = req.full_url if hasattr(req, "full_url") else str(req)
            if "/api/camera_proxy/" in url:
                r = MagicMock()
                r.read.return_value = placeholder_png
                r.headers = {"Content-Type": "image/png"}
                r.__enter__.return_value = r
                r.__exit__.return_value = False
                return r
            if "/api/frame.jpeg" in url:
                go2rtc_hits.append(url)
                if should_timeout[0]:
                    raise TimeoutError("go2rtc keyframe timeout")
                r = MagicMock()
                r.read.return_value = good_jpeg
                r.headers = {"Content-Type": "image/jpeg"}
                r.__enter__.return_value = r
                r.__exit__.return_value = False
                return r
            if "/api/camera_proxy_stream/" in url:
                raise TimeoutError("stream timeout")
            raise RuntimeError(f"Unexpected URL: {url}")

        t0 = 1794610000.0
        with (
            patch.dict(srv.CAMERA_SNAP_CACHE, {}, clear=True),
            patch.dict(srv.CAMERA_LAST_GOOD_FRAME, {}, clear=True),
            patch.dict(srv.CAMERA_FAILURE_COOLDOWN, {}, clear=True),
            patch.dict(srv.CAMERA_FAILURE_STREAK, {}, clear=True),
            patch.object(srv, "_get_configured_cameras", return_value=fake_cams),
            patch.object(srv, "_cfg", side_effect=lambda k, d="": cfg_vals.get(k, d)),
            patch("urllib.request.urlopen", side_effect=fake_urlopen),
        ):
            # Cycle 1: 2 timeouts -> 300s cooldown
            with patch.object(srv.time, "time", return_value=t0):
                srv.fetch_camera_snapshot("yard_cam", force_refresh=True)
            with patch.object(srv.time, "time", return_value=t0 + 60.0):
                srv.fetch_camera_snapshot("yard_cam", force_refresh=True)
            self.assertAlmostEqual(srv.CAMERA_FAILURE_COOLDOWN["camera:yard_cam"], t0 + 360.0)
            self.assertAlmostEqual(srv.CAMERA_FAILURE_COOLDOWN["go2rtc:yard_cam"], t0 + 360.0)
            self.assertTrue(srv._is_in_failure_cooldown("camera:yard_cam", now=t0 + 359.0))

            # Cycle 2: after 300s expires, 2 more timeouts -> 900s cooldown
            t1 = t0 + 361.0
            with patch.object(srv.time, "time", return_value=t1):
                srv.fetch_camera_snapshot("yard_cam", force_refresh=True)
            with patch.object(srv.time, "time", return_value=t1 + 60.0):
                srv.fetch_camera_snapshot("yard_cam", force_refresh=True)
            self.assertAlmostEqual(srv.CAMERA_FAILURE_COOLDOWN["camera:yard_cam"], t1 + 960.0)
            self.assertAlmostEqual(srv.CAMERA_FAILURE_COOLDOWN["go2rtc:yard_cam"], t1 + 960.0)
            self.assertTrue(srv._is_in_failure_cooldown("camera:yard_cam", now=t1 + 959.0))

            # Cycle 3: after 900s expires, 2 more timeouts -> 3600s cooldown (capped)
            t2 = t1 + 961.0
            with patch.object(srv.time, "time", return_value=t2):
                srv.fetch_camera_snapshot("yard_cam", force_refresh=True)
            with patch.object(srv.time, "time", return_value=t2 + 60.0):
                srv.fetch_camera_snapshot("yard_cam", force_refresh=True)
            self.assertAlmostEqual(srv.CAMERA_FAILURE_COOLDOWN["camera:yard_cam"], t2 + 3660.0)
            self.assertAlmostEqual(srv.CAMERA_FAILURE_COOLDOWN["go2rtc:yard_cam"], t2 + 3660.0)
            self.assertTrue(srv._is_in_failure_cooldown("camera:yard_cam", now=t2 + 3659.0))

            # Cycle 4: still capped at 3600s
            t3 = t2 + 3661.0
            with patch.object(srv.time, "time", return_value=t3):
                srv.fetch_camera_snapshot("yard_cam", force_refresh=True)
            with patch.object(srv.time, "time", return_value=t3 + 60.0):
                srv.fetch_camera_snapshot("yard_cam", force_refresh=True)
            self.assertAlmostEqual(srv.CAMERA_FAILURE_COOLDOWN["camera:yard_cam"], t3 + 3660.0)

            # Recovery clears escalation state; subsequent timeouts start back at 300s
            should_timeout[0] = False
            t4 = t3 + 3661.0
            with patch.object(srv.time, "time", return_value=t4):
                data_ok, _ = srv.fetch_camera_snapshot("yard_cam", force_refresh=True)
            self.assertEqual(data_ok, good_jpeg)
            self.assertNotIn("camera:yard_cam", srv.CAMERA_FAILURE_STREAK)
            self.assertNotIn("go2rtc:yard_cam", srv.CAMERA_FAILURE_STREAK)

            should_timeout[0] = True
            t5 = t4 + 10.0
            with patch.object(srv.time, "time", return_value=t5):
                srv.fetch_camera_snapshot("yard_cam", force_refresh=True)
            with patch.object(srv.time, "time", return_value=t5 + 60.0):
                srv.fetch_camera_snapshot("yard_cam", force_refresh=True)
            self.assertAlmostEqual(srv.CAMERA_FAILURE_COOLDOWN["camera:yard_cam"], t5 + 360.0)
            self.assertAlmostEqual(srv.CAMERA_FAILURE_COOLDOWN["go2rtc:yard_cam"], t5 + 360.0)

    def test_camera_go2rtc_alias_404_applies_24h_cooldown(self):
        import urllib.error
        from http.client import HTTPMessage

        placeholder_png = b"\x89PNG" + (b"\x00" * (srv.HA_NEST_PLACEHOLDER_LEN - 4))
        fake_cams = [
            {
                "id": "yard_cam",
                "entity_id": "camera.yard_cam",
                "go2rtc_src": "yard_cam",
                "aliases": ["yard_cam_legacy"],
                "event_driven": False,
            }
        ]
        cfg_vals = {
            "HA_BASE_URL": "http://198.51.100.10:8123",
            "HA_ACCESS_TOKEN": "test_ha_tok",
            "GO2RTC_BASE_URL": "http://198.51.100.10:1984",
            "CAMERA_FAILURE_COOLDOWN_SEC": 300.0,
        }
        probed_srcs = []

        def fake_urlopen(req, timeout=5):
            url = req.full_url if hasattr(req, "full_url") else str(req)
            if "/api/camera_proxy/" in url:
                r = MagicMock()
                r.read.return_value = placeholder_png
                r.headers = {"Content-Type": "image/png"}
                r.__enter__.return_value = r
                r.__exit__.return_value = False
                return r
            if "/api/frame.jpeg" in url:
                src = urllib.parse.parse_qs(urllib.parse.urlparse(url).query).get("src", [""])[0]
                probed_srcs.append(src)
                if src == "yard_cam_legacy":
                    raise urllib.error.HTTPError(
                        url, 404, "Not Found", HTTPMessage(), io.BytesIO(b"stream not found")
                    )
                raise TimeoutError("primary stream timeout")
            if "/api/camera_proxy_stream/" in url:
                raise TimeoutError("stream timeout")
            raise RuntimeError(f"Unexpected URL: {url}")

        t0 = 1794620000.0
        with (
            patch.dict(srv.CAMERA_SNAP_CACHE, {}, clear=True),
            patch.dict(srv.CAMERA_LAST_GOOD_FRAME, {}, clear=True),
            patch.dict(srv.CAMERA_FAILURE_COOLDOWN, {}, clear=True),
            patch.dict(srv.CAMERA_FAILURE_STREAK, {}, clear=True),
            patch.object(srv, "_get_configured_cameras", return_value=fake_cams),
            patch.object(srv, "_cfg", side_effect=lambda k, d="": cfg_vals.get(k, d)),
            patch("urllib.request.urlopen", side_effect=fake_urlopen),
        ):
            # Pass 1: primary times out, alias returns 404 -> alias enters 86400s cooldown
            with patch.object(srv.time, "time", return_value=t0):
                srv.fetch_camera_snapshot("yard_cam", force_refresh=True)
            self.assertEqual(probed_srcs, ["yard_cam", "yard_cam_legacy"])
            self.assertAlmostEqual(
                srv.CAMERA_FAILURE_COOLDOWN["go2rtc:yard_cam_legacy"], t0 + 86400.0
            )

            # Pass 2 & later passes within 24h: alias is never probed again even when primary retries
            probed_srcs.clear()
            with patch.object(srv.time, "time", return_value=t0 + 60.0):
                srv.fetch_camera_snapshot("yard_cam", force_refresh=True)
            with patch.object(srv.time, "time", return_value=t0 + 365.0):
                srv.fetch_camera_snapshot("yard_cam", force_refresh=True)
            self.assertEqual(probed_srcs, ["yard_cam", "yard_cam"])
            self.assertTrue(srv._is_in_failure_cooldown("go2rtc:yard_cam_legacy", now=t0 + 86399.0))

            # After 24h expires, alias may be probed again
            probed_srcs.clear()
            with patch.object(srv.time, "time", return_value=t0 + 86401.0):
                srv.fetch_camera_snapshot("yard_cam", force_refresh=True)
            self.assertEqual(probed_srcs, ["yard_cam", "yard_cam_legacy"])

    def test_camera_backoff_decays_after_idle_gap_and_honors_max_below_base(self):
        self.assertEqual(srv._compute_backoff_cooldown(300.0, 0, 60.0), 60.0)
        self.assertEqual(srv._compute_backoff_cooldown(300.0, 2, 60.0), 60.0)

        t0 = 1794630000.0
        cfg_vals = {
            "CAMERA_FAILURE_COOLDOWN_SEC": 300.0,
            "CAMERA_FAILURE_COOLDOWN_MAX_SEC": 3600.0,
        }
        with (
            patch.dict(srv.CAMERA_FAILURE_COOLDOWN, {}, clear=True),
            patch.dict(srv.CAMERA_FAILURE_STREAK, {}, clear=True),
            patch.object(srv, "_cfg", side_effect=lambda k, d="": cfg_vals.get(k, d)),
        ):
            # Cycle 1: 2 failures -> 300s cooldown, escalations=1
            self.assertFalse(srv._record_upstream_failure("camera:idle_cam", now=t0))
            self.assertTrue(srv._record_upstream_failure("camera:idle_cam", now=t0 + 10.0))
            self.assertAlmostEqual(srv.CAMERA_FAILURE_COOLDOWN["camera:idle_cam"], t0 + 310.0)

            # Long idle gap past cooldown (300s) + grace window (300s) -> escalation decays to 0
            t_idle = t0 + 10.0 + 601.0
            self.assertFalse(srv._record_upstream_failure("camera:idle_cam", now=t_idle))
            self.assertTrue(srv._record_upstream_failure("camera:idle_cam", now=t_idle + 10.0))
            self.assertAlmostEqual(
                srv.CAMERA_FAILURE_COOLDOWN["camera:idle_cam"], t_idle + 10.0 + 300.0
            )


if __name__ == "__main__":
    unittest.main()

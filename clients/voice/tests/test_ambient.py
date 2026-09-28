"""Tests for wake_mode "ambient" / "both" (SPEC-011 classifier-gated ambient wake)."""

import http.server
import json
import os
import struct
import tempfile
import threading
import time
import unittest
from email.parser import BytesParser
from unittest.mock import patch

from clients.voice.config import VoiceConfig
from clients.voice.client import VoiceDaemon


class MockModel:
    """Mock openWakeWord Model for deterministic unit testing."""

    def __init__(self, score_map=None):
        self.models = {"hey_jarvis": None, "hey_aerial": None}
        self.score_map = score_map or {}
        self.reset_called = False
        self.preprocessor = None

    def predict(self, chunk):
        return self.score_map

    def reset(self):
        self.reset_called = True


class FakeAmbientServer:
    """A real HTTP server exercising the multipart POST + JSON response contract."""

    def __init__(self, response_body=None, status=200, delay=0.0):
        self.response_body = response_body if response_body is not None else {
            "engage": False,
            "transcript": "",
            "score": 0.0,
            "classifier": "none",
        }
        self.status = status
        self.delay = delay
        self.requests = []  # list of (node_id, audio_bytes)
        self._server = None
        self._thread = None

    def _make_handler(self):
        outer = self

        class Handler(http.server.BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def do_POST(self):
                try:
                    length = int(self.headers.get("Content-Length", 0))
                    raw = self.rfile.read(length)
                    if outer.delay:
                        time.sleep(outer.delay)

                    content_type = self.headers.get("Content-Type", "")
                    msg_bytes = b"Content-Type: " + content_type.encode("utf-8") + b"\r\n\r\n" + raw
                    msg = BytesParser().parsebytes(msg_bytes)

                    node_id = None
                    audio = None
                    if msg.is_multipart():
                        for part in msg.get_payload():
                            name = part.get_param("name", header="Content-Disposition")
                            if name == "node_id":
                                node_id = part.get_payload(decode=True).decode("utf-8")
                            elif name == "audio":
                                audio = part.get_payload(decode=True)
                    outer.requests.append((node_id, audio))

                    body = json.dumps(outer.response_body).encode("utf-8")
                    self.send_response(outer.status)
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(body)))
                    self.end_headers()
                    self.wfile.write(body)
                except Exception:
                    pass

        return Handler

    def start(self):
        self._server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), self._make_handler())
        self._thread = threading.Thread(target=self._server.serve_forever, daemon=True)
        self._thread.start()

    @property
    def url(self):
        host, port = self._server.server_address
        return f"http://{host}:{port}/ambient"

    def stop(self):
        self._server.shutdown()
        self._server.server_close()


class TestAmbientMode(unittest.TestCase):
    def setUp(self):
        self.temp_dir = tempfile.mkdtemp()
        self.save_path = os.path.join(self.temp_dir, "ambient_utterance.wav")

    def tearDown(self):
        if os.path.exists(self.save_path):
            os.unlink(self.save_path)
        if os.path.exists(self.temp_dir):
            os.rmdir(self.temp_dir)

    def _cfg(self, wake_mode="ambient", server_url="", **overrides):
        kwargs = dict(
            wake_mode=wake_mode,
            ambient_url=server_url,
            ambient_timeout_seconds=2.0,
            save_path=self.save_path,
            silence_ms=100,
            speech_threshold_db=-30.0,
            cooldown_seconds=1.0,
            mirrormere_url="http://test-core/kiosk",
            hub_url="http://test-hub/api/voice/interact",
        )
        kwargs.update(overrides)
        return VoiceConfig(**kwargs)

    @staticmethod
    def _speech_frame():
        return struct.pack("<1280h", *([10000] * 1280))

    @staticmethod
    def _silence_frame():
        return struct.pack("<1280h", *([0] * 1280))

    def _feed_segment(self, daemon, voiced_seconds=0.5, silence_ms=None):
        """Starts an ambient segment, backdates its voiced span/silence, then ends it.

        Real wall-clock time between process_frame() calls in a test is a
        few microseconds, far below the 0.4s minimum / silence_ms gate, so
        the voiced span and silence-start are backdated directly (mirroring
        the pattern test_client.py uses for the wake-word path).
        """
        event = daemon.process_frame(self._speech_frame())
        if daemon.state == "ambient_listening":
            daemon.ambient_first_voice_at = daemon.ambient_last_voice_at - voiced_seconds
            limit = (silence_ms if silence_ms is not None else daemon.cfg.silence_ms) / 1000.0
            daemon.ambient_silence_start = time.time() - (limit + 0.05)
        return daemon.process_frame(self._silence_frame()) or event

    @staticmethod
    def _wait_until(cond, timeout=2.0):
        deadline = time.time() + timeout
        while time.time() < deadline:
            if cond():
                return True
            time.sleep(0.01)
        return False

    # --- engage -> hub call with the same bytes -----------------------------

    @patch("clients.voice.client.post_voice_state")
    def test_engage_dispatches_hub_with_same_bytes(self, mock_post_state):
        server = FakeAmbientServer(response_body={
            "engage": True,
            "transcript": "turn on the lights",
            "score": 0.91,
            "classifier": "test-classifier",
        })
        server.start()
        try:
            cfg = self._cfg(server_url=server.url)
            daemon = VoiceDaemon(cfg)
            with patch.object(daemon, "dispatch_hub_interaction") as mock_dispatch:
                event = self._feed_segment(daemon)
                self.assertEqual(event, "ambient_segment_dispatched")
                self.assertTrue(self._wait_until(lambda: not daemon.ambient_inflight))

            self.assertEqual(len(server.requests), 1)
            node_id, audio_sent = server.requests[0]
            self.assertEqual(node_id, cfg.node_id)
            self.assertTrue(audio_sent.startswith(b"RIFF"))

            mock_dispatch.assert_called_once_with(cfg.save_path)
            with open(cfg.save_path, "rb") as f:
                saved = f.read()
            self.assertEqual(saved, audio_sent, "hub must receive the exact bytes the classifier saw")

            mock_post_state.assert_called_once_with(cfg.mirrormere_url, "listening")
        finally:
            server.stop()

    # --- no engage -> no hub call, no state relay ---------------------------

    @patch("clients.voice.client.post_voice_state")
    def test_no_engage_makes_no_hub_call_and_relays_no_state(self, mock_post_state):
        server = FakeAmbientServer(response_body={
            "engage": False,
            "transcript": "just some background noise",
            "score": 0.12,
            "classifier": "test-classifier",
        })
        server.start()
        try:
            cfg = self._cfg(server_url=server.url)
            daemon = VoiceDaemon(cfg)
            with patch.object(daemon, "dispatch_hub_interaction") as mock_dispatch:
                self._feed_segment(daemon)
                self.assertTrue(self._wait_until(lambda: not daemon.ambient_inflight))

            mock_dispatch.assert_not_called()
            mock_post_state.assert_not_called()
            self.assertEqual(len(server.requests), 1)
            self.assertFalse(os.path.exists(cfg.save_path))
        finally:
            server.stop()

    # --- errors / timeouts continue the capture loop ------------------------

    def test_ambient_server_error_continues_and_recovers(self):
        server = FakeAmbientServer(status=500, response_body={"engage": False})
        server.start()
        try:
            cfg = self._cfg(server_url=server.url)
            daemon = VoiceDaemon(cfg)
            with patch.object(daemon, "dispatch_hub_interaction") as mock_dispatch:
                self._feed_segment(daemon)
                self.assertTrue(self._wait_until(lambda: not daemon.ambient_inflight))
                mock_dispatch.assert_not_called()

                # The in-flight flag must clear on error so the next segment
                # is still classified rather than being dropped forever.
                self._feed_segment(daemon)
                self.assertTrue(self._wait_until(lambda: not daemon.ambient_inflight))
            self.assertEqual(len(server.requests), 2)
        finally:
            server.stop()

    def test_ambient_timeout_continues_without_crash(self):
        server = FakeAmbientServer(delay=0.3, response_body={"engage": True})
        server.start()
        try:
            cfg = self._cfg(server_url=server.url, ambient_timeout_seconds=0.05)
            daemon = VoiceDaemon(cfg)
            with patch.object(daemon, "dispatch_hub_interaction") as mock_dispatch:
                self._feed_segment(daemon)
                self.assertTrue(self._wait_until(lambda: not daemon.ambient_inflight, timeout=2.0))
                mock_dispatch.assert_not_called()
        finally:
            server.stop()

    # --- suppression during playback / cooldown -----------------------------

    def test_segments_suppressed_while_busy(self):
        cfg = self._cfg(server_url="http://127.0.0.1:1/unused")
        daemon = VoiceDaemon(cfg)
        daemon.busy = True
        with patch.object(daemon, "_dispatch_ambient_classification") as mock_classify:
            daemon.process_frame(self._speech_frame())
            self.assertEqual(daemon.state, "idle")
            mock_classify.assert_not_called()

    def test_segments_suppressed_during_post_reply_cooldown(self):
        cfg = self._cfg(server_url="http://127.0.0.1:1/unused", cooldown_seconds=5.0)
        daemon = VoiceDaemon(cfg)
        daemon.reply_ended_at = time.time()
        with patch.object(daemon, "_dispatch_ambient_classification") as mock_classify:
            daemon.process_frame(self._speech_frame())
            self.assertEqual(daemon.state, "idle")
            mock_classify.assert_not_called()

    # --- minimum segment duration -------------------------------------------

    def test_short_segment_below_minimum_is_dropped(self):
        cfg = self._cfg(server_url="http://127.0.0.1:1/unused")
        daemon = VoiceDaemon(cfg)
        with patch.object(daemon, "_dispatch_ambient_classification") as mock_classify:
            event = self._feed_segment(daemon, voiced_seconds=0.1)
        self.assertEqual(event, "ambient_segment_dropped")
        mock_classify.assert_not_called()

    # --- "both" mode routing -------------------------------------------------

    def test_both_mode_wake_word_routes_directly_and_skips_ambient(self):
        server = FakeAmbientServer(response_body={"engage": True})
        server.start()
        try:
            cfg = self._cfg(wake_mode="both", server_url=server.url, threshold=0.4)
            mock_model = MockModel(score_map={"hey_jarvis": 0.9})
            with patch("clients.voice.client.post_voice_state"):
                daemon = VoiceDaemon(cfg, model=mock_model)
                event = daemon.process_frame(self._speech_frame())
            self.assertEqual(event, "wake_detected")
            self.assertEqual(daemon.state, "listening")
            self.assertEqual(len(server.requests), 0)
        finally:
            server.stop()

    def test_both_mode_no_wake_word_routes_to_ambient(self):
        server = FakeAmbientServer(response_body={"engage": True, "transcript": "x", "score": 0.9, "classifier": "t"})
        server.start()
        try:
            cfg = self._cfg(wake_mode="both", server_url=server.url, threshold=0.9)
            mock_model = MockModel(score_map={"hey_jarvis": 0.0})
            with patch("clients.voice.client.post_voice_state"):
                daemon = VoiceDaemon(cfg, model=mock_model)
                with patch.object(daemon, "dispatch_hub_interaction") as mock_dispatch:
                    self._feed_segment(daemon)
                    self.assertTrue(self._wait_until(lambda: not daemon.ambient_inflight))
            mock_dispatch.assert_called_once_with(cfg.save_path)
        finally:
            server.stop()

    def test_both_mode_wake_word_mid_segment_discards_ambient_buffer(self):
        server = FakeAmbientServer(response_body={"engage": True})
        server.start()
        try:
            cfg = self._cfg(wake_mode="both", server_url=server.url, threshold=0.9)
            mock_model = MockModel(score_map={"hey_jarvis": 0.0})
            with patch("clients.voice.client.post_voice_state"):
                daemon = VoiceDaemon(cfg, model=mock_model)
                daemon.process_frame(self._speech_frame())
                self.assertEqual(daemon.state, "ambient_listening")

                mock_model.score_map = {"hey_jarvis": 0.95}
                event = daemon.process_frame(self._speech_frame())

            self.assertEqual(event, "wake_detected")
            self.assertEqual(daemon.state, "listening")
            self.assertEqual(daemon.ambient_buffer, [])
            # Give any stray background thread a moment; none should exist.
            time.sleep(0.05)
            self.assertEqual(len(server.requests), 0)
        finally:
            server.stop()

    # --- openwakeword mode never touches ambient_url ------------------------

    def test_openwakeword_mode_never_calls_ambient_url(self):
        server = FakeAmbientServer()
        server.start()
        try:
            cfg = self._cfg(wake_mode="openwakeword", server_url=server.url, threshold=0.9)
            mock_model = MockModel(score_map={"hey_jarvis": 0.0})
            daemon = VoiceDaemon(cfg, model=mock_model)
            for _ in range(5):
                daemon.process_frame(self._speech_frame())
            time.sleep(0.1)
            self.assertEqual(len(server.requests), 0)
        finally:
            server.stop()

    def test_ambient_mode_does_not_load_openwakeword(self):
        cfg = self._cfg(wake_mode="ambient")
        with patch.object(
            VoiceDaemon,
            "_init_openwakeword",
            side_effect=AssertionError("must not load openWakeWord models in ambient mode"),
        ):
            daemon = VoiceDaemon(cfg)
        self.assertIsNone(daemon.model)
        self.assertEqual(daemon.active_models, [])


if __name__ == "__main__":
    unittest.main()

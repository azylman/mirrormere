import io
import math
import os
import struct
import subprocess
import tempfile
import time
import unittest
from unittest.mock import MagicMock, patch

from clients.voice.config import VoiceConfig
from clients.voice.client import VoiceDaemon, post_voice_state


class MockModel:
    """Mock openWakeWord Model for deterministic unit testing."""

    def __init__(self, score_map=None):
        self.models = {"hey_jarvis": None, "hey_aerial": None}
        self.score_map = score_map or {}
        self.reset_called = False
        self.preprocessor = MagicMock()
        self.preprocessor.raw_data_buffer = MagicMock()
        self.preprocessor.feature_buffer = MagicMock()
        self.preprocessor.melspectrogram_buffer = MagicMock()

    def predict(self, chunk):
        return self.score_map

    def reset(self):
        self.reset_called = True


class TestVoiceDaemon(unittest.TestCase):
    def setUp(self):
        self.temp_dir = tempfile.mkdtemp()
        self.save_path = os.path.join(self.temp_dir, "test_utterance.wav")
        self.cfg = VoiceConfig(
            save_path=self.save_path,
            threshold=0.40,
            silence_ms=100,
            speech_threshold_db=-30.0,
            cooldown_seconds=1.0,
            mirrormere_url="http://test-core/kiosk",
            hub_url="http://test-hub/api/voice/interact",
        )

    def tearDown(self):
        if os.path.exists(self.save_path):
            os.unlink(self.save_path)
        if os.path.exists(self.temp_dir):
            os.rmdir(self.temp_dir)

    def test_compute_db(self):
        silent_bytes = struct.pack("<1280h", *([0] * 1280))
        db_silent = VoiceDaemon.compute_db(silent_bytes)
        self.assertEqual(db_silent, -100.0)

        # Full amplitude square wave (32767)
        loud_bytes = struct.pack("<1280h", *([32767] * 1280))
        db_loud = VoiceDaemon.compute_db(loud_bytes)
        self.assertAlmostEqual(db_loud, 0.0, delta=0.5)

    @patch("clients.voice.client.urllib.request.urlopen")
    def test_post_voice_state(self, mock_urlopen):
        mock_resp = MagicMock()
        mock_resp.status = 200
        mock_urlopen.return_value.__enter__.return_value = mock_resp

        res = post_voice_state("http://test-core/kiosk", "listening", transcript="hello")
        self.assertTrue(res)
        mock_urlopen.assert_called_once()
        req = mock_urlopen.call_args[0][0]
        self.assertEqual(req.full_url, "http://test-core/kiosk/api/voice/state")

    def test_post_voice_state_empty_url(self):
        self.assertFalse(post_voice_state("", "listening"))

    @patch("clients.voice.client.urllib.request.urlopen")
    def test_wake_detection_and_preroll(self, mock_urlopen):
        mock_resp = MagicMock()
        mock_resp.status = 200
        mock_urlopen.return_value.__enter__.return_value = mock_resp

        mock_model = MockModel(score_map={"hey_jarvis": 0.0})
        daemon = VoiceDaemon(self.cfg, model=mock_model)

        silent_frame = struct.pack("<1280h", *([0] * 1280))
        # Feed 3 pre-roll frames in idle
        daemon.process_frame(silent_frame)
        daemon.process_frame(silent_frame)
        daemon.process_frame(silent_frame)
        self.assertEqual(len(daemon.pre_roll), 3)

        # Trigger wake on 4th frame
        mock_model.score_map = {"hey_jarvis": 0.85}
        event = daemon.process_frame(silent_frame)

        self.assertEqual(event, "wake_detected")
        self.assertEqual(daemon.state, "listening")
        # Pre-roll frames (4) should be captured in utterance buffer
        self.assertEqual(len(daemon.utterance_buffer), 4)

    @patch("clients.voice.client.urllib.request.urlopen")
    def test_false_wake_abort(self, mock_urlopen):
        mock_resp = MagicMock()
        mock_resp.status = 200
        mock_urlopen.return_value.__enter__.return_value = mock_resp

        mock_model = MockModel(score_map={"hey_jarvis": 0.90})
        daemon = VoiceDaemon(self.cfg, model=mock_model)

        silent_frame = struct.pack("<1280h", *([0] * 1280))
        # 1. Trigger wake
        daemon.process_frame(silent_frame)
        self.assertEqual(daemon.state, "listening")

        # 2. Simulate 3.1 seconds of silence without speech
        daemon.record_start = time.time() - 3.2
        event = daemon.process_frame(silent_frame)

        self.assertEqual(event, "wake_aborted")
        self.assertEqual(daemon.state, "idle")
        self.assertTrue(mock_model.reset_called)

    @patch("clients.voice.client.urllib.request.urlopen")
    def test_utterance_completion_and_flushing(self, mock_urlopen):
        mock_resp = MagicMock()
        mock_resp.status = 200
        mock_urlopen.return_value.__enter__.return_value = mock_resp

        mock_model = MockModel(score_map={"hey_jarvis": 0.90})
        daemon = VoiceDaemon(self.cfg, model=mock_model)

        # 1. Trigger wake
        silent_frame = struct.pack("<1280h", *([0] * 1280))
        daemon.process_frame(silent_frame)
        self.assertEqual(daemon.state, "listening")

        # 2. Simulate speech frame (high amplitude 10000 -> approx -10 dBFS)
        speech_frame = struct.pack("<1280h", *([10000] * 1280))
        daemon.process_frame(speech_frame)
        self.assertTrue(daemon.has_spoken)

        # 3. Simulate silence frame starting silence interval with hub dispatch mocked
        daemon.silence_start = time.time() - 0.2  # 200ms of silence > silence_ms (100ms)
        with patch.object(daemon, "dispatch_hub_interaction") as mock_dispatch:
            event = daemon.process_frame(silent_frame)
            mock_dispatch.assert_called_once_with(self.save_path)

        self.assertEqual(event, "utterance_saved")
        self.assertEqual(daemon.state, "idle")
        self.assertTrue(os.path.exists(self.save_path))
        self.assertTrue(mock_model.reset_called)
        self.assertGreater(daemon.cooldown_until, time.time())

        # 4. Immediate frame during cooldown should NOT retrigger even with high score
        retrigger_event = daemon.process_frame(silent_frame)
        self.assertIsNone(retrigger_event)
        self.assertEqual(daemon.state, "idle")

    @patch("clients.voice.client.urllib.request.urlopen")
    def test_hub_sse_stream_worker(self, mock_urlopen):
        # Mock SSE stream lines from Hub
        sse_lines = [
            b"event: transcript\r\n",
            b'data: {"transcript": "is the garage door open"}\r\n',
            b"\r\n",
            b"event: status\r\n",
            b'data: {"status": "Checking Home Assistant..."}\r\n',
            b"\r\n",
            b"event: reply\r\n",
            b'data: {"reply": "The garage door is closed"}\r\n',
            b"\r\n",
            b"event: audio_chunk\r\n",
            b'data: {"chunk_index": 0, "audio": "ZmFrZS1hdWRpby1ieXRlcw=="}\r\n',
            b"\r\n",
            b"event: done\r\n",
            b"data: {}\r\n",
            b"\r\n",
        ]
        mock_resp = MagicMock()
        mock_resp.status = 200
        mock_resp.__iter__.return_value = iter(sse_lines)
        mock_urlopen.return_value.__enter__.return_value = mock_resp

        # Create dummy wav
        with open(self.save_path, "wb") as f:
            f.write(b"RIFFdummyWAVE")

        daemon = VoiceDaemon(self.cfg, model=MockModel())
        with patch.object(daemon, "play_audio") as mock_play:
            daemon._hub_stream_worker(self.save_path)
            mock_play.assert_called_once_with(b"fake-audio-bytes")

        # Verified mock_urlopen called for Hub POST
        self.assertEqual(mock_urlopen.call_count, 1)

    def test_play_audio_empty(self):
        daemon = VoiceDaemon(self.cfg, model=MockModel())
        self.assertFalse(daemon.play_audio(b""))

    @patch("clients.voice.client.shutil.which", return_value="/usr/bin/pw-play")
    @patch("clients.voice.client.subprocess.Popen")
    def test_play_audio_success(self, mock_popen, mock_which):
        proc = MagicMock()
        proc.communicate.return_value = (b"", b"")
        proc.returncode = 0
        mock_popen.return_value = proc

        daemon = VoiceDaemon(self.cfg, model=MockModel())
        self.assertTrue(daemon.play_audio(b"fake-mp3-bytes"))
        mock_popen.assert_called_once_with(["/usr/bin/pw-play", "-"], stdin=subprocess.PIPE, stderr=subprocess.DEVNULL)

    def test_stop_signal(self):
        mock_model = MockModel()
        daemon = VoiceDaemon(self.cfg, model=mock_model)
        self.assertTrue(daemon.running)
        daemon.stop()
        self.assertFalse(daemon.running)


if __name__ == "__main__":
    unittest.main()

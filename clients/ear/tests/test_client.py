import base64
import io
import json
import math
import os
import struct
import subprocess
import tempfile
import time
import unittest
from unittest.mock import MagicMock, call, patch

from clients.ear.config import VoiceConfig
from clients.ear.client import VoiceDaemon, post_voice_state, post_voice_heartbeat


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

    @patch("clients.ear.client.urllib.request.urlopen")
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

    @patch("clients.ear.client.urllib.request.urlopen")
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

    @patch("clients.ear.client.urllib.request.urlopen")
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

    @patch("clients.ear.client.urllib.request.urlopen")
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

    @patch("clients.ear.client.urllib.request.urlopen")
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
            b'data: {"chunk_index": 0, "data": "ZmFrZS1hdWRpby1ieXRlcw=="}\r\n',
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
        req = mock_urlopen.call_args[0][0]
        self.assertIn(b'name="node_id"\r\n\r\ntouch-kiosk-kitchen', req.data)
        self.assertNotIn(b'name="device_name"', req.data)
        self.assertNotIn(b'name="session_id"', req.data)

    @patch("clients.ear.client.urllib.request.urlopen")
    def test_hub_sse_stream_worker_multi_sentence_audio(self, mock_urlopen):
        """MANDOS sentence streaming: when the Voice Hub's configured brain
        streams sentence audio (from the karakos gateway's POST
        /ask/stream), the Hub's own POST /api/voice/interact response can
        carry several audio_chunk events for one turn, one per sentence,
        is_final true only on the last (a completion marker). This test
        pins the dock's existing behavior against that shape:
        _hub_stream_worker must play each audio_chunk, in order, via the
        same blocking play_audio() call it already uses for a single
        chunk — no client code change needed for correctness, this test
        just pins that behavior so a future refactor can't silently drop or
        reorder chunks."""
        sse_lines = [
            b"event: transcript\r\n",
            b'data: {"transcript": "tell me a story"}\r\n',
            b"\r\n",
            b"event: audio_chunk\r\n",
            b'data: {"chunk_index": 0, "is_final": false, "data": "'
            + base64.b64encode(b"sentence-one-audio")
            + b'"}\r\n',
            b"\r\n",
            b"event: audio_chunk\r\n",
            b'data: {"chunk_index": 1, "is_final": false, "data": "'
            + base64.b64encode(b"sentence-two-audio")
            + b'"}\r\n',
            b"\r\n",
            b"event: audio_chunk\r\n",
            b'data: {"chunk_index": 2, "is_final": true, "data": "'
            + base64.b64encode(b"sentence-three-audio")
            + b'"}\r\n',
            b"\r\n",
            b"event: reply\r\n",
            b'data: {"reply": "Once upon a time. Middle. The end."}\r\n',
            b"\r\n",
            b"event: done\r\n",
            b"data: {}\r\n",
            b"\r\n",
        ]
        mock_resp = MagicMock()
        mock_resp.status = 200
        mock_resp.__iter__.return_value = iter(sse_lines)
        mock_urlopen.return_value.__enter__.return_value = mock_resp

        with open(self.save_path, "wb") as f:
            f.write(b"RIFFdummyWAVE")

        daemon = VoiceDaemon(self.cfg, model=MockModel())
        with patch.object(daemon, "play_audio") as mock_play:
            daemon._hub_stream_worker(self.save_path)
            # Played in order, one call per sentence, sequential (blocking)
            # playback via the existing play_audio() path.
            self.assertEqual(
                mock_play.call_args_list,
                [
                    call(b"sentence-one-audio"),
                    call(b"sentence-two-audio"),
                    call(b"sentence-three-audio"),
                ],
            )

    def test_play_audio_empty(self):
        daemon = VoiceDaemon(self.cfg, model=MockModel())
        self.assertFalse(daemon.play_audio(b""))

    @patch("clients.ear.client.shutil.which", return_value="/usr/bin/pw-play")
    @patch("clients.ear.client.subprocess.Popen")
    def test_play_audio_success(self, mock_popen, mock_which):
        proc = MagicMock()
        proc.communicate.return_value = (b"", b"")
        proc.returncode = 0
        mock_popen.return_value = proc

        daemon = VoiceDaemon(self.cfg, model=MockModel())
        self.assertTrue(daemon.play_audio(b"fake-mp3-bytes"))
        mock_popen.assert_called_once_with(["/usr/bin/pw-play", "-"], stdin=subprocess.PIPE, stderr=subprocess.DEVNULL)

    @patch("clients.ear.client.shutil.which")
    @patch("clients.ear.client.subprocess.Popen")
    def test_start_pcm_stream_commands(self, mock_popen, mock_which):
        daemon = VoiceDaemon(self.cfg, model=MockModel())

        # pw-play command
        mock_which.return_value = "/usr/bin/pw-play"
        daemon._start_pcm_stream(sample_rate=24000, channels=1)
        mock_popen.assert_called_with(
            ["/usr/bin/pw-play", "--format=s16", "--rate=24000", "--channels=1", "-"],
            stdin=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
        )

        # aplay fallback
        mock_which.return_value = "/usr/bin/aplay"
        daemon._start_pcm_stream(sample_rate=16000, channels=2)
        mock_popen.assert_called_with(
            ["/usr/bin/aplay", "-f", "S16_LE", "-r", "16000", "-c", "2", "-"],
            stdin=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
        )

        # mpv fallback
        mock_which.return_value = "/usr/bin/mpv"
        daemon._start_pcm_stream(sample_rate=24000, channels=1)
        mock_popen.assert_called_with(
            [
                "/usr/bin/mpv",
                "--demuxer-rawaudio-format=s16le",
                "--demuxer-rawaudio-rate=24000",
                "--demuxer-rawaudio-channels=1",
                "-",
            ],
            stdin=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
        )

        # No player found
        mock_which.return_value = None
        self.assertIsNone(daemon._start_pcm_stream(24000, 1))

    @patch("clients.ear.client.urllib.request.urlopen")
    def test_hub_sse_stream_worker_pcm_stream(self, mock_urlopen):
        """Tests that format: pcm chunks spawn a single persistent player subprocess,
        pipe data to stdin, close stdin on completion, and wait for drain."""
        pcm1 = b"\x00\x01" * 100
        pcm2 = b"\x02\x03" * 100
        sse_lines = [
            b"event: audio_chunk\r\n",
            b'data: {"chunk_index": 0, "format": "pcm", "sample_rate": 24000, "channels": 1, "is_final": false, "data": "'
            + base64.b64encode(pcm1)
            + b'"}\r\n',
            b"\r\n",
            b"event: audio_chunk\r\n",
            b'data: {"chunk_index": 1, "format": "pcm", "sample_rate": 24000, "channels": 1, "is_final": false, "data": "'
            + base64.b64encode(pcm2)
            + b'"}\r\n',
            b"\r\n",
            b"event: audio_chunk\r\n",
            b'data: {"chunk_index": 2, "format": "pcm", "sample_rate": 24000, "channels": 1, "is_final": true, "data": ""}\r\n',
            b"\r\n",
            b"event: done\r\n",
            b"data: {}\r\n",
            b"\r\n",
        ]
        mock_resp = MagicMock()
        mock_resp.status = 200
        mock_resp.__iter__.return_value = iter(sse_lines)
        mock_urlopen.return_value.__enter__.return_value = mock_resp

        with open(self.save_path, "wb") as f:
            f.write(b"RIFFdummyWAVE")

        daemon = VoiceDaemon(self.cfg, model=MockModel())
        mock_proc = MagicMock()
        mock_proc.stdin = MagicMock()
        mock_proc.wait.return_value = 0

        with patch.object(daemon, "_start_pcm_stream", return_value=mock_proc) as mock_start:
            daemon._hub_stream_worker(self.save_path)
            mock_start.assert_called_once_with(24000, 1)
            self.assertEqual(mock_proc.stdin.write.call_args_list, [call(pcm1), call(pcm2)])
            self.assertEqual(mock_proc.stdin.flush.call_count, 2)
            mock_proc.stdin.close.assert_called_once()
            mock_proc.wait.assert_called_once_with(timeout=5.0)
            self.assertFalse(daemon.busy)
            self.assertGreater(daemon.reply_ended_at, 0.0)

    @patch("clients.ear.client.urllib.request.urlopen")
    def test_hub_sse_stream_worker_drain_before_clear_gating(self, mock_urlopen):
        """Tests that self.busy remains True until player.wait() returns."""
        pcm = b"\x01\x02" * 50
        sse_lines = [
            b"event: audio_chunk\r\n",
            b'data: {"chunk_index": 0, "format": "pcm", "sample_rate": 24000, "channels": 1, "is_final": true, "data": "'
            + base64.b64encode(pcm)
            + b'"}\r\n',
            b"\r\n",
            b"event: done\r\n",
            b"data: {}\r\n",
            b"\r\n",
        ]
        mock_resp = MagicMock()
        mock_resp.status = 200
        mock_resp.__iter__.return_value = iter(sse_lines)
        mock_urlopen.return_value.__enter__.return_value = mock_resp

        with open(self.save_path, "wb") as f:
            f.write(b"RIFFdummyWAVE")

        daemon = VoiceDaemon(self.cfg, model=MockModel())
        mock_proc = MagicMock()
        mock_proc.stdin = MagicMock()

        busy_during_wait = []

        def fake_wait(timeout=None):
            busy_during_wait.append(daemon.busy)
            return 0

        mock_proc.wait.side_effect = fake_wait

        with patch.object(daemon, "_start_pcm_stream", return_value=mock_proc):
            daemon._hub_stream_worker(self.save_path)

        self.assertEqual(busy_during_wait, [True], "busy flag must remain True while player.wait() executes")
        self.assertFalse(daemon.busy, "busy flag must clear after player.wait() completes")

    @patch("clients.ear.client.urllib.request.urlopen")
    def test_hub_sse_stream_worker_pcm_timeout_kill(self, mock_urlopen):
        """Tests that a hung player subprocess is killed and reaped before releasing busy."""
        pcm = b"\x01\x02" * 50
        sse_lines = [
            b"event: audio_chunk\r\n",
            b'data: {"chunk_index": 0, "format": "pcm", "sample_rate": 24000, "channels": 1, "is_final": true, "data": "'
            + base64.b64encode(pcm)
            + b'"}\r\n',
            b"\r\n",
            b"event: done\r\n",
            b"data: {}\r\n",
            b"\r\n",
        ]
        mock_resp = MagicMock()
        mock_resp.status = 200
        mock_resp.__iter__.return_value = iter(sse_lines)
        mock_urlopen.return_value.__enter__.return_value = mock_resp

        with open(self.save_path, "wb") as f:
            f.write(b"RIFFdummyWAVE")

        daemon = VoiceDaemon(self.cfg, model=MockModel())
        mock_proc = MagicMock()
        mock_proc.stdin = MagicMock()
        # First wait times out, second wait succeeds after kill()
        mock_proc.wait.side_effect = [subprocess.TimeoutExpired(cmd="pw-play", timeout=5.0), 0]

        with patch.object(daemon, "_start_pcm_stream", return_value=mock_proc):
            daemon._hub_stream_worker(self.save_path)

        mock_proc.kill.assert_called_once()
        self.assertEqual(mock_proc.wait.call_count, 2)
        self.assertFalse(daemon.busy)

    @patch("clients.ear.client.urllib.request.urlopen")
    def test_hub_sse_stream_worker_total_playback_telemetry(self, mock_urlopen):
        """Tests that last_playback_sec totals duration across multiple chunks (fixing #340)."""
        sse_lines = [
            b"event: audio_chunk\r\n",
            b'data: {"chunk_index": 0, "format": "wav", "is_final": false, "data": "'
            + base64.b64encode(b"chunk1")
            + b'"}\r\n',
            b"\r\n",
            b"event: audio_chunk\r\n",
            b'data: {"chunk_index": 1, "format": "wav", "is_final": true, "data": "'
            + base64.b64encode(b"chunk2")
            + b'"}\r\n',
            b"\r\n",
            b"event: done\r\n",
            b"data: {}\r\n",
            b"\r\n",
        ]
        mock_resp = MagicMock()
        mock_resp.status = 200
        mock_resp.__iter__.return_value = iter(sse_lines)
        mock_urlopen.return_value.__enter__.return_value = mock_resp

        with open(self.save_path, "wb") as f:
            f.write(b"RIFFdummyWAVE")

        daemon = VoiceDaemon(self.cfg, model=MockModel())
        with patch.object(daemon, "play_audio", side_effect=lambda data: (time.sleep(0.01), True)[1]):
            daemon._hub_stream_worker(self.save_path)

        self.assertGreaterEqual(daemon.last_playback_sec, 0.015)


    def test_check_wake_word_measures_eval_latency(self):
        mock_model = MockModel(score_map={"hey_jarvis": 0.1})
        daemon = VoiceDaemon(self.cfg, model=mock_model)
        self.assertEqual(daemon.last_wake_eval_ms, 0.0)
        self.assertEqual(daemon.last_speech_duration_ms, 0.0)
        self.assertEqual(daemon.last_silence_duration_ms, 0.0)
        self.assertEqual(daemon.false_wakes_count, 0)
        self.assertEqual(daemon.last_playback_sec, 0.0)

        # Measure eval latency during prediction
        silent_frame = struct.pack("<1280h", *([0] * 1280))
        with patch.object(mock_model, "predict", side_effect=lambda chunk: (time.sleep(0.002), {"hey_jarvis": 0.1})[1]):
            daemon._check_wake_word(silent_frame)

        self.assertGreater(daemon.last_wake_eval_ms, 0.0)

        # Ambient reset: preparing an ambient segment resets last_wake_eval_ms to 0.0
        daemon.last_wake_eval_ms = 42.0
        with patch.object(daemon, "_classify_ambient", return_value={"engage": False}):
            daemon._ambient_worker([silent_frame])
        self.assertEqual(daemon.last_wake_eval_ms, 0.0)

    @patch("clients.ear.client.post_voice_state")
    def test_utterance_timing_includes_preroll(self, mock_post_state):
        mock_model = MockModel(score_map={"hey_jarvis": 0.0})
        daemon = VoiceDaemon(self.cfg, model=mock_model)

        silent_frame = struct.pack("<1280h", *([0] * 1280))
        # Feed 3 pre-roll frames in idle (3 * 1280 / 16000 = 0.24s)
        daemon.process_frame(silent_frame)
        daemon.process_frame(silent_frame)
        daemon.process_frame(silent_frame)
        self.assertEqual(len(daemon.pre_roll), 3)

        # Wake detection on 4th frame
        mock_model.score_map = {"hey_jarvis": 0.90}
        event = daemon.process_frame(silent_frame)
        self.assertEqual(event, "wake_detected")
        self.assertEqual(daemon.state, "listening")

        # Speech frame
        speech_frame = struct.pack("<1280h", *([10000] * 1280))
        daemon.process_frame(speech_frame)
        self.assertTrue(daemon.has_spoken)

        # Simulate 2.0s elapsed speech and 0.2s silence
        now = time.time()
        daemon.record_start = now - 2.0
        daemon.silence_start = now - 0.2
        with patch.object(daemon, "dispatch_hub_interaction") as mock_dispatch:
            event = daemon.process_frame(silent_frame)
            self.assertEqual(event, "utterance_saved")
            mock_dispatch.assert_called_once_with(self.save_path)

        # Pre-roll is 4 frames now (4 * 1280 / 16000 = 0.32s)
        # Total elapsed = 2.0 + 0.32 = 2.32s
        # Silence duration = 0.2s
        # Speech duration = (2.32 - 0.2) = 2.12s -> 2120ms
        self.assertAlmostEqual(daemon.last_speech_duration_ms, 2120.0, delta=100.0)
        self.assertAlmostEqual(daemon.last_silence_duration_ms, 200.0, delta=100.0)

    @patch("clients.ear.client.post_voice_state")
    def test_false_wake_increments_counter(self, mock_post_state):
        mock_model = MockModel(score_map={"hey_jarvis": 0.90})
        daemon = VoiceDaemon(self.cfg, model=mock_model)
        self.assertEqual(daemon.false_wakes_count, 0)

        silent_frame = struct.pack("<1280h", *([0] * 1280))
        # 1. First false wake
        daemon.process_frame(silent_frame)
        self.assertEqual(daemon.state, "listening")
        daemon.record_start = time.time() - 3.2
        event1 = daemon.process_frame(silent_frame)
        self.assertEqual(event1, "wake_aborted")
        self.assertEqual(daemon.false_wakes_count, 1)

        # 2. Second false wake
        daemon.cooldown_until = 0.0
        daemon.process_frame(silent_frame)
        self.assertEqual(daemon.state, "listening")
        daemon.record_start = time.time() - 3.2
        event2 = daemon.process_frame(silent_frame)
        self.assertEqual(event2, "wake_aborted")
        self.assertEqual(daemon.false_wakes_count, 2)

    @patch("clients.ear.client.urllib.request.urlopen")
    def test_hub_stream_worker_attaches_timings_and_120s_timeout(self, mock_urlopen):
        mock_resp = MagicMock()
        mock_resp.status = 200
        mock_resp.__iter__.return_value = iter([b"event: done\r\ndata: {}\r\n\r\n"])
        mock_urlopen.return_value.__enter__.return_value = mock_resp

        with open(self.save_path, "wb") as f:
            f.write(b"RIFFdummyWAVE")

        daemon = VoiceDaemon(self.cfg, model=MockModel())
        daemon.last_wake_eval_ms = 45.2
        daemon.last_speech_duration_ms = 2150.3
        daemon.last_silence_duration_ms = 850.1

        daemon._hub_stream_worker(self.save_path)

        mock_urlopen.assert_called_once()
        req, kwargs = mock_urlopen.call_args[0][0], mock_urlopen.call_args[1]
        self.assertEqual(kwargs.get("timeout"), 120.0)

        body = req.data.decode("utf-8", errors="replace")
        self.assertIn('name="wake_eval_ms"\r\n\r\n45.2', body)
        self.assertIn('name="speech_duration_ms"\r\n\r\n2150.3', body)
        self.assertIn('name="silence_duration_ms"\r\n\r\n850.1', body)
        self.assertIn('name="node_id"\r\n\r\ntouch-kiosk-kitchen', body)
        self.assertIn('name="audio"; filename="utterance.wav"', body)

    @patch("clients.ear.client.urllib.request.urlopen")
    def test_heartbeat_payload_and_exception_handling(self, mock_urlopen):
        mock_resp = MagicMock()
        mock_resp.status = 200
        mock_urlopen.return_value.__enter__.return_value = mock_resp

        # 1. Valid heartbeat post
        res = post_voice_heartbeat(
            hub_url="http://test-hub:9000/api/voice/interact",
            node_id="touch-kiosk-kitchen",
            db=-40.56,
            false_wakes=3,
            last_playback_sec=1.234,
            timeout=2.0,
        )
        self.assertTrue(res)
        mock_urlopen.assert_called_once()
        req, kwargs = mock_urlopen.call_args[0][0], mock_urlopen.call_args[1]
        self.assertEqual(req.full_url, "http://test-hub:9000/api/voice/heartbeat")
        self.assertEqual(kwargs.get("timeout"), 2.0)
        payload = json.loads(req.data.decode("utf-8"))
        self.assertEqual(payload["node_id"], "touch-kiosk-kitchen")
        self.assertEqual(payload["ambient_rms_dbfs"], -40.6)
        self.assertEqual(payload["false_wakes"], 3)
        self.assertEqual(payload["last_playback_sec"], 1.23)

        # 2. Empty hub_url returns False without throwing
        self.assertFalse(post_voice_heartbeat("", "touch-kiosk-kitchen", -50.0))

        # 3. Exception handling in post_voice_heartbeat (URLError and Exception)
        import urllib.error
        mock_urlopen.side_effect = urllib.error.URLError("Network unreachable")
        self.assertFalse(
            post_voice_heartbeat("http://test-hub:9000", "touch-kiosk-kitchen", -50.0)
        )

        mock_urlopen.side_effect = RuntimeError("Fatal socket error")
        self.assertFalse(
            post_voice_heartbeat("http://test-hub:9000", "touch-kiosk-kitchen", -50.0)
        )

        # 4. VoiceDaemon._send_heartbeat catches exceptions cleanly
        daemon = VoiceDaemon(self.cfg, model=MockModel())
        mock_urlopen.side_effect = urllib.error.URLError("Connection refused")
        # Should not raise
        daemon._send_heartbeat("http://test-hub:9000", payload)

        # 5. Play audio measures playback duration
        proc = MagicMock()
        proc.communicate.side_effect = lambda input, timeout: (time.sleep(0.002), (b"", b""))[1]
        proc.returncode = 0
        with patch("clients.ear.client.shutil.which", return_value="/usr/bin/pw-play"), \
                patch("clients.ear.client.subprocess.Popen", return_value=proc):
            daemon.play_audio(b"sample-audio")
            self.assertGreater(daemon.last_playback_sec, 0.0)

    @patch("clients.ear.client.threading.Thread")
    def test_run_heartbeat_dispatch(self, mock_thread_cls):
        mock_model = MockModel(score_map={"hey_jarvis": 0.0})
        daemon = VoiceDaemon(self.cfg, model=mock_model)
        daemon.false_wakes_count = 2
        daemon.last_playback_sec = 3.5

        # Feed 1 frame, but simulate time advancing past 10s
        chunk_bytes = self.cfg.chunk_samples * 2
        silent_frame = struct.pack("<1280h", *([0] * 1280))
        audio_stream = io.BytesIO(silent_frame)

        time_calls = [100.0, 100.0, 115.0, 115.0]
        with patch("clients.ear.client.time.time", side_effect=lambda: time_calls.pop(0) if time_calls else 115.0), \
             patch("clients.ear.client.post_voice_state"):
            daemon.run(audio_source=audio_stream)

        mock_thread_cls.assert_called_once()
        _, kwargs = mock_thread_cls.call_args
        self.assertEqual(kwargs.get("target"), daemon._send_heartbeat)
        args = kwargs.get("args")
        self.assertEqual(args[0], self.cfg.hub_url)
        payload = args[1]
        self.assertEqual(payload["node_id"], self.cfg.node_id)
        self.assertEqual(payload["false_wakes"], 2)
        self.assertEqual(payload["last_playback_sec"], 3.5)
        self.assertEqual(daemon.false_wakes_count, 0)
        self.assertEqual(daemon.last_playback_sec, 0.0)

    def test_stop_signal(self):
        mock_model = MockModel()
        daemon = VoiceDaemon(self.cfg, model=mock_model)
        self.assertTrue(daemon.running)
        daemon.stop()
        self.assertFalse(daemon.running)


if __name__ == "__main__":
    unittest.main()

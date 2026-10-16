import os
import tempfile
import threading
import unittest
from unittest.mock import MagicMock, patch

from client.ear.config import VoiceConfig, load_config
from client.ear.client import VoiceDaemon

FRAME = b"\x00" * 2560  # 1280 samples of 16-bit PCM = 80 ms @ 16 kHz


class MockModel:
    """Minimal openWakeWord stand-in; records resets."""

    def __init__(self):
        self.models = {"hey_jarvis": None}
        self.reset_called = False
        self.preprocessor = MagicMock()

    def predict(self, chunk):
        return {}

    def reset(self):
        self.reset_called = True


class FakeDetector:
    """Scripted speech detector: returns the next flag per frame, False when exhausted."""

    def __init__(self, flags=()):
        self.flags = list(flags)
        self.resets = 0
        self.frames = 0

    def reset(self):
        self.resets += 1

    def is_speech(self, raw_bytes):
        self.frames += 1
        return self.flags.pop(0) if self.flags else False


class BargeInTestCase(unittest.TestCase):
    def setUp(self):
        self.temp_dir = tempfile.mkdtemp()
        self.save_path = os.path.join(self.temp_dir, "utterance.wav")
        self.cfg = VoiceConfig(
            save_path=self.save_path,
            silence_ms=100,
            mirrormere_url="http://test-core/kiosk",
            hub_url="http://test-hub/api/voice/interact",
            wake_models=["hey_jarvis"],
            barge_in=True,
            barge_in_min_speech_ms=300,  # 4 frames of 80 ms
        )
        # State changes post to the core; keep them off the network.
        patcher = patch("client.ear.client.post_voice_state")
        self.mock_post_state = patcher.start()
        self.addCleanup(patcher.stop)

    def tearDown(self):
        if os.path.exists(self.save_path):
            os.unlink(self.save_path)
        os.rmdir(self.temp_dir)

    def make_daemon(self, flags=(), cfg=None, model=None):
        detector = FakeDetector(flags)
        daemon = VoiceDaemon(cfg or self.cfg, model=model or MockModel(), barge_detector=detector)
        return daemon, detector

    @staticmethod
    def start_playback(daemon):
        """Puts the daemon in the mid-reply state barge-in watches for."""
        daemon.busy = True
        daemon.playing = True

    def feed(self, daemon, n):
        return [daemon.process_frame(FRAME) for _ in range(n)]


class TestBargeInDisabled(BargeInTestCase):
    def test_default_config_is_off(self):
        cfg = VoiceConfig()
        self.assertFalse(cfg.barge_in)
        self.assertEqual(cfg.barge_in_min_speech_ms, 300)
        self.assertEqual(cfg.barge_in_vad_threshold, 0.7)

    def test_off_builds_no_detector(self):
        self.cfg.barge_in = False
        with patch.object(VoiceDaemon, "_build_barge_detector") as mock_build:
            daemon = VoiceDaemon(self.cfg, model=MockModel())
        mock_build.assert_not_called()
        self.assertIsNone(daemon.barge_detector)

    def test_off_ignores_injected_detector(self):
        self.cfg.barge_in = False
        detector = FakeDetector([True] * 20)
        daemon = VoiceDaemon(self.cfg, model=MockModel(), barge_detector=detector)
        self.assertIsNone(daemon.barge_detector)

    def test_off_never_fires(self):
        self.cfg.barge_in = False
        daemon = VoiceDaemon(self.cfg, model=MockModel())
        self.start_playback(daemon)
        with patch.object(daemon, "_do_barge_in") as mock_do:
            results = self.feed(daemon, 30)
        mock_do.assert_not_called()
        self.assertNotIn("barge_in", results)
        self.assertEqual(daemon.state, "idle")
        self.assertTrue(daemon.busy)
        self.assertTrue(daemon.playing)

    def test_check_barge_in_inert_when_not_playing(self):
        daemon, detector = self.make_daemon([True] * 20)
        daemon.busy = True  # waiting on the hub, nothing audible yet
        self.assertEqual(self.feed(daemon, 10), [None] * 10)
        self.assertEqual(detector.frames, 0)
        self.assertEqual(daemon.state, "idle")


class TestBuildBargeDetector(BargeInTestCase):
    @patch("client.ear.client.SileroDetector")
    @patch("client.ear.client.load_silero_vad_model")
    def test_builds_dedicated_silero_with_barge_threshold(self, mock_load, mock_det):
        self.cfg.barge_in_vad_threshold = 0.65
        daemon = VoiceDaemon(self.cfg, model=MockModel())
        mock_det.assert_called_once_with(mock_load.return_value, 0.65)
        self.assertIs(daemon.barge_detector, mock_det.return_value)

    @patch("client.ear.client.load_silero_vad_model", side_effect=RuntimeError("no model"))
    def test_silero_unavailable_disables_feature(self, _mock_load):
        daemon = VoiceDaemon(self.cfg, model=MockModel())
        self.assertIsNone(daemon.barge_detector)
        self.start_playback(daemon)
        self.assertEqual(self.feed(daemon, 10), [None] * 10)


class TestCheckBargeIn(BargeInTestCase):
    def test_fires_only_after_min_speech(self):
        daemon, detector = self.make_daemon([True] * 10)
        self.start_playback(daemon)
        # 3 frames = 240 ms < 300 ms: nothing yet.
        self.assertEqual(self.feed(daemon, 3), [None] * 3)
        self.assertEqual(daemon.state, "idle")
        self.assertEqual(daemon.barge_in_count, 0)
        # 4th frame = 320 ms: fires.
        self.assertEqual(daemon.process_frame(FRAME), "barge_in")
        self.assertEqual(daemon.barge_in_count, 1)
        self.assertEqual(daemon.state, "listening")
        self.assertEqual(detector.resets, 1, "detector gets fresh state when playback watching starts")

    def test_single_non_speech_frame_inside_word_is_tolerated(self):
        # S S gap S S: the lone gap neither resets nor counts.
        daemon, _ = self.make_daemon([True, True, False, True, True])
        self.start_playback(daemon)
        self.assertEqual(self.feed(daemon, 4), [None] * 4)
        self.assertEqual(daemon.process_frame(FRAME), "barge_in")

    def test_two_consecutive_non_speech_frames_reset_the_count(self):
        # S S S gap gap resets; the next 3 speech frames are not enough, the 4th is.
        daemon, _ = self.make_daemon([True] * 3 + [False] * 2 + [True] * 4)
        self.start_playback(daemon)
        self.assertEqual(self.feed(daemon, 8), [None] * 8)
        self.assertEqual(daemon.barge_in_count, 0)
        self.assertEqual(daemon.process_frame(FRAME), "barge_in")

    def test_watching_resets_when_playback_stops(self):
        daemon, _ = self.make_daemon([True] * 3 + [True] * 3)
        self.start_playback(daemon)
        self.feed(daemon, 3)
        daemon.playing = False
        self.assertEqual(daemon.process_frame(FRAME), None)  # not playing: counters dropped
        self.start_playback(daemon)
        # Only 3 more speech frames since the reset: still below the threshold.
        self.assertEqual(self.feed(daemon, 3), [None] * 3)
        self.assertEqual(daemon.barge_in_count, 0)


class TestDoBargeIn(BargeInTestCase):
    def test_stops_playback_and_hands_daemon_to_listening(self):
        model = MockModel()
        daemon, _ = self.make_daemon(model=model)
        self.start_playback(daemon)
        abort = threading.Event()
        daemon._turn_abort = abort
        proc_a, proc_b = MagicMock(), MagicMock()
        proc_b.kill.side_effect = OSError("already dead")  # best-effort kill
        resp = MagicMock()
        daemon._player_procs.extend([proc_a, proc_b])
        daemon._active_resp = resp
        daemon.pre_roll.append(b"interrupting-speech")

        daemon._do_barge_in(123.0, 320.0)

        proc_a.kill.assert_called_once()
        proc_b.kill.assert_called_once()
        self.assertEqual(daemon._player_procs, [])
        self.assertTrue(abort.is_set())
        resp.close.assert_called_once()
        self.assertIsNone(daemon._active_resp)
        self.assertFalse(daemon.busy)
        self.assertFalse(daemon.playing)
        self.assertEqual(daemon.reply_ended_at, 123.0)
        self.assertTrue(model.reset_called, "wake engine is reset via reset_wake_engine")
        # Left in the listening state, seeded with the buffered speech.
        self.assertEqual(daemon.state, "listening")
        self.assertTrue(daemon.has_spoken)
        self.assertTrue(daemon._barge_pending)
        self.assertEqual(daemon.utterance_buffer, [b"interrupting-speech"])
        self.mock_post_state.assert_called_with("http://test-core/kiosk", "listening")
        self.assertEqual(daemon.barge_in_count, 1)

    def test_without_active_turn_still_recovers(self):
        daemon, _ = self.make_daemon()
        self.start_playback(daemon)
        daemon._do_barge_in(1.0, 300.0)
        self.assertEqual(daemon.state, "listening")
        self.assertFalse(daemon.busy)
        self.assertFalse(daemon.playing)

    def test_abort_response_shuts_down_socket_then_closes(self):
        daemon, _ = self.make_daemon()
        resp = MagicMock()
        daemon._abort_response(resp)
        resp.fp.raw._sock.shutdown.assert_called_once()
        resp.close.assert_called_once()

    def test_abort_response_swallows_errors(self):
        daemon, _ = self.make_daemon()
        resp = MagicMock()
        resp.fp.raw._sock.shutdown.side_effect = OSError("closed")
        resp.close.side_effect = OSError("closed")
        daemon._abort_response(resp)  # must not raise


class TestAbortedTurnRace(BargeInTestCase):
    """The hub worker's finally block must not stomp on the turn that replaced it."""

    def run_worker(self, daemon, sse_after_barge):
        with open(self.save_path, "wb") as f:
            f.write(b"RIFFdummyWAVE")

        def stream():
            yield b"event: reply\r\n"
            yield b'data: {"reply": "The garage door is closed"}\r\n'
            yield b"\r\n"
            # The user talks over the reply while the worker is mid-stream.
            self.start_playback(daemon)
            daemon._do_barge_in(50.0, 320.0)
            # The new turn's dispatch claims busy and pre_roll refills before the
            # old worker gets to unwind.
            daemon.busy = True
            daemon.pre_roll.append(b"new-turn-frame")
            daemon.model.reset_called = False
            for line in sse_after_barge:
                yield line

        mock_resp = MagicMock()
        mock_resp.status = 200
        mock_resp.__iter__.side_effect = lambda: stream()
        with patch("client.ear.client.urllib.request.urlopen") as mock_urlopen:
            mock_urlopen.return_value.__enter__.return_value = mock_resp
            daemon._hub_stream_worker(self.save_path)
        return mock_urlopen

    def test_finally_leaves_busy_pre_roll_and_wake_engine_alone(self):
        daemon, _ = self.make_daemon(model=MockModel())
        mock_urlopen = self.run_worker(
            daemon,
            [
                b"event: audio_chunk\r\n",
                b'data: {"chunk_index": 0, "data": "ZmFrZS1hdWRpby1ieXRlcw=="}\r\n',
                b"\r\n",
                b"event: done\r\n",
                b"data: {}\r\n",
                b"\r\n",
            ],
        )
        mock_urlopen.assert_called_once()  # no stray post-turn state call through urlopen
        self.assertTrue(daemon.busy, "aborted worker must not clear the new turn's busy")
        self.assertEqual(list(daemon.pre_roll), [b"new-turn-frame"], "pre_roll must survive")
        self.assertFalse(daemon.model.reset_called, "wake engine must not be reset again")
        self.assertEqual(daemon.state, "listening")
        for c in self.mock_post_state.call_args_list:
            self.assertNotEqual(c.args[1], "idle", "aborted worker must not post idle over the new turn")
        self.assertIsNone(daemon._active_resp)

    def test_no_audio_played_after_abort(self):
        daemon, _ = self.make_daemon(model=MockModel())
        with patch.object(daemon, "play_audio") as mock_play:
            self.run_worker(
                daemon,
                [
                    b"event: audio_chunk\r\n",
                    b'data: {"chunk_index": 1, "data": "ZmFrZS1hdWRpby1ieXRlcw=="}\r\n',
                    b"\r\n",
                ],
            )
        mock_play.assert_not_called()

    def test_unaborted_worker_still_cleans_up(self):
        """Control: without a barge-in the same finally block clears busy and pre_roll."""
        daemon, _ = self.make_daemon(model=MockModel())
        with open(self.save_path, "wb") as f:
            f.write(b"RIFFdummyWAVE")
        daemon.pre_roll.append(b"stale")
        mock_resp = MagicMock()
        mock_resp.status = 200
        mock_resp.__iter__.return_value = iter([b"event: done\r\ndata: {}\r\n\r\n"])
        with patch("client.ear.client.urllib.request.urlopen") as mock_urlopen:
            mock_urlopen.return_value.__enter__.return_value = mock_resp
            daemon._hub_stream_worker(self.save_path)
        self.assertFalse(daemon.busy)
        self.assertEqual(len(daemon.pre_roll), 0)
        self.assertTrue(daemon.model.reset_called)


class TestBargeInFormField(BargeInTestCase):
    def post_turn(self, daemon):
        with open(self.save_path, "wb") as f:
            f.write(b"RIFFdummyWAVE")
        mock_resp = MagicMock()
        mock_resp.status = 200
        mock_resp.__iter__.return_value = iter([b"event: done\r\ndata: {}\r\n\r\n"])
        with patch("client.ear.client.urllib.request.urlopen") as mock_urlopen:
            mock_urlopen.return_value.__enter__.return_value = mock_resp
            daemon._hub_stream_worker(self.save_path)
        return mock_urlopen.call_args_list[0][0][0].data

    def finish_utterance(self, daemon):
        """Drives the listening state to utterance completion with trailing silence."""
        daemon.silence_start = 1.0  # epoch-ish: long past silence_ms
        with patch.object(daemon, "dispatch_hub_interaction") as mock_dispatch:
            result = daemon.process_frame(FRAME)
        self.assertEqual(result, "utterance_saved")
        mock_dispatch.assert_called_once()

    def test_next_utterance_carries_barge_in_exactly_once(self):
        daemon, _ = self.make_daemon([True] * 4)
        self.start_playback(daemon)
        self.assertEqual(self.feed(daemon, 4)[-1], "barge_in")

        self.finish_utterance(daemon)
        self.assertTrue(daemon._turn_barge_in)
        self.assertFalse(daemon._barge_pending)

        first = self.post_turn(daemon)
        self.assertIn(b'name="barge_in"\r\n\r\ntrue', first)
        self.assertEqual(first.count(b'name="barge_in"'), 1)
        self.assertFalse(daemon._turn_barge_in)

        second = self.post_turn(daemon)
        self.assertNotIn(b'name="barge_in"', second)

    def test_ordinary_turn_has_no_barge_in_field(self):
        daemon, _ = self.make_daemon()
        self.assertNotIn(b'name="barge_in"', self.post_turn(daemon))


class TestBargeInConfig(unittest.TestCase):
    def load(self, yaml_text):
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "voice.yaml")
            with open(path, "w", encoding="utf-8") as f:
                f.write(yaml_text)
            return load_config(path)

    def test_defaults(self):
        cfg = self.load("ear:\n  node_id: x\n")
        self.assertFalse(cfg.barge_in)
        self.assertEqual(cfg.barge_in_min_speech_ms, 300)
        self.assertEqual(cfg.barge_in_vad_threshold, 0.7)

    def test_values_load(self):
        cfg = self.load(
            "ear:\n  barge_in: true\n  barge_in_min_speech_ms: 450\n  barge_in_vad_threshold: 0.8\n"
        )
        self.assertTrue(cfg.barge_in)
        self.assertEqual(cfg.barge_in_min_speech_ms, 450)
        self.assertEqual(cfg.barge_in_vad_threshold, 0.8)

    def test_non_bool_barge_in_is_ignored(self):
        cfg = self.load('ear:\n  barge_in: "yes please"\n')
        self.assertFalse(cfg.barge_in)

    def test_out_of_range_values_fall_back_to_defaults(self):
        cfg = self.load("ear:\n  barge_in_vad_threshold: 1.5\n  barge_in_min_speech_ms: -10\n")
        self.assertEqual(cfg.barge_in_vad_threshold, 0.7)
        self.assertEqual(cfg.barge_in_min_speech_ms, 300)

    def test_int_threshold_is_coerced_to_float(self):
        cfg = self.load("ear:\n  barge_in_vad_threshold: 1\n")
        self.assertEqual(cfg.barge_in_vad_threshold, 1.0)
        self.assertIsInstance(cfg.barge_in_vad_threshold, float)


if __name__ == "__main__":
    unittest.main()

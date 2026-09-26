"""
test_sse.py - Hermetic unit tests for SSE stream listener and event filtering
Reference: SPEC-009 §1-§3, SPEC-006 §1-§3, Issue #246
"""

import io
import threading
import unittest
from typing import Any

from sse import SSEListener, DIRTY_EVENTS, IGNORED_EVENTS, parse_sse_block


class MockResponse:
    """
    Mock response for urlopen streaming lines of SSE data.
    """

    def __init__(self, lines: list[str | bytes], headers=None):
        self.lines = list(lines)
        self.headers = headers or {}
        self.closed = False

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc_val, exc_tb):
        self.closed = True

    def __iter__(self):
        for line in self.lines:
            yield line


class TestSSEListener(unittest.TestCase):
    def test_parse_sse_block(self):
        """
        Parses standard SSE blocks and strips comments.
        """
        lines = [
            ": ping 1727216200",
            "event: widget.update",
            "id: evt_01",
            "data: {\"widget_id\":\"weather\"}",
        ]
        has_content, event_type, event_id, data = parse_sse_block(lines)
        self.assertTrue(has_content)
        self.assertEqual(event_type, "widget.update")
        self.assertEqual(event_id, "evt_01")
        self.assertEqual(data, '{"widget_id":"weather"}')

    def test_parse_sse_comment_only_block(self):
        """
        Comment-only blocks return has_content=False.
        """
        lines = [
            ": ping 1727216200",
            ": keepalive",
        ]
        has_content, event_type, event_id, data = parse_sse_block(lines)
        self.assertFalse(has_content)

    def test_parse_sse_multiline_data(self):
        """
        Multiple data: lines should be joined by newline.
        """
        lines = [
            "event: custom.event",
            "data: line 1",
            "data: line 2",
        ]
        has_content, event_type, event_id, data = parse_sse_block(lines)
        self.assertTrue(has_content)
        self.assertEqual(event_type, "custom.event")
        self.assertIsNone(event_id)
        self.assertEqual(data, "line 1\nline 2")

    def test_dirty_events_trigger_on_dirty(self):
        """
        Verifies that each specified dirty trigger event invokes the on_dirty callback.
        """
        expected_events = [
            "widget.update",
            "header.update",
            "screen.rotate",
            "widget.reload",
            "style.reload",
        ]
        self.assertEqual(DIRTY_EVENTS, set(expected_events))

        dirty_calls: list[tuple[str, str | None, str]] = []

        stream_lines = []
        for idx, ev in enumerate(expected_events):
            stream_lines.extend([
                f"event: {ev}\n",
                f"id: evt_{idx}\n",
                f"data: {{\"event\":\"{ev}\"}}\n",
                "\n",
            ])

        mock_resp = MockResponse(stream_lines)

        listener = SSEListener(
            events_url="http://localhost:8080/api/events",
            on_dirty=lambda t, i, d: dirty_calls.append((t, i, d)),
            opener=lambda req, timeout=None: mock_resp,
        )

        stop_event = threading.Event()
        # Run one iteration and stop
        t = threading.Thread(target=listener.run, args=(stop_event,))
        t.start()
        # Allow thread to process lines
        stop_event.set()
        t.join(timeout=1.0)

        self.assertEqual(len(dirty_calls), len(expected_events))
        received_types = [call[0] for call in dirty_calls]
        self.assertEqual(received_types, expected_events)
        self.assertEqual(listener.last_event_id, f"evt_{len(expected_events)-1}")

    def test_ignored_events_do_not_trigger_on_dirty(self):
        """
        Verifies that non-dirty events (system.status, voice.state, audio.state, : ping)
        do not mark the screen dirty.
        """
        dirty_calls: list[tuple[str, str | None, str]] = []
        all_events: list[str] = []

        stream_lines = [
            ": ping 1727216200\n",
            "\n",
            "event: system.status\n",
            "id: evt_status\n",
            "data: {\"online\":true}\n",
            "\n",
            "event: voice.state\n",
            "id: evt_voice\n",
            "data: {\"state\":\"listening\"}\n",
            "\n",
            "event: audio.state\n",
            "id: evt_audio\n",
            "data: {\"volume\":80}\n",
            "\n",
        ]

        mock_resp = MockResponse(stream_lines)

        listener = SSEListener(
            events_url="http://localhost:8080/api/events",
            on_dirty=lambda t, i, d: dirty_calls.append((t, i, d)),
            on_event=lambda t, i, d: all_events.append(t),
            opener=lambda req, timeout=None: mock_resp,
        )

        stop_event = threading.Event()
        t = threading.Thread(target=listener.run, args=(stop_event,))
        t.start()
        stop_event.set()
        t.join(timeout=1.0)

        # None of the ignored events should trigger on_dirty
        self.assertEqual(len(dirty_calls), 0, "Ignored events must not invoke on_dirty")
        # But they are delivered to general on_event
        self.assertEqual(all_events, ["system.status", "voice.state", "audio.state"])
        self.assertEqual(listener.last_event_id, "evt_audio")

    def test_last_event_id_header_on_reconnect(self):
        """
        Verifies that Last-Event-ID is included in HTTP headers on subsequent connections.
        """
        captured_requests = []

        def mock_opener(req, timeout=None):
            captured_requests.append(req)
            if len(captured_requests) == 1:
                return MockResponse([
                    "event: widget.update\n",
                    "id: evt_42\n",
                    "data: {}\n",
                    "\n",
                ])
            return MockResponse([])

        backoffs = []
        stop_event = threading.Event()

        listener = SSEListener(
            events_url="http://localhost:8080/api/events",
            opener=mock_opener,
            sleep_fn=lambda sec: (backoffs.append(sec), stop_event.set() if len(captured_requests) >= 2 else None),
        )

        t = threading.Thread(target=listener.run, args=(stop_event,))
        t.start()
        t.join(timeout=1.0)

        self.assertGreaterEqual(len(captured_requests), 2)
        # First request: no Last-Event-ID
        self.assertNotIn("Last-event-id", captured_requests[0].headers)
        # Second request: Last-Event-ID is evt_42
        self.assertEqual(captured_requests[1].headers.get("Last-event-id"), "evt_42")

    def test_exponential_backoff_progression(self):
        """
        Verifies that connection errors trigger exponential backoff doubling from 1.0 up to 60.0 max,
        and resets to 1.0 on a successful connection.
        """
        backoff_delays = []
        call_count = [0]
        stop_event = threading.Event()

        def failing_opener(req, timeout=None):
            call_count[0] += 1
            if call_count[0] <= 7:
                raise ConnectionError("connection refused")
            # 8th call succeeds
            return MockResponse([
                "event: header.update\n",
                "id: evt_ok\n",
                "data: {}\n",
                "\n",
            ])

        listener = SSEListener(
            events_url="http://localhost:8080/api/events",
            initial_backoff=1.0,
            max_backoff=60.0,
            backoff_factor=2.0,
            opener=failing_opener,
            sleep_fn=lambda sec: (
                backoff_delays.append(sec),
                stop_event.set() if len(backoff_delays) >= 8 else None,
            ),
        )

        t = threading.Thread(target=listener.run, args=(stop_event,))
        t.start()
        t.join(timeout=1.0)

        # 1.0 -> 2.0 -> 4.0 -> 8.0 -> 16.0 -> 32.0 -> 60.0 -> 60.0
        expected = [1.0, 2.0, 4.0, 8.0, 16.0, 32.0, 60.0]
        self.assertEqual(backoff_delays[:7], expected)


if __name__ == "__main__":
    unittest.main()

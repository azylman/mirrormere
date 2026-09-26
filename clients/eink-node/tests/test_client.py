"""
test_client.py - Integrated unit tests for EinkClient orchestration
Reference: SPEC-009 §1-§3, Issue #246
"""

import time
import unittest

from client import EinkClient
from config import EinkConfig
from fetcher import FetchResult


class MockSSEStream:
    def __init__(self):
        self.closed = False

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc_val, exc_tb):
        self.closed = True

    def __iter__(self):
        return self

    def __next__(self):
        # Hang or raise StopIteration when closed
        if self.closed:
            raise StopIteration
        time.sleep(0.1)
        return ": ping\n\n"


class TestEinkClient(unittest.TestCase):
    def test_client_lifecycle_and_dispatch(self):
        """
        Validates starting the client, receiving an SSE trigger, coalescing,
        updating the panel writer, and stopping all background threads cleanly.
        """
        cfg = EinkConfig()
        cfg.refresh.coalesce_seconds = 0.05
        cfg.refresh.min_refresh_seconds = 0.05
        cfg.refresh.clock_refresh_seconds = 60.0

        panel_writes = []

        def mock_panel_writer(data: bytes, is_full: bool):
            panel_writes.append((data, is_full))

        client = EinkClient(config=cfg, panel_writer=mock_panel_writer)

        # Mock SSE listener opener
        mock_stream = MockSSEStream()
        client.sse_listener.opener = lambda req, timeout=None: mock_stream

        # Mock fetcher to return fresh image
        client.fetcher.fetch = lambda last_etag=None: FetchResult(
            status_code=200,
            etag='"client-test-etag"',
            data=b"test-eink-bytes",
            not_modified=False,
        )

        client.start()

        # Trigger SSE dirty event
        client._on_sse_dirty("widget.update", "evt_100", '{"data": 1}')
        self.assertTrue(client.engine.is_dirty)

        # Wait briefly for worker thread to coalesce and execute
        start_time = time.time()
        while time.time() - start_time < 1.0:
            if len(panel_writes) >= 1:
                break
            time.sleep(0.02)

        self.assertGreaterEqual(len(panel_writes), 1)
        self.assertEqual(panel_writes[0][0], b"test-eink-bytes")
        self.assertFalse(client.engine.is_dirty)

        # Stop client cleanly
        mock_stream.closed = True
        client.stop()
        for t in client._threads:
            self.assertFalse(t.is_alive(), f"Thread {t.name} must terminate on stop()")

    def test_client_clock_timer_tick(self):
        """
        Validates that the periodic clock refresh timer marks the engine dirty.
        """
        cfg = EinkConfig()
        cfg.refresh.coalesce_seconds = 0.05
        cfg.refresh.min_refresh_seconds = 0.05
        cfg.refresh.clock_refresh_seconds = 0.05

        client = EinkClient(config=cfg)
        mock_stream = MockSSEStream()
        client.sse_listener.opener = lambda req, timeout=None: mock_stream
        client.fetcher.fetch = lambda last_etag=None: FetchResult(
            status_code=304,
            etag=last_etag,
            data=None,
            not_modified=True,
        )

        client.start()

        start_time = time.time()
        while time.time() - start_time < 1.0:
            if client.engine.is_dirty or "clock_timer" in client.engine.dirty_reasons:
                break
            time.sleep(0.02)

        self.assertIn("clock_timer", client.engine.dirty_reasons)

        mock_stream.closed = True
        client.stop()


if __name__ == "__main__":
    unittest.main()

"""
test_coalesce.py - Hermetic unit tests for refresh coalescing engine and ETag caching
Reference: SPEC-009 §1-§3, Issue #246
"""

import unittest
from typing import Any

from coalesce import CoalescingEngine, RefreshResult
from fetcher import FetchResult


class MockFetcher:
    """
    Test double for ETagFetcher allowing programmatic control over HTTP responses.
    """

    def __init__(self, responses=None):
        # responses: list of FetchResult or single FetchResult or callable
        self.responses = list(responses) if responses is not None else []
        self.call_history: list[dict[str, Any]] = []

    def set_response(self, resp: FetchResult):
        self.responses = [resp]

    def add_response(self, resp: FetchResult):
        self.responses.append(resp)

    def fetch(self, last_etag: str | None = None) -> FetchResult:
        self.call_history.append({"last_etag": last_etag})
        if self.responses:
            if len(self.responses) > 1:
                return self.responses.pop(0)
            return self.responses[0]
        # Default response
        return FetchResult(status_code=304, etag=last_etag, data=None, not_modified=True)


class MockClock:
    """
    Simulated clock advancing instantly without real sleeps.
    """

    def __init__(self, start_time: float = 1000.0):
        self.current_time = start_time

    def time(self) -> float:
        return self.current_time

    def advance(self, seconds: float):
        self.current_time += seconds


class TestCoalescingEngine(unittest.TestCase):
    def setUp(self):
        self.clock = MockClock(1000.0)
        self.fetcher = MockFetcher()
        self.panel_writes: list[dict[str, Any]] = []

    def panel_writer(self, data: bytes, is_full: bool):
        self.panel_writes.append({
            "data": data,
            "is_full": is_full,
            "timestamp": self.clock.time(),
        })

    def create_engine(
        self,
        coalesce_seconds: float = 5.0,
        min_refresh_seconds: float = 60.0,
        max_idle_seconds: float = 900.0,
    ) -> CoalescingEngine:
        return CoalescingEngine(
            coalesce_seconds=coalesce_seconds,
            min_refresh_seconds=min_refresh_seconds,
            max_idle_seconds=max_idle_seconds,
            fetcher=self.fetcher,
            panel_writer=self.panel_writer,
            time_fn=self.clock.time,
        )

    def test_event_burst_debouncing(self):
        """
        A rapid burst of dirty events (t=0, t=1, t=2, t=3) collapses into a single
        refresh cycle exactly coalesce_seconds (5s) after the LAST event.
        """
        engine = self.create_engine(coalesce_seconds=5.0, min_refresh_seconds=60.0)
        self.fetcher.set_response(
            FetchResult(status_code=200, etag='"etag-1"', data=b"frame-1", not_modified=False)
        )

        # Initial state: clean
        ready, _ = engine.should_refresh(self.clock.time())
        self.assertFalse(ready)

        # t=1000: Event 1 arrives
        engine.mark_dirty("widget.update")
        self.assertTrue(engine.is_dirty)

        # t=1001: Event 2 arrives
        self.clock.advance(1.0)
        engine.mark_dirty("header.update")

        # t=1002: Event 3 arrives
        self.clock.advance(1.0)
        engine.mark_dirty("widget.update")

        # t=1003: Event 4 arrives
        self.clock.advance(1.0)
        engine.mark_dirty("screen.rotate")

        # At t=1003, last dirty time is 1003. Coalesce wait is 5s, so ready time is 1008.
        # Check at t=1005 (2s after last event): should NOT be ready yet
        self.clock.advance(2.0)  # now 1005
        ready, _ = engine.should_refresh(self.clock.time())
        self.assertFalse(ready, "Must not refresh while debounce window is active")

        # Check at t=1007.9 (4.9s after last event): should NOT be ready
        self.clock.advance(2.9)  # now 1007.9
        ready, _ = engine.should_refresh(self.clock.time())
        self.assertFalse(ready, "Must not refresh before 5s debounce window completes")

        # Check at t=1008.0 (5.0s after last event): SHOULD be ready!
        self.clock.advance(0.1)  # now 1008.0
        ready, reason = engine.should_refresh(self.clock.time())
        self.assertTrue(ready)
        self.assertEqual(reason, "dirty")

        # Execute refresh
        res = engine.execute_refresh(self.clock.time())
        self.assertEqual(res.status, "refreshed")
        self.assertEqual(res.etag, '"etag-1"')
        self.assertEqual(len(self.panel_writes), 1)
        self.assertEqual(self.panel_writes[0]["data"], b"frame-1")
        self.assertFalse(engine.is_dirty, "Dirty flag must be cleared after refresh")
        self.assertEqual(engine.fetch_count, 1)

    def test_min_refresh_seconds_floor(self):
        """
        Enforces min_refresh_seconds (60s) panel write floor between consecutive refreshes
        to protect electronic paper hardware from thermal and voltage stress.
        """
        engine = self.create_engine(coalesce_seconds=5.0, min_refresh_seconds=60.0)

        # First write at t=1000
        self.fetcher.set_response(
            FetchResult(status_code=200, etag='"etag-1"', data=b"frame-1", not_modified=False)
        )
        engine.mark_dirty("init")
        self.clock.advance(5.0)  # t=1005
        res = engine.execute_refresh(self.clock.time())
        self.assertEqual(res.status, "refreshed")
        self.assertEqual(engine.last_write_time, 1005.0)

        # New event arrives at t=1015 (10s after last write)
        self.clock.advance(10.0)  # t=1015
        engine.mark_dirty("widget.update")

        # At t=1020, 5s debounce has elapsed, but only 15s have elapsed since last write (< 60s floor)
        self.clock.advance(5.0)  # t=1020
        ready, _ = engine.should_refresh(self.clock.time())
        self.assertFalse(ready, "Must not refresh before min_refresh_seconds floor")
        self.assertAlmostEqual(engine.time_until_next_check(self.clock.time()), 45.0)

        # At t=1064.9 (59.9s since write): still not ready
        self.clock.advance(44.9)  # t=1064.9
        ready, _ = engine.should_refresh(self.clock.time())
        self.assertFalse(ready)

        # At t=1065.0 (60.0s since write): ready to refresh!
        self.clock.advance(0.1)  # t=1065.0
        ready, reason = engine.should_refresh(self.clock.time())
        self.assertTrue(ready)
        self.assertEqual(reason, "dirty")

        # Execute second refresh with new frame
        self.fetcher.set_response(
            FetchResult(status_code=200, etag='"etag-2"', data=b"frame-2", not_modified=False)
        )
        res2 = engine.execute_refresh(self.clock.time())
        self.assertEqual(res2.status, "refreshed")
        self.assertEqual(len(self.panel_writes), 2)
        self.assertEqual(engine.last_write_time, 1065.0)

    def test_burst_arriving_near_min_refresh_floor(self):
        """
        If a new burst of events arrives right as min_refresh_seconds floor is expiring,
        the debounce window (5s) must take precedence and prevent premature write.
        """
        engine = self.create_engine(coalesce_seconds=5.0, min_refresh_seconds=60.0)

        # First write at t=1000
        self.fetcher.set_response(
            FetchResult(status_code=200, etag='"etag-1"', data=b"frame-1", not_modified=False)
        )
        engine.mark_dirty("init")
        self.clock.advance(5.0)  # t=1005
        engine.execute_refresh(self.clock.time())

        # Event arrives at t=1058 (53s after last write)
        self.clock.advance(53.0)  # t=1058
        engine.mark_dirty("widget.update")

        # At t=1065 (60s after last write), min floor has expired, BUT only 7s passed since 1058?
        # Wait: 1058 + 5 = 1063.
        # Let's test event arriving at t=1063 (58s after last write):
        # At t=1065 (60s after write), only 2s has passed since 1063 (< 5s debounce).
        # It must wait until 1063 + 5 = 1068!
        self.clock.advance(5.0)  # t=1063
        engine.mark_dirty("rapid.burst")

        # At t=1065 (floor satisfied, but debounce NOT satisfied: 2s < 5s)
        self.clock.advance(2.0)  # t=1065
        ready, _ = engine.should_refresh(self.clock.time())
        self.assertFalse(ready, "Debounce window must be enforced even if min write floor is satisfied")
        self.assertAlmostEqual(engine.time_until_next_check(self.clock.time()), 3.0)

        # At t=1068 (both floor and debounce satisfied)
        self.clock.advance(3.0)  # t=1068
        ready, reason = engine.should_refresh(self.clock.time())
        self.assertTrue(ready)
        self.assertEqual(reason, "dirty")

    def test_etag_304_skips_panel_write(self):
        """
        When the sidecar returns HTTP 304 Not Modified, the dirty flag is cleared
        and the physical panel write is skipped.
        """
        engine = self.create_engine(coalesce_seconds=5.0, min_refresh_seconds=60.0)

        # Set sidecar to return 304 Not Modified
        self.fetcher.set_response(
            FetchResult(status_code=304, etag='"cached-etag"', data=None, not_modified=True)
        )

        engine.mark_dirty("screen.rotate")
        self.clock.advance(5.0)

        ready, _ = engine.should_refresh(self.clock.time())
        self.assertTrue(ready)

        res = engine.execute_refresh(self.clock.time())
        self.assertEqual(res.status, "skipped_304")
        self.assertFalse(engine.is_dirty, "Dirty flag must be cleared on 304")
        self.assertEqual(len(self.panel_writes), 0, "No panel write should occur on 304")
        self.assertEqual(engine.skipped_304_count, 1)

    def test_identical_bytes_skips_panel_write(self):
        """
        If the server returns 200 OK but with identical PNG bytes as the cached frame,
        the panel write is skipped to prevent hardware flicker.
        """
        engine = self.create_engine(coalesce_seconds=5.0, min_refresh_seconds=60.0)

        # First fetch: 200 OK with frame-1
        self.fetcher.set_response(
            FetchResult(status_code=200, etag='"etag-1"', data=b"identical-png-bytes", not_modified=False)
        )
        engine.mark_dirty("init")
        self.clock.advance(5.0)
        res1 = engine.execute_refresh(self.clock.time())
        self.assertEqual(res1.status, "refreshed")
        self.assertEqual(len(self.panel_writes), 1)

        # Second fetch: 200 OK but identical bytes (e.g. server sent new or null etag)
        self.clock.advance(60.0)
        engine.mark_dirty("widget.update")
        self.clock.advance(5.0)

        self.fetcher.set_response(
            FetchResult(status_code=200, etag='"etag-2"', data=b"identical-png-bytes", not_modified=False)
        )
        res2 = engine.execute_refresh(self.clock.time())
        self.assertEqual(res2.status, "skipped_identical")
        self.assertEqual(len(self.panel_writes), 1, "Panel write must be skipped for identical bytes")
        self.assertFalse(engine.is_dirty)
        self.assertEqual(engine.skipped_identical_count, 1)

    def test_safety_fetch_max_idle(self):
        """
        A safety fetch runs every max_idle_seconds (900s) even with zero incoming events,
        preventing the panel from remaining stale if an SSE event was dropped.
        """
        engine = self.create_engine(coalesce_seconds=5.0, min_refresh_seconds=60.0, max_idle_seconds=900.0)

        # Start clean; advance 899s
        self.clock.advance(899.0)
        ready, _ = engine.should_refresh(self.clock.time())
        self.assertFalse(ready)

        # Advance to 900s: safety fetch triggers
        self.clock.advance(1.0)
        ready, reason = engine.should_refresh(self.clock.time())
        self.assertTrue(ready)
        self.assertEqual(reason, "max_idle")

        # Sidecar returns 304
        self.fetcher.set_response(
            FetchResult(status_code=304, etag=None, data=None, not_modified=True)
        )
        res = engine.execute_refresh(self.clock.time())
        self.assertEqual(res.status, "skipped_304")
        self.assertEqual(len(self.panel_writes), 0)

    def test_fetch_failure_preserves_dirty_state(self):
        """
        If the fetcher encounters an HTTP 503 or connection drop, the screen remains
        marked dirty so it retries on the next cycle, and panel writes are not attempted.
        """
        engine = self.create_engine(coalesce_seconds=5.0, min_refresh_seconds=60.0)
        self.fetcher.set_response(
            FetchResult(status_code=503, etag=None, data=None, not_modified=False, error="HTTP 503: Service Unavailable")
        )

        engine.mark_dirty("widget.update")
        self.clock.advance(5.0)

        res = engine.execute_refresh(self.clock.time())
        self.assertEqual(res.status, "error")
        self.assertEqual(engine.consecutive_fetch_failures, 1)
        self.assertTrue(engine.is_dirty, "Dirty flag must remain true on fetch failure")
        self.assertEqual(len(self.panel_writes), 0)

    def test_clock_refresh_ticks(self):
        """
        Simulates clock ticks marking the screen dirty every clock_refresh_seconds (300s).
        """
        engine = self.create_engine(coalesce_seconds=5.0, min_refresh_seconds=60.0)
        self.fetcher.set_response(
            FetchResult(status_code=200, etag='"clock-1"', data=b"clock-png-1", not_modified=False)
        )

        # Simulate clock tick
        engine.mark_dirty("clock_timer")
        self.assertTrue(engine.is_dirty)

        self.clock.advance(5.0)
        ready, reason = engine.should_refresh(self.clock.time())
        self.assertTrue(ready)
        self.assertEqual(reason, "dirty")

        res = engine.execute_refresh(self.clock.time())
        self.assertEqual(res.status, "refreshed")
        self.assertEqual(len(self.panel_writes), 1)


if __name__ == "__main__":
    unittest.main()

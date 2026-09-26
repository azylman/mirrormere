"""
coalesce.py - Refresh coalescing engine and rate limiting for Mirrormere E-Ink display node
Reference: SPEC-009 §1-§3
"""

from dataclasses import dataclass
import logging
import threading
import time
from typing import Callable, Any

try:
    from .fetcher import ETagFetcher, FetchResult
except ImportError:
    from fetcher import ETagFetcher, FetchResult

logger = logging.getLogger("mirrormere.eink.coalesce")


@dataclass
class RefreshResult:
    status: str  # "refreshed", "skipped_304", "skipped_identical", "error", "no_fetcher"
    etag: str | None = None
    bytes_written: int = 0
    error: str | None = None
    status_code: int = 0


class CoalescingEngine:
    """
    Manages e-paper panel refresh timing and event coalescing.
    Enforces:
      - coalesce_seconds (default 5s) debounce on incoming dirty triggers
      - min_refresh_seconds (default 60s) panel write floor to protect hardware
      - max_idle_seconds (default 900s) safety fetch to catch missed events
      - ETag 304 and identical byte skipping to prevent redundant panel cycles
    """

    def __init__(
        self,
        coalesce_seconds: float = 5.0,
        min_refresh_seconds: float = 60.0,
        max_idle_seconds: float = 900.0,
        fetcher: ETagFetcher | None = None,
        panel_writer: Callable[[bytes, bool], Any] | None = None,
        time_fn: Callable[[], float] = time.time,
    ):
        self.coalesce_seconds = coalesce_seconds
        self.min_refresh_seconds = min_refresh_seconds
        self.max_idle_seconds = max_idle_seconds
        self.fetcher = fetcher
        self.panel_writer = panel_writer
        self._time_fn = time_fn

        self._lock = threading.RLock()
        self._cond = threading.Condition(self._lock)

        self.is_dirty: bool = False
        self.first_dirty_time: float | None = None
        self.last_dirty_time: float | None = None
        self.dirty_reasons: list[str] = []

        # -1.0 means no panel write has occurred yet (allowing initial refresh on boot without waiting 60s)
        self.last_write_time: float = -1.0
        self.last_fetch_time: float = self._time_fn()
        self.last_etag: str | None = None
        self.last_image_bytes: bytes | None = None

        self.fetch_count: int = 0
        self.write_count: int = 0
        self.skipped_304_count: int = 0
        self.skipped_identical_count: int = 0
        self.consecutive_fetch_failures: int = 0

    def mark_dirty(self, reason: str = "event") -> None:
        """
        Marks the screen dirty. Thread-safe and wakes any sleeping worker.
        """
        with self._lock:
            now = self._time_fn()
            if not self.is_dirty:
                self.is_dirty = True
                self.first_dirty_time = now
                self.dirty_reasons = []
            self.last_dirty_time = now
            self.dirty_reasons.append(reason)
            logger.debug("Marked dirty (reason: %s, total reasons: %d)", reason, len(self.dirty_reasons))
            self._cond.notify_all()

    def should_refresh(self, now: float | None = None) -> tuple[bool, str]:
        """
        Determines if a refresh/fetch should execute at timestamp `now`.
        Returns (should_refresh, trigger_reason).
        """
        with self._lock:
            if now is None:
                now = self._time_fn()

            # 1. Safety fetch: max_idle_seconds elapsed since last fetch
            if self.max_idle_seconds > 0 and self.last_fetch_time >= 0:
                if (now - self.last_fetch_time) >= self.max_idle_seconds:
                    return True, "max_idle"

            # 2. Coalesced dirty trigger: screen is dirty, debounce expired, and write floor respected
            if self.is_dirty and self.last_dirty_time is not None:
                debounce_satisfied = (now - self.last_dirty_time) >= self.coalesce_seconds
                min_floor_satisfied = (
                    self.last_write_time < 0 or (now - self.last_write_time) >= self.min_refresh_seconds
                )
                if debounce_satisfied and min_floor_satisfied:
                    return True, "dirty"

            return False, ""

    def time_until_next_check(self, now: float | None = None) -> float:
        """
        Calculates the seconds to wait until the next refresh could be ready.
        Returns float('inf') if no timer or dirty condition is pending.
        """
        with self._lock:
            if now is None:
                now = self._time_fn()

            wait_candidates: list[float] = []

            # Time until dirty debounce + write floor satisfied
            if self.is_dirty and self.last_dirty_time is not None:
                wait_debounce = max(0.0, (self.last_dirty_time + self.coalesce_seconds) - now)
                wait_floor = 0.0
                if self.last_write_time >= 0:
                    wait_floor = max(0.0, (self.last_write_time + self.min_refresh_seconds) - now)
                wait_candidates.append(max(wait_debounce, wait_floor))

            # Time until safety fetch
            if self.max_idle_seconds > 0 and self.last_fetch_time >= 0:
                wait_safety = max(0.0, (self.last_fetch_time + self.max_idle_seconds) - now)
                wait_candidates.append(wait_safety)

            if not wait_candidates:
                return float("inf")

            return min(wait_candidates)

    def execute_refresh(self, now: float | None = None, is_full: bool = False) -> RefreshResult:
        """
        Performs an ETag-guarded fetch and, if content changed, triggers the panel writer.
        """
        with self._lock:
            if now is None:
                now = self._time_fn()

            if not self.fetcher:
                logger.warning("execute_refresh called without configured fetcher")
                return RefreshResult(status="no_fetcher", error="No fetcher configured")

            self.last_fetch_time = now
            self.fetch_count += 1

            logger.info("Executing fetch from sidecar (If-None-Match: %s)", self.last_etag)
            result: FetchResult = self.fetcher.fetch(last_etag=self.last_etag)

            if result.error or (result.status_code not in (200, 304)):
                self.consecutive_fetch_failures += 1
                logger.warning(
                    "Fetch failed (status %d, failure %d): %s",
                    result.status_code,
                    self.consecutive_fetch_failures,
                    result.error,
                )
                return RefreshResult(
                    status="error",
                    error=result.error or f"HTTP {result.status_code}",
                    status_code=result.status_code,
                )

            self.consecutive_fetch_failures = 0

            # Handle 304 Not Modified
            if result.not_modified or result.status_code == 304:
                logger.info("Image unchanged (304 Not Modified, ETag: %s); skipping panel write", self.last_etag)
                self.is_dirty = False
                self.first_dirty_time = None
                self.last_dirty_time = None
                self.dirty_reasons = []
                self.skipped_304_count += 1
                return RefreshResult(status="skipped_304", etag=self.last_etag)

            # Handle 200 OK with identical content bytes
            if self.last_image_bytes is not None and result.data == self.last_image_bytes:
                logger.info("Image payload identical to cached buffer; skipping panel write")
                self.is_dirty = False
                self.first_dirty_time = None
                self.last_dirty_time = None
                self.dirty_reasons = []
                if result.etag:
                    self.last_etag = result.etag
                self.skipped_identical_count += 1
                return RefreshResult(status="skipped_identical", etag=self.last_etag)

            # Handle 200 OK with fresh content
            payload_len = len(result.data) if result.data else 0
            logger.info("Received fresh 1-bit image (%d bytes, ETag: %s); writing to panel", payload_len, result.etag)
            self.is_dirty = False
            self.first_dirty_time = None
            self.last_dirty_time = None
            self.dirty_reasons = []
            self.last_etag = result.etag
            self.last_image_bytes = result.data
            self.last_write_time = now
            self.write_count += 1

            if self.panel_writer and result.data:
                try:
                    self.panel_writer(result.data, is_full)
                except Exception as pw_err:
                    logger.error("Panel writer error: %s", pw_err)

            return RefreshResult(
                status="refreshed",
                etag=self.last_etag,
                bytes_written=payload_len,
            )

    def worker_loop(self, stop_event: threading.Event) -> None:
        """
        Continuous worker loop for production background execution.
        """
        logger.info("Starting refresh coalescing worker loop")
        while not stop_event.is_set():
            with self._lock:
                ready, reason = self.should_refresh()
                if ready:
                    logger.debug("Worker ready to refresh (reason: %s)", reason)
                    self.execute_refresh()
                    continue

                wait_timeout = self.time_until_next_check()
                if wait_timeout == float("inf"):
                    # Sleep waiting for mark_dirty signal, checking stop_event every 1s
                    self._cond.wait(timeout=1.0)
                else:
                    sleep_time = min(wait_timeout, 1.0)
                    self._cond.wait(timeout=max(0.001, sleep_time))
        logger.info("Refresh coalescing worker loop stopped")

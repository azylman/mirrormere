"""
client.py - Standalone E-Ink Node Client daemon for Raspberry Pi Profile B
Reference: SPEC-009 §1-§3
"""

import argparse
import logging
import signal
import sys
import threading
import time

try:
    from .config import EinkConfig, load_config
    from .coalesce import CoalescingEngine, RefreshResult
    from .fetcher import ETagFetcher
    from .sse import SSEListener
except ImportError:
    from config import EinkConfig, load_config
    from coalesce import CoalescingEngine, RefreshResult
    from fetcher import ETagFetcher
    from sse import SSEListener

logger = logging.getLogger("mirrormere.eink.client")


class EinkClient:
    """
    Orchestrates the SSE stream listener, background clock refresh timer,
    ETag image fetcher, and refresh coalescing engine for the e-paper panel.
    """

    def __init__(
        self,
        config: EinkConfig | None = None,
        panel_writer=None,
        time_fn=None,
    ):
        self.config = config or load_config()
        self.panel_writer = panel_writer or self._default_panel_writer
        self.time_fn = time_fn or time.time

        self.stop_event = threading.Event()

        # Initialize ETag Fetcher
        self.fetcher = ETagFetcher(
            image_url=self.config.server.image_url,
            timeout=10.0,
        )

        # Initialize Coalescing Engine
        self.engine = CoalescingEngine(
            coalesce_seconds=self.config.refresh.coalesce_seconds,
            min_refresh_seconds=self.config.refresh.min_refresh_seconds,
            max_idle_seconds=self.config.refresh.max_idle_seconds,
            fetcher=self.fetcher,
            panel_writer=self.panel_writer,
            time_fn=self.time_fn,
        )

        # Initialize SSE Listener
        self.sse_listener = SSEListener(
            events_url=self.config.server.events_url,
            on_dirty=self._on_sse_dirty,
            on_connect=self._on_sse_connect,
            on_disconnect=self._on_sse_disconnect,
            time_fn=self.time_fn,
        )

        self._threads: list[threading.Thread] = []

    def _default_panel_writer(self, data: bytes, is_full: bool) -> None:
        """
        Default panel writer callback (used until hardware SPI panel is bound in Chunk 5.3).
        """
        logger.info(
            "Panel write received (%d bytes, full_refresh=%s)",
            len(data),
            is_full,
        )

    def _on_sse_dirty(self, event_type: str, event_id: str | None, data: str) -> None:
        """
        Callback when SSE listener receives a dirty trigger event.
        """
        logger.info("SSE event '%s' (id: %s) marked screen dirty", event_type, event_id)
        self.engine.mark_dirty(reason=f"sse:{event_type}")

    def _on_sse_connect(self) -> None:
        logger.info("Connected to Mirrormere SSE event bus at %s", self.config.server.events_url)

    def _on_sse_disconnect(self, err: Exception | None) -> None:
        if err:
            logger.warning("Disconnected from Mirrormere SSE event bus: %s", err)
        else:
            logger.info("Disconnected from Mirrormere SSE event bus")

    def _clock_timer_loop(self) -> None:
        """
        Periodic background clock timer: marks screen dirty every clock_refresh_seconds (default 300)
        to keep the header clock synchronized.
        """
        interval = self.config.refresh.clock_refresh_seconds
        logger.info("Started clock refresh timer (interval: %.1fs)", interval)

        while not self.stop_event.wait(timeout=interval):
            logger.debug("Clock timer ticked (interval: %.1fs)", interval)
            self.engine.mark_dirty(reason="clock_timer")

        logger.info("Clock refresh timer stopped")

    def start(self) -> None:
        """
        Starts all background worker threads (SSE listener, clock timer, refresh worker).
        """
        self.stop_event.clear()

        sse_thread = threading.Thread(
            target=self.sse_listener.run,
            args=(self.stop_event,),
            name="SSEListenerThread",
            daemon=True,
        )
        clock_thread = threading.Thread(
            target=self._clock_timer_loop,
            name="ClockTimerThread",
            daemon=True,
        )
        worker_thread = threading.Thread(
            target=self.engine.worker_loop,
            args=(self.stop_event,),
            name="RefreshWorkerThread",
            daemon=True,
        )

        self._threads = [sse_thread, clock_thread, worker_thread]
        for t in self._threads:
            t.start()
        logger.info("E-Ink Node Client daemon started successfully")

    def stop(self) -> None:
        """
        Signals all threads to stop and waits for completion.
        """
        logger.info("Stopping E-Ink Node Client daemon...")
        self.stop_event.set()
        # Wake condition variable in coalesce engine
        with self.engine._lock:
            self.engine._cond.notify_all()

        for t in self._threads:
            t.join(timeout=2.0)
        logger.info("E-Ink Node Client daemon stopped")

    def run(self) -> None:
        """
        Starts the client and runs until interrupted by SIGINT / SIGTERM.
        """
        def handle_signal(sig, frame):
            logger.info("Caught signal %d; shutting down", sig)
            self.stop()
            sys.exit(0)

        signal.signal(signal.SIGINT, handle_signal)
        signal.signal(signal.SIGTERM, handle_signal)

        self.start()

        try:
            while not self.stop_event.is_set():
                time.sleep(0.5)
        except KeyboardInterrupt:
            self.stop()


def main() -> None:
    parser = argparse.ArgumentParser(description="Mirrormere E-Ink Node Client Daemon")
    parser.add_argument(
        "-c",
        "--config",
        dest="config_path",
        default=None,
        help="Path to YAML configuration file",
    )
    parser.add_argument(
        "--once",
        action="store_true",
        help="Execute single fetch/refresh cycle and exit",
    )
    parser.add_argument(
        "-v",
        "--verbose",
        action="store_true",
        help="Enable debug logging output",
    )

    args = parser.parse_args()

    log_level = logging.DEBUG if args.verbose else logging.INFO
    logging.basicConfig(
        level=log_level,
        format="[%(asctime)s] [%(name)s] [%(levelname)s] %(message)s",
    )

    cfg = load_config(args.config_path)

    client = EinkClient(config=cfg)

    if args.once:
        logger.info("Single-cycle mode: fetching and evaluating once...")
        client.engine.mark_dirty(reason="once_cli")
        res = client.engine.execute_refresh()
        logger.info("Single-cycle result: %s", res)
        sys.exit(0 if res.status != "error" else 1)

    client.run()


if __name__ == "__main__":
    main()

"""
client.py - Standalone E-Ink Node Client daemon for Raspberry Pi Profile B
Reference: SPEC-009 §1-§8
"""

import argparse
import logging
import signal
import sys
import threading
import time
from typing import Any

try:
    from .config import EinkConfig, load_config
    from .coalesce import CoalescingEngine, RefreshResult
    from .fetcher import ETagFetcher
    from .sse import SSEListener
    from .panel import BasePanel, FakePanel, WaveshareEPDPanel, PanelManager
    from .offline import OfflineGuard
    from .buttons import ButtonHandler
    from .health import HealthServer
except ImportError:
    from config import EinkConfig, load_config
    from coalesce import CoalescingEngine, RefreshResult
    from fetcher import ETagFetcher
    from sse import SSEListener
    from panel import BasePanel, FakePanel, WaveshareEPDPanel, PanelManager
    from offline import OfflineGuard
    from buttons import ButtonHandler
    from health import HealthServer

logger = logging.getLogger("mirrormere.eink.client")


class EinkClient:
    """
    Orchestrates the SSE stream listener, background clock refresh timer,
    ETag image fetcher, refresh coalescing engine, SPI panel manager,
    offline disconnection guard, hardware button handlers, and health telemetry server.
    """

    def __init__(
        self,
        config: EinkConfig | None = None,
        panel: BasePanel | None = None,
        panel_writer: Any | None = None,
        time_fn=None,
        enable_health_server: bool = True,
    ):
        self.config = config or load_config()
        self.time_fn = time_fn or time.time
        self.start_time = self.time_fn()
        self.enable_health_server = enable_health_server
        self.custom_panel_writer = panel_writer

        self.stop_event = threading.Event()

        # Initialize Panel Manager
        self.panel_manager = PanelManager(
            config=self.config,
            panel=panel or FakePanel(),
            time_fn=self.time_fn,
        )

        # Initialize Offline Disconnection Guard
        self.offline_guard = OfflineGuard(
            config=self.config,
            panel_manager=self.panel_manager,
            time_fn=self.time_fn,
        )

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
            panel_writer=self._on_panel_write,
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

        # Initialize Hardware Buttons
        self.button_handler = ButtonHandler(
            config=self.config,
            on_force_refresh=self._on_button_force_refresh,
        )

        # Initialize Healthcheck HTTP Server
        self.health_server = None
        if self.enable_health_server:
            self.health_server = HealthServer(
                port=8099,
                telemetry_provider=self.get_telemetry,
            )

        self._threads: list[threading.Thread] = []

    def _on_panel_write(self, data: bytes, is_full: bool) -> None:
        """
        Callback from CoalescingEngine to render freshly fetched image to panel.
        """
        if self.custom_panel_writer is not None:
            self.custom_panel_writer(data, is_full)
        else:
            self.panel_manager.write_frame(data, force_full=is_full)
            self.offline_guard.record_healthy(image_data=data)

    def _on_sse_dirty(self, event_type: str, event_id: str | None, data: str) -> None:
        """Callback when SSE listener receives a dirty trigger event."""
        logger.info("SSE event '%s' (id: %s) marked screen dirty", event_type, event_id)
        self.engine.mark_dirty(reason=f"sse:{event_type}")

    def _on_sse_connect(self) -> None:
        logger.info("Connected to Mirrormere SSE event bus at %s", self.config.server.events_url)
        was_offline = self.offline_guard.record_healthy()
        if was_offline:
            logger.info("Node recovered from offline state; forcing full refresh to remove dot")
            self.engine.mark_dirty(reason="reconnect_recovery")

    def _on_sse_disconnect(self, err: Exception | None) -> None:
        if err:
            logger.warning("Disconnected from Mirrormere SSE event bus: %s", err)
        else:
            logger.info("Disconnected from Mirrormere SSE event bus")

    def _on_button_force_refresh(self) -> None:
        """Button 1 pressed: forces immediate fetch and full panel refresh."""
        logger.info("Manual hardware button triggered full refresh")
        self.engine.mark_dirty(reason="hardware_button_1")
        # Trigger immediate refresh with force_full=True
        self.engine.execute_refresh(is_full=True)

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

    def _offline_monitor_loop(self) -> None:
        """
        Periodic check for offline grace period expiration.
        """
        logger.info("Started offline status monitor loop")
        while not self.stop_event.wait(timeout=2.0):
            self.offline_guard.check_offline_status(self.sse_listener.is_connected)
        logger.info("Offline status monitor loop stopped")

    def get_telemetry(self) -> dict[str, Any]:
        """Collects diagnostic status for GET :8099/healthz."""
        now = self.time_fn()
        conn_state = "online" if self.sse_listener.is_connected else "disconnected"
        if self.offline_guard.is_offline:
            conn_state = "offline"

        return {
            "status": "ok",
            "connection_state": conn_state,
            "last_write_time": self.panel_manager.last_write_time if self.panel_manager.last_write_time >= 0 else None,
            "last_write_type": self.panel_manager.last_write_type,
            "last_etag": self.engine.last_etag,
            "partials_since_full_refresh": self.panel_manager.consecutive_partials,
            "total_full_refreshes": self.panel_manager.total_full_refreshes,
            "total_partial_refreshes": self.panel_manager.total_partial_refreshes,
            "is_offline": self.offline_guard.is_offline,
            "offline_dot_stamped": self.offline_guard.offline_dot_stamped,
            "uptime_seconds": round(now - self.start_time, 1),
        }

    def start(self) -> None:
        """Starts all background worker threads and healthcheck server."""
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
        offline_thread = threading.Thread(
            target=self._offline_monitor_loop,
            name="OfflineMonitorThread",
            daemon=True,
        )

        self._threads = [sse_thread, clock_thread, worker_thread, offline_thread]
        for t in self._threads:
            t.start()

        if self.health_server:
            self.health_server.start()

        logger.info("E-Ink Node Client daemon started successfully")

    def stop(self) -> None:
        """Signals all threads to stop, shuts down health server, and cleans up panel."""
        logger.info("Stopping E-Ink Node Client daemon...")
        self.stop_event.set()

        if self.health_server:
            self.health_server.stop()

        self.button_handler.close()

        # Wake condition variable in coalesce engine
        with self.engine._lock:
            self.engine._cond.notify_all()

        for t in self._threads:
            t.join(timeout=2.0)

        self.panel_manager.shutdown()
        logger.info("E-Ink Node Client daemon stopped")

    def run(self) -> None:
        """Starts the client and runs until interrupted by SIGINT / SIGTERM."""
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

    # Initialize panel: attempt Waveshare SPI driver on hardware, fallback to FakePanel
    panel = None
    try:
        panel = WaveshareEPDPanel(pins=cfg.panel.pins)
    except Exception as e:
        logger.info("Physical Waveshare SPI panel unavailable (%s); using FakePanel", e)
        panel = FakePanel()

    client = EinkClient(config=cfg, panel=panel)

    if args.once:
        logger.info("Single-cycle mode: fetching and evaluating once...")
        client.engine.mark_dirty(reason="once_cli")
        res = client.engine.execute_refresh()
        logger.info("Single-cycle result: %s", res)
        sys.exit(0 if res.status != "error" else 1)

    client.run()


if __name__ == "__main__":
    main()

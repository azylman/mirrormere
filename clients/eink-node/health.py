"""
health.py - Observability and healthcheck HTTP server on port 8099
Reference: SPEC-009 §Observability
"""

from http.server import HTTPServer, BaseHTTPRequestHandler
import json
import logging
import threading
import time
from typing import Callable, Any

logger = logging.getLogger("mirrormere.eink.health")


class HealthHTTPServer(HTTPServer):
    """Custom HTTPServer holding instance-level telemetry provider."""

    def __init__(self, server_address, RequestHandlerClass, telemetry_provider=None):
        super().__init__(server_address, RequestHandlerClass)
        self.telemetry_provider = telemetry_provider


class HealthRequestHandler(BaseHTTPRequestHandler):
    """Handles GET /healthz requests and renders diagnostic state."""

    def do_GET(self):
        if self.path in ("/healthz", "/health"):
            data = {}
            provider = getattr(self.server, "telemetry_provider", None)
            if provider:
                try:
                    data = provider()
                except Exception as e:
                    data = {"status": "error", "error": str(e)}
            else:
                data = {"status": "ok"}

            payload = json.dumps(data, indent=2).encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)
        else:
            self.send_response(404)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(b'{"error":"not found"}')

    def log_message(self, format, *args):
        # Suppress routine request logging to prevent journal noise
        pass


class HealthServer:
    """
    Lightweight healthcheck daemon server running on port 8099.
    """

    def __init__(
        self,
        port: int = 8099,
        telemetry_provider: Callable[[], dict[str, Any]] | None = None,
    ):
        self.port = port
        self.telemetry_provider = telemetry_provider
        self._server: HealthHTTPServer | None = None
        self._thread = None
        self._stop_event = threading.Event()

    def start(self) -> None:
        """Starts the healthcheck HTTP server in a background daemon thread."""
        try:
            self._server = HealthHTTPServer(
                ("0.0.0.0", self.port),
                HealthRequestHandler,
                telemetry_provider=self.telemetry_provider,
            )
            self._server.timeout = 1.0
            self._thread = threading.Thread(
                target=self._run_server,
                name="HealthServerThread",
                daemon=True,
            )
            self._thread.start()
            logger.info("Healthcheck endpoint active on port %d (:8099/healthz)", self.port)
        except Exception as e:
            logger.warning("Could not start healthcheck server on port %d: %s", self.port, e)

    def _run_server(self) -> None:
        while not self._stop_event.is_set():
            if self._server:
                self._server.handle_request()

    def stop(self) -> None:
        """Shuts down the healthcheck server."""
        self._stop_event.set()
        if self._server:
            try:
                self._server.server_close()
            except Exception:
                pass
        if self._thread:
            self._thread.join(timeout=1.0)
        logger.info("Healthcheck endpoint stopped")

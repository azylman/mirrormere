"""
sse.py - Server-Sent Events (SSE) listener and trigger router for Mirrormere E-Ink display node
Reference: SPEC-009 §1-§3, SPEC-006 §1-§3
"""

import logging
import threading
import time
import urllib.request
import urllib.error

logger = logging.getLogger("mirrormere.eink.sse")

# Events that indicate screen contents have changed and require an e-paper redraw
DIRTY_EVENTS = {
    "widget.update",
    "header.update",
    "screen.rotate",
    "widget.reload",
    "style.reload",
}

# Events that are explicitly ignored from triggering e-paper refreshes (to avoid unnecessary panel flashing)
IGNORED_EVENTS = {
    "system.status",
    "voice.state",
    "audio.state",
    "video.state",
}


def parse_sse_block(lines: list[str]) -> tuple[bool, str, str | None, str]:
    """
    Parses a single buffered SSE message block into (has_content, event_type, event_id, data).
    Lines starting with ':' are comments and ignored.
    """
    event_type = "message"
    event_id: str | None = None
    data_parts: list[str] = []
    has_event = False
    has_data = False
    has_id = False

    for raw_line in lines:
        line = raw_line.rstrip("\r\n")
        if not line or line.startswith(":"):
            continue

        if ":" in line:
            field, _, value = line.partition(":")
            if value.startswith(" "):
                value = value[1:]
        else:
            field = line
            value = ""

        if field == "event":
            event_type = value
            has_event = True
        elif field == "id":
            event_id = value
            has_id = True
        elif field == "data":
            data_parts.append(value)
            has_data = True

    has_content = has_event or has_data or has_id
    return has_content, event_type, event_id, "\n".join(data_parts)


class SSEListener:
    """
    Maintains a persistent SSE stream to GET /api/events with Last-Event-ID tracking
    and exponential backoff (1s to 60s max). Routes dirty events to the coalescing engine.
    """

    def __init__(
        self,
        events_url: str,
        on_dirty=None,
        on_event=None,
        on_connect=None,
        on_disconnect=None,
        initial_backoff: float = 1.0,
        max_backoff: float = 60.0,
        backoff_factor: float = 2.0,
        socket_timeout: float = 45.0,
        opener=None,
        time_fn=None,
        sleep_fn=None,
    ):
        self.events_url = events_url
        self.on_dirty = on_dirty
        self.on_event = on_event
        self.on_connect = on_connect
        self.on_disconnect = on_disconnect
        self.initial_backoff = initial_backoff
        self.max_backoff = max_backoff
        self.backoff_factor = backoff_factor
        self.socket_timeout = socket_timeout
        self.opener = opener or urllib.request.urlopen
        self.time_fn = time_fn or time.time
        self.sleep_fn = sleep_fn

        self.last_event_id: str | None = None
        self.is_connected: bool = False
        self.connect_count: int = 0
        self.disconnect_count: int = 0

    def run(self, stop_event: threading.Event) -> None:
        """
        Runs the SSE connection loop until stop_event is set.
        """
        current_backoff = self.initial_backoff

        while not stop_event.is_set():
            headers = {
                "Accept": "text/event-stream",
                "Cache-Control": "no-cache",
                "User-Agent": "Mirrormere-EInk-Node/1.0",
            }
            if self.last_event_id:
                headers["Last-Event-ID"] = self.last_event_id

            req = urllib.request.Request(self.events_url, headers=headers, method="GET")

            try:
                logger.info(
                    "Connecting to SSE stream at %s (Last-Event-ID: %s)",
                    self.events_url,
                    self.last_event_id,
                )
                with self.opener(req, timeout=self.socket_timeout) as resp:
                    self.is_connected = True
                    self.connect_count += 1
                    current_backoff = self.initial_backoff
                    if self.on_connect:
                        try:
                            self.on_connect()
                        except Exception as cb_err:
                            logger.error("Error in on_connect callback: %s", cb_err)

                    line_buffer: list[str] = []

                    for raw_line in resp:
                        if stop_event.is_set():
                            break

                        if isinstance(raw_line, bytes):
                            line = raw_line.decode("utf-8", errors="replace")
                        else:
                            line = str(raw_line)

                        stripped = line.rstrip("\r\n")

                        # Empty line signals the dispatch of an accumulated message block
                        if not stripped:
                            if line_buffer:
                                has_content, event_type, event_id, event_data = parse_sse_block(line_buffer)
                                line_buffer = []

                                if not has_content:
                                    continue

                                if event_id is not None:
                                    self.last_event_id = event_id

                                if event_type in DIRTY_EVENTS:
                                    logger.debug("Received dirty event: %s (id: %s)", event_type, event_id)
                                    if self.on_dirty:
                                        try:
                                            self.on_dirty(event_type, event_id, event_data)
                                        except Exception as cb_err:
                                            logger.error("Error in on_dirty callback: %s", cb_err)
                                elif event_type in IGNORED_EVENTS:
                                    logger.debug("Ignoring non-dirty event: %s", event_type)

                                if self.on_event:
                                    try:
                                        self.on_event(event_type, event_id, event_data)
                                    except Exception as cb_err:
                                        logger.error("Error in on_event callback: %s", cb_err)
                            continue

                        line_buffer.append(stripped)

            except Exception as e:
                self.is_connected = False
                self.disconnect_count += 1
                logger.warning("SSE connection dropped or failed: %s", e)
                if self.on_disconnect:
                    try:
                        self.on_disconnect(e)
                    except Exception as cb_err:
                        logger.error("Error in on_disconnect callback: %s", cb_err)
            else:
                self.is_connected = False
                self.disconnect_count += 1
                logger.info("SSE stream closed by server")
                if self.on_disconnect:
                    try:
                        self.on_disconnect(None)
                    except Exception as cb_err:
                        logger.error("Error in on_disconnect callback: %s", cb_err)

            if stop_event.is_set():
                break

            logger.info("Reconnecting in %.1fs (exponential backoff)...", current_backoff)
            if self.sleep_fn:
                self.sleep_fn(current_backoff)
            else:
                stop_event.wait(timeout=current_backoff)

            current_backoff = min(self.max_backoff, current_backoff * self.backoff_factor)

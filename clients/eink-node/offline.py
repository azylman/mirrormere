"""
offline.py - Offline disconnection guard, 8x8 dot overlay, and persistence
Reference: SPEC-009 §Offline Behavior
"""

import logging
import threading
import time

try:
    from .config import EinkConfig
    from .image import stamp_offline_dot, save_last_image, load_last_image
    from .panel import PanelManager
except ImportError:
    from config import EinkConfig
    from image import stamp_offline_dot, save_last_image, load_last_image
    from panel import PanelManager

logger = logging.getLogger("mirrormere.eink.offline")


class OfflineGuard:
    """
    Monitors SSE stream reachability and fetch health.
    If disconnected or fetch fails for > offline_grace_seconds (default 120s):
      - Stamps an 8x8 black square in the top-right corner of the last good frame.
      - Pushes a single partial refresh to alert user that data is stale.
      - Persists to disk so a reboot while offline restores the screen + dot.
    On reconnection:
      - Automatically triggers a full refresh to clear accumulated ghosting and remove the dot.
    """

    def __init__(
        self,
        config: EinkConfig,
        panel_manager: PanelManager,
        persistence_path: str = "/var/lib/mirrormere-eink/last.png",
        time_fn=None,
    ):
        self.config = config
        self.panel_manager = panel_manager
        self.persistence_path = persistence_path
        self.time_fn = time_fn or time.time

        self._lock = threading.Lock()
        self.is_offline: bool = False
        self.offline_dot_stamped: bool = False
        self.last_healthy_time: float = self.time_fn()
        self.last_good_image: bytes | None = load_last_image(self.persistence_path)

    def record_healthy(self, image_data: bytes | None = None) -> bool:
        """
        Records that the node is online and healthy.
        Returns True if the node was previously offline (signaling that a full refresh is required).
        """
        with self._lock:
            now = self.time_fn()
            self.last_healthy_time = now

            if image_data:
                self.last_good_image = image_data
                save_last_image(image_data, self.persistence_path)

            was_offline = self.is_offline or self.offline_dot_stamped

            if was_offline:
                logger.info("Node recovered from offline state; scheduling full refresh to remove dot")
                self.is_offline = False
                self.offline_dot_stamped = False
                return True

            return False

    def check_offline_status(self, is_connected: bool) -> bool:
        """
        Evaluates offline grace period.
        If offline > offline_grace_seconds and dot not yet stamped, renders the 8x8 dot.
        Returns True if the dot was stamped on this check.
        """
        with self._lock:
            now = self.time_fn()

            if is_connected:
                self.last_healthy_time = now
                return False

            offline_duration = now - self.last_healthy_time
            if offline_duration >= self.config.refresh.offline_grace_seconds:
                self.is_offline = True
                if not self.offline_dot_stamped and self.last_good_image:
                    logger.warning(
                        "Offline grace period exceeded (%.1fs >= %.1fs); stamping 8x8 disconnect dot",
                        offline_duration,
                        self.config.refresh.offline_grace_seconds,
                    )
                    try:
                        dotted_frame = stamp_offline_dot(self.last_good_image)
                        # Perform one partial refresh with dotted frame
                        self.panel_manager.write_frame(dotted_frame, force_full=False)
                        self.offline_dot_stamped = True
                        save_last_image(dotted_frame, self.persistence_path)
                        return True
                    except Exception as e:
                        logger.error("Failed to stamp and display offline dot: %s", e)

            return False

"""
buttons.py - Adafruit E-Ink Bonnet hardware button listeners and REST action dispatch
Reference: SPEC-009 §Buttons
"""

import logging
import threading
import urllib.request
import urllib.error

try:
    from .config import EinkConfig
except ImportError:
    from config import EinkConfig

logger = logging.getLogger("mirrormere.eink.buttons")


class ButtonHandler:
    """
    Manages physical hardware button inputs on the Adafruit E-Ink Bonnet:
      - Button 1 (default GPIO 5): Forces immediate fetch and full panel refresh.
      - Button 2 (default GPIO 6): Dispatches POST /api/screen/advance to Core.
    """

    def __init__(
        self,
        config: EinkConfig,
        on_force_refresh=None,
        on_advance_screen=None,
        gpio_button_factory=None,
    ):
        self.config = config
        self.on_force_refresh = on_force_refresh
        self.on_advance_screen = on_advance_screen or self._default_advance_screen
        self.gpio_button_factory = gpio_button_factory

        self.btn_refresh = None
        self.btn_advance = None

        if not self.config.buttons.enabled:
            logger.info("Hardware buttons disabled in configuration")
            return

        self._init_gpio()

    def _init_gpio(self) -> None:
        """Initializes gpiozero Button instances if available."""
        factory = self.gpio_button_factory
        if factory is None:
            try:
                from gpiozero import Button as GpioButton
                factory = GpioButton
            except ImportError:
                logger.warning("gpiozero not available; hardware buttons inactive")
                return

        try:
            pin_ref = self.config.buttons.refresh_pin
            pin_adv = self.config.buttons.next_screen_pin

            logger.info("Initializing hardware buttons: Refresh=GPIO%d, Advance=GPIO%d", pin_ref, pin_adv)
            self.btn_refresh = factory(pin_ref, bounce_time=0.1)
            self.btn_advance = factory(pin_adv, bounce_time=0.1)

            self.btn_refresh.when_pressed = self.handle_button_refresh
            self.btn_advance.when_pressed = self.handle_button_advance
        except Exception as e:
            logger.error("Failed to initialize GPIO buttons: %s", e)

    def handle_button_refresh(self) -> None:
        """Button 1 pressed: Force fetch and full panel refresh."""
        logger.info("Button 1 (GPIO %d) pressed: forcing full refresh", self.config.buttons.refresh_pin)
        if self.on_force_refresh:
            try:
                self.on_force_refresh()
            except Exception as e:
                logger.error("Error in on_force_refresh callback: %s", e)

    def handle_button_advance(self) -> None:
        """Button 2 pressed: Advance to next screen."""
        logger.info("Button 2 (GPIO %d) pressed: advancing screen", self.config.buttons.next_screen_pin)
        if self.on_advance_screen:
            try:
                self.on_advance_screen()
            except Exception as e:
                logger.error("Error in on_advance_screen callback: %s", e)

    def _default_advance_screen(self) -> None:
        """Dispatches POST /api/screen/advance in background thread."""
        base_url = self.config.server.events_url.replace("/api/events", "")
        url = f"{base_url}/api/screen/advance"

        def _do_post():
            try:
                logger.info("Dispatching POST %s", url)
                req = urllib.request.Request(url, data=b"{}", headers={"Content-Type": "application/json"}, method="POST")
                with urllib.request.urlopen(req, timeout=5.0) as resp:
                    logger.info("Screen advance response: %d", resp.status)
            except Exception as e:
                logger.error("Failed to dispatch screen advance to %s: %s", url, e)

        t = threading.Thread(target=_do_post, name="ScreenAdvanceThread", daemon=True)
        t.start()

    def close(self) -> None:
        """Cleans up GPIO button handles."""
        if self.btn_refresh and hasattr(self.btn_refresh, "close"):
            try:
                self.btn_refresh.close()
            except Exception:
                pass
        if self.btn_advance and hasattr(self.btn_advance, "close"):
            try:
                self.btn_advance.close()
            except Exception:
                pass

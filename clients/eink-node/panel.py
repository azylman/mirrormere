"""
panel.py - Waveshare SPI panel driver controller, Adafruit Bonnet pin patching, and refresh lifecycle
Reference: SPEC-009 §Reference Hardware, §Refresh Lifecycle, §Pin Map
"""

from abc import ABC, abstractmethod
import io
import logging
import threading
import time
from typing import Any

try:
    from .config import EinkConfig, PanelPinsConfig
    from .image import validate_png
except ImportError:
    from config import EinkConfig, PanelPinsConfig
    from image import validate_png

logger = logging.getLogger("mirrormere.eink.panel")


def _repin_implementation(impl: Any, pins: PanelPinsConfig) -> tuple:
    """
    Re-creates the gpiozero pin objects owned by the real Waveshare
    epdconfig.RaspberryPi implementation. The real driver builds them in
    __init__ at import time, so changing attributes alone does not move the
    hardware lines. Returns the (rst, dc, busy, cs, pwr) actually applied.
    """
    import gpiozero

    pwr = pins.pwr
    if pwr is None:
        # The real driver calls GPIO_PWR_PIN.on()/.off() unconditionally in
        # module_init/module_exit, so it cannot run without a PWR pin.
        pwr = getattr(impl, "PWR_PIN", None)
        logger.warning(
            "Real Waveshare driver requires a PWR pin; keeping PWR at existing pin %s",
            pwr,
        )

    current = (
        getattr(impl, "RST_PIN", None),
        getattr(impl, "DC_PIN", None),
        getattr(impl, "BUSY_PIN", None),
        getattr(impl, "PWR_PIN", None),
    )
    wanted = (pins.rst, pins.dc, pins.busy, pwr)
    if hasattr(impl, "CS_PIN"):
        impl.CS_PIN = pins.cs
    if current == wanted:
        logger.info("Driver pins already match configuration; no repin needed")
        return (wanted[0], wanted[1], wanted[2], pins.cs, wanted[3])

    for name in ("GPIO_RST_PIN", "GPIO_DC_PIN", "GPIO_PWR_PIN", "GPIO_BUSY_PIN"):
        old = getattr(impl, name, None)
        if old is not None:
            try:
                old.close()
            except Exception:
                logger.exception("Failed to close %s", name)

    impl.RST_PIN, impl.DC_PIN, impl.BUSY_PIN, impl.PWR_PIN = wanted
    impl.GPIO_RST_PIN = gpiozero.LED(impl.RST_PIN)
    impl.GPIO_DC_PIN = gpiozero.LED(impl.DC_PIN)
    impl.GPIO_PWR_PIN = gpiozero.LED(impl.PWR_PIN)
    impl.GPIO_BUSY_PIN = gpiozero.Button(impl.BUSY_PIN, pull_up=False)
    return (wanted[0], wanted[1], wanted[2], pins.cs, wanted[3])


def patch_bonnet_pins(pins: PanelPinsConfig, epdconfig_module: Any) -> None:
    """
    Applies the Adafruit E-Ink Bonnet pin mapping to the loaded epdconfig
    prior to calling epd.init().
    Reference: SPEC-009 §Pin Map
      RST: 27, DC: 22, BUSY: 17, CS: 8, PWR: None

    With the real Waveshare driver the pins live on
    epdconfig.implementation (created at import time), so they are set there
    and the gpiozero objects are re-created. The vendored stub keeps plain
    module-level constants.
    """
    impl = getattr(epdconfig_module, "implementation", None)
    if impl is not None:
        rst, dc, busy, cs, pwr = _repin_implementation(impl, pins)
        # EPD.__init__ reads pin numbers from the module, and the real
        # digital_write/read dispatch against the implementation's pins, so
        # the module constants must match what was actually applied.
        epdconfig_module.RST_PIN = rst
        epdconfig_module.DC_PIN = dc
        epdconfig_module.BUSY_PIN = busy
        epdconfig_module.CS_PIN = cs
        epdconfig_module.PWR_PIN = pwr
    else:
        if hasattr(epdconfig_module, "RST_PIN"):
            epdconfig_module.RST_PIN = pins.rst
        if hasattr(epdconfig_module, "DC_PIN"):
            epdconfig_module.DC_PIN = pins.dc
        if hasattr(epdconfig_module, "BUSY_PIN"):
            epdconfig_module.BUSY_PIN = pins.busy
        if hasattr(epdconfig_module, "CS_PIN"):
            epdconfig_module.CS_PIN = pins.cs
        if hasattr(epdconfig_module, "PWR_PIN"):
            epdconfig_module.PWR_PIN = pins.pwr

    logger.info(
        "Patched epdconfig pins: RST=%s, DC=%s, BUSY=%s, CS=%s, PWR=%s",
        pins.rst,
        pins.dc,
        pins.busy,
        pins.cs,
        pins.pwr,
    )


class BasePanel(ABC):
    """Abstract interface for e-paper panel controllers."""

    @abstractmethod
    def init_full(self) -> None:
        """Initializes panel for a full refresh."""
        pass

    @abstractmethod
    def display_full(self, buffer: bytes) -> None:
        """Pushes full frame to panel display memory."""
        pass

    @abstractmethod
    def init_part(self) -> None:
        """Initializes panel for a partial refresh."""
        pass

    @abstractmethod
    def display_partial(self, buffer: bytes) -> None:
        """Pushes partial frame to panel display memory."""
        pass

    @abstractmethod
    def sleep(self) -> None:
        """Puts panel into deep sleep mode."""
        pass

    @abstractmethod
    def clear(self) -> None:
        """Clears panel display registers to blank white."""
        pass


class FakePanel(BasePanel):
    """
    Hermetic test double for e-paper display hardware.
    Tracks all lifecycle calls without touching physical SPI or GPIO lines.
    """

    def __init__(self):
        self.full_refreshes: int = 0
        self.partial_refreshes: int = 0
        self.sleeps: int = 0
        self.clears: int = 0
        self.frames: list[bytes] = []
        self.is_sleeping: bool = True
        self.last_mode: str | None = None

    def init_full(self) -> None:
        self.is_sleeping = False
        self.last_mode = "full"

    def display_full(self, buffer: bytes) -> None:
        self.full_refreshes += 1
        self.frames.append(buffer)

    def init_part(self) -> None:
        self.is_sleeping = False
        self.last_mode = "partial"

    def display_partial(self, buffer: bytes) -> None:
        self.partial_refreshes += 1
        self.frames.append(buffer)

    def sleep(self) -> None:
        self.sleeps += 1
        self.is_sleeping = True

    def clear(self) -> None:
        self.clears += 1


class WaveshareEPDPanel(BasePanel):
    """
    Hardware panel driver targeting Waveshare 7.5inch V2 raw e-paper display
    via Adafruit E-Ink Bonnet pinout.
    """

    def __init__(self, pins: PanelPinsConfig):
        self.pins = pins
        self._epd = None

        # Dynamically import or load epd driver module
        try:
            from waveshare_epd import epd7in5_V2, epdconfig
        except ImportError:
            try:
                from .driver import epd7in5_V2, epdconfig
            except ImportError:
                from driver import epd7in5_V2, epdconfig

        # Apply Bonnet pin patches before hardware init
        patch_bonnet_pins(self.pins, epdconfig)
        self._epd = epd7in5_V2.EPD()

    def init_full(self) -> None:
        self._epd.init()

    @staticmethod
    def _decode(buffer: bytes):
        """Decodes PNG bytes to a PIL Image, as the real getbuffer() expects."""
        from PIL import Image

        return Image.open(io.BytesIO(buffer))

    def display_full(self, buffer: bytes) -> None:
        buf = self._epd.getbuffer(self._decode(buffer))
        self._epd.display(buf)

    def init_part(self) -> None:
        self._epd.init_part()

    def display_partial(self, buffer: bytes) -> None:
        buf = self._epd.getbuffer(self._decode(buffer))
        self._epd.display_Partial(buf, 0, 0, self._epd.width, self._epd.height)

    def sleep(self) -> None:
        self._epd.sleep()

    def clear(self) -> None:
        self._epd.Clear()


class PanelManager:
    """
    Supervises the e-paper panel refresh lifecycle:
      - Initial boot: full refresh
      - Periodic full refresh: every full_refresh_minutes (default 60m)
      - Consecutive partials limit: full refresh after 30 partials
      - Reconnection recovery: forces full refresh
      - Mandatory deep sleep: calls sleep() after every single write
      - Input validation: verifies 800x480 1-bit PNG before touching SPI
    """

    def __init__(
        self,
        config: EinkConfig,
        panel: BasePanel | None = None,
        time_fn=None,
    ):
        self.config = config
        self.panel = panel or FakePanel()
        self.time_fn = time_fn or time.time

        self._lock = threading.Lock()
        self.consecutive_partials: int = 0
        self.last_full_refresh_time: float = -1.0
        self.last_write_time: float = -1.0
        self.last_write_type: str | None = None
        self.total_full_refreshes: int = 0
        self.total_partial_refreshes: int = 0

    def write_frame(self, data: bytes, force_full: bool = False) -> str:
        """
        Validates frame and dispatches either full or partial panel refresh,
        guaranteeing immediate deep sleep upon completion.
        Returns the refresh type ("full", "partial", or "rejected").
        """
        # Validate input PNG
        is_valid, err = validate_png(data)
        if not is_valid:
            logger.error("Rejecting malformed frame before SPI write: %s", err)
            return "rejected"

        with self._lock:
            now = self.time_fn()

            # Determine whether full or partial refresh is required
            needs_full = (
                force_full
                or (self.total_full_refreshes == 0)
                or (self.consecutive_partials >= self.config.refresh.max_consecutive_partials)
                or (
                    self.last_full_refresh_time >= 0
                    and (now - self.last_full_refresh_time) >= self.config.refresh.full_refresh_minutes * 60
                )
            )

            start_t = time.perf_counter()
            refresh_type = "full" if needs_full else "partial"

            try:
                if needs_full:
                    logger.info(
                        "Executing FULL panel refresh (consecutive partials: %d, forced: %s)",
                        self.consecutive_partials,
                        force_full,
                    )
                    self.panel.init_full()
                    self.panel.display_full(data)
                    self.total_full_refreshes += 1
                    self.consecutive_partials = 0
                    self.last_full_refresh_time = now
                else:
                    logger.info(
                        "Executing PARTIAL panel refresh (partial #%d of %d)",
                        self.consecutive_partials + 1,
                        self.config.refresh.max_consecutive_partials,
                    )
                    self.panel.init_part()
                    self.panel.display_partial(data)
                    self.total_partial_refreshes += 1
                    self.consecutive_partials += 1

                self.last_write_time = now
                self.last_write_type = refresh_type
            finally:
                # Mandatory deep sleep after every single write to protect hardware
                self.panel.sleep()
                duration = time.perf_counter() - start_t
                logger.info(
                    "Panel write complete: type=%s, duration=%.3fs, deep_sleep=active",
                    refresh_type,
                    duration,
                )

            return refresh_type

    def shutdown(self) -> None:
        """
        Handles graceful teardown. Clears screen if clear_on_shutdown is enabled.
        """
        with self._lock:
            if self.config.refresh.clear_on_shutdown:
                logger.info("clear_on_shutdown enabled: clearing e-paper panel")
                try:
                    self.panel.init_full()
                    self.panel.clear()
                finally:
                    self.panel.sleep()
            else:
                logger.info("Preserving last bistable image on panel during shutdown")

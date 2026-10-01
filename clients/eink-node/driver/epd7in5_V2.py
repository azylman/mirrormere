"""
epd7in5_V2.py - Waveshare 7.5inch e-Paper V2 driver reference module
Reference: SPEC-009 §Reference Hardware, §Refresh Lifecycle
"""

import logging
from . import epdconfig

logger = logging.getLogger("mirrormere.eink.driver.epd7in5_V2")

EPD_WIDTH = 800
EPD_HEIGHT = 480


class EPD:
    def __init__(self):
        self.width = EPD_WIDTH
        self.height = EPD_HEIGHT
        self.is_sleeping = False
        self.is_initialized = False
        self.reset_pin = epdconfig.RST_PIN
        self.dc_pin = epdconfig.DC_PIN
        self.busy_pin = epdconfig.BUSY_PIN
        self.cs_pin = epdconfig.CS_PIN

    def send_command(self, command):
        """Writes one command byte (DC low)."""
        logger.debug("EPD send_command 0x%02X", command)

    def send_data(self, data):
        """Writes one data byte (DC high)."""
        logger.debug("EPD send_data 0x%02X", data)

    def send_data2(self, data):
        """Writes a bytes-like block of data in one SPI transfer (spidev.writebytes2)."""
        logger.debug("EPD send_data2 (%d bytes)", len(data))

    def ReadBusy(self):
        """Waits for BUSY to release (stub: returns immediately)."""

    def init(self):
        """Full refresh initialization."""
        epdconfig.module_init()
        self.is_sleeping = False
        self.is_initialized = True
        logger.debug("EPD initialized for full refresh")
        return 0

    def init_part(self):
        """Partial refresh initialization."""
        epdconfig.module_init()
        self.is_sleeping = False
        self.is_initialized = True
        logger.debug("EPD initialized for partial refresh")
        return 0

    def getbuffer(self, image):
        """Converts PIL or raw buffer into 1-bit scanline array."""
        if hasattr(image, "tobytes"):
            return bytearray(image.tobytes())
        return bytearray(image)

    def display(self, image):
        """Pushes full frame to panel display registers."""
        logger.debug("EPD full display write (%d bytes)", len(image) if image else 0)

    def display_Partial(self, Image, Xstart, Ystart, Xend, Yend):
        """Pushes a partial window (Xstart, Ystart)-(Xend, Yend) to the panel."""
        logger.debug(
            "EPD partial display write (%d bytes, window %d,%d-%d,%d)",
            len(Image) if Image else 0, Xstart, Ystart, Xend, Yend,
        )

    def Clear(self):
        """Clears panel to white."""
        logger.debug("EPD clear called")

    def sleep(self):
        """Puts panel into deep sleep mode to prevent DC bias degradation."""
        self.is_sleeping = True
        epdconfig.module_exit()
        logger.debug("EPD entered deep sleep mode")

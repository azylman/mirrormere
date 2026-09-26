"""
epdconfig.py - Pin definitions and hardware configuration for Waveshare e-Paper panels.
Reference: SPEC-009 §Reference Hardware

Default values reflect the Waveshare HAT.
Adafruit E-Ink Bonnet requires dynamic patching before init():
  RST: 27, DC: 22, BUSY: 17, CS: 8, PWR: None
"""

import logging

logger = logging.getLogger("mirrormere.eink.epdconfig")

# Default Waveshare HAT pin mapping
RST_PIN = 17
DC_PIN = 25
CS_PIN = 8
BUSY_PIN = 24
PWR_PIN = 18

IS_SPI_INITIALIZED = False


def module_init():
    global IS_SPI_INITIALIZED
    IS_SPI_INITIALIZED = True
    logger.debug(
        "epdconfig module_init called (RST=%s, DC=%s, BUSY=%s, CS=%s, PWR=%s)",
        RST_PIN,
        DC_PIN,
        BUSY_PIN,
        CS_PIN,
        PWR_PIN,
    )
    return 0


def module_exit():
    global IS_SPI_INITIALIZED
    IS_SPI_INITIALIZED = False
    logger.debug("epdconfig module_exit called")


def digital_write(pin, value):
    pass


def digital_read(pin):
    return 0


def spi_writebyte(data):
    pass


def spi_writebyte2(data):
    pass


def delay_ms(delaytime):
    pass

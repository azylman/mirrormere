# SPEC-002: Hardware Profiles & Bill of Materials

## Status
Approved / Hardware In-Flight

## Context
Mirrormere targets two hardware archetypes:
1. **Interactive Touch Kiosk**: Kitchen/hallway family command center with full touch, media streaming, and voice.
2. **Ambient E-Paper Radiator**: Low-power, distraction-free wall newspaper and agenda glance.

---

## Profile A: Interactive Touch Kiosk (Alex)

### Bill of Materials (BOM)
- **Display**: UPERFECT 15.6" 1080p Capacitive Touchscreen Monitor (rear 75×75mm VESA holes, USB-C single-cable power/video/touch).
- **Compute Unit**: Beelink Mini S12 Pro (Intel N100 4C/4T up to 3.4GHz, 16GB DDR4 RAM, 500GB NVMe SSD, ~6W idle).
- **Video Capture Ingest**: USB 3.0 HDMI Video Capture Dongle (MS2109 or MacroSilicon UVC compliant, 1080p60 input).
- **Cast Receiver**: Google Chromecast (HDMI output feeding into the capture card).
- **Audio Node**: Nano USB Microphone Dongle (thumbnail-sized stub plugged into rear USB port).
- **Mounting**: Low-profile fixed 75×75mm VESA wall mount + short right-angle USB-C and HDMI jumper cables.

### Physical Architecture ("The VESA Sandwich")
- The Beelink Mini S12 Pro includes a metal VESA bracket in the box.
- The mini PC bolts directly to the back of the UPERFECT monitor using the 75×75mm pattern.
- The entire assembly hangs as a single integrated unit off the wall bracket, protruding approximately 2.5" from the drywall.
- A single 12V/19V power line or recessed in-wall power box powers the system.

### Operating System & Runtime Environment
- **Base OS**: Minimal Debian 12 / Ubuntu Server (headless base, zero desktop bloat).
- **Display Server**: Wayland with the `cage` kiosk compositor (single-application fullscreen confinement).
- **Frontend Container**: Chromium browser launched with:
  ```bash
  chromium --kiosk --noerrdialogs --disable-infobars \
    --check-for-update-interval=31536000 \
    --enable-features=OverlayScrollbar \
    --use-gl=egl \
    http://localhost:8080
  ```
- **Power Management**: Display DPMS sleep via `wlr-randr` or CEC scheduling based on room motion or time of day.

---

## Profile B: Ambient E-Paper Kiosk (Mike & Amos)

### Bill of Materials (BOM)
- **Display**: Waveshare 7.5" V2 raw e-paper panel (800×480, SPI, Black & White).
  > **Critical Warning**: Must be strictly the Black/White V2 model. 3-color (Red/Yellow) panels require 15–30 seconds per full refresh and lack partial refresh support.
- **Driver Board**: Adafruit E-Ink Bonnet for Raspberry Pi (24-pin FPC). Pin map differs from the Waveshare HAT; see SPEC-009.
  - The all-in-one Waveshare 7.5" e-Paper HAT (V2) is a supported alternative using the driver's default pins.
- **Compute Unit**: Raspberry Pi 3 Model B+ (Mike's unit, already owned). Raspberry Pi 4 Model B also supported.
- **Power Supply**: 5V 2.5A micro-USB supply (Pi 3 B+), or official USB-C supply (Pi 4B).
- **Storage**: 32GB Class A1 MicroSD Card.
- **Sensors (optional)**: DHT22 temperature/humidity sensor for an indoor-climate widget.
- **Audio / Voice Array (optional)**: reSpeaker XVF3800 USB 4-Mic Array with case.
- **Mounting**: None for now; M2.5 nylon standoffs secure the Bonnet to the Pi.

### Physical Architecture
- The Adafruit E-Ink Bonnet stacks onto the Pi's 40-pin GPIO header, held by M2.5 standoffs.
- The panel's 24-pin ribbon connects to the Bonnet's FPC connector.
- Rendering happens on the server (SPEC-003 §3 sidecar); the Pi only fetches and flushes images, so no active cooling is needed.

### Operating System & Runtime Environment
- **Base OS**: Raspberry Pi OS Lite (64-bit, headless, no X11/Wayland).
- **Hardware Interface**: SPI enabled via `/boot/config.txt` (`dtparam=spi=on`).
- **Rendering Pipeline**: No local rendering. The `clients/eink-node` daemon (SPEC-009) fetches the sidecar's 1-bit PNG and pushes it to the panel via `spidev`.
- **Refresh Strategy**:
  - Full refresh every 60 minutes to clear accumulated ghosting.
  - Partial refreshes on state change (e.g. new calendar events, chore completion) or every 5 minutes for clock updates.
  - Strict zero-animation rule: UI redrawing is purely event- or timer-driven.

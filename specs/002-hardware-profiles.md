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
- **Display**: Waveshare 7.5" e-Paper HAT (V2, 800×480 resolution, SPI interface, Black & White).
  > **Critical Warning**: Must be strictly the Black/White V2 model. 3-color (Red/Yellow) panels require 15–30 seconds per full refresh and lack partial refresh support.
- **Compute Unit**: Raspberry Pi 4 Model B (4GB RAM).
- **Power Supply**: Official Raspberry Pi 15W (5.1V / 3A) or 27W USB-C Power Adapter.
- **Storage**: 32GB Class A1 MicroSD Card (SanDisk Ultra / High Endurance).
- **Thermals**: Low-profile adhesive copper/aluminum heatsink pack (passive fins clearing the GPIO HAT).
- **Audio / Voice Array**: reSpeaker XVF3800 USB 4-Mic Array with case (or nano USB mic stub).
- **Mounting**: Flat bench stand or custom 3D printed bezel.

### Physical Architecture
- The Waveshare HAT stacks directly onto the Pi 4B 40-pin GPIO header via standard standoffs.
- The ribbon cable connects the HAT driver board to the raw 7.5" e-paper raw panel mounted on the front.
- Passive heatsinks provide adequate cooling for the BCM2711 SoC without requiring a noisy or bulky active fan.

### Operating System & Runtime Environment
- **Base OS**: Raspberry Pi OS Lite (64-bit, headless, no X11/Wayland).
- **Hardware Interface**: SPI enabled via `/boot/config.txt` (`dtparam=spi=on`).
- **Rendering Pipeline**: Python script utilizing Pillow (PIL) or headless SVG/cairo rendering, pushing raw byte buffers directly to the e-paper driver via `spidev`.
- **Refresh Strategy**:
  - Full refresh every 60 minutes to clear accumulated ghosting.
  - Partial refreshes on state change (e.g. new calendar events, chore completion) or every 5 minutes for clock updates.
  - Strict zero-animation rule: UI redrawing is purely event- or timer-driven.

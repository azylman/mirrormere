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
- **Video Capture Ingest**: USB 3.0 HDMI Video Capture Dongle (MacroSilicon MS2130 or UVC-compliant USB 3.0 chip for uncompressed 1080p60 capture; MS2109 USB 2.0 supported as fallback at 1080p30 MJPEG with hardware transcode).
- **Cast Receiver**: Google Chromecast (HDMI output feeding into the capture card).
- **Audio Output**: Dual built-in stereo speakers integrated into the UPERFECT monitor chassis (audio delivered digitally over HDMI/USB-C, zero extra cables).
- **Audio / Voice Ingest (optional)**: Nano USB Microphone Dongle (thumbnail-sized stub plugged into rear USB port; software voice stack deferred past v1).
- **Mounting**: Low-profile fixed 75×75mm VESA wall mount + short right-angle USB-C and HDMI jumper cables.

### Physical Architecture ("The VESA Sandwich")
- The Beelink Mini S12 Pro includes a metal VESA bracket in the box.
- The mini PC bolts directly to the back of the UPERFECT monitor using the 75×75mm pattern.
- The entire assembly hangs as a single integrated unit off the wall bracket, protruding approximately 2.5" from the drywall.
- A single 12V/19V power line or recessed in-wall power box powers the system.

### Operating System & Runtime Environment
- **Base OS**: Minimal Debian 12 / Ubuntu Server (headless base, zero desktop bloat).
- **Display Server**: Wayland with the `cage` kiosk compositor (single-application fullscreen confinement).
- **Frontend Runtime**: Chromium browser running in `--kiosk` mode pointing to `http://localhost:8080/display` (see SPEC-010).
- **Input Strategy**: Glance-and-tap only (screen navigation swipes, video dismiss, tap media transport controls). Zero on-screen virtual keyboard (OSK); task lists are strictly read-only ambient surfaces, and all task additions/edits are handled phone-first at the source.
- **Power Management**: Display DPMS sleep via `swayidle` and `wlr-randr` (see SPEC-010):
  - Fixed night schedule (display hard sleep 11 PM – 6 AM).
  - Daytime idle timeout (10 minutes of inactivity blanks panel).
  - Wake on tap: touch digitizer input event instantly restores display power.

### Reference Deployment Topology & Manifest (`deploy/compose.yml`)
Profile A runs on the Beelink N100 mini PC driving the touch kiosk directly. Host bootstrap and Wayland `cage` launch scripts are provisioned from `deploy/kiosk/`. The core daemon, `go2rtc` stream ingest, and custom `cast-watcher` bridge run as Docker containers activated via Compose's native `video` profile:

```yaml
# deploy/compose.yml (Activated with: docker compose --profile video up -d)
services:
  mirrormere-core:
    build: .
    image: ghcr.io/azylman/mirrormere:latest
    container_name: mirrormere-core
    restart: unless-stopped
    ports:
      - "8080:8080" # Core Daemon: REST API, SSE (/api/events), web UI
    environment:
      - PORT=8080
      - HOST=0.0.0.0
      - TZ=America/Los_Angeles
    volumes:
      - ./config.yaml:/config/config.yaml:ro
      - ./custom.css:/config/custom.css:ro # e.g. from deploy/examples/kiosk.css
      - ./data:/data
    healthcheck:
      test: ["CMD", "wget", "--no-verbose", "--tries=1", "--spider", "http://127.0.0.1:8080/healthz"]
      interval: 15s
      timeout: 3s
      start_period: 5s
      retries: 3

  go2rtc:
    image: alexxit/go2rtc:latest
    container_name: go2rtc
    profiles: ["video"]
    restart: unless-stopped
    ports:
      - "1984:1984" # go2rtc WebRTC stream server & REST API
      - "8554:8554" # RTSP stream proxy (optional)
      - "8555:8555/tcp" # WebRTC media signaling
      - "8555:8555/udp"
    devices:
      - /dev/video0:/dev/video0 # USB 3.0 UVC HDMI capture card (Chromecast ingest)
    volumes:
      - ./go2rtc.yaml:/config/go2rtc.yaml:ro

  cast-watcher:
    build: sidecars/cast-watcher
    image: ghcr.io/azylman/mirrormere-cast-watcher:latest
    container_name: cast-watcher
    profiles: ["video"]
    restart: unless-stopped
    expose:
      - "8090" # Internal container port for Core transport control (POST /action)
    environment:
      - CORE_URL=http://mirrormere-core:8080
      - CHROMECAST_IP=192.168.1.50 # Configurable target IP
      - PORT=8090
    depends_on:
      - mirrormere-core
```

---

## Profile B: Ambient E-Paper Kiosk (Mike & Amos)

### Bill of Materials (BOM)
- **Display**: Waveshare 7.5" V2 raw e-paper panel (800×480, SPI, Black & White).
  > **Critical Warning**: Must be strictly the Black/White V2 model. 3-color (Red/Yellow) panels require 15–30 seconds per full refresh and lack partial refresh support.
- **Driver Board**: Adafruit E-Ink Bonnet for Raspberry Pi (24-pin FPC). Pin map differs from the Waveshare HAT; see SPEC-009.
  - The all-in-one Waveshare 7.5" e-Paper HAT (V2) is a supported alternative using the driver's default pins.
- **Compute Unit**: Raspberry Pi 4 Model B (Mike's unit: 8GB RAM, 64-bit OS; minimum 2GB+ required to co-host Docker Compose and the display client). Drives the panel over SPI.
- **Power Supply**: Official Raspberry Pi 15W USB-C Power Supply (5.1V 3.0A).
- **Storage**: 32GB Class A1 MicroSD Card.
- **Sensors (optional)**: DHT22 temperature/humidity sensor for an indoor-climate widget.
- **Audio / Voice Array (optional)**: reSpeaker XVF3800 USB 4-Mic Array with case.
- **Mounting**: None for now; M2.5 nylon standoffs secure the Bonnet to the Pi.

### Physical Architecture
- The Adafruit E-Ink Bonnet stacks onto the Pi's 40-pin GPIO header, held by M2.5 standoffs.
- The panel's 24-pin ribbon connects to the Bonnet's FPC connector.
- Rendering and daemon co-location: Mike's Pi 4B hosts both the Docker Compose stack (`mirrormere-core` and `eink-renderer`) and the display client (`clients/eink-node`) on a single machine. The client fetches pre-rendered 1-bit monochrome images locally over loopback (`http://localhost:8081/eink.png`) and pushes them to SPI. (A decoupled multi-host topology—where Compose runs on an existing home server or NAS and the display Pi only runs `eink-node`—remains a supported deployment option, but Profile B's reference setup runs entirely on the single Pi 4B).

### Operating System & Runtime Environment
- **Base OS**: Raspberry Pi OS Lite (64-bit, headless, no X11/Wayland).
- **Hardware Interface**: SPI enabled via `/boot/firmware/config.txt` (`dtparam=spi=on`).
- **Rendering Pipeline**: Rendered via the `eink-renderer` Chromium sidecar. The `clients/eink-node` daemon (SPEC-009) fetches the 1-bit PNG and pushes it to the panel via `spidev`.
- **Refresh Strategy**:
  - Full refresh every 60 minutes to clear accumulated ghosting.
  - Partial refreshes on state change (e.g. new calendar events, upstream chore updates), screen rotation, or periodic clock refresh timer (`clock_refresh_seconds`, see SPEC-009).
  - Strict zero-animation rule: UI redrawing is purely event- or timer-driven.

### Reference Deployment Topology & Manifest (`deploy/compose.yml`)
In Profile B, the reference setup is a single Raspberry Pi 4 Model B (2 GB+ RAM, 64-bit OS) running Docker Compose (`docker compose --profile eink up -d`) to host `mirrormere-core` (port 8080) and `eink-renderer` (port 8081). Alongside Compose, `clients/eink-node` (SPEC-009) runs as a native systemd service on the same machine, listening for SSE events on `http://localhost:8080/api/events` and fetching rendered frames from `http://localhost:8081/eink.png` to drive the e-paper panel over SPI. A separate-server topology remains supported for low-power or decoupled deployments where the display Pi connects over the LAN:

```yaml
# deploy/compose.yml (Activated with: docker compose --profile eink up -d)
services:
  mirrormere-core:
    build: .
    image: ghcr.io/azylman/mirrormere:latest
    container_name: mirrormere-core
    restart: unless-stopped
    ports:
      - "8080:8080" # Core Daemon: REST API, SSE (/api/events), web UI
    environment:
      - PORT=8080
      - HOST=0.0.0.0
      - TZ=America/Los_Angeles
    volumes:
      - ./config.yaml:/config/config.yaml:ro
      - ./custom.css:/config/custom.css:ro # e.g. from deploy/examples/eink.css
      - ./data:/data
    healthcheck:
      test: ["CMD", "wget", "--no-verbose", "--tries=1", "--spider", "http://127.0.0.1:8080/healthz"]
      interval: 15s
      timeout: 3s
      start_period: 5s
      retries: 3

  eink-renderer:
    build: sidecars/eink-renderer
    image: ghcr.io/azylman/mirrormere-eink-renderer:latest
    container_name: eink-renderer
    profiles: ["eink"]
    restart: unless-stopped
    ports:
      - "8081:8081" # E-Ink PNG Renderer: headless 800x480 1-bit dithered rasterizer
    environment:
      - DAEMON_URL=http://mirrormere-core:8080
      - PORT=8081
    depends_on:
      - mirrormere-core
```

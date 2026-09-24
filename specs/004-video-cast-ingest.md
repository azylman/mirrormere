# SPEC-004: Video & Google Cast Ingestion Pipeline

## Status
Approved / Day 1 Requirement (Profile A)

## Context & Motivation
Commercial smart displays (like Google Nest Hub) allow users to cast YouTube, Spotify, or mobile screens seamlessly. However, replicating Google Cast in a self-hosted Linux kiosk using open-source software receivers is notoriously unreliable:
- Cloud-negotiated Widevine DRM blocks protected video playback.
- Open-source Cast protocol emulators frequently break due to closed Google protocol updates.
- Software decoding imposes heavy CPU/GPU loads on small mini PCs.

Mirrormere solves this cleanly by adopting a **Hardware-Assisted Ingestion Architecture**: plugging a physical Google Chromecast into a low-latency USB 3.0 HDMI capture card.

---

## Hardware Ingestion Pipeline

```mermaid
flowchart LR
    Phone[Mobile Phone / Laptop] -->|Google Cast / AirPlay| CC[Physical Google Chromecast]
    CC -->|HDMI 1080p60 Video & Audio| Cap[USB 3.0 HDMI Capture Dongle]
    Cap -->|USB 3.0 UVC & UAC| MiniPC[Beelink Mini S12 Pro]

    subgraph Linux Kernel
        MiniPC --> V4L2[/dev/video0 - V4L2 Video]
        MiniPC --> ALSA[/dev/snd/pcmC* - ALSA Audio]
    end

    V4L2 -->|getUserMedia Stream| PWA[Chromium Kiosk PWA]
    ALSA -->|Web Audio API| PWA
    PWA -->|Hardware Rendered Video| Monitor[UPERFECT 15.6 Inch Touch Display]
```

### Key Technical Advantages
1. **Zero DRM Friction**: The Chromecast performs all HDCP handshakes directly with the HDMI capture chip. Commercial streaming services (YouTube, Netflix, Disney+, Spotify) work natively without software workarounds.
2. **Standard Linux Drivers**: The capture card adheres to USB Video Class (UVC) and USB Audio Class (UAC) specifications. It requires zero custom kernel modules or proprietary drivers.
3. **Hardware Acceleration**: Chromium ingests the UVC device as a standard web camera, offloading frame rendering directly to the Intel N100's Intel UHD graphics via VA-API / WebGL.

---

## Web Platform Ingestion (`getUserMedia`)

The Chromium PWA accesses the live video feed directly through standard HTML5 Media Capture APIs:

```javascript
// Request video stream from the specific USB capture card
async function startCastIngestion() {
  const devices = await navigator.mediaDevices.enumerateDevices();
  const captureDevice = devices.find(d => 
    d.kind === 'videoinput' && d.label.toLowerCase().includes('cam') || 
    d.label.toLowerCase().includes('video')
  );

  const constraints = {
    video: {
      deviceId: captureDevice ? { exact: captureDevice.deviceId } : undefined,
      width: { ideal: 1920 },
      height: { ideal: 1080 },
      frameRate: { ideal: 60 }
    },
    audio: false // Handled via PipeWire / ALSA system audio routing
  };

  const stream = await navigator.mediaDevices.getUserMedia(constraints);
  const videoElement = document.getElementById('cast-stream');
  videoElement.srcObject = stream;
  videoElement.play();
}
```

---

## Signal Detection & Display Layout Modes

To provide an appliance-grade user experience, the system monitors video stream activity:

### 1. Active Signal Detection
- A lightweight background daemon monitors the V4L2 device status via `v4l2-ctl --get-dv-timings` or periodic frame luminance analysis.
- When an active video stream is detected (i.e. a user begins casting):
  - Emits a `cast:active` event across the local WebSocket bus.
  - Automatically wakes the display from DPMS low-power mode if sleeping.

### 2. Layout Modes
The Touch Kiosk UI supports three distinct rendering layouts:

- **Split-Screen Mode (Default on Cast)**:
  - Video stream occupies 60% of the screen (left or center).
  - Agenda, chore checklist, and family weather widgets remain pinned on the remaining 40%.
- **Picture-in-Picture (PIP) Mode**:
  - The video feed floats in a movable, resizable viewport in the lower-right corner while the full calendar layout is visible.
- **Full-Screen Cinema Mode**:
  - The video feed expands to 100% of the 15.6" display.
  - Tapping anywhere on the screen displays a subtle touch HUD overlay with volume controls, return-to-calendar button, and quick smart home toggles.

### 3. Smart Home HUD Overlays
Because the video is rendered inside standard HTML/CSS DOM:
- Home Assistant doorbell camera feeds (WebRTC/HLS) dynamically pop up as a floating Picture-in-Picture card directly over the playing video when the doorbell rings.
- Cooking timers and smart home security alerts float as high-contrast HUD badges without pausing or interrupting video playback.

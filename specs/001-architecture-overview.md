# SPEC-001: Architecture Overview

## Status
Approved / Foundational

## Context & Motivation
Commercial family wall calendars (e.g. Skylight) and smart displays (e.g. Google Nest Hub, Amazon Echo Show) suffer from severe limitations:
- Cloud subscription paywalls for basic features like video playback, photo storage, or chore tracking.
- Closed operating systems vulnerable to deprecation and abandonware.
- Lack of local Home Assistant integration, custom sensor overlays, or flexible hardware options.

Mirrormere solves this by establishing an open, headless smart display platform capable of powering both rich interactive touchscreens and ultra-low-power ambient e-paper panels from a single unified codebase.

---

## Scope & Phasing

- **Phase 1 (v1 MVP Core Pillars)**:
  1. Multi-Calendar Ingestion (iCal / CalDAV / Google Calendar - SPEC-007)
  2. Weather Forecasts & Live Conditions (Open-Meteo - SPEC-007)
  3. Household Lists, Chores & Tasks (Local SQLite / Google Tasks - SPEC-008)
  4. Google Photos Shared Album Slideshows (SPEC-007)
  5. Unified Video Stream API, Priority Stack & Cast Sidecar (SPEC-004)
  6. 6×2 Grid Layouts with Auto-Packed Bin-Packing Engine (SPEC-005)
  7. Client Ecosystem: Ambient E-Ink Node (SPEC-009) & Touch Kiosk (SPEC-010)

- **Phase 2 (Post-MVP Roadmap)**:
  1. Home Assistant Core entity state streaming (climate, lights, locks, sensors)
  2. Smart home quick-action widget controls
  3. Native Home Assistant doorbell push integration (complementing Phase 1 generic Webhook API)
  4. Voice assistant satellite pipeline (`wyoming-satellite`, or the adapter pipeline in SPEC-011)

---

## System Topology

```mermaid
graph TD
    subgraph Data Sources
        GCal[Google Calendar / CalDAV]
        Weather[Open-Meteo API]
        Photos[Google Photos Shared Album]
        Chores[Local Task Storage / SQLite]
        CC[Chromecast via HDMI]
        Doorbell[Doorbell / Security Camera]
        HA[Home Assistant Core - Phase 2]
    end

    subgraph Mirrormere Core Daemon
        Engine[Sync & Ingestion Engine]
        PubSub[SSE Event Stream Bus]
        WidgetEngine[Widget Engine & 6x2 Solver]
        VideoStack[Video Priority Stack]
    end

    subgraph Client Tier: Touch Kiosk (Intel N100)
        Cage[cage Wayland Compositor]
        Chromium[Chromium Kiosk Browser]
        CastSidecar[clients/cast-sidecar go2rtc]
        Cage --> Chromium
    end

    subgraph Client Tier: Ambient E-Ink (Raspberry Pi 3B+ / 4B)
        Sidecar[mirrormere-eink-renderer Sidecar]
        Node[clients/eink-node Python Daemon]
        Driver[Waveshare SPI Driver]
        Sidecar -->|GET /eink.png| Node
        Node --> Driver
    end

    GCal --> Engine
    Weather --> Engine
    Photos --> Engine
    Chores --> Engine
    HA -.->|Phase 2| Engine
    Doorbell -->|POST /api/video/trigger| Engine

    Engine --> WidgetEngine
    WidgetEngine --> PubSub
    Engine --> VideoStack
    VideoStack --> PubSub

    PubSub -->|SSE events| Chromium
    PubSub -->|SSE events| Node
    CC -->|HDMI to USB UVC| CastSidecar
    CastSidecar -->|POST /api/video/trigger| Engine
    CastSidecar -->|WebRTC Stream| Chromium
    Doorbell -.->|WebRTC / HLS Stream| Chromium
```

---

## Architectural Pillars

### 1. Headless Data Engine
The core service is a lightweight daemon written in Go running locally on the home network (or directly on the display host). It is responsible for:
- Synchronizing external calendars (Google Calendar iCal/CalDAV feeds, local iCal - SPEC-007).
- Maintaining household chore, grocery, and to-do lists in a local pure-Go SQLite database (`modernc.org/sqlite`, zero CGO) (SPEC-008).
- Ingesting Open-Meteo weather forecasts and Google Photos shared albums (SPEC-007).
- Ingesting physical Google Cast HDMI video via USB 3.0 UVC capture (SPEC-004).
- Executing the exact 6×2 bin-packing layout engine to automatically pack widgets into minimal screens (SPEC-005).
- Exposing a local REST API and Server-Sent Events (SSE) state stream for client displays (SPEC-006).
- Home Assistant entity streaming and camera interrupts (deferred to Phase 2).

### 2. Client Display Tiers
Mirrormere targets two distinct display classes:

- **Tier 1: Touch-Interactive Kiosk (60Hz)**
  - Targeted at Intel N100 hardware driving 1080p capacitive touch monitors (SPEC-002, SPEC-010).
  - Runs Chromium under Wayland `cage` with hardware-accelerated WebGL/CSS.
  - Supports live touch gestures, smooth transitions, real-time status pulses, and video capture overlays.
  - Zero on-screen keyboard (OSK) overhead; item editing is companion/phone-first.
  - DPMS sleep lifecycle with night window and daytime tap-to-wake.

- **Tier 2: Ambient E-Paper Kiosk (0.01Hz)**
  - Targeted at Raspberry Pi hardware driving 7.5" e-Paper SPI HATs/Bonnets (SPEC-002, SPEC-009).
  - Operates completely headlessly without running a local browser on the display node.
  - Subscribes to backend SSE change events with 5s coalescing and 60s minimum refresh floors.
  - Renders 1-bit selectively dithered PNGs via the server-side `mirrormere-eink-renderer` sidecar (SPEC-003, SPEC-009).

### 3. Decoupled Presentation Layer
The Go backend has zero awareness of how pixels are drawn. The server renders semantic HTML layouts (`views/widget.html`), injected with deployment-time volume-mounted stylesheets (`/config/custom.css`), allowing the exact same underlying calendar or task model to be presented on interactive touch kiosks or captured for static monochrome e-paper.

---

## Standardized Port Allocations & Network Topology

To maintain predictable local network service discovery, simplify firewall configuration, and prevent port collisions across sidecars, Mirrormere standardizes host port allocations across all deployment topologies:

| Port | Service | Protocol | Description | Profile |
|---|---|---|---|---|
| **8080** | `mirrormere-core` | HTTP / SSE | Core Daemon: REST API, SSE event stream (`/api/events`), healthcheck (`/healthz`), and web UI | Profile A & Profile B |
| **8081** | `mirrormere-eink-renderer` | HTTP | E-Ink PNG Renderer: Headless browser rasterizer generating 800×480 1-bit dithered image (`GET /eink.png`) | Profile B |
| **1984** | `mirrormere-cast` (`go2rtc`) | HTTP / WebRTC | Video Stream Ingest: UVC capture ingest, WebRTC streaming, and RTSP stream proxy | Profile A |
| **9000** | `mirrormere-voice` | HTTP / WebSocket | Voice Hub Sidecar: Local audio ingest, wake word, Whisper STT, and ElevenLabs/Piper TTS coordinator | Phase 2 (Optional) |

See SPEC-002 for authoritative reference `docker-compose.yml` manifests for both Profile A and Profile B.


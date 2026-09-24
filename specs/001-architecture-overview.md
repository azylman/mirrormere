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

## System Topology

```mermaid
graph TD
    subgraph Data Sources
        GCal[Google Calendar / CalDAV]
        Weather[Open-Meteo API]
        HA[Home Assistant Core]
        Chores[Local Task Storage]
        CC[Chromecast via HDMI]
    end

    subgraph Mirrormere Core Daemon
        Engine[Sync & Ingestion Engine]
        PubSub[WebSocket & Event Bus]
        WidgetEngine[Widget Plugin Manager]
    end

    subgraph Client Tier: Touch Kiosk (Intel N100)
        Chromium[Chromium Kiosk Browser]
        PWA[Touch PWA & Web Components]
        V4L2[UVC Video /dev/video0]
    end

    subgraph Client Tier: Ambient E-Ink (Raspberry Pi 4B)
        Cron[Timer & Event Listener]
        Renderer[SVG / Pillow Static Renderer]
        Driver[Waveshare SPI Driver]
    end

    GCal --> Engine
    Weather --> Engine
    HA --> Engine
    Chores --> Engine

    Engine --> WidgetEngine
    WidgetEngine --> PubSub

    PubSub -->|WebSocket / State JSON| PWA
    CC -->|HDMI to USB UVC| V4L2
    V4L2 -->|getUserMedia HTML5| Chromium

    PubSub -->|REST / Webhook Trigger| Cron
    Cron --> Renderer
    Renderer --> Driver
```

---

## Architectural Pillars

### 1. Headless Data Engine
The core service is a lightweight daemon (written in Go or Python/Node) running locally on the home network (or directly on the display host). It is responsible for:
- Synchronizing external calendars (Google Calendar OAuth, CalDAV, local iCal).
- Maintaining household chore and meal lists in a local SQLite database.
- Subscribing to Home Assistant WebSocket streams for live entity states and camera events.
- Exposing a local REST API and WebSocket event feed for client displays.

### 2. Client Display Tiers
Mirrormere targets two distinct display classes:

- **Tier 1: Touch-Interactive Kiosk (60Hz)**
  - Targeted at Intel N100 hardware driving 1080p/4K capacitive touch monitors.
  - Runs a Progressive Web App (PWA) under Wayland with hardware-accelerated WebGL/CSS.
  - Supports live touch gestures, smooth transitions, real-time status pulses, and video capture overlays.

- **Tier 2: Ambient E-Paper Kiosk (0.01Hz)**
  - Targeted at Raspberry Pi 4B hardware driving 7.5" e-Paper SPI HATs.
  - Operates completely headlessly without running a heavy browser.
  - Subscribes to backend change events or runs on a scheduled cron (e.g. 5–15 minute cadence), rendering static high-contrast monochrome layouts directly to the frame buffer.

### 3. Decoupled Presentation Layer
The backend has zero awareness of how pixels are drawn. Each widget package defines separate view adapters, allowing the same underlying calendar or task model to be rendered as an interactive HTML5 component or a static monochrome bitmap.

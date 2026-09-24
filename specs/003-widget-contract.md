# SPEC-003: Pluggable Widget Contract & Extension Model

## Status
Approved / Architecture Defined

## Overview
Mirrormere uses a pluggable, declarative widget model. A widget is a self-contained unit responsible for data ingestion and presentation. To support both 60Hz capacitive touchscreens and low-refresh e-paper panels, widgets decouple the data pipeline from the physical display adapter while utilizing a unified semantic HTML template styled per deployment.

---

## Directory Structure

A widget package lives in `widgets/<widget-id>/` (for standard core widgets) or `custom_widgets/<widget-id>/` (for user-specific extensions):

```
widgets/calendar-agenda/
├── manifest.yaml           # Widget metadata, configuration schema, and capabilities
├── provider.go             # Backend data fetcher and event publisher
├── views/
│   └── widget.html         # Unified semantic HTML layout for all display profiles
└── assets/                 # Optional static icons or assets
```

---

## Manifest Schema (`manifest.yaml`)

Each widget declares its identity, required capabilities, and configuration parameters:

```yaml
id: calendar-agenda
name: Family Calendar Agenda
version: "1.0.0"
description: Multi-calendar agenda view supporting Google Calendar, CalDAV, and iCal feeds.

capabilities:
  - ambient-static
  - touch-interactive

refresh:
  interval_seconds: 300
  push_triggers:
    - calendar_event_changed
    - midnight_rollover

config_schema:
  calendars:
    type: list
    required: true
    items:
      name: string
      url: string
      color: string
  show_relative_time:
    type: boolean
    default: true
```

### Capability Tags
- `ambient-static`: Supports rendering to static monochrome or grayscale displays (e-paper).
- `touch-interactive`: Supports full touch interaction, tapping to view event details, gestures, and animations.
- `video-capture`: Involves live video streaming or UVC ingestion (automatically ignored by e-paper targets).
- `audio-reactive`: Utilizes microphone input or voice triggers.

---

## The Data Provider Contract

The backend data provider runs in the Mirrormere Core service:
1. **Lifecycle**:
   - `Init(config)`: Validates credentials, sets up sync loops or WebSocket listeners.
   - `Fetch()`: Returns an atomic JSON payload representing current state.
   - `Subscribe(eventSink)`: Emits real-time event updates when data changes.
   - `Shutdown()`: Cleans up background listeners and connections.

2. **Standard Payload Envelope**:
   ```json
   {
     "widget_id": "calendar-agenda",
     "timestamp": "2026-09-24T18:00:00Z",
     "state": "healthy",
     "data": {
       "events": [
         {
           "id": "evt_101",
           "title": "Soccer Practice",
           "start": "2026-09-24T18:30:00Z",
           "end": "2026-09-24T19:30:00Z",
           "calendar": "Family",
           "color": "#3b82f6"
         }
       ]
     }
   }
   ```

---

## Unified Semantic HTML & Server-Wide Styling

Mirrormere eliminates the authoring and maintenance overhead of dual templates (`touch.html` vs. `eink.svg`). Every widget authors **one single semantic HTML template** (`views/widget.html`).

### 1. View Template (`views/widget.html`)
- Delivered as a clean semantic HTML5 snippet or modern Web Component loaded into the canvas.
- Markup uses clean semantic classes (e.g. `.widget`, `.widget-title`, `.item-done`) and standard CSS variables for styling.
- Layout flexes and responds dynamically to its assigned grid cells per SPEC-005.
- Receives state updates reactively via Server-Sent Events (`widget.update` per SPEC-006).

### 2. Server-Wide Volume-Mounted Stylesheet (`/config/custom.css`)
In alignment with Mirrormere's independent deployment topology and zero-runtime-multiplexing invariant:
- **Zero Config Keys**: There are no `theme:`, `profile:`, or `css:` selectors in `config.yaml`.
- The server serves a single unified stylesheet at `/style.css`.
- At startup, the server inspects `/config/custom.css` on disk:
  - **Mounted File Present**: Serves the volume-mounted file directly.
  - **Unmounted / Absent**: Serves the default embedded stylesheet (`embed.FS`).
- Each household injects its display styling purely via Docker volume mounts:
  - **Alex's Touch Kiosk**: `-v ./kiosk.css:/config/custom.css:ro` (dark mode cyberpunk palette, 48px touch targets, glowing accents).
  - **Mike's Ambient E-Ink**: `-v ./eink.css:/config/custom.css:ro` (high-contrast 1-bit monochrome, bold typography, zero animations, 2px solid borders).

### 3. Decoupled E-Ink Headless Capture Sidecar
To keep the core Go daemon container lightweight, hermetic, and minimal (< 25MB static binary with `CGO_ENABLED=0`):
- The core Go server **never bundles Chromium or headless browser dependencies**.
- The core Go server exposes the full-screen layout at `GET /display` (HTML).
- Ambient e-ink displays are serviced by an **optional headless capture sidecar** (`mirrormere-eink-renderer`).
- The sidecar loads the page, captures the 800×480 viewport, applies 1-bit Floyd-Steinberg dithering / quantization, and exposes `GET /eink.png` for display nodes to fetch.

---

## Core vs. Private Extensions

Mirrormere enforces a strict separation between core public widgets and private user extensions:

1. **Standard Core Library (`widgets/`)**:
   - 100% generic, reusable, and free of personal identifiers or proprietary hardware dependencies.
   - Includes: Google Calendar / CalDAV, Open-Meteo Weather, Daily Chores & Todo, Clock & World Time, Family Photo Carousel.

2. **Private User Extensions (`custom_widgets/`)**:
   - User-defined integrations that reference personal home configurations or specific hardware peripherals.
   - Includes: Home Assistant entity dashboards, live RTSP doorbell camera feeds, and Chromecast UVC ingest pipelines.

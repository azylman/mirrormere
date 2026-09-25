# SPEC-003: Pluggable Widget Contract & Extension Model

## Status
Approved / Architecture Defined

## Overview
Mirrormere uses a pluggable, declarative widget model. A widget is a self-contained unit responsible for data ingestion and presentation. To support both 60Hz capacitive touchscreens and low-refresh e-paper panels, widgets decouple the data pipeline from the physical display adapter while utilizing a unified semantic HTML template styled per deployment.

---

## Directory Structure

A widget package lives in `widgets/<widget-id>/` (for standard core widgets compiled into the binary) or in a mounted configuration directory (e.g. `/config/widgets/<widget-id>/` or `custom_widgets/<widget-id>/` for custom user extensions):

```
widgets/calendar-agenda/
├── manifest.yaml           # Widget metadata, configuration schema, and capabilities
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

## The Data Provider Contract & Architecture

Mirrormere compiles into a static, zero-CGO Go binary (`CGO_ENABLED=0`). Because Go cannot dynamically load shared libraries (`.so` plugins via `plugin.Open`) into static binaries, Mirrormere does not support dynamic runtime Go plugins. Instead, data providers follow one of two clean extension models:

### 1. In-Process Compiled Providers (Core Widgets & Custom Builds)
Core built-in widgets run directly in the Mirrormere Core service process:
1. **Lifecycle Interface**:
   - `Init(ctx context.Context, config map[string]any) error`: Validates credentials, sets up sync loops or WebSocket listeners.
   - `Fetch(ctx context.Context) (WidgetPayload, error)`: Returns an atomic JSON payload representing current state.
   - `Subscribe(ctx context.Context, eventSink chan<- WidgetPayload) error`: Emits real-time event updates when data changes.
   - `Shutdown(ctx context.Context) error`: Cleans up background listeners and connections.

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

### 2. Out-of-Process Generic HTTP Providers (Private Extensions & Sidecars)
For user-specific integrations, private household services, or extensions written in any language (Python, Node.js, Go), Mirrormere provides an out-of-process Generic HTTP Provider adapter (matching the HTTP provider pattern in SPEC-008):

1. **Manifest & Configuration**:
   A custom widget declares its HTTP provider endpoint in `manifest.yaml` or `config.yaml`:
   ```yaml
   widgets:
     - id: custom-sensor-hud
       provider: http
       http:
         endpoint: "http://sensor-sidecar:8090/data"
         poll_interval_seconds: 60
         token_env: SENSOR_HUD_TOKEN
   ```

2. **Wire Endpoints**:
   - `GET {endpoint}`: Returns the standard JSON payload envelope (`widget_id`, `timestamp`, `state`, `data`).
   - `POST {endpoint}/action`: Optional mutation handler. Forwards user actions from `POST /api/widgets/{widget_id}/action` (returns `200` OK, `202` Accepted, or `502` Bad Gateway per SPEC-006).

3. **Realtime Push / Webhook Support**:
   Sidecars and external services can push updates immediately into Mirrormere by issuing an authenticated webhook:
   - `POST /api/widgets/{widget_id}/push`
   - Headers: `Authorization: Bearer <shared_secret>`, `Content-Type: application/json`
   - Body: Standard payload envelope.
   - Triggers an immediate SSE broadcast (`widget.update` per SPEC-006) to active display nodes without waiting for the polling timer.

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
- The sidecar loads the page, captures the 800×480 viewport, applies selective 1-bit quantization and dithering (detailed below), and exposes `GET /eink.png` for display nodes to fetch.
- `GET /eink.png` renders on request (or returns a render newer than the last `widget.update`) and sends a strong `ETag` equal to a hash of the PNG bytes, so display nodes can skip unchanged frames (SPEC-009).

### 4. Selective Dithering Pipeline
Full-page error diffusion (e.g. applying naive Floyd-Steinberg dithering across the entire 800×480 viewport) degrades sharp text, numbers, and thin borders into fuzzy gray pixel speckles. To deliver crisp typography alongside high-quality continuous-tone graphics, `mirrormere-eink-renderer` employs a two-pass selective quantization pipeline:

1. **Strict 1-Bit Thresholding Default**:
   - Typography, digits, calendar grids, task checkboxes, clock glyphs, and UI borders are processed via a strict luminance threshold (luminance < 128 → black, ≥ 128 → white).
   - Guarantees razor-sharp vector edges and clean typography with zero dithering artifacts or salt-and-pepper noise.
2. **Selective Dithering on `.dither` Regions**:
   - Continuous-tone elements such as photographs (photo carousel) and grayscale weather illustrations declare the CSS class `.dither` in their template markup (`views/widget.html`).
   - The renderer queries the DOM for all `.dither` elements (`document.querySelectorAll('.dither')`) to extract their bounding client rectangles.
   - Only pixel regions within these bounding boxes are quantized via Floyd-Steinberg or Atkinson error-diffusion dithering.
3. **Buffer Composition & Output**:
   - The selectively dithered regions are composited onto the thresholded 1-bit canvas.
   - The resulting 800×480 1-bit monochrome image is encoded as a standard PNG (color type 0, bit depth 1) and served at `GET /eink.png`.

---

## Core vs. Private Extensions

Mirrormere enforces a strict separation between core public widgets and private user extensions:

1. **Standard Core Library (`widgets/`)**:
   - 100% generic, reusable, and free of personal identifiers or proprietary hardware dependencies.
   - Includes: Google Calendar / CalDAV, Open-Meteo Weather, Daily Chores & Todo, Clock & World Time, Family Photo Carousel.

2. **Private User Extensions (`custom_widgets/` or Sidecar Services)**:
   - User-defined integrations that reference personal home configurations or specific hardware peripherals.
   - Run out-of-process as generic HTTP provider sidecars (or custom containers) communicating via the HTTP/webhook contract, with semantic templates mounted into `/config/widgets/`.
   - Includes: Home Assistant entity dashboards, live RTSP doorbell camera feeds, and Chromecast UVC ingest pipelines.

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
      url: string       # optional public feed URL
      url_env: string   # environment variable name resolving private secret URL
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

## Declarative Widget Instance Configuration (`config.yaml`)

Mirrormere uses a generic, decoupled configuration model where **each widget instance declared in `display.widgets` is a self-contained unit managing both its data ingestion parameters and its UI presentation settings**. There is no disconnected top-level providers block; all sync cadences, data sources, and display parameters live directly in the widget's `config:` mapping.

### 1. Unified Widget Instance Declaration
In `config.yaml`, the `display.widgets` array defines the active widgets. Each entry decouples the instance identity (`id`) from its underlying widget type (`type`) and supplies a generic `config:` dictionary:

```yaml
display:
  widgets:
    - id: home-weather              # Unique instance identifier (kebab-case)
      type: weather-forecast        # Widget engine / template type
      dimensions: [2, 1]            # [width_cols, height_rows] on 6x2 grid
      pinned: false                 # Pinned across all rotation screens
      config:                       # Widget-specific parameters & sync settings
        refresh_interval_seconds: 900 # Ingestion sync loop cadence
        latitude: 37.8044
        longitude: -122.2712
        units: imperial             # "metric" | "imperial"
        hourly: false
        forecast_days: 5

    - id: office-weather            # Second independent weather instance!
      type: weather-forecast
      dimensions: [2, 1]
      config:
        refresh_interval_seconds: 900
        latitude: 37.7749
        longitude: -122.4194
        units: imperial
        hourly: false
        forecast_days: 5

    - id: family-calendar
      type: calendar-agenda
      dimensions: [4, 2]
      config:
        refresh_interval_seconds: 300 # Ingestion sync loop cadence
        view: week                  # Presentation: "day" | "week" | "month"
        days_ahead: 7
        max_events: 12
        show_relative_time: true
        calendars:
          - name: "Family Events"
            url_env: FAMILY_CALENDAR_URL
            color: "#a855f7"

    - id: living-room-photos
      type: photo-carousel
      dimensions: [3, 2]
      config:
        share_url: "https://photos.app.goo.gl/AbCdEf123456789"
        refresh_interval_seconds: 3600 # Album metadata sync cadence
        cycle_interval_seconds: 60     # Local client photo rotation interval
        shuffle: true

    - id: grocery-list
      type: tasks
      dimensions: [2, 1]
      config:
        list_id: "groceries"
        list_name: "Groceries"
        source: local
        show_completed: 3

    - id: layout-spacer
      type: spacer
      dimensions: [2, 1] # Built-in zero-data transparent tile to satisfy 12-cell bin-packing (SPEC-005)
```

### 2. Multi-Instance Capability
Separating `id` from `type` and attaching sync configuration directly to the instance enables multiple independent instances of the same widget type across screens with distinct upstream targets:
- Multiple weather locations: `id: home-weather` on Screen 1 and `id: office-weather` on Screen 2 with different GPS coordinates and polling rates.
- Multiple task lists: `id: daily-chores` on Screen 1 defines `list_id: chores` with `source: gtasks`, while `id: compact-chores` on Screen 2 references the same `list_id: chores` with `show_completed: 0`, sharing its background sync worker.
- Multiple photo streams: `id: family-photos` on Screen 1 and `id: art-gallery` on Screen 2 with distinct Google Photos share URLs.
- Multiple calendar views: a `[2, 1]` compact `day` view on Screen 1 and a `[4, 2]` expansive `week` view on Screen 2.

### 3. Lifecycle Binding & Background Ingestion in Go
The Go backend unmarshals the generic `config` mapping as `map[string]any`:
- When initializing each widget instance on server boot, the engine passes `w.Config` into `Init(ctx, w.Config)`.
- Core built-in widgets deserialize `w.Config` into strongly typed internal structs (e.g. `CalendarConfig`, `WeatherConfig`) using `mitchellh/mapstructure` or YAML decoder defaults.
- **Dedicated Sync Goroutine**: During `Init`, the widget instance starts its own background goroutine running on its configured `refresh_interval_seconds`. This worker runs for the lifetime of the Mirrormere daemon, keeping in-memory caches continuously fresh regardless of whether the widget is currently visible or rotated off-screen.
- **SSE Event Emission**: When the sync loop fetches updated data, it publishes a `widget.update` event over the SSE bus tagged with its unique instance ID (`widget_id: w.ID`).
- **Template Context Injection**: The `config` object is also injected into the semantic HTML template renderer context (`views/widget.html`), allowing templates to inspect presentation parameters directly (e.g. `{{ if eq .Config.view "month" }}...{{ end }}`).

---

## The Data Provider Contract & Architecture

Mirrormere compiles into a static, zero-CGO Go binary (`CGO_ENABLED=0`). Because Go cannot dynamically load shared libraries (`.so` plugins via `plugin.Open`) into static binaries, Mirrormere does not support dynamic runtime Go plugins. Instead, data providers follow one of two clean extension models:

### 1. In-Process Compiled Providers (Core Widgets & Custom Builds)
Core built-in widgets run directly in the Mirrormere Core service process (`calendar-agenda`, `weather-forecast`, `tasks`, `photo-carousel`, and `spacer` for layout padding per SPEC-005):
1. **Lifecycle Interface**:
   - `Init(ctx context.Context, config map[string]any) error`: Validates credentials, sets up sync loops or WebSocket listeners.
   - `Fetch(ctx context.Context) (WidgetPayload, error)`: Returns an atomic JSON payload representing current state.
   - `Subscribe(ctx context.Context, eventSink chan<- WidgetPayload) error`: Emits real-time event updates when data changes.
   - `Shutdown(ctx context.Context) error`: Cleans up background listeners and connections.

### 2. Standard Payload Envelope & Stale-While-Revalidate Policy
Every widget payload envelope published over the SSE bus (`widget.update` per SPEC-006) or returned via provider fetch conforms to the standard JSON structure:

```json
{
  "widget_id": "family-calendar",
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

- `widget_id` (string, required): Canonical instance identifier (kebab-case).
- `timestamp` (string, RFC 3339, required): Time the data snapshot was captured by the provider.
- `state` (string, required): Operational state: `"healthy"` | `"degraded"` | `"error"`.
- `data` (object, required): Strongly typed domain payload conforming to the widget type's schema.

#### Stale-While-Revalidate Degraded State Policy
Upstream network partitions, rate limits, and external service outages (e.g. Google Calendar CalDAV 500s, Open-Meteo downtime) must **never blank, crash, or unmount a widget cell** on the wall display. All widgets strictly implement a **stale-while-revalidate** policy:

1. **State Transitions**:
   - **`healthy`**: The upstream provider returned a valid response during its most recent sync cycle. `timestamp` reflects the fresh fetch time.
   - **`degraded` (Stale-While-Revalidate)**: The upstream provider experienced a fetch timeout, network partition, or non-200 error, BUT the daemon holds a Last-Known-Good (LKG) data snapshot in memory or SQLite cache.
     - The Go daemon **freezes and preserves the LKG data in `data`**.
     - `state` transitions to `"degraded"`.
     - `timestamp` retains the timestamp of the last successful data fetch, allowing clients to calculate data staleness.
     - The background worker continues retrying on its standard polling interval with exponential backoff (e.g. 30s, 60s, up to the regular interval).
   - **`error` (Cold Boot Failure)**: The widget has NO last-known-good data (e.g. cold-booting without internet connectivity and an empty cache).
     - `data` is empty or contains minimal fallback schema (e.g. empty lists).
     - `state` is set to `"error"`.

2. **Client Presentation Guidelines (Touch & E-Ink)**:
   - **Never Blank**: The client MUST NEVER replace a degraded widget with an empty container, crash boundary, or stack trace.
   - **Visual Stale Indicator**:
     - When `state == "degraded"`, the client dims widget content slightly (`opacity: 0.8`) and displays an unobtrusive, subtle stale badge in the widget corner (e.g. "Updated 2h ago" or a discreet warning dot).
     - When `state == "error"` on cold boot, the client renders an empty state illustration with an inline message (e.g. "Connecting to calendar...") rather than a blank box.
   - **Instant Re-Hydration**: As soon as the upstream provider recovers and a successful sync occurs, the Go daemon emits a `widget.update` with `state: "healthy"`, and the client automatically removes the stale badge and restores full visual brightness without requiring a page reload.

### 3. Out-of-Process Generic HTTP Providers (Private Extensions & Sidecars)
For user-specific integrations, private household services, or extensions written in any language (Python, Node.js, Go), Mirrormere provides an out-of-process Generic HTTP Provider adapter (matching the HTTP provider pattern in SPEC-008):

1. **Manifest & Configuration**:
   A custom widget instance is declared directly under `display.widgets` in `config.yaml` using `type: http`, supplying its layout dimensions and provider settings under `config:`:
   ```yaml
   display:
     widgets:
       - id: custom-sensor-hud
         type: http
         dimensions: [2, 1]
         config:
           endpoint: "http://sensor-sidecar:8095/data"
           refresh_interval_seconds: 60
           token_env: SENSOR_HUD_TOKEN
   ```

2. **Wire Endpoints**:
   - `GET {endpoint}`: Returns the standard JSON payload envelope (`widget_id`, `timestamp`, `state`, `data`).
   - `POST {endpoint}/action`: Optional mutation handler. Forwards user actions from `POST /api/widgets/{widget_id}/action` (returns `200` OK, `202` Accepted, or `502` Bad Gateway per SPEC-006).

3. **Realtime Push / Webhook Support**:
   Sidecars and external services can push updates immediately into Mirrormere by issuing an HTTP POST webhook:
   - `POST /api/widgets/{widget_id}/push`
   - Headers: `Content-Type: application/json`
   - Body: Standard payload envelope (`widget_id`, `timestamp`, `state`, `data`).
   - Auth: None (all inbound LAN calls are fully trusted per the local-network trust model).
   - Behavior: Updates the widget's in-memory and SQLite cache regardless of active screen. Push webhooks are strictly limited to sidecar and generic HTTP provider widgets; calling push on list-backed widgets (`type: tasks`) is rejected with `409 Conflict` (task lists are strictly read-only ambient displays ingested from configured upstream providers per SPEC-006 and SPEC-008). The daemon immediately broadcasts a `widget.update` SSE event across all open `/api/events` connections (per SPEC-006), ensuring all connected displays, companion nodes, and cached client stores update their local state immediately with zero rotation delay. See SPEC-006 for full request/response schemas, status codes (200/400/404/409), and validation rules.

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

### 3. Visual Styling Standards & Design Tokens
To achieve a refined, modern aesthetic suitable for high-visibility wall mounting, Mirrormere establishes consistent design tokens and layout conventions across all semantic widgets:

1. **Atmospheric Depth & Frosted Glass**:
   - Backgrounds adopt dark slate/charcoal foundations (`#1c1c1e` base, `#2c2c2e` surface).
   - Widget cards utilize subtle frosted glass (`backdrop-filter: blur(12px)`, background `rgba(255, 255, 255, 0.05)`, border `1px solid rgba(255, 255, 255, 0.08)`, border-radius `16px`).
   - Clean high-contrast typography hierarchy (`#ffffff` primary text, `#d3d3d3` secondary, `#8e8e93` muted).

2. **Ambient Sensor Pill Badges (`.pill-badge`)**:
   - Secondary status readouts (temperature, humidity, active light counts, open doors, sync state) are encapsulated in compact pill capsules (`height: 24px–28px`, `border-radius: 9999px`, padding `2px 10px`).
   - Pairs micro-icons with clean tabular numbers, preserving glanceability on 1080p touch screens while thresholding cleanly to crisp 1-bit vector shapes on e-paper.

3. **Chunky Inset Pill Sliders (`.slider-pill`)**:
   - Volume, brightness, and range controls reject default browser `<input type="range">` elements in favor of tactile inset pill tracks with minimum 48px hit areas for effortless finger targeting.

4. **Clean Photo Canvas Invariant**:
   - Family photography (`photo-carousel` widget) is rendered edge-to-edge with zero floating text overlays, clocks, or weather badges. Ambient glance data is strictly partitioned into the fixed header zone to keep photos clean.

### 4. Decoupled E-Ink Headless Capture Sidecar
To keep the core Go daemon container lightweight, hermetic, and minimal (< 25MB static binary with `CGO_ENABLED=0`):
- The core Go server **never bundles Chromium or headless browser dependencies**.
- The core Go server exposes the full-screen layout at `GET /display` (HTML).
- Ambient e-ink displays are serviced by an **optional headless capture sidecar** (`mirrormere-eink-renderer`).
- The sidecar loads the page, captures the 800×480 viewport, applies selective 1-bit quantization and dithering (detailed below), and exposes `GET /eink.png` for display nodes to fetch.
- `GET /eink.png` renders on request (or returns a render newer than the last `widget.update`) and sends a strong `ETag` equal to a hash of the PNG bytes, so display nodes can skip unchanged frames (SPEC-009).

### 5. Selective Dithering Pipeline
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
   - Core widget types: calendar agenda (`calendar-agenda`), weather forecast (`weather-forecast`), tasks & chores (`tasks`), photo carousel (`photo-carousel`), and grid spacer (`spacer`). Fixed header zones independently render the clock and ambient status.

2. **Private User Extensions (`custom_widgets/` or Sidecar Services)**:
   - User-defined integrations that reference personal home configurations or specific hardware peripherals.
   - Run out-of-process as generic HTTP provider sidecars (or custom containers) communicating via the HTTP/webhook contract, with semantic templates mounted into `/config/widgets/`.
   - Includes: Home Assistant entity dashboards, live RTSP doorbell camera feeds, and Chromecast UVC ingest pipelines.

# SPEC-003: Pluggable Widget Contract & Extension Model

## Status
Approved / Architecture Defined

## Overview
Mirrormere uses a pluggable, declarative widget model. A widget is a self-contained package responsible for data ingestion and presentation. To support both 60Hz capacitive touchscreens and low-refresh e-paper panels, widgets decouple the data pipeline from the physical display adapter while utilizing a unified semantic HTML template styled per deployment.

To ensure frictionless development and rapid iteration, Mirrormere enforces a **filesystem-first dependency model with zero embedded static assets**: the Go binary compiles purely as an ingestion and layout engine, while all widget templates, manifests, and assets are served and parsed directly from disk.

---

## Directory Structure & Deterministic Dual Paths

A widget package lives in a directory named strictly after its widget **type** (`<widget-type>`), completely decoupling the package definition from runtime instance identifiers (`id`):

```
widgets/<widget-type>/
├── manifest.yaml           # Widget metadata, configuration schema, provider binding, and capabilities
├── views/
│   └── widget.html         # Unified semantic HTML layout for all display profiles
└── assets/                 # Optional static icons or assets
```

### Deterministic Dual Paths & Invariant
To eliminate environment variable bloat and prevent volume mount conflicts, the Go daemon always resolves widget packages from two fixed, deterministic directory paths with zero runtime flag or ENV sprawl:

1. **Core Built-In Widgets (`/app/widgets/<widget-type>/`)**:
   - Shipped directly within the container image (source repository path: `widgets/<widget-type>/`).
   - Contains core standard widgets (`calendar-agenda`, `weather-forecast`, `tasks`, `photo-carousel`, `spacer`).
2. **Custom User Extensions (`/config/widgets/<widget-type>/`)**:
   - Reserved exclusively for user volume mounts (e.g. `-v ./custom_widgets:/config/widgets:ro`).
   - Houses household-specific widgets, Home Assistant sensor tiles, and custom sidecar wrappers.

### Clean `/config/` Territory Rule & Precedence Chain
All user-provided configuration, styles, and custom extensions reside strictly under `/config/`:
- `/config/config.yaml` → Household layout, screens, and widget instances
- `/config/custom.css` → Household theme and display styling
- `/config/widgets/<widget-type>/` → Household custom templates and manifests

While all core application files live under `/app/` (`/app/web`, `/app/widgets`). A household can mount their entire private configuration directory directly to `/config` without clobbering or hiding built-in widgets.

**Package-Level Resolution Precedence & Whole-Package Override Rule**: When the display engine resolves a widget package for `type: <widget-type>`, it evaluates candidate directories at the whole-package level:
1. `/config/widgets/<widget-type>/` (Custom User Package override)
2. `/app/widgets/<widget-type>/` (Core Built-In Package)

If `/config/widgets/<widget-type>/` exists, it is selected as the authoritative package, and `/app/widgets/<widget-type>/` is ignored entirely. There is **zero partial inheritance, layering, or file-by-file fallback** between `/config` and `/app`:
- **Complete Package Requirement**: An override package in `/config/widgets/<widget-type>/` must be fully self-contained, supplying both its own `manifest.yaml` and `views/widget.html`. The `assets/` directory remains optional within an override package (matching built-in packages).
- **Startup Validation Error**: If `/config/widgets/<widget-type>/` exists but lacks either required file, the Go daemon aborts startup with a fatal validation error:
  ```text
  [widget.loader] fatal: widget package at /config/widgets/<widget-type>/ is incomplete: missing required file manifest.yaml (or views/widget.html); overriding a widget type requires a complete package
  ```
  The daemon never falls back to `/app/widgets/<widget-type>/` for missing files within an overridden package directory. This enables households to introduce brand-new custom widgets OR completely replace built-in widget implementations cleanly without leaking underlying assets or schemas.

### Static Asset Serving & URL Contract (`assets/`)
Widget packages can optionally provide static icons, images, or assets within an `assets/` subdirectory (e.g., `widgets/<widget-type>/assets/icon.svg`).

1. **HTTP Asset Route (`GET /widgets/{type}/assets/{path}`)**:
   The Go daemon routes static assets via:
   `GET /widgets/{type}/assets/{path}`
   - **Resolution Source**: Static assets are served strictly from the resolved widget package directory per the whole-package override rule. If `/config/widgets/{type}/` exists, assets are served from `/config/widgets/{type}/assets/{path}`. Otherwise, they are served from `/app/widgets/{type}/assets/{path}`.
   - **Path Traversal Protection**: The HTTP handler sanitizes `{path}` using `filepath.Clean`. Any attempts at path traversal (such as `..`, null bytes, or paths resolving outside the resolved package's `assets/` directory) are rejected with `400 Bad Request` or `404 Not Found`.
   - **MIME Types**: Assets are served with standard `Content-Type` headers inferred from the file extension (e.g. `image/svg+xml`, `image/png`, `image/webp`).
   - **Status Codes**:
     - `200 OK`: File exists and is served with appropriate MIME type.
     - `404 Not Found`: Asset file does not exist, or the resolved package omits the `assets/` directory.
     - `400 Bad Request`: Malformed path or directory traversal attempt.
   - **API Schema**: This route is formally defined in `api/openapi.yaml`.

2. **Template Context Integration**:
   Templates must never hard-code asset path prefixes. The server injects an `.Assets` base URL variable (evaluating to `/widgets/<widget-type>/assets`) into the template execution context:
   ```html
   <img src="{{ .Assets }}/weather-icon.svg" alt="Weather condition" class="widget-icon" />
   ```

---

## Manifest Schema (`manifest.yaml`)

Each widget declares its identity, required capabilities, provider binding, default dimensions, and configuration parameters:

```yaml
name: Family Calendar Agenda
version: "1.0.0"
description: Multi-calendar agenda view supporting Google Calendar, CalDAV, and iCal feeds.
provider: calendar-agenda   # Built-in driver name or "http" for custom sidecar extensions

capabilities:
  - ambient-static
  - touch-interactive

default_dimensions: [4, 2]  # Suggested default size on the 6x2 grid

refresh:
  interval_seconds: 300
  push_triggers:
    - calendar_event_changed
    - midnight_rollover

config_schema:
  type: object
  required:
    - calendars
  properties:
    calendars:
      type: array
      items:
        type: object
        required:
          - name
          - color
        properties:
          name:
            type: string
          url:
            type: string       # optional public feed URL
          url_env:
            type: string       # environment variable name resolving private secret URL
          color:
            type: string
    show_relative_time:
      type: boolean
      default: true

response_schema:
  type: object
  required:
    - events
  properties:
    events:
      type: array
      items:
        type: object
        required:
          - id
          - title
          - start
          - end
          - calendar
          - color
        properties:
          id:
            type: string
          title:
            type: string
          start:
            type: string
          end:
            type: string
          calendar:
            type: string
          color:
            type: string
```

### Manifest Functional Roles
1. **Hardware Profile Filtering (`capabilities`)**: Tells the 6×2 layout solver which display tiers can render the widget:
   - `ambient-static`: Supports rendering to static monochrome or grayscale displays (e-paper).
   - `touch-interactive`: Supports full touch interaction, tapping to view event details, gestures, and animations.
   - `video-capture`: Involves live video streaming or UVC ingestion (automatically ignored by e-paper targets).
   - `audio-reactive`: Utilizes microphone input or voice triggers.
2. **Data Provider Routing (`provider`)**: Identifies how data is ingested. Built-in widgets point to compiled Go fetchers (`calendar-agenda`, `weather-forecast`, `tasks`, `photo-carousel`, `spacer`), while custom extensions declare `provider: http` to invoke the generic HTTP sidecar client.
3. **Default Cadence Fallback (`refresh`)**: If an instance in `config.yaml` omits `refresh_interval_seconds`, the daemon automatically defaults to this declared interval.
4. **Boot-Time Configuration Schema Validation (`config_schema`)**: The Go daemon validates the instance's nested `config:` mapping against this schema during startup, failing fast with descriptive diagnostic logs rather than silently malfunctioning at runtime. Standard instance settings (`id`, `type`, `dimensions`, `pinned`, `refresh_interval_seconds`, `endpoint`, `method`, `token_env`) are validated by the Core framework and are never passed to `config_schema`.
5. **Runtime Response Schema Validation (`response_schema`)**: The Go daemon validates incoming data payloads (the domain fields within the `data:` object) received from HTTP servers, upstream provider syncs, or inbound webhooks against this schema. Invalid payloads are rejected, preserving the Last-Known-Good (LKG) cache and transitioning the widget to `degraded` state under Stale-While-Revalidate rules, guaranteeing templates never execute against corrupt or incomplete state.

#### Schema Dialect & Validation Standards
Manifest schemas (`config_schema` and `response_schema`) adhere strictly to standard **JSON Schema Draft 2020-12** (represented directly in YAML), matching OpenAPI 3.1:
- **Object & Collection Schemas**: Top-level schemas define `type: object`, declare child keys under `properties:`, enforce mandatory presence via a `required:` array of property names, and represent lists using `type: array` with an `items:` schema (rather than legacy ad-hoc `type: list` or inline `required: true`).
- **Go Validator Engine**: In Go, Mirrormere compiles and evaluates both boot-time configuration schemas and runtime response schemas using `github.com/santhosh-tekuri/jsonschema/v6`.
- **Default Value Semantics**: In JSON Schema Draft 2020-12, `default` is an annotation keyword only; standard validators do not populate or apply default values to instance payloads or configuration mappings.

---

## Declarative Widget Instance Configuration (`config.yaml`)

Mirrormere uses a generic, decoupled configuration model where **each widget instance declared in `display.widgets` is a self-contained unit managing both its data ingestion parameters and its UI presentation settings**. There is no disconnected top-level providers block; all sync cadences, data sources, and display parameters live directly in the widget declaration.

To maintain strict schema isolation and prevent false-positive validation crashes, Mirrormere cleanly decouples **standard framework settings** (which live at the top level of each widget instance entry) from **custom domain configuration** (which is nested inside the widget's `config:` dictionary):

### 1. Standard Instance Keys vs. Custom Domain Configuration
- **Standard Framework Keys** (Top-Level):
  - `id` (string, required): Unique instance identifier (kebab-case).
  - `type` (string, required): Widget package/template type.
  - `dimensions` ([cols, rows], optional): Target layout dimensions on the 6×2 grid. Defaults to package manifest `default_dimensions` (SPEC-005).
  - `pinned` (boolean, optional, default: `false`): Pin widget across all rotation screens.
  - `refresh_interval_seconds` (integer, optional): Sync/polling loop interval in seconds. Defaults to package manifest `refresh.interval_seconds`.
- **Standard Transport Keys** (Top-Level, `provider: http` only):
  - `endpoint` (string, required for `provider: http`): Outbound HTTP URL to poll for widget data.
  - `method` (string, optional, default: `POST`): Outbound HTTP method (`POST` or `GET`).
  - `token_env` (string, optional): Environment variable name containing bearer authentication token.
- **Custom Domain Keys** (Nested under `config:`):
  - `config` (mapping, optional): Domain-specific parameters unique to the widget type (e.g. coordinates for weather, calendar feed URLs, task list IDs, sensor entity IDs).
  - **Validation Boundary**: Only the keys inside `config:` are validated against the widget package's `config_schema`. Top-level standard keys are handled exclusively by the Core engine and are excluded from manifest schema validation.

### 2. Unified Widget Instance Declaration
In `config.yaml`, the `display.widgets` array defines the active widgets using this nested structure:

```yaml
display:
  widgets:
    - id: home-weather              # Unique instance identifier (kebab-case)
      type: weather-forecast        # Widget engine / template type
      dimensions: [2, 1]            # [width_cols, height_rows] on 6x2 grid
      pinned: false                 # Pinned across all rotation screens
      refresh_interval_seconds: 900 # Ingestion sync loop cadence
      config:                       # Widget-specific parameters validated by manifest config_schema
        latitude: 37.8044
        longitude: -122.2712
        units: imperial             # "metric" | "imperial"

    - id: office-weather            # Second independent weather instance!
      type: weather-forecast
      dimensions: [2, 1]
      refresh_interval_seconds: 900
      config:
        latitude: 37.7749
        longitude: -122.4194
        units: imperial

    - id: family-calendar
      type: calendar-agenda
      dimensions: [4, 2]
      refresh_interval_seconds: 300 # Ingestion sync loop cadence
      config:
        view: week                  # Presentation: "day" | "week" | "month"
        window_days_past: 1
        window_days_future: 14
        show_relative_time: true
        calendars:
          - name: "Family Events"
            url_env: FAMILY_CALENDAR_URL
            color: "#a855f7"

    - id: living-room-photos
      type: photo-carousel
      dimensions: [3, 2]
      refresh_interval_seconds: 3600 # Album metadata sync cadence
      config:
        share_url: "https://photos.app.goo.gl/AbCdEf123456789"
        cycle_interval_seconds: 60     # Local client photo rotation interval
        shuffle: true

    - id: grocery-list
      type: tasks
      dimensions: [2, 1]
      refresh_interval_seconds: 300
      config:
        list_id: "groceries"
        list_name: "Groceries"
        source: local
        show_completed: 3

    - id: layout-spacer
      type: spacer
      dimensions: [2, 1] # Built-in zero-data transparent tile to satisfy 12-cell bin-packing (SPEC-005)

    - id: living-room-temp
      type: sensor-card             # Custom widget resolved from /config/widgets/sensor-card/
      dimensions: [2, 1]
      pinned: false
      refresh_interval_seconds: 60
      endpoint: "http://ha-bridge:8095/data"
      token_env: HA_SENSOR_TOKEN
      config:
        entity_id: "sensor.living_room_temp"
        unit: "F"
```

### 3. Multi-Instance Capability
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

1. **Manifest & Instance Declaration**:
   A custom widget declares `provider: http` in its `manifest.yaml` located at `/config/widgets/<widget-type>/manifest.yaml`. The manifest defines both the configuration schema (`config_schema`) and the expected server response payload schema (`response_schema`):
   ```yaml
   # /config/widgets/sensor-card/manifest.yaml
   name: Sensor Card
   version: "1.0.0"
   description: Environmental telemetry tile
   provider: http
   capabilities:
     - ambient-static
     - touch-interactive
   default_dimensions: [2, 1]
   refresh:
     interval_seconds: 60

   config_schema:
     type: object
     required:
       - entity_id
     properties:
       entity_id:
         type: string
       unit:
         type: string
         default: "F"

   response_schema:
     type: object
     required:
       - temperature
     properties:
       temperature:
         type: number
       humidity:
         type: number
       status:
         type: string
         default: "ok"
   ```

   Widget instances in `config.yaml` reference that custom `type` under `display.widgets`:
   ```yaml
   display:
     widgets:
       - id: living-room-temp
         type: sensor-card           # Resolved from /config/widgets/sensor-card/
         dimensions: [2, 1]
         pinned: false
         refresh_interval_seconds: 60
         endpoint: "http://sensor-sidecar:8095/data"
         token_env: SENSOR_HUD_TOKEN
         config:
           entity_id: "sensor.living_room_temp"
           unit: "F"
   ```

2. **Ingestion Polling Wire Contract (`POST {endpoint}`)**:
   To prevent HTTP proxies, web servers, and client frameworks (FastAPI, Express, Axios, nginx) from stripping or rejecting request bodies, Mirrormere uses standard **`POST`** requests to poll the custom endpoint:
   - **Method**: `POST {endpoint}` (configured via top-level instance key `endpoint`; optional `method: GET` configuration supported for dumb IoT sensors that only expose a static parameterless `/status` page).
   - **Headers**:
     - `Content-Type: application/json`
     - `Accept: application/json`
     - `Authorization: Bearer <TOKEN>` (resolved dynamically from `token_env` if defined)
     - `X-Widget-ID: living-room-temp`
     - `X-Widget-Type: sensor-card`
     - `X-Widget-Dimensions: 2x1`
   - **Request Body Sent TO Endpoint**:
     Mirrormere serializes ONLY the custom domain configuration (the exact object declared under the instance's nested `config:` mapping). Top-level framework settings (`id`, `type`, `dimensions`, `pinned`, `refresh_interval_seconds`) and transport keys (`endpoint`, `method`, `token_env`) are never included in the body; instance metadata is passed exclusively via `X-Widget-*` request headers:
     ```json
     {
       "entity_id": "sensor.living_room_temp",
       "unit": "F"
     }
     ```
     This keeps outbound request payloads clean, prevents internal framework settings or secrets from leaking across the network, and allows custom sidecars to directly deserialize domain parameters without unwrapping a wrapper object.
   - **Response Body Received FROM Endpoint**:
     Returns the standard JSON payload envelope:
     ```json
     {
       "widget_id": "living-room-temp",
       "timestamp": "2026-09-24T22:20:00Z",
       "state": "healthy",
       "data": {
         "temperature": 71.2
       }
     }
     ```
   - **Mutation Handling (`POST {endpoint}/action`)**:
     Optional mutation handler. Forwards user actions from `POST /api/widgets/{widget_id}/action` (returns `200` OK, `202` Accepted, or `502` Bad Gateway per SPEC-006).

3. **Runtime Response Schema Validation (`response_schema`)**:
   When data is received from the HTTP endpoint (or inbound push webhook), the Go daemon validates the payload before passing it to the presentation layer:
   - **Validation Scope**: Validates the contents of the `data` object against the `response_schema` defined in the widget's `manifest.yaml` using JSON Schema Draft 2020-12 compiled via `github.com/santhosh-tekuri/jsonschema/v6`. If an IoT endpoint returns raw domain JSON without an outer envelope (e.g. `{"temperature": 71.2}`), the daemon validates it against `response_schema` and wraps it into the canonical envelope automatically.
   - **Enforcement & Stale-While-Revalidate**: If required fields are missing, unexpected nulls occur, or primitive types mismatch (e.g. string provided instead of number), the Go daemon:
     1. Emits a structured log (`[widget.validator] widget="living-room-temp" error="response_schema validation failed: missing required field 'temperature'"`).
     2. Freezes and preserves the Last-Known-Good (LKG) data snapshot.
     3. Transitions widget operational state to `degraded` (or `error` on cold boot).
     4. Suppresses updating the SQLite cache with corrupt payloads.
   - **Template Safety Guarantee**: The semantic view template (`views/widget.html`) is guaranteed that every variable it accesses strictly satisfies the declared schema contract, preventing runtime template execution errors or partial visual tearing.

4. **Realtime Push / Webhook Support**:
   Sidecars and external services can push updates immediately into Mirrormere by issuing an HTTP POST webhook:
   - `POST /api/widgets/{widget_id}/push`
   - Headers: `Content-Type: application/json`
   - Body: Standard payload envelope (`widget_id`, `timestamp`, `state`, `data`).
   - Auth: None (all inbound LAN calls are fully trusted per the local-network trust model).
   - Behavior: Validates `data` against `response_schema`, then updates the widget's in-memory and SQLite cache regardless of active screen. Push webhooks are strictly limited to sidecar and generic HTTP provider widgets; calling push on list-backed widgets (`type: tasks`) is rejected with `409 Conflict` (task lists are strictly read-only ambient displays ingested from configured upstream providers per SPEC-006 and SPEC-008). The daemon immediately broadcasts a `widget.update` SSE event across all open `/api/events` connections (per SPEC-006), ensuring all connected displays, companion nodes, and cached client stores update their local state immediately with zero rotation delay. See SPEC-006 for full request/response schemas, status codes (200/400/404/409), and validation rules.

---

## Unified Semantic HTML & Server-Wide Styling

Mirrormere eliminates the authoring and maintenance overhead of dual templates (`touch.html` vs. `eink.svg`). Every widget authors **one single semantic HTML template** (`views/widget.html`).

### 1. View Template (`views/widget.html`)
- Delivered as a clean semantic HTML5 snippet or modern Web Component loaded into the canvas.
- Markup uses clean semantic classes (e.g. `.widget`, `.widget-title`, `.item-done`) and standard CSS variables for styling.
- Layout flexes and responds dynamically to its assigned grid cells per SPEC-005.
- Receives state updates reactively via Server-Sent Events (`widget.update` per SPEC-006).
- Template Execution Context: Evaluated with `.Config`, `.Data`, `.State`, `.Timestamp`, and `.Assets` (base URL path `/widgets/<widget-type>/assets` referencing static package assets).

### 2. Server-Wide Volume-Mounted Stylesheet (`/config/custom.css`)
In alignment with Mirrormere's independent deployment topology and zero-runtime-multiplexing invariant:
- **Zero Config Keys**: There are no `theme:`, `profile:`, or `css:` selectors in `config.yaml`.
- The server serves a single unified stylesheet at `/style.css`.
- At startup, the server inspects `/config/custom.css` on disk:
  - **Mounted File Present**: Serves the volume-mounted file directly.
  - **Unmounted / Absent**: Serves the default core stylesheet directly from disk (`/app/web/static/css/hud.css`).
- Each household injects its display styling purely via Docker volume mounts:
  - **Alex's Touch Kiosk**: `-v ./kiosk.css:/config/custom.css:ro` (dark mode cyberpunk palette, 48px touch targets, glowing accents).
  - **Mike's Ambient E-Ink**: `-v ./eink.css:/config/custom.css:ro` (high-contrast 1-bit monochrome, bold typography, zero animations, 2px solid borders).

---

## In-Process Hot-Reloading (`fsnotify` & SSE)

To deliver a frictionless developer experience and enable instant visual iteration on physical display hardware without restarting the Go daemon or rebuilding containers:

### 1. In-Process File Watching (`fsnotify`)
The Go daemon runs a background `fsnotify` file system watcher monitoring `/config/widgets/`, `/config/custom.css`, and `/config/config.yaml`:
- **Debounced Processing (100ms)**: Batches rapid filesystem events from code editors and atomic file rename operations to eliminate thrashing.
- **In-Memory Cache Eviction**:
  - When any `views/widget.html` file changes on disk, the Go server immediately evicts its in-memory compiled `html/template` cache. The next canvas render parses the fresh template directly from disk.
  - When `manifest.yaml` updates, the daemon updates the widget registry metadata JIT.
  - When `/config/config.yaml` updates, the configuration parser re-parses with `${VAR}` interpolation under the Last Known Good Configuration (LKGC) resilience model.

### 2. Live SSE Signal Dispatch
Because connected display clients (Chromium kiosk browser, companion tablets, e-ink renderers) maintain an active stream on `GET /api/events`, the Go daemon broadcasts targeted reload events over the event bus:
- **Widget Markup Updates**: Broadcasts `event: widget.reload` with payload `{"type": "<widget-type>"}`.
- **Stylesheet Updates**: Broadcasts `event: style.reload` with payload `{"file": "custom.css", "timestamp": "2026-09-25T19:40:00Z"}`.

### 3. Client DOM Reaction (Zero Manual Screen Taps)
The frontend display script (`web/static/js/sse.js`) handles reload events reactively:
- **Zero-Flicker Style Hot-Swapping**: On `style.reload`, the browser swaps the stylesheet `<link>` tag's `href` with a cache-busting timestamp query parameter (`/style.css?t=Date.now()`). The entire display updates its visual theme instantly with **zero visual flicker**.
- **Instant Template Refresh**: On `widget.reload`, the client re-fetches the updated widget markup fragment and patches its DOM node (or cleanly invokes `location.reload()`).

Developers and users edit files in their IDE on the host, and the physical kiosk on the wall updates in under 100ms—with zero container restarts, zero SSH sessions, and zero touching the display.

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
   - Includes: Home Assistant entity dashboards and custom LAN sensor monitors (video feeds such as Chromecast and doorbell cameras enter directly through the Unified Video Stream API per SPEC-004 rather than as grid widgets).

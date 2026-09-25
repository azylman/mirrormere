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
  4. Voice assistant streaming pipeline (`mirrormere-voice` client and LAN Voice Hub in SPEC-011)

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

    subgraph Host Infrastructure: Touch Kiosk (Intel N100)
        Cage[cage Wayland Compositor - deploy/kiosk]
        Chromium[Chromium Kiosk Browser]
        Go2rtc[Stock go2rtc Video Streamer]
        CastWatcher[sidecars/cast-watcher Sidecar]
        Cage --> Chromium
    end

    subgraph Host Infrastructure: Ambient E-Ink (Raspberry Pi 4B)
        Sidecar[sidecars/eink-renderer Sidecar]
        Node[clients/eink-node Python Daemon]
        Driver[Waveshare / Adafruit SPI Driver]
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
    CC -->|HDMI to USB UVC| Go2rtc
    CC -.->|TCP 8009 CastV2| CastWatcher
    CastWatcher -->|POST /api/video/trigger| Engine
    Go2rtc -->|WebRTC Stream| Chromium
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
  - Host bootstrap scripts, Wayland `cage` launch wrappers, and systemd units are maintained in `deploy/kiosk/`.
  - Runs Chromium under Wayland `cage` with hardware-accelerated WebGL/CSS.
  - Supports live touch gestures, smooth transitions, real-time status pulses, and video capture overlays.
  - Zero on-screen keyboard (OSK) overhead; item editing is companion/phone-first.
  - DPMS sleep lifecycle with night window and daytime tap-to-wake.

- **Tier 2: Ambient E-Paper Kiosk (0.01Hz)**
  - Targeted at Raspberry Pi hardware driving 7.5" e-Paper SPI HATs/Bonnets (SPEC-002, SPEC-009).
  - Physical display client software is maintained in `clients/eink-node/` as a lightweight Python daemon.
  - Operates completely headlessly without running a local browser on the display node.
  - Subscribes to backend SSE change events with 5s coalescing and 60s minimum refresh floors.
  - Renders 1-bit selectively dithered PNGs via the server-side `sidecars/eink-renderer` sidecar (SPEC-003, SPEC-009).

### 3. Decoupled Presentation Layer
The Go backend has zero awareness of how pixels are drawn. The server renders semantic HTML layouts (`views/widget.html`), injected with deployment-time volume-mounted stylesheets (`/config/custom.css`), allowing the exact same underlying calendar or task model to be presented on interactive touch kiosks or captured for static monochrome e-paper.

### 4. Declarative Configuration & House Timezone (`config.yaml`)
Mirrormere instances are statically configured at deployment time via a root configuration file mounted into `/config/config.yaml`:
- **Top-Level `timezone` Key**: Declares the authoritative household IANA timezone (e.g. `timezone: "America/Los_Angeles"`). All date formatting, header clocks, calendar event bounding intervals, and midnight chore rollovers execute against this house timezone, preventing container host timezone divergence.
- **Top-Level Schema Structure**:
  ```yaml
  timezone: "America/Los_Angeles" # Authoritative house timezone (IANA)

  display:
    rotation:
      interval_seconds: 30
      transition: "slide"
    header:
      enabled: true
      elements: [clock, date, weather_badge, sync_status]
    widgets:
      - id: family-calendar
        type: calendar-agenda
        dimensions: [4, 2]
      - id: calendar-pad
        type: spacer
        dimensions: [2, 2]
  ```

---

## Standardized Port Allocations & Network Topology

To maintain predictable local network service discovery, simplify firewall configuration, and prevent port collisions across sidecars, Mirrormere standardizes host port allocations across all deployment topologies:

| Port | Service | Protocol | Description | Profile / Deploy Target |
|---|---|---|---|---|
| **8080** | `mirrormere-core` | HTTP / SSE | Core Daemon: REST API, SSE event stream (`/api/events`), healthcheck (`/healthz`), and web UI | Base Default (all instances) |
| **8081** | `eink-renderer` | HTTP | E-Ink PNG Renderer: Headless Chromium rasterizer generating 800×480 1-bit dithered image (`GET /eink.png`) | `profiles: [eink]` |
| **1984** | `go2rtc` | HTTP / WebRTC | Video Stream Ingest: Stock UVC capture ingest, WebRTC streaming, and RTSP stream proxy | `profiles: [video]` |
| **8090** | `cast-watcher` | HTTP | CastV2 Protocol Bridge: Internal HTTP transport control (`POST /action`) and outbound LAN TCP 8009 to Chromecast | `profiles: [video]` (internal network) |
| **9000** | `voice-hub` | HTTP / SSE | LAN Voice Hub Sidecar: Audio ingest, wake word, Whisper STT, and Kokoro/Piper TTS gateway | Phase 2 `profiles: [voice]` |

All container services are declared within a single, unified `deploy/compose.yml` leveraging native Docker Compose `profiles:` (`video`, `eink`, `voice`), completely eliminating divergent per-profile compose files.

---

## Repository Layout & Monorepo Architecture

Mirrormere is organized as a modular, polyglot monorepo cleanly separating the generic pure-Go server application from auxiliary container sidecars, edge hardware clients, and deployment automation:

```text
mirrormere/
├── .github/
│   └── workflows/              # Multi-arch CI (amd64 + arm64), linting, and release pipelines
├── api/
│   ├── openapi.yaml            # Single source of truth for REST endpoints and DTO schemas
│   └── schemas/                # Formal JSON Schemas for Server-Sent Events (SSE) payloads
├── cmd/
│   └── server/                 # mirrormere-core entrypoint binary (main.go)
├── internal/                   # Private Go core application packages (encapsulated, non-importable)
│   ├── api/                    # HTTP REST handlers, SSE pub/sub broker, and generated router
│   ├── config/                 # YAML configuration parsing, defaults, and validation
│   ├── layout/                 # 6x2 grid bin-packing, rotation engine, and spacer solver
│   ├── providers/              # Data sync clients (iCal/CalDAV, Open-Meteo, Google Photos)
│   └── storage/                # SQLite WAL store for custom lists, chores, and persistent state
├── web/
│   ├── static/                 # Embedded base CSS (cyber HUD variables), JS, and touch handlers
│   └── views/                  # Embedded semantic HTML widget templates (widget.html)
├── sidecars/
│   ├── eink-renderer/          # Headless Chromium snapshotter (Dockerfile, Node/Puppeteer or Go)
│   └── cast-watcher/           # Lightweight CastV2 LAN bridge (Dockerfile, Python/Go)
├── clients/
│   └── eink-node/              # Python SPI client for Raspberry Pi driving e-paper panels
├── deploy/
│   ├── compose.yml             # Authoritative compose manifest using profiles: [video, eink]
│   ├── go2rtc.yaml             # Reference go2rtc hardware mapping (/dev/video0 UVC to WebRTC)
│   ├── kiosk/                  # Wayland cage + Chromium launch scripts and systemd units
│   └── examples/               # Reference custom.css (kiosk.css, eink.css) and config.yaml files
├── specs/                      # Living architectural specifications (001–011)
├── scripts/                    # Pre-flight verification (verify.sh) and coverage auditing
├── Dockerfile                  # Multi-stage, multi-arch build for mirrormere-core
└── Makefile                    # Local build, test, and verification shortcuts
```

### Monorepo Boundaries & Rules:
1. **Application Encapsulation (`internal/`)**: The Go server is an application binary, not an exported public library. All Go packages reside under `internal/` to prevent external import coupling and grant full freedom for internal refactoring.
2. **Custom Sidecars vs. Stock Upstream**:
   - `sidecars/` contains custom microservices built from local Dockerfiles (`eink-renderer`, `cast-watcher`).
   - Stock off-the-shelf images (`alexxit/go2rtc`) have zero custom code in the repository and are configured purely via mounts from `deploy/go2rtc.yaml`.
3. **Clients vs. Provisioning Taxonomy**:
   - `clients/` is reserved for standalone client application source code (e.g. `clients/eink-node/` Python SPI daemon).
   - Shell launch wrappers, desktop compositor configurations, and systemd units for the display kiosk belong in `deploy/kiosk/`.
4. **Deferred Phase 2 Scaffolding**:
   - Phase 2 Voice directories (`sidecars/voice-hub`, `clients/mirrormere-voice`) are intentionally deferred from Git scaffolding until active Phase 2 implementation begins to prevent empty directory drift.

---

## API-First Contract, Codegen & Verification Invariants

To guarantee interoperability across polyglot components (Go server, Python clients, browser JavaScript, and companion webhooks) without contract drift, Mirrormere enforces an **API-First Development Contract**:

### 1. Single Source of Truth (`api/openapi.yaml` & `api/schemas/`)
- All REST endpoints, query parameters, request bodies, and JSON responses are formally defined in `api/openapi.yaml` (OpenAPI 3.1).
- All Server-Sent Events (SSE) dispatched on `GET /api/events` are formally defined via JSON Schemas in `api/schemas/`, exactly matching the canonical event names defined in SPEC-006 §2: `widget.update.json`, `screen.rotate.json`, `system.status.json`, `video.state.json`, `audio.state.json`, and `voice.state.json`. Every event type emitted across the SSE event bus must maintain an authoritative schema file under `api/schemas/` enforced via CI pre-flight validation.

### 2. Compile-Time Go Server Contract (`oapi-codegen`)
- Core REST interfaces and Data Transfer Objects (DTOs) are generated into `internal/api/` via `oapi-codegen`.
- Generated code produces a strict Go interface:
  ```go
  type ServerInterface interface {
      // GetHealthz handles GET /healthz
      GetHealthz(w http.ResponseWriter, r *http.Request)
      // PostApiVideoTrigger handles POST /api/video/trigger
      PostApiVideoTrigger(w http.ResponseWriter, r *http.Request)
      // ...
  }
  ```
- The Go server handler struct MUST implement `ServerInterface`. Any path modification, parameter addition, or schema mutation in `openapi.yaml` immediately results in a **Go compile-time error** until the implementation is updated.

### 3. Zero-Drift Codegen Pre-Flight Gate (`scripts/verify.sh`)
- `scripts/verify.sh` and GitHub Actions CI execute:
  ```bash
  go generate ./...
  git diff --exit-code internal/api/
  ```
- If a developer or agent edits `api/openapi.yaml` without regenerating Go code—or hand-edits generated files—the verification step fails with a non-zero exit code prior to commit.

### 4. Fast OpenAPI & Schema Linting (`vacuum`)
- `vacuum` is integrated into `scripts/verify.sh` as a fast (<50ms), zero-dependency OpenAPI linter.
- Validates unresolved `$ref` models, missing HTTP status codes, malformed path parameters, and OWASP API hygiene rules.

### 5. Breaking Change Detection (`oasdiff`)
- Pull Request CI compares `api/openapi.yaml` against `origin/main:api/openapi.yaml` using `oasdiff`.
- Incompatible changes (e.g. dropped endpoints, altered field types, or newly required parameters) block merge without an explicit semver breaking change override.

### 6. Client Type Safety (Python & Web)
- Python daemons (`clients/eink-node/` and `sidecars/cast-watcher/`) generate typed Pydantic models or dataclasses via `datamodel-code-generator` from `api/openapi.yaml`, enforced by `mypy` and `ruff`.
- The web frontend PWA validates event envelopes against `api/schemas/` in end-to-end integration tests.


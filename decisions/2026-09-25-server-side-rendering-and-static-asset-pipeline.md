# Architecture Decision Record: Server-Side Rendering Engine & Static Asset Pipeline

- **Date:** 2026-09-25
- **Status:** Accepted
- **Context:** Implementing Chunk 2.3 of Phase 2 (Issue #206, SPEC-003 §1–3, SPEC-006 §1.A, §2.I, SPEC-013 Chunk 2.3).

---

## 1. Problem Statement
Mirrormere mandates that all widget rendering occurs strictly on the headless Go backend daemon using Go's `html/template` package. Display clients (Chromium Touch Kiosk PWA and Raspberry Pi E-Ink capture nodes) receive pre-rendered HTML fragments and hot-swap DOM nodes without compiling templates or executing client-side data binding libraries.

Chunk 2.3 requires:
1. In-process `html/template` compilation, thread-safe template caching, and cache invalidation on `widget.reload` events.
2. Injection of the unified semantic template context (`.ID`, `.Type`, `.Dimensions`, `.Data`, `.State`, `.Timestamp`, `.Config`, `.Origin`, `.Theme`, `.Online`, `.Assets`).
3. Strict sanitization of `.Config` to prevent credentials, secrets, or `*_env` pointers from leaking into template execution contexts.
4. Implementation of standard template helper functions (`seq`, `formatDate`, `formatTime`, `json`, `relDate`).
5. HTTP endpoints:
   - `GET /api/widgets/{widget_id}/render`: returns `Content-Type: text/html; charset=utf-8` fragment for active widget instances.
   - `GET /widget-types/{type}/assets/{path}`: serves static package assets with strict path traversal defenses.
   - `GET /style.css`: serves volume-mounted custom stylesheet (`/config/custom.css`) if present, falling back to default core stylesheet (`hud.css`).
6. Wiring endpoints into `internal/server/server.go` while preserving Slowloris defenses and meeting the `>= 95.0%` statement test coverage floor.

---

## 2. Decision & Architecture

### A. Template Engine & In-Memory Caching (`internal/render/engine.go`)
- The `Engine` manages a thread-safe cache (`map[string]*html/template.Template`) protected by a read-write mutex (`sync.RWMutex`).
- Templates are parsed JIT on first access and cached by widget type.
- Granular cache eviction is supported via `Invalidate(widgetType)` and `InvalidateAll()`, allowing the filesystem watcher to invalidate cached templates immediately when `views/widget.html` modifications occur.
- If a widget instance is not placed on any active layout screen (e.g. rotated off-screen or unplaced), the engine gracefully falls back to the instance's declared dimensions, the package's `default_dimensions`, or a nominal `[1, 1]` footprint with `Origin: [0, 0]`.

### B. Template Context & Secret Sanitization (`internal/render/context.go`)
- The template execution `Context` exposes all 11 required fields per SPEC-003: `.ID`, `.Type`, `.Dimensions`, `.Data`, `.State`, `.Timestamp`, `.Config`, `.Origin`, `.Theme`, `.Online`, and `.Assets`.
- `SanitizeConfig(cfg)` recursively clones custom widget configuration maps while pruning any keys matching credential patterns (`_env`, `token`, `secret`, `password`, `pass`, `api_key`, `apikey`, `auth`). This guarantees zero token or credential leakage into rendered HTML markups.

### C. Standard Template Helpers (`internal/render/helpers.go`)
- `seq(args ...int) []int`: supports 1-argument (`0..n-1`), 2-argument (`start..end`), and 3-argument (`start..end step`) forms for ergonomic `{{range seq ...}}` loops.
- `formatDate(layout, val)` & `formatTime(layout, val)`: robust multi-layout time parsers (supporting RFC3339, RFC3339Nano, date-only, time-only, 12h formats, and unix timestamps) returning formatted time strings or original values on unparseable inputs.
- `relDate(refTime, val)`: computes calendar day offsets in the timestamp's local timezone, returning "Today", "Tomorrow", "Yesterday", "in X days", or "X days ago".
- `json(val)` & `jsonJS(val)`: serialization helpers returning unescaped `template.HTML` and safe `template.JS`.

### D. Static Asset Pipeline & Path Traversal Defenses (`internal/render/handler.go`)
- `GET /widget-types/{type}/assets/{path...}` validates inbound relative paths against directory traversal (`..`, absolute paths, leading slashes).
- Uses `filepath.Rel` and `filepath.Clean` to strictly verify that requested files reside entirely within the package's declared `assets/` directory.
- Refuses to serve directory indexes (returning 404 for directory requests).

### E. Unified Stylesheet Endpoint (`GET /style.css`)
- Inspects `/config/custom.css` on disk (supporting volume mounts `-v ./config:/config:ro`).
- If custom CSS is unmounted or absent, inspects `/app/web/static/css/hud.css`.
- If neither exists on disk (such as in airgapped unit test environments), serves the embedded default cyber HUD stylesheet (`defaultEmbeddedHUDCSS`) directly, ensuring `/style.css` always responds with `200 OK` and `Content-Type: text/css; charset=utf-8`.

### F. Server Decoupling (`internal/server/server.go`)
- Declares the `RenderHandler` interface in `internal/server` decoupling routing from the rendering engine implementation.
- Registers standard Go 1.22+ wildcard routes:
  - `/api/widgets/{widget_id}/render`
  - `/widget-types/{type}/assets/{path...}`
  - `/style.css`
- Supports dynamic registration via `server.RegisterRenderHandler(h)` matching the existing `EventsHandler` and `ScreenHandler` patterns.

---

## 3. Verification & Metrics
- All package unit tests pass hermetically without network or live container requirements.
- Full `./scripts/verify.sh` passes with zero codegen drift, zero lint warnings, and zero deadcode.
- Statement test coverage:
  - `internal/render`: **97.45%** (306/314 statements)
  - `internal/server`: **98.00%** (147/150 statements)
  - Total Monorepo: **97.25%** (2,478/2,548 statements), exceeding the 95.0% floor across all packages.

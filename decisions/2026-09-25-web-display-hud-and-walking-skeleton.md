# Architecture Decision Record: Web Display HUD Client & Walking Skeleton

- **Date:** 2026-09-25
- **Status:** Accepted
- **Context:** Implementing Chunk 2.4 of Phase 2 (Issue #208, SPEC-003 §1–4, SPEC-005 §1–4, SPEC-006 §1–2, SPEC-010, SPEC-013 Chunk 2.4).

---

## 1. Problem Statement
Mirrormere requires an end-to-end walking skeleton connecting backend data providers, layout solvers, screen rotation, server-side rendering, and the web display client runtime. The display client is loaded by fullscreen Chromium kiosks (`http://localhost:8080/display` in `--kiosk` mode under Wayland `cage`) and headless capture sidecars (`sidecars/eink-renderer`).

Chunk 2.4 requires:
1. Core built-in `spacer` widget package (`widgets/spacer/manifest.yaml` and `widgets/spacer/views/widget.html`) enabling sparse widget layouts on the 6x2 grid while strictly preserving the 12-cell fully-filled invariant.
2. Web display HUD shell (`web/templates/display.html`) and Cyber HUD CSS tokens (`web/static/css/hud.css`).
3. Vanilla ES6 event bus client (`web/static/js/sse.js`) consuming `GET /api/events` with exponential backoff and automatic reconnection.
4. Carousel controller (`web/static/js/carousel.js`) implementing zero-flash DOM pre-warming, layout positioning, and touch swipe gestures.
5. Display application runtime (`web/static/js/display.js`) managing header clock/date, ambient weather pill, status dot, and live stylesheet/widget hot-swapping.
6. Display handler (`internal/display/handler.go`) serving `GET /display` and `GET /static/{path...}` with strict directory traversal defenses, filesystem-first resolution, and embedded hermetic fallbacks.
7. Server wiring in `internal/server/server.go` adhering to Slowloris defenses and meeting the `>= 95.0%` statement test coverage floor.

---

## 2. Decision & Architecture

### A. Core Built-In `spacer` Widget (`widgets/spacer/`)
- Declarative manifest (`widgets/spacer/manifest.yaml`) declares `provider: spacer`, `default_dimensions: [1, 1]`, and all 12 supported dimensions on the 6×2 grid ($1 \le \text{cols} \le 6, 1 \le \text{rows} \le 2$).
- Declares both `ambient-static` and `touch-interactive` capabilities.
- Semantic HTML template (`widgets/spacer/views/widget.html`) renders an empty tile `<div class="widget-spacer">` with inline grid span styles and data attributes (`data-widget-id`, `data-widget-type`).
- Fully transparent canvas styling (`.widget-spacer { background: transparent; border: none; pointer-events: none; }`) allows the underlying theme background to show through cleanly with zero data fetching or polling overhead.

### B. Display HUD Shell & Styling (`web/templates/display.html`, `web/static/css/hud.css`)
- Fullscreen `#mirrormere-app` layout partitioning the fixed header zone (`.header-zone`) from the 6×2 CSS grid canvas (`#grid-canvas`).
- Fixed header features:
  - Local real-time clock (`.header-clock`) and date (`.header-date`) updating every 1000ms.
  - Ambient weather pill badge (`.pill-badge`, `.weather-temp`, `.weather-condition`) updating via `header.update` SSE events.
  - System status indicator dot (`.status-dot`) with animated status transitions for healthy (green), degraded (amber), and offline (red).
- CSS Grid tokens and variables styled with the Cyber HUD dark theme (moody dark slate canvas `#090514`, deep violet surfaces, electric purple neon glow accents, and monospace font tokens).
- Touch sanitization: `-webkit-tap-highlight-color: transparent`, `user-select: none`, `-webkit-touch-callout: none` per SPEC-010 appliance standards.

### C. Realtime Event Bus (`web/static/js/sse.js`)
- `MirrormereSSE` encapsulates native `EventSource('/api/events')`.
- Handles connection lifecycle with exponential backoff: base delay 1000ms, maximum delay 30000ms, backoff factor 1.5, and random jitter to avoid reconnect thundering herds.
- Listens for all standard typed events: `screen.rotate`, `widget.update`, `widget.reload`, `style.reload`, `header.update`, `system.status`, and `voice.state`.

### D. Carousel & Zero-Flash Pre-Warming (`web/static/js/carousel.js`)
- Screen rotation transitions implement an off-screen pre-warming pipeline:
  1. On `screen.rotate`, concurrently fetches `GET /api/widgets/{widget_id}/render` for all widgets on the target layout via `Promise.all`.
  2. Builds incoming DOM nodes into a `DocumentFragment` with explicit CSS grid line positioning (`gridColumn: ${colStart} / span ${colSpan}`, `gridRow: ${rowStart} / span ${rowSpan}`).
  3. Replaces active grid nodes in a single frame with a 300ms transition class, eliminating intermediate blank screen flashes.
- In-place widget updates: on `widget.update`, if the widget is visible on screen, fetches fresh markup and swaps its element in place without disrupting neighboring tiles.
- Touch swipe navigation: capacitive touch swipes left/right trigger `POST /api/screen/advance` (`next`/`prev`).

### E. Application Runtime & Live Hot-Reloading (`web/static/js/display.js`)
- Coordinates component initialization on `DOMContentLoaded`.
- Hot-swaps stylesheet `<link id="hud-stylesheet">` on `style.reload` events using a cache-busting timestamp query parameter (`/style.css?t=Date.now()`), allowing real-time CSS editing with zero browser refreshes.
- Live widget reloads: on `widget.reload` events, queries all mounted elements matching `data-widget-type` and triggers in-place DOM updates.

### F. Display Handler & Static Pipeline (`internal/display/handler.go`)
- Serves `GET /display` returning the base HTML shell.
- Serves `GET /static/{path...}` with strict path traversal defenses (`filepath.Clean`, rejection of `..`, absolute paths, leading slashes, and dot-prefixed paths).
- Filesystem-first priority: inspects `/app/web` (container mount) followed by `web/` (local repo development) for instant live development without rebuilding binaries.
- Hermetic fallback: embedded `web.Content` FS ensures airgapped unit tests and standalone static binaries function without external filesystem mounts.

### G. Server Integration (`internal/server/server.go`)
- Declares the `DisplayHandler` interface in `internal/server` decoupling server routing from the display handler implementation.
- Registers `/display` and `/static/{path...}` routes.
- Fully wired into `server.Config` and dynamically configurable via `RegisterDisplayHandler`.

---

## 3. Verification & Metrics
- Monorepo pre-flight sweep (`./scripts/verify.sh`) passes with zero codegen drift, zero lint warnings, and zero deadcode.
- Statement test coverage:
  - `internal/display`: **95.50%** (106/111 statements)
  - `internal/server`: **98.27%** (170/173 statements)
  - Monorepo Total: **97.20%** (2,607/2,682 statements), exceeding the 95.0% coverage floor across all packages.
- Walking skeleton end-to-end integration test (`TestWalkingSkeleton_EndToEnd`) verifies full orchestration from config snapshot and spacer rendering through display HTML and static asset delivery.

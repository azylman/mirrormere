# Legacy Display Performance Baseline

Measured against the legacy vanilla-JS display (`web/templates/display.html` and `web/static/js/*.js`) before the React UI migration (#765). All client-side timings are recorded in milliseconds via `?perf=1` (`web/static/js/perf.js`) and exported by the core server as `mirrormere_display_<metric>_ms{ui="legacy"}` histograms on `/metrics` (see `docs/perf.md`).

- **Commit SHA**: `8b76f8b` (`8b76f8b8504566cfd8a1b14bc6b48e8788253658`)
- **Date**: `2026-10-31`
- **Legacy JS bundle**: `63.7 KB` gzip (`288.0 KB` raw across 10 scripts in `web/templates/display.html`; `59.7 KB` concatenated gzip, excluding `?perf=1`-only `perf.js`)

---

## 1. Touch Kiosk Hardware

- **Environment**: Intel N100 Mini PC (x86_64 Linux, Wayland `cage` compositor at 2.0x display scale on 1920x1080 touchscreen)
- **Browser**: `Google Chrome 155.0.8059.39`
- **Commit SHA**: `8b76f8b`
- **Date**: `2026-10-31`

### 1a. Headless Bench (`--cpu-throttle 1 --swipes 30`)

Command:

```sh
node scripts/perf/bench.mjs --url <kiosk-display-url> --cpu-throttle 1 --swipes 30
```

| Metric | Count | p50 (ms) | p95 (ms) | Max (ms) | Notes |
| --- | ---: | ---: | ---: | ---: | --- |
| `gesture_first_frame` | 30 | 14.4 | 17.4 | 38.9 | `touchstart` to first `requestAnimationFrame`; nothing moves visually until `touchend`. |
| `swipe_settle` | 2 | 243.1 | 270.3 | 270.3 | Center-viewport swipes (`x=810..1110`) are ignored while Screen 0's 4-column `calendar-grid` widget is mounted (`data-prevent-screen-swipe`); see 1b for unobstructed swipes. |
| `frame` | 29 | 16.7 | 66.6 | 66.8 | Sampled while `body.dataset.animating === "1"` during screen transitions. |
| `long_task` | 1 | 62.0 | 62.0 | 62.0 | Main-thread task ≥ 50 ms during initial hydration and transition swap. |
| `interaction` | 872 | 40.0 | 72.0 | 400.0 | Event Timing entries > 16 ms across touch gestures. |
| `screen_ready` | 1 | 368.5 | 368.5 | 368.5 | Navigation start to first `data-screen-hydrated="true"` stamp. |
| `widget_update` | 0 | — | — | — | No server `widget.update` push occurred during the 30 s bench window; see 1b for live updates. |

### 1b. Live Kiosk Session — 10 Minutes of Normal Use (`Chrome --kiosk` under `cage`, `?perf=1` & `/metrics`)

Captured over 10 minutes of active kiosk operation under Wayland `cage` (`--kiosk --ozone-platform=wayland --force-device-scale-factor=2.0`) with 30 s screen rotation, 60 s photo crossfades, live provider polling, and unobstructed touch swipes on the right-hand pane (`x=1450..1750`):

| Metric | Count | p50 (ms) | p95 (ms) | Max (ms) | `/metrics` Histogram Summary (`mirrormere_display_<metric>_ms{ui="legacy"}`) | react (`?ui=react`, kiosk) |
| --- | ---: | ---: | ---: | ---: | --- | --- |
| `gesture_first_frame` | 46 | 14.2 | 15.5 | 15.5 | 46/46 live samples in `le="16.7"` bucket (mean `14.1 ms`). | `n=20`, p50 `13.9 ms`, p95 `15.0 ms`, max `15.9 ms` (`82/84` `/metrics` samples in `le="16.7"`). |
| `swipe_settle` | 45 | 204.1 | 235.8 | 240.8 | 7 in `le="200"`, 45/45 in `le="300"` (mean `213.6 ms`; 2–3 widget HTML refetches per swipe). | `n=20`, p50 `284.4 ms`, p95 `303.6 ms`, max `317.5 ms` (`250 ms` CSS settle transition + rAF alignment; 0 widget HTML refetches). |
| `frame` | 399 | 16.7 | 50.0 | 99.9 | 238 in `le="16.7"` (59.6%), 339 in `le="33"` (85.0%), 389 in `le="50"` (97.5%), 399 in `le="100"` (100%); 15.0% of transition frames exceed 33 ms. | `n=284`, p50 `16.7 ms`, p95 `16.8 ms`, max `33.4 ms` (`353/496` `/metrics` samples in `le="33"`, `412/496` in `le="50"`). |
| `long_task` | 0 | — | — | — | 0 long tasks during steady-state rotation; 4 total across cold loads (`le="50"`: 1, `le="100"`: 4, sum `249.0 ms`). | `0` during swipes (`0` in 1x bench across cold load + 20 swipes; `3` cold-load samples in `le="100"` on `/metrics`). |
| `interaction` | 1036 | 24.0 | 32.0 | 128.0 | 334 in `le="16.7"`, 991 in `le="33"`, 1023 in `le="50"`, 1035 in `le="100"`, 1036 in `le="200"`. | `n=600`, p50 `48.0 ms`, p95 `56.0 ms`, max `64.0 ms` (`1202` `/metrics` samples in `le="33"`, `1552` in `le="100"`). |
| `screen_ready` | 2 | 100.9 | 192.6 | 192.6 | 1 in `le="200"` (`100.9 ms` warm cache), 2 in `le="200"` (`192.6 ms` reload). | `n=1`, `102.2 ms` (`1/5` `/metrics` samples in `le="100"`, `4/5` in `le="200"`, mean `161.1 ms`). |
| `widget_update` | 8 | 20.5 | 46.9 | 46.9 | 1 in `le="16.7"`, 6 in `le="33"`, 8/8 in `le="50"` (mean `27.0 ms`; full widget HTML refetch and DOM node replacement). | `0` (N/A — React reconciles `widget.update` JSON in-place via Zustand store without HTML refetch or DOM node replacement). |

---

## 2. CI-Class Machine (Headless 4x CPU Throttle)

- **Environment**: Headless x86_64 Linux container (`--cpu-throttle 4`, 1920x1080 virtual viewport, touch emulation)
- **Browser**: `Google Chrome for Testing 152.0.7977.42`
- **Commit SHA**: `8b76f8b`
- **Date**: `2026-10-31`

Command:

```sh
node scripts/perf/bench.mjs --url <kiosk-display-url> --cpu-throttle 4 --swipes 30
```

| Metric | Count | p50 (ms) | p95 (ms) | Max (ms) | Notes |
| --- | ---: | ---: | ---: | ---: | --- |
| `gesture_first_frame` | 30 | 15.2 | 15.8 | 16.2 | Main-thread touch listener to first animation frame under 4x CPU throttle. |
| `swipe_settle` | 1 | 233.6 | 233.6 | 233.6 | First swipe transitions Screen 1 -> Screen 0; subsequent center-viewport swipes land on Screen 0's 4-column `calendar-grid`. |
| `frame` | 28 | 16.7 | 33.3 | 66.7 | Transition frame intervals under 4x CPU throttle. |
| `long_task` | 2 | 52.0 | 73.0 | 73.0 | Two main-thread tasks > 50 ms during initial hydration and screen swap. |
| `interaction` | 896 | 40.0 | 48.0 | 80.0 | Event Timing entries > 16 ms across synthetic CDP touch dispatches. |
| `screen_ready` | 1 | 372.8 | 372.8 | 372.8 | Cold navigation to first `data-screen-hydrated="true"` stamp at 4x CPU throttle. |
| `widget_update` | 0 | — | — | — | Idle 30 s bench window without concurrent provider pushes. |

---

## 3. React UI Post-Migration Verification (`?ui=react`, #780)

Measured against the complete React display bundle (`web/static/ui/app.js`) after landing all DURIN'S CROWN (#765) widget ports (`#773`–`#779`, `#822`, `#825`).

- **Commit SHA**: `ca1e534` (`ca1e53483dfb2160873c498de68f4c15dcc6b76c`; initial React default cut at `199ec1f780d4fad258371aeda2f1fc8e6711db6e`)
- **Date**: `2026-11-01`
- **React JS bundle**: `118.4 KiB` (`121,194 bytes`) gzip (`422.6 KiB` / `432,734 bytes` raw single-file IIFE in `web/static/ui/app.js` at `ca1e534`; `118.3 KiB` / `121,155 bytes` at `199ec1f`; target `<= 150 KB` gzip — **PASS**)

### 3a. CI-Class Machine (Headless 4x CPU Throttle, 20 Swipes)

Command:

```sh
node scripts/perf/bench.mjs --url http://127.0.0.1:8080/display?ui=react&perf=1 --swipes 20 --cpu-throttle 4
```

| Metric | Count | p50 (ms) | p95 (ms) | Max (ms) | #765 Target | CI Budget (1.2x) | Status & Notes |
| --- | ---: | ---: | ---: | ---: | --- | --- | --- |
| `gesture_first_frame` | 20 | 14.0 | 14.7 | 15.2 | p95 `<= 17 ms` (one frame) | p95 `<= 20.4 ms` | **PASS** (p95 `14.7 ms <= 17 ms`; `.carousel-track` translates 1:1 with the finger on the first `pointermove`). |
| `swipe_settle` | 20 | 284.2 | 300.3 | 301.3 | p95 `<= 300 ms`, 0 network requests | p95 `<= 360 ms` | **MISS (#765 target) / PASS (CI budget)** (p95 `300.3 ms` at 4x CPU throttle and `303.6 ms` on kiosk exceed the `300 ms` #765 target by `0.3–3.6 ms`; `20/20` swipes settled with `0` network requests, bounded by `SETTLE_MS = 250 ms` CSS `transform 250ms ease-out` transition plus ~2–3 60 Hz frames for `rAF` start and `transitionend` dispatch). |
| `frame` | 284 | 16.7 | 16.8 | 33.4 | p95 `<= 16.7 ms`, max `<= 33 ms` | p95 `<= 20.04 ms`, max `<= 39.6 ms` | **MISS (#765 target) / PASS (CI budget)** (p95 `16.8 ms` and max `33.4 ms` exceed the `16.7 ms` / `33 ms` #765 target by `0.1 ms` / `0.4 ms` due to 60 Hz `rAF` timestamp quantization on 1x and 2x `16.67 ms` VSync ticks; `0` frames > 2 VSync intervals vs. `15.0%` in legacy). |
| `long_task` (during swipes) | 0 | — | — | — | `0` over `50 ms` | `0` during swipes | **PASS** (`0` long tasks across all 20 swipes; 4 cold-load tasks during initial bundle parse/hydration at 4x CPU throttle, `0` at 1x). |
| `interaction` | 600 | 48.0 | 56.0 | 64.0 | p95 `<= 100 ms` | p95 `<= 120 ms` | **PASS** (`56.0 ms` p95 `<= 100 ms` target). |
| `screen_ready` | 1 | 759.9 | 759.9 | 759.9 | p95 `<= 1.5 s` | p95 `<= 1800 ms` | **PASS** (`759.9 ms` at 4x CPU throttle; `102.2 ms` at 1x). |
| `widget_update` | 0 | — | — | — | p95 `<= 50 ms`, one component re-rendered | p95 `<= 60 ms` (`min_count: 0`) | **NOT MEASURED** (`perf.js` hooks `MirrormereCarousel.prototype.handleWidgetUpdate` DOM node replacement; React reconciles `widget.update` JSON in-place via the store so one component re-renders without HTML refetch or node swap, and `bench.mjs` does not emit synthetic SSE widget updates). |
| `app.js` gzip size | 1 | — | — | — | `<= 150 KB` gzip | `<= 153,600 B` | **PASS** (`118.4 KiB` / `121,194 B` at `ca1e534`; `118.3 KiB` / `121,155 B` at `199ec1f`). |

### 3b. 24-Hour Kiosk Soak Gate (#780 / #781)

- **Status**: **IN PROGRESS** — React default (`display.ui: "react"`) deployed to `mirrormere-core` on `2026-11-01` (`199ec1f` / `ca1e534`).
- **Acceptance Criteria before #781 (`remove the legacy display JS`)**: 24-hour soak on the touch kiosk with `0` reload loops in `system.status` logs and `< 20%` renderer memory growth.

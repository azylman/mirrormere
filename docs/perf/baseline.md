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

| Metric | Count | p50 (ms) | p95 (ms) | Max (ms) | `/metrics` Histogram Summary (`mirrormere_display_<metric>_ms{ui="legacy"}`) |
| --- | ---: | ---: | ---: | ---: | --- |
| `gesture_first_frame` | 46 | 14.2 | 15.5 | 15.5 | 46/46 live samples in `le="16.7"` bucket (mean `14.1 ms`). |
| `swipe_settle` | 45 | 204.1 | 235.8 | 240.8 | 7 in `le="200"`, 45/45 in `le="300"` (mean `213.6 ms`; 2–3 widget HTML refetches per swipe). |
| `frame` | 399 | 16.7 | 50.0 | 99.9 | 238 in `le="16.7"` (59.6%), 339 in `le="33"` (85.0%), 389 in `le="50"` (97.5%), 399 in `le="100"` (100%); 15.0% of transition frames exceed 33 ms. |
| `long_task` | 0 | — | — | — | 0 long tasks during steady-state rotation; 4 total across cold loads (`le="50"`: 1, `le="100"`: 4, sum `249.0 ms`). |
| `interaction` | 1036 | 24.0 | 32.0 | 128.0 | 334 in `le="16.7"`, 991 in `le="33"`, 1023 in `le="50"`, 1035 in `le="100"`, 1036 in `le="200"`. |
| `screen_ready` | 2 | 100.9 | 192.6 | 192.6 | 1 in `le="200"` (`100.9 ms` warm cache), 2 in `le="200"` (`192.6 ms` reload). |
| `widget_update` | 8 | 20.5 | 46.9 | 46.9 | 1 in `le="16.7"`, 6 in `le="33"`, 8/8 in `le="50"` (mean `27.0 ms`; full widget HTML refetch and DOM node replacement). |

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

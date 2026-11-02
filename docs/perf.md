# Display performance measurement

The display records client-side timings and ships them to the core server, which exports them
on `/metrics`. A bench script drives a headless Chrome through a fixed set of swipes and prints
a summary. Use both to compare changes against the same baseline.

## What is measured

All timings are milliseconds. The legacy and React displays both record them when the page is
loaded with `?perf=1` (`/display?perf=1` and `/display?ui=react&perf=1`). Each sample is tagged
`ui="react"` when the React bundle is in the page and `ui="legacy"` otherwise.

| Metric | Measures |
| --- | --- |
| `gesture_first_frame` | `touchstart` to the first animation frame after it. Main-thread responsiveness to touch. |
| `swipe_settle` | Legacy: `touchend` that starts a screen change (a navigation request) to the first frame after the new screen is hydrated and the transition class is removed. React: `touchend` or `pointerup` followed within 1 s by `body[data-animating]` becoming `1`, to the first frame after `data-animating` returns to `0` with the current `#grid-canvas` hydrated. The React carousel sends its navigation request after the settle, so the React start does not use the request. |
| `frame` | Gap between consecutive animation frames, sampled only while a screen transition is running. Around 16.7 means 60 fps. |
| `long_task` | Main-thread tasks of 50 ms or more (`PerformanceObserver` `longtask`). |
| `interaction` | Event Timing duration for input events slower than 16 ms. |
| `screen_ready` | Navigation start to the first hydrated screen. Not recorded if the screen was already hydrated before the instrumentation loaded. |
| `widget_update` | `widget.update` delivery to the first frame after the widget node is replaced. Recorded only when a node was actually replaced. |

The browser keeps the last 1000 samples per metric in memory and batches unsent samples to
`POST /api/perf` every 10 seconds, and again when the page is hidden or unloaded.

## Run the bench

Requirements: Node 22 or later, and a Chrome or Chromium binary. There are no npm dependencies.

```sh
node scripts/perf/bench.mjs --url http://localhost:8080/display
```

Flags:

| Flag | Default | Notes |
| --- | --- | --- |
| `--url` | required | Display page. `perf=1` is added to the query string. |
| `--chrome` | `$CHROME_BIN`, then `google-chrome` | Path to the Chrome executable. |
| `--cpu-throttle` | `4` | CPU slowdown factor, at least 1. Use `1` for a raw desktop run. |
| `--swipes` | `20` | Horizontal swipes. Even swipes go left (next), odd swipes go right (prev). |

The script launches Chrome headless at 1920x1080 with touch emulation, waits for
`data-screen-hydrated` on `#grid-canvas`, then sends each swipe as 300 px over 120 ms in 8 moves
with a 1 s gap. It prints `window.__mmPerf.snapshot()` as JSON on stdout, plus
`long_task_during_swipes`: the long tasks recorded between the first and last swipe, which leaves
out the cold-load tasks in the snapshot. Progress messages go to stderr, so the output can be
piped into a file.

For the React display, use `--url 'http://localhost:8080/display?ui=react'`.

`--swipe-x <px>` and `--swipe-y <px>` set the centre of each swipe (default: the viewport centre).
Before each swipe the bench reads the rects of the swipe-blocking elements and of the widgets in
`#grid-canvas`, and `scripts/perf/swipe-point.mjs` moves the centre off a blocking element onto the
widest widget that no blocker overlaps; if none qualifies, the centre stays where it was. The bench
prints `swipes_sent` and `swipes_settled` (the `swipe_settle` samples recorded during the swipes)
beside the snapshot and in its JSON. It exits 2 when `swipes_settled` is under 80% of `swipes_sent`,
which usually means the swipe centre is on a blocking widget.

## CI budget

The `perf` job in `.github/workflows/ci.yml` runs on pull requests that touch the React UI or
this harness. It starts the server from `deploy/examples/config.yaml`, runs the bench at 4x CPU
throttle with 20 swipes against `/display?ui=react`, and then runs
`scripts/perf/check-budget.mjs`:

```sh
node scripts/perf/bench.mjs --url 'http://localhost:8080/display?ui=react&perf=1' --cpu-throttle 4 --swipes 20 > bench.json
node scripts/perf/check-budget.mjs --bench bench.json --budget scripts/perf/budget.json --app-js web/static/ui/app.js
```

`budget.json` holds the #765 targets plus 20% headroom for CI noise. The gate also fails when
the gzipped `web/static/ui/app.js` is over its budget. `min_count` is the fewest samples a metric
must have, so a run that dropped swipes cannot pass on one sample. Metrics with `min_count: 0` are
reported as skipped when they have no samples; `widget_update` is one of them, because the React
display does not emit it.

To run the same server locally, build it and start it with the example config and the fixture
values the job uses. The example config reads these by name, and the fixture URL refuses
connections, so those providers fail fast and the screens still load from config:

```sh
(cd web/ui && npm ci && npm run build)
go build -o bin/server ./cmd/server
FAMILY_CALENDAR_URL=http://localhost:9/fixture.ics TODOIST_API_TOKEN=fixture-token \
  GOOGLE_PHOTOS_ALBUM_URL=http://localhost:9/fixture-photos \
  CONFIG_PATH=deploy/examples/config.yaml ./bin/server
```

Notes:

- `swipe_settle` needs at least two screens configured. With one screen, swipes do nothing.
- The carousel ignores swipes that start inside a calendar widget or an element marked
  `data-prevent-screen-swipe`, so those swipes produce no sample.
- React `swipe_settle` ends at the first frame after `body[data-animating]` returns to `0` with a
  hydrated `#grid-canvas`. That can be one frame before React commits the new pane. A cancelled
  drag (a rest settle) ends the same way and is counted, which the legacy path does not do.
- `widget_update` needs the server to push widget updates during the run. An idle run reports
  zero samples, with `null` percentiles. The key is still present.
- Chrome's history gesture on touch input would navigate back to `about:blank` mid-run. The
  script clears that history entry after the first hydration.

## Read the metrics

The core server exposes the histograms as `mirrormere_display_<metric>_ms{ui}`, with `ui`
set to `legacy` today and `react` for the React UI. The bucket upper bounds are 4, 8, 16.7, 33,
50, 100, 200, 300, 500, 1000, 2000 and 5000 ms.

```sh
curl -s http://localhost:8080/metrics | grep '^mirrormere_display_'
```

Useful PromQL:

```promql
# p95 swipe settle over 15 minutes, per UI
histogram_quantile(0.95, sum by (le, ui) (rate(mirrormere_display_swipe_settle_ms_bucket[15m])))

# share of frames slower than 33 ms (two missed frames)
1 - (sum(rate(mirrormere_display_frame_ms_bucket{le="33"}[15m]))
  / sum(rate(mirrormere_display_frame_ms_count[15m])))
```

`histogram_quantile` interpolates linearly inside a bucket, so percentiles read from `/metrics`
are approximate. The bench's p50/p95 are exact over the retained samples.

## Live inspection

Open the display with `?perf=1` and run in the devtools console:

```js
window.__mmPerf.snapshot()
```

This returns `{ count, p50, p95, max }` for each of the seven metrics, computed from the
in-memory samples of the current page.

## Endpoint contract

`POST /api/perf` (see `api/openapi.yaml`, `PerfBatchRequest`):

- Body: `{"samples":[{"metric":"swipe_settle","value_ms":42.5,"ui":"legacy"}]}`
- 1 to 200 samples per request. Bodies are capped at 64 KiB.
- `metric` must be one of the seven names above. `ui` must be `legacy` or `react`.
  `value_ms` must be a finite number of 0 or more.
- A batch is validated as a whole. Any invalid sample returns `400` and nothing is recorded.
- Success returns `204`. Other methods return `405`.

The payload carries timings only, no user data.

## Loading behaviour

`static/js/perf.js` is included only when the display page is requested with `?perf=1`, on both
`display.html` and `display_react.html`. Without the parameter, the page loads no perf script.
The legacy `carousel.js` and the React carousel both set `body.dataset.animating` during
transitions; perf.js reads it for `frame` and, in React mode, for `swipe_settle`.

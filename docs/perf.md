# Display performance measurement

The display records client-side timings and ships them to the core server, which exports them
on `/metrics`. A bench script drives a headless Chrome through a fixed set of swipes and prints
a summary. Use both to compare changes against the same baseline.

## What is measured

All timings are milliseconds. The legacy UI records them when the page is loaded with `?perf=1`.

| Metric | Measures |
| --- | --- |
| `gesture_first_frame` | `touchstart` to the first animation frame after it. Main-thread responsiveness to touch. |
| `swipe_settle` | `touchend` that starts a screen change (a navigation request) to the first frame after the new screen is hydrated and the transition class is removed. |
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
with a 1 s gap. It prints `window.__mmPerf.snapshot()` as JSON on stdout. Progress messages go to
stderr, so the output can be piped into a file.

Notes:

- `swipe_settle` needs at least two screens configured. With one screen, swipes do nothing.
- The carousel ignores swipes that start inside a calendar widget or an element marked
  `data-prevent-screen-swipe`, so those swipes produce no sample.
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

`static/js/perf.js` is included only when the display page is requested with `?perf=1`.
Without the parameter, the page loads no perf script. `carousel.js` still sets
`body.dataset.animating` during transitions, but nothing reads it.

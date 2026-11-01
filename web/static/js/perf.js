/**
 * Mirrormere display performance instrumentation (legacy and React UI).
 *
 * Loaded only when the page is requested with ?perf=1 (see display.html and
 * display_react.html). Records long tasks, Event Timing interactions, frame timing during
 * screen transitions, and gesture / screen / widget latency. Samples are flushed to POST api/perf every 10 s
 * and kept in memory for window.__mmPerf.snapshot() (p50 / p95 / max per metric).
 * Each sample is tagged 'react' when the React bundle is in the document, else 'legacy'.
 */
(function () {
  'use strict';

  const METRICS = [
    'gesture_first_frame',
    'swipe_settle',
    'frame',
    'long_task',
    'interaction',
    'screen_ready',
    'widget_update',
  ];
  const UI_SCRIPT_SELECTOR = 'script[src*="static/ui/app.js"]';
  const GRID_CANVAS_ID = 'grid-canvas';
  const MAX_BATCH = 200; // POST /api/perf accepts at most 200 samples per request
  const MAX_BUFFER = 2000; // unsent samples kept before the oldest are dropped
  const MAX_RETAINED = 1000; // per-metric samples kept for snapshot()
  const FLUSH_INTERVAL_MS = 10000;
  const SCREEN_NAV_RE = /(^|\/)api\/screen\/(advance|select)$/;
  const SWIPE_TOUCH_WINDOW_MS = 1000; // a navigation fetch this soon after touchend belongs to that touch
  const SWIPE_SETTLE_MAX_MS = 10000; // drop a pending swipe that never settled (e.g. paused carousel)

  /** Nearest-rank percentile of an unsorted numeric array; null when empty. */
  function percentile(values, q) {
    if (!values.length) return null;
    const sorted = values.slice().sort((a, b) => a - b);
    const rank = Math.ceil((q / 100) * sorted.length);
    return sorted[Math.min(sorted.length - 1, Math.max(0, rank - 1))];
  }

  function round1(v) {
    return v === null ? null : Math.round(v * 10) / 10;
  }

  /** { count, p50, p95, max } for an unsorted numeric array. */
  function summarize(values) {
    if (!values.length) return { count: 0, p50: null, p95: null, max: null };
    let max = values[0];
    for (const v of values) {
      if (v > max) max = v;
    }
    return {
      count: values.length,
      p50: round1(percentile(values, 50)),
      p95: round1(percentile(values, 95)),
      max: round1(max),
    };
  }

  /**
   * Returns a function that reports 'react' once the React bundle's script tag is in the
   * document and 'legacy' before that. perf.js runs before that tag is parsed, so the
   * check happens per call; the 'react' answer is kept once it is found.
   */
  function createUiDetector(doc) {
    let detected = null;
    return function uiOf() {
      if (detected === null && doc && typeof doc.querySelector === 'function' && doc.querySelector(UI_SCRIPT_SELECTOR)) {
        detected = 'react';
      }
      return detected || 'legacy';
    };
  }

  /** Whether a fetch URL is a screen navigation request (advance or select). */
  function isScreenNavigation(url) {
    return typeof url === 'string' && SCREEN_NAV_RE.test(url.replace(/[?#].*$/, ''));
  }

  /**
   * Sample buffer with retained per-metric history. `send(body)` posts one JSON
   * body and returns whether it was accepted for delivery. `uiOf()` gives the value
   * each sample is tagged with.
   */
  function createRecorder(send, uiOf) {
    const ui = uiOf || (() => 'legacy');
    const retained = {};
    for (const m of METRICS) retained[m] = [];
    let pending = [];

    return {
      record(metric, valueMs) {
        if (METRICS.indexOf(metric) < 0 || typeof valueMs !== 'number' || !isFinite(valueMs) || valueMs < 0) {
          return false;
        }
        const history = retained[metric];
        history.push(valueMs);
        if (history.length > MAX_RETAINED) history.splice(0, history.length - MAX_RETAINED);
        pending.push({ metric, value_ms: valueMs, ui: ui() });
        if (pending.length > MAX_BUFFER) pending = pending.slice(pending.length - MAX_BUFFER);
        return true;
      },
      snapshot() {
        const out = {};
        for (const m of METRICS) out[m] = summarize(retained[m]);
        return out;
      },
      pendingCount() {
        return pending.length;
      },
      /** Sends pending samples in batches of at most MAX_BATCH. Returns the number of requests sent. */
      flush() {
        let sent = 0;
        while (pending.length) {
          const batch = pending.splice(0, MAX_BATCH);
          send(JSON.stringify({ samples: batch }));
          sent++;
        }
        return sent;
      },
    };
  }

  const api = {
    METRICS,
    MAX_BATCH,
    percentile,
    summarize,
    isScreenNavigation,
    createUiDetector,
    createRecorder,
  };

  if (typeof module !== 'undefined' && module.exports) {
    module.exports = api;
  }

  function sendBeacon(body) {
    if (navigator.sendBeacon && navigator.sendBeacon('api/perf', body)) return;
    fetch('api/perf', { method: 'POST', keepalive: true, body }).catch(() => {});
  }

  function observeEntries(type, extra, onEntry) {
    if (typeof PerformanceObserver !== 'function') return;
    try {
      const observer = new PerformanceObserver((list) => {
        for (const entry of list.getEntries()) onEntry(entry);
      });
      observer.observe(Object.assign({ type, buffered: true }, extra));
    } catch (_) {
      // Entry type unsupported by this browser; that metric stays empty.
    }
  }

  function install(recorder, uiOf) {
    const body = document.body;
    // #grid-canvas moves between panes in the React carousel, so it is looked up at each use.
    const canvasNow = () => document.getElementById(GRID_CANVAS_ID);
    const isReact = () => uiOf() === 'react';
    const isAnimating = () => body.dataset.animating === '1';

    window.__mmPerf = { snapshot: () => recorder.snapshot() };

    observeEntries('longtask', {}, (e) => recorder.record('long_task', e.duration));
    observeEntries('event', { durationThreshold: 16 }, (e) => recorder.record('interaction', e.duration));

    // gesture_first_frame: touchstart to the first frame painted after it.
    window.addEventListener('touchstart', () => {
      const t0 = performance.now();
      requestAnimationFrame(() => recorder.record('gesture_first_frame', performance.now() - t0));
    }, { passive: true, capture: true });

    // frame: rAF deltas, sampled only while the page flags a transition (body data-animating).
    let frameRunning = false;
    let lastFrame = null;
    const frameTick = (ts) => {
      if (!isAnimating()) {
        frameRunning = false;
        lastFrame = null;
        return;
      }
      if (lastFrame !== null) recorder.record('frame', ts - lastFrame);
      lastFrame = ts;
      requestAnimationFrame(frameTick);
    };

    // swipe_settle. Legacy: a touchend followed by a screen navigation fetch starts it, and
    // the class-based end is checked below. React: a touchend or pointerup followed by
    // data-animating becoming '1' starts it (the navigation fetch comes after the settle, so
    // it cannot). The end is the first frame after data-animating returns to '0' with the
    // current #grid-canvas hydrated.
    let touchEndAt = null; // legacy: matched against the navigation fetch
    let gestureEndAt = null; // React: matched against data-animating becoming '1'
    let swipeStart = null;

    const checkReactSettle = () => {
      if (swipeStart === null || isAnimating()) return;
      const canvas = canvasNow();
      if (canvas && canvas.dataset.screenHydrated === 'true') {
        const t0 = swipeStart;
        swipeStart = null;
        if (performance.now() - t0 < SWIPE_SETTLE_MAX_MS) recorder.record('swipe_settle', performance.now() - t0);
        return;
      }
      if (performance.now() - swipeStart >= SWIPE_SETTLE_MAX_MS) {
        swipeStart = null;
        return;
      }
      requestAnimationFrame(checkReactSettle);
    };

    new MutationObserver(() => {
      const animating = isAnimating();
      if (animating && !frameRunning) {
        frameRunning = true;
        lastFrame = null;
        requestAnimationFrame(frameTick);
      }
      if (animating) {
        if (gestureEndAt !== null) {
          const t0 = gestureEndAt;
          gestureEndAt = null;
          if (isReact() && performance.now() - t0 < SWIPE_TOUCH_WINDOW_MS) swipeStart = t0;
        }
      } else if (swipeStart !== null && isReact()) {
        requestAnimationFrame(checkReactSettle);
      }
    }).observe(body, { attributes: true, attributeFilter: ['data-animating'] });

    window.addEventListener('touchend', () => {
      touchEndAt = performance.now();
      gestureEndAt = touchEndAt;
    }, { passive: true, capture: true });
    window.addEventListener('pointerup', () => {
      gestureEndAt = performance.now();
    }, { passive: true, capture: true });

    const nativeFetch = window.fetch;
    if (typeof nativeFetch === 'function') {
      window.fetch = function (input, init) {
        const url = typeof input === 'string' ? input : (input && input.url) || '';
        if (touchEndAt !== null && performance.now() - touchEndAt < SWIPE_TOUCH_WINDOW_MS && isScreenNavigation(url) && !isReact()) {
          swipeStart = touchEndAt;
          touchEndAt = null;
        }
        return nativeFetch.apply(window, arguments);
      };
    }

    // screen_ready: navigation start to the first data-screen-hydrated stamp on #grid-canvas.
    // A page that was already hydrated when this script ran has no usable stamp time, so it
    // is not recorded. Legacy swipe_settle end: the canvas is hydrated and not transitioning.
    const hydratedNow = () => {
      const canvas = canvasNow();
      return canvas !== null && canvas.dataset.screenHydrated === 'true';
    };
    let screenReadyDone = hydratedNow();
    new MutationObserver(() => {
      if (!screenReadyDone && hydratedNow()) {
        screenReadyDone = true;
        recorder.record('screen_ready', performance.now());
      }
      if (swipeStart !== null && !isReact()) {
        const canvas = canvasNow();
        const settled = hydratedNow() && !canvas.classList.contains('transitioning');
        if (settled) {
          const t0 = swipeStart;
          swipeStart = null;
          if (performance.now() - t0 < SWIPE_SETTLE_MAX_MS) {
            requestAnimationFrame(() => recorder.record('swipe_settle', performance.now() - t0));
          }
        }
      }
    }).observe(body, { attributes: true, subtree: true, attributeFilter: ['data-screen-hydrated', 'class', 'id'] });

    // widget_update: widget.update delivery (handleWidgetUpdate call) to the first frame after
    // the widget node has been replaced. Recorded only when a replacement actually happened.
    const Carousel = window.MirrormereCarousel;
    if (Carousel && Carousel.prototype && typeof Carousel.prototype.handleWidgetUpdate === 'function') {
      const original = Carousel.prototype.handleWidgetUpdate;
      const findWidget = (root, id) => {
        if (!root || !id) return null;
        return Array.from(root.querySelectorAll('[data-widget-id]')).find((el) => el.dataset.widgetId === id) || null;
      };
      Carousel.prototype.handleWidgetUpdate = function (data) {
        const t0 = performance.now();
        const id = data && data.widget_id;
        const before = findWidget(this.canvas, id);
        return Promise.resolve(original.apply(this, arguments)).then((result) => {
          const after = findWidget(this.canvas, id);
          if (before && after && after !== before) {
            requestAnimationFrame(() => recorder.record('widget_update', performance.now() - t0));
          }
          return result;
        });
      };
    }

    setInterval(() => recorder.flush(), FLUSH_INTERVAL_MS);
    const flushIfHidden = () => {
      if (document.visibilityState === 'hidden') recorder.flush();
    };
    document.addEventListener('visibilitychange', flushIfHidden);
    window.addEventListener('pagehide', () => recorder.flush());
  }

  if (typeof window !== 'undefined' && typeof document !== 'undefined' && document.body) {
    const uiOf = createUiDetector(document);
    install(createRecorder(sendBeacon, uiOf), uiOf);
  }
})();

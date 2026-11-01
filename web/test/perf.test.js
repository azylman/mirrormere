const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const perfPath = path.join(__dirname, '../static/js/perf.js');
const perf = require(perfPath);

test('percentile uses nearest-rank and handles empty input', () => {
  const values = [9, 1, 5, 3, 7, 2, 8, 4, 6, 10];
  assert.equal(perf.percentile(values, 50), 5);
  assert.equal(perf.percentile(values, 95), 10);
  assert.equal(perf.percentile(values, 100), 10);
  assert.equal(perf.percentile([42], 50), 42);
  assert.equal(perf.percentile([42], 95), 42);
  assert.equal(perf.percentile([], 50), null);
  // Input array is not reordered.
  assert.deepEqual(values, [9, 1, 5, 3, 7, 2, 8, 4, 6, 10]);
});

test('summarize returns count, p50, p95 and max rounded to one decimal', () => {
  assert.deepEqual(perf.summarize([]), { count: 0, p50: null, p95: null, max: null });
  assert.deepEqual(perf.summarize([16.66, 33.31, 8.04]), { count: 3, p50: 16.7, p95: 33.3, max: 33.3 });
});

test('isScreenNavigation matches only advance and select endpoints', () => {
  assert.equal(perf.isScreenNavigation('api/screen/advance'), true);
  assert.equal(perf.isScreenNavigation('/api/screen/select?x=1'), true);
  assert.equal(perf.isScreenNavigation('api/screen/pause'), false);
  assert.equal(perf.isScreenNavigation('api/widgets/clock/render'), false);
  assert.equal(perf.isScreenNavigation(undefined), false);
});

test('recorder rejects unknown metrics and invalid values', () => {
  const rec = perf.createRecorder(() => {});
  assert.equal(rec.record('constructor', 1), false);
  assert.equal(rec.record('__proto__', 1), false);
  assert.equal(rec.record('not_a_metric', 1), false);
  assert.equal(rec.record('frame', -1), false);
  assert.equal(rec.record('frame', NaN), false);
  assert.equal(rec.record('frame', Infinity), false);
  assert.equal(rec.record('frame', '16'), false);
  assert.equal(rec.pendingCount(), 0);
  assert.equal(rec.record('frame', 16.7), true);
  assert.equal(rec.pendingCount(), 1);
});

test('snapshot reports all seven metrics, empty ones with zero count', () => {
  const rec = perf.createRecorder(() => {});
  rec.record('long_task', 80);
  rec.record('long_task', 120);
  const snap = rec.snapshot();
  assert.deepEqual(Object.keys(snap).sort(), [...perf.METRICS].sort());
  assert.equal(perf.METRICS.length, 7);
  assert.deepEqual(snap.long_task, { count: 2, p50: 80, p95: 120, max: 120 });
  assert.deepEqual(snap.frame, { count: 0, p50: null, p95: null, max: null });
});

test('flush batches pending samples into requests of at most 200', () => {
  const bodies = [];
  const rec = perf.createRecorder((body) => bodies.push(body));
  for (let i = 0; i < 450; i++) rec.record('interaction', i);

  assert.equal(rec.flush(), 3);
  assert.deepEqual(bodies.map((b) => JSON.parse(b).samples.length), [200, 200, 50]);

  const first = JSON.parse(bodies[0]).samples[0];
  assert.deepEqual(first, { metric: 'interaction', value_ms: 0, ui: 'legacy' });
  const last = JSON.parse(bodies[2]).samples.at(-1);
  assert.equal(last.value_ms, 449);

  // Flushed samples are gone; an empty flush sends nothing.
  assert.equal(rec.pendingCount(), 0);
  assert.equal(rec.flush(), 0);
  assert.equal(bodies.length, 3);
});

test('pending buffer drops the oldest samples past its cap', () => {
  const bodies = [];
  const rec = perf.createRecorder((body) => bodies.push(body));
  for (let i = 0; i < 2105; i++) rec.record('frame', i);
  assert.equal(rec.pendingCount(), 2000);
  rec.flush();
  const sent = bodies.flatMap((b) => JSON.parse(b).samples.map((s) => s.value_ms));
  assert.equal(sent[0], 105);
  assert.equal(sent.at(-1), 2104);
});

test('snapshot history is bounded per metric', () => {
  const rec = perf.createRecorder(() => {});
  for (let i = 0; i < 1200; i++) rec.record('swipe_settle', i);
  const snap = rec.snapshot();
  assert.equal(snap.swipe_settle.count, 1000);
  assert.equal(snap.swipe_settle.max, 1199);
});

test('browser wiring: long tasks reach the beacon and snapshot on the 10 s flush', () => {
  const source = fs.readFileSync(perfPath, 'utf8');
  const beacons = [];
  let intervalFn = null;
  let clock = 0;
  const observerCallbacks = {};
  const body = { dataset: { animating: '0' } };
  const canvas = { dataset: {}, classList: { contains: () => false }, querySelectorAll: () => [] };

  class FakeObserver {
    constructor(cb) { this.cb = cb; }
    observe() {}
  }
  class FakePerformanceObserver {
    constructor(cb) { this.cb = cb; }
    observe(options) { observerCallbacks[options.type] = this.cb; }
  }

  const win = {
    addEventListener() {},
    fetch: () => Promise.reject(new Error('no network')),
  };
  const doc = {
    body,
    visibilityState: 'visible',
    getElementById: (id) => (id === 'grid-canvas' ? canvas : null),
    addEventListener() {},
  };
  const nav = { sendBeacon(url, payload) { beacons.push({ url, payload }); return true; } };
  const fn = new Function(
    'window', 'document', 'navigator', 'performance', 'requestAnimationFrame', 'MutationObserver',
    'PerformanceObserver', 'setInterval', 'module', source,
  );
  fn(
    win, doc, nav, { now: () => clock }, () => {}, FakeObserver, FakePerformanceObserver,
    (cb) => { intervalFn = cb; return 1; }, undefined,
  );

  assert.equal(typeof win.__mmPerf.snapshot, 'function');
  assert.equal(typeof observerCallbacks.longtask, 'function');
  assert.equal(typeof observerCallbacks.event, 'function');

  observerCallbacks.longtask({ getEntries: () => [{ duration: 80 }, { duration: 150 }] });
  assert.equal(beacons.length, 0, 'nothing is sent before the flush interval');

  intervalFn();
  assert.equal(beacons.length, 1);
  assert.equal(beacons[0].url, 'api/perf');
  const sent = JSON.parse(beacons[0].payload).samples;
  assert.deepEqual(sent, [
    { metric: 'long_task', value_ms: 80, ui: 'legacy' },
    { metric: 'long_task', value_ms: 150, ui: 'legacy' },
  ]);
  assert.equal(win.__mmPerf.snapshot().long_task.count, 2);

  intervalFn();
  assert.equal(beacons.length, 1, 'an empty interval sends nothing');
});

test('browser wiring: widget_update is recorded only when the node is replaced', async () => {
  const source = fs.readFileSync(perfPath, 'utf8');
  const frames = [];
  const beacons = [];
  let clock = 100;

  const original = { dataset: { widgetId: 'clock' } };
  const canvas = {
    dataset: {},
    classList: { contains: () => false },
    children: [original],
    querySelectorAll() { return this.children; },
  };
  const Carousel = {
    prototype: {
      async handleWidgetUpdate(data) {
        clock += 30;
        if (data.replace) {
          this.canvas.children = [{ dataset: { widgetId: 'clock' } }];
        }
      },
    },
  };
  class FakeObserver { constructor() {} observe() {} }
  const win = { addEventListener() {}, fetch: () => Promise.reject(new Error('x')), MirrormereCarousel: Carousel };
  const doc = {
    body: { dataset: { animating: '0' } },
    visibilityState: 'visible',
    getElementById: () => canvas,
    addEventListener() {},
  };
  const fn = new Function(
    'window', 'document', 'navigator', 'performance', 'requestAnimationFrame', 'MutationObserver',
    'PerformanceObserver', 'setInterval', 'module', source,
  );
  fn(
    win, doc, { sendBeacon(u, p) { beacons.push(JSON.parse(p)); return true; } }, { now: () => clock },
    (cb) => frames.push(cb), FakeObserver, undefined, () => 1, undefined,
  );
  const runFrames = () => { while (frames.length) frames.shift()(clock); };

  const instance = Object.create(Carousel.prototype);
  instance.canvas = canvas;

  // Unchanged node: no sample.
  await instance.handleWidgetUpdate({ widget_id: 'clock', replace: false });
  runFrames();
  assert.equal(win.__mmPerf.snapshot().widget_update.count, 0);

  // Replaced node: one sample, measured to the frame after the swap.
  await instance.handleWidgetUpdate({ widget_id: 'clock', replace: true });
  runFrames();
  const snap = win.__mmPerf.snapshot().widget_update;
  assert.equal(snap.count, 1);
  assert.equal(snap.max, 30);
  assert.equal(beacons.length, 0);
});

// Browser wiring for the React page (#779). The fake document has a querySelector for the
// app.js script tag, a mutable #grid-canvas (the React carousel moves the id between panes),
// and mutation observers that run only when the test fires them for an attribute it changed.
function harness({ react = false } = {}) {
  const source = fs.readFileSync(perfPath, 'utf8');
  const beacons = [];
  const frames = [];
  const observers = [];
  const listeners = {};
  const fetchCalls = [];
  const intervals = [];
  const APP_SCRIPT = 'script[src*="static/ui/app.js"]';
  let clock = 1000;

  const makePane = (hydrated, classes = []) => {
    const classSet = new Set(classes);
    return {
      dataset: hydrated ? { screenHydrated: 'true' } : {},
      classList: { contains: (c) => classSet.has(c), set: classSet },
    };
  };
  const body = { dataset: { animating: '0' } };
  const doc = {
    body,
    visibilityState: 'visible',
    scripts: react ? [{ src: 'static/ui/app.js' }] : [],
    current: makePane(false),
    getElementById: (id) => (id === 'grid-canvas' ? doc.current : null),
    querySelector: (sel) => (sel === APP_SCRIPT && doc.scripts.length ? doc.scripts[0] : null),
    addEventListener() {},
  };

  class FakeMutationObserver {
    constructor(cb) { this.cb = cb; observers.push(this); }
    observe(target, options) { this.target = target; this.options = options; }
  }
  class FakePerformanceObserver {
    observe() {}
  }
  const win = {
    addEventListener(type, fn) { (listeners[type] = listeners[type] || []).push(fn); },
    fetch(input) { fetchCalls.push(input); return Promise.resolve({ ok: true }); },
  };
  const nav = { sendBeacon(url, payload) { beacons.push(JSON.parse(payload)); return true; } };
  const fn = new Function(
    'window', 'document', 'navigator', 'performance', 'requestAnimationFrame', 'MutationObserver',
    'PerformanceObserver', 'setInterval', 'module', source,
  );
  fn(
    win, doc, nav, { now: () => clock }, (cb) => frames.push(cb), FakeMutationObserver,
    FakePerformanceObserver, (cb) => { intervals.push(cb); return 1; }, undefined,
  );

  const fire = (attr) => {
    for (const o of observers) {
      if (o.options.attributeFilter && o.options.attributeFilter.includes(attr)) o.cb([]);
    }
  };
  const runFrames = () => {
    for (let i = 0; i < 50 && frames.length; i++) frames.shift()(clock);
  };
  return {
    win,
    doc,
    beacons,
    fetchCalls,
    snapshot: () => win.__mmPerf.snapshot(),
    flush: () => { for (const cb of intervals) cb(); },
    pendingFrames: () => frames.length,
    at(ms) { clock = ms; },
    dispatch(type) { for (const fn of listeners[type] || []) fn({}); },
    setAnimating(value) { body.dataset.animating = value; fire('data-animating'); },
    setCanvas(pane) { doc.current = pane; fire('id'); fire('data-screen-hydrated'); },
    setHydrated(hydrated) {
      if (hydrated) doc.current.dataset.screenHydrated = 'true';
      else delete doc.current.dataset.screenHydrated;
      fire('data-screen-hydrated');
    },
    runFrames,
    makePane,
  };
}

test('ui detection: the detector reports legacy until the React script is present, then react for good', () => {
  const doc = { scripts: [], querySelector: (sel) => (sel === 'script[src*="static/ui/app.js"]' && doc.scripts.length ? doc.scripts[0] : null) };
  const uiOf = perf.createUiDetector(doc);
  assert.equal(uiOf(), 'legacy');
  doc.scripts.push({ src: 'static/ui/app.js' });
  assert.equal(uiOf(), 'react');
  doc.scripts.length = 0;
  assert.equal(uiOf(), 'react');
  assert.equal(perf.createUiDetector({ querySelector: () => null })(), 'legacy');
  assert.equal(perf.createUiDetector({})(), 'legacy');
  // createRecorder without a detector tags legacy, as before.
  const samples = [];
  const rec = perf.createRecorder((b) => samples.push(JSON.parse(b)));
  rec.record('frame', 16);
  rec.flush();
  assert.equal(samples[0].samples[0].ui, 'legacy');
});

test('ui detection: browser samples carry react only once the React script is in the document', () => {
  const h = harness({ react: false });
  h.at(1000);
  h.dispatch('touchstart');
  h.runFrames(); // recorded while only the legacy page is present
  h.doc.scripts.push({ src: 'static/ui/app.js' }); // app.js is parsed later in the page
  h.dispatch('touchstart');
  h.runFrames();
  h.flush();
  assert.deepEqual(h.beacons.flatMap((b) => b.samples.map((s) => s.ui)), ['legacy', 'react']);
});

test('canvas lookup: #grid-canvas is resolved at each use, so a pane that becomes current is seen', () => {
  const h = harness({ react: true });
  const neighbour = h.makePane(false);
  h.at(1000);
  h.doc.current = h.makePane(false); // React mounts; no current pane hydrated yet
  h.setCanvas(neighbour); // the carousel moves #grid-canvas onto a pane that was not hydrated
  h.setHydrated(true);
  assert.equal(h.snapshot().screen_ready.count, 1);
  // Hydration is recorded once; a later change of current pane does not record it again.
  h.setCanvas(h.makePane(true));
  assert.equal(h.snapshot().screen_ready.count, 1);
});

test('react swipe_settle: starts at touchend followed by animating=1, ends on the first frame after animating=0 with a hydrated current canvas', () => {
  const h = harness({ react: true });
  h.at(1000);
  h.setCanvas(h.makePane(true)); // the current pane before the swipe
  h.dispatch('pointerup');
  h.dispatch('touchend');
  h.at(1010);
  h.setAnimating('1');
  h.at(1200);
  // The React carousel's navigation fetch comes after the settle, so it must not start one.
  h.win.fetch('api/screen/select');
  assert.equal(h.snapshot().swipe_settle.count, 0);

  // The track settles; React commits the new current pane in the same frame window.
  h.at(1260);
  h.setAnimating('0');
  h.setCanvas(h.makePane(true)); // new current pane, hydrated
  h.at(1276);
  h.runFrames();
  const snap = h.snapshot().swipe_settle;
  assert.equal(snap.count, 1);
  assert.equal(snap.p50, 276);
  assert.equal(h.snapshot().swipe_settle.max, 276);
});

test('react swipe_settle: a settle that ends before the current pane is hydrated waits for the stamp', () => {
  const h = harness({ react: true });
  h.at(1000);
  h.setCanvas(h.makePane(false)); // a freshly mounted current pane, not hydrated yet
  h.dispatch('touchend');
  h.at(1005);
  h.setAnimating('1');
  h.at(1250);
  h.setAnimating('0');
  h.at(1260);
  h.runFrames(); // no hydrated current pane yet: no sample, and the check is requeued
  assert.equal(h.pendingFrames(), 1);
  assert.equal(h.snapshot().swipe_settle.count, 0);
  h.setCanvas(h.makePane(false));
  h.setHydrated(true);
  h.at(1300);
  h.runFrames();
  assert.equal(h.snapshot().swipe_settle.count, 1);
  assert.equal(h.snapshot().swipe_settle.p50, 300);
  assert.equal(h.pendingFrames(), 0);
});

test('react swipe_settle: a tap that never animates does not start a settle', () => {
  const h = harness({ react: true });
  h.at(1000);
  h.setCanvas(h.makePane(true));
  h.dispatch('touchend');
  h.at(2500); // outside SWIPE_TOUCH_WINDOW_MS
  h.setAnimating('1');
  h.at(2700);
  h.setAnimating('0');
  h.runFrames();
  assert.equal(h.snapshot().swipe_settle.count, 0);
});

test('react swipe_settle: pending swipes that never settle are dropped after SWIPE_SETTLE_MAX_MS', () => {
  const h = harness({ react: true });
  h.at(1000);
  h.setCanvas(h.makePane(false));
  h.dispatch('touchend');
  h.at(1010);
  h.setAnimating('1');
  h.at(1100);
  h.setAnimating('0'); // never hydrates
  for (let t = 1100; t < 12000; t += 16) {
    h.at(t);
    h.runFrames();
  }
  assert.equal(h.snapshot().swipe_settle.count, 0);
  assert.equal(h.pendingFrames(), 0, 'the frame check stops re-queuing past SWIPE_SETTLE_MAX_MS');
});

test('legacy swipe_settle is unchanged: navigation fetch starts it, the canvas class ends it', () => {
  const h = harness({ react: false });
  h.at(1000);
  const canvas = h.makePane(true, ['transitioning']);
  h.doc.current = canvas;
  h.dispatch('touchend');
  h.at(1100);
  h.win.fetch('api/screen/advance');
  h.at(1150);
  canvas.classList.set.delete('transitioning');
  h.setCanvas(canvas);
  h.runFrames();
  assert.equal(h.snapshot().swipe_settle.count, 1);
  assert.equal(h.snapshot().swipe_settle.p50, 150);
});

test('legacy ignores pointerup and animating for swipe_settle', () => {
  const h = harness({ react: false });
  h.at(1000);
  h.setCanvas(h.makePane(true));
  h.dispatch('pointerup');
  h.dispatch('touchend');
  h.at(1010);
  h.setAnimating('1');
  h.at(1200);
  h.setAnimating('0');
  h.runFrames();
  assert.equal(h.snapshot().swipe_settle.count, 0);
});

const test = require('node:test');
const assert = require('node:assert/strict');

const { WaveformMouth } = require('../static/js/waveform.js');

/**
 * Lightweight mock DOM element for hermetic Node.js testing.
 */
function createMockElement(tagName = 'div') {
  const attributes = new Map();
  const children = [];
  const classes = new Set();
  return {
    tagName: tagName.toUpperCase(),
    children,
    parentNode: null,
    classList: {
      contains: (c) => classes.has(c),
      add: (...cls) => cls.forEach((c) => classes.add(c)),
      remove: (...cls) => cls.forEach((c) => classes.delete(c)),
      toggle(c, force) {
        const on = force === undefined ? !classes.has(c) : !!force;
        if (on) classes.add(c);
        else classes.delete(c);
        return on;
      },
    },
    setAttribute(name, value) {
      attributes.set(name, String(value));
      if (name === 'class') value.split(/\s+/).filter(Boolean).forEach((c) => classes.add(c));
    },
    getAttribute(name) {
      return attributes.has(name) ? attributes.get(name) : null;
    },
    appendChild(child) {
      child.parentNode = this;
      children.push(child);
      return child;
    },
  };
}

function createMockDocument() {
  const body = createMockElement('body');
  return {
    body,
    createElementNS(_ns, tag) {
      return createMockElement(tag);
    },
  };
}

/** Fake EventSource factory: records the URL and lets the test push frame events. */
function createFakeEventSourceFactory() {
  const sources = [];
  const factory = (url) => {
    const handlers = new Map();
    const source = {
      url,
      addEventListener(type, fn) {
        handlers.set(type, fn);
      },
      emit(type, data) {
        const fn = handlers.get(type);
        if (fn) fn({ data: typeof data === 'string' ? data : JSON.stringify(data) });
      },
    };
    sources.push(source);
    return source;
  };
  factory.sources = sources;
  return factory;
}

/** Fake sseClient exposing on(eventType, callback) and a way to emit voice.state. */
function createFakeSSE() {
  const handlers = new Map();
  return {
    on(type, fn) {
      handlers.set(type, fn);
    },
    emit(type, data) {
      const fn = handlers.get(type);
      if (fn) fn(data);
    },
    hasHandler(type) {
      return handlers.has(type);
    },
  };
}

/** Fake clock and single-slot raf: runFrame(now) runs the pending animation callback. */
function createFakeTiming() {
  const timing = {
    t: 1000,
    pending: null,
    clock: () => timing.t,
    raf: (cb) => {
      timing.pending = cb;
      return 1;
    },
    caf: () => {
      timing.pending = null;
    },
    runFrame(now = timing.t) {
      const cb = timing.pending;
      timing.pending = null;
      if (cb) cb(now);
    },
  };
  return timing;
}

const LOUD = new Array(33).fill(100);
const QUIET = new Array(33).fill(0);

function makeMouth(overrides = {}) {
  const doc = createMockDocument();
  const sse = createFakeSSE();
  const factory = createFakeEventSourceFactory();
  const timing = createFakeTiming();
  const mouth = new WaveformMouth({
    sseClient: sse,
    document: doc,
    levelsUrl: 'http://127.0.0.1:8765/levels',
    gain: 2.2,
    eventSourceFactory: factory,
    clock: timing.clock,
    raf: timing.raf,
    caf: timing.caf,
    reducedMotion: false,
    ...overrides,
  });
  const svg = doc.body.children[0];
  const polyline = svg ? svg.children[0] : null;
  return { mouth, doc, sse, factory, timing, svg, polyline };
}

test('waveform mouth', async (t) => {
  await t.test('no levelsUrl: no DOM, no sseClient.on, no EventSource', () => {
    const doc = createMockDocument();
    const sse = createFakeSSE();
    let factoryCalls = 0;
    const mouth = new WaveformMouth({
      sseClient: sse,
      document: doc,
      levelsUrl: '',
      eventSourceFactory: () => {
        factoryCalls += 1;
        return null;
      },
    });
    assert.equal(mouth.element, null);
    assert.equal(doc.body.children.length, 0);
    assert.equal(sse.hasHandler('voice.state'), false);
    assert.equal(factoryCalls, 0);
  });

  await t.test('mounts an aria-hidden svg with one polyline and opens the levels stream', () => {
    const { svg, polyline, factory, sse } = makeMouth();
    assert.ok(svg, 'svg appended to body');
    assert.equal(svg.getAttribute('aria-hidden'), 'true');
    assert.equal(svg.getAttribute('viewBox'), '0 0 100 100');
    assert.equal(svg.classList.contains('mm-voice-waveform'), true);
    assert.equal(polyline.classList.contains('mm-voice-waveform__line'), true);
    assert.equal(factory.sources.length, 1);
    assert.equal(factory.sources[0].url, 'http://127.0.0.1:8765/levels');
    assert.equal(sse.hasHandler('voice.state'), true);
  });

  await t.test('frame + speaking shows the mouth and sets polyline points', () => {
    const { svg, polyline, factory, sse, timing } = makeMouth();
    sse.emit('voice.state', { state: 'speaking' });
    factory.sources[0].emit('frame', LOUD);
    timing.runFrame();
    assert.equal(svg.classList.contains('is-active'), true);
    const pairs = polyline.getAttribute('points').split(' ');
    assert.equal(pairs.length, 33);
    assert.equal(pairs[0], '20.00,28.00');
  });

  await t.test('speaking without a frame stays hidden', () => {
    const { svg, sse } = makeMouth();
    sse.emit('voice.state', { state: 'speaking' });
    assert.equal(svg.classList.contains('is-active'), false);
  });

  await t.test('frame older than 400 ms hides the mouth', () => {
    const { svg, factory, sse, timing } = makeMouth();
    sse.emit('voice.state', { state: 'speaking' });
    factory.sources[0].emit('frame', LOUD);
    assert.equal(svg.classList.contains('is-active'), true);
    timing.t += 400;
    timing.runFrame();
    assert.equal(svg.classList.contains('is-active'), true, 'exactly 400 ms is still fresh');
    timing.t += 1;
    timing.runFrame();
    assert.equal(svg.classList.contains('is-active'), false);
  });

  await t.test('non-speaking state hides the mouth even with a fresh frame', () => {
    const { svg, factory, sse } = makeMouth();
    sse.emit('voice.state', { state: 'speaking' });
    factory.sources[0].emit('frame', LOUD);
    assert.equal(svg.classList.contains('is-active'), true);
    sse.emit('voice.state', { state: 'thinking' });
    assert.equal(svg.classList.contains('is-active'), false);
    factory.sources[0].emit('frame', LOUD);
    assert.equal(svg.classList.contains('is-active'), false);
  });

  await t.test('wrong-length or non-numeric frames are ignored', () => {
    const { svg, factory, sse } = makeMouth();
    sse.emit('voice.state', { state: 'speaking' });
    factory.sources[0].emit('frame', new Array(32).fill(100));
    factory.sources[0].emit('frame', ['a'].concat(new Array(32).fill(0)));
    factory.sources[0].emit('frame', 'not json{');
    assert.equal(svg.classList.contains('is-active'), false);
  });

  await t.test('reduced motion draws the latest frame without easing', () => {
    const { polyline, factory, sse, timing } = makeMouth({ reducedMotion: true });
    sse.emit('voice.state', { state: 'speaking' });
    factory.sources[0].emit('frame', QUIET);
    timing.runFrame(1000);
    factory.sources[0].emit('frame', LOUD);
    timing.runFrame(1016);
    assert.equal(polyline.getAttribute('points').split(' ')[0], '20.00,28.00');
  });

  await t.test('eased motion moves toward the latest frame over several steps', () => {
    const { polyline, factory, sse, timing } = makeMouth();
    sse.emit('voice.state', { state: 'speaking' });
    factory.sources[0].emit('frame', QUIET);
    timing.runFrame(1000);
    factory.sources[0].emit('frame', LOUD);
    timing.runFrame(1040);
    const first = parseFloat(polyline.getAttribute('points').split(' ')[0].split(',')[1]);
    assert.ok(first > 28 && first < 50, `eased y ${first} should sit between flat and target`);
  });
});

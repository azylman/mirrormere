const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const { FaceController, WORDS } = require('../static/js/face.js');

/**
 * Lightweight mock DOM element for hermetic Node.js testing.
 */
function createMockElement(tagName = 'div') {
  const attributes = new Map();
  const children = [];
  return {
    tagName: tagName.toUpperCase(),
    id: '',
    className: '',
    dataset: {},
    textContent: '',
    children,
    parentNode: null,
    setAttribute(name, value) { attributes.set(name, String(value)); },
    getAttribute(name) { return attributes.has(name) ? attributes.get(name) : null; },
    appendChild(child) {
      child.parentNode = this;
      children.push(child);
      return child;
    },
  };
}

/**
 * Mock document with a body element and a createElement spy.
 */
function createMockDocument() {
  const body = createMockElement('body');
  const doc = {
    body,
    createCount: 0,
    createElement(tag) {
      doc.createCount += 1;
      return createMockElement(tag);
    },
  };
  return doc;
}

/**
 * Constructs a FaceController with window.location.search set to `search`.
 */
function withSearch(search, build) {
  const prev = global.window;
  global.window = { location: { search } };
  try {
    return build();
  } finally {
    if (prev === undefined) {
      delete global.window;
    } else {
      global.window = prev;
    }
  }
}

function mountFace(search = '?face=1') {
  const doc = createMockDocument();
  const sseClient = {
    handlers: new Map(),
    onCalls: [],
    on(eventType, fn) {
      this.onCalls.push(eventType);
      this.handlers.set(eventType, fn);
      return this;
    },
  };
  const controller = withSearch(search, () => new FaceController({ sseClient, document: doc }));
  const el = doc.body.children[0];
  return { controller, doc, sseClient, el };
}

test('Amos state word overlay', async (t) => {
  await t.test('word map covers every voice state', () => {
    assert.deepEqual(WORDS, {
      idle: 'IDLE',
      listening: 'LISTENING',
      transcribing: 'TRANSCRIBING',
      thinking: 'THINKING',
      synthesizing: 'SYNTHESIZING',
      speaking: 'SPEAKING',
      error: 'ERROR',
    });
  });

  await t.test('creates the overlay element with idle defaults when face=1', () => {
    const { doc, el, sseClient } = mountFace();
    assert.equal(doc.body.children.length, 1);
    assert.equal(el.id, 'amos-face');
    assert.equal(el.className, 'amos-face');
    assert.equal(el.getAttribute('aria-live'), 'polite');
    assert.equal(el.dataset.state, 'idle');
    assert.equal(el.textContent, 'IDLE');
    assert.deepEqual(sseClient.onCalls, ['voice.state']);
  });

  await t.test('each state maps to its word and dataset state', () => {
    const { controller, el } = mountFace();
    for (const [state, word] of Object.entries(WORDS)) {
      controller.setState(state);
      assert.equal(el.textContent, word, `text for ${state}`);
      assert.equal(el.dataset.state, state, `dataset for ${state}`);
    }
  });

  await t.test('unknown or missing state shows IDLE', () => {
    const { controller, el } = mountFace();
    controller.setState('speaking');
    controller.setState('bogus');
    assert.equal(el.textContent, 'IDLE');
    assert.equal(el.dataset.state, 'idle');

    controller.setState('speaking');
    controller.setState(undefined);
    assert.equal(el.textContent, 'IDLE');
    assert.equal(el.dataset.state, 'idle');
  });

  await t.test('error returns to IDLE after 4 s', (t2) => {
    t2.mock.timers.enable({ apis: ['setTimeout'] });
    const { controller, el } = mountFace();
    controller.setState('error');
    assert.equal(el.textContent, 'ERROR');
    assert.equal(el.dataset.state, 'error');

    t2.mock.timers.tick(3999);
    assert.equal(el.textContent, 'ERROR');

    t2.mock.timers.tick(1);
    assert.equal(el.textContent, 'IDLE');
    assert.equal(el.dataset.state, 'idle');
    t2.mock.timers.reset();
  });

  await t.test('a newer state cancels the pending error reset', (t2) => {
    t2.mock.timers.enable({ apis: ['setTimeout'] });
    const { controller, el } = mountFace();
    controller.setState('error');
    t2.mock.timers.tick(2000);
    controller.setState('speaking');

    t2.mock.timers.tick(10000);
    assert.equal(el.textContent, 'SPEAKING');
    assert.equal(el.dataset.state, 'speaking');
    t2.mock.timers.reset();
  });

  await t.test('a second error restarts the 4 s window', (t2) => {
    t2.mock.timers.enable({ apis: ['setTimeout'] });
    const { controller, el } = mountFace();
    controller.setState('error');
    t2.mock.timers.tick(3000);
    controller.setState('error');
    t2.mock.timers.tick(3999);
    assert.equal(el.textContent, 'ERROR');
    t2.mock.timers.tick(1);
    assert.equal(el.textContent, 'IDLE');
    t2.mock.timers.reset();
  });

  await t.test('without face=1 no element is created and sseClient.on is not called', () => {
    const doc = createMockDocument();
    const sseClient = {
      onCalls: 0,
      on() { this.onCalls += 1; return this; },
    };
    withSearch('?face=0', () => new FaceController({ sseClient, document: doc }));
    withSearch('', () => new FaceController({ sseClient, document: doc }));
    assert.equal(doc.createCount, 0);
    assert.equal(doc.body.children.length, 0);
    assert.equal(sseClient.onCalls, 0);
  });

  await t.test('voice.state SSE event updates the word', () => {
    const { sseClient, el } = mountFace();
    const handler = sseClient.handlers.get('voice.state');
    assert.equal(typeof handler, 'function');

    handler({ state: 'listening' });
    assert.equal(el.textContent, 'LISTENING');

    handler({ state: 'error' });
    assert.equal(el.textContent, 'ERROR');

    handler(null);
    assert.equal(el.textContent, 'IDLE');
  });

  await t.test('browser global window.MirrormereFace binding', () => {
    const faceCode = fs.readFileSync(path.join(__dirname, '../static/js/face.js'), 'utf8');
    const mockWindow = {};
    const fn = new Function('window', 'module', faceCode);
    fn(mockWindow, {});

    assert.ok(mockWindow.MirrormereFace, 'window.MirrormereFace should be defined');
    assert.strictEqual(typeof mockWindow.MirrormereFace.FaceController, 'function');
    assert.deepEqual(mockWindow.MirrormereFace.WORDS, WORDS);
  });
});

const { test, describe, beforeEach, afterEach } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const htmlPath = path.resolve(__dirname, '../static/photo_frame.html');
const displayPath = path.resolve(__dirname, '../static/js/display.js');

const htmlContent = fs.readFileSync(htmlPath, 'utf8');
const scriptMatch = htmlContent.match(/<script>([\s\S]*?)<\/script>/);
if (!scriptMatch) {
  throw new Error('Could not find <script> block in photo_frame.html');
}
const photoFrameScript = scriptMatch[1];

function createMockElement(id) {
  const classes = new Set();
  const listeners = {};
  return {
    id,
    src: '',
    textContent: '',
    style: {},
    classList: {
      add: (...names) => names.forEach((n) => classes.add(n)),
      remove: (...names) => names.forEach((n) => classes.delete(n)),
      contains: (n) => classes.has(n),
    },
    addEventListener: (ev, handler) => {
      listeners[ev] = listeners[ev] || [];
      listeners[ev].push(handler);
    },
    removeEventListener: (ev, handler) => {
      if (!listeners[ev]) return;
      listeners[ev] = listeners[ev].filter((h) => h !== handler);
    },
    closest: () => null,
    click: () => {
      (listeners['click'] || []).forEach((h) => h({ stopPropagation: () => {} }));
    },
  };
}

function createPhotoFrameInstance({ parentWindow, origin = 'http://localhost:8080' } = {}) {
  const elements = {
    'slide-a': createMockElement('slide-a'),
    'slide-b': createMockElement('slide-b'),
    'sync-dot': createMockElement('sync-dot'),
    'counter-pill': createMockElement('counter-pill'),
    'empty-state': createMockElement('empty-state'),
    'empty-desc': createMockElement('empty-desc'),
    'connect-btn': createMockElement('connect-btn'),
    'next-btn': createMockElement('next-btn'),
    'status-caption': createMockElement('status-caption'),
    'album-title': createMockElement('album-title'),
  };

  const messageListeners = [];

  const iframeWin = {
    location: {
      origin,
      search: '',
    },
    addEventListener: (type, handler) => {
      if (type === 'message') {
        messageListeners.push(handler);
      }
    },
    removeEventListener: (type, handler) => {
      if (type === 'message') {
        const idx = messageListeners.indexOf(handler);
        if (idx !== -1) messageListeners.splice(idx, 1);
      }
    },
    postMessage: (data, targetOrigin) => {
      const ev = {
        data,
        origin: targetOrigin === '*' ? origin : targetOrigin,
        source: parentWindow || iframeWin,
      };
      for (const listener of [...messageListeners]) {
        listener(ev);
      }
    },
    console: {
      log: () => {},
      warn: () => {},
      error: () => {},
    },
    setTimeout: (fn, ms) => setTimeout(fn, ms),
    clearTimeout: (id) => clearTimeout(id),
    setInterval: () => 1,
    clearInterval: () => {},
    fetch: async () => ({
      ok: true,
      json: async () => ({ album_url: '', photos: [] }),
    }),
  };

  if (parentWindow) {
    iframeWin.parent = parentWindow;
    iframeWin.top = parentWindow;
  } else {
    iframeWin.parent = iframeWin;
    iframeWin.top = iframeWin;
  }

  const iframeDoc = {
    getElementById: (id) => elements[id] || null,
    querySelector: (sel) => {
      if (sel === '.empty-title') return createMockElement('empty-title');
      return null;
    },
    addEventListener: () => {},
    removeEventListener: () => {},
  };

  iframeWin.window = iframeWin;
  iframeWin.document = iframeDoc;
  iframeWin.self = iframeWin;

  vm.createContext(iframeWin);
  vm.runInContext(photoFrameScript, iframeWin);

  return {
    window: iframeWin,
    document: iframeDoc,
    elements,
    messageListeners,
  };
}

describe('Photo Frame Multi-Iframe Sync (#848)', () => {
  let origWindow;
  let origDocument;

  beforeEach(() => {
    origWindow = global.window;
    origDocument = global.document;
    delete require.cache[require.resolve('../static/js/display.js')];
  });

  afterEach(() => {
    global.window = origWindow;
    global.document = origDocument;
  });

  test('two photo_frame iframes both receive onEmberPhotosSynced without clobbering parent handler', () => {
    const parentWin = {
      location: { origin: 'http://localhost:8080' },
    };

    const inst1 = createPhotoFrameInstance({ parentWindow: parentWin, origin: 'http://localhost:8080' });
    const inst2 = createPhotoFrameInstance({ parentWindow: parentWin, origin: 'http://localhost:8080' });

    // Verify neither iframe clobbered parentWin or bound window.onEmberPhotosSynced in child frames
    assert.equal(inst1.window.onEmberPhotosSynced, undefined, 'inst1 must not bind window.onEmberPhotosSynced when embedded');
    assert.equal(inst2.window.onEmberPhotosSynced, undefined, 'inst2 must not bind window.onEmberPhotosSynced when embedded');
    assert.equal(parentWin.onEmberPhotosSynced, undefined, 'Child iframes must not overwrite parent.onEmberPhotosSynced');

    // Wire up parent shell display.js forwardEmberPhotosSynced
    const iframes = [
      {
        contentWindow: {
          postMessage: (data, targetOrigin) => {
            for (const h of inst1.messageListeners) {
              h({ data, origin: targetOrigin === '*' ? 'http://localhost:8080' : targetOrigin, source: parentWin });
            }
          },
        },
      },
      {
        contentWindow: {
          postMessage: (data, targetOrigin) => {
            for (const h of inst2.messageListeners) {
              h({ data, origin: targetOrigin === '*' ? 'http://localhost:8080' : targetOrigin, source: parentWin });
            }
          },
        },
      },
    ];

    const parentDoc = {
      querySelectorAll: (sel) => (sel === 'iframe' ? iframes : []),
    };

    global.window = parentWin;
    global.document = parentDoc;

    const display = require('../static/js/display.js');
    assert.equal(typeof parentWin.onEmberPhotosSynced, 'function', 'Parent window must expose onEmberPhotosSynced');
    assert.equal(typeof display.forwardEmberPhotosSynced, 'function', 'MirrormereDisplay must expose forwardEmberPhotosSynced');

    // Parent broadcasts Ember photo sync to all iframes
    const syncPayload = {
      source: 'ember-android-kiosk',
      photos: [
        'https://lh3.googleusercontent.com/pw/photo1',
        'https://lh3.googleusercontent.com/pw/photo2',
      ],
    };

    parentWin.onEmberPhotosSynced(syncPayload);

    // Verify BOTH iframes received the update and updated their counter pills
    assert.equal(inst1.elements['counter-pill'].textContent, '1 / 2', 'Iframe 1 counter pill should reflect 2 synced photos');
    assert.equal(inst2.elements['counter-pill'].textContent, '1 / 2', 'Iframe 2 counter pill should reflect 2 synced photos');
    assert.ok(inst1.elements['empty-state'].classList.contains('hidden'), 'Iframe 1 should hide empty state');
    assert.ok(inst2.elements['empty-state'].classList.contains('hidden'), 'Iframe 2 should hide empty state');
  });

  test('photo_frame binds onEmberPhotosSynced when running standalone at top level', () => {
    const inst = createPhotoFrameInstance({ parentWindow: null, origin: 'http://localhost:8080' });

    assert.equal(typeof inst.window.onEmberPhotosSynced, 'function', 'Standalone instance must expose onEmberPhotosSynced');

    inst.window.onEmberPhotosSynced({
      photos: ['https://lh3.googleusercontent.com/pw/photoA'],
    });

    assert.equal(inst.elements['counter-pill'].textContent, '1 / 1');
    assert.ok(inst.elements['empty-state'].classList.contains('hidden'));
  });

  test('message listener rejects untrusted messages or invalid origins', () => {
    const parentWin = {
      location: { origin: 'http://localhost:8080' },
    };

    const inst = createPhotoFrameInstance({ parentWindow: parentWin, origin: 'http://localhost:8080' });

    // 1. Wrong event type
    for (const h of inst.messageListeners) {
      h({
        data: { type: 'unrelated-event', payload: { photos: ['url'] } },
        origin: 'http://localhost:8080',
        source: parentWin,
      });
    }
    assert.equal(inst.elements['counter-pill'].textContent, '', 'Untrusted event type must be ignored');

    // 2. Untrusted source window
    const evilWindow = {};
    for (const h of inst.messageListeners) {
      h({
        data: { type: 'ember-photos-synced', payload: { photos: ['url'] } },
        origin: 'http://localhost:8080',
        source: evilWindow,
      });
    }
    assert.equal(inst.elements['counter-pill'].textContent, '', 'Untrusted source window must be ignored');

    // 3. Origin mismatch
    for (const h of inst.messageListeners) {
      h({
        data: { type: 'ember-photos-synced', payload: { photos: ['url'] } },
        origin: 'http://evil.com',
        source: parentWin,
      });
    }
    assert.equal(inst.elements['counter-pill'].textContent, '', 'Untrusted origin must be ignored');

    // 4. Valid origin succeeds
    for (const h of inst.messageListeners) {
      h({
        data: { type: 'ember-photos-synced', payload: { photos: ['https://lh3.googleusercontent.com/pw/valid'] } },
        origin: 'http://localhost:8080',
        source: parentWin,
      });
    }
    assert.equal(inst.elements['counter-pill'].textContent, '1 / 1', 'Valid message must update photo pool');
  });

  test('reconcilePhotos preserves active photo slide and updates counter pill during Ember sync without reloading', () => {
    const inst = createPhotoFrameInstance({ parentWindow: null, origin: 'http://localhost:8080' });

    // Initial sync with 2 photos
    inst.window.onEmberPhotosSynced({
      photos: [
        'https://lh3.googleusercontent.com/pw/photoA=w500',
        'https://lh3.googleusercontent.com/pw/photoB=w500',
      ],
    });

    assert.equal(inst.elements['counter-pill'].textContent, '1 / 2');
    const slideB = inst.elements['slide-b'];
    assert.equal(slideB.src, 'https://lh3.googleusercontent.com/pw/photoA=w1080-h1080-c');

    // Trigger onload to mark active
    slideB.onload();

    // Reset slideA src to test whether it gets reloaded
    const slideA = inst.elements['slide-a'];
    slideA.src = 'untouched';

    // 5m periodic sync arrives with updated sizing query params and a new 3rd photo
    inst.window.onEmberPhotosSynced({
      photos: [
        'https://lh3.googleusercontent.com/pw/photoA=w600',
        'https://lh3.googleusercontent.com/pw/photoB=w600',
        'https://lh3.googleusercontent.com/pw/photoC=w600',
      ],
    });

    // Counter pill text is updated in place to reflect 3 total photos without reload
    assert.equal(inst.elements['counter-pill'].textContent, '1 / 3');
    assert.equal(slideA.src, 'untouched', 'Non-active slide must not be loaded when displayed photo is preserved');
  });

  test('advances through seen photos without replacement before repeating in photo_frame', () => {
    const inst = createPhotoFrameInstance({ parentWindow: null, origin: 'http://localhost:8080' });

    inst.window.onEmberPhotosSynced({
      photos: [
        'https://lh3.googleusercontent.com/pw/p1',
        'https://lh3.googleusercontent.com/pw/p2',
        'https://lh3.googleusercontent.com/pw/p3',
      ],
    });

    assert.equal(inst.elements['counter-pill'].textContent, '1 / 3');

    // Click next button -> advances to p2 (2 / 3)
    inst.elements['next-btn'].click();
    assert.equal(inst.elements['counter-pill'].textContent, '2 / 3');

    // Click next button -> advances to p3 (3 / 3)
    inst.elements['next-btn'].click();
    assert.equal(inst.elements['counter-pill'].textContent, '3 / 3');

    // Click next button -> deck exhausted, clears seen and wraps around to p1 (1 / 3)
    inst.elements['next-btn'].click();
    assert.equal(inst.elements['counter-pill'].textContent, '1 / 3');
  });
});

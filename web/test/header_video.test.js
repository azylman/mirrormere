const { test, describe } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

describe('Header Video Action Button', () => {
  test('template display.html includes header-video-btn to the left of weather-pill', () => {
    const tmplPath = path.join(__dirname, '../templates/display.html');
    const content = fs.readFileSync(tmplPath, 'utf8');

    assert.ok(content.includes('id="header-video-btn"'), 'must include header-video-btn');
    assert.ok(content.includes('class="header-video-icon"'), 'must include header-video-icon');
    assert.ok(content.includes('id="header-video-spinner"'), 'must include header-video-spinner');

    const videoIdx = content.indexOf('id="header-video-btn"');
    const weatherIdx = content.indexOf('id="weather-pill"');
    assert.ok(videoIdx > 0 && weatherIdx > 0, 'both elements must exist');
    assert.ok(videoIdx < weatherIdx, 'header-video-btn must appear before weather-pill in DOM order');
  });

  function createMockButton(dataset = {}) {
    const listeners = {};
    const classes = new Set(['header-video-btn']);
    return {
      id: 'header-video-btn',
      dataset: { ...dataset },
      classList: {
        add: (c) => classes.add(c),
        remove: (c) => classes.delete(c),
        contains: (c) => classes.has(c),
      },
      addEventListener: (type, handler) => {
        if (!listeners[type]) listeners[type] = [];
        listeners[type].push(handler);
      },
      removeEventListener: (type, handler) => {
        if (listeners[type]) {
          listeners[type] = listeners[type].filter((h) => h !== handler);
        }
      },
      dispatchEvent: (evt) => {
        const handlers = listeners[evt.type] || [];
        for (const h of handlers) {
          h(evt);
        }
      },
      _listeners: listeners,
      _classes: classes,
    };
  }

  function createMockSSE() {
    const handlers = {};
    return {
      on: (type, fn) => {
        if (!handlers[type]) handlers[type] = [];
        handlers[type].push(fn);
      },
      off: (type, fn) => {
        if (handlers[type]) {
          handlers[type] = handlers[type].filter((h) => h !== fn);
        }
      },
      emit: (type, data) => {
        const list = handlers[type] || [];
        for (const fn of list) {
          fn(data);
        }
      },
      _handlers: handlers,
    };
  }

  test('dispatches concurrent wake action and video presentation trigger on click', async () => {
    delete require.cache[require.resolve('../static/js/display.js')];
    const display = require('../static/js/display.js');

    const btn = createMockButton();
    const sse = createMockSSE();
    const fetchCalls = [];

    const fetchMock = async (url, opts) => {
      fetchCalls.push({ url, opts, body: opts && opts.body ? JSON.parse(opts.body) : null });
      return { ok: true, status: 200, json: () => Promise.resolve({ status: 'ok' }) };
    };

    const ctrl = display.setupHeaderVideo({
      button: btn,
      sseClient: sse,
      fetch: fetchMock,
      forceNew: true,
      window: { location: { hostname: '192.168.1.147' } },
    });

    assert.ok(ctrl, 'controller must be initialized');
    assert.equal(ctrl.isLoading, false);

    // Simulate click
    btn.dispatchEvent({ type: 'click' });

    assert.equal(btn.classList.contains('loading'), true);
    assert.equal(ctrl.isLoading, true);

    await new Promise((r) => setTimeout(r, 20));

    assert.equal(fetchCalls.length, 2, 'must issue wake action and video trigger');

    // 1. Wake call
    const wakeCall = fetchCalls.find((c) => c.url.includes('/action'));
    assert.ok(wakeCall, 'wake call to control URL must be issued');
    assert.equal(wakeCall.opts.method, 'POST');
    assert.deepEqual(wakeCall.body, { id: 'chromecast', action: 'wake' });

    // 2. Video presentation trigger
    const triggerCall = fetchCalls.find((c) => c.url === 'api/video/trigger');
    assert.ok(triggerCall, 'api/video/trigger must be called');
    assert.equal(triggerCall.opts.method, 'POST');
    assert.equal(triggerCall.body.id, 'chromecast');
    assert.equal(triggerCall.body.stream_url, 'http://192.168.1.147:1984/api/webrtc?src=cast');
    assert.equal(triggerCall.body.type, 'webrtc');
    assert.equal(triggerCall.body.priority, 'persistent');
    assert.equal(triggerCall.body.controllable, true);

    // SSE video.state event with mode: 'video' clears loading state
    sse.emit('video.state', { mode: 'video', primary: { id: 'chromecast' } });
    assert.equal(btn.classList.contains('loading'), false);
    assert.equal(ctrl.isLoading, false);

    ctrl.destroy();
  });

  test('debounces rapid double taps and prevents duplicate requests while loading', async () => {
    delete require.cache[require.resolve('../static/js/display.js')];
    const display = require('../static/js/display.js');

    const btn = createMockButton();
    const sse = createMockSSE();
    let triggerCount = 0;

    const fetchMock = async (url) => {
      if (url === 'api/video/trigger') triggerCount++;
      return { ok: true, status: 200, json: () => Promise.resolve({}) };
    };

    const ctrl = display.setupHeaderVideo({
      button: btn,
      sseClient: sse,
      fetch: fetchMock,
      forceNew: true,
    });

    // 1. First tap triggers video presentation
    btn.dispatchEvent({ type: 'pointerup' });
    assert.equal(triggerCount, 1);
    assert.equal(ctrl.isLoading, true);

    // 2. Immediate second tap while isLoading is true is rejected
    btn.dispatchEvent({ type: 'click' });
    assert.equal(triggerCount, 1);

    ctrl.clearLoading();

    // 3. Second tap within 350ms debounce window is rejected
    btn.dispatchEvent({ type: 'click' });
    assert.equal(triggerCount, 1);

    // 4. Tap after cooldown succeeds
    await new Promise((r) => setTimeout(r, 360));
    btn.dispatchEvent({ type: 'click' });
    assert.equal(triggerCount, 2);

    ctrl.destroy();
  });

  test('drag gesture >= 15px is rejected and does not trigger video', async () => {
    delete require.cache[require.resolve('../static/js/display.js')];
    const display = require('../static/js/display.js');

    const btn = createMockButton();
    let triggered = false;

    const fetchMock = async () => {
      triggered = true;
      return { ok: true, status: 200, json: () => Promise.resolve({}) };
    };

    const ctrl = display.setupHeaderVideo({
      button: btn,
      fetch: fetchMock,
      forceNew: true,
    });

    // Drag from (100, 100) to (125, 100) - distance 25px
    btn.dispatchEvent({ type: 'pointerdown', clientX: 100, clientY: 100 });
    btn.dispatchEvent({ type: 'pointerup', clientX: 125, clientY: 100 });

    assert.equal(triggered, false, 'drag gesture must not trigger video');
    assert.equal(ctrl.isLoading, false);

    // Stationary tap distance 3px succeeds
    await new Promise((r) => setTimeout(r, 360));
    btn.dispatchEvent({ type: 'pointerdown', clientX: 100, clientY: 100 });
    btn.dispatchEvent({ type: 'pointerup', clientX: 102, clientY: 101 });

    assert.equal(triggered, true, 'stationary tap must trigger video');

    ctrl.destroy();
  });

  test('no-ops or brings up HUD if display is already in video presentation mode', async () => {
    delete require.cache[require.resolve('../static/js/display.js')];
    const display = require('../static/js/display.js');

    const btn = createMockButton();
    let triggered = false;
    let hudShown = false;

    const fetchMock = async () => {
      triggered = true;
      return { ok: true, status: 200 };
    };

    const mockWin = {
      videoManager: { currentMode: 'video' },
      videoHUD: {
        showHUD: () => {
          hudShown = true;
        },
      },
    };

    const ctrl = display.setupHeaderVideo({
      button: btn,
      fetch: fetchMock,
      forceNew: true,
      window: mockWin,
    });

    btn.dispatchEvent({ type: 'click' });

    assert.equal(triggered, false, 'must not trigger video when already in video mode');
    assert.equal(hudShown, true, 'must reveal HUD instead');
    assert.equal(ctrl.isLoading, false);

    ctrl.destroy();
  });

  test('clears loading state and adds error indicator on trigger failure', async () => {
    delete require.cache[require.resolve('../static/js/display.js')];
    const display = require('../static/js/display.js');

    const btn = createMockButton();

    const fetchMock = async (url) => {
      if (url === 'api/video/trigger') {
        return { ok: false, status: 500 };
      }
      return { ok: true, status: 200 };
    };

    const ctrl = display.setupHeaderVideo({
      button: btn,
      fetch: fetchMock,
      forceNew: true,
    });

    btn.dispatchEvent({ type: 'click' });
    await new Promise((r) => setTimeout(r, 20));

    assert.equal(ctrl.isLoading, false);
    assert.equal(btn.classList.contains('loading'), false);
    assert.equal(btn.classList.contains('error'), true);

    ctrl.destroy();
  });

  test('resolves custom stream URL, control URL, and stream ID from dataset and options', async () => {
    delete require.cache[require.resolve('../static/js/display.js')];
    const display = require('../static/js/display.js');

    const btn = createMockButton({
      streamUrl: 'http://custom-host:1984/stream',
      controlUrl: 'http://custom-sidecar:8090/wake',
      streamId: 'custom-cam',
    });

    let capturedTrigger = null;
    let capturedWake = null;

    const fetchMock = async (url, opts) => {
      if (url.includes('/wake')) {
        capturedWake = { url, body: JSON.parse(opts.body) };
      } else if (url === 'api/video/trigger') {
        capturedTrigger = { url, body: JSON.parse(opts.body) };
      }
      return { ok: true, status: 200 };
    };

    const ctrl = display.setupHeaderVideo({
      button: btn,
      fetch: fetchMock,
      forceNew: true,
    });

    btn.dispatchEvent({ type: 'click' });
    await new Promise((r) => setTimeout(r, 20));

    assert.ok(capturedWake);
    assert.equal(capturedWake.url, 'http://custom-sidecar:8090/wake');
    assert.deepEqual(capturedWake.body, { id: 'custom-cam', action: 'wake' });

    assert.ok(capturedTrigger);
    assert.equal(capturedTrigger.body.id, 'custom-cam');
    assert.equal(capturedTrigger.body.stream_url, 'http://custom-host:1984/stream');
    assert.equal(capturedTrigger.body.control_url, 'http://custom-sidecar:8090/wake');

    ctrl.destroy();
  });
});

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const liveViewCode = fs.readFileSync(path.join(__dirname, '../static/js/live_view.js'), 'utf8');

function setupDOMMock() {
  global.window = {};
  const fn = new Function('window', 'document', 'fetch', 'RTCPeerConnection', 'IntersectionObserver', 'setTimeout', 'clearTimeout', liveViewCode);
  fn(global.window, {}, global.fetch, undefined, undefined, global.setTimeout, global.clearTimeout);
}

test('Live View Client Controller', async (t) => {
  setupDOMMock();

  class MockPeerConnection {
    constructor() {
      this.transceivers = [];
      this.iceGatheringState = 'complete';
      this.localDescription = { sdp: 'v=0\r\no=mock\r\n' };
      this.closed = false;
    }
    addTransceiver(kind, init) {
      this.transceivers.push({ kind, init });
    }
    createOffer() {
      return Promise.resolve({ type: 'offer', sdp: 'v=0\r\no=mock\r\n' });
    }
    setLocalDescription() {
      return Promise.resolve();
    }
    setRemoteDescription() {
      return Promise.resolve();
    }
    close() {
      this.closed = true;
    }
  }

  function createMockElement(configObj = {}) {
    const listeners = {};
    const classes = new Set();
    const videoEl = {
      muted: false,
      volume: 1,
      srcObject: null,
      play() { return Promise.resolve(); },
      pause() {},
    };
    const imgEl = { src: '' };

    return {
      dataset: { widgetId: 'cam-tile' },
      classList: {
        add(c) { classes.add(c); },
        remove(c) { classes.delete(c); },
        contains(c) { return classes.has(c); },
      },
      addEventListener(evt, fn) {
        listeners[evt] = fn;
      },
      removeEventListener(evt) {
        delete listeners[evt];
      },
      dispatchEvent(evt) {
        if (listeners[evt.type]) listeners[evt.type](evt);
      },
      querySelector(selector) {
        if (selector === '.live-view-config') {
          return { textContent: JSON.stringify(configObj) };
        }
        if (selector === 'video.live-view-media') return videoEl;
        if (selector === 'img.live-view-mjpeg') return imgEl;
        return null;
      },
      _video: videoEl,
      _img: imgEl,
    };
  }

  await t.test('mounts instance and parses config cleanly', () => {
    const el = createMockElement({ stream_url: 'http://127.0.0.1:1984/api/webrtc?src=cam' });
    const inst = global.window.MirrormereLiveView.mount(el);

    assert.ok(inst);
    assert.equal(inst.widgetId, 'cam-tile');
    assert.equal(inst.config.stream_url, 'http://127.0.0.1:1984/api/webrtc?src=cam');

    global.window.MirrormereLiveView.unmount('cam-tile');
    assert.equal(global.window.MirrormereLiveView.getInstance('cam-tile'), null);
  });

  await t.test('dispatches POST /api/video/trigger on click with in-flight lock and omits timeout_seconds when 0', async () => {
    let triggeredPayload = null;
    const fetchMock = async (url, opts) => {
      if (url === 'api/video/trigger') {
        triggeredPayload = JSON.parse(opts.body);
        return { ok: true, status: 200, text: () => Promise.resolve('{"status":"ok"}') };
      }
      return { ok: true, status: 200, text: () => Promise.resolve('{}') };
    };

    const el = createMockElement({
      stream_url: 'http://127.0.0.1:1984/api/webrtc?src=cast',
      stream_type: 'webrtc',
      stream_id: 'chromecast',
      priority: 'persistent',
      controllable: true,
      control_url: 'http://cast-watcher:8090/action',
    });

    const inst = global.window.MirrormereLiveView.mount(el, {
      fetch: fetchMock,
      RTCPeerConnection: MockPeerConnection,
    });

    // Simulate tap
    el.dispatchEvent({ type: 'click' });

    assert.equal(el.classList.contains('loading'), true);
    assert.equal(inst.isLoading, true);

    // Second tap while in-flight should be ignored
    el.dispatchEvent({ type: 'click' });

    assert.ok(triggeredPayload);
    assert.equal(triggeredPayload.id, 'chromecast');
    assert.equal(triggeredPayload.stream_url, 'http://127.0.0.1:1984/api/webrtc?src=cast');
    assert.equal(triggeredPayload.priority, 'persistent');
    assert.equal(triggeredPayload.controllable, true);
    assert.equal(triggeredPayload.control_url, 'http://cast-watcher:8090/action');
    // Crucial catch: timeout_seconds omitted when not specified
    assert.equal(triggeredPayload.timeout_seconds, undefined);

    global.window.MirrormereLiveView.unmount('cam-tile');
  });

  await t.test('includes timeout_seconds when configured for temporary alert stream', async () => {
    let triggeredPayload = null;
    const fetchMock = async (url, opts) => {
      if (url === 'api/video/trigger') {
        triggeredPayload = JSON.parse(opts.body);
        return { ok: true, status: 200, text: () => Promise.resolve('{"status":"ok"}') };
      }
      return { ok: true, status: 200, text: () => Promise.resolve('{}') };
    };

    const el = createMockElement({
      stream_url: 'http://127.0.0.1:1984/api/webrtc?src=doorbell',
      stream_id: 'doorbell',
      priority: 'temporary',
      timeout_seconds: 30,
    });

    global.window.MirrormereLiveView.mount(el, {
      fetch: fetchMock,
      RTCPeerConnection: MockPeerConnection,
    });

    el.dispatchEvent({ type: 'click' });

    assert.ok(triggeredPayload);
    assert.equal(triggeredPayload.id, 'doorbell');
    assert.equal(triggeredPayload.priority, 'temporary');
    assert.equal(triggeredPayload.timeout_seconds, 30);

    global.window.MirrormereLiveView.unmount('cam-tile');
  });

  await t.test('IntersectionObserver tears down decoders off-screen and restarts in-view', () => {
    let observerCallback = null;
    class MockObserver {
      constructor(cb) {
        observerCallback = cb;
      }
      observe() {}
      disconnect() {}
    }

    const el = createMockElement({
      stream_url: 'http://127.0.0.1:1984/api/webrtc?src=cast',
    });

    const inst = global.window.MirrormereLiveView.mount(el, {
      RTCPeerConnection: MockPeerConnection,
      IntersectionObserver: MockObserver,
    });

    assert.ok(inst.pc);
    const firstPc = inst.pc;

    // Simulate rotating off-screen
    observerCallback([{ target: el, isIntersecting: false }]);
    assert.equal(inst.pc, null);
    assert.equal(firstPc.closed, true);

    // Simulate rotating back on-screen
    observerCallback([{ target: el, isIntersecting: true }]);
    assert.ok(inst.pc);
    assert.notEqual(inst.pc, firstPc);

    global.window.MirrormereLiveView.unmount('cam-tile');
  });

  await t.test('SSE video.state pauses decoders and clears loading state in video mode', () => {
    let sseCallback = null;
    const mockSSE = {
      on(evt, cb) {
        if (evt === 'video.state') sseCallback = cb;
      }
    };

    const el = createMockElement({
      stream_url: 'http://127.0.0.1:1984/api/webrtc?src=cast',
    });

    const inst = global.window.MirrormereLiveView.mount(el, {
      RTCPeerConnection: MockPeerConnection,
      sseClient: mockSSE,
    });

    inst.isLoading = true;
    el.classList.add('loading');
    assert.ok(inst.pc);

    // Incoming video.state entering video mode
    sseCallback({ mode: 'video', primary: { id: 'chromecast' } });

    assert.equal(inst.pc, null);
    assert.equal(inst.isLoading, false);
    assert.equal(el.classList.contains('loading'), false);

    // Returning to widgets mode resumes stream
    sseCallback({ mode: 'widgets', primary: null });
    assert.ok(inst.pc);

    global.window.MirrormereLiveView.unmount('cam-tile');
  });

  await t.test('mounts instance and parses double-encoded config string cleanly', () => {
    const rawConfig = { stream_url: 'http://127.0.0.1:1984/api/webrtc?src=cast', stream_type: 'webrtc' };
    const el = createMockElement(rawConfig);
    el.querySelector = (selector) => {
      if (selector === '.live-view-config') {
        return { textContent: JSON.stringify(JSON.stringify(rawConfig)) };
      }
      if (selector === 'video.live-view-media') return el._video;
      if (selector === 'img.live-view-mjpeg') return el._img;
      return null;
    };

    const inst = global.window.MirrormereLiveView.mount(el, {
      RTCPeerConnection: MockPeerConnection,
    });

    assert.ok(inst);
    assert.equal(typeof inst.config, 'object');
    assert.equal(inst.config.stream_url, 'http://127.0.0.1:1984/api/webrtc?src=cast');
    assert.ok(inst.pc);

    global.window.MirrormereLiveView.unmount('cam-tile');
  });

  await t.test('startStream cleans up existing pc without invalidating currentEpoch', async () => {
    const el = createMockElement({ stream_url: 'http://127.0.0.1:1984/api/webrtc?src=cast' });
    const mockFetch = async () => ({
      ok: true,
      text: () => Promise.resolve('{"sdp":"v=0\\r\\no=mock\\r\\n"}'),
    });
    const inst = global.window.MirrormereLiveView.mount(el, {
      RTCPeerConnection: MockPeerConnection,
      fetch: mockFetch,
    });

    assert.ok(inst.pc);
    const initialPc = inst.pc;

    await inst.startStream();

    assert.ok(initialPc.closed);
    assert.ok(inst.pc);
    assert.equal(inst.pc.closed, false);

    global.window.MirrormereLiveView.unmount('cam-tile');
  });

  await t.test('attachSSE dynamically binds SSE client and resumes stream on widgets mode even if isIntersecting is false', async () => {
    let sseCallback = null;
    let sseOffCalled = false;
    const mockSSE = {
      on(evt, cb) {
        if (evt === 'video.state') sseCallback = cb;
      },
      off(evt, cb) {
        if (evt === 'video.state') sseOffCalled = true;
      }
    };

    const el = createMockElement({ stream_url: 'http://127.0.0.1:1984/api/webrtc?src=cast' });
    el.isConnected = true;
    el.getBoundingClientRect = () => ({ width: 400, height: 250 });

    // Mount without sseClient (simulating widget rendered before display.js initializes SSE)
    const inst = global.window.MirrormereLiveView.mount(el, {
      RTCPeerConnection: MockPeerConnection,
    });

    assert.equal(inst.sseClient, null);
    assert.ok(inst.pc);

    // Later, display.js initializes and calls attachSSE
    global.window.MirrormereLiveView.attachSSE(mockSSE);
    assert.equal(inst.sseClient, mockSSE);
    assert.ok(sseCallback);

    // Fullscreen presentation activates: enters video mode
    sseCallback({ mode: 'video', primary: { id: 'chromecast' } });
    assert.equal(inst.pc, null);

    // Simulate display: none making isIntersecting false
    inst.isIntersecting = false;

    // Fullscreen dismissed: returns to widgets mode
    sseCallback({ mode: 'widgets', primary: null });

    // Stream resumes via isElementVisible()
    assert.ok(inst.pc, 'stream should resume on widgets mode transition via element visibility');

    global.window.MirrormereLiveView.unmount('cam-tile');
    assert.equal(sseOffCalled, true, 'unmount should remove SSE listener');
  });
});

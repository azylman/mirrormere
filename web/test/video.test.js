const test = require('node:test');
const assert = require('node:assert/strict');
const { VideoPlayerManager } = require('../static/js/video.js');

/**
 * Lightweight mock DOM elements for hermetic Node.js testing.
 */
function createMockElement(tagName = 'div', id = '') {
  const listeners = new Map();
  const children = [];
  const attributes = new Map();

  const element = {
    tagName: tagName.toUpperCase(),
    id,
    className: '',
    style: {},
    dataset: {},
    children,
    parentNode: null,
    src: '',
    srcObject: null,
    autoplay: false,
    playsInline: false,
    muted: false,
    volume: 1.0,
    paused: false,
    isConnected: true,

    get innerHTML() {
      return '';
    },
    set innerHTML(val) {
      children.length = 0;
    },

    classList: {
      _classes: new Set(),
      add(...cls) { cls.forEach((c) => this._classes.add(c)); },
      remove(...cls) { cls.forEach((c) => this._classes.delete(c)); },
      contains(c) { return this._classes.has(c); },
    },

    appendChild(child) {
      child.parentNode = element;
      children.push(child);
      return child;
    },

    removeChild(child) {
      const idx = children.indexOf(child);
      if (idx !== -1) {
        children.splice(idx, 1);
        child.parentNode = null;
      }
      return child;
    },

    removeAttribute(attr) {
      attributes.delete(attr);
      if (attr === 'src') element.src = '';
    },

    setAttribute(attr, val) {
      attributes.set(attr, String(val));
      if (attr === 'src') element.src = String(val);
    },

    getAttribute(attr) {
      return attributes.get(attr) || null;
    },

    addEventListener(event, fn) {
      if (!listeners.has(event)) listeners.set(event, []);
      listeners.get(event).push(fn);
    },

    removeEventListener(event, fn) {
      if (listeners.has(event)) {
        const arr = listeners.get(event).filter((f) => f !== fn);
        listeners.set(event, arr);
      }
    },

    dispatchEvent(event, payload = {}) {
      const ev = { type: event, stopPropagation: () => {}, ...payload };
      const handlers = listeners.get(event) || [];
      for (const h of handlers) {
        h(ev);
      }
    },

    click() {
      element.dispatchEvent('click');
    },

    pause() {
      element.paused = true;
    },

    play() {
      element.paused = false;
      return Promise.resolve();
    },

    load() {
      // no-op mock
    },

    canPlayType(type) {
      return '';
    },
  };

  return element;
}

/**
 * Mock RTCPeerConnection for WebRTC testing.
 */
class MockRTCPeerConnection {
  constructor() {
    this.transceivers = [];
    this.ontrack = null;
    this.onicecandidate = null;
    this.oniceconnectionstatechange = null;
    this.onconnectionstatechange = null;
    this.iceGatheringState = 'complete';
    this.connectionState = 'new';
    this.iceConnectionState = 'new';
    this.localDescription = null;
    this.remoteDescription = null;
    this.closed = false;
    this._listeners = new Map();
  }

  addEventListener(event, fn) {
    if (!this._listeners.has(event)) this._listeners.set(event, []);
    this._listeners.get(event).push(fn);
  }

  removeEventListener(event, fn) {
    if (this._listeners.has(event)) {
      const arr = this._listeners.get(event).filter((f) => f !== fn);
      this._listeners.set(event, arr);
    }
  }

  dispatchEvent(event, payload = {}) {
    const ev = { type: event, ...payload };
    const handlers = this._listeners.get(event) || [];
    for (const h of handlers) {
      h(ev);
    }
  }

  setConnectionState(state) {
    this.connectionState = state;
    if (typeof this.onconnectionstatechange === 'function') {
      this.onconnectionstatechange({ type: 'connectionstatechange' });
    }
    this.dispatchEvent('connectionstatechange');
  }

  setIceConnectionState(state) {
    this.iceConnectionState = state;
    if (typeof this.oniceconnectionstatechange === 'function') {
      this.oniceconnectionstatechange({ type: 'iceconnectionstatechange' });
    }
    this.dispatchEvent('iceconnectionstatechange');
  }

  addTransceiver(kind, init) {
    const t = { kind, direction: init ? init.direction : 'sendrecv' };
    this.transceivers.push(t);
    return t;
  }

  async createOffer() {
    return { type: 'offer', sdp: 'v=0\r\no=- 12345 2 IN IP4 127.0.0.1\r\ns=Mirrormere' };
  }

  async setLocalDescription(desc) {
    this.localDescription = desc;
  }

  async setRemoteDescription(desc) {
    this.remoteDescription = desc;
  }

  close() {
    this.closed = true;
    this.connectionState = 'closed';
    this.iceConnectionState = 'closed';
  }
}

/**
 * Mock AudioManager complying with Chunk 4.1 AudioManager interface.
 */
function createMockAudioManager() {
  const registered = new Set();
  return {
    registered,
    registerMediaElement(el) {
      registered.add(el);
      if (el) el.volume = 0.60; // 75% master volume at 80% ceiling = 0.60
    },
    unregisterMediaElement(el) {
      registered.delete(el);
    },
  };
}

/**
 * Mock Carousel complying with MirrormereCarousel interface.
 */
function createMockCarousel() {
  return {
    paused: false,
    pause() { this.paused = true; },
    resume() { this.paused = false; },
  };
}

test('Video Presentation Mode & Multi-Stream Player with PiP', async (t) => {
  // Setup mock DOM environment
  const gridCanvas = createMockElement('main', 'grid-canvas');
  const mockBody = createMockElement('body');
  mockBody.offsetHeight = 1080;

  global.document = {
    body: mockBody,
    createElement(tag) { return createMockElement(tag); },
    getElementById(id) {
      if (id === 'grid-canvas') return gridCanvas;
      if (id === 'mm-compositor-heartbeat') {
        return mockBody.children.find((c) => c.id === 'mm-compositor-heartbeat') || null;
      }
      return null;
    },
  };

  await t.test('Multi-Stream Protocol Triad', async (t2) => {
    await t2.test('mounts MJPEG stream via responsive img element without audio', () => {
      const stage = createMockElement('div', 'video-stage');
      const primarySlot = createMockElement('div', 'video-primary-slot');
      const audioMgr = createMockAudioManager();

      const mgr = new VideoPlayerManager({
        stageElement: stage,
        primarySlot,
        audioManager: audioMgr,
      });

      mgr.handleVideoState({
        mode: 'video',
        primary: {
          id: 'doorbell_mjpeg',
          stream_url: 'http://cam.lan/mjpeg',
          type: 'mjpeg',
        },
      });

      assert.strictEqual(primarySlot.children.length, 2);
      const img = primarySlot.children[0];
      assert.strictEqual(img.tagName, 'IMG');
      assert.strictEqual(img.src, 'http://cam.lan/mjpeg');
      assert.strictEqual(img.className, 'video-stream-media');
      assert.strictEqual(primarySlot.children[1].className, 'video-stream-spinner');
      // MJPEG image should never register with AudioManager
      assert.strictEqual(audioMgr.registered.size, 0);

      // Clean exit tears down image
      mgr.exitVideoMode();
      assert.strictEqual(primarySlot.children.length, 0);
      assert.strictEqual(img.src, '');
    });

    await t2.test('mounts HLS stream via native video element', () => {
      const stage = createMockElement('div', 'video-stage');
      const primarySlot = createMockElement('div', 'video-primary-slot');
      const audioMgr = createMockAudioManager();

      const mgr = new VideoPlayerManager({
        stageElement: stage,
        primarySlot,
        audioManager: audioMgr,
      });

      mgr.handleVideoState({
        mode: 'video',
        primary: {
          id: 'stream_hls',
          stream_url: 'http://video.lan/stream.m3u8',
          type: 'hls',
        },
      });

      assert.strictEqual(primarySlot.children.length, 2);
      const video = primarySlot.children[0];
      assert.strictEqual(video.tagName, 'VIDEO');
      assert.strictEqual(video.src, 'http://video.lan/stream.m3u8');
      assert.strictEqual(primarySlot.children[1].className, 'video-stream-spinner');
      assert.strictEqual(video.muted, false);
      assert.strictEqual(audioMgr.registered.has(video), true);
      assert.strictEqual(video.volume, 0.60);

      mgr.exitVideoMode();
      assert.strictEqual(primarySlot.children.length, 0);
      assert.strictEqual(video.paused, true);
      assert.strictEqual(audioMgr.registered.has(video), false);
    });

    await t2.test('mounts WebRTC stream and negotiates SDP offer/answer with go2rtc endpoint', async () => {
      const stage = createMockElement('div', 'video-stage');
      const primarySlot = createMockElement('div', 'video-primary-slot');
      const audioMgr = createMockAudioManager();

      let fetchCalledWith = null;
      const mockFetch = async (url, opts) => {
        fetchCalledWith = { url, opts };
        return {
          ok: true,
          status: 200,
          text: async () => 'v=0\r\ns=go2rtc answer',
        };
      };

      const mgr = new VideoPlayerManager({
        stageElement: stage,
        primarySlot,
        audioManager: audioMgr,
        RTCPeerConnection: MockRTCPeerConnection,
        fetch: mockFetch,
      });

      mgr.handleVideoState({
        mode: 'video',
        primary: {
          id: 'chromecast',
          stream_url: 'http://127.0.0.1:1984/cast',
          type: 'webrtc',
        },
      });

      assert.strictEqual(primarySlot.children.length, 2);
      const video = primarySlot.children[0];
      assert.strictEqual(video.tagName, 'VIDEO');
      assert.strictEqual(primarySlot.children[1].className, 'video-stream-spinner');
      assert.strictEqual(video.muted, false);
      assert.strictEqual(audioMgr.registered.has(video), true);

      // Yield event loop to allow async SDP negotiation
      await new Promise((r) => setImmediate(r));

      assert.ok(fetchCalledWith);
      assert.strictEqual(fetchCalledWith.url, 'http://127.0.0.1:1984/cast');
      assert.strictEqual(fetchCalledWith.opts.method, 'POST');
      assert.ok(fetchCalledWith.opts.body.includes('v=0'));

      const state = mgr.getState();
      assert.strictEqual(state.hasPrimarySession, true);

      mgr.exitVideoMode();
      assert.strictEqual(mgr.getState().hasPrimarySession, false);
      assert.strictEqual(audioMgr.registered.has(video), false);
    });

    await t2.test('parses JSON SDP answer payload from go2rtc endpoints', async () => {
      const stage = createMockElement('div', 'video-stage');
      const primarySlot = createMockElement('div', 'video-primary-slot');

      const mockFetch = async () => ({
        ok: true,
        status: 200,
        text: async () => JSON.stringify({ type: 'answer', sdp: 'v=0\r\ns=json-answer' }),
      });

      const mgr = new VideoPlayerManager({
        stageElement: stage,
        primarySlot,
        RTCPeerConnection: MockRTCPeerConnection,
        fetch: mockFetch,
      });

      mgr.handleVideoState({
        mode: 'video',
        primary: { id: 'stream1', stream_url: 'http://cast/ws', type: 'webrtc' },
      });

      await new Promise((r) => setImmediate(r));
      assert.strictEqual(mgr.primarySession.pc.remoteDescription.sdp, 'v=0\r\ns=json-answer');
      mgr.exitVideoMode();
    });
  });

  await t.test('Picture-in-Picture (PiP) Slot & Strict Audio Muting Isolation', async () => {
    const stage = createMockElement('div', 'video-stage');
    const primarySlot = createMockElement('div', 'video-primary-slot');
    const pipSlot = createMockElement('div', 'video-pip-slot');
    const audioMgr = createMockAudioManager();

    const mgr = new VideoPlayerManager({
      stageElement: stage,
      primarySlot,
      pipSlot,
      audioManager: audioMgr,
      RTCPeerConnection: MockRTCPeerConnection,
      fetch: async () => ({ ok: true, text: async () => 'sdp' }),
    });

    mgr.handleVideoState({
      mode: 'video',
      primary: { id: 'chromecast', stream_url: 'http://cast', type: 'webrtc' },
      pip: { id: 'doorbell', stream_url: 'http://doorbell', type: 'webrtc', muted: true },
    });

    assert.strictEqual(pipSlot.style.display, 'flex');
    assert.strictEqual(pipSlot.children.length, 1);
    const pipVideo = pipSlot.children[0];

    // PiP stream MUST be strictly muted and NOT registered with AudioManager
    assert.strictEqual(pipVideo.muted, true);
    assert.strictEqual(pipVideo.volume, 0);
    assert.strictEqual(audioMgr.registered.has(pipVideo), false);

    // Primary video stream is registered and unmuted
    const primaryVideo = primarySlot.children[0];
    assert.strictEqual(primaryVideo.muted, false);
    assert.strictEqual(audioMgr.registered.has(primaryVideo), true);

    mgr.exitVideoMode();
  });

  await t.test('Zero-Reparenting Interactive Swap-on-Tap & Audio Handover', async () => {
    const stage = createMockElement('div', 'video-stage');
    const primarySlot = createMockElement('div', 'video-primary-slot');
    const pipSlot = createMockElement('div', 'video-pip-slot');
    const audioMgr = createMockAudioManager();

    const mgr = new VideoPlayerManager({
      stageElement: stage,
      primarySlot,
      pipSlot,
      audioManager: audioMgr,
      RTCPeerConnection: MockRTCPeerConnection,
      fetch: async () => ({ ok: true, text: async () => 'sdp' }),
    });

    mgr.handleVideoState({
      mode: 'video',
      primary: { id: 'chromecast', stream_url: 'http://cast', type: 'webrtc' },
      pip: { id: 'doorbell', stream_url: 'http://doorbell', type: 'webrtc', muted: true },
    });

    const primaryVideo = primarySlot.children[0];
    const pipVideo = pipSlot.children[0];

    // Initial state: not swapped
    assert.strictEqual(stage.dataset.swapped, undefined);
    assert.strictEqual(audioMgr.registered.has(primaryVideo), true);
    assert.strictEqual(audioMgr.registered.has(pipVideo), false);
    assert.strictEqual(primaryVideo.muted, false);
    assert.strictEqual(pipVideo.muted, true);

    // 1. User taps PiP dock to swap
    pipSlot.click();

    // Zero DOM reparenting: elements remain in their original slots
    assert.strictEqual(primarySlot.children[0], primaryVideo);
    assert.strictEqual(pipSlot.children[0], pipVideo);

    // CSS state inversion attribute set
    assert.strictEqual(stage.dataset.swapped, 'true');
    assert.strictEqual(mgr.isSwapped, true);

    // Audio focus handed over: Doorbell (PiP element) is now unmuted & registered
    assert.strictEqual(audioMgr.registered.has(pipVideo), true);
    assert.strictEqual(pipVideo.muted, false);
    assert.strictEqual(pipVideo.volume, 0.60);

    // Chromecast (Primary element) is now demoted to muted PiP role
    assert.strictEqual(audioMgr.registered.has(primaryVideo), false);
    assert.strictEqual(primaryVideo.muted, true);
    assert.strictEqual(primaryVideo.volume, 0);

    // 2. User taps again on primarySlot (which visually renders the PiP dock while swapped)
    primarySlot.click();

    assert.strictEqual(stage.dataset.swapped, 'false');
    assert.strictEqual(mgr.isSwapped, false);
    assert.strictEqual(audioMgr.registered.has(primaryVideo), true);
    assert.strictEqual(audioMgr.registered.has(pipVideo), false);
    assert.strictEqual(primaryVideo.muted, false);
    assert.strictEqual(pipVideo.muted, true);

    mgr.exitVideoMode();
  });

  await t.test('Presentation Mode Switching and Carousel Coordination', async () => {
    const stage = createMockElement('div', 'video-stage');
    const primarySlot = createMockElement('div', 'video-primary-slot');
    const pipSlot = createMockElement('div', 'video-pip-slot');
    const carousel = createMockCarousel();

    const mgr = new VideoPlayerManager({
      stageElement: stage,
      primarySlot,
      pipSlot,
      carousel,
      RTCPeerConnection: MockRTCPeerConnection,
      fetch: async () => ({ ok: true, text: async () => 'sdp' }),
    });

    // 1. Transition from widgets to video
    mgr.handleVideoState({
      mode: 'video',
      primary: { id: 'chromecast', stream_url: 'http://cast', type: 'webrtc' },
    });

    assert.strictEqual(mgr.currentMode, 'video');
    assert.strictEqual(carousel.paused, true);
    assert.strictEqual(gridCanvas.style.display, 'none');
    assert.strictEqual(stage.style.display, 'flex');
    assert.strictEqual(stage.classList.contains('active'), true);

    // 2. Transition back to widgets
    mgr.handleVideoState({
      mode: 'widgets',
      primary: null,
      pip: null,
    });

    assert.strictEqual(mgr.currentMode, 'widgets');
    assert.strictEqual(carousel.paused, false);
    assert.strictEqual(gridCanvas.style.display, '');
    assert.strictEqual(stage.style.display, 'none');
    assert.strictEqual(stage.classList.contains('active'), false);
    assert.strictEqual(primarySlot.children.length, 0);
  });

  await t.test('State Reconciliation: Dismissing PiP resets swap without interrupting primary stream', async () => {
    const stage = createMockElement('div', 'video-stage');
    const primarySlot = createMockElement('div', 'video-primary-slot');
    const pipSlot = createMockElement('div', 'video-pip-slot');
    const audioMgr = createMockAudioManager();

    const mgr = new VideoPlayerManager({
      stageElement: stage,
      primarySlot,
      pipSlot,
      audioManager: audioMgr,
      RTCPeerConnection: MockRTCPeerConnection,
      fetch: async () => ({ ok: true, text: async () => 'sdp' }),
    });

    // Both active
    mgr.handleVideoState({
      mode: 'video',
      primary: { id: 'chromecast', stream_url: 'http://cast', type: 'webrtc' },
      pip: { id: 'doorbell', stream_url: 'http://doorbell', type: 'webrtc' },
    });

    // User swaps
    pipSlot.click();
    assert.strictEqual(mgr.isSwapped, true);

    // Server dismisses PiP (doorbell alert timer expired)
    mgr.handleVideoState({
      mode: 'video',
      primary: { id: 'chromecast', stream_url: 'http://cast', type: 'webrtc' },
      pip: null,
    });

    // Swap state must reset cleanly
    assert.strictEqual(mgr.isSwapped, false);
    assert.strictEqual(stage.dataset.swapped, 'false');
    assert.strictEqual(pipSlot.style.display, 'none');
    assert.strictEqual(pipSlot.children.length, 0);

    // Primary audio restored to unmuted
    const primaryVideo = primarySlot.children[0];
    assert.strictEqual(primaryVideo.muted, false);
    assert.strictEqual(audioMgr.registered.has(primaryVideo), true);

    mgr.exitVideoMode();
  });

  await t.test('Session Cancellation Token: Cancel async SDP negotiation if session is torn down mid-flight', async () => {
    const stage = createMockElement('div', 'video-stage');
    const primarySlot = createMockElement('div', 'video-primary-slot');

    let resolveFetch = null;
    const slowFetch = () => new Promise((resolve) => { resolveFetch = resolve; });

    let createdPC = null;
    class TrackablePC extends MockRTCPeerConnection {
      constructor() {
        super();
        createdPC = this;
      }
    }

    const mgr = new VideoPlayerManager({
      stageElement: stage,
      primarySlot,
      RTCPeerConnection: TrackablePC,
      fetch: slowFetch,
    });

    mgr.handleVideoState({
      mode: 'video',
      primary: { id: 'stream1', stream_url: 'http://slow', type: 'webrtc' },
    });

    // Immediately exit video mode before fetch resolves
    mgr.exitVideoMode();
    assert.strictEqual(createdPC.closed, true);

    // Resolve delayed fetch
    if (resolveFetch) {
      resolveFetch({ ok: true, text: async () => 'sdp' });
    }
    await new Promise((r) => setImmediate(r));

    // Connection remains closed and not resurrected
    assert.strictEqual(createdPC.closed, true);
    assert.strictEqual(primarySlot.children.length, 0);
  });

  await t.test('Session Cancellation Token: Unchanged stream metadata/player_state updates mid-negotiation do NOT abort negotiation', async () => {
    const stage = createMockElement('div', 'video-stage');
    const primarySlot = createMockElement('div', 'video-primary-slot');
    const pipSlot = createMockElement('div', 'video-pip-slot');

    let resolveFetch = null;
    const slowFetch = () => new Promise((resolve) => { resolveFetch = resolve; });

    let createdPC = null;
    class TrackablePC extends MockRTCPeerConnection {
      constructor() {
        super();
        createdPC = this;
      }
    }

    const mgr = new VideoPlayerManager({
      stageElement: stage,
      primarySlot,
      pipSlot,
      RTCPeerConnection: TrackablePC,
      fetch: slowFetch,
    });

    // Mount primary stream
    mgr.handleVideoState({
      mode: 'video',
      primary: { id: 'chromecast', stream_url: 'http://cast/webrtc', type: 'webrtc', player_state: 'buffering' },
    });

    assert.ok(createdPC);
    assert.strictEqual(createdPC.closed, false);

    // Yield to let negotiation reach in-flight fetch
    await new Promise((r) => setImmediate(r));
    assert.ok(resolveFetch, 'slowFetch should be in-flight');

    // Cast-watcher reports transition from buffering to playing mid-negotiation
    mgr.handleVideoState({
      mode: 'video',
      primary: { id: 'chromecast', stream_url: 'http://cast/webrtc', type: 'webrtc', player_state: 'playing' },
    });

    // Peer connection must NOT be closed because the stream is unchanged
    assert.strictEqual(createdPC.closed, false);

    // Secondary PiP alert arrives mid-negotiation as well
    mgr.handleVideoState({
      mode: 'video',
      primary: { id: 'chromecast', stream_url: 'http://cast/webrtc', type: 'webrtc', player_state: 'playing' },
      pip: { id: 'doorbell', stream_url: 'http://doorbell/mjpeg', type: 'mjpeg' },
    });

    // Primary peer connection still must NOT be closed
    assert.strictEqual(createdPC.closed, false);

    // Now resolve the in-flight SDP fetch
    resolveFetch({ ok: true, status: 200, text: async () => 'v=0\r\ns=cast-answer' });
    await new Promise((r) => setImmediate(r));

    // Negotiation succeeded! Remote description applied and connection remained open
    assert.strictEqual(createdPC.closed, false);
    assert.ok(createdPC.remoteDescription);
    assert.strictEqual(createdPC.remoteDescription.sdp, 'v=0\r\ns=cast-answer');

    mgr.exitVideoMode();
    assert.strictEqual(createdPC.closed, true);
  });

  await t.test('Session Cancellation Token: Replaced stream mid-negotiation aborts stale negotiation and mounts new stream', async () => {
    const stage = createMockElement('div', 'video-stage');
    const primarySlot = createMockElement('div', 'video-primary-slot');

    let resolveFetch1 = null;
    let resolveFetch2 = null;
    const mockFetch = (url) => {
      if (url === 'http://stream1/webrtc') {
        return new Promise((r) => { resolveFetch1 = r; });
      }
      return new Promise((r) => { resolveFetch2 = r; });
    };

    const pcs = [];
    class TrackablePC extends MockRTCPeerConnection {
      constructor() {
        super();
        pcs.push(this);
      }
    }

    const mgr = new VideoPlayerManager({
      stageElement: stage,
      primarySlot,
      RTCPeerConnection: TrackablePC,
      fetch: mockFetch,
    });

    mgr.handleVideoState({
      mode: 'video',
      primary: { id: 'stream1', stream_url: 'http://stream1/webrtc', type: 'webrtc' },
    });

    assert.strictEqual(pcs.length, 1);
    const pc1 = pcs[0];
    assert.strictEqual(pc1.closed, false);

    // Yield to let stream1 negotiation reach in-flight fetch
    await new Promise((r) => setImmediate(r));
    assert.ok(resolveFetch1, 'resolveFetch1 should be in-flight');

    // Replace primary with stream2 while stream1 negotiation is in-flight
    mgr.handleVideoState({
      mode: 'video',
      primary: { id: 'stream2', stream_url: 'http://stream2/webrtc', type: 'webrtc' },
    });

    // Old pc1 must be torn down and cancelled
    assert.strictEqual(pc1.closed, true);
    assert.strictEqual(pcs.length, 2);
    const pc2 = pcs[1];
    assert.strictEqual(pc2.closed, false);

    // Yield to let stream2 negotiation reach in-flight fetch
    await new Promise((r) => setImmediate(r));
    assert.ok(resolveFetch2, 'resolveFetch2 should be in-flight');

    // Resolve stream1 fetch late - must be ignored and not touch anything
    resolveFetch1({ ok: true, status: 200, text: async () => 'v=0\r\ns=stale-answer' });
    await new Promise((r) => setImmediate(r));
    assert.strictEqual(pc1.remoteDescription, null);

    // Resolve stream2 fetch
    resolveFetch2({ ok: true, status: 200, text: async () => 'v=0\r\ns=stream2-answer' });
    await new Promise((r) => setImmediate(r));
    assert.strictEqual(pc2.closed, false);
    assert.strictEqual(pc2.remoteDescription.sdp, 'v=0\r\ns=stream2-answer');

    mgr.exitVideoMode();
    assert.strictEqual(pc2.closed, true);
  });

  await t.test('WebRTC Stream Watchdog, Auto-Reconnect & Widget Fallback', async (t2) => {
    await t2.test('auto-reconnects with backoff when connectionState transitions to failed', async () => {
      const stage = createMockElement('div', 'video-stage');
      const primarySlot = createMockElement('div', 'video-primary-slot');

      const pcs = [];
      class MultiPC extends MockRTCPeerConnection {
        constructor() {
          super();
          pcs.push(this);
        }
      }

      const mgr = new VideoPlayerManager({
        stageElement: stage,
        primarySlot,
        RTCPeerConnection: MultiPC,
        reconnectBaseDelay: 20,
        reconnectBackoffFactor: 1.0,
        fetch: async () => ({ ok: true, status: 200, text: async () => 'v=0\r\ns=answer' }),
      });

      mgr.handleVideoState({
        mode: 'video',
        primary: { id: 'stream1', stream_url: 'http://cast/webrtc', type: 'webrtc' },
      });

      await new Promise((r) => setImmediate(r));
      assert.strictEqual(pcs.length, 1);
      const pc1 = pcs[0];

      // Simulate go2rtc crash mid-stream: connection fails
      pc1.setConnectionState('failed');

      // Wait for backoff timer and reconnection
      await new Promise((r) => setTimeout(r, 60));

      assert.strictEqual(pc1.closed, true);
      assert.strictEqual(pcs.length, 2);
      const pc2 = pcs[1];
      assert.strictEqual(pc2.closed, false);
      assert.strictEqual(mgr.primarySession.pc, pc2);
      assert.strictEqual(pc2.remoteDescription.sdp, 'v=0\r\ns=answer');

      mgr.exitVideoMode();
    });

    await t2.test('reconnection resets reconnectAttempts back to 0 on connected/completed', async () => {
      const stage = createMockElement('div', 'video-stage');
      const primarySlot = createMockElement('div', 'video-primary-slot');

      const pcs = [];
      class MultiPC extends MockRTCPeerConnection {
        constructor() {
          super();
          pcs.push(this);
        }
      }

      const mgr = new VideoPlayerManager({
        stageElement: stage,
        primarySlot,
        RTCPeerConnection: MultiPC,
        reconnectBaseDelay: 20,
        reconnectBackoffFactor: 1.0,
        fetch: async () => ({ ok: true, status: 200, text: async () => 'v=0\r\ns=answer' }),
      });

      mgr.handleVideoState({
        mode: 'video',
        primary: { id: 'stream1', stream_url: 'http://cast/webrtc', type: 'webrtc' },
      });

      await new Promise((r) => setImmediate(r));
      const pc1 = pcs[0];

      pc1.setConnectionState('failed');
      await new Promise((r) => setTimeout(r, 60));

      const pc2 = pcs[1];
      assert.strictEqual(mgr.primarySession.reconnectAttempts, 1);

      // pc2 successfully establishes and transitions to connected
      pc2.setConnectionState('connected');
      assert.strictEqual(mgr.primarySession.reconnectAttempts, 0);

      mgr.exitVideoMode();
    });

    await t2.test('transient disconnected state uses grace period and cancels if reconnected before timeout', async () => {
      const stage = createMockElement('div', 'video-stage');
      const primarySlot = createMockElement('div', 'video-primary-slot');

      const pcs = [];
      class MultiPC extends MockRTCPeerConnection {
        constructor() {
          super();
          pcs.push(this);
        }
      }

      const mgr = new VideoPlayerManager({
        stageElement: stage,
        primarySlot,
        RTCPeerConnection: MultiPC,
        disconnectTimeout: 50,
        reconnectBaseDelay: 20,
        fetch: async () => ({ ok: true, status: 200, text: async () => 'v=0\r\ns=answer' }),
      });

      mgr.handleVideoState({
        mode: 'video',
        primary: { id: 'stream1', stream_url: 'http://cast/webrtc', type: 'webrtc' },
      });

      await new Promise((r) => setImmediate(r));
      const pc1 = pcs[0];

      // Disconnect starts grace period
      pc1.setConnectionState('disconnected');
      assert.strictEqual(pcs.length, 1);

      // Reconnects quickly after 15ms before grace timer expires
      await new Promise((r) => setTimeout(r, 15));
      pc1.setConnectionState('connected');

      // Wait beyond the original 50ms disconnectTimeout
      await new Promise((r) => setTimeout(r, 60));

      // No new connection created; pc1 is still alive
      assert.strictEqual(pcs.length, 1);
      assert.strictEqual(pc1.closed, false);
      assert.strictEqual(mgr.primarySession.pc, pc1);

      mgr.exitVideoMode();
    });

    await t2.test('transient disconnected state triggers reconnect if timeout expires', async () => {
      const stage = createMockElement('div', 'video-stage');
      const primarySlot = createMockElement('div', 'video-primary-slot');

      const pcs = [];
      class MultiPC extends MockRTCPeerConnection {
        constructor() {
          super();
          pcs.push(this);
        }
      }

      const mgr = new VideoPlayerManager({
        stageElement: stage,
        primarySlot,
        RTCPeerConnection: MultiPC,
        disconnectTimeout: 20,
        reconnectBaseDelay: 20,
        reconnectBackoffFactor: 1.0,
        fetch: async () => ({ ok: true, status: 200, text: async () => 'v=0\r\ns=answer' }),
      });

      mgr.handleVideoState({
        mode: 'video',
        primary: { id: 'stream1', stream_url: 'http://cast/webrtc', type: 'webrtc' },
      });

      await new Promise((r) => setImmediate(r));
      const pc1 = pcs[0];

      pc1.setIceConnectionState('disconnected');

      // Wait for 20ms disconnect timeout + 20ms reconnect delay
      await new Promise((r) => setTimeout(r, 70));

      assert.strictEqual(pc1.closed, true);
      assert.strictEqual(pcs.length, 2);
      assert.strictEqual(mgr.primarySession.pc, pcs[1]);

      mgr.exitVideoMode();
    });

    await t2.test('cleanly drops back to widgets mode when reconnect attempts are exhausted', async () => {
      const stage = createMockElement('div', 'video-stage');
      const primarySlot = createMockElement('div', 'video-primary-slot');
      const carousel = createMockCarousel();

      const pcs = [];
      class MultiPC extends MockRTCPeerConnection {
        constructor() {
          super();
          pcs.push(this);
        }
      }

      let fetchCallCount = 0;
      const mockFetch = async () => {
        fetchCallCount++;
        if (fetchCallCount === 1) {
          return { ok: true, status: 200, text: async () => 'v=0\r\ns=answer' };
        }
        // Subsequent reconnect attempts fail (go2rtc down)
        return { ok: false, status: 502, text: async () => 'Bad Gateway' };
      };

      let fallbackNotified = null;
      const mgr = new VideoPlayerManager({
        stageElement: stage,
        primarySlot,
        carousel,
        RTCPeerConnection: MultiPC,
        maxReconnectAttempts: 2,
        reconnectBaseDelay: 15,
        reconnectBackoffFactor: 1.0,
        fetch: mockFetch,
        onFallback: (session, reason) => {
          fallbackNotified = { session, reason };
        },
      });

      mgr.handleVideoState({
        mode: 'video',
        primary: { id: 'chromecast', stream_url: 'http://127.0.0.1:1984/cast', type: 'webrtc' },
      });

      await new Promise((r) => setImmediate(r));
      assert.strictEqual(mgr.currentMode, 'video');
      assert.strictEqual(carousel.paused, true);
      assert.strictEqual(gridCanvas.style.display, 'none');

      const pc1 = pcs[0];
      // Mid-stream go2rtc process dies
      pc1.setConnectionState('failed');

      // Wait for 2 retries (15ms * 2 + overhead)
      await new Promise((r) => setTimeout(r, 100));

      // Must have cleanly dropped back to widgets mode!
      assert.strictEqual(mgr.currentMode, 'widgets');
      assert.strictEqual(mgr.getState().hasPrimarySession, false);
      assert.strictEqual(primarySlot.children.length, 0);
      assert.strictEqual(stage.style.display, 'none');
      assert.strictEqual(gridCanvas.style.display, '');
      assert.strictEqual(carousel.paused, false);
      assert.ok(fallbackNotified);
      assert.strictEqual(fallbackNotified.reason, 'widgets');
    });

    await t2.test('PiP stream failure tears down only PiP without disrupting primary presentation', async () => {
      const stage = createMockElement('div', 'video-stage');
      const primarySlot = createMockElement('div', 'video-primary-slot');
      const pipSlot = createMockElement('div', 'video-pip-slot');
      const audioMgr = createMockAudioManager();

      const pcs = [];
      class MultiPC extends MockRTCPeerConnection {
        constructor() {
          super();
          pcs.push(this);
        }
      }

      let pipFetches = 0;
      const mockFetch = async (url) => {
        if (url.includes('doorbell')) {
          pipFetches++;
          if (pipFetches > 1) {
            return { ok: false, status: 503, text: async () => 'Unavailable' };
          }
        }
        return { ok: true, status: 200, text: async () => 'v=0\r\ns=answer' };
      };

      const mgr = new VideoPlayerManager({
        stageElement: stage,
        primarySlot,
        pipSlot,
        audioManager: audioMgr,
        RTCPeerConnection: MultiPC,
        maxReconnectAttempts: 1,
        reconnectBaseDelay: 15,
        fetch: mockFetch,
      });

      mgr.handleVideoState({
        mode: 'video',
        primary: { id: 'chromecast', stream_url: 'http://cast/webrtc', type: 'webrtc' },
        pip: { id: 'doorbell', stream_url: 'http://doorbell/webrtc', type: 'webrtc' },
      });

      await new Promise((r) => setImmediate(r));
      assert.strictEqual(pcs.length, 2);
      const pipPC = pcs[1];

      // PiP connection fails
      pipPC.setConnectionState('failed');

      // Wait for retry to exhaust
      await new Promise((r) => setTimeout(r, 60));

      // Video presentation remains active for primary
      assert.strictEqual(mgr.currentMode, 'video');
      assert.strictEqual(mgr.getState().hasPrimarySession, true);
      assert.strictEqual(mgr.getState().hasPipSession, false);
      assert.strictEqual(pipSlot.style.display, 'none');
      assert.strictEqual(pipSlot.children.length, 0);

      // Primary stream remains registered and unmuted
      const primaryVideo = primarySlot.children[0];
      assert.strictEqual(primaryVideo.muted, false);
      assert.strictEqual(audioMgr.registered.has(primaryVideo), true);

      mgr.exitVideoMode();
    });

    await t2.test('teardownSession cancels active reconnect and disconnect timers immediately', async () => {
      const stage = createMockElement('div', 'video-stage');
      const primarySlot = createMockElement('div', 'video-primary-slot');

      const pcs = [];
      class MultiPC extends MockRTCPeerConnection {
        constructor() {
          super();
          pcs.push(this);
        }
      }

      const mgr = new VideoPlayerManager({
        stageElement: stage,
        primarySlot,
        RTCPeerConnection: MultiPC,
        reconnectBaseDelay: 50,
        fetch: async () => ({ ok: true, status: 200, text: async () => 'v=0\r\ns=answer' }),
      });

      mgr.handleVideoState({
        mode: 'video',
        primary: { id: 'stream1', stream_url: 'http://cast/webrtc', type: 'webrtc' },
      });

      await new Promise((r) => setImmediate(r));
      const pc1 = pcs[0];

      // Trigger failure to schedule reconnect
      pc1.setConnectionState('failed');

      // Immediately exit video mode before reconnect timer fires
      mgr.exitVideoMode();
      assert.strictEqual(pc1.closed, true);

      // Wait beyond the 50ms reconnectBaseDelay
      await new Promise((r) => setTimeout(r, 80));

      // No new peer connection should have been created!
      assert.strictEqual(pcs.length, 1);
      assert.strictEqual(mgr.currentMode, 'widgets');
      assert.strictEqual(primarySlot.children.length, 0);
    });

    await t2.test('aborts and triggers reconnect if initial SDP negotiation fails with HTTP error', async () => {
      const stage = createMockElement('div', 'video-stage');
      const primarySlot = createMockElement('div', 'video-primary-slot');

      const pcs = [];
      class MultiPC extends MockRTCPeerConnection {
        constructor() {
          super();
          pcs.push(this);
        }
      }

      let fetchCount = 0;
      const mockFetch = async () => {
        fetchCount++;
        if (fetchCount === 1) {
          // Initial negotiation fails (e.g. go2rtc starting up)
          return { ok: false, status: 502, text: async () => 'Bad Gateway' };
        }
        return { ok: true, status: 200, text: async () => 'v=0\r\ns=recovered-answer' };
      };

      const mgr = new VideoPlayerManager({
        stageElement: stage,
        primarySlot,
        RTCPeerConnection: MultiPC,
        maxReconnectAttempts: 2,
        reconnectBaseDelay: 20,
        reconnectBackoffFactor: 1.0,
        fetch: mockFetch,
      });

      mgr.handleVideoState({
        mode: 'video',
        primary: { id: 'stream1', stream_url: 'http://cast/webrtc', type: 'webrtc' },
      });

      // Wait for initial failure + 20ms reconnect
      await new Promise((r) => setTimeout(r, 60));

      assert.strictEqual(pcs.length, 2);
      const pc2 = pcs[1];
      assert.strictEqual(pc2.closed, false);
      assert.strictEqual(pc2.remoteDescription.sdp, 'v=0\r\ns=recovered-answer');

      mgr.exitVideoMode();
    });
  });
  await t.test('Video Stream Loading Spinner & Wake Animation Lifecycle', async (t2) => {
    await t2.test('creates spinner DOM element when mounting primary stream but not PiP', () => {
      const stage = createMockElement('div', 'video-stage');
      const primarySlot = createMockElement('div', 'video-primary-slot');
      const pipSlot = createMockElement('div', 'video-pip-slot');

      const mgr = new VideoPlayerManager({
        stageElement: stage,
        primarySlot,
        pipSlot,
        RTCPeerConnection: MockRTCPeerConnection,
      });

      mgr.handleVideoState({
        mode: 'video',
        primary: { id: 'stream-primary', stream_url: 'http://cast/webrtc', type: 'webrtc' },
        pip: { id: 'stream-pip', stream_url: 'http://pip/webrtc', type: 'webrtc' },
      });

      // Primary stream container has media + spinner
      assert.strictEqual(primarySlot.children.length, 2);
      const spinner = primarySlot.children[1];
      assert.strictEqual(spinner.className, 'video-stream-spinner');
      assert.ok(mgr.primarySession.spinner);
      assert.strictEqual(mgr.primarySession.spinner, spinner);

      // PiP container has media ONLY (no spinner)
      assert.strictEqual(pipSlot.children.length, 1);
      assert.strictEqual(mgr.pipSession.spinner, undefined);

      mgr.exitVideoMode();
    });

    await t2.test('dismisses spinner on playing and loadeddata events', async () => {
      const stage = createMockElement('div', 'video-stage');
      const primarySlot = createMockElement('div', 'video-primary-slot');

      const mgr = new VideoPlayerManager({
        stageElement: stage,
        primarySlot,
        RTCPeerConnection: MockRTCPeerConnection,
      });

      mgr.handleVideoState({
        mode: 'video',
        primary: { id: 'stream-primary', stream_url: 'http://cast/webrtc', type: 'webrtc' },
      });

      const video = primarySlot.children[0];
      const spinner = primarySlot.children[1];
      assert.strictEqual(spinner.classList.contains('fade-out'), false);

      // Fire playing event
      video.dispatchEvent('playing');
      assert.strictEqual(spinner.classList.contains('fade-out'), true);

      // Wait 450ms for fade-out timeout removal
      await new Promise((r) => setTimeout(r, 450));
      assert.strictEqual(primarySlot.children.length, 1);
      assert.strictEqual(primarySlot.children[0], video);

      mgr.exitVideoMode();
    });

    await t2.test('cleans up spinner and safety timer on session teardown', () => {
      const stage = createMockElement('div', 'video-stage');
      const primarySlot = createMockElement('div', 'video-primary-slot');

      const mgr = new VideoPlayerManager({
        stageElement: stage,
        primarySlot,
        RTCPeerConnection: MockRTCPeerConnection,
      });

      mgr.handleVideoState({
        mode: 'video',
        primary: { id: 'stream-primary', stream_url: 'http://cast/webrtc', type: 'webrtc' },
      });

      assert.ok(mgr.primarySession.spinnerSafetyTimer);
      assert.strictEqual(primarySlot.children.length, 2);

      // Teardown session directly
      mgr.teardownSession(mgr.primarySession);

      assert.strictEqual(primarySlot.children.length, 0);
    });

    await t2.test('dismisses spinner on WebRTC connection failure or disconnect', () => {
      const stage = createMockElement('div', 'video-stage');
      const primarySlot = createMockElement('div', 'video-primary-slot');

      const mgr = new VideoPlayerManager({
        stageElement: stage,
        primarySlot,
        RTCPeerConnection: MockRTCPeerConnection,
      });

      mgr.handleVideoState({
        mode: 'video',
        primary: { id: 'stream-primary', stream_url: 'http://cast/webrtc', type: 'webrtc' },
      });

      const spinner = mgr.primarySession.spinner;
      assert.strictEqual(spinner.classList.contains('fade-out'), false);

      // Transition connection state to failed
      mgr.primarySession.pc.setConnectionState('failed');
      assert.strictEqual(spinner.classList.contains('fade-out'), true);

      mgr.exitVideoMode();
    });
  });

  await t.test('Wayland/Cage Compositor Deadlock Prevention & Micro-Damage Heartbeat', async (t2) => {
    await t2.test('creates and appends compositor heartbeat element to document.body when missing', () => {
      mockBody.children.length = 0;
      const stage = createMockElement('div', 'video-stage');
      const mgr = new VideoPlayerManager({ stageElement: stage });

      const hb = mgr.ensureCompositorHeartbeat();
      assert.ok(hb);
      assert.strictEqual(hb.id, 'mm-compositor-heartbeat');
      assert.strictEqual(hb.className, 'mm-compositor-heartbeat');
      assert.strictEqual(hb.getAttribute('aria-hidden'), 'true');
      assert.strictEqual(mockBody.children.includes(hb), true);

      // Subsequent call strictly reuses element without duplicate append
      const hb2 = mgr.ensureCompositorHeartbeat();
      assert.strictEqual(hb, hb2);
      assert.strictEqual(mockBody.children.filter((c) => c.id === 'mm-compositor-heartbeat').length, 1);
      mgr.destroy();
    });

    await t2.test('flushCompositorPaint forces layout reflow and schedules multi-frame damage style toggles', async () => {
      mockBody.children.length = 0;
      const rAFCallbacks = [];
      const origRAF = global.requestAnimationFrame;
      global.requestAnimationFrame = (cb) => {
        rAFCallbacks.push(cb);
        return rAFCallbacks.length;
      };

      try {
        const stage = createMockElement('div', 'video-stage');
        const mgr = new VideoPlayerManager({ stageElement: stage });
        const hb = mgr.ensureCompositorHeartbeat();

        mgr.flushCompositorPaint();
        assert.ok(mgr.paintFlushTimer);

        // Frame 1
        assert.strictEqual(rAFCallbacks.length, 1);
        rAFCallbacks[0]();
        assert.strictEqual(hb.dataset.flush, '1');
        assert.strictEqual(hb.style.transform, 'translateZ(0) scale(1.001)');

        // Frame 2
        assert.strictEqual(rAFCallbacks.length, 2);
        rAFCallbacks[1]();
        assert.strictEqual(hb.dataset.flush, '2');
        assert.strictEqual(hb.style.transform, 'translateZ(0) scale(1)');

        // Settle
        assert.strictEqual(mgr.paintFlushTimer, null);
        mgr.destroy();
      } finally {
        global.requestAnimationFrame = origRAF;
      }
    });

    await t2.test('exitVideoMode triggers flushCompositorPaint to release pending Wayland frame callbacks', () => {
      mockBody.children.length = 0;
      const stage = createMockElement('div', 'video-stage');
      const primarySlot = createMockElement('div', 'video-primary-slot');
      const mgr = new VideoPlayerManager({
        stageElement: stage,
        primarySlot,
      });

      let flushCalled = false;
      mgr.flushCompositorPaint = () => { flushCalled = true; };

      mgr.handleVideoState({
        mode: 'video',
        primary: { id: 'stream-primary', stream_url: 'http://cam/mjpeg', type: 'mjpeg' },
      });
      assert.strictEqual(mgr.currentMode, 'video');

      mgr.exitVideoMode();
      assert.strictEqual(mgr.currentMode, 'widgets');
      assert.strictEqual(flushCalled, true);
      mgr.destroy();
    });

    await t2.test('destroy cleanly cancels active paintFlushTimer', () => {
      mockBody.children.length = 0;
      const stage = createMockElement('div', 'video-stage');
      let cancelledTimer = null;
      const origCancelRAF = global.cancelAnimationFrame;
      global.cancelAnimationFrame = (id) => { cancelledTimer = id; };

      try {
        const mgr = new VideoPlayerManager({ stageElement: stage });
        mgr.paintFlushTimer = 999;
        mgr.destroy();
        assert.strictEqual(cancelledTimer, 999);
        assert.strictEqual(mgr.paintFlushTimer, null);
      } finally {
        global.cancelAnimationFrame = origCancelRAF;
      }
    });

    await t2.test('gracefully handles SSR or headless environment without document or document.body', () => {
      const origDoc = global.document;
      try {
        global.document = undefined;
        const mgr = new VideoPlayerManager({});
        assert.doesNotThrow(() => {
          mgr.ensureCompositorHeartbeat();
          mgr.flushCompositorPaint();
          mgr.destroy();
        });
      } finally {
        global.document = origDoc;
      }
    });
  });

  await t.test('Browser-Level Touch Forwarding & Aspect Ratio Calibration', async (t2) => {
    await t2.test('calculates normalized coordinates correctly with standard 16:9 display', () => {
      const mgr = new VideoPlayerManager({});
      const mockVideo = {
        videoWidth: 1920,
        videoHeight: 1080,
        getBoundingClientRect: () => ({ left: 0, top: 0, width: 1920, height: 1080 }),
      };

      const coords = mgr.getNormalizedVideoCoordinates({ clientX: 960, clientY: 540 }, mockVideo);
      assert.strictEqual(coords.x, 0.5);
      assert.strictEqual(coords.y, 0.5);
    });

    await t2.test('compensates for pillarboxing when 16:9 video is rendered inside ultrawide container', () => {
      const mgr = new VideoPlayerManager({});
      // 16:9 video in 2560x1080 container: content width is 1920, offset left is 320
      const mockVideo = {
        videoWidth: 1920,
        videoHeight: 1080,
        getBoundingClientRect: () => ({ left: 0, top: 0, width: 2560, height: 1080 }),
      };

      // Center of container (1280, 540) is also center of video content
      const centerCoords = mgr.getNormalizedVideoCoordinates({ clientX: 1280, clientY: 540 }, mockVideo);
      assert.strictEqual(centerCoords.x, 0.5);
      assert.strictEqual(centerCoords.y, 0.5);

      // Left edge of video content (clientX = 320)
      const leftCoords = mgr.getNormalizedVideoCoordinates({ clientX: 320, clientY: 540 }, mockVideo);
      assert.strictEqual(leftCoords.x, 0);

      // Right edge of video content (clientX = 2240)
      const rightCoords = mgr.getNormalizedVideoCoordinates({ clientX: 2240, clientY: 540 }, mockVideo);
      assert.strictEqual(rightCoords.x, 1);
    });

    await t2.test('compensates for letterboxing when 16:9 video is rendered inside 4:3 container', () => {
      const mgr = new VideoPlayerManager({});
      // 16:9 video in 1920x1440 container: content height is 1080, offset top is 180
      const mockVideo = {
        videoWidth: 1920,
        videoHeight: 1080,
        getBoundingClientRect: () => ({ left: 0, top: 0, width: 1920, height: 1440 }),
      };

      const centerCoords = mgr.getNormalizedVideoCoordinates({ clientX: 960, clientY: 720 }, mockVideo);
      assert.strictEqual(centerCoords.x, 0.5);
      assert.strictEqual(centerCoords.y, 0.5);

      // Top edge of video content (clientY = 180)
      const topCoords = mgr.getNormalizedVideoCoordinates({ clientX: 960, clientY: 180 }, mockVideo);
      assert.strictEqual(topCoords.y, 0);
    });

    await t2.test('dispatches tap touch action on pointerdown and pointerup without move', async () => {
      const dispatched = [];
      const mockFetch = async (url, options) => {
        dispatched.push({ url, body: JSON.parse(options.body) });
        return { ok: true, status: 200, json: async () => ({ status: 'ok' }) };
      };

      const primarySlot = createMockElement('div', 'video-primary-slot');
      const pipSlot = createMockElement('div', 'video-pip-slot');
      const stage = createMockElement('div', 'video-stage');

      const mgr = new VideoPlayerManager({
        stageElement: stage,
        primarySlot,
        pipSlot,
        fetch: mockFetch,
        remoteUrl: 'http://localhost:8092',
      });

      mgr.enterVideoMode({
        primary: { id: 'chromecast', stream_url: 'webrtc://cast', type: 'webrtc' },
      });

      const videoEl = mgr.primarySession.element;
      videoEl.getBoundingClientRect = () => ({ left: 0, top: 0, width: 1920, height: 1080 });
      videoEl.videoWidth = 1920;
      videoEl.videoHeight = 1080;

      let stoppedPropagation = false;
      let preventedDefault = false;

      // Simulate pointerdown
      videoEl.dispatchEvent('pointerdown', {
        clientX: 960,
        clientY: 540,
        button: 0,
        stopPropagation: () => { stoppedPropagation = true; },
        preventDefault: () => { preventedDefault = true; },
      });
      assert.strictEqual(stoppedPropagation, true);
      assert.strictEqual(preventedDefault, true);

      // Simulate pointerup
      videoEl.dispatchEvent('pointerup', {
        clientX: 960,
        clientY: 540,
        stopPropagation: () => {},
        preventDefault: () => {},
      });

      assert.strictEqual(dispatched.length, 2);
      assert.strictEqual(dispatched[0].body.action, 'down');
      assert.strictEqual(dispatched[1].body.action, 'tap');
      assert.strictEqual(dispatched[1].body.x, 0.5);
      assert.strictEqual(dispatched[1].body.y, 0.5);

      mgr.destroy();
    });

    await t2.test('dispatches move and up touch actions on drag gesture', async () => {
      const dispatched = [];
      const mockFetch = async (url, options) => {
        dispatched.push({ url, body: JSON.parse(options.body) });
        return { ok: true, status: 200, json: async () => ({ status: 'ok' }) };
      };

      const primarySlot = createMockElement('div', 'video-primary-slot');
      const mgr = new VideoPlayerManager({
        primarySlot,
        fetch: mockFetch,
        remoteUrl: 'http://localhost:8092',
      });

      mgr.enterVideoMode({
        primary: { id: 'chromecast', stream_url: 'webrtc://cast', type: 'webrtc' },
      });

      const videoEl = mgr.primarySession.element;
      videoEl.getBoundingClientRect = () => ({ left: 0, top: 0, width: 1000, height: 1000 });
      videoEl.videoWidth = 1000;
      videoEl.videoHeight = 1000;

      // Pointerdown at (100, 100)
      videoEl.dispatchEvent('pointerdown', { clientX: 100, clientY: 100, button: 0 });

      // Wait 35ms to clear move throttle
      await new Promise((r) => setTimeout(r, 35));

      // Pointermove to (300, 300) (>10px drag)
      videoEl.dispatchEvent('pointermove', { clientX: 300, clientY: 300 });

      // Pointerup at (300, 300)
      videoEl.dispatchEvent('pointerup', { clientX: 300, clientY: 300 });

      assert.strictEqual(dispatched.length, 3);
      assert.strictEqual(dispatched[0].body.action, 'down');
      assert.strictEqual(dispatched[1].body.action, 'move');
      assert.strictEqual(dispatched[2].body.action, 'up');

      mgr.destroy();
    });

    await t2.test('releases sticky touch on pointercancel or pointerleave', async () => {
      const dispatched = [];
      const mockFetch = async (url, options) => {
        dispatched.push({ url, body: JSON.parse(options.body) });
        return { ok: true, status: 200, json: async () => ({ status: 'ok' }) };
      };

      const primarySlot = createMockElement('div', 'video-primary-slot');
      const mgr = new VideoPlayerManager({
        primarySlot,
        fetch: mockFetch,
        remoteUrl: 'http://localhost:8092',
      });

      mgr.enterVideoMode({
        primary: { id: 'chromecast', stream_url: 'webrtc://cast', type: 'webrtc' },
      });

      const videoEl = mgr.primarySession.element;
      videoEl.getBoundingClientRect = () => ({ left: 0, top: 0, width: 1000, height: 1000 });

      // Pointerdown
      videoEl.dispatchEvent('pointerdown', { clientX: 500, clientY: 500, button: 0 });

      // Pointerleave (finger slipped off glass)
      videoEl.dispatchEvent('pointerleave', {});

      assert.strictEqual(dispatched.length, 2);
      assert.strictEqual(dispatched[0].body.action, 'down');
      assert.strictEqual(dispatched[1].body.action, 'up');

      mgr.destroy();
    });

    await t2.test('cleans up touch listeners on session teardown', () => {
      const primarySlot = createMockElement('div', 'video-primary-slot');
      const mgr = new VideoPlayerManager({ primarySlot });

      mgr.enterVideoMode({
        primary: { id: 'chromecast', stream_url: 'webrtc://cast', type: 'webrtc' },
      });

      const session = mgr.primarySession;
      assert.strictEqual(session.touchCleanups.length, 6);

      mgr.exitVideoMode();
      assert.strictEqual(session.touchCleanups.length, 0);
    });
  });
});

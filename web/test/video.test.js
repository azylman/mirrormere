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
    this.iceGatheringState = 'complete';
    this.localDescription = null;
    this.remoteDescription = null;
    this.closed = false;
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

test('Video Presentation Mode & Multi-Stream Player with PiP (SPEC-004 §1, §5, SPEC-010 §1)', async (t) => {
  // Setup mock DOM environment
  global.document = {
    createElement(tag) { return createMockElement(tag); },
    getElementById(id) {
      if (id === 'grid-canvas') return gridCanvas;
      return null;
    },
  };

  const gridCanvas = createMockElement('main', 'grid-canvas');

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

      assert.strictEqual(primarySlot.children.length, 1);
      const img = primarySlot.children[0];
      assert.strictEqual(img.tagName, 'IMG');
      assert.strictEqual(img.src, 'http://cam.lan/mjpeg');
      assert.strictEqual(img.className, 'video-stream-media');
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

      assert.strictEqual(primarySlot.children.length, 1);
      const video = primarySlot.children[0];
      assert.strictEqual(video.tagName, 'VIDEO');
      assert.strictEqual(video.src, 'http://video.lan/stream.m3u8');
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

      assert.strictEqual(primarySlot.children.length, 1);
      const video = primarySlot.children[0];
      assert.strictEqual(video.tagName, 'VIDEO');
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
});

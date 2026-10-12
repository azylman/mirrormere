const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { VideoHUDController } = require('../static/js/video_hud.js');

/**
 * Lightweight mock DOM elements for hermetic Node.js testing.
 */
function createMockElement(tagName = 'div', id = '', className = '') {
  const listeners = new Map();
  const children = [];
  const attributes = new Map();

  const element = {
    tagName: tagName.toUpperCase(),
    id,
    className,
    style: {},
    dataset: {},
    children,
    parentNode: null,
    textContent: '',
    value: '75',
    title: '',

    _innerHTML: '',
    get innerHTML() {
      return this._innerHTML || '';
    },
    set innerHTML(val) {
      this._innerHTML = String(val);
      children.length = 0;
    },

    classList: {
      _classes: new Set(className ? className.split(/\s+/).filter(Boolean) : []),
      add(...cls) { cls.forEach((c) => this._classes.add(c)); },
      remove(...cls) { cls.forEach((c) => this._classes.delete(c)); },
      contains(c) { return this._classes.has(c); },
      toggle(c) {
        if (this.contains(c)) {
          this.remove(c);
          return false;
        }
        this.add(c);
        return true;
      },
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
    },

    setAttribute(attr, val) {
      attributes.set(attr, String(val));
    },

    getAttribute(attr) {
      return attributes.get(attr) || null;
    },

    hasAttribute(attr) {
      return attributes.has(attr);
    },

    closest(selector) {
      let cur = element;
      while (cur) {
        if (selector.startsWith('.') && cur.classList.contains(selector.slice(1))) {
          return cur;
        }
        if (selector.startsWith('#') && cur.id === selector.slice(1)) {
          return cur;
        }
        cur = cur.parentNode;
      }
      return null;
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
      let stopped = false;
      const ev = {
        type: event,
        target: element,
        stopPropagation: () => { stopped = true; },
        ...payload,
      };
      const handlers = listeners.get(event) || [];
      for (const h of handlers) {
        h(ev);
        if (stopped) break;
      }
    },

    click() {
      element.dispatchEvent('click');
    },

    getBoundingClientRect() {
      return { left: 0, top: 0, width: 200, height: 10, right: 200, bottom: 10 };
    },

    setPointerCapture(id) {
      this._capturedPointerId = id;
    },

    releasePointerCapture(id) {
      if (this._capturedPointerId === id) this._capturedPointerId = null;
    },
  };

  return element;
}

/**
 * Helper to build standard HUD DOM tree for tests.
 */
function createHUDDOM() {
  const stage = createMockElement('div', 'video-stage');
  const overlay = createMockElement('div', 'video-hud-overlay');
  const header = createMockElement('div', '', 'video-hud-header');
  const navBar = createMockElement('div', 'video-hud-nav-bar', 'video-hud-capsule-top-left video-hud-nav-bar');
  navBar.style.display = 'none';
  const backBtn = createMockElement('button', 'video-hud-back-btn', 'video-hud-btn video-hud-nav-btn');
  const homeBtn = createMockElement('button', 'video-hud-home-btn', 'video-hud-btn video-hud-nav-btn');
  navBar.appendChild(backBtn);
  navBar.appendChild(homeBtn);

  const title = createMockElement('span', 'video-hud-title');
  const dismissBtn = createMockElement('button', 'video-hud-dismiss-btn', 'video-hud-btn video-hud-dismiss');

  header.appendChild(navBar);
  header.appendChild(title);
  header.appendChild(dismissBtn);

  const controls = createMockElement('div', '', 'video-hud-controls');
  const playBtn = createMockElement('button', 'video-hud-play-btn', 'video-hud-btn video-hud-play');
  const playIcon = createMockElement('span', 'video-hud-play-icon');
  playBtn.appendChild(playIcon);

  const muteBtn = createMockElement('button', 'video-hud-mute-btn', 'video-hud-btn video-hud-mute');
  const muteIcon = createMockElement('span', 'video-hud-mute-icon');
  muteBtn.appendChild(muteIcon);

  const volumeSlider = createMockElement('input', 'video-hud-volume-slider', 'video-hud-volume-slider');
  volumeSlider.value = '75';

  controls.appendChild(playBtn);
  controls.appendChild(muteBtn);
  controls.appendChild(volumeSlider);

  const bottomBar = createMockElement('div', 'video-hud-bottom-bar', 'video-hud-bottom-bar');
  bottomBar.style.display = 'none';

  const progressContainer = createMockElement('div', 'video-hud-progress-container', 'video-hud-progress-container');
  const timeCurrent = createMockElement('span', 'video-hud-time-current', 'video-hud-time-current');
  const progressTrack = createMockElement('div', 'video-hud-progress-track', 'video-hud-progress-track');
  const progressFill = createMockElement('div', 'video-hud-progress-fill', 'video-hud-progress-fill');
  progressTrack.appendChild(progressFill);
  const timeTotal = createMockElement('span', 'video-hud-time-total', 'video-hud-time-total');
  progressContainer.appendChild(timeCurrent);
  progressContainer.appendChild(progressTrack);
  progressContainer.appendChild(timeTotal);
  bottomBar.appendChild(progressContainer);

  const ccBtn = createMockElement('button', 'video-hud-cc-btn', 'video-hud-btn video-hud-media-btn');
  const prevBtn = createMockElement('button', 'video-hud-prev-btn', 'video-hud-btn video-hud-media-btn');
  const rewindBtn = createMockElement('button', 'video-hud-rewind-btn', 'video-hud-btn video-hud-media-btn');
  const forwardBtn = createMockElement('button', 'video-hud-forward-btn', 'video-hud-btn video-hud-media-btn');
  const nextBtn = createMockElement('button', 'video-hud-next-btn', 'video-hud-btn video-hud-media-btn');

  bottomBar.appendChild(ccBtn);
  bottomBar.appendChild(prevBtn);
  bottomBar.appendChild(rewindBtn);
  bottomBar.appendChild(playBtn);
  bottomBar.appendChild(forwardBtn);
  bottomBar.appendChild(nextBtn);

  overlay.appendChild(header);
  overlay.appendChild(controls);
  overlay.appendChild(bottomBar);

  const primarySlot = createMockElement('div', 'video-primary-slot', 'video-primary-slot');
  const pipSlot = createMockElement('div', 'video-pip-slot', 'video-pip-slot');
  stage.appendChild(primarySlot);
  stage.appendChild(pipSlot);
  stage.appendChild(overlay);

  return {
    stage,
    overlay,
    header,
    navBar,
    backBtn,
    homeBtn,
    title,
    dismissBtn,
    controls,
    playBtn,
    playIcon,
    muteBtn,
    muteIcon,
    volumeSlider,
    bottomBar,
    progressContainer,
    timeCurrent,
    progressTrack,
    progressFill,
    timeTotal,
    ccBtn,
    prevBtn,
    rewindBtn,
    forwardBtn,
    nextBtn,
    primarySlot,
    pipSlot,
  };
}

test('VideoHUDController: auto-fade lifecycle and tap-to-wake', async () => {
  const dom = createHUDDOM();
  const hud = new VideoHUDController({
    overlayElement: dom.overlay,
    titleElement: dom.title,
    dismissBtn: dom.dismissBtn,
    playBtn: dom.playBtn,
    playIcon: dom.playIcon,
    muteBtn: dom.muteBtn,
    muteIcon: dom.muteIcon,
    volumeSlider: dom.volumeSlider,
    stageElement: dom.stage,
    autoFadeTimeout: 40, // 40ms fast timer for test
  });

  assert.equal(hud.isVisible, false);
  assert.equal(dom.overlay.classList.contains('visible'), false);

  // Show HUD
  hud.showHUD();
  assert.equal(hud.isVisible, true);
  assert.equal(dom.overlay.classList.contains('visible'), true);

  // Wait for fast auto-fade timeout
  await new Promise((r) => setTimeout(r, 60));
  assert.equal(hud.isVisible, false);
  assert.equal(dom.overlay.classList.contains('visible'), false);

  // Stage tap-to-wake
  dom.stage.dispatchEvent('click', { target: dom.stage });
  assert.equal(hud.isVisible, true);
  assert.equal(dom.overlay.classList.contains('visible'), true);

  // Tapping controls does not hide HUD, it resets auto-fade
  dom.playBtn.dispatchEvent('click', { target: dom.playBtn });
  assert.equal(hud.isVisible, true);

  // Clicking PiP does not toggle HUD
  hud.hideHUD();
  assert.equal(hud.isVisible, false);
  dom.pipSlot.dispatchEvent('click', { target: dom.pipSlot });
  assert.equal(hud.isVisible, false);

  hud.destroy();
});

test('VideoHUDController: stream state sync and controllable gating', () => {
  const dom = createHUDDOM();
  const hud = new VideoHUDController({
    overlayElement: dom.overlay,
    titleElement: dom.title,
    dismissBtn: dom.dismissBtn,
    playBtn: dom.playBtn,
    playIcon: dom.playIcon,
    muteBtn: dom.muteBtn,
    muteIcon: dom.muteIcon,
    volumeSlider: dom.volumeSlider,
    stageElement: dom.stage,
  });

  // 1. Controllable stream in playing state
  hud.handleVideoState({
    mode: 'video',
    primary: {
      id: 'cam_driveway',
      title: 'Driveway Camera',
      controllable: true,
      player_state: 'playing',
    },
  });

  assert.equal(dom.title.textContent, 'Driveway Camera');
  assert.equal(dom.playBtn.style.display, '');
  assert.equal(dom.playBtn.hasAttribute('hidden'), false);
  assert.equal(dom.playIcon.dataset.icon, 'pause');
  assert.ok(dom.playIcon.innerHTML.includes('<svg'));
  assert.equal(dom.playBtn.getAttribute('aria-label'), 'Pause');

  // 2. Stream paused
  hud.handleVideoState({
    mode: 'video',
    primary: {
      id: 'cam_driveway',
      title: 'Driveway Camera',
      controllable: true,
      player_state: 'paused',
    },
  });
  assert.equal(dom.playIcon.dataset.icon, 'play');
  assert.ok(dom.playIcon.innerHTML.includes('<svg'));
  assert.equal(dom.playBtn.getAttribute('aria-label'), 'Play');

  // 3. Uncontrollable stream (e.g. live CCTV)
  hud.handleVideoState({
    mode: 'video',
    primary: {
      id: 'cam_street',
      controllable: false,
      player_state: 'playing',
    },
  });

  assert.equal(dom.title.textContent, 'Stream: cam_street');
  assert.equal(dom.playBtn.style.display, 'none');
  assert.equal(dom.playBtn.getAttribute('hidden'), 'true');

  // 4. Return to widgets mode hides HUD
  hud.showHUD();
  assert.equal(hud.isVisible, true);
  hud.handleVideoState({ mode: 'widgets', primary: null });
  assert.equal(hud.isVisible, false);

  hud.destroy();
});

test('VideoHUDController: transport action and dismiss dispatching with in-flight lock', async () => {
  const dom = createHUDDOM();
  const networkCalls = [];

  const mockFetch = async (url, opts) => {
    networkCalls.push({ url, body: JSON.parse(opts.body) });
    return { ok: true, status: 200, json: async () => ({ status: 'ok' }) };
  };

  const hud = new VideoHUDController({
    overlayElement: dom.overlay,
    titleElement: dom.title,
    dismissBtn: dom.dismissBtn,
    playBtn: dom.playBtn,
    playIcon: dom.playIcon,
    muteBtn: dom.muteBtn,
    muteIcon: dom.muteIcon,
    volumeSlider: dom.volumeSlider,
    stageElement: dom.stage,
    fetch: mockFetch,
  });

  hud.handleVideoState({
    mode: 'video',
    primary: {
      id: 'stream_porch',
      controllable: true,
      player_state: 'playing',
    },
  });

  // Play/pause toggle
  dom.playBtn.click();
  assert.equal(networkCalls.length, 1);
  assert.equal(networkCalls[0].url, 'api/video/action');
  assert.deepEqual(networkCalls[0].body, { id: 'stream_porch', action: 'toggle_playback' });

  // Rapid second click blocked by in-flight lock
  dom.playBtn.click();
  assert.equal(networkCalls.length, 1);

  // Wait out lock (300ms)
  await new Promise((r) => setTimeout(r, 320));

  // Dismiss button click
  dom.dismissBtn.click();
  assert.equal(networkCalls.length, 2);
  assert.equal(networkCalls[1].url, 'api/video/dismiss');
  assert.deepEqual(networkCalls[1].body, { id: 'stream_porch' });

  // Rapid second dismiss blocked by in-flight lock
  dom.dismissBtn.click();
  assert.equal(networkCalls.length, 2);

  hud.destroy();
});

test('VideoHUDController: audio state updates and mute toggle', async () => {
  const dom = createHUDDOM();
  const networkCalls = [];

  const mockFetch = async (url, opts) => {
    networkCalls.push({ url, body: JSON.parse(opts.body) });
    return { ok: true, status: 200, json: async () => ({ status: 'ok' }) };
  };

  const hud = new VideoHUDController({
    overlayElement: dom.overlay,
    titleElement: dom.title,
    dismissBtn: dom.dismissBtn,
    playBtn: dom.playBtn,
    playIcon: dom.playIcon,
    muteBtn: dom.muteBtn,
    muteIcon: dom.muteIcon,
    volumeSlider: dom.volumeSlider,
    stageElement: dom.stage,
    fetch: mockFetch,
  });

  // Unmuted state update
  hud.handleAudioState({ volume: 65, muted: false });
  assert.equal(dom.muteIcon.dataset.icon, 'unmuted');
  assert.ok(dom.muteIcon.innerHTML.includes('<svg'));
  assert.equal(dom.muteBtn.getAttribute('aria-label'), 'Mute');
  assert.equal(dom.volumeSlider.value, '65');
  assert.equal(dom.muteBtn.classList.contains('muted'), false);
  assert.equal(dom.volumeSlider.classList.contains('muted'), false);

  // Click mute
  dom.muteBtn.click();
  assert.equal(networkCalls.length, 1);
  assert.equal(networkCalls[0].url, 'api/audio/mute');
  assert.deepEqual(networkCalls[0].body, { muted: true });

  // Incoming muted state update
  hud.handleAudioState({ volume: 65, muted: true });
  assert.equal(dom.muteIcon.dataset.icon, 'muted');
  assert.ok(dom.muteIcon.innerHTML.includes('<svg'));
  assert.equal(dom.muteBtn.getAttribute('aria-label'), 'Unmute');
  assert.equal(dom.muteBtn.classList.contains('muted'), true);
  assert.equal(dom.volumeSlider.classList.contains('muted'), true);

  // Wait out lock (300ms)
  await new Promise((r) => setTimeout(r, 320));

  // Click unmute
  dom.muteBtn.click();
  assert.equal(networkCalls.length, 2);
  assert.equal(networkCalls[1].url, 'api/audio/mute');
  assert.deepEqual(networkCalls[1].body, { muted: false });

  hud.destroy();
});

test('VideoHUDController: continuous slider drag 200ms throttle and release commit', async () => {
  const dom = createHUDDOM();
  const networkCalls = [];

  const mockFetch = async (url, opts) => {
    networkCalls.push({ url, body: JSON.parse(opts.body) });
    return { ok: true, status: 200, json: async () => ({ status: 'ok' }) };
  };

  const hud = new VideoHUDController({
    overlayElement: dom.overlay,
    titleElement: dom.title,
    dismissBtn: dom.dismissBtn,
    playBtn: dom.playBtn,
    playIcon: dom.playIcon,
    muteBtn: dom.muteBtn,
    muteIcon: dom.muteIcon,
    volumeSlider: dom.volumeSlider,
    stageElement: dom.stage,
    fetch: mockFetch,
    throttleInterval: 50, // Fast 50ms interval for unit test
    commitCooldown: 80,
  });

  hud.handleAudioState({ volume: 40, muted: false });
  assert.equal(dom.volumeSlider.value, '40');

  // Start drag: first input immediately dispatches
  dom.volumeSlider.value = '50';
  dom.volumeSlider.dispatchEvent('input');
  assert.equal(hud.isDragging, true);
  assert.equal(networkCalls.length, 1);
  assert.equal(networkCalls[0].url, 'api/audio/volume');
  assert.deepEqual(networkCalls[0].body, { volume: 50 });

  // Rapid intermediate inputs within throttle window do not send immediate requests
  dom.volumeSlider.value = '55';
  dom.volumeSlider.dispatchEvent('input');
  dom.volumeSlider.value = '60';
  dom.volumeSlider.dispatchEvent('input');
  assert.equal(networkCalls.length, 1);

  // Intermediate audio.state arriving during drag is ignored (active drag isolation)
  hud.handleAudioState({ volume: 20, muted: false });
  assert.equal(dom.volumeSlider.value, '60');

  // Wait for throttle timer to fire
  await new Promise((r) => setTimeout(r, 65));
  assert.equal(networkCalls.length, 2);
  assert.deepEqual(networkCalls[1].body, { volume: 60 });

  // Move further and release commit
  dom.volumeSlider.value = '75';
  dom.volumeSlider.dispatchEvent('input'); // Sends 75 immediately because timer was null
  assert.equal(networkCalls.length, 3);
  assert.deepEqual(networkCalls[2].body, { volume: 75 });

  dom.volumeSlider.value = '82';
  dom.volumeSlider.dispatchEvent('pointerup');
  assert.equal(hud.isDragging, false);
  assert.equal(networkCalls.length, 4);
  assert.deepEqual(networkCalls[3].body, { volume: 82 });

  // Intermediate audio.state arriving during commit cooldown window is ignored
  hud.handleAudioState({ volume: 30, muted: false });
  assert.equal(dom.volumeSlider.value, '82');

  // Wait for cooldown window to expire
  await new Promise((r) => setTimeout(r, 100));

  // Now incoming audio.state updates slider value normally
  hud.handleAudioState({ volume: 90, muted: false });
  assert.equal(dom.volumeSlider.value, '90');

  hud.destroy();
});

test('VideoHUDController: swap-on-tap presentation stream sync', () => {
  const dom = createHUDDOM();

  const mockVideoManager = {
    isSwapped: false,
    serverPrimary: { id: 'primary_stream', title: 'Main Feed', controllable: true, player_state: 'playing' },
    serverPip: { id: 'pip_stream', title: 'Doorbell PiP', controllable: false, player_state: 'playing' },
  };

  const hud = new VideoHUDController({
    overlayElement: dom.overlay,
    titleElement: dom.title,
    dismissBtn: dom.dismissBtn,
    playBtn: dom.playBtn,
    playIcon: dom.playIcon,
    muteBtn: dom.muteBtn,
    muteIcon: dom.muteIcon,
    volumeSlider: dom.volumeSlider,
    stageElement: dom.stage,
    videoManager: mockVideoManager,
  });

  hud.syncStream();
  assert.equal(dom.title.textContent, 'Main Feed');
  assert.equal(dom.playBtn.style.display, '');

  // Simulate swapStreams()
  mockVideoManager.isSwapped = true;
  hud.syncStream();

  assert.equal(dom.title.textContent, 'Doorbell PiP');
  assert.equal(dom.playBtn.style.display, 'none'); // Doorbell is not controllable

  hud.destroy();
});

test('VideoHUDController: tap-to-wake in swapped stream state', () => {
  const dom = createHUDDOM();

  const mockVideoManager = {
    isSwapped: true,
    serverPrimary: { id: 'primary_stream', title: 'Main Feed', controllable: true, player_state: 'playing' },
    serverPip: { id: 'pip_stream', title: 'Doorbell PiP', controllable: false, player_state: 'playing' },
  };

  const hud = new VideoHUDController({
    overlayElement: dom.overlay,
    titleElement: dom.title,
    dismissBtn: dom.dismissBtn,
    playBtn: dom.playBtn,
    playIcon: dom.playIcon,
    muteBtn: dom.muteBtn,
    muteIcon: dom.muteIcon,
    volumeSlider: dom.volumeSlider,
    stageElement: dom.stage,
    videoManager: mockVideoManager,
  });

  assert.equal(hud.isVisible, false);

  // In swapped state, pipSlot is the fullscreen background. Tapping it bubbles to stage and wakes HUD!
  dom.stage.dispatchEvent('click', { target: dom.pipSlot });
  assert.equal(hud.isVisible, true);

  // Clicking the corner dock (which is primarySlot in swapped state) does NOT toggle HUD
  hud.hideHUD();
  assert.equal(hud.isVisible, false);
  dom.stage.dispatchEvent('click', { target: dom.primarySlot });
  assert.equal(hud.isVisible, false);

  hud.destroy();
});

test('VideoHUDController: dismiss button sends id: "all" when both primary and PiP are active', async () => {
  const dom = createHUDDOM();
  const networkCalls = [];

  const mockFetch = async (url, opts) => {
    networkCalls.push({ url, body: JSON.parse(opts.body) });
    return { ok: true, status: 200, json: async () => ({ status: 'ok' }) };
  };

  const hud = new VideoHUDController({
    overlayElement: dom.overlay,
    titleElement: dom.title,
    dismissBtn: dom.dismissBtn,
    playBtn: dom.playBtn,
    playIcon: dom.playIcon,
    muteBtn: dom.muteBtn,
    muteIcon: dom.muteIcon,
    volumeSlider: dom.volumeSlider,
    stageElement: dom.stage,
    fetch: mockFetch,
  });

  hud.handleVideoState({
    mode: 'video',
    primary: { id: 'chromecast', title: 'Chromecast', controllable: true, player_state: 'playing' },
    pip: { id: 'doorbell', title: 'Doorbell', controllable: false, player_state: 'playing' },
  });

  dom.dismissBtn.click();
  assert.equal(networkCalls.length, 1);
  assert.equal(networkCalls[0].url, 'api/video/dismiss');
  assert.deepEqual(networkCalls[0].body, { id: 'all' });

  hud.destroy();
});

test('VideoHUDController: dismiss button sends id: "all" in swapped presentation state', async () => {
  const dom = createHUDDOM();
  const networkCalls = [];

  const mockFetch = async (url, opts) => {
    networkCalls.push({ url, body: JSON.parse(opts.body) });
    return { ok: true, status: 200, json: async () => ({ status: 'ok' }) };
  };

  const mockVideoManager = {
    isSwapped: true,
    serverPrimary: { id: 'chromecast', title: 'Chromecast', controllable: true, player_state: 'playing' },
    serverPip: { id: 'doorbell', title: 'Doorbell', controllable: false, player_state: 'playing' },
  };

  const hud = new VideoHUDController({
    overlayElement: dom.overlay,
    titleElement: dom.title,
    dismissBtn: dom.dismissBtn,
    playBtn: dom.playBtn,
    playIcon: dom.playIcon,
    muteBtn: dom.muteBtn,
    muteIcon: dom.muteIcon,
    volumeSlider: dom.volumeSlider,
    stageElement: dom.stage,
    videoManager: mockVideoManager,
    fetch: mockFetch,
  });

  dom.dismissBtn.click();
  assert.equal(networkCalls.length, 1);
  assert.equal(networkCalls[0].url, 'api/video/dismiss');
  assert.deepEqual(networkCalls[0].body, { id: 'all' });

  hud.destroy();
});

test('VideoHUDController: initial stream presentation reveals HUD', () => {
  const dom = createHUDDOM();
  const hud = new VideoHUDController({
    overlayElement: dom.overlay,
    titleElement: dom.title,
    dismissBtn: dom.dismissBtn,
    stageElement: dom.stage,
  });

  assert.equal(hud.isVisible, false);

  hud.handleVideoState({
    mode: 'video',
    primary: { id: 'stream_porch', title: 'Porch Camera', controllable: true, player_state: 'playing' },
  });

  assert.equal(hud.isVisible, true);
  assert.equal(dom.overlay.classList.contains('visible'), true);

  hud.destroy();
});

test('VideoHUDController: default fetch binds window context without Illegal invocation', async () => {
  const dom = createHUDDOM();
  const networkCalls = [];

  const fakeWindow = {
    fetch: function(url, opts) {
      if (this !== fakeWindow) {
        throw new TypeError("Failed to execute 'fetch' on 'Window': Illegal invocation");
      }
      networkCalls.push({ url, body: JSON.parse(opts.body) });
      return Promise.resolve({ ok: true, status: 200, json: async () => ({ status: 'ok' }) });
    },
  };

  const originalWindow = global.window;
  try {
    global.window = fakeWindow;

    const hud = new VideoHUDController({
      overlayElement: dom.overlay,
      titleElement: dom.title,
      dismissBtn: dom.dismissBtn,
      stageElement: dom.stage,
      // fetch option intentionally omitted to test default fetch binding
    });

    hud.handleVideoState({
      mode: 'video',
      primary: { id: 'stream_porch', title: 'Porch Camera' },
    });

    dom.dismissBtn.click();

    // Verify network call completed without throwing Illegal invocation
    assert.equal(networkCalls.length, 1);
    assert.equal(networkCalls[0].url, 'api/video/dismiss');
    assert.deepEqual(networkCalls[0].body, { id: 'stream_porch' });

    hud.destroy();
  } finally {
    global.window = originalWindow;
  }
});

test('VideoHUDController: touch/pointer tap-to-wake and gesture debouncing on video stage', async () => {
  const dom = createHUDDOM();
  const hud = new VideoHUDController({
    overlayElement: dom.overlay,
    titleElement: dom.title,
    dismissBtn: dom.dismissBtn,
    stageElement: dom.stage,
  });

  assert.equal(hud.isVisible, false);

  // 1. pointerup wakes the HUD
  dom.stage.dispatchEvent('pointerup', { target: dom.stage });
  assert.equal(hud.isVisible, true);
  assert.equal(dom.overlay.classList.contains('visible'), true);

  // 2. Synthetic click immediately following pointerup within debounce window is ignored (stays visible)
  dom.stage.dispatchEvent('click', { target: dom.stage });
  assert.equal(hud.isVisible, true);
  assert.equal(dom.overlay.classList.contains('visible'), true);

  // 3. Duplicate event arriving immediately within debounce window is also ignored
  dom.stage.dispatchEvent('pointerup', { target: dom.stage });
  assert.equal(hud.isVisible, true);

  // 4. After debounce cooldown (>350ms), pointerup toggles HUD to hidden
  await new Promise((r) => setTimeout(r, 360));
  dom.stage.dispatchEvent('pointerup', { target: dom.stage });
  assert.equal(hud.isVisible, false);
  assert.equal(dom.overlay.classList.contains('visible'), false);

  // 5. After debounce cooldown, click toggles HUD back to visible
  await new Promise((r) => setTimeout(r, 360));
  dom.stage.dispatchEvent('click', { target: dom.stage });
  assert.equal(hud.isVisible, true);

  // 6. Tapping interactive header/controls on touch does not hide HUD
  dom.header.dispatchEvent('pointerup', { target: dom.header });
  assert.equal(hud.isVisible, true);

  // 7. Corner PiP dock does not toggle HUD on touch/pointerup
  hud.hideHUD();
  assert.equal(hud.isVisible, false);
  await new Promise((r) => setTimeout(r, 360));
  dom.stage.dispatchEvent('pointerup', { target: dom.pipSlot });
  assert.equal(hud.isVisible, false);

  // 8. Drag/swipe gestures (movement >= 15px) do NOT toggle HUD, and suppress immediate ghost click
  await new Promise((r) => setTimeout(r, 360));
  dom.stage.dispatchEvent('pointerdown', { clientX: 50, clientY: 50 });
  dom.stage.dispatchEvent('pointerup', { clientX: 100, clientY: 100, target: dom.stage });
  assert.equal(hud.isVisible, false); // Stays hidden
  // Immediate synthetic click from drag release is also debounced/eaten
  dom.stage.dispatchEvent('click', { target: dom.stage });
  assert.equal(hud.isVisible, false); // Stays hidden

  hud.destroy();
});

/**
 * Helper to build modern HUD DOM tree with right-aligned vertical side rail.
 */
function createSideRailHUDDOM() {
  const stage = createMockElement('div', 'video-stage');
  const overlay = createMockElement('div', 'video-hud-overlay');
  const header = createMockElement('div', '', 'video-hud-header');
  const title = createMockElement('span', 'video-hud-title');
  header.appendChild(title);

  const sideRail = createMockElement('div', 'video-hud-side-rail', 'video-hud-side-rail');
  const dismissBtn = createMockElement('button', 'video-hud-dismiss-btn', 'video-hud-btn video-hud-dismiss');
  const muteBtn = createMockElement('button', 'video-hud-mute-btn', 'video-hud-btn video-hud-mute');
  const muteIcon = createMockElement('span', 'video-hud-mute-icon');
  muteBtn.appendChild(muteIcon);

  const volumeSlider = createMockElement('input', 'video-hud-volume-slider', 'video-hud-volume-slider');
  volumeSlider.value = '75';

  sideRail.appendChild(dismissBtn);
  sideRail.appendChild(muteBtn);
  sideRail.appendChild(volumeSlider);

  overlay.appendChild(header);
  overlay.appendChild(sideRail);

  const primarySlot = createMockElement('div', 'video-primary-slot', 'video-primary-slot');
  const pipSlot = createMockElement('div', 'video-pip-slot', 'video-pip-slot');
  stage.appendChild(primarySlot);
  stage.appendChild(pipSlot);
  stage.appendChild(overlay);

  return {
    stage,
    overlay,
    header,
    title,
    sideRail,
    dismissBtn,
    muteBtn,
    muteIcon,
    volumeSlider,
    primarySlot,
    pipSlot,
  };
}

test('VideoHUDController: vertical side rail DOM without play button works cleanly', async () => {
  const dom = createSideRailHUDDOM();
  const hud = new VideoHUDController({
    overlayElement: dom.overlay,
    titleElement: dom.title,
    dismissBtn: dom.dismissBtn,
    muteBtn: dom.muteBtn,
    muteIcon: dom.muteIcon,
    volumeSlider: dom.volumeSlider,
    stageElement: dom.stage,
    sideRailElement: dom.sideRail,
    autoFadeTimeout: 40,
  });

  assert.equal(hud.playBtn, null);
  assert.equal(hud.isVisible, false);

  // Sync stream without error when controllable is true
  hud.handleVideoState({
    mode: 'video',
    primary: { id: 'cast', controllable: true, player_state: 'playing' },
  });
  assert.equal(hud.isVisible, true);

  // Tapping side rail does not toggle or dismiss HUD
  dom.sideRail.dispatchEvent('pointerup', { target: dom.sideRail });
  assert.equal(hud.isVisible, true);

  // Mute toggle works
  let mutePosted = null;
  hud.fetchFn = async (url, opts) => {
    if (url === 'api/audio/mute') {
      mutePosted = JSON.parse(opts.body);
      return { ok: true };
    }
  };
  dom.muteBtn.click();
  assert.deepEqual(mutePosted, { muted: true });

  // Dismiss button works
  let dismissPosted = null;
  hud.fetchFn = async (url, opts) => {
    if (url === 'api/video/dismiss') {
      dismissPosted = JSON.parse(opts.body);
      return { ok: true };
    }
  };
  dom.dismissBtn.click();
  assert.deepEqual(dismissPosted, { id: 'cast' });

  hud.destroy();
});

test('VideoHUDController: side rail stops pointer event propagation to video stage', async () => {
  const dom = createSideRailHUDDOM();
  const hud = new VideoHUDController({
    overlayElement: dom.overlay,
    titleElement: dom.title,
    dismissBtn: dom.dismissBtn,
    muteBtn: dom.muteBtn,
    muteIcon: dom.muteIcon,
    volumeSlider: dom.volumeSlider,
    stageElement: dom.stage,
    sideRailElement: dom.sideRail,
  });

  let stoppedEvents = [];
  const fakeEvent = (type) => ({
    type,
    stopPropagation: () => {
      stoppedEvents.push(type);
    },
  });

  dom.sideRail.dispatchEvent('pointerdown', fakeEvent('pointerdown'));
  dom.sideRail.dispatchEvent('pointerup', fakeEvent('pointerup'));
  dom.sideRail.dispatchEvent('touchstart', fakeEvent('touchstart'));
  dom.sideRail.dispatchEvent('touchend', fakeEvent('touchend'));

  assert.deepEqual(stoppedEvents, ['pointerdown', 'pointerup', 'touchstart', 'touchend']);

  hud.destroy();
});

test('VideoHUDController: video-hud-overlay defines transparent background without border shadow gradients in hud.css', () => {
  const hudCSSPath = path.resolve(__dirname, '../static/css/hud.css');
  assert.ok(fs.existsSync(hudCSSPath), 'hud.css must exist');
  const content = fs.readFileSync(hudCSSPath, 'utf8');

  const overlayMatch = content.match(/\.video-hud-overlay\s*\{([^}]+)\}/);
  assert.ok(overlayMatch, '.video-hud-overlay rule must exist');
  assert.doesNotMatch(
    overlayMatch[1],
    /linear-gradient/,
    '.video-hud-overlay must not define linear-gradient border shadow background'
  );
  assert.match(
    overlayMatch[1],
    /background:\s*transparent;/,
    '.video-hud-overlay must define background: transparent;'
  );
});

test('VideoHUDController: bottom bar is displayed for controllable or persistent media streams, and hidden for non-media/doorbell streams', () => {
  const dom = createHUDDOM();
  const hud = new VideoHUDController({
    overlayElement: dom.overlay,
    titleElement: dom.title,
    dismissBtn: dom.dismissBtn,
    bottomBarElement: dom.bottomBar,
    rewindBtn: dom.rewindBtn,
    forwardBtn: dom.forwardBtn,
    playBtn: dom.playBtn,
    playIcon: dom.playIcon,
    muteBtn: dom.muteBtn,
    muteIcon: dom.muteIcon,
    volumeSlider: dom.volumeSlider,
    stageElement: dom.stage,
    remoteUrl: 'http://chromecast.lan:8080',
  });

  // 1. Initial state: bottom bar is hidden
  assert.equal(dom.bottomBar.style.display, 'none');

  // 2. Controllable stream (e.g. Plex / YouTube via sidecar)
  hud.handleVideoState({
    mode: 'video',
    primary: {
      id: 'stream_plex',
      title: 'Plex Media',
      controllable: true,
      player_state: 'playing',
    },
  });
  assert.equal(dom.bottomBar.style.display, '');
  assert.equal(dom.bottomBar.hasAttribute('hidden'), false);
  assert.equal(dom.playBtn.style.display, '');

  // 3. Non-controllable, persistent stream with remoteUrl
  hud.handleVideoState({
    mode: 'video',
    primary: {
      id: 'stream_chromecast',
      title: 'Chromecast Ambient',
      controllable: false,
      priority: 'persistent',
      player_state: 'playing',
    },
  });
  assert.equal(dom.bottomBar.style.display, '');
  assert.equal(dom.bottomBar.hasAttribute('hidden'), false);

  // 4. Non-media temporary stream (e.g. doorbell camera alert)
  hud.handleVideoState({
    mode: 'video',
    primary: {
      id: 'cam_doorbell',
      title: 'Front Doorbell',
      controllable: false,
      priority: 'temporary',
      player_state: 'playing',
    },
  });
  assert.equal(dom.bottomBar.style.display, 'none');
  assert.equal(dom.bottomBar.getAttribute('hidden'), 'true');
  assert.equal(dom.playBtn.style.display, 'none');

  // 5. Stream torn down (mode: widgets)
  hud.handleVideoState({ mode: 'widgets', primary: null });
  assert.equal(dom.bottomBar.style.display, 'none');

  hud.destroy();
});

test('VideoHUDController: rewind and fast forward dispatch actions and remote keys with in-flight lock', async () => {
  const dom = createHUDDOM();
  const networkCalls = [];

  const mockFetch = async (url, opts) => {
    networkCalls.push({ url, body: JSON.parse(opts.body) });
    return { ok: true, status: 200, json: async () => ({ status: 'ok' }) };
  };

  const hud = new VideoHUDController({
    overlayElement: dom.overlay,
    titleElement: dom.title,
    dismissBtn: dom.dismissBtn,
    bottomBarElement: dom.bottomBar,
    rewindBtn: dom.rewindBtn,
    forwardBtn: dom.forwardBtn,
    playBtn: dom.playBtn,
    playIcon: dom.playIcon,
    muteBtn: dom.muteBtn,
    muteIcon: dom.muteIcon,
    volumeSlider: dom.volumeSlider,
    stageElement: dom.stage,
    remoteUrl: 'http://tv.lan:8080',
    fetch: mockFetch,
  });

  // 1. Controllable stream with control_url
  hud.handleVideoState({
    mode: 'video',
    primary: {
      id: 'stream_controllable',
      controllable: true,
      control_url: 'http://control.lan',
      player_state: 'playing',
    },
  });

  // Rewind click
  dom.rewindBtn.click();
  assert.equal(networkCalls.length, 1);
  assert.equal(networkCalls[0].url, 'api/video/action');
  assert.deepEqual(networkCalls[0].body, { id: 'stream_controllable', action: 'rewind' });

  // Rapid second click during lock is dropped
  dom.rewindBtn.click();
  assert.equal(networkCalls.length, 1);

  // Wait out lock (300ms)
  await new Promise((r) => setTimeout(r, 320));

  // Fast forward click
  dom.forwardBtn.click();
  assert.equal(networkCalls.length, 2);
  assert.equal(networkCalls[1].url, 'api/video/action');
  assert.deepEqual(networkCalls[1].body, { id: 'stream_controllable', action: 'fast_forward' });

  // Wait out lock
  await new Promise((r) => setTimeout(r, 320));

  // 2. Persistent stream without control_url -> dispatches to remoteUrl/key
  hud.handleVideoState({
    mode: 'video',
    primary: {
      id: 'stream_persistent',
      controllable: false,
      priority: 'persistent',
      player_state: 'playing',
    },
  });

  dom.rewindBtn.click();
  assert.equal(networkCalls.length, 3);
  assert.equal(networkCalls[2].url, 'http://tv.lan:8080/key');
  assert.deepEqual(networkCalls[2].body, { key: 'rewind' });

  await new Promise((r) => setTimeout(r, 320));

  dom.forwardBtn.click();
  assert.equal(networkCalls.length, 4);
  assert.equal(networkCalls[3].url, 'http://tv.lan:8080/key');
  assert.deepEqual(networkCalls[3].body, { key: 'fast_forward' });

  await new Promise((r) => setTimeout(r, 320));

  dom.playBtn.click();
  assert.equal(networkCalls.length, 5);
  assert.equal(networkCalls[4].url, 'http://tv.lan:8080/key');
  assert.deepEqual(networkCalls[4].body, { key: 'play_pause' });

  hud.destroy();
});

test('VideoHUDController: bottom bar stops pointer event propagation to video stage', () => {
  const dom = createHUDDOM();
  const hud = new VideoHUDController({
    overlayElement: dom.overlay,
    titleElement: dom.title,
    dismissBtn: dom.dismissBtn,
    bottomBarElement: dom.bottomBar,
    rewindBtn: dom.rewindBtn,
    forwardBtn: dom.forwardBtn,
    playBtn: dom.playBtn,
    muteBtn: dom.muteBtn,
    volumeSlider: dom.volumeSlider,
    stageElement: dom.stage,
  });

  let stoppedEvents = [];
  const fakeEvent = (type) => ({
    type,
    stopPropagation: () => {
      stoppedEvents.push(type);
    },
  });

  dom.bottomBar.dispatchEvent('pointerdown', fakeEvent('pointerdown'));
  dom.bottomBar.dispatchEvent('pointerup', fakeEvent('pointerup'));
  dom.bottomBar.dispatchEvent('touchstart', fakeEvent('touchstart'));
  dom.bottomBar.dispatchEvent('touchend', fakeEvent('touchend'));
  dom.bottomBar.dispatchEvent('click', fakeEvent('click'));

  assert.deepEqual(stoppedEvents, ['pointerdown', 'pointerup', 'touchstart', 'touchend', 'click']);

  hud.destroy();
});

test('VideoHUDController: double-tap on video stage toggles play/pause on media streams and does nothing on non-media', async () => {
  const dom = createHUDDOM();
  const networkCalls = [];

  const mockFetch = async (url, opts) => {
    networkCalls.push({ url, body: JSON.parse(opts.body) });
    return { ok: true, status: 200, json: async () => ({ status: 'ok' }) };
  };

  const hud = new VideoHUDController({
    overlayElement: dom.overlay,
    titleElement: dom.title,
    dismissBtn: dom.dismissBtn,
    bottomBarElement: dom.bottomBar,
    rewindBtn: dom.rewindBtn,
    forwardBtn: dom.forwardBtn,
    playBtn: dom.playBtn,
    playIcon: dom.playIcon,
    muteBtn: dom.muteBtn,
    muteIcon: dom.muteIcon,
    volumeSlider: dom.volumeSlider,
    stageElement: dom.stage,
    fetch: mockFetch,
    autoFadeTimeout: 5000,
  });

  // 1. Controllable media stream active
  hud.handleVideoState({
    mode: 'video',
    primary: {
      id: 'stream_media',
      controllable: true,
      control_url: 'http://control.lan',
      player_state: 'playing',
    },
  });

  // Ensure HUD is hidden initially to test reveal + double-tap
  hud.hideHUD();
  assert.equal(hud.isVisible, false);

  // First tap: reveals HUD
  dom.stage.dispatchEvent('pointerdown', { clientX: 100, clientY: 100 });
  dom.stage.dispatchEvent('pointerup', { clientX: 100, clientY: 100, target: dom.stage });
  assert.equal(hud.isVisible, true);
  assert.equal(networkCalls.length, 0);

  // Second tap within 100ms and <30px: triggers double-tap play/pause!
  await new Promise((r) => setTimeout(r, 50));
  dom.stage.dispatchEvent('pointerdown', { clientX: 105, clientY: 102 });
  dom.stage.dispatchEvent('pointerup', { clientX: 105, clientY: 102, target: dom.stage });

  assert.equal(networkCalls.length, 1);
  assert.equal(networkCalls[0].url, 'api/video/action');
  assert.deepEqual(networkCalls[0].body, { id: 'stream_media', action: 'toggle_playback' });
  assert.equal(hud.isVisible, true);

  // 3rd tap within 300ms cooldown: ignored (does not trigger toggle or pairing)
  await new Promise((r) => setTimeout(r, 50));
  dom.stage.dispatchEvent('pointerdown', { clientX: 105, clientY: 102 });
  dom.stage.dispatchEvent('pointerup', { clientX: 105, clientY: 102, target: dom.stage });
  assert.equal(networkCalls.length, 1);

  // Wait out lock and cooldown
  await new Promise((r) => setTimeout(r, 400));

  // 2. Non-media stream (doorbell camera)
  hud.handleVideoState({
    mode: 'video',
    primary: {
      id: 'stream_doorbell',
      controllable: false,
      priority: 'temporary',
      player_state: 'playing',
    },
  });

  // Double-tap on non-media stream does NOT trigger play/pause
  dom.stage.dispatchEvent('pointerdown', { clientX: 100, clientY: 100 });
  dom.stage.dispatchEvent('pointerup', { clientX: 100, clientY: 100, target: dom.stage });
  await new Promise((r) => setTimeout(r, 50));
  dom.stage.dispatchEvent('pointerdown', { clientX: 102, clientY: 101 });
  dom.stage.dispatchEvent('pointerup', { clientX: 102, clientY: 101, target: dom.stage });

  // networkCalls remains 1 (no new action was sent)
  assert.equal(networkCalls.length, 1);

  hud.destroy();
});

test('VideoHUDController: single-tap reveals HUD immediately when hidden and hides with 250ms delay when visible', async () => {
  const dom = createHUDDOM();
  const hud = new VideoHUDController({
    overlayElement: dom.overlay,
    titleElement: dom.title,
    dismissBtn: dom.dismissBtn,
    bottomBarElement: dom.bottomBar,
    rewindBtn: dom.rewindBtn,
    forwardBtn: dom.forwardBtn,
    playBtn: dom.playBtn,
    muteBtn: dom.muteBtn,
    volumeSlider: dom.volumeSlider,
    stageElement: dom.stage,
    autoFadeTimeout: 5000,
  });

  hud.handleVideoState({
    mode: 'video',
    primary: {
      id: 'stream_media',
      controllable: true,
      player_state: 'playing',
    },
  });
  hud.hideHUD();

  assert.equal(hud.isVisible, false);

  // 1. Single-tap when hidden reveals HUD immediately
  dom.stage.dispatchEvent('pointerdown', { clientX: 50, clientY: 50 });
  dom.stage.dispatchEvent('pointerup', { clientX: 50, clientY: 50, target: dom.stage });
  assert.equal(hud.isVisible, true);

  // Synthetic click from browser is suppressed
  dom.stage.dispatchEvent('click', { target: dom.stage });
  assert.equal(hud.isVisible, true);

  // Wait 350ms so cooldown expires and single tap is settled
  await new Promise((r) => setTimeout(r, 360));
  assert.equal(hud.isVisible, true);

  // 2. Single-tap when visible initiates 250ms delayed hide
  dom.stage.dispatchEvent('pointerdown', { clientX: 50, clientY: 50 });
  dom.stage.dispatchEvent('pointerup', { clientX: 50, clientY: 50, target: dom.stage });

  // Still visible immediately after tap
  assert.equal(hud.isVisible, true);

  // After 270ms, hideHUD executes
  await new Promise((r) => setTimeout(r, 270));
  assert.equal(hud.isVisible, false);

  hud.destroy();
});



test('VideoHUDController: back and home buttons dispatch actions and remote keys with in-flight lock', async () => {
  const dom = createHUDDOM();
  const networkCalls = [];

  const mockFetch = async (url, opts) => {
    networkCalls.push({ url, body: JSON.parse(opts.body) });
    return { ok: true, status: 200, json: async () => ({ status: 'ok' }) };
  };

  const hud = new VideoHUDController({
    overlayElement: dom.overlay,
    titleElement: dom.title,
    dismissBtn: dom.dismissBtn,
    navBarElement: dom.navBar,
    backBtn: dom.backBtn,
    homeBtn: dom.homeBtn,
    bottomBarElement: dom.bottomBar,
    fetch: mockFetch,
    remoteUrl: 'http://remote.lan:8080',
  });

  // 1. Controllable stream with control_url dispatches api/video/action
  hud.handleVideoState({
    mode: 'video',
    primary: {
      id: 'stream_tv',
      controllable: true,
      control_url: 'http://control.lan',
      player_state: 'playing',
    },
  });

  dom.backBtn.click();
  assert.equal(networkCalls.length, 1);
  assert.equal(networkCalls[0].url, 'api/video/action');
  assert.deepEqual(networkCalls[0].body, { id: 'stream_tv', action: 'back' });

  // Rapid second click blocked by in-flight lock
  dom.backBtn.click();
  assert.equal(networkCalls.length, 1);

  // Wait out 300ms debounce lock
  await new Promise((r) => setTimeout(r, 320));

  dom.homeBtn.click();
  assert.equal(networkCalls.length, 2);
  assert.equal(networkCalls[1].url, 'api/video/action');
  assert.deepEqual(networkCalls[1].body, { id: 'stream_tv', action: 'home' });

  // 2. Non-controllable persistent stream dispatches remoteUrl/key
  hud.handleVideoState({
    mode: 'video',
    primary: {
      id: 'stream_chromecast',
      controllable: false,
      priority: 'persistent',
      player_state: 'playing',
    },
  });

  await new Promise((r) => setTimeout(r, 320));

  dom.backBtn.click();
  assert.equal(networkCalls.length, 3);
  assert.equal(networkCalls[2].url, 'http://remote.lan:8080/key');
  assert.deepEqual(networkCalls[2].body, { key: 'back' });

  await new Promise((r) => setTimeout(r, 320));

  dom.homeBtn.click();
  assert.equal(networkCalls.length, 4);
  assert.equal(networkCalls[3].url, 'http://remote.lan:8080/key');
  assert.deepEqual(networkCalls[3].body, { key: 'home' });

  hud.destroy();
});

test('VideoHUDController: cc, prev, and next buttons dispatch actions and remote keys with in-flight lock', async () => {
  const dom = createHUDDOM();
  const networkCalls = [];

  const mockFetch = async (url, opts) => {
    networkCalls.push({ url, body: JSON.parse(opts.body) });
    return { ok: true, status: 200, json: async () => ({ status: 'ok' }) };
  };

  const hud = new VideoHUDController({
    overlayElement: dom.overlay,
    titleElement: dom.title,
    dismissBtn: dom.dismissBtn,
    bottomBarElement: dom.bottomBar,
    ccBtn: dom.ccBtn,
    prevBtn: dom.prevBtn,
    nextBtn: dom.nextBtn,
    fetch: mockFetch,
    remoteUrl: 'http://remote.lan:8080',
  });

  // 1. Controllable stream dispatches to api/video/action
  hud.handleVideoState({
    mode: 'video',
    primary: {
      id: 'stream_plex',
      controllable: true,
      control_url: 'http://plex.lan',
      player_state: 'playing',
    },
  });

  dom.ccBtn.click();
  assert.equal(networkCalls.length, 1);
  assert.equal(networkCalls[0].url, 'api/video/action');
  assert.deepEqual(networkCalls[0].body, { id: 'stream_plex', action: 'captions' });

  // Rapid second click blocked by lock
  dom.ccBtn.click();
  assert.equal(networkCalls.length, 1);

  await new Promise((r) => setTimeout(r, 320));

  dom.prevBtn.click();
  assert.equal(networkCalls.length, 2);
  assert.equal(networkCalls[1].url, 'api/video/action');
  assert.deepEqual(networkCalls[1].body, { id: 'stream_plex', action: 'previous' });

  await new Promise((r) => setTimeout(r, 320));

  dom.nextBtn.click();
  assert.equal(networkCalls.length, 3);
  assert.equal(networkCalls[2].url, 'api/video/action');
  assert.deepEqual(networkCalls[2].body, { id: 'stream_plex', action: 'next' });

  // 2. Persistent stream dispatches to remoteUrl/key
  hud.handleVideoState({
    mode: 'video',
    primary: {
      id: 'stream_cast',
      controllable: false,
      priority: 'persistent',
      player_state: 'playing',
    },
  });

  await new Promise((r) => setTimeout(r, 320));

  dom.ccBtn.click();
  assert.equal(networkCalls.length, 4);
  assert.equal(networkCalls[3].url, 'http://remote.lan:8080/key');
  assert.deepEqual(networkCalls[3].body, { key: 'cc' });

  await new Promise((r) => setTimeout(r, 320));

  dom.prevBtn.click();
  assert.equal(networkCalls.length, 5);
  assert.equal(networkCalls[4].url, 'http://remote.lan:8080/key');
  assert.deepEqual(networkCalls[4].body, { key: 'prev' });

  await new Promise((r) => setTimeout(r, 320));

  dom.nextBtn.click();
  assert.equal(networkCalls.length, 6);
  assert.equal(networkCalls[5].url, 'http://remote.lan:8080/key');
  assert.deepEqual(networkCalls[5].body, { key: 'next' });

  hud.destroy();
});

test('VideoHUDController: nav bar stops pointer event propagation to video stage', () => {
  const dom = createHUDDOM();
  const hud = new VideoHUDController({
    overlayElement: dom.overlay,
    titleElement: dom.title,
    dismissBtn: dom.dismissBtn,
    navBarElement: dom.navBar,
    backBtn: dom.backBtn,
    homeBtn: dom.homeBtn,
    stageElement: dom.stage,
  });

  const stoppedEvents = [];
  const fakeEvent = (type) => ({
    type,
    stopPropagation: () => { stoppedEvents.push(type); },
  });

  dom.navBar.dispatchEvent('pointerdown', fakeEvent('pointerdown'));
  dom.navBar.dispatchEvent('pointerup', fakeEvent('pointerup'));
  dom.navBar.dispatchEvent('touchstart', fakeEvent('touchstart'));
  dom.navBar.dispatchEvent('touchend', fakeEvent('touchend'));

  assert.deepEqual(stoppedEvents, ['pointerdown', 'pointerup', 'touchstart', 'touchend']);

  hud.destroy();
});

test('VideoHUDController: in-flight lock is released via finally even when network fetch fails', async () => {
  const dom = createHUDDOM();
  let failFetch = true;

  const mockFetch = async () => {
    if (failFetch) {
      throw new Error('Network connection timeout');
    }
    return { ok: true, status: 200, json: async () => ({ status: 'ok' }) };
  };

  const hud = new VideoHUDController({
    overlayElement: dom.overlay,
    titleElement: dom.title,
    dismissBtn: dom.dismissBtn,
    navBarElement: dom.navBar,
    backBtn: dom.backBtn,
    homeBtn: dom.homeBtn,
    fetch: mockFetch,
    remoteUrl: 'http://remote.lan',
  });

  hud.handleVideoState({
    mode: 'video',
    primary: {
      id: 'stream_test',
      controllable: true,
      player_state: 'playing',
    },
  });

  // Action fails network call
  dom.backBtn.click();
  assert.equal(hud.isActionPending, true);

  // Wait for 300ms timer and finally block to resolve
  await new Promise((r) => setTimeout(r, 320));
  assert.equal(hud.isActionPending, false);

  // Subsequent call succeeds
  failFetch = false;
  dom.homeBtn.click();
  assert.equal(hud.isActionPending, true);

  await new Promise((r) => setTimeout(r, 320));
  assert.equal(hud.isActionPending, false);

  hud.destroy();
});

test('VideoHUDController: nav bar is displayed for media streams and hidden for non-media streams', () => {
  const dom = createHUDDOM();
  const hud = new VideoHUDController({
    overlayElement: dom.overlay,
    titleElement: dom.title,
    dismissBtn: dom.dismissBtn,
    navBarElement: dom.navBar,
    bottomBarElement: dom.bottomBar,
    remoteUrl: 'http://remote.lan',
  });

  // 1. Initial state: navBar is hidden
  assert.equal(dom.navBar.style.display, 'none');

  // 2. Persistent media stream: navBar is shown
  hud.handleVideoState({
    mode: 'video',
    primary: {
      id: 'stream_cast',
      priority: 'persistent',
      controllable: false,
      player_state: 'playing',
    },
  });
  assert.equal(dom.navBar.style.display, '');

  // 3. Non-media temporary stream (doorbell): navBar is hidden
  hud.handleVideoState({
    mode: 'video',
    primary: {
      id: 'cam_doorbell',
      priority: 'temporary',
      controllable: false,
      player_state: 'playing',
    },
  });
  assert.equal(dom.navBar.style.display, 'none');

  // 4. Teardown (null stream): navBar is hidden
  hud.handleVideoState({
    mode: 'widgets',
    primary: null,
  });
  assert.equal(dom.navBar.style.display, 'none');

  hud.destroy();
});

test('VideoHUDController: prev and next buttons render vector SVG icons instead of unicode tofu', () => {
  const dom = createHUDDOM();
  const hud = new VideoHUDController({
    overlayElement: dom.overlay,
    prevBtn: dom.prevBtn,
    nextBtn: dom.nextBtn,
  });

  assert.ok(dom.prevBtn.innerHTML.includes('<svg'), 'prevBtn must render inline SVG');
  assert.ok(dom.prevBtn.innerHTML.includes('polygon'), 'prevBtn SVG must contain polygon');
  assert.ok(dom.nextBtn.innerHTML.includes('<svg'), 'nextBtn must render inline SVG');
  assert.ok(dom.nextBtn.innerHTML.includes('polygon'), 'nextBtn SVG must contain polygon');

  hud.destroy();
});

test('VideoHUD: display.html defines vector SVGs for prev and next buttons', () => {
  const html = fs.readFileSync(path.resolve(__dirname, '../templates/display.html'), 'utf8');

  assert.ok(html.includes('id="video-hud-prev-btn"'), 'display.html must contain video-hud-prev-btn');
  assert.ok(html.includes('id="video-hud-next-btn"'), 'display.html must contain video-hud-next-btn');

  // Verify neither contains raw unicode tofu characters
  assert.ok(!html.includes('⏮'), 'display.html must not use unicode ⏮');
  assert.ok(!html.includes('⏭'), 'display.html must not use unicode ⏭');

  // Verify both contain SVG tags
  const prevBtnHtml = html.match(/<button id="video-hud-prev-btn"[^>]*>([\s\S]*?)<\/button>/);
  assert.ok(prevBtnHtml && prevBtnHtml[1].includes('<svg'), 'video-hud-prev-btn must contain svg in template');

  const nextBtnHtml = html.match(/<button id="video-hud-next-btn"[^>]*>([\s\S]*?)<\/button>/);
  assert.ok(nextBtnHtml && nextBtnHtml[1].includes('<svg'), 'video-hud-next-btn must contain svg in template');
});

test('VideoHUDController: formatStreamTitle formats series, season, episode, title, and fallbacks', () => {
  const dom = createHUDDOM();
  const hud = new VideoHUDController({
    overlayElement: dom.overlay,
    titleElement: dom.title,
    bottomBar: dom.bottomBar,
  });

  // Full metadata with integer season/episode
  assert.equal(
    hud.formatStreamTitle({
      series: 'Trapped in a Dating Sim',
      season: 2,
      episode: 3,
      title: 'Hey, Brat',
    }),
    'Trapped in a Dating Sim S2E3: Hey, Brat'
  );

  // String season/episode with S/E prefixes already
  assert.equal(
    hud.formatStreamTitle({
      series: 'Sousou no Frieren',
      season: 'S01',
      episode: 'E08',
      title: 'Frieren the Slayer',
    }),
    'Sousou no Frieren S01E08: Frieren the Slayer'
  );

  // Series + title without season/episode
  assert.equal(
    hud.formatStreamTitle({
      series: 'Trapped in a Dating Sim',
      title: 'Hey, Brat',
    }),
    'Trapped in a Dating Sim: Hey, Brat'
  );

  // Series + season/episode annotation without episode title
  assert.equal(
    hud.formatStreamTitle({
      series: 'Sousou no Frieren',
      season: 1,
      episode: 5,
    }),
    'Sousou no Frieren S1E5'
  );

  // Standalone episode/video title (e.g. YouTube video or generic file)
  assert.equal(
    hud.formatStreamTitle({
      title: 'Chainsaw Man Episode 12',
    }),
    'Chainsaw Man Episode 12'
  );

  // Standalone series name
  assert.equal(
    hud.formatStreamTitle({
      series: 'Crunchyroll Live',
    }),
    'Crunchyroll Live'
  );

  // Fallback to stream id
  assert.equal(
    hud.formatStreamTitle({
      id: 'chromecast',
    }),
    'Stream: chromecast'
  );

  // Null or missing stream fallback
  assert.equal(hud.formatStreamTitle(null), 'Live Video');
  assert.equal(hud.formatStreamTitle({}), 'Live Video');

  hud.destroy();
});

test('VideoHUDController: formatTime formats seconds to M:SS and H:MM:SS with zero padding', () => {
  const dom = createHUDDOM();
  const hud = new VideoHUDController({
    overlayElement: dom.overlay,
  });

  assert.equal(hud.formatTime(0), '0:00');
  assert.equal(hud.formatTime(5), '0:05');
  assert.equal(hud.formatTime(65), '1:05');
  assert.equal(hud.formatTime(1217.89), '20:17');
  assert.equal(hud.formatTime(3600), '1:00:00');
  assert.equal(hud.formatTime(3665), '1:01:05');
  assert.equal(hud.formatTime(7325), '2:02:05');

  // Edge cases
  assert.equal(hud.formatTime(-10), '0:00');
  assert.equal(hud.formatTime(NaN), '0:00');
  assert.equal(hud.formatTime(Infinity), '0:00');
  assert.equal(hud.formatTime(null), '0:00');

  hud.destroy();
});

test('VideoHUDController: progress bar updates fill percentage, time labels, and hides for live streams', () => {
  const dom = createHUDDOM();
  const hud = new VideoHUDController({
    overlayElement: dom.overlay,
    titleElement: dom.title,
    bottomBar: dom.bottomBar,
    progressContainer: dom.progressContainer,
    timeCurrentElement: dom.timeCurrent,
    timeTotalElement: dom.timeTotal,
    progressFillElement: dom.progressFill,
  });

  // Active stream with duration and position
  hud.handleVideoState({
    mode: 'video',
    primary: {
      id: 'chromecast',
      stream_url: 'http://127.0.0.1:1984/cast',
      priority: 'persistent',
      player_state: 'playing',
      title: 'Hey, Brat',
      series: 'Trapped in a Dating Sim',
      season: 2,
      episode: 3,
      position: 120,
      duration: 1420,
      position_updated_at: Date.now(),
    },
  });

  assert.equal(dom.title.textContent, 'Trapped in a Dating Sim S2E3: Hey, Brat');
  assert.equal(dom.progressContainer.style.display, '');
  assert.equal(dom.progressContainer.hasAttribute('hidden'), false);
  assert.equal(dom.timeCurrent.textContent, '2:00');
  assert.equal(dom.timeTotal.textContent, '23:40');
  // 120 / 1420 * 100 = 8.45% -> 8.5%
  assert.equal(dom.progressFill.style.width, '8.5%');

  // Live doorbell stream with duration 0 -> progress hidden
  hud.handleVideoState({
    mode: 'video',
    primary: {
      id: 'doorbell',
      stream_url: 'http://127.0.0.1:1984/doorbell',
      priority: 'temporary',
      timeout_seconds: 45,
      title: 'Front Door Camera',
      duration: 0,
      position: 0,
    },
  });

  assert.equal(dom.title.textContent, 'Front Door Camera');
  assert.equal(dom.progressContainer.style.display, 'none');
  assert.equal(dom.progressContainer.getAttribute('hidden'), 'true');

  hud.destroy();
});

test('VideoHUDController: progress ticker advances with timestamp drift compensation', async () => {
  const dom = createHUDDOM();
  const hud = new VideoHUDController({
    overlayElement: dom.overlay,
    titleElement: dom.title,
    bottomBar: dom.bottomBar,
    progressContainer: dom.progressContainer,
    timeCurrentElement: dom.timeCurrent,
    timeTotalElement: dom.timeTotal,
    progressFillElement: dom.progressFill,
  });

  const now = Date.now();
  hud.handleVideoState({
    mode: 'video',
    primary: {
      id: 'chromecast',
      stream_url: 'http://127.0.0.1:1984/cast',
      priority: 'persistent',
      player_state: 'playing',
      position: 100,
      duration: 1000,
      position_updated_at: now - 5000, // 5 seconds elapsed since anchor
    },
  });

  // renderProgressTick should drift by ~5s
  hud.renderProgressTick();
  assert.equal(dom.timeCurrent.textContent, '1:45');
  assert.equal(dom.progressFill.style.width, '10.5%');

  // When paused, drift compensation does not advance past base position
  hud.handleVideoState({
    mode: 'video',
    primary: {
      id: 'chromecast',
      stream_url: 'http://127.0.0.1:1984/cast',
      priority: 'persistent',
      player_state: 'paused',
      position: 100,
      duration: 1000,
      position_updated_at: now - 5000,
    },
  });
  hud.renderProgressTick();
  assert.equal(dom.timeCurrent.textContent, '1:40');
  assert.equal(dom.progressFill.style.width, '10.0%');

  hud.destroy();
});

test('VideoHUD: display.html and hud.css define progress elements and night mode styling', () => {
  const html = fs.readFileSync(path.resolve(__dirname, '../templates/display.html'), 'utf8');
  const css = fs.readFileSync(path.resolve(__dirname, '../static/css/hud.css'), 'utf8');

  // Verify display.html structure
  assert.ok(html.includes('id="video-hud-progress-container"'));
  assert.ok(html.includes('id="video-hud-time-current"'));
  assert.ok(html.includes('id="video-hud-progress-track"'));
  assert.ok(html.includes('id="video-hud-progress-fill"'));
  assert.ok(html.includes('id="video-hud-time-total"'));

  // Verify hud.css classes and fairyfloss night mode styles
  assert.ok(css.includes('.video-hud-progress-container'));
  assert.ok(css.includes('.video-hud-progress-track'));
  assert.ok(css.includes('.video-hud-progress-fill'));
  assert.ok(css.includes('.video-hud-time'));
  assert.ok(css.includes('[data-theme="night"] .video-hud-progress-fill'));
  assert.ok(css.includes('[data-theme="night"] .video-hud-progress-track'));
  assert.ok(css.includes('[data-theme="night"] .video-hud-time'));
});

test('VideoHUDController: click-to-seek updates position and dispatches seek action payload with value', async () => {
  const dom = createHUDDOM();
  const networkCalls = [];

  const mockFetch = async (url, opts) => {
    networkCalls.push({ url, body: JSON.parse(opts.body) });
    return { ok: true, status: 200, json: async () => ({ status: 'ok' }) };
  };

  const hud = new VideoHUDController({
    overlayElement: dom.overlay,
    titleElement: dom.title,
    bottomBarElement: dom.bottomBar,
    progressContainer: dom.progressContainer,
    progressTrackElement: dom.progressTrack,
    progressFillElement: dom.progressFill,
    timeCurrentElement: dom.timeCurrent,
    timeTotalElement: dom.timeTotal,
    fetch: mockFetch,
  });

  hud.handleVideoState({
    mode: 'video',
    primary: {
      id: 'chromecast',
      controllable: true,
      control_url: 'http://127.0.0.1:8009/action',
      player_state: 'playing',
      position: 100,
      duration: 1000,
    },
  });

  // Track width is 200px (left: 0). Click at clientX: 100 is 50% = 500s.
  dom.progressTrack.dispatchEvent('pointerdown', { clientX: 100, pointerId: 1 });
  assert.equal(hud.isScrubbing, true);
  assert.equal(dom.progressTrack.classList.contains('is-scrubbing'), true);
  assert.equal(dom.progressFill.style.width, '50.0%');
  assert.equal(dom.timeCurrent.textContent, '8:20');

  dom.progressTrack.dispatchEvent('pointerup', { clientX: 100, pointerId: 1 });
  assert.equal(hud.isScrubbing, false);
  assert.equal(dom.progressTrack.classList.contains('is-scrubbing'), false);
  assert.equal(hud.basePosition, 500);

  assert.equal(networkCalls.length, 1);
  assert.equal(networkCalls[0].url, 'api/video/action');
  assert.deepEqual(networkCalls[0].body, {
    id: 'chromecast',
    action: 'seek',
    value: 500,
  });

  hud.destroy();
});

test('VideoHUDController: drag scrubbing tracks pointermove and releases on pointerup / pointercancel', async () => {
  const dom = createHUDDOM();
  const networkCalls = [];

  const mockFetch = async (url, opts) => {
    networkCalls.push({ url, body: JSON.parse(opts.body) });
    return { ok: true, status: 200, json: async () => ({ status: 'ok' }) };
  };

  const hud = new VideoHUDController({
    overlayElement: dom.overlay,
    titleElement: dom.title,
    bottomBarElement: dom.bottomBar,
    progressContainer: dom.progressContainer,
    progressTrackElement: dom.progressTrack,
    progressFillElement: dom.progressFill,
    timeCurrentElement: dom.timeCurrent,
    timeTotalElement: dom.timeTotal,
    fetch: mockFetch,
  });

  hud.handleVideoState({
    mode: 'video',
    primary: {
      id: 'chromecast',
      controllable: true,
      control_url: 'http://127.0.0.1:8009/action',
      player_state: 'playing',
      position: 50,
      duration: 600,
    },
  });

  // 1. Pointerdown at clientX: 50 (25% = 150s)
  dom.progressTrack.dispatchEvent('pointerdown', { clientX: 50, pointerId: 2 });
  assert.equal(hud.isScrubbing, true);
  assert.equal(dom.progressFill.style.width, '25.0%');
  assert.equal(dom.timeCurrent.textContent, '2:30');

  // 2. Drag pointermove to clientX: 150 (75% = 450s)
  dom.progressTrack.dispatchEvent('pointermove', { clientX: 150, pointerId: 2 });
  assert.equal(dom.progressFill.style.width, '75.0%');
  assert.equal(dom.timeCurrent.textContent, '7:30');

  // 3. Drag out of bounds clamped (>200px -> 100% = 600s)
  dom.progressTrack.dispatchEvent('pointermove', { clientX: 250, pointerId: 2 });
  assert.equal(dom.progressFill.style.width, '100.0%');
  assert.equal(dom.timeCurrent.textContent, '10:00');

  // 4. Drag out of bounds clamped (<0px -> 0% = 0s)
  dom.progressTrack.dispatchEvent('pointermove', { clientX: -20, pointerId: 2 });
  assert.equal(dom.progressFill.style.width, '0.0%');
  assert.equal(dom.timeCurrent.textContent, '0:00');

  // 5. Release via pointerup
  dom.progressTrack.dispatchEvent('pointerup', { pointerId: 2 });
  assert.equal(hud.isScrubbing, false);
  assert.equal(networkCalls.length, 1);
  assert.equal(networkCalls[0].url, 'api/video/action');
  assert.deepEqual(networkCalls[0].body, {
    id: 'chromecast',
    action: 'seek',
    value: 0,
  });

  // Reset in-flight lock for second test action
  hud.isActionPending = false;

  // 6. Test pointercancel scrubbing abort / release
  dom.progressTrack.dispatchEvent('pointerdown', { clientX: 100, pointerId: 3 });
  assert.equal(hud.isScrubbing, true);
  dom.progressTrack.dispatchEvent('pointermove', { clientX: 160, pointerId: 3 }); // 160/200 = 80% = 480s
  assert.equal(dom.progressFill.style.width, '80.0%');
  dom.progressTrack.dispatchEvent('pointercancel', { pointerId: 3 });
  assert.equal(hud.isScrubbing, false);
  assert.equal(networkCalls.length, 2);
  assert.equal(networkCalls[1].url, 'api/video/action');
  assert.deepEqual(networkCalls[1].body, {
    id: 'chromecast',
    action: 'seek',
    value: 480,
  });

  hud.destroy();
});

test('VideoHUDController: renderProgressTick is suppressed during active scrubbing', async () => {
  const dom = createHUDDOM();
  const hud = new VideoHUDController({
    overlayElement: dom.overlay,
    titleElement: dom.title,
    bottomBarElement: dom.bottomBar,
    progressContainer: dom.progressContainer,
    progressTrackElement: dom.progressTrack,
    progressFillElement: dom.progressFill,
    timeCurrentElement: dom.timeCurrent,
    timeTotalElement: dom.timeTotal,
    fetch: async () => ({ ok: true, status: 200, json: async () => ({ status: 'ok' }) }),
  });

  const now = Date.now();
  hud.handleVideoState({
    mode: 'video',
    primary: {
      id: 'chromecast',
      controllable: true,
      player_state: 'playing',
      position: 100,
      duration: 1000,
      position_updated_at: now - 10000,
    },
  });

  // Active scrub to 300s (clientX: 60 / 200 = 30%)
  dom.progressTrack.dispatchEvent('pointerdown', { clientX: 60 });
  assert.equal(hud.isScrubbing, true);
  assert.equal(dom.progressFill.style.width, '30.0%');
  assert.equal(dom.timeCurrent.textContent, '5:00');

  // Trigger renderProgressTick while scrubbing
  hud.renderProgressTick();
  // Fill and time label should NOT be updated by ticker drift!
  assert.equal(dom.progressFill.style.width, '30.0%');
  assert.equal(dom.timeCurrent.textContent, '5:00');

  // Release scrub
  dom.progressTrack.dispatchEvent('pointerup', {});
  assert.equal(hud.isScrubbing, false);

  hud.destroy();
});

test('VideoHUDController: handleSeek dispatches seek action and handles remote keys fallback', async () => {
  const dom = createHUDDOM();
  const networkCalls = [];

  const mockFetch = async (url, opts) => {
    networkCalls.push({ url, body: JSON.parse(opts.body) });
    return { ok: true, status: 200, json: async () => ({ status: 'ok' }) };
  };

  const hud = new VideoHUDController({
    overlayElement: dom.overlay,
    titleElement: dom.title,
    bottomBarElement: dom.bottomBar,
    progressContainer: dom.progressContainer,
    fetch: mockFetch,
    remoteUrl: 'http://remote.lan:8080',
  });

  // Controllable stream without control_url forwards to api/video/action with value
  hud.handleVideoState({
    mode: 'video',
    primary: {
      id: 'stream_plex',
      controllable: true,
      duration: 500,
      player_state: 'playing',
    },
  });

  await hud.handleSeek(250.5);
  assert.equal(networkCalls.length, 1);
  assert.equal(networkCalls[0].url, 'api/video/action');
  assert.deepEqual(networkCalls[0].body, {
    id: 'stream_plex',
    action: 'seek',
    value: 250.5,
  });

  hud.destroy();
});

test('VideoHUDController: rapid successive seek actions bypass isActionPending lock and updateProgress protects mid-scrub', async () => {
  const dom = createHUDDOM();
  const networkCalls = [];
  const mockFetch = async (url, options) => {
    networkCalls.push({ url, body: JSON.parse(options.body) });
    return { ok: true, json: async () => ({ status: 'ok' }) };
  };

  const hud = new VideoHUDController({
    overlayElement: dom.overlay,
    titleElement: dom.title,
    bottomBarElement: dom.bottomBar,
    progressContainer: dom.progressContainer,
    progressTrackElement: dom.progressTrack,
    progressFillElement: dom.progressFill,
    timeCurrentElement: dom.timeCurrent,
    timeTotalElement: dom.timeTotal,
    fetch: mockFetch,
  });

  hud.handleVideoState({
    mode: 'video',
    primary: {
      id: 'chromecast',
      controllable: true,
      control_url: 'http://cast-watcher:8090/action',
      duration: 1000,
      position: 100,
      player_state: 'playing',
    },
  });

  // 1. Rapid successive seek calls should both dispatch
  hud.handleSeek(200);
  assert.equal(hud.isActionPending, true);
  await hud.handleSeek(300);
  assert.equal(networkCalls.length, 2);
  assert.equal(networkCalls[0].body.value, 200);
  assert.equal(networkCalls[1].body.value, 300);

  // 2. updateProgress is a no-op while isScrubbing is true
  hud.isScrubbing = true;
  hud.handleVideoState({
    mode: 'video',
    primary: {
      id: 'chromecast',
      duration: 5000,
      position: 4000,
    },
  });
  // baseDuration remains protected from mid-gesture mutation
  assert.equal(hud.baseDuration, 1000);

  hud.isScrubbing = false;
  hud.destroy();
});

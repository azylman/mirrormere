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
  const title = createMockElement('span', 'video-hud-title');
  const dismissBtn = createMockElement('button', 'video-hud-dismiss-btn', 'video-hud-btn video-hud-dismiss');

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

  const rewindBtn = createMockElement('button', 'video-hud-rewind-btn', 'video-hud-btn video-hud-media-btn');
  const forwardBtn = createMockElement('button', 'video-hud-forward-btn', 'video-hud-btn video-hud-media-btn');

  bottomBar.appendChild(rewindBtn);
  bottomBar.appendChild(playBtn);
  bottomBar.appendChild(forwardBtn);

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
    title,
    dismissBtn,
    controls,
    playBtn,
    playIcon,
    muteBtn,
    muteIcon,
    volumeSlider,
    bottomBar,
    rewindBtn,
    forwardBtn,
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



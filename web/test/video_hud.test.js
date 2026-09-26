const test = require('node:test');
const assert = require('node:assert/strict');
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

    get innerHTML() {
      return '';
    },
    set innerHTML(val) {
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

  overlay.appendChild(header);
  overlay.appendChild(controls);

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
  assert.equal(dom.playIcon.textContent, '⏸');
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
  assert.equal(dom.playIcon.textContent, '▶');
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
  assert.equal(dom.muteIcon.textContent, '🔊');
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
  assert.equal(dom.muteIcon.textContent, '🔇');
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

test('VideoHUDController: dismiss button sends id: "all" when both primary and PiP are active per SPEC-004 §2', async () => {
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

test('VideoHUDController: dismiss button sends id: "all" in swapped presentation state per SPEC-004 §2', async () => {
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


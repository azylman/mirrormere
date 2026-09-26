const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const { VoiceHUDController } = require('../static/js/voice.js');
const { AudioManager } = require('../static/js/audio.js');

/**
 * Lightweight mock DOM element for hermetic Node.js testing.
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
      return stopped;
    },

    click() {
      return element.dispatchEvent('click');
    },
  };

  return element;
}

/**
 * Helper to build mock DOM tree for voice HUD tests.
 */
function createVoiceDOM() {
  const headerVoice = createMockElement('div', 'header-voice');
  const voiceIndicator = createMockElement('div', 'voice-indicator');
  const voiceDot = createMockElement('span', 'voice-dot');
  const voicePulseRing = createMockElement('span', 'voice-pulse-ring');
  const voiceLabel = createMockElement('span', 'voice-label');
  const voiceTranscript = createMockElement('div', 'voice-transcript');

  const voiceStatus = createMockElement('span', 'voice-status');
  voiceStatus.style.display = 'none';

  voiceIndicator.appendChild(voicePulseRing);
  voiceIndicator.appendChild(voiceDot);
  voiceIndicator.appendChild(voiceLabel);
  headerVoice.appendChild(voiceIndicator);
  headerVoice.appendChild(voiceStatus);
  headerVoice.appendChild(voiceTranscript);

  const toastDock = createMockElement('div', 'voice-toast-dock');
  const toast = createMockElement('div', 'voice-toast');
  const toastBadge = createMockElement('span', 'voice-toast-badge');
  const toastBody = createMockElement('span', 'voice-toast-body');

  toast.appendChild(toastBadge);
  toast.appendChild(toastBody);
  toastDock.appendChild(toast);

  return {
    headerVoice,
    voiceIndicator,
    voiceDot,
    voicePulseRing,
    voiceLabel,
    voiceStatus,
    voiceTranscript,
    toastDock,
    toast,
    toastBadge,
    toastBody,
  };
}

test('Touch Kiosk Voice HUD, Caption Toasts & Dynamic Video Ducking (SPEC-010 §5, SPEC-011 §2–§5)', async (t) => {
  await t.test('initializes with default idle state and hidden elements', () => {
    const dom = createVoiceDOM();
    const audioManager = new AudioManager({ initialVolume: 75 });
    const controller = new VoiceHUDController({
      ...dom,
      headerVoiceElement: dom.headerVoice,
      voiceIndicatorElement: dom.voiceIndicator,
      voiceDotElement: dom.voiceDot,
      voicePulseRingElement: dom.voicePulseRing,
      voiceLabelElement: dom.voiceLabel,
      voiceTranscriptElement: dom.voiceTranscript,
      toastDockElement: dom.toastDock,
      toastElement: dom.toast,
      toastBadgeElement: dom.toastBadge,
      toastBodyElement: dom.toastBody,
      audioManager,
    });

    assert.strictEqual(controller.currentState, 'idle');
    assert.strictEqual(controller.turnDismissed, false);
    assert.strictEqual(dom.headerVoice.classList.contains('visible'), false);
    assert.strictEqual(dom.toast.classList.contains('visible'), false);
    assert.strictEqual(audioManager.ducked, false);
    assert.strictEqual(audioManager.getEffectiveVolume(), 0.60);
  });

  await t.test('Listening State Visibility Invariant (SPEC-011 §5)', () => {
    const dom = createVoiceDOM();
    const mediaEl = { volume: 1.0, isConnected: true };
    const audioManager = new AudioManager({ initialVolume: 75, mediaElements: [mediaEl] });
    const controller = new VoiceHUDController({
      ...dom,
      headerVoiceElement: dom.headerVoice,
      voiceIndicatorElement: dom.voiceIndicator,
      voiceDotElement: dom.voiceDot,
      voicePulseRingElement: dom.voicePulseRing,
      voiceLabelElement: dom.voiceLabel,
      voiceTranscriptElement: dom.voiceTranscript,
      toastDockElement: dom.toastDock,
      toastElement: dom.toast,
      toastBadgeElement: dom.toastBadge,
      toastBodyElement: dom.toastBody,
      audioManager,
    });

    controller.handleVoiceState({ state: 'listening' });

    // Listening state invariant: Header voice is visible and radar ring is pulsing
    assert.strictEqual(dom.headerVoice.classList.contains('visible'), true);
    assert.strictEqual(dom.voiceIndicator.classList.contains('listening'), true);
    assert.strictEqual(dom.voiceLabel.textContent, 'Listening...');
    assert.strictEqual(dom.voiceTranscript.style.display, 'none');

    // Dynamic video audio ducking: active video attenuated to 20%
    assert.strictEqual(audioManager.ducked, true);
    assert.strictEqual(audioManager.getEffectiveVolume(), 0.12);
    assert.strictEqual(mediaEl.volume, 0.12);
  });

  await t.test('Transcribing & Thinking states: stops pulse ring and presents recognized transcript', () => {
    const dom = createVoiceDOM();
    const audioManager = new AudioManager({ initialVolume: 75 });
    const controller = new VoiceHUDController({
      ...dom,
      headerVoiceElement: dom.headerVoice,
      voiceIndicatorElement: dom.voiceIndicator,
      voiceDotElement: dom.voiceDot,
      voicePulseRingElement: dom.voicePulseRing,
      voiceLabelElement: dom.voiceLabel,
      voiceTranscriptElement: dom.voiceTranscript,
      toastDockElement: dom.toastDock,
      toastElement: dom.toast,
      toastBadgeElement: dom.toastBadge,
      toastBodyElement: dom.toastBody,
      audioManager,
    });

    // 1. Enter listening
    controller.handleVoiceState({ state: 'listening' });
    assert.strictEqual(dom.voiceIndicator.classList.contains('listening'), true);

    // 2. Transition to transcribing (pulse ring must stop per SPEC-011)
    controller.handleVoiceState({ state: 'transcribing' });
    assert.strictEqual(dom.voiceIndicator.classList.contains('listening'), false);
    assert.strictEqual(dom.voiceIndicator.classList.contains('transcribing'), true);
    assert.strictEqual(dom.voiceLabel.textContent, 'Transcribing...');
    assert.strictEqual(audioManager.ducked, true);

    // 3. Transition to thinking with STT transcript
    controller.handleVoiceState({
      state: 'thinking',
      transcript: "What time is Alex's next meeting?",
    });
    assert.strictEqual(dom.voiceIndicator.classList.contains('transcribing'), false);
    assert.strictEqual(dom.voiceIndicator.classList.contains('thinking'), true);
    assert.strictEqual(dom.voiceLabel.textContent, 'Thinking...');
    assert.strictEqual(dom.voiceTranscript.textContent, '"What time is Alex\'s next meeting?"');
    assert.strictEqual(dom.voiceTranscript.style.display, '');
    assert.strictEqual(audioManager.ducked, true);

    // 4. Synthesizing state retains transcript
    controller.handleVoiceState({ state: 'synthesizing' });
    assert.strictEqual(dom.voiceLabel.textContent, 'Synthesizing...');
    assert.strictEqual(dom.voiceTranscript.textContent, '"What time is Alex\'s next meeting?"');
    assert.strictEqual(audioManager.ducked, true);
  });

  await t.test('Speaking state pops caption toast with reply and TTS engine badges', () => {
    const dom = createVoiceDOM();
    const audioManager = new AudioManager({ initialVolume: 75 });
    const controller = new VoiceHUDController({
      ...dom,
      headerVoiceElement: dom.headerVoice,
      voiceIndicatorElement: dom.voiceIndicator,
      voiceDotElement: dom.voiceDot,
      voicePulseRingElement: dom.voicePulseRing,
      voiceLabelElement: dom.voiceLabel,
      voiceTranscriptElement: dom.voiceTranscript,
      toastDockElement: dom.toastDock,
      toastElement: dom.toast,
      toastBadgeElement: dom.toastBadge,
      toastBodyElement: dom.toastBody,
      audioManager,
    });

    // Kokoro engine (cyan badge)
    controller.handleVoiceState({
      state: 'speaking',
      transcript: "What time is Alex's next meeting?",
      reply: 'Your next meeting is Project Sync at 2:00 PM.',
      tts_engine: 'kokoro',
    });

    assert.strictEqual(dom.toast.classList.contains('visible'), true);
    assert.strictEqual(dom.toast.style.display, 'flex');
    assert.strictEqual(dom.toastBody.textContent, 'Your next meeting is Project Sync at 2:00 PM.');
    assert.strictEqual(dom.toastBadge.textContent, 'kokoro');
    assert.strictEqual(dom.toastBadge.dataset.engine, 'kokoro');
    assert.strictEqual(dom.toastBadge.style.display, '');
    assert.strictEqual(audioManager.ducked, true);

    // ElevenLabs engine (purple badge)
    controller.handleVoiceState({
      state: 'speaking',
      reply: 'Hello from ElevenLabs.',
      tts_engine: 'ElevenLabs',
    });
    assert.strictEqual(dom.toastBadge.textContent, 'elevenlabs');
    assert.strictEqual(dom.toastBadge.dataset.engine, 'elevenlabs');

    // Piper engine (amber degraded fallback badge per SPEC-011 §3.3)
    controller.handleVoiceState({
      state: 'speaking',
      reply: 'Offline fallback active.',
      tts_engine: 'piper',
    });
    assert.strictEqual(dom.toastBadge.textContent, 'piper');
    assert.strictEqual(dom.toastBadge.dataset.engine, 'piper');

    // Null/missing TTS engine hides badge cleanly
    controller.handleVoiceState({
      state: 'speaking',
      reply: 'No engine badge provided.',
      tts_engine: null,
    });
    assert.strictEqual(dom.toastBadge.style.display, 'none');

    // Empty reply text does NOT pop empty toast
    controller.resetHUD();
    controller.handleVoiceState({
      state: 'speaking',
      reply: null,
    });
    assert.strictEqual(dom.toast.classList.contains('visible'), false);
    assert.strictEqual(dom.toast.style.display, 'none');
  });

  await t.test('Auto-dismisses caption toast and restores volume on idle and error transitions', () => {
    const dom = createVoiceDOM();
    const mediaEl = { volume: 1.0, isConnected: true };
    const audioManager = new AudioManager({ initialVolume: 75, mediaElements: [mediaEl] });
    const controller = new VoiceHUDController({
      ...dom,
      headerVoiceElement: dom.headerVoice,
      voiceIndicatorElement: dom.voiceIndicator,
      voiceDotElement: dom.voiceDot,
      voicePulseRingElement: dom.voicePulseRing,
      voiceLabelElement: dom.voiceLabel,
      voiceTranscriptElement: dom.voiceTranscript,
      toastDockElement: dom.toastDock,
      toastElement: dom.toast,
      toastBadgeElement: dom.toastBadge,
      toastBodyElement: dom.toastBody,
      audioManager,
    });

    // Active speaking turn
    controller.handleVoiceState({
      state: 'speaking',
      reply: 'Weather is 72 degrees.',
      tts_engine: 'kokoro',
    });
    assert.strictEqual(dom.toast.classList.contains('visible'), true);
    assert.strictEqual(audioManager.ducked, true);
    assert.strictEqual(mediaEl.volume, 0.12);

    // Transition to idle
    controller.handleVoiceState({ state: 'idle' });
    assert.strictEqual(controller.currentState, 'idle');
    assert.strictEqual(dom.toast.classList.contains('visible'), false);
    assert.strictEqual(dom.toast.style.display, 'none');
    assert.strictEqual(dom.headerVoice.classList.contains('visible'), false);
    assert.strictEqual(dom.voiceTranscript.textContent, '');
    assert.strictEqual(audioManager.ducked, false);
    assert.strictEqual(mediaEl.volume, 0.60);

    // Active listening turn transitioning to error
    controller.handleVoiceState({ state: 'listening' });
    assert.strictEqual(audioManager.ducked, true);
    assert.strictEqual(mediaEl.volume, 0.12);

    controller.handleVoiceState({ state: 'error' });
    assert.strictEqual(controller.currentState, 'idle');
    assert.strictEqual(dom.headerVoice.classList.contains('visible'), false);
    assert.strictEqual(audioManager.ducked, false);
    assert.strictEqual(mediaEl.volume, 0.60);
  });

  await t.test('Tap-to-dismiss suppresses toast reopen for the current turn', () => {
    const dom = createVoiceDOM();
    const audioManager = new AudioManager({ initialVolume: 75 });
    const controller = new VoiceHUDController({
      ...dom,
      headerVoiceElement: dom.headerVoice,
      voiceIndicatorElement: dom.voiceIndicator,
      voiceDotElement: dom.voiceDot,
      voicePulseRingElement: dom.voicePulseRing,
      voiceLabelElement: dom.voiceLabel,
      voiceTranscriptElement: dom.voiceTranscript,
      toastDockElement: dom.toastDock,
      toastElement: dom.toast,
      toastBadgeElement: dom.toastBadge,
      toastBodyElement: dom.toastBody,
      audioManager,
    });

    // Start speaking turn
    controller.handleVoiceState({
      state: 'speaking',
      reply: 'Playing favorite playlist.',
      tts_engine: 'kokoro',
    });
    assert.strictEqual(dom.toast.classList.contains('visible'), true);

    // User taps toast card to dismiss it
    const stopped = dom.toast.click();
    assert.strictEqual(stopped, true, 'tap must call stopPropagation');
    assert.strictEqual(controller.turnDismissed, true);
    assert.strictEqual(dom.toast.classList.contains('visible'), false);
    assert.strictEqual(dom.toast.style.display, 'none');

    // Repeated audio chunks or state updates during the same turn must NOT reopen toast
    controller.handleVoiceState({
      state: 'speaking',
      reply: 'Playing favorite playlist.',
      tts_engine: 'kokoro',
    });
    assert.strictEqual(dom.toast.classList.contains('visible'), false);
    assert.strictEqual(dom.toast.style.display, 'none');

    // New listening turn resets turnDismissed
    controller.handleVoiceState({ state: 'listening' });
    assert.strictEqual(controller.turnDismissed, false);
  });

  await t.test('Safety watchdog resets HUD and unducks audio on dropped connection', async () => {
    const dom = createVoiceDOM();
    const audioManager = new AudioManager({ initialVolume: 75 });
    const controller = new VoiceHUDController({
      ...dom,
      headerVoiceElement: dom.headerVoice,
      voiceIndicatorElement: dom.voiceIndicator,
      voiceDotElement: dom.voiceDot,
      voicePulseRingElement: dom.voicePulseRing,
      voiceLabelElement: dom.voiceLabel,
      voiceTranscriptElement: dom.voiceTranscript,
      toastDockElement: dom.toastDock,
      toastElement: dom.toast,
      toastBadgeElement: dom.toastBadge,
      toastBodyElement: dom.toastBody,
      audioManager,
      safetyTimeoutMs: 50, // Short timeout for test
    });

    controller.handleVoiceState({ state: 'listening' });
    assert.strictEqual(audioManager.ducked, true);

    // Wait for safety timeout to expire
    await new Promise((resolve) => setTimeout(resolve, 80));

    assert.strictEqual(controller.currentState, 'idle');
    assert.strictEqual(audioManager.ducked, false);
    assert.strictEqual(dom.headerVoice.classList.contains('visible'), false);

    controller.destroy();
  });

  await t.test('SSE subscription via attachSSE (.on and .addEventListener)', () => {
    const dom = createVoiceDOM();
    const audioManager = new AudioManager({ initialVolume: 75 });

    // 1. MirrormereSSE client using .on()
    const onListeners = {};
    const mockSSEOn = {
      on(event, cb) { onListeners[event] = cb; },
    };
    const c1 = new VoiceHUDController({
      ...dom,
      headerVoiceElement: dom.headerVoice,
      voiceIndicatorElement: dom.voiceIndicator,
      voiceDotElement: dom.voiceDot,
      voicePulseRingElement: dom.voicePulseRing,
      voiceLabelElement: dom.voiceLabel,
      voiceTranscriptElement: dom.voiceTranscript,
      toastDockElement: dom.toastDock,
      toastElement: dom.toast,
      toastBadgeElement: dom.toastBadge,
      toastBodyElement: dom.toastBody,
      audioManager,
      sseClient: mockSSEOn,
    });
    assert.strictEqual(typeof onListeners['voice.state'], 'function');
    onListeners['voice.state']({ state: 'listening' });
    assert.strictEqual(c1.currentState, 'listening');

    // 2. Standard EventSource using .addEventListener()
    const addListeners = {};
    const mockEventSource = {
      addEventListener(event, cb) { addListeners[event] = cb; },
    };
    const c2 = new VoiceHUDController({
      ...dom,
      headerVoiceElement: dom.headerVoice,
      voiceIndicatorElement: dom.voiceIndicator,
      voiceDotElement: dom.voiceDot,
      voicePulseRingElement: dom.voicePulseRing,
      voiceLabelElement: dom.voiceLabel,
      voiceTranscriptElement: dom.voiceTranscript,
      toastDockElement: dom.toastDock,
      toastElement: dom.toast,
      toastBadgeElement: dom.toastBadge,
      toastBodyElement: dom.toastBody,
      audioManager,
      sseClient: mockEventSource,
    });
    assert.strictEqual(typeof addListeners['voice.state'], 'function');
    addListeners['voice.state']({ data: JSON.stringify({ state: 'thinking', transcript: 'Hello' }) });
    assert.strictEqual(c2.currentState, 'thinking');

    // Malformed JSON should not throw
    addListeners['voice.state']({ data: '{invalid json' });
    assert.strictEqual(c2.currentState, 'thinking');
  });

  await t.test('renders live tool status telemetry in thinking/transcribing and clears on speaking/idle', () => {
    const dom = createVoiceDOM();
    const audioManager = new AudioManager({ initialVolume: 75 });
    const controller = new VoiceHUDController({
      ...dom,
      headerVoiceElement: dom.headerVoice,
      voiceIndicatorElement: dom.voiceIndicator,
      voiceStatusElement: dom.voiceStatus,
      voiceTranscriptElement: dom.voiceTranscript,
      audioManager,
      safetyTimeoutMs: 1000,
    });

    // Initial state: status badge hidden
    assert.strictEqual(dom.voiceStatus.style.display, 'none');

    // 1. Thinking with live tool status update
    controller.handleVoiceState({
      state: 'thinking',
      transcript: 'Turn on kitchen lights',
      status: '⚡ Checking Home Assistant...',
    });

    assert.strictEqual(dom.voiceStatus.style.display, '');
    assert.strictEqual(dom.voiceStatus.textContent, '⚡ Checking Home Assistant...');
    assert.strictEqual(controller.currentStatus, '⚡ Checking Home Assistant...');

    // 2. Subsequent status update within thinking phase
    controller.handleVoiceState({
      state: 'thinking',
      status: '💡 Turning on 3 lights...',
    });
    assert.strictEqual(dom.voiceStatus.textContent, '💡 Turning on 3 lights...');
    assert.strictEqual(dom.voiceStatus.style.display, '');

    // 3. Transition to speaking clears status badge
    controller.handleVoiceState({
      state: 'speaking',
      reply: 'Kitchen lights are now on.',
      tts_engine: 'kokoro',
    });
    assert.strictEqual(dom.voiceStatus.style.display, 'none');
    assert.strictEqual(dom.voiceStatus.textContent, '');
    assert.strictEqual(controller.currentStatus, null);

    // 4. Entering listening clears status badge
    controller.handleVoiceState({
      state: 'listening',
    });
    assert.strictEqual(dom.voiceStatus.style.display, 'none');
  });

  await t.test('browser global window.MirrormereVoice binding', () => {
    const voiceCode = fs.readFileSync(path.join(__dirname, '../static/js/voice.js'), 'utf8');
    const mockWindow = {};
    const fn = new Function('window', 'module', voiceCode);
    fn(mockWindow, {});

    assert.ok(mockWindow.MirrormereVoice, 'window.MirrormereVoice should be defined');
    assert.strictEqual(typeof mockWindow.MirrormereVoice.VoiceHUDController, 'function');
  });
});

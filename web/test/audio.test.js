const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const { computeEffectiveVolume, AudioManager } = require('../static/js/audio.js');

test('Audio Coordinator & 80% Software Volume Ceiling (SPEC-004 §3, SPEC-006 §5, SPEC-010 §4)', async (t) => {
  await t.test('computeEffectiveVolume pure function', async (t2) => {
    await t2.test('calculates nominal volume at 80% ceiling without ducking or mute', () => {
      assert.strictEqual(computeEffectiveVolume(100), 0.80);
      assert.strictEqual(computeEffectiveVolume(75), 0.60);
      assert.strictEqual(computeEffectiveVolume(50), 0.40);
      assert.strictEqual(computeEffectiveVolume(25), 0.20);
      assert.strictEqual(computeEffectiveVolume(0), 0.0);
    });

    await t2.test('enforces zero volume when isMuted is true', () => {
      assert.strictEqual(computeEffectiveVolume(100, true), 0.0);
      assert.strictEqual(computeEffectiveVolume(75, true), 0.0);
      assert.strictEqual(computeEffectiveVolume(50, true), 0.0);
      assert.strictEqual(computeEffectiveVolume(0, true), 0.0);
    });

    await t2.test('applies 0.20 ducking multiplier when isDucked is true', () => {
      assert.strictEqual(computeEffectiveVolume(100, false, true), 0.16);
      assert.strictEqual(computeEffectiveVolume(75, false, true), 0.12);
      assert.strictEqual(computeEffectiveVolume(50, false, true), 0.08);
      assert.strictEqual(computeEffectiveVolume(25, false, true), 0.04);
      assert.strictEqual(computeEffectiveVolume(0, false, true), 0.0);
    });

    await t2.test('handles ducked and muted simultaneously', () => {
      assert.strictEqual(computeEffectiveVolume(100, true, true), 0.0);
      assert.strictEqual(computeEffectiveVolume(75, true, true), 0.0);
    });

    await t2.test('clamps out-of-range volume inputs to [0, 100]', () => {
      assert.strictEqual(computeEffectiveVolume(150), 0.80);
      assert.strictEqual(computeEffectiveVolume(1000), 0.80);
      assert.strictEqual(computeEffectiveVolume(-10), 0.0);
      assert.strictEqual(computeEffectiveVolume(-100), 0.0);
    });

    await t2.test('handles numeric strings and non-numeric inputs gracefully', () => {
      assert.strictEqual(computeEffectiveVolume('75'), 0.60);
      assert.strictEqual(computeEffectiveVolume('100'), 0.80);
      assert.strictEqual(computeEffectiveVolume('0'), 0.0);
      assert.strictEqual(computeEffectiveVolume('invalid'), 0.0);
      assert.strictEqual(computeEffectiveVolume(null), 0.0);
      assert.strictEqual(computeEffectiveVolume(undefined), 0.0);
      assert.strictEqual(computeEffectiveVolume(NaN), 0.0);
    });

    await t2.test('rounds floating point results to avoid IEEE 754 precision noise', () => {
      // (55 / 100) * 0.80 = 0.44000000000000006 in standard JS float
      const res = computeEffectiveVolume(55);
      assert.strictEqual(res, 0.44);
    });
  });

  await t.test('AudioManager class lifecycle', async (t2) => {
    function createMockMediaElement(connected = true) {
      return {
        volume: 1.0,
        isConnected: connected,
      };
    }

    await t2.test('initializes with default volume 75 and unmuted', () => {
      const mgr = new AudioManager();
      assert.strictEqual(mgr.volume, 75);
      assert.strictEqual(mgr.muted, false);
      assert.strictEqual(mgr.ducked, false);
      assert.strictEqual(mgr.getEffectiveVolume(), 0.60);
    });

    await t2.test('initializes with custom options', () => {
      const el = createMockMediaElement();
      const mgr = new AudioManager({
        initialVolume: 50,
        initialMuted: true,
        mediaElements: [el],
      });
      assert.strictEqual(mgr.volume, 50);
      assert.strictEqual(mgr.muted, true);
      assert.strictEqual(mgr.getEffectiveVolume(), 0.0);
      // Immediately primed to current effective volume
      assert.strictEqual(el.volume, 0.0);
    });

    await t2.test('registerMediaElement immediately applies current volume', () => {
      const mgr = new AudioManager({ initialVolume: 80 });
      const el1 = createMockMediaElement();
      const el2 = createMockMediaElement();

      mgr.registerMediaElement(el1);
      assert.strictEqual(el1.volume, 0.64);

      mgr.registerMediaElement(el2);
      assert.strictEqual(el2.volume, 0.64);
    });

    await t2.test('registerMediaElement handles null or throwing elements gracefully', () => {
      const mgr = new AudioManager();
      mgr.registerMediaElement(null);
      mgr.registerMediaElement(undefined);

      const throwingEl = {
        get volume() { return 1.0; },
        set volume(v) { throw new Error('DOMException: audio not allowed'); },
        isConnected: true,
      };
      // Must not throw
      mgr.registerMediaElement(throwingEl);
    });

    await t2.test('unregisterMediaElement stops tracking element', () => {
      const mgr = new AudioManager({ initialVolume: 80 });
      const el = createMockMediaElement();
      mgr.registerMediaElement(el);
      assert.strictEqual(el.volume, 0.64);

      mgr.unregisterMediaElement(el);
      mgr.updateAudioState({ volume: 50 });
      // el volume stays 0.64 because it was unregistered
      assert.strictEqual(el.volume, 0.64);
    });

    await t2.test('updateAudioState updates registered elements and clamps values', () => {
      const mgr = new AudioManager();
      const el = createMockMediaElement();
      mgr.registerMediaElement(el);
      assert.strictEqual(el.volume, 0.60); // 75% -> 0.60

      mgr.updateAudioState({ volume: 100 });
      assert.strictEqual(mgr.volume, 100);
      assert.strictEqual(el.volume, 0.80);

      mgr.updateAudioState({ volume: 150 }); // clamped to 100
      assert.strictEqual(mgr.volume, 100);
      assert.strictEqual(el.volume, 0.80);

      mgr.updateAudioState({ volume: -20 }); // clamped to 0
      assert.strictEqual(mgr.volume, 0);
      assert.strictEqual(el.volume, 0.0);

      mgr.updateAudioState({ volume: 75, muted: true });
      assert.strictEqual(mgr.volume, 75);
      assert.strictEqual(mgr.muted, true);
      assert.strictEqual(el.volume, 0.0);

      mgr.updateAudioState({ muted: false });
      assert.strictEqual(mgr.muted, false);
      assert.strictEqual(el.volume, 0.60);
    });

    await t2.test('applyVolumeToAll prunes disconnected elements from set', () => {
      const mgr = new AudioManager({ initialVolume: 75 });
      const elConnected = createMockMediaElement(true);
      const elDisconnected = createMockMediaElement(false);

      mgr.registerMediaElement(elConnected);
      mgr.registerMediaElement(elDisconnected);
      assert.strictEqual(mgr.mediaElements.size, 2);

      mgr.updateAudioState({ volume: 50 });
      assert.strictEqual(elConnected.volume, 0.40);
      assert.strictEqual(mgr.mediaElements.size, 1);
      assert.strictEqual(mgr.mediaElements.has(elConnected), true);
      assert.strictEqual(mgr.mediaElements.has(elDisconnected), false);
    });

    await t2.test('attachSSE works with MirrormereSSE .on() emitter', () => {
      const listeners = {};
      const mockSSE = {
        on(event, cb) {
          listeners[event] = cb;
        },
      };

      const mgr = new AudioManager({ sseClient: mockSSE });
      const el = createMockMediaElement();
      mgr.registerMediaElement(el);

      assert.strictEqual(typeof listeners['audio.state'], 'function');

      // Trigger parsed JSON payload
      listeners['audio.state']({ volume: 50, muted: false });
      assert.strictEqual(mgr.volume, 50);
      assert.strictEqual(el.volume, 0.40);

      listeners['audio.state']({ volume: 50, muted: true });
      assert.strictEqual(mgr.muted, true);
      assert.strictEqual(el.volume, 0.0);
    });

    await t2.test('attachSSE works with standard EventSource .addEventListener() emitter', () => {
      const listeners = {};
      const mockEventSource = {
        addEventListener(event, cb) {
          listeners[event] = cb;
        },
      };

      const mgr = new AudioManager({ sseClient: mockEventSource });
      const el = createMockMediaElement();
      mgr.registerMediaElement(el);

      assert.strictEqual(typeof listeners['audio.state'], 'function');

      // Event with string data
      listeners['audio.state']({ data: JSON.stringify({ volume: 25, muted: false }) });
      assert.strictEqual(mgr.volume, 25);
      assert.strictEqual(el.volume, 0.20);

      // Event with parsed object data
      listeners['audio.state']({ data: { volume: 100, muted: false } });
      assert.strictEqual(mgr.volume, 100);
      assert.strictEqual(el.volume, 0.80);

      // Malformed JSON should not throw
      listeners['audio.state']({ data: '{invalid json' });
      assert.strictEqual(mgr.volume, 100); // preserves last valid state
    });
  });

  await t.test('browser global window.MirrormereAudio binding', () => {
    const audioCode = fs.readFileSync(path.join(__dirname, '../static/js/audio.js'), 'utf8');
    const mockWindow = {};
    const fn = new Function('window', 'module', audioCode);
    fn(mockWindow, {});

    assert.ok(mockWindow.MirrormereAudio, 'window.MirrormereAudio should be defined');
    assert.strictEqual(typeof mockWindow.MirrormereAudio.computeEffectiveVolume, 'function');
    assert.strictEqual(typeof mockWindow.MirrormereAudio.AudioManager, 'function');
  });
});

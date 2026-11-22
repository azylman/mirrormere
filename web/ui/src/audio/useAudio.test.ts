import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { useDisplayStore } from '../store';
import {
  AudioController,
  computeEffectiveVolume,
  useAudioController,
} from './useAudio';

describe('useAudio & AudioController', () => {

  beforeEach(() => {
    delete window.audioManager;
  });

  afterEach(() => {
    delete window.audioManager;
  });

  describe('computeEffectiveVolume', () => {
    // Effective volume = clamp(volume, 0, 100) / 100 * 0.80, * 0.20 when ducked, 0 when muted, rounded to 4 places.
    const tests: { name: string; volume: number; muted?: boolean; ducked?: boolean; want: number }[] = [
      { name: 'normal: 75%', volume: 75, want: 0.6 },
      { name: 'normal: 100%', volume: 100, want: 0.8 },
      { name: 'normal: 50%', volume: 50, want: 0.4 },
      { name: 'normal: 0%', volume: 0, want: 0 },
      { name: 'clamps below 0', volume: -20, want: 0 },
      { name: 'clamps above 100', volume: 150, want: 0.8 },
      { name: 'clamps NaN to 0', volume: Number.NaN, want: 0 },
      { name: 'muted: 75%', volume: 75, muted: true, ducked: false, want: 0 },
      { name: 'muted: 100%', volume: 100, muted: true, ducked: false, want: 0 },
      { name: 'ducked: 75% attenuated to 20%', volume: 75, muted: false, ducked: true, want: 0.12 },
      { name: 'ducked: 100% attenuated to 20%', volume: 100, muted: false, ducked: true, want: 0.16 },
      { name: 'ducked and muted', volume: 75, muted: true, ducked: true, want: 0 },
      { name: 'rounds to 4 decimal places', volume: 33, want: 0.264 },
    ];
    for (const tc of tests) {
      it(tc.name, () => {
        expect(computeEffectiveVolume(tc.volume, tc.muted, tc.ducked)).toBe(tc.want);
      });
    }
  });

  describe('AudioController class', () => {
    it('initializes with default or custom options', () => {
      const defaultCtrl = new AudioController();
      expect(defaultCtrl.volume).toBe(75);
      expect(defaultCtrl.isMuted).toBe(false);
      expect(defaultCtrl.isDucked).toBe(false);
      expect(defaultCtrl.getEffectiveVolume()).toBe(0.60);

      const customCtrl = new AudioController({ initialVolume: 50, initialMuted: true });
      expect(customCtrl.volume).toBe(50);
      expect(customCtrl.isMuted).toBe(true);
      expect(customCtrl.getEffectiveVolume()).toBe(0);
    });

    it('provides muted and ducked getters and setters', () => {
      const ctrl = new AudioController();
      expect(ctrl.muted).toBe(false);
      ctrl.muted = true;
      expect(ctrl.isMuted).toBe(true);
      expect(ctrl.muted).toBe(true);

      expect(ctrl.ducked).toBe(false);
      ctrl.ducked = true;
      expect(ctrl.isDucked).toBe(true);
      expect(ctrl.ducked).toBe(true);
    });

    it('registers media elements and applies volume immediately', () => {
      const ctrl = new AudioController({ initialVolume: 75 });
      const el = document.createElement('video');
      document.body.appendChild(el);

      ctrl.registerMediaElement(el);
      expect(ctrl.mediaElements.has(el)).toBe(true);
      expect(el.volume).toBe(0.60);
      expect(el.muted).toBe(false);

      ctrl.unregisterMediaElement(el);
      expect(ctrl.mediaElements.has(el)).toBe(false);
      document.body.removeChild(el);
    });

    it('handles initial mediaElements in constructor', () => {
      const el = document.createElement('audio');
      document.body.appendChild(el);

      const ctrl = new AudioController({ initialVolume: 50, mediaElements: [el] });
      expect(ctrl.mediaElements.has(el)).toBe(true);
      expect(el.volume).toBe(0.40);
      document.body.removeChild(el);
    });

    it('updates state via handleAudioState and updateAudioState', () => {
      const ctrl = new AudioController();
      const el = document.createElement('video');
      document.body.appendChild(el);
      ctrl.registerMediaElement(el);

      ctrl.handleAudioState({ volume: 90, muted: false });
      expect(ctrl.volume).toBe(90);
      expect(ctrl.isMuted).toBe(false);
      expect(el.volume).toBe(0.72);

      ctrl.updateAudioState({ muted: true });
      expect(ctrl.isMuted).toBe(true);
      expect(el.muted).toBe(true);

      document.body.removeChild(el);
    });

    it('toggles ducking idempotently via setDucked', () => {
      const ctrl = new AudioController({ initialVolume: 75 });
      const el = document.createElement('video');
      document.body.appendChild(el);
      ctrl.registerMediaElement(el);

      ctrl.setDucked(true);
      expect(ctrl.isDucked).toBe(true);
      expect(el.volume).toBe(0.12);

      // Re-calling with same value is a no-op
      ctrl.setDucked(true);
      expect(ctrl.isDucked).toBe(true);

      ctrl.setDucked(false);
      expect(ctrl.isDucked).toBe(false);
      expect(el.volume).toBe(0.60);

      document.body.removeChild(el);
    });

    it('prunes disconnected elements in applyVolumeToAll', () => {
      const ctrl = new AudioController({ initialVolume: 75 });
      const el = document.createElement('video');
      // Not attached to DOM: el.isConnected === false
      ctrl.mediaElements.add(el);
      expect(ctrl.mediaElements.size).toBe(1);

      ctrl.applyVolumeToAll();
      expect(ctrl.mediaElements.size).toBe(0);
    });

    it('clears registered elements on destroy', () => {
      const ctrl = new AudioController();
      const el = document.createElement('video');
      ctrl.mediaElements.add(el);
      ctrl.destroy();
      expect(ctrl.mediaElements.size).toBe(0);
    });
  });

  describe('useAudioController & useAudio hook', () => {
    it('sets window.audioManager on mount and cleans up on unmount', () => {
      const { result, unmount } = renderHook(() => useAudioController());
      expect(result.current).toBeInstanceOf(AudioController);
      expect(window.audioManager).toBe(result.current);

      unmount();
      expect(window.audioManager).toBeUndefined();
    });

    it('reacts to store audio updates', () => {
      const { result } = renderHook(() => useAudioController());
      const el = document.createElement('video');
      document.body.appendChild(el);
      result.current.registerMediaElement(el);

      act(() => {
        useDisplayStore.setState({ audio: { volume: 50, muted: false } });
      });

      expect(result.current.volume).toBe(50);
      expect(el.volume).toBe(0.40);

      act(() => {
        useDisplayStore.setState({ audio: { volume: 50, muted: true } });
      });

      expect(result.current.isMuted).toBe(true);
      expect(el.muted).toBe(true);

      document.body.removeChild(el);
    });

    it('ducks when voice state enters speaking and unducks when idle', () => {
      const { result } = renderHook(() => useAudioController());
      const el = document.createElement('video');
      document.body.appendChild(el);
      result.current.registerMediaElement(el);

      expect(result.current.isDucked).toBe(false);
      expect(el.volume).toBe(0.60);

      act(() => {
        useDisplayStore.setState({
          voice: {
            state: 'speaking',
            transcript: null,
            reply: null,
            tts_engine: null,
            status: null,
          },
        });
      });

      expect(result.current.isDucked).toBe(true);
      expect(el.volume).toBe(0.12);

      act(() => {
        useDisplayStore.setState({
          voice: {
            state: 'idle',
            transcript: null,
            reply: null,
            tts_engine: null,
            status: null,
          },
        });
      });

      expect(result.current.isDucked).toBe(false);
      expect(el.volume).toBe(0.60);

      document.body.removeChild(el);
    });
  });
});

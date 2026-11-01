import { act, cleanup, fireEvent, render } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { AudioController } from '../audio/useAudio';
import { useDisplayStore } from '../store';
import {
  DUCK_STATES,
  VOICE_SVG_MIC,
  VOICE_SVG_SPARKLE,
  VOICE_SVG_THINK,
  VoiceHUDController,
  VoiceHud,
} from './VoiceHud';

function setupHeaderVoiceDOM() {
  const container = document.createElement('div');
  container.innerHTML = `
    <div class="header-voice" id="header-voice">
      <div class="voice-indicator" id="voice-indicator">
        <span class="voice-pulse-ring" id="voice-pulse-ring"></span>
        <span class="voice-dot" id="voice-dot" style="display: none;"></span>
        <span class="voice-icon" id="voice-icon"></span>
        <span class="voice-label" id="voice-label"></span>
      </div>
      <span class="voice-status" id="voice-status" style="display: none;"></span>
      <div class="voice-transcript" id="voice-transcript" style="display: none;"></div>
      <button id="header-voice-dismiss-btn" class="header-voice-dismiss" type="button" aria-label="Dismiss Voice">✕</button>
      <div class="voice-audio-progress" id="voice-audio-progress" style="display: none;"></div>
    </div>
  `;
  const dismissBtn = container.querySelector('#header-voice-dismiss-btn');
  dismissBtn?.addEventListener('click', () => {
    window.MirrormereVoice?.resetHUD?.();
  });
  document.body.appendChild(container);
  return container;
}

describe('VoiceHud and VoiceHUDController', () => {
  const initialStore = useDisplayStore.getState();
  let headerContainer: HTMLElement | null = null;

  beforeEach(() => {
    useDisplayStore.setState(initialStore, true);
    delete window.audioManager;
    delete window.voiceHUD;
    headerContainer = setupHeaderVoiceDOM();
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    useDisplayStore.setState(initialStore, true);
    delete window.audioManager;
    delete window.voiceHUD;
    if (headerContainer && headerContainer.parentNode) {
      headerContainer.parentNode.removeChild(headerContainer);
    }
  });

  describe('constants & icons', () => {
    it('exports duck states and SVGs', () => {
      expect(DUCK_STATES.has('listening')).toBe(true);
      expect(DUCK_STATES.has('transcribing')).toBe(true);
      expect(DUCK_STATES.has('thinking')).toBe(true);
      expect(DUCK_STATES.has('synthesizing')).toBe(true);
      expect(DUCK_STATES.has('speaking')).toBe(true);
      expect(DUCK_STATES.has('idle')).toBe(false);

      expect(VOICE_SVG_MIC).toContain('mm-voice-icon-svg');
      expect(VOICE_SVG_THINK).toContain('mm-voice-icon-svg');
      expect(VOICE_SVG_SPARKLE).toContain('mm-voice-icon-svg');
    });
  });

  describe('VoiceHud component state transitions and rendering', () => {
    it('initializes with idle state and exposes window bindings', () => {
      const { unmount } = render(<VoiceHud />);

      expect(window.voiceHUD).toBeDefined();
      expect(window.MirrormereVoice).toBeDefined();
      expect(typeof window.MirrormereVoice?.resetHUD).toBe('function');
      expect(window.voiceHUD?.currentState).toBe('idle');

      const headerVoice = document.getElementById('header-voice');
      const toast = document.getElementById('voice-toast');
      expect(headerVoice?.classList.contains('visible')).toBe(false);
      expect(toast?.classList.contains('visible')).toBe(false);
      expect(toast?.style.display).toBe('none');

      unmount();
      expect(window.voiceHUD).toBeUndefined();
    });

    it('creates fallback AudioController on window.audioManager when absent and tears down on unmount', () => {
      expect(window.audioManager).toBeUndefined();
      const { unmount } = render(<VoiceHud />);

      expect(window.audioManager).toBeInstanceOf(AudioController);
      const fallback = window.audioManager;

      unmount();
      expect(window.audioManager).toBeUndefined();
      expect(fallback?.mediaElements.size).toBe(0);
    });

    it('does not overwrite existing window.audioManager if already provided', () => {
      const mockManager = new AudioController({ initialVolume: 80 });
      window.audioManager = mockManager;

      const { unmount } = render(<VoiceHud />);
      expect(window.audioManager).toBe(mockManager);

      unmount();
      expect(window.audioManager).toBe(mockManager);
    });

    it('handles listening state: reveals header, activates pulse ring, ducks audio, hides toast', () => {
      render(<VoiceHud />);
      const headerVoice = document.getElementById('header-voice')!;
      const indicator = document.getElementById('voice-indicator')!;
      const label = document.getElementById('voice-label')!;
      const transcript = document.getElementById('voice-transcript')!;
      const toast = document.getElementById('voice-toast')!;

      act(() => {
        useDisplayStore.setState({
          voice: {
            state: 'listening',
            transcript: null,
            reply: null,
            tts_engine: null,
            status: null,
          },
        });
      });

      expect(headerVoice.classList.contains('visible')).toBe(true);
      expect(indicator.classList.contains('listening')).toBe(true);
      expect(label.textContent).toBe('Listening...');
      expect(transcript.style.display).toBe('none');
      expect(toast.style.display).toBe('none');
      expect(window.audioManager?.isDucked).toBe(true);
    });

    it('handles transcribing state: halts pulse ring, renders label and transcript', () => {
      render(<VoiceHud />);
      const indicator = document.getElementById('voice-indicator')!;
      const label = document.getElementById('voice-label')!;
      const transcript = document.getElementById('voice-transcript')!;

      act(() => {
        useDisplayStore.setState({
          voice: {
            state: 'transcribing',
            transcript: 'Turn on the patio heater',
            reply: null,
            tts_engine: null,
            status: null,
          },
        });
      });

      expect(indicator.classList.contains('listening')).toBe(false);
      expect(indicator.classList.contains('transcribing')).toBe(true);
      expect(label.textContent).toBe('Transcribing...');
      expect(transcript.textContent).toBe('"Turn on the patio heater"');
      expect(transcript.style.display).toBe('');
      expect(window.audioManager?.isDucked).toBe(true);
    });

    it('handles thinking and synthesizing states', () => {
      render(<VoiceHud />);
      const indicator = document.getElementById('voice-indicator')!;
      const label = document.getElementById('voice-label')!;
      const transcript = document.getElementById('voice-transcript')!;

      // Thinking
      act(() => {
        useDisplayStore.setState({
          voice: {
            state: 'thinking',
            transcript: 'What is the temperature?',
            reply: null,
            tts_engine: null,
            status: null,
          },
        });
      });

      expect(indicator.classList.contains('transcribing')).toBe(false);
      expect(indicator.classList.contains('thinking')).toBe(true);
      expect(label.textContent).toBe('Thinking...');
      expect(transcript.textContent).toBe('"What is the temperature?"');
      expect(window.audioManager?.isDucked).toBe(true);

      // Synthesizing
      act(() => {
        useDisplayStore.setState({
          voice: {
            state: 'synthesizing',
            transcript: 'What is the temperature?',
            reply: null,
            tts_engine: null,
            status: null,
          },
        });
      });

      expect(label.textContent).toBe('Synthesizing...');
      expect(transcript.textContent).toBe('"What is the temperature?"');
      expect(window.audioManager?.isDucked).toBe(true);
    });

    it('handles speaking state with caption toasts and various TTS engine badges', () => {
      render(<VoiceHud />);
      const indicator = document.getElementById('voice-indicator')!;
      const label = document.getElementById('voice-label')!;
      const transcript = document.getElementById('voice-transcript')!;
      const toast = document.getElementById('voice-toast')!;
      const toastBadge = document.getElementById('voice-toast-badge')!;
      const toastBody = document.getElementById('voice-toast-body')!;
      const audioProgress = document.getElementById('voice-audio-progress')!;

      // 1. Kokoro engine
      act(() => {
        useDisplayStore.setState({
          voice: {
            state: 'speaking',
            transcript: 'What is the temperature?',
            reply: 'The temperature is 68 degrees.',
            tts_engine: 'kokoro',
            status: null,
          },
        });
      });

      expect(indicator.classList.contains('speaking')).toBe(true);
      expect(label.textContent).toBe('Speaking...');
      expect(audioProgress.style.display).toBe('block');
      expect(transcript.textContent).toBe('"The temperature is 68 degrees."');
      expect(toast.style.display).toBe('flex');
      expect(toast.classList.contains('visible')).toBe(true);
      expect(toastBody.textContent).toBe('The temperature is 68 degrees.');
      expect(toastBadge.textContent).toBe('kokoro');
      expect(toastBadge.dataset.engine).toBe('kokoro');
      expect(toastBadge.style.display).toBe('');
      expect(window.audioManager?.isDucked).toBe(true);

      // 2. ElevenLabs engine (case-insensitive conversion)
      act(() => {
        useDisplayStore.setState({
          voice: {
            state: 'speaking',
            transcript: null,
            reply: 'Hello from ElevenLabs.',
            tts_engine: 'ElevenLabs',
            status: null,
          },
        });
      });
      expect(toastBadge.textContent).toBe('elevenlabs');
      expect(toastBadge.dataset.engine).toBe('elevenlabs');

      // 3. Piper engine
      act(() => {
        useDisplayStore.setState({
          voice: {
            state: 'speaking',
            transcript: null,
            reply: 'Fallback audio.',
            tts_engine: 'piper',
            status: null,
          },
        });
      });
      expect(toastBadge.textContent).toBe('piper');
      expect(toastBadge.dataset.engine).toBe('piper');

      // 4. Null engine hides badge
      act(() => {
        useDisplayStore.setState({
          voice: {
            state: 'speaking',
            transcript: null,
            reply: 'No badge reply.',
            tts_engine: null,
            status: null,
          },
        });
      });
      expect(toastBadge.style.display).toBe('none');

      // 5. Speaking without reply hides toast
      act(() => {
        window.voiceHUD?.resetHUD();
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
      expect(toast.style.display).toBe('none');
    });

    it('handles idle and error transitions by resetting HUD and restoring volume', () => {
      render(<VoiceHud />);
      const headerVoice = document.getElementById('header-voice')!;
      const toast = document.getElementById('voice-toast')!;

      // Active speaking
      act(() => {
        useDisplayStore.setState({
          voice: {
            state: 'speaking',
            transcript: 'Test',
            reply: 'Hello',
            tts_engine: 'kokoro',
            status: null,
          },
        });
      });
      expect(headerVoice.classList.contains('visible')).toBe(true);
      expect(toast.classList.contains('visible')).toBe(true);
      expect(window.audioManager?.isDucked).toBe(true);

      // Transition to idle
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
      expect(headerVoice.classList.contains('visible')).toBe(false);
      expect(toast.classList.contains('visible')).toBe(false);
      expect(toast.style.display).toBe('none');
      expect(window.audioManager?.isDucked).toBe(false);

      // Active listening -> error transition
      act(() => {
        useDisplayStore.setState({
          voice: {
            state: 'listening',
            transcript: null,
            reply: null,
            tts_engine: null,
            status: null,
          },
        });
      });
      expect(window.audioManager?.isDucked).toBe(true);

      act(() => {
        useDisplayStore.setState({
          voice: {
            state: 'error',
            transcript: null,
            reply: null,
            tts_engine: null,
            status: null,
          },
        });
      });
      expect(headerVoice.classList.contains('visible')).toBe(false);
      expect(window.audioManager?.isDucked).toBe(false);
    });

    it('renders live tool status badge during thinking and clears on speaking/idle', () => {
      render(<VoiceHud />);
      const statusEl = document.getElementById('voice-status')!;
      expect(statusEl.style.display).toBe('none');

      // Thinking with tool status
      act(() => {
        useDisplayStore.setState({
          voice: {
            state: 'thinking',
            transcript: 'Lock front door',
            reply: null,
            tts_engine: null,
            status: '⚡ Checking lock state...',
          },
        });
      });

      expect(statusEl.style.display).toBe('');
      expect(statusEl.textContent).toBe('⚡ Checking lock state...');
      expect(window.voiceHUD?.currentStatus).toBe('⚡ Checking lock state...');

      // Subsequent status update
      act(() => {
        useDisplayStore.setState({
          voice: {
            state: 'thinking',
            transcript: 'Lock front door',
            reply: null,
            tts_engine: null,
            status: '🔒 Locking front door...',
          },
        });
      });
      expect(statusEl.textContent).toBe('🔒 Locking front door...');

      // Transition to speaking clears status
      act(() => {
        useDisplayStore.setState({
          voice: {
            state: 'speaking',
            transcript: 'Lock front door',
            reply: 'Front door is locked.',
            tts_engine: 'kokoro',
            status: null,
          },
        });
      });
      expect(statusEl.style.display).toBe('none');
      expect(statusEl.textContent).toBe('');
    });

    it('supports tap-to-dismiss for caption toast without reopening during the same turn', () => {
      render(<VoiceHud />);
      const toast = document.getElementById('voice-toast')!;

      act(() => {
        useDisplayStore.setState({
          voice: {
            state: 'speaking',
            transcript: 'Play song',
            reply: 'Playing song now.',
            tts_engine: 'kokoro',
            status: null,
          },
        });
      });

      expect(toast.classList.contains('visible')).toBe(true);

      // User clicks toast to dismiss
      fireEvent.click(toast);
      expect(window.voiceHUD?.turnDismissed).toBe(true);
      expect(toast.classList.contains('visible')).toBe(false);
      expect(toast.style.display).toBe('none');

      // Same turn speaking update does NOT reopen toast
      act(() => {
        useDisplayStore.setState({
          voice: {
            state: 'speaking',
            transcript: 'Play song',
            reply: 'Playing song now.',
            tts_engine: 'kokoro',
            status: null,
          },
        });
      });
      expect(toast.classList.contains('visible')).toBe(false);

      // Fresh turn in listening state resets turnDismissed
      act(() => {
        useDisplayStore.setState({
          voice: {
            state: 'listening',
            transcript: null,
            reply: null,
            tts_engine: null,
            status: null,
          },
        });
      });
      expect(window.voiceHUD?.turnDismissed).toBe(false);
    });

    it('integrates with header-voice-dismiss-btn via window.MirrormereVoice.resetHUD()', () => {
      render(<VoiceHud />);
      const dismissBtn = document.getElementById('header-voice-dismiss-btn')!;
      const headerVoice = document.getElementById('header-voice')!;

      act(() => {
        useDisplayStore.setState({
          voice: {
            state: 'speaking',
            transcript: 'Stop',
            reply: 'Stopping now.',
            tts_engine: 'kokoro',
            status: null,
          },
        });
      });
      expect(headerVoice.classList.contains('visible')).toBe(true);
      expect(window.audioManager?.isDucked).toBe(true);

      // Click dismiss button
      fireEvent.click(dismissBtn);

      expect(headerVoice.classList.contains('visible')).toBe(false);
      expect(window.audioManager?.isDucked).toBe(false);
      expect(window.voiceHUD?.currentState).toBe('idle');
    });

    it('arms 30s safety watchdog and resets HUD on timeout', () => {
      vi.useFakeTimers();
      render(<VoiceHud />);

      act(() => {
        useDisplayStore.setState({
          voice: {
            state: 'listening',
            transcript: null,
            reply: null,
            tts_engine: null,
            status: null,
          },
        });
      });

      expect(window.audioManager?.isDucked).toBe(true);

      // Advance 29 seconds: still ducked
      act(() => {
        vi.advanceTimersByTime(29000);
      });
      expect(window.audioManager?.isDucked).toBe(true);

      // Advance past 30 seconds: watchdog fires and restores state
      act(() => {
        vi.advanceTimersByTime(1500);
      });
      expect(window.voiceHUD?.currentState).toBe('idle');
      expect(window.audioManager?.isDucked).toBe(false);

      vi.useRealTimers();
    });
  });

  describe('VoiceHUDController standalone methods and SSE integration', () => {
    it('supports attachSSE with both .on and .addEventListener', () => {
      const controller = new VoiceHUDController();

      // Test .on interface
      const onHandlers: Record<string, (d: unknown) => void> = {};
      const mockSSEOn = {
        on(event: string, cb: (data: unknown) => void) {
          onHandlers[event] = cb;
        },
      };
      controller.attachSSE(mockSSEOn);
      expect(typeof onHandlers['voice.state']).toBe('function');
      onHandlers['voice.state']!({ state: 'listening' });
      expect(controller.currentState).toBe('listening');

      // Test .addEventListener interface
      const addHandlers: Record<string, (evt: { data?: unknown }) => void> = {};
      const mockEventSource = {
        addEventListener(event: string, cb: (evt: { data?: unknown }) => void) {
          addHandlers[event] = cb;
        },
      };
      controller.attachSSE(mockEventSource);
      expect(typeof addHandlers['voice.state']).toBe('function');
      addHandlers['voice.state']!({ data: JSON.stringify({ state: 'thinking', transcript: 'Hi' }) });
      expect(controller.currentState).toBe('thinking');

      // Invalid json is safely ignored
      addHandlers['voice.state']!({ data: '{bad json' });
      expect(controller.currentState).toBe('thinking');

      controller.destroy();
    });
  });
});

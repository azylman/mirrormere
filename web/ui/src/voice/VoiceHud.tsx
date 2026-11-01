import { memo, useEffect } from 'react';
import { AudioController } from '../audio/useAudio';
import { Waveform } from './Waveform';
import { useVoiceHudState, useVoiceHudStore } from './useVoiceHudState';

export { DUCK_STATES } from './useVoiceHudState';

/**
 * VoiceHud renders the voice toast dock and Waveform mouth visualizer declaratively,
 * sharing state with HeaderVoice via useVoiceHudState().
 */
export const VoiceHud = memo(function VoiceHud() {
  const { voiceToast, dismissToast } = useVoiceHudState();
  const { visible, badgeText, bodyText } = voiceToast;

  useEffect(() => {
    let createdFallback = false;
    let fallbackController: AudioController | null = null;
    if (typeof window !== 'undefined' && !window.audioManager) {
      fallbackController = new AudioController();
      window.audioManager = fallbackController;
      createdFallback = true;
    }

    useVoiceHudStore.getState().syncDuckState();

    if (typeof window !== 'undefined') {
      window.voiceHUD = {
        get currentState() {
          const state = useVoiceHudStore.getState();
          if (state.isReset) return 'idle';
          return state.activeState;
        },
        get currentStatus() {
          const state = useVoiceHudStore.getState();
          if (state.isReset) return null;
          return state.statusText;
        },
        get turnDismissed() {
          return useVoiceHudStore.getState().turnDismissed;
        },
        resetHUD: () => {
          useVoiceHudStore.getState().resetHUD();
        },
      };

      window.MirrormereVoice = {
        resetHUD: () => {
          useVoiceHudStore.getState().resetHUD();
        },
      };
    }

    return () => {
      useVoiceHudStore.getState().resetHUD();
      if (typeof window !== 'undefined') {
        delete window.voiceHUD;
      }
      if (createdFallback && typeof window !== 'undefined' && window.audioManager === fallbackController) {
        delete window.audioManager;
        fallbackController?.destroy();
      }
    };
  }, []);

  return (
    <>
      <div id="voice-toast-dock" className="voice-toast-dock" aria-live="polite">
        <div
          id="voice-toast"
          className={`voice-toast${visible ? ' visible' : ''}`}
          style={{ display: visible ? 'flex' : 'none' }}
          onClick={dismissToast}
        >
          <span
            id="voice-toast-badge"
            className="voice-toast-badge"
            data-engine={badgeText ?? undefined}
            style={{ display: badgeText ? undefined : 'none' }}
          >
            {badgeText || null}
          </span>
          <span id="voice-toast-body" className="voice-toast-body">
            {bodyText || null}
          </span>
        </div>
      </div>
      <Waveform />
    </>
  );
});

declare global {
  interface Window {
    voiceHUD?: {
      currentState: string;
      currentStatus: string | null;
      turnDismissed: boolean;
      resetHUD: () => void;
    };
    MirrormereVoice?: {
      resetHUD: () => void;
    };
  }
}

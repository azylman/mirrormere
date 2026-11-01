import { useCallback, useReducer } from 'react';
import { AudioController } from '../audio/useAudio';
import type { VoiceState } from '../sse/types';
import { useDisplayStore } from '../store';
import type { VoiceSlice } from '../store/types';
import type { VoiceIconKind } from './VoiceIcon';

export const DUCK_STATES: ReadonlySet<VoiceState> = new Set<VoiceState>([
  'listening',
  'transcribing',
  'thinking',
  'synthesizing',
  'speaking',
]);

export interface HeaderVoiceViewProps {
  isActive: boolean;
  stateClass: string;
  iconKind: VoiceIconKind;
  label: string;
  statusText: string | null;
  transcriptText: string | null;
  showDot: boolean;
  showPulse: boolean;
  showProgress: boolean;
}

export interface VoiceToastViewProps {
  visible: boolean;
  stateClass: string;
  badgeText: string | null;
  bodyText: string | null;
}

export interface VoiceHudSnapshot extends HeaderVoiceViewProps {
  headerVoice: HeaderVoiceViewProps;
  voiceToast: VoiceToastViewProps;
  dismissToast: () => void;
  resetHUD: () => void;
  reset: () => void;
  activeState: VoiceState;
  statusText: string | null;
  turnDismissed: boolean;
}

export function resetHUD(): void {
  useDisplayStore.setState({
    voice: {
      state: 'idle',
      transcript: null,
      reply: null,
      tts_engine: null,
      status: null,
    },
  });
  if (typeof window !== 'undefined' && window.audioManager?.setDucked) {
    window.audioManager.setDucked(false);
  }
}

export const IDLE_HEADER_VOICE: Readonly<HeaderVoiceViewProps> = Object.freeze({
  isActive: false,
  stateClass: '',
  iconKind: 'mic' as VoiceIconKind,
  label: '',
  statusText: null,
  transcriptText: null,
  showDot: false,
  showPulse: false,
  showProgress: false,
});

export const IDLE_VOICE_TOAST: Readonly<VoiceToastViewProps> = Object.freeze({
  visible: false,
  stateClass: '',
  badgeText: null,
  bodyText: null,
});

const IDLE_DERIVED_PROPS = Object.freeze({
  headerVoice: IDLE_HEADER_VOICE as HeaderVoiceViewProps,
  voiceToast: IDLE_VOICE_TOAST as VoiceToastViewProps,
});

export function deriveVoiceHudProps(state: {
  activeState: VoiceState | string;
  transcript?: string | null;
  reply?: string | null;
  ttsEngine?: string | null;
  statusText?: string | null;
  turnDismissed?: boolean;
  isReset?: boolean;
  [key: string]: unknown;
}): {
  headerVoice: HeaderVoiceViewProps;
  voiceToast: VoiceToastViewProps;
} {
  const activeState = (state.activeState || 'idle') as VoiceState;
  const transcript = state.transcript ?? null;
  const reply = state.reply ?? null;
  const ttsEngine = state.ttsEngine ?? null;
  const statusText = state.statusText ?? null;
  const turnDismissed = state.turnDismissed ?? false;
  const isReset = state.isReset ?? false;

  const isActive = !isReset && activeState !== 'idle' && activeState !== 'error';

  if (!isActive) {
    return IDLE_DERIVED_PROPS;
  }

  let stateClass = '';
  let iconKind: VoiceIconKind = 'mic';
  let label = '';
  let showPulse = false;
  let showProgress = false;
  let transcriptText: string | null = null;

  switch (activeState) {
    case 'listening':
      stateClass = 'listening';
      iconKind = 'mic';
      label = 'Listening...';
      showPulse = true;
      break;
    case 'transcribing':
      stateClass = 'transcribing';
      iconKind = 'mic';
      label = 'Transcribing...';
      if (transcript) {
        transcriptText = `"${transcript}"`;
      }
      break;
    case 'thinking':
      stateClass = 'thinking';
      iconKind = 'think';
      label = 'Thinking...';
      if (transcript) {
        transcriptText = `"${transcript}"`;
      }
      break;
    case 'synthesizing':
      stateClass = 'thinking';
      iconKind = 'think';
      label = 'Synthesizing...';
      if (transcript) {
        transcriptText = `"${transcript}"`;
      }
      break;
    case 'speaking':
      stateClass = 'speaking';
      iconKind = 'sparkle';
      label = 'Speaking...';
      showProgress = true;
      if (reply) {
        transcriptText = `"${reply}"`;
      } else if (transcript) {
        transcriptText = `"${transcript}"`;
      }
      break;
  }

  const toastVisible =
    isActive && activeState === 'speaking' && Boolean(reply) && !turnDismissed;
  const toastBadgeText = toastVisible && ttsEngine ? ttsEngine.toLowerCase() : null;
  const toastBodyText = toastVisible && reply ? reply : null;

  const headerVoice: HeaderVoiceViewProps = {
    isActive,
    stateClass,
    iconKind,
    label,
    statusText: isActive ? statusText : null,
    transcriptText,
    showDot: false,
    showPulse,
    showProgress,
  };

  const voiceToast: VoiceToastViewProps = {
    visible: toastVisible,
    stateClass: toastVisible ? 'visible' : '',
    badgeText: toastBadgeText,
    bodyText: toastBodyText,
  };

  return { headerVoice, voiceToast };
}

interface VoiceLocalState {
  turnDismissed: boolean;
  statusText: string | null;
  prevVoice: VoiceSlice | null;
}

type VoiceAction =
  | { type: 'SYNC_VOICE'; voice: VoiceSlice }
  | { type: 'DISMISS_TOAST' }
  | { type: 'RESET' };

function voiceLocalReducer(state: VoiceLocalState, action: VoiceAction): VoiceLocalState {
  switch (action.type) {
    case 'SYNC_VOICE': {
      const { voice } = action;
      const rawState = voice?.state ?? 'idle';
      const normalizedStatus =
        voice?.status && voice.status.trim().length > 0 ? voice.status : null;
      let nextDismissed = state.turnDismissed;
      let nextStatus = state.statusText;

      if (rawState === 'idle' || rawState === 'error') {
        nextDismissed = false;
        nextStatus = null;
      } else if (rawState === 'listening') {
        nextDismissed = false;
        nextStatus = normalizedStatus;
      } else if (rawState === 'thinking' || rawState === 'transcribing') {
        if (normalizedStatus !== null) {
          nextStatus = normalizedStatus;
        }
      } else {
        nextStatus = normalizedStatus;
      }

      return {
        turnDismissed: nextDismissed,
        statusText: nextStatus,
        prevVoice: voice,
      };
    }
    case 'DISMISS_TOAST':
      return {
        ...state,
        turnDismissed: true,
      };
    case 'RESET':
      return {
        turnDismissed: false,
        statusText: null,
        prevVoice: state.prevVoice,
      };
    default:
      return state;
  }
}

export function useVoiceHudState(): VoiceHudSnapshot {
  const voice = useDisplayStore((s) => s.voice);

  const [localState, dispatch] = useReducer(voiceLocalReducer, null, () => {
    const rawState = voice?.state ?? 'idle';
    const normalizedStatus =
      voice?.status && voice.status.trim().length > 0 ? voice.status : null;
    const initialStatus =
      rawState === 'idle' || rawState === 'error' ? null : normalizedStatus;
    return {
      turnDismissed: false,
      statusText: initialStatus,
      prevVoice: voice,
    };
  });

  let effectiveDismissed = localState.turnDismissed;
  let effectiveStatus = localState.statusText;

  if (voice !== localState.prevVoice) {
    const rawState = voice?.state ?? 'idle';
    const normalizedStatus =
      voice?.status && voice.status.trim().length > 0 ? voice.status : null;

    if (rawState === 'idle' || rawState === 'error') {
      effectiveDismissed = false;
      effectiveStatus = null;
    } else if (rawState === 'listening') {
      effectiveDismissed = false;
      effectiveStatus = normalizedStatus;
    } else if (rawState === 'thinking' || rawState === 'transcribing') {
      if (normalizedStatus !== null) {
        effectiveStatus = normalizedStatus;
      }
    } else {
      effectiveStatus = normalizedStatus;
    }
    dispatch({ type: 'SYNC_VOICE', voice });
  }

  const dismissToast = useCallback(() => {
    dispatch({ type: 'DISMISS_TOAST' });
  }, []);

  const reset = useCallback(() => {
    dispatch({ type: 'RESET' });
    resetHUD();
  }, []);

  const activeState = (voice?.state ?? 'idle') as VoiceState;
  const transcript = voice?.transcript ?? null;
  const reply = voice?.reply ?? null;
  const ttsEngine = voice?.tts_engine ?? null;

  const { headerVoice, voiceToast } = deriveVoiceHudProps({
    activeState,
    transcript,
    reply,
    ttsEngine,
    statusText: effectiveStatus,
    turnDismissed: effectiveDismissed,
  });

  return {
    ...headerVoice,
    headerVoice,
    voiceToast,
    dismissToast,
    resetHUD: reset,
    reset,
    activeState,
    statusText: effectiveStatus,
    turnDismissed: effectiveDismissed,
  };
}

declare global {
  interface Window {
    audioManager?: AudioController;
    MirrormereVoice?: {
      resetHUD: () => void;
    };
  }
}

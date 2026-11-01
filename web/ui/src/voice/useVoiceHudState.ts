import { create } from 'zustand';
import { AudioController } from '../audio/useAudio';
import { useDisplayStore } from '../store';
import type { VoiceSlice } from '../store/types';
import type { VoiceIconKind } from './VoiceIcon';

export const DUCK_STATES: ReadonlySet<string> = new Set([
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

export interface VoiceHudStoreState {
  activeState: string;
  transcript: string | null;
  reply: string | null;
  ttsEngine: string | null;
  statusText: string | null;
  turnDismissed: boolean;
  isReset: boolean;
  safetyTimeoutMs: number;
  syncVoice: (voice: VoiceSlice) => void;
  resetHUD: () => void;
  dismissToast: () => void;
  syncDuckState: () => void;
}

function getAudioManager(): { setDucked?: (ducked: boolean) => void } | null {
  if (typeof window !== 'undefined' && window.audioManager) {
    return window.audioManager as unknown as { setDucked?: (ducked: boolean) => void };
  }
  return null;
}

let safetyTimer: ReturnType<typeof setTimeout> | null = null;

function clearSafetyWatchdog(): void {
  if (safetyTimer) {
    clearTimeout(safetyTimer);
    safetyTimer = null;
  }
}

function armSafetyWatchdog(timeoutMs: number, onTimeout: () => void): void {
  clearSafetyWatchdog();
  safetyTimer = setTimeout(() => {
    onTimeout();
  }, timeoutMs);
  const timer = safetyTimer as unknown as { unref?: () => void };
  if (timer && typeof timer.unref === 'function') {
    timer.unref();
  }
}

export const useVoiceHudStore = create<VoiceHudStoreState>()((set, get) => ({
  activeState: 'idle',
  transcript: null,
  reply: null,
  ttsEngine: null,
  statusText: null,
  turnDismissed: false,
  isReset: false,
  safetyTimeoutMs: 30000,

  syncVoice(voice: VoiceSlice) {
    const state = voice?.state ?? 'idle';
    const transcript = voice?.transcript ?? null;
    const reply = voice?.reply ?? null;
    const ttsEngine = voice?.tts_engine ?? null;
    const incomingStatus = voice?.status ?? null;

    if (state === 'idle' || state === 'error') {
      clearSafetyWatchdog();
      getAudioManager()?.setDucked?.(false);
      const cur = get();
      if (
        cur.activeState === state &&
        cur.transcript === null &&
        cur.reply === null &&
        cur.ttsEngine === null &&
        cur.statusText === null &&
        cur.turnDismissed === false &&
        cur.isReset === false
      ) {
        return;
      }
      set({
        activeState: state,
        transcript: null,
        reply: null,
        ttsEngine: null,
        statusText: null,
        turnDismissed: false,
        isReset: false,
      });
      return;
    }

    let nextStatus = get().statusText;
    if (incomingStatus) {
      nextStatus = incomingStatus;
    } else if (state !== 'thinking' && state !== 'transcribing') {
      nextStatus = null;
    }

    let turnDismissed = get().turnDismissed;
    if (state === 'listening') {
      turnDismissed = false;
    }

    const shouldDuck = DUCK_STATES.has(state);
    getAudioManager()?.setDucked?.(shouldDuck);

    if (shouldDuck || nextStatus) {
      armSafetyWatchdog(get().safetyTimeoutMs, () => {
        get().resetHUD();
      });
    } else {
      clearSafetyWatchdog();
    }

    set({
      activeState: state,
      transcript,
      reply,
      ttsEngine,
      statusText: nextStatus,
      turnDismissed,
      isReset: false,
    });
  },

  resetHUD() {
    clearSafetyWatchdog();
    getAudioManager()?.setDucked?.(false);
    const cur = get();
    if (
      cur.activeState === 'idle' &&
      cur.transcript === null &&
      cur.reply === null &&
      cur.ttsEngine === null &&
      cur.statusText === null &&
      cur.turnDismissed === false &&
      cur.isReset === true
    ) {
      return;
    }
    set({
      activeState: 'idle',
      transcript: null,
      reply: null,
      ttsEngine: null,
      statusText: null,
      turnDismissed: false,
      isReset: true,
    });
  },

  dismissToast() {
    if (get().turnDismissed) return;
    set({ turnDismissed: true });
  },

  syncDuckState() {
    const { activeState, isReset } = get();
    const shouldDuck = !isReset && DUCK_STATES.has(activeState);
    getAudioManager()?.setDucked?.(shouldDuck);
  },
}));

// Synchronize with useDisplayStore
let lastVoice: VoiceSlice | undefined = useDisplayStore.getState().voice;
useDisplayStore.subscribe((state) => {
  if (state.voice !== lastVoice) {
    lastVoice = state.voice;
    useVoiceHudStore.getState().syncVoice(state.voice);
  }
});

// Window bindings
if (typeof window !== 'undefined') {
  window.MirrormereVoice = {
    resetHUD: () => {
      useVoiceHudStore.getState().resetHUD();
    },
  };
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

export function deriveVoiceHudProps(state: VoiceHudStoreState): {
  headerVoice: HeaderVoiceViewProps;
  voiceToast: VoiceToastViewProps;
} {
  const { activeState, transcript, reply, ttsEngine, statusText, turnDismissed, isReset } = state;
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

let cachedIdleVoiceHudState: {
  headerVoice: HeaderVoiceViewProps;
  voiceToast: VoiceToastViewProps;
  dismissToast: () => void;
  resetHUD: () => void;
  activeState: string;
  turnDismissed: boolean;
  isActive: boolean;
  stateClass: string;
  iconKind: VoiceIconKind;
  label: string;
  statusText: string | null;
  transcriptText: string | null;
  showDot: boolean;
  showPulse: boolean;
  showProgress: boolean;
} | null = null;

export function useVoiceHudState() {
  const store = useVoiceHudStore();

  const isIdle =
    !store.isReset &&
    store.activeState === 'idle' &&
    !store.turnDismissed &&
    !store.statusText;

  if (isIdle) {
    if (!cachedIdleVoiceHudState) {
      cachedIdleVoiceHudState = {
        ...IDLE_HEADER_VOICE,
        headerVoice: IDLE_HEADER_VOICE as HeaderVoiceViewProps,
        voiceToast: IDLE_VOICE_TOAST as VoiceToastViewProps,
        dismissToast: store.dismissToast,
        resetHUD: store.resetHUD,
        activeState: 'idle',
        turnDismissed: false,
      };
    }
    return cachedIdleVoiceHudState;
  }

  const { headerVoice, voiceToast } = deriveVoiceHudProps(store);

  return {
    ...headerVoice,
    headerVoice,
    voiceToast,
    dismissToast: store.dismissToast,
    resetHUD: store.resetHUD,
    activeState: store.isReset ? 'idle' : store.activeState,
    turnDismissed: store.turnDismissed,
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

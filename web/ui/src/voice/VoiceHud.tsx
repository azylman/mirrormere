import { useEffect, useRef } from 'react';
import { AudioController } from '../audio/useAudio';
import { useVoice } from '../store/hooks';
import { Waveform } from './Waveform';

export const DUCK_STATES: ReadonlySet<string> = new Set([
  'listening',
  'transcribing',
  'thinking',
  'synthesizing',
  'speaking',
]);

export const VOICE_SVG_MIC =
  '<svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="mm-voice-icon-svg"><path d="M12 2a3 3 0 0 0-3 3v7a3 3 0 0 0 6 0V5a3 3 0 0 0-3-3Z"></path><path d="M19 10v2a7 7 0 0 1-14 0v-2"></path><line x1="12" y1="19" x2="12" y2="22"></line></svg>';
export const VOICE_SVG_THINK =
  '<svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="mm-voice-icon-svg"><path d="M7.9 20A9 9 0 1 0 4 16.1L2 22Z"></path></svg>';
export const VOICE_SVG_SPARKLE =
  '<svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="mm-voice-icon-svg"><path d="m12 3-1.912 5.813a2 2 0 0 1-1.275 1.275L3 12l5.813 1.912a2 2 0 0 1 1.275 1.275L12 21l1.912-5.813a2 2 0 0 1 1.275-1.275L21 12l-5.813-1.912a2 2 0 0 1-1.275-1.275L12 3Z"></path></svg>';

export interface SSEClientLike {
  on?: (event: string, cb: (data: unknown) => void) => void;
  addEventListener?: (event: string, cb: (evt: { data?: unknown }) => void) => void;
}

export interface VoiceHUDControllerOptions {
  headerVoiceElement?: HTMLElement | null;
  voiceIndicatorElement?: HTMLElement | null;
  voiceDotElement?: HTMLElement | null;
  voicePulseRingElement?: HTMLElement | null;
  voiceIconElement?: HTMLElement | null;
  voiceLabelElement?: HTMLElement | null;
  voiceStatusElement?: HTMLElement | null;
  voiceTranscriptElement?: HTMLElement | null;
  voiceAudioProgressElement?: HTMLElement | null;
  toastDockElement?: HTMLElement | null;
  toastElement?: HTMLElement | null;
  toastBadgeElement?: HTMLElement | null;
  toastBodyElement?: HTMLElement | null;
  audioManager?: { setDucked?: (ducked: boolean) => void } | null;
  sseClient?: SSEClientLike | null;
  safetyTimeoutMs?: number;
}

export interface VoiceStatePayload {
  state: string;
  transcript?: string | null;
  reply?: string | null;
  tts_engine?: string | null;
  status?: string | null;
}

/**
 * VoiceHUDController coordinates voice UI indicators, caption toasts, and video audio ducking.
 */
export class VoiceHUDController {
  public options: VoiceHUDControllerOptions;
  public audioManager: { setDucked?: (ducked: boolean) => void } | null;
  public sseClient: VoiceHUDControllerOptions['sseClient'];
  public safetyTimeoutMs: number;

  private _headerVoice?: HTMLElement | null;
  private _voiceIndicator?: HTMLElement | null;
  private _voiceDot?: HTMLElement | null;
  private _voicePulseRing?: HTMLElement | null;
  private _voiceIcon?: HTMLElement | null;
  private _voiceLabel?: HTMLElement | null;
  private _voiceStatus?: HTMLElement | null;
  private _voiceTranscript?: HTMLElement | null;
  private _voiceAudioProgress?: HTMLElement | null;
  private _toastDock?: HTMLElement | null;
  private _toast?: HTMLElement | null;
  private _toastBadge?: HTMLElement | null;
  private _toastBody?: HTMLElement | null;

  public currentState: string;
  public currentTranscript: string | null;
  public currentStatus: string | null;
  public currentReply: string | null;
  public currentTTSEngine: string | null;
  public turnDismissed: boolean;
  public safetyTimer: ReturnType<typeof setTimeout> | null;

  constructor(options: VoiceHUDControllerOptions = {}) {
    this.options = options;
    this.audioManager =
      options.audioManager ??
      (typeof window !== 'undefined' ? (window.audioManager as unknown as AudioController) ?? null : null);
    this.sseClient = options.sseClient ?? null;
    this.safetyTimeoutMs = options.safetyTimeoutMs ?? 30000;

    this.currentState = 'idle';
    this.currentTranscript = null;
    this.currentStatus = null;
    this.currentReply = null;
    this.currentTTSEngine = null;
    this.turnDismissed = false;
    this.safetyTimer = null;

    this.handleToastClick = this.handleToastClick.bind(this);
    if (this.toast) {
      this.toast.addEventListener('click', this.handleToastClick);
    }

    if (this.sseClient) {
      this.attachSSE(this.sseClient);
    }
  }

  get headerVoice(): HTMLElement | null {
    if (this._headerVoice !== undefined) return this._headerVoice;
    if (this.options.headerVoiceElement !== undefined) return this.options.headerVoiceElement;
    return typeof document !== 'undefined' ? document.getElementById('header-voice') : null;
  }
  set headerVoice(el: HTMLElement | null) {
    this._headerVoice = el;
  }

  get voiceIndicator(): HTMLElement | null {
    if (this._voiceIndicator !== undefined) return this._voiceIndicator;
    if (this.options.voiceIndicatorElement !== undefined) return this.options.voiceIndicatorElement;
    return typeof document !== 'undefined' ? document.getElementById('voice-indicator') : null;
  }
  set voiceIndicator(el: HTMLElement | null) {
    this._voiceIndicator = el;
  }

  get voiceDot(): HTMLElement | null {
    if (this._voiceDot !== undefined) return this._voiceDot;
    if (this.options.voiceDotElement !== undefined) return this.options.voiceDotElement;
    return typeof document !== 'undefined' ? document.getElementById('voice-dot') : null;
  }
  set voiceDot(el: HTMLElement | null) {
    this._voiceDot = el;
  }

  get voicePulseRing(): HTMLElement | null {
    if (this._voicePulseRing !== undefined) return this._voicePulseRing;
    if (this.options.voicePulseRingElement !== undefined) return this.options.voicePulseRingElement;
    return typeof document !== 'undefined' ? document.getElementById('voice-pulse-ring') : null;
  }
  set voicePulseRing(el: HTMLElement | null) {
    this._voicePulseRing = el;
  }

  get voiceIcon(): HTMLElement | null {
    if (this._voiceIcon !== undefined) return this._voiceIcon;
    if (this.options.voiceIconElement !== undefined) return this.options.voiceIconElement;
    return typeof document !== 'undefined' ? document.getElementById('voice-icon') : null;
  }
  set voiceIcon(el: HTMLElement | null) {
    this._voiceIcon = el;
  }

  get voiceLabel(): HTMLElement | null {
    if (this._voiceLabel !== undefined) return this._voiceLabel;
    if (this.options.voiceLabelElement !== undefined) return this.options.voiceLabelElement;
    return typeof document !== 'undefined' ? document.getElementById('voice-label') : null;
  }
  set voiceLabel(el: HTMLElement | null) {
    this._voiceLabel = el;
  }

  get voiceStatus(): HTMLElement | null {
    if (this._voiceStatus !== undefined) return this._voiceStatus;
    if (this.options.voiceStatusElement !== undefined) return this.options.voiceStatusElement;
    return typeof document !== 'undefined' ? document.getElementById('voice-status') : null;
  }
  set voiceStatus(el: HTMLElement | null) {
    this._voiceStatus = el;
  }

  get voiceTranscript(): HTMLElement | null {
    if (this._voiceTranscript !== undefined) return this._voiceTranscript;
    if (this.options.voiceTranscriptElement !== undefined) return this.options.voiceTranscriptElement;
    return typeof document !== 'undefined' ? document.getElementById('voice-transcript') : null;
  }
  set voiceTranscript(el: HTMLElement | null) {
    this._voiceTranscript = el;
  }

  get voiceAudioProgress(): HTMLElement | null {
    if (this._voiceAudioProgress !== undefined) return this._voiceAudioProgress;
    if (this.options.voiceAudioProgressElement !== undefined) return this.options.voiceAudioProgressElement;
    return typeof document !== 'undefined' ? document.getElementById('voice-audio-progress') : null;
  }
  set voiceAudioProgress(el: HTMLElement | null) {
    this._voiceAudioProgress = el;
  }

  get toastDock(): HTMLElement | null {
    if (this._toastDock !== undefined) return this._toastDock;
    if (this.options.toastDockElement !== undefined) return this.options.toastDockElement;
    return typeof document !== 'undefined' ? document.getElementById('voice-toast-dock') : null;
  }
  set toastDock(el: HTMLElement | null) {
    this._toastDock = el;
  }

  get toast(): HTMLElement | null {
    if (this._toast !== undefined) return this._toast;
    if (this.options.toastElement !== undefined) return this.options.toastElement;
    return typeof document !== 'undefined' ? document.getElementById('voice-toast') : null;
  }
  set toast(el: HTMLElement | null) {
    this._toast = el;
  }

  get toastBadge(): HTMLElement | null {
    if (this._toastBadge !== undefined) return this._toastBadge;
    if (this.options.toastBadgeElement !== undefined) return this.options.toastBadgeElement;
    return typeof document !== 'undefined' ? document.getElementById('voice-toast-badge') : null;
  }
  set toastBadge(el: HTMLElement | null) {
    this._toastBadge = el;
  }

  get toastBody(): HTMLElement | null {
    if (this._toastBody !== undefined) return this._toastBody;
    if (this.options.toastBodyElement !== undefined) return this.options.toastBodyElement;
    return typeof document !== 'undefined' ? document.getElementById('voice-toast-body') : null;
  }
  set toastBody(el: HTMLElement | null) {
    this._toastBody = el;
  }

  attachSSE(sseClient: VoiceHUDControllerOptions['sseClient']): void {
    if (!sseClient) return;

    const handleEvent = (data: unknown) => {
      if (!data) return;
      this.handleVoiceState(data as VoiceStatePayload);
    };

    if (typeof sseClient.on === 'function') {
      sseClient.on('voice.state', handleEvent);
    } else if (typeof sseClient.addEventListener === 'function') {
      sseClient.addEventListener('voice.state', (evt: { data?: unknown }) => {
        try {
          const data = typeof evt.data === 'string' ? JSON.parse(evt.data) : evt.data;
          handleEvent(data);
        } catch {
          // Ignore malformed payload
        }
      });
    }
  }

  handleToastClick(e?: Event): void {
    if (e && typeof e.stopPropagation === 'function') {
      e.stopPropagation();
    }
    this.dismissToast();
  }

  dismissToast(): void {
    this.turnDismissed = true;
    const toast = this.toast;
    if (toast) {
      toast.classList.remove('visible');
      toast.style.display = 'none';
    }
  }

  handleVoiceState(data?: VoiceStatePayload | null): void {
    if (!data || typeof data.state !== 'string') {
      this.resetHUD();
      return;
    }

    const state = data.state;
    const transcript = typeof data.transcript === 'string' ? data.transcript : null;
    const reply = typeof data.reply === 'string' ? data.reply : null;
    const ttsEngine = typeof data.tts_engine === 'string' ? data.tts_engine : null;
    const statusText = typeof data.status === 'string' ? data.status : null;

    if (statusText) {
      this.renderStatus(statusText);
    } else if (state !== 'thinking' && state !== 'transcribing') {
      this.clearStatus();
    }

    if (state === 'listening' || state === 'idle' || state === 'error') {
      this.turnDismissed = false;
    }

    const shouldDuck = DUCK_STATES.has(state);
    const audioMgr =
      this.audioManager ?? (typeof window !== 'undefined' ? window.audioManager ?? null : null);
    if (audioMgr && typeof audioMgr.setDucked === 'function') {
      audioMgr.setDucked(shouldDuck);
    }

    if (shouldDuck) {
      this.armSafetyWatchdog();
    } else {
      this.clearSafetyWatchdog();
    }

    this.currentState = state;

    switch (state) {
      case 'listening':
        this.renderListening();
        break;
      case 'transcribing':
        this.renderTranscribing(transcript);
        break;
      case 'thinking':
        this.renderThinking(transcript);
        break;
      case 'synthesizing':
        this.renderSynthesizing(transcript);
        break;
      case 'speaking':
        this.renderSpeaking(reply, ttsEngine, transcript);
        break;
      case 'idle':
      case 'error':
      default:
        this.resetHUD();
        break;
    }
  }

  renderStatus(statusText: string): void {
    if (statusText && typeof statusText === 'string') {
      this.currentStatus = statusText;
      const statusEl = this.voiceStatus;
      if (statusEl) {
        statusEl.textContent = statusText;
        statusEl.style.display = '';
      }
      this.armSafetyWatchdog();
    } else {
      this.clearStatus();
    }
  }

  clearStatus(): void {
    this.currentStatus = null;
    const statusEl = this.voiceStatus;
    if (statusEl) {
      statusEl.textContent = '';
      statusEl.style.display = 'none';
    }
  }

  renderListening(): void {
    this.clearStatus();
    this.showHeaderVoice(true);

    const indicator = this.voiceIndicator;
    if (indicator) {
      indicator.classList.remove('thinking', 'transcribing', 'speaking');
      indicator.classList.add('listening');
    }

    const voiceIcon = this.voiceIcon;
    if (voiceIcon) voiceIcon.innerHTML = VOICE_SVG_MIC;

    const label = this.voiceLabel;
    if (label) {
      label.textContent = 'Listening...';
    }

    const transcript = this.voiceTranscript;
    if (transcript) {
      transcript.textContent = '';
      transcript.style.display = 'none';
    }

    const toast = this.toast;
    if (toast) {
      toast.classList.remove('visible');
      toast.style.display = 'none';
    }
  }

  renderTranscribing(transcript: string | null): void {
    this.showHeaderVoice(true);

    const indicator = this.voiceIndicator;
    if (indicator) {
      indicator.classList.remove('listening', 'thinking', 'speaking');
      indicator.classList.add('transcribing');
    }

    const voiceIcon = this.voiceIcon;
    if (voiceIcon) voiceIcon.innerHTML = VOICE_SVG_MIC;

    const label = this.voiceLabel;
    if (label) {
      label.textContent = 'Transcribing...';
    }

    const voiceTranscript = this.voiceTranscript;
    if (transcript && voiceTranscript) {
      this.currentTranscript = transcript;
      voiceTranscript.textContent = `"${transcript}"`;
      voiceTranscript.style.display = '';
    }
  }

  renderThinking(transcript: string | null): void {
    this.showHeaderVoice(true);

    const indicator = this.voiceIndicator;
    if (indicator) {
      indicator.classList.remove('listening', 'transcribing', 'speaking');
      indicator.classList.add('thinking');
    }

    const voiceIcon = this.voiceIcon;
    if (voiceIcon) voiceIcon.innerHTML = VOICE_SVG_THINK;

    const label = this.voiceLabel;
    if (label) {
      label.textContent = 'Thinking...';
    }

    if (transcript) {
      this.currentTranscript = transcript;
    }

    const voiceTranscript = this.voiceTranscript;
    if (voiceTranscript) {
      if (this.currentTranscript) {
        voiceTranscript.textContent = `"${this.currentTranscript}"`;
        voiceTranscript.style.display = '';
      } else {
        voiceTranscript.textContent = '';
        voiceTranscript.style.display = 'none';
      }
    }
  }

  renderSynthesizing(transcript: string | null): void {
    this.showHeaderVoice(true);

    const indicator = this.voiceIndicator;
    if (indicator) {
      indicator.classList.remove('listening', 'transcribing', 'speaking');
      indicator.classList.add('thinking');
    }

    const voiceIcon = this.voiceIcon;
    if (voiceIcon) voiceIcon.innerHTML = VOICE_SVG_THINK;

    const label = this.voiceLabel;
    if (label) {
      label.textContent = 'Synthesizing...';
    }

    if (transcript) {
      this.currentTranscript = transcript;
    }

    const voiceTranscript = this.voiceTranscript;
    if (voiceTranscript && this.currentTranscript) {
      voiceTranscript.textContent = `"${this.currentTranscript}"`;
      voiceTranscript.style.display = '';
    }
  }

  renderSpeaking(reply: string | null, ttsEngine: string | null, transcript: string | null): void {
    this.clearStatus();
    this.showHeaderVoice(true);

    const indicator = this.voiceIndicator;
    if (indicator) {
      indicator.classList.remove('listening', 'transcribing', 'thinking');
      indicator.classList.add('speaking');
    }

    const voiceIcon = this.voiceIcon;
    if (voiceIcon) voiceIcon.innerHTML = VOICE_SVG_SPARKLE;

    const audioProgress = this.voiceAudioProgress;
    if (audioProgress) audioProgress.style.display = 'block';

    const label = this.voiceLabel;
    if (label) {
      label.textContent = 'Speaking...';
    }

    if (transcript) {
      this.currentTranscript = transcript;
    }

    if (reply !== null) {
      this.currentReply = reply;
    }
    this.currentTTSEngine = ttsEngine;

    const voiceTranscript = this.voiceTranscript;
    if (voiceTranscript) {
      if (this.currentReply) {
        voiceTranscript.textContent = `"${this.currentReply}"`;
        voiceTranscript.style.display = '';
      } else if (this.currentTranscript) {
        voiceTranscript.textContent = `"${this.currentTranscript}"`;
        voiceTranscript.style.display = '';
      }
    }

    const toast = this.toast;
    if (this.currentReply && !this.turnDismissed && toast) {
      const toastBody = this.toastBody;
      if (toastBody) {
        toastBody.textContent = this.currentReply;
      }

      const toastBadge = this.toastBadge;
      if (toastBadge) {
        if (this.currentTTSEngine) {
          const engine = this.currentTTSEngine.toLowerCase();
          toastBadge.textContent = engine;
          toastBadge.dataset.engine = engine;
          toastBadge.style.display = '';
        } else {
          toastBadge.textContent = '';
          toastBadge.style.display = 'none';
        }
      }

      toast.style.display = 'flex';
      toast.classList.add('visible');

      if (typeof requestAnimationFrame === 'function') {
        requestAnimationFrame(() => {
          if (this.toast && !this.turnDismissed && this.currentState === 'speaking') {
            this.toast.classList.add('visible');
          }
        });
      }
    }
  }

  showHeaderVoice(show: boolean): void {
    const el = this.headerVoice;
    if (!el) return;
    if (show) {
      el.classList.add('visible');
    } else {
      el.classList.remove('visible');
    }
  }

  resetHUD(): void {
    this.clearSafetyWatchdog();
    this.clearStatus();

    this.currentState = 'idle';
    this.currentTranscript = null;
    this.currentReply = null;
    this.currentTTSEngine = null;
    this.turnDismissed = false;

    const audioMgr =
      this.audioManager ?? (typeof window !== 'undefined' ? window.audioManager ?? null : null);
    if (audioMgr && typeof audioMgr.setDucked === 'function') {
      audioMgr.setDucked(false);
    }

    this.showHeaderVoice(false);
    const indicator = this.voiceIndicator;
    if (indicator) {
      indicator.classList.remove('listening', 'transcribing', 'thinking', 'speaking');
    }
    const label = this.voiceLabel;
    if (label) {
      label.textContent = '';
    }
    const voiceIcon = this.voiceIcon;
    if (voiceIcon) voiceIcon.innerHTML = VOICE_SVG_MIC;
    const audioProgress = this.voiceAudioProgress;
    if (audioProgress) audioProgress.style.display = 'none';

    const voiceTranscript = this.voiceTranscript;
    if (voiceTranscript) {
      voiceTranscript.textContent = '';
      voiceTranscript.style.display = 'none';
    }

    const toast = this.toast;
    if (toast) {
      toast.classList.remove('visible');
      toast.style.display = 'none';
    }
    const toastBody = this.toastBody;
    if (toastBody) {
      toastBody.textContent = '';
    }
    const toastBadge = this.toastBadge;
    if (toastBadge) {
      toastBadge.textContent = '';
      toastBadge.style.display = 'none';
    }
  }

  armSafetyWatchdog(): void {
    this.clearSafetyWatchdog();
    this.safetyTimer = setTimeout(() => {
      this.resetHUD();
    }, this.safetyTimeoutMs);
    const timer = this.safetyTimer as unknown as { unref?: () => void };
    if (timer && typeof timer.unref === 'function') {
      timer.unref();
    }
  }

  clearSafetyWatchdog(): void {
    if (this.safetyTimer) {
      clearTimeout(this.safetyTimer);
      this.safetyTimer = null;
    }
  }

  destroy(): void {
    this.resetHUD();
    const toast = this.toast;
    if (toast) {
      toast.removeEventListener('click', this.handleToastClick);
    }
  }
}

/**
 * VoiceHud renders the voice toast dock and Waveform mouth visualizer,
 * subscribing to useVoice() and synchronizing VoiceHUDController.
 */
export function VoiceHud() {
  const voice = useVoice();
  const voiceRef = useRef(voice);
  voiceRef.current = voice;

  const controllerRef = useRef<VoiceHUDController | null>(null);

  if (!controllerRef.current) {
    controllerRef.current = new VoiceHUDController();
  }

  const controller = controllerRef.current;

  useEffect(() => {
    let createdFallback = false;
    let fallbackController: AudioController | null = null;
    if (typeof window !== 'undefined' && !window.audioManager) {
      fallbackController = new AudioController();
      window.audioManager = fallbackController;
      createdFallback = true;
    }

    if (typeof window !== 'undefined') {
      window.voiceHUD = controller;
      window.MirrormereVoice = {
        VoiceHUDController,
        resetHUD: () => controllerRef.current?.resetHUD(),
      };
    }

    const toast = controller.toast;
    if (toast) {
      toast.removeEventListener('click', controller.handleToastClick);
      toast.addEventListener('click', controller.handleToastClick);
    }

    controller.handleVoiceState(voiceRef.current);

    return () => {
      if (toast) {
        toast.removeEventListener('click', controller.handleToastClick);
      }
      controller.destroy();
      if (typeof window !== 'undefined') {
        if (window.voiceHUD === controller) {
          delete window.voiceHUD;
        }
      }
      if (createdFallback && typeof window !== 'undefined' && window.audioManager === fallbackController) {
        delete window.audioManager;
        fallbackController?.destroy();
      }
    };
  }, [controller]);

  useEffect(() => {
    controllerRef.current?.handleVoiceState(voice);
  }, [voice]);

  return (
    <>
      <div id="voice-toast-dock" className="voice-toast-dock" aria-live="polite">
        <div
          id="voice-toast"
          className="voice-toast"
          style={{ display: 'none' }}
          onClick={(e) => controllerRef.current?.handleToastClick(e.nativeEvent)}
        >
          <span id="voice-toast-badge" className="voice-toast-badge" style={{ display: 'none' }} />
          <span id="voice-toast-body" className="voice-toast-body" />
        </div>
      </div>
      <Waveform />
    </>
  );
}

// Window bindings
if (typeof window !== 'undefined') {
  window.MirrormereVoice = {
    VoiceHUDController,
    resetHUD: () => {
      window.voiceHUD?.resetHUD();
    },
  };
}

declare global {
  interface Window {
    voiceHUD?: VoiceHUDController;
    MirrormereVoice?: {
      VoiceHUDController: typeof VoiceHUDController;
      resetHUD: () => void;
    };
  }
}

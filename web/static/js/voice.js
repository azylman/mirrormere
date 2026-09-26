/**
 * Mirrormere Touch Kiosk Voice HUD, Caption Toasts & Dynamic Audio Ducking
 * Manages client-side voice interaction state presentation, listening state visibility invariant,
 * recognized transcript rendering, caption toast dock, and 20% video audio ducking.
 * Complies with SPEC-010 §4–§5 and SPEC-011 §2–§5.
 */
(() => {
  const DUCK_STATES = new Set([
    'listening',
    'transcribing',
    'thinking',
    'synthesizing',
    'speaking',
  ]);

  /**
   * VoiceHUDController coordinates voice UI indicators, caption toasts, and video audio ducking.
   */
  class VoiceHUDController {
    /**
     * @param {Object} [options={}]
     * @param {HTMLElement} [options.headerVoiceElement]
     * @param {HTMLElement} [options.voiceIndicatorElement]
     * @param {HTMLElement} [options.voiceDotElement]
     * @param {HTMLElement} [options.voicePulseRingElement]
     * @param {HTMLElement} [options.voiceLabelElement]
     * @param {HTMLElement} [options.voiceTranscriptElement]
     * @param {HTMLElement} [options.toastDockElement]
     * @param {HTMLElement} [options.toastElement]
     * @param {HTMLElement} [options.toastBadgeElement]
     * @param {HTMLElement} [options.toastBodyElement]
     * @param {Object} [options.audioManager]
     * @param {Object} [options.sseClient]
     * @param {number} [options.safetyTimeoutMs=30000]
     */
    constructor(options = {}) {
      this.options = options;
      this.audioManager = options.audioManager || (typeof window !== 'undefined' ? window.audioManager : null);
      this.sseClient = options.sseClient || null;
      this.safetyTimeoutMs = options.safetyTimeoutMs || 30000;

      // DOM Elements
      this.headerVoice = options.headerVoiceElement || (typeof document !== 'undefined' ? document.getElementById('header-voice') : null);
      this.voiceIndicator = options.voiceIndicatorElement || (typeof document !== 'undefined' ? document.getElementById('voice-indicator') : null);
      this.voiceDot = options.voiceDotElement || (typeof document !== 'undefined' ? document.getElementById('voice-dot') : null);
      this.voicePulseRing = options.voicePulseRingElement || (typeof document !== 'undefined' ? document.getElementById('voice-pulse-ring') : null);
      this.voiceLabel = options.voiceLabelElement || (typeof document !== 'undefined' ? document.getElementById('voice-label') : null);
      this.voiceStatus = options.voiceStatusElement || (typeof document !== 'undefined' ? document.getElementById('voice-status') : null);
      this.voiceTranscript = options.voiceTranscriptElement || (typeof document !== 'undefined' ? document.getElementById('voice-transcript') : null);

      this.toastDock = options.toastDockElement || (typeof document !== 'undefined' ? document.getElementById('voice-toast-dock') : null);
      this.toast = options.toastElement || (typeof document !== 'undefined' ? document.getElementById('voice-toast') : null);
      this.toastBadge = options.toastBadgeElement || (typeof document !== 'undefined' ? document.getElementById('voice-toast-badge') : null);
      this.toastBody = options.toastBodyElement || (typeof document !== 'undefined' ? document.getElementById('voice-toast-body') : null);

      // State
      this.currentState = 'idle';
      this.currentTranscript = null;
      this.currentStatus = null;
      this.currentReply = null;
      this.currentTTSEngine = null;
      this.turnDismissed = false;
      this.safetyTimer = null;

      // Bindings
      this.handleToastClick = this.handleToastClick.bind(this);
      if (this.toast) {
        this.toast.addEventListener('click', this.handleToastClick);
      }

      if (this.sseClient) {
        this.attachSSE(this.sseClient);
      }
    }

    /**
     * Bind to SSE client stream for voice.state events.
     * @param {Object} sseClient
     */
    attachSSE(sseClient) {
      if (!sseClient) return;

      const handleEvent = (data) => {
        if (!data) return;
        this.handleVoiceState(data);
      };

      if (typeof sseClient.on === 'function') {
        sseClient.on('voice.state', handleEvent);
      } else if (typeof sseClient.addEventListener === 'function') {
        sseClient.addEventListener('voice.state', (evt) => {
          try {
            const data = typeof evt.data === 'string' ? JSON.parse(evt.data) : evt.data;
            handleEvent(data);
          } catch (e) {
            // Ignore malformed payload
          }
        });
      }
    }

    /**
     * Handle user tap on caption toast to manually dismiss it for current turn.
     * @param {Event} e
     */
    handleToastClick(e) {
      if (e && typeof e.stopPropagation === 'function') {
        e.stopPropagation();
      }
      this.dismissToast();
    }

    /**
     * Dismiss active caption toast card while maintaining audio ducking and header transcript.
     */
    dismissToast() {
      this.turnDismissed = true;
      if (this.toast) {
        this.toast.classList.remove('visible');
        this.toast.style.display = 'none';
      }
    }

    /**
     * Core voice state transition handler.
     * @param {Object} data
     * @param {string} data.state - One of: 'idle', 'listening', 'transcribing', 'thinking', 'synthesizing', 'speaking', 'error'
     * @param {string|null} [data.transcript]
     * @param {string|null} [data.reply]
     * @param {string|null} [data.tts_engine]
     */
    handleVoiceState(data) {
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

      // Reset turnDismissed flag when entering a fresh interaction turn or closing
      if (state === 'listening' || state === 'idle' || state === 'error') {
        this.turnDismissed = false;
      }

      // Update ducking state idempotently across media elements
      const shouldDuck = DUCK_STATES.has(state);
      if (this.audioManager && typeof this.audioManager.setDucked === 'function') {
        this.audioManager.setDucked(shouldDuck);
      }

      // Manage safety watchdog timer
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

    /**
     * Render live agent tool execution status badge.
     * @param {string} statusText
     */
    renderStatus(statusText) {
      if (statusText && typeof statusText === 'string') {
        this.currentStatus = statusText;
        if (this.voiceStatus) {
          this.voiceStatus.textContent = statusText;
          this.voiceStatus.style.display = '';
        }
        // Live tool status update re-arms the safety watchdog
        this.armSafetyWatchdog();
      } else {
        this.clearStatus();
      }
    }

    /**
     * Clear active tool status badge.
     */
    clearStatus() {
      this.currentStatus = null;
      if (this.voiceStatus) {
        this.voiceStatus.textContent = '';
        this.voiceStatus.style.display = 'none';
      }
    }

    /**
     * Render listening state.
     * SPEC-011 §5: Listening State Visibility Invariant requires active pulsing cyan/violet radar ring.
     */
    renderListening() {
      this.clearStatus();
      this.showHeaderVoice(true);

      if (this.voiceIndicator) {
        this.voiceIndicator.classList.remove('thinking', 'transcribing', 'speaking');
        this.voiceIndicator.classList.add('listening');
      }

      if (this.voiceLabel) {
        this.voiceLabel.textContent = 'Listening...';
      }

      // Clear transcript while waiting for speech
      if (this.voiceTranscript) {
        this.voiceTranscript.textContent = '';
        this.voiceTranscript.style.display = 'none';
      }

      // Hide toast during input capture
      if (this.toast) {
        this.toast.classList.remove('visible');
        this.toast.style.display = 'none';
      }
    }

    /**
     * Render transcribing state (STT processing).
     * @param {string|null} transcript
     */
    renderTranscribing(transcript) {
      this.showHeaderVoice(true);

      if (this.voiceIndicator) {
        // Stop pulse ring immediately: audio capture is halted per SPEC-011
        this.voiceIndicator.classList.remove('listening', 'thinking', 'speaking');
        this.voiceIndicator.classList.add('transcribing');
      }

      if (this.voiceLabel) {
        this.voiceLabel.textContent = 'Transcribing...';
      }

      if (transcript && this.voiceTranscript) {
        this.currentTranscript = transcript;
        this.voiceTranscript.textContent = `"${transcript}"`;
        this.voiceTranscript.style.display = '';
      }
    }

    /**
     * Render thinking state (agent brain deliberation).
     * @param {string|null} transcript
     */
    renderThinking(transcript) {
      this.showHeaderVoice(true);

      if (this.voiceIndicator) {
        this.voiceIndicator.classList.remove('listening', 'transcribing', 'speaking');
        this.voiceIndicator.classList.add('thinking');
      }

      if (this.voiceLabel) {
        this.voiceLabel.textContent = 'Thinking...';
      }

      if (transcript) {
        this.currentTranscript = transcript;
      }

      if (this.voiceTranscript) {
        if (this.currentTranscript) {
          this.voiceTranscript.textContent = `"${this.currentTranscript}"`;
          this.voiceTranscript.style.display = '';
        } else {
          this.voiceTranscript.textContent = '';
          this.voiceTranscript.style.display = 'none';
        }
      }
    }

    /**
     * Render synthesizing state (TTS synthesis before audio starts).
     * @param {string|null} transcript
     */
    renderSynthesizing(transcript) {
      this.showHeaderVoice(true);

      if (this.voiceIndicator) {
        this.voiceIndicator.classList.remove('listening', 'transcribing', 'speaking');
        this.voiceIndicator.classList.add('thinking');
      }

      if (this.voiceLabel) {
        this.voiceLabel.textContent = 'Synthesizing...';
      }

      if (transcript) {
        this.currentTranscript = transcript;
      }

      if (this.voiceTranscript && this.currentTranscript) {
        this.voiceTranscript.textContent = `"${this.currentTranscript}"`;
        this.voiceTranscript.style.display = '';
      }
    }

    /**
     * Render speaking state with caption toasts.
     * @param {string|null} reply
     * @param {string|null} ttsEngine
     * @param {string|null} transcript
     */
    renderSpeaking(reply, ttsEngine, transcript) {
      this.clearStatus();
      this.showHeaderVoice(true);

      if (this.voiceIndicator) {
        this.voiceIndicator.classList.remove('listening', 'transcribing', 'thinking');
        this.voiceIndicator.classList.add('speaking');
      }

      if (this.voiceLabel) {
        this.voiceLabel.textContent = 'Speaking...';
      }

      if (transcript) {
        this.currentTranscript = transcript;
      }

      if (this.voiceTranscript && this.currentTranscript) {
        this.voiceTranscript.textContent = `"${this.currentTranscript}"`;
        this.voiceTranscript.style.display = '';
      }

      // Present caption toast if reply text is available and user hasn't dismissed this turn
      if (reply !== null) {
        this.currentReply = reply;
      }
      this.currentTTSEngine = ttsEngine;

      if (this.currentReply && !this.turnDismissed && this.toast) {
        if (this.toastBody) {
          this.toastBody.textContent = this.currentReply;
        }

        if (this.toastBadge) {
          if (this.currentTTSEngine) {
            const engine = this.currentTTSEngine.toLowerCase();
            this.toastBadge.textContent = engine;
            this.toastBadge.dataset.engine = engine;
            this.toastBadge.style.display = '';
          } else {
            this.toastBadge.textContent = '';
            this.toastBadge.style.display = 'none';
          }
        }

        this.toast.style.display = 'flex';
        // Trigger reflow/animation frame for CSS transition
        if (typeof requestAnimationFrame === 'function') {
          requestAnimationFrame(() => {
            if (this.toast && !this.turnDismissed) {
              this.toast.classList.add('visible');
            }
          });
        } else {
          this.toast.classList.add('visible');
        }
      }
    }

    /**
     * Helper to show or hide the header voice container cleanly without CLS.
     * @param {boolean} show
     */
    showHeaderVoice(show) {
      if (!this.headerVoice) return;
      if (show) {
        this.headerVoice.classList.add('visible');
      } else {
        this.headerVoice.classList.remove('visible');
      }
    }

    /**
     * Resets all Voice HUD presentation and restores video audio volume to 100%.
     */
    resetHUD() {
      this.clearSafetyWatchdog();
      this.clearStatus();

      this.currentState = 'idle';
      this.currentTranscript = null;
      this.currentReply = null;
      this.currentTTSEngine = null;
      this.turnDismissed = false;

      // Restore video audio volume
      if (this.audioManager && typeof this.audioManager.setDucked === 'function') {
        this.audioManager.setDucked(false);
      }

      // Hide header voice UI
      this.showHeaderVoice(false);
      if (this.voiceIndicator) {
        this.voiceIndicator.classList.remove('listening', 'transcribing', 'thinking', 'speaking');
      }
      if (this.voiceLabel) {
        this.voiceLabel.textContent = '';
      }
      if (this.voiceTranscript) {
        this.voiceTranscript.textContent = '';
        this.voiceTranscript.style.display = 'none';
      }

      // Dismiss caption toast
      if (this.toast) {
        this.toast.classList.remove('visible');
        this.toast.style.display = 'none';
      }
      if (this.toastBody) {
        this.toastBody.textContent = '';
      }
      if (this.toastBadge) {
        this.toastBadge.textContent = '';
        this.toastBadge.style.display = 'none';
      }
    }

    /**
     * Arms a 30s safety watchdog timer to restore volume if connection drops mid-turn.
     */
    armSafetyWatchdog() {
      this.clearSafetyWatchdog();
      this.safetyTimer = setTimeout(() => {
        this.resetHUD();
      }, this.safetyTimeoutMs);
      if (this.safetyTimer && typeof this.safetyTimer.unref === 'function') {
        this.safetyTimer.unref();
      }
    }

    /**
     * Clears active safety watchdog timer.
     */
    clearSafetyWatchdog() {
      if (this.safetyTimer) {
        clearTimeout(this.safetyTimer);
        this.safetyTimer = null;
      }
    }

    /**
     * Destroy controller and clean up all listeners and timers.
     */
    destroy() {
      this.resetHUD();
      if (this.toast) {
        this.toast.removeEventListener('click', this.handleToastClick);
      }
    }
  }

  // Global and CommonJS export bindings
  if (typeof window !== 'undefined') {
    window.MirrormereVoice = {
      VoiceHUDController,
    };
  }

  if (typeof module !== 'undefined' && module.exports) {
    module.exports = {
      VoiceHUDController,
    };
  }
})();

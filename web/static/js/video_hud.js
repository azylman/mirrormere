/**
 * Mirrormere Touch HUD Video Overlay & Debounced Audio Controller
 * Manages the semi-transparent Cyber HUD video overlay, 5-second auto-fade lifecycle,
 * tap-to-wake gestures, transport controls (play/pause, dismiss), and debounced touch
 * volume slider network mutations.
 * Complies with SPEC-004 §5, SPEC-010 §1, and SPEC-010 §4.
 */
(() => {
  /**
   * VideoHUDController coordinates the HUD overlay lifecycle, transport actions,
   * and audio controls over live video streams.
   */
  class VideoHUDController {
    /**
     * @param {Object} [options={}]
     * @param {HTMLElement} [options.overlayElement] - The #video-hud-overlay element.
     * @param {HTMLElement} [options.titleElement] - The #video-hud-title element.
     * @param {HTMLElement} [options.dismissBtn] - The #video-hud-dismiss-btn element.
     * @param {HTMLElement} [options.playBtn] - The #video-hud-play-btn element.
     * @param {HTMLElement} [options.playIcon] - The #video-hud-play-icon element.
     * @param {HTMLElement} [options.muteBtn] - The #video-hud-mute-btn element.
     * @param {HTMLElement} [options.muteIcon] - The #video-hud-mute-icon element.
     * @param {HTMLInputElement} [options.volumeSlider] - The #video-hud-volume-slider element.
     * @param {HTMLElement} [options.stageElement] - The #video-stage root container.
     * @param {Object} [options.videoManager] - VideoPlayerManager instance.
     * @param {Function} [options.fetch] - Custom fetch function for tests.
     * @param {number} [options.autoFadeTimeout=5000] - Auto-fade timeout in ms.
     * @param {number} [options.throttleInterval=200] - Volume network throttle interval in ms.
     * @param {number} [options.commitCooldown=350] - Post-commit SSE ignore window in ms.
     */
    constructor(options = {}) {
      this.options = options;
      this.overlay = options.overlayElement || (typeof document !== 'undefined' ? document.getElementById('video-hud-overlay') : null);
      this.titleEl = options.titleElement || (typeof document !== 'undefined' ? document.getElementById('video-hud-title') : null);
      this.dismissBtn = options.dismissBtn || (typeof document !== 'undefined' ? document.getElementById('video-hud-dismiss-btn') : null);
      this.playBtn = options.playBtn || (typeof document !== 'undefined' ? document.getElementById('video-hud-play-btn') : null);
      this.playIcon = options.playIcon || (typeof document !== 'undefined' ? document.getElementById('video-hud-play-icon') : null);
      this.muteBtn = options.muteBtn || (typeof document !== 'undefined' ? document.getElementById('video-hud-mute-btn') : null);
      this.muteIcon = options.muteIcon || (typeof document !== 'undefined' ? document.getElementById('video-hud-mute-icon') : null);
      this.volumeSlider = options.volumeSlider || (typeof document !== 'undefined' ? document.getElementById('video-hud-volume-slider') : null);
      this.stageElement = options.stageElement || (typeof document !== 'undefined' ? document.getElementById('video-stage') : null);
      this.videoManager = options.videoManager || null;
      this.fetchFn = options.fetch || (typeof fetch !== 'undefined' ? fetch : null);

      this.autoFadeTimeout = options.autoFadeTimeout !== undefined ? options.autoFadeTimeout : 5000;
      this.throttleInterval = options.throttleInterval !== undefined ? options.throttleInterval : 200;
      this.commitCooldown = options.commitCooldown !== undefined ? options.commitCooldown : 350;

      // Runtime state
      this.isVisible = false;
      this.autoFadeTimer = null;
      this.activeStream = null;
      this.lastVideoState = null;
      this.currentMuted = false;

      // In-flight action locks
      this.isActionPending = false;
      this.isMutePending = false;

      // Volume slider debounce state
      this.isDragging = false;
      this.throttleTimer = null;
      this.pendingVolume = null;
      this.lastNetworkVolume = null;
      this.commitCooldownUntil = 0;

      this.boundHandlers = [];

      this.bindControls();
    }

    /**
     * Bind DOM event listeners.
     */
    bindControls() {
      // 1. Stage tap-to-wake / tap-to-toggle
      if (this.stageElement) {
        const onStageClick = (e) => {
          // Identify which slot is currently rendered as the corner PiP dock
          const isSwapped = (this.videoManager && this.videoManager.isSwapped) ||
            (this.stageElement && this.stageElement.dataset && this.stageElement.dataset.swapped === 'true');
          const cornerDockSelector = isSwapped ? '.video-primary-slot' : '.video-pip-slot';

          // If click was inside the active corner PiP dock, let PiP handler process the swap
          if (e.target && e.target.closest && e.target.closest(cornerDockSelector)) {
            return;
          }
          // If click was on interactive HUD elements, let them handle it
          if (e.target && e.target.closest && (e.target.closest('.video-hud-controls') || e.target.closest('.video-hud-header'))) {
            return;
          }
          this.toggleHUD();
        };
        this.stageElement.addEventListener('click', onStageClick);
        this.boundHandlers.push({ el: this.stageElement, ev: 'click', fn: onStageClick });
      }

      // 2. Dismiss button
      if (this.dismissBtn) {
        const onDismiss = (e) => {
          if (e && typeof e.stopPropagation === 'function') e.stopPropagation();
          this.resetAutoFade();
          this.handleDismiss();
        };
        this.dismissBtn.addEventListener('click', onDismiss);
        this.boundHandlers.push({ el: this.dismissBtn, ev: 'click', fn: onDismiss });
      }

      // 3. Play/Pause button
      if (this.playBtn) {
        const onPlayToggle = (e) => {
          if (e && typeof e.stopPropagation === 'function') e.stopPropagation();
          this.resetAutoFade();
          this.handlePlayPauseToggle();
        };
        this.playBtn.addEventListener('click', onPlayToggle);
        this.boundHandlers.push({ el: this.playBtn, ev: 'click', fn: onPlayToggle });
      }

      // 4. Mute button
      if (this.muteBtn) {
        const onMuteToggle = (e) => {
          if (e && typeof e.stopPropagation === 'function') e.stopPropagation();
          this.resetAutoFade();
          this.handleMuteToggle();
        };
        this.muteBtn.addEventListener('click', onMuteToggle);
        this.boundHandlers.push({ el: this.muteBtn, ev: 'click', fn: onMuteToggle });
      }

      // 5. Volume slider interactions
      if (this.volumeSlider) {
        const onSliderInput = (e) => {
          if (e && typeof e.stopPropagation === 'function') e.stopPropagation();
          this.handleSliderInput();
        };
        this.volumeSlider.addEventListener('input', onSliderInput);
        this.boundHandlers.push({ el: this.volumeSlider, ev: 'input', fn: onSliderInput });

        const onSliderCommit = (e) => {
          if (e && typeof e.stopPropagation === 'function') e.stopPropagation();
          this.handleSliderCommit();
        };

        const commitEvents = ['pointerup', 'touchend', 'pointercancel', 'touchcancel', 'change'];
        commitEvents.forEach((ev) => {
          this.volumeSlider.addEventListener(ev, onSliderCommit);
          this.boundHandlers.push({ el: this.volumeSlider, ev: ev, fn: onSliderCommit });
        });
      }
    }

    /**
     * Reveal HUD overlay and start auto-fade countdown.
     */
    showHUD() {
      this.isVisible = true;
      if (this.overlay) {
        this.overlay.classList.add('visible');
      }
      this.resetAutoFade();
    }

    /**
     * Hide HUD overlay and cancel countdown timer.
     */
    hideHUD() {
      this.isVisible = false;
      if (this.autoFadeTimer) {
        clearTimeout(this.autoFadeTimer);
        this.autoFadeTimer = null;
      }
      if (this.overlay) {
        this.overlay.classList.remove('visible');
      }
    }

    /**
     * Toggle HUD visibility state.
     */
    toggleHUD() {
      if (this.isVisible) {
        this.hideHUD();
      } else {
        this.showHUD();
      }
    }

    /**
     * Reset the 5-second auto-fade inactivity timer.
     */
    resetAutoFade() {
      if (!this.isVisible) return;

      if (this.autoFadeTimer) {
        clearTimeout(this.autoFadeTimer);
      }

      this.autoFadeTimer = setTimeout(() => {
        this.hideHUD();
      }, this.autoFadeTimeout);

      if (this.autoFadeTimer && typeof this.autoFadeTimer.unref === 'function') {
        this.autoFadeTimer.unref();
      }
    }

    /**
     * Determine active presentation stream dynamically respecting swap state.
     */
    getActiveStream() {
      if (this.videoManager && this.videoManager.isSwapped) {
        return this.videoManager.serverPip || this.lastVideoState?.pip || null;
      }
      return this.videoManager?.serverPrimary || this.lastVideoState?.primary || null;
    }

    /**
     * Synchronize stream presentation, title, transport controls, and visibility.
     */
    syncStream() {
      const stream = this.getActiveStream();
      this.activeStream = stream;

      if (!stream) {
        this.hideHUD();
        return;
      }

      // Update stream title
      if (this.titleEl) {
        this.titleEl.textContent = stream.title || (stream.id ? `Stream: ${stream.id}` : 'Live Video');
      }

      // Update Play/Pause visibility based on controllable gating
      if (this.playBtn) {
        if (stream.controllable === true) {
          this.playBtn.style.display = '';
          this.playBtn.removeAttribute('hidden');
        } else {
          this.playBtn.style.display = 'none';
          this.playBtn.setAttribute('hidden', 'true');
        }
      }

      // Update Play/Pause icon based on player_state
      if (this.playIcon && this.playBtn) {
        if (stream.player_state === 'paused') {
          this.playIcon.textContent = '▶';
          this.playBtn.setAttribute('aria-label', 'Play');
          this.playBtn.title = 'Play';
        } else {
          this.playIcon.textContent = '⏸';
          this.playBtn.setAttribute('aria-label', 'Pause');
          this.playBtn.title = 'Pause';
        }
      }
    }

    /**
     * Process incoming video.state SSE event.
     * @param {Object} data
     */
    handleVideoState(data) {
      this.lastVideoState = data;
      this.syncStream();
    }

    /**
     * Process incoming audio.state SSE event.
     * @param {Object} data - { volume: number, muted: boolean }
     */
    handleAudioState(data) {
      if (!data) return;

      this.currentMuted = Boolean(data.muted);

      // Update Mute button icon and styling
      if (this.muteIcon) {
        this.muteIcon.textContent = this.currentMuted ? '🔇' : '🔊';
      }
      if (this.muteBtn) {
        this.muteBtn.setAttribute('aria-label', this.currentMuted ? 'Unmute' : 'Mute');
        this.muteBtn.title = this.currentMuted ? 'Unmute' : 'Mute';
        if (this.currentMuted) {
          this.muteBtn.classList.add('muted');
        } else {
          this.muteBtn.classList.remove('muted');
        }
      }

      // Update slider visual muted state
      if (this.volumeSlider) {
        if (this.currentMuted) {
          this.volumeSlider.classList.add('muted');
        } else {
          this.volumeSlider.classList.remove('muted');
        }

        // Active drag isolation & post-commit cooldown
        const now = Date.now();
        if (!this.isDragging && now >= this.commitCooldownUntil && data.volume !== undefined) {
          this.volumeSlider.value = String(data.volume);
        }
      }
    }

    /**
     * Dispatch play/pause toggle action with in-flight lock.
     */
    handlePlayPauseToggle() {
      if (this.isActionPending || !this.activeStream || !this.activeStream.id) {
        return;
      }

      this.isActionPending = true;
      setTimeout(() => {
        this.isActionPending = false;
      }, 300);

      this.postAction({
        id: this.activeStream.id,
        action: 'toggle_playback',
      });
    }

    /**
     * Dispatch video stream dismiss action with in-flight lock.
     * Unmounts video mode immediately and returns to widgets mode per SPEC-004 §2.
     */
    handleDismiss() {
      if (this.isActionPending) {
        return;
      }

      const primaryId = this.videoManager?.serverPrimary?.id || this.lastVideoState?.primary?.id;
      const pipId = this.videoManager?.serverPip?.id || this.lastVideoState?.pip?.id;

      if (!primaryId && !pipId && (!this.activeStream || !this.activeStream.id)) {
        return;
      }

      this.isActionPending = true;
      setTimeout(() => {
        this.isActionPending = false;
      }, 300);

      if (primaryId && pipId && primaryId !== pipId) {
        // When multiple streams are present (primary and PiP), dismiss all streams
        // to guarantee unmounting video mode and returning to widgets mode per SPEC-004 §2.
        this.postDismiss({ id: 'all' });
      } else {
        const targetId = primaryId || pipId || this.activeStream?.id;
        this.postDismiss({ id: targetId });
      }
    }

    /**
     * Dispatch mute toggle action with in-flight lock.
     */
    handleMuteToggle() {
      if (this.isMutePending) {
        return;
      }

      this.isMutePending = true;
      setTimeout(() => {
        this.isMutePending = false;
      }, 300);

      const targetMuted = !this.currentMuted;
      this.postMute({
        muted: targetMuted,
      });
    }

    /**
     * Handle continuous volume slider dragging (60fps visual tracking, 200ms network throttle).
     */
    handleSliderInput() {
      if (!this.volumeSlider) return;

      this.isDragging = true;
      this.resetAutoFade();

      const val = parseInt(this.volumeSlider.value, 10);
      if (Number.isNaN(val)) return;

      this.pendingVolume = val;

      if (!this.throttleTimer) {
        // Immediate network dispatch for first gesture event
        this.postVolume({ volume: val });
        this.lastNetworkVolume = val;

        this.throttleTimer = setTimeout(() => {
          this.throttleTimer = null;
          if (this.pendingVolume !== null && this.pendingVolume !== this.lastNetworkVolume) {
            this.postVolume({ volume: this.pendingVolume });
            this.lastNetworkVolume = this.pendingVolume;
          }
        }, this.throttleInterval);

        if (this.throttleTimer && typeof this.throttleTimer.unref === 'function') {
          this.throttleTimer.unref();
        }
      }
    }

    /**
     * Commit final volume on touch/pointer release.
     */
    handleSliderCommit() {
      if (!this.volumeSlider) return;

      this.isDragging = false;
      this.resetAutoFade();

      if (this.throttleTimer) {
        clearTimeout(this.throttleTimer);
        this.throttleTimer = null;
      }

      const finalVal = parseInt(this.volumeSlider.value, 10);
      if (!Number.isNaN(finalVal) && finalVal !== this.lastNetworkVolume) {
        this.postVolume({ volume: finalVal });
        this.lastNetworkVolume = finalVal;
      }

      this.pendingVolume = null;
      this.commitCooldownUntil = Date.now() + this.commitCooldown;
    }

    /**
     * Network POST helpers.
     */
    postAction(body) {
      if (!this.fetchFn) return Promise.resolve();
      return this.fetchFn('api/video/action', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      }).catch((err) => {
        console.warn('[MirrormereHUD] Action failed:', err);
      });
    }

    postDismiss(body) {
      if (!this.fetchFn) return Promise.resolve();
      return this.fetchFn('api/video/dismiss', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      }).catch((err) => {
        console.warn('[MirrormereHUD] Dismiss failed:', err);
      });
    }

    postMute(body) {
      if (!this.fetchFn) return Promise.resolve();
      return this.fetchFn('api/audio/mute', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      }).catch((err) => {
        console.warn('[MirrormereHUD] Mute toggle failed:', err);
      });
    }

    postVolume(body) {
      if (!this.fetchFn) return Promise.resolve();
      return this.fetchFn('api/audio/volume', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      }).catch((err) => {
        console.warn('[MirrormereHUD] Volume update failed:', err);
      });
    }

    /**
     * Teardown all timers and listeners.
     */
    destroy() {
      this.hideHUD();

      if (this.throttleTimer) {
        clearTimeout(this.throttleTimer);
        this.throttleTimer = null;
      }

      for (const { el, ev, fn } of this.boundHandlers) {
        if (el && typeof el.removeEventListener === 'function') {
          el.removeEventListener(ev, fn);
        }
      }
      this.boundHandlers = [];
    }
  }

  const api = {
    VideoHUDController,
  };

  if (typeof window !== 'undefined') {
    window.MirrormereHUD = api;
  }
  if (typeof module !== 'undefined' && module.exports) {
    module.exports = api;
  }
})();

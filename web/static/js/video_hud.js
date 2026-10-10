/**
 * Mirrormere Touch HUD Video Overlay & Debounced Audio Controller
 * Manages the semi-transparent Cyber HUD video overlay, 5-second auto-fade lifecycle,
 * tap-to-wake gestures, transport controls (play/pause, dismiss), and debounced touch
 * volume slider network mutations.
 */
(() => {
  const HUD_ICONS = {
    play: '<svg viewBox="0 0 24 24" width="22" height="22" fill="currentColor" aria-hidden="true"><polygon points="6 4 20 12 6 20 6 4"></polygon></svg>',
    pause: '<svg viewBox="0 0 24 24" width="22" height="22" fill="currentColor" aria-hidden="true"><rect x="6" y="4" width="4" height="16" rx="1"></rect><rect x="14" y="4" width="4" height="16" rx="1"></rect></svg>',
    mute: '<svg viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><polygon points="11 5 6 9 2 9 2 15 6 15 11 19 11 5" fill="currentColor"></polygon><line x1="23" y1="9" x2="17" y2="15"></line><line x1="17" y1="9" x2="23" y2="15"></line></svg>',
    unmute: '<svg viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><polygon points="11 5 6 9 2 9 2 15 6 15 11 19 11 5" fill="currentColor"></polygon><path d="M19.07 4.93a10 10 0 0 1 0 14.14M15.54 8.46a5 5 0 0 1 0 7.07"></path></svg>',
  };

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
     * @param {HTMLElement} [options.rewindBtn] - The #video-hud-rewind-btn element.
     * @param {HTMLElement} [options.forwardBtn] - The #video-hud-forward-btn element.
     * @param {HTMLElement} [options.bottomBarElement] - The #video-hud-bottom-bar element.
     * @param {HTMLElement} [options.muteBtn] - The #video-hud-mute-btn element.
     * @param {HTMLElement} [options.muteIcon] - The #video-hud-mute-icon element.
     * @param {HTMLInputElement} [options.volumeSlider] - The #video-hud-volume-slider element.
     * @param {HTMLElement} [options.stageElement] - The #video-stage root container.
     * @param {Object} [options.videoManager] - VideoPlayerManager instance.
     * @param {string} [options.remoteUrl] - Remote sidecar base URL.
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
      this.bottomBar = options.bottomBarElement ||
        (this.overlay?.children?.find ? this.overlay.children.find((c) => c.id === 'video-hud-bottom-bar') : null) ||
        (typeof document !== 'undefined' ? document.getElementById('video-hud-bottom-bar') : null);
      this.rewindBtn = options.rewindBtn ||
        (this.bottomBar?.children?.find ? this.bottomBar.children.find((c) => c.id === 'video-hud-rewind-btn') : null) ||
        (typeof document !== 'undefined' ? document.getElementById('video-hud-rewind-btn') : null);
      this.forwardBtn = options.forwardBtn ||
        (this.bottomBar?.children?.find ? this.bottomBar.children.find((c) => c.id === 'video-hud-forward-btn') : null) ||
        (typeof document !== 'undefined' ? document.getElementById('video-hud-forward-btn') : null);
      this.playBtn = options.playBtn || (typeof document !== 'undefined' ? document.getElementById('video-hud-play-btn') : null);
      this.playIcon = options.playIcon || (typeof document !== 'undefined' ? document.getElementById('video-hud-play-icon') : null);
      this.muteBtn = options.muteBtn || (typeof document !== 'undefined' ? document.getElementById('video-hud-mute-btn') : null);
      this.muteIcon = options.muteIcon || (typeof document !== 'undefined' ? document.getElementById('video-hud-mute-icon') : null);
      this.volumeSlider = options.volumeSlider || (typeof document !== 'undefined' ? document.getElementById('video-hud-volume-slider') : null);
      this.skipBackBtn = options.skipBackBtn || (typeof document !== 'undefined' ? document.getElementById('video-hud-skip-back-btn') : null);
      this.skipFwdBtn = options.skipFwdBtn || (typeof document !== 'undefined' ? document.getElementById('video-hud-skip-fwd-btn') : null);
      this.stageElement = options.stageElement || (typeof document !== 'undefined' ? document.getElementById('video-stage') : null);
      this.videoManager = options.videoManager || null;
      this._remoteUrl = options.remoteUrl || '';

      const defaultFetch = (typeof window !== 'undefined' && typeof window.fetch === 'function')
        ? window.fetch.bind(window)
        : (typeof fetch === 'function' ? fetch : null);
      this.fetchFn = options.fetch || defaultFetch;

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

      // Gesture and double-tap state
      this.lastTapTime = 0;
      this.lastTapCoords = null;
      this.singleTapTimer = null;
      this.doubleTapCooldownUntil = 0;
      this.lastPointerUpTime = 0;

      // Volume slider debounce state
      this.isDragging = false;
      this.throttleTimer = null;
      this.pendingVolume = null;
      this.lastNetworkVolume = null;
      this.commitCooldownUntil = 0;

      this.boundHandlers = [];

      this.bindControls();
    }

    get remoteUrl() {
      return this._remoteUrl ||
        this.videoManager?.remoteUrl ||
        (typeof document !== 'undefined' && document.body && document.body.dataset && document.body.dataset.remoteUrl) ||
        (typeof window !== 'undefined' && window.MIRRORMERE_REMOTE_URL) ||
        '';
    }

    set remoteUrl(val) {
      this._remoteUrl = val;
    }

    /**
     * Bind DOM event listeners.
     */
    bindControls() {
      // 1. Stage tap-to-wake / tap-to-toggle & double-tap play/pause
      if (this.stageElement) {
        let startX = null;
        let startY = null;

        const getCoords = (e) => {
          if (e && e.changedTouches && e.changedTouches.length > 0) {
            return { x: e.changedTouches[0].clientX, y: e.changedTouches[0].clientY };
          }
          if (e && typeof e.clientX === 'number' && typeof e.clientY === 'number') {
            return { x: e.clientX, y: e.clientY };
          }
          return null;
        };

        const onStagePointerDown = (e) => {
          const coords = getCoords(e);
          if (coords) {
            startX = coords.x;
            startY = coords.y;
          } else {
            startX = null;
            startY = null;
          }
        };

        const onStageToggle = (e) => {
          const now = Date.now();

          // Filter out synthetic duplicate click events using 350ms suppression
          if (e.type === 'click') {
            if (now - this.lastPointerUpTime < 350) {
              return;
            }
          } else if (e.type === 'pointerup') {
            this.lastPointerUpTime = now;
          }

          // Ignore events if in 300ms double-tap cooldown to avoid 3rd-tap pairing
          if (now < this.doubleTapCooldownUntil) {
            return;
          }

          // Ignore drag/swipe gestures (movement >= 15px)
          const coords = getCoords(e);
          if (startX !== null && startY !== null && coords) {
            const dist = Math.hypot(coords.x - startX, coords.y - startY);
            if (dist >= 15) {
              this.lastPointerUpTime = now; // Suppress ghost events following drag release
              return;
            }
          }

          // Identify which slot is currently rendered as the corner PiP dock
          const isSwapped = (this.videoManager && this.videoManager.isSwapped) ||
            (this.stageElement && this.stageElement.dataset && this.stageElement.dataset.swapped === 'true');
          const cornerDockSelector = isSwapped ? '.video-primary-slot' : '.video-pip-slot';

          // If event was inside the active corner PiP dock, let PiP handler process the swap
          if (e.target && e.target.closest && e.target.closest(cornerDockSelector)) {
            return;
          }
          // If event was on interactive HUD elements, let them handle it
          if (e.target && e.target.closest && (
            e.target.closest('.video-hud-controls') ||
            e.target.closest('.video-hud-header') ||
            e.target.closest('.video-hud-side-rail') ||
            e.target.closest('.video-hud-bottom-bar')
          )) {
            return;
          }

          // Check if this tap pairs with previous tap for double-tap (<300ms, <30px movement)
          const timeDiff = now - this.lastTapTime;
          let isDoubleTap = false;
          if (this.lastTapTime > 0 && timeDiff < 300) {
            if (coords && this.lastTapCoords) {
              const moveDist = Math.hypot(coords.x - this.lastTapCoords.x, coords.y - this.lastTapCoords.y);
              if (moveDist < 30) {
                isDoubleTap = true;
              }
            } else {
              isDoubleTap = true;
            }
          }

          const stream = this.getActiveStream();
          const isMedia = Boolean(stream && (stream.controllable === true || (stream.priority === 'persistent' && Boolean(this.remoteUrl))));

          if (isDoubleTap) {
            // Clear tap timers and state, set 300ms cooldown
            if (this.singleTapTimer) {
              clearTimeout(this.singleTapTimer);
              this.singleTapTimer = null;
            }
            this.lastTapTime = 0;
            this.lastTapCoords = null;
            this.doubleTapCooldownUntil = now + 300;

            if (isMedia) {
              this.handlePlayPauseToggle();
              this.showHUD();
            }
            return;
          }

          // Single-tap handling
          this.lastTapTime = now;
          this.lastTapCoords = coords;

          if (!this.isVisible) {
            // If HUD hidden: show HUD immediately
            this.showHUD();
          } else if (isMedia) {
            // If HUD visible and media active: delay hideHUD by 250ms so a rapid second tap can cancel it and register as double-tap
            if (this.singleTapTimer) {
              clearTimeout(this.singleTapTimer);
            }
            this.singleTapTimer = setTimeout(() => {
              this.singleTapTimer = null;
              this.hideHUD();
            }, 250);
            if (this.singleTapTimer && typeof this.singleTapTimer.unref === 'function') {
              this.singleTapTimer.unref();
            }
          } else {
            // If HUD visible and no media active: toggle HUD immediately
            this.hideHUD();
          }
        };

        this.stageElement.addEventListener('pointerdown', onStagePointerDown);
        this.boundHandlers.push({ el: this.stageElement, ev: 'pointerdown', fn: onStagePointerDown });

        const stageEvents = ['pointerup', 'click'];
        stageEvents.forEach((ev) => {
          this.stageElement.addEventListener(ev, onStageToggle);
          this.boundHandlers.push({ el: this.stageElement, ev, fn: onStageToggle });
        });
      }

      // 2. Side rail pointer event isolation
      const sideRail = this.options.sideRailElement || (typeof document !== 'undefined' ? document.getElementById('video-hud-side-rail') : null);
      if (sideRail) {
        const onRailPointer = (e) => {
          if (e && typeof e.stopPropagation === 'function') e.stopPropagation();
        };
        const railEvents = ['pointerdown', 'pointerup', 'touchstart', 'touchend'];
        railEvents.forEach((ev) => {
          sideRail.addEventListener(ev, onRailPointer);
          this.boundHandlers.push({ el: sideRail, ev, fn: onRailPointer });
        });
      }

      // 3. Bottom bar pointer event isolation & button handlers
      if (this.bottomBar) {
        const onBottomBarPointer = (e) => {
          if (e && typeof e.stopPropagation === 'function') e.stopPropagation();
        };
        const barEvents = ['pointerdown', 'pointerup', 'touchstart', 'touchend', 'click'];
        barEvents.forEach((ev) => {
          this.bottomBar.addEventListener(ev, onBottomBarPointer);
          this.boundHandlers.push({ el: this.bottomBar, ev, fn: onBottomBarPointer });
        });
      }

      // 4. Rewind button
      if (this.rewindBtn) {
        const onRewind = (e) => {
          if (e && typeof e.stopPropagation === 'function') e.stopPropagation();
          this.resetAutoFade();
          this.handleRewind();
        };
        this.rewindBtn.addEventListener('click', onRewind);
        this.boundHandlers.push({ el: this.rewindBtn, ev: 'click', fn: onRewind });
      }

      // 5. Fast Forward button
      if (this.forwardBtn) {
        const onForward = (e) => {
          if (e && typeof e.stopPropagation === 'function') e.stopPropagation();
          this.resetAutoFade();
          this.handleFastForward();
        };
        this.forwardBtn.addEventListener('click', onForward);
        this.boundHandlers.push({ el: this.forwardBtn, ev: 'click', fn: onForward });
      }

      // 6. Dismiss button
      if (this.dismissBtn) {
        const onDismiss = (e) => {
          if (e && typeof e.stopPropagation === 'function') e.stopPropagation();
          this.resetAutoFade();
          this.handleDismiss();
        };
        this.dismissBtn.addEventListener('click', onDismiss);
        this.boundHandlers.push({ el: this.dismissBtn, ev: 'click', fn: onDismiss });
      }

      // 7. Play/Pause button
      if (this.playBtn) {
        const onPlayToggle = (e) => {
          if (e && typeof e.stopPropagation === 'function') e.stopPropagation();
          this.resetAutoFade();
          this.handlePlayPauseToggle();
        };
        this.playBtn.addEventListener('click', onPlayToggle);
        this.boundHandlers.push({ el: this.playBtn, ev: 'click', fn: onPlayToggle });
      }

      // 8. Skip Back (-10s) and Skip Forward (+10s)
      if (this.skipBackBtn) {
        const onSkipBack = (e) => {
          if (e && typeof e.stopPropagation === 'function') e.stopPropagation();
          this.resetAutoFade();
          this.handleRewind();
        };
        this.skipBackBtn.addEventListener('click', onSkipBack);
        this.boundHandlers.push({ el: this.skipBackBtn, ev: 'click', fn: onSkipBack });
      }

      if (this.skipFwdBtn) {
        const onSkipFwd = (e) => {
          if (e && typeof e.stopPropagation === 'function') e.stopPropagation();
          this.resetAutoFade();
          this.handleFastForward();
        };
        this.skipFwdBtn.addEventListener('click', onSkipFwd);
        this.boundHandlers.push({ el: this.skipFwdBtn, ev: 'click', fn: onSkipFwd });
      }

      // 9. Mute button
      if (this.muteBtn) {
        const onMuteToggle = (e) => {
          if (e && typeof e.stopPropagation === 'function') e.stopPropagation();
          this.resetAutoFade();
          this.handleMuteToggle();
        };
        this.muteBtn.addEventListener('click', onMuteToggle);
        this.boundHandlers.push({ el: this.muteBtn, ev: 'click', fn: onMuteToggle });
      }

      // 9. Volume slider interactions
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
      const hadStream = Boolean(this.activeStream);
      this.activeStream = stream;

      if (!stream) {
        this.hideHUD();
        if (this.bottomBar) {
          this.bottomBar.style.display = 'none';
          this.bottomBar.setAttribute('hidden', 'true');
        }
        return;
      }

      if (!hadStream && stream) {
        this.showHUD();
      }

      // Update stream title
      if (this.titleEl) {
        this.titleEl.textContent = stream.title || (stream.id ? `Stream: ${stream.id}` : 'Live Video');
      }

      // Gate bottom bar visibility
      const isMedia = Boolean(stream && (stream.controllable === true || (stream.priority === 'persistent' && Boolean(this.remoteUrl))));
      if (this.bottomBar) {
        if (isMedia) {
          this.bottomBar.style.display = '';
          this.bottomBar.removeAttribute('hidden');
        } else {
          this.bottomBar.style.display = 'none';
          this.bottomBar.setAttribute('hidden', 'true');
        }
      }

      // Update Play/Pause visibility based on controllable gating / isMedia
      if (this.playBtn) {
        if (isMedia) {
          this.playBtn.style.display = '';
          this.playBtn.removeAttribute('hidden');
        } else {
          this.playBtn.style.display = 'none';
          this.playBtn.setAttribute('hidden', 'true');
        }
      }

      if (this.skipBackBtn) {
        if (stream.controllable === true) {
          this.skipBackBtn.style.display = '';
          this.skipBackBtn.removeAttribute('hidden');
        } else {
          this.skipBackBtn.style.display = 'none';
          this.skipBackBtn.setAttribute('hidden', 'true');
        }
      }

      if (this.skipFwdBtn) {
        if (stream.controllable === true) {
          this.skipFwdBtn.style.display = '';
          this.skipFwdBtn.removeAttribute('hidden');
        } else {
          this.skipFwdBtn.style.display = 'none';
          this.skipFwdBtn.setAttribute('hidden', 'true');
        }
      }

      // Update Play/Pause icon based on player_state
      const isPaused = stream.player_state === 'paused';
      const iconType = isPaused ? 'play' : 'pause';
      if (this.playIcon) {
        this.playIcon.innerHTML = HUD_ICONS[iconType];
        if (this.playIcon.dataset) this.playIcon.dataset.icon = iconType;
      }
      if (this.playBtn) {
        this.playBtn.setAttribute('aria-label', isPaused ? 'Play' : 'Pause');
        this.playBtn.title = isPaused ? 'Play' : 'Pause';
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
        const iconType = this.currentMuted ? 'mute' : 'unmute';
        this.muteIcon.innerHTML = HUD_ICONS[iconType];
        if (this.muteIcon.dataset) this.muteIcon.dataset.icon = this.currentMuted ? 'muted' : 'unmuted';
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
      if (this.isActionPending) {
        return;
      }
      const stream = this.getActiveStream();
      if (!stream || !stream.id) {
        return;
      }

      this.isActionPending = true;
      const timer = setTimeout(() => {
        this.isActionPending = false;
      }, 300);
      if (timer && typeof timer.unref === 'function') timer.unref();

      if (stream.controllable && stream.control_url) {
        this.postAction({
          id: stream.id,
          action: 'toggle_playback',
        });
      } else if (this.remoteUrl) {
        this.postRemoteKey('play_pause');
      } else if (stream.controllable) {
        this.postAction({
          id: stream.id,
          action: 'toggle_playback',
        });
      }
    }

    /**
     * Dispatch rewind action with in-flight lock.
     */
    handleRewind() {
      if (this.isActionPending) {
        return;
      }
      const stream = this.getActiveStream();
      if (!stream || !stream.id) {
        return;
      }

      this.isActionPending = true;
      const timer = setTimeout(() => {
        this.isActionPending = false;
      }, 300);
      if (timer && typeof timer.unref === 'function') timer.unref();

      if (stream.controllable && stream.control_url) {
        this.postAction({
          id: stream.id,
          action: 'rewind',
        });
      } else if (this.remoteUrl) {
        this.postRemoteKey('rewind');
      } else if (stream.controllable) {
        this.postAction({
          id: stream.id,
          action: 'rewind',
        });
      }
    }

    /**
     * Dispatch fast forward action with in-flight lock.
     */
    handleFastForward() {
      if (this.isActionPending) {
        return;
      }
      const stream = this.getActiveStream();
      if (!stream || !stream.id) {
        return;
      }

      this.isActionPending = true;
      const timer = setTimeout(() => {
        this.isActionPending = false;
      }, 300);
      if (timer && typeof timer.unref === 'function') timer.unref();

      if (stream.controllable && stream.control_url) {
        this.postAction({
          id: stream.id,
          action: 'fast_forward',
        });
      } else if (this.remoteUrl) {
        this.postRemoteKey('fast_forward');
      } else if (stream.controllable) {
        this.postAction({
          id: stream.id,
          action: 'fast_forward',
        });
      }
    }

    /**
     * Skip forward or backward by the specified number of seconds.
     * @param {number} seconds
     */
    skipTime(seconds) {
      if (!this.stageElement) return;
      const videoEl = this.stageElement.querySelector('video');
      if (videoEl && typeof videoEl.currentTime === 'number') {
        videoEl.currentTime = Math.max(0, videoEl.currentTime + seconds);
      }
    }

    /**
     * Dispatch video stream dismiss action with in-flight lock.
     * Unmounts video mode immediately and returns to widgets mode.
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
      const timer = setTimeout(() => {
        this.isActionPending = false;
      }, 300);
      if (timer && typeof timer.unref === 'function') timer.unref();

      if (primaryId && pipId && primaryId !== pipId) {
        // When multiple streams are present (primary and PiP), dismiss all streams
        // to guarantee unmounting video mode and returning to widgets mode.
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
      const timer = setTimeout(() => {
        this.isMutePending = false;
      }, 300);
      if (timer && typeof timer.unref === 'function') timer.unref();

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
      return (0, this.fetchFn)('api/video/action', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      }).catch((err) => {
        console.warn('[MirrormereHUD] Action failed:', err);
      });
    }

    postRemoteKey(key) {
      if (!this.fetchFn || !this.remoteUrl) return Promise.resolve();
      return (0, this.fetchFn)(`${this.remoteUrl}/key`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ key }),
      }).catch((err) => {
        console.warn('[MirrormereHUD] Remote key failed:', err);
      });
    }

    postDismiss(body) {
      if (!this.fetchFn) return Promise.resolve();
      return (0, this.fetchFn)('api/video/dismiss', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      }).catch((err) => {
        console.warn('[MirrormereHUD] Dismiss failed:', err);
      });
    }

    postMute(body) {
      if (!this.fetchFn) return Promise.resolve();
      return (0, this.fetchFn)('api/audio/mute', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      }).catch((err) => {
        console.warn('[MirrormereHUD] Mute toggle failed:', err);
      });
    }

    postVolume(body) {
      if (!this.fetchFn) return Promise.resolve();
      return (0, this.fetchFn)('api/audio/volume', {
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

      if (this.singleTapTimer) {
        clearTimeout(this.singleTapTimer);
        this.singleTapTimer = null;
      }

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

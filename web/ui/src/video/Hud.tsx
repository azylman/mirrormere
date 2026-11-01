import { useEffect, useRef } from 'react';
import type { VideoSource, VideoStatePayload } from '../sse/types';
import type { AudioSlice } from '../store/types';
import { useDisplayStore } from '../store';

/**
 * Standard HUD icons used across VideoHUD transport and audio controls.
 */
export const HUD_ICONS = {
  play: '<svg viewBox="0 0 24 24" width="22" height="22" fill="currentColor" aria-hidden="true"><polygon points="6 4 20 12 6 20 6 4"></polygon></svg>',
  pause: '<svg viewBox="0 0 24 24" width="22" height="22" fill="currentColor" aria-hidden="true"><rect x="6" y="4" width="4" height="16" rx="1"></rect><rect x="14" y="4" width="4" height="16" rx="1"></rect></svg>',
  mute: '<svg viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><polygon points="11 5 6 9 2 9 2 15 6 15 11 19 11 5" fill="currentColor"></polygon><line x1="23" y1="9" x2="17" y2="15"></line><line x1="17" y1="9" x2="23" y2="15"></line></svg>',
  unmute: '<svg viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><polygon points="11 5 6 9 2 9 2 15 6 15 11 19 11 5" fill="currentColor"></polygon><path d="M19.07 4.93a10 10 0 0 1 0 14.14M15.54 8.46a5 5 0 0 1 0 7.07"></path></svg>',
  prev: '<svg viewBox="0 0 24 24" width="20" height="20" fill="currentColor" aria-hidden="true"><rect x="5" y="4" width="2.5" height="16" rx="1"></rect><polygon points="19 4 8.5 12 19 20"></polygon></svg>',
  next: '<svg viewBox="0 0 24 24" width="20" height="20" fill="currentColor" aria-hidden="true"><polygon points="5 4 15.5 12 5 20"></polygon><rect x="16.5" y="4" width="2.5" height="16" rx="1"></rect></svg>',
} as const;

export interface VideoHUDControllerOptions {
  overlayElement?: HTMLElement | null;
  titleElement?: HTMLElement | null;
  dismissBtn?: HTMLElement | null;
  playBtn?: HTMLElement | null;
  playIcon?: HTMLElement | null;
  rewindBtn?: HTMLElement | null;
  forwardBtn?: HTMLElement | null;
  bottomBarElement?: HTMLElement | null;
  muteBtn?: HTMLElement | null;
  muteIcon?: HTMLElement | null;
  volumeSlider?: HTMLInputElement | null;
  skipBackBtn?: HTMLElement | null;
  skipFwdBtn?: HTMLElement | null;
  navBarElement?: HTMLElement | null;
  backBtn?: HTMLElement | null;
  homeBtn?: HTMLElement | null;
  ccBtn?: HTMLElement | null;
  prevBtn?: HTMLElement | null;
  nextBtn?: HTMLElement | null;
  progressContainer?: HTMLElement | null;
  timeCurrentElement?: HTMLElement | null;
  timeTotalElement?: HTMLElement | null;
  progressFillElement?: HTMLElement | null;
  progressTrackElement?: HTMLElement | null;
  sideRailElement?: HTMLElement | null;
  stageElement?: HTMLElement | null;
  videoManager?: {
    isSwapped?: boolean;
    serverPrimary?: VideoSource | null;
    serverPip?: VideoSource | null;
    remoteUrl?: string;
  } | null;
  remoteUrl?: string;
  fetch?: typeof fetch;
  autoFadeTimeout?: number;
  throttleInterval?: number;
  commitCooldown?: number;
}

function tryUnref(timer: unknown): void {
  if (!timer) return;
  const unrefable = timer as { unref?: () => void };
  if (typeof unrefable.unref === 'function') {
    unrefable.unref();
  }
}

interface BoundHandler {
  el: HTMLElement | Window | Document;
  ev: string;
  fn: (e: Event) => void;
}

/**
 * VideoHUDController coordinates the HUD overlay lifecycle, transport actions,
 * and audio controls over live video streams.
 */
export class VideoHUDController {
  public options: VideoHUDControllerOptions;
  public overlay: HTMLElement | null;
  public titleEl: HTMLElement | null;
  public dismissBtn: HTMLElement | null;
  public bottomBar: HTMLElement | null;
  public rewindBtn: HTMLElement | null;
  public forwardBtn: HTMLElement | null;
  public playBtn: HTMLElement | null;
  public playIcon: HTMLElement | null;
  public muteBtn: HTMLElement | null;
  public muteIcon: HTMLElement | null;
  public volumeSlider: HTMLInputElement | null;
  public skipBackBtn: HTMLElement | null;
  public skipFwdBtn: HTMLElement | null;
  public navBar: HTMLElement | null;
  public backBtn: HTMLElement | null;
  public homeBtn: HTMLElement | null;
  public ccBtn: HTMLElement | null;
  public prevBtn: HTMLElement | null;
  public nextBtn: HTMLElement | null;
  public progressContainer: HTMLElement | null;
  public timeCurrentEl: HTMLElement | null;
  public timeTotalEl: HTMLElement | null;
  public progressFillEl: HTMLElement | null;
  public progressTrackEl: HTMLElement | null;
  public stageElement: HTMLElement | null;
  public videoManager: VideoHUDControllerOptions['videoManager'] | null;
  private _remoteUrl: string;
  public fetchFn: typeof fetch | null;
  public autoFadeTimeout: number;
  public throttleInterval: number;
  public commitCooldown: number;

  public isVisible: boolean;
  public autoFadeTimer: ReturnType<typeof setTimeout> | null;
  public activeStream: VideoSource | null;
  public lastVideoState: VideoStatePayload | null;
  public currentMuted: boolean;

  public progressTicker: ReturnType<typeof setInterval> | null;
  public basePosition: number;
  public baseDuration: number;
  public basePositionTime: number;
  public isScrubbing: boolean;

  public isActionPending: boolean;
  public isMutePending: boolean;

  public lastTapTime: number;
  public lastTapCoords: { x: number; y: number } | null;
  public singleTapTimer: ReturnType<typeof setTimeout> | null;
  public doubleTapCooldownUntil: number;
  public lastPointerUpTime: number;

  public isDragging: boolean;
  public throttleTimer: ReturnType<typeof setTimeout> | null;
  public pendingVolume: number | null;
  public lastNetworkVolume: number | null;
  public commitCooldownUntil: number;

  private boundHandlers: BoundHandler[];

  constructor(options: VideoHUDControllerOptions = {}) {
    this.options = options;
    this.overlay =
      options.overlayElement ||
      (typeof document !== 'undefined' ? document.getElementById('video-hud-overlay') : null);
    this.titleEl =
      options.titleElement ||
      (typeof document !== 'undefined' ? document.getElementById('video-hud-title') : null);
    this.dismissBtn =
      options.dismissBtn ||
      (typeof document !== 'undefined' ? document.getElementById('video-hud-dismiss-btn') : null);
    this.bottomBar =
      options.bottomBarElement ||
      (this.overlay?.querySelector
        ? this.overlay.querySelector<HTMLElement>('#video-hud-bottom-bar')
        : null) ||
      (typeof document !== 'undefined' ? document.getElementById('video-hud-bottom-bar') : null);

    const findInBottomBar = (id: string): HTMLElement | null => {
      if (typeof document !== 'undefined') {
        const docEl = document.getElementById(id);
        if (docEl) return docEl;
      }
      if (this.bottomBar?.querySelector) {
        const nested = this.bottomBar.querySelector<HTMLElement>(`#${id}`);
        if (nested) return nested;
      }
      return null;
    };

    this.rewindBtn = options.rewindBtn || findInBottomBar('video-hud-rewind-btn');
    this.forwardBtn = options.forwardBtn || findInBottomBar('video-hud-forward-btn');
    this.playBtn =
      options.playBtn ||
      (typeof document !== 'undefined' ? document.getElementById('video-hud-play-btn') : null) ||
      findInBottomBar('video-hud-play-btn') ||
      (this.overlay?.querySelector ? this.overlay.querySelector<HTMLElement>('#video-hud-play-btn') : null) ||
      null;
    this.playIcon =
      options.playIcon ||
      (this.playBtn?.querySelector ? this.playBtn.querySelector<HTMLElement>('#video-hud-play-icon') : null) ||
      (typeof document !== 'undefined' ? document.getElementById('video-hud-play-icon') : null);
    this.muteBtn =
      options.muteBtn ||
      (typeof document !== 'undefined' ? document.getElementById('video-hud-mute-btn') : null);
    this.muteIcon =
      options.muteIcon ||
      (typeof document !== 'undefined' ? document.getElementById('video-hud-mute-icon') : null);
    this.volumeSlider =
      options.volumeSlider ||
      (typeof document !== 'undefined'
        ? (document.getElementById('video-hud-volume-slider') as HTMLInputElement | null)
        : null);
    this.skipBackBtn =
      options.skipBackBtn ||
      (typeof document !== 'undefined' ? document.getElementById('video-hud-skip-back-btn') : null);
    this.skipFwdBtn =
      options.skipFwdBtn ||
      (typeof document !== 'undefined' ? document.getElementById('video-hud-skip-fwd-btn') : null);
    this.navBar =
      options.navBarElement ||
      (this.overlay?.querySelector ? this.overlay.querySelector<HTMLElement>('#video-hud-nav-bar') : null) ||
      (typeof document !== 'undefined' ? document.getElementById('video-hud-nav-bar') : null);
    this.backBtn =
      options.backBtn ||
      (this.navBar?.querySelector ? this.navBar.querySelector<HTMLElement>('#video-hud-back-btn') : null) ||
      (typeof document !== 'undefined' ? document.getElementById('video-hud-back-btn') : null);
    this.homeBtn =
      options.homeBtn ||
      (this.navBar?.querySelector ? this.navBar.querySelector<HTMLElement>('#video-hud-home-btn') : null) ||
      (typeof document !== 'undefined' ? document.getElementById('video-hud-home-btn') : null);
    this.ccBtn = options.ccBtn || findInBottomBar('video-hud-cc-btn');
    this.prevBtn = options.prevBtn || findInBottomBar('video-hud-prev-btn');
    this.nextBtn = options.nextBtn || findInBottomBar('video-hud-next-btn');

    this.progressContainer =
      options.progressContainer || findInBottomBar('video-hud-progress-container');
    this.timeCurrentEl = options.timeCurrentElement || findInBottomBar('video-hud-time-current');
    this.timeTotalEl = options.timeTotalElement || findInBottomBar('video-hud-time-total');
    this.progressFillEl = options.progressFillElement || findInBottomBar('video-hud-progress-fill');
    this.progressTrackEl =
      options.progressTrackElement || findInBottomBar('video-hud-progress-track');

    if (this.prevBtn && (!this.prevBtn.innerHTML || !this.prevBtn.innerHTML.includes('<svg'))) {
      this.prevBtn.innerHTML = HUD_ICONS.prev;
    }
    if (this.nextBtn && (!this.nextBtn.innerHTML || !this.nextBtn.innerHTML.includes('<svg'))) {
      this.nextBtn.innerHTML = HUD_ICONS.next;
    }
    this.stageElement =
      options.stageElement ||
      (typeof document !== 'undefined' ? document.getElementById('video-stage') : null);
    this.videoManager = options.videoManager || null;
    this._remoteUrl = options.remoteUrl || '';

    const defaultFetch =
      typeof window !== 'undefined' && typeof window.fetch === 'function'
        ? window.fetch.bind(window)
        : typeof fetch === 'function'
          ? fetch
          : null;
    this.fetchFn = options.fetch || defaultFetch;

    this.autoFadeTimeout = options.autoFadeTimeout !== undefined ? options.autoFadeTimeout : 5000;
    this.throttleInterval = options.throttleInterval !== undefined ? options.throttleInterval : 200;
    this.commitCooldown = options.commitCooldown !== undefined ? options.commitCooldown : 350;

    this.isVisible = false;
    this.autoFadeTimer = null;
    this.activeStream = null;
    this.lastVideoState = null;
    this.currentMuted = false;

    this.progressTicker = null;
    this.basePosition = 0;
    this.baseDuration = 0;
    this.basePositionTime = 0;
    this.isScrubbing = false;

    this.isActionPending = false;
    this.isMutePending = false;

    this.lastTapTime = 0;
    this.lastTapCoords = null;
    this.singleTapTimer = null;
    this.doubleTapCooldownUntil = 0;
    this.lastPointerUpTime = 0;

    this.isDragging = false;
    this.throttleTimer = null;
    this.pendingVolume = null;
    this.lastNetworkVolume = null;
    this.commitCooldownUntil = 0;

    this.boundHandlers = [];

    this.bindControls();
  }

  get remoteUrl(): string {
    return (
      this._remoteUrl ||
      this.videoManager?.remoteUrl ||
      (typeof document !== 'undefined' && document.body && document.body.dataset && document.body.dataset.remoteUrl) ||
      (typeof window !== 'undefined' && (window as unknown as { MIRRORMERE_REMOTE_URL?: string }).MIRRORMERE_REMOTE_URL) ||
      ''
    );
  }

  set remoteUrl(val: string) {
    this._remoteUrl = val;
  }

  /**
   * Bind DOM event listeners.
   */
  bindControls(): void {
    // 1. Stage tap-to-wake / tap-to-toggle & double-tap play/pause
    if (this.stageElement) {
      let startX: number | null = null;
      let startY: number | null = null;

      const getCoords = (e: Event): { x: number; y: number } | null => {
        const touchEvent = e as TouchEvent;
        if (touchEvent.changedTouches && touchEvent.changedTouches.length > 0) {
          return {
            x: touchEvent.changedTouches[0].clientX,
            y: touchEvent.changedTouches[0].clientY,
          };
        }
        const mouseEvent = e as MouseEvent;
        if (typeof mouseEvent.clientX === 'number' && typeof mouseEvent.clientY === 'number') {
          return { x: mouseEvent.clientX, y: mouseEvent.clientY };
        }
        return null;
      };

      const onStagePointerDown = (e: Event): void => {
        const coords = getCoords(e);
        if (coords) {
          startX = coords.x;
          startY = coords.y;
        } else {
          startX = null;
          startY = null;
        }
      };

      const onStageToggle = (e: Event): void => {
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
        const isSwapped =
          (this.videoManager && this.videoManager.isSwapped) ||
          (this.stageElement &&
            this.stageElement.dataset &&
            this.stageElement.dataset.swapped === 'true');
        const cornerDockSelector = isSwapped ? '.video-primary-slot' : '.video-pip-slot';

        const targetEl = e.target as Element | null;
        if (targetEl && typeof targetEl.closest === 'function' && targetEl.closest(cornerDockSelector)) {
          return;
        }

        if (
          targetEl &&
          typeof targetEl.closest === 'function' &&
          (targetEl.closest('.video-hud-controls') ||
            targetEl.closest('.video-hud-header') ||
            targetEl.closest('.video-hud-side-rail') ||
            targetEl.closest('.video-hud-bottom-bar') ||
            targetEl.closest('.video-hud-play-center') ||
            targetEl.closest('#video-hud-play-btn'))
        ) {
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
        const isMedia = Boolean(
          stream &&
            (stream.controllable === true ||
              (stream.priority === 'persistent' && Boolean(this.remoteUrl))),
        );
        const isIdle = Boolean(stream && stream.player_state === 'idle');
        const isPlaybackActive = Boolean(isMedia && !isIdle);

        if (isDoubleTap) {
          if (this.singleTapTimer) {
            clearTimeout(this.singleTapTimer);
            this.singleTapTimer = null;
          }
          this.lastTapTime = 0;
          this.lastTapCoords = null;
          this.doubleTapCooldownUntil = now + 300;

          if (isPlaybackActive) {
            this.handlePlayPauseToggle();
            this.showHUD();
          }
          return;
        }

        // Single-tap handling
        this.lastTapTime = now;
        this.lastTapCoords = coords;

        if (!this.isVisible) {
          this.showHUD();
        } else if (isPlaybackActive) {
          if (this.singleTapTimer) {
            clearTimeout(this.singleTapTimer);
          }
          this.singleTapTimer = setTimeout(() => {
            this.singleTapTimer = null;
            this.hideHUD();
          }, 250);
          tryUnref(this.singleTapTimer);
        } else {
          this.lastTapTime = 0;
          this.lastTapCoords = null;
          this.hideHUD();
        }
      };

      this.stageElement.addEventListener('pointerdown', onStagePointerDown);
      this.boundHandlers.push({
        el: this.stageElement,
        ev: 'pointerdown',
        fn: onStagePointerDown,
      });

      const stageEvents = ['pointerup', 'click'];
      stageEvents.forEach((ev) => {
        if (!this.stageElement) return;
        this.stageElement.addEventListener(ev, onStageToggle);
        this.boundHandlers.push({ el: this.stageElement, ev, fn: onStageToggle });
      });
    }

    // 2. Side rail pointer event isolation
    const sideRail =
      this.options.sideRailElement ||
      (typeof document !== 'undefined' ? document.getElementById('video-hud-side-rail') : null);
    if (sideRail) {
      const onRailPointer = (e: Event): void => {
        if (e && typeof e.stopPropagation === 'function') e.stopPropagation();
      };
      const railEvents = ['pointerdown', 'pointerup', 'touchstart', 'touchend'];
      railEvents.forEach((ev) => {
        sideRail.addEventListener(ev, onRailPointer);
        this.boundHandlers.push({ el: sideRail, ev, fn: onRailPointer });
      });
    }

    // 2b. Nav bar pointer event isolation & navigation handlers
    if (this.navBar) {
      const onNavPointer = (e: Event): void => {
        if (e && typeof e.stopPropagation === 'function') e.stopPropagation();
      };
      const navEvents = ['pointerdown', 'pointerup', 'touchstart', 'touchend'];
      navEvents.forEach((ev) => {
        if (!this.navBar) return;
        this.navBar.addEventListener(ev, onNavPointer);
        this.boundHandlers.push({ el: this.navBar, ev, fn: onNavPointer });
      });
    }

    // Back button
    if (this.backBtn) {
      const onBack = (e: Event): void => {
        if (e && typeof e.stopPropagation === 'function') e.stopPropagation();
        this.resetAutoFade();
        this.handleBack();
      };
      this.backBtn.addEventListener('click', onBack);
      this.boundHandlers.push({ el: this.backBtn, ev: 'click', fn: onBack });
    }

    // Home button
    if (this.homeBtn) {
      const onHome = (e: Event): void => {
        if (e && typeof e.stopPropagation === 'function') e.stopPropagation();
        this.resetAutoFade();
        this.handleHome();
      };
      this.homeBtn.addEventListener('click', onHome);
      this.boundHandlers.push({ el: this.homeBtn, ev: 'click', fn: onHome });
    }

    // 3. Bottom bar pointer event isolation & button handlers
    if (this.bottomBar) {
      const onBottomBarPointer = (e: Event): void => {
        if (e && typeof e.stopPropagation === 'function') e.stopPropagation();
      };
      const barEvents = ['pointerdown', 'pointerup', 'touchstart', 'touchend'];
      barEvents.forEach((ev) => {
        if (!this.bottomBar) return;
        this.bottomBar.addEventListener(ev, onBottomBarPointer);
        this.boundHandlers.push({ el: this.bottomBar, ev, fn: onBottomBarPointer });
      });
    }

    // CC button
    if (this.ccBtn) {
      const onCC = (e: Event): void => {
        if (e && typeof e.stopPropagation === 'function') e.stopPropagation();
        this.resetAutoFade();
        this.handleCaptions();
      };
      this.ccBtn.addEventListener('click', onCC);
      this.boundHandlers.push({ el: this.ccBtn, ev: 'click', fn: onCC });
    }

    // Previous button
    if (this.prevBtn) {
      const onPrev = (e: Event): void => {
        if (e && typeof e.stopPropagation === 'function') e.stopPropagation();
        this.resetAutoFade();
        this.handlePrevious();
      };
      this.prevBtn.addEventListener('click', onPrev);
      this.boundHandlers.push({ el: this.prevBtn, ev: 'click', fn: onPrev });
    }

    // 4. Rewind button
    if (this.rewindBtn) {
      const onRewind = (e: Event): void => {
        if (e && typeof e.stopPropagation === 'function') e.stopPropagation();
        this.resetAutoFade();
        this.handleRewind();
      };
      this.rewindBtn.addEventListener('click', onRewind);
      this.boundHandlers.push({ el: this.rewindBtn, ev: 'click', fn: onRewind });
    }

    // 5. Fast Forward button
    if (this.forwardBtn) {
      const onForward = (e: Event): void => {
        if (e && typeof e.stopPropagation === 'function') e.stopPropagation();
        this.resetAutoFade();
        this.handleFastForward();
      };
      this.forwardBtn.addEventListener('click', onForward);
      this.boundHandlers.push({ el: this.forwardBtn, ev: 'click', fn: onForward });
    }

    // Next button
    if (this.nextBtn) {
      const onNext = (e: Event): void => {
        if (e && typeof e.stopPropagation === 'function') e.stopPropagation();
        this.resetAutoFade();
        this.handleNext();
      };
      this.nextBtn.addEventListener('click', onNext);
      this.boundHandlers.push({ el: this.nextBtn, ev: 'click', fn: onNext });
    }

    // 6. Dismiss button
    if (this.dismissBtn) {
      const onDismiss = (e: Event): void => {
        if (e && typeof e.stopPropagation === 'function') e.stopPropagation();
        this.resetAutoFade();
        this.handleDismiss();
      };
      this.dismissBtn.addEventListener('click', onDismiss);
      this.boundHandlers.push({ el: this.dismissBtn, ev: 'click', fn: onDismiss });
    }

    // 7. Play/Pause button and pointer isolation
    if (this.playBtn) {
      const onPlayPointer = (e: Event): void => {
        if (e && typeof e.stopPropagation === 'function') e.stopPropagation();
      };
      const playEvents = ['pointerdown', 'pointerup', 'touchstart', 'touchend'];
      playEvents.forEach((ev) => {
        if (!this.playBtn) return;
        this.playBtn.addEventListener(ev, onPlayPointer);
        this.boundHandlers.push({ el: this.playBtn, ev, fn: onPlayPointer });
      });

      const onPlayToggle = (e: Event): void => {
        if (e && typeof e.stopPropagation === 'function') e.stopPropagation();
        this.resetAutoFade();
        this.handlePlayPauseToggle();
      };
      this.playBtn.addEventListener('click', onPlayToggle);
      this.boundHandlers.push({ el: this.playBtn, ev: 'click', fn: onPlayToggle });
    }

    // 8. Skip Back (-10s) and Skip Forward (+10s)
    if (this.skipBackBtn) {
      const onSkipBack = (e: Event): void => {
        if (e && typeof e.stopPropagation === 'function') e.stopPropagation();
        this.resetAutoFade();
        this.handleRewind();
      };
      this.skipBackBtn.addEventListener('click', onSkipBack);
      this.boundHandlers.push({ el: this.skipBackBtn, ev: 'click', fn: onSkipBack });
    }

    if (this.skipFwdBtn) {
      const onSkipFwd = (e: Event): void => {
        if (e && typeof e.stopPropagation === 'function') e.stopPropagation();
        this.resetAutoFade();
        this.handleFastForward();
      };
      this.skipFwdBtn.addEventListener('click', onSkipFwd);
      this.boundHandlers.push({ el: this.skipFwdBtn, ev: 'click', fn: onSkipFwd });
    }

    // 9. Mute button
    if (this.muteBtn) {
      const onMuteToggle = (e: Event): void => {
        if (e && typeof e.stopPropagation === 'function') e.stopPropagation();
        this.resetAutoFade();
        this.handleMuteToggle();
      };
      this.muteBtn.addEventListener('click', onMuteToggle);
      this.boundHandlers.push({ el: this.muteBtn, ev: 'click', fn: onMuteToggle });
    }

    // 10. Volume slider interactions
    if (this.volumeSlider) {
      const onSliderInput = (e: Event): void => {
        if (e && typeof e.stopPropagation === 'function') e.stopPropagation();
        this.handleSliderInput();
      };
      this.volumeSlider.addEventListener('input', onSliderInput);
      this.boundHandlers.push({ el: this.volumeSlider, ev: 'input', fn: onSliderInput });

      const onSliderCommit = (e: Event): void => {
        if (e && typeof e.stopPropagation === 'function') e.stopPropagation();
        this.handleSliderCommit();
      };

      const commitEvents = ['pointerup', 'touchend', 'pointercancel', 'touchcancel', 'change'];
      commitEvents.forEach((ev) => {
        if (!this.volumeSlider) return;
        this.volumeSlider.addEventListener(ev, onSliderCommit);
        this.boundHandlers.push({ el: this.volumeSlider, ev, fn: onSliderCommit });
      });
    }

    // 11. Progress track click-and-drag seeking
    if (this.progressTrackEl) {
      let currentSeekTime = 0;

      const updateScrub = (clientX: number): number => {
        if (!this.progressTrackEl || this.baseDuration <= 0) return 0;
        const rect =
          typeof this.progressTrackEl.getBoundingClientRect === 'function'
            ? this.progressTrackEl.getBoundingClientRect()
            : { left: 0, width: 100 };
        const width = rect.width > 0 ? rect.width : 1;
        const pct = Math.max(0, Math.min(1, (clientX - rect.left) / width));
        const targetTime = pct * this.baseDuration;
        currentSeekTime = targetTime;

        if (this.progressFillEl) {
          this.progressFillEl.style.width = `${(pct * 100).toFixed(1)}%`;
        }
        if (this.timeCurrentEl) {
          this.timeCurrentEl.textContent = this.formatTime(targetTime);
        }
        return targetTime;
      };

      const onTrackPointerDown = (e: Event): void => {
        if (e && typeof e.stopPropagation === 'function') e.stopPropagation();
        this.resetAutoFade();

        if (this.baseDuration <= 0) return;
        const stream = this.getActiveStream();
        if (!stream || (stream.duration !== undefined && stream.duration <= 0)) return;

        const pe = e as PointerEvent;
        if (this.progressTrackEl && typeof this.progressTrackEl.setPointerCapture === 'function' && pe.pointerId !== undefined) {
          try {
            this.progressTrackEl.setPointerCapture(pe.pointerId);
          } catch {
            // ignore
          }
        }

        this.isScrubbing = true;
        if (this.progressTrackEl?.classList) {
          this.progressTrackEl.classList.add('is-scrubbing');
        }

        const clientX = typeof pe.clientX === 'number' ? pe.clientX : 0;
        updateScrub(clientX);
      };

      const onTrackPointerMove = (e: Event): void => {
        if (!this.isScrubbing) return;
        if (e && typeof e.stopPropagation === 'function') e.stopPropagation();
        this.resetAutoFade();

        const pe = e as PointerEvent;
        const clientX = typeof pe.clientX === 'number' ? pe.clientX : 0;
        updateScrub(clientX);
      };

      const onTrackPointerEnd = (e: Event): void => {
        if (!this.isScrubbing) return;
        if (e && typeof e.stopPropagation === 'function') e.stopPropagation();
        this.resetAutoFade();

        const pe = e as PointerEvent;
        if (this.progressTrackEl && typeof this.progressTrackEl.releasePointerCapture === 'function' && pe.pointerId !== undefined) {
          try {
            this.progressTrackEl.releasePointerCapture(pe.pointerId);
          } catch {
            // ignore
          }
        }

        this.isScrubbing = false;
        if (this.progressTrackEl?.classList) {
          this.progressTrackEl.classList.remove('is-scrubbing');
        }

        const targetTime = currentSeekTime;
        this.handleSeek(targetTime);
        this.basePosition = targetTime;
        this.basePositionTime = Date.now();
      };

      const trackEvents: { ev: string; fn: (e: Event) => void }[] = [
        { ev: 'pointerdown', fn: onTrackPointerDown },
        { ev: 'pointermove', fn: onTrackPointerMove },
        { ev: 'pointerup', fn: onTrackPointerEnd },
        { ev: 'pointercancel', fn: onTrackPointerEnd },
      ];
      trackEvents.forEach(({ ev, fn }) => {
        if (!this.progressTrackEl) return;
        this.progressTrackEl.addEventListener(ev, fn);
        this.boundHandlers.push({ el: this.progressTrackEl, ev, fn });
      });
    }
  }

  /**
   * Reveal HUD overlay and start auto-fade countdown.
   */
  showHUD(): void {
    this.isVisible = true;
    if (this.overlay) {
      this.overlay.classList.add('visible');
    }
    this.renderProgressTick();
    this.startProgressTicker();
    this.resetAutoFade();
  }

  /**
   * Hide HUD overlay and cancel countdown timer.
   */
  hideHUD(): void {
    this.isVisible = false;
    this.stopProgressTicker();
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
  toggleHUD(): void {
    if (this.isVisible) {
      this.hideHUD();
    } else {
      this.showHUD();
    }
  }

  /**
   * Reset the 5-second auto-fade inactivity timer.
   */
  resetAutoFade(): void {
    if (!this.isVisible) return;

    if (this.autoFadeTimer) {
      clearTimeout(this.autoFadeTimer);
    }

    this.autoFadeTimer = setTimeout(() => {
      this.hideHUD();
    }, this.autoFadeTimeout);

    tryUnref(this.autoFadeTimer);
  }

  /**
   * Determine active presentation stream dynamically respecting swap state.
   */
  getActiveStream(): VideoSource | null {
    if (this.videoManager && this.videoManager.isSwapped) {
      return this.videoManager.serverPip || this.lastVideoState?.pip || null;
    }
    return this.videoManager?.serverPrimary || this.lastVideoState?.primary || null;
  }

  /**
   * Format stream presentation title: "<Series Name> <season + episode annotation>: <episode name>"
   * with graceful fallbacks.
   */
  formatStreamTitle(stream?: VideoSource | null): string {
    if (!stream) return 'Live Video';

    const streamRecord = stream as unknown as Record<string, unknown>;
    const series = String(
      stream.series || streamRecord.series_title || streamRecord.show_title || '',
    ).trim();
    const title = String(stream.title || streamRecord.episode_name || '').trim();
    let annotation = String(streamRecord.annotation || '').trim();

    if (!annotation) {
      const rawSeason =
        stream.season !== undefined && stream.season !== null
          ? stream.season
          : streamRecord.season_number;
      const s =
        rawSeason !== undefined && rawSeason !== null && String(rawSeason).trim() !== ''
          ? String(rawSeason).trim()
          : '';
      const rawEp =
        stream.episode !== undefined && stream.episode !== null
          ? stream.episode
          : streamRecord.episode_number;
      const ep =
        rawEp !== undefined && rawEp !== null && String(rawEp).trim() !== ''
          ? String(rawEp).trim()
          : '';

      if (s && ep) {
        const sNorm = s.toUpperCase().startsWith('S') ? s.toUpperCase() : `S${s}`;
        const epNorm = ep.toUpperCase().startsWith('E') ? ep.toUpperCase() : `E${ep}`;
        annotation = `${sNorm}${epNorm}`;
      } else if (s) {
        annotation = s.toUpperCase().startsWith('S') ? s.toUpperCase() : `S${s}`;
      } else if (ep) {
        annotation = ep.toUpperCase().startsWith('E') ? ep.toUpperCase() : `E${ep}`;
      }
    }

    if (series && annotation && title) {
      return `${series} ${annotation}: ${title}`;
    }
    if (series && title) {
      return `${series}: ${title}`;
    }
    if (series && annotation) {
      return `${series} ${annotation}`;
    }
    if (annotation && title) {
      return `${annotation}: ${title}`;
    }
    if (title) {
      return title;
    }
    if (series) {
      return series;
    }
    return stream.id ? `Stream: ${stream.id}` : 'Live Video';
  }

  /**
   * Format time in seconds to M:SS or H:MM:SS string.
   */
  formatTime(seconds: number): string {
    if (typeof seconds !== 'number' || !Number.isFinite(seconds) || seconds < 0) {
      return '0:00';
    }
    const totalSecs = Math.floor(seconds);
    const hrs = Math.floor(totalSecs / 3600);
    const mins = Math.floor((totalSecs % 3600) / 60);
    const secs = totalSecs % 60;
    const padSecs = String(secs).padStart(2, '0');
    if (hrs > 0) {
      const padMins = String(mins).padStart(2, '0');
      return `${hrs}:${padMins}:${padSecs}`;
    }
    return `${mins}:${padSecs}`;
  }

  /**
   * Update progress state, visibility, and labels.
   */
  updateProgress(stream?: VideoSource | null): void {
    if (!this.progressContainer) return;
    if (this.isScrubbing) return;

    if (stream?.player_state === 'idle') {
      this.stopProgressTicker();
      this.baseDuration = 0;
      this.basePosition = 0;
      this.progressContainer.style.display = 'none';
      this.progressContainer.setAttribute('hidden', 'true');
      return;
    }

    const duration =
      typeof stream?.duration === 'number' && Number.isFinite(stream.duration)
        ? stream.duration
        : 0;
    const position =
      typeof stream?.position === 'number' && Number.isFinite(stream.position)
        ? stream.position
        : 0;
    const positionUpdatedAt =
      typeof stream?.position_updated_at === 'number' && Number.isFinite(stream.position_updated_at)
        ? stream.position_updated_at
        : Date.now();

    this.baseDuration = duration;
    this.basePosition = position;
    this.basePositionTime = positionUpdatedAt;

    if (duration > 0) {
      this.progressContainer.style.display = '';
      this.progressContainer.removeAttribute('hidden');
      this.renderProgressTick();
      this.startProgressTicker();
    } else {
      this.stopProgressTicker();
      this.progressContainer.style.display = 'none';
      this.progressContainer.setAttribute('hidden', 'true');
    }
  }

  /**
   * Advance progress by 1 tick using timestamp drift compensation.
   */
  renderProgressTick(): void {
    if (!this.progressContainer || this.baseDuration <= 0) return;
    if (this.isScrubbing) return;

    let currentPos = this.basePosition;
    const stream = this.activeStream;
    if (stream && stream.player_state === 'playing') {
      const elapsed = Math.max(0, (Date.now() - this.basePositionTime) / 1000);
      currentPos = Math.min(this.baseDuration, this.basePosition + elapsed);
    }

    const pct = Math.min(100, Math.max(0, (currentPos / this.baseDuration) * 100));
    if (this.progressFillEl) {
      this.progressFillEl.style.width = `${pct.toFixed(1)}%`;
    }
    if (this.timeCurrentEl) {
      this.timeCurrentEl.textContent = this.formatTime(currentPos);
    }
    if (this.timeTotalEl) {
      this.timeTotalEl.textContent = this.formatTime(this.baseDuration);
    }
  }

  /**
   * Start the 1-second progress ticker if HUD is visible and duration is positive.
   */
  startProgressTicker(): void {
    if (this.progressTicker) return;
    if (!this.isVisible || this.baseDuration <= 0) return;

    this.progressTicker = setInterval(() => {
      this.renderProgressTick();
    }, 1000);
    tryUnref(this.progressTicker);
  }

  /**
   * Stop active progress ticker timer.
   */
  stopProgressTicker(): void {
    if (this.progressTicker) {
      clearInterval(this.progressTicker);
      this.progressTicker = null;
    }
  }

  /**
   * Synchronize stream presentation, title, transport controls, and visibility.
   */
  syncStream(): void {
    const stream = this.getActiveStream();
    const hadStream = Boolean(this.activeStream);
    this.activeStream = stream;

    if (!stream) {
      this.hideHUD();
      if (this.overlay && this.overlay.dataset) {
        delete this.overlay.dataset.playerState;
      }
      if (this.bottomBar) {
        this.bottomBar.style.display = 'none';
        this.bottomBar.style.pointerEvents = 'none';
        this.bottomBar.setAttribute('hidden', 'true');
      }
      if (this.navBar) {
        this.navBar.style.display = 'none';
        this.navBar.setAttribute('hidden', 'true');
      }
      return;
    }

    if (!hadStream && stream) {
      this.showHUD();
    }

    if (this.titleEl) {
      this.titleEl.textContent = this.formatStreamTitle(stream);
    }

    this.updateProgress(stream);

    const isMedia = Boolean(
      stream &&
        (stream.controllable === true ||
          (stream.priority === 'persistent' && Boolean(this.remoteUrl))),
    );
    const isIdle = Boolean(stream && stream.player_state === 'idle');
    const isPlaybackActive = Boolean(isMedia && !isIdle);

    if (this.overlay && this.overlay.dataset) {
      this.overlay.dataset.playerState = stream.player_state || (isIdle ? 'idle' : 'playing');
    }

    if (this.bottomBar) {
      if (isPlaybackActive) {
        this.bottomBar.style.display = '';
        this.bottomBar.style.pointerEvents = '';
        this.bottomBar.removeAttribute('hidden');
      } else {
        this.bottomBar.style.display = 'none';
        this.bottomBar.style.pointerEvents = 'none';
        this.bottomBar.setAttribute('hidden', 'true');
      }
    }

    if (this.navBar) {
      if (isMedia) {
        this.navBar.style.display = '';
        this.navBar.removeAttribute('hidden');
      } else {
        this.navBar.style.display = 'none';
        this.navBar.setAttribute('hidden', 'true');
      }
    }

    if (this.playBtn) {
      if (isPlaybackActive) {
        this.playBtn.style.display = '';
        this.playBtn.style.pointerEvents = '';
        this.playBtn.removeAttribute('hidden');
      } else {
        this.playBtn.style.display = 'none';
        this.playBtn.style.pointerEvents = 'none';
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
   * Process incoming video.state event payload.
   */
  handleVideoState(data?: VideoStatePayload | null): void {
    this.lastVideoState = data || null;
    this.syncStream();
  }

  /**
   * Process incoming audio.state event payload.
   */
  handleAudioState(data?: Partial<AudioSlice> | null): void {
    if (!data) return;

    this.currentMuted = Boolean(data.muted);

    if (this.muteIcon) {
      const iconType = this.currentMuted ? 'mute' : 'unmute';
      this.muteIcon.innerHTML = HUD_ICONS[iconType];
      if (this.muteIcon.dataset) {
        this.muteIcon.dataset.icon = this.currentMuted ? 'muted' : 'unmuted';
      }
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

    if (this.volumeSlider) {
      if (this.currentMuted) {
        this.volumeSlider.classList.add('muted');
      } else {
        this.volumeSlider.classList.remove('muted');
      }

      const now = Date.now();
      if (!this.isDragging && now >= this.commitCooldownUntil && data.volume !== undefined) {
        this.volumeSlider.value = String(data.volume);
      }
    }
  }

  /**
   * Dispatch a video action with in-flight lock and guaranteed finally release.
   */
  executeAction(actionName: string, remoteKeyName?: string, value?: unknown): Promise<void> {
    if (this.isActionPending && actionName !== 'seek') {
      return Promise.resolve();
    }
    const stream = this.getActiveStream();
    if (!stream || !stream.id) {
      return Promise.resolve();
    }

    this.isActionPending = true;
    let timer: ReturnType<typeof setTimeout> | null = setTimeout(() => {
      timer = null;
      this.isActionPending = false;
    }, 300);
    tryUnref(timer);

    const payload: Record<string, unknown> = {
      id: stream.id,
      action: actionName,
    };
    if (value !== undefined) {
      payload.value = value;
    }

    const isRemoteKeyPreferred = ['back', 'home', 'captions', 'previous', 'next'].includes(actionName);

    let p: Promise<unknown> | null = null;
    if (isRemoteKeyPreferred && this.remoteUrl) {
      p = this.postRemoteKey(remoteKeyName || actionName);
    } else if (value !== undefined || actionName === 'seek' || (stream.controllable && stream.control_url)) {
      p = this.postAction(payload);
    } else if (this.remoteUrl) {
      p = this.postRemoteKey(remoteKeyName || actionName);
    } else if (stream.controllable) {
      p = this.postAction(payload);
    }

    return Promise.resolve(p)
      .then(() => {})
      .finally(() => {
        if (!timer) {
          this.isActionPending = false;
        }
      });
  }

  /**
   * Dispatch seek action with in-flight lock.
   */
  handleSeek(positionSeconds: number): Promise<void> {
    return this.executeAction('seek', 'seek', positionSeconds);
  }

  /**
   * Dispatch play/pause toggle action with in-flight lock.
   */
  handlePlayPauseToggle(): Promise<void> {
    return this.executeAction('toggle_playback', 'play_pause');
  }

  /**
   * Dispatch rewind action with in-flight lock.
   */
  handleRewind(): Promise<void> {
    return this.executeAction('rewind', 'rewind');
  }

  /**
   * Dispatch fast forward action with in-flight lock.
   */
  handleFastForward(): Promise<void> {
    return this.executeAction('fast_forward', 'fast_forward');
  }

  /**
   * Dispatch back action with in-flight lock.
   */
  handleBack(): Promise<void> {
    return this.executeAction('back', 'back');
  }

  /**
   * Dispatch home action with in-flight lock.
   */
  handleHome(): Promise<void> {
    return this.executeAction('home', 'home');
  }

  /**
   * Dispatch captions toggle action with in-flight lock.
   */
  handleCaptions(): Promise<void> {
    return this.executeAction('captions', 'cc');
  }

  /**
   * Dispatch previous episode/track action with in-flight lock.
   */
  handlePrevious(): Promise<void> {
    return this.executeAction('previous', 'prev');
  }

  /**
   * Dispatch next episode/track action with in-flight lock.
   */
  handleNext(): Promise<void> {
    return this.executeAction('next', 'next');
  }

  /**
   * Skip forward or backward by specified seconds in video element.
   */
  skipTime(seconds: number): void {
    if (!this.stageElement) return;
    const videoEl = this.stageElement.querySelector('video');
    if (videoEl && typeof videoEl.currentTime === 'number') {
      videoEl.currentTime = Math.max(0, videoEl.currentTime + seconds);
    }
  }

  /**
   * Dispatch video stream dismiss action with in-flight lock.
   */
  handleDismiss(): void {
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
    tryUnref(timer);

    if (primaryId && pipId && primaryId !== pipId) {
      this.postDismiss({ id: 'all' });
    } else {
      const targetId = primaryId || pipId || this.activeStream?.id;
      this.postDismiss({ id: targetId });
    }
  }

  /**
   * Dispatch mute toggle action with in-flight lock.
   */
  handleMuteToggle(): void {
    if (this.isMutePending) {
      return;
    }

    this.isMutePending = true;
    const timer = setTimeout(() => {
      this.isMutePending = false;
    }, 300);
    tryUnref(timer);

    const targetMuted = !this.currentMuted;
    this.postMute({
      muted: targetMuted,
    });
  }

  /**
   * Handle continuous volume slider dragging (60fps visual tracking, 200ms network throttle).
   */
  handleSliderInput(): void {
    if (!this.volumeSlider) return;

    this.isDragging = true;
    this.resetAutoFade();

    const val = parseInt(this.volumeSlider.value, 10);
    if (Number.isNaN(val)) return;

    this.pendingVolume = val;

    if (!this.throttleTimer) {
      this.postVolume({ volume: val });
      this.lastNetworkVolume = val;

      this.throttleTimer = setTimeout(() => {
        this.throttleTimer = null;
        if (this.pendingVolume !== null && this.pendingVolume !== this.lastNetworkVolume) {
          this.postVolume({ volume: this.pendingVolume });
          this.lastNetworkVolume = this.pendingVolume;
        }
      }, this.throttleInterval);

      tryUnref(this.throttleTimer);
    }
  }

  /**
   * Commit final volume on touch/pointer release.
   */
  handleSliderCommit(): void {
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
  postAction(body: Record<string, unknown>): Promise<unknown> {
    if (!this.fetchFn) return Promise.resolve();
    return this.fetchFn('api/video/action', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    }).catch((err) => {
      console.warn('[MirrormereHUD] Action failed:', err);
    });
  }

  postRemoteKey(key: string): Promise<unknown> {
    if (!this.fetchFn || !this.remoteUrl) return Promise.resolve();
    return this.fetchFn(`${this.remoteUrl}/key`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ key }),
    }).catch((err) => {
      console.warn('[MirrormereHUD] Remote key failed:', err);
    });
  }

  postDismiss(body: Record<string, unknown>): Promise<unknown> {
    if (!this.fetchFn) return Promise.resolve();
    return this.fetchFn('api/video/dismiss', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    }).catch((err) => {
      console.warn('[MirrormereHUD] Dismiss failed:', err);
    });
  }

  postMute(body: Record<string, unknown>): Promise<unknown> {
    if (!this.fetchFn) return Promise.resolve();
    return this.fetchFn('api/audio/mute', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    }).catch((err) => {
      console.warn('[MirrormereHUD] Mute toggle failed:', err);
    });
  }

  postVolume(body: Record<string, unknown>): Promise<unknown> {
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
  destroy(): void {
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
        el.removeEventListener(ev, fn as EventListener);
      }
    }
    this.boundHandlers = [];
  }
}

export interface HudProps {
  controllerRef?:
    | React.MutableRefObject<VideoHUDController | null>
    | React.RefCallback<VideoHUDController | null>;
  videoManager?: VideoHUDControllerOptions['videoManager'];
  remoteUrl?: string;
  stageElement?: HTMLElement | null;
}

/**
 * Hud React component rendering #video-hud-overlay and lifecycle controller.
 */
export function Hud({ controllerRef, videoManager, remoteUrl, stageElement }: HudProps = {}) {
  const overlayRef = useRef<HTMLDivElement>(null);
  const controllerInstanceRef = useRef<VideoHUDController | null>(null);
  const videoState = useDisplayStore((s) => s.video);
  const audioState = useDisplayStore((s) => s.audio);

  useEffect(() => {
    const overlay = overlayRef.current;
    if (!overlay) return;

    const resolvedStage =
      stageElement ||
      (overlay.closest<HTMLElement>('#video-stage') ??
        (typeof document !== 'undefined' ? document.getElementById('video-stage') : null));

    const controller = new VideoHUDController({
      overlayElement: overlay,
      stageElement: resolvedStage,
      videoManager,
      remoteUrl,
    });

    controllerInstanceRef.current = controller;

    if (typeof controllerRef === 'function') {
      controllerRef(controller);
    } else if (controllerRef) {
      controllerRef.current = controller;
    }

    if (typeof window !== 'undefined') {
      (window as unknown as { videoHUD?: VideoHUDController }).videoHUD = controller;
    }

    controller.handleAudioState(useDisplayStore.getState().audio);
    controller.handleVideoState(useDisplayStore.getState().video);

    return () => {
      controller.destroy();
      controllerInstanceRef.current = null;
      if (typeof controllerRef === 'function') {
        controllerRef(null);
      } else if (controllerRef) {
        controllerRef.current = null;
      }
      if (
        typeof window !== 'undefined' &&
        (window as unknown as { videoHUD?: VideoHUDController }).videoHUD === controller
      ) {
        delete (window as unknown as { videoHUD?: VideoHUDController }).videoHUD;
      }
    };
  }, [controllerRef, videoManager, remoteUrl, stageElement]);

  useEffect(() => {
    controllerInstanceRef.current?.handleVideoState(videoState);
  }, [videoState]);

  useEffect(() => {
    controllerInstanceRef.current?.handleAudioState(audioState);
  }, [audioState]);

  return (
    <div id="video-hud-overlay" className="video-hud-overlay" ref={overlayRef}>
      <div className="video-hud-header">
        {/* Unified horizontal capsule in upper left: Back + Home */}
        <div className="video-hud-capsule-top-left video-hud-nav-bar" id="video-hud-nav-bar">
          <button
            id="video-hud-back-btn"
            className="video-hud-btn video-hud-nav-btn"
            aria-label="Back"
            title="Back"
          >
            ‹
          </button>
          <button
            id="video-hud-home-btn"
            className="video-hud-btn video-hud-nav-btn"
            aria-label="Home"
            title="Home"
          >
            ⌂
          </button>
        </div>
        <span id="video-hud-title" className="video-hud-title">
          Live Video
        </span>
        {/* Unified vertical frosted pill in upper right: Dismiss + Volume + Mute */}
        <div className="video-hud-capsule-top-right video-hud-side-rail" id="video-hud-side-rail">
          <button
            id="video-hud-dismiss-btn"
            className="video-hud-btn video-hud-dismiss"
            aria-label="Dismiss Video"
            title="Dismiss Video"
          >
            ✕
          </button>
          <div className="video-hud-volume-container">
            <input
              type="range"
              id="video-hud-volume-slider"
              className="video-hud-volume-slider"
              min="0"
              max="100"
              defaultValue={75}
              aria-label="Master Volume"
            />
          </div>
          <button
            id="video-hud-mute-btn"
            className="video-hud-btn video-hud-mute"
            aria-label="Mute / Unmute"
            title="Mute / Unmute"
          >
            <span className="video-hud-icon" id="video-hud-mute-icon" data-icon="unmuted">
              🔊
            </span>
          </button>
        </div>
      </div>
      <button
        id="video-hud-play-btn"
        className="video-hud-btn video-hud-play video-hud-play-center"
        aria-label="Play / Pause"
        title="Play / Pause"
      >
        <span className="video-hud-icon" id="video-hud-play-icon" data-icon="pause">
          ⏸
        </span>
      </button>
      <div className="video-hud-controls video-hud-bottom-bar" id="video-hud-bottom-bar">
        <button
          id="video-hud-cc-btn"
          className="video-hud-btn video-hud-cc video-hud-media-btn"
          aria-label="Closed Captions"
          title="Closed Captions"
        >
          CC
        </button>
        <button
          id="video-hud-prev-btn"
          className="video-hud-btn video-hud-prev video-hud-media-btn"
          aria-label="Previous Episode"
          title="Previous Episode"
        >
          <svg viewBox="0 0 24 24" width="20" height="20" fill="currentColor" aria-hidden="true">
            <rect x="5" y="4" width="2.5" height="16" rx="1" />
            <polygon points="19 4 8.5 12 19 20" />
          </svg>
        </button>
        <button
          id="video-hud-rewind-btn"
          className="video-hud-btn video-hud-skip video-hud-media-btn"
          aria-label="Rewind 10 Seconds"
          title="Rewind 10 Seconds"
        >
          ↺ 10s
        </button>
        <div
          className="video-hud-progress-container"
          id="video-hud-progress-container"
          style={{ display: 'none' }}
        >
          <span className="video-hud-time video-hud-time-current" id="video-hud-time-current">
            0:00
          </span>
          <div className="video-hud-progress-track" id="video-hud-progress-track">
            <div className="video-hud-progress-fill" id="video-hud-progress-fill" style={{ width: '0%' }}>
              <div className="video-hud-progress-thumb" id="video-hud-progress-thumb" />
            </div>
          </div>
          <span className="video-hud-time video-hud-time-total" id="video-hud-time-total">
            0:00
          </span>
        </div>
        <button
          id="video-hud-forward-btn"
          className="video-hud-btn video-hud-skip video-hud-media-btn"
          aria-label="Fast Forward 10 Seconds"
          title="Fast Forward 10 Seconds"
        >
          ↻ 10s
        </button>
        <button
          id="video-hud-next-btn"
          className="video-hud-btn video-hud-next video-hud-media-btn"
          aria-label="Next Episode"
          title="Next Episode"
        >
          <svg viewBox="0 0 24 24" width="20" height="20" fill="currentColor" aria-hidden="true">
            <polygon points="5 4 15.5 12 5 20" />
            <rect x="16.5" y="4" width="2.5" height="16" rx="1" />
          </svg>
        </button>
      </div>
    </div>
  );
}

declare global {
  interface Window {
    videoHUD?: VideoHUDController;
    MirrormereHUD?: {
      VideoHUDController: typeof VideoHUDController;
    };
  }
}

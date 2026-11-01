import { useCallback, useEffect, useRef } from 'react';
import type { VideoSource, VideoStatePayload } from '../sse/types';
import { useAudioController } from '../audio/useAudio';
import { Hud, VideoHUDController } from './Hud';
import { useDisplayStore } from '../store';

function tryUnref(timer: unknown): void {
  if (!timer) return;
  const unrefable = timer as { unref?: () => void };
  if (typeof unrefable.unref === 'function') {
    unrefable.unref();
  }
}

export interface NormalizedCoordinates {
  x: number;
  y: number;
}

export type CoordinateEvent =
  | PointerEvent
  | MouseEvent
  | TouchEvent
  | {
      clientX?: number;
      clientY?: number;
      button?: number;
      touches?: ArrayLike<{ clientX: number; clientY: number }>;
      changedTouches?: ArrayLike<{ clientX: number; clientY: number }>;
      preventDefault?: () => void;
      stopPropagation?: () => void;
    };

/**
 * Maps DOM pointer/touch event coordinates into normalized [0.0, 1.0] video space,
 * compensating for letterboxing/pillarboxing with object-fit: contain.
 */
export function getNormalizedVideoCoordinates(
  e: CoordinateEvent,
  videoEl: HTMLVideoElement | HTMLImageElement | { getBoundingClientRect: () => DOMRect | { left: number; top: number; width: number; height: number }; videoWidth?: number; videoHeight?: number; naturalWidth?: number; naturalHeight?: number },
): NormalizedCoordinates | null {
  if (!videoEl || typeof videoEl.getBoundingClientRect !== 'function') return null;
  const rect = videoEl.getBoundingClientRect();
  if (rect.width <= 0 || rect.height <= 0) return null;

  let clientX: number | null = null;
  let clientY: number | null = null;
  if ('touches' in e && e.touches && e.touches.length > 0) {
    clientX = e.touches[0].clientX;
    clientY = e.touches[0].clientY;
  } else if ('changedTouches' in e && e.changedTouches && e.changedTouches.length > 0) {
    clientX = e.changedTouches[0].clientX;
    clientY = e.changedTouches[0].clientY;
  } else if ('clientX' in e && typeof e.clientX === 'number' && 'clientY' in e && typeof e.clientY === 'number') {
    clientX = e.clientX;
    clientY = e.clientY;
  }

  if (clientX === null || clientY === null) return null;

  let contentWidth = rect.width;
  let contentHeight = rect.height;
  let contentLeft = rect.left;
  let contentTop = rect.top;

  const vWidth =
    'videoWidth' in videoEl && typeof videoEl.videoWidth === 'number' && videoEl.videoWidth > 0
      ? videoEl.videoWidth
      : 'naturalWidth' in videoEl && typeof videoEl.naturalWidth === 'number' && videoEl.naturalWidth > 0
        ? videoEl.naturalWidth
        : 0;

  const vHeight =
    'videoHeight' in videoEl && typeof videoEl.videoHeight === 'number' && videoEl.videoHeight > 0
      ? videoEl.videoHeight
      : 'naturalHeight' in videoEl && typeof videoEl.naturalHeight === 'number' && videoEl.naturalHeight > 0
        ? videoEl.naturalHeight
        : 0;

  // Compensate for letterboxing or pillarboxing with object-fit: contain
  if (vWidth > 0 && vHeight > 0) {
    const videoRatio = vWidth / vHeight;
    const elementRatio = rect.width / rect.height;

    if (elementRatio > videoRatio) {
      // Pillarboxed: black bars on left and right
      contentWidth = rect.height * videoRatio;
      contentLeft = rect.left + (rect.width - contentWidth) / 2;
    } else {
      // Letterboxed: black bars on top and bottom
      contentHeight = rect.width / videoRatio;
      contentTop = rect.top + (rect.height - contentHeight) / 2;
    }
  }

  if (contentWidth <= 0 || contentHeight <= 0) return null;

  const relX = (clientX - contentLeft) / contentWidth;
  const relY = (clientY - contentTop) / contentHeight;

  // Clamp coordinates to [0.0, 1.0]
  return {
    x: Math.max(0.0, Math.min(1.0, relX)),
    y: Math.max(0.0, Math.min(1.0, relY)),
  };
}

interface HlsInstance {
  loadSource: (url: string) => void;
  attachMedia: (video: HTMLVideoElement) => void;
  on: (event: string, callback: (event: string, data?: { fatal?: boolean }) => void) => void;
  destroy?: () => void;
}

interface HlsConstructor {
  new (): HlsInstance;
  isSupported?: () => boolean;
  Events?: { ERROR: string };
}

export interface VideoSession {
  stream: VideoSource;
  container: HTMLElement;
  isPip: boolean;
  element: HTMLVideoElement | HTMLImageElement;
  type: 'webrtc' | 'hls' | 'mjpeg';
  token: number;
  cancelled: boolean;
  spinner?: HTMLElement | null;
  dismissSpinner?: () => void;
  spinnerSafetyTimer?: ReturnType<typeof setTimeout> | null;
  pc?: RTCPeerConnection | null;
  hls?: HlsInstance | null;
  reconnectAttempts?: number;
  reconnectTimer?: ReturnType<typeof setTimeout> | null;
  disconnectTimer?: ReturnType<typeof setTimeout> | null;
  touchCleanups?: Array<() => void>;
}

export interface AudioManagerLike {
  registerMediaElement?: (el: HTMLMediaElement) => void;
  unregisterMediaElement?: (el: HTMLMediaElement) => void;
}

export interface CarouselLike {
  pause?: () => void;
  resume?: () => void;
}

export interface HUDLike {
  hideHUD?: () => void;
  syncStream?: () => void;
  handleVideoState?: (data?: VideoStatePayload | null) => void;
  lastVideoState?: VideoStatePayload | null;
}

export interface VideoPlayerManagerOptions {
  stageElement?: HTMLElement | null;
  primarySlot?: HTMLElement | null;
  pipSlot?: HTMLElement | null;
  audioManager?: AudioManagerLike | null;
  carousel?: CarouselLike | null;
  hud?: HUDLike | null;
  window?: Window | null;
  remoteUrl?: string;
  touchForwardingEnabled?: boolean;
  fetch?: typeof fetch;
  RTCPeerConnection?: typeof RTCPeerConnection;
  negotiationTimeout?: number;
  disconnectTimeout?: number;
  maxReconnectAttempts?: number;
  reconnectBaseDelay?: number;
  reconnectBackoffFactor?: number;
  onFallback?: (session: VideoSession, fallbackTarget: 'widgets' | 'pip_dismissed') => void;
}

export interface VideoPlayerManagerState {
  mode: 'widgets' | 'video';
  isSwapped: boolean;
  primary: VideoSource | null;
  pip: VideoSource | null;
  hasPrimarySession: boolean;
  hasPipSession: boolean;
}

/**
 * VideoPlayerManager coordinates the video presentation stage and stream playback.
 */
export class VideoPlayerManager {
  public options: VideoPlayerManagerOptions;
  public stage: HTMLElement | null;
  public primarySlot: HTMLElement | null;
  public pipSlot: HTMLElement | null;
  public audioManager: AudioManagerLike | null;
  public carousel: CarouselLike | null;
  public hud: HUDLike | null;
  public remoteUrl: string;
  public touchForwardingEnabled: boolean;
  public fetchFn: typeof fetch | null;
  public PeerConnectionClass: typeof RTCPeerConnection | null;

  public currentMode: 'widgets' | 'video';
  public isSwapped: boolean;
  public mountEpoch: number;
  public sessionTokenSeq: number;

  public serverPrimary: VideoSource | null;
  public serverPip: VideoSource | null;

  public primarySession: VideoSession | null;
  public pipSession: VideoSession | null;

  public paintFlushTimer: ReturnType<typeof requestAnimationFrame> | ReturnType<typeof setTimeout> | null;
  public heartbeatElement: HTMLElement | null;
  public slotClickCleanups: Array<() => void>;

  constructor(options: VideoPlayerManagerOptions = {}) {
    this.options = options;
    this.stage =
      options.stageElement ||
      (typeof document !== 'undefined' ? document.getElementById('video-stage') : null);
    this.primarySlot =
      options.primarySlot ||
      (typeof document !== 'undefined' ? document.getElementById('video-primary-slot') : null);
    this.pipSlot =
      options.pipSlot ||
      (typeof document !== 'undefined' ? document.getElementById('video-pip-slot') : null);
    this.audioManager =
      options.audioManager ||
      (typeof window !== 'undefined' ? (window.audioManager as unknown as AudioManagerLike) : null);
    this.carousel =
      options.carousel ||
      (typeof window !== 'undefined' ? (window as unknown as { carousel?: CarouselLike }).carousel || null : null);
    this.hud =
      options.hud ||
      (typeof window !== 'undefined' ? (window.videoHUD as unknown as HUDLike) || null : null);

    const win = options.window || (typeof window !== 'undefined' ? window : null);
    const defaultRemoteUrl =
      (win && (win as unknown as { MIRRORMERE_REMOTE_URL?: string }).MIRRORMERE_REMOTE_URL) ||
      (typeof document !== 'undefined' &&
        document.body &&
        document.body.dataset &&
        document.body.dataset.remoteUrl) ||
      '';
    this.remoteUrl = options.remoteUrl !== undefined ? options.remoteUrl : defaultRemoteUrl;
    this.touchForwardingEnabled =
      options.touchForwardingEnabled !== undefined ? options.touchForwardingEnabled : true;

    const defaultFetch =
      typeof window !== 'undefined' && typeof window.fetch === 'function'
        ? window.fetch.bind(window)
        : typeof fetch === 'function'
          ? fetch
          : null;
    this.fetchFn = options.fetch || defaultFetch;
    this.PeerConnectionClass =
      options.RTCPeerConnection || (typeof RTCPeerConnection !== 'undefined' ? RTCPeerConnection : null);

    this.currentMode = 'widgets';
    this.isSwapped = false;
    this.mountEpoch = 0;
    this.sessionTokenSeq = 0;

    this.serverPrimary = null;
    this.serverPip = null;

    this.primarySession = null;
    this.pipSession = null;

    this.paintFlushTimer = null;
    this.heartbeatElement = null;
    this.slotClickCleanups = [];

    this.bindSlotGestures();
    this.ensureCompositorHeartbeat();
  }

  /**
   * Bind touch/click gestures on PiP and primary slots for interactive swap-on-tap.
   */
  bindSlotGestures(): void {
    if (this.pipSlot) {
      const onPipClick = (e: Event): void => {
        if (!this.isSwapped) {
          e.stopPropagation();
          this.handleSlotClick('pip');
        }
      };
      this.pipSlot.addEventListener('click', onPipClick);
      this.slotClickCleanups.push(() => {
        this.pipSlot?.removeEventListener('click', onPipClick);
      });
    }

    if (this.primarySlot) {
      const onPrimaryClick = (e: Event): void => {
        // If in swapped state, the primarySlot is rendered visually as the corner PiP dock
        if (this.isSwapped) {
          e.stopPropagation();
          this.handleSlotClick('primary');
        }
      };
      this.primarySlot.addEventListener('click', onPrimaryClick);
      this.slotClickCleanups.push(() => {
        this.primarySlot?.removeEventListener('click', onPrimaryClick);
      });
    }
  }

  /**
   * Handle slot tap interaction for zero-reparenting stream swapping.
   */
  handleSlotClick(clickedSlot: 'primary' | 'pip'): void {
    if (!this.serverPip || !this.pipSession || !this.primarySession) {
      return; // Nothing to swap if PiP is not active
    }

    if (!this.isSwapped && clickedSlot === 'pip') {
      this.swapStreams();
    } else if (this.isSwapped && clickedSlot === 'primary') {
      this.swapStreams();
    }
  }

  /**
   * Connect or update HUD controller reference.
   */
  setHUD(hud: HUDLike | null): void {
    this.hud = hud;
    if (this.currentMode === 'video' && hud && typeof hud.syncStream === 'function') {
      hud.syncStream();
    }
  }

  /**
   * Swaps the active presentation and audio focus between primary and PiP streams
   * without reparenting DOM nodes or dropping WebRTC/MSE playback.
   */
  swapStreams(): void {
    if (!this.primarySession || !this.pipSession) {
      return;
    }

    this.isSwapped = !this.isSwapped;

    if (this.stage) {
      this.stage.dataset.swapped = this.isSwapped ? 'true' : 'false';
    }

    this.syncAudioFocus();

    if (this.hud && typeof this.hud.syncStream === 'function') {
      this.hud.syncStream();
    }
  }

  /**
   * Synchronize audio focus strictly:
   * - The presentation Primary stream is unmuted and registered with AudioManager.
   * - The presentation PiP stream is strictly muted, volume 0, and unregistered from AudioManager.
   */
  syncAudioFocus(): void {
    const activePrimarySession = this.isSwapped ? this.pipSession : this.primarySession;
    const activePipSession = this.isSwapped ? this.primarySession : this.pipSession;

    // 1. Configure active PiP stream (strictly muted)
    if (activePipSession && activePipSession.element) {
      if (activePipSession.type !== 'mjpeg') {
        const mediaEl = activePipSession.element as HTMLMediaElement;
        if (this.audioManager && typeof this.audioManager.unregisterMediaElement === 'function') {
          this.audioManager.unregisterMediaElement(mediaEl);
        }
        mediaEl.muted = true;
        mediaEl.volume = 0;
      }
    }

    // 2. Configure active Primary stream (unmuted & audioManager registered)
    if (activePrimarySession && activePrimarySession.element) {
      if (activePrimarySession.type !== 'mjpeg') {
        const mediaEl = activePrimarySession.element as HTMLMediaElement;
        mediaEl.muted = false;
        if (this.audioManager && typeof this.audioManager.registerMediaElement === 'function') {
          this.audioManager.registerMediaElement(mediaEl);
        }
      }
    }
  }

  /**
   * Process incoming video.state SSE events.
   */
  handleVideoState(data?: VideoStatePayload | null): void {
    if (!data) return;

    this.mountEpoch++;
    const mode = data.mode || (data.primary ? 'video' : 'widgets');

    if (mode === 'video' && data.primary) {
      this.enterVideoMode(data);
    } else if (this.currentMode === 'video' || this.primarySession || this.pipSession) {
      this.exitVideoMode();
    }
  }

  /**
   * Transition into video presentation mode and mount/reconcile active streams.
   */
  enterVideoMode(data: VideoStatePayload): void {
    this.currentMode = 'video';

    // 1. Pause carousel background processing
    if (this.carousel && typeof this.carousel.pause === 'function') {
      this.carousel.pause();
    }
    try {
      useDisplayStore.getState().pause();
    } catch {
      // ignore
    }

    // 2. Reveal video stage
    if (this.stage) {
      this.stage.style.display = 'flex';
      this.stage.classList.add('active');
    }

    // 4. Reconcile PiP slot
    if (!data.pip) {
      // PiP stream was dismissed or absent
      if (this.isSwapped) {
        this.isSwapped = false;
        if (this.stage) this.stage.dataset.swapped = 'false';
      }
      if (this.pipSession) {
        this.teardownSession(this.pipSession);
        this.pipSession = null;
      }
      this.serverPip = null;
      if (this.pipSlot) {
        this.pipSlot.style.display = 'none';
      }
    } else {
      if (this.pipSlot) {
        this.pipSlot.style.display = 'flex';
      }
    }

    // 5. Reconcile Primary stream mount
    if (data.primary) {
      if (this.primarySlot) {
        this.primarySlot.style.display = '';
      }
      if (
        !this.primarySession ||
        this.serverPrimary?.id !== data.primary.id ||
        this.serverPrimary?.stream_url !== data.primary.stream_url
      ) {
        if (this.primarySession) {
          this.teardownSession(this.primarySession);
          this.primarySession = null;
        }
        if (this.primarySlot) {
          this.primarySession = this.mountStream(data.primary, this.primarySlot, false);
        }
      } else {
        // Update properties if changed
        this.primarySession.stream = { ...this.primarySession.stream, ...data.primary };
      }
      this.serverPrimary = { ...data.primary };
    }

    // 6. Reconcile PiP stream mount
    if (data.pip) {
      if (
        !this.pipSession ||
        this.serverPip?.id !== data.pip.id ||
        this.serverPip?.stream_url !== data.pip.stream_url
      ) {
        if (this.pipSession) {
          this.teardownSession(this.pipSession);
          this.pipSession = null;
        }
        if (this.pipSlot) {
          this.pipSession = this.mountStream(data.pip, this.pipSlot, true);
        }
      } else {
        this.pipSession.stream = { ...this.pipSession.stream, ...data.pip };
      }
      this.serverPip = { ...data.pip };
    }

    // 7. Enforce audio focus
    this.syncAudioFocus();

    // 8. Synchronize HUD with updated stream state
    if (this.hud && typeof this.hud.syncStream === 'function') {
      this.hud.syncStream();
    }
  }

  /**
   * Transition back to widgets mode and clean up all video sessions.
   */
  exitVideoMode(flushPaint: boolean = true): void {
    this.currentMode = 'widgets';
    this.isSwapped = false;
    this.serverPrimary = null;
    this.serverPip = null;

    if (this.hud) {
      if (typeof this.hud.handleVideoState === 'function') {
        this.hud.handleVideoState(null);
      } else {
        this.hud.lastVideoState = null;
      }
      if (typeof this.hud.hideHUD === 'function') {
        this.hud.hideHUD();
      }
      if (typeof this.hud.syncStream === 'function') {
        this.hud.syncStream();
      }
    }

    if (this.stage) {
      this.stage.dataset.swapped = 'false';
      this.stage.classList.remove('active');
      this.stage.style.display = 'none';
    }

    if (this.primarySlot) {
      this.primarySlot.style.display = '';
    }

    if (this.pipSlot) {
      this.pipSlot.style.display = 'none';
    }

    // 1. Cleanly teardown media sessions
    if (this.primarySession) {
      this.teardownSession(this.primarySession);
      this.primarySession = null;
    }
    if (this.pipSession) {
      this.teardownSession(this.pipSession);
      this.pipSession = null;
    }

    // 2. Resume carousel processing
    if (this.carousel && typeof this.carousel.resume === 'function') {
      this.carousel.resume();
    }
    try {
      useDisplayStore.getState().resume();
    } catch {
      // ignore
    }

    // 3. Reset display store video slice if still in video mode
    if (flushPaint) {
      try {
        const cur = useDisplayStore.getState().video;
        if (cur?.mode === 'video' || cur?.primary !== null) {
          useDisplayStore.setState({ video: { mode: 'widgets', primary: null, pip: null } });
        }
      } catch {
        // ignore
      }
      this.flushCompositorPaint();
    }
  }

  /**
   * Ensures the compositor keep-alive heartbeat DOM element exists.
   * Prevents Wayland/Cage output frame starvation on static views.
   */
  ensureCompositorHeartbeat(): HTMLElement | null {
    if (typeof document === 'undefined') return null;
    let el = document.getElementById('mm-compositor-heartbeat');
    if (!el && document.body && typeof document.body.appendChild === 'function') {
      el = document.createElement('div');
      el.id = 'mm-compositor-heartbeat';
      el.className = 'mm-compositor-heartbeat';
      if (typeof el.setAttribute === 'function') {
        el.setAttribute('aria-hidden', 'true');
      }
      document.body.appendChild(el);
    }
    this.heartbeatElement = el;
    return el;
  }

  /**
   * Explicitly forces a layout reflow and multi-frame damage commit.
   * Guarantees that Chromium Ozone Wayland commits a buffer with damage
   * to satisfy wlroots/Cage frame callback expectations on stream teardown.
   */
  flushCompositorPaint(): void {
    if (typeof document === 'undefined') return;

    // 1. Force synchronous layout reflow pass
    if (document.body && typeof document.body.offsetHeight === 'number') {
      void document.body.offsetHeight;
    }

    const hb = this.heartbeatElement || this.ensureCompositorHeartbeat();

    // 2. Schedule multi-frame compositor damage burst
    const rAF =
      typeof window !== 'undefined' && typeof window.requestAnimationFrame === 'function'
        ? window.requestAnimationFrame.bind(window)
        : typeof requestAnimationFrame === 'function'
          ? requestAnimationFrame
          : (cb: FrameRequestCallback) => setTimeout(cb, 16);

    const cancelRAF =
      typeof window !== 'undefined' && typeof window.cancelAnimationFrame === 'function'
        ? window.cancelAnimationFrame.bind(window)
        : typeof cancelAnimationFrame === 'function'
          ? cancelAnimationFrame
          : clearTimeout;

    if (this.paintFlushTimer) {
      cancelRAF(this.paintFlushTimer as number);
      this.paintFlushTimer = null;
    }

    let frameCount = 0;
    const step = (): void => {
      frameCount++;
      if (hb) {
        if (hb.dataset) {
          hb.dataset.flush = String(frameCount);
        }
        if (hb.style) {
          hb.style.transform = frameCount % 2 === 1 ? 'translateZ(0) scale(1.001)' : 'translateZ(0) scale(1)';
        }
      }
      if (document.body && typeof document.body.offsetHeight === 'number') {
        void document.body.offsetHeight;
      }

      if (frameCount < 2) {
        this.paintFlushTimer = rAF(step);
      } else {
        this.paintFlushTimer = null;
      }
    };

    this.paintFlushTimer = rAF(step);
  }

  /**
   * Mounts a stream into the target slot container according to its protocol triad type.
   */
  mountStream(stream: VideoSource, container: HTMLElement, isPip: boolean): VideoSession | null {
    if (!container) return null;
    container.innerHTML = '';

    const type = (stream.type || 'webrtc').toLowerCase();
    let session: VideoSession;

    if (type === 'mjpeg') {
      session = this.mountMJPEG(stream, container, isPip);
    } else if (type === 'hls') {
      session = this.mountHLS(stream, container, isPip);
    } else {
      // Default to WebRTC
      session = this.mountWebRTC(stream, container, isPip);
    }

    this.bindTouchForwarding(session);

    return session;
  }

  /**
   * Attaches cyberpunk stream loading spinner to primary video stage container.
   */
  attachStreamSpinner(
    session: VideoSession,
    container: HTMLElement,
    mediaElement: HTMLMediaElement | HTMLImageElement,
    isPip: boolean,
  ): void {
    if (isPip || !container || typeof document === 'undefined') return;

    const spinner = document.createElement('div');
    spinner.className = 'video-stream-spinner';
    spinner.innerHTML =
      '<div class="video-spinner-ring"></div><span class="video-spinner-text">Waking Stream...</span>';
    container.appendChild(spinner);
    session.spinner = spinner;

    const dismissSpinner = (): void => {
      if (session.spinnerSafetyTimer) {
        clearTimeout(session.spinnerSafetyTimer);
        session.spinnerSafetyTimer = null;
      }
      if (!session.spinner || session.spinner.classList.contains('fade-out')) return;
      session.spinner.classList.add('fade-out');
      const removeTimer = setTimeout(() => {
        if (session.spinner && session.spinner.parentNode) {
          session.spinner.parentNode.removeChild(session.spinner);
        }
      }, 400);
      tryUnref(removeTimer);
    };
    session.dismissSpinner = dismissSpinner;

    if (mediaElement && typeof mediaElement.addEventListener === 'function') {
      if (session.type === 'mjpeg') {
        mediaElement.addEventListener('load', dismissSpinner);
        mediaElement.addEventListener('error', dismissSpinner);
      } else {
        mediaElement.addEventListener('playing', dismissSpinner);
        mediaElement.addEventListener('loadeddata', dismissSpinner);
      }
    }

    session.spinnerSafetyTimer = setTimeout(dismissSpinner, 12000);
    tryUnref(session.spinnerSafetyTimer);
  }

  /**
   * Mounts MJPEG stream via responsive <img> element.
   */
  mountMJPEG(stream: VideoSource, container: HTMLElement, isPip: boolean = false): VideoSession {
    const img = document.createElement('img');
    img.className = 'video-stream-media';
    img.alt = `Stream ${stream.id}`;
    img.src = stream.stream_url;

    container.appendChild(img);
    const session: VideoSession = {
      stream,
      container,
      isPip: Boolean(isPip),
      element: img,
      type: 'mjpeg',
      token: ++this.sessionTokenSeq,
      cancelled: false,
    };

    this.attachStreamSpinner(session, container, img, isPip);

    return session;
  }

  /**
   * Mounts HLS stream via native video or hls.js fallback.
   */
  mountHLS(stream: VideoSource, container: HTMLElement, isPip: boolean = false): VideoSession {
    const video = document.createElement('video');
    video.className = 'video-stream-media';
    video.autoplay = true;
    video.playsInline = true;
    video.muted = isPip;
    video.style.touchAction = 'none';
    if (isPip) video.volume = 0;

    let hlsInstance: HlsInstance | null = null;
    const Hls =
      typeof window !== 'undefined'
        ? (window as unknown as { Hls?: HlsConstructor }).Hls
        : undefined;

    if (Hls && typeof Hls.isSupported === 'function' && Hls.isSupported()) {
      hlsInstance = new Hls();
      hlsInstance.loadSource(stream.stream_url);
      hlsInstance.attachMedia(video);
      const errorEvent = Hls.Events?.ERROR ?? 'hlsError';
      hlsInstance.on(errorEvent, (_event: string, data?: { fatal?: boolean }) => {
        if (data && data.fatal) {
          console.warn('[MirrormereVideo] Fatal HLS error encountered:', data);
          if (typeof hlsInstance?.destroy === 'function') hlsInstance.destroy();
        }
      });
    } else if (typeof video.canPlayType === 'function' && video.canPlayType('application/vnd.apple.mpegurl')) {
      video.src = stream.stream_url;
    } else {
      console.warn('[MirrormereVideo] HLS playback not natively supported and hls.js is absent');
      video.src = stream.stream_url;
    }

    container.appendChild(video);
    this.safePlay(video);

    const session: VideoSession = {
      stream,
      container,
      isPip: Boolean(isPip),
      element: video,
      hls: hlsInstance,
      type: 'hls',
      token: ++this.sessionTokenSeq,
      cancelled: false,
    };

    this.attachStreamSpinner(session, container, video, isPip);

    return session;
  }

  /**
   * Mounts WebRTC stream using RTCPeerConnection and SDP offer/answer exchange.
   */
  mountWebRTC(stream: VideoSource, container: HTMLElement, isPip: boolean = false): VideoSession {
    const video = document.createElement('video');
    video.className = 'video-stream-media';
    video.autoplay = true;
    video.playsInline = true;
    video.muted = isPip;
    video.style.touchAction = 'none';
    if (isPip) video.volume = 0;

    container.appendChild(video);

    const session: VideoSession = {
      stream,
      container,
      isPip: Boolean(isPip),
      element: video,
      pc: null,
      type: 'webrtc',
      token: ++this.sessionTokenSeq,
      cancelled: false,
      reconnectAttempts: 0,
      reconnectTimer: null,
      disconnectTimer: null,
    };

    this.attachStreamSpinner(session, container, video, isPip);

    if (this.PeerConnectionClass) {
      try {
        const pc = new this.PeerConnectionClass();
        session.pc = pc;

        if (typeof pc.addTransceiver === 'function') {
          const videoTransceiver = pc.addTransceiver('video', { direction: 'recvonly' });
          pc.addTransceiver('audio', { direction: 'recvonly' });
          if (videoTransceiver && videoTransceiver.receiver) {
            this.applyZeroLatencyPlayoutDelay(videoTransceiver.receiver, 'video');
          }
        }

        pc.ontrack = (event: RTCTrackEvent) => {
          if (session.cancelled) return;
          if (event.receiver && event.track && event.track.kind === 'video') {
            this.applyZeroLatencyPlayoutDelay(event.receiver, 'video');
          }
          if (event.streams && event.streams[0]) {
            video.srcObject = event.streams[0];
          } else {
            const MediaStreamConstructor =
              typeof MediaStream !== 'undefined'
                ? MediaStream
                : typeof window !== 'undefined'
                  ? window.MediaStream
                  : null;
            if (!video.srcObject && MediaStreamConstructor) {
              video.srcObject = new MediaStreamConstructor();
            }
            if (
              video.srcObject &&
              typeof (video.srcObject as MediaStream).addTrack === 'function' &&
              event.track
            ) {
              (video.srcObject as MediaStream).addTrack(event.track);
            }
          }
        };

        const onConnChange = (): void => {
          if (session.pc) {
            const cs = session.pc.connectionState;
            const ics = session.pc.iceConnectionState;
            if (cs === 'failed' || cs === 'disconnected' || ics === 'failed' || ics === 'disconnected') {
              if (typeof session.dismissSpinner === 'function') {
                session.dismissSpinner();
              }
            }
          }
          this.handleWebRTCConnectionChange(session);
        };
        pc.oniceconnectionstatechange = onConnChange;
        pc.onconnectionstatechange = onConnChange;

        void this.negotiateWebRTC(session, stream.stream_url);
      } catch (err: unknown) {
        console.warn('[MirrormereVideo] RTCPeerConnection initialization error:', err);
        if (!session.cancelled) {
          this.scheduleWebRTCReconnect(session);
        }
      }
    }

    this.safePlay(video);
    return session;
  }

  /**
   * Minimizes WebRTC receiver jitter buffer delay for ultra-low latency interactive streaming.
   * Restricts zero-delay hint strictly to video streams to prevent audio buffer starvation/crackling.
   */
  applyZeroLatencyPlayoutDelay(receiver?: RTCRtpReceiver | null, kind: 'video' | 'audio' = 'video'): void {
    if (!receiver || kind !== 'video') return;
    try {
      if ('playoutDelayHint' in receiver) {
        (receiver as { playoutDelayHint?: number }).playoutDelayHint = 0;
      }
    } catch {
      // Non-fatal if browser blocks or throws on playoutDelayHint
    }
  }

  /**
   * Performs async WebRTC offer/answer SDP negotiation with go2rtc endpoint.
   */
  async negotiateWebRTC(session: VideoSession, streamUrl: string): Promise<void> {
    const pc = session ? session.pc : null;
    if (!pc) return;

    let timeoutId: ReturnType<typeof setTimeout> | null = null;
    try {
      const offer = await pc.createOffer();
      if (session.cancelled) {
        if (session.pc === pc) session.pc = null;
        pc.oniceconnectionstatechange = null;
        pc.onconnectionstatechange = null;
        pc.ontrack = null;
        pc.onicecandidate = null;
        if (typeof pc.close === 'function') pc.close();
        return;
      }

      await pc.setLocalDescription(offer);
      if (session.cancelled) {
        if (session.pc === pc) session.pc = null;
        pc.oniceconnectionstatechange = null;
        pc.onconnectionstatechange = null;
        pc.ontrack = null;
        pc.onicecandidate = null;
        if (typeof pc.close === 'function') pc.close();
        return;
      }

      // Wait for ICE gathering completion or 500ms safety timeout
      await new Promise<void>((resolve) => {
        if (pc.iceGatheringState === 'complete') return resolve();
        let settled = false;
        let iceTimer: ReturnType<typeof setTimeout> | null = null;

        const cleanup = (): void => {
          if (iceTimer) {
            clearTimeout(iceTimer);
            iceTimer = null;
          }
          if (typeof pc.removeEventListener === 'function') {
            pc.removeEventListener('icegatheringstatechange', check);
          }
        };

        const check = (): void => {
          if (pc.iceGatheringState === 'complete' && !settled) {
            settled = true;
            cleanup();
            resolve();
          }
        };

        if (typeof pc.addEventListener === 'function') {
          pc.addEventListener('icegatheringstatechange', check);
        }

        iceTimer = setTimeout(() => {
          if (!settled) {
            settled = true;
            cleanup();
            resolve();
          }
        }, 500);
        tryUnref(iceTimer);
      });

      if (session.cancelled) {
        if (session.pc === pc) session.pc = null;
        pc.oniceconnectionstatechange = null;
        pc.onconnectionstatechange = null;
        pc.ontrack = null;
        pc.onicecandidate = null;
        if (typeof pc.close === 'function') pc.close();
        return;
      }

      const sdpPayload = pc.localDescription ? pc.localDescription.sdp : offer.sdp;
      const fetchFn = this.fetchFn || (typeof fetch === 'function' ? fetch : null);
      if (!fetchFn) return;

      const controller = typeof AbortController !== 'undefined' ? new AbortController() : null;
      const timeoutMs =
        typeof this.options.negotiationTimeout === 'number' ? this.options.negotiationTimeout : 5000;
      if (controller) {
        timeoutId = setTimeout(() => controller.abort(), timeoutMs);
        tryUnref(timeoutId);
      }

      const res = await fetchFn(streamUrl, {
        method: 'POST',
        headers: { 'Content-Type': 'application/sdp' },
        body: sdpPayload,
        signal: controller ? controller.signal : undefined,
      });

      if (timeoutId) {
        clearTimeout(timeoutId);
        timeoutId = null;
      }

      if (session.cancelled) {
        if (session.pc === pc) session.pc = null;
        pc.oniceconnectionstatechange = null;
        pc.onconnectionstatechange = null;
        pc.ontrack = null;
        pc.onicecandidate = null;
        if (typeof pc.close === 'function') pc.close();
        return;
      }

      if (!res.ok) {
        throw new Error(`HTTP ${res.status}`);
      }

      const text = await res.text();
      if (session.cancelled) {
        if (session.pc === pc) session.pc = null;
        pc.oniceconnectionstatechange = null;
        pc.onconnectionstatechange = null;
        pc.ontrack = null;
        pc.onicecandidate = null;
        if (typeof pc.close === 'function') pc.close();
        return;
      }

      let answerSDP = text;
      try {
        const json = JSON.parse(text) as { sdp?: string };
        if (json && typeof json.sdp === 'string') {
          answerSDP = json.sdp;
        }
      } catch {
        // Payload is raw SDP text
      }

      await pc.setRemoteDescription({ type: 'answer', sdp: answerSDP });
      if (typeof pc.getReceivers === 'function') {
        for (const receiver of pc.getReceivers()) {
          if (receiver && receiver.track && receiver.track.kind === 'video') {
            this.applyZeroLatencyPlayoutDelay(receiver, 'video');
          }
        }
      }
    } catch (err: unknown) {
      if (timeoutId) {
        clearTimeout(timeoutId);
      }
      if (!session.cancelled) {
        console.warn('[MirrormereVideo] WebRTC SDP negotiation failed:', err);
        if (session.pc === pc) session.pc = null;
        pc.oniceconnectionstatechange = null;
        pc.onconnectionstatechange = null;
        pc.ontrack = null;
        pc.onicecandidate = null;
        if (typeof pc.close === 'function') pc.close();
        this.scheduleWebRTCReconnect(session);
      }
    }
  }

  /**
   * Watches RTCPeerConnection and ICE connection state changes for stream health.
   */
  handleWebRTCConnectionChange(session: VideoSession): void {
    if (!session || session.cancelled || !session.pc) return;

    const connState = session.pc.connectionState;
    const iceState = session.pc.iceConnectionState;

    // 1. Healthy connected state: reset retry count and cancel active timers
    if (connState === 'connected' || iceState === 'connected' || iceState === 'completed') {
      if (session.disconnectTimer) {
        clearTimeout(session.disconnectTimer);
        session.disconnectTimer = null;
      }
      if (session.reconnectTimer) {
        clearTimeout(session.reconnectTimer);
        session.reconnectTimer = null;
      }
      session.reconnectAttempts = 0;
      return;
    }

    // 2. Active renegotiation in progress: clear disconnect timer to avoid racing
    if (connState === 'connecting' || iceState === 'checking') {
      if (session.disconnectTimer) {
        clearTimeout(session.disconnectTimer);
        session.disconnectTimer = null;
      }
      return;
    }

    // 3. Definite failure: trigger reconnect immediately
    if (connState === 'failed' || iceState === 'failed') {
      if (typeof session.dismissSpinner === 'function') {
        session.dismissSpinner();
      }
      if (session.disconnectTimer) {
        clearTimeout(session.disconnectTimer);
        session.disconnectTimer = null;
      }
      this.scheduleWebRTCReconnect(session);
      return;
    }

    // 4. Transient disconnect: arm grace period before tearing down
    if (connState === 'disconnected' || iceState === 'disconnected') {
      if (typeof session.dismissSpinner === 'function') {
        session.dismissSpinner();
      }
      if (!session.disconnectTimer) {
        const timeout =
          typeof this.options.disconnectTimeout === 'number' ? this.options.disconnectTimeout : 2000;
        session.disconnectTimer = setTimeout(() => {
          session.disconnectTimer = null;
          if (session.cancelled) return;
          this.scheduleWebRTCReconnect(session);
        }, timeout);
        tryUnref(session.disconnectTimer);
      }
      return;
    }

    // 5. Unexpected close
    if ((connState === 'closed' || iceState === 'closed') && !session.cancelled) {
      this.scheduleWebRTCReconnect(session);
    }
  }

  /**
   * Schedules auto-reconnection with exponential backoff or triggers fallback if retries exhausted.
   */
  scheduleWebRTCReconnect(session: VideoSession): void {
    if (!session || session.cancelled || session.reconnectTimer) return;

    const maxAttempts =
      typeof this.options.maxReconnectAttempts === 'number' ? this.options.maxReconnectAttempts : 3;
    if ((session.reconnectAttempts ?? 0) >= maxAttempts) {
      console.warn(
        `[MirrormereVideo] WebRTC stream ${session.stream?.id} reconnect attempts exhausted (${maxAttempts}). Falling back.`,
      );
      this.handleStreamFallback(session);
      return;
    }

    session.reconnectAttempts = (session.reconnectAttempts ?? 0) + 1;
    const baseDelay =
      typeof this.options.reconnectBaseDelay === 'number' ? this.options.reconnectBaseDelay : 1000;
    const factor =
      typeof this.options.reconnectBackoffFactor === 'number' ? this.options.reconnectBackoffFactor : 1.5;
    const delay = Math.round(baseDelay * Math.pow(factor, session.reconnectAttempts - 1));

    session.reconnectTimer = setTimeout(async () => {
      session.reconnectTimer = null;
      if (session.cancelled) return;
      await this.reconnectWebRTC(session);
    }, delay);
    tryUnref(session.reconnectTimer);
  }

  /**
   * Closes dead peer connection, flushes hardware decode buffer, and establishes fresh WebRTC session.
   */
  async reconnectWebRTC(session: VideoSession): Promise<void> {
    if (!session || session.cancelled) return;

    // 1. Tear down old peer connection and unbind handlers to avoid leaks
    if (session.pc) {
      const pc = session.pc;
      session.pc = null;
      pc.oniceconnectionstatechange = null;
      pc.onconnectionstatechange = null;
      pc.ontrack = null;
      pc.onicecandidate = null;
      if (typeof pc.close === 'function') {
        pc.close();
      }
    }

    // 2. Explicitly flush hardware decode frame buffer to prevent VAAPI/EGL kernel D-state lockup
    if (session.element) {
      const videoEl = session.element as HTMLVideoElement;
      videoEl.srcObject = null;
      if (typeof videoEl.load === 'function') {
        try {
          videoEl.load();
        } catch {
          // ignore
        }
      }
    }

    if (session.cancelled || !this.PeerConnectionClass) return;

    // 3. Create fresh RTCPeerConnection and re-bind event listeners
    try {
      const pc = new this.PeerConnectionClass();
      session.pc = pc;

      if (typeof pc.addTransceiver === 'function') {
        const videoTransceiver = pc.addTransceiver('video', { direction: 'recvonly' });
        pc.addTransceiver('audio', { direction: 'recvonly' });
        if (videoTransceiver && videoTransceiver.receiver) {
          this.applyZeroLatencyPlayoutDelay(videoTransceiver.receiver, 'video');
        }
      }

      const videoEl = session.element as HTMLVideoElement;
      pc.ontrack = (event: RTCTrackEvent) => {
        if (session.cancelled) return;
        if (event.receiver && event.track && event.track.kind === 'video') {
          this.applyZeroLatencyPlayoutDelay(event.receiver, 'video');
        }
        if (event.streams && event.streams[0]) {
          videoEl.srcObject = event.streams[0];
        } else {
          const MediaStreamConstructor =
            typeof MediaStream !== 'undefined'
              ? MediaStream
              : typeof window !== 'undefined'
                ? window.MediaStream
                : null;
          if (!videoEl.srcObject && MediaStreamConstructor) {
            videoEl.srcObject = new MediaStreamConstructor();
          }
          if (
            videoEl.srcObject &&
            typeof (videoEl.srcObject as MediaStream).addTrack === 'function' &&
            event.track
          ) {
            (videoEl.srcObject as MediaStream).addTrack(event.track);
          }
        }
      };

      const onConnChange = (): void => this.handleWebRTCConnectionChange(session);
      pc.oniceconnectionstatechange = onConnChange;
      pc.onconnectionstatechange = onConnChange;

      await this.negotiateWebRTC(session, session.stream.stream_url);
    } catch (err: unknown) {
      console.warn('[MirrormereVideo] WebRTC reconnection cycle error:', err);
      if (!session.cancelled) {
        this.scheduleWebRTCReconnect(session);
      }
    }
  }

  /**
   * Handles stream failure when reconnect retries are exhausted.
   * Drops cleanly back to widgets mode if primary stream fails, or dismisses PiP slot.
   */
  handleStreamFallback(session: VideoSession): void {
    if (!session || session.cancelled) return;

    const isPrimary = session === this.primarySession;
    const isPip = session === this.pipSession;
    if (!isPrimary && !isPip) return;

    if (isPip) {
      if (!this.primarySession) {
        console.warn('[MirrormereVideo] Primary presentation stream lost. Falling back to widgets mode.');
        this.exitVideoMode();
        if (typeof this.options.onFallback === 'function') {
          this.options.onFallback(session, 'widgets');
        }
        return;
      }
      console.warn('[MirrormereVideo] PiP stream lost. Tearing down PiP slot.');
      if (this.isSwapped) {
        this.isSwapped = false;
        if (this.stage) this.stage.dataset.swapped = 'false';
      }
      if (this.pipSession) {
        this.teardownSession(this.pipSession);
        this.pipSession = null;
      }
      this.serverPip = null;
      if (this.pipSlot) {
        this.pipSlot.style.display = 'none';
      }
      if (this.primarySlot) {
        this.primarySlot.style.display = '';
      }
      if (this.hud) {
        if (this.hud.lastVideoState) {
          this.hud.lastVideoState = { ...this.hud.lastVideoState, pip: null };
        }
      }
      this.syncAudioFocus();
      if (this.hud && typeof this.hud.syncStream === 'function') {
        this.hud.syncStream();
      }
      if (typeof this.options.onFallback === 'function') {
        this.options.onFallback(session, 'pip_dismissed');
      }
      return;
    }

    if (isPrimary) {
      if (this.isSwapped && this.pipSession) {
        // Swapped primary was displayed in the corner dock; healthy pipSession remains full-screen
        console.warn('[MirrormereVideo] Swapped primary dock stream lost. Tearing down dock slot.');
        if (this.primarySession) {
          this.teardownSession(this.primarySession);
          this.primarySession = null;
        }
        this.serverPrimary = null;
        if (this.primarySlot) {
          this.primarySlot.style.display = 'none';
        }
        if (this.hud) {
          if (this.hud.lastVideoState) {
            this.hud.lastVideoState = { ...this.hud.lastVideoState, primary: null };
          }
        }
        this.syncAudioFocus();
        if (this.hud && typeof this.hud.syncStream === 'function') {
          this.hud.syncStream();
        }
        if (typeof this.options.onFallback === 'function') {
          this.options.onFallback(session, 'pip_dismissed');
        }
        return;
      }

      console.warn('[MirrormereVideo] Primary presentation stream lost. Falling back to widgets mode.');
      this.exitVideoMode();
      if (typeof this.options.onFallback === 'function') {
        this.options.onFallback(session, 'widgets');
      }
    }
  }

  /**
   * Safely initiates video playback with autoplay rejection protection.
   */
  safePlay(video: HTMLVideoElement | null): void {
    if (!video || typeof video.play !== 'function') return;
    try {
      const p = video.play();
      if (p !== undefined && typeof p.catch === 'function') {
        p.catch((err: unknown) => {
          console.warn('[MirrormereVideo] Playback prevented by browser autoplay policy:', err);
        });
      }
    } catch {
      // ignore
    }
  }

  /**
   * Hermetic teardown of a media session freeing decoders, sinks, and peer connections.
   */
  teardownSession(session: VideoSession | null): void {
    if (!session) return;
    session.cancelled = true;

    if (session.spinnerSafetyTimer) {
      clearTimeout(session.spinnerSafetyTimer);
      session.spinnerSafetyTimer = null;
    }

    if (session.spinner && session.spinner.parentNode) {
      session.spinner.parentNode.removeChild(session.spinner);
      session.spinner = null;
    }

    if (session.reconnectTimer) {
      clearTimeout(session.reconnectTimer);
      session.reconnectTimer = null;
    }
    if (session.disconnectTimer) {
      clearTimeout(session.disconnectTimer);
      session.disconnectTimer = null;
    }

    if (session.touchCleanups) {
      session.touchCleanups.forEach((cleanup) => cleanup());
      session.touchCleanups = [];
    }

    if (this.audioManager && session.element && typeof this.audioManager.unregisterMediaElement === 'function') {
      this.audioManager.unregisterMediaElement(session.element as HTMLMediaElement);
    }

    if (session.element) {
      if (session.type === 'mjpeg') {
        const img = session.element as HTMLImageElement;
        img.src = '';
        img.removeAttribute('src');
        img.onload = null;
        img.onerror = null;
      } else {
        const video = session.element as HTMLVideoElement;
        if (typeof video.pause === 'function') {
          try {
            video.pause();
          } catch {
            // ignore
          }
        }
        video.srcObject = null;
        video.removeAttribute('src');
        if (typeof video.load === 'function') {
          try {
            video.load();
          } catch {
            // ignore
          }
        }
      }

      if (session.element.parentNode) {
        session.element.parentNode.removeChild(session.element);
      }
    }

    if (session.pc) {
      const pc = session.pc;
      session.pc = null;
      pc.oniceconnectionstatechange = null;
      pc.onconnectionstatechange = null;
      pc.ontrack = null;
      pc.onicecandidate = null;
      if (typeof pc.close === 'function') {
        pc.close();
      }
    }

    if (session.hls && typeof session.hls.destroy === 'function') {
      session.hls.destroy();
    }
  }

  /**
   * Instance delegate for getNormalizedVideoCoordinates.
   */
  getNormalizedVideoCoordinates(
    e: CoordinateEvent,
    videoEl: HTMLVideoElement | HTMLImageElement | { getBoundingClientRect: () => DOMRect | { left: number; top: number; width: number; height: number }; videoWidth?: number; videoHeight?: number; naturalWidth?: number; naturalHeight?: number },
  ): NormalizedCoordinates | null {
    return getNormalizedVideoCoordinates(e, videoEl);
  }

  /**
   * Binds pointer event handlers to the video element for browser-level touch forwarding.
   */
  bindTouchForwarding(session: VideoSession): void {
    if (
      !this.touchForwardingEnabled ||
      !this.remoteUrl ||
      !session?.element ||
      !session?.stream?.controllable
    ) {
      return;
    }
    const videoEl = session.element;

    let isDown = false;
    let startX = 0;
    let startY = 0;
    let lastMoveTime = 0;
    let hasMoved = false;

    const isPresentationPrimary = (): boolean => {
      return (
        (session === this.primarySession && !this.isSwapped) ||
        (session === this.pipSession && this.isSwapped)
      );
    };

    const onPointerDown = (e: Event): void => {
      if (!isPresentationPrimary()) return;
      const pe = e as PointerEvent;
      if (pe.button !== undefined && pe.button !== 0) return;

      const coords = getNormalizedVideoCoordinates(pe, videoEl);
      if (!coords) return;

      isDown = true;
      hasMoved = false;
      startX = pe.clientX !== undefined ? pe.clientX : 0;
      startY = pe.clientY !== undefined ? pe.clientY : 0;
      lastMoveTime = Date.now();

      if (typeof pe.preventDefault === 'function') pe.preventDefault();
      if (typeof pe.stopPropagation === 'function') pe.stopPropagation();

      void this.sendTouchEvent('down', coords.x, coords.y);
    };

    const onPointerMove = (e: Event): void => {
      if (!isDown) return;
      if (!isPresentationPrimary()) return;
      const pe = e as PointerEvent;

      const currentX = pe.clientX !== undefined ? pe.clientX : 0;
      const currentY = pe.clientY !== undefined ? pe.clientY : 0;
      const dist = Math.hypot(currentX - startX, currentY - startY);
      if (dist > 10) {
        hasMoved = true;
      }

      const now = Date.now();
      if (now - lastMoveTime < 30) return; // ~33Hz throttle
      lastMoveTime = now;

      const coords = getNormalizedVideoCoordinates(pe, videoEl);
      if (!coords) return;

      if (typeof pe.preventDefault === 'function') pe.preventDefault();
      if (typeof pe.stopPropagation === 'function') pe.stopPropagation();

      void this.sendTouchEvent('move', coords.x, coords.y);
    };

    const onPointerUp = (e: Event): void => {
      if (!isDown) return;
      isDown = false;
      if (!isPresentationPrimary()) return;
      const pe = e as PointerEvent;

      const coords = getNormalizedVideoCoordinates(pe, videoEl);

      if (typeof pe.preventDefault === 'function') pe.preventDefault();
      if (typeof pe.stopPropagation === 'function') pe.stopPropagation();

      if (!hasMoved && coords) {
        void this.sendTouchEvent('tap', coords.x, coords.y);
      } else if (coords) {
        void this.sendTouchEvent('up', coords.x, coords.y);
      }
    };

    const onPointerCancel = (e: Event): void => {
      if (!isDown) return;
      isDown = false;
      if (!isPresentationPrimary()) return;
      const pe = e as PointerEvent;

      const coords = getNormalizedVideoCoordinates(pe, videoEl);
      const x = coords ? coords.x : 0.5;
      const y = coords ? coords.y : 0.5;
      void this.sendTouchEvent('up', x, y);
    };

    const onWindowBlur = (): void => {
      if (!isDown) return;
      isDown = false;
      const coords = getNormalizedVideoCoordinates({ clientX: startX, clientY: startY }, videoEl);
      const x = coords ? coords.x : 0.5;
      const y = coords ? coords.y : 0.5;
      void this.sendTouchEvent('up', x, y);
    };

    videoEl.addEventListener('pointerdown', onPointerDown);
    videoEl.addEventListener('pointermove', onPointerMove);
    videoEl.addEventListener('pointerup', onPointerUp);
    videoEl.addEventListener('pointercancel', onPointerCancel);
    videoEl.addEventListener('pointerleave', onPointerCancel);

    if (typeof window !== 'undefined' && typeof window.addEventListener === 'function') {
      window.addEventListener('blur', onWindowBlur);
    }

    session.touchCleanups = [
      () => videoEl.removeEventListener('pointerdown', onPointerDown),
      () => videoEl.removeEventListener('pointermove', onPointerMove),
      () => videoEl.removeEventListener('pointerup', onPointerUp),
      () => videoEl.removeEventListener('pointercancel', onPointerCancel),
      () => videoEl.removeEventListener('pointerleave', onPointerCancel),
      () => {
        if (typeof window !== 'undefined' && typeof window.removeEventListener === 'function') {
          window.removeEventListener('blur', onWindowBlur);
        }
      },
    ];
  }

  /**
   * Dispatches normalized touch actions to the remote sidecar via HTTP POST.
   */
  sendTouchEvent(action: 'down' | 'move' | 'up' | 'tap', x: number, y: number): Promise<unknown> {
    const fetchFn = this.fetchFn || (typeof fetch === 'function' ? fetch : null);
    if (!fetchFn || !this.remoteUrl) return Promise.resolve();

    const payload = { action, x, y };
    return fetchFn(`${this.remoteUrl}/touch`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
      keepalive: true,
    }).catch(() => {
      // Fallback to /tap for older sidecar versions
      if (action === 'tap') {
        return fetchFn(`${this.remoteUrl}/tap`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ x, y }),
          keepalive: true,
        }).catch(() => {
          // ignore
        });
      }
    });
  }

  /**
   * Inspect active runtime state for testing and HUD rendering.
   */
  getState(): VideoPlayerManagerState {
    return {
      mode: this.currentMode,
      isSwapped: this.isSwapped,
      primary: this.serverPrimary,
      pip: this.serverPip,
      hasPrimarySession: Boolean(this.primarySession),
      hasPipSession: Boolean(this.pipSession),
    };
  }

  /**
   * Complete teardown of manager instance.
   */
  destroy(): void {
    if (this.paintFlushTimer) {
      const cancelRAF =
        typeof window !== 'undefined' && typeof window.cancelAnimationFrame === 'function'
          ? window.cancelAnimationFrame.bind(window)
          : typeof cancelAnimationFrame === 'function'
            ? cancelAnimationFrame
            : clearTimeout;
      cancelRAF(this.paintFlushTimer as number);
      this.paintFlushTimer = null;
    }
    for (const cleanup of this.slotClickCleanups) {
      cleanup();
    }
    this.slotClickCleanups = [];
    this.exitVideoMode(false);
  }
}

/**
 * VideoStage React component rendering #video-stage, slots, and HUD.
 */
export function VideoStage(): React.JSX.Element {
  const stageRef = useRef<HTMLDivElement>(null);
  const primarySlotRef = useRef<HTMLDivElement>(null);
  const pipSlotRef = useRef<HTMLDivElement>(null);
  const hudControllerRef = useRef<VideoHUDController | null>(null);
  const managerRef = useRef<VideoPlayerManager | null>(null);

  const audioController = useAudioController();
  const videoState = useDisplayStore((s) => s.video);

  // Initialize and mount VideoPlayerManager
  useEffect(() => {
    const stageEl =
      stageRef.current || (typeof document !== 'undefined' ? document.getElementById('video-stage') : null);
    const primaryEl =
      primarySlotRef.current ||
      (typeof document !== 'undefined' ? document.getElementById('video-primary-slot') : null);
    const pipEl =
      pipSlotRef.current ||
      (typeof document !== 'undefined' ? document.getElementById('video-pip-slot') : null);

    const manager = new VideoPlayerManager({
      stageElement: stageEl,
      primarySlot: primaryEl,
      pipSlot: pipEl,
      audioManager: audioController,
      hud: hudControllerRef.current,
    });
    managerRef.current = manager;

    if (typeof window !== 'undefined') {
      window.videoManager = manager;
    }

    if (hudControllerRef.current) {
      hudControllerRef.current.videoManager = manager;
      manager.setHUD(hudControllerRef.current);
    }

    // Apply active video state immediately if already set
    if (videoState && (videoState.mode === 'video' || videoState.primary)) {
      manager.handleVideoState(videoState);
    }

    return () => {
      if (typeof window !== 'undefined' && window.videoManager === manager) {
        delete window.videoManager;
      }
      manager.destroy();
      managerRef.current = null;
    };
  }, [audioController]); // eslint-disable-line react-hooks/exhaustive-deps

  // Keep audioManager synced if controller reference updates
  useEffect(() => {
    if (managerRef.current) {
      managerRef.current.audioManager = audioController;
      managerRef.current.syncAudioFocus();
    }
  }, [audioController]);

  // Sync incoming displayStore video state updates
  useEffect(() => {
    managerRef.current?.handleVideoState(videoState);
  }, [videoState]);

  // Stable callback ref ensuring bidirectional linkage between VideoPlayerManager and VideoHUDController
  const handleHudController = useCallback((hud: VideoHUDController | null) => {
    hudControllerRef.current = hud;
    if (typeof window !== 'undefined') {
      if (hud) {
        window.videoHUD = hud;
      } else {
        delete window.videoHUD;
      }
    }
    if (managerRef.current) {
      managerRef.current.setHUD(hud);
    }
    if (hud && managerRef.current) {
      hud.videoManager = managerRef.current;
    }
  }, []);

  return (
    <div id="video-stage" ref={stageRef} className="video-stage" style={{ display: 'none' }}>
      <div id="video-primary-slot" ref={primarySlotRef} className="video-primary-slot" />
      <div id="video-pip-slot" ref={pipSlotRef} className="video-pip-slot" style={{ display: 'none' }} />
      <Hud controllerRef={handleHudController} />
    </div>
  );
}

declare global {
  interface Window {
    videoManager?: VideoPlayerManager;
    MirrormereVideo?: {
      VideoPlayerManager: typeof VideoPlayerManager;
      getNormalizedVideoCoordinates: typeof getNormalizedVideoCoordinates;
    };
  }
}

if (typeof window !== 'undefined') {
  window.MirrormereVideo = {
    VideoPlayerManager,
    getNormalizedVideoCoordinates,
  };
}

import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { useWidget } from '../../store/hooks';
import type { WidgetComponentProps } from '../registry';
import { useIsCurrentPane } from '../../carousel/PaneContext';
import { useDocumentVisible } from '../../lifecycle/visibility';
import { beginAnimation, endAnimation } from '../../perf/animating';
import { useDisplayStore } from '../../store';
import {
  HudNextIcon,
  HudPauseIcon,
  HudPlayIcon,
  HudPrevIcon,
} from '../../video/HudIcons';

declare global {
  interface Window {
    EmberBridge?: {
      syncGooglePhotosAlbum?: (albumUrl: string) => void;
      connectGooglePhotos?: (albumUrl: string) => void;
    };
    onEmberPhotosSynced?: (payload: unknown) => void;
  }
}

export interface PhotoItem {
  id?: string;
  url?: string;
  URL?: string;
  width?: number;
  height?: number;
}

export interface PhotoCarouselData {
  photos?: PhotoItem[];
  album_url?: string;
  cycle_interval_seconds?: number;
}

interface PhotoCarouselConfig {
  source?: 'share_url' | 'ember_bridge';
  album_url?: string;
  album_url_env?: string;
}

export interface ContainerSize {
  width: number;
  height: number;
}

const CROSSFADE_MS = 850;
const EMPTY_PHOTOS: PhotoItem[] = [];
const DEFAULT_CYCLE_SECONDS = 30;

export const PHOTO_CONTROLS_HIDE_MS = 3000;
export const PHOTO_PAUSE_INACTIVITY_MS = 60000;

export function parsePhotoCarouselData(raw: unknown): PhotoCarouselData {
  if (!raw) return {};
  if (typeof raw === 'object' && !Array.isArray(raw)) {
    return raw as PhotoCarouselData;
  }
  if (typeof raw === 'string') {
    try {
      let parsed = JSON.parse(raw);
      if (typeof parsed === 'string') {
        try {
          parsed = JSON.parse(parsed);
        } catch {
          // ignore nested parse failure
        }
      }
      if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
        return parsed as PhotoCarouselData;
      }
    } catch {
      return {};
    }
  }
  return {};
}

export function getBackdropPhotoURL(rawUrl?: string): string {
  if (!rawUrl) return '';
  return `${rawUrl}=w96-h54-c`;
}

export function getSizedPhotoURL(
  rawUrl?: string,
  containerSize?: ContainerSize,
): string {
  if (!rawUrl) return '';

  const dpr = (typeof window !== 'undefined' && window.devicePixelRatio) || 1;
  const screenWidth = Math.round(
    (((typeof window !== 'undefined' && window.screen?.width) ||
      (typeof window !== 'undefined' && window.innerWidth) ||
      1920) *
      dpr),
  );
  const screenHeight = Math.round(
    (((typeof window !== 'undefined' && window.screen?.height) ||
      (typeof window !== 'undefined' && window.innerHeight) ||
      1080) *
      dpr),
  );

  const rectW = Math.round((containerSize?.width || 0) * dpr);
  const rectH = Math.round((containerSize?.height || 0) * dpr);

  const targetWidth = rectW > 0 ? Math.max(rectW, screenWidth) : screenWidth;
  const targetHeight = rectH > 0 ? Math.max(rectH, screenHeight) : screenHeight;
  return `${rawUrl}=w${targetWidth}-h${targetHeight}`;
}

export interface WidgetPhotoDeckState {
  currentKey: string | null;
  seenKeys: Set<string>;
  remainingMs: number | null;
}

export const photoDeckStateById = new Map<string, WidgetPhotoDeckState>();

useDisplayStore.subscribe((state) => {
  for (const id of Array.from(photoDeckStateById.keys())) {
    if (!state?.widgets?.[id]) {
      photoDeckStateById.delete(id);
    }
  }
});

function getWidgetPhotoDeckState(id: string): WidgetPhotoDeckState {
  let deck = photoDeckStateById.get(id);
  if (!deck) {
    deck = {
      currentKey: null,
      seenKeys: new Set<string>(),
      remainingMs: null,
    };
    photoDeckStateById.set(id, deck);
  }
  return deck;
}

export function getPhotoItemKey(item?: PhotoItem, fallbackIndex?: number): string {
  const rawUrl = item?.url ?? item?.URL;
  if (rawUrl && rawUrl.trim() !== '') return rawUrl;
  if (item?.id && item.id.trim() !== '') return item.id;
  return fallbackIndex !== undefined ? String(fallbackIndex) : '';
}

export function selectNextPhotoIndex(
  photos: readonly PhotoItem[],
  baseIndex: number,
  seenKeys: Set<string>,
): number {
  if (photos.length <= 1) return 0;
  const safeBase = ((baseIndex % photos.length) + photos.length) % photos.length;
  for (let i = 1; i < photos.length; i++) {
    const candidateIdx = (safeBase + i) % photos.length;
    const candidateKey = getPhotoItemKey(photos[candidateIdx], candidateIdx);
    if (!seenKeys.has(candidateKey)) {
      return candidateIdx;
    }
  }
  seenKeys.clear();
  return (safeBase + 1) % photos.length;
}

export function selectPrevPhotoIndex(photos: readonly PhotoItem[], baseIndex: number): number {
  if (photos.length <= 1) return 0;
  return (((baseIndex % photos.length) + photos.length) % photos.length - 1 + photos.length) % photos.length;
}

function scheduleCycleTick(
  timerRef: React.MutableRefObject<ReturnType<typeof setTimeout> | null>,
  activeDelayMsRef: React.MutableRefObject<number>,
  cycleStartedAtRef: React.MutableRefObject<number | null>,
  deck: WidgetPhotoDeckState,
  cycleIntervalSeconds: number,
  triggerAdvance: () => void,
  delayMs: number,
) {
  timerRef.current = setTimeout(() => {
    deck.remainingMs = cycleIntervalSeconds * 1000;
    activeDelayMsRef.current = cycleIntervalSeconds * 1000;
    cycleStartedAtRef.current = Date.now();
    triggerAdvance();
    scheduleCycleTick(
      timerRef,
      activeDelayMsRef,
      cycleStartedAtRef,
      deck,
      cycleIntervalSeconds,
      triggerAdvance,
      cycleIntervalSeconds * 1000,
    );
  }, delayMs);
}

function preloadAndDecode(src: string): Promise<boolean> {
  const preload = new Image();
  preload.src = src;
  return new Promise<boolean>((resolve) => {
    let settled = false;
    const onOk = () => {
      if (!settled) {
        settled = true;
        resolve(true);
      }
    };
    const onFail = () => {
      if (!settled) {
        settled = true;
        resolve(false);
      }
    };

    if (typeof preload.decode === 'function') {
      preload.decode().then(onOk, onFail);
      preload.onerror = onFail;
    } else {
      preload.onload = onOk;
      preload.onerror = onFail;
    }
  });
}

function resolveInitialPhotoIndex(
  photos: readonly PhotoItem[],
  deck: WidgetPhotoDeckState,
): number {
  if (photos.length === 0) return 0;
  if (deck.currentKey) {
    const idx = photos.findIndex((p, i) => getPhotoItemKey(p, i) === deck.currentKey);
    if (idx !== -1) {
      deck.seenKeys.add(deck.currentKey);
      return idx;
    }
  }
  let unseenIdx = photos.findIndex((p, i) => !deck.seenKeys.has(getPhotoItemKey(p, i)));
  if (unseenIdx === -1) {
    deck.seenKeys.clear();
    unseenIdx = 0;
  }
  const key = getPhotoItemKey(photos[unseenIdx], unseenIdx);
  deck.currentKey = key;
  deck.seenKeys.add(key);
  return unseenIdx;
}

function useContainerSize() {
  const [containerSize, setContainerSize] = useState<ContainerSize>({ width: 0, height: 0 });
  const containerSizeRef = useRef<ContainerSize>({ width: 0, height: 0 });
  const [element, setElement] = useState<HTMLDivElement | null>(null);

  const rootRef = (node: HTMLDivElement | null) => {
    setElement(node);
  };

  useEffect(() => {
    if (!element || typeof ResizeObserver !== 'function') return;

    const ro = new ResizeObserver((entries) => {
      for (const entry of entries) {
        const { width, height } = entry.contentRect;
        if (width > 0 && height > 0) {
          const cur = containerSizeRef.current;
          if (cur.width !== width || cur.height !== height) {
            const newSize = { width, height };
            containerSizeRef.current = newSize;
            setContainerSize(newSize);
          }
        }
      }
    });
    ro.observe(element);
    return () => ro.disconnect();
  }, [element]);

  return { rootRef, containerSize, containerSizeRef };
}

function PhotoCarouselLoading() {
  return (
    <div
      className="photo-loading"
      style={{
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        height: '100%',
        color: 'var(--mm-text-dim, #8b819f)',
        fontSize: '0.95rem',
        fontWeight: 700,
      }}
    >
      <span>Loading photos...</span>
    </div>
  );
}

interface PhotoCarouselEmptyProps {
  source: 'share_url' | 'ember_bridge';
  needsAuth: boolean;
  syncError: boolean;
  albumUrl: string;
}

function PhotoCarouselEmpty({ source, needsAuth, syncError, albumUrl }: PhotoCarouselEmptyProps) {
  let emptyTitle = 'No photos in shared album';
  if (source === 'ember_bridge') {
    if (needsAuth) {
      emptyTitle = 'Google Photos sign-in required';
    } else if (syncError) {
      emptyTitle = 'Photo sync failed';
    } else {
      emptyTitle = 'No photos available';
    }
  }

  const canConnect =
    source === 'ember_bridge' &&
    albumUrl.trim() !== '' &&
    typeof window !== 'undefined' &&
    typeof window.EmberBridge?.connectGooglePhotos === 'function';

  return (
    <div className="photo-empty">
      <span className="empty-icon">
        <svg
          width="24"
          height="24"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
          aria-hidden="true"
        >
          <rect width="18" height="18" x="3" y="3" rx="2" ry="2" />
          <circle cx="9" cy="9" r="2" />
          <path d="m21 15-3.086-3.086a2 2 0 0 0-2.828 0L6 21" />
        </svg>
      </span>
      <span className="empty-title">{emptyTitle}</span>
      {canConnect && (
        <button
          type="button"
          className="photo-connect-btn"
          onClick={(e) => {
            e.stopPropagation();
            window.EmberBridge?.connectGooglePhotos?.(albumUrl);
          }}
        >
          Connect Google Photos
        </button>
      )}
    </div>
  );
}

function PhotoBackdrop({
  currentUrl,
  nextUrl,
  isTransitioning,
}: {
  currentUrl: string;
  nextUrl: string;
  isTransitioning: boolean;
}) {
  return (
    <div className="photo-backdrop-container" aria-hidden="true">
      <img
        className="photo-backdrop-layer photo-backdrop-current"
        src={currentUrl}
        alt=""
        style={{
          opacity: isTransitioning ? 0 : 1,
          transition: isTransitioning ? 'opacity 0.85s ease-in-out' : 'none',
        }}
      />
      <img
        className="photo-backdrop-layer photo-backdrop-next"
        src={isTransitioning ? nextUrl : undefined}
        alt=""
        style={{
          opacity: isTransitioning ? 1 : 0,
          transition: isTransitioning ? 'opacity 0.85s ease-in-out' : 'none',
        }}
      />
    </div>
  );
}

function PhotoForeground({
  currentUrl,
  nextUrl,
  isTransitioning,
}: {
  currentUrl: string;
  nextUrl: string;
  isTransitioning: boolean;
}) {
  return (
    <div className="photo-foreground-container">
      <img
        className="photo-layer photo-current dither"
        src={currentUrl}
        alt="Photo"
        style={{
          opacity: isTransitioning ? 0 : 1,
          transition: isTransitioning ? 'opacity 0.85s ease-in-out' : 'none',
        }}
      />
      <img
        className="photo-layer photo-next dither"
        src={isTransitioning ? nextUrl : undefined}
        alt="Photo"
        style={{
          opacity: isTransitioning ? 1 : 0,
          transition: isTransitioning ? 'opacity 0.85s ease-in-out' : 'none',
        }}
      />
    </div>
  );
}

interface PhotoTransportControlsProps {
  visible: boolean;
  isPaused: boolean;
  onPrev: () => void;
  onNext: () => void;
  onTogglePause: () => void;
}

function PhotoTransportControls({
  visible,
  isPaused,
  onPrev,
  onNext,
  onTogglePause,
}: PhotoTransportControlsProps) {
  return (
    <div
      className={`photo-transport-controls${visible ? ' visible' : ''}`}
      onClick={(e) => {
        e.stopPropagation();
      }}
    >
      <button
        type="button"
        className="photo-nav-btn photo-nav-prev"
        aria-label="Previous photo"
        onClick={(e) => {
          e.stopPropagation();
          onPrev();
        }}
      >
        <HudPrevIcon />
      </button>
      <button
        type="button"
        className="photo-nav-btn photo-pause-pill"
        aria-label={isPaused ? 'Resume slideshow' : 'Pause slideshow'}
        aria-pressed={isPaused}
        data-paused={isPaused ? 'true' : 'false'}
        onClick={(e) => {
          e.stopPropagation();
          onTogglePause();
        }}
      >
        {isPaused ? <HudPlayIcon /> : <HudPauseIcon />}
      </button>
      <button
        type="button"
        className="photo-nav-btn photo-nav-next"
        aria-label="Next photo"
        onClick={(e) => {
          e.stopPropagation();
          onNext();
        }}
      >
        <HudNextIcon />
      </button>
    </div>
  );
}

// Module-level listener registry for window.onEmberPhotosSynced
type EmberSyncListener = (payload: unknown) => void;
const emberSyncListeners = new Set<EmberSyncListener>();

function handleGlobalEmberPhotosSynced(payload: unknown) {
  for (const listener of Array.from(emberSyncListeners)) {
    try {
      listener(payload);
    } catch {
      // safe no-op on listener error
    }
  }
}

function registerEmberSyncListener(listener: EmberSyncListener): () => void {
  emberSyncListeners.add(listener);
  if (typeof window !== 'undefined') {
    window.onEmberPhotosSynced = handleGlobalEmberPhotosSynced;
  }
  return () => {
    emberSyncListeners.delete(listener);
    if (emberSyncListeners.size === 0 && typeof window !== 'undefined') {
      delete window.onEmberPhotosSynced;
    }
  };
}

export function PhotoCarouselWidget({
  id,
}: WidgetComponentProps) {
  const entry = useWidget(id);
  const isCurrentPane = useIsCurrentPane();
  const isDocumentVisible = useDocumentVisible();
  const isVideoMode = useDisplayStore(
    (s) => (s.video?.mode || (s.video?.primary ? 'video' : 'widgets')) === 'video',
  );

  const config = entry?.meta?.config as PhotoCarouselConfig | undefined;
  const rawData = entry?.data;
  const data = parsePhotoCarouselData(rawData);
  const hasLoaded = entry?.state !== 'loading' && Array.isArray(data.photos);

  const source: 'share_url' | 'ember_bridge' =
    config?.source === 'ember_bridge' ? 'ember_bridge' : 'share_url';
  const albumUrl = (config?.album_url || data?.album_url || '').trim();

  const [needsAuth, setNeedsAuth] = useState(false);
  const [syncError, setSyncError] = useState(false);

  // Sync listener and interval for ember_bridge
  useEffect(() => {
    if (
      source !== 'ember_bridge' ||
      !albumUrl ||
      typeof window === 'undefined' ||
      typeof window.EmberBridge?.syncGooglePhotosAlbum !== 'function'
    ) {
      return;
    }

    const listener: EmberSyncListener = (rawPayload) => {
      let payload = rawPayload as Record<string, unknown> | null;
      if (typeof rawPayload === 'string') {
        try {
          payload = JSON.parse(rawPayload);
        } catch {
          return;
        }
      }
      if (!payload || typeof payload !== 'object') return;

      if (payload.needs_auth === true) {
        setNeedsAuth(true);
        setSyncError(false);
      } else if (payload.ok === false) {
        setSyncError(true);
        setNeedsAuth(false);
      } else if (Array.isArray(payload.photos) && payload.photos.length > 0) {
        setNeedsAuth(false);
        setSyncError(false);
        fetch('api/widgets/' + encodeURIComponent(id) + '/photos', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ photos: payload.photos }),
        }).catch(() => {});
      }
    };

    const unbind = registerEmberSyncListener(listener);
    window.EmberBridge.syncGooglePhotosAlbum(albumUrl);
    const intervalTimer = setInterval(() => {
      window.EmberBridge?.syncGooglePhotosAlbum?.(albumUrl);
    }, 300_000);

    return () => {
      unbind();
      clearInterval(intervalTimer);
    };
  }, [source, albumUrl, id]);

  const photos = Array.isArray(data.photos) ? data.photos : EMPTY_PHOTOS;
  const cycleIntervalSeconds = Math.max(
    5,
    Number(data?.cycle_interval_seconds) || DEFAULT_CYCLE_SECONDS,
  );

  const [isPaused, setIsPaused] = useState(false);
  const [controlsVisible, setControlsVisible] = useState(false);
  const isPausedRef = useRef(isPaused);
  const controlsTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const pauseInactivityTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const { rootRef, containerSize, containerSizeRef } = useContainerSize();

  const [currentIndex, setCurrentIndex] = useState(() =>
    resolveInitialPhotoIndex(photos, getWidgetPhotoDeckState(id)),
  );

  const safeIndex = photos.length > 0 ? currentIndex % photos.length : 0;
  const currentIndexRef = useRef(safeIndex);

  const probeIndexRef = useRef<number | null>(null);
  const [retryTick, setRetryTick] = useState(0);

  const photosRef = useRef(photos);
  const isAnimatingRef = useRef(false);

  // Crossfade transition state
  const [transitionState, setTransitionState] = useState<{
    nextIndex: number;
    active: boolean;
  } | null>(null);
  const transitionStateRef = useRef<{ nextIndex: number; active: boolean } | null>(null);

  const cycleSeqRef = useRef(0);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const transitionTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const cycleStartedAtRef = useRef<number | null>(null);
  const activeDelayMsRef = useRef<number>(cycleIntervalSeconds * 1000);

  useLayoutEffect(() => {
    isPausedRef.current = isPaused;
    currentIndexRef.current = safeIndex;
    photosRef.current = photos;
    transitionStateRef.current = transitionState;
  });

  useEffect(() => {
    probeIndexRef.current = null;
    if (photos.length === 0) return;
    const targetIdx = resolveInitialPhotoIndex(photos, getWidgetPhotoDeckState(id));
    if (currentIndexRef.current !== targetIdx) {
      setCurrentIndex(targetIdx);
      currentIndexRef.current = targetIdx;
    }
  }, [photos, id]);

  const startAnimation = () => {
    if (!isAnimatingRef.current) {
      isAnimatingRef.current = true;
      beginAnimation();
    }
  };

  const stopAnimation = () => {
    if (isAnimatingRef.current) {
      isAnimatingRef.current = false;
      endAnimation();
    }
  };

  const clearPendingTransition = () => {
    cycleSeqRef.current += 1;
    stopAnimation();
    if (transitionTimeoutRef.current) {
      clearTimeout(transitionTimeoutRef.current);
      transitionTimeoutRef.current = null;
    }
    if (transitionStateRef.current !== null) {
      transitionStateRef.current = null;
      setTransitionState(null);
    }
  };

  const showControls = () => {
    setControlsVisible(true);
    if (controlsTimerRef.current) {
      clearTimeout(controlsTimerRef.current);
    }
    controlsTimerRef.current = setTimeout(() => {
      setControlsVisible(false);
      controlsTimerRef.current = null;
    }, PHOTO_CONTROLS_HIDE_MS);
  };

  const resetPauseInactivityTimer = () => {
    if (pauseInactivityTimerRef.current) {
      clearTimeout(pauseInactivityTimerRef.current);
      pauseInactivityTimerRef.current = null;
    }
    pauseInactivityTimerRef.current = setTimeout(() => {
      setIsPaused(false);
      isPausedRef.current = false;
      pauseInactivityTimerRef.current = null;
    }, PHOTO_PAUSE_INACTIVITY_MS);
  };

  const togglePause = () => {
    if (!isPaused) {
      setIsPaused(true);
      isPausedRef.current = true;
      resetPauseInactivityTimer();
    } else {
      setIsPaused(false);
      isPausedRef.current = false;
      if (pauseInactivityTimerRef.current) {
        clearTimeout(pauseInactivityTimerRef.current);
        pauseInactivityTimerRef.current = null;
      }
      showControls();
    }
  };

  const triggerAdvance = (direction: 'next' | 'prev' = 'next') => {
    const currentPhotos = photosRef.current;
    if (currentPhotos.length <= 1) return;

    const baseIndex = transitionStateRef.current?.active
      ? transitionStateRef.current.nextIndex
      : (probeIndexRef.current ?? currentIndexRef.current);

    const deck = getWidgetPhotoDeckState(id);
    const baseKey = getPhotoItemKey(currentPhotos[baseIndex], baseIndex);
    deck.currentKey = baseKey;
    if (direction === 'prev') {
      deck.seenKeys.delete(baseKey);
    } else {
      deck.seenKeys.add(baseKey);
    }

    if (transitionStateRef.current?.active) {
      setCurrentIndex(baseIndex);
      currentIndexRef.current = baseIndex;
      stopAnimation();
      if (transitionTimeoutRef.current) {
        clearTimeout(transitionTimeoutRef.current);
        transitionTimeoutRef.current = null;
      }
      transitionStateRef.current = null;
      setTransitionState(null);
    }

    const nextIndex =
      direction === 'prev'
        ? selectPrevPhotoIndex(currentPhotos, baseIndex)
        : selectNextPhotoIndex(currentPhotos, baseIndex, deck.seenKeys);
    const nextPhoto = currentPhotos[nextIndex];
    const nextRawUrl = nextPhoto?.url ?? nextPhoto?.URL;
    const nextKey = getPhotoItemKey(nextPhoto, nextIndex);

    if (!nextRawUrl) {
      deck.seenKeys.add(nextKey);
      probeIndexRef.current = nextIndex;
      deck.remainingMs = cycleIntervalSeconds * 1000;
      clearPendingTransition();
      setRetryTick((t) => t + 1);
      return;
    }

    const nextFgUrl = getSizedPhotoURL(
      nextRawUrl,
      containerSizeRef.current,
    );

    const seq = ++cycleSeqRef.current;
    if (transitionTimeoutRef.current) {
      clearTimeout(transitionTimeoutRef.current);
      transitionTimeoutRef.current = null;
    }

    preloadAndDecode(nextFgUrl).then((ok) => {
      if (cycleSeqRef.current !== seq) return;

      if (!ok) {
        deck.seenKeys.add(nextKey);
        probeIndexRef.current = nextIndex;
        deck.remainingMs = cycleIntervalSeconds * 1000;
        clearPendingTransition();
        setRetryTick((t) => t + 1);
        return;
      }

      probeIndexRef.current = null;
      startAnimation();
      const nextState = { nextIndex, active: true };
      transitionStateRef.current = nextState;
      setTransitionState(nextState);

      transitionTimeoutRef.current = setTimeout(() => {
        if (cycleSeqRef.current !== seq) return;
        deck.currentKey = nextKey;
        deck.seenKeys.add(nextKey);
        setCurrentIndex(nextIndex);
        currentIndexRef.current = nextIndex;
        stopAnimation();
        transitionStateRef.current = null;
        setTransitionState(null);
        transitionTimeoutRef.current = null;
      }, CROSSFADE_MS);
    });
  };

  const stepPhoto = (direction: 'prev' | 'next') => {
    showControls();
    if (isPausedRef.current) {
      resetPauseInactivityTimer();
    }
    const deck = getWidgetPhotoDeckState(id);
    deck.remainingMs = cycleIntervalSeconds * 1000;
    triggerAdvance(direction);
    if (timerRef.current) {
      clearTimeout(timerRef.current);
      timerRef.current = null;
    }
    if (isCurrentPane && isDocumentVisible && !isVideoMode && !isPausedRef.current) {
      activeDelayMsRef.current = cycleIntervalSeconds * 1000;
      cycleStartedAtRef.current = Date.now();
      scheduleCycleTick(
        timerRef,
        activeDelayMsRef,
        cycleStartedAtRef,
        deck,
        cycleIntervalSeconds,
        triggerAdvance,
        cycleIntervalSeconds * 1000,
      );
    }
  };

  // Handle tap-to-advance and reset interval
  const handleClick = (e: React.MouseEvent<HTMLDivElement>) => {
    if (photos.length <= 1) return;
    if (e.clientX > 0) {
      const rect = e.currentTarget.getBoundingClientRect();
      if (rect.width > 0 && e.clientX - rect.left < rect.width / 3) {
        stepPhoto('prev');
        return;
      }
    }
    stepPhoto('next');
  };

  const clearPendingTransitionRef = useRef(clearPendingTransition);
  const triggerAdvanceRef = useRef(triggerAdvance);
  useEffect(() => {
    clearPendingTransitionRef.current = clearPendingTransition;
    triggerAdvanceRef.current = triggerAdvance;
  });

  // Interval timer subscription
  useEffect(() => {
    if (timerRef.current) {
      clearTimeout(timerRef.current);
      timerRef.current = null;
    }

    const shouldRun =
      isCurrentPane && isDocumentVisible && !isVideoMode && !isPaused && photos.length > 1;

    const deck = getWidgetPhotoDeckState(id);

    if (!shouldRun) {
      if (cycleStartedAtRef.current !== null) {
        const elapsed = Date.now() - cycleStartedAtRef.current;
        deck.remainingMs = Math.max(1, activeDelayMsRef.current - elapsed);
        cycleStartedAtRef.current = null;
      }
      const pausedOnly =
        isPaused && isCurrentPane && isDocumentVisible && !isVideoMode && photos.length > 1;
      if (!pausedOnly) {
        clearPendingTransitionRef.current();
      }
      return;
    }

    const initialDelayMs = deck.remainingMs ?? cycleIntervalSeconds * 1000;
    activeDelayMsRef.current = initialDelayMs;
    cycleStartedAtRef.current = Date.now();

    scheduleCycleTick(
      timerRef,
      activeDelayMsRef,
      cycleStartedAtRef,
      deck,
      cycleIntervalSeconds,
      () => triggerAdvanceRef.current(),
      initialDelayMs,
    );

    return () => {
      if (cycleStartedAtRef.current !== null) {
        const elapsed = Date.now() - cycleStartedAtRef.current;
        deck.remainingMs = Math.max(1, activeDelayMsRef.current - elapsed);
        cycleStartedAtRef.current = null;
      }
      if (timerRef.current) {
        clearTimeout(timerRef.current);
        timerRef.current = null;
      }
    };
  }, [
    isCurrentPane,
    isDocumentVisible,
    isVideoMode,
    isPaused,
    photos.length,
    cycleIntervalSeconds,
    retryTick,
    id,
  ]);

  // Cleanup on unmount
  useEffect(() => {
    return () => {
      clearPendingTransitionRef.current();
      if (controlsTimerRef.current) {
        clearTimeout(controlsTimerRef.current);
        controlsTimerRef.current = null;
      }
      if (pauseInactivityTimerRef.current) {
        clearTimeout(pauseInactivityTimerRef.current);
        pauseInactivityTimerRef.current = null;
      }
    };
  }, []);

  const currentPhoto = photos[safeIndex];
  const currentRawUrl = currentPhoto?.url ?? currentPhoto?.URL;
  const currentFgUrl = getSizedPhotoURL(
    currentRawUrl,
    containerSize,
  );
  const currentBackdropUrl = getBackdropPhotoURL(currentRawUrl);

  const isTransitioning = Boolean(transitionState?.active);
  const nextPhoto = transitionState ? photos[transitionState.nextIndex] : undefined;
  const nextRawUrl = nextPhoto?.url ?? nextPhoto?.URL;
  const nextFgUrl = nextRawUrl
    ? getSizedPhotoURL(nextRawUrl, containerSize)
    : '';
  const nextBackdropUrl = nextRawUrl ? getBackdropPhotoURL(nextRawUrl) : '';

  return (
    <div
      ref={rootRef}
      className="widget-photo-carousel dither"
      data-fit="contain"
      onClick={hasLoaded && photos.length > 0 ? handleClick : undefined}
    >
      {!hasLoaded ? (
        <PhotoCarouselLoading />
      ) : photos.length === 0 ? (
        <PhotoCarouselEmpty
          source={source}
          needsAuth={needsAuth}
          syncError={syncError}
          albumUrl={albumUrl}
        />
      ) : (
        <div className="photo-viewport dither">
          <PhotoBackdrop
            currentUrl={currentBackdropUrl}
            nextUrl={nextBackdropUrl}
            isTransitioning={isTransitioning}
          />
          <PhotoForeground
            currentUrl={currentFgUrl}
            nextUrl={nextFgUrl}
            isTransitioning={isTransitioning}
          />
          {photos.length > 1 && (
            <PhotoTransportControls
              visible={controlsVisible || isPaused}
              isPaused={isPaused}
              onPrev={() => stepPhoto('prev')}
              onNext={() => stepPhoto('next')}
              onTogglePause={togglePause}
            />
          )}
        </div>
      )}
    </div>
  );
}

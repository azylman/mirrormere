import { memo, useEffect, useRef, useState, useCallback, useMemo } from 'react';
import { useWidget } from '../../store/hooks';
import type { WidgetComponentProps } from '../registry';
import { useIsCurrentPane } from '../../carousel/PaneContext';
import { useDocumentVisible } from '../../lifecycle/visibility';
import { beginAnimation, endAnimation } from '../../perf/animating';

export interface PhotoItem {
  id?: string;
  url?: string;
  URL?: string;
  width?: number;
  height?: number;
}

export interface PhotoCarouselData {
  photos?: PhotoItem[];
  cycle_interval_seconds?: number;
}

export interface PhotoCarouselConfig {
  fit?: 'contain' | 'cover';
  image_size?: [number, number];
  cycle_interval_seconds?: number;
}

export interface ContainerSize {
  width: number;
  height: number;
}

const CROSSFADE_MS = 850;
const EMPTY_PHOTOS: PhotoItem[] = [];

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

export function getBackdropPhotoURL(rawUrl?: string, fit: 'contain' | 'cover' = 'contain'): string {
  if (!rawUrl || fit === 'cover') return '';
  return `${rawUrl}=w96-h54-c`;
}

export function getSizedPhotoURL(
  rawUrl?: string,
  fit: 'contain' | 'cover' = 'contain',
  imageSize?: [number, number],
  containerSize?: ContainerSize,
): string {
  if (!rawUrl) return '';

  if (
    Array.isArray(imageSize) &&
    imageSize.length >= 2 &&
    Number(imageSize[0]) > 0 &&
    Number(imageSize[1]) > 0
  ) {
    const w = Math.round(Number(imageSize[0]));
    const h = Math.round(Number(imageSize[1]));
    return `${rawUrl}=w${w}-h${h}${fit === 'cover' ? '-c' : ''}`;
  }

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

  if (fit === 'cover') {
    const targetWidth = rectW > 0 ? rectW : screenWidth;
    const targetHeight = rectH > 0 ? rectH : screenHeight;
    return `${rawUrl}=w${targetWidth}-h${targetHeight}-c`;
  }

  const targetWidth = rectW > 0 ? Math.max(rectW, screenWidth) : screenWidth;
  const targetHeight = rectH > 0 ? Math.max(rectH, screenHeight) : screenHeight;
  return `${rawUrl}=w${targetWidth}-h${targetHeight}`;
}

export const PhotoCarouselWidget = memo(function PhotoCarouselWidget({
  id,
}: WidgetComponentProps) {
  const entry = useWidget(id);
  const isCurrentPane = useIsCurrentPane();
  const isDocumentVisible = useDocumentVisible();

  const config = entry?.meta?.config as PhotoCarouselConfig | undefined;
  const rawData = entry?.data;
  const data = useMemo(() => parsePhotoCarouselData(rawData), [rawData]);
  const hasLoaded = entry?.state !== 'loading' && Array.isArray(data.photos);

  const photos = Array.isArray(data.photos) ? data.photos : EMPTY_PHOTOS;
  const fit: 'contain' | 'cover' = config?.fit === 'cover' ? 'cover' : 'contain';
  const cycleIntervalSeconds = Math.max(
    5,
    Number(data?.cycle_interval_seconds ?? config?.cycle_interval_seconds) || 60,
  );

  const rootRef = useRef<HTMLDivElement | null>(null);
  const [containerSize, setContainerSize] = useState<ContainerSize>({ width: 0, height: 0 });
  const containerSizeRef = useRef<ContainerSize>({ width: 0, height: 0 });

  const [currentIndex, setCurrentIndex] = useState(0);
  const safeIndex = photos.length > 0 ? currentIndex % photos.length : 0;
  const currentIndexRef = useRef(safeIndex);
  currentIndexRef.current = safeIndex;

  const probeIndexRef = useRef<number | null>(null);
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    probeIndexRef.current = null;
  }, [photos]);

  const photosRef = useRef(photos);
  photosRef.current = photos;

  const fitRef = useRef(fit);
  fitRef.current = fit;

  const imageSizeRef = useRef(config?.image_size);
  imageSizeRef.current = config?.image_size;

  const isAnimatingRef = useRef(false);

  const startAnimation = useCallback(() => {
    if (!isAnimatingRef.current) {
      isAnimatingRef.current = true;
      beginAnimation();
    }
  }, []);

  const stopAnimation = useCallback(() => {
    if (isAnimatingRef.current) {
      isAnimatingRef.current = false;
      endAnimation();
    }
  }, []);

  // Crossfade transition state
  const [transitionState, setTransitionState] = useState<{
    nextIndex: number;
    active: boolean;
  } | null>(null);
  const transitionStateRef = useRef<{ nextIndex: number; active: boolean } | null>(null);
  transitionStateRef.current = transitionState;

  const cycleSeqRef = useRef(0);
  const timerRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const transitionTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // ResizeObserver for measuring container size (never forced via getBoundingClientRect)
  useEffect(() => {
    const el = rootRef.current;
    if (!el || typeof ResizeObserver !== 'function') return;

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
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const clearPendingTransition = useCallback(() => {
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
  }, [stopAnimation]);

  const triggerAdvance = useCallback(() => {
    const currentPhotos = photosRef.current;
    if (currentPhotos.length <= 1) return;

    const baseIndex = transitionStateRef.current?.active
      ? transitionStateRef.current.nextIndex
      : (probeIndexRef.current ?? currentIndexRef.current);

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

    const nextIndex = (baseIndex + 1) % currentPhotos.length;
    const nextPhoto = currentPhotos[nextIndex];
    const nextRawUrl = nextPhoto?.url ?? nextPhoto?.URL;
    if (!nextRawUrl) {
      probeIndexRef.current = nextIndex;
      clearPendingTransition();
      setRetryTick((t) => t + 1);
      return;
    }

    const nextFgUrl = getSizedPhotoURL(
      nextRawUrl,
      fitRef.current,
      imageSizeRef.current,
      containerSizeRef.current,
    );

    const seq = ++cycleSeqRef.current;
    if (transitionTimeoutRef.current) {
      clearTimeout(transitionTimeoutRef.current);
      transitionTimeoutRef.current = null;
    }

    const preload = new Image();
    preload.src = nextFgUrl;

    const decodePromise = new Promise<boolean>((resolve) => {
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

    decodePromise.then((ok) => {
      if (cycleSeqRef.current !== seq) return;

      if (!ok) {
        probeIndexRef.current = nextIndex;
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
        setCurrentIndex(nextIndex);
        currentIndexRef.current = nextIndex;
        stopAnimation();
        transitionStateRef.current = null;
        setTransitionState(null);
        transitionTimeoutRef.current = null;
      }, CROSSFADE_MS);
    });
  }, [clearPendingTransition, startAnimation, stopAnimation]);

  // Handle tap-to-advance and reset interval
  const handleClick = useCallback(() => {
    if (photos.length > 1) {
      triggerAdvance();
      if (timerRef.current) {
        clearInterval(timerRef.current);
        timerRef.current = null;
      }
      if (isCurrentPane && isDocumentVisible) {
        timerRef.current = setInterval(triggerAdvance, cycleIntervalSeconds * 1000);
      }
    }
  }, [photos.length, triggerAdvance, isCurrentPane, isDocumentVisible, cycleIntervalSeconds]);

  // Interval timer subscription
  useEffect(() => {
    if (timerRef.current) {
      clearInterval(timerRef.current);
      timerRef.current = null;
    }

    if (!isCurrentPane || !isDocumentVisible) {
      clearPendingTransition();
      return;
    }

    if (photos.length <= 1) {
      clearPendingTransition();
      return;
    }

    timerRef.current = setInterval(triggerAdvance, cycleIntervalSeconds * 1000);

    return () => {
      if (timerRef.current) {
        clearInterval(timerRef.current);
        timerRef.current = null;
      }
    };
  }, [
    isCurrentPane,
    isDocumentVisible,
    photos.length,
    cycleIntervalSeconds,
    triggerAdvance,
    clearPendingTransition,
    retryTick,
  ]);

  // Cleanup on unmount
  useEffect(() => {
    return () => {
      clearPendingTransition();
    };
  }, [clearPendingTransition]);

  // Loading state check: if entry data is missing or photos array is not yet loaded
  if (!hasLoaded) {
    return (
      <div
        ref={rootRef}
        className="widget-photo-carousel dither"
        data-fit={fit}
      >
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
      </div>
    );
  }

  // Empty state check
  if (photos.length === 0) {
    return (
      <div
        ref={rootRef}
        className="widget-photo-carousel dither"
        data-fit={fit}
      >
        <div className="photo-empty">
          <span className="empty-icon">🖼️</span>
          <span className="empty-title">No photos in shared album</span>
        </div>
      </div>
    );
  }

  const currentPhoto = photos[safeIndex];
  const currentRawUrl = currentPhoto?.url ?? currentPhoto?.URL;
  const currentFgUrl = getSizedPhotoURL(
    currentRawUrl,
    fit,
    config?.image_size,
    containerSize,
  );
  const currentBackdropUrl = getBackdropPhotoURL(currentRawUrl, fit);

  const isTransitioning = Boolean(transitionState?.active);
  const nextPhoto = transitionState ? photos[transitionState.nextIndex] : undefined;
  const nextRawUrl = nextPhoto?.url ?? nextPhoto?.URL;
  const nextFgUrl = nextRawUrl
    ? getSizedPhotoURL(nextRawUrl, fit, config?.image_size, containerSize)
    : '';
  const nextBackdropUrl = nextRawUrl ? getBackdropPhotoURL(nextRawUrl, fit) : '';

  return (
    <div
      ref={rootRef}
      className="widget-photo-carousel dither"
      data-fit={fit}
      onClick={handleClick}
    >
      <div className="photo-viewport dither">
        {fit !== 'cover' && (
          <div className="photo-backdrop-container" aria-hidden="true">
            <img
              className="photo-backdrop-layer photo-backdrop-current"
              src={currentBackdropUrl}
              alt=""
              style={{
                opacity: isTransitioning ? 0 : 1,
                transition: isTransitioning ? 'opacity 0.85s ease-in-out' : 'none',
              }}
            />
            <img
              className="photo-backdrop-layer photo-backdrop-next"
              src={isTransitioning ? nextBackdropUrl : undefined}
              alt=""
              style={{
                opacity: isTransitioning ? 1 : 0,
                transition: isTransitioning ? 'opacity 0.85s ease-in-out' : 'none',
              }}
            />
          </div>
        )}
        <div className="photo-foreground-container">
          <img
            className="photo-layer photo-current dither"
            src={currentFgUrl}
            alt="Photo"
            style={{
              opacity: isTransitioning ? 0 : 1,
              transition: isTransitioning ? 'opacity 0.85s ease-in-out' : 'none',
            }}
          />
          <img
            className="photo-layer photo-next dither"
            src={isTransitioning ? nextFgUrl : undefined}
            alt="Photo"
            style={{
              opacity: isTransitioning ? 1 : 0,
              transition: isTransitioning ? 'opacity 0.85s ease-in-out' : 'none',
            }}
          />
        </div>
      </div>
    </div>
  );
});

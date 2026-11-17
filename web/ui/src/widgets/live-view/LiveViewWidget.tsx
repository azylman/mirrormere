import './LiveViewWidget.css';
import { useEffect, useRef, useState } from 'react';
import type { WidgetComponentProps } from '../registry';
import { useWidget } from '../../store/hooks';
import { useDisplayStore } from '../../store';
import { useIsCurrentPane } from '../../carousel/PaneContext';
import { useDocumentVisible } from '../../lifecycle/visibility';
import {
  activeAutoLiveStreams,
  attachTrackToVideo,
  buildSnapshotUrl,
  computeStaggerDelayMs,
  exchangeWhepSDP,
  getPointerCoords,
  parseLiveViewConfig,
  useAutoLiveMotion,
  waitForIceGathering,
  type LiveViewConfig,
} from './liveViewHelpers';

export { activeAutoLiveStreams, computeStaggerDelayMs, parseLiveViewConfig, type LiveViewConfig };

function isFiniteSnapshotEndpoint(streamUrl: string): boolean {
  const raw = (streamUrl.split(/[?#]/)[0] ?? '').toLowerCase();
  let path = raw;
  try {
    path = new URL(raw, 'http://localhost').pathname.toLowerCase();
  } catch {
    /* keep raw path */
  }
  return (
    path.includes('/hearth-api/cameras/') ||
    /(?:^|\/)snapshot(?:$|[/.])/.test(path) ||
    path.startsWith('/api/camera_proxy/') ||
    /\.(?:jpe?g|png|webp)$/.test(path)
  );
}

function isContinuousMjpegUrl(
  streamType: LiveViewConfig['stream_type'],
  streamUrl: string,
): boolean {
  return streamType === 'mjpeg' && Boolean(streamUrl) && !isFiniteSnapshotEndpoint(streamUrl);
}

export function LiveViewWidget({ id }: WidgetComponentProps) {
  const entry = useWidget(id);
  const isCurrentPane = useIsCurrentPane();
  const isDocumentVisible = useDocumentVisible();
  const videoMode = useDisplayStore((s) => s.video.mode);

  const rawConfig = entry.meta?.config ?? entry.data;
  const config = parseLiveViewConfig(rawConfig, id);
  const {
    stream_url: streamUrl, stream_type: streamType, fit, title, poster_url: posterUrl,
    expand_on_click: expandOnClick, stream_id: streamId, priority, timeout_seconds: timeoutSeconds,
    controllable, control_url: controlUrl, expand_stream_url: expandStreamUrl, expand_stream_type: expandStreamType,
    refresh_interval_seconds: refreshIntervalSeconds, background_refresh_interval_seconds: backgroundRefreshIntervalSeconds,
    stagger_index: staggerIndex, idle_timeout_seconds: idleTimeoutSeconds,
    auto_live_on_motion_seconds: autoLiveOnMotionSeconds, max_auto_live_streams: maxAutoLiveStreams,
  } = config;

  const rootRef = useRef<HTMLDivElement | null>(null);
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const imgRef = useRef<HTMLImageElement | null>(null);

  const [isLoading, setIsLoading] = useState(false);
  const [isIntersecting, setIsIntersecting] = useState(() => true);
  const [isIdle, setIsIdle] = useState(false);

  const epochRef = useRef(0);
  const pcRef = useRef<RTCPeerConnection | null>(null);
  const abortControllerRef = useRef<AbortController | null>(null);
  const safetyTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const pointerDownCoordsRef = useRef<{ x: number; y: number } | null>(null);
  const lastDragTimeRef = useRef(0);
  const lastTriggerTimeRef = useRef(0);

  const [prevVideoMode, setPrevVideoMode] = useState(videoMode);
  if (videoMode !== prevVideoMode) {
    setPrevVideoMode(videoMode);
    if (videoMode === 'video' && isLoading) setIsLoading(false);
    else if (prevVideoMode === 'video') setIsIntersecting(true);
  }

  const isPaneActive = isCurrentPane && isDocumentVisible && videoMode !== 'video';
  if (!isPaneActive && isIdle) setIsIdle(false);

  useEffect(() => {
    if (videoMode === 'video' && safetyTimerRef.current) {
      clearTimeout(safetyTimerRef.current);
      safetyTimerRef.current = null;
    }
  }, [videoMode]);

  useEffect(() => {
    if (!isPaneActive) return;
    const idleMs = (idleTimeoutSeconds ?? 300) * 1000;
    let timerId: ReturnType<typeof setTimeout> | null = null;
    const armTimer = () => {
      if (timerId) clearTimeout(timerId);
      timerId = setTimeout(() => {
        setIsIdle(true);
        const store = useDisplayStore.getState();
        if (store.screen.currentScreen > 0) {
          store.setCurrentScreen(0);
          Promise.resolve(
            fetch('api/screen/select', {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ screen_index: 0 }),
            }),
          ).catch(() => { /* ignore */ });
        }
      }, idleMs);
    };
    const handleActivity = () => { setIsIdle(false); armTimer(); };
    armTimer();
    window.addEventListener('pointerdown', handleActivity, { passive: true });
    window.addEventListener('touchstart', handleActivity, { passive: true });
    window.addEventListener('keydown', handleActivity, { passive: true });
    return () => {
      if (timerId) clearTimeout(timerId);
      window.removeEventListener('pointerdown', handleActivity);
      window.removeEventListener('touchstart', handleActivity);
      window.removeEventListener('keydown', handleActivity);
    };
  }, [isPaneActive, idleTimeoutSeconds]);

  useEffect(() => {
    const el = rootRef.current;
    if (!el || typeof IntersectionObserver !== 'function') {
      setIsIntersecting(true);
      return;
    }
    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.target === el) setIsIntersecting(entry.isIntersecting);
        }
      },
      { threshold: 0.1 },
    );
    observer.observe(el);
    return () => { observer.disconnect(); };
  }, []);

  const stopDecoders = (clearMjpeg = true) => {
    epochRef.current += 1;
    if (abortControllerRef.current) {
      abortControllerRef.current.abort();
      abortControllerRef.current = null;
    }
    if (pcRef.current) {
      try { pcRef.current.close(); } catch { /* ignore */ }
      pcRef.current = null;
    }
    if (videoRef.current) {
      videoRef.current.srcObject = null;
      try { videoRef.current.removeAttribute('src'); } catch { /* ignore */ }
      try { videoRef.current.pause(); } catch { /* ignore */ }
    }
    if (clearMjpeg && imgRef.current) {
      imgRef.current.src = '';
      try { imgRef.current.removeAttribute('src'); } catch { /* ignore */ }
    }
  };

  const isContinuousMjpegStream = isContinuousMjpegUrl(streamType, streamUrl);
  const clearMjpeg = streamType !== 'mjpeg' || isContinuousMjpegStream;

  const shouldPlay =
    isCurrentPane && isDocumentVisible && isIntersecting && videoMode !== 'video' && !isIdle && Boolean(streamUrl);
  const shouldBackgroundRefresh =
    !isCurrentPane &&
    isDocumentVisible &&
    videoMode !== 'video' &&
    Boolean(streamUrl) &&
    streamType === 'mjpeg' &&
    !isContinuousMjpegStream;
  const windowSec = autoLiveOnMotionSeconds ?? 0;
  const canPollMotion = shouldPlay && streamType === 'mjpeg' && Boolean(expandStreamUrl) && windowSec > 0;
  const activeAutoLive = useAutoLiveMotion(
    id, streamId, streamUrl, canPollMotion, windowSec, maxAutoLiveStreams ?? 2,
  );

  useEffect(() => {
    if (!shouldPlay) {
      stopDecoders(clearMjpeg);
      return;
    }
    const epoch = ++epochRef.current;
    const ac = new AbortController();
    const isStale = (): boolean => epochRef.current !== epoch || ac.signal.aborted;
    const closeQuietly = (pc: RTCPeerConnection) => {
      try { pc.close(); } catch { /* ignore */ }
    };
    abortControllerRef.current = ac;

    const effectiveType = activeAutoLive ? 'webrtc' : streamType;
    const effectiveUrl = activeAutoLive ? expandStreamUrl : streamUrl;

    if (effectiveType === 'mjpeg') {
      if (imgRef.current) imgRef.current.src = streamUrl;
      const intervalSec = refreshIntervalSeconds ?? 15;
      if (intervalSec <= 0) return () => { stopDecoders(clearMjpeg); };
      const intervalMs = intervalSec * 1000;
      const staggerMs = computeStaggerDelayMs(id, staggerIndex, 1600);
      let intervalId: ReturnType<typeof setInterval> | null = null;
      const timeoutId = setTimeout(() => {
        if (imgRef.current) imgRef.current.src = buildSnapshotUrl(streamUrl, Date.now());
        intervalId = setInterval(() => {
          if (imgRef.current) imgRef.current.src = buildSnapshotUrl(streamUrl, Date.now());
        }, intervalMs);
      }, staggerMs);
      return () => {
        clearTimeout(timeoutId);
        if (intervalId) clearInterval(intervalId);
        stopDecoders(clearMjpeg);
      };
    }
    if (effectiveType === 'hls') {
      if (videoRef.current) {
        videoRef.current.muted = true;
        videoRef.current.volume = 0;
        videoRef.current.src = effectiveUrl;
        videoRef.current.play().catch(() => { /* autoplay can be refused; the element stays paused */ });
      }
      return () => { stopDecoders(clearMjpeg); };
    }

    const startWebRTC = async () => {
      const PeerConnection =
        typeof RTCPeerConnection !== 'undefined'
          ? RTCPeerConnection
          : (window as unknown as { RTCPeerConnection?: typeof RTCPeerConnection }).RTCPeerConnection;
      if (!PeerConnection) return;
      if (videoRef.current) {
        videoRef.current.muted = true;
        videoRef.current.volume = 0;
      }
      try {
        const pc = new PeerConnection();
        pcRef.current = pc;
        if (typeof pc.addTransceiver === 'function') pc.addTransceiver('video', { direction: 'recvonly' });
        pc.ontrack = (event) => {
          if (epochRef.current === epoch && videoRef.current) attachTrackToVideo(videoRef.current, event);
        };
        const offer = await pc.createOffer();
        if (isStale()) { closeQuietly(pc); return; }
        await pc.setLocalDescription(offer);
        if (isStale()) { closeQuietly(pc); return; }
        await waitForIceGathering(pc, ac.signal);
        if (isStale()) { closeQuietly(pc); return; }
        const answerSdp = await exchangeWhepSDP(effectiveUrl, pc.localDescription?.sdp ?? '', ac.signal);
        if (isStale()) { closeQuietly(pc); return; }
        await pc.setRemoteDescription({ type: 'answer', sdp: answerSdp });
        if (isStale()) { closeQuietly(pc); return; }
        videoRef.current?.play().catch(() => { /* autoplay can be refused; the element stays paused */ });
      } catch (err) {
        if (epochRef.current === epoch && !ac.signal.aborted) {
          console.warn(`[MirrormereLiveView] WebRTC negotiation error for ${id}:`, err);
        }
      }
    };

    void startWebRTC();
    return () => { stopDecoders(clearMjpeg); };
  }, [shouldPlay, streamUrl, streamType, expandStreamUrl, activeAutoLive, id, refreshIntervalSeconds, staggerIndex, clearMjpeg]);

  useEffect(() => {
    if (!shouldBackgroundRefresh) return;
    if (imgRef.current && !imgRef.current.getAttribute('src')) imgRef.current.src = streamUrl;
    const bgIntervalSec = backgroundRefreshIntervalSeconds ?? 300;
    if (bgIntervalSec <= 0) return;
    const bgIntervalMs = bgIntervalSec * 1000;
    const staggerMs = computeStaggerDelayMs(id, staggerIndex, 5000);
    let intervalId: ReturnType<typeof setInterval> | null = null;
    const timeoutId = setTimeout(() => {
      intervalId = setInterval(() => {
        if (imgRef.current) imgRef.current.src = buildSnapshotUrl(streamUrl, Date.now());
      }, bgIntervalMs);
    }, staggerMs);
    return () => {
      clearTimeout(timeoutId);
      if (intervalId) clearInterval(intervalId);
    };
  }, [shouldBackgroundRefresh, streamUrl, id, backgroundRefreshIntervalSeconds, staggerIndex]);

  const triggerExpand = async () => {
    if (!expandOnClick || isLoading || !streamUrl) return;
    const now = Date.now();
    if (now - lastTriggerTimeRef.current < 350) return;
    lastTriggerTimeRef.current = now;

    setIsLoading(true);
    if (safetyTimerRef.current) clearTimeout(safetyTimerRef.current);
    safetyTimerRef.current = setTimeout(() => {
      setIsLoading(false);
      safetyTimerRef.current = null;
    }, 2000);

    const payload: Record<string, unknown> = {
      id: streamId, stream_url: expandStreamUrl, type: expandStreamType,
      priority, controllable, control_url: controlUrl,
      ...(timeoutSeconds !== undefined && timeoutSeconds > 0 ? { timeout_seconds: timeoutSeconds } : {}),
    };

    if (controllable && controlUrl) {
      fetch(controlUrl, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: streamId, action: 'wake' }),
      }).catch((err: unknown) => {
        console.warn('[MirrormereLiveView] Wake action failed:', err);
      });
    }

    try {
      const res = await fetch('api/video/trigger', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      });
      if (!res.ok) throw new Error(`Trigger failed with HTTP ${res.status}`);
    } catch (err) {
      console.warn(`[MirrormereLiveView] Trigger failed for ${streamId}:`, err);
      setIsLoading(false);
      if (safetyTimerRef.current) {
        clearTimeout(safetyTimerRef.current);
        safetyTimerRef.current = null;
      }
    }
  };

  const handlePointerDown = (e: React.PointerEvent) => {
    pointerDownCoordsRef.current = getPointerCoords(e);
  };
  const handlePointerUp = (e: React.PointerEvent) => {
    const start = pointerDownCoordsRef.current;
    pointerDownCoordsRef.current = null;
    const coords = getPointerCoords(e);
    if (start && coords && Math.hypot(coords.x - start.x, coords.y - start.y) >= 15) {
      lastDragTimeRef.current = Date.now();
      return;
    }
    void triggerExpand();
  };
  const handleClick = () => {
    if (Date.now() - lastDragTimeRef.current < 400) return;
    void triggerExpand();
  };

  useEffect(() => () => {
    if (safetyTimerRef.current) {
      clearTimeout(safetyTimerRef.current);
      safetyTimerRef.current = null;
    }
  }, []);

  return (
    <div
      ref={rootRef}
      className={'widget-live-view' + (isLoading ? ' loading' : '')}
      data-fit={fit}
      data-expand={expandOnClick ? 'true' : 'false'}
      onPointerDown={handlePointerDown}
      onPointerUp={handlePointerUp}
      onClick={handleClick}
    >
      <div className="live-view-viewport">
        <video
          ref={videoRef}
          className="live-view-media"
          autoPlay
          playsInline
          muted
          poster={posterUrl || undefined}
          style={streamType === 'mjpeg' && !activeAutoLive ? { display: 'none' } : undefined}
        />
        <img
          ref={imgRef}
          className="live-view-mjpeg"
          alt={title}
          style={streamType === 'mjpeg' && !activeAutoLive ? undefined : { display: 'none' }}
        />
        <div className="live-view-badge">
          <span className="live-view-dot" />
          <span className="live-view-title">{title}</span>
        </div>
        <div className="live-view-spinner">
          <div className="video-spinner-ring" />
        </div>
      </div>
    </div>
  );
}

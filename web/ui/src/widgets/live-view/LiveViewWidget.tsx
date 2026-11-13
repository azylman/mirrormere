import './LiveViewWidget.css';
import { useEffect, useRef, useState } from 'react';
import type { WidgetComponentProps } from '../registry';
import { useWidget } from '../../store/hooks';
import { useDisplayStore } from '../../store';
import { useIsCurrentPane } from '../../carousel/PaneContext';
import { useDocumentVisible } from '../../lifecycle/visibility';

export interface LiveViewConfig {
  stream_url: string;
  stream_type: 'webrtc' | 'mjpeg' | 'hls';
  fit: 'contain' | 'cover';
  title: string;
  poster_url: string;
  expand_on_click: boolean;
  stream_id: string;
  priority: 'persistent' | 'temporary';
  timeout_seconds?: number;
  controllable: boolean;
  control_url: string;
  expand_stream_url: string;
  expand_stream_type: string;
  refresh_interval_seconds?: number;
  background_refresh_interval_seconds?: number;
  stagger_index?: number;
  idle_timeout_seconds?: number;
}

export function computeStaggerDelayMs(id: string, staggerIndex?: number, stepMs = 1600): number {
  if (typeof staggerIndex === 'number' && !isNaN(staggerIndex) && staggerIndex >= 0) {
    return Math.floor(staggerIndex) * stepMs;
  }
  let hash = 0;
  for (let i = 0; i < id.length; i++) hash = ((hash << 5) - hash + id.charCodeAt(i)) | 0;
  return (Math.abs(hash) % 9) * stepMs;
}

function buildSnapshotUrl(baseUrl: string, timestamp: number): string {
  return `${baseUrl}${baseUrl.includes('?') ? '&' : '?'}t=${timestamp}`;
}

function unwrapConfigObject(raw: unknown): Record<string, unknown> {
  let val = raw;
  for (let i = 0; i < 2 && typeof val === 'string'; i++) {
    try { val = JSON.parse(val); } catch { break; }
  }
  return val && typeof val === 'object' && !Array.isArray(val) ? (val as Record<string, unknown>) : {};
}

function parsePositiveNumber(val: unknown): number | undefined {
  if (val === undefined || val === null || val === '') return undefined;
  const n = Number(val);
  return !isNaN(n) && n > 0 ? n : undefined;
}

function parseNonNegativeNumber(val: unknown, floorInt = false): number | undefined {
  if (val === undefined || val === null || val === '') return undefined;
  const n = Number(val);
  return !isNaN(n) && n >= 0 ? (floorInt ? Math.floor(n) : n) : undefined;
}

function parseNonEmptyStr(val: unknown, fallback: string): string {
  return typeof val === 'string' && val.trim() !== '' ? val : fallback;
}

export function parseLiveViewConfig(raw: unknown, fallbackId = ''): LiveViewConfig {
  const obj = unwrapConfigObject(raw);
  const streamUrl = typeof obj.stream_url === 'string' ? obj.stream_url : '';
  const rawType = typeof obj.stream_type === 'string' ? obj.stream_type.toLowerCase() : '';
  const streamType = rawType === 'mjpeg' || rawType === 'hls' ? rawType : 'webrtc';
  const isMjpeg = streamType === 'mjpeg';

  return {
    stream_url: streamUrl,
    stream_type: streamType,
    fit: obj.fit === 'cover' ? 'cover' : 'contain',
    title: parseNonEmptyStr(obj.title, 'Live View'),
    poster_url: typeof obj.poster_url === 'string' ? obj.poster_url : '',
    expand_on_click: obj.expand_on_click !== false,
    stream_id: parseNonEmptyStr(obj.stream_id, fallbackId),
    priority: obj.priority === 'temporary' ? 'temporary' : 'persistent',
    timeout_seconds: parsePositiveNumber(obj.timeout_seconds),
    controllable: Boolean(obj.controllable),
    control_url: typeof obj.control_url === 'string' ? obj.control_url : '',
    expand_stream_url: parseNonEmptyStr(obj.expand_stream_url, streamUrl),
    expand_stream_type: parseNonEmptyStr(obj.expand_stream_type, streamType),
    refresh_interval_seconds: parseNonNegativeNumber(obj.refresh_interval_seconds) ?? (isMjpeg ? 15 : undefined),
    background_refresh_interval_seconds: parseNonNegativeNumber(obj.background_refresh_interval_seconds) ?? (isMjpeg ? 300 : undefined),
    stagger_index: parseNonNegativeNumber(obj.stagger_index, true),
    idle_timeout_seconds: parsePositiveNumber(obj.idle_timeout_seconds),
  };
}

function waitForIceGathering(pc: RTCPeerConnection, signal: AbortSignal): Promise<void> {
  if (pc.iceGatheringState === 'complete') return Promise.resolve();
  return new Promise<void>((resolve) => {
    let settled = false;
    let timer: ReturnType<typeof setTimeout> | null = null;
    const cleanup = () => {
      if (settled) return;
      settled = true;
      if (timer) { clearTimeout(timer); timer = null; }
      if (typeof pc.removeEventListener === 'function') pc.removeEventListener('icegatheringstatechange', onIceChange);
      signal.removeEventListener('abort', cleanup);
      resolve();
    };
    const onIceChange = () => { if (pc.iceGatheringState === 'complete') cleanup(); };
    timer = setTimeout(cleanup, 500);
    if (typeof pc.addEventListener === 'function') pc.addEventListener('icegatheringstatechange', onIceChange);
    signal.addEventListener('abort', cleanup);
  });
}

function attachTrackToVideo(video: HTMLVideoElement, event: RTCTrackEvent): void {
  if (event.streams && event.streams[0]) {
    video.srcObject = event.streams[0];
  } else if (event.track) {
    const MediaStreamCtor = typeof MediaStream !== 'undefined'
      ? MediaStream
      : (window as unknown as { MediaStream?: typeof MediaStream }).MediaStream;
    if (MediaStreamCtor) {
      try {
        video.srcObject = new MediaStreamCtor([event.track]);
      } catch {
        const ms = new MediaStreamCtor();
        if (typeof ms.addTrack === 'function') ms.addTrack(event.track);
        video.srcObject = ms;
      }
    }
  }
  video.play?.()?.catch?.(() => {});
}

async function exchangeWhepSDP(streamUrl: string, sdpOffer: string, signal: AbortSignal): Promise<string> {
  const res = await fetch(streamUrl, {
    method: 'POST',
    headers: { 'Content-Type': 'application/sdp' },
    body: sdpOffer,
    signal,
  });
  if (!res.ok) throw new Error(`SDP exchange failed with HTTP ${res.status}`);
  const resText = await res.text();
  try {
    const json = JSON.parse(resText);
    if (json && typeof json.sdp === 'string') return json.sdp;
  } catch { /* use raw text */ }
  return resText;
}

function getPointerCoords(e: React.PointerEvent | React.TouchEvent | React.MouseEvent): { x: number; y: number } | null {
  const touchEvt = e as unknown as React.TouchEvent;
  if (touchEvt.changedTouches && touchEvt.changedTouches.length > 0) {
    return { x: touchEvt.changedTouches[0].clientX, y: touchEvt.changedTouches[0].clientY };
  }
  const mouseEvt = e as unknown as React.MouseEvent;
  if (typeof mouseEvt.clientX === 'number' && !isNaN(mouseEvt.clientX) && typeof mouseEvt.clientY === 'number' && !isNaN(mouseEvt.clientY)) {
    return { x: mouseEvt.clientX, y: mouseEvt.clientY };
  }
  return null;
}

export function LiveViewWidget({ id }: WidgetComponentProps) {
  const entry = useWidget(id);
  const isCurrentPane = useIsCurrentPane();
  const isDocumentVisible = useDocumentVisible();
  const videoMode = useDisplayStore((s) => s.video.mode || (s.video.primary ? 'video' : 'widgets'));

  const rawConfig = entry?.meta?.config ?? entry?.data;
  const config = parseLiveViewConfig(rawConfig, id);

  const {
    stream_url: streamUrl, stream_type: streamType, fit, title, poster_url: posterUrl,
    expand_on_click: expandOnClick, stream_id: streamId, priority, timeout_seconds: timeoutSeconds,
    controllable, control_url: controlUrl, expand_stream_url: expandStreamUrl, expand_stream_type: expandStreamType,
    refresh_interval_seconds: refreshIntervalSeconds,
    background_refresh_interval_seconds: backgroundRefreshIntervalSeconds,
    stagger_index: staggerIndex,
    idle_timeout_seconds: idleTimeoutSeconds,
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
    if (idleMs <= 0) return;

    let timerId: ReturnType<typeof setTimeout> | null = null;
    const armTimer = () => {
      if (timerId) clearTimeout(timerId);
      timerId = setTimeout(() => {
        setIsIdle(true);
        const store = useDisplayStore.getState();
        if (store.screen.currentScreen > 0) {
          store.setCurrentScreen(0);
          Promise.resolve(fetch('api/screen/select', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ screen_index: 0 }) })).catch(() => {});
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
          if (entry.target === el) setIsIntersecting(Boolean(entry.isIntersecting));
        }
      },
      { threshold: 0.1 },
    );
    observer.observe(el);
    return () => observer.disconnect();
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
      try { videoRef.current.pause?.(); } catch { /* ignore */ }
    }
    if (clearMjpeg && imgRef.current) {
      imgRef.current.src = '';
      try { imgRef.current.removeAttribute('src'); } catch { /* ignore */ }
    }
  };

  const shouldPlay = isCurrentPane && isDocumentVisible && isIntersecting && videoMode !== 'video' && !isIdle && Boolean(streamUrl);
  const shouldBackgroundRefresh = !isCurrentPane && isDocumentVisible && videoMode !== 'video' && Boolean(streamUrl) && streamType === 'mjpeg';

  useEffect(() => {
    if (!shouldPlay) {
      stopDecoders(streamType !== 'mjpeg');
      return;
    }
    const epoch = ++epochRef.current;
    const ac = new AbortController();
    abortControllerRef.current = ac;

    if (streamType === 'mjpeg') {
      if (imgRef.current) imgRef.current.src = streamUrl;
      const intervalSec = refreshIntervalSeconds ?? 15;
      if (intervalSec <= 0) return () => stopDecoders(false);
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
        stopDecoders(false);
      };
    }
    if (streamType === 'hls') {
      if (videoRef.current) {
        videoRef.current.muted = true;
        videoRef.current.volume = 0;
        videoRef.current.src = streamUrl;
        videoRef.current.play?.()?.catch?.(() => {});
      }
      return () => stopDecoders();
    }

    const startWebRTC = async () => {
      const PeerConnection = typeof RTCPeerConnection !== 'undefined'
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
          if (epochRef.current !== epoch || !videoRef.current) return;
          attachTrackToVideo(videoRef.current, event);
        };

        const offer = await pc.createOffer();
        if (epochRef.current !== epoch || ac.signal.aborted) { try { pc.close(); } catch { /* ignore */ } return; }
        await pc.setLocalDescription(offer);
        if (epochRef.current !== epoch || ac.signal.aborted) { try { pc.close(); } catch { /* ignore */ } return; }
        await waitForIceGathering(pc, ac.signal);
        if (epochRef.current !== epoch || ac.signal.aborted) { try { pc.close(); } catch { /* ignore */ } return; }
        const answerSdp = await exchangeWhepSDP(streamUrl, pc.localDescription?.sdp ?? '', ac.signal);
        if (epochRef.current !== epoch || ac.signal.aborted) { try { pc.close(); } catch { /* ignore */ } return; }
        await pc.setRemoteDescription({ type: 'answer', sdp: answerSdp });
        if (epochRef.current !== epoch || ac.signal.aborted) { try { pc.close(); } catch { /* ignore */ } return; }
        videoRef.current?.play?.()?.catch?.(() => {});
      } catch (err) {
        if (epochRef.current === epoch && !ac.signal.aborted) {
          console.warn(`[MirrormereLiveView] WebRTC negotiation error for ${id}:`, err);
        }
      }
    };

    void startWebRTC();
    return () => stopDecoders();
  }, [shouldPlay, streamUrl, streamType, id, refreshIntervalSeconds, staggerIndex]);

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
      id: streamId,
      stream_url: expandStreamUrl || streamUrl,
      type: expandStreamType || streamType || 'webrtc',
      priority: priority || 'persistent',
      controllable: Boolean(controllable),
      control_url: controlUrl || '',
      ...(timeoutSeconds !== undefined && Number(timeoutSeconds) > 0 ? { timeout_seconds: Number(timeoutSeconds) } : {}),
    };

    if (controllable && controlUrl) {
      fetch(controlUrl, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: streamId, action: 'wake' }),
      }).catch((err) => { console.warn('[MirrormereLiveView] Wake action failed:', err); });
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

  const handlePointerDown = (e: React.PointerEvent) => { pointerDownCoordsRef.current = getPointerCoords(e); };
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
          style={streamType === 'mjpeg' ? { display: 'none' } : undefined}
        />
        <img
          ref={imgRef}
          className="live-view-mjpeg"
          alt={title}
          style={streamType === 'mjpeg' ? undefined : { display: 'none' }}
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

import { useEffect, useState } from 'react';
import type React from 'react';

export interface LiveViewConfig {
  stream_url: string;
  stream_type: 'webrtc' | 'mjpeg' | 'hls';
  fit: 'contain' | 'cover';
  title: string;
  poster_url: string;
  expand_on_click: boolean;
  stream_id: string;
  priority: 'persistent' | 'temporary';
  timeout_seconds?: number | undefined;
  controllable: boolean;
  control_url: string;
  expand_stream_url: string;
  expand_stream_type: string;
  refresh_interval_seconds?: number | undefined;
  background_refresh_interval_seconds?: number | undefined;
  stagger_index?: number | undefined;
  idle_timeout_seconds?: number | undefined;
  auto_live_on_motion_seconds?: number | undefined;
  max_auto_live_streams?: number | undefined;
}

export const activeAutoLiveStreams = new Map<string, number>();
const autoLiveDemoters = new Map<string, () => void>();

interface CameraStatusItem {
  id?: string | undefined;
  stream_url?: string | undefined;
  snapshot_url?: string | undefined;
  webrtc_url?: string | undefined;
  auto_live?: boolean | undefined;
  last_event_epoch?: number | undefined;
}

function releaseAutoLiveSlot(widgetId: string): void {
  activeAutoLiveStreams.delete(widgetId);
  autoLiveDemoters.delete(widgetId);
}

function matchesCamera(cam: CameraStatusItem, streamId: string, streamUrl: string): boolean {
  const norm = (s: string) => s.trim().toLowerCase().replace(/^cam[-_]/, '').replace(/-/g, '_');
  const camId = norm(cam.id ?? '');
  if (camId && camId === norm(streamId)) return true;
  const cleanUrl = streamUrl.split('?')[0];
  if (!cleanUrl) return false;
  return (
    cam.stream_url === cleanUrl ||
    cam.snapshot_url === cleanUrl ||
    Boolean(camId && cleanUrl.toLowerCase().includes(`/${camId}/`))
  );
}

function claimAutoLiveSlot(
  widgetId: string,
  eventEpoch: number,
  maxStreams: number,
  onDemote: () => void,
): boolean {
  activeAutoLiveStreams.set(widgetId, eventEpoch);
  autoLiveDemoters.set(widgetId, onDemote);
  if (activeAutoLiveStreams.size > maxStreams) {
    let oldestId = widgetId;
    let oldestEpoch = eventEpoch;
    for (const [k, v] of activeAutoLiveStreams.entries()) {
      if (v < oldestEpoch) {
        oldestEpoch = v;
        oldestId = k;
      }
    }
    const evictedDemote = autoLiveDemoters.get(oldestId);
    releaseAutoLiveSlot(oldestId);
    if (oldestId !== widgetId) evictedDemote?.();
  }
  return activeAutoLiveStreams.has(widgetId);
}

export function computeStaggerDelayMs(id: string, staggerIndex?: number, stepMs = 1600): number {
  if (typeof staggerIndex === 'number' && !isNaN(staggerIndex) && staggerIndex >= 0) {
    return Math.floor(staggerIndex) * stepMs;
  }
  let hash = 0;
  for (let i = 0; i < id.length; i++) {
    hash = ((hash << 5) - hash + id.charCodeAt(i)) | 0;
  }
  return (Math.abs(hash) % 9) * stepMs;
}

export function buildSnapshotUrl(baseUrl: string, timestamp: number): string {
  return `${baseUrl}${baseUrl.includes('?') ? '&' : '?'}t=${timestamp}`;
}

function unwrapConfigObject(raw: unknown): Record<string, unknown> {
  let val: unknown = raw;
  for (let i = 0; i < 2 && typeof val === 'string'; i++) {
    try {
      val = JSON.parse(val) as unknown;
    } catch {
      break;
    }
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
    background_refresh_interval_seconds:
      parseNonNegativeNumber(obj.background_refresh_interval_seconds) ?? (isMjpeg ? 300 : undefined),
    stagger_index: parseNonNegativeNumber(obj.stagger_index, true),
    idle_timeout_seconds: parsePositiveNumber(obj.idle_timeout_seconds),
    auto_live_on_motion_seconds: parsePositiveNumber(obj.auto_live_on_motion_seconds),
    max_auto_live_streams: parsePositiveNumber(obj.max_auto_live_streams),
  };
}

export function waitForIceGathering(pc: RTCPeerConnection, signal: AbortSignal): Promise<void> {
  if (pc.iceGatheringState === 'complete') return Promise.resolve();
  return new Promise<void>((resolve) => {
    let settled = false;
    let timer: ReturnType<typeof setTimeout> | null = null;
    const cleanup = () => {
      if (settled) return;
      settled = true;
      if (timer) {
        clearTimeout(timer);
        timer = null;
      }
      if (typeof pc.removeEventListener === 'function') {
        pc.removeEventListener('icegatheringstatechange', onIceChange);
      }
      signal.removeEventListener('abort', cleanup);
      resolve();
    };
    const onIceChange = () => {
      if (pc.iceGatheringState === 'complete') cleanup();
    };
    timer = setTimeout(cleanup, 500);
    if (typeof pc.addEventListener === 'function') {
      pc.addEventListener('icegatheringstatechange', onIceChange);
    }
    signal.addEventListener('abort', cleanup);
  });
}

export function attachTrackToVideo(video: HTMLVideoElement, event: RTCTrackEvent): void {
  if (event.streams[0]) {
    video.srcObject = event.streams[0];
  } else {
    const MediaStreamCtor =
      typeof MediaStream !== 'undefined'
        ? MediaStream
        : (window as unknown as { MediaStream?: typeof MediaStream }).MediaStream;
    if (MediaStreamCtor) {
      video.srcObject = new MediaStreamCtor([event.track]);
    }
  }
  video.play().catch(() => {
    /* autoplay can be refused; the element stays paused */
  });
}

export async function exchangeWhepSDP(streamUrl: string, sdpOffer: string, signal: AbortSignal): Promise<string> {
  const res = await fetch(streamUrl, {
    method: 'POST',
    headers: { 'Content-Type': 'application/sdp' },
    body: sdpOffer,
    signal,
  });
  if (!res.ok) throw new Error(`SDP exchange failed with HTTP ${res.status}`);
  const resText = await res.text();
  try {
    const json = JSON.parse(resText) as { sdp?: unknown } | null;
    if (json && typeof json.sdp === 'string') return json.sdp;
  } catch {
    /* use raw text */
  }
  return resText;
}

export function getPointerCoords(
  e: React.PointerEvent | React.TouchEvent | React.MouseEvent,
): { x: number; y: number } | null {
  const touch = 'changedTouches' in e ? e.changedTouches[0] : undefined;
  if (touch) return { x: touch.clientX, y: touch.clientY };
  const mouseEvt = e as unknown as React.MouseEvent;
  if (
    typeof mouseEvt.clientX === 'number' &&
    !isNaN(mouseEvt.clientX) &&
    typeof mouseEvt.clientY === 'number' &&
    !isNaN(mouseEvt.clientY)
  ) {
    return { x: mouseEvt.clientX, y: mouseEvt.clientY };
  }
  return null;
}

export function useAutoLiveMotion(
  id: string,
  streamId: string,
  streamUrl: string,
  canPoll: boolean,
  windowSec: number,
  maxStreams = 2,
): boolean {
  const [isAutoLive, setIsAutoLive] = useState(false);
  if (!canPoll && isAutoLive) setIsAutoLive(false);

  useEffect(() => {
    if (!canPoll) {
      releaseAutoLiveSlot(id);
      return;
    }
    const ac = new AbortController();
    let calmTimer: ReturnType<typeof setTimeout> | null = null;
    const clearCalm = () => {
      if (calmTimer) {
        clearTimeout(calmTimer);
        calmTimer = null;
      }
    };
    const onEvict = () => {
      clearCalm();
      setIsAutoLive(false);
    };
    const demote = () => {
      releaseAutoLiveSlot(id);
      onEvict();
    };

    const pollMotion = async () => {
      try {
        const res = await fetch('/hearth-api/cameras', { signal: ac.signal });
        if (!res.ok) return;
        const data = JSON.parse(await res.text()) as { cameras?: CameraStatusItem[] };
        if (ac.signal.aborted) return;
        const list = Array.isArray(data.cameras) ? data.cameras : [];
        const cam = list.find((c) => matchesCamera(c, streamId, streamUrl));
        if (!cam) {
          demote();
          return;
        }
        const nowSec = Date.now() / 1000;
        const lastEpoch =
          typeof cam.last_event_epoch === 'number' && cam.last_event_epoch > 0 ? cam.last_event_epoch : 0;
        const elapsedSec = lastEpoch > 0 ? Math.max(0, nowSec - lastEpoch) : 0;
        const active =
          cam.auto_live === true || (cam.auto_live !== false && lastEpoch > 0 && elapsedSec < windowSec);
        if (!active) {
          demote();
          return;
        }
        if (claimAutoLiveSlot(id, lastEpoch || nowSec, maxStreams, onEvict)) {
          setIsAutoLive(true);
          clearCalm();
          const remMs =
            lastEpoch > 0
              ? Math.min(windowSec * 1000, Math.max(50, (windowSec - elapsedSec) * 1000))
              : windowSec * 1000;
          calmTimer = setTimeout(demote, remMs);
        } else {
          onEvict();
        }
      } catch {
        /* ignore */
      }
    };

    void pollMotion();
    const pollInterval = setInterval(() => {
      void pollMotion();
    }, 5000);
    return () => {
      ac.abort();
      clearInterval(pollInterval);
      clearCalm();
      releaseAutoLiveSlot(id);
    };
  }, [canPoll, windowSec, maxStreams, id, streamId, streamUrl]);

  return canPoll && isAutoLive;
}

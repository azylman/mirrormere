import { memo, useEffect, useRef, useState, useCallback, useMemo } from 'react';
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
}

export function parseLiveViewConfig(raw: unknown, fallbackId = ''): LiveViewConfig {
  let obj: Record<string, unknown> = {};
  if (raw && typeof raw === 'object' && !Array.isArray(raw)) {
    obj = raw as Record<string, unknown>;
  } else if (typeof raw === 'string') {
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
        obj = parsed as Record<string, unknown>;
      }
    } catch {
      // ignore JSON parse failure
    }
  }

  const streamUrl = typeof obj.stream_url === 'string' ? obj.stream_url : '';
  const rawType = typeof obj.stream_type === 'string' ? obj.stream_type.toLowerCase() : '';
  const streamType: 'webrtc' | 'mjpeg' | 'hls' =
    rawType === 'mjpeg' || rawType === 'hls' ? rawType : 'webrtc';
  const fit: 'contain' | 'cover' = obj.fit === 'cover' ? 'cover' : 'contain';
  const title = typeof obj.title === 'string' && obj.title.trim() !== '' ? obj.title : 'Live View';
  const posterUrl = typeof obj.poster_url === 'string' ? obj.poster_url : '';
  const expandOnClick = obj.expand_on_click !== false;
  const streamId =
    typeof obj.stream_id === 'string' && obj.stream_id.trim() !== ''
      ? obj.stream_id
      : fallbackId;
  const priority: 'persistent' | 'temporary' =
    obj.priority === 'temporary' ? 'temporary' : 'persistent';
  const controllable = Boolean(obj.controllable);
  const controlUrl = typeof obj.control_url === 'string' ? obj.control_url : '';
  const expandStreamUrl =
    typeof obj.expand_stream_url === 'string' && obj.expand_stream_url.trim() !== ''
      ? obj.expand_stream_url
      : streamUrl;
  const expandStreamType =
    typeof obj.expand_stream_type === 'string' && obj.expand_stream_type.trim() !== ''
      ? obj.expand_stream_type
      : streamType || 'webrtc';

  let timeoutSeconds: number | undefined;
  if (
    obj.timeout_seconds !== undefined &&
    obj.timeout_seconds !== null &&
    !isNaN(Number(obj.timeout_seconds))
  ) {
    const num = Number(obj.timeout_seconds);
    if (num > 0) {
      timeoutSeconds = num;
    }
  }

  return {
    stream_url: streamUrl,
    stream_type: streamType,
    fit,
    title,
    poster_url: posterUrl,
    expand_on_click: expandOnClick,
    stream_id: streamId,
    priority,
    timeout_seconds: timeoutSeconds,
    controllable,
    control_url: controlUrl,
    expand_stream_url: expandStreamUrl,
    expand_stream_type: expandStreamType,
  };
}

export const LiveViewWidget = memo(function LiveViewWidget({ id }: WidgetComponentProps) {
  const entry = useWidget(id);
  const isCurrentPane = useIsCurrentPane();
  const isDocumentVisible = useDocumentVisible();
  const videoMode = useDisplayStore(
    (s) => s.video.mode || (s.video.primary ? 'video' : 'widgets'),
  );

  const rawConfig = entry?.meta?.config ?? entry?.data;
  const config = useMemo(() => parseLiveViewConfig(rawConfig, id), [rawConfig, id]);

  const {
    stream_url: streamUrl,
    stream_type: streamType,
    fit,
    title,
    poster_url: posterUrl,
    expand_on_click: expandOnClick,
    stream_id: streamId,
    priority,
    timeout_seconds: timeoutSeconds,
    controllable,
    control_url: controlUrl,
    expand_stream_url: expandStreamUrl,
    expand_stream_type: expandStreamType,
  } = config;

  const rootRef = useRef<HTMLDivElement | null>(null);
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const imgRef = useRef<HTMLImageElement | null>(null);

  const [isLoading, setIsLoading] = useState(false);
  const [isIntersecting, setIsIntersecting] = useState(true);

  const epochRef = useRef(0);
  const pcRef = useRef<RTCPeerConnection | null>(null);
  const abortControllerRef = useRef<AbortController | null>(null);
  const safetyTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const pointerDownCoordsRef = useRef<{ x: number; y: number } | null>(null);
  const lastDragTimeRef = useRef(0);
  const lastTriggerTimeRef = useRef(0);

  // Clear loading state when video takes over fullscreen display,
  // and resume intersecting state when returning to widgets mode.
  const prevVideoModeRef = useRef(videoMode);
  useEffect(() => {
    if (videoMode === 'video') {
      setIsLoading(false);
      if (safetyTimerRef.current) {
        clearTimeout(safetyTimerRef.current);
        safetyTimerRef.current = null;
      }
    } else if (prevVideoModeRef.current === 'video') {
      setIsIntersecting(true);
    }
    prevVideoModeRef.current = videoMode;
  }, [videoMode]);

  // Observe root element visibility in viewport
  useEffect(() => {
    const el = rootRef.current;
    if (!el || typeof IntersectionObserver !== 'function') {
      setIsIntersecting(true);
      return;
    }

    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.target === el) {
            setIsIntersecting(Boolean(entry.isIntersecting));
          }
        }
      },
      { threshold: 0.1 },
    );

    observer.observe(el);
    return () => {
      observer.disconnect();
    };
  }, []);

  const stopDecoders = useCallback(() => {
    epochRef.current += 1;
    if (abortControllerRef.current) {
      abortControllerRef.current.abort();
      abortControllerRef.current = null;
    }
    if (pcRef.current) {
      try {
        pcRef.current.close();
      } catch {
        // ignore close error
      }
      pcRef.current = null;
    }
    if (videoRef.current) {
      videoRef.current.srcObject = null;
      try {
        videoRef.current.removeAttribute('src');
      } catch {
        // ignore attribute error
      }
      try {
        videoRef.current.pause?.();
      } catch {
        // ignore pause error
      }
    }
    if (imgRef.current) {
      imgRef.current.src = '';
      try {
        imgRef.current.removeAttribute('src');
      } catch {
        // ignore attribute error
      }
    }
  }, []);

  const shouldPlay =
    isCurrentPane &&
    isDocumentVisible &&
    isIntersecting &&
    videoMode !== 'video' &&
    Boolean(streamUrl);

  useEffect(() => {
    if (!shouldPlay) {
      stopDecoders();
      return;
    }

    const epoch = ++epochRef.current;
    const ac = new AbortController();
    abortControllerRef.current = ac;

    if (streamType === 'mjpeg') {
      if (imgRef.current) {
        imgRef.current.src = streamUrl;
      }
      return () => {
        stopDecoders();
      };
    }

    if (streamType === 'hls') {
      if (videoRef.current) {
        videoRef.current.muted = true;
        videoRef.current.volume = 0;
        videoRef.current.src = streamUrl;
        videoRef.current.play?.()?.catch?.(() => {});
      }
      return () => {
        stopDecoders();
      };
    }

    // Default WebRTC playback
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

        if (typeof pc.addTransceiver === 'function') {
          pc.addTransceiver('video', { direction: 'recvonly' });
        }

        pc.ontrack = (event) => {
          if (epochRef.current !== epoch) return;
          const video = videoRef.current;
          if (!video) return;

          if (event.streams && event.streams[0]) {
            video.srcObject = event.streams[0];
          } else if (event.track) {
            const MediaStreamCtor =
              typeof MediaStream !== 'undefined'
                ? MediaStream
                : (window as unknown as { MediaStream?: typeof MediaStream }).MediaStream;
            if (MediaStreamCtor) {
              try {
                video.srcObject = new MediaStreamCtor([event.track]);
              } catch {
                const ms = new MediaStreamCtor();
                if (typeof ms.addTrack === 'function') {
                  ms.addTrack(event.track);
                }
                video.srcObject = ms;
              }
            }
          }
          video.play?.()?.catch?.(() => {});
        };

        const offer = await pc.createOffer();
        if (epochRef.current !== epoch || ac.signal.aborted) {
          try {
            pc.close();
          } catch {
            // ignore
          }
          return;
        }

        await pc.setLocalDescription(offer);
        if (epochRef.current !== epoch || ac.signal.aborted) {
          try {
            pc.close();
          } catch {
            // ignore
          }
          return;
        }

        if (pc.iceGatheringState !== 'complete') {
          await new Promise<void>((resolve) => {
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
              ac.signal.removeEventListener('abort', onAbort);
              resolve();
            };

            const onIceChange = () => {
              if (pc.iceGatheringState === 'complete') {
                cleanup();
              }
            };

            const onAbort = () => {
              cleanup();
            };

            timer = setTimeout(cleanup, 500);
            if (typeof pc.addEventListener === 'function') {
              pc.addEventListener('icegatheringstatechange', onIceChange);
            }
            ac.signal.addEventListener('abort', onAbort);
          });
        }

        if (epochRef.current !== epoch || ac.signal.aborted) {
          try {
            pc.close();
          } catch {
            // ignore
          }
          return;
        }

        const sdpOffer = pc.localDescription?.sdp ?? '';
        const res = await fetch(streamUrl, {
          method: 'POST',
          headers: { 'Content-Type': 'application/sdp' },
          body: sdpOffer,
          signal: ac.signal,
        });

        if (epochRef.current !== epoch || ac.signal.aborted) {
          try {
            pc.close();
          } catch {
            // ignore
          }
          return;
        }

        if (!res.ok) {
          throw new Error(`SDP exchange failed with HTTP ${res.status}`);
        }

        const resText = await res.text();
        if (epochRef.current !== epoch || ac.signal.aborted) {
          try {
            pc.close();
          } catch {
            // ignore
          }
          return;
        }

        let answerSdp = resText;
        try {
          const jsonResp = JSON.parse(resText);
          if (jsonResp && typeof jsonResp.sdp === 'string') {
            answerSdp = jsonResp.sdp;
          }
        } catch {
          // ignore JSON parse fallback
        }

        await pc.setRemoteDescription({ type: 'answer', sdp: answerSdp });
        if (epochRef.current !== epoch || ac.signal.aborted) {
          try {
            pc.close();
          } catch {
            // ignore
          }
          return;
        }

        videoRef.current?.play?.()?.catch?.(() => {});
      } catch (err) {
        if (epochRef.current === epoch && !ac.signal.aborted) {
          console.warn(`[MirrormereLiveView] WebRTC negotiation error for ${id}:`, err);
        }
      }
    };

    void startWebRTC();

    return () => {
      stopDecoders();
    };
  }, [shouldPlay, streamUrl, streamType, id, stopDecoders]);

  const triggerExpand = useCallback(async () => {
    if (!expandOnClick || isLoading || !streamUrl) return;

    const now = Date.now();
    if (now - lastTriggerTimeRef.current < 350) return;
    lastTriggerTimeRef.current = now;

    setIsLoading(true);

    if (safetyTimerRef.current) {
      clearTimeout(safetyTimerRef.current);
    }
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
    };

    if (timeoutSeconds !== undefined && Number(timeoutSeconds) > 0) {
      payload.timeout_seconds = Number(timeoutSeconds);
    }

    if (controllable && controlUrl) {
      fetch(controlUrl, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: streamId, action: 'wake' }),
      }).catch((err) => {
        console.warn('[MirrormereLiveView] Wake action failed:', err);
      });
    }

    try {
      const res = await fetch('api/video/trigger', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      });
      if (!res.ok) {
        throw new Error(`Trigger failed with HTTP ${res.status}`);
      }
    } catch (err) {
      console.warn(`[MirrormereLiveView] Trigger failed for ${streamId}:`, err);
      setIsLoading(false);
      if (safetyTimerRef.current) {
        clearTimeout(safetyTimerRef.current);
        safetyTimerRef.current = null;
      }
    }
  }, [
    expandOnClick,
    isLoading,
    streamUrl,
    streamId,
    expandStreamUrl,
    expandStreamType,
    streamType,
    priority,
    controllable,
    controlUrl,
    timeoutSeconds,
  ]);

  const getCoords = (
    e: React.PointerEvent | React.TouchEvent | React.MouseEvent,
  ): { x: number; y: number } | null => {
    const touchEvt = e as unknown as React.TouchEvent;
    if (touchEvt.changedTouches && touchEvt.changedTouches.length > 0) {
      return {
        x: touchEvt.changedTouches[0].clientX,
        y: touchEvt.changedTouches[0].clientY,
      };
    }
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
  };

  const handlePointerDown = useCallback((e: React.PointerEvent) => {
    pointerDownCoordsRef.current = getCoords(e);
  }, []);

  const handlePointerUp = useCallback(
    (e: React.PointerEvent) => {
      const start = pointerDownCoordsRef.current;
      pointerDownCoordsRef.current = null;
      const coords = getCoords(e);
      if (start && coords) {
        const dist = Math.hypot(coords.x - start.x, coords.y - start.y);
        if (dist >= 15) {
          lastDragTimeRef.current = Date.now();
          return;
        }
      }
      void triggerExpand();
    },
    [triggerExpand],
  );

  const handleClick = useCallback(() => {
    if (Date.now() - lastDragTimeRef.current < 400) {
      return;
    }
    void triggerExpand();
  }, [triggerExpand]);

  useEffect(() => {
    return () => {
      if (safetyTimerRef.current) {
        clearTimeout(safetyTimerRef.current);
        safetyTimerRef.current = null;
      }
    };
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
});

import { useEffect } from 'react';
import type { VideoSource } from '../sse/types';

function safePlay(video: HTMLVideoElement | null): void {
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

export interface UseHlsStreamOptions {
  videoRef: React.RefObject<HTMLVideoElement | null>;
  stream: VideoSource | null;
  enabled?: boolean;
}

/**
 * Custom hook managing native HLS stream playback on the target video element.
 */
export function useHlsStream({
  videoRef,
  stream,
  enabled = true,
}: UseHlsStreamOptions): void {
  useEffect(() => {
    if (!enabled || !stream?.stream_url) {
      return;
    }

    const video = videoRef.current;
    if (!video) return;

    if (
      typeof video.canPlayType !== 'function' ||
      !video.canPlayType('application/vnd.apple.mpegurl')
    ) {
      console.warn('[MirrormereVideo] HLS playback not natively supported');
    }
    video.src = stream.stream_url;
    safePlay(video);

    return () => {
      if (video) {
        if (typeof video.pause === 'function') {
          try {
            video.pause();
          } catch {
            // ignore
          }
        }
        video.removeAttribute('src');
        if (typeof video.load === 'function') {
          try {
            video.load();
          } catch {
            // ignore
          }
        }
      }
    };
  }, [stream?.id, stream?.stream_url, stream?.type, enabled]); // eslint-disable-line react-hooks/exhaustive-deps
}

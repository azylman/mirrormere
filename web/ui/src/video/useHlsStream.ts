import { useEffect } from 'react';
import type { VideoSource } from '../sse/types';
import { safePlay } from './useWebRTCStream';

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
  const streamId = stream?.id;
  const streamUrl = stream?.stream_url;
  const streamType = stream?.type;

  useEffect(() => {
    if (!enabled || !streamUrl) {
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
    video.src = streamUrl;
    safePlay(video);

    return () => {
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
    };
  }, [enabled, streamId, streamUrl, streamType, videoRef]);
}

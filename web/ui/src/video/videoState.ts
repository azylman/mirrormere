import type { VideoStatePayload } from '../sse/types';

/**
 * Video state as accepted from the legacy `window.videoManager` / `window.videoHUD` bridges.
 * The Go server always sends `mode`, `primary` and `pip`, but bridge callers may send only the
 * streams, so those fields stay optional here until normalizeVideoState fills them in.
 */
export interface LegacyVideoStatePayload {
  mode?: VideoStatePayload['mode'] | '';
  primary?: VideoStatePayload['primary'];
  pip?: VideoStatePayload['pip'];
}

/** Fills in the store's VideoStatePayload contract, inferring video mode from the streams when mode is missing. */
export function normalizeVideoState(data: LegacyVideoStatePayload | null): VideoStatePayload {
  const primary = data?.primary ?? null;
  const pip = data?.pip ?? null;
  const mode = data?.mode || (primary || pip ? 'video' : 'widgets');
  return { mode, primary, pip };
}

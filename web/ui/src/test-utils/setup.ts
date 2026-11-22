import { cleanup } from '@testing-library/react';
import { afterEach, beforeEach, vi } from 'vitest';
import { useDisplayStore } from '../store';

const initialStore = useDisplayStore.getState();

beforeEach(() => {
  // jsdom does not implement media playback; every suite needs inert play/load/pause.
  vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue(undefined);
  vi.spyOn(HTMLMediaElement.prototype, 'load').mockReturnValue(undefined);
  vi.spyOn(HTMLMediaElement.prototype, 'pause').mockReturnValue(undefined);
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  useDisplayStore.setState(initialStore, true);
});

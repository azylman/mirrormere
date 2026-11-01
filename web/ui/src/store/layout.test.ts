import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { BufferedEvent } from '../sse/batch';
import type { ScreenRotatePayload, ScreensResponse } from '../sse/types';
import { useDisplayStore } from './index';

const initial = useDisplayStore.getState();

const rotate = (current: number, total = 3): BufferedEvent => ({
  type: 'screen.rotate',
  data: {
    current_screen: current,
    total_screens: total,
    interval_seconds: 30,
    widgets: [{ widget_id: `w-${current}`, origin: [0, 0], dimensions: [2, 2] }],
  } satisfies ScreenRotatePayload,
});

const screens: ScreensResponse = {
  current_screen: 1,
  interval_seconds: 30,
  screens: [0, 1, 2].map((index) => ({
    index,
    widgets: [{ widget_id: `w-${index}`, type: 'clock', origin: [0, 0], dimensions: [2, 2] }],
  })),
};

function stubScreens(body: ScreensResponse = screens) {
  const fetchMock = vi.fn(async () => new Response(JSON.stringify(body), { status: 200 }));
  vi.stubGlobal('fetch', fetchMock);
  return fetchMock;
}

describe('carousel store', () => {
  beforeEach(() => {
    useDisplayStore.setState(initial, true);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    useDisplayStore.setState(initial, true);
  });

  describe('pause and resume', () => {
    it('queues the latest screen.rotate while paused and leaves the shown screen alone', () => {
      useDisplayStore.getState().pause();
      useDisplayStore.getState().applyEvents([rotate(1)]);
      useDisplayStore.getState().applyEvents([rotate(2)]);

      const state = useDisplayStore.getState();
      expect(state.screen.currentScreen).toBe(0);
      expect(state.carousel.paused).toBe(true);
      expect(state.carousel.pendingRotate?.current_screen).toBe(2);
    });

    it('applies the queued rotate on resume and clears it', () => {
      useDisplayStore.getState().pause();
      useDisplayStore.getState().applyEvents([rotate(2)]);
      useDisplayStore.getState().resume();

      const state = useDisplayStore.getState();
      expect(state.carousel.paused).toBe(false);
      expect(state.carousel.pendingRotate).toBeNull();
      expect(state.screen.currentScreen).toBe(2);
      expect(state.screen.widgets).toEqual([{ widget_id: 'w-2', origin: [0, 0], dimensions: [2, 2] }]);
    });

    it('keeps the shown screen when resuming with nothing queued', () => {
      useDisplayStore.getState().applyEvents([rotate(1)]);
      useDisplayStore.getState().pause();
      useDisplayStore.getState().resume();
      expect(useDisplayStore.getState().screen.currentScreen).toBe(1);
    });

    it('is idempotent for repeated pause and resume calls', () => {
      useDisplayStore.getState().pause();
      const paused = useDisplayStore.getState().carousel;
      useDisplayStore.getState().pause();
      expect(useDisplayStore.getState().carousel).toBe(paused);

      useDisplayStore.getState().resume();
      const resumed = useDisplayStore.getState().carousel;
      useDisplayStore.getState().resume();
      expect(useDisplayStore.getState().carousel).toBe(resumed);
    });
  });

  describe('layoutVersion', () => {
    it('bumps on widget.reload', () => {
      useDisplayStore.getState().applyEvents([{ type: 'widget.reload', data: { type: 'clock' } }]);
      expect(useDisplayStore.getState().layoutVersion).toBe(1);
    });

    it('bumps when the config status or error changes', () => {
      useDisplayStore.getState().applyEvents([
        { type: 'system.status', data: { online: true, time: 't', config_status: 'error', config_error: 'bad' } },
      ]);
      expect(useDisplayStore.getState().layoutVersion).toBe(1);

      useDisplayStore.getState().applyEvents([
        { type: 'system.status', data: { online: true, time: 't', config_status: 'error', config_error: 'bad' } },
      ]);
      expect(useDisplayStore.getState().layoutVersion).toBe(1);
    });

    it('bumps when the boot id changes after the first one is known', () => {
      useDisplayStore.getState().applyEvents([
        { type: 'system.status', data: { online: true, time: 't', config_status: 'ok', config_error: null, boot_id: 'a' } },
      ]);
      expect(useDisplayStore.getState().layoutVersion).toBe(0);

      useDisplayStore.getState().applyEvents([
        { type: 'system.status', data: { online: true, time: 't', config_status: 'ok', config_error: null, boot_id: 'b' } },
      ]);
      expect(useDisplayStore.getState().layoutVersion).toBe(1);
    });

    it('does not bump for ordinary status heartbeats', () => {
      useDisplayStore.getState().applyEvents([
        { type: 'system.status', data: { online: true, time: 't', config_status: 'ok', config_error: null } },
      ]);
      expect(useDisplayStore.getState().layoutVersion).toBe(0);
    });
  });

  describe('loadScreens', () => {
    it('stores the layout of every screen and syncs the screen count', async () => {
      const fetchMock = stubScreens();
      await useDisplayStore.getState().loadScreens();

      expect(fetchMock).toHaveBeenCalledWith('api/screens', expect.anything());
      expect(useDisplayStore.getState().layout).toEqual(screens.screens);
      expect(useDisplayStore.getState().screen.totalScreens).toBe(3);
    });

    it('leaves the layout reference alone when the response is unchanged', async () => {
      stubScreens();
      await useDisplayStore.getState().loadScreens();
      const first = useDisplayStore.getState().layout;
      await useDisplayStore.getState().loadScreens();
      expect(useDisplayStore.getState().layout).toBe(first);
    });

    it('rejects on a non-OK response and keeps the previous layout', async () => {
      vi.stubGlobal('fetch', vi.fn(async () => new Response('', { status: 500 })));
      await expect(useDisplayStore.getState().loadScreens()).rejects.toThrow('HTTP 500');
      expect(useDisplayStore.getState().layout).toEqual([]);
    });
  });
});

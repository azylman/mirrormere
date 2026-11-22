import { act } from '@testing-library/react';
import { vi } from 'vitest';
import type { ScreensResponse } from '../sse/types';

/**
 * jsdom has no requestIdleCallback, so scheduleIdleStep uses its 16 ms timer there. Tests that
 * wait for neighbour mounts fake only the timer functions and step the clock, with no real sleeps.
 */
export function fakeIdleClock(): void {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
}

/**
 * Steps the fake clock until the idle steps are done. act() applies state only when its scope
 * ends, so each pass gives every neighbour pane one step and lets it schedule the next.
 */
export async function runIdleSteps(): Promise<void> {
  for (let pass = 0; pass < 20; pass++) {
    // eslint-disable-next-line @typescript-eslint/require-await -- an async act scope flushes the effects each step schedules
    await act(async () => {
      vi.advanceTimersByTime(16);
    });
  }
}

/** Holds the detail fetch for `id` until `until` resolves. */
interface ScreensApiHold {
  id: string;
  until: Promise<void>;
}

export interface ScreensApiOptions {
  /** Response for api/screen/select; defaults to 200 `{}`. */
  select?: () => Promise<Response>;
  hold?: ScreensApiHold;
}

/**
 * Stubs fetch with the screens API: api/screens returns `screens`, api/screen/select calls
 * `select`, any .../render URL returns a legacy card and api/widgets/{id} returns a healthy detail whose
 * type comes from the layout (clock when unknown). Returns the fetch mock.
 */
export function stubScreensApi(screens: ScreensResponse, { select, hold }: ScreensApiOptions = {}) {
  const fetchMock = vi.fn(async (url: string) => {
    if (url === 'api/screens') {
      return new Response(JSON.stringify(screens), { status: 200 });
    }
    if (url === 'api/screen/select') {
      return select ? select() : new Response('{}', { status: 200 });
    }
    if (url.endsWith('/render')) {
      return new Response('<div class="widget-card"><span class="legacy-label">legacy</span></div>', { status: 200 });
    }
    const id = decodeURIComponent(url.replace('api/widgets/', ''));
    if (hold?.id === id) await hold.until;
    const type = screens.screens.flatMap((screen) => screen.widgets).find((w) => w.widget_id === id)?.type ?? 'clock';
    return new Response(
      JSON.stringify({
        widget_id: id,
        type,
        origin: [0, 0],
        dimensions: [1, 1],
        config: {},
        state: 'healthy',
        data: {},
        timestamp: '2026-10-31T12:00:00Z',
        online: true,
        assets_base: '',
      }),
      { status: 200 },
    );
  });
  vi.stubGlobal('fetch', fetchMock);
  return fetchMock;
}

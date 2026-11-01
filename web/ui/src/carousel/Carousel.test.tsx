import { act, cleanup, render } from '@testing-library/react';
import { Profiler } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useDisplayStore } from '../store';
import type { ScreenLayout, ScreensResponse } from '../sse/types';
import captureSource from '../../../../sidecars/eink-renderer/src/capture.js?raw';
import { Carousel, panesFor, paneKey } from './Carousel';

const initial = useDisplayStore.getState();

const layout: ScreenLayout[] = [
  {
    index: 0,
    widgets: [
      { widget_id: 'a-0', type: 'clock', origin: [0, 0], dimensions: [2, 1] },
      { widget_id: 'shared', type: 'clock', origin: [2, 0], dimensions: [2, 1] },
    ],
  },
  {
    index: 1,
    widgets: [
      { widget_id: 'b-1', type: 'weather-forecast', origin: [0, 0], dimensions: [3, 2] },
      { widget_id: 'b-2', type: 'clock', origin: [3, 0], dimensions: [3, 1] },
    ],
  },
  {
    index: 2,
    widgets: [{ widget_id: 'c-2', type: 'clock', origin: [0, 1], dimensions: [6, 1] }],
  },
];

const screens: ScreensResponse = { current_screen: 1, interval_seconds: 30, screens: layout };

/**
 * The settle predicate from the e-ink renderer, read from its source so the test
 * evaluates the real expression. Raw import keeps the Node-only sidecar out of the bundle.
 */
function settlePredicate(): boolean {
  const match = /SETTLE_PREDICATE_EXPRESSION = `([\s\S]*?)`;/.exec(captureSource);
  if (!match) throw new Error('SETTLE_PREDICATE_EXPRESSION not found in capture.js');
  return new Function(`return (${match[1]});`)() as boolean;
}

function stubApi(screenBody: ScreensResponse = screens) {
  const fetchMock = vi.fn(async (url: string) => {
    if (url === 'api/screens') {
      return new Response(JSON.stringify(screenBody), { status: 200 });
    }
    const id = decodeURIComponent(url.replace('api/widgets/', ''));
    const type = layout.flatMap((screen) => screen.widgets).find((w) => w.widget_id === id)?.type ?? 'clock';
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

function gridCanvas(): HTMLElement | null {
  return document.querySelector<HTMLElement>('#grid-canvas');
}

describe('panesFor and paneKey', () => {
  it('mounts previous, current and next, wrapping', () => {
    expect(panesFor(0, 3)).toEqual([
      { index: 2, offset: -1 },
      { index: 0, offset: 0 },
      { index: 1, offset: 1 },
    ]);
  });

  it('mounts only the current pane with one screen', () => {
    expect(panesFor(0, 1)).toEqual([{ index: 0, offset: 0 }]);
  });

  it('gives the previous pane its own key when the two neighbours are the same screen', () => {
    const panes = panesFor(0, 2);
    const keys = panes.map((pane) => paneKey(pane, 2));
    expect(new Set(keys).size).toBe(keys.length);
  });

  it('keys by screen index otherwise, so a neighbour keeps its DOM when it becomes current', () => {
    expect(panesFor(1, 3).map((pane) => paneKey(pane, 3))).toEqual(['0', '1', '2']);
  });
});

describe('Carousel', () => {
  beforeEach(() => {
    useDisplayStore.setState(initial, true);
    useDisplayStore.setState({ screen: { ...initial.screen, currentScreen: 1, totalScreens: 3 } });
  });

  afterEach(() => {
    cleanup();
    vi.useRealTimers();
    vi.unstubAllGlobals();
    useDisplayStore.setState(initial, true);
    document.body.innerHTML = '';
    document.body.removeAttribute('data-animating');
  });

  it('loads the layout on mount and mounts every widget of the three panes', async () => {
    const fetchMock = stubApi();
    render(<Carousel />);
    await act(async () => {});

    expect(fetchMock).toHaveBeenCalledWith('api/screens', expect.anything());
    const hydrated = fetchMock.mock.calls
      .map(([url]) => String(url))
      .filter((url) => url.startsWith('api/widgets/'))
      .sort();
    expect(hydrated).toEqual(['api/widgets/a-0', 'api/widgets/b-1', 'api/widgets/b-2', 'api/widgets/c-2', 'api/widgets/shared']);
    expect(document.querySelectorAll('[data-widget-id]')).toHaveLength(5);
  });

  it('puts #grid-canvas and the hydration attributes on the current pane only', async () => {
    stubApi();
    render(<Carousel />);
    await act(async () => {});

    expect(document.querySelectorAll('#grid-canvas')).toHaveLength(1);
    const canvas = gridCanvas();
    expect(canvas?.dataset.widgetCount).toBe('2');
    expect(canvas?.dataset.screenHydrated).toBe('true');
    expect(Array.from(canvas?.querySelectorAll('[data-widget-id]') ?? []).map((el) => (el as HTMLElement).dataset.widgetId)).toEqual(['b-1', 'b-2']);

    const panes = Array.from(document.querySelectorAll<HTMLElement>('.grid-pane'));
    expect(panes).toHaveLength(3);
    const neighbours = panes.filter((pane) => pane.id !== 'grid-canvas');
    expect(neighbours.every((pane) => !pane.dataset.screenHydrated && !pane.dataset.widgetCount)).toBe(true);
    // Neighbour widgets exist, but outside #grid-canvas.
    expect(neighbours.flatMap((pane) => Array.from(pane.querySelectorAll('[data-widget-id]')))).toHaveLength(3);
  });

  it('passes the e-ink settle predicate with neighbour widgets mounted', async () => {
    stubApi();
    render(<Carousel />);
    await act(async () => {});

    expect(document.readyState).toBe('complete');
    expect(settlePredicate()).toBe(true);
  });

  it('does not pass the settle predicate before the layout has loaded', () => {
    stubApi();
    render(<Carousel />);
    // No flush: the layout fetch has not resolved.
    expect(gridCanvas()?.dataset.screenHydrated).toBeUndefined();
    expect(settlePredicate()).toBe(false);
  });

  it('keeps the shown screen and renders nothing new when the echo names the screen already shown', async () => {
    stubApi();
    const onRender = vi.fn();
    render(
      <Profiler id="carousel" onRender={onRender}>
        <Carousel />
      </Profiler>,
    );
    await act(async () => {});
    const before = onRender.mock.calls.length;

    act(() => {
      useDisplayStore.getState().applyEvents([
        {
          type: 'screen.rotate',
          data: {
            current_screen: 1,
            total_screens: 3,
            interval_seconds: 30,
            widgets: layout[1].widgets.map((w) => ({ widget_id: w.widget_id, origin: w.origin, dimensions: w.dimensions })),
          },
        },
      ]);
    });

    expect(onRender.mock.calls.length).toBe(before);
    expect(gridCanvas()?.querySelectorAll('[data-widget-id]')).toHaveLength(2);
  });

  it('slides to a server-rotated neighbour and re-centres with the new current pane', async () => {
    vi.useFakeTimers();
    const clientWidth = vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(1000);
    stubApi();
    render(<Carousel />);
    await act(async () => {});

    const track = document.querySelector<HTMLElement>('.carousel-track');
    const oldNext = document.querySelector<HTMLElement>('.grid-pane[style*="left: 100%"]');
    expect(track).not.toBeNull();

    act(() => {
      useDisplayStore.setState({ screen: { ...useDisplayStore.getState().screen, currentScreen: 2 } });
    });
    expect(track?.style.transform).toBe('translate3d(-1000px,0,0)');
    expect(document.body.dataset.animating).toBe('1');

    await act(async () => {
      vi.advanceTimersByTime(300);
    });

    // The pane that was next is now the current one: same DOM node, same mounted widgets.
    expect(gridCanvas()).toBe(oldNext);
    expect(track?.style.transform).toBe('');
    expect(gridCanvas()?.querySelectorAll('[data-widget-id]')).toHaveLength(1);
    expect(document.body.dataset.animating).toBe('0');
    clientWidth.mockRestore();
  });

  it('shows a placeholder card for widget types with no registered component', async () => {
    stubApi();
    render(<Carousel />);
    await act(async () => {});
    const card = document.querySelector<HTMLElement>('[data-widget-id="b-1"]');
    expect(card?.className).toContain('widget-card');
    expect(card?.dataset.widgetType).toBe('weather-forecast');
    expect(card?.querySelector('.widget-card-placeholder')?.textContent).toBe('weather-forecast');
  });

  it('refetches the layout when the layout version changes', async () => {
    const fetchMock = stubApi();
    render(<Carousel />);
    await act(async () => {});
    const screenCalls = () => fetchMock.mock.calls.filter(([url]) => url === 'api/screens').length;
    expect(screenCalls()).toBe(1);

    act(() => {
      useDisplayStore.getState().applyEvents([{ type: 'widget.reload', data: { type: 'clock' } }]);
    });
    await act(async () => {});
    expect(screenCalls()).toBe(2);
  });
});

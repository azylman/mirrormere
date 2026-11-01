import { act, cleanup, render } from '@testing-library/react';
import { Profiler, useLayoutEffect } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useDisplayStore } from '../store';
import type { ScreenLayout, ScreensResponse } from '../sse/types';
import captureSource from '../../../../sidecars/eink-renderer/src/capture.js?raw';
import { Carousel, panesFor, paneKey } from './Carousel';
import { SETTLE_FALLBACK_MS } from './track';

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

type SelectHandler = () => Promise<Response>;

function stubApi(screenBody: ScreensResponse = screens, select: SelectHandler = async () => new Response('{}', { status: 200 })) {
  const fetchMock = vi.fn(async (url: string) => {
    if (url === 'api/screens') {
      return new Response(JSON.stringify(screenBody), { status: 200 });
    }
    if (url === 'api/screen/select') {
      return select();
    }
    if (url.endsWith('/render')) {
      return new Response('<div class="widget-card"><span class="legacy-label">legacy</span></div>', { status: 200 });
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
      .filter((url) => url.startsWith('api/widgets/') && !url.endsWith('/render'))
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

  it('follows a store change that lands between the first render and the controller mount', async () => {
    vi.useFakeTimers();
    stubApi();
    useDisplayStore.setState({ screen: { ...useDisplayStore.getState().screen, currentScreen: 0 } });
    // Layout effects run after Carousel renders and before its passive effects build the
    // controller: the same gap a reconnect hydration batch can land in.
    function MoveStore() {
      useLayoutEffect(() => {
        useDisplayStore.setState({ screen: { ...useDisplayStore.getState().screen, currentScreen: 1 } });
      }, []);
      return null;
    }
    render(
      <>
        <Carousel />
        <MoveStore />
      </>,
    );
    await act(async () => {
      vi.advanceTimersByTime(SETTLE_FALLBACK_MS);
    });
    expect(gridCanvas()?.querySelector('[data-widget-id="b-1"]')).not.toBeNull();

    // A swipe back to screen 0 still renders it.
    act(() => {
      useDisplayStore.setState({ screen: { ...useDisplayStore.getState().screen, currentScreen: 0 } });
    });
    await act(async () => {
      vi.advanceTimersByTime(SETTLE_FALLBACK_MS);
    });
    expect(gridCanvas()?.querySelector('[data-widget-id="a-0"]')).not.toBeNull();
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
      vi.advanceTimersByTime(SETTLE_FALLBACK_MS);
    });

    // The pane that was next is now the current one: same DOM node, same mounted widgets.
    expect(gridCanvas()).toBe(oldNext);
    expect(track?.style.transform).toBe('');
    expect(gridCanvas()?.querySelectorAll('[data-widget-id]')).toHaveLength(1);
    expect(document.body.dataset.animating).toBe('0');
    clientWidth.mockRestore();
  });

  it('renders widget types with no registered component through the legacy fragment', async () => {
    stubApi();
    render(<Carousel />);
    await act(async () => {});
    const card = document.querySelector<HTMLElement>('[data-widget-id="b-1"]');
    expect(card?.className).toContain('widget-card');
    expect(card?.dataset.widgetType).toBe('weather-forecast');
    expect(card?.querySelector('.widget-legacy .legacy-label')?.textContent).toBe('legacy');
  });

  describe('selecting a screen with a dot', () => {
    function clickDot(index: number): void {
      act(() => {
        document.querySelector<HTMLButtonElement>(`[data-screen-index="${index}"]`)?.click();
      });
    }

    function activeDot(): number | null {
      const active = document.querySelector<HTMLButtonElement>('.screen-indicator-dot.active');
      return active ? Number(active.dataset.screenIndex) : null;
    }

    /** Clicks a dot, lets the slide settle, and returns once the select request has been made. */
    async function selectAndSettle(): Promise<void> {
      clickDot(2);
      await act(async () => {
        vi.advanceTimersByTime(SETTLE_FALLBACK_MS);
      });
    }

    it('sets currentScreen before the select request is made', async () => {
      vi.useFakeTimers();
      let seenAtRequest: number | undefined;
      stubApi(screens, async () => {
        seenAtRequest = useDisplayStore.getState().screen.currentScreen;
        return new Response('{}', { status: 200 });
      });
      render(<Carousel />);
      await act(async () => {});

      await selectAndSettle();

      expect(seenAtRequest).toBe(2);
      expect(useDisplayStore.getState().screen.currentScreen).toBe(2);
      expect(activeDot()).toBe(2);
    });

    it('keeps the new screen when the select request succeeds', async () => {
      vi.useFakeTimers();
      stubApi();
      render(<Carousel />);
      await act(async () => {});

      await selectAndSettle();
      await act(async () => {});

      expect(useDisplayStore.getState().screen.currentScreen).toBe(2);
      expect(activeDot()).toBe(2);
    });

    it('restores the previous screen and slides back when the request rejects', async () => {
      vi.useFakeTimers();
      const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {});
      stubApi(screens, async () => {
        throw new TypeError('network down');
      });
      render(<Carousel />);
      await act(async () => {});

      await selectAndSettle();
      await act(async () => {});

      expect(useDisplayStore.getState().screen.currentScreen).toBe(1);
      expect(consoleError).toHaveBeenCalled();
      await act(async () => {
        vi.advanceTimersByTime(SETTLE_FALLBACK_MS);
      });
      expect(activeDot()).toBe(1);
    });

    it('does not revert a failed select when a newer rotate has moved the screen on', async () => {
      vi.useFakeTimers();
      const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {});
      let rejectSelect: (err: Error) => void = () => {};
      stubApi(screens, () => new Promise<Response>((_, reject) => (rejectSelect = reject)));
      render(<Carousel />);
      await act(async () => {});

      await selectAndSettle();
      expect(useDisplayStore.getState().screen.currentScreen).toBe(2);

      act(() => {
        useDisplayStore.getState().applyEvents([
          {
            type: 'screen.rotate',
            data: {
              current_screen: 0,
              total_screens: 3,
              interval_seconds: 30,
              widgets: layout[0].widgets.map((w) => ({ widget_id: w.widget_id, origin: w.origin, dimensions: w.dimensions })),
            },
          },
        ]);
      });
      expect(useDisplayStore.getState().screen.currentScreen).toBe(0);

      await act(async () => {
        rejectSelect(new TypeError('network down'));
      });

      expect(consoleError).toHaveBeenCalled();
      expect(useDisplayStore.getState().screen.currentScreen).toBe(0);
    });

    it('restores the previous screen and slides back when the request returns 500', async () => {
      vi.useFakeTimers();
      const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {});
      stubApi(screens, async () => new Response('', { status: 500 }));
      render(<Carousel />);
      await act(async () => {});

      await selectAndSettle();
      await act(async () => {});

      expect(useDisplayStore.getState().screen.currentScreen).toBe(1);
      expect(consoleError).toHaveBeenCalled();
      await act(async () => {
        vi.advanceTimersByTime(SETTLE_FALLBACK_MS);
      });
      expect(activeDot()).toBe(1);
    });
  });

  describe('swiping the screen indicator pill', () => {
    function pill(): HTMLElement {
      const el = document.querySelector<HTMLElement>('#screen-indicator');
      if (!el) throw new Error('#screen-indicator not rendered');
      return el;
    }

    function firePill(type: string, x: number, target: EventTarget = pill()): void {
      act(() => {
        const event = new Event(type, { bubbles: true, cancelable: true });
        Object.assign(event, { clientX: x, clientY: 100, pointerId: 1, isPrimary: true });
        target.dispatchEvent(event);
      });
    }

    function activeDot(): number | null {
      const active = document.querySelector<HTMLButtonElement>('.screen-indicator-dot.active');
      return active ? Number(active.dataset.screenIndex) : null;
    }

    it('selects the next screen on a 60 px left swipe and captures the pointer on press', async () => {
      vi.useFakeTimers();
      stubApi();
      render(<Carousel />);
      await act(async () => {});
      const capture = vi.fn();
      Object.assign(pill(), { setPointerCapture: capture });

      firePill('pointerdown', 500);
      expect(capture).toHaveBeenCalledWith(1);
      firePill('pointerup', 440);
      await act(async () => {
        vi.advanceTimersByTime(SETTLE_FALLBACK_MS);
      });

      expect(useDisplayStore.getState().screen.currentScreen).toBe(2);
      expect(activeDot()).toBe(2);
    });

    it('does not capture a press that starts on a dot, so the dot keeps its click', async () => {
      vi.useFakeTimers();
      stubApi();
      render(<Carousel />);
      await act(async () => {});
      const capture = vi.fn();
      Object.assign(pill(), { setPointerCapture: capture });
      const dot = document.querySelector<HTMLButtonElement>('[data-screen-index="0"]');
      if (!dot) throw new Error('dot 0 not rendered');

      firePill('pointerdown', 500, dot);
      expect(capture).not.toHaveBeenCalled();
    });

    it('drops the gesture on pointercancel, so a lone pointerup does nothing', async () => {
      vi.useFakeTimers();
      stubApi();
      render(<Carousel />);
      await act(async () => {});

      firePill('pointerdown', 500);
      firePill('pointercancel', 500);
      firePill('pointerup', 440);
      await act(async () => {
        vi.advanceTimersByTime(SETTLE_FALLBACK_MS);
      });

      expect(useDisplayStore.getState().screen.currentScreen).toBe(1);
      expect(activeDot()).toBe(1);
    });
  });

  it('shows screen 0 when the layout shrinks under the shown screen', async () => {
    const screenOf = (index: number) => ({
      index,
      widgets: [{ widget_id: `s${index}`, type: 'clock', origin: [0, 0] as [number, number], dimensions: [1, 1] as [number, number] }],
    });
    const fourScreens: ScreensResponse = { current_screen: 3, interval_seconds: 30, screens: [0, 1, 2, 3].map(screenOf) };
    const twoScreens: ScreensResponse = { current_screen: 3, interval_seconds: 30, screens: [0, 1].map(screenOf) };
    stubApi(fourScreens);
    useDisplayStore.setState({ screen: { ...initial.screen, currentScreen: 3, totalScreens: 4 } });
    render(<Carousel />);
    await act(async () => {});
    expect(gridCanvas()?.querySelector('[data-widget-id="s3"]')).not.toBeNull();

    stubApi(twoScreens);
    await act(async () => {
      await useDisplayStore.getState().loadScreens();
    });
    expect(useDisplayStore.getState().screen.totalScreens).toBe(2);
    expect(gridCanvas()?.querySelector('[data-widget-id="s0"]')).not.toBeNull();
    expect(gridCanvas()?.querySelector('[data-widget-id="s3"]')).toBeNull();
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

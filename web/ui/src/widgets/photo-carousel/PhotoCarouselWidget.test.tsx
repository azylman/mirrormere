import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { cleanup, render, fireEvent, act } from '@testing-library/react';
import {
  PhotoCarouselWidget,
  getSizedPhotoURL,
  getBackdropPhotoURL,
  parsePhotoCarouselData,
} from './PhotoCarouselWidget';
import { useDisplayStore } from '../../store';
import type { WidgetMeta } from '../../store/types';
import { PaneContext } from '../../carousel/PaneContext';
import { endAnimation } from '../../perf/animating';

function makeWidgetMeta(config: Record<string, unknown> = {}): WidgetMeta {
  return {
    type: 'photo-carousel',
    config,
    origin: [0, 0],
    dimensions: [2, 2],
    assets_base: '',
  };
}

describe('PhotoCarouselWidget helpers', () => {
  describe('parsePhotoCarouselData', () => {
    it('returns empty object when data is nullish or invalid', () => {
      expect(parsePhotoCarouselData(null)).toEqual({});
      expect(parsePhotoCarouselData(undefined)).toEqual({});
      expect(parsePhotoCarouselData(123)).toEqual({});
      expect(parsePhotoCarouselData('not-json')).toEqual({});
    });

    it('returns object as-is if already an object', () => {
      const data = { photos: [{ url: 'https://example.com/1.jpg' }], cycle_interval_seconds: 45 };
      expect(parsePhotoCarouselData(data)).toEqual(data);
    });

    it('parses single-encoded JSON string', () => {
      const data = { photos: [{ url: 'https://example.com/1.jpg' }], cycle_interval_seconds: 45 };
      expect(parsePhotoCarouselData(JSON.stringify(data))).toEqual(data);
    });

    it('parses double-encoded JSON string', () => {
      const data = { photos: [{ url: 'https://example.com/1.jpg' }], cycle_interval_seconds: 45 };
      expect(parsePhotoCarouselData(JSON.stringify(JSON.stringify(data)))).toEqual(data);
    });
  });

  describe('getBackdropPhotoURL', () => {
    it('returns empty string when rawUrl is missing or fit is cover', () => {
      expect(getBackdropPhotoURL('', 'contain')).toBe('');
      expect(getBackdropPhotoURL('https://example.com/photo.jpg', 'cover')).toBe('');
    });

    it('returns =w96-h54-c url when fit is contain', () => {
      expect(getBackdropPhotoURL('https://example.com/photo.jpg', 'contain')).toBe(
        'https://example.com/photo.jpg=w96-h54-c',
      );
    });
  });

  describe('getSizedPhotoURL', () => {
    it('returns empty string if rawUrl is empty', () => {
      expect(getSizedPhotoURL('', 'contain', undefined, { width: 800, height: 600 })).toBe('');
    });

    it('respects explicit imageSize config', () => {
      expect(
        getSizedPhotoURL('https://example.com/pic.jpg', 'contain', [640, 480], { width: 800, height: 600 }),
      ).toBe('https://example.com/pic.jpg=w640-h480');

      expect(
        getSizedPhotoURL('https://example.com/pic.jpg', 'cover', [640, 480], { width: 800, height: 600 }),
      ).toBe('https://example.com/pic.jpg=w640-h480-c');
    });

    it('computes sized URL based on containerSize and DPR for cover fit', () => {
      const originalDpr = window.devicePixelRatio;
      window.devicePixelRatio = 2;
      try {
        const url = getSizedPhotoURL('https://example.com/pic.jpg', 'cover', undefined, { width: 400, height: 300 });
        expect(url).toBe('https://example.com/pic.jpg=w800-h600-c');
      } finally {
        window.devicePixelRatio = originalDpr;
      }
    });

    it('computes sized URL based on containerSize and screen resolution for contain fit', () => {
      const originalDpr = window.devicePixelRatio;
      window.devicePixelRatio = 1;
      try {
        // screen width/height default fallback or mock
        const url = getSizedPhotoURL('https://example.com/pic.jpg', 'contain', undefined, { width: 2500, height: 1500 });
        expect(url).toContain('=w2500-h1500');
      } finally {
        window.devicePixelRatio = originalDpr;
      }
    });
  });
});

describe('PhotoCarouselWidget component', () => {
  const widgetId = 'w-photo-1';
  const initialStore = useDisplayStore.getState();

  beforeEach(() => {
    vi.useFakeTimers();
    useDisplayStore.setState(initialStore, true);
  });

  afterEach(() => {
    cleanup();
    vi.clearAllTimers();
    vi.useRealTimers();
    while (document.body.dataset.animating === '1') {
      endAnimation();
    }
    delete document.body.dataset.animating;
  });

  it('renders loading state when widget data has not loaded or has not hydrated photos array', () => {
    useDisplayStore.setState({
      widgets: {
        [widgetId]: {
          meta: undefined,
          data: undefined,
          state: 'loading',
          timestamp: '',
        },
      },
    });

    const { container, rerender } = render(
      <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
        <PhotoCarouselWidget id={widgetId} type="photo-carousel" />
      </PaneContext.Provider>,
    );

    let loading = container.querySelector('.photo-loading');
    expect(loading).not.toBeNull();
    expect(loading?.textContent).toContain('Loading photos...');

    // Also renders loading state when state is healthy but photos array is not yet present
    useDisplayStore.setState({
      widgets: {
        [widgetId]: {
          meta: makeWidgetMeta({}),
          data: {},
          state: 'healthy',
          timestamp: '2026-10-31T12:00:00Z',
        },
      },
    });

    rerender(
      <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
        <PhotoCarouselWidget id={widgetId} type="photo-carousel" />
      </PaneContext.Provider>,
    );

    loading = container.querySelector('.photo-loading');
    expect(loading).not.toBeNull();
    expect(loading?.textContent).toContain('Loading photos...');
  });

  it('renders empty state when photos array is empty', () => {
    useDisplayStore.setState({
      widgets: {
        [widgetId]: {
          meta: makeWidgetMeta({}),
          data: { photos: [] },
          state: 'ready',
          timestamp: '2026-10-31T12:00:00Z',
        },
      },
    });

    const { container } = render(
      <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
        <PhotoCarouselWidget id={widgetId} type="photo-carousel" />
      </PaneContext.Provider>,
    );

    const empty = container.querySelector('.photo-empty');
    expect(empty).not.toBeNull();
    expect(empty?.querySelector('.empty-title')?.textContent).toBe('No photos in shared album');
    expect(empty?.querySelector('.empty-icon')?.textContent).toBe('🖼️');
  });

  it('renders photo viewport with backdrop and foreground for contain fit', () => {
    useDisplayStore.setState({
      widgets: {
        [widgetId]: {
          meta: makeWidgetMeta({ fit: 'contain', image_size: [1000, 800] }),
          data: {
            photos: [
              { url: 'https://example.com/p1.jpg' },
              { url: 'https://example.com/p2.jpg' },
            ],
            cycle_interval_seconds: 10,
          },
          state: 'ready',
          timestamp: '2026-10-31T12:00:00Z',
        },
      },
    });

    const { container } = render(
      <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
        <PhotoCarouselWidget id={widgetId} type="photo-carousel" />
      </PaneContext.Provider>,
    );

    const root = container.querySelector('.widget-photo-carousel');
    expect(root).not.toBeNull();
    expect(root?.getAttribute('data-fit')).toBe('contain');
    expect(root?.classList.contains('dither')).toBe(true);
    // Invariant: no duplicate data-widget-id on inner element
    expect(root?.getAttribute('data-widget-id')).toBeNull();

    const viewport = container.querySelector('.photo-viewport');
    expect(viewport?.classList.contains('dither')).toBe(true);

    const backdropContainer = container.querySelector('.photo-backdrop-container');
    expect(backdropContainer).not.toBeNull();

    const currentBackdrop = container.querySelector('.photo-backdrop-current');
    expect(currentBackdrop?.getAttribute('src')).toBe('https://example.com/p1.jpg=w96-h54-c');

    const currentFg = container.querySelector('.photo-layer.photo-current');
    expect(currentFg?.getAttribute('src')).toBe('https://example.com/p1.jpg=w1000-h800');
    expect(currentFg?.classList.contains('dither')).toBe(true);
  });

  it('does not render backdrop when fit is cover', () => {
    useDisplayStore.setState({
      widgets: {
        [widgetId]: {
          meta: makeWidgetMeta({ fit: 'cover', image_size: [1000, 800] }),
          data: {
            photos: [{ url: 'https://example.com/p1.jpg' }],
          },
          state: 'ready',
          timestamp: '2026-10-31T12:00:00Z',
        },
      },
    });

    const { container } = render(
      <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
        <PhotoCarouselWidget id={widgetId} type="photo-carousel" />
      </PaneContext.Provider>,
    );

    const root = container.querySelector('.widget-photo-carousel');
    expect(root?.getAttribute('data-fit')).toBe('cover');

    const backdropContainer = container.querySelector('.photo-backdrop-container');
    expect(backdropContainer).toBeNull();

    const currentFg = container.querySelector('.photo-layer.photo-current');
    expect(currentFg?.getAttribute('src')).toBe('https://example.com/p1.jpg=w1000-h800-c');
  });

  it('cycles photos automatically with decode preloading', async () => {
    let resolveDecode: (() => void) | null = null;
    const originalImage = window.Image;

    class MockImage {
      src = '';
      onload: (() => void) | null = null;
      onerror: (() => void) | null = null;
      decode() {
        return new Promise<void>((resolve) => {
          resolveDecode = resolve;
        });
      }
    }
    // @ts-expect-error mock window.Image
    window.Image = MockImage;

    try {
      useDisplayStore.setState({
        widgets: {
          [widgetId]: {
            meta: makeWidgetMeta({ fit: 'contain', image_size: [1000, 800] }),
            data: {
              photos: [
                { url: 'https://example.com/p1.jpg' },
                { url: 'https://example.com/p2.jpg' },
              ],
              cycle_interval_seconds: 10,
            },
            state: 'ready',
            timestamp: '2026-10-31T12:00:00Z',
          },
        },
      });

      const { container } = render(
        <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
          <PhotoCarouselWidget id={widgetId} type="photo-carousel" />
        </PaneContext.Provider>,
      );

      const currentFg = container.querySelector('.photo-layer.photo-current') as HTMLImageElement;
      expect(currentFg.getAttribute('src')).toBe('https://example.com/p1.jpg=w1000-h800');

      // Fast-forward interval timer to trigger cycle advance callback
      act(() => {
        vi.advanceTimersByTime(10000);
      });

      // Decode has not resolved yet -> next image is not yet in DOM with opacity 1
      let nextFg = container.querySelector('.photo-layer.photo-next') as HTMLImageElement;
      expect(nextFg.getAttribute('src')).toBeNull();

      // In JSDOM with fake timers, if setInterval didn't advance, advance 10000 more
      if (!resolveDecode) {
        act(() => {
          vi.advanceTimersByTime(10000);
        });
      }

      // Resolve decode and let React process state update
      expect(resolveDecode).not.toBeNull();
      resolveDecode!();
      let nextFgTransitioned = container.querySelector('.photo-layer.photo-next') as HTMLImageElement;
      await vi.waitFor(() => {
        nextFgTransitioned = container.querySelector('.photo-layer.photo-next') as HTMLImageElement;
        expect(nextFgTransitioned.getAttribute('src')).toBe('https://example.com/p2.jpg=w1000-h800');
      });

      expect(nextFgTransitioned.style.opacity).toBe('1');
      expect(currentFg.style.opacity).toBe('0');

      // Fast-forward 850ms crossfade
      act(() => {
        vi.advanceTimersByTime(850);
      });

      // Next image committed to current
      expect(currentFg.getAttribute('src')).toBe('https://example.com/p2.jpg=w1000-h800');
      expect(currentFg.style.opacity).toBe('1');
      nextFg = container.querySelector('.photo-layer.photo-next') as HTMLImageElement;
      expect(nextFg.getAttribute('src')).toBeNull();
      expect(nextFg.style.opacity).toBe('0');
    } finally {
      window.Image = originalImage;
    }
  });

  it('supports tap/click to advance to next photo immediately and resets interval', async () => {
    let resolveDecode: (() => void) | null = null;
    const originalImage = window.Image;

    class MockImage {
      src = '';
      decode() {
        return new Promise<void>((resolve) => {
          resolveDecode = resolve;
        });
      }
    }
    // @ts-expect-error mock window.Image
    window.Image = MockImage;

    try {
      useDisplayStore.setState({
        widgets: {
          [widgetId]: {
            meta: makeWidgetMeta({ fit: 'cover', image_size: [800, 600] }),
            data: {
              photos: [
                { url: 'https://example.com/p1.jpg' },
                { url: 'https://example.com/p2.jpg' },
              ],
              cycle_interval_seconds: 15,
            },
            state: 'ready',
            timestamp: '2026-10-31T12:00:00Z',
          },
        },
      });

      const { container } = render(
        <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
          <PhotoCarouselWidget id={widgetId} type="photo-carousel" />
        </PaneContext.Provider>,
      );

      const root = container.querySelector('.widget-photo-carousel')!;
      // Click immediately
      act(() => {
        fireEvent.click(root);
      });

      // Resolve decode and let promise microtasks run
      expect(resolveDecode).not.toBeNull();
      resolveDecode!();
      let nextFg = container.querySelector('.photo-layer.photo-next') as HTMLImageElement;
      await vi.waitFor(() => {
        nextFg = container.querySelector('.photo-layer.photo-next') as HTMLImageElement;
        expect(nextFg.getAttribute('src')).toBe('https://example.com/p2.jpg=w800-h600-c');
      });

      const currentFg = container.querySelector('.photo-layer.photo-current') as HTMLImageElement;

      // Fast-forward crossfade
      act(() => {
        vi.advanceTimersByTime(850);
      });

      expect(currentFg.getAttribute('src')).toBe('https://example.com/p2.jpg=w800-h600-c');
    } finally {
      window.Image = originalImage;
    }
  });

  it('immediately commits in-flight transition and advances to next photo when tapped mid-crossfade', async () => {
    const decodeResolvers: Array<() => void> = [];
    const originalImage = window.Image;

    class MockImage {
      src = '';
      decode() {
        return new Promise<void>((resolve) => {
          decodeResolvers.push(resolve);
        });
      }
    }
    // @ts-expect-error mock window.Image
    window.Image = MockImage;

    try {
      useDisplayStore.setState({
        widgets: {
          [widgetId]: {
            meta: makeWidgetMeta({ fit: 'cover', image_size: [800, 600] }),
            data: {
              photos: [
                { url: 'https://example.com/p1.jpg' },
                { url: 'https://example.com/p2.jpg' },
                { url: 'https://example.com/p3.jpg' },
              ],
              cycle_interval_seconds: 15,
            },
            state: 'ready',
            timestamp: '2026-10-31T12:00:00Z',
          },
        },
      });

      const { container } = render(
        <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
          <PhotoCarouselWidget id={widgetId} type="photo-carousel" />
        </PaneContext.Provider>,
      );

      const root = container.querySelector('.widget-photo-carousel')!;
      // First click: begins transition from p1 to p2
      act(() => {
        fireEvent.click(root);
      });

      expect(decodeResolvers.length).toBe(1);
      // Resolve decode for p2 so crossfade becomes active
      act(() => {
        decodeResolvers[0]();
      });

      let nextFg = container.querySelector('.photo-layer.photo-next') as HTMLImageElement;
      await vi.waitFor(() => {
        nextFg = container.querySelector('.photo-layer.photo-next') as HTMLImageElement;
        expect(nextFg.getAttribute('src')).toBe('https://example.com/p2.jpg=w800-h600-c');
      });

      // While crossfade is active (before 850ms timeout), click again!
      // This should immediately commit p2 and initiate transition to p3.
      act(() => {
        fireEvent.click(root);
      });

      expect(decodeResolvers.length).toBe(2);
      // Resolve decode for p3
      act(() => {
        decodeResolvers[1]();
      });

      await vi.waitFor(() => {
        nextFg = container.querySelector('.photo-layer.photo-next') as HTMLImageElement;
        expect(nextFg.getAttribute('src')).toBe('https://example.com/p3.jpg=w800-h600-c');
      });

      // Fast-forward 850ms crossfade for p3
      act(() => {
        vi.advanceTimersByTime(850);
      });

      const currentFg = container.querySelector('.photo-layer.photo-current') as HTMLImageElement;
      expect(currentFg.getAttribute('src')).toBe('https://example.com/p3.jpg=w800-h600-c');
    } finally {
      window.Image = originalImage;
    }
  });

  it('pauses cycle timer and clears transitions when not current pane', async () => {
    useDisplayStore.setState({
      widgets: {
        [widgetId]: {
          meta: makeWidgetMeta({}),
          data: {
            photos: [
              { url: 'https://example.com/p1.jpg' },
              { url: 'https://example.com/p2.jpg' },
            ],
            cycle_interval_seconds: 10,
          },
          state: 'ready',
          timestamp: '2026-10-31T12:00:00Z',
        },
      },
    });

    const { rerender, container } = render(
      <PaneContext.Provider value={{ isCurrentPane: false, paneIndex: 0 }}>
        <PhotoCarouselWidget id={widgetId} type="photo-carousel" />
      </PaneContext.Provider>,
    );

    act(() => {
      vi.advanceTimersByTime(30000);
    });

    const currentFg = container.querySelector('.photo-layer.photo-current');
    expect(currentFg?.getAttribute('src')).toContain('p1.jpg');

    // Make it current pane
    rerender(
      <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
        <PhotoCarouselWidget id={widgetId} type="photo-carousel" />
      </PaneContext.Provider>,
    );
  });

  it('sizes photo URLs via ResizeObserver without synchronous getBoundingClientRect', () => {
    let resizeCallback: ((entries: Array<{ contentRect: { width: number; height: number } }>) => void) | null = null;
    const originalResizeObserver = window.ResizeObserver;

    class MockResizeObserver {
      constructor(cb: (entries: Array<{ contentRect: { width: number; height: number } }>) => void) {
        resizeCallback = cb;
      }
      observe = vi.fn();
      disconnect = vi.fn();
      unobserve = vi.fn();
    }
    // @ts-expect-error mock window.ResizeObserver
    window.ResizeObserver = MockResizeObserver;

    const getBoundingClientRectSpy = vi.spyOn(Element.prototype, 'getBoundingClientRect');

    try {
      useDisplayStore.setState({
        widgets: {
          [widgetId]: {
            meta: makeWidgetMeta({ fit: 'cover' }),
            data: {
              photos: [{ url: 'https://example.com/p1.jpg' }],
            },
            state: 'ready',
            timestamp: '2026-10-31T12:00:00Z',
          },
        },
      });

      const { container } = render(
        <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
          <PhotoCarouselWidget id={widgetId} type="photo-carousel" />
        </PaneContext.Provider>,
      );

      // Invariant: no synchronous getBoundingClientRect on mount!
      expect(getBoundingClientRectSpy).not.toHaveBeenCalled();

      // Initial render uses fallback screen/window size before ResizeObserver delivers box
      const currentFg = container.querySelector('.photo-layer.photo-current') as HTMLImageElement;
      expect(currentFg.getAttribute('src')).toMatch(/^https:\/\/example\.com\/p1\.jpg=w\d+-h\d+-c$/);

      // ResizeObserver delivers measured container size
      expect(resizeCallback).not.toBeNull();
      act(() => {
        resizeCallback!([{ contentRect: { width: 960, height: 540 } }]);
      });

      expect(currentFg.getAttribute('src')).toBe('https://example.com/p1.jpg=w960-h540-c');
    } finally {
      window.ResizeObserver = originalResizeObserver;
      getBoundingClientRectSpy.mockRestore();
    }
  });

  it('includes dither class on photo-next during active transition and defaults interval to 60s', async () => {
    let resolveDecode: (() => void) | null = null;
    const originalImage = window.Image;

    class MockImage {
      src = '';
      decode() {
        return new Promise<void>((resolve) => {
          resolveDecode = resolve;
        });
      }
    }
    // @ts-expect-error mock window.Image
    window.Image = MockImage;

    try {
      useDisplayStore.setState({
        widgets: {
          [widgetId]: {
            meta: makeWidgetMeta({ fit: 'cover' }),
            data: {
              photos: [
                { url: 'https://example.com/p1.jpg' },
                { url: 'https://example.com/p2.jpg' },
              ],
              // Neither data nor config specifies cycle_interval_seconds -> defaults to 60s
            },
            state: 'ready',
            timestamp: '2026-10-31T12:00:00Z',
          },
        },
      });

      const { container } = render(
        <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
          <PhotoCarouselWidget id={widgetId} type="photo-carousel" />
        </PaneContext.Provider>,
      );

      // Should NOT advance at 30 seconds
      act(() => {
        vi.advanceTimersByTime(30000);
      });
      expect(resolveDecode).toBeNull();

      // Advancing to 60 seconds triggers the cycle timer
      act(() => {
        vi.advanceTimersByTime(30000);
      });
      expect(resolveDecode).not.toBeNull();

      // Resolve decode to enter active transition
      resolveDecode!();
      let nextFg: HTMLImageElement | null = null;
      await vi.waitFor(() => {
        nextFg = container.querySelector('.photo-layer.photo-next') as HTMLImageElement;
        expect(nextFg).not.toBeNull();
        expect(nextFg.getAttribute('src')).toContain('p2.jpg');
      });

      expect(nextFg!.classList.contains('dither')).toBe(true);
    } finally {
      window.Image = originalImage;
    }
  });

  it('sets document.body.dataset.animating to 1 during crossfade and 0 after CROSSFADE_MS', async () => {
    let resolveDecode: (() => void) | null = null;
    const originalImage = window.Image;

    class MockImage {
      src = '';
      decode() {
        return new Promise<void>((resolve) => {
          resolveDecode = resolve;
        });
      }
    }
    // @ts-expect-error mock window.Image
    window.Image = MockImage;

    try {
      useDisplayStore.setState({
        widgets: {
          [widgetId]: {
            meta: makeWidgetMeta({ fit: 'cover', image_size: [800, 600] }),
            data: {
              photos: [
                { url: 'https://example.com/p1.jpg' },
                { url: 'https://example.com/p2.jpg' },
              ],
              cycle_interval_seconds: 10,
            },
            state: 'ready',
            timestamp: '2026-10-31T12:00:00Z',
          },
        },
      });

      render(
        <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
          <PhotoCarouselWidget id={widgetId} type="photo-carousel" />
        </PaneContext.Provider>,
      );

      expect(document.body.dataset.animating).toBeUndefined();

      act(() => {
        vi.advanceTimersByTime(10000);
      });

      expect(resolveDecode).not.toBeNull();
      expect(document.body.dataset.animating).toBeUndefined();

      act(() => {
        resolveDecode!();
      });

      await vi.waitFor(() => {
        expect(document.body.dataset.animating).toBe('1');
      });

      act(() => {
        vi.advanceTimersByTime(850);
      });

      expect(document.body.dataset.animating).toBe('0');
    } finally {
      window.Image = originalImage;
    }
  });

  it('resets document.body.dataset.animating to 0 on early tap commit mid-crossfade', async () => {
    const decodeResolvers: Array<() => void> = [];
    const originalImage = window.Image;

    class MockImage {
      src = '';
      decode() {
        return new Promise<void>((resolve) => {
          decodeResolvers.push(resolve);
        });
      }
    }
    // @ts-expect-error mock window.Image
    window.Image = MockImage;

    try {
      useDisplayStore.setState({
        widgets: {
          [widgetId]: {
            meta: makeWidgetMeta({ fit: 'cover', image_size: [800, 600] }),
            data: {
              photos: [
                { url: 'https://example.com/p1.jpg' },
                { url: 'https://example.com/p2.jpg' },
                { url: 'https://example.com/p3.jpg' },
              ],
              cycle_interval_seconds: 15,
            },
            state: 'ready',
            timestamp: '2026-10-31T12:00:00Z',
          },
        },
      });

      const { container } = render(
        <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
          <PhotoCarouselWidget id={widgetId} type="photo-carousel" />
        </PaneContext.Provider>,
      );

      const root = container.querySelector('.widget-photo-carousel')!;
      act(() => {
        fireEvent.click(root);
      });

      expect(decodeResolvers.length).toBe(1);
      act(() => {
        decodeResolvers[0]();
      });

      await vi.waitFor(() => {
        expect(document.body.dataset.animating).toBe('1');
      });

      // Tap mid-crossfade: should commit p2 and reset animating to 0 immediately
      act(() => {
        fireEvent.click(root);
      });

      expect(document.body.dataset.animating).toBe('0');
      expect(decodeResolvers.length).toBe(2);

      act(() => {
        decodeResolvers[1]();
      });

      await vi.waitFor(() => {
        expect(document.body.dataset.animating).toBe('1');
      });

      act(() => {
        vi.advanceTimersByTime(850);
      });

      expect(document.body.dataset.animating).toBe('0');
    } finally {
      window.Image = originalImage;
    }
  });

  it('resets document.body.dataset.animating to 0 on unmount mid-crossfade', async () => {
    let resolveDecode: (() => void) | null = null;
    const originalImage = window.Image;

    class MockImage {
      src = '';
      decode() {
        return new Promise<void>((resolve) => {
          resolveDecode = resolve;
        });
      }
    }
    // @ts-expect-error mock window.Image
    window.Image = MockImage;

    try {
      useDisplayStore.setState({
        widgets: {
          [widgetId]: {
            meta: makeWidgetMeta({ fit: 'cover', image_size: [800, 600] }),
            data: {
              photos: [
                { url: 'https://example.com/p1.jpg' },
                { url: 'https://example.com/p2.jpg' },
              ],
              cycle_interval_seconds: 10,
            },
            state: 'ready',
            timestamp: '2026-10-31T12:00:00Z',
          },
        },
      });

      const { unmount } = render(
        <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
          <PhotoCarouselWidget id={widgetId} type="photo-carousel" />
        </PaneContext.Provider>,
      );

      act(() => {
        vi.advanceTimersByTime(10000);
      });

      expect(resolveDecode).not.toBeNull();
      act(() => {
        resolveDecode!();
      });

      await vi.waitFor(() => {
        expect(document.body.dataset.animating).toBe('1');
      });

      unmount();

      expect(document.body.dataset.animating).toBe('0');
    } finally {
      window.Image = originalImage;
    }
  });

  it('aborts transition when Image.decode() rejects and advances to subsequent valid photo on next cycle', async () => {
    const originalImage = window.Image;
    const decodeCalls: string[] = [];

    class MockImage {
      _src = '';
      set src(v: string) {
        this._src = v;
      }
      get src() {
        return this._src;
      }
      decode() {
        decodeCalls.push(this._src);
        if (this._src.includes('p2.jpg')) {
          return Promise.reject(new Error('Corrupt image data'));
        }
        return Promise.resolve();
      }
    }
    // @ts-expect-error mock window.Image
    window.Image = MockImage;

    try {
      useDisplayStore.setState({
        widgets: {
          [widgetId]: {
            meta: makeWidgetMeta({ fit: 'cover', image_size: [800, 600] }),
            data: {
              photos: [
                { url: 'https://example.com/p1.jpg' },
                { url: 'https://example.com/p2.jpg' },
                { url: 'https://example.com/p3.jpg' },
              ],
              cycle_interval_seconds: 10,
            },
            state: 'ready',
            timestamp: '2026-10-31T12:00:00Z',
          },
        },
      });

      const { container } = render(
        <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
          <PhotoCarouselWidget id={widgetId} type="photo-carousel" />
        </PaneContext.Provider>,
      );

      const currentFg = container.querySelector('.photo-layer.photo-current') as HTMLImageElement;
      expect(currentFg.getAttribute('src')).toBe('https://example.com/p1.jpg=w800-h600-c');

      // Trigger cycle 1: attempts to preload p2 (which rejects)
      await act(async () => {
        vi.advanceTimersByTime(10000);
      });

      expect(decodeCalls.some((url) => url.includes('p2.jpg'))).toBe(true);

      // Wait for decodePromise rejection microtask to be handled
      await vi.waitFor(() => {
        expect(document.body.dataset.animating).not.toBe('1');
        const nextFg = container.querySelector('.photo-layer.photo-next') as HTMLImageElement;
        expect(nextFg.getAttribute('src')).toBeNull();
        expect(nextFg.style.opacity).toBe('0');
        expect(currentFg.getAttribute('src')).toBe('https://example.com/p1.jpg=w800-h600-c');
      });

      // Next interval cycle: timer re-armed via retryTick, now tries p3 (valid)
      await act(async () => {
        vi.advanceTimersByTime(10000);
      });

      expect(decodeCalls.some((url) => url.includes('p3.jpg'))).toBe(true);

      await vi.waitFor(() => {
        const nextFg = container.querySelector('.photo-layer.photo-next') as HTMLImageElement;
        expect(nextFg.getAttribute('src')).toBe('https://example.com/p3.jpg=w800-h600-c');
        expect(document.body.dataset.animating).toBe('1');
      });

      act(() => {
        vi.advanceTimersByTime(850);
      });

      expect(currentFg.getAttribute('src')).toBe('https://example.com/p3.jpg=w800-h600-c');
      expect(document.body.dataset.animating).toBe('0');
    } finally {
      window.Image = originalImage;
    }
  });

  it('aborts transition when onerror fires for the next photo URL and advances to subsequent valid photo', async () => {
    const originalImage = window.Image;
    const instances: MockImage[] = [];

    class MockImage {
      _src = '';
      onload: (() => void) | null = null;
      onerror: (() => void) | null = null;
      set src(v: string) {
        this._src = v;
      }
      get src() {
        return this._src;
      }
      constructor() {
        instances.push(this);
      }
    }
    // @ts-expect-error mock window.Image without decode
    window.Image = MockImage;

    try {
      useDisplayStore.setState({
        widgets: {
          [widgetId]: {
            meta: makeWidgetMeta({ fit: 'cover', image_size: [800, 600] }),
            data: {
              photos: [
                { url: 'https://example.com/p1.jpg' },
                { url: 'https://example.com/p2.jpg' },
                { url: 'https://example.com/p3.jpg' },
              ],
              cycle_interval_seconds: 10,
            },
            state: 'ready',
            timestamp: '2026-10-31T12:00:00Z',
          },
        },
      });

      const { container } = render(
        <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
          <PhotoCarouselWidget id={widgetId} type="photo-carousel" />
        </PaneContext.Provider>,
      );

      const currentFg = container.querySelector('.photo-layer.photo-current') as HTMLImageElement;
      expect(currentFg.getAttribute('src')).toBe('https://example.com/p1.jpg=w800-h600-c');

      // Trigger cycle 1: preload p2
      act(() => {
        vi.advanceTimersByTime(10000);
      });

      const p2Img = instances.find((img) => img._src.includes('p2.jpg'));
      expect(p2Img).toBeDefined();

      // Trigger onerror for p2
      act(() => {
        p2Img!.onerror?.();
      });

      await vi.waitFor(() => {
        expect(document.body.dataset.animating).not.toBe('1');
        const nextFg = container.querySelector('.photo-layer.photo-next') as HTMLImageElement;
        expect(nextFg.getAttribute('src')).toBeNull();
        expect(nextFg.style.opacity).toBe('0');
        expect(currentFg.getAttribute('src')).toBe('https://example.com/p1.jpg=w800-h600-c');
      });

      // Next interval cycle: preload p3
      act(() => {
        vi.advanceTimersByTime(10000);
      });

      const p3Img = instances.find((img) => img._src.includes('p3.jpg'));
      expect(p3Img).toBeDefined();

      // Trigger onload for p3
      act(() => {
        p3Img!.onload?.();
      });

      await vi.waitFor(() => {
        const nextFg = container.querySelector('.photo-layer.photo-next') as HTMLImageElement;
        expect(nextFg.getAttribute('src')).toBe('https://example.com/p3.jpg=w800-h600-c');
        expect(document.body.dataset.animating).toBe('1');
      });

      act(() => {
        vi.advanceTimersByTime(850);
      });

      expect(currentFg.getAttribute('src')).toBe('https://example.com/p3.jpg=w800-h600-c');
      expect(document.body.dataset.animating).toBe('0');
    } finally {
      window.Image = originalImage;
    }
  });
});

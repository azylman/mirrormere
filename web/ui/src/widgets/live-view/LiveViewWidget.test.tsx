import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { cleanup, render, fireEvent, act } from '@testing-library/react';
import { LiveViewWidget, parseLiveViewConfig } from './LiveViewWidget';
import { useDisplayStore } from '../../store';
import type { WidgetMeta } from '../../store/types';
import { PaneContext } from '../../carousel/PaneContext';

function makeWidgetMeta(config: Record<string, unknown> = {}): WidgetMeta {
  return {
    type: 'live-view',
    config,
    origin: [0, 0],
    dimensions: [3, 2],
    assets_base: '',
  };
}

describe('LiveViewWidget helpers', () => {
  describe('parseLiveViewConfig', () => {
    it('returns default config when input is null, undefined, primitive, or invalid JSON', () => {
      const def = parseLiveViewConfig(null, 'default-id');
      expect(def.stream_url).toBe('');
      expect(def.stream_type).toBe('webrtc');
      expect(def.fit).toBe('contain');
      expect(def.title).toBe('Live View');
      expect(def.poster_url).toBe('');
      expect(def.expand_on_click).toBe(true);
      expect(def.stream_id).toBe('default-id');
      expect(def.priority).toBe('persistent');
      expect(def.timeout_seconds).toBeUndefined();
      expect(def.controllable).toBe(false);
      expect(def.control_url).toBe('');
      expect(def.expand_stream_url).toBe('');
      expect(def.expand_stream_type).toBe('webrtc');

      expect(parseLiveViewConfig(undefined, 'fallback').stream_id).toBe('fallback');
      expect(parseLiveViewConfig(12345).title).toBe('Live View');
      expect(parseLiveViewConfig('invalid-json').fit).toBe('contain');
    });

    it('parses plain object config with custom properties', () => {
      const cfg = parseLiveViewConfig({
        stream_url: 'https://stream.example.com/webrtc',
        stream_type: 'hls',
        fit: 'cover',
        title: 'Front Porch',
        poster_url: 'https://stream.example.com/poster.jpg',
        expand_on_click: false,
        stream_id: 'cam-front',
        priority: 'temporary',
        timeout_seconds: 45,
        controllable: true,
        control_url: 'https://stream.example.com/control',
        expand_stream_url: 'https://stream.example.com/high-res',
        expand_stream_type: 'webrtc',
      });

      expect(cfg.stream_url).toBe('https://stream.example.com/webrtc');
      expect(cfg.stream_type).toBe('hls');
      expect(cfg.fit).toBe('cover');
      expect(cfg.title).toBe('Front Porch');
      expect(cfg.poster_url).toBe('https://stream.example.com/poster.jpg');
      expect(cfg.expand_on_click).toBe(false);
      expect(cfg.stream_id).toBe('cam-front');
      expect(cfg.priority).toBe('temporary');
      expect(cfg.timeout_seconds).toBe(45);
      expect(cfg.controllable).toBe(true);
      expect(cfg.control_url).toBe('https://stream.example.com/control');
      expect(cfg.expand_stream_url).toBe('https://stream.example.com/high-res');
      expect(cfg.expand_stream_type).toBe('webrtc');
    });

    it('parses single-encoded and double-encoded JSON strings', () => {
      const data = {
        stream_url: 'https://example.com/mjpeg',
        stream_type: 'mjpeg',
        title: 'Driveway',
      };
      const single = parseLiveViewConfig(JSON.stringify(data), 'id-1');
      expect(single.stream_url).toBe('https://example.com/mjpeg');
      expect(single.stream_type).toBe('mjpeg');
      expect(single.title).toBe('Driveway');

      const double = parseLiveViewConfig(JSON.stringify(JSON.stringify(data)), 'id-1');
      expect(double.stream_url).toBe('https://example.com/mjpeg');
      expect(double.stream_type).toBe('mjpeg');
    });

    it('normalizes stream_type, fit, and handles fallback stream_id/expand URLs', () => {
      const cfg = parseLiveViewConfig({
        stream_url: 'https://example.com/live',
        stream_type: 'UNKNOWN_TYPE',
        fit: 'not-cover',
        title: '   ',
      }, 'fallback-widget-id');

      expect(cfg.stream_type).toBe('webrtc');
      expect(cfg.fit).toBe('contain');
      expect(cfg.title).toBe('Live View');
      expect(cfg.stream_id).toBe('fallback-widget-id');
      expect(cfg.expand_stream_url).toBe('https://example.com/live');
      expect(cfg.expand_stream_type).toBe('webrtc');
    });

    it('handles timeout_seconds edge cases: ignores zero, negative, and non-numeric', () => {
      expect(parseLiveViewConfig({ timeout_seconds: 0 }).timeout_seconds).toBeUndefined();
      expect(parseLiveViewConfig({ timeout_seconds: -10 }).timeout_seconds).toBeUndefined();
      expect(parseLiveViewConfig({ timeout_seconds: 'abc' }).timeout_seconds).toBeUndefined();
      expect(parseLiveViewConfig({ timeout_seconds: '30' }).timeout_seconds).toBe(30);
    });
  });
});

describe('LiveViewWidget component', () => {
  const widgetId = 'w-live-1';
  const initialStore = useDisplayStore.getState();

  if (typeof window.PointerEvent === 'undefined') {
    // @ts-expect-error polyfill PointerEvent for JSDOM
    window.PointerEvent = class PointerEvent extends MouseEvent {
      pointerId: number;
      width: number;
      height: number;
      pressure: number;
      pointerType: string;
      isPrimary: boolean;

      constructor(type: string, params: PointerEventInit = {}) {
        super(type, params);
        this.pointerId = params.pointerId ?? 0;
        this.width = params.width ?? 1;
        this.height = params.height ?? 1;
        this.pressure = params.pressure ?? 0;
        this.pointerType = params.pointerType ?? '';
        this.isPrimary = params.isPrimary ?? false;
      }
    };
  }

  class MockRTCPeerConnection {
    transceivers: Array<{ kind: string; init?: unknown }> = [];
    iceGatheringState = 'complete';
    localDescription: { sdp: string } | null = { sdp: 'v=0\r\no=mock\r\n' };
    closed = false;
    ontrack: ((evt: { streams?: MediaStream[]; track?: MediaStreamTrack }) => void) | null = null;
    listeners: Record<string, Array<() => void>> = {};

    addTransceiver(kind: string, init?: unknown) {
      this.transceivers.push({ kind, init });
    }
    createOffer() {
      return Promise.resolve({ type: 'offer' as const, sdp: 'v=0\r\no=mock\r\n' });
    }
    setLocalDescription() {
      return Promise.resolve();
    }
    setRemoteDescription() {
      return Promise.resolve();
    }
    close() {
      this.closed = true;
    }
    addEventListener(evt: string, fn: () => void) {
      if (!this.listeners[evt]) this.listeners[evt] = [];
      this.listeners[evt].push(fn);
    }
    removeEventListener(evt: string, fn: () => void) {
      if (!this.listeners[evt]) return;
      this.listeners[evt] = this.listeners[evt].filter((l) => l !== fn);
    }
  }

  let originalPeerConnection: typeof RTCPeerConnection;
  let originalFetch: typeof globalThis.fetch;
  let originalPlay: typeof HTMLMediaElement.prototype.play;
  let originalPause: typeof HTMLMediaElement.prototype.pause;
  let lastPeerConnection: MockRTCPeerConnection | null = null;

  beforeEach(() => {
    vi.useFakeTimers();
    useDisplayStore.setState(initialStore, true);

    originalPeerConnection = window.RTCPeerConnection;
    originalFetch = globalThis.fetch;
    originalPlay = HTMLMediaElement.prototype.play;
    originalPause = HTMLMediaElement.prototype.pause;

    HTMLMediaElement.prototype.play = vi.fn().mockResolvedValue(undefined);
    HTMLMediaElement.prototype.pause = vi.fn();

    lastPeerConnection = null;
    // @ts-expect-error mock RTCPeerConnection
    window.RTCPeerConnection = class extends MockRTCPeerConnection {
      constructor() {
        super();
        // eslint-disable-next-line @typescript-eslint/no-this-alias
        lastPeerConnection = this;
      }
    };

    globalThis.fetch = vi.fn().mockImplementation(async (url: string) => {
      if (url.includes('webrtc') || url.includes('stream')) {
        return {
          ok: true,
          status: 200,
          text: async () => JSON.stringify({ sdp: 'v=0\r\no=mock-answer\r\n' }),
        };
      }
      return {
        ok: true,
        status: 200,
        text: async () => JSON.stringify({ status: 'ok' }),
      };
    });
  });

  afterEach(() => {
    cleanup();
    vi.clearAllTimers();
    vi.useRealTimers();
    window.RTCPeerConnection = originalPeerConnection;
    globalThis.fetch = originalFetch;
    HTMLMediaElement.prototype.play = originalPlay;
    HTMLMediaElement.prototype.pause = originalPause;
  });

  it('renders live view DOM structure without duplicate data-widget-id', () => {
    useDisplayStore.setState({
      widgets: {
        [widgetId]: {
          meta: makeWidgetMeta({
            stream_url: 'https://cam.example.com/webrtc',
            title: 'Backyard',
            fit: 'cover',
            poster_url: 'https://cam.example.com/poster.jpg',
          }),
          data: {},
          state: 'ready',
          timestamp: '2026-10-31T12:00:00Z',
        },
      },
    });

    const { container } = render(
      <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
        <LiveViewWidget id={widgetId} type="live-view" />
      </PaneContext.Provider>,
    );

    const root = container.querySelector('.widget-live-view');
    expect(root).not.toBeNull();
    // Critical architectural invariant: no data-widget-id on inner element
    expect(root?.getAttribute('data-widget-id')).toBeNull();
    expect(root?.getAttribute('data-fit')).toBe('cover');
    expect(root?.getAttribute('data-expand')).toBe('true');

    const viewport = container.querySelector('.live-view-viewport');
    expect(viewport).not.toBeNull();

    const video = container.querySelector('video.live-view-media') as HTMLVideoElement;
    expect(video).not.toBeNull();
    expect(video.getAttribute('poster')).toBe('https://cam.example.com/poster.jpg');

    const img = container.querySelector('img.live-view-mjpeg') as HTMLImageElement;
    expect(img).not.toBeNull();
    expect(img.getAttribute('alt')).toBe('Backyard');
    expect(img.style.display).toBe('none');

    const badge = container.querySelector('.live-view-badge');
    expect(badge).not.toBeNull();
    expect(badge?.querySelector('.live-view-dot')).not.toBeNull();
    expect(badge?.querySelector('.live-view-title')?.textContent).toBe('Backyard');

    const spinner = container.querySelector('.live-view-spinner');
    expect(spinner).not.toBeNull();
    expect(spinner?.querySelector('.video-spinner-ring')).not.toBeNull();
  });

  it('starts WebRTC negotiation and connects ontrack stream to video element', async () => {
    useDisplayStore.setState({
      widgets: {
        [widgetId]: {
          meta: makeWidgetMeta({
            stream_url: 'https://cam.example.com/webrtc',
            stream_type: 'webrtc',
            title: 'Driveway',
          }),
          data: {},
          state: 'ready',
          timestamp: '2026-10-31T12:00:00Z',
        },
      },
    });

    const { container } = render(
      <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
        <LiveViewWidget id={widgetId} type="live-view" />
      </PaneContext.Provider>,
    );

    await vi.waitFor(() => {
      expect(lastPeerConnection).not.toBeNull();
    });

    expect(lastPeerConnection?.transceivers.length).toBe(1);
    expect(lastPeerConnection?.transceivers[0].kind).toBe('video');

    await vi.waitFor(() => {
      expect(globalThis.fetch).toHaveBeenCalledWith(
        'https://cam.example.com/webrtc',
        expect.objectContaining({
          method: 'POST',
          headers: { 'Content-Type': 'application/sdp' },
          body: 'v=0\r\no=mock\r\n',
        }),
      );
    });

    // Simulate ontrack with a stream
    const video = container.querySelector('video.live-view-media') as HTMLVideoElement;
    const playSpy = vi.spyOn(video, 'play').mockResolvedValue(undefined);

    const fakeStream = {} as MediaStream;
    act(() => {
      lastPeerConnection?.ontrack?.({ streams: [fakeStream] });
    });

    expect(video.srcObject).toBe(fakeStream);
    expect(playSpy).toHaveBeenCalled();
  });

  it('plays MJPEG stream on img element and hides video', () => {
    useDisplayStore.setState({
      widgets: {
        [widgetId]: {
          meta: makeWidgetMeta({
            stream_url: 'https://cam.example.com/mjpeg',
            stream_type: 'mjpeg',
            title: 'Porch',
          }),
          data: {},
          state: 'ready',
          timestamp: '2026-10-31T12:00:00Z',
        },
      },
    });

    const { container } = render(
      <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
        <LiveViewWidget id={widgetId} type="live-view" />
      </PaneContext.Provider>,
    );

    const img = container.querySelector('img.live-view-mjpeg') as HTMLImageElement;
    const video = container.querySelector('video.live-view-media') as HTMLVideoElement;

    expect(img.getAttribute('src')).toBe('https://cam.example.com/mjpeg');
    expect(img.style.display).toBe('');
    expect(video.style.display).toBe('none');
    expect(lastPeerConnection).toBeNull();
  });

  it('plays HLS stream on video element and hides img', () => {
    useDisplayStore.setState({
      widgets: {
        [widgetId]: {
          meta: makeWidgetMeta({
            stream_url: 'https://cam.example.com/index.m3u8',
            stream_type: 'hls',
            title: 'HLS Stream',
          }),
          data: {},
          state: 'ready',
          timestamp: '2026-10-31T12:00:00Z',
        },
      },
    });

    const { container } = render(
      <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
        <LiveViewWidget id={widgetId} type="live-view" />
      </PaneContext.Provider>,
    );

    const video = container.querySelector('video.live-view-media') as HTMLVideoElement;
    const img = container.querySelector('img.live-view-mjpeg') as HTMLImageElement;

    expect(video.getAttribute('src')).toBe('https://cam.example.com/index.m3u8');
    expect(video.muted).toBe(true);
    expect(video.volume).toBe(0);
    expect(video.style.display).toBe('');
    expect(img.style.display).toBe('none');
    expect(lastPeerConnection).toBeNull();
  });

  it('tears down decoders when isCurrentPane becomes false, and resumes when current pane again', async () => {
    useDisplayStore.setState({
      widgets: {
        [widgetId]: {
          meta: makeWidgetMeta({
            stream_url: 'https://cam.example.com/webrtc',
            stream_type: 'webrtc',
          }),
          data: {},
          state: 'ready',
          timestamp: '2026-10-31T12:00:00Z',
        },
      },
    });

    const { rerender } = render(
      <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
        <LiveViewWidget id={widgetId} type="live-view" />
      </PaneContext.Provider>,
    );

    await vi.waitFor(() => {
      expect(lastPeerConnection).not.toBeNull();
    });
    const firstPc = lastPeerConnection;

    // Switch pane
    rerender(
      <PaneContext.Provider value={{ isCurrentPane: false, paneIndex: 0 }}>
        <LiveViewWidget id={widgetId} type="live-view" />
      </PaneContext.Provider>,
    );

    expect(firstPc?.closed).toBe(true);

    // Switch back to current pane
    rerender(
      <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
        <LiveViewWidget id={widgetId} type="live-view" />
      </PaneContext.Provider>,
    );

    await vi.waitFor(() => {
      expect(lastPeerConnection).not.toBe(firstPc);
      expect(lastPeerConnection?.closed).toBe(false);
    });
  });

  it('tears down decoders when document becomes hidden', async () => {
    useDisplayStore.setState({
      widgets: {
        [widgetId]: {
          meta: makeWidgetMeta({
            stream_url: 'https://cam.example.com/webrtc',
            stream_type: 'webrtc',
          }),
          data: {},
          state: 'ready',
          timestamp: '2026-10-31T12:00:00Z',
        },
      },
    });

    render(
      <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
        <LiveViewWidget id={widgetId} type="live-view" />
      </PaneContext.Provider>,
    );

    await vi.waitFor(() => {
      expect(lastPeerConnection).not.toBeNull();
    });
    const activePc = lastPeerConnection;

    act(() => {
      useDisplayStore.getState().setDocumentVisible(false);
    });

    expect(activePc?.closed).toBe(true);
  });

  it('tears down decoders when IntersectionObserver reports non-intersecting', async () => {
    let intersectionCb: ((entries: IntersectionObserverEntry[]) => void) | null = null;
    const originalIO = window.IntersectionObserver;

    class MockIO {
      constructor(cb: (entries: IntersectionObserverEntry[]) => void) {
        intersectionCb = cb;
      }
      observe = vi.fn();
      disconnect = vi.fn();
      unobserve = vi.fn();
    }
    // @ts-expect-error mock IntersectionObserver
    window.IntersectionObserver = MockIO;

    try {
      useDisplayStore.setState({
        widgets: {
          [widgetId]: {
            meta: makeWidgetMeta({
              stream_url: 'https://cam.example.com/webrtc',
              stream_type: 'webrtc',
            }),
            data: {},
            state: 'ready',
            timestamp: '2026-10-31T12:00:00Z',
          },
        },
      });

      const { container } = render(
        <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
          <LiveViewWidget id={widgetId} type="live-view" />
        </PaneContext.Provider>,
      );

      await vi.waitFor(() => {
        expect(lastPeerConnection).not.toBeNull();
      });
      const activePc = lastPeerConnection;

      const root = container.querySelector('.widget-live-view');
      // Trigger non-intersecting entry
      act(() => {
        intersectionCb?.([
          {
            target: root!,
            isIntersecting: false,
          } as unknown as IntersectionObserverEntry,
        ]);
      });

      expect(activePc?.closed).toBe(true);
    } finally {
      window.IntersectionObserver = originalIO;
    }
  });

  it('tears down decoders and clears loading when videoMode becomes "video", and resumes on "widgets"', async () => {
    useDisplayStore.setState({
      widgets: {
        [widgetId]: {
          meta: makeWidgetMeta({
            stream_url: 'https://cam.example.com/webrtc',
            stream_type: 'webrtc',
          }),
          data: {},
          state: 'ready',
          timestamp: '2026-10-31T12:00:00Z',
        },
      },
    });

    const { container } = render(
      <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
        <LiveViewWidget id={widgetId} type="live-view" />
      </PaneContext.Provider>,
    );

    await vi.waitFor(() => {
      expect(lastPeerConnection).not.toBeNull();
    });
    const activePc = lastPeerConnection;

    // Switch to fullscreen video mode
    act(() => {
      useDisplayStore.setState({
        video: {
          mode: 'video',
          primary: {
            id: 'cast-stream',
            stream_url: 'https://cam.example.com/fullscreen',
            type: 'webrtc',
          },
          pip: null,
        },
      });
    });

    expect(activePc?.closed).toBe(true);
    const root = container.querySelector('.widget-live-view');
    expect(root?.classList.contains('loading')).toBe(false);

    // Return to widgets dashboard mode
    act(() => {
      useDisplayStore.setState({
        video: { mode: 'widgets', primary: null, pip: null },
      });
    });

    await vi.waitFor(() => {
      expect(lastPeerConnection).not.toBe(activePc);
      expect(lastPeerConnection?.closed).toBe(false);
    });
  });

  it('handles tap-to-wake: fires wake action and video trigger with payload', async () => {
    useDisplayStore.setState({
      widgets: {
        [widgetId]: {
          meta: makeWidgetMeta({
            stream_url: 'https://cam.example.com/stream',
            stream_type: 'webrtc',
            stream_id: 'front-door',
            controllable: true,
            control_url: 'https://cam.example.com/control',
            priority: 'temporary',
            timeout_seconds: 45,
          }),
          data: {},
          state: 'ready',
          timestamp: '2026-10-31T12:00:00Z',
        },
      },
    });

    const { container } = render(
      <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
        <LiveViewWidget id={widgetId} type="live-view" />
      </PaneContext.Provider>,
    );

    const root = container.querySelector('.widget-live-view')!;

    // Tap without drag
    act(() => {
      fireEvent.pointerDown(root, { clientX: 100, clientY: 100 });
      fireEvent.pointerUp(root, { clientX: 102, clientY: 101 });
      fireEvent.click(root);
    });

    // Root should have loading class
    expect(root.classList.contains('loading')).toBe(true);

    // Control wake URL
    expect(globalThis.fetch).toHaveBeenCalledWith(
      'https://cam.example.com/control',
      expect.objectContaining({
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: 'front-door', action: 'wake' }),
      }),
    );

    // Video trigger URL with timeout_seconds
    expect(globalThis.fetch).toHaveBeenCalledWith(
      'api/video/trigger',
      expect.objectContaining({
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          id: 'front-door',
          stream_url: 'https://cam.example.com/stream',
          type: 'webrtc',
          priority: 'temporary',
          controllable: true,
          control_url: 'https://cam.example.com/control',
          timeout_seconds: 45,
        }),
      }),
    );

    // Loading safety reset after 2000ms
    act(() => {
      vi.advanceTimersByTime(2000);
    });
    expect(root.classList.contains('loading')).toBe(false);
  });

  it('omits timeout_seconds when not provided or zero, and respects expand_stream_url', async () => {
    useDisplayStore.setState({
      widgets: {
        [widgetId]: {
          meta: makeWidgetMeta({
            stream_url: 'https://cam.example.com/ambient',
            expand_stream_url: 'https://cam.example.com/hq',
            expand_stream_type: 'hls',
            timeout_seconds: 0,
          }),
          data: {},
          state: 'ready',
          timestamp: '2026-10-31T12:00:00Z',
        },
      },
    });

    const { container } = render(
      <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
        <LiveViewWidget id={widgetId} type="live-view" />
      </PaneContext.Provider>,
    );

    const root = container.querySelector('.widget-live-view')!;

    act(() => {
      fireEvent.click(root);
    });

    expect(globalThis.fetch).toHaveBeenCalledWith(
      'api/video/trigger',
      expect.objectContaining({
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          id: widgetId,
          stream_url: 'https://cam.example.com/hq',
          type: 'hls',
          priority: 'persistent',
          controllable: false,
          control_url: '',
        }),
      }),
    );
  });

  it('rejects drag gestures (hypot >= 15) and suppresses synthetic clicks within 400ms', () => {
    useDisplayStore.setState({
      widgets: {
        [widgetId]: {
          meta: makeWidgetMeta({
            stream_url: 'https://cam.example.com/stream',
          }),
          data: {},
          state: 'ready',
          timestamp: '2026-10-31T12:00:00Z',
        },
      },
    });

    const { container } = render(
      <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
        <LiveViewWidget id={widgetId} type="live-view" />
      </PaneContext.Provider>,
    );

    const root = container.querySelector('.widget-live-view')!;

    // Drag: move from (0, 0) to (20, 20) -> dist = 28.28 >= 15
    act(() => {
      fireEvent.pointerDown(root, { clientX: 0, clientY: 0 });
      fireEvent.pointerUp(root, { clientX: 20, clientY: 20 });
      // Browser dispatches synthetic click right after pointerup
      fireEvent.click(root);
    });

    expect(globalThis.fetch).not.toHaveBeenCalledWith(
      'api/video/trigger',
      expect.anything(),
    );
    expect(root.classList.contains('loading')).toBe(false);
  });

  it('ignores tap when expand_on_click is false', () => {
    useDisplayStore.setState({
      widgets: {
        [widgetId]: {
          meta: makeWidgetMeta({
            stream_url: 'https://cam.example.com/stream',
            expand_on_click: false,
          }),
          data: {},
          state: 'ready',
          timestamp: '2026-10-31T12:00:00Z',
        },
      },
    });

    const { container } = render(
      <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
        <LiveViewWidget id={widgetId} type="live-view" />
      </PaneContext.Provider>,
    );

    const root = container.querySelector('.widget-live-view')!;
    expect(root.getAttribute('data-expand')).toBe('false');

    act(() => {
      fireEvent.click(root);
    });

    expect(globalThis.fetch).not.toHaveBeenCalledWith(
      'api/video/trigger',
      expect.anything(),
    );
  });

  it('handles trigger fetch rejection gracefully by clearing loading state', async () => {
    globalThis.fetch = vi.fn().mockRejectedValue(new Error('Network error'));

    useDisplayStore.setState({
      widgets: {
        [widgetId]: {
          meta: makeWidgetMeta({
            stream_url: 'https://cam.example.com/stream',
          }),
          data: {},
          state: 'ready',
          timestamp: '2026-10-31T12:00:00Z',
        },
      },
    });

    const { container } = render(
      <PaneContext.Provider value={{ isCurrentPane: true, paneIndex: 0 }}>
        <LiveViewWidget id={widgetId} type="live-view" />
      </PaneContext.Provider>,
    );

    const root = container.querySelector('.widget-live-view')!;

    await act(async () => {
      fireEvent.click(root);
    });

    expect(root.classList.contains('loading')).toBe(false);
  });
});

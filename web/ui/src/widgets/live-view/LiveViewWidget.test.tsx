// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, fireEvent, createEvent, act } from '@testing-library/react';
import { LiveViewWidget, parseLiveViewConfig, computeStaggerDelayMs, activeAutoLiveStreams } from './LiveViewWidget';
import { resolveCamerasStatusUrl } from './liveViewHelpers';
import { useDisplayStore } from '../../store';
import { PaneContext } from '../../carousel/PaneContext';
import { itemAt } from '../../test-utils/lookup';
import { FakeRTCPeerConnection, MockMediaStream, createFakeTrack } from '../../test-utils/mediaFakes';
import { renderWidget, seedWidget, widgetMeta } from '../../test-utils/widget';


/** The w-live-1 widget inside a PaneContext, for rerender() after renderWidget(). */
function liveViewPane(isCurrentPane: boolean, paneIndex = 0) {
  return <PaneContext.Provider value={{ isCurrentPane, paneIndex }}><LiveViewWidget id="w-live-1" type="live-view" /></PaneContext.Provider>;
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
      expect(def.auto_live_on_motion_seconds).toBeUndefined();
      expect(def.max_auto_live_streams).toBeUndefined();

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
        auto_live_on_motion_seconds: 60,
        max_auto_live_streams: 2,
      });
      expect(cfg.auto_live_on_motion_seconds).toBe(60);
      expect(cfg.max_auto_live_streams).toBe(2);

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

    it.each([
      { input: { timeout_seconds: 0 }, want: undefined },
      { input: { timeout_seconds: -10 }, want: undefined },
      { input: { timeout_seconds: 'abc' }, want: undefined },
      { input: { timeout_seconds: '30' }, want: 30 },
    ])('handles timeout_seconds ($input.timeout_seconds)', ({ input, want }) => {
      expect(parseLiveViewConfig(input).timeout_seconds).toBe(want);
    });

    it('parses refresh_interval_seconds, background_refresh_interval_seconds, and stagger_index with mjpeg defaults', () => {
      const mjpegDef = parseLiveViewConfig({ stream_type: 'mjpeg' });
      expect(mjpegDef.refresh_interval_seconds).toBe(15);
      expect(mjpegDef.background_refresh_interval_seconds).toBe(300);
      expect(mjpegDef.stagger_index).toBeUndefined();

      const customMjpeg = parseLiveViewConfig({
        stream_type: 'mjpeg',
        refresh_interval_seconds: 20,
        background_refresh_interval_seconds: 600,
        stagger_index: 4,
      });
      expect(customMjpeg.refresh_interval_seconds).toBe(20);
      expect(customMjpeg.background_refresh_interval_seconds).toBe(600);
      expect(customMjpeg.stagger_index).toBe(4);

      const zeroRefreshMjpeg = parseLiveViewConfig({
        stream_type: 'mjpeg',
        refresh_interval_seconds: 0,
        background_refresh_interval_seconds: 0,
      });
      expect(zeroRefreshMjpeg.refresh_interval_seconds).toBe(0);
      expect(zeroRefreshMjpeg.background_refresh_interval_seconds).toBe(0);

      const webrtcDef = parseLiveViewConfig({ stream_type: 'webrtc' });
      expect(webrtcDef.refresh_interval_seconds).toBeUndefined();
      expect(webrtcDef.background_refresh_interval_seconds).toBeUndefined();
      expect(webrtcDef.stagger_index).toBeUndefined();
    });

    it('computes stagger delay using stagger_index or deterministic id hash', () => {
      expect(computeStaggerDelayMs('cam-nursery', 0, 1600)).toBe(0);
      expect(computeStaggerDelayMs('cam-doorbell', 1, 1600)).toBe(1600);
      expect(computeStaggerDelayMs('cam-driveway', 8, 5000)).toBe(40000);
      const hashed = computeStaggerDelayMs('cam-nursery', undefined, 1600);
      expect(hashed).toBeGreaterThanOrEqual(0);
      expect(hashed).toBeLessThan(9 * 1600);
      expect(hashed % 1600).toBe(0);
    });
  });
});

describe('LiveViewWidget component', () => {
  const widgetId = 'w-live-1';
  const seedLiveView = (config: Record<string, unknown> = {}) => { seedWidget(widgetId, widgetMeta('live-view', config, [3, 2]), {}); };

  if (typeof window.PointerEvent === 'undefined') {
    // @ts-expect-error polyfill PointerEvent for JSDOM
    window.PointerEvent = class PointerEvent extends MouseEvent {
      pointerId: number;
      width: number;
      height: number;
      pressure: number;
      pointerType: string;
      isPrimary: boolean;
      changedTouches?: { clientX: number; clientY: number }[];

      constructor(type: string, params: PointerEventInit & { changedTouches?: { clientX: number; clientY: number }[] } = {}) {
        super(type, params);
        this.pointerId = params.pointerId ?? 0;
        this.width = params.width ?? 1;
        this.height = params.height ?? 1;
        this.pressure = params.pressure ?? 0;
        this.pointerType = params.pointerType ?? '';
        this.isPrimary = params.isPrimary ?? false;
        if (params.changedTouches) this.changedTouches = params.changedTouches;
      }
    };
  }

  let originalPeerConnection: typeof RTCPeerConnection;
  let originalFetch: typeof globalThis.fetch;
  let originalPlay: typeof HTMLMediaElement.prototype.play;
  let originalPause: typeof HTMLMediaElement.prototype.pause;
  let lastPeerConnection: FakeRTCPeerConnection | null = null;

  beforeEach(() => {
    vi.useFakeTimers();

    originalPeerConnection = window.RTCPeerConnection;
    originalFetch = globalThis.fetch;
    originalPlay = HTMLMediaElement.prototype.play;
    originalPause = HTMLMediaElement.prototype.pause;

    lastPeerConnection = null;
    // @ts-expect-error mock RTCPeerConnection
    window.RTCPeerConnection = class extends FakeRTCPeerConnection {
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
    vi.clearAllTimers();
    window.RTCPeerConnection = originalPeerConnection;
    globalThis.fetch = originalFetch;
    HTMLMediaElement.prototype.play = originalPlay;
    HTMLMediaElement.prototype.pause = originalPause;
  });

  it('renders live view DOM structure without duplicate data-widget-id', () => {
    seedLiveView({
      stream_url: 'https://cam.example.com/webrtc',
      title: 'Backyard',
      fit: 'cover',
      poster_url: 'https://cam.example.com/poster.jpg',
    });

    const { container } = renderWidget(LiveViewWidget, widgetId, 'live-view');

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
    seedLiveView({
      stream_url: 'https://cam.example.com/webrtc',
      stream_type: 'webrtc',
      title: 'Driveway',
    });

    const { container } = renderWidget(LiveViewWidget, widgetId, 'live-view');

    await vi.waitFor(() => {
      expect(lastPeerConnection).not.toBeNull();
    });

    expect(lastPeerConnection?.transceivers.length).toBe(1);
    expect(itemAt(lastPeerConnection?.transceivers, 0).kind).toBe('video');

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
    seedLiveView({
      stream_url: 'https://cam.example.com/mjpeg',
      stream_type: 'mjpeg',
      title: 'Porch',
    });

    const { container } = renderWidget(LiveViewWidget, widgetId, 'live-view');

    const img = container.querySelector('img.live-view-mjpeg') as HTMLImageElement;
    const video = container.querySelector('video.live-view-media') as HTMLVideoElement;

    expect(img.getAttribute('src')).toBe('https://cam.example.com/mjpeg');
    expect(img.style.display).toBe('');
    expect(video.style.display).toBe('none');
    expect(lastPeerConnection).toBeNull();
  });

  it('plays HLS stream on video element and hides img', () => {
    seedLiveView({
      stream_url: 'https://cam.example.com/index.m3u8',
      stream_type: 'hls',
      title: 'HLS Stream',
    });

    const { container } = renderWidget(LiveViewWidget, widgetId, 'live-view');

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
    seedLiveView({
      stream_url: 'https://cam.example.com/webrtc',
      stream_type: 'webrtc',
    });

    const { rerender } = renderWidget(LiveViewWidget, widgetId, 'live-view');

    await vi.waitFor(() => {
      expect(lastPeerConnection).not.toBeNull();
    });
    const firstPc = lastPeerConnection;

    // Switch pane
    rerender(liveViewPane(false));

    expect(firstPc?.closed).toBe(true);

    // Switch back to current pane
    rerender(liveViewPane(true));

    await vi.waitFor(() => {
      expect(lastPeerConnection).not.toBe(firstPc);
      expect(lastPeerConnection?.closed).toBe(false);
    });
  });

  it('tears down decoders when document becomes hidden', async () => {
    seedLiveView({
      stream_url: 'https://cam.example.com/webrtc',
      stream_type: 'webrtc',
    });

    renderWidget(LiveViewWidget, widgetId, 'live-view');

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
      seedLiveView({
        stream_url: 'https://cam.example.com/webrtc',
        stream_type: 'webrtc',
      });

      const { container } = renderWidget(LiveViewWidget, widgetId, 'live-view');

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
    seedLiveView({
      stream_url: 'https://cam.example.com/webrtc',
      stream_type: 'webrtc',
    });

    const { container } = renderWidget(LiveViewWidget, widgetId, 'live-view');

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
    seedLiveView({
      stream_url: 'https://cam.example.com/stream',
      stream_type: 'webrtc',
      stream_id: 'front-door',
      controllable: true,
      control_url: 'https://cam.example.com/control',
      priority: 'temporary',
      timeout_seconds: 45,
    });

    const { container } = renderWidget(LiveViewWidget, widgetId, 'live-view');

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
    seedLiveView({
      stream_url: 'https://cam.example.com/ambient',
      expand_stream_url: 'https://cam.example.com/hq',
      expand_stream_type: 'hls',
      timeout_seconds: 0,
    });

    const { container } = renderWidget(LiveViewWidget, widgetId, 'live-view');

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
    seedLiveView({
      stream_url: 'https://cam.example.com/stream',
    });

    const { container } = renderWidget(LiveViewWidget, widgetId, 'live-view');

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
    seedLiveView({
      stream_url: 'https://cam.example.com/stream',
      expand_on_click: false,
    });

    const { container } = renderWidget(LiveViewWidget, widgetId, 'live-view');

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

    seedLiveView({
      stream_url: 'https://cam.example.com/stream',
    });

    const { container } = renderWidget(LiveViewWidget, widgetId, 'live-view');

    const root = container.querySelector('.widget-live-view')!;

    await act(async () => {
      fireEvent.click(root);
    });

    expect(root.classList.contains('loading')).toBe(false);
  });

  it('waits for ICE gathering completion before sending offer', async () => {
    let peerInstance: FakeRTCPeerConnection | null = null;
    // @ts-expect-error mock RTCPeerConnection
    window.RTCPeerConnection = function () {
      const pc = new FakeRTCPeerConnection();
      pc.iceGatheringState = 'gathering';
      peerInstance = pc;
      lastPeerConnection = pc;
      return pc;
    };

    seedLiveView({
      stream_url: 'https://cam.example.com/webrtc',
      stream_type: 'webrtc',
    });

    renderWidget(LiveViewWidget, widgetId, 'live-view');

    await vi.waitFor(() => {
      expect(peerInstance?.listeners.icegatheringstatechange?.length).toBe(1);
    });

    // Offer not sent yet because ICE gathering is still in progress
    expect(globalThis.fetch).not.toHaveBeenCalledWith('https://cam.example.com/webrtc', expect.anything());
    HTMLMediaElement.prototype.play = vi.fn().mockRejectedValueOnce(new Error('autoplay blocked'));

    // Fire non-complete then complete ICE gathering state change
    act(() => {
      peerInstance!.dispatchEvent('icegatheringstatechange');
      peerInstance!.iceGatheringState = 'complete';
      peerInstance!.dispatchEvent('icegatheringstatechange');
    });

    await vi.waitFor(() => {
      expect(globalThis.fetch).toHaveBeenCalledWith(
        'https://cam.example.com/webrtc',
        expect.anything(),
      );
    });
  });

  it('proceeds after 500ms ICE gathering timeout if complete event does not fire', async () => {
    let peerInstance: FakeRTCPeerConnection | null = null;
    // @ts-expect-error mock RTCPeerConnection
    window.RTCPeerConnection = function () {
      const pc = new FakeRTCPeerConnection();
      pc.iceGatheringState = 'gathering';
      peerInstance = pc;
      lastPeerConnection = pc;
      return pc;
    };

    seedLiveView({
      stream_url: 'https://cam.example.com/webrtc',
      stream_type: 'webrtc',
    });

    renderWidget(LiveViewWidget, widgetId, 'live-view');
    await act(async () => { await vi.advanceTimersByTimeAsync(499); });
    expect(peerInstance).not.toBeNull();
    expect(globalThis.fetch).not.toHaveBeenCalledWith('https://cam.example.com/webrtc', expect.anything());

    // The 500ms ICE gathering timeout fires and the offer is posted.
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    expect(globalThis.fetch).toHaveBeenCalledWith('https://cam.example.com/webrtc', expect.anything());
  });

  it('aborts ICE gathering wait when component is unmounted', async () => {
    let peerInstance: FakeRTCPeerConnection | null = null;
    // @ts-expect-error mock RTCPeerConnection
    window.RTCPeerConnection = function () {
      const pc = new FakeRTCPeerConnection();
      pc.iceGatheringState = 'gathering';
      peerInstance = pc;
      lastPeerConnection = pc;
      return pc;
    };

    seedLiveView({
      stream_url: 'https://cam.example.com/webrtc',
      stream_type: 'webrtc',
    });

    const { unmount } = renderWidget(LiveViewWidget, widgetId, 'live-view');

    await vi.waitFor(() => {
      expect(peerInstance).not.toBeNull();
    });

    unmount();
    expect(peerInstance!.closed).toBe(true);
  });

  it('constructs MediaStream with the track when the streams array is empty', async () => {
    const originalMediaStream = window.MediaStream;
    // @ts-expect-error mock MediaStream
    window.MediaStream = MockMediaStream;

    try {
      seedLiveView({
        stream_url: 'https://cam.example.com/webrtc',
        stream_type: 'webrtc',
      });

      const { container } = renderWidget(LiveViewWidget, widgetId, 'live-view');

      await vi.waitFor(() => {
        expect(lastPeerConnection).not.toBeNull();
      });

      const video = container.querySelector('video.live-view-media') as HTMLVideoElement;
      const fakeTrack = createFakeTrack('video', 'track-1');

      act(() => {
        lastPeerConnection?.ontrack?.({ streams: [], track: fakeTrack });
      });

      expect(video.srcObject).toBeInstanceOf(MockMediaStream);
      expect((video.srcObject as unknown as MockMediaStream).tracks).toContain(fakeTrack);
    } finally {
      window.MediaStream = originalMediaStream;
    }
  });

  it('extracts tap coordinates from touch changedTouches and rejects touch drag gestures', () => {
    Object.defineProperty(Object.prototype, 'changedTouches', {
      get() {
        return this.nativeEvent?.changedTouches;
      },
      configurable: true,
    });

    try {
      seedLiveView({
        stream_url: 'https://cam.example.com/stream',
      });

      const { container } = renderWidget(LiveViewWidget, widgetId, 'live-view');

      const root = container.querySelector('.widget-live-view')!;

      // 1. Touch tap without drag: start (100, 100), end (102, 101) -> triggers expand
      act(() => {
        const downEvt = createEvent.pointerDown(root);
        Object.defineProperty(downEvt, 'changedTouches', {
          value: [{ clientX: 100, clientY: 100 }],
        });
        fireEvent(root, downEvt);

        const upEvt = createEvent.pointerUp(root);
        Object.defineProperty(upEvt, 'changedTouches', {
          value: [{ clientX: 102, clientY: 101 }],
        });
        fireEvent(root, upEvt);

        fireEvent.click(root);
      });

      expect(globalThis.fetch).toHaveBeenCalledWith(
        'api/video/trigger',
        expect.anything(),
      );
      expect(root.classList.contains('loading')).toBe(true);

      // Reset fetch and loading
      act(() => {
        vi.advanceTimersByTime(2000);
      });
      vi.mocked(globalThis.fetch).mockClear();

      // 2. Touch drag: start (10, 10), end (50, 50) -> distance >= 15, suppressed
      act(() => {
        const downEvt = createEvent.pointerDown(root);
        Object.defineProperty(downEvt, 'changedTouches', {
          value: [{ clientX: 10, clientY: 10 }],
        });
        fireEvent(root, downEvt);

        const upEvt = createEvent.pointerUp(root);
        Object.defineProperty(upEvt, 'changedTouches', {
          value: [{ clientX: 50, clientY: 50 }],
        });
        fireEvent(root, upEvt);

        fireEvent.click(root);
      });

      expect(globalThis.fetch).not.toHaveBeenCalledWith(
        'api/video/trigger',
        expect.anything(),
      );
    } finally {
      // @ts-expect-error clean up prototype getter
      delete Object.prototype.changedTouches;
    }
  });

  it('catches and logs warning when wake action fetch fails', async () => {
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});
    globalThis.fetch = vi.fn().mockImplementation((url: string) => {
      if (url === 'https://cam.example.com/wake') {
        return Promise.reject(new Error('Wake offline'));
      }
      return Promise.resolve(new Response('{}', { status: 200 }));
    });

    seedLiveView({
      stream_url: 'https://cam.example.com/stream',
      controllable: true,
      control_url: 'https://cam.example.com/wake',
    });

    const { container } = renderWidget(LiveViewWidget, widgetId, 'live-view');

    const root = container.querySelector('.widget-live-view')!;
    await act(async () => {
      fireEvent.click(root);
    });

    expect(warnSpy).toHaveBeenCalledWith(
      '[MirrormereLiveView] Wake action failed:',
      expect.any(Error),
    );
    warnSpy.mockRestore();
  });

  it('handles events with invalid coordinates gracefully', () => {
    seedLiveView({
      stream_url: 'https://cam.example.com/stream',
    });

    const { container } = renderWidget(LiveViewWidget, widgetId, 'live-view');

    const root = container.querySelector('.widget-live-view')!;
    const downEvt = createEvent.pointerDown(root);
    Object.defineProperty(downEvt, 'clientX', { value: NaN });
    Object.defineProperty(downEvt, 'clientY', { value: NaN });
    fireEvent(root, downEvt);

    const upEvt = createEvent.pointerUp(root);
    Object.defineProperty(upEvt, 'clientX', { value: NaN });
    Object.defineProperty(upEvt, 'clientY', { value: NaN });
    fireEvent(root, upEvt);
  });

  it('handles WebRTC SDP exchange HTTP failure gracefully', async () => {
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});
    globalThis.fetch = vi.fn().mockImplementation((url: string) => {
      if (url === 'https://cam.example.com/webrtc-err') {
        return Promise.resolve(new Response('Server Error', { status: 500 }));
      }
      return Promise.resolve(new Response('{}', { status: 200 }));
    });

    seedLiveView({
      stream_url: 'https://cam.example.com/webrtc-err',
      stream_type: 'webrtc',
    });

    renderWidget(LiveViewWidget, widgetId, 'live-view');

    await vi.waitFor(() => {
      expect(warnSpy).toHaveBeenCalledWith(
        expect.stringContaining('WebRTC negotiation error'),
        expect.any(Error),
      );
    });
    warnSpy.mockRestore();
  });

  it('closes RTCPeerConnection if unmounted while SDP exchange is in flight', async () => {
    let resolveSdp!: (res: Response) => void;
    const sdpPromise = new Promise<Response>((resolve) => {
      resolveSdp = resolve;
    });

    globalThis.fetch = vi.fn().mockImplementation((url: string) => {
      if (url === 'https://cam.example.com/webrtc-hang') {
        return sdpPromise;
      }
      return Promise.resolve(new Response('{}', { status: 200 }));
    });

    seedLiveView({
      stream_url: 'https://cam.example.com/webrtc-hang',
      stream_type: 'webrtc',
    });

    const { unmount } = renderWidget(LiveViewWidget, widgetId, 'live-view');

    await vi.waitFor(() => {
      expect(lastPeerConnection).not.toBeNull();
    });

    unmount();
    resolveSdp(new Response('v=0\r\n', { status: 200 }));
    expect(lastPeerConnection?.closed).toBe(true);
  });

  it('closes RTCPeerConnection if unmounted while setRemoteDescription is in flight', async () => {
    let resolveRemote!: () => void;
    const remotePromise = new Promise<void>((resolve) => {
      resolveRemote = resolve;
    });

    seedLiveView({
      stream_url: 'https://cam.example.com/webrtc',
      stream_type: 'webrtc',
    });

    const { unmount } = renderWidget(LiveViewWidget, widgetId, 'live-view');

    await vi.waitFor(() => {
      expect(lastPeerConnection).not.toBeNull();
    });
    const pc = lastPeerConnection!;
    pc.setRemoteDescription = () => remotePromise;

    // Trigger state change so fetch resolves
    act(() => {
      pc.iceGatheringState = 'complete';
      pc.dispatchEvent('icegatheringstatechange');
    });

    await vi.waitFor(() => {
      expect(globalThis.fetch).toHaveBeenCalledWith('https://cam.example.com/webrtc', expect.anything());
    });

    unmount();
    resolveRemote();
    expect(pc.closed).toBe(true);
  });

  it('handles trigger API returning HTTP 500 error and clears safety timer on error', async () => {
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});
    globalThis.fetch = vi.fn().mockImplementation((url: string) => {
      if (url === 'api/video/trigger') {
        return Promise.resolve(new Response('Internal Server Error', { status: 500 }));
      }
      return Promise.resolve(new Response('{}', { status: 200 }));
    });

    seedLiveView({
      stream_url: 'https://cam.example.com/stream',
    });

    const { container } = renderWidget(LiveViewWidget, widgetId, 'live-view');

    const root = container.querySelector('.widget-live-view')!;
    await act(async () => {
      fireEvent.click(root);
    });

    expect(warnSpy).toHaveBeenCalledWith(
      expect.stringContaining('Trigger failed for'),
      expect.any(Error),
    );
    expect(root.classList.contains('loading')).toBe(false);
    warnSpy.mockRestore();
  });

  it('clears safetyTimerRef on unmount when loading is active', () => {
    seedLiveView({
      stream_url: 'https://cam.example.com/stream',
    });

    const { container, unmount } = renderWidget(LiveViewWidget, widgetId, 'live-view');

    const root = container.querySelector('.widget-live-view')!;
    act(() => {
      fireEvent.click(root);
    });
    expect(root.classList.contains('loading')).toBe(true);

    unmount();
    // Verify advancing timers causes no exceptions after unmount
    act(() => {
      vi.advanceTimersByTime(2500);
    });
  });

  it('resets existing safetyTimer on subsequent click after debounce window', () => {
    seedLiveView({
      stream_url: 'https://cam.example.com/stream',
    });

    const { container } = renderWidget(LiveViewWidget, widgetId, 'live-view');

    const root = container.querySelector('.widget-live-view')!;
    // First click arms safety timer
    act(() => {
      fireEvent.click(root);
    });
    expect(root.classList.contains('loading')).toBe(true);

    // Advance 400ms past 350ms debounce threshold, while 2000ms safety timer is still active
    act(() => {
      vi.advanceTimersByTime(400);
    });

    // Second click clears prior safety timer and re-arms
    act(() => {
      fireEvent.click(root);
    });
    expect(root.classList.contains('loading')).toBe(true);
  });

  it('closes pc and returns early if unmounted during SDP answer text resolution', async () => {
    let resolveText: (v: string) => void = () => {};
    const textPromise = new Promise<string>((resolve) => {
      resolveText = resolve;
    });

    globalThis.fetch = vi.fn().mockImplementation((url: string) => {
      if (url === 'https://cam.example.com/webrtc') {
        return Promise.resolve({
          ok: true,
          status: 200,
          text: () => textPromise,
        });
      }
      return Promise.resolve(new Response('{}', { status: 200 }));
    });

    seedLiveView({
      stream_url: 'https://cam.example.com/webrtc',
      stream_type: 'webrtc',
    });

    const { unmount } = renderWidget(LiveViewWidget, widgetId, 'live-view');

    await vi.waitFor(() => {
      expect(lastPeerConnection).not.toBeNull();
    });
    const pc = lastPeerConnection!;

    act(() => {
      pc.iceGatheringState = 'complete';
      pc.dispatchEvent('icegatheringstatechange');
    });

    await vi.waitFor(() => {
      expect(globalThis.fetch).toHaveBeenCalledWith('https://cam.example.com/webrtc', expect.anything());
    });

    unmount();
    resolveText('v=0\r\nanswer');
    expect(pc.closed).toBe(true);
  });

  it('returns early when RTCPeerConnection is unavailable in the environment', () => {
    const origPC = window.RTCPeerConnection;
    // @ts-expect-error test undefined RTCPeerConnection
    delete window.RTCPeerConnection;

    try {
      seedLiveView({
        stream_url: 'https://cam.example.com/webrtc',
        stream_type: 'webrtc',
      });

      const { container } = renderWidget(LiveViewWidget, widgetId, 'live-view');

      expect(container.querySelector('.widget-live-view')).not.toBeNull();
    } finally {
      window.RTCPeerConnection = origPC;
    }
  });

  it('stops playback when fullscreen video mode is active', () => {
    useDisplayStore.setState({
      video: {
        mode: 'video',
        primary: null,
        pip: null,
      },
      widgets: {
        [widgetId]: {
          meta: widgetMeta('live-view', {
            stream_url: 'https://cam.example.com/webrtc',
            stream_type: 'webrtc',
          }, [3, 2]),
          data: {},
          state: 'ready',
          timestamp: '2026-10-31T12:00:00Z',
        },
      },
    });

    const { container } = renderWidget(LiveViewWidget, widgetId, 'live-view');

    // Peer connection should not be initialized when videoMode === 'video'
    expect(container.querySelector('.widget-live-view')).not.toBeNull();
  });

  it('sends complete payload with timeout_seconds, expand URLs, and temporary priority in triggerExpand', async () => {
    globalThis.fetch = vi.fn().mockImplementation((url: string) => {
      if (url === 'https://cam.example.com/wake') {
        return Promise.resolve(new Response('{}', { status: 200 }));
      }
      if (url === 'api/video/trigger') {
        return Promise.resolve(new Response('{}', { status: 200 }));
      }
      return Promise.resolve(new Response('{}', { status: 200 }));
    });

    seedLiveView({
      stream_url: 'https://cam.example.com/stream',
      stream_id: 'cam-custom',
      priority: 'temporary',
      timeout_seconds: 45,
      controllable: true,
      control_url: 'https://cam.example.com/wake',
      expand_stream_url: 'https://cam.example.com/expand-stream',
      expand_stream_type: 'hls',
    });

    const { container } = renderWidget(LiveViewWidget, widgetId, 'live-view');

    const root = container.querySelector('.widget-live-view')!;
    await act(async () => {
      fireEvent.click(root);
    });

    expect(globalThis.fetch).toHaveBeenCalledWith(
      'https://cam.example.com/wake',
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ id: 'cam-custom', action: 'wake' }),
      }),
    );

    expect(globalThis.fetch).toHaveBeenCalledWith(
      'api/video/trigger',
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({
          id: 'cam-custom',
          stream_url: 'https://cam.example.com/expand-stream',
          type: 'hls',
          priority: 'temporary',
          controllable: true,
          control_url: 'https://cam.example.com/wake',
          timeout_seconds: 45,
        }),
      }),
    );
  });

  it('staggers on-screen mjpeg snapshot refresh and preserves img.src when swiping off-pane into 5-minute background refresh', () => {
    seedLiveView({
      stream_url: '/hearth-api/cameras/doorbell/stream',
      stream_type: 'mjpeg',
      title: 'Doorbell',
      refresh_interval_seconds: 15,
      background_refresh_interval_seconds: 300,
      stagger_index: 2,
    });

    const { container, rerender } = renderWidget(LiveViewWidget, widgetId, 'live-view');

    const img = container.querySelector('img.live-view-mjpeg') as HTMLImageElement;
    expect(img.getAttribute('src')).toBe('/hearth-api/cameras/doorbell/stream');

    // Stagger index 2 * 1600ms = 3200ms offset before first staggered refresh
    act(() => {
      vi.advanceTimersByTime(3100);
    });
    expect(img.getAttribute('src')).toBe('/hearth-api/cameras/doorbell/stream');

    act(() => {
      vi.advanceTimersByTime(150);
    });
    const firstRefreshedSrc = img.getAttribute('src') ?? '';
    expect(firstRefreshedSrc).toMatch(/^\/hearth-api\/cameras\/doorbell\/stream\?t=\d+$/);

    // Next refresh after 15000ms
    act(() => {
      vi.advanceTimersByTime(15000);
    });
    const secondRefreshedSrc = img.getAttribute('src') ?? '';
    expect(secondRefreshedSrc).toMatch(/^\/hearth-api\/cameras\/doorbell\/stream\?t=\d+$/);
    expect(secondRefreshedSrc).not.toBe(firstRefreshedSrc);

    // Swipe off-pane: img.src MUST be preserved (not cleared to '')
    rerender(liveViewPane(false));
    expect(img.getAttribute('src')).toBe(secondRefreshedSrc);

    // Off-pane background refresh: stagger 2 * 5000ms = 10000ms + 300000ms (5 minutes)
    act(() => {
      vi.advanceTimersByTime(10000 + 299000);
    });
    expect(img.getAttribute('src')).toBe(secondRefreshedSrc);

    act(() => {
      vi.advanceTimersByTime(1500);
    });
    const bgRefreshedSrc = img.getAttribute('src') ?? '';
    expect(bgRefreshedSrc).toMatch(/^\/hearth-api\/cameras\/doorbell\/stream\?t=\d+$/);
    expect(bgRefreshedSrc).not.toBe(secondRefreshedSrc);
  });

  it('hydrates initial mjpeg streamUrl when mounted directly off-pane and appends &t= when query params exist', () => {
    seedLiveView({
      stream_url: '/hearth-api/cameras/nursery_cam/stream?quality=high',
      stream_type: 'mjpeg',
      title: 'Nursery Cam',
      stagger_index: 0,
    });

    const { container } = renderWidget(LiveViewWidget, widgetId, 'live-view', { pane: { isCurrentPane: false, paneIndex: 1 } });

    const img = container.querySelector('img.live-view-mjpeg') as HTMLImageElement;
    expect(img.getAttribute('src')).toBe('/hearth-api/cameras/nursery_cam/stream?quality=high');

    act(() => {
      vi.advanceTimersByTime(300000);
    });
    expect(img.getAttribute('src')).toMatch(/^\/hearth-api\/cameras\/nursery_cam\/stream\?quality=high&t=\d+$/);
  });

  it('disables periodic on-screen and background refresh timers when refresh_interval_seconds and background_refresh_interval_seconds are 0', () => {
    seedLiveView({
      stream_url: '/hearth-api/cameras/doorbell/stream',
      stream_type: 'mjpeg',
      title: 'Doorbell',
      refresh_interval_seconds: 0,
      background_refresh_interval_seconds: 0,
      stagger_index: 1,
    });

    const { container, rerender } = renderWidget(LiveViewWidget, widgetId, 'live-view');

    const img = container.querySelector('img.live-view-mjpeg') as HTMLImageElement;
    expect(img.getAttribute('src')).toBe('/hearth-api/cameras/doorbell/stream');

    // Advance well past normal 15s on-screen refresh + stagger: img.src must not append ?t=
    act(() => {
      vi.advanceTimersByTime(120000);
    });
    expect(img.getAttribute('src')).toBe('/hearth-api/cameras/doorbell/stream');

    // Switch off-pane: background_refresh_interval_seconds: 0 must also disable periodic timer
    rerender(liveViewPane(false));

    act(() => {
      vi.advanceTimersByTime(600000);
    });
    expect(img.getAttribute('src')).toBe('/hearth-api/cameras/doorbell/stream');
  });

  it('pauses on-screen mjpeg refresh after 5-minute idle timeout without changing screen unless idle_timeout_seconds is set, and resets timer on pointerdown', () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue({ ok: true, status: 200 } as Response);
    useDisplayStore.setState((s) => ({ ...s, screen: { ...s.screen, currentScreen: 1 } }));
    seedLiveView({
      stream_url: '/hearth-api/cameras/nursery_cam/stream',
      stream_type: 'mjpeg',
      title: 'Nursery Cam',
      refresh_interval_seconds: 30,
      background_refresh_interval_seconds: 0,
      stagger_index: 0,
    });

    const { container, unmount } = renderWidget(LiveViewWidget, widgetId, 'live-view', { pane: { isCurrentPane: true, paneIndex: 1 } });
    const img = container.querySelector('img.live-view-mjpeg') as HTMLImageElement;

    act(() => { vi.advanceTimersByTime(240_000); });
    expect(useDisplayStore.getState().screen.currentScreen).toBe(1);
    act(() => { fireEvent.pointerDown(window); });
    act(() => { vi.advanceTimersByTime(240_000); });
    expect(useDisplayStore.getState().screen.currentScreen).toBe(1);

    const srcBeforeIdle = img.getAttribute('src') ?? '';
    expect(srcBeforeIdle).toMatch(/^\/hearth-api\/cameras\/nursery_cam\/stream\?t=\d+$/);

    act(() => { vi.advanceTimersByTime(60_000); });
    expect(useDisplayStore.getState().screen.currentScreen).toBe(1);
    expect(fetchMock).not.toHaveBeenCalledWith('api/screen/select', expect.anything());

    const srcAtIdle = img.getAttribute('src') ?? '';
    act(() => { vi.advanceTimersByTime(120_000); });
    expect(img.getAttribute('src')).toBe(srcAtIdle);

    // Opt-in screen-0 navigation fires only when idle_timeout_seconds is explicitly configured
    unmount();
    seedLiveView({ stream_url: '/hearth-api/cameras/nursery_cam/stream', stream_type: 'mjpeg', idle_timeout_seconds: 120, stagger_index: 0 });
    renderWidget(LiveViewWidget, widgetId, 'live-view', { pane: { isCurrentPane: true, paneIndex: 1 } });
    act(() => { vi.advanceTimersByTime(119_000); });
    expect(useDisplayStore.getState().screen.currentScreen).toBe(1);
    act(() => { vi.advanceTimersByTime(1_000); });
    expect(useDisplayStore.getState().screen.currentScreen).toBe(0);
    expect(fetchMock).toHaveBeenCalledWith('api/screen/select', expect.objectContaining({ method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ screen_index: 0 }) }));
  });

  it('promotes mjpeg stream to inline WebRTC when /hearth-api/cameras reports auto_live and falls back to static img after 60s of calm', async () => {
    activeAutoLiveStreams.clear();
    const nowEpoch = Math.ceil(Date.now() / 1000);
    const cameraPayload: Record<string, unknown> = {
      cameras: [{ id: 'front', stream_url: '/hearth-api/cameras/front/stream', auto_live: true, last_event_epoch: nowEpoch }],
    };

    globalThis.fetch = vi.fn().mockImplementation(async (url: string) => ({
      ok: true,
      status: 200,
      text: async () => JSON.stringify(url === '/hearth-api/cameras' ? cameraPayload : { sdp: 'v=0\r\no=mock-answer\r\n' }),
    }));

    seedLiveView({
      stream_id: 'front',
      stream_url: '/hearth-api/cameras/front/stream',
      stream_type: 'mjpeg',
      expand_stream_url: 'http://localhost:1984/api/webrtc?src=front',
      expand_stream_type: 'webrtc',
      auto_live_on_motion_seconds: 60,
      max_auto_live_streams: 2,
      refresh_interval_seconds: 0,
      background_refresh_interval_seconds: 0,
    });

    const { container } = renderWidget(LiveViewWidget, widgetId, 'live-view');

    const video = container.querySelector('video.live-view-media') as HTMLVideoElement;
    const img = container.querySelector('img.live-view-mjpeg') as HTMLImageElement;

    await vi.waitFor(() => {
      expect(video.style.display).toBe('');
      expect(img.style.display).toBe('none');
      expect(lastPeerConnection).not.toBeNull();
    });

    await vi.waitFor(() => {
      expect(globalThis.fetch).toHaveBeenCalledWith(
        'http://localhost:1984/api/webrtc?src=front',
        expect.objectContaining({ method: 'POST', headers: { 'Content-Type': 'application/sdp' } }),
      );
    });

    const promotedPc = lastPeerConnection;
    expect(promotedPc?.closed).toBe(false);

    // Non-OK polls are ignored gracefully while calm timer remains active up to the 60s deadline
    globalThis.fetch = vi.fn().mockResolvedValue({ ok: false, status: 503, text: async () => '' });
    await act(async () => { vi.advanceTimersByTime(5_000); });
    expect(video.style.display).toBe('');
    expect(promotedPc?.closed).toBe(false);

    // Advance to just before the 60s calm deadline (59s total since promotion): playback still persists
    await act(async () => {
      vi.advanceTimersByTime(54_000);
    });
    expect(video.style.display).toBe('');
    expect(promotedPc?.closed).toBe(false);

    // Advance past the 60s calm deadline: calm timer demotes back to static img without any catalog response
    await act(async () => {
      vi.advanceTimersByTime(2_000);
    });

    await vi.waitFor(() => {
      expect(video.style.display).toBe('none');
      expect(img.style.display).toBe('');
      expect(promotedPc?.closed).toBe(true);
    });
  });

  it('clears continuous MJPEG img.src (including unknown URLs like /api/camera_proxy_stream/camera.front) when off-pane, hidden, or idle while preserving finite snapshot polling', () => {
    seedLiveView({ stream_url: '/api/camera_proxy_stream/camera.front', stream_type: 'mjpeg', title: 'Continuous Cam' });
    const { container, rerender } = renderWidget(LiveViewWidget, widgetId, 'live-view');
    const img = container.querySelector('img.live-view-mjpeg') as HTMLImageElement;
    expect(img.getAttribute('src')).toBe('/api/camera_proxy_stream/camera.front');

    const renderPane = (isCurrentPane: boolean) => {
      rerender(liveViewPane(isCurrentPane));
    };

    // Off-pane, document hidden, and idle each tear down continuous MJPEG img.src
    renderPane(false);
    expect(img.getAttribute('src')).toBeNull();
    renderPane(true);
    expect(img.getAttribute('src')).toBe('/api/camera_proxy_stream/camera.front');
    act(() => { useDisplayStore.getState().setDocumentVisible(false); });
    expect(img.getAttribute('src')).toBeNull();
    act(() => { useDisplayStore.getState().setDocumentVisible(true); });
    expect(img.getAttribute('src')).toBe('/api/camera_proxy_stream/camera.front');
    act(() => { vi.advanceTimersByTime(300_000); });
    expect(img.getAttribute('src')).toBeNull();
    act(() => { fireEvent.pointerDown(window); });

    // Finite snapshot endpoints preserve img.src when off-pane, document hidden, or idle
    for (const url of ['/hearth-api/cameras/porch/stream', '/api/camera_proxy/camera.front']) {
      seedLiveView({ stream_url: url, stream_type: 'mjpeg', title: 'Snapshot Cam', stagger_index: 0 });
      renderPane(true);
      expect(img.getAttribute('src')).toBe(url);
      renderPane(false);
      expect(img.getAttribute('src')).toBe(url);
      act(() => { useDisplayStore.getState().setDocumentVisible(false); });
      expect(img.getAttribute('src')).toBe(url);
      act(() => { useDisplayStore.getState().setDocumentVisible(true); });
      renderPane(true);
      act(() => { vi.advanceTimersByTime(300_000); });
      expect(img.getAttribute('src')).toContain(url);
      act(() => { fireEvent.pointerDown(window); });
    }
  });

  it('does not rewrite continuous MJPEG stream URLs with ?t= refresh timers while preserving snapshot refresh', () => {
    for (const url of ['/api/camera_proxy_stream/camera.front', 'https://cam.example.com/mjpeg']) {
      seedLiveView({ stream_url: url, stream_type: 'mjpeg', title: 'Continuous MJPEG', stagger_index: 0 });
      const { container, unmount } = renderWidget(LiveViewWidget, widgetId, 'live-view');
      const img = container.querySelector('img.live-view-mjpeg') as HTMLImageElement;
      expect(img.getAttribute('src')).toBe(url);
      act(() => { vi.advanceTimersByTime(30_000); });
      expect(img.getAttribute('src')).toBe(url);
      unmount();
    }
    seedLiveView({ stream_url: '/hearth-api/cameras/porch/stream', stream_type: 'mjpeg', title: 'Snapshot MJPEG', stagger_index: 0 });
    const { container, unmount } = renderWidget(LiveViewWidget, widgetId, 'live-view');
    const img = container.querySelector('img.live-view-mjpeg') as HTMLImageElement;
    act(() => { vi.advanceTimersByTime(16_000); });
    expect(img.getAttribute('src')).toMatch(/^\/hearth-api\/cameras\/porch\/stream\?t=\d+$/);
    unmount();
  });

  it('enforces max_auto_live_streams concurrency cap of 2 when 3 widgets receive motion and demotes on off-pane', async () => {
    activeAutoLiveStreams.clear();
    const nowEpoch = Math.floor(Date.now() / 1000);

    globalThis.fetch = vi.fn().mockImplementation(async (url: string) => ({
      ok: true,
      status: 200,
      text: async () => JSON.stringify(
        url === '/hearth-api/cameras'
          ? {
              cameras: [
                { id: 'cam1', auto_live: true, last_event_epoch: nowEpoch - 20 },
                { id: 'unmatched_id', snapshot_url: '/hearth-api/cameras/alias2/stream', last_event_epoch: nowEpoch - 10 },
                { id: 'cam3', auto_live: true },
              ],
            }
          : { sdp: 'v=0\r\no=mock-answer\r\n' },
      ),
    }));

    const mkCamEntry = (sid: string) => ({
      meta: widgetMeta('live-view', {
        stream_id: sid, stream_url: `/hearth-api/cameras/${sid}/stream`, stream_type: 'mjpeg',
        expand_stream_url: 'http://localhost:1984/api/webrtc?src=front', auto_live_on_motion_seconds: 60,
        max_auto_live_streams: 2, refresh_interval_seconds: 0, background_refresh_interval_seconds: 0,
      }, [3, 2]),
      data: {}, state: 'ready' as const, timestamp: '2026-10-31T12:00:00Z',
    });

    useDisplayStore.setState({
      widgets: { 'w-cam-1': mkCamEntry('cam1'), 'w-cam-2': mkCamEntry('alias2'), 'w-cam-3': mkCamEntry('cam3') },
    });

    const threeWidgets = (isCurrentPane: boolean) => (
      <PaneContext.Provider value={{ isCurrentPane, paneIndex: 0 }}>
        <div>
          <LiveViewWidget id="w-cam-1" type="live-view" />
          <LiveViewWidget id="w-cam-2" type="live-view" />
          <LiveViewWidget id="w-cam-3" type="live-view" />
        </div>
      </PaneContext.Provider>
    );
    const { container, rerender } = render(threeWidgets(true));

    await vi.waitFor(() => {
      expect(activeAutoLiveStreams.size).toBe(2);
      const videos = Array.from(container.querySelectorAll<HTMLVideoElement>('video.live-view-media'));
      expect(videos.filter((v) => v.style.display === '').length).toBe(2);
      expect(itemAt(videos, 0).style.display).toBe('none');
    });

    expect(activeAutoLiveStreams.has('w-cam-2')).toBe(true);
    expect(activeAutoLiveStreams.has('w-cam-3')).toBe(true);
    expect(activeAutoLiveStreams.has('w-cam-1')).toBe(false);

    // Next 5s poll keeps w-cam-1 rejected at the concurrency cap
    await act(async () => { vi.advanceTimersByTime(5_000); });
    expect(activeAutoLiveStreams.size).toBe(2);
    expect(activeAutoLiveStreams.has('w-cam-1')).toBe(false);

    // Swiping off-pane immediately releases all auto-live slots and demotes video
    rerender(threeWidgets(false));
    expect(activeAutoLiveStreams.size).toBe(0);
  });

  it('resolveCamerasStatusUrl preserves prefix/origin and falls back to base-relative hearth-api/cameras', () => {
    expect(resolveCamerasStatusUrl('/hearth-api/cameras/front/stream')).toBe('/hearth-api/cameras');
    expect(resolveCamerasStatusUrl('/ingress/hearth-api/cameras/front/stream?t=1#h')).toBe('/ingress/hearth-api/cameras');
    expect(resolveCamerasStatusUrl('https://mirror.local:8443/ingress/hearth-api/camera/stream/front')).toBe('https://mirror.local:8443/ingress/hearth-api/cameras');
    expect(resolveCamerasStatusUrl('hearth-api/cameras/front/stream')).toBe('hearth-api/cameras');
    expect(resolveCamerasStatusUrl('http://localhost:1984/api/stream')).toBe('hearth-api/cameras');
    expect(resolveCamerasStatusUrl('http://localhost:1984/api/stream?ref=hearth-api/camera')).toBe('hearth-api/cameras');
    expect(resolveCamerasStatusUrl('')).toBe('hearth-api/cameras');
  });

  it('polls prefixed or base-relative hearth-api/cameras from stream_url', async () => {
    for (const [streamUrl, expected] of [
      ['/ingress/hearth-api/cameras/front/stream', '/ingress/hearth-api/cameras'],
      ['http://localhost:1984/api/stream?src=front', 'hearth-api/cameras'],
    ] as const) {
      activeAutoLiveStreams.clear();
      const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 200, text: async () => '{"cameras":[]}' });
      globalThis.fetch = fetchMock;
      seedLiveView({ stream_id: 'front', stream_url: streamUrl, stream_type: 'mjpeg', auto_live_on_motion_seconds: 60 });
      const { unmount } = renderWidget(LiveViewWidget, widgetId, 'live-view');
      await vi.waitFor(() => { expect(fetchMock).toHaveBeenCalledWith(expected, expect.objectContaining({ signal: expect.any(AbortSignal) })); });
      unmount();
    }
  });
});

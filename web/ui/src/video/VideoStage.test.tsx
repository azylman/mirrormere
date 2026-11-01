import { act, cleanup, render } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { VideoSource } from '../sse/types';
import { useDisplayStore } from '../store';
import {
  getNormalizedVideoCoordinates,
  VideoPlayerManager,
  VideoStage,
} from './VideoStage';

class MockRTCPeerConnection {
  public connectionState: RTCPeerConnectionState = 'new';
  public iceConnectionState: RTCIceConnectionState = 'new';
  public iceGatheringState: RTCIceGatheringState = 'complete';
  public localDescription: RTCSessionDescriptionInit | null = null;
  public remoteDescription: RTCSessionDescriptionInit | null = null;
  public transceivers: Array<{
    kind: string;
    direction: string;
    receiver: { playoutDelayHint: number | null; track: { kind: string } };
  }> = [];
  public ontrack: ((ev: RTCTrackEvent) => void) | null = null;
  public onconnectionstatechange: ((ev: Event) => void) | null = null;
  public oniceconnectionstatechange: ((ev: Event) => void) | null = null;
  public closed = false;

  private listeners = new Map<string, Array<(e: Event) => void>>();

  addEventListener(type: string, listener: (e: Event) => void): void {
    if (!this.listeners.has(type)) this.listeners.set(type, []);
    this.listeners.get(type)!.push(listener);
  }

  removeEventListener(type: string, listener: (e: Event) => void): void {
    const arr = this.listeners.get(type);
    if (arr) {
      this.listeners.set(type, arr.filter((l) => l !== listener));
    }
  }

  dispatchEvent(event: Event): boolean {
    const arr = this.listeners.get(event.type) || [];
    for (const l of arr) l(event);
    return true;
  }

  setConnectionState(state: RTCPeerConnectionState): void {
    this.connectionState = state;
    this.onconnectionstatechange?.(new Event('connectionstatechange'));
    this.dispatchEvent(new Event('connectionstatechange'));
  }

  setIceConnectionState(state: RTCIceConnectionState): void {
    this.iceConnectionState = state;
    this.oniceconnectionstatechange?.(new Event('iceconnectionstatechange'));
    this.dispatchEvent(new Event('iceconnectionstatechange'));
  }

  addTransceiver(
    kind: string,
    init?: { direction?: string },
  ): {
    kind: string;
    direction: string;
    receiver: { playoutDelayHint: number | null; track: { kind: string } };
  } {
    const receiver = { playoutDelayHint: null, track: { kind } };
    const t = { kind, direction: init?.direction ?? 'sendrecv', receiver };
    this.transceivers.push(t);
    return t;
  }

  getReceivers(): Array<{ playoutDelayHint: number | null; track: { kind: string } }> {
    return this.transceivers.map((t) => t.receiver);
  }

  async createOffer(): Promise<RTCSessionDescriptionInit> {
    return { type: 'offer', sdp: 'v=0\r\no=- 12345 2 IN IP4 127.0.0.1\r\ns=Mirrormere' };
  }

  async setLocalDescription(desc: RTCSessionDescriptionInit): Promise<void> {
    this.localDescription = desc;
  }

  async setRemoteDescription(desc: RTCSessionDescriptionInit): Promise<void> {
    this.remoteDescription = desc;
  }

  close(): void {
    this.closed = true;
    this.connectionState = 'closed';
    this.iceConnectionState = 'closed';
  }
}

function createMockAudioManager() {
  const registered = new Set<HTMLMediaElement>();
  return {
    registered,
    registerMediaElement: vi.fn((el: HTMLMediaElement) => {
      registered.add(el);
      if (el) el.volume = 0.6;
    }),
    unregisterMediaElement: vi.fn((el: HTMLMediaElement) => {
      registered.delete(el);
    }),
  };
}

function createMockCarousel() {
  return {
    paused: false,
    pause: vi.fn(function (this: { paused: boolean }) {
      this.paused = true;
    }),
    resume: vi.fn(function (this: { paused: boolean }) {
      this.paused = false;
    }),
  };
}

describe('VideoStage and VideoPlayerManager', () => {
  const initialStore = useDisplayStore.getState();

  beforeEach(() => {
    cleanup();
    document.body.innerHTML = '';
    useDisplayStore.setState(initialStore, true);
    delete window.videoManager;
    delete window.videoHUD;
    delete window.audioManager;
    HTMLMediaElement.prototype.play = vi.fn().mockResolvedValue(undefined);
    HTMLMediaElement.prototype.load = vi.fn();
    HTMLMediaElement.prototype.pause = vi.fn();
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
    cleanup();
    document.body.innerHTML = '';
    useDisplayStore.setState(initialStore, true);
    delete window.videoManager;
    delete window.videoHUD;
    delete window.audioManager;
  });

  describe('getNormalizedVideoCoordinates', () => {
    it('calculates normalized coordinates correctly with standard 16:9 display', () => {
      const mockVideo = {
        videoWidth: 1920,
        videoHeight: 1080,
        getBoundingClientRect: () =>
          ({ left: 0, top: 0, width: 1920, height: 1080, right: 1920, bottom: 1080 }) as DOMRect,
      };

      const coords = getNormalizedVideoCoordinates({ clientX: 960, clientY: 540 }, mockVideo);
      expect(coords).not.toBeNull();
      expect(coords?.x).toBe(0.5);
      expect(coords?.y).toBe(0.5);
    });

    it('compensates for pillarboxing when 16:9 video is rendered inside ultrawide container', () => {
      // 16:9 video in 2560x1080 container: content width is 1920, offset left is 320
      const mockVideo = {
        videoWidth: 1920,
        videoHeight: 1080,
        getBoundingClientRect: () =>
          ({ left: 0, top: 0, width: 2560, height: 1080, right: 2560, bottom: 1080 }) as DOMRect,
      };

      const center = getNormalizedVideoCoordinates({ clientX: 1280, clientY: 540 }, mockVideo);
      expect(center?.x).toBe(0.5);
      expect(center?.y).toBe(0.5);

      const left = getNormalizedVideoCoordinates({ clientX: 320, clientY: 540 }, mockVideo);
      expect(left?.x).toBe(0);

      const right = getNormalizedVideoCoordinates({ clientX: 2240, clientY: 540 }, mockVideo);
      expect(right?.x).toBe(1);
    });

    it('compensates for letterboxing when 16:9 video is rendered inside 4:3 container', () => {
      // 16:9 video in 1920x1440 container: content height is 1080, offset top is 180
      const mockVideo = {
        videoWidth: 1920,
        videoHeight: 1080,
        getBoundingClientRect: () =>
          ({ left: 0, top: 0, width: 1920, height: 1440, right: 1920, bottom: 1440 }) as DOMRect,
      };

      const center = getNormalizedVideoCoordinates({ clientX: 960, clientY: 720 }, mockVideo);
      expect(center?.x).toBe(0.5);
      expect(center?.y).toBe(0.5);

      const top = getNormalizedVideoCoordinates({ clientX: 960, clientY: 180 }, mockVideo);
      expect(top?.y).toBe(0);

      const bottom = getNormalizedVideoCoordinates({ clientX: 960, clientY: 1260 }, mockVideo);
      expect(bottom?.y).toBe(1);
    });

    it('clamps coordinates outside bounds to [0.0, 1.0]', () => {
      const mockVideo = {
        videoWidth: 1920,
        videoHeight: 1080,
        getBoundingClientRect: () =>
          ({ left: 0, top: 0, width: 1920, height: 1080, right: 1920, bottom: 1080 }) as DOMRect,
      };

      const outside = getNormalizedVideoCoordinates({ clientX: -100, clientY: 2000 }, mockVideo);
      expect(outside?.x).toBe(0);
      expect(outside?.y).toBe(1);
    });

    it('handles touch events using touches and changedTouches', () => {
      const mockVideo = {
        videoWidth: 1000,
        videoHeight: 1000,
        getBoundingClientRect: () =>
          ({ left: 0, top: 0, width: 1000, height: 1000, right: 1000, bottom: 1000 }) as DOMRect,
      };

      const touchCoord = getNormalizedVideoCoordinates(
        { touches: [{ clientX: 250, clientY: 750 }] },
        mockVideo,
      );
      expect(touchCoord?.x).toBe(0.25);
      expect(touchCoord?.y).toBe(0.75);

      const changedTouchCoord = getNormalizedVideoCoordinates(
        { changedTouches: [{ clientX: 800, clientY: 200 }] },
        mockVideo,
      );
      expect(changedTouchCoord?.x).toBe(0.8);
      expect(changedTouchCoord?.y).toBe(0.2);
    });

    it('returns null on invalid elements or zero bounds', () => {
      expect(getNormalizedVideoCoordinates({ clientX: 100, clientY: 100 }, null as unknown as HTMLVideoElement)).toBeNull();
      const zeroRectVideo = {
        getBoundingClientRect: () => ({ left: 0, top: 0, width: 0, height: 0, right: 0, bottom: 0 }) as DOMRect,
      };
      expect(getNormalizedVideoCoordinates({ clientX: 100, clientY: 100 }, zeroRectVideo)).toBeNull();
    });

    it('instance method produces identical results to exported pure function', () => {
      const mgr = new VideoPlayerManager({});
      const mockVideo = {
        videoWidth: 1920,
        videoHeight: 1080,
        getBoundingClientRect: () =>
          ({ left: 0, top: 0, width: 1920, height: 1080, right: 1920, bottom: 1080 }) as DOMRect,
      };
      const direct = getNormalizedVideoCoordinates({ clientX: 960, clientY: 540 }, mockVideo);
      const instance = mgr.getNormalizedVideoCoordinates({ clientX: 960, clientY: 540 }, mockVideo);
      expect(direct).toEqual(instance);
      mgr.destroy();
    });
  });

  describe('Presentation Mode Switching & Carousel Coordination', () => {
    it('pauses carousel, leaves grid-canvas unmutated, and shows video-stage on entering video mode', () => {
      const gridCanvas = document.createElement('main');
      gridCanvas.id = 'grid-canvas';
      document.body.appendChild(gridCanvas);

      const stage = document.createElement('div');
      stage.id = 'video-stage';
      stage.style.display = 'none';
      document.body.appendChild(stage);

      const primarySlot = document.createElement('div');
      primarySlot.id = 'video-primary-slot';
      stage.appendChild(primarySlot);

      const carousel = createMockCarousel();
      const mgr = new VideoPlayerManager({
        stageElement: stage,
        primarySlot,
        carousel,
      });

      mgr.handleVideoState({
        mode: 'video',
        primary: {
          id: 'camera_1',
          stream_url: 'http://cam/mjpeg',
          type: 'mjpeg',
        },
        pip: null,
      });

      expect(carousel.pause).toHaveBeenCalledTimes(1);
      expect(useDisplayStore.getState().carousel.paused).toBe(true);
      expect(gridCanvas.style.display).toBe('');
      expect(stage.style.display).toBe('flex');
      expect(stage.classList.contains('active')).toBe(true);
      expect(mgr.currentMode).toBe('video');

      // Exit video mode
      mgr.exitVideoMode();

      expect(carousel.resume).toHaveBeenCalledTimes(1);
      expect(useDisplayStore.getState().carousel.paused).toBe(false);
      expect(gridCanvas.style.display).toBe('');
      expect(stage.style.display).toBe('none');
      expect(stage.classList.contains('active')).toBe(false);
      expect(mgr.currentMode).toBe('widgets');

      mgr.destroy();
    });

    it('reconciles primary stream updates without remounting unchanged streams', () => {
      const stage = document.createElement('div');
      const primarySlot = document.createElement('div');
      stage.appendChild(primarySlot);

      const mgr = new VideoPlayerManager({
        stageElement: stage,
        primarySlot,
      });

      const initialSource: VideoSource = {
        id: 'cam_a',
        stream_url: 'http://cam/mjpeg',
        type: 'mjpeg',
        title: 'Initial Title',
      };

      mgr.handleVideoState({ mode: 'video', primary: initialSource, pip: null });
      const firstSession = mgr.primarySession;
      expect(firstSession).not.toBeNull();
      expect(firstSession?.stream.title).toBe('Initial Title');

      // Stream with same ID and stream_url updates metadata in place
      mgr.handleVideoState({
        mode: 'video',
        primary: { ...initialSource, title: 'Updated Title' },
        pip: null,
      });
      expect(mgr.primarySession).toBe(firstSession);
      expect(mgr.primarySession?.stream.title).toBe('Updated Title');

      // Different stream_url replaces session
      mgr.handleVideoState({
        mode: 'video',
        primary: { ...initialSource, stream_url: 'http://cam/mjpeg_hd' },
        pip: null,
      });
      expect(mgr.primarySession).not.toBe(firstSession);
      expect(mgr.primarySession?.stream.stream_url).toBe('http://cam/mjpeg_hd');

      mgr.destroy();
    });
  });

  describe('Multi-Stream Triad & WebRTC Negotiation', () => {
    it('mounts MJPEG stream via responsive img element without registering with AudioManager', () => {
      const stage = document.createElement('div');
      const primarySlot = document.createElement('div');
      stage.appendChild(primarySlot);
      const audioMgr = createMockAudioManager();

      const mgr = new VideoPlayerManager({
        stageElement: stage,
        primarySlot,
        audioManager: audioMgr,
      });

      mgr.handleVideoState({
        mode: 'video',
        primary: { id: 'doorbell', stream_url: 'http://doorbell/stream.mjpg', type: 'mjpeg' },
        pip: null,
      });

      const img = primarySlot.querySelector('img');
      expect(img).not.toBeNull();
      expect(img?.src).toBe('http://doorbell/stream.mjpg');
      expect(audioMgr.registered.size).toBe(0);

      mgr.destroy();
    });

    it('mounts HLS stream and registers with AudioManager', () => {
      const stage = document.createElement('div');
      const primarySlot = document.createElement('div');
      stage.appendChild(primarySlot);
      const audioMgr = createMockAudioManager();

      const mgr = new VideoPlayerManager({
        stageElement: stage,
        primarySlot,
        audioManager: audioMgr,
      });

      mgr.handleVideoState({
        mode: 'video',
        primary: { id: 'hls_stream', stream_url: 'http://host/stream.m3u8', type: 'hls' },
        pip: null,
      });

      const video = primarySlot.querySelector('video');
      expect(video).not.toBeNull();
      expect(video?.muted).toBe(false);
      expect(audioMgr.registerMediaElement).toHaveBeenCalledWith(video);

      mgr.destroy();
    });

    it('mounts WebRTC stream, negotiates SDP offer/answer, and tears down cleanly on exit', async () => {
      const stage = document.createElement('div');
      const primarySlot = document.createElement('div');
      stage.appendChild(primarySlot);

      let sdpPostSent = false;
      const mockFetch = vi.fn().mockImplementation(async (_unusedUrl: string, opts) => {
        if (opts?.method === 'POST') {
          sdpPostSent = true;
          return {
            ok: true,
            status: 200,
            text: async () => JSON.stringify({ sdp: 'v=0\r\ns=go2rtc_answer' }),
          };
        }
        return { ok: true, status: 200, text: async () => '' };
      });

      const mgr = new VideoPlayerManager({
        stageElement: stage,
        primarySlot,
        RTCPeerConnection: MockRTCPeerConnection as unknown as typeof RTCPeerConnection,
        fetch: mockFetch as unknown as typeof fetch,
      });

      mgr.handleVideoState({
        mode: 'video',
        primary: { id: 'webrtc_cam', stream_url: 'http://go2rtc/stream', type: 'webrtc' },
        pip: null,
      });

      const session = mgr.primarySession;
      expect(session).not.toBeNull();
      expect(session?.pc).not.toBeNull();

      // Transceivers added
      const mockPC = session?.pc as unknown as MockRTCPeerConnection;
      expect(mockPC.transceivers.length).toBe(2);
      expect(mockPC.transceivers[0].kind).toBe('video');
      expect(mockPC.transceivers[0].receiver.playoutDelayHint).toBe(0);

      // Run timers for ICE gathering and negotiation
      await act(async () => {
        vi.advanceTimersByTime(600);
      });

      expect(sdpPostSent).toBe(true);
      expect(mockPC.remoteDescription?.sdp).toBe('v=0\r\ns=go2rtc_answer');

      // Teardown
      mgr.exitVideoMode();
      expect(mockPC.closed).toBe(true);
      expect(mgr.primarySession).toBeNull();
      expect(primarySlot.children.length).toBe(0);

      mgr.destroy();
    });
  });

  describe('PiP Slot, Zero-Reparenting Swap-on-Tap, and Audio Focus', () => {
    it('manages PiP slot visibility, audio separation, and interactive swap-on-tap', () => {
      const stage = document.createElement('div');
      stage.id = 'video-stage';
      const primarySlot = document.createElement('div');
      primarySlot.id = 'video-primary-slot';
      const pipSlot = document.createElement('div');
      pipSlot.id = 'video-pip-slot';
      stage.appendChild(primarySlot);
      stage.appendChild(pipSlot);
      document.body.appendChild(stage);

      const audioMgr = createMockAudioManager();
      const mgr = new VideoPlayerManager({
        stageElement: stage,
        primarySlot,
        pipSlot,
        audioManager: audioMgr,
      });

      mgr.handleVideoState({
        mode: 'video',
        primary: { id: 'primary_stream', stream_url: 'http://main/stream.m3u8', type: 'hls' },
        pip: { id: 'pip_stream', stream_url: 'http://pip/stream.m3u8', type: 'hls' },
      });

      expect(pipSlot.style.display).toBe('flex');
      const primaryVideo = primarySlot.querySelector('video') as HTMLVideoElement;
      const pipVideo = pipSlot.querySelector('video') as HTMLVideoElement;

      // Audio focus initially on primary
      expect(primaryVideo.muted).toBe(false);
      expect(audioMgr.registered.has(primaryVideo)).toBe(true);
      expect(pipVideo.muted).toBe(true);
      expect(pipVideo.volume).toBe(0);
      expect(audioMgr.registered.has(pipVideo)).toBe(false);

      // Tap PiP to trigger zero-reparenting swap
      mgr.handleSlotClick('pip');
      expect(mgr.isSwapped).toBe(true);
      expect(stage.dataset.swapped).toBe('true');

      // Audio focus handed over to visual primary (former PiP)
      expect(pipVideo.muted).toBe(false);
      expect(audioMgr.registered.has(pipVideo)).toBe(true);
      expect(primaryVideo.muted).toBe(true);
      expect(primaryVideo.volume).toBe(0);
      expect(audioMgr.registered.has(primaryVideo)).toBe(false);

      // Tap Primary while swapped swaps back
      mgr.handleSlotClick('primary');
      expect(mgr.isSwapped).toBe(false);
      expect(stage.dataset.swapped).toBe('false');
      expect(primaryVideo.muted).toBe(false);
      expect(audioMgr.registered.has(primaryVideo)).toBe(true);

      // Dismissing PiP resets swap state without interrupting primary
      mgr.handleVideoState({
        mode: 'video',
        primary: { id: 'primary_stream', stream_url: 'http://main/stream.m3u8', type: 'hls' },
        pip: null,
      });
      expect(mgr.pipSession).toBeNull();
      expect(pipSlot.style.display).toBe('none');
      expect(mgr.isSwapped).toBe(false);
      expect(mgr.primarySession).not.toBeNull();

      mgr.destroy();
    });
  });

  describe('WebRTC Watchdog, Auto-Reconnect & Fallback', () => {
    it('auto-reconnects with backoff on failure and flushes hardware decode buffer', async () => {
      const stage = document.createElement('div');
      const primarySlot = document.createElement('div');
      stage.appendChild(primarySlot);

      const mockFetch = vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        text: async () => 'v=0\r\ns=answer',
      });

      const mgr = new VideoPlayerManager({
        stageElement: stage,
        primarySlot,
        RTCPeerConnection: MockRTCPeerConnection as unknown as typeof RTCPeerConnection,
        fetch: mockFetch as unknown as typeof fetch,
        reconnectBaseDelay: 100,
      });

      mgr.handleVideoState({
        mode: 'video',
        primary: { id: 'cast', stream_url: 'http://cast/webrtc', type: 'webrtc' },
        pip: null,
      });

      const session = mgr.primarySession!;
      const initialPC = session.pc as unknown as MockRTCPeerConnection;

      // Fail connection
      initialPC.setConnectionState('failed');
      expect(session.reconnectAttempts).toBe(1);

      // Hardware decode frame buffer flushed on reconnect
      await act(async () => {
        vi.advanceTimersByTime(150);
      });

      expect(session.pc).not.toBe(initialPC);
      expect(HTMLMediaElement.prototype.load).toHaveBeenCalled();

      mgr.destroy();
    });

    it('falls back to widgets mode when primary reconnect attempts are exhausted', async () => {
      const stage = document.createElement('div');
      const primarySlot = document.createElement('div');
      stage.appendChild(primarySlot);

      const mgr = new VideoPlayerManager({
        stageElement: stage,
        primarySlot,
        RTCPeerConnection: MockRTCPeerConnection as unknown as typeof RTCPeerConnection,
        maxReconnectAttempts: 2,
        reconnectBaseDelay: 50,
      });

      mgr.handleVideoState({
        mode: 'video',
        primary: { id: 'cam', stream_url: 'http://cam/webrtc', type: 'webrtc' },
        pip: null,
      });

      const session = mgr.primarySession!;
      session.reconnectAttempts = 2; // already reached max
      mgr.scheduleWebRTCReconnect(session);

      // Primary failure triggers exitVideoMode
      expect(mgr.currentMode).toBe('widgets');
      expect(mgr.primarySession).toBeNull();

      mgr.destroy();
    });

    it('dismisses only PiP slot when PiP reconnect attempts are exhausted', () => {
      const stage = document.createElement('div');
      const primarySlot = document.createElement('div');
      const pipSlot = document.createElement('div');
      stage.appendChild(primarySlot);
      stage.appendChild(pipSlot);

      const mgr = new VideoPlayerManager({
        stageElement: stage,
        primarySlot,
        pipSlot,
        maxReconnectAttempts: 2,
      });

      mgr.handleVideoState({
        mode: 'video',
        primary: { id: 'primary', stream_url: 'http://p/mjpeg', type: 'mjpeg' },
        pip: { id: 'pip', stream_url: 'http://pip/mjpeg', type: 'mjpeg' },
      });

      const pipSession = mgr.pipSession!;
      pipSession.reconnectAttempts = 2;
      mgr.scheduleWebRTCReconnect(pipSession);

      // PiP is dismissed, primary remains active
      expect(mgr.currentMode).toBe('video');
      expect(mgr.pipSession).toBeNull();
      expect(mgr.primarySession).not.toBeNull();
      expect(pipSlot.style.display).toBe('none');

      mgr.destroy();
    });
  });

  describe('Touch Forwarding', () => {
    it('dispatches tap, move, and up actions over remoteUrl', async () => {
      const dispatched: Array<{ url: string; body: unknown }> = [];
      const mockFetch = vi.fn().mockImplementation(async (url: string, opts) => {
        dispatched.push({ url, body: JSON.parse(opts.body) });
        return { ok: true, status: 200, json: async () => ({ status: 'ok' }) };
      });

      const stage = document.createElement('div');
      const primarySlot = document.createElement('div');
      stage.appendChild(primarySlot);

      const mgr = new VideoPlayerManager({
        stageElement: stage,
        primarySlot,
        fetch: mockFetch as unknown as typeof fetch,
        remoteUrl: 'http://sidecar:8092',
      });

      mgr.enterVideoMode({
        mode: 'video',
        primary: {
          id: 'interactive_cam',
          stream_url: 'http://cam/mjpeg',
          type: 'mjpeg',
          controllable: true,
        },
        pip: null,
      });

      const img = mgr.primarySession?.element as HTMLImageElement;
      img.getBoundingClientRect = () =>
        ({ left: 0, top: 0, width: 1000, height: 1000, right: 1000, bottom: 1000 }) as DOMRect;
      Object.defineProperty(img, 'naturalWidth', { value: 1000 });
      Object.defineProperty(img, 'naturalHeight', { value: 1000 });

      // Simulate tap gesture (pointerdown then pointerup without movement)
      const downEvent = new MouseEvent('pointerdown', { clientX: 500, clientY: 500, button: 0 });
      img.dispatchEvent(downEvent);

      const upEvent = new MouseEvent('pointerup', { clientX: 500, clientY: 500, button: 0 });
      img.dispatchEvent(upEvent);

      expect(dispatched.length).toBe(2);
      expect(dispatched[0].url).toBe('http://sidecar:8092/touch');
      expect(dispatched[0].body).toEqual({ action: 'down', x: 0.5, y: 0.5 });
      expect(dispatched[1].body).toEqual({ action: 'tap', x: 0.5, y: 0.5 });

      // Simulate drag gesture
      dispatched.length = 0;
      img.dispatchEvent(new MouseEvent('pointerdown', { clientX: 100, clientY: 100, button: 0 }));

      // Move past 10px and throttle interval
      await act(async () => {
        vi.advanceTimersByTime(40);
      });
      img.dispatchEvent(new MouseEvent('pointermove', { clientX: 300, clientY: 300 }));
      img.dispatchEvent(new MouseEvent('pointerup', { clientX: 300, clientY: 300 }));

      expect(dispatched.length).toBe(3);
      expect(dispatched[0].body).toEqual({ action: 'down', x: 0.1, y: 0.1 });
      expect(dispatched[1].body).toEqual({ action: 'move', x: 0.3, y: 0.3 });
      expect(dispatched[2].body).toEqual({ action: 'up', x: 0.3, y: 0.3 });

      mgr.destroy();
    });

    it('falls back to /tap if /touch fails on tap action', async () => {
      const calls: string[] = [];
      const mockFetch = vi.fn().mockImplementation(async (url: string) => {
        calls.push(url);
        if (url.endsWith('/touch')) {
          throw new Error('404 Not Found');
        }
        return { ok: true, status: 200, json: async () => ({ status: 'ok' }) };
      });

      const mgr = new VideoPlayerManager({
        fetch: mockFetch as unknown as typeof fetch,
        remoteUrl: 'http://sidecar:8092',
      });

      await mgr.sendTouchEvent('tap', 0.5, 0.5);

      expect(calls).toEqual(['http://sidecar:8092/touch', 'http://sidecar:8092/tap']);
      mgr.destroy();
    });
  });

  describe('<VideoStage /> React Component', () => {
    it('renders stage structure, sets window.videoManager and window.videoHUD, and cleans up on unmount', () => {
      const { container, unmount } = render(<VideoStage />);

      expect(container.querySelector('#video-stage')).not.toBeNull();
      expect(container.querySelector('#video-primary-slot')).not.toBeNull();
      expect(container.querySelector('#video-pip-slot')).not.toBeNull();
      expect(container.querySelector('#video-hud-overlay')).not.toBeNull();

      expect(window.videoManager).toBeDefined();
      expect(window.videoHUD).toBeDefined();

      unmount();

      expect(window.videoManager).toBeUndefined();
    });

    it('responds to displayStore video slice updates to enter and exit video presentation mode', () => {
      render(<VideoStage />);
      const stageEl = document.getElementById('video-stage');

      expect(stageEl?.style.display).toBe('none');

      // Trigger video mode via store
      act(() => {
        useDisplayStore.setState({
          video: {
            mode: 'video',
            primary: { id: 'doorbell', stream_url: 'http://cam/mjpeg', type: 'mjpeg' },
            pip: null,
          },
        });
      });

      expect(stageEl?.style.display).toBe('flex');
      expect(stageEl?.classList.contains('active')).toBe(true);

      // Return to widgets mode
      act(() => {
        useDisplayStore.setState({
          video: {
            mode: 'widgets',
            primary: null,
            pip: null,
          },
        });
      });

      expect(stageEl?.style.display).toBe('none');
      expect(stageEl?.classList.contains('active')).toBe(false);
    });

    it('preserves VideoHUDController and VideoPlayerManager bidirectional linkage across store re-renders', () => {
      render(<VideoStage />);

      expect(window.videoManager).toBeDefined();
      expect(window.videoHUD).toBeDefined();
      expect(window.videoManager?.hud).toBe(window.videoHUD);
      expect(window.videoHUD?.videoManager).toBe(window.videoManager);

      const initialHud = window.videoHUD;

      // Trigger store-driven re-render via audio update
      act(() => {
        useDisplayStore.setState({
          audio: { volume: 50, muted: false },
        });
      });

      expect(window.videoHUD).toBe(initialHud);
      expect(window.videoManager?.hud).toBe(initialHud);
      expect(window.videoHUD?.videoManager).toBe(window.videoManager);
    });

    it('synchronizes HUD stream state on video state updates, PiP dismissal while swapped, and mode exit', () => {
      render(<VideoStage />);

      const hud = window.videoHUD!;
      expect(hud).toBeDefined();

      // Enter video mode with primary and PiP
      act(() => {
        useDisplayStore.setState({
          video: {
            mode: 'video',
            primary: { id: 'primary_stream', stream_url: 'http://p/mjpeg', type: 'mjpeg', title: 'Primary Live' },
            pip: { id: 'pip_stream', stream_url: 'http://pip/mjpeg', type: 'mjpeg', title: 'PiP Live' },
          },
        });
      });

      expect(hud.activeStream?.id).toBe('primary_stream');
      expect(hud.titleEl?.textContent).toBe('Primary Live');

      // Swap streams
      act(() => {
        window.videoManager?.swapStreams();
      });
      expect(hud.activeStream?.id).toBe('pip_stream');
      expect(hud.titleEl?.textContent).toBe('PiP Live');

      // Dismiss PiP while swapped: resets swap state and synchronizes HUD back to primary
      act(() => {
        useDisplayStore.setState({
          video: {
            mode: 'video',
            primary: { id: 'primary_stream', stream_url: 'http://p/mjpeg', type: 'mjpeg', title: 'Primary Live' },
            pip: null,
          },
        });
      });

      expect(window.videoManager?.isSwapped).toBe(false);
      expect(hud.activeStream?.id).toBe('primary_stream');
      expect(hud.titleEl?.textContent).toBe('Primary Live');

      // Update stream title while in video mode
      act(() => {
        useDisplayStore.setState({
          video: {
            mode: 'video',
            primary: { id: 'primary_stream', stream_url: 'http://p/mjpeg', type: 'mjpeg', title: 'Updated Movie Title' },
            pip: null,
          },
        });
      });

      expect(hud.activeStream?.title).toBe('Updated Movie Title');
      expect(hud.titleEl?.textContent).toBe('Updated Movie Title');

      // Exit video mode clears activeStream on HUD
      act(() => {
        useDisplayStore.setState({
          video: {
            mode: 'widgets',
            primary: null,
            pip: null,
          },
        });
      });

      expect(hud.activeStream).toBeNull();
    });

    it('unbinds slot click event listeners on destroy()', () => {
      const stageEl = document.createElement('div');
      const primarySlot = document.createElement('div');
      const pipSlot = document.createElement('div');
      document.body.appendChild(stageEl);
      document.body.appendChild(primarySlot);
      document.body.appendChild(pipSlot);

      const manager = new VideoPlayerManager({
        stageElement: stageEl,
        primarySlot,
        pipSlot,
      });

      manager.handleVideoState({
        mode: 'video',
        primary: { id: 'p1', stream_url: 'http://p/mjpeg', type: 'mjpeg' },
        pip: { id: 'pip1', stream_url: 'http://pip/mjpeg', type: 'mjpeg' },
      });

      expect(manager.isSwapped).toBe(false);

      // Clicking pipSlot swaps
      pipSlot.dispatchEvent(new MouseEvent('click', { bubbles: true }));
      expect(manager.isSwapped).toBe(true);

      // Clicking primarySlot (which is dock when swapped) swaps back
      primarySlot.dispatchEvent(new MouseEvent('click', { bubbles: true }));
      expect(manager.isSwapped).toBe(false);

      // Destroy manager - unbinds listeners
      manager.destroy();

      // Subsequent clicks do not trigger swaps
      pipSlot.dispatchEvent(new MouseEvent('click', { bubbles: true }));
      expect(manager.isSwapped).toBe(false);

      primarySlot.dispatchEvent(new MouseEvent('click', { bubbles: true }));
      expect(manager.isSwapped).toBe(false);
    });

    it('handles handleStreamFallback correctly when swapped vs not swapped', () => {
      const stageEl = document.createElement('div');
      const primarySlot = document.createElement('div');
      const pipSlot = document.createElement('div');
      document.body.appendChild(stageEl);
      document.body.appendChild(primarySlot);
      document.body.appendChild(pipSlot);

      const mockAudio = createMockAudioManager();
      const mockHud = {
        hideHUD: vi.fn(),
        syncStream: vi.fn(),
        handleVideoState: vi.fn(),
        lastVideoState: {
          mode: 'video' as const,
          primary: { id: 'p1', stream_url: 'http://p/hls', type: 'hls' as const },
          pip: { id: 'pip1', stream_url: 'http://pip/hls', type: 'hls' as const },
        },
      };
      const onFallback = vi.fn();

      const manager = new VideoPlayerManager({
        stageElement: stageEl,
        primarySlot,
        pipSlot,
        audioManager: mockAudio,
        hud: mockHud,
        onFallback,
      });

      manager.handleVideoState({
        mode: 'video',
        primary: { id: 'p1', stream_url: 'http://p/hls', type: 'hls' },
        pip: { id: 'pip1', stream_url: 'http://pip/hls', type: 'hls' },
      });

      const primarySession = manager.primarySession!;
      const pipSession = manager.pipSession!;
      expect(primarySession).toBeDefined();
      expect(pipSession).toBeDefined();

      // 1. Swap streams: pipSession is presentation primary; primarySession is in corner dock
      manager.swapStreams();
      expect(manager.isSwapped).toBe(true);

      // 2. Primary session fails while in swapped corner dock
      manager.handleStreamFallback(primarySession);

      expect(onFallback).toHaveBeenCalledWith(primarySession, 'pip_dismissed');
      // Dock is torn down and hidden, but healthy pipSession stays running full-screen!
      expect(manager.primarySession).toBeNull();
      expect(manager.pipSession).toBe(pipSession);
      expect(manager.serverPrimary).toBeNull();
      expect(primarySlot.style.display).toBe('none');
      expect(manager.currentMode).toBe('video');
      expect(mockHud.lastVideoState.primary).toBeNull();
      expect(mockHud.syncStream).toHaveBeenCalled();

      // 2b. Subsequent failure of remaining pipSession (with primarySession already null) exits video mode
      manager.handleStreamFallback(pipSession);
      expect(onFallback).toHaveBeenCalledWith(pipSession, 'widgets');
      expect(manager.currentMode).toBe('widgets');
      expect(manager.pipSession).toBeNull();

      // 3. Reset and test opposite: pipSession fails while swapped (full-screen fails)
      manager.enterVideoMode({
        mode: 'video',
        primary: { id: 'p2', stream_url: 'http://p2/hls', type: 'hls' },
        pip: { id: 'pip2', stream_url: 'http://pip2/hls', type: 'hls' },
      });
      if (!manager.isSwapped) {
        manager.swapStreams();
      }
      expect(manager.isSwapped).toBe(true);

      const newPipSession = manager.pipSession!;
      const newPrimarySession = manager.primarySession!;

      manager.handleStreamFallback(newPipSession);

      expect(onFallback).toHaveBeenCalledWith(newPipSession, 'pip_dismissed');
      // Should unswap, tear down pipSession, and promote primarySession back to full screen!
      expect(manager.isSwapped).toBe(false);
      expect(stageEl.dataset.swapped).toBe('false');
      expect(manager.pipSession).toBeNull();
      expect(manager.primarySession).toBe(newPrimarySession);
      expect(manager.serverPip).toBeNull();
      expect(pipSlot.style.display).toBe('none');
      expect(primarySlot.style.display).toBe('');
      expect(mockHud.lastVideoState.pip).toBeNull();

      // 4. Test not swapped: primarySession fails -> falls back to widgets
      manager.handleStreamFallback(newPrimarySession);
      expect(onFallback).toHaveBeenCalledWith(newPrimarySession, 'widgets');
      expect(manager.currentMode).toBe('widgets');
      expect(manager.primarySession).toBeNull();
    });

    it('clears HUD cached state and restores primarySlot display in exitVideoMode', () => {
      const stageEl = document.createElement('div');
      const primarySlot = document.createElement('div');
      const pipSlot = document.createElement('div');
      document.body.appendChild(stageEl);
      document.body.appendChild(primarySlot);
      document.body.appendChild(pipSlot);

      const mockHud = {
        hideHUD: vi.fn(),
        syncStream: vi.fn(),
        handleVideoState: vi.fn(),
        lastVideoState: {
          mode: 'video' as const,
          primary: { id: 'p1', stream_url: 'http://p/mjpeg', type: 'mjpeg' as const },
          pip: null,
        },
      };

      const manager = new VideoPlayerManager({
        stageElement: stageEl,
        primarySlot,
        pipSlot,
        hud: mockHud,
      });

      manager.enterVideoMode({
        mode: 'video',
        primary: { id: 'p1', stream_url: 'http://p/mjpeg', type: 'mjpeg' },
        pip: null,
      });

      // Simulate primarySlot hidden by prior dock fallback
      primarySlot.style.display = 'none';

      manager.exitVideoMode();

      expect(mockHud.handleVideoState).toHaveBeenCalledWith(null);
      expect(mockHud.hideHUD).toHaveBeenCalled();
      expect(mockHud.syncStream).toHaveBeenCalled();
      expect(primarySlot.style.display).toBe('');
      expect(pipSlot.style.display).toBe('none');
      expect(manager.serverPrimary).toBeNull();
      expect(manager.serverPip).toBeNull();
    });

    it('manages RTCPeerConnection listeners and nulls session.pc before closing', async () => {
      const stageEl = document.createElement('div');
      const primarySlot = document.createElement('div');
      document.body.appendChild(stageEl);
      document.body.appendChild(primarySlot);

      const mockPC = new MockRTCPeerConnection();
      const mockFetch = vi.fn().mockResolvedValue({
        ok: true,
        text: async () => 'v=0\r\no=- 54321 2 IN IP4 127.0.0.1\r\ns=Answer',
      });

      const manager = new VideoPlayerManager({
        stageElement: stageEl,
        primarySlot,
        fetch: mockFetch as unknown as typeof fetch,
        RTCPeerConnection: function () {
          return mockPC;
        } as unknown as typeof RTCPeerConnection,
      });

      manager.enterVideoMode({
        mode: 'video',
        primary: { id: 'rtc_stream', stream_url: 'http://rtc/webrtc', type: 'webrtc' },
        pip: null,
      });

      const session = manager.primarySession!;
      expect(session).toBeDefined();
      expect(session.pc).toBe(mockPC);

      // Teardown should null session.pc and close mockPC
      manager.teardownSession(session);
      expect(session.pc).toBeNull();
      expect(mockPC.closed).toBe(true);
      expect(mockPC.onconnectionstatechange).toBeNull();
      expect(mockPC.oniceconnectionstatechange).toBeNull();
    });
  });
});

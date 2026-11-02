import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { VideoSource } from '../sse/types';
import { useHlsStream } from './useHlsStream';
import { useHudAutoFade } from './useHudAutoFade';
import { formatDuration, useStreamProgress } from './useStreamProgress';
import { useTouchForwarding } from './useTouchForwarding';
import { useVolumeSlider } from './useVolumeSlider';
import { useWebRTCStream } from './useWebRTCStream';

class FakeMediaStream {
  tracks: unknown[] = [];
  addTrack(t: unknown) {
    this.tracks.push(t);
  }
}

describe('useHlsStream', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('does nothing when disabled or streamUrl is missing', () => {
    const video = document.createElement('video');
    const playSpy = vi.spyOn(video, 'play').mockResolvedValue(undefined as unknown as void);
    const videoRef = { current: video };

    const { rerender } = renderHook(
      ({ enabled, stream }) => useHlsStream({ videoRef, stream, enabled }),
      {
        initialProps: {
          enabled: false,
          stream: { id: 's1', stream_url: 'http://test/stream.m3u8', type: 'hls' } as VideoSource,
        },
      },
    );

    expect(video.src).toBe('');
    expect(playSpy).not.toHaveBeenCalled();

    rerender({
      enabled: true,
      stream: { id: 's1', stream_url: '', type: 'hls' } as VideoSource,
    });
    expect(video.src).toBe('');
    expect(playSpy).not.toHaveBeenCalled();
  });

  it('warns when canPlayType does not natively support HLS', () => {
    const video = document.createElement('video');
    video.canPlayType = vi.fn().mockReturnValue('');
    vi.spyOn(video, 'play').mockResolvedValue(undefined as unknown as void);
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const videoRef = { current: video };

    renderHook(() =>
      useHlsStream({
        videoRef,
        stream: { id: 's1', stream_url: 'http://test/stream.m3u8', type: 'hls' } as VideoSource,
      }),
    );

    expect(warnSpy).toHaveBeenCalledWith('[MirrormereVideo] HLS playback not natively supported');
    expect(video.src).toBe('http://test/stream.m3u8');
  });

  it('catches autoplay policy rejections from play()', async () => {
    const video = document.createElement('video');
    video.canPlayType = vi.fn().mockReturnValue('probably');
    const rejection = new Error('Autoplay blocked');
    vi.spyOn(video, 'play').mockRejectedValue(rejection);
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const videoRef = { current: video };

    renderHook(() =>
      useHlsStream({
        videoRef,
        stream: { id: 's1', stream_url: 'http://test/stream.m3u8', type: 'hls' } as VideoSource,
      }),
    );

    await Promise.resolve();
    expect(warnSpy).toHaveBeenCalledWith(
      '[MirrormereVideo] Playback prevented by browser autoplay policy:',
      rejection,
    );
  });

  it('cleans up on unmount by pausing, removing src, and reloading', () => {
    const video = document.createElement('video');
    video.canPlayType = vi.fn().mockReturnValue('probably');
    const pauseSpy = vi.spyOn(video, 'pause').mockImplementation(() => {});
    const loadSpy = vi.spyOn(video, 'load').mockImplementation(() => {});
    vi.spyOn(video, 'play').mockResolvedValue(undefined as unknown as void);
    const removeAttrSpy = vi.spyOn(video, 'removeAttribute');
    const videoRef = { current: video };

    const { unmount } = renderHook(() =>
      useHlsStream({
        videoRef,
        stream: { id: 's1', stream_url: 'http://test/stream.m3u8', type: 'hls' } as VideoSource,
      }),
    );

    unmount();
    expect(pauseSpy).toHaveBeenCalledOnce();
    expect(removeAttrSpy).toHaveBeenCalledWith('src');
    expect(loadSpy).toHaveBeenCalledOnce();
  });
});

describe('useWebRTCStream', () => {
  class MockRTCPeerConnection {
    connectionState: RTCPeerConnectionState = 'new';
    iceConnectionState: RTCIceConnectionState = 'new';
    iceGatheringState: RTCIceGatheringState = 'complete';
    localDescription: { sdp: string; type: RTCSdpType } | null = null;
    remoteDescription: { sdp: string; type: RTCSdpType } | null = null;
    oniceconnectionstatechange: (() => void) | null = null;
    onconnectionstatechange: (() => void) | null = null;
    ontrack: ((e: RTCTrackEvent) => void) | null = null;
    onicecandidate: ((e: RTCPeerConnectionIceEvent) => void) | null = null;
    closed = false;

    private listeners = new Map<string, Array<() => void>>();
    private receivers: RTCRtpReceiver[] = [];

    addEventListener(type: string, listener: () => void) {
      const arr = this.listeners.get(type) ?? [];
      arr.push(listener);
      this.listeners.set(type, arr);
    }

    removeEventListener(type: string, listener: () => void) {
      const arr = this.listeners.get(type) ?? [];
      this.listeners.set(type, arr.filter((cb) => cb !== listener));
    }

    dispatch(type: string) {
      for (const listener of this.listeners.get(type) ?? []) {
        listener();
      }
    }

    async createOffer(): Promise<RTCSessionDescriptionInit> {
      return { type: 'offer', sdp: 'v=0\r\no=mock 0 0 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\n' };
    }

    async setLocalDescription(desc: RTCSessionDescriptionInit): Promise<void> {
      this.localDescription = { sdp: desc.sdp ?? '', type: desc.type };
    }

    async setRemoteDescription(desc: RTCSessionDescriptionInit): Promise<void> {
      this.remoteDescription = { sdp: desc.sdp ?? '', type: desc.type };
    }

    addTransceiver(kind: string, init?: RTCRtpTransceiverInit): RTCRtpTransceiver {
      const receiver = {
        track: { kind, id: `${kind}-track` } as MediaStreamTrack,
        playoutDelayHint: 1,
      } as unknown as RTCRtpReceiver;
      this.receivers.push(receiver);
      return {
        receiver,
        direction: init?.direction ?? 'recvonly',
      } as unknown as RTCRtpTransceiver;
    }

    getReceivers(): RTCRtpReceiver[] {
      return this.receivers;
    }

    close(): void {
      this.closed = true;
      this.connectionState = 'closed';
      this.iceConnectionState = 'closed';
    }
  }

  let activePC: MockRTCPeerConnection | null = null;

  const MockPCFactory = function () {
    const pc = new MockRTCPeerConnection();
    activePC = pc;
    return pc;
  } as unknown as typeof RTCPeerConnection;

  beforeEach(() => {
    vi.useFakeTimers();
    vi.restoreAllMocks();
    activePC = null;
    HTMLMediaElement.prototype.play = vi.fn().mockResolvedValue(undefined);
    HTMLMediaElement.prototype.load = vi.fn();
    HTMLMediaElement.prototype.pause = vi.fn();
    globalThis.MediaStream = FakeMediaStream as unknown as typeof MediaStream;
  });

  afterEach(() => {
    vi.useRealTimers();
    activePC = null;
  });

  it('does nothing when disabled or streamUrl is missing', () => {
    const video = document.createElement('video');
    const videoRef = { current: video };

    renderHook(() =>
      useWebRTCStream({
        videoRef,
        stream: null,
        enabled: false,
        RTCPeerConnectionClass: MockPCFactory,
      }),
    );

    expect(activePC).toBeNull();
  });

  it('completes SDP negotiation with raw SDP answer and zeroes playoutDelayHint', async () => {
    const video = document.createElement('video');
    const videoRef = { current: video };
    const onConnected = vi.fn();

    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      text: async () => 'v=0\r\no=server-answer 0 0 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\n',
    });

    renderHook(() =>
      useWebRTCStream({
        videoRef,
        stream: { id: 'cam1', stream_url: 'http://test/whep', type: 'webrtc' } as VideoSource,
        fetchFn: fetchMock as unknown as typeof fetch,
        RTCPeerConnectionClass: MockPCFactory,
        onConnected,
      }),
    );

    expect(activePC).not.toBeNull();
    const pc = activePC!;

    // Resolve offer and SDP negotiation microtasks
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(fetchMock).toHaveBeenCalledWith('http://test/whep', expect.objectContaining({
      method: 'POST',
      headers: { 'Content-Type': 'application/sdp' },
    }));

    await act(async () => {
      await Promise.resolve();
    });

    expect(pc.remoteDescription?.sdp).toContain('server-answer');

    // Simulate ontrack with event.streams[0]
    const stream = new FakeMediaStream() as unknown as MediaStream;
    const track = { kind: 'video', id: 'vid1' } as MediaStreamTrack;
    act(() => {
      pc.ontrack?.({
        streams: [stream],
        track,
        receiver: pc.getReceivers()[0],
      } as unknown as RTCTrackEvent);
    });
    expect(video.srcObject).toBe(stream);
    expect((pc.getReceivers()[0] as { playoutDelayHint?: number }).playoutDelayHint).toBe(0);

    // Transition to connected
    act(() => {
      pc.connectionState = 'connected';
      pc.onconnectionstatechange?.();
    });
    expect(onConnected).toHaveBeenCalledOnce();
  });

  it('supports JSON { sdp } answer and ontrack without event.streams fallback', async () => {
    const video = document.createElement('video');
    const videoRef = { current: video };

    const DeferredIceMockPCFactory = function () {
      const pc = new MockRTCPeerConnection();
      pc.iceGatheringState = 'gathering';
      activePC = pc;
      return pc;
    } as unknown as typeof RTCPeerConnection;

    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      text: async () => JSON.stringify({ sdp: 'v=0\r\no=json-answer\r\n' }),
    });

    renderHook(() =>
      useWebRTCStream({
        videoRef,
        stream: { id: 'cam2', stream_url: 'http://test/whep', type: 'webrtc' } as VideoSource,
        fetchFn: fetchMock as unknown as typeof fetch,
        RTCPeerConnectionClass: DeferredIceMockPCFactory,
      }),
    );

    const pc = activePC!;
    await act(async () => {
      await Promise.resolve();
    });

    // Dispatch ICE gathering completion event directly
    pc.iceGatheringState = 'complete';
    await act(async () => {
      pc.dispatch('icegatheringstatechange');
      await Promise.resolve();
    });

    expect(fetchMock).toHaveBeenCalledWith('http://test/whep', expect.anything());

    await act(async () => {
      await Promise.resolve();
    });

    expect(pc.remoteDescription?.sdp).toBe('v=0\r\no=json-answer\r\n');

    // ontrack without event.streams uses fallback MediaStream
    const track = { kind: 'video', id: 'track-v' } as MediaStreamTrack;
    act(() => {
      pc.ontrack?.({
        streams: [],
        track,
        receiver: pc.getReceivers()[0],
      } as unknown as RTCTrackEvent);
    });
    expect(video.srcObject).toBeInstanceOf(FakeMediaStream);
  });

  it('schedules reconnects on HTTP failure and calls onFallback after maxReconnectAttempts', async () => {
    const video = document.createElement('video');
    const videoRef = { current: video };
    const onFallback = vi.fn();
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});

    const fetchMock = vi.fn().mockResolvedValue({
      ok: false,
      status: 502,
    });

    renderHook(() =>
      useWebRTCStream({
        videoRef,
        stream: { id: 'cam-err', stream_url: 'http://test/whep', type: 'webrtc' } as VideoSource,
        fetchFn: fetchMock as unknown as typeof fetch,
        RTCPeerConnectionClass: MockPCFactory,
        maxReconnectAttempts: 2,
        reconnectBaseDelay: 100,
        reconnectBackoffFactor: 2,
        onFallback,
      }),
    );

    // Initial attempt: gathering complete -> HTTP 502 -> reconnect scheduled
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
      await Promise.resolve();
    });

    // Advance by reconnect delay 1: 100 * 2^0 = 100ms
    await act(async () => {
      vi.advanceTimersByTime(100);
      await Promise.resolve();
      await Promise.resolve();
      await Promise.resolve();
    });

    // Advance by reconnect delay 2: 100 * 2^1 = 200ms
    await act(async () => {
      vi.advanceTimersByTime(200);
      await Promise.resolve();
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(onFallback).toHaveBeenCalledOnce();
    expect(warnSpy).toHaveBeenCalledWith(
      expect.stringContaining('reconnect attempts exhausted (2). Falling back.'),
    );
  });

  it('handles disconnected state with 2000ms grace timer and failed state immediately', async () => {
    const video = document.createElement('video');
    const videoRef = { current: video };
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      text: async () => 'sdp-ok',
    });

    renderHook(() =>
      useWebRTCStream({
        videoRef,
        stream: { id: 'cam-grace', stream_url: 'http://test/whep', type: 'webrtc' } as VideoSource,
        fetchFn: fetchMock as unknown as typeof fetch,
        RTCPeerConnectionClass: MockPCFactory,
        disconnectTimeout: 2000,
        reconnectBaseDelay: 100,
      }),
    );

    const pc = activePC!;
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });

    // Mark as disconnected: grace period begins
    act(() => {
      pc.connectionState = 'disconnected';
      pc.onconnectionstatechange?.();
    });

    // Advance 1000ms (not yet timed out)
    await act(async () => {
      vi.advanceTimersByTime(1000);
    });
    expect(fetchMock).toHaveBeenCalledTimes(1);

    // Recover back to connecting -> clears grace timer
    act(() => {
      pc.connectionState = 'connecting';
      pc.onconnectionstatechange?.();
    });

    // Advance beyond original 2000ms: no reconnect triggered
    await act(async () => {
      vi.advanceTimersByTime(2000);
    });
    expect(fetchMock).toHaveBeenCalledTimes(1);

    // Failed state triggers reconnect immediately
    act(() => {
      pc.connectionState = 'failed';
      pc.onconnectionstatechange?.();
    });

    // Advance base delay (100ms)
    await act(async () => {
      vi.advanceTimersByTime(100);
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });
});

describe('useTouchForwarding', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('ignores touches when disabled or missing remoteUrl', () => {
    const mediaEl = document.createElement('div');
    const customFetch = vi.fn();
    const mediaRef = { current: mediaEl };

    const { result } = renderHook(() =>
      useTouchForwarding({
        enabled: false,
        remoteUrl: '',
        mediaRef,
        customFetch: customFetch as unknown as typeof fetch,
      }),
    );

    const event = {
      button: 0,
      clientX: 50,
      clientY: 50,
      nativeEvent: new MouseEvent('pointerdown', { clientX: 50, clientY: 50 }),
      preventDefault: vi.fn(),
      stopPropagation: vi.fn(),
    } as unknown as React.PointerEvent;

    result.current.onPointerDown(event);
    expect(customFetch).not.toHaveBeenCalled();
  });

  it('ignores non-primary buttons on pointerdown', () => {
    const mediaEl = document.createElement('div');
    const customFetch = vi.fn();
    const mediaRef = { current: mediaEl };

    const { result } = renderHook(() =>
      useTouchForwarding({
        enabled: true,
        remoteUrl: 'http://sidecar/stream',
        mediaRef,
        customFetch: customFetch as unknown as typeof fetch,
      }),
    );

    const event = {
      button: 2, // right click
      clientX: 50,
      clientY: 50,
      nativeEvent: new MouseEvent('pointerdown', { clientX: 50, clientY: 50 }),
      preventDefault: vi.fn(),
      stopPropagation: vi.fn(),
    } as unknown as React.PointerEvent;

    result.current.onPointerDown(event);
    expect(customFetch).not.toHaveBeenCalled();
  });

  it('distinguishes tap (<=10px) from drag (>10px) gestures and throttles moves', async () => {
    const mediaEl = document.createElement('div');
    vi.spyOn(mediaEl, 'getBoundingClientRect').mockReturnValue({
      left: 0,
      top: 0,
      width: 100,
      height: 100,
      right: 100,
      bottom: 100,
      x: 0,
      y: 0,
      toJSON: () => {},
    });
    const customFetch = vi.fn().mockResolvedValue({ ok: true });
    const mediaRef = { current: mediaEl };

    const { result } = renderHook(() =>
      useTouchForwarding({
        enabled: true,
        remoteUrl: 'http://sidecar/stream',
        mediaRef,
        customFetch: customFetch as unknown as typeof fetch,
        throttleMs: 30,
      }),
    );

    const makePointerEvent = (x: number, y: number): React.PointerEvent => ({
      button: 0,
      clientX: x,
      clientY: y,
      nativeEvent: new MouseEvent('pointermove', { clientX: x, clientY: y }),
      preventDefault: vi.fn(),
      stopPropagation: vi.fn(),
    } as unknown as React.PointerEvent);

    // 1. Pointer Down at (50, 50)
    result.current.onPointerDown(makePointerEvent(50, 50));
    expect(customFetch).toHaveBeenCalledWith(
      'http://sidecar/stream/touch',
      expect.objectContaining({
        body: JSON.stringify({ action: 'down', x: 0.5, y: 0.5 }),
      }),
    );

    // 2. Small move <= 10px (55, 50)
    result.current.onPointerMove(makePointerEvent(55, 50));

    // 3. Pointer up without exceeding 10px -> sends tap
    result.current.onPointerUp(makePointerEvent(55, 50));
    expect(customFetch).toHaveBeenCalledWith(
      'http://sidecar/stream/touch',
      expect.objectContaining({
        body: JSON.stringify({ action: 'tap', x: 0.55, y: 0.5 }),
      }),
    );

    // 4. Next gesture: Drag exceeding 10px
    result.current.onPointerDown(makePointerEvent(10, 10));
    result.current.onPointerMove(makePointerEvent(50, 50)); // dist > 10px
    result.current.onPointerUp(makePointerEvent(50, 50));
    expect(customFetch).toHaveBeenCalledWith(
      'http://sidecar/stream/touch',
      expect.objectContaining({
        body: JSON.stringify({ action: 'up', x: 0.5, y: 0.5 }),
      }),
    );
  });

  it('sends fallback up coordinates (0.5, 0.5) on cancel and window blur', () => {
    const mediaEl = document.createElement('div');
    vi.spyOn(mediaEl, 'getBoundingClientRect').mockReturnValue({
      left: 0,
      top: 0,
      width: 100,
      height: 100,
      right: 100,
      bottom: 100,
      x: 0,
      y: 0,
      toJSON: () => {},
    });
    const customFetch = vi.fn().mockResolvedValue({ ok: true });
    const mediaRef = { current: mediaEl };

    const { result } = renderHook(() =>
      useTouchForwarding({
        enabled: true,
        remoteUrl: 'http://sidecar/stream',
        mediaRef,
        customFetch: customFetch as unknown as typeof fetch,
      }),
    );

    const makePointerEvent = (x: number, y: number): React.PointerEvent => ({
      button: 0,
      clientX: x,
      clientY: y,
      nativeEvent: new MouseEvent('pointerdown', { clientX: x, clientY: y }),
      preventDefault: vi.fn(),
      stopPropagation: vi.fn(),
    } as unknown as React.PointerEvent);

    result.current.onPointerDown(makePointerEvent(20, 20));
    result.current.onPointerCancel(makePointerEvent(20, 20));
    expect(customFetch).toHaveBeenCalledWith(
      'http://sidecar/stream/touch',
      expect.objectContaining({
        body: JSON.stringify({ action: 'up', x: 0.2, y: 0.2 }),
      }),
    );

    // Test window blur release
    result.current.onPointerDown(makePointerEvent(20, 20));
    window.dispatchEvent(new Event('blur'));
    expect(customFetch).toHaveBeenLastCalledWith(
      'http://sidecar/stream/touch',
      expect.objectContaining({
        body: JSON.stringify({ action: 'up', x: 0.5, y: 0.5 }),
      }),
    );
  });
});

describe('useVolumeSlider', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.restoreAllMocks();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('throttles slider inputs and commits on release', async () => {
    const customFetch = vi.fn().mockResolvedValue({ ok: true });
    const onUserActivity = vi.fn();

    const { result } = renderHook(() =>
      useVolumeSlider({
        volume: 50,
        muted: false,
        throttleInterval: 100,
        commitCooldown: 300,
        fetch: customFetch as unknown as typeof fetch,
        onUserActivity,
      }),
    );

    expect(result.current.volume).toBe(50);

    // First input (leading edge)
    act(() => {
      result.current.handleSliderInput({ target: { value: '60' } });
    });
    expect(result.current.volume).toBe(60);
    expect(customFetch).toHaveBeenCalledWith('api/audio/volume', expect.objectContaining({
      body: JSON.stringify({ volume: 60 }),
    }));
    expect(onUserActivity).toHaveBeenCalled();

    // Intermediate input within throttleInterval
    act(() => {
      result.current.handleSliderInput({ target: { value: '70' } });
    });
    expect(result.current.volume).toBe(70);
    expect(customFetch).toHaveBeenCalledTimes(1);

    // Advance throttle interval (trailing edge)
    act(() => {
      vi.advanceTimersByTime(100);
    });
    expect(customFetch).toHaveBeenCalledTimes(2);
    expect(customFetch).toHaveBeenLastCalledWith('api/audio/volume', expect.objectContaining({
      body: JSON.stringify({ volume: 70 }),
    }));

    // Commit slider
    act(() => {
      result.current.handleSliderCommit();
    });
  });

  it('debounces mute toggle by 300ms', () => {
    const customFetch = vi.fn().mockResolvedValue({ ok: true });

    const { result } = renderHook(() =>
      useVolumeSlider({
        volume: 50,
        muted: false,
        fetch: customFetch as unknown as typeof fetch,
      }),
    );

    act(() => {
      result.current.handleMuteToggle();
      result.current.handleMuteToggle(); // ignored due to 300ms debounce
    });

    expect(customFetch).toHaveBeenCalledTimes(1);
    expect(customFetch).toHaveBeenCalledWith('api/audio/mute', expect.objectContaining({
      body: JSON.stringify({ muted: true }),
    }));

    act(() => {
      vi.advanceTimersByTime(300);
    });

    act(() => {
      result.current.handleMuteToggle();
    });
    expect(customFetch).toHaveBeenCalledTimes(2);
  });
});

describe('useStreamProgress and formatDuration', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.restoreAllMocks();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('formats duration correctly across ranges and invalid inputs', () => {
    expect(formatDuration(-10)).toBe('0:00');
    expect(formatDuration(NaN)).toBe('0:00');
    expect(formatDuration(45)).toBe('0:45');
    expect(formatDuration(125)).toBe('2:05');
    expect(formatDuration(3665)).toBe('1:01:05');
  });

  it('updates progress via 1Hz ticker and coordinates pointer scrub capture', () => {
    const onSeek = vi.fn();
    const onUserActivity = vi.fn();

    const stream: VideoSource = {
      id: 'vod1',
      title: 'Movie',
      stream_url: 'http://test/vod',
      type: 'webrtc',
      player_state: 'playing',
      duration: 100,
      position: 10,
      position_updated_at: Date.now(),
    };

    const { result } = renderHook(() =>
      useStreamProgress({
        stream,
        isVisible: true,
        onSeek,
        onUserActivity,
      }),
    );

    expect(result.current.hasProgress).toBe(true);
    expect(result.current.totalTimeStr).toBe('1:40');

    // Advance 2s on the 1Hz ticker
    act(() => {
      vi.advanceTimersByTime(2000);
    });
    expect(result.current.currentPosition).toBe(12);

    // Track scrubbing
    const trackEl = document.createElement('div');
    trackEl.setPointerCapture = vi.fn();
    trackEl.releasePointerCapture = vi.fn();
    vi.spyOn(trackEl, 'getBoundingClientRect').mockReturnValue({
      left: 0,
      width: 200,
      top: 0,
      bottom: 20,
      right: 200,
      height: 20,
      x: 0,
      y: 0,
      toJSON: () => {},
    });

    const makePointer = (x: number): React.PointerEvent<HTMLDivElement> => ({
      clientX: x,
      pointerId: 1,
      currentTarget: trackEl,
      stopPropagation: vi.fn(),
    } as unknown as React.PointerEvent<HTMLDivElement>);

    act(() => {
      result.current.onTrackPointerDown(makePointer(100)); // 50% = 50s
    });
    expect(result.current.isScrubbing).toBe(true);
    expect(result.current.currentPosition).toBe(50);

    act(() => {
      result.current.onTrackPointerMove(makePointer(150)); // 75% = 75s
    });
    expect(result.current.currentPosition).toBe(75);

    act(() => {
      result.current.onTrackPointerUp(makePointer(150));
    });
    expect(result.current.isScrubbing).toBe(false);
    expect(onSeek).toHaveBeenCalledWith(75);

    // Cancel scrubbing
    act(() => {
      result.current.onTrackPointerDown(makePointer(40));
    });
    expect(result.current.isScrubbing).toBe(true);

    act(() => {
      result.current.onTrackPointerCancel(makePointer(40));
    });
    expect(result.current.isScrubbing).toBe(false);
  });
});

describe('useHudAutoFade', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.restoreAllMocks();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('manages auto-fade visibility lifecycle and toggleHUD', () => {
    const onPlayPauseToggle = vi.fn();
    const { result } = renderHook(() =>
      useHudAutoFade({
        autoFadeTimeout: 5000,
        isPlaybackActive: true,
        onPlayPauseToggle,
      }),
    );

    expect(result.current.isVisible).toBe(false);

    act(() => {
      result.current.showHUD();
    });
    expect(result.current.isVisible).toBe(true);

    // Fade out after 5000ms
    act(() => {
      vi.advanceTimersByTime(5000);
    });
    expect(result.current.isVisible).toBe(false);

    // toggleHUD
    act(() => {
      result.current.toggleHUD();
    });
    expect(result.current.isVisible).toBe(true);

    act(() => {
      result.current.toggleHUD();
    });
    expect(result.current.isVisible).toBe(false);
  });

  it('handles stage double-tap for play/pause and suppresses synthetic duplicate clicks', () => {
    const stageEl = document.createElement('div');
    const onPlayPauseToggle = vi.fn();

    const { result } = renderHook(() =>
      useHudAutoFade({
        stageElement: stageEl,
        autoFadeTimeout: 5000,
        isPlaybackActive: true,
        onPlayPauseToggle,
      }),
    );

    // 1. First tap at (100, 100)
    act(() => {
      stageEl.dispatchEvent(new MouseEvent('pointerdown', { clientX: 100, clientY: 100 }));
      stageEl.dispatchEvent(new MouseEvent('pointerup', { clientX: 100, clientY: 100 }));
    });
    expect(result.current.isVisible).toBe(true);

    // Synthetic click within 350ms is ignored
    act(() => {
      stageEl.dispatchEvent(new MouseEvent('click', { clientX: 100, clientY: 100 }));
    });

    // Advance 100ms and second tap at (105, 102) -> double tap!
    act(() => {
      vi.advanceTimersByTime(100);
      stageEl.dispatchEvent(new MouseEvent('pointerdown', { clientX: 105, clientY: 102 }));
      stageEl.dispatchEvent(new MouseEvent('pointerup', { clientX: 105, clientY: 102 }));
    });
    expect(onPlayPauseToggle).toHaveBeenCalledOnce();
  });
});

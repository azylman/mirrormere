import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { VideoSource } from '../sse/types';
import { useHlsStream } from './useHlsStream';
import { useHudAutoFade } from './useHudAutoFade';
import { formatDuration, useStreamProgress } from './useStreamProgress';
import { useTouchForwarding } from './useTouchForwarding';
import { useVolumeSlider } from './useVolumeSlider';
import {
  applyZeroLatencyPlayoutDelay,
  safePlay,
  tryUnref,
  useWebRTCStream,
} from './useWebRTCStream';
import { FakeRTCPeerConnection } from '../test-utils/fakeRTCPeerConnection';

/** Drains enough microtask turns for an un-guarded negotiation to reach its next SDP step. */
async function flushMicrotasks(): Promise<void> {
  for (let i = 0; i < 10; i++) await Promise.resolve();
}

class FakeMediaStream {
  tracks: unknown[] = [];
  addTrack(t: unknown) {
    this.tracks.push(t);
  }
}

describe('useHlsStream', () => {

  it('does nothing when disabled or streamUrl is missing', () => {
    const video = document.createElement('video');
    const playSpy = vi.spyOn(video, 'play').mockResolvedValue(undefined);
    const videoRef = { current: video };

    const { rerender } = renderHook<undefined, { enabled: boolean; stream: VideoSource }>(
      ({ enabled, stream }) => { useHlsStream({ videoRef, stream, enabled }); },
      {
        initialProps: {
          enabled: false,
          stream: { id: 's1', stream_url: 'http://test/stream.m3u8', type: 'hls' },
        },
      },
    );

    expect(video.src).toBe('');
    expect(playSpy).not.toHaveBeenCalled();

    rerender({
      enabled: true,
      stream: { id: 's1', stream_url: '', type: 'hls' },
    });
    expect(video.src).toBe('');
    expect(playSpy).not.toHaveBeenCalled();
  });

  it('warns when canPlayType does not natively support HLS', () => {
    const video = document.createElement('video');
    video.canPlayType = vi.fn().mockReturnValue('');
    vi.spyOn(video, 'play').mockResolvedValue(undefined);
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const videoRef = { current: video };

    renderHook(() => { useHlsStream({
        videoRef,
        stream: { id: 's1', stream_url: 'http://test/stream.m3u8', type: 'hls' },
      }); },
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

    renderHook(() => { useHlsStream({
        videoRef,
        stream: { id: 's1', stream_url: 'http://test/stream.m3u8', type: 'hls' },
      }); },
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
    vi.spyOn(video, 'play').mockResolvedValue(undefined);
    const removeAttrSpy = vi.spyOn(video, 'removeAttribute');
    const videoRef = { current: video };

    const { unmount } = renderHook(() => { useHlsStream({
        videoRef,
        stream: { id: 's1', stream_url: 'http://test/stream.m3u8', type: 'hls' },
      }); },
    );

    unmount();
    expect(pauseSpy).toHaveBeenCalledOnce();
    expect(removeAttrSpy).toHaveBeenCalledWith('src');
    expect(loadSpy).toHaveBeenCalledOnce();
  });
});

describe('useWebRTCStream', () => {
  let activePC: FakeRTCPeerConnection | null = null;

  const MockPCFactory = function () {
    const pc = new FakeRTCPeerConnection();
    activePC = pc;
    return pc;
  } as unknown as typeof RTCPeerConnection;

  beforeEach(() => {
    vi.useFakeTimers();
    activePC = null;
    globalThis.MediaStream = FakeMediaStream as unknown as typeof MediaStream;
  });

  afterEach(() => {
    activePC = null;
  });

  it('does nothing when disabled or streamUrl is missing', () => {
    const video = document.createElement('video');
    const videoRef = { current: video };

    renderHook(() => { useWebRTCStream({
        videoRef,
        stream: null,
        enabled: false,
        RTCPeerConnectionClass: MockPCFactory,
      }); },
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

    renderHook(() => { useWebRTCStream({
        videoRef,
        stream: { id: 'cam1', stream_url: 'http://test/whep', type: 'webrtc' },
        fetchFn: fetchMock,
        RTCPeerConnectionClass: MockPCFactory,
        onConnected,
      }); },
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
      const pc = new FakeRTCPeerConnection();
      pc.iceGatheringState = 'gathering';
      activePC = pc;
      return pc;
    } as unknown as typeof RTCPeerConnection;

    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      text: async () => JSON.stringify({ sdp: 'v=0\r\no=json-answer\r\n' }),
    });

    renderHook(() => { useWebRTCStream({
        videoRef,
        stream: { id: 'cam2', stream_url: 'http://test/whep', type: 'webrtc' },
        fetchFn: fetchMock,
        RTCPeerConnectionClass: DeferredIceMockPCFactory,
      }); },
    );

    const pc = activePC!;
    await act(async () => {
      await Promise.resolve();
    });

    // Dispatch ICE gathering completion event directly
    pc.iceGatheringState = 'complete';
    await act(async () => {
      pc.dispatchEvent('icegatheringstatechange');
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

    renderHook(() => { useWebRTCStream({
        videoRef,
        stream: { id: 'cam-err', stream_url: 'http://test/whep', type: 'webrtc' },
        fetchFn: fetchMock,
        RTCPeerConnectionClass: MockPCFactory,
        maxReconnectAttempts: 2,
        reconnectBaseDelay: 100,
        reconnectBackoffFactor: 2,
        onFallback,
      }); },
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

    renderHook(() => { useWebRTCStream({
        videoRef,
        stream: { id: 'cam-grace', stream_url: 'http://test/whep', type: 'webrtc' },
        fetchFn: fetchMock,
        RTCPeerConnectionClass: MockPCFactory,
        disconnectTimeout: 2000,
        reconnectBaseDelay: 100,
      }); },
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

  it('handles video.play rejection autoplay warning', async () => {
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const video = document.createElement('video');
    video.play = vi.fn().mockReturnValue(Promise.reject(new Error('Autoplay blocked')));
    const videoRef = { current: video };
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      text: async () => 'sdp-ok',
    });

    renderHook(() => { useWebRTCStream({
        videoRef,
        stream: { id: 'cam-closed', stream_url: 'http://test/whep', type: 'webrtc' },
        fetchFn: fetchMock,
        RTCPeerConnectionClass: MockPCFactory,
        reconnectBaseDelay: 50,
      }); },
    );

    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(warnSpy).toHaveBeenCalledWith(
      expect.stringContaining('Playback prevented by browser autoplay policy'),
      expect.any(Error),
    );
    warnSpy.mockRestore();
  });

  it('handles PeerConnection constructor throwing and logs warning', async () => {
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const video = document.createElement('video');
    const videoRef = { current: video };

    const FailingPCFactory = function () {
      throw new Error('WebRTC not supported on device');
    } as unknown as typeof RTCPeerConnection;

    renderHook(() => { useWebRTCStream({
        videoRef,
        stream: { id: 'cam-fail', stream_url: 'http://test/whep', type: 'webrtc' },
        RTCPeerConnectionClass: FailingPCFactory,
      }); },
    );

    expect(warnSpy).toHaveBeenCalledWith(
      '[MirrormereVideo] RTCPeerConnection initialization error:',
      expect.any(Error),
    );
  });

  it('falls back to 500ms safety timer when ICE gathering does not complete', async () => {
    const video = document.createElement('video');
    const videoRef = { current: video };

    const SlowIceMockPCFactory = function () {
      const pc = new FakeRTCPeerConnection();
      pc.iceGatheringState = 'gathering';
      activePC = pc;
      return pc;
    } as unknown as typeof RTCPeerConnection;

    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      text: async () => 'sdp-ice-timeout',
    });

    renderHook(() => { useWebRTCStream({
        videoRef,
        stream: { id: 'cam-slow-ice', stream_url: 'http://test/whep', type: 'webrtc' },
        fetchFn: fetchMock,
        RTCPeerConnectionClass: SlowIceMockPCFactory,
      }); },
    );

    expect(activePC).not.toBeNull();
    await act(async () => {
      await Promise.resolve();
    });

    // Advance 500ms for safety timer to expire
    await act(async () => {
      vi.advanceTimersByTime(500);
      await Promise.resolve();
    });

    expect(fetchMock).toHaveBeenCalledWith('http://test/whep', expect.anything());
  });

  it('aborts negotiation gracefully if unmounted during createOffer', async () => {
    const video = document.createElement('video');
    const videoRef = { current: video };

    let resolveOffer: ((desc: RTCSessionDescriptionInit) => void) | null = null;
    const SlowOfferPCFactory = function () {
      const pc = new FakeRTCPeerConnection();
      pc.createOffer = () => new Promise((res) => { resolveOffer = res; });
      activePC = pc;
      return pc;
    } as unknown as typeof RTCPeerConnection;

    const { unmount } = renderHook(() => { useWebRTCStream({
        videoRef,
        stream: { id: 'cam-unmount', stream_url: 'http://test/whep', type: 'webrtc' },
        RTCPeerConnectionClass: SlowOfferPCFactory,
      }); },
    );

    const pc = activePC!;
    const setLocalSpy = vi.spyOn(pc, 'setLocalDescription');

    // Fulfil the offer, then unmount before its await continuation runs (a closed native
    // connection never fulfils createOffer, so the offer must resolve while still open).
    await act(async () => {
      resolveOffer?.({ type: 'offer', sdp: 'late-sdp' });
      unmount();
      await flushMicrotasks();
    });

    expect(pc.closed).toBe(true);
    // Negotiation must stop at the post-createOffer cancellation guard.
    expect(setLocalSpy).not.toHaveBeenCalled();
  });

  it('aborts negotiation gracefully if unmounted during res.text()', async () => {
    const video = document.createElement('video');
    const videoRef = { current: video };

    let resolveText!: (val: string) => void;
    const textPromise = new Promise<string>((r) => {
      resolveText = r;
    });
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      text: () => textPromise,
    });

    const { unmount } = renderHook(() => { useWebRTCStream({
        videoRef,
        stream: { id: 'cam-text-unmount', stream_url: 'http://test/whep', type: 'webrtc' },
        fetchFn: fetchMock,
        RTCPeerConnectionClass: MockPCFactory,
      }); },
    );

    const pc = activePC!;
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });

    const setRemoteSpy = vi.spyOn(pc, 'setRemoteDescription');
    unmount();

    await act(async () => {
      resolveText('sdp-after-unmount');
      await flushMicrotasks();
    });

    expect(pc.closed).toBe(true);
    // Negotiation must stop at the post-res.text() cancellation guard.
    expect(setRemoteSpy).not.toHaveBeenCalled();
  });

  it('aborts negotiation gracefully if unmounted during fetch', async () => {
    const video = document.createElement('video');
    const videoRef = { current: video };

    let resolveFetch: ((res: unknown) => void) | null = null;
    const fetchMock = vi.fn().mockImplementation(() => new Promise((res) => { resolveFetch = res; }));

    const { unmount } = renderHook(() => { useWebRTCStream({
        videoRef,
        stream: { id: 'cam-fetch-unmount', stream_url: 'http://test/whep', type: 'webrtc' },
        fetchFn: fetchMock,
        RTCPeerConnectionClass: MockPCFactory,
      }); },
    );

    const pc = activePC!;
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });

    unmount();

    const text = vi.fn().mockResolvedValue('sdp-unmounted');
    await act(async () => {
      resolveFetch?.({ ok: true, text });
      await flushMicrotasks();
    });

    expect(pc.closed).toBe(true);
    // Negotiation must stop at the post-fetch cancellation guard.
    expect(text).not.toHaveBeenCalled();
  });

  it('clears disconnect and reconnect timers when transitioning to connected', async () => {
    const video = document.createElement('video');
    const videoRef = { current: video };
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      text: async () => 'sdp-ok',
    });

    renderHook(() => { useWebRTCStream({
        videoRef,
        stream: { id: 'cam-timers', stream_url: 'http://test/whep', type: 'webrtc' },
        fetchFn: fetchMock,
        RTCPeerConnectionClass: MockPCFactory,
        disconnectTimeout: 2000,
      }); },
    );

    const pc = activePC!;
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });

    // Enter disconnected to arm disconnectTimer
    act(() => {
      pc.connectionState = 'disconnected';
      pc.onconnectionstatechange?.();
    });

    // Before timer expires, enter connected
    act(() => {
      pc.connectionState = 'connected';
      pc.onconnectionstatechange?.();
    });

    // Advance 3000ms: disconnectTimer was cleared, so no reconnect triggers
    await act(async () => {
      vi.advanceTimersByTime(3000);
      await Promise.resolve();
    });

    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('clears disconnectTimer and immediately schedules reconnect on failed state', async () => {
    const video = document.createElement('video');
    const videoRef = { current: video };
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      text: async () => 'sdp-ok',
    });

    renderHook(() => { useWebRTCStream({
        videoRef,
        stream: { id: 'cam-fail-clear', stream_url: 'http://test/whep', type: 'webrtc' },
        fetchFn: fetchMock,
        RTCPeerConnectionClass: MockPCFactory,
        disconnectTimeout: 5000,
        reconnectBaseDelay: 50,
      }); },
    );

    const pc = activePC!;
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });

    // Enter disconnected to arm disconnectTimer (5000ms)
    act(() => {
      pc.connectionState = 'disconnected';
      pc.onconnectionstatechange?.();
    });

    // Now fail immediately
    act(() => {
      pc.connectionState = 'failed';
      pc.onconnectionstatechange?.();
    });

    // Advance reconnectBaseDelay (50ms) -> triggers reconnect
    await act(async () => {
      vi.advanceTimersByTime(50);
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it('clears pending reconnect and disconnect timers on unmount', async () => {
    const video = document.createElement('video');
    const videoRef = { current: video };
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      text: async () => 'sdp-ok',
    });

    const { unmount } = renderHook(() => { useWebRTCStream({
        videoRef,
        stream: { id: 'cam-unmount-timers', stream_url: 'http://test/whep', type: 'webrtc' },
        fetchFn: fetchMock,
        RTCPeerConnectionClass: MockPCFactory,
        disconnectTimeout: 2000,
      }); },
    );

    const pc = activePC!;
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });

    act(() => {
      pc.connectionState = 'disconnected';
      pc.onconnectionstatechange?.();
    });

    unmount();

    await act(async () => {
      vi.advanceTimersByTime(5000);
      await Promise.resolve();
    });

    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('handles ontrack safely when video element ref is null', async () => {
    const videoRef = { current: null };
    renderHook(() => { useWebRTCStream({
        videoRef,
        stream: { id: 'cam-null-video', stream_url: 'http://test/whep', type: 'webrtc' },
        fetchFn: vi.fn().mockResolvedValue({ ok: true, text: async () => 'sdp-ok' }),
        RTCPeerConnectionClass: MockPCFactory,
      }); },
    );

    const pc = activePC!;
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(() => {
      pc.ontrack?.({
        streams: [],
        track: { kind: 'video', id: 'v1' } as MediaStreamTrack,
        receiver: pc.getReceivers()[0],
      } as unknown as RTCTrackEvent);
    }).not.toThrow();
  });

  const nullSafeHelperTests: { name: string; fn: (input: never) => void; input: unknown }[] = [
    { name: 'tryUnref(null)', fn: tryUnref, input: null },
    { name: 'tryUnref without unref', fn: tryUnref, input: {} },
    { name: 'applyZeroLatencyPlayoutDelay(null)', fn: applyZeroLatencyPlayoutDelay, input: null },
    { name: 'applyZeroLatencyPlayoutDelay without playoutDelayHint', fn: applyZeroLatencyPlayoutDelay, input: {} },
    { name: 'safePlay(null)', fn: safePlay, input: null },
    { name: 'safePlay without play()', fn: safePlay, input: {} },
    { name: 'safePlay when play() returns a non-promise', fn: safePlay, input: { play: () => undefined } },
  ];

  for (const tc of nullSafeHelperTests) {
    it(`tolerates missing capabilities: ${tc.name}`, () => {
      expect(() => {
        (tc.fn as (input: unknown) => void)(tc.input);
      }).not.toThrow();
    });
  }

  it('tryUnref calls unref when the handle has one', () => {
    const unrefSpy = vi.fn();
    tryUnref({ unref: unrefSpy });
    expect(unrefSpy).toHaveBeenCalledOnce();
  });

  it('applyZeroLatencyPlayoutDelay zeroes an existing playoutDelayHint', () => {
    const receiver = { playoutDelayHint: 5 } as unknown as RTCRtpReceiver;
    applyZeroLatencyPlayoutDelay(receiver);
    expect((receiver as { playoutDelayHint?: number }).playoutDelayHint).toBe(0);
  });

  it('reconnects once when ICE moves from checking to disconnected to failed', async () => {
    const video = document.createElement('video');
    const videoRef = { current: video };
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      text: async () => 'sdp-ok',
    });

    renderHook(() => { useWebRTCStream({
        videoRef,
        stream: { id: 'stream-fallback', stream_url: 'http://test/whep', type: 'webrtc' },
        fetchFn: fetchMock,
        RTCPeerConnectionClass: MockPCFactory,
        reconnectBaseDelay: 50,
      }); },
    );

    const pc = activePC!;
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });

    // iceState 'checking' clears disconnect timer
    act(() => {
      pc.iceConnectionState = 'checking';
      pc.oniceconnectionstatechange?.();
    });

    // iceState 'disconnected' starts 2000ms timer
    act(() => {
      pc.iceConnectionState = 'disconnected';
      pc.oniceconnectionstatechange?.();
    });

    // While disconnectTimer is active, transition to 'failed' clears disconnect timer and schedules reconnect
    act(() => {
      pc.iceConnectionState = 'failed';
      pc.oniceconnectionstatechange?.();
    });

    await act(async () => {
      vi.advanceTimersByTime(50);
      await Promise.resolve();
    });

    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it('ignores audio and ref-less tracks, closes mid-disconnect on unmount, and warns when the constructor throws', async () => {
    const video = document.createElement('video');
    const videoRef = { current: video as HTMLVideoElement | null };
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      text: async () => 'sdp-ok',
    });

    const { unmount } = renderHook(() => { useWebRTCStream({
        videoRef: videoRef,
        stream: { id: 'stream-edge', stream_url: 'http://test/whep', type: 'webrtc' },
        fetchFn: fetchMock,
        RTCPeerConnectionClass: MockPCFactory,
        reconnectBaseDelay: 50,
      }); },
    );

    const pc = activePC!;
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });

    // ontrack with audio track
    act(() => {
      pc.ontrack?.({
        streams: [],
        track: { kind: 'audio', id: 'a-1' },
      } as unknown as RTCTrackEvent);
    });

    // ontrack when videoRef.current is null
    videoRef.current = null;
    act(() => {
      pc.ontrack?.({
        track: { kind: 'video', id: 'v-1' },
      } as unknown as RTCTrackEvent);
    });

    // iceState 'disconnected' arms disconnectTimer
    act(() => {
      pc.iceConnectionState = 'disconnected';
      pc.oniceconnectionstatechange?.();
    });

    // Unmount while disconnectTimer is active -> clears disconnectTimer
    unmount();
    expect(pc.closed).toBe(true);

    // Constructor throws error
    const ThrowingPCFactory = function () {
      throw new Error('WebRTC not supported on device');
    } as unknown as typeof RTCPeerConnection;

    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const { unmount: unmountThrowing } = renderHook(() => { useWebRTCStream({
        videoRef: videoRef,
        stream: { stream_url: 'http://test/whep', type: 'webrtc' } as VideoSource,
        fetchFn: fetchMock,
        RTCPeerConnectionClass: ThrowingPCFactory,
        reconnectBaseDelay: 50,
      }); },
    );

    expect(warnSpy).toHaveBeenCalledWith(
      expect.stringContaining('RTCPeerConnection initialization error:'),
      expect.any(Error),
    );
    unmountThrowing();
    warnSpy.mockRestore();
  });
});

describe('useTouchForwarding', () => {

  // Each row differs from a dispatching pointerdown (enabled, primary button, configured
  // remoteUrl, nonzero media rect) in exactly one guard, so removing that guard sends a request.
  const ignoredPointerDownTests = [
    { name: 'ignores touches when disabled', enabled: false, button: 0 },
    { name: 'ignores non-primary buttons on pointerdown', enabled: true, button: 2 },
  ];
  for (const tc of ignoredPointerDownTests) {
    it(tc.name, () => {
      const mediaEl = document.createElement('div');
      vi.spyOn(mediaEl, 'getBoundingClientRect').mockReturnValue(new DOMRect(0, 0, 100, 100));
      const customFetch = vi.fn().mockResolvedValue({ ok: true });
      const mediaRef = { current: mediaEl };

      const { result } = renderHook(() =>
        useTouchForwarding({
          enabled: tc.enabled,
          remoteUrl: 'http://sidecar/stream',
          mediaRef,
          customFetch: customFetch,
        }),
      );

      const event = {
        button: tc.button,
        clientX: 50,
        clientY: 50,
        nativeEvent: new MouseEvent('pointerdown', { clientX: 50, clientY: 50 }),
        preventDefault: vi.fn(),
        stopPropagation: vi.fn(),
      } as unknown as React.PointerEvent;

      result.current.onPointerDown(event);
      expect(customFetch).not.toHaveBeenCalled();
    });
  }

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
        customFetch: customFetch,
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
        customFetch: customFetch,
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
  });

  it('throttles slider inputs and commits on release', async () => {
    const customFetch = vi.fn().mockResolvedValue({ ok: true });
    const onUserActivity = vi.fn();

    const { result, unmount } = renderHook(() =>
      useVolumeSlider({
        volume: 50,
        muted: false,
        throttleInterval: 100,
        commitCooldown: 300,
        fetch: customFetch,
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

    // Commit slider while throttle timer is pending with event and currentTarget
    act(() => {
      result.current.handleSliderInput({ target: { value: '72' } });
    });
    const stopPropagation = vi.fn();
    act(() => {
      result.current.handleSliderCommit({
        stopPropagation,
        currentTarget: { value: '75' },
      } as unknown as React.SyntheticEvent);
    });
    expect(stopPropagation).toHaveBeenCalled();
    expect(result.current.volume).toBe(75);

    // Cover unmount with active throttle timer
    act(() => {
      result.current.handleSliderInput({ target: { value: '80' } });
      result.current.handleSliderInput({ target: { value: '85' } });
    });
    unmount();
  });

  it('debounces mute toggle by 300ms', () => {
    const customFetch = vi.fn().mockResolvedValue({ ok: true });

    const { result } = renderHook(() =>
      useVolumeSlider({
        volume: 50,
        muted: false,
        fetch: customFetch,
      }),
    );

    const stopMuteProp = vi.fn();
    act(() => {
      result.current.handleMuteToggle({ stopPropagation: stopMuteProp } as unknown as React.SyntheticEvent);
      result.current.handleMuteToggle(); // ignored due to 300ms debounce
    });
    expect(stopMuteProp).toHaveBeenCalled();

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

describe('video/api helpers', () => {

  it('exercises formatStreamTitle across combinations of series, annotation, and episode name', async () => {
    const { formatStreamTitle } = await import('./api');
    expect(formatStreamTitle(null)).toBe('Live Video');
    expect(formatStreamTitle({ id: 'cam1' } as VideoSource)).toBe('Stream: cam1');
    expect(formatStreamTitle({ title: 'Single Title' } as VideoSource)).toBe('Single Title');
    expect(formatStreamTitle({ series: 'Show', title: 'Ep' } as VideoSource)).toBe('Show: Ep');
    expect(formatStreamTitle({ series: 'Show', season: 2, episode: 5, title: 'Ep' } as VideoSource)).toBe('Show S2E5: Ep');
    expect(formatStreamTitle({ series: 'Show', season: 2 } as VideoSource)).toBe('Show S2');
    expect(formatStreamTitle({ episode: 5, title: 'Ep' } as VideoSource)).toBe('E5: Ep');
  });

  it('posts video and audio actions with success and catch warnings', async () => {
    const { postVideoAction, postRemoteKey, postVideoDismiss, postAudioMute, postAudioVolume, postTouchEvent } = await import('./api');
    const mockFetch = vi.fn().mockResolvedValue({ ok: true });
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});

    await postVideoAction({ id: 'v1', action: 'play' }, mockFetch);
    await postRemoteKey('http://remote', 'Enter', mockFetch);
    await postVideoDismiss({ id: 'v1' }, mockFetch);
    await postAudioMute({ muted: true }, mockFetch);
    await postAudioVolume({ volume: 80 }, mockFetch);
    await postTouchEvent('http://remote', 'tap', 10, 20, mockFetch);
    expect(mockFetch).toHaveBeenCalledTimes(6);

    // Failures log warning
    const failFetch = vi.fn().mockRejectedValue(new Error('Network error'));
    await postVideoAction({ id: 'v1', action: 'play' }, failFetch);
    await postRemoteKey('http://remote', 'Enter', failFetch);
    await postVideoDismiss({ id: 'v1' }, failFetch);
    await postAudioMute({ muted: true }, failFetch);
    await postAudioVolume({ volume: 80 }, failFetch);
    await postTouchEvent('http://remote', 'tap', 10, 20, failFetch);
    expect(warnSpy).toHaveBeenCalled();
  });
});

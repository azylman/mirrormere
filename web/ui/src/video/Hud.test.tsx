import { act, cleanup, fireEvent, render } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { VideoSource } from '../sse/types';
import { useDisplayStore } from '../store';
import { HUD_ICONS, Hud, VideoHUDController } from './Hud';
import { formatStreamTitle } from './api';
import { formatDuration } from './useStreamProgress';

describe('Hud component and declarative decomposition', () => {
  const initialStore = useDisplayStore.getState();

  beforeEach(() => {
    cleanup();
    document.body.innerHTML = '';
    useDisplayStore.setState(initialStore, true);
    delete window.videoHUD;
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
    cleanup();
    document.body.innerHTML = '';
    useDisplayStore.setState(initialStore, true);
    delete window.videoHUD;
  });

  describe('HUD_ICONS export', () => {
    it('contains all required svg icons', () => {
      expect(HUD_ICONS.play).toContain('<svg');
      expect(HUD_ICONS.pause).toContain('<svg');
      expect(HUD_ICONS.mute).toContain('<svg');
      expect(HUD_ICONS.unmute).toContain('<svg');
      expect(HUD_ICONS.prev).toContain('<svg');
      expect(HUD_ICONS.next).toContain('<svg');
    });
  });

  describe('<Hud /> DOM rendering & hierarchy', () => {
    it('renders the complete HUD overlay hierarchy matching display.html lines 107-149', () => {
      const { container } = render(
        <div id="video-stage">
          <Hud />
        </div>,
      );

      expect(container.querySelector('#video-hud-overlay')).not.toBeNull();
      expect(container.querySelector('#video-hud-nav-bar')).not.toBeNull();
      expect(container.querySelector('#video-hud-back-btn')).not.toBeNull();
      expect(container.querySelector('#video-hud-home-btn')).not.toBeNull();
      expect(container.querySelector('#video-hud-title')).not.toBeNull();
      expect(container.querySelector('#video-hud-side-rail')).not.toBeNull();
      expect(container.querySelector('#video-hud-dismiss-btn')).not.toBeNull();
      expect(container.querySelector('#video-hud-volume-slider')).not.toBeNull();
      expect(container.querySelector('#video-hud-mute-btn')).not.toBeNull();
      expect(container.querySelector('#video-hud-mute-icon')).not.toBeNull();
      expect(container.querySelector('#video-hud-play-btn')).not.toBeNull();
      expect(container.querySelector('#video-hud-play-icon')).not.toBeNull();
      expect(container.querySelector('#video-hud-bottom-bar')).not.toBeNull();
      expect(container.querySelector('#video-hud-cc-btn')).not.toBeNull();
      expect(container.querySelector('#video-hud-prev-btn')).not.toBeNull();
      expect(container.querySelector('#video-hud-rewind-btn')).not.toBeNull();
      expect(container.querySelector('#video-hud-progress-container')).not.toBeNull();
      expect(container.querySelector('#video-hud-time-current')).not.toBeNull();
      expect(container.querySelector('#video-hud-progress-track')).not.toBeNull();
      expect(container.querySelector('#video-hud-progress-fill')).not.toBeNull();
      expect(container.querySelector('#video-hud-progress-thumb')).not.toBeNull();
      expect(container.querySelector('#video-hud-time-total')).not.toBeNull();
      expect(container.querySelector('#video-hud-forward-btn')).not.toBeNull();
      expect(container.querySelector('#video-hud-next-btn')).not.toBeNull();
    });

    it('attaches controller to window.videoHUD and cleans up on unmount', () => {
      let createdCtrl: VideoHUDController | null = null;
      const { unmount } = render(
        <div id="video-stage">
          <Hud controllerRef={(ctrl) => { createdCtrl = ctrl; }} />
        </div>,
      );

      expect(createdCtrl).toBeInstanceOf(VideoHUDController);
      expect(window.videoHUD).toBe(createdCtrl);

      unmount();
      expect(window.videoHUD).toBeUndefined();
    });

    it('responds to store audio and video changes', () => {
      let ctrl: VideoHUDController | null = null;
      render(
        <div id="video-stage">
          <Hud controllerRef={(c) => { ctrl = c; }} />
        </div>,
      );
      const activeCtrl = ctrl as unknown as VideoHUDController;
      expect(activeCtrl).not.toBeNull();

      act(() => {
        useDisplayStore.setState({ audio: { volume: 45, muted: true } });
      });

      expect(activeCtrl.currentMuted).toBe(true);

      const stream: VideoSource = {
        id: 'stream-1',
        title: 'Episode 1',
        stream_url: 'rtsp://test',
        type: 'webrtc',
        controllable: true,
        player_state: 'playing',
      };

      act(() => {
        useDisplayStore.setState({
          video: {
            mode: 'video',
            primary: stream,
            pip: null,
          },
        });
      });

      expect(activeCtrl.activeStream?.id).toBe('stream-1');
      expect(document.getElementById('video-hud-title')?.textContent).toBe('Episode 1');
    });
  });

  describe('formatStreamTitle helper', () => {
    it('formats stream titles accurately with series, season, episode and fallbacks', () => {
      expect(formatStreamTitle(null)).toBe('Live Video');
      expect(formatStreamTitle({ id: 's1', stream_url: '', type: 'webrtc' })).toBe('Stream: s1');
      expect(formatStreamTitle({ id: '', stream_url: '', type: 'webrtc' })).toBe('Live Video');

      expect(
        formatStreamTitle({
          id: 's1',
          stream_url: '',
          type: 'webrtc',
          series: 'Severance',
          season: 1,
          episode: 9,
          title: 'The Grim Barbarity',
        }),
      ).toBe('Severance S1E9: The Grim Barbarity');

      expect(
        formatStreamTitle({
          id: 's1',
          stream_url: '',
          type: 'webrtc',
          series: 'Severance',
          title: 'Pilot',
        }),
      ).toBe('Severance: Pilot');

      expect(
        formatStreamTitle({
          id: 's1',
          stream_url: '',
          type: 'webrtc',
          title: 'Security Feed',
        }),
      ).toBe('Security Feed');
    });
  });

  describe('formatDuration helper', () => {
    it('formats time to M:SS and H:MM:SS accurately', () => {
      expect(formatDuration(-10)).toBe('0:00');
      expect(formatDuration(0)).toBe('0:00');
      expect(formatDuration(65)).toBe('1:05');
      expect(formatDuration(599)).toBe('9:59');
      expect(formatDuration(3665)).toBe('1:01:05');
    });
  });

  describe('Auto-fade lifecycle and visibility', () => {
    it('manages auto-fade timeout (5s default)', () => {
      let ctrl: VideoHUDController | null = null;
      const { container } = render(
        <div id="video-stage">
          <Hud controllerRef={(c) => { ctrl = c; }} />
        </div>,
      );

      const overlay = container.querySelector('#video-hud-overlay') as HTMLElement;
      expect(overlay.classList.contains('visible')).toBe(false);

      act(() => {
        ctrl?.showHUD();
      });
      expect(overlay.classList.contains('visible')).toBe(true);

      // Advance 4900ms -> still visible
      act(() => {
        vi.advanceTimersByTime(4900);
      });
      expect(overlay.classList.contains('visible')).toBe(true);

      // Advance remaining 100ms -> hidden
      act(() => {
        vi.advanceTimersByTime(100);
      });
      expect(overlay.classList.contains('visible')).toBe(false);
    });

    it('automatically shows HUD when activeStream transitions from null to present and hides when null', () => {
      render(
        <div id="video-stage">
          <Hud />
        </div>,
      );

      const overlay = document.getElementById('video-hud-overlay')!;
      expect(overlay.classList.contains('visible')).toBe(false);

      // Stream becomes present -> HUD automatically shows
      act(() => {
        useDisplayStore.setState({
          video: {
            mode: 'video',
            primary: { id: 'stream-auto-show', stream_url: '', type: 'webrtc' },
            pip: null,
          },
        });
      });

      expect(overlay.classList.contains('visible')).toBe(true);

      // Stream becomes null -> HUD automatically hides
      act(() => {
        useDisplayStore.setState({
          video: {
            mode: 'widgets',
            primary: null,
            pip: null,
          },
        });
      });

      expect(overlay.classList.contains('visible')).toBe(false);
    });
  });

  describe('Stage tap and double-tap gestures', () => {
    it('toggles HUD on stage single tap when idle vs active playback', () => {
      let stage: HTMLElement;
      render(
        <div
          id="video-stage"
          ref={(el) => {
            if (el) stage = el;
          }}
        >
          <Hud />
        </div>,
      );

      const overlay = document.getElementById('video-hud-overlay')!;

      // Tap when hidden -> shows HUD immediately
      fireEvent.pointerDown(stage!, { clientX: 100, clientY: 100 });
      fireEvent.pointerUp(stage!, { clientX: 100, clientY: 100 });
      expect(overlay.classList.contains('visible')).toBe(true);

      // Advance past double-tap window (>300ms)
      act(() => {
        vi.advanceTimersByTime(400);
      });

      // Now set non-media / idle stream: single tap hides immediately
      act(() => {
        useDisplayStore.setState({
          video: {
            mode: 'video',
            primary: { id: 's1', stream_url: '', type: 'webrtc', player_state: 'idle' },
            pip: null,
          },
        });
      });

      fireEvent.pointerDown(stage!, { clientX: 100, clientY: 100 });
      fireEvent.pointerUp(stage!, { clientX: 100, clientY: 100 });
      expect(overlay.classList.contains('visible')).toBe(false);

      // Advance past double-tap window
      act(() => {
        vi.advanceTimersByTime(400);
      });

      // Now set playing controllable stream: single tap delays hide by 250ms
      act(() => {
        useDisplayStore.setState({
          video: {
            mode: 'video',
            primary: { id: 's1', stream_url: '', type: 'webrtc', controllable: true, player_state: 'playing' },
            pip: null,
          },
        });
        window.videoHUD?.showHUD();
      });
      expect(overlay.classList.contains('visible')).toBe(true);

      act(() => {
        vi.advanceTimersByTime(400);
      });

      fireEvent.pointerDown(stage!, { clientX: 100, clientY: 100 });
      fireEvent.pointerUp(stage!, { clientX: 100, clientY: 100 });
      // Not hidden immediately: 250ms delay
      expect(overlay.classList.contains('visible')).toBe(true);

      act(() => {
        vi.advanceTimersByTime(250);
      });
      expect(overlay.classList.contains('visible')).toBe(false);
    });

    it('triggers play/pause toggle on rapid double-tap (<300ms, <30px)', () => {
      const mockFetch = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));
      let stage: HTMLElement;

      render(
        <div
          id="video-stage"
          ref={(el) => {
            if (el) stage = el;
          }}
        >
          <Hud fetch={mockFetch as unknown as typeof fetch} />
        </div>,
      );

      act(() => {
        useDisplayStore.setState({
          video: {
            mode: 'video',
            primary: { id: 's1', stream_url: '', type: 'webrtc', controllable: true, player_state: 'playing' },
            pip: null,
          },
        });
      });

      // Tap 1
      fireEvent.pointerDown(stage!, { clientX: 100, clientY: 100 });
      fireEvent.pointerUp(stage!, { clientX: 100, clientY: 100 });

      // Tap 2 within 100ms and 5px away
      act(() => {
        vi.advanceTimersByTime(100);
      });
      fireEvent.pointerDown(stage!, { clientX: 105, clientY: 102 });
      fireEvent.pointerUp(stage!, { clientX: 105, clientY: 102 });

      expect(mockFetch).toHaveBeenCalledWith(
        'api/video/action',
        expect.objectContaining({
          body: JSON.stringify({ id: 's1', action: 'toggle_playback' }),
        }),
      );
    });

    it('ignores taps inside active corner PiP dock', () => {
      let pipSlot: HTMLElement;

      render(
        <div id="video-stage">
          <div
            className="video-pip-slot"
            ref={(el) => {
              if (el) pipSlot = el;
            }}
          />
          <Hud />
        </div>
      );

      const overlay = document.getElementById('video-hud-overlay')!;
      expect(overlay.classList.contains('visible')).toBe(false);

      // Tap inside PiP dock -> does not wake HUD
      fireEvent.pointerDown(pipSlot!, { clientX: 10, clientY: 10 });
      fireEvent.pointerUp(pipSlot!, { clientX: 10, clientY: 10 });

      expect(overlay.classList.contains('visible')).toBe(false);
    });
  });

  describe('Transport controls and actions', () => {
    it('executes transport actions with 300ms in-flight lock', () => {
      const mockFetch = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));

      render(
        <div id="video-stage">
          <Hud fetch={mockFetch as unknown as typeof fetch} />
        </div>,
      );

      act(() => {
        useDisplayStore.setState({
          video: {
            mode: 'video',
            primary: { id: 's1', stream_url: '', type: 'webrtc', controllable: true, player_state: 'playing' },
            pip: null,
          },
        });
      });

      const rewindBtn = document.getElementById('video-hud-rewind-btn')!;
      const forwardBtn = document.getElementById('video-hud-forward-btn')!;

      // Click rewind twice rapidly
      fireEvent.click(rewindBtn);
      fireEvent.click(rewindBtn);

      expect(mockFetch).toHaveBeenCalledTimes(1);
      expect(mockFetch).toHaveBeenCalledWith(
        'api/video/action',
        expect.objectContaining({
          body: JSON.stringify({ id: 's1', action: 'rewind' }),
        }),
      );

      // Advance beyond 300ms lock
      act(() => {
        vi.advanceTimersByTime(350);
      });

      fireEvent.click(forwardBtn);
      expect(mockFetch).toHaveBeenCalledTimes(2);
      expect(mockFetch).toHaveBeenCalledWith(
        'api/video/action',
        expect.objectContaining({
          body: JSON.stringify({ id: 's1', action: 'fast_forward' }),
        }),
      );
    });

    it('posts to remoteUrl when preferred remote keys (back, home, etc.) are triggered', () => {
      const mockFetch = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));

      render(
        <div id="video-stage">
          <Hud remoteUrl="http://sidecar:8080" fetch={mockFetch as unknown as typeof fetch} />
        </div>,
      );

      act(() => {
        useDisplayStore.setState({
          video: {
            mode: 'video',
            primary: { id: 's1', stream_url: '', type: 'webrtc', player_state: 'playing', controllable: true },
            pip: null,
          },
        });
      });

      const backBtn = document.getElementById('video-hud-back-btn')!;
      const homeBtn = document.getElementById('video-hud-home-btn')!;

      fireEvent.click(backBtn);
      expect(mockFetch).toHaveBeenCalledWith(
        'http://sidecar:8080/key',
        expect.objectContaining({
          body: JSON.stringify({ key: 'back' }),
        }),
      );

      act(() => {
        vi.advanceTimersByTime(350);
      });

      fireEvent.click(homeBtn);
      expect(mockFetch).toHaveBeenCalledWith(
        'http://sidecar:8080/key',
        expect.objectContaining({
          body: JSON.stringify({ key: 'home' }),
        }),
      );
    });

    it('handleDismiss dismisses all streams if both primary and pip are active with distinct IDs', () => {
      const mockFetch = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));

      render(
        <div id="video-stage">
          <Hud
            videoManager={{
              serverPrimary: { id: 'primary-1', stream_url: '', type: 'webrtc' },
              serverPip: { id: 'pip-1', stream_url: '', type: 'webrtc' },
            }}
            fetch={mockFetch as unknown as typeof fetch}
          />
        </div>,
      );

      const dismissBtn = document.getElementById('video-hud-dismiss-btn')!;
      fireEvent.click(dismissBtn);

      expect(mockFetch).toHaveBeenCalledWith(
        'api/video/dismiss',
        expect.objectContaining({
          body: JSON.stringify({ id: 'all' }),
        }),
      );
    });

    it('handleDismiss dismisses single targetId when only primary is active', () => {
      const mockFetch = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));

      render(
        <div id="video-stage">
          <Hud
            videoManager={{
              serverPrimary: { id: 'primary-1', stream_url: '', type: 'webrtc' },
              serverPip: null,
            }}
            fetch={mockFetch as unknown as typeof fetch}
          />
        </div>,
      );

      const dismissBtn = document.getElementById('video-hud-dismiss-btn')!;
      fireEvent.click(dismissBtn);

      expect(mockFetch).toHaveBeenCalledWith(
        'api/video/dismiss',
        expect.objectContaining({
          body: JSON.stringify({ id: 'primary-1' }),
        }),
      );
    });
  });

  describe('Audio controls & volume slider', () => {
    it('handles mute toggle and updates UI icons', () => {
      const mockFetch = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));

      render(
        <div id="video-stage">
          <Hud fetch={mockFetch as unknown as typeof fetch} />
        </div>,
      );

      const muteBtn = document.getElementById('video-hud-mute-btn')!;
      const muteIcon = document.getElementById('video-hud-mute-icon')!;

      expect(muteBtn.classList.contains('muted')).toBe(false);

      fireEvent.click(muteBtn);
      expect(mockFetch).toHaveBeenCalledWith(
        'api/audio/mute',
        expect.objectContaining({
          body: JSON.stringify({ muted: true }),
        }),
      );

      act(() => {
        useDisplayStore.setState({ audio: { muted: true, volume: 75 } });
      });

      expect(muteBtn.classList.contains('muted')).toBe(true);
      expect(muteIcon.dataset.icon).toBe('muted');

      act(() => {
        vi.advanceTimersByTime(350);
      });

      fireEvent.click(muteBtn);
      expect(mockFetch).toHaveBeenCalledWith(
        'api/audio/mute',
        expect.objectContaining({
          body: JSON.stringify({ muted: false }),
        }),
      );
    });

    it('throttles volume slider updates (200ms) and commits on release', () => {
      const mockFetch = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));

      render(
        <div id="video-stage">
          <Hud fetch={mockFetch as unknown as typeof fetch} />
        </div>,
      );

      const volumeSlider = document.getElementById('video-hud-volume-slider') as HTMLInputElement;

      fireEvent.input(volumeSlider, { target: { value: '80' } });

      // First input immediately dispatches
      expect(mockFetch).toHaveBeenCalledWith(
        'api/audio/volume',
        expect.objectContaining({
          body: JSON.stringify({ volume: 80 }),
        }),
      );

      // Second input within 50ms is throttled
      fireEvent.input(volumeSlider, { target: { value: '85' } });
      expect(mockFetch).toHaveBeenCalledTimes(1);

      // Advance 200ms throttle interval -> flushes pending volume
      act(() => {
        vi.advanceTimersByTime(200);
      });
      expect(mockFetch).toHaveBeenCalledTimes(2);
      expect(mockFetch).toHaveBeenCalledWith(
        'api/audio/volume',
        expect.objectContaining({
          body: JSON.stringify({ volume: 85 }),
        }),
      );

      // Commit event commits final
      volumeSlider.value = '90';
      fireEvent.pointerUp(volumeSlider);
      expect(mockFetch).toHaveBeenCalledTimes(3);
      expect(mockFetch).toHaveBeenCalledWith(
        'api/audio/volume',
        expect.objectContaining({
          body: JSON.stringify({ volume: 90 }),
        }),
      );

      // Within commitCooldown (350ms), server audio state is ignored
      act(() => {
        useDisplayStore.setState({ audio: { muted: false, volume: 50 } });
      });
      expect(volumeSlider.value).toBe('90');

      act(() => {
        vi.advanceTimersByTime(400);
        useDisplayStore.setState({ audio: { muted: false, volume: 50 } });
      });
      expect(volumeSlider.value).toBe('50');
    });

    it('does not revert committed volume if no server audio update arrives during cooldown', () => {
      const mockFetch = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));

      render(
        <div id="video-stage">
          <Hud fetch={mockFetch as unknown as typeof fetch} />
        </div>,
      );

      const volumeSlider = document.getElementById('video-hud-volume-slider') as HTMLInputElement;

      fireEvent.input(volumeSlider, { target: { value: '92' } });
      fireEvent.pointerUp(volumeSlider);
      expect(volumeSlider.value).toBe('92');

      // Cooldown timer expires without any server SSE update arriving
      act(() => {
        vi.advanceTimersByTime(500);
      });

      // Volume must remain at 92, NOT revert to default 75
      expect(volumeSlider.value).toBe('92');
    });
  });

  describe('Progress bar & scrubbing', () => {
    it('handles progress ticker and scrubbing seeks', () => {
      const mockFetch = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));

      render(
        <div id="video-stage">
          <Hud fetch={mockFetch as unknown as typeof fetch} />
        </div>,
      );

      act(() => {
        useDisplayStore.setState({
          video: {
            mode: 'video',
            primary: {
              id: 's1',
              stream_url: '',
              type: 'webrtc',
              player_state: 'playing',
              controllable: true,
              duration: 100,
              position: 10,
            },
            pip: null,
          },
        });
        window.videoHUD?.showHUD();
      });

      const progressContainer = document.getElementById('video-hud-progress-container')!;
      const timeCurrent = document.getElementById('video-hud-time-current')!;
      const timeTotal = document.getElementById('video-hud-time-total')!;
      const progressTrack = document.getElementById('video-hud-progress-track')!;

      expect(progressContainer.style.display).not.toBe('none');
      expect(timeTotal.textContent).toBe('1:40');

      // Ticker runs every 1000ms: position starts at 10s -> advances to 11s, 12s
      expect(timeCurrent.textContent).toBe('0:10');
      act(() => {
        vi.advanceTimersByTime(1000);
      });
      expect(timeCurrent.textContent).toBe('0:11');
      act(() => {
        vi.advanceTimersByTime(1000);
      });
      expect(timeCurrent.textContent).toBe('0:12');

      // Scrubbing
      progressTrack.getBoundingClientRect = () => ({
        left: 0,
        top: 0,
        width: 200,
        height: 20,
        right: 200,
        bottom: 20,
        x: 0,
        y: 0,
        toJSON: () => {},
      });

      fireEvent.pointerDown(progressTrack, { clientX: 100, pointerId: 1 });
      expect(progressTrack.classList.contains('is-scrubbing')).toBe(true);

      fireEvent.pointerMove(progressTrack, { clientX: 150, pointerId: 1 });
      // 150 / 200 = 75% of 100s = 75s = 1:15
      expect(timeCurrent.textContent).toBe('1:15');

      fireEvent.pointerUp(progressTrack, { clientX: 150, pointerId: 1 });
      expect(progressTrack.classList.contains('is-scrubbing')).toBe(false);
      expect(timeCurrent.textContent).toBe('1:15');
      expect(mockFetch).toHaveBeenCalledWith(
        'api/video/action',
        expect.objectContaining({
          body: JSON.stringify({ id: 's1', action: 'seek', value: 75 }),
        }),
      );

      // Next 1Hz tick interpolates forward from seek position (1:16)
      act(() => {
        vi.advanceTimersByTime(1000);
      });
      expect(timeCurrent.textContent).toBe('1:16');
    });
  });
});

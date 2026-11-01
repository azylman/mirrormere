import { act, cleanup, fireEvent, render } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { VideoSource } from '../sse/types';
import { useDisplayStore } from '../store';
import { HUD_ICONS, Hud, VideoHUDController } from './Hud';

describe('Hud component and VideoHUDController', () => {
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

  describe('<Hud /> DOM rendering', () => {
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

  describe('VideoHUDController unit tests', () => {
    let mockFetch: ReturnType<typeof vi.fn>;
    let stage: HTMLElement;
    let overlay: HTMLElement;
    let titleEl: HTMLElement;
    let dismissBtn: HTMLElement;
    let playBtn: HTMLElement;
    let playIcon: HTMLElement;
    let muteBtn: HTMLElement;
    let muteIcon: HTMLElement;
    let volumeSlider: HTMLInputElement;
    let rewindBtn: HTMLElement;
    let forwardBtn: HTMLElement;
    let backBtn: HTMLElement;
    let homeBtn: HTMLElement;
    let ccBtn: HTMLElement;
    let prevBtn: HTMLElement;
    let nextBtn: HTMLElement;
    let progressContainer: HTMLElement;
    let progressTrack: HTMLElement;
    let progressFill: HTMLElement;
    let timeCurrent: HTMLElement;
    let timeTotal: HTMLElement;

    beforeEach(() => {
      mockFetch = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));

      stage = document.createElement('div');
      stage.id = 'video-stage';

      overlay = document.createElement('div');
      overlay.id = 'video-hud-overlay';
      stage.appendChild(overlay);

      titleEl = document.createElement('span');
      titleEl.id = 'video-hud-title';
      overlay.appendChild(titleEl);

      dismissBtn = document.createElement('button');
      dismissBtn.id = 'video-hud-dismiss-btn';
      overlay.appendChild(dismissBtn);

      playBtn = document.createElement('button');
      playBtn.id = 'video-hud-play-btn';
      playIcon = document.createElement('span');
      playIcon.id = 'video-hud-play-icon';
      playBtn.appendChild(playIcon);
      overlay.appendChild(playBtn);

      muteBtn = document.createElement('button');
      muteBtn.id = 'video-hud-mute-btn';
      muteIcon = document.createElement('span');
      muteIcon.id = 'video-hud-mute-icon';
      muteBtn.appendChild(muteIcon);
      overlay.appendChild(muteBtn);

      volumeSlider = document.createElement('input');
      volumeSlider.type = 'range';
      volumeSlider.id = 'video-hud-volume-slider';
      volumeSlider.value = '75';
      overlay.appendChild(volumeSlider);

      const navBar = document.createElement('div');
      navBar.id = 'video-hud-nav-bar';
      backBtn = document.createElement('button');
      backBtn.id = 'video-hud-back-btn';
      homeBtn = document.createElement('button');
      homeBtn.id = 'video-hud-home-btn';
      navBar.appendChild(backBtn);
      navBar.appendChild(homeBtn);
      overlay.appendChild(navBar);

      const bottomBar = document.createElement('div');
      bottomBar.id = 'video-hud-bottom-bar';
      ccBtn = document.createElement('button');
      ccBtn.id = 'video-hud-cc-btn';
      prevBtn = document.createElement('button');
      prevBtn.id = 'video-hud-prev-btn';
      rewindBtn = document.createElement('button');
      rewindBtn.id = 'video-hud-rewind-btn';
      forwardBtn = document.createElement('button');
      forwardBtn.id = 'video-hud-forward-btn';
      nextBtn = document.createElement('button');
      nextBtn.id = 'video-hud-next-btn';

      progressContainer = document.createElement('div');
      progressContainer.id = 'video-hud-progress-container';
      timeCurrent = document.createElement('span');
      timeCurrent.id = 'video-hud-time-current';
      timeTotal = document.createElement('span');
      timeTotal.id = 'video-hud-time-total';
      progressTrack = document.createElement('div');
      progressTrack.id = 'video-hud-progress-track';
      progressFill = document.createElement('div');
      progressFill.id = 'video-hud-progress-fill';
      progressTrack.appendChild(progressFill);

      progressContainer.appendChild(timeCurrent);
      progressContainer.appendChild(progressTrack);
      progressContainer.appendChild(timeTotal);

      bottomBar.appendChild(ccBtn);
      bottomBar.appendChild(prevBtn);
      bottomBar.appendChild(rewindBtn);
      bottomBar.appendChild(progressContainer);
      bottomBar.appendChild(forwardBtn);
      bottomBar.appendChild(nextBtn);
      overlay.appendChild(bottomBar);

      document.body.appendChild(stage);
    });

    afterEach(() => {
      if (stage.parentNode) {
        stage.parentNode.removeChild(stage);
      }
    });

    it('manages auto-fade timeout (5s default)', () => {
      const ctrl = new VideoHUDController({
        overlayElement: overlay,
        stageElement: stage,
        fetch: mockFetch as unknown as typeof fetch,
      });

      ctrl.showHUD();
      expect(ctrl.isVisible).toBe(true);
      expect(overlay.classList.contains('visible')).toBe(true);

      // Advance 4900ms -> still visible
      vi.advanceTimersByTime(4900);
      expect(ctrl.isVisible).toBe(true);

      // Advance remaining 100ms -> hidden
      vi.advanceTimersByTime(100);
      expect(ctrl.isVisible).toBe(false);
      expect(overlay.classList.contains('visible')).toBe(false);

      ctrl.destroy();
    });

    it('formats stream titles accurately with series, season, episode and fallbacks', () => {
      const ctrl = new VideoHUDController({ overlayElement: overlay, stageElement: stage });

      expect(ctrl.formatStreamTitle(null)).toBe('Live Video');
      expect(ctrl.formatStreamTitle({ id: 's1', stream_url: '', type: 'webrtc' })).toBe('Stream: s1');
      expect(ctrl.formatStreamTitle({ id: '', stream_url: '', type: 'webrtc' })).toBe('Live Video');

      expect(
        ctrl.formatStreamTitle({
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
        ctrl.formatStreamTitle({
          id: 's1',
          stream_url: '',
          type: 'webrtc',
          series: 'Severance',
          title: 'Pilot',
        }),
      ).toBe('Severance: Pilot');

      expect(
        ctrl.formatStreamTitle({
          id: 's1',
          stream_url: '',
          type: 'webrtc',
          title: 'Security Feed',
        }),
      ).toBe('Security Feed');

      ctrl.destroy();
    });

    it('formats time to M:SS and H:MM:SS accurately', () => {
      const ctrl = new VideoHUDController({ overlayElement: overlay, stageElement: stage });

      expect(ctrl.formatTime(-10)).toBe('0:00');
      expect(ctrl.formatTime(0)).toBe('0:00');
      expect(ctrl.formatTime(65)).toBe('1:05');
      expect(ctrl.formatTime(599)).toBe('9:59');
      expect(ctrl.formatTime(3665)).toBe('1:01:05');

      ctrl.destroy();
    });

    it('toggles HUD on stage single tap when idle vs active playback', () => {
      const ctrl = new VideoHUDController({
        overlayElement: overlay,
        stageElement: stage,
        fetch: mockFetch as unknown as typeof fetch,
      });

      // Tap when hidden -> shows HUD immediately
      fireEvent.pointerDown(stage, { clientX: 100, clientY: 100 });
      fireEvent.pointerUp(stage, { clientX: 100, clientY: 100 });
      expect(ctrl.isVisible).toBe(true);

      // Advance past double-tap window (>300ms)
      vi.advanceTimersByTime(400);

      // Now set non-media / idle stream: single tap hides immediately
      ctrl.handleVideoState({
        mode: 'video',
        primary: { id: 's1', stream_url: '', type: 'webrtc', player_state: 'idle' },
        pip: null,
      });

      fireEvent.pointerDown(stage, { clientX: 100, clientY: 100 });
      fireEvent.pointerUp(stage, { clientX: 100, clientY: 100 });
      expect(ctrl.isVisible).toBe(false);

      // Advance past double-tap window
      vi.advanceTimersByTime(400);

      // Now set playing controllable stream: single tap delays hide by 250ms
      ctrl.handleVideoState({
        mode: 'video',
        primary: { id: 's1', stream_url: '', type: 'webrtc', controllable: true, player_state: 'playing' },
        pip: null,
      });
      ctrl.showHUD();
      expect(ctrl.isVisible).toBe(true);

      vi.advanceTimersByTime(400);

      fireEvent.pointerDown(stage, { clientX: 100, clientY: 100 });
      fireEvent.pointerUp(stage, { clientX: 100, clientY: 100 });
      // Not hidden immediately: 250ms delay
      expect(ctrl.isVisible).toBe(true);

      vi.advanceTimersByTime(250);
      expect(ctrl.isVisible).toBe(false);

      ctrl.destroy();
    });

    it('triggers play/pause toggle on rapid double-tap (<300ms, <30px)', () => {
      const ctrl = new VideoHUDController({
        overlayElement: overlay,
        stageElement: stage,
        fetch: mockFetch as unknown as typeof fetch,
      });

      const spyAction = vi.spyOn(ctrl, 'handlePlayPauseToggle');

      ctrl.handleVideoState({
        mode: 'video',
        primary: { id: 's1', stream_url: '', type: 'webrtc', controllable: true, player_state: 'playing' },
        pip: null,
      });

      // Tap 1
      fireEvent.pointerDown(stage, { clientX: 100, clientY: 100 });
      fireEvent.pointerUp(stage, { clientX: 100, clientY: 100 });

      // Tap 2 within 100ms and 5px away
      vi.advanceTimersByTime(100);
      fireEvent.pointerDown(stage, { clientX: 105, clientY: 102 });
      fireEvent.pointerUp(stage, { clientX: 105, clientY: 102 });

      expect(spyAction).toHaveBeenCalledTimes(1);

      ctrl.destroy();
    });

    it('ignores taps inside active corner PiP dock', () => {
      const pipSlot = document.createElement('div');
      pipSlot.className = 'video-pip-slot';
      stage.appendChild(pipSlot);

      const ctrl = new VideoHUDController({
        overlayElement: overlay,
        stageElement: stage,
        fetch: mockFetch as unknown as typeof fetch,
      });

      expect(ctrl.isVisible).toBe(false);

      // Tap inside PiP dock -> does not wake HUD
      fireEvent.pointerDown(pipSlot, { clientX: 10, clientY: 10 });
      fireEvent.pointerUp(pipSlot, { clientX: 10, clientY: 10 });

      expect(ctrl.isVisible).toBe(false);

      ctrl.destroy();
      stage.removeChild(pipSlot);
    });

    it('executes transport actions with 300ms in-flight lock', () => {
      const ctrl = new VideoHUDController({
        overlayElement: overlay,
        stageElement: stage,
        fetch: mockFetch as unknown as typeof fetch,
      });

      ctrl.handleVideoState({
        mode: 'video',
        primary: { id: 's1', stream_url: '', type: 'webrtc', controllable: true, player_state: 'playing' },
        pip: null,
      });

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
      vi.advanceTimersByTime(350);

      fireEvent.click(forwardBtn);
      expect(mockFetch).toHaveBeenCalledTimes(2);
      expect(mockFetch).toHaveBeenCalledWith(
        'api/video/action',
        expect.objectContaining({
          body: JSON.stringify({ id: 's1', action: 'fast_forward' }),
        }),
      );

      ctrl.destroy();
    });

    it('posts to remoteUrl when preferred remote keys (back, home, etc.) are triggered', () => {
      const ctrl = new VideoHUDController({
        overlayElement: overlay,
        stageElement: stage,
        remoteUrl: 'http://sidecar:8080',
        fetch: mockFetch as unknown as typeof fetch,
      });

      ctrl.handleVideoState({
        mode: 'video',
        primary: { id: 's1', stream_url: '', type: 'webrtc', player_state: 'playing', controllable: true },
        pip: null,
      });

      fireEvent.click(backBtn);
      expect(mockFetch).toHaveBeenCalledWith(
        'http://sidecar:8080/key',
        expect.objectContaining({
          body: JSON.stringify({ key: 'back' }),
        }),
      );

      vi.advanceTimersByTime(350);
      fireEvent.click(homeBtn);
      expect(mockFetch).toHaveBeenCalledWith(
        'http://sidecar:8080/key',
        expect.objectContaining({
          body: JSON.stringify({ key: 'home' }),
        }),
      );

      ctrl.destroy();
    });

    it('handleDismiss dismisses all streams if both primary and pip are active with distinct IDs', () => {
      const ctrl = new VideoHUDController({
        overlayElement: overlay,
        stageElement: stage,
        dismissBtn,
        videoManager: {
          serverPrimary: { id: 'primary-1', stream_url: '', type: 'webrtc' },
          serverPip: { id: 'pip-1', stream_url: '', type: 'webrtc' },
        },
        fetch: mockFetch as unknown as typeof fetch,
      });

      fireEvent.click(dismissBtn);
      expect(mockFetch).toHaveBeenCalledWith(
        'api/video/dismiss',
        expect.objectContaining({
          body: JSON.stringify({ id: 'all' }),
        }),
      );

      ctrl.destroy();
    });

    it('handleDismiss dismisses single targetId when only primary is active', () => {
      const ctrl = new VideoHUDController({
        overlayElement: overlay,
        stageElement: stage,
        dismissBtn,
        videoManager: {
          serverPrimary: { id: 'primary-1', stream_url: '', type: 'webrtc' },
          serverPip: null,
        },
        fetch: mockFetch as unknown as typeof fetch,
      });

      fireEvent.click(dismissBtn);
      expect(mockFetch).toHaveBeenCalledWith(
        'api/video/dismiss',
        expect.objectContaining({
          body: JSON.stringify({ id: 'primary-1' }),
        }),
      );

      ctrl.destroy();
    });

    it('handles mute toggle and updates UI icons', () => {
      const ctrl = new VideoHUDController({
        overlayElement: overlay,
        stageElement: stage,
        muteBtn,
        muteIcon,
        fetch: mockFetch as unknown as typeof fetch,
      });

      expect(ctrl.currentMuted).toBe(false);

      fireEvent.click(muteBtn);
      expect(mockFetch).toHaveBeenCalledWith(
        'api/audio/mute',
        expect.objectContaining({
          body: JSON.stringify({ muted: true }),
        }),
      );

      ctrl.handleAudioState({ muted: true, volume: 75 });
      expect(ctrl.currentMuted).toBe(true);
      expect(muteBtn.classList.contains('muted')).toBe(true);
      expect(muteIcon.dataset.icon).toBe('muted');

      vi.advanceTimersByTime(350);

      fireEvent.click(muteBtn);
      expect(mockFetch).toHaveBeenCalledWith(
        'api/audio/mute',
        expect.objectContaining({
          body: JSON.stringify({ muted: false }),
        }),
      );

      ctrl.destroy();
    });

    it('throttles volume slider updates (200ms) and commits on release', () => {
      const ctrl = new VideoHUDController({
        overlayElement: overlay,
        stageElement: stage,
        volumeSlider,
        fetch: mockFetch as unknown as typeof fetch,
      });

      volumeSlider.value = '80';
      fireEvent.input(volumeSlider);

      // First input immediately dispatches
      expect(mockFetch).toHaveBeenCalledWith(
        'api/audio/volume',
        expect.objectContaining({
          body: JSON.stringify({ volume: 80 }),
        }),
      );

      // Second input within 50ms is throttled
      volumeSlider.value = '85';
      fireEvent.input(volumeSlider);
      expect(mockFetch).toHaveBeenCalledTimes(1);

      // Advance 200ms throttle interval -> flushes pending volume
      vi.advanceTimersByTime(200);
      expect(mockFetch).toHaveBeenCalledTimes(2);
      expect(mockFetch).toHaveBeenCalledWith(
        'api/audio/volume',
        expect.objectContaining({
          body: JSON.stringify({ volume: 85 }),
        }),
      );

      // Commit event commits final
      volumeSlider.value = '90';
      fireEvent.change(volumeSlider);
      expect(mockFetch).toHaveBeenCalledTimes(3);
      expect(mockFetch).toHaveBeenCalledWith(
        'api/audio/volume',
        expect.objectContaining({
          body: JSON.stringify({ volume: 90 }),
        }),
      );

      // Within commitCooldown (350ms), server audio state is ignored
      ctrl.handleAudioState({ volume: 50 });
      expect(volumeSlider.value).toBe('90');

      vi.advanceTimersByTime(400);
      ctrl.handleAudioState({ volume: 50 });
      expect(volumeSlider.value).toBe('50');

      ctrl.destroy();
    });

    it('handles progress ticker and scrubbing seeks', () => {
      const ctrl = new VideoHUDController({
        overlayElement: overlay,
        stageElement: stage,
        progressContainer,
        progressTrackElement: progressTrack,
        progressFillElement: progressFill,
        timeCurrentElement: timeCurrent,
        timeTotalElement: timeTotal,
        fetch: mockFetch as unknown as typeof fetch,
      });

      ctrl.handleVideoState({
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
      });

      ctrl.showHUD();
      expect(progressContainer.style.display).not.toBe('none');
      expect(timeTotal.textContent).toBe('1:40');

      // Ticker runs every 1000ms
      vi.advanceTimersByTime(1000);
      expect(timeCurrent.textContent).not.toBe('0:00');

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

      fireEvent.pointerDown(progressTrack, { clientX: 100 });
      expect(ctrl.isScrubbing).toBe(true);
      expect(progressTrack.classList.contains('is-scrubbing')).toBe(true);

      fireEvent.pointerMove(progressTrack, { clientX: 150 });
      // 150 / 200 = 75% of 100s = 75s = 1:15
      expect(timeCurrent.textContent).toBe('1:15');

      fireEvent.pointerUp(progressTrack, { clientX: 150 });
      expect(ctrl.isScrubbing).toBe(false);
      expect(mockFetch).toHaveBeenCalledWith(
        'api/video/action',
        expect.objectContaining({
          body: JSON.stringify({ id: 's1', action: 'seek', value: 75 }),
        }),
      );

      ctrl.destroy();
    });
  });
});

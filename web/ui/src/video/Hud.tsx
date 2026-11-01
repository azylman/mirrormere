import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { VideoSource, VideoStatePayload } from '../sse/types';
import type { AudioSlice } from '../store/types';
import { useDisplayStore } from '../store';
import {
  formatStreamTitle,
  postRemoteKey,
  postVideoAction,
  postVideoDismiss,
} from './api';
import {
  HudPauseIcon,
  HudPlayIcon,
} from './HudIcons';
import { HudTopBar } from './HudTopBar';
import { HudTransportBar } from './HudTransportBar';
import { useHudAutoFade } from './useHudAutoFade';
import { formatDuration, useStreamProgress } from './useStreamProgress';
import { useVolumeSlider } from './useVolumeSlider';

export { HUD_ICONS } from './HudIcons';

export interface VideoHUDManagerRef {
  isSwapped?: boolean;
  serverPrimary?: VideoSource | null;
  serverPip?: VideoSource | null;
  remoteUrl?: string;
}

export interface VideoHUDControllerOptions {
  videoManager?: VideoHUDManagerRef | null;
  remoteUrl?: string;
  stageElement?: HTMLElement | null;
  fetch?: typeof fetch;
  autoFadeTimeout?: number;
  throttleInterval?: number;
  commitCooldown?: number;
  [key: string]: unknown;
}

/**
 * Handle exposing imperative compatibility bridge methods for VideoStage and HeaderVideoButton.
 */
export interface VideoHUDHandle {
  showHUD: () => void;
  hideHUD: () => void;
  syncStream: () => void;
  handleVideoState: (data?: VideoStatePayload | null) => void;
  handleAudioState: (data?: Partial<AudioSlice> | null) => void;
  activeStream: VideoSource | null;
  videoManager?: VideoHUDControllerOptions['videoManager'] | null;
  lastVideoState?: VideoStatePayload | null;
  isVisible?: boolean;
  currentMuted?: boolean;
  destroy?: () => void;
  titleEl: HTMLElement | null;
  formatStreamTitle?: (stream?: VideoSource | null) => string;
  formatTime?: (seconds: number) => string;
  handlePlayPauseToggle?: () => Promise<void>;
  handleRewind?: () => Promise<void>;
  handleFastForward?: () => Promise<void>;
  handleBack?: () => Promise<void>;
  handleHome?: () => Promise<void>;
  handleCaptions?: () => Promise<void>;
  handlePrevious?: () => Promise<void>;
  handleNext?: () => Promise<void>;
  handleSeek?: (positionSeconds: number) => Promise<void>;
  handleDismiss?: () => void;
  handleMuteToggle?: () => void;
  isScrubbing?: boolean;
}

/**
 * Concrete compatibility class exported as both type and value for VideoStage.
 */
export class VideoHUDController implements VideoHUDHandle {
  public showHUD: () => void = () => {};
  public hideHUD: () => void = () => {};
  public syncStream: () => void = () => {};
  public handleVideoState: (data?: VideoStatePayload | null) => void = () => {};
  public handleAudioState: (data?: Partial<AudioSlice> | null) => void = () => {};
  public videoManager?: VideoHUDControllerOptions['videoManager'] | null = null;
  public lastVideoState?: VideoStatePayload | null = null;
  public isVisible?: boolean = false;
  public currentMuted?: boolean = false;
  public destroy?: () => void = () => {};
  public formatStreamTitle?: (stream?: VideoSource | null) => string = formatStreamTitle;
  public formatTime?: (seconds: number) => string = formatDuration;
  public handlePlayPauseToggle?: () => Promise<void>;
  public handleRewind?: () => Promise<void>;
  public handleFastForward?: () => Promise<void>;
  public handleBack?: () => Promise<void>;
  public handleHome?: () => Promise<void>;
  public handleCaptions?: () => Promise<void>;
  public handlePrevious?: () => Promise<void>;
  public handleNext?: () => Promise<void>;
  public handleSeek?: (positionSeconds: number) => Promise<void>;
  public handleDismiss?: () => void;
  public handleMuteToggle?: () => void;
  public isScrubbing?: boolean = false;

  private _activeStream: VideoSource | null = null;

  public get activeStream(): VideoSource | null {
    const vm = this.videoManager;
    if (vm) {
      if (vm.isSwapped) {
        return vm.serverPip || this.lastVideoState?.pip || null;
      }
      return vm.serverPrimary || this.lastVideoState?.primary || null;
    }
    return this._activeStream || this.lastVideoState?.primary || this.lastVideoState?.pip || null;
  }

  public set activeStream(stream: VideoSource | null) {
    this._activeStream = stream;
  }

  public get titleEl(): HTMLElement | null {
    return typeof document !== 'undefined' ? document.getElementById('video-hud-title') : null;
  }
}

export interface HudProps {
  controllerRef?:
    | React.MutableRefObject<VideoHUDController | null>
    | React.RefCallback<VideoHUDController | null>;
  videoManager?: VideoHUDControllerOptions['videoManager'];
  remoteUrl?: string;
  stageElement?: HTMLElement | null;
  fetch?: typeof fetch;
  autoFadeTimeout?: number;
  throttleInterval?: number;
  commitCooldown?: number;
}

/**
 * Hud React component rendering the declarative #video-hud-overlay, navigation,
 * volume rail, central play/pause transport, and scrubbable progress bar.
 */
export function Hud({
  controllerRef,
  videoManager,
  remoteUrl,
  stageElement,
  fetch: customFetch,
  autoFadeTimeout = 5000,
  throttleInterval = 200,
  commitCooldown = 350,
}: HudProps = {}) {
  const overlayRef = useRef<HTMLDivElement>(null);
  const [syncTick, setSyncTick] = useState(0);

  // Maintain stable compatibility controller instance
  const controllerInstanceRef = useRef<VideoHUDController>(new VideoHUDController());

  const videoState = useDisplayStore((s) => s.video);
  const audioState = useDisplayStore((s) => s.audio);

  const effectiveVideoManager = controllerInstanceRef.current.videoManager || videoManager;
  const isSwapped = Boolean(effectiveVideoManager?.isSwapped);

  const activeStream = useMemo(() => {
    void syncTick;
    const vm = controllerInstanceRef.current.videoManager || videoManager;
    if (vm && vm.isSwapped) {
      return vm.serverPip || videoState?.pip || null;
    }
    return vm?.serverPrimary || videoState?.primary || null;
  }, [syncTick, videoManager, videoState?.pip, videoState?.primary]);

  const resolvedRemoteUrl =
    remoteUrl ||
    effectiveVideoManager?.remoteUrl ||
    (typeof document !== 'undefined' && document.body?.dataset?.remoteUrl) ||
    (typeof window !== 'undefined' && (window as unknown as { MIRRORMERE_REMOTE_URL?: string }).MIRRORMERE_REMOTE_URL) ||
    '';

  const isMedia = Boolean(
    activeStream &&
      (activeStream.controllable === true ||
        (activeStream.priority === 'persistent' && Boolean(resolvedRemoteUrl))),
  );
  const isIdle = Boolean(activeStream && activeStream.player_state === 'idle');
  const isPlaybackActive = Boolean(isMedia && !isIdle);
  const isPaused = activeStream?.player_state === 'paused';

  const resetAutoFadeRef = useRef<() => void>(() => {});
  const isActionPendingRef = useRef(false);

  const executeAction = useCallback(
    async (actionName: string, remoteKeyName?: string, value?: unknown): Promise<void> => {
      resetAutoFadeRef.current();
      if (isActionPendingRef.current && actionName !== 'seek') {
        return;
      }
      if (!activeStream || !activeStream.id) {
        return;
      }

      isActionPendingRef.current = true;
      let timer: ReturnType<typeof setTimeout> | null = setTimeout(() => {
        timer = null;
        isActionPendingRef.current = false;
      }, 300);

      const payload = {
        id: activeStream.id,
        action: actionName,
        ...(value !== undefined ? { value } : {}),
      };

      const isRemoteKeyPreferred = ['back', 'home', 'captions', 'previous', 'next'].includes(actionName);

      try {
        if (isRemoteKeyPreferred && resolvedRemoteUrl) {
          await postRemoteKey(resolvedRemoteUrl, remoteKeyName || actionName, customFetch);
        } else if (value !== undefined || actionName === 'seek' || (activeStream.controllable && activeStream.control_url)) {
          await postVideoAction(payload, customFetch);
        } else if (resolvedRemoteUrl) {
          await postRemoteKey(resolvedRemoteUrl, remoteKeyName || actionName, customFetch);
        } else if (activeStream.controllable) {
          await postVideoAction(payload, customFetch);
        }
      } finally {
        if (!timer) {
          isActionPendingRef.current = false;
        }
      }
    },
    [activeStream, customFetch, resolvedRemoteUrl],
  );

  const handlePlayPauseToggle = useCallback(() => executeAction('toggle_playback', 'play_pause'), [executeAction]);
  const handleRewind = useCallback(() => executeAction('rewind', 'rewind'), [executeAction]);
  const handleFastForward = useCallback(() => executeAction('fast_forward', 'fast_forward'), [executeAction]);
  const handleBack = useCallback(() => executeAction('back', 'back'), [executeAction]);
  const handleHome = useCallback(() => executeAction('home', 'home'), [executeAction]);
  const handleCaptions = useCallback(() => executeAction('captions', 'cc'), [executeAction]);
  const handlePrevious = useCallback(() => executeAction('previous', 'prev'), [executeAction]);
  const handleNext = useCallback(() => executeAction('next', 'next'), [executeAction]);
  const handleSeek = useCallback((pos: number) => executeAction('seek', 'seek', pos), [executeAction]);

  const handleDismiss = useCallback((): void => {
    resetAutoFadeRef.current();
    if (isActionPendingRef.current) return;

    const primaryId = effectiveVideoManager?.serverPrimary?.id || videoState?.primary?.id;
    const pipId = effectiveVideoManager?.serverPip?.id || videoState?.pip?.id;

    if (!primaryId && !pipId && (!activeStream || !activeStream.id)) {
      return;
    }

    isActionPendingRef.current = true;
    setTimeout(() => {
      isActionPendingRef.current = false;
    }, 300);

    if (primaryId && pipId && primaryId !== pipId) {
      void postVideoDismiss({ id: 'all' }, customFetch);
    } else {
      const targetId = primaryId || pipId || activeStream?.id;
      void postVideoDismiss({ id: targetId }, customFetch);
    }
  }, [activeStream, customFetch, effectiveVideoManager?.serverPip?.id, effectiveVideoManager?.serverPrimary?.id, videoState?.pip?.id, videoState?.primary?.id]);

  const [stageEl, setStageEl] = useState<HTMLElement | null>(stageElement || null);

  useEffect(() => {
    if (stageElement) {
      setStageEl(stageElement);
    } else {
      const found =
        overlayRef.current?.closest<HTMLElement>('#video-stage') ||
        (typeof document !== 'undefined' ? document.getElementById('video-stage') : null);
      if (found) {
        setStageEl(found);
      }
    }
  }, [stageElement]);

  const resolvedStage =
    stageElement ||
    stageEl ||
    (overlayRef.current ? overlayRef.current.closest<HTMLElement>('#video-stage') : null) ||
    (typeof document !== 'undefined' ? document.getElementById('video-stage') : null);

  const { isVisible, showHUD, hideHUD, resetAutoFade } = useHudAutoFade({
    stageElement: resolvedStage,
    autoFadeTimeout,
    isPlaybackActive,
    onPlayPauseToggle: handlePlayPauseToggle,
    isSwapped,
  });

  resetAutoFadeRef.current = resetAutoFade;

  const {
    volume,
    isMuted,
    handleSliderInput,
    handleSliderCommit,
    handleMuteToggle,
  } = useVolumeSlider({
    volume: audioState.volume,
    muted: audioState.muted,
    throttleInterval,
    commitCooldown,
    fetch: customFetch,
    onUserActivity: resetAutoFade,
    audioState,
  });

  const {
    hasProgress,
    isScrubbing,
    currentTimeStr,
    totalTimeStr,
    progressPercent,
    onTrackPointerDown,
    onTrackPointerMove,
    onTrackPointerUp,
    onTrackPointerCancel,
  } = useStreamProgress({
    stream: activeStream,
    isVisible,
    onSeek: handleSeek,
    onUserActivity: resetAutoFade,
  });

  const hadStreamRef = useRef<boolean>(false);

  useEffect(() => {
    const hadStream = hadStreamRef.current;
    const hasStream = activeStream !== null;
    hadStreamRef.current = hasStream;

    if (!hadStream && hasStream) {
      showHUD();
    } else if (hadStream && !hasStream) {
      hideHUD();
    }
  }, [activeStream, hideHUD, showHUD]);

  Object.assign(controllerInstanceRef.current, {
    showHUD,
    hideHUD,
    activeStream,
    lastVideoState: videoState,
    isVisible,
    currentMuted: isMuted,
    destroy: hideHUD,
    formatStreamTitle,
    formatTime: formatDuration,
    handlePlayPauseToggle,
    handleRewind,
    handleFastForward,
    handleBack,
    handleHome,
    handleCaptions,
    handlePrevious,
    handleNext,
    handleSeek,
    handleDismiss,
    handleMuteToggle,
    isScrubbing,
  });
  controllerInstanceRef.current.syncStream = () => {
    const vm = controllerInstanceRef.current.videoManager || videoManager;
    const currentStream = vm && vm.isSwapped ? (vm.serverPip || videoState?.pip || null) : (vm?.serverPrimary || videoState?.primary || null);
    if (!currentStream) {
      hideHUD();
    } else if (!hadStreamRef.current) {
      showHUD();
    }
    setSyncTick((t) => t + 1);
  };
  controllerInstanceRef.current.handleVideoState = (data) => {
    if (data !== undefined) {
      useDisplayStore.setState({ video: data ?? { mode: 'widgets', primary: null, pip: null } });
    }
  };
  controllerInstanceRef.current.handleAudioState = (data) => {
    if (data) {
      useDisplayStore.setState((s) => ({ audio: { ...s.audio, ...data } }));
    }
  };
  if (videoManager !== undefined) {
    controllerInstanceRef.current.videoManager = videoManager;
  }

  useEffect(() => {
    const ctrl = controllerInstanceRef.current;
    if (typeof window !== 'undefined') {
      window.videoHUD = ctrl;
    }
    if (typeof controllerRef === 'function') {
      controllerRef(ctrl);
    } else if (controllerRef) {
      controllerRef.current = ctrl;
    }

    return () => {
      if (typeof window !== 'undefined' && window.videoHUD === ctrl) {
        delete window.videoHUD;
      }
      if (typeof controllerRef === 'function') {
        controllerRef(null);
      } else if (controllerRef) {
        controllerRef.current = null;
      }
    };
  }, [controllerRef]);

  const playerState = activeStream
    ? activeStream.player_state || (isIdle ? 'idle' : 'playing')
    : undefined;

  const titleText = formatStreamTitle(activeStream);

  return (
    <div
      id="video-hud-overlay"
      className={`video-hud-overlay${isVisible ? ' visible' : ''}`}
      ref={overlayRef}
      data-player-state={playerState}
    >
      <HudTopBar
        titleText={titleText}
        isMedia={isMedia}
        volume={volume}
        isMuted={isMuted}
        onBack={handleBack}
        onHome={handleHome}
        onDismiss={handleDismiss}
        onVolumeInput={handleSliderInput}
        onVolumeCommit={handleSliderCommit}
        onMuteToggle={handleMuteToggle}
      />

      <button
        id="video-hud-play-btn"
        className="video-hud-btn video-hud-play video-hud-play-center"
        aria-label={isPaused ? 'Play' : 'Pause'}
        title={isPaused ? 'Play' : 'Pause'}
        style={{
          display: isPlaybackActive ? '' : 'none',
          pointerEvents: isPlaybackActive ? undefined : 'none',
        }}
        onPointerDown={(e) => e.stopPropagation()}
        onClick={(e) => {
          e.stopPropagation();
          resetAutoFade();
          void handlePlayPauseToggle();
        }}
      >
        <span
          className="video-hud-icon"
          id="video-hud-play-icon"
          data-icon={isPaused ? 'play' : 'pause'}
        >
          {isPaused ? <HudPlayIcon /> : <HudPauseIcon />}
        </span>
      </button>

      <HudTransportBar
        isPlaybackActive={isPlaybackActive}
        hasProgress={hasProgress}
        isScrubbing={isScrubbing}
        currentTimeStr={currentTimeStr}
        totalTimeStr={totalTimeStr}
        progressPercent={progressPercent}
        onCaptions={handleCaptions}
        onPrevious={handlePrevious}
        onRewind={handleRewind}
        onFastForward={handleFastForward}
        onNext={handleNext}
        onTrackPointerDown={onTrackPointerDown}
        onTrackPointerMove={onTrackPointerMove}
        onTrackPointerUp={onTrackPointerUp}
        onTrackPointerCancel={onTrackPointerCancel}
      />
    </div>
  );
}

declare global {
  interface Window {
    videoHUD?: VideoHUDHandle;
    MirrormereHUD?: {
      VideoHUDController: unknown;
    };
  }
}

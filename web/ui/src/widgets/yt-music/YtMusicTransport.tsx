import type { ChangeEvent } from 'react';
import {
  YtMusicNextIcon,
  YtMusicPauseIcon,
  YtMusicPlayIcon,
  YtMusicPrevIcon,
  YtMusicSpeakerIcon,
  YtMusicStopIcon,
} from './YtMusicIcons';
import type { NowPlaying } from './ytMusicTypes';

export interface YtMusicTransportProps {
  np: NowPlaying;
  onShuffle?: (() => void) | undefined;
  onPrev: () => void;
  onPlayPause: () => void;
  onNext: () => void;
  onStop: () => void;
  onVolumeChange: (e: ChangeEvent<HTMLInputElement>) => void;
}

/**
 * Transport row (shuffle, prev, play/pause, next, stop) plus the speaker-badged
 * volume slider, shared by the mini player and the full Now Playing view.
 */
export function YtMusicTransport({
  np,
  onShuffle,
  onPrev,
  onPlayPause,
  onNext,
  onStop,
  onVolumeChange,
}: YtMusicTransportProps) {
  'use no memo';
  const isPlaying = np.state === 'playing';
  return (
    <>
      <div className="transport">
        <button
          type="button"
          className={`ctrl-btn${np.shuffle ? ' active' : ''}`}
          id="btn-shuffle"
          title="Shuffle"
          aria-label="Shuffle"
          aria-pressed={Boolean(np.shuffle)}
          onClick={onShuffle}
        >
          <svg
            viewBox="0 0 24 24"
            width="16"
            height="16"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
            aria-hidden="true"
          >
            <polyline points="16 3 21 3 21 8" />
            <line x1="4" y1="20" x2="21" y2="3" />
            <polyline points="21 16 21 21 16 21" />
            <line x1="15" y1="15" x2="21" y2="21" />
            <line x1="4" y1="4" x2="9" y2="9" />
          </svg>
        </button>
        <button type="button" className="ctrl-btn" id="btn-prev" title="Previous" aria-label="Previous" onClick={onPrev}>
          <YtMusicPrevIcon />
        </button>
        <button
          type="button"
          className="ctrl-btn play-main"
          id="btn-play"
          title={isPlaying ? 'Pause' : 'Play'}
          aria-label={isPlaying ? 'Pause' : 'Play'}
          onClick={onPlayPause}
        >
          {isPlaying ? <YtMusicPauseIcon /> : <YtMusicPlayIcon />}
        </button>
        <button type="button" className="ctrl-btn" id="btn-next" title="Next" aria-label="Next" onClick={onNext}>
          <YtMusicNextIcon />
        </button>
        <button type="button" className="ctrl-btn" id="btn-stop" title="Stop" aria-label="Stop" onClick={onStop}>
          <YtMusicStopIcon />
        </button>
      </div>
      <div className="vol-wrap" data-prevent-screen-swipe="">
        <span style={{ fontSize: '0.75rem', display: 'inline-flex', alignItems: 'center' }} aria-hidden="true">
          <YtMusicSpeakerIcon />
        </span>
        <input
          type="range"
          className="vol-slider"
          id="vol-slider"
          data-prevent-screen-swipe=""
          aria-label="Volume"
          min={0}
          max={100}
          value={typeof np.volume === 'number' ? np.volume : 35}
          style={{ touchAction: 'pan-x' }}
          onChange={onVolumeChange}
        />
      </div>
    </>
  );
}

import { HudPlayIcon } from '../../video/HudIcons';
import { getVideoStreamURL } from './photoCarouselHelpers';

export interface PhotoForegroundProps {
  currentUrl: string;
  currentSecondaryUrl?: string;
  nextUrl: string;
  nextSecondaryUrl?: string;
  isTransitioning: boolean;
  currentRawUrl?: string;
  currentIsVideo?: boolean;
  currentCompanionRawUrl?: string;
  currentCompanionIsVideo?: boolean;
  playingSlot: 'primary' | 'secondary' | null;
  streamSuffixIndex: number;
  onPlaySlot: (slot: 'primary' | 'secondary') => void;
  onStopVideo: () => void;
  onVideoEnded: () => void;
  onVideoError: () => void;
}

export function PhotoForeground({
  currentUrl, currentSecondaryUrl, nextUrl, nextSecondaryUrl, isTransitioning,
  currentRawUrl, currentIsVideo, currentCompanionRawUrl, currentCompanionIsVideo,
  playingSlot, streamSuffixIndex,
  onPlaySlot, onStopVideo, onVideoEnded, onVideoError,
}: PhotoForegroundProps) {
  return (
    <div className="photo-foreground-container">
      {currentSecondaryUrl ? (
        <div
          className="photo-layer photo-current photo-foreground-diptych dither"
          style={{
            opacity: isTransitioning ? 0 : 1,
            transition: isTransitioning ? 'opacity 0.85s ease-in-out' : 'none',
          }}
        >
          <div className="photo-diptych-slot">
            {playingSlot === 'primary' ? (
              <video
                className="photo-diptych-item photo-video-player dither"
                src={getVideoStreamURL(currentRawUrl, streamSuffixIndex)}
                poster={currentUrl}
                autoPlay
                playsInline
                onClick={(e) => { e.stopPropagation(); onStopVideo(); }}
                onEnded={onVideoEnded}
                onError={onVideoError}
              />
            ) : (
              <img className="photo-diptych-item photo-current dither" src={currentUrl} alt="Photo" />
            )}
            {!isTransitioning && currentIsVideo && playingSlot !== 'primary' && (
              <button
                type="button"
                className="photo-video-play-btn"
                aria-label="Play video"
                onClick={(e) => { e.stopPropagation(); onPlaySlot('primary'); }}
              >
                <HudPlayIcon />
              </button>
            )}
          </div>
          <div className="photo-diptych-slot">
            {playingSlot === 'secondary' ? (
              <video
                className="photo-diptych-item photo-video-player dither"
                src={getVideoStreamURL(currentCompanionRawUrl, streamSuffixIndex)}
                poster={currentSecondaryUrl}
                autoPlay
                playsInline
                onClick={(e) => { e.stopPropagation(); onStopVideo(); }}
                onEnded={onVideoEnded}
                onError={onVideoError}
              />
            ) : (
              <img className="photo-diptych-item photo-current-secondary dither" src={currentSecondaryUrl} alt="Photo" />
            )}
            {!isTransitioning && currentCompanionIsVideo && playingSlot !== 'secondary' && (
              <button
                type="button"
                className="photo-video-play-btn"
                aria-label="Play video"
                onClick={(e) => { e.stopPropagation(); onPlaySlot('secondary'); }}
              >
                <HudPlayIcon />
              </button>
            )}
          </div>
        </div>
      ) : (
        <>
          {playingSlot === 'primary' ? (
            <video
              className="photo-layer photo-current photo-video-player dither"
              src={getVideoStreamURL(currentRawUrl, streamSuffixIndex)}
              poster={currentUrl}
              autoPlay
              playsInline
              onClick={(e) => { e.stopPropagation(); onStopVideo(); }}
              onEnded={onVideoEnded}
              onError={onVideoError}
            />
          ) : (
            <img
              className="photo-layer photo-current dither"
              src={currentUrl}
              alt="Photo"
              style={{
                opacity: isTransitioning ? 0 : 1,
                transition: isTransitioning ? 'opacity 0.85s ease-in-out' : 'none',
              }}
            />
          )}
          {!isTransitioning && currentIsVideo && playingSlot !== 'primary' && (
            <button
              type="button"
              className="photo-video-play-btn"
              aria-label="Play video"
              onClick={(e) => { e.stopPropagation(); onPlaySlot('primary'); }}
            >
              <HudPlayIcon />
            </button>
          )}
        </>
      )}
      {nextSecondaryUrl ? (
        <div
          className="photo-layer photo-next photo-foreground-diptych dither"
          style={{
            opacity: isTransitioning ? 1 : 0,
            transition: isTransitioning ? 'opacity 0.85s ease-in-out' : 'none',
          }}
        >
          <div className="photo-diptych-slot">
            <img className="photo-diptych-item photo-next dither" src={isTransitioning ? nextUrl : undefined} alt="Photo" />
          </div>
          <div className="photo-diptych-slot">
            <img className="photo-diptych-item photo-next-secondary dither" src={isTransitioning ? nextSecondaryUrl : undefined} alt="Photo" />
          </div>
        </div>
      ) : (
        <img
          className="photo-layer photo-next dither"
          src={isTransitioning ? nextUrl : undefined}
          alt="Photo"
          style={{
            opacity: isTransitioning ? 1 : 0,
            transition: isTransitioning ? 'opacity 0.85s ease-in-out' : 'none',
          }}
        />
      )}
    </div>
  );
}

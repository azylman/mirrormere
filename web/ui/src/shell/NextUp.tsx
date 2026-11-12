import { useEffect, useState } from 'react';
import type { KeyboardEvent as ReactKeyboardEvent, MouseEvent as ReactMouseEvent } from 'react';
import { useDocumentVisible } from '../lifecycle/visibility';
import { useDisplayStore } from '../store';
import { deepEqual } from '../store/equal';
import {
  YtMusicNextIcon,
  YtMusicPauseIcon,
  YtMusicPlayIcon,
} from '../widgets/yt-music/YtMusicIcons';
import {
  DEFAULT_ART,
  isYtMusicLiveIslandActive,
  postMusicApi,
  setSharedYtMusicState,
  useSharedYtMusicState,
} from '../widgets/yt-music/ytMusicTypes';
import { resolveActiveTimezone } from './Clock';
import {
  formatRelativeCountdown,
  parseEventDates,
  resolveNextUpEvent,
  selectCalendarEvents,
} from './nextUpHelpers';
import type { RawCalendarEvent } from './nextUpHelpers';

export type { RawCalendarEvent };
export {
  formatRelativeCountdown,
  parseEventDates,
  resolveNextUpEvent,
  selectCalendarEvents,
};

let cachedCalendarWidgets: unknown = null;
let cachedCalendarEvents: RawCalendarEvent[] = [];

/**
 * Hook subscribing to calendar events in display store widgets.
 * Stabilizes the returned array reference via deepEqual so unrelated widget updates do not trigger re-renders.
 */
export function useCalendarEvents(): RawCalendarEvent[] {
  return useDisplayStore((s) => {
    if (s.widgets === cachedCalendarWidgets) {
      return cachedCalendarEvents;
    }
    const nextEvents = selectCalendarEvents(s.widgets);
    if (!deepEqual(cachedCalendarEvents, nextEvents)) {
      cachedCalendarEvents = nextEvents;
    }
    cachedCalendarWidgets = s.widgets;
    return cachedCalendarEvents;
  });
}

export function resolveYtMusicScreenIndex(): number | null {
  const state = useDisplayStore.getState();
  for (const screen of state.layout) {
    if (
      screen.widgets.some(
        (w) => w.type === 'yt-music' || state.widgets[w.widget_id]?.meta?.type === 'yt-music',
      )
    ) {
      return screen.index;
    }
  }
  return null;
}

export interface NextUpProps {
  now?: Date;
  timezone?: string;
  events?: readonly RawCalendarEvent[];
  onPillClick?: () => void;
}

export function NextUp(props: NextUpProps = {}) {
  const { now: propNow, timezone: propTimezone, events: propEvents, onPillClick } = props;
  const header = useDisplayStore((s) => s.header);
  const storeEvents = useCalendarEvents();
  const visible = useDocumentVisible();
  const ytmState = useSharedYtMusicState();
  const isMediaActive = isYtMusicLiveIslandActive(ytmState.now_playing);

  const [currentInstant, setCurrentInstant] = useState(() => new Date());

  useEffect(() => {
    if (!visible || propNow !== undefined) return;

    let timerId: ReturnType<typeof setTimeout> | null = null;
    let cancelled = false;

    const scheduleNext = () => {
      if (cancelled) return;
      const nowMs = Date.now();
      const delay = Math.max(1000, 15000 - (nowMs % 15000));
      timerId = setTimeout(() => {
        if (cancelled) return;
        setCurrentInstant(new Date());
        scheduleNext();
      }, delay);
    };

    scheduleNext();

    return () => {
      cancelled = true;
      if (timerId !== null) clearTimeout(timerId);
    };
  }, [visible, propNow]);

  const activeTz = resolveActiveTimezone(propTimezone, header);
  const events = propEvents ?? storeEvents;
  const now = propNow ?? currentInstant;

  const resolved = resolveNextUpEvent(events, now, activeTz);

  const handleMediaPillClick = () => {
    if (onPillClick) {
      onPillClick();
      return;
    }
    const targetScreen = resolveYtMusicScreenIndex();
    const store = useDisplayStore.getState();
    const alreadyOnTarget = targetScreen === null || store.screen.currentScreen === targetScreen;
    if (targetScreen !== null && !alreadyOnTarget) {
      const hasCarouselViewport =
        typeof document !== 'undefined' && Boolean(document.querySelector('.carousel-viewport'));
      if (
        hasCarouselViewport &&
        typeof window !== 'undefined' &&
        window.location?.origin &&
        window.location.origin !== 'null'
      ) {
        window.postMessage(
          { type: 'hearth-screen-select', screen_index: targetScreen },
          window.location.origin,
        );
      } else {
        store.setCurrentScreen(targetScreen);
        fetch('api/screen/select', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ screen_index: targetScreen }),
        }).catch(() => {});
      }
      return;
    }
    if (typeof document !== 'undefined') {
      const target = document.querySelector('#grid-canvas .widget-yt-music');
      if (target && typeof target.scrollIntoView === 'function') {
        target.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
      }
    }
  };

  const handleMediaControl = async (action: 'play_pause' | 'next', e: ReactMouseEvent<HTMLButtonElement>) => {
    e.stopPropagation();
    const { apiBase, active_speaker, active_account, now_playing } = ytmState;
    if (action === 'play_pause' && now_playing) {
      const nextState = now_playing.state === 'playing' ? 'paused' : 'playing';
      setSharedYtMusicState((prev) => ({
        ...prev,
        now_playing: { ...prev.now_playing, state: nextState },
      }));
    }
    const next = await postMusicApi(`${apiBase}/music/control`, {
      action,
      speaker: active_speaker,
      ...(active_account ? { account_id: active_account } : {}),
    });
    if (next) {
      setSharedYtMusicState((prev) => ({
        ...prev,
        now_playing: next.now_playing ?? prev.now_playing,
        active_speaker: next.active_speaker || prev.active_speaker,
        active_account: next.active_account ?? prev.active_account,
      }));
    }
  };

  const handlePillClick = () => {
    if (isMediaActive) {
      handleMediaPillClick();
      return;
    }
    if (onPillClick) {
      onPillClick();
      return;
    }
    if (typeof document !== 'undefined') {
      const target = document.querySelector(
        '.widget-calendar-feed, .widget-calendar-grid, .calendar-event-item, .cf-day'
      );
      if (target && typeof target.scrollIntoView === 'function') {
        target.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
      }
    }
  };

  const handleKeyDown = (e: ReactKeyboardEvent<HTMLDivElement>) => {
    if (e.target !== e.currentTarget) return;
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      handlePillClick();
    }
  };

  if (isMediaActive) {
    const np = ytmState.now_playing!;
    const isPlaying = np.state === 'playing';
    const trackTitle = np.title || 'YouTube Music';
    const trackArtist = np.artist || 'Ready';
    const mediaTitle = `${trackTitle}${np.artist ? ` — ${np.artist}` : ''}`;

    return (
      <div className="header-next-event" id="header-next-event">
        <div
          className={`pill-badge next-event-pill live-island-pill${isPlaying ? ' is-playing' : ' is-paused'}`}
          id="next-event-pill"
          data-live-island="yt-music"
          role="button"
          tabIndex={0}
          title={mediaTitle}
          onClick={handlePillClick}
          onKeyDown={handleKeyDown}
        >
          <img
            className="live-island-art"
            id="live-island-art"
            src={np.thumb || DEFAULT_ART}
            alt=""
          />
          <div className="live-island-meta" id="live-island-meta">
            <span className="live-island-title" id="live-island-title">
              {trackTitle}
            </span>
            <span className="live-island-artist" id="live-island-artist">
              {trackArtist}
            </span>
          </div>
          <div className="live-island-controls" id="live-island-controls">
            <button
              type="button"
              className="live-island-btn play-pause"
              id="live-island-play-pause"
              aria-label={isPlaying ? 'Pause' : 'Play'}
              title={isPlaying ? 'Pause' : 'Play'}
              onClick={(e) => void handleMediaControl('play_pause', e)}
            >
              {isPlaying ? <YtMusicPauseIcon /> : <YtMusicPlayIcon />}
            </button>
            <button
              type="button"
              className="live-island-btn next"
              id="live-island-next"
              aria-label="Next"
              title="Next"
              onClick={(e) => void handleMediaControl('next', e)}
            >
              <YtMusicNextIcon />
            </button>
          </div>
        </div>
      </div>
    );
  }

  const pillClass = resolved?.isNow
    ? 'pill-badge next-event-pill status-now'
    : 'pill-badge next-event-pill';
  const pillTitle = resolved
    ? `${resolved.label}: ${resolved.title} (${resolved.timeStr})`
    : 'Next upcoming event';

  return (
    <div
      className="header-next-event"
      id="header-next-event"
      style={{ display: resolved ? '' : 'none' }}
    >
      <div
        className={pillClass}
        id="next-event-pill"
        role="button"
        tabIndex={0}
        title={pillTitle}
        onClick={handlePillClick}
        onKeyDown={handleKeyDown}
      >
        <span className="next-event-label" id="next-event-label">
          {resolved?.label ?? 'NEXT'}
        </span>
        <span className="next-event-time" id="next-event-time">
          {resolved?.timeStr ?? '--:--'}
        </span>
        <span
          className="next-event-title"
          id="next-event-title"
          title={resolved?.title ?? undefined}
        >
          {resolved?.title ?? '--'}
        </span>
        <span className="next-event-countdown" id="next-event-countdown">
          {resolved?.countdown ?? ''}
        </span>
      </div>
    </div>
  );
}

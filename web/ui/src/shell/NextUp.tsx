import { useEffect, useState } from 'react';
import type { KeyboardEvent as ReactKeyboardEvent, MouseEvent as ReactMouseEvent, ReactNode } from 'react';
import { useDocumentVisible } from '../lifecycle/visibility';
import { useDisplayStore } from '../store';
import { deepEqual } from '../store/equal';
import { applyOptimisticUpdate, dispatchWidgetAction } from '../widgets/home-assistant/homeAssistantTypes';
import type { EntityState } from '../widgets/home-assistant/homeAssistantTypes';
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
import type { NowPlaying, SharedYtMusicState } from '../widgets/yt-music/ytMusicTypes';
import { resolveActiveTimezone } from './Clock';
import {
  formatRelativeCountdown,
  formatTimerCountdown,
  getSharedPlexSession,
  isPlexLiveIslandActive,
  parseEventDates,
  resetSharedPlexSession,
  resolveNextUpEvent,
  resolveScreenIndexForWidgetType,
  selectCalendarEvents,
  selectHomeAssistantLiveActivities,
  setSharedPlexSession,
  useSharedPlexSession,
} from './nextUpHelpers';
import type {
  HaLiveIslandActivity,
  PlexNowPlayingSession,
  RawCalendarEvent,
} from './nextUpHelpers';

export type { HaLiveIslandActivity, PlexNowPlayingSession, RawCalendarEvent };
export {
  formatRelativeCountdown, formatTimerCountdown, getSharedPlexSession, isPlexLiveIslandActive,
  parseEventDates, resetSharedPlexSession, resolveNextUpEvent, resolveScreenIndexForWidgetType,
  selectCalendarEvents, selectHomeAssistantLiveActivities, setSharedPlexSession,
};

let cachedCalendarWidgets: unknown = null;
let cachedCalendarEvents: RawCalendarEvent[] = [];

export function useCalendarEvents(): RawCalendarEvent[] {
  return useDisplayStore((s) => {
    if (s.widgets === cachedCalendarWidgets) return cachedCalendarEvents;
    const nextEvents = selectCalendarEvents(s.widgets);
    if (!deepEqual(cachedCalendarEvents, nextEvents)) cachedCalendarEvents = nextEvents;
    cachedCalendarWidgets = s.widgets;
    return cachedCalendarEvents;
  });
}

let cachedHaWidgets: unknown = null;
let cachedHaNowSlot = -1;
let cachedHaActivities: HaLiveIslandActivity[] = [];

function useHaLiveActivities(now: Date): HaLiveIslandActivity[] {
  const nowSlot = Math.floor(now.getTime() / 15000);
  return useDisplayStore((s) => {
    if (s.widgets === cachedHaWidgets && nowSlot === cachedHaNowSlot) return cachedHaActivities;
    const nextActivities = selectHomeAssistantLiveActivities(s.widgets, now);
    if (!deepEqual(cachedHaActivities, nextActivities)) cachedHaActivities = nextActivities;
    cachedHaWidgets = s.widgets;
    cachedHaNowSlot = nowSlot;
    return cachedHaActivities;
  });
}

export function resolveYtMusicScreenIndex(): number | null {
  return resolveScreenIndexForWidgetType('yt-music');
}

export interface NextUpProps {
  now?: Date;
  timezone?: string;
  events?: readonly RawCalendarEvent[];
  onPillClick?: () => void;
}

type ActiveIslandItem =
  | { kind: 'ha-alert' | 'ha-timer'; id: string; ha: HaLiveIslandActivity }
  | { kind: 'yt-music'; id: 'yt-music' }
  | { kind: 'plex'; id: string; plex: PlexNowPlayingSession };

type LiveIslandKind = ActiveIslandItem['kind'];

function navigateToScreenOrWidget(widgetType: string, selector: string, onPillClick?: () => void): void {
  if (onPillClick) {
    onPillClick();
    return;
  }
  const targetScreen = resolveScreenIndexForWidgetType(widgetType);
  const store = useDisplayStore.getState();
  const alreadyOnTarget = targetScreen === null || store.screen.currentScreen === targetScreen;
  if (targetScreen !== null && !alreadyOnTarget) {
    const hasCarouselViewport = typeof document !== 'undefined' && Boolean(document.querySelector('.carousel-viewport'));
    if (hasCarouselViewport && typeof window !== 'undefined' && window.location?.origin && window.location.origin !== 'null') {
      window.postMessage({ type: 'hearth-screen-select', screen_index: targetScreen }, window.location.origin);
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
    const target = document.querySelector(selector);
    if (target && typeof target.scrollIntoView === 'function') {
      target.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
    }
  }
}

async function dispatchYtMusicControl(action: 'play_pause' | 'next', ytmState: SharedYtMusicState): Promise<void> {
  const { apiBase, active_speaker, active_account, now_playing } = ytmState;
  if (action === 'play_pause' && now_playing) {
    const nextState = now_playing.state === 'playing' ? 'paused' : 'playing';
    setSharedYtMusicState((prev) => ({ ...prev, now_playing: { ...prev.now_playing, state: nextState } }));
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
}

async function executeHaQuickAction(ha: HaLiveIslandActivity): Promise<void> {
  const store = useDisplayStore.getState();
  const entry = store.widgets[ha.widgetId];
  if (entry && entry.data && typeof entry.data === 'object') {
    const dataObj = entry.data as Record<string, unknown>;
    const entities = dataObj.entities as Record<string, EntityState | undefined> | undefined;
    const curr = entities?.[ha.entityId];
    if (curr) {
      const nextEntity =
        ha.actionPayload.domain === 'timer'
          ? { ...curr, state: 'idle' }
          : applyOptimisticUpdate(curr, ha.actionPayload.domain, ha.actionPayload.service);
      useDisplayStore.setState((s) => ({
        widgets: {
          ...s.widgets,
          [ha.widgetId]: { ...entry, data: { ...dataObj, entities: { ...entities, [ha.entityId]: nextEntity } } },
        },
      }));
    }
  }
  try {
    await dispatchWidgetAction(ha.widgetId, ha.actionPayload);
  } catch {
    // ignore network failure in header quick action
  }
}

function renderYtMusicIsland(
  np: NowPlaying,
  ytmState: SharedYtMusicState,
  onClick: () => void,
  onKeyDown: (e: ReactKeyboardEvent<HTMLDivElement>) => void,
  cycleButton: ReactNode,
) {
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
        onClick={onClick}
        onKeyDown={onKeyDown}
      >
        <img className="live-island-art" id="live-island-art" src={np.thumb || DEFAULT_ART} alt="" />
        <div className="live-island-meta" id="live-island-meta">
          <span className="live-island-title" id="live-island-title">{trackTitle}</span>
          <span className="live-island-artist" id="live-island-artist">{trackArtist}</span>
        </div>
        <div className="live-island-controls" id="live-island-controls">
          <button
            type="button"
            className="live-island-btn play-pause"
            id="live-island-play-pause"
            aria-label={isPlaying ? 'Pause' : 'Play'}
            title={isPlaying ? 'Pause' : 'Play'}
            onClick={(e) => {
              e.stopPropagation();
              void dispatchYtMusicControl('play_pause', ytmState);
            }}
          >
            {isPlaying ? <YtMusicPauseIcon /> : <YtMusicPlayIcon />}
          </button>
          <button
            type="button"
            className="live-island-btn next"
            id="live-island-next"
            aria-label="Next"
            title="Next"
            onClick={(e) => {
              e.stopPropagation();
              void dispatchYtMusicControl('next', ytmState);
            }}
          >
            <YtMusicNextIcon />
          </button>
          {cycleButton}
        </div>
      </div>
    </div>
  );
}

function renderHaIsland(
  ha: HaLiveIslandActivity,
  onClick: () => void,
  onKeyDown: (e: ReactKeyboardEvent<HTMLDivElement>) => void,
  cycleButton: ReactNode,
) {
  return (
    <div className="header-next-event" id="header-next-event">
      <div
        className="pill-badge next-event-pill live-island-pill is-playing"
        id="next-event-pill"
        data-live-island={ha.kind}
        role="button"
        tabIndex={0}
        title={`${ha.title} — ${ha.subtitle}`}
        onClick={onClick}
        onKeyDown={onKeyDown}
      >
        <span className="live-island-icon-badge" id="live-island-icon" aria-hidden="true">{ha.icon}</span>
        <div className="live-island-meta" id="live-island-meta">
          <span className="live-island-title" id="live-island-title">{ha.title}</span>
          <span className="live-island-artist" id="live-island-artist">{ha.subtitle}</span>
        </div>
        <div className="live-island-controls" id="live-island-controls">
          <button
            type="button"
            className="live-island-action-pill"
            id="live-island-action"
            aria-label={`${ha.actionLabel} ${ha.title}`}
            title={`${ha.actionLabel} ${ha.title}`}
            onClick={(e) => {
              e.stopPropagation();
              void executeHaQuickAction(ha);
            }}
          >
            {ha.actionLabel}
          </button>
          {cycleButton}
        </div>
      </div>
    </div>
  );
}

function renderPlexIsland(
  plex: PlexNowPlayingSession,
  onClick: () => void,
  onKeyDown: (e: ReactKeyboardEvent<HTMLDivElement>) => void,
  cycleButton: ReactNode,
) {
  const subtitle = plex.subtitle || 'Now Playing on TV';
  return (
    <div className="header-next-event" id="header-next-event">
      <div
        className="pill-badge next-event-pill live-island-pill is-playing"
        id="next-event-pill"
        data-live-island="plex"
        role="button"
        tabIndex={0}
        title={`${plex.title} — ${subtitle}`}
        onClick={onClick}
        onKeyDown={onKeyDown}
      >
        <img className="live-island-art" id="live-island-art" src={plex.thumb || DEFAULT_ART} alt="" />
        <div className="live-island-meta" id="live-island-meta">
          <span className="live-island-title" id="live-island-title">{plex.title}</span>
          <span className="live-island-artist" id="live-island-artist">{subtitle}</span>
        </div>
        {cycleButton ? <div className="live-island-controls" id="live-island-controls">{cycleButton}</div> : null}
      </div>
    </div>
  );
}

export function NextUp(props: NextUpProps = {}) {
  const { now: propNow, timezone: propTimezone, events: propEvents, onPillClick } = props;
  const header = useDisplayStore((s) => s.header);
  const storeEvents = useCalendarEvents();
  const visible = useDocumentVisible();
  const ytmState = useSharedYtMusicState();
  const plexSession = useSharedPlexSession();

  const [currentInstant, setCurrentInstant] = useState(() => new Date());
  const [cycleIndex, setCycleIndex] = useState(0);

  useEffect(() => {
    if (!visible || propNow !== undefined) return;
    let timerId: ReturnType<typeof setTimeout> | null = null;
    let cancelled = false;
    const scheduleNext = () => {
      if (cancelled) return;
      const delay = Math.max(1000, 15000 - (Date.now() % 15000));
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
  const haActivities = useHaLiveActivities(now);

  const activities: ActiveIslandItem[] = haActivities.map((ha) => ({ kind: ha.kind, id: ha.id, ha }));
  if (isYtMusicLiveIslandActive(ytmState.now_playing)) {
    activities.push({ kind: 'yt-music', id: 'yt-music' });
  }
  if (isPlexLiveIslandActive(plexSession) && plexSession) {
    activities.push({ kind: 'plex', id: `plex-${plexSession.id}`, plex: plexSession });
  }

  const effectiveIdx = activities.length > 0 ? cycleIndex % activities.length : 0;
  const activeItem = activities.length > 0 ? activities[effectiveIdx] : null;
  const resolved = resolveNextUpEvent(events, now, activeTz);

  const handlePillClick = () => {
    if (activeItem) {
      const targetMap: Record<LiveIslandKind, [string, string]> = {
        'yt-music': ['yt-music', '#grid-canvas .widget-yt-music'],
        'ha-alert': ['home-assistant', '#grid-canvas .widget-home-assistant'],
        'ha-timer': ['home-assistant', '#grid-canvas .widget-home-assistant'],
        plex: ['plex-recent', '#grid-canvas .widget-plex-recent'],
      };
      const [wType, selector] = targetMap[activeItem.kind];
      navigateToScreenOrWidget(wType, selector, onPillClick);
      return;
    }
    if (onPillClick) {
      onPillClick();
      return;
    }
    if (typeof document !== 'undefined') {
      const target = document.querySelector(
        '.widget-calendar-feed, .widget-calendar-grid, .calendar-event-item, .cf-day',
      );
      if (target && typeof target.scrollIntoView === 'function') {
        target.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
      }
    }
  };

  const handleKeyDown = (e: ReactKeyboardEvent<HTMLDivElement>) => {
    if (e.target === e.currentTarget && (e.key === 'Enter' || e.key === ' ')) {
      e.preventDefault();
      handlePillClick();
    }
  };

  const cycleButton =
    activities.length > 1 ? (
      <button
        type="button"
        className="live-island-btn cycle"
        id="live-island-cycle"
        aria-label={`Switch live activity (${effectiveIdx + 1} of ${activities.length})`}
        title={`Switch live activity (${effectiveIdx + 1}/${activities.length})`}
        onClick={(e: ReactMouseEvent<HTMLButtonElement>) => {
          e.stopPropagation();
          setCycleIndex((prev) => (prev + 1) % Math.max(1, activities.length));
        }}
      >
        {effectiveIdx + 1}/{activities.length}
      </button>
    ) : null;

  if (activeItem?.kind === 'yt-music') {
    return renderYtMusicIsland(ytmState.now_playing!, ytmState, handlePillClick, handleKeyDown, cycleButton);
  }
  if (activeItem?.kind === 'ha-alert' || activeItem?.kind === 'ha-timer') {
    return renderHaIsland(activeItem.ha, handlePillClick, handleKeyDown, cycleButton);
  }
  if (activeItem?.kind === 'plex') {
    return renderPlexIsland(activeItem.plex, handlePillClick, handleKeyDown, cycleButton);
  }

  const pillClass = resolved?.isNow ? 'pill-badge next-event-pill status-now' : 'pill-badge next-event-pill';
  const pillTitle = resolved ? `${resolved.label}: ${resolved.title} (${resolved.timeStr})` : 'Next upcoming event';

  return (
    <div className="header-next-event" id="header-next-event" style={{ display: resolved ? '' : 'none' }}>
      <div
        className={pillClass}
        id="next-event-pill"
        role="button"
        tabIndex={0}
        title={pillTitle}
        onClick={handlePillClick}
        onKeyDown={handleKeyDown}
      >
        <span className="next-event-label" id="next-event-label">{resolved?.label ?? 'NEXT'}</span>
        <span className="next-event-time" id="next-event-time">{resolved?.timeStr ?? '--:--'}</span>
        <span className="next-event-title" id="next-event-title" title={resolved?.title ?? undefined}>
          {resolved?.title ?? '--'}
        </span>
        <span className="next-event-countdown" id="next-event-countdown">{resolved?.countdown ?? ''}</span>
      </div>
    </div>
  );
}

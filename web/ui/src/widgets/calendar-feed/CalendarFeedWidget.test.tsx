import { act, fireEvent } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { useDisplayStore } from '../../store';
import { renderWidget, seedWidget, widgetMeta } from '../../test-utils/widget';
import { CalendarFeedWidget } from './CalendarFeedWidget';
import { itemAt } from '../../test-utils/lookup';

interface CalendarFeedSeed {
  config?: Record<string, unknown>;
  state?: string;
  timestamp?: string;
}

/** Seeds and renders calendar-feed `id`; `timestamp` overrides seedWidget's fixed entry timestamp. */
function renderCalendarFeed(id: string, data: unknown, { config = {}, state = 'healthy', timestamp }: CalendarFeedSeed = {}) {
  seedWidget(id, widgetMeta('calendar-feed', config, [3, 2]), data, state);
  if (timestamp !== undefined) {
    useDisplayStore.setState((s) => ({ widgets: { [id]: { ...s.widgets[id]!, timestamp } } }));
  }
  return renderWidget(CalendarFeedWidget, id, 'calendar-feed').container;
}

function setUtcHeader(timestamp: string) {
  useDisplayStore.setState({ header: { timestamp, timezone: 'UTC', weather: null } });
}

const chipCalendars = [
  { name: 'Personal', color: '#ff4365', enabled: true },
  { name: 'Work', color: '#3a86ff', enabled: 'true' },
  { name: 'Archived', color: '#999999', enabled: false },
];

describe('CalendarFeedWidget', () => {
  // wantText: [selector, exact textContent]; wantCount: [selector, match count]; wantAbsent: selectors that must not match.
  const tests: {
    name: string;
    data: unknown;
    seed?: CalendarFeedSeed;
    wantText: [string, string][];
    wantCount: [string, number][];
    wantAbsent: string[];
  }[] = [
    {
      name: 'renders loading state when data is undefined',
      data: undefined,
      seed: { timestamp: '' },
      wantText: [['.calendar-loading', 'Loading calendar feed...']],
      wantCount: [],
      wantAbsent: [],
    },
    {
      name: 'renders loading state when data is null',
      data: null,
      seed: { timestamp: '' },
      wantText: [['.calendar-loading', 'Loading calendar feed...']],
      wantCount: [],
      wantAbsent: [],
    },
    {
      name: 'renders loading state for unhydrated {} before the first poll',
      data: {},
      seed: { timestamp: '' },
      wantText: [['.calendar-loading', 'Loading calendar feed...']],
      wantCount: [],
      wantAbsent: ['.calendar-empty'],
    },
    {
      name: 'renders empty state when data has no upcoming events',
      data: { events: [] },
      wantText: [
        ['.calendar-empty .empty-title', 'All clear! Enjoy your day ✨'],
        ['.calendar-empty .empty-subtitle', 'No upcoming events on your agenda'],
      ],
      wantCount: [['.calendar-empty .empty-icon svg', 1]],
      wantAbsent: [],
    },
    {
      name: 'renders error banner when entry state is error',
      data: null,
      seed: { state: 'error', timestamp: '' },
      wantText: [['.widget-error-banner', '⚠Widget Error']],
      wantCount: [],
      wantAbsent: [],
    },
    {
      name: 'renders custom title and active calendar chips in header',
      data: { events: [] },
      seed: { config: { title: 'Family Schedule', calendars: chipCalendars }, timestamp: '' },
      wantText: [
        ['.calendar-header-title', 'Family Schedule'],
        ['.calendar-header-chips .calendar-count-badge', '2 Calendars'],
      ],
      wantCount: [
        ['.calendar-header-chips .calendar-avatar-dot', 2],
        ['.calendar-avatar-dot:nth-child(1)[title="Personal"]', 1],
        ['.calendar-avatar-dot:nth-child(2)[title="Work"]', 1],
      ],
      wantAbsent: [],
    },
  ];

  for (const tc of tests) {
    it(tc.name, () => {
      const container = renderCalendarFeed('cal-1', tc.data, tc.seed);
      for (const [selector, text] of tc.wantText) {
        expect(container.querySelector(selector)?.textContent, selector).toBe(text);
      }
      for (const [selector, count] of tc.wantCount) {
        expect(container.querySelectorAll(selector).length, selector).toBe(count);
      }
      for (const selector of tc.wantAbsent) {
        expect(container.querySelector(selector), selector).toBeNull();
      }
    });
  }

  it('re-renders in place from loading through hydration as the entry updates', () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-10-31T08:00:00Z'));
    setUtcHeader('2026-10-31T08:00:00Z');
    const container = renderCalendarFeed('cal-update', undefined, { timestamp: '' });
    expect(container.querySelector('.calendar-loading')?.textContent).toBe('Loading calendar feed...');

    act(() => {
      seedWidget('cal-update', widgetMeta('calendar-feed', {}, [3, 2]), null, 'healthy');
    });
    expect(container.querySelector('.calendar-loading')?.textContent).toBe('Loading calendar feed...');

    act(() => {
      seedWidget('cal-update', widgetMeta('calendar-feed', {}, [3, 2]), { events: [{ id: 'ev-1', title: 'Team Sync', start: '2026-10-31T14:30:00Z' }] }, 'healthy');
    });
    expect(container.querySelector('.calendar-loading')).toBeNull();
    expect(container.querySelector('.event-title')?.textContent).toBe('Team Sync');
  });

  it('renders multi-day grouped events with all-day and timed details', () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-10-31T08:00:00Z'));
    setUtcHeader('2026-10-31T08:00:00Z');
    const container = renderCalendarFeed(
      'cal-events',
      {
        events: [
          {
            id: 'ev-allday',
            title: 'All Day Hackathon',
            start: '2026-10-31',
            end: '2026-10-31',
            all_day: true,
            calendar_name: 'Events',
            color: '#ff4365',
          },
          {
            id: 'ev-timed',
            title: 'Team Sync',
            start: '2026-10-31T14:30:00Z',
            end: '2026-10-31T15:30:00Z',
            all_day: false,
            location: 'Room A',
            calendar_name: 'Work',
            color: '#3a86ff',
          },
          {
            id: 'ev-tomorrow',
            title: 'Morning Flight',
            start: '2026-11-01T09:00:00Z',
            end: '2026-11-01T12:00:00Z',
            all_day: false,
            calendar_name: 'Travel',
          },
        ],
      },
      { config: { title: 'Upcoming' }, timestamp: '2026-10-31T08:00:00Z' },
    );

    const groups = container.querySelectorAll('.calendar-day-group');
    expect(groups.length).toBe(2);

    // Group 0: Today
    const todayPill = itemAt(groups, 0).querySelector('.day-pill-badge');
    expect(todayPill?.classList.contains('day-pill-today')).toBe(true);
    expect(todayPill?.textContent).toContain('TODAY •');

    const todayEvents = itemAt(groups, 0).querySelectorAll('.calendar-event-item');
    expect(todayEvents.length).toBe(2);

    // All-day item
    expect(itemAt(todayEvents, 0).getAttribute('data-event-id')).toBe('ev-allday');
    expect(itemAt(todayEvents, 0).getAttribute('data-event-allday')).toBe('true');
    expect(itemAt(todayEvents, 0).querySelector('.event-time-allday')?.textContent).toBe('All Day');
    expect(itemAt(todayEvents, 0).querySelector('.event-title')?.textContent).toBe('All Day Hackathon');
    expect(itemAt(todayEvents, 0).querySelector('.event-calendar-pill')?.textContent).toBe('Events');

    // Timed item with location and color
    expect(itemAt(todayEvents, 1).getAttribute('data-event-id')).toBe('ev-timed');
    expect(itemAt(todayEvents, 1).getAttribute('data-event-allday')).toBe('false');
    expect(itemAt(todayEvents, 1).querySelector('.event-time-val')).not.toBeNull();
    expect(itemAt(todayEvents, 1).querySelector('.event-time-ampm')).not.toBeNull();
    expect(itemAt(todayEvents, 1).querySelector('.event-location-text')?.textContent).toBe('Room A');
    expect(itemAt(todayEvents, 1).querySelector('.event-calendar-pill')?.textContent).toBe('Work');

    // Group 1: Tomorrow
    const tomorrowPill = itemAt(groups, 1).querySelector('.day-pill-badge');
    expect(tomorrowPill?.classList.contains('day-pill-tomorrow')).toBe(true);
    expect(tomorrowPill?.textContent).toContain('TOMORROW •');

    const tomorrowEvents = itemAt(groups, 1).querySelectorAll('.calendar-event-item');
    expect(tomorrowEvents.length).toBe(1);
    expect(itemAt(tomorrowEvents, 0).querySelector('.event-title')?.textContent).toBe('Morning Flight');
  });

  it('handles PascalCase Events payload and invalid timestamp without crashing', () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-10-31T08:00:00Z'));
    setUtcHeader('2026-10-31T08:00:00Z');
    const container = renderCalendarFeed(
      'cal-pascal',
      { Events: [{ ID: 'ev-p1', Title: 'Pascal Meeting', Start: '2026-10-31T10:00:00Z', End: '2026-10-31T11:00:00Z' }] },
      { timestamp: 'invalid-timestamp-value' },
    );
    expect(container.querySelector('.event-title')?.textContent).toBe('Pascal Meeting');
  });

  it('opens timed and all-day event modals on click and keyboard, and closes via close button, backdrop click, or Escape', () => {
    setUtcHeader('2026-10-31T08:00:00Z');
    const container = renderCalendarFeed(
      'cal-modal-test',
      {
        events: [
          {
            id: 'ev-allday',
            title: 'All Day Hackathon',
            start: '2026-10-31',
            all_day: true,
            description: 'Full day hackathon building awesome stuff',
            calendar_name: 'Events',
          },
          {
            id: 'ev-timed',
            title: 'Team Sync',
            start: '2026-10-31T14:30:00Z',
            end: '2026-10-31T15:30:00Z',
            all_day: false,
            location: 'Room A',
            description: 'Weekly team sprint sync',
            calendar_name: 'Work',
            color: '#3a86ff',
          },
        ],
      },
      { timestamp: '2026-10-31T08:00:00Z' },
    );

    const timedEvent = container.querySelector('[data-event-id="ev-timed"]')!;
    expect(timedEvent).not.toBeNull();
    expect(timedEvent.classList.contains('cf-event-clickable')).toBe(true);
    expect(timedEvent.getAttribute('role')).toBe('button');
    expect(timedEvent.getAttribute('tabIndex')).toBe('0');

    // 1. Open timed event modal via click
    fireEvent.click(timedEvent);

    let modal = container.querySelector('.cf-modal-overlay');
    expect(modal).not.toBeNull();
    expect(container.querySelector('.cf-modal-title')?.textContent).toBe('Team Sync');
    const modalTime = container.querySelector('.cf-modal-time');
    expect(modalTime?.textContent).toBe('Sat, Oct 31 • 2:30 PM – 3:30 PM');
    const modalLocation = container.querySelector('.cf-modal-location');
    expect(modalLocation?.textContent).toBe('Room A');
    expect(container.querySelector('.cf-modal-owner-chip')?.textContent).toContain('Work');
    expect(container.querySelector('.cf-modal-description')?.textContent).toContain('Weekly team sprint sync');

    // 2. Close via close button
    const closeBtn = container.querySelector('.cf-modal-close')!;
    fireEvent.click(closeBtn);
    expect(container.querySelector('.cf-modal-overlay')).toBeNull();

    // 3. Re-open and close via backdrop click
    fireEvent.click(timedEvent);
    modal = container.querySelector('.cf-modal-overlay');
    expect(modal).not.toBeNull();
    fireEvent.click(modal!);
    expect(container.querySelector('.cf-modal-overlay')).toBeNull();

    // 4. Re-open and close via Escape
    fireEvent.click(timedEvent);
    expect(container.querySelector('.cf-modal-overlay')).not.toBeNull();
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(container.querySelector('.cf-modal-overlay')).toBeNull();

    // 5. Open all-day event modal via Enter key
    const allDayEvent = container.querySelector('[data-event-id="ev-allday"]')!;
    expect(allDayEvent).not.toBeNull();
    fireEvent.keyDown(allDayEvent, { key: 'Enter' });

    modal = container.querySelector('.cf-modal-overlay');
    expect(modal).not.toBeNull();
    expect(container.querySelector('.cf-modal-title')?.textContent).toBe('All Day Hackathon');
    expect(container.querySelector('.cf-modal-time')?.textContent).toBe('Sat, Oct 31 • All Day');
    expect(container.querySelector('.cf-modal-owner-chip')?.textContent).toContain('Events');
    expect(container.querySelector('.cf-modal-description')?.textContent).toContain('Full day hackathon building awesome stuff');

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(container.querySelector('.cf-modal-overlay')).toBeNull();

    // 6. Open all-day event modal via Space key
    fireEvent.keyDown(allDayEvent, { key: ' ' });
    expect(container.querySelector('.cf-modal-overlay')).not.toBeNull();
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(container.querySelector('.cf-modal-overlay')).toBeNull();

    // 7. Open via Spacebar key and ignore non-activation keys
    fireEvent.keyDown(allDayEvent, { key: 'Spacebar' });
    expect(container.querySelector('.cf-modal-overlay')).not.toBeNull();
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(container.querySelector('.cf-modal-overlay')).toBeNull();

    fireEvent.keyDown(allDayEvent, { key: 'ArrowDown' });
    expect(container.querySelector('.cf-modal-overlay')).toBeNull();
  });


  it('handles event without end timestamp in modal correctly', () => {
    const container = renderCalendarFeed(
      'cal-no-end',
      { events: [{ id: 'ev-no-end', title: 'Quick Call', start: '2026-10-31T10:00:00Z', all_day: false }] },
      { timestamp: '2026-10-31T08:00:00Z' },
    );
    const noEndEvent = container.querySelector('[data-event-id="ev-no-end"]')!;
    fireEvent.click(noEndEvent);
    expect(container.querySelector('.cf-modal-overlay')).not.toBeNull();
    expect(container.querySelector('.cf-modal-title')?.textContent).toBe('Quick Call');
  });

  it('auto-closes event modal and resumes store after 60s inactivity timer', () => {
    vi.useFakeTimers();
    setUtcHeader('2026-10-31T08:00:00Z');
    seedWidget(
      'cal-inactivity',
      widgetMeta('calendar-feed', {}, [3, 2]),
      { events: [{ id: 'ev-timed', title: 'Team Sync', start: '2026-10-31T14:30:00Z', end: '2026-10-31T15:30:00Z', all_day: false }] },
    );

    const pauseSpy = vi.spyOn(useDisplayStore.getState(), 'pause');
    const resumeSpy = vi.spyOn(useDisplayStore.getState(), 'resume');

    const { container } = renderWidget(CalendarFeedWidget, 'cal-inactivity', 'calendar-feed');

    const timedEvent = container.querySelector('[data-event-id="ev-timed"]')!;
    fireEvent.click(timedEvent);

    expect(container.querySelector('.cf-modal-overlay')).not.toBeNull();
    expect(pauseSpy).toHaveBeenCalledTimes(1);
    expect(resumeSpy).not.toHaveBeenCalled();

    // Advance 59s: modal still open
    act(() => {
      vi.advanceTimersByTime(59000);
    });
    expect(container.querySelector('.cf-modal-overlay')).not.toBeNull();
    expect(resumeSpy).not.toHaveBeenCalled();

    // Advance remaining 2s (total 61s): modal closes, store resumes
    act(() => {
      vi.advanceTimersByTime(2000);
    });
    expect(container.querySelector('.cf-modal-overlay')).toBeNull();
    expect(resumeSpy).toHaveBeenCalledTimes(1);
  });

  it('covers keyboard navigation (Space, Spacebar), fallback event ID, camelCase fields, and tomorrow/future groups', () => {
    setUtcHeader('2026-10-31T08:00:00Z');
    const container = renderCalendarFeed(
      'cal-coverage',
      {
        events: [
          { title: 'Tomorrow Standup', start: '2026-11-01T09:00:00Z', allDay: true, calendarName: 'Team Work' },
          { id: 'ev-future', title: 'Future Workshop', start: '2026-11-05T14:00:00Z', all_day: false, calendar_name: 'Personal' },
        ],
      },
      { timestamp: '2026-10-31T08:00:00Z' },
    );

    expect(container.querySelector('.day-pill-tomorrow')?.textContent).toContain('TOMORROW');
    expect(container.querySelector('.day-pill-future')?.textContent).toContain('Thursday');

    const items = container.querySelectorAll('.calendar-event-item');
    expect(items.length).toBe(2);

    fireEvent.keyDown(itemAt(items, 0), { key: ' ' });
    expect(container.querySelector('.cf-modal-overlay')).not.toBeNull();
    expect(container.querySelector('.cf-modal-title')?.textContent).toBe('Tomorrow Standup');

    const closeBtn = container.querySelector('.cf-modal-close')!;
    fireEvent.click(closeBtn);
    expect(container.querySelector('.cf-modal-overlay')).toBeNull();

    fireEvent.keyDown(itemAt(items, 1), { key: 'Spacebar' });
    expect(container.querySelector('.cf-modal-overlay')).not.toBeNull();
    expect(container.querySelector('.cf-modal-title')?.textContent).toBe('Future Workshop');

    fireEvent.keyDown(itemAt(items, 1), { key: 'Tab' });
    expect(container.querySelector('.cf-modal-overlay')).not.toBeNull();
  });
});

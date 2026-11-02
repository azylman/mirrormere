import { fireEvent, render } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useDisplayStore } from '../store';
import {
  NextUp,
  formatRelativeCountdown,
  parseEventDates,
  resolveNextUpEvent,
  selectCalendarEvents,
  useCalendarEvents,
} from './NextUp';
import type { RawCalendarEvent } from './NextUp';

describe('formatRelativeCountdown', () => {
  it('formats under 1 minute as <1m', () => {
    expect(formatRelativeCountdown(0)).toBe('in <1m');
    expect(formatRelativeCountdown(-5000)).toBe('in <1m');
    expect(formatRelativeCountdown(20000, true)).toBe('ends in <1m');
  });

  it('formats minutes under an hour', () => {
    expect(formatRelativeCountdown(15 * 60 * 1000)).toBe('in 15m');
    expect(formatRelativeCountdown(45 * 60 * 1000, true)).toBe('ends in 45m');
  });

  it('formats exact hours', () => {
    expect(formatRelativeCountdown(60 * 60 * 1000)).toBe('in 1h');
    expect(formatRelativeCountdown(2 * 60 * 60 * 1000, true)).toBe('ends in 2h');
  });

  it('formats combined hours and minutes', () => {
    expect(formatRelativeCountdown(90 * 60 * 1000)).toBe('in 1h 30m');
    expect(formatRelativeCountdown(125 * 60 * 1000, true)).toBe('ends in 2h 5m');
  });
});

describe('parseEventDates', () => {
  const refNow = new Date('2026-10-31T10:00:00Z');

  it('returns null for invalid inputs', () => {
    expect(parseEventDates(null as unknown as RawCalendarEvent, refNow)).toBeNull();
    expect(parseEventDates({ title: 'Invalid', start: 'invalid-date' }, refNow)).toBeNull();
  });

  it('parses all-day ISO date string', () => {
    const parsed = parseEventDates(
      {
        id: 'ev-all-day',
        title: 'Halloween',
        start: '2026-10-31',
        allDay: true,
      },
      refNow,
      'UTC'
    );
    expect(parsed).not.toBeNull();
    expect(parsed?.isAllDay).toBe(true);
    expect(parsed?.startDate.toISOString()).toContain('2026-10-31T00:00:00');
    expect(parsed?.endDate.toISOString()).toContain('2026-10-31T23:59:59');
  });

  it('parses timed HH:MM format', () => {
    const parsed = parseEventDates(
      {
        id: 'ev-meeting',
        title: 'Team Sync',
        start: '14:30',
        end: '15:30',
        date: '2026-10-31',
      },
      refNow,
      'UTC'
    );
    expect(parsed).not.toBeNull();
    expect(parsed?.isAllDay).toBe(false);
    expect(parsed?.startDate.toISOString()).toContain('2026-10-31T14:30:00');
    expect(parsed?.endDate.toISOString()).toContain('2026-10-31T15:30:00');
  });

  it('defaults end time to 1 hour after start when missing', () => {
    const parsed = parseEventDates(
      {
        title: 'Solo Start',
        start: '2026-10-31T12:00:00Z',
      },
      refNow,
      'UTC'
    );
    expect(parsed).not.toBeNull();
    expect(parsed!.endDate.getTime() - parsed!.startDate.getTime()).toBe(60 * 60 * 1000);
  });
});

describe('resolveNextUpEvent priority rules', () => {
  const baseNow = new Date('2026-10-31T10:00:00Z');
  const tz = 'UTC';

  it('ignores expired events whose end date is in the past', () => {
    const events: RawCalendarEvent[] = [
      {
        id: 'expired-1',
        title: 'Breakfast',
        start: '2026-10-31T08:00:00Z',
        end: '2026-10-31T09:00:00Z',
      },
    ];
    const resolved = resolveNextUpEvent(events, baseNow, tz);
    expect(resolved).toBeNull();
  });

  it('prioritizes ongoing timed event over upcoming timed and all-day events', () => {
    const events: RawCalendarEvent[] = [
      {
        id: 'future-today',
        title: 'Afternoon Tea',
        start: '2026-10-31T15:00:00Z',
        end: '2026-10-31T16:00:00Z',
      },
      {
        id: 'ongoing-now',
        title: 'Morning Meeting',
        start: '2026-10-31T09:30:00Z',
        end: '2026-10-31T10:30:00Z',
      },
      {
        id: 'all-day-today',
        title: 'Halloween Holiday',
        start: '2026-10-31',
        allDay: true,
      },
    ];

    const resolved = resolveNextUpEvent(events, baseNow, tz);
    expect(resolved).not.toBeNull();
    expect(resolved?.label).toBe('NOW');
    expect(resolved?.title).toBe('Morning Meeting');
    expect(resolved?.isNow).toBe(true);
    expect(resolved?.countdown).toBe('ends in 30m');
  });

  it('prioritizes today upcoming timed event over today all-day event', () => {
    const events: RawCalendarEvent[] = [
      {
        id: 'all-day-today',
        title: 'Full Day Festival',
        start: '2026-10-31',
        allDay: true,
      },
      {
        id: 'upcoming-today',
        title: 'Lunch with Alice',
        start: '2026-10-31T12:00:00Z',
        end: '2026-10-31T13:00:00Z',
      },
    ];

    const resolved = resolveNextUpEvent(events, baseNow, tz);
    expect(resolved).not.toBeNull();
    expect(resolved?.label).toBe('NEXT');
    expect(resolved?.title).toBe('Lunch with Alice');
    expect(resolved?.isNow).toBe(false);
    expect(resolved?.countdown).toBe('in 2h');
  });

  it('prioritizes today all-day event when no timed events exist today', () => {
    const events: RawCalendarEvent[] = [
      {
        id: 'all-day-today',
        title: 'Costume Day',
        start: '2026-10-31',
        allDay: true,
      },
      {
        id: 'future-day-timed',
        title: 'Monday Standup',
        start: '2026-11-02T09:00:00Z',
        end: '2026-11-02T10:00:00Z',
      },
    ];

    const resolved = resolveNextUpEvent(events, baseNow, tz);
    expect(resolved).not.toBeNull();
    expect(resolved?.label).toBe('TODAY');
    expect(resolved?.title).toBe('Costume Day');
    expect(resolved?.timeStr).toBe('All Day');
    expect(resolved?.countdown).toBe('');
    expect(resolved?.isNow).toBe(false);
  });

  it('resolves future timed event when nothing is scheduled today', () => {
    const events: RawCalendarEvent[] = [
      {
        id: 'future-timed',
        title: 'Doctor Appointment',
        start: '2026-11-01T14:00:00Z',
        end: '2026-11-01T15:00:00Z',
      },
    ];

    const resolved = resolveNextUpEvent(events, baseNow, tz);
    expect(resolved).not.toBeNull();
    expect(resolved?.label).toBe('NEXT');
    expect(resolved?.title).toBe('Doctor Appointment');
    expect(resolved?.timeStr).toContain('Sun');
    expect(resolved?.isNow).toBe(false);
  });

  it('resolves future all-day event with uppercase weekday label', () => {
    const events: RawCalendarEvent[] = [
      {
        id: 'future-all-day',
        title: 'All Saints Day',
        start: '2026-11-01',
        allDay: true,
      },
    ];

    const resolved = resolveNextUpEvent(events, baseNow, tz);
    expect(resolved).not.toBeNull();
    expect(resolved?.label).toBe('SUN');
    expect(resolved?.title).toBe('All Saints Day');
    expect(resolved?.timeStr).toBe('All Day');
    expect(resolved?.countdown).toBe('');
  });
});

describe('selectCalendarEvents store extraction and deduplication', () => {
  it('extracts and deduplicates events across feed, grid, and viewModel formats', () => {
    const widgets = {
      'feed-widget': {
        state: 'ok',
        timestamp: '2026-10-31T10:00:00Z',
        data: {
          events: [
            { id: 'shared-ev-1', title: 'Dentist', start: '2026-10-31T11:00:00Z' },
            { id: 'shared-ev-1', title: 'Dentist Duplicate', start: '2026-10-31T11:00:00Z' },
            { id: 'feed-ev-2', title: 'Gym Workout', start: '2026-10-31T18:00:00Z' },
          ],
        },
      },
      'grid-widget': {
        state: 'ok',
        timestamp: '2026-10-31T10:00:00Z',
        data: {
          days: [
            {
              date: '2026-10-31',
              events: [
                { id: 'grid-ev-1', title: 'Coffee Chat', start: '11:00' },
                { id: 'grid-ev-3', title: 'Dinner', start: '19:00' },
              ],
              all_day: [{ id: 'holiday-1', title: 'Halloween' }],
            },
          ],
        },
      },
      'vm-widget': {
        state: 'ok',
        timestamp: '2026-10-31T10:00:00Z',
        data: null,
        viewModel: {
          week: {
            days: [
              {
                date: '2026-11-01',
                events: [{ id: 'vm-ev-4', title: 'Grocery Run', start: '10:00' }],
              },
            ],
          },
        },
      },
    };

    const extracted = selectCalendarEvents(widgets);
    expect(extracted).toHaveLength(6);
    const titles = extracted.map((e) => e.title);
    expect(titles).toContain('Dentist');
    expect(titles).toContain('Gym Workout');
    expect(titles).toContain('Coffee Chat');
    expect(titles).toContain('Dinner');
    expect(titles).toContain('Halloween');
    expect(titles).toContain('Grocery Run');
  });

  it('preserves multi-day event slices across grid day columns and parses empty start/end', () => {
    const widgets = {
      'grid-widget': {
        state: 'ok',
        timestamp: '2026-10-31T10:00:00Z',
        data: {
          days: [
            {
              date: '2026-10-30',
              events: [
                { id: 'multiday-conf', title: 'Tech Conference', start: '', end: '' },
              ],
            },
            {
              date: '2026-10-31',
              events: [
                { id: 'multiday-conf', title: 'Tech Conference', start: '', end: '' },
              ],
            },
          ],
        },
      },
    };

    const extracted = selectCalendarEvents(widgets);
    expect(extracted).toHaveLength(2);
    expect(extracted[0].id).toBe('multiday-conf-2026-10-30');
    expect(extracted[0].start).toBe('2026-10-30');
    expect(extracted[0].allDay).toBe(true);
    expect(extracted[1].id).toBe('multiday-conf-2026-10-31');
    expect(extracted[1].start).toBe('2026-10-31');
    expect(extracted[1].allDay).toBe(true);

    // On 2026-10-31 at 10:00 UTC, the 2026-10-30 slice is expired, but the 2026-10-31 slice resolves as TODAY!
    const resolved = resolveNextUpEvent(extracted, new Date('2026-10-31T10:00:00Z'), 'UTC');
    expect(resolved).not.toBeNull();
    expect(resolved?.label).toBe('TODAY');
    expect(resolved?.title).toBe('Tech Conference');
  });

  it('supports raw array in entry.data', () => {
    const widgets = {
      arrayFeed: {
        state: 'ok',
        timestamp: '2026-10-31T10:00:00Z',
        data: [{ id: 'arr-1', title: 'Array Event', start: '2026-10-31T14:00:00Z' }],
      },
    };
    const extracted = selectCalendarEvents(widgets);
    expect(extracted).toHaveLength(1);
    expect(extracted[0].title).toBe('Array Event');
  });

  it('extracts day.timed events, vm.week.all_day_events, and vm.month.days', () => {
    const widgets = {
      timedWidget: {
        state: 'ok',
        timestamp: '2026-10-31T10:00:00Z',
        data: {
          days: [
            {
              date: '2026-10-31',
              timed: [{ id: 'timed-ev-1', title: 'Timed Standup', start: '09:00', end: '09:30' }],
            },
          ],
        },
      },
      vmWidget: {
        state: 'ok',
        timestamp: '2026-10-31T10:00:00Z',
        data: null,
        viewModel: {
          week: {
            days: [
              { date: '2026-11-01', events: [] },
              { date: '2026-11-02', events: [] },
              { date: '2026-11-03', events: [] },
            ],
            all_day_events: [
              {
                id: 'all-day-banner',
                title: 'Design Sprint',
                start_col: 2,
              },
            ],
          },
          month: {
            days: [
              {
                date: '2026-11-15',
                events: [{ id: 'month-ev-1', title: 'Board Meeting', start: '14:00' }],
              },
            ],
          },
        },
      },
    };

    const extracted = selectCalendarEvents(widgets);
    const titles = extracted.map((e) => e.title);
    expect(titles).toContain('Timed Standup');
    expect(titles).toContain('Design Sprint');
    expect(titles).toContain('Board Meeting');

    const sprint = extracted.find((e) => e.title === 'Design Sprint');
    expect(sprint?.date).toBe('2026-11-02');
    expect(sprint?.allDay).toBe(true);
  });

  it('useCalendarEvents hook extracts events reactively from store', () => {
    useDisplayStore.setState({
      widgets: {
        w1: {
          state: 'ok',
          timestamp: '2026-10-31T10:00:00Z',
          data: [{ id: 'hook-ev', title: 'Hook Event', start: '2026-10-31T12:00:00Z' }],
        },
      },
    });
    function TestHookConsumer() {
      const events = useCalendarEvents();
      return <div data-testid="count">{events.length}</div>;
    }
    const { getByTestId } = render(<TestHookConsumer />);
    expect(getByTestId('count').textContent).toBe('1');
  });
});

describe('NextUp component', () => {
  beforeEach(() => {
    useDisplayStore.setState({
      header: { timestamp: null, timezone: 'UTC', weather: null },
      widgets: {},
    });
  });

  it('renders hidden when no calendar events exist', () => {
    const { container } = render(<NextUp />);
    const wrapper = container.querySelector('#header-next-event') as HTMLDivElement;
    expect(wrapper).not.toBeNull();
    expect(wrapper.style.display).toBe('none');
  });

  it('renders ongoing event pill with status-now and formatted countdown', () => {
    const now = new Date('2026-10-31T10:00:00Z');
    const events: RawCalendarEvent[] = [
      {
        id: 'now-1',
        title: 'Project Demo',
        start: '2026-10-31T09:45:00Z',
        end: '2026-10-31T10:45:00Z',
      },
    ];

    const { container } = render(<NextUp now={now} timezone="UTC" events={events} />);
    const wrapper = container.querySelector('#header-next-event') as HTMLDivElement;
    expect(wrapper.style.display).toBe('');

    const pill = container.querySelector('#next-event-pill');
    expect(pill?.classList.contains('status-now')).toBe(true);
    expect(container.querySelector('#next-event-label')?.textContent).toBe('NOW');
    expect(container.querySelector('#next-event-title')?.textContent).toBe('Project Demo');
    expect(container.querySelector('#next-event-countdown')?.textContent).toBe('ends in 45m');
  });

  it('handles click and keyboard Enter smooth scroll', () => {
    const onPillClick = vi.fn();
    const now = new Date('2026-10-31T10:00:00Z');
    const events: RawCalendarEvent[] = [
      {
        id: 'next-1',
        title: 'Upcoming Talk',
        start: '2026-10-31T11:00:00Z',
        end: '2026-10-31T12:00:00Z',
      },
    ];

    const { container } = render(
      <NextUp now={now} timezone="UTC" events={events} onPillClick={onPillClick} />
    );
    const pill = container.querySelector('#next-event-pill') as HTMLDivElement;

    fireEvent.click(pill);
    expect(onPillClick).toHaveBeenCalledTimes(1);

    fireEvent.keyDown(pill, { key: 'Enter' });
    expect(onPillClick).toHaveBeenCalledTimes(2);

    fireEvent.keyDown(pill, { key: ' ' });
    expect(onPillClick).toHaveBeenCalledTimes(3);
  });

  it('prefers document.body.dataset.timezone before initial SSE header event arrives', () => {
    document.body.dataset.timezone = 'America/New_York';
    useDisplayStore.setState({
      header: { timestamp: null, timezone: 'UTC', weather: null },
      widgets: {},
    });

    const events: RawCalendarEvent[] = [
      {
        id: 'ev-tz',
        title: 'Strategy Meeting',
        start: '2026-10-31T15:00:00Z',
        end: '2026-10-31T16:00:00Z',
      },
    ];

    const now = new Date('2026-10-31T14:00:00Z');
    const { container } = render(<NextUp now={now} events={events} />);

    // In America/New_York (EDT, UTC-4), 15:00 UTC is 11:00 AM
    expect(container.querySelector('#next-event-time')?.textContent).toContain('11:00');
    delete document.body.dataset.timezone;
  });

  it('smoothly scrolls to calendar target when onPillClick is omitted', () => {
    const gridTarget = document.createElement('div');
    gridTarget.className = 'widget-calendar-grid';
    const scrollSpy = vi.fn();
    gridTarget.scrollIntoView = scrollSpy;
    document.body.appendChild(gridTarget);

    const now = new Date('2026-10-31T10:00:00Z');
    const events: RawCalendarEvent[] = [
      {
        id: 'next-scroll',
        title: 'Calendar Review',
        start: '2026-10-31T11:00:00Z',
      },
    ];

    const { container } = render(<NextUp now={now} timezone="UTC" events={events} />);
    const pill = container.querySelector('#next-event-pill') as HTMLDivElement;

    fireEvent.click(pill);
    expect(scrollSpy).toHaveBeenCalledWith({ behavior: 'smooth', block: 'nearest' });

    document.body.removeChild(gridTarget);
  });
});

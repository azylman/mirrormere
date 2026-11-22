import { Profiler } from 'react';
import { act, fireEvent, render, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useDisplayStore } from '../store';
import {
  resetSharedYtMusicState,
  setSharedYtMusicState,
} from '../widgets/yt-music/ytMusicTypes';
import {
  NextUp,
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
  useCalendarEvents,
} from './NextUp';
import type { RawCalendarEvent } from './NextUp';
import { itemAt } from '../test-utils/lookup';
import { seedWidget, widgetMeta } from '../test-utils/widget';

describe('formatRelativeCountdown', () => {
  const tests = [
    { name: 'zero is under a minute', ms: 0, isEnding: false, want: 'in <1m' },
    { name: 'negative is under a minute', ms: -5000, isEnding: false, want: 'in <1m' },
    { name: 'ending under a minute', ms: 20000, isEnding: true, want: 'ends in <1m' },
    { name: 'minutes under an hour', ms: 15 * 60 * 1000, isEnding: false, want: 'in 15m' },
    { name: 'ending minutes under an hour', ms: 45 * 60 * 1000, isEnding: true, want: 'ends in 45m' },
    { name: 'exact hour', ms: 60 * 60 * 1000, isEnding: false, want: 'in 1h' },
    { name: 'ending exact hours', ms: 2 * 60 * 60 * 1000, isEnding: true, want: 'ends in 2h' },
    { name: 'hours and minutes', ms: 90 * 60 * 1000, isEnding: false, want: 'in 1h 30m' },
    { name: 'ending hours and minutes', ms: 125 * 60 * 1000, isEnding: true, want: 'ends in 2h 5m' },
  ];

  for (const tc of tests) {
    it(tc.name, () => {
      expect(formatRelativeCountdown(tc.ms, tc.isEnding)).toBe(tc.want);
    });
  }
});

describe('parseEventDates', () => {
  const refNow = new Date('2026-10-31T10:00:00Z');
  const allDay = (title: string, day: string) => ({ title, isAllDay: true, start: `${day}T00:00:00.000Z`, end: `${day}T23:59:59.999Z` });
  const timed = (title: string, start: string, end: string) => ({
    title,
    isAllDay: false,
    start: `2026-10-31T${start}:00.000Z`,
    end: `2026-10-31T${end}:00.000Z`,
  });
  const tests: { name: string; raw: unknown; tz?: string; want: ReturnType<typeof allDay> | null }[] = [
    { name: 'null input', raw: null, want: null },
    { name: 'invalid start string', raw: { title: 'Invalid', start: 'invalid-date' }, want: null },
    {
      name: 'all-day ISO date',
      raw: { id: 'ev-all-day', title: 'Halloween', start: '2026-10-31', allDay: true }, tz: 'UTC', want: allDay('Halloween', '2026-10-31'),
    },
    {
      name: 'all-day from date field', raw: { title: 'Ad Date', date: '2026-10-31', allDay: true }, tz: 'UTC', want: allDay('Ad Date', '2026-10-31'),
    },
    {
      name: 'all-day from dayDate field',
      raw: { title: 'Ad DayDate', dayDate: '2026-10-31', allDay: true }, tz: 'UTC', want: allDay('Ad DayDate', '2026-10-31'),
    },
    {
      name: 'all-day from a Date start',
      raw: { title: 'Ad Date Obj', start: new Date('2026-10-31T00:00:00Z'), allDay: true },
      tz: 'UTC',
      want: allDay('Ad Date Obj', '2026-10-31'),
    },
    {
      name: 'all-day with an invalid end falls back to end of day',
      raw: { title: 'Ad Invalid End', start: '2026-10-31', end: 'bad-end', allDay: true },
      tz: 'UTC',
      want: allDay('Ad Invalid End', '2026-10-31'),
    },
    { name: 'all-day inferred from a bare date', raw: { title: 'UTC Ev', start: '2026-10-31' }, tz: 'UTC', want: allDay('UTC Ev', '2026-10-31') },
    {
      name: 'invalid timezone falls back to host-local midnight',
      raw: { title: 'Invalid Tz Ev', start: '2026-10-31' },
      tz: 'Invalid/Zone',
      want: {
        title: 'Invalid Tz Ev',
        isAllDay: true,
        start: new Date(2026, 9, 31, 0, 0, 0, 0).toISOString(),
        end: new Date(2026, 9, 31, 23, 59, 59, 999).toISOString(),
      },
    },
    {
      name: 'snake_case all_day',
      raw: { title: 'Snake AllDay', start: '2026-10-31', all_day: true }, tz: 'UTC', want: allDay('Snake AllDay', '2026-10-31'),
    },
    {
      name: 'future all-day',
      raw: { title: 'Upcoming All Day', start: '2026-11-01', all_day: true }, tz: 'UTC', want: allDay('Upcoming All Day', '2026-11-01'),
    },
    {
      name: 'all-day Date instances keep their times and default the title',
      raw: { title: '', start: new Date('2026-10-31T12:00:00Z'), end: new Date('2026-10-31T13:00:00Z'), allDay: true },
      tz: 'UTC',
      want: { ...timed('Event', '12:00', '13:00'), isAllDay: true },
    },
    {
      name: 'timed HH:MM with date',
      raw: { id: 'ev-meeting', title: 'Team Sync', start: '14:30', end: '15:30', date: '2026-10-31' },
      tz: 'UTC',
      want: timed('Team Sync', '14:30', '15:30'),
    },
    {
      name: 'timed HH:MM with date (morning)',
      raw: { title: 'Timed Standalone', start: '09:30', end: '10:45', date: '2026-10-31' },
      tz: 'UTC',
      want: timed('Timed Standalone', '09:30', '10:45'),
    },
    {
      name: 'timed HH:MM with dayDate',
      raw: { title: 'Day Date Event', start: '10:00', end: '11:00', dayDate: '2026-10-31' },
      tz: 'UTC',
      want: timed('Day Date Event', '10:00', '11:00'),
    },
    {
      name: 'timed HH:MM without date uses the reference day',
      raw: { title: 'Timed No Date', start: '14:15' }, tz: 'UTC', want: timed('Timed No Date', '14:15', '15:15'),
    },
    {
      name: 'missing end defaults to one hour',
      raw: { title: 'Solo Start', start: '2026-10-31T12:00:00Z' }, tz: 'UTC', want: timed('Solo Start', '12:00', '13:00'),
    },
    {
      name: 'timed Date instances',
      raw: { title: 'Timed Date Objs', start: new Date('2026-10-31T09:00:00Z'), end: new Date('2026-10-31T10:00:00Z') },
      tz: 'UTC',
      want: timed('Timed Date Objs', '09:00', '10:00'),
    },
    {
      name: 'timed Date instances in the afternoon',
      raw: { title: 'Timed Dates', start: new Date('2026-10-31T14:00:00Z'), end: new Date('2026-10-31T15:00:00Z') },
      tz: 'UTC',
      want: timed('Timed Dates', '14:00', '15:00'),
    },
    {
      name: 'timed Date start without end defaults to one hour',
      raw: { title: 'Timed Start Only', start: new Date('2026-10-31T14:00:00Z') },
      tz: 'UTC',
      want: timed('Timed Start Only', '14:00', '15:00'),
    },
  ];

  for (const tc of tests) {
    it(tc.name, () => {
      const p = parseEventDates(tc.raw, refNow, tc.tz);
      const got = p && { title: p.title, isAllDay: p.isAllDay, start: p.startDate.toISOString(), end: p.endDate.toISOString() };
      expect(got).toEqual(tc.want);
    });
  }
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
  it('still resolves events when the timezone is invalid', () => {
    const resolved = resolveNextUpEvent(
      [{ id: 'inv-tz', title: 'Invalid TZ Ev', start: '2026-10-31T12:00:00Z' }],
      new Date('2026-10-31T10:00:00Z'),
      'Invalid/NonExistent_Timezone',
    );
    expect(resolved?.title).toBe('Invalid TZ Ev');
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
    expect(itemAt(extracted, 0).id).toBe('multiday-conf-2026-10-30');
    expect(itemAt(extracted, 0).start).toBe('2026-10-30');
    expect(itemAt(extracted, 0).allDay).toBe(true);
    expect(itemAt(extracted, 1).id).toBe('multiday-conf-2026-10-31');
    expect(itemAt(extracted, 1).start).toBe('2026-10-31');
    expect(itemAt(extracted, 1).allDay).toBe(true);

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
    expect(itemAt(extracted, 0).title).toBe('Array Event');
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
  const toleranceTests = [
    {
      name: 'week all_day_events skip nulls and keep camelCase and out-of-range columns',
      widgets: {
        w: {
          state: 'ok',
          timestamp: '2026-10-31T12:00:00Z',
          viewModel: {
            week: { days: null, all_day_events: [null, { title: 'Camel StartCol', startCol: 1 }, { title: 'OutOfBounds Col', start_col: 99 }] },
          },
        },
      },
      wantTitles: ['Camel StartCol', 'OutOfBounds Col'],
    },
    {
      name: 'month days skip null days and null events',
      widgets: {
        w2: {
          state: 'ok',
          timestamp: '2026-10-31T12:00:00Z',
          viewModel: { month: { days: [null, { date: '2026-10-31', events: [null, { title: 'Month Ev', start: '2026-10-31T15:00:00Z' }] }] } },
        },
      },
      wantTitles: ['Month Ev'],
    },
    {
      name: 'null widget entries and untitled day events are dropped',
      widgets: {
        wNull: null,
        wDayDate: {
          state: 'ok',
          data: { days: [{ date: '2026-10-31', events: [{ title: 'DayDate Ev', start: '11:00' }, { id: 'no-title', start: '12:00' }] }] },
        },
      },
      wantTitles: ['DayDate Ev'],
    },
  ];

  for (const tc of toleranceTests) {
    it(`tolerates malformed calendar payloads: ${tc.name}`, () => {
      // @ts-expect-error loosely typed widget JSON
      expect(selectCalendarEvents(tc.widgets).map((e) => e.title)).toEqual(tc.wantTitles);
    });
  }
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

  const pillNavigationTests = [
    {
      name: 'custom onPillClick handles click, Enter and Space',
      target: null, onPillClick: true, keys: ['click', 'Enter', ' '], wantCallbacks: 3, wantScrolls: 0,
    },
    {
      name: 'custom onPillClick wins over a calendar target',
      target: 'widget-calendar-grid', onPillClick: true, keys: ['click'], wantCallbacks: 1, wantScrolls: 0,
    },
    {
      name: 'grid target scrolls on click, Enter and Space',
      target: 'widget-calendar-grid', onPillClick: false, keys: ['click', 'Enter', ' '], wantCallbacks: 0, wantScrolls: 3,
    },
    { name: 'feed target scrolls on click', target: 'widget-calendar-feed', onPillClick: false, keys: ['click'], wantCallbacks: 0, wantScrolls: 1 },
    { name: 'other keys are ignored', target: 'widget-calendar-grid', onPillClick: false, keys: ['a', 'Escape'], wantCallbacks: 0, wantScrolls: 0 },
    { name: 'missing calendar element is a no-op', target: null, onPillClick: false, keys: ['click', 'Enter'], wantCallbacks: 0, wantScrolls: 0 },
  ];

  for (const tc of pillNavigationTests) {
    it(`pill navigation: ${tc.name}`, () => {
      const scrollSpy = vi.fn();
      const targetEl = document.createElement('div');
      if (tc.target) {
        targetEl.className = tc.target;
        targetEl.scrollIntoView = scrollSpy;
        document.body.appendChild(targetEl);
      }
      const onPillClick = vi.fn();
      const { container } = render(
        <NextUp
          now={new Date('2026-10-31T10:00:00Z')}
          timezone="UTC"
          events={[{ id: 'nav-1', title: 'Future Event', start: '2026-10-31T14:00:00Z' }]}
          {...(tc.onPillClick ? { onPillClick } : {})}
        />,
      );
      const pill = container.querySelector('#next-event-pill') as HTMLDivElement;
      for (const key of tc.keys) {
        if (key === 'click') fireEvent.click(pill);
        else fireEvent.keyDown(pill, { key });
      }
      targetEl.remove();
      expect(onPillClick).toHaveBeenCalledTimes(tc.wantCallbacks);
      expect(scrollSpy.mock.calls).toEqual(Array.from({ length: tc.wantScrolls }, () => [{ behavior: 'smooth', block: 'nearest' }]));
    });
  }

  it('renders the pill when the timezone is invalid', () => {
    const { container } = render(
      <NextUp
        now={new Date('2026-10-31T10:00:00Z')}
        timezone="Invalid/Timezone"
        events={[{ id: 'inv-tz-c', title: 'Invalid TZ Component', start: '2026-10-31T12:00:00Z' }]}
      />,
    );
    expect(container.querySelector('#next-event-title')?.textContent).toBe('Invalid TZ Component');
  });

  it('exercises periodic interval timer in NextUp component when propNow is omitted', () => {
    vi.useFakeTimers();
    try {
      const events: RawCalendarEvent[] = [
        {
          id: 'timer-ev',
          title: 'Timer Event',
          start: new Date(Date.now() + 60000).toISOString(),
        },
      ];

      const { unmount } = render(<NextUp timezone="UTC" events={events} />);
      // Fast-forward 15s to trigger scheduleNext timer callback
      act(() => {
        vi.advanceTimersByTime(16000);
      });
      unmount();
    } finally {
      vi.useRealTimers();
    }
  });

  it('sorts multiple events within ongoingTimed, upcomingTimed, allDayToday, and upcomingAllDay buckets', () => {
    const baseNow = new Date('2026-10-31T10:00:00Z');
    const events: RawCalendarEvent[] = [
      // 2 ongoing timed events
      { id: 'on-2', title: 'Later Ongoing', start: '2026-10-31T09:00:00Z', end: '2026-10-31T12:00:00Z' },
      { id: 'on-1', title: 'Earlier Ongoing', start: '2026-10-31T09:30:00Z', end: '2026-10-31T11:00:00Z' },
    ];
    // Ongoing sorted by end date ascending -> on-1 ends earlier (11:00)
    const res1 = resolveNextUpEvent(events, baseNow, 'UTC');
    expect(res1?.title).toBe('Earlier Ongoing');

    // 2 upcoming timed events today
    const events2: RawCalendarEvent[] = [
      { id: 'up-2', title: 'Later Upcoming', start: '2026-10-31T15:00:00Z', end: '2026-10-31T16:00:00Z' },
      { id: 'up-1', title: 'Earlier Upcoming', start: '2026-10-31T12:00:00Z', end: '2026-10-31T13:00:00Z' },
    ];
    const res2 = resolveNextUpEvent(events2, baseNow, 'UTC');
    expect(res2?.title).toBe('Earlier Upcoming');

    // 2 all day events today
    const events3: RawCalendarEvent[] = [
      { id: 'ad-2', title: 'All Day 2', start: '2026-10-31', allDay: true },
      { id: 'ad-1', title: 'All Day 1', start: '2026-10-31', allDay: true },
    ];
    const res3 = resolveNextUpEvent(events3, baseNow, 'UTC');
    expect(res3?.label).toBe('TODAY');

    // 2 upcoming all day events future
    const events4: RawCalendarEvent[] = [
      { id: 'fad-2', title: 'Future All Day 2', start: '2026-11-03', allDay: true },
      { id: 'fad-1', title: 'Future All Day 1', start: '2026-11-02', allDay: true },
    ];
    const res4 = resolveNextUpEvent(events4, baseNow, 'UTC');
    expect(res4?.title).toBe('Future All Day 1');
  });

  it('selectCalendarEvents extracts from data arrays and viewModel week/month/days/events', () => {
    const widgets = {
      'grid-w': {
        meta: { type: 'calendar-grid', origin: [0, 0], dimensions: [3, 2], config: {}, assets_base: '' },
        state: 'healthy',
        timestamp: '2026-10-31T12:00:00Z',
        data: [
          {
            date: '2026-10-31',
            events: [{ title: 'Grid Day Ev', start: '10:00' }],
            early_events: [{ title: 'Early Ev', start: '06:00' }],
            late_events: [{ title: 'Late Ev', start: '22:00' }],
            timed: [{ title: 'Timed Ev', start: '14:00' }],
            all_day: [{ title: 'All Day Ev' }],
          },
        ],
        viewModel: {
          week: {
            days: [{ date: '2026-10-31' }],
            all_day_events: [
              { title: 'Week All Day', start_col: 1 },
              { title: 'Week All Day 2', start: '2026-10-31' },
            ],
          },
          month: {
            days: [
              {
                date: '2026-11-01',
                events: [{ title: 'Month Day Ev', start: '2026-11-01T10:00:00Z' }],
              },
            ],
          },
          days: [
            {
              date: '2026-11-02',
              events: [{ title: 'Direct VM Day Ev', start: '2026-11-02T10:00:00Z' }],
            },
          ],
          events: [{ title: 'Direct VM Event', start: '2026-11-03T10:00:00Z' }],
        },
      },
    };

    // @ts-expect-error test widgets input
    const extracted = selectCalendarEvents(widgets);
    expect(extracted.length).toBeGreaterThanOrEqual(8);
    const titles = extracted.map((e) => e.title);
    expect(titles).toContain('Grid Day Ev');
    expect(titles).toContain('Early Ev');
    expect(titles).toContain('Late Ev');
    expect(titles).toContain('Timed Ev');
    expect(titles).toContain('All Day Ev');
    expect(titles).toContain('Week All Day');
    expect(titles).toContain('Month Day Ev');
    expect(titles).toContain('Direct VM Day Ev');
    expect(titles).toContain('Direct VM Event');
  });

  it('does not re-render NextUp or mutate calendar event reference when an unrelated non-calendar widget updates', () => {
    seedWidget(
      'cal-1',
      widgetMeta('calendar-grid', {}, [3, 2]),
      [{ date: '2026-10-31', events: [{ title: 'Team Sync', start: '2026-10-31T11:00:00Z', end: '2026-10-31T12:00:00Z' }] }],
      'ok',
    );

    const onRender = vi.fn();
    const fixedNow = new Date('2026-10-31T10:00:00Z');

    const { container } = render(
      <Profiler id="NextUp" onRender={onRender}>
        <NextUp now={fixedNow} timezone="UTC" />
      </Profiler>
    );

    expect(container.querySelector('#next-event-title')?.textContent).toBe('Team Sync');
    expect(onRender).toHaveBeenCalledTimes(1);

    // Apply update to an unrelated widget (e.g. weather-1)
    act(() => {
      useDisplayStore.setState((s) => ({
        widgets: {
          ...s.widgets,
          'weather-1': {
            meta: { type: 'weather', origin: [0, 0], dimensions: [2, 2], config: {}, assets_base: '' },
            state: 'ok',
            timestamp: '2026-10-31T10:05:00Z',
            data: { temp: 72 },
          },
        },
      }));
    });

    // onRender must still have been called only once (no re-render for NextUp)
    expect(onRender).toHaveBeenCalledTimes(1);
    expect(container.querySelector('#next-event-title')?.textContent).toBe('Team Sync');
  });

  describe('Multi-Source Live Island Bus (HA Alerts, Timers, Plex, YT Music)', () => {
    beforeEach(() => {
      resetSharedYtMusicState();
      resetSharedPlexSession();
      useDisplayStore.setState({
        header: { timestamp: null, timezone: 'UTC', weather: null },
        screen: { currentScreen: 0, totalScreens: 3, intervalSeconds: 30, widgets: [] },
        layout: [
          { index: 0, widgets: [{ widget_id: 'cal-1', type: 'calendar-grid', origin: [0, 0], dimensions: [3, 2] }] },
          { index: 1, widgets: [{ widget_id: 'ha-1', type: 'home-assistant', origin: [0, 0], dimensions: [3, 2] }] },
          { index: 2, widgets: [{ widget_id: 'plex-1', type: 'plex-recent', origin: [0, 0], dimensions: [3, 2] }] },
        ],
        widgets: {},
      });
    });

    it('formats timer countdowns and extracts HA lock, cover, and timer activities', () => {
      const refNow = new Date('2026-10-31T10:00:00Z');
      expect(formatTimerCountdown({ finishes_at: '2026-10-31T10:05:30Z' }, refNow)).toBe('5:30 left');
      expect(formatTimerCountdown({ finishes_at: '2026-10-31T11:02:05Z' }, refNow)).toBe('1:02:05 left');
      expect(formatTimerCountdown({ remaining: '0:12:09' }, refNow)).toBe('12:09 left');
      expect(formatTimerCountdown({ remaining: '01:15:00' }, refNow)).toBe('01:15:00 left');
      expect(formatTimerCountdown({}, refNow)).toBe('Timer active');

      const activities = selectHomeAssistantLiveActivities(
        {
          'ha-1': {
            state: 'ok',
            timestamp: '2026-10-31T10:00:00Z',
            data: {
              entities: {
                'lock.front_door': {
                  entity_id: 'lock.front_door',
                  state: 'unlocked',
                  attributes: { friendly_name: 'Front Door' },
                },
                'cover.garage_door': {
                  entity_id: 'cover.garage_door',
                  state: 'open',
                  attributes: { friendly_name: 'Garage Door' },
                },
                'timer.kitchen': {
                  entity_id: 'timer.kitchen',
                  state: 'active',
                  attributes: { friendly_name: 'Kitchen Timer', remaining: '0:04:15' },
                },
                'lock.back_door': {
                  entity_id: 'lock.back_door',
                  state: 'locked',
                  attributes: { friendly_name: 'Back Door' },
                },
              },
            },
          },
        },
        refNow,
      );

      expect(activities).toHaveLength(3);
      expect(activities[0]).toMatchObject({
        kind: 'ha-alert',
        entityId: 'lock.front_door',
        title: 'Front Door',
        subtitle: 'Unlocked',
        actionLabel: 'Lock',
      });
      expect(activities[1]).toMatchObject({
        kind: 'ha-alert',
        entityId: 'cover.garage_door',
        title: 'Garage Door',
        subtitle: 'Open',
        actionLabel: 'Close',
      });
      expect(activities[2]).toMatchObject({
        kind: 'ha-timer',
        entityId: 'timer.kitchen',
        title: 'Kitchen Timer',
        subtitle: '4:15 left',
        actionLabel: 'Cancel',
      });
    });

    it('builds HA activities for entities whose attributes arrive as null', () => {
      const refNow = new Date('2026-10-31T10:00:00Z');
      // Go serializes a nil attribute map as null; titles fall back to the entity id.
      const activities = selectHomeAssistantLiveActivities(
        {
          'ha-null': {
            state: 'ok',
            timestamp: '2026-10-31T10:00:00Z',
            data: {
              entities: {
                'lock.side_door': { entity_id: 'lock.side_door', state: 'unlocked', attributes: null },
                'timer.oven': { entity_id: 'timer.oven', state: 'active', attributes: null },
                'sensor.team_tracker_bare': { entity_id: 'sensor.team_tracker_bare', state: 'in', attributes: null },
              },
            },
          },
        },
        refNow,
      );

      expect(activities.slice(0, 2)).toMatchObject([
        { kind: 'ha-alert', entityId: 'lock.side_door', title: 'side door', subtitle: 'Unlocked' },
        { kind: 'ha-timer', entityId: 'timer.oven', title: 'oven', subtitle: 'Timer active' },
      ]);
    });

    it('renders HA alert pill, dispatches 1-tap Lock action with optimistic store update, and navigates on pill click', async () => {
      const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ ok: true }), { status: 200 }));
      vi.stubGlobal('fetch', fetchMock);

      useDisplayStore.setState({
        widgets: {
          'ha-1': {
            state: 'ok',
            timestamp: '2026-10-31T10:00:00Z',
            data: {
              entities: {
                'lock.front_door': {
                  entity_id: 'lock.front_door',
                  state: 'unlocked',
                  attributes: { friendly_name: 'Front Door' },
                },
              },
            },
          },
        },
      });

      const { container } = render(<NextUp now={new Date('2026-10-31T10:00:00Z')} timezone="UTC" />);
      const pill = container.querySelector('#next-event-pill') as HTMLElement;
      expect(pill.getAttribute('data-live-island')).toBe('ha-alert');
      expect(container.querySelector('#live-island-title')?.textContent).toBe('Front Door');
      expect(container.querySelector('#live-island-artist')?.textContent).toBe('Unlocked');

      // Click pill body -> switches to screen 1 (home-assistant)
      fireEvent.click(pill);
      expect(useDisplayStore.getState().screen.currentScreen).toBe(1);

      // Click 1-tap Lock action button -> optimistic update locks entity and dispatches POST /api/widgets/ha-1/action
      const actionBtn = container.querySelector('#live-island-action') as HTMLButtonElement;
      expect(actionBtn.textContent).toBe('Lock');
      fireEvent.click(actionBtn);

      await waitFor(() => {
        expect(fetchMock).toHaveBeenCalledWith(
          '/api/widgets/ha-1/action',
          expect.objectContaining({
            method: 'POST',
            body: JSON.stringify({
              domain: 'lock',
              service: 'lock',
              entity_id: 'lock.front_door',
              service_data: { entity_id: 'lock.front_door' },
            }),
          }),
        );
      });

      // Because optimistic update changed state to 'locked', HA alert disappears
      expect(container.querySelector('#next-event-pill')?.getAttribute('data-live-island')).toBeNull();
      vi.unstubAllGlobals();
    });

    it('prioritizes ha-alert > ha-timer > yt-music > plex and cycles through active Live Island pills', async () => {
      const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ ok: true }), { status: 200 }));
      vi.stubGlobal('fetch', fetchMock);

      useDisplayStore.setState({
        widgets: {
          'ha-1': {
            state: 'ok',
            timestamp: '2026-10-31T10:00:00Z',
            data: {
              entities: {
                'cover.garage': {
                  entity_id: 'cover.garage',
                  state: 'open',
                  attributes: { friendly_name: 'Garage' },
                },
                'timer.oven': {
                  entity_id: 'timer.oven',
                  state: 'active',
                  attributes: { friendly_name: 'Oven Timer', finishes_at: '2026-10-31T10:10:00Z' },
                },
              },
            },
          },
        },
      });

      act(() => {
        setSharedYtMusicState({
          now_playing: { title: 'Starboy', artist: 'The Weeknd', state: 'playing' },
          active_speaker: 'this_device',
          apiBase: '/hearth-api',
        });
        setSharedPlexSession({
          id: 'm-1',
          title: 'Dune: Part Two',
          subtitle: 'Now Playing on TV',
          thumb: 'https://example.com/dune.jpg',
          state: 'playing',
        });
      });

      expect(getSharedPlexSession()?.title).toBe('Dune: Part Two');
      expect(isPlexLiveIslandActive(getSharedPlexSession())).toBe(true);
      expect(resolveScreenIndexForWidgetType('plex-recent')).toBe(2);

      // No-op when setting identical plex session
      act(() => {
        setSharedPlexSession((prev) => (prev ? { ...prev } : null));
      });

      const { container } = render(<NextUp now={new Date('2026-10-31T10:00:00Z')} timezone="UTC" />);

      // 1/4: ha-alert (Garage)
      expect(container.querySelector('#next-event-pill')?.getAttribute('data-live-island')).toBe('ha-alert');
      expect(container.querySelector('#live-island-title')?.textContent).toBe('Garage');
      const cycleBtn = container.querySelector('#live-island-cycle') as HTMLButtonElement;
      expect(cycleBtn).not.toBeNull();
      expect(cycleBtn.textContent).toBe('1/4');

      // Click cycle -> 2/4: ha-timer (Oven Timer)
      fireEvent.click(cycleBtn);
      expect(container.querySelector('#next-event-pill')?.getAttribute('data-live-island')).toBe('ha-timer');
      expect(container.querySelector('#live-island-title')?.textContent).toBe('Oven Timer');
      expect(container.querySelector('#live-island-artist')?.textContent).toBe('10:00 left');
      expect(container.querySelector('#live-island-cycle')?.textContent).toBe('2/4');

      // Click cycle -> 3/4: yt-music (Starboy)
      fireEvent.click(container.querySelector('#live-island-cycle')!);
      expect(container.querySelector('#next-event-pill')?.getAttribute('data-live-island')).toBe('yt-music');
      expect(container.querySelector('#live-island-title')?.textContent).toBe('Starboy');
      expect(container.querySelector('#live-island-cycle')?.textContent).toBe('3/4');

      // Click cycle -> 4/4: plex (Dune: Part Two)
      fireEvent.click(container.querySelector('#live-island-cycle')!);
      expect(container.querySelector('#next-event-pill')?.getAttribute('data-live-island')).toBe('plex');
      expect(container.querySelector('#live-island-title')?.textContent).toBe('Dune: Part Two');
      expect(container.querySelector('#live-island-artist')?.textContent).toBe('Now Playing on TV');
      expect(container.querySelector('#live-island-cycle')?.textContent).toBe('4/4');

      // Click Plex pill -> navigates to screen 2 (plex-recent)
      fireEvent.click(container.querySelector('#next-event-pill')!);
      expect(useDisplayStore.getState().screen.currentScreen).toBe(2);

      // Cycle back to ha-timer (index 1) and click Cancel
      fireEvent.click(container.querySelector('#live-island-cycle')!); // 1/4
      fireEvent.click(container.querySelector('#live-island-cycle')!); // 2/4 (ha-timer)
      fireEvent.click(container.querySelector('#live-island-action')!); // Cancel timer
      await waitFor(() => {
        expect(fetchMock).toHaveBeenCalledWith(
          '/api/widgets/ha-1/action',
          expect.objectContaining({ method: 'POST' }),
        );
      });

      vi.unstubAllGlobals();
    });

    it('filters live sports games by configurable live_island_teams and dispatches 1-tap Watch action', async () => {
      const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ ok: true }), { status: 200 }));
      vi.stubGlobal('fetch', fetchMock);

      seedWidget(
        'ha-1',
        widgetMeta('home-assistant', { live_island_teams: ['MLB:CHC'] }, [3, 2]),
        {
          entities: {
            'sensor.team_tracker_cubs': {
              entity_id: 'sensor.team_tracker_cubs',
              state: 'IN',
              attributes: {
                league: 'MLB',
                sport: 'baseball',
                team_abbr: 'CHC',
                team_name: 'Cubs',
                team_score: 5,
                opponent_abbr: 'LAD',
                opponent_name: 'Dodgers',
                opponent_score: 3,
                clock: 'Bot 7th',
              },
            },
            'sensor.team_tracker_bears': {
              entity_id: 'sensor.team_tracker_bears',
              state: 'IN',
              attributes: {
                league: 'NFL',
                sport: 'football',
                team_abbr: 'CHI',
                team_score: 14,
                opponent_abbr: 'GB',
                opponent_score: 10,
                clock: 'Q3 04:12',
              },
            },
            'sensor.team_tracker_bulls': {
              entity_id: 'sensor.team_tracker_bulls',
              state: 'PRE',
              attributes: {
                league: 'NBA',
                team_abbr: 'CHI',
                opponent_abbr: 'MIA',
              },
            },
          },
        },
        'ok',
      );

      const { container } = render(<NextUp now={new Date('2026-10-31T10:00:00Z')} timezone="UTC" />);
      const pill = container.querySelector('#next-event-pill') as HTMLElement;
      expect(pill.getAttribute('data-live-island')).toBe('ha-sports');
      expect(container.querySelector('#live-island-icon')?.textContent).toBe('⚾');
      expect(container.querySelector('#live-island-title')?.textContent).toBe('CHC 5 · LAD 3');
      expect(container.querySelector('#live-island-artist')?.textContent).toBe('Bot 7th · MLB');
      // Only CHC game matched allowlist, so no cycle button
      expect(container.querySelector('#live-island-cycle')).toBeNull();

      const watchBtn = container.querySelector('#live-island-action') as HTMLButtonElement;
      expect(watchBtn.textContent).toBe('Watch');
      fireEvent.click(watchBtn);

      await waitFor(() => {
        expect(fetchMock).toHaveBeenCalledWith(
          '/api/widgets/ha-1/action',
          expect.objectContaining({
            method: 'POST',
            body: JSON.stringify({
              domain: 'script',
              service: 'turn_on',
              entity_id: 'script.watch_live_sports',
              service_data: {
                entity_id: 'script.watch_live_sports',
                team: 'CHC',
                league: 'MLB',
              },
            }),
          }),
        );
      });

      vi.unstubAllGlobals();
    });

    it('renders inline Play/Pause and Stop controls on Plex Live Island pill and dispatches /plex/control', async () => {
      const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ ok: true }), { status: 200 }));
      vi.stubGlobal('fetch', fetchMock);

      act(() => {
        setSharedPlexSession({
          id: 'rk-42',
          title: 'Interstellar',
          subtitle: 'Living Room TV',
          state: 'playing',
          apiBase: '/hearth-api',
        });
      });

      const { container } = render(<NextUp now={new Date('2026-10-31T10:00:00Z')} timezone="UTC" />);
      const pill = container.querySelector('#next-event-pill') as HTMLElement;
      expect(pill.getAttribute('data-live-island')).toBe('plex');
      expect(pill.className).toContain('is-playing');

      const playPauseBtn = container.querySelector('#live-island-play-pause') as HTMLButtonElement;
      expect(playPauseBtn.getAttribute('aria-label')).toBe('Pause');
      fireEvent.click(playPauseBtn);

      expect(getSharedPlexSession()?.state).toBe('paused');
      expect(pill.className).toContain('is-paused');
      await waitFor(() => {
        expect(fetchMock).toHaveBeenCalledWith(
          '/hearth-api/plex/control',
          expect.objectContaining({
            method: 'POST',
            body: JSON.stringify({ action: 'pause', rating_key: 'rk-42' }),
          }),
        );
      });

      const stopBtn = container.querySelector('#live-island-stop') as HTMLButtonElement;
      expect(stopBtn.getAttribute('aria-label')).toBe('Stop');
      fireEvent.click(stopBtn);

      expect(getSharedPlexSession()).toBeNull();
      await waitFor(() => {
        expect(fetchMock).toHaveBeenCalledWith(
          '/hearth-api/plex/control',
          expect.objectContaining({
            method: 'POST',
            body: JSON.stringify({ action: 'stop', rating_key: 'rk-42' }),
          }),
        );
      });

      vi.unstubAllGlobals();
    });

    it('covers all sports leagues, allowlist patterns, timer formats, and fallback icons', () => {
      const refNow = new Date('2026-10-31T10:00:00Z');
      expect(formatTimerCountdown({ finishes_at: 'invalid-date' }, refNow)).toBe('Timer active');
      expect(formatTimerCountdown({ remaining: '0:04:15.500' }, refNow)).toBe('4:15 left');
      expect(formatTimerCountdown({ remaining: '25.5' }, refNow)).toBe('25 left');

      const activities = selectHomeAssistantLiveActivities(
        {
          'ha-sports': {
            meta: {
              type: 'home-assistant',
              origin: [0, 0],
              dimensions: [3, 2],
              config: { live_island_teams: '*, NFL:GB, NBA:GSW, NHL:SJS, MLS:SJE, TENNIS:FED' },
              assets_base: '',
            },
            state: 'ok',
            timestamp: '2026-10-31T10:00:00Z',
            data: {
              entities: {
                'sensor.nba': {
                  entity_id: 'sensor.nba',
                  state: 'in_progress',
                  attributes: { league: 'NBA', sport: 'basketball', team_abbr: 'GSW', opponent_abbr: 'LAL', quarter: 'Q2' },
                },
                'sensor.nhl': {
                  entity_id: 'sensor.nhl',
                  state: 'live',
                  attributes: { league: 'NHL', sport: 'hockey', team_abbr: 'SJS', opponent_abbr: 'VGK', period: 'P1' },
                },
                'sensor.mls': {
                  entity_id: 'sensor.mls',
                  state: 'in',
                  attributes: { league: 'MLS', sport: 'soccer', team_abbr: 'SJE', opponent_abbr: 'LAFC', inning: '1H' },
                },
                'sensor.tennis': {
                  entity_id: 'sensor.tennis',
                  state: 'in',
                  attributes: { league: 'ATP', sport: 'tennis', team_abbr: 'FED', opponent_abbr: 'NAD' },
                },
              },
            },
          },
        },
        refNow,
      );

      expect(activities.find((a) => a.entityId === 'sensor.nba')?.icon).toBe('🏀');
      expect(activities.find((a) => a.entityId === 'sensor.nhl')?.icon).toBe('🏒');
      expect(activities.find((a) => a.entityId === 'sensor.mls')?.icon).toBe('⚽');
      expect(activities.find((a) => a.entityId === 'sensor.tennis')?.icon).toBe('🏆');
    });
  });
});

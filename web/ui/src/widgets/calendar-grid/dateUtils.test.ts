import { createElement } from 'react';
import { render } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { CalendarWeekView } from './CalendarWeekView';
import {
  addDays,
  addMonths,
  computeDayLayout,
  computeNowIndicatorPct,
  formatDayLabel,
  formatEventTime,
  formatISODate,
  formatModalDateRange,
  formatMonthLabel,
  formatRangeLabel,
  formatShortDate,
  mapTimeMinutesToPct,
  matchesMemberFilter,
  parseISODate,
  parseTimeMinutes,
  sliceWeekWindow,
  stripMealiePrefix,
} from './dateUtils';
import type {
  CollapsedHoursGap,
  FamilyDay,
  FamilySpanEvent,
  FamilyViewModel,
} from './types';
import { itemAt } from '../../test-utils/lookup';

describe('dateUtils', () => {
  it('parses and formats ISO dates', () => {
    const d = parseISODate('2026-10-27');
    expect(d.getFullYear()).toBe(2026);
    expect(d.getMonth()).toBe(9);
    expect(d.getDate()).toBe(27);

    expect(formatISODate(d)).toBe('2026-10-27');
  });

  it('adds days and months correctly', () => {
    const d = parseISODate('2026-10-27');
    const plus3 = addDays(d, 3);
    expect(formatISODate(plus3)).toBe('2026-10-30');

    const nextMonth = addMonths(d, 1);
    expect(formatISODate(nextMonth)).toBe('2026-11-01');
  });

  it('formats short dates and labels', () => {
    expect(formatShortDate('2026-10-27')).toBe('Tue, Oct 27');
    expect(formatShortDate('invalid')).toBe('');
    expect(formatShortDate('')).toBe('');

    const d = parseISODate('2026-10-27');
    expect(formatDayLabel(d)).toBe('Tuesday, Oct 27');
    expect(formatMonthLabel(d)).toBe('October 2026');

    const start = parseISODate('2026-10-25');
    const end = parseISODate('2026-10-31');
    expect(formatRangeLabel(start, end)).toBe('Oct 25 – 31, 2026');

    const endDiffMonth = parseISODate('2026-11-05');
    expect(formatRangeLabel(start, endDiffMonth)).toBe('Oct 25 – Nov 5, 2026');

    const endDiffYear = parseISODate('2027-01-02');
    expect(formatRangeLabel(start, endDiffYear)).toBe('Oct 25, 2026 – Jan 2, 2027');
  });

  it('formats modal date range', () => {
    expect(formatModalDateRange('2026-10-25', '2026-10-31')).toBe('Sun, Oct 25 – Sat, Oct 31');
    expect(formatModalDateRange('2026-10-25', '2026-10-25')).toBe('Sun, Oct 25');
    expect(formatModalDateRange('2026-10-25', undefined)).toBe('Sun, Oct 25');
    expect(formatModalDateRange(undefined, '2026-10-25')).toBe('Sun, Oct 25');
    expect(formatModalDateRange(undefined, undefined)).toBe('');
  });

  it('strips leading Mealie emojis', () => {
    expect(stripMealiePrefix('🍽️ Carnitas Tacos')).toBe('Carnitas Tacos');
    expect(stripMealiePrefix('🍽️🍽️ Double')).toBe('Double');
    expect(stripMealiePrefix('Regular Title')).toBe('Regular Title');
    expect(stripMealiePrefix('')).toBe('');
    expect(stripMealiePrefix(undefined)).toBe('');
  });

  it('slices week window for day and week views', () => {
    const days: FamilyDay[] = [
      { date: '2026-10-25', day_of_month: 25, is_today: false, is_weekend: true },
      { date: '2026-10-26', day_of_month: 26, is_today: false, is_weekend: false },
      { date: '2026-10-27', day_of_month: 27, is_today: true, is_weekend: false },
    ];

    expect(sliceWeekWindow(days, 'week', '2026-10-27')).toEqual(days);
    expect(sliceWeekWindow(days, 'day', '2026-10-26')).toEqual([days[1]]);
    expect(sliceWeekWindow(days, 'day', '2026-10-99')).toEqual([days[0]]);
    expect(sliceWeekWindow([], 'day', '2026-10-27')).toEqual([]);
    expect(sliceWeekWindow(undefined, 'week', '2026-10-27')).toEqual([]);
  });

  it('computes day layout for span events', () => {
    const days: FamilyDay[] = [
      { date: '2026-10-25', day_of_month: 25, is_today: false, is_weekend: true },
      { date: '2026-10-26', day_of_month: 26, is_today: false, is_weekend: false },
      { date: '2026-10-27', day_of_month: 27, is_today: true, is_weekend: false },
    ];
    const spanEvents: FamilySpanEvent[] = [
      { id: '1', title: 'Ev 1', start_col: 2, col_span: 2 },
    ];

    // Week view returns all span events
    expect(computeDayLayout(spanEvents, days, 'week', '2026-10-25')).toEqual(spanEvents);

    // Day 2 (Oct 26 -> col 2): Ev 1 is on cols 2 and 3
    expect(computeDayLayout(spanEvents, days, 'day', '2026-10-26')).toEqual(spanEvents);

    // Day 1 (Oct 25 -> col 1): Ev 1 is not visible
    expect(computeDayLayout(spanEvents, days, 'day', '2026-10-25')).toEqual([]);

    expect(computeDayLayout([], days, 'day', '2026-10-26')).toEqual([]);
    expect(computeDayLayout(undefined, days, 'day', '2026-10-26')).toEqual([]);
  });

  describe('parseTimeMinutes', () => {
    const tests: { name: string; in: string | undefined; want: number | null }[] = [
      { name: '24h midnight', in: '00:00', want: 0 },
      { name: '24h zero-padded hour', in: '08:00', want: 480 },
      { name: '24h single-digit hour', in: '8:00', want: 480 },
      { name: '24h afternoon', in: '14:30', want: 870 },
      { name: '24h last minute', in: '23:59', want: 1439 },
      { name: '24h end of day', in: '24:00', want: 1440 },
      { name: '12h midnight with minutes', in: '12:00 AM', want: 0 },
      { name: '12h midnight bare hour', in: '12 AM', want: 0 },
      { name: '12h bare morning hour', in: '8 AM', want: 480 },
      { name: '12h morning with minutes', in: '8:30 AM', want: 510 },
      { name: '12h noon with minutes', in: '12:00 PM', want: 720 },
      { name: '12h noon bare hour', in: '12 PM', want: 720 },
      { name: '12h afternoon', in: '1:15 PM', want: 795 },
      { name: '12h lowercase pm', in: '11:59 pm', want: 1439 },
      { name: 'empty', in: '', want: null },
      { name: 'whitespace', in: '   ', want: null },
      { name: 'undefined', in: undefined, want: null },
      { name: 'not a time', in: 'invalid', want: null },
      { name: '24h hour out of range', in: '25:00', want: null },
      { name: 'minutes out of range', in: '12:60', want: null },
      { name: '12h hour above 12', in: '13:00 AM', want: null },
      { name: '12h bare zero hour', in: '0 AM', want: null },
      { name: '12h zero hour with minutes', in: '0:00 PM', want: null },
    ];
    for (const tc of tests) {
      it(tc.name, () => {
        expect(parseTimeMinutes(tc.in)).toBe(tc.want);
      });
    }
  });

  describe('computeNowIndicatorPct', () => {
    const baseVM: FamilyViewModel = {
      range_start: '2026-10-25',
      range_end: '2026-10-31',
      range_label: 'Oct 25 – 31, 2026',
      today: '2026-10-27',
      hours_start: '08:00',
      hours_end: '20:00',
    };
    const noHours: FamilyViewModel = { ...baseVM, hours_start: undefined, hours_end: undefined };
    const tests: { name: string; vm: FamilyViewModel | undefined; now: string; tz: string; want: number | null }[] = [
      // 14:00 is halfway between 08:00 (480) and 20:00 (1200).
      { name: 'within hours_start/hours_end', vm: baseVM, now: '2026-10-27T14:00:00Z', tz: 'UTC', want: 50 },
      {
        // 09:00 (540) to 17:00 (1020); 13:00 (780) is 50%.
        name: 'bounds from timeline_markers when hours are missing',
        vm: {
          ...noHours,
          timeline_markers: [
            { label: '09:00', top_pct: 0, is_start: true },
            { label: '17:00', top_pct: 100, is_end: true },
          ],
        },
        now: '2026-10-27T13:00:00Z',
        tz: 'UTC',
        want: 50,
      },
      // 10:00 (600) to 18:00 (1080); 12:00 (720) is 25%.
      {
        name: 'bounds from hour_markers when timeline_markers are missing',
        vm: { ...noHours, hour_markers: ['10:00', '14:00', '18:00'] },
        now: '2026-10-27T12:00:00Z',
        tz: 'UTC',
        want: 25,
      },
      { name: 'default 08:00-20:00 when weekVM is undefined', vm: undefined, now: '2026-10-27T14:00:00Z', tz: 'UTC', want: 50 },
      { name: 'null before the window', vm: baseVM, now: '2026-10-27T07:30:00Z', tz: 'UTC', want: null },
      { name: 'null after the window', vm: baseVM, now: '2026-10-27T20:30:00Z', tz: 'UTC', want: null },
      {
        name: 'null when end <= start',
        vm: { ...baseVM, hours_start: '20:00', hours_end: '08:00' },
        now: '2026-10-27T14:00:00Z',
        tz: 'UTC',
        want: null,
      },
      { name: 'null for an invalid Date', vm: baseVM, now: 'invalid', tz: 'UTC', want: null },
      // 18:00 UTC = 11:00 PDT; (660 - 480) / 720 = 25%.
      { name: 'converts to the given time zone', vm: baseVM, now: '2026-10-27T18:00:00Z', tz: 'America/Los_Angeles', want: 25 },
      {
        // 12:00 is halfway through the symmetric 10:00-14:00 gap in an 08:00-16:00 window.
        name: 'maps onto the compressed timeline when collapsed_gaps is present',
        vm: {
          ...baseVM,
          hours_end: '16:00',
          collapsed_gaps: [{ start_hour: 10, end_hour: 14, label: '10:00 – 14:00', top_pct: 43.01, height_pct: 13.98 }],
        },
        now: '2026-10-27T12:00:00Z',
        tz: 'UTC',
        want: 50,
      },
    ];
    for (const tc of tests) {
      it(tc.name, () => {
        const got = computeNowIndicatorPct(tc.vm, new Date(tc.now), tc.tz);
        if (tc.want === null) expect(got).toBeNull();
        else expect(got).toBeCloseTo(tc.want, 1);
      });
    }

    it('falls back to local time for an invalid time zone instead of throwing', () => {
      expect(() => computeNowIndicatorPct(baseVM, new Date('2026-10-27T18:00:00Z'), 'Invalid/Timezone')).not.toThrow();
    });
  });

  describe('matchesMemberFilter', () => {
    const members = [
      { name: 'Alpha', initial: 'A', color: '#ff6f59', text_color: '#ffffff', pattern: 'solid' },
      { name: 'Beta', initial: 'B', color: '#8b60ed', text_color: '#ffffff', pattern: 'dots' },
    ];
    type Ev = Parameters<typeof matchesMemberFilter>[0];
    const owned: Ev = { id: '1', title: 'Team Meeting', owners: ['Alpha'] };
    const byInitial: Ev = { id: '2', title: 'Coffee', initials: ['A'] };
    const emptyOwners: Ev = { id: '3', title: 'Lunch', owners: [], initials: ['B'] };
    const external: Ev = { id: '4', title: 'External', initials: ['G'] };
    const tests: { name: string; ev: Ev; member: string | null; want: boolean }[] = [
      { name: 'null selection matches everything', ev: { id: '1', title: 'Test' }, member: null, want: true },
      { name: 'empty selection matches everything', ev: { id: '1', title: 'Test' }, member: '', want: true },
      { name: 'owners contain the member', ev: owned, member: 'Alpha', want: true },
      { name: 'owners lack the member', ev: owned, member: 'Beta', want: false },
      { name: 'no owners, initials contain member.initial', ev: byInitial, member: 'Alpha', want: true },
      { name: 'no owners, initials lack member.initial', ev: byInitial, member: 'Beta', want: false },
      { name: 'empty owners, initials contain member.initial', ev: emptyOwners, member: 'Beta', want: true },
      { name: 'empty owners, initials lack member.initial', ev: emptyOwners, member: 'Alpha', want: false },
      { name: 'unknown member falls back to the first letter, matching', ev: external, member: 'Gamma', want: true },
      { name: 'unknown member falls back to the first letter, not matching', ev: external, member: 'Delta', want: false },
      { name: 'neither owners nor initials', ev: { id: '5', title: 'Dentist' }, member: 'Alpha', want: false },
    ];
    for (const tc of tests) {
      it(tc.name, () => {
        expect(matchesMemberFilter(tc.ev, tc.member, members)).toBe(tc.want);
      });
    }
  });

  describe('formatEventTime', () => {
    const tests: { name: string; in?: string; format?: string; want: string }[] = [
      { name: 'no arguments', want: '' },
      { name: 'empty', in: '', want: '' },
      { name: 'whitespace', in: '   ', want: '' },
      { name: 'unparseable input is trimmed', in: '  TBD  ', want: 'TBD' },
      { name: 'unparseable input with 12h', in: 'invalid', format: '12h', want: 'invalid' },
      { name: '12h midnight', in: '00:00', format: '12h', want: '12am' },
      { name: '12h after midnight', in: '00:30', format: '12h', want: '12:30am' },
      { name: '12h morning on the hour', in: '09:00', format: '12h', want: '9am' },
      { name: '12h format is case-insensitive', in: '09:15', format: '12H', want: '9:15am' },
      { name: '12h noon', in: '12:00', format: '12h', want: '12pm' },
      { name: '12h after noon', in: '12:45', format: '12h', want: '12:45pm' },
      { name: '12h evening on the hour', in: '18:00', format: '12h', want: '6pm' },
      { name: '12h evening with minutes', in: '18:30', format: '12h', want: '6:30pm' },
      { name: '12h end of day', in: '24:00', format: '12h', want: '12am' },
      { name: 'default pads 24h', in: '9:05', want: '09:05' },
      { name: 'explicit 24h', in: '18:30', format: '24h', want: '18:30' },
      { name: '12h input to 24h', in: '6:30 PM', format: '24h', want: '18:30' },
      { name: 'default keeps 12h input on the hour', in: '6pm', want: '6pm' },
      { name: 'default keeps 12h input with minutes', in: '6:30pm', want: '6:30pm' },
    ];
    for (const tc of tests) {
      it(tc.name, () => {
        expect(formatEventTime(tc.in, tc.format)).toBe(tc.want);
      });
    }
  });

  describe('mapTimeMinutesToPct', () => {
    // Window 08:00-16:00; the 4 h gap 10:00-14:00 counts as 0.65 h (39 min), so 279 effective minutes.
    const gap: CollapsedHoursGap[] = [{ start_hour: 10, end_hour: 14, label: '10:00 – 14:00', top_pct: 0, height_pct: 0 }];
    // Window 08:00-18:00: active 8-9, 12-14, 17-18 (240) plus two 3 h gaps (78) = 318; 05-07 lies outside.
    const unsorted: CollapsedHoursGap[] = [
      { start_hour: 14, end_hour: 17, label: '14:00 – 17:00', top_pct: 0, height_pct: 0 },
      { start_hour: 5, end_hour: 7, label: '05:00 – 07:00', top_pct: 0, height_pct: 0 },
      { start_hour: 9, end_hour: 12, label: '09:00 – 12:00', top_pct: 0, height_pct: 0 },
    ];
    const tests: { name: string; min: number; start: number; end: number; gaps?: CollapsedHoursGap[]; want: number }[] = [
      { name: 'empty window', min: 500, start: 600, end: 600, want: 0 },
      { name: 'inverted window', min: 500, start: 700, end: 600, want: 0 },
      { name: 'before the window', min: 400, start: 480, end: 1200, want: 0 },
      { name: 'at the window start', min: 480, start: 480, end: 1200, want: 0 },
      { name: 'at the window end', min: 1200, start: 480, end: 1200, want: 100 },
      { name: 'after the window', min: 1300, start: 480, end: 1200, want: 100 },
      { name: 'linear midpoint', min: 840, start: 480, end: 1200, want: 50 },
      { name: 'linear midpoint with empty gaps', min: 840, start: 480, end: 1200, gaps: [], want: 50 },
      { name: 'before the gap', min: 540, start: 480, end: 960, gaps: gap, want: (60 / 279) * 100 },
      { name: 'at the gap start', min: 600, start: 480, end: 960, gaps: gap, want: (120 / 279) * 100 },
      { name: 'halfway inside the gap', min: 720, start: 480, end: 960, gaps: gap, want: 50 },
      { name: 'at the gap end', min: 840, start: 480, end: 960, gaps: gap, want: (159 / 279) * 100 },
      { name: 'after the gap', min: 900, start: 480, end: 960, gaps: gap, want: (219 / 279) * 100 },
      // 13:00: 60 (8-9) + 39 (gap 9-12) + 60 (12-13) = 159 of 318.
      { name: 'sorts out-of-order gaps and ignores out-of-range ones', min: 780, start: 480, end: 1080, gaps: unsorted, want: 50 },
    ];
    for (const tc of tests) {
      it(tc.name, () => {
        expect(mapTimeMinutesToPct(tc.min, tc.start, tc.end, tc.gaps)).toBeCloseTo(tc.want, 2);
      });
    }
  });

  describe('CalendarWeekView collapsed gaps', () => {
    it('renders .cf-collapsed-gap bands in CalendarWeekView only when weekVM.collapsed_gaps is present', () => {
      const days: FamilyDay[] = [
        { date: '2026-10-27', weekday_abbr: 'Tue', day_of_month: 27, is_today: true, is_weekend: false, events: [] },
      ];
      const members = [
        { name: 'Alpha', initial: 'A', color: '#ff6f59', text_color: '#ffffff', pattern: 'solid' },
      ];

      const { container, rerender } = render(
        createElement(CalendarWeekView, {
          activeView: 'week',
          weekVM: {
            range_start: '2026-10-25',
            range_end: '2026-10-31',
            range_label: 'Oct 25 – 31, 2026',
            today: '2026-10-27',
            timeline_markers: [{ label: '9am', top_pct: 0, is_start: true }],
          },
          daysToRender: days,
          visibleAllDayEvents: [],
          members,
          currentDateISO: '2026-10-27',
          onOpenModal: vi.fn(),
        })
      );
      expect(container.querySelectorAll('.cf-collapsed-gap').length).toBe(0);

      rerender(
        createElement(CalendarWeekView, {
          activeView: 'week',
          weekVM: {
            range_start: '2026-10-25',
            range_end: '2026-10-31',
            range_label: 'Oct 25 – 31, 2026',
            today: '2026-10-27',
            time_format: '12h',
            collapsed_gaps: [
              {
                start_hour: 10,
                end_hour: 13,
                label: '10am – 1pm',
                top_pct: 25,
                height_pct: 12,
              },
            ],
          },
          daysToRender: days,
          visibleAllDayEvents: [],
          members,
          currentDateISO: '2026-10-27',
          onOpenModal: vi.fn(),
        })
      );
      const gaps = container.querySelectorAll('.cf-collapsed-gap');
      expect(gaps.length).toBe(1);
      expect(itemAt(gaps, 0).getAttribute('data-gap-start')).toBe('10:00');
      expect(itemAt(gaps, 0).getAttribute('data-gap-end')).toBe('13:00');
      expect(itemAt(gaps, 0).querySelector('.cf-collapsed-gap-label')?.textContent).toBe('10am – 1pm');
    });
  });
});

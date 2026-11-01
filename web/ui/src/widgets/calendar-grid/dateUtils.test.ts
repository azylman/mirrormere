import { describe, expect, it } from 'vitest';
import {
  addDays,
  addMonths,
  buildMonthViewFromDays,
  computeDayLayout,
  formatDayLabel,
  formatISODate,
  formatModalDateRange,
  formatMonthLabel,
  formatRangeLabel,
  formatShortDate,
  parseISODate,
  sliceWeekWindow,
  startOfWeek,
  stripLeadingMealieEmoji,
} from './dateUtils';
import type { FamilyDay, FamilyMonthDay, FamilySpanEvent } from './types';

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

  it('computes startOfWeek', () => {
    // 2026-10-27 is a Tuesday
    const d = parseISODate('2026-10-27');
    const sun = startOfWeek(d, 0); // Sunday
    expect(formatISODate(sun)).toBe('2026-10-25');

    const mon = startOfWeek(d, 1); // Monday
    expect(formatISODate(mon)).toBe('2026-10-26');
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
    expect(stripLeadingMealieEmoji('🍽️ Carnitas Tacos')).toBe('Carnitas Tacos');
    expect(stripLeadingMealieEmoji('🍽️🍽️ Double')).toBe('Double');
    expect(stripLeadingMealieEmoji('Regular Title')).toBe('Regular Title');
    expect(stripLeadingMealieEmoji('')).toBe('');
    expect(stripLeadingMealieEmoji(undefined)).toBe('');
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

  it('builds month view from days in 7-day chunks', () => {
    const monthDays: FamilyMonthDay[] = Array.from({ length: 14 }).map((_, i) => ({
      date: `2026-10-${String(i + 1).padStart(2, '0')}`,
      day_of_month: i + 1,
      is_current_month: true,
      is_today: false,
      is_weekend: false,
    }));

    const weeks = buildMonthViewFromDays(monthDays);
    expect(weeks.length).toBe(2);
    expect(weeks[0].week_number).toBe(1);
    expect(weeks[0].days.length).toBe(7);
    expect(weeks[1].week_number).toBe(2);
    expect(weeks[1].days.length).toBe(7);
  });
});

import { describe, expect, it } from 'vitest';
import {
  calendarFeedActiveCalendars,
  calendarFeedDayGroups,
  escapeHtml,
  formatDate,
  formatTime,
  isValidTimezone,
  relDate,
  tasksStats,
  todayPrecipProb,
  weatherHourlyGraphSvg,
  weatherIconPath,
  weatherIconSvg,
} from './format';

describe('format helpers', () => {
  describe('escapeHtml', () => {
    it('escapes special characters', () => {
      expect(escapeHtml('<script>"alert" & \'test\'</script>')).toBe(
        '&lt;script&gt;&quot;alert&quot; &amp; &#39;test&#39;&lt;/script&gt;',
      );
    });
  });

  describe('tasksStats', () => {
    const fixedNow = new Date('2026-10-31T15:00:00Z');

    it('handles null, undefined, and empty objects', () => {
      expect(tasksStats(null)).toEqual({ total: 0, completed: 0, remaining: 0, overdue: 0 });
      expect(tasksStats(undefined)).toEqual({ total: 0, completed: 0, remaining: 0, overdue: 0 });
      expect(tasksStats({})).toEqual({ total: 0, completed: 0, remaining: 0, overdue: 0 });
      expect(tasksStats([])).toEqual({ total: 0, completed: 0, remaining: 0, overdue: 0 });
    });

    it('calculates metrics from a flat array with snake_case and PascalCase', () => {
      const items = [
        { done: true, due_date: '2026-10-20' }, // completed, past due (not overdue because done)
        { Done: false, DueDate: '2026-10-30' }, // pending, past due (overdue)
        { done: false, due_date: '2026-10-31' }, // pending, due today UTC (not overdue)
        { done: false, due_date: '2026-11-05' }, // pending, future due (not overdue)
        { done: false }, // pending, no due date (not overdue)
      ];

      const res = tasksStats(items, fixedNow);
      expect(res).toEqual({
        total: 5,
        completed: 1,
        remaining: 4,
        overdue: 1,
      });
    });

    it('supports RFC3339 timestamps for overdue checks', () => {
      const items = [
        { done: false, due_date: '2026-10-31T14:00:00Z' }, // 1 hour ago (overdue)
        { done: false, due_date: '2026-10-31T16:00:00Z' }, // 1 hour in future (not overdue)
      ];

      const res = tasksStats(items, fixedNow);
      expect(res.overdue).toBe(1);
    });

    it('extracts items from wrapper objects with items or list properties', () => {
      expect(tasksStats({ items: [{ done: true }] })).toEqual({
        total: 1,
        completed: 1,
        remaining: 0,
        overdue: 0,
      });
      expect(tasksStats({ Items: [{ Done: false }] })).toEqual({
        total: 1,
        completed: 0,
        remaining: 1,
        overdue: 0,
      });
      expect(tasksStats({ list: [{ done: true }] })).toEqual({
        total: 1,
        completed: 1,
        remaining: 0,
        overdue: 0,
      });
    });
  });

  describe('isValidTimezone', () => {
    it('validates IANA timezones', () => {
      expect(isValidTimezone('America/Los_Angeles')).toBe(true);
      expect(isValidTimezone('UTC')).toBe(true);
      expect(isValidTimezone('Europe/London')).toBe(true);
      expect(isValidTimezone('Invalid/Zone')).toBe(false);
      expect(isValidTimezone(null)).toBe(false);
      expect(isValidTimezone('')).toBe(false);
    });
  });

  describe('formatTime', () => {
    it('supports 1-argument formatTime(val) defaulting layout to "15:04"', () => {
      expect(formatTime('2026-09-26T14:05:00Z')).toBe('14:05');
      expect(formatTime('14:05')).toBe('14:05');
    });

    it('supports "3 PM" layout on ISO and RFC3339 strings', () => {
      expect(formatTime('3 PM', '2026-09-26T14:00')).toBe('2 PM');
      expect(formatTime('3 PM', '2026-09-26T15:00')).toBe('3 PM');
      expect(formatTime('3 PM', '2026-09-26T00:00')).toBe('12 AM');
      expect(formatTime('3 PM', '2026-09-26T12:00')).toBe('12 PM');
      expect(formatTime('3 PM', '2026-09-26 15:30:00')).toBe('3 PM');
      expect(formatTime('3 PM', '2026-01-02T18:39:10Z')).toBe('6 PM');
    });

    it('supports "3:04" layout without AM/PM', () => {
      expect(formatTime('3:04', '2026-09-26T09:05:00Z')).toBe('9:05');
      expect(formatTime('3:04', '2026-09-26T14:30:00Z')).toBe('2:30');
      expect(formatTime('3:04', '2026-09-26T00:15:00Z')).toBe('12:15');
    });

    it('supports "PM" layout returning AM or PM', () => {
      expect(formatTime('PM', '2026-09-26T09:05:00Z')).toBe('AM');
      expect(formatTime('PM', '2026-09-26T14:30:00Z')).toBe('PM');
    });

    it('supports "15:04" layout', () => {
      expect(formatTime('15:04', '2026-01-02T18:39:10Z')).toBe('18:39');
      expect(formatTime('15:04', '14:05')).toBe('14:05');
    });

    it('supports timezone conversion for RFC3339/ISO with Z or offset', () => {
      const eventUTC = '2026-10-12T03:30:00Z';
      expect(formatTime('3:04', eventUTC, 'America/Los_Angeles')).toBe('8:30');
      expect(formatTime('PM', eventUTC, 'America/Los_Angeles')).toBe('PM');
      expect(formatTime('15:04', eventUTC, 'America/Los_Angeles')).toBe('20:30');
      expect(formatTime('3 PM', eventUTC, 'America/Los_Angeles')).toBe('8 PM');
    });

    it('returns empty string for invalid input or date-only string', () => {
      expect(formatTime('3 PM', null)).toBe('');
      expect(formatTime('3 PM', undefined)).toBe('');
      expect(formatTime('3 PM', '')).toBe('');
      expect(formatTime('3 PM', '2026-09-26')).toBe('');
      expect(formatTime('3 PM', 'not-a-date')).toBe('');
      expect(formatTime(null)).toBe('');
    });
  });

  describe('formatDate', () => {
    const target = new Date('2026-09-25T14:30:00Z');
    const eventUTC = '2026-10-12T03:30:00Z';

    it('handles 1-arg defaulting to "2006-01-02"', () => {
      expect(formatDate(target)).toBe('2026-09-25');
      expect(formatDate('2026-09-25T14:30:00Z')).toBe('2026-09-25');
    });

    it('supports "Jan 2", "Jan 02, 2006", and "Monday • Jan 2" layouts', () => {
      expect(formatDate('Jan 2', target)).toBe('Sep 25');
      expect(formatDate('Jan 02, 2006', target)).toBe('Sep 25, 2026');
      expect(formatDate('Monday • Jan 2', target)).toBe('Friday • Sep 25');
    });

    it('supports empty or "default" layout', () => {
      expect(formatDate('', target)).toBe('2026-09-25');
      expect(formatDate('default', target)).toBe('2026-09-25');
    });

    it('preserves date-only strings without timezone shifting', () => {
      expect(formatDate('2006-01-02', '2026-10-12', 'America/Los_Angeles')).toBe('2026-10-12');
      expect(formatDate('Jan 2', '2026-10-12', 'America/Los_Angeles')).toBe('Oct 12');
      expect(formatDate('Monday • Jan 2', '2026-10-12', 'America/Los_Angeles')).toBe('Monday • Oct 12');
    });

    it('converts full UTC timestamp to destination timezone', () => {
      expect(formatDate('2006-01-02', eventUTC, 'America/Los_Angeles')).toBe('2026-10-11');
      expect(formatDate('Jan 2', eventUTC, 'America/Los_Angeles')).toBe('Oct 11');
      expect(formatDate('Monday • Jan 2', eventUTC, 'America/Los_Angeles')).toBe('Sunday • Oct 11');
    });

    it('returns raw value for unparseable input', () => {
      expect(formatDate('not-a-date')).toBe('not-a-date');
      expect(formatDate('2006-01-02', 'bad-date')).toBe('bad-date');
    });

    it('returns empty string on empty input', () => {
      expect(formatDate(null)).toBe('');
      expect(formatDate(undefined)).toBe('');
      expect(formatDate('')).toBe('');
    });
  });

  describe('relDate', () => {
    const ref = new Date('2026-09-25T12:00:00Z');
    const eventUTC = '2026-10-12T03:30:00Z'; // 2026-10-11 20:30 in LA

    it('returns relative date strings', () => {
      expect(relDate(ref, ref)).toBe('Today');
      expect(relDate(ref, '2026-09-25')).toBe('Today');
      expect(relDate(ref, '2026-09-26T09:00:00Z')).toBe('Tomorrow');
      expect(relDate(ref, '2026-09-24T23:00:00Z')).toBe('Yesterday');
      expect(relDate(ref, '2026-09-28T10:00:00Z')).toBe('in 3 days');
      expect(relDate(ref, '2026-09-21T10:00:00Z')).toBe('4 days ago');
    });

    it('supports timezone conversions', () => {
      // Sat Oct 10 19:00 in LA -> event is Oct 11 in LA -> Tomorrow
      const satNow = new Date('2026-10-11T02:00:00Z');
      expect(relDate(satNow, eventUTC, 'America/Los_Angeles')).toBe('Tomorrow');

      // Sun Oct 11 14:00 in LA -> event is Oct 11 in LA -> Today
      const sunNow = new Date('2026-10-11T21:00:00Z');
      expect(relDate(sunNow, eventUTC, 'America/Los_Angeles')).toBe('Today');
    });

    it('handles unparseable or empty values', () => {
      expect(relDate(ref, 'invalid')).toBe('invalid');
      expect(relDate(ref, null)).toBe('');
      expect(relDate(ref, undefined)).toBe('');
      expect(relDate(ref, '')).toBe('');
    });
  });

  describe('calendarFeedActiveCalendars', () => {
    it('returns empty array on empty or invalid input', () => {
      expect(calendarFeedActiveCalendars(null)).toEqual([]);
      expect(calendarFeedActiveCalendars({})).toEqual([]);
      expect(calendarFeedActiveCalendars('invalid')).toEqual([]);
    });

    it('filters out calendars where enabled is false or "false"', () => {
      const cfg = {
        calendars: [
          { name: 'Personal', color: '#ff6f59' },
          { name: 'Disabled Feed', color: '#cccccc', enabled: false },
          { name: 'Family', color: '#9d8df1', enabled: true },
          { name: 'Explicit False String', color: '#000000', enabled: 'false' },
        ],
      };
      const active = calendarFeedActiveCalendars(cfg);
      expect(active.map((c) => c.name)).toEqual(['Personal', 'Family']);
    });

    it('supports passing array directly and normalizes enabled boolean', () => {
      const list = [
        { name: 'Feed A', enabled: 'true', color: '#123456' },
        { name: 'Feed B', enabled: false },
      ];
      const active = calendarFeedActiveCalendars(list);
      expect(active).toHaveLength(1);
      expect(active[0]).toEqual({
        name: 'Feed A',
        color: '#123456',
        enabled: true,
      });
    });
  });

  describe('calendarFeedDayGroups', () => {
    const refTime = new Date('2026-10-10T15:30:00Z');

    it('returns empty array on nil or empty data', () => {
      expect(calendarFeedDayGroups(null, refTime)).toEqual([]);
      expect(calendarFeedDayGroups({}, refTime)).toEqual([]);
      expect(calendarFeedDayGroups({ events: [] }, refTime)).toEqual([]);
    });

    it('filters concluded events and groups by calendar date', () => {
      const data = {
        events: [
          { id: '1', title: 'Past Timed', start: '2026-10-09T20:00:00Z', end: '2026-10-09T21:00:00Z' },
          { id: '2', title: 'Past All-Day', start: '2026-10-09', end: '2026-10-09', all_day: true },
          { id: '3', title: 'Earlier Today Concluded', start: '2026-10-10T13:00:00Z', end: '2026-10-10T14:00:00Z' },
          { id: '4', title: 'Currently Ongoing', start: '2026-10-10T15:00:00Z', end: '2026-10-10T16:00:00Z' },
          { id: '5', title: 'Later Today', start: '2026-10-10T18:00:00Z', end: '2026-10-10T19:00:00Z' },
          { id: '6', title: 'Today All-Day', start: '2026-10-10', end: '2026-10-10', all_day: true },
          { id: '7', title: 'Tomorrow All-Day', start: '2026-10-11', end: '2026-10-11', all_day: true },
        ],
      };

      const groups = calendarFeedDayGroups(data, refTime, 'UTC');
      expect(groups.length).toBe(2);
      expect(groups[0].date).toBe('2026-10-10');
      expect(groups[0].rel).toBe('Today');
      expect(groups[0].events.map((e) => e.title)).toEqual([
        'Currently Ongoing',
        'Later Today',
        'Today All-Day',
      ]);

      expect(groups[1].date).toBe('2026-10-11');
      expect(groups[1].rel).toBe('Tomorrow');
      expect(groups[1].events.map((e) => e.title)).toEqual(['Tomorrow All-Day']);
    });
  });

  describe('todayPrecipProb', () => {
    it('returns 0 for empty or invalid data', () => {
      expect(todayPrecipProb(null)).toBe(0);
      expect(todayPrecipProb(undefined)).toBe(0);
      expect(todayPrecipProb({})).toBe(0);
      expect(todayPrecipProb('invalid')).toBe(0);
    });

    it('extracts from daily array', () => {
      expect(todayPrecipProb({ daily: [{ precip_prob_max: 75 }] })).toBe(75);
      expect(todayPrecipProb({ Daily: [{ PrecipProbMax: 60 }] })).toBe(60);
    });

    it('extracts max across first 12 hourly entries when daily is missing', () => {
      const data = {
        hourly: [
          { precip_prob: 10 },
          { precip_prob: 45 },
          { precip_prob: 30 },
          // 13th hour should be ignored
          ...Array.from({ length: 9 }, () => ({ precip_prob: 0 })),
          { precip_prob: 99 },
        ],
      };
      expect(todayPrecipProb(data)).toBe(45);
    });

    it('falls back to current snapshot or direct property', () => {
      expect(todayPrecipProb({ current: { precip_prob_max: 35 } })).toBe(35);
      expect(todayPrecipProb({ Current: { PrecipProbMax: 40 } })).toBe(40);
      expect(todayPrecipProb({ precip_prob_max: 55 })).toBe(55);
    });
  });

  describe('weatherIconPath & weatherIconSvg', () => {
    it('returns valid SVG path for known tokens', () => {
      expect(weatherIconPath('weather-sunny')).toContain('circle');
      expect(weatherIconPath('weather-rainy')).toContain('path');
      expect(weatherIconPath('weather-clear-night')).toContain('M21');
      expect(weatherIconPath('unknown-token')).toContain('M17.5 19H7');
    });

    it('renders scalable SVG clamped to [12, 64]', () => {
      const defaultSvg = weatherIconSvg('weather-sunny');
      expect(defaultSvg).toContain('width="20"');
      expect(defaultSvg).toContain('height="20"');
      expect(defaultSvg).toContain('mm-icon-weather-sunny');

      const smallSvg = weatherIconSvg('weather-sunny', 5);
      expect(smallSvg).toContain('width="12"');

      const largeSvg = weatherIconSvg('weather-sunny', 100);
      expect(largeSvg).toContain('width="64"');

      const customSvg = weatherIconSvg('weather-cloudy', 32);
      expect(customSvg).toContain('width="32"');
    });
  });

  describe('weatherHourlyGraphSvg', () => {
    it('returns empty string on empty or missing points', () => {
      expect(weatherHourlyGraphSvg(null)).toBe('');
      expect(weatherHourlyGraphSvg([])).toBe('');
      expect(weatherHourlyGraphSvg([null])).toBe('');
    });

    it('renders a single point without curve or area path', () => {
      const pts = [{ time: '2026-09-26T14:00', temp: 72.0, precip_prob: 0, icon: 'weather-sunny' }];
      const svg = weatherHourlyGraphSvg(pts, 'test-widget');
      expect(svg).toContain('<svg class="weather-hourly-graph"');
      expect(svg).toContain('Now');
      expect(svg).toContain('72°');
      expect(svg).toContain('id="mm-weather-grad-test-widget"');
      expect(svg).not.toContain('<path d="M');
    });

    it('renders multiple points with cubic Bezier paths and 108px default height', () => {
      const pts = [
        { time: '2026-09-26T14:00', temp: 65.0, precip_prob: 10, icon: 'weather-cloudy' },
        { time: '2026-09-26T15:00', temp: 68.0, precip_prob: 20, icon: 'weather-cloudy' },
        { time: '2026-09-26T16:00', temp: 62.0, precip_prob: 0, icon: 'weather-rainy' },
      ];
      const svg = weatherHourlyGraphSvg(pts);
      expect(svg).toContain('viewBox="0 0 560.0 108.0"');
      expect(svg).toContain('<path d="M');
      expect(svg).toContain('id="mm-weather-hourly-grad"');
      expect(svg).toContain('Now');
      expect(svg).toContain('3 PM');
      expect(svg).toContain('4 PM');
      expect(svg).toContain('☂ 10%');
      expect(svg).toContain('☂ 20%');
    });

    it('caps rendering to the first 8 points', () => {
      const pts = Array.from({ length: 12 }, (_, i) => ({
        time: `2026-09-26T${String(12 + i).padStart(2, '0')}:00`,
        temp: 70 + i,
        icon: 'weather-sunny',
      }));
      const svg = weatherHourlyGraphSvg(pts);
      // Point 7 is 7 PM (19:00), Point 8 (20:00 = 8 PM) is the 9th item and should not be present
      expect(svg).toContain('7 PM');
      expect(svg).not.toContain('8 PM');
    });

    it('supports 300px height mode when passed via options', () => {
      const pts = [
        { time: '2026-09-26T12:00', temp: 80.0, precip_prob: 40, icon: 'weather-rainy' },
        { time: '2026-09-26T13:00', temp: 50.0, precip_prob: 0, icon: 'weather-cloudy' },
      ];
      const svg = weatherHourlyGraphSvg(pts, undefined, {
        svgHeight: 300.0,
        curveTop: 90.0,
        curveBottom: 205.0,
        baseY: 240.0,
      });
      expect(svg).toContain('viewBox="0 0 560.0 300.0"');
      expect(svg).toContain('y1="240.0"');
      expect(svg).toContain('y2="240.0"');
      expect(svg).toContain('y="275"');
    });
  });
});

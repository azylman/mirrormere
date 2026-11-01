import { describe, expect, it } from 'vitest';
import {
  escapeHtml,
  formatTime,
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

  describe('formatTime', () => {
    it('supports "3 PM" layout on ISO and RFC3339 strings', () => {
      expect(formatTime('3 PM', '2026-09-26T14:00')).toBe('2 PM');
      expect(formatTime('3 PM', '2026-09-26T15:00')).toBe('3 PM');
      expect(formatTime('3 PM', '2026-09-26T00:00')).toBe('12 AM');
      expect(formatTime('3 PM', '2026-09-26T12:00')).toBe('12 PM');
      expect(formatTime('3 PM', '2026-09-26 15:30:00')).toBe('3 PM');
      expect(formatTime('3 PM', '2026-01-02T18:39:10Z')).toBe('6 PM');
    });

    it('supports "15:04" layout', () => {
      expect(formatTime('15:04', '2026-01-02T18:39:10Z')).toBe('18:39');
      expect(formatTime('15:04', '14:05')).toBe('14:05');
    });

    it('returns empty string for invalid input or date-only string', () => {
      expect(formatTime('3 PM', null)).toBe('');
      expect(formatTime('3 PM', undefined)).toBe('');
      expect(formatTime('3 PM', '')).toBe('');
      expect(formatTime('3 PM', '2026-09-26')).toBe('');
      expect(formatTime('3 PM', 'not-a-date')).toBe('');
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

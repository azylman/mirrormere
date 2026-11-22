import { describe, expect, it } from 'vitest';
import {
  calendarFeedActiveCalendars,
  calendarFeedDayGroups,
  computeHourlyGraphLayout,
  escapeHtml,
  formatDate,
  formatTime,
  isValidTimezone,
  relDate,
  tasksStats,
  todayPrecipProb,
  type TasksStatsResult,
} from './format';
import { type CalendarFeedActiveCalendar } from './formatCalendar';
import { normalizeMonthDay } from './formatDate';
import { itemAt } from '../test-utils/lookup';

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
    const zero = { total: 0, completed: 0, remaining: 0, overdue: 0 };
    const tests: { name: string; input: unknown; want: TasksStatsResult }[] = [
      { name: 'null', input: null, want: zero },
      { name: 'undefined', input: undefined, want: zero },
      { name: 'empty object', input: {}, want: zero },
      { name: 'empty array', input: [], want: zero },
      {
        name: 'flat array with snake_case and PascalCase; done and due-today items are not overdue',
        input: [
          { done: true, due_date: '2026-10-20' },
          { Done: false, DueDate: '2026-10-30' },
          { done: false, due_date: '2026-10-31' },
          { done: false, due_date: '2026-11-05' },
          { done: false },
        ],
        want: { total: 5, completed: 1, remaining: 4, overdue: 1 },
      },
      {
        name: 'RFC3339 due timestamps compare against now',
        input: [
          { done: false, due_date: '2026-10-31T14:00:00Z' },
          { done: false, due_date: '2026-10-31T16:00:00Z' },
        ],
        want: { total: 2, completed: 0, remaining: 2, overdue: 1 },
      },
      { name: 'items wrapper', input: { items: [{ done: true }] }, want: { total: 1, completed: 1, remaining: 0, overdue: 0 } },
      { name: 'Items wrapper', input: { Items: [{ Done: false }] }, want: { total: 1, completed: 0, remaining: 1, overdue: 0 } },
      { name: 'list wrapper', input: { list: [{ done: true }] }, want: { total: 1, completed: 1, remaining: 0, overdue: 0 } },
    ];
    for (const tc of tests) {
      it(tc.name, () => {
        expect(tasksStats(tc.input, fixedNow)).toEqual(tc.want);
      });
    }
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
    const eventUTC = '2026-10-12T03:30:00Z';
    const date = new Date(eventUTC);
    const timestamp = date.getTime();
    const tests: { name: string; args: [unknown, unknown?, string?]; want: unknown }[] = [
      { name: '1-arg ISO defaults to 15:04', args: ['2026-09-26T14:05:00Z'], want: '14:05' },
      { name: '1-arg HH:MM defaults to 15:04', args: ['14:05'], want: '14:05' },
      { name: '3 PM: afternoon', args: ['3 PM', '2026-09-26T14:00'], want: '2 PM' },
      { name: '3 PM: on the hour', args: ['3 PM', '2026-09-26T15:00'], want: '3 PM' },
      { name: '3 PM: midnight', args: ['3 PM', '2026-09-26T00:00'], want: '12 AM' },
      { name: '3 PM: noon', args: ['3 PM', '2026-09-26T12:00'], want: '12 PM' },
      { name: '3 PM: space-separated datetime', args: ['3 PM', '2026-09-26 15:30:00'], want: '3 PM' },
      { name: '3 PM: RFC3339 Z', args: ['3 PM', '2026-01-02T18:39:10Z'], want: '6 PM' },
      { name: '3:04: morning', args: ['3:04', '2026-09-26T09:05:00Z'], want: '9:05' },
      { name: '3:04: afternoon without AM/PM', args: ['3:04', '2026-09-26T14:30:00Z'], want: '2:30' },
      { name: '3:04: after midnight', args: ['3:04', '2026-09-26T00:15:00Z'], want: '12:15' },
      { name: 'PM: morning', args: ['PM', '2026-09-26T09:05:00Z'], want: 'AM' },
      { name: 'PM: afternoon', args: ['PM', '2026-09-26T14:30:00Z'], want: 'PM' },
      { name: '15:04: RFC3339', args: ['15:04', '2026-01-02T18:39:10Z'], want: '18:39' },
      { name: '15:04: bare HH:MM', args: ['15:04', '14:05'], want: '14:05' },
      { name: 'tz conversion: 3:04', args: ['3:04', eventUTC, 'America/Los_Angeles'], want: '8:30' },
      { name: 'tz conversion: PM', args: ['PM', eventUTC, 'America/Los_Angeles'], want: 'PM' },
      { name: 'tz conversion: 15:04', args: ['15:04', eventUTC, 'America/Los_Angeles'], want: '20:30' },
      { name: 'tz conversion: 3 PM', args: ['3 PM', eventUTC, 'America/Los_Angeles'], want: '8 PM' },
      { name: '3:04 PM: afternoon', args: ['3:04 PM', '2026-09-26T14:30:00Z'], want: '2:30 PM' },
      { name: '3:04 PM: after midnight', args: ['3:04 PM', '2026-09-26T00:05:00Z'], want: '12:05 AM' },
      { name: '3:04 PM: noon', args: ['3:04 PM', '2026-09-26T12:00:00Z'], want: '12:00 PM' },
      { name: 'Date input with valid tz', args: ['3:04 PM', date, 'America/Los_Angeles'], want: '8:30 PM' },
      { name: 'Date input with invalid tz falls back to local', args: ['15:04', date, 'Invalid/Timezone'], want: expect.stringMatching(/^\d{2}:\d{2}$/) },
      { name: 'invalid Date', args: ['15:04', new Date(NaN)], want: '' },
      { name: 'invalid Date with 3:04 PM layout', args: ['3:04 PM', new Date(NaN)], want: '' },
      { name: 'numeric timestamp with valid tz', args: ['3:04 PM', timestamp, 'America/Los_Angeles'], want: '8:30 PM' },
      { name: 'numeric timestamp with invalid tz falls back to local', args: ['15:04', timestamp, 'Invalid/Timezone'], want: expect.stringMatching(/^\d{2}:\d{2}$/) },
      { name: 'NaN timestamp', args: ['15:04', NaN], want: '' },
      { name: 'zoneless full date string with tz', args: ['3:04 PM', '2026-10-12 15:30:00', 'America/Los_Angeles'], want: '3:30 PM' },
      { name: 'object input', args: ['15:04', { foo: 'bar' }], want: '' },
      { name: 'offset without tz keeps wall time', args: ['15:04', '2026-10-27T15:30:00+02:00'], want: '15:30' },
      { name: 'offset converted to tz', args: ['15:04', '2026-10-27T15:30:00+02:00', 'UTC'], want: '13:30' },
      { name: 'out-of-range ISO string with Z', args: ['15:04', '99999-99-99T99:99:99Z'], want: '' },
      { name: 'non-ISO string with tz', args: ['15:04', 'October 27, 2026 15:30:00 GMT', 'America/New_York'], want: '11:30' },
      { name: 'bare HH:MM out of range', args: ['15:04', '25:70'], want: '' },
      { name: 'null value', args: ['3 PM', null], want: '' },
      { name: 'undefined value', args: ['3 PM', undefined], want: '' },
      { name: 'empty value', args: ['3 PM', ''], want: '' },
      { name: 'date-only string', args: ['3 PM', '2026-09-26'], want: '' },
      { name: 'unparseable string', args: ['3 PM', 'not-a-date'], want: '' },
      { name: '1-arg null', args: [null], want: '' },
    ];
    for (const tc of tests) {
      it(tc.name, () => {
        expect(formatTime(...tc.args)).toEqual(tc.want);
      });
    }
  });

  describe('normalizeMonthDay', () => {
    const tests: { name: string; raw: unknown; want: string | undefined }[] = [
      { name: 'full ISO date', raw: '2026-03-07', want: '03-07' },
      { name: 'full date with single digits', raw: '2026-3-7', want: '03-07' },
      { name: 'month-day with dash', raw: '3-7', want: '03-07' },
      { name: 'month/day with slash and padding', raw: ' 12/25 ', want: '12-25' },
      { name: 'already padded', raw: '01-02', want: '01-02' },
      { name: 'unrecognized string', raw: 'March 7', want: undefined },
      { name: 'empty string', raw: '', want: undefined },
      { name: 'non-string', raw: 307, want: undefined },
    ];
    for (const tc of tests) {
      it(tc.name, () => {
        expect(normalizeMonthDay(tc.raw)).toBe(tc.want);
      });
    }
  });

  describe('formatDate', () => {
    const target = new Date('2026-09-25T14:30:00Z');
    const eventUTC = '2026-10-12T03:30:00Z';
    const date = new Date(eventUTC);
    const timestamp = date.getTime();
    const tests: { name: string; args: [unknown, unknown?, string?]; want: string }[] = [
      { name: '1-arg Date defaults to 2006-01-02', args: [target], want: '2026-09-25' },
      { name: '1-arg ISO string defaults to 2006-01-02', args: ['2026-09-25T14:30:00Z'], want: '2026-09-25' },
      { name: 'Jan 2 layout', args: ['Jan 2', target], want: 'Sep 25' },
      { name: 'Jan 02, 2006 layout', args: ['Jan 02, 2006', target], want: 'Sep 25, 2026' },
      { name: 'Monday • Jan 2 layout', args: ['Monday • Jan 2', target], want: 'Friday • Sep 25' },
      { name: '2006 layout', args: ['2006', target], want: '2026' },
      { name: 'empty layout uses default', args: ['', target], want: '2026-09-25' },
      { name: '"default" layout uses default', args: ['default', target], want: '2026-09-25' },
      { name: 'unknown layout falls back to raw value', args: ['unknown-layout', '2026-10-27'], want: '2026-10-27' },
      { name: 'Date input with valid tz', args: ['2006-01-02', date, 'America/Los_Angeles'], want: '2026-10-11' },
      { name: 'Date input with invalid tz', args: ['2006-01-02', date, 'Invalid/Timezone'], want: '2026-10-12' },
      { name: 'invalid Date', args: ['2006-01-02', new Date(NaN)], want: 'Invalid Date' },
      { name: 'numeric timestamp with valid tz', args: ['2006-01-02', timestamp, 'America/Los_Angeles'], want: '2026-10-11' },
      { name: 'numeric timestamp with invalid tz', args: ['2006-01-02', timestamp, 'Invalid/Timezone'], want: '2026-10-12' },
      { name: 'numeric timestamp with UTC', args: ['2006-01-02', 1761566400000, 'UTC'], want: '2025-10-27' },
      { name: 'NaN timestamp', args: ['2006-01-02', NaN], want: 'NaN' },
      { name: 'zoneless datetime string stays local', args: ['2006-01-02', '2026-10-12T15:30:00'], want: '2026-10-12' },
      { name: 'unknown object type', args: ['2006-01-02', { foo: 'bar' }], want: '[object Object]' },
      { name: 'boolean value', args: ['2006-01-02', true], want: 'true' },
      { name: 'date-only string not shifted: 2006-01-02', args: ['2006-01-02', '2026-10-12', 'America/Los_Angeles'], want: '2026-10-12' },
      { name: 'date-only string not shifted: Jan 2', args: ['Jan 2', '2026-10-12', 'America/Los_Angeles'], want: 'Oct 12' },
      { name: 'date-only string not shifted: Monday • Jan 2', args: ['Monday • Jan 2', '2026-10-12', 'America/Los_Angeles'], want: 'Monday • Oct 12' },
      { name: 'UTC timestamp converted to tz: 2006-01-02', args: ['2006-01-02', eventUTC, 'America/Los_Angeles'], want: '2026-10-11' },
      { name: 'UTC timestamp converted to tz: Jan 2', args: ['Jan 2', eventUTC, 'America/Los_Angeles'], want: 'Oct 11' },
      { name: 'UTC timestamp converted to tz: Monday • Jan 2', args: ['Monday • Jan 2', eventUTC, 'America/Los_Angeles'], want: 'Sunday • Oct 11' },
      { name: '1-arg unparseable returns raw', args: ['not-a-date'], want: 'not-a-date' },
      { name: 'unparseable returns raw', args: ['2006-01-02', 'bad-date'], want: 'bad-date' },
      { name: 'null', args: [null], want: '' },
      { name: 'undefined', args: [undefined], want: '' },
      { name: 'empty string', args: [''], want: '' },
    ];
    for (const tc of tests) {
      it(tc.name, () => {
        expect(formatDate(...tc.args)).toBe(tc.want);
      });
    }
  });

  describe('relDate', () => {
    const ref = new Date('2026-09-25T12:00:00Z');
    const eventUTC = '2026-10-12T03:30:00Z'; // 2026-10-11 20:30 in LA
    const tests: { name: string; ref: Date | null; value: unknown; tz?: string; want: string }[] = [
      { name: 'same Date is Today', ref, value: ref, want: 'Today' },
      { name: 'date-only today', ref, value: '2026-09-25', want: 'Today' },
      { name: 'Tomorrow', ref, value: '2026-09-26T09:00:00Z', want: 'Tomorrow' },
      { name: 'Yesterday', ref, value: '2026-09-24T23:00:00Z', want: 'Yesterday' },
      { name: 'in N days', ref, value: '2026-09-28T10:00:00Z', want: 'in 3 days' },
      { name: 'N days ago', ref, value: '2026-09-21T10:00:00Z', want: '4 days ago' },
      { name: 'Yesterday with UTC tz', ref: new Date('2026-10-27T12:00:00Z'), value: '2026-10-26T12:00:00Z', tz: 'UTC', want: 'Yesterday' },
      { name: 'N days ago with UTC tz', ref: new Date('2026-10-27T12:00:00Z'), value: '2026-10-23T12:00:00Z', tz: 'UTC', want: '4 days ago' },
      { name: 'tz: Saturday evening LA sees Sunday event as Tomorrow', ref: new Date('2026-10-11T02:00:00Z'), value: eventUTC, tz: 'America/Los_Angeles', want: 'Tomorrow' },
      { name: 'tz: Sunday afternoon LA sees event as Today', ref: new Date('2026-10-11T21:00:00Z'), value: eventUTC, tz: 'America/Los_Angeles', want: 'Today' },
      { name: 'unparseable value returned raw', ref, value: 'invalid', want: 'invalid' },
      { name: 'null value', ref, value: null, want: '' },
      { name: 'undefined value', ref, value: undefined, want: '' },
      { name: 'empty value', ref, value: '', want: '' },
      { name: 'null ref returns raw value', ref: null, value: '2026-10-27', want: '2026-10-27' },
      { name: 'invalid ref returns raw value', ref: new Date(NaN), value: '2026-10-27', want: '2026-10-27' },
    ];
    for (const tc of tests) {
      it(tc.name, () => {
        expect(relDate(tc.ref, tc.value, tc.tz)).toBe(tc.want);
      });
    }
  });

  describe('calendarFeedActiveCalendars', () => {
    const tests: { name: string; input: unknown; want: CalendarFeedActiveCalendar[] }[] = [
      { name: 'null', input: null, want: [] },
      { name: 'empty object', input: {}, want: [] },
      { name: 'non-object', input: 'invalid', want: [] },
      {
        name: 'drops calendars with enabled false or "false"',
        input: {
          calendars: [
            { name: 'Personal', color: '#ff6f59' },
            { name: 'Disabled Feed', color: '#cccccc', enabled: false },
            { name: 'Family', color: '#9d8df1', enabled: true },
            { name: 'Explicit False String', color: '#000000', enabled: 'false' },
          ],
        },
        want: [
          { name: 'Personal', color: '#ff6f59', enabled: true },
          { name: 'Family', color: '#9d8df1', enabled: true },
        ],
      },
      {
        name: 'direct array normalizes enabled "true"',
        input: [
          { name: 'Feed A', enabled: 'true', color: '#123456' },
          { name: 'Feed B', enabled: false },
        ],
        want: [{ name: 'Feed A', color: '#123456', enabled: true }],
      },
      {
        name: 'skips non-objects and defaults nullish name and color',
        input: [null, { name: null, color: null, enabled: true }, { name: 'alpha', color: '#ff0000', enabled: true }, { name: 'beta', enabled: true }],
        want: [
          { name: '', color: undefined, enabled: true },
          { name: 'alpha', color: '#ff0000', enabled: true },
          { name: 'beta', color: undefined, enabled: true },
        ],
      },
    ];
    for (const tc of tests) {
      it(tc.name, () => {
        expect(calendarFeedActiveCalendars(tc.input)).toEqual(tc.want);
      });
    }
  });

  describe('calendarFeedDayGroups', () => {
    const refTime = new Date('2026-10-10T15:30:00Z');
    const laterRef = new Date('2026-10-27T12:00:00Z');
    const morningRef = new Date('2026-10-27T08:00:00Z');
    // want is matched with toMatchObject: group/event arrays must match in length, listed fields must match.
    const tests: { name: string; data: unknown; ref: Date; tz?: string; want: Record<string, unknown>[] }[] = [
      { name: 'null data', data: null, ref: refTime, want: [] },
      { name: 'empty object', data: {}, ref: refTime, want: [] },
      { name: 'empty events', data: { events: [] }, ref: refTime, want: [] },
      { name: 'null events', data: { events: null }, ref: laterRef, want: [] },
      { name: 'event without title or start', data: { events: [{ title: '', start: '' }] }, ref: laterRef, want: [] },
      {
        name: 'drops concluded events and groups by calendar date',
        data: {
          events: [
            { id: '1', title: 'Past Timed', start: '2026-10-09T20:00:00Z', end: '2026-10-09T21:00:00Z' },
            { id: '2', title: 'Past All-Day', start: '2026-10-09', end: '2026-10-09', all_day: true },
            { id: '3', title: 'Earlier Today Concluded', start: '2026-10-10T13:00:00Z', end: '2026-10-10T14:00:00Z' },
            { id: '4', title: 'Currently Ongoing', start: '2026-10-10T15:00:00Z', end: '2026-10-10T16:00:00Z' },
            { id: '5', title: 'Later Today', start: '2026-10-10T18:00:00Z', end: '2026-10-10T19:00:00Z' },
            { id: '6', title: 'Today All-Day', start: '2026-10-10', end: '2026-10-10', all_day: true },
            { id: '7', title: 'Ongoing No End', start: '2026-10-10T15:00:00Z' },
            { id: '8', title: 'Past No End', start: '2026-10-10T13:00:00Z' },
            { id: '9', title: 'Tomorrow All-Day', start: '2026-10-11', end: '2026-10-11', all_day: true },
          ],
        },
        ref: refTime,
        tz: 'UTC',
        want: [
          {
            date: '2026-10-10',
            rel: 'Today',
            events: [{ title: 'Currently Ongoing' }, { title: 'Later Today' }, { title: 'Today All-Day' }, { title: 'Ongoing No End' }],
          },
          { date: '2026-10-11', rel: 'Tomorrow', events: [{ title: 'Tomorrow All-Day' }] },
        ],
      },
      {
        name: 'drops past all-day, timed and endless events',
        data: {
          events: [
            { title: 'Past All Day', start: '2026-10-20', all_day: true },
            { title: 'Past Timed', start: '2026-10-20T10:00:00Z', end: '2026-10-20T11:00:00Z' },
            { title: 'Past No End', start: '2026-10-20T10:00:00Z' },
            { ID: 'multi-1', Title: 'Today Event 1', Start: '2026-10-27T14:00:00Z', Location: 'Main Room', Description: 'Notes', CalendarName: 'Shared', Color: '#fff' },
            { ID: 'multi-2', Title: 'Today Event 2', Start: '2026-10-27T16:00:00Z' },
          ],
        },
        ref: laterRef,
        tz: 'UTC',
        want: [{ events: [{ title: 'Today Event 1', location: 'Main Room' }, { title: 'Today Event 2' }] }],
      },
      {
        name: 'normalizes camelCase and PascalCase descriptions on all-day and timed events',
        data: {
          events: [
            { id: 'ev-1', title: 'All Day With Desc', start: '2026-10-10', all_day: true, description: 'Important details' },
            { id: 'ev-2', title: 'Timed With Pascal Desc', start: '2026-10-10T16:00:00Z', end: '2026-10-10T17:00:00Z', Description: 'Meeting agenda and notes' },
            { id: 'ev-3', title: 'Timed Without Desc', start: '2026-10-10T18:00:00Z', end: '2026-10-10T19:00:00Z' },
          ],
        },
        ref: refTime,
        tz: 'UTC',
        want: [
          {
            events: [
              { description: 'Important details' },
              { description: 'Meeting agenda and notes' },
              { description: undefined },
            ],
          },
        ],
      },
      {
        name: 'accepts a direct array and skips null entries',
        data: [{ title: 'Array Event', start: '2026-10-27T10:00:00Z', end: '2026-10-27T11:00:00Z' }, null],
        ref: morningRef,
        tz: 'UTC',
        want: [{ events: [{ title: 'Array Event' }] }],
      },
      {
        name: 'reads PascalCase event fields',
        data: {
          events: [
            {
              Title: 'Pascal Title',
              ID: 'pascal-id-1',
              Location: 'Meeting Room A',
              Description: 'Sync demo',
              CalendarName: 'Work',
              Color: '#3b82f6',
              Start: '2026-10-27T14:00:00Z',
              End: '2026-10-27T15:00:00Z',
            },
          ],
        },
        ref: morningRef,
        tz: 'UTC',
        want: [
          {
            events: [
              { title: 'Pascal Title', id: 'pascal-id-1', location: 'Meeting Room A', description: 'Sync demo', calendarName: 'Work', color: '#3b82f6' },
            ],
          },
        ],
      },
    ];
    for (const tc of tests) {
      it(tc.name, () => {
        expect(calendarFeedDayGroups(tc.data, tc.ref, tc.tz)).toMatchObject(tc.want);
      });
    }
  });

  describe('todayPrecipProb', () => {
    const tests: { name: string; data: unknown; want: number }[] = [
      { name: 'null', data: null, want: 0 },
      { name: 'undefined', data: undefined, want: 0 },
      { name: 'empty object', data: {}, want: 0 },
      { name: 'non-object', data: 'invalid', want: 0 },
      { name: 'daily precip_prob_max', data: { daily: [{ precip_prob_max: 75 }] }, want: 75 },
      { name: 'Daily PrecipProbMax', data: { Daily: [{ PrecipProbMax: 60 }] }, want: 60 },
      { name: 'daily precip_prob', data: { daily: [{ precip_prob: 42 }] }, want: 42 },
      { name: 'daily PrecipitationProbability', data: { daily: [{ PrecipitationProbability: 55 }] }, want: 55 },
      {
        name: 'hourly max over the first 12 entries only',
        data: {
          hourly: [
            { precip_prob: 10 },
            { precip_prob: 45 },
            { precip_prob: 30 },
            ...Array.from({ length: 9 }, () => ({ precip_prob: 0 })),
            { precip_prob: 99 },
          ],
        },
        want: 45,
      },
      { name: 'hourly PrecipitationProbability', data: { hourly: [{ PrecipitationProbability: 65 }] }, want: 65 },
      { name: 'Hourly PrecipProb', data: { Hourly: [{ PrecipProb: 50 }] }, want: 50 },
      { name: 'current precip_prob_max', data: { current: { precip_prob_max: 35 } }, want: 35 },
      { name: 'Current PrecipProbMax', data: { Current: { PrecipProbMax: 40 } }, want: 40 },
      { name: 'current PrecipitationProbability', data: { current: { PrecipitationProbability: 75 } }, want: 75 },
      { name: 'top-level precip_prob_max', data: { precip_prob_max: 55 }, want: 55 },
    ];
    for (const tc of tests) {
      it(tc.name, () => {
        expect(todayPrecipProb(tc.data)).toBe(tc.want);
      });
    }
  });

  describe('computeHourlyGraphLayout', () => {
    it('returns null on empty or missing points', () => {
      expect(computeHourlyGraphLayout(null)).toBeNull();
      expect(computeHourlyGraphLayout([])).toBeNull();
      expect(computeHourlyGraphLayout([null])).toBeNull();
    });

    it('computes layout for a single point without curve or area path', () => {
      const pts = [{ time: '2026-09-26T14:00', temp: 72.0, precip_prob: 0, icon: 'weather-sunny' }];
      const layout = computeHourlyGraphLayout(pts, 'test-widget');
      expect(layout).not.toBeNull();
      expect(layout?.totalWidth).toBe(560.0);
      expect(layout?.svgHeight).toBe(270.0);
      expect(layout?.baseY).toBe(238.0);
      expect(layout?.gradientId).toBe('mm-weather-grad-test-widget');
      expect(layout?.curvePath).toBe('');
      expect(layout?.areaPath).toBe('');
      expect(layout?.points).toHaveLength(1);
      expect(itemAt(layout?.points, 0).tempRound).toBe(72);
      expect(itemAt(layout?.points, 0).timeLabel).toBe('Now');
      expect(itemAt(layout?.points, 0).icon).toBe('weather-sunny');

      const invalidTempLayout = computeHourlyGraphLayout([{ time: '2026-09-26T14:00', temp: NaN }]);
      expect(itemAt(invalidTempLayout?.points, 0).tempRound).toBe(0);
    });

    it('computes layout for multiple points with cubic Bezier paths and 270px default height', () => {
      const pts = [
        { time: '2026-09-26T14:00', temp: 65.0, precip_prob: 10, icon: 'weather-cloudy' },
        { time: '2026-09-26T15:00', temp: 68.0, precip_prob: 20, icon: 'weather-cloudy' },
        { time: '2026-09-26T16:00', temp: 62.0, precip_prob: 0, icon: 'weather-rainy' },
      ];
      const layout = computeHourlyGraphLayout(pts);
      expect(layout).not.toBeNull();
      expect(layout?.totalWidth).toBe(560.0);
      expect(layout?.svgHeight).toBe(270.0);
      expect(layout?.baseY).toBe(238.0);
      expect(layout?.gradientId).toBe('mm-weather-hourly-grad');
      expect(layout?.curvePath).toContain('M 18.0 144.0');
      // Verify cubic Bezier control points: C (x0+dx, y0), (x1-dx, y1), (x1, y1)
      expect(layout?.curvePath).toContain('C 149.0 144.0, 149.0 76.0, 280.0 76.0');
      expect(layout?.areaPath).toContain('M 18.0 238.0 L 18.0 144.0');
      expect(layout?.gridLinesPath).toContain('M 18.0 150.0 L 18.0 238.0');
      expect(layout?.points).toHaveLength(3);
      expect(itemAt(layout?.points, 0).timeLabel).toBe('Now');
      expect(itemAt(layout?.points, 1).timeLabel).toBe('3 PM');
      expect(itemAt(layout?.points, 2).timeLabel).toBe('4 PM');
      expect(itemAt(layout?.points, 0).precipProb).toBe(10);
      expect(itemAt(layout?.points, 1).precipProb).toBe(20);
      expect(itemAt(layout?.points, 2).precipProb).toBe(0);
    });

    it('computes layout for 24 hourly points capped to 10 points by default, and preserves 24 points when maxPoints is 24', () => {
      const pts = Array.from({ length: 24 }, (_, i) => ({
        time: `2026-09-26T${String(i).padStart(2, '0')}:00`,
        temp: 55 + (i % 10),
        icon: 'weather-sunny',
      }));
      const layout = computeHourlyGraphLayout(pts);
      expect(layout).not.toBeNull();
      expect(layout?.totalWidth).toBe(560.0);
      expect(layout?.points).toHaveLength(10);
      expect(itemAt(layout?.points, 0).timeLabel).toBe('Now');
      expect(itemAt(layout?.points, 9).timeLabel).toBe('9 AM');

      const customLayout = computeHourlyGraphLayout(pts, undefined, { maxPoints: 24 });
      expect(customLayout).not.toBeNull();
      expect(customLayout?.totalWidth).toBe(1344.0);
      expect(customLayout?.points).toHaveLength(24);
      expect(itemAt(customLayout?.points, 0).timeLabel).toBe('Now');
      expect(itemAt(customLayout?.points, 23).timeLabel).toBe('11 PM');
    });

    it('supports compact 108px height mode when passed via options', () => {
      const pts = [
        { time: '2026-09-26T12:00', temp: 80.0, precip_prob: 40, icon: 'weather-rainy' },
        { time: '2026-09-26T13:00', temp: 50.0, precip_prob: 0, icon: 'weather-cloudy' },
      ];
      const layout = computeHourlyGraphLayout(pts, undefined, {
        svgHeight: 108.0,
        curveTop: 34.0,
        curveBottom: 78.0,
        baseY: 90.0,
      });
      expect(layout).not.toBeNull();
      expect(layout?.svgHeight).toBe(108.0);
      expect(layout?.baseY).toBe(90.0);
      expect(layout?.isTall).toBe(false);
      expect(itemAt(layout?.points, 0).tempFontSize).toBe(12);
      expect(itemAt(layout?.points, 0).circleR).toBe(3.5);
      expect(itemAt(layout?.points, 0).timeY).toBe(102);
    });
    it('honors a custom baseY option', () => {
      const layout = computeHourlyGraphLayout([{ time: '2026-09-26T14:00', temp: 70, icon: 'weather-sunny' }], undefined, { baseY: 150 });
      expect(layout?.baseY).toBe(150);
    });
  });
  it('clears the formatTime and formatDate caches past 2000 entries', () => {
    for (let i = 0; i < 2005; i++) {
      formatTime('15:04', `2026-10-27T10:${String(i % 60).padStart(2, '0')}:${String((i * 7) % 60).padStart(2, '0')}.${String(i).padStart(3, '0')}Z`);
      formatDate('2006-01-02', `2026-${String((i % 12) + 1).padStart(2, '0')}-${String((i % 28) + 1).padStart(2, '0')}T${String(i % 24).padStart(2, '0')}:00:00.${String(i).padStart(3, '0')}Z`);
    }
  });
});

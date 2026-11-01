/**
 * Shared formatting and SVG helpers for Mirrormere widgets.
 * Ported from internal/render/helpers.go for native React widgets.
 */

export interface TasksStatsResult {
  total: number;
  completed: number;
  remaining: number;
  overdue: number;
}

/** Escapes XML/HTML special characters for safe inclusion in SVG markup. */
export function escapeHtml(str: string): string {
  return str
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

/**
 * Calculates task completion metrics (total, completed, remaining, overdue).
 * Accepts an array of task items or an object containing an items/list array.
 * Supports both snake_case (done, due_date) and PascalCase (Done, DueDate).
 */
export function tasksStats(input: unknown, now: Date = new Date()): TasksStatsResult {
  if (!input) {
    return { total: 0, completed: 0, remaining: 0, overdue: 0 };
  }

  let items: unknown[] | null = null;
  if (Array.isArray(input)) {
    items = input;
  } else if (typeof input === 'object') {
    const obj = input as Record<string, unknown>;
    const candidate = obj.items ?? obj.Items ?? obj.list ?? obj.List;
    if (Array.isArray(candidate)) {
      items = candidate;
    }
  }

  if (!items) {
    return { total: 0, completed: 0, remaining: 0, overdue: 0 };
  }

  let total = 0;
  let completed = 0;
  let overdue = 0;

  const nowUTC = Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate());
  const nowMs = now.getTime();

  for (const item of items) {
    if (!item || typeof item !== 'object') continue;
    const task = item as Record<string, unknown>;

    // Done check
    const doneVal = task.done ?? task.Done;
    const isDone = Boolean(doneVal);
    total++;
    if (isDone) {
      completed++;
      continue;
    }

    // Overdue check on pending tasks
    const rawDue = task.due_date ?? task.DueDate ?? task.due ?? task.Due;
    if (typeof rawDue === 'string' && rawDue.trim() !== '') {
      const dueStr = rawDue.trim();
      if (dueStr.length === 10 && /^\d{4}-\d{2}-\d{2}$/.test(dueStr)) {
        const [y, m, d] = dueStr.split('-').map(Number);
        const parsedUTC = Date.UTC(y, m - 1, d);
        if (nowUTC > parsedUTC) {
          overdue++;
        }
      } else {
        const parsed = new Date(dueStr);
        if (!isNaN(parsed.getTime()) && nowMs > parsed.getTime()) {
          overdue++;
        }
      }
    }
  }

  return {
    total,
    completed,
    remaining: Math.max(total - completed, 0),
    overdue,
  };
}

const validTimezoneCache = new Map<string, boolean>();

export function isValidTimezone(tz: string | null | undefined): boolean {
  if (!tz || typeof tz !== 'string') return false;
  const cached = validTimezoneCache.get(tz);
  if (cached !== undefined) return cached;
  try {
    new Intl.DateTimeFormat(undefined, { timeZone: tz });
    validTimezoneCache.set(tz, true);
    return true;
  } catch {
    validTimezoneCache.set(tz, false);
    return false;
  }
}

const timeFormatters = new Map<string, Intl.DateTimeFormat>();

function getTimeFormatter(tz: string): Intl.DateTimeFormat {
  let fmt = timeFormatters.get(tz);
  if (!fmt) {
    fmt = new Intl.DateTimeFormat('en-US', {
      timeZone: tz,
      hour: 'numeric',
      minute: 'numeric',
      hourCycle: 'h23',
    });
    timeFormatters.set(tz, fmt);
  }
  return fmt;
}

function extractHourMinute(dtf: Intl.DateTimeFormat, d: Date): { hour: number; minute: number } {
  const parts = dtf.formatToParts(d);
  let hour = -1;
  let minute = 0;
  for (let i = 0; i < parts.length; i++) {
    const p = parts[i];
    if (p.type === 'hour') hour = Number(p.value);
    else if (p.type === 'minute') minute = Number(p.value);
  }
  return { hour, minute };
}

const formatTimeCache = new Map<string, string>();

/**
 * Formats a time value according to Go layout string ('3 PM', '3:04', 'PM', '15:04', 'HH:mm').
 * Supports 1-argument formatTime(val) defaulting to '15:04', and 2/3 arguments formatTime(layout, val, tz).
 * Returns '' on invalid input or date-only inputs ('YYYY-MM-DD').
 */
export function formatTime(layoutOrValue: unknown, value?: unknown, tz?: string): string {
  let layout = '15:04';
  let val: unknown = layoutOrValue;

  if (value !== undefined) {
    if (typeof layoutOrValue === 'string' && layoutOrValue !== '' && layoutOrValue !== 'default') {
      layout = layoutOrValue;
    }
    val = value;
  }

  if (val === null || val === undefined || val === '') return '';

  const cacheKey =
    typeof val === 'string' || typeof val === 'number'
      ? `${layout}|${val}|${tz ?? ''}`
      : val instanceof Date && !isNaN(val.getTime())
        ? `${layout}|${val.getTime()}|${tz ?? ''}`
        : null;

  if (cacheKey) {
    const cached = formatTimeCache.get(cacheKey);
    if (cached !== undefined) return cached;
  }

  let date: Date | null = null;
  let hour = -1;
  let minute = 0;
  const hasValidTz = Boolean(tz && isValidTimezone(tz));

  if (typeof val === 'string') {
    const s = val.trim();
    // Go formatTime returns "" on date-only input (length 10: YYYY-MM-DD)
    if (/^\d{4}-\d{2}-\d{2}$/.test(s)) {
      return '';
    }

    // Match ISO with optional timezone
    const isoMatch = /^(\d{4})-(\d{2})-(\d{2})[T ](\d{2}):(\d{2})(?::(\d{2}))?(?:\.\d+)?(Z|[+-]\d{2}:?\d{2})?$/.exec(s);
    if (isoMatch) {
      const tzMatch = isoMatch[7];
      if (hasValidTz && (tzMatch === 'Z' || (tzMatch && tzMatch !== ''))) {
        const d = new Date(s);
        if (isNaN(d.getTime())) return '';
        const hm = extractHourMinute(getTimeFormatter(tz!), d);
        hour = hm.hour;
        minute = hm.minute;
      } else if (tzMatch === 'Z') {
        const d = new Date(s);
        if (isNaN(d.getTime())) return '';
        hour = d.getUTCHours();
        minute = d.getUTCMinutes();
      } else {
        hour = Number(isoMatch[4]);
        minute = Number(isoMatch[5]);
      }
    } else {
      // Check for standalone HH:mm
      const timeMatch = /^(\d{2}):(\d{2})(?::(\d{2}))?$/.exec(s);
      if (timeMatch) {
        hour = Number(timeMatch[1]);
        minute = Number(timeMatch[2]);
      } else {
        const d = new Date(s);
        if (isNaN(d.getTime())) return '';
        if (hasValidTz) {
          const hm = extractHourMinute(getTimeFormatter(tz!), d);
          hour = hm.hour;
          minute = hm.minute;
        } else {
          date = d;
        }
      }
    }
  } else if (val instanceof Date) {
    if (isNaN(val.getTime())) return '';
    if (hasValidTz) {
      const hm = extractHourMinute(getTimeFormatter(tz!), val);
      hour = hm.hour;
      minute = hm.minute;
    } else {
      date = val;
    }
  } else if (typeof val === 'number') {
    const d = new Date(val);
    if (isNaN(d.getTime())) return '';
    if (hasValidTz) {
      const hm = extractHourMinute(getTimeFormatter(tz!), d);
      hour = hm.hour;
      minute = hm.minute;
    } else {
      date = d;
    }
  } else {
    return '';
  }

  if (date !== null) {
    hour = date.getHours();
    minute = date.getMinutes();
  }

  if (hour < 0 || hour > 23 || minute < 0 || minute > 59) {
    return '';
  }

  const ampm = hour >= 12 ? 'PM' : 'AM';
  const h12 = hour % 12 === 0 ? 12 : hour % 12;

  let res = '';
  if (layout === '3 PM') {
    res = `${h12} ${ampm}`;
  } else if (layout === '3:04') {
    res = `${h12}:${String(minute).padStart(2, '0')}`;
  } else if (layout === '3:04 PM') {
    res = `${h12}:${String(minute).padStart(2, '0')} ${ampm}`;
  } else if (layout === 'PM') {
    res = ampm;
  } else if (layout === '15:04' || layout === 'HH:mm') {
    res = `${String(hour).padStart(2, '0')}:${String(minute).padStart(2, '0')}`;
  }

  if (cacheKey) {
    if (formatTimeCache.size > 2000) formatTimeCache.clear();
    formatTimeCache.set(cacheKey, res);
  }
  return res;
}

const SHORT_MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
const FULL_WEEKDAYS = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];

const dateFormatters = new Map<string, Intl.DateTimeFormat>();

function getDateFormatter(tz: string): Intl.DateTimeFormat {
  let fmt = dateFormatters.get(tz);
  if (!fmt) {
    fmt = new Intl.DateTimeFormat('en-US', {
      timeZone: tz,
      weekday: 'long',
      year: 'numeric',
      month: 'numeric',
      day: 'numeric',
    });
    dateFormatters.set(tz, fmt);
  }
  return fmt;
}

function extractDateParts(
  dtf: Intl.DateTimeFormat,
  d: Date
): { year: number; month: number; day: number; weekdayIdx: number } {
  const parts = dtf.formatToParts(d);
  let year = -1;
  let month = -1;
  let day = -1;
  let weekdayIdx = -1;
  for (let i = 0; i < parts.length; i++) {
    const p = parts[i];
    if (p.type === 'year') year = Number(p.value);
    else if (p.type === 'month') month = Number(p.value);
    else if (p.type === 'day') day = Number(p.value);
    else if (p.type === 'weekday') weekdayIdx = FULL_WEEKDAYS.indexOf(p.value);
  }
  return { year, month, day, weekdayIdx };
}

const formatDateCache = new Map<string, string>();

/**
 * Formats a date value according to Go layout string ('2006-01-02', 'Jan 2', 'Monday • Jan 2', etc.).
 * Supports 1-argument formatDate(val) defaulting to '2006-01-02', and 2/3 arguments formatDate(layout, val, tz).
 * Preserves date-only strings ('YYYY-MM-DD') as calendar dates without timezone shifting.
 */
export function formatDate(layoutOrValue: unknown, value?: unknown, tz?: string): string {
  let layout = '2006-01-02';
  let val: unknown = layoutOrValue;

  if (value !== undefined) {
    if (typeof layoutOrValue === 'string' && layoutOrValue !== '' && layoutOrValue !== 'default') {
      layout = layoutOrValue;
    }
    val = value;
  }

  if (val === null || val === undefined || val === '') return '';

  const cacheKey =
    typeof val === 'string' || typeof val === 'number'
      ? `${layout}|${val}|${tz ?? ''}`
      : val instanceof Date && !isNaN(val.getTime())
        ? `${layout}|${val.getTime()}|${tz ?? ''}`
        : null;

  if (cacheKey) {
    const cached = formatDateCache.get(cacheKey);
    if (cached !== undefined) return cached;
  }

  let year: number;
  let month: number; // 1-12
  let day: number; // 1-31
  let weekdayIdx: number; // 0-6

  const hasValidTz = Boolean(tz && isValidTimezone(tz));

  if (typeof val === 'string') {
    const s = val.trim();
    // Check date-only string (YYYY-MM-DD)
    const dateOnlyMatch = /^(\d{4})-(\d{2})-(\d{2})$/.exec(s);
    if (dateOnlyMatch) {
      year = Number(dateOnlyMatch[1]);
      month = Number(dateOnlyMatch[2]);
      day = Number(dateOnlyMatch[3]);
      // Weekday for date-only: construct local Date noon
      const d = new Date(year, month - 1, day, 12, 0, 0);
      weekdayIdx = d.getDay();
    } else {
      const d = new Date(s);
      if (isNaN(d.getTime())) {
        return s;
      }
      if (hasValidTz) {
        const dp = extractDateParts(getDateFormatter(tz!), d);
        year = dp.year;
        month = dp.month;
        day = dp.day;
        weekdayIdx = dp.weekdayIdx;
      } else {
        if (s.endsWith('Z')) {
          year = d.getUTCFullYear();
          month = d.getUTCMonth() + 1;
          day = d.getUTCDate();
          weekdayIdx = d.getUTCDay();
        } else {
          year = d.getFullYear();
          month = d.getMonth() + 1;
          day = d.getDate();
          weekdayIdx = d.getDay();
        }
      }
    }
  } else if (val instanceof Date) {
    if (isNaN(val.getTime())) return String(val);
    if (hasValidTz) {
      const dp = extractDateParts(getDateFormatter(tz!), val);
      year = dp.year;
      month = dp.month;
      day = dp.day;
      weekdayIdx = dp.weekdayIdx;
    } else {
      year = val.getUTCFullYear();
      month = val.getUTCMonth() + 1;
      day = val.getUTCDate();
      weekdayIdx = val.getUTCDay();
    }
  } else if (typeof val === 'number') {
    const d = new Date(val);
    if (isNaN(d.getTime())) return String(val);
    if (hasValidTz) {
      const dp = extractDateParts(getDateFormatter(tz!), d);
      year = dp.year;
      month = dp.month;
      day = dp.day;
      weekdayIdx = dp.weekdayIdx;
    } else {
      year = d.getUTCFullYear();
      month = d.getUTCMonth() + 1;
      day = d.getUTCDate();
      weekdayIdx = d.getUTCDay();
    }
  } else {
    return String(val);
  }

  let res = String(val);
  if (year >= 0 && month >= 1 && month <= 12 && day >= 1 && day <= 31) {
    const shortMonth = SHORT_MONTHS[month - 1];
    const fullWeekday = weekdayIdx >= 0 ? FULL_WEEKDAYS[weekdayIdx] : '';

    if (layout === '2006-01-02' || layout === 'default') {
      res = `${year}-${String(month).padStart(2, '0')}-${String(day).padStart(2, '0')}`;
    } else if (layout === 'Jan 2') {
      res = `${shortMonth} ${day}`;
    } else if (layout === 'Jan 02, 2006') {
      res = `${shortMonth} ${String(day).padStart(2, '0')}, ${year}`;
    } else if (layout === 'Monday • Jan 2') {
      res = `${fullWeekday} • ${shortMonth} ${day}`;
    } else if (layout === '2006') {
      res = `${year}`;
    } else {
      res = `${year}-${String(month).padStart(2, '0')}-${String(day).padStart(2, '0')}`;
    }
  }

  if (cacheKey) {
    if (formatDateCache.size > 2000) formatDateCache.clear();
    formatDateCache.set(cacheKey, res);
  }
  return res;
}

/**
 * Returns human-readable relative date expressions ("Today", "Tomorrow", "Yesterday", "in N days", "N days ago").
 */
export function relDate(refTime: Date, value: unknown, tz?: string): string {
  if (value === null || value === undefined || value === '') return '';
  if (!refTime || isNaN(refTime.getTime())) return String(value);

  const refDateStr = formatDate('2006-01-02', refTime, tz);
  const valDateStr = formatDate('2006-01-02', value, tz);
  if (
    !refDateStr ||
    !valDateStr ||
    !/^\d{4}-\d{2}-\d{2}$/.test(refDateStr) ||
    !/^\d{4}-\d{2}-\d{2}$/.test(valDateStr)
  ) {
    return String(value);
  }

  const [ry, rm, rd] = refDateStr.split('-').map(Number);
  const [vy, vm, vd] = valDateStr.split('-').map(Number);
  const refUtc = Date.UTC(ry, rm - 1, rd);
  const valUtc = Date.UTC(vy, vm - 1, vd);
  const diffDays = Math.round((valUtc - refUtc) / (24 * 60 * 60 * 1000));

  if (diffDays === 0) return 'Today';
  if (diffDays === 1) return 'Tomorrow';
  if (diffDays === -1) return 'Yesterday';
  if (diffDays > 1) return `in ${diffDays} days`;
  return `${-diffDays} days ago`;
}

export interface CalendarFeedActiveCalendar {
  name: string;
  color?: string;
  enabled?: boolean;
  [key: string]: unknown;
}

/**
 * Filters widget configuration calendars down to those that are enabled.
 */
export function calendarFeedActiveCalendars(config: unknown): CalendarFeedActiveCalendar[] {
  if (!config || typeof config !== 'object') return [];

  let rawList: unknown[] | null = null;
  if (Array.isArray(config)) {
    rawList = config;
  } else {
    const cfg = config as Record<string, unknown>;
    if (Array.isArray(cfg.calendars)) {
      rawList = cfg.calendars;
    }
  }

  if (!rawList) return [];

  const result: CalendarFeedActiveCalendar[] = [];
  for (const item of rawList) {
    if (!item || typeof item !== 'object') continue;
    const m = item as Record<string, unknown>;
    const enabledVal = m.enabled;
    if (enabledVal === false || (typeof enabledVal === 'string' && enabledVal.toLowerCase() === 'false')) {
      continue;
    }
    result.push({
      ...m,
      name: String(m.name ?? ''),
      color: m.color ? String(m.color) : undefined,
      enabled: true,
    });
  }

  return result;
}

export interface CalendarFeedEvent {
  id?: string;
  title: string;
  start: string;
  end?: string;
  all_day?: boolean;
  allDay?: boolean;
  location?: string;
  calendar_name?: string;
  calendarName?: string;
  color?: string;
  [key: string]: unknown;
}

export interface CalendarFeedDayGroup {
  date: string;
  rel: string;
  start: string;
  events: CalendarFeedEvent[];
}

/**
 * Groups filtered calendar feed events by local calendar date.
 */
export function calendarFeedDayGroups(
  data: unknown,
  refTime: Date = new Date(),
  tz?: string
): CalendarFeedDayGroup[] {
  if (!data || typeof data !== 'object') return [];

  const effectiveRefTime = refTime && !isNaN(refTime.getTime()) ? refTime : new Date();

  let rawEvents: unknown[] | null = null;
  if (Array.isArray(data)) {
    rawEvents = data;
  } else {
    const d = data as Record<string, unknown>;
    if (Array.isArray(d.events)) {
      rawEvents = d.events;
    } else if (Array.isArray(d.Events)) {
      rawEvents = d.Events;
    }
  }

  if (!rawEvents || rawEvents.length === 0) return [];

  const todayStr = formatDate('2006-01-02', effectiveRefTime, tz);
  const refTimeMs = effectiveRefTime.getTime();

  const filtered: CalendarFeedEvent[] = [];

  for (const raw of rawEvents) {
    if (!raw || typeof raw !== 'object') continue;
    const ev = raw as Record<string, unknown>;
    const title = typeof ev.title === 'string' ? ev.title : typeof ev.Title === 'string' ? ev.Title : '';
    const rawStart = ev.start ?? ev.Start;
    const startStr = typeof rawStart === 'string' ? rawStart : '';
    if (!title && !startStr) continue;

    const rawEnd = ev.end ?? ev.End;
    const endStr = typeof rawEnd === 'string' ? rawEnd : '';
    const isAllDay = Boolean(ev.all_day || ev.allDay || ev.AllDay || /^\d{4}-\d{2}-\d{2}$/.test(startStr));

    if (isAllDay) {
      const endDateStr = endStr !== '' ? endStr : startStr;
      if (endDateStr < todayStr) {
        continue;
      }
      filtered.push({
        ...ev,
        id: ev.id ? String(ev.id) : ev.ID ? String(ev.ID) : undefined,
        title,
        start: startStr,
        end: endStr || undefined,
        allDay: true,
        all_day: true,
        location: (ev.location ?? ev.Location) ? String(ev.location ?? ev.Location) : undefined,
        calendarName: (ev.calendar_name ?? ev.calendarName ?? ev.CalendarName) ? String(ev.calendar_name ?? ev.calendarName ?? ev.CalendarName) : undefined,
        color: (ev.color ?? ev.Color) ? String(ev.color ?? ev.Color) : undefined,
      });
      continue;
    }

    // Timed event
    if (endStr !== '') {
      const endDate = new Date(endStr);
      if (!isNaN(endDate.getTime()) && endDate.getTime() < refTimeMs) {
        continue;
      }
    } else if (startStr !== '') {
      const startDate = new Date(startStr);
      if (!isNaN(startDate.getTime()) && startDate.getTime() + 60 * 60 * 1000 < refTimeMs) {
        continue;
      }
    }

    filtered.push({
      ...ev,
      id: ev.id ? String(ev.id) : ev.ID ? String(ev.ID) : undefined,
      title,
      start: startStr,
      end: endStr || undefined,
      allDay: false,
      all_day: false,
      location: (ev.location ?? ev.Location) ? String(ev.location ?? ev.Location) : undefined,
      calendarName: (ev.calendar_name ?? ev.calendarName ?? ev.CalendarName) ? String(ev.calendar_name ?? ev.calendarName ?? ev.CalendarName) : undefined,
      color: (ev.color ?? ev.Color) ? String(ev.color ?? ev.Color) : undefined,
    });
  }

  if (filtered.length === 0) return [];

  const groups: CalendarFeedDayGroup[] = [];
  for (const ev of filtered) {
    const dateStr = formatDate('2006-01-02', ev.start, tz);
    if (groups.length === 0 || groups[groups.length - 1].date !== dateStr) {
      const rel = relDate(effectiveRefTime, ev.start, tz);
      groups.push({
        date: dateStr,
        rel,
        start: ev.start,
        events: [ev],
      });
    } else {
      groups[groups.length - 1].events.push(ev);
    }
  }

  return groups;
}

/**
 * Extracts today's maximum precipitation probability percentage (0-100)
 * from daily forecast, hourly forecast, current snapshot, or map data.
 */
export function todayPrecipProb(data: unknown): number {
  if (!data || typeof data !== 'object') {
    return 0;
  }

  const d = data as Record<string, unknown>;

  // Daily array
  const daily = d.daily ?? d.Daily;
  if (Array.isArray(daily) && daily.length > 0) {
    const first = daily[0];
    if (first && typeof first === 'object') {
      const f = first as Record<string, unknown>;
      const p = f.precip_prob_max ?? f.PrecipProbMax ?? f.precip_prob ?? f.PrecipitationProbability;
      if (typeof p === 'number' && !isNaN(p)) return Math.max(0, Math.min(100, Math.round(p)));
    }
  }

  // Hourly array (max across first 12 hours)
  const hourly = d.hourly ?? d.Hourly;
  if (Array.isArray(hourly) && hourly.length > 0) {
    let maxProb = 0;
    for (const item of hourly.slice(0, 12)) {
      if (item && typeof item === 'object') {
        const h = item as Record<string, unknown>;
        const p = h.precip_prob ?? h.PrecipProb ?? h.precip_prob_max ?? h.PrecipitationProbability;
        if (typeof p === 'number' && !isNaN(p)) {
          maxProb = Math.max(maxProb, Math.round(p));
        }
      }
    }
    if (maxProb > 0) {
      return Math.max(0, Math.min(100, maxProb));
    }
  }

  // Current
  const current = d.current ?? d.Current;
  if (current && typeof current === 'object') {
    const c = current as Record<string, unknown>;
    const p = c.precip_prob_max ?? c.PrecipProbMax ?? c.precip_prob ?? c.PrecipitationProbability;
    if (typeof p === 'number' && !isNaN(p)) return Math.max(0, Math.min(100, Math.round(p)));
  }

  // Direct precip_prob_max
  const direct = d.precip_prob_max ?? d.PrecipProbMax;
  if (typeof direct === 'number' && !isNaN(direct)) {
    return Math.max(0, Math.min(100, Math.round(direct)));
  }

  return 0;
}

export interface WeatherHourlyGraphOptions {
  svgHeight?: number;
  curveTop?: number;
  curveBottom?: number;
  baseY?: number;
  containerWidth?: number;
}

export interface HourlyPointLayout {
  x: number;
  y: number;
  lineY1: number;
  precipProb: number;
  precipY: number;
  icon: string;
  iconTranslateX: number;
  iconY: number;
  iconScale: number;
  tempRound: number;
  tempY: number;
  tempFontSize: number;
  circleR: number;
  timeLabel: string;
  timeWeight: string;
  timeColor: string;
  timeY: number;
  timeFontSize: number;
}

export interface HourlyGraphLayout {
  totalWidth: number;
  svgHeight: number;
  baseY: number;
  isTall: boolean;
  gradientId: string;
  areaPath: string;
  curvePath: string;
  gridLinesPath: string;
  points: HourlyPointLayout[];
}

/**
 * Computes deterministic layout metrics and SVG geometry for an hourly weather forecast graph.
 */
export function computeHourlyGraphLayout(
  hourly: unknown,
  widgetId?: string,
  options?: WeatherHourlyGraphOptions,
): HourlyGraphLayout | null {
  if (!hourly || !Array.isArray(hourly) || hourly.length === 0) {
    return null;
  }

  const rawPts: { timeStr: string; temp: number; precipProb: number; icon: string }[] = [];

  for (const item of hourly) {
    if (!item || typeof item !== 'object') continue;
    const m = item as Record<string, unknown>;

    let timeStr = '';
    const rawTime = m.time ?? m.Time;
    if (typeof rawTime === 'string') {
      timeStr = rawTime;
    }

    let temp = Number(m.temp ?? m.Temp);
    if (isNaN(temp) || !isFinite(temp)) {
      temp = 0.0;
    }

    let precipProb = 0;
    const rawPrecip = m.precip_prob ?? m.PrecipProb;
    if (typeof rawPrecip === 'number' && !isNaN(rawPrecip)) {
      precipProb = Math.round(rawPrecip);
    }

    let icon = 'weather-cloudy';
    const rawIcon = m.icon ?? m.Icon;
    if (typeof rawIcon === 'string' && rawIcon.trim() !== '') {
      icon = rawIcon.trim();
    }

    rawPts.push({ timeStr, temp, precipProb, icon });
  }

  if (rawPts.length === 0) {
    return null;
  }

  const n = rawPts.length;
  const gradientId =
    widgetId && widgetId.trim() !== ''
      ? `mm-weather-grad-${escapeHtml(widgetId.trim())}`
      : 'mm-weather-hourly-grad';

  const baseColWidth = 68.0;
  const minGraphWidth = 560.0;
  const svgHeight = options?.svgHeight ?? 300.0;
  const curveTop = options?.curveTop ?? 90.0;
  const curveBottom = options?.curveBottom ?? 205.0;
  const baseY = options?.baseY ?? (svgHeight === 108.0 ? 90.0 : 240.0);

  const isTall = svgHeight >= 200.0;

  let colWidth = baseColWidth;
  if (options?.containerWidth && options.containerWidth > 0 && n > 0) {
    colWidth = options.containerWidth / n;
  } else if (n * colWidth < minGraphWidth && n > 0) {
    colWidth = minGraphWidth / n;
  }
  const totalWidth = n * colWidth;

  let minTemp = rawPts[0].temp;
  let maxTemp = rawPts[0].temp;
  for (let i = 1; i < n; i++) {
    if (rawPts[i].temp < minTemp) minTemp = rawPts[i].temp;
    if (rawPts[i].temp > maxTemp) maxTemp = rawPts[i].temp;
  }

  let span = maxTemp - minTemp;
  if (span < 4.0) {
    const mid = (minTemp + maxTemp) / 2.0;
    minTemp = mid - 2.0;
    span = 4.0;
  }

  const coords: { x: number; y: number }[] = [];
  for (let i = 0; i < n; i++) {
    const x = i * colWidth + colWidth / 2.0;
    const ratio = (rawPts[i].temp - minTemp) / span;
    const y = curveBottom - ratio * (curveBottom - curveTop);
    coords.push({ x, y });
  }

  let curvePath = '';
  let areaPath = '';

  if (n > 1) {
    curvePath = `M ${coords[0].x.toFixed(1)} ${coords[0].y.toFixed(1)}`;
    areaPath = `M ${coords[0].x.toFixed(1)} ${baseY.toFixed(1)} L ${coords[0].x.toFixed(1)} ${coords[0].y.toFixed(1)}`;

    for (let i = 0; i < n - 1; i++) {
      const dx = (coords[i + 1].x - coords[i].x) / 2.0;
      const seg = ` C ${(coords[i].x + dx).toFixed(1)} ${coords[i].y.toFixed(1)}, ${(coords[i + 1].x - dx).toFixed(1)} ${coords[i + 1].y.toFixed(1)}, ${coords[i + 1].x.toFixed(1)} ${coords[i + 1].y.toFixed(1)}`;
      curvePath += seg;
      areaPath += seg;
    }
    areaPath += ` L ${coords[n - 1].x.toFixed(1)} ${baseY.toFixed(1)} Z`;
  }

  const points: HourlyPointLayout[] = [];
  for (let i = 0; i < n; i++) {
    const pt = rawPts[i];
    const coord = coords[i];
    const lineTopOffset = isTall ? 6.0 : 4.0;
    const lineY1 = coord.y + lineTopOffset;
    const precipY = isTall ? 20 : 12;

    const iconY = isTall ? 32 : 14;
    const iconScale = isTall ? 1.2 : 0.8;
    const iconOffsetX = isTall ? 14.4 : 9.6;
    const iconTranslateX = coord.x - iconOffsetX;

    // Round half-away-from-zero to match Go math.Round
    const tempRound = Math.sign(pt.temp) * Math.round(Math.abs(pt.temp)) || 0;
    const tempY = isTall ? coord.y - 10.0 : coord.y - 6.0;
    const tempFontSize = isTall ? 14 : 12;
    const circleR = isTall ? 4.0 : 3.5;

    let timeLabel = 'Now';
    let timeWeight = '800';
    let timeColor = 'var(--mm-coral, #ff6f59)';
    if (i > 0) {
      timeWeight = '600';
      timeColor = 'var(--mm-text-dim, #8b819f)';
      const formatted = formatTime('3 PM', pt.timeStr);
      timeLabel = formatted !== '' ? formatted : pt.timeStr;
    }
    const timeY = isTall ? 275 : 102;
    const timeFontSize = isTall ? 13 : 11;

    points.push({
      x: coord.x,
      y: coord.y,
      lineY1,
      precipProb: pt.precipProb,
      precipY,
      icon: pt.icon,
      iconTranslateX,
      iconY,
      iconScale,
      tempRound,
      tempY,
      tempFontSize,
      circleR,
      timeLabel,
      timeWeight,
      timeColor,
      timeY,
      timeFontSize,
    });
  }

  let gridLinesPath = '';
  for (let i = 0; i < n; i++) {
    const pt = points[i];
    gridLinesPath += `M ${pt.x.toFixed(1)} ${pt.lineY1.toFixed(1)} L ${pt.x.toFixed(1)} ${baseY.toFixed(1)} `;
  }

  return {
    totalWidth,
    svgHeight,
    baseY,
    isTall,
    gradientId,
    areaPath,
    curvePath,
    gridLinesPath: gridLinesPath.trim(),
    points,
  };
}

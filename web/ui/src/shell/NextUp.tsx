import { useEffect, useState } from 'react';
import type { KeyboardEvent as ReactKeyboardEvent } from 'react';
import { useDocumentVisible } from '../lifecycle/visibility';
import { useDisplayStore } from '../store';
import type { WidgetEntry } from '../store/types';
import { isValidTimezone, resolveActiveTimezone } from './Clock';

export interface RawCalendarEvent {
  id?: string;
  title: string;
  start: string | Date;
  end?: string | Date;
  allDay?: boolean;
  all_day?: boolean;
  date?: string;
  dayDate?: string;
  location?: string;
  [key: string]: unknown;
}

export interface ResolvedNextUpEvent {
  event: RawCalendarEvent;
  title: string;
  label: string;
  timeStr: string;
  countdown: string;
  isNow: boolean;
}

export interface ParsedEventDates {
  raw: RawCalendarEvent;
  id: string;
  title: string;
  startDate: Date;
  endDate: Date;
  isAllDay: boolean;
}

/**
 * Format relative countdown string (e.g. "in 1h 30m", "in 45m", "ends in 15m").
 * Ported from web/static/js/display.js lines 923-932.
 */
export function formatRelativeCountdown(ms: number, isEnding = false): string {
  const totalMinutes = Math.round(ms / 60000);
  const prefix = isEnding ? 'ends in ' : 'in ';
  if (totalMinutes <= 0) return `${prefix}<1m`;
  if (totalMinutes < 60) return `${prefix}${totalMinutes}m`;
  const hours = Math.floor(totalMinutes / 60);
  const mins = totalMinutes % 60;
  if (mins === 0) return `${prefix}${hours}h`;
  return `${prefix}${hours}h ${mins}m`;
}

const tzOffsetFormatters = new Map<string, Intl.DateTimeFormat>();

function getTimezoneOffsetString(date: Date, tz?: string): string {
  if (!tz || !isValidTimezone(tz)) return '';
  try {
    let formatter = tzOffsetFormatters.get(tz);
    if (!formatter) {
      formatter = new Intl.DateTimeFormat('en-US', {
        timeZone: tz,
        timeZoneName: 'longOffset',
      });
      tzOffsetFormatters.set(tz, formatter);
    }
    const parts = formatter.formatToParts(date);
    for (let i = 0; i < parts.length; i++) {
      const p = parts[i];
      if (p.type === 'timeZoneName' && p.value) {
        const m = p.value.match(/GMT([+-]\d{2}:\d{2})/);
        if (m) return m[1];
        if (p.value === 'GMT' || p.value === 'UTC') return 'Z';
      }
    }
  } catch {
    // Ignore timezone formatting failures
  }
  return '';
}

const datePartsFormatters = new Map<string, Intl.DateTimeFormat>();

function getDatePartsInTimezone(
  date: Date,
  tz?: string
): { year: number; month: number; day: number } {
  if (!tz || !isValidTimezone(tz)) {
    return {
      year: date.getFullYear(),
      month: date.getMonth() + 1,
      day: date.getDate(),
    };
  }
  try {
    let formatter = datePartsFormatters.get(tz);
    if (!formatter) {
      formatter = new Intl.DateTimeFormat('en-US', {
        timeZone: tz,
        year: 'numeric',
        month: 'numeric',
        day: 'numeric',
      });
      datePartsFormatters.set(tz, formatter);
    }
    const parts = formatter.formatToParts(date);
    let year = date.getFullYear();
    let month = date.getMonth() + 1;
    let day = date.getDate();
    for (let i = 0; i < parts.length; i++) {
      const p = parts[i];
      if (p.type === 'year') year = parseInt(p.value, 10);
      else if (p.type === 'month') month = parseInt(p.value, 10);
      else if (p.type === 'day') day = parseInt(p.value, 10);
    }
    return { year, month, day };
  } catch {
    return {
      year: date.getFullYear(),
      month: date.getMonth() + 1,
      day: date.getDate(),
    };
  }
}

/**
 * Parse raw calendar event dates into normalized Date objects.
 * Ported from web/static/js/display.js lines 995-1060.
 */
export function parseEventDates(
  raw: RawCalendarEvent,
  referenceNow: Date,
  tz?: string
): ParsedEventDates | null {
  if (!raw || typeof raw !== 'object') return null;
  const offsetStr = getTimezoneOffsetString(referenceNow, tz);
  const isAllDay = Boolean(
    raw.allDay ||
      raw.all_day ||
      (typeof raw.start === 'string' && raw.start.length === 10 && !raw.start.includes(':'))
  );
  let startDate: Date | null = null;
  let endDate: Date | null = null;

  if (isAllDay) {
    let dateStr = typeof raw.start === 'string' ? raw.start.substring(0, 10) : '';
    if (!dateStr && typeof raw.date === 'string') dateStr = raw.date;
    if (!dateStr && typeof raw.dayDate === 'string') dateStr = raw.dayDate;
    if (dateStr && /^\d{4}-\d{2}-\d{2}$/.test(dateStr)) {
      startDate = new Date(`${dateStr}T00:00:00${offsetStr}`);
      let endDateStr = typeof raw.end === 'string' ? raw.end.substring(0, 10) : dateStr;
      if (!endDateStr || !/^\d{4}-\d{2}-\d{2}$/.test(endDateStr)) endDateStr = dateStr;
      endDate = new Date(`${endDateStr}T23:59:59.999${offsetStr}`);
    } else if (raw.start instanceof Date) {
      startDate = new Date(raw.start);
      endDate =
        raw.end instanceof Date
          ? new Date(raw.end)
          : new Date(startDate.getTime() + 24 * 3600 * 1000 - 1);
    }
  } else {
    if (typeof raw.start === 'string' && /^\d{1,2}:\d{2}$/.test(raw.start)) {
      let dateStr =
        (typeof raw.date === 'string' ? raw.date : undefined) ||
        (typeof raw.dayDate === 'string' ? raw.dayDate : undefined);
      if (!dateStr || !/^\d{4}-\d{2}-\d{2}$/.test(dateStr)) {
        const np = getDatePartsInTimezone(referenceNow, tz);
        dateStr = `${np.year}-${String(np.month).padStart(2, '0')}-${String(np.day).padStart(2, '0')}`;
      }
      const [sh, sm] = raw.start.split(':').map((s) => s.padStart(2, '0'));
      startDate = new Date(`${dateStr}T${sh}:${sm}:00${offsetStr}`);

      if (typeof raw.end === 'string' && /^\d{1,2}:\d{2}$/.test(raw.end)) {
        const [eh, em] = raw.end.split(':').map((s) => s.padStart(2, '0'));
        endDate = new Date(`${dateStr}T${eh}:${em}:00${offsetStr}`);
      } else {
        endDate = new Date(startDate.getTime() + 60 * 60 * 1000);
      }
    } else if (typeof raw.start === 'string') {
      startDate = new Date(raw.start);
      if (typeof raw.end === 'string') {
        endDate = new Date(raw.end);
      } else {
        endDate = new Date(startDate.getTime() + 60 * 60 * 1000);
      }
    } else if (raw.start instanceof Date) {
      startDate = new Date(raw.start);
      endDate =
        raw.end instanceof Date
          ? new Date(raw.end)
          : new Date(startDate.getTime() + 60 * 60 * 1000);
    }
  }

  if (!startDate || isNaN(startDate.getTime()) || !endDate || isNaN(endDate.getTime())) {
    return null;
  }

  return {
    raw,
    id: raw.id || `${raw.title}-${String(raw.start)}`,
    title: raw.title || 'Event',
    startDate,
    endDate,
    isAllDay,
  };
}

const timeFormatters = new Map<string, Intl.DateTimeFormat>();

function formatTime(date: Date, tz?: string): string {
  const normTz = tz && isValidTimezone(tz) ? tz : '';
  let fmt = timeFormatters.get(normTz);
  if (!fmt) {
    const opts: Intl.DateTimeFormatOptions = { hour: 'numeric', minute: '2-digit' };
    if (normTz) opts.timeZone = normTz;
    fmt = new Intl.DateTimeFormat(undefined, opts);
    timeFormatters.set(normTz, fmt);
  }
  return fmt.format(date);
}

const weekdayFormatters = new Map<string, Intl.DateTimeFormat>();

function formatWeekday(date: Date, tz?: string): string {
  const normTz = tz && isValidTimezone(tz) ? tz : '';
  let fmt = weekdayFormatters.get(normTz);
  if (!fmt) {
    const opts: Intl.DateTimeFormatOptions = { weekday: 'short' };
    if (normTz) opts.timeZone = normTz;
    fmt = new Intl.DateTimeFormat(undefined, opts);
    weekdayFormatters.set(normTz, fmt);
  }
  return fmt.format(date);
}

/**
 * Resolve earliest next up or ongoing event across candidates.
 * Ported from web/static/js/display.js lines 1065-1186.
 */
export function resolveNextUpEvent(
  events: readonly RawCalendarEvent[],
  now: Date,
  tz?: string
): ResolvedNextUpEvent | null {
  if (!Array.isArray(events) || events.length === 0) return null;
  const activeTz = tz;

  const normalized: ParsedEventDates[] = [];
  for (const raw of events) {
    const parsed = parseEventDates(raw, now, activeTz);
    if (!parsed) continue;
    if (parsed.endDate <= now) continue;
    normalized.push(parsed);
  }

  if (normalized.length === 0) return null;

  const ongoingTimed: ParsedEventDates[] = [];
  const upcomingTimed: ParsedEventDates[] = [];
  const allDayToday: ParsedEventDates[] = [];
  const upcomingAllDay: ParsedEventDates[] = [];

  for (const ev of normalized) {
    if (ev.isAllDay) {
      if (ev.startDate <= now && ev.endDate >= now) {
        allDayToday.push(ev);
      } else if (ev.startDate > now) {
        upcomingAllDay.push(ev);
      }
    } else {
      if (ev.startDate <= now && ev.endDate > now) {
        ongoingTimed.push(ev);
      } else if (ev.startDate > now) {
        upcomingTimed.push(ev);
      }
    }
  }

  ongoingTimed.sort((a, b) => a.endDate.getTime() - b.endDate.getTime());
  upcomingTimed.sort((a, b) => a.startDate.getTime() - b.startDate.getTime());
  allDayToday.sort((a, b) => a.startDate.getTime() - b.startDate.getTime());
  upcomingAllDay.sort((a, b) => a.startDate.getTime() - b.startDate.getTime());

  // 1. Ongoing timed event takes top priority
  if (ongoingTimed.length > 0) {
    const ev = ongoingTimed[0];
    const remainingMs = ev.endDate.getTime() - now.getTime();
    return {
      event: ev.raw,
      title: ev.title,
      label: 'NOW',
      timeStr: formatTime(ev.startDate, activeTz),
      countdown: formatRelativeCountdown(remainingMs, true),
      isNow: true,
    };
  }

  // 2. Upcoming timed event today takes precedence over all-day event
  const nowParts = getDatePartsInTimezone(now, activeTz);
  const todayUpcoming = upcomingTimed.filter((e) => {
    const ep = getDatePartsInTimezone(e.startDate, activeTz);
    return ep.year === nowParts.year && ep.month === nowParts.month && ep.day === nowParts.day;
  });

  if (todayUpcoming.length > 0) {
    const ev = todayUpcoming[0];
    const untilMs = ev.startDate.getTime() - now.getTime();
    return {
      event: ev.raw,
      title: ev.title,
      label: 'NEXT',
      timeStr: formatTime(ev.startDate, activeTz),
      countdown: formatRelativeCountdown(untilMs, false),
      isNow: false,
    };
  }

  // 3. All-day event today
  if (allDayToday.length > 0) {
    const ev = allDayToday[0];
    return {
      event: ev.raw,
      title: ev.title,
      label: 'TODAY',
      timeStr: 'All Day',
      countdown: '',
      isNow: false,
    };
  }

  // 4. Upcoming timed event on a future day
  if (upcomingTimed.length > 0) {
    const ev = upcomingTimed[0];
    const untilMs = ev.startDate.getTime() - now.getTime();
    const weekday = formatWeekday(ev.startDate, activeTz);
    return {
      event: ev.raw,
      title: ev.title,
      label: 'NEXT',
      timeStr: `${weekday} ${formatTime(ev.startDate, activeTz)}`,
      countdown: formatRelativeCountdown(untilMs, false),
      isNow: false,
    };
  }

  // 5. Upcoming all-day event on a future day
  if (upcomingAllDay.length > 0) {
    const ev = upcomingAllDay[0];
    const weekday = formatWeekday(ev.startDate, activeTz);
    return {
      event: ev.raw,
      title: ev.title,
      label: weekday.toUpperCase(),
      timeStr: 'All Day',
      countdown: '',
      isNow: false,
    };
  }

  return null;
}

function extractEventsFromDay(day: Record<string, unknown>, out: RawCalendarEvent[]) {
  const dayDate =
    (typeof day.date === 'string' ? day.date : '') ||
    (typeof day.dayDate === 'string' ? day.dayDate : '');

  const pushGridEvent = (ev: unknown, forceAllDay = false) => {
    if (!ev || typeof ev !== 'object') return;
    const rec = ev as Record<string, unknown>;
    const title = typeof rec.title === 'string' ? rec.title : '';
    if (!title) return;
    const rawStart = typeof rec.start === 'string' ? rec.start.trim() : rec.start;
    const rawEnd = typeof rec.end === 'string' ? rec.end.trim() : rec.end;
    const isAllDay = Boolean(forceAllDay || rec.all_day || rec.allDay || !rawStart);
    const start =
      (typeof rawStart === 'string' && rawStart !== '') || rawStart instanceof Date
        ? rawStart
        : dayDate;
    const end =
      (typeof rawEnd === 'string' && rawEnd !== '') || rawEnd instanceof Date
        ? rawEnd
        : dayDate;
    const rawId = typeof rec.id === 'string' ? rec.id.trim() : '';
    const id = rawId
      ? dayDate && !rawId.endsWith(`-${dayDate}`)
        ? `${rawId}-${dayDate}`
        : rawId
      : `grid-${dayDate}-${start || 'allday'}-${title}`;

    out.push({
      ...rec,
      id,
      title,
      start,
      end,
      allDay: isAllDay,
      date: dayDate || (typeof rec.date === 'string' ? rec.date : undefined),
      dayDate: dayDate || (typeof rec.dayDate === 'string' ? rec.dayDate : undefined),
    });
  };

  for (const groupKey of ['events', 'early_events', 'late_events', 'timed']) {
    const group = day[groupKey];
    if (Array.isArray(group)) {
      for (const ev of group) {
        pushGridEvent(ev, false);
      }
    }
  }
  if (Array.isArray(day.all_day)) {
    for (const ev of day.all_day) {
      pushGridEvent(ev, true);
    }
  }
}

function processArray(arr: unknown[], out: RawCalendarEvent[]) {
  for (const item of arr) {
    if (!item || typeof item !== 'object') continue;
    const rec = item as Record<string, unknown>;
    if (
      typeof rec.date === 'string' &&
      (Array.isArray(rec.events) ||
        Array.isArray(rec.all_day) ||
        Array.isArray(rec.early_events) ||
        Array.isArray(rec.late_events) ||
        Array.isArray(rec.timed))
    ) {
      extractEventsFromDay(rec, out);
    } else if (
      typeof rec.title === 'string' &&
      (rec.start !== undefined || rec.allDay || rec.all_day || rec.date || rec.dayDate)
    ) {
      out.push(rec as unknown as RawCalendarEvent);
    }
  }
}

/**
 * Extracts and deduplicates calendar events from display widgets store.
 * Supports { events: [...] }, array entry.data, { days: [...] }, and entry.viewModel?.week?.days.
 */
export function selectCalendarEvents(
  widgets: Record<string, WidgetEntry | undefined>
): RawCalendarEvent[] {
  const rawList: RawCalendarEvent[] = [];

  for (const entry of Object.values(widgets)) {
    if (!entry) continue;

    if (Array.isArray(entry.data)) {
      processArray(entry.data, rawList);
    } else if (entry.data && typeof entry.data === 'object') {
      const dataObj = entry.data as Record<string, unknown>;
      if (Array.isArray(dataObj.events)) {
        processArray(dataObj.events, rawList);
      }
      if (Array.isArray(dataObj.days)) {
        processArray(dataObj.days, rawList);
      }
    }

    if (entry.viewModel && typeof entry.viewModel === 'object') {
      const vm = entry.viewModel as Record<string, unknown>;
      if (vm.week && typeof vm.week === 'object') {
        const week = vm.week as Record<string, unknown>;
        if (Array.isArray(week.days)) {
          processArray(week.days, rawList);
        }
        if (Array.isArray(week.all_day_events)) {
          const days = Array.isArray(week.days)
            ? (week.days as Record<string, unknown>[])
            : [];
          for (const evItem of week.all_day_events) {
            if (!evItem || typeof evItem !== 'object') continue;
            const ev = { ...(evItem as Record<string, unknown>) };
            if (!ev.start && !ev.date) {
              const startCol =
                typeof ev.start_col === 'number'
                  ? ev.start_col
                  : typeof ev.startCol === 'number'
                    ? ev.startCol
                    : 1;
              const targetDay = days[startCol - 1];
              if (targetDay && typeof targetDay.date === 'string') {
                ev.date = targetDay.date;
              }
            }
            if (ev.allDay === undefined && ev.all_day === undefined) {
              ev.allDay = true;
            }
            if (typeof ev.title === 'string') {
              processArray([ev], rawList);
            }
          }
        }
      }
      if (vm.month && typeof vm.month === 'object') {
        const month = vm.month as Record<string, unknown>;
        if (Array.isArray(month.days)) {
          processArray(month.days, rawList);
        }
      }
      if (Array.isArray(vm.days)) {
        processArray(vm.days, rawList);
      }
      if (Array.isArray(vm.events)) {
        processArray(vm.events, rawList);
      }
    }
  }

  const deduped = new Map<string, RawCalendarEvent>();
  for (const ev of rawList) {
    if (!ev || !ev.title) continue;
    const key = ev.id || `${ev.title}-${String(ev.start)}`;
    if (!deduped.has(key)) {
      deduped.set(key, ev);
    }
  }

  return Array.from(deduped.values());
}

/**
 * Hook subscribing to calendar events in display store widgets.
 */
export function useCalendarEvents(): RawCalendarEvent[] {
  const widgets = useDisplayStore((s) => s.widgets);
  return selectCalendarEvents(widgets);
}

export interface NextUpProps {
  now?: Date;
  timezone?: string;
  events?: readonly RawCalendarEvent[];
  onPillClick?: () => void;
}

export function NextUp(props: NextUpProps = {}) {
  const { now: propNow, timezone: propTimezone, events: propEvents, onPillClick } = props;
  const header = useDisplayStore((s) => s.header);
  const storeEvents = useCalendarEvents();
  const visible = useDocumentVisible();

  const [currentInstant, setCurrentInstant] = useState(() => propNow ?? new Date());

  useEffect(() => {
    if (propNow) {
      setCurrentInstant(propNow);
    }
  }, [propNow]);

  useEffect(() => {
    if (!visible || propNow) return;

    let timerId: ReturnType<typeof setTimeout> | null = null;
    let cancelled = false;

    const scheduleNext = () => {
      if (cancelled) return;
      const nowMs = Date.now();
      const delay = Math.max(1000, 15000 - (nowMs % 15000));
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
  const effectiveNow = propNow ?? currentInstant;

  const resolved = resolveNextUpEvent(events, effectiveNow, activeTz);

  const handlePillClick = () => {
    if (onPillClick) {
      onPillClick();
      return;
    }
    if (typeof document !== 'undefined') {
      const target = document.querySelector(
        '.widget-calendar-feed, .widget-calendar-grid, .calendar-event-item, .cf-day'
      );
      if (target && typeof target.scrollIntoView === 'function') {
        target.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
      }
    }
  };

  const handleKeyDown = (e: ReactKeyboardEvent<HTMLDivElement>) => {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      handlePillClick();
    }
  };

  const pillClass = resolved?.isNow
    ? 'pill-badge next-event-pill status-now'
    : 'pill-badge next-event-pill';
  const pillTitle = resolved
    ? `${resolved.label}: ${resolved.title} (${resolved.timeStr})`
    : 'Next upcoming event';

  return (
    <div
      className="header-next-event"
      id="header-next-event"
      style={{ display: resolved ? '' : 'none' }}
    >
      <div
        className={pillClass}
        id="next-event-pill"
        role="button"
        tabIndex={0}
        title={pillTitle}
        onClick={handlePillClick}
        onKeyDown={handleKeyDown}
      >
        <span className="next-event-label" id="next-event-label">
          {resolved?.label ?? 'NEXT'}
        </span>
        <span className="next-event-time" id="next-event-time">
          {resolved?.timeStr ?? '--:--'}
        </span>
        <span
          className="next-event-title"
          id="next-event-title"
          title={resolved?.title ?? undefined}
        >
          {resolved?.title ?? '--'}
        </span>
        <span className="next-event-countdown" id="next-event-countdown">
          {resolved?.countdown ?? ''}
        </span>
      </div>
    </div>
  );
}

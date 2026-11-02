import { useEffect, useMemo, useRef } from 'react';
import { useDocumentVisible } from '../lifecycle/visibility';
import { useHeader } from '../store/hooks';

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

interface ClockParts {
  time: string;
  ampm: string;
  weekday: string;
  fullDate: string;
}

export interface ClockFormatters {
  formatTime: (d: Date) => string;
  formatDate: (d: Date) => string;
  formatParts: (d: Date) => ClockParts;
}

const formatterCache = new Map<string, ClockFormatters>();

export function createClockFormatters(tz?: string, locale?: string): ClockFormatters {
  const normalizedTz = tz && isValidTimezone(tz) ? tz : '';
  const cacheKey = `${normalizedTz}|${locale ?? ''}`;
  const cached = formatterCache.get(cacheKey);
  if (cached) {
    return cached;
  }

  const timeOpts: Intl.DateTimeFormatOptions = { hour: 'numeric', minute: '2-digit' };
  const dateOpts: Intl.DateTimeFormatOptions = { weekday: 'long', month: 'short', day: 'numeric' };
  const weekdayOpts: Intl.DateTimeFormatOptions = { weekday: 'long' };
  const fullDateOpts: Intl.DateTimeFormatOptions = { month: 'short', day: 'numeric' };

  if (normalizedTz) {
    timeOpts.timeZone = normalizedTz;
    dateOpts.timeZone = normalizedTz;
    weekdayOpts.timeZone = normalizedTz;
    fullDateOpts.timeZone = normalizedTz;
  }

  const timeFormatter = new Intl.DateTimeFormat(locale, timeOpts);
  const dateFormatter = new Intl.DateTimeFormat(locale, dateOpts);
  const weekdayFormatter = new Intl.DateTimeFormat(locale, weekdayOpts);
  const fullDateFormatter = new Intl.DateTimeFormat(locale, fullDateOpts);

  const formatters: ClockFormatters = {
    formatTime: (d: Date) => timeFormatter.format(d),
    formatDate: (d: Date) => dateFormatter.format(d),
    formatParts: (d: Date) => {
      const parts = timeFormatter.formatToParts(d);
      const ampmPart = parts.find((p) => p.type === 'dayPeriod');
      const ampm = ampmPart ? ampmPart.value : '';
      const time = parts
        .filter((p) => p.type !== 'dayPeriod')
        .map((p) => p.value)
        .join('')
        .trim();
      return {
        time,
        ampm,
        weekday: weekdayFormatter.format(d),
        fullDate: fullDateFormatter.format(d),
      };
    },
  };

  formatterCache.set(cacheKey, formatters);
  return formatters;
}

export function resolveActiveTimezone(
  propTimezone?: string,
  header?: { timestamp?: string | null; timezone?: string } | null
): string | undefined {
  const bodyTz = typeof document !== 'undefined' ? document.body?.dataset?.timezone : undefined;
  // If an SSE header.update has arrived (indicated by non-null timestamp), prefer header.timezone.
  // Prior to initial SSE event, prefer document.body data-timezone rendered by the server template
  // over the store's default fallback timezone ('UTC').
  const sseTimezone = header?.timestamp ? header.timezone : undefined;
  const candidates = [propTimezone, sseTimezone, bodyTz, header?.timezone];
  return candidates.find(isValidTimezone);
}

export interface ClockProps {
  timezone?: string;
  locale?: string;
}

export function Clock({ timezone: propTimezone, locale: propLocale }: ClockProps = {}) {
  const header = useHeader();
  const visible = useDocumentVisible();

  const clockRef = useRef<HTMLDivElement | null>(null);
  const ampmRef = useRef<HTMLSpanElement | null>(null);
  const dateRef = useRef<HTMLDivElement | null>(null);
  const weekdayRef = useRef<HTMLSpanElement | null>(null);
  const fullDateRef = useRef<HTMLSpanElement | null>(null);

  const lastTimeRef = useRef<string | null>(null);
  const lastAmpmRef = useRef<string | null>(null);
  const lastWeekdayRef = useRef<string | null>(null);
  const lastFullDateRef = useRef<string | null>(null);

  const activeTz = resolveActiveTimezone(propTimezone, header);

  const formatters = useMemo(
    () => createClockFormatters(activeTz, propLocale),
    [activeTz, propLocale]
  );

  // Sync data-timezone attribute across DOM anchors
  useEffect(() => {
    if (typeof document !== 'undefined' && activeTz) {
      if (document.body?.dataset) {
        document.body.dataset.timezone = activeTz;
      }
      const appEl = document.getElementById('mirrormere-app');
      if (appEl?.dataset) {
        appEl.dataset.timezone = activeTz;
      }
      if (clockRef.current?.dataset) {
        clockRef.current.dataset.timezone = activeTz;
      }
    }
  }, [activeTz]);

  // Second-aligned clock tick, paused when hidden
  useEffect(() => {
    lastTimeRef.current = null;
    lastAmpmRef.current = null;
    lastWeekdayRef.current = null;
    lastFullDateRef.current = null;

    if (!visible) return;

    let timerId: ReturnType<typeof setTimeout> | null = null;
    let cancelled = false;

    const tick = () => {
      if (cancelled) return;
      const now = new Date();
      const parts = formatters.formatParts(now);

      if (clockRef.current && parts.time !== lastTimeRef.current) {
        lastTimeRef.current = parts.time;
        const target = clockRef.current.firstChild ?? clockRef.current;
        target.textContent = parts.time;
      }

      if (ampmRef.current && parts.ampm !== lastAmpmRef.current) {
        lastAmpmRef.current = parts.ampm;
        ampmRef.current.textContent = parts.ampm;
        ampmRef.current.style.display = parts.ampm ? '' : 'none';
      }

      if (weekdayRef.current && parts.weekday !== lastWeekdayRef.current) {
        lastWeekdayRef.current = parts.weekday;
        weekdayRef.current.textContent = parts.weekday;
      }

      if (fullDateRef.current && parts.fullDate !== lastFullDateRef.current) {
        lastFullDateRef.current = parts.fullDate;
        fullDateRef.current.textContent = parts.fullDate;
      }

      const nowMs = Date.now();
      const delay = 1000 - (nowMs % 1000);
      timerId = setTimeout(tick, delay);
    };

    tick();

    return () => {
      cancelled = true;
      if (timerId !== null) {
        clearTimeout(timerId);
      }
    };
  }, [visible, formatters]);

  return (
    <div className="header-time-block">
      <div className="header-clock" id="header-clock" data-timezone={activeTz} ref={clockRef}>
        --:--
        <span className="header-clock-ampm" ref={ampmRef} />
      </div>
      <div className="header-date-stack" id="header-date-stack">
        <div className="header-date" id="header-date" ref={dateRef}>
          <span className="header-date-weekday" ref={weekdayRef} />
          <span className="header-date-full" ref={fullDateRef} />
        </div>
      </div>
    </div>
  );
}

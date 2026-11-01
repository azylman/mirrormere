import { useEffect, useMemo, useRef } from 'react';
import { useDocumentVisible } from '../lifecycle/visibility';
import { useHeader } from '../store/hooks';

export function isValidTimezone(tz: string | null | undefined): boolean {
  if (!tz || typeof tz !== 'string') return false;
  try {
    new Intl.DateTimeFormat(undefined, { timeZone: tz });
    return true;
  } catch {
    return false;
  }
}

interface ClockFormatters {
  formatTime: (d: Date) => string;
  formatDate: (d: Date) => string;
}

const formatterCache = new Map<string, ClockFormatters>();

export function createClockFormatters(tz: string | undefined): ClockFormatters {
  const normalizedTz = tz && isValidTimezone(tz) ? tz : '';
  const cached = formatterCache.get(normalizedTz);
  if (cached) {
    return cached;
  }

  const timeOpts: Intl.DateTimeFormatOptions = { hour: 'numeric', minute: '2-digit' };
  const dateOpts: Intl.DateTimeFormatOptions = { weekday: 'long', month: 'short', day: 'numeric' };
  if (normalizedTz) {
    timeOpts.timeZone = normalizedTz;
    dateOpts.timeZone = normalizedTz;
  }

  const timeFormatter = new Intl.DateTimeFormat(undefined, timeOpts);
  const dateFormatter = new Intl.DateTimeFormat(undefined, dateOpts);

  const formatters: ClockFormatters = {
    formatTime: (d: Date) => timeFormatter.format(d),
    formatDate: (d: Date) => dateFormatter.format(d),
  };

  formatterCache.set(normalizedTz, formatters);
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
}

export function Clock({ timezone: propTimezone }: ClockProps = {}) {
  const header = useHeader();
  const visible = useDocumentVisible();

  const clockRef = useRef<HTMLDivElement | null>(null);
  const dateRef = useRef<HTMLDivElement | null>(null);

  const lastTimeRef = useRef<string | null>(null);
  const lastDateRef = useRef<string | null>(null);

  const activeTz = resolveActiveTimezone(propTimezone, header);

  const formatters = useMemo(() => createClockFormatters(activeTz), [activeTz]);

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
    if (!visible) return;

    let timerId: ReturnType<typeof setTimeout> | null = null;
    let cancelled = false;

    const tick = () => {
      if (cancelled) return;
      const now = new Date();
      const timeStr = formatters.formatTime(now);
      const dateStr = formatters.formatDate(now);

      if (clockRef.current && timeStr !== lastTimeRef.current) {
        lastTimeRef.current = timeStr;
        const target = clockRef.current.firstChild ?? clockRef.current;
        target.textContent = timeStr;
      }

      if (dateRef.current && dateStr !== lastDateRef.current) {
        lastDateRef.current = dateStr;
        const target = dateRef.current.firstChild ?? dateRef.current;
        target.textContent = dateStr;
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
      </div>
      <div className="header-date-stack" id="header-date-stack">
        <div className="header-date" id="header-date" ref={dateRef}>
          ------
        </div>
      </div>
    </div>
  );
}

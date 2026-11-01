import { memo, useCallback, useEffect, useMemo, useRef, useState, type CSSProperties } from 'react';
import { useDisplayStore } from '../../store';
import { useWidget } from '../../store/hooks';
import type { WidgetComponentProps } from '../registry';

export interface FamilyMember {
  name: string;
  initial: string;
  color: string;
  text_color: string;
  pattern: string;
  calendars?: string[];
}

export interface FamilyEvent {
  id: string;
  title: string;
  location?: string;
  description?: string;
  start?: string;
  end?: string;
  all_day?: boolean;
  owners?: string[];
  initials?: string[];
  colors?: string[];
  color?: string;
  text_color?: string;
  patterns?: string[];
  pattern?: string;
}

export interface FamilySpanEvent extends FamilyEvent {
  start_col: number;
  col_span: number;
}

export interface FamilyDayEvent extends FamilyEvent {
  top_pct: number;
  height_pct: number;
  left_pct: number;
  width_pct: number;
  is_early_edge?: boolean;
  is_late_edge?: boolean;
  col_span?: number;
  link_glyph?: string;
  is_linked?: boolean;
}

export interface TimelineMarker {
  label: string;
  top_pct: number;
  is_start?: boolean;
  is_end?: boolean;
}

export interface FamilyDay {
  date: string;
  weekday_abbr?: string;
  day_of_month: number;
  is_today: boolean;
  is_weekend: boolean;
  all_day?: FamilyEvent[];
  timed?: FamilyEvent[];
  overflow?: number;
  events?: FamilyDayEvent[];
  early_events?: FamilyDayEvent[];
  late_events?: FamilyDayEvent[];
}

export interface FamilyViewModel {
  range_start: string;
  range_end: string;
  range_label: string;
  today: string;
  hours_start?: string;
  hours_end?: string;
  hour_markers?: string[];
  timeline_markers?: TimelineMarker[];
  members?: FamilyMember[];
  days?: FamilyDay[];
  all_day_events?: FamilySpanEvent[];
  shared_color?: string;
  cached_range_start?: string;
  cached_range_end?: string;
}

export interface FamilyMonthDot {
  name: string;
  initial: string;
  color: string;
  text_color: string;
  pattern: string;
  is_shared: boolean;
  member_index: number;
}

export interface FamilyMonthDay {
  date: string;
  day_of_month: number;
  weekday_abbr?: string;
  is_current_month: boolean;
  is_today: boolean;
  is_weekend: boolean;
  dots?: FamilyMonthDot[];
  event_title?: string;
  events_count?: number;
  events?: FamilyEvent[];
}

export interface FamilyMonthWeek {
  week_number: number;
  days: FamilyMonthDay[];
}

export interface FamilyMonthViewModel {
  month_label: string;
  month_name: string;
  year: number;
  month: number;
  range_start: string;
  range_end: string;
  today: string;
  weekday_headers: string[];
  members?: FamilyMember[];
  weeks?: FamilyMonthWeek[];
  days?: FamilyMonthDay[];
  shared_color?: string;
  cached_range_start?: string;
  cached_range_end?: string;
}

export interface CalendarGridViewModel {
  week?: FamilyViewModel;
  month?: FamilyMonthViewModel;
  active_view?: string;
  cached_range_start?: string;
  cached_range_end?: string;
}

const SHORT_MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
const FULL_WEEKDAYS = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];
const FULL_MONTHS = [
  'January', 'February', 'March', 'April', 'May', 'June',
  'July', 'August', 'September', 'October', 'November', 'December',
];

function parseISODate(s: string): Date {
  const parts = s.split('T')[0].split('-');
  const y = parseInt(parts[0], 10);
  const m = parseInt(parts[1], 10);
  const d = parseInt(parts[2], 10);
  return new Date(y, m - 1, d, 12, 0, 0);
}

function toISODate(d: Date): string {
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, '0');
  const day = String(d.getDate()).padStart(2, '0');
  return `${y}-${m}-${day}`;
}

function addDays(d: Date, n: number): Date {
  const res = new Date(d.getTime());
  res.setDate(res.getDate() + n);
  return res;
}

function addMonths(d: Date, n: number): Date {
  const res = new Date(d.getTime());
  res.setDate(1);
  res.setMonth(res.getMonth() + n);
  return res;
}

function formatDayLabel(d: Date): string {
  return `${FULL_WEEKDAYS[d.getDay()]}, ${SHORT_MONTHS[d.getMonth()]} ${d.getDate()}`;
}

function formatWeekLabel(start: Date, end: Date): string {
  if (start.getFullYear() === end.getFullYear()) {
    if (start.getMonth() === end.getMonth()) {
      return `${SHORT_MONTHS[start.getMonth()]} ${start.getDate()} – ${end.getDate()}, ${start.getFullYear()}`;
    }
    return `${SHORT_MONTHS[start.getMonth()]} ${start.getDate()} – ${SHORT_MONTHS[end.getMonth()]} ${end.getDate()}, ${start.getFullYear()}`;
  }
  return `${SHORT_MONTHS[start.getMonth()]} ${start.getDate()}, ${start.getFullYear()} – ${SHORT_MONTHS[end.getMonth()]} ${end.getDate()}, ${end.getFullYear()}`;
}

function formatMonthLabel(d: Date): string {
  return `${FULL_MONTHS[d.getMonth()]} ${d.getFullYear()}`;
}

/**
 * Two-phase ResizeObserver helper for fitting day column events without layout thrashing.
 * Phase 1 reads all geometries into a plain array; Phase 2 toggles .cf-event-compact.
 */
export function batchFitDayColumnEvents(columnEl: HTMLElement): void {
  if (!columnEl || typeof columnEl.querySelectorAll !== 'function') return;
  const dayEvents = columnEl.querySelectorAll<HTMLElement>('.cf-day-event');
  if (dayEvents.length === 0) return;

  // Pre-pass: remove .cf-event-compact from all events in the column before reading geometries.
  // When .cf-event-compact is present, CSS sets .cf-event-header and .cf-event-time to display: none !important,
  // which makes offsetHeight/offsetWidth read as 0 and falsely reports that the event fits.
  for (let i = 0; i < dayEvents.length; i++) {
    dayEvents[i].classList.remove('cf-event-compact');
  }

  // Phase 1: Pure geometry reads
  const measurements: Array<{ el: HTMLElement; fits: boolean }> = [];
  for (let i = 0; i < dayEvents.length; i++) {
    const evEl = dayEvents[i];
    const timeEl = evEl.querySelector<HTMLElement>('.cf-event-time');
    const titleEl = evEl.querySelector<HTMLElement>('.cf-event-title');
    if (!timeEl || !titleEl) continue;

    const headerEl = evEl.querySelector<HTMLElement>('.cf-event-header');
    let fits = true;

    if (typeof evEl.clientHeight === 'number' && evEl.clientHeight > 0) {
      const headerH = (headerEl && typeof headerEl.offsetHeight === 'number' && headerEl.offsetHeight > 0)
        ? headerEl.offsetHeight
        : (typeof timeEl.offsetHeight === 'number' ? timeEl.offsetHeight : 0);
      const titleH = typeof titleEl.offsetHeight === 'number' ? titleEl.offsetHeight : 0;
      if (headerH > 0 && titleH > 0) {
        if (headerH + titleH + 2 > evEl.clientHeight) {
          fits = false;
        }
      } else if (typeof evEl.scrollHeight === 'number' && evEl.scrollHeight > evEl.clientHeight + 1) {
        const locEl = evEl.querySelector('.cf-event-location');
        if (!locEl) {
          fits = false;
        }
      }
    }

    if (fits && typeof evEl.clientWidth === 'number' && evEl.clientWidth > 0) {
      const timeW = typeof timeEl.offsetWidth === 'number' ? timeEl.offsetWidth : 0;
      const titleW = typeof titleEl.scrollWidth === 'number' ? titleEl.scrollWidth : (typeof titleEl.offsetWidth === 'number' ? titleEl.offsetWidth : 0);
      if (timeW > 0 && titleW > 0 && timeEl.offsetTop === titleEl.offsetTop) {
        if (timeW + titleW + 8 > evEl.clientWidth) {
          fits = false;
        }
      }
    }

    measurements.push({ el: evEl, fits });
  }

  // Phase 2: Pure class mutations
  for (let i = 0; i < measurements.length; i++) {
    const { el, fits } = measurements[i];
    if (fits) {
      el.classList.remove('cf-event-compact');
    } else {
      el.classList.add('cf-event-compact');
    }
  }
}

/**
 * Native React calendar grid and family planner widget.
 * Ports widgets/calendar-grid/views/widget.html and web/static/js/calendar_family.js.
 */
export const CalendarGridWidget = memo(function CalendarGridWidget({
  id,
}: WidgetComponentProps) {
  const entry = useWidget(id);
  const config = entry?.meta?.config as Record<string, unknown> | undefined;
  const data = entry?.data as Record<string, unknown> | undefined;
  const isError = entry?.state === 'error';

  // Resolve default view
  const rawDefault = (
    typeof config?.default_view === 'string'
      ? config.default_view
      : typeof entry?.viewModel?.active_view === 'string'
        ? entry.viewModel.active_view
        : 'week'
  ).toLowerCase();
  const defaultView: 'day' | 'week' | 'month' =
    rawDefault === 'month' ? 'month' : rawDefault === 'day' ? 'day' : 'week';

  const [activeView, setActiveView] = useState<'day' | 'week' | 'month'>(defaultView);
  const [activeModalEvent, setActiveModalEvent] = useState<FamilyEvent | null>(null);

  // Base anchor date
  const todayStr = useMemo(() => {
    const vmToday = (entry?.viewModel as Record<string, unknown> | undefined)?.week as Record<string, unknown> | undefined;
    if (typeof vmToday?.today === 'string') return vmToday.today;
    const vmMonth = (entry?.viewModel as Record<string, unknown> | undefined)?.month as Record<string, unknown> | undefined;
    if (typeof vmMonth?.today === 'string') return vmMonth.today;
    if (typeof data?.today === 'string') return data.today;
    return toISODate(new Date());
  }, [entry?.viewModel, data]);

  const baseDate = useMemo(() => parseISODate(todayStr), [todayStr]);

  const baseWeekStart = useMemo(() => {
    const vmWeek = (entry?.viewModel as Record<string, unknown> | undefined)?.week as Record<string, unknown> | undefined;
    if (typeof vmWeek?.range_start === 'string') {
      return parseISODate(vmWeek.range_start);
    }
    return baseDate;
  }, [entry?.viewModel, baseDate]);

  const [pagedDate, setPagedDate] = useState<Date | null>(null);
  const [pagedWeekStart, setPagedWeekStart] = useState<Date | null>(null);

  const currentDate = pagedDate ?? baseDate;
  const currentWeekStart = pagedWeekStart ?? baseWeekStart;

  // Current view model state & LRU cache
  const [currentViewModel, setCurrentViewModel] = useState<Record<string, unknown> | undefined>(
    entry?.viewModel
  );

  // Derive the week step length from the active/baseline week view (e.g. 3 or 5 days on narrow widgets, default 7)
  const weekStep = useMemo(() => {
    const currentDays = (currentViewModel?.week as FamilyViewModel | undefined)?.days;
    if (Array.isArray(currentDays) && currentDays.length > 0) return currentDays.length;
    const baseDays = ((entry?.viewModel as Record<string, unknown> | undefined)?.week as FamilyViewModel | undefined)?.days;
    if (Array.isArray(baseDays) && baseDays.length > 0) return baseDays.length;
    const cfgDays = Number(config?.days);
    if (Number.isFinite(cfgDays) && cfgDays > 0) return Math.round(cfgDays);
    return 7;
  }, [currentViewModel, entry?.viewModel, config?.days]);

  // Derived page offset relative to baseline date anchor of the active view
  const pageOffset = useMemo(() => {
    if (activeView === 'day') {
      const diffMs = currentDate.getTime() - baseDate.getTime();
      return Math.round(diffMs / 86400000);
    }
    if (activeView === 'month') {
      return (
        (currentDate.getFullYear() - baseDate.getFullYear()) * 12 +
        (currentDate.getMonth() - baseDate.getMonth())
      );
    }
    // Week view: step count between currentWeekStart and baseWeekStart using weekStep
    const diffMs = currentWeekStart.getTime() - baseWeekStart.getTime();
    if (diffMs === 0) return 0;
    const step = Math.round(diffMs / (weekStep * 86400000));
    return step === 0 ? (diffMs > 0 ? 1 : -1) : step;
  }, [activeView, currentDate, baseDate, currentWeekStart, baseWeekStart, weekStep]);

  const isAtBaseDate = pageOffset === 0;

  const lruCacheRef = useRef<Map<string, CalendarGridViewModel>>(new Map());
  const abortControllerRef = useRef<AbortController | null>(null);
  const [syncMessage, setSyncMessage] = useState<string | null>(null);
  const syncTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const showSyncIndicator = useCallback((msg: string) => {
    if (syncTimerRef.current) clearTimeout(syncTimerRef.current);
    setSyncMessage(msg);
    syncTimerRef.current = setTimeout(() => {
      setSyncMessage(null);
      syncTimerRef.current = null;
    }, 2500);
  }, []);

  // Timestamp update invalidates LRU cache
  const prevTimestampRef = useRef<string | undefined>(entry?.timestamp);
  useEffect(() => {
    if (prevTimestampRef.current !== entry?.timestamp) {
      prevTimestampRef.current = entry?.timestamp;
      lruCacheRef.current.clear();
      if (isAtBaseDate) {
        setCurrentViewModel(entry?.viewModel);
      }
    }
  }, [entry?.timestamp, entry?.viewModel, isAtBaseDate]);

  // If entry.viewModel changes when on baseline date, update currentViewModel
  useEffect(() => {
    if (isAtBaseDate && entry?.viewModel) {
      setCurrentViewModel(entry.viewModel);
    }
  }, [entry?.viewModel, isAtBaseDate]);

  // Bounds resolution
  const resolveCachedRange = useCallback(() => {
    const vm = currentViewModel as Record<string, unknown> | undefined;
    let start =
      (config?.cached_range_start as string) ||
      (vm?.cached_range_start as string) ||
      (data?.cached_range_start as string);
    let end =
      (config?.cached_range_end as string) ||
      (vm?.cached_range_end as string) ||
      (data?.cached_range_end as string);

    if (!start) {
      start = toISODate(addDays(baseDate, -90));
    }
    if (!end) {
      end = toISODate(addDays(baseDate, 180));
    }
    return { start, end };
  }, [currentViewModel, data, config, baseDate]);

  const abortInFlight = useCallback(() => {
    if (abortControllerRef.current) {
      abortControllerRef.current.abort();
      abortControllerRef.current = null;
    }
  }, []);

  // Fetch / LRU cache loader
  const loadDateView = useCallback(
    async (
      targetDate: Date,
      targetView: 'day' | 'week' | 'month',
      rollbackFn: () => void,
      syncWeekStart?: boolean
    ) => {
      abortInFlight();

      const dateStr = toISODate(targetDate);
      const viewStr = targetView;
      const cacheKey = `${dateStr}:${viewStr}`;

      const cache = lruCacheRef.current;
      if (cache.has(cacheKey)) {
        const cachedVM = cache.get(cacheKey)!;
        cache.delete(cacheKey);
        cache.set(cacheKey, cachedVM);
        setCurrentViewModel(cachedVM as Record<string, unknown>);
        if (syncWeekStart && cachedVM.week?.range_start) {
          const parsedWeekStart = parseISODate(cachedVM.week.range_start);
          setPagedWeekStart(parsedWeekStart);
          if (targetView === 'week') {
            setPagedDate(parsedWeekStart);
          }
        }
        return;
      }

      const ac = new AbortController();
      abortControllerRef.current = ac;

      try {
        let url = `api/widgets/${encodeURIComponent(id)}?date=${encodeURIComponent(dateStr)}&view=${encodeURIComponent(viewStr)}`;
        if (typeof window !== 'undefined' && window.location?.search) {
          const node = new URLSearchParams(window.location.search).get('node');
          if (node) {
            url += `&node=${encodeURIComponent(node)}`;
          }
        }

        const res = await fetch(url, { signal: ac.signal });
        if (ac.signal.aborted) return;
        if (!res.ok) {
          throw new Error(`Fetch failed with status ${res.status}`);
        }

        const body = await res.json();
        if (ac.signal.aborted) return;
        const vm = (body.view_model ?? body) as CalendarGridViewModel;

        if (cache.size >= 20) {
          const oldestKey = cache.keys().next().value;
          if (oldestKey !== undefined) {
            cache.delete(oldestKey);
          }
        }
        cache.set(cacheKey, vm);
        setCurrentViewModel(vm as Record<string, unknown>);
        if (syncWeekStart && vm.week?.range_start) {
          const parsedWeekStart = parseISODate(vm.week.range_start);
          setPagedWeekStart(parsedWeekStart);
          if (targetView === 'week') {
            setPagedDate(parsedWeekStart);
          }
        }
      } catch (err: unknown) {
        if (ac.signal.aborted || (err instanceof Error && err.name === 'AbortError')) {
          return;
        }
        showSyncIndicator('Sync failed');
        rollbackFn();
      } finally {
        if (abortControllerRef.current === ac) {
          abortControllerRef.current = null;
        }
      }
    },
    [id, showSyncIndicator, abortInFlight]
  );

  // Inactivity & Store pause/resume tracking
  const hasPausedStoreRef = useRef(false);
  const inactivityTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const inactivityContextRef = useRef({
    defaultView,
    baseDate,
    baseWeekStart,
    entryViewModel: entry?.viewModel,
    loadDateView,
  });
  inactivityContextRef.current = {
    defaultView,
    baseDate,
    baseWeekStart,
    entryViewModel: entry?.viewModel,
    loadDateView,
  };

  const clearInactivityTimer = useCallback(() => {
    if (inactivityTimerRef.current) {
      clearTimeout(inactivityTimerRef.current);
      inactivityTimerRef.current = null;
    }
  }, []);

  const resetInactivityTimer = useCallback(() => {
    clearInactivityTimer();
    inactivityTimerRef.current = setTimeout(() => {
      const {
        defaultView: defView,
        baseDate: bDate,
        entryViewModel,
        loadDateView: loadFn,
      } = inactivityContextRef.current;

      // Inactivity timeout: reset to default resting state and restore baseline view_model
      setActiveModalEvent(null);
      setActiveView(defView);
      setPagedDate(null);
      setPagedWeekStart(null);
      setSyncMessage(null);

      abortInFlight();

      if (entryViewModel) {
        setCurrentViewModel(entryViewModel);
      } else {
        loadFn(bDate, defView, () => {});
      }

      if (hasPausedStoreRef.current) {
        useDisplayStore.getState().resume();
        hasPausedStoreRef.current = false;
      }
    }, 60000);
  }, [clearInactivityTimer, abortInFlight]);

  // Only extend inactivity timer when widget is actively in an interactive state
  const handleUserActivity = useCallback(() => {
    if (hasPausedStoreRef.current || inactivityTimerRef.current !== null) {
      resetInactivityTimer();
    }
  }, [resetInactivityTimer]);

  const isInteractive = Boolean(activeModalEvent) || activeView !== defaultView || pageOffset !== 0;

  useEffect(() => {
    if (isInteractive) {
      if (!hasPausedStoreRef.current) {
        useDisplayStore.getState().pause();
        hasPausedStoreRef.current = true;
      }
      resetInactivityTimer();
    } else {
      clearInactivityTimer();
      if (hasPausedStoreRef.current) {
        useDisplayStore.getState().resume();
        hasPausedStoreRef.current = false;
      }
    }
  }, [isInteractive, resetInactivityTimer, clearInactivityTimer]);

  // Unmount cleanup
  useEffect(() => {
    return () => {
      clearInactivityTimer();
      if (syncTimerRef.current) {
        clearTimeout(syncTimerRef.current);
      }
      abortInFlight();
      if (hasPausedStoreRef.current) {
        useDisplayStore.getState().resume();
        hasPausedStoreRef.current = false;
      }
    };
  }, [clearInactivityTimer, abortInFlight]);

  // Paging handlers
  const pageNext = useCallback(() => {
    handleUserActivity();
    const cached = resolveCachedRange();
    const prevPagedDate = pagedDate;
    const prevPagedWeekStart = pagedWeekStart;

    const rollbackFn = () => {
      setPagedDate(prevPagedDate);
      setPagedWeekStart(prevPagedWeekStart);
    };

    if (activeView === 'day') {
      const nextDate = addDays(currentDate, 1);
      const nextDateStr = toISODate(nextDate);
      if (nextDateStr > cached.end) {
        showSyncIndicator('Not synced yet');
        return;
      }
      if (nextDateStr === toISODate(baseDate) && entry?.viewModel) {
        abortInFlight();
        setPagedDate(null);
        setPagedWeekStart(null);
        setCurrentViewModel(entry.viewModel);
        return;
      }
      setPagedDate(nextDate);
      loadDateView(nextDate, 'day', rollbackFn);
    } else if (activeView === 'week') {
      const step = weekStep;
      const nextStart = addDays(currentWeekStart, step);
      const nextStartStr = toISODate(nextStart);
      if (nextStartStr > cached.end) {
        showSyncIndicator('Not synced yet');
        return;
      }
      if (nextStartStr === toISODate(baseWeekStart) && entry?.viewModel) {
        abortInFlight();
        setPagedDate(null);
        setPagedWeekStart(null);
        setCurrentViewModel(entry.viewModel);
        return;
      }
      setPagedWeekStart(nextStart);
      setPagedDate(nextStart);
      loadDateView(nextStart, 'week', rollbackFn);
    } else if (activeView === 'month') {
      const nextMonth = addMonths(currentDate, 1);
      const nextMonthStr = toISODate(nextMonth).slice(0, 7);
      if (nextMonthStr > cached.end.slice(0, 7)) {
        showSyncIndicator('Not synced yet');
        return;
      }
      if (
        nextMonth.getFullYear() === baseDate.getFullYear() &&
        nextMonth.getMonth() === baseDate.getMonth() &&
        entry?.viewModel
      ) {
        abortInFlight();
        setPagedDate(null);
        setPagedWeekStart(null);
        setCurrentViewModel(entry.viewModel);
        return;
      }
      setPagedDate(nextMonth);
      loadDateView(nextMonth, 'month', rollbackFn);
    }
  }, [
    activeView,
    currentDate,
    currentWeekStart,
    pagedDate,
    pagedWeekStart,
    baseDate,
    baseWeekStart,
    weekStep,
    entry?.viewModel,
    resolveCachedRange,
    loadDateView,
    abortInFlight,
    showSyncIndicator,
    handleUserActivity,
  ]);

  const pagePrev = useCallback(() => {
    handleUserActivity();
    const cached = resolveCachedRange();
    const prevPagedDate = pagedDate;
    const prevPagedWeekStart = pagedWeekStart;

    const rollbackFn = () => {
      setPagedDate(prevPagedDate);
      setPagedWeekStart(prevPagedWeekStart);
    };

    if (activeView === 'day') {
      const prevDateTarget = addDays(currentDate, -1);
      const prevDateStr = toISODate(prevDateTarget);
      if (prevDateStr < cached.start) {
        showSyncIndicator('Not synced yet');
        return;
      }
      if (prevDateStr === toISODate(baseDate) && entry?.viewModel) {
        abortInFlight();
        setPagedDate(null);
        setPagedWeekStart(null);
        setCurrentViewModel(entry.viewModel);
        return;
      }
      setPagedDate(prevDateTarget);
      loadDateView(prevDateTarget, 'day', rollbackFn);
    } else if (activeView === 'week') {
      const step = weekStep;
      const targetStart = addDays(currentWeekStart, -step);
      const targetEnd = addDays(targetStart, step - 1);
      const targetEndStr = toISODate(targetEnd);
      if (targetEndStr < cached.start) {
        showSyncIndicator('Not synced yet');
        return;
      }
      if (toISODate(targetStart) === toISODate(baseWeekStart) && entry?.viewModel) {
        abortInFlight();
        setPagedDate(null);
        setPagedWeekStart(null);
        setCurrentViewModel(entry.viewModel);
        return;
      }
      setPagedWeekStart(targetStart);
      setPagedDate(targetStart);
      loadDateView(targetStart, 'week', rollbackFn);
    } else if (activeView === 'month') {
      const targetMonth = addMonths(currentDate, -1);
      const targetMonthStr = toISODate(targetMonth).slice(0, 7);
      if (targetMonthStr < cached.start.slice(0, 7)) {
        showSyncIndicator('Not synced yet');
        return;
      }
      if (
        targetMonth.getFullYear() === baseDate.getFullYear() &&
        targetMonth.getMonth() === baseDate.getMonth() &&
        entry?.viewModel
      ) {
        abortInFlight();
        setPagedDate(null);
        setPagedWeekStart(null);
        setCurrentViewModel(entry.viewModel);
        return;
      }
      setPagedDate(targetMonth);
      loadDateView(targetMonth, 'month', rollbackFn);
    }
  }, [
    activeView,
    currentDate,
    currentWeekStart,
    pagedDate,
    pagedWeekStart,
    baseDate,
    baseWeekStart,
    weekStep,
    entry?.viewModel,
    resolveCachedRange,
    loadDateView,
    abortInFlight,
    showSyncIndicator,
    handleUserActivity,
  ]);

  const resetDate = useCallback(() => {
    setPagedDate(null);
    setPagedWeekStart(null);
    setSyncMessage(null);

    abortInFlight();

    // At base offset, restore baseline viewModel without redundant network fetch
    if (entry?.viewModel) {
      setCurrentViewModel(entry.viewModel);
    } else {
      loadDateView(baseDate, activeView, () => {});
    }
    handleUserActivity();
  }, [baseDate, activeView, entry?.viewModel, loadDateView, abortInFlight, handleUserActivity]);

  const handleViewSwitch = useCallback(
    (targetView: 'day' | 'week' | 'month') => {
      if (targetView === activeView) return;
      const prevActiveView = activeView;
      setActiveView(targetView);
      handleUserActivity();

      const currentISO = toISODate(currentDate);
      const baseDateISO = toISODate(baseDate);
      const baseWeekStartISO = toISODate(baseWeekStart);
      const baseWeekEndISO = toISODate(addDays(baseWeekStart, weekStep - 1));

      const isTargetAtBase =
        targetView === 'day'
          ? currentISO === baseDateISO
          : targetView === 'week'
            ? currentISO >= baseWeekStartISO && currentISO <= baseWeekEndISO
            : currentDate.getFullYear() === baseDate.getFullYear() &&
              currentDate.getMonth() === baseDate.getMonth();

      // When target view's anchor date is at its baseline, restore baseline anchors and entry.viewModel without fetching
      if (isTargetAtBase) {
        const baseVM = (entry?.viewModel ?? (isAtBaseDate ? currentViewModel : undefined)) as
          | Record<string, unknown>
          | undefined;
        const hasMonth = Boolean(baseVM?.month);
        const hasWeek = Boolean(baseVM?.week);
        if (
          (targetView === 'month' && hasMonth) ||
          ((targetView === 'week' || targetView === 'day') && hasWeek)
        ) {
          abortInFlight();
          setPagedDate(null);
          setPagedWeekStart(null);
          if (baseVM) {
            setCurrentViewModel(baseVM);
          }
          return;
        }
      }

      const prevPagedDate = pagedDate;
      const prevPagedWeekStart = pagedWeekStart;
      const fetchAnchorDate = isTargetAtBase ? baseDate : currentDate;
      if (isTargetAtBase) {
        setPagedDate(null);
        setPagedWeekStart(null);
      } else if (targetView === 'week') {
        setPagedWeekStart(currentDate);
      }
      loadDateView(
        fetchAnchorDate,
        targetView,
        () => {
          setActiveView(prevActiveView);
          setPagedDate(prevPagedDate);
          setPagedWeekStart(prevPagedWeekStart);
        },
        !isTargetAtBase
      );
    },
    [
      activeView,
      currentDate,
      baseDate,
      baseWeekStart,
      weekStep,
      isAtBaseDate,
      pagedDate,
      pagedWeekStart,
      currentViewModel,
      entry?.viewModel,
      loadDateView,
      abortInFlight,
      handleUserActivity,
    ]
  );

  // Modal open/close
  const openModal = useCallback(
    (ev: FamilyEvent) => {
      setActiveModalEvent(ev);
      handleUserActivity();
    },
    [handleUserActivity]
  );

  const closeModal = useCallback(() => {
    setActiveModalEvent(null);
    handleUserActivity();
  }, [handleUserActivity]);

  // Escape key listener for modal
  useEffect(() => {
    if (!activeModalEvent) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        closeModal();
      }
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [activeModalEvent, closeModal]);

  // Touch swipe navigation
  const touchStartXRef = useRef<number>(0);
  const touchStartYRef = useRef<number>(0);
  const touchStartTimeRef = useRef<number>(0);
  const isSwipingRef = useRef<boolean>(false);

  const handleTouchStart = (e: React.TouchEvent) => {
    handleUserActivity();
    if (activeModalEvent) return;
    const t = e.touches[0];
    touchStartXRef.current = t.clientX;
    touchStartYRef.current = t.clientY;
    touchStartTimeRef.current = Date.now();
    isSwipingRef.current = true;
  };

  const handleTouchMove = () => {
    handleUserActivity();
  };

  const handleTouchEnd = (e: React.TouchEvent) => {
    handleUserActivity();
    if (!isSwipingRef.current || activeModalEvent) {
      isSwipingRef.current = false;
      return;
    }
    isSwipingRef.current = false;
    const t = e.changedTouches[0];
    const deltaX = t.clientX - touchStartXRef.current;
    const deltaY = t.clientY - touchStartYRef.current;
    const duration = Math.max(Date.now() - touchStartTimeRef.current, 1);
    const absDeltaX = Math.abs(deltaX);
    const absDeltaY = Math.abs(deltaY);
    const velocity = absDeltaX / duration;

    const isFlick = absDeltaX >= 40 && velocity >= 0.15 && absDeltaX > absDeltaY * 1.5;
    const isDrag = absDeltaX >= 50 && duration < 800 && absDeltaX > absDeltaY * 1.5;

    if (isFlick || isDrag) {
      if (deltaX < 0) {
        pageNext();
      } else {
        pagePrev();
      }
    }
  };

  // View models
  const weekVM = currentViewModel?.week as FamilyViewModel | undefined;
  const monthVM = currentViewModel?.month as FamilyMonthViewModel | undefined;

  // Member CSS variables
  const memberCssVars = useMemo(() => {
    const vars: Record<string, string> = {};
    const sharedColor = weekVM?.shared_color || monthVM?.shared_color || '#3D405B';
    vars['--mm-cf-shared'] = sharedColor;
    const members = weekVM?.members || monthVM?.members || [];
    members.forEach((m, i) => {
      if (m.color) vars[`--mm-member-${i}`] = m.color;
      if (m.text_color) vars[`--mm-member-${i}-text`] = m.text_color;
    });
    return vars as CSSProperties;
  }, [weekVM, monthVM]);

  // Date range labels
  let rangeLabelWeek = weekVM?.range_label || '';
  if (!rangeLabelWeek && currentWeekStart) {
    const daysLen = weekVM?.days?.length || 7;
    const weekEnd = addDays(currentWeekStart, daysLen - 1);
    rangeLabelWeek = formatWeekLabel(currentWeekStart, weekEnd);
  }

  let rangeLabelMonth = monthVM?.month_label || '';
  if (!rangeLabelMonth && currentDate) {
    rangeLabelMonth = formatMonthLabel(currentDate);
  }

  const rangeLabelDay = formatDayLabel(currentDate);
  const currentDateISO = toISODate(currentDate);

  // Day columns to render
  const daysToRender: FamilyDay[] = useMemo(() => {
    const allDays = weekVM?.days || [];
    if (allDays.length === 0) return [];
    if (activeView === 'day') {
      const match = allDays.find((d) => d.date === currentDateISO);
      return match ? [match] : [allDays[0]];
    }
    return allDays;
  }, [weekVM?.days, activeView, currentDateISO]);

  // Pre-filtered all-day events for active view (ensures day view only renders events on the active day)
  const visibleAllDayEvents = useMemo(() => {
    if (!weekVM?.all_day_events || weekVM.all_day_events.length === 0) return [];
    if (activeView !== 'day') return weekVM.all_day_events;
    const dayDate = daysToRender[0]?.date || currentDateISO;
    const dayIdx = (weekVM.days || []).findIndex((d) => d.date === dayDate);
    if (dayIdx < 0) return [];
    return weekVM.all_day_events.filter(
      (ev) => ev.start_col <= dayIdx + 1 && ev.start_col + ev.col_span > dayIdx + 1
    );
  }, [weekVM?.all_day_events, weekVM?.days, activeView, daysToRender, currentDateISO]);

  // ResizeObserver registration per day canvas column
  const canvasRefs = useRef<Map<string, HTMLElement>>(new Map());
  const registerDayCanvas = useCallback((date: string, el: HTMLElement | null) => {
    if (el) {
      canvasRefs.current.set(date, el);
      batchFitDayColumnEvents(el);
    } else {
      canvasRefs.current.delete(date);
    }
  }, []);

  useEffect(() => {
    if (typeof ResizeObserver === 'undefined') return;
    const observers: ResizeObserver[] = [];
    canvasRefs.current.forEach((canvasEl) => {
      const ro = new ResizeObserver(() => {
        batchFitDayColumnEvents(canvasEl);
      });
      ro.observe(canvasEl);
      observers.push(ro);
    });
    return () => {
      observers.forEach((ro) => ro.disconnect());
    };
  }, [currentViewModel, activeView, daysToRender]);

  const customTitle =
    typeof config?.title === 'string' && config.title.trim() !== ''
      ? config.title.trim()
      : 'Calendar';

  const members = weekVM?.members || monthVM?.members || [];

  return (
    <div
      className={`widget-calendar-grid widget-calendar-family cf-view-${activeView}`}
      data-widget-id={id}
      data-widget-type="calendar-grid"
      data-default-view={defaultView}
      data-active-view={activeView}
      data-current-date={currentDateISO}
      data-page-offset={String(pageOffset)}
      data-prevent-screen-swipe="true"
      style={memberCssVars}
      onTouchStart={handleTouchStart}
      onTouchMove={handleTouchMove}
      onTouchEnd={handleTouchEnd}
      onPointerDown={handleUserActivity}
    >
      {isError && (
        <div className="widget-error-banner">
          <span className="error-icon">⚠</span>
          <span>Widget Error</span>
        </div>
      )}

      <div className="cf-header" style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', width: '100%', paddingBottom: '8px', marginBottom: '8px', borderBottom: '1px solid var(--mm-divider, rgba(90, 60, 30, 0.08))' }}>
        <div className="cf-header-left" style={{ display: 'flex', alignItems: 'center', gap: '12px', minWidth: 0 }}>
          <div className="cf-title-badge" style={{ width: '36px', height: '36px', borderRadius: '11px', background: 'rgba(139, 92, 246, 0.12)', color: 'var(--mm-lavender, #8b60ed)', display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0 }}>
            <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <rect x="3" y="4" width="18" height="18" rx="2" ry="2" />
              <line x1="16" y1="2" x2="16" y2="6" />
              <line x1="8" y1="2" x2="8" y2="6" />
              <line x1="3" y1="10" x2="21" y2="10" />
            </svg>
          </div>

          <h2 className="cf-title" style={{ margin: 0, fontSize: '1.1rem', fontWeight: 800, color: 'var(--mm-text-title, #2a2338)', lineHeight: 1, flexShrink: 0 }}>
            {customTitle}
          </h2>

          <div className="cf-date-row cf-date-pill" style={{ display: 'inline-flex', alignItems: 'center', gap: '2px', background: 'rgba(90, 60, 30, 0.04)', border: '1px solid var(--mm-card-border, rgba(90, 60, 30, 0.08))', borderRadius: '9999px', padding: '2px 4px', flexShrink: 0 }}>
            <button type="button" className="cf-nav-btn cf-prev-btn" aria-label="Previous date range" title="Previous" onClick={pagePrev}>
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.6" strokeLinecap="round" strokeLinejoin="round"><polyline points="15 18 9 12 15 6" /></svg>
            </button>
            <div className="cf-subtitle" style={{ display: 'flex', alignItems: 'center', padding: '0 6px', fontSize: '0.80rem', fontWeight: 700, color: 'var(--mm-text-title, #2a2338)', whiteSpace: 'nowrap' }}>
              <span className="cf-range-label cf-label-week" style={{ display: activeView === 'week' ? undefined : 'none', fontWeight: 700 }}>
                {rangeLabelWeek}
              </span>
              <span className="cf-range-label cf-label-month" style={{ display: activeView === 'month' ? undefined : 'none', fontWeight: 700 }}>
                {rangeLabelMonth}
              </span>
              <span className="cf-range-label cf-label-day" style={{ display: activeView === 'day' ? undefined : 'none', fontWeight: 700 }}>
                {rangeLabelDay}
              </span>
            </div>
            <button type="button" className="cf-nav-btn cf-next-btn" aria-label="Next date range" title="Next" onClick={pageNext}>
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.6" strokeLinecap="round" strokeLinejoin="round"><polyline points="9 18 15 12 9 6" /></svg>
            </button>
            <button
              type="button"
              className="cf-today-badge"
              title="Return to today"
              style={{ display: pageOffset !== 0 ? undefined : 'none' }}
              onClick={resetDate}
            >
              TODAY
            </button>
          </div>

          {syncMessage && (
            <span className="cf-sync-indicator" role="status" aria-live="polite">
              {syncMessage}
            </span>
          )}
        </div>

        <div className="cf-header-right" style={{ display: 'flex', alignItems: 'center', gap: '12px', flexShrink: 0 }}>
          {members.length > 0 && (
            <div className="cf-legend" style={{ display: 'flex', alignItems: 'center', gap: '6px', flexWrap: 'wrap' }}>
              {members.map((m, i) => (
                <div
                  key={m.name || i}
                  className={`cf-member-chip cf-pattern-${m.pattern || 'solid'}`}
                  title={m.name}
                  style={{ display: 'inline-flex', alignItems: 'center', gap: '5px', background: 'rgba(90, 60, 30, 0.04)', padding: '3px 9px', borderRadius: '9999px', border: '1px solid var(--mm-card-border, rgba(90, 60, 30, 0.08))' }}
                >
                  <span
                    className="cf-member-dot"
                    style={{ display: 'inline-block', width: '7px', height: '7px', borderRadius: '50%', background: `var(--mm-member-${i}, ${m.color})`, flexShrink: 0 }}
                  />
                  <span className="cf-member-name" style={{ fontSize: '0.70rem', fontWeight: 700, color: 'var(--mm-text-primary, #2a2338)', lineHeight: 1 }}>
                    {m.name}
                  </span>
                </div>
              ))}
            </div>
          )}

          <div className="cf-view-switcher" role="tablist" aria-label="Calendar view">
            <button
              type="button"
              className={`cf-view-btn${activeView === 'week' ? ' active' : ''}`}
              data-view="week"
              role="tab"
              aria-selected={activeView === 'week' ? 'true' : 'false'}
              onClick={() => handleViewSwitch('week')}
            >
              Week
            </button>
            <button
              type="button"
              className={`cf-view-btn${activeView === 'month' ? ' active' : ''}`}
              data-view="month"
              role="tab"
              aria-selected={activeView === 'month' ? 'true' : 'false'}
              onClick={() => handleViewSwitch('month')}
            >
              Month
            </button>
          </div>
        </div>
      </div>

      {/* Week / Day View Panel */}
      <div
        className="cf-view-panel cf-view-week"
        style={{
          display: activeView !== 'month' ? 'flex' : 'none',
          flex: 1,
          minHeight: 0,
          flexDirection: 'column',
        }}
      >
        {daysToRender.length > 0 && members.length > 0 ? (
          <>
            {/* All Day Events Band */}
            {visibleAllDayEvents.length > 0 ? (
              <div
                className="cf-allday-band cf-week-allday-grid"
                style={{
                  marginLeft: (weekVM?.timeline_markers?.length || weekVM?.hour_markers?.length) ? '44px' : undefined,
                  display: 'grid',
                  gridTemplateColumns: `repeat(${daysToRender.length}, 1fr)`,
                  gridAutoFlow: 'dense',
                  gap: '6px',
                  paddingBottom: '6px',
                  marginBottom: '6px',
                  borderBottom: '1px dashed var(--mm-divider, rgba(90, 60, 30, 0.1))',
                }}
              >
                {visibleAllDayEvents.map((ev) => {
                    const isMealie = ev.location?.startsWith('/hearth-api/mealie/image/');
                    return (
                      <div
                        key={ev.id}
                        className={`cf-event cf-allday cf-event-clickable${ev.pattern ? ` cf-pattern-${ev.pattern}` : ''}`}
                        data-event-id={ev.id}
                        data-event-title={ev.title}
                        data-event-allday="true"
                        data-event-location={ev.location}
                        data-event-description={ev.description}
                        data-mealie-decorated={isMealie ? 'true' : undefined}
                        onClick={() => openModal(ev)}
                        style={{
                          gridColumn: activeView === 'day' ? '1 / span 1' : `${ev.start_col} / span ${ev.col_span}`,
                          fontSize: '0.68rem',
                          fontWeight: 700,
                          borderRadius: '6px',
                          padding: '3px 6px',
                          overflow: 'hidden',
                          textOverflow: 'ellipsis',
                          whiteSpace: 'nowrap',
                          color: ev.text_color,
                          backgroundColor: ev.color,
                          cursor: 'pointer',
                          touchAction: 'manipulation',
                          display: isMealie ? 'flex' : undefined,
                          alignItems: isMealie ? 'center' : undefined,
                          gap: isMealie ? '6px' : undefined,
                        }}
                      >
                        {isMealie ? (
                          <>
                            <img
                              src={ev.location}
                              alt=""
                              style={{ width: '26px', height: '26px', borderRadius: '5px', objectFit: 'cover', flexShrink: 0, boxShadow: '0 1px 3px rgba(0,0,0,0.3)' }}
                            />
                            <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontWeight: 800 }}>
                              {`🍽️ ${ev.title.replace(/^🍽️\s*/, '')}`}
                            </span>
                          </>
                        ) : (
                          ev.title
                        )}
                      </div>
                    );
                  })}
              </div>
            ) : daysToRender.some((d) => d.all_day && d.all_day.length > 0) ? (
              <div
                className="cf-allday-band cf-week-allday-grid"
                style={{
                  marginLeft: (weekVM?.timeline_markers?.length || weekVM?.hour_markers?.length) ? '44px' : undefined,
                  display: 'grid',
                  gridTemplateColumns: `repeat(${daysToRender.length}, 1fr)`,
                  gap: '6px',
                  paddingBottom: '6px',
                  marginBottom: '6px',
                  borderBottom: '1px dashed var(--mm-divider, rgba(90, 60, 30, 0.1))',
                }}
              >
                {daysToRender.map((day) => (
                  <div key={day.date} className="cf-day-allday-col" style={{ display: 'flex', flexDirection: 'column', gap: '2px' }}>
                    {day.all_day?.map((ev) => {
                      const isMealie = ev.location?.startsWith('/hearth-api/mealie/image/');
                      return (
                        <div key={ev.id} className="cf-edge-band cf-allday-band-local" style={{ display: 'flex', flexDirection: 'column', gap: '2px', padding: '2px 4px', background: 'rgba(90, 60, 30, 0.05)', borderRadius: '6px', border: '1px dashed var(--mm-divider, rgba(90, 60, 30, 0.12))', flexShrink: 0 }}>
                          <div
                            className={`cf-event cf-allday cf-event-clickable${ev.pattern ? ` cf-pattern-${ev.pattern}` : ''}`}
                            role="button"
                            tabIndex={0}
                            title={ev.title}
                            aria-label={`${ev.title} (All Day)`}
                            data-event-id={ev.id}
                            data-event-title={ev.title}
                            data-event-allday="true"
                            data-event-location={ev.location}
                            data-event-description={ev.description}
                            data-mealie-decorated={isMealie ? 'true' : undefined}
                            onClick={() => openModal(ev)}
                            style={{
                              fontSize: '0.65rem',
                              fontWeight: 700,
                              borderRadius: '4px',
                              padding: '2px 4px',
                              color: ev.text_color,
                              backgroundColor: ev.color,
                              cursor: 'pointer',
                              touchAction: 'manipulation',
                              overflow: 'hidden',
                              textOverflow: 'ellipsis',
                              whiteSpace: 'nowrap',
                              display: isMealie ? 'flex' : undefined,
                              alignItems: isMealie ? 'center' : undefined,
                              gap: isMealie ? '6px' : undefined,
                            }}
                          >
                            {isMealie ? (
                              <>
                                <img
                                  src={ev.location}
                                  alt=""
                                  style={{ width: '26px', height: '26px', borderRadius: '5px', objectFit: 'cover', flexShrink: 0, boxShadow: '0 1px 3px rgba(0,0,0,0.3)' }}
                                />
                                <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontWeight: 800 }}>
                                  {`🍽️ ${ev.title.replace(/^🍽️\s*/, '')}`}
                                </span>
                              </>
                            ) : (
                              ev.title
                            )}
                          </div>
                        </div>
                      );
                    })}
                  </div>
                ))}
              </div>
            ) : null}

            {/* Early Events Grid */}
            {daysToRender.some((d) => d.early_events && d.early_events.length > 0) && (
              <div
                className="cf-edge-band cf-week-early-grid"
                style={{
                  marginLeft: (weekVM?.timeline_markers?.length || weekVM?.hour_markers?.length) ? '44px' : undefined,
                  display: 'grid',
                  gridTemplateColumns: `repeat(${daysToRender.length}, 1fr)`,
                  gap: '6px',
                  paddingBottom: '4px',
                  marginBottom: '4px',
                  borderBottom: '1px dashed var(--mm-divider, rgba(90, 60, 30, 0.1))',
                }}
              >
                {daysToRender.map((day) => (
                  <div key={day.date} className="cf-day-early-col" style={{ display: 'flex', flexDirection: 'column', gap: '2px' }}>
                    {day.early_events?.map((ev) => (
                      <div key={ev.id} className="cf-edge-band cf-early-band" style={{ display: 'flex', flexDirection: 'column', gap: '2px', padding: '2px 4px', background: 'rgba(90, 60, 30, 0.05)', borderRadius: '6px', border: '1px dashed var(--mm-divider, rgba(90, 60, 30, 0.12))', flexShrink: 0 }}>
                        <div
                          className={`cf-edge-event cf-event-clickable${ev.pattern ? ` cf-pattern-${ev.pattern}` : ''}`}
                          role="button"
                          tabIndex={0}
                          title={`Early event: ${ev.start} ${ev.title}${ev.location ? ` (${ev.location})` : ''}`}
                          aria-label={`Early event: ${ev.title} at ${ev.start}`}
                          data-event-id={ev.id}
                          data-event-title={ev.title}
                          data-event-start={ev.start}
                          data-event-end={ev.end}
                          data-event-location={ev.location}
                          data-event-description={ev.description}
                          onClick={() => openModal(ev)}
                          style={{
                            fontSize: '0.60rem',
                            fontWeight: 700,
                            padding: '2px 4px',
                            borderRadius: '4px',
                            backgroundColor: ev.color,
                            color: ev.text_color,
                            overflow: 'hidden',
                            textOverflow: 'ellipsis',
                            whiteSpace: 'nowrap',
                            cursor: 'pointer',
                            touchAction: 'manipulation',
                          }}
                        >
                          ▲ {ev.start} {ev.title}
                        </div>
                      </div>
                    ))}
                  </div>
                ))}
              </div>
            )}

            {/* Week / Day Body */}
            <div className="cf-week-body" style={{ display: 'flex', flex: 1, minHeight: 0, gap: '6px', position: 'relative' }}>
              {(weekVM?.timeline_markers?.length || weekVM?.hour_markers?.length) ? (
                <div className="cf-timeline-axis" style={{ width: '38px', display: 'flex', flexDirection: 'column', flexShrink: 0, userSelect: 'none' }}>
                  <div className="cf-timeline-spacer" style={{ height: '44px', flexShrink: 0 }} />
                  <div className="cf-timeline-markers" style={{ flex: 1, position: 'relative', minHeight: 0, marginBottom: '6px', fontFamily: 'var(--mm-font-mono, monospace)', fontSize: '0.60rem', fontWeight: 700, color: 'var(--mm-text-dim, #8b819f)' }}>
                    {weekVM.timeline_markers && weekVM.timeline_markers.length > 0 ? (
                      weekVM.timeline_markers.map((marker, idx) => (
                        <span
                          key={idx}
                          className="cf-hour-marker"
                          style={{
                            position: 'absolute',
                            right: '6px',
                            top: `${marker.top_pct}%`,
                            transform: marker.is_start ? 'translateY(0)' : marker.is_end ? 'translateY(-100%)' : 'translateY(-50%)',
                            lineHeight: 1,
                            whiteSpace: 'nowrap',
                            pointerEvents: 'none',
                          }}
                        >
                          {marker.label}
                        </span>
                      ))
                    ) : (
                      <div style={{ height: '100%', display: 'flex', flexDirection: 'column', justifyContent: 'space-between', textAlign: 'right', paddingRight: '6px' }}>
                        {weekVM.hour_markers?.map((marker, idx) => (
                          <span key={idx} className="cf-hour-marker" style={{ lineHeight: 1 }}>
                            {marker}
                          </span>
                        ))}
                      </div>
                    )}
                  </div>
                </div>
              ) : null}

              <div className="cf-week-grid" style={{ display: 'grid', gridTemplateColumns: `repeat(${daysToRender.length}, 1fr)`, gap: '6px', flex: 1, minHeight: 0 }}>
                {daysToRender.map((day) => (
                  <div
                    key={day.date}
                    className={`cf-day${day.is_today ? ' cf-today' : ''}${day.is_weekend ? ' cf-weekend' : ''}`}
                    data-date={day.date}
                    style={{
                      display: 'flex',
                      flexDirection: 'column',
                      minWidth: 0,
                      minHeight: 0,
                      borderRadius: '14px',
                      padding: '6px',
                      background: day.is_today
                        ? 'var(--mm-cal-today-bg, var(--mm-lavender-light, #f3effe))'
                        : day.is_weekend
                        ? 'var(--mm-cal-weekend-bg, rgba(90, 60, 30, 0.02))'
                        : 'var(--mm-cal-day-bg, rgba(90, 60, 30, 0.03))',
                      border: day.is_today
                        ? '1px solid var(--mm-cal-today-border, var(--mm-lavender, #8b60ed))'
                        : '1px solid var(--mm-card-border, rgba(90, 60, 30, 0.08))',
                      overflow: 'hidden',
                      position: 'relative',
                    }}
                  >
                    <div
                      className="cf-day-header"
                      style={{
                        height: '44px',
                        boxSizing: 'border-box',
                        textAlign: 'center',
                        paddingBottom: '6px',
                        borderBottom: day.is_today
                          ? '1px solid var(--mm-cal-today-header-border, rgba(139, 96, 237, 0.25))'
                          : '1px solid var(--mm-divider, rgba(90, 60, 30, 0.07))',
                        flexShrink: 0,
                        background: 'transparent',
                      }}
                    >
                      <div
                        className="cf-day-weekday"
                        style={{
                          fontSize: '0.68rem',
                          fontWeight: 800,
                          textTransform: 'uppercase',
                          letterSpacing: '0.04em',
                          color: day.is_today
                            ? 'var(--mm-cal-today-text, var(--mm-lavender, #8b60ed))'
                            : 'var(--mm-text-dim, #8b819f)',
                        }}
                      >
                        {day.is_today ? 'Today' : day.weekday_abbr || ''}
                      </div>
                      <div
                        className="cf-day-num"
                        style={{
                          fontSize: '1.15rem',
                          fontWeight: 900,
                          lineHeight: 1.1,
                          color: day.is_today
                            ? 'var(--mm-cal-today-text, var(--mm-lavender, #8b60ed))'
                            : 'var(--mm-text-title, #2a2338)',
                        }}
                      >
                        {day.day_of_month}
                      </div>
                    </div>

                    <div
                      className="cf-day-canvas"
                      ref={(el) => registerDayCanvas(day.date, el)}
                      style={{ flex: 1, position: 'relative', minHeight: 0, marginTop: '4px' }}
                    >
                      {day.events?.map((ev) => (
                        <div
                          key={ev.id}
                          className={`cf-day-event cf-event-clickable${ev.pattern ? ` cf-pattern-${ev.pattern}` : ''}`}
                          role="button"
                          tabIndex={0}
                          title={`${ev.start}–${ev.end}: ${ev.title}${ev.location ? ` (${ev.location})` : ''}`}
                          aria-label={`${ev.title}, ${ev.start} to ${ev.end}${ev.location ? `, ${ev.location}` : ''}`}
                          data-event-id={ev.id}
                          data-event-title={ev.title}
                          data-event-start={ev.start}
                          data-event-end={ev.end}
                          data-event-location={ev.location}
                          data-event-description={ev.description}
                          onClick={() => openModal(ev)}
                          style={{
                            position: 'absolute',
                            top: `${ev.top_pct}%`,
                            height: `${ev.height_pct}%`,
                            left: `calc(${ev.left_pct}% + 1px)`,
                            width: `calc(${ev.width_pct}% - 2px)`,
                            minHeight: '20px',
                            padding: '2px 4px',
                            borderRadius: '6px',
                            backgroundColor: ev.color,
                            color: ev.text_color,
                            overflow: 'hidden',
                            display: 'flex',
                            flexDirection: 'column',
                            boxSizing: 'border-box',
                            border: '1px solid rgba(0, 0, 0, 0.12)',
                            boxShadow: '0 1px 3px rgba(0, 0, 0, 0.12)',
                            cursor: 'pointer',
                            touchAction: 'manipulation',
                          }}
                        >
                          <div className="cf-event-header" style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: '2px', fontSize: '0.60rem', fontWeight: 800, lineHeight: 1.1, flexShrink: 0 }}>
                            <span className="cf-event-time" style={{ fontFamily: 'var(--mm-font-mono, monospace)' }}>{ev.start}</span>
                            {ev.initials && ev.initials.length > 0 && (
                              <span className="cf-event-initials" style={{ opacity: 0.85 }}>{ev.initials.join('')}</span>
                            )}
                          </div>
                          <div className="cf-event-title" style={{ fontSize: '0.65rem', fontWeight: 700, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', lineHeight: 1.2, flexShrink: 0 }}>
                            {ev.title}
                          </div>
                          {ev.location && (
                            <div className="cf-event-location" style={{ fontSize: '0.55rem', opacity: 0.85, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                              {ev.location}
                            </div>
                          )}
                        </div>
                      ))}
                    </div>
                  </div>
                ))}
              </div>
            </div>

            {/* Late Events Grid */}
            {daysToRender.some((d) => d.late_events && d.late_events.length > 0) && (
              <div
                className="cf-edge-band cf-week-late-grid"
                style={{
                  marginLeft: (weekVM?.timeline_markers?.length || weekVM?.hour_markers?.length) ? '44px' : undefined,
                  display: 'grid',
                  gridTemplateColumns: `repeat(${daysToRender.length}, 1fr)`,
                  gap: '6px',
                  paddingTop: '4px',
                  marginTop: '4px',
                  borderTop: '1px dashed var(--mm-divider, rgba(90, 60, 30, 0.1))',
                }}
              >
                {daysToRender.map((day) => (
                  <div key={day.date} className="cf-day-late-col" style={{ display: 'flex', flexDirection: 'column', gap: '2px' }}>
                    {day.late_events?.map((ev) => (
                      <div key={ev.id} className="cf-edge-band cf-late-band" style={{ display: 'flex', flexDirection: 'column', gap: '2px', padding: '2px 4px', background: 'rgba(90, 60, 30, 0.05)', borderRadius: '6px', border: '1px dashed var(--mm-divider, rgba(90, 60, 30, 0.12))', flexShrink: 0 }}>
                        <div
                          className={`cf-edge-event cf-event-clickable${ev.pattern ? ` cf-pattern-${ev.pattern}` : ''}`}
                          role="button"
                          tabIndex={0}
                          title={`Late event: ${ev.start} ${ev.title}${ev.location ? ` (${ev.location})` : ''}`}
                          aria-label={`Late event: ${ev.title} at ${ev.start}`}
                          data-event-id={ev.id}
                          data-event-title={ev.title}
                          data-event-start={ev.start}
                          data-event-end={ev.end}
                          data-event-location={ev.location}
                          data-event-description={ev.description}
                          onClick={() => openModal(ev)}
                          style={{
                            fontSize: '0.60rem',
                            fontWeight: 700,
                            padding: '2px 4px',
                            borderRadius: '4px',
                            backgroundColor: ev.color,
                            color: ev.text_color,
                            overflow: 'hidden',
                            textOverflow: 'ellipsis',
                            whiteSpace: 'nowrap',
                            cursor: 'pointer',
                            touchAction: 'manipulation',
                          }}
                        >
                          ▼ {ev.start} {ev.title}
                        </div>
                      </div>
                    ))}
                  </div>
                ))}
              </div>
            )}
          </>
        ) : (
          <div className="cf-empty" style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', flex: 1, color: 'var(--mm-text-dim)', fontSize: '0.9rem' }}>
            <span>No calendar data yet</span>
          </div>
        )}
      </div>

      {/* Month View Panel */}
      <div
        className="cf-view-panel cf-view-month"
        style={{
          display: activeView === 'month' ? 'flex' : 'none',
          flex: 1,
          minHeight: 0,
          flexDirection: 'column',
        }}
      >
        {monthVM?.days && members.length > 0 ? (
          <>
            <div className="cf-month-weekdays" style={{ display: 'grid', gridTemplateColumns: 'repeat(7, 1fr)', gap: '4px', paddingBottom: '4px', borderBottom: '1px solid var(--mm-divider, rgba(255, 255, 255, 0.08))', textAlign: 'center', fontSize: '0.70rem', fontWeight: 700, color: 'var(--mm-text-dim, #94a3b8)' }}>
              {monthVM.weekday_headers?.map((header) => (
                <span key={header} className="cf-weekday-header">
                  {header}
                </span>
              ))}
            </div>

            <div className="cf-month-grid" style={{ display: 'grid', gridTemplateColumns: 'repeat(7, 1fr)', gridTemplateRows: 'repeat(6, 1fr)', gap: '4px', flex: 1, minHeight: 0, marginTop: '4px' }}>
              {monthVM.days.map((day) => (
                <div
                  key={day.date}
                  className={`cf-month-day${!day.is_current_month ? ' cf-other-month' : ''}${day.is_today ? ' cf-today' : ''}${day.is_weekend ? ' cf-weekend' : ''}${day.events_count && day.events_count > 0 ? ' cf-has-events' : ''}`}
                  data-date={day.date}
                  data-event-id={day.events && day.events.length > 0 ? day.events[0].id : undefined}
                  onClick={() => {
                    if (day.events && day.events.length > 0) {
                      openModal(day.events[0]);
                    } else if (day.event_title) {
                      openModal({ id: day.date, title: day.event_title });
                    }
                  }}
                  style={{
                    display: 'flex',
                    flexDirection: 'column',
                    minWidth: 0,
                    minHeight: 0,
                    padding: '3px 4px',
                    borderRadius: '6px',
                    background: day.is_weekend ? 'rgba(255, 255, 255, 0.02)' : 'rgba(255, 255, 255, 0.04)',
                    border: '1px solid rgba(255, 255, 255, 0.05)',
                    overflow: 'hidden',
                    position: 'relative',
                    opacity: !day.is_current_month ? 0.38 : undefined,
                    outline: day.is_today ? '1px solid var(--mm-cal-today-border, var(--mm-lavender, #8b60ed))' : undefined,
                    zIndex: day.is_today ? 2 : undefined,
                    cursor: (day.events && day.events.length > 0) || day.event_title ? 'pointer' : undefined,
                  }}
                >
                  <div className="cf-month-day-header" style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', fontSize: '0.68rem', fontWeight: 700, lineHeight: 1.2 }}>
                    <span
                      className={`cf-day-num${day.is_today ? ' cf-today-num' : ''}`}
                      style={
                        day.is_today
                          ? {
                              background: 'var(--mm-cal-today-badge, var(--mm-lavender, #8b60ed))',
                              color: 'var(--mm-cal-today-badge-text, #ffffff)',
                              borderRadius: '50%',
                              width: '16px',
                              height: '16px',
                              display: 'inline-flex',
                              alignItems: 'center',
                              justifyContent: 'center',
                              fontSize: '0.60rem',
                              fontWeight: 800,
                            }
                          : { color: 'var(--mm-text-title, #ffffff)' }
                      }
                    >
                      {day.day_of_month}
                    </span>
                  </div>

                  {day.dots && day.dots.length > 0 && (
                    <div className="cf-month-dots" style={{ display: 'flex', flexWrap: 'wrap', gap: '3px', alignItems: 'center', marginTop: '2px' }}>
                      {day.dots.map((dot, dIdx) => (
                        <span
                          key={dIdx}
                          className={`cf-month-dot cf-pattern-${dot.pattern || 'solid'}${dot.is_shared ? ' cf-dot-shared' : ` cf-member-${dot.member_index}`}`}
                          title={dot.name}
                          style={{
                            display: 'inline-block',
                            width: '6px',
                            height: '6px',
                            borderRadius: '50%',
                            background: dot.is_shared ? 'var(--mm-cf-shared, #3D405B)' : `var(--mm-member-${dot.member_index}, ${dot.color})`,
                            flexShrink: 0,
                          }}
                        />
                      ))}
                    </div>
                  )}

                  {day.event_title ? (
                    day.events && day.events.length > 0 ? (
                      <>
                        {day.events.slice(0, 2).map((ev) => (
                          <div
                            key={ev.id}
                            className="cf-month-event-title cf-event-clickable"
                            data-event-id={ev.id}
                            title={ev.title}
                            onClick={(e) => {
                              e.stopPropagation();
                              openModal(ev);
                            }}
                            style={{
                              fontSize: '0.60rem',
                              color: 'var(--mm-text-dim, #cbd5e1)',
                              overflow: 'hidden',
                              textOverflow: 'ellipsis',
                              whiteSpace: 'nowrap',
                              marginTop: '2px',
                              lineHeight: 1.2,
                              cursor: 'pointer',
                              touchAction: 'manipulation',
                            }}
                          >
                            {ev.title}
                          </div>
                        ))}
                        {day.events.length > 2 && (
                          <div className="cf-month-more" style={{ fontSize: '0.55rem', color: 'var(--mm-text-dim, #94a3b8)', lineHeight: 1.1, marginTop: '1px' }}>
                            +{day.events.length - 2} more
                          </div>
                        )}
                      </>
                    ) : (
                      <div
                        className="cf-month-event-title cf-event-clickable"
                        title={day.event_title}
                        onClick={(e) => {
                          e.stopPropagation();
                          openModal({ id: day.date, title: day.event_title! });
                        }}
                        style={{
                          fontSize: '0.60rem',
                          color: 'var(--mm-text-dim, #cbd5e1)',
                          overflow: 'hidden',
                          textOverflow: 'ellipsis',
                          whiteSpace: 'nowrap',
                          marginTop: '2px',
                          lineHeight: 1.2,
                          cursor: 'pointer',
                          touchAction: 'manipulation',
                        }}
                      >
                        {day.event_title}
                      </div>
                    )
                  ) : null}
                </div>
              ))}
            </div>
          </>
        ) : (
          <div className="cf-empty" style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', flex: 1, color: 'var(--mm-text-dim)', fontSize: '0.9rem' }}>
            <span>No calendar data yet</span>
          </div>
        )}
      </div>

      {/* Event Details Modal */}
      {activeModalEvent && (
        <div
          className="cf-modal-overlay"
          role="dialog"
          aria-modal="true"
          aria-label={activeModalEvent.title || 'Event Details'}
          onClick={(e) => {
            if (e.target === e.currentTarget) {
              closeModal();
            }
          }}
        >
          <div className="cf-modal-card" onClick={(e) => e.stopPropagation()}>
            <div className="cf-modal-header">
              <h3 className="cf-modal-title">{activeModalEvent.title || 'Untitled Event'}</h3>
              <button
                type="button"
                className="cf-modal-close"
                aria-label="Close"
                onClick={closeModal}
              >
                &times;
              </button>
            </div>

            {/* Time */}
            {activeModalEvent.all_day ? (
              <div className="cf-modal-time">🕒 All Day</div>
            ) : activeModalEvent.start && activeModalEvent.end ? (
              <div className="cf-modal-time">🕒 {activeModalEvent.start} – {activeModalEvent.end}</div>
            ) : activeModalEvent.start ? (
              <div className="cf-modal-time">🕒 {activeModalEvent.start}</div>
            ) : null}

            {/* Location or Mealie Hero Image */}
            {activeModalEvent.location && activeModalEvent.location.trim() ? (
              activeModalEvent.location.startsWith('/hearth-api/mealie/image/') ? (
                <div style={{ width: '100%', height: '190px', borderRadius: '12px', overflow: 'hidden', margin: '8px 0 10px', background: 'rgba(15,23,42,0.4)' }}>
                  <img
                    src={activeModalEvent.location}
                    alt={activeModalEvent.title || 'Meal Photo'}
                    style={{ width: '100%', height: '100%', objectFit: 'cover', display: 'block' }}
                  />
                </div>
              ) : (
                <div className="cf-modal-location">
                  📍 {activeModalEvent.location}
                </div>
              )
            ) : null}

            {/* Owners Chips */}
            {((activeModalEvent.owners && activeModalEvent.owners.length > 0) || (activeModalEvent.initials && activeModalEvent.initials.length > 0)) && (
              <div className="cf-modal-owners">
                {Array.from({ length: Math.max(activeModalEvent.owners?.length || 0, activeModalEvent.initials?.length || 0) }).map((_, i) => {
                  const ownerName = activeModalEvent.owners?.[i] || '';
                  const initial = activeModalEvent.initials?.[i] || (ownerName ? ownerName.charAt(0).toUpperCase() : '');
                  const color = activeModalEvent.colors?.[i] || activeModalEvent.color || 'var(--mm-accent-purple)';
                  const pattern = activeModalEvent.patterns?.[i] || activeModalEvent.pattern || 'solid';
                  return (
                    <span key={i} className={`cf-modal-owner-chip cf-pattern-${pattern}`}>
                      <span className="cf-modal-owner-badge" style={{ backgroundColor: color }}>
                        {initial}
                      </span>
                      {ownerName && <span className="cf-modal-owner-name">{ownerName}</span>}
                    </span>
                  );
                })}
              </div>
            )}

            {/* Description */}
            {activeModalEvent.description && activeModalEvent.description.trim() ? (() => {
              let desc = activeModalEvent.description.trim().replace(/\\n/g, '\n').replace(/\\,/g, ',').replace(/\\;/g, ';');
              const isMealie =
                activeModalEvent.location?.startsWith('/hearth-api/mealie/image/') ||
                activeModalEvent.title?.includes('🍽️');
              if (!isMealie && desc.length > 280) {
                desc = desc.slice(0, 277) + '...';
              }
              return (
                <div
                  className="cf-modal-description"
                  style={isMealie ? { whiteSpace: 'pre-wrap', maxHeight: '320px', overflowY: 'auto', fontSize: '0.86rem', lineHeight: 1.45, paddingRight: '6px' } : undefined}
                >
                  {desc}
                </div>
              );
            })() : null}
          </div>
        </div>
      )}
    </div>
  );
});

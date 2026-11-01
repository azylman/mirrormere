import { memo, useCallback, useMemo, useRef, useState, type CSSProperties } from 'react';
import { useWidget } from '../../store/hooks';
import type { WidgetComponentProps } from '../registry';
import { CalendarEventModal } from './CalendarEventModal';
import { CalendarHeader } from './CalendarHeader';
import { CalendarMonthView } from './CalendarMonthView';
import { CalendarWeekView } from './CalendarWeekView';
import {
  addDays,
  computeDayLayout,
  formatDayLabel,
  formatModalDateRange,
  formatMonthLabel,
  formatWeekLabel,
  parseISODate,
  sliceWeekWindow,
  toISODate,
} from './dateUtils';
import type {
  FamilyDay,
  FamilyEvent,
  FamilyMonthViewModel,
  FamilyViewModel,
  ViewMode,
} from './types';
import { useCalendarRangeData } from './useCalendarRangeData';
import { useInactivityReset } from './useInactivityReset';

export * from './types';
export * from './dateUtils';
export { batchFitDayColumnEvents } from './CalendarWeekView';

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

  const rawDefault = (
    typeof config?.default_view === 'string'
      ? config.default_view
      : typeof entry?.viewModel?.active_view === 'string'
      ? entry.viewModel.active_view
      : 'week'
  ).toLowerCase();
  const defaultView: ViewMode =
    rawDefault === 'month' ? 'month' : rawDefault === 'day' ? 'day' : 'week';

  const [activeView, setActiveView] = useState<ViewMode>(defaultView);
  const [activeModalEvent, setActiveModalEvent] = useState<FamilyEvent | null>(null);
  const [activeModalDateLabel, setActiveModalDateLabel] = useState<string>('');

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
    return typeof vmWeek?.range_start === 'string' ? parseISODate(vmWeek.range_start) : baseDate;
  }, [entry?.viewModel, baseDate]);

  const weekStep = useMemo(() => {
    const baseDays = ((entry?.viewModel as Record<string, unknown> | undefined)?.week as FamilyViewModel | undefined)?.days;
    if (Array.isArray(baseDays) && baseDays.length > 0) return baseDays.length;
    const cfgDays = Number(config?.days);
    return Number.isFinite(cfgDays) && cfgDays > 0 ? Math.round(cfgDays) : 7;
  }, [entry?.viewModel, config?.days]);

  // Ref for user activity hook so range data callbacks can notify inactivity timer
  const activityCallbackRef = useRef<() => void>(() => {});

  const {
    currentViewModel,
    currentDate,
    currentWeekStart,
    pageOffset,
    syncMessage,
    pageNext,
    pagePrev,
    resetDate,
    handleViewSwitch,
    resetToBaseline,
  } = useCalendarRangeData({
    id,
    baseDate,
    baseWeekStart,
    weekStep,
    activeView,
    setActiveView,
    entryTimestamp: entry?.timestamp,
    entryViewModel: entry?.viewModel,
    cachedRangeStartConfig: config?.cached_range_start as string | undefined,
    cachedRangeEndConfig: config?.cached_range_end as string | undefined,
    dataCachedRangeStart: data?.cached_range_start as string | undefined,
    dataCachedRangeEnd: data?.cached_range_end as string | undefined,
    onActivity: () => activityCallbackRef.current(),
  });

  const handleInactivityReset = useCallback(() => {
    setActiveModalEvent(null);
    setActiveModalDateLabel('');
    resetToBaseline(defaultView);
  }, [defaultView, resetToBaseline]);

  const isInteractive = Boolean(activeModalEvent) || activeView !== defaultView || pageOffset !== 0;

  const { handleUserActivity } = useInactivityReset({
    isInteractive,
    onReset: handleInactivityReset,
  });
  activityCallbackRef.current = handleUserActivity;

  const openModal = useCallback(
    (ev: FamilyEvent, dateStr?: string, endDateStr?: string) => {
      const resolvedStart =
        dateStr || (ev.start && /^\d{4}-\d{2}-\d{2}/.test(ev.start) ? ev.start.slice(0, 10) : undefined);
      const resolvedEnd =
        endDateStr || (ev.end && /^\d{4}-\d{2}-\d{2}/.test(ev.end) ? ev.end.slice(0, 10) : undefined);
      setActiveModalEvent(ev);
      setActiveModalDateLabel(formatModalDateRange(resolvedStart, resolvedEnd));
      handleUserActivity();
    },
    [handleUserActivity]
  );

  const closeModal = useCallback(() => {
    setActiveModalEvent(null);
    setActiveModalDateLabel('');
    handleUserActivity();
  }, [handleUserActivity]);

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
      if (deltaX < 0) pageNext();
      else pagePrev();
    }
  };

  const weekVM = currentViewModel?.week as FamilyViewModel | undefined;
  const monthVM = currentViewModel?.month as FamilyMonthViewModel | undefined;

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

  let rangeLabelWeek = weekVM?.range_label || '';
  if (!rangeLabelWeek && currentWeekStart) {
    const daysLen = weekVM?.days?.length || 7;
    rangeLabelWeek = formatWeekLabel(currentWeekStart, addDays(currentWeekStart, daysLen - 1));
  }

  let rangeLabelMonth = monthVM?.month_label || '';
  if (!rangeLabelMonth && currentDate) {
    rangeLabelMonth = formatMonthLabel(currentDate);
  }

  const rangeLabelDay = formatDayLabel(currentDate);
  const currentDateISO = toISODate(currentDate);

  const daysToRender: FamilyDay[] = useMemo(() => {
    return sliceWeekWindow(weekVM?.days, activeView, currentDateISO);
  }, [weekVM?.days, activeView, currentDateISO]);

  const visibleAllDayEvents = useMemo(() => {
    return computeDayLayout(weekVM?.all_day_events, weekVM?.days, activeView, currentDateISO);
  }, [weekVM?.all_day_events, weekVM?.days, activeView, currentDateISO]);

  const customTitle =
    typeof config?.title === 'string' && config.title.trim() !== '' ? config.title.trim() : 'Calendar';
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

      <CalendarHeader
        customTitle={customTitle}
        activeView={activeView}
        pageOffset={pageOffset}
        rangeLabelWeek={rangeLabelWeek}
        rangeLabelMonth={rangeLabelMonth}
        rangeLabelDay={rangeLabelDay}
        syncMessage={syncMessage}
        members={members}
        onPrev={pagePrev}
        onNext={pageNext}
        onResetDate={resetDate}
        onViewSwitch={handleViewSwitch}
      />

      <CalendarWeekView
        activeView={activeView}
        weekVM={weekVM}
        daysToRender={daysToRender}
        visibleAllDayEvents={visibleAllDayEvents}
        members={members}
        currentDateISO={currentDateISO}
        onOpenModal={openModal}
      />

      <CalendarMonthView
        activeView={activeView}
        monthVM={monthVM}
        members={members}
        onOpenModal={(ev, date) => openModal(ev, date)}
      />

      <CalendarEventModal
        event={activeModalEvent}
        dateLabel={activeModalDateLabel}
        onClose={closeModal}
      />
    </div>
  );
});

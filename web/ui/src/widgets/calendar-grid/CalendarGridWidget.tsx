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
  matchesMemberFilter,
  parseISODate,
  sliceWeekWindow,
  toISODate,
} from './dateUtils';
import type {
  FamilyDay,
  FamilyEvent,
  FamilyMonthDay,
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
  const [selectedMember, setSelectedMember] = useState<string | null>(null);
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
    selectDayView,
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
    setSelectedMember(null);
    resetToBaseline(defaultView);
  }, [defaultView, resetToBaseline]);

  const isInteractive =
    Boolean(activeModalEvent) ||
    activeView !== defaultView ||
    pageOffset !== 0 ||
    Boolean(selectedMember);

  const { handleUserActivity } = useInactivityReset({
    isInteractive,
    onReset: handleInactivityReset,
  });
  activityCallbackRef.current = handleUserActivity;

  const handleSelectMember = useCallback(
    (name: string) => {
      setSelectedMember((prev) => (prev === name ? null : name));
      handleUserActivity();
    },
    [handleUserActivity]
  );

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

  const rawDaysToRender: FamilyDay[] = useMemo(() => {
    return sliceWeekWindow(weekVM?.days, activeView, currentDateISO);
  }, [weekVM?.days, activeView, currentDateISO]);

  const daysToRender: FamilyDay[] = useMemo(() => {
    if (!selectedMember) return rawDaysToRender;
    const membersList = weekVM?.members || monthVM?.members || [];
    return rawDaysToRender.map((day) => ({
      ...day,
      events: day.events?.filter((ev) => matchesMemberFilter(ev, selectedMember, membersList)),
      early_events: day.early_events?.filter((ev) => matchesMemberFilter(ev, selectedMember, membersList)),
      late_events: day.late_events?.filter((ev) => matchesMemberFilter(ev, selectedMember, membersList)),
      all_day: day.all_day?.filter((ev) => matchesMemberFilter(ev, selectedMember, membersList)),
      timed: day.timed?.filter((ev) => matchesMemberFilter(ev, selectedMember, membersList)),
    }));
  }, [rawDaysToRender, selectedMember, weekVM?.members, monthVM?.members]);

  const rawVisibleAllDayEvents = useMemo(() => {
    return computeDayLayout(weekVM?.all_day_events, weekVM?.days, activeView, currentDateISO);
  }, [weekVM?.all_day_events, weekVM?.days, activeView, currentDateISO]);

  const visibleAllDayEvents = useMemo(() => {
    if (!selectedMember) return rawVisibleAllDayEvents;
    const membersList = weekVM?.members || monthVM?.members || [];
    return rawVisibleAllDayEvents.filter((ev) => matchesMemberFilter(ev, selectedMember, membersList));
  }, [rawVisibleAllDayEvents, selectedMember, weekVM?.members, monthVM?.members]);

  const filteredMonthVM = useMemo(() => {
    if (!monthVM) return undefined;
    if (!selectedMember) return monthVM;
    const membersList = weekVM?.members || monthVM?.members || [];
    const filterDay = (day: FamilyMonthDay): FamilyMonthDay => {
      const filteredEvents = day.events
        ? day.events.filter((ev) => matchesMemberFilter(ev, selectedMember, membersList))
        : [];
      const filteredDots = day.dots
        ? day.dots.filter((dot) => dot.name === selectedMember)
        : undefined;
      return {
        ...day,
        dots: filteredDots,
        events: filteredEvents,
        events_count: filteredEvents.length,
        event_title: filteredEvents[0]?.title,
      };
    };

    return {
      ...monthVM,
      days: monthVM.days ? monthVM.days.map(filterDay) : undefined,
      weeks: monthVM.weeks
        ? monthVM.weeks.map((w) => ({
            ...w,
            days: w.days.map(filterDay),
          }))
        : undefined,
    };
  }, [monthVM, selectedMember, weekVM?.members]);

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
      style={memberCssVars}
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
        selectedMember={selectedMember}
        onSelectMember={handleSelectMember}
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
        onSelectDay={selectDayView}
      />

      <CalendarMonthView
        activeView={activeView}
        monthVM={filteredMonthVM}
        members={members}
        onOpenModal={(ev, date) => openModal(ev, date)}
        onSelectDay={selectDayView}
      />

      <CalendarEventModal
        event={activeModalEvent}
        dateLabel={activeModalDateLabel}
        onClose={closeModal}
      />
    </div>
  );
});

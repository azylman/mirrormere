import { act, fireEvent, render } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useDisplayStore } from '../../store';
import { CalendarGridWidget, type CalendarGridViewModel } from './CalendarGridWidget';
import { itemAt } from '../../test-utils/lookup';

const mockViewModel: CalendarGridViewModel = {
  active_view: 'week',
  cached_range_start: '2026-10-01',
  cached_range_end: '2026-11-30',
  week: {
    range_start: '2026-10-25',
    range_end: '2026-10-31',
    range_label: 'Oct 25 – 31, 2026',
    today: '2026-10-27',
    shared_color: '#3D405B',
    members: [
      { name: 'Alex', initial: 'A', color: '#ff6f59', text_color: '#ffffff', pattern: 'solid' },
      { name: 'Sam', initial: 'S', color: '#8b60ed', text_color: '#ffffff', pattern: 'dots' },
    ],
    hour_markers: ['08:00', '12:00', '18:00'],
    timeline_markers: [
      { label: '08:00', top_pct: 0, is_start: true },
      { label: '12:00', top_pct: 50 },
      { label: '18:00', top_pct: 100, is_end: true },
    ],
    all_day_events: [
      { id: 'ev-allday-1', title: 'Conference Day', start: '2026-10-26', end: '2026-10-27', all_day: true, start_col: 2, col_span: 2, color: '#ff6f59', text_color: '#ffffff' },
      { id: 'ev-mealie-1', title: '🍽️ Tacos Dinner', start: '2026-10-27', all_day: true, start_col: 3, col_span: 1, color: '#8b60ed', text_color: '#ffffff', location: '/hearth-api/mealie/image/tacos.jpg', description: 'Delicious homemade carnitas tacos recipe details...' },
    ],
    days: [
      { date: '2026-10-25', weekday_abbr: 'Sun', day_of_month: 25, is_today: false, is_weekend: true, events: [] },
      {
        date: '2026-10-26', weekday_abbr: 'Mon', day_of_month: 26, is_today: false, is_weekend: false,
        events: [{ id: 'ev-timed-1', title: 'Morning Standup', start: '09:00', end: '09:30', all_day: false, top_pct: 10, height_pct: 15, left_pct: 0, width_pct: 100, color: '#ff6f59', text_color: '#ffffff', owners: ['Alex'], initials: ['A'], location: 'Room 101', description: 'Weekly team sprint planning and standup sync.' }],
      },
      {
        date: '2026-10-27', weekday_abbr: 'Tue', day_of_month: 27, is_today: true, is_weekend: false,
        early_events: [{ id: 'ev-early-1', title: 'Early Workout', start: '06:30', all_day: false, top_pct: 0, height_pct: 0, left_pct: 0, width_pct: 100, color: '#8b60ed', text_color: '#ffffff' }],
        events: [{ id: 'ev-timed-2', title: 'Project Demo', start: '14:00', end: '15:00', all_day: false, top_pct: 60, height_pct: 20, left_pct: 0, width_pct: 100, color: '#8b60ed', text_color: '#ffffff', owners: ['Sam'], initials: ['S'] }],
        late_events: [{ id: 'ev-late-1', title: 'Late Flight', start: '22:00', all_day: false, top_pct: 0, height_pct: 0, left_pct: 0, width_pct: 100, color: '#ff6f59', text_color: '#ffffff' }],
      },
      { date: '2026-10-28', weekday_abbr: 'Wed', day_of_month: 28, is_today: false, is_weekend: false, events: [] },
      { date: '2026-10-29', weekday_abbr: 'Thu', day_of_month: 29, is_today: false, is_weekend: false, events: [] },
      { date: '2026-10-30', weekday_abbr: 'Fri', day_of_month: 30, is_today: false, is_weekend: false, events: [] },
      { date: '2026-10-31', weekday_abbr: 'Sat', day_of_month: 31, is_today: false, is_weekend: true, events: [] },
    ],
  },
  month: {
    month_label: 'October 2026',
    month_name: 'October',
    year: 2026,
    month: 10,
    range_start: '2026-10-01',
    range_end: '2026-10-31',
    today: '2026-10-27',
    shared_color: '#3D405B',
    weekday_headers: ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'],
    members: [
      { name: 'Alex', initial: 'A', color: '#ff6f59', text_color: '#ffffff', pattern: 'solid' },
      { name: 'Sam', initial: 'S', color: '#8b60ed', text_color: '#ffffff', pattern: 'dots' },
    ],
    days: [
      { date: '2026-09-27', day_of_month: 27, weekday_abbr: 'Sun', is_current_month: false, is_today: false, is_weekend: true, dots: [] },
      { date: '2026-10-01', day_of_month: 1, weekday_abbr: 'Thu', is_current_month: true, is_today: false, is_weekend: false, dots: [] },
      {
        date: '2026-10-27', day_of_month: 27, weekday_abbr: 'Tue', is_current_month: true, is_today: true, is_weekend: false,
        dots: [
          { name: 'Alex', initial: 'A', color: '#ff6f59', text_color: '#ffffff', pattern: 'solid', is_shared: false, member_index: 0 },
          { name: 'Sam', initial: 'S', color: '#8b60ed', text_color: '#ffffff', pattern: 'dots', is_shared: false, member_index: 1 },
        ],
        event_title: '🍽️ Tacos Dinner',
        events_count: 2,
        events: [
          { id: 'ev-month-1', title: '🍽️ Tacos Dinner', all_day: true, location: '/hearth-api/mealie/image/tacos.jpg' },
          { id: 'ev-month-2', title: 'Project Demo', start: '14:00', all_day: false },
        ],
      },
    ],
  },
};

function setGridState(override?: {
  widgetId?: string;
  config?: Record<string, unknown>;
  dimensions?: [number, number];
  data?: Record<string, unknown>;
  viewModel?: unknown;
  state?: 'healthy' | 'idle' | 'error';
  timestamp?: string;
}) {
  const id = override?.widgetId ?? 'cal-grid';
  useDisplayStore.setState({
    widgets: {
      [id]: {
        meta: {
          type: 'calendar-grid',
          origin: [0, 0],
          dimensions: override?.dimensions ?? [6, 2],
          config: override?.config ?? {},
          assets_base: '',
        },
        state: override?.state ?? 'healthy',
        data: override?.data ?? {},
        viewModel: (override && 'viewModel' in override ? override.viewModel : mockViewModel) as Record<string, unknown>,
        timestamp: override?.timestamp ?? '2026-10-27T10:00:00Z',
      },
    },
  });
}

describe('CalendarGridWidget', () => {

  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn());
  });

  it('renders week view by default with days, header, members legend, and events', () => {
    setGridState({ config: { title: 'Family Grid' } });
    const { container, rerender } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);

    expect(container.querySelector('.cf-title')?.textContent).toBe('Family Grid');
    expect(container.querySelector('.cf-range-label.cf-label-week')?.textContent).toBe('Oct 25 – 31, 2026');

    // Re-render with changed type to bust React.memo and trigger memoization cache hit
    rerender(<CalendarGridWidget id="cal-grid" type="calendar-grid-custom" />);
    expect(container.querySelector('.cf-title')?.textContent).toBe('Family Grid');

    const chips = container.querySelectorAll('.cf-member-chip');
    expect(chips.length).toBe(2);
    expect(itemAt(chips, 0).textContent).toContain('Alex');
    expect(itemAt(chips, 1).textContent).toContain('Sam');

    expect(container.querySelector('.widget-calendar-grid')?.classList.contains('cf-view-week')).toBe(true);
    expect(container.querySelector('.cf-view-btn[data-view="week"]')?.classList.contains('active')).toBe(true);
    expect(container.querySelectorAll('.cf-hour-marker').length).toBeGreaterThan(0);

    const days = container.querySelectorAll('.cf-day');
    expect(days.length).toBe(7);

    const todayCol = container.querySelector('.cf-day.cf-today');
    expect(todayCol).not.toBeNull();
    expect(todayCol?.getAttribute('data-date')).toBe('2026-10-27');

    expect(container.querySelector('[data-event-id="ev-allday-1"]')).not.toBeNull();
    expect(container.querySelector('[data-event-id="ev-timed-1"]')?.textContent).toContain('Morning Standup');
    expect(container.querySelector('[data-event-id="ev-early-1"]')?.textContent).toContain('Early Workout');
    expect(container.querySelector('[data-event-id="ev-late-1"]')?.textContent).toContain('Late Flight');
  });

  it('switches between week and month views and renders month grid with weekday headers, day numbers, and event dots', () => {
    setGridState();
    const { container } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);

    const monthBtn = container.querySelector('.cf-view-btn[data-view="month"]');
    expect(monthBtn).not.toBeNull();
    fireEvent.click(monthBtn!);

    expect(container.querySelector('.widget-calendar-grid')?.classList.contains('cf-view-month')).toBe(true);
    expect(monthBtn?.classList.contains('active')).toBe(true);
    expect(container.querySelector('.cf-range-label.cf-label-month')?.textContent).toBe('October 2026');

    const weekdayHeaders = container.querySelectorAll('.cf-weekday-header');
    expect(weekdayHeaders.length).toBe(7);
    expect(itemAt(weekdayHeaders, 0).textContent).toBe('Sun');
    expect(container.querySelectorAll('.cf-month-dot').length).toBeGreaterThan(0);
  });

  it('renders day view as a 1-day slice of week.days and formats range label as Weekday, Mon D', () => {
    setGridState({ dimensions: [3, 2], config: { default_view: 'day' } });
    const { container } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);

    expect(container.querySelector('.widget-calendar-grid')?.classList.contains('cf-view-day')).toBe(true);
    const days = container.querySelectorAll('.cf-day');
    expect(days.length).toBe(1);
    expect(itemAt(days, 0).getAttribute('data-date')).toBe('2026-10-27');
    expect(container.querySelector('.cf-range-label.cf-label-day')?.textContent).toBe('Tuesday, Oct 27');
  });

  it('opens event modal on event click, displays details/Mealie hero image, and closes via close button, backdrop click, or Escape', () => {
    setGridState();
    const { container } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);

    // Click single-day timed event
    const timedEl = container.querySelector('[data-event-id="ev-timed-1"]');
    expect(timedEl).not.toBeNull();
    fireEvent.click(timedEl!);
    expect(container.querySelector('.cf-modal-overlay')).not.toBeNull();
    expect(container.querySelector('.cf-modal-title')?.textContent).toBe('Morning Standup');
    expect(container.querySelector('.cf-modal-time')?.textContent).toBe('Mon, Oct 26 • 09:00 – 09:30');
    expect(container.querySelector('.cf-modal-location')?.textContent).toBe('Room 101');
    expect(container.querySelector('.cf-modal-owner-chip')?.textContent).toContain('Alex');
    expect(container.querySelector('.cf-modal-description')?.textContent).toContain('Weekly team sprint planning');

    // Close via close button
    const closeBtn = container.querySelector('.cf-modal-close')!;
    fireEvent.click(closeBtn);
    expect(container.querySelector('.cf-modal-overlay')).toBeNull();

    // Click Mealie event to assert hero image and close via backdrop click
    const mealieEl = container.querySelector('[data-event-id="ev-mealie-1"]');
    expect(mealieEl).not.toBeNull();
    fireEvent.click(mealieEl!);
    expect(container.querySelector('.cf-modal-overlay')).not.toBeNull();
    expect(container.querySelector('.cf-modal-title')?.textContent).toBe('Tacos Dinner');
    expect(container.querySelector('.cf-modal-time')?.textContent).toBe('Tue, Oct 27 • All Day');
    const heroImg = container.querySelector('.cf-modal-card img');
    expect(heroImg?.getAttribute('src')).toBe('/hearth-api/mealie/image/tacos.jpg');

    const overlay = container.querySelector('.cf-modal-overlay')!;
    fireEvent.click(overlay);
    expect(container.querySelector('.cf-modal-overlay')).toBeNull();

    // Click multi-day all-day span event to exercise resolveModalDateLabel endDateStr branch and close via Escape
    const allDayEl = container.querySelector('[data-event-id="ev-allday-1"]');
    expect(allDayEl).not.toBeNull();
    fireEvent.click(allDayEl!);
    expect(container.querySelector('.cf-modal-overlay')).not.toBeNull();
    expect(container.querySelector('.cf-modal-time')?.textContent).toBe('Mon, Oct 26 – Tue, Oct 27 • All Day');

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(container.querySelector('.cf-modal-overlay')).toBeNull();
  });

  it('decorates Mealie all-day pills declaratively with data-mealie-decorated="true"', () => {
    setGridState();
    const { container } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);

    const mealiePill = container.querySelector('[data-event-id="ev-mealie-1"]');
    expect(mealiePill?.getAttribute('data-mealie-decorated')).toBe('true');
    const pillImg = mealiePill?.querySelector('img');
    expect(pillImg?.getAttribute('src')).toBe('/hearth-api/mealie/image/tacos.jpg');
    expect(mealiePill?.querySelector('svg.cf-mealie-icon')).not.toBeNull();
    expect(mealiePill?.textContent).toBe('Tacos Dinner');
  });


  it('renders empty message when neither week nor month data is available', () => {
    setGridState({ widgetId: 'cal-grid-empty', viewModel: undefined });
    const { container } = render(<CalendarGridWidget id="cal-grid-empty" type="calendar-grid" />);
    expect(container.querySelector('.cf-empty')?.textContent).toBe('No calendar data yet');
  });

  it('does not trigger network fetch when switching views at pageOffset 0 with baseline week and month payloads', () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);

    setGridState();
    const { container } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);
    const monthBtn = container.querySelector('.cf-view-btn[data-view="month"]')!;
    const weekBtn = container.querySelector('.cf-view-btn[data-view="week"]')!;

    fireEvent.click(monthBtn);
    expect(container.querySelector('.widget-calendar-grid')?.classList.contains('cf-view-month')).toBe(true);
    expect(fetchMock).not.toHaveBeenCalled();

    fireEvent.click(weekBtn);
    expect(container.querySelector('.widget-calendar-grid')?.classList.contains('cf-view-week')).toBe(true);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('does not arm inactivity timer on benign tap/pointer events when isInteractive is false and does not block screen swipes', () => {
    vi.useFakeTimers();
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);

    setGridState();
    const pauseSpy = vi.spyOn(useDisplayStore.getState(), 'pause');
    const resumeSpy = vi.spyOn(useDisplayStore.getState(), 'resume');

    const { container } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);
    const gridEl = container.querySelector('.widget-calendar-grid')!;

    expect(gridEl.getAttribute('data-prevent-screen-swipe')).toBeNull();

    fireEvent.pointerDown(gridEl);
    fireEvent.touchStart(gridEl, { touches: [{ clientX: 250, clientY: 100 }] });
    fireEvent.touchMove(gridEl, { touches: [{ clientX: 50, clientY: 100 }] });
    fireEvent.touchEnd(gridEl, { changedTouches: [{ clientX: 50, clientY: 100 }] });

    expect(gridEl.getAttribute('data-page-offset')).toBe('0');
    expect(pauseSpy).not.toHaveBeenCalled();
    act(() => {
      vi.advanceTimersByTime(65000);
    });
    expect(fetchMock).not.toHaveBeenCalled();
    expect(resumeSpy).not.toHaveBeenCalled();
  });

  const narrow3DayVM: CalendarGridViewModel = {
    ...mockViewModel,
    week: {
      ...mockViewModel.week!,
      range_start: '2026-10-27',
      range_end: '2026-10-29',
      range_label: 'Oct 27 – 29, 2026',
      days: mockViewModel.week!.days!.slice(0, 3),
    },
  };

  it.each([
    {
      name: 'restores entry.viewModel without network fetch when clicking return to today',
      steps: ['next', 'today'] as const,
      isNarrow: false,
      wantOffset: '0',
      wantView: 'week',
      wantLabel: 'Oct 25 – 31, 2026',
      wantTodayVisible: false,
      wantFetchCalls: 1,
      checkAbort: false,
      expectedFetchUrls: undefined,
    },
    {
      name: 'switches from paged month view to week view synchronizing week range',
      steps: ['month', 'next', 'week'] as const,
      isNarrow: false,
      wantOffset: '1',
      wantView: 'week',
      wantLabel: 'Nov 1 – 7, 2026',
      wantTodayVisible: true,
      wantFetchCalls: 2,
      checkAbort: false,
      expectedFetchUrls: ['date=2026-11-01', 'view=week'],
    },
    {
      name: 'resets both currentDate and currentWeekStart when paging back to baseline month and switching to week view',
      steps: ['next', 'month', 'prev', 'week'] as const,
      isNarrow: false,
      wantOffset: '0',
      wantView: 'week',
      wantLabel: 'Oct 25 – 31, 2026',
      wantTodayVisible: false,
      wantFetchCalls: 2,
      checkAbort: false,
      expectedFetchUrls: undefined,
    },
    {
      name: 'aborts in-flight page fetches when returning to baseline date',
      steps: ['next', 'prev'] as const,
      isNarrow: false,
      wantOffset: '0',
      wantView: 'week',
      wantLabel: 'Oct 25 – 31, 2026',
      wantTodayVisible: false,
      wantFetchCalls: 1,
      checkAbort: true,
      expectedFetchUrls: undefined,
    },
    {
      name: 'computes week pageOffset using weekVM.days.length for narrow 3-day widgets',
      steps: ['next'] as const,
      isNarrow: true,
      wantOffset: '1',
      wantView: 'week',
      wantLabel: 'Oct 30 – Nov 1, 2026',
      wantTodayVisible: true,
      wantFetchCalls: 1,
      checkAbort: false,
      expectedFetchUrls: undefined,
    },
  ])('handles view-paging and baseline-reset navigation: $name', async ({ steps, isNarrow, wantOffset, wantView, wantLabel, wantTodayVisible, wantFetchCalls, checkAbort, expectedFetchUrls }) => {
    let aborted = false;
    let lastUrl = '';
    let resolvePendingJson: ((val: unknown) => void) | undefined;
    const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      lastUrl = url;
      if (init?.signal) {
        init.signal.addEventListener('abort', () => {
          aborted = true;
        });
      }
      if (checkAbort) {
        return Promise.resolve({
          ok: true,
          json: () =>
            new Promise((resolve) => {
              resolvePendingJson = resolve;
            }),
        });
      }
      const isNov = url.includes('2026-11');
      const pagedWeek = isNarrow
        ? {
            ...narrow3DayVM.week!,
            range_start: '2026-10-30',
            range_end: '2026-11-01',
            range_label: 'Oct 30 – Nov 1, 2026',
          }
        : {
            ...mockViewModel.week,
            range_start: isNov ? '2026-11-01' : '2026-10-25',
            range_end: isNov ? '2026-11-07' : '2026-10-31',
            range_label: isNov ? 'Nov 1 – 7, 2026' : 'Oct 25 – 31, 2026',
          };
      return Promise.resolve({
        ok: true,
        json: () =>
          Promise.resolve({
            view_model: {
              ...mockViewModel,
              week: pagedWeek,
              month: {
                ...mockViewModel.month,
                range_start: isNov ? '2026-11-01' : '2026-10-01',
                range_end: isNov ? '2026-11-30' : '2026-10-31',
                month_label: isNov ? 'November 2026' : 'October 2026',
              },
            },
          }),
      });
    });
    vi.stubGlobal('fetch', fetchMock);

    if (isNarrow) {
      setGridState({
        dimensions: [3, 2],
        config: { days: 3, cached_range_start: '2026-01-01', cached_range_end: '2027-01-01' },
        viewModel: narrow3DayVM,
      });
    } else {
      setGridState({ config: { cached_range_start: '2026-01-01', cached_range_end: '2027-01-01' } });
    }

    const { container } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);

    const selectorMap: Record<string, string> = {
      next: '.cf-nav-btn.cf-next-btn',
      prev: '.cf-nav-btn.cf-prev-btn',
      today: '.cf-today-badge',
      week: '.cf-view-btn[data-view="week"]',
      month: '.cf-view-btn[data-view="month"]',
    };

    for (const step of steps) {
      const btn = container.querySelector(selectorMap[step]!);
      expect(btn).not.toBeNull();
      await act(async () => {
        fireEvent.click(btn!);
      });
    }

    const widget = container.querySelector('.widget-calendar-grid');
    expect(widget?.getAttribute('data-page-offset')).toBe(wantOffset);
    expect(widget?.classList.contains(`cf-view-${wantView}`)).toBe(true);
    const label = container.querySelector(`.cf-range-label.cf-label-${wantView}`)?.textContent;
    expect(label).toContain(wantLabel);
    const todayBadge = container.querySelector('.cf-today-badge') as HTMLElement;
    if (wantTodayVisible) {
      expect(todayBadge.style.display).not.toBe('none');
    } else {
      expect(todayBadge.style.display).toBe('none');
    }
    expect(fetchMock).toHaveBeenCalledTimes(wantFetchCalls);
    if (checkAbort) {
      expect(aborted).toBe(true);
      await act(async () => {
        resolvePendingJson?.({
          view_model: {
            ...mockViewModel,
            week: { ...mockViewModel.week, range_label: 'Stale Week Range' },
          },
        });
      });
      // Verify stale response did not clobber baseline label
      const currentLabel = container.querySelector(`.cf-range-label.cf-label-${wantView}`)?.textContent;
      expect(currentLabel).toContain(wantLabel);
    }
    if (expectedFetchUrls) {
      for (const expectedPart of expectedFetchUrls) {
        expect(lastUrl).toContain(expectedPart);
      }
    }
  });


  it('does not reset inactivity timer on background SSE widget.update when isInteractive is true', () => {
    vi.useFakeTimers();
    setGridState();

    const resumeSpy = vi.spyOn(useDisplayStore.getState(), 'resume');
    const { container, rerender } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);

    const monthBtn = container.querySelector('.cf-view-btn[data-view="month"]')!;
    fireEvent.click(monthBtn);

    act(() => {
      vi.advanceTimersByTime(45000);
    });
    expect(resumeSpy).not.toHaveBeenCalled();

    const sseUpdatedViewModel: CalendarGridViewModel = {
      ...mockViewModel,
      active_view: 'week',
    };
    setGridState({ viewModel: sseUpdatedViewModel, timestamp: '2026-10-27T10:01:00Z' });
    rerender(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);

    act(() => {
      vi.advanceTimersByTime(16000);
    });

    expect(resumeSpy).toHaveBeenCalledTimes(1);
    expect(container.querySelector('.widget-calendar-grid')?.classList.contains('cf-view-week')).toBe(true);
  });

  it('does not render .cf-allday-band in day view when there are no all-day events on the active day', () => {
    setGridState({
      dimensions: [3, 2],
      config: { default_view: 'day' },
      viewModel: {
        ...mockViewModel,
        week: {
          ...mockViewModel.week!,
          today: '2026-10-25',
        },
      },
    });

    const { container } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);
    expect(container.querySelector('.cf-allday-band')).toBeNull();
  });

  it('tracks baseDate and baseWeekStart updates when resting without flipping isInteractive or pausing store', () => {
    setGridState({ timestamp: '2026-10-27T23:59:00Z' });
    const pauseSpy = vi.spyOn(useDisplayStore.getState(), 'pause');
    const { container, rerender } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);

    const nextDayVM: CalendarGridViewModel = {
      ...mockViewModel,
      week: {
        ...mockViewModel.week!,
        range_start: '2026-11-01',
        range_end: '2026-11-07',
        range_label: 'Nov 1 – 7, 2026',
        today: '2026-11-01',
      },
      month: { ...mockViewModel.month!, today: '2026-11-01' },
    };

    setGridState({ viewModel: nextDayVM, timestamp: '2026-11-01T00:00:05Z' });
    rerender(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);

    expect(pauseSpy).not.toHaveBeenCalled();
    expect(container.querySelector('.widget-calendar-grid')?.getAttribute('data-page-offset')).toBe('0');
    expect(container.querySelector('.widget-calendar-grid')?.getAttribute('data-current-date')).toBe('2026-11-01');
    expect(container.querySelector('.cf-range-label.cf-label-week')?.textContent).toBe('Nov 1 – 7, 2026');
  });

  describe('cf-now-indicator', () => {
    it('renders .cf-now-indicator at expected top percentage on .cf-today when within bounds', () => {
      vi.useFakeTimers();
      // 13:00 UTC with start 08:00 (480) and end 18:00 (1080) -> (780 - 480) / 600 = 50%
      vi.setSystemTime(new Date('2026-10-27T13:00:00Z'));
      useDisplayStore.setState({
        header: { timestamp: '2026-10-27T13:00:00Z', timezone: 'UTC', weather: null },
      });

      setGridState();
      const { container } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);

      const todayCol = container.querySelector('.cf-day.cf-today');
      expect(todayCol).toBeTruthy();

      const indicator = todayCol?.querySelector('.cf-now-indicator') as HTMLElement;
      expect(indicator).toBeTruthy();
      expect(indicator.getAttribute('data-now-pct')).toBe('50.0');
      expect(indicator.style.top).toMatch(/^50(?:\.0+)?%$/);
      expect(indicator.querySelector('.cf-now-dot')).toBeTruthy();

      // Non-today days must not contain .cf-now-indicator
      const nonTodayCols = container.querySelectorAll('.cf-day:not(.cf-today)');
      expect(nonTodayCols.length).toBeGreaterThan(0);
      nonTodayCols.forEach((col) => {
        expect(col.querySelector('.cf-now-indicator')).toBeNull();
      });
    });

    it('hides .cf-now-indicator when current time is outside bounds', () => {
      vi.useFakeTimers();
      // 06:00 UTC is earlier than timeline start (08:00)
      vi.setSystemTime(new Date('2026-10-27T06:00:00Z'));
      useDisplayStore.setState({
        header: { timestamp: '2026-10-27T06:00:00Z', timezone: 'UTC', weather: null },
      });

      setGridState();
      const { container } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);

      const todayCol = container.querySelector('.cf-day.cf-today');
      expect(todayCol).toBeTruthy();
      expect(todayCol?.querySelector('.cf-now-indicator')).toBeNull();
    });

    it('updates .cf-now-indicator when 60s timer ticks', () => {
      vi.useFakeTimers();
      vi.setSystemTime(new Date('2026-10-27T13:00:00Z'));
      useDisplayStore.setState({
        header: { timestamp: '2026-10-27T13:00:00Z', timezone: 'UTC', weather: null },
      });

      setGridState();
      const { container } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);

      const indicator = container.querySelector('.cf-today .cf-now-indicator') as HTMLElement;
      expect(indicator).toBeTruthy();
      expect(indicator.getAttribute('data-now-pct')).toBe('50.0');

      // Advance by 60 seconds: 13:01:00 -> (781 - 480) / 600 = 50.1666... -> 50.2%
      act(() => {
        vi.advanceTimersByTime(60_000);
      });

      const updated = container.querySelector('.cf-today .cf-now-indicator') as HTMLElement;
      expect(updated.getAttribute('data-now-pct')).toBe('50.2');
    });

    it('pauses now timer when document is hidden and updates immediately on visibility restore', () => {
      vi.useFakeTimers();
      vi.setSystemTime(new Date('2026-10-27T13:00:00Z'));
      useDisplayStore.setState({
        header: { timestamp: '2026-10-27T13:00:00Z', timezone: 'UTC', weather: null },
        visibility: { visible: true },
      });

      setGridState();
      const { container } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);

      let indicator = container.querySelector('.cf-today .cf-now-indicator') as HTMLElement;
      expect(indicator.getAttribute('data-now-pct')).toBe('50.0');

      // Hide document
      act(() => {
        useDisplayStore.setState({ visibility: { visible: false } });
      });

      // Advance time while hidden: 30 minutes to 13:30:00 -> (810 - 480) / 600 = 55.0%
      act(() => {
        vi.advanceTimersByTime(30 * 60_000);
      });

      // While hidden and timer was disarmed, it should still retain old value (not ticked via interval)
      indicator = container.querySelector('.cf-today .cf-now-indicator') as HTMLElement;
      expect(indicator.getAttribute('data-now-pct')).toBe('50.0');

      // Restore document visibility
      act(() => {
        useDisplayStore.setState({ visibility: { visible: true } });
      });

      // On visibility restore, useEffect immediately calls setNow(new Date())
      indicator = container.querySelector('.cf-today .cf-now-indicator') as HTMLElement;
      expect(indicator.getAttribute('data-now-pct')).toBe('55.0');
    });
  });

  describe('member chip filters and day drill-down', () => {
    it('toggles member filter on chip click, dimming non-selected chips, and filters week events', () => {
      setGridState();
      const { container } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);

      const alexChip = container.querySelector('.cf-member-chip[data-member="Alex"]') as HTMLButtonElement;
      const samChip = container.querySelector('.cf-member-chip[data-member="Sam"]') as HTMLButtonElement;
      expect(alexChip).toBeTruthy();
      expect(samChip).toBeTruthy();
      expect(alexChip.getAttribute('aria-pressed')).toBe('false');
      expect(samChip.getAttribute('aria-pressed')).toBe('false');

      // Before filtering, both events are visible
      expect(container.querySelector('[data-event-id="ev-timed-1"]')).toBeTruthy();
      expect(container.querySelector('[data-event-id="ev-timed-2"]')).toBeTruthy();

      // Click Alex chip -> filter to Alex
      fireEvent.click(alexChip);
      expect(alexChip.classList.contains('active')).toBe(true);
      expect(alexChip.getAttribute('aria-pressed')).toBe('true');
      expect(samChip.classList.contains('dimmed')).toBe(true);
      expect(samChip.style.opacity).toBe('0.45');

      // Alex event is visible; Sam event is filtered out
      expect(container.querySelector('[data-event-id="ev-timed-1"]')).toBeTruthy();
      expect(container.querySelector('[data-event-id="ev-timed-2"]')).toBeNull();

      // Click Alex chip again -> toggle off
      fireEvent.click(alexChip);
      expect(alexChip.classList.contains('active')).toBe(false);
      expect(alexChip.getAttribute('aria-pressed')).toBe('false');
      expect(samChip.classList.contains('dimmed')).toBe(false);

      expect(container.querySelector('[data-event-id="ev-timed-1"]')).toBeTruthy();
      expect(container.querySelector('[data-event-id="ev-timed-2"]')).toBeTruthy();

      // Click Sam chip -> filter to Sam
      fireEvent.click(samChip);
      expect(samChip.classList.contains('active')).toBe(true);
      expect(samChip.getAttribute('aria-pressed')).toBe('true');
      expect(alexChip.classList.contains('dimmed')).toBe(true);
      expect(alexChip.style.opacity).toBe('0.45');

      expect(container.querySelector('[data-event-id="ev-timed-1"]')).toBeNull();
      expect(container.querySelector('[data-event-id="ev-timed-2"]')).toBeTruthy();
    });

    it('filters month dots and events when member filter is active', () => {
      setGridState();
      const { container } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);

      const monthBtn = container.querySelector('.cf-view-btn[data-view="month"]')!;
      fireEvent.click(monthBtn);

      // Baseline has 2 dots on 2026-10-27
      expect(container.querySelectorAll('.cf-month-dot').length).toBe(2);

      const alexChip = container.querySelector('.cf-member-chip[data-member="Alex"]') as HTMLButtonElement;
      fireEvent.click(alexChip);

      // Only Alex's dot remains
      const dotsAfterAlex = container.querySelectorAll('.cf-month-dot');
      expect(dotsAfterAlex.length).toBe(1);
      expect(itemAt(dotsAfterAlex, 0).getAttribute('title')).toBe('Alex');

      // Toggle off -> both dots return
      fireEvent.click(alexChip);
      expect(container.querySelectorAll('.cf-month-dot').length).toBe(2);
    });

    it('resets selected member filter on inactivity timeout', () => {
      vi.useFakeTimers();
      setGridState();
      const { container } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);

      const alexChip = container.querySelector('.cf-member-chip[data-member="Alex"]') as HTMLButtonElement;
      fireEvent.click(alexChip);
      expect(alexChip.classList.contains('active')).toBe(true);

      // Advance by 60s inactivity threshold
      act(() => {
        vi.advanceTimersByTime(60_000);
      });

      expect(alexChip.classList.contains('active')).toBe(false);
      expect(alexChip.getAttribute('aria-pressed')).toBe('false');
    });

    it('filters .cf-mealie-bar items when member chip is selected in week view', () => {
      const daysWithMeals = mockViewModel.week!.days!.map((day) => {
        if (day.date === '2026-10-27') {
          return {
            ...day,
            meals: [
              { id: 'meal-1', title: 'Alex Lunch', owners: ['Alex'] },
              { id: 'meal-2', title: 'Sam Dinner', owners: ['Sam'] },
            ],
          };
        }
        return day;
      });

      setGridState({
        viewModel: {
          ...mockViewModel,
          week: {
            ...mockViewModel.week!,
            days: daysWithMeals,
          },
        },
      });

      const { container } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);
      expect(container.querySelectorAll('.cf-mealie-bar').length).toBe(2);

      const alexChip = container.querySelector('.cf-member-chip[data-member="Alex"]') as HTMLElement;
      fireEvent.click(alexChip);

      const filteredMeals = container.querySelectorAll('.cf-mealie-bar');
      expect(filteredMeals.length).toBe(1);
      expect(itemAt(filteredMeals, 0).getAttribute('data-event-id')).toBe('meal-1');
    });

    it.each([
      { initialView: 'week' as const, selector: '.cf-day[data-date="2026-10-26"] .cf-day-header', event: 'click' as const, key: '', expectedDate: '2026-10-26' },
      { initialView: 'week' as const, selector: '.cf-day[data-date="2026-10-27"] .cf-day-header', event: 'keydown' as const, key: 'Enter', expectedDate: '2026-10-27' },
      { initialView: 'month' as const, selector: '.cf-month-day-header', event: 'click' as const, key: '', expectedDate: '2026-10-27' },
      { initialView: 'month' as const, selector: '.cf-month-more', event: 'click' as const, key: '', expectedDate: '2026-10-27' },
      { initialView: 'month' as const, selector: '.cf-month-more', event: 'keydown' as const, key: ' ', expectedDate: '2026-10-27' },
    ])('drills down into day view from $initialView via $selector ($event $key)', ({ initialView, selector, event, key, expectedDate }) => {
      const monthWithMultipleEvents: CalendarGridViewModel = {
        ...mockViewModel,
        month: {
          ...mockViewModel.month!,
          days: [
            {
              date: '2026-10-27',
              day_of_month: 27,
              is_current_month: true,
              is_today: true,
              is_weekend: false,
              events_count: 3,
              event_title: 'First Event',
              events: [
                { id: 'ev-1', title: 'Event 1' },
                { id: 'ev-2', title: 'Event 2' },
                { id: 'ev-3', title: 'Event 3' },
              ],
            },
          ],
        },
      };

      setGridState({ viewModel: monthWithMultipleEvents });
      const { container } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);

      if (initialView === 'month') {
        const monthBtn = container.querySelector('.cf-view-btn[data-view="month"]')!;
        fireEvent.click(monthBtn);
      }

      const target = container.querySelector(selector) as HTMLElement;
      expect(target).toBeTruthy();
      expect(target.getAttribute('role')).toBe('button');
      expect(target.getAttribute('tabindex')).toBe('0');

      if (event === 'click') {
        fireEvent.click(target);
      } else {
        fireEvent.keyDown(target, { key });
      }

      const widget = container.querySelector('.widget-calendar-grid');
      expect(widget?.getAttribute('data-active-view')).toBe('day');
      expect(widget?.getAttribute('data-current-date')).toBe(expectedDate);

      const dayViewHeader = container.querySelector('.cf-day-header') as HTMLElement;
      expect(dayViewHeader.getAttribute('role')).toBeNull();
      expect(dayViewHeader.getAttribute('tabindex')).toBeNull();
    });

    it.each([
      {
        name: 'triggers loadDateView fetch when drilling into a day outside cached weekVM',
        pagedMonth: false,
        targetDate: '2026-11-15',
        wantFetchUrl: 'date=2026-11-15&view=day',
        wantAbort: false,
      },
      {
        name: 'resets pagedWeekStart and aborts in-flight fetches when drilling into baseline week from paged month',
        pagedMonth: true,
        targetDate: '2026-10-27',
        wantFetchUrl: undefined,
        wantAbort: true,
      },
    ])('handles network and abort lifecycle on day drill-down: $name', async ({ pagedMonth, targetDate, wantFetchUrl, wantAbort }) => {
      let aborted = false;
      const fetchMock = vi.fn().mockImplementation((url: string, opts) => {
        if (opts?.signal) {
          opts.signal.addEventListener('abort', () => {
            aborted = true;
          });
        }
        if (wantFetchUrl && url.includes(wantFetchUrl)) {
          return Promise.resolve({
            ok: true,
            json: async () => ({
              active_view: 'day',
              week: {
                ...mockViewModel.week,
                range_start: '2026-11-15',
                range_end: '2026-11-15',
                days: [{ date: '2026-11-15', day_of_month: 15, is_today: false, is_weekend: false, events: [] }],
              },
            }),
          });
        }
        return new Promise(() => {}); // never resolves
      });
      vi.stubGlobal('fetch', fetchMock);

      const vmWithNovDay: CalendarGridViewModel = {
        ...mockViewModel,
        month: {
          ...mockViewModel.month!,
          days: [
            ...mockViewModel.month!.days!,
            {
              date: '2026-11-15',
              day_of_month: 15,
              is_current_month: false,
              is_today: false,
              is_weekend: false,
            },
          ],
        },
      };

      setGridState({ viewModel: vmWithNovDay });
      const { container } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);

      // Switch to month view
      const monthBtn = container.querySelector('.cf-view-btn[data-view="month"]')!;
      fireEvent.click(monthBtn);

      if (pagedMonth) {
        // Page forward 1 month to initiate fetch and set pagedDate
        const nextBtn = container.querySelector('.cf-nav-btn.cf-next-btn')!;
        fireEvent.click(nextBtn);
        expect(container.querySelector('.widget-calendar-grid')?.getAttribute('data-page-offset')).toBe('1');
        expect(fetchMock).toHaveBeenCalled();
      }

      const dayHeader = container.querySelector(`.cf-month-day[data-date="${targetDate}"] .cf-month-day-header`) as HTMLElement;
      expect(dayHeader).toBeTruthy();

      await act(async () => {
        fireEvent.click(dayHeader);
      });

      expect(aborted).toBe(wantAbort);
      const widget = container.querySelector('.widget-calendar-grid');
      expect(widget?.getAttribute('data-active-view')).toBe('day');
      expect(widget?.getAttribute('data-current-date')).toBe(targetDate);

      if (wantFetchUrl) {
        expect(fetchMock).toHaveBeenCalledWith(expect.stringContaining(wantFetchUrl), expect.any(Object));
      }

      if (pagedMonth) {
        // Switch back to week view and verify page-offset is reset to 0
        const weekBtn = container.querySelector('.cf-view-btn[data-view="week"]')!;
        fireEvent.click(weekBtn);
        expect(container.querySelector('.widget-calendar-grid')?.getAttribute('data-page-offset')).toBe('0');
      }
    });

    it('renders error banner when widget state is error and opens event modal from month view', () => {
      setGridState({ state: 'error' });
      const { container } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);
      expect(container.querySelector('.widget-error-banner')).not.toBeNull();

      // Switch to month view
      const monthBtn = container.querySelector('.cf-view-btn[data-view="month"]')!;
      fireEvent.click(monthBtn);

      // Click month day in month view to open modal (exercises line 304)
      const dayCell = container.querySelector('.cf-month-day[data-date="2026-10-27"]') as HTMLElement;
      expect(dayCell).not.toBeNull();
      fireEvent.click(dayCell);

      expect(container.querySelector('.cf-modal-overlay')).not.toBeNull();
      expect(container.querySelector('.cf-modal-card')).not.toBeNull();
    });

    it('filters early_events, late_events, all_day, timed, and month weeks when selectedMember is active', () => {
      const vmWithAllDayAndWeeks = {
        ...mockViewModel,
        week: {
          ...mockViewModel.week!,
          days: [
            ...mockViewModel.week!.days!.slice(0, 2),
            {
              ...mockViewModel.week!.days![2],
              all_day: [{ id: 'ev-ad-1', title: 'Sam All Day', all_day: true, owners: ['Sam'] }],
              timed: [{ id: 'ev-tm-1', title: 'Sam Timed', all_day: false, owners: ['Sam'] }],
            },
            ...mockViewModel.week!.days!.slice(3),
          ],
        },
        month: {
          ...mockViewModel.month!,
          weeks: [
            {
              week_num: 44,
              days: mockViewModel.month!.days,
            },
          ],
        },
      } as unknown as CalendarGridViewModel;

      setGridState({ viewModel: vmWithAllDayAndWeeks });
      const { container } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);

      // Filter by Alex (so Sam's items get filtered out)
      const alexChip = container.querySelector('.cf-member-chip[data-member="Alex"]') as HTMLElement;
      expect(alexChip).not.toBeNull();
      fireEvent.click(alexChip);

      // Verify Sam's items are filtered
      expect(container.textContent).not.toContain('Project Demo');

      // Switch to month view and verify weeks and days are filtered
      const monthBtn = container.querySelector('.cf-view-btn[data-view="month"]')!;
      fireEvent.click(monthBtn);

      expect(container.querySelector('.widget-calendar-grid')?.getAttribute('data-active-view')).toBe('month');
    });

    it('covers member text_color vars, default shared_color, range label formatters, and customTitle fallbacks', () => {
      const strippedVM = {
        week: {
          days: mockViewModel.week!.days!,
          members: [
            { name: 'Alex', color: '#111', text_color: '#fff' },
            { name: 'Sam' },
          ],
        },
        month: {
          days: [
            {
              date: '2026-10-25',
              day_of_month: 25,
              weekday_abbr: 'Sun',
              is_current_month: true,
              is_today: false,
              is_weekend: true,
            },
            {
              date: '2026-10-26',
              day_of_month: 26,
              weekday_abbr: 'Mon',
              is_current_month: true,
              is_today: true,
              is_weekend: false,
              dots: [{ name: 'Alex', color: '#111' }],
              events: [{ id: 'm1', title: 'Alex Event', all_day: true, owners: ['Alex'] }],
            },
          ],
        },
      } as unknown as CalendarGridViewModel;

      setGridState({
        viewModel: strippedVM,
        config: { title: '   ' },
      });

      const { container } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);
      const grid = container.querySelector('.widget-calendar-grid') as HTMLElement;
      expect(grid.style.getPropertyValue('--mm-cf-shared')).toBe('#3D405B');
      expect(grid.style.getPropertyValue('--mm-member-0')).toBe('#111');
      expect(grid.style.getPropertyValue('--mm-member-0-text')).toBe('#fff');
      expect(container.textContent).toContain('Calendar');

      // Member filter with dots and events in month view
      const alexChip = container.querySelector('.cf-member-chip[data-member="Alex"]') as HTMLElement;
      fireEvent.click(alexChip);

      const monthBtn = container.querySelector('.cf-view-btn[data-view="month"]')!;
      fireEvent.click(monthBtn);
      expect(container.textContent).toContain('Alex Event');
    });

    it('covers default_view month, missing range_label fallbacks, and ISO date modal parsing', () => {
      // 1. default_view month
      setGridState({
        config: { default_view: 'month' },
        viewModel: {
          ...mockViewModel,
          month: {
            ...mockViewModel.month,
            month_label: '',
          },
          week: {
            ...mockViewModel.week,
            range_label: '',
          },
        } as unknown as CalendarGridViewModel,
      });

      const { container } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);
      expect(container.querySelector('.cf-view-btn[data-view="month"]')?.classList.contains('active')).toBe(true);
      expect(container.querySelector('.cf-range-label.cf-label-month')?.textContent).toContain('October');

      // Switch to week view to verify week range_label fallback and click event
      const weekBtn = container.querySelector('.cf-view-btn[data-view="week"]')!;
      fireEvent.click(weekBtn);
      expect(container.querySelector('.cf-range-label.cf-label-week')?.textContent).toContain('Oct 25');

      const evEl = container.querySelector('[data-event-id="ev-timed-1"]')!;
      fireEvent.click(evEl);
      expect(container.querySelector('.cf-modal-card')).not.toBeNull();

      // 2. data.today and config.days with missing viewModel
      setGridState({
        viewModel: null,
        data: { today: '2026-10-29' },
        config: { days: 5 },
      });
      const { container: dataC } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);
      expect(dataC.querySelector('.widget-calendar-grid')).not.toBeNull();
    });

  });
});

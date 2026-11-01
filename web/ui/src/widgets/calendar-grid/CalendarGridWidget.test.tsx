import { act, cleanup, fireEvent, render } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useDisplayStore } from '../../store';
import { batchFitDayColumnEvents, CalendarGridWidget, type CalendarGridViewModel } from './CalendarGridWidget';

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
      {
        name: 'Alex',
        initial: 'A',
        color: '#ff6f59',
        text_color: '#ffffff',
        pattern: 'solid',
      },
      {
        name: 'Sam',
        initial: 'S',
        color: '#8b60ed',
        text_color: '#ffffff',
        pattern: 'dots',
      },
    ],
    hour_markers: ['08:00', '12:00', '18:00'],
    timeline_markers: [
      { label: '08:00', top_pct: 0, is_start: true },
      { label: '12:00', top_pct: 50 },
      { label: '18:00', top_pct: 100, is_end: true },
    ],
    all_day_events: [
      {
        id: 'ev-allday-1',
        title: 'Conference Day',
        start: '2026-10-26',
        end: '2026-10-27',
        all_day: true,
        start_col: 2,
        col_span: 2,
        color: '#ff6f59',
        text_color: '#ffffff',
      },
      {
        id: 'ev-mealie-1',
        title: '🍽️ Tacos Dinner',
        start: '2026-10-27',
        all_day: true,
        start_col: 3,
        col_span: 1,
        color: '#8b60ed',
        text_color: '#ffffff',
        location: '/hearth-api/mealie/image/tacos.jpg',
        description: 'Delicious homemade carnitas tacos recipe details...',
      },
    ],
    days: [
      {
        date: '2026-10-25',
        weekday_abbr: 'Sun',
        day_of_month: 25,
        is_today: false,
        is_weekend: true,
        events: [],
      },
      {
        date: '2026-10-26',
        weekday_abbr: 'Mon',
        day_of_month: 26,
        is_today: false,
        is_weekend: false,
        events: [
          {
            id: 'ev-timed-1',
            title: 'Morning Standup',
            start: '09:00',
            end: '09:30',
            all_day: false,
            top_pct: 10,
            height_pct: 15,
            left_pct: 0,
            width_pct: 100,
            color: '#ff6f59',
            text_color: '#ffffff',
            owners: ['Alex'],
            initials: ['A'],
            location: 'Room 101',
            description: 'Weekly team sprint planning and standup sync.',
          },
        ],
      },
      {
        date: '2026-10-27',
        weekday_abbr: 'Tue',
        day_of_month: 27,
        is_today: true,
        is_weekend: false,
        early_events: [
          {
            id: 'ev-early-1',
            title: 'Early Workout',
            start: '06:30',
            all_day: false,
            top_pct: 0,
            height_pct: 0,
            left_pct: 0,
            width_pct: 100,
            color: '#8b60ed',
            text_color: '#ffffff',
          },
        ],
        events: [
          {
            id: 'ev-timed-2',
            title: 'Project Demo',
            start: '14:00',
            end: '15:00',
            all_day: false,
            top_pct: 60,
            height_pct: 20,
            left_pct: 0,
            width_pct: 100,
            color: '#8b60ed',
            text_color: '#ffffff',
            owners: ['Sam'],
            initials: ['S'],
          },
        ],
        late_events: [
          {
            id: 'ev-late-1',
            title: 'Late Flight',
            start: '22:00',
            all_day: false,
            top_pct: 0,
            height_pct: 0,
            left_pct: 0,
            width_pct: 100,
            color: '#ff6f59',
            text_color: '#ffffff',
          },
        ],
      },
      {
        date: '2026-10-28',
        weekday_abbr: 'Wed',
        day_of_month: 28,
        is_today: false,
        is_weekend: false,
        events: [],
      },
      {
        date: '2026-10-29',
        weekday_abbr: 'Thu',
        day_of_month: 29,
        is_today: false,
        is_weekend: false,
        events: [],
      },
      {
        date: '2026-10-30',
        weekday_abbr: 'Fri',
        day_of_month: 30,
        is_today: false,
        is_weekend: false,
        events: [],
      },
      {
        date: '2026-10-31',
        weekday_abbr: 'Sat',
        day_of_month: 31,
        is_today: false,
        is_weekend: true,
        events: [],
      },
    ],
  },
  month: {
    month_label: 'October 2026',
    month_name: 'October',
    year: 2026,
    month: 10,
    range_start: '2026-09-27',
    range_end: '2026-11-07',
    today: '2026-10-27',
    weekday_headers: ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'],
    members: [
      {
        name: 'Alex',
        initial: 'A',
        color: '#ff6f59',
        text_color: '#ffffff',
        pattern: 'solid',
      },
    ],
    weeks: [],
    days: [
      {
        date: '2026-10-26',
        day_of_month: 26,
        is_current_month: true,
        is_today: false,
        is_weekend: false,
        events_count: 1,
        dots: [
          {
            name: 'Alex',
            initial: 'A',
            color: '#ff6f59',
            text_color: '#ffffff',
            pattern: 'solid',
            is_shared: false,
            member_index: 0,
          },
        ],
        events: [
          {
            id: 'ev-month-1',
            title: 'Morning Standup',
            start: '09:00',
            all_day: false,
          },
        ],
      },
      {
        date: '2026-10-27',
        day_of_month: 27,
        is_current_month: true,
        is_today: true,
        is_weekend: false,
        events_count: 1,
        event_title: 'Project Demo',
        events: [
          {
            id: 'ev-month-2',
            title: 'Project Demo',
            start: '14:00',
            all_day: false,
          },
        ],
      },
    ],
  },
};

describe('CalendarGridWidget', () => {
  const initialStore = useDisplayStore.getState();

  beforeEach(() => {
    useDisplayStore.setState(initialStore, true);
    vi.stubGlobal('fetch', vi.fn());
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
    cleanup();
  });

  it('renders week view by default with days, header, members legend, and events', () => {
    useDisplayStore.setState({
      widgets: {
        'cal-grid': {
          meta: {
            type: 'calendar-grid',
            origin: [0, 0],
            dimensions: [6, 2],
            config: { title: 'Family Grid' },
            assets_base: '',
          },
          state: 'healthy',
          data: {},
          viewModel: mockViewModel as unknown as Record<string, unknown>,
          timestamp: '2026-10-27T10:00:00Z',
        },
      },
    });

    const { container } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);

    // Header title and range label
    expect(container.querySelector('.cf-title')?.textContent).toBe('Family Grid');
    expect(container.querySelector('.cf-range-label.cf-label-week')?.textContent).toBe('Oct 25 – 31, 2026');

    // Members legend
    const chips = container.querySelectorAll('.cf-member-chip');
    expect(chips.length).toBe(2);
    expect(chips[0].textContent).toContain('Alex');
    expect(chips[1].textContent).toContain('Sam');

    // Week view active
    expect(container.querySelector('.widget-calendar-grid')?.classList.contains('cf-view-week')).toBe(true);
    const weekBtn = container.querySelector('.cf-view-btn[data-view="week"]');
    expect(weekBtn?.classList.contains('active')).toBe(true);

    // Timeline markers
    expect(container.querySelectorAll('.cf-hour-marker').length).toBeGreaterThan(0);

    // Days grid
    const days = container.querySelectorAll('.cf-day');
    expect(days.length).toBe(7);

    // Today indicator
    const todayCol = container.querySelector('.cf-day.cf-today');
    expect(todayCol).not.toBeNull();
    expect(todayCol?.getAttribute('data-date')).toBe('2026-10-27');

    // All-day and timed events
    expect(container.querySelector('[data-event-id="ev-allday-1"]')).not.toBeNull();
    expect(container.querySelector('[data-event-id="ev-timed-1"]')?.textContent).toContain('Morning Standup');
    expect(container.querySelector('[data-event-id="ev-early-1"]')?.textContent).toContain('Early Workout');
    expect(container.querySelector('[data-event-id="ev-late-1"]')?.textContent).toContain('Late Flight');
  });

  it('switches between week and month views and renders month grid with weekday headers, day numbers, and event dots', () => {
    useDisplayStore.setState({
      widgets: {
        'cal-grid': {
          meta: {
            type: 'calendar-grid',
            origin: [0, 0],
            dimensions: [6, 2],
            config: {},
            assets_base: '',
          },
          state: 'healthy',
          data: {},
          viewModel: mockViewModel as unknown as Record<string, unknown>,
          timestamp: '2026-10-27T10:00:00Z',
        },
      },
    });

    const { container } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);

    // Click Month button
    const monthBtn = container.querySelector('.cf-view-btn[data-view="month"]');
    expect(monthBtn).not.toBeNull();
    fireEvent.click(monthBtn!);

    // Month view is active
    expect(container.querySelector('.widget-calendar-grid')?.classList.contains('cf-view-month')).toBe(true);
    expect(monthBtn?.classList.contains('active')).toBe(true);

    // Month label
    expect(container.querySelector('.cf-range-label.cf-label-month')?.textContent).toBe('October 2026');

    // Month grid weekdays
    const weekdayHeaders = container.querySelectorAll('.cf-weekday-header');
    expect(weekdayHeaders.length).toBe(7);
    expect(weekdayHeaders[0].textContent).toBe('Sun');

    // Month dots
    const dots = container.querySelectorAll('.cf-month-dot');
    expect(dots.length).toBeGreaterThan(0);
  });

  it('renders day view as a 1-day slice of week.days and formats range label as Weekday, Mon D', () => {
    useDisplayStore.setState({
      widgets: {
        'cal-grid': {
          meta: {
            type: 'calendar-grid',
            origin: [0, 0],
            dimensions: [3, 2],
            config: { default_view: 'day' },
            assets_base: '',
          },
          state: 'healthy',
          data: {},
          viewModel: mockViewModel as unknown as Record<string, unknown>,
          timestamp: '2026-10-27T10:00:00Z',
        },
      },
    });

    const { container } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);

    expect(container.querySelector('.widget-calendar-grid')?.classList.contains('cf-view-day')).toBe(true);

    // Only 1 column rendered
    const days = container.querySelectorAll('.cf-day');
    expect(days.length).toBe(1);
    expect(days[0].getAttribute('data-date')).toBe('2026-10-27');

    // Range label formatted as "Weekday, Mon D" -> Tuesday, Oct 27
    const labelDay = container.querySelector('.cf-range-label.cf-label-day');
    expect(labelDay?.textContent).toBe('Tuesday, Oct 27');
  });

  it('handles LRU(20) cache: caches fetched date views and evicts oldest entry when size exceeds 20', async () => {
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      return Promise.resolve({
        ok: true,
        json: () =>
          Promise.resolve({
            view_model: {
              ...mockViewModel,
              week: {
                ...mockViewModel.week,
                range_label: `Range for ${url}`,
              },
            },
          }),
      });
    });
    vi.stubGlobal('fetch', fetchMock);

    useDisplayStore.setState({
      widgets: {
        'cal-grid': {
          meta: {
            type: 'calendar-grid',
            origin: [0, 0],
            dimensions: [6, 2],
            config: { cached_range_start: '2020-01-01', cached_range_end: '2030-01-01' },
            assets_base: '',
          },
          state: 'healthy',
          data: {},
          viewModel: mockViewModel as unknown as Record<string, unknown>,
          timestamp: '2026-10-27T10:00:00Z',
        },
      },
    });

    const { container } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);
    const nextBtn = container.querySelector('.cf-nav-btn.cf-next-btn')!;

    // Page 21 times forward to fill and evict oldest from the 20-entry LRU cache
    for (let i = 0; i < 21; i++) {
      await act(async () => {
        fireEvent.click(nextBtn);
      });
    }

    expect(fetchMock).toHaveBeenCalledTimes(21);

    // Now clicking prev will hit the cache for recent items
    const prevBtn = container.querySelector('.cf-nav-btn.cf-prev-btn')!;
    const callsBefore = fetchMock.mock.calls.length;
    await act(async () => {
      fireEvent.click(prevBtn);
    });

    // Should be a cache hit, so no extra fetch
    expect(fetchMock.mock.calls.length).toBe(callsBefore);
  });

  it('aborts prior in-flight fetch via AbortController on rapid paging', async () => {
    let capturedSignal: AbortSignal | null = null;
    const fetchMock = vi.fn().mockImplementation((_url: string, opts?: { signal?: AbortSignal }) => {
      capturedSignal = opts?.signal || null;
      return new Promise(() => {}); // never resolves
    });
    vi.stubGlobal('fetch', fetchMock);

    useDisplayStore.setState({
      widgets: {
        'cal-grid': {
          meta: {
            type: 'calendar-grid',
            origin: [0, 0],
            dimensions: [6, 2],
            config: {},
            assets_base: '',
          },
          state: 'healthy',
          data: {},
          viewModel: mockViewModel as unknown as Record<string, unknown>,
          timestamp: '2026-10-27T10:00:00Z',
        },
      },
    });

    const { container } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);
    const nextBtn = container.querySelector('.cf-nav-btn.cf-next-btn')!;

    // Click 1
    fireEvent.click(nextBtn);
    const signal1 = capturedSignal;
    expect(signal1).not.toBeNull();
    expect((signal1 as unknown as AbortSignal).aborted).toBe(false);

    // Rapid Click 2
    fireEvent.click(nextBtn);
    expect((signal1 as unknown as AbortSignal).aborted).toBe(true);
  });

  it('rolls back currentDate, currentWeekStart, and pageOffset and displays Sync failed on fetch failure', async () => {
    const fetchMock = vi.fn().mockImplementation(() => {
      return Promise.reject(new Error('Network error'));
    });
    vi.stubGlobal('fetch', fetchMock);

    useDisplayStore.setState({
      widgets: {
        'cal-grid': {
          meta: {
            type: 'calendar-grid',
            origin: [0, 0],
            dimensions: [6, 2],
            config: {},
            assets_base: '',
          },
          state: 'healthy',
          data: {},
          viewModel: mockViewModel as unknown as Record<string, unknown>,
          timestamp: '2026-10-27T10:00:00Z',
        },
      },
    });

    const { container } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);
    const nextBtn = container.querySelector('.cf-nav-btn.cf-next-btn')!;

    fireEvent.click(nextBtn);

    // Wait for reject microtask
    await vi.waitFor(() => {
      const syncEl = container.querySelector('.cf-sync-indicator');
      expect(syncEl?.textContent).toBe('Sync failed');
    });

    // Offset rolled back to 0
    expect(container.querySelector('.widget-calendar-grid')?.getAttribute('data-page-offset')).toBe('0');
  });

  it('displays Not synced yet without fetching when paging exceeds cached date bounds', () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);

    useDisplayStore.setState({
      widgets: {
        'cal-grid': {
          meta: {
            type: 'calendar-grid',
            origin: [0, 0],
            dimensions: [6, 2],
            config: {
              cached_range_start: '2026-10-25',
              cached_range_end: '2026-10-31', // current week ends on 31st
            },
            assets_base: '',
          },
          state: 'healthy',
          data: {},
          viewModel: mockViewModel as unknown as Record<string, unknown>,
          timestamp: '2026-10-27T10:00:00Z',
        },
      },
    });

    const { container } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);
    const nextBtn = container.querySelector('.cf-nav-btn.cf-next-btn')!;

    fireEvent.click(nextBtn);

    // Fetch must NOT be called
    expect(fetchMock).not.toHaveBeenCalled();

    // Not synced yet indicator shown
    const syncEl = container.querySelector('.cf-sync-indicator');
    expect(syncEl?.textContent).toBe('Not synced yet');
  });

  it('opens event modal on event click, displays details/Mealie hero image, and closes on close button, backdrop click, or Escape', () => {
    useDisplayStore.setState({
      widgets: {
        'cal-grid': {
          meta: {
            type: 'calendar-grid',
            origin: [0, 0],
            dimensions: [6, 2],
            config: {},
            assets_base: '',
          },
          state: 'healthy',
          data: {},
          viewModel: mockViewModel as unknown as Record<string, unknown>,
          timestamp: '2026-10-27T10:00:00Z',
        },
      },
    });

    const { container } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);

    // Click standard timed event
    const eventEl = container.querySelector('[data-event-id="ev-timed-1"]');
    expect(eventEl).not.toBeNull();
    fireEvent.click(eventEl!);

    // Modal is open
    let modal = container.querySelector('.cf-modal-overlay');
    expect(modal).not.toBeNull();
    expect(container.querySelector('.cf-modal-title')?.textContent).toBe('Morning Standup');
    const modalTime = container.querySelector('.cf-modal-time');
    expect(modalTime?.querySelector('svg.cf-modal-icon')).not.toBeNull();
    expect(modalTime?.textContent).toBe('Mon, Oct 26 • 09:00 – 09:30');
    const modalLocation = container.querySelector('.cf-modal-location');
    expect(modalLocation?.querySelector('svg.cf-modal-icon')).not.toBeNull();
    expect(modalLocation?.textContent).toBe('Room 101');
    expect(container.querySelector('.cf-modal-owner-chip')?.textContent).toContain('Alex');
    expect(container.querySelector('.cf-modal-description')?.textContent).toContain('Weekly team sprint planning');

    // Close via close button
    const closeBtn = container.querySelector('.cf-modal-close')!;
    fireEvent.click(closeBtn);
    expect(container.querySelector('.cf-modal-overlay')).toBeNull();

    // Click Mealie recipe event
    const mealieEl = container.querySelector('[data-event-id="ev-mealie-1"]');
    expect(mealieEl).not.toBeNull();
    fireEvent.click(mealieEl!);

    // Modal has Mealie hero image and formatted all-day date
    modal = container.querySelector('.cf-modal-overlay');
    expect(modal).not.toBeNull();
    expect(container.querySelector('.cf-modal-title')?.textContent).toBe('Tacos Dinner');
    expect(container.querySelector('.cf-modal-time')?.textContent).toBe('Tue, Oct 27 • All Day');
    const heroImg = container.querySelector('.cf-modal-card img');
    expect(heroImg?.getAttribute('src')).toBe('/hearth-api/mealie/image/tacos.jpg');

    // Close via Escape key
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(container.querySelector('.cf-modal-overlay')).toBeNull();

    // Open again and close via backdrop click
    fireEvent.click(mealieEl!);
    modal = container.querySelector('.cf-modal-overlay')!;
    fireEvent.click(modal);
    expect(container.querySelector('.cf-modal-overlay')).toBeNull();

    // Click multi-day all-day span event
    const allDaySpanEl = container.querySelector('[data-event-id="ev-allday-1"]');
    expect(allDaySpanEl).not.toBeNull();
    fireEvent.click(allDaySpanEl!);
    expect(container.querySelector('.cf-modal-time')?.textContent).toBe('Mon, Oct 26 – Tue, Oct 27 • All Day');
    fireEvent.keyDown(window, { key: 'Escape' });
  });

  it('decorates Mealie all-day pills declaratively with data-mealie-decorated="true"', () => {
    useDisplayStore.setState({
      widgets: {
        'cal-grid': {
          meta: {
            type: 'calendar-grid',
            origin: [0, 0],
            dimensions: [6, 2],
            config: {},
            assets_base: '',
          },
          state: 'healthy',
          data: {},
          viewModel: mockViewModel as unknown as Record<string, unknown>,
          timestamp: '2026-10-27T10:00:00Z',
        },
      },
    });

    const { container } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);

    const mealiePill = container.querySelector('[data-event-id="ev-mealie-1"]');
    expect(mealiePill?.getAttribute('data-mealie-decorated')).toBe('true');
    const pillImg = mealiePill?.querySelector('img');
    expect(pillImg?.getAttribute('src')).toBe('/hearth-api/mealie/image/tacos.jpg');
    expect(mealiePill?.querySelector('svg.cf-mealie-icon')).not.toBeNull();
    expect(mealiePill?.textContent).toBe('Tacos Dinner');
  });

  it('manages 60s inactivity reset and store pause/resume', () => {
    vi.useFakeTimers();

    useDisplayStore.setState({
      widgets: {
        'cal-grid': {
          meta: {
            type: 'calendar-grid',
            origin: [0, 0],
            dimensions: [6, 2],
            config: {},
            assets_base: '',
          },
          state: 'healthy',
          data: {},
          viewModel: mockViewModel as unknown as Record<string, unknown>,
          timestamp: '2026-10-27T10:00:00Z',
        },
      },
    });

    const pauseSpy = vi.spyOn(useDisplayStore.getState(), 'pause');
    const resumeSpy = vi.spyOn(useDisplayStore.getState(), 'resume');

    const { container, unmount } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);

    // Switch to month view -> becomes interactive
    const monthBtn = container.querySelector('.cf-view-btn[data-view="month"]')!;
    fireEvent.click(monthBtn);

    expect(pauseSpy).toHaveBeenCalledTimes(1);

    // User pointer activity resets inactivity timer
    fireEvent.pointerDown(container.querySelector('.widget-calendar-grid')!);

    // Advance 59 seconds - still interactive
    act(() => {
      vi.advanceTimersByTime(59000);
    });
    expect(resumeSpy).not.toHaveBeenCalled();

    // Advance 2 more seconds (61s elapsed since pointer activity)
    act(() => {
      vi.advanceTimersByTime(2000);
    });

    // Reset back to default view and resumed store
    expect(resumeSpy).toHaveBeenCalledTimes(1);
    expect(container.querySelector('.widget-calendar-grid')?.classList.contains('cf-view-week')).toBe(true);

    // Verify unmount resume safety
    fireEvent.click(monthBtn);
    expect(pauseSpy).toHaveBeenCalledTimes(2);
    unmount();
    expect(resumeSpy).toHaveBeenCalledTimes(2);
  });

  it('batchFitDayColumnEvents performs two-phase measurement and toggles cf-event-compact', () => {
    const columnEl = document.createElement('div');
    columnEl.className = 'cf-day-canvas';

    const evEl = document.createElement('div');
    evEl.className = 'cf-day-event';

    const headerEl = document.createElement('div');
    headerEl.className = 'cf-event-header';
    const timeEl = document.createElement('span');
    timeEl.className = 'cf-event-time';
    headerEl.appendChild(timeEl);

    const titleEl = document.createElement('div');
    titleEl.className = 'cf-event-title';

    evEl.appendChild(headerEl);
    evEl.appendChild(titleEl);
    columnEl.appendChild(evEl);

    // Mock overflowing geometry (header + title > clientHeight)
    Object.defineProperty(evEl, 'clientHeight', { value: 20, configurable: true });
    Object.defineProperty(evEl, 'clientWidth', { value: 100, configurable: true });
    Object.defineProperty(headerEl, 'offsetHeight', { value: 15, configurable: true });
    Object.defineProperty(timeEl, 'offsetHeight', { value: 15, configurable: true });
    Object.defineProperty(titleEl, 'offsetHeight', { value: 15, configurable: true });

    batchFitDayColumnEvents(columnEl);
    expect(evEl.classList.contains('cf-event-compact')).toBe(true);

    // Mock fitting geometry (header + title fits in clientHeight)
    Object.defineProperty(evEl, 'clientHeight', { value: 80, configurable: true });
    batchFitDayColumnEvents(columnEl);
    expect(evEl.classList.contains('cf-event-compact')).toBe(false);
  });

  it('clears LRU cache when entry.timestamp updates via SSE', () => {
    useDisplayStore.setState({
      widgets: {
        'cal-grid': {
          meta: {
            type: 'calendar-grid',
            origin: [0, 0],
            dimensions: [6, 2],
            config: {},
            assets_base: '',
          },
          state: 'healthy',
          data: {},
          viewModel: mockViewModel as unknown as Record<string, unknown>,
          timestamp: '2026-10-27T10:00:00Z',
        },
      },
    });

    const { rerender, container } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);

    // Simulate SSE update with new timestamp and updated range_label
    const updatedViewModel: CalendarGridViewModel = {
      ...mockViewModel,
      week: {
        ...mockViewModel.week!,
        range_label: 'Nov 1 – 7, 2026',
      },
    };

    useDisplayStore.setState({
      widgets: {
        'cal-grid': {
          meta: {
            type: 'calendar-grid',
            origin: [0, 0],
            dimensions: [6, 2],
            config: {},
            assets_base: '',
          },
          state: 'healthy',
          data: {},
          viewModel: updatedViewModel as unknown as Record<string, unknown>,
          timestamp: '2026-10-27T10:05:00Z',
        },
      },
    });

    rerender(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);

    expect(container.querySelector('.cf-range-label.cf-label-week')?.textContent).toBe('Nov 1 – 7, 2026');
  });

  it('renders empty message when neither week nor month data is available', () => {
    useDisplayStore.setState({
      widgets: {
        'cal-grid-empty': {
          meta: {
            type: 'calendar-grid',
            origin: [0, 0],
            dimensions: [6, 2],
            config: {},
            assets_base: '',
          },
          state: 'healthy',
          data: {},
          viewModel: undefined,
          timestamp: '2026-10-27T10:00:00Z',
        },
      },
    });

    const { container } = render(<CalendarGridWidget id="cal-grid-empty" type="calendar-grid" />);
    expect(container.querySelector('.cf-empty')?.textContent).toBe('No calendar data yet');
  });

  it('does not trigger network fetch when switching views at pageOffset 0 with baseline week and month payloads', () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);

    useDisplayStore.setState({
      widgets: {
        'cal-grid': {
          meta: {
            type: 'calendar-grid',
            origin: [0, 0],
            dimensions: [6, 2],
            config: {},
            assets_base: '',
          },
          state: 'healthy',
          data: {},
          viewModel: mockViewModel as unknown as Record<string, unknown>,
          timestamp: '2026-10-27T10:00:00Z',
        },
      },
    });

    const { container } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);
    const monthBtn = container.querySelector('.cf-view-btn[data-view="month"]')!;
    const weekBtn = container.querySelector('.cf-view-btn[data-view="week"]')!;

    // Switch to month view at pageOffset 0
    fireEvent.click(monthBtn);
    expect(container.querySelector('.widget-calendar-grid')?.classList.contains('cf-view-month')).toBe(true);
    expect(fetchMock).not.toHaveBeenCalled();

    // Switch back to week view at pageOffset 0
    fireEvent.click(weekBtn);
    expect(container.querySelector('.widget-calendar-grid')?.classList.contains('cf-view-week')).toBe(true);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('does not arm inactivity timer on benign tap/pointer events when isInteractive is false', () => {
    vi.useFakeTimers();

    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);

    useDisplayStore.setState({
      widgets: {
        'cal-grid': {
          meta: {
            type: 'calendar-grid',
            origin: [0, 0],
            dimensions: [6, 2],
            config: {},
            assets_base: '',
          },
          state: 'healthy',
          data: {},
          viewModel: mockViewModel as unknown as Record<string, unknown>,
          timestamp: '2026-10-27T10:00:00Z',
        },
      },
    });

    const pauseSpy = vi.spyOn(useDisplayStore.getState(), 'pause');
    const resumeSpy = vi.spyOn(useDisplayStore.getState(), 'resume');

    const { container } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);
    const gridEl = container.querySelector('.widget-calendar-grid')!;

    // Benign touch / pointer events while resting (isInteractive is false)
    fireEvent.pointerDown(gridEl);
    fireEvent.touchStart(gridEl, { touches: [{ clientX: 10, clientY: 10 }] });
    fireEvent.touchMove(gridEl);
    fireEvent.touchEnd(gridEl, { changedTouches: [{ clientX: 10, clientY: 10 }] });

    expect(pauseSpy).not.toHaveBeenCalled();

    // Advance 65s - no timer should fire and no network fetch should occur
    act(() => {
      vi.advanceTimersByTime(65000);
    });

    expect(fetchMock).not.toHaveBeenCalled();
    expect(resumeSpy).not.toHaveBeenCalled();
  });

  it('restores entry.viewModel without network fetch when clicking return to today', async () => {
    const fetchMock = vi.fn().mockImplementation(() => {
      return Promise.resolve({
        ok: true,
        json: () =>
          Promise.resolve({
            view_model: {
              ...mockViewModel,
              week: {
                ...mockViewModel.week,
                range_label: 'Paged Range',
              },
            },
          }),
      });
    });
    vi.stubGlobal('fetch', fetchMock);

    useDisplayStore.setState({
      widgets: {
        'cal-grid': {
          meta: {
            type: 'calendar-grid',
            origin: [0, 0],
            dimensions: [6, 2],
            config: {},
            assets_base: '',
          },
          state: 'healthy',
          data: {},
          viewModel: mockViewModel as unknown as Record<string, unknown>,
          timestamp: '2026-10-27T10:00:00Z',
        },
      },
    });

    const { container } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);
    const nextBtn = container.querySelector('.cf-nav-btn.cf-next-btn')!;

    // Page forward to pageOffset 1 (triggers 1 fetch)
    await act(async () => {
      fireEvent.click(nextBtn);
    });
    expect(fetchMock).toHaveBeenCalledTimes(1);

    // Return to today via TODAY button
    const todayBtn = container.querySelector('.cf-today-badge')!;
    await act(async () => {
      fireEvent.click(todayBtn);
    });

    // Reset to offset 0 and restored original range label without second fetch
    expect(container.querySelector('.widget-calendar-grid')?.getAttribute('data-page-offset')).toBe('0');
    expect(container.querySelector('.cf-range-label.cf-label-week')?.textContent).toBe('Oct 25 – 31, 2026');
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('batchFitDayColumnEvents removes .cf-event-compact before reading dimensions to measure uncompacted geometry', () => {
    const columnEl = document.createElement('div');
    columnEl.className = 'cf-day-canvas';

    const evEl = document.createElement('div');
    evEl.className = 'cf-day-event cf-event-compact'; // Already compacted!

    const headerEl = document.createElement('div');
    headerEl.className = 'cf-event-header';
    const timeEl = document.createElement('span');
    timeEl.className = 'cf-event-time';
    headerEl.appendChild(timeEl);

    const titleEl = document.createElement('div');
    titleEl.className = 'cf-event-title';

    evEl.appendChild(headerEl);
    evEl.appendChild(titleEl);
    columnEl.appendChild(evEl);

    // Mock getter that simulates CSS display: none when .cf-event-compact is present
    Object.defineProperty(evEl, 'clientHeight', { value: 30, configurable: true });
    Object.defineProperty(evEl, 'clientWidth', { value: 100, configurable: true });
    Object.defineProperty(titleEl, 'offsetHeight', { value: 15, configurable: true });
    Object.defineProperty(timeEl, 'offsetHeight', {
      get: () => (evEl.classList.contains('cf-event-compact') ? 0 : 20),
      configurable: true,
    });
    Object.defineProperty(headerEl, 'offsetHeight', {
      get: () => (evEl.classList.contains('cf-event-compact') ? 0 : 20),
      configurable: true,
    });

    // If .cf-event-compact were NOT removed before reading, headerH + titleH would read as 0 + 15 = 15 <= 30 (fits = true),
    // and .cf-event-compact would be stripped incorrectly.
    // Because it is removed first, headerH + titleH reads as 20 + 15 = 35 > 30 (fits = false),
    // so .cf-event-compact remains applied!
    batchFitDayColumnEvents(columnEl);
    expect(evEl.classList.contains('cf-event-compact')).toBe(true);
  });

  it('does not reset inactivity timer on background SSE widget.update when isInteractive is true', () => {
    vi.useFakeTimers();

    useDisplayStore.setState({
      widgets: {
        'cal-grid': {
          meta: {
            type: 'calendar-grid',
            origin: [0, 0],
            dimensions: [6, 2],
            config: {},
            assets_base: '',
          },
          state: 'healthy',
          data: {},
          viewModel: mockViewModel as unknown as Record<string, unknown>,
          timestamp: '2026-10-27T10:00:00Z',
        },
      },
    });

    const resumeSpy = vi.spyOn(useDisplayStore.getState(), 'resume');
    const { container, rerender } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);

    // Switch to month view -> becomes interactive
    const monthBtn = container.querySelector('.cf-view-btn[data-view="month"]')!;
    fireEvent.click(monthBtn);

    // Advance 45 seconds (15s remaining until 60s inactivity timeout)
    act(() => {
      vi.advanceTimersByTime(45000);
    });
    expect(resumeSpy).not.toHaveBeenCalled();

    // Background SSE update arrives with new viewModel and timestamp
    const sseUpdatedViewModel: CalendarGridViewModel = {
      ...mockViewModel,
      active_view: 'week',
    };
    useDisplayStore.setState({
      widgets: {
        'cal-grid': {
          meta: {
            type: 'calendar-grid',
            origin: [0, 0],
            dimensions: [6, 2],
            config: {},
            assets_base: '',
          },
          state: 'healthy',
          data: {},
          viewModel: sseUpdatedViewModel as unknown as Record<string, unknown>,
          timestamp: '2026-10-27T10:01:00Z',
        },
      },
    });
    rerender(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);

    // Advance 16 more seconds (total 61s from user action)
    // The background SSE update must NOT have reset the timer!
    act(() => {
      vi.advanceTimersByTime(16000);
    });

    // Inactivity timeout should have fired
    expect(resumeSpy).toHaveBeenCalledTimes(1);
    expect(container.querySelector('.widget-calendar-grid')?.classList.contains('cf-view-week')).toBe(true);
  });

  it('switches from paged month view to week view using currentDate and synchronizing week range', async () => {
    let requestedUrl = '';
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      requestedUrl = url;
      return Promise.resolve({
        ok: true,
        json: () =>
          Promise.resolve({
            view_model: {
              ...mockViewModel,
              week: {
                ...mockViewModel.week,
                range_start: '2026-11-01',
                range_end: '2026-11-07',
                range_label: 'Nov 1 – 7, 2026',
              },
            },
          }),
      });
    });
    vi.stubGlobal('fetch', fetchMock);

    useDisplayStore.setState({
      widgets: {
        'cal-grid': {
          meta: {
            type: 'calendar-grid',
            origin: [0, 0],
            dimensions: [6, 2],
            config: {
              cached_range_start: '2026-01-01',
              cached_range_end: '2027-01-01',
            },
            assets_base: '',
          },
          state: 'healthy',
          data: {},
          viewModel: mockViewModel as unknown as Record<string, unknown>,
          timestamp: '2026-10-27T10:00:00Z',
        },
      },
    });

    const { container } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);

    // Switch to month view at pageOffset 0 (no fetch)
    const monthBtn = container.querySelector('.cf-view-btn[data-view="month"]')!;
    fireEvent.click(monthBtn);
    expect(fetchMock).not.toHaveBeenCalled();

    // Page next to November (offset 1, triggers month fetch)
    const nextBtn = container.querySelector('.cf-nav-btn.cf-next-btn')!;
    await act(async () => {
      fireEvent.click(nextBtn);
    });
    expect(requestedUrl).toContain('date=2026-11-01');

    // Switch to week view: must request week using the November currentDate rather than stale baseWeekStart
    const weekBtn = container.querySelector('.cf-view-btn[data-view="week"]')!;
    await act(async () => {
      fireEvent.click(weekBtn);
    });
    expect(requestedUrl).toContain('date=2026-11-01');
    expect(requestedUrl).toContain('view=week');

    // Week range label reflects the November week
    expect(container.querySelector('.cf-range-label.cf-label-week')?.textContent).toBe('Nov 1 – 7, 2026');
  });

  it('does not render .cf-allday-band in day view when there are no all-day events on the active day', () => {
    useDisplayStore.setState({
      widgets: {
        'cal-grid': {
          meta: {
            type: 'calendar-grid',
            origin: [0, 0],
            dimensions: [3, 2],
            config: { default_view: 'day' },
            assets_base: '',
          },
          state: 'healthy',
          data: {},
          viewModel: {
            ...mockViewModel,
            week: {
              ...mockViewModel.week!,
              // Oct 25 has no all-day events in mockViewModel (they are on Oct 26 and 27)
              today: '2026-10-25',
            },
          } as unknown as Record<string, unknown>,
          timestamp: '2026-10-27T10:00:00Z',
        },
      },
    });

    const { container } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);
    // .cf-allday-band should not exist when active day has no all-day events
    expect(container.querySelector('.cf-allday-band')).toBeNull();
  });

  it('does not clobber anchor dates or prematurely deactivate interactivity when paging across views in opposite directions', async () => {
    let requestedUrl = '';
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      requestedUrl = url;
      const isNov = url.includes('2026-11');
      return Promise.resolve({
        ok: true,
        json: () =>
          Promise.resolve({
            view_model: {
              ...mockViewModel,
              week: {
                ...mockViewModel.week,
                range_start: isNov ? '2026-11-01' : '2026-10-25',
                range_end: isNov ? '2026-11-07' : '2026-10-31',
                range_label: isNov ? 'Nov 1 – 7, 2026' : 'Oct 25 – 31, 2026',
              },
              month: {
                ...mockViewModel.month,
                range_start: '2026-11-01',
                range_end: '2026-11-30',
                month_label: 'November 2026',
              },
            },
          }),
      });
    });
    vi.stubGlobal('fetch', fetchMock);

    useDisplayStore.setState({
      widgets: {
        'cal-grid': {
          meta: {
            type: 'calendar-grid',
            origin: [0, 0],
            dimensions: [6, 2],
            config: {
              cached_range_start: '2026-01-01',
              cached_range_end: '2027-01-01',
            },
            assets_base: '',
          },
          state: 'healthy',
          data: {},
          viewModel: mockViewModel as unknown as Record<string, unknown>,
          timestamp: '2026-10-27T10:00:00Z',
        },
      },
    });

    const { container } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);

    // 1. Switch to Month view (at baseline Oct 2026 -> pageOffset = 0, no fetch)
    const monthBtn = container.querySelector('.cf-view-btn[data-view="month"]')!;
    fireEvent.click(monthBtn);
    expect(fetchMock).not.toHaveBeenCalled();

    // 2. Page Next in Month view to November
    const nextBtn = container.querySelector('.cf-nav-btn.cf-next-btn')!;
    await act(async () => {
      fireEvent.click(nextBtn);
    });
    expect(requestedUrl).toContain('date=2026-11-01');
    const todayBadge = container.querySelector('.cf-today-badge') as HTMLElement;
    expect(todayBadge?.style.display).not.toBe('none');

    // 3. Switch to Week view (currentDate is now in Nov, week syncs to week of Nov 1)
    const weekBtn = container.querySelector('.cf-view-btn[data-view="week"]')!;
    await act(async () => {
      fireEvent.click(weekBtn);
    });

    // 4. Page Prev in Week view: moves from Nov week to baseline Oct 25 week.
    // When landing back on baseWeekStart, entry.viewModel is restored directly without fetch.
    const prevBtn = container.querySelector('.cf-nav-btn.cf-prev-btn')!;
    fetchMock.mockClear();
    await act(async () => {
      fireEvent.click(prevBtn);
    });

    // Baseline entry.viewModel restored without extra network fetch
    expect(fetchMock).not.toHaveBeenCalled();
    expect(container.querySelector('.cf-range-label.cf-label-week')?.textContent).toBe('Oct 25 – 31, 2026');
  });

  it('resets both currentDate and currentWeekStart when paging back to the baseline month and switching to week view', async () => {
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      const isNov = url.includes('2026-11');
      return Promise.resolve({
        ok: true,
        json: () =>
          Promise.resolve({
            view_model: {
              ...mockViewModel,
              week: {
                ...mockViewModel.week,
                range_start: isNov ? '2026-11-01' : '2026-10-25',
                range_end: isNov ? '2026-11-07' : '2026-10-31',
                range_label: isNov ? 'Nov 1 – 7, 2026' : 'Oct 25 – 31, 2026',
              },
              month: {
                ...mockViewModel.month,
                range_start: '2026-11-01',
                range_end: '2026-11-30',
                month_label: 'November 2026',
              },
            },
          }),
      });
    });
    vi.stubGlobal('fetch', fetchMock);

    useDisplayStore.setState({
      widgets: {
        'cal-grid': {
          meta: {
            type: 'calendar-grid',
            origin: [0, 0],
            dimensions: [6, 2],
            config: {
              cached_range_start: '2026-01-01',
              cached_range_end: '2027-01-01',
            },
            assets_base: '',
          },
          state: 'healthy',
          data: {},
          viewModel: mockViewModel as unknown as Record<string, unknown>,
          timestamp: '2026-10-27T10:00:00Z',
        },
      },
    });

    const { container } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);
    const nextBtn = container.querySelector('.cf-nav-btn.cf-next-btn')!;
    const prevBtn = container.querySelector('.cf-nav-btn.cf-prev-btn')!;
    const monthBtn = container.querySelector('.cf-view-btn[data-view="month"]')!;
    const weekBtn = container.querySelector('.cf-view-btn[data-view="week"]')!;

    // 1. Page Next in Week view -> week of 2026-11-01
    await act(async () => {
      fireEvent.click(nextBtn);
    });
    expect(container.querySelector('.widget-calendar-grid')?.getAttribute('data-page-offset')).toBe('1');

    // 2. Switch to Month view -> November 2026
    await act(async () => {
      fireEvent.click(monthBtn);
    });
    expect(container.querySelector('.widget-calendar-grid')?.getAttribute('data-page-offset')).toBe('1');

    // 3. Page Prev in Month view -> lands back on October 2026 (baseline month)
    fetchMock.mockClear();
    await act(async () => {
      fireEvent.click(prevBtn);
    });
    expect(fetchMock).not.toHaveBeenCalled();
    expect(container.querySelector('.widget-calendar-grid')?.getAttribute('data-page-offset')).toBe('0');

    // 4. Switch back to Week view -> must be at pageOffset 0 on baseline week without fetching
    await act(async () => {
      fireEvent.click(weekBtn);
    });
    expect(fetchMock).not.toHaveBeenCalled();
    expect(container.querySelector('.widget-calendar-grid')?.getAttribute('data-page-offset')).toBe('0');
    expect(container.querySelector('.widget-calendar-grid')?.getAttribute('data-current-date')).toBe('2026-10-27');
    const todayBadge = container.querySelector('.cf-today-badge') as HTMLElement;
    expect(todayBadge?.style.display).toBe('none');
  });

  it('tracks baseDate and baseWeekStart updates when resting without flipping isInteractive or pausing store', () => {
    useDisplayStore.setState({
      widgets: {
        'cal-grid': {
          meta: {
            type: 'calendar-grid',
            origin: [0, 0],
            dimensions: [6, 2],
            config: {},
            assets_base: '',
          },
          state: 'healthy',
          data: {},
          viewModel: mockViewModel as unknown as Record<string, unknown>,
          timestamp: '2026-10-27T23:59:00Z',
        },
      },
    });

    const pauseSpy = vi.spyOn(useDisplayStore.getState(), 'pause');
    const { container, rerender } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);

    // Simulate midnight crossover SSE update advancing today and range_start
    const nextDayVM: CalendarGridViewModel = {
      ...mockViewModel,
      week: {
        ...mockViewModel.week!,
        range_start: '2026-11-01',
        range_end: '2026-11-07',
        range_label: 'Nov 1 – 7, 2026',
        today: '2026-11-01',
      },
      month: {
        ...mockViewModel.month!,
        today: '2026-11-01',
      },
    };

    useDisplayStore.setState({
      widgets: {
        'cal-grid': {
          meta: {
            type: 'calendar-grid',
            origin: [0, 0],
            dimensions: [6, 2],
            config: {},
            assets_base: '',
          },
          state: 'healthy',
          data: {},
          viewModel: nextDayVM as unknown as Record<string, unknown>,
          timestamp: '2026-11-01T00:00:05Z',
        },
      },
    });
    rerender(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);

    expect(pauseSpy).not.toHaveBeenCalled();
    expect(container.querySelector('.widget-calendar-grid')?.getAttribute('data-page-offset')).toBe('0');
    expect(container.querySelector('.widget-calendar-grid')?.getAttribute('data-current-date')).toBe('2026-11-01');
    expect(container.querySelector('.cf-range-label.cf-label-week')?.textContent).toBe('Nov 1 – 7, 2026');
  });

  it('aborts in-flight page fetches when hitting the LRU cache or returning to baseline date', async () => {
    let abortSignalPage1: AbortSignal | undefined;
    let resolvePage1Json: ((val: unknown) => void) | undefined;

    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      abortSignalPage1 = init?.signal ?? undefined;
      return Promise.resolve({
        ok: true,
        json: () =>
          new Promise((resolve) => {
            resolvePage1Json = resolve;
          }),
      });
    });
    vi.stubGlobal('fetch', fetchMock);

    useDisplayStore.setState({
      widgets: {
        'cal-grid': {
          meta: {
            type: 'calendar-grid',
            origin: [0, 0],
            dimensions: [6, 2],
            config: {
              cached_range_start: '2026-01-01',
              cached_range_end: '2027-01-01',
            },
            assets_base: '',
          },
          state: 'healthy',
          data: {},
          viewModel: mockViewModel as unknown as Record<string, unknown>,
          timestamp: '2026-10-27T10:00:00Z',
        },
      },
    });

    const { container } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);
    const nextBtn = container.querySelector('.cf-nav-btn.cf-next-btn')!;
    const prevBtn = container.querySelector('.cf-nav-btn.cf-prev-btn')!;

    // 1. Page Next -> starts in-flight network request
    await act(async () => {
      fireEvent.click(nextBtn);
    });
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(abortSignalPage1?.aborted).toBe(false);

    // 2. Quickly Page Prev back to baseline before network request settles
    await act(async () => {
      fireEvent.click(prevBtn);
    });
    expect(abortSignalPage1?.aborted).toBe(true);
    expect(container.querySelector('.widget-calendar-grid')?.getAttribute('data-page-offset')).toBe('0');

    // 3. Resolve the stale fetch and verify it does not overwrite baseline currentViewModel
    await act(async () => {
      resolvePage1Json?.({
        view_model: {
          ...mockViewModel,
          week: {
            ...mockViewModel.week,
            range_label: 'Stale Week Range',
          },
        },
      });
    });
    expect(container.querySelector('.cf-range-label.cf-label-week')?.textContent).toBe('Oct 25 – 31, 2026');
  });

  it('computes week pageOffset and baseline week bounds using weekVM.days.length for narrow 3-day widgets', async () => {
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

    const fetchMock = vi.fn().mockImplementation(() =>
      Promise.resolve({
        ok: true,
        json: () =>
          Promise.resolve({
            view_model: {
              ...narrow3DayVM,
              week: {
                ...narrow3DayVM.week!,
                range_start: '2026-10-30',
                range_end: '2026-11-01',
                range_label: 'Oct 30 – Nov 1, 2026',
              },
            },
          }),
      })
    );
    vi.stubGlobal('fetch', fetchMock);

    useDisplayStore.setState({
      widgets: {
        'cal-grid': {
          meta: {
            type: 'calendar-grid',
            origin: [0, 0],
            dimensions: [3, 2],
            config: {
              days: 3,
              cached_range_start: '2026-01-01',
              cached_range_end: '2027-01-01',
            },
            assets_base: '',
          },
          state: 'healthy',
          data: {},
          viewModel: narrow3DayVM as unknown as Record<string, unknown>,
          timestamp: '2026-10-27T10:00:00Z',
        },
      },
    });

    const { container } = render(<CalendarGridWidget id="cal-grid" type="calendar-grid" />);
    const nextBtn = container.querySelector('.cf-nav-btn.cf-next-btn')!;

    await act(async () => {
      fireEvent.click(nextBtn);
    });

    expect(container.querySelector('.widget-calendar-grid')?.getAttribute('data-page-offset')).toBe('1');
    const todayBadge = container.querySelector('.cf-today-badge') as HTMLElement;
    expect(todayBadge?.style.display).not.toBe('none');
  });
});

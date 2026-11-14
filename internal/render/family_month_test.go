package render

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/domain"
	"github.com/azylman/mirrormere/internal/provider"
)

type monthDotExp struct {
	name     string
	color    string
	isShared bool
}

type familyMonthViewExpectations struct {
	daysCount        int
	rangeStart       string
	rangeEnd         string
	monthLabel       string
	currentMonthDays int
	dots             map[string][]monthDotExp
	eventsCount      map[string]int
	firstEventTitle  map[string]string
	emptyEventTitle  map[string]bool
	todayDate        string
	satSunWeekends   bool
	eventRedacted    map[string]string
	eventDates       map[string][]string
	eventNoDates     map[string][]string
}

func assertMonthViewHeader(t *testing.T, view *FamilyMonthViewModel, exp familyMonthViewExpectations) {
	t.Helper()
	if exp.daysCount > 0 && len(view.Days) != exp.daysCount {
		t.Errorf("len(Days) = %d, want %d", len(view.Days), exp.daysCount)
	}
	if exp.rangeStart != "" && view.RangeStart != exp.rangeStart {
		t.Errorf("RangeStart = %q, want %q", view.RangeStart, exp.rangeStart)
	}
	if exp.rangeEnd != "" && view.RangeEnd != exp.rangeEnd {
		t.Errorf("RangeEnd = %q, want %q", view.RangeEnd, exp.rangeEnd)
	}
	if exp.monthLabel != "" && view.MonthLabel != exp.monthLabel {
		t.Errorf("MonthLabel = %q, want %q", view.MonthLabel, exp.monthLabel)
	}
	if exp.currentMonthDays > 0 {
		count := 0
		for _, d := range view.Days {
			if d.IsCurrentMonth {
				count++
			}
		}
		if count != exp.currentMonthDays {
			t.Errorf("currentMonthDays = %d, want %d", count, exp.currentMonthDays)
		}
	}
}

func assertMonthViewDays(t *testing.T, dayMap map[string]FamilyMonthDay, exp familyMonthViewExpectations) {
	t.Helper()
	for _, d := range dayMap {
		if exp.todayDate != "" && d.IsToday != (d.Date == exp.todayDate) {
			t.Errorf("date %s IsToday = %v", d.Date, d.IsToday)
		}
		if exp.satSunWeekends && d.IsWeekend != (d.WeekdayAbbr == "Sat" || d.WeekdayAbbr == "Sun") {
			t.Errorf("date %s IsWeekend = %v", d.Date, d.IsWeekend)
		}
	}
	for date, wantCount := range exp.eventsCount {
		if dayMap[date].EventsCount != wantCount {
			t.Errorf("date %s EventsCount = %d, want %d", date, dayMap[date].EventsCount, wantCount)
		}
	}
}

func assertMonthViewDots(t *testing.T, dayMap map[string]FamilyMonthDay, exp familyMonthViewExpectations) {
	t.Helper()
	for date, wantDots := range exp.dots {
		d := dayMap[date]
		if len(d.Dots) != len(wantDots) {
			t.Fatalf("date %s dots = %d, want %d", date, len(d.Dots), len(wantDots))
		}
		for i, wd := range wantDots {
			if d.Dots[i].Name != wd.name || d.Dots[i].Color != wd.color || d.Dots[i].IsShared != wd.isShared {
				t.Errorf("date %s dot[%d] = %+v, want %+v", date, i, d.Dots[i], wd)
			}
		}
	}
}

func assertMonthViewTitles(t *testing.T, dayMap map[string]FamilyMonthDay, exp familyMonthViewExpectations) {
	t.Helper()
	for date, wantTitle := range exp.firstEventTitle {
		if dayMap[date].EventTitle != wantTitle {
			t.Errorf("date %s EventTitle = %q, want %q", date, dayMap[date].EventTitle, wantTitle)
		}
	}
	for date := range exp.emptyEventTitle {
		if dayMap[date].EventTitle != "" {
			t.Errorf("date %s expected empty EventTitle, got %q", date, dayMap[date].EventTitle)
		}
	}
}

func monthEventDates(view *FamilyMonthViewModel, evID string) map[string]bool {
	seen := make(map[string]bool)
	for _, d := range view.Days {
		for _, ev := range d.Events {
			if ev.ID == evID {
				seen[d.Date] = true
			}
		}
	}
	return seen
}

func assertMonthViewEvents(t *testing.T, view *FamilyMonthViewModel, exp familyMonthViewExpectations) {
	t.Helper()
	for evID, wantTitle := range exp.eventRedacted {
		found := false
		for _, d := range view.Days {
			for _, ev := range d.Events {
				if ev.ID == evID && ev.Title == wantTitle {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("redacted event %q=%q not found", evID, wantTitle)
		}
	}
	for evID, wantDates := range exp.eventDates {
		seen := monthEventDates(view, evID)
		for _, date := range wantDates {
			if !seen[date] {
				t.Errorf("event %q missing on date %s", evID, date)
			}
		}
	}
	for evID, noDates := range exp.eventNoDates {
		seen := monthEventDates(view, evID)
		for _, date := range noDates {
			if seen[date] {
				t.Errorf("event %q should NOT appear on %s", evID, date)
			}
		}
	}
}

func assertFamilyMonthViewExpectations(t *testing.T, view *FamilyMonthViewModel, exp familyMonthViewExpectations) {
	t.Helper()
	assertMonthViewHeader(t, view, exp)
	dayMap := make(map[string]FamilyMonthDay, len(view.Days))
	for _, d := range view.Days {
		dayMap[d.Date] = d
	}
	assertMonthViewDays(t, dayMap, exp)
	assertMonthViewDots(t, dayMap, exp)
	assertMonthViewTitles(t, dayMap, exp)
	assertMonthViewEvents(t, view, exp)
}

var defaultNow = time.Date(2026, 10, 10, 14, 0, 0, 0, time.UTC)

func TestBuildFamilyMonthView(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		data    any
		cfg     map[string]any
		dims    domain.Dimension
		wantErr string
		exp     familyMonthViewExpectations
	}{
		{name: "sunday start default October 2026", cfg: withFamilyCfg(map[string]any{"week_starts": "sunday"}), exp: familyMonthViewExpectations{daysCount: 42, rangeStart: "2026-09-27", rangeEnd: "2026-11-07", monthLabel: "October 2026", todayDate: "2026-10-10", satSunWeekends: true}},
		{name: "monday start October 2026", cfg: withFamilyCfg(map[string]any{"week_starts": "monday"}), exp: familyMonthViewExpectations{daysCount: 42, rangeStart: "2026-09-28", rangeEnd: "2026-11-08", monthLabel: "October 2026"}},
		{name: "february 2024 leap year 29 days", cfg: withFamilyCfg(map[string]any{"month": "2024-02"}), exp: familyMonthViewExpectations{daysCount: 42, monthLabel: "February 2024", currentMonthDays: 29}},
		{name: "year boundary transition January 2025", cfg: withFamilyCfg(map[string]any{"date": "2025-01-10", "week_starts": "sunday"}), exp: familyMonthViewExpectations{rangeStart: "2024-12-29", monthLabel: "January 2025"}},
		{
			name: "member dot lists",
			cfg: map[string]any{
				"shared_color": "#123456",
				"members": []any{
					map[string]any{"name": "Alice", "color": "#E11D48", "initial": "A", "calendars": []any{"alice-cal"}},
					map[string]any{"name": "Bob", "color": "#2563EB", "initial": "B", "calendars": []any{"bob-cal"}},
					map[string]any{"name": "Charlie", "color": "#16A34A", "initial": "C", "calendars": []any{"charlie-cal"}},
				},
				"calendars": []any{map[string]any{"name": "alice-cal"}, map[string]any{"name": "bob-cal"}, map[string]any{"name": "charlie-cal"}, map[string]any{"name": "unclaimed-cal"}},
			},
			data: snap(
				mkCalEv("e1", "alice-cal", "Alice Event 1", "2026-10-10T09:00:00Z", "2026-10-10T10:00:00Z", false),
				mkCalEv("e2", "alice-cal", "Alice Event 2", "2026-10-10T14:00:00Z", "2026-10-10T15:00:00Z", false),
				mkCalEv("e3", "charlie-cal", "Charlie Soccer", "2026-10-10T11:00:00Z", "2026-10-10T12:00:00Z", false),
				mkCalEv("e4", "alice-cal", "Family Brunch", "2026-10-11T10:00:00Z", "2026-10-11T12:00:00Z", false),
				mkCalEv("e4", "bob-cal", "Family Brunch", "2026-10-11T10:00:00Z", "2026-10-11T12:00:00Z", false),
				mkCalEv("e5", "unclaimed-cal", "Public Holiday", "2026-10-12", "2026-10-12", true),
			),
			exp: familyMonthViewExpectations{
				eventsCount: map[string]int{"2026-10-10": 3, "2026-10-13": 0},
				dots: map[string][]monthDotExp{
					"2026-10-10": {{"Alice", "#E11D48", false}, {"Charlie", "#16A34A", false}},
					"2026-10-11": {{"Alice", "#E11D48", false}, {"Bob", "#2563EB", false}},
					"2026-10-12": {{"Shared", "#123456", true}},
					"2026-10-13": {},
				},
			},
		},
		{
			name: "density adaptation 6x2, redaction, multi-day and outside window",
			dims: domain.NewDimension(6, 2),
			data: snap(
				mkCalEv("e_timed", "alex-personal", "Later Timed Event", "2026-10-15T15:00:00Z", "2026-10-15T16:00:00Z", false),
				mkCalEv("e_early", "alex-personal", "Earlier Timed Event", "2026-10-15T09:00:00Z", "2026-10-15T10:00:00Z", false),
				mkCalEv("e_allday", "alex-personal", "All Day Conference", "2026-10-15", "2026-10-15", true),
				mkCalEv("e_priv", "secret-work", "Confidential Review", "2026-10-16T10:00:00Z", "2026-10-16T11:00:00Z", false),
				mkCalEv("e_multi_month", "alex-personal", "Long Vacation", "2026-09-25", "2026-10-02", true),
				mkCalEv("e_outside", "alex-personal", "Outside Event", "2026-11-20T10:00:00Z", "2026-11-20T11:00:00Z", false),
			),
			exp: familyMonthViewExpectations{
				firstEventTitle: map[string]string{"2026-10-15": "All Day Conference"},
				eventsCount:     map[string]int{"2026-10-15": 3},
				eventRedacted:   map[string]string{"e_priv": "Busy"},
				eventDates:      map[string][]string{"e_multi_month": {"2026-09-27", "2026-09-28", "2026-09-29", "2026-09-30", "2026-10-01", "2026-10-02"}},
				eventNoDates:    map[string][]string{"e_outside": {"2026-11-20"}},
			},
		},
		{
			name: "density adaptation 4x2 resolves event title",
			dims: domain.NewDimension(4, 2),
			data: snap(mkCalEv("e_timed", "alex-personal", "Later Timed Event", "2026-10-15T15:00:00Z", "2026-10-15T16:00:00Z", false), mkCalEv("e_allday", "alex-personal", "All Day Conference", "2026-10-15", "2026-10-15", true)),
			exp:  familyMonthViewExpectations{firstEventTitle: map[string]string{"2026-10-15": "All Day Conference"}},
		},
		{
			name: "density adaptation narrow 2x2 omits event titles",
			dims: domain.NewDimension(2, 2),
			data: snap(mkCalEv("e_allday", "alex-personal", "All Day Conference", "2026-10-15", "2026-10-15", true)),
			exp:  familyMonthViewExpectations{emptyEventTitle: map[string]bool{"2026-10-15": true}},
		},
		{name: "invalid member config returns error", cfg: map[string]any{"members": []any{"not-a-map"}}, wantErr: "is not a mapping"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dims := domain.NewDimension(6, 2)
			if tc.dims.Cols > 0 || tc.dims.Rows > 0 {
				dims = tc.dims
			}
			cfg := tc.cfg
			if cfg == nil && tc.wantErr == "" {
				cfg = baseFamilyConfig()
			}
			view, err := BuildFamilyMonthView(tc.data, cfg, dims, defaultNow)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertFamilyMonthViewExpectations(t, view, tc.exp)
		})
	}
}

func TestBuildFamilyCombinedData(t *testing.T) {
	t.Parallel()
	weekView := &FamilyViewModel{
		Today: "2026-10-10", RangeStart: "2026-10-05", RangeEnd: "2026-10-11",
		Days:         []FamilyDay{{Date: "2026-10-10", DayOfMonth: 10}},
		AllDayEvents: []FamilySpanEvent{{ID: "e_span", Title: "Conference"}},
	}
	monthView := &FamilyMonthViewModel{
		RangeStart: "2026-09-28", RangeEnd: "2026-11-08",
		Days: []FamilyMonthDay{{Date: "2026-10-10", DayOfMonth: 10}},
	}

	tests := []struct {
		name      string
		weekView  *FamilyViewModel
		monthView *FamilyMonthViewModel
		cfg       map[string]any
		wantKeys  map[string]any
	}{
		{name: "nil inputs safety"},
		{
			name: "full aggregation", weekView: weekView, monthView: monthView, cfg: map[string]any{"default_view": "week"},
			wantKeys: map[string]any{"today": "2026-10-10", "range_start": "2026-10-05", "range_end": "2026-10-11", "cached_range_start": "2026-07-12", "cached_range_end": "2027-04-08", "month_range_start": "2026-09-28", "month_range_end": "2026-11-08"},
		},
		{name: "explicit cfg bounds take precedence", weekView: weekView, cfg: map[string]any{"cached_range_start": "2026-09-01", "cached_range_end": "2026-12-31"}, wantKeys: map[string]any{"cached_range_start": "2026-09-01", "cached_range_end": "2026-12-31"}},
		{name: "monthView bounds fallback when weekView nil", monthView: monthView, wantKeys: map[string]any{"cached_range_start": "2026-09-28", "cached_range_end": "2026-11-08"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res := BuildFamilyCombinedData(tc.weekView, tc.monthView, tc.cfg)
			if res == nil {
				t.Fatalf("expected non-nil map")
			}
			for k, wantVal := range tc.wantKeys {
				if res[k] != wantVal {
					t.Errorf("res[%q] = %v, want %v", k, res[k], wantVal)
				}
			}
		})
	}
}

type calendarWidgetTestCase struct {
	name                 string
	widgetDir            string
	dims                 domain.Dimension
	data                 any
	cfg                  map[string]any
	wantContains         []string
	wantNotContains      []string
	wantOrdered          []string
	wantDayCardsCount    int
	dayCardMustContain   []string
	thursCardMustContain []string
}

func assertCalendarWidgetStrings(t *testing.T, html string, tc calendarWidgetTestCase) {
	t.Helper()
	for _, s := range tc.wantContains {
		if !strings.Contains(html, s) {
			t.Errorf("expected HTML to contain %q", s)
		}
	}
	for _, s := range tc.wantNotContains {
		if strings.Contains(html, s) {
			t.Errorf("expected HTML NOT to contain %q", s)
		}
	}
	lastIdx := -1
	for _, s := range tc.wantOrdered {
		idx := strings.Index(html, s)
		if idx == -1 || idx < lastIdx {
			t.Errorf("order check failed for %q (idx=%d, lastIdx=%d)", s, idx, lastIdx)
		}
		lastIdx = idx
	}
}

func assertCalendarWidgetCards(t *testing.T, html string, tc calendarWidgetTestCase) {
	t.Helper()
	if tc.wantDayCardsCount > 0 {
		if cnt := strings.Count(html, `class="calendar-day-card"`); cnt != tc.wantDayCardsCount {
			t.Errorf("calendar-day-card count = %d, want %d", cnt, tc.wantDayCardsCount)
		}
	}
	if len(tc.dayCardMustContain) > 0 {
		if cardStart := strings.Index(html, `<div class="calendar-day-card">`); cardStart != -1 {
			for _, s := range tc.dayCardMustContain {
				if !strings.Contains(html[cardStart:], s) {
					t.Errorf("expected day-card to contain %q", s)
				}
			}
		}
	}
	if len(tc.thursCardMustContain) > 0 {
		if cards := strings.Split(html, `<div class="calendar-day-card">`); len(cards) >= 4 {
			for _, s := range tc.thursCardMustContain {
				if !strings.Contains(cards[3], s) {
					t.Errorf("expected Thursday card to contain %q", s)
				}
			}
		}
	}
}

func calendarWidgetTestCases(laLoc *time.Location) []calendarWidgetTestCase {
	tzEvents := []provider.CalendarEvent{
		{ID: "e-mon", Title: "Monday Evening Dinner", CalendarName: "Family", Color: "#3b82f6", Start: "2026-10-13T02:00:00Z", End: "2026-10-13T03:00:00Z"},
		{ID: "e-party", Title: "Weekend Bachelorette Party", CalendarName: "Family", Color: "#a855f7", Start: "2026-10-15", End: "2026-10-15", AllDay: true},
		{ID: "e-wed", Title: "Wednesday Game Night", CalendarName: "Family", Color: "#3b82f6", Start: "2026-10-15T02:00:00Z", End: "2026-10-15T04:00:00Z"},
		{ID: "e-brunch", Title: "Brunch with Bridesmaids", CalendarName: "Family", Color: "#a855f7", Start: "2026-10-15T17:00:00Z", End: "2026-10-15T18:30:00Z"},
	}
	provider.SortCalendarEvents(tzEvents, laLoc)

	return []calendarWidgetTestCase{
		{
			name:            "calendar-family day view fallback to week on 6x2",
			data:            snap(mkCalEv("e1", "alex-personal", "Design Review", "2026-09-27T10:00:00Z", "2026-09-27T11:00:00Z", false)),
			cfg:             withFamilyCfg(map[string]any{"default_view": "day", "date": "2026-09-27"}),
			wantContains:    []string{"cf-view-week", "--mm-member-0: #E07A5F", ">Alex</span>", ">Kid</span>", "cf-week-grid", "Design Review"},
			wantNotContains: []string{"cf-view-day", `data-view="day"`},
		},
		{
			name:         "calendar-family day view empty events",
			data:         provider.CalendarSnapshot{},
			cfg:          withFamilyCfg(map[string]any{"default_view": "day", "date": "2026-09-27"}),
			wantContains: []string{"cf-view-week", "cf-week-grid"},
		},
		{
			name:         "calendar-family day view no members empty",
			data:         nil,
			cfg:          map[string]any{"default_view": "day"},
			wantContains: []string{"No calendar data yet"},
		},
		{
			name: "calendar-family week view template render and switcher",
			data: snap(
				mkCalEv("e_week", "alex-personal", "Team Standup", "2026-09-28T09:00:00Z", "2026-09-28T09:30:00Z", false),
				mkCalEv("e_single", "alex-personal", "Town Hall", "2026-09-30", "2026-09-30", true),
				mkCalEv("e_multi", "alex-personal", "Camping Trip", "2026-09-28", "2026-09-30", true),
				mkCalEv("e_soc", "alex-personal", "Soccer Practice", "2026-09-27T16:00:00Z", "2026-09-27T17:00:00Z", false),
			),
			cfg: withFamilyCfg(map[string]any{"default_view": "week", "date": "2026-09-27", "hours": []any{"07:00", "21:00"}}),
			wantContains: []string{
				"cf-view-switcher", "cf-view-week", "cf-week-grid", "Team Standup", "cf-today", "border: 1px solid var(--mm-cal-today-border",
				`data-view="week" role="tab" aria-selected="true">Week</button>`,
				`data-view="month" role="tab" aria-selected="false">Month</button>`,
				"calendar-family-data", "MirrormereCalendarFamily.mount",
				"cf-week-allday-grid", "grid-column: 2 / span 3", "Camping Trip", `data-event-id="e_multi"`,
				"cf-week-body", "cf-timeline-axis", "cf-timeline-markers", "cf-hour-marker", "cf-day-canvas", "Soccer Practice",
				"cf-collapsed-gap", "cf-collapsed-gap-label", `data-gap-start="10:00"`, `data-gap-end="16:00"`, "pointer-events: none",
				"height: 44px",
			},
			wantNotContains: []string{"var(--mm-coral, #ff6f59)", `data-view="day"`, "border: 2px solid var(--mm-cal-today-border"},
		},
		{
			name:      "calendar-grid custom title template render",
			widgetDir: "calendar-grid",
			data:      snap(mkCalEv("e1", "alex-personal", "Dentist", "2026-09-27T10:00:00Z", "2026-09-27T11:00:00Z", false)),
			cfg:       withFamilyCfg(map[string]any{"title": "Custom Family Schedule", "date": "2026-09-27"}),
			wantContains: []string{
				"Custom Family Schedule",
				"cf-timeline-spacer",
				"height: 44px",
			},
		},
		{
			name:      "calendar-grid renders event header and title with flex-shrink 0 and cf-event-compact CSS",
			widgetDir: "calendar-grid",
			data: snap(
				mkCalEv("e_short", "alex-personal", "Quick 15m Check-in", "2026-09-27T09:15:00Z", "2026-09-27T09:30:00Z", false),
				mkCalEv("e_long", "alex-personal", "Full Hour Review", "2026-09-27T11:00:00Z", "2026-09-27T12:00:00Z", false),
			),
			cfg: withFamilyCfg(map[string]any{"date": "2026-09-27", "hours": []any{"07:00", "21:00"}}),
			wantContains: []string{
				"Quick 15m Check-in",
				".cf-day-event.cf-event-compact",
				`data-event-start="09:15"`,
				`<span class="cf-event-time" style="font-family: var(--mm-font-mono, monospace);">09:15</span>`,
				"Full Hour Review",
				`<span class="cf-event-time" style="font-family: var(--mm-font-mono, monospace);">11:00</span>`,
			},
		},
		{
			name:      "calendar-grid week view early and late edge bands in week-level grid",
			widgetDir: "calendar-grid",
			data: snap(
				mkCalEv("e_early", "alex-personal", "Early Morning Jog", "2026-09-28T05:30:00Z", "2026-09-28T06:30:00Z", false),
				mkCalEv("e_mid", "alex-personal", "Midday Sync", "2026-09-28T12:00:00Z", "2026-09-28T13:00:00Z", false),
				mkCalEv("e_late", "alex-personal", "Late Flight Arrival", "2026-09-28T22:30:00Z", "2026-09-28T23:30:00Z", false),
			),
			cfg: withFamilyCfg(map[string]any{"default_view": "week", "date": "2026-09-27", "hours": []any{"07:00", "21:00"}}),
			wantContains: []string{
				"cf-week-early-grid",
				"cf-early-band",
				"Early Morning Jog",
				"cf-week-late-grid",
				"cf-late-band",
				"Late Flight Arrival",
				"cf-week-body",
				"cf-day-canvas",
				"Midday Sync",
			},
		},
		{
			name: "calendar-family month view template render 6x2 and switcher",
			data: snap(
				mkCalEv("e1", "alex-personal", "Parent Teacher Night", "2026-09-27T18:00:00Z", "2026-09-27T19:00:00Z", false),
				mkCalEv("e2", "holidays", "Public Holiday", "2026-09-27", "2026-09-27", true),
			),
			cfg: withFamilyCfg(map[string]any{"default_view": "month", "date": "2026-09-27"}),
			wantContains: []string{
				"cf-view-switcher", "cf-view-month", "September 2026", "cf-month-grid", "cf-month-day", "cf-month-dot",
				"var(--mm-member-0", "var(--mm-cf-shared", "cf-month-event-title", "Parent Teacher Night",
				`data-view="month" role="tab" aria-selected="true">Month</button>`,
				`data-view="week" role="tab" aria-selected="false">Week</button>`,
				"outline: 1px solid var(--mm-cal-today-border",
			},
			wantNotContains: []string{`data-view="day"`, "outline: 2px solid var(--mm-cal-today-border"},
		},
		{
			name:         "calendar-family month view template render 4x2",
			dims:         domain.NewDimension(4, 2),
			data:         snap(mkCalEv("e_dentist", "alex-personal", "Dentist Visit", "2026-10-10T14:00:00Z", "2026-10-10T15:00:00Z", false)),
			cfg:          withFamilyCfg(map[string]any{"default_view": "month", "date": "2026-10-10"}),
			wantContains: []string{"cf-view-month", "cf-month-dot", "cf-month-event-title", "Dentist Visit", `data-event-id="e_dentist"`},
		},
		{
			name:            "calendar-family month view template render narrow dots only",
			dims:            domain.NewDimension(2, 2),
			data:            snap(mkCalEv("e1", "alex-personal", "Parent Teacher Night", "2026-09-27T18:00:00Z", "2026-09-27T19:00:00Z", false)),
			cfg:             withFamilyCfg(map[string]any{"default_view": "month", "date": "2026-09-27"}),
			wantContains:    []string{"cf-month-dot"},
			wantNotContains: []string{"cf-month-event-title"},
		},
		{
			name:         "calendar-family month view template render empty",
			data:         provider.CalendarSnapshot{},
			cfg:          withFamilyCfg(map[string]any{"default_view": "month", "date": "2026-09-27"}),
			wantContains: []string{"cf-view-month", "September 2026", "cf-month-day"},
		},
		{
			name:      "calendar-feed alignment and hierarchy",
			widgetDir: "calendar-feed",
			dims:      domain.NewDimension(4, 3),
			data: provider.CalendarSnapshot{
				LastSync: "2026-10-10T12:00:00Z",
				Events: []provider.CalendarEvent{
					{ID: "e1", Title: "Doctor Appointment", CalendarName: "Family", Color: "#ff6f59", Start: "2026-10-10T14:00:00Z", End: "2026-10-10T15:00:00Z", Location: "Medical Center Bldg B"},
					{ID: "e2", Title: "Team Sync", CalendarName: "Work", Color: "#9d8df1", Start: "2026-10-10T16:00:00Z", End: "2026-10-10T17:00:00Z"},
					{ID: "e3", Title: "No-Calendar Event", Start: "2026-10-10T18:00:00Z", End: "2026-10-10T19:00:00Z", Location: "Room 101"},
				},
			},
			cfg:                map[string]any{"title": "Upcoming", "__now": time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)},
			wantContains:       []string{`<div class="event-details-col">`, "Medical Center Bldg B", `class="event-calendar-pill"`, `class="event-accent-dot"`, "Doctor Appointment", "Team Sync", "No-Calendar Event", "Room 101"},
			wantOrdered:        []string{`class="event-calendar-pill"`, `class="event-accent-dot"`},
			wantDayCardsCount:  1,
			dayCardMustContain: []string{"Doctor Appointment", "Team Sync", "No-Calendar Event"},
		},
		{
			name:                 "calendar-feed all-day sorting across timezones",
			widgetDir:            "calendar-feed",
			dims:                 domain.NewDimension(4, 3),
			data:                 provider.CalendarSnapshot{LastSync: "2026-10-10T12:00:00Z", Events: tzEvents},
			cfg:                  map[string]any{"title": "Upcoming", "__now": time.Date(2026, 10, 10, 12, 0, 0, 0, laLoc)},
			wantOrdered:          []string{"Monday Evening Dinner", "Wednesday Game Night", "Weekend Bachelorette Party", "Brunch with Bridesmaids"},
			wantContains:         []string{"Thursday • Oct 15", "event-time-allday"},
			wantDayCardsCount:    3,
			thursCardMustContain: []string{"Weekend Bachelorette Party", "Brunch with Bridesmaids"},
		},
	}
}

func TestCalendarWidgetTemplates(t *testing.T) {
	t.Parallel()
	laLoc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatalf("failed to load America/Los_Angeles: %v", err)
	}

	tests := calendarWidgetTestCases(laLoc)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wDir := tc.widgetDir
			if wDir == "" {
				wDir = "calendar-family"
			}
			dims := tc.dims
			if dims.Cols == 0 && dims.Rows == 0 {
				dims = domain.NewDimension(6, 2)
			}
			html := renderWidgetTemplate(t, wDir, Context{ID: "test-" + wDir, Type: wDir, Dimensions: dims, Data: tc.data, Config: tc.cfg})
			assertCalendarWidgetStrings(t, html, tc)
			assertCalendarWidgetCards(t, html, tc)
		})
	}
}

func assertMealieViewDayMeals(t *testing.T, view *FamilyViewModel) {
	t.Helper()
	if view.HoursEnd != "21:00" {
		t.Errorf("expected HoursEnd to remain 21:00, got %q", view.HoursEnd)
	}
	if len(view.CollapsedGaps) != 0 {
		t.Errorf("expected filtered duplicate dinner at 18:00 on Mealie day to not create phantom collapsed midday gap, got %+v", view.CollapsedGaps)
	}

	day0 := view.Days[0]
	if len(day0.Meals) != 2 || day0.Meals[0].ID != "mealie_2026-09-27" {
		t.Fatalf("expected Mealie events in day0.Meals, got %+v", day0.Meals)
	}
	if day0.Meals[0].ImageURL != "/api/media/recipes/recipe-123/images/min-original.webp" {
		t.Errorf("expected day0.Meals[0].ImageURL preserved, got %q", day0.Meals[0].ImageURL)
	}
	if len(day0.AllDay) != 0 {
		t.Errorf("expected Mealie event excluded from day0.AllDay, got %+v", day0.AllDay)
	}
	for _, spanEv := range view.AllDayEvents {
		if spanEv.ID == "mealie_2026-09-27" {
			t.Errorf("expected Mealie event excluded from top AllDayEvents spans")
		}
	}
	for _, te := range day0.Timed {
		if te.ID == "dup_dinner_1" || te.ID == "mealie_timed_2026-09-27" {
			t.Errorf("expected duplicate dinner and timed Mealie event filtered on Mealie day, got %+v", te)
		}
	}
	for _, de := range day0.Events {
		if de.ID == "dup_dinner_1" || de.ID == "mealie_timed_2026-09-27" {
			t.Errorf("expected duplicate dinner and timed Mealie event filtered from day canvas Events, got %+v", de)
		}
	}
}

func assertMealieViewOwnersAndRedaction(t *testing.T, view *FamilyViewModel) {
	t.Helper()
	dropoffEv, _ := findViewEvent(view, "kid_dropoff")
	if dropoffEv == nil || len(dropoffEv.Owners) != 1 || dropoffEv.Owners[0] != "Kid" || dropoffEv.Color != "#EC4899" {
		t.Errorf("expected 'Kid dropoff' on shared Family calendar to match Kid, got %+v", dropoffEv)
	}

	bdayEv, _ := findViewEvent(view, "kid_bday")
	if bdayEv == nil || len(bdayEv.Owners) != 1 || bdayEv.Owners[0] != "Kid" || bdayEv.Color != "#EC4899" {
		t.Errorf("expected 'Kid's birthday' on shared Family calendar to match Kid, got %+v", bdayEv)
	}

	privEv, _ := findViewEvent(view, "priv_shared_ev")
	if privEv == nil || privEv.Title != "Busy" || len(privEv.Owners) != 2 {
		t.Errorf("expected private shared calendar event to be redacted and retain both owners without narrowing by raw title, got %+v", privEv)
	}
}

func assertMealieViewDeduplication(t *testing.T, view *FamilyViewModel) {
	t.Helper()
	assertMealieViewDayMeals(t, view)
	assertMealieViewOwnersAndRedaction(t, view)
}

func assertMealieMonthViewAndHTML(t *testing.T, events []provider.CalendarEvent, cfg map[string]any) {
	t.Helper()
	viewWithNonDup, err := BuildFamilyView(snap(events...), cfg, domain.NewDimension(6, 2), testNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	day1 := viewWithNonDup.Days[1]
	if len(day1.Timed) != 1 || day1.Timed[0].ID != "non_dup_dinner" {
		t.Errorf("expected timed Dinner event kept when no Mealie event on that day, got %+v", day1.Timed)
	}

	monthView, err := BuildFamilyMonthView(snap(events...), cfg, domain.NewDimension(6, 2), testNow)
	if err != nil {
		t.Fatalf("unexpected error in BuildFamilyMonthView: %v", err)
	}
	for _, md := range monthView.Days {
		if md.Date == "2026-09-27" {
			for _, ev := range md.Events {
				if ev.ID == "dup_dinner_1" {
					t.Errorf("expected duplicate dinner filtered from month view on 2026-09-27")
				}
			}
		}
	}

	html := renderWidgetTemplate(t, "calendar-grid", Context{
		ID:         "test-mealie-grid",
		Type:       "calendar-grid",
		Dimensions: domain.NewDimension(6, 2),
		Data:       snap(events...),
		Config:     cfg,
	})
	if !strings.Contains(html, "cf-week-meal-row") || !strings.Contains(html, "cf-day-meal-bar") || !strings.Contains(html, "cf-mealie-bar") {
		t.Errorf("expected widget HTML to render cf-week-meal-row, cf-day-meal-bar, and cf-mealie-bar")
	}
}

func assertMealiePredicatesAndMemberMatching(t *testing.T) {
	t.Helper()
	if isMealieFamilyEvent(nil, provider.CalendarEvent{}) {
		t.Errorf("expected nil event to not be Mealie event")
	}
	if !isMealieFamilyEvent(&FamilyEvent{Title: "🍽️ Pasta"}, provider.CalendarEvent{}) {
		t.Errorf("expected plate emoji prefix to match Mealie event")
	}
	if !isMealieFamilyEvent(&FamilyEvent{ImageURL: "/api/media/recipes/abc/images/min-original.webp"}, provider.CalendarEvent{}) {
		t.Errorf("expected /api/media/recipes/ ImageURL to match Mealie event")
	}
	if !isMealieFamilyEvent(&FamilyEvent{Location: "data:image/webp;base64,AAAA"}, provider.CalendarEvent{}) {
		t.Errorf("expected data:image/ Location to match Mealie event")
	}
	if feImg := newFamilyEvent(provider.CalendarEvent{ID: "m1", Title: "🍽️ Dish", ImageURL: "/api/media/recipes/abc/images/min-original.webp", AllDay: true}, time.UTC); feImg.Location != "/api/media/recipes/abc/images/min-original.webp" || feImg.ImageURL != "/api/media/recipes/abc/images/min-original.webp" {
		t.Errorf("expected newFamilyEvent to backfill empty Location from ImageURL, got %+v", feImg)
	}
	if !isMealieFamilyEvent(&FamilyEvent{ID: "mealie_2026-10-01"}, provider.CalendarEvent{}) {
		t.Errorf("expected mealie_ ID prefix to match Mealie event")
	}
	if !isMealieFamilyEvent(&FamilyEvent{ID: "ev1"}, provider.CalendarEvent{CalendarName: "Mealie"}) {
		t.Errorf("expected Mealie calendar name to match Mealie event")
	}
	if summaryMentionsMember("Kid dropoff", "") {
		t.Errorf("expected empty member name to return false")
	}
	if !summaryMentionsMember("Kid", "Kid") {
		t.Errorf("expected exact summary match to return true")
	}
	if !summaryMentionsMember("Dinner at Kid's", "Kid") {
		t.Errorf("expected end-of-string possessive match to return true")
	}
	if summaryMentionsMember("Kidbell recital", "Kid") {
		t.Errorf("expected partial word prefix without boundary to return false")
	}
	if !summaryMentionsMember("Kidbell and Kid recital", "Kid") {
		t.Errorf("expected second occurrence after non-boundary prefix to return true")
	}
}

func TestMealieBottomBarDuplicateDinnerAndMemberSummaryMatching(t *testing.T) {
	t.Parallel()
	cfg := map[string]any{
		"shared_color": "#3D405B",
		"week_starts":  "sunday",
		"date":         "2026-09-27",
		"members": []any{
			map[string]any{"name": "Alex", "color": "#2563EB", "pattern": "solid", "calendars": []any{"Family", "PrivateShared"}},
			map[string]any{"name": "Kid", "color": "#EC4899", "pattern": "dots", "calendars": []any{"kid-cal", "PrivateShared"}},
		},
		"calendars": []any{
			map[string]any{"name": "Family"},
			map[string]any{"name": "kid-cal"},
			map[string]any{"name": "Mealie"},
			map[string]any{"name": "PrivateShared", "private": true},
		},
	}

	events := []provider.CalendarEvent{
		{
			ID:           "mealie_2026-09-27",
			CalendarName: "Mealie",
			Title:        "🍽️ Honey Garlic Salmon",
			Location:     "/hearth-api/mealie/image/recipe-123",
			ImageURL:     "/api/media/recipes/recipe-123/images/min-original.webp",
			Description:  "Ingredients:\n- Salmon\n- Honey",
			Start:        "2026-09-27",
			End:          "2026-09-27",
			AllDay:       true,
		},
		{
			ID:           "mealie_timed_2026-09-27",
			CalendarName: "Mealie",
			Title:        "🍽️ Timed Mealie Prep",
			Start:        "2026-09-27T17:00:00Z",
			End:          "2026-09-27T17:30:00Z",
			AllDay:       false,
		},
		{
			ID:           "dup_dinner_1",
			CalendarName: "Family",
			Title:        "Dinner: Honey Garlic Salmon",
			Start:        "2026-09-27T18:00:00Z",
			End:          "2026-09-27T19:00:00Z",
			AllDay:       false,
		},
		{
			ID:           "kid_dropoff",
			CalendarName: "Family",
			Title:        "Kid dropoff",
			Start:        "2026-09-27T08:30:00Z",
			End:          "2026-09-27T09:00:00Z",
			AllDay:       false,
		},
		{
			ID:           "kid_bday",
			CalendarName: "Family",
			Title:        "Kid's birthday",
			Start:        "2026-09-29",
			End:          "2026-09-29",
			AllDay:       true,
		},
		{
			ID:           "priv_shared_ev",
			CalendarName: "PrivateShared",
			Title:        "Kid secret appointment",
			Start:        "2026-09-27T09:00:00Z",
			End:          "2026-09-27T09:30:00Z",
			AllDay:       false,
		},
	}

	view, err := BuildFamilyView(snap(events...), cfg, domain.NewDimension(6, 2), testNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertMealieViewDeduplication(t, view)

	withNonDupDinner := append(slices.Clone(events), provider.CalendarEvent{
		ID:           "non_dup_dinner",
		CalendarName: "Family",
		Title:        "Dinner: Tacos (No Mealie Today)",
		Start:        "2026-09-28T18:00:00Z",
		End:          "2026-09-28T19:00:00Z",
		AllDay:       false,
	})
	assertMealieMonthViewAndHTML(t, withNonDupDinner, cfg)
	assertMealiePredicatesAndMemberMatching(t)
}

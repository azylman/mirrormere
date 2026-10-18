package render

import (
	"bytes"
	"html/template"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/domain"
	"github.com/azylman/mirrormere/internal/provider"
)

func baseFamilyConfig() map[string]any {
	return map[string]any{
		"shared_color": "#3D405B",
		"week_starts":  "sunday",
		"members": []any{
			map[string]any{"name": "Alex", "color": "#E07A5F", "pattern": "solid", "calendars": []any{"alex-personal"}},
			map[string]any{"name": "Kid", "color": "#81B29A", "pattern": "hatch", "calendars": []any{"school", "soccer"}},
		},
		"calendars": []any{
			map[string]any{"name": "alex-personal"},
			map[string]any{"name": "school"},
			map[string]any{"name": "soccer"},
			map[string]any{"name": "holidays"},
			map[string]any{"name": "secret-work", "private": true},
		},
	}
}

func fiveMemberConfig() map[string]any {
	return map[string]any{
		"members": []any{
			map[string]any{"name": "Alex", "color": "#e11d48", "pattern": "solid", "calendars": []any{"alex-cal"}},
			map[string]any{"name": "Kid", "color": "#81B29A", "pattern": "hatch", "calendars": []any{"kid-cal"}},
			map[string]any{"name": "Partner", "color": "#3B82F6", "pattern": "dots", "calendars": []any{"partner-cal"}},
			map[string]any{"name": "Grandma", "color": "#F59E0B", "pattern": "outline", "calendars": []any{"grandma-cal"}},
			map[string]any{"name": "Grandpa", "color": "#10B981", "pattern": "solid", "calendars": []any{"grandpa-cal"}},
		},
		"calendars": []any{
			map[string]any{"name": "alex-cal"},
			map[string]any{"name": "kid-cal"},
			map[string]any{"name": "partner-cal"},
			map[string]any{"name": "grandma-cal"},
			map[string]any{"name": "grandpa-cal"},
			map[string]any{"name": "unclaimed-cal"},
		},
		"hours": []any{"08:00", "20:00"},
		"date":  "2026-09-27",
	}
}

var testNow = mustTimeStatic("2006-01-02", "2026-09-27")

func mustTimeStatic(layout, val string) time.Time {
	tm, err := time.Parse(layout, val)
	if err != nil {
		panic(err)
	}
	return tm
}

func mkCalEv(id, cal, title, start, end string, allDay bool) provider.CalendarEvent {
	return provider.CalendarEvent{ID: id, CalendarName: cal, Title: title, Start: start, End: end, AllDay: allDay}
}

func snap(evs ...provider.CalendarEvent) provider.CalendarSnapshot {
	return provider.CalendarSnapshot{Events: evs}
}

func withFamilyCfg(overrides map[string]any) map[string]any {
	cfg := baseFamilyConfig()
	for k, v := range overrides {
		cfg[k] = v
	}
	return cfg
}

func findViewEvent(view *FamilyViewModel, evID string) (*FamilyEvent, string) {
	for _, d := range view.Days {
		for i := range d.Timed {
			if d.Timed[i].ID == evID {
				return &d.Timed[i], d.Date
			}
		}
		for i := range d.AllDay {
			if d.AllDay[i].ID == evID {
				return &d.AllDay[i], d.Date
			}
		}
	}
	return nil, ""
}

func renderWidgetTemplate(t *testing.T, widgetDir string, ctx Context) string {
	t.Helper()
	now := testNow
	if n, ok := ctx.Config["__now"].(time.Time); ok {
		now = n
	}
	tmplPath := filepath.Join("..", "..", "widgets", widgetDir, "views", "widget.html")
	if _, err := os.Stat(tmplPath); os.IsNotExist(err) && widgetDir == "calendar-family" {
		tmplPath = filepath.Join("..", "..", "widgets", "calendar-grid", "views", "widget.html")
	}
	tmpl, err := template.New("widget.html").Funcs(StandardFuncMap(func() time.Time { return now })).ParseFiles(tmplPath)
	if err != nil {
		t.Fatalf("failed to parse %s widget template: %v", widgetDir, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, ctx); err != nil {
		t.Fatalf("failed to execute %s widget template: %v", widgetDir, err)
	}
	return buf.String()
}

type buildFamilyViewExpectations struct {
	daysCount        int
	rangeStart       string
	rangeEnd         string
	rangeLabel       string
	today            string
	memberCount      int
	memberInitial    string
	memberPattern    string
	dayTimedCount    map[int]int
	dayAllDayCount   map[int]int
	dayOverflow      map[int]int
	dayIsToday       map[int]bool
	dayIsWeekend     map[int]bool
	eventOwners      map[string][]string
	eventColors      map[string]string
	eventTextColors  map[string]string
	eventPatterns    map[string]string
	eventRedacted    map[string]string
	eventStart       map[string]string
	eventDay         map[string]string
	eventDates       map[string][]string
	eventNoDates     map[string][]string
	spanEvents       []FamilySpanEvent
	dayEventPos      map[string]FamilyDayEvent
	earlyEventsCount map[int]int
	lateEventsCount  map[int]int
}

func TestBuildFamilyView(t *testing.T) {
	t.Parallel()
	pdtLoc := time.FixedZone("PDT", -7*3600)

	tests := []struct {
		name    string
		data    any
		cfg     map[string]any
		dims    domain.Dimension
		now     time.Time
		wantErr string
		exp     buildFamilyViewExpectations
	}{
		{
			name: "member resolution, unclaimed color, contrast text color, today/weekend flags, and range label",
			data: snap(
				mkCalEv("evt_1", "alex-personal", "Dentist", "2026-09-27T10:00:00Z", "2026-09-27T11:00:00Z", false),
				mkCalEv("evt_2", "holidays", "Rosh Hashanah", "2026-09-27", "2026-09-27", true),
				mkCalEv("evt_light", "school", "Light bg", "2026-09-27T15:00:00Z", "2026-09-27T16:00:00Z", false),
			),
			exp: buildFamilyViewExpectations{
				daysCount:       7,
				rangeLabel:      "Sep 27 – Oct 3",
				dayTimedCount:   map[int]int{0: 2},
				dayAllDayCount:  map[int]int{0: 1},
				dayIsToday:      map[int]bool{0: true, 3: false},
				dayIsWeekend:    map[int]bool{0: true, 3: false, 6: true},
				eventOwners:     map[string][]string{"evt_1": {"Alex"}},
				eventColors:     map[string]string{"evt_1": "#E07A5F", "evt_2": "#3D405B", "evt_light": "#81B29A"},
				eventTextColors: map[string]string{"evt_1": "#ffffff", "evt_2": "#ffffff", "evt_light": "#000000"},
				eventPatterns:   map[string]string{"evt_1": "solid", "evt_2": ""},
			},
		},
		{
			name: "shared event merge by ID",
			data: snap(
				mkCalEv("evt_shared_1", "alex-personal", "Shared Soccer", "2026-09-27T14:00:00Z", "2026-09-27T15:00:00Z", false),
				mkCalEv("evt_shared_1", "soccer", "Shared Soccer", "2026-09-27T14:00:00Z", "2026-09-27T15:00:00Z", false),
			),
			exp: buildFamilyViewExpectations{daysCount: 7, dayTimedCount: map[int]int{0: 1}, eventOwners: map[string][]string{"evt_shared_1": {"Alex", "Kid"}}},
		},
		{
			name: "private calendar redaction",
			data: snap(provider.CalendarEvent{ID: "evt_priv", CalendarName: "secret-work", Title: "1:1 with manager", Location: "Room 4", Description: "sensitive", Start: "2026-09-30T14:00:00Z", End: "2026-09-30T15:00:00Z"}),
			exp:  buildFamilyViewExpectations{daysCount: 7, eventRedacted: map[string]string{"evt_priv": "Busy"}},
		},
		{name: "window day-count auto 6x2 is 7 days", dims: domain.NewDimension(6, 2), exp: buildFamilyViewExpectations{daysCount: 7}},
		{name: "window day-count auto 4x2 is 5 days", dims: domain.NewDimension(4, 2), exp: buildFamilyViewExpectations{daysCount: 5}},
		{name: "window day-count explicit days 3", dims: domain.NewDimension(4, 2), cfg: withFamilyCfg(map[string]any{"days": "3"}), exp: buildFamilyViewExpectations{daysCount: 3}},
		{name: "window day-count explicit days 7 at 4x2 allowed", dims: domain.NewDimension(4, 2), cfg: withFamilyCfg(map[string]any{"days": "7"}), exp: buildFamilyViewExpectations{daysCount: 7}},
		{name: "week aligns to sunday start", cfg: withFamilyCfg(map[string]any{"week_starts": "sunday"}), exp: buildFamilyViewExpectations{rangeStart: "2026-09-27"}},
		{name: "week aligns to monday start", cfg: withFamilyCfg(map[string]any{"week_starts": "monday"}), exp: buildFamilyViewExpectations{rangeStart: "2026-09-21"}},
		{
			name: "overflow cutoff on 4x2",
			dims: domain.NewDimension(4, 2),
			data: snap(
				mkCalEv("evt_o1", "alex-personal", "Busy 1", "2026-09-27T01:00:00Z", "2026-09-27T09:00:00Z", false),
				mkCalEv("evt_o2", "alex-personal", "Busy 2", "2026-09-27T02:00:00Z", "2026-09-27T09:00:00Z", false),
				mkCalEv("evt_o3", "alex-personal", "Busy 3", "2026-09-27T03:00:00Z", "2026-09-27T09:00:00Z", false),
				mkCalEv("evt_o4", "alex-personal", "Busy 4", "2026-09-27T04:00:00Z", "2026-09-27T09:00:00Z", false),
				mkCalEv("evt_o5", "alex-personal", "Busy 5", "2026-09-27T05:00:00Z", "2026-09-27T09:00:00Z", false),
				mkCalEv("evt_o6", "alex-personal", "Busy 6", "2026-09-27T06:00:00Z", "2026-09-27T09:00:00Z", false),
			),
			exp: buildFamilyViewExpectations{daysCount: 5, dayTimedCount: map[int]int{0: 3}, dayOverflow: map[int]int{0: 3}},
		},
		{name: "missing member color error", cfg: map[string]any{"members": []any{map[string]any{"name": "Alex"}}, "calendars": []any{}}, wantErr: "requires non-empty 'color'"},
		{name: "default initial and pattern", cfg: map[string]any{"members": []any{map[string]any{"name": "zoe", "color": "#123456", "calendars": []any{}}}, "calendars": []any{}}, exp: buildFamilyViewExpectations{memberCount: 1, memberInitial: "Z", memberPattern: "solid"}},
		{name: "pointer snapshot accepted", data: &provider.CalendarSnapshot{Events: []provider.CalendarEvent{mkCalEv("evt_ptr", "alex-personal", "Ptr test", "2026-09-27T10:00:00Z", "2026-09-27T11:00:00Z", false)}}, exp: buildFamilyViewExpectations{dayTimedCount: map[int]int{0: 1}}},
		{name: "nil data accepted", data: nil, exp: buildFamilyViewExpectations{dayTimedCount: map[int]int{0: 0}}},
		{name: "members not a list", cfg: map[string]any{"members": "not-a-list", "calendars": []any{}}, exp: buildFamilyViewExpectations{memberCount: 0}},
		{name: "member entry not a map", cfg: map[string]any{"members": []any{"oops"}, "calendars": []any{}}, wantErr: "is not a mapping"},
		{name: "member calendars not a list", cfg: map[string]any{"members": []any{map[string]any{"name": "Alex", "color": "#E07A5F", "calendars": "not-a-list"}}, "calendars": []any{}}, exp: buildFamilyViewExpectations{memberCount: 1}},
		{
			name: "timezone converts to local day",
			now:  time.Date(2026, 9, 27, 12, 0, 0, 0, pdtLoc),
			data: snap(mkCalEv("evt_tz", "alex-personal", "Late call", "2026-09-28T05:30:00Z", "2026-09-28T06:00:00Z", false)),
			exp:  buildFamilyViewExpectations{eventStart: map[string]string{"evt_tz": "22:30"}, eventDay: map[string]string{"evt_tz": "2026-09-27"}},
		},
		{
			name: "multi-day all-day events and cross-window spans",
			data: snap(
				mkCalEv("evt_multi", "alex-personal", "Camping trip", "2026-09-28", "2026-09-30", true),
				mkCalEv("evt_early_start", "alex-personal", "Started earlier", "2026-09-20", "2026-09-28", true),
			),
			exp: buildFamilyViewExpectations{
				eventDates:   map[string][]string{"evt_multi": {"2026-09-28", "2026-09-29", "2026-09-30"}, "evt_early_start": {"2026-09-27", "2026-09-28"}},
				eventNoDates: map[string][]string{"evt_multi": {"2026-09-27", "2026-10-01"}},
			},
		},
		{
			name: "continuous spanning bars 6x2",
			data: snap(
				mkCalEv("e_single", "alex-personal", "Town Hall", "2026-09-30", "2026-09-30", true),
				mkCalEv("e_multi", "alex-personal", "Camping Trip", "2026-09-28", "2026-09-30", true),
				mkCalEv("e_cross", "alex-personal", "Cross-Window Trip", "2026-09-26", "2026-09-28", true),
				mkCalEv("e_future", "alex-personal", "Future Trip", "2026-10-03", "2026-10-05", true),
				mkCalEv("e_outside", "alex-personal", "Outside Trip", "2026-09-20", "2026-09-25", true),
			),
			exp: buildFamilyViewExpectations{
				spanEvents: []FamilySpanEvent{
					{StartCol: 1, ColSpan: 2, FamilyEvent: FamilyEvent{Title: "Cross-Window Trip"}},
					{StartCol: 2, ColSpan: 3, FamilyEvent: FamilyEvent{Title: "Camping Trip"}},
					{StartCol: 4, ColSpan: 1, FamilyEvent: FamilyEvent{Title: "Town Hall"}},
					{StartCol: 7, ColSpan: 1, FamilyEvent: FamilyEvent{Title: "Future Trip"}},
				},
			},
		},
		{
			name: "continuous spanning bars custom day count 4x2",
			dims: domain.NewDimension(4, 2),
			data: snap(
				mkCalEv("e_single", "alex-personal", "Town Hall", "2026-09-30", "2026-09-30", true),
				mkCalEv("e_multi", "alex-personal", "Camping Trip", "2026-09-28", "2026-09-30", true),
			),
			exp: buildFamilyViewExpectations{
				spanEvents: []FamilySpanEvent{
					{StartCol: 2, ColSpan: 3, FamilyEvent: FamilyEvent{Title: "Camping Trip"}},
					{StartCol: 4, ColSpan: 1, FamilyEvent: FamilyEvent{Title: "Town Hall"}},
				},
			},
		},
		{
			name: "week view timeline positioning and edge bands",
			cfg:  withFamilyCfg(map[string]any{"hours": []any{"07:00", "21:00"}}),
			data: snap(
				mkCalEv("evt_pos", "alex-personal", "Dentist", "2026-09-27T09:00:00Z", "2026-09-27T10:30:00Z", false),
				mkCalEv("e_early", "alex-personal", "Dawn Run", "2026-09-27T05:30:00Z", "2026-09-27T06:30:00Z", false),
				mkCalEv("e_late", "alex-personal", "Late Movie", "2026-09-27T22:00:00Z", "2026-09-27T23:30:00Z", false),
			),
			exp: buildFamilyViewExpectations{
				dayEventPos:      map[string]FamilyDayEvent{"evt_pos": {TopPct: 14.29, HeightPct: 10.71, LeftPct: 0.0, WidthPct: 100.0}},
				earlyEventsCount: map[int]int{0: 1},
				lateEventsCount:  map[int]int{0: 1},
			},
		},
		{
			name: "week view overlapping events",
			data: snap(
				mkCalEv("e1", "alex-personal", "Event 1", "2026-09-27T09:00:00Z", "2026-09-27T10:30:00Z", false),
				mkCalEv("e2", "school", "Event 2", "2026-09-27T09:30:00Z", "2026-09-27T11:00:00Z", false),
			),
			exp: buildFamilyViewExpectations{
				dayEventPos: map[string]FamilyDayEvent{"e1": {LeftPct: 0.0, WidthPct: 50.0}, "e2": {LeftPct: 50.0, WidthPct: 50.0}},
			},
		},
		{
			name: "midnight crossover",
			data: snap(mkCalEv("e_cross", "alex-personal", "Late Call", "2026-09-27T22:00:00Z", "2026-09-28T02:00:00Z", false)),
			exp:  buildFamilyViewExpectations{lateEventsCount: map[int]int{0: 1}, earlyEventsCount: map[int]int{1: 1}},
		},
		{name: "date override 2026-10-15 Thursday", cfg: withFamilyCfg(map[string]any{"date": "2026-10-15"}), exp: buildFamilyViewExpectations{rangeStart: "2026-10-11", rangeEnd: "2026-10-17", today: "2026-09-27", dayIsToday: map[int]bool{0: false, 3: false, 6: false}}},
		{name: "date override 2026-09-29 current week", cfg: withFamilyCfg(map[string]any{"date": "2026-09-29"}), exp: buildFamilyViewExpectations{rangeStart: "2026-09-27", rangeEnd: "2026-10-03", today: "2026-09-27", dayIsToday: map[int]bool{0: true, 1: false, 2: false}}},
		{name: "date override week starts monday", cfg: withFamilyCfg(map[string]any{"week_starts": "monday", "date": "2026-10-15"}), exp: buildFamilyViewExpectations{rangeStart: "2026-10-12", rangeEnd: "2026-10-18"}},
		{name: "date override days 3", dims: domain.NewDimension(3, 2), cfg: withFamilyCfg(map[string]any{"days": 3, "date": "2026-10-15"}), exp: buildFamilyViewExpectations{daysCount: 3, rangeStart: "2026-10-15", rangeEnd: "2026-10-17"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			now := testNow
			if !tc.now.IsZero() {
				now = tc.now
			}
			dims := domain.NewDimension(6, 2)
			if tc.dims.Cols > 0 || tc.dims.Rows > 0 {
				dims = tc.dims
			}
			cfg := tc.cfg
			if cfg == nil && tc.wantErr == "" {
				cfg = baseFamilyConfig()
			}
			view, err := BuildFamilyView(tc.data, cfg, dims, now)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			exp := tc.exp
			if exp.daysCount > 0 && len(view.Days) != exp.daysCount {
				t.Errorf("len(Days) = %d, want %d", len(view.Days), exp.daysCount)
			}
			if exp.rangeStart != "" && view.RangeStart != exp.rangeStart {
				t.Errorf("RangeStart = %q, want %q", view.RangeStart, exp.rangeStart)
			}
			if exp.rangeEnd != "" && view.RangeEnd != exp.rangeEnd {
				t.Errorf("RangeEnd = %q, want %q", view.RangeEnd, exp.rangeEnd)
			}
			if exp.rangeLabel != "" && view.RangeLabel != exp.rangeLabel {
				t.Errorf("RangeLabel = %q, want %q", view.RangeLabel, exp.rangeLabel)
			}
			if exp.today != "" && view.Today != exp.today {
				t.Errorf("Today = %q, want %q", view.Today, exp.today)
			}
			if exp.memberCount > 0 && len(view.Members) != exp.memberCount {
				t.Errorf("len(Members) = %d, want %d", len(view.Members), exp.memberCount)
			}
			if exp.memberInitial != "" && len(view.Members) > 0 && view.Members[0].Initial != exp.memberInitial {
				t.Errorf("Members[0].Initial = %q, want %q", view.Members[0].Initial, exp.memberInitial)
			}
			if exp.memberPattern != "" && len(view.Members) > 0 && view.Members[0].Pattern != exp.memberPattern {
				t.Errorf("Members[0].Pattern = %q, want %q", view.Members[0].Pattern, exp.memberPattern)
			}
			for dayIdx, wantCount := range exp.dayTimedCount {
				if dayIdx < len(view.Days) && len(view.Days[dayIdx].Timed) != wantCount {
					t.Errorf("Days[%d].Timed count = %d, want %d", dayIdx, len(view.Days[dayIdx].Timed), wantCount)
				}
			}
			for dayIdx, wantCount := range exp.dayAllDayCount {
				if dayIdx < len(view.Days) && len(view.Days[dayIdx].AllDay) != wantCount {
					t.Errorf("Days[%d].AllDay count = %d, want %d", dayIdx, len(view.Days[dayIdx].AllDay), wantCount)
				}
			}
			for dayIdx, wantOverflow := range exp.dayOverflow {
				if dayIdx < len(view.Days) && view.Days[dayIdx].Overflow != wantOverflow {
					t.Errorf("Days[%d].Overflow = %d, want %d", dayIdx, view.Days[dayIdx].Overflow, wantOverflow)
				}
			}
			for dayIdx, wantToday := range exp.dayIsToday {
				if dayIdx < len(view.Days) && view.Days[dayIdx].IsToday != wantToday {
					t.Errorf("Days[%d].IsToday = %v, want %v", dayIdx, view.Days[dayIdx].IsToday, wantToday)
				}
			}
			for dayIdx, wantWeekend := range exp.dayIsWeekend {
				if dayIdx < len(view.Days) && view.Days[dayIdx].IsWeekend != wantWeekend {
					t.Errorf("Days[%d].IsWeekend = %v, want %v", dayIdx, view.Days[dayIdx].IsWeekend, wantWeekend)
				}
			}
			for evID, wantOwners := range exp.eventOwners {
				found, _ := findViewEvent(view, evID)
				if found == nil || len(found.Owners) != len(wantOwners) {
					t.Fatalf("event %q owners = %v, want %v", evID, found, wantOwners)
				}
				for i, o := range wantOwners {
					if found.Owners[i] != o {
						t.Errorf("event %q owner[%d] = %q, want %q", evID, i, found.Owners[i], o)
					}
				}
			}
			for evID, wantColor := range exp.eventColors {
				if found, _ := findViewEvent(view, evID); found == nil || found.Color != wantColor {
					t.Errorf("event %q color = %v, want %q", evID, found, wantColor)
				}
			}
			for evID, wantTextColor := range exp.eventTextColors {
				if found, _ := findViewEvent(view, evID); found == nil || found.TextColor != wantTextColor {
					t.Errorf("event %q text_color = %v, want %q", evID, found, wantTextColor)
				}
			}
			for evID, wantPattern := range exp.eventPatterns {
				if found, _ := findViewEvent(view, evID); found == nil || found.Pattern != wantPattern {
					t.Errorf("event %q pattern = %v, want %q", evID, found, wantPattern)
				}
			}
			for evID, wantTitle := range exp.eventRedacted {
				if found, _ := findViewEvent(view, evID); found == nil || found.Title != wantTitle || found.Location != "" || found.Description != wantTitle {
					t.Errorf("event %q redacted = %+v, want %q", evID, found, wantTitle)
				}
			}
			for evID, wantStart := range exp.eventStart {
				found, foundDate := findViewEvent(view, evID)
				if found == nil || found.Start != wantStart {
					t.Errorf("event %q start = %v, want %q", evID, found, wantStart)
				}
				if wantDay, ok := exp.eventDay[evID]; ok && foundDate != wantDay {
					t.Errorf("event %q date = %q, want %q", evID, foundDate, wantDay)
				}
			}
			for evID, wantDates := range exp.eventDates {
				seen := make(map[string]bool)
				for _, d := range view.Days {
					for _, ev := range d.AllDay {
						if ev.ID == evID {
							seen[d.Date] = true
						}
					}
				}
				for _, d := range wantDates {
					if !seen[d] {
						t.Errorf("event %q expected on date %q", evID, d)
					}
				}
			}
			for evID, noDates := range exp.eventNoDates {
				for _, d := range view.Days {
					for _, ev := range d.AllDay {
						if ev.ID == evID {
							for _, badDate := range noDates {
								if d.Date == badDate {
									t.Errorf("event %q should NOT appear on date %q", evID, badDate)
								}
							}
						}
					}
				}
			}
			if len(exp.spanEvents) > 0 {
				if len(view.AllDayEvents) != len(exp.spanEvents) {
					t.Fatalf("len(AllDayEvents) = %d, want %d", len(view.AllDayEvents), len(exp.spanEvents))
				}
				for i, wantSpan := range exp.spanEvents {
					if got := view.AllDayEvents[i]; got.StartCol != wantSpan.StartCol || got.ColSpan != wantSpan.ColSpan || got.Title != wantSpan.Title {
						t.Errorf("AllDayEvents[%d] = %+v, want %+v", i, got, wantSpan)
					}
				}
			}
			for evID, wantPos := range exp.dayEventPos {
				var found *FamilyDayEvent
				for _, d := range view.Days {
					for i := range d.Events {
						if d.Events[i].ID == evID {
							found = &d.Events[i]
						}
					}
				}
				if found == nil {
					t.Fatalf("day event %q not found in day events", evID)
				}
				if (wantPos.TopPct > 0 && found.TopPct != wantPos.TopPct) || (wantPos.HeightPct > 0 && found.HeightPct != wantPos.HeightPct) || (wantPos.LeftPct > 0 && found.LeftPct != wantPos.LeftPct) || (wantPos.WidthPct > 0 && found.WidthPct != wantPos.WidthPct) {
					t.Errorf("event %q pos = %+v, want %+v", evID, found, wantPos)
				}
			}
			for dayIdx, wantCount := range exp.earlyEventsCount {
				if dayIdx < len(view.Days) && len(view.Days[dayIdx].EarlyEvents) != wantCount {
					t.Errorf("Days[%d].EarlyEvents = %d, want %d", dayIdx, len(view.Days[dayIdx].EarlyEvents), wantCount)
				}
			}
			for dayIdx, wantCount := range exp.lateEventsCount {
				if dayIdx < len(view.Days) && len(view.Days[dayIdx].LateEvents) != wantCount {
					t.Errorf("Days[%d].LateEvents = %d, want %d", dayIdx, len(view.Days[dayIdx].LateEvents), wantCount)
				}
			}
		})
	}
}

func TestFamilyUnitHelpers(t *testing.T) {
	t.Parallel()
	if contrastTextColor("#FFFFFF") != "#000000" || contrastTextColor("#000000") != "#ffffff" || contrastTextColor("bad") != "#ffffff" {
		t.Errorf("contrastTextColor failed")
	}
	if _, _, _, ok := parseHexColor("#fff"); ok {
		t.Errorf("expected short hex to fail")
	}
	if _, _, _, ok := parseHexColor("#gggggg"); ok {
		t.Errorf("expected non-hex to fail")
	}
	if r, g, b, ok := parseHexColor("#A855F7"); !ok || r != 0xA8 || g != 0x55 || b != 0xF7 {
		t.Errorf("parseHexColor(#A855F7) failed")
	}
	if _, ok := parseFamilyTime(""); ok {
		t.Errorf("expected empty time to fail")
	}
	if _, ok := parseFamilyTime("not-a-time"); ok {
		t.Errorf("expected garbage time to fail")
	}
	if got := eventDateKey(mkCalEv("e", "c", "t", "not-a-time", "", false), time.UTC); got != "not-a-time" {
		t.Errorf("eventDateKey fallback = %q", got)
	}
	if resolveFamilyDayCount(map[string]any{"days": 7}, domain.NewDimension(4, 2)) != 7 ||
		resolveFamilyDayCount(map[string]any{"days": float64(3)}, domain.NewDimension(6, 2)) != 3 ||
		resolveFamilyDayCount(map[string]any{"days": 0}, domain.NewDimension(6, 2)) != 7 ||
		resolveFamilyDayCount(map[string]any{"days": float64(-1)}, domain.NewDimension(4, 2)) != 5 {
		t.Errorf("resolveFamilyDayCount numeric branches failed")
	}
	if stringOr(nil, "k", "fb") != "fb" || stringOr(map[string]any{"k": 123}, "k", "fb") != "fb" || stringOr(map[string]any{"k": "   "}, "k", "fb") != "fb" {
		t.Errorf("stringOr fallback branches failed")
	}
	cals := parseFamilyCalendarSources(map[string]any{"calendars": []any{"not-a-map", map[string]any{"name": ""}, map[string]any{"name": "  "}, map[string]any{"name": "valid", "private": true}}})
	if len(cals) != 1 || !cals["valid"].private {
		t.Errorf("parseFamilyCalendarSources = %+v", cals)
	}
	if truncateString("short", 10) != "short" || truncateString(strings.Repeat("a", 300), 280) != strings.Repeat("a", 279)+"…" || truncateString(strings.Repeat("🎉", 10), 5) != strings.Repeat("🎉", 4)+"…" || truncateString("abc", 1) != "a" {
		t.Errorf("truncateString branches failed")
	}
	if maxMemberColumns(domain.NewDimension(6, 2)) != 8 || maxMemberColumns(domain.NewDimension(8, 2)) != 8 || maxMemberColumns(domain.NewDimension(4, 2)) != 3 || maxMemberColumns(domain.NewDimension(3, 2)) != 3 || maxMemberColumns(domain.NewDimension(0, 0)) != 3 {
		t.Errorf("maxMemberColumns branches failed")
	}
}

func TestParseHoursBand(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		cfg         map[string]any
		wantStart   string
		wantEnd     string
		wantStartH  int
		wantStartM  int
		wantEndH    int
		wantEndM    int
		wantMarkers []string
	}{
		{name: "defaults when nil", cfg: nil, wantStart: "07:00", wantEnd: "21:00", wantStartH: 7, wantEndH: 21},
		{name: "defaults when empty", cfg: map[string]any{"hours": []any{}}, wantStart: "07:00", wantEnd: "21:00", wantStartH: 7, wantEndH: 21},
		{name: "valid custom []any", cfg: map[string]any{"hours": []any{"06:00", "22:00"}}, wantStart: "06:00", wantEnd: "22:00", wantStartH: 6, wantEndH: 22},
		{name: "valid custom []string", cfg: map[string]any{"hours": []string{"08:30", "19:45"}}, wantStart: "08:30", wantEnd: "19:45", wantStartH: 8, wantStartM: 30, wantEndH: 19, wantEndM: 45},
		{name: "valid custom bare int strings", cfg: map[string]any{"hours": []any{"6", "22"}}, wantStart: "06:00", wantEnd: "22:00", wantStartH: 6, wantEndH: 22},
		{name: "invalid length 1", cfg: map[string]any{"hours": []any{"07:00"}}, wantStart: "07:00", wantEnd: "21:00"},
		{name: "invalid start clock", cfg: map[string]any{"hours": []any{"invalid", "20:00"}}, wantStart: "07:00", wantEnd: "21:00"},
		{name: "invalid end clock", cfg: map[string]any{"hours": []any{"07:00", "invalid"}}, wantStart: "07:00", wantEnd: "21:00"},
		{name: "negative hour", cfg: map[string]any{"hours": []any{"-1:00", "20:00"}}, wantStart: "07:00", wantEnd: "21:00"},
		{name: "hour over 24", cfg: map[string]any{"hours": []any{"07:00", "25:00"}}, wantStart: "07:00", wantEnd: "21:00"},
		{name: "hour 24 with minutes", cfg: map[string]any{"hours": []any{"24:01", "24:00"}}, wantStart: "07:00", wantEnd: "21:00"},
		{name: "minute over 59", cfg: map[string]any{"hours": []any{"07:65", "20:00"}}, wantStart: "07:00", wantEnd: "21:00"},
		{name: "three parts clock", cfg: map[string]any{"hours": []any{"07:00:00", "20:00"}}, wantStart: "07:00", wantEnd: "21:00"},
		{name: "empty string clock", cfg: map[string]any{"hours": []any{"", "21:00"}}, wantStart: "07:00", wantEnd: "21:00"},
		{name: "inverted hours", cfg: map[string]any{"hours": []any{"22:00", "06:00"}}, wantStart: "07:00", wantEnd: "21:00"},
		{name: "equal hours", cfg: map[string]any{"hours": []any{"10:00", "10:00"}}, wantStart: "07:00", wantEnd: "21:00"},
		{name: "non string types", cfg: map[string]any{"hours": []any{123, 456}}, wantStart: "07:00", wantEnd: "21:00"},
		{name: "fractional markers", cfg: map[string]any{"hours": []any{"07:30", "10:15"}}, wantStart: "07:30", wantEnd: "10:15", wantMarkers: []string{"07:30", "08:00", "09:00", "10:00", "10:15"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			band := ParseHoursBand(tc.cfg)
			if band.StartStr != tc.wantStart || band.EndStr != tc.wantEnd {
				t.Errorf("got %s-%s, want %s-%s", band.StartStr, band.EndStr, tc.wantStart, tc.wantEnd)
			}
			if (tc.wantStartH != 0 && band.StartHour != tc.wantStartH) || (tc.wantStartM != 0 && band.StartMinute != tc.wantStartM) || (tc.wantEndH != 0 && band.EndHour != tc.wantEndH) || (tc.wantEndM != 0 && band.EndMinute != tc.wantEndM) {
				t.Errorf("got %02d:%02d-%02d:%02d, want %02d:%02d-%02d:%02d", band.StartHour, band.StartMinute, band.EndHour, band.EndMinute, tc.wantStartH, tc.wantStartM, tc.wantEndH, tc.wantEndM)
			}
			for i, m := range tc.wantMarkers {
				if i >= len(band.Markers) || band.Markers[i] != m {
					t.Errorf("marker %d = %v, want %q", i, band.Markers, m)
				}
			}
		})
	}
}

func TestResolveFamilyHoursBand(t *testing.T) {
	t.Parallel()
	dayIndex := map[string]int{"2026-09-27": 0, "2026-09-28": 1, "2026-09-29": 2, "2026-09-30": 3, "2026-10-01": 4, "2026-10-02": 5, "2026-10-03": 6}

	tests := []struct {
		name       string
		cfg        map[string]any
		events     []provider.CalendarEvent
		nilLoc     bool
		wantStart  string
		wantEnd    string
		wantStartH int
		wantStartM int
	}{
		{name: "nil cfg and no events", cfg: nil, wantStart: "07:00", wantEnd: "21:00", wantStartH: 7},
		{name: "all-day events only", cfg: map[string]any{}, events: []provider.CalendarEvent{mkCalEv("e1", "", "Holiday", "2026-09-28", "2026-09-28", true)}, wantStart: "07:00", wantEnd: "21:00", wantStartH: 7},
		{name: "timed 09:00", cfg: map[string]any{}, events: []provider.CalendarEvent{mkCalEv("e1", "", "", "2026-09-28T09:00:00Z", "2026-09-28T10:00:00Z", false)}, wantStart: "09:00", wantEnd: "21:00", wantStartH: 9},
		{name: "timed 08:30", cfg: map[string]any{}, events: []provider.CalendarEvent{mkCalEv("e1", "", "", "2026-09-28T08:30:00Z", "2026-09-28T09:30:00Z", false)}, wantStart: "08:30", wantEnd: "21:00", wantStartH: 8, wantStartM: 30},
		{name: "timed 09:45 floors to 09:30", cfg: map[string]any{}, events: []provider.CalendarEvent{mkCalEv("e1", "", "", "2026-09-28T09:45:00Z", "2026-09-28T10:30:00Z", false)}, wantStart: "09:30", wantEnd: "21:00", wantStartH: 9, wantStartM: 30},
		{name: "timed 09:15 floors to 09:00", cfg: map[string]any{}, events: []provider.CalendarEvent{mkCalEv("e1", "", "", "2026-09-28T09:15:00Z", "2026-09-28T10:00:00Z", false)}, wantStart: "09:00", wantEnd: "21:00", wantStartH: 9},
		{name: "timed 17:15 floors to 17:00", cfg: map[string]any{}, events: []provider.CalendarEvent{mkCalEv("e1", "", "", "2026-09-28T17:15:00Z", "2026-09-28T18:00:00Z", false)}, wantStart: "17:00", wantEnd: "21:00", wantStartH: 17},
		{name: "timed 17:35 violates 240-min window", cfg: map[string]any{}, events: []provider.CalendarEvent{mkCalEv("e1", "", "", "2026-09-28T17:35:00Z", "2026-09-28T18:30:00Z", false)}, wantStart: "07:00", wantEnd: "21:00", wantStartH: 7},
		{name: "timed 05:15 floors to 05:00", cfg: map[string]any{}, events: []provider.CalendarEvent{mkCalEv("e1", "", "", "2026-09-28T05:15:00Z", "2026-09-28T06:30:00Z", false)}, wantStart: "05:00", wantEnd: "21:00", wantStartH: 5},
		{name: "multiple events picks earliest", cfg: map[string]any{}, events: []provider.CalendarEvent{mkCalEv("e1", "", "", "2026-09-28T11:00:00Z", "2026-09-28T12:00:00Z", false), mkCalEv("e2", "", "", "2026-09-29T08:15:00Z", "2026-09-29T09:00:00Z", false), mkCalEv("e3", "", "", "2026-09-30T14:00:00Z", "2026-09-30T15:00:00Z", false)}, wantStart: "08:00", wantEnd: "21:00", wantStartH: 8},
		{name: "event outside week ignored", cfg: map[string]any{}, events: []provider.CalendarEvent{mkCalEv("e1", "", "", "2026-10-15T06:00:00Z", "2026-10-15T07:00:00Z", false)}, wantStart: "07:00", wantEnd: "21:00", wantStartH: 7},
		{name: "invalid start ignored", cfg: map[string]any{}, events: []provider.CalendarEvent{mkCalEv("e1", "", "", "invalid-time", "2026-09-28T10:00:00Z", false)}, wantStart: "07:00", wantEnd: "21:00", wantStartH: 7},
		{name: "late event 19:00 fallback", cfg: map[string]any{}, events: []provider.CalendarEvent{mkCalEv("e1", "", "", "2026-09-28T19:00:00Z", "2026-09-28T20:00:00Z", false)}, wantStart: "07:00", wantEnd: "21:00", wantStartH: 7},
		{name: "late event 22:00 fallback", cfg: map[string]any{}, events: []provider.CalendarEvent{mkCalEv("e1", "", "", "2026-09-28T22:00:00Z", "2026-09-28T23:00:00Z", false)}, wantStart: "07:00", wantEnd: "21:00", wantStartH: 7},
		{name: "explicit []any overrides dynamic", cfg: map[string]any{"hours": []any{"06:00", "22:00"}}, events: []provider.CalendarEvent{mkCalEv("e1", "", "", "2026-09-28T09:00:00Z", "2026-09-28T10:00:00Z", false)}, wantStart: "06:00", wantEnd: "22:00", wantStartH: 6},
		{name: "explicit []string overrides dynamic", cfg: map[string]any{"hours": []string{"08:00", "20:00"}}, events: []provider.CalendarEvent{mkCalEv("e1", "", "", "2026-09-28T06:00:00Z", "2026-09-28T07:00:00Z", false)}, wantStart: "08:00", wantEnd: "20:00", wantStartH: 8},
		{name: "invalid explicit falls back to dynamic", cfg: map[string]any{"hours": []any{"invalid", "21:00"}}, events: []provider.CalendarEvent{mkCalEv("e1", "", "", "2026-09-28T09:00:00Z", "2026-09-28T10:00:00Z", false)}, wantStart: "09:00", wantEnd: "21:00", wantStartH: 9},
		{name: "inverted explicit falls back to default", cfg: map[string]any{"hours": []any{"22:00", "06:00"}}, wantStart: "07:00", wantEnd: "21:00", wantStartH: 7},
		{name: "nil loc defaults safely", cfg: map[string]any{}, events: []provider.CalendarEvent{mkCalEv("e1", "", "", "2026-09-28T10:00:00Z", "2026-09-28T11:00:00Z", false)}, nilLoc: true, wantStart: "10:00", wantEnd: "21:00", wantStartH: 10},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			loc := time.UTC
			if tc.nilLoc {
				loc = nil
			}
			band := resolveFamilyHoursBand(tc.cfg, tc.events, dayIndex, loc)
			if band.StartStr != tc.wantStart || band.EndStr != tc.wantEnd || band.StartHour != tc.wantStartH || band.StartMinute != tc.wantStartM || len(band.TimelineMarkers) == 0 {
				t.Errorf("got %+v, want %s-%s (%02d:%02d)", band, tc.wantStart, tc.wantEnd, tc.wantStartH, tc.wantStartM)
			}
		})
	}
}

func TestGenerateTimelineMarkers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name              string
		hStart, mStart    int
		hEnd, mEnd        int
		wantNil           bool
		wantLen           int
		wantFirst         TimelineMarker
		wantSecond        TimelineMarker
		wantLast          TimelineMarker
		wantSecondLastLbl string
	}{
		{name: "standard 07:00 to 21:00", hStart: 7, hEnd: 21, wantLen: 15, wantFirst: TimelineMarker{Label: "07:00", TopPct: 0, IsStart: true}, wantSecond: TimelineMarker{Label: "08:00", TopPct: 7.14}, wantLast: TimelineMarker{Label: "21:00", TopPct: 100, IsEnd: true}},
		{name: "half-hour start 09:30 to 21:00", hStart: 9, mStart: 30, hEnd: 21, wantLen: 13, wantFirst: TimelineMarker{Label: "09:30", TopPct: 0, IsStart: true}, wantSecond: TimelineMarker{Label: "10:00", TopPct: 4.35}, wantLast: TimelineMarker{Label: "21:00", TopPct: 100, IsEnd: true}},
		{name: "non-zero end minute 08:30 to 19:45", hStart: 8, mStart: 30, hEnd: 19, mEnd: 45, wantFirst: TimelineMarker{Label: "08:30", TopPct: 0, IsStart: true}, wantLast: TimelineMarker{Label: "19:45", TopPct: 100, IsEnd: true}, wantSecondLastLbl: "19:00"},
		{name: "invalid duration returns nil", hStart: 21, hEnd: 7, wantNil: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			markers := generateTimelineMarkers(tc.hStart, tc.mStart, tc.hEnd, tc.mEnd)
			if tc.wantNil {
				if markers != nil {
					t.Fatalf("expected nil, got %+v", markers)
				}
				return
			}
			if (tc.wantLen > 0 && len(markers) != tc.wantLen) || markers[0] != tc.wantFirst || markers[len(markers)-1] != tc.wantLast {
				t.Errorf("markers = %+v", markers)
			}
			if tc.wantSecond.Label != "" && markers[1] != tc.wantSecond {
				t.Errorf("second = %+v, want %+v", markers[1], tc.wantSecond)
			}
			if tc.wantSecondLastLbl != "" && markers[len(markers)-2].Label != tc.wantSecondLastLbl {
				t.Errorf("secondLast = %q, want %q", markers[len(markers)-2].Label, tc.wantSecondLastLbl)
			}
		})
	}
}

func TestComputeDayEventPosition(t *testing.T) {
	t.Parallel()
	day := testNow
	band := defaultHoursBand()

	tests := []struct {
		name             string
		start, end       string
		nextDay          bool
		customBand       *HoursBand
		wantTop          float64
		wantHeight       float64
		wantEarly        bool
		wantLate         bool
		wantMinHeight    float64
		wantMaxTopHeight float64
		wantNonZero      bool
	}{
		{name: "normal inside window", start: "2026-09-27 09:00", end: "2026-09-27 10:30", wantTop: 14.29, wantHeight: 10.71},
		{name: "out of bounds early", start: "2026-09-27 05:00", end: "2026-09-27 06:30", wantEarly: true},
		{name: "out of bounds late", start: "2026-09-27 21:30", end: "2026-09-27 23:00", wantTop: 100.0, wantLate: true},
		{name: "early spanning into window", start: "2026-09-27 06:00", end: "2026-09-27 08:30", wantHeight: 10.71, wantEarly: true},
		{name: "late spanning out of window", start: "2026-09-27 20:00", end: "2026-09-27 23:00", wantTop: 92.86, wantHeight: 7.14, wantLate: true},
		{name: "midnight crossing Day 1", start: "2026-09-27 22:00", end: "2026-09-28 02:00", wantTop: 100.0, wantLate: true},
		{name: "midnight crossing Day 2", start: "2026-09-27 22:00", end: "2026-09-28 02:00", nextDay: true, wantEarly: true},
		{name: "short event minimum height", start: "2026-09-27 09:00", end: "2026-09-27 09:02", wantMinHeight: 2.0},
		{name: "edge clamping near bottom", start: "2026-09-27 20:59", end: "2026-09-27 21:00", wantMaxTopHeight: 100.0},
		{name: "fallback on inverted band", start: "2026-09-27 09:00", end: "2026-09-27 10:00", customBand: &HoursBand{StartHour: 20, EndHour: 10}, wantNonZero: true},
		{name: "end before start defaults to 30m", start: "2026-09-27 09:00", end: "2026-09-27 08:00", wantHeight: 3.57},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			refDay := day
			if tc.nextDay {
				refDay = day.AddDate(0, 0, 1)
			}
			hb := band
			if tc.customBand != nil {
				hb = *tc.customBand
			}
			top, height, early, late := ComputeDayEventPosition(mustTimeStatic("2006-01-02 15:04", tc.start), mustTimeStatic("2006-01-02 15:04", tc.end), refDay, hb)
			if (tc.wantTop > 0 && top != tc.wantTop) || (tc.wantHeight > 0 && height != tc.wantHeight) || early != tc.wantEarly || late != tc.wantLate {
				t.Errorf("got (%v, %v, %v, %v), want (%v, %v, %v, %v)", top, height, early, late, tc.wantTop, tc.wantHeight, tc.wantEarly, tc.wantLate)
			}
			if (tc.wantMinHeight > 0 && height < tc.wantMinHeight) || (tc.wantMaxTopHeight > 0 && top+height > tc.wantMaxTopHeight) || (tc.wantNonZero && top == 0 && height == 0) {
				t.Errorf("bounds check failed: top=%v, height=%v", top, height)
			}
		})
	}
}

type familyDayViewExpectations struct {
	colCount             int
	colNames             []string
	colShared            map[int]bool
	colEventsCount       map[int]int
	colEarlyCount        map[int]int
	colLateCount         map[int]int
	eventColIdx          map[string]int
	eventColSpan         map[string]int
	eventIsLinked        map[string]bool
	eventLinkGlyph       map[string]string
	eventInitials        map[string][]string
	eventColor           map[string]string
	duplicatedCols       map[string][]int
	allDayCount          int
	sharedColOmitted     bool
	privateEventRedacted bool
}

func TestBuildFamilyDayView(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		data    any
		cfg     map[string]any
		dims    domain.Dimension
		wantErr string
		exp     familyDayViewExpectations
	}{
		{
			name: "comprehensive single day view",
			data: snap(
				mkCalEv("evt_timed_alex", "alex-personal", "Alex Meeting", "2026-09-27T10:00:00Z", "2026-09-27T11:00:00Z", false),
				mkCalEv("evt_shared_holiday", "holidays", "Rosh Hashanah", "2026-09-27", "2026-09-27", true),
				mkCalEv("evt_early_alex", "alex-personal", "Dawn Run", "2026-09-27T05:30:00Z", "2026-09-27T06:30:00Z", false),
				mkCalEv("evt_late_alex", "alex-personal", "Late Movie", "2026-09-27T22:00:00Z", "2026-09-27T23:30:00Z", false),
				mkCalEv("evt_unclaimed", "holidays", "Town Parade", "2026-09-27T14:00:00Z", "2026-09-27T15:00:00Z", false),
				provider.CalendarEvent{ID: "evt_private", CalendarName: "secret-work", Title: "Confidential Call", Location: "Secret Room", Description: "Secret notes", Start: "2026-09-27T16:00:00Z", End: "2026-09-27T17:00:00Z"},
			),
			exp: familyDayViewExpectations{
				colCount: 3, colNames: []string{"Alex", "Kid", "Shared"}, colShared: map[int]bool{2: true},
				colEventsCount: map[int]int{0: 1, 1: 0, 2: 2}, colEarlyCount: map[int]int{0: 1}, colLateCount: map[int]int{0: 1},
				allDayCount: 1, privateEventRedacted: true,
			},
		},
		{
			name: "6x2 allows all 5 member columns",
			cfg:  fiveMemberConfig(), dims: domain.NewDimension(6, 2),
			data: snap(mkCalEv("e1", "alex-cal", "Alex Job", "2026-09-27T10:00:00Z", "2026-09-27T11:00:00Z", false), mkCalEv("e2", "grandma-cal", "Knitting", "2026-09-27T14:00:00Z", "2026-09-27T15:00:00Z", false)),
			exp:  familyDayViewExpectations{colCount: 5, colNames: []string{"Alex", "Kid", "Partner", "Grandma", "Grandpa"}, eventColIdx: map[string]int{"Knitting": 3}},
		},
		{
			name: "4x2 collapses remaining members into shared column with colored initials",
			cfg:  fiveMemberConfig(), dims: domain.NewDimension(4, 2),
			data: snap(mkCalEv("e1", "alex-cal", "Alex Job", "2026-09-27T10:00:00Z", "2026-09-27T11:00:00Z", false), mkCalEv("e2", "grandma-cal", "Knitting", "2026-09-27T14:00:00Z", "2026-09-27T15:00:00Z", false)),
			exp: familyDayViewExpectations{
				colCount: 4, colNames: []string{"Alex", "Kid", "Partner", "Shared"}, eventColIdx: map[string]int{"Knitting": 3},
				eventInitials: map[string][]string{"Knitting": {"G"}}, eventColor: map[string]string{"Knitting": "#F59E0B"},
				eventColSpan: map[string]int{"Knitting": 1}, eventIsLinked: map[string]bool{"Knitting": false},
			},
		},
		{
			name: "4x2 omits shared column when no collapsed or unclaimed events",
			cfg:  fiveMemberConfig(), dims: domain.NewDimension(4, 2),
			data: snap(mkCalEv("e1", "alex-cal", "Alex Job", "2026-09-27T10:00:00Z", "2026-09-27T11:00:00Z", false), mkCalEv("e2", "kid-cal", "Soccer Practice", "2026-09-27T15:00:00Z", "2026-09-27T16:00:00Z", false)),
			exp:  familyDayViewExpectations{colCount: 3, sharedColOmitted: true},
		},
		{
			name: "adjacent column ColSpan 2 and ColSpan 3",
			cfg:  fiveMemberConfig(), dims: domain.NewDimension(6, 2),
			data: snap(
				mkCalEv("e_adj_2", "alex-cal", "Team Sync", "2026-09-27T10:00:00Z", "2026-09-27T11:00:00Z", false),
				mkCalEv("e_adj_2", "kid-cal", "Team Sync", "2026-09-27T10:00:00Z", "2026-09-27T11:00:00Z", false),
				mkCalEv("e_adj_3", "kid-cal", "Family Dinner", "2026-09-27T18:00:00Z", "2026-09-27T19:30:00Z", false),
				mkCalEv("e_adj_3", "partner-cal", "Family Dinner", "2026-09-27T18:00:00Z", "2026-09-27T19:30:00Z", false),
				mkCalEv("e_adj_3", "grandma-cal", "Family Dinner", "2026-09-27T18:00:00Z", "2026-09-27T19:30:00Z", false),
			),
			exp: familyDayViewExpectations{
				colCount: 5, eventColIdx: map[string]int{"Team Sync": 0, "Family Dinner": 1},
				eventColSpan: map[string]int{"Team Sync": 2, "Family Dinner": 3}, eventIsLinked: map[string]bool{"Team Sync": false, "Family Dinner": false},
			},
		},
		{
			name: "6x2 non-adjacent members duplicate with link glyph",
			cfg:  fiveMemberConfig(), dims: domain.NewDimension(6, 2),
			data: snap(mkCalEv("e_nonadj", "alex-cal", "Doctor Visit", "2026-09-27T11:00:00Z", "2026-09-27T12:00:00Z", false), mkCalEv("e_nonadj", "partner-cal", "Doctor Visit", "2026-09-27T11:00:00Z", "2026-09-27T12:00:00Z", false)),
			exp:  familyDayViewExpectations{colCount: 5, duplicatedCols: map[string][]int{"Doctor Visit": {0, 2}}, eventIsLinked: map[string]bool{"Doctor Visit": true}, eventLinkGlyph: map[string]string{"Doctor Visit": "🔗"}, eventColSpan: map[string]int{"Doctor Visit": 1}},
		},
		{
			name: "4x2 mixed active and collapsed member duplicates into member and shared",
			cfg:  fiveMemberConfig(), dims: domain.NewDimension(4, 2),
			data: snap(mkCalEv("e_mixed", "alex-cal", "Park Picnic", "2026-09-27T13:00:00Z", "2026-09-27T14:30:00Z", false), mkCalEv("e_mixed", "grandma-cal", "Park Picnic", "2026-09-27T13:00:00Z", "2026-09-27T14:30:00Z", false)),
			exp:  familyDayViewExpectations{colCount: 4, duplicatedCols: map[string][]int{"Park Picnic": {0, 3}}, eventIsLinked: map[string]bool{"Park Picnic": true}, eventLinkGlyph: map[string]string{"Park Picnic": "🔗"}},
		},
		{
			name: "unclaimed event routes to shared column",
			cfg:  fiveMemberConfig(), dims: domain.NewDimension(6, 2),
			data: snap(mkCalEv("e_unclaimed", "unclaimed-cal", "Town Marathon", "2026-09-27T09:00:00Z", "2026-09-27T11:00:00Z", false)),
			exp:  familyDayViewExpectations{colCount: 6, eventColIdx: map[string]int{"Town Marathon": 5}, eventColSpan: map[string]int{"Town Marathon": 1}, eventIsLinked: map[string]bool{"Town Marathon": false}},
		},
		{name: "invalid member config error", cfg: map[string]any{"members": []any{"not-a-map"}}, wantErr: "is not a mapping"},
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
			view, err := BuildFamilyDayView(tc.data, cfg, dims, testNow)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			exp := tc.exp
			if exp.colCount > 0 && len(view.Columns) != exp.colCount {
				t.Errorf("len(Columns) = %d, want %d", len(view.Columns), exp.colCount)
			}
			for i, name := range exp.colNames {
				if i < len(view.Columns) && view.Columns[i].Name != name {
					t.Errorf("Columns[%d].Name = %q, want %q", i, view.Columns[i].Name, name)
				}
			}
			for colIdx, wantShared := range exp.colShared {
				if colIdx < len(view.Columns) && view.Columns[colIdx].IsShared != wantShared {
					t.Errorf("Columns[%d].IsShared = %v, want %v", colIdx, view.Columns[colIdx].IsShared, wantShared)
				}
			}
			for colIdx, count := range exp.colEventsCount {
				if colIdx < len(view.Columns) && len(view.Columns[colIdx].Events) != count {
					t.Errorf("Columns[%d].Events = %d, want %d", colIdx, len(view.Columns[colIdx].Events), count)
				}
			}
			for colIdx, count := range exp.colEarlyCount {
				if colIdx < len(view.Columns) && len(view.Columns[colIdx].EarlyEvents) != count {
					t.Errorf("Columns[%d].EarlyEvents = %d, want %d", colIdx, len(view.Columns[colIdx].EarlyEvents), count)
				}
			}
			for colIdx, count := range exp.colLateCount {
				if colIdx < len(view.Columns) && len(view.Columns[colIdx].LateEvents) != count {
					t.Errorf("Columns[%d].LateEvents = %d, want %d", colIdx, len(view.Columns[colIdx].LateEvents), count)
				}
			}
			if exp.allDayCount > 0 && len(view.AllDayEvents) != exp.allDayCount {
				t.Errorf("len(AllDayEvents) = %d, want %d", len(view.AllDayEvents), exp.allDayCount)
			}
			if exp.sharedColOmitted {
				for _, col := range view.Columns {
					if col.IsShared {
						t.Errorf("expected shared column omitted")
					}
				}
			}
			if exp.privateEventRedacted {
				var priv *FamilyDayEvent
				for _, col := range view.Columns {
					for i := range col.Events {
						if col.Events[i].ID == "evt_private" {
							priv = &col.Events[i]
						}
					}
				}
				if priv == nil || priv.Title != "Busy" || priv.Description != "Busy" || priv.Location != "" {
					t.Errorf("private event redaction failed: %+v", priv)
				}
			}
			for evTitle, wantCol := range exp.eventColIdx {
				found := false
				if wantCol < len(view.Columns) {
					for _, ev := range view.Columns[wantCol].Events {
						if ev.Title == evTitle {
							found = true
							if wantSpan, ok := exp.eventColSpan[evTitle]; ok && ev.ColSpan != wantSpan {
								t.Errorf("event %q ColSpan = %d, want %d", evTitle, ev.ColSpan, wantSpan)
							}
							if wantLinked, ok := exp.eventIsLinked[evTitle]; ok && ev.IsLinked != wantLinked {
								t.Errorf("event %q IsLinked = %v, want %v", evTitle, ev.IsLinked, wantLinked)
							}
							if wantInitials, ok := exp.eventInitials[evTitle]; ok && len(ev.Initials) != len(wantInitials) {
								t.Errorf("event %q initials = %d, want %d", evTitle, len(ev.Initials), len(wantInitials))
							}
							if wantColor, ok := exp.eventColor[evTitle]; ok && ev.Color != wantColor {
								t.Errorf("event %q Color = %q, want %q", evTitle, ev.Color, wantColor)
							}
						}
					}
				}
				if !found {
					t.Errorf("event %q not found in col %d", evTitle, wantCol)
				}
			}
			for evTitle, wantCols := range exp.duplicatedCols {
				for _, cIdx := range wantCols {
					found := false
					for _, ev := range view.Columns[cIdx].Events {
						if ev.Title == evTitle {
							found = true
							if (exp.eventIsLinked[evTitle] != ev.IsLinked) || (exp.eventLinkGlyph[evTitle] != "" && ev.LinkGlyph != exp.eventLinkGlyph[evTitle]) {
								t.Errorf("event %q link mismatch in col %d: %+v", evTitle, cIdx, ev)
							}
						}
					}
					if !found {
						t.Errorf("event %q not found in duplicated col %d", evTitle, cIdx)
					}
				}
			}
		})
	}
}

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

func TestBuildFamilyMonthView(t *testing.T) {
	t.Parallel()
	defaultNow := time.Date(2026, 10, 10, 14, 0, 0, 0, time.UTC)

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
			exp := tc.exp
			if (exp.daysCount > 0 && len(view.Days) != exp.daysCount) || (exp.rangeStart != "" && view.RangeStart != exp.rangeStart) || (exp.rangeEnd != "" && view.RangeEnd != exp.rangeEnd) || (exp.monthLabel != "" && view.MonthLabel != exp.monthLabel) {
				t.Errorf("header mismatch: got %+v", view)
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
			dayMap := make(map[string]FamilyMonthDay, len(view.Days))
			for _, d := range view.Days {
				dayMap[d.Date] = d
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
				seen := make(map[string]bool)
				for _, d := range view.Days {
					for _, ev := range d.Events {
						if ev.ID == evID {
							seen[d.Date] = true
						}
					}
				}
				for _, date := range wantDates {
					if !seen[date] {
						t.Errorf("event %q missing on date %s", evID, date)
					}
				}
			}
			for evID, noDates := range exp.eventNoDates {
				for _, date := range noDates {
					for _, ev := range dayMap[date].Events {
						if ev.ID == evID {
							t.Errorf("event %q should NOT appear on %s", evID, date)
						}
					}
				}
			}
		})
	}
}

func TestBuildFamilyCombinedData(t *testing.T) {
	t.Parallel()
	weekView := &FamilyViewModel{
		Today: "2026-10-10", RangeStart: "2026-10-05", RangeEnd: "2026-10-11",
		Days:         []FamilyDay{{Date: "2026-10-10", DayOfMonth: 10}},
		AllDayEvents: []FamilySpanEvent{{FamilyEvent: FamilyEvent{ID: "e_span", Title: "Conference"}}},
	}
	dayView := &FamilyDayViewModel{
		Date: "2026-10-10", Columns: []FamilyDayColumn{{Name: "Alex"}},
		AllDayEvents: []FamilyDayEvent{{FamilyEvent: FamilyEvent{ID: "e_day_allday", Title: "Holiday"}}},
	}
	monthView := &FamilyMonthViewModel{
		RangeStart: "2026-09-28", RangeEnd: "2026-11-08",
		Days: []FamilyMonthDay{{Date: "2026-10-10", DayOfMonth: 10}},
	}

	tests := []struct {
		name      string
		dayView   *FamilyDayViewModel
		weekView  *FamilyViewModel
		monthView *FamilyMonthViewModel
		cfg       map[string]any
		wantKeys  map[string]any
	}{
		{name: "nil inputs safety"},
		{
			name: "full aggregation", dayView: dayView, weekView: weekView, monthView: monthView, cfg: map[string]any{"default_view": "week"},
			wantKeys: map[string]any{"date": "2026-10-10", "today": "2026-10-10", "range_start": "2026-10-05", "range_end": "2026-10-11", "cached_range_start": "2026-07-12", "cached_range_end": "2027-04-08", "month_range_start": "2026-09-28", "month_range_end": "2026-11-08"},
		},
		{name: "explicit cfg bounds take precedence", weekView: weekView, cfg: map[string]any{"cached_range_start": "2026-09-01", "cached_range_end": "2026-12-31"}, wantKeys: map[string]any{"cached_range_start": "2026-09-01", "cached_range_end": "2026-12-31"}},
		{name: "dayView rolling window bounds fallback", dayView: &FamilyDayViewModel{Date: "2026-10-10"}, wantKeys: map[string]any{"cached_range_start": "2026-07-12", "cached_range_end": "2027-04-08"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res := BuildFamilyCombinedData(tc.dayView, tc.weekView, tc.monthView, tc.cfg)
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

func TestCalendarWidgetTemplates(t *testing.T) {
	t.Parallel()
	laLoc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatalf("failed to load America/Los_Angeles: %v", err)
	}
	tzEvents := []provider.CalendarEvent{
		{ID: "e-mon", Title: "Monday Evening Dinner", CalendarName: "Family", Color: "#3b82f6", Start: "2026-10-13T02:00:00Z", End: "2026-10-13T03:00:00Z"},
		{ID: "e-party", Title: "Weekend Bachelorette Party", CalendarName: "Family", Color: "#a855f7", Start: "2026-10-15", End: "2026-10-15", AllDay: true},
		{ID: "e-wed", Title: "Wednesday Game Night", CalendarName: "Family", Color: "#3b82f6", Start: "2026-10-15T02:00:00Z", End: "2026-10-15T04:00:00Z"},
		{ID: "e-brunch", Title: "Brunch with Bridesmaids", CalendarName: "Family", Color: "#a855f7", Start: "2026-10-15T17:00:00Z", End: "2026-10-15T18:30:00Z"},
	}
	provider.SortCalendarEvents(tzEvents, laLoc)

	tests := []struct {
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
	}{
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
				"cf-view-switcher", "cf-view-week", "cf-week-grid", "Team Standup", "cf-today", "var(--mm-cal-today-border",
				`data-view="week" role="tab" aria-selected="true">Week</button>`,
				`data-view="month" role="tab" aria-selected="false">Month</button>`,
				"calendar-family-data", "MirrormereCalendarFamily.mount",
				"cf-week-allday-grid", "grid-column: 2 / span 3", "Camping Trip", `data-event-id="e_multi"`,
				"cf-week-body", "cf-timeline-axis", "cf-timeline-markers", "cf-hour-marker", "cf-day-canvas", "Soccer Practice", "top: 64.29%",
			},
			wantNotContains: []string{"var(--mm-coral, #ff6f59)", `data-view="day"`},
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
			},
			wantNotContains: []string{`data-view="day"`},
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
		})
	}
}

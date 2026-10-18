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
			map[string]any{
				"name":      "Alex",
				"color":     "#E07A5F",
				"pattern":   "solid",
				"calendars": []any{"alex-personal"},
			},
			map[string]any{
				"name":      "Kid",
				"color":     "#81B29A",
				"pattern":   "hatch",
				"calendars": []any{"school", "soccer"},
			},
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
	return provider.CalendarEvent{
		ID:           id,
		CalendarName: cal,
		Title:        title,
		Start:        start,
		End:          end,
		AllDay:       allDay,
	}
}

func withFamilyCfg(overrides map[string]any) map[string]any {
	cfg := baseFamilyConfig()
	for k, v := range overrides {
		cfg[k] = v
	}
	return cfg
}

func renderWidgetTemplate(t *testing.T, widgetDir string, ctx Context) string {
	t.Helper()
	now := testNow
	if n, ok := ctx.Config["__now"].(time.Time); ok {
		now = n
	}
	tmplPath := filepath.Join("..", "..", "widgets", widgetDir, "views", "widget.html")
	if _, err := os.Stat(tmplPath); os.IsNotExist(err) {
		if widgetDir == "calendar-family" {
			tmplPath = filepath.Join("..", "..", "widgets", "calendar-grid", "views", "widget.html")
		}
	}
	tmpl, err := template.New("widget.html").Funcs(StandardFuncMap(func() time.Time {
		return now
	})).ParseFiles(tmplPath)
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
			name: "member resolution and unclaimed color and text color",
			data: provider.CalendarSnapshot{Events: []provider.CalendarEvent{
				mkCalEv("evt_1", "alex-personal", "Dentist", "2026-09-27T10:00:00Z", "2026-09-27T11:00:00Z", false),
				mkCalEv("evt_2", "holidays", "Rosh Hashanah", "2026-09-27", "2026-09-27", true),
			}},
			exp: buildFamilyViewExpectations{
				daysCount:       7,
				dayTimedCount:   map[int]int{0: 1},
				dayAllDayCount:  map[int]int{0: 1},
				eventOwners:     map[string][]string{"evt_1": {"Alex"}},
				eventColors:     map[string]string{"evt_1": "#E07A5F", "evt_2": "#3D405B"},
				eventTextColors: map[string]string{"evt_1": "#ffffff", "evt_2": "#ffffff"},
				eventPatterns:   map[string]string{"evt_1": "solid", "evt_2": ""},
			},
		},
		{
			name: "shared event merge by ID",
			data: provider.CalendarSnapshot{Events: []provider.CalendarEvent{
				mkCalEv("evt_shared_1", "alex-personal", "Shared Soccer", "2026-09-27T14:00:00Z", "2026-09-27T15:00:00Z", false),
				mkCalEv("evt_shared_1", "soccer", "Shared Soccer", "2026-09-27T14:00:00Z", "2026-09-27T15:00:00Z", false),
			}},
			exp: buildFamilyViewExpectations{
				daysCount:     7,
				dayTimedCount: map[int]int{0: 1},
				eventOwners:   map[string][]string{"evt_shared_1": {"Alex", "Kid"}},
			},
		},
		{
			name: "private calendar redaction",
			data: provider.CalendarSnapshot{Events: []provider.CalendarEvent{
				{ID: "evt_priv", CalendarName: "secret-work", Title: "1:1 with manager", Location: "Room 4", Description: "sensitive", Start: "2026-09-30T14:00:00Z", End: "2026-09-30T15:00:00Z"},
			}},
			exp: buildFamilyViewExpectations{
				daysCount:     7,
				eventRedacted: map[string]string{"evt_priv": "Busy"},
			},
		},
		{
			name: "window day-count auto 6x2 is 7 days",
			dims: domain.NewDimension(6, 2),
			exp:  buildFamilyViewExpectations{daysCount: 7},
		},
		{
			name: "window day-count auto 4x2 is 5 days",
			dims: domain.NewDimension(4, 2),
			exp:  buildFamilyViewExpectations{daysCount: 5},
		},
		{
			name: "window day-count explicit days 3",
			dims: domain.NewDimension(4, 2),
			cfg:  withFamilyCfg(map[string]any{"days": "3"}),
			exp:  buildFamilyViewExpectations{daysCount: 3},
		},
		{
			name: "window day-count explicit days 7 at 4x2 allowed",
			dims: domain.NewDimension(4, 2),
			cfg:  withFamilyCfg(map[string]any{"days": "7"}),
			exp:  buildFamilyViewExpectations{daysCount: 7},
		},
		{
			name: "week aligns to sunday start",
			cfg:  withFamilyCfg(map[string]any{"week_starts": "sunday"}),
			exp:  buildFamilyViewExpectations{rangeStart: "2026-09-27"},
		},
		{
			name: "week aligns to monday start",
			cfg:  withFamilyCfg(map[string]any{"week_starts": "monday"}),
			exp:  buildFamilyViewExpectations{rangeStart: "2026-09-21"},
		},
		{
			name: "overflow cutoff on 4x2",
			dims: domain.NewDimension(4, 2),
			data: provider.CalendarSnapshot{Events: []provider.CalendarEvent{
				mkCalEv("evt_o1", "alex-personal", "Busy 1", "2026-09-27T01:00:00Z", "2026-09-27T09:00:00Z", false),
				mkCalEv("evt_o2", "alex-personal", "Busy 2", "2026-09-27T02:00:00Z", "2026-09-27T09:00:00Z", false),
				mkCalEv("evt_o3", "alex-personal", "Busy 3", "2026-09-27T03:00:00Z", "2026-09-27T09:00:00Z", false),
				mkCalEv("evt_o4", "alex-personal", "Busy 4", "2026-09-27T04:00:00Z", "2026-09-27T09:00:00Z", false),
				mkCalEv("evt_o5", "alex-personal", "Busy 5", "2026-09-27T05:00:00Z", "2026-09-27T09:00:00Z", false),
				mkCalEv("evt_o6", "alex-personal", "Busy 6", "2026-09-27T06:00:00Z", "2026-09-27T09:00:00Z", false),
			}},
			exp: buildFamilyViewExpectations{
				daysCount:     5,
				dayTimedCount: map[int]int{0: 3},
				dayOverflow:   map[int]int{0: 3},
			},
		},
		{
			name: "today and weekend flags",
			exp: buildFamilyViewExpectations{
				daysCount:    7,
				dayIsToday:   map[int]bool{0: true, 3: false},
				dayIsWeekend: map[int]bool{0: true, 3: false, 6: true},
			},
		},
		{
			name:    "missing member color error",
			cfg:     map[string]any{"members": []any{map[string]any{"name": "Alex"}}, "calendars": []any{}},
			wantErr: "requires non-empty 'color'",
		},
		{
			name: "default initial and pattern",
			cfg:  map[string]any{"members": []any{map[string]any{"name": "zoe", "color": "#123456", "calendars": []any{}}}, "calendars": []any{}},
			exp:  buildFamilyViewExpectations{memberCount: 1, memberInitial: "Z", memberPattern: "solid"},
		},
		{
			name: "pointer snapshot accepted",
			data: &provider.CalendarSnapshot{Events: []provider.CalendarEvent{
				mkCalEv("evt_ptr", "alex-personal", "Ptr test", "2026-09-27T10:00:00Z", "2026-09-27T11:00:00Z", false),
			}},
			exp: buildFamilyViewExpectations{dayTimedCount: map[int]int{0: 1}},
		},
		{
			name: "nil data accepted",
			data: nil,
			exp:  buildFamilyViewExpectations{dayTimedCount: map[int]int{0: 0}},
		},
		{
			name: "members not a list",
			cfg:  map[string]any{"members": "not-a-list", "calendars": []any{}},
			exp:  buildFamilyViewExpectations{memberCount: 0},
		},
		{
			name:    "member entry not a map",
			cfg:     map[string]any{"members": []any{"oops"}, "calendars": []any{}},
			wantErr: "is not a mapping",
		},
		{
			name: "member calendars not a list",
			cfg:  map[string]any{"members": []any{map[string]any{"name": "Alex", "color": "#E07A5F", "calendars": "not-a-list"}}, "calendars": []any{}},
			exp:  buildFamilyViewExpectations{memberCount: 1},
		},
		{
			name: "timezone converts to local day",
			now:  time.Date(2026, 9, 27, 12, 0, 0, 0, pdtLoc),
			data: provider.CalendarSnapshot{Events: []provider.CalendarEvent{
				mkCalEv("evt_tz", "alex-personal", "Late call", "2026-09-28T05:30:00Z", "2026-09-28T06:00:00Z", false),
			}},
			exp: buildFamilyViewExpectations{
				eventStart: map[string]string{"evt_tz": "22:30"},
				eventDay:   map[string]string{"evt_tz": "2026-09-27"},
			},
		},
		{
			name: "contrast text color bound to event",
			data: provider.CalendarSnapshot{Events: []provider.CalendarEvent{
				mkCalEv("evt_light", "school", "Light bg", "2026-09-27T15:00:00Z", "2026-09-27T16:00:00Z", false),
			}},
			exp: buildFamilyViewExpectations{
				eventColors:     map[string]string{"evt_light": "#81B29A"},
				eventTextColors: map[string]string{"evt_light": "#000000"},
			},
		},
		{
			name: "unclaimed event default text color",
			data: provider.CalendarSnapshot{Events: []provider.CalendarEvent{
				mkCalEv("evt_hol", "holidays", "Rosh Hashanah", "2026-09-27", "2026-09-27", true),
			}},
			exp: buildFamilyViewExpectations{
				eventTextColors: map[string]string{"evt_hol": "#ffffff"},
				eventPatterns:   map[string]string{"evt_hol": ""},
			},
		},
		{
			name: "multi-day all-day event spans every column",
			data: provider.CalendarSnapshot{Events: []provider.CalendarEvent{
				mkCalEv("evt_multi", "alex-personal", "Camping trip", "2026-09-28", "2026-09-30", true),
			}},
			exp: buildFamilyViewExpectations{
				eventDates:   map[string][]string{"evt_multi": {"2026-09-28", "2026-09-29", "2026-09-30"}},
				eventNoDates: map[string][]string{"evt_multi": {"2026-09-27", "2026-10-01"}},
			},
		},
		{
			name: "multi-day event spanning into window from before",
			data: provider.CalendarSnapshot{Events: []provider.CalendarEvent{
				mkCalEv("evt_early_start", "alex-personal", "Started earlier", "2026-09-20", "2026-09-28", true),
			}},
			exp: buildFamilyViewExpectations{
				eventDates: map[string][]string{"evt_early_start": {"2026-09-27", "2026-09-28"}},
			},
		},
		{
			name: "range label crosses month",
			exp:  buildFamilyViewExpectations{rangeLabel: "Sep 27 – Oct 3"},
		},
		{
			name: "continuous spanning bars 6x2",
			data: provider.CalendarSnapshot{Events: []provider.CalendarEvent{
				mkCalEv("e_single", "alex-personal", "Town Hall", "2026-09-30", "2026-09-30", true),
				mkCalEv("e_multi", "alex-personal", "Camping Trip", "2026-09-28", "2026-09-30", true),
				mkCalEv("e_cross", "alex-personal", "Cross-Window Trip", "2026-09-26", "2026-09-28", true),
				mkCalEv("e_future", "alex-personal", "Future Trip", "2026-10-03", "2026-10-05", true),
				mkCalEv("e_outside", "alex-personal", "Outside Trip", "2026-09-20", "2026-09-25", true),
			}},
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
			data: provider.CalendarSnapshot{Events: []provider.CalendarEvent{
				mkCalEv("e_single", "alex-personal", "Town Hall", "2026-09-30", "2026-09-30", true),
				mkCalEv("e_multi", "alex-personal", "Camping Trip", "2026-09-28", "2026-09-30", true),
			}},
			exp: buildFamilyViewExpectations{
				spanEvents: []FamilySpanEvent{
					{StartCol: 2, ColSpan: 3, FamilyEvent: FamilyEvent{Title: "Camping Trip"}},
					{StartCol: 4, ColSpan: 1, FamilyEvent: FamilyEvent{Title: "Town Hall"}},
				},
			},
		},
		{
			name: "week view timeline positioning",
			cfg:  withFamilyCfg(map[string]any{"hours": []any{"07:00", "21:00"}}),
			data: provider.CalendarSnapshot{Events: []provider.CalendarEvent{
				mkCalEv("evt_pos", "alex-personal", "Dentist", "2026-09-27T09:00:00Z", "2026-09-27T10:30:00Z", false),
			}},
			exp: buildFamilyViewExpectations{
				dayEventPos: map[string]FamilyDayEvent{
					"evt_pos": {TopPct: 14.29, HeightPct: 10.71, LeftPct: 0.0, WidthPct: 100.0},
				},
			},
		},
		{
			name: "week view overlapping events",
			data: provider.CalendarSnapshot{Events: []provider.CalendarEvent{
				mkCalEv("e1", "alex-personal", "Event 1", "2026-09-27T09:00:00Z", "2026-09-27T10:30:00Z", false),
				mkCalEv("e2", "school", "Event 2", "2026-09-27T09:30:00Z", "2026-09-27T11:00:00Z", false),
			}},
			exp: buildFamilyViewExpectations{
				dayEventPos: map[string]FamilyDayEvent{
					"e1": {LeftPct: 0.0, WidthPct: 50.0},
					"e2": {LeftPct: 50.0, WidthPct: 50.0},
				},
			},
		},
		{
			name: "week view edge bands",
			cfg:  withFamilyCfg(map[string]any{"hours": []any{"07:00", "21:00"}}),
			data: provider.CalendarSnapshot{Events: []provider.CalendarEvent{
				mkCalEv("e_early", "alex-personal", "Dawn Run", "2026-09-27T05:30:00Z", "2026-09-27T06:30:00Z", false),
				mkCalEv("e_late", "alex-personal", "Late Movie", "2026-09-27T22:00:00Z", "2026-09-27T23:30:00Z", false),
			}},
			exp: buildFamilyViewExpectations{
				earlyEventsCount: map[int]int{0: 1},
				lateEventsCount:  map[int]int{0: 1},
			},
		},
		{
			name: "midnight crossover",
			data: provider.CalendarSnapshot{Events: []provider.CalendarEvent{
				mkCalEv("e_cross", "alex-personal", "Late Call", "2026-09-27T22:00:00Z", "2026-09-28T02:00:00Z", false),
			}},
			exp: buildFamilyViewExpectations{
				lateEventsCount:  map[int]int{0: 1},
				earlyEventsCount: map[int]int{1: 1},
			},
		},
		{
			name: "date override 2026-10-15 Thursday",
			cfg:  withFamilyCfg(map[string]any{"date": "2026-10-15"}),
			exp: buildFamilyViewExpectations{
				rangeStart: "2026-10-11",
				rangeEnd:   "2026-10-17",
				today:      "2026-09-27",
				dayIsToday: map[int]bool{0: false, 1: false, 2: false, 3: false, 4: false, 5: false, 6: false},
			},
		},
		{
			name: "date override 2026-09-29 current week",
			cfg:  withFamilyCfg(map[string]any{"date": "2026-09-29"}),
			exp: buildFamilyViewExpectations{
				rangeStart: "2026-09-27",
				rangeEnd:   "2026-10-03",
				today:      "2026-09-27",
				dayIsToday: map[int]bool{0: true, 1: false, 2: false},
			},
		},
		{
			name: "date override week starts monday",
			cfg:  withFamilyCfg(map[string]any{"week_starts": "monday", "date": "2026-10-15"}),
			exp: buildFamilyViewExpectations{
				rangeStart: "2026-10-12",
				rangeEnd:   "2026-10-18",
			},
		},
		{
			name: "date override days 3",
			dims: domain.NewDimension(3, 2),
			cfg:  withFamilyCfg(map[string]any{"days": 3, "date": "2026-10-15"}),
			exp: buildFamilyViewExpectations{
				daysCount:  3,
				rangeStart: "2026-10-15",
				rangeEnd:   "2026-10-17",
			},
		},
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
				var found *FamilyEvent
				for _, d := range view.Days {
					for i := range d.Timed {
						if d.Timed[i].ID == evID {
							found = &d.Timed[i]
						}
					}
				}
				if found == nil {
					t.Fatalf("event %q not found in timed events", evID)
				}
				if len(found.Owners) != len(wantOwners) {
					t.Fatalf("event %q owners len = %d, want %d", evID, len(found.Owners), len(wantOwners))
				}
				for i, o := range wantOwners {
					if found.Owners[i] != o {
						t.Errorf("event %q owner[%d] = %q, want %q", evID, i, found.Owners[i], o)
					}
				}
			}
			for evID, wantColor := range exp.eventColors {
				var found *FamilyEvent
				for _, d := range view.Days {
					for i := range d.Timed {
						if d.Timed[i].ID == evID {
							found = &d.Timed[i]
						}
					}
					for i := range d.AllDay {
						if d.AllDay[i].ID == evID {
							found = &d.AllDay[i]
						}
					}
				}
				if found == nil || found.Color != wantColor {
					t.Errorf("event %q color = %v, want %q", evID, found, wantColor)
				}
			}
			for evID, wantTextColor := range exp.eventTextColors {
				var found *FamilyEvent
				for _, d := range view.Days {
					for i := range d.Timed {
						if d.Timed[i].ID == evID {
							found = &d.Timed[i]
						}
					}
					for i := range d.AllDay {
						if d.AllDay[i].ID == evID {
							found = &d.AllDay[i]
						}
					}
				}
				if found == nil || found.TextColor != wantTextColor {
					t.Errorf("event %q text_color = %v, want %q", evID, found, wantTextColor)
				}
			}
			for evID, wantPattern := range exp.eventPatterns {
				var found *FamilyEvent
				for _, d := range view.Days {
					for i := range d.Timed {
						if d.Timed[i].ID == evID {
							found = &d.Timed[i]
						}
					}
					for i := range d.AllDay {
						if d.AllDay[i].ID == evID {
							found = &d.AllDay[i]
						}
					}
				}
				if found == nil || found.Pattern != wantPattern {
					t.Errorf("event %q pattern = %v, want %q", evID, found, wantPattern)
				}
			}
			for evID, wantTitle := range exp.eventRedacted {
				var found *FamilyEvent
				for _, d := range view.Days {
					for i := range d.Timed {
						if d.Timed[i].ID == evID {
							found = &d.Timed[i]
						}
					}
				}
				if found == nil || found.Title != wantTitle || found.Location != "" || found.Description != wantTitle {
					t.Errorf("event %q redacted title = %v, want %q", evID, found, wantTitle)
				}
			}
			for evID, wantStart := range exp.eventStart {
				var found *FamilyEvent
				var foundDate string
				for _, d := range view.Days {
					for i := range d.Timed {
						if d.Timed[i].ID == evID {
							found = &d.Timed[i]
							foundDate = d.Date
						}
					}
				}
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
					got := view.AllDayEvents[i]
					if got.StartCol != wantSpan.StartCol || got.ColSpan != wantSpan.ColSpan || got.Title != wantSpan.Title {
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
				if wantPos.TopPct > 0 && found.TopPct != wantPos.TopPct {
					t.Errorf("event %q TopPct = %v, want %v", evID, found.TopPct, wantPos.TopPct)
				}
				if wantPos.HeightPct > 0 && found.HeightPct != wantPos.HeightPct {
					t.Errorf("event %q HeightPct = %v, want %v", evID, found.HeightPct, wantPos.HeightPct)
				}
				if wantPos.LeftPct > 0 && found.LeftPct != wantPos.LeftPct {
					t.Errorf("event %q LeftPct = %v, want %v", evID, found.LeftPct, wantPos.LeftPct)
				}
				if wantPos.WidthPct > 0 && found.WidthPct != wantPos.WidthPct {
					t.Errorf("event %q WidthPct = %v, want %v", evID, found.WidthPct, wantPos.WidthPct)
				}
				if found.IsEarlyEdge != wantPos.IsEarlyEdge {
					t.Errorf("event %q IsEarlyEdge = %v, want %v", evID, found.IsEarlyEdge, wantPos.IsEarlyEdge)
				}
				if found.IsLateEdge != wantPos.IsLateEdge {
					t.Errorf("event %q IsLateEdge = %v, want %v", evID, found.IsLateEdge, wantPos.IsLateEdge)
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

	tests := []struct {
		name           string
		hex            string
		timeStr        string
		event          provider.CalendarEvent
		loc            *time.Location
		cfg            map[string]any
		key            string
		fallback       string
		dims           domain.Dimension
		truncStr       string
		maxRunes       int
		wantTextColor  string
		wantR, wantG   int
		wantB          int
		wantColorOK    bool
		wantTimeOK     bool
		wantDateKey    string
		wantDayCount   int
		wantString     string
		wantCalSources map[string]familyCalendarSource
		wantTrunc      string
		wantMemberCols int
	}{
		{name: "contrast white bg", hex: "#FFFFFF", wantTextColor: "#000000"},
		{name: "contrast black bg", hex: "#000000", wantTextColor: "#ffffff"},
		{name: "contrast malformed bg", hex: "not-a-color", wantTextColor: "#ffffff"},
		{name: "parseHexColor short", hex: "#fff", wantColorOK: false},
		{name: "parseHexColor non-hex", hex: "#gggggg", wantColorOK: false},
		{name: "parseHexColor valid", hex: "#A855F7", wantColorOK: true, wantR: 0xA8, wantG: 0x55, wantB: 0xF7},
		{name: "parseFamilyTime empty", timeStr: "", wantTimeOK: false},
		{name: "parseFamilyTime garbage", timeStr: "not-a-time", wantTimeOK: false},
		{name: "eventDateKey unparsable fallback", event: mkCalEv("e", "c", "t", "not-a-time", "", false), loc: time.UTC, wantDateKey: "not-a-time"},
		{name: "resolveFamilyDayCount int 7", cfg: map[string]any{"days": 7}, dims: domain.NewDimension(4, 2), wantDayCount: 7},
		{name: "resolveFamilyDayCount float64 3", cfg: map[string]any{"days": float64(3)}, dims: domain.NewDimension(6, 2), wantDayCount: 3},
		{name: "resolveFamilyDayCount non-positive int falls back", cfg: map[string]any{"days": 0}, dims: domain.NewDimension(6, 2), wantDayCount: 7},
		{name: "resolveFamilyDayCount non-positive float falls back", cfg: map[string]any{"days": float64(-1)}, dims: domain.NewDimension(4, 2), wantDayCount: 5},
		{name: "stringOr nil map", cfg: nil, key: "k", fallback: "fb", wantString: "fb"},
		{name: "stringOr non-string falls back", cfg: map[string]any{"k": 123}, key: "k", fallback: "fb", wantString: "fb"},
		{name: "stringOr whitespace falls back", cfg: map[string]any{"k": "   "}, key: "k", fallback: "fb", wantString: "fb"},
		{
			name: "parseFamilyCalendarSources malformed skipped",
			cfg: map[string]any{
				"calendars": []any{
					"not-a-map",
					map[string]any{"name": ""},
					map[string]any{"name": "  "},
					map[string]any{"name": "valid", "private": true},
				},
			},
			wantCalSources: map[string]familyCalendarSource{
				"valid": {name: "valid", private: true},
			},
		},
		{name: "truncateString short unchanged", truncStr: "short", maxRunes: 10, wantTrunc: "short"},
		{name: "truncateString 300 runes to 280", truncStr: strings.Repeat("a", 300), maxRunes: 280, wantTrunc: strings.Repeat("a", 279) + "…"},
		{name: "truncateString emojis", truncStr: strings.Repeat("🎉", 10), maxRunes: 5, wantTrunc: strings.Repeat("🎉", 4) + "…"},
		{name: "truncateString maxRunes 1", truncStr: "abc", maxRunes: 1, wantTrunc: "a"},
		{name: "maxMemberColumns 6x2", dims: domain.NewDimension(6, 2), wantMemberCols: 8},
		{name: "maxMemberColumns 8x2", dims: domain.NewDimension(8, 2), wantMemberCols: 8},
		{name: "maxMemberColumns 4x2", dims: domain.NewDimension(4, 2), wantMemberCols: 3},
		{name: "maxMemberColumns 3x2", dims: domain.NewDimension(3, 2), wantMemberCols: 3},
		{name: "maxMemberColumns 0x0", dims: domain.NewDimension(0, 0), wantMemberCols: 3},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.wantTextColor != "" {
				if got := contrastTextColor(tc.hex); got != tc.wantTextColor {
					t.Errorf("contrastTextColor(%q) = %q, want %q", tc.hex, got, tc.wantTextColor)
				}
			}
			if tc.wantColorOK || (tc.hex != "" && tc.wantTextColor == "") {
				r, g, b, ok := parseHexColor(tc.hex)
				if ok != tc.wantColorOK || r != tc.wantR || g != tc.wantG || b != tc.wantB {
					t.Errorf("parseHexColor(%q) = (%d, %d, %d, %v), want (%d, %d, %d, %v)",
						tc.hex, r, g, b, ok, tc.wantR, tc.wantG, tc.wantB, tc.wantColorOK)
				}
			}
			if tc.timeStr != "" {
				_, ok := parseFamilyTime(tc.timeStr)
				if ok != tc.wantTimeOK {
					t.Errorf("parseFamilyTime(%q) ok = %v, want %v", tc.timeStr, ok, tc.wantTimeOK)
				}
			}
			if tc.wantDateKey != "" {
				if got := eventDateKey(tc.event, tc.loc); got != tc.wantDateKey {
					t.Errorf("eventDateKey(...) = %q, want %q", got, tc.wantDateKey)
				}
			}
			if tc.wantDayCount > 0 {
				if got := resolveFamilyDayCount(tc.cfg, tc.dims); got != tc.wantDayCount {
					t.Errorf("resolveFamilyDayCount(...) = %d, want %d", got, tc.wantDayCount)
				}
			}
			if tc.key != "" {
				if got := stringOr(tc.cfg, tc.key, tc.fallback); got != tc.wantString {
					t.Errorf("stringOr(...) = %q, want %q", got, tc.wantString)
				}
			}
			if tc.wantCalSources != nil {
				got := parseFamilyCalendarSources(tc.cfg)
				if len(got) != len(tc.wantCalSources) {
					t.Fatalf("parseFamilyCalendarSources() len = %d, want %d", len(got), len(tc.wantCalSources))
				}
				for k, wantSrc := range tc.wantCalSources {
					if got[k] != wantSrc {
						t.Errorf("parseFamilyCalendarSources()[%q] = %+v, want %+v", k, got[k], wantSrc)
					}
				}
			}
			if tc.maxRunes > 0 {
				if got := truncateString(tc.truncStr, tc.maxRunes); got != tc.wantTrunc {
					t.Errorf("truncateString(%q, %d) = %q, want %q", tc.truncStr, tc.maxRunes, got, tc.wantTrunc)
				}
			}
			if tc.wantMemberCols > 0 {
				if got := maxMemberColumns(tc.dims); got != tc.wantMemberCols {
					t.Errorf("maxMemberColumns(%v) = %d, want %d", tc.dims, got, tc.wantMemberCols)
				}
			}
		})
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
		{
			name:        "hour markers fractional starts and ends",
			cfg:         map[string]any{"hours": []any{"07:30", "10:15"}},
			wantStart:   "07:30",
			wantEnd:     "10:15",
			wantMarkers: []string{"07:30", "08:00", "09:00", "10:00", "10:15"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			band := ParseHoursBand(tc.cfg)
			if tc.wantStart != "" && band.StartStr != tc.wantStart {
				t.Errorf("StartStr = %q, want %q", band.StartStr, tc.wantStart)
			}
			if tc.wantEnd != "" && band.EndStr != tc.wantEnd {
				t.Errorf("EndStr = %q, want %q", band.EndStr, tc.wantEnd)
			}
			if tc.wantStartH != 0 && band.StartHour != tc.wantStartH {
				t.Errorf("StartHour = %d, want %d", band.StartHour, tc.wantStartH)
			}
			if tc.wantStartM != 0 && band.StartMinute != tc.wantStartM {
				t.Errorf("StartMinute = %d, want %d", band.StartMinute, tc.wantStartM)
			}
			if tc.wantEndH != 0 && band.EndHour != tc.wantEndH {
				t.Errorf("EndHour = %d, want %d", band.EndHour, tc.wantEndH)
			}
			if tc.wantEndM != 0 && band.EndMinute != tc.wantEndM {
				t.Errorf("EndMinute = %d, want %d", band.EndMinute, tc.wantEndM)
			}
			if len(tc.wantMarkers) > 0 {
				if len(band.Markers) != len(tc.wantMarkers) {
					t.Fatalf("len(Markers) = %d, want %d", len(band.Markers), len(tc.wantMarkers))
				}
				for i, m := range tc.wantMarkers {
					if band.Markers[i] != m {
						t.Errorf("marker %d = %q, want %q", i, band.Markers[i], m)
					}
				}
			}
		})
	}
}

func TestResolveFamilyHoursBand(t *testing.T) {
	t.Parallel()

	loc := time.UTC
	dayIndex := map[string]int{
		"2026-09-27": 0,
		"2026-09-28": 1,
		"2026-09-29": 2,
		"2026-09-30": 3,
		"2026-10-01": 4,
		"2026-10-02": 5,
		"2026-10-03": 6,
	}

	tests := []struct {
		name       string
		cfg        map[string]any
		events     []provider.CalendarEvent
		loc        *time.Location
		wantStart  string
		wantEnd    string
		wantStartH int
		wantStartM int
	}{
		{
			name:       "nil cfg and no events defaults to 07:00-21:00",
			cfg:        nil,
			events:     nil,
			loc:        loc,
			wantStart:  "07:00",
			wantEnd:    "21:00",
			wantStartH: 7,
			wantStartM: 0,
		},
		{
			name: "all-day events only defaults to 07:00-21:00",
			cfg:  map[string]any{},
			events: []provider.CalendarEvent{
				mkCalEv("e1", "", "Holiday", "2026-09-28", "2026-09-28", true),
			},
			loc:        loc,
			wantStart:  "07:00",
			wantEnd:    "21:00",
			wantStartH: 7,
			wantStartM: 0,
		},
		{
			name: "timed event at 09:00 sets start to 09:00",
			cfg:  map[string]any{},
			events: []provider.CalendarEvent{
				mkCalEv("e1", "", "", "2026-09-28T09:00:00Z", "2026-09-28T10:00:00Z", false),
			},
			loc:        loc,
			wantStart:  "09:00",
			wantEnd:    "21:00",
			wantStartH: 9,
			wantStartM: 0,
		},
		{
			name: "timed event at 08:30 floors start to 08:30 for 30-min snapping",
			cfg:  map[string]any{},
			events: []provider.CalendarEvent{
				mkCalEv("e1", "", "", "2026-09-28T08:30:00Z", "2026-09-28T09:30:00Z", false),
			},
			loc:        loc,
			wantStart:  "08:30",
			wantEnd:    "21:00",
			wantStartH: 8,
			wantStartM: 30,
		},
		{
			name: "timed event at 09:45 floors start to 09:30",
			cfg:  map[string]any{},
			events: []provider.CalendarEvent{
				mkCalEv("e1", "", "", "2026-09-28T09:45:00Z", "2026-09-28T10:30:00Z", false),
			},
			loc:        loc,
			wantStart:  "09:30",
			wantEnd:    "21:00",
			wantStartH: 9,
			wantStartM: 30,
		},
		{
			name: "timed event at 09:15 floors start to 09:00",
			cfg:  map[string]any{},
			events: []provider.CalendarEvent{
				mkCalEv("e1", "", "", "2026-09-28T09:15:00Z", "2026-09-28T10:00:00Z", false),
			},
			loc:        loc,
			wantStart:  "09:00",
			wantEnd:    "21:00",
			wantStartH: 9,
			wantStartM: 0,
		},
		{
			name: "timed event at 17:15 floors to 17:00 and satisfies 240-min minimum window",
			cfg:  map[string]any{},
			events: []provider.CalendarEvent{
				mkCalEv("e1", "", "", "2026-09-28T17:15:00Z", "2026-09-28T18:00:00Z", false),
			},
			loc:        loc,
			wantStart:  "17:00",
			wantEnd:    "21:00",
			wantStartH: 17,
			wantStartM: 0,
		},
		{
			name: "timed event at 17:35 floors to 17:30 but violates 240-min window falling back to 07:00",
			cfg:  map[string]any{},
			events: []provider.CalendarEvent{
				mkCalEv("e1", "", "", "2026-09-28T17:35:00Z", "2026-09-28T18:30:00Z", false),
			},
			loc:        loc,
			wantStart:  "07:00",
			wantEnd:    "21:00",
			wantStartH: 7,
			wantStartM: 0,
		},
		{
			name: "timed event at 05:15 floors start to 05:00",
			cfg:  map[string]any{},
			events: []provider.CalendarEvent{
				mkCalEv("e1", "", "", "2026-09-28T05:15:00Z", "2026-09-28T06:30:00Z", false),
			},
			loc:        loc,
			wantStart:  "05:00",
			wantEnd:    "21:00",
			wantStartH: 5,
			wantStartM: 0,
		},
		{
			name: "multiple events across week picks earliest",
			cfg:  map[string]any{},
			events: []provider.CalendarEvent{
				mkCalEv("e1", "", "", "2026-09-28T11:00:00Z", "2026-09-28T12:00:00Z", false),
				mkCalEv("e2", "", "", "2026-09-29T08:15:00Z", "2026-09-29T09:00:00Z", false),
				mkCalEv("e3", "", "", "2026-09-30T14:00:00Z", "2026-09-30T15:00:00Z", false),
			},
			loc:        loc,
			wantStart:  "08:00",
			wantEnd:    "21:00",
			wantStartH: 8,
			wantStartM: 0,
		},
		{
			name: "event outside displayed week is ignored",
			cfg:  map[string]any{},
			events: []provider.CalendarEvent{
				mkCalEv("e1", "", "", "2026-10-15T06:00:00Z", "2026-10-15T07:00:00Z", false),
			},
			loc:        loc,
			wantStart:  "07:00",
			wantEnd:    "21:00",
			wantStartH: 7,
			wantStartM: 0,
		},
		{
			name: "event with invalid start format is ignored",
			cfg:  map[string]any{},
			events: []provider.CalendarEvent{
				mkCalEv("e1", "", "", "invalid-time", "2026-09-28T10:00:00Z", false),
			},
			loc:        loc,
			wantStart:  "07:00",
			wantEnd:    "21:00",
			wantStartH: 7,
			wantStartM: 0,
		},
		{
			name: "late event violating 4-hour window guardrail falls back to 07:00",
			cfg:  map[string]any{},
			events: []provider.CalendarEvent{
				mkCalEv("e1", "", "", "2026-09-28T19:00:00Z", "2026-09-28T20:00:00Z", false),
			},
			loc:        loc,
			wantStart:  "07:00",
			wantEnd:    "21:00",
			wantStartH: 7,
			wantStartM: 0,
		},
		{
			name: "late evening event after 21:00 falls back to 07:00",
			cfg:  map[string]any{},
			events: []provider.CalendarEvent{
				mkCalEv("e1", "", "", "2026-09-28T22:00:00Z", "2026-09-28T23:00:00Z", false),
			},
			loc:        loc,
			wantStart:  "07:00",
			wantEnd:    "21:00",
			wantStartH: 7,
			wantStartM: 0,
		},
		{
			name: "explicit hours []any overrides dynamic calculation",
			cfg:  map[string]any{"hours": []any{"06:00", "22:00"}},
			events: []provider.CalendarEvent{
				mkCalEv("e1", "", "", "2026-09-28T09:00:00Z", "2026-09-28T10:00:00Z", false),
			},
			loc:        loc,
			wantStart:  "06:00",
			wantEnd:    "22:00",
			wantStartH: 6,
			wantStartM: 0,
		},
		{
			name: "explicit hours []string overrides dynamic calculation",
			cfg:  map[string]any{"hours": []string{"08:00", "20:00"}},
			events: []provider.CalendarEvent{
				mkCalEv("e1", "", "", "2026-09-28T06:00:00Z", "2026-09-28T07:00:00Z", false),
			},
			loc:        loc,
			wantStart:  "08:00",
			wantEnd:    "20:00",
			wantStartH: 8,
			wantStartM: 0,
		},
		{
			name: "invalid explicit hours falls back to dynamic or default",
			cfg:  map[string]any{"hours": []any{"invalid", "21:00"}},
			events: []provider.CalendarEvent{
				mkCalEv("e1", "", "", "2026-09-28T09:00:00Z", "2026-09-28T10:00:00Z", false),
			},
			loc:        loc,
			wantStart:  "09:00",
			wantEnd:    "21:00",
			wantStartH: 9,
			wantStartM: 0,
		},
		{
			name:       "inverted explicit hours falls back to dynamic or default",
			cfg:        map[string]any{"hours": []any{"22:00", "06:00"}},
			events:     nil,
			loc:        loc,
			wantStart:  "07:00",
			wantEnd:    "21:00",
			wantStartH: 7,
			wantStartM: 0,
		},
		{
			name: "nil loc defaults safely without panicking",
			cfg:  map[string]any{},
			events: []provider.CalendarEvent{
				mkCalEv("e1", "", "", "2026-09-28T10:00:00Z", "2026-09-28T11:00:00Z", false),
			},
			loc:        nil,
			wantStart:  "10:00",
			wantEnd:    "21:00",
			wantStartH: 10,
			wantStartM: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			band := resolveFamilyHoursBand(tc.cfg, tc.events, dayIndex, tc.loc)
			if band.StartStr != tc.wantStart || band.EndStr != tc.wantEnd {
				t.Errorf("hours string mismatch: got %s–%s, want %s–%s", band.StartStr, band.EndStr, tc.wantStart, tc.wantEnd)
			}
			if band.StartHour != tc.wantStartH || band.StartMinute != tc.wantStartM {
				t.Errorf("start time mismatch: got %02d:%02d, want %02d:%02d", band.StartHour, band.StartMinute, tc.wantStartH, tc.wantStartM)
			}
			if len(band.TimelineMarkers) == 0 {
				t.Errorf("expected timeline markers to be populated")
			}
		})
	}
}

func TestGenerateTimelineMarkers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                string
		hStart, mStart      int
		hEnd, mEnd          int
		wantNil             bool
		wantLen             int
		wantFirstLabel      string
		wantFirstTop        float64
		wantFirstIsStart    bool
		wantLastLabel       string
		wantLastTop         float64
		wantLastIsEnd       bool
		wantSecondLabel     string
		wantSecondTop       float64
		wantSecondLastLabel string
	}{
		{
			name:             "standard 07:00 to 21:00",
			hStart: 7, mStart: 0, hEnd: 21, mEnd: 0,
			wantLen:          15,
			wantFirstLabel:   "07:00",
			wantFirstTop:     0.0,
			wantFirstIsStart: true,
			wantLastLabel:    "21:00",
			wantLastTop:      100.0,
			wantLastIsEnd:    true,
			wantSecondLabel:  "08:00",
			wantSecondTop:    7.14,
		},
		{
			name:             "half-hour start 09:30 to 21:00",
			hStart: 9, mStart: 30, hEnd: 21, mEnd: 0,
			wantLen:          13,
			wantFirstLabel:   "09:30",
			wantFirstTop:     0.0,
			wantFirstIsStart: true,
			wantLastLabel:    "21:00",
			wantLastTop:      100.0,
			wantLastIsEnd:    true,
			wantSecondLabel:  "10:00",
			wantSecondTop:    4.35,
		},
		{
			name:                "non-zero end minute 08:30 to 19:45",
			hStart: 8, mStart: 30, hEnd: 19, mEnd: 45,
			wantFirstLabel:      "08:30",
			wantFirstTop:        0.0,
			wantFirstIsStart:    true,
			wantLastLabel:       "19:45",
			wantLastTop:         100.0,
			wantLastIsEnd:       true,
			wantSecondLastLabel: "19:00",
		},
		{
			name:    "invalid duration returns nil",
			hStart: 21, mStart: 0, hEnd: 7, mEnd: 0,
			wantNil: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			markers := generateTimelineMarkers(tc.hStart, tc.mStart, tc.hEnd, tc.mEnd)
			if tc.wantNil {
				if markers != nil {
					t.Errorf("expected nil markers, got %+v", markers)
				}
				return
			}
			if markers == nil {
				t.Fatalf("expected non-nil markers")
			}
			if tc.wantLen > 0 && len(markers) != tc.wantLen {
				t.Errorf("len(markers) = %d, want %d", len(markers), tc.wantLen)
			}
			if len(markers) > 0 {
				first := markers[0]
				if tc.wantFirstLabel != "" && first.Label != tc.wantFirstLabel {
					t.Errorf("first label = %q, want %q", first.Label, tc.wantFirstLabel)
				}
				if first.TopPct != tc.wantFirstTop {
					t.Errorf("first TopPct = %v, want %v", first.TopPct, tc.wantFirstTop)
				}
				if first.IsStart != tc.wantFirstIsStart {
					t.Errorf("first IsStart = %v, want %v", first.IsStart, tc.wantFirstIsStart)
				}
				last := markers[len(markers)-1]
				if tc.wantLastLabel != "" && last.Label != tc.wantLastLabel {
					t.Errorf("last label = %q, want %q", last.Label, tc.wantLastLabel)
				}
				if last.TopPct != tc.wantLastTop {
					t.Errorf("last TopPct = %v, want %v", last.TopPct, tc.wantLastTop)
				}
				if last.IsEnd != tc.wantLastIsEnd {
					t.Errorf("last IsEnd = %v, want %v", last.IsEnd, tc.wantLastIsEnd)
				}
			}
			if tc.wantSecondLabel != "" && len(markers) > 1 {
				if markers[1].Label != tc.wantSecondLabel {
					t.Errorf("second label = %q, want %q", markers[1].Label, tc.wantSecondLabel)
				}
				if markers[1].TopPct != tc.wantSecondTop {
					t.Errorf("second TopPct = %v, want %v", markers[1].TopPct, tc.wantSecondTop)
				}
			}
			if tc.wantSecondLastLabel != "" && len(markers) > 1 {
				secondLast := markers[len(markers)-2]
				if secondLast.Label != tc.wantSecondLastLabel {
					t.Errorf("secondLast label = %q, want %q", secondLast.Label, tc.wantSecondLastLabel)
				}
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
		start            time.Time
		end              time.Time
		refDay           time.Time
		hoursBand        HoursBand
		wantTop          float64
		wantHeight       float64
		wantEarly        bool
		wantLate         bool
		wantMinHeight    float64
		wantMaxTopHeight float64
		wantNonZero      bool
	}{
		{
			name:       "normal event fully inside window",
			start:      mustTimeStatic("2006-01-02 15:04", "2026-09-27 09:00"),
			end:        mustTimeStatic("2006-01-02 15:04", "2026-09-27 10:30"),
			refDay:     day,
			hoursBand:  band,
			wantTop:    14.29,
			wantHeight: 10.71,
		},
		{
			name:       "out of bounds early collapses to early edge band",
			start:      mustTimeStatic("2006-01-02 15:04", "2026-09-27 05:00"),
			end:        mustTimeStatic("2006-01-02 15:04", "2026-09-27 06:30"),
			refDay:     day,
			hoursBand:  band,
			wantTop:    0.0,
			wantHeight: 0.0,
			wantEarly:  true,
		},
		{
			name:       "out of bounds late collapses to late edge band",
			start:      mustTimeStatic("2006-01-02 15:04", "2026-09-27 21:30"),
			end:        mustTimeStatic("2006-01-02 15:04", "2026-09-27 23:00"),
			refDay:     day,
			hoursBand:  band,
			wantTop:    100.0,
			wantHeight: 0.0,
			wantLate:   true,
		},
		{
			name:       "early event spanning into window is clamped to top 0",
			start:      mustTimeStatic("2006-01-02 15:04", "2026-09-27 06:00"),
			end:        mustTimeStatic("2006-01-02 15:04", "2026-09-27 08:30"),
			refDay:     day,
			hoursBand:  band,
			wantTop:    0.0,
			wantHeight: 10.71,
			wantEarly:  true,
		},
		{
			name:       "late event spanning out of window is clamped to bottom",
			start:      mustTimeStatic("2006-01-02 15:04", "2026-09-27 20:00"),
			end:        mustTimeStatic("2006-01-02 15:04", "2026-09-27 23:00"),
			refDay:     day,
			hoursBand:  band,
			wantTop:    92.86,
			wantHeight: 7.14,
			wantLate:   true,
		},
		{
			name:      "midnight crossing Day 1 late collapse",
			start:     mustTimeStatic("2006-01-02 15:04", "2026-09-27 22:00"),
			end:       mustTimeStatic("2006-01-02 15:04", "2026-09-28 02:00"),
			refDay:    day,
			hoursBand: band,
			wantTop:   100.0,
			wantLate:  true,
		},
		{
			name:      "midnight crossing Day 2 early collapse",
			start:     mustTimeStatic("2006-01-02 15:04", "2026-09-27 22:00"),
			end:       mustTimeStatic("2006-01-02 15:04", "2026-09-28 02:00"),
			refDay:    day.AddDate(0, 0, 1),
			hoursBand: band,
			wantTop:   0.0,
			wantEarly: true,
		},
		{
			name:          "short event minimum height",
			start:         mustTimeStatic("2006-01-02 15:04", "2026-09-27 09:00"),
			end:           mustTimeStatic("2006-01-02 15:04", "2026-09-27 09:02"),
			refDay:        day,
			hoursBand:     band,
			wantMinHeight: 2.0,
		},
		{
			name:             "edge clamping near bottom",
			start:            mustTimeStatic("2006-01-02 15:04", "2026-09-27 20:59"),
			end:              mustTimeStatic("2006-01-02 15:04", "2026-09-27 21:00"),
			refDay:           day,
			hoursBand:        band,
			wantMaxTopHeight: 100.0,
		},
		{
			name:        "fallback on inverted band duration",
			start:       mustTimeStatic("2006-01-02 15:04", "2026-09-27 09:00"),
			end:         mustTimeStatic("2006-01-02 15:04", "2026-09-27 10:00"),
			refDay:      day,
			hoursBand:   HoursBand{StartHour: 20, EndHour: 10},
			wantNonZero: true,
		},
		{
			name:       "end before start defaults to 30 min duration",
			start:      mustTimeStatic("2006-01-02 15:04", "2026-09-27 09:00"),
			end:        mustTimeStatic("2006-01-02 15:04", "2026-09-27 08:00"),
			refDay:     day,
			hoursBand:  band,
			wantHeight: 3.57,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			top, height, early, late := ComputeDayEventPosition(tc.start, tc.end, tc.refDay, tc.hoursBand)
			if tc.wantTop > 0 && top != tc.wantTop {
				t.Errorf("TopPct = %v, want %v", top, tc.wantTop)
			}
			if tc.wantHeight > 0 && height != tc.wantHeight {
				t.Errorf("HeightPct = %v, want %v", height, tc.wantHeight)
			}
			if early != tc.wantEarly {
				t.Errorf("isEarlyEdge = %v, want %v", early, tc.wantEarly)
			}
			if late != tc.wantLate {
				t.Errorf("isLateEdge = %v, want %v", late, tc.wantLate)
			}
			if tc.wantMinHeight > 0 && height < tc.wantMinHeight {
				t.Errorf("HeightPct = %v, want >= %v", height, tc.wantMinHeight)
			}
			if tc.wantMaxTopHeight > 0 && top+height > tc.wantMaxTopHeight {
				t.Errorf("TopPct + HeightPct = %v, want <= %v", top+height, tc.wantMaxTopHeight)
			}
			if tc.wantNonZero && top == 0 && height == 0 {
				t.Errorf("expected non-zero top/height fallback")
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
	eventTitle           map[string]string
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
			data: provider.CalendarSnapshot{Events: []provider.CalendarEvent{
				mkCalEv("evt_timed_alex", "alex-personal", "Alex Meeting", "2026-09-27T10:00:00Z", "2026-09-27T11:00:00Z", false),
				mkCalEv("evt_shared_holiday", "holidays", "Rosh Hashanah", "2026-09-27", "2026-09-27", true),
				mkCalEv("evt_early_alex", "alex-personal", "Dawn Run", "2026-09-27T05:30:00Z", "2026-09-27T06:30:00Z", false),
				mkCalEv("evt_late_alex", "alex-personal", "Late Movie", "2026-09-27T22:00:00Z", "2026-09-27T23:30:00Z", false),
				mkCalEv("evt_unclaimed", "holidays", "Town Parade", "2026-09-27T14:00:00Z", "2026-09-27T15:00:00Z", false),
				{ID: "evt_private", CalendarName: "secret-work", Title: "Confidential Call", Location: "Secret Room", Description: "Secret notes", Start: "2026-09-27T16:00:00Z", End: "2026-09-27T17:00:00Z"},
			}},
			exp: familyDayViewExpectations{
				colCount:             3,
				colNames:             []string{"Alex", "Kid", "Shared"},
				colShared:            map[int]bool{2: true},
				colEventsCount:       map[int]int{0: 1, 1: 0, 2: 2},
				colEarlyCount:        map[int]int{0: 1},
				colLateCount:         map[int]int{0: 1},
				allDayCount:          1,
				privateEventRedacted: true,
			},
		},
		{
			name: "6x2 allows all 5 member columns",
			cfg:  fiveMemberConfig(),
			dims: domain.NewDimension(6, 2),
			data: provider.CalendarSnapshot{Events: []provider.CalendarEvent{
				mkCalEv("e1", "alex-cal", "Alex Job", "2026-09-27T10:00:00Z", "2026-09-27T11:00:00Z", false),
				mkCalEv("e2", "grandma-cal", "Knitting", "2026-09-27T14:00:00Z", "2026-09-27T15:00:00Z", false),
			}},
			exp: familyDayViewExpectations{
				colCount: 5,
				colNames: []string{"Alex", "Kid", "Partner", "Grandma", "Grandpa"},
				eventColIdx: map[string]int{
					"Knitting": 3,
				},
			},
		},
		{
			name: "4x2 collapses remaining members into shared column with colored initials",
			cfg:  fiveMemberConfig(),
			dims: domain.NewDimension(4, 2),
			data: provider.CalendarSnapshot{Events: []provider.CalendarEvent{
				mkCalEv("e1", "alex-cal", "Alex Job", "2026-09-27T10:00:00Z", "2026-09-27T11:00:00Z", false),
				mkCalEv("e2", "grandma-cal", "Knitting", "2026-09-27T14:00:00Z", "2026-09-27T15:00:00Z", false),
			}},
			exp: familyDayViewExpectations{
				colCount: 4,
				colNames: []string{"Alex", "Kid", "Partner", "Shared"},
				eventColIdx: map[string]int{
					"Knitting": 3,
				},
				eventInitials: map[string][]string{
					"Knitting": {"G"},
				},
				eventColor: map[string]string{
					"Knitting": "#F59E0B",
				},
				eventColSpan: map[string]int{
					"Knitting": 1,
				},
				eventIsLinked: map[string]bool{
					"Knitting": false,
				},
			},
		},
		{
			name: "4x2 omits shared column when no collapsed member events and no unclaimed events",
			cfg:  fiveMemberConfig(),
			dims: domain.NewDimension(4, 2),
			data: provider.CalendarSnapshot{Events: []provider.CalendarEvent{
				mkCalEv("e1", "alex-cal", "Alex Job", "2026-09-27T10:00:00Z", "2026-09-27T11:00:00Z", false),
				mkCalEv("e2", "kid-cal", "Soccer Practice", "2026-09-27T15:00:00Z", "2026-09-27T16:00:00Z", false),
			}},
			exp: familyDayViewExpectations{
				colCount:         3,
				sharedColOmitted: true,
			},
		},
		{
			name: "adjacent column ColSpan 2 and ColSpan 3",
			cfg:  fiveMemberConfig(),
			dims: domain.NewDimension(6, 2),
			data: provider.CalendarSnapshot{Events: []provider.CalendarEvent{
				mkCalEv("e_adj_2", "alex-cal", "Team Sync", "2026-09-27T10:00:00Z", "2026-09-27T11:00:00Z", false),
				mkCalEv("e_adj_2", "kid-cal", "Team Sync", "2026-09-27T10:00:00Z", "2026-09-27T11:00:00Z", false),
				mkCalEv("e_adj_3", "kid-cal", "Family Dinner", "2026-09-27T18:00:00Z", "2026-09-27T19:30:00Z", false),
				mkCalEv("e_adj_3", "partner-cal", "Family Dinner", "2026-09-27T18:00:00Z", "2026-09-27T19:30:00Z", false),
				mkCalEv("e_adj_3", "grandma-cal", "Family Dinner", "2026-09-27T18:00:00Z", "2026-09-27T19:30:00Z", false),
			}},
			exp: familyDayViewExpectations{
				colCount: 5,
				eventColIdx: map[string]int{
					"Team Sync":     0,
					"Family Dinner": 1,
				},
				eventColSpan: map[string]int{
					"Team Sync":     2,
					"Family Dinner": 3,
				},
				eventIsLinked: map[string]bool{
					"Team Sync":     false,
					"Family Dinner": false,
				},
			},
		},
		{
			name: "6x2 non-adjacent members duplicate with link glyph",
			cfg:  fiveMemberConfig(),
			dims: domain.NewDimension(6, 2),
			data: provider.CalendarSnapshot{Events: []provider.CalendarEvent{
				mkCalEv("e_nonadj", "alex-cal", "Doctor Visit", "2026-09-27T11:00:00Z", "2026-09-27T12:00:00Z", false),
				mkCalEv("e_nonadj", "partner-cal", "Doctor Visit", "2026-09-27T11:00:00Z", "2026-09-27T12:00:00Z", false),
			}},
			exp: familyDayViewExpectations{
				colCount: 5,
				duplicatedCols: map[string][]int{
					"Doctor Visit": {0, 2},
				},
				eventIsLinked: map[string]bool{
					"Doctor Visit": true,
				},
				eventLinkGlyph: map[string]string{
					"Doctor Visit": "🔗",
				},
				eventColSpan: map[string]int{
					"Doctor Visit": 1,
				},
			},
		},
		{
			name: "4x2 mixed active and collapsed member duplicates into member and shared with link glyph",
			cfg:  fiveMemberConfig(),
			dims: domain.NewDimension(4, 2),
			data: provider.CalendarSnapshot{Events: []provider.CalendarEvent{
				mkCalEv("e_mixed", "alex-cal", "Park Picnic", "2026-09-27T13:00:00Z", "2026-09-27T14:30:00Z", false),
				mkCalEv("e_mixed", "grandma-cal", "Park Picnic", "2026-09-27T13:00:00Z", "2026-09-27T14:30:00Z", false),
			}},
			exp: familyDayViewExpectations{
				colCount: 4,
				duplicatedCols: map[string][]int{
					"Park Picnic": {0, 3},
				},
				eventIsLinked: map[string]bool{
					"Park Picnic": true,
				},
				eventLinkGlyph: map[string]string{
					"Park Picnic": "🔗",
				},
			},
		},
		{
			name: "unclaimed event routes to shared column",
			cfg:  fiveMemberConfig(),
			dims: domain.NewDimension(6, 2),
			data: provider.CalendarSnapshot{Events: []provider.CalendarEvent{
				mkCalEv("e_unclaimed", "unclaimed-cal", "Town Marathon", "2026-09-27T09:00:00Z", "2026-09-27T11:00:00Z", false),
			}},
			exp: familyDayViewExpectations{
				colCount: 6,
				eventColIdx: map[string]int{
					"Town Marathon": 5,
				},
				eventColSpan: map[string]int{
					"Town Marathon": 1,
				},
				eventIsLinked: map[string]bool{
					"Town Marathon": false,
				},
			},
		},
		{
			name:    "invalid member config error",
			cfg:     map[string]any{"members": []any{"not-a-map"}},
			wantErr: "is not a mapping",
		},
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
			if len(exp.colNames) > 0 {
				for i, name := range exp.colNames {
					if i < len(view.Columns) && view.Columns[i].Name != name {
						t.Errorf("Columns[%d].Name = %q, want %q", i, view.Columns[i].Name, name)
					}
				}
			}
			for colIdx, wantShared := range exp.colShared {
				if colIdx < len(view.Columns) && view.Columns[colIdx].IsShared != wantShared {
					t.Errorf("Columns[%d].IsShared = %v, want %v", colIdx, view.Columns[colIdx].IsShared, wantShared)
				}
			}
			for colIdx, count := range exp.colEventsCount {
				if colIdx < len(view.Columns) && len(view.Columns[colIdx].Events) != count {
					t.Errorf("Columns[%d].Events count = %d, want %d", colIdx, len(view.Columns[colIdx].Events), count)
				}
			}
			for colIdx, count := range exp.colEarlyCount {
				if colIdx < len(view.Columns) && len(view.Columns[colIdx].EarlyEvents) != count {
					t.Errorf("Columns[%d].EarlyEvents count = %d, want %d", colIdx, len(view.Columns[colIdx].EarlyEvents), count)
				}
			}
			for colIdx, count := range exp.colLateCount {
				if colIdx < len(view.Columns) && len(view.Columns[colIdx].LateEvents) != count {
					t.Errorf("Columns[%d].LateEvents count = %d, want %d", colIdx, len(view.Columns[colIdx].LateEvents), count)
				}
			}
			if exp.allDayCount > 0 && len(view.AllDayEvents) != exp.allDayCount {
				t.Errorf("len(AllDayEvents) = %d, want %d", len(view.AllDayEvents), exp.allDayCount)
			}
			if exp.sharedColOmitted {
				for _, col := range view.Columns {
					if col.IsShared {
						t.Errorf("expected shared column to be omitted, found: %+v", col)
					}
				}
			}
			if exp.privateEventRedacted {
				var priv *FamilyDayEvent
				for _, col := range view.Columns {
					for _, ev := range col.Events {
						if ev.ID == "evt_private" {
							priv = &ev
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
							if wantGlyph, ok := exp.eventLinkGlyph[evTitle]; ok && ev.LinkGlyph != wantGlyph {
								t.Errorf("event %q LinkGlyph = %q, want %q", evTitle, ev.LinkGlyph, wantGlyph)
							}
							if wantInitials, ok := exp.eventInitials[evTitle]; ok {
								if len(ev.Initials) != len(wantInitials) {
									t.Errorf("event %q initials len = %d, want %d", evTitle, len(ev.Initials), len(wantInitials))
								}
							}
							if wantColor, ok := exp.eventColor[evTitle]; ok && ev.Color != wantColor {
								t.Errorf("event %q Color = %q, want %q", evTitle, ev.Color, wantColor)
							}
						}
					}
				}
				if !found {
					t.Errorf("event %q not found in column %d", evTitle, wantCol)
				}
			}
			for evTitle, wantCols := range exp.duplicatedCols {
				for _, cIdx := range wantCols {
					found := false
					if cIdx < len(view.Columns) {
						for _, ev := range view.Columns[cIdx].Events {
							if ev.Title == evTitle {
								found = true
								if wantLinked, ok := exp.eventIsLinked[evTitle]; ok && ev.IsLinked != wantLinked {
									t.Errorf("event %q IsLinked = %v in col %d", evTitle, ev.IsLinked, cIdx)
								}
								if wantGlyph, ok := exp.eventLinkGlyph[evTitle]; ok && ev.LinkGlyph != wantGlyph {
									t.Errorf("event %q LinkGlyph = %q in col %d", evTitle, ev.LinkGlyph, cIdx)
								}
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
		now     time.Time
		wantErr string
		exp     familyMonthViewExpectations
	}{
		{
			name: "sunday start default October 2026",
			cfg:  withFamilyCfg(map[string]any{"week_starts": "sunday"}),
			exp: familyMonthViewExpectations{
				daysCount:  42,
				rangeStart: "2026-09-27",
				rangeEnd:   "2026-11-07",
				monthLabel: "October 2026",
			},
		},
		{
			name: "monday start October 2026",
			cfg:  withFamilyCfg(map[string]any{"week_starts": "monday"}),
			exp: familyMonthViewExpectations{
				daysCount:  42,
				rangeStart: "2026-09-28",
				rangeEnd:   "2026-11-08",
				monthLabel: "October 2026",
			},
		},
		{
			name: "february 2024 leap year 29 days",
			cfg:  withFamilyCfg(map[string]any{"month": "2024-02"}),
			exp: familyMonthViewExpectations{
				daysCount:        42,
				monthLabel:       "February 2024",
				currentMonthDays: 29,
			},
		},
		{
			name: "year boundary transition January 2025",
			cfg:  withFamilyCfg(map[string]any{"date": "2025-01-10", "week_starts": "sunday"}),
			exp: familyMonthViewExpectations{
				rangeStart: "2024-12-29",
				monthLabel: "January 2025",
			},
		},
		{
			name: "member dot lists",
			cfg: map[string]any{
				"shared_color": "#123456",
				"members": []any{
					map[string]any{"name": "Alice", "color": "#E11D48", "initial": "A", "calendars": []any{"alice-cal"}},
					map[string]any{"name": "Bob", "color": "#2563EB", "initial": "B", "calendars": []any{"bob-cal"}},
					map[string]any{"name": "Charlie", "color": "#16A34A", "initial": "C", "calendars": []any{"charlie-cal"}},
				},
				"calendars": []any{
					map[string]any{"name": "alice-cal"},
					map[string]any{"name": "bob-cal"},
					map[string]any{"name": "charlie-cal"},
					map[string]any{"name": "unclaimed-cal"},
				},
			},
			data: provider.CalendarSnapshot{Events: []provider.CalendarEvent{
				mkCalEv("e1", "alice-cal", "Alice Event 1", "2026-10-10T09:00:00Z", "2026-10-10T10:00:00Z", false),
				mkCalEv("e2", "alice-cal", "Alice Event 2", "2026-10-10T14:00:00Z", "2026-10-10T15:00:00Z", false),
				mkCalEv("e3", "charlie-cal", "Charlie Soccer", "2026-10-10T11:00:00Z", "2026-10-10T12:00:00Z", false),
				mkCalEv("e4", "alice-cal", "Family Brunch", "2026-10-11T10:00:00Z", "2026-10-11T12:00:00Z", false),
				mkCalEv("e4", "bob-cal", "Family Brunch", "2026-10-11T10:00:00Z", "2026-10-11T12:00:00Z", false),
				mkCalEv("e5", "unclaimed-cal", "Public Holiday", "2026-10-12", "2026-10-12", true),
			}},
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
			name: "density adaptation 6x2 resolves event title",
			dims: domain.NewDimension(6, 2),
			data: provider.CalendarSnapshot{Events: []provider.CalendarEvent{
				mkCalEv("e_timed", "alex-personal", "Later Timed Event", "2026-10-15T15:00:00Z", "2026-10-15T16:00:00Z", false),
				mkCalEv("e_early", "alex-personal", "Earlier Timed Event", "2026-10-15T09:00:00Z", "2026-10-15T10:00:00Z", false),
				mkCalEv("e_allday", "alex-personal", "All Day Conference", "2026-10-15", "2026-10-15", true),
			}},
			exp: familyMonthViewExpectations{
				firstEventTitle: map[string]string{"2026-10-15": "All Day Conference"},
				eventsCount:     map[string]int{"2026-10-15": 3},
			},
		},
		{
			name: "density adaptation 4x2 resolves event title",
			dims: domain.NewDimension(4, 2),
			data: provider.CalendarSnapshot{Events: []provider.CalendarEvent{
				mkCalEv("e_timed", "alex-personal", "Later Timed Event", "2026-10-15T15:00:00Z", "2026-10-15T16:00:00Z", false),
				mkCalEv("e_allday", "alex-personal", "All Day Conference", "2026-10-15", "2026-10-15", true),
			}},
			exp: familyMonthViewExpectations{
				firstEventTitle: map[string]string{"2026-10-15": "All Day Conference"},
			},
		},
		{
			name: "density adaptation narrow 2x2 omits event titles",
			dims: domain.NewDimension(2, 2),
			data: provider.CalendarSnapshot{Events: []provider.CalendarEvent{
				mkCalEv("e_allday", "alex-personal", "All Day Conference", "2026-10-15", "2026-10-15", true),
			}},
			exp: familyMonthViewExpectations{
				emptyEventTitle: map[string]bool{"2026-10-15": true},
			},
		},
		{
			name: "today and weekend flags",
			exp: familyMonthViewExpectations{
				todayDate:      "2026-10-10",
				satSunWeekends: true,
			},
		},
		{
			name:    "invalid member config returns error",
			cfg:     map[string]any{"members": []any{"not-a-map"}},
			wantErr: "is not a mapping",
		},
		{
			name: "event outside window is ignored",
			data: provider.CalendarSnapshot{Events: []provider.CalendarEvent{
				mkCalEv("e_outside", "alex-personal", "Outside Event", "2026-11-20T10:00:00Z", "2026-11-20T11:00:00Z", false),
			}},
			exp: familyMonthViewExpectations{
				eventNoDates: map[string][]string{"e_outside": {"2026-11-20"}},
			},
		},
		{
			name: "private calendar redaction in month view",
			data: provider.CalendarSnapshot{Events: []provider.CalendarEvent{
				mkCalEv("e_priv", "secret-work", "Confidential Review", "2026-10-15T10:00:00Z", "2026-10-15T11:00:00Z", false),
			}},
			exp: familyMonthViewExpectations{
				eventRedacted: map[string]string{"e_priv": "Busy"},
			},
		},
		{
			name: "multi-day event spanning across month window",
			data: provider.CalendarSnapshot{Events: []provider.CalendarEvent{
				mkCalEv("e_multi_month", "alex-personal", "Long Vacation", "2026-09-25", "2026-10-02", true),
			}},
			exp: familyMonthViewExpectations{
				eventDates: map[string][]string{"e_multi_month": {"2026-09-27", "2026-09-28", "2026-09-29", "2026-09-30", "2026-10-01", "2026-10-02"}},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			now := defaultNow
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
			view, err := BuildFamilyMonthView(tc.data, cfg, dims, now)
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
			dayMap := make(map[string]FamilyMonthDay)
			for _, d := range view.Days {
				dayMap[d.Date] = d
			}
			for date, wantCount := range exp.eventsCount {
				if d, ok := dayMap[date]; ok && d.EventsCount != wantCount {
					t.Errorf("date %s EventsCount = %d, want %d", date, d.EventsCount, wantCount)
				}
			}
			for date, wantDots := range exp.dots {
				d, ok := dayMap[date]
				if !ok {
					t.Fatalf("date %s not found in month days", date)
				}
				if len(d.Dots) != len(wantDots) {
					t.Errorf("date %s dots count = %d, want %d", date, len(d.Dots), len(wantDots))
				}
				for i, wantDot := range wantDots {
					if i < len(d.Dots) {
						if d.Dots[i].Name != wantDot.name || d.Dots[i].Color != wantDot.color || d.Dots[i].IsShared != wantDot.isShared {
							t.Errorf("date %s dot[%d] = %+v, want %+v", date, i, d.Dots[i], wantDot)
						}
					}
				}
			}
			for date, wantTitle := range exp.firstEventTitle {
				if d, ok := dayMap[date]; ok && d.EventTitle != wantTitle {
					t.Errorf("date %s EventTitle = %q, want %q", date, d.EventTitle, wantTitle)
				}
			}
			for date := range exp.emptyEventTitle {
				if d, ok := dayMap[date]; ok && d.EventTitle != "" {
					t.Errorf("date %s expected empty EventTitle, got %q", date, d.EventTitle)
				}
			}
			if exp.todayDate != "" {
				for _, d := range view.Days {
					if d.Date == exp.todayDate && !d.IsToday {
						t.Errorf("date %s expected IsToday == true", d.Date)
					} else if d.Date != exp.todayDate && d.IsToday {
						t.Errorf("date %s expected IsToday == false", d.Date)
					}
				}
			}
			if exp.satSunWeekends {
				for _, d := range view.Days {
					isSatSun := d.WeekdayAbbr == "Sat" || d.WeekdayAbbr == "Sun"
					if d.IsWeekend != isSatSun {
						t.Errorf("date %s (%s) IsWeekend = %v, want %v", d.Date, d.WeekdayAbbr, d.IsWeekend, isSatSun)
					}
				}
			}
			for evID, wantTitle := range exp.eventRedacted {
				found := false
				for _, d := range view.Days {
					for _, ev := range d.Events {
						if ev.ID == evID {
							found = true
							if ev.Title != wantTitle {
								t.Errorf("event %q Title = %q, want %q", evID, ev.Title, wantTitle)
							}
						}
					}
				}
				if !found {
					t.Errorf("redacted event %q not found", evID)
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
				for _, d := range view.Days {
					for _, ev := range d.Events {
						if ev.ID == evID {
							for _, badDate := range noDates {
								if d.Date == badDate {
									t.Errorf("event %q should NOT appear on date %s", evID, badDate)
								}
							}
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
		Today:      "2026-10-10",
		RangeStart: "2026-10-05",
		RangeEnd:   "2026-10-11",
		Days:       []FamilyDay{{Date: "2026-10-10", DayOfMonth: 10}},
		AllDayEvents: []FamilySpanEvent{
			{FamilyEvent: FamilyEvent{ID: "e_span", Title: "Conference"}},
		},
	}
	dayView := &FamilyDayViewModel{
		Date: "2026-10-10",
		Columns: []FamilyDayColumn{
			{Name: "Alex"},
		},
		AllDayEvents: []FamilyDayEvent{
			{FamilyEvent: FamilyEvent{ID: "e_day_allday", Title: "Holiday"}},
		},
	}
	monthView := &FamilyMonthViewModel{
		RangeStart: "2026-09-28",
		RangeEnd:   "2026-11-08",
		Days:       []FamilyMonthDay{{Date: "2026-10-10", DayOfMonth: 10}},
	}

	tests := []struct {
		name             string
		dayView          *FamilyDayViewModel
		weekView         *FamilyViewModel
		monthView        *FamilyMonthViewModel
		cfg              map[string]any
		wantKeys         map[string]any
		wantFuncMapCheck bool
	}{
		{
			name: "nil inputs safety",
		},
		{
			name:      "full aggregation and funcMap helpers",
			dayView:   dayView,
			weekView:  weekView,
			monthView: monthView,
			cfg:       map[string]any{"default_view": "week"},
			wantKeys: map[string]any{
				"date":               "2026-10-10",
				"today":              "2026-10-10",
				"range_start":        "2026-10-05",
				"range_end":          "2026-10-11",
				"cached_range_start": "2026-07-12",
				"cached_range_end":   "2027-04-08",
			},
			wantFuncMapCheck: true,
		},
		{
			name:     "rolling window bounds default -90d and +180d",
			weekView: weekView,
			wantKeys: map[string]any{
				"cached_range_start": "2026-07-12",
				"cached_range_end":   "2027-04-08",
			},
		},
		{
			name:     "explicit cfg bounds take precedence",
			weekView: weekView,
			cfg: map[string]any{
				"cached_range_start": "2026-09-01",
				"cached_range_end":   "2026-12-31",
			},
			wantKeys: map[string]any{
				"cached_range_start": "2026-09-01",
				"cached_range_end":   "2026-12-31",
			},
		},
		{
			name:    "dayView rolling window bounds fallback",
			dayView: &FamilyDayViewModel{Date: "2026-10-10"},
			wantKeys: map[string]any{
				"cached_range_start": "2026-07-12",
				"cached_range_end":   "2027-04-08",
			},
		},
		{
			name:      "monthView present does not shrink bounds",
			weekView:  weekView,
			monthView: monthView,
			wantKeys: map[string]any{
				"cached_range_start": "2026-07-12",
				"cached_range_end":   "2027-04-08",
				"month_range_start":  "2026-09-28",
				"month_range_end":    "2026-11-08",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res := BuildFamilyCombinedData(tc.dayView, tc.weekView, tc.monthView, tc.cfg)
			if res == nil {
				t.Fatalf("expected non-nil map from BuildFamilyCombinedData")
			}
			for k, wantVal := range tc.wantKeys {
				if res[k] != wantVal {
					t.Errorf("res[%q] = %v, want %v", k, res[k], wantVal)
				}
			}
			if tc.wantFuncMapCheck {
				fm := StandardFuncMap(func() time.Time { return testNow })
				gridFn, ok1 := fm["calendarGridData"].(func(*FamilyDayViewModel, *FamilyViewModel, *FamilyMonthViewModel, map[string]any) map[string]any)
				familyFn, ok2 := fm["calendarFamilyData"].(func(*FamilyDayViewModel, *FamilyViewModel, *FamilyMonthViewModel, map[string]any) map[string]any)
				if !ok1 || !ok2 || gridFn == nil || familyFn == nil {
					t.Fatalf("expected calendarGridData and calendarFamilyData in StandardFuncMap")
				}
				resGrid := gridFn(tc.dayView, tc.weekView, tc.monthView, tc.cfg)
				if resGrid["date"] != tc.wantKeys["date"] {
					t.Errorf("gridFn date = %v, want %v", resGrid["date"], tc.wantKeys["date"])
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

	tests := []struct {
		name                 string
		widgetDir            string
		ctx                  Context
		funcMapCheck         bool
		funcMapType          string
		wantContains         []string
		wantNotContains      []string
		wantOrdered          []string
		wantDayCardsCount    int
		dayCardMustContain   []string
		thursCardMustContain []string
	}{
		{
			name:         "funcMap helper calendarFamilyDayView",
			funcMapCheck: true,
			funcMapType:  "calendarFamilyDayView",
		},
		{
			name:         "funcMap helper dayView and isDayView and add sub",
			funcMapCheck: true,
			funcMapType:  "dayViewAndMath",
		},
		{
			name:         "funcMap helper calendarFamilyMonthView",
			funcMapCheck: true,
			funcMapType:  "calendarFamilyMonthView",
		},
		{
			name:      "calendar-family day view fallback to week on 6x2",
			widgetDir: "calendar-family",
			ctx: Context{
				ID:         "test-family-day",
				Type:       "calendar-family",
				Dimensions: domain.NewDimension(6, 2),
				Data: provider.CalendarSnapshot{
					LastSync: "2026-09-27T12:00:00Z",
					Events: []provider.CalendarEvent{
						mkCalEv("e1", "alex-personal", "Design Review", "2026-09-27T10:00:00Z", "2026-09-27T11:00:00Z", false),
					},
				},
				Config: withFamilyCfg(map[string]any{"default_view": "day", "date": "2026-09-27"}),
			},
			wantContains: []string{
				"cf-view-week",
				"--mm-member-0: #E07A5F",
				">Alex</span>",
				">Kid</span>",
				"cf-week-grid",
				"Design Review",
			},
			wantNotContains: []string{
				"cf-view-day",
			},
		},
		{
			name:      "calendar-family day view empty events",
			widgetDir: "calendar-family",
			ctx: Context{
				ID:         "test-day-empty-events",
				Type:       "calendar-family",
				Dimensions: domain.NewDimension(6, 2),
				Data:       provider.CalendarSnapshot{},
				Config:     withFamilyCfg(map[string]any{"default_view": "day", "date": "2026-09-27"}),
			},
			wantContains: []string{
				"cf-view-week",
				"cf-week-grid",
			},
		},
		{
			name:      "calendar-family day view no members empty",
			widgetDir: "calendar-family",
			ctx: Context{
				ID:         "test-day-no-members",
				Type:       "calendar-family",
				Dimensions: domain.NewDimension(6, 2),
				Data:       nil,
				Config:     map[string]any{"default_view": "day"},
			},
			wantContains: []string{
				"No calendar data yet",
			},
		},
		{
			name:      "calendar-family week view template render",
			widgetDir: "calendar-family",
			ctx: Context{
				ID:         "test-family-week",
				Type:       "calendar-family",
				Dimensions: domain.NewDimension(6, 2),
				Data: provider.CalendarSnapshot{Events: []provider.CalendarEvent{
					mkCalEv("e_week", "alex-personal", "Team Standup", "2026-09-28T09:00:00Z", "2026-09-28T09:30:00Z", false),
				}},
				Config: withFamilyCfg(map[string]any{"default_view": "week", "hours": []any{"07:00", "21:00"}}),
			},
			wantContains: []string{
				"cf-view-week",
				"cf-week-grid",
				"Team Standup",
				"cf-today",
				"var(--mm-cal-today-border",
			},
			wantNotContains: []string{
				"var(--mm-coral, #ff6f59)",
			},
		},
		{
			name:      "calendar-family month view template render 6x2",
			widgetDir: "calendar-family",
			ctx: Context{
				ID:         "test-family-month-6x2",
				Type:       "calendar-family",
				Dimensions: domain.NewDimension(6, 2),
				Data: provider.CalendarSnapshot{
					LastSync: "2026-09-27T12:00:00Z",
					Events: []provider.CalendarEvent{
						mkCalEv("e1", "alex-personal", "Parent Teacher Night", "2026-09-27T18:00:00Z", "2026-09-27T19:00:00Z", false),
						mkCalEv("e2", "holidays", "Public Holiday", "2026-09-27", "2026-09-27", true),
					},
				},
				Config: withFamilyCfg(map[string]any{"default_view": "month", "date": "2026-09-27"}),
			},
			wantContains: []string{
				"cf-view-month",
				"September 2026",
				"cf-month-grid",
				"cf-month-day",
				"cf-month-dot",
				"var(--mm-member-0",
				"var(--mm-cf-shared",
				"cf-month-event-title",
				"Parent Teacher Night",
			},
		},
		{
			name:      "calendar-family month view template render 4x2",
			widgetDir: "calendar-family",
			ctx: Context{
				ID:         "test-family-month-4x2",
				Type:       "calendar-family",
				Dimensions: domain.NewDimension(4, 2),
				Data: provider.CalendarSnapshot{Events: []provider.CalendarEvent{
					mkCalEv("e1", "alex-personal", "Parent Teacher Night", "2026-09-27T18:00:00Z", "2026-09-27T19:00:00Z", false),
				}},
				Config: withFamilyCfg(map[string]any{"default_view": "month", "date": "2026-09-27"}),
			},
			wantContains: []string{
				"cf-view-month",
				"cf-month-dot",
				"cf-month-event-title",
				"Parent Teacher Night",
			},
		},
		{
			name:      "calendar-family month view template render narrow dots only",
			widgetDir: "calendar-family",
			ctx: Context{
				ID:         "test-family-month-narrow",
				Type:       "calendar-family",
				Dimensions: domain.NewDimension(2, 2),
				Data: provider.CalendarSnapshot{Events: []provider.CalendarEvent{
					mkCalEv("e1", "alex-personal", "Parent Teacher Night", "2026-09-27T18:00:00Z", "2026-09-27T19:00:00Z", false),
				}},
				Config: withFamilyCfg(map[string]any{"default_view": "month", "date": "2026-09-27"}),
			},
			wantContains: []string{
				"cf-month-dot",
			},
			wantNotContains: []string{
				"cf-month-event-title",
			},
		},
		{
			name:      "calendar-family month view template render empty",
			widgetDir: "calendar-family",
			ctx: Context{
				ID:         "test-family-month-empty",
				Type:       "calendar-family",
				Dimensions: domain.NewDimension(6, 2),
				Data:       provider.CalendarSnapshot{},
				Config:     withFamilyCfg(map[string]any{"default_view": "month", "date": "2026-09-27"}),
			},
			wantContains: []string{
				"cf-view-month",
				"September 2026",
				"cf-month-day",
			},
		},
		{
			name:      "calendar-family month view template render 4x2 events",
			widgetDir: "calendar-family",
			ctx: Context{
				ID:         "test-family-month-4x2-events",
				Type:       "calendar-family",
				Dimensions: domain.NewDimension(4, 2),
				Data: provider.CalendarSnapshot{Events: []provider.CalendarEvent{
					mkCalEv("e_dentist", "alex-personal", "Dentist Visit", "2026-10-10T14:00:00Z", "2026-10-10T15:00:00Z", false),
				}},
				Config: withFamilyCfg(map[string]any{"default_view": "month", "date": "2026-10-10"}),
			},
			wantContains: []string{
				"cf-view-month",
				"Dentist Visit",
				"cf-month-event-title",
				`data-event-id="e_dentist"`,
			},
		},
		{
			name:      "calendar-family view switcher week",
			widgetDir: "calendar-family",
			ctx: Context{
				ID:         "test-switcher-week",
				Type:       "calendar-family",
				Dimensions: domain.NewDimension(6, 2),
				Data: provider.CalendarSnapshot{Events: []provider.CalendarEvent{
					mkCalEv("e1", "alex-personal", "Soccer Practice", "2026-09-27T16:00:00Z", "2026-09-27T17:00:00Z", false),
				}},
				Config: withFamilyCfg(map[string]any{"default_view": "week", "date": "2026-09-27"}),
			},
			wantContains: []string{
				"cf-view-switcher",
				"cf-view-week",
				`data-view="week" role="tab" aria-selected="true">Week</button>`,
				`data-view="month" role="tab" aria-selected="false">Month</button>`,
				"calendar-family-data",
				"MirrormereCalendarFamily.mount",
			},
			wantNotContains: []string{
				`data-view="day"`,
			},
		},
		{
			name:      "calendar-family view switcher month",
			widgetDir: "calendar-family",
			ctx: Context{
				ID:         "test-switcher-month",
				Type:       "calendar-family",
				Dimensions: domain.NewDimension(6, 2),
				Data: provider.CalendarSnapshot{Events: []provider.CalendarEvent{
					mkCalEv("e1", "alex-personal", "Soccer Practice", "2026-09-27T16:00:00Z", "2026-09-27T17:00:00Z", false),
				}},
				Config: withFamilyCfg(map[string]any{"default_view": "month", "date": "2026-09-27"}),
			},
			wantContains: []string{
				"cf-view-switcher",
				"cf-view-month",
				`data-view="month" role="tab" aria-selected="true">Month</button>`,
				`data-view="week" role="tab" aria-selected="false">Week</button>`,
				"calendar-family-data",
				"MirrormereCalendarFamily.mount",
			},
			wantNotContains: []string{
				`data-view="day"`,
			},
		},
		{
			name:      "calendar-family view switcher day fallback to week",
			widgetDir: "calendar-family",
			ctx: Context{
				ID:         "test-switcher-day-fallback",
				Type:       "calendar-family",
				Dimensions: domain.NewDimension(6, 2),
				Data: provider.CalendarSnapshot{Events: []provider.CalendarEvent{
					mkCalEv("e1", "alex-personal", "Soccer Practice", "2026-09-27T16:00:00Z", "2026-09-27T17:00:00Z", false),
				}},
				Config: withFamilyCfg(map[string]any{"default_view": "day", "date": "2026-09-27"}),
			},
			wantContains: []string{
				"cf-view-week",
			},
			wantNotContains: []string{
				`data-view="day"`,
			},
		},
		{
			name:      "calendar-family week continuous spanning bars",
			widgetDir: "calendar-family",
			ctx: Context{
				ID:         "test-family-spanning-bars",
				Type:       "calendar-family",
				Dimensions: domain.NewDimension(6, 2),
				Data: provider.CalendarSnapshot{Events: []provider.CalendarEvent{
					mkCalEv("e_single", "alex-personal", "Town Hall", "2026-09-30", "2026-09-30", true),
					mkCalEv("e_multi", "alex-personal", "Camping Trip", "2026-09-28", "2026-09-30", true),
				}},
				Config: withFamilyCfg(map[string]any{"default_view": "week", "date": "2026-09-27"}),
			},
			wantContains: []string{
				"cf-week-allday-grid",
				"grid-column: 2 / span 3",
				"Camping Trip",
				`data-event-id="e_multi"`,
			},
		},
		{
			name:      "calendar-family week timeline",
			widgetDir: "calendar-family",
			ctx: Context{
				ID:         "test-family-timeline",
				Type:       "calendar-family",
				Dimensions: domain.NewDimension(6, 2),
				Data: provider.CalendarSnapshot{Events: []provider.CalendarEvent{
					mkCalEv("e1", "alex-personal", "Soccer Practice", "2026-09-27T16:00:00Z", "2026-09-27T17:00:00Z", false),
				}},
				Config: withFamilyCfg(map[string]any{"default_view": "week", "date": "2026-09-27", "hours": []any{"07:00", "21:00"}}),
			},
			wantContains: []string{
				"cf-week-body",
				"cf-week-grid",
				"cf-timeline-axis",
				"cf-timeline-markers",
				"cf-hour-marker",
				"cf-day-canvas",
				"Soccer Practice",
				"top: 64.29%",
			},
		},
		{
			name:      "calendar-feed alignment and hierarchy",
			widgetDir: "calendar-feed",
			ctx: Context{
				ID:         "test-cal-feed",
				Type:       "calendar-feed",
				Dimensions: domain.NewDimension(4, 3),
				Data: provider.CalendarSnapshot{
					LastSync: "2026-10-10T12:00:00Z",
					Events: []provider.CalendarEvent{
						{ID: "e1", Title: "Doctor Appointment", CalendarName: "Family", Color: "#ff6f59", Start: "2026-10-10T14:00:00Z", End: "2026-10-10T15:00:00Z", Location: "Medical Center Bldg B"},
						{ID: "e2", Title: "Team Sync", CalendarName: "Work", Color: "#9d8df1", Start: "2026-10-10T16:00:00Z", End: "2026-10-10T17:00:00Z"},
						{ID: "e3", Title: "No-Calendar Event", Start: "2026-10-10T18:00:00Z", End: "2026-10-10T19:00:00Z", Location: "Room 101"},
					},
				},
				Config: map[string]any{"title": "Upcoming", "__now": time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)},
			},
			wantContains: []string{
				`<div class="event-details-col">`,
				"Medical Center Bldg B",
				`class="event-calendar-pill"`,
				`class="event-accent-dot"`,
				"Doctor Appointment",
				"Team Sync",
				"No-Calendar Event",
				"Room 101",
			},
			wantOrdered: []string{
				`class="event-calendar-pill"`,
				`class="event-accent-dot"`,
			},
			wantDayCardsCount:  1,
			dayCardMustContain: []string{"Doctor Appointment", "Team Sync", "No-Calendar Event"},
		},
		{
			name:      "calendar-feed all-day sorting across timezones",
			widgetDir: "calendar-feed",
			ctx: Context{
				ID:         "test-cal-feed-tz",
				Type:       "calendar-feed",
				Dimensions: domain.NewDimension(4, 3),
				Data: provider.CalendarSnapshot{
					LastSync: "2026-10-10T12:00:00Z",
					Events: func() []provider.CalendarEvent {
						evs := []provider.CalendarEvent{
							{ID: "e-mon", Title: "Monday Evening Dinner", CalendarName: "Family", Color: "#3b82f6", Start: "2026-10-13T02:00:00Z", End: "2026-10-13T03:00:00Z"},
							{ID: "e-party", Title: "Weekend Bachelorette Party", CalendarName: "Family", Color: "#a855f7", Start: "2026-10-15", End: "2026-10-15", AllDay: true},
							{ID: "e-wed", Title: "Wednesday Game Night", CalendarName: "Family", Color: "#3b82f6", Start: "2026-10-15T02:00:00Z", End: "2026-10-15T04:00:00Z"},
							{ID: "e-brunch", Title: "Brunch with Bridesmaids", CalendarName: "Family", Color: "#a855f7", Start: "2026-10-15T17:00:00Z", End: "2026-10-15T18:30:00Z"},
						}
						provider.SortCalendarEvents(evs, laLoc)
						return evs
					}(),
				},
				Config: map[string]any{"title": "Upcoming", "__now": time.Date(2026, 10, 10, 12, 0, 0, 0, laLoc)},
			},
			wantOrdered: []string{
				"Monday Evening Dinner",
				"Wednesday Game Night",
				"Weekend Bachelorette Party",
				"Brunch with Bridesmaids",
			},
			wantContains: []string{
				"Thursday • Oct 15",
				"event-time-allday",
			},
			wantDayCardsCount:    3,
			thursCardMustContain: []string{"Weekend Bachelorette Party", "Brunch with Bridesmaids"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.funcMapCheck {
				fm := StandardFuncMap(func() time.Time { return testNow })
				switch tc.funcMapType {
				case "calendarFamilyDayView":
					fn, ok := fm["calendarFamilyDayView"].(func(any, map[string]any, domain.Dimension) (*FamilyDayViewModel, error))
					if !ok || fn == nil {
						t.Fatalf("expected calendarFamilyDayView in funcMap")
					}
					view, err := fn(provider.CalendarSnapshot{}, baseFamilyConfig(), domain.NewDimension(6, 2))
					if err != nil || view == nil {
						t.Fatalf("calendarFamilyDayView failed: %v", err)
					}
				case "dayViewAndMath":
					dayViewFn, ok := fm["dayView"].(func(data any, cfg map[string]any, dims domain.Dimension) (*FamilyDayViewModel, error))
					if !ok || dayViewFn == nil {
						t.Fatalf("expected dayView helper in funcMap")
					}
					cfg := baseFamilyConfig()
					cfg["date"] = "2026-09-27"
					v, err := dayViewFn(provider.CalendarSnapshot{}, cfg, domain.NewDimension(6, 2))
					if err != nil || v == nil || v.Date != "2026-09-27" {
						t.Fatalf("dayView helper failed: v=%+v, err=%v", v, err)
					}
					isDayViewFn, ok2 := fm["isDayView"].(func(cfg map[string]any) bool)
					if !ok2 || !isDayViewFn(map[string]any{"default_view": "day"}) || isDayViewFn(map[string]any{"default_view": "week"}) {
						t.Errorf("isDayView helper failed")
					}
					addFn, ok3 := fm["add"].(func(a, b int) int)
					subFn, ok4 := fm["sub"].(func(a, b int) int)
					if !ok3 || !ok4 || addFn(3, 4) != 7 || subFn(5, 2) != 3 {
						t.Errorf("add/sub helpers failed")
					}
				case "calendarFamilyMonthView":
					fn, ok := fm["calendarFamilyMonthView"].(func(any, map[string]any, domain.Dimension) (*FamilyMonthViewModel, error))
					if !ok || fn == nil {
						t.Fatalf("expected calendarFamilyMonthView in funcMap")
					}
					view, err := fn(provider.CalendarSnapshot{}, baseFamilyConfig(), domain.NewDimension(6, 2))
					if err != nil || view == nil {
						t.Fatalf("calendarFamilyMonthView failed: %v", err)
					}
				}
				return
			}

			html := renderWidgetTemplate(t, tc.widgetDir, tc.ctx)
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
			if len(tc.wantOrdered) > 1 {
				lastIdx := -1
				for _, s := range tc.wantOrdered {
					idx := strings.Index(html, s)
					if idx == -1 {
						t.Fatalf("expected substring %q in html", s)
					}
					if idx < lastIdx {
						t.Errorf("expected substring %q to appear after previous substring, got idx=%d < lastIdx=%d", s, idx, lastIdx)
					}
					lastIdx = idx
				}
			}
			if tc.wantDayCardsCount > 0 {
				cnt := strings.Count(html, `class="calendar-day-card"`)
				if cnt != tc.wantDayCardsCount {
					t.Errorf("calendar-day-card count = %d, want %d", cnt, tc.wantDayCardsCount)
				}
			}
			if len(tc.dayCardMustContain) > 0 {
				cardStart := strings.Index(html, `<div class="calendar-day-card">`)
				if cardStart != -1 {
					cardContent := html[cardStart:]
					for _, s := range tc.dayCardMustContain {
						if !strings.Contains(cardContent, s) {
							t.Errorf("expected day-card to contain %q", s)
						}
					}
				}
			}
			if len(tc.thursCardMustContain) > 0 {
				cards := strings.Split(html, `<div class="calendar-day-card">`)
				if len(cards) >= 4 {
					thursCard := cards[3]
					for _, s := range tc.thursCardMustContain {
						if !strings.Contains(thursCard, s) {
							t.Errorf("expected Thursday card to contain %q", s)
						}
					}
				}
			}
		})
	}
}

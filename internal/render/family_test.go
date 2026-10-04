package render

import (
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

// A fixed Sunday reference, matching week_starts: sunday.
var testNow = mustTimeStatic("2006-01-02", "2026-09-27")

func mustTimeStatic(layout, val string) time.Time {
	tm, err := time.Parse(layout, val)
	if err != nil {
		panic(err)
	}
	return tm
}

func TestBuildFamilyView_MemberResolutionAndUnclaimed(t *testing.T) {
	snap := provider.CalendarSnapshot{Events: []provider.CalendarEvent{
		{ID: "evt_1", CalendarName: "alex-personal", Title: "Dentist", Start: "2026-09-28T15:00:00Z", End: "2026-09-28T16:00:00Z"},
		{ID: "evt_2", CalendarName: "holidays", Title: "Rosh Hashanah", AllDay: true, Start: "2026-09-27", End: "2026-09-27"},
	}}

	dims := domain.NewDimension(6, 2)
	view, err := BuildFamilyView(snap, baseFamilyConfig(), dims, testNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(view.Members) != 2 || view.Members[0].Name != "Alex" || view.Members[1].Name != "Kid" {
		t.Fatalf("unexpected member order/resolution: %+v", view.Members)
	}

	var dentist, holiday *FamilyEvent
	for i := range view.Days {
		for j := range view.Days[i].Timed {
			if view.Days[i].Timed[j].ID == "evt_1" {
				dentist = &view.Days[i].Timed[j]
			}
		}
		for j := range view.Days[i].AllDay {
			if view.Days[i].AllDay[j].ID == "evt_2" {
				holiday = &view.Days[i].AllDay[j]
			}
		}
	}
	if dentist == nil {
		t.Fatal("expected dentist event in timed list")
	}
	if len(dentist.Owners) != 1 || dentist.Owners[0] != "Alex" || dentist.Color != "#E07A5F" {
		t.Fatalf("expected dentist owned by Alex: %+v", dentist)
	}
	if holiday == nil {
		t.Fatal("expected holiday event in all-day list")
	}
	if len(holiday.Owners) != 0 || holiday.Color != "#3D405B" {
		t.Fatalf("expected unclaimed holiday to use shared_color with no owner: %+v", holiday)
	}
}

func TestBuildFamilyView_SharedEventMergeByID(t *testing.T) {
	snap := provider.CalendarSnapshot{Events: []provider.CalendarEvent{
		{ID: "evt_shared_1", CalendarName: "school", Title: "Parent-teacher conf", Start: "2026-09-29T18:00:00Z", End: "2026-09-29T18:30:00Z"},
		{ID: "evt_shared_1", CalendarName: "alex-personal", Title: "Parent-teacher conf", Start: "2026-09-29T18:00:00Z", End: "2026-09-29T18:30:00Z"},
	}}

	dims := domain.NewDimension(6, 2)
	view, err := BuildFamilyView(snap, baseFamilyConfig(), dims, testNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var count int
	var owners []string
	for _, d := range view.Days {
		for _, ev := range d.Timed {
			if ev.ID == "evt_shared_1" {
				count++
				owners = ev.Owners
			}
		}
	}
	if count != 1 {
		t.Fatalf("expected shared event to render exactly once, rendered %d times", count)
	}
	if len(owners) != 2 || owners[0] != "Alex" || owners[1] != "Kid" {
		t.Fatalf("expected both owners in member order, got %v", owners)
	}
}

func TestBuildFamilyView_PrivateCalendarRedaction(t *testing.T) {
	snap := provider.CalendarSnapshot{Events: []provider.CalendarEvent{
		{ID: "evt_priv", CalendarName: "secret-work", Title: "1:1 with manager", Location: "Room 4", Description: "sensitive", Start: "2026-09-30T14:00:00Z", End: "2026-09-30T15:00:00Z"},
	}}

	dims := domain.NewDimension(6, 2)
	view, err := BuildFamilyView(snap, baseFamilyConfig(), dims, testNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var found bool
	for _, d := range view.Days {
		for _, ev := range d.Timed {
			if ev.ID == "evt_priv" {
				found = true
				if ev.Title != "Busy" || ev.Location != "" {
					t.Fatalf("expected private event redacted to Busy with no location, got %+v", ev)
				}
			}
		}
	}
	if !found {
		t.Fatal("expected private event to still appear (time+owner preserved)")
	}
}

func TestBuildFamilyView_WindowComputation(t *testing.T) {
	cases := []struct {
		name     string
		dims     domain.Dimension
		cfg      map[string]any
		wantDays int
	}{
		{"auto at 6x2 is 7 days", domain.NewDimension(6, 2), map[string]any{}, 7},
		{"auto at 4x2 is 5 days", domain.NewDimension(4, 2), map[string]any{}, 5},
		{"explicit days:3", domain.NewDimension(4, 2), map[string]any{"days": "3"}, 3},
		{"explicit days:7 at 4x2 allowed", domain.NewDimension(4, 2), map[string]any{"days": "7"}, 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := baseFamilyConfig()
			for k, v := range tc.cfg {
				cfg[k] = v
			}
			view, err := BuildFamilyView(provider.CalendarSnapshot{}, cfg, tc.dims, testNow)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(view.Days) != tc.wantDays {
				t.Fatalf("expected %d days, got %d", tc.wantDays, len(view.Days))
			}
		})
	}
}

func TestBuildFamilyView_WeekAlignsToWeekStart(t *testing.T) {
	// testNow is Sunday 2026-09-27; week_starts sunday should start the range on that same day.
	cfg := baseFamilyConfig()
	view, err := BuildFamilyView(provider.CalendarSnapshot{}, cfg, domain.NewDimension(6, 2), testNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if view.RangeStart != "2026-09-27" {
		t.Fatalf("expected range start 2026-09-27, got %s", view.RangeStart)
	}

	cfg["week_starts"] = "monday"
	view, err = BuildFamilyView(provider.CalendarSnapshot{}, cfg, domain.NewDimension(6, 2), testNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if view.RangeStart != "2026-09-21" {
		t.Fatalf("expected Monday-aligned range start 2026-09-21, got %s", view.RangeStart)
	}
}

func TestBuildFamilyView_OverflowMore(t *testing.T) {
	var events []provider.CalendarEvent
	for i := 0; i < 6; i++ {
		events = append(events, provider.CalendarEvent{
			ID:           "evt_overflow_" + string(rune('a'+i)),
			CalendarName: "alex-personal",
			Title:        "Busy block",
			Start:        "2026-09-27T0" + string(rune('0'+i)) + ":00:00Z",
			End:          "2026-09-27T09:00:00Z",
		})
	}
	dims := domain.NewDimension(4, 2) // maxVisible = 3
	view, err := BuildFamilyView(provider.CalendarSnapshot{Events: events}, baseFamilyConfig(), dims, testNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	today := view.Days[0]
	if len(today.Timed) != 3 {
		t.Fatalf("expected timed list truncated to 3, got %d", len(today.Timed))
	}
	if today.Overflow != 3 {
		t.Fatalf("expected overflow of 3, got %d", today.Overflow)
	}
}

func TestBuildFamilyView_TodayAndWeekendFlags(t *testing.T) {
	view, err := BuildFamilyView(provider.CalendarSnapshot{}, baseFamilyConfig(), domain.NewDimension(6, 2), testNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !view.Days[0].IsToday {
		t.Fatalf("expected first day (2026-09-27, the configured 'now') to be marked today")
	}
	// 2026-09-27 is a Sunday, 2026-10-03 is a Saturday: both ends of a sunday-start week are weekend.
	if !view.Days[0].IsWeekend {
		t.Fatalf("expected Sunday to be flagged weekend")
	}
	if !view.Days[6].IsWeekend {
		t.Fatalf("expected Saturday to be flagged weekend")
	}
	if view.Days[3].IsWeekend {
		t.Fatalf("expected a midweek day to not be flagged weekend")
	}
}

func TestBuildFamilyView_ContrastTextColor(t *testing.T) {
	if got := contrastTextColor("#FFFFFF"); got != "#000000" {
		t.Fatalf("expected white bg -> black text, got %s", got)
	}
	if got := contrastTextColor("#000000"); got != "#ffffff" {
		t.Fatalf("expected black bg -> white text, got %s", got)
	}
	if got := contrastTextColor("not-a-color"); got != "#ffffff" {
		t.Fatalf("expected malformed color to fall back to white text, got %s", got)
	}
}

func TestBuildFamilyView_MissingMemberColorErrors(t *testing.T) {
	cfg := map[string]any{
		"members": []any{
			map[string]any{"name": "Alex"},
		},
		"calendars": []any{},
	}
	if _, err := BuildFamilyView(provider.CalendarSnapshot{}, cfg, domain.NewDimension(6, 2), testNow); err == nil {
		t.Fatal("expected error for member missing color")
	}
}

func TestBuildFamilyView_DefaultInitialAndPattern(t *testing.T) {
	cfg := map[string]any{
		"members": []any{
			map[string]any{"name": "zoe", "color": "#123456", "calendars": []any{}},
		},
		"calendars": []any{},
	}
	view, err := BuildFamilyView(provider.CalendarSnapshot{}, cfg, domain.NewDimension(6, 2), testNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if view.Members[0].Initial != "Z" {
		t.Fatalf("expected default initial derived from name, got %q", view.Members[0].Initial)
	}
	if view.Members[0].Pattern != "solid" {
		t.Fatalf("expected default pattern 'solid', got %q", view.Members[0].Pattern)
	}
}

func TestBuildFamilyView_PointerSnapshotAndNilData(t *testing.T) {
	snap := &provider.CalendarSnapshot{Events: []provider.CalendarEvent{
		{ID: "evt_ptr", CalendarName: "alex-personal", Title: "Ptr test", Start: "2026-09-27T10:00:00Z", End: "2026-09-27T11:00:00Z"},
	}}
	view, err := BuildFamilyView(snap, baseFamilyConfig(), domain.NewDimension(6, 2), testNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(view.Days[0].Timed) != 1 {
		t.Fatalf("expected pointer snapshot to be accepted, got days[0]=%+v", view.Days[0])
	}

	view, err = BuildFamilyView(nil, baseFamilyConfig(), domain.NewDimension(6, 2), testNow)
	if err != nil {
		t.Fatalf("unexpected error for nil data: %v", err)
	}
	if view.Days[0].Timed != nil {
		t.Fatalf("expected empty timed list for nil data")
	}
}

func TestBuildFamilyView_MembersNotAList(t *testing.T) {
	cfg := map[string]any{"members": "not-a-list", "calendars": []any{}}
	view, err := BuildFamilyView(provider.CalendarSnapshot{}, cfg, domain.NewDimension(6, 2), testNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(view.Members) != 0 {
		t.Fatalf("expected no members when 'members' is not a list, got %+v", view.Members)
	}
}

func TestBuildFamilyView_MemberEntryNotAMap(t *testing.T) {
	cfg := map[string]any{"members": []any{"oops"}, "calendars": []any{}}
	if _, err := BuildFamilyView(provider.CalendarSnapshot{}, cfg, domain.NewDimension(6, 2), testNow); err == nil {
		t.Fatal("expected error for non-mapping member entry")
	}
}

func TestParseFamilyTime_Invalid(t *testing.T) {
	if _, ok := parseFamilyTime(""); ok {
		t.Fatal("expected empty string to fail parse")
	}
	if _, ok := parseFamilyTime("not-a-time"); ok {
		t.Fatal("expected garbage string to fail parse")
	}
}

func TestEventDateKey_UnparsableTimedFallsBackToRawStart(t *testing.T) {
	ev := provider.CalendarEvent{Start: "garbage"}
	if got := eventDateKey(ev, time.UTC); got != "garbage" {
		t.Fatalf("expected raw fallback, got %q", got)
	}
}

func TestResolveFamilyDayCount_NumericConfigValues(t *testing.T) {
	if got := resolveFamilyDayCount(map[string]any{"days": 3}, domain.NewDimension(6, 2)); got != 3 {
		t.Fatalf("expected int days:3 to resolve to 3, got %d", got)
	}
	if got := resolveFamilyDayCount(map[string]any{"days": float64(5)}, domain.NewDimension(4, 2)); got != 5 {
		t.Fatalf("expected float64 days:5 to resolve to 5, got %d", got)
	}
	if got := resolveFamilyDayCount(map[string]any{"days": "not-a-number"}, domain.NewDimension(6, 2)); got != 7 {
		t.Fatalf("expected unparsable string days to fall back to auto(7), got %d", got)
	}
	if got := resolveFamilyDayCount(map[string]any{"days": 0}, domain.NewDimension(4, 2)); got != 5 {
		t.Fatalf("expected non-positive int days to fall back to auto(5), got %d", got)
	}
	if got := resolveFamilyDayCount(nil, domain.NewDimension(6, 2)); got != 7 {
		t.Fatalf("expected nil config to fall back to auto(7), got %d", got)
	}
}

func TestStringOr_NonStringValueFallsBack(t *testing.T) {
	if got := stringOr(map[string]any{"k": 42}, "k", "fallback"); got != "fallback" {
		t.Fatalf("expected non-string value to fall back, got %q", got)
	}
	if got := stringOr(nil, "k", "fallback"); got != "fallback" {
		t.Fatalf("expected nil config to fall back, got %q", got)
	}
}

func TestParseFamilyCalendarSources_MalformedEntriesSkipped(t *testing.T) {
	cfg := map[string]any{
		"calendars": []any{
			"not-a-map",
			map[string]any{"name": ""},
			map[string]any{"name": "  "},
			map[string]any{"name": "valid", "private": true},
		},
	}
	out := parseFamilyCalendarSources(cfg)
	if len(out) != 1 {
		t.Fatalf("expected only 1 valid calendar source, got %d: %+v", len(out), out)
	}
	if !out["valid"].private {
		t.Fatalf("expected 'valid' source to be private")
	}
}

func TestParseHexColor_InvalidInputs(t *testing.T) {
	if _, _, _, ok := parseHexColor("#fff"); ok {
		t.Fatal("expected short hex to be rejected")
	}
	if _, _, _, ok := parseHexColor("#gggggg"); ok {
		t.Fatal("expected non-hex digits to be rejected")
	}
	if r, g, b, ok := parseHexColor("#A855F7"); !ok || r != 0xA8 || g != 0x55 || b != 0xF7 {
		t.Fatalf("expected valid hex to parse, got r=%d g=%d b=%d ok=%v", r, g, b, ok)
	}
}

func TestBuildFamilyView_MemberCalendarsNotAList(t *testing.T) {
	cfg := map[string]any{
		"members": []any{
			map[string]any{"name": "Alex", "color": "#E07A5F", "calendars": "not-a-list"},
		},
		"calendars": []any{},
	}
	view, err := BuildFamilyView(provider.CalendarSnapshot{}, cfg, domain.NewDimension(6, 2), testNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(view.Members[0].Calendars) != 0 {
		t.Fatalf("expected no claimed calendars, got %+v", view.Members[0].Calendars)
	}
}

func TestBuildFamilyView_TimezoneConvertsToLocalDay(t *testing.T) {
	// A fixed-offset zone (no tzdata dependency): PDT, UTC-7.
	loc := time.FixedZone("PDT", -7*3600)
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, loc) // "today" is Sep 27 in this location.

	// 05:30 UTC on Sep 28 is 22:30 local on Sep 27 - a UTC event near midnight that must land
	// on the *local* day (Sep 27), not the UTC day (Sep 28), "Timezones".
	snap := provider.CalendarSnapshot{Events: []provider.CalendarEvent{
		{ID: "evt_tz", CalendarName: "alex-personal", Title: "Late call", Start: "2026-09-28T05:30:00Z", End: "2026-09-28T06:00:00Z"},
	}}

	view, err := BuildFamilyView(snap, baseFamilyConfig(), domain.NewDimension(6, 2), now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var found *FamilyEvent
	var foundDate string
	for _, d := range view.Days {
		for i, ev := range d.Timed {
			if ev.ID == "evt_tz" {
				found = &d.Timed[i]
				foundDate = d.Date
			}
		}
	}
	if found == nil {
		t.Fatal("expected the timezone-converted event to appear in the timed list")
	}
	if foundDate != "2026-09-27" {
		t.Fatalf("expected event to land on local day 2026-09-27, landed on %s", foundDate)
	}
	if found.Start != "22:30" {
		t.Fatalf("expected local start time 22:30, got %q", found.Start)
	}
}

func TestBuildFamilyView_ContrastTextColorBoundToEvent(t *testing.T) {
	view, err := BuildFamilyView(
		provider.CalendarSnapshot{Events: []provider.CalendarEvent{
			{ID: "evt_light", CalendarName: "school", Title: "Light bg", Start: "2026-09-27T15:00:00Z", End: "2026-09-27T16:00:00Z"},
		}},
		baseFamilyConfig(), domain.NewDimension(6, 2), testNow,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var light *FamilyEvent
	for i := range view.Days[0].Timed {
		if view.Days[0].Timed[i].ID == "evt_light" {
			light = &view.Days[0].Timed[i]
		}
	}
	if light == nil {
		t.Fatal("expected evt_light in today's timed list")
	}
	// Kid's color #81B29A is light enough for black text.
	if light.Color != "#81B29A" || light.TextColor != "#000000" {
		t.Fatalf("expected light bg #81B29A -> black text, got color=%s text=%s", light.Color, light.TextColor)
	}
}

func TestBuildFamilyView_UnclaimedEventDefaultTextColor(t *testing.T) {
	snap := provider.CalendarSnapshot{Events: []provider.CalendarEvent{
		{ID: "evt_2", CalendarName: "holidays", Title: "Rosh Hashanah", AllDay: true, Start: "2026-09-27", End: "2026-09-27"},
	}}
	view, err := BuildFamilyView(snap, baseFamilyConfig(), domain.NewDimension(6, 2), testNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var holiday *FamilyEvent
	for i := range view.Days[0].AllDay {
		if view.Days[0].AllDay[i].ID == "evt_2" {
			holiday = &view.Days[0].AllDay[i]
		}
	}
	if holiday == nil {
		t.Fatal("expected holiday in today's all-day list")
	}
	// Default shared_color #3D405B is dark: expect white text and no owner pattern.
	if holiday.TextColor != "#ffffff" {
		t.Fatalf("expected dark shared_color -> white text, got %q", holiday.TextColor)
	}
	if holiday.Pattern != "" {
		t.Fatalf("expected unclaimed event to carry no pattern, got %q", holiday.Pattern)
	}
}

func TestBuildFamilyView_MultiDayAllDayEventSpansEveryColumn(t *testing.T) {
	// testNow is Sunday 2026-09-27. A 3-day all-day event Sep 28-30 (inclusive) must appear on
	// all three of its own columns, "Multi-day events".
	snap := provider.CalendarSnapshot{Events: []provider.CalendarEvent{
		{ID: "evt_multi", CalendarName: "alex-personal", Title: "Camping trip", AllDay: true, Start: "2026-09-28", End: "2026-09-30"},
	}}
	view, err := BuildFamilyView(snap, baseFamilyConfig(), domain.NewDimension(6, 2), testNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantDates := map[string]bool{"2026-09-28": false, "2026-09-29": false, "2026-09-30": false}
	for _, d := range view.Days {
		for _, ev := range d.AllDay {
			if ev.ID == "evt_multi" {
				if _, ok := wantDates[d.Date]; !ok {
					t.Fatalf("multi-day event appeared on unexpected date %s", d.Date)
				}
				wantDates[d.Date] = true
			}
		}
	}
	for date, seen := range wantDates {
		if !seen {
			t.Fatalf("expected multi-day event to appear on %s", date)
		}
	}
	// And must NOT appear on the day before its start or after its end.
	for _, d := range view.Days {
		if d.Date == "2026-09-27" || d.Date == "2026-10-01" {
			for _, ev := range d.AllDay {
				if ev.ID == "evt_multi" {
					t.Fatalf("multi-day event must not appear outside its span, found on %s", d.Date)
				}
			}
		}
	}
}

func TestBuildFamilyView_MultiDayEventSpanningIntoWindowFromBefore(t *testing.T) {
	// An event that starts before the displayed window but ends inside it must still appear on
	// every in-window day it covers, rather than being dropped because its start day (the old
	// single-date lookup) falls outside dayIndex.
	snap := provider.CalendarSnapshot{Events: []provider.CalendarEvent{
		{ID: "evt_early_start", CalendarName: "alex-personal", Title: "Started earlier", AllDay: true, Start: "2026-09-20", End: "2026-09-28"},
	}}
	view, err := BuildFamilyView(snap, baseFamilyConfig(), domain.NewDimension(6, 2), testNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var foundOn27, foundOn28 bool
	for _, d := range view.Days {
		for _, ev := range d.AllDay {
			if ev.ID == "evt_early_start" {
				if d.Date == "2026-09-27" {
					foundOn27 = true
				}
				if d.Date == "2026-09-28" {
					foundOn28 = true
				}
			}
		}
	}
	if !foundOn27 || !foundOn28 {
		t.Fatalf("expected event spanning into the window to appear on both in-window days, foundOn27=%v foundOn28=%v", foundOn27, foundOn28)
	}
}

func TestBuildFamilyView_RangeLabelCrossesMonth(t *testing.T) {
	// 2026-09-27 is a Sunday; the Sunday-start week ends 2026-10-03, crossing month boundary.
	view, err := BuildFamilyView(provider.CalendarSnapshot{}, baseFamilyConfig(), domain.NewDimension(6, 2), testNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if view.RangeLabel != "Sep 27 – Oct 3" {
		t.Fatalf("expected cross-month range label, got %q", view.RangeLabel)
	}
}

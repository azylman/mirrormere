package render

import (
	"os"
	"bytes"
	"html/template"
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


func TestParseHoursBand(t *testing.T) {
	t.Run("defaults when nil or empty", func(t *testing.T) {
		band := ParseHoursBand(nil)
		if band.StartStr != "07:00" || band.EndStr != "21:00" || band.StartHour != 7 || band.EndHour != 21 {
			t.Fatalf("expected 07:00-21:00 default, got %+v", band)
		}
		band2 := ParseHoursBand(map[string]any{"hours": []any{}})
		if band2.StartStr != "07:00" || band2.EndStr != "21:00" {
			t.Fatalf("expected default on empty hours array, got %+v", band2)
		}
	})

	t.Run("valid custom []any and []string", func(t *testing.T) {
		band := ParseHoursBand(map[string]any{"hours": []any{"08:30", "19:45"}})
		if band.StartHour != 8 || band.StartMinute != 30 || band.EndHour != 19 || band.EndMinute != 45 {
			t.Fatalf("unexpected parsed hours: %+v", band)
		}
		if band.StartStr != "08:30" || band.EndStr != "19:45" {
			t.Fatalf("unexpected strings: %+v", band)
		}

		bandStr := ParseHoursBand(map[string]any{"hours": []string{"06:00", "22:00"}})
		if bandStr.StartHour != 6 || bandStr.EndHour != 22 {
			t.Fatalf("expected []string support, got %+v", bandStr)
		}
	})

	t.Run("hour without minutes supported", func(t *testing.T) {
		band := ParseHoursBand(map[string]any{"hours": []any{"8", "20"}})
		if band.StartHour != 8 || band.EndHour != 20 {
			t.Fatalf("expected 8 to 20, got %+v", band)
		}
	})

	t.Run("invalid formats fall back to default", func(t *testing.T) {
		cases := []any{
			[]any{"invalid", "21:00"},
			[]any{"07:00", "invalid"},
			[]any{"-1:00", "20:00"},
			[]any{"07:00", "25:00"},
			[]any{"24:01", "24:00"},
			[]any{"07:65", "20:00"},
			[]any{"07:00:00", "20:00"},
			[]any{"", "21:00"},
			[]any{"22:00", "06:00"}, // inverted
			[]any{"10:00", "10:00"}, // equal
			[]any{123, 456},          // non-string
		}
		for _, c := range cases {
			b := ParseHoursBand(map[string]any{"hours": c})
			if b.StartStr != "07:00" || b.EndStr != "21:00" {
				t.Fatalf("expected default for %v, got %+v", c, b)
			}
		}
	})

	t.Run("hour markers generation with fractional starts and ends", func(t *testing.T) {
		band := ParseHoursBand(map[string]any{"hours": []any{"07:30", "10:15"}})
		want := []string{"07:30", "08:00", "09:00", "10:00", "10:15"}
		if len(band.Markers) != len(want) {
			t.Fatalf("expected %d markers %v, got %d markers %v", len(want), want, len(band.Markers), band.Markers)
		}
		for i, m := range want {
			if band.Markers[i] != m {
				t.Errorf("marker %d: want %s, got %s", i, m, band.Markers[i])
			}
		}
	})
}

func TestComputeDayEventPosition(t *testing.T) {
	day := testNow // 2026-09-27
	band := defaultHoursBand() // 07:00 to 21:00 (14 hours = 840 mins)

	t.Run("normal event fully inside window", func(t *testing.T) {
		start := mustTimeStatic("2006-01-02 15:04", "2026-09-27 09:00")
		end := mustTimeStatic("2006-01-02 15:04", "2026-09-27 10:30") // 90 min duration, offset 120 min
		top, height, early, late := ComputeDayEventPosition(start, end, day, band)
		if early || late {
			t.Fatalf("expected in-window event to not be edge, early=%v late=%v", early, late)
		}
		wantTop := roundToTwoDecimals((120.0 / 840.0) * 100.0)
		wantHeight := roundToTwoDecimals((90.0 / 840.0) * 100.0)
		if top != wantTop || height != wantHeight {
			t.Fatalf("want top=%v height=%v, got top=%v height=%v", wantTop, wantHeight, top, height)
		}
	})

	t.Run("out of bounds early collapses to early edge band", func(t *testing.T) {
		start := mustTimeStatic("2006-01-02 15:04", "2026-09-27 05:00")
		end := mustTimeStatic("2006-01-02 15:04", "2026-09-27 06:30")
		top, height, early, late := ComputeDayEventPosition(start, end, day, band)
		if !early || late {
			t.Fatalf("expected early edge only, got early=%v late=%v", early, late)
		}
		if top != 0.0 || height != 0.0 {
			t.Fatalf("expected 0 top/height for completely out of bounds, got top=%v height=%v", top, height)
		}
	})

	t.Run("out of bounds late collapses to late edge band", func(t *testing.T) {
		start := mustTimeStatic("2006-01-02 15:04", "2026-09-27 21:30")
		end := mustTimeStatic("2006-01-02 15:04", "2026-09-27 23:00")
		top, height, early, late := ComputeDayEventPosition(start, end, day, band)
		if early || !late {
			t.Fatalf("expected late edge only, got early=%v late=%v", early, late)
		}
		if top != 100.0 || height != 0.0 {
			t.Fatalf("expected top=100 height=0, got top=%v height=%v", top, height)
		}
	})

	t.Run("early event spanning into window is clamped to top 0", func(t *testing.T) {
		start := mustTimeStatic("2006-01-02 15:04", "2026-09-27 06:00")
		end := mustTimeStatic("2006-01-02 15:04", "2026-09-27 08:30") // 90 min inside window (07:00-08:30)
		top, height, early, late := ComputeDayEventPosition(start, end, day, band)
		if !early || late {
			t.Fatalf("expected early edge, got early=%v late=%v", early, late)
		}
		wantHeight := roundToTwoDecimals((90.0 / 840.0) * 100.0)
		if top != 0.0 || height != wantHeight {
			t.Fatalf("expected top=0 height=%v, got top=%v height=%v", wantHeight, top, height)
		}
	})

	t.Run("late event spanning out of window is clamped to bottom", func(t *testing.T) {
		start := mustTimeStatic("2006-01-02 15:04", "2026-09-27 20:00") // 60 min inside window (20:00-21:00)
		end := mustTimeStatic("2006-01-02 15:04", "2026-09-27 23:00")
		top, height, early, late := ComputeDayEventPosition(start, end, day, band)
		if early || !late {
			t.Fatalf("expected late edge, got early=%v late=%v", early, late)
		}
		wantTop := roundToTwoDecimals((780.0 / 840.0) * 100.0)
		wantHeight := roundToTwoDecimals((60.0 / 840.0) * 100.0)
		if top != wantTop || height != wantHeight {
			t.Fatalf("expected top=%v height=%v, got top=%v height=%v", wantTop, wantHeight, top, height)
		}
	})

	t.Run("midnight crossing: event spans across 2 days", func(t *testing.T) {
		start := mustTimeStatic("2006-01-02 15:04", "2026-09-27 22:00")
		end := mustTimeStatic("2006-01-02 15:04", "2026-09-28 02:00")

		// On Day 1 (2026-09-27): starts after 21:00, collapses into late edge
		top1, height1, early1, late1 := ComputeDayEventPosition(start, end, day, band)
		if early1 || !late1 || top1 != 100.0 || height1 != 0.0 {
			t.Fatalf("Day 1: expected late edge collapse, got early=%v late=%v top=%v height=%v", early1, late1, top1, height1)
		}

		// On Day 2 (2026-09-28): ends at 02:00, collapses into early edge
		day2 := day.AddDate(0, 0, 1)
		top2, height2, early2, late2 := ComputeDayEventPosition(start, end, day2, band)
		if !early2 || late2 || top2 != 0.0 || height2 != 0.0 {
			t.Fatalf("Day 2: expected early edge collapse, got early=%v late=%v top=%v height=%v", early2, late2, top2, height2)
		}
	})

	t.Run("short event minimum height and clamping", func(t *testing.T) {
		start := mustTimeStatic("2006-01-02 15:04", "2026-09-27 09:00")
		end := mustTimeStatic("2006-01-02 15:04", "2026-09-27 09:02") // 2 min
		_, height, _, _ := ComputeDayEventPosition(start, end, day, band)
		if height < 2.0 {
			t.Fatalf("expected minimum height >= 2.0%%, got %v", height)
		}

		// Edge clamping near bottom
		startNearBottom := mustTimeStatic("2006-01-02 15:04", "2026-09-27 20:59")
		endNearBottom := mustTimeStatic("2006-01-02 15:04", "2026-09-27 21:00")
		topNear, heightNear, _, _ := ComputeDayEventPosition(startNearBottom, endNearBottom, day, band)
		if topNear+heightNear > 100.0 {
			t.Fatalf("expected top + height <= 100%%, got %v", topNear+heightNear)
		}
	})

	t.Run("fallback on inverted or zero band duration", func(t *testing.T) {
		start := mustTimeStatic("2006-01-02 15:04", "2026-09-27 09:00")
		end := mustTimeStatic("2006-01-02 15:04", "2026-09-27 10:00")
		badBand := HoursBand{StartHour: 20, EndHour: 10}
		top, height, _, _ := ComputeDayEventPosition(start, end, day, badBand)
		if top == 0.0 && height == 0.0 {
			t.Fatalf("expected fallback to 07:00-21:00 on invalid band duration")
		}
	})

	t.Run("end before start defaults to 30 min duration", func(t *testing.T) {
		start := mustTimeStatic("2006-01-02 15:04", "2026-09-27 09:00")
		end := mustTimeStatic("2006-01-02 15:04", "2026-09-27 08:00")
		_, height, _, _ := ComputeDayEventPosition(start, end, day, band)
		wantHeight := roundToTwoDecimals((30.0 / 840.0) * 100.0)
		if height != wantHeight {
			t.Fatalf("expected 30m default duration height %v, got %v", wantHeight, height)
		}
	})
}

func TestTruncateString(t *testing.T) {
	if got := truncateString("short", 10); got != "short" {
		t.Fatalf("expected unchanged short string, got %q", got)
	}
	long := strings.Repeat("a", 300)
	got := truncateString(long, 280)
	if len([]rune(got)) != 280 || !strings.HasSuffix(got, "…") {
		t.Fatalf("expected 280 runes ending in ellipsis, got len=%d, val=%q", len([]rune(got)), got)
	}
	emojis := strings.Repeat("🎉", 10)
	gotEmoji := truncateString(emojis, 5)
	if len([]rune(gotEmoji)) != 5 || !strings.HasSuffix(gotEmoji, "…") {
		t.Fatalf("expected 5 runes of emojis, got len=%d, val=%q", len([]rune(gotEmoji)), gotEmoji)
	}
	if got1 := truncateString("abc", 1); got1 != "a" {
		t.Fatalf("expected maxRunes=1 to return single rune, got %q", got1)
	}
}

func TestBuildFamilyDayView_Comprehensive(t *testing.T) {
	snap := provider.CalendarSnapshot{Events: []provider.CalendarEvent{
		{ID: "evt_timed_alex", CalendarName: "alex-personal", Title: "Alex Meeting", Description: "Discuss Q4 architecture goals", Start: "2026-09-27T10:00:00Z", End: "2026-09-27T11:00:00Z"},
		{ID: "evt_shared_holiday", CalendarName: "holidays", Title: "Rosh Hashanah", AllDay: true, Start: "2026-09-27", End: "2026-09-27"},
		{ID: "evt_unclaimed_timed", CalendarName: "holidays", Title: "Town Parade", Start: "2026-09-27T14:00:00Z", End: "2026-09-27T15:30:00Z"},
		{ID: "evt_private", CalendarName: "secret-work", Title: "Confidential 1:1", Description: "Secret notes", Start: "2026-09-27T16:00:00Z", End: "2026-09-27T17:00:00Z"},
		{ID: "evt_early", CalendarName: "alex-personal", Title: "Dawn Run", Start: "2026-09-27T05:30:00Z", End: "2026-09-27T06:30:00Z"},
		{ID: "evt_late", CalendarName: "alex-personal", Title: "Late Movie", Start: "2026-09-27T22:00:00Z", End: "2026-09-27T23:30:00Z"},
	}}

	cfg := baseFamilyConfig()
	cfg["hours"] = []any{"07:00", "21:00"}
	cfg["date"] = "2026-09-27"
	dims := domain.NewDimension(6, 2)

	view, err := BuildFamilyDayView(snap, cfg, dims, testNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if view.Date != "2026-09-27" || view.WeekdayAbbr != "Sun" || view.DayOfMonth != 27 {
		t.Fatalf("unexpected day metadata: %+v", view)
	}
	if !view.IsToday || !view.IsWeekend {
		t.Fatalf("expected IsToday and IsWeekend to be true")
	}
	if len(view.AllDayEvents) != 1 || view.AllDayEvents[0].Title != "Rosh Hashanah" {
		t.Fatalf("expected Rosh Hashanah in all-day events, got %+v", view.AllDayEvents)
	}

	// Columns should include Alex, Kid, plus Shared (since unclaimed event exists)
	if len(view.Columns) != 3 {
		t.Fatalf("expected 3 columns (Alex, Kid, Shared), got %d", len(view.Columns))
	}

	alexCol := view.Columns[0]
	if alexCol.Name != "Alex" {
		t.Fatalf("expected Alex column first, got %q", alexCol.Name)
	}
	if len(alexCol.Events) == 0 {
		t.Fatalf("expected timed events in Alex column")
	}
	if alexCol.Events[0].Description != "Discuss Q4 architecture goals" {
		t.Fatalf("expected description preserved on Alex event, got %q", alexCol.Events[0].Description)
	}
	if len(alexCol.EarlyEvents) != 1 || alexCol.EarlyEvents[0].Title != "Dawn Run" {
		t.Fatalf("expected Dawn Run in EarlyEvents, got %+v", alexCol.EarlyEvents)
	}
	if len(alexCol.LateEvents) != 1 || alexCol.LateEvents[0].Title != "Late Movie" {
		t.Fatalf("expected Late Movie in LateEvents, got %+v", alexCol.LateEvents)
	}

	sharedCol := view.Columns[2]
	if !sharedCol.IsShared || sharedCol.Name != "Shared" {
		t.Fatalf("expected third column to be Shared, got %+v", sharedCol)
	}
	if len(sharedCol.Events) != 2 || sharedCol.Events[0].Title != "Town Parade" || sharedCol.Events[1].Title != "Busy" {
		t.Fatalf("expected Town Parade and Busy in shared column, got %+v", sharedCol.Events)
	}

	// Verify private calendar redaction
	var privEvent *FamilyDayEvent
	for _, col := range view.Columns {
		for _, ev := range col.Events {
			if ev.ID == "evt_private" {
				privEvent = &ev
			}
		}
	}
	if privEvent == nil {
		t.Fatalf("expected private event in view")
	}
	if privEvent.Title != "Busy" || privEvent.Description != "Busy" || privEvent.Location != "" {
		t.Fatalf("expected private event fully redacted, got %+v", privEvent)
	}

	// Error on invalid members config
	badCfg := map[string]any{"members": []any{"not-a-map"}}
	_, err = BuildFamilyDayView(snap, badCfg, dims, testNow)
	if err == nil {
		t.Fatalf("expected error on invalid config")
	}
}

func TestHelpers_CalendarFamilyDayView(t *testing.T) {
	fm := StandardFuncMap(time.Now)
	fn, ok := fm["calendarFamilyDayView"].(func(any, map[string]any, domain.Dimension) (*FamilyDayViewModel, error))
	if !ok {
		t.Fatalf("expected calendarFamilyDayView in funcMap")
	}
	view, err := fn(provider.CalendarSnapshot{}, baseFamilyConfig(), domain.NewDimension(6, 2))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if view == nil {
		t.Fatalf("expected non-nil view")
	}
}

func TestMaxMemberColumns(t *testing.T) {
	t.Parallel()
	tests := []struct {
		cols int
		rows int
		want int
	}{
		{cols: 6, rows: 2, want: 8},
		{cols: 8, rows: 2, want: 8},
		{cols: 4, rows: 2, want: 3},
		{cols: 3, rows: 2, want: 3},
		{cols: 0, rows: 0, want: 3},
	}
	for _, tc := range tests {
		got := maxMemberColumns(domain.NewDimension(tc.cols, tc.rows))
		if got != tc.want {
			t.Errorf("maxMemberColumns(%dx%d) = %d; want %d", tc.cols, tc.rows, got, tc.want)
		}
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

func TestBuildFamilyDayView_ColumnCapacityAndAdaptation(t *testing.T) {
	t.Parallel()

	// 1. 6x2: all 5 members get their own column.
	t.Run("6x2 allows all 5 member columns", func(t *testing.T) {
		t.Parallel()
		cfg := fiveMemberConfig()
		snap := provider.CalendarSnapshot{Events: []provider.CalendarEvent{
			{ID: "e1", CalendarName: "alex-cal", Title: "Alex Job", Start: "2026-09-27T10:00:00Z", End: "2026-09-27T11:00:00Z"},
			{ID: "e2", CalendarName: "grandma-cal", Title: "Knitting", Start: "2026-09-27T14:00:00Z", End: "2026-09-27T15:00:00Z"},
		}}
		view, err := BuildFamilyDayView(snap, cfg, domain.NewDimension(6, 2), testNow)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(view.Columns) != 5 {
			t.Fatalf("expected 5 columns on 6x2, got %d", len(view.Columns))
		}
		expectedNames := []string{"Alex", "Kid", "Partner", "Grandma", "Grandpa"}
		for i, name := range expectedNames {
			if view.Columns[i].Name != name || view.Columns[i].IsShared {
				t.Errorf("column %d expected %s, got %+v", i, name, view.Columns[i])
			}
		}
		// Grandma is column 3, knitting event should be in Grandma's column
		if len(view.Columns[3].Events) != 1 || view.Columns[3].Events[0].Title != "Knitting" {
			t.Fatalf("expected Knitting event in Grandma's column (idx 3), got %+v", view.Columns[3].Events)
		}
	})

	// 2. 4x2: only 3 member columns (Alex, Kid, Partner). Grandma and Grandpa collapse.
	t.Run("4x2 collapses remaining members into shared column with colored initials", func(t *testing.T) {
		t.Parallel()
		cfg := fiveMemberConfig()
		snap := provider.CalendarSnapshot{Events: []provider.CalendarEvent{
			{ID: "e1", CalendarName: "alex-cal", Title: "Alex Job", Start: "2026-09-27T10:00:00Z", End: "2026-09-27T11:00:00Z"},
			{ID: "e2", CalendarName: "grandma-cal", Title: "Knitting", Start: "2026-09-27T14:00:00Z", End: "2026-09-27T15:00:00Z"},
		}}
		view, err := BuildFamilyDayView(snap, cfg, domain.NewDimension(4, 2), testNow)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Expect 3 member columns + 1 Shared column (total 4)
		if len(view.Columns) != 4 {
			t.Fatalf("expected 4 columns on 4x2 (3 members + 1 shared), got %d", len(view.Columns))
		}
		expectedNames := []string{"Alex", "Kid", "Partner", "Shared"}
		for i, name := range expectedNames {
			if view.Columns[i].Name != name {
				t.Errorf("column %d expected %s, got %s", i, name, view.Columns[i].Name)
			}
		}

		sharedCol := view.Columns[3]
		if !sharedCol.IsShared {
			t.Errorf("expected 4th column to be IsShared=true")
		}
		if len(sharedCol.Events) != 1 {
			t.Fatalf("expected 1 event in shared column, got %d", len(sharedCol.Events))
		}
		gEvent := sharedCol.Events[0]
		if gEvent.Title != "Knitting" {
			t.Errorf("expected Knitting in shared column, got %s", gEvent.Title)
		}
		// Check that colored owner initials are preserved for collapsed member
		if len(gEvent.Initials) != 1 || gEvent.Initials[0] != "G" {
			t.Errorf("expected initial 'G' on collapsed event, got %+v", gEvent.Initials)
		}
		if gEvent.Color != "#F59E0B" {
			t.Errorf("expected Grandma color #F59E0B on event, got %s", gEvent.Color)
		}
		if gEvent.ColSpan != 1 || gEvent.IsLinked {
			t.Errorf("expected ColSpan=1 and IsLinked=false for collapsed single member event, got span=%d linked=%v", gEvent.ColSpan, gEvent.IsLinked)
		}
	})

	// 3. 4x2 with no events for collapsed members and no unclaimed events -> Shared column omitted
	t.Run("4x2 omits shared column when no collapsed member events and no unclaimed events", func(t *testing.T) {
		t.Parallel()
		cfg := fiveMemberConfig()
		snap := provider.CalendarSnapshot{Events: []provider.CalendarEvent{
			{ID: "e1", CalendarName: "alex-cal", Title: "Alex Job", Start: "2026-09-27T10:00:00Z", End: "2026-09-27T11:00:00Z"},
			{ID: "e2", CalendarName: "kid-cal", Title: "Soccer Practice", Start: "2026-09-27T15:00:00Z", End: "2026-09-27T16:00:00Z"},
		}}
		view, err := BuildFamilyDayView(snap, cfg, domain.NewDimension(4, 2), testNow)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(view.Columns) != 3 {
			t.Fatalf("expected exactly 3 columns (Shared omitted), got %d", len(view.Columns))
		}
		for _, col := range view.Columns {
			if col.IsShared {
				t.Errorf("unexpected shared column in view: %+v", col)
			}
		}
	})
}

func TestBuildFamilyDayView_AdjacentColumnSpanning(t *testing.T) {
	t.Parallel()

	cfg := fiveMemberConfig()
	dims := domain.NewDimension(6, 2)

	// Event 1: Shared by Alex (col 0) and Kid (col 1) -> adjacent, ColSpan=2
	// Event 2: Shared by Kid (col 1), Partner (col 2), and Grandma (col 3) -> adjacent, ColSpan=3
	snap := provider.CalendarSnapshot{Events: []provider.CalendarEvent{
		{ID: "ev_adj_2", CalendarName: "alex-cal", Title: "Family Breakfast", Start: "2026-09-27T08:30:00Z", End: "2026-09-27T09:30:00Z"},
		{ID: "ev_adj_2", CalendarName: "kid-cal", Title: "Family Breakfast", Start: "2026-09-27T08:30:00Z", End: "2026-09-27T09:30:00Z"},
		{ID: "ev_adj_3", CalendarName: "kid-cal", Title: "Park Trip", Start: "2026-09-27T13:00:00Z", End: "2026-09-27T15:00:00Z"},
		{ID: "ev_adj_3", CalendarName: "partner-cal", Title: "Park Trip", Start: "2026-09-27T13:00:00Z", End: "2026-09-27T15:00:00Z"},
		{ID: "ev_adj_3", CalendarName: "grandma-cal", Title: "Park Trip", Start: "2026-09-27T13:00:00Z", End: "2026-09-27T15:00:00Z"},
	}}

	view, err := BuildFamilyDayView(snap, cfg, dims, testNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	alexCol := view.Columns[0]
	kidCol := view.Columns[1]
	partnerCol := view.Columns[2]
	grandmaCol := view.Columns[3]

	// Alex column should have "Family Breakfast" with ColSpan: 2, IsLinked: false
	if len(alexCol.Events) != 1 {
		t.Fatalf("expected 1 event in Alex column, got %d", len(alexCol.Events))
	}
	evBreakfast := alexCol.Events[0]
	if evBreakfast.Title != "Family Breakfast" || evBreakfast.ColSpan != 2 || evBreakfast.IsLinked || evBreakfast.LinkGlyph != "" {
		t.Fatalf("expected ColSpan=2, IsLinked=false, LinkGlyph='' on Breakfast, got %+v", evBreakfast)
	}

	// Kid column should NOT have "Family Breakfast", but SHOULD have "Park Trip" with ColSpan: 3
	if len(kidCol.Events) != 1 {
		t.Fatalf("expected 1 event in Kid column, got %d", len(kidCol.Events))
	}
	evPark := kidCol.Events[0]
	if evPark.Title != "Park Trip" || evPark.ColSpan != 3 || evPark.IsLinked || evPark.LinkGlyph != "" {
		t.Fatalf("expected ColSpan=3, IsLinked=false on Park Trip in Kid col, got %+v", evPark)
	}

	// Partner and Grandma columns should have 0 events because Park Trip spans from Kid's column
	if len(partnerCol.Events) != 0 {
		t.Errorf("expected 0 events in Partner column (spanned from Kid), got %d", len(partnerCol.Events))
	}
	if len(grandmaCol.Events) != 0 {
		t.Errorf("expected 0 events in Grandma column (spanned from Kid), got %d", len(grandmaCol.Events))
	}
}

func TestBuildFamilyDayView_NonAdjacentSharedEvents(t *testing.T) {
	t.Parallel()

	t.Run("6x2 non-adjacent members duplicate with link glyph", func(t *testing.T) {
		t.Parallel()
		cfg := fiveMemberConfig()
		dims := domain.NewDimension(6, 2)
		// Event shared by Alex (col 0) and Partner (col 2) - Kid (col 1) is not involved
		snap := provider.CalendarSnapshot{Events: []provider.CalendarEvent{
			{ID: "ev_nonadj", CalendarName: "alex-cal", Title: "Parent Teacher Meeting", Start: "2026-09-27T11:00:00Z", End: "2026-09-27T12:00:00Z"},
			{ID: "ev_nonadj", CalendarName: "partner-cal", Title: "Parent Teacher Meeting", Start: "2026-09-27T11:00:00Z", End: "2026-09-27T12:00:00Z"},
		}}

		view, err := BuildFamilyDayView(snap, cfg, dims, testNow)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		alexCol := view.Columns[0]
		kidCol := view.Columns[1]
		partnerCol := view.Columns[2]

		if len(alexCol.Events) != 1 {
			t.Fatalf("expected 1 event in Alex column, got %d", len(alexCol.Events))
		}
		if len(kidCol.Events) != 0 {
			t.Fatalf("expected 0 events in Kid column, got %d", len(kidCol.Events))
		}
		if len(partnerCol.Events) != 1 {
			t.Fatalf("expected 1 event in Partner column, got %d", len(partnerCol.Events))
		}

		evAlex := alexCol.Events[0]
		evPartner := partnerCol.Events[0]

		if !evAlex.IsLinked || evAlex.LinkGlyph != "🔗" || evAlex.ColSpan != 1 {
			t.Errorf("Alex event should have IsLinked=true, LinkGlyph='🔗', ColSpan=1, got %+v", evAlex)
		}
		if !evPartner.IsLinked || evPartner.LinkGlyph != "🔗" || evPartner.ColSpan != 1 {
			t.Errorf("Partner event should have IsLinked=true, LinkGlyph='🔗', ColSpan=1, got %+v", evPartner)
		}
		if evAlex.Color != alexCol.Color {
			t.Errorf("expected Alex event to use Alex column color %s, got %s", alexCol.Color, evAlex.Color)
		}
		if evPartner.Color != partnerCol.Color {
			t.Errorf("expected Partner event to use Partner column color %s, got %s", partnerCol.Color, evPartner.Color)
		}
	})

	t.Run("4x2 mixed active member and collapsed member duplicates into member and shared with link glyph", func(t *testing.T) {
		t.Parallel()
		cfg := fiveMemberConfig()
		dims := domain.NewDimension(4, 2)
		// On 4x2: Alex (col 0), Kid (col 1), Partner (col 2). Grandma is collapsed into Shared.
		// Event shared by Alex and Grandma:
		snap := provider.CalendarSnapshot{Events: []provider.CalendarEvent{
			{ID: "ev_mixed", CalendarName: "alex-cal", Title: "Lunch with Grandma", Start: "2026-09-27T12:00:00Z", End: "2026-09-27T13:00:00Z"},
			{ID: "ev_mixed", CalendarName: "grandma-cal", Title: "Lunch with Grandma", Start: "2026-09-27T12:00:00Z", End: "2026-09-27T13:00:00Z"},
		}}

		view, err := BuildFamilyDayView(snap, cfg, dims, testNow)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// 3 active member columns + 1 shared column
		if len(view.Columns) != 4 {
			t.Fatalf("expected 4 columns, got %d", len(view.Columns))
		}

		alexCol := view.Columns[0]
		sharedCol := view.Columns[3]

		if len(alexCol.Events) != 1 {
			t.Fatalf("expected 1 event in Alex column, got %d", len(alexCol.Events))
		}
		if len(sharedCol.Events) != 1 {
			t.Fatalf("expected 1 event in Shared column, got %d", len(sharedCol.Events))
		}

		evAlex := alexCol.Events[0]
		evShared := sharedCol.Events[0]

		if !evAlex.IsLinked || evAlex.LinkGlyph != "🔗" || evAlex.ColSpan != 1 {
			t.Errorf("Alex event should be linked with glyph, got %+v", evAlex)
		}
		if !evShared.IsLinked || evShared.LinkGlyph != "🔗" || evShared.ColSpan != 1 {
			t.Errorf("Shared event should be linked with glyph, got %+v", evShared)
		}
	})
}

func TestBuildFamilyDayView_UnclaimedSharedColumnRouting(t *testing.T) {
	t.Parallel()

	t.Run("unclaimed event routes to shared column", func(t *testing.T) {
		t.Parallel()
		cfg := fiveMemberConfig()
		dims := domain.NewDimension(6, 2)
		snap := provider.CalendarSnapshot{Events: []provider.CalendarEvent{
			{ID: "e_unclaimed", CalendarName: "unclaimed-cal", Title: "Town Marathon", Start: "2026-09-27T09:00:00Z", End: "2026-09-27T11:00:00Z"},
		}}
		view, err := BuildFamilyDayView(snap, cfg, dims, testNow)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// 5 members + 1 shared column = 6 columns
		if len(view.Columns) != 6 {
			t.Fatalf("expected 6 columns (5 members + shared), got %d", len(view.Columns))
		}
		sharedCol := view.Columns[5]
		if !sharedCol.IsShared || sharedCol.Name != "Shared" {
			t.Fatalf("expected last column to be Shared, got %+v", sharedCol)
		}
		if len(sharedCol.Events) != 1 || sharedCol.Events[0].Title != "Town Marathon" {
			t.Fatalf("expected Town Marathon in shared column, got %+v", sharedCol.Events)
		}
		if sharedCol.Events[0].IsLinked || sharedCol.Events[0].ColSpan != 1 {
			t.Errorf("unclaimed event should not be linked: %+v", sharedCol.Events[0])
		}
	})
}

func renderFamilyWidgetTemplate(t *testing.T, ctx Context, now time.Time) string {
	t.Helper()
	tmplPath := filepath.Join("..", "..", "widgets", "calendar-grid", "views", "widget.html")
	if _, err := os.Stat(tmplPath); os.IsNotExist(err) {
		tmplPath = filepath.Join("..", "..", "widgets", "calendar-family", "views", "widget.html")
	}
	tmpl, err := template.New("widget.html").Funcs(StandardFuncMap(func() time.Time {
		return now
	})).ParseFiles(tmplPath)
	if err != nil {
		t.Fatalf("failed to parse calendar-family widget template: %v", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, ctx); err != nil {
		t.Fatalf("failed to execute calendar-family widget template: %v", err)
	}
	return buf.String()
}

func TestCalendarFamily_DayViewTemplateRender_6x2(t *testing.T) {
	t.Parallel()

	snap := provider.CalendarSnapshot{
		LastSync: "2026-09-27T12:00:00Z",
		Events: []provider.CalendarEvent{
			{ID: "e1", CalendarName: "alex-personal", Title: "Design Review", Start: "2026-09-27T10:00:00Z", End: "2026-09-27T11:00:00Z", Location: "War Room A"},
			{ID: "e2", CalendarName: "school", Title: "All Day Holiday", Start: "2026-09-27", End: "2026-09-27", AllDay: true},
			{ID: "e3", CalendarName: "alex-personal", Title: "Dawn Run", Start: "2026-09-27T05:30:00Z", End: "2026-09-27T06:30:00Z"},
			{ID: "e4", CalendarName: "alex-personal", Title: "Late Movie", Start: "2026-09-27T22:00:00Z", End: "2026-09-27T23:30:00Z"},
			{ID: "e5", CalendarName: "unclaimed-cal", Title: "Town Marathon", Start: "2026-09-27T14:00:00Z", End: "2026-09-27T15:30:00Z"},
		},
	}

	cfg := baseFamilyConfig()
	cfg["default_view"] = "day"
	cfg["date"] = "2026-09-27"
	cfg["hours"] = []any{"07:00", "21:00"}

	ctx := Context{
		ID:         "test-family-day",
		Type:       "calendar-family",
		Dimensions: domain.NewDimension(6, 2),
		Data:       snap,
		Config:     cfg,
	}

	html := renderFamilyWidgetTemplate(t, ctx, testNow)

	// Verify root container & class
	if !strings.Contains(html, "cf-view-day") {
		t.Errorf("expected 'cf-view-day' in rendered output: %s", html)
	}
	// Verify CSS variable declarations
	if !strings.Contains(html, "--mm-member-0: #E07A5F") {
		t.Errorf("expected --mm-member-0 in html: %s", html)
	}
	// Verify date header
	if !strings.Contains(html, "Sun, 2026-09-27") {
		t.Errorf("expected date header 'Sun, 2026-09-27' in html: %s", html)
	}
	// Verify all-day banner
	if !strings.Contains(html, "cf-allday-banner") || !strings.Contains(html, "All Day Holiday") {
		t.Errorf("expected all-day event 'All Day Holiday' in html: %s", html)
	}
	// Verify timeline axis with hour markers
	if !strings.Contains(html, "cf-timeline-axis") || !strings.Contains(html, "07:00") || !strings.Contains(html, "21:00") {
		t.Errorf("expected timeline axis with 07:00 and 21:00 markers: %s", html)
	}
	// Verify member column headers and avatars
	if !strings.Contains(html, ">Alex</span>") || !strings.Contains(html, ">Kid</span>") {
		t.Errorf("expected column headers for Alex and Kid: %s", html)
	}
	// Verify early and late edge bands
	if !strings.Contains(html, "cf-early-band") || !strings.Contains(html, "Dawn Run") {
		t.Errorf("expected early edge band with Dawn Run: %s", html)
	}
	if !strings.Contains(html, "cf-late-band") || !strings.Contains(html, "Late Movie") {
		t.Errorf("expected late edge band with Late Movie: %s", html)
	}
	// Verify timed event positioning
	if !strings.Contains(html, "Design Review") || !strings.Contains(html, "top:") || !strings.Contains(html, "height:") {
		t.Errorf("expected Design Review with top and height styles: %s", html)
	}
	if !strings.Contains(html, "War Room A") {
		t.Errorf("expected event location 'War Room A': %s", html)
	}
	// Verify pattern classes
	if !strings.Contains(html, "cf-pattern-solid") || !strings.Contains(html, "cf-pattern-hatch") {
		t.Errorf("expected pattern classes cf-pattern-solid and cf-pattern-hatch in output: %s", html)
	}
}

func TestCalendarFamily_DayViewTemplateRender_4x2(t *testing.T) {
	t.Parallel()

	snap := provider.CalendarSnapshot{
		LastSync: "2026-09-27T12:00:00Z",
		Events: []provider.CalendarEvent{
			{ID: "e_shared", CalendarName: "alex-personal", Title: "Family Dinner", Start: "2026-09-27T18:00:00Z", End: "2026-09-27T19:30:00Z"},
			{ID: "e_grandma", CalendarName: "grandma-cal", Title: "Knitting", Start: "2026-09-27T10:00:00Z", End: "2026-09-27T11:00:00Z"},
		},
	}

	cfg := fiveMemberConfig()
	cfg["default_view"] = "day"
	cfg["date"] = "2026-09-27"
	cfg["hours"] = []any{"07:00", "21:00"}

	ctx := Context{
		ID:         "test-family-day-4x2",
		Type:       "calendar-family",
		Dimensions: domain.NewDimension(4, 2),
		Data:       snap,
		Config:     cfg,
	}

	html := renderFamilyWidgetTemplate(t, ctx, testNow)

	if !strings.Contains(html, "cf-view-day") {
		t.Errorf("expected cf-view-day in 4x2 html: %s", html)
	}
	// 4x2 renders 3 member columns + 1 shared column for collapsed grandma
	if !strings.Contains(html, ">Shared</span>") {
		t.Errorf("expected Shared column in 4x2: %s", html)
	}
	if !strings.Contains(html, "Knitting") {
		t.Errorf("expected Knitting in 4x2 html: %s", html)
	}
	// Pattern classes for dots and outline
	if !strings.Contains(html, "cf-pattern-dots") || !strings.Contains(html, "cf-pattern-outline") {
		t.Errorf("expected cf-pattern-dots and cf-pattern-outline in 4x2 html: %s", html)
	}
}

func TestCalendarFamily_DayViewTemplateRender_EmptyAndNoData(t *testing.T) {
	t.Parallel()

	// 1. Members configured but no events
	cfg := baseFamilyConfig()
	cfg["default_view"] = "day"
	cfg["date"] = "2026-09-27"

	ctxEmpty := Context{
		ID:         "test-day-empty-events",
		Type:       "calendar-family",
		Dimensions: domain.NewDimension(6, 2),
		Data:       provider.CalendarSnapshot{},
		Config:     cfg,
	}
	htmlEmpty := renderFamilyWidgetTemplate(t, ctxEmpty, testNow)
	if !strings.Contains(htmlEmpty, "cf-view-day") || !strings.Contains(htmlEmpty, "Alex") {
		t.Errorf("expected day columns even with 0 events: %s", htmlEmpty)
	}

	// 2. No members configured -> cf-empty
	cfgNoMembers := map[string]any{"default_view": "day"}
	ctxNoMembers := Context{
		ID:         "test-day-no-members",
		Type:       "calendar-family",
		Dimensions: domain.NewDimension(6, 2),
		Data:       nil,
		Config:     cfgNoMembers,
	}
	htmlNoMembers := renderFamilyWidgetTemplate(t, ctxNoMembers, testNow)
	if !strings.Contains(htmlNoMembers, "No calendar data yet") {
		t.Errorf("expected 'No calendar data yet' when no members: %s", htmlNoMembers)
	}
}

func TestCalendarFamily_WeekViewTemplateRender(t *testing.T) {
	t.Parallel()

	snap := provider.CalendarSnapshot{
		Events: []provider.CalendarEvent{
			{ID: "e_week", CalendarName: "alex-personal", Title: "Team Standup", Start: "2026-09-28T09:00:00Z", End: "2026-09-28T09:30:00Z"},
		},
	}
	cfg := baseFamilyConfig()
	cfg["default_view"] = "week"

	ctx := Context{
		ID:         "test-family-week",
		Type:       "calendar-family",
		Dimensions: domain.NewDimension(6, 2),
		Data:       snap,
		Config:     cfg,
	}

	html := renderFamilyWidgetTemplate(t, ctx, testNow)
	if !strings.Contains(html, "cf-view-week") {
		t.Errorf("expected 'cf-view-week' in rendered output: %s", html)
	}
	if !strings.Contains(html, "cf-week-grid") {
		t.Errorf("expected 'cf-week-grid' in rendered output: %s", html)
	}
	if !strings.Contains(html, "Team Standup") {
		t.Errorf("expected 'Team Standup' in rendered output: %s", html)
	}
}

func TestHelpers_DayViewAndAddSub(t *testing.T) {
	t.Parallel()

	funcMap := StandardFuncMap(func() time.Time { return testNow })

	// Test dayView
	dayViewFn, ok := funcMap["dayView"].(func(data any, cfg map[string]any, dims domain.Dimension) (*FamilyDayViewModel, error))
	if !ok {
		t.Fatalf("expected dayView helper in funcMap")
	}
	cfg := baseFamilyConfig()
	cfg["date"] = "2026-09-27"
	v, err := dayViewFn(provider.CalendarSnapshot{}, cfg, domain.NewDimension(6, 2))
	if err != nil || v == nil || v.Date != "2026-09-27" {
		t.Fatalf("dayView helper failed: v=%+v, err=%v", v, err)
	}

	// Test isDayView
	isDayViewFn, ok := funcMap["isDayView"].(func(cfg map[string]any) bool)
	if !ok {
		t.Fatalf("expected isDayView helper in funcMap")
	}
	if !isDayViewFn(map[string]any{"default_view": "day"}) {
		t.Errorf("expected isDayView to return true for 'day'")
	}
	if !isDayViewFn(map[string]any{"default_view": "DAY"}) {
		t.Errorf("expected isDayView to return true for 'DAY' (case-insensitive)")
	}
	if isDayViewFn(map[string]any{"default_view": "week"}) {
		t.Errorf("expected isDayView to return false for 'week'")
	}
	if isDayViewFn(nil) {
		t.Errorf("expected isDayView to return false for nil config")
	}

	// Test add and sub
	addFn, ok := funcMap["add"].(func(a, b int) int)
	if !ok || addFn(3, 4) != 7 {
		t.Errorf("add helper failed")
	}
	subFn, ok := funcMap["sub"].(func(a, b int) int)
	if !ok || subFn(5, 2) != 3 {
		t.Errorf("sub helper failed")
	}
}

func TestBuildFamilyMonthView_WindowComputationAndWeekStarts(t *testing.T) {
	t.Parallel()
	dims := domain.NewDimension(6, 2)
	now := time.Date(2026, 10, 10, 14, 0, 0, 0, time.UTC)

	t.Run("sunday start default (October 2026)", func(t *testing.T) {
		t.Parallel()
		cfg := baseFamilyConfig()
		cfg["week_starts"] = "sunday"

		view, err := BuildFamilyMonthView(provider.CalendarSnapshot{}, cfg, dims, now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(view.Days) != 42 {
			t.Fatalf("expected 42 days in month grid, got %d", len(view.Days))
		}
		if len(view.Weeks) != 6 {
			t.Fatalf("expected 6 weeks in month grid, got %d", len(view.Weeks))
		}
		for i, w := range view.Weeks {
			if w.WeekNumber != i+1 {
				t.Errorf("expected week number %d, got %d", i+1, w.WeekNumber)
			}
			if len(w.Days) != 7 {
				t.Fatalf("expected 7 days in week %d, got %d", i+1, len(w.Days))
			}
		}

		// Oct 1, 2026 is Thursday. Sunday start means window starts on Sunday Sep 27, 2026.
		if view.RangeStart != "2026-09-27" {
			t.Errorf("expected RangeStart 2026-09-27, got %s", view.RangeStart)
		}
		// 42 days from Sep 27 ends on Saturday Nov 7, 2026.
		if view.RangeEnd != "2026-11-07" {
			t.Errorf("expected RangeEnd 2026-11-07, got %s", view.RangeEnd)
		}
		if view.MonthLabel != "October 2026" {
			t.Errorf("expected MonthLabel 'October 2026', got %s", view.MonthLabel)
		}
		if view.MonthName != "October" || view.Year != 2026 || view.Month != 10 {
			t.Errorf("unexpected month/year fields: %+v", view)
		}

		expectedHeaders := []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
		if len(view.WeekdayHeaders) != 7 {
			t.Fatalf("expected 7 headers, got %d", len(view.WeekdayHeaders))
		}
		for i, h := range expectedHeaders {
			if view.WeekdayHeaders[i] != h {
				t.Errorf("header %d: expected %s, got %s", i, h, view.WeekdayHeaders[i])
			}
		}

		// Verify first day is Sunday Sep 27
		firstDay := view.Days[0]
		if firstDay.Date != "2026-09-27" || firstDay.WeekdayAbbr != "Sun" || firstDay.IsCurrentMonth {
			t.Errorf("unexpected first day: %+v", firstDay)
		}
	})

	t.Run("monday start (October 2026)", func(t *testing.T) {
		t.Parallel()
		cfg := baseFamilyConfig()
		cfg["week_starts"] = "monday"

		view, err := BuildFamilyMonthView(provider.CalendarSnapshot{}, cfg, dims, now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(view.Days) != 42 {
			t.Fatalf("expected 42 days in month grid, got %d", len(view.Days))
		}
		// Oct 1, 2026 is Thursday. Monday start means window starts on Monday Sep 28, 2026.
		if view.RangeStart != "2026-09-28" {
			t.Errorf("expected RangeStart 2026-09-28, got %s", view.RangeStart)
		}
		// 42 days from Sep 28 ends on Sunday Nov 8, 2026.
		if view.RangeEnd != "2026-11-08" {
			t.Errorf("expected RangeEnd 2026-11-08, got %s", view.RangeEnd)
		}

		expectedHeaders := []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}
		for i, h := range expectedHeaders {
			if view.WeekdayHeaders[i] != h {
				t.Errorf("header %d: expected %s, got %s", i, h, view.WeekdayHeaders[i])
			}
		}

		// First day is Monday Sep 28
		firstDay := view.Days[0]
		if firstDay.Date != "2026-09-28" || firstDay.WeekdayAbbr != "Mon" || firstDay.IsCurrentMonth {
			t.Errorf("unexpected first day: %+v", firstDay)
		}
	})
}

func TestBuildFamilyMonthView_LeapYearAndMonthBoundaries(t *testing.T) {
	t.Parallel()
	dims := domain.NewDimension(6, 2)
	now := time.Date(2024, 2, 15, 10, 0, 0, 0, time.UTC)

	t.Run("february 2024 leap year 29 days", func(t *testing.T) {
		t.Parallel()
		cfg := baseFamilyConfig()
		cfg["month"] = "2024-02"
		cfg["week_starts"] = "sunday"

		view, err := BuildFamilyMonthView(provider.CalendarSnapshot{}, cfg, dims, now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(view.Days) != 42 {
			t.Fatalf("expected 42 days, got %d", len(view.Days))
		}

		currentMonthCount := 0
		var feb29 *FamilyMonthDay
		var mar1 *FamilyMonthDay
		for i := range view.Days {
			d := &view.Days[i]
			if d.IsCurrentMonth {
				currentMonthCount++
			}
			if d.Date == "2024-02-29" {
				feb29 = d
			}
			if d.Date == "2024-03-01" {
				mar1 = d
			}
		}

		if currentMonthCount != 29 {
			t.Errorf("expected 29 current month days for Feb 2024, got %d", currentMonthCount)
		}
		if feb29 == nil || !feb29.IsCurrentMonth {
			t.Errorf("expected Feb 29 to be present and marked as IsCurrentMonth: %+v", feb29)
		}
		if mar1 == nil || mar1.IsCurrentMonth {
			t.Errorf("expected Mar 1 to be marked as NOT IsCurrentMonth: %+v", mar1)
		}
	})

	t.Run("year boundary transition (January 2025)", func(t *testing.T) {
		t.Parallel()
		cfg := baseFamilyConfig()
		cfg["date"] = "2025-01-10"
		cfg["week_starts"] = "sunday"

		view, err := BuildFamilyMonthView(provider.CalendarSnapshot{}, cfg, dims, now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if view.RangeStart != "2024-12-29" {
			t.Errorf("expected RangeStart 2024-12-29, got %s", view.RangeStart)
		}
		if view.Days[0].Date != "2024-12-29" || view.Days[0].IsCurrentMonth {
			t.Errorf("expected Dec 29 to be IsCurrentMonth false, got %+v", view.Days[0])
		}
		if view.MonthLabel != "January 2025" {
			t.Errorf("expected January 2025, got %s", view.MonthLabel)
		}
	})
}

func TestBuildFamilyMonthView_MemberDotLists(t *testing.T) {
	t.Parallel()
	dims := domain.NewDimension(6, 2)
	now := time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)

	cfg := map[string]any{
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
			map[string]any{"name": "shared-family"},
			map[string]any{"name": "unclaimed-cal"},
		},
	}

	snap := provider.CalendarSnapshot{
		Events: []provider.CalendarEvent{
			{ID: "e1", CalendarName: "alice-cal", Title: "Alice Event 1", Start: "2026-10-10T09:00:00Z", End: "2026-10-10T10:00:00Z"},
			{ID: "e2", CalendarName: "alice-cal", Title: "Alice Event 2", Start: "2026-10-10T14:00:00Z", End: "2026-10-10T15:00:00Z"},
			{ID: "e3", CalendarName: "charlie-cal", Title: "Charlie Soccer", Start: "2026-10-10T11:00:00Z", End: "2026-10-10T12:00:00Z"},
			{ID: "e4", CalendarName: "alice-cal", Title: "Family Brunch", Start: "2026-10-11T10:00:00Z", End: "2026-10-11T12:00:00Z"},
			{ID: "e4", CalendarName: "bob-cal", Title: "Family Brunch", Start: "2026-10-11T10:00:00Z", End: "2026-10-11T12:00:00Z"},
			{ID: "e5", CalendarName: "unclaimed-cal", Title: "Public Holiday", AllDay: true, Start: "2026-10-12", End: "2026-10-12"},
		},
	}

	view, err := BuildFamilyMonthView(snap, cfg, dims, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	dayMap := make(map[string]FamilyMonthDay)
	for _, d := range view.Days {
		dayMap[d.Date] = d
	}

	d10 := dayMap["2026-10-10"]
	if d10.EventsCount != 3 {
		t.Errorf("expected 3 events on Oct 10, got %d", d10.EventsCount)
	}
	if len(d10.Dots) != 2 {
		t.Fatalf("expected 2 dots on Oct 10 (Alice, Charlie), got %d: %+v", len(d10.Dots), d10.Dots)
	}
	if d10.Dots[0].Name != "Alice" || d10.Dots[0].Color != "#E11D48" {
		t.Errorf("expected first dot to be Alice, got %+v", d10.Dots[0])
	}
	if d10.Dots[1].Name != "Charlie" || d10.Dots[1].Color != "#16A34A" {
		t.Errorf("expected second dot to be Charlie, got %+v", d10.Dots[1])
	}

	d11 := dayMap["2026-10-11"]
	if len(d11.Dots) != 2 {
		t.Fatalf("expected 2 dots on Oct 11, got %d: %+v", len(d11.Dots), d11.Dots)
	}
	if d11.Dots[0].Name != "Alice" || d11.Dots[1].Name != "Bob" {
		t.Errorf("expected dots [Alice, Bob], got [%s, %s]", d11.Dots[0].Name, d11.Dots[1].Name)
	}

	d12 := dayMap["2026-10-12"]
	if len(d12.Dots) != 1 {
		t.Fatalf("expected 1 dot on Oct 12, got %d: %+v", len(d12.Dots), d12.Dots)
	}
	if !d12.Dots[0].IsShared || d12.Dots[0].Name != "Shared" || d12.Dots[0].Color != "#123456" {
		t.Errorf("expected Shared dot with #123456, got %+v", d12.Dots[0])
	}

	d13 := dayMap["2026-10-13"]
	if len(d13.Dots) != 0 || d13.EventsCount != 0 {
		t.Errorf("expected 0 dots on empty day Oct 13, got %d dots, %d events", len(d13.Dots), d13.EventsCount)
	}
}

func TestBuildFamilyMonthView_DensityAdaptation(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)
	cfg := baseFamilyConfig()

	snap := provider.CalendarSnapshot{
		Events: []provider.CalendarEvent{
			{ID: "e_timed", CalendarName: "alex-cal", Title: "Later Timed Event", Start: "2026-10-15T15:00:00Z", End: "2026-10-15T16:00:00Z"},
			{ID: "e_early", CalendarName: "alex-cal", Title: "Earlier Timed Event", Start: "2026-10-15T09:00:00Z", End: "2026-10-15T10:00:00Z"},
			{ID: "e_allday", CalendarName: "alex-cal", Title: "All Day Conference", AllDay: true, Start: "2026-10-15", End: "2026-10-15"},
		},
	}

	t.Run("6x2 dimension resolves first event title (all-day takes priority)", func(t *testing.T) {
		t.Parallel()
		dims6x2 := domain.NewDimension(6, 2)
		view, err := BuildFamilyMonthView(snap, cfg, dims6x2, now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		for _, d := range view.Days {
			if d.Date == "2026-10-15" {
				if d.EventTitle != "All Day Conference" {
					t.Errorf("expected EventTitle 'All Day Conference', got %q", d.EventTitle)
				}
				if d.EventsCount != 3 {
					t.Errorf("expected 3 events, got %d", d.EventsCount)
				}
			}
		}
	})

	t.Run("4x2 dimension resolves event titles", func(t *testing.T) {
		t.Parallel()
		dims4x2 := domain.NewDimension(4, 2)
		view, err := BuildFamilyMonthView(snap, cfg, dims4x2, now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		for _, d := range view.Days {
			if d.Date == "2026-10-15" {
				if d.EventTitle != "All Day Conference" {
					t.Errorf("expected EventTitle 'All Day Conference' on 4x2, got %q", d.EventTitle)
				}
				if len(d.Events) != 3 {
					t.Errorf("expected 2 events in Events slice on 4x2, got %d", len(d.Events))
				}
				if len(d.Dots) == 0 {
					t.Errorf("expected dots to still be populated on 4x2")
				}
			}
		}
	})

	t.Run("narrow dimension (< 4 cols) omits event titles (dots-only)", func(t *testing.T) {
		t.Parallel()
		dims2x2 := domain.NewDimension(2, 2)
		view, err := BuildFamilyMonthView(snap, cfg, dims2x2, now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		for _, d := range view.Days {
			if d.Date == "2026-10-15" {
				if d.EventTitle != "" {
					t.Errorf("expected empty EventTitle on narrow dimension, got %q", d.EventTitle)
				}
				if len(d.Events) != 3 {
					t.Errorf("expected events to still be populated on narrow dimension, got %d", len(d.Events))
				}
				if len(d.Dots) == 0 {
					t.Errorf("expected dots to still be populated on narrow dimension")
				}
			}
		}
	})
}

func TestBuildFamilyMonthView_TodayAndWeekendFlags(t *testing.T) {
	t.Parallel()
	dims := domain.NewDimension(6, 2)
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	cfg := baseFamilyConfig()

	view, err := BuildFamilyMonthView(provider.CalendarSnapshot{}, cfg, dims, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, d := range view.Days {
		if d.Date == "2026-10-10" {
			if !d.IsToday {
				t.Errorf("expected Oct 10 to have IsToday == true")
			}
			if !d.IsWeekend {
				t.Errorf("expected Saturday Oct 10 to have IsWeekend == true")
			}
		} else {
			if d.IsToday {
				t.Errorf("expected date %s to have IsToday == false", d.Date)
			}
		}

		if d.WeekdayAbbr == "Sat" || d.WeekdayAbbr == "Sun" {
			if !d.IsWeekend {
				t.Errorf("expected %s (%s) to have IsWeekend == true", d.Date, d.WeekdayAbbr)
			}
		} else {
			if d.IsWeekend {
				t.Errorf("expected %s (%s) to have IsWeekend == false", d.Date, d.WeekdayAbbr)
			}
		}
	}
}

func TestBuildFamilyMonthView_ErrorAndEdgeCases(t *testing.T) {
	t.Parallel()
	dims := domain.NewDimension(6, 2)
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

	t.Run("invalid member config returns error", func(t *testing.T) {
		t.Parallel()
		badCfg := map[string]any{
			"members": []any{
				map[string]any{"name": "NoColor"},
			},
		}
		if _, err := BuildFamilyMonthView(provider.CalendarSnapshot{}, badCfg, dims, now); err == nil {
			t.Fatalf("expected error on missing member color")
		}
	})

	t.Run("private calendar redaction in month view", func(t *testing.T) {
		t.Parallel()
		cfg := baseFamilyConfig()
		cfg["calendars"] = []any{
			map[string]any{"name": "alex-cal", "private": true},
		}
		snap := provider.CalendarSnapshot{
			Events: []provider.CalendarEvent{
				{ID: "e_priv", CalendarName: "alex-cal", Title: "Secret Meeting", Start: "2026-10-10T10:00:00Z", End: "2026-10-10T11:00:00Z"},
			},
		}
		view, err := BuildFamilyMonthView(snap, cfg, dims, now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, d := range view.Days {
			if d.Date == "2026-10-10" {
				if d.EventTitle != "Busy" {
					t.Errorf("expected redacted title 'Busy', got %q", d.EventTitle)
				}
			}
		}
	})

	t.Run("multi-day event spanning across month window", func(t *testing.T) {
		t.Parallel()
		cfg := baseFamilyConfig()
		snap := provider.CalendarSnapshot{
			Events: []provider.CalendarEvent{
				{ID: "e_vacation", CalendarName: "alex-personal", Title: "Fall Break", AllDay: true, Start: "2026-10-05", End: "2026-10-08"},
			},
		}
		view, err := BuildFamilyMonthView(snap, cfg, dims, now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		spannedDates := []string{"2026-10-05", "2026-10-06", "2026-10-07", "2026-10-08"}
		dayMap := make(map[string]FamilyMonthDay)
		for _, d := range view.Days {
			dayMap[d.Date] = d
		}
		for _, dateStr := range spannedDates {
			d := dayMap[dateStr]
			if d.EventsCount != 1 || d.EventTitle != "Fall Break" {
				t.Errorf("expected Fall Break on %s, got %+v", dateStr, d)
			}
			if len(d.Dots) != 1 || d.Dots[0].Name != "Alex" {
				t.Errorf("expected Alex dot on %s, got %+v", dateStr, d.Dots)
			}
		}
	})

	t.Run("event outside window is ignored", func(t *testing.T) {
		t.Parallel()
		cfg := baseFamilyConfig()
		snap := provider.CalendarSnapshot{
			Events: []provider.CalendarEvent{
				{ID: "e_far_future", CalendarName: "alex-cal", Title: "Far Future", Start: "2027-10-10T10:00:00Z", End: "2027-10-10T11:00:00Z"},
			},
		}
		view, err := BuildFamilyMonthView(snap, cfg, dims, now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, d := range view.Days {
			if d.EventsCount > 0 {
				t.Errorf("expected 0 events in window, got %d on %s", d.EventsCount, d.Date)
			}
		}
	})
}

func TestHelpers_CalendarFamilyMonthView(t *testing.T) {
	t.Parallel()
	fm := StandardFuncMap(time.Now)

	fn, ok := fm["calendarFamilyMonthView"].(func(any, map[string]any, domain.Dimension) (*FamilyMonthViewModel, error))
	if !ok {
		t.Fatalf("expected calendarFamilyMonthView in funcMap")
	}
	view, err := fn(provider.CalendarSnapshot{}, baseFamilyConfig(), domain.NewDimension(6, 2))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if view == nil || len(view.Days) != 42 {
		t.Fatalf("expected 42 days in month view, got %+v", view)
	}

	monthViewFn, ok := fm["monthView"].(func(any, map[string]any, domain.Dimension) (*FamilyMonthViewModel, error))
	if !ok {
		t.Fatalf("expected monthView in funcMap")
	}
	if _, err := monthViewFn(provider.CalendarSnapshot{}, baseFamilyConfig(), domain.NewDimension(6, 2)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	isMonthViewFn, ok := fm["isMonthView"].(func(map[string]any) bool)
	if !ok {
		t.Fatalf("expected isMonthView in funcMap")
	}
	if !isMonthViewFn(map[string]any{"default_view": "month"}) {
		t.Errorf("expected isMonthView to return true for 'month'")
	}
	if !isMonthViewFn(map[string]any{"default_view": "MONTH"}) {
		t.Errorf("expected isMonthView to return true for 'MONTH'")
	}
	if isMonthViewFn(map[string]any{"default_view": "week"}) {
		t.Errorf("expected isMonthView to return false for 'week'")
	}
	if isMonthViewFn(nil) {
		t.Errorf("expected isMonthView to return false for nil config")
	}
}

func TestCalendarFamily_MonthViewTemplateRender_6x2(t *testing.T) {
	t.Parallel()

	snap := provider.CalendarSnapshot{
		LastSync: "2026-09-27T12:00:00Z",
		Events: []provider.CalendarEvent{
			{ID: "e1", CalendarName: "alex-personal", Title: "Parent Teacher Night", Start: "2026-09-27T18:00:00Z", End: "2026-09-27T19:00:00Z"},
			{ID: "e2", CalendarName: "school", Title: "Fall Break", Start: "2026-09-15", End: "2026-09-18", AllDay: true},
			{ID: "e3", CalendarName: "holidays", Title: "Town Fair", Start: "2026-09-20T10:00:00Z", End: "2026-09-20T12:00:00Z"},
		},
	}

	cfg := baseFamilyConfig()
	cfg["default_view"] = "month"
	cfg["date"] = "2026-09-27"

	ctx := Context{
		ID:         "test-family-month-6x2",
		Type:       "calendar-family",
		Dimensions: domain.NewDimension(6, 2),
		Data:       snap,
		Config:     cfg,
	}

	html := renderFamilyWidgetTemplate(t, ctx, testNow)

	// Verify root container & class
	if !strings.Contains(html, "cf-view-month") {
		t.Errorf("expected 'cf-view-month' in rendered output: %s", html)
	}
	// Verify CSS variable declarations for members
	if !strings.Contains(html, "--mm-member-0: #E07A5F") {
		t.Errorf("expected --mm-member-0 in html: %s", html)
	}
	// Verify Month label
	if !strings.Contains(html, "September 2026") {
		t.Errorf("expected 'September 2026' in html: %s", html)
	}
	// Verify Weekday headers
	if !strings.Contains(html, "cf-month-weekdays") || !strings.Contains(html, "cf-weekday-header") {
		t.Errorf("expected weekday headers in html: %s", html)
	}
	if !strings.Contains(html, ">Sun<") || !strings.Contains(html, ">Sat<") {
		t.Errorf("expected Sunday and Saturday in weekday headers: %s", html)
	}
	// Verify Month grid container
	if !strings.Contains(html, "cf-month-grid") {
		t.Errorf("expected cf-month-grid in html: %s", html)
	}
	// Verify current day styling & outline
	if !strings.Contains(html, "cf-today") || !strings.Contains(html, "cf-today-num") {
		t.Errorf("expected cf-today and cf-today-num in html: %s", html)
	}
	// Verify other month dimming
	if !strings.Contains(html, "cf-other-month") {
		t.Errorf("expected cf-other-month in html: %s", html)
	}
	// Verify member dot chips colored with --mm-member-N
	if !strings.Contains(html, "cf-month-dot") || !strings.Contains(html, "var(--mm-member-0") {
		t.Errorf("expected cf-month-dot and var(--mm-member-0) in html: %s", html)
	}
	// Verify unclaimed/shared dot uses shared color variable
	if !strings.Contains(html, "var(--mm-cf-shared") {
		t.Errorf("expected var(--mm-cf-shared) in html: %s", html)
	}
	// Verify primary event title rendered on 6x2
	if !strings.Contains(html, "cf-month-event-title") || !strings.Contains(html, "Parent Teacher Night") {
		t.Errorf("expected event title 'Parent Teacher Night' in html: %s", html)
	}
}

func TestCalendarFamily_MonthViewTemplateRender_4x2(t *testing.T) {
	t.Parallel()

	snap := provider.CalendarSnapshot{
		LastSync: "2026-09-27T12:00:00Z",
		Events: []provider.CalendarEvent{
			{ID: "e1", CalendarName: "alex-personal", Title: "Parent Teacher Night", Start: "2026-09-27T18:00:00Z", End: "2026-09-27T19:00:00Z"},
		},
	}

	cfg := baseFamilyConfig()
	cfg["default_view"] = "month"
	cfg["date"] = "2026-09-27"

	ctx := Context{
		ID:         "test-family-month-4x2",
		Type:       "calendar-family",
		Dimensions: domain.NewDimension(4, 2),
		Data:       snap,
		Config:     cfg,
	}

	html := renderFamilyWidgetTemplate(t, ctx, testNow)

	if !strings.Contains(html, "cf-view-month") {
		t.Errorf("expected 'cf-view-month' in rendered output: %s", html)
	}
	// On 4x2, member dots are present
	if !strings.Contains(html, "cf-month-dot") {
		t.Errorf("expected member dots on 4x2: %s", html)
	}
	// On 4x2, event titles are rendered
	if !strings.Contains(html, "cf-month-event-title") {
		t.Errorf("expected event title to be rendered on 4x2 layout: %s", html)
	}
	if !strings.Contains(html, "Parent Teacher Night") {
		t.Errorf("expected Parent Teacher Night on 4x2 layout: %s", html)
	}
}

func TestCalendarFamily_MonthViewTemplateRender_Narrow_DotsOnly(t *testing.T) {
	t.Parallel()

	snap := provider.CalendarSnapshot{
		LastSync: "2026-09-27T12:00:00Z",
		Events: []provider.CalendarEvent{
			{ID: "e1", CalendarName: "alex-personal", Title: "Parent Teacher Night", Start: "2026-09-27T18:00:00Z", End: "2026-09-27T19:00:00Z"},
		},
	}

	cfg := baseFamilyConfig()
	cfg["default_view"] = "month"
	cfg["date"] = "2026-09-27"

	ctx := Context{
		ID:         "test-family-month-narrow",
		Type:       "calendar-family",
		Dimensions: domain.NewDimension(2, 2),
		Data:       snap,
		Config:     cfg,
	}

	html := renderFamilyWidgetTemplate(t, ctx, testNow)

	if !strings.Contains(html, "cf-month-dot") {
		t.Errorf("expected member dots on narrow layout: %s", html)
	}
	if strings.Contains(html, "cf-month-event-title") {
		t.Errorf("expected event title to be omitted on narrow layout: %s", html)
	}
}

func TestCalendarFamily_MonthViewTemplateRender_Empty(t *testing.T) {
	t.Parallel()

	cfg := baseFamilyConfig()
	cfg["default_view"] = "month"
	cfg["date"] = "2026-09-27"

	ctx := Context{
		ID:         "test-family-month-empty",
		Type:       "calendar-family",
		Dimensions: domain.NewDimension(6, 2),
		Data:       provider.CalendarSnapshot{},
		Config:     cfg,
	}

	html := renderFamilyWidgetTemplate(t, ctx, testNow)

	if !strings.Contains(html, "cf-view-month") {
		t.Errorf("expected 'cf-view-month' in rendered output: %s", html)
	}
	if !strings.Contains(html, "September 2026") {
		t.Errorf("expected 'September 2026' in html: %s", html)
	}
	// Should render 42 day cells even when empty of events
	if !strings.Contains(html, "cf-month-day") {
		t.Errorf("expected cf-month-day in html: %s", html)
	}
}

func TestCalendarFamily_MonthViewTemplateRender_4x2_Events(t *testing.T) {
	t.Parallel()

	cfg := baseFamilyConfig()
	cfg["default_view"] = "month"
	cfg["date"] = "2026-10-10"

	snap := provider.CalendarSnapshot{
		LastSync: "2026-10-10T12:00:00Z",
		Events: []provider.CalendarEvent{
			{ID: "e_dentist", CalendarName: "alex-personal", Title: "Dentist Visit", Start: "2026-10-10T14:00:00Z", End: "2026-10-10T15:00:00Z"},
		},
	}

	ctx := Context{
		ID:         "test-family-month-4x2-events",
		Type:       "calendar-family",
		Dimensions: domain.NewDimension(4, 2),
		Data:       snap,
		Config:     cfg,
	}

	html := renderFamilyWidgetTemplate(t, ctx, testNow)

	if !strings.Contains(html, "cf-view-month") {
		t.Errorf("expected 'cf-view-month' in rendered output")
	}
	if !strings.Contains(html, "Dentist Visit") {
		t.Errorf("expected event title 'Dentist Visit' in rendered 4x2 month html: %s", html)
	}
	if !strings.Contains(html, "cf-month-event-title") {
		t.Errorf("expected 'cf-month-event-title' class in rendered 4x2 month html")
	}
	if !strings.Contains(html, `data-event-id="e_dentist"`) {
		t.Errorf("expected data-event-id='e_dentist' in rendered 4x2 month html")
	}
}

func TestCalendarFamily_ViewSwitcherAndBootstrap(t *testing.T) {
	t.Parallel()

	snap := provider.CalendarSnapshot{
		LastSync: "2026-09-27T12:00:00Z",
		Events: []provider.CalendarEvent{
			{ID: "e1", CalendarName: "alex-personal", Title: "Soccer Practice", Start: "2026-09-27T16:00:00Z", End: "2026-09-27T17:00:00Z"},
		},
	}

	testViews := []struct {
		view      string
		activeBtn string
		inactiveA string
		inactiveB string
		rootClass string
	}{
		{"day", `data-view="day" role="tab" aria-selected="true">Day</button>`, `data-view="week" role="tab" aria-selected="false">Week</button>`, `data-view="month" role="tab" aria-selected="false">Month</button>`, "cf-view-day"},
		{"week", `data-view="week" role="tab" aria-selected="true">Week</button>`, `data-view="day" role="tab" aria-selected="false">Day</button>`, `data-view="month" role="tab" aria-selected="false">Month</button>`, "cf-view-week"},
		{"month", `data-view="month" role="tab" aria-selected="true">Month</button>`, `data-view="day" role="tab" aria-selected="false">Day</button>`, `data-view="week" role="tab" aria-selected="false">Week</button>`, "cf-view-month"},
	}

	for _, tc := range testViews {
		t.Run(tc.view, func(t *testing.T) {
			t.Parallel()
			cfg := baseFamilyConfig()
			cfg["default_view"] = tc.view
			cfg["date"] = "2026-09-27"

			ctx := Context{
				ID:         "test-switcher-" + tc.view,
				Type:       "calendar-family",
				Dimensions: domain.NewDimension(6, 2),
				Data:       snap,
				Config:     cfg,
			}

			html := renderFamilyWidgetTemplate(t, ctx, testNow)

			if !strings.Contains(html, "cf-view-switcher") {
				t.Errorf("[%s] expected cf-view-switcher in html: %s", tc.view, html)
			}
			if !strings.Contains(html, tc.rootClass) {
				t.Errorf("[%s] expected root class %s in html", tc.view, tc.rootClass)
			}
			if !strings.Contains(html, tc.activeBtn) {
				t.Errorf("[%s] expected active button %s in html", tc.view, tc.activeBtn)
			}
			if !strings.Contains(html, tc.inactiveA) || !strings.Contains(html, tc.inactiveB) {
				t.Errorf("[%s] expected inactive buttons %s and %s in html", tc.view, tc.inactiveA, tc.inactiveB)
			}
			if !strings.Contains(html, "calendar-family-data") {
				t.Errorf("[%s] expected embedded calendar-family-data JSON script in html", tc.view)
			}
			if !strings.Contains(html, "MirrormereCalendarFamily.mount") {
				t.Errorf("[%s] expected MirrormereCalendarFamily self-mount hook in html", tc.view)
			}
		})
	}
}

func TestBuildFamilyView_ContinuousSpanningBars(t *testing.T) {
	t.Parallel()

	// testNow is Sunday 2026-09-27. Window for 6x2 (7 days, Sunday start) is 2026-09-27 through 2026-10-03:
	// Col 1: 2026-09-27 (Sun)
	// Col 2: 2026-09-28 (Mon)
	// Col 3: 2026-09-29 (Tue)
	// Col 4: 2026-09-30 (Wed)
	// Col 5: 2026-10-01 (Thu)
	// Col 6: 2026-10-02 (Fri)
	// Col 7: 2026-10-03 (Sat)
	snap := provider.CalendarSnapshot{
		Events: []provider.CalendarEvent{
			// 1. Single-day all-day event on Wednesday (Col 4)
			{ID: "e_single", CalendarName: "alex-personal", Title: "Town Hall", AllDay: true, Start: "2026-09-30", End: "2026-09-30"},
			// 2. Multi-day all-day event Mon-Wed (Cols 2..4, span 3)
			{ID: "e_camp", CalendarName: "alex-personal", Title: "Camping Trip", AllDay: true, Start: "2026-09-28", End: "2026-09-30"},
			// 3. Event entering window from before rangeStart (Sep 20..Sep 28 -> Cols 1..2, span 2)
			{ID: "e_early", CalendarName: "alex-personal", Title: "Early Vacation", AllDay: true, Start: "2026-09-20", End: "2026-09-28"},
			// 4. Event extending past window end (Oct 02..Oct 06 -> Cols 6..7, span 2)
			{ID: "e_late", CalendarName: "alex-personal", Title: "Late Conference", AllDay: true, Start: "2026-10-02", End: "2026-10-06"},
			// 5. Event spanning entire window (Sep 20..Oct 10 -> Cols 1..7, span 7)
			{ID: "e_full", CalendarName: "alex-personal", Title: "Autumn Festival", AllDay: true, Start: "2026-09-20", End: "2026-10-10"},
			// 6. Event completely outside window (Sep 10..Sep 15)
			{ID: "e_outside", CalendarName: "alex-personal", Title: "Past Trip", AllDay: true, Start: "2026-09-10", End: "2026-09-15"},
		},
	}

	view, err := BuildFamilyView(snap, baseFamilyConfig(), domain.NewDimension(6, 2), testNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify events outside window are omitted
	for _, ev := range view.AllDayEvents {
		if ev.ID == "e_outside" {
			t.Fatalf("event outside window should not be in AllDayEvents")
		}
	}

	// Index by ID
	byID := make(map[string]FamilySpanEvent)
	for _, ev := range view.AllDayEvents {
		byID[ev.ID] = ev
	}

	// Check single day event
	single, ok := byID["e_single"]
	if !ok {
		t.Fatalf("e_single missing from AllDayEvents")
	}
	if single.StartCol != 4 || single.ColSpan != 1 {
		t.Errorf("e_single: want StartCol=4, ColSpan=1; got StartCol=%d, ColSpan=%d", single.StartCol, single.ColSpan)
	}

	// Check multi-day event inside window (Mon-Wed)
	camp, ok := byID["e_camp"]
	if !ok {
		t.Fatalf("e_camp missing from AllDayEvents")
	}
	if camp.StartCol != 2 || camp.ColSpan != 3 {
		t.Errorf("e_camp: want StartCol=2, ColSpan=3; got StartCol=%d, ColSpan=%d", camp.StartCol, camp.ColSpan)
	}

	// Check event entering window from before rangeStart
	early, ok := byID["e_early"]
	if !ok {
		t.Fatalf("e_early missing from AllDayEvents")
	}
	if early.StartCol != 1 || early.ColSpan != 2 {
		t.Errorf("e_early: want StartCol=1, ColSpan=2; got StartCol=%d, ColSpan=%d", early.StartCol, early.ColSpan)
	}

	// Check event extending past window end
	late, ok := byID["e_late"]
	if !ok {
		t.Fatalf("e_late missing from AllDayEvents")
	}
	if late.StartCol != 6 || late.ColSpan != 2 {
		t.Errorf("e_late: want StartCol=6, ColSpan=2; got StartCol=%d, ColSpan=%d", late.StartCol, late.ColSpan)
	}

	// Check event spanning full window
	full, ok := byID["e_full"]
	if !ok {
		t.Fatalf("e_full missing from AllDayEvents")
	}
	if full.StartCol != 1 || full.ColSpan != 7 {
		t.Errorf("e_full: want StartCol=1, ColSpan=7; got StartCol=%d, ColSpan=%d", full.StartCol, full.ColSpan)
	}

	// Check sorting: StartCol asc, ColSpan desc, Title asc
	// At StartCol=1, we have e_full (span 7) and e_early (span 2) -> e_full must precede e_early
	if len(view.AllDayEvents) < 2 {
		t.Fatalf("expected at least 2 events")
	}
	if view.AllDayEvents[0].ID != "e_full" {
		t.Errorf("expected first event to be e_full (StartCol=1, ColSpan=7), got %s", view.AllDayEvents[0].ID)
	}
	if view.AllDayEvents[1].ID != "e_early" {
		t.Errorf("expected second event to be e_early (StartCol=1, ColSpan=2), got %s", view.AllDayEvents[1].ID)
	}
}

func TestRenderFamilyWidgetTemplate_WeekContinuousSpanningBars(t *testing.T) {
	t.Parallel()

	snap := provider.CalendarSnapshot{
		LastSync: "2026-09-27T12:00:00Z",
		Events: []provider.CalendarEvent{
			{ID: "e_multi", CalendarName: "alex-personal", Title: "Camping Trip", AllDay: true, Start: "2026-09-28", End: "2026-09-30"},
			{ID: "e_timed", CalendarName: "alex-personal", Title: "Morning Standup", Start: "2026-09-28T09:00:00Z", End: "2026-09-28T09:30:00Z"},
		},
	}
	cfg := baseFamilyConfig()
	cfg["default_view"] = "week"

	ctx := Context{
		ID:         "test-family-week-spanning",
		Type:       "calendar-family",
		Dimensions: domain.NewDimension(6, 2),
		Data:       snap,
		Config:     cfg,
	}

	html := renderFamilyWidgetTemplate(t, ctx, testNow)

	// Verify all-day grid exists and has the spanning bar
	if !strings.Contains(html, "cf-week-allday-grid") {
		t.Errorf("expected 'cf-week-allday-grid' in rendered week HTML: %s", html)
	}
	if !strings.Contains(html, "grid-column: 2 / span 3") {
		t.Errorf("expected 'grid-column: 2 / span 3' for Camping Trip in week HTML: %s", html)
	}
	if !strings.Contains(html, "Camping Trip") {
		t.Errorf("expected 'Camping Trip' in rendered week HTML: %s", html)
	}
	if !strings.Contains(html, `data-event-id="e_multi"`) {
		t.Errorf("expected data-event-id='e_multi' in rendered week HTML: %s", html)
	}

	// Verify individual cf-day columns do NOT contain duplicated allday blocks
	// Count occurrences of rendered DOM event cards for 'Camping Trip' in week view panel - should only appear ONCE as a single spanning bar
	weekHTML := html
	if idx := strings.Index(html, "cf-view-week"); idx != -1 {
		if endIdx := strings.Index(html[idx:], "cf-view-month"); endIdx != -1 {
			weekHTML = html[idx : idx+endIdx]
		}
	}
	count := strings.Count(weekHTML, ">Camping Trip</div>")
	if count != 1 {
		t.Errorf("expected 'Camping Trip' DOM card to appear exactly once in week view panel (as a single spanning bar), got %d occurrences", count)
	}

	// Verify timed events still render in their columns
	if !strings.Contains(html, "Morning Standup") {
		t.Errorf("expected 'Morning Standup' timed event in week HTML: %s", html)
	}
}

func TestBuildFamilyView_ContinuousSpanningBars_CustomDayCount(t *testing.T) {
	t.Parallel()

	// 4x2 dimension defaults to 5 days starting from today (2026-09-27):
	// Col 1: Sep 27, Col 2: Sep 28, Col 3: Sep 29, Col 4: Sep 30, Col 5: Oct 01
	snap := provider.CalendarSnapshot{
		Events: []provider.CalendarEvent{
			{ID: "e_span5", CalendarName: "alex-personal", Title: "Long Weekend", AllDay: true, Start: "2026-09-28", End: "2026-10-04"},
		},
	}

	view, err := BuildFamilyView(snap, baseFamilyConfig(), domain.NewDimension(4, 2), testNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(view.Days) != 5 {
		t.Fatalf("expected 5 days, got %d", len(view.Days))
	}
	if len(view.AllDayEvents) != 1 {
		t.Fatalf("expected 1 all-day event, got %d", len(view.AllDayEvents))
	}

	ev := view.AllDayEvents[0]
	// Starts Sep 28 (Col 2). Extends to Oct 04, but window ends Oct 01 (Col 5).
	// Spanned visible cols: 2, 3, 4, 5 -> StartCol=2, ColSpan=4
	if ev.StartCol != 2 || ev.ColSpan != 4 {
		t.Errorf("want StartCol=2, ColSpan=4; got StartCol=%d, ColSpan=%d", ev.StartCol, ev.ColSpan)
	}
}


func TestBuildFamilyView_WeekViewTimelinePositioning(t *testing.T) {
	t.Parallel()

	snap := provider.CalendarSnapshot{
		LastSync: "2026-09-27T12:00:00Z",
		Events: []provider.CalendarEvent{
			{
				ID:           "evt_morning",
				CalendarName: "alex-personal",
				Title:        "Morning Standup",
				Start:        "2026-09-28T09:00:00Z",
				End:          "2026-09-28T10:00:00Z",
			},
		},
	}
	cfg := baseFamilyConfig()
	view, err := BuildFamilyView(snap, cfg, domain.NewDimension(6, 2), testNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if view.HoursStart != "07:00" || view.HoursEnd != "21:00" {
		t.Errorf("unexpected hours window: %s to %s", view.HoursStart, view.HoursEnd)
	}
	if len(view.HourMarkers) == 0 {
		t.Error("expected hour markers to be populated")
	}

	monDay := view.Days[1]
	if len(monDay.Events) != 1 {
		t.Fatalf("expected 1 canvas event on Monday, got %d", len(monDay.Events))
	}
	ev := monDay.Events[0]
	if ev.Title != "Morning Standup" {
		t.Errorf("expected title Morning Standup, got %s", ev.Title)
	}
	if ev.TopPct < 14.0 || ev.TopPct > 15.0 {
		t.Errorf("expected TopPct ~14.29%%, got %f", ev.TopPct)
	}
	if ev.HeightPct < 7.0 || ev.HeightPct > 8.0 {
		t.Errorf("expected HeightPct ~7.14%%, got %f", ev.HeightPct)
	}
	if ev.LeftPct != 0.0 || ev.WidthPct != 100.0 {
		t.Errorf("expected LeftPct=0, WidthPct=100; got Left=%f, Width=%f", ev.LeftPct, ev.WidthPct)
	}
}

func TestBuildFamilyView_WeekViewOverlappingEvents(t *testing.T) {
	t.Parallel()

	snap := provider.CalendarSnapshot{
		LastSync: "2026-09-27T12:00:00Z",
		Events: []provider.CalendarEvent{
			{
				ID:           "evt_a",
				CalendarName: "alex-personal",
				Title:        "Team Sync",
				Start:        "2026-09-28T09:00:00Z",
				End:          "2026-09-28T10:00:00Z",
			},
			{
				ID:           "evt_b",
				CalendarName: "alex-personal",
				Title:        "Design Critique",
				Start:        "2026-09-28T09:30:00Z",
				End:          "2026-09-28T10:30:00Z",
			},
		},
	}
	cfg := baseFamilyConfig()
	view, err := BuildFamilyView(snap, cfg, domain.NewDimension(6, 2), testNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	monDay := view.Days[1]
	if len(monDay.Events) != 2 {
		t.Fatalf("expected 2 canvas events on Monday, got %d", len(monDay.Events))
	}
	ev1 := monDay.Events[0]
	ev2 := monDay.Events[1]

	if ev1.LeftPct != 0.0 || ev1.WidthPct != 50.0 {
		t.Errorf("ev1 expected LeftPct=0, WidthPct=50; got Left=%f, Width=%f", ev1.LeftPct, ev1.WidthPct)
	}
	if ev2.LeftPct != 50.0 || ev2.WidthPct != 50.0 {
		t.Errorf("ev2 expected LeftPct=50, WidthPct=50; got Left=%f, Width=%f", ev2.LeftPct, ev2.WidthPct)
	}
}

func TestBuildFamilyView_WeekViewEdgeBands(t *testing.T) {
	t.Parallel()

	snap := provider.CalendarSnapshot{
		LastSync: "2026-09-27T12:00:00Z",
		Events: []provider.CalendarEvent{
			{
				ID:           "evt_early",
				CalendarName: "alex-personal",
				Title:        "Early Flight",
				Start:        "2026-09-28T05:00:00Z",
				End:          "2026-09-28T06:30:00Z",
			},
			{
				ID:           "evt_late",
				CalendarName: "alex-personal",
				Title:        "Late Concert",
				Start:        "2026-09-28T22:00:00Z",
				End:          "2026-09-28T23:30:00Z",
			},
		},
	}
	cfg := baseFamilyConfig()
	view, err := BuildFamilyView(snap, cfg, domain.NewDimension(6, 2), testNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	monDay := view.Days[1]
	if len(monDay.EarlyEvents) != 1 {
		t.Fatalf("expected 1 EarlyEvent, got %d", len(monDay.EarlyEvents))
	}
	if monDay.EarlyEvents[0].Title != "Early Flight" {
		t.Errorf("unexpected early event title: %s", monDay.EarlyEvents[0].Title)
	}
	if len(monDay.LateEvents) != 1 {
		t.Fatalf("expected 1 LateEvent, got %d", len(monDay.LateEvents))
	}
	if monDay.LateEvents[0].Title != "Late Concert" {
		t.Errorf("unexpected late event title: %s", monDay.LateEvents[0].Title)
	}
}

func TestBuildFamilyView_MidnightCrossover(t *testing.T) {
	t.Parallel()

	snap := provider.CalendarSnapshot{
		LastSync: "2026-09-27T12:00:00Z",
		Events: []provider.CalendarEvent{
			{
				ID:           "evt_crossover",
				CalendarName: "alex-personal",
				Title:        "Overnight Hackathon",
				Start:        "2026-09-28T22:00:00Z",
				End:          "2026-09-29T04:00:00Z",
			},
		},
	}
	cfg := baseFamilyConfig()
	view, err := BuildFamilyView(snap, cfg, domain.NewDimension(6, 2), testNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	monDay := view.Days[1]
	tueDay := view.Days[2]

	if len(monDay.LateEvents) != 1 {
		t.Errorf("expected 1 late event on Monday, got %d", len(monDay.LateEvents))
	}
	if len(tueDay.EarlyEvents) != 1 {
		t.Errorf("expected 1 early event on Tuesday, got %d", len(tueDay.EarlyEvents))
	}
}

func TestRenderFamilyWidgetTemplate_WeekTimeline(t *testing.T) {
	t.Parallel()

	snap := provider.CalendarSnapshot{
		LastSync: "2026-09-27T12:00:00Z",
		Events: []provider.CalendarEvent{
			{ID: "e_timed", CalendarName: "alex-personal", Title: "Soccer Practice", Start: "2026-09-28T16:00:00Z", End: "2026-09-28T17:30:00Z"},
		},
	}
	cfg := baseFamilyConfig()
	cfg["default_view"] = "week"

	ctx := Context{
		ID:         "test-family-week-timeline",
		Type:       "calendar-family",
		Dimensions: domain.NewDimension(6, 2),
		Data:       snap,
		Config:     cfg,
	}

	html := renderFamilyWidgetTemplate(t, ctx, testNow)

	if !strings.Contains(html, "cf-week-body") {
		t.Errorf("expected 'cf-week-body' in rendered HTML: %s", html)
	}
	if !strings.Contains(html, "cf-timeline-axis") {
		t.Errorf("expected 'cf-timeline-axis' in rendered HTML: %s", html)
	}
	if !strings.Contains(html, "cf-day-canvas") {
		t.Errorf("expected 'cf-day-canvas' in rendered HTML: %s", html)
	}
	if !strings.Contains(html, "Soccer Practice") {
		t.Errorf("expected 'Soccer Practice' in rendered HTML: %s", html)
	}
}

func TestBuildFamilyCombinedData_NilSafetyAndAggregation(t *testing.T) {
	t.Parallel()

	// 1. Nil safety
	nilResult := BuildFamilyCombinedData(nil, nil, nil, nil)
	if nilResult == nil {
		t.Fatalf("expected non-nil map from BuildFamilyCombinedData with all nil inputs")
	}

	// 2. Full aggregation
	dayView := &FamilyDayViewModel{
		Date: "2026-10-10",
		Columns: []FamilyDayColumn{
			{Name: "Alex", Initial: "A", Color: "#a855f7"},
		},
		AllDayEvents: []FamilyDayEvent{
			{FamilyEvent: FamilyEvent{ID: "e_day_allday", Title: "Holiday"}},
		},
	}
	weekView := &FamilyViewModel{
		Today:      "2026-10-10",
		RangeStart: "2026-10-05",
		RangeEnd:   "2026-10-11",
		Days: []FamilyDay{
			{Date: "2026-10-10", DayOfMonth: 10},
		},
		AllDayEvents: []FamilySpanEvent{
			{FamilyEvent: FamilyEvent{ID: "e_span", Title: "Conference"}},
		},
	}
	monthView := &FamilyMonthViewModel{
		RangeStart: "2026-09-28",
		RangeEnd:   "2026-11-08",
		Days: []FamilyMonthDay{
			{Date: "2026-10-10", DayOfMonth: 10},
		},
	}
	cfg := map[string]any{"default_view": "week"}

	res := BuildFamilyCombinedData(dayView, weekView, monthView, cfg)
	if res["date"] != "2026-10-10" {
		t.Errorf("expected date 2026-10-10, got %v", res["date"])
	}
	if res["today"] != "2026-10-10" {
		t.Errorf("expected today 2026-10-10, got %v", res["today"])
	}
	if res["range_start"] != "2026-10-05" {
		t.Errorf("expected range_start 2026-10-05, got %v", res["range_start"])
	}
	if res["range_end"] != "2026-10-11" {
		t.Errorf("expected range_end 2026-10-11, got %v", res["range_end"])
	}
	if res["cached_range_start"] != "2026-09-28" {
		t.Errorf("expected cached_range_start 2026-09-28, got %v", res["cached_range_start"])
	}
	if res["cached_range_end"] != "2026-11-08" {
		t.Errorf("expected cached_range_end 2026-11-08, got %v", res["cached_range_end"])
	}

	// 3. Helper registration
	funcMap := StandardFuncMap(func() time.Time { return testNow })
	gridFn, ok := funcMap["calendarGridData"].(func(*FamilyDayViewModel, *FamilyViewModel, *FamilyMonthViewModel, map[string]any) map[string]any)
	if !ok || gridFn == nil {
		t.Fatalf("expected calendarGridData in StandardFuncMap")
	}
	familyFn, ok := funcMap["calendarFamilyData"].(func(*FamilyDayViewModel, *FamilyViewModel, *FamilyMonthViewModel, map[string]any) map[string]any)
	if !ok || familyFn == nil {
		t.Fatalf("expected calendarFamilyData in StandardFuncMap")
	}
	res2 := gridFn(dayView, weekView, monthView, cfg)
	if res2["date"] != "2026-10-10" {
		t.Errorf("expected date from helper call")
	}
}

func TestBuildFamilyView_DateOverrideAndIsToday(t *testing.T) {
	t.Parallel()

	snap := provider.CalendarSnapshot{
		LastSync: "2026-09-27T12:00:00Z",
		Events: []provider.CalendarEvent{
			{ID: "e1", CalendarName: "alex-personal", Title: "Event on Oct 15", Start: "2026-10-15T10:00:00Z", End: "2026-10-15T11:00:00Z"},
		},
	}

	// 1. cfg with date set to 2026-10-15 (Thursday) with week_starts sunday
	cfg := baseFamilyConfig()
	cfg["date"] = "2026-10-15"
	dims := domain.NewDimension(6, 2)

	// testNow is 2026-09-27 (Sunday)
	view, err := BuildFamilyView(snap, cfg, dims, testNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The week of 2026-10-15 starting Sunday is 2026-10-11 to 2026-10-17
	if view.RangeStart != "2026-10-11" {
		t.Errorf("expected RangeStart 2026-10-11, got %q", view.RangeStart)
	}
	if view.RangeEnd != "2026-10-17" {
		t.Errorf("expected RangeEnd 2026-10-17, got %q", view.RangeEnd)
	}
	if view.Today != "2026-09-27" {
		t.Errorf("expected Today to remain 2026-09-27, got %q", view.Today)
	}

	// Since 2026-09-27 is not in 2026-10-11..2026-10-17, no day should have IsToday = true
	for _, day := range view.Days {
		if day.IsToday {
			t.Errorf("expected IsToday false for day %s, but got true", day.Date)
		}
	}

	// 2. cfg with date set to 2026-09-29 (Tuesday of the current week containing testNow = 2026-09-27)
	cfg2 := baseFamilyConfig()
	cfg2["date"] = "2026-09-29"
	view2, err := BuildFamilyView(snap, cfg2, dims, testNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if view2.RangeStart != "2026-09-27" {
		t.Errorf("expected RangeStart 2026-09-27, got %q", view2.RangeStart)
	}
	if view2.RangeEnd != "2026-10-03" {
		t.Errorf("expected RangeEnd 2026-10-03, got %q", view2.RangeEnd)
	}
	// Exactly Sunday (index 0, 2026-09-27) should have IsToday == true
	todayFound := false
	for _, day := range view2.Days {
		if day.Date == "2026-09-27" {
			if !day.IsToday {
				t.Errorf("expected IsToday true for 2026-09-27")
			}
			todayFound = true
		} else if day.IsToday {
			t.Errorf("expected IsToday false for non-today date %s", day.Date)
		}
	}
	if !todayFound {
		t.Errorf("expected to find day 2026-09-27 in view2")
	}

	// 3. Week starts Monday with date override
	cfg3 := baseFamilyConfig()
	cfg3["week_starts"] = "monday"
	cfg3["date"] = "2026-10-15" // Thursday
	view3, err := BuildFamilyView(snap, cfg3, dims, testNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Week starting Monday for 2026-10-15 is 2026-10-12 to 2026-10-18
	if view3.RangeStart != "2026-10-12" {
		t.Errorf("expected RangeStart 2026-10-12, got %q", view3.RangeStart)
	}
	if view3.RangeEnd != "2026-10-18" {
		t.Errorf("expected RangeEnd 2026-10-18, got %q", view3.RangeEnd)
	}

	// 4. Num days < 7 (e.g. 3 days)
	cfg4 := baseFamilyConfig()
	cfg4["days"] = 3
	cfg4["date"] = "2026-10-15"
	view4, err := BuildFamilyView(snap, cfg4, domain.NewDimension(3, 2), testNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if view4.RangeStart != "2026-10-15" {
		t.Errorf("expected RangeStart 2026-10-15, got %q", view4.RangeStart)
	}
	if view4.RangeEnd != "2026-10-17" {
		t.Errorf("expected RangeEnd 2026-10-17, got %q", view4.RangeEnd)
	}
}

func TestBuildFamilyCombinedData_RollingWindowBounds(t *testing.T) {
	t.Parallel()

	weekView := &FamilyViewModel{
		Today:      "2026-10-10",
		RangeStart: "2026-10-05",
		RangeEnd:   "2026-10-11",
	}

	// Case 1: no monthView and no explicit cfg - should default to -90d and +180d from Today
	res := BuildFamilyCombinedData(nil, weekView, nil, nil)
	expectedStart := "2026-07-12" // 2026-10-10 - 90 days
	expectedEnd := "2027-04-08"   // 2026-10-10 + 180 days
	if res["cached_range_start"] != expectedStart {
		t.Errorf("expected cached_range_start %s, got %v", expectedStart, res["cached_range_start"])
	}
	if res["cached_range_end"] != expectedEnd {
		t.Errorf("expected cached_range_end %s, got %v", expectedEnd, res["cached_range_end"])
	}

	// Case 2: explicit cfg bounds take precedence
	cfgExplicit := map[string]any{
		"cached_range_start": "2026-09-01",
		"cached_range_end":   "2026-12-31",
	}
	resExplicit := BuildFamilyCombinedData(nil, weekView, nil, cfgExplicit)
	if resExplicit["cached_range_start"] != "2026-09-01" {
		t.Errorf("expected explicit cached_range_start 2026-09-01, got %v", resExplicit["cached_range_start"])
	}
	if resExplicit["cached_range_end"] != "2026-12-31" {
		t.Errorf("expected explicit cached_range_end 2026-12-31, got %v", resExplicit["cached_range_end"])
	}

	// Case 3: dayView when weekView is nil
	dayView := &FamilyDayViewModel{
		Date: "2026-10-10",
	}
	resDay := BuildFamilyCombinedData(dayView, nil, nil, nil)
	if resDay["cached_range_start"] != expectedStart {
		t.Errorf("expected dayView cached_range_start %s, got %v", expectedStart, resDay["cached_range_start"])
	}
	if resDay["cached_range_end"] != expectedEnd {
		t.Errorf("expected dayView cached_range_end %s, got %v", expectedEnd, resDay["cached_range_end"])
	}
}

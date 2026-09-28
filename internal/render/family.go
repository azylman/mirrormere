package render

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/azylman/mirrormere/internal/domain"
	"github.com/azylman/mirrormere/internal/provider"
)

// FamilyMember represents a resolved member per SPEC-014 "Member resolution".
type FamilyMember struct {
	Name      string   `json:"name"`
	Initial   string   `json:"initial"`
	Color     string   `json:"color"`
	TextColor string   `json:"text_color"`
	Pattern   string   `json:"pattern"`
	Calendars []string `json:"calendars"`
}

// FamilyEvent represents a resolved, presentation-ready event within a single day column.
type FamilyEvent struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Location  string   `json:"location"`
	Start     string   `json:"start"`
	End       string   `json:"end"`
	AllDay    bool     `json:"all_day"`
	Owners    []string `json:"owners"`
	Initials  []string `json:"initials"`
	Colors    []string `json:"colors"`
	Color     string   `json:"color"`
	TextColor string   `json:"text_color"`
	Patterns  []string `json:"patterns"`
	Pattern   string   `json:"pattern"`
}

// FamilyDay represents one column of the week view per SPEC-014 "Week (default)".
type FamilyDay struct {
	Date       string        `json:"date"`
	WeekdayAbb string        `json:"weekday_abbr"`
	DayOfMonth int           `json:"day_of_month"`
	IsToday    bool          `json:"is_today"`
	IsWeekend  bool          `json:"is_weekend"`
	AllDay     []FamilyEvent `json:"all_day"`
	Timed      []FamilyEvent `json:"timed"`
	Overflow   int           `json:"overflow"`
}

// FamilyViewModel is the fully resolved server-side view model consumed by the calendar-family
// week view template per SPEC-014 "View model".
type FamilyViewModel struct {
	RangeStart  string         `json:"range_start"`
	RangeEnd    string         `json:"range_end"`
	RangeLabel  string         `json:"range_label"`
	Today       string         `json:"today"`
	Members     []FamilyMember `json:"members"`
	Days        []FamilyDay    `json:"days"`
	SharedColor string         `json:"shared_color"`
}

const (
	defaultSharedColor = "#3D405B"
	maxFamilyMembers   = 8
)

// familyCalendarSource mirrors the subset of the calendar-family config_schema `calendars[]`
// entry needed by the view model (private-calendar redaction and unclaimed-calendar color).
type familyCalendarSource struct {
	name    string
	color   string
	private bool
}

// BuildFamilyView resolves the calendar-agenda provider's CalendarSnapshot payload plus the
// calendar-family widget instance config into the week-view model per SPEC-014.
//
// dims is the widget's rendered grid dimensions (SPEC-005), used to size the visible-row cutoff
// that produces the "+N more" overflow per SPEC-014 "Size adaptation".
func BuildFamilyView(data any, cfg map[string]any, dims domain.Dimension, now time.Time) (*FamilyViewModel, error) {
	members, err := parseFamilyMembers(cfg)
	if err != nil {
		return nil, err
	}
	calendars := parseFamilyCalendarSources(cfg)
	sharedColor := stringOr(cfg, "shared_color", defaultSharedColor)
	weekStartsMonday := strings.EqualFold(stringOr(cfg, "week_starts", "sunday"), "monday")

	numDays := resolveFamilyDayCount(cfg, dims)

	var rangeStart time.Time
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if numDays >= 7 {
		rangeStart = startOfWeek(today, weekStartsMonday)
	} else {
		rangeStart = today
	}
	rangeEnd := rangeStart.AddDate(0, 0, numDays-1)

	snapshot := extractCalendarSnapshot(data)

	days := make([]FamilyDay, numDays)
	for i := 0; i < numDays; i++ {
		d := rangeStart.AddDate(0, 0, i)
		days[i] = FamilyDay{
			Date:       d.Format("2006-01-02"),
			WeekdayAbb: d.Format("Mon"),
			DayOfMonth: d.Day(),
			IsToday:    d.Equal(today),
			IsWeekend:  d.Weekday() == time.Saturday || d.Weekday() == time.Sunday,
		}
	}

	dayIndex := make(map[string]int, numDays)
	for i, d := range days {
		dayIndex[d.Date] = i
	}

	maxVisible := maxVisibleTimedRows(dims)

	// Merge shared events by id: an event id seen on multiple member calendars is one event
	// owned by every claiming member, per SPEC-014 "Shared events".
	merged := make(map[string]*FamilyEvent)
	mergedOrder := make([]string, 0)
	// mergedDates holds every in-window date key a merged event lands on: a single entry for
	// a timed event, or one entry per spanned day for a multi-day all-day event (SPEC-014
	// "Multi-day events" - a spanning all-day event appears in every date column it covers,
	// not only its start day).
	mergedDates := make(map[string][]string)
	mergedOwnerSet := make(map[string]map[string]bool)

	for _, ev := range snapshot.Events {
		var evDates []string
		if ev.AllDay {
			evDates = familyDateRange(ev.Start, ev.End)
		} else {
			evDates = []string{eventDateKey(ev, now.Location())}
		}

		var matchedDates []string
		for _, d := range evDates {
			if _, ok := dayIndex[d]; ok {
				matchedDates = append(matchedDates, d)
			}
		}
		if len(matchedDates) == 0 {
			continue // no day this event spans falls inside the displayed window
		}

		fe, exists := merged[ev.ID]
		if !exists {
			fe = &FamilyEvent{
				ID:       ev.ID,
				Title:    ev.Title,
				Location: ev.Location,
				AllDay:   ev.AllDay,
			}
			if !ev.AllDay {
				if t, ok := parseFamilyTime(ev.Start); ok {
					fe.Start = t.In(now.Location()).Format("15:04")
				}
				if t, ok := parseFamilyTime(ev.End); ok {
					fe.End = t.In(now.Location()).Format("15:04")
				}
			}
			merged[ev.ID] = fe
			mergedOrder = append(mergedOrder, ev.ID)
			mergedDates[ev.ID] = matchedDates
			mergedOwnerSet[ev.ID] = make(map[string]bool)
		}

		if src, ok := calendars[ev.CalendarName]; ok && src.private {
			fe.Title = "Busy"
			fe.Location = ""
		}

		for _, m := range members {
			if calendarClaimedBy(m, ev.CalendarName) {
				mergedOwnerSet[ev.ID][m.Name] = true
			}
		}
	}

	// Resolve owners in member order (SPEC-014 "Member order"), independent of the order in
	// which each merged event's underlying per-calendar occurrences were fetched.
	for _, id := range mergedOrder {
		fe := merged[id]
		claimed := mergedOwnerSet[id]
		for _, m := range members {
			if claimed[m.Name] {
				fe.Owners = append(fe.Owners, m.Name)
				fe.Initials = append(fe.Initials, m.Initial)
				fe.Colors = append(fe.Colors, m.Color)
				fe.Patterns = append(fe.Patterns, m.Pattern)
			}
		}
	}

	for _, id := range mergedOrder {
		fe := merged[id]
		if len(fe.Colors) > 0 {
			fe.Color = fe.Colors[0]
		} else {
			fe.Color = sharedColor
		}
		if len(fe.Patterns) > 0 {
			fe.Pattern = fe.Patterns[0]
		}
		fe.TextColor = contrastTextColor(fe.Color)
		for _, d := range mergedDates[id] {
			idx := dayIndex[d]
			if fe.AllDay {
				days[idx].AllDay = append(days[idx].AllDay, *fe)
			} else {
				days[idx].Timed = append(days[idx].Timed, *fe)
			}
		}
	}

	for i := range days {
		sort.SliceStable(days[i].Timed, func(a, b int) bool {
			return days[i].Timed[a].Start < days[i].Timed[b].Start
		})
		if maxVisible > 0 && len(days[i].Timed) > maxVisible {
			days[i].Overflow = len(days[i].Timed) - maxVisible
			days[i].Timed = days[i].Timed[:maxVisible]
		}
	}

	return &FamilyViewModel{
		RangeStart:  rangeStart.Format("2006-01-02"),
		RangeEnd:    rangeEnd.Format("2006-01-02"),
		RangeLabel:  formatFamilyRangeLabel(rangeStart, rangeEnd),
		Today:       today.Format("2006-01-02"),
		Members:     members,
		Days:        days,
		SharedColor: sharedColor,
	}, nil
}

func extractCalendarSnapshot(data any) provider.CalendarSnapshot {
	switch v := data.(type) {
	case provider.CalendarSnapshot:
		return v
	case *provider.CalendarSnapshot:
		if v != nil {
			return *v
		}
	}
	return provider.CalendarSnapshot{}
}

// eventDateKey resolves the local calendar date a timed event's start falls on, per SPEC-014
// "Timezones" - the provider emits RFC3339 timestamps, which must be converted to the
// household's location before date-keying or a UTC event near local midnight lands on the
// wrong day. All-day events are already plain date strings with no timezone component.
func eventDateKey(ev provider.CalendarEvent, loc *time.Location) string {
	if ev.AllDay {
		return ev.Start
	}
	if t, ok := parseFamilyTime(ev.Start); ok {
		return t.In(loc).Format("2006-01-02")
	}
	return ev.Start
}

// familyDateRange returns every date key (inclusive, "2006-01-02") from startDate through
// endDate, per SPEC-014 "Multi-day events". endDate is the provider's already-normalized
// inclusive last day (see provider.formatEventRange); an empty or earlier endDate collapses
// to the single startDate. Capped well above any realistic display window to bound a
// malformed or absurdly long feed entry.
func familyDateRange(startDate, endDate string) []string {
	start, ok := parseFamilyTime(startDate)
	if !ok {
		return []string{startDate}
	}
	end, ok := parseFamilyTime(endDate)
	if !ok || end.Before(start) {
		end = start
	}
	const maxSpanDays = 400
	dates := make([]string, 0, 1)
	for d, i := start, 0; !d.After(end) && i < maxSpanDays; d, i = d.AddDate(0, 0, 1), i+1 {
		dates = append(dates, d.Format("2006-01-02"))
	}
	return dates
}

func parseFamilyTime(v string) (time.Time, bool) {
	if v == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, true
	}
	if t, err := time.Parse("2006-01-02", v); err == nil {
		return t, true
	}
	return time.Time{}, false
}

func startOfWeek(day time.Time, mondayStart bool) time.Time {
	wd := int(day.Weekday()) // Sunday=0
	if mondayStart {
		wd = (wd + 6) % 7 // Monday=0
	}
	return day.AddDate(0, 0, -wd)
}

func formatFamilyRangeLabel(start, end time.Time) string {
	if start.Month() == end.Month() {
		return fmt.Sprintf("%s – %s", start.Format("Jan 2"), end.Format("2"))
	}
	return fmt.Sprintf("%s – %s", start.Format("Jan 2"), end.Format("Jan 2"))
}

func resolveFamilyDayCount(cfg map[string]any, dims domain.Dimension) int {
	// A numeric value stored as int/float64 in the raw config map (non-string YAML scalar,
	// e.g. `days: 7`) takes precedence over the string path below.
	switch v := cfg["days"].(type) {
	case int:
		if v > 0 {
			return v
		}
	case float64:
		if v > 0 {
			return int(v)
		}
	}

	raw := stringOr(cfg, "days", "auto")
	if raw != "auto" && raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return n
		}
	}
	if dims.Cols >= 6 {
		return 7
	}
	return 5
}

func maxVisibleTimedRows(dims domain.Dimension) int {
	if dims.Cols >= 6 {
		return 4
	}
	return 3
}

func stringOr(cfg map[string]any, key, fallback string) string {
	if cfg == nil {
		return fallback
	}
	if v, ok := cfg[key].(string); ok && strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}

func calendarClaimedBy(m FamilyMember, calendarName string) bool {
	for _, c := range m.Calendars {
		if c == calendarName {
			return true
		}
	}
	return false
}

func parseFamilyMembers(cfg map[string]any) ([]FamilyMember, error) {
	rawList, isList := cfg["members"].([]any)
	if !isList {
		rawList = nil
	}
	members := make([]FamilyMember, 0, len(rawList))
	for idx, item := range rawList {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("members[%d] is not a mapping", idx)
		}
		var name string
		if v, ok := m["name"].(string); ok {
			name = v
		}
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, fmt.Errorf("members[%d] requires non-empty 'name'", idx)
		}
		var color string
		if v, ok := m["color"].(string); ok {
			color = v
		}
		if strings.TrimSpace(color) == "" {
			return nil, fmt.Errorf("member %q requires non-empty 'color'", name)
		}
		var initial string
		if v, ok := m["initial"].(string); ok {
			initial = v
		}
		initial = strings.TrimSpace(initial)
		if initial == "" {
			initial = strings.ToUpper(string([]rune(name)[0]))
		}
		var pattern string
		if v, ok := m["pattern"].(string); ok {
			pattern = v
		}
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			pattern = "solid"
		}
		var calNames []string
		if cl, ok := m["calendars"].([]any); ok {
			for _, c := range cl {
				if s, ok := c.(string); ok {
					calNames = append(calNames, s)
				}
			}
		}
		members = append(members, FamilyMember{
			Name:      name,
			Initial:   initial,
			Color:     color,
			TextColor: contrastTextColor(color),
			Pattern:   pattern,
			Calendars: calNames,
		})
	}
	return members, nil
}

func parseFamilyCalendarSources(cfg map[string]any) map[string]familyCalendarSource {
	out := make(map[string]familyCalendarSource)
	rawList, isList := cfg["calendars"].([]any)
	if !isList {
		rawList = nil
	}
	for _, item := range rawList {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		var name string
		if v, ok := m["name"].(string); ok {
			name = v
		}
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		var color string
		if v, ok := m["color"].(string); ok {
			color = v
		}
		var private bool
		if v, ok := m["private"].(bool); ok {
			private = v
		}
		out[name] = familyCalendarSource{name: name, color: color, private: private}
	}
	return out
}

// contrastTextColor returns "#000000" or "#ffffff", whichever has the higher contrast against
// the given hex background color, per SPEC-014 "Styling".
func contrastTextColor(hex string) string {
	r, g, b, ok := parseHexColor(hex)
	if !ok {
		return "#ffffff"
	}
	// Perceptual luminance (ITU-R BT.601 weighting), sufficient for a binary text-color choice.
	luminance := 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)
	if luminance > 150 {
		return "#000000"
	}
	return "#ffffff"
}

func parseHexColor(hex string) (r, g, b int, ok bool) {
	hex = strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(hex) != 6 {
		return 0, 0, 0, false
	}
	rv, err1 := strconv.ParseInt(hex[0:2], 16, 32)
	gv, err2 := strconv.ParseInt(hex[2:4], 16, 32)
	bv, err3 := strconv.ParseInt(hex[4:6], 16, 32)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, 0, 0, false
	}
	return int(rv), int(gv), int(bv), true
}

package render

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/azylman/mirrormere/internal/domain"
	"github.com/azylman/mirrormere/internal/provider"
)

// FamilyMember represents a resolved member "Member resolution".
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
	Location    string   `json:"location"`
	Description string   `json:"description"`
	Start       string   `json:"start"`
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

// FamilyDay represents one column of the week view "Week (default)".
type FamilyDay struct {
	Date        string           `json:"date"`
	WeekdayAbb  string           `json:"weekday_abbr"`
	DayOfMonth  int              `json:"day_of_month"`
	IsToday     bool             `json:"is_today"`
	IsWeekend   bool             `json:"is_weekend"`
	AllDay      []FamilyEvent    `json:"all_day"`
	Timed       []FamilyEvent    `json:"timed"`
	Overflow    int              `json:"overflow"`
	Events      []FamilyDayEvent `json:"events"`
	EarlyEvents []FamilyDayEvent `json:"early_events"`
	LateEvents  []FamilyDayEvent `json:"late_events"`
}

// FamilySpanEvent represents an all-day event positioned across columns in the week view.
type FamilySpanEvent struct {
	FamilyEvent
	StartCol int `json:"start_col"`
	ColSpan  int `json:"col_span"`
}

// FamilyViewModel is the fully resolved server-side view model consumed by the calendar-family
// week view template "View model".
type FamilyViewModel struct {
	RangeStart   string            `json:"range_start"`
	RangeEnd     string            `json:"range_end"`
	RangeLabel   string            `json:"range_label"`
	Today        string            `json:"today"`
	HoursStart   string            `json:"hours_start"`
	HoursEnd     string            `json:"hours_end"`
	HourMarkers  []string          `json:"hour_markers"`
	Members      []FamilyMember    `json:"members"`
	Days         []FamilyDay       `json:"days"`
	AllDayEvents []FamilySpanEvent `json:"all_day_events"`
	SharedColor  string            `json:"shared_color"`
}

// FamilyDayEvent represents a resolved event positioned within a day view column or edge band.
type FamilyDayEvent struct {
	FamilyEvent
	TopPct      float64 `json:"top_pct"`
	HeightPct   float64 `json:"height_pct"`
	LeftPct     float64 `json:"left_pct"`
	WidthPct    float64 `json:"width_pct"`
	IsEarlyEdge bool    `json:"is_early_edge"`
	IsLateEdge  bool    `json:"is_late_edge"`
	ColSpan     int     `json:"col_span"`
	LinkGlyph   string  `json:"link_glyph"`
	IsLinked    bool    `json:"is_linked"`
}

// FamilyDayColumn represents a single member column (or shared column) in the day view.
type FamilyDayColumn struct {
	Name        string           `json:"name"`
	Initial     string           `json:"initial"`
	Color       string           `json:"color"`
	TextColor   string           `json:"text_color"`
	Pattern     string           `json:"pattern"`
	IsShared    bool             `json:"is_shared"`
	Events      []FamilyDayEvent `json:"events"`
	EarlyEvents []FamilyDayEvent `json:"early_events"`
	LateEvents  []FamilyDayEvent `json:"late_events"`
}

// FamilyDayViewModel is the fully resolved server-side view model consumed by the calendar-family
// day view template.
type FamilyDayViewModel struct {
	Date         string            `json:"date"`
	WeekdayAbbr  string            `json:"weekday_abbr"`
	DayOfMonth   int               `json:"day_of_month"`
	IsToday      bool              `json:"is_today"`
	IsWeekend    bool              `json:"is_weekend"`
	HoursStart   string            `json:"hours_start"`
	HoursEnd     string            `json:"hours_end"`
	HourMarkers  []string          `json:"hour_markers"`
	Members      []FamilyMember    `json:"members"`
	Columns      []FamilyDayColumn `json:"columns"`
	AllDayEvents []FamilyDayEvent  `json:"all_day_events"`
	SharedColor  string            `json:"shared_color"`
}

// HoursBand represents the parsed visible hours window for the day view.
type HoursBand struct {
	StartHour   int      `json:"start_hour"`
	StartMinute int      `json:"start_minute"`
	EndHour     int      `json:"end_hour"`
	EndMinute   int      `json:"end_minute"`
	StartStr    string   `json:"start_str"`
	EndStr      string   `json:"end_str"`
	Markers     []string `json:"markers"`
}

const (
	defaultSharedColor = "#3D405B"
	maxFamilyMembers   = 8
	defaultLinkGlyph   = "🔗"
)

// familyCalendarSource mirrors the subset of the calendar-family config_schema `calendars[]`
// entry needed by the view model (private-calendar redaction and unclaimed-calendar color).
type familyCalendarSource struct {
	name    string
	color   string
	private bool
}

// BuildFamilyView resolves the calendar-agenda provider's CalendarSnapshot payload plus the
// calendar-family widget instance config into the week-view model.
//
// dims is the widget's rendered grid dimensions, used to size the visible-row cutoff
// that produces the "+N more" overflow "Size adaptation".
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

	hours := ParseHoursBand(cfg)

	// Merge shared events by id: an event id seen on multiple member calendars is one event
	// owned by every claiming member, "Shared events".
	merged := make(map[string]*FamilyEvent)
	mergedOrder := make([]string, 0)
	// mergedDates holds every in-window date key a merged event lands on: a single entry for
	// a timed event, or one entry per spanned day for a multi-day all-day event.
	mergedDates := make(map[string][]string)
	mergedOwnerSet := make(map[string]map[string]bool)
	mergedRawEvents := make(map[string]provider.CalendarEvent)

	for _, ev := range snapshot.Events {
		mergedRawEvents[ev.ID] = ev
		var evDates []string
		if ev.AllDay {
			evDates = familyDateRange(ev.Start, ev.End)
		} else {
			evDates = timedEventDateRange(ev, now.Location())
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
				ID:          ev.ID,
				Title:       ev.Title,
				Location:    ev.Location,
				Description: truncateString(ev.Description, 280),
				AllDay:      ev.AllDay,
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
			fe.Description = "Busy"
		}

		for _, m := range members {
			if calendarClaimedBy(m, ev.CalendarName) {
				mergedOwnerSet[ev.ID][m.Name] = true
			}
		}
	}

	// Resolve owners in member order, independent of the order in
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

	var allDayEvents []FamilySpanEvent
	for _, id := range mergedOrder {
		fe := merged[id]
		if !fe.AllDay {
			continue
		}
		dates := mergedDates[id]
		if len(dates) == 0 {
			continue
		}
		minIdx := len(days)
		maxIdx := -1
		for _, d := range dates {
			if idx, ok := dayIndex[d]; ok {
				if idx < minIdx {
					minIdx = idx
				}
				if idx > maxIdx {
					maxIdx = idx
				}
			}
		}
		if minIdx <= maxIdx {
			spanEv := FamilySpanEvent{
				FamilyEvent: *fe,
				StartCol:    minIdx + 1,
				ColSpan:     maxIdx - minIdx + 1,
			}
			allDayEvents = append(allDayEvents, spanEv)
		}
	}

	sort.SliceStable(allDayEvents, func(i, j int) bool {
		if allDayEvents[i].StartCol != allDayEvents[j].StartCol {
			return allDayEvents[i].StartCol < allDayEvents[j].StartCol
		}
		if allDayEvents[i].ColSpan != allDayEvents[j].ColSpan {
			return allDayEvents[i].ColSpan > allDayEvents[j].ColSpan
		}
		return allDayEvents[i].Title < allDayEvents[j].Title
	})

	for i := range days {
		sort.SliceStable(days[i].Timed, func(a, b int) bool {
			return days[i].Timed[a].Start < days[i].Timed[b].Start
		})
		if maxVisible > 0 && len(days[i].Timed) > maxVisible {
			days[i].Overflow = len(days[i].Timed) - maxVisible
			days[i].Timed = days[i].Timed[:maxVisible]
		}

		days[i].Events = make([]FamilyDayEvent, 0, len(days[i].Timed))
		days[i].EarlyEvents = make([]FamilyDayEvent, 0)
		days[i].LateEvents = make([]FamilyDayEvent, 0)
		d := rangeStart.AddDate(0, 0, i)
		dStr := days[i].Date

		for _, id := range mergedOrder {
			fe := merged[id]
			if fe.AllDay {
				continue
			}
			hasDay := false
			for _, dateKey := range mergedDates[id] {
				if dateKey == dStr {
					hasDay = true
					break
				}
			}
			if !hasDay {
				continue
			}

			rawEv := mergedRawEvents[id]
			tStart, ok1 := parseFamilyTime(rawEv.Start)
			tEnd, ok2 := parseFamilyTime(rawEv.End)
			if ok1 {
				if !ok2 || tEnd.Before(tStart) || tEnd.Equal(tStart) {
					tEnd = tStart.Add(30 * time.Minute)
				}
				topPct, heightPct, isEarly, isLate := ComputeDayEventPosition(tStart, tEnd, d, hours)
				dayEv := FamilyDayEvent{
					FamilyEvent: *fe,
					TopPct:      topPct,
					HeightPct:   heightPct,
					IsEarlyEdge: isEarly,
					IsLateEdge:  isLate,
					ColSpan:     1,
				}
				if heightPct > 0 {
					days[i].Events = append(days[i].Events, dayEv)
				}
				if isEarly {
					days[i].EarlyEvents = append(days[i].EarlyEvents, dayEv)
				}
				if isLate {
					days[i].LateEvents = append(days[i].LateEvents, dayEv)
				}
			}
		}

		days[i].Events = layoutDayEvents(days[i].Events)
	}

	return &FamilyViewModel{
		RangeStart:   rangeStart.Format("2006-01-02"),
		RangeEnd:     rangeEnd.Format("2006-01-02"),
		RangeLabel:   formatFamilyRangeLabel(rangeStart, rangeEnd),
		Today:        today.Format("2006-01-02"),
		HoursStart:   hours.StartStr,
		HoursEnd:     hours.EndStr,
		HourMarkers:  hours.Markers,
		Members:      members,
		Days:         days,
		AllDayEvents: allDayEvents,
		SharedColor:  sharedColor,
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

// eventDateKey resolves the local calendar date a timed event's start falls on,
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

// timedEventDateRange returns every date key (inclusive, "2006-01-02") that a timed event
// touches in the household's local timezone. For events spanning across midnight, it returns
// multiple date keys so each affected day column can render its visible portion.
func timedEventDateRange(ev provider.CalendarEvent, loc *time.Location) []string {
	if tStart, ok := parseFamilyTime(ev.Start); ok {
		s := tStart.In(loc)
		e := s
		if tEnd, ok2 := parseFamilyTime(ev.End); ok2 {
			e = tEnd.In(loc)
		}
		if e.Before(s) {
			e = s
		}
		startDate := time.Date(s.Year(), s.Month(), s.Day(), 0, 0, 0, 0, loc)
		endDate := time.Date(e.Year(), e.Month(), e.Day(), 0, 0, 0, 0, loc)
		if e.Equal(endDate) && endDate.After(startDate) {
			endDate = endDate.AddDate(0, 0, -1)
		}
		var dates []string
		for cur := startDate; !cur.After(endDate); cur = cur.AddDate(0, 0, 1) {
			dates = append(dates, cur.Format("2006-01-02"))
			if len(dates) >= 30 {
				break
			}
		}
		return dates
	}
	return []string{eventDateKey(ev, loc)}
}

const maxDayLanes = 4

// layoutDayEvents partitions overlapping canvas events into parallel horizontal lanes,
// calculating LeftPct and WidthPct so concurrent events don't obscure each other.
func layoutDayEvents(events []FamilyDayEvent) []FamilyDayEvent {
	if len(events) == 0 {
		return events
	}
	if len(events) == 1 {
		events[0].LeftPct = 0
		events[0].WidthPct = 100
		return events
	}

	sort.SliceStable(events, func(i, j int) bool {
		if events[i].TopPct != events[j].TopPct {
			return events[i].TopPct < events[j].TopPct
		}
		return events[i].HeightPct > events[j].HeightPct
	})

	type clusterRange struct {
		startIdx int
		endIdx   int
	}
	var clusters []clusterRange
	cStart := 0
	cMaxBottom := events[0].TopPct + events[0].HeightPct

	for i := 1; i < len(events); i++ {
		evStart := events[i].TopPct
		evEnd := events[i].TopPct + events[i].HeightPct
		if evStart < cMaxBottom {
			if evEnd > cMaxBottom {
				cMaxBottom = evEnd
			}
		} else {
			clusters = append(clusters, clusterRange{startIdx: cStart, endIdx: i})
			cStart = i
			cMaxBottom = evEnd
		}
	}
	clusters = append(clusters, clusterRange{startIdx: cStart, endIdx: len(events)})

	for _, c := range clusters {
		var lanes []float64
		assignedLanes := make([]int, c.endIdx-c.startIdx)

		for k := c.startIdx; k < c.endIdx; k++ {
			idx := k - c.startIdx
			evStart := events[k].TopPct
			evEnd := events[k].TopPct + events[k].HeightPct
			assigned := false
			for laneIdx, laneEnd := range lanes {
				if evStart >= laneEnd {
					lanes[laneIdx] = evEnd
					assignedLanes[idx] = laneIdx
					assigned = true
					break
				}
			}
			if !assigned {
				assignedLanes[idx] = len(lanes)
				lanes = append(lanes, evEnd)
			}
		}

		numLanes := len(lanes)
		if numLanes > maxDayLanes {
			numLanes = maxDayLanes
		}
		laneWidth := roundToTwoDecimals(100.0 / float64(numLanes))

		for k := c.startIdx; k < c.endIdx; k++ {
			idx := k - c.startIdx
			lane := assignedLanes[idx]
			if lane >= maxDayLanes {
				lane = maxDayLanes - 1
			}
			events[k].LeftPct = roundToTwoDecimals(float64(lane) * (100.0 / float64(numLanes)))
			events[k].WidthPct = laneWidth
		}
	}

	return events
}

// familyDateRange returns every date key (inclusive, "2006-01-02") from startDate through
// endDate, "Multi-day events". endDate is the provider's already-normalized
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

func maxMemberColumns(dims domain.Dimension) int {
	if dims.Cols >= 6 {
		return 8
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
// the given hex background color, "Styling".
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

// ParseHoursBand parses the `hours` array from config, validating that start < end.
// Defaults to 07:00–21:00 if absent, invalid, or inverted.
func ParseHoursBand(cfg map[string]any) HoursBand {
	defaultBand := defaultHoursBand()
	if cfg == nil {
		return defaultBand
	}
	var rawItems []string
	switch v := cfg["hours"].(type) {
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				rawItems = append(rawItems, s)
			}
		}
	case []string:
		rawItems = v
	}
	if len(rawItems) < 2 {
		return defaultBand
	}

	hStart, mStart, ok1 := parseClockTime(rawItems[0])
	hEnd, mEnd, ok2 := parseClockTime(rawItems[1])
	if !ok1 || !ok2 {
		return defaultBand
	}

	totalStart := hStart*60 + mStart
	totalEnd := hEnd*60 + mEnd
	if totalStart >= totalEnd {
		return defaultBand
	}

	markers := generateHourMarkers(hStart, mStart, hEnd, mEnd)
	return HoursBand{
		StartHour:   hStart,
		StartMinute: mStart,
		EndHour:     hEnd,
		EndMinute:   mEnd,
		StartStr:    fmt.Sprintf("%02d:%02d", hStart, mStart),
		EndStr:      fmt.Sprintf("%02d:%02d", hEnd, mEnd),
		Markers:     markers,
	}
}

func defaultHoursBand() HoursBand {
	return HoursBand{
		StartHour:   7,
		StartMinute: 0,
		EndHour:     21,
		EndMinute:   0,
		StartStr:    "07:00",
		EndStr:      "21:00",
		Markers:     generateHourMarkers(7, 0, 21, 0),
	}
}

func generateHourMarkers(hStart, mStart, hEnd, mEnd int) []string {
	var markers []string
	firstHour := hStart
	if mStart > 0 {
		markers = append(markers, fmt.Sprintf("%02d:%02d", hStart, mStart))
		firstHour = hStart + 1
	}
	for h := firstHour; h <= hEnd; h++ {
		if h == hEnd && mEnd > 0 {
			markers = append(markers, fmt.Sprintf("%02d:00", h))
			markers = append(markers, fmt.Sprintf("%02d:%02d", hEnd, mEnd))
			break
		}
		markers = append(markers, fmt.Sprintf("%02d:00", h))
	}
	return markers
}

func parseClockTime(v string) (hour, minute int, ok bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, 0, false
	}
	if strings.Contains(v, ":") {
		parts := strings.Split(v, ":")
		if len(parts) != 2 {
			return 0, 0, false
		}
		h, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
		m, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err1 != nil || err2 != nil {
			return 0, 0, false
		}
		if h < 0 || h > 24 || m < 0 || m > 59 {
			return 0, 0, false
		}
		if h == 24 && m > 0 {
			return 0, 0, false
		}
		return h, m, true
	}

	h, err := strconv.Atoi(v)
	if err != nil || h < 0 || h > 24 {
		return 0, 0, false
	}
	return h, 0, true
}

// ComputeDayEventPosition calculates the vertical TopPct and HeightPct for an event on a given day
// relative to the visible HoursBand, along with flags for early/late edge collapsing.
//
// day is the reference date for the column/view (time of day is ignored, local midnight used).
//
// If event starts before the visible hours window, isEarlyEdge is true.
// If event ends after the visible hours window, isLateEdge is true.
// For events crossing midnight or multi-day events, the event bounds are evaluated relative
// to the target day's hours window.
func ComputeDayEventPosition(eventStart, eventEnd time.Time, day time.Time, hours HoursBand) (topPct, heightPct float64, isEarlyEdge, isLateEdge bool) {
	loc := day.Location()
	wStart := time.Date(day.Year(), day.Month(), day.Day(), hours.StartHour, hours.StartMinute, 0, 0, loc)
	wEnd := time.Date(day.Year(), day.Month(), day.Day(), hours.EndHour, hours.EndMinute, 0, 0, loc)

	wDuration := wEnd.Sub(wStart).Seconds()
	if wDuration <= 0 {
		wStart = time.Date(day.Year(), day.Month(), day.Day(), 7, 0, 0, 0, loc)
		wEnd = time.Date(day.Year(), day.Month(), day.Day(), 21, 0, 0, 0, loc)
		wDuration = wEnd.Sub(wStart).Seconds()
	}

	start := eventStart.In(loc)
	end := eventEnd.In(loc)
	if end.Before(start) || end.Equal(start) {
		end = start.Add(30 * time.Minute)
	}

	if start.Before(wStart) {
		isEarlyEdge = true
	}
	if end.After(wEnd) {
		isLateEdge = true
	}

	effStart := start
	if effStart.Before(wStart) {
		effStart = wStart
	}
	effEnd := end
	if effEnd.After(wEnd) {
		effEnd = wEnd
	}

	if effEnd.After(effStart) && !start.After(wEnd) && !end.Before(wStart) {
		topPct = (effStart.Sub(wStart).Seconds() / wDuration) * 100.0
		heightPct = (effEnd.Sub(effStart).Seconds() / wDuration) * 100.0
		// Ensure visible events have at least 2.0% height for legibility and tap targets
		if heightPct < 2.0 {
			heightPct = 2.0
		}
		if topPct+heightPct > 100.0 {
			topPct = 100.0 - heightPct
		}
	} else {
		if start.After(wEnd) || start.Equal(wEnd) {
			topPct = 100.0
			heightPct = 0.0
		} else {
			topPct = 0.0
			heightPct = 0.0
		}
	}

	topPct = roundToTwoDecimals(topPct)
	heightPct = roundToTwoDecimals(heightPct)
	return
}

func roundToTwoDecimals(val float64) float64 {
	return math.Round(val*100) / 100
}

func truncateString(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) > maxRunes {
		if maxRunes > 1 {
			return string(runes[:maxRunes-1]) + "…"
		}
		return string(runes[:maxRunes])
	}
	return s
}

// BuildFamilyDayView resolves the calendar-agenda provider's CalendarSnapshot payload plus the
// calendar-family widget instance config into the single-day view model.
func BuildFamilyDayView(data any, cfg map[string]any, dims domain.Dimension, now time.Time) (*FamilyDayViewModel, error) {
	members, err := parseFamilyMembers(cfg)
	if err != nil {
		return nil, err
	}
	calendars := parseFamilyCalendarSources(cfg)
	sharedColor := stringOr(cfg, "shared_color", defaultSharedColor)
	hours := ParseHoursBand(cfg)

	loc := now.Location()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	if dateStr, ok := cfg["date"].(string); ok && strings.TrimSpace(dateStr) != "" {
		if t, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(dateStr), loc); err == nil {
			today = t
		}
	}

	snapshot := extractCalendarSnapshot(data)
	dayDateStr := today.Format("2006-01-02")
	dayStart := today
	dayEnd := today.AddDate(0, 0, 1)

	maxCols := maxMemberColumns(dims)
	activeCount := len(members)
	if activeCount > maxCols {
		activeCount = maxCols
	}

	activeMembers := members[:activeCount]
	collapsedMembers := members[activeCount:]

	columns := make([]FamilyDayColumn, len(activeMembers))
	memberColMap := make(map[string]int, len(activeMembers))
	for i, m := range activeMembers {
		columns[i] = FamilyDayColumn{
			Name:        m.Name,
			Initial:     m.Initial,
			Color:       m.Color,
			TextColor:   m.TextColor,
			Pattern:     m.Pattern,
			IsShared:    false,
			Events:      make([]FamilyDayEvent, 0),
			EarlyEvents: make([]FamilyDayEvent, 0),
			LateEvents:  make([]FamilyDayEvent, 0),
		}
		memberColMap[m.Name] = i
	}

	collapsedMemberMap := make(map[string]bool, len(collapsedMembers))
	for _, m := range collapsedMembers {
		collapsedMemberMap[m.Name] = true
	}

	sharedCol := FamilyDayColumn{
		Name:        "Shared",
		Initial:     "S",
		Color:       sharedColor,
		TextColor:   contrastTextColor(sharedColor),
		Pattern:     "solid",
		IsShared:    true,
		Events:      make([]FamilyDayEvent, 0),
		EarlyEvents: make([]FamilyDayEvent, 0),
		LateEvents:  make([]FamilyDayEvent, 0),
	}

	var allDayEvents []FamilyDayEvent

	merged := make(map[string]*FamilyEvent)
	mergedOrder := make([]string, 0)
	mergedOwnerSet := make(map[string]map[string]bool)
	mergedRawEvents := make(map[string]provider.CalendarEvent)

	for _, ev := range snapshot.Events {
		var touchesDay bool
		if ev.AllDay {
			for _, d := range familyDateRange(ev.Start, ev.End) {
				if d == dayDateStr {
					touchesDay = true
					break
				}
			}
		} else {
			tStart, ok1 := parseFamilyTime(ev.Start)
			tEnd, ok2 := parseFamilyTime(ev.End)
			if ok1 {
				tStartLoc := tStart.In(loc)
				tEndLoc := tStartLoc.Add(30 * time.Minute)
				if ok2 {
					tEndLoc = tEnd.In(loc)
				}
				if tStartLoc.Before(dayEnd) && tEndLoc.After(dayStart) {
					touchesDay = true
				}
			}
		}

		if !touchesDay {
			continue
		}

		fe, exists := merged[ev.ID]
		if !exists {
			fe = &FamilyEvent{
				ID:          ev.ID,
				Title:       ev.Title,
				Location:    ev.Location,
				Description: truncateString(ev.Description, 280),
				AllDay:      ev.AllDay,
			}
			if !ev.AllDay {
				if t, ok := parseFamilyTime(ev.Start); ok {
					fe.Start = t.In(loc).Format("15:04")
				}
				if t, ok := parseFamilyTime(ev.End); ok {
					fe.End = t.In(loc).Format("15:04")
				}
			}
			merged[ev.ID] = fe
			mergedOrder = append(mergedOrder, ev.ID)
			mergedOwnerSet[ev.ID] = make(map[string]bool)
			mergedRawEvents[ev.ID] = ev
		}

		if src, ok := calendars[ev.CalendarName]; ok && src.private {
			fe.Title = "Busy"
			fe.Location = ""
			fe.Description = "Busy"
		}

		for _, m := range members {
			if calendarClaimedBy(m, ev.CalendarName) {
				mergedOwnerSet[ev.ID][m.Name] = true
			}
		}
	}

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
		if len(fe.Colors) > 0 {
			fe.Color = fe.Colors[0]
		} else {
			fe.Color = sharedColor
		}
		if len(fe.Patterns) > 0 {
			fe.Pattern = fe.Patterns[0]
		} else {
			fe.Pattern = "solid"
		}
		fe.TextColor = contrastTextColor(fe.Color)

		rawEv := mergedRawEvents[id]
		if fe.AllDay {
			dayEv := FamilyDayEvent{
				FamilyEvent: *fe,
				ColSpan:     1,
			}
			allDayEvents = append(allDayEvents, dayEv)
			continue
		}

		tStart, _ := parseFamilyTime(rawEv.Start)
		tEnd, okEnd := parseFamilyTime(rawEv.End)
		tStartLoc := tStart.In(loc)
		tEndLoc := tStartLoc.Add(30 * time.Minute)
		if okEnd {
			tEndLoc = tEnd.In(loc)
		}
		topPct, heightPct, isEarly, isLate := ComputeDayEventPosition(tStartLoc, tEndLoc, today, hours)
		dayEv := FamilyDayEvent{
			FamilyEvent: *fe,
			TopPct:      topPct,
			HeightPct:   heightPct,
			IsEarlyEdge: isEarly,
			IsLateEdge:  isLate,
			ColSpan:     1,
		}

		if len(fe.Owners) == 0 {
			dispatchDayEvent(&sharedCol, dayEv)
			continue
		}

		var activeCols []int
		var hasCollapsed bool
		for _, ownerName := range fe.Owners {
			if colIdx, ok := memberColMap[ownerName]; ok {
				activeCols = append(activeCols, colIdx)
			} else if collapsedMemberMap[ownerName] {
				hasCollapsed = true
			}
		}

		if len(activeCols) == 0 {
			// All claiming members are collapsed into the Shared column
			dispatchDayEvent(&sharedCol, dayEv)
			continue
		}

		if !hasCollapsed {
			if len(activeCols) == 1 {
				dispatchDayEvent(&columns[activeCols[0]], dayEv)
				continue
			}

			// Check if active columns are contiguous/adjacent
			isAdjacent := (activeCols[len(activeCols)-1] - activeCols[0]) == (len(activeCols) - 1)
			if isAdjacent {
				dayEv.ColSpan = len(activeCols)
				dispatchDayEvent(&columns[activeCols[0]], dayEv)
				continue
			}
		}

		// Non-adjacent columns or mixed active and collapsed members:
		// Duplicate event block into each claiming column with link indicator glyph.
		dayEv.ColSpan = 1
		dayEv.IsLinked = true
		dayEv.LinkGlyph = defaultLinkGlyph

		for _, colIdx := range activeCols {
			copyEv := dayEv
			copyEv.Color = columns[colIdx].Color
			copyEv.TextColor = columns[colIdx].TextColor
			copyEv.Pattern = columns[colIdx].Pattern
			dispatchDayEvent(&columns[colIdx], copyEv)
		}

		if hasCollapsed {
			dispatchDayEvent(&sharedCol, dayEv)
		}
	}

	if len(sharedCol.Events) > 0 || len(sharedCol.EarlyEvents) > 0 || len(sharedCol.LateEvents) > 0 {
		columns = append(columns, sharedCol)
	}

	for i := range columns {
		sort.SliceStable(columns[i].Events, func(a, b int) bool {
			if columns[i].Events[a].TopPct != columns[i].Events[b].TopPct {
				return columns[i].Events[a].TopPct < columns[i].Events[b].TopPct
			}
			return columns[i].Events[a].Start < columns[i].Events[b].Start
		})
		sort.SliceStable(columns[i].EarlyEvents, func(a, b int) bool {
			return columns[i].EarlyEvents[a].Start < columns[i].EarlyEvents[b].Start
		})
		sort.SliceStable(columns[i].LateEvents, func(a, b int) bool {
			return columns[i].LateEvents[a].Start < columns[i].LateEvents[b].Start
		})
	}

	return &FamilyDayViewModel{
		Date:         dayDateStr,
		WeekdayAbbr:  today.Format("Mon"),
		DayOfMonth:   today.Day(),
		IsToday:      today.Year() == now.Year() && today.YearDay() == now.YearDay(),
		IsWeekend:    today.Weekday() == time.Saturday || today.Weekday() == time.Sunday,
		HoursStart:   hours.StartStr,
		HoursEnd:     hours.EndStr,
		HourMarkers:  hours.Markers,
		Members:      members,
		Columns:      columns,
		AllDayEvents: allDayEvents,
		SharedColor:  sharedColor,
	}, nil
}

func dispatchDayEvent(col *FamilyDayColumn, ev FamilyDayEvent) {
	if ev.HeightPct > 0 {
		col.Events = append(col.Events, ev)
	}
	if ev.IsEarlyEdge {
		col.EarlyEvents = append(col.EarlyEvents, ev)
	}
	if ev.IsLateEdge {
		col.LateEvents = append(col.LateEvents, ev)
	}
}

// FamilyMonthDot represents a colored dot indicator for a member with events on a day in Month view.
type FamilyMonthDot struct {
	Name        string `json:"name"`
	Initial     string `json:"initial"`
	Color       string `json:"color"`
	TextColor   string `json:"text_color"`
	Pattern     string `json:"pattern"`
	IsShared    bool   `json:"is_shared"`
	MemberIndex int    `json:"member_index"`
}

// FamilyMonthDay represents a single calendar cell in the 42-day Month view grid.
type FamilyMonthDay struct {
	Date           string           `json:"date"`
	DayOfMonth     int              `json:"day_of_month"`
	WeekdayAbbr    string           `json:"weekday_abbr"`
	IsCurrentMonth bool             `json:"is_current_month"`
	IsToday        bool             `json:"is_today"`
	IsWeekend      bool             `json:"is_weekend"`
	Dots           []FamilyMonthDot `json:"dots"`
	EventTitle     string           `json:"event_title,omitempty"`
	EventsCount    int              `json:"events_count"`
	Events         []FamilyEvent    `json:"events,omitempty"`
}

// FamilyMonthWeek represents one 7-day row of the 42-day Month view grid.
type FamilyMonthWeek struct {
	WeekNumber int              `json:"week_number"`
	Days       []FamilyMonthDay `json:"days"`
}

// FamilyMonthViewModel is the fully resolved server-side view model consumed by the calendar-family
// month view template.
type FamilyMonthViewModel struct {
	MonthLabel     string            `json:"month_label"`
	MonthName      string            `json:"month_name"`
	Year           int               `json:"year"`
	Month          int               `json:"month"`
	RangeStart     string            `json:"range_start"`
	RangeEnd       string            `json:"range_end"`
	Today          string            `json:"today"`
	WeekdayHeaders []string          `json:"weekday_headers"`
	Members        []FamilyMember    `json:"members"`
	Weeks          []FamilyMonthWeek `json:"weeks"`
	Days           []FamilyMonthDay  `json:"days"`
	SharedColor    string            `json:"shared_color"`
}

// BuildFamilyMonthView resolves the calendar-agenda provider's CalendarSnapshot payload plus the
// calendar-family widget instance config into the 42-day month view model.
func BuildFamilyMonthView(data any, cfg map[string]any, dims domain.Dimension, now time.Time) (*FamilyMonthViewModel, error) {
	members, err := parseFamilyMembers(cfg)
	if err != nil {
		return nil, err
	}
	calendars := parseFamilyCalendarSources(cfg)
	sharedColor := stringOr(cfg, "shared_color", defaultSharedColor)
	weekStartsMonday := strings.EqualFold(stringOr(cfg, "week_starts", "sunday"), "monday")

	loc := now.Location()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	target := today
	if mStr, ok := cfg["month"].(string); ok && strings.TrimSpace(mStr) != "" {
		mStr = strings.TrimSpace(mStr)
		if t, err := time.ParseInLocation("2006-01", mStr, loc); err == nil {
			target = t
		} else if t, err := time.ParseInLocation("2006-01-02", mStr, loc); err == nil {
			target = t
		}
	} else if dStr, ok := cfg["date"].(string); ok && strings.TrimSpace(dStr) != "" {
		dStr = strings.TrimSpace(dStr)
		if t, err := time.ParseInLocation("2006-01-02", dStr, loc); err == nil {
			target = t
		} else if t, err := time.ParseInLocation("2006-01", dStr, loc); err == nil {
			target = t
		}
	}

	firstOfMonth := time.Date(target.Year(), target.Month(), 1, 0, 0, 0, 0, loc)
	targetMonth := firstOfMonth.Month()
	targetYear := firstOfMonth.Year()

	windowStart := startOfWeek(firstOfMonth, weekStartsMonday)
	const (
		monthGridDays = 42
		daysPerWeek   = 7
		numWeeks      = 6
	)
	windowEnd := windowStart.AddDate(0, 0, monthGridDays-1)

	dayIndex := make(map[string]int, monthGridDays)
	days := make([]FamilyMonthDay, monthGridDays)
	for i := 0; i < monthGridDays; i++ {
		d := windowStart.AddDate(0, 0, i)
		dStr := d.Format("2006-01-02")
		dayIndex[dStr] = i
		days[i] = FamilyMonthDay{
			Date:           dStr,
			DayOfMonth:     d.Day(),
			WeekdayAbbr:    d.Format("Mon"),
			IsCurrentMonth: d.Year() == targetYear && d.Month() == targetMonth,
			IsToday:        d.Year() == today.Year() && d.YearDay() == today.YearDay(),
			IsWeekend:      d.Weekday() == time.Saturday || d.Weekday() == time.Sunday,
			Dots:           make([]FamilyMonthDot, 0),
		}
	}

	snapshot := extractCalendarSnapshot(data)
	merged := make(map[string]*FamilyEvent)
	mergedOrder := make([]string, 0)
	mergedDates := make(map[string][]string)
	mergedOwnerSet := make(map[string]map[string]bool)

	for _, ev := range snapshot.Events {
		var evDates []string
		if ev.AllDay {
			evDates = familyDateRange(ev.Start, ev.End)
		} else {
			tStart, ok1 := parseFamilyTime(ev.Start)
			tEnd, ok2 := parseFamilyTime(ev.End)
			if ok1 {
				tStartLoc := tStart.In(loc)
				d1 := tStartLoc.Format("2006-01-02")
				if ok2 && tEnd.After(tStart) {
					tEndLoc := tEnd.In(loc)
					d2 := tEndLoc.Format("2006-01-02")
					if tEndLoc.Hour() == 0 && tEndLoc.Minute() == 0 && tEndLoc.Second() == 0 && d2 > d1 {
						d2 = tEndLoc.Add(-1 * time.Second).Format("2006-01-02")
					}
					evDates = familyDateRange(d1, d2)
				} else {
					evDates = []string{d1}
				}
			} else {
				evDates = []string{ev.Start}
			}
		}

		var matchedDates []string
		for _, d := range evDates {
			if _, ok := dayIndex[d]; ok {
				matchedDates = append(matchedDates, d)
			}
		}
		if len(matchedDates) == 0 {
			continue
		}

		fe, exists := merged[ev.ID]
		if !exists {
			fe = &FamilyEvent{
				ID:          ev.ID,
				Title:       ev.Title,
				Location:    ev.Location,
				Description: truncateString(ev.Description, 280),
				AllDay:      ev.AllDay,
			}
			if !ev.AllDay {
				if t, ok := parseFamilyTime(ev.Start); ok {
					fe.Start = t.In(loc).Format("15:04")
				}
				if t, ok := parseFamilyTime(ev.End); ok {
					fe.End = t.In(loc).Format("15:04")
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
			fe.Description = "Busy"
		}

		for _, m := range members {
			if calendarClaimedBy(m, ev.CalendarName) {
				mergedOwnerSet[ev.ID][m.Name] = true
			}
		}
	}

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
		if len(fe.Colors) > 0 {
			fe.Color = fe.Colors[0]
		} else {
			fe.Color = sharedColor
		}
		if len(fe.Patterns) > 0 {
			fe.Pattern = fe.Patterns[0]
		} else {
			fe.Pattern = "solid"
		}
		fe.TextColor = contrastTextColor(fe.Color)
	}

	dayEventsMap := make(map[int][]FamilyEvent, monthGridDays)
	dayMembersMap := make(map[int]map[string]bool, monthGridDays)
	dayUnclaimedMap := make(map[int]bool, monthGridDays)

	for _, id := range mergedOrder {
		fe := merged[id]
		for _, dStr := range mergedDates[id] {
			idx := dayIndex[dStr]
			dayEventsMap[idx] = append(dayEventsMap[idx], *fe)
			if dayMembersMap[idx] == nil {
				dayMembersMap[idx] = make(map[string]bool)
			}
			if len(fe.Owners) == 0 {
				dayUnclaimedMap[idx] = true
			} else {
				for _, owner := range fe.Owners {
					dayMembersMap[idx][owner] = true
				}
			}
		}
	}

	showTitles := dims.Cols >= 6
	for idx := range days {
		evs := dayEventsMap[idx]
		sort.SliceStable(evs, func(a, b int) bool {
			if evs[a].AllDay != evs[b].AllDay {
				return evs[a].AllDay && !evs[b].AllDay
			}
			if evs[a].Start != evs[b].Start {
				return evs[a].Start < evs[b].Start
			}
			return evs[a].Title < evs[b].Title
		})

		days[idx].EventsCount = len(evs)
		if showTitles {
			days[idx].Events = evs
			if len(evs) > 0 {
				days[idx].EventTitle = evs[0].Title
			}
		}

		memberSet := dayMembersMap[idx]
		var dots []FamilyMonthDot
		for mIdx, m := range members {
			if memberSet != nil && memberSet[m.Name] {
				dots = append(dots, FamilyMonthDot{
					Name:        m.Name,
					Initial:     m.Initial,
					Color:       m.Color,
					TextColor:   m.TextColor,
					Pattern:     m.Pattern,
					IsShared:    false,
					MemberIndex: mIdx,
				})
			}
		}
		if dayUnclaimedMap[idx] {
			dots = append(dots, FamilyMonthDot{
				Name:        "Shared",
				Initial:     "S",
				Color:       sharedColor,
				TextColor:   contrastTextColor(sharedColor),
				Pattern:     "solid",
				IsShared:    true,
				MemberIndex: -1,
			})
		}
		days[idx].Dots = dots
	}

	weeks := make([]FamilyMonthWeek, numWeeks)
	for w := 0; w < numWeeks; w++ {
		weekDays := make([]FamilyMonthDay, daysPerWeek)
		copy(weekDays, days[w*daysPerWeek:(w+1)*daysPerWeek])
		weeks[w] = FamilyMonthWeek{
			WeekNumber: w + 1,
			Days:       weekDays,
		}
	}

	return &FamilyMonthViewModel{
		MonthLabel:     firstOfMonth.Format("January 2006"),
		MonthName:      firstOfMonth.Format("January"),
		Year:           targetYear,
		Month:          int(targetMonth),
		RangeStart:     windowStart.Format("2006-01-02"),
		RangeEnd:       windowEnd.Format("2006-01-02"),
		Today:          today.Format("2006-01-02"),
		WeekdayHeaders: weekdayHeaders(weekStartsMonday),
		Members:        members,
		Weeks:          weeks,
		Days:           days,
		SharedColor:    sharedColor,
	}, nil
}

func weekdayHeaders(mondayStart bool) []string {
	if mondayStart {
		return []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}
	}
	return []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
}

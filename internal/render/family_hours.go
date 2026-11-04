package render

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/azylman/mirrormere/internal/provider"
)

func formatTimeLabel(h, m int, timeFormat string) string {
	if strings.EqualFold(timeFormat, "12h") {
		hMod := (h%24 + 24) % 24
		period := "am"
		if hMod >= 12 {
			period = "pm"
		}
		h12 := hMod % 12
		if h12 == 0 {
			h12 = 12
		}
		if m == 0 {
			return fmt.Sprintf("%d%s", h12, period)
		}
		return fmt.Sprintf("%d:%02d%s", h12, m, period)
	}
	return fmt.Sprintf("%02d:%02d", h, m)
}

func newHoursBand(hStart, mStart, hEnd, mEnd int, timeFormat ...string) HoursBand {
	tf := "24h"
	if len(timeFormat) > 0 && timeFormat[0] != "" {
		tf = timeFormat[0]
	}
	return HoursBand{
		StartHour:       hStart,
		StartMinute:     mStart,
		EndHour:         hEnd,
		EndMinute:       mEnd,
		StartStr:        fmt.Sprintf("%02d:%02d", hStart, mStart),
		EndStr:          fmt.Sprintf("%02d:%02d", hEnd, mEnd),
		Markers:         generateHourMarkers(hStart, mStart, hEnd, mEnd, tf),
		TimelineMarkers: generateTimelineMarkers(hStart, mStart, hEnd, mEnd, tf),
	}
}

func parseExplicitHoursBand(cfg map[string]any) (HoursBand, bool) {
	if cfg == nil {
		return HoursBand{}, false
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
		return HoursBand{}, false
	}

	hStart, mStart, ok1 := parseClockTime(rawItems[0])
	hEnd, mEnd, ok2 := parseClockTime(rawItems[1])
	if !ok1 || !ok2 {
		return HoursBand{}, false
	}

	totalStart := hStart*60 + mStart
	totalEnd := hEnd*60 + mEnd
	if totalStart >= totalEnd {
		return HoursBand{}, false
	}

	return newHoursBand(hStart, mStart, hEnd, mEnd, stringOr(cfg, "time_format", "24h")), true
}

func defaultHoursBand(timeFormat ...string) HoursBand {
	tf := "24h"
	if len(timeFormat) > 0 && timeFormat[0] != "" {
		tf = timeFormat[0]
	}
	return newHoursBand(7, 0, 21, 0, tf)
}

// resolveFamilyHoursBand resolves the timeline hours band for the week view.
// If cfg specifies an explicit valid hours band, that configuration is respected.
// Otherwise, the timeline starts at the earliest start time of timed events for the week
// (floored to 30-minute intervals for clean timeline alignment), with a minimum 4-hour
// window and a fallback to 07:00–21:00 when no events or only late events exist.
func resolveFamilyHoursBand(cfg map[string]any, events []provider.CalendarEvent, dayIndex map[string]int, loc *time.Location) HoursBand {
	timeFormat := stringOr(cfg, "time_format", "24h")
	if loc == nil {
		loc = time.UTC
	}

	var band HoursBand
	if explicitBand, ok := parseExplicitHoursBand(cfg); ok {
		band = explicitBand
	} else {
		band = computeDynamicHoursBand(events, dayIndex, loc, timeFormat)
	}

	return applyCollapsedMiddayGaps(band, events, dayIndex, loc, timeFormat)
}

func computeDynamicHoursBand(events []provider.CalendarEvent, dayIndex map[string]int, loc *time.Location, timeFormat string) HoursBand {
	defaultBand := defaultHoursBand(timeFormat)
	endH := defaultBand.EndHour
	endM := defaultBand.EndMinute
	endTotal := endH*60 + endM

	earliestMinutes := -1
	for _, ev := range events {
		if ev.AllDay {
			continue
		}
		tStart, ok := parseFamilyTime(ev.Start)
		if !ok {
			continue
		}
		startLocal := tStart.In(loc)
		dateStr := startLocal.Format("2006-01-02")
		if _, inWeek := dayIndex[dateStr]; !inWeek {
			continue
		}
		mins := startLocal.Hour()*60 + startLocal.Minute()
		if earliestMinutes == -1 || mins < earliestMinutes {
			earliestMinutes = mins
		}
	}

	// If no timed events start in the week, or if the window would be less than 4 hours (240 mins),
	// fall back to default 07:00.
	startH := defaultBand.StartHour
	startM := defaultBand.StartMinute
	if earliestMinutes >= 0 {
		floorMins := (earliestMinutes / 30) * 30
		if endTotal-floorMins >= 240 {
			startH = floorMins / 60
			startM = floorMins % 60
		}
	}

	return newHoursBand(startH, startM, endH, endM, timeFormat)
}

func applyCollapsedMiddayGaps(band HoursBand, events []provider.CalendarEvent, dayIndex map[string]int, loc *time.Location, timeFormat string) HoursBand {
	if len(events) == 0 || len(dayIndex) == 0 {
		return band
	}
	if loc == nil {
		loc = time.UTC
	}

	firstActiveHour := band.StartHour
	if band.StartMinute > 0 {
		firstActiveHour++
	}
	lastActiveHour := band.EndHour
	if lastActiveHour-firstActiveHour < minCollapsedGapHours+2 {
		return band
	}

	occupied := collectOccupiedWeekHours(events, dayIndex, loc, firstActiveHour, lastActiveHour)
	minOccupied, maxOccupied := occupiedHourBounds(occupied, firstActiveHour, lastActiveHour)

	// Only collapse interior spans strictly between the first and last active event hours.
	if minOccupied == -1 || maxOccupied <= minOccupied+minCollapsedGapHours {
		return band
	}

	gaps := buildCollapsedHourGaps(occupied, minOccupied, maxOccupied, timeFormat)
	if len(gaps) == 0 {
		return band
	}

	band.CollapsedGaps = gaps
	for i := range band.CollapsedGaps {
		g := &band.CollapsedGaps[i]
		top := mapMinutesToCanvasPct(float64(g.StartHour*60), band)
		bot := mapMinutesToCanvasPct(float64(g.EndHour*60), band)
		g.TopPct = roundToTwoDecimals(top)
		g.HeightPct = roundToTwoDecimals(bot - top)
	}
	band.TimelineMarkers = generateTimelineMarkersWithGaps(band, timeFormat)
	return band
}

func collectOccupiedWeekHours(events []provider.CalendarEvent, dayIndex map[string]int, loc *time.Location, firstActiveHour, lastActiveHour int) map[int]bool {
	occupied := make(map[int]bool, lastActiveHour-firstActiveHour)
	for _, ev := range events {
		if ev.AllDay {
			continue
		}
		tStart, ok := parseFamilyTime(ev.Start)
		if !ok {
			continue
		}
		tEnd, ok2 := parseFamilyTime(ev.End)
		if !ok2 || !tEnd.After(tStart) {
			tEnd = tStart.Add(30 * time.Minute)
		}
		for _, dStr := range timedEventDateRange(ev, loc) {
			if _, inWindow := dayIndex[dStr]; !inWindow {
				continue
			}
			markOccupiedEventHoursForDay(occupied, dStr, tStart, tEnd, loc, firstActiveHour, lastActiveHour)
		}
	}
	return occupied
}

func markOccupiedEventHoursForDay(occupied map[int]bool, dStr string, tStart, tEnd time.Time, loc *time.Location, firstActiveHour, lastActiveHour int) {
	dayTime, err := time.ParseInLocation("2006-01-02", dStr, loc)
	if err != nil {
		return
	}
	startLocal := tStart.In(loc)
	endLocal := tEnd.In(loc)
	for h := firstActiveHour; h < lastActiveHour; h++ {
		slotStart := time.Date(dayTime.Year(), dayTime.Month(), dayTime.Day(), h, 0, 0, 0, loc)
		slotEnd := slotStart.Add(time.Hour)
		if startLocal.Before(slotEnd) && endLocal.After(slotStart) {
			occupied[h] = true
		}
	}
}

func occupiedHourBounds(occupied map[int]bool, firstActiveHour, lastActiveHour int) (int, int) {
	minOccupied := -1
	maxOccupied := -1
	for h := firstActiveHour; h < lastActiveHour; h++ {
		if !occupied[h] {
			continue
		}
		if minOccupied == -1 || h < minOccupied {
			minOccupied = h
		}
		if maxOccupied == -1 || h > maxOccupied {
			maxOccupied = h
		}
	}
	return minOccupied, maxOccupied
}

func buildCollapsedHourGaps(occupied map[int]bool, minOccupied, maxOccupied int, timeFormat string) []CollapsedHoursGap {
	var gaps []CollapsedHoursGap
	runStart := -1
	for h := minOccupied + 1; h <= maxOccupied; h++ {
		if !occupied[h] && h < maxOccupied {
			if runStart == -1 {
				runStart = h
			}
			continue
		}
		if runStart != -1 && h-runStart >= minCollapsedGapHours {
			startLbl := formatTimeLabel(runStart, 0, timeFormat)
			endLbl := formatTimeLabel(h, 0, timeFormat)
			gaps = append(gaps, CollapsedHoursGap{
				StartHour: runStart,
				EndHour:   h,
				StartStr:  fmt.Sprintf("%02d:00", runStart),
				EndStr:    fmt.Sprintf("%02d:00", h),
				Label:     fmt.Sprintf("%s – %s", startLbl, endLbl),
			})
		}
		runStart = -1
	}
	return gaps
}

func mapMinutesToCanvasPct(mins float64, hours HoursBand) float64 {
	startTotal := float64(hours.StartHour*60 + hours.StartMinute)
	endTotal := float64(hours.EndHour*60 + hours.EndMinute)
	if endTotal <= startTotal {
		return 0.0
	}
	if mins <= startTotal {
		return 0.0
	}
	if mins >= endTotal {
		return 100.0
	}
	if len(hours.CollapsedGaps) == 0 {
		return ((mins - startTotal) / (endTotal - startTotal)) * 100.0
	}

	compressedGapMins := collapsedGapHourEquivalent * 60.0
	totalEff := 0.0
	effAtMins := 0.0
	cur := startTotal

	for _, g := range hours.CollapsedGaps {
		gStart := math.Max(float64(g.StartHour*60), cur)
		gEnd := math.Min(float64(g.EndHour*60), endTotal)
		if gEnd <= gStart {
			continue
		}
		if gStart > cur {
			seg := gStart - cur
			if mins > cur {
				effAtMins += math.Min(mins, gStart) - cur
			}
			totalEff += seg
		}
		gapActual := gEnd - gStart
		if mins > gStart {
			inGap := math.Min(mins, gEnd) - gStart
			effAtMins += (inGap / gapActual) * compressedGapMins
		}
		totalEff += compressedGapMins
		cur = gEnd
	}

	if endTotal > cur {
		seg := endTotal - cur
		if mins > cur {
			effAtMins += math.Min(mins, endTotal) - cur
		}
		totalEff += seg
	}

	if totalEff <= 0 {
		return 0.0
	}
	return (effAtMins / totalEff) * 100.0
}

func generateTimelineMarkersWithGaps(hours HoursBand, timeFormat string) []TimelineMarker {
	hStart, mStart, hEnd, mEnd := hours.StartHour, hours.StartMinute, hours.EndHour, hours.EndMinute
	startTotal := hStart*60 + mStart
	endTotal := hEnd*60 + mEnd
	if endTotal <= startTotal {
		return nil
	}

	isInteriorGapHour := func(h int) bool {
		for _, g := range hours.CollapsedGaps {
			if h > g.StartHour && h < g.EndHour {
				return true
			}
		}
		return false
	}

	markers := make([]TimelineMarker, 0, (hEnd-hStart)+2)
	markers = append(markers, TimelineMarker{
		Label:   formatTimeLabel(hStart, mStart, timeFormat),
		TopPct:  0.0,
		IsStart: true,
	})

	firstHour := hStart + 1
	for h := firstHour; h < hEnd; h++ {
		if isInteriorGapHour(h) {
			continue
		}
		pct := mapMinutesToCanvasPct(float64(h*60), hours)
		markers = append(markers, TimelineMarker{
			Label:  formatTimeLabel(h, 0, timeFormat),
			TopPct: roundToTwoDecimals(pct),
		})
	}

	if mEnd > 0 && hEnd*60 > startTotal && !isInteriorGapHour(hEnd) {
		pct := mapMinutesToCanvasPct(float64(hEnd*60), hours)
		markers = append(markers, TimelineMarker{
			Label:  formatTimeLabel(hEnd, 0, timeFormat),
			TopPct: roundToTwoDecimals(pct),
		})
	}

	markers = append(markers, TimelineMarker{
		Label:  formatTimeLabel(hEnd, mEnd, timeFormat),
		TopPct: 100.0,
		IsEnd:  true,
	})

	return markers
}

func generateTimelineMarkers(hStart, mStart, hEnd, mEnd int, timeFormat ...string) []TimelineMarker {
	tf := "24h"
	if len(timeFormat) > 0 && timeFormat[0] != "" {
		tf = timeFormat[0]
	}
	startTotal := hStart*60 + mStart
	endTotal := hEnd*60 + mEnd
	duration := float64(endTotal - startTotal)
	if duration <= 0 {
		return nil
	}

	markers := make([]TimelineMarker, 0, (hEnd-hStart)+2)
	markers = append(markers, TimelineMarker{
		Label:   formatTimeLabel(hStart, mStart, tf),
		TopPct:  0.0,
		IsStart: true,
	})

	firstHour := hStart + 1
	for h := firstHour; h < hEnd; h++ {
		pct := float64(h*60-startTotal) / duration * 100.0
		markers = append(markers, TimelineMarker{
			Label:  formatTimeLabel(h, 0, tf),
			TopPct: roundToTwoDecimals(pct),
		})
	}

	if mEnd > 0 && hEnd*60 > startTotal {
		pct := float64(hEnd*60-startTotal) / duration * 100.0
		markers = append(markers, TimelineMarker{
			Label:  formatTimeLabel(hEnd, 0, tf),
			TopPct: roundToTwoDecimals(pct),
		})
	}

	markers = append(markers, TimelineMarker{
		Label:  formatTimeLabel(hEnd, mEnd, tf),
		TopPct: 100.0,
		IsEnd:  true,
	})

	return markers
}

func generateHourMarkers(hStart, mStart, hEnd, mEnd int, timeFormat ...string) []string {
	tf := "24h"
	if len(timeFormat) > 0 && timeFormat[0] != "" {
		tf = timeFormat[0]
	}
	var markers []string
	firstHour := hStart
	if mStart > 0 {
		markers = append(markers, formatTimeLabel(hStart, mStart, tf))
		firstHour = hStart + 1
	}
	for h := firstHour; h <= hEnd; h++ {
		if h == hEnd && mEnd > 0 {
			markers = append(markers, formatTimeLabel(h, 0, tf))
			markers = append(markers, formatTimeLabel(hEnd, mEnd, tf))
			break
		}
		markers = append(markers, formatTimeLabel(h, 0, tf))
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

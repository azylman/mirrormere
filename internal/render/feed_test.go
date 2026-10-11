package render

import (
	"bytes"
	"html/template"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/domain"
	"github.com/azylman/mirrormere/internal/provider"
)

func renderFeedWidgetTemplate(t *testing.T, ctx Context, now time.Time) string {
	t.Helper()
	tmplPath := filepath.Join("..", "..", "widgets", "calendar-feed", "views", "widget.html")
	tmpl, err := template.New("widget.html").Funcs(StandardFuncMap(func() time.Time {
		return now
	})).ParseFiles(tmplPath)
	if err != nil {
		t.Fatalf("failed to parse calendar-feed widget template: %v", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, ctx); err != nil {
		t.Fatalf("failed to execute calendar-feed widget template: %v", err)
	}
	return buf.String()
}

func TestCalendarFeedWidget_AlignmentAndHierarchy(t *testing.T) {
	t.Parallel()

	testNow := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	snap := provider.CalendarSnapshot{
		LastSync: "2026-10-10T12:00:00Z",
		Events: []provider.CalendarEvent{
			{
				ID:           "e1",
				Title:        "Doctor Appointment",
				CalendarName: "Family",
				Color:        "#ff6f59",
				Start:        "2026-10-10T14:00:00Z",
				End:          "2026-10-10T15:00:00Z",
				Location:     "Medical Center Bldg B",
			},
			{
				ID:           "e2",
				Title:        "Team Sync",
				CalendarName: "Work",
				Color:        "#9d8df1",
				Start:        "2026-10-10T16:00:00Z",
				End:          "2026-10-10T17:00:00Z",
			},
			{
				ID:       "e3",
				Title:    "No-Calendar Event",
				Start:    "2026-10-10T18:00:00Z",
				End:      "2026-10-10T19:00:00Z",
				Location: "Room 101",
			},
		},
	}

	ctx := Context{
		ID:         "test-cal-feed",
		Type:       "calendar-feed",
		Dimensions: domain.NewDimension(4, 3),
		Data:       snap,
		Config: map[string]any{
			"title": "Upcoming",
		},
	}

	html := renderFeedWidgetTemplate(t, ctx, testNow)

	// 1. Verify that event-calendar-pill is NOT nested inside event-title-row
	firstEventIdx := strings.Index(html, "Doctor Appointment")
	if firstEventIdx == -1 {
		t.Fatalf("expected 'Doctor Appointment' in rendered output")
	}
	titleRowStart := strings.LastIndex(html[:firstEventIdx], `<div class="event-title-row">`)
	if titleRowStart == -1 {
		t.Fatalf("expected event-title-row before title")
	}
	titleRowEnd := strings.Index(html[titleRowStart:], `</div>`)
	if titleRowEnd == -1 {
		t.Fatalf("expected closing div for event-title-row")
	}
	titleRowContent := html[titleRowStart : titleRowStart+titleRowEnd]
	if strings.Contains(titleRowContent, "event-calendar-pill") {
		t.Errorf("event-calendar-pill must NOT be inside event-title-row: %s", titleRowContent)
	}

	// 2. Verify that event-calendar-pill is a sibling outside event-details-col
	detailsColStart := strings.LastIndex(html[:firstEventIdx], `<div class="event-details-col">`)
	if detailsColStart == -1 {
		t.Fatalf("expected event-details-col before title")
	}

	// 3. Verify location row is inside event-details-col
	if !strings.Contains(html, "Medical Center Bldg B") {
		t.Errorf("expected location to be rendered: %s", html)
	}

	// 4. Verify relative ordering in calendar-event-item: event-details-col, then event-calendar-pill, then event-accent-dot
	pillIdx := strings.Index(html, `class="event-calendar-pill"`)
	dotIdx := strings.Index(html, `class="event-accent-dot"`)
	if pillIdx == -1 || dotIdx == -1 || dotIdx < pillIdx {
		t.Errorf("expected event-accent-dot to follow event-calendar-pill, got pillIdx=%d, dotIdx=%d", pillIdx, dotIdx)
	}

	// 5. Verify event e3 has location but no event-calendar-pill
	e3Idx := strings.Index(html, "No-Calendar Event")
	if e3Idx == -1 {
		t.Fatalf("expected 'No-Calendar Event' in rendered output")
	}
	e3Snippet := html[e3Idx:]
	if nextItemIdx := strings.Index(e3Snippet, `class="calendar-event-item"`); nextItemIdx != -1 {
		e3Snippet = e3Snippet[:nextItemIdx]
	}
	if strings.Contains(e3Snippet, "event-calendar-pill") {
		t.Errorf("event e3 should not have event-calendar-pill")
	}
	if !strings.Contains(e3Snippet, "Room 101") {
		t.Errorf("event e3 should have location 'Room 101'")
	}
	if !strings.Contains(e3Snippet, `class="event-accent-dot"`) {
		t.Errorf("event e3 should still have event-accent-dot")
	}

	// 6. Verify all 3 events on 2026-10-10 are combined into a single calendar-day-card pill
	dayCardsCount := strings.Count(html, `class="calendar-day-card"`)
	if dayCardsCount != 1 {
		t.Errorf("expected exactly 1 calendar-day-card for single day, got %d", dayCardsCount)
	}

	cardStart := strings.Index(html, `<div class="calendar-day-card">`)
	if cardStart == -1 {
		t.Fatalf("expected calendar-day-card in rendered output")
	}
	cardContent := html[cardStart:]
	if !strings.Contains(cardContent, "Doctor Appointment") ||
		!strings.Contains(cardContent, "Team Sync") ||
		!strings.Contains(cardContent, "No-Calendar Event") {
		t.Errorf("expected all events to be contained within the calendar-day-card")
	}
}


func TestCalendarFeedWidget_AllDaySortingAcrossTimezones(t *testing.T) {
	t.Parallel()

	laLoc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatalf("failed to load America/Los_Angeles location: %v", err)
	}

	testNow := time.Date(2026, 10, 10, 12, 0, 0, 0, laLoc)

	snap := provider.CalendarSnapshot{
		LastSync: "2026-10-10T12:00:00Z",
		Events: func() []provider.CalendarEvent {
			// Input with party listed before Wednesday game night
			evs := []provider.CalendarEvent{
				{
					ID:           "e-mon",
					Title:        "Monday Evening Dinner",
					CalendarName: "Family",
					Color:        "#3b82f6",
					Start:        "2026-10-13T02:00:00Z", // Oct 12 19:00 PDT
					End:          "2026-10-13T03:00:00Z",
				},
				{
					ID:           "e-party",
					Title:        "Weekend Bachelorette Party",
					CalendarName: "Family",
					Color:        "#a855f7",
					Start:        "2026-10-15",
					End:          "2026-10-15",
					AllDay:       true,
				},
				{
					ID:           "e-wed",
					Title:        "Wednesday Game Night",
					CalendarName: "Family",
					Color:        "#3b82f6",
					Start:        "2026-10-15T02:00:00Z", // Oct 14 19:00 PDT
					End:          "2026-10-15T04:00:00Z",
				},
				{
					ID:           "e-brunch",
					Title:        "Brunch with Bridesmaids",
					CalendarName: "Family",
					Color:        "#a855f7",
					Start:        "2026-10-15T17:00:00Z", // Oct 15 10:00 PDT
					End:          "2026-10-15T18:30:00Z",
				},
			}
			provider.SortCalendarEvents(evs, laLoc)
			return evs
		}(),
	}

	ctx := Context{
		ID:         "test-cal-feed",
		Type:       "calendar-feed",
		Dimensions: domain.NewDimension(4, 3),
		Data:       snap,
		Config: map[string]any{
			"title": "Upcoming",
		},
	}

	html := renderFeedWidgetTemplate(t, ctx, testNow)

	// 1. Verify "Monday Evening Dinner" appears before "Wednesday Game Night"
	monIdx := strings.Index(html, "Monday Evening Dinner")
	wedIdx := strings.Index(html, "Wednesday Game Night")
	partyIdx := strings.Index(html, "Weekend Bachelorette Party")
	if partyIdx == -1 {
		partyIdx = strings.Index(html, "Weekend Bachelorette Party")
	}
	brunchIdx := strings.Index(html, "Brunch with Bridesmaids")

	if monIdx == -1 || wedIdx == -1 || partyIdx == -1 || brunchIdx == -1 {
		t.Fatalf("expected all 4 events in HTML output; got mon=%d, wed=%d, party=%d, brunch=%d",
			monIdx, wedIdx, partyIdx, brunchIdx)
	}

	if monIdx > wedIdx {
		t.Errorf("expected Monday dinner before Wednesday game night, got monIdx=%d, wedIdx=%d", monIdx, wedIdx)
	}
	if wedIdx > partyIdx {
		t.Errorf("expected Wednesday game night before Thursday bachelorette party, got wedIdx=%d, partyIdx=%d", wedIdx, partyIdx)
	}
	if partyIdx > brunchIdx {
		t.Errorf("expected Thursday bachelorette party before Thursday brunch, got partyIdx=%d, brunchIdx=%d", partyIdx, brunchIdx)
	}

	// 2. Verify Day Divider for Thursday Oct 15 appears only once
	day15Matches := strings.Count(html, "Thursday • Oct 15")
	if day15Matches != 1 {
		t.Errorf("expected exactly 1 day divider for 'Thursday • Oct 15', got %d", day15Matches)
	}

	// 3. Verify All Day badge for the party
	if !strings.Contains(html, "event-time-allday") {
		t.Errorf("expected event-time-allday class in HTML output")
	}

	// 4. Verify day cards: exactly 3 calendar-day-card pills (Monday, Wednesday, Thursday)
	dayCardsCount := strings.Count(html, `class="calendar-day-card"`)
	if dayCardsCount != 3 {
		t.Errorf("expected exactly 3 calendar-day-card pills for the 3 days, got %d", dayCardsCount)
	}
	cards := strings.Split(html, `<div class="calendar-day-card">`)
	if len(cards) >= 4 {
		thursCard := cards[3]
		if !strings.Contains(thursCard, "Weekend Bachelorette Party") || !strings.Contains(thursCard, "Brunch with Bridesmaids") {
			t.Errorf("expected Thursday day-card to contain both Thursday events, got: %s", thursCard)
		}
	}
}

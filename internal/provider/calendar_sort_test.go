package provider_test

import (
	"slices"
	"testing"

	"github.com/azylman/mirrormere/internal/provider"
)

// TestSortCalendarEvents_UTCFallback covers the nil-location default plus
// compact all-day dates and unparsable starts, which sort by their raw string.
func TestSortCalendarEvents_UTCFallback(t *testing.T) {
	t.Parallel()
	events := []provider.CalendarEvent{
		{Title: "Timed B", Start: "2026-11-02T12:00:00Z", End: "2026-11-02T13:00:00Z"},
		{Title: "AllDay A", Start: "20261102", End: "20261103", AllDay: true},
		{Title: "Timed A", Start: "20260102T150405", End: "20260102T160405"},
		{Title: "Invalid", Start: "not-a-date", End: "not-a-date"},
		{Title: "Empty", Start: "", End: ""},
	}
	provider.SortCalendarEvents(events, nil)
	got := make([]string, len(events))
	for i, ev := range events {
		got[i] = ev.Title
	}
	if want := []string{"Empty", "Timed A", "AllDay A", "Timed B", "Invalid"}; !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

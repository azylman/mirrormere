package events

import (
	"strings"
	"testing"
	"time"
)

func TestSSEFrames(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		frame []byte
		want  string
	}{
		{"event_with_id", (&Event{ID: "evt_123", Type: EventWidgetUpdate, Data: []byte("{\"hello\":\r\n\"world\"}")}).Format(), "event: widget.update\nid: evt_123\ndata: {\"hello\":\n\"world\"}\n\n"},
		{"event_without_id", (&Event{Type: EventSystemStatus, Data: []byte("{\"status\":\"ok\"}")}).Format(), "event: system.status\ndata: {\"status\":\"ok\"}\n\n"},
		{"ping", FormatPing(time.Date(2026, 9, 25, 20, 15, 30, 0, time.UTC)), ": ping 1790367330\n\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := string(tc.frame); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestIDGenerator(t *testing.T) {
	t.Parallel()
	gen := NewIDGenerator()
	now := time.Date(2026, 9, 25, 20, 0, 0, 0, time.UTC)
	id1, id2 := gen.Next(now), gen.Next(now)
	if !strings.HasPrefix(id1, "evt_") || !strings.HasPrefix(id2, "evt_") || id1 == id2 {
		t.Fatalf("unexpected IDs: %q, %q", id1, id2)
	}
}

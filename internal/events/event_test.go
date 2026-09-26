package events

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestEvent_Format(t *testing.T) {
	t.Parallel()

	// 1. Full event with ID and carriage returns
	evt := &Event{
		ID:   "evt_123",
		Type: EventWidgetUpdate,
		Data: []byte("{\"hello\":\r\n\"world\"}"),
	}

	formatted := string(evt.Format())
	expected := "event: widget.update\nid: evt_123\ndata: {\"hello\":\n\"world\"}\n\n"
	if formatted != expected {
		t.Errorf("expected format:\n%q\ngot:\n%q", expected, formatted)
	}

	// 2. Event without ID
	evtNoID := &Event{
		Type: EventSystemStatus,
		Data: []byte("{\"status\":\"ok\"}"),
	}
	formattedNoID := string(evtNoID.Format())
	expectedNoID := "event: system.status\ndata: {\"status\":\"ok\"}\n\n"
	if formattedNoID != expectedNoID {
		t.Errorf("expected format:\n%q\ngot:\n%q", expectedNoID, formattedNoID)
	}
}

func TestFormatPing(t *testing.T) {
	t.Parallel()

	refTime := time.Date(2026, 9, 25, 20, 15, 30, 0, time.UTC)
	ping := FormatPing(refTime)

	expected := ": ping 1790367330\n\n"
	if !bytes.Equal(ping, []byte(expected)) {
		t.Errorf("expected ping %q, got %q", expected, string(ping))
	}
}

func TestIDGenerator(t *testing.T) {
	t.Parallel()

	gen := NewIDGenerator()
	now := time.Date(2026, 9, 25, 20, 0, 0, 0, time.UTC)

	id1 := gen.Next(now)
	id2 := gen.Next(now)

	if !strings.HasPrefix(id1, "evt_") || !strings.HasPrefix(id2, "evt_") {
		t.Errorf("expected prefix evt_, got %s, %s", id1, id2)
	}
	if id1 == id2 {
		t.Errorf("expected distinct IDs, got %s and %s", id1, id2)
	}
}

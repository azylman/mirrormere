package eventstest_test

import (
	"slices"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/events"
	"github.com/azylman/mirrormere/internal/testutil/eventstest"
)

// drainBroadcaster calls WaitForPublish n times and returns each event type,
// or "" for a wait that timed out.
func drainBroadcaster(b *eventstest.Broadcaster, n int) []string {
	types := make([]string, 0, n)
	for range n {
		evt, ok := b.WaitForPublish(10 * time.Millisecond)
		if !ok {
			types = append(types, "")
			continue
		}
		types = append(types, evt.Type)
	}
	return types
}

func broadcasterEventFields(evts []*events.Event) (types, ids []string) {
	for _, evt := range evts {
		types = append(types, evt.Type)
		ids = append(ids, evt.ID)
	}
	return types, ids
}

func TestBroadcaster(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		publish   []string
		waits     int
		wantTypes []string
		wantWaits []string
		wantIDs   []string
	}{
		{"records and delivers in order", []string{events.EventWidgetUpdate, events.EventScreenRotate}, 2, []string{"widget.update", "screen.rotate"}, []string{"widget.update", "screen.rotate"}, []string{"evt_1", "evt_2"}},
		{"wait times out when nothing is pending", nil, 1, nil, []string{""}, nil},
		{"publish beyond the buffer keeps recording", slices.Repeat([]string{events.EventVoiceState}, 65), 0, slices.Repeat([]string{"voice.state"}, 65), nil, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var b eventstest.Broadcaster
			for _, typ := range tc.publish {
				if evt := b.Publish(typ, []byte(`{"room":"kitchen"}`)); evt.Type != typ || string(evt.Data) != `{"room":"kitchen"}` || evt.Timestamp.IsZero() {
					t.Fatalf("Publish(%s) = %+v", typ, evt)
				}
			}
			gotWaits := drainBroadcaster(&b, tc.waits)
			gotTypes, gotIDs := broadcasterEventFields(b.Events())
			if !slices.Equal(gotTypes, tc.wantTypes) || !slices.Equal(gotWaits, tc.wantWaits) || (tc.wantIDs != nil && !slices.Equal(gotIDs, tc.wantIDs)) {
				t.Fatalf("types %v waits %v ids %v; want %v %v %v", gotTypes, gotWaits, gotIDs, tc.wantTypes, tc.wantWaits, tc.wantIDs)
			}
		})
	}
}

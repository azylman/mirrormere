package events

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/config"
	"github.com/azylman/mirrormere/internal/domain"
	"github.com/azylman/mirrormere/internal/layout"
)

type mockCoordinator struct{ calledWith *config.Snapshot }

func (m *mockCoordinator) UpdateConfig(snap *config.Snapshot) ScreenRotateData {
	m.calledWith = snap
	return ScreenRotateData{TotalScreens: 2, IntervalSeconds: 30}
}

type mockProviderCoordinator struct {
	calledWith *config.Snapshot
	err        error
}

func (m *mockProviderCoordinator) UpdateConfig(snap *config.Snapshot) error {
	m.calledWith = snap
	return m.err
}

type dispatchReloadCase struct {
	name       string
	snap       *config.Snapshot
	diff       *config.ConfigDiff
	coord      bool
	provCoord  bool
	provErr    bool
	wantEvents []string
	wantSub    string
}

func setupHubCoordinators(hub *Hub, tc dispatchReloadCase) (*mockCoordinator, *mockProviderCoordinator) {
	var mockRC *mockCoordinator
	if tc.coord {
		mockRC = &mockCoordinator{}
		hub.SetRotationCoordinator(mockRC)
	}
	var mockPC *mockProviderCoordinator
	if tc.provCoord {
		var err error
		if tc.provErr {
			err = errors.New("reconcile err")
		}
		mockPC = &mockProviderCoordinator{err: err}
		hub.SetProviderCoordinator(mockPC)
	}
	return mockRC, mockPC
}

func verifyHubDispatchEvents(t *testing.T, ch <-chan *Event, wantEvents []string, wantSub string) {
	t.Helper()
	for _, wantType := range wantEvents {
		select {
		case evt := <-ch:
			if evt.Type != wantType || (wantSub != "" && !strings.Contains(string(evt.Data), wantSub)) {
				t.Fatalf("got %s %s, want %s %s", evt.Type, string(evt.Data), wantType, wantSub)
			}
		case <-time.After(1 * time.Second):
			t.Fatalf("timeout waiting for %s", wantType)
		}
	}

	select {
	case evt := <-ch:
		t.Fatalf("unexpected extra event: %s %s", evt.Type, string(evt.Data))
	default:
	}
}

func TestHub_DispatchConfigReload(t *testing.T) {
	t.Parallel()
	snapWithLayout := &config.Snapshot{
		Config: &config.Config{Display: config.DisplayConfig{Widgets: []config.WidgetConfig{{ID: "w1", Type: "clock"}}}},
		Layout: &layout.Layout{TotalScreens: 1, Screens: []layout.Screen{{Index: 0, Widgets: []layout.PlacedWidget{{WidgetID: "w1", Origin: [2]int{0, 0}, Dimensions: domain.Dimension{Cols: 3, Rows: 2}}}}}},
	}

	tests := []dispatchReloadCase{
		{name: "nil_snapshot", snap: nil},
		{
			name: "layout_and_domain_change", snap: snapWithLayout,
			diff:       &config.ConfigDiff{Modified: []config.InstanceDiff{{ID: "w1", DomainChange: true}, {ID: "w2", DomainChange: false}}},
			wantEvents: []string{EventScreenRotate, EventWidgetUpdate}, wantSub: `"widget_id":"w1"`,
		},
		{
			name: "timezone_change", snap: &config.Snapshot{Config: &config.Config{Timezone: "America/Chicago"}},
			diff: &config.ConfigDiff{TimezoneChanged: true}, wantEvents: []string{EventHeaderUpdate}, wantSub: "America/Chicago",
		},
		{
			name: "shell_changed", snap: &config.Snapshot{Config: &config.Config{Timezone: "UTC"}},
			diff: &config.ConfigDiff{ShellChanged: true}, wantEvents: []string{EventSystemReload}, wantSub: "shell_change",
		},
		{name: "with_rotation_coordinator", snap: &config.Snapshot{Config: &config.Config{}}, coord: true},
		{name: "with_provider_coordinator", snap: &config.Snapshot{Config: &config.Config{}}, provCoord: true},
		{name: "with_provider_coordinator_error", snap: &config.Snapshot{Config: &config.Config{}}, provCoord: true, provErr: true},
		{
			name:       "provider_coordinator_suppresses_duplicate_widget_update",
			snap:       snapWithLayout,
			diff:       &config.ConfigDiff{Modified: []config.InstanceDiff{{ID: "w1", DomainChange: true}}},
			provCoord:  true,
			wantEvents: []string{EventScreenRotate},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			hub := NewHub(HubConfig{}, NewInMemoryStateProvider(nil), nil)
			defer hub.Close()

			mockRC, mockPC := setupHubCoordinators(hub, tc)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ch, unsub := hub.Subscribe(ctx)
			defer unsub()

			if err := hub.DispatchConfigReload(tc.snap, tc.diff); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.coord && mockRC.calledWith != tc.snap {
				t.Error("expected rotation coordinator called with snapshot")
			}
			if tc.provCoord && mockPC.calledWith != tc.snap {
				t.Error("expected provider coordinator called with snapshot")
			}

			verifyHubDispatchEvents(t, ch, tc.wantEvents, tc.wantSub)
		})
	}
}

// subscribeDispatchHub returns a hub over provider plus a live subscription closed via t.Cleanup.
func subscribeDispatchHub(t *testing.T, provider StateProvider) (*Hub, <-chan *Event) {
	t.Helper()
	hub := NewHub(HubConfig{}, provider, nil)
	t.Cleanup(hub.Close)
	ch, unsub := hub.Subscribe(context.Background())
	t.Cleanup(unsub)
	return hub, ch
}

func expectOne(t *testing.T, ch <-chan *Event, wantType, wantSub string) {
	t.Helper()
	verifyHubDispatchEvents(t, ch, []string{wantType}, wantSub)
}

func TestHub_PublishJSONError(t *testing.T) {
	t.Parallel()
	hub, ch := subscribeDispatchHub(t, nil)
	if err := hub.publishJSON("test", make(chan int)); err == nil {
		t.Fatal("expected publishJSON to fail on unmarshalable payload")
	}
	verifyHubDispatchEvents(t, ch, nil, "")
}

func TestHub_DispatchStyleReload(t *testing.T) {
	t.Parallel()
	hub, ch := subscribeDispatchHub(t, NewInMemoryStateProvider(nil))
	if err := hub.DispatchStyleReload(StyleReloadEvent{File: "custom.css", Timestamp: "2026-09-25T20:00:00Z"}); err != nil {
		t.Fatalf("DispatchStyleReload: %v", err)
	}
	expectOne(t, ch, EventStyleReload, `"file":"custom.css"`)
}

func TestHub_DispatchWidgetReload(t *testing.T) {
	t.Parallel()
	hub, ch := subscribeDispatchHub(t, NewInMemoryStateProvider(nil))
	if err := hub.DispatchWidgetReload(WidgetReloadEvent{Type: "sensor-card"}); err != nil {
		t.Fatalf("DispatchWidgetReload: %v", err)
	}
	expectOne(t, ch, EventWidgetReload, `"type":"sensor-card"`)
}

func TestHub_DispatchSystemReload(t *testing.T) {
	t.Parallel()
	hub, ch := subscribeDispatchHub(t, NewInMemoryStateProvider(nil))
	if err := hub.DispatchSystemReload(SystemReloadEvent{Reason: "manual_reload", Timestamp: "2026-09-25T20:00:00Z"}); err != nil {
		t.Fatalf("DispatchSystemReload: %v", err)
	}
	expectOne(t, ch, EventSystemReload, `"reason":"manual_reload"`)
}

func TestHub_DispatchStatus(t *testing.T) {
	t.Parallel()
	provider := NewInMemoryStateProvider(nil)
	provider.SetProvidersStatus(map[string]string{"pkg": "missing manifest"})
	hub, ch := subscribeDispatchHub(t, provider)
	errStr := "syntax error"
	if err := hub.DispatchStatus(config.Status{ConfigStatus: config.ConfigStatusError, ConfigError: &errStr}); err != nil {
		t.Fatalf("DispatchStatus: %v", err)
	}
	if got := provider.CurrentStatus().ConfigStatus; got != config.ConfigStatusError {
		t.Errorf("provider ConfigStatus = %q, want %q", got, config.ConfigStatusError)
	}
	expectOne(t, ch, EventSystemStatus, "syntax error")
}

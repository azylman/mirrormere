package events

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/config"
	"github.com/azylman/mirrormere/internal/domain"
	"github.com/azylman/mirrormere/internal/layout"
	"github.com/azylman/mirrormere/internal/watcher"
)

func TestHub_DispatchConfigReload(t *testing.T) {
	t.Parallel()

	provider := NewInMemoryStateProvider(nil)
	hub := NewHub(HubConfig{}, provider, nil)
	defer hub.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, unsub := hub.Subscribe(ctx)
	defer unsub()

	// 1. Nil snapshot is a no-op
	if err := hub.DispatchConfigReload(nil, nil); err != nil {
		t.Fatalf("unexpected error on nil snapshot: %v", err)
	}

	// 2. Snapshot with Layout and DomainChange diff
	cfg := &config.Config{
		Display: config.DisplayConfig{
			Widgets: []config.WidgetConfig{
				{ID: "w1", Type: "clock"},
			},
		},
	}
	lay := &layout.Layout{
		TotalScreens: 1,
		Screens: []layout.Screen{
			{
				Index: 0,
				Widgets: []layout.PlacedWidget{
					{
						WidgetID:   "w1",
						Origin:     [2]int{0, 0},
						Dimensions: domain.NewDimension(3, 2),
					},
				},
			},
		},
	}
	snap := &config.Snapshot{
		Config: cfg,
		Layout: lay,
	}

	diff := &config.ConfigDiff{
		Modified: []config.InstanceDiff{
			{
				ID:           "w1",
				DomainChange: true,
			},
			{
				ID:           "w2",
				DomainChange: false, // Should NOT trigger widget.update
			},
		},
	}

	if err := hub.DispatchConfigReload(snap, diff); err != nil {
		t.Fatalf("DispatchConfigReload failed: %v", err)
	}

	// Should receive:
	// 1. screen.rotate
	// 2. widget.update for w1
	select {
	case evt := <-ch:
		if evt.Type != EventScreenRotate {
			t.Errorf("expected EventScreenRotate, got %s", evt.Type)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for screen.rotate")
	}

	select {
	case evt := <-ch:
		if evt.Type != EventWidgetUpdate {
			t.Errorf("expected EventWidgetUpdate, got %s", evt.Type)
		}
		var wData WidgetUpdateData
		_ = json.Unmarshal(evt.Data, &wData)
		if wData.WidgetID != "w1" || wData.State != "degraded" {
			t.Errorf("unexpected widget update: %+v", wData)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for widget.update")
	}
}

func TestHub_DispatchStyleAndWidgetReload(t *testing.T) {
	t.Parallel()

	hub := NewHub(HubConfig{}, nil, nil)
	defer hub.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, unsub := hub.Subscribe(ctx)
	defer unsub()

	// 1. Style reload
	styleEvt := watcher.StyleReloadEvent{
		File:      "custom.css",
		Timestamp: "2026-09-25T20:00:00Z",
	}
	if err := hub.DispatchStyleReload(styleEvt); err != nil {
		t.Fatalf("DispatchStyleReload failed: %v", err)
	}

	select {
	case evt := <-ch:
		if evt.Type != EventStyleReload {
			t.Errorf("expected %s, got %s", EventStyleReload, evt.Type)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for style.reload")
	}

	// 2. Widget reload
	widgetEvt := watcher.WidgetReloadEvent{
		Type: "sensor-card",
	}
	if err := hub.DispatchWidgetReload(widgetEvt); err != nil {
		t.Fatalf("DispatchWidgetReload failed: %v", err)
	}

	select {
	case evt := <-ch:
		if evt.Type != EventWidgetReload {
			t.Errorf("expected %s, got %s", EventWidgetReload, evt.Type)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for widget.reload")
	}
}

func TestHub_DispatchStatus(t *testing.T) {
	t.Parallel()

	provider := NewInMemoryStateProvider(nil)
	hub := NewHub(HubConfig{}, provider, nil)
	defer hub.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, unsub := hub.Subscribe(ctx)
	defer unsub()

	errStr := "syntax error"
	status := config.Status{
		ConfigStatus: config.ConfigStatusError,
		ConfigError:  &errStr,
	}
	provider.SetProvidersStatus(map[string]string{
		"pkg": "missing manifest",
	})

	if err := hub.DispatchStatus(status); err != nil {
		t.Fatalf("DispatchStatus failed: %v", err)
	}

	select {
	case evt := <-ch:
		if evt.Type != EventSystemStatus {
			t.Errorf("expected %s, got %s", EventSystemStatus, evt.Type)
		}
		var sys SystemStatusData
		_ = json.Unmarshal(evt.Data, &sys)
		if sys.ConfigStatus != "error" || *sys.ConfigError != errStr || sys.Providers["pkg"] != "missing manifest" {
			t.Errorf("unexpected status data: %+v", sys)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for system.status")
	}

	// Verify provider was updated
	if provider.CurrentStatus().ConfigStatus != config.ConfigStatusError {
		t.Errorf("expected provider status updated to error")
	}
}

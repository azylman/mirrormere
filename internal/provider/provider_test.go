package provider_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/azylman/mirrormere/internal/provider"
	"github.com/azylman/mirrormere/internal/testutil"
)

type dummyProvider struct{}

func (*dummyProvider) Init(context.Context, map[string]any, provider.InitOptions) error { return nil }

func (*dummyProvider) Fetch(context.Context) (any, error) { return map[string]any{}, nil }

func (*dummyProvider) Subscribe(context.Context, chan<- provider.WidgetPayload) error { return nil }

func (*dummyProvider) Shutdown(context.Context) error { return nil }

// TestRegistry creates every builtin plus a custom registration and checks the
// no-op Subscribe(nil)/Shutdown lifecycle each provider promises.
func TestRegistry(t *testing.T) {
	t.Parallel()
	tests := []struct {
		typ       string
		wantType  string
		wantErrIs error
	}{
		{typ: "spacer", wantType: "*provider.SpacerProvider"},
		{typ: "live-view", wantType: "*provider.SpacerProvider"},
		{typ: "http", wantType: "*provider.HTTPProvider"},
		{typ: "weather-forecast", wantType: "*provider.WeatherProvider"},
		{typ: "calendar-feed", wantType: "*provider.CalendarProvider"},
		{typ: "photo-carousel", wantType: "*provider.PhotosProvider"},
		{typ: "tasks", wantType: "*provider.TasksProvider"},
		{typ: "home-assistant", wantType: "*provider.HomeAssistantProvider"},
		{typ: "custom-dummy", wantType: "*provider_test.dummyProvider"},
		{typ: "unknown", wantErrIs: provider.ErrProviderNotRegistered},
	}
	for _, tc := range tests {
		t.Run(tc.typ, func(t *testing.T) {
			t.Parallel()
			reg := provider.NewRegistry()
			reg.Register("custom-dummy", func() provider.Provider { return &dummyProvider{} })
			p, err := reg.Create(tc.typ)
			if !errors.Is(err, tc.wantErrIs) {
				t.Fatalf("Create(%q) error = %v, want %v", tc.typ, err, tc.wantErrIs)
			}
			if err != nil {
				return
			}
			if got := fmt.Sprintf("%T", p); got != tc.wantType {
				t.Fatalf("Create(%q) = %s, want %s", tc.typ, got, tc.wantType)
			}
			ctx := context.Background()
			if err := p.Subscribe(ctx, nil); err != nil {
				t.Errorf("Subscribe(nil) = %v, want nil", err)
			}
			if err := p.Shutdown(ctx); err != nil {
				t.Errorf("Shutdown = %v, want nil", err)
			}
		})
	}
}

// TestNoopProviders checks the padding/streaming providers initialize, fetch an
// empty payload and accept a live sink without doing any work.
func TestNoopProviders(t *testing.T) {
	t.Parallel()
	for _, typ := range []string{"spacer", "live-view"} {
		t.Run(typ, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			p, err := provider.NewRegistry().Create(typ)
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			if err := p.Init(ctx, nil, provider.InitOptions{}); err != nil {
				t.Fatalf("Init = %v", err)
			}
			if err := p.Subscribe(ctx, make(chan provider.WidgetPayload, 1)); err != nil {
				t.Fatalf("Subscribe = %v", err)
			}
			if data, err := p.Fetch(ctx); err != nil || len(testutil.As[map[string]any](t, data)) != 0 {
				t.Fatalf("Fetch = %v, %v; want empty map", data, err)
			}
		})
	}
}

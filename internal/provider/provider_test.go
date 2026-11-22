package provider_test

import (
	"context"
	"errors"
	"testing"

	"github.com/azylman/mirrormere/internal/provider"
	"github.com/azylman/mirrormere/internal/testutil"
)

type dummyProvider struct{ init, shut bool }

func (d *dummyProvider) Init(context.Context, map[string]any, provider.InitOptions) error {
	d.init = true
	return nil
}

func (*dummyProvider) Fetch(context.Context) (any, error) {
	return map[string]any{"dummy": true}, nil
}

func (*dummyProvider) Subscribe(context.Context, chan<- provider.WidgetPayload) error { return nil }

func (d *dummyProvider) Shutdown(context.Context) error {
	d.shut = true
	return nil
}

func TestRegistry(t *testing.T) {
	t.Parallel()
	reg := provider.NewRegistry()
	builtins := []string{"spacer", "live-view", "http", "weather-forecast", "calendar-feed", "photo-carousel", "tasks"}
	for _, name := range builtins {
		if p, err := reg.Create(name); err != nil || p == nil {
			t.Fatalf("create %s failed: %v", name, err)
		}
	}
	ctx := context.Background()
	for _, name := range []string{"spacer", "live-view"} {
		p, _ := reg.Create(name)
		if p.Init(ctx, nil, provider.InitOptions{}) != nil || p.Subscribe(ctx, make(chan provider.WidgetPayload, 1)) != nil || p.Shutdown(ctx) != nil {
			t.Fatal("lifecycle failed")
		}
		if data, err := p.Fetch(ctx); err != nil || len(testutil.As[map[string]any](t, data)) != 0 {
			t.Fatalf("bad fetch: %v %v", data, err)
		}
	}
	dummy := &dummyProvider{}
	reg.Register("custom-dummy", func() provider.Provider { return dummy })
	if got, err := reg.Create("custom-dummy"); err != nil || got != dummy {
		t.Fatalf("bad custom: %v", err)
	}
	if _, err := reg.Create("unknown"); !errors.Is(err, provider.ErrProviderNotRegistered) {
		t.Fatalf("want ErrProviderNotRegistered: %v", err)
	}
}

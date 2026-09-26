package provider_test

import (
	"context"
	"errors"
	"testing"

	"github.com/azylman/mirrormere/internal/provider"
)

type dummyProvider struct {
	initialized bool
	shutDown    bool
}

func (d *dummyProvider) Init(ctx context.Context, config map[string]any) error {
	d.initialized = true
	return nil
}

func (d *dummyProvider) Fetch(ctx context.Context) (any, error) {
	return map[string]any{"dummy": true}, nil
}

func (d *dummyProvider) Subscribe(ctx context.Context, eventSink chan<- provider.WidgetPayload) error {
	return nil
}

func (d *dummyProvider) Shutdown(ctx context.Context) error {
	d.shutDown = true
	return nil
}

func TestRegistry_BuiltInSpacer(t *testing.T) {
	t.Parallel()

	reg := provider.NewRegistry()
	if !reg.Has("spacer") {
		t.Fatal("expected registry to have 'spacer' provider")
	}

	p, err := reg.Create("spacer")
	if err != nil {
		t.Fatalf("failed to create spacer provider: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil spacer provider")
	}

	// Verify spacer provider contract
	ctx := context.Background()
	if err := p.Init(ctx, nil); err != nil {
		t.Fatalf("spacer Init failed: %v", err)
	}

	data, err := p.Fetch(ctx)
	if err != nil {
		t.Fatalf("spacer Fetch failed: %v", err)
	}
	dataMap, ok := data.(map[string]any)
	if !ok || len(dataMap) != 0 {
		t.Errorf("expected empty map from spacer Fetch, got %v", data)
	}

	sink := make(chan provider.WidgetPayload, 1)
	if err := p.Subscribe(ctx, sink); err != nil {
		t.Fatalf("spacer Subscribe failed: %v", err)
	}

	if err := p.Shutdown(ctx); err != nil {
		t.Fatalf("spacer Shutdown failed: %v", err)
	}
}

func TestRegistry_CustomProviderRegistration(t *testing.T) {
	t.Parallel()

	reg := provider.NewRegistry()
	dummy := &dummyProvider{}

	reg.Register("custom-dummy", func() provider.Provider {
		return dummy
	})

	if !reg.Has("custom-dummy") {
		t.Fatal("expected registry to have 'custom-dummy'")
	}

	created, err := reg.Create("custom-dummy")
	if err != nil {
		t.Fatalf("failed to create custom provider: %v", err)
	}
	if created != dummy {
		t.Fatalf("expected created provider to match dummy instance")
	}

	// Unknown provider returns ErrProviderNotRegistered
	if reg.Has("unknown-provider") {
		t.Fatal("expected registry to not have 'unknown-provider'")
	}
	_, err = reg.Create("unknown-provider")
	if !errors.Is(err, provider.ErrProviderNotRegistered) {
		t.Fatalf("expected ErrProviderNotRegistered, got %v", err)
	}
}

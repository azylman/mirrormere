package provider

import (
	"context"
	"maps"
	"net/http"
	"strings"
	"sync"
)

func init() {
	registerBuiltin("floorplan-3d", func() Provider {
		return NewFloorplan3dProvider()
	})
}

// Floorplan3dProvider implements Provider for the 3D floorplan widget,
// optionally hydrating live Home Assistant entity states when HA credentials are configured.
type Floorplan3dProvider struct {
	mu     sync.RWMutex
	client *http.Client
	ha     *HomeAssistantProvider
}

var _ Provider = (*Floorplan3dProvider)(nil)

// NewFloorplan3dProvider constructs a Floorplan3dProvider using the default HTTP client.
func NewFloorplan3dProvider() *Floorplan3dProvider {
	return NewFloorplan3dProviderWithClient(nil)
}

// NewFloorplan3dProviderWithClient constructs a Floorplan3dProvider using the provided HTTP client.
func NewFloorplan3dProviderWithClient(client *http.Client) *Floorplan3dProvider {
	return &Floorplan3dProvider{
		client: client,
	}
}

// Init configures optional Home Assistant entity hydration when HA connection settings are present.
// Unconfigured or layout_url-only widgets remain in demo mode with no credentials required.
func (p *Floorplan3dProvider) Init(ctx context.Context, config map[string]any, opts InitOptions) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !hasFloorplanHAConfig(config, opts) {
		p.ha = nil
		return nil
	}

	effectiveCfg := withFloorplanDomains(config)
	ha := NewHomeAssistantProviderWithClient(p.client, "", "")
	if err := ha.Init(ctx, effectiveCfg, opts); err != nil {
		p.ha = nil
		return err
	}
	p.ha = ha
	return nil
}

// Fetch returns an empty map in unconfigured demo mode, or {"entities": states} when HA is configured.
func (p *Floorplan3dProvider) Fetch(ctx context.Context) (any, error) {
	p.mu.RLock()
	ha := p.ha
	p.mu.RUnlock()

	if ha == nil {
		return map[string]any{}, nil
	}

	states, err := ha.HydrateStates(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"entities": states}, nil
}

// Subscribe returns nil because floorplan-3d entity states are synchronized via periodic Fetch calls.
func (*Floorplan3dProvider) Subscribe(context.Context, chan<- WidgetPayload) error {
	return nil
}

// Shutdown cleans up any resources held by the provider.
func (*Floorplan3dProvider) Shutdown(context.Context) error {
	return nil
}

func hasFloorplanHAConfig(config map[string]any, opts InitOptions) bool {
	for _, key := range []string{"url", "base_url", "url_env", "base_url_env", "token_env"} {
		if _, ok := config[key]; ok {
			return true
		}
	}
	if strings.TrimSpace(opts.Endpoint) != "" {
		return true
	}
	return resolveConfigField(config, opts, "url", "base_url") != "" || resolveToken(config, opts, "") != ""
}

func withFloorplanDomains(config map[string]any) map[string]any {
	if config != nil {
		if rawDomains, ok := config["domains"]; ok && len(toStringSlice(rawDomains)) > 0 {
			return config
		}
	}

	bindingDomains := extractFloorplanBindingDomains(config)
	merged := make([]string, 0, len(SupportedDomains)+len(bindingDomains))
	merged = append(merged, SupportedDomains...)
	merged = append(merged, bindingDomains...)

	effective := maps.Clone(config)
	if effective == nil {
		effective = make(map[string]any, 1)
	}
	effective["domains"] = deduplicateStrings(merged)
	return effective
}

func extractFloorplanBindingDomains(config map[string]any) []string {
	if config == nil {
		return nil
	}
	var domains []string
	domains = append(domains, domainsFromBindings(config["haBindings"])...)
	if layout, ok := config["layout"].(map[string]any); ok {
		domains = append(domains, domainsFromBindings(layout["haBindings"])...)
	}
	return domains
}

func domainsFromBindings(raw any) []string {
	var domains []string
	appendFromMap := func(m map[string]any) {
		var entityID string
		if id, ok := m["entityId"].(string); ok {
			entityID = id
		} else if id, ok := m["entity_id"].(string); ok {
			entityID = id
		}
		entityID = strings.TrimSpace(entityID)
		if domain, _, ok := strings.Cut(entityID, "."); ok {
			domain = strings.ToLower(strings.TrimSpace(domain))
			if domain != "" {
				domains = append(domains, domain)
			}
		}
	}

	switch items := raw.(type) {
	case []any:
		for _, item := range items {
			if m, ok := item.(map[string]any); ok {
				appendFromMap(m)
			}
		}
	case []map[string]any:
		for _, m := range items {
			appendFromMap(m)
		}
	}
	return domains
}

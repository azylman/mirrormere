package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strings"
	"sync"
)

const floorplanHABindingsKey = "haBindings"

func init() {
	registerBuiltin("floorplan-3d", func() Provider {
		return NewFloorplan3dProvider()
	})
}

// Floorplan3dProvider implements Provider for the 3D floorplan widget,
// optionally hydrating live Home Assistant entity states when HA credentials are configured.
type Floorplan3dProvider struct {
	mu         sync.RWMutex
	client     *http.Client
	ha         *HomeAssistantProvider
	hydrateAll bool
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
		p.hydrateAll = false
		return nil
	}

	hydrateAll := hasFloorplanLayoutURL(config) && !hasExplicitDomains(config)
	effectiveCfg := withFloorplanDomains(config)
	ha := NewHomeAssistantProviderWithClient(p.client, "", "")
	if err := ha.Init(ctx, effectiveCfg, opts); err != nil {
		p.ha = nil
		p.hydrateAll = false
		return err
	}
	p.ha = ha
	p.hydrateAll = hydrateAll
	return nil
}

// Fetch returns an empty map in unconfigured demo mode, or {"entities": states} when HA is configured.
func (p *Floorplan3dProvider) Fetch(ctx context.Context) (any, error) {
	p.mu.RLock()
	ha := p.ha
	hydrateAll := p.hydrateAll
	p.mu.RUnlock()

	if ha == nil {
		return map[string]any{}, nil
	}

	var (
		states map[string]EntityState
		err    error
	)
	if hydrateAll {
		states, err = hydrateAllHAStates(ctx, ha)
	} else {
		states, err = ha.HydrateStates(ctx)
	}
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

func hydrateAllHAStates(ctx context.Context, ha *HomeAssistantProvider) (map[string]EntityState, error) {
	ha.mu.RLock()
	cleanBase := strings.TrimRight(ha.baseURL, "/")
	token := ha.token
	client := ha.client
	ha.mu.RUnlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cleanBase+"/api/states", http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create states request: %w", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("states request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read states response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("states request returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var rawStates []EntityState
	if err := json.Unmarshal(body, &rawStates); err != nil {
		return nil, fmt.Errorf("failed to decode states: %w", err)
	}

	states := make(map[string]EntityState, len(rawStates))
	for _, s := range rawStates {
		if strings.TrimSpace(s.EntityID) != "" {
			states[s.EntityID] = normalizeEntityState(s, cleanBase)
		}
	}
	return states, nil
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

func hasExplicitDomains(config map[string]any) bool {
	if config == nil {
		return false
	}
	rawDomains, ok := config["domains"]
	return ok && len(deduplicateStrings(toStringSlice(rawDomains))) > 0
}

func hasFloorplanLayoutURL(config map[string]any) bool {
	for _, key := range []string{"layout_url", "layoutUrl"} {
		if raw, ok := config[key].(string); ok && strings.TrimSpace(raw) != "" {
			return true
		}
	}
	return false
}

func withFloorplanDomains(config map[string]any) map[string]any {
	if hasExplicitDomains(config) {
		return config
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
	domains := domainsFromBindings(config[floorplanHABindingsKey])
	switch layout := config["layout"].(type) {
	case map[string]any:
		domains = append(domains, domainsFromBindings(layout[floorplanHABindingsKey])...)
	case string:
		if trimmed := strings.TrimSpace(layout); trimmed != "" {
			var parsed map[string]any
			if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
				domains = append(domains, domainsFromBindings(parsed[floorplanHABindingsKey])...)
			}
		}
	}
	return domains
}

func domainsFromBindings(raw any) []string {
	var domains []string
	appendEntityDomain := func(entityID string) {
		entityID = strings.TrimSpace(entityID)
		if domain, _, ok := strings.Cut(entityID, "."); ok {
			domain = strings.ToLower(strings.TrimSpace(domain))
			if domain != "" {
				domains = append(domains, domain)
			}
		}
	}
	appendFromMap := func(m map[string]any, fallbackKey string) {
		appendEntityDomain(bindingEntityID(m, fallbackKey))
	}

	switch items := raw.(type) {
	case []any:
		for _, item := range items {
			if m, ok := item.(map[string]any); ok {
				appendFromMap(m, "")
			}
		}
	case []map[string]any:
		for _, m := range items {
			appendFromMap(m, "")
		}
	case map[string]any:
		for key, val := range items {
			if m, ok := val.(map[string]any); ok {
				appendFromMap(m, key)
			} else {
				appendEntityDomain(key)
			}
		}
	case string:
		if trimmed := strings.TrimSpace(items); trimmed != "" {
			var decoded any
			if err := json.Unmarshal([]byte(trimmed), &decoded); err == nil {
				if _, isStr := decoded.(string); !isStr {
					domains = append(domains, domainsFromBindings(decoded)...)
				}
			}
		}
	}
	return domains
}

func bindingEntityID(m map[string]any, fallbackKey string) string {
	for _, key := range []string{"entityId", "entity_id"} {
		if id, ok := m[key].(string); ok && strings.TrimSpace(id) != "" {
			return id
		}
	}
	return fallbackKey
}

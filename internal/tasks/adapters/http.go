package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/azylman/mirrormere/internal/tasks"
)

// HTTPAdapterConfig holds configuration for the generic HTTP task list adapter.
type HTTPAdapterConfig struct {
	BaseURL string
	Token   string
	Client  *http.Client
}

// HTTPAdapter ingests task lists and items from an external HTTP endpoint.
type HTTPAdapter struct {
	baseURL string
	token   string
	client  *http.Client
}

// NewHTTPAdapter creates a validated HTTPAdapter.
func NewHTTPAdapter(cfg HTTPAdapterConfig) (*HTTPAdapter, error) {
	baseURL, err := validateHTTPURL(cfg.BaseURL, "", "base_url")
	if err != nil {
		return nil, err
	}
	return &HTTPAdapter{
		baseURL: baseURL,
		token:   strings.TrimSpace(cfg.Token),
		client:  defaultHTTPClient(cfg.Client),
	}, nil
}

func (a *HTTPAdapter) Name() string { return "http" }

type rawCombinedPayload struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Source    string            `json:"source"`
	Sections  []string          `json:"sections"`
	UpdatedAt *time.Time        `json:"updated_at"`
	Items     *[]tasks.ListItem `json:"items"`
}

// FetchList retrieves task list metadata and items from the configured HTTP endpoint.
func (a *HTTPAdapter) FetchList(ctx context.Context) (*tasks.List, []tasks.ListItem, error) {
	baseBody, _, err := doBearerGet(ctx, a.client, a.baseURL, a.token, "http request")
	if err != nil {
		return nil, nil, err
	}

	var combined rawCombinedPayload
	if decErr := json.Unmarshal(baseBody, &combined); decErr == nil && (combined.ID != "" || combined.Name != "" || combined.Items != nil) {
		listID := combined.ID
		if listID == "" {
			listID = a.deriveListIDFromURL()
		}
		listName := combined.Name
		if listName == "" {
			listName = listID
		}
		sections := combined.Sections
		if sections == nil {
			sections = []string{}
		}
		updatedAt := time.Now().UTC()
		if combined.UpdatedAt != nil && !combined.UpdatedAt.IsZero() {
			updatedAt = combined.UpdatedAt.UTC()
		}
		list := &tasks.List{ID: listID, Name: listName, Source: "http", Sections: sections, UpdatedAt: updatedAt}

		if combined.Items != nil {
			items := *combined.Items
			a.normalizeItems(items, list.ID)
			return list, items, nil
		}

		itemsURL, buildErr := a.buildItemsURL()
		if buildErr != nil {
			return nil, nil, fmt.Errorf("failed to build items URL: %w", buildErr)
		}
		itemsBody, _, itemsErr := doBearerGet(ctx, a.client, itemsURL, a.token, "http request")
		if itemsErr != nil {
			return nil, nil, fmt.Errorf("failed to fetch list items from %s: %w", sanitizeURL(itemsURL), itemsErr)
		}

		var items []tasks.ListItem
		if unmarshalErr := json.Unmarshal(itemsBody, &items); unmarshalErr != nil {
			return nil, nil, fmt.Errorf("failed to parse items response from %s: %w", sanitizeURL(itemsURL), unmarshalErr)
		}
		a.normalizeItems(items, list.ID)
		return list, items, nil
	}

	var directItems []tasks.ListItem
	if decErr := json.Unmarshal(baseBody, &directItems); decErr == nil {
		listID := a.deriveListIDFromURL()
		list := &tasks.List{ID: listID, Name: listID, Source: "http", Sections: []string{}, UpdatedAt: time.Now().UTC()}
		a.normalizeItems(directItems, list.ID)
		return list, directItems, nil
	}

	return nil, nil, fmt.Errorf("unexpected json response structure from %s", sanitizeURL(a.baseURL))
}

func (a *HTTPAdapter) deriveListIDFromURL() string {
	u, err := url.Parse(a.baseURL)
	if err != nil || u == nil {
		return "http-list"
	}
	trimmed := strings.TrimRight(u.Path, "/")
	if trimmed == "" {
		return "http-list"
	}
	parts := strings.Split(trimmed, "/")
	last := parts[len(parts)-1]
	if last == "items" && len(parts) > 1 {
		last = parts[len(parts)-2]
	}
	return last
}

func (a *HTTPAdapter) buildItemsURL() (string, error) {
	u, err := url.Parse(a.baseURL)
	if err != nil {
		return "", err
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/items"
	return u.String(), nil
}

func (a *HTTPAdapter) normalizeItems(items []tasks.ListItem, listID string) {
	now := time.Now().UTC()
	for i := range items {
		items[i].ListID = listID
		if items[i].ID == "" {
			items[i].ID = fmt.Sprintf("%s-item-%d", listID, i+1)
		}
		if items[i].Position == 0 && i > 0 {
			items[i].Position = i
		}
		if items[i].CreatedAt.IsZero() {
			items[i].CreatedAt = now
		}
		if items[i].UpdatedAt.IsZero() {
			items[i].UpdatedAt = now
		}
	}
}

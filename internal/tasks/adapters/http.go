package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// HTTPAdapter ingests task lists and items from an external HTTP endpoint per SPEC-008 §3.
type HTTPAdapter struct {
	baseURL string
	token   string
	client  *http.Client
}

// NewHTTPAdapter creates a validated HTTPAdapter.
func NewHTTPAdapter(cfg HTTPAdapterConfig) (*HTTPAdapter, error) {
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		return nil, errors.New("base_url is required")
	}

	u, err := url.ParseRequestURI(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("invalid base_url %q: must be http:// or https:// with host", baseURL)
	}

	client := cfg.Client
	if client == nil {
		client = &http.Client{
			Timeout: 10 * time.Second,
		}
	}

	return &HTTPAdapter{
		baseURL: baseURL,
		token:   strings.TrimSpace(cfg.Token),
		client:  client,
	}, nil
}

// Name returns the adapter type name.
func (a *HTTPAdapter) Name() string {
	return "http"
}

// rawCombinedPayload represents a response that may contain list metadata and/or embedded items.
type rawCombinedPayload struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Source    string            `json:"source"`
	Sections  []string          `json:"sections"`
	UpdatedAt *time.Time        `json:"updated_at"`
	Items     *[]tasks.ListItem `json:"items"`
}

// FetchList retrieves task list metadata and items from the configured HTTP endpoint.
// Supports both combined payloads (SPEC-008 §Source Adapters - HTTP) and split endpoints
// GET {base_url} -> List and GET {base_url}/items -> []ListItem per SPEC-008 §Wire Protocol.
func (a *HTTPAdapter) FetchList(ctx context.Context) (*tasks.List, []tasks.ListItem, error) {
	baseBody, err := a.doGet(ctx, a.baseURL)
	if err != nil {
		return nil, nil, err
	}

	// 1. Try decoding as rawCombinedPayload
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

		list := &tasks.List{
			ID:        listID,
			Name:      listName,
			Source:    "http",
			Sections:  sections,
			UpdatedAt: updatedAt,
		}

		// If items were provided in the combined payload
		if combined.Items != nil {
			items := *combined.Items
			a.normalizeItems(items, list.ID)
			return list, items, nil
		}

		// Items not provided in base response: query secondary GET {baseURL}/items per SPEC-008 §Wire Protocol
		itemsURL, buildErr := a.buildItemsURL()
		if buildErr != nil {
			return nil, nil, fmt.Errorf("failed to build items URL: %w", buildErr)
		}
		itemsBody, itemsErr := a.doGet(ctx, itemsURL)
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

	// 2. Try decoding as raw []tasks.ListItem directly from base URL
	var directItems []tasks.ListItem
	if decErr := json.Unmarshal(baseBody, &directItems); decErr == nil {
		listID := a.deriveListIDFromURL()
		list := &tasks.List{
			ID:        listID,
			Name:      listID,
			Source:    "http",
			Sections:  []string{},
			UpdatedAt: time.Now().UTC(),
		}
		a.normalizeItems(directItems, list.ID)
		return list, directItems, nil
	}

	return nil, nil, fmt.Errorf("unexpected json response structure from %s", sanitizeURL(a.baseURL))
}

func (a *HTTPAdapter) doGet(ctx context.Context, targetURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create http request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request failed for %s: %w", sanitizeURL(targetURL), err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	switch resp.StatusCode {
	case http.StatusOK:
		// valid
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, ErrUnauthorized
	case http.StatusNotFound:
		return nil, ErrUpstreamNotFound
	default:
		return nil, UpstreamHTTPError{Code: resp.StatusCode, URL: targetURL}
	}

	// Slowloris defense: read at most MaxPayloadBytes + 1
	limitReader := io.LimitReader(resp.Body, MaxPayloadBytes+1)
	buf := bytes.NewBuffer(make([]byte, 0, 16*1024))
	n, readErr := buf.ReadFrom(limitReader)
	if readErr != nil {
		return nil, fmt.Errorf("failed to read response body: %w", readErr)
	}
	if n > MaxPayloadBytes {
		return nil, ErrPayloadTooLarge
	}

	return buf.Bytes(), nil
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

// sanitizeURL strips query parameters and userinfo from a URL string to avoid leaking secrets in errors.
func sanitizeURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u == nil {
		return rawURL
	}
	u.User = nil
	u.RawQuery = ""
	return u.String()
}

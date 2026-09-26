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
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/azylman/mirrormere/internal/tasks"
)

const (
	defaultGoogleTasksBaseURL  = "https://tasks.googleapis.com"
	defaultGoogleOAuthTokenURL = "https://oauth2.googleapis.com/token"
	maxGoogleTasksPages        = 10
	googleTasksPageSize        = 100
)

// GTasksAdapterConfig holds configuration for the Google Tasks API adapter.
type GTasksAdapterConfig struct {
	TaskListID   string
	Token        string // static access token (test-only fallback)
	ClientID     string
	ClientSecret string
	RefreshToken string
	TokenURL     string // defaults to https://oauth2.googleapis.com/token
	ListName     string
	BaseURL      string
	Client       *http.Client
}

// GTasksAdapter ingests task lists and items from the Google Tasks API per SPEC-008 §3.
type GTasksAdapter struct {
	taskListID   string
	token        string
	clientID     string
	clientSecret string
	refreshToken string
	tokenURL     string
	listName     string
	baseURL      string
	client       *http.Client

	tokenMu     sync.Mutex
	cachedToken string
	tokenExpiry time.Time
}

// NewGTasksAdapter creates a validated GTasksAdapter.
func NewGTasksAdapter(cfg GTasksAdapterConfig) (*GTasksAdapter, error) {
	taskListID := strings.TrimSpace(cfg.TaskListID)
	if taskListID == "" {
		return nil, errors.New("tasklist_id is required")
	}

	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = defaultGoogleTasksBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")

	u, err := url.ParseRequestURI(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("invalid base_url %q: must be http:// or https:// with host", baseURL)
	}

	tokenURL := strings.TrimSpace(cfg.TokenURL)
	if tokenURL == "" {
		tokenURL = defaultGoogleOAuthTokenURL
	}
	tokenURL = strings.TrimRight(tokenURL, "/")

	tu, err := url.ParseRequestURI(tokenURL)
	if err != nil || (tu.Scheme != "http" && tu.Scheme != "https") || tu.Host == "" {
		return nil, fmt.Errorf("invalid token_url %q: must be http:// or https:// with host", tokenURL)
	}

	client := cfg.Client
	if client == nil {
		client = &http.Client{
			Timeout: 10 * time.Second,
		}
	}

	return &GTasksAdapter{
		taskListID:   taskListID,
		token:        strings.TrimSpace(cfg.Token),
		clientID:     strings.TrimSpace(cfg.ClientID),
		clientSecret: strings.TrimSpace(cfg.ClientSecret),
		refreshToken: strings.TrimSpace(cfg.RefreshToken),
		tokenURL:     tokenURL,
		listName:     strings.TrimSpace(cfg.ListName),
		baseURL:      baseURL,
		client:       client,
	}, nil
}

// Name returns the adapter type name.
func (a *GTasksAdapter) Name() string {
	return "gtasks"
}

type gtasksListMetadata struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Updated string `json:"updated"`
}

type gtasksItem struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Updated   string `json:"updated"`
	Status    string `json:"status"` // "needsAction" or "completed"
	Due       string `json:"due"`    // RFC 3339 timestamp e.g. "2026-09-27T00:00:00.000Z"
	Position  string `json:"position"`
	Parent    string `json:"parent"`
	Notes     string `json:"notes"`
	Deleted   bool   `json:"deleted"`
	Hidden    bool   `json:"hidden"`
	Completed string `json:"completed"`
}

type gtasksListResponse struct {
	Kind          string       `json:"kind"`
	Items         []gtasksItem `json:"items"`
	NextPageToken string       `json:"nextPageToken"`
}

// FetchList retrieves task list metadata and items from the Google Tasks API.
func (a *GTasksAdapter) FetchList(ctx context.Context) (*tasks.List, []tasks.ListItem, error) {
	// 1. Fetch or resolve list metadata
	listTitle := a.listName
	listUpdatedAt := time.Now().UTC()

	metaURL := fmt.Sprintf("%s/tasks/v1/users/@me/lists/%s", a.baseURL, url.PathEscape(a.taskListID))
	metaBody, metaErr := a.doGet(ctx, metaURL)
	if metaErr == nil {
		var meta gtasksListMetadata
		if jsonErr := json.Unmarshal(metaBody, &meta); jsonErr == nil {
			if meta.Title != "" && listTitle == "" {
				listTitle = meta.Title
			}
			if meta.Updated != "" {
				if t, parseErr := time.Parse(time.RFC3339Nano, meta.Updated); parseErr == nil {
					listUpdatedAt = t.UTC()
				}
			}
		}
	} else if errors.Is(metaErr, ErrUnauthorized) || errors.Is(metaErr, ErrUpstreamNotFound) {
		// Terminal errors on the list resource itself fail immediately
		return nil, nil, metaErr
	}

	if listTitle == "" {
		listTitle = a.taskListID
	}

	// 2. Fetch tasks items with pagination
	allGTasks := make([]gtasksItem, 0, 64)
	seenTokens := make(map[string]bool)
	pageToken := ""

	for page := 0; page < maxGoogleTasksPages; page++ {
		queryParams := url.Values{}
		queryParams.Set("showCompleted", "true")
		queryParams.Set("showHidden", "true")
		queryParams.Set("maxResults", fmt.Sprintf("%d", googleTasksPageSize))
		if pageToken != "" {
			queryParams.Set("pageToken", pageToken)
		}

		tasksURL := fmt.Sprintf("%s/tasks/v1/lists/%s/tasks?%s",
			a.baseURL, url.PathEscape(a.taskListID), queryParams.Encode())

		body, err := a.doGet(ctx, tasksURL)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to fetch tasks from google tasks: %w", err)
		}

		var pageResp gtasksListResponse
		if err := json.Unmarshal(body, &pageResp); err != nil {
			return nil, nil, fmt.Errorf("failed to parse google tasks json response: %w", err)
		}

		allGTasks = append(allGTasks, pageResp.Items...)

		pageToken = strings.TrimSpace(pageResp.NextPageToken)
		if pageToken == "" || seenTokens[pageToken] {
			break
		}
		seenTokens[pageToken] = true
	}

	// 3. Filter soft-deleted items and sort hierarchically by parent and position
	activeItems := make([]gtasksItem, 0, len(allGTasks))
	for _, gt := range allGTasks {
		// Skip soft-deleted items per Google Tasks protocol
		if gt.Deleted {
			continue
		}
		activeItems = append(activeItems, gt)
	}

	orderedGTasks := orderGTasks(activeItems)

	now := time.Now().UTC()
	items := make([]tasks.ListItem, 0, len(orderedGTasks))

	for _, gt := range orderedGTasks {
		done := strings.EqualFold(gt.Status, "completed")

		var dueDate *string
		if len(gt.Due) >= 10 {
			datePart := gt.Due[:10]
			if _, parseErr := time.Parse("2006-01-02", datePart); parseErr == nil {
				dueDate = &datePart
			}
		}

		itemUpdatedAt := now
		if gt.Updated != "" {
			if t, err := time.Parse(time.RFC3339Nano, gt.Updated); err == nil {
				itemUpdatedAt = t.UTC()
			}
		}

		items = append(items, tasks.ListItem{
			ID:        gt.ID,
			ListID:    a.taskListID,
			Title:     gt.Title,
			Done:      done,
			Position:  len(items),
			DueDate:   dueDate,
			CreatedAt: itemUpdatedAt,
			UpdatedAt: itemUpdatedAt,
		})
	}

	list := &tasks.List{
		ID:        a.taskListID,
		Name:      listTitle,
		Source:    "gtasks",
		Sections:  []string{},
		UpdatedAt: listUpdatedAt,
	}

	return list, items, nil
}

// orderGTasks sorts Google Tasks items according to their hierarchical structure:
// top-level tasks are ordered by their Position key lexicographically, with direct subtasks
// (items with Parent set) placed immediately after their parent task in sibling Position order.
func orderGTasks(items []gtasksItem) []gtasksItem {
	if len(items) <= 1 {
		return items
	}

	itemByID := make(map[string]gtasksItem, len(items))
	childrenByParent := make(map[string][]gtasksItem)
	var topLevel []gtasksItem

	for _, item := range items {
		itemByID[item.ID] = item
	}

	for _, item := range items {
		if item.Parent == "" {
			topLevel = append(topLevel, item)
		} else if _, exists := itemByID[item.Parent]; exists {
			childrenByParent[item.Parent] = append(childrenByParent[item.Parent], item)
		} else {
			// Orphaned subtask with non-existent or deleted parent; treat as top-level
			topLevel = append(topLevel, item)
		}
	}

	sortGTasks := func(slice []gtasksItem) {
		sort.SliceStable(slice, func(i, j int) bool {
			return slice[i].Position < slice[j].Position
		})
	}

	sortGTasks(topLevel)
	for parentID := range childrenByParent {
		sortGTasks(childrenByParent[parentID])
	}

	ordered := make([]gtasksItem, 0, len(items))
	visited := make(map[string]bool, len(items))

	var traverse func(item gtasksItem)
	traverse = func(item gtasksItem) {
		if visited[item.ID] {
			return
		}
		visited[item.ID] = true
		ordered = append(ordered, item)
		for _, child := range childrenByParent[item.ID] {
			traverse(child)
		}
	}

	for _, item := range topLevel {
		traverse(item)
	}

	// Safeguard against cyclic or unvisited items
	for _, item := range items {
		if !visited[item.ID] {
			traverse(item)
		}
	}

	return ordered
}

type oauthTokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	TokenType   string `json:"token_type"`
}

func (a *GTasksAdapter) getAccessToken(ctx context.Context, forceRefresh bool) (string, error) {
	a.tokenMu.Lock()
	defer a.tokenMu.Unlock()

	// If refresh token is not configured, fall back to static token (test-only)
	if a.refreshToken == "" {
		return a.token, nil
	}

	// Reuse cached token if valid and not forcing refresh
	if !forceRefresh && a.cachedToken != "" && time.Now().Add(60*time.Second).Before(a.tokenExpiry) {
		return a.cachedToken, nil
	}

	// Request new access token via refresh_token exchange
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", a.clientID)
	form.Set("client_secret", a.clientSecret)
	form.Set("refresh_token", a.refreshToken)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("failed to create oauth token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("oauth token request failed: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if readErr != nil {
		return "", fmt.Errorf("failed to read oauth token response: %w", readErr)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("oauth token refresh failed with status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp oauthTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf("failed to parse oauth token response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return "", errors.New("oauth token response missing access_token")
	}

	expiresIn := tokenResp.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}

	a.cachedToken = tokenResp.AccessToken
	a.tokenExpiry = time.Now().Add(time.Duration(expiresIn) * time.Second)

	return a.cachedToken, nil
}

func (a *GTasksAdapter) doGet(ctx context.Context, targetURL string) ([]byte, error) {
	return a.doGetWithRetry(ctx, targetURL, false)
}

func (a *GTasksAdapter) doGetWithRetry(ctx context.Context, targetURL string, isRetry bool) ([]byte, error) {
	token, err := a.getAccessToken(ctx, false)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain gtasks access token: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create http request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("google tasks request failed: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusUnauthorized && !isRetry && a.refreshToken != "" {
		// Refresh token once on 401
		if _, refreshErr := a.getAccessToken(ctx, true); refreshErr == nil {
			return a.doGetWithRetry(ctx, targetURL, true)
		}
	}

	switch resp.StatusCode {
	case http.StatusOK:
		// success
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, ErrUnauthorized
	case http.StatusNotFound:
		return nil, ErrUpstreamNotFound
	default:
		return nil, UpstreamHTTPError{Code: resp.StatusCode, URL: targetURL}
	}

	limitReader := io.LimitReader(resp.Body, MaxPayloadBytes+1)
	buf := bytes.NewBuffer(make([]byte, 0, 16*1024))
	n, readErr := buf.ReadFrom(limitReader)
	if readErr != nil {
		return nil, fmt.Errorf("failed to read google tasks response: %w", readErr)
	}
	if n > MaxPayloadBytes {
		return nil, ErrPayloadTooLarge
	}

	return buf.Bytes(), nil
}

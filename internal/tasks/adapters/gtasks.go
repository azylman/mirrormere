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

const (
	defaultGoogleTasksBaseURL = "https://tasks.googleapis.com"
	maxGoogleTasksPages       = 10
	googleTasksPageSize       = 100
)

// GTasksAdapterConfig holds configuration for the Google Tasks API adapter.
type GTasksAdapterConfig struct {
	TaskListID string
	Token      string
	ListName   string
	BaseURL    string
	Client     *http.Client
}

// GTasksAdapter ingests task lists and items from the Google Tasks API per SPEC-008 §3.
type GTasksAdapter struct {
	taskListID string
	token      string
	listName   string
	baseURL    string
	client     *http.Client
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

	client := cfg.Client
	if client == nil {
		client = &http.Client{
			Timeout: 10 * time.Second,
		}
	}

	return &GTasksAdapter{
		taskListID: taskListID,
		token:      strings.TrimSpace(cfg.Token),
		listName:   strings.TrimSpace(cfg.ListName),
		baseURL:    baseURL,
		client:     client,
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

	// 3. Map into tasks.ListItem
	now := time.Now().UTC()
	items := make([]tasks.ListItem, 0, len(allGTasks))

	for _, gt := range allGTasks {
		// Skip soft-deleted items per Google Tasks protocol
		if gt.Deleted {
			continue
		}

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

func (a *GTasksAdapter) doGet(ctx context.Context, targetURL string) ([]byte, error) {
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
		return nil, fmt.Errorf("google tasks request failed: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

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

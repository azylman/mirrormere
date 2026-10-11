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
	"strconv"
	"strings"
	"time"

	"github.com/azylman/mirrormere/internal/tasks"
)

const (
	defaultTodoistBaseURL = "https://api.todoist.com"
)

var _ Adapter = (*TodoistAdapter)(nil)

// TodoistAdapterConfig holds configuration for the Todoist tasks adapter.
type TodoistAdapterConfig struct {
	ProjectID   string
	ProjectName string
	Token       string
	BaseURL     string
	ListName    string
	Client      *http.Client
}

// TodoistAdapter ingests task lists and items from the Todoist REST API v2 and Sync API v9.
type TodoistAdapter struct {
	projectID   string
	projectName string
	token       string
	baseURL     string
	listName    string
	client      *http.Client
}

// NewTodoistAdapter creates a validated TodoistAdapter.
func NewTodoistAdapter(cfg TodoistAdapterConfig) (*TodoistAdapter, error) {
	token := strings.TrimSpace(cfg.Token)
	if token == "" {
		return nil, errors.New("token is required")
	}

	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = defaultTodoistBaseURL
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

	return &TodoistAdapter{
		projectID:   strings.TrimSpace(cfg.ProjectID),
		projectName: strings.TrimSpace(cfg.ProjectName),
		token:       token,
		baseURL:     baseURL,
		listName:    strings.TrimSpace(cfg.ListName),
		client:      client,
	}, nil
}

// Name returns the adapter type name.
func (a *TodoistAdapter) Name() string {
	return "todoist"
}

// flexibleString supports unmarshaling JSON values that can be strings, numbers, or null.
type flexibleString string

func (f *flexibleString) UnmarshalJSON(b []byte) error {
	var raw any
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	switch v := raw.(type) {
	case string:
		*f = flexibleString(strings.TrimSpace(v))
	case float64:
		*f = flexibleString(strconv.FormatInt(int64(v), 10))
	case nil:
		*f = ""
	default:
		*f = flexibleString(strings.TrimSpace(fmt.Sprintf("%v", v)))
	}
	return nil
}

func (f flexibleString) String() string {
	return string(f)
}

type todoistProject struct {
	ID             flexibleString `json:"id"`
	Name           string         `json:"name"`
	IsInboxProject bool           `json:"is_inbox_project"`
	Order          int            `json:"order"`
}

type todoistSection struct {
	ID        flexibleString `json:"id"`
	ProjectID flexibleString `json:"project_id"`
	Name      string         `json:"name"`
	Order     int            `json:"order"`
}

type todoistDue struct {
	Date     string `json:"date"`
	Datetime string `json:"datetime"`
}

type todoistTask struct {
	ID          flexibleString `json:"id"`
	ProjectID   flexibleString `json:"project_id"`
	SectionID   flexibleString `json:"section_id"`
	Content     string         `json:"content"`
	Description string         `json:"description"`
	IsCompleted bool           `json:"is_completed"`
	Order       int            `json:"order"`
	ParentID    flexibleString `json:"parent_id"`
	Priority    int            `json:"priority"`
	Due         *todoistDue    `json:"due"`
	CreatedAt   string         `json:"created_at"`
}

type todoistCompletedItem struct {
	ID            flexibleString `json:"id"`
	TaskID        flexibleString `json:"task_id"`
	Content       string         `json:"content"`
	CompletedAt   string         `json:"completed_at"`
	CompletedDate string         `json:"completed_date"`
	ProjectID     flexibleString `json:"project_id"`
	SectionID     flexibleString `json:"section_id"`
}

type todoistCompletedResponse struct {
	Items []todoistCompletedItem `json:"items"`
}

// FetchList retrieves task list metadata and items from the Todoist API.
func (a *TodoistAdapter) FetchList(ctx context.Context) (*tasks.List, []tasks.ListItem, error) {
	// 1. Resolve Project
	var (
		resolvedProjectID    string
		resolvedProjectTitle string
	)

	if a.projectID != "" {
		projectURL := fmt.Sprintf("%s/rest/v2/projects/%s", a.baseURL, url.PathEscape(a.projectID))
		body, err := a.doGet(ctx, projectURL)
		if err != nil {
			return nil, nil, err
		}
		var p todoistProject
		if err := json.Unmarshal(body, &p); err != nil {
			return nil, nil, fmt.Errorf("failed to parse todoist project from %s: %w", sanitizeURL(projectURL), err)
		}
		resolvedProjectID = string(p.ID)
		if resolvedProjectID == "" {
			resolvedProjectID = a.projectID
		}
		resolvedProjectTitle = p.Name
	} else if a.projectName != "" {
		projectsURL := fmt.Sprintf("%s/rest/v2/projects", a.baseURL)
		body, err := a.doGet(ctx, projectsURL)
		if err != nil {
			return nil, nil, err
		}
		var projects []todoistProject
		if err := json.Unmarshal(body, &projects); err != nil {
			return nil, nil, fmt.Errorf("failed to parse todoist projects from %s: %w", sanitizeURL(projectsURL), err)
		}
		var match *todoistProject
		for i := range projects {
			if strings.EqualFold(projects[i].Name, a.projectName) {
				match = &projects[i]
				break
			}
		}
		if match == nil {
			return nil, nil, ErrUpstreamNotFound
		}
		resolvedProjectID = string(match.ID)
		resolvedProjectTitle = match.Name
	} else {
		projectsURL := fmt.Sprintf("%s/rest/v2/projects", a.baseURL)
		body, err := a.doGet(ctx, projectsURL)
		if err != nil {
			return nil, nil, err
		}
		var projects []todoistProject
		if err := json.Unmarshal(body, &projects); err != nil {
			return nil, nil, fmt.Errorf("failed to parse todoist projects from %s: %w", sanitizeURL(projectsURL), err)
		}
		for i := range projects {
			if projects[i].IsInboxProject {
				resolvedProjectID = string(projects[i].ID)
				resolvedProjectTitle = projects[i].Name
				break
			}
		}
		if resolvedProjectID == "" {
			if len(projects) > 0 {
				resolvedProjectID = string(projects[0].ID)
				resolvedProjectTitle = projects[0].Name
			} else {
				resolvedProjectID = ""
				resolvedProjectTitle = "Inbox"
			}
		}
	}

	// Determine listTitle and listID
	listTitle := a.listName
	if listTitle == "" {
		listTitle = resolvedProjectTitle
	}
	if listTitle == "" {
		listTitle = a.projectID
	}
	if listTitle == "" {
		listTitle = "Inbox"
	}

	listID := resolvedProjectID
	if listID == "" {
		listID = a.projectID
	}
	if listID == "" {
		listID = "inbox"
	}

	// 2. Fetch Sections
	sections := []string{}
	sectionMap := make(map[string]string)
	if resolvedProjectID != "" {
		sectionsURL := fmt.Sprintf("%s/rest/v2/sections?project_id=%s", a.baseURL, url.QueryEscape(resolvedProjectID))
		secBody, err := a.doGet(ctx, sectionsURL)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to fetch todoist sections: %w", err)
		}
		var rawSections []todoistSection
		if err := json.Unmarshal(secBody, &rawSections); err != nil {
			return nil, nil, fmt.Errorf("failed to parse todoist sections from %s: %w", sanitizeURL(sectionsURL), err)
		}
		sort.SliceStable(rawSections, func(i, j int) bool {
			return rawSections[i].Order < rawSections[j].Order
		})
		sections = make([]string, 0, len(rawSections))
		for _, s := range rawSections {
			sections = append(sections, s.Name)
			if string(s.ID) != "" {
				sectionMap[string(s.ID)] = s.Name
			}
		}
	}

	// 3. Fetch Active Tasks
	var tasksURL string
	if resolvedProjectID != "" {
		tasksURL = fmt.Sprintf("%s/rest/v2/tasks?project_id=%s", a.baseURL, url.QueryEscape(resolvedProjectID))
	} else {
		tasksURL = fmt.Sprintf("%s/rest/v2/tasks", a.baseURL)
	}
	tasksBody, err := a.doGet(ctx, tasksURL)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to fetch todoist tasks: %w", err)
	}
	var rawTasks []todoistTask
	if err := json.Unmarshal(tasksBody, &rawTasks); err != nil {
		return nil, nil, fmt.Errorf("failed to parse todoist tasks from %s: %w", sanitizeURL(tasksURL), err)
	}

	// 4. Fetch Completed Tasks (Soft Enhancement)
	var completedTasks []tasks.ListItem
	if resolvedProjectID != "" {
		completedURL := fmt.Sprintf("%s/sync/v9/completed/get_all?project_id=%s&limit=30", a.baseURL, url.QueryEscape(resolvedProjectID))
		if completedBody, cErr := a.doGet(ctx, completedURL); cErr == nil {
			var completedResp todoistCompletedResponse
			if jsonErr := json.Unmarshal(completedBody, &completedResp); jsonErr == nil {
				for _, item := range completedResp.Items {
					itemID := string(item.TaskID)
					if itemID == "" {
						itemID = string(item.ID)
					}
					if itemID == "" {
						continue
					}
					completedDateStr := item.CompletedAt
					if completedDateStr == "" {
						completedDateStr = item.CompletedDate
					}
					completedAt := parseTimeFlexible(completedDateStr)
					var section string
					if item.SectionID != "" {
						if name, ok := sectionMap[string(item.SectionID)]; ok {
							section = name
						} else {
							section = string(item.SectionID)
						}
					}
					completedTasks = append(completedTasks, tasks.ListItem{
						ID:        itemID,
						ListID:    listID,
						Title:     item.Content,
						Done:      true,
						Section:   section,
						CreatedAt: completedAt,
						UpdatedAt: completedAt,
					})
				}
			}
		}
	}

	// 5. Hierarchical Sorting & Assembly
	orderedTasks := orderTodoistTasks(rawTasks)

	now := time.Now().UTC()
	items := make([]tasks.ListItem, 0, len(orderedTasks)+len(completedTasks))
	seenIDs := make(map[string]bool, len(orderedTasks)+len(completedTasks))

	for _, t := range orderedTasks {
		id := string(t.ID)
		if id == "" {
			continue
		}
		seenIDs[id] = true

		var section string
		if t.SectionID != "" {
			if name, ok := sectionMap[string(t.SectionID)]; ok {
				section = name
			} else {
				section = string(t.SectionID)
			}
		}

		var dueDate *string
		if t.Due != nil {
			rawDate := t.Due.Date
			if rawDate == "" {
				rawDate = t.Due.Datetime
			}
			rawDate = strings.TrimSpace(rawDate)
			if len(rawDate) >= 10 {
				datePart := rawDate[:10]
				if _, parseErr := time.Parse("2006-01-02", datePart); parseErr == nil {
					dueDate = &datePart
				}
			}
		}

		createdAt := parseTimeFlexible(t.CreatedAt)
		updatedAt := createdAt
		if updatedAt.IsZero() {
			updatedAt = now
		}

		items = append(items, tasks.ListItem{
			ID:        id,
			ListID:    listID,
			Title:     t.Content,
			Done:      t.IsCompleted,
			Section:   section,
			Position:  len(items),
			DueDate:   dueDate,
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		})
	}

	for _, c := range completedTasks {
		if seenIDs[c.ID] {
			continue
		}
		seenIDs[c.ID] = true
		c.Position = len(items)
		items = append(items, c)
	}

	list := &tasks.List{
		ID:        listID,
		Name:      listTitle,
		Source:    "todoist",
		Sections:  sections,
		UpdatedAt: now,
	}

	return list, items, nil
}

// orderTodoistTasks orders tasks hierarchically:
// top-level tasks sorted by Order ascending, children placed immediately after their parent
// sorted by Order ascending, circular references protected via visited map, and orphaned
// subtasks treated as top-level. Subtasks inherit the parent's SectionID if empty.
func orderTodoistTasks(items []todoistTask) []todoistTask {
	if len(items) == 0 {
		return items
	}

	itemByID := make(map[string]todoistTask, len(items))
	childrenByParent := make(map[string][]todoistTask)
	var topLevel []todoistTask

	for _, item := range items {
		if string(item.ID) != "" {
			itemByID[string(item.ID)] = item
		}
	}

	for _, item := range items {
		parentID := string(item.ParentID)
		if parentID == "" {
			topLevel = append(topLevel, item)
		} else if _, exists := itemByID[parentID]; exists {
			childrenByParent[parentID] = append(childrenByParent[parentID], item)
		} else {
			// Orphaned subtask with non-existent parent; treat as top-level
			topLevel = append(topLevel, item)
		}
	}

	sortTasks := func(slice []todoistTask) {
		sort.SliceStable(slice, func(i, j int) bool {
			return slice[i].Order < slice[j].Order
		})
	}

	sortTasks(topLevel)
	for parentID := range childrenByParent {
		sortTasks(childrenByParent[parentID])
	}

	ordered := make([]todoistTask, 0, len(items))
	visited := make(map[string]bool, len(items))

	var traverse func(item todoistTask, parentSectionID string)
	traverse = func(item todoistTask, parentSectionID string) {
		id := string(item.ID)
		if id != "" {
			if visited[id] {
				return
			}
			visited[id] = true
		}

		if item.SectionID == "" && parentSectionID != "" {
			item.SectionID = flexibleString(parentSectionID)
		}

		ordered = append(ordered, item)
		if id != "" {
			for _, child := range childrenByParent[id] {
				traverse(child, string(item.SectionID))
			}
		}
	}

	for _, item := range topLevel {
		traverse(item, "")
	}

	// Safeguard against cyclic or unvisited items
	for _, item := range items {
		id := string(item.ID)
		if id != "" && !visited[id] {
			traverse(item, "")
		}
	}

	return ordered
}

func (a *TodoistAdapter) doGet(ctx context.Context, targetURL string) ([]byte, error) {
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
		// success
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

func parseTimeFlexible(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Now().UTC()
	}
	formats := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t.UTC()
		}
	}
	return time.Now().UTC()
}

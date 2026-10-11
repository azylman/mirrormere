package adapters_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azylman/mirrormere/internal/tasks/adapters"
)

func TestNewTodoistAdapter_Validation(t *testing.T) {
	t.Parallel()

	// 1. Missing token
	_, err := adapters.NewTodoistAdapter(adapters.TodoistAdapterConfig{})
	if err == nil || !strings.Contains(err.Error(), "token is required") {
		t.Fatalf("expected error for empty token, got %v", err)
	}

	// 2. Invalid BaseURL
	_, err = adapters.NewTodoistAdapter(adapters.TodoistAdapterConfig{
		Token:   "secret-token",
		BaseURL: "ftp://invalid",
	})
	if err == nil || !strings.Contains(err.Error(), "must be http:// or https://") {
		t.Fatalf("expected error for invalid base_url, got %v", err)
	}

	// 3. Valid Config
	ad, err := adapters.NewTodoistAdapter(adapters.TodoistAdapterConfig{
		ProjectID: "proj-1",
		Token:     "secret-token",
		ListName:  "Household",
	})
	if err != nil {
		t.Fatalf("unexpected error for valid config: %v", err)
	}
	if ad.Name() != "todoist" {
		t.Errorf("expected Name() to be 'todoist', got %q", ad.Name())
	}
}

func TestTodoistAdapter_FetchList_Success_WithProjectID(t *testing.T) {
	t.Parallel()

	var receivedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/api/v1/projects/proj-123":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":   "proj-123",
				"name": "Family Errands",
			})
		case "/api/v1/sections":
			if r.URL.Query().Get("project_id") != "proj-123" {
				http.Error(w, "invalid project_id query", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": "sec-2", "project_id": "proj-123", "order": 2, "name": "Hardware"},
				{"id": "sec-1", "project_id": "proj-123", "order": 1, "name": "Groceries"},
			})
		case "/api/v1/tasks":
			if r.URL.Query().Get("project_id") != "proj-123" {
				http.Error(w, "invalid project_id query", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"id":         "t-1",
					"project_id": "proj-123",
					"section_id": "sec-1",
					"content":    "Buy Oat Milk",
					"order":      1,
					"due":        map[string]any{"date": "2026-10-15"},
					"created_at": "2026-10-10T12:00:00Z",
				},
				{
					"id":         "t-2",
					"project_id": "proj-123",
					"section_id": "sec-2",
					"content":    "Buy Screws",
					"order":      2,
					"created_at": "2026-10-10T12:05:00Z",
				},
			})
		case "/api/v1/tasks/completed":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{
						"id":             "c-1",
						"task_id":        "t-99",
						"content":        "Buy Bread",
						"completed_date": "2026-10-09T18:00:00Z",
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ad, err := adapters.NewTodoistAdapter(adapters.TodoistAdapterConfig{
		ProjectID: "proj-123",
		Token:     "test-token",
		BaseURL:   server.URL,
		Client:    server.Client(),
	})
	if err != nil {
		t.Fatalf("unexpected error creating adapter: %v", err)
	}

	ctx := context.Background()
	list, items, err := ad.FetchList(ctx)
	if err != nil {
		t.Fatalf("FetchList failed: %v", err)
	}

	if receivedAuth != "Bearer test-token" {
		t.Errorf("expected Authorization 'Bearer test-token', got %q", receivedAuth)
	}

	if list.ID != "proj-123" {
		t.Errorf("expected list ID 'proj-123', got %q", list.ID)
	}
	if list.Name != "Family Errands" {
		t.Errorf("expected list Name 'Family Errands', got %q", list.Name)
	}
	if len(list.Sections) != 2 || list.Sections[0] != "Groceries" || list.Sections[1] != "Hardware" {
		t.Errorf("unexpected sections: %+v", list.Sections)
	}

	if len(items) != 3 {
		t.Fatalf("expected 3 items (2 active + 1 completed), got %d", len(items))
	}

	// Active task 1
	if items[0].Title != "Buy Oat Milk" || items[0].Done != false || items[0].Section != "Groceries" {
		t.Errorf("unexpected item 0: %+v", items[0])
	}
	if items[0].DueDate == nil || *items[0].DueDate != "2026-10-15" {
		t.Errorf("unexpected due date item 0: %+v", items[0].DueDate)
	}

	// Active task 2
	if items[1].Title != "Buy Screws" || items[1].Done != false || items[1].Section != "Hardware" {
		t.Errorf("unexpected item 1: %+v", items[1])
	}

	// Completed task
	if items[2].Title != "Buy Bread" || items[2].Done != true || items[2].ID != "t-99" {
		t.Errorf("unexpected item 2: %+v", items[2])
	}
}

func TestTodoistAdapter_FetchList_ByProjectName(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/projects":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": "p-inbox", "name": "Inbox", "is_inbox_project": true},
				{"id": "p-fam", "name": "Family Tasks", "is_inbox_project": false},
			})
		case "/api/v1/sections":
			_ = json.NewEncoder(w).Encode([]map[string]any{})
		case "/api/v1/tasks":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": "t-1", "content": "Costume ideas", "order": 1},
			})
		case "/api/v1/tasks/completed":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ad, err := adapters.NewTodoistAdapter(adapters.TodoistAdapterConfig{
		ProjectName: "family tasks", // case-insensitive match
		Token:       "tok-123",
		BaseURL:     server.URL,
		Client:      server.Client(),
	})
	if err != nil {
		t.Fatalf("unexpected adapter init error: %v", err)
	}

	list, items, err := ad.FetchList(context.Background())
	if err != nil {
		t.Fatalf("FetchList failed: %v", err)
	}

	if list.ID != "p-fam" {
		t.Errorf("expected resolved list ID 'p-fam', got %q", list.ID)
	}
	if list.Name != "Family Tasks" {
		t.Errorf("expected list name 'Family Tasks', got %q", list.Name)
	}
	if len(items) != 1 || items[0].Title != "Costume ideas" {
		t.Errorf("unexpected items: %+v", items)
	}
}

func TestTodoistAdapter_FetchList_InboxFallback(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/projects":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": "p-def-inbox", "name": "Inbox", "is_inbox_project": true},
			})
		case "/api/v1/sections":
			_ = json.NewEncoder(w).Encode([]map[string]any{})
		case "/api/v1/tasks":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": "t-inbox-1", "content": "Inbox Item", "order": 1},
			})
		case "/api/v1/tasks/completed":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	// Neither ProjectID nor ProjectName configured -> fallback to Inbox
	ad, err := adapters.NewTodoistAdapter(adapters.TodoistAdapterConfig{
		Token:   "tok-inbox",
		BaseURL: server.URL,
		Client:  server.Client(),
	})
	if err != nil {
		t.Fatalf("unexpected adapter init error: %v", err)
	}

	list, items, err := ad.FetchList(context.Background())
	if err != nil {
		t.Fatalf("FetchList failed: %v", err)
	}

	if list.ID != "p-def-inbox" {
		t.Errorf("expected list ID 'p-def-inbox', got %q", list.ID)
	}
	if list.Name != "Inbox" {
		t.Errorf("expected list name 'Inbox', got %q", list.Name)
	}
	if len(items) != 1 || items[0].Title != "Inbox Item" {
		t.Errorf("unexpected items: %+v", items)
	}
}

func TestTodoistAdapter_FetchList_HierarchyAndSections(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/projects/proj-hier":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "proj-hier", "name": "Hierarchical"})
		case "/api/v1/sections":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": "sec-kitchen", "name": "Kitchen", "order": 1},
			})
		case "/api/v1/tasks":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"id":         "p-1",
					"section_id": "sec-kitchen",
					"content":    "Parent Task 1",
					"order":      1,
				},
				{
					"id":        "c-1",
					"parent_id": "p-1",
					"content":   "Subtask 1A",
					"order":     1,
				},
				{
					"id":        "c-2",
					"parent_id": "p-1",
					"content":   "Subtask 1B",
					"order":     2,
				},
				{
					"id":        "orphan-1",
					"parent_id": "non-existent-parent",
					"content":   "Orphaned Task",
					"order":     2,
				},
			})
		case "/api/v1/tasks/completed":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ad, err := adapters.NewTodoistAdapter(adapters.TodoistAdapterConfig{
		ProjectID: "proj-hier",
		Token:     "tok",
		BaseURL:   server.URL,
		Client:    server.Client(),
	})
	if err != nil {
		t.Fatalf("unexpected adapter init error: %v", err)
	}

	_, items, err := ad.FetchList(context.Background())
	if err != nil {
		t.Fatalf("FetchList failed: %v", err)
	}

	if len(items) != 4 {
		t.Fatalf("expected 4 items, got %d", len(items))
	}

	// Verify order: Parent 1 -> Subtask 1A -> Subtask 1B -> Orphaned Task
	if items[0].Title != "Parent Task 1" || items[0].Section != "Kitchen" {
		t.Errorf("expected parent task first, got %+v", items[0])
	}
	if items[1].Title != "Subtask 1A" || items[1].Section != "Kitchen" {
		t.Errorf("expected Subtask 1A second with inherited Kitchen section, got %+v", items[1])
	}
	if items[2].Title != "Subtask 1B" || items[2].Section != "Kitchen" {
		t.Errorf("expected Subtask 1B third with inherited Kitchen section, got %+v", items[2])
	}
	if items[3].Title != "Orphaned Task" {
		t.Errorf("expected Orphaned Task fourth, got %+v", items[3])
	}
}

func TestTodoistAdapter_FetchList_DueDates(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/projects/p-due":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "p-due", "name": "Due Project"})
		case "/api/v1/sections":
			_ = json.NewEncoder(w).Encode([]any{})
		case "/api/v1/tasks":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"id":      "d-1",
					"content": "Date only",
					"due":     map[string]any{"date": "2026-10-31"},
				},
				{
					"id":      "d-2",
					"content": "Datetime",
					"due":     map[string]any{"datetime": "2026-11-05T14:30:00Z"},
				},
			})
		case "/api/v1/tasks/completed":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ad, err := adapters.NewTodoistAdapter(adapters.TodoistAdapterConfig{
		ProjectID: "p-due",
		Token:     "tok",
		BaseURL:   server.URL,
		Client:    server.Client(),
	})
	if err != nil {
		t.Fatalf("unexpected adapter init error: %v", err)
	}

	_, items, err := ad.FetchList(context.Background())
	if err != nil {
		t.Fatalf("FetchList failed: %v", err)
	}

	if items[0].DueDate == nil || *items[0].DueDate != "2026-10-31" {
		t.Errorf("expected due date 2026-10-31, got %+v", items[0].DueDate)
	}
	if items[1].DueDate == nil || *items[1].DueDate != "2026-11-05" {
		t.Errorf("expected datetime truncated to 2026-11-05, got %+v", items[1].DueDate)
	}
}

func TestTodoistAdapter_FetchList_SyncDegradation(t *testing.T) {
	t.Parallel()

	// Sync endpoint returns 500 Server Error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/projects/p-degrade":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "p-degrade", "name": "Degrade Test"})
		case "/api/v1/sections":
			_ = json.NewEncoder(w).Encode([]any{})
		case "/api/v1/tasks":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": "t-act", "content": "Still Works", "order": 1},
			})
		case "/api/v1/tasks/completed":
			http.Error(w, "internal sync error", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ad, err := adapters.NewTodoistAdapter(adapters.TodoistAdapterConfig{
		ProjectID: "p-degrade",
		Token:     "tok",
		BaseURL:   server.URL,
		Client:    server.Client(),
	})
	if err != nil {
		t.Fatalf("unexpected adapter init error: %v", err)
	}

	// FetchList must still succeed with active tasks!
	list, items, err := ad.FetchList(context.Background())
	if err != nil {
		t.Fatalf("expected FetchList to succeed despite sync v9 failure, got %v", err)
	}

	if list.Name != "Degrade Test" {
		t.Errorf("expected list name 'Degrade Test', got %q", list.Name)
	}
	if len(items) != 1 || items[0].Title != "Still Works" {
		t.Errorf("expected 1 item, got %+v", items)
	}
}

func TestTodoistAdapter_FetchList_Errors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		statusCode int
		wantErr    error
	}{
		{"401 Unauthorized", http.StatusUnauthorized, adapters.ErrUnauthorized},
		{"403 Forbidden", http.StatusForbidden, adapters.ErrUnauthorized},
		{"404 Not Found", http.StatusNotFound, adapters.ErrUpstreamNotFound},
		{"429 Rate Limit", http.StatusTooManyRequests, adapters.UpstreamHTTPError{Code: 429}},
		{"500 Server Error", http.StatusInternalServerError, adapters.UpstreamHTTPError{Code: 500}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "server error", tt.statusCode)
			}))
			defer server.Close()

			ad, err := adapters.NewTodoistAdapter(adapters.TodoistAdapterConfig{
				ProjectID: "p-err",
				Token:     "tok",
				BaseURL:   server.URL,
				Client:    server.Client(),
			})
			if err != nil {
				t.Fatalf("unexpected adapter init error: %v", err)
			}

			_, _, err = ad.FetchList(context.Background())
			if err == nil {
				t.Fatalf("expected error for status %d, got nil", tt.statusCode)
			}

			if errors.Is(tt.wantErr, adapters.ErrUnauthorized) || errors.Is(tt.wantErr, adapters.ErrUpstreamNotFound) {
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("expected %v, got %v", tt.wantErr, err)
				}
			} else {
				var httpErr adapters.UpstreamHTTPError
				if errors.As(err, &httpErr) {
					if httpErr.Code != tt.statusCode {
						t.Errorf("expected status code %d, got %d", tt.statusCode, httpErr.Code)
					}
				} else {
					t.Errorf("expected UpstreamHTTPError, got %v", err)
				}
			}
		})
	}
}

func TestTodoistAdapter_FetchList_PayloadTooLarge(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Emit more than 2MB
		huge := make([]byte, adapters.MaxPayloadBytes+1024)
		for i := range huge {
			huge[i] = 'a'
		}
		_, _ = w.Write(huge)
	}))
	defer server.Close()

	ad, err := adapters.NewTodoistAdapter(adapters.TodoistAdapterConfig{
		ProjectID: "p-huge",
		Token:     "tok",
		BaseURL:   server.URL,
		Client:    server.Client(),
	})
	if err != nil {
		t.Fatalf("unexpected adapter init error: %v", err)
	}

	_, _, err = ad.FetchList(context.Background())
	if !errors.Is(err, adapters.ErrPayloadTooLarge) {
		t.Fatalf("expected ErrPayloadTooLarge, got %v", err)
	}
}


func TestTodoistAdapter_FetchList_ByProjectName_NotFound(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": "p-1", "name": "Work"},
		})
	}))
	defer server.Close()

	ad, err := adapters.NewTodoistAdapter(adapters.TodoistAdapterConfig{
		ProjectName: "NonExistent",
		Token:       "tok",
		BaseURL:     server.URL,
		Client:      server.Client(),
	})
	if err != nil {
		t.Fatalf("unexpected adapter init error: %v", err)
	}

	_, _, err = ad.FetchList(context.Background())
	if !errors.Is(err, adapters.ErrUpstreamNotFound) {
		t.Fatalf("expected ErrUpstreamNotFound, got %v", err)
	}
}

func TestTodoistAdapter_FetchList_ListNameOverride(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/projects/p-1":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "p-1", "name": "API Name"})
		case "/api/v1/sections":
			_ = json.NewEncoder(w).Encode([]any{})
		case "/api/v1/tasks":
			_ = json.NewEncoder(w).Encode([]any{})
		case "/api/v1/tasks/completed":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ad, err := adapters.NewTodoistAdapter(adapters.TodoistAdapterConfig{
		ProjectID: "p-1",
		ListName:  "Custom Overridden Name",
		Token:     "tok",
		BaseURL:   server.URL,
		Client:    server.Client(),
	})
	if err != nil {
		t.Fatalf("unexpected adapter init error: %v", err)
	}

	list, _, err := ad.FetchList(context.Background())
	if err != nil {
		t.Fatalf("FetchList failed: %v", err)
	}
	if list.Name != "Custom Overridden Name" {
		t.Errorf("expected list name 'Custom Overridden Name', got %q", list.Name)
	}
}

func TestTodoistAdapter_FetchList_NumericIDsAndFlexibleString(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/projects":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 2203306141, "name": "Numeric Project", "is_inbox_project": false, "order": 1},
			})
		case "/api/v1/sections":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 998877, "project_id": 2203306141, "name": "Numeric Section", "order": 1},
			})
		case "/api/v1/tasks":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"id":         11223344,
					"project_id": 2203306141,
					"section_id": 998877,
					"content":    "Task with Numeric ID",
					"order":      1,
				},
			})
		case "/api/v1/tasks/completed":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{
						"id":         556677,
						"task_id":    889900,
						"content":    "Completed Numeric Task",
						"section_id": 998877,
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ad, err := adapters.NewTodoistAdapter(adapters.TodoistAdapterConfig{
		Token:   "tok",
		BaseURL: server.URL,
		Client:  server.Client(),
	})
	if err != nil {
		t.Fatalf("unexpected adapter init error: %v", err)
	}

	list, items, err := ad.FetchList(context.Background())
	if err != nil {
		t.Fatalf("FetchList failed: %v", err)
	}

	if list.ID != "2203306141" {
		t.Errorf("expected list ID '2203306141', got %q", list.ID)
	}
	if list.Name != "Numeric Project" {
		t.Errorf("expected list Name 'Numeric Project', got %q", list.Name)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].ID != "11223344" || items[0].Section != "Numeric Section" {
		t.Errorf("unexpected item 0: %+v", items[0])
	}
	if items[1].ID != "889900" || items[1].Section != "Numeric Section" || !items[1].Done {
		t.Errorf("unexpected completed item 1: %+v", items[1])
	}
}

func TestTodoistAdapter_FetchList_CycleProtection(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/projects/p-cycle":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "p-cycle", "name": "Cycle Project"})
		case "/api/v1/sections":
			_ = json.NewEncoder(w).Encode([]any{})
		case "/api/v1/tasks":
			// Mutual circular parent references
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": "t-1", "parent_id": "t-2", "content": "Cycle A", "order": 1},
				{"id": "t-2", "parent_id": "t-1", "content": "Cycle B", "order": 2},
			})
		case "/api/v1/tasks/completed":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ad, err := adapters.NewTodoistAdapter(adapters.TodoistAdapterConfig{
		ProjectID: "p-cycle",
		Token:     "tok",
		BaseURL:   server.URL,
		Client:    server.Client(),
	})
	if err != nil {
		t.Fatalf("unexpected adapter init error: %v", err)
	}

	_, items, err := ad.FetchList(context.Background())
	if err != nil {
		t.Fatalf("FetchList failed: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items despite cycle, got %d", len(items))
	}
}

func TestTodoistAdapter_FetchList_EmptyProjectsFallback(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/projects":
			_ = json.NewEncoder(w).Encode([]any{})
		case "/api/v1/sections":
			_ = json.NewEncoder(w).Encode([]any{})
		case "/api/v1/tasks":
			_ = json.NewEncoder(w).Encode([]any{})
		case "/api/v1/tasks/completed":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ad, err := adapters.NewTodoistAdapter(adapters.TodoistAdapterConfig{
		Token:   "tok",
		BaseURL: server.URL,
		Client:  server.Client(),
	})
	if err != nil {
		t.Fatalf("unexpected adapter init error: %v", err)
	}

	list, items, err := ad.FetchList(context.Background())
	if err != nil {
		t.Fatalf("FetchList failed: %v", err)
	}
	if list.ID != "inbox" {
		t.Errorf("expected list ID 'inbox', got %q", list.ID)
	}
	if list.Name != "Inbox" {
		t.Errorf("expected list Name 'Inbox', got %q", list.Name)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items, got %d", len(items))
	}
}

func TestTodoist_CoverageGaps(t *testing.T) {
	t.Parallel()

	// 1. Malformed JSON on single project endpoint
	t.Run("malformed_project_json", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{invalid-json"))
		}))
		defer srv.Close()

		ad, _ := adapters.NewTodoistAdapter(adapters.TodoistAdapterConfig{
			ProjectID: "proj-bad",
			Token:     "tok",
			BaseURL:   srv.URL,
			Client:    srv.Client(),
		})
		_, _, err := ad.FetchList(context.Background())
		if err == nil || !strings.Contains(err.Error(), "failed to parse todoist project") {
			t.Fatalf("expected failed to parse todoist project error, got %v", err)
		}
	})

	// 2. Malformed JSON on projects list endpoint
	t.Run("malformed_projects_list_json", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{not-an-array"))
		}))
		defer srv.Close()

		ad, _ := adapters.NewTodoistAdapter(adapters.TodoistAdapterConfig{
			ProjectName: "Family",
			Token:       "tok",
			BaseURL:     srv.URL,
			Client:      srv.Client(),
		})
		_, _, err := ad.FetchList(context.Background())
		if err == nil || !strings.Contains(err.Error(), "failed to parse todoist projects") {
			t.Fatalf("expected failed to parse todoist projects error, got %v", err)
		}
	})

	// 3. Fallback when projects exist but none marked as inbox
	t.Run("projects_without_inbox_fallback", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/api/v1/projects":
				_ = json.NewEncoder(w).Encode([]map[string]any{
					{"id": "p-first", "name": "First Project", "is_inbox_project": false},
				})
			case "/api/v1/sections":
				_ = json.NewEncoder(w).Encode([]any{})
			case "/api/v1/tasks":
				_ = json.NewEncoder(w).Encode([]any{})
			case "/api/v1/tasks/completed":
				_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
			default:
				http.NotFound(w, r)
			}
		}))
		defer srv.Close()

		ad, _ := adapters.NewTodoistAdapter(adapters.TodoistAdapterConfig{
			Token:   "tok",
			BaseURL: srv.URL,
			Client:  srv.Client(),
		})
		list, _, err := ad.FetchList(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if list.ID != "p-first" || list.Name != "First Project" {
			t.Errorf("expected fallback to first project, got ID=%q Name=%q", list.ID, list.Name)
		}
	})

	// 4. Malformed JSON on sections
	t.Run("malformed_sections_json", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/api/v1/projects/p-1":
				_ = json.NewEncoder(w).Encode(map[string]any{"id": "p-1", "name": "P1"})
			case "/api/v1/sections":
				_, _ = w.Write([]byte("{invalid-sections"))
			default:
				http.NotFound(w, r)
			}
		}))
		defer srv.Close()

		ad, _ := adapters.NewTodoistAdapter(adapters.TodoistAdapterConfig{
			ProjectID: "p-1",
			Token:     "tok",
			BaseURL:   srv.URL,
			Client:    srv.Client(),
		})
		_, _, err := ad.FetchList(context.Background())
		if err == nil || !strings.Contains(err.Error(), "failed to parse todoist sections") {
			t.Fatalf("expected failed to parse todoist sections error, got %v", err)
		}
	})

	// 5. Malformed JSON on tasks
	t.Run("malformed_tasks_json", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/api/v1/projects/p-1":
				_ = json.NewEncoder(w).Encode(map[string]any{"id": "p-1", "name": "P1"})
			case "/api/v1/sections":
				_ = json.NewEncoder(w).Encode([]any{})
			case "/api/v1/tasks":
				_, _ = w.Write([]byte("{invalid-tasks"))
			default:
				http.NotFound(w, r)
			}
		}))
		defer srv.Close()

		ad, _ := adapters.NewTodoistAdapter(adapters.TodoistAdapterConfig{
			ProjectID: "p-1",
			Token:     "tok",
			BaseURL:   srv.URL,
			Client:    srv.Client(),
		})
		_, _, err := ad.FetchList(context.Background())
		if err == nil || !strings.Contains(err.Error(), "failed to parse todoist tasks") {
			t.Fatalf("expected failed to parse todoist tasks error, got %v", err)
		}
	})

	// 6. Numeric and null IDs + invalid date parsing fallback
	t.Run("numeric_and_null_ids", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/api/v1/projects/12345":
				_ = json.NewEncoder(w).Encode(map[string]any{"id": 12345, "name": "Numeric Project"})
			case "/api/v1/sections":
				_ = json.NewEncoder(w).Encode([]map[string]any{
					{"id": 999, "name": "Num Sec", "order": 1},
				})
			case "/api/v1/tasks":
				_ = json.NewEncoder(w).Encode([]map[string]any{
					{"id": 555, "section_id": 999, "content": "Numeric ID Task", "parent_id": nil, "order": 1, "due": nil, "created_at": "invalid-date"},
				})
			case "/api/v1/tasks/completed":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"items": []map[string]any{
						{"id": 777, "task_id": 777, "content": "Comp Num", "completed_date": "invalid-date", "section_id": 999},
					},
				})
			default:
				http.NotFound(w, r)
			}
		}))
		defer srv.Close()

		ad, _ := adapters.NewTodoistAdapter(adapters.TodoistAdapterConfig{
			ProjectID: "12345",
			Token:     "tok",
			BaseURL:   srv.URL,
			Client:    srv.Client(),
		})
		list, items, err := ad.FetchList(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if list.ID != "12345" {
			t.Errorf("expected list ID 12345, got %q", list.ID)
		}
		if len(items) != 2 {
			t.Fatalf("expected 2 items, got %d", len(items))
		}
	})
}

func TestTodoistAdapter_CompleteItem(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		statusCode int
		itemID     string
		cancelCtx  bool
		wantErr    error
		wantCode   int
		expectErr  bool
	}{
		{
			name:       "204 No Content success",
			statusCode: http.StatusNoContent,
			itemID:     "task-123",
			expectErr:  false,
		},
		{
			name:       "200 OK success",
			statusCode: http.StatusOK,
			itemID:     "task-456",
			expectErr:  false,
		},
		{
			name:       "401 Unauthorized",
			statusCode: http.StatusUnauthorized,
			itemID:     "task-123",
			wantErr:    adapters.ErrUnauthorized,
			expectErr:  true,
		},
		{
			name:       "403 Forbidden",
			statusCode: http.StatusForbidden,
			itemID:     "task-123",
			wantErr:    adapters.ErrUnauthorized,
			expectErr:  true,
		},
		{
			name:       "404 Not Found",
			statusCode: http.StatusNotFound,
			itemID:     "task-123",
			wantErr:    adapters.ErrUpstreamNotFound,
			expectErr:  true,
		},
		{
			name:       "429 Too Many Requests",
			statusCode: http.StatusTooManyRequests,
			itemID:     "task-123",
			wantCode:   429,
			expectErr:  true,
		},
		{
			name:       "500 Internal Server Error",
			statusCode: http.StatusInternalServerError,
			itemID:     "task-123",
			wantCode:   500,
			expectErr:  true,
		},
		{
			name:      "empty item ID",
			itemID:    "",
			expectErr: true,
		},
		{
			name:      "path traversal item ID with slashes",
			itemID:    "task/sub",
			expectErr: true,
		},
		{
			name:      "path traversal item ID with dotdot",
			itemID:    "../evil",
			expectErr: true,
		},
		{
			name:      "canceled context",
			itemID:    "task-123",
			cancelCtx: true,
			expectErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var capturedAuth string
			var capturedMethod string
			var capturedPath string

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedAuth = r.Header.Get("Authorization")
				capturedMethod = r.Method
				capturedPath = r.URL.Path

				w.WriteHeader(tc.statusCode)
			}))
			defer srv.Close()

			ad, err := adapters.NewTodoistAdapter(adapters.TodoistAdapterConfig{
				Token:   "secret-token",
				BaseURL: srv.URL,
				Client:  srv.Client(),
			})
			if err != nil {
				t.Fatalf("failed to create adapter: %v", err)
			}

			ctx := context.Background()
			if tc.cancelCtx {
				cctx, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cctx
			}

			err = ad.CompleteItem(ctx, tc.itemID)

			if tc.expectErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
					t.Fatalf("expected error %v, got %v", tc.wantErr, err)
				}
				if tc.wantCode != 0 {
					var httpErr adapters.UpstreamHTTPError
					if errors.As(err, &httpErr) {
						if httpErr.Code != tc.wantCode {
							t.Errorf("expected code %d, got %d", tc.wantCode, httpErr.Code)
						}
					} else {
						t.Errorf("expected UpstreamHTTPError, got %T: %v", err, err)
					}
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if capturedMethod != http.MethodPost {
					t.Errorf("expected method POST, got %q", capturedMethod)
				}
				if capturedAuth != "Bearer secret-token" {
					t.Errorf("expected auth 'Bearer secret-token', got %q", capturedAuth)
				}
				expectedPath := "/api/v1/tasks/" + tc.itemID + "/close"
				if capturedPath != expectedPath {
					t.Errorf("expected path %q, got %q", expectedPath, capturedPath)
				}
			}
		})
	}
}

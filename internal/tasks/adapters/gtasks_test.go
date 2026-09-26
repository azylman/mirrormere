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

func TestNewGTasksAdapter_Validation(t *testing.T) {
	t.Parallel()

	// 1. Missing tasklist_id
	_, err := adapters.NewGTasksAdapter(adapters.GTasksAdapterConfig{})
	if err == nil || !strings.Contains(err.Error(), "tasklist_id is required") {
		t.Fatalf("expected error for empty tasklist_id, got %v", err)
	}

	// 2. Invalid BaseURL
	_, err = adapters.NewGTasksAdapter(adapters.GTasksAdapterConfig{
		TaskListID: "test-id",
		BaseURL:    "ftp://invalid",
	})
	if err == nil || !strings.Contains(err.Error(), "must be http:// or https://") {
		t.Fatalf("expected error for invalid base_url, got %v", err)
	}

	// 3. Valid Config
	ad, err := adapters.NewGTasksAdapter(adapters.GTasksAdapterConfig{
		TaskListID: "my-tasks",
		Token:      "oauth-token",
		ListName:   "Personal Tasks",
	})
	if err != nil {
		t.Fatalf("unexpected error for valid config: %v", err)
	}
	if ad.Name() != "gtasks" {
		t.Errorf("expected Name() to be 'gtasks', got %q", ad.Name())
	}
}

func TestGTasksAdapter_FetchList_Success(t *testing.T) {
	t.Parallel()

	var receivedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Path == "/tasks/v1/users/@me/lists/my-list" {
			meta := map[string]any{
				"id":      "my-list",
				"title":   "Daily Chores",
				"updated": "2026-09-26T12:00:00.000Z",
			}
			_ = json.NewEncoder(w).Encode(meta)
			return
		}

		if r.URL.Path == "/tasks/v1/lists/my-list/tasks" {
			// Verify query parameters
			q := r.URL.Query()
			if q.Get("showCompleted") != "true" || q.Get("showHidden") != "true" {
				http.Error(w, "missing required query parameters", http.StatusBadRequest)
				return
			}

			resp := map[string]any{
				"kind": "tasks#tasks",
				"items": []map[string]any{
					{
						"id":      "task-1",
						"title":   "Clean room",
						"status":  "needsAction",
						"due":     "2026-09-28T00:00:00.000Z",
						"updated": "2026-09-26T10:00:00.000Z",
					},
					{
						"id":      "task-2",
						"title":   "Fold laundry",
						"status":  "completed",
						"updated": "2026-09-26T11:00:00.000Z",
					},
					{
						"id":      "task-3-deleted",
						"title":   "Discarded task",
						"deleted": true,
					},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		http.NotFound(w, r)
	}))
	defer server.Close()

	ad, err := adapters.NewGTasksAdapter(adapters.GTasksAdapterConfig{
		TaskListID: "my-list",
		Token:      "oauth2-bearer-token",
		BaseURL:    server.URL,
		Client:     server.Client(),
	})
	if err != nil {
		t.Fatalf("failed to create gtasks adapter: %v", err)
	}

	list, items, err := ad.FetchList(context.Background())
	if err != nil {
		t.Fatalf("FetchList failed: %v", err)
	}

	if receivedAuth != "Bearer oauth2-bearer-token" {
		t.Errorf("expected Bearer oauth2-bearer-token, got %q", receivedAuth)
	}
	if list.ID != "my-list" || list.Name != "Daily Chores" || list.Source != "gtasks" {
		t.Errorf("unexpected list: %+v", list)
	}
	// Verify deleted task was filtered out
	if len(items) != 2 {
		t.Fatalf("expected 2 items (excluding soft-deleted), got %d", len(items))
	}
	if items[0].Title != "Clean room" || items[0].Done || items[0].DueDate == nil || *items[0].DueDate != "2026-09-28" {
		t.Errorf("unexpected item 0: %+v", items[0])
	}
	if items[1].Title != "Fold laundry" || !items[1].Done || items[1].DueDate != nil {
		t.Errorf("unexpected item 1: %+v", items[1])
	}
}

func TestGTasksAdapter_FetchList_PaginationAndCycleGuard(t *testing.T) {
	t.Parallel()

	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Path == "/tasks/v1/users/@me/lists/paged-list" {
			// Fail metadata call to exercise fallback name
			http.Error(w, "Metadata unavailable", http.StatusServiceUnavailable)
			return
		}

		if r.URL.Path == "/tasks/v1/lists/paged-list/tasks" {
			calls++
			pageToken := r.URL.Query().Get("pageToken")
			if pageToken == "" {
				resp := map[string]any{
					"items": []map[string]any{
						{"id": "t-1", "title": "Page 1 Item"},
					},
					"nextPageToken": "page-2-token",
				}
				_ = json.NewEncoder(w).Encode(resp)
				return
			}
			if pageToken == "page-2-token" {
				// Returns token pointing back to page-2-token to test cycle prevention
				resp := map[string]any{
					"items": []map[string]any{
						{"id": "t-2", "title": "Page 2 Item"},
					},
					"nextPageToken": "page-2-token",
				}
				_ = json.NewEncoder(w).Encode(resp)
				return
			}
		}

		http.NotFound(w, r)
	}))
	defer server.Close()

	ad, err := adapters.NewGTasksAdapter(adapters.GTasksAdapterConfig{
		TaskListID: "paged-list",
		ListName:   "Fallback Title",
		BaseURL:    server.URL,
		Client:     server.Client(),
	})
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}

	list, items, err := ad.FetchList(context.Background())
	if err != nil {
		t.Fatalf("FetchList failed: %v", err)
	}

	if list.Name != "Fallback Title" {
		t.Errorf("expected fallback title 'Fallback Title', got %q", list.Name)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items across pages, got %d", len(items))
	}
	if calls != 2 {
		t.Errorf("expected 2 calls before cycle break, got %d", calls)
	}
}

func TestGTasksAdapter_FetchList_Errors(t *testing.T) {
	t.Parallel()

	// 1. 401 Unauthorized on metadata call
	server401 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	}))
	defer server401.Close()

	ad401, _ := adapters.NewGTasksAdapter(adapters.GTasksAdapterConfig{
		TaskListID: "list-401",
		BaseURL:    server401.URL,
		Client:     server401.Client(),
	})
	_, _, err := ad401.FetchList(context.Background())
	if !errors.Is(err, adapters.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}

	// 2. 404 Not Found on metadata call
	server404 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server404.Close()

	ad404, _ := adapters.NewGTasksAdapter(adapters.GTasksAdapterConfig{
		TaskListID: "list-404",
		BaseURL:    server404.URL,
		Client:     server404.Client(),
	})
	_, _, err = ad404.FetchList(context.Background())
	if !errors.Is(err, adapters.ErrUpstreamNotFound) {
		t.Fatalf("expected ErrUpstreamNotFound, got %v", err)
	}

	// 3. 500 Server Error on tasks fetch
	server500 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/users/@me/lists") {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "list-500", "title": "Test"})
			return
		}
		http.Error(w, "API Crash", http.StatusInternalServerError)
	}))
	defer server500.Close()

	ad500, _ := adapters.NewGTasksAdapter(adapters.GTasksAdapterConfig{
		TaskListID: "list-500",
		BaseURL:    server500.URL,
		Client:     server500.Client(),
	})
	_, _, err = ad500.FetchList(context.Background())
	if err == nil || !strings.Contains(err.Error(), "status 500") {
		t.Fatalf("expected status 500 error, got %v", err)
	}

	// 4. Slowloris Payload limit exceeded (> 2MB)
	serverLarge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/users/@me/lists") {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "list-large", "title": "Large"})
			return
		}
		payload := strings.Repeat("X", adapters.MaxPayloadBytes+2048)
		_, _ = w.Write([]byte(payload))
	}))
	defer serverLarge.Close()

	adLarge, _ := adapters.NewGTasksAdapter(adapters.GTasksAdapterConfig{
		TaskListID: "list-large",
		BaseURL:    serverLarge.URL,
		Client:     serverLarge.Client(),
	})
	_, _, err = adLarge.FetchList(context.Background())
	if !errors.Is(err, adapters.ErrPayloadTooLarge) {
		t.Fatalf("expected ErrPayloadTooLarge, got %v", err)
	}

	// 5. Malformed JSON on tasks response
	serverBadJSON := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/users/@me/lists") {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "list-bad", "title": "Bad"})
			return
		}
		_, _ = w.Write([]byte(`{ "unclosed": `))
	}))
	defer serverBadJSON.Close()

	adBadJSON, _ := adapters.NewGTasksAdapter(adapters.GTasksAdapterConfig{
		TaskListID: "list-bad",
		BaseURL:    serverBadJSON.URL,
		Client:     serverBadJSON.Client(),
	})
	_, _, err = adBadJSON.FetchList(context.Background())
	if err == nil || !strings.Contains(err.Error(), "failed to parse google tasks json response") {
		t.Fatalf("expected parse json error, got %v", err)
	}

	// 6. Network failure
	closedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closedURL := closedServer.URL
	closedServer.Close()

	adClosed, _ := adapters.NewGTasksAdapter(adapters.GTasksAdapterConfig{
		TaskListID: "list-closed",
		BaseURL:    closedURL,
	})
	_, _, err = adClosed.FetchList(context.Background())
	if err == nil || !strings.Contains(err.Error(), "failed to fetch tasks from google tasks") {
		t.Fatalf("expected network failure error, got %v", err)
	}
}

func TestGTasksAdapter_FetchList_TimestampsAndDefaultTitle(t *testing.T) {
	t.Parallel()

	// Server returning RFC3339 timestamps (without fractional seconds) and empty metadata title
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/users/@me/lists") {
			// Title is empty, Updated is RFC3339 without fractions
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":      "simple-list",
				"title":   "",
				"updated": "2026-09-26T14:30:00Z",
			})
			return
		}

		if strings.Contains(r.URL.Path, "/tasks") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{
						"id":      "item-standard",
						"title":   "Standard Task",
						"status":  "needsAction",
						"updated": "2026-09-26T14:35:00Z",
						"due":     "invalid-date",
					},
				},
			})
			return
		}
	}))
	defer server.Close()

	ad, err := adapters.NewGTasksAdapter(adapters.GTasksAdapterConfig{
		TaskListID: "simple-list",
		BaseURL:    server.URL,
		Client:     server.Client(),
	})
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}

	list, items, err := ad.FetchList(context.Background())
	if err != nil {
		t.Fatalf("FetchList failed: %v", err)
	}

	// When metadata title and ListName are empty, title defaults to TaskListID
	if list.Name != "simple-list" {
		t.Errorf("expected list name 'simple-list', got %q", list.Name)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	// Invalid due date should result in nil DueDate
	if items[0].DueDate != nil {
		t.Errorf("expected nil DueDate for invalid-date, got %v", *items[0].DueDate)
	}
}

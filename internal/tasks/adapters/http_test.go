package adapters_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/tasks/adapters"
)

func TestNewHTTPAdapter_Validation(t *testing.T) {
	t.Parallel()

	// 1. Empty BaseURL
	_, err := adapters.NewHTTPAdapter(adapters.HTTPAdapterConfig{})
	if err == nil || !strings.Contains(err.Error(), "base_url is required") {
		t.Fatalf("expected error for empty base_url, got %v", err)
	}

	// 2. Invalid Scheme
	_, err = adapters.NewHTTPAdapter(adapters.HTTPAdapterConfig{
		BaseURL: "ftp://example.com/tasks",
	})
	if err == nil || !strings.Contains(err.Error(), "must be http:// or https://") {
		t.Fatalf("expected error for ftp scheme, got %v", err)
	}

	// 3. No Host
	_, err = adapters.NewHTTPAdapter(adapters.HTTPAdapterConfig{
		BaseURL: "http://",
	})
	if err == nil || !strings.Contains(err.Error(), "must be http:// or https:// with host") {
		t.Fatalf("expected error for missing host, got %v", err)
	}

	// 4. Valid Config with default client
	ad, err := adapters.NewHTTPAdapter(adapters.HTTPAdapterConfig{
		BaseURL: "https://example.com/api/lists/family-todo",
		Token:   "secret-token",
	})
	if err != nil {
		t.Fatalf("unexpected error for valid config: %v", err)
	}
	if ad.Name() != "http" {
		t.Errorf("expected Name() to be 'http', got %q", ad.Name())
	}
}

func TestHTTPAdapter_FetchList_CombinedPayload(t *testing.T) {
	t.Parallel()

	var receivedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")

		now := time.Now().UTC().Format(time.RFC3339)
		resp := map[string]any{
			"id":         "groceries",
			"name":       "Weekly Groceries",
			"sections":   []string{"Produce", "Dairy"},
			"updated_at": now,
			"items": []map[string]any{
				{
					"id":       "item-1",
					"title":    "Apples",
					"done":     false,
					"section":  "Produce",
					"position": 0,
				},
				{
					"id":       "item-2",
					"title":    "Milk",
					"done":     true,
					"section":  "Dairy",
					"position": 1,
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	ad, err := adapters.NewHTTPAdapter(adapters.HTTPAdapterConfig{
		BaseURL: server.URL + "/lists/groceries",
		Token:   "test-token",
		Client:  server.Client(),
	})
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}

	list, items, err := ad.FetchList(context.Background())
	if err != nil {
		t.Fatalf("FetchList failed: %v", err)
	}

	if receivedAuth != "Bearer test-token" {
		t.Errorf("expected Bearer test-token, got %q", receivedAuth)
	}
	if list.ID != "groceries" || list.Name != "Weekly Groceries" || list.Source != "http" {
		t.Errorf("unexpected list: %+v", list)
	}
	if len(list.Sections) != 2 || list.Sections[0] != "Produce" {
		t.Errorf("unexpected sections: %+v", list.Sections)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].Title != "Apples" || items[0].Done {
		t.Errorf("unexpected item 0: %+v", items[0])
	}
	if items[1].Title != "Milk" || !items[1].Done {
		t.Errorf("unexpected item 1: %+v", items[1])
	}
}

func TestHTTPAdapter_FetchList_SplitEndpoints(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/todo" {
			// Base URL returns List metadata without items
			listResp := map[string]any{
				"id":   "todo-list",
				"name": "General To-Do",
			}
			_ = json.NewEncoder(w).Encode(listResp)
			return
		}

		if r.URL.Path == "/api/todo/items" {
			// Secondary URL returns []tasks.ListItem
			itemsResp := []map[string]any{
				{
					"title": "Clean desk",
					"done":  false,
				},
			}
			_ = json.NewEncoder(w).Encode(itemsResp)
			return
		}

		http.NotFound(w, r)
	}))
	defer server.Close()

	ad, err := adapters.NewHTTPAdapter(adapters.HTTPAdapterConfig{
		BaseURL: server.URL + "/api/todo",
		Client:  server.Client(),
	})
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}

	list, items, err := ad.FetchList(context.Background())
	if err != nil {
		t.Fatalf("FetchList failed: %v", err)
	}

	if list.ID != "todo-list" || list.Name != "General To-Do" {
		t.Errorf("unexpected list: %+v", list)
	}
	if len(items) != 1 || items[0].Title != "Clean desk" {
		t.Fatalf("unexpected items: %+v", items)
	}
	// Verify synthetic ID generation
	if !strings.HasPrefix(items[0].ID, "todo-list-item-") {
		t.Errorf("expected synthetic item ID with prefix, got %q", items[0].ID)
	}
}

func TestHTTPAdapter_FetchList_DirectItemsArray(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		itemsResp := []map[string]any{
			{
				"id":    "item-raw-1",
				"title": "Mow lawn",
				"done":  false,
			},
		}
		_ = json.NewEncoder(w).Encode(itemsResp)
	}))
	defer server.Close()

	ad, err := adapters.NewHTTPAdapter(adapters.HTTPAdapterConfig{
		BaseURL: server.URL + "/chores",
		Client:  server.Client(),
	})
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}

	list, items, err := ad.FetchList(context.Background())
	if err != nil {
		t.Fatalf("FetchList failed: %v", err)
	}

	if list.ID != "chores" {
		t.Errorf("expected derived list ID 'chores', got %q", list.ID)
	}
	if len(items) != 1 || items[0].Title != "Mow lawn" {
		t.Fatalf("unexpected items: %+v", items)
	}
}

func TestHTTPAdapter_FetchList_Errors(t *testing.T) {
	t.Parallel()

	// 1. 401 Unauthorized
	server401 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	}))
	defer server401.Close()

	ad401, _ := adapters.NewHTTPAdapter(adapters.HTTPAdapterConfig{
		BaseURL: server401.URL,
		Client:  server401.Client(),
	})
	_, _, err := ad401.FetchList(context.Background())
	if !errors.Is(err, adapters.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}

	// 2. 404 Not Found
	server404 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server404.Close()

	ad404, _ := adapters.NewHTTPAdapter(adapters.HTTPAdapterConfig{
		BaseURL: server404.URL,
		Client:  server404.Client(),
	})
	_, _, err = ad404.FetchList(context.Background())
	if !errors.Is(err, adapters.ErrUpstreamNotFound) {
		t.Fatalf("expected ErrUpstreamNotFound, got %v", err)
	}

	// 3. 500 Internal Server Error
	server500 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Crash", http.StatusInternalServerError)
	}))
	defer server500.Close()

	ad500, _ := adapters.NewHTTPAdapter(adapters.HTTPAdapterConfig{
		BaseURL: server500.URL,
		Client:  server500.Client(),
	})
	_, _, err = ad500.FetchList(context.Background())
	if err == nil || !strings.Contains(err.Error(), "status 500") {
		t.Fatalf("expected status 500 error, got %v", err)
	}

	// 4. Slowloris Ceiling Exceeded (> 2MB)
	serverLarge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Stream more than 2MB
		payload := strings.Repeat("A", adapters.MaxPayloadBytes+1024)
		_, _ = w.Write([]byte(payload))
	}))
	defer serverLarge.Close()

	adLarge, _ := adapters.NewHTTPAdapter(adapters.HTTPAdapterConfig{
		BaseURL: serverLarge.URL,
		Client:  serverLarge.Client(),
	})
	_, _, err = adLarge.FetchList(context.Background())
	if !errors.Is(err, adapters.ErrPayloadTooLarge) {
		t.Fatalf("expected ErrPayloadTooLarge, got %v", err)
	}

	// 5. Malformed JSON
	serverBadJSON := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{ "invalid_json": `))
	}))
	defer serverBadJSON.Close()

	adBadJSON, _ := adapters.NewHTTPAdapter(adapters.HTTPAdapterConfig{
		BaseURL: serverBadJSON.URL,
		Client:  serverBadJSON.Client(),
	})
	_, _, err = adBadJSON.FetchList(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unexpected json response structure") {
		t.Fatalf("expected unexpected json response structure error, got %v", err)
	}

	// 6. Secondary /items endpoint fails with error
	serverBadItems := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/list" {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "test-list"})
			return
		}
		http.Error(w, "Items Crash", http.StatusInternalServerError)
	}))
	defer serverBadItems.Close()

	adBadItems, _ := adapters.NewHTTPAdapter(adapters.HTTPAdapterConfig{
		BaseURL: serverBadItems.URL + "/list",
		Client:  serverBadItems.Client(),
	})
	_, _, err = adBadItems.FetchList(context.Background())
	if err == nil || !strings.Contains(err.Error(), "failed to fetch list items") {
		t.Fatalf("expected failed to fetch list items error, got %v", err)
	}

	// 7. Secondary /items returns malformed JSON
	serverBadItemsJSON := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/list" {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "test-list"})
			return
		}
		_, _ = w.Write([]byte(`not json`))
	}))
	defer serverBadItemsJSON.Close()

	adBadItemsJSON, _ := adapters.NewHTTPAdapter(adapters.HTTPAdapterConfig{
		BaseURL: serverBadItemsJSON.URL + "/list",
		Client:  serverBadItemsJSON.Client(),
	})
	_, _, err = adBadItemsJSON.FetchList(context.Background())
	if err == nil || !strings.Contains(err.Error(), "failed to parse items response") {
		t.Fatalf("expected failed to parse items response error, got %v", err)
	}

	// 8. Network request failure
	closedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closedURL := closedServer.URL
	closedServer.Close()

	adClosed, _ := adapters.NewHTTPAdapter(adapters.HTTPAdapterConfig{
		BaseURL: closedURL,
	})
	_, _, err = adClosed.FetchList(context.Background())
	if err == nil || !strings.Contains(err.Error(), "http request failed") {
		t.Fatalf("expected http request failed error, got %v", err)
	}
}

func TestHTTPAdapter_DeriveListID(t *testing.T) {
	t.Parallel()

	// Case 1: Combined payload without ID, URL has /items suffix
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"items": []map[string]any{
				{"title": "First", "position": 0},
				{"title": "Second", "position": 0},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	ad, err := adapters.NewHTTPAdapter(adapters.HTTPAdapterConfig{
		BaseURL: server.URL + "/api/v1/groceries/items",
		Client:  server.Client(),
	})
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}

	list, items, err := ad.FetchList(context.Background())
	if err != nil {
		t.Fatalf("FetchList failed: %v", err)
	}

	if list.ID != "groceries" {
		t.Errorf("expected list ID 'groceries' derived from URL, got %q", list.ID)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[1].Position != 1 {
		t.Errorf("expected normalized position 1 for second item, got %d", items[1].Position)
	}

	// Case 2: URL with root path only
	adRoot, err := adapters.NewHTTPAdapter(adapters.HTTPAdapterConfig{
		BaseURL: server.URL + "/",
		Client:  server.Client(),
	})
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	listRoot, _, err := adRoot.FetchList(context.Background())
	if err != nil {
		t.Fatalf("FetchList failed: %v", err)
	}
	if listRoot.ID != "http-list" {
		t.Errorf("expected list ID 'http-list' for root URL, got %q", listRoot.ID)
	}

	// Case 3: URL with query parameters requesting secondary items endpoint
	serverWithQuery := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/list/items" && r.URL.Query().Get("account") == "main" {
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": "query-item-1", "title": "Query Task"},
			})
			return
		}
		if r.URL.Path == "/api/list" && r.URL.Query().Get("account") == "main" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":   "query-list",
				"name": "Query List",
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer serverWithQuery.Close()

	adQuery, err := adapters.NewHTTPAdapter(adapters.HTTPAdapterConfig{
		BaseURL: serverWithQuery.URL + "/api/list?account=main",
		Client:  serverWithQuery.Client(),
	})
	if err != nil {
		t.Fatalf("failed to create adapter with query: %v", err)
	}
	qList, qItems, err := adQuery.FetchList(context.Background())
	if err != nil {
		t.Fatalf("FetchList with query params failed: %v", err)
	}
	if qList.ID != "query-list" || len(qItems) != 1 || qItems[0].Title != "Query Task" {
		t.Errorf("unexpected list or items with query parameters: %+v, %+v", qList, qItems)
	}
}

func TestUpstreamHTTPError(t *testing.T) {
	t.Parallel()
	err := adapters.UpstreamHTTPError{
		Code: 503,
		URL:  "https://user:pass@example.com/api?token=secret",
	}
	if err.StatusCode() != 503 {
		t.Errorf("expected 503, got %d", err.StatusCode())
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "pass") {
		t.Errorf("expected sanitized URL, got %s", err.Error())
	}
}



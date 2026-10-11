package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/api"
	"github.com/azylman/mirrormere/internal/server"
	"github.com/azylman/mirrormere/internal/tasks"
	"github.com/azylman/mirrormere/internal/tasks/adapters"
)

type mockListStore struct {
	items       []tasks.ListItem
	err         error
	lastListID  string
	lastInclude bool
	calls       int
}

func (m *mockListStore) GetListItems(ctx context.Context, listID string, includeDone bool) ([]tasks.ListItem, error) {
	m.calls++
	m.lastListID = listID
	m.lastInclude = includeDone
	if m.err != nil {
		return nil, m.err
	}
	return m.items, nil
}

func TestDefaultListsHandler_Options(t *testing.T) {
	t.Parallel()

	h := server.NewDefaultListsHandler(&mockListStore{})
	req := httptest.NewRequest(http.MethodOptions, "/api/lists/groceries/items", nil)
	rec := httptest.NewRecorder()

	h.GetListItems(rec, req, "groceries")

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 No Content for OPTIONS, got %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("expected CORS origin '*', got %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
	if rec.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Errorf("expected CORS allow methods set")
	}
}

func TestDefaultListsHandler_MethodNotAllowed(t *testing.T) {
	t.Parallel()

	h := server.NewDefaultListsHandler(&mockListStore{})
	methods := []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch}

	for _, method := range methods {
		req := httptest.NewRequest(method, "/api/lists/groceries/items", nil)
		rec := httptest.NewRecorder()

		h.GetListItems(rec, req, "groceries")

		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected 405 for method %s, got %d", method, rec.Code)
		}
		if rec.Header().Get("Allow") != "GET, HEAD, OPTIONS" {
			t.Errorf("expected Allow header 'GET, HEAD, OPTIONS', got %q", rec.Header().Get("Allow"))
		}
		var errResp api.ErrorResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
			t.Fatalf("failed to parse error response: %v", err)
		}
		if errResp.Status != "error" {
			t.Errorf("expected status 'error', got %q", errResp.Status)
		}
	}
}

func TestDefaultListsHandler_NilStore(t *testing.T) {
	t.Parallel()

	h := server.NewDefaultListsHandler(nil)
	req := httptest.NewRequest(http.MethodGet, "/api/lists/groceries/items", nil)
	rec := httptest.NewRecorder()

	h.GetListItems(rec, req, "groceries")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for nil store, got %d", rec.Code)
	}
	var errResp api.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("failed to parse error response: %v", err)
	}
	if errResp.Status != "error" {
		t.Errorf("expected status 'error', got %q", errResp.Status)
	}
}

func TestDefaultListsHandler_InvalidIncludeDone(t *testing.T) {
	t.Parallel()

	h := server.NewDefaultListsHandler(&mockListStore{})
	req := httptest.NewRequest(http.MethodGet, "/api/lists/groceries/items?include_done=not-a-bool", nil)
	rec := httptest.NewRecorder()

	h.GetListItems(rec, req, "groceries")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid include_done, got %d", rec.Code)
	}
	var errResp api.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("failed to parse error response: %v", err)
	}
	if errResp.Status != "error" {
		t.Errorf("expected status 'error', got %q", errResp.Status)
	}
}

func TestDefaultListsHandler_ListNotFound(t *testing.T) {
	t.Parallel()

	mock := &mockListStore{err: tasks.ErrListNotFound}
	h := server.NewDefaultListsHandler(mock)
	req := httptest.NewRequest(http.MethodGet, "/api/lists/missing-list/items", nil)
	rec := httptest.NewRecorder()

	h.GetListItems(rec, req, "missing-list")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for ErrListNotFound, got %d", rec.Code)
	}
	var errResp api.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("failed to parse error response: %v", err)
	}
	if errResp.Status != "error" {
		t.Errorf("expected status 'error', got %q", errResp.Status)
	}
	if errResp.Error != "list 'missing-list' not found" {
		t.Errorf("expected error message %q, got %q", "list 'missing-list' not found", errResp.Error)
	}
}

func TestDefaultListsHandler_StoreError(t *testing.T) {
	t.Parallel()

	mock := &mockListStore{err: errors.New("db disk failure")}
	h := server.NewDefaultListsHandler(mock)
	req := httptest.NewRequest(http.MethodGet, "/api/lists/groceries/items", nil)
	rec := httptest.NewRecorder()

	h.GetListItems(rec, req, "groceries")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for db error, got %d", rec.Code)
	}
}

func TestDefaultListsHandler_Success(t *testing.T) {
	t.Parallel()

	assignee := "Alex"
	dueDate := "2026-09-26"
	now := time.Now().UTC().Truncate(time.Second)

	mockItems := []tasks.ListItem{
		{
			ID:        "item-1",
			ListID:    "groceries",
			Title:     "Oat Milk",
			Done:      false,
			Section:   "Dairy",
			Position:  0,
			Assignee:  &assignee,
			DueDate:   &dueDate,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "item-2",
			ListID:    "groceries",
			Title:     "Coffee Beans",
			Done:      true,
			Section:   "Pantry",
			Position:  1,
			Assignee:  nil,
			DueDate:   nil,
			CreatedAt: now,
			UpdatedAt: now,
		},
	}

	mock := &mockListStore{items: mockItems}
	h := server.NewDefaultListsHandler(mock)

	// Case 1: Default include_done (true)
	req := httptest.NewRequest(http.MethodGet, "/api/lists/groceries/items", nil)
	rec := httptest.NewRecorder()

	h.GetListItems(rec, req, "groceries")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !mock.lastInclude {
		t.Errorf("expected includeDone to default to true")
	}
	if mock.lastListID != "groceries" {
		t.Errorf("expected listID 'groceries', got %q", mock.lastListID)
	}

	var items []tasks.ListItem
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("failed to parse JSON response: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].Title != "Oat Milk" || items[0].Assignee == nil || *items[0].Assignee != "Alex" {
		t.Errorf("item 0 mismatch: %+v", items[0])
	}
	if items[1].Title != "Coffee Beans" || items[1].Assignee != nil {
		t.Errorf("item 1 mismatch: %+v", items[1])
	}

	// Case 2: Explicit include_done=false
	reqFalse := httptest.NewRequest(http.MethodGet, "/api/lists/groceries/items?include_done=false", nil)
	recFalse := httptest.NewRecorder()

	h.GetListItems(recFalse, reqFalse, "groceries")

	if recFalse.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recFalse.Code)
	}
	if mock.lastInclude != false {
		t.Errorf("expected includeDone to be false")
	}

	// Case 3: Empty items returns empty array [] not null
	mockEmpty := &mockListStore{items: nil}
	hEmpty := server.NewDefaultListsHandler(mockEmpty)
	reqEmpty := httptest.NewRequest(http.MethodGet, "/api/lists/groceries/items", nil)
	recEmpty := httptest.NewRecorder()

	hEmpty.GetListItems(recEmpty, reqEmpty, "groceries")

	if recEmpty.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recEmpty.Code)
	}
	if recEmpty.Body.String() != "[]\n" {
		t.Errorf("expected empty array '[]\\n', got %q", recEmpty.Body.String())
	}

	// Case 4: HEAD request returns 200 with empty body
	reqHead := httptest.NewRequest(http.MethodHead, "/api/lists/groceries/items", nil)
	recHead := httptest.NewRecorder()

	h.GetListItems(recHead, reqHead, "groceries")

	if recHead.Code != http.StatusOK {
		t.Fatalf("expected 200 for HEAD, got %d", recHead.Code)
	}
	if recHead.Body.Len() != 0 {
		t.Errorf("expected empty body for HEAD, got %d bytes", recHead.Body.Len())
	}
}

type mockItemCompleter struct {
	calls      int
	lastListID string
	lastItemID string
	err        error
}

func (m *mockItemCompleter) CompleteListItem(ctx context.Context, listID, itemID string) error {
	m.calls++
	m.lastListID = listID
	m.lastItemID = itemID
	return m.err
}

type mockStoreWithComplete struct {
	mockListStore
	completeCalls int
	lastItemID    string
	completeErr   error
}

func (m *mockStoreWithComplete) CompleteItem(ctx context.Context, listID, itemID string) error {
	m.completeCalls++
	m.lastItemID = itemID
	return m.completeErr
}

func TestDefaultListsHandler_CompleteListItem(t *testing.T) {
	t.Parallel()

	// 1. OPTIONS preflight
	t.Run("OPTIONS returns 204 with CORS", func(t *testing.T) {
		t.Parallel()
		h := server.NewDefaultListsHandler(&mockListStore{})
		req := httptest.NewRequest(http.MethodOptions, "/api/lists/groceries/items/i1/complete", nil)
		rec := httptest.NewRecorder()

		h.CompleteListItem(rec, req, "groceries", "i1")

		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected 204, got %d", rec.Code)
		}
		if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Errorf("expected CORS origin '*', got %q", rec.Header().Get("Access-Control-Allow-Origin"))
		}
	})

	// 2. Method Not Allowed
	t.Run("Method Not Allowed for GET, PUT, DELETE", func(t *testing.T) {
		t.Parallel()
		h := server.NewDefaultListsHandler(&mockListStore{})
		for _, m := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
			req := httptest.NewRequest(m, "/api/lists/groceries/items/i1/complete", nil)
			rec := httptest.NewRecorder()

			h.CompleteListItem(rec, req, "groceries", "i1")

			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("expected 405 for %s, got %d", m, rec.Code)
			}
		}
	})

	// 3. Validation errors
	t.Run("Validation errors on empty or path traversal IDs", func(t *testing.T) {
		t.Parallel()
		h := server.NewDefaultListsHandler(&mockListStore{})

		badPairs := [][2]string{
			{"", "item1"},
			{"list1", ""},
			{"../list", "item1"},
			{"list1", "../item"},
			{"list/sub", "item1"},
			{"list1", "item/sub"},
		}

		for _, pair := range badPairs {
			req := httptest.NewRequest(http.MethodPost, "/api/lists/x/items/y/complete", nil)
			rec := httptest.NewRecorder()

			h.CompleteListItem(rec, req, pair[0], pair[1])

			if rec.Code != http.StatusBadRequest {
				t.Errorf("expected 400 for pair %v, got %d", pair, rec.Code)
			}
		}
	})

	// 4. Success via ItemCompleter
	t.Run("Success via ItemCompleter", func(t *testing.T) {
		t.Parallel()
		completer := &mockItemCompleter{}
		h := server.NewDefaultListsHandler(&mockListStore{}, server.WithItemCompleter(completer))

		req := httptest.NewRequest(http.MethodPost, "/api/lists/groceries/items/item-1/complete", nil)
		rec := httptest.NewRecorder()

		h.CompleteListItem(rec, req, "groceries", "item-1")

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		if completer.calls != 1 || completer.lastListID != "groceries" || completer.lastItemID != "item-1" {
			t.Errorf("completer not called properly: %+v", completer)
		}
		var resp api.ActionResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if resp.Status != "ok" {
			t.Errorf("expected status ok, got %q", resp.Status)
		}
	})

	// 5. Error mappings via ItemCompleter
	t.Run("Error mappings: 404, 401, 502, 500", func(t *testing.T) {
		t.Parallel()

		errCases := []struct {
			name     string
			err      error
			wantCode int
		}{
			{"list not found", tasks.ErrListNotFound, http.StatusNotFound},
			{"item not found", tasks.ErrItemNotFound, http.StatusNotFound},
			{"upstream not found", adapters.ErrUpstreamNotFound, http.StatusNotFound},
			{"unauthorized", adapters.ErrUnauthorized, http.StatusUnauthorized},
			{"upstream 429", adapters.UpstreamHTTPError{Code: 429, URL: "http://api"}, http.StatusBadGateway},
			{"upstream 500", adapters.UpstreamHTTPError{Code: 500, URL: "http://api"}, http.StatusBadGateway},
			{"internal error", errors.New("db crash"), http.StatusInternalServerError},
		}

		for _, tc := range errCases {
			completer := &mockItemCompleter{err: tc.err}
			h := server.NewDefaultListsHandler(&mockListStore{}, server.WithItemCompleter(completer))

			req := httptest.NewRequest(http.MethodPost, "/api/lists/groceries/items/item-1/complete", nil)
			rec := httptest.NewRecorder()

			h.CompleteListItem(rec, req, "groceries", "item-1")

			if rec.Code != tc.wantCode {
				t.Errorf("%s: expected code %d, got %d (body: %s)", tc.name, tc.wantCode, rec.Code, rec.Body.String())
			}
		}
	})

	// 6. Fallback to StoreCompleter and SetItemCompleter dynamic update
	t.Run("Fallback to StoreCompleter and SetItemCompleter", func(t *testing.T) {
		t.Parallel()
		store := &mockStoreWithComplete{}
		notifier := tasks.NewChangeNotifier()
		ch := make(chan struct{}, 1)
		unsub := notifier.RegisterListener("groceries", ch)
		defer unsub()

		h := server.NewDefaultListsHandler(store, server.WithChangeNotifier(notifier))

		req := httptest.NewRequest(http.MethodPost, "/api/lists/groceries/items/item-1/complete", nil)
		rec := httptest.NewRecorder()

		h.CompleteListItem(rec, req, "groceries", "item-1")

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		if store.completeCalls != 1 || store.lastItemID != "item-1" {
			t.Errorf("store not called properly: %+v", store)
		}
		select {
		case <-ch:
			// Notified
		default:
			t.Error("expected change notification on store fallback")
		}

		// Dynamically set completer
		completer := &mockItemCompleter{}
		h.SetItemCompleter(completer)

		rec2 := httptest.NewRecorder()
		h.CompleteListItem(rec2, req, "groceries", "item-2")
		if rec2.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec2.Code)
		}
		if completer.calls != 1 || completer.lastItemID != "item-2" {
			t.Errorf("dynamically set completer not called: %+v", completer)
		}
	})

	// 7. Nil completer and store without StoreCompleter returns 500
	t.Run("Store without StoreCompleter returns 500", func(t *testing.T) {
		t.Parallel()
		h := server.NewDefaultListsHandler(&mockListStore{})
		req := httptest.NewRequest(http.MethodPost, "/api/lists/groceries/items/item-1/complete", nil)
		rec := httptest.NewRecorder()

		h.CompleteListItem(rec, req, "groceries", "item-1")

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d", rec.Code)
		}
	})
}

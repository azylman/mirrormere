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

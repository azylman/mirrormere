package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azylman/mirrormere/internal/server"
	"github.com/azylman/mirrormere/internal/tasks"
	"github.com/azylman/mirrormere/internal/tasks/adapters"
)

type mockListStore struct {
	items       []tasks.ListItem
	err         error
	lastListID  string
	lastInclude bool
}

func (m *mockListStore) GetListItems(ctx context.Context, listID string, includeDone bool) ([]tasks.ListItem, error) {
	m.lastListID, m.lastInclude = listID, includeDone
	return m.items, m.err
}

type mockItemCompleter struct {
	lastListID, lastItemID string
	err                    error
}

func (m *mockItemCompleter) CompleteListItem(ctx context.Context, listID, itemID string) error {
	m.lastListID, m.lastItemID = listID, itemID
	return m.err
}

type mockStoreWithComplete struct {
	mockListStore
	lastItemID  string
	completeErr error
}

func (m *mockStoreWithComplete) CompleteItem(ctx context.Context, listID, itemID string) error {
	m.lastItemID = itemID
	return m.completeErr
}

func TestDefaultListsHandler_GetListItems(t *testing.T) {
	t.Parallel()
	alex := "Alex"
	sampleItems := []tasks.ListItem{
		{ID: "i1", ListID: "groceries", Title: "Oat Milk", Assignee: &alex},
		{ID: "i2", ListID: "groceries", Title: "Coffee Beans"},
	}
	tests := []struct {
		name, method, query, listID string
		nilStore                    bool
		items                       []tasks.ListItem
		storeErr                    error
		wantCode, wantCount         int
		wantInclude                 bool
		wantErrSub                  string
	}{
		{"default_include", http.MethodGet, "", "groceries", false, sampleItems, nil, http.StatusOK, 2, true, ""},
		{"explicit_false", http.MethodGet, "?include_done=false", "groceries", false, sampleItems, nil, http.StatusOK, 2, false, ""},
		{"empty_items", http.MethodGet, "", "groceries", false, nil, nil, http.StatusOK, 0, true, ""},
		{"head_ok", http.MethodHead, "", "groceries", false, sampleItems, nil, http.StatusOK, 0, true, ""},
		{"options_cors", http.MethodOptions, "", "groceries", false, nil, nil, http.StatusNoContent, 0, false, ""},
		{"post_not_allowed", http.MethodPost, "", "groceries", false, nil, nil, http.StatusMethodNotAllowed, 0, false, "method not allowed"},
		{"nil_store", http.MethodGet, "", "groceries", true, nil, nil, http.StatusInternalServerError, 0, false, "tasks store not configured"},
		{"bad_include_done", http.MethodGet, "?include_done=bad", "groceries", false, nil, nil, http.StatusBadRequest, 0, false, "invalid include_done"},
		{"list_not_found", http.MethodGet, "", "missing", false, nil, tasks.ErrListNotFound, http.StatusNotFound, 0, true, "not found"},
		{"store_error", http.MethodGet, "", "groceries", false, nil, errors.New("db crash"), http.StatusInternalServerError, 0, true, "failed to query"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := &mockListStore{items: tc.items, err: tc.storeErr}
			var s server.ListItemsStore = store
			if tc.nilStore {
				s = nil
			}
			rec := httptest.NewRecorder()
			server.NewDefaultListsHandler(s).GetListItems(rec, httptest.NewRequest(tc.method, "/api/lists/"+tc.listID+"/items"+tc.query, nil), tc.listID)
			if rec.Code != tc.wantCode {
				t.Fatalf("code = %d, want %d", rec.Code, tc.wantCode)
			}
			if tc.wantCode == http.StatusOK && tc.method != http.MethodHead {
				var items []tasks.ListItem
				if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil || len(items) != tc.wantCount {
					t.Fatalf("unexpected items: %v, count: %d", err, len(items))
				}
				if store.lastInclude != tc.wantInclude {
					t.Fatalf("includeDone = %v, want %v", store.lastInclude, tc.wantInclude)
				}
			}
			if tc.wantErrSub != "" && !strings.Contains(rec.Body.String(), tc.wantErrSub) {
				t.Fatalf("expected error containing %q, got %s", tc.wantErrSub, rec.Body.String())
			}
		})
	}
}

func TestDefaultListsHandler_CompleteListItem(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, method, listID, itemID string
		useCompleter, useStore, swap bool
		compErr                      error
		wantCode                     int
		wantNotified                 bool
	}{
		{"options_cors", http.MethodOptions, "groceries", "i1", false, false, false, nil, http.StatusNoContent, false},
		{"method_not_allowed", http.MethodGet, "groceries", "i1", false, false, false, nil, http.StatusMethodNotAllowed, false},
		{"bad_list_empty", http.MethodPost, "", "i1", false, false, false, nil, http.StatusBadRequest, false},
		{"bad_list_traversal", http.MethodPost, "../list", "i1", false, false, false, nil, http.StatusBadRequest, false},
		{"bad_item_empty", http.MethodPost, "groceries", "", false, false, false, nil, http.StatusBadRequest, false},
		{"bad_item_traversal", http.MethodPost, "groceries", "../item", false, false, false, nil, http.StatusBadRequest, false},
		{"success_completer", http.MethodPost, "groceries", "i1", true, false, false, nil, http.StatusOK, false},
		{"err_list_not_found", http.MethodPost, "groceries", "i1", true, false, false, tasks.ErrListNotFound, http.StatusNotFound, false},
		{"err_item_not_found", http.MethodPost, "groceries", "i1", true, false, false, tasks.ErrItemNotFound, http.StatusNotFound, false},
		{"err_upstream_not_found", http.MethodPost, "groceries", "i1", true, false, false, adapters.ErrUpstreamNotFound, http.StatusNotFound, false},
		{"err_unauthorized", http.MethodPost, "groceries", "i1", true, false, false, adapters.ErrUnauthorized, http.StatusUnauthorized, false},
		{"err_upstream_500", http.MethodPost, "groceries", "i1", true, false, false, adapters.UpstreamHTTPError{Code: 500}, http.StatusBadGateway, false},
		{"err_internal", http.MethodPost, "groceries", "i1", true, false, false, errors.New("db error"), http.StatusInternalServerError, false},
		{"store_completer_fallback", http.MethodPost, "groceries", "i1", false, true, false, nil, http.StatusOK, true},
		{"dynamic_swap_completer", http.MethodPost, "groceries", "i1", false, true, true, nil, http.StatusOK, false},
		{"no_completer_or_store", http.MethodPost, "groceries", "i1", false, false, false, nil, http.StatusInternalServerError, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var comp *mockItemCompleter
			if tc.useCompleter {
				comp = &mockItemCompleter{err: tc.compErr}
			}
			notifier := tasks.NewChangeNotifier()
			ch := make(chan struct{}, 1)
			unsub := notifier.RegisterListener("groceries", ch)
			defer unsub()

			var store server.ListItemsStore = &mockListStore{}
			if tc.useStore {
				store = &mockStoreWithComplete{completeErr: tc.compErr}
			}
			opts := []server.ListsHandlerOption{server.WithChangeNotifier(notifier)}
			if comp != nil {
				opts = append(opts, server.WithItemCompleter(comp))
			}
			h := server.NewDefaultListsHandler(store, opts...)
			if tc.swap {
				comp = &mockItemCompleter{err: tc.compErr}
				h.SetItemCompleter(comp)
			}
			rec := httptest.NewRecorder()
			h.CompleteListItem(rec, httptest.NewRequest(tc.method, "/api/lists/"+tc.listID+"/items/"+tc.itemID+"/complete", nil), tc.listID, tc.itemID)
			if rec.Code != tc.wantCode {
				t.Fatalf("code = %d, want %d", rec.Code, tc.wantCode)
			}
			if tc.wantNotified {
				select {
				case <-ch:
				default:
					t.Fatalf("expected change notification")
				}
			}
		})
	}
}

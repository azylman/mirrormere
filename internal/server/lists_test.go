package server_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azylman/mirrormere/internal/server"
	"github.com/azylman/mirrormere/internal/tasks"
	"github.com/azylman/mirrormere/internal/tasks/adapters"
)

type mockItemCompleter struct {
	err error
}

func (m *mockItemCompleter) CompleteListItem(_ context.Context, _, _ string) error {
	return m.err
}

func TestDefaultListsHandler_CompleteListItem(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		method, listID string
		itemID         string
		useCompleter   bool
		swap           bool
		compErr        error
		wantCode       int
	}{
		{"options", http.MethodOptions, "groceries", "i1", false, false, nil, http.StatusNoContent},
		{"method_not_allowed", http.MethodGet, "groceries", "i1", false, false, nil, http.StatusMethodNotAllowed},
		{"bad_list_empty", http.MethodPost, "", "i1", false, false, nil, http.StatusBadRequest},
		{"bad_list_traversal", http.MethodPost, "../list", "i1", false, false, nil, http.StatusBadRequest},
		{"bad_item_empty", http.MethodPost, "groceries", "", false, false, nil, http.StatusBadRequest},
		{"bad_item_traversal", http.MethodPost, "groceries", "../item", false, false, nil, http.StatusBadRequest},
		{"success_completer", http.MethodPost, "groceries", "i1", true, false, nil, http.StatusOK},
		{"err_list_not_found", http.MethodPost, "groceries", "i1", true, false, tasks.ErrListNotFound, http.StatusNotFound},
		{"err_item_not_found", http.MethodPost, "groceries", "i1", true, false, tasks.ErrItemNotFound, http.StatusNotFound},
		{"err_upstream_not_found", http.MethodPost, "groceries", "i1", true, false, adapters.ErrUpstreamNotFound, http.StatusNotFound},
		{"err_unauthorized", http.MethodPost, "groceries", "i1", true, false, adapters.ErrUnauthorized, http.StatusUnauthorized},
		{"err_upstream_500", http.MethodPost, "groceries", "i1", true, false, adapters.UpstreamHTTPError{Code: 500}, http.StatusBadGateway},
		{"err_internal", http.MethodPost, "groceries", "i1", true, false, errors.New("upstream error"), http.StatusInternalServerError},
		{"dynamic_swap_completer", http.MethodPost, "groceries", "i1", false, true, nil, http.StatusOK},
		{"no_completer", http.MethodPost, "groceries", "i1", false, false, nil, http.StatusInternalServerError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var opts []server.ListsHandlerOption
			if tc.useCompleter {
				opts = append(opts, server.WithItemCompleter(&mockItemCompleter{err: tc.compErr}))
			}
			h := server.NewDefaultListsHandler(opts...)
			if tc.swap {
				h.SetItemCompleter(&mockItemCompleter{err: tc.compErr})
			}
			rec := httptest.NewRecorder()
			h.CompleteListItem(rec, httptest.NewRequest(tc.method, "/api/lists/"+tc.listID+"/items/"+tc.itemID+"/complete", nil), tc.listID, tc.itemID)
			if rec.Code != tc.wantCode {
				t.Fatalf("code = %d, want %d", rec.Code, tc.wantCode)
			}
		})
	}
}

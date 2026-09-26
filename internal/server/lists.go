package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/azylman/mirrormere/internal/api"
	"github.com/azylman/mirrormere/internal/tasks"
)

// ListItemsStore abstracts storage access needed by DefaultListsHandler.
type ListItemsStore interface {
	GetListItems(ctx context.Context, listID string, includeDone bool) ([]tasks.ListItem, error)
}

// DefaultListsHandler serves the OpenAPI GET /api/lists/{list_id}/items endpoint.
type DefaultListsHandler struct {
	store ListItemsStore
}

// NewDefaultListsHandler constructs a DefaultListsHandler backed by store.
func NewDefaultListsHandler(store ListItemsStore) *DefaultListsHandler {
	return &DefaultListsHandler{store: store}
}

// GetListItems handles GET /api/lists/{list_id}/items.
func (h *DefaultListsHandler) GetListItems(w http.ResponseWriter, r *http.Request, listID string) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD, OPTIONS")
		writeListsError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if h.store == nil {
		writeListsError(w, http.StatusInternalServerError, "tasks store not configured")
		return
	}

	includeDone := true
	if raw := r.URL.Query().Get("include_done"); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			writeListsError(w, http.StatusBadRequest, fmt.Sprintf("invalid include_done query parameter: %q", raw))
			return
		}
		includeDone = parsed
	}

	items, err := h.store.GetListItems(r.Context(), listID, includeDone)
	if err != nil {
		if errors.Is(err, tasks.ErrListNotFound) {
			writeListsError(w, http.StatusNotFound, fmt.Sprintf("list '%s' not found", listID))
			return
		}
		writeListsError(w, http.StatusInternalServerError, "failed to query list items: "+err.Error())
		return
	}

	if items == nil {
		items = []tasks.ListItem{}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}

	if err := json.NewEncoder(w).Encode(items); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func writeListsError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(api.ErrorResponse{
		Status: "error",
		Error:  msg,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

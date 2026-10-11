package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/azylman/mirrormere/internal/api"
	"github.com/azylman/mirrormere/internal/tasks"
	"github.com/azylman/mirrormere/internal/tasks/adapters"
)

// ListItemsStore abstracts storage access needed by DefaultListsHandler.
type ListItemsStore interface {
	GetListItems(ctx context.Context, listID string, includeDone bool) ([]tasks.ListItem, error)
}

// ItemCompleter abstracts item completion logic across stores and providers.
type ItemCompleter interface {
	CompleteListItem(ctx context.Context, listID, itemID string) error
}

// StoreCompleter allows a ListItemsStore that directly implements CompleteItem to be used.
type StoreCompleter interface {
	CompleteItem(ctx context.Context, listID, itemID string) error
}

// DefaultListsHandler serves the OpenAPI GET /api/lists/{list_id}/items and POST /api/lists/{list_id}/items/{item_id}/complete endpoints.
type DefaultListsHandler struct {
	mu        sync.RWMutex
	store     ListItemsStore
	completer ItemCompleter
	notifier  *tasks.ChangeNotifier
}

// ListsHandlerOption allows configuring optional dependencies for DefaultListsHandler.
type ListsHandlerOption func(*DefaultListsHandler)

// WithItemCompleter injects an external ItemCompleter (e.g. ProviderCoordinator).
func WithItemCompleter(c ItemCompleter) ListsHandlerOption {
	return func(h *DefaultListsHandler) {
		h.completer = c
	}
}

// WithChangeNotifier injects a custom ChangeNotifier for local fallback updates.
func WithChangeNotifier(n *tasks.ChangeNotifier) ListsHandlerOption {
	return func(h *DefaultListsHandler) {
		h.notifier = n
	}
}

// NewDefaultListsHandler constructs a DefaultListsHandler backed by store and optional options.
func NewDefaultListsHandler(store ListItemsStore, opts ...ListsHandlerOption) *DefaultListsHandler {
	h := &DefaultListsHandler{
		store:    store,
		notifier: tasks.DefaultChangeNotifier(),
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// SetItemCompleter dynamically updates the ItemCompleter (e.g. after ProviderCoordinator is initialized).
func (h *DefaultListsHandler) SetItemCompleter(c ItemCompleter) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.completer = c
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

	h.mu.RLock()
	store := h.store
	h.mu.RUnlock()

	if store == nil {
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

	items, err := store.GetListItems(r.Context(), listID, includeDone)
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

// CompleteListItem handles POST /api/lists/{list_id}/items/{item_id}/complete.
func (h *DefaultListsHandler) CompleteListItem(w http.ResponseWriter, r *http.Request, listID, itemID string) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, OPTIONS")
		writeListsError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	listID = strings.TrimSpace(listID)
	itemID = strings.TrimSpace(itemID)
	if listID == "" || strings.Contains(listID, "..") || strings.Contains(listID, "/") {
		writeListsError(w, http.StatusBadRequest, "invalid list_id")
		return
	}
	if itemID == "" || strings.Contains(itemID, "..") || strings.Contains(itemID, "/") {
		writeListsError(w, http.StatusBadRequest, "invalid item_id")
		return
	}

	h.mu.RLock()
	completer := h.completer
	store := h.store
	notifier := h.notifier
	h.mu.RUnlock()

	var completeErr error
	if completer != nil {
		completeErr = completer.CompleteListItem(r.Context(), listID, itemID)
	} else if sc, ok := store.(StoreCompleter); ok {
		completeErr = sc.CompleteItem(r.Context(), listID, itemID)
		if completeErr == nil && notifier != nil {
			notifier.NotifyChange(listID)
		}
	} else {
		writeListsError(w, http.StatusInternalServerError, "task completion not configured")
		return
	}

	if completeErr != nil {
		if errors.Is(completeErr, tasks.ErrListNotFound) {
			writeListsError(w, http.StatusNotFound, fmt.Sprintf("list '%s' not found", listID))
			return
		}
		if errors.Is(completeErr, tasks.ErrItemNotFound) || errors.Is(completeErr, adapters.ErrUpstreamNotFound) {
			writeListsError(w, http.StatusNotFound, fmt.Sprintf("item '%s' not found in list '%s'", itemID, listID))
			return
		}
		if errors.Is(completeErr, adapters.ErrUnauthorized) {
			writeListsError(w, http.StatusUnauthorized, "upstream provider unauthorized")
			return
		}
		var upstreamErr adapters.UpstreamHTTPError
		if errors.As(completeErr, &upstreamErr) {
			writeListsError(w, http.StatusBadGateway, fmt.Sprintf("upstream provider error (status %d)", upstreamErr.Code))
			return
		}
		writeListsError(w, http.StatusInternalServerError, "failed to complete item: "+completeErr.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(api.ActionResponse{
		Status: "ok",
	}); err != nil {
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

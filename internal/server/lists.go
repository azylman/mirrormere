package server

import (
	"context"
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

// DefaultListsHandler serves OpenAPI GET /api/lists/{list_id}/items and POST .../complete.
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
	return func(h *DefaultListsHandler) { h.completer = c }
}

// WithChangeNotifier injects a custom ChangeNotifier for local fallback updates.
func WithChangeNotifier(n *tasks.ChangeNotifier) ListsHandlerOption {
	return func(h *DefaultListsHandler) { h.notifier = n }
}

// NewDefaultListsHandler constructs a DefaultListsHandler backed by store and optional options.
func NewDefaultListsHandler(store ListItemsStore, opts ...ListsHandlerOption) *DefaultListsHandler {
	h := &DefaultListsHandler{store: store, notifier: tasks.DefaultChangeNotifier()}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// SetItemCompleter dynamically updates the ItemCompleter.
func (h *DefaultListsHandler) SetItemCompleter(c ItemCompleter) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.completer = c
}

// GetListItems handles GET /api/lists/{list_id}/items.
func (h *DefaultListsHandler) GetListItems(w http.ResponseWriter, r *http.Request, listID string) {
	if !requireCORSAndMethod(w, r, "GET, HEAD, OPTIONS", http.MethodGet, http.MethodHead) {
		return
	}
	h.mu.RLock()
	store := h.store
	h.mu.RUnlock()
	if store == nil {
		writeAPIError(w, http.StatusInternalServerError, "tasks store not configured")
		return
	}
	includeDone := true
	if raw := r.URL.Query().Get("include_done"); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, fmt.Sprintf("invalid include_done query parameter: %q", raw))
			return
		}
		includeDone = parsed
	}
	items, err := store.GetListItems(r.Context(), listID, includeDone)
	if err != nil {
		if errors.Is(err, tasks.ErrListNotFound) {
			writeAPIError(w, http.StatusNotFound, fmt.Sprintf("list '%s' not found", listID))
			return
		}
		writeAPIError(w, http.StatusInternalServerError, "failed to query list items: "+err.Error())
		return
	}
	if items == nil {
		items = []tasks.ListItem{}
	}
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

// CompleteListItem handles POST /api/lists/{list_id}/items/{item_id}/complete.
func (h *DefaultListsHandler) CompleteListItem(w http.ResponseWriter, r *http.Request, listID, itemID string) {
	if !requireCORSAndMethod(w, r, "POST, OPTIONS", http.MethodPost) {
		return
	}
	listID, itemID = strings.TrimSpace(listID), strings.TrimSpace(itemID)
	if listID == "" || strings.Contains(listID, "..") || strings.Contains(listID, "/") {
		writeAPIError(w, http.StatusBadRequest, "invalid list_id")
		return
	}
	if itemID == "" || strings.Contains(itemID, "..") || strings.Contains(itemID, "/") {
		writeAPIError(w, http.StatusBadRequest, "invalid item_id")
		return
	}

	h.mu.RLock()
	completer, store, notifier := h.completer, h.store, h.notifier
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
		writeAPIError(w, http.StatusInternalServerError, "task completion not configured")
		return
	}

	if completeErr != nil {
		switch {
		case errors.Is(completeErr, tasks.ErrListNotFound):
			writeAPIError(w, http.StatusNotFound, fmt.Sprintf("list '%s' not found", listID))
		case errors.Is(completeErr, tasks.ErrItemNotFound), errors.Is(completeErr, adapters.ErrUpstreamNotFound):
			writeAPIError(w, http.StatusNotFound, fmt.Sprintf("item '%s' not found in list '%s'", itemID, listID))
		case errors.Is(completeErr, adapters.ErrUnauthorized):
			writeAPIError(w, http.StatusUnauthorized, "upstream provider unauthorized")
		default:
			var upstreamErr adapters.UpstreamHTTPError
			if errors.As(completeErr, &upstreamErr) {
				writeAPIError(w, http.StatusBadGateway, fmt.Sprintf("upstream provider error (status %d)", upstreamErr.Code))
			} else {
				writeAPIError(w, http.StatusInternalServerError, "failed to complete item: "+completeErr.Error())
			}
		}
		return
	}
	writeJSON(w, http.StatusOK, api.ActionResponse{Status: "ok"})
}

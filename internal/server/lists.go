package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/azylman/mirrormere/internal/api"
	"github.com/azylman/mirrormere/internal/tasks"
	"github.com/azylman/mirrormere/internal/tasks/adapters"
)

// ItemCompleter abstracts item completion logic across upstream task providers.
type ItemCompleter interface {
	CompleteListItem(ctx context.Context, listID, itemID string) error
}

// DefaultListsHandler serves OpenAPI POST /api/lists/{list_id}/items/{item_id}/complete.
type DefaultListsHandler struct {
	mu        sync.RWMutex
	completer ItemCompleter
}

// ListsHandlerOption allows configuring optional dependencies for DefaultListsHandler.
type ListsHandlerOption func(*DefaultListsHandler)

// WithItemCompleter injects an external ItemCompleter (e.g. ProviderCoordinator).
func WithItemCompleter(c ItemCompleter) ListsHandlerOption {
	return func(h *DefaultListsHandler) { h.completer = c }
}

// NewDefaultListsHandler constructs a DefaultListsHandler with optional options.
func NewDefaultListsHandler(opts ...ListsHandlerOption) *DefaultListsHandler {
	h := &DefaultListsHandler{}
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
	completer := h.completer
	h.mu.RUnlock()

	if completer == nil {
		writeAPIError(w, http.StatusInternalServerError, "task completion not configured")
		return
	}

	if completeErr := completer.CompleteListItem(r.Context(), listID, itemID); completeErr != nil {
		switch {
		case errors.Is(completeErr, tasks.ErrListNotFound):
			writeAPIError(w, http.StatusNotFound, fmt.Sprintf("list '%s' not found", listID))
		case errors.Is(completeErr, tasks.ErrItemNotFound), errors.Is(completeErr, adapters.ErrUpstreamNotFound):
			writeAPIError(w, http.StatusNotFound, fmt.Sprintf("item '%s' not found in list '%s'", itemID, listID))
		case errors.Is(completeErr, tasks.ErrCompletionUnsupported):
			writeAPIError(w, http.StatusNotImplemented, "task completion not supported by provider")
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

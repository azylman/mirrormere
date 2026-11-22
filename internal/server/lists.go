package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/azylman/mirrormere/internal/api"
	"github.com/azylman/mirrormere/internal/tasks"
	"github.com/azylman/mirrormere/internal/tasks/adapters"
)

// ItemCompleter abstracts item completion logic across upstream task providers.
type ItemCompleter interface {
	CompleteListItem(ctx context.Context, listID, itemID string) error
}

// DefaultListsHandler serves POST /api/lists/{list_id}/items/{item_id}/complete.
type DefaultListsHandler struct {
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

// CompleteListItem handles POST /api/lists/{list_id}/items/{item_id}/complete.
func (h *DefaultListsHandler) CompleteListItem(w http.ResponseWriter, r *http.Request, listID, itemID string) {
	if !api.AllowMethods(w, r, http.MethodPost) {
		return
	}
	listID, itemID = strings.TrimSpace(listID), strings.TrimSpace(itemID)
	if listID == "" || strings.Contains(listID, "..") || strings.Contains(listID, "/") {
		api.WriteError(w, http.StatusBadRequest, "invalid list_id")
		return
	}
	if itemID == "" || strings.Contains(itemID, "..") || strings.Contains(itemID, "/") {
		api.WriteError(w, http.StatusBadRequest, "invalid item_id")
		return
	}

	completer := h.completer
	if completer == nil {
		api.WriteError(w, http.StatusInternalServerError, "task completion not configured")
		return
	}

	if completeErr := completer.CompleteListItem(r.Context(), listID, itemID); completeErr != nil {
		switch {
		case errors.Is(completeErr, tasks.ErrListNotFound):
			api.WriteError(w, http.StatusNotFound, fmt.Sprintf("list '%s' not found", listID))
		case errors.Is(completeErr, tasks.ErrItemNotFound), errors.Is(completeErr, adapters.ErrUpstreamNotFound):
			api.WriteError(w, http.StatusNotFound, fmt.Sprintf("item '%s' not found in list '%s'", itemID, listID))
		case errors.Is(completeErr, tasks.ErrCompletionUnsupported):
			api.WriteError(w, http.StatusNotImplemented, "task completion not supported by provider")
		case errors.Is(completeErr, adapters.ErrUnauthorized):
			api.WriteError(w, http.StatusUnauthorized, "upstream provider unauthorized")
		default:
			if upstreamErr, ok := errors.AsType[api.UpstreamHTTPError](completeErr); ok {
				api.WriteError(w, http.StatusBadGateway, fmt.Sprintf("upstream provider error (status %d)", upstreamErr.Code))
			} else {
				api.WriteError(w, http.StatusInternalServerError, "failed to complete item: "+completeErr.Error())
			}
		}
		return
	}
	api.WriteJSON(w, http.StatusOK, api.ActionResponse{Status: "ok"})
}

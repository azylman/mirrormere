package server

import (
	"net/http"
)

// Routes returns the configured http.Handler for in-memory testing.
func (s *Server) Routes() http.Handler { return s.mux }

// SetItemCompleter dynamically updates the ItemCompleter.
func (h *DefaultListsHandler) SetItemCompleter(c ItemCompleter) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.completer = c
}

// Ready returns a channel closed as soon as the server listener is bound.
func (s *Server) Ready() <-chan struct{} { return s.ready }

// Addr returns the network address the server is actively listening on.
func (s *Server) Addr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.addr
}

// SetWriteCountersDirForTest overrides writeCountersDirFn for testing purposes.
func SetWriteCountersDirForTest(fn func(string) error) func() {
	writeCountersDirMu.Lock()
	prev := writeCountersDirFn
	writeCountersDirFn = fn
	writeCountersDirMu.Unlock()
	return func() {
		writeCountersDirMu.Lock()
		writeCountersDirFn = prev
		writeCountersDirMu.Unlock()
	}
}

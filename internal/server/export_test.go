package server

import "time"

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

// SetNowFnForTest overrides the time provider for testing debounce without sleeps.
func SetNowFnForTest(fn func() time.Time) func() {
	flushMu.Lock()
	prev := nowFn
	nowFn = fn
	flushMu.Unlock()
	return func() {
		flushMu.Lock()
		nowFn = prev
		flushMu.Unlock()
	}
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

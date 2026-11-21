// Package runutil holds small process-lifecycle helpers shared by mains.
package runutil

import (
	"errors"
	"log/slog"
	"net"
)

// CloseListener closes l, ignoring net.ErrClosed and logging other errors.
// A nil listener is a no-op; a nil logger uses slog.Default().
func CloseListener(l net.Listener, logger *slog.Logger) {
	if l == nil {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}
	if err := l.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		logger.Warn("failed to close listener", "error", err)
	}
}

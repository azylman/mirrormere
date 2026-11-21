package runutil

import (
	"bytes"
	"errors"
	"log/slog"
	"net"
	"strings"
	"testing"
)

type errListener struct {
	net.Listener

	err error
}

func (l *errListener) Close() error { return l.err }

func TestCloseListener(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		l       net.Listener
		wantLog string
	}{
		{name: "nil listener", l: nil},
		{name: "already closed ignored", l: &errListener{err: net.ErrClosed}},
		{name: "other error logged", l: &errListener{err: errors.New("boom")}, wantLog: "boom"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			CloseListener(tc.l, slog.New(slog.NewTextHandler(&buf, nil)))
			if tc.wantLog == "" && buf.Len() != 0 {
				t.Fatalf("unexpected log %q", buf.String())
			}
			if !strings.Contains(buf.String(), tc.wantLog) {
				t.Fatalf("log %q missing %q", buf.String(), tc.wantLog)
			}
		})
	}
}

func TestCloseListenerNilLogger(t *testing.T) {
	t.Parallel()
	CloseListener(&errListener{err: errors.New("boom")}, nil)
}

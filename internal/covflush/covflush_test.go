package covflush

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type failWriter struct {
	err error
}

func (fw failWriter) Write(p []byte) (n int, err error) {
	return 0, fw.err
}

var mu sync.Mutex

type writeArchiveTestCase struct {
	name         string
	failTempDir  bool
	failReadDir  bool
	failRemove   bool
	metaWriteErr bool
	countersErr  bool
	failWriter   bool
	wantFiles    int
	wantErr      bool
}

func setupCovFlushHooks(t *testing.T, tc writeArchiveTestCase) {
	t.Helper()
	if tc.failTempDir {
		orig := mkdirTempFn
		mkdirTempFn = func(dir, pattern string) (string, error) {
			return "", errors.New("temp dir failure")
		}
		t.Cleanup(func() { mkdirTempFn = orig })
	}
	if tc.failReadDir {
		orig := readDirFn
		readDirFn = func(name string) ([]os.DirEntry, error) {
			return nil, errors.New("readdir failure")
		}
		t.Cleanup(func() { readDirFn = orig })
	}
	if tc.failRemove {
		orig := removeAllFn
		removeAllFn = func(path string) error {
			return errors.New("remove failed")
		}
		t.Cleanup(func() { removeAllFn = orig })
	}
}

func getCovFlushFns(tc writeArchiveTestCase) (func(string) error, func(string) error) {
	var metaFn func(string) error
	if tc.metaWriteErr {
		metaFn = func(dir string) error {
			return errors.New("meta failed")
		}
	} else {
		metaFn = func(dir string) error {
			return os.WriteFile(filepath.Join(dir, "meta.cov"), []byte("meta data"), 0o600)
		}
	}

	var countersFn func(string) error
	if tc.countersErr {
		countersFn = func(dir string) error {
			return errors.New("counters failed")
		}
	} else {
		countersFn = func(dir string) error {
			return os.WriteFile(filepath.Join(dir, "counters.cov"), []byte("counter data"), 0o600)
		}
	}
	return metaFn, countersFn
}

func verifyTarBufferFiles(t *testing.T, w io.Writer, wantFiles int) {
	t.Helper()
	buf, ok := w.(*bytes.Buffer)
	if !ok {
		t.Fatalf("expected *bytes.Buffer for successful archive inspection")
	}
	tr := tar.NewReader(buf)
	count := 0
	for {
		_, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("reading tar archive: %v", err)
		}
		count++
	}
	if count != wantFiles {
		t.Fatalf("got %d archive entries, want %d", count, wantFiles)
	}
}

func runWriteArchiveCase(t *testing.T, tc writeArchiveTestCase) {
	t.Helper()
	mu.Lock()
	defer mu.Unlock()

	setupCovFlushHooks(t, tc)
	metaFn, countersFn := getCovFlushFns(tc)

	var w io.Writer = &bytes.Buffer{}
	if tc.failWriter {
		w = failWriter{err: errors.New("write failure")}
	}

	err := WriteArchive(w, metaFn, countersFn)
	if (err != nil) != tc.wantErr {
		t.Fatalf("WriteArchive() error = %v, wantErr %v", err, tc.wantErr)
	}
	if !tc.wantErr && tc.wantFiles > 0 {
		verifyTarBufferFiles(t, w, tc.wantFiles)
	}
}

func TestWriteArchive(t *testing.T) {
	tests := []writeArchiveTestCase{
		{
			name:      "success with files",
			wantFiles: 2,
			wantErr:   false,
		},
		{
			name:         "metaFn error ignored",
			metaWriteErr: true,
			wantFiles:    1,
			wantErr:      false,
		},
		{
			name:        "countersFn returns error",
			countersErr: true,
			wantErr:     true,
		},
		{
			name:       "writer failure on tar write",
			failWriter: true,
			wantErr:    true,
		},
		{
			name:        "mkdirTemp failure",
			failTempDir: true,
			wantErr:     true,
		},
		{
			name:        "readDir failure",
			failReadDir: true,
			wantErr:     true,
		},
		{
			name:       "removeAll failure",
			failRemove: true,
			wantErr:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runWriteArchiveCase(t, tc)
		})
	}
}

func TestHandler(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		writeErr    error
		wantCode    int
		wantAllow   string
		wantType    string
		wantBodySub string
	}{
		{name: "method not allowed", method: http.MethodDelete, wantCode: http.StatusMethodNotAllowed, wantAllow: "GET, POST"},
		{name: "post tar", method: http.MethodPost, wantCode: http.StatusOK, wantType: "application/x-tar", wantBodySub: "covcounters.test"},
		{name: "get tar", method: http.MethodGet, wantCode: http.StatusOK, wantType: "application/x-tar", wantBodySub: "covcounters.test"},
		{name: "write error", method: http.MethodGet, writeErr: errors.New("simulated flush error"), wantCode: http.StatusInternalServerError, wantBodySub: "simulated flush error"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := handler(nil, func(dir string) error {
				if tc.writeErr != nil {
					return tc.writeErr
				}
				return os.WriteFile(filepath.Join(dir, "covcounters.test"), []byte("covdata"), 0o600)
			})
			rec := httptest.NewRecorder()
			h(rec, httptest.NewRequest(tc.method, "/debug/coverage/flush", http.NoBody))
			if rec.Code != tc.wantCode {
				t.Fatalf("code = %d, want %d", rec.Code, tc.wantCode)
			}
			if got := rec.Header().Get("Allow"); got != tc.wantAllow {
				t.Fatalf("Allow = %q, want %q", got, tc.wantAllow)
			}
			if tc.wantType != "" && rec.Header().Get("Content-Type") != tc.wantType {
				t.Fatalf("Content-Type = %q", rec.Header().Get("Content-Type"))
			}
			if tc.wantBodySub != "" && !strings.Contains(rec.Body.String(), tc.wantBodySub) {
				t.Fatalf("body = %q, want %q", rec.Body.String(), tc.wantBodySub)
			}
		})
	}
}

func TestHandlerPublic(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler()(rec, httptest.NewRequest(http.MethodPut, "/", http.NoBody))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("code = %d", rec.Code)
	}
}

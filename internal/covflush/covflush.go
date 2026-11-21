package covflush

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime/coverage"
	"sync"
)

var (
	mkdirTempFn = os.MkdirTemp
	readDirFn   = os.ReadDir
	removeAllFn = os.RemoveAll
)

// WriteArchive invokes metaFn and countersFn within a temporary directory,
// then packages all files generated in that directory into a tar archive written to w.
func WriteArchive(w io.Writer, metaFn, countersFn func(string) error) (retErr error) {
	dir, err := mkdirTempFn("", "cov-*")
	if err != nil {
		return err
	}
	defer func() {
		if rmErr := removeAllFn(dir); rmErr != nil && retErr == nil {
			retErr = rmErr
		}
	}()

	if metaFn != nil {
		if err = metaFn(dir); err != nil {
			// WriteMetaDir returns an error when binary is not built with -cover.
			// This is non-fatal for coverage counters capture during unit tests or uninstrumented runs.
			_ = err
		}
	}
	if countersFn != nil {
		if err = countersFn(dir); err != nil {
			return err
		}
	}

	entries, err := readDirFn(dir)
	if err != nil {
		return err
	}

	tw := tar.NewWriter(w)
	for _, e := range entries {
		data, rErr := os.ReadFile(filepath.Join(dir, e.Name()))
		hErr := tw.WriteHeader(&tar.Header{
			Name: e.Name(),
			Mode: 0o600,
			Size: int64(len(data)),
		})
		_, wErr := tw.Write(data)
		if err := errors.Join(rErr, hErr, wErr); err != nil {
			if cErr := tw.Close(); cErr != nil {
				return errors.Join(err, cErr)
			}
			return err
		}
	}

	return tw.Close()
}

var flushMu sync.Mutex

// Handler returns the shared GET/POST coverage flush endpoint handler that
// streams a tar of the process coverage meta and counter files.
func Handler() http.HandlerFunc {
	return handler(coverage.WriteMetaDir, coverage.WriteCountersDir)
}

func handler(metaFn, countersFn func(string) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		flushMu.Lock()
		defer flushMu.Unlock()

		var buf bytes.Buffer
		if err := WriteArchive(&buf, metaFn, countersFn); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/x-tar")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(buf.Bytes()); err != nil {
			return
		}
	}
}

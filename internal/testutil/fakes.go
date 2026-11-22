package testutil

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/azylman/mirrormere/internal/domain"
)

// ErrWrite is the error every FailingFlusher Write returns.
var ErrWrite = errors.New("testutil: simulated write failure")

// NonFlusher is an http.ResponseWriter that does not implement http.Flusher.
// Like net/http, the first status written wins and a Write without a prior
// WriteHeader records 200.
type NonFlusher struct {
	Code   int
	Body   bytes.Buffer
	header http.Header
}

// Header returns the response headers, allocating them on first use.
func (n *NonFlusher) Header() http.Header {
	if n.header == nil {
		n.header = make(http.Header)
	}
	return n.header
}

// Write appends b to Body.
func (n *NonFlusher) Write(b []byte) (int, error) {
	n.WriteHeader(http.StatusOK)
	return n.Body.Write(b)
}

// WriteHeader records code unless a status was already recorded.
func (n *NonFlusher) WriteHeader(code int) {
	if n.Code == 0 {
		n.Code = code
	}
}

// FailingFlusher is a flushable NonFlusher whose Write always fails with
// ErrWrite.
type FailingFlusher struct{ NonFlusher }

// Write fails with ErrWrite without recording anything.
func (*FailingFlusher) Write([]byte) (int, error) { return 0, ErrWrite }

// Flush does nothing.
func (*FailingFlusher) Flush() {}

// ErrReader is an io.Reader whose Read always fails with Err, or with
// io.ErrUnexpectedEOF when Err is nil.
type ErrReader struct{ Err error }

// Read returns no bytes and the configured error.
func (r ErrReader) Read([]byte) (int, error) {
	if r.Err == nil {
		return 0, io.ErrUnexpectedEOF
	}
	return 0, r.Err
}

// Packages is a widget package loader backed by a map.
type Packages struct {
	ByType map[string]*domain.Package
	Err    error
}

// LoadPackage returns Err when set, else the package for widgetType, else a
// "package not found: <type>" error.
func (p *Packages) LoadPackage(widgetType string) (*domain.Package, error) {
	if p.Err != nil {
		return nil, p.Err
	}
	if pkg, ok := p.ByType[widgetType]; ok {
		return pkg, nil
	}
	return nil, fmt.Errorf("package not found: %s", widgetType)
}

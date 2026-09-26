package provider

import (
	"strings"
	"testing"
)

func TestCalDAV_InternalHelpers(t *testing.T) {
	t.Parallel()

	t.Run("NewCalDAVClient nil defaults", func(t *testing.T) {
		t.Parallel()
		client := NewCalDAVClient(nil, nil)
		if client == nil || client.client == nil || client.logger == nil {
			t.Fatal("expected non-nil client and logger defaults")
		}
	})

	t.Run("ensureTrailingSlash", func(t *testing.T) {
		t.Parallel()
		if got := ensureTrailingSlash("https://example.com/dav"); got != "https://example.com/dav/" {
			t.Errorf("expected trailing slash, got %q", got)
		}
		if got := ensureTrailingSlash("https://example.com/dav/"); got != "https://example.com/dav/" {
			t.Errorf("expected unchanged trailing slash, got %q", got)
		}
		if got := ensureTrailingSlash("https://example.com/dav/file.ics"); got != "https://example.com/dav/file.ics" {
			t.Errorf("expected unchanged file URL, got %q", got)
		}
		if got := ensureTrailingSlash("http:// invalid url"); got != "http:// invalid url" {
			t.Errorf("expected unchanged invalid URL, got %q", got)
		}
	})

	t.Run("redactURL", func(t *testing.T) {
		t.Parallel()
		if got := redactURL("https://user:secret@example.com/dav"); !strings.Contains(got, "xxxxx") {
			t.Errorf("expected redacted password, got %q", got)
		}
		if got := redactURL("http:// invalid url"); got != "http:// invalid url" {
			t.Errorf("expected unchanged invalid URL, got %q", got)
		}
	})

	t.Run("resolveHRef", func(t *testing.T) {
		t.Parallel()
		if got := resolveHRef("https://example.com/dav/", "/cal/"); got != "https://example.com/cal/" {
			t.Errorf("expected resolved absolute path, got %q", got)
		}
		if got := resolveHRef("https://example.com/dav/", "cal/"); got != "https://example.com/dav/cal/" {
			t.Errorf("expected resolved relative path, got %q", got)
		}
		if got := resolveHRef("http:// invalid base", "cal/"); got != "cal/" {
			t.Errorf("expected fallback on invalid base, got %q", got)
		}
		if got := resolveHRef("https://example.com/dav/", "http:// invalid ref"); got != "http:// invalid ref" {
			t.Errorf("expected fallback on invalid ref, got %q", got)
		}
	})
}

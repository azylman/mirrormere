package provider

import "testing"

func TestNewCalDAVClient_Defaults(t *testing.T) {
	t.Parallel()
	if c := NewCalDAVClient(nil, nil); c.client == nil || c.logger == nil || c.discoveryCache == nil {
		t.Fatalf("NewCalDAVClient(nil, nil) = %+v, want default client, logger and cache", c)
	}
}

func TestEnsureTrailingSlash(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, in, want string }{
		{"collection gains slash", "https://example.com/dav", "https://example.com/dav/"},
		{"slash kept", "https://example.com/dav/", "https://example.com/dav/"},
		{"file path untouched", "https://example.com/dav/file.ics", "https://example.com/dav/file.ics"},
		{"unparsable returned as is", "http:// invalid url", "http:// invalid url"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ensureTrailingSlash(tc.in); got != tc.want {
				t.Errorf("ensureTrailingSlash(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestRedactURL(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, in, want string }{
		{"password masked", "https://user:secret@example.com/dav", "https://user:xxxxx@example.com/dav"},
		{"unparsable returned as is", "http:// invalid url", "http:// invalid url"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := redactURL(tc.in); got != tc.want {
				t.Errorf("redactURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestResolveHRef(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, base, ref, want string }{
		{"absolute path", "https://example.com/dav/", "/cal/", "https://example.com/cal/"},
		{"relative path", "https://example.com/dav/", "cal/", "https://example.com/dav/cal/"},
		{"unparsable base returns ref", "http:// invalid base", "cal/", "cal/"},
		{"unparsable ref returned as is", "https://example.com/dav/", "http:// invalid ref", "http:// invalid ref"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := resolveHRef(tc.base, tc.ref); got != tc.want {
				t.Errorf("resolveHRef(%q, %q) = %q, want %q", tc.base, tc.ref, got, tc.want)
			}
		})
	}
}

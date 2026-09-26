package provider_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/azylman/mirrormere/internal/provider"
)

func TestPhotosProvider_AFInitDataCallback_Success(t *testing.T) {
	// Sample mock Google Photos shared album HTML with AF_initDataCallback
	mockHTML := `<!DOCTYPE html>
<html>
<head>
  <meta property="og:title" content="Family Vacation 2026 - Google Photos">
  <title>Family Vacation 2026 - Google Photos</title>
</head>
<body>
  <script>
    AF_initDataCallback({
      key: 'ds:1',
      hash: '2',
      data: [
        "ignored_key",
        [
          ["photo_1", ["https://lh3.googleusercontent.com/pw/AL9nZEW1=w1024-h768", 1200, 800], 1755268200000],
          ["photo_2", ["https://lh3.googleusercontent.com/pw/AL9nZEW2", 1920, 1080], 1755268300000],
          ["photo_3", ["https://lh3.googleusercontent.com/pw/AL9nZEW3=s0", 1000, 1000], 1755268400000]
        ]
      ],
      sideChannel: {}
    });
  </script>
</body>
</html>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(mockHTML))
	}))
	defer server.Close()

	p := provider.NewPhotosProvider()
	cfg := map[string]any{
		"share_url":              server.URL,
		"cycle_interval_seconds": 30,
		"preload_count":          10,
		"shuffle":                false,
	}

	err := p.Init(context.Background(), cfg, provider.InitOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	res, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}

	snap, ok := res.(*provider.PhotoSnapshot)
	if !ok {
		t.Fatalf("expected *provider.PhotoSnapshot, got %T", res)
	}

	if snap.AlbumName != "Family Vacation 2026" {
		t.Errorf("expected album name 'Family Vacation 2026', got %q", snap.AlbumName)
	}
	if snap.TotalPhotos != 3 {
		t.Errorf("expected 3 total photos, got %d", snap.TotalPhotos)
	}
	if snap.CycleIntervalSeconds != 30 {
		t.Errorf("expected cycle interval 30, got %d", snap.CycleIntervalSeconds)
	}
	if len(snap.Photos) != 3 {
		t.Fatalf("expected 3 photos, got %d", len(snap.Photos))
	}

	p1 := snap.Photos[0]
	if p1.ID != "photo_1" {
		t.Errorf("expected photo_1, got %q", p1.ID)
	}
	if p1.URL != "https://lh3.googleusercontent.com/pw/AL9nZEW1" {
		t.Errorf("expected clean URL, got %q", p1.URL)
	}
	if p1.AspectRatio != 1.5 {
		t.Errorf("expected aspect ratio 1.5, got %v", p1.AspectRatio)
	}
	if p1.Timestamp == "" {
		t.Errorf("expected RFC3339 timestamp, got empty")
	}

	p2 := snap.Photos[1]
	if p2.URL != "https://lh3.googleusercontent.com/pw/AL9nZEW2" {
		t.Errorf("expected clean URL for p2, got %q", p2.URL)
	}
	if p2.AspectRatio != 1.78 {
		t.Errorf("expected aspect ratio 1.78, got %v", p2.AspectRatio)
	}

	p3 := snap.Photos[2]
	if p3.URL != "https://lh3.googleusercontent.com/pw/AL9nZEW3" {
		t.Errorf("expected clean URL for p3, got %q", p3.URL)
	}
	if p3.AspectRatio != 1.0 {
		t.Errorf("expected aspect ratio 1.0, got %v", p3.AspectRatio)
	}
}

func TestPhotosProvider_AFInitDataCallback_FunctionWrapper(t *testing.T) {
	mockHTML := `<!DOCTYPE html>
<html>
<head>
  <title>Summer Roadtrip - Google Photos</title>
</head>
<body>
  <script>
    AF_initDataCallback({
      key: 'ds:1',
      hash: '2',
      data: function() {
        return [
          "key",
          [
            ["img_a", ["https://lh3.googleusercontent.com/pw/ROADA", 800, 600], 1755268200000]
          ]
        ]
      },
      sideChannel: {}
    });
  </script>
</body>
</html>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(mockHTML))
	}))
	defer server.Close()

	p := provider.NewPhotosProvider()
	cfg := map[string]any{
		"share_url": server.URL,
	}

	if err := p.Init(context.Background(), cfg, provider.InitOptions{}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	res, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}

	snap, ok := res.(*provider.PhotoSnapshot)
	if !ok {
		t.Fatalf("expected *provider.PhotoSnapshot, got %T", res)
	}

	if snap.AlbumName != "Summer Roadtrip" {
		t.Errorf("expected album name 'Summer Roadtrip', got %q", snap.AlbumName)
	}
	if len(snap.Photos) != 1 {
		t.Fatalf("expected 1 photo, got %d", len(snap.Photos))
	}
	if snap.Photos[0].ID != "img_a" {
		t.Errorf("expected img_a, got %q", snap.Photos[0].ID)
	}
}

func TestPhotosProvider_FallbackRegex_Success(t *testing.T) {
	mockHTML := `<!DOCTYPE html>
<html>
<head>
  <meta property="og:title" content="Art Showcase">
</head>
<body>
  <div>
    <img src="https://lh3.googleusercontent.com/pw/ART_ITEM_1=w500-h300" />
    <img src="https://lh4.googleusercontent.com/pw/ART_ITEM_2" />
    <img src="https://lh3.googleusercontent.com/pw/ART_ITEM_1=w100" /> <!-- Duplicate -->
    <img src="https://lh3.googleusercontent.com/a/AVATAR_IGNORED" /> <!-- Avatar ignored -->
  </div>
</body>
</html>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(mockHTML))
	}))
	defer server.Close()

	p := provider.NewPhotosProvider()
	cfg := map[string]any{
		"share_url": server.URL,
	}

	if err := p.Init(context.Background(), cfg, provider.InitOptions{}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	res, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}

	snap, ok := res.(*provider.PhotoSnapshot)
	if !ok {
		t.Fatalf("expected *provider.PhotoSnapshot, got %T", res)
	}

	if snap.AlbumName != "Art Showcase" {
		t.Errorf("expected 'Art Showcase', got %q", snap.AlbumName)
	}
	if snap.TotalPhotos != 2 {
		t.Errorf("expected 2 unique photos, got %d", snap.TotalPhotos)
	}
	if len(snap.Photos) != 2 {
		t.Fatalf("expected 2 photos, got %d", len(snap.Photos))
	}
}

func TestPhotosProvider_RedirectsAndAuthErrors(t *testing.T) {
	tests := []struct {
		name        string
		handler     http.HandlerFunc
		expectedErr error
	}{
		{
			name: "Redirect to accounts.google.com (private album)",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/login" {
					w.WriteHeader(http.StatusOK)
					return
				}
				http.Redirect(w, r, "https://accounts.google.com/ServiceLogin?continue=https://photos.google.com", http.StatusFound)
			},
			expectedErr: provider.ErrAlbumPrivate,
		},
		{
			name: "Redirect to sorry.google.com (CAPTCHA)",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "https://sorry.google.com/sorry/index", http.StatusFound)
			},
			expectedErr: provider.ErrAlbumRateLimit,
		},
		{
			name: "Redirect to consent.google.com",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "https://consent.google.com/m?continue=https://photos.google.com", http.StatusFound)
			},
			expectedErr: provider.ErrAlbumPrivate,
		},
		{
			name: "HTTP 404 Not Found",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			},
			expectedErr: provider.ErrAlbumNotFound,
		},
		{
			name: "HTTP 403 Forbidden",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusForbidden)
			},
			expectedErr: provider.ErrAlbumPrivate,
		},
		{
			name: "HTTP 429 Too Many Requests",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusTooManyRequests)
			},
			expectedErr: provider.ErrAlbumRateLimit,
		},
		{
			name: "HTTP 500 Server Error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
			expectedErr: errors.New("upstream Google Photos returned HTTP 500"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(tc.handler)
			defer server.Close()

			p := provider.NewPhotosProvider()
			cfg := map[string]any{"share_url": server.URL}
			if err := p.Init(context.Background(), cfg, provider.InitOptions{}); err != nil {
				t.Fatalf("Init failed: %v", err)
			}

			_, err := p.Fetch(context.Background())
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if tc.expectedErr != nil && !strings.Contains(err.Error(), tc.expectedErr.Error()) {
				t.Errorf("expected error containing %q, got %q", tc.expectedErr.Error(), err.Error())
			}
		})
	}
}

func TestPhotosProvider_SlowlorisDefense(t *testing.T) {
	// Server returns > 5MB of HTML
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		// Write 5.5MB of data
		chunk := strings.Repeat("A", 1024*1024)
		for i := 0; i < 6; i++ {
			_, _ = io.WriteString(w, chunk)
		}
	}))
	defer server.Close()

	p := provider.NewPhotosProvider()
	cfg := map[string]any{"share_url": server.URL}
	if err := p.Init(context.Background(), cfg, provider.InitOptions{}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	_, err := p.Fetch(context.Background())
	if err == nil {
		t.Fatalf("expected Slowloris ceiling error, got nil")
	}
	if !strings.Contains(err.Error(), "google photos payload exceeded") {
		t.Errorf("expected payload exceeded error, got: %v", err)
	}
}

func TestPhotosProvider_SecretResolution(t *testing.T) {
	// 1. Both share_url and share_url_env provided
	p := provider.NewPhotosProvider()
	err := p.Init(context.Background(), map[string]any{
		"share_url":     "https://photos.app.goo.gl/123",
		"share_url_env": "MY_PHOTO_ENV",
	}, provider.InitOptions{})
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected mutually exclusive error, got: %v", err)
	}

	// 2. Neither provided
	p = provider.NewPhotosProvider()
	err = p.Init(context.Background(), map[string]any{}, provider.InitOptions{})
	if err == nil || !strings.Contains(err.Error(), "requires either 'share_url' or 'share_url_env'") {
		t.Errorf("expected missing URL error, got: %v", err)
	}

	// 3. share_url_env set but missing/empty in opts and env
	p = provider.NewPhotosProvider()
	err = p.Init(context.Background(), map[string]any{
		"share_url_env": "MISSING_ENV_VAR_12345",
	}, provider.InitOptions{})
	if err == nil || !strings.Contains(err.Error(), "is not set or empty") {
		t.Errorf("expected unset env var error, got: %v", err)
	}

	// 4. share_url_env resolved from opts.Secrets
	p = provider.NewPhotosProvider()
	err = p.Init(context.Background(), map[string]any{
		"share_url_env": "PHOTO_URL_SECRET",
	}, provider.InitOptions{
		Secrets: map[string]string{
			"PHOTO_URL_SECRET": "https://photos.app.goo.gl/secret123",
		},
	})
	if err != nil {
		t.Errorf("expected successful resolution from opts.Secrets, got: %v", err)
	}

	// 5. share_url_env resolved from os.Getenv
	_ = os.Setenv("TEST_ALBUM_ENV_VAR", "https://photos.app.goo.gl/env123")
	defer func() { _ = os.Unsetenv("TEST_ALBUM_ENV_VAR") }()

	p = provider.NewPhotosProvider()
	err = p.Init(context.Background(), map[string]any{
		"share_url_env": "TEST_ALBUM_ENV_VAR",
	}, provider.InitOptions{})
	if err != nil {
		t.Errorf("expected successful resolution from os.Getenv, got: %v", err)
	}

	// 6. share_url resolved from opts.Secrets["share_url"]
	p = provider.NewPhotosProvider()
	err = p.Init(context.Background(), map[string]any{
		"share_url": "https://placeholder.url",
	}, provider.InitOptions{
		Secrets: map[string]string{
			"share_url": "https://photos.app.goo.gl/override123",
		},
	})
	if err != nil {
		t.Errorf("expected successful override from opts.Secrets, got: %v", err)
	}

	// 7. Invalid URL scheme (ftp://)
	p = provider.NewPhotosProvider()
	err = p.Init(context.Background(), map[string]any{
		"share_url": "ftp://photos.google.com/album",
	}, provider.InitOptions{})
	if err == nil || !strings.Contains(err.Error(), "invalid share URL") {
		t.Errorf("expected invalid share URL error, got: %v", err)
	}
}

func TestPhotosProvider_SWR(t *testing.T) {
	fetchCount := 0
	mockHTML := `<!DOCTYPE html>
<html>
<head><title>SWR Album</title></head>
<body>
  <img src="https://lh3.googleusercontent.com/pw/SWROK" />
</body>
</html>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetchCount++
		if fetchCount == 1 {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(mockHTML))
		} else {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	p := provider.NewPhotosProvider()
	cfg := map[string]any{"share_url": server.URL}
	if err := p.Init(context.Background(), cfg, provider.InitOptions{}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// First fetch: succeeds
	res1, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("First fetch failed: %v", err)
	}
	snap1 := res1.(*provider.PhotoSnapshot)
	if len(snap1.Photos) != 1 {
		t.Fatalf("expected 1 photo, got %d", len(snap1.Photos))
	}

	// Second fetch: upstream 500, but SWR returns cached snapshot
	res2, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Second fetch should return cached snapshot under SWR, got err: %v", err)
	}
	snap2 := res2.(*provider.PhotoSnapshot)
	if len(snap2.Photos) != 1 || snap2.Photos[0].URL != snap1.Photos[0].URL {
		t.Errorf("expected cached snapshot returned, got: %+v", snap2)
	}
}

func TestPhotosProvider_ShuffleAndPreload(t *testing.T) {
	// Generate HTML with 10 photos
	var sb strings.Builder
	sb.WriteString("<html><body>")
	for i := 1; i <= 10; i++ {
		sb.WriteString(fmt.Sprintf(`<img src="https://lh3.googleusercontent.com/pw/PHOTO_%d" />`, i))
	}
	sb.WriteString("</body></html>")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sb.String()))
	}))
	defer server.Close()

	p := provider.NewPhotosProvider()
	cfg := map[string]any{
		"share_url":              server.URL,
		"preload_count":          4,
		"shuffle":                true,
		"cycle_interval_seconds": 120,
		"image_size":             []any{800, 600},
	}

	if err := p.Init(context.Background(), cfg, provider.InitOptions{}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	res, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}
	snap := res.(*provider.PhotoSnapshot)

	if snap.TotalPhotos != 10 {
		t.Errorf("expected 10 total photos, got %d", snap.TotalPhotos)
	}
	if len(snap.Photos) != 4 {
		t.Errorf("expected 4 photos clamped to preload_count, got %d", len(snap.Photos))
	}
	if snap.CycleIntervalSeconds != 120 {
		t.Errorf("expected cycle interval 120, got %d", snap.CycleIntervalSeconds)
	}
}

func TestPhotosProvider_EmptyAlbum(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html><body><div>Empty Album</div></body></html>"))
	}))
	defer server.Close()

	p := provider.NewPhotosProvider()
	if err := p.Init(context.Background(), map[string]any{"share_url": server.URL}, provider.InitOptions{}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	res, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch should succeed on empty album, got: %v", err)
	}
	snap := res.(*provider.PhotoSnapshot)
	if snap.TotalPhotos != 0 {
		t.Errorf("expected 0 total photos, got %d", snap.TotalPhotos)
	}
	if len(snap.Photos) != 0 {
		t.Errorf("expected 0 photos, got %d", len(snap.Photos))
	}
}

func TestPhotosProvider_RegistryAndLifecycle(t *testing.T) {
	reg := provider.NewRegistry()

	if !reg.Has("photo-carousel") {
		t.Error("registry missing 'photo-carousel'")
	}
	if !reg.Has("photos") {
		t.Error("registry missing 'photos'")
	}

	p1, err := reg.Create("photo-carousel")
	if err != nil {
		t.Fatalf("Create photo-carousel failed: %v", err)
	}
	if p1 == nil {
		t.Fatal("expected non-nil Provider")
	}

	p2, err := reg.Create("photos")
	if err != nil {
		t.Fatalf("Create photos failed: %v", err)
	}
	if p2 == nil {
		t.Fatal("expected non-nil Provider")
	}

	// Subscribe is a no-op
	if err := p1.Subscribe(context.Background(), nil); err != nil {
		t.Errorf("Subscribe failed: %v", err)
	}

	// Shutdown cleans resources
	if err := p1.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown failed: %v", err)
	}
}

func TestPhotosProvider_EdgeCases(t *testing.T) {
	// Uninitialized provider Fetch returns error
	p := provider.NewPhotosProvider()
	_, err := p.Fetch(context.Background())
	if err == nil || !strings.Contains(err.Error(), "provider not initialized") {
		t.Errorf("expected uninitialized error, got: %v", err)
	}

	// Malformed AF_initDataCallback with invalid JSON
	malformedHTML := `<html><body>
  <script>AF_initDataCallback({key: 'ds:1', data: [INVALID JSON...});</script>
</body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(malformedHTML))
	}))
	defer server.Close()

	if err := p.Init(context.Background(), map[string]any{"share_url": server.URL}, provider.InitOptions{}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	res, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch on malformed JSON should return empty photos without failing: %v", err)
	}
	snap := res.(*provider.PhotoSnapshot)
	if len(snap.Photos) != 0 {
		t.Errorf("expected 0 photos, got %d", len(snap.Photos))
	}
}

func TestPhotosProvider_ConfigFloatsAndClamping(t *testing.T) {
	p := provider.NewPhotosProvider()
	cfg := map[string]any{
		"share_url":              "https://photos.google.com/share/test",
		"cycle_interval_seconds": float64(2),    // Below min (5)
		"preload_count":          float64(1000), // Above max (500)
	}
	if err := p.Init(context.Background(), cfg, provider.InitOptions{}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
}

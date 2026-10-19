package provider_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azylman/mirrormere/internal/provider"
)

const (
	mockPhotosAFHTML       = `<!DOCTYPE html><html><head><meta property="og:title" content="Family Vacation 2026 - Google Photos"><title>Family Vacation 2026 - Google Photos</title></head><body><script>AF_initDataCallback({key:'ds:1',hash:'2',data:["k",[["photo_1",["https://lh3.googleusercontent.com/pw/AL9nZEW1=w1024-h768",1200,800],1755268200000],["photo_2",["https://lh3.googleusercontent.com/pw/AL9nZEW2",1920,1080],1755268300000],["photo_3",["https://lh3.googleusercontent.com/pw/AL9nZEW3=s0",1000,1000],1755268400000]]],sideChannel:{}});</script></body></html>`
	mockPhotosFuncHTML     = `<!DOCTYPE html><html><head><title>Summer Roadtrip - Google Photos</title></head><body><script>AF_initDataCallback({key:'ds:1',hash:'2',data:function(){return ["key",[["img_a",["https://lh3.googleusercontent.com/pw/ROADA",800,600],1755268200000]]]},sideChannel:{}});</script></body></html>`
	mockPhotosFallbackHTML = `<!DOCTYPE html><html><head><meta property="og:title" content="Art Showcase"></head><body><div><img src="https://lh3.googleusercontent.com/pw/ART_ITEM_1=w500-h300"/><img src="https://lh4.googleusercontent.com/pw/ART_ITEM_2"/><img src="https://lh3.googleusercontent.com/pw/ART_ITEM_1=w100"/><img src="https://lh3.googleusercontent.com/a/AVATAR_IGNORED"/></div></body></html>`
)

func TestPhotosProvider_FetchExtraction(t *testing.T) {
	t.Parallel()
	var tenPhotos strings.Builder
	tenPhotos.WriteString("<html><body>")
	for i := 1; i <= 10; i++ { fmt.Fprintf(&tenPhotos, `<img src="https://lh3.googleusercontent.com/pw/PHOTO_%d"/>`, i) }
	tenPhotos.WriteString("</body></html>")

	tests := []struct {
		name             string
		html             string
		cfg              map[string]any
		wantAlbum        string
		wantTotal        int
		wantPhotos       int
		wantCycle        int
		wantFirstID      string
		wantFirstURL     string
		wantFirstAspect  float64
		wantSecondAspect float64
		wantThirdAspect  float64
	}{
		{"af_callback", mockPhotosAFHTML, map[string]any{"cycle_interval_seconds": 30, "preload_count": 10, "shuffle": false}, "Family Vacation 2026", 3, 3, 30, "photo_1", "https://lh3.googleusercontent.com/pw/AL9nZEW1", 1.5, 1.78, 1.0},
		{"func_wrapper", mockPhotosFuncHTML, nil, "Summer Roadtrip", 1, 1, 0, "img_a", "", 0, 0, 0},
		{"fallback_regex", mockPhotosFallbackHTML, nil, "Art Showcase", 2, 2, 0, "", "", 0, 0, 0},
		{"shuffle_preload", tenPhotos.String(), map[string]any{"preload_count": 4, "shuffle": true, "cycle_interval_seconds": 120, "image_size": []any{800, 600}}, "Shared Album", 10, 4, 120, "", "", 0, 0, 0},
		{"empty_album", "<html><body><div>Empty Album</div></body></html>", nil, "Shared Album", 0, 0, 0, "", "", 0, 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = w.Write([]byte(tc.html))
			}))
			defer ts.Close()

			cfg := map[string]any{"share_url": ts.URL}
			for k, v := range tc.cfg { cfg[k] = v }
			p := provider.NewPhotosProvider()
			if err := p.Init(context.Background(), cfg, provider.InitOptions{}); err != nil { t.Fatalf("Init failed: %v", err) }
			res, err := p.Fetch(context.Background())
			if err != nil { t.Fatalf("Fetch failed: %v", err) }
			snap := res.(*provider.PhotoSnapshot)
			if snap.AlbumName != tc.wantAlbum || snap.TotalPhotos != tc.wantTotal || len(snap.Photos) != tc.wantPhotos {
				t.Fatalf("got album=%q total=%d photos=%d; want %q, %d, %d", snap.AlbumName, snap.TotalPhotos, len(snap.Photos), tc.wantAlbum, tc.wantTotal, tc.wantPhotos)
			}
			if tc.wantCycle != 0 && snap.CycleIntervalSeconds != tc.wantCycle {
				t.Errorf("cycle interval = %d, want %d", snap.CycleIntervalSeconds, tc.wantCycle)
			}
			if tc.wantFirstID != "" && snap.Photos[0].ID != tc.wantFirstID {
				t.Errorf("first ID = %q, want %q", snap.Photos[0].ID, tc.wantFirstID)
			}
			if tc.wantFirstURL != "" && snap.Photos[0].URL != tc.wantFirstURL {
				t.Errorf("first URL = %q, want %q", snap.Photos[0].URL, tc.wantFirstURL)
			}
			if tc.wantFirstAspect != 0 && snap.Photos[0].AspectRatio != tc.wantFirstAspect {
				t.Errorf("first aspect = %v, want %v", snap.Photos[0].AspectRatio, tc.wantFirstAspect)
			}
			if tc.wantSecondAspect != 0 && snap.Photos[1].AspectRatio != tc.wantSecondAspect {
				t.Errorf("second aspect = %v, want %v", snap.Photos[1].AspectRatio, tc.wantSecondAspect)
			}
			if tc.wantThirdAspect != 0 && snap.Photos[2].AspectRatio != tc.wantThirdAspect {
				t.Errorf("third aspect = %v, want %v", snap.Photos[2].AspectRatio, tc.wantThirdAspect)
			}
			if tc.name == "af_callback" && snap.Photos[0].Timestamp == "" {
				t.Error("expected non-empty RFC3339 timestamp")
			}
		})
	}
}

func TestPhotosProvider_ErrorsAndSWR(t *testing.T) {
	t.Parallel()

	t.Run("redirects and auth", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			name   string
			status int
			target string
			err    error
		}{
			{"accounts", 302, "https://accounts.google.com/ServiceLogin?continue=https://photos.google.com", provider.ErrAlbumPrivate},
			{"sorry", 302, "https://sorry.google.com/sorry/index", provider.ErrAlbumRateLimit},
			{"consent", 302, "https://consent.google.com/m?continue=https://photos.google.com", provider.ErrAlbumPrivate},
			{"404", 404, "", provider.ErrAlbumNotFound},
			{"403", 403, "", provider.ErrAlbumPrivate},
			{"429", 429, "", provider.ErrAlbumRateLimit},
			{"500", 500, "", errors.New("upstream Google Photos returned HTTP 500")},
		} {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.target != "" {
					http.Redirect(w, r, tc.target, tc.status)
					return
				}
				w.WriteHeader(tc.status)
			}))
			p := provider.NewPhotosProvider()
			_ = p.Init(context.Background(), map[string]any{"share_url": ts.URL}, provider.InitOptions{})
			_, err := p.Fetch(context.Background())
			ts.Close()
			if err == nil || !strings.Contains(err.Error(), tc.err.Error()) {
				t.Errorf("%s: want %v, got %v", tc.name, tc.err, err)
			}
		}
	})

	t.Run("slowloris", func(t *testing.T) {
		t.Parallel()
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			chunk := strings.Repeat("A", 1024*1024)
			for i := 0; i < 6; i++ { _, _ = io.WriteString(w, chunk) }
		}))
		defer ts.Close()
		p := provider.NewPhotosProvider()
		_ = p.Init(context.Background(), map[string]any{"share_url": ts.URL}, provider.InitOptions{})
		_, err := p.Fetch(context.Background())
		if err == nil || !strings.Contains(err.Error(), "google photos payload exceeded") {
			t.Fatalf("expected payload exceeded error, got: %v", err)
		}
	})

	t.Run("swr", func(t *testing.T) {
		t.Parallel()
		var count int
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			count++
			if count == 1 {
				w.WriteHeader(200)
				_, _ = w.Write([]byte(`<html><head><title>SWR</title></head><body><img src="https://lh3.googleusercontent.com/pw/SWROK"/></body></html>`))
			} else {
				w.WriteHeader(500)
			}
		}))
		defer ts.Close()
		p := provider.NewPhotosProvider()
		_ = p.Init(context.Background(), map[string]any{"share_url": ts.URL}, provider.InitOptions{})
		res1, err := p.Fetch(context.Background())
		if err != nil { t.Fatalf("first fetch failed: %v", err) }
		res2, err := p.Fetch(context.Background())
		if err != nil { t.Fatalf("second fetch under SWR failed: %v", err) }
		s1, s2 := res1.(*provider.PhotoSnapshot), res2.(*provider.PhotoSnapshot)
		if len(s2.Photos) != 1 || s2.Photos[0].URL != s1.Photos[0].URL {
			t.Fatalf("expected cached snapshot under SWR: %+v", s2)
		}
	})
}

func TestPhotosProvider_InitAndLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name, envKey, envVal string
		cfg                  map[string]any
		opts                 provider.InitOptions
		wantErrSub           string
	}{
		{"mutex", "", "", map[string]any{"share_url": "https://p.app/1", "share_url_env": "E"}, provider.InitOptions{}, "mutually exclusive"},
		{"neither", "", "", map[string]any{}, provider.InitOptions{}, "requires either 'share_url' or 'share_url_env'"},
		{"unset_env", "", "", map[string]any{"share_url_env": "MISSING"}, provider.InitOptions{}, "is not set or empty"},
		{"opts_secret", "", "", map[string]any{"share_url_env": "S"}, provider.InitOptions{Secrets: map[string]string{"S": "https://p.app/s"}}, ""},
		{"env_var", "TEST_ALBUM_ENV", "https://p.app/env", map[string]any{"share_url_env": "TEST_ALBUM_ENV"}, provider.InitOptions{}, ""},
		{"opts_override", "", "", map[string]any{"share_url": "https://p.app/raw"}, provider.InitOptions{Secrets: map[string]string{"share_url": "https://p.app/over"}}, ""},
		{"bad_scheme", "", "", map[string]any{"share_url": "ftp://photos.google.com/album"}, provider.InitOptions{}, "invalid share URL"},
	} {
		if tc.envKey != "" { t.Setenv(tc.envKey, tc.envVal) }
		p := provider.NewPhotosProvider()
		err := p.Init(context.Background(), tc.cfg, tc.opts)
		if tc.wantErrSub != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Errorf("%s: want error %q, got %v", tc.name, tc.wantErrSub, err)
			}
		} else if err != nil {
			t.Errorf("%s: unexpected error: %v", tc.name, err)
		}
	}

	p := provider.NewPhotosProvider()
	if err := p.Init(context.Background(), map[string]any{"share_url": "https://p.app/1", "cycle_interval_seconds": float64(2), "preload_count": float64(1000)}, provider.InitOptions{}); err != nil {
		t.Fatalf("Init with float options failed: %v", err)
	}

	pUninit := provider.NewPhotosProvider()
	if _, err := pUninit.Fetch(context.Background()); err == nil || !strings.Contains(err.Error(), "provider not initialized") {
		t.Fatalf("expected uninitialized error, got: %v", err)
	}
	tsMal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><body><script>AF_initDataCallback({key:'ds:1',data:[INVALID JSON...});</script></body></html>`))
	}))
	defer tsMal.Close()
	pMal := provider.NewPhotosProvider()
	_ = pMal.Init(context.Background(), map[string]any{"share_url": tsMal.URL}, provider.InitOptions{})
	if res, err := pMal.Fetch(context.Background()); err != nil || len(res.(*provider.PhotoSnapshot).Photos) != 0 {
		t.Fatalf("expected empty photos on malformed JSON, got %v, err=%v", res, err)
	}

	reg := provider.NewRegistry()
	if !reg.Has("photo-carousel") { t.Fatal("registry missing photo-carousel provider") }
	p1, err := reg.Create("photo-carousel")
	if err != nil || p1 == nil { t.Fatalf("failed create photo-carousel: %v", err) }
	if err := p1.Subscribe(context.Background(), nil); err != nil { t.Errorf("Subscribe failed: %v", err) }
	if err := p1.Shutdown(context.Background()); err != nil { t.Errorf("Shutdown failed: %v", err) }
}

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"testing"
)

func TestPhotos_InternalHelpers(t *testing.T) {
	t.Parallel()

	t.Run("toFloat conversions", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			val any
			exp float64
			ok  bool
		}{
			{42.5, 42.5, true},
			{float32(10.5), 10.5, true},
			{7, 7.0, true},
			{int64(100), 100.0, true},
			{json.Number("3.14"), 3.14, true},
			{json.Number("invalid"), 0, false},
			{"123.45", 123.45, true},
			{"invalid", 0, false},
			{true, 0, false},
			{nil, 0, false},
		} {
			if got, ok := toFloat(tc.val); ok != tc.ok || (ok && got != tc.exp) {
				t.Errorf("toFloat(%v) = (%v, %v), want (%v, %v)", tc.val, got, ok, tc.exp, tc.ok)
			}
		}
	})

	t.Run("parsePhotosConfig validation", func(t *testing.T) {
		t.Parallel()
		if _, err := parsePhotosConfig(nil, InitOptions{}); err == nil {
			t.Fatal("expected error for nil cfg")
		}
		if _, err := parsePhotosConfig(map[string]any{}, InitOptions{}); err == nil {
			t.Fatal("expected error for missing source")
		}
		if _, err := parsePhotosConfig(map[string]any{"source": "invalid"}, InitOptions{}); err == nil {
			t.Fatal("expected error for invalid source")
		}
		if _, err := parsePhotosConfig(map[string]any{"source": "share_url"}, InitOptions{}); err == nil {
			t.Fatal("expected error for missing album_url")
		}
		if _, err := parsePhotosConfig(map[string]any{"source": "share_url", "album_url": "https://photos.app.goo.gl/TEST", "album_url_env": "ENV_VAR"}, InitOptions{}); err == nil {
			t.Fatal("expected error for both album_url and album_url_env")
		}
		cfg, err := parsePhotosConfig(map[string]any{"source": "share_url", "album_url": "https://photos.app.goo.gl/TEST"}, InitOptions{})
		if err != nil || cfg.Source != "share_url" || cfg.AlbumURL != "https://photos.app.goo.gl/TEST" {
			t.Fatalf("unexpected cfg result: cfg=%+v, err=%v", cfg, err)
		}
		cfgEnv, err := parsePhotosConfig(map[string]any{"source": "ember_bridge", "album_url_env": "ALBUM_ENV"}, InitOptions{})
		if err != nil || cfgEnv.Source != "ember_bridge" || cfgEnv.AlbumURLEnv != "ALBUM_ENV" {
			t.Fatalf("unexpected cfgEnv result: cfg=%+v, err=%v", cfgEnv, err)
		}
	})

	t.Run("parseMediaItem edge cases", func(t *testing.T) {
		t.Parallel()
		for _, bad := range []any{"not-an-array", []any{"id"}, []any{"id", "not-an-array"}, []any{"id", []any{}}, []any{"id", []any{"https://example.com/pic.jpg"}}} {
			if _, ok := parseMediaItem(bad); ok {
				t.Errorf("expected parseMediaItem(%v) ok=false", bad)
			}
		}
		if item, ok := parseMediaItem([]any{"", []any{"https://lh3.googleusercontent.com/pw/TEST", 800, 600}}); !ok || item.ID == "" || item.AspectRatio != 1.33 {
			t.Fatalf("unexpected valid item result: item=%+v, ok=%v", item, ok)
		}
	})

	t.Run("findMediaItems and extractBalancedArray", func(t *testing.T) {
		t.Parallel()
		nested := map[string]any{"wrapper": []any{map[string]any{"photos": []any{[]any{"p1", []any{"https://lh3.googleusercontent.com/pw/N1", 100, 100}}}}}}
		if len(findMediaItems(nested)) == 0 {
			t.Error("expected items found in nested map, got 0")
		}
		if _, ok := extractBalancedArray("no brackets"); ok {
			t.Error("expected false for no brackets")
		}
		if _, ok := extractBalancedArray("[ unclosed"); ok {
			t.Error("expected false for unclosed bracket")
		}
		if got, ok := extractBalancedArray(`[ "hello \"world\"", 'single \'quote\'', [1, 2, 3] ] tail`); !ok || got != `[ "hello \"world\"", 'single \'quote\'', [1, 2, 3] ]` {
			t.Fatalf("extractBalancedArray failed: got=%q, ok=%v", got, ok)
		}
	})

	t.Run("extractAlbumTitle and cleanURL", func(t *testing.T) {
		t.Parallel()
		if title := extractAlbumTitle("<html><body>no title</body></html>"); title != "Shared Album" {
			t.Errorf("expected 'Shared Album', got %q", title)
		}
		raw := "https://lh3.googleusercontent.com/pw/ABCDEF"
		if got := cleanGooglePhotoURL(raw); got != raw {
			t.Errorf("expected %q, got %q", raw, got)
		}
	})

	t.Run("photoItemKey", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name string
			item PhotoItem
			want string
		}{
			{"url_present", PhotoItem{ID: "id1", URL: "https://lh3.googleusercontent.com/pw/1"}, "https://lh3.googleusercontent.com/pw/1"},
			{"url_empty", PhotoItem{ID: "id2", URL: ""}, "id2"},
		}
		for _, tc := range tests {
			if got := photoItemKey(tc.item); got != tc.want {
				t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
			}
		}
	})

	t.Run("reconcilePhotoDeck operations", func(t *testing.T) {
		t.Parallel()
		p := NewPhotosProvider()
		r := rand.New(rand.NewSource(42))

		initial := []PhotoItem{
			{ID: "1", URL: "u1"},
			{ID: "2", URL: "u2"},
			{ID: "3", URL: "u3"},
		}
		p.reconcilePhotoDeck(initial, r)
		if len(p.deck) != 3 || p.deckCursor != 0 {
			t.Fatalf("expected 3 items in deck, got %d (cursor=%d)", len(p.deck), p.deckCursor)
		}
		initialOrder := make([]string, len(p.deck))
		for i, it := range p.deck {
			initialOrder[i] = it.ID
		}

		withAdded := append(slices.Clone(initial), PhotoItem{ID: "4", URL: "u4"})
		p.reconcilePhotoDeck(withAdded, r)
		if len(p.deck) != 4 {
			t.Fatalf("expected 4 items in deck, got %d", len(p.deck))
		}
		for i := range 3 {
			if p.deck[i].ID != initialOrder[i] {
				t.Errorf("deck[%d] = %q, want %q", i, p.deck[i].ID, initialOrder[i])
			}
		}

		withUpdated := slices.Clone(withAdded)
		withUpdated[0].AspectRatio = 2.5
		p.reconcilePhotoDeck(withUpdated, r)
		foundUpdated := false
		for _, it := range p.deck {
			if it.ID == withUpdated[0].ID && it.AspectRatio == 2.5 {
				foundUpdated = true
				break
			}
		}
		if !foundUpdated {
			t.Error("expected updated metadata to be reflected in deck")
		}

		p.deckCursor = 2
		removedBefore := []PhotoItem{withAdded[1], withAdded[2], withAdded[3]}
		p.reconcilePhotoDeck(removedBefore, r)
		if p.deckCursor != 1 {
			t.Errorf("expected cursor to decrement from 2 to 1, got %d", p.deckCursor)
		}

		p.deckCursor = 1
		removedAfter := []PhotoItem{withAdded[1], withAdded[2]}
		p.reconcilePhotoDeck(removedAfter, r)
		if p.deckCursor != 1 {
			t.Errorf("expected cursor to stay 1, got %d", p.deckCursor)
		}
	})

	t.Run("selectAlbumWindow operations", func(t *testing.T) {
		t.Parallel()
		single := []PhotoItem{{ID: "1", URL: "u1"}}
		pSingle := NewPhotosProvider()
		pSingle.deck = slices.Clone(single)
		pSingle.deckCursor = 1
		winSingle := pSingle.selectAlbumWindow(single)
		if len(winSingle) != 1 || pSingle.deck != nil || pSingle.deckCursor != 0 {
			t.Errorf("expected single-item to reset deck, got deck=%v cursor=%d", pSingle.deck, pSingle.deckCursor)
		}

		scraped := []PhotoItem{
			{ID: "1", URL: "u1"},
			{ID: "2", URL: "u2"},
			{ID: "3", URL: "u3"},
			{ID: "4", URL: "u4"},
		}

		pFull := NewPhotosProvider()
		pFull.randSource = rand.New(rand.NewSource(99))
		w1 := pFull.selectAlbumWindow(scraped)
		w2 := pFull.selectAlbumWindow(scraped)
		if len(w1) != 4 || len(w2) != 4 {
			t.Fatalf("expected 4 items in window, got %d, %d", len(w1), len(w2))
		}
		for i := range w1 {
			if w1[i].ID != w2[i].ID {
				t.Fatalf("expected stable deck order: w1[%d]=%s, w2[%d]=%s", i, w1[i].ID, i, w2[i].ID)
			}
		}

		many := make([]PhotoItem, 600)
		for i := range 600 {
			many[i] = PhotoItem{ID: fmt.Sprintf("id-%d", i), URL: fmt.Sprintf("https://lh3.googleusercontent.com/pw/%d", i)}
		}
		pCap := NewPhotosProvider()
		pCap.randSource = rand.New(rand.NewSource(100))
		wCap := pCap.selectAlbumWindow(many)
		if len(wCap) != defaultPhotoPreloadCount {
			t.Fatalf("expected %d items in capped window, got %d", defaultPhotoPreloadCount, len(wCap))
		}
		wWrap := pCap.selectAlbumWindow(many)
		if len(wWrap) != defaultPhotoPreloadCount {
			t.Fatalf("expected wrap-around window length %d, got %d", defaultPhotoPreloadCount, len(wWrap))
		}

		many1000 := make([]PhotoItem, 1000)
		for i := range 1000 {
			many1000[i] = PhotoItem{ID: fmt.Sprintf("id-%d", i), URL: fmt.Sprintf("https://lh3.googleusercontent.com/pw/%d", i)}
		}
		pExact := NewPhotosProvider()
		pExact.randSource = rand.New(rand.NewSource(200))
		_ = pExact.selectAlbumWindow(many1000)
		wExact := pExact.selectAlbumWindow(many1000)
		if len(wExact) != defaultPhotoPreloadCount {
			t.Fatalf("expected exact window length %d, got %d", defaultPhotoPreloadCount, len(wExact))
		}
	})

	t.Run("lifecycle deck reset", func(t *testing.T) {
		t.Parallel()
		p := NewPhotosProvider()
		p.deck = []PhotoItem{{ID: "1", URL: "u1"}}
		p.deckCursor = 1
		p.cachedSnapshot = &PhotoSnapshot{}

		if err := p.Shutdown(context.Background()); err != nil {
			t.Fatalf("Shutdown failed: %v", err)
		}
		if p.deck != nil || p.deckCursor != 0 || p.cachedSnapshot != nil {
			t.Errorf("Shutdown did not reset deck: deck=%v, cursor=%d, cached=%v", p.deck, p.deckCursor, p.cachedSnapshot)
		}

		p.deck = []PhotoItem{{ID: "2", URL: "u2"}}
		p.deckCursor = 1
		p.cachedSnapshot = &PhotoSnapshot{}
		if err := p.Init(context.Background(), map[string]any{"source": "share_url", "album_url": "https://example.com/album"}, InitOptions{}); err != nil {
			t.Fatalf("Init failed: %v", err)
		}
		if p.deck != nil || p.deckCursor != 0 || p.cachedSnapshot != nil {
			t.Errorf("Init did not reset deck: deck=%v, cursor=%d, cached=%v", p.deck, p.deckCursor, p.cachedSnapshot)
		}
	})

	t.Run("IngestPushedPhotos", func(t *testing.T) {
		t.Parallel()
		pShare := NewPhotosProvider()
		_ = pShare.Init(context.Background(), map[string]any{"source": "share_url", "album_url": "https://photos.app.goo.gl/share"}, InitOptions{})
		if _, err := pShare.IngestPushedPhotos([]string{"https://lh3.googleusercontent.com/pw/VALID"}, "Test"); !errors.Is(err, ErrPhotoPushNotSupported) {
			t.Fatalf("expected ErrPhotoPushNotSupported on share_url provider, got %v", err)
		}

		pEmber := NewPhotosProvider()
		_ = pEmber.Init(context.Background(), map[string]any{"source": "ember_bridge", "album_url": "https://photos.app.goo.gl/ember"}, InitOptions{})

		if _, err := pEmber.IngestPushedPhotos(nil, "Empty"); !errors.Is(err, ErrInvalidPhotoPushPayload) {
			t.Fatalf("expected ErrInvalidPhotoPushPayload on empty photos, got %v", err)
		}
		if _, err := pEmber.IngestPushedPhotos([]string{"https://notgoogle.com/pic.jpg"}, "BadURL"); !errors.Is(err, ErrInvalidPhotoPushPayload) {
			t.Fatalf("expected ErrInvalidPhotoPushPayload on bad url, got %v", err)
		}

		validURLs := []string{
			"https://lh3.googleusercontent.com/pw/VALID1=w1024-h768",
			"https://lh3.googleusercontent.com/pw/VALID1",
			"https://lh4.googleusercontent.com/pw/VALID2",
		}
		snap, err := pEmber.IngestPushedPhotos(validURLs, "Custom Album")
		if err != nil {
			t.Fatalf("IngestPushedPhotos failed: %v", err)
		}
		if snap.AlbumName != "Custom Album" || snap.TotalPhotos != 2 || len(snap.Photos) != 2 {
			t.Fatalf("unexpected snapshot: %+v", snap)
		}
		if snap.AlbumURL != "https://photos.app.goo.gl/ember" {
			t.Fatalf("unexpected AlbumURL: %s", snap.AlbumURL)
		}

		// Empty album name preserves cached album name
		snap2, err := pEmber.IngestPushedPhotos([]string{"https://lh3.googleusercontent.com/pw/VALID3"}, "  ")
		if err != nil {
			t.Fatalf("second push failed: %v", err)
		}
		if snap2.AlbumName != "Custom Album" || snap2.TotalPhotos != 1 {
			t.Fatalf("expected preserved album name 'Custom Album', got %q", snap2.AlbumName)
		}
	})

	t.Run("provider methods and url resolution", func(t *testing.T) {
		t.Parallel()
		p := NewPhotosProvider()
		if p.Source() != "" {
			t.Errorf("expected empty initial source, got %q", p.Source())
		}
		if err := p.Subscribe(context.Background(), nil); err != nil {
			t.Errorf("expected nil from Subscribe, got %v", err)
		}
		if _, err := p.Fetch(context.Background()); err == nil {
			t.Error("expected error from uninitialized Fetch")
		}
		pEmber := NewPhotosProvider()
		pEmber.cfg = PhotosConfig{Source: PhotoSourceEmberBridge}
		if _, err := pEmber.Fetch(context.Background()); err == nil {
			t.Error("expected error from uninitialized ember_bridge Fetch")
		}

		if _, err := resolvePhotoAlbumURL(PhotosConfig{AlbumURL: "http://example.com", AlbumURLEnv: "SOME_ENV"}, InitOptions{}); err == nil {
			t.Error("expected mutually exclusive error")
		}
		if _, err := resolvePhotoAlbumURL(PhotosConfig{}, InitOptions{}); err == nil {
			t.Error("expected neither url set error")
		}
		if _, err := resolvePhotoAlbumURL(PhotosConfig{AlbumURLEnv: "UNSET_VAR_XYZ"}, InitOptions{}); err == nil {
			t.Error("expected unset env error")
		}
		if _, err := resolvePhotoAlbumURL(PhotosConfig{AlbumURL: "not-a-valid-url"}, InitOptions{}); err == nil {
			t.Error("expected invalid url error")
		}
		resolved, err := resolvePhotoAlbumURL(PhotosConfig{AlbumURLEnv: "SECRET_VAR"}, InitOptions{Secrets: map[string]string{"SECRET_VAR": "https://photos.app.goo.gl/secret"}})
		if err != nil || resolved != "https://photos.app.goo.gl/secret" {
			t.Errorf("unexpected secret resolution: %s, %v", resolved, err)
		}
	})
}

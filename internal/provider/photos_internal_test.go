package provider

import (
	"context"
	"encoding/json"
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

	t.Run("parsePhotosConfig nil cfg", func(t *testing.T) {
		t.Parallel()
		if cfg, err := parsePhotosConfig(nil, InitOptions{}); err != nil || cfg.CycleIntervalSeconds != defaultPhotoCycleIntervalSeconds {
			t.Fatalf("unexpected nil cfg result: cfg=%+v, err=%v", cfg, err)
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
		scraped := []PhotoItem{
			{ID: "1", URL: "u1"},
			{ID: "2", URL: "u2"},
			{ID: "3", URL: "u3"},
			{ID: "4", URL: "u4"},
		}

		p1 := NewPhotosProvider()
		p1.deck = slices.Clone(scraped)
		p1.deckCursor = 2
		wNoShuffle := p1.selectAlbumWindow(scraped, PhotosConfig{Shuffle: false, PreloadCount: 2})
		if len(wNoShuffle) != 2 || p1.deck != nil || p1.deckCursor != 0 {
			t.Errorf("expected no-shuffle to reset deck, got deck=%v cursor=%d", p1.deck, p1.deckCursor)
		}

		pFull := NewPhotosProvider()
		pFull.randSource = rand.New(rand.NewSource(99))
		cfgFull := PhotosConfig{Shuffle: true, PreloadCount: 10}
		w1 := pFull.selectAlbumWindow(scraped, cfgFull)
		w2 := pFull.selectAlbumWindow(scraped, cfgFull)
		if len(w1) != 4 || len(w2) != 4 {
			t.Fatalf("expected 4 items in window, got %d, %d", len(w1), len(w2))
		}
		for i := range w1 {
			if w1[i].ID != w2[i].ID {
				t.Fatalf("expected stable deck order: w1[%d]=%s, w2[%d]=%s", i, w1[i].ID, i, w2[i].ID)
			}
		}

		pPart := NewPhotosProvider()
		pPart.randSource = rand.New(rand.NewSource(100))
		cfgPart := PhotosConfig{Shuffle: true, PreloadCount: 2}
		win1 := pPart.selectAlbumWindow(scraped, cfgPart)
		win2 := pPart.selectAlbumWindow(scraped, cfgPart)
		if len(win1) != 2 || len(win2) != 2 {
			t.Fatalf("expected 2 items per window, got %d, %d", len(win1), len(win2))
		}
		seen := make(map[string]bool)
		for _, it := range win1 {
			seen[it.ID] = true
		}
		for _, it := range win2 {
			if seen[it.ID] {
				t.Errorf("expected disjoint windows, but %s was repeated", it.ID)
			}
		}

		pWrap := NewPhotosProvider()
		pWrap.randSource = rand.New(rand.NewSource(101))
		cfgWrap := PhotosConfig{Shuffle: true, PreloadCount: 3}
		wrap1 := pWrap.selectAlbumWindow(scraped, cfgWrap)
		wrap2 := pWrap.selectAlbumWindow(scraped, cfgWrap)
		if len(wrap1) != 3 || len(wrap2) != 3 {
			t.Fatalf("expected 3 items per wrap window, got %d, %d", len(wrap1), len(wrap2))
		}
		for _, w := range [][]PhotoItem{wrap1, wrap2} {
			u := make(map[string]bool)
			for _, it := range w {
				if u[it.ID] {
					t.Errorf("found duplicate photo %s in wrap-around window", it.ID)
				}
				u[it.ID] = true
			}
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
		if err := p.Init(context.Background(), map[string]any{"share_url": "https://example.com/album"}, InitOptions{}); err != nil {
			t.Fatalf("Init failed: %v", err)
		}
		if p.deck != nil || p.deckCursor != 0 || p.cachedSnapshot != nil {
			t.Errorf("Init did not reset deck: deck=%v, cursor=%d, cached=%v", p.deck, p.deckCursor, p.cachedSnapshot)
		}
	})
}

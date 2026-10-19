package provider

import (
	"encoding/json"
	"testing"
)

func TestPhotos_InternalHelpers(t *testing.T) {
	t.Parallel()

	t.Run("toFloat conversions", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			val any; exp float64; ok bool
		}{
			{42.5, 42.5, true}, {float32(10.5), 10.5, true}, {7, 7.0, true}, {int64(100), 100.0, true},
			{json.Number("3.14"), 3.14, true}, {json.Number("invalid"), 0, false}, {"123.45", 123.45, true},
			{"invalid", 0, false}, {true, 0, false}, {nil, 0, false},
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
}

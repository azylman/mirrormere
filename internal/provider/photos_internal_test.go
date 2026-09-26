package provider

import (
	"encoding/json"
	"testing"
)

func TestPhotos_InternalHelpers(t *testing.T) {
	t.Parallel()

	t.Run("toFloat conversions", func(t *testing.T) {
		t.Parallel()
		cases := []struct {
			val      any
			expected float64
			ok       bool
		}{
			{float64(42.5), 42.5, true},
			{float32(10.5), 10.5, true},
			{int(7), 7.0, true},
			{int64(100), 100.0, true},
			{json.Number("3.14"), 3.14, true},
			{json.Number("invalid"), 0, false},
			{"123.45", 123.45, true},
			{"invalid", 0, false},
			{true, 0, false},
			{nil, 0, false},
		}

		for _, tc := range cases {
			got, ok := toFloat(tc.val)
			if ok != tc.ok {
				t.Errorf("toFloat(%v): expected ok=%v, got %v", tc.val, tc.ok, ok)
			}
			if ok && got != tc.expected {
				t.Errorf("toFloat(%v): expected %v, got %v", tc.val, tc.expected, got)
			}
		}
	})

	t.Run("parsePhotosConfig nil cfg", func(t *testing.T) {
		t.Parallel()
		cfg, err := parsePhotosConfig(nil, InitOptions{})
		if err != nil {
			t.Fatalf("unexpected error on nil cfg: %v", err)
		}
		if cfg.CycleIntervalSeconds != defaultPhotoCycleIntervalSeconds {
			t.Errorf("expected default interval %d, got %d", defaultPhotoCycleIntervalSeconds, cfg.CycleIntervalSeconds)
		}
	})

	t.Run("parseMediaItem edge cases", func(t *testing.T) {
		t.Parallel()

		// Not an array
		if _, ok := parseMediaItem("not-an-array"); ok {
			t.Error("expected false for string")
		}

		// Array too short
		if _, ok := parseMediaItem([]any{"id"}); ok {
			t.Error("expected false for array of length 1")
		}

		// mediaInfo not an array
		if _, ok := parseMediaItem([]any{"id", "not-an-array"}); ok {
			t.Error("expected false for non-array mediaInfo")
		}

		// mediaInfo empty
		if _, ok := parseMediaItem([]any{"id", []any{}}); ok {
			t.Error("expected false for empty mediaInfo")
		}

		// media URL not starting with https://lh
		if _, ok := parseMediaItem([]any{"id", []any{"https://example.com/pic.jpg"}}); ok {
			t.Error("expected false for non-lh URL")
		}

		// Empty ID defaults to generated ID
		item, ok := parseMediaItem([]any{"", []any{"https://lh3.googleusercontent.com/pw/TEST", 800, 600}})
		if !ok {
			t.Fatal("expected ok for valid item with empty ID")
		}
		if item.ID == "" {
			t.Error("expected generated ID, got empty")
		}
		if item.AspectRatio != 1.33 {
			t.Errorf("expected 1.33, got %v", item.AspectRatio)
		}
	})

	t.Run("findMediaItems map traversal", func(t *testing.T) {
		t.Parallel()

		nestedMap := map[string]any{
			"wrapper": []any{
				map[string]any{
					"photos": []any{
						[]any{"p1", []any{"https://lh3.googleusercontent.com/pw/N1", 100, 100}},
					},
				},
			},
		}

		items := findMediaItems(nestedMap)
		if len(items) == 0 {
			t.Error("expected items found in nested map, got 0")
		}
	})

	t.Run("extractBalancedArray edge cases", func(t *testing.T) {
		t.Parallel()

		// No bracket
		if _, ok := extractBalancedArray("no brackets here"); ok {
			t.Error("expected false for string without brackets")
		}

		// Escaped characters and strings
		input := `[ "hello \"world\"", 'single \'quote\'', [1, 2, 3] ] and trailing text`
		got, ok := extractBalancedArray(input)
		if !ok {
			t.Fatal("expected ok for balanced array with strings")
		}
		expected := `[ "hello \"world\"", 'single \'quote\'', [1, 2, 3] ]`
		if got != expected {
			t.Errorf("expected %q, got %q", expected, got)
		}

		// Unbalanced bracket
		if _, ok := extractBalancedArray("[ unclosed array"); ok {
			t.Error("expected false for unclosed array")
		}
	})

	t.Run("extractAlbumTitle fallback", func(t *testing.T) {
		t.Parallel()
		title := extractAlbumTitle("<html><body>no title tags</body></html>")
		if title != "Shared Album" {
			t.Errorf("expected 'Shared Album', got %q", title)
		}
	})

	t.Run("cleanGooglePhotoURL without equals", func(t *testing.T) {
		t.Parallel()
		raw := "https://lh3.googleusercontent.com/pw/ABCDEF"
		if got := cleanGooglePhotoURL(raw); got != raw {
			t.Errorf("expected %q, got %q", raw, got)
		}
	})
}

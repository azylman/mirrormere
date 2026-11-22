package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/testutil"
)

const photosBatchexecutePath = "/_/PhotosUi/data/batchexecute"

// newPhotosHTMLServer serves body as text/html at the server root.
func newPhotosHTMLServer(t testing.TB, body string) *httptest.Server {
	t.Helper()
	srv, _ := testutil.RouteServer(t, map[string]testutil.Route{"/": {Body: body, ContentType: "text/html; charset=utf-8"}})
	return srv
}

// photosClosedURL returns the URL of a listener that was started and closed, so
// dialing it fails with connection refused.
func photosClosedURL(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	return "http://" + addr
}

// photosItems builds n distinct photo items with the given ID prefix.
func photosItems(prefix string, n int) []PhotoItem {
	out := make([]PhotoItem, n)
	for i := range n {
		out[i] = PhotoItem{ID: fmt.Sprintf("%s%d", prefix, i), URL: fmt.Sprintf("https://lh3.googleusercontent.com/pw/%s%d", prefix, i)}
	}
	return out
}

func photosErrText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func TestPhotos_ToFloat(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		val    any
		want   float64
		wantOK bool
	}{
		{"float64", 42.5, 42.5, true},
		{"float32", float32(10.5), 10.5, true},
		{"int", 7, 7.0, true},
		{"int64", int64(100), 100.0, true},
		{"json_number", json.Number("3.14"), 3.14, true},
		{"json_number_invalid", json.Number("invalid"), 0, false},
		{"string", "123.45", 123.45, true},
		{"string_invalid", "invalid", 0, false},
		{"bool", true, 0, false},
		{"nil", nil, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got, ok := toFloat(tc.val); ok != tc.wantOK || got != tc.want {
				t.Errorf("toFloat(%v) = (%v, %v), want (%v, %v)", tc.val, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestPhotos_ParsePhotosConfig(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		cfg             map[string]any
		wantSource      string
		wantAlbumURL    string
		wantAlbumURLEnv string
		wantErr         string
	}{
		{name: "nil", cfg: nil, wantErr: "photo-carousel: configuration is required"},
		{name: "missing_source", cfg: map[string]any{}, wantErr: `photo-carousel: 'source' is required and must be either "share_url" or "ember_bridge"`},
		{name: "invalid_source", cfg: map[string]any{"source": "invalid"}, wantErr: `photo-carousel: 'source' is required and must be either "share_url" or "ember_bridge"`},
		{name: "missing_album_url", cfg: map[string]any{"source": "share_url"}, wantErr: "photo-carousel requires either 'album_url' or 'album_url_env'"},
		{
			name:    "url_and_env",
			cfg:     map[string]any{"source": "share_url", "album_url": "https://photos.app.goo.gl/TEST", "album_url_env": "ENV_VAR"},
			wantErr: "photo-carousel: 'album_url' and 'album_url_env' are mutually exclusive",
		},
		{
			name:       "share_url",
			cfg:        map[string]any{"source": "share_url", "album_url": "https://photos.app.goo.gl/TEST"},
			wantSource: "share_url", wantAlbumURL: "https://photos.app.goo.gl/TEST",
		},
		{
			name:       "ember_env",
			cfg:        map[string]any{"source": "ember_bridge", "album_url_env": "ALBUM_ENV"},
			wantSource: "ember_bridge", wantAlbumURLEnv: "ALBUM_ENV",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := parsePhotosConfig(tc.cfg, InitOptions{})
			if got := photosErrText(err); got != tc.wantErr {
				t.Fatalf("err = %q, want %q", got, tc.wantErr)
			}
			if cfg.Source != tc.wantSource || cfg.AlbumURL != tc.wantAlbumURL || cfg.AlbumURLEnv != tc.wantAlbumURLEnv {
				t.Errorf("cfg = %+v, want source=%q url=%q env=%q", cfg, tc.wantSource, tc.wantAlbumURL, tc.wantAlbumURLEnv)
			}
		})
	}
}

func TestPhotos_ParseMediaItem(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		in            any
		wantOK        bool
		wantIDPrefix  string
		wantAspect    float64
		wantColor     string
		wantMediaType string
	}{
		{name: "not_array", in: "not-an-array"},
		{name: "id_only", in: []any{"id"}},
		{name: "info_not_array", in: []any{"id", "not-an-array"}},
		{name: "info_empty", in: []any{"id", []any{}}},
		{name: "info_no_dims", in: []any{"id", []any{"https://example.com/pic.jpg"}}},
		{
			name:   "generated_id_and_color",
			in:     []any{"", []any{"https://lh3.googleusercontent.com/pw/TEST", 800, 600, nil, nil, nil, nil, nil, nil, []any{3549993}}},
			wantOK: true, wantIDPrefix: "photo_", wantAspect: 1.33, wantColor: "#362b29",
		},
		{
			name:   "photo",
			in:     []any{"p1", []any{"https://lh3.googleusercontent.com/pw/P1", 800, 600}, 1755268200000},
			wantOK: true, wantIDPrefix: "p1", wantAspect: 1.33,
		},
		{
			name:   "video_mime",
			in:     []any{"v1", []any{"https://lh3.googleusercontent.com/pw/V1", 1920, 1080}, 1755268200000, []any{"video/mp4", 30000}},
			wantOK: true, wantIDPrefix: "v1", wantAspect: 1.78, wantMediaType: "video",
		},
		{
			name:   "video_extension",
			in:     []any{"v2", []any{"https://lh3.googleusercontent.com/pw/V2", 1920, 1080}, 1755268200000, "clip.mp4"},
			wantOK: true, wantIDPrefix: "v2", wantAspect: 1.78, wantMediaType: "video",
		},
		{
			name:   "gif",
			in:     []any{"g1", []any{"https://lh3.googleusercontent.com/pw/G1", 800, 600}, 1755268200000, map[string]any{"filename": "PXL_20261111-ANIMATION.gif"}},
			wantOK: true, wantIDPrefix: "g1", wantAspect: 1.33, wantMediaType: "gif",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			item, ok := parseMediaItem(tc.in)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (item=%+v)", ok, tc.wantOK, item)
			}
			if !ok {
				return
			}
			if !strings.HasPrefix(item.ID, tc.wantIDPrefix) || item.AspectRatio != tc.wantAspect || item.DominantColor != tc.wantColor || item.MediaType != tc.wantMediaType {
				t.Errorf("item = %+v, want id prefix %q aspect %v color %q media %q", item, tc.wantIDPrefix, tc.wantAspect, tc.wantColor, tc.wantMediaType)
			}
		})
	}
}

func TestPhotos_ExtractDominantColor(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		info []any
		want string
	}{
		{"empty_slice", []any{"u", 1, 1, nil, nil, nil, nil, nil, nil, []any{}}, ""},
		{"negative", []any{"u", 1, 1, nil, nil, nil, nil, nil, nil, -1}, ""},
		{"packed_rgb", []any{"u", 1, 1, nil, nil, nil, nil, nil, nil, []any{3549993}}, "#362b29"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := extractDominantColor(tc.info); got != tc.want {
				t.Errorf("extractDominantColor = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPhotos_IsMediaItemList(t *testing.T) {
	t.Parallel()
	photo := []any{"p1", []any{"https://lh3.googleusercontent.com/pw/P1", 800, 600}, 1755268200000}
	video := []any{"v1", []any{"https://lh3.googleusercontent.com/pw/V1", 1920, 1080}, 1755268200000, []any{"video/mp4", 30000}}
	tests := []struct {
		name string
		list []any
		want bool
	}{
		{"single_photo", []any{photo}, true},
		{"single_video", []any{video}, true},
		{"majority_video", []any{photo, video, video, video}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isMediaItemList(tc.list); got != tc.want {
				t.Errorf("isMediaItemList = %v, want %v", got, tc.want)
			}
			for i, elem := range tc.list {
				if !isPhotoItemArray(elem) {
					t.Errorf("isPhotoItemArray(list[%d]) = false, want true", i)
				}
			}
		})
	}
}

func TestPhotos_CarryCachedPhotoMetadata(t *testing.T) {
	t.Parallel()
	existing := []PhotoItem{{ID: "AF1Qip_cached", URL: "https://lh3.googleusercontent.com/pw/CACHED", Location: "Alpha City", LocationChecked: true, DominantColor: "#112233", MediaType: "video"}}
	fresh := []PhotoItem{{ID: "AF1Qip_cached", URL: "https://lh3.googleusercontent.com/pw/CACHED"}, {ID: "AF1Qip_new1", URL: "https://lh3.googleusercontent.com/pw/NEW1"}}
	carryCachedPhotoMetadata(fresh, existing)
	want := []PhotoItem{existing[0], {ID: "AF1Qip_new1", URL: "https://lh3.googleusercontent.com/pw/NEW1"}}
	if !slices.Equal(fresh, want) {
		t.Errorf("fresh = %+v, want %+v", fresh, want)
	}
	if !hasUncheckedPhotoLocations(fresh) {
		t.Error("hasUncheckedPhotoLocations = false, want true")
	}
}

func TestPhotos_EnrichPhotoLocations(t *testing.T) {
	t.Parallel()
	const fdcnBody = ")]}'\n\n120\n[[[\"wrb.fr\",\"fDcn4b\",\"[[\\\"AF1Qip_new1\\\",null,\\\"PXL_ANIMATION.gif\\\",null,null,null,null,null,null,null,null,null,null,[null,null,[[null,[[\\\"Beta Town\\\",null,1]],\\\"ChIJ123\\\"]]]]]\",null,null,null,\"1\"],[\"wrb.fr\",\"fDcn4b\",\"[[\\\"AF1Qip_noloc\\\",null,\\\"clip.mp4\\\"]]\",null,null,null,\"2\"],[\"wrb.fr\",\"fDcn4b\",\"[null]\",null,null,null,\"3\"]]]\n"
	srv, _ := testutil.RouteServer(t, map[string]testutil.Route{photosBatchexecutePath: {Body: fdcnBody, ContentType: "text/plain; charset=utf-8"}})
	items := []PhotoItem{
		{ID: "AF1Qip_new1", URL: "https://lh3.googleusercontent.com/pw/NEW1"},
		{ID: "AF1Qip_noloc", URL: "https://lh3.googleusercontent.com/pw/NOLOC"},
		{ID: "AF1Qip_omitted", URL: "https://lh3.googleusercontent.com/pw/OMITTED"},
		{ID: "photo_positional", URL: "https://lh3.googleusercontent.com/pw/POS"},
	}
	tests := []struct {
		name          string
		albumURL      string
		want          []PhotoItem
		wantUnchecked bool
	}{
		{
			name:     "batch",
			albumURL: srv.URL + "?key=auth123",
			want: []PhotoItem{
				{ID: "AF1Qip_new1", URL: "https://lh3.googleusercontent.com/pw/NEW1", Location: "Beta Town", LocationChecked: true, MediaType: "gif"},
				{ID: "AF1Qip_noloc", URL: "https://lh3.googleusercontent.com/pw/NOLOC", LocationChecked: true, MediaType: "video"},
				{ID: "AF1Qip_omitted", URL: "https://lh3.googleusercontent.com/pw/OMITTED", LocationChecked: true},
				{ID: "photo_positional", URL: "https://lh3.googleusercontent.com/pw/POS"},
			},
		},
		{name: "network_error", albumURL: photosClosedURL(t), want: items, wantUnchecked: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := slices.Clone(items)
			enrichPhotoLocations(context.Background(), srv.Client(), tc.albumURL, "auth123", got, slog.Default())
			if !slices.Equal(got, tc.want) {
				t.Errorf("items = %+v, want %+v", got, tc.want)
			}
			if hasUncheckedPhotoLocations(got) != tc.wantUnchecked {
				t.Errorf("hasUncheckedPhotoLocations = %v, want %v", !tc.wantUnchecked, tc.wantUnchecked)
			}
		})
	}
}

func TestPhotos_ParseSingleFDcn4bEnvelope(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		inner string
	}{
		{"empty_inner", ""},
		{"invalid_json", "invalid"},
		{"info_not_slice", `["not-slice"]`},
		{"id_not_string", `[[123]]`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, _, _, ok := parseSingleFDcn4bEnvelope([]any{"wrb.fr", "fDcn4b", tc.inner}); ok {
				t.Error("ok = true, want false")
			}
		})
	}
}

func TestPhotos_ParseFDcn4bResponse(t *testing.T) {
	t.Parallel()
	locs, mediaTypes, checked := parseFDcn4bResponse([]byte(`)]}'
[[["wrb.fr","fDcn4b","[[\"AF1Qip_single\",null,\"burst-ANIMATION.gif\",null,null,null,null,null,null,null,null,null,null,[\"https://maps\",\"0x123\",\"ChIJabc\",\"Gamma City\"]]]",null,null,null,"1"]]]`))
	if !checked["AF1Qip_single"] || locs["AF1Qip_single"] != "Gamma City" || mediaTypes["AF1Qip_single"] != "gif" {
		t.Errorf("locs=%v mediaTypes=%v checked=%v", locs, mediaTypes, checked)
	}
}

func TestPhotos_ResolveBatchexecuteBaseURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, in, want string
	}{
		{"shortlink", "https://photos.app.goo.gl/abc123", "https://photos.google.com"},
		{"direct_host", "http://127.0.0.1:8080/album?key=1", "http://127.0.0.1:8080"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := resolveBatchexecuteBaseURL(tc.in); got != tc.want {
				t.Errorf("resolveBatchexecuteBaseURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestPhotos_FindMediaItems(t *testing.T) {
	t.Parallel()
	nested := map[string]any{"wrapper": []any{map[string]any{"photos": []any{[]any{"p1", []any{"https://lh3.googleusercontent.com/pw/N1", 100, 100}}}}}}
	if got := findMediaItems(nested); len(got) != 1 {
		t.Errorf("findMediaItems found %d lists, want 1", len(got))
	}
}

func TestPhotos_ExtractBalancedArray(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		in     string
		want   string
		wantOK bool
	}{
		{"no_brackets", "no brackets", "", false},
		{"unclosed", "[ unclosed", "", false},
		{"quotes_and_nesting", `[ "hello \"world\"", 'single \'quote\'', [1, 2, 3] ] tail`, `[ "hello \"world\"", 'single \'quote\'', [1, 2, 3] ]`, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got, ok := extractBalancedArray(tc.in); got != tc.want || ok != tc.wantOK {
				t.Errorf("extractBalancedArray = (%q, %v), want (%q, %v)", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestPhotos_ExtractAlbumTitle(t *testing.T) {
	t.Parallel()
	if title := extractAlbumTitle("<html><body>no title</body></html>"); title != "Shared Album" {
		t.Errorf("extractAlbumTitle = %q, want Shared Album", title)
	}
}

func TestPhotos_CleanGooglePhotoURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, in, want string
	}{
		{"no_size_suffix", "https://lh3.googleusercontent.com/pw/ABCDEF", "https://lh3.googleusercontent.com/pw/ABCDEF"},
		{"not_googleusercontent", "https://example.com/not-lh", "https://example.com/not-lh"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := cleanGooglePhotoURL(tc.in); got != tc.want {
				t.Errorf("cleanGooglePhotoURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestPhotos_PhotoItemKey(t *testing.T) {
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
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := photoItemKey(tc.item); got != tc.want {
				t.Errorf("photoItemKey = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPhotos_PhotoDedupeKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		item PhotoItem
		want string
	}{
		{"clean_url_preferred", PhotoItem{ID: "photo_1", URL: "https://lh3.googleusercontent.com/pw/ABC=w100"}, "https://lh3.googleusercontent.com/pw/ABC"},
		{"empty_url_custom_id", PhotoItem{ID: "custom_uuid", URL: ""}, "custom_uuid"},
		{"empty_url_positional_id", PhotoItem{ID: "photo_99", URL: ""}, "photo_99"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := photoDedupeKey(tc.item); got != tc.want {
				t.Errorf("photoDedupeKey = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestPhotos_ReconcilePhotoDeck walks one deck through add, update and remove
// steps; each step depends on the order and cursor the previous one left.
func TestPhotos_ReconcilePhotoDeck(t *testing.T) {
	t.Parallel()
	p := NewPhotosProvider()
	r := rand.New(rand.NewSource(42))
	initial := []PhotoItem{{ID: "1", URL: "u1"}, {ID: "2", URL: "u2"}, {ID: "3", URL: "u3"}}
	p.reconcilePhotoDeck(initial, r)
	if len(p.deck) != 3 || p.deckCursor != 0 {
		t.Fatalf("deck len=%d cursor=%d, want 3, 0", len(p.deck), p.deckCursor)
	}
	initialOrder := make([]string, len(p.deck))
	for i, it := range p.deck {
		initialOrder[i] = it.ID
	}

	withAdded := append(slices.Clone(initial), PhotoItem{ID: "4", URL: "u4"})
	p.reconcilePhotoDeck(withAdded, r)
	if len(p.deck) != 4 {
		t.Fatalf("deck len = %d, want 4", len(p.deck))
	}
	for i := range 3 {
		if p.deck[i].ID != initialOrder[i] {
			t.Errorf("deck[%d] = %q, want %q", i, p.deck[i].ID, initialOrder[i])
		}
	}

	withUpdated := slices.Clone(withAdded)
	withUpdated[0].AspectRatio = 2.5
	p.reconcilePhotoDeck(withUpdated, r)
	if !slices.ContainsFunc(p.deck, func(it PhotoItem) bool { return it.ID == withUpdated[0].ID && it.AspectRatio == 2.5 }) {
		t.Error("updated metadata not reflected in deck")
	}

	p.deckCursor = 2
	p.reconcilePhotoDeck([]PhotoItem{withAdded[1], withAdded[2], withAdded[3]}, r)
	if p.deckCursor != 1 {
		t.Errorf("cursor after removal before it = %d, want 1", p.deckCursor)
	}
	p.deckCursor = 1
	p.reconcilePhotoDeck([]PhotoItem{withAdded[1], withAdded[2]}, r)
	if p.deckCursor != 1 {
		t.Errorf("cursor after removal after it = %d, want 1", p.deckCursor)
	}
}

// TestPhotos_ReconcilePhotoDeckPerDay checks per-day burst de-biasing and
// multi-album pairing, then that a second reconcile keeps the chosen items.
func TestPhotos_ReconcilePhotoDeckPerDay(t *testing.T) {
	t.Parallel()
	laLoc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	p := NewPhotosProvider()
	p.location = laLoc
	dated := []PhotoItem{
		// 2026-08-01 in America/Los_Angeles (evening PDT = early UTC next day)
		{ID: "a1", URL: "https://lh3.googleusercontent.com/pw/A1", Timestamp: "2026-08-01T18:00:00Z", AlbumName: "Album A"},
		{ID: "a2", URL: "https://lh3.googleusercontent.com/pw/A2", Timestamp: "2026-08-02T02:00:00Z", AlbumName: "Album A"},
		{ID: "b1", URL: "https://lh3.googleusercontent.com/pw/B1", Timestamp: "2026-08-01T20:00:00Z", AlbumName: "Album B"},
		// 2026-08-02 in America/Los_Angeles
		{ID: "a3", URL: "https://lh3.googleusercontent.com/pw/A3", Timestamp: "2026-08-02T19:00:00Z", AlbumName: "Album A"},
	}
	p.reconcilePhotoDeck(dated, nil)
	if len(p.deck) != 3 {
		t.Fatalf("deck len = %d, want 3 (2 for 2026-08-01 across 2 albums, 1 for 2026-08-02): %+v", len(p.deck), p.deck)
	}
	firstDeck := slices.Clone(p.deck)
	p.reconcilePhotoDeck(dated, rand.New(rand.NewSource(42)))
	if !slices.Equal(p.deck, firstDeck) {
		t.Errorf("deck after second reconcile = %+v, want %+v", p.deck, firstDeck)
	}
}

func TestPhotos_PhotoDayKey(t *testing.T) {
	t.Parallel()
	laLoc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	tests := []struct {
		name string
		ts   string
		loc  *time.Location
		want string
	}{
		{"utc_when_nil", "2026-08-02T02:00:00Z", nil, "2026-08-02"},
		{"local_day", "2026-08-02T02:00:00Z", laLoc, "2026-08-01"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := photoDayKey(PhotoItem{ID: "d1", URL: "u", Timestamp: tc.ts}, tc.loc); got != tc.want {
				t.Errorf("photoDayKey = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPhotos_SelectAlbumWindow(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		items       int
		seed        int64
		deck        int
		cursor      int
		calls       int
		wantLen     int
		wantDeckLen int
		wantCursor  int
		wantStable  bool
	}{
		{name: "single_resets_deck", items: 1, deck: 1, cursor: 1, calls: 1, wantLen: 1},
		{name: "fits_preload_stable", items: 4, seed: 99, calls: 2, wantLen: 4, wantDeckLen: 4, wantStable: true},
		{name: "negative_cursor", items: 5, deck: 5, cursor: -5, calls: 1, wantLen: 5, wantDeckLen: 5},
		{name: "cursor_at_end", items: 5, deck: 5, cursor: 4, calls: 1, wantLen: 5, wantDeckLen: 5},
		{name: "capped_wraps", items: 600, seed: 100, calls: 2, wantLen: defaultPhotoPreloadCount, wantDeckLen: 600, wantCursor: 500},
		{name: "exact_multiple", items: 1000, seed: 200, calls: 2, wantLen: defaultPhotoPreloadCount, wantDeckLen: 1000},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			items := photosItems("id-", tc.items)
			p := NewPhotosProvider()
			if tc.seed != 0 {
				p.randSource = rand.New(rand.NewSource(tc.seed))
			}
			if tc.deck > 0 {
				p.deck = slices.Clone(items[:tc.deck])
				p.deckCursor = tc.cursor
			}
			var prev, win []PhotoItem
			for range tc.calls {
				prev, win = win, p.selectAlbumWindow(items)
			}
			if len(win) != tc.wantLen || len(p.deck) != tc.wantDeckLen || p.deckCursor != tc.wantCursor {
				t.Errorf("window=%d deck=%d cursor=%d, want %d, %d, %d", len(win), len(p.deck), p.deckCursor, tc.wantLen, tc.wantDeckLen, tc.wantCursor)
			}
			if tc.wantStable && !slices.Equal(prev, win) {
				t.Errorf("window changed between calls: %v then %v", prev, win)
			}
		})
	}
}

func TestPhotosProvider_ShutdownResetsDeck(t *testing.T) {
	t.Parallel()
	p := NewPhotosProvider()
	p.deck, p.deckCursor, p.cachedSnapshot = []PhotoItem{{ID: "1", URL: "u1"}}, 1, &PhotoSnapshot{}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if p.deck != nil || p.deckCursor != 0 || p.cachedSnapshot != nil {
		t.Errorf("deck=%v cursor=%d cached=%v, want reset", p.deck, p.deckCursor, p.cachedSnapshot)
	}
}

func TestPhotosProvider_InitResetsDeck(t *testing.T) {
	t.Parallel()
	p := NewPhotosProvider()
	p.deck, p.deckCursor, p.cachedSnapshot = []PhotoItem{{ID: "2", URL: "u2"}}, 1, &PhotoSnapshot{}
	if err := p.Init(context.Background(), map[string]any{"source": "share_url", "album_url": "https://example.com/album"}, InitOptions{}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if p.deck != nil || p.deckCursor != 0 || p.cachedSnapshot != nil {
		t.Errorf("deck=%v cursor=%d cached=%v, want reset", p.deck, p.deckCursor, p.cachedSnapshot)
	}
}

type photosPush struct {
	urls  []string
	album string
}

func TestPhotosProvider_IngestPushedPhotos(t *testing.T) {
	t.Parallel()
	emberCfg := map[string]any{"source": "ember_bridge", "album_url": "https://photos.app.goo.gl/ember"}
	mixedCfg := map[string]any{"albums": []any{
		map[string]any{"name": "Shared 1", "source": "share_url", "url": "https://photos.app.goo.gl/share1"},
		map[string]any{"name": "Pushed 1", "source": "ember_bridge", "url": "https://photos.app.goo.gl/ember1"},
	}}
	validURLs := []string{"https://lh3.googleusercontent.com/pw/VALID1=w1024-h768", "https://lh3.googleusercontent.com/pw/VALID1", "https://lh4.googleusercontent.com/pw/VALID2"}
	tests := []struct {
		name          string
		cfg           map[string]any
		seedFirst     []PhotoItem
		pushes        []photosPush
		wantErr       error
		wantAlbumName string
		wantTotal     int
		wantAlbumURL  string
	}{
		{
			name:    "share_url_rejects_push",
			cfg:     map[string]any{"source": "share_url", "album_url": "https://photos.app.goo.gl/share"},
			pushes:  []photosPush{{[]string{"https://lh3.googleusercontent.com/pw/VALID"}, "Test"}},
			wantErr: ErrPhotoPushNotSupported,
		},
		{name: "empty_payload", cfg: emberCfg, pushes: []photosPush{{nil, "Empty"}}, wantErr: ErrInvalidPhotoPushPayload},
		{name: "bad_url", cfg: emberCfg, pushes: []photosPush{{[]string{"https://notgoogle.com/pic.jpg"}, "BadURL"}}, wantErr: ErrInvalidPhotoPushPayload},
		{
			name: "dedupes_urls", cfg: emberCfg, pushes: []photosPush{{validURLs, "Custom Album"}},
			wantAlbumName: "Custom Album", wantTotal: 2, wantAlbumURL: "https://photos.app.goo.gl/ember",
		},
		{
			name: "blank_name_keeps_cached", cfg: emberCfg,
			pushes:        []photosPush{{validURLs, "Custom Album"}, {[]string{"https://lh3.googleusercontent.com/pw/VALID3"}, "  "}},
			wantAlbumName: "Custom Album", wantTotal: 1, wantAlbumURL: "https://photos.app.goo.gl/ember",
		},
		{
			name: "mixed_albums", cfg: mixedCfg, seedFirst: []PhotoItem{{ID: "share_p1", URL: "https://lh3.googleusercontent.com/pw/SHARE1"}},
			pushes:        []photosPush{{[]string{"https://lh3.googleusercontent.com/pw/PUSHED1"}, ""}},
			wantAlbumName: "Shared 1, Pushed 1", wantTotal: 2, wantAlbumURL: "https://photos.app.goo.gl/ember1",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := NewPhotosProvider()
			if err := p.Init(context.Background(), tc.cfg, InitOptions{}); err != nil {
				t.Fatalf("Init: %v", err)
			}
			if tc.seedFirst != nil {
				p.albums[0].photos, p.albums[0].hydrated = tc.seedFirst, true
			}
			var snap *PhotoSnapshot
			var err error
			for _, push := range tc.pushes {
				snap, err = p.IngestPushedPhotos(push.urls, push.album)
			}
			if !errors.Is(err, tc.wantErr) || (err != nil) != (tc.wantErr != nil) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if err != nil {
				return
			}
			if snap.AlbumName != tc.wantAlbumName || snap.TotalPhotos != tc.wantTotal || len(snap.Photos) != tc.wantTotal || snap.AlbumURL != tc.wantAlbumURL {
				t.Errorf("snap = %+v, want name %q total %d url %q", snap, tc.wantAlbumName, tc.wantTotal, tc.wantAlbumURL)
			}
		})
	}
}

func TestPhotosProvider_UninitializedErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		cfg    PhotosConfig
		albums []*albumState
	}{
		{name: "zero_provider"},
		{name: "ember_cfg_no_albums", cfg: PhotosConfig{Source: PhotoSourceEmberBridge}},
		{name: "ember_album_no_snapshot", albums: []*albumState{{cfg: AlbumSource{Source: PhotoSourceEmberBridge}}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := NewPhotosProvider()
			p.cfg, p.albums = tc.cfg, tc.albums
			if _, err := p.Fetch(context.Background()); photosErrText(err) != "photo-carousel: provider not initialized" {
				t.Errorf("Fetch err = %v, want provider not initialized", err)
			}
		})
	}
}

func TestPhotosProvider_Source(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		albums []*albumState
		want   string
	}{
		{"no_albums", nil, ""},
		{"unknown_source", []*albumState{{cfg: AlbumSource{Source: "other_source"}}}, ""},
		{"ember_bridge", []*albumState{{cfg: AlbumSource{Source: PhotoSourceEmberBridge}}}, PhotoSourceEmberBridge},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := NewPhotosProvider()
			p.albums = tc.albums
			if got := p.Source(); got != tc.want {
				t.Errorf("Source = %q, want %q", got, tc.want)
			}
			if got, want := p.isAllEmberBridgeLocked(), tc.want == PhotoSourceEmberBridge; got != want {
				t.Errorf("isAllEmberBridgeLocked = %v, want %v", got, want)
			}
		})
	}
}

func TestPhotos_ResolveSingleAlbumURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		idx     int
		src     AlbumSource
		legacy  bool
		secrets map[string]string
		want    string
		wantErr string
	}{
		{name: "unset_env", src: AlbumSource{URLEnv: "UNSET_VAR_XYZ"}, wantErr: `photo-carousel: environment variable "UNSET_VAR_XYZ" specified in "url_env" is not set or empty`},
		{name: "invalid_url", src: AlbumSource{URL: "not-a-valid-url"}, wantErr: `photo-carousel: invalid album URL "not-a-valid-url"`},
		{name: "malformed_port", src: AlbumSource{URL: "http://[::1]:namedport"}, wantErr: `photo-carousel: invalid album URL "http://[::1]:namedport"`},
		{name: "empty", wantErr: "photo-carousel: resolved album URL is empty"},
		{name: "env_secret", src: AlbumSource{URLEnv: "SECRET_VAR"}, secrets: map[string]string{"SECRET_VAR": "https://photos.app.goo.gl/secret"}, want: "https://photos.app.goo.gl/secret"},
		{name: "legacy_album_url", src: AlbumSource{URL: "https://example.com/a"}, legacy: true, secrets: map[string]string{"album_url": "https://example.com/secret_album"}, want: "https://example.com/secret_album"},
		{name: "legacy_share_url", src: AlbumSource{URL: "https://example.com/a"}, legacy: true, secrets: map[string]string{"share_url": "https://example.com/secret_share"}, want: "https://example.com/secret_share"},
		{
			name: "legacy_share_url_named", src: AlbumSource{Name: "Shared Album", Source: "share_url", URL: "https://example.com/a"}, legacy: true,
			secrets: map[string]string{"share_url": "https://example.com/legacy_share"}, want: "https://example.com/legacy_share",
		},
		{
			name: "indexed_secret", idx: 2, src: AlbumSource{Name: "Alb", Source: "share_url", URLEnv: "SOME_ENV"},
			secrets: map[string]string{"albums[2].url": "https://example.com/indexed"}, want: "https://example.com/indexed",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := resolveSingleAlbumURL(tc.idx, tc.src, tc.legacy, InitOptions{Secrets: tc.secrets})
			if got != tc.want || photosErrText(err) != tc.wantErr {
				t.Errorf("resolveSingleAlbumURL = (%q, %v), want (%q, %q)", got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestPhotos_ResolveSnapshotAlbumName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		albums []*albumState
		want   string
	}{
		{"nil", nil, "Shared Album"},
		{"blank_names", []*albumState{{cfg: AlbumSource{Name: "  "}}}, "Shared Album"},
		{"dedupes", []*albumState{{cfg: AlbumSource{Name: "Vacation"}}, {cfg: AlbumSource{Name: "Vacation"}}, {cfg: AlbumSource{Name: "Family"}}}, "Vacation, Family"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := resolveSnapshotAlbumName(tc.albums); got != tc.want {
				t.Errorf("resolveSnapshotAlbumName = %q, want %q", got, tc.want)
			}
			if got := resolveSnapshotAlbumURL(tc.albums); got != "" {
				t.Errorf("resolveSnapshotAlbumURL = %q, want empty", got)
			}
		})
	}
}

func TestPhotosProvider_PeekWindowLocked(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		albumPhotos int
		deck        int
		cursor      int
		want        int
	}{
		{"empty_deck_small_album", 2, 0, 0, 2},
		{"empty_deck_capped", 600, 0, 0, defaultPhotoPreloadCount},
		{"deck_wraps", 0, 3, 2, 3},
		{"deck_cursor_past_end", 0, 10, 20, 10},
		{"deck_sliced_wrap", 0, 10, 7, 10},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := NewPhotosProvider()
			p.albums = []*albumState{{cfg: AlbumSource{Name: "A1"}, photos: photosItems("a", tc.albumPhotos)}}
			if tc.deck > 0 {
				p.deck, p.deckCursor = photosItems("d", tc.deck), tc.cursor
			}
			if got := p.peekWindowLocked(); len(got) != tc.want {
				t.Errorf("peekWindowLocked len = %d, want %d", len(got), tc.want)
			}
		})
	}
}

func TestPhotosProvider_BuildSnapshotLocked(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		albums     []*albumState
		wantPhotos int
		wantName   string
	}{
		{"no_albums", nil, 0, "Shared Album"},
		{"empty_window_falls_back_to_album", []*albumState{{cfg: AlbumSource{Name: "Shared Album"}, photos: photosItems("p", 1)}}, 1, "Shared Album"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := NewPhotosProvider()
			p.albums = tc.albums
			snap := p.buildSnapshotLocked(nil)
			if len(snap.Photos) != tc.wantPhotos || snap.AlbumName != tc.wantName {
				t.Errorf("snap = %+v, want %d photos named %q", snap, tc.wantPhotos, tc.wantName)
			}
		})
	}
}

// preparePhotosCacheTarget pre-creates a corrupt cache file or a directory at
// the cache file path before the write under test.
func preparePhotosCacheTarget(t *testing.T, dir, target string, corrupt, block bool) {
	t.Helper()
	if !corrupt && !block {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if corrupt {
		if err := os.WriteFile(target, []byte("invalid json"), 0o600); err != nil {
			t.Fatalf("write corrupt cache: %v", err)
		}
	}
	if block {
		if err := os.Mkdir(target, 0o755); err != nil {
			t.Fatalf("mkdir target: %v", err)
		}
	}
}

func TestPhotos_AlbumDiskCache(t *testing.T) {
	t.Parallel()
	const url = "http://example.com/album"
	tests := []struct {
		name          string
		dir           string
		cache         *albumDiskCache
		corrupt       bool
		blockTarget   bool
		wantErr       string
		wantAlbumName string
	}{
		{name: "no_dir", dir: "", cache: &albumDiskCache{AlbumName: "x"}, wantErr: os.ErrNotExist.Error()},
		{name: "nil_cache", dir: "nil", wantErr: "no such file or directory"},
		{name: "mkdir_fails", dir: "/dev/null/cannot/create", cache: &albumDiskCache{}, wantErr: "not a directory"},
		{name: "corrupt_file", dir: "corrupt", corrupt: true, wantErr: "invalid character"},
		{name: "rename_onto_directory", dir: "blocked", cache: &albumDiskCache{AlbumName: "blocked"}, blockTarget: true, wantErr: "is a directory"},
		{name: "round_trip", dir: "valid", cache: &albumDiskCache{AlbumName: "Saved", Photos: photosItems("s", 1)}, wantAlbumName: "Saved"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := tc.dir
			if dir != "" && !filepath.IsAbs(dir) {
				dir = filepath.Join(t.TempDir(), dir)
			}
			preparePhotosCacheTarget(t, dir, filepath.Join(dir, albumCacheFilename("share_url", url)), tc.corrupt, tc.blockTarget)
			writeAlbumDiskCache(dir, "share_url", url, tc.cache, slog.Default())
			loaded, err := loadAlbumDiskCache(dir, "share_url", url)
			if (tc.wantErr == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("load err = %v, want containing %q", err, tc.wantErr)
			}
			if err == nil && loaded.AlbumName != tc.wantAlbumName {
				t.Errorf("loaded album = %q, want %q", loaded.AlbumName, tc.wantAlbumName)
			}
		})
	}
}

// TestPhotos_WriteAlbumDiskCacheReadOnlyDir drives the temp-file failure path.
// Root can write into a 0500 directory, so the outcome is not asserted; the
// call must only return without panicking.
func TestPhotos_WriteAlbumDiskCacheReadOnlyDir(t *testing.T) {
	t.Parallel()
	roDir := filepath.Join(t.TempDir(), "readonly")
	if err := os.Mkdir(roDir, 0o500); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeAlbumDiskCache(roDir, "share_url", "https://example.com/ro", &albumDiskCache{AlbumName: "ro"}, slog.Default())
}

func TestPhotos_ParseAlbumsList(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		raw     any
		wantErr string
	}{
		{"nil", nil, "photo-carousel: 'albums' must contain at least one album"},
		{"not_map", []any{"not-a-map"}, "photo-carousel: album at index 0 must be an object"},
		{"blank_name", []any{map[string]any{"name": "   "}}, "photo-carousel: album at index 0 requires non-empty 'name'"},
		{"invalid_source", []any{map[string]any{"name": "Alb", "source": "invalid"}}, `photo-carousel: 'source' must be either "share_url" or "ember_bridge"`},
		{"url_and_env", []any{map[string]any{"name": "Alb", "url": "https://p.app/1", "url_env": "E"}}, `photo-carousel: 'url' and 'url_env' are mutually exclusive in album "Alb"`},
		{"no_url", []any{map[string]any{"name": "Alb", "source": "share_url"}}, `photo-carousel: album "Alb" requires either 'url' or 'url_env'`},
		{"no_source_no_url", []any{map[string]any{"name": "Alb"}}, `photo-carousel: album "Alb" requires 'source'`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := parseAlbumsList(tc.raw); photosErrText(err) != tc.wantErr {
				t.Errorf("err = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestPhotos_ResolveAlbumSources(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		albums  []AlbumSource
		secrets map[string]string
		wantURL string
		wantErr string
	}{
		{name: "no_albums", wantErr: "photo-carousel: no albums configured"},
		{
			name:    "unset_env",
			albums:  []AlbumSource{{Name: "Alb", Source: "share_url", URLEnv: "UNSET_ALBUM_ENV"}},
			wantErr: `photo-carousel: environment variable "UNSET_ALBUM_ENV" specified in "url_env" is not set or empty`,
		},
		{
			name:    "indexed_secret_single_shared_album",
			albums:  []AlbumSource{{Name: "Shared Album", Source: "share_url", URLEnv: "SOME_UNSET_ENV"}},
			secrets: map[string]string{"albums[0].url": "https://photos.app.goo.gl/indexed_shared_secret"},
			wantURL: "https://photos.app.goo.gl/indexed_shared_secret",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := resolveAlbumSources(PhotosConfig{Albums: tc.albums}, InitOptions{Secrets: tc.secrets})
			if photosErrText(err) != tc.wantErr {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
			if err == nil && (len(got) != 1 || got[0].ResolvedURL != tc.wantURL) {
				t.Errorf("resolved = %+v, want one album at %q", got, tc.wantURL)
			}
		})
	}
}

func TestPhotosProvider_SyncErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		cfg     map[string]any
		wantErr string
	}{
		{"uninitialized", nil, "photo-carousel: provider not initialized"},
		{"unhydrated_dial_failure", map[string]any{"source": "share_url", "album_url": photosClosedURL(t)}, "photo-carousel scrape failed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := NewPhotosProvider()
			if tc.cfg != nil {
				if err := p.Init(context.Background(), tc.cfg, InitOptions{}); err != nil {
					t.Fatalf("Init: %v", err)
				}
			}
			if err := p.Sync(context.Background(), true); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Sync err = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestPhotos_ExtractAlbumKeyAndToken(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, html, wantKey, wantToken string
	}{
		{"no_data", "AF_initDataCallback({key: '1', some_other_field: 'val'});", "", ""},
		{"invalid_data", "AF_initDataCallback({data: [invalid json, foo]});", "", ""},
		{"key_and_token", `AF_initDataCallback({key: 'ds:1', data: [null, [], "tok_next", ["AF1Qip_album_meta_key", "Vacation"]]});`, "AF1Qip_album_meta_key", "tok_next"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if k, tok := extractAlbumKeyAndToken(tc.html); k != tc.wantKey || tok != tc.wantToken {
				t.Errorf("extractAlbumKeyAndToken = (%q, %q), want (%q, %q)", k, tok, tc.wantKey, tc.wantToken)
			}
		})
	}
}

func TestPhotos_ParseBatchexecuteResponse(t *testing.T) {
	t.Parallel()
	body := []byte(")]}'\n\n120\n[[[\"wrb.fr\",\"snAcKc\",\"[null,[[\\\"p_ml\\\",[\\\"https://lh3.googleusercontent.com/pw/ML1\\\",1000,800],1755268100000]],\\\"tok_ml2\\\"]\",null,null,null,\"generic\"]]]\n")
	photos, next, err := parseBatchexecuteResponse(body, "tok_ml1")
	if err != nil || len(photos) != 1 || photos[0].ID != "p_ml" || next != "tok_ml2" {
		t.Errorf("parseBatchexecuteResponse = (%v, %q, %v), want one p_ml photo and tok_ml2", photos, next, err)
	}
}

func TestPhotos_FetchBatchexecutePage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		route   *testutil.Route
		wantErr string
	}{
		{name: "dial_error", wantErr: "connect: connection refused"},
		{name: "http_500", route: &testutil.Route{Status: 500, Body: "server error"}, wantErr: "batchexecute returned HTTP 500"},
		{name: "payload_ceiling", route: &testutil.Route{Body: strings.Repeat("A", 6*1024*1024), ContentType: "text/plain"}, wantErr: "batchexecute payload exceeded"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			albumURL := photosClosedURL(t)
			if tc.route != nil {
				srv, _ := testutil.RouteServer(t, map[string]testutil.Route{photosBatchexecutePath: *tc.route})
				albumURL = srv.URL
			}
			_, _, err := fetchBatchexecutePage(context.Background(), &http.Client{}, albumURL, "key", "token", "auth")
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestPhotosProvider_InitCacheDir(t *testing.T) {
	t.Parallel()
	p := NewPhotosProvider()
	dir := t.TempDir()
	if err := p.Init(context.Background(), map[string]any{"source": "ember_bridge", "album_url": "https://photos.app.goo.gl/custom", "cache_dir": dir}, InitOptions{}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if p.effectiveCacheDir != dir {
		t.Errorf("effectiveCacheDir = %q, want %q", p.effectiveCacheDir, dir)
	}
}

func TestPhotosProvider_CheckWarmBootStaleLocked(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name          string
		page1Age      time.Duration
		paginated     bool
		checked       bool
		wantStale     bool
		wantNeedsFull bool
	}{
		{"page1_stale_only", 2 * time.Hour, true, true, true, false},
		{"unpaginated_backfill", 0, false, true, true, true},
		{"unchecked_location_backfill", 0, true, false, true, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := NewPhotosProvider()
			p.albums = []*albumState{{
				cfg: AlbumSource{Source: PhotoSourceShareURL}, lastFullSync: now, lastPage1Sync: now.Add(-tc.page1Age),
				paginated: tc.paginated, photos: []PhotoItem{{ID: "AF1Qip_x", LocationChecked: tc.checked}},
			}}
			if stale, full := p.checkWarmBootStaleLocked(now); stale != tc.wantStale || full != tc.wantNeedsFull {
				t.Errorf("checkWarmBootStaleLocked = (%v, %v), want (%v, %v)", stale, full, tc.wantStale, tc.wantNeedsFull)
			}
		})
	}
}

func TestPhotosProvider_CheckPeriodicFullLocked(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		paginated bool
		elapsed   time.Duration
		want      bool
	}{
		{"unpaginated", false, 0, true},
		{"fresh_paginated", true, 0, false},
		{"past_full_interval", true, 25 * time.Hour, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := NewPhotosProvider()
			p.albums = []*albumState{{cfg: AlbumSource{Source: PhotoSourceShareURL}, lastFullSync: now, paginated: tc.paginated}}
			if got := p.checkPeriodicFullLocked(now.Add(tc.elapsed)); got != tc.want {
				t.Errorf("checkPeriodicFullLocked = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestPhotosProvider_ConcurrentFetchSync keeps Fetch and background Sync racing
// on one hydrated provider so -race can catch unguarded state.
func TestPhotosProvider_ConcurrentFetchSync(t *testing.T) {
	t.Parallel()
	srv := newPhotosHTMLServer(t, `<html><head><title>Test Album - Google Photos</title></head><body><script>AF_initDataCallback({data: [null, [["p1", ["https://lh3.googleusercontent.com/pw/CONCURRENT1", 100, 100], 12345]]]})</script></body></html>`)
	p := NewPhotosProvider()
	if err := p.Init(context.Background(), map[string]any{"albums": []any{map[string]any{"name": "Album 1", "source": "share_url", "url": srv.URL}}}, InitOptions{}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := p.Sync(context.Background(), true); err != nil {
		t.Fatalf("initial Sync: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			for ctx.Err() == nil {
				_, _ = p.Fetch(ctx)
			}
		})
		wg.Go(func() {
			for ctx.Err() == nil {
				_ = p.Sync(ctx, false)
			}
		})
	}
	wg.Wait()
}

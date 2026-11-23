package provider_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/provider"
	"github.com/azylman/mirrormere/internal/testutil"
)

const (
	mockPhotosAFHTML            = `<!DOCTYPE html><html><head><meta property="og:title" content="Family Vacation 2026 - Google Photos"><title>Family Vacation 2026 - Google Photos</title></head><body><script>AF_initDataCallback({key:'ds:1',hash:'2',data:["k",[["photo_1",["https://lh3.googleusercontent.com/pw/AL9nZEW1=w1024-h768",1200,800],1755268200000],["photo_2",["https://lh3.googleusercontent.com/pw/AL9nZEW2",1920,1080],1755354600000],["photo_3",["https://lh3.googleusercontent.com/pw/AL9nZEW3=s0",1000,1000],1755441000000]]],sideChannel:{}});</script></body></html>`
	mockPhotosWithVideoHTML     = `<!DOCTYPE html><html><head><meta property="og:title" content="Family Vacation 2026 - Google Photos"><title>Family Vacation 2026 - Google Photos</title></head><body><script>AF_initDataCallback({key:'ds:1',hash:'2',data:["k",[["photo_1",["https://lh3.googleusercontent.com/pw/AL9nZEW1=w1024-h768",1200,800],1755268200000],["video_1",["https://lh3.googleusercontent.com/pw/AL9nZEVideo",1920,1080],1755354600000,["video/mp4",30000]],["photo_2",["https://lh3.googleusercontent.com/pw/AL9nZEW2",1920,1080],1755441000000]]],sideChannel:{}});</script></body></html>`
	mockPhotosProtobufVideoHTML = `<!DOCTYPE html><html><head><meta property="og:title" content="Family Vacation 2026 - Google Photos"><title>Family Vacation 2026 - Google Photos</title></head><body><script>AF_initDataCallback({key:'ds:1',hash:'2',data:["k",[["photo_1",["https://lh3.googleusercontent.com/pw/AL9nZEW1=w1024-h768",1200,800],1755268200000,null,null,null,null,null,null,{"146008172":[0,1500]}],["video_proto",["https://lh3.googleusercontent.com/pw/AL9nZEProtoVideo",1920,1080],1755354600000,null,null,null,null,null,null,{"76647426":[14200,null,1920,1080]}],["photo_2",["https://lh3.googleusercontent.com/pw/AL9nZEW2",1920,1080],1755441000000,null,null,null,null,null,null,{"76647426":null}]]],sideChannel:{}});</script></body></html>`
	mockPhotosSameDayHTML       = `<!DOCTYPE html><html><head><meta property="og:title" content="Family Vacation 2026 - Google Photos"></head><body><script>AF_initDataCallback({key:'ds:1',hash:'2',data:["k",[["photo_1",["https://lh3.googleusercontent.com/pw/AL9nZEW1=w1024-h768",1200,800],1755268200000],["photo_2",["https://lh3.googleusercontent.com/pw/AL9nZEW2",1920,1080],1755268300000],["photo_3",["https://lh3.googleusercontent.com/pw/AL9nZEW3=s0",1000,1000],1755268400000]]],sideChannel:{}});</script></body></html>`
	mockPhotosFuncHTML          = `<!DOCTYPE html><html><head><title>Summer Roadtrip - Google Photos</title></head><body><script>AF_initDataCallback({key:'ds:1',hash:'2',data:function(){return ["key",[["img_a",["https://lh3.googleusercontent.com/pw/ROADA",800,600],1755268200000]]]},sideChannel:{}});</script></body></html>`
)

func testAlbumConfig(source, url string) map[string]any {
	return map[string]any{
		"albums": []any{
			map[string]any{
				"name":   "Shared Album",
				"source": source,
				"url":    url,
			},
		},
	}
}

type photosExtractionTestCase struct {
	name               string
	html               string
	wantAlbum          string
	wantTotal          int
	wantPhotos         int
	wantFirstID        string
	wantFirstURL       string
	wantFirstTimestamp string
	wantVideoID        string
	wantAspects        map[string]float64
}

func runPhotosFetchExtractionCase(t *testing.T, tc photosExtractionTestCase) {
	t.Helper()
	t.Parallel()
	ts := provider.NewPhotosHTMLServer(t, tc.html)
	p := provider.NewPhotosProvider()
	if err := p.Init(context.Background(), testAlbumConfig("share_url", ts.URL), provider.InitOptions{}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	res, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}
	snap := testutil.As[*provider.PhotoSnapshot](t, res)
	if snap.AlbumName != tc.wantAlbum || snap.TotalPhotos != tc.wantTotal || len(snap.Photos) != tc.wantPhotos {
		t.Fatalf("got album=%q total=%d photos=%d; want %q, %d, %d", snap.AlbumName, snap.TotalPhotos, len(snap.Photos), tc.wantAlbum, tc.wantTotal, tc.wantPhotos)
	}
	if snap.AlbumURL != ts.URL {
		t.Errorf("album URL = %q, want %q", snap.AlbumURL, ts.URL)
	}
	assertPhotosExtractionDetails(t, snap, tc)
}

func assertPhotosExtractionDetails(t *testing.T, snap *provider.PhotoSnapshot, tc photosExtractionTestCase) {
	t.Helper()
	photoMap := make(map[string]provider.PhotoItem, len(snap.Photos))
	for _, item := range snap.Photos {
		photoMap[item.ID] = item
	}
	if tc.wantFirstID != "" {
		item, ok := photoMap[tc.wantFirstID]
		if !ok {
			t.Fatalf("item %q not found in photos", tc.wantFirstID)
		}
		if item.URL != tc.wantFirstURL || item.Timestamp != tc.wantFirstTimestamp {
			t.Errorf("item %q = url %q ts %q, want %q, %q", tc.wantFirstID, item.URL, item.Timestamp, tc.wantFirstURL, tc.wantFirstTimestamp)
		}
	}
	for id, want := range tc.wantAspects {
		if item, ok := photoMap[id]; !ok || item.AspectRatio != want {
			t.Errorf("%s aspect = %v (present=%v), want %v", id, item.AspectRatio, ok, want)
		}
	}
	if tc.wantVideoID != "" {
		if item, ok := photoMap[tc.wantVideoID]; !ok || item.MediaType != "video" {
			t.Errorf("expected %s with MediaType='video', got item=%+v, ok=%v", tc.wantVideoID, item, ok)
		}
	}
}

func TestPhotosProvider_FetchExtraction(t *testing.T) {
	t.Parallel()
	var tenPhotos strings.Builder
	tenPhotos.WriteString(`<!DOCTYPE html><html><body><script>AF_initDataCallback({key:'ds:1',data:["k",[`)
	for i := 1; i <= 10; i++ {
		if i > 1 {
			tenPhotos.WriteString(",")
		}
		fmt.Fprintf(&tenPhotos, `["p%d",["https://lh3.googleusercontent.com/pw/PHOTO_%d",800,600],%d]`, i, i, 1755268200000+int64(i)*86400000)
	}
	tenPhotos.WriteString(`]]});</script></body></html>`)

	const photo1URL = "https://lh3.googleusercontent.com/pw/AL9nZEW1"
	const photo1TS = "2025-08-15T14:30:00Z"
	tests := []photosExtractionTestCase{
		{
			name: "af_callback", html: mockPhotosAFHTML, wantAlbum: "Family Vacation 2026", wantTotal: 3, wantPhotos: 3,
			wantFirstID: "photo_1", wantFirstURL: photo1URL, wantFirstTimestamp: photo1TS,
			wantAspects: map[string]float64{"photo_1": 1.5, "photo_2": 1.78, "photo_3": 1.0},
		},
		{
			name: "keeps_video", html: mockPhotosWithVideoHTML, wantAlbum: "Family Vacation 2026", wantTotal: 3, wantPhotos: 3,
			wantFirstID: "photo_1", wantFirstURL: photo1URL, wantFirstTimestamp: photo1TS, wantVideoID: "video_1",
			wantAspects: map[string]float64{"photo_1": 1.5, "photo_2": 1.78},
		},
		{
			name: "protobuf_video_descriptor", html: mockPhotosProtobufVideoHTML, wantAlbum: "Family Vacation 2026", wantTotal: 3, wantPhotos: 3,
			wantFirstID: "photo_1", wantFirstURL: photo1URL, wantFirstTimestamp: photo1TS, wantVideoID: "video_proto",
			wantAspects: map[string]float64{"photo_1": 1.5, "photo_2": 1.78},
		},
		{name: "same_day_burst", html: mockPhotosSameDayHTML, wantAlbum: "Family Vacation 2026", wantTotal: 3, wantPhotos: 1},
		{name: "func_wrapper", html: mockPhotosFuncHTML, wantAlbum: "Summer Roadtrip", wantTotal: 1, wantPhotos: 1, wantFirstID: "img_a", wantFirstURL: "https://lh3.googleusercontent.com/pw/ROADA", wantFirstTimestamp: photo1TS},
		{name: "ten_photos", html: tenPhotos.String(), wantAlbum: "Shared Album", wantTotal: 10, wantPhotos: 10},
		{name: "empty_album", html: "<html><body><div>Empty Album</div></body></html>", wantAlbum: "Shared Album"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runPhotosFetchExtractionCase(t, tc)
		})
	}
}

// photosLoopbackTransport serves loopback requests over the network and answers
// every other host with an empty 200, so redirect hops to Google login, consent
// or captcha hosts are classified by final host without leaving the machine.
type photosLoopbackTransport struct{}

func (photosLoopbackTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if host := req.URL.Hostname(); host == "127.0.0.1" || host == "::1" || host == "localhost" {
		return http.DefaultTransport.RoundTrip(req)
	}
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: http.Header{}, Request: req}, nil
}

func TestPhotosProvider_FetchErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		status     int
		redirect   string
		bodyBytes  int
		wantErrSub string
	}{
		{"accounts", 302, "https://accounts.google.com/ServiceLogin?continue=https://photos.google.com", 0, provider.ErrAlbumPrivate.Error()},
		{"sorry", 302, "https://sorry.google.com/sorry/index", 0, provider.ErrAlbumRateLimit.Error()},
		{"consent", 302, "https://consent.google.com/m?continue=https://photos.google.com", 0, provider.ErrAlbumPrivate.Error()},
		{"404", 404, "", 0, provider.ErrAlbumNotFound.Error()},
		{"403", 403, "", 0, provider.ErrAlbumPrivate.Error()},
		{"429", 429, "", 0, provider.ErrAlbumRateLimit.Error()},
		{"500", 500, "", 0, "upstream Google Photos returned HTTP 500"},
		{"slowloris", 200, "", 6 * 1024 * 1024, "google photos payload exceeded"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.redirect != "" {
					http.Redirect(w, r, tc.redirect, tc.status)
					return
				}
				w.Header().Set("Content-Type", "text/html")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, strings.Repeat("A", tc.bodyBytes))
			}))
			defer ts.Close()
			p := provider.NewPhotosProvider(provider.WithPhotosTransport(photosLoopbackTransport{}))
			if err := p.Init(context.Background(), testAlbumConfig("share_url", ts.URL), provider.InitOptions{}); err != nil {
				t.Fatalf("Init: %v", err)
			}
			if _, err := p.Fetch(context.Background()); err == nil || !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Errorf("Fetch err = %v, want containing %q", err, tc.wantErrSub)
			}
		})
	}
}

// TestPhotosProvider_FetchServesStaleOnSyncFailure checks stale-while-revalidate:
// a failed forced re-sync keeps serving the last good snapshot.
func TestPhotosProvider_FetchServesStaleOnSyncFailure(t *testing.T) {
	t.Parallel()
	ts, captured := testutil.RouteServer(t, map[string]testutil.Route{"/": {
		Body:        `<!DOCTYPE html><html><head><title>SWR</title></head><body><script>AF_initDataCallback({key:'ds:1',data:["k",[["swr_1",["https://lh3.googleusercontent.com/pw/SWROK",800,600],1755268200000]]]});</script></body></html>`,
		ContentType: "text/html",
		Next:        []testutil.Route{{Status: http.StatusInternalServerError}},
	}})
	_ = captured
	p := provider.NewPhotosProvider()
	if err := p.Init(context.Background(), testAlbumConfig("share_url", ts.URL), provider.InitOptions{}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := p.Fetch(context.Background()); err != nil {
		t.Fatalf("first fetch failed: %v", err)
	}
	if err := p.Sync(context.Background(), true); err != nil {
		t.Fatalf("forced Sync against HTTP 500 with a cached snapshot = %v, want nil (SWR)", err)
	}
	res, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("second fetch under SWR failed: %v", err)
	}
	if s := testutil.As[*provider.PhotoSnapshot](t, res); len(s.Photos) != 1 || s.Photos[0].URL != "https://lh3.googleusercontent.com/pw/SWROK" {
		t.Fatalf("expected cached snapshot under SWR: %+v", s)
	}
}

type photosInitTestCase struct {
	name, envKey, envVal string
	cfg                  map[string]any
	opts                 provider.InitOptions
	wantErrSub           string
}

func runPhotosInitCase(t *testing.T, tc photosInitTestCase) {
	t.Helper()
	if tc.envKey != "" {
		t.Setenv(tc.envKey, tc.envVal)
	}
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

func assertPhotosProviderEmberAndMalformed(t *testing.T) {
	t.Helper()
	pEmber := provider.NewPhotosProvider()
	if err := pEmber.Init(context.Background(), testAlbumConfig("ember_bridge", "https://photos.app.goo.gl/ember"), provider.InitOptions{}); err != nil {
		t.Fatalf("Init ember_bridge failed: %v", err)
	}
	if pEmber.Source() != provider.PhotoSourceEmberBridge {
		t.Errorf("got Source=%q, want %q", pEmber.Source(), provider.PhotoSourceEmberBridge)
	}
	resEmber, err := pEmber.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch on ember_bridge failed: %v", err)
	}
	snapEmber := testutil.As[*provider.PhotoSnapshot](t, resEmber)
	if snapEmber.AlbumName != "Shared Album" || snapEmber.AlbumURL != "https://photos.app.goo.gl/ember" || snapEmber.TotalPhotos != 0 || len(snapEmber.Photos) != 0 {
		t.Fatalf("unexpected ember snapshot: %+v", snapEmber)
	}

	pUninit := provider.NewPhotosProvider()
	if _, err = pUninit.Fetch(context.Background()); err == nil || !strings.Contains(err.Error(), "provider not initialized") {
		t.Fatalf("expected uninitialized error, got: %v", err)
	}
	tsMal := provider.NewPhotosHTMLServer(t, `<html><body><script>AF_initDataCallback({key:'ds:1',data:[INVALID JSON...});</script></body></html>`)
	pMal := provider.NewPhotosProvider()
	if err = pMal.Init(context.Background(), testAlbumConfig("share_url", tsMal.URL), provider.InitOptions{}); err != nil {
		t.Fatalf("Init pMal failed: %v", err)
	}
	resMal, err := pMal.Fetch(context.Background())
	if err != nil || len(testutil.As[*provider.PhotoSnapshot](t, resMal).Photos) != 0 {
		t.Fatalf("expected empty photos on malformed JSON, got %v, err=%v", resMal, err)
	}

	reg := provider.NewRegistry()
	p1, err := reg.Create("photo-carousel")
	if err != nil || p1 == nil {
		t.Fatalf("failed create photo-carousel: %v", err)
	}
	if err := p1.Subscribe(context.Background(), nil); err != nil {
		t.Errorf("Subscribe failed: %v", err)
	}
	if err := p1.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown failed: %v", err)
	}
}

func TestPhotosProvider_InitAndLifecycle(t *testing.T) {
	tests := []photosInitTestCase{
		{"missing_albums", "", "", map[string]any{}, provider.InitOptions{}, "'albums' is required"},
		{"empty_albums", "", "", map[string]any{"albums": []any{}}, provider.InitOptions{}, "must contain at least one album"},
		{"not_object", "", "", map[string]any{"albums": []any{"invalid"}}, provider.InitOptions{}, "must be an object"},
		{"missing_name", "", "", map[string]any{"albums": []any{map[string]any{"source": "share_url", "url": "https://p.app/1"}}}, provider.InitOptions{}, "requires non-empty 'name'"},
		{"missing_source_and_url", "", "", map[string]any{"albums": []any{map[string]any{"name": "Album"}}}, provider.InitOptions{}, "requires 'source'"},
		{"invalid_source", "", "", map[string]any{"albums": []any{map[string]any{"name": "Album", "source": "bad_source", "url": "https://p.app/1"}}}, provider.InitOptions{}, "must be either"},
		{"mutex_url_and_url_env", "", "", map[string]any{"albums": []any{map[string]any{"name": "Album", "source": "share_url", "url": "https://p.app/1", "url_env": "E"}}}, provider.InitOptions{}, "mutually exclusive"},
		{"neither_url_nor_url_env", "", "", map[string]any{"albums": []any{map[string]any{"name": "Album", "source": "share_url"}}}, provider.InitOptions{}, "requires either 'url' or 'url_env'"},
		{"unset_env", "", "", map[string]any{"albums": []any{map[string]any{"name": "Album", "source": "share_url", "url_env": "MISSING"}}}, provider.InitOptions{}, "is not set or empty"},
		{"opts_secret", "", "", map[string]any{"albums": []any{map[string]any{"name": "Album", "source": "share_url", "url_env": "S"}}}, provider.InitOptions{Secrets: map[string]string{"S": "https://p.app/s"}}, ""},
		{"env_var", "TEST_ALBUM_ENV", "https://p.app/env", map[string]any{"albums": []any{map[string]any{"name": "Album", "source": "share_url", "url_env": "TEST_ALBUM_ENV"}}}, provider.InitOptions{}, ""},
		{"opts_override", "", "", map[string]any{"albums": []any{map[string]any{"name": "Album", "source": "share_url", "url": "https://p.app/raw"}}}, provider.InitOptions{Secrets: map[string]string{"albums[0].url": "https://p.app/over"}}, ""},
		{"bad_scheme", "", "", map[string]any{"albums": []any{map[string]any{"name": "Album", "source": "share_url", "url": "ftp://photos.google.com/album"}}}, provider.InitOptions{}, "invalid album URL"},
		{"bad_host", "", "", map[string]any{"albums": []any{map[string]any{"name": "Album", "source": "share_url", "url": "http:///no-host"}}}, provider.InitOptions{}, "invalid album URL"},
		{"ember_bridge_valid", "", "", map[string]any{"albums": []any{map[string]any{"name": "Album", "source": "ember_bridge", "url": "https://photos.app.goo.gl/ember"}}}, provider.InitOptions{}, ""},
	}
	for _, tc := range tests {
		runPhotosInitCase(t, tc)
	}
	assertPhotosProviderEmberAndMalformed(t)
}

type deckWindowTestCase struct {
	name                string
	pollBatches         [][]string
	wantCounts          []int
	wantStable          bool
	wantAppended        bool
	wantRemovedExcluded string
}

// deckWindowRoute serves each poll batch once, in order, then keeps serving the last.
func deckWindowRoute(batches [][]string) testutil.Route {
	routes := make([]testutil.Route, len(batches))
	for i, batch := range batches {
		var b strings.Builder
		b.WriteString(`<!DOCTYPE html><html><body><script>AF_initDataCallback({key:'ds:1',data:["k",[`)
		for j, id := range batch {
			if j > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, `["%s",["https://lh3.googleusercontent.com/pw/%s",800,600],%d]`, id, id, 1755268200000+int64(j)*86400000)
		}
		b.WriteString(`]]});</script></body></html>`)
		routes[i] = testutil.Route{Body: b.String(), ContentType: "text/html; charset=utf-8"}
	}
	first := routes[0]
	first.Next = routes[1:]
	return first
}

func runDeckWindowCase(t *testing.T, tc deckWindowTestCase) {
	t.Helper()
	t.Parallel()
	ts, captured := testutil.RouteServer(t, map[string]testutil.Route{"/": deckWindowRoute(tc.pollBatches)})
	_ = captured
	p := provider.NewPhotosProvider()
	if err := p.Init(context.Background(), testAlbumConfig("share_url", ts.URL), provider.InitOptions{}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	snapshots := make([]*provider.PhotoSnapshot, len(tc.pollBatches))
	for i := range tc.pollBatches {
		if i > 0 {
			if err := p.Sync(context.Background(), true); err != nil {
				t.Fatalf("poll %d Sync failed: %v", i, err)
			}
		}
		res, err := p.Fetch(context.Background())
		if err != nil {
			t.Fatalf("poll %d Fetch failed: %v", i, err)
		}
		snapshots[i] = testutil.As[*provider.PhotoSnapshot](t, res)
	}
	assertDeckSnapshots(t, snapshots, tc)
}

func assertDeckSnapshots(t *testing.T, snapshots []*provider.PhotoSnapshot, tc deckWindowTestCase) {
	t.Helper()
	for i, snap := range snapshots {
		if len(snap.Photos) != tc.wantCounts[i] {
			t.Errorf("poll %d photos count = %d, want %d", i, len(snap.Photos), tc.wantCounts[i])
		}
		seen := make(map[string]bool, len(snap.Photos))
		for _, photo := range snap.Photos {
			if seen[photo.URL] {
				t.Errorf("poll %d contains duplicate photo URL %q", i, photo.URL)
			}
			seen[photo.URL] = true
		}
	}
	assertDeckOrdering(t, snapshots, tc)
}

func assertDeckOrdering(t *testing.T, snapshots []*provider.PhotoSnapshot, tc deckWindowTestCase) {
	t.Helper()
	s1, s2 := snapshots[0].Photos, snapshots[1].Photos
	if tc.wantStable && len(s1) != len(s2) {
		t.Errorf("stable check photo count mismatch: %d vs %d", len(s1), len(s2))
	}
	if tc.wantStable || tc.wantAppended {
		for i := range min(len(s1), len(s2)) {
			if s1[i].URL != s2[i].URL {
				t.Errorf("photo[%d] = %q, want %q (existing order must hold)", i, s2[i].URL, s1[i].URL)
			}
		}
	}
	if tc.wantAppended && len(s2) <= len(s1) {
		t.Errorf("expected snapshot 2 to have appended photos, got len %d <= %d", len(s2), len(s1))
	}
	if tc.wantRemovedExcluded != "" {
		for _, ph := range s2 {
			if strings.Contains(ph.URL, tc.wantRemovedExcluded) {
				t.Errorf("expected removed photo %q to be excluded from poll 2, but found %q", tc.wantRemovedExcluded, ph.URL)
			}
		}
	}
}

func TestPhotosProvider_DeckWindowAndReconcile(t *testing.T) {
	t.Parallel()
	tests := []deckWindowTestCase{
		{
			name:        "stable_deck_order_across_polls_when_album_fits_preload",
			pollBatches: [][]string{{"p1", "p2", "p3", "p4", "p5"}, {"p1", "p2", "p3", "p4", "p5"}},
			wantCounts:  []int{5, 5},
			wantStable:  true,
		},
		{
			name:         "reconcile_appends_new_photos_without_reshuffling_existing",
			pollBatches:  [][]string{{"p1", "p2", "p3"}, {"p1", "p2", "p3", "p4"}},
			wantCounts:   []int{3, 4},
			wantAppended: true,
		},
		{
			name:                "reconcile_removes_old_photos_cleanly",
			pollBatches:         [][]string{{"p1", "p2", "p3", "p4"}, {"p1", "p3", "p4"}},
			wantCounts:          []int{4, 3},
			wantRemovedExcluded: "p2",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runDeckWindowCase(t, tc)
		})
	}
}

const photosBatchexecutePath = "/_/PhotosUi/data/batchexecute"

// photosDiskSeed is an album cache file written before Init.
type photosDiskSeed struct {
	albumName, scrapedTitle string
	url                     string
	page1Age, fullAge       time.Duration
	photos                  []provider.PhotoItem
}

// photosFetchServers starts one RouteServer per HTML body and returns a
// replacer that turns "$0", "$1", ... into their URLs. batchPages are served
// in order at the batchexecute path of the first server.
func photosFetchServers(t *testing.T, htmlByServer []string, batchPages []testutil.Route) ([]*testutil.Captured, *strings.Replacer) {
	t.Helper()
	captured := make([]*testutil.Captured, len(htmlByServer))
	pairs := make([]string, 0, 2*len(htmlByServer))
	for i, html := range htmlByServer {
		routes := map[string]testutil.Route{"/": {Body: html, ContentType: "text/html; charset=utf-8"}}
		if i == 0 && len(batchPages) > 0 {
			first := batchPages[0]
			first.Next = batchPages[1:]
			routes[photosBatchexecutePath] = first
		}
		srv, c := testutil.RouteServer(t, routes)
		captured[i] = c
		pairs = append(pairs, fmt.Sprintf("$%d", i), srv.URL)
	}
	return captured, strings.NewReplacer(pairs...)
}

// photosSubstitute returns a copy of v with every string run through r.
func photosSubstitute(v any, r *strings.Replacer) any {
	switch x := v.(type) {
	case string:
		return r.Replace(x)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = photosSubstitute(val, r)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = photosSubstitute(val, r)
		}
		return out
	default:
		return v
	}
}

func writePhotosDiskSeed(t *testing.T, dir string, seed *photosDiskSeed, r *strings.Replacer) {
	t.Helper()
	if seed == nil {
		return
	}
	url := r.Replace(seed.url)
	data, err := json.Marshal(map[string]any{
		"album_name":      seed.albumName,
		"scraped_title":   seed.scrapedTitle,
		"resolved_url":    url,
		"last_page1_sync": time.Now().Add(-seed.page1Age),
		"last_full_sync":  time.Now().Add(-seed.fullAge),
		"photos":          seed.photos,
	})
	if err != nil {
		t.Fatalf("marshal disk seed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, provider.AlbumCacheFilename("share_url", url)), data, 0o600); err != nil {
		t.Fatalf("write disk seed: %v", err)
	}
}

func photosBatchCalls(captured []*testutil.Captured) int {
	n := 0
	for _, req := range captured[0].Requests() {
		if req.Path == photosBatchexecutePath {
			n++
		}
	}
	return n
}

const (
	photosPage1HTML  = `<!DOCTYPE html><html><head><title>Paged Album - Google Photos</title></head><body><script>AF_initDataCallback({key:'ds:1',hash:'2',data:["k",[["p1",["https://lh3.googleusercontent.com/pw/PAGE1",1000,800],1755268100000]],"token_page2"],sideChannel:{}});</script></body></html>`
	photosBatchPage2 = `)]}'
[[["wrb.fr","snAcKc","[[[\"p2\",[\"https://lh3.googleusercontent.com/pw/PAGE2\",1200,900],1755268200000]],\"token_page3\"]",null,null,null,"generic"]]]`
	photosBatchPage3 = `)]}'
[[["wrb.fr","snAcKc","[[[\"p3\",[\"https://lh3.googleusercontent.com/pw/PAGE3\",1200,900],1755268300000]],\"\"]",null,null,null,"generic"]]]`
)

func TestPhotosProvider_FetchAlbums(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		cfg             map[string]any
		secrets         map[string]string
		htmlByServer    []string
		batchPages      []testutil.Route
		diskSeed        *photosDiskSeed
		tz              string
		wantAlbumName   string
		wantAlbumURL    string
		wantTotal       int
		wantDealt       int
		wantURLs        []string
		wantPhotoAlbum  string
		wantAlbumsByDay map[string][]string
		wantBatchCalls  int
		wantPageHits    int
	}{
		{
			name: "multi_album_parsing_and_merging",
			cfg: map[string]any{"albums": []any{
				map[string]any{"name": "Vacation 2026", "source": "share_url", "url_env": "ALBUM_SHARE_URL"},
				map[string]any{"name": "Ember Feed", "source": "ember_bridge", "url": "https://photos.app.goo.gl/ember-url"},
			}},
			secrets:      map[string]string{"albums[0].url": "$0"},
			htmlByServer: []string{`<!DOCTYPE html><html><head><title>Share Vacation</title></head><body><script>AF_initDataCallback({key:'ds:1',data:["k",[["p1",["https://lh3.googleusercontent.com/pw/P_SHARE1",800,600],1755268200000],["p2",["https://lh3.googleusercontent.com/pw/P_DUP",800,600],1755354600000]]]});</script></body></html>`},
			// The first ember_bridge album's URL takes precedence for AlbumURL.
			wantAlbumName: "Vacation 2026, Ember Feed", wantAlbumURL: "https://photos.app.goo.gl/ember-url", wantTotal: 2, wantDealt: 2,
			wantPhotoAlbum: "Vacation 2026", wantPageHits: 1,
		},
		{
			// Day 1: Album A has 3 photos, Album B 2 -> one from each, adjacent, A first.
			// Day 2: Album A has 2 photos, Album B none -> one from A.
			name: "per_day_multi_album_pairing",
			cfg: map[string]any{"albums": []any{
				map[string]any{"name": "Album A", "source": "share_url", "url": "$0"},
				map[string]any{"name": "Album B", "source": "share_url", "url": "$1"},
			}},
			htmlByServer: []string{
				`<!DOCTYPE html><html><body><script>AF_initDataCallback({key:'ds:1',data:["k",[["a_d1_1",["https://lh3.googleusercontent.com/pw/A_D1_1",800,600],1755268200000],["a_d1_2",["https://lh3.googleusercontent.com/pw/A_D1_2",800,600],1755268260000],["a_d1_3",["https://lh3.googleusercontent.com/pw/A_D1_3",800,600],1755268320000],["a_d2_1",["https://lh3.googleusercontent.com/pw/A_D2_1",800,600],1755354600000],["a_d2_2",["https://lh3.googleusercontent.com/pw/A_D2_2",800,600],1755354660000]]]});</script></body></html>`,
				`<!DOCTYPE html><html><body><script>AF_initDataCallback({key:'ds:1',data:["k",[["b_d1_1",["https://lh3.googleusercontent.com/pw/B_D1_1",800,600],1755268200000],["b_d1_2",["https://lh3.googleusercontent.com/pw/B_D1_2",800,600],1755268260000]]]});</script></body></html>`,
			},
			tz:            "UTC",
			wantAlbumName: "Album A, Album B", wantAlbumURL: "$0", wantTotal: 7, wantDealt: 3,
			wantAlbumsByDay: map[string][]string{"2025-08-15": {"Album A", "Album B"}, "2025-08-16": {"Album A"}},
			wantPageHits:    1,
		},
		{
			name:         "warm_boot_zero_http",
			cfg:          testAlbumConfig("share_url", "$0"),
			htmlByServer: []string{""},
			diskSeed: &photosDiskSeed{
				albumName: "Cached Album", scrapedTitle: "Warm Scraped Title", url: "$0", page1Age: 10 * time.Minute, fullAge: 2 * time.Hour,
				photos: []provider.PhotoItem{
					{ID: "c1", URL: "https://lh3.googleusercontent.com/pw/WARM1", AspectRatio: 1.33, AlbumName: "Warm Scraped Title"},
					{ID: "c2", URL: "https://lh3.googleusercontent.com/pw/WARM2", AspectRatio: 1.33, AlbumName: "Warm Scraped Title"},
				},
			},
			wantAlbumName: "Warm Scraped Title", wantAlbumURL: "$0", wantTotal: 2, wantDealt: 2,
			wantURLs: []string{"https://lh3.googleusercontent.com/pw/WARM1", "https://lh3.googleusercontent.com/pw/WARM2"},
		},
		{
			name:          "snAcKc_pagination",
			cfg:           testAlbumConfig("share_url", "$0"),
			htmlByServer:  []string{photosPage1HTML},
			batchPages:    []testutil.Route{{Body: photosBatchPage2}, {Body: photosBatchPage3}},
			wantAlbumName: "Paged Album", wantAlbumURL: "$0", wantTotal: 3, wantDealt: 1,
			wantBatchCalls: 2, wantPageHits: 1,
		},
		{
			name:         "snAcKc_repeated_token_then_error",
			cfg:          testAlbumConfig("share_url", "$0"),
			htmlByServer: []string{`<!DOCTYPE html><html><body><script>AF_initDataCallback({key:'ds:1',data:["k",[["p1",["https://lh3.googleusercontent.com/pw/PAGE1",1000,800],1755268100000]],"repeated_tok"]});</script></body></html>`},
			batchPages: []testutil.Route{
				{Body: `)]}'
[[["wrb.fr","snAcKc","[[[\"p2\",[\"https://lh3.googleusercontent.com/pw/PAGE2\",1200,900],1755268200000]],\"repeated_tok\"]",null,null,null,"generic"]]]`},
				{Status: http.StatusInternalServerError},
			},
			wantAlbumName: "Shared Album", wantAlbumURL: "$0", wantTotal: 2, wantDealt: 1,
			wantBatchCalls: 1, wantPageHits: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			captured, r := photosFetchServers(t, tc.htmlByServer, tc.batchPages)
			dir := t.TempDir()
			writePhotosDiskSeed(t, dir, tc.diskSeed, r)
			secrets := make(map[string]string, len(tc.secrets))
			for k, v := range tc.secrets {
				secrets[k] = r.Replace(v)
			}
			cfg, _ := photosSubstitute(tc.cfg, r).(map[string]any)
			p := provider.NewPhotosProvider(provider.WithPhotosCacheDir(dir))
			if err := p.Init(context.Background(), cfg, provider.InitOptions{Secrets: secrets, Timezone: tc.tz}); err != nil {
				t.Fatalf("Init failed: %v", err)
			}
			if p.Source() != provider.PhotoSourceShareURL {
				t.Errorf("Source = %q, want %q", p.Source(), provider.PhotoSourceShareURL)
			}
			res, err := p.Fetch(context.Background())
			if err != nil {
				t.Fatalf("Fetch failed: %v", err)
			}
			snap := testutil.As[*provider.PhotoSnapshot](t, res)
			if snap.AlbumName != tc.wantAlbumName || snap.AlbumURL != r.Replace(tc.wantAlbumURL) || snap.TotalPhotos != tc.wantTotal || len(snap.Photos) != tc.wantDealt {
				t.Errorf("snap name=%q url=%q total=%d dealt=%d, want %q, %q, %d, %d",
					snap.AlbumName, snap.AlbumURL, snap.TotalPhotos, len(snap.Photos), tc.wantAlbumName, r.Replace(tc.wantAlbumURL), tc.wantTotal, tc.wantDealt)
			}
			assertPhotosFetchedAlbums(t, snap, tc.wantURLs, tc.wantPhotoAlbum, tc.wantAlbumsByDay)
			if got := photosBatchCalls(captured); got != tc.wantBatchCalls {
				t.Errorf("batchexecute calls = %d, want %d", got, tc.wantBatchCalls)
			}
			if got := len(captured[0].Requests()) - photosBatchCalls(captured); got != tc.wantPageHits {
				t.Errorf("album page hits = %d, want %d", got, tc.wantPageHits)
			}
		})
	}
}

func assertPhotosFetchedAlbums(t *testing.T, snap *provider.PhotoSnapshot, wantURLs []string, wantPhotoAlbum string, wantAlbumsByDay map[string][]string) {
	t.Helper()
	urls := make(map[string]bool, len(snap.Photos))
	for _, ph := range snap.Photos {
		urls[ph.URL] = true
		if wantPhotoAlbum != "" && ph.AlbumName != wantPhotoAlbum {
			t.Errorf("photo %s AlbumName = %q, want %q", ph.ID, ph.AlbumName, wantPhotoAlbum)
		}
	}
	for _, u := range wantURLs {
		if !urls[u] {
			t.Errorf("missing photo %q in %v", u, urls)
		}
	}
	for day, wantAlbums := range wantAlbumsByDay {
		var idx []int
		var albums []string
		for i, ph := range snap.Photos {
			if strings.HasPrefix(ph.Timestamp, day) {
				idx = append(idx, i)
				albums = append(albums, ph.AlbumName)
			}
		}
		if !slices.Equal(albums, wantAlbums) {
			t.Errorf("day %s albums = %v, want %v", day, albums, wantAlbums)
		}
		for i := 1; i < len(idx); i++ {
			if idx[i]-idx[i-1] != 1 {
				t.Errorf("day %s photos not adjacent: indices %v", day, idx)
			}
		}
	}
}

// TestPhotosProvider_DeltaSyncSkipsPagination checks that Sync(ctx, false)
// re-scrapes only page 1 after a full paginated sync.
func TestPhotosProvider_DeltaSyncSkipsPagination(t *testing.T) {
	t.Parallel()
	captured, r := photosFetchServers(t, []string{photosPage1HTML}, []testutil.Route{{Body: photosBatchPage2}, {Body: photosBatchPage3}})
	p := provider.NewPhotosProvider(provider.WithPhotosCacheDir(t.TempDir()))
	if err := p.Init(context.Background(), testAlbumConfig("share_url", r.Replace("$0")), provider.InitOptions{}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	if _, err := p.Fetch(context.Background()); err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}
	if err := p.Sync(context.Background(), false); err != nil {
		t.Fatalf("delta Sync failed: %v", err)
	}
	if got := photosBatchCalls(captured); got != 2 {
		t.Errorf("batchexecute calls after delta = %d, want 2", got)
	}
	if got := len(captured[0].Requests()) - photosBatchCalls(captured); got != 2 {
		t.Errorf("album page hits after delta = %d, want 2", got)
	}
}

func TestPhotosProvider_SubscribeEmitsHealthy(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		html     string
		page1    time.Duration
		full     time.Duration
		diskSeed *photosDiskSeed
	}{
		{
			name:  "sync_ticks_after_hydration",
			html:  `<!DOCTYPE html><html><head><title>Sub Album</title></head><body><script>AF_initDataCallback({key:'ds:1',data:["k",[["sub1",["https://lh3.googleusercontent.com/pw/SUB1",800,600],1755268200000]]]});</script></body></html>`,
			page1: 10 * time.Millisecond, full: 50 * time.Millisecond,
		},
		{
			name:  "warm_boot_stale_cache_syncs",
			html:  `<!DOCTYPE html><html><head><title>Stale Sub</title></head><body><script>AF_initDataCallback({key:'ds:1',data:["k",[["stale1",["https://lh3.googleusercontent.com/pw/STALE1",800,600],1755268200000]]]});</script></body></html>`,
			page1: 10 * time.Hour, full: 24 * time.Hour,
			diskSeed: &photosDiskSeed{
				albumName: "Stale Cached", scrapedTitle: "Stale Scraped", url: "$0", page1Age: 48 * time.Hour, fullAge: 48 * time.Hour,
				photos: []provider.PhotoItem{{ID: "c1", URL: "https://lh3.googleusercontent.com/pw/WARM_OLD", AspectRatio: 1.33}},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, r := photosFetchServers(t, []string{tc.html}, nil)
			dir := t.TempDir()
			writePhotosDiskSeed(t, dir, tc.diskSeed, r)
			p := provider.NewPhotosProvider(provider.WithPhotosCacheDir(dir), provider.WithPhotosSyncIntervals(tc.page1, tc.full))
			if err := p.Init(context.Background(), testAlbumConfig("share_url", r.Replace("$0")), provider.InitOptions{}); err != nil {
				t.Fatalf("Init failed: %v", err)
			}
			if tc.diskSeed == nil {
				if _, err := p.Fetch(context.Background()); err != nil {
					t.Fatalf("hydrating Fetch failed: %v", err)
				}
			}
			sink := make(chan provider.WidgetPayload, 5)
			ctx, cancel := context.WithCancel(context.Background())
			errChan := make(chan error, 1)
			go func() { errChan <- p.Subscribe(ctx, sink) }()
			defer func() {
				cancel()
				<-errChan
			}()
			select {
			case payload := <-sink:
				snap, ok := payload.Data.(*provider.PhotoSnapshot)
				if payload.State != provider.StateHealthy || !ok || len(snap.Photos) == 0 {
					t.Errorf("payload state=%v data=%+v, want healthy snapshot with photos", payload.State, payload.Data)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("timed out waiting for Subscribe payload")
			}
		})
	}
}

package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/config"
)

type mockEmbedder struct {
	emb   []float64
	model string
	err   error
}

func (m *mockEmbedder) Embed(ctx context.Context, wavData []byte) ([]float64, string, error) {
	return m.emb, m.model, m.err
}

type mockIdentifier struct {
	id  string
	err error
}

func (m *mockIdentifier) Identify(ctx context.Context, wavData []byte) (string, error) {
	return m.id, m.err
}

func writeFingerprints(t *testing.T, path string, f FingerprintFile) {
	t.Helper()
	raw, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCosineSimilarity(t *testing.T) {
	t.Parallel()
	sim, err := CosineSimilarity([]float64{1, 0}, []float64{1, 0})
	if err != nil || sim < 0.999 {
		t.Fatalf("identical vectors: sim=%v err=%v", sim, err)
	}
	sim, err = CosineSimilarity([]float64{1, 0}, []float64{0, 1})
	if err != nil || sim != 0 {
		t.Fatalf("orthogonal vectors: sim=%v err=%v", sim, err)
	}
	if _, err := CosineSimilarity([]float64{1}, []float64{1, 0}); err == nil {
		t.Fatal("expected dimension mismatch error")
	}
	if _, err := CosineSimilarity([]float64{0, 0}, []float64{1, 0}); err == nil {
		t.Fatal("expected zero-vector error")
	}
}

func TestMatchSpeaker(t *testing.T) {
	t.Parallel()
	speakers := []Fingerprint{
		{ID: "mike", Embedding: []float64{1, 0, 0}},
		{ID: "lauren", Embedding: []float64{0, 1, 0}},
	}

	id, sim, err := MatchSpeaker([]float64{0.9, 0.1, 0}, speakers, 0.7)
	if err != nil || id != "mike" || sim < 0.9 {
		t.Fatalf("expected mike, got id=%q sim=%v err=%v", id, sim, err)
	}

	id, _, err = MatchSpeaker([]float64{0.1, 0.95, 0}, speakers, 0.7)
	if err != nil || id != "lauren" {
		t.Fatalf("expected lauren, got id=%q err=%v", id, err)
	}

	// Equidistant from both, below threshold: nobody.
	id, _, err = MatchSpeaker([]float64{0, 0, 1}, speakers, 0.7)
	if err != nil || id != "" {
		t.Fatalf("expected no match, got id=%q err=%v", id, err)
	}

	if _, _, err := MatchSpeaker([]float64{1, 0}, speakers, 0.7); err == nil {
		t.Fatal("expected dimension mismatch error")
	}
}

func TestFingerprintStore_LoadAndReload(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "speakers.json")
	writeFingerprints(t, path, FingerprintFile{Model: "ecapa", Speakers: []Fingerprint{{ID: "mike", Embedding: []float64{1, 0}}}})

	store := NewFingerprintStore(path)
	f, err := store.Load()
	if err != nil || len(f.Speakers) != 1 || f.Speakers[0].ID != "mike" {
		t.Fatalf("first load: %+v err=%v", f, err)
	}

	// Re-enrollment: file changes on disk, next Load sees it without a restart.
	writeFingerprints(t, path, FingerprintFile{Model: "ecapa", Speakers: []Fingerprint{
		{ID: "mike", Embedding: []float64{1, 0}},
		{ID: "lauren", Embedding: []float64{0, 1}},
	}})
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	f, err = store.Load()
	if err != nil || len(f.Speakers) != 2 {
		t.Fatalf("reload: %+v err=%v", f, err)
	}
}

func TestFingerprintStore_Invalid(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cases := map[string]FingerprintFile{
		"empty-id":        {Speakers: []Fingerprint{{ID: "", Embedding: []float64{1}}}},
		"duplicate-id":    {Speakers: []Fingerprint{{ID: "a", Embedding: []float64{1}}, {ID: "a", Embedding: []float64{1}}}},
		"empty-embedding": {Speakers: []Fingerprint{{ID: "a"}}},
	}
	for name, f := range cases {
		path := filepath.Join(dir, name+".json")
		writeFingerprints(t, path, f)
		if _, err := NewFingerprintStore(path).Load(); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if _, err := NewFingerprintStore(filepath.Join(dir, "missing.json")).Load(); err == nil {
		t.Error("missing file: expected error")
	}
}

func TestFingerprintIdentifier(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "speakers.json")
	writeFingerprints(t, path, FingerprintFile{Model: "ecapa", Speakers: []Fingerprint{
		{ID: "mike", Embedding: []float64{1, 0}},
		{ID: "lauren", Embedding: []float64{0, 1}},
	}})
	store := NewFingerprintStore(path)

	id, err := NewFingerprintIdentifier(&mockEmbedder{emb: []float64{0.2, 0.98}, model: "ecapa"}, store, 0.7).Identify(context.Background(), nil)
	if err != nil || id != "lauren" {
		t.Fatalf("expected lauren, got %q err=%v", id, err)
	}

	_, err = NewFingerprintIdentifier(&mockEmbedder{emb: []float64{0, 1}, model: "wavlm"}, store, 0.7).Identify(context.Background(), nil)
	if err == nil {
		t.Fatal("expected model mismatch error")
	}

	_, err = NewFingerprintIdentifier(&mockEmbedder{err: errors.New("down")}, store, 0.7).Identify(context.Background(), nil)
	if err == nil {
		t.Fatal("expected embed error")
	}

	// No enrolled speakers: empty result, embedder never needed.
	emptyPath := filepath.Join(t.TempDir(), "empty.json")
	writeFingerprints(t, emptyPath, FingerprintFile{Model: "ecapa"})
	id, err = NewFingerprintIdentifier(&mockEmbedder{err: errors.New("should not be called")}, NewFingerprintStore(emptyPath), 0.7).Identify(context.Background(), nil)
	if err != nil || id != "" {
		t.Fatalf("empty store: id=%q err=%v", id, err)
	}
}

func TestDefaultEmbedClient(t *testing.T) {
	t.Parallel()
	var gotType string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotType = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"embedding":[0.5,0.25],"model":"ecapa"}`))
	}))
	defer srv.Close()

	emb, model, err := NewDefaultEmbedClient(srv.URL, 5).Embed(context.Background(), []byte("RIFFwav"))
	if err != nil || len(emb) != 2 || model != "ecapa" {
		t.Fatalf("emb=%v model=%q err=%v", emb, model, err)
	}
	if gotType != "audio/wav" || !bytes.Equal(gotBody, []byte("RIFFwav")) {
		t.Fatalf("request: type=%q body=%q", gotType, gotBody)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer bad.Close()
	if _, _, err := NewDefaultEmbedClient(bad.URL, 5).Embed(context.Background(), nil); err == nil {
		t.Fatal("expected http error")
	}

	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"embedding":[]}`))
	}))
	defer empty.Close()
	if _, _, err := NewDefaultEmbedClient(empty.URL, 5).Embed(context.Background(), nil); err == nil {
		t.Fatal("expected empty-embedding error")
	}
}

func TestNewSpeakerIdentifierFromConfig(t *testing.T) {
	t.Parallel()
	if NewSpeakerIdentifierFromConfig(nil) != nil {
		t.Fatal("nil config should disable speaker id")
	}
	if NewSpeakerIdentifierFromConfig(&config.SpeakerIDConfig{EmbedURL: "http://x"}) != nil {
		t.Fatal("missing fingerprints path should disable speaker id")
	}
	if NewSpeakerIdentifierFromConfig(&config.SpeakerIDConfig{EmbedURL: "http://x", FingerprintsPath: "/f.json"}) == nil {
		t.Fatal("complete config should enable speaker id")
	}
}

func collectHubEvents(t *testing.T, h *Hub) map[string]any {
	t.Helper()
	var mu sync.Mutex
	events := map[string]any{}
	sink := func(event string, data any) error {
		mu.Lock()
		defer mu.Unlock()
		events[event] = data
		return nil
	}
	if err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(1600)), "eink-display-livingroom", "sess-1", sink); err != nil {
		t.Fatalf("interact: %v", err)
	}
	return events
}

func TestHub_SpeakerCarriedToTranscriptAndBrain(t *testing.T) {
	t.Parallel()
	var brainBody map[string]any
	brainSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&brainBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"reply":"ok"}`))
	}))
	defer brainSrv.Close()

	h := NewHub(&config.VoiceHubConfig{Enabled: true}, nil,
		WithSTTClient(&mockSTT{text: "what's on my calendar"}),
		WithBrainClient(NewDefaultBrainClient(brainSrv.URL, 5)),
		WithTTSClient(&mockTTS{}),
		WithSpeakerIdentifier(&mockIdentifier{id: "mike"}),
	)
	events := collectHubEvents(t, h)

	tr, _ := events["transcript"].(map[string]string)
	if tr["speaker"] != "mike" || tr["transcript"] != "what's on my calendar" {
		t.Fatalf("transcript event: %+v", events["transcript"])
	}
	if brainBody["speaker"] != "mike" || brainBody["node_id"] != "eink-display-livingroom" {
		t.Fatalf("brain request: %+v", brainBody)
	}
}

func TestHub_SpeakerFailureIsNotFatal(t *testing.T) {
	t.Parallel()
	brain := &mockBrain{reply: "ok"}
	h := NewHub(&config.VoiceHubConfig{Enabled: true}, nil,
		WithSTTClient(&mockSTT{text: "hello"}),
		WithBrainClient(brain),
		WithTTSClient(&mockTTS{}),
		WithSpeakerIdentifier(&mockIdentifier{err: errors.New("embed service down")}),
	)
	events := collectHubEvents(t, h)

	tr, _ := events["transcript"].(map[string]string)
	if tr["speaker"] != "" {
		t.Fatalf("expected empty speaker, got %+v", tr)
	}
	if _, ok := events["done"]; !ok {
		t.Fatal("expected turn to complete")
	}
	if brain.calledWith != "hello" {
		t.Fatalf("brain not called with transcript: %q", brain.calledWith)
	}
}

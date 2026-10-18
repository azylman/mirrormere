package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func (m *mockEmbedder) Embed(context.Context, []byte) ([]float64, string, error) {
	return m.emb, m.model, m.err
}

type mockIdentifier struct {
	id    string
	score float64
	err   error
	delay time.Duration
}

func (m *mockIdentifier) Identify(ctx context.Context, _ []byte) (SpeakerMatch, error) {
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return SpeakerMatch{}, ctx.Err()
		}
	}
	return SpeakerMatch{Speaker: m.id, Score: m.score}, m.err
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read error") }

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
	tests := []struct {
		name    string
		a, b    []float64
		want    float64
		wantErr bool
	}{
		{"identical", []float64{1, 0}, []float64{1, 0}, 1.0, false},
		{"orthogonal", []float64{1, 0}, []float64{0, 1}, 0.0, false},
		{"dim_mismatch", []float64{1}, []float64{1, 0}, 0, true},
		{"zero_vector", []float64{0, 0}, []float64{1, 0}, 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sim, err := CosineSimilarity(tc.a, tc.b)
			if (err != nil) != tc.wantErr || (!tc.wantErr && math.Abs(sim-tc.want) > 0.001) {
				t.Fatalf("sim=%v err=%v", sim, err)
			}
		})
	}
}

func TestMatchSpeaker(t *testing.T) {
	t.Parallel()
	spk := []Fingerprint{{ID: "mike", Embedding: []float64{1, 0, 0}}, {ID: "lauren", Embedding: []float64{0, 1, 0}}}
	mixed := []Fingerprint{{ID: "broken", Embedding: []float64{1, 0}}, spk[0]}
	tests := []struct {
		name    string
		emb     []float64
		list    []Fingerprint
		wantID  string
		minSim  float64
		wantErr bool
	}{
		{"match_first", []float64{0.9, 0.1, 0}, spk, "mike", 0.9, false},
		{"match_second", []float64{0.1, 0.95, 0}, spk, "lauren", 0.7, false},
		{"below_threshold", []float64{0, 0, 1}, spk, "", 0, false},
		{"none_comparable", []float64{1, 0}, spk, "", 0, true},
		{"skip_uncomparable", []float64{1, 0, 0}, mixed, "mike", 0.9, false},
		{"nan_embedding", []float64{math.NaN(), 0, 0}, spk, "", 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			id, sim, err := MatchSpeaker(tc.emb, tc.list, 0.7)
			if (err != nil) != tc.wantErr || id != tc.wantID || (tc.minSim > 0 && sim < tc.minSim) {
				t.Fatalf("id=%q sim=%v err=%v", id, sim, err)
			}
		})
	}
}

func TestFingerprintStore(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "reload.json")
	writeFingerprints(t, p, FingerprintFile{Model: "ecapa", Speakers: []Fingerprint{{ID: "mike", Embedding: []float64{1, 0}}}})
	store := NewFingerprintStore(p)
	if f, err := store.Load(); err != nil || len(f.Speakers) != 1 {
		t.Fatalf("first load: %+v err=%v", f, err)
	}
	writeFingerprints(t, p, FingerprintFile{Model: "ecapa", Speakers: []Fingerprint{{ID: "mike", Embedding: []float64{1, 0}}, {ID: "lauren", Embedding: []float64{0, 1}}}})
	fut := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(p, fut, fut)
	if f, err := store.Load(); err != nil || len(f.Speakers) != 2 {
		t.Fatalf("reload: %+v err=%v", f, err)
	}

	badJSON := filepath.Join(dir, "bad.json")
	_ = os.WriteFile(badJSON, []byte("{not json"), 0o644)
	for name, f := range map[string]FingerprintFile{
		"empty_id":        {Speakers: []Fingerprint{{ID: "", Embedding: []float64{1}}}},
		"duplicate_id":    {Speakers: []Fingerprint{{ID: "a", Embedding: []float64{1}}, {ID: "a", Embedding: []float64{1}}}},
		"empty_embedding": {Speakers: []Fingerprint{{ID: "a"}}},
		"mixed_dim":       {Speakers: []Fingerprint{{ID: "a", Embedding: []float64{1, 0}}, {ID: "b", Embedding: []float64{1}}}},
		"zero_vector":     {Speakers: []Fingerprint{{ID: "a", Embedding: []float64{0, 0}}}},
	} {
		path := filepath.Join(dir, name+".json")
		writeFingerprints(t, path, f)
		if _, err := NewFingerprintStore(path).Load(); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if _, err := NewFingerprintStore(badJSON).Load(); err == nil {
		t.Error("bad json: expected error")
	}
	if _, err := NewFingerprintStore(filepath.Join(dir, "missing.json")).Load(); err == nil {
		t.Error("missing: expected error")
	}
}

func TestFingerprintIdentifier(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "speakers.json")
	writeFingerprints(t, path, FingerprintFile{Model: "ecapa", Speakers: []Fingerprint{{ID: "mike", Embedding: []float64{1, 0}}, {ID: "lauren", Embedding: []float64{0, 1}}}})
	store := NewFingerprintStore(path)
	emptyPath := filepath.Join(dir, "empty.json")
	writeFingerprints(t, emptyPath, FingerprintFile{Model: "ecapa"})

	tests := []struct {
		name        string
		emb         SpeakerEmbedder
		store       *FingerprintStore
		wantSpeaker string
		wantErr     bool
	}{
		{"match_speaker", &mockEmbedder{emb: []float64{0.2, 0.98}, model: "ecapa"}, store, "lauren", false},
		{"model_mismatch", &mockEmbedder{emb: []float64{0, 1}, model: "wavlm"}, store, "", true},
		{"embed_error", &mockEmbedder{err: errors.New("down")}, store, "", true},
		{"empty_store", &mockEmbedder{err: errors.New("unused")}, NewFingerprintStore(emptyPath), "", false},
		{"missing_store", &mockEmbedder{}, NewFingerprintStore(filepath.Join(dir, "missing.json")), "", true},
		{"zero_embedding", &mockEmbedder{emb: []float64{0, 0}}, store, "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m, err := NewFingerprintIdentifier(tc.emb, tc.store, 0.7).Identify(context.Background(), nil)
			if (err != nil) != tc.wantErr || m.Speaker != tc.wantSpeaker {
				t.Fatalf("got %q err=%v, want %q wantErr=%v", m.Speaker, err, tc.wantSpeaker, tc.wantErr)
			}
		})
	}
	if NewSpeakerIdentifierFromConfig(nil) != nil ||
		NewSpeakerIdentifierFromConfig(&config.SpeakerIDConfig{EmbedURL: "http://x"}) != nil ||
		NewSpeakerIdentifierFromConfig(&config.SpeakerIDConfig{EmbedURL: "http://x", FingerprintsPath: "/f.json"}) == nil {
		t.Fatal("NewSpeakerIdentifierFromConfig failed")
	}
	cfg := &config.VoiceHubConfig{Enabled: true, SpeakerID: &config.SpeakerIDConfig{EmbedURL: "http://x", FingerprintsPath: "/f.json"}}
	if h := NewHub(cfg, nil); h.speaker == nil {
		t.Fatal("expected speaker identifier wired")
	}
	if h := NewHub(&config.VoiceHubConfig{Enabled: true}, nil); h.speaker != nil {
		t.Fatal("expected nil speaker identifier")
	}
}

func TestDefaultEmbedClient(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		rawURL    string
		status    int
		body      string
		wavData   []byte
		wantModel string
		wantErr   bool
	}{
		{"success", "", 200, `{"embedding":[0.5,0.25],"model":"ecapa"}`, []byte("RIFFwav"), "ecapa", false},
		{"http_500", "", 500, "boom", nil, "", true},
		{"empty_embedding", "", 200, `{"embedding":[]}`, nil, "", true},
		{"malformed_json", "", 200, "not json", nil, "", true},
		{"empty_url", "  ", 0, "", nil, "", true},
		{"unreachable", "http://127.0.0.1:1", 0, "", nil, "", true},
		{"invalid_url", "http://bad host", 0, "", nil, "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			url := tc.rawURL
			if url == "" {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if tc.wavData != nil && (!bytes.Equal(tc.wavData, []byte("RIFFwav")) || r.Header.Get("Content-Type") != "audio/wav") {
						t.Errorf("unexpected req")
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.body))
				}))
				defer srv.Close()
				url = srv.URL
			}
			emb, model, err := NewDefaultEmbedClient(url, 1).Embed(context.Background(), tc.wavData)
			if (err != nil) != tc.wantErr || (!tc.wantErr && (model != tc.wantModel || len(emb) != 2)) {
				t.Fatalf("emb=%v model=%q err=%v", emb, model, err)
			}
		})
	}
	broken := &http.Response{StatusCode: http.StatusBadGateway, Body: io.NopCloser(errReader{})}
	if err := readHTTPError(broken, "test"); err == nil || !strings.Contains(err.Error(), "unknown error") {
		t.Fatalf("expected unknown error, got %v", err)
	}
}

func TestHub_SpeakerIntegration(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		stt        string
		sttErr     error
		spkID      string
		spkErr     error
		spkDelay   time.Duration
		checkBrain bool
		wantSpk    string
		wantErr    bool
	}{
		{"carried_to_brain", "what's on my calendar", nil, "mike", nil, 0, true, "mike", false},
		{"failure_not_fatal", "hello", nil, "", errors.New("down"), 0, false, "", false},
		{"slow_speaker", "hello", nil, "mike", nil, 10 * time.Second, false, "", false},
		{"stt_error", "", errors.New("whisper down"), "mike", nil, 10 * time.Second, false, "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var brainBody map[string]any
			var brainMu sync.Mutex
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				brainMu.Lock()
				_ = json.NewDecoder(r.Body).Decode(&brainBody)
				brainMu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"reply":"ok"}`))
			}))
			defer srv.Close()

			h := NewHub(&config.VoiceHubConfig{Enabled: true}, nil,
				WithSTTClient(&mockSTT{text: tc.stt, err: tc.sttErr}),
				WithBrainClient(NewDefaultBrainClient(srv.URL, 5)),
				WithTTSClient(&mockTTS{}),
				WithSpeakerIdentifier(&mockIdentifier{id: tc.spkID, score: 0.83, err: tc.spkErr, delay: tc.spkDelay}),
			)
			events := map[string]any{}
			var mu sync.Mutex
			start := time.Now()
			err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(1600)), "eink-display-livingroom", "sess-1", func(ev string, d any) error {
				mu.Lock()
				events[ev] = d
				mu.Unlock()
				return nil
			}, EdgeTimings{})

			if (err != nil) != tc.wantErr || (tc.wantErr && time.Since(start) > time.Second) {
				t.Fatalf("interact: err=%v elapsed=%v", err, time.Since(start))
			}
			if !tc.wantErr {
				mu.Lock()
				tr, _ := events["transcript"].(map[string]string)
				done := events["done"]
				mu.Unlock()
				if tr["speaker"] != tc.wantSpk || done == nil {
					t.Fatalf("transcript: %+v done=%v", tr, done)
				}
				brainMu.Lock()
				bb := brainBody
				brainMu.Unlock()
				if tc.checkBrain && (bb["speaker"] != "mike" || bb["speaker_score"] != 0.83) {
					t.Fatalf("brain body: %+v", bb)
				}
			}
		})
	}
}

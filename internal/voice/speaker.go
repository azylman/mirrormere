package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/azylman/mirrormere/internal/config"
)

// SpeakerEmbedder turns an utterance into a voice embedding.
type SpeakerEmbedder interface {
	// Embed returns the embedding vector and the model identifier that produced it.
	Embed(ctx context.Context, wavData []byte) ([]float64, string, error)
}

// SpeakerMatch is the result of speaker identification. Speaker is a hint for
// personalisation, not authentication (SPEC-011 §Speaker Identification).
type SpeakerMatch struct {
	// Speaker is the matched enrolled id, or "" when nobody cleared the threshold.
	Speaker string
	// Score is the best cosine similarity seen (0 when matching did not run).
	Score float64
}

// SpeakerIdentifier resolves the speaker of an utterance. An error is never
// fatal to the voice turn.
type SpeakerIdentifier interface {
	Identify(ctx context.Context, wavData []byte) (SpeakerMatch, error)
}

// Fingerprint is one enrolled speaker.
type Fingerprint struct {
	ID        string    `json:"id"`
	Embedding []float64 `json:"embedding"`
}

// FingerprintFile is the on-disk enrollment store (SPEC-011 §Speaker Identification).
type FingerprintFile struct {
	// Model is the embedding model the fingerprints were generated with.
	// Embeddings from different models are not comparable.
	Model    string        `json:"model"`
	Speakers []Fingerprint `json:"speakers"`
}

// FingerprintStore loads the fingerprint file and reloads it when it changes on disk.
type FingerprintStore struct {
	path string

	mu      sync.Mutex
	modTime time.Time
	size    int64
	loaded  *FingerprintFile
}

// NewFingerprintStore constructs a store for the given file path.
func NewFingerprintStore(path string) *FingerprintStore {
	return &FingerprintStore{path: strings.TrimSpace(path)}
}

// Load returns the current fingerprints, re-reading the file if its mtime or size changed.
func (s *FingerprintStore) Load() (*FingerprintFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	info, err := os.Stat(s.path)
	if err != nil {
		return nil, fmt.Errorf("fingerprints: %w", err)
	}
	if s.loaded != nil && info.ModTime().Equal(s.modTime) && info.Size() == s.size {
		return s.loaded, nil
	}

	raw, err := os.ReadFile(s.path)
	if err != nil {
		return nil, fmt.Errorf("fingerprints: %w", err)
	}
	var f FingerprintFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("fingerprints: parse %s: %w", s.path, err)
	}
	seen := make(map[string]bool, len(f.Speakers))
	dim := 0
	for i, sp := range f.Speakers {
		if strings.TrimSpace(sp.ID) == "" {
			return nil, fmt.Errorf("fingerprints: speaker %d has an empty id", i)
		}
		if seen[sp.ID] {
			return nil, fmt.Errorf("fingerprints: duplicate speaker id %q", sp.ID)
		}
		seen[sp.ID] = true
		if len(sp.Embedding) == 0 {
			return nil, fmt.Errorf("fingerprints: speaker %q has an empty embedding", sp.ID)
		}
		if dim == 0 {
			dim = len(sp.Embedding)
		} else if len(sp.Embedding) != dim {
			return nil, fmt.Errorf("fingerprints: speaker %q has dimension %d, expected %d", sp.ID, len(sp.Embedding), dim)
		}
		if err := checkVector(sp.Embedding); err != nil {
			return nil, fmt.Errorf("fingerprints: speaker %q: %w", sp.ID, err)
		}
	}

	s.loaded = &f
	s.modTime = info.ModTime()
	s.size = info.Size()
	return s.loaded, nil
}

// checkVector rejects vectors that cannot be compared: non-finite values or zero magnitude.
func checkVector(v []float64) error {
	var norm float64
	for _, x := range v {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return errors.New("embedding contains NaN or Inf")
		}
		norm += x * x
	}
	if norm == 0 {
		return errors.New("embedding has zero magnitude")
	}
	return nil
}

// CosineSimilarity returns the cosine similarity of two equal-length vectors.
func CosineSimilarity(a, b []float64) (float64, error) {
	if len(a) != len(b) {
		return 0, fmt.Errorf("dimension mismatch: %d vs %d", len(a), len(b))
	}
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0, errors.New("zero-length vector")
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb)), nil
}

// MatchSpeaker returns the enrolled speaker most similar to the embedding, if
// that similarity is at least threshold. It returns "" when nobody qualifies.
// The utterance embedding must be finite and non-zero; a fingerprint that
// cannot be compared is skipped so it cannot block matching for everyone else.
func MatchSpeaker(embedding []float64, speakers []Fingerprint, threshold float64) (string, float64, error) {
	if err := checkVector(embedding); err != nil {
		return "", 0, fmt.Errorf("utterance: %w", err)
	}
	bestID := ""
	best := math.Inf(-1)
	for _, sp := range speakers {
		sim, err := CosineSimilarity(embedding, sp.Embedding)
		if err != nil || math.IsNaN(sim) {
			slog.Warn("skipping uncomparable fingerprint", "speaker", sp.ID, "error", err)
			continue
		}
		if sim > best {
			best, bestID = sim, sp.ID
		}
	}
	if bestID == "" {
		return "", 0, errors.New("no enrolled fingerprint is comparable with the utterance embedding")
	}
	if best < threshold {
		return "", best, nil
	}
	return bestID, best, nil
}

// FingerprintIdentifier matches utterances against a FingerprintStore.
type FingerprintIdentifier struct {
	embedder  SpeakerEmbedder
	store     *FingerprintStore
	threshold float64
}

// NewFingerprintIdentifier constructs an identifier from an embedder, store and threshold.
func NewFingerprintIdentifier(embedder SpeakerEmbedder, store *FingerprintStore, threshold float64) *FingerprintIdentifier {
	return &FingerprintIdentifier{embedder: embedder, store: store, threshold: threshold}
}

// NewSpeakerIdentifierFromConfig returns nil when speaker matching is not configured.
func NewSpeakerIdentifierFromConfig(cfg *config.SpeakerIDConfig) SpeakerIdentifier {
	if !cfg.IsEnabled() {
		return nil
	}
	return NewFingerprintIdentifier(
		NewDefaultEmbedClient(cfg.EmbedURL, cfg.GetTimeoutSeconds()),
		NewFingerprintStore(cfg.FingerprintsPath),
		cfg.GetThreshold(),
	)
}

// Identify embeds the utterance and matches it against enrolled fingerprints.
func (f *FingerprintIdentifier) Identify(ctx context.Context, wavData []byte) (SpeakerMatch, error) {
	file, err := f.store.Load()
	if err != nil {
		return SpeakerMatch{}, err
	}
	if len(file.Speakers) == 0 {
		return SpeakerMatch{}, nil
	}
	emb, model, err := f.embedder.Embed(ctx, wavData)
	if err != nil {
		return SpeakerMatch{}, fmt.Errorf("embed: %w", err)
	}
	if file.Model != "" && model != "" && file.Model != model {
		return SpeakerMatch{}, fmt.Errorf("model mismatch: embedding model %q does not match fingerprint model %q", model, file.Model)
	}
	id, score, err := MatchSpeaker(emb, file.Speakers, f.threshold)
	if err != nil {
		return SpeakerMatch{}, err
	}
	return SpeakerMatch{Speaker: id, Score: score}, nil
}

// DefaultEmbedClient calls an HTTP speaker-embedding endpoint.
//
// Request:  POST <url>, Content-Type: audio/wav, body = raw WAV bytes.
// Response: 200 {"embedding": [float...], "model": "<model id>"}.
type DefaultEmbedClient struct {
	url        string
	httpClient *http.Client
}

// NewDefaultEmbedClient constructs an embed client with the given timeout.
func NewDefaultEmbedClient(url string, timeoutSec int) *DefaultEmbedClient {
	return &DefaultEmbedClient{
		url:        strings.TrimSpace(url),
		httpClient: &http.Client{Timeout: time.Duration(timeoutSec) * time.Second},
	}
}

// Embed posts the WAV to the embed endpoint.
func (c *DefaultEmbedClient) Embed(ctx context.Context, wavData []byte) ([]float64, string, error) {
	if c.url == "" {
		return nil, "", errors.New("embed url is empty")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(wavData))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "audio/wav")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, "", fmt.Errorf("embed http error %d: %s", resp.StatusCode, string(body))
	}
	var out struct {
		Embedding []float64 `json:"embedding"`
		Model     string    `json:"model"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, "", err
	}
	if len(out.Embedding) == 0 {
		return nil, "", errors.New("embed response has an empty embedding")
	}
	return out.Embedding, out.Model, nil
}

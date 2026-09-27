package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// SpeakerIdentifier resolves the speaker of an utterance. It returns an empty
// string when nobody matches; an error is never fatal to the voice turn.
type SpeakerIdentifier interface {
	Identify(ctx context.Context, wavData []byte) (string, error)
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
	}

	s.loaded = &f
	s.modTime = info.ModTime()
	s.size = info.Size()
	return s.loaded, nil
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
func MatchSpeaker(embedding []float64, speakers []Fingerprint, threshold float64) (string, float64, error) {
	bestID := ""
	best := math.Inf(-1)
	for _, sp := range speakers {
		sim, err := CosineSimilarity(embedding, sp.Embedding)
		if err != nil {
			return "", 0, fmt.Errorf("speaker %q: %w", sp.ID, err)
		}
		if sim > best {
			best, bestID = sim, sp.ID
		}
	}
	if bestID == "" || best < threshold {
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
func (f *FingerprintIdentifier) Identify(ctx context.Context, wavData []byte) (string, error) {
	file, err := f.store.Load()
	if err != nil {
		return "", err
	}
	if len(file.Speakers) == 0 {
		return "", nil
	}
	emb, model, err := f.embedder.Embed(ctx, wavData)
	if err != nil {
		return "", fmt.Errorf("embed: %w", err)
	}
	if file.Model != "" && model != "" && file.Model != model {
		return "", fmt.Errorf("embedding model %q does not match fingerprint model %q", model, file.Model)
	}
	id, _, err := MatchSpeaker(emb, file.Speakers, f.threshold)
	return id, err
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

// TurnMeta is request-scoped metadata about the current voice turn, carried
// to the brain client through the context.
type TurnMeta struct {
	NodeID  string
	Speaker string
}

type turnMetaKey struct{}

// WithTurnMeta returns a context carrying the turn metadata.
func WithTurnMeta(ctx context.Context, meta TurnMeta) context.Context {
	return context.WithValue(ctx, turnMetaKey{}, meta)
}

// TurnMetaFrom returns the turn metadata from the context, if any.
func TurnMetaFrom(ctx context.Context) (TurnMeta, bool) {
	m, ok := ctx.Value(turnMetaKey{}).(TurnMeta)
	return m, ok
}

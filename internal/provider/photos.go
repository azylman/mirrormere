package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultPhotoCycleIntervalSeconds = 60
	minPhotoCycleIntervalSeconds     = 5
	defaultPhotoPreloadCount         = 50
	maxPhotoPreloadCount             = 500
	maxPhotoAlbumBytes               = 5 * 1024 * 1024 // 5MB Slowloris defense ceiling
)

// Standard error definitions for Google Photos ingestion.
var (
	ErrAlbumPrivate   = errors.New("shared album requires authentication or link is expired")
	ErrAlbumRateLimit = errors.New("google photos request rate-limited or blocked by captcha")
	ErrAlbumNotFound  = errors.New("shared album not found")
)

// PhotoSnapshot represents the normalized photo carousel data emitted over SSE.
// Complies with SPEC-007 §3.
type PhotoSnapshot struct {
	AlbumName            string      `json:"album_name"`
	TotalPhotos          int         `json:"total_photos"`
	CycleIntervalSeconds int         `json:"cycle_interval_seconds"`
	Photos               []PhotoItem `json:"photos"`
}

// PhotoItem represents a single photograph within a shared album.
// Contains clean CDN base URL without static sizing parameters.
type PhotoItem struct {
	ID          string  `json:"id"`
	URL         string  `json:"url"` // Clean base URL e.g. https://lh3.googleusercontent.com/pw/...
	Timestamp   string  `json:"timestamp,omitempty"`
	AspectRatio float64 `json:"aspect_ratio"`
}

// PhotosConfig holds parsed configuration for PhotosProvider.
type PhotosConfig struct {
	ShareURL             string
	ShareURLEnv          string
	CycleIntervalSeconds int
	PreloadCount         int
	Shuffle              bool
	ImageSize            [2]int
}

// PhotosProvider implements Provider for Google Photos unlisted shared album carousels.
// Complies with SPEC-003 §1, SPEC-007 §3, and Phase 3 Chunk 3.3D.
type PhotosProvider struct {
	mu             sync.RWMutex
	cfg            PhotosConfig
	resolvedURL    string
	cachedSnapshot *PhotoSnapshot
	client         *http.Client
	logger         *slog.Logger
	randSource     *rand.Rand
}

// NewPhotosProvider constructs an uninitialized PhotosProvider.
func NewPhotosProvider() *PhotosProvider {
	return &PhotosProvider{
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
		logger:     slog.Default().With("provider", "photo-carousel"),
		randSource: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Init parses configuration, resolves 3-tier secrets, and validates parameters.
func (p *PhotosProvider) Init(_ context.Context, config map[string]any, opts InitOptions) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	parsed, err := parsePhotosConfig(config, opts)
	if err != nil {
		return err
	}
	p.cfg = parsed

	// 3-Tier Secret Resolution Pipeline:
	// Tier 1: opts.Secrets["share_url"] or parsed.ShareURL
	// Tier 2: opts.GetSecret(parsed.ShareURLEnv)
	// Tier 3: os.Getenv(parsed.ShareURLEnv)
	var resolvedURL string
	if parsed.ShareURLEnv != "" {
		if sec := opts.GetSecret(parsed.ShareURLEnv); sec != "" {
			resolvedURL = strings.TrimSpace(sec)
		} else if env := os.Getenv(parsed.ShareURLEnv); env != "" {
			resolvedURL = strings.TrimSpace(env)
		} else {
			return fmt.Errorf("photo-carousel: environment variable %q specified in 'share_url_env' is not set or empty", parsed.ShareURLEnv)
		}
	} else if parsed.ShareURL != "" {
		if sec := opts.GetSecret("share_url"); sec != "" {
			resolvedURL = strings.TrimSpace(sec)
		} else {
			resolvedURL = parsed.ShareURL
		}
	} else {
		return fmt.Errorf("photo-carousel requires either 'share_url' or 'share_url_env'")
	}

	if resolvedURL == "" {
		return fmt.Errorf("photo-carousel: resolved share URL is empty")
	}

	parsedU, err := url.Parse(resolvedURL)
	if err != nil || (parsedU.Scheme != "http" && parsedU.Scheme != "https") {
		return fmt.Errorf("photo-carousel: invalid share URL %q", redactURL(resolvedURL))
	}

	p.resolvedURL = resolvedURL
	return nil
}

// Fetch executes an album synchronization cycle with SWR caching.
func (p *PhotosProvider) Fetch(ctx context.Context) (any, error) {
	p.mu.RLock()
	targetURL := p.resolvedURL
	cfg := p.cfg
	cached := p.cachedSnapshot
	client := p.client
	logger := p.logger
	p.mu.RUnlock()

	if targetURL == "" {
		return nil, fmt.Errorf("photo-carousel: provider not initialized with target URL")
	}

	snap, err := p.scrapeAlbum(ctx, client, targetURL, cfg, logger)
	if err != nil {
		if cached != nil {
			logger.Warn("photo-carousel upstream scrape failed; returning cached SWR snapshot",
				"error", err,
				"url", redactURL(targetURL),
				"cached_photos", len(cached.Photos))
			return cached, nil
		}
		return nil, fmt.Errorf("photo-carousel scrape failed: %w", err)
	}

	p.mu.Lock()
	p.cachedSnapshot = snap
	p.mu.Unlock()

	return snap, nil
}

// Subscribe is a no-op for polling photo-carousel providers.
func (p *PhotosProvider) Subscribe(_ context.Context, _ chan<- WidgetPayload) error {
	return nil
}

// Shutdown cleans up resources.
func (p *PhotosProvider) Shutdown(_ context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cachedSnapshot = nil
	return nil
}

// scrapeAlbum fetches the shared album HTML, follows redirects, extracts image entries, and builds PhotoSnapshot.
func (p *PhotosProvider) scrapeAlbum(ctx context.Context, client *http.Client, albumURL string, cfg PhotosConfig, logger *slog.Logger) (*PhotoSnapshot, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, albumURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Browser User-Agent and Accept-Language per Girl Gang recommendations
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request to %s failed: %w", redactURL(albumURL), err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Detect redirect to login wall, cookie consent, or CAPTCHA blocks
	if resp.Request != nil && resp.Request.URL != nil {
		finalHost := resp.Request.URL.Host
		if strings.Contains(finalHost, "accounts.google.com") {
			return nil, ErrAlbumPrivate
		}
		if strings.Contains(finalHost, "sorry.google.com") {
			return nil, ErrAlbumRateLimit
		}
		if strings.Contains(finalHost, "consent.google.com") {
			return nil, ErrAlbumPrivate
		}
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrAlbumNotFound
	}
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrAlbumPrivate
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, ErrAlbumRateLimit
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("upstream Google Photos returned HTTP %d", resp.StatusCode)
	}

	// Slowloris defense: enforce 5MB ceiling
	limitReader := io.LimitReader(resp.Body, maxPhotoAlbumBytes+1)
	bodyBytes, err := io.ReadAll(limitReader)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	if len(bodyBytes) > maxPhotoAlbumBytes {
		return nil, fmt.Errorf("google photos payload exceeded %d byte ceiling", maxPhotoAlbumBytes)
	}

	html := string(bodyBytes)
	albumName := extractAlbumTitle(html)
	photos := extractPhotosFromHTML(html, logger)

	totalFound := len(photos)

	// Shuffling
	if cfg.Shuffle && len(photos) > 1 {
		p.mu.Lock()
		r := p.randSource
		if r == nil {
			r = rand.New(rand.NewSource(time.Now().UnixNano()))
		}
		r.Shuffle(len(photos), func(i, j int) {
			photos[i], photos[j] = photos[j], photos[i]
		})
		p.mu.Unlock()
	}

	// Preload count clamping
	if cfg.PreloadCount > 0 && len(photos) > cfg.PreloadCount {
		photos = photos[:cfg.PreloadCount]
	}

	return &PhotoSnapshot{
		AlbumName:            albumName,
		TotalPhotos:          totalFound,
		CycleIntervalSeconds: cfg.CycleIntervalSeconds,
		Photos:               photos,
	}, nil
}

// Regex patterns for metadata and photo extraction.
var (
	ogTitleRe  = regexp.MustCompile(`(?i)<meta\s+property=["']og:title["']\s+content=["']([^"']+)["']`)
	titleTagRe = regexp.MustCompile(`(?i)<title>([^<]+)</title>`)

	// AF_initDataCallback invocation prefix
	afCallbackPrefixRe = regexp.MustCompile(`(?i)AF_initDataCallback\s*\(`)

	// Fallback regex matching Google Photos user CDN URLs (/pw/ path)
	pwURLRe = regexp.MustCompile(`https:\/\/lh[0-9]*\.googleusercontent\.com\/pw\/[a-zA-Z0-9_\-]+`)
)

// extractAlbumTitle discovers the album name from og:title or title tag, stripping Google Photos branding.
func extractAlbumTitle(html string) string {
	if m := ogTitleRe.FindStringSubmatch(html); len(m) > 1 {
		title := strings.TrimSpace(m[1])
		title = strings.TrimSuffix(title, " - Google Photos")
		title = strings.TrimSuffix(title, " - Google")
		if title != "" {
			return title
		}
	}
	if m := titleTagRe.FindStringSubmatch(html); len(m) > 1 {
		title := strings.TrimSpace(m[1])
		title = strings.TrimSuffix(title, " - Google Photos")
		title = strings.TrimSuffix(title, " - Google")
		if title != "" {
			return title
		}
	}
	return "Shared Album"
}

// extractPhotosFromHTML implements the multi-strategy photo extraction pipeline.
func extractPhotosFromHTML(html string, logger *slog.Logger) []PhotoItem {
	// Strategy 1: AF_initDataCallback array extraction
	photos := extractFromAFInitData(html)
	if len(photos) > 0 {
		return photos
	}

	// Strategy 2: Fallback regex on /pw/ image CDN URLs
	photos = extractFromFallbackRegex(html)
	if len(photos) > 0 {
		return photos
	}

	if logger != nil {
		logger.Debug("photo-carousel: no photos extracted from album HTML")
	}
	return []PhotoItem{}
}

// extractFromAFInitData searches for AF_initDataCallback blocks and extracts [id, [url, w, h], timestamp] items.
func extractFromAFInitData(html string) []PhotoItem {
	callbackIndices := afCallbackPrefixRe.FindAllStringIndex(html, -1)
	if len(callbackIndices) == 0 {
		return nil
	}

	seenURLs := make(map[string]struct{})
	var results []PhotoItem

	for _, idxRange := range callbackIndices {
		start := idxRange[0]
		end := start + 500*1024
		if end > len(html) {
			end = len(html)
		}
		chunk := html[start:end]

		dataIdx := strings.Index(chunk, "data:")
		if dataIdx == -1 {
			dataIdx = strings.Index(chunk, "data :")
		}
		if dataIdx == -1 {
			continue
		}

		arrayJSON, ok := extractBalancedArray(chunk[dataIdx:])
		if !ok {
			continue
		}

		var dataArray []any
		if err := json.Unmarshal([]byte(arrayJSON), &dataArray); err != nil {
			continue
		}

		// In Google Photos shared albums, items typically reside at data[1] or nested within data[0]
		items := findMediaItems(dataArray)
		for _, item := range items {
			photo, ok := parseMediaItem(item)
			if ok && photo.URL != "" {
				if _, seen := seenURLs[photo.URL]; !seen {
					seenURLs[photo.URL] = struct{}{}
					results = append(results, photo)
				}
			}
		}
	}

	return results
}

// extractBalancedArray scans for the first '[' and tracks nested brackets until depth returns to 0.
func extractBalancedArray(s string) (string, bool) {
	start := strings.IndexByte(s, '[')
	if start == -1 {
		return "", false
	}

	depth := 0
	inString := false
	var stringChar byte
	escaped := false

	for i := start; i < len(s); i++ {
		ch := s[i]

		if inString {
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == stringChar {
				inString = false
			}
			continue
		}

		if ch == '"' || ch == '\'' {
			inString = true
			stringChar = ch
			continue
		}

		if ch == '[' {
			depth++
		} else if ch == ']' {
			depth--
			if depth == 0 {
				return s[start : i+1], true
			}
		}
	}

	return "", false
}

// findMediaItems recursively inspects the parsed JSON array to locate lists of photo media items.
func findMediaItems(node any) []any {
	var candidates []any

	switch v := node.(type) {
	case []any:
		// Check if this array looks like a list of photo items (each entry is an array with URL at [1][0])
		if len(v) > 0 {
			matchesCount := 0
			for _, elem := range v {
				if elemArr, ok := elem.([]any); ok && len(elemArr) >= 2 {
					if mediaArr, ok := elemArr[1].([]any); ok && len(mediaArr) > 0 {
						if u, ok := mediaArr[0].(string); ok && strings.HasPrefix(u, "https://lh") {
							matchesCount++
						}
					}
				}
			}
			if matchesCount > 0 && matchesCount >= len(v)/2 {
				return v
			}
		}

		for _, elem := range v {
			if res := findMediaItems(elem); len(res) > 0 {
				return res
			}
		}
	case map[string]any:
		for _, val := range v {
			if res := findMediaItems(val); len(res) > 0 {
				return res
			}
		}
	}

	return candidates
}

// parseMediaItem parses an individual media item array: [id, [url, width, height], timestamp, ...]
func parseMediaItem(elem any) (PhotoItem, bool) {
	arr, ok := elem.([]any)
	if !ok || len(arr) < 2 {
		return PhotoItem{}, false
	}

	var id string
	if s, ok := arr[0].(string); ok {
		id = s
	}
	mediaInfo, ok := arr[1].([]any)
	if !ok || len(mediaInfo) == 0 {
		return PhotoItem{}, false
	}

	rawURL, ok := mediaInfo[0].(string)
	if !ok || !strings.HasPrefix(rawURL, "https://lh") {
		return PhotoItem{}, false
	}

	cleanURL := cleanGooglePhotoURL(rawURL)

	var width, height float64
	if len(mediaInfo) >= 3 {
		width, _ = toFloat(mediaInfo[1])
		height, _ = toFloat(mediaInfo[2])
	}

	aspectRatio := 1.33 // Default 4:3 aspect ratio per panel review
	if width > 0 && height > 0 {
		aspectRatio = math.Round((width/height)*100) / 100
	}

	var timestampStr string
	if len(arr) >= 3 {
		if tsMs, ok := toFloat(arr[2]); ok && tsMs > 0 {
			t := time.UnixMilli(int64(tsMs)).UTC()
			timestampStr = t.Format(time.RFC3339)
		}
	}

	if id == "" {
		id = fmt.Sprintf("photo_%d", time.Now().UnixNano())
	}

	return PhotoItem{
		ID:          id,
		URL:         cleanURL,
		Timestamp:   timestampStr,
		AspectRatio: aspectRatio,
	}, true
}

// extractFromFallbackRegex scans the HTML for user media URLs (/pw/ path) and extracts clean URLs.
func extractFromFallbackRegex(html string) []PhotoItem {
	matches := pwURLRe.FindAllString(html, -1)
	if len(matches) == 0 {
		return nil
	}

	seen := make(map[string]struct{})
	var results []PhotoItem

	for idx, rawURL := range matches {
		cleanURL := cleanGooglePhotoURL(rawURL)
		if _, exists := seen[cleanURL]; !exists {
			seen[cleanURL] = struct{}{}
			results = append(results, PhotoItem{
				ID:          fmt.Sprintf("photo_%d", idx+1),
				URL:         cleanURL,
				AspectRatio: 1.33,
			})
		}
	}

	return results
}

// cleanGooglePhotoURL strips trailing sizing parameters (e.g. =w1024-h768 or =s0) from CDN URLs.
func cleanGooglePhotoURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if idx := strings.Index(raw, "="); idx != -1 {
		return raw[:idx]
	}
	return raw
}

func toFloat(val any) (float64, bool) {
	switch v := val.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		if f, err := v.Float64(); err == nil {
			return f, true
		}
	case string:
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

// parsePhotosConfig parses and validates instance configuration.
func parsePhotosConfig(cfg map[string]any, _ InitOptions) (PhotosConfig, error) {
	res := PhotosConfig{
		CycleIntervalSeconds: defaultPhotoCycleIntervalSeconds,
		PreloadCount:         defaultPhotoPreloadCount,
		Shuffle:              true,
	}

	if cfg == nil {
		return res, nil
	}

	if v, ok := cfg["share_url"].(string); ok {
		res.ShareURL = strings.TrimSpace(v)
	}
	if v, ok := cfg["share_url_env"].(string); ok {
		res.ShareURLEnv = strings.TrimSpace(v)
	}

	// Mutual exclusivity check
	if res.ShareURL != "" && res.ShareURLEnv != "" {
		return res, fmt.Errorf("photo-carousel: 'share_url' and 'share_url_env' are mutually exclusive")
	}

	if v, ok := cfg["cycle_interval_seconds"].(int); ok && v > 0 {
		res.CycleIntervalSeconds = v
	} else if v, ok := cfg["cycle_interval_seconds"].(float64); ok && v > 0 {
		res.CycleIntervalSeconds = int(v)
	}
	if res.CycleIntervalSeconds < minPhotoCycleIntervalSeconds {
		res.CycleIntervalSeconds = minPhotoCycleIntervalSeconds
	}

	if v, ok := cfg["preload_count"].(int); ok && v > 0 {
		res.PreloadCount = v
	} else if v, ok := cfg["preload_count"].(float64); ok && v > 0 {
		res.PreloadCount = int(v)
	}
	if res.PreloadCount > maxPhotoPreloadCount {
		res.PreloadCount = maxPhotoPreloadCount
	}

	if v, ok := cfg["shuffle"].(bool); ok {
		res.Shuffle = v
	}

	if arr, ok := cfg["image_size"].([]any); ok && len(arr) == 2 {
		w, okW := toFloat(arr[0])
		h, okH := toFloat(arr[1])
		if okW && okH && w > 0 && h > 0 {
			res.ImageSize = [2]int{int(w), int(h)}
		}
	}

	return res, nil
}

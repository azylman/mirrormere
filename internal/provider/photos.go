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
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultPhotoCycleIntervalSeconds = 60
	minPhotoCycleIntervalSeconds     = 5
	defaultPhotoPreloadCount         = 500
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
type PhotosProvider struct {
	mu             sync.RWMutex
	cfg            PhotosConfig
	resolvedURL    string
	cachedSnapshot *PhotoSnapshot
	deck           []PhotoItem
	deckCursor     int
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

func resolvePhotoShareURL(parsed PhotosConfig, opts InitOptions) (string, error) {
	if parsed.ShareURLEnv != "" {
		if sec := opts.GetSecret(parsed.ShareURLEnv); sec != "" {
			return strings.TrimSpace(sec), nil
		}
		if env := os.Getenv(parsed.ShareURLEnv); env != "" {
			return strings.TrimSpace(env), nil
		}
		return "", fmt.Errorf("photo-carousel: environment variable %q specified in 'share_url_env' is not set or empty", parsed.ShareURLEnv)
	}
	if parsed.ShareURL != "" {
		if sec := opts.GetSecret("share_url"); sec != "" {
			return strings.TrimSpace(sec), nil
		}
		return parsed.ShareURL, nil
	}
	return "", fmt.Errorf("photo-carousel requires either 'share_url' or 'share_url_env'")
}

// Init parses configuration, resolves 3-tier secrets, and validates parameters.
func (p *PhotosProvider) Init(_ context.Context, config map[string]any, opts InitOptions) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.cachedSnapshot = nil
	p.deck = nil
	p.deckCursor = 0

	parsed, err := parsePhotosConfig(config, opts)
	if err != nil {
		return err
	}
	p.cfg = parsed

	resolvedURL, err := resolvePhotoShareURL(parsed, opts)
	if err != nil {
		return err
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
	p.deck = nil
	p.deckCursor = 0
	return nil
}

func checkAlbumResponseStatus(resp *http.Response) error {
	// Detect redirect to login wall, cookie consent, or CAPTCHA blocks
	if resp.Request != nil && resp.Request.URL != nil {
		finalHost := resp.Request.URL.Host
		if strings.Contains(finalHost, "accounts.google.com") {
			return ErrAlbumPrivate
		}
		if strings.Contains(finalHost, "sorry.google.com") {
			return ErrAlbumRateLimit
		}
		if strings.Contains(finalHost, "consent.google.com") {
			return ErrAlbumPrivate
		}
	}

	if resp.StatusCode == http.StatusNotFound {
		return ErrAlbumNotFound
	}
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		return ErrAlbumPrivate
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return ErrAlbumRateLimit
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("upstream Google Photos returned HTTP %d", resp.StatusCode)
	}
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

	if err := checkAlbumResponseStatus(resp); err != nil {
		return nil, err
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
	photos = p.selectAlbumWindow(photos, cfg)

	return &PhotoSnapshot{
		AlbumName:            albumName,
		TotalPhotos:          totalFound,
		CycleIntervalSeconds: cfg.CycleIntervalSeconds,
		Photos:               photos,
	}, nil
}

func photoItemKey(item PhotoItem) string {
	if item.URL != "" {
		return item.URL
	}
	return item.ID
}

func (p *PhotosProvider) reconcilePhotoDeck(scraped []PhotoItem, r *rand.Rand) {
	if r == nil {
		r = rand.New(rand.NewSource(time.Now().UnixNano()))
	}

	scrapedByKey := make(map[string]PhotoItem, len(scraped))
	for _, item := range scraped {
		scrapedByKey[photoItemKey(item)] = item
	}

	newDeck := make([]PhotoItem, 0, len(scraped))
	deckKeys := make(map[string]struct{}, len(p.deck))
	newCursor := p.deckCursor

	for i, item := range p.deck {
		key := photoItemKey(item)
		if updated, ok := scrapedByKey[key]; ok {
			newDeck = append(newDeck, updated)
			deckKeys[key] = struct{}{}
		} else if i < p.deckCursor {
			newCursor--
		}
	}
	if newCursor < 0 {
		newCursor = 0
	}
	if newCursor > len(newDeck) {
		newCursor = len(newDeck)
	}
	p.deckCursor = newCursor

	var newItems []PhotoItem
	for _, item := range scraped {
		key := photoItemKey(item)
		if _, exists := deckKeys[key]; !exists {
			newItems = append(newItems, item)
			deckKeys[key] = struct{}{}
		}
	}

	if len(newItems) > 0 {
		if len(newItems) > 1 {
			r.Shuffle(len(newItems), func(i, j int) {
				newItems[i], newItems[j] = newItems[j], newItems[i]
			})
		}
		newDeck = append(newDeck, newItems...)
	}

	p.deck = newDeck
}

func (p *PhotosProvider) selectAlbumWindow(scraped []PhotoItem, cfg PhotosConfig) []PhotoItem {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !cfg.Shuffle || len(scraped) <= 1 {
		p.deck = nil
		p.deckCursor = 0
		limit := len(scraped)
		if cfg.PreloadCount > 0 && limit > cfg.PreloadCount {
			limit = cfg.PreloadCount
		}
		return slices.Clone(scraped[:limit])
	}

	r := p.randSource
	if r == nil {
		r = rand.New(rand.NewSource(time.Now().UnixNano()))
		p.randSource = r
	}
	p.reconcilePhotoDeck(scraped, r)

	if p.deckCursor < 0 || p.deckCursor >= len(p.deck) {
		p.deckCursor = 0
	}

	limit := len(p.deck)
	if cfg.PreloadCount > 0 {
		limit = cfg.PreloadCount
	}

	if limit >= len(p.deck) {
		p.deckCursor = 0
		return slices.Clone(p.deck)
	}

	if p.deckCursor+limit < len(p.deck) {
		window := slices.Clone(p.deck[p.deckCursor : p.deckCursor+limit])
		p.deckCursor += limit
		return window
	}

	if p.deckCursor+limit == len(p.deck) {
		tail := slices.Clone(p.deck[p.deckCursor:])
		r.Shuffle(len(p.deck), func(i, j int) {
			p.deck[i], p.deck[j] = p.deck[j], p.deck[i]
		})
		if len(p.deck) > 1 && photoItemKey(p.deck[0]) == photoItemKey(tail[len(tail)-1]) {
			p.deck[0], p.deck[len(p.deck)-1] = p.deck[len(p.deck)-1], p.deck[0]
		}
		p.deckCursor = 0
		return tail
	}

	// Wrap-around: p.deckCursor+limit > len(p.deck)
	tail := slices.Clone(p.deck[p.deckCursor:])
	prefix := slices.Clone(p.deck[:p.deckCursor])
	r.Shuffle(len(prefix), func(i, j int) {
		prefix[i], prefix[j] = prefix[j], prefix[i]
	})
	p.deck = slices.Concat(tail, prefix)
	window := slices.Clone(p.deck[:limit])
	p.deckCursor = limit
	return window
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
	for _, re := range []*regexp.Regexp{ogTitleRe, titleTagRe} {
		if m := re.FindStringSubmatch(html); len(m) > 1 {
			title := strings.TrimSpace(m[1])
			title = strings.TrimSuffix(title, " - Google Photos")
			title = strings.TrimSuffix(title, " - Google")
			if title != "" {
				return title
			}
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

// extractFromCallback parses a single callback chunk for photo media items.
func extractFromCallback(chunk string, seenURLs map[string]struct{}) []PhotoItem {
	dataIdx := strings.Index(chunk, "data:")
	if dataIdx == -1 {
		dataIdx = strings.Index(chunk, "data :")
	}
	if dataIdx == -1 {
		return nil
	}

	arrayJSON, ok := extractBalancedArray(chunk[dataIdx:])
	if !ok {
		return nil
	}

	payloads := collectCallbackPayloads([][]string{{"", arrayJSON}})
	if len(payloads) == 0 {
		return nil
	}

	// In Google Photos shared albums, items typically reside at data[1] or nested within data[0]
	items := findMediaItems(payloads[0])
	var results []PhotoItem
	for _, item := range items {
		photo, ok := parseMediaItem(item)
		if ok && photo.URL != "" {
			if _, seen := seenURLs[photo.URL]; !seen {
				seenURLs[photo.URL] = struct{}{}
				results = append(results, photo)
			}
		}
	}
	return results
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
		end := min(start+500*1024, len(html))
		chunk := html[start:end]
		results = append(results, extractFromCallback(chunk, seenURLs)...)
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
			switch {
			case escaped:
				escaped = false
			case ch == '\\':
				escaped = true
			case ch == stringChar:
				inString = false
			}
			continue
		}

		if ch == '"' || ch == '\'' {
			inString = true
			stringChar = ch
			continue
		}

		switch ch {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return s[start : i+1], true
			}
		}
	}

	return "", false
}

func isPhotoItemArray(elem any) bool {
	elemArr, ok := elem.([]any)
	if !ok || len(elemArr) < 2 {
		return false
	}
	mediaArr, ok := elemArr[1].([]any)
	if !ok || len(mediaArr) == 0 {
		return false
	}
	u, ok := mediaArr[0].(string)
	return ok && strings.HasPrefix(u, "https://lh")
}

func isMediaItemList(v []any) bool {
	if len(v) == 0 {
		return false
	}
	matchesCount := 0
	for _, elem := range v {
		if isPhotoItemArray(elem) {
			matchesCount++
		}
	}
	return matchesCount > 0 && matchesCount >= len(v)/2
}

// findMediaItems recursively inspects the parsed JSON array to locate lists of photo media items.
func findMediaItems(node any) []any {
	switch v := node.(type) {
	case []any:
		if isMediaItemList(v) {
			return v
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

	return nil
}

func tryParseMediaSlice(arr []any) (PhotoItem, bool) {
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

// parseMediaItem parses an individual media item array: [id, [url, width, height], timestamp, ...]
func parseMediaItem(elem any) (PhotoItem, bool) {
	arr, ok := elem.([]any)
	if !ok || len(arr) < 2 {
		return PhotoItem{}, false
	}
	return tryParseMediaSlice(arr)
}

func collectCallbackPayloads(matches [][]string) [][]any {
	var payloads [][]any
	for _, m := range matches {
		if len(m) > 1 {
			var data []any
			if err := json.Unmarshal([]byte(m[1]), &data); err == nil {
				payloads = append(payloads, data)
			}
		}
	}
	return payloads
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
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

func positiveIntFromMap(cfg map[string]any, key string, def int) int {
	if cfg == nil {
		return def
	}
	switch v := cfg[key].(type) {
	case int:
		if v > 0 {
			return v
		}
	case float64:
		if v > 0 {
			return int(v)
		}
	}
	return def
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

	res.CycleIntervalSeconds = max(positiveIntFromMap(cfg, "cycle_interval_seconds", defaultPhotoCycleIntervalSeconds), minPhotoCycleIntervalSeconds)

	res.PreloadCount = min(positiveIntFromMap(cfg, "preload_count", defaultPhotoPreloadCount), maxPhotoPreloadCount)

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

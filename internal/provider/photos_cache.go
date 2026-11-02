package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	DefaultPhotosCacheDir         = "/data/photos"
	DefaultPhotosPage1Interval    = time.Hour
	DefaultPhotosFullSyncInterval = 24 * time.Hour
)

// PhotosOption configures PhotosProvider options.
type PhotosOption func(*PhotosProvider)

// WithPhotosCacheDir sets the default disk cache directory.
func WithPhotosCacheDir(dir string) PhotosOption {
	return func(p *PhotosProvider) {
		p.defaultCacheDir = dir
	}
}

// WithPhotosSyncIntervals sets the background sync intervals for Page 1 and full sync.
func WithPhotosSyncIntervals(page1Interval, fullSyncInterval time.Duration) PhotosOption {
	return func(p *PhotosProvider) {
		if page1Interval > 0 {
			p.page1Interval = page1Interval
		}
		if fullSyncInterval > 0 {
			p.fullSyncInterval = fullSyncInterval
		}
	}
}

// AlbumCacheFilename computes the sha256 hex filename for an album source.
func AlbumCacheFilename(source, resolvedURL string) string {
	h := sha256.Sum256([]byte(source + ":" + resolvedURL))
	return hex.EncodeToString(h[:]) + ".json"
}

func albumCacheFilename(source, resolvedURL string) string {
	return AlbumCacheFilename(source, resolvedURL)
}

// albumDiskCache represents the on-disk JSON cache format for an album.
type albumDiskCache struct {
	AlbumName     string      `json:"album_name"`
	ScrapedTitle  string      `json:"scraped_title"`
	ResolvedURL   string      `json:"resolved_url"`
	LastPage1Sync time.Time   `json:"last_page1_sync"`
	LastFullSync  time.Time   `json:"last_full_sync"`
	Photos        []PhotoItem `json:"photos"`
}

func loadAlbumDiskCache(cacheDir, source, resolvedURL string) (*albumDiskCache, error) {
	if cacheDir == "" {
		return nil, os.ErrNotExist
	}
	filePath := filepath.Join(cacheDir, albumCacheFilename(source, resolvedURL))
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	var cached albumDiskCache
	if err := json.Unmarshal(data, &cached); err != nil {
		return nil, err
	}
	return &cached, nil
}

func writeAlbumDiskCache(cacheDir, source, resolvedURL string, cache *albumDiskCache, logger *slog.Logger) {
	if cacheDir == "" || cache == nil {
		return
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		if logger != nil {
			logger.Warn("photo-carousel: failed to create cache directory", "error", err, "dir", cacheDir)
		}
		return
	}
	targetPath := filepath.Join(cacheDir, albumCacheFilename(source, resolvedURL))
	tmpFile, err := os.CreateTemp(cacheDir, "album-*.json.tmp")
	if err != nil {
		if logger != nil {
			logger.Warn("photo-carousel: failed to create temp cache file", "error", err)
		}
		return
	}
	tmpName := tmpFile.Name()
	defer func() {
		if err := os.Remove(tmpName); err != nil && !errors.Is(err, os.ErrNotExist) && logger != nil {
			logger.Debug("photo-carousel: failed to remove temp cache file", "error", err)
		}
	}()

	enc := json.NewEncoder(tmpFile)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cache); err != nil {
		if closeErr := tmpFile.Close(); closeErr != nil && logger != nil {
			logger.Debug("photo-carousel: failed to close temp cache file", "error", closeErr)
		}
		if logger != nil {
			logger.Warn("photo-carousel: failed to encode cache json", "error", err)
		}
		return
	}
	if err := tmpFile.Close(); err != nil {
		if logger != nil {
			logger.Warn("photo-carousel: failed to close temp cache file", "error", err)
		}
		return
	}
	if err := os.Rename(tmpName, targetPath); err != nil {
		if logger != nil {
			logger.Warn("photo-carousel: failed to rename temp cache file", "error", err, "path", targetPath)
		}
		return
	}
}

// photoDedupeKey returns the deduplication key according to the guardrail.
func photoDedupeKey(item PhotoItem) string {
	if clean := cleanGooglePhotoURL(item.URL); clean != "" {
		return clean
	}
	if !strings.HasPrefix(item.ID, "photo_") && item.ID != "" {
		return item.ID
	}
	return item.ID
}

// extractAlbumKeyAndToken extracts the albumKey and continuation token from HTML if present.
func extractAlbumKeyAndToken(html string) (string, string) {
	indices := afCallbackPrefixRe.FindAllStringIndex(html, -1)
	for _, idxRange := range indices {
		start := idxRange[0]
		end := min(start+500*1024, len(html))
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
		var arr []any
		if err := json.Unmarshal([]byte(arrayJSON), &arr); err != nil {
			continue
		}
		var albumKey, token string
		if len(arr) >= 1 {
			if s, ok := arr[0].(string); ok {
				albumKey = s
			}
		}
		if len(arr) >= 3 {
			if s, ok := arr[2].(string); ok && s != "" {
				token = s
			}
		}
		if albumKey != "" || token != "" {
			return albumKey, token
		}
	}
	return "", ""
}

// fetchBatchexecutePage queries /_/PhotosUi/data/batchexecute?rpcids=snAcKc for the next page.
func fetchBatchexecutePage(ctx context.Context, client *http.Client, albumURL, albumKey, token string) ([]PhotoItem, string, error) {
	baseURL := "https://photos.google.com"
	if parsedU, err := url.Parse(albumURL); err == nil && parsedU.Host != "" {
		baseURL = parsedU.Scheme + "://" + parsedU.Host
	}
	endpoint := baseURL + "/_/PhotosUi/data/batchexecute?rpcids=snAcKc"

	innerJSON, err := json.Marshal([]any{albumKey, token})
	if err != nil {
		return nil, "", err
	}
	envelope, err := json.Marshal([][][]any{
		{
			{"snAcKc", string(innerJSON), nil, "generic"},
		},
	})
	if err != nil {
		return nil, "", err
	}

	values := url.Values{}
	values.Set("f.req", string(envelope))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=utf-8")
	req.Header.Set("Accept", "*/*")

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("batchexecute returned HTTP %d", resp.StatusCode)
	}

	limitReader := io.LimitReader(resp.Body, maxPhotoAlbumBytes+1)
	bodyBytes, err := io.ReadAll(limitReader)
	if err != nil {
		return nil, "", err
	}
	if len(bodyBytes) > maxPhotoAlbumBytes {
		return nil, "", fmt.Errorf("batchexecute payload exceeded %d byte ceiling", maxPhotoAlbumBytes)
	}

	return parseBatchexecuteResponse(bodyBytes, token)
}

func parseBatchexecuteResponse(bodyBytes []byte, currentToken string) ([]PhotoItem, string, error) {
	bodyStr := strings.TrimSpace(string(bodyBytes))
	bodyStr = strings.TrimPrefix(bodyStr, ")]}'")
	bodyStr = strings.TrimSpace(bodyStr)

	var root any
	if err := json.Unmarshal([]byte(bodyStr), &root); err == nil {
		photos, nextToken := extractBatchexecutePayload(root, currentToken)
		if len(photos) > 0 {
			return photos, nextToken, nil
		}
	}

	photos := extractFromFallbackRegex(bodyStr)
	return photos, "", nil
}

func findBatchExecuteEnvelopes(node any) [][]any {
	v, ok := node.([]any)
	if !ok {
		return nil
	}
	if len(v) >= 3 {
		if s, ok := v[1].(string); ok && s == "snAcKc" {
			return [][]any{v}
		}
	}
	var envelopes [][]any
	for _, elem := range v {
		envelopes = append(envelopes, findBatchExecuteEnvelopes(elem)...)
	}
	return envelopes
}

func extractBatchexecutePayload(node any, currentToken string) ([]PhotoItem, string) {
	envelopes := findBatchExecuteEnvelopes(node)
	var photos []PhotoItem
	var nextToken string

	for _, elemArr := range envelopes {
		if innerStr, ok := elemArr[2].(string); ok && innerStr != "" {
			var inner any
			if err := json.Unmarshal([]byte(innerStr), &inner); err == nil {
				photos = append(photos, extractPhotosFromInner(inner)...)
				if tok := extractContinuationTokenFromInner(inner, currentToken); tok != "" {
					nextToken = tok
				}
			}
		}
	}

	if len(photos) == 0 {
		photos = extractPhotosFromInner(node)
		nextToken = extractContinuationTokenFromInner(node, currentToken)
	}
	return photos, nextToken
}

func extractPhotosFromInner(inner any) []PhotoItem {
	items := findMediaItems(inner)
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	var results []PhotoItem
	for _, item := range items {
		photo, ok := parseMediaItem(item)
		if ok && photo.URL != "" {
			if _, exists := seen[photo.URL]; !exists {
				seen[photo.URL] = struct{}{}
				results = append(results, photo)
			}
		}
	}
	return results
}

func extractContinuationTokenFromInner(inner any, currentToken string) string {
	arr, ok := inner.([]any)
	if !ok {
		return ""
	}
	for _, elem := range arr {
		if s, ok := elem.(string); ok {
			s = strings.TrimSpace(s)
			if s != "" && s != currentToken && !strings.HasPrefix(s, "http") {
				return s
			}
		}
	}
	return ""
}

func paginateSnAcKc(ctx context.Context, client *http.Client, albumURL, albumKey, initialToken string, logger *slog.Logger) []PhotoItem {
	if initialToken == "" {
		return nil
	}
	var allPaged []PhotoItem
	seenTokens := make(map[string]struct{})
	seenTokens[initialToken] = struct{}{}
	token := initialToken

	for page := range 50 {
		select {
		case <-ctx.Done():
			return allPaged
		default:
		}

		photos, nextToken, err := fetchBatchexecutePage(ctx, client, albumURL, albumKey, token)
		if err != nil {
			if logger != nil {
				logger.Warn("photo-carousel: snAcKc pagination error; retaining earlier pages", "error", err, "page", page)
			}
			break
		}
		if len(photos) == 0 {
			break
		}
		allPaged = append(allPaged, photos...)

		nextToken = strings.TrimSpace(nextToken)
		if nextToken == "" || nextToken == token {
			break
		}
		if _, seen := seenTokens[nextToken]; seen {
			break
		}
		seenTokens[nextToken] = struct{}{}
		token = nextToken
	}

	return allPaged
}

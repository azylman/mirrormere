package provider

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

func resolveRawSingleAlbumURL(idx int, a AlbumSource, opts InitOptions) string {
	if sec := opts.GetSecret(fmt.Sprintf("albums[%d].url", idx)); sec != "" {
		return strings.TrimSpace(sec)
	}
	if a.URLEnv != "" {
		if sec := opts.GetSecret(a.URLEnv); sec != "" {
			return strings.TrimSpace(sec)
		}
		if env := os.Getenv(a.URLEnv); env != "" {
			return strings.TrimSpace(env)
		}
		return ""
	}
	return a.URL
}

func resolveSingleAlbumURL(idx int, a AlbumSource, opts InitOptions) (string, error) {
	resolvedURL := resolveRawSingleAlbumURL(idx, a, opts)
	if resolvedURL == "" && a.URLEnv != "" {
		return "", fmt.Errorf("photo-carousel: environment variable %q specified in \"url_env\" is not set or empty", a.URLEnv)
	}

	if resolvedURL == "" {
		return "", fmt.Errorf("photo-carousel: resolved album URL is empty")
	}

	parsedU, err := url.Parse(resolvedURL)
	if err != nil || (parsedU.Scheme != "http" && parsedU.Scheme != "https") || parsedU.Host == "" {
		return "", fmt.Errorf("photo-carousel: invalid album URL %q", redactURL(resolvedURL))
	}

	return resolvedURL, nil
}

func resolveAlbumSources(parsed PhotosConfig, opts InitOptions) ([]AlbumSource, error) {
	if len(parsed.Albums) == 0 {
		return nil, fmt.Errorf("photo-carousel: no albums configured")
	}

	resolved := make([]AlbumSource, len(parsed.Albums))
	for idx, a := range parsed.Albums {
		resolvedURL, err := resolveSingleAlbumURL(idx, a, opts)
		if err != nil {
			return nil, err
		}
		a.ResolvedURL = resolvedURL
		resolved[idx] = a
	}
	return resolved, nil
}

// parsePhotosConfig parses and validates instance configuration.
func parsePhotosConfig(cfg map[string]any, _ InitOptions) (PhotosConfig, error) {
	if cfg == nil {
		return PhotosConfig{}, fmt.Errorf("photo-carousel: configuration is required")
	}

	var res PhotosConfig
	if cd, ok := cfg["cache_dir"].(string); ok && strings.TrimSpace(cd) != "" {
		res.CacheDir = strings.TrimSpace(cd)
	}

	rawAlbums, ok := cfg["albums"]
	if !ok || rawAlbums == nil {
		if cfg["source"] != nil || cfg["album_url"] != nil || cfg["share_url"] != nil || cfg["album_url_env"] != nil || cfg["share_url_env"] != nil {
			return PhotosConfig{}, fmt.Errorf("photo-carousel: legacy single-album configuration ('source', 'album_url', 'share_url') is no longer supported; please migrate to 'albums: [{name, source, url/url_env}]'")
		}
		return PhotosConfig{}, fmt.Errorf("photo-carousel: 'albums' is required")
	}

	albums, err := parseAlbumsList(rawAlbums)
	if err != nil {
		return PhotosConfig{}, err
	}
	res.Albums = albums
	if len(albums) > 0 {
		res.Source = albums[0].Source
		res.AlbumURL = albums[0].URL
		res.AlbumURLEnv = albums[0].URLEnv
	}
	return res, nil
}

func mapString(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func parseAlbumsList(raw any) ([]AlbumSource, error) {
	slice, ok := raw.([]any)
	if !ok || len(slice) == 0 {
		return nil, fmt.Errorf("photo-carousel: 'albums' must contain at least one album")
	}

	albums := make([]AlbumSource, 0, len(slice))
	for idx, item := range slice {
		itemMap, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("photo-carousel: album at index %d must be an object", idx)
		}
		name := mapString(itemMap, "name")
		if name == "" {
			return nil, fmt.Errorf("photo-carousel: album at index %d requires non-empty 'name'", idx)
		}

		source := mapString(itemMap, "source")
		urlStr := mapString(itemMap, "url")
		urlEnvStr := mapString(itemMap, "url_env")

		if source == "" {
			if urlStr == "" && urlEnvStr == "" {
				return nil, fmt.Errorf("photo-carousel: album %q requires 'source'", name)
			}
			source = PhotoSourceShareURL
		} else if source != PhotoSourceShareURL && source != PhotoSourceEmberBridge {
			return nil, fmt.Errorf("photo-carousel: 'source' must be either %q or %q", PhotoSourceShareURL, PhotoSourceEmberBridge)
		}

		if urlStr != "" && urlEnvStr != "" {
			return nil, fmt.Errorf("photo-carousel: 'url' and 'url_env' are mutually exclusive in album %q", name)
		}
		if urlStr == "" && urlEnvStr == "" {
			return nil, fmt.Errorf("photo-carousel: album %q requires either 'url' or 'url_env'", name)
		}

		albums = append(albums, AlbumSource{
			Name:   name,
			Source: source,
			URL:    urlStr,
			URLEnv: urlEnvStr,
		})
	}
	return albums, nil
}

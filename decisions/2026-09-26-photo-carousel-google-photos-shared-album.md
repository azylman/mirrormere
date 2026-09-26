# Architecture Decision Record: Google Photos Shared Album Carousel Provider & Dynamic Presentation Sizing

- **Date:** 2026-09-26
- **Status:** Accepted
- **Context:** Implementing Issue #233 on `azylman/mirrormere` (SPEC-013 Phase 3, Chunk 3.3D), fulfilling SPEC-007 §3, SPEC-003 §4, and SPEC-005.

---

## 1. Problem Statement
1. **Google Photos API Deprecation**: Google deprecated and severely restricted third-party access to the official Google Photos Library API (`photoslibrary.readonly`) in 2025. Ambient displays require a zero-credential, unlisted link ingestion strategy.
2. **HTML Scraper Fragility & `AF_initDataCallback` Structure**: Unlisted share links (`https://photos.app.goo.gl/...`) follow redirects to `https://photos.google.com/share/...`, embedding page state in JavaScript hydration callbacks (`AF_initDataCallback`). The outer payload contains unquoted JavaScript object keys, causing direct `json.Unmarshal` calls to fail.
3. **Hardware Coupling vs Dynamic Presentation Sizing**: Hardcoding display resolutions or maintaining target device tables in backend Go code violates the Decoupled Configuration and Zero Runtime Switching invariants. Image sizing must be determined at presentation time based on the rendered geometry of the widget on the grid canvas.
4. **HTML5 `innerHTML` Script Execution Trap**: Mirrormere's kiosk display engine (`web/static/js/carousel.js`) swaps widget markup using `temp.innerHTML = ...; existing.replaceWith(newNode)`. Per the HTML5 specification, `<script>` tags inserted via `innerHTML` are intentionally never executed by browsers, preventing naive inline scripts in `views/widget.html` from running.
5. **Headless E-Ink Cold-Boot Rendering**: Mike's E-Ink sidecar captures 1-bit monochrome screenshots via headless Chromium. If the widget starts with a blank container waiting for client JavaScript animation ticks, the headless capture sidecar renders an empty box.
6. **Slowloris & Security Defense**: Album HTML payloads from remote endpoints must be strictly bounded (5MB ceiling) to protect against slowloris attacks, unbounded memory consumption, or redirect loops to authentication walls.

---

## 2. Decision & Architecture

### A. Google Photos Ingestion Driver (`internal/provider/photos.go`)
- **Registration**: Registered `"photo-carousel"` and alias `"photos"` in `DefaultRegistry`.
- **3-Tier Secret Resolution Pipeline**:
  - Tier 1: `opts.Secrets["share_url"]` or `share_url` in instance config.
  - Tier 2: `opts.GetSecret(c.ShareURLEnv)`.
  - Tier 3: `os.Getenv(c.ShareURLEnv)`.
  - Enforces mutual exclusivity between `share_url` and `share_url_env`, and fails fast on startup if `share_url_env` resolves to an empty or unset variable.
- **Redirect Following & Security Traversal**:
  - Modern browser User-Agent (`Mozilla/5.0 ... Chrome/128.0.0.0 Safari/537.36`) and Accept headers.
  - Detects redirection to authentication walls (`accounts.google.com`), cookie consent walls (`consent.google.com`), or CAPTCHA barriers (`sorry.google.com`), returning explicit errors (`ErrAlbumPrivate`, `ErrAlbumRateLimit`).
- **Slowloris Defense**: Wraps response streams in `io.LimitReader(resp.Body, maxPhotoAlbumBytes+1)` (5MB ceiling), returning an explicit error if the server response exceeds 5MB.
- **Robust Multi-Strategy Extraction**:
  - *Primary Strategy (`extractFromAFInitData`)*: Identifies `AF_initDataCallback` invocations and scans forward to `data:`, extracting the inner balanced JSON array `[ ... ]` by tracking bracket depth and string escaping. Parses media items `[id, [url, width, height], timestamp]`.
  - *Fallback Strategy (`extractFromFallbackRegex`)*: Regex-scans for user media URLs matching `/pw/` path (`https://lh*.googleusercontent.com/pw/...`), deduplicating URLs via a set map.
  - *Clean Base URL Normalization*: Strips trailing sizing parameters (`=...`) so clients can cleanly append dynamic sizing query parameters.
  - *Metadata & Aspect Ratio*: Extracts album name from `<meta property="og:title">` or `<title>`, stripping Google Photos branding. Computes `aspect_ratio = width / height` with division-by-zero guards.
  - *Shuffle & Preload*: Shuffles photos when configured (`shuffle: true`), clamps to `preload_count` (1..500), and records `TotalPhotos`.
- **Stale-While-Revalidate (SWR) Caching**: Caches snapshot in memory under `sync.RWMutex`. Transient upstream scrape failures return the last known good snapshot with structured warning logs.

### B. Dynamic Edge Sizing & Kiosk Lifecycle (`widgets/photo-carousel/`, `web/static/js/carousel.js`)
- **SSR First-Photo Parity**: Server-Side Rendering (`views/widget.html`) immediately populates `.photo-current` with the first photo (`{{ $first.URL }}=w960-h640-c`). Headless Chromium on E-Ink renders the photo immediately on cold boot without requiring client animation ticks.
- **HTML5 `innerHTML` Script Execution Workaround**:
  - Implements an invisible bootstrap dummy element:
    ```html
    <img src="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg'/%3E" style="display:none;" onload="window.MirrormerePhotoCarousel && window.MirrormerePhotoCarousel.mount(this.closest('.widget-photo-carousel'))" />
    ```
  - Inline `onload` handlers execute immediately upon `innerHTML` insertion, triggering `mount()` cleanly across both initial render and dynamic `widget.update` swaps.
- **Client DOM Measurement & Preload**:
  - `MirrormerePhotoCarousel` measures container bounding rect (`rect = container.getBoundingClientRect()`) scaled by DPR (`dpr = window.devicePixelRatio || 1`).
  - Appends `=w{targetWidth}-h{targetHeight}-c` dynamically on CDN fetch.
  - Preloads the next image in an off-screen `new Image()` object before triggering smooth 0.8s CSS opacity crossfades.
- **Clean Photo Canvas Invariant**: Edge-to-edge rendering with `object-fit: cover; width: 100%; height: 100%;`. Zero floating text overlays, clocks, or status badges.
- **Selective Dithering Support**: Markup and images carry the `.dither` class, instructing `mirrormere-eink-renderer` to apply Floyd-Steinberg dithering while maintaining crisp boundaries.

---

## 3. Verification & Compliance
- **Hermetic Unit Testing**:
  - `internal/provider/photos_test.go`: Tests mock server with `AF_initDataCallback` array, function wrapper, `/pw/` fallback regex, redirect to `accounts.google.com` (private album), 404, 429, 500, Slowloris 5MB limit, SWR caching, 3-tier secret resolution, shuffle/preload clamping, empty albums, and registry lifecycle.
  - `internal/provider/photos_internal_test.go`: Tests unexported helpers `toFloat`, `parsePhotosConfig`, `parseMediaItem`, `findMediaItems`, `extractBalancedArray`, and `cleanGooglePhotoURL`.
  - `web/test/photo_carousel.test.js`: Node test runner verifying client-side dynamic DPR sizing, fallback dimensions, and mount/unmount lifecycle.
- **Coverage Compliance**: Maintained 95.7% statement coverage in `internal/provider`, strictly exceeding the >= 95.0% floor across all packages.
- **Verification Pipeline**: Verified clean `./scripts/verify.sh --staged` compliance covering codegen, go vet, golangci-lint, deadcode, Go unit tests, and Node UI integration tests.

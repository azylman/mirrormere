# ADR: Relative Path for Widget Template .Assets Context Variable

## Context & Problem Statement
In PR #293 (commit 69ad466), frontend endpoints, scripts, and stylesheets were made subpath-agnostic so Mirrormere could be served behind reverse-proxy subpaths (e.g. `/kiosk/`). As part of this change, `web/templates/display.html` established `<base href="./">`, and client-side endpoints in `sse.js`, `display.js`, `carousel.js`, and `video_hud.js` were converted to relative paths.

In Issue #295, Mike Carmody identified that `internal/render/engine.go` still injected `.Assets` as a root-absolute path:
```go
Assets: fmt.Sprintf("/widget-types/%s/assets", w.Type),
```

SPEC-003 (§HTTP Asset Route, line 80, 270, and 459) also defined `.Assets` with a leading slash: `/widget-types/<widget-type>/assets`.

Because `<base href="./">` only applies to relative URLs, any widget template referencing assets via standard syntax (`<img src="{{ .Assets }}/icon.svg">`) would resolve to `https://<host>/widget-types/...` instead of `https://<host>/kiosk/widget-types/...`, resulting in a 404 Not Found error under subpath deployments.

## Decision
1. **Relative Path Injection in `internal/render/engine.go`**:
   - Modified `buildContext` in `internal/render/engine.go` to omit the leading slash:
     ```go
     Assets: fmt.Sprintf("widget-types/%s/assets", w.Type),
     ```
   - This ensures templates referencing `{{ .Assets }}/<filename>` produce relative paths (`widget-types/<type>/assets/<filename>`) that resolve against `<base href="./">` correctly in both root (`/`) and subpath (`/kiosk/`) deployments.

2. **Specification Alignment in `specs/003-widget-contract.md`**:
   - Updated SPEC-003 §HTTP Asset Route (line 80) and Context definitions (lines 270 and 459) to specify `widget-types/<widget-type>/assets` without a leading slash.

3. **Hermetic Test Updates in `internal/render/engine_test.go`**:
   - Updated `TestEngine_RenderWidget_ContextSanitizationAndDefaults` to assert `widget-types/weather/assets` (relative path).

## Consequences & Alternatives Considered
- **Configurable `base_url` or `subpath` backend parameter**:
  Rejected. Mirrormere strictly avoids unnecessary runtime configuration multiplexing. Using `<base href="./">` in HTML coupled with clean relative URLs allows the client browser and reverse proxy to determine the base path without requiring backend configuration flags.
- **Positive**:
  Widget packages shipping static assets (icons, images, SVGs) will seamlessly load under both root domain (`http://localhost:8080/`) and subpath reverse-proxy (`https://lan.home/kiosk/`) deployments without template changes.

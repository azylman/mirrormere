# Architecture Decision Record: Headless E-Ink Snapshot Sidecar and Selective Dithering Pipeline

- **Date:** 2026-09-26
- **Status:** Accepted
- **Context:** Implementing Issue #245 on `azylman/mirrormere` (SPEC-013 Phase 5, Chunk 5.1), fulfilling SPEC-003 §4-§5, SPEC-009 §3, and SPEC-002 §3.

---

## 1. Problem Statement
Prior to Chunk 5.1:
1. **Fuzzy Text & Gray Speckle Trap in Standard Dithering**: Microcontroller-driven electronic paper displays (such as the Waveshare 7.5" 800×480 monochrome e-paper display on ESP32-S3) require 1-bit black-and-white pixels. Applying universal Floyd-Steinberg error diffusion across the entire viewport scatters stray black and white error pixels into clean typography, numbers, widget borders, and solid backgrounds. This destroys readability on high-contrast ambient dashboards.
2. **Missing Grayscale Gradients on Rich Media**: Conversely, applying naive global thresholding across the canvas turns continuous photos, weather maps, and art into harsh high-contrast blobs with severe posterization.
3. **Bandwidth and MCU Power Waste**: An ESP32 or Raspberry Pi Pico e-paper node polling for periodic screen updates must not repeatedly re-download and re-render unchanged 48KB monochrome frames over Wi-Fi. HTTP cache validation using strong cryptographic ETags (`304 Not Modified`) is essential to preserve battery life and reduce microcontroller compute cycles.
4. **Rendering Stampedes**: Multiple e-ink clients or rapid HTTP polling could trigger redundant, expensive headless browser screenshot cycles simultaneously, starving the host CPU.

---

## 2. Decision & Architecture

Mirrormere resolves this by creating a dedicated, zero-dependency Node.js snapshot sidecar service at `sidecars/eink-renderer/`:

### Two-Pass Selective Dithering Pipeline (`dither.js`)
- **Luminance Extraction**: Calculates perceived luminance using the ITU-R BT.601 standard formula: `0.299 * R + 0.587 * G + 0.114 * B`. Alpha channels are pre-blended against a solid black background: `luminance * (A / 255)`.
- **Pass 1 (Strict 1-Bit Thresholding)**: Pixels outside `.dither` elements are strictly thresholded at luminance 128 (values < 128 become 0/black, values >= 128 become 255/white). This guarantees razor-sharp typography, crisp lines, and clean whitespace with zero stray error speckles.
- **Pass 2 (Selective Error Diffusion)**:
  - Scans DOM for elements annotated with the CSS class `.dither` and retrieves their integer bounding client rects.
  - Bounding rects are safely clamped to `[0, width)` and `[0, height)` and rasterized into a fast 2D boolean mask.
  - Floyd-Steinberg error diffusion (7/16 right, 3/16 bottom-left, 5/16 bottom, 1/16 bottom-right) is executed exclusively inside mask regions.
  - Boundary Error Confinement: Error is diffused to adjacent neighbor pixels if and only if those neighbors also reside inside the `.dither` mask. Any error targeted at pixels outside the mask boundary is strictly discarded, completely preventing error bleed from images into adjacent text, widgets, or borders.
- **Pure-JS 1-Bit Monochrome PNG Encoder (`encode1BitPNG`)**:
  - Encodes the 800×480 1-bit buffer into a standard PNG with Color Type 0 (Grayscale) and Bit Depth 1.
  - Each scanline consists of 1 filter byte (`0x00` - None) followed by 100 packed bytes (8 pixels per byte, MSB first: 1 for white, 0 for black).
  - Emits valid IHDR, IDAT (deflated via Node.js built-in `zlib`), and IEND chunks with standard IEEE 802.3 CRC-32 checksums. Zero external npm dependencies required.
- **Pure-JS PNG Decoder (`decodePNG`)**:
  - Implements a self-contained PNG decoder supporting all standard PNG filter algorithms (0: None, 1: Sub, 2: Up, 3: Average, 4: Paeth).
  - Supports 1-bit grayscale, 8-bit grayscale, 8-bit RGB, and 8-bit RGBA inputs, enabling seamless decoding of headless Chromium CDP screenshots.
- **Strong SHA-256 ETag Computation (`computeETag`)**:
  - Generates a quoted SHA-256 hex digest (`"..."`) over the exact encoded 1-bit PNG byte buffer.

### Headless Chromium CDP Capture Controller (`capture.js`)
- **Automated Viewport Management**: Navigates headless Chromium to `DISPLAY_URL` at fixed 800×480 resolution with `deviceScaleFactor: 1`.
- **DOM Evaluation**: Executes `Runtime.evaluate` over Chrome DevTools Protocol (CDP) WebSocket to retrieve `.dither` bounding boxes directly from the rendered DOM.
- **Capture & Layout Stabilization**: Waits 300ms for web components and fonts to hydrate, triggers `Page.captureScreenshot`, and streams PNG data into the dithering pipeline.
- **Single-Flight Coalescing & Debouncing**:
  - Coalesces concurrent in-flight requests into a single shared execution promise to eliminate rendering stampedes.
  - Enforces a configurable debounce window (`debounceWindowMs: 2000`) returning the cached frame unless `?force=true` is requested.
  - Injected `screenshotProvider` interface enables hermetic unit testing without requiring a live browser subprocess.

### Snapshot HTTP Server (`server.js`)
- Serves HTTP on internal port 8081 with Slowloris timeouts (`headersTimeout: 5000`, `requestTimeout: 10000`).
- `GET /healthz`: Returns 200 `{"status":"ok"}` for container health monitoring.
- `GET /eink.png`:
  - Validates `If-None-Match` against the computed strong ETag. If matched, returns `304 Not Modified` with zero response body.
  - Returns `200 OK` with `Content-Type: image/png`, `ETag: "..."`, and `Cache-Control: no-cache, must-revalidate`.
  - Resilience Fallback: Returns cached frame with header `X-Mirrormere-Degraded: true` if upstream Core drops, or `503 Service Unavailable` on cold boot failure so the e-ink display node preserves its last visible screen.

### Deployment & Compose Configuration (`deploy/compose.yml`)
- Added `eink-renderer` service under `profiles: ["eink"]` ensuring it is isolated to e-ink profiles and not run on video/touch kiosks.
- Hardened Alpine container (`sidecars/eink-renderer/Dockerfile`) with Chromium, Noto fonts, and non-root user `10001:10001`.
- Healthchecked via `wget --spider http://127.0.0.1:8081/healthz`.

---

## 3. Verification & Compliance
- **Hermetic Testing**:
  - `sidecars/eink-renderer/test/dither.test.js`: Verified ITU-R BT.601 luminance and alpha blending, Pass 1 strict thresholding with zero noise outside `.dither` regions, Pass 2 Floyd-Steinberg error diffusion bounded within `.dither` regions without boundary leakage, overlapping and out-of-bounds rect handling, standard 800×480 1-bit monochrome PNG encoding (Color Type 0, Bit Depth 1), pure-JS `decodePNG` unfiltering and bit-unpacking, `CaptureService` concurrent single-flight coalescing, HTTP 200/304 ETag caching, and 503 cold boot error fallback. All 9 test suites pass cleanly.
  - `scripts/verify.sh`: Integrated sidecar test suite into `run_node_tests()`.
- **Zero Invariant Violations**:
  - Zero markdown tables in ADR or manifests.
  - Zero plaintext tokens.
  - Zero external npm dependencies.

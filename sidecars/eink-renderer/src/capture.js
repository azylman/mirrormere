/**
 * capture.js - Headless Chromium Capture Controller & CDP Integration
 * Reference: SPEC-003 §4-§5, SPEC-009 §3
 */

const http = require('http');
const { decodePNG, applySelectiveDithering, encode1BitPNG, computeETag } = require('./dither');

/**
 * Predicate evaluated in-page (via CDP Runtime.evaluate) to decide whether the
 * carousel has finished hydrating a freshly-navigated/rotated display.
 *
 * Settled means:
 *  - the document has finished loading, AND
 *  - the carousel has hydrated the current screen (handleScreenRotate in
 *    carousel.js sets data-screen-hydrated="true" and data-widget-count=<N> on
 *    the canvas), AND all N widgets are mounted (data-widget-id per node).
 *    N may be 0 for a header-only screen, which is settled once hydrated, AND
 *  - the canvas is not mid hot-swap transition (carousel.js toggles the
 *    `transitioning` class on the canvas for ~150ms around the DOM swap).
 *
 * Before SSE delivers `screen.rotate` and the widget fetches resolve, the page
 * only shows the header + placeholder text ("--:-- ------ --°"), so the
 * hydration marker is what actually distinguishes "populated" from
 * "placeholder" rather than just readyState, which is already 'complete' by
 * then.
 */
const SETTLE_PREDICATE_EXPRESSION = `
  (function () {
    if (document.readyState !== 'complete') return false;
    var canvas = document.querySelector('#grid-canvas');
    if (!canvas) return false;
    // carousel.js stamps data-screen-hydrated / data-widget-count on the canvas
    // after each screen.rotate hot-swap. The count is the number of widgets the
    // screen is configured for, which is 0 for an intentional header-only view.
    var ds = canvas.dataset || {};
    if (ds.screenHydrated !== 'true') return false;
    var expected = Number(ds.widgetCount);
    if (!isFinite(expected) || expected < 0) return false;
    if (document.querySelectorAll('#grid-canvas [data-widget-id]').length < expected) return false;
    if (document.querySelector('#grid-canvas.transitioning')) return false;
    return true;
  })()
`;

const DEFAULT_SETTLE_TIMEOUT_MS = 5000;
const SETTLE_MIN_WAIT_MS = 300; // floor: preserves the previous fixed-delay behavior
const SETTLE_POLL_INTERVAL_MS = 100;
const DEFAULT_LOAD_EVENT_TIMEOUT_MS = 10000; // cap on waiting for Page.loadEventFired after navigate
const SETTLE_PAINT_DELAY_MS = 150; // extra settle time for paint after the predicate passes

const defaultSleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

/**
 * Polls `evaluateFn` (an async function returning a boolean "is it settled?")
 * until it reports settled, a configurable max wait elapses, or the caller's
 * min-wait floor + poll loop otherwise concludes. Never throws on timeout —
 * callers should capture regardless and just log a warning via `onTimeout`.
 *
 * Factored out from captureViaCDP so it is unit-testable without a real
 * Chromium/CDP connection: tests pass a fake `evaluateFn`.
 *
 * @param {() => Promise<boolean>} evaluateFn
 * @param {Object} [options]
 * @param {number} [options.minWaitMs] Minimum time to wait before the first check (floor).
 * @param {number} [options.pollIntervalMs] Delay between polls.
 * @param {number} [options.maxWaitMs] Overall cap (measured from the start of the wait) after which polling stops.
 * @param {number} [options.paintDelayMs] Extra delay applied after settling (or timing out) for paint to finish.
 * @param {(elapsedMs: number) => void} [options.onTimeout] Called if maxWaitMs is hit without settling.
 * @param {(ms: number) => Promise<void>} [options.sleep] Injectable sleep, for fast tests.
 * @returns {Promise<boolean>} Whether the page reported settled before the timeout.
 */
async function pollForSettle(evaluateFn, options = {}) {
  const {
    minWaitMs = SETTLE_MIN_WAIT_MS,
    pollIntervalMs = SETTLE_POLL_INTERVAL_MS,
    maxWaitMs = DEFAULT_SETTLE_TIMEOUT_MS,
    paintDelayMs = SETTLE_PAINT_DELAY_MS,
    onTimeout = null,
    sleep = defaultSleep,
  } = options;

  const start = Date.now();

  // A single check that swallows evaluateFn errors as "not settled yet"
  // rather than propagating them: a CDP Runtime.evaluate can transiently
  // fail with things like "Execution context was destroyed" right after a
  // navigation or a hot-swap replaces the DOM mid-poll. Never let that abort
  // the capture - just keep polling until the cap.
  const checkSettled = async () => {
    try {
      return !!(await evaluateFn());
    } catch {
      return false;
    }
  };

  // Preserve the previous fixed-delay behavior as a floor: never check before this.
  await sleep(minWaitMs);

  let settled = await checkSettled();
  while (!settled && (Date.now() - start) < maxWaitMs) {
    await sleep(pollIntervalMs);
    settled = await checkSettled();
  }

  if (!settled && typeof onTimeout === 'function') {
    onTimeout(Date.now() - start);
  }

  // Never hang or throw just because we timed out — capture whatever is there.
  await sleep(paintDelayMs);

  return settled;
}

/** Throws a descriptive error for a non-2xx CDP HTTP response. */
function assertOk(res, body, endpoint) {
  const status = res.statusCode;
  if (typeof status !== 'number' || status < 200 || status >= 300) {
    const snippet = String(body).slice(0, 200);
    throw new Error(`CDP ${endpoint} returned HTTP ${status}: ${snippet}`);
  }
}

class CaptureService {
  constructor(options = {}) {
    this.coreURL = options.coreURL || process.env.CORE_URL || 'http://mirrormere-core:8080';
    this.displayURL = options.displayURL || process.env.DISPLAY_URL || `${this.coreURL}/display`;
    this.cdpURL = options.cdpURL || process.env.CDP_URL || 'http://localhost:9222';
    this.screenshotProvider = options.screenshotProvider || null;
    this.width = options.width || 800;
    this.height = options.height || 480;
    this.settleTimeoutMs = options.settleTimeoutMs
      || Number(process.env.SETTLE_TIMEOUT_MS)
      || DEFAULT_SETTLE_TIMEOUT_MS;

    this.loadEventTimeoutMs = options.loadEventTimeoutMs || DEFAULT_LOAD_EVENT_TIMEOUT_MS;
    this.sleep = options.sleep || defaultSleep;

    // In-flight coalescing promise to prevent rendering stampedes
    this.inFlightPromise = null;
    this.cachedFrame = null;
    this.cachedETag = null;
    this.lastRenderTime = 0;
    this.debounceWindowMs = options.debounceWindowMs || 2000;
  }

  /**
   * Sets a custom screenshot provider for hermetic testing.
   * Provider must return { rgbaBuffer, ditherRects } or { pngBuffer, ditherRects }.
   *
   * @param {Function} provider Async or sync function returning { rgbaBuffer|pngBuffer, ditherRects }
   */
  setScreenshotProvider(provider) {
    this.screenshotProvider = provider;
  }

  /**
   * Performs a capture pass and returns the 1-bit monochrome PNG buffer with its strong ETag.
   * Coalesces concurrent calls so multiple HTTP requests share the identical capture cycle.
   *
   * @param {boolean} [force=false] Force bypass of debounce window
   * @returns {Promise<{png: Buffer, etag: string, fromCache: boolean}>}
   */
  async getSnapshot(force = false) {
    const now = Date.now();

    // Serve cached frame if within debounce window and not forced
    if (!force && this.cachedFrame && (now - this.lastRenderTime < this.debounceWindowMs)) {
      return {
        png: this.cachedFrame,
        etag: this.cachedETag,
        fromCache: true,
      };
    }

    // Coalesce concurrent in-flight capture cycles
    if (this.inFlightPromise) {
      return this.inFlightPromise;
    }

    this.inFlightPromise = (async () => {
      try {
        let rgbaBuffer;
        let ditherRects = [];

        if (this.screenshotProvider) {
          const res = await this.screenshotProvider();
          ditherRects = res.ditherRects || [];
          if (res.rgbaBuffer) {
            rgbaBuffer = res.rgbaBuffer;
          } else if (res.pngBuffer) {
            const decoded = decodePNG(res.pngBuffer);
            rgbaBuffer = decoded.data;
          } else {
            throw new Error('Screenshot provider returned neither rgbaBuffer nor pngBuffer');
          }
        } else {
          // Live CDP Capture from Headless Chromium
          const cdpRes = await this.captureViaCDP();
          const decoded = decodePNG(cdpRes.pngBuffer);
          rgbaBuffer = decoded.data;
          ditherRects = cdpRes.ditherRects || [];
        }

        // Apply two-pass selective dithering pipeline
        const monoPixels = applySelectiveDithering(rgbaBuffer, this.width, this.height, ditherRects);

        // Encode to standard 800×480 1-bit monochrome PNG
        const png = encode1BitPNG(monoPixels, this.width, this.height);
        const etag = computeETag(png);

        this.cachedFrame = png;
        this.cachedETag = etag;
        this.lastRenderTime = Date.now();

        return {
          png,
          etag,
          fromCache: false,
        };
      } finally {
        this.inFlightPromise = null;
      }
    })();

    return this.inFlightPromise;
  }

  /**
   * Captures the active viewport from Chromium via Chrome DevTools Protocol (CDP).
   *
   * @returns {Promise<{pngBuffer: Buffer, ditherRects: Array}>}
   */
  async captureViaCDP() {
    // 1. Discover or create target page via CDP HTTP API
    const target = await this.getOrCreateTarget();
    const wsUrl = target.webSocketDebuggerUrl;

    if (!wsUrl) {
      throw new Error(`No webSocketDebuggerUrl returned for CDP target: ${JSON.stringify(target)}`);
    }

    return new Promise((resolve, reject) => {
      const WebSocket = globalThis.WebSocket;
      if (!WebSocket) {
        return reject(new Error('WebSocket is not available in global scope'));
      }

      const ws = new WebSocket(wsUrl);
      let id = 1;
      const callbacks = new Map();

      const send = (method, params = {}) => {
        return new Promise((res, rej) => {
          const msgId = id++;
          callbacks.set(msgId, { resolve: res, reject: rej });
          ws.send(JSON.stringify({ id: msgId, method, params }));
        });
      };

      ws.onmessage = (event) => {
        const msg = JSON.parse(event.data);
        if (msg.method && eventListeners.has(msg.method)) {
          for (const fn of Array.from(eventListeners.get(msg.method))) fn(msg.params);
        }
        if (msg.id && callbacks.has(msg.id)) {
          const cb = callbacks.get(msg.id);
          callbacks.delete(msg.id);
          if (msg.error) {
            cb.reject(new Error(`CDP error ${msg.error.code}: ${msg.error.message}`));
          } else {
            cb.resolve(msg.result);
          }
        }
      };

      let closedNormally = false;
      const eventListeners = new Map(); // CDP event method -> Set<fn>
      const onEvent = (method, fn) => {
        if (!eventListeners.has(method)) eventListeners.set(method, new Set());
        eventListeners.get(method).add(fn);
        return () => eventListeners.get(method).delete(fn);
      };

      ws.onerror = (err) => {
        reject(err);
      };

      ws.onclose = () => {
        if (!closedNormally) {
          for (const [, cb] of callbacks) {
            cb.reject(new Error('CDP WebSocket closed unexpectedly'));
          }
          callbacks.clear();
          reject(new Error('CDP WebSocket closed unexpectedly'));
        }
      };

      ws.onopen = async () => {
        try {
          await send('Page.enable');
          await send('Emulation.setDeviceMetricsOverride', {
            width: this.width,
            height: this.height,
            deviceScaleFactor: 1,
            mobile: false,
          });

          // Navigate to display URL. Page.navigate resolves as soon as the
          // navigation is scheduled, NOT when the new document commits, so a
          // reused target would still show the previous (already hydrated)
          // DOM and the settle predicate would pass instantly on stale pixels.
          // Register the load listener BEFORE navigating so the event cannot
          // be missed, then gate the poll on it.
          let loadFired = false;
          let resolveLoad;
          const loadPromise = new Promise((r) => { resolveLoad = r; });
          const offLoad = onEvent('Page.loadEventFired', () => { loadFired = true; resolveLoad(); });
          let loadTimer;
          try {
            await send('Page.navigate', { url: this.displayURL });
            await Promise.race([
              loadPromise,
              new Promise((r) => { loadTimer = setTimeout(r, this.loadEventTimeoutMs); }),
            ]);
          } finally {
            clearTimeout(loadTimer);
            offLoad();
          }
          if (!loadFired) {
            // eslint-disable-next-line no-console
            console.warn(
              `[eink-renderer] captureViaCDP: Page.loadEventFired not seen within `
              + `${this.loadEventTimeoutMs}ms - polling for settle anyway`
            );
          }

          // Poll until the carousel has actually hydrated widgets onto the
          // canvas (see SETTLE_PREDICATE_EXPRESSION) instead of trusting a
          // fixed delay: SSE delivery + per-widget fetches can take well
          // over 300ms, and a fixed sleep captured the pre-hydration
          // placeholder ("--:-- ------ --°") instead of real widget content.
          await pollForSettle(
            async () => {
              const evalRes = await send('Runtime.evaluate', {
                expression: SETTLE_PREDICATE_EXPRESSION,
                returnByValue: true,
              });
              return !!(evalRes && evalRes.result && evalRes.result.value);
            },
            {
              maxWaitMs: this.settleTimeoutMs,
              sleep: this.sleep,
              onTimeout: (elapsedMs) => {
                // eslint-disable-next-line no-console
                console.warn(
                  `[eink-renderer] captureViaCDP: display did not settle within ${elapsedMs}ms `
                  + `(cap ${this.settleTimeoutMs}ms) - capturing anyway`
                );
              },
            }
          );

          // Query .dither bounding client rects
          const evalRes = await send('Runtime.evaluate', {
            expression: `
              Array.from(document.querySelectorAll('.dither')).map(el => {
                const r = el.getBoundingClientRect();
                return {
                  x: Math.round(r.left),
                  y: Math.round(r.top),
                  width: Math.round(r.width),
                  height: Math.round(r.height)
                };
              })
            `,
            returnByValue: true,
          });

          const ditherRects = (evalRes && evalRes.result && evalRes.result.value) || [];

          // Capture 800×480 viewport screenshot as PNG
          const screenshotRes = await send('Page.captureScreenshot', {
            format: 'png',
            clip: {
              x: 0,
              y: 0,
              width: this.width,
              height: this.height,
              scale: 1,
            },
          });

          const pngBuffer = Buffer.from(screenshotRes.data, 'base64');
          closedNormally = true;
          ws.close();
          resolve({ pngBuffer, ditherRects });
        } catch (err) {
          closedNormally = true;
          ws.close();
          reject(err);
        }
      };
    });
  }

  /**
   * Queries or creates a page target via Chromium's HTTP JSON endpoints.
   *
   * @returns {Promise<Object>}
   */
  async getOrCreateTarget() {
    return new Promise((resolve, reject) => {
      const req = http.get(`${this.cdpURL}/json/list`, (res) => {
        let body = '';
        res.on('data', (chunk) => { body += chunk; });
        res.on('end', () => {
          try {
            assertOk(res, body, '/json/list');
            const list = JSON.parse(body);
            const page = list.find((t) => t.type === 'page');
            if (page) {
              return resolve(page);
            }
            // Create new page if none exists. Current Chromium rejects a GET
            // here ("Using unsafe HTTP verb GET to invoke /json/new. This
            // action supports only PUT verb.") - must be a PUT.
            const newReq = http.request(
              `${this.cdpURL}/json/new?${encodeURIComponent(this.displayURL)}`,
              { method: 'PUT' },
              (newRes) => {
                let newBody = '';
                newRes.on('data', (chunk) => { newBody += chunk; });
                newRes.on('end', () => {
                  try {
                    assertOk(newRes, newBody, '/json/new');
                    resolve(JSON.parse(newBody));
                  } catch (e) {
                    reject(e);
                  }
                });
              }
            );
            newReq.on('error', reject);
            newReq.end();
          } catch (e) {
            reject(e);
          }
        });
      });
      req.on('error', reject);
    });
  }
}

module.exports = {
  CaptureService,
  pollForSettle,
  SETTLE_PREDICATE_EXPRESSION,
  DEFAULT_SETTLE_TIMEOUT_MS,
  SETTLE_MIN_WAIT_MS,
  SETTLE_POLL_INTERVAL_MS,
  SETTLE_PAINT_DELAY_MS,
};

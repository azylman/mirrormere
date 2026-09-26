/**
 * capture.js - Headless Chromium Capture Controller & CDP Integration
 * Reference: SPEC-003 §4-§5, SPEC-009 §3
 */

const http = require('http');
const { decodePNG, applySelectiveDithering, encode1BitPNG, computeETag } = require('./dither');

class CaptureService {
  constructor(options = {}) {
    this.coreURL = options.coreURL || process.env.CORE_URL || 'http://mirrormere-core:8080';
    this.displayURL = options.displayURL || process.env.DISPLAY_URL || `${this.coreURL}/display`;
    this.cdpURL = options.cdpURL || process.env.CDP_URL || 'http://localhost:9222';
    this.screenshotProvider = options.screenshotProvider || null;
    this.width = options.width || 800;
    this.height = options.height || 480;

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

      ws.onerror = (err) => {
        reject(err);
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

          // Navigate to display URL
          await send('Page.navigate', { url: this.displayURL });

          // Wait 300ms for layout stabilization
          await new Promise((r) => setTimeout(r, 300));

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
          ws.close();
          resolve({ pngBuffer, ditherRects });
        } catch (err) {
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
            const list = JSON.parse(body);
            const page = list.find((t) => t.type === 'page');
            if (page) {
              return resolve(page);
            }
            // Create new page if none exists
            http.get(`${this.cdpURL}/json/new?${encodeURIComponent(this.displayURL)}`, (newRes) => {
              let newBody = '';
              newRes.on('data', (chunk) => { newBody += chunk; });
              newRes.on('end', () => {
                try {
                  resolve(JSON.parse(newBody));
                } catch (e) {
                  reject(e);
                }
              });
            }).on('error', reject);
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
};

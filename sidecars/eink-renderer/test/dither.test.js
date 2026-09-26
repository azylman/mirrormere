/**
 * dither.test.js - Unit and Integration Tests for E-Ink Snapshot Sidecar
 * Reference: SPEC-003 §4-§5, SPEC-009 §3
 */

const { describe, it } = require('node:test');
const assert = require('node:assert');
const zlib = require('zlib');
const http = require('http');

const {
  luminance,
  applySelectiveDithering,
  encode1BitPNG,
  decodePNG,
  computeETag,
} = require('../src/dither');
const { CaptureService } = require('../src/capture');
const { createServer } = require('../src/server');

describe('E-Ink Dithering & 1-Bit PNG Engine (SPEC-003 §4-§5, SPEC-009 §3)', () => {
  it('calculates ITU-R BT.601 luminance and blends alpha over black', () => {
    assert.strictEqual(luminance(0, 0, 0), 0);
    assert.strictEqual(luminance(255, 255, 255), 255);
    assert.strictEqual(luminance(255, 0, 0), 76);   // 0.299 * 255 = 76.245
    assert.strictEqual(luminance(0, 255, 0), 150);  // 0.587 * 255 = 149.685
    assert.strictEqual(luminance(0, 0, 255), 29);   // 0.114 * 255 = 29.07

    // 50% white over black (alpha 128 / 255 ~ 0.5019)
    const semiWhite = luminance(255, 255, 255, 128);
    assert.ok(semiWhite >= 126 && semiWhite <= 129, `expected semiWhite ~128, got ${semiWhite}`);
  });

  it('Pass 1: applies strict 1-bit thresholding with zero noise outside .dither boxes', () => {
    const width = 10;
    const height = 10;
    const rgba = Buffer.alloc(width * height * 4);

    // Fill with values just above and below 128
    for (let y = 0; y < height; y++) {
      for (let x = 0; x < width; x++) {
        const idx = (y * width + x) * 4;
        const grayVal = x < 5 ? 120 : 135;
        rgba[idx] = grayVal;
        rgba[idx + 1] = grayVal;
        rgba[idx + 2] = grayVal;
        rgba[idx + 3] = 255;
      }
    }

    // No .dither rects
    const mono = applySelectiveDithering(rgba, width, height, []);

    for (let y = 0; y < height; y++) {
      for (let x = 0; x < width; x++) {
        const val = mono[y * width + x];
        if (x < 5) {
          assert.strictEqual(val, 0, `pixel (${x}, ${y}) should be strictly 0`);
        } else {
          assert.strictEqual(val, 255, `pixel (${x}, ${y}) should be strictly 255`);
        }
      }
    }
  });

  it('Pass 2: applies Floyd-Steinberg error diffusion strictly within .dither boxes without leaking error outside', () => {
    const width = 20;
    const height = 20;
    const rgba = Buffer.alloc(width * height * 4);

    // Uniform mid-gray 160 across entire canvas
    for (let i = 0; i < width * height; i++) {
      rgba[i * 4] = 160;
      rgba[i * 4 + 1] = 160;
      rgba[i * 4 + 2] = 160;
      rgba[i * 4 + 3] = 255;
    }

    // Dither rect strictly in the center [5..14] x [5..14]
    const ditherRect = { x: 5, y: 5, width: 10, height: 10 };
    const mono = applySelectiveDithering(rgba, width, height, [ditherRect]);

    let ditherBlackCount = 0;
    let ditherWhiteCount = 0;

    for (let y = 0; y < height; y++) {
      for (let x = 0; x < width; x++) {
        const val = mono[y * width + x];
        const inside = x >= 5 && x < 15 && y >= 5 && y < 15;

        if (inside) {
          if (val === 0) ditherBlackCount++;
          if (val === 255) ditherWhiteCount++;
        } else {
          // Outside .dither box: must be strictly thresholded without error diffusion.
          // Since input is 160 (>= 128), EVERY pixel outside MUST be 255!
          assert.strictEqual(
            val,
            255,
            `boundary leak detected: pixel (${x}, ${y}) outside .dither box received error and flipped to ${val}`
          );
        }
      }
    }

    // Inside .dither box: error diffusion produces a mixture of black and white dots
    assert.ok(ditherBlackCount > 0, 'expected dither black dots inside .dither box');
    assert.ok(ditherWhiteCount > 0, 'expected dither white dots inside .dither box');
  });

  it('handles overlapping, nested, and out-of-bounds .dither rects safely', () => {
    const width = 20;
    const height = 20;
    const rgba = Buffer.alloc(width * height * 4);
    rgba.fill(160);

    const rects = [
      { x: -5, y: -5, width: 10, height: 10 }, // Out of bounds negative
      { x: 5, y: 5, width: 8, height: 8 },     // Normal
      { x: 8, y: 8, width: 8, height: 8 },     // Overlapping
      { x: 15, y: 15, width: 10, height: 10 }, // Extending beyond bounds
      null,                                    // Malformed
      { width: 0, height: 0 },                 // Zero dimensions
    ];

    assert.doesNotThrow(() => {
      const mono = applySelectiveDithering(rgba, width, height, rects);
      assert.strictEqual(mono.length, width * height);
    });
  });

  it('encodes standard 800x480 1-bit monochrome PNG (Color Type 0, Bit Depth 1)', () => {
    const width = 800;
    const height = 480;
    const monoPixels = new Uint8Array(width * height);

    // Checkerboard pattern
    for (let y = 0; y < height; y++) {
      for (let x = 0; x < width; x++) {
        monoPixels[y * width + x] = (x + y) % 2 === 0 ? 255 : 0;
      }
    }

    const png = encode1BitPNG(monoPixels, width, height);

    // 1. Signature
    assert.strictEqual(png.subarray(0, 8).toString('hex'), '89504e470d0a1a0a');

    // 2. IHDR Chunk
    assert.strictEqual(png.toString('ascii', 12, 16), 'IHDR');
    assert.strictEqual(png.readUInt32BE(16), 800, 'width must be 800');
    assert.strictEqual(png.readUInt32BE(20), 480, 'height must be 480');
    assert.strictEqual(png.readUInt8(24), 1, 'bit depth must be 1');
    assert.strictEqual(png.readUInt8(25), 0, 'color type must be 0 (Grayscale)');
    assert.strictEqual(png.readUInt8(26), 0, 'compression must be 0');
    assert.strictEqual(png.readUInt8(27), 0, 'filter must be 0');
    assert.strictEqual(png.readUInt8(28), 0, 'interlace must be 0');

    // 3. Decompress IDAT scanlines
    let offset = 8;
    const idatDataList = [];
    while (offset < png.length) {
      const len = png.readUInt32BE(offset);
      const type = png.toString('ascii', offset + 4, offset + 8);
      const data = png.subarray(offset + 8, offset + 8 + len);
      if (type === 'IDAT') idatDataList.push(data);
      offset += 8 + len + 4;
    }

    const uncompressed = zlib.inflateSync(Buffer.concat(idatDataList));
    const expectedUncompressedBytes = height * (1 + Math.ceil(width / 8)); // 480 * 101 = 48,480
    assert.strictEqual(uncompressed.length, expectedUncompressedBytes);

    // Verify filter bytes are 0x00
    for (let y = 0; y < height; y++) {
      assert.strictEqual(uncompressed[y * 101], 0x00, `row ${y} filter byte must be 0x00`);
    }

    // 4. SHA-256 ETag format
    const etag = computeETag(png);
    assert.match(etag, /^"[a-f0-9]{64}"$/);
  });

  it('pure-JS decodePNG handles RGBA and unfilters rows correctly', () => {
    const width = 4;
    const height = 4;
    const mono = new Uint8Array(width * height);
    mono.fill(255);

    const png = encode1BitPNG(mono, width, height);
    const decoded = decodePNG(png);

    assert.strictEqual(decoded.width, width);
    assert.strictEqual(decoded.height, height);
    assert.strictEqual(decoded.data.length, width * height * 4);

    // Every pixel should be white (255, 255, 255, 255)
    for (let i = 0; i < width * height; i++) {
      assert.strictEqual(decoded.data[i * 4], 255);
      assert.strictEqual(decoded.data[i * 4 + 1], 255);
      assert.strictEqual(decoded.data[i * 4 + 2], 255);
      assert.strictEqual(decoded.data[i * 4 + 3], 255);
    }
  });

  it('CaptureService coalesces concurrent in-flight capture cycles', async () => {
    let callCount = 0;
    const service = new CaptureService({
      screenshotProvider: async () => {
        callCount++;
        await new Promise((r) => setTimeout(r, 20));
        const rgba = Buffer.alloc(800 * 480 * 4);
        rgba.fill(255);
        return { rgbaBuffer: rgba, ditherRects: [] };
      },
      debounceWindowMs: 0,
    });

    // Fire 5 simultaneous requests
    const promises = [
      service.getSnapshot(true),
      service.getSnapshot(true),
      service.getSnapshot(true),
      service.getSnapshot(true),
      service.getSnapshot(true),
    ];

    const results = await Promise.all(promises);

    // Should only have called screenshot provider once
    assert.strictEqual(callCount, 1, 'screenshot provider should be invoked exactly once for coalesced calls');
    assert.strictEqual(results.length, 5);
    assert.strictEqual(results[0].etag, results[1].etag);
    assert.strictEqual(results[0].png.length, results[1].png.length);
  });

  it('HTTP Server endpoints: /healthz, /eink.png 200, and 304 Not Modified caching', async () => {
    const mockRgba = Buffer.alloc(800 * 480 * 4);
    for (let i = 0; i < 800 * 480; i++) {
      mockRgba[i * 4] = i % 256;
      mockRgba[i * 4 + 1] = i % 256;
      mockRgba[i * 4 + 2] = i % 256;
      mockRgba[i * 4 + 3] = 255;
    }

    const { server, captureService } = createServer({
      port: 0,
      screenshotProvider: async () => ({
        rgbaBuffer: mockRgba,
        ditherRects: [{ x: 50, y: 50, width: 200, height: 100 }],
      }),
      debounceWindowMs: 500,
    });

    await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
    const address = server.address();
    const baseURL = `http://127.0.0.1:${address.port}`;

    try {
      // 1. /healthz
      const healthRes = await fetch(`${baseURL}/healthz`);
      assert.strictEqual(healthRes.status, 200);
      const healthBody = await healthRes.json();
      assert.strictEqual(healthBody.status, 'ok');

      // 2. /eink.png (200 OK)
      const pngRes = await fetch(`${baseURL}/eink.png`);
      assert.strictEqual(pngRes.status, 200);
      assert.strictEqual(pngRes.headers.get('content-type'), 'image/png');
      const etag = pngRes.headers.get('etag');
      assert.ok(etag, 'ETag header must be present');
      const pngBytes = await pngRes.arrayBuffer();
      assert.strictEqual(Buffer.from(pngBytes).subarray(0, 8).toString('hex'), '89504e470d0a1a0a');

      // 3. /eink.png with matching If-None-Match (304 Not Modified)
      const cachedRes = await fetch(`${baseURL}/eink.png`, {
        headers: { 'If-None-Match': etag },
      });
      assert.strictEqual(cachedRes.status, 304, 'expected 304 Not Modified on matching ETag');
      assert.strictEqual(cachedRes.headers.get('etag'), etag);
      const emptyBytes = await cachedRes.arrayBuffer();
      assert.strictEqual(emptyBytes.byteLength, 0, '304 response body must be empty');

      // 4. /eink.png with mismatched If-None-Match (200 OK)
      const mismatchRes = await fetch(`${baseURL}/eink.png`, {
        headers: { 'If-None-Match': '"mismatched-etag"' },
      });
      assert.strictEqual(mismatchRes.status, 200);

      // 5. Method not allowed (405)
      const postRes = await fetch(`${baseURL}/eink.png`, { method: 'POST' });
      assert.strictEqual(postRes.status, 405);

      // 6. Unknown route (404)
      const notFoundRes = await fetch(`${baseURL}/random`);
      assert.strictEqual(notFoundRes.status, 404);
    } finally {
      await new Promise((resolve) => server.close(resolve));
    }
  });

  it('HTTP Server cold-boot upstream failure returns 503 Service Unavailable', async () => {
    const { server } = createServer({
      port: 0,
      screenshotProvider: async () => {
        throw new Error('Connection refused to Core');
      },
    });

    await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
    const address = server.address();
    const baseURL = `http://127.0.0.1:${address.port}`;

    try {
      const res = await fetch(`${baseURL}/eink.png`);
      assert.strictEqual(res.status, 503, 'cold boot failure must return 503 Service Unavailable');
      const body = await res.json();
      assert.strictEqual(body.status, 'error');
      assert.ok(body.error.includes('Connection refused to Core'));
    } finally {
      await new Promise((resolve) => server.close(resolve));
    }
  });
});

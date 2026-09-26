/**
 * dither.js - Pure-JS Selective Two-Pass Quantization & 1-Bit Monochrome PNG Engine
 * Reference: SPEC-003 §4-§5, SPEC-009 §3
 */

const zlib = require('zlib');
const crypto = require('crypto');

/**
 * Calculates perceived luminance using the standard ITU-R BT.601 formula.
 * Blends alpha over a solid black foundation (#000000) if alpha < 255.
 *
 * @param {number} r Red channel (0-255)
 * @param {number} g Green channel (0-255)
 * @param {number} b Blue channel (0-255)
 * @param {number} [a=255] Alpha channel (0-255)
 * @returns {number} Luminance value in range [0, 255]
 */
function luminance(r, g, b, a = 255) {
  if (a < 255) {
    const alphaNorm = a / 255;
    r = Math.round(r * alphaNorm);
    g = Math.round(g * alphaNorm);
    b = Math.round(b * alphaNorm);
  }
  return Math.round(0.299 * r + 0.587 * g + 0.114 * b);
}

/**
 * Applies the two-pass selective dithering pipeline:
 * - Pass 1: Strict 1-bit thresholding (luminance < 128 -> black, >= 128 -> white)
 *   for text, numbers, borders, and UI widgets outside `.dither` regions.
 * - Pass 2: Floyd-Steinberg error-diffusion dithering strictly within `.dither`
 *   bounding boxes (photos and continuous-tone weather icons).
 *
 * Guaranteed Invariant: Error diffusion never bleeds outside `.dither` bounding boxes.
 *
 * @param {Buffer|Uint8Array} rgbaBuffer Raw 8-bit RGBA pixel buffer (width * height * 4 bytes)
 * @param {number} width Canvas width in pixels (e.g. 800)
 * @param {number} height Canvas height in pixels (e.g. 480)
 * @param {Array<{x: number, y: number, width: number, height: number}>} [ditherRects=[]]
 * @returns {Uint8Array} 1-byte-per-pixel array (length: width * height), each value 0 (black) or 255 (white).
 */
function applySelectiveDithering(rgbaBuffer, width = 800, height = 480, ditherRects = []) {
  const totalPixels = width * height;
  const isDither = new Uint8Array(totalPixels);

  // Rasterize dither bounding boxes into 2D bitmask with boundary clamping.
  // Rasterizing first cleanly handles nested, overlapping, and out-of-bounds rects.
  if (Array.isArray(ditherRects)) {
    for (const rect of ditherRects) {
      if (!rect || rect.width <= 0 || rect.height <= 0) continue;
      const startX = Math.max(0, Math.floor(rect.x || 0));
      const endX = Math.min(width, Math.ceil((rect.x || 0) + rect.width));
      const startY = Math.max(0, Math.floor(rect.y || 0));
      const endY = Math.min(height, Math.ceil((rect.y || 0) + rect.height));

      for (let y = startY; y < endY; y++) {
        const rowOffset = y * width;
        for (let x = startX; x < endX; x++) {
          isDither[rowOffset + x] = 1;
        }
      }
    }
  }

  // Convert RGBA pixels to working grayscale float buffer
  const gray = new Float32Array(totalPixels);
  for (let i = 0; i < totalPixels; i++) {
    const srcIdx = i * 4;
    gray[i] = luminance(
      rgbaBuffer[srcIdx],
      rgbaBuffer[srcIdx + 1],
      rgbaBuffer[srcIdx + 2],
      rgbaBuffer[srcIdx + 3] !== undefined ? rgbaBuffer[srcIdx + 3] : 255
    );
  }

  const output = new Uint8Array(totalPixels);

  // Single raster-scan pass across the canvas
  for (let y = 0; y < height; y++) {
    const rowOffset = y * width;
    for (let x = 0; x < width; x++) {
      const idx = rowOffset + x;

      if (isDither[idx] === 0) {
        // Pass 1: Strict 1-Bit Thresholding
        // Guaranteed razor-sharp vector edges and clean typography with zero gray noise.
        output[idx] = gray[idx] < 128 ? 0 : 255;
      } else {
        // Pass 2: Floyd-Steinberg Error Diffusion
        const oldVal = Math.min(255, Math.max(0, gray[idx]));
        const newVal = oldVal < 128 ? 0 : 255;
        output[idx] = newVal;
        const err = oldVal - newVal;

        // Diffuse error ONLY to neighbor pixels that are ALSO inside the .dither mask.
        // Drops error if neighbor falls outside the mask or canvas boundaries.
        // (x + 1, y) -> 7/16
        if (x + 1 < width && isDither[idx + 1] === 1) {
          gray[idx + 1] += err * (7 / 16);
        }
        // (x - 1, y + 1) -> 3/16
        if (x - 1 >= 0 && y + 1 < height && isDither[idx + width - 1] === 1) {
          gray[idx + width - 1] += err * (3 / 16);
        }
        // (x, y + 1) -> 5/16
        if (y + 1 < height && isDither[idx + width] === 1) {
          gray[idx + width] += err * (5 / 16);
        }
        // (x + 1, y + 1) -> 1/16
        if (x + 1 < width && y + 1 < height && isDither[idx + width + 1] === 1) {
          gray[idx + width + 1] += err * (1 / 16);
        }
      }
    }
  }

  return output;
}

/**
 * Creates a standard PNG chunk with length, chunk type, data, and CRC-32.
 *
 * @param {string} type 4-character ASCII chunk type (e.g. 'IHDR', 'IDAT', 'IEND')
 * @param {Buffer} data Chunk data buffer
 * @returns {Buffer} Formatted chunk buffer
 */
function createChunk(type, data) {
  const typeBuf = Buffer.from(type, 'ascii');
  const len = data ? data.length : 0;
  const chunk = Buffer.alloc(8 + len + 4);

  chunk.writeUInt32BE(len, 0);
  typeBuf.copy(chunk, 4);
  if (data && len > 0) {
    data.copy(chunk, 8);
  }

  // CRC-32 is calculated over chunk type + chunk data
  const crcPayload = chunk.subarray(4, 8 + len);
  const crc = zlib.crc32(crcPayload);
  chunk.writeUInt32BE(crc >>> 0, 8 + len);

  return chunk;
}

/**
 * Encodes an 800×480 1-bit monochrome image into a valid, standard PNG buffer.
 * Color Type: 0 (Grayscale), Bit Depth: 1.
 *
 * @param {Uint8Array} monoPixels 1-byte-per-pixel array of 0 (black) or 255 (white).
 * @param {number} [width=800] Canvas width in pixels
 * @param {number} [height=480] Canvas height in pixels
 * @returns {Buffer} Standard 1-bit monochrome PNG buffer
 */
function encode1BitPNG(monoPixels, width = 800, height = 480) {
  const bytesPerRow = Math.ceil(width / 8); // 800 / 8 = 100 bytes
  const rowStride = 1 + bytesPerRow;        // 1 filter byte (0x00) + 100 bytes = 101 bytes
  const uncompressed = Buffer.alloc(height * rowStride);

  for (let y = 0; y < height; y++) {
    const rowStart = y * rowStride;
    const pixelRowStart = y * width;
    uncompressed[rowStart] = 0x00; // Filter: 0 (None)

    for (let byteIdx = 0; byteIdx < bytesPerRow; byteIdx++) {
      let b = 0;
      const pixelOffset = pixelRowStart + byteIdx * 8;
      for (let bit = 0; bit < 8; bit++) {
        const pIdx = pixelOffset + bit;
        if (pIdx < pixelRowStart + width) {
          // In standard 1-bit grayscale PNG: 0 = black, 1 = white (MSB first)
          if (monoPixels[pIdx] >= 128) {
            b |= 1 << (7 - bit);
          }
        }
      }
      uncompressed[rowStart + 1 + byteIdx] = b;
    }
  }

  const signature = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);

  // IHDR: 13 bytes
  const ihdrData = Buffer.alloc(13);
  ihdrData.writeUInt32BE(width, 0);
  ihdrData.writeUInt32BE(height, 4);
  ihdrData.writeUInt8(1, 8);  // Bit depth: 1
  ihdrData.writeUInt8(0, 9);  // Color type: 0 (Grayscale)
  ihdrData.writeUInt8(0, 10); // Compression method: 0 (Deflate)
  ihdrData.writeUInt8(0, 11); // Filter method: 0 (Standard adaptive)
  ihdrData.writeUInt8(0, 12); // Interlace method: 0 (None)

  const ihdrChunk = createChunk('IHDR', ihdrData);
  const compressedData = zlib.deflateSync(uncompressed, { level: 9 });
  const idatChunk = createChunk('IDAT', compressedData);
  const iendChunk = createChunk('IEND', Buffer.alloc(0));

  return Buffer.concat([signature, ihdrChunk, idatChunk, iendChunk]);
}

/**
 * Decodes a PNG buffer into raw RGBA pixels (width * height * 4) using Node.js built-ins.
 * Supports standard PNG filter algorithms: 0 (None), 1 (Sub), 2 (Up), 3 (Average), 4 (Paeth).
 * Handles Color Type 6 (RGBA), 2 (RGB), and 0 (Grayscale).
 *
 * @param {Buffer} pngBuffer Encoded PNG buffer
 * @returns {{width: number, height: number, data: Buffer}} Decoded image dimensions and raw RGBA buffer
 */
function decodePNG(pngBuffer) {
  if (pngBuffer.length < 8 || pngBuffer.subarray(0, 8).toString('hex') !== '89504e470d0a1a0a') {
    throw new Error('Invalid PNG signature');
  }

  let offset = 8;
  let width = 0;
  let height = 0;
  let bitDepth = 8;
  let colorType = 6;
  const idatChunks = [];

  while (offset < pngBuffer.length) {
    const length = pngBuffer.readUInt32BE(offset);
    const type = pngBuffer.toString('ascii', offset + 4, offset + 8);
    const data = pngBuffer.subarray(offset + 8, offset + 8 + length);
    offset += 8 + length + 4; // length + type + data + crc

    if (type === 'IHDR') {
      width = data.readUInt32BE(0);
      height = data.readUInt32BE(4);
      bitDepth = data.readUInt8(8);
      colorType = data.readUInt8(9);
    } else if (type === 'IDAT') {
      idatChunks.push(data);
    } else if (type === 'IEND') {
      break;
    }
  }

  if (width === 0 || height === 0) {
    throw new Error('Missing or invalid IHDR chunk in PNG');
  }

  const compressedData = Buffer.concat(idatChunks);
  const uncompressed = zlib.inflateSync(compressedData);

  let filterBpp = 1;
  let rowBytes = width;
  if (colorType === 6) { // RGBA
    filterBpp = 4;
    rowBytes = width * 4;
  } else if (colorType === 2) { // RGB
    filterBpp = 3;
    rowBytes = width * 3;
  } else if (colorType === 0) { // Grayscale
    if (bitDepth === 1) {
      filterBpp = 1;
      rowBytes = Math.ceil(width / 8);
    } else {
      filterBpp = 1;
      rowBytes = width;
    }
  }

  const rgba = Buffer.alloc(width * height * 4);
  const prevRow = Buffer.alloc(rowBytes);
  const currentRow = Buffer.alloc(rowBytes);

  let srcOffset = 0;

  for (let y = 0; y < height; y++) {
    const filterType = uncompressed[srcOffset++];
    const rawScanline = uncompressed.subarray(srcOffset, srcOffset + rowBytes);
    srcOffset += rowBytes;

    for (let x = 0; x < rowBytes; x++) {
      const byteVal = rawScanline[x];
      const left = x >= filterBpp ? currentRow[x - filterBpp] : 0;
      const up = prevRow[x];
      const upLeft = x >= filterBpp ? prevRow[x - filterBpp] : 0;

      let unfiltered = byteVal;
      switch (filterType) {
        case 0: // None
          unfiltered = byteVal;
          break;
        case 1: // Sub
          unfiltered = (byteVal + left) & 0xff;
          break;
        case 2: // Up
          unfiltered = (byteVal + up) & 0xff;
          break;
        case 3: // Average
          unfiltered = (byteVal + Math.floor((left + up) / 2)) & 0xff;
          break;
        case 4: { // Paeth
          const p = left + up - upLeft;
          const pa = Math.abs(p - left);
          const pb = Math.abs(p - up);
          const pc = Math.abs(p - upLeft);
          let pr;
          if (pa <= pb && pa <= pc) pr = left;
          else if (pb <= pc) pr = up;
          else pr = upLeft;
          unfiltered = (byteVal + pr) & 0xff;
          break;
        }
        default:
          unfiltered = byteVal;
      }
      currentRow[x] = unfiltered;
    }

    // Convert unfiltered row to RGBA output
    const dstRowOffset = y * width * 4;
    for (let px = 0; px < width; px++) {
      const dstPxIdx = dstRowOffset + px * 4;
      if (colorType === 6) {
        const srcPxIdx = px * 4;
        rgba[dstPxIdx] = currentRow[srcPxIdx];
        rgba[dstPxIdx + 1] = currentRow[srcPxIdx + 1];
        rgba[dstPxIdx + 2] = currentRow[srcPxIdx + 2];
        rgba[dstPxIdx + 3] = currentRow[srcPxIdx + 3];
      } else if (colorType === 2) {
        const srcPxIdx = px * 3;
        rgba[dstPxIdx] = currentRow[srcPxIdx];
        rgba[dstPxIdx + 1] = currentRow[srcPxIdx + 1];
        rgba[dstPxIdx + 2] = currentRow[srcPxIdx + 2];
        rgba[dstPxIdx + 3] = 255;
      } else if (colorType === 0) {
        let val;
        if (bitDepth === 1) {
          const byteIdx = Math.floor(px / 8);
          const bitIdx = 7 - (px % 8);
          val = ((currentRow[byteIdx] >> bitIdx) & 1) ? 255 : 0;
        } else {
          val = currentRow[px];
        }
        rgba[dstPxIdx] = val;
        rgba[dstPxIdx + 1] = val;
        rgba[dstPxIdx + 2] = val;
        rgba[dstPxIdx + 3] = 255;
      }
    }

    currentRow.copy(prevRow);
  }

  return { width, height, data: rgba };
}

/**
 * Computes a strong SHA-256 ETag wrapped in quotes for HTTP cache validation per SPEC-009 §3.
 *
 * @param {Buffer} buffer Target byte buffer
 * @returns {string} Strong ETag string e.g. '"a1b2c3..."'
 */
function computeETag(buffer) {
  const hash = crypto.createHash('sha256').update(buffer).digest('hex');
  return `"${hash}"`;
}

module.exports = {
  luminance,
  applySelectiveDithering,
  encode1BitPNG,
  decodePNG,
  computeETag,
  createChunk,
};

/**
 * server.js - E-Ink Snapshot Sidecar HTTP Server & ETag Caching Endpoint
 * Reference: SPEC-003 §4-§5, SPEC-009 §3
 */

const http = require('http');
const { CaptureService } = require('./capture');

function createServer(options = {}) {
  const captureService = options.captureService || new CaptureService(options);
  const port = options.port || parseInt(process.env.PORT || '8081', 10);

  const server = http.createServer(async (req, res) => {
    // Basic CORS and headers
    res.setHeader('Access-Control-Allow-Origin', '*');

    if (req.method === 'OPTIONS') {
      res.writeHead(204, {
        'Access-Control-Allow-Methods': 'GET, OPTIONS',
        'Access-Control-Allow-Headers': 'Content-Type, If-None-Match',
      });
      return res.end();
    }

    const url = new URL(req.url, `http://${req.headers.host || 'localhost'}`);

    // Healthcheck endpoint for container supervision
    if (url.pathname === '/healthz' || url.pathname === '/health') {
      res.writeHead(200, { 'Content-Type': 'application/json' });
      return res.end(JSON.stringify({ status: 'ok' }));
    }

    // Main 1-Bit Monochrome Snapshot Endpoint
    if (url.pathname === '/eink.png') {
      if (req.method !== 'GET') {
        res.writeHead(405, { 'Content-Type': 'application/json', 'Allow': 'GET, OPTIONS' });
        return res.end(JSON.stringify({ status: 'error', error: 'method not allowed' }));
      }

      const clientETag = req.headers['if-none-match'];

      try {
        const force = url.searchParams.get('force') === 'true';
        const snapshot = await captureService.getSnapshot(force);

        // Check If-None-Match cache validation
        if (clientETag && clientETag === snapshot.etag) {
          res.writeHead(304, {
            'ETag': snapshot.etag,
            'Cache-Control': 'no-cache, must-revalidate',
          });
          return res.end();
        }

        // Return fresh 800×480 1-bit monochrome PNG
        res.writeHead(200, {
          'Content-Type': 'image/png',
          'Content-Length': snapshot.png.length,
          'ETag': snapshot.etag,
          'Cache-Control': 'no-cache, must-revalidate',
        });
        return res.end(snapshot.png);
      } catch (err) {
        // Fall back to cached frame if available during upstream outage
        if (captureService.cachedFrame && captureService.cachedETag) {
          if (clientETag && clientETag === captureService.cachedETag) {
            res.writeHead(304, {
              'ETag': captureService.cachedETag,
              'Cache-Control': 'no-cache, must-revalidate',
            });
            return res.end();
          }
          res.writeHead(200, {
            'Content-Type': 'image/png',
            'Content-Length': captureService.cachedFrame.length,
            'ETag': captureService.cachedETag,
            'Cache-Control': 'no-cache, must-revalidate',
            'X-Mirrormere-Degraded': 'true',
          });
          return res.end(captureService.cachedFrame);
        }

        // Cold-boot upstream failure: 503 so e-ink display node preserves last image
        res.writeHead(503, { 'Content-Type': 'application/json' });
        return res.end(JSON.stringify({ status: 'error', error: `e-ink render failed: ${err.message}` }));
      }
    }

    res.writeHead(404, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ status: 'error', error: 'not found' }));
  });

  // Slowloris timeout mitigation
  server.headersTimeout = 5000;
  server.requestTimeout = 10000;

  return { server, captureService, port };
}

if (require.main === module) {
  const { server, port } = createServer();
  server.listen(port, '0.0.0.0', () => {
    console.log(`[eink-renderer] listening on 0.0.0.0:${port}`);
  });

  const shutdown = () => {
    console.log('[eink-renderer] stopping server...');
    server.close(() => {
      console.log('[eink-renderer] server stopped');
      process.exit(0);
    });
  };

  process.on('SIGTERM', shutdown);
  process.on('SIGINT', shutdown);
}

module.exports = {
  createServer,
};

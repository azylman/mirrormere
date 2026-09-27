const { test, describe, beforeEach, afterEach } = require('node:test');
const assert = require('node:assert/strict');

describe('Display Stylesheet Freshness Heartbeat (SPEC-003 §3, SPEC-010 §3)', () => {
  let originalFetch;
  let originalDocument;
  let linkElement;

  beforeEach(() => {
    originalFetch = global.fetch;
    originalDocument = global.document;

    linkElement = {
      id: 'hud-stylesheet',
      href: 'http://192.168.1.14/kiosk/style.css',
      rel: 'stylesheet',
    };

    global.document = {
      getElementById(id) {
        if (id === 'hud-stylesheet') return linkElement;
        return null;
      },
      querySelector(selector) {
        if (selector.includes('style.css')) return linkElement;
        return null;
      },
    };
  });

  afterEach(() => {
    global.fetch = originalFetch;
    global.document = originalDocument;

    // Reset module state
    delete require.cache[require.resolve('../static/js/display.js')];
    const display = require('../static/js/display.js');
    display.resetStyleHeartbeatState();
  });

  test('cold boot: initial HEAD request registers timestamps without triggering reload', async () => {
    let fetchCalls = [];
    global.fetch = async (url, options) => {
      fetchCalls.push({ url, options });
      return {
        status: 200,
        headers: {
          get(name) {
            if (name.toLowerCase() === 'last-modified') return 'Sun, 27 Sep 2026 05:11:04 GMT';
            if (name.toLowerCase() === 'etag') return '"abc123etag"';
            return null;
          },
        },
      };
    };

    delete require.cache[require.resolve('../static/js/display.js')];
    const display = require('../static/js/display.js');
    display.resetStyleHeartbeatState();

    const initialHref = linkElement.href;
    await display.checkStyleHeartbeat();

    assert.equal(fetchCalls.length, 1);
    assert.equal(fetchCalls[0].url, 'http://192.168.1.14/kiosk/style.css');
    assert.equal(fetchCalls[0].options.method, 'HEAD');
    assert.deepEqual(fetchCalls[0].options.headers, {});

    // Stylesheet must not be reloaded on initial discovery
    assert.equal(linkElement.href, initialHref);

    const state = display.getStyleHeartbeatState();
    assert.equal(state.lastStyleModified, 'Sun, 27 Sep 2026 05:11:04 GMT');
    assert.equal(state.lastStyleETag, '"abc123etag"');
  });

  test('304 Not Modified: does not trigger stylesheet reload', async () => {
    let responseStatus = 200;
    let fetchCalls = [];

    global.fetch = async (url, options) => {
      fetchCalls.push({ url, options });
      if (responseStatus === 304) {
        return {
          status: 304,
          headers: { get: () => null },
        };
      }
      return {
        status: 200,
        headers: {
          get(name) {
            if (name.toLowerCase() === 'last-modified') return 'Sun, 27 Sep 2026 05:11:04 GMT';
            if (name.toLowerCase() === 'etag') return '"abc123etag"';
            return null;
          },
        },
      };
    };

    delete require.cache[require.resolve('../static/js/display.js')];
    const display = require('../static/js/display.js');
    display.resetStyleHeartbeatState();

    // 1. Initial discovery
    await display.checkStyleHeartbeat();
    const initialHref = linkElement.href;

    // 2. Subsequent 304 Not Modified check
    responseStatus = 304;
    await display.checkStyleHeartbeat();

    assert.equal(fetchCalls.length, 2);
    assert.equal(fetchCalls[1].options.headers['If-Modified-Since'], 'Sun, 27 Sep 2026 05:11:04 GMT');
    assert.equal(fetchCalls[1].options.headers['If-None-Match'], '"abc123etag"');

    // Must not reload on 304
    assert.equal(linkElement.href, initialHref);
  });

  test('200 OK with updated timestamp: hot-swaps stylesheet and updates cache', async () => {
    let currentModified = 'Sun, 27 Sep 2026 05:11:04 GMT';
    let currentETag = '"v1"';

    global.fetch = async (url, options) => {
      return {
        status: 200,
        headers: {
          get(name) {
            if (name.toLowerCase() === 'last-modified') return currentModified;
            if (name.toLowerCase() === 'etag') return currentETag;
            return null;
          },
        },
      };
    };

    delete require.cache[require.resolve('../static/js/display.js')];
    const display = require('../static/js/display.js');
    display.resetStyleHeartbeatState();

    // 1. Initial discovery
    await display.checkStyleHeartbeat();
    assert.equal(linkElement.href, 'http://192.168.1.14/kiosk/style.css');

    // 2. File modified on disk (git-sync / Hangar volume mount update)
    currentModified = 'Sun, 27 Sep 2026 06:25:00 GMT';
    currentETag = '"v2"';

    await display.checkStyleHeartbeat();

    // Link href must have cache-busting timestamp appended
    assert.match(linkElement.href, /^http:\/\/192\.168\.1\.14\/kiosk\/style\.css\?t=\d+$/);

    const state = display.getStyleHeartbeatState();
    assert.equal(state.lastStyleModified, 'Sun, 27 Sep 2026 06:25:00 GMT');
    assert.equal(state.lastStyleETag, '"v2"');
  });

  test('network failure: catches error gracefully without throwing', async () => {
    global.fetch = async () => {
      throw new Error('connection refused');
    };

    delete require.cache[require.resolve('../static/js/display.js')];
    const display = require('../static/js/display.js');
    display.resetStyleHeartbeatState();

    // Must resolve cleanly without uncaught exception
    await assert.doesNotReject(async () => {
      await display.checkStyleHeartbeat();
    });
  });

  test('gracefully handles missing link element or SSR environment', async () => {
    global.document = undefined;

    delete require.cache[require.resolve('../static/js/display.js')];
    const display = require('../static/js/display.js');

    await assert.doesNotReject(async () => {
      await display.checkStyleHeartbeat();
    });
  });
});

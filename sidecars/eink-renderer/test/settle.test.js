/**
 * settle.test.js - Unit tests for the CDP capture readiness-poll logic.
 * Reference: fix/eink-renderer-settle - replaces a fixed 300ms post-navigate
 * sleep (which captured the pre-hydration placeholder) with a poll against
 * the carousel's actual DOM state.
 *
 * These tests exercise `pollForSettle` directly with a fake `evaluateFn` and
 * an injectable `sleep`, so they run instantly with no real Chromium/CDP
 * connection.
 */

const { describe, it } = require('node:test');
const assert = require('node:assert');

const {
  pollForSettle,
  SETTLE_PREDICATE_EXPRESSION,
} = require('../src/capture');

/**
 * Builds a fake `sleep` that just records requested delays and resolves
 * immediately, so tests don't actually wait in real time.
 */
function fakeSleep(log) {
  return (ms) => {
    log.push(ms);
    return Promise.resolve();
  };
}

describe('pollForSettle', () => {
  it('waits the minWaitMs floor before the first check, then returns settled=true immediately if already settled', async () => {
    const sleepLog = [];
    let evaluateCalls = 0;

    const settled = await pollForSettle(
      async () => {
        evaluateCalls++;
        return true;
      },
      {
        minWaitMs: 300,
        pollIntervalMs: 100,
        maxWaitMs: 5000,
        paintDelayMs: 150,
        sleep: fakeSleep(sleepLog),
      }
    );

    assert.strictEqual(settled, true);
    assert.strictEqual(evaluateCalls, 1, 'should settle on the first check after the floor wait');
    // First sleep is the min-wait floor, last sleep is the post-settle paint delay.
    assert.strictEqual(sleepLog[0], 300);
    assert.strictEqual(sleepLog[sleepLog.length - 1], 150);
    // No poll-interval sleeps should have happened since it settled immediately.
    assert.strictEqual(sleepLog.length, 2);
  });

  it('polls at pollIntervalMs until the predicate becomes true, then applies the paint delay', async () => {
    const sleepLog = [];
    let evaluateCalls = 0;

    // Not settled for the first 3 checks, settled on the 4th.
    const settled = await pollForSettle(
      async () => {
        evaluateCalls++;
        return evaluateCalls >= 4;
      },
      {
        minWaitMs: 300,
        pollIntervalMs: 100,
        maxWaitMs: 5000,
        paintDelayMs: 150,
        sleep: fakeSleep(sleepLog),
      }
    );

    assert.strictEqual(settled, true);
    assert.strictEqual(evaluateCalls, 4);
    // floor + 3 poll intervals (between checks 1-2, 2-3, 3-4) + paint delay
    assert.deepStrictEqual(sleepLog, [300, 100, 100, 100, 150]);
  });

  it('gives up at maxWaitMs, calls onTimeout with the elapsed time, and still applies the paint delay without throwing', async () => {
    const sleepLog = [];
    let evaluateCalls = 0;
    let timeoutElapsed = null;

    // Simulate elapsed time advancing by pollIntervalMs on each sleep so
    // Date.now()-based elapsed tracking inside pollForSettle actually moves,
    // even though our fake sleep never really waits.
    let now = 1000;
    const originalNow = Date.now;
    Date.now = () => now;

    const sleep = (ms) => {
      sleepLog.push(ms);
      now += ms;
      return Promise.resolve();
    };

    try {
      const settled = await pollForSettle(
        async () => {
          evaluateCalls++;
          return false; // never settles
        },
        {
          minWaitMs: 300,
          pollIntervalMs: 100,
          maxWaitMs: 1000,
          paintDelayMs: 150,
          onTimeout: (elapsedMs) => {
            timeoutElapsed = elapsedMs;
          },
          sleep,
        }
      );

      assert.strictEqual(settled, false, 'never throws just because it timed out - reports unsettled');
      assert.ok(timeoutElapsed !== null, 'onTimeout should have been called');
      assert.ok(timeoutElapsed >= 1000, `expected elapsed >= maxWaitMs (1000), got ${timeoutElapsed}`);
      // Last sleep applied must still be the paint delay, even on timeout.
      assert.strictEqual(sleepLog[sleepLog.length - 1], 150);
    } finally {
      Date.now = originalNow;
    }
  });

  it('never throws when evaluateFn itself is slow/never resolves settled before the cap', async () => {
    const sleepLog = [];
    // onTimeout omitted entirely - must not throw for lack of a handler.
    const settled = await pollForSettle(
      async () => false,
      {
        minWaitMs: 10,
        pollIntervalMs: 10,
        maxWaitMs: 25,
        paintDelayMs: 5,
        sleep: fakeSleep(sleepLog),
      }
    );
    assert.strictEqual(settled, false);
  });

  it('treats a transient evaluateFn rejection as "not settled yet" rather than propagating it', async () => {
    // Simulates a CDP Runtime.evaluate transiently failing right after a
    // navigation (e.g. "Execution context was destroyed") before the page
    // has finished settling.
    const sleepLog = [];
    let evaluateCalls = 0;

    const settled = await pollForSettle(
      async () => {
        evaluateCalls++;
        if (evaluateCalls <= 2) {
          throw new Error('Execution context was destroyed');
        }
        return true;
      },
      {
        minWaitMs: 10,
        pollIntervalMs: 10,
        maxWaitMs: 5000,
        paintDelayMs: 5,
        sleep: fakeSleep(sleepLog),
      }
    );

    assert.strictEqual(settled, true, 'should recover from transient evaluate errors and eventually settle');
    assert.strictEqual(evaluateCalls, 3);
  });

  it('never rejects even if evaluateFn keeps throwing all the way to the timeout', async () => {
    let now = 0;
    const originalNow = Date.now;
    Date.now = () => now;

    const sleep = (ms) => {
      now += ms;
      return Promise.resolve();
    };

    try {
      let timedOut = false;
      const settled = await pollForSettle(
        async () => {
          throw new Error('permanently broken CDP connection');
        },
        {
          minWaitMs: 10,
          pollIntervalMs: 10,
          maxWaitMs: 50,
          paintDelayMs: 5,
          onTimeout: () => { timedOut = true; },
          sleep,
        }
      );

      assert.strictEqual(settled, false);
      assert.strictEqual(timedOut, true);
    } finally {
      Date.now = originalNow;
    }
  });
});

describe('SETTLE_PREDICATE_EXPRESSION', () => {
  /**
   * The predicate is a string of JS meant to be evaluated in-page via CDP's
   * Runtime.evaluate. We can't run a real DOM here without Chromium, but we
   * can sanity-check it against a minimal `document` stand-in so a future
   * edit to the expression can't silently break its shape (e.g. forgetting
   * to return a boolean, or referencing a selector that doesn't match what
   * carousel.js actually produces).
   */
  function evaluateAgainst(fakeDocument) {
    // eslint-disable-next-line no-new-func
    // Wrap in parens directly after `return` (no line break before the
    // expression) to avoid an ASI pitfall: `return\n(...)` gets a semicolon
    // inserted after `return`, silently making the function return undefined.
    const fn = new Function('document', `return (${SETTLE_PREDICATE_EXPRESSION});`);
    return fn(fakeDocument);
  }

  function makeFakeDocument({ readyState, widgetCount, transitioning, hydrated = true, expected = widgetCount, noCanvas = false }) {
    const dataset = {};
    if (hydrated) {
      dataset.screenHydrated = 'true';
      if (expected !== undefined) dataset.widgetCount = String(expected);
    }
    return {
      readyState,
      querySelectorAll(selector) {
        if (selector === '#grid-canvas [data-widget-id]') {
          return new Array(widgetCount).fill(0);
        }
        throw new Error(`unexpected querySelectorAll selector: ${selector}`);
      },
      querySelector(selector) {
        if (selector === '#grid-canvas.transitioning') {
          return transitioning ? {} : null;
        }
        if (selector === '#grid-canvas') {
          return noCanvas ? null : { dataset };
        }
        throw new Error(`unexpected querySelector selector: ${selector}`);
      },
    };
  }

  it('is NOT settled before document.readyState is complete', () => {
    const doc = makeFakeDocument({ readyState: 'loading', widgetCount: 2, transitioning: false });
    assert.strictEqual(evaluateAgainst(doc), false);
  });

  it('is NOT settled before the carousel has hydrated the screen (pre-hydration placeholder state)', () => {
    const doc = makeFakeDocument({ readyState: 'complete', widgetCount: 0, transitioning: false, hydrated: false });
    assert.strictEqual(evaluateAgainst(doc), false);
  });

  it('is NOT settled when fewer widgets are mounted than the screen expects', () => {
    const doc = makeFakeDocument({ readyState: 'complete', widgetCount: 1, expected: 3, transitioning: false });
    assert.strictEqual(evaluateAgainst(doc), false);
  });

  it('is NOT settled when there is no grid canvas', () => {
    const doc = makeFakeDocument({ readyState: 'complete', widgetCount: 0, transitioning: false, noCanvas: true });
    assert.strictEqual(evaluateAgainst(doc), false);
  });

  it('is NOT settled mid hot-swap transition', () => {
    const doc = makeFakeDocument({ readyState: 'complete', widgetCount: 2, transitioning: true });
    assert.strictEqual(evaluateAgainst(doc), false);
  });

  it('IS settled once loaded, widgets are mounted, and no transition is in flight', () => {
    const doc = makeFakeDocument({ readyState: 'complete', widgetCount: 2, transitioning: false });
    assert.strictEqual(evaluateAgainst(doc), true);
  });

  it('IS settled for a zero-widget (header-only) screen once the canvas is hydrated', () => {
    const doc = makeFakeDocument({ readyState: 'complete', widgetCount: 0, expected: 0, transitioning: false });
    assert.strictEqual(evaluateAgainst(doc), true);
  });
});

describe('captureViaCDP load-event gating', () => {
  const { CaptureService } = require('../src/capture');
  const http = require('node:http');

  /** Minimal fake CDP WebSocket; behaviour is driven by the `script` object. */
  function installFakeWebSocket(script) {
    const original = globalThis.WebSocket;
    const log = [];
    class FakeWS {
      constructor() {
        script.instance = this;
        setImmediate(() => this.onopen && this.onopen());
      }
      emit(method, params = {}) {
        this.onmessage({ data: JSON.stringify({ method, params }) });
      }
      reply(id, result = {}) {
        this.onmessage({ data: JSON.stringify({ id, result }) });
      }
      send(raw) {
        const { id, method, params } = JSON.parse(raw);
        log.push(method);
        script.onSend(this, id, method, params);
      }
      close() {}
    }
    globalThis.WebSocket = FakeWS;
    return { log, restore: () => { globalThis.WebSocket = original; } };
  }

  function makeService(extra = {}) {
    const svc = new CaptureService({
      settleTimeoutMs: 1000,
      sleep: () => Promise.resolve(),
      ...extra,
    });
    svc.getOrCreateTarget = async () => ({ webSocketDebuggerUrl: 'ws://fake' });
    return svc;
  }

  const PNG_B64 = Buffer.from('png').toString('base64');

  it('does not start polling before Page.loadEventFired and ignores a stale-DOM settled result', async () => {
    let loadFired = false;
    let evaluatesBeforeLoad = 0;
    let settleEvaluates = 0;
    const script = {
      onSend(ws, id, method, params) {
        if (method === 'Page.navigate') {
          ws.reply(id, { frameId: 'f' });
          // The new document commits late; fire load well after navigate resolved.
          setTimeout(() => { loadFired = true; ws.emit('Page.loadEventFired', { timestamp: 1 }); }, 40);
        } else if (method === 'Runtime.evaluate') {
          if (params.expression.includes('readyState')) {
            settleEvaluates++;
            if (!loadFired) evaluatesBeforeLoad++;
            // Stale predicate: the previous hydrated DOM claims "settled" until load fires...
            // after load the fresh DOM becomes settled on the second check.
            ws.reply(id, { result: { value: !loadFired ? true : settleEvaluates > 1 } });
          } else {
            ws.reply(id, { result: { value: [] } });
          }
        } else if (method === 'Page.captureScreenshot') {
          ws.reply(id, { data: PNG_B64 });
        } else {
          ws.reply(id);
        }
      },
    };
    const fake = installFakeWebSocket(script);
    try {
      const res = await makeService().captureViaCDP();
      assert.strictEqual(evaluatesBeforeLoad, 0, 'poll must not evaluate before Page.loadEventFired');
      assert.ok(settleEvaluates >= 2, 'the stale "settled" answer must not have been accepted; poll continued after load');
      assert.ok(Buffer.isBuffer(res.pngBuffer));
      const order = fake.log;
      assert.ok(order.indexOf('Page.navigate') < order.indexOf('Runtime.evaluate'));
    } finally {
      fake.restore();
    }
  });

  it('registers the load listener before navigating (load fired synchronously with the navigate reply is not missed)', async () => {
    const script = {
      onSend(ws, id, method) {
        if (method === 'Page.navigate') {
          ws.emit('Page.loadEventFired', {});
          ws.reply(id, {});
        } else if (method === 'Runtime.evaluate') {
          ws.reply(id, { result: { value: true } });
        } else if (method === 'Page.captureScreenshot') {
          ws.reply(id, { data: PNG_B64 });
        } else {
          ws.reply(id);
        }
      },
    };
    const fake = installFakeWebSocket(script);
    try {
      // 5s timeout would fail the test runner's patience if the event were missed;
      // use a tiny timeout so a miss shows up as a warning-path difference.
      const warnings = [];
      const origWarn = console.warn;
      console.warn = (m) => warnings.push(String(m));
      try {
        await makeService({ loadEventTimeoutMs: 2000 }).captureViaCDP();
      } finally {
        console.warn = origWarn;
      }
      assert.deepStrictEqual(warnings.filter((w) => w.includes('loadEventFired')), []);
    } finally {
      fake.restore();
    }
  });

  it('falls through to polling with a logged warning if Page.loadEventFired never fires', async () => {
    const script = {
      onSend(ws, id, method) {
        if (method === 'Runtime.evaluate') ws.reply(id, { result: { value: true } });
        else if (method === 'Page.captureScreenshot') ws.reply(id, { data: PNG_B64 });
        else ws.reply(id);
      },
    };
    const fake = installFakeWebSocket(script);
    const warnings = [];
    const origWarn = console.warn;
    console.warn = (m) => warnings.push(String(m));
    try {
      const res = await makeService({ loadEventTimeoutMs: 30 }).captureViaCDP();
      assert.ok(Buffer.isBuffer(res.pngBuffer));
      assert.ok(warnings.some((w) => w.includes('Page.loadEventFired')), 'expected a load-event timeout warning');
    } finally {
      console.warn = origWarn;
      fake.restore();
    }
  });

  describe('getOrCreateTarget status checking', () => {
    function withServer(handler, fn) {
      return new Promise((resolve, reject) => {
        const server = http.createServer(handler);
        server.listen(0, '127.0.0.1', async () => {
          try {
            await fn(`http://127.0.0.1:${server.address().port}`);
            server.close(() => resolve());
          } catch (e) {
            server.close(() => reject(e));
          }
        });
      });
    }

    it('throws a clear error with status and body snippet on a non-2xx /json/list', async () => {
      await withServer((req, res) => { res.statusCode = 503; res.end('upstream unavailable'); }, async (url) => {
        const svc = new CaptureService({ cdpURL: url });
        await assert.rejects(() => svc.getOrCreateTarget(), /\/json\/list returned HTTP 503: upstream unavailable/);
      });
    });

    it('throws a clear error with status and body snippet on a non-2xx /json/new', async () => {
      await withServer((req, res) => {
        if (req.url.startsWith('/json/list')) { res.end('[]'); return; }
        res.statusCode = 405;
        res.end('Using unsafe HTTP verb');
      }, async (url) => {
        const svc = new CaptureService({ cdpURL: url });
        await assert.rejects(() => svc.getOrCreateTarget(), /\/json\/new returned HTTP 405: Using unsafe HTTP verb/);
      });
    });

    it('still returns the target on 2xx', async () => {
      await withServer((req, res) => { res.end(JSON.stringify([{ type: 'page', id: 'x' }])); }, async (url) => {
        const svc = new CaptureService({ cdpURL: url });
        assert.strictEqual((await svc.getOrCreateTarget()).id, 'x');
      });
    });
  });
});

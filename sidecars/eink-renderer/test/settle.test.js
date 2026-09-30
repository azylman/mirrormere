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

  function makeFakeDocument({ readyState, widgetCount, transitioning }) {
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
        throw new Error(`unexpected querySelector selector: ${selector}`);
      },
    };
  }

  it('is NOT settled before document.readyState is complete', () => {
    const doc = makeFakeDocument({ readyState: 'loading', widgetCount: 2, transitioning: false });
    assert.strictEqual(evaluateAgainst(doc), false);
  });

  it('is NOT settled when no widgets have been mounted yet (the pre-hydration placeholder state)', () => {
    const doc = makeFakeDocument({ readyState: 'complete', widgetCount: 0, transitioning: false });
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
});

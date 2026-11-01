#!/usr/bin/env node
/**
 * bench.mjs - display performance bench for the legacy and React displays.
 *
 * Launches headless Chrome, loads <url>?perf=1 with CPU throttling, waits for the first
 * screen to hydrate, sends horizontal touch swipes over raw CDP, then prints
 * window.__mmPerf.snapshot() to stdout as JSON. Progress goes to stderr. Works for the legacy
 * and React displays; the page decides which (perf.js detects it).
 *
 * Usage:
 *   node scripts/perf/bench.mjs --url http://localhost:8080/display \
 *     [--chrome <path>] [--cpu-throttle 4] [--swipes 20] [--swipe-x <px>] [--swipe-y <px>]
 *
 * The output is the snapshot plus long_task_during_swipes (the long tasks recorded between the
 * first and last swipe, which excludes cold-load tasks), swipes_sent and swipes_settled (the
 * swipe_settle samples recorded during the swipes). Exits 2 when swipes_settled is under 80% of
 * swipes_sent.
 *
 * Each swipe is centred on --swipe-x/--swipe-y (default: viewport centre), moved off any
 * swipe-blocking widget by pickSwipePoint (swipe-point.mjs).
 *
 * --chrome defaults to $CHROME_BIN, then "google-chrome". Requires Node 22+ (global WebSocket).
 * No npm dependencies.
 */
import { spawn } from 'node:child_process';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { pickSwipePoint } from './swipe-point.mjs';

const VIEWPORT = { width: 1920, height: 1080 };
const SWIPE_BLOCKERS = '.widget-calendar-grid, .widget-calendar-family, [data-prevent-screen-swipe]';
const MIN_SETTLE_RATIO = 0.8;
const SWIPE_DISTANCE_PX = 300;
const SWIPE_DURATION_MS = 120;
const SWIPE_MOVES = 8;
const SWIPE_GAP_MS = 1000;
const HYDRATE_TIMEOUT_MS = 30000;
const CHROME_START_TIMEOUT_MS = 15000;

class UsageError extends Error {}

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

export function parseArgs(argv) {
  const opts = {
    url: null,
    chrome: process.env.CHROME_BIN || 'google-chrome',
    cpuThrottle: 4,
    swipes: 20,
    swipeX: null,
    swipeY: null,
    help: false,
  };
  for (let i = 0; i < argv.length; i++) {
    const flag = argv[i];
    const take = () => {
      const value = argv[++i];
      if (value === undefined) throw new UsageError(`${flag} requires a value`);
      return value;
    };
    switch (flag) {
      case '--url': opts.url = take(); break;
      case '--chrome': opts.chrome = take(); break;
      case '--cpu-throttle': opts.cpuThrottle = Number(take()); break;
      case '--swipes': opts.swipes = Number(take()); break;
      case '--swipe-x': opts.swipeX = Number(take()); break;
      case '--swipe-y': opts.swipeY = Number(take()); break;
      case '-h':
      case '--help': opts.help = true; break;
      default: throw new UsageError(`unknown argument ${flag}`);
    }
  }
  if (opts.help) return opts;
  if (!opts.url) throw new UsageError('--url is required');
  let parsed;
  try {
    parsed = new URL(opts.url);
  } catch {
    throw new UsageError(`--url is not a valid URL: ${opts.url}`);
  }
  if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
    throw new UsageError('--url must be http or https');
  }
  if (!Number.isFinite(opts.cpuThrottle) || opts.cpuThrottle < 1) {
    throw new UsageError('--cpu-throttle must be a number >= 1');
  }
  if (!Number.isInteger(opts.swipes) || opts.swipes < 0 || opts.swipes > 1000) {
    throw new UsageError('--swipes must be an integer between 0 and 1000');
  }
  for (const [flag, value, max] of [['--swipe-x', opts.swipeX, VIEWPORT.width], ['--swipe-y', opts.swipeY, VIEWPORT.height]]) {
    if (value !== null && (!Number.isFinite(value) || value < 0 || value > max)) {
      throw new UsageError(`${flag} must be a number between 0 and ${max}`);
    }
  }
  parsed.searchParams.set('perf', '1');
  opts.pageURL = parsed.toString();
  return opts;
}

/** Minimal CDP client over a single page websocket. */
class CDPSession {
  constructor(wsUrl) {
    this.ws = new WebSocket(wsUrl);
    this.nextId = 1;
    this.pending = new Map();
    this.ws.addEventListener('message', (event) => {
      const msg = JSON.parse(event.data);
      if (msg.id === undefined || !this.pending.has(msg.id)) return;
      const { resolve, reject } = this.pending.get(msg.id);
      this.pending.delete(msg.id);
      if (msg.error) reject(new Error(`CDP ${msg.error.code}: ${msg.error.message}`));
      else resolve(msg.result);
    });
  }

  open() {
    return new Promise((resolve, reject) => {
      this.ws.addEventListener('open', () => resolve(), { once: true });
      this.ws.addEventListener('error', () => reject(new Error('CDP websocket failed to connect')), { once: true });
    });
  }

  send(method, params = {}) {
    const id = this.nextId++;
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
      this.ws.send(JSON.stringify({ id, method, params }));
    });
  }

  close() {
    this.ws.close();
  }
}

async function evaluate(cdp, expression) {
  const { result, exceptionDetails } = await cdp.send('Runtime.evaluate', {
    expression,
    returnByValue: true,
    awaitPromise: true,
  });
  if (exceptionDetails) {
    const detail = exceptionDetails.exception?.description || exceptionDetails.text || JSON.stringify(exceptionDetails);
    throw new Error(`page evaluation failed: ${detail.split('\n')[0]}`);
  }
  return result.value;
}

/** Starts Chrome with a random DevTools port and returns { proc, port, profileDir }. */
async function launchChrome(chromePath) {
  const profileDir = await mkdtemp(join(tmpdir(), 'mm-perf-'));
  const proc = spawn(chromePath, [
    '--headless=new',
    // Root (0:0) Docker CI containers run without user namespaces and default to 64 MB /dev/shm.
    '--no-sandbox',
    '--disable-dev-shm-usage',
    '--remote-debugging-port=0',
    `--user-data-dir=${profileDir}`,
    '--no-first-run',
    '--no-default-browser-check',
    '--disable-background-networking',
    `--window-size=${VIEWPORT.width},${VIEWPORT.height}`,
    'about:blank',
  ], { stdio: ['ignore', 'ignore', 'pipe'] });

  const portReady = new Promise((resolve, reject) => {
    let stderr = '';
    const timer = setTimeout(() => {
      reject(new Error(`Chrome did not expose DevTools within ${CHROME_START_TIMEOUT_MS} ms; stderr: ${stderr.slice(-400)}`));
    }, CHROME_START_TIMEOUT_MS);
    proc.stderr.on('data', (chunk) => {
      stderr += chunk.toString();
      const match = stderr.match(/DevTools listening on ws:\/\/[^:\s]+:(\d+)\//);
      if (match) {
        clearTimeout(timer);
        resolve(Number(match[1]));
      }
    });
    proc.once('error', (err) => {
      clearTimeout(timer);
      reject(new Error(`failed to start Chrome (${chromePath}): ${err.message}`));
    });
    proc.once('exit', (code) => {
      clearTimeout(timer);
      reject(new Error(`Chrome exited early with code ${code}; stderr: ${stderr.slice(-400)}`));
    });
  });
  let port;
  try {
    port = await portReady;
  } catch (err) {
    // Startup failed: runBench's finally never sees this Chrome, so clean it up here.
    proc.kill('SIGTERM');
    await rm(profileDir, { recursive: true, force: true }).catch(() => {});
    throw err;
  }
  return { proc, port, profileDir };
}

/** Returns the websocket URL of the first page target, creating one if Chrome has none. */
async function pageWebSocketURL(port) {
  const base = `http://127.0.0.1:${port}`;
  const targets = await (await fetch(`${base}/json/list`)).json();
  const page = targets.find((t) => t.type === 'page');
  if (page) return page.webSocketDebuggerUrl;
  const created = await (await fetch(`${base}/json/new?about:blank`, { method: 'PUT' })).json();
  return created.webSocketDebuggerUrl;
}

async function waitForHydration(cdp) {
  const deadline = Date.now() + HYDRATE_TIMEOUT_MS;
  const expression = `(function () {
    var c = document.querySelector('#grid-canvas');
    return !!(window.__mmPerf && c && c.dataset.screenHydrated === 'true');
  })()`;
  while (Date.now() < deadline) {
    try {
      if (await evaluate(cdp, expression)) return;
    } catch {
      // Execution context can be replaced mid-navigation; keep polling.
    }
    await sleep(100);
  }
  throw new Error(`screen did not hydrate within ${HYDRATE_TIMEOUT_MS} ms (is the display page reachable?)`);
}

async function dispatchTouch(cdp, type, touchPoints) {
  await cdp.send('Input.dispatchTouchEvent', { type, touchPoints });
}

const SWIPE_RECTS_EXPRESSION = `(function () {
  var rect = function (r) { return { left: r.left, top: r.top, right: r.right, bottom: r.bottom }; };
  var blocked = Array.from(document.querySelectorAll(${JSON.stringify(SWIPE_BLOCKERS)}))
    .map(function (el) { return rect(el.getBoundingClientRect()); });
  var canvas = document.getElementById('grid-canvas');
  var widgets = canvas
    ? Array.from(canvas.querySelectorAll('[data-widget-id]')).map(function (el) { return rect(el.getBoundingClientRect()); })
    : [];
  return JSON.stringify({ blocked: blocked, widgets: widgets });
})()`;

/** Rects are clipped to the viewport: a touch cannot land off screen, so a widget's centre must not either. */
function clipToViewport(r) {
  return {
    left: Math.max(0, r.left),
    top: Math.max(0, r.top),
    right: Math.min(VIEWPORT.width, r.right),
    bottom: Math.min(VIEWPORT.height, r.bottom),
  };
}

/** Swipe-blocking rects and widget rects on the current pane, in viewport CSS pixels. */
async function swipeRects(cdp) {
  const { blocked, widgets } = JSON.parse(await evaluate(cdp, SWIPE_RECTS_EXPRESSION));
  return { blocked: blocked.map(clipToViewport), widgets: widgets.map(clipToViewport) };
}

/** Samples of a metric recorded so far on this page (the retained count, as in the snapshot). */
async function sampleCount(cdp, metric) {
  return JSON.parse(await evaluate(cdp, `JSON.stringify(window.__mmPerf.snapshot().${metric}.count)`));
}

/** One horizontal swipe centred on `centre`: 300 px over 120 ms in 8 moves. direction is 'left' (next) or 'right' (prev). */
async function swipe(cdp, direction, centre) {
  const y = Math.round(centre.y);
  const half = SWIPE_DISTANCE_PX / 2;
  const startX = Math.round(centre.x + (direction === 'left' ? half : -half));
  const dx = direction === 'left' ? -SWIPE_DISTANCE_PX : SWIPE_DISTANCE_PX;
  const stepMs = SWIPE_DURATION_MS / SWIPE_MOVES;

  await dispatchTouch(cdp, 'touchStart', [{ x: startX, y }]);
  for (let i = 1; i <= SWIPE_MOVES; i++) {
    await sleep(stepMs);
    await dispatchTouch(cdp, 'touchMove', [{ x: Math.round(startX + (dx * i) / SWIPE_MOVES), y }]);
  }
  await dispatchTouch(cdp, 'touchEnd', []);
}

export async function runBench(opts, log = (msg) => process.stderr.write(`${msg}\n`)) {
  const chrome = await launchChrome(opts.chrome);
  let cdp = null;
  try {
    log(`chrome: devtools port ${chrome.port}`);
    cdp = new CDPSession(await pageWebSocketURL(chrome.port));
    await cdp.open();

    await cdp.send('Page.enable');
    await cdp.send('Emulation.setDeviceMetricsOverride', { ...VIEWPORT, deviceScaleFactor: 1, mobile: false });
    await cdp.send('Emulation.setTouchEmulationEnabled', { enabled: true, maxTouchPoints: 1 });
    await cdp.send('Emulation.setCPUThrottlingRate', { rate: opts.cpuThrottle });

    const nav = await cdp.send('Page.navigate', { url: opts.pageURL });
    if (nav.errorText) throw new Error(`navigation to ${opts.pageURL} failed: ${nav.errorText}`);
    log(`loaded ${opts.pageURL} (cpu throttle ${opts.cpuThrottle}x)`);

    await waitForHydration(cdp);
    // Drop the about:blank entry Chrome started with so a right swipe has no history to go back to.
    await cdp.send('Page.resetNavigationHistory');
    // Allow cold-load hydration microtasks, style passes, and initial effects to settle before measuring.
    await sleep(SWIPE_GAP_MS);
    log(`screen hydrated; sending ${opts.swipes} swipes`);

    // Long tasks and settles from cold load are in the snapshot too. The swipe window is measured
    // by the difference, which is what the CI budget's "long tasks during swipes" means.
    const requested = { x: opts.swipeX ?? VIEWPORT.width / 2, y: opts.swipeY ?? VIEWPORT.height / 2 };
    const longTasksBefore = await sampleCount(cdp, 'long_task');
    const settledBefore = await sampleCount(cdp, 'swipe_settle');
    let sent = 0;
    for (let i = 0; i < opts.swipes; i++) {
      const { blocked, widgets } = await swipeRects(cdp);
      const centre = pickSwipePoint(requested, blocked, widgets);
      await swipe(cdp, i % 2 === 0 ? 'left' : 'right', centre);
      sent++;
      await sleep(SWIPE_GAP_MS);
    }
    const during = (await sampleCount(cdp, 'long_task')) - longTasksBefore;
    const settled = (await sampleCount(cdp, 'swipe_settle')) - settledBefore;
    log(`swipes_sent ${sent}, swipes_settled ${settled}`);

    const json = await evaluate(cdp, 'JSON.stringify(window.__mmPerf.snapshot())');
    return {
      ...JSON.parse(json),
      long_task_during_swipes: during,
      swipes_sent: sent,
      swipes_settled: settled,
    };
  } finally {
    if (cdp) cdp.close();
    chrome.proc.kill('SIGTERM');
    await rm(chrome.profileDir, { recursive: true, force: true }).catch(() => {});
  }
}

const USAGE = `usage: node scripts/perf/bench.mjs --url <display-url> [--chrome <path>] [--cpu-throttle N] [--swipes N]
                                  [--swipe-x <px>] [--swipe-y <px>]
  --url           display page to measure, e.g. http://localhost:8080/display (perf=1 is added)
  --chrome        Chrome executable (default: $CHROME_BIN or google-chrome)
  --cpu-throttle  CPU slowdown factor, >= 1 (default 4)
  --swipes        number of horizontal swipes (default 20)
  --swipe-x       swipe centre x in px (default: viewport centre); moved off blocking widgets
  --swipe-y       swipe centre y in px (default: viewport centre)
`;

async function main() {
  let opts;
  try {
    opts = parseArgs(process.argv.slice(2));
  } catch (err) {
    if (err instanceof UsageError) {
      process.stderr.write(`${err.message}\n\n${USAGE}`);
      process.exit(2);
    }
    throw err;
  }
  if (opts.help) {
    process.stdout.write(USAGE);
    return;
  }
  const snapshot = await runBench(opts);
  process.stdout.write(`${JSON.stringify(snapshot, null, 2)}\n`);
  if (snapshot.swipes_settled < MIN_SETTLE_RATIO * snapshot.swipes_sent) {
    process.stderr.write(
      `only ${snapshot.swipes_settled} of ${snapshot.swipes_sent} swipes settled (need ${MIN_SETTLE_RATIO * 100}%); `
      + 'the swipe centre is probably on a blocking widget (see --swipe-x/--swipe-y)\n',
    );
    process.exitCode = 2;
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch((err) => {
    process.stderr.write(`bench failed: ${err.message}\n`);
    process.exit(1);
  });
}

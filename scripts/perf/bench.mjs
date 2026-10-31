#!/usr/bin/env node
/**
 * bench.mjs - display performance bench for the legacy UI.
 *
 * Launches headless Chrome, loads <url>?perf=1 with CPU throttling, waits for the first
 * screen to hydrate, sends horizontal touch swipes over raw CDP, then prints
 * window.__mmPerf.snapshot() to stdout as JSON. Progress goes to stderr.
 *
 * Usage:
 *   node scripts/perf/bench.mjs --url http://localhost:8080/display \
 *     [--chrome <path>] [--cpu-throttle 4] [--swipes 20]
 *
 * --chrome defaults to $CHROME_BIN, then "google-chrome". Requires Node 22+ (global WebSocket).
 * No npm dependencies.
 */
import { spawn } from 'node:child_process';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';

const VIEWPORT = { width: 1920, height: 1080 };
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
    '--remote-debugging-port=0',
    `--user-data-dir=${profileDir}`,
    '--no-first-run',
    '--no-default-browser-check',
    '--disable-background-networking',
    `--window-size=${VIEWPORT.width},${VIEWPORT.height}`,
    'about:blank',
  ], { stdio: ['ignore', 'ignore', 'pipe'] });

  const port = await new Promise((resolve, reject) => {
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

/** One horizontal swipe: 300 px over 120 ms in 8 moves. direction is 'left' (next) or 'right' (prev). */
async function swipe(cdp, direction) {
  const y = Math.round(VIEWPORT.height / 2);
  const half = SWIPE_DISTANCE_PX / 2;
  const startX = Math.round(VIEWPORT.width / 2 + (direction === 'left' ? half : -half));
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
    log(`screen hydrated; sending ${opts.swipes} swipes`);

    for (let i = 0; i < opts.swipes; i++) {
      await swipe(cdp, i % 2 === 0 ? 'left' : 'right');
      await sleep(SWIPE_GAP_MS);
    }

    const json = await evaluate(cdp, 'JSON.stringify(window.__mmPerf.snapshot())');
    return JSON.parse(json);
  } finally {
    if (cdp) cdp.close();
    chrome.proc.kill('SIGTERM');
    await rm(chrome.profileDir, { recursive: true, force: true }).catch(() => {});
  }
}

const USAGE = `usage: node scripts/perf/bench.mjs --url <display-url> [--chrome <path>] [--cpu-throttle N] [--swipes N]
  --url           display page to measure, e.g. http://localhost:8080/display (perf=1 is added)
  --chrome        Chrome executable (default: $CHROME_BIN or google-chrome)
  --cpu-throttle  CPU slowdown factor, >= 1 (default 4)
  --swipes        number of horizontal swipes (default 20)
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
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch((err) => {
    process.stderr.write(`bench failed: ${err.message}\n`);
    process.exit(1);
  });
}

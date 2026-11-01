const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { pathToFileURL } = require('node:url');

const budgetPath = path.join(__dirname, '../../scripts/perf/budget.json');
const checkPath = path.join(__dirname, '../../scripts/perf/check-budget.mjs');

async function loadChecker() {
  return import(pathToFileURL(checkPath).href);
}

const stats = (count, p95, max = p95) => ({ count, p50: p95, p95, max });

/** A bench result that meets every budget. */
function passingBench() {
  return {
    gesture_first_frame: stats(20, 12),
    swipe_settle: stats(20, 280),
    interaction: stats(380, 40),
    frame: stats(290, 16.7, 16.8),
    long_task: stats(1, 79),
    screen_ready: stats(1, 650),
    widget_update: stats(0, null, null),
    long_task_during_swipes: 0,
  };
}

test('budget.json carries the #765 targets at +20% and the bundle cap', () => {
  const budget = JSON.parse(fs.readFileSync(budgetPath, 'utf8'));
  const byKey = Object.fromEntries(budget.metrics.map((m) => [`${m.metric} ${m.stat}`, m.max]));
  assert.equal(byKey['gesture_first_frame p95'], 20.4);
  assert.equal(byKey['swipe_settle p95'], 360);
  assert.equal(byKey['interaction p95'], 120);
  assert.equal(byKey['frame p95'], 20.04);
  assert.equal(byKey['frame max'], 39.6);
  assert.equal(byKey['screen_ready p95'], 1800);
  assert.equal(byKey['widget_update p95'], 60);
  assert.deepEqual(budget.long_task_during_swipes, { max: 0 });
  assert.deepEqual(budget.app_js_gzip_bytes, { max: 153600 });
});

test('checkBudget passes a bench within every budget', async () => {
  const { checkBudget } = await loadChecker();
  const budget = JSON.parse(fs.readFileSync(budgetPath, 'utf8'));
  const results = checkBudget({ bench: passingBench(), budget, appJsGzipBytes: 78000 });
  assert.equal(results.filter((r) => !r.ok).length, 0);
  // widget_update has no samples on React: reported as skipped, not failed.
  const widget = results.find((r) => r.name === 'widget_update p95');
  assert.equal(widget.skipped, true);
});

test('checkBudget fails a metric over its budget, and gzip or long tasks over theirs', async () => {
  const { checkBudget } = await loadChecker();
  const budget = JSON.parse(fs.readFileSync(budgetPath, 'utf8'));
  const bench = passingBench();
  bench.swipe_settle = stats(20, 361);
  bench.long_task_during_swipes = 1;
  const results = checkBudget({ bench, budget, appJsGzipBytes: 153601 });
  const failed = results.filter((r) => !r.ok).map((r) => r.name).sort();
  assert.deepEqual(failed, ['app.js gzip bytes', 'long_task during swipes', 'swipe_settle p95']);
});

test('a bench that dropped swipes fails on the minimum sample count, not on a lucky p95', async () => {
  const { checkBudget } = await loadChecker();
  const budget = JSON.parse(fs.readFileSync(budgetPath, 'utf8'));
  const bench = passingBench();
  bench.swipe_settle = stats(1, 270); // one settle out of twenty swipes
  const result = checkBudget({ bench, budget, appJsGzipBytes: 78000 }).find((r) => r.name === 'swipe_settle p95');
  assert.equal(result.ok, false);
  assert.match(result.message, /1 samples, at least 15 required/);
});

test('a metric missing from the bench output fails the count, and a missing long-task key fails', async () => {
  const { checkBudget } = await loadChecker();
  const budget = JSON.parse(fs.readFileSync(budgetPath, 'utf8'));
  const bench = passingBench();
  delete bench.gesture_first_frame;
  delete bench.long_task_during_swipes;
  const results = checkBudget({ bench, budget, appJsGzipBytes: 78000 });
  assert.equal(results.find((r) => r.name === 'gesture_first_frame p95').ok, false);
  assert.equal(results.find((r) => r.name === 'long_task during swipes').ok, false);
});

test('the CLI exits 1 on an over-budget bench and 0 on a passing one', () => {
  const { spawnSync } = require('node:child_process');
  const os = require('node:os');
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'mm-budget-'));
  const benchPath = path.join(dir, 'bench.json');
  const appPath = path.join(dir, 'app.js');
  fs.writeFileSync(appPath, 'x'.repeat(1000));
  const bad = passingBench();
  bad.gesture_first_frame = stats(20, 25);
  fs.writeFileSync(benchPath, JSON.stringify(bad));
  const run = (bench) => spawnSync(process.execPath, [checkPath, '--bench', bench, '--app-js', appPath], { encoding: 'utf8' });

  const failing = run(benchPath);
  assert.equal(failing.status, 1);
  assert.match(failing.stdout, /FAIL gesture_first_frame p95/);

  fs.writeFileSync(benchPath, JSON.stringify(passingBench()));
  const passing = run(benchPath);
  assert.equal(passing.status, 0, passing.stdout + passing.stderr);

  assert.equal(spawnSync(process.execPath, [checkPath], { encoding: 'utf8' }).status, 2);
  fs.rmSync(dir, { recursive: true, force: true });
});

#!/usr/bin/env node
/**
 * check-budget.mjs - compares scripts/perf/bench.mjs output with scripts/perf/budget.json.
 *
 * Usage:
 *   node scripts/perf/check-budget.mjs --bench <bench.json> [--budget scripts/perf/budget.json]
 *     [--app-js web/static/ui/app.js]
 *
 * Prints one line per check. Exits 1 when a metric exceeds its budget, a required metric has no
 * samples, long tasks during swipes exceed the budget, or the gzipped app.js exceeds its budget.
 * Exits 2 on usage errors. No npm dependencies.
 */
import { readFileSync } from 'node:fs';
import { gzipSync } from 'node:zlib';
import { dirname, join } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const HERE = dirname(fileURLToPath(import.meta.url));
const DEFAULT_BUDGET = join(HERE, 'budget.json');
const DEFAULT_APP_JS = join(HERE, '../../web/static/ui/app.js');

class UsageError extends Error {}

/**
 * Compares a bench result and an app.js gzip size with a budget. Returns one result per check:
 * { name, ok, skipped, value, max, message }. Pure: no I/O.
 */
export function checkBudget({ bench, budget, appJsGzipBytes }) {
  const results = [];

  for (const check of budget.metrics) {
    const name = `${check.metric} ${check.stat}`;
    const stats = bench[check.metric];
    const count = stats ? stats.count : 0;
    const minCount = check.min_count ?? 0;
    const value = count > 0 ? stats[check.stat] : null;
    if (count < minCount) {
      results.push({ name, ok: false, skipped: false, value, max: check.max, message: `${count} samples, at least ${minCount} required` });
      continue;
    }
    if (value === null || value === undefined) {
      results.push({ name, ok: true, skipped: true, value: null, max: check.max, message: 'no samples; not measured' });
      continue;
    }
    const ok = value <= check.max;
    results.push({ name, ok, skipped: false, value, max: check.max, message: `${value} (budget ${check.max})` });
  }

  if (budget.long_task_during_swipes) {
    const max = budget.long_task_during_swipes.max;
    const value = bench.long_task_during_swipes;
    if (typeof value !== 'number') {
      results.push({ name: 'long_task during swipes', ok: false, skipped: false, value: null, max, message: 'missing from bench output' });
    } else {
      results.push({ name: 'long_task during swipes', ok: value <= max, skipped: false, value, max, message: `${value} (budget ${max})` });
    }
  }

  if (budget.app_js_gzip_bytes) {
    const max = budget.app_js_gzip_bytes.max;
    results.push({
      name: 'app.js gzip bytes',
      ok: appJsGzipBytes <= max,
      skipped: false,
      value: appJsGzipBytes,
      max,
      message: `${appJsGzipBytes} (budget ${max})`,
    });
  }

  return results;
}

function parseArgs(argv) {
  const opts = { bench: null, budget: DEFAULT_BUDGET, appJs: DEFAULT_APP_JS };
  for (let i = 0; i < argv.length; i++) {
    const flag = argv[i];
    const take = () => {
      const value = argv[++i];
      if (value === undefined) throw new UsageError(`${flag} requires a value`);
      return value;
    };
    switch (flag) {
      case '--bench': opts.bench = take(); break;
      case '--budget': opts.budget = take(); break;
      case '--app-js': opts.appJs = take(); break;
      default: throw new UsageError(`unknown argument ${flag}`);
    }
  }
  if (!opts.bench) throw new UsageError('--bench is required');
  return opts;
}

const USAGE = `usage: node scripts/perf/check-budget.mjs --bench <bench.json> [--budget <budget.json>] [--app-js <app.js>]
`;

function main() {
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
  const bench = JSON.parse(readFileSync(opts.bench, 'utf8'));
  const budget = JSON.parse(readFileSync(opts.budget, 'utf8'));
  const appJsGzipBytes = gzipSync(readFileSync(opts.appJs)).length;
  const results = checkBudget({ bench, budget, appJsGzipBytes });

  let failed = 0;
  for (const r of results) {
    if (!r.ok) failed++;
    const status = r.ok ? (r.skipped ? 'SKIP' : 'PASS') : 'FAIL';
    process.stdout.write(`${status} ${r.name}: ${r.message}\n`);
  }
  if (failed > 0) {
    process.stdout.write(`${failed} of ${results.length} checks over budget\n`);
    process.exit(1);
  }
  process.stdout.write(`all ${results.length} checks within budget\n`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main();
}

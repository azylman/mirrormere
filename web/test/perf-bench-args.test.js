const { test, before } = require('node:test');
const assert = require('node:assert/strict');
const path = require('node:path');
const { pathToFileURL } = require('node:url');

// scripts/perf/bench.mjs is an ES module; load it with a dynamic import from this CommonJS test.
let parseArgs;
before(async () => {
  const file = path.join(__dirname, '../../scripts/perf/bench.mjs');
  ({ parseArgs } = await import(pathToFileURL(file).href));
});

test('bench --trace-animating is off by default and on when given', () => {
  const base = ['--url', 'http://localhost:8080/display?ui=react'];
  assert.equal(parseArgs(base).traceAnimating, false);
  assert.equal(parseArgs([...base, '--trace-animating']).traceAnimating, true);
});

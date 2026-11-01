const { test, before } = require('node:test');
const assert = require('node:assert/strict');
const path = require('node:path');
const { pathToFileURL } = require('node:url');

// scripts/perf/swipe-point.mjs is an ES module; load it with a dynamic import from this CommonJS test.
let pickSwipePoint;
before(async () => {
  const file = path.join(__dirname, '../../scripts/perf/swipe-point.mjs');
  ({ pickSwipePoint } = await import(pathToFileURL(file).href));
});

const rect = (left, top, right, bottom) => ({ left, top, right, bottom });

test('keeps the point when it is outside every blocked rect', () => {
  const point = { x: 960, y: 540 };
  const blocked = [rect(0, 0, 400, 1080)];
  const widgets = [rect(500, 0, 1900, 1000)];
  assert.equal(pickSwipePoint(point, blocked, widgets), point);
});

test('moves a point inside a block to the centre of the widest clear widget', () => {
  const point = { x: 960, y: 540 };
  const blocked = [rect(800, 0, 1200, 1080)];
  const widgets = [
    rect(0, 0, 300, 300), // clear, width 300
    rect(100, 400, 700, 800), // clear, width 600 (widest clear)
    rect(1300, 0, 1900, 500), // clear, width 600 but later, so not chosen
  ];
  assert.deepEqual(pickSwipePoint(point, blocked, widgets), { x: 400, y: 600 });
});

test('skips a widget that overlaps a block even when it is the widest', () => {
  const point = { x: 960, y: 540 };
  const blocked = [rect(800, 0, 1200, 1080)];
  const widgets = [
    rect(0, 0, 1920, 1080), // widest, but overlaps the block
    rect(0, 0, 500, 500), // clear
  ];
  assert.deepEqual(pickSwipePoint(point, blocked, widgets), { x: 250, y: 250 });
});

test('a widget that only touches a block edge is not overlapping', () => {
  const point = { x: 960, y: 540 };
  const blocked = [rect(800, 0, 1200, 1080)];
  const widgets = [rect(0, 0, 800, 1080)];
  assert.deepEqual(pickSwipePoint(point, blocked, widgets), { x: 400, y: 540 });
});

test('returns the original point when no widget is clear', () => {
  const point = { x: 960, y: 540 };
  const blocked = [rect(800, 0, 1200, 1080)];
  const widgets = [rect(700, 0, 1300, 1080), rect(0, 0, 900, 100)];
  assert.equal(pickSwipePoint(point, blocked, widgets), point);
  assert.equal(pickSwipePoint(point, blocked, []), point);
});

test('ignores zero-area rects so hidden elements cannot block or attract the swipe', () => {
  const point = { x: 960, y: 540 };
  const hiddenBlock = [rect(0, 0, 0, 0)];
  const hiddenWidget = [rect(0, 0, 0, 0)];
  assert.equal(pickSwipePoint(point, hiddenBlock, []), point);
  assert.equal(pickSwipePoint(point, [rect(800, 0, 1200, 1080)], hiddenWidget), point);
});

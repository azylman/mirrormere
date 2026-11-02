const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const carouselCode = fs.readFileSync(path.join(__dirname, '../static/js/carousel.js'), 'utf8');

function setup({ totalScreens = 3 } = {}) {
  const handlers = {};
  const body = { addEventListener: (type, fn) => { handlers[type] = fn; } };
  const doc = { body };
  const win = { location: { search: '' }, innerWidth: 1080 };
  const fn = new Function('window', 'document', 'requestAnimationFrame',
    carouselCode + '\nreturn MirrormereCarousel;');
  const Carousel = fn(win, doc, (cb) => cb());
  const c = Object.create(Carousel.prototype);
  c.canvas = { style: {}, clientWidth: 1000 };
  c.totalScreens = totalScreens;
  c.isPaused = false;
  c.isNavigating = false;
  c.touchIgnored = false;
  c._swipe = null;
  c._swipeOut = null;
  c.advanced = [];
  c.advanceScreen = (dir) => { c.advanced.push(dir); };
  c.bindTouchGestures();
  const t = (x, y) => ({ clientX: x, clientY: y });
  const target = { closest: () => null };
  return {
    c,
    start: (x, y) => handlers.touchstart({ target, touches: [t(x, y)] }),
    move: (x, y) => handlers.touchmove({ target, touches: [t(x, y)] }),
    end: (x, y) => handlers.touchend({ target, changedTouches: [t(x, y)] }),
    cancel: () => handlers.touchcancel({ target }),
  };
}

test('canvas follows a horizontal drag before release', () => {
  const s = setup();
  s.start(800, 500);
  s.move(700, 505);
  assert.equal(s.c.canvas.style.transform, 'translateX(-60px)'); // 100px * 0.6
  assert.doesNotMatch(s.c.canvas.style.transition, /transform/);
  assert.deepEqual(s.c.advanced, []);
});

test('a vertical drag does not move the canvas', () => {
  const s = setup();
  s.start(500, 800);
  s.move(505, 600);
  assert.equal(s.c.canvas.style.transform, undefined);
});

test('a slow deliberate swipe commits (no 800ms cap)', () => {
  const s = setup();
  const realNow = Date.now;
  let now = 1_000_000;
  Date.now = () => now;
  try {
    s.start(800, 500);
    now += 1000; // the clip Ryan posted: ~1 s of drag
    s.move(600, 505);
    s.end(600, 505);
  } finally {
    Date.now = realNow;
  }
  assert.deepEqual(s.c.advanced, ['next']);
  assert.equal(s.c.canvas.style.transform, 'translateX(-250px)'); // slides a quarter out
  assert.ok(s.c._swipeOut);
});

test('a short drag springs back without navigating', () => {
  const s = setup();
  const realNow = Date.now;
  let now = 2_000_000;
  Date.now = () => now;
  try {
    s.start(500, 500);
    now += 400;
    s.move(470, 500);
    s.end(470, 500);
  } finally {
    Date.now = realNow;
  }
  assert.deepEqual(s.c.advanced, []);
  assert.equal(s.c.canvas.style.transform, '');
  assert.match(s.c.canvas.style.transition, /transform/);
});

test('swipe right goes to the previous screen', () => {
  const s = setup();
  s.start(200, 500);
  s.move(400, 500);
  s.end(400, 500);
  assert.deepEqual(s.c.advanced, ['prev']);
});

test('the new screen slides in from the far side once screen.rotate lands', () => {
  const s = setup();
  s.start(800, 500);
  s.move(600, 500);
  s.end(600, 500);
  s.c.finishSwipeIn();
  assert.equal(s.c._swipeOut, null);
  assert.equal(s.c.canvas.style.transform, ''); // settled at 0 after the rAF hop
});

test('a single-screen layout springs back instead of sliding out', () => {
  const s = setup({ totalScreens: 1 });
  s.start(800, 500);
  s.move(600, 500);
  s.end(600, 500);
  assert.equal(s.c._swipeOut, null);
  assert.equal(s.c.canvas.style.transform, '');
});

test('touchcancel springs the canvas back', () => {
  const s = setup();
  s.start(800, 500);
  s.move(700, 500);
  s.cancel();
  assert.equal(s.c.canvas.style.transform, '');
});

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const carouselCode = fs.readFileSync(path.join(__dirname, '../static/js/carousel.js'), 'utf8');

function loadCarousel() {
  const win = { location: { search: '' } };
  const doc = {
    createDocumentFragment: () => ({ children: [], appendChild(n) { this.children.push(n); } }),
    createElement: () => ({ style: {}, dataset: {}, set innerHTML(v) {}, firstElementChild: null }),
  };
  const fn = new Function('window', 'document', 'requestAnimationFrame', 'setTimeout', 'setInterval', 'clearInterval', 'clearTimeout', carouselCode);
  fn(win, doc, () => {}, () => {}, setInterval, clearInterval, clearTimeout);
  return win.MirrormereCarousel;
}

function fakeCanvas() {
  return {
    dataset: {},
    classList: { add() {}, remove() {} },
    appendChild() {},
    addEventListener() {},
    set innerHTML(v) {},
  };
}

test('handleScreenRotate stamps the hydration marker the e-ink settle predicate reads', async () => {
  const Carousel = loadCarousel();
  const canvas = fakeCanvas();
  const c = new Carousel(canvas);
  await c.handleScreenRotate({ widgets: [], current_screen: 0, total_screens: 1 });
  assert.equal(canvas.dataset.screenHydrated, 'true');
  assert.equal(canvas.dataset.widgetCount, '0');
});

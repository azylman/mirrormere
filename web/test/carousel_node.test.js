const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const carouselCode = fs.readFileSync(path.join(__dirname, '../static/js/carousel.js'), 'utf8');

function loadCarousel(search) {
  const win = { location: { search } };
  const fn = new Function('window', 'document', carouselCode + '\nreturn MirrormereCarousel;');
  const Carousel = fn(win, {});
  const instance = Object.create(Carousel.prototype);
  return instance;
}

test('carousel forwards the display node id on widget render requests', async (t) => {
  await t.test('adds ?node= when the page URL carries one', () => {
    const c = loadCarousel('?node=touch-kiosk-kitchen');
    assert.equal(c.renderURL('voice-chat'), 'api/widgets/voice-chat/render?node=touch-kiosk-kitchen');
  });

  await t.test('encodes the node id and widget id', () => {
    const c = loadCarousel('?node=a%20b');
    assert.equal(c.renderURL('w/1'), 'api/widgets/w%2F1/render?node=a%20b');
  });

  await t.test('omits the parameter when no node is set', () => {
    const c = loadCarousel('');
    assert.equal(c.renderURL('voice-chat'), 'api/widgets/voice-chat/render');
  });
});

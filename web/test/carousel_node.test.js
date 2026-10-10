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

test('carousel handleScreenRotate extracts .widget-card and adopts sibling style tag', async () => {
  const win = { location: { search: '' } };
  const styleEl = { tagName: 'STYLE' };
  const cardEl = {
    tagName: 'DIV',
    isWidgetCard: true,
    style: {},
    dataset: {},
    children: [],
    appendChild(n) { this.children.push(n); },
  };

  const doc = {
    createDocumentFragment: () => ({ children: [], appendChild(n) { this.children.push(n); } }),
    createElement: (tag) => {
      if (tag === 'div') {
        return {
          tagName: 'DIV',
          style: {},
          dataset: {},
          set innerHTML(html) {
            this.children = [styleEl, cardEl];
            this.firstElementChild = styleEl;
          },
          querySelector(sel) {
            if (sel === '.widget-card') return cardEl;
            return null;
          },
        };
      }
      return { style: {}, dataset: {}, appendChild() {} };
    },
  };

  const fn = new Function('window', 'document', 'requestAnimationFrame', 'setTimeout', 'setInterval', 'clearInterval', 'clearTimeout', carouselCode);
  fn(win, doc, () => {}, () => {}, setInterval, clearInterval, clearTimeout);
  const Carousel = win.MirrormereCarousel;

  const canvas = {
    dataset: {},
    classList: { add() {}, remove() {} },
    appendChild(fragment) { this.fragment = fragment; },
    addEventListener() {},
  };
  const c = new Carousel(canvas);

  const origFetch = globalThis.fetch;
  globalThis.fetch = async () => ({
    ok: true,
    text: async () => '<style>.foo{}</style><div class="widget-card">Card</div>',
  });

  try {
    await c.handleScreenRotate({
      widgets: [{ widget_id: 'cal', origin: [0, 0], dimensions: [4, 2] }],
      current_screen: 0,
      total_screens: 1,
    });

    assert.equal(c.activeWidgets.get('cal').element, cardEl);
    assert.equal(cardEl.children.includes(styleEl), true, 'should adopt style sibling into .widget-card');
    assert.equal(cardEl.style.gridColumn, '1 / span 4');
    assert.equal(cardEl.style.gridRow, '1 / span 2');
  } finally {
    globalThis.fetch = origFetch;
  }
});

test('carousel touch gesture navigation and isolation', async (t) => {
  const win = { location: { search: '' } };
  const doc = {
    createDocumentFragment: () => ({ children: [], appendChild() {} }),
    createElement: () => ({ style: {}, dataset: {}, appendChild() {} }),
  };

  const fn = new Function('window', 'document', 'requestAnimationFrame', 'setTimeout', 'setInterval', 'clearInterval', 'clearTimeout', carouselCode);
  fn(win, doc, () => {}, () => {}, setInterval, clearInterval, clearTimeout);
  const Carousel = win.MirrormereCarousel;

  function createMockCanvas() {
    const listeners = {};
    return {
      dataset: {},
      classList: { add() {}, remove() {} },
      appendChild() {},
      addEventListener(event, handler) {
        listeners[event] = handler;
      },
      trigger(event, ev) {
        if (listeners[event]) listeners[event](ev);
      },
    };
  }

  function createMockElement(selectors = []) {
    return {
      closest(sel) {
        const parts = sel.split(',').map(s => s.trim());
        for (const part of parts) {
          if (selectors.includes(part)) return this;
        }
        return null;
      },
    };
  }

  await t.test('horizontal swipe starting on .widget-calendar-grid does not trigger advanceScreen even if touchend occurs outside', () => {
    const canvas = createMockCanvas();
    const c = new Carousel(canvas);
    const advanceCalls = [];
    c.advanceScreen = async (dir) => { advanceCalls.push(dir); };

    const calendarElement = createMockElement(['.widget-calendar-grid']);
    const canvasOutside = createMockElement([]);

    // Touchstart inside calendar widget
    canvas.trigger('touchstart', {
      target: calendarElement,
      touches: [{ clientX: 300, clientY: 100 }],
    });
    assert.equal(c.touchIgnored, true);

    // Touchend outside on canvas
    canvas.trigger('touchend', {
      target: canvasOutside,
      changedTouches: [{ clientX: 100, clientY: 100 }],
    });

    assert.equal(advanceCalls.length, 0, 'advanceScreen should not be called');
    assert.equal(c.touchIgnored, false, 'touchIgnored should be reset after touchend');
  });

  await t.test('horizontal swipe starting on [data-prevent-screen-swipe] does not trigger advanceScreen', () => {
    const canvas = createMockCanvas();
    const c = new Carousel(canvas);
    const advanceCalls = [];
    c.advanceScreen = async (dir) => { advanceCalls.push(dir); };

    const preventElement = createMockElement(['[data-prevent-screen-swipe]']);
    const canvasOutside = createMockElement([]);

    canvas.trigger('touchstart', {
      target: preventElement,
      touches: [{ clientX: 300, clientY: 100 }],
    });
    assert.equal(c.touchIgnored, true);

    canvas.trigger('touchend', {
      target: canvasOutside,
      changedTouches: [{ clientX: 100, clientY: 100 }],
    });

    assert.equal(advanceCalls.length, 0, 'advanceScreen should not be called');
    assert.equal(c.touchIgnored, false, 'touchIgnored should be reset after touchend');
  });

  await t.test('normal horizontal swipe outside calendar widgets triggers advanceScreen', () => {
    const canvas = createMockCanvas();
    const c = new Carousel(canvas);
    const advanceCalls = [];
    c.advanceScreen = async (dir) => { advanceCalls.push(dir); };

    const normalElement = createMockElement([]);

    // Swipe left (next)
    canvas.trigger('touchstart', {
      target: normalElement,
      touches: [{ clientX: 300, clientY: 100 }],
    });
    assert.equal(c.touchIgnored, false);

    canvas.trigger('touchend', {
      target: normalElement,
      changedTouches: [{ clientX: 100, clientY: 100 }],
    });
    assert.deepEqual(advanceCalls, ['next']);

    // Swipe right (prev)
    canvas.trigger('touchstart', {
      target: normalElement,
      touches: [{ clientX: 100, clientY: 100 }],
    });
    assert.equal(c.touchIgnored, false);

    canvas.trigger('touchend', {
      target: normalElement,
      changedTouches: [{ clientX: 300, clientY: 100 }],
    });
    assert.deepEqual(advanceCalls, ['next', 'prev']);
  });
});


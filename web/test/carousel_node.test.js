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

test('carousel screen indicator and touch navigation', async (t) => {
  function createMockClassList() {
    const classes = new Set();
    return {
      add(c) { classes.add(c); },
      remove(c) { classes.delete(c); },
      toggle(c, force) {
        if (force === undefined) {
          if (classes.has(c)) classes.delete(c);
          else classes.add(c);
        } else if (force) {
          classes.add(c);
        } else {
          classes.delete(c);
        }
      },
      contains(c) { return classes.has(c); },
    };
  }

  function createMockIndicator() {
    const listeners = {};
    const el = {
      style: {},
      dataset: {},
      classList: createMockClassList(),
      children: [],
      appendChild(child) { this.children.push(child); child.parentElement = this; },
      replaceChildren() { this.children = []; },
      set innerHTML(v) { if (v === '') this.children = []; },
      addEventListener(event, handler) {
        if (!listeners[event]) listeners[event] = [];
        listeners[event].push(handler);
      },
      trigger(event, ev = {}) {
        if (listeners[event]) {
          for (const h of listeners[event]) h(ev);
        }
      },
    };
    return el;
  }

  function createMockButton() {
    const listeners = {};
    const classList = createMockClassList();
    let classNameStr = '';
    const btn = {
      type: 'button',
      style: {},
      dataset: {},
      classList,
      get className() { return classNameStr; },
      set className(v) {
        classNameStr = v;
        (v || '').split(/\s+/).filter(Boolean).forEach(c => classList.add(c));
      },
      attributes: {},
      setAttribute(k, v) { this.attributes[k] = v; },
      getAttribute(k) { return this.attributes[k]; },
      addEventListener(event, handler) {
        if (!listeners[event]) listeners[event] = [];
        listeners[event].push(handler);
      },
      trigger(event, ev = {}) {
        if (listeners[event]) {
          for (const h of listeners[event]) h(ev);
        }
      },
      click() {
        this.trigger('click', { stopPropagation() {} });
      },
    };
    return btn;
  }

  function setupCarouselWithIndicator() {
    const win = { location: { search: '' } };
    const doc = {
      createDocumentFragment: () => ({ children: [], appendChild() {} }),
      createElement: (tag) => {
        if (tag === 'button') return createMockButton();
        return { style: {}, dataset: {}, appendChild() {} };
      },
    };

    const fn = new Function('window', 'document', 'requestAnimationFrame', 'setTimeout', 'setInterval', 'clearInterval', 'clearTimeout', carouselCode);
    fn(win, doc, () => {}, setTimeout, setInterval, clearInterval, clearTimeout);
    const Carousel = win.MirrormereCarousel;

    const canvas = {
      dataset: {},
      classList: createMockClassList(),
      appendChild() {},
      addEventListener() {},
    };
    const indicator = createMockIndicator();
    const c = new Carousel(canvas, indicator);
    return { c, canvas, indicator };
  }

  await t.test('renders dots and marks active dot when total_screens > 1', async () => {
    const { c, indicator } = setupCarouselWithIndicator();
    await c.handleScreenRotate({ widgets: [], current_screen: 0, total_screens: 3 });

    assert.equal(indicator.style.display, 'inline-flex');
    assert.equal(indicator.children.length, 3);
    assert.equal(indicator.children[0].classList.contains('active'), true);
    assert.equal(indicator.children[0].getAttribute('aria-current'), 'step');
    assert.equal(indicator.children[1].classList.contains('active'), false);
    assert.equal(indicator.children[1].getAttribute('aria-current'), 'false');
    assert.equal(indicator.children[2].classList.contains('active'), false);
    assert.equal(indicator.children[2].getAttribute('aria-current'), 'false');
  });

  await t.test('hides indicator when total_screens <= 1', async () => {
    const { c, indicator } = setupCarouselWithIndicator();
    await c.handleScreenRotate({ widgets: [], current_screen: 0, total_screens: 2 });
    assert.equal(indicator.style.display, 'inline-flex');
    assert.equal(indicator.children.length, 2);

    await c.handleScreenRotate({ widgets: [], current_screen: 0, total_screens: 1 });
    assert.equal(indicator.style.display, 'none');
    assert.equal(indicator.children.length, 0);
  });

  await t.test('reuses dot DOM nodes when total_screens is unchanged across rotations', async () => {
    const { c, indicator } = setupCarouselWithIndicator();
    await c.handleScreenRotate({ widgets: [], current_screen: 0, total_screens: 2 });
    const firstDot = indicator.children[0];
    const secondDot = indicator.children[1];

    await c.handleScreenRotate({ widgets: [], current_screen: 1, total_screens: 2 });
    assert.equal(indicator.children[0], firstDot, 'should reuse first dot DOM node');
    assert.equal(indicator.children[1], secondDot, 'should reuse second dot DOM node');
    assert.equal(firstDot.classList.contains('active'), false);
    assert.equal(firstDot.getAttribute('aria-current'), 'false');
    assert.equal(secondDot.classList.contains('active'), true);
    assert.equal(secondDot.getAttribute('aria-current'), 'step');
  });

  await t.test('dot click invokes selectScreen with target screen index', async () => {
    const { c, indicator } = setupCarouselWithIndicator();
    await c.handleScreenRotate({ widgets: [], current_screen: 0, total_screens: 3 });

    const selectCalls = [];
    c.selectScreen = async (idx) => { selectCalls.push(idx); };

    // Click dot 1 (second screen)
    indicator.children[1].click();
    assert.deepEqual(selectCalls, [1]);

    // Clicking current screen (0) should not trigger duplicate navigation
    indicator.children[0].click();
    assert.deepEqual(selectCalls, [1]);
  });

  await t.test('selectScreen dispatches POST to api/screen/select with payload', async () => {
    const { c, indicator } = setupCarouselWithIndicator();
    await c.handleScreenRotate({ widgets: [], current_screen: 0, total_screens: 2 });

    const origFetch = globalThis.fetch;
    const fetchCalls = [];
    globalThis.fetch = async (url, opts) => {
      fetchCalls.push({ url, opts });
      return { ok: true, json: async () => ({}) };
    };

    try {
      await c.selectScreen(1);
      assert.equal(fetchCalls.length, 1);
      assert.equal(fetchCalls[0].url, 'api/screen/select');
      assert.equal(fetchCalls[0].opts.method, 'POST');
      assert.deepEqual(JSON.parse(fetchCalls[0].opts.body), { screen_index: 1 });
    } finally {
      globalThis.fetch = origFetch;
    }
  });

  await t.test('horizontal swipe on indicator triggers advanceScreen', async () => {
    const { c, indicator } = setupCarouselWithIndicator();
    const advanceCalls = [];
    c.advanceScreen = async (dir) => { advanceCalls.push(dir); };

    // Swipe Left (advance to next)
    indicator.trigger('touchstart', {
      touches: [{ clientX: 200, clientY: 100 }],
      stopPropagation() {},
    });
    indicator.trigger('touchmove', {
      touches: [{ clientX: 150, clientY: 100 }],
    });
    indicator.trigger('touchend', {
      changedTouches: [{ clientX: 100, clientY: 100 }],
      stopPropagation() {},
      preventDefault() {},
    });
    assert.deepEqual(advanceCalls, ['next']);

    // Swipe Right (return to prev)
    indicator.trigger('touchstart', {
      touches: [{ clientX: 100, clientY: 100 }],
      stopPropagation() {},
    });
    indicator.trigger('touchmove', {
      touches: [{ clientX: 150, clientY: 100 }],
    });
    indicator.trigger('touchend', {
      changedTouches: [{ clientX: 200, clientY: 100 }],
      stopPropagation() {},
      preventDefault() {},
    });
    assert.deepEqual(advanceCalls, ['next', 'prev']);
  });

  await t.test('dragging on indicator suppresses dot click event', async () => {
    const { c, indicator } = setupCarouselWithIndicator();
    await c.handleScreenRotate({ widgets: [], current_screen: 0, total_screens: 2 });

    const selectCalls = [];
    c.selectScreen = async (idx) => { selectCalls.push(idx); };

    // Simulate drag start and move
    indicator.trigger('touchstart', {
      touches: [{ clientX: 100, clientY: 100 }],
      stopPropagation() {},
    });
    indicator.trigger('touchmove', {
      touches: [{ clientX: 150, clientY: 100 }],
    });

    // Attempt dot click while dragging
    indicator.children[1].click();
    assert.equal(selectCalls.length, 0, 'click should be suppressed during drag');
  });

  await t.test('navigation debounce lock prevents duplicate rapid requests', async () => {
    const { c } = setupCarouselWithIndicator();
    c.totalScreens = 2;
    const origFetch = globalThis.fetch;
    let fetchCount = 0;
    globalThis.fetch = async () => {
      fetchCount++;
      return { ok: true, json: async () => ({}) };
    };

    try {
      // Fire two rapid advanceScreen calls
      c.advanceScreen('next');
      c.advanceScreen('next');
      assert.equal(fetchCount, 1, 'second call should be blocked by navigation lock');

      // screen.rotate event releases lock
      await c.handleScreenRotate({ widgets: [], current_screen: 1, total_screens: 2 });
      assert.equal(c.isNavigating, false, 'lock should be released on screen.rotate');

      c.advanceScreen('next');
      assert.equal(fetchCount, 2, 'subsequent call should succeed after lock release');
    } finally {
      globalThis.fetch = origFetch;
    }
  });

  await t.test('pause and resume toggle paused class and update indicator', async () => {
    const { c, indicator } = setupCarouselWithIndicator();
    await c.handleScreenRotate({ widgets: [], current_screen: 0, total_screens: 2 });

    c.pause();
    assert.equal(c.isPaused, true);
    assert.equal(indicator.classList.contains('paused'), true);

    c.resume();
    assert.equal(c.isPaused, false);
    assert.equal(indicator.classList.contains('paused'), false);
  });
});



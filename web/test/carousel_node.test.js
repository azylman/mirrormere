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

test('carousel layout and indicator styling in hud.css', () => {
  const cssPath = path.resolve(__dirname, '../static/css/hud.css');
  assert.ok(fs.existsSync(cssPath), 'hud.css must exist');
  const css = fs.readFileSync(cssPath, 'utf8');

  // Must define layout variables for indicator space
  assert.match(css, /--mm-indicator-height:\s*16px;/, 'hud.css must define --mm-indicator-height: 16px');
  assert.match(css, /--mm-indicator-margin-top:\s*8px;/, 'hud.css must define --mm-indicator-margin-top: 8px');
  assert.match(css, /--mm-indicator-space:\s*calc\(/, 'hud.css must define --mm-indicator-space calculation');

  // Must bump grid-canvas up by subtracting indicator space
  assert.match(
    css,
    /height:\s*calc\(100% - var\(--mm-header-height\) - var\(--mm-grid-gap\) - var\(--mm-indicator-space\)\);/,
    'hud.css must bump grid-canvas up by subtracting --mm-indicator-space'
  );

  // Must not be a floating fixed pill
  assert.doesNotMatch(
    css,
    /\.screen-indicator\s*\{[^}]*position:\s*fixed/s,
    'screen-indicator must not be position: fixed'
  );
  assert.match(
    css,
    /\.screen-indicator\s*\{[^}]*background:\s*transparent;/s,
    'screen-indicator must define background: transparent'
  );
  assert.match(
    css,
    /\.screen-indicator\s*\{[^}]*border:\s*none;/s,
    'screen-indicator must define border: none'
  );

  // Must retain dot symbols with active pill
  assert.match(
    css,
    /\.screen-indicator-dot\.active::after\s*\{[^}]*width:\s*26px;/s,
    'screen-indicator-dot.active must define 26px wide active pill'
  );
});

test('carousel unmounts outgoing widget controllers before DOM replacement', async (t) => {
  function setupCarouselWithControllers() {
    const unmounted = [];
    const win = {
      location: { search: '' },
    };

    const doc = {
      createDocumentFragment: () => ({
        children: [],
        appendChild(child) { this.children.push(child); },
      }),
      createElement: (tag) => {
        const el = {
          tagName: tag.toUpperCase(),
          style: {},
          dataset: {},
          children: [],
          appendChild(child) { this.children.push(child); },
          querySelector(sel) {
            if (sel === '.widget-card') return this;
            return null;
          },
          replaceWith() {},
        };
        el.firstElementChild = el;
        return el;
      },
    };

    const fn = new Function('window', 'document', 'requestAnimationFrame', 'setTimeout', 'setInterval', 'clearInterval', 'clearTimeout', carouselCode);
    fn(win, doc, () => {}, setTimeout, setInterval, clearInterval, clearTimeout);
    const Carousel = win.MirrormereCarousel;

    win.MirrormereCalendarFamily = {
      unmount: (id) => { unmounted.push(['calendar', id]); },
    };
    win.MirrormereLiveView = {
      unmount: (id) => { unmounted.push(['liveview', id]); },
    };
    win.MirrormerePhotoCarousel = {
      unmount: (id) => { unmounted.push(['photo', id]); },
    };

    return { win, doc, Carousel, unmounted };
  }

  await t.test('handleScreenRotate invokes unmount on activeWidgets and canvas querySelectorAll items before clearing innerHTML', async () => {
    const { Carousel, unmounted } = setupCarouselWithControllers();

    const canvasElements = [
      { dataset: { widgetId: 'w-from-dom' } },
    ];
    let clearedBeforeUnmount = false;

    const canvas = {
      dataset: {},
      classList: { add() {}, remove() {} },
      appendChild() {},
      addEventListener() {},
      querySelectorAll(sel) {
        if (sel === '[data-widget-id]') return canvasElements;
        return [];
      },
      set innerHTML(val) {
        if (val === '') {
          if (unmounted.length === 0) {
            clearedBeforeUnmount = true;
          }
        }
      },
    };

    const c = new Carousel(canvas);
    c.activeWidgets = new Map([
      ['w-active', { element: {} }],
    ]);

    const origFetch = globalThis.fetch;
    globalThis.fetch = async () => ({
      ok: true,
      text: async () => '<div class="widget-card">New Card</div>',
    });

    try {
      await c.handleScreenRotate({
        widgets: [{ widget_id: 'w-incoming', origin: [0, 0], dimensions: [2, 1] }],
        current_screen: 0,
        total_screens: 1,
      });

      assert.equal(clearedBeforeUnmount, false, 'unmount must occur before innerHTML is cleared');
      assert.deepEqual(unmounted, [
        ['calendar', 'w-active'],
        ['liveview', 'w-active'],
        ['photo', 'w-active'],
        ['calendar', 'w-from-dom'],
        ['liveview', 'w-from-dom'],
        ['photo', 'w-from-dom'],
      ]);
    } finally {
      globalThis.fetch = origFetch;
    }
  });

  await t.test('handleWidgetUpdate invokes unmount on registered controllers before replacing node', async () => {
    const { Carousel, unmounted } = setupCarouselWithControllers();

    let replacedBeforeUnmount = false;
    const existingNode = {
      dataset: { widgetId: 'w-target' },
      style: { gridColumn: '1 / span 2', gridRow: '1 / span 1' },
      replaceWith(newNode) {
        if (unmounted.length === 0) {
          replacedBeforeUnmount = true;
        }
      },
    };

    const canvas = {
      dataset: {},
      classList: { add() {}, remove() {} },
      addEventListener() {},
      querySelector(sel) {
        if (sel === '[data-widget-id="w-target"]') return existingNode;
        return null;
      },
    };

    const c = new Carousel(canvas);
    c.activeWidgets = new Map([
      ['w-target', { element: existingNode }],
    ]);

    const origFetch = globalThis.fetch;
    globalThis.fetch = async () => ({
      ok: true,
      text: async () => '<div class="widget-card">Updated Card</div>',
    });

    try {
      await c.handleWidgetUpdate({ widget_id: 'w-target' });

      assert.equal(replacedBeforeUnmount, false, 'unmount must occur before replaceWith');
      assert.deepEqual(unmounted, [
        ['calendar', 'w-target'],
        ['liveview', 'w-target'],
        ['photo', 'w-target'],
      ]);
    } finally {
      globalThis.fetch = origFetch;
    }
  });
});

test('carousel dynamic task switcher and activeTaskWidgetId substitution', async (t) => {
  function setupTestEnv() {
    const createdElements = [];
    const doc = {
      createDocumentFragment: () => ({ children: [], appendChild() {} }),
      createElement: (tag) => {
        const el = {
          tagName: tag.toUpperCase(),
          children: [],
          style: {},
          dataset: {},
          listeners: {},
          appendChild(ch) { this.children.push(ch); },
          querySelector(sel) {
            if (sel === '.hearth-task-switcher') {
              return this.children.find(c => c.className === 'hearth-task-switcher');
            }
            if (sel === '.tasks-progress') return null;
            return null;
          },
          addEventListener(evt, fn) {
            this.listeners[evt] = fn;
          },
        };
        createdElements.push(el);
        return el;
      },
    };
    const win = { location: { search: '' } };
    const fn = new Function('window', 'document', 'requestAnimationFrame', 'setTimeout', 'setInterval', 'clearInterval', 'clearTimeout', carouselCode);
    fn(win, doc, () => {}, () => {}, () => {}, () => {}, () => {});
    const Carousel = win.MirrormereCarousel;
    const canvas = {
      dataset: {},
      classList: { add() {}, remove() {} },
      appendChild() {},
      addEventListener() {},
    };
    const c = new Carousel(canvas);
    return { c, doc, createdElements };
  }

  await t.test('injectTaskSwitcher parses taskTabs dataset and populates taskWidgetIds', () => {
    const { c } = setupTestEnv();
    assert.deepEqual(c.taskWidgetIds, []);

    const hdr = {
      className: 'tasks-header',
      children: [],
      appendChild(ch) { this.children.push(ch); },
      querySelector(sel) {
        if (sel === '.hearth-task-switcher') return this.children.find(ch => ch.className === 'hearth-task-switcher') || null;
        return null;
      },
    };

    const node = {
      dataset: {
        taskTabs: JSON.stringify([
          { id: 'tasks-personal', label: 'Personal' },
          { id: 'tasks-work', label: 'Work' },
        ]),
      },
      querySelector(sel) {
        if (sel === '.tasks-header') return hdr;
        return null;
      },
    };

    c.injectTaskSwitcher(node, 'tasks-personal');
    assert.deepEqual(c.taskWidgetIds, ['tasks-personal', 'tasks-work']);
    assert.equal(hdr.children.length, 1);
    const switcher = hdr.children[0];
    assert.equal(switcher.className, 'hearth-task-switcher');
    assert.equal(switcher.children.length, 2);
    assert.equal(switcher.children[0].textContent, 'Personal');
    assert.equal(switcher.children[1].textContent, 'Work');
  });

  await t.test('getWidgetsForScreen substitutes activeTaskWidgetId when set', () => {
    const { c } = setupTestEnv();
    c.screenLayouts.set(0, [
      { widget_id: 'weather', origin: [0, 0], dimensions: [2, 2] },
      { widget_id: 'tasks-personal', origin: [0, 2], dimensions: [2, 4] },
    ]);
    c.taskWidgetIds = ['tasks-personal', 'tasks-work'];

    // Before activeTaskWidgetId is set
    const widgetsBefore = c.getWidgetsForScreen(0);
    assert.deepEqual(widgetsBefore, [
      { widget_id: 'weather', origin: [0, 0], dimensions: [2, 2] },
      { widget_id: 'tasks-personal', origin: [0, 2], dimensions: [2, 4] },
    ]);

    // Set activeTaskWidgetId to 'tasks-work'
    c.activeTaskWidgetId = 'tasks-work';
    const widgetsAfter = c.getWidgetsForScreen(0);
    assert.deepEqual(widgetsAfter, [
      { widget_id: 'weather', origin: [0, 0], dimensions: [2, 2] },
      { widget_id: 'tasks-work', origin: [0, 2], dimensions: [2, 4] },
    ]);
  });

  await t.test('applyCachedScreen resolves any widget whose widget_id is in taskWidgetIds to activeTaskWidgetId', () => {
    const { c } = setupTestEnv();
    c.taskWidgetIds = ['tasks-personal', 'tasks-work'];
    c.activeTaskWidgetId = 'tasks-work';

    const nodePersonal = { style: {}, dataset: { widgetId: 'tasks-personal' }, querySelector: () => null };
    const nodeWork = { style: {}, dataset: { widgetId: 'tasks-work' }, querySelector: () => null };
    c.widgetNodeCache.set('tasks-personal', nodePersonal);
    c.widgetNodeCache.set('tasks-work', nodeWork);

    c.applyCachedScreen(0, [
      { widget_id: 'tasks-personal', origin: [0, 0], dimensions: [2, 2] },
    ]);

    assert.equal(c.activeWidgets.has('tasks-work'), true);
    assert.equal(c.activeWidgets.has('tasks-personal'), false);
    assert.equal(nodeWork.style.display, '');
    assert.equal(nodePersonal.style.display, 'none');
  });

  await t.test('switcher button click sets activeTaskWidgetId and calls applyCachedScreen with substituted widgets', async () => {
    const { c } = setupTestEnv();
    c.currentScreen = 0;
    c.screenLayouts.set(0, [
      { widget_id: 'tasks-personal', origin: [0, 0], dimensions: [2, 2] },
    ]);

    const hdr = {
      className: 'tasks-header',
      children: [],
      appendChild(ch) { this.children.push(ch); },
      querySelector(sel) {
        if (sel === '.hearth-task-switcher') return this.children.find(ch => ch.className === 'hearth-task-switcher') || null;
        return null;
      },
    };

    const node = {
      dataset: {
        taskSwitcher: JSON.stringify([
          { id: 'tasks-personal', label: 'Personal' },
          { id: 'tasks-work', label: 'Work' },
        ]),
      },
      querySelector(sel) {
        if (sel === '.tasks-header') return hdr;
        return null;
      },
    };

    c.injectTaskSwitcher(node, 'tasks-personal');
    const switcher = hdr.children[0];
    const workBtn = switcher.children[1];

    let appliedWidgets = null;
    c.applyCachedScreen = (screenIndex, widgets) => {
      appliedWidgets = widgets;
    };
    c.ensureWidgetsMounted = async () => {};

    // Click work button
    let stopped = false;
    workBtn.listeners.click({ stopPropagation: () => { stopped = true; } });

    assert.equal(stopped, true);
    assert.equal(c.activeTaskWidgetId, 'tasks-work');
    await new Promise(resolve => setTimeout(resolve, 10));
    assert.ok(appliedWidgets);
    assert.equal(appliedWidgets[0].widget_id, 'tasks-work');
  });
});



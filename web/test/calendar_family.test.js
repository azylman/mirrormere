const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const scriptCode = fs.readFileSync(path.join(__dirname, '../static/js/calendar_family.js'), 'utf8');

function setupDOMMock() {
  global.window = {};
  const fn = new Function('window', 'document', 'setTimeout', 'clearTimeout', scriptCode);
  fn(global.window, {}, global.setTimeout, global.clearTimeout);
}

function createMockElement(options = {}) {
  const widgetId = options.widgetId || 'family-cal-test';
  const defaultView = options.defaultView || 'week';
  const activeView = options.activeView || defaultView;
  const data = options.data || null;

  const classList = new Set(['widget-card', 'widget-calendar-family', `cf-view-${activeView}`]);

  const btnListeners = {};
  function makeButton(view) {
    const btnClasses = new Set(['cf-view-btn']);
    if (view === activeView) {
      btnClasses.add('active');
    }
    const attributes = {
      'role': 'tab',
      'aria-selected': view === activeView ? 'true' : 'false',
    };
    const listeners = {};
    return {
      dataset: { view },
      classList: {
        add: (c) => btnClasses.add(c),
        remove: (c) => btnClasses.delete(c),
        contains: (c) => btnClasses.has(c),
      },
      setAttribute: (k, v) => { attributes[k] = String(v); },
      getAttribute: (k) => attributes[k] || null,
      addEventListener: (evt, handler) => {
        if (!listeners[evt]) listeners[evt] = [];
        listeners[evt].push(handler);
      },
      removeEventListener: (evt, handler) => {
        if (listeners[evt]) {
          listeners[evt] = listeners[evt].filter(h => h !== handler);
        }
      },
      click() {
        if (listeners['click']) {
          for (const handler of listeners['click']) {
            handler({ currentTarget: this, target: this });
          }
        }
      }
    };
  }

  const buttons = [makeButton('day'), makeButton('week'), makeButton('month')];

  const panels = {
    day: { style: { display: activeView === 'day' ? 'flex' : 'none' }, classList: new Set(['cf-view-panel', 'cf-view-day']) },
    week: { style: { display: activeView === 'week' ? 'flex' : 'none' }, classList: new Set(['cf-view-panel', 'cf-view-week']) },
    month: { style: { display: activeView === 'month' ? 'flex' : 'none' }, classList: new Set(['cf-view-panel', 'cf-view-month']) },
  };

  const labels = {
    day: { style: { display: activeView === 'day' ? '' : 'none' }, classList: new Set(['cf-range-label', 'cf-label-day']) },
    week: { style: { display: activeView === 'week' ? '' : 'none' }, classList: new Set(['cf-range-label', 'cf-label-week']) },
    month: { style: { display: activeView === 'month' ? '' : 'none' }, classList: new Set(['cf-range-label', 'cf-label-month']) },
  };

  const todayBadge = {
    style: { display: activeView === 'day' ? '' : 'none' },
    classList: new Set(['cf-today-badge', 'cf-today-day'])
  };

  const dataScript = data !== null ? {
    textContent: typeof data === 'string' ? data : JSON.stringify(data),
  } : null;

  const element = {
    dataset: {
      widgetId,
      defaultView,
      activeView,
    },
    classList: {
      add: (...classes) => classes.forEach(c => classList.add(c)),
      remove: (...classes) => classes.forEach(c => classList.delete(c)),
      contains: (c) => classList.has(c),
    },
    querySelector(selector) {
      if (selector === '.calendar-family-data') return dataScript;
      if (selector === '.cf-view-panel.cf-view-day' || selector === '.cf-view-day') return panels.day;
      if (selector === '.cf-view-panel.cf-view-week' || selector === '.cf-view-week') return panels.week;
      if (selector === '.cf-view-panel.cf-view-month' || selector === '.cf-view-month') return panels.month;
      if (selector === '.cf-range-label.cf-label-day') return labels.day;
      if (selector === '.cf-range-label.cf-label-week') return labels.week;
      if (selector === '.cf-range-label.cf-label-month') return labels.month;
      if (selector === '.cf-today-badge.cf-today-day' || selector === '.cf-today-badge') return todayBadge;
      return null;
    },
    querySelectorAll(selector) {
      if (selector === '.cf-view-btn') return buttons;
      return [];
    },
    _buttons: buttons,
    _panels: panels,
    _labels: labels,
    _todayBadge: todayBadge,
  };

  return element;
}

test('Calendar Family Client Controller', async (t) => {
  setupDOMMock();

  await t.test('mounts instance and registers globally in window.MirrormereCalendarFamily', () => {
    const el = createMockElement({ widgetId: 'test-cal-1' });
    const inst = global.window.MirrormereCalendarFamily.mount(el);

    assert.ok(inst, 'mount should return instance');
    assert.equal(inst.widgetId, 'test-cal-1');
    assert.equal(global.window.MirrormereCalendarFamily.getInstance('test-cal-1'), inst);

    global.window.MirrormereCalendarFamily.unmount('test-cal-1');
    assert.equal(global.window.MirrormereCalendarFamily.getInstance('test-cal-1'), null);
  });

  await t.test('remounting with same widgetId unmounts previous instance', () => {
    let destroyed = false;
    const el1 = createMockElement({ widgetId: 'test-cal-dup' });
    const inst1 = global.window.MirrormereCalendarFamily.mount(el1);
    inst1.destroy = () => { destroyed = true; };

    const el2 = createMockElement({ widgetId: 'test-cal-dup' });
    const inst2 = global.window.MirrormereCalendarFamily.mount(el2);

    assert.equal(destroyed, true, 'previous instance should be destroyed');
    assert.equal(global.window.MirrormereCalendarFamily.getInstance('test-cal-dup'), inst2);

    global.window.MirrormereCalendarFamily.unmount('test-cal-dup');
  });

  await t.test('resolves defaultView from dataset and config', () => {
    // 1. Explicit default_view: day
    const elDay = createMockElement({ defaultView: 'day' });
    const instDay = global.window.MirrormereCalendarFamily.mount(elDay);
    assert.equal(instDay.activeView, 'day');
    global.window.MirrormereCalendarFamily.unmount(elDay.dataset.widgetId);

    // 2. Explicit default_view: month
    const elMonth = createMockElement({ defaultView: 'month' });
    const instMonth = global.window.MirrormereCalendarFamily.mount(elMonth);
    assert.equal(instMonth.activeView, 'month');
    global.window.MirrormereCalendarFamily.unmount(elMonth.dataset.widgetId);

    // 3. Fallback when invalid or missing
    const elFallback = createMockElement({ defaultView: 'unknown' });
    const instFallback = global.window.MirrormereCalendarFamily.mount(elFallback);
    assert.equal(instFallback.activeView, 'week');
    global.window.MirrormereCalendarFamily.unmount(elFallback.dataset.widgetId);
  });

  await t.test('switches view on 3-way view switcher button clicks', () => {
    let notifiedView = null;
    const el = createMockElement({
      widgetId: 'switcher-cal',
      defaultView: 'week',
    });

    const inst = global.window.MirrormereCalendarFamily.mount(el, {
      onViewChange: (view) => { notifiedView = view; },
    });

    assert.equal(inst.activeView, 'week');
    assert.equal(el._buttons[1].classList.contains('active'), true);
    assert.equal(el._buttons[1].getAttribute('aria-selected'), 'true');
    assert.equal(el._buttons[0].classList.contains('active'), false);
    assert.equal(el._buttons[0].getAttribute('aria-selected'), 'false');

    // Click 'Day' button (index 0)
    el._buttons[0].click();

    assert.equal(inst.activeView, 'day');
    assert.equal(el.dataset.activeView, 'day');
    assert.equal(el.classList.contains('cf-view-day'), true);
    assert.equal(el.classList.contains('cf-view-week'), false);
    assert.equal(el._buttons[0].classList.contains('active'), true);
    assert.equal(el._buttons[0].getAttribute('aria-selected'), 'true');
    assert.equal(el._buttons[1].classList.contains('active'), false);
    assert.equal(el._buttons[1].getAttribute('aria-selected'), 'false');
    assert.equal(notifiedView, 'day');

    // Check panels and labels display
    assert.equal(el._panels.day.style.display, 'flex');
    assert.equal(el._panels.week.style.display, 'none');
    assert.equal(el._panels.month.style.display, 'none');
    assert.equal(el._labels.day.style.display, '');
    assert.equal(el._labels.week.style.display, 'none');
    assert.equal(el._todayBadge.style.display, '');

    // Click 'Month' button (index 2)
    el._buttons[2].click();

    assert.equal(inst.activeView, 'month');
    assert.equal(el.dataset.activeView, 'month');
    assert.equal(el.classList.contains('cf-view-month'), true);
    assert.equal(el.classList.contains('cf-view-day'), false);
    assert.equal(el._buttons[2].classList.contains('active'), true);
    assert.equal(el._buttons[2].getAttribute('aria-selected'), 'true');
    assert.equal(el._buttons[0].classList.contains('active'), false);
    assert.equal(el._buttons[0].getAttribute('aria-selected'), 'false');
    assert.equal(notifiedView, 'month');

    // Panels and labels display in month view
    assert.equal(el._panels.month.style.display, 'flex');
    assert.equal(el._panels.day.style.display, 'none');
    assert.equal(el._labels.month.style.display, '');
    assert.equal(el._labels.day.style.display, 'none');
    assert.equal(el._todayBadge.style.display, 'none');

    // Ignore invalid switchView input
    inst.switchView('invalid-view');
    assert.equal(inst.activeView, 'month');

    global.window.MirrormereCalendarFamily.unmount('switcher-cal');
  });

  await t.test('handles missing or malformed JSON data gracefully', () => {
    // 1. Missing data element
    const elNoData = createMockElement({ data: null });
    const instNoData = global.window.MirrormereCalendarFamily.mount(elNoData);
    assert.ok(instNoData);
    assert.deepEqual(instNoData.data, {});
    global.window.MirrormereCalendarFamily.unmount(elNoData.dataset.widgetId);

    // 2. Malformed JSON
    const elBadJSON = createMockElement({ data: '{bad json' });
    const instBadJSON = global.window.MirrormereCalendarFamily.mount(elBadJSON);
    assert.ok(instBadJSON);
    assert.deepEqual(instBadJSON.data, {});
    global.window.MirrormereCalendarFamily.unmount(elBadJSON.dataset.widgetId);

    // 3. Double-encoded string JSON
    const elDoubleEncoded = createMockElement({ data: JSON.stringify(JSON.stringify({ range_label: 'Oct 2026' })) });
    const instDouble = global.window.MirrormereCalendarFamily.mount(elDoubleEncoded);
    assert.ok(instDouble);
    assert.equal(instDouble.data.range_label, 'Oct 2026');
    global.window.MirrormereCalendarFamily.unmount(elDoubleEncoded.dataset.widgetId);
  });

  await t.test('safe no-ops on null element and unknown unmount', () => {
    const instNull = global.window.MirrormereCalendarFamily.mount(null);
    assert.equal(instNull, null);

    assert.doesNotThrow(() => {
      global.window.MirrormereCalendarFamily.unmount('nonexistent');
    });
  });
});

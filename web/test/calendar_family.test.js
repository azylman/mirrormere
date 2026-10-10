const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const scriptCode = fs.readFileSync(path.join(__dirname, '../static/js/calendar_family.js'), 'utf8');

function parseHTMLFragment(html, rootNode) {
  if (!html || typeof html !== 'string') return;
  if (!html.includes('<')) {
    rootNode._textContent = html;
    return;
  }

  const tagRegex = /<!--[\s\S]*?-->|<(\/)?([a-zA-Z0-9-]+)([^>]*)>|([^<]+)/g;
  const stack = [rootNode];
  let match;

  while ((match = tagRegex.exec(html)) !== null) {
    const [fullMatch, isClosing, tagName, attrString, textContent] = match;

    if (fullMatch.startsWith('<!--')) {
      continue;
    }

    if (textContent !== undefined) {
      const text = textContent.trim();
      if (text) {
        const cur = stack[stack.length - 1];
        if (cur) {
          cur._textContent = (cur._textContent ? cur._textContent + ' ' : '') + text;
        }
      }
      continue;
    }

    if (isClosing) {
      const lower = tagName.toLowerCase();
      for (let i = stack.length - 1; i > 0; i--) {
        if (stack[i].tagName.toLowerCase() === lower) {
          stack.length = i;
          break;
        }
      }
      continue;
    }

    const lower = tagName.toLowerCase();
    const attrs = {};
    if (attrString) {
      const attrRegex = /([a-zA-Z0-9_-]+)(?:=(?:"([^"]*)"|'([^']*)'|([^\s>]+)))?/g;
      let aMatch;
      while ((aMatch = attrRegex.exec(attrString)) !== null) {
        const name = aMatch[1];
        const val = aMatch[2] !== undefined ? aMatch[2] : (aMatch[3] !== undefined ? aMatch[3] : (aMatch[4] !== undefined ? aMatch[4] : ''));
        attrs[name] = val;
      }
    }

    const newNode = createMockDOMNode(lower, attrs);
    if (attrs.style) {
      attrs.style.split(';').forEach(rule => {
        const idx = rule.indexOf(':');
        if (idx !== -1) {
          const k = rule.slice(0, idx).trim();
          const v = rule.slice(idx + 1).trim();
          if (k && v) {
            const camel = k.replace(/-([a-z])/g, (_, c) => c.toUpperCase());
            newNode.style[camel] = v;
          }
        }
      });
    }
    for (const [k, v] of Object.entries(attrs)) {
      if (k.startsWith('data-')) {
        const camel = k.slice(5).replace(/-([a-z])/g, (_, c) => c.toUpperCase());
        newNode.dataset[camel] = v;
      }
      if (k === 'class') {
        newNode.className = v;
      }
    }

    const cur = stack[stack.length - 1];
    if (cur) {
      cur.appendChild(newNode);
    }

    const isSelfClosing = attrString.trim().endsWith('/') || ['br', 'hr', 'img', 'input', 'meta', 'link'].includes(lower);
    if (!isSelfClosing) {
      if (lower === 'script' || lower === 'style') {
        const closeTag = `</${lower}>`;
        const restIndex = html.indexOf(closeTag, tagRegex.lastIndex);
        if (restIndex !== -1) {
          const rawContent = html.slice(tagRegex.lastIndex, restIndex);
          newNode._textContent = rawContent;
          newNode._innerHTML = rawContent;
          tagRegex.lastIndex = restIndex + closeTag.length;
        }
      } else {
        stack.push(newNode);
      }
    }
  }
}

function createMockDOMNode(tag = 'div', attributes = {}) {
  const classList = new Set();
  const children = [];
  const listeners = {};
  const style = {};
  const dataset = {};
  const attrs = { ...attributes };

  if (attributes.className) {
    attributes.className.split(/\s+/).filter(Boolean).forEach(c => classList.add(c));
  }
  for (const [k, v] of Object.entries(attributes)) {
    if (k.startsWith('data-')) {
      const camel = k.slice(5).replace(/-([a-z])/g, (_, c) => c.toUpperCase());
      dataset[camel] = v;
    }
  }

  const node = {
    tagName: tag.toUpperCase(),
    style,
    dataset,
    children,
    parentNode: null,
    _listeners: listeners,
    get className() {
      return Array.from(classList).join(' ');
    },
    set className(val) {
      classList.clear();
      if (val) val.split(/\s+/).filter(Boolean).forEach(c => classList.add(c));
    },
    classList: {
      add: (...classes) => classes.forEach(c => classList.add(c)),
      remove: (...classes) => classes.forEach(c => classList.delete(c)),
      contains: (c) => classList.has(c),
    },
    _textContent: '',
    get textContent() {
      return this._textContent;
    },
    set textContent(val) {
      this._textContent = String(val);
    },
    _innerHTML: '',
    get innerHTML() {
      return this._innerHTML || this._textContent;
    },
    set innerHTML(val) {
      this._innerHTML = String(val);
      this._textContent = String(val);
      while (children.length > 0) {
        const c = children.pop();
        if (c) c.parentNode = null;
      }
      parseHTMLFragment(String(val), node);
    },
    setAttribute(k, v) {
      attrs[k] = String(v);
    },
    getAttribute(k) {
      return attrs[k] !== undefined ? attrs[k] : null;
    },
    removeAttribute(k) {
      delete attrs[k];
    },
    appendChild(child) {
      if (!child) return child;
      if (child.parentNode) {
        child.parentNode.removeChild(child);
      }
      child.parentNode = node;
      children.push(child);
      return child;
    },
    removeChild(child) {
      const idx = children.indexOf(child);
      if (idx !== -1) {
        children.splice(idx, 1);
        child.parentNode = null;
      }
      return child;
    },
    replaceChild(newNode, oldNode) {
      if (!newNode || !oldNode) return null;
      const idx = children.indexOf(oldNode);
      if (idx !== -1) {
        if (newNode.parentNode) {
          newNode.parentNode.removeChild(newNode);
        }
        newNode.parentNode = node;
        oldNode.parentNode = null;
        children[idx] = newNode;
        return oldNode;
      }
      return null;
    },
    replaceWith(newNode) {
      if (this.parentNode && typeof this.parentNode.replaceChild === 'function') {
        this.parentNode.replaceChild(newNode, this);
      }
    },
    remove() {
      if (this.parentNode) {
        this.parentNode.removeChild(this);
      }
    },
    contains(target) {
      if (target === node) return true;
      for (const child of children) {
        if (child === target || (child.contains && child.contains(target))) {
          return true;
        }
      }
      return false;
    },
    closest(selector) {
      let cur = this;
      while (cur) {
        if (cur.matches && cur.matches(selector)) {
          return cur;
        }
        cur = cur.parentNode;
      }
      return null;
    },
    matches(selector) {
      if (!selector) return false;
      const selectors = selector.split(',').map(s => s.trim());
      for (const sel of selectors) {
        if (!sel) continue;
        if (sel.startsWith('[') && sel.endsWith(']')) {
          const inner = sel.slice(1, -1);
          if (inner.includes('=')) {
            let [attrName, attrVal] = inner.split('=');
            if (attrVal.startsWith('"') && attrVal.endsWith('"')) attrVal = attrVal.slice(1, -1);
            if (attrVal.startsWith("'") && attrVal.endsWith("'")) attrVal = attrVal.slice(1, -1);
            if (attrName === 'data-event-id') return this.dataset && this.dataset.eventId === attrVal;
            if (attrName.startsWith('data-')) {
              const camel = attrName.slice(5).replace(/-([a-z])/g, (_, c) => c.toUpperCase());
              return this.dataset && this.dataset[camel] === attrVal;
            }
            return attrs[attrName] === attrVal;
          }
          if (inner === 'data-event-id' && this.dataset && this.dataset.eventId) return true;
          if (inner.startsWith('data-')) {
            const camel = inner.slice(5).replace(/-([a-z])/g, (_, c) => c.toUpperCase());
            if (this.dataset && this.dataset[camel] !== undefined) return true;
          }
          if (attrs[inner] !== undefined) return true;
        } else if (sel.includes('.')) {
          const dotIdx = sel.indexOf('.');
          const tagPart = sel.slice(0, dotIdx);
          const classPart = sel.slice(dotIdx);
          if (tagPart && tagPart.toLowerCase() !== this.tagName.toLowerCase()) {
            continue;
          }
          const parts = classPart.split('.').filter(Boolean);
          if (parts.length > 0 && parts.every(c => classList.has(c))) {
            return true;
          }
        } else if (this.tagName.toLowerCase() === sel.toLowerCase()) {
          return true;
        }
      }
      return false;
    },
    querySelector(selector) {
      for (const child of children) {
        if (child.matches && child.matches(selector)) {
          return child;
        }
        if (child.querySelector) {
          const res = child.querySelector(selector);
          if (res) return res;
        }
      }
      return null;
    },
    querySelectorAll(selector) {
      const found = [];
      function walk(n) {
        for (const c of n.children) {
          if (c.matches && c.matches(selector)) {
            found.push(c);
          }
          if (c.children && c.children.length > 0) {
            walk(c);
          }
        }
      }
      walk(this);
      return found;
    },
    addEventListener(evt, handler, _options) {
      if (!listeners[evt]) listeners[evt] = [];
      listeners[evt].push(handler);
    },
    removeEventListener(evt, handler) {
      if (listeners[evt]) {
        listeners[evt] = listeners[evt].filter(h => h !== handler);
      }
    },
    dispatchEvent(evt) {
      let cur = this;
      while (cur) {
        const handlers = cur._listeners[evt.type] ? [...cur._listeners[evt.type]] : [];
        evt.currentTarget = cur;
        for (const h of handlers) {
          h(evt);
        }
        if (evt._stopped) break;
        cur = cur.parentNode;
      }
    },
    click() {
      const evt = {
        type: 'click',
        target: this,
        currentTarget: this,
        _stopped: false,
        preventDefault: () => {},
        stopPropagation() { this._stopped = true; },
      };
      this.dispatchEvent(evt);
    }
  };

  return node;
}

function setupDOMMock() {
  const documentListeners = {};
  const mockDoc = {
    createElement: (tag) => createMockDOMNode(tag),
    addEventListener: (evt, handler) => {
      if (!documentListeners[evt]) documentListeners[evt] = [];
      documentListeners[evt].push(handler);
    },
    removeEventListener: (evt, handler) => {
      if (documentListeners[evt]) {
        documentListeners[evt] = documentListeners[evt].filter(h => h !== handler);
      }
    },
    _dispatchKeydown: (key) => {
      const handlers = documentListeners['keydown'] ? [...documentListeners['keydown']] : [];
      for (const h of handlers) {
        h({ key, preventDefault: () => {} });
      }
    }
  };

  global.document = mockDoc;
  global.window = {
    document: mockDoc,
    MirrormereCarousel: {
      pauseCount: 0,
      resumeCount: 0,
      pause() { this.pauseCount++; },
      resume() { this.resumeCount++; },
      reset() { this.pauseCount = 0; this.resumeCount = 0; },
    },
  };

  const fn = new Function('window', 'document', 'setTimeout', 'clearTimeout', scriptCode);
  fn(global.window, mockDoc, global.setTimeout, global.clearTimeout);
}

function createMockElement(options = {}) {
  const widgetId = options.widgetId || 'family-cal-test';
  const defaultView = options.defaultView || 'week';
  const activeView = options.activeView || defaultView;
  const data = options.data !== undefined ? options.data : {
    config: { default_view: defaultView },
    days: [
      {
        date: '2026-10-10',
        all_day: [
          {
            id: 'ev-allday-1',
            title: 'School Fall Break',
            all_day: true,
            start: '',
            end: '',
            location: 'Oakland High',
            description: 'Annual fall semester vacation period for students and staff.',
            owners: ['Alex'],
            initials: ['A'],
            color: '#E11D48',
            pattern: 'solid',
          }
        ],
        timed: [
          {
            id: 'ev-timed-1',
            title: 'Soccer Practice',
            all_day: false,
            start: '16:00',
            end: '17:30',
            location: 'Montclair Park',
            description: 'Drills, scrimmage and conditioning. Bring water bottle and shin guards.',
            owners: ['Alex', 'Charlie'],
            initials: ['A', 'C'],
            colors: ['#E11D48', '#2563EB'],
            patterns: ['solid', 'hatch'],
            color: '#E11D48',
            pattern: 'solid',
          }
        ],
        events: [
          {
            id: 'ev-month-1',
            title: 'Dentist Checkup',
            all_day: false,
            start: '09:00',
            end: '10:00',
            location: 'Grand Ave Dental',
            description: 'Routine cleaning and checkup.',
            owners: ['Alex'],
            initials: ['A'],
            color: '#E11D48',
            pattern: 'solid',
          }
        ]
      }
    ]
  };

  const element = createMockDOMNode('div', {
    className: `widget-card widget-calendar-family cf-view-${activeView}`
  });
  element.dataset.widgetId = widgetId;
  element.dataset.defaultView = defaultView;
  element.dataset.activeView = activeView;

  // Buttons
  const btnDay = createMockDOMNode('button', { className: 'cf-view-btn' + (activeView === 'day' ? ' active' : '') });
  btnDay.dataset.view = 'day';
  const btnWeek = createMockDOMNode('button', { className: 'cf-view-btn' + (activeView === 'week' ? ' active' : '') });
  btnWeek.dataset.view = 'week';
  const btnMonth = createMockDOMNode('button', { className: 'cf-view-btn' + (activeView === 'month' ? ' active' : '') });
  btnMonth.dataset.view = 'month';

  element.appendChild(btnDay);
  element.appendChild(btnWeek);
  element.appendChild(btnMonth);

  // Nav buttons
  const btnPrev = createMockDOMNode('button', { className: 'cf-nav-btn cf-prev-btn' });
  const btnNext = createMockDOMNode('button', { className: 'cf-nav-btn cf-next-btn' });
  element.appendChild(btnPrev);
  element.appendChild(btnNext);

  // Panels
  const panelDay = createMockDOMNode('div', { className: 'cf-view-panel cf-view-day' });
  panelDay.style.display = activeView === 'day' ? 'flex' : 'none';
  const panelWeek = createMockDOMNode('div', { className: 'cf-view-panel cf-view-week' });
  panelWeek.style.display = activeView === 'week' ? 'flex' : 'none';
  const panelMonth = createMockDOMNode('div', { className: 'cf-view-panel cf-view-month' });
  panelMonth.style.display = activeView === 'month' ? 'flex' : 'none';

  element.appendChild(panelDay);
  element.appendChild(panelWeek);
  element.appendChild(panelMonth);

  // Labels
  const lblDay = createMockDOMNode('span', { className: 'cf-range-label cf-label-day' });
  const lblWeek = createMockDOMNode('span', { className: 'cf-range-label cf-label-week' });
  const lblMonth = createMockDOMNode('span', { className: 'cf-range-label cf-label-month' });
  element.appendChild(lblDay);
  element.appendChild(lblWeek);
  element.appendChild(lblMonth);

  // Today Badge
  const todayBadge = createMockDOMNode('span', { className: 'cf-today-badge cf-today-day' });
  element.appendChild(todayBadge);

  // Data script
  if (data !== null) {
    const dataScript = createMockDOMNode('script', { className: 'calendar-family-data' });
    dataScript.textContent = typeof data === 'string' ? data : JSON.stringify(data);
    element.appendChild(dataScript);
  }

  // Event elements inside panelWeek
  const evAllDay = createMockDOMNode('div', { className: 'cf-event cf-allday cf-event-clickable' });
  evAllDay.dataset.eventId = 'ev-allday-1';
  panelWeek.appendChild(evAllDay);

  const evTimed = createMockDOMNode('div', { className: 'cf-event cf-timed cf-event-clickable' });
  evTimed.dataset.eventId = 'ev-timed-1';
  panelWeek.appendChild(evTimed);

  // Event element inside panelMonth
  const monthDay = createMockDOMNode('div', { className: 'cf-month-day cf-has-events' });
  monthDay.dataset.date = '2026-10-10';
  const monthTitle = createMockDOMNode('div', { className: 'cf-month-event-title cf-event-clickable' });
  monthTitle.dataset.eventId = 'ev-month-1';
  monthDay.appendChild(monthTitle);
  panelMonth.appendChild(monthDay);

  element._buttons = [btnDay, btnWeek, btnMonth];
  element._btnPrev = btnPrev;
  element._btnNext = btnNext;
  element._navButtons = { prev: btnPrev, next: btnNext };
  element._panels = { day: panelDay, week: panelWeek, month: panelMonth };
  element._labels = { day: lblDay, week: lblWeek, month: lblMonth };
  element._todayBadge = todayBadge;
  element._events = { allDay: evAllDay, timed: evTimed, month: monthTitle, monthDay };

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
    const elDay = createMockElement({ defaultView: 'day' });
    const instDay = global.window.MirrormereCalendarFamily.mount(elDay);
    assert.equal(instDay.activeView, 'day');
    global.window.MirrormereCalendarFamily.unmount(elDay.dataset.widgetId);

    const elMonth = createMockElement({ defaultView: 'month' });
    const instMonth = global.window.MirrormereCalendarFamily.mount(elMonth);
    assert.equal(instMonth.activeView, 'month');
    global.window.MirrormereCalendarFamily.unmount(elMonth.dataset.widgetId);

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

    // Panels and labels display in day view
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
    const elNoData = createMockElement({ data: null });
    const instNoData = global.window.MirrormereCalendarFamily.mount(elNoData);
    assert.ok(instNoData);
    assert.deepEqual(instNoData.data, {});
    global.window.MirrormereCalendarFamily.unmount(elNoData.dataset.widgetId);

    const elBadJSON = createMockElement({ data: '{bad json' });
    const instBadJSON = global.window.MirrormereCalendarFamily.mount(elBadJSON);
    assert.ok(instBadJSON);
    assert.deepEqual(instBadJSON.data, {});
    global.window.MirrormereCalendarFamily.unmount(elBadJSON.dataset.widgetId);

    const elDoubleEncoded = createMockElement({ data: JSON.stringify(JSON.stringify({ range_label: 'Oct 2026' })) });
    const instDoubleEncoded = global.window.MirrormereCalendarFamily.mount(elDoubleEncoded);
    assert.ok(instDoubleEncoded);
    assert.deepEqual(instDoubleEncoded.data, { range_label: 'Oct 2026' });
    global.window.MirrormereCalendarFamily.unmount(elDoubleEncoded.dataset.widgetId);
  });

  await t.test('safe no-ops on null element and unknown unmount', () => {
    assert.equal(global.window.MirrormereCalendarFamily.mount(null), null);
    assert.doesNotThrow(() => {
      global.window.MirrormereCalendarFamily.unmount('non-existent-widget');
    });
  });

  // Chunk 7: Modal, Carousel, and Inactivity Tests
  await t.test('tapping a timed event opens overlay detail modal card with formatted content', () => {
    global.window.MirrormereCarousel.reset();
    const el = createMockElement({ widgetId: 'modal-timed-cal' });
    let modalOpenedEv = null;
    const inst = global.window.MirrormereCalendarFamily.mount(el, {
      onModalOpen: (ev) => { modalOpenedEv = ev; },
    });

    assert.equal(inst.activeModal, null);
    assert.equal(global.window.MirrormereCarousel.pauseCount, 0);

    // Tap timed event
    el._events.timed.click();

    assert.ok(inst.activeModal, 'modal overlay should be created');
    assert.equal(el.querySelector('.cf-modal-overlay'), inst.activeModal);
    assert.equal(global.window.MirrormereCarousel.pauseCount, 1, 'opening modal should pause carousel');
    assert.equal(modalOpenedEv.id, 'ev-timed-1');

    // Inspect modal card content
    const card = inst.activeModal.querySelector('.cf-modal-card');
    assert.ok(card, 'modal card should exist');

    const titleEl = card.querySelector('.cf-modal-title');
    assert.equal(titleEl.textContent, 'Soccer Practice');

    const timeEl = card.querySelector('.cf-modal-time');
    assert.ok(timeEl);
    assert.ok(timeEl.textContent.includes('16:00 – 17:30'));

    const locEl = card.querySelector('.cf-modal-location');
    assert.ok(locEl);
    assert.ok(locEl.textContent.includes('Montclair Park'));

    const ownersEl = card.querySelector('.cf-modal-owners');
    assert.ok(ownersEl);
    const chips = ownersEl.querySelectorAll('.cf-modal-owner-chip');
    assert.equal(chips.length, 2, 'should have 2 owner chips for Alex and Charlie');
    assert.equal(chips[0].querySelector('.cf-modal-owner-name').textContent, 'Alex');
    assert.equal(chips[1].querySelector('.cf-modal-owner-name').textContent, 'Charlie');

    const descEl = card.querySelector('.cf-modal-description');
    assert.ok(descEl);
    assert.ok(descEl.textContent.includes('Drills, scrimmage and conditioning'));

    // Close button dismisses modal and resumes carousel
    const closeBtn = card.querySelector('.cf-modal-close');
    assert.ok(closeBtn);
    closeBtn.click();

    assert.equal(inst.activeModal, null);
    assert.equal(el.querySelector('.cf-modal-overlay'), null);
    assert.equal(global.window.MirrormereCarousel.resumeCount, 1, 'closing modal should resume carousel');

    global.window.MirrormereCalendarFamily.unmount('modal-timed-cal');
  });

  await t.test('tapping an all-day event formats time as "All Day"', () => {
    global.window.MirrormereCarousel.reset();
    const el = createMockElement({ widgetId: 'modal-allday-cal' });
    const inst = global.window.MirrormereCalendarFamily.mount(el);

    el._events.allDay.click();

    assert.ok(inst.activeModal);
    const card = inst.activeModal.querySelector('.cf-modal-card');
    const timeEl = card.querySelector('.cf-modal-time');
    assert.ok(timeEl);
    assert.ok(timeEl.textContent.includes('All Day'), 'should display "All Day" for all day events');

    // Dismiss by tapping overlay backdrop (target === overlay)
    const overlay = inst.activeModal;
    overlay.dispatchEvent({
      type: 'click',
      target: overlay,
      currentTarget: overlay,
    });

    assert.equal(inst.activeModal, null);
    assert.equal(global.window.MirrormereCarousel.resumeCount, 1);

    global.window.MirrormereCalendarFamily.unmount('modal-allday-cal');
  });

  await t.test('tapping inside modal card does NOT dismiss modal', () => {
    const el = createMockElement({ widgetId: 'modal-card-click' });
    const inst = global.window.MirrormereCalendarFamily.mount(el);

    el._events.timed.click();
    assert.ok(inst.activeModal);

    const card = inst.activeModal.querySelector('.cf-modal-card');
    // Click on card element
    card.click();
    assert.ok(inst.activeModal, 'modal should remain open on card click');

    // Dismiss via escape key
    global.document._dispatchKeydown('Escape');
    assert.equal(inst.activeModal, null, 'escape should dismiss modal');

    global.window.MirrormereCalendarFamily.unmount('modal-card-click');
  });

  await t.test('truncates event description longer than 280 characters', () => {
    const longDesc = 'A'.repeat(320);
    const el = createMockElement({
      widgetId: 'modal-trunc-cal',
      data: {
        days: [{
          all_day: [{
            id: 'ev-long',
            title: 'Long Event',
            description: longDesc,
          }]
        }]
      }
    });
    const inst = global.window.MirrormereCalendarFamily.mount(el);
    inst.openModal(inst.eventIndex.get('ev-long'));

    const descEl = inst.activeModal.querySelector('.cf-modal-description');
    assert.ok(descEl);
    assert.equal(descEl.textContent.length, 280);
    assert.ok(descEl.textContent.endsWith('...'));

    inst.closeModal();
    global.window.MirrormereCalendarFamily.unmount('modal-trunc-cal');
  });

  await t.test('opening a new modal while one is open replaces it without extra carousel resume', () => {
    global.window.MirrormereCarousel.reset();
    const el = createMockElement({ widgetId: 'modal-replace-cal' });
    const inst = global.window.MirrormereCalendarFamily.mount(el);

    el._events.allDay.click();
    assert.equal(global.window.MirrormereCarousel.pauseCount, 1);
    assert.equal(global.window.MirrormereCarousel.resumeCount, 0);

    // Click second event
    el._events.timed.click();
    assert.equal(global.window.MirrormereCarousel.pauseCount, 2);
    // resumeCount should still be 0 because the first modal was replaced, not dismissed to idle
    assert.equal(global.window.MirrormereCarousel.resumeCount, 0);

    inst.closeModal();
    assert.equal(global.window.MirrormereCarousel.resumeCount, 1);

    global.window.MirrormereCalendarFamily.unmount('modal-replace-cal');
  });

  await t.test('inactivity reset returns active view to default_view and closes modal after timeout', async () => {
    const el = createMockElement({
      widgetId: 'inactivity-cal',
      defaultView: 'week',
    });

    let resetFired = false;
    // Set a very short inactivity timeout (40ms) for fast unit test
    const inst = global.window.MirrormereCalendarFamily.mount(el, {
      inactivityTimeout: 40,
      onInactivityReset: () => { resetFired = true; },
    });

    // 1. Switch to 'day' view
    inst.switchView('day');
    assert.equal(inst.activeView, 'day');

    // 2. Open an event modal
    el._events.timed.click();
    assert.ok(inst.activeModal);

    // 3. Wait for inactivity timeout (60ms > 40ms)
    await new Promise(r => setTimeout(r, 60));

    assert.equal(resetFired, true, 'onInactivityReset callback should be called');
    assert.equal(inst.activeView, 'week', 'activeView should reset to defaultView (week)');
    assert.equal(inst.activeModal, null, 'open modal should be dismissed on inactivity timeout');

    global.window.MirrormereCalendarFamily.unmount('inactivity-cal');
  });

  await t.test('user activity touches reset the inactivity timer', async () => {
    const el = createMockElement({
      widgetId: 'inactivity-touch-cal',
      defaultView: 'week',
    });

    let resetFired = false;
    const inst = global.window.MirrormereCalendarFamily.mount(el, {
      inactivityTimeout: 50,
      onInactivityReset: () => { resetFired = true; },
    });

    inst.switchView('month');
    assert.equal(inst.activeView, 'month');

    // After 25ms, simulate user touch
    await new Promise(r => setTimeout(r, 25));
    el.dispatchEvent({ type: 'touchstart', preventDefault: () => {} });

    // After another 30ms (total 55ms, but timer was reset at 25ms so 30ms < 50ms):
    await new Promise(r => setTimeout(r, 30));
    assert.equal(resetFired, false, 'inactivity should not have fired yet because touch reset it');
    assert.equal(inst.activeView, 'month');

    // Wait remaining 40ms (now 70ms > 50ms from touch)
    await new Promise(r => setTimeout(r, 40));
    assert.equal(resetFired, true, 'inactivity should now have fired');
    assert.equal(inst.activeView, 'week');

    global.window.MirrormereCalendarFamily.unmount('inactivity-touch-cal');
  });

  await t.test('unmount cleanly destroys timers, active modal and listeners', () => {
    global.window.MirrormereCarousel.reset();
    const el = createMockElement({ widgetId: 'destroy-cal' });
    const inst = global.window.MirrormereCalendarFamily.mount(el);

    el._events.timed.click();
    assert.ok(inst.activeModal);
    assert.ok(inst.inactivityTimer);

    global.window.MirrormereCalendarFamily.unmount('destroy-cal');

    assert.equal(inst.inactivityTimer, null);
    assert.equal(inst.activeModal, null);
    assert.equal(global.window.MirrormereCarousel.resumeCount, 1);
  });

  // Chunk 8: Touch swipe gesture navigation and window bounds paging
  await t.test('swiping left in week view pages forward by 7 days', () => {
    const el = createMockElement({
      widgetId: 'swipe-week-cal',
      defaultView: 'week',
      data: {
        range_start: '2026-10-04',
        range_end: '2026-10-25',
        cached_range_start: '2026-10-04',
        cached_range_end: '2026-10-25',
        days: [
          { date: '2026-10-04' },
          { date: '2026-10-10' }
        ]
      }
    });
    const inst = global.window.MirrormereCalendarFamily.mount(el);

    assert.equal(inst.activeView, 'week');
    assert.equal(inst.pageOffset, 0);
    assert.equal(inst.formatDateISO(inst.currentWeekStart), '2026-10-04');

    // Swipe left (next): start at 200, end at 80 (deltaX = -120)
    el.dispatchEvent({
      type: 'touchstart',
      touches: [{ clientX: 200, clientY: 100 }],
    });
    el.dispatchEvent({
      type: 'touchmove',
      touches: [{ clientX: 140, clientY: 100 }],
    });
    el.dispatchEvent({
      type: 'touchend',
      changedTouches: [{ clientX: 80, clientY: 100 }],
    });

    assert.equal(inst.pageOffset, 1, 'pageOffset should increment on swipe left');
    assert.equal(inst.formatDateISO(inst.currentWeekStart), '2026-10-11', 'weekStart should advance by 7 days');
    assert.ok(el._labels.week.textContent.includes('Oct 11 – 17, 2026'), 'range label should reflect next week');

    // Swipe right (prev): start at 80, end at 200 (deltaX = +120)
    el.dispatchEvent({
      type: 'touchstart',
      touches: [{ clientX: 80, clientY: 100 }],
    });
    el.dispatchEvent({
      type: 'touchend',
      changedTouches: [{ clientX: 200, clientY: 100 }],
    });

    assert.equal(inst.pageOffset, 0, 'pageOffset should return to 0');
    assert.equal(inst.formatDateISO(inst.currentWeekStart), '2026-10-04', 'weekStart should return to initial week');
    assert.ok(el._labels.week.textContent.includes('Oct 4 – 10, 2026'));

    global.window.MirrormereCalendarFamily.unmount('swipe-week-cal');
  });

  await t.test('swiping in day view pages forward and backward by 1 day', () => {
    const el = createMockElement({
      widgetId: 'swipe-day-cal',
      defaultView: 'day',
      data: {
        date: '2026-10-10',
        cached_range_start: '2026-10-01',
        cached_range_end: '2026-10-20',
      }
    });
    const inst = global.window.MirrormereCalendarFamily.mount(el);

    assert.equal(inst.activeView, 'day');
    assert.equal(inst.formatDateISO(inst.currentDate), '2026-10-10');
    assert.equal(el._todayBadge.style.display, '');

    // Swipe left (next): deltaX = -100
    el.dispatchEvent({
      type: 'touchstart',
      touches: [{ clientX: 180, clientY: 50 }],
    });
    el.dispatchEvent({
      type: 'touchend',
      changedTouches: [{ clientX: 80, clientY: 50 }],
    });

    assert.equal(inst.pageOffset, 1);
    assert.equal(inst.formatDateISO(inst.currentDate), '2026-10-11');
    assert.equal(el._labels.day.textContent, 'Sun, Oct 11');
    assert.equal(el._todayBadge.style.display, 'none', 'today badge should hide when viewing future date');

    // Swipe right (prev): deltaX = +100
    el.dispatchEvent({
      type: 'touchstart',
      touches: [{ clientX: 80, clientY: 50 }],
    });
    el.dispatchEvent({
      type: 'touchend',
      changedTouches: [{ clientX: 180, clientY: 50 }],
    });

    assert.equal(inst.pageOffset, 0);
    assert.equal(inst.formatDateISO(inst.currentDate), '2026-10-10');
    assert.equal(el._labels.day.textContent, 'Sat, Oct 10');
    assert.equal(el._todayBadge.style.display, '', 'today badge should show when viewing today');

    global.window.MirrormereCalendarFamily.unmount('swipe-day-cal');
  });

  await t.test('swiping in month view pages forward and backward by 1 month', () => {
    const el = createMockElement({
      widgetId: 'swipe-month-cal',
      defaultView: 'month',
      data: {
        date: '2026-10-10',
        cached_range_start: '2026-08-01',
        cached_range_end: '2026-12-31',
      }
    });
    const inst = global.window.MirrormereCalendarFamily.mount(el);

    assert.equal(inst.activeView, 'month');

    // Swipe left: deltaX = -120 -> Nov 2026
    el.dispatchEvent({
      type: 'touchstart',
      touches: [{ clientX: 200, clientY: 50 }],
    });
    el.dispatchEvent({
      type: 'touchend',
      changedTouches: [{ clientX: 80, clientY: 50 }],
    });

    assert.equal(inst.pageOffset, 1);
    assert.equal(inst.formatDateISO(inst.currentDate).slice(0, 7), '2026-11');
    assert.equal(el._labels.month.textContent, 'November 2026');

    // Swipe right: deltaX = +120 -> Oct 2026
    el.dispatchEvent({
      type: 'touchstart',
      touches: [{ clientX: 80, clientY: 50 }],
    });
    el.dispatchEvent({
      type: 'touchend',
      changedTouches: [{ clientX: 200, clientY: 50 }],
    });

    assert.equal(inst.pageOffset, 0);
    assert.equal(inst.formatDateISO(inst.currentDate).slice(0, 7), '2026-10');
    assert.equal(el._labels.month.textContent, 'October 2026');

    global.window.MirrormereCalendarFamily.unmount('swipe-month-cal');
  });

  await t.test('swiping beyond cached date range blocks paging and displays inline "Not synced yet" indicator', () => {
    const el = createMockElement({
      widgetId: 'bounds-cal',
      defaultView: 'day',
      data: {
        date: '2026-10-10',
        cached_range_start: '2026-10-10',
        cached_range_end: '2026-10-10',
      }
    });
    const inst = global.window.MirrormereCalendarFamily.mount(el);

    assert.equal(el.querySelector('.cf-sync-indicator'), null);

    // Swipe left (next day: 2026-10-11 > cached_range_end)
    el.dispatchEvent({
      type: 'touchstart',
      touches: [{ clientX: 200, clientY: 50 }],
    });
    el.dispatchEvent({
      type: 'touchend',
      changedTouches: [{ clientX: 80, clientY: 50 }],
    });

    assert.equal(inst.pageOffset, 0, 'page offset should remain 0 when beyond bounds');
    assert.equal(inst.formatDateISO(inst.currentDate), '2026-10-10', 'date should not change');

    // Indicator should be displayed
    const indicator = el.querySelector('.cf-sync-indicator');
    assert.ok(indicator, 'inline sync indicator should be created');
    assert.equal(indicator.textContent, 'Not synced yet');
    assert.equal(indicator.style.display, '');

    // Swipe right (prev day: 2026-10-09 < cached_range_start)
    el.dispatchEvent({
      type: 'touchstart',
      touches: [{ clientX: 80, clientY: 50 }],
    });
    el.dispatchEvent({
      type: 'touchend',
      changedTouches: [{ clientX: 200, clientY: 50 }],
    });

    assert.equal(inst.pageOffset, 0);
    assert.equal(inst.formatDateISO(inst.currentDate), '2026-10-10');
    assert.equal(indicator.textContent, 'Not synced yet');

    global.window.MirrormereCalendarFamily.unmount('bounds-cal');
  });

  await t.test('inactivity timeout resets both activeView and paged date window', async () => {
    const el = createMockElement({
      widgetId: 'inactivity-page-cal',
      defaultView: 'week',
      data: {
        date: '2026-10-10',
        cached_range_start: '2026-10-01',
        cached_range_end: '2026-10-31',
      }
    });
    let resetNotified = false;
    const inst = global.window.MirrormereCalendarFamily.mount(el, {
      inactivityTimeout: 40,
      onInactivityReset: () => { resetNotified = true; },
    });

    // Switch to day view and page forward by 2 days
    inst.switchView('day');
    inst.pageNext();
    inst.pageNext();
    assert.equal(inst.activeView, 'day');
    assert.equal(inst.pageOffset, 2);
    assert.equal(inst.formatDateISO(inst.currentDate), '2026-10-12');

    // Wait for inactivity timeout (60ms > 40ms)
    await new Promise(r => setTimeout(r, 60));

    assert.equal(resetNotified, true);
    assert.equal(inst.activeView, 'week', 'activeView should reset to defaultView (week)');
    assert.equal(inst.pageOffset, 0, 'pageOffset should reset to 0');
    assert.equal(inst.formatDateISO(inst.currentDate), '2026-10-10', 'currentDate should reset to today');

    global.window.MirrormereCalendarFamily.unmount('inactivity-page-cal');
  });

  await t.test('rejects non-swipe gestures: vertical scroll, short distance, slow drag, and open modal', () => {
    const el = createMockElement({
      widgetId: 'gesture-reject-cal',
      defaultView: 'week',
    });
    const inst = global.window.MirrormereCalendarFamily.mount(el, {
      cachedRangeStart: '2026-09-01',
      cachedRangeEnd: '2026-11-30',
    });

    // 1. Dominant vertical scroll (deltaX = -45, deltaY = 120)
    el.dispatchEvent({
      type: 'touchstart',
      touches: [{ clientX: 100, clientY: 50 }],
    });
    el.dispatchEvent({
      type: 'touchend',
      changedTouches: [{ clientX: 55, clientY: 170 }],
    });
    assert.equal(inst.pageOffset, 0, 'vertical gesture should not trigger paging');

    // 2. Short distance (deltaX = -15 < swipeDistanceThreshold 40)
    el.dispatchEvent({
      type: 'touchstart',
      touches: [{ clientX: 100, clientY: 50 }],
    });
    el.dispatchEvent({
      type: 'touchend',
      changedTouches: [{ clientX: 85, clientY: 50 }],
    });
    assert.equal(inst.pageOffset, 0, 'short gesture should not trigger paging');

    // 3. Slow drag (velocity < threshold)
    el.dispatchEvent({
      type: 'touchstart',
      touches: [{ clientX: 200, clientY: 50 }],
    });
    // Artificially simulate 2000ms duration for slow drag
    inst.touchStartTime = Date.now() - 2000;
    el.dispatchEvent({
      type: 'touchend',
      changedTouches: [{ clientX: 80, clientY: 50 }],
    });
    assert.equal(inst.pageOffset, 0, 'slow drag should not trigger paging');

    // 4. Swipe ignored while detail modal is open
    el._events.timed.click();
    assert.ok(inst.activeModal);

    el.dispatchEvent({
      type: 'touchstart',
      touches: [{ clientX: 200, clientY: 50 }],
    });
    el.dispatchEvent({
      type: 'touchend',
      changedTouches: [{ clientX: 80, clientY: 50 }],
    });
    assert.equal(inst.pageOffset, 0, 'swipe while modal is open should be ignored');

    inst.closeModal();
    global.window.MirrormereCalendarFamily.unmount('gesture-reject-cal');
  });

  await t.test('swiping does not trigger accidental event click modal', () => {
    const el = createMockElement({
      widgetId: 'ghost-click-cal',
      defaultView: 'week',
      data: {
        date: '2026-10-10',
        cached_range_start: '2026-10-01',
        cached_range_end: '2026-10-31',
      }
    });
    const inst = global.window.MirrormereCalendarFamily.mount(el);

    // Perform a valid swipe on an event element
    el._events.timed.dispatchEvent({
      type: 'touchstart',
      touches: [{ clientX: 200, clientY: 50 }],
    });
    el._events.timed.dispatchEvent({
      type: 'touchend',
      changedTouches: [{ clientX: 80, clientY: 50 }],
    });

    assert.equal(inst.pageOffset, 1, 'swipe should advance page');

    // Simulate trailing click event on the same element right after touchend
    el._events.timed.click();

    assert.equal(inst.activeModal, null, 'ghost click after swipe should NOT open modal');

    global.window.MirrormereCalendarFamily.unmount('ghost-click-cal');
  });

  await t.test('indexes combined data from month_days, day_all_day_events, and events list', () => {
    const el = createMockElement({
      widgetId: 'combined-data-cal',
      data: {
        events: [{ id: 'ev-flat', title: 'Flat Event' }],
        day_all_day_events: [{ id: 'ev-day-allday', title: 'Day All Day' }],
        month_days: [{
          date: '2026-10-15',
          events: [{ id: 'ev-month-1', title: 'Month Gala' }],
        }],
      }
    });
    const inst = global.window.MirrormereCalendarFamily.mount(el);

    assert.ok(inst.eventIndex.has('ev-flat'), 'should index flat events list');
    assert.ok(inst.eventIndex.has('ev-day-allday'), 'should index day_all_day_events');
    assert.ok(inst.eventIndex.has('ev-month-1'), 'should index events in month_days');

    const evData = inst.resolveEventData({
      dataset: { date: '2026-10-15' },
    });
    assert.ok(evData, 'should resolve event data from month_days date cell');
    assert.equal(evData.id, 'ev-month-1');

    global.window.MirrormereCalendarFamily.unmount('combined-data-cal');
  });

  await t.test('swiping left/right in week, day, and month views invokes fetch with ?date=...&view=... and replaces panel DOM with fresh event cards', async () => {
    const fetchCalls = [];
    const mockFetch = async (url, opts) => {
      fetchCalls.push({ url, signal: opts && opts.signal });
      const parsedUrl = new URL('http://localhost/' + url);
      const date = parsedUrl.searchParams.get('date');
      const view = parsedUrl.searchParams.get('view');
      const html = `
        <div class="cf-view-panel cf-view-day">
          <div class="cf-day-event cf-event-clickable" data-event-id="ev-fresh-${date}-day">
            <div class="cf-event-title">Fresh Day Event ${date}</div>
          </div>
        </div>
        <div class="cf-view-panel cf-view-week">
          <div class="cf-event cf-timed cf-event-clickable" data-event-id="ev-fresh-${date}-week">
            <div class="cf-event-title">Fresh Week Event ${date}</div>
          </div>
        </div>
        <div class="cf-view-panel cf-view-month">
          <div class="cf-month-day cf-has-events" data-date="${date}">
            <div class="cf-month-event-title cf-event-clickable" data-event-id="ev-fresh-${date}-month">Fresh Month Event ${date}</div>
          </div>
        </div>
        <span class="cf-range-label cf-label-day">Day ${date}</span>
        <span class="cf-range-label cf-label-week">Week ${date}</span>
        <span class="cf-range-label cf-label-month">Month ${date}</span>
        <script class="calendar-family-data">${JSON.stringify({
          days: [{
            date,
            timed: [{ id: `ev-fresh-${date}-week`, title: `Fresh Week Event ${date}` }],
            all_day: [{ id: `ev-fresh-${date}-day`, title: `Fresh Day Event ${date}` }],
            events: [{ id: `ev-fresh-${date}-month`, title: `Fresh Month Event ${date}` }]
          }],
          month_days: [{
            date,
            events: [{ id: `ev-fresh-${date}-month`, title: `Fresh Month Event ${date}` }]
          }]
        })}</script>
      `;
      return { ok: true, text: async () => html };
    };

    const el = createMockElement({
      widgetId: 'paging-swipe-cal',
      defaultView: 'week',
    });
    const inst = global.window.MirrormereCalendarFamily.mount(el, {
      fetch: mockFetch,
      cachedRange: { start: '2026-09-01', end: '2026-11-30' },
    });

    // 1. Week view swipe left (forward)
    el.dispatchEvent({
      type: 'touchstart',
      touches: [{ clientX: 200, clientY: 50 }],
    });
    el.dispatchEvent({
      type: 'touchend',
      changedTouches: [{ clientX: 50, clientY: 50 }],
    });

    await new Promise(r => setTimeout(r, 10));

    assert.equal(fetchCalls.length, 1);
    assert.ok(fetchCalls[0].url.includes('date=2026-10-17'));
    assert.ok(fetchCalls[0].url.includes('view=week'));

    // Check DOM replacement: week panel should have the fresh event card
    const freshWeekCard = inst.viewPanels.week.querySelector('[data-event-id="ev-fresh-2026-10-17-week"]');
    assert.ok(freshWeekCard, 'week panel should be replaced with fresh event card');

    // Wait for justSwiped cooldown (120ms) then tap fresh card
    await new Promise(r => setTimeout(r, 130));
    freshWeekCard.click();
    assert.ok(inst.activeModal, 'tapping fresh event opens modal');
    assert.equal(inst.activeModal.querySelector('.cf-modal-title').textContent, 'Fresh Week Event 2026-10-17');
    inst.closeModal();

    // 2. Day view swipe left
    inst.switchView('day');
    fetchCalls.length = 0;
    el.dispatchEvent({
      type: 'touchstart',
      touches: [{ clientX: 200, clientY: 50 }],
    });
    el.dispatchEvent({
      type: 'touchend',
      changedTouches: [{ clientX: 50, clientY: 50 }],
    });

    await new Promise(r => setTimeout(r, 10));
    assert.equal(fetchCalls.length, 1);
    assert.ok(fetchCalls[0].url.includes('view=day'));
    const freshDayCard = inst.viewPanels.day.querySelector('[data-event-id]');
    assert.ok(freshDayCard, 'day panel should be replaced with fresh day event card');

    // 3. Month view swipe left
    inst.switchView('month');
    fetchCalls.length = 0;
    el.dispatchEvent({
      type: 'touchstart',
      touches: [{ clientX: 200, clientY: 50 }],
    });
    el.dispatchEvent({
      type: 'touchend',
      changedTouches: [{ clientX: 50, clientY: 50 }],
    });

    await new Promise(r => setTimeout(r, 10));
    assert.equal(fetchCalls.length, 1);
    assert.ok(fetchCalls[0].url.includes('view=month'));
    const freshMonthCard = inst.viewPanels.month.querySelector('[data-event-id]');
    assert.ok(freshMonthCard, 'month panel should be replaced with fresh month event card');

    global.window.MirrormereCalendarFamily.unmount('paging-swipe-cal');
  });

  await t.test('tapping .cf-prev-btn and .cf-next-btn triggers paging and DOM replacement', async () => {
    const fetchCalls = [];
    const mockFetch = async (url) => {
      fetchCalls.push(url);
      const parsedUrl = new URL('http://localhost/' + url);
      const date = parsedUrl.searchParams.get('date');
      const html = `
        <div class="cf-view-panel cf-view-week">
          <div class="cf-event cf-timed cf-event-clickable" data-event-id="ev-btn-${date}">
            <div class="cf-event-title">Button Event ${date}</div>
          </div>
        </div>
        <script class="calendar-family-data">${JSON.stringify({
          days: [{ date, timed: [{ id: `ev-btn-${date}`, title: `Button Event ${date}` }] }]
        })}</script>
      `;
      return { ok: true, text: async () => html };
    };

    const el = createMockElement({ widgetId: 'btn-paging-cal' });
    const inst = global.window.MirrormereCalendarFamily.mount(el, {
      fetch: mockFetch,
      cachedRange: { start: '2026-09-01', end: '2026-11-30' },
    });

    // Tap next button
    el._btnNext.click();
    await new Promise(r => setTimeout(r, 10));

    assert.equal(fetchCalls.length, 1);
    assert.ok(fetchCalls[0].includes('date=2026-10-17'));
    assert.equal(inst.pageOffset, 1);
    const cardNext = inst.viewPanels.week.querySelector('[data-event-id="ev-btn-2026-10-17"]');
    assert.ok(cardNext, 'next button replaces week panel with paged content');

    // Tap prev button
    el._btnPrev.click();
    await new Promise(r => setTimeout(r, 10));

    assert.equal(fetchCalls.length, 2);
    assert.ok(fetchCalls[1].includes('date=2026-10-10'));
    assert.equal(inst.pageOffset, 0);

    global.window.MirrormereCalendarFamily.unmount('btn-paging-cal');
  });

  await t.test('tapping .cf-today-badge resets date back to base date', async () => {
    let fetchCount = 0;
    const mockFetch = async (url) => {
      fetchCount++;
      return { ok: true, text: async () => `<div class="cf-view-panel cf-view-day"></div>` };
    };

    const el = createMockElement({ widgetId: 'today-badge-cal', defaultView: 'day' });
    const inst = global.window.MirrormereCalendarFamily.mount(el, {
      fetch: mockFetch,
      cachedRange: { start: '2026-09-01', end: '2026-11-30' },
    });

    // Advance 2 days forward
    el._btnNext.click();
    el._btnNext.click();
    assert.equal(inst.pageOffset, 2);
    assert.equal(inst.currentDate.getDate(), 12);

    // Tap TODAY badge
    el._todayBadge.click();
    assert.equal(inst.pageOffset, 0, 'pageOffset should reset to 0');
    assert.equal(inst.currentDate.getDate(), 10, 'currentDate should reset to base date');

    global.window.MirrormereCalendarFamily.unmount('today-badge-cal');
  });

  await t.test('cached date views are served from renderCache without additional fetch requests', async () => {
    let fetchCount = 0;
    const mockFetch = async (url) => {
      fetchCount++;
      const parsedUrl = new URL('http://localhost/' + url);
      const date = parsedUrl.searchParams.get('date');
      return {
        ok: true,
        text: async () => `
          <div class="cf-view-panel cf-view-week">
            <div class="cf-event cf-timed" data-event-id="cached-${date}">Event ${date}</div>
          </div>
          <script class="calendar-family-data">${JSON.stringify({
            days: [{ date, events: [{ id: `cached-${date}`, title: `Event ${date}` }] }]
          })}</script>
        `
      };
    };

    const el = createMockElement({ widgetId: 'render-cache-cal' });
    const inst = global.window.MirrormereCalendarFamily.mount(el, {
      fetch: mockFetch,
      cachedRange: { start: '2026-09-01', end: '2026-11-30' },
    });

    // Page forward to 2026-10-17 -> fetch 1
    el._btnNext.click();
    await new Promise(r => setTimeout(r, 10));
    assert.equal(fetchCount, 1);
    assert.ok(inst.renderCache.has('2026-10-17:week'));

    // Page backward to 2026-10-10 -> fetch 2
    el._btnPrev.click();
    await new Promise(r => setTimeout(r, 10));
    assert.equal(fetchCount, 2);
    assert.ok(inst.renderCache.has('2026-10-10:week'));

    // Page forward again to 2026-10-17 -> should be served from renderCache!
    el._btnNext.click();
    await new Promise(r => setTimeout(r, 10));
    assert.equal(fetchCount, 2, 'subsequent navigation to cached date view should not fetch');
    const cachedCard = inst.viewPanels.week.querySelector('[data-event-id="cached-2026-10-17"]');
    assert.ok(cachedCard, 'panel should display content from renderCache');

    global.window.MirrormereCalendarFamily.unmount('render-cache-cal');
  });

  await t.test('rapid multi-swipes cancel in-flight fetches and ignore stale out-of-order responses', async () => {
    const signals = [];
    let resolveFirst;
    let resolveSecond;

    const mockFetch = (url, opts) => {
      signals.push(opts.signal);
      if (signals.length === 1) {
        return new Promise(resolve => {
          resolveFirst = () => resolve({
            ok: true,
            text: async () => `
              <div class="cf-view-panel cf-view-week">
                <div class="cf-event" data-event-id="stale-slow">Stale Slow Event</div>
              </div>
            `
          });
        });
      }
      return new Promise(resolve => {
        resolveSecond = () => resolve({
          ok: true,
          text: async () => `
            <div class="cf-view-panel cf-view-week">
              <div class="cf-event" data-event-id="fresh-fast">Fresh Fast Event</div>
            </div>
          `
        });
      });
    };

    const el = createMockElement({ widgetId: 'race-defense-cal' });
    const inst = global.window.MirrormereCalendarFamily.mount(el, {
      fetch: mockFetch,
      cachedRange: { start: '2026-09-01', end: '2026-11-30' },
    });

    // Swipe 1 (starts fetch 1)
    el.dispatchEvent({ type: 'touchstart', touches: [{ clientX: 200, clientY: 50 }] });
    el.dispatchEvent({ type: 'touchend', changedTouches: [{ clientX: 50, clientY: 50 }] });

    assert.equal(signals.length, 1);
    assert.equal(signals[0].aborted, false);

    // Rapid Swipe 2 (cancels fetch 1, starts fetch 2)
    el.dispatchEvent({ type: 'touchstart', touches: [{ clientX: 200, clientY: 50 }] });
    el.dispatchEvent({ type: 'touchend', changedTouches: [{ clientX: 50, clientY: 50 }] });

    assert.equal(signals.length, 2);
    assert.equal(signals[0].aborted, true, 'first in-flight fetch should be aborted');

    // Resolve second fetch first
    resolveSecond();
    await new Promise(r => setTimeout(r, 10));

    const fastCard = inst.viewPanels.week.querySelector('[data-event-id="fresh-fast"]');
    assert.ok(fastCard, 'second fetch response should be applied');

    // Now resolve first stale fetch late
    resolveFirst();
    await new Promise(r => setTimeout(r, 10));

    // Panel should STILL have fresh-fast, NOT stale-slow
    const staleCard = inst.viewPanels.week.querySelector('[data-event-id="stale-slow"]');
    assert.equal(staleCard, null, 'stale out-of-order response should be discarded');
    assert.ok(inst.viewPanels.week.querySelector('[data-event-id="fresh-fast"]'));

    global.window.MirrormereCalendarFamily.unmount('race-defense-cal');
  });

  await t.test('failed network response triggers rollback and displays sync failed indicator', async () => {
    const mockFetch = async () => {
      return { ok: false, status: 500 };
    };

    const el = createMockElement({ widgetId: 'rollback-cal' });
    const inst = global.window.MirrormereCalendarFamily.mount(el, {
      fetch: mockFetch,
      cachedRange: { start: '2026-09-01', end: '2026-11-30' },
    });

    const initialDate = inst.formatDateISO(inst.currentDate);
    const initialOffset = inst.pageOffset;

    // Trigger swipe forward
    el.dispatchEvent({ type: 'touchstart', touches: [{ clientX: 200, clientY: 50 }] });
    el.dispatchEvent({ type: 'touchend', changedTouches: [{ clientX: 50, clientY: 50 }] });

    await new Promise(r => setTimeout(r, 15));

    // Should have rolled back
    assert.equal(inst.pageOffset, initialOffset, 'pageOffset should roll back on fetch error');
    assert.equal(inst.formatDateISO(inst.currentDate), initialDate, 'currentDate should roll back on fetch error');

    // Should display sync failed indicator
    const indicator = el.querySelector('.cf-sync-indicator');
    assert.ok(indicator, 'sync indicator should be created');
    assert.equal(indicator.textContent, 'Sync failed', 'sync indicator text should show failure');
    assert.equal(indicator.style.display, '');

    global.window.MirrormereCalendarFamily.unmount('rollback-cal');
  });

});

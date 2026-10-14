const { test, describe, beforeEach, afterEach } = require('node:test');
const assert = require('node:assert/strict');

describe('Header Next Up Event Pill', () => {
  let originalDocument;
  let originalWindow;

  function setupMockDOM({ timezone = 'America/Los_Angeles', theme = 'day' } = {}) {
    const elements = {};
    const getEl = (id) => {
      if (!elements[id]) {
        elements[id] = {
          id,
          textContent: '',
          innerHTML: '',
          title: '',
          style: {},
          classList: new Set(),
          dataset: {},
          setAttribute(k, v) { this[k] = v; },
          getAttribute(k) { return this[k]; },
        };
        elements[id].classList.contains = function(c) { return this.has(c); };
        elements[id].classList.remove = function(c) { return this.delete(c); };
      }
      return elements[id];
    };

    global.document = {
      getElementById(id) {
        return getEl(id);
      },
      querySelectorAll(selector) {
        return [];
      },
      querySelector(selector) {
        return null;
      },
      body: {
        dataset: { timezone, theme },
        classList: new Set(),
      },
      documentElement: {
        dataset: { theme },
      },
      readyState: 'complete',
      addEventListener() {},
      removeEventListener() {},
    };

    global.window = {
      location: { search: '' },
    };

    return elements;
  }

  afterEach(() => {
    global.document = originalDocument;
    global.window = originalWindow;
  });

  beforeEach(() => {
    originalDocument = global.document;
    originalWindow = global.window;
  });

  test('hides next up event pill when no events exist', () => {
    const elements = setupMockDOM();
    delete require.cache[require.resolve('../static/js/display.js')];
    const display = require('../static/js/display.js');

    const now = new Date('2026-10-12T14:00:00-07:00');
    display.updateNextUpEvent(now, []);

    const container = elements['header-next-event'];
    assert.equal(container.style.display, 'none');
  });

  test('picks earliest upcoming timed event and renders NEXT badge with relative countdown', () => {
    const elements = setupMockDOM();
    delete require.cache[require.resolve('../static/js/display.js')];
    const display = require('../static/js/display.js');

    const now = new Date('2026-10-12T14:00:00-07:00');
    const events = [
      {
        id: 'ev-later',
        title: 'Dinner with Grandma',
        start: '2026-10-12T18:00:00-07:00',
        end: '2026-10-12T19:30:00-07:00',
        allDay: false,
      },
      {
        id: 'ev-sooner',
        title: 'Soccer Practice',
        start: '2026-10-12T15:30:00-07:00',
        end: '2026-10-12T16:30:00-07:00',
        allDay: false,
      },
      {
        id: 'ev-past',
        title: 'Morning Standup',
        start: '2026-10-12T09:00:00-07:00',
        end: '2026-10-12T09:30:00-07:00',
        allDay: false,
      },
    ];

    display.updateNextUpEvent(now, events);

    const container = elements['header-next-event'];
    assert.notEqual(container.style.display, 'none');

    const titleEl = elements['next-event-title'];
    assert.equal(titleEl.textContent, 'Soccer Practice');

    const labelEl = elements['next-event-label'];
    assert.equal(labelEl.textContent, 'NEXT');

    const timeEl = elements['next-event-time'];
    assert.match(timeEl.textContent, /3:30\s*PM/i);

    const countdownEl = elements['next-event-countdown'];
    assert.match(countdownEl.textContent, /in 1h 30m|in 90m/i);
  });

  test('renders NOW badge for currently ongoing timed event with remaining time', () => {
    const elements = setupMockDOM();
    delete require.cache[require.resolve('../static/js/display.js')];
    const display = require('../static/js/display.js');

    const now = new Date('2026-10-12T14:15:00-07:00');
    const events = [
      {
        id: 'ev-active',
        title: 'Deep Work Block',
        start: '2026-10-12T14:00:00-07:00',
        end: '2026-10-12T15:00:00-07:00',
        allDay: false,
      },
      {
        id: 'ev-next',
        title: 'Doctor Appointment',
        start: '2026-10-12T16:00:00-07:00',
        end: '2026-10-12T17:00:00-07:00',
        allDay: false,
      },
    ];

    display.updateNextUpEvent(now, events);

    const container = elements['header-next-event'];
    assert.notEqual(container.style.display, 'none');

    const pillEl = elements['next-event-pill'];
    assert.equal(pillEl.classList.contains('status-now'), true);

    const labelEl = elements['next-event-label'];
    assert.equal(labelEl.textContent, 'NOW');

    const titleEl = elements['next-event-title'];
    assert.equal(titleEl.textContent, 'Deep Work Block');

    const countdownEl = elements['next-event-countdown'];
    assert.match(countdownEl.textContent, /ends in 45m/i);
  });

  test('handles all-day event for today when no earlier timed event is pending', () => {
    const elements = setupMockDOM();
    delete require.cache[require.resolve('../static/js/display.js')];
    const display = require('../static/js/display.js');

    const now = new Date('2026-10-12T10:00:00-07:00');
    const events = [
      {
        id: 'ev-allday',
        title: 'Indigenous Peoples Day',
        start: '2026-10-12',
        end: '2026-10-12',
        allDay: true,
      },
    ];

    display.updateNextUpEvent(now, events);

    const container = elements['header-next-event'];
    assert.notEqual(container.style.display, 'none');

    const labelEl = elements['next-event-label'];
    assert.equal(labelEl.textContent, 'TODAY');

    const timeEl = elements['next-event-time'];
    assert.equal(timeEl.textContent, 'All Day');

    const titleEl = elements['next-event-title'];
    assert.equal(titleEl.textContent, 'Indigenous Peoples Day');
  });

  test('prioritizes upcoming timed event over all-day event if timed event is sooner', () => {
    const elements = setupMockDOM();
    delete require.cache[require.resolve('../static/js/display.js')];
    const display = require('../static/js/display.js');

    const now = new Date('2026-10-12T10:00:00-07:00');
    const events = [
      {
        id: 'ev-allday',
        title: 'Alex in Sunnyvale',
        start: '2026-10-12',
        end: '2026-10-12',
        allDay: true,
      },
      {
        id: 'ev-timed',
        title: 'Lunch with Sarah',
        start: '2026-10-12T12:00:00-07:00',
        end: '2026-10-12T13:00:00-07:00',
        allDay: false,
      },
    ];

    display.updateNextUpEvent(now, events);

    const titleEl = elements['next-event-title'];
    assert.equal(titleEl.textContent, 'Lunch with Sarah');
    const labelEl = elements['next-event-label'];
    assert.equal(labelEl.textContent, 'NEXT');
  });

  test('extracts events from embedded calendar-feed and calendar-grid DOM nodes', () => {
    const elements = setupMockDOM();

    // Mock document.querySelectorAll returning DOM nodes with JSON scripts
    const feedJson = JSON.stringify([
      {
        id: 'feed-1',
        title: 'Feed Event',
        start: '2026-10-12T16:00:00-07:00',
        end: '2026-10-12T17:00:00-07:00',
        all_day: false,
      },
    ]);

    const gridJson = JSON.stringify({
      days: [
        {
          date: '2026-10-12',
          events: [
            {
              id: 'grid-1',
              title: 'Grid Event',
              start: '15:00',
              end: '15:45',
              all_day: false,
            },
          ],
        },
      ],
    });

    global.document.querySelectorAll = (selector) => {
      if (selector.includes('calendar-feed-data')) {
        return [{ textContent: feedJson }];
      }
      if (selector.includes('calendar-grid-data') || selector.includes('calendar-family-data')) {
        return [{ textContent: gridJson }];
      }
      return [];
    };

    delete require.cache[require.resolve('../static/js/display.js')];
    const display = require('../static/js/display.js');

    const now = new Date('2026-10-12T14:00:00-07:00');
    display.updateNextUpEvent(now);

    const titleEl = elements['next-event-title'];
    // Grid event at 15:00 is earlier than feed event at 16:00
    assert.equal(titleEl.textContent, 'Grid Event');
  });

  test('next-event-pill is styled at 52px height matching weather pill with no glowing icon', () => {
    const fs = require('node:fs');
    const path = require('node:path');

    const templatePath = path.resolve(__dirname, '../templates/display.html');
    const templateContent = fs.readFileSync(templatePath, 'utf8');
    assert.doesNotMatch(templateContent, /next-event-icon/, 'display.html must not contain next-event-icon');
    assert.doesNotMatch(templateContent, /mm-next-event-icon-svg/, 'display.html must not contain mm-next-event-icon-svg');

    const hudCSSPath = path.resolve(__dirname, '../static/css/hud.css');

    for (const cssPath of [hudCSSPath]) {
      const cssContent = fs.readFileSync(cssPath, 'utf8');
      assert.match(cssContent, /\.next-event-pill\s*\{[^}]*height:\s*52px;/s, `${path.basename(cssPath)} must define height: 52px`);
      assert.match(cssContent, /\.next-event-pill\s*\{[^}]*min-height:\s*52px;/s, `${path.basename(cssPath)} must define min-height: 52px`);
      assert.match(cssContent, /\.next-event-pill\s*\{[^}]*box-sizing:\s*border-box;/s, `${path.basename(cssPath)} must define box-sizing: border-box`);
      assert.doesNotMatch(cssContent, /\.next-event-icon\b/, `${path.basename(cssPath)} must not define .next-event-icon`);
    }
  });
});

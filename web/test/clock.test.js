const { test, describe } = require('node:test');
const assert = require('node:assert/strict');

describe('Header Clock Household Timezone (SPEC-001 §4, SPEC-012 §5)', () => {
  function setupMockDOM(dataset = {}) {
    const elements = {};
    global.document = {
      getElementById(id) {
        if (!elements[id]) {
          elements[id] = { textContent: '', dataset: {} };
        }
        return elements[id];
      },
      body: {
        dataset: { ...dataset },
      },
      readyState: 'complete',
      addEventListener() {},
    };
    return elements;
  }

  test('renders clock with fixed instant and America/Los_Angeles from UTC environment', () => {
    setupMockDOM({ timezone: 'America/Los_Angeles' });

    // Clear module cache to re-initialize with fresh mock DOM
    delete require.cache[require.resolve('../static/js/display.js')];
    const display = require('../static/js/display.js');

    // Fixed instant: 2026-09-25 20:00:00 UTC
    // In America/Los_Angeles (Daylight Saving PDT, UTC-7), this is 13:00 (1:00 PM) on Friday, Sep 25.
    const fixedInstant = new Date('2026-09-25T20:00:00Z');
    display.updateClock(fixedInstant);

    const clockEl = document.getElementById('header-clock');
    const dateEl = document.getElementById('header-date');

    // Validate 1:00 PM (or 13:00 for 24h locales) and Friday, Sep 25
    assert.match(clockEl.textContent, /^(1:00\s*PM|13:00)$/i, `unexpected clock time: ${clockEl.textContent}`);
    assert.match(dateEl.textContent, /Friday.*Sep.*25/i, `unexpected date format: ${dateEl.textContent}`);
  });

  test('updates clock dynamically when header.update delivers new timezone', () => {
    setupMockDOM({ timezone: 'America/Los_Angeles' });

    delete require.cache[require.resolve('../static/js/display.js')];
    const display = require('../static/js/display.js');

    const fixedInstant = new Date('2026-09-25T20:00:00Z');
    display.updateClock(fixedInstant);

    let clockEl = document.getElementById('header-clock');
    assert.match(clockEl.textContent, /^(1:00\s*PM|13:00)$/i);

    // Live config reload emits header.update with America/New_York (EDT, UTC-4)
    // 20:00 UTC is 16:00 (4:00 PM)
    display.handleHeaderUpdate({
      timestamp: '2026-09-25T20:00:00Z',
      timezone: 'America/New_York',
    });
    display.updateClock(fixedInstant);

    clockEl = document.getElementById('header-clock');
    assert.match(clockEl.textContent, /^(4:00\s*PM|16:00)$/i, `unexpected clock time after timezone update: ${clockEl.textContent}`);
  });

  test('gracefully falls back when invalid timezone or unrendered template is provided', () => {
    setupMockDOM({ timezone: '{{ .Timezone }}' });

    delete require.cache[require.resolve('../static/js/display.js')];
    const display = require('../static/js/display.js');

    assert.equal(display.isValidTimezone('{{ .Timezone }}'), false);
    assert.equal(display.isValidTimezone('Invalid/Zone'), false);
    assert.equal(display.isValidTimezone('America/Los_Angeles'), true);

    const fixedInstant = new Date('2026-09-25T20:00:00Z');
    // Should not throw and should render fallback format
    display.updateClock(fixedInstant);

    const clockEl = document.getElementById('header-clock');
    assert.ok(clockEl.textContent.length > 0);
  });
});

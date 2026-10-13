const { test, describe } = require('node:test');
const assert = require('node:assert/strict');

describe('Header Clock Household Timezone', () => {
  function setupMockDOM(dataset = {}) {
    const elements = {};
    global.document = {
      getElementById(id) {
        if (!elements[id]) {
          elements[id] = { textContent: '', innerHTML: '', title: '', dataset: {} };
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

  test('updates weather condition icon with SVG markup in header.update', () => {
    const elements = setupMockDOM();

    delete require.cache[require.resolve('../static/js/display.js')];
    const display = require('../static/js/display.js');

    // Test helper directly
    const sunnySvg = display.getWeatherIconSVG('weather-sunny', 20);
    assert.ok(sunnySvg.includes('width="20"'));
    assert.ok(sunnySvg.includes('mm-icon-weather-sunny'));

    const fallbackSvg = display.getWeatherIconSVG('unknown-token', 18);
    assert.ok(fallbackSvg.includes('width="18"'));
    assert.ok(fallbackSvg.includes('mm-icon-weather-cloudy'));

    // Test handleHeaderUpdate
    display.handleHeaderUpdate({
      weather: {
        temperature: 72.4,
        units: '°F',
        icon: 'weather-partly-cloudy',
      },
    });

    const tempEl = elements['weather-temp'];
    const conditionEl = elements['weather-condition'];

    assert.equal(tempEl.textContent, '72°F');
    assert.ok(conditionEl.innerHTML.includes('<svg'));
    assert.ok(conditionEl.innerHTML.includes('mm-icon-weather-partly-cloudy'));
    assert.equal(conditionEl.title, 'weather-partly-cloudy');
  });

  test('updates nighttime weather condition icon and description in header.update', () => {
    const elements = setupMockDOM();

    delete require.cache[require.resolve('../static/js/display.js')];
    const display = require('../static/js/display.js');

    // Test clear night helper directly
    const clearNightSvg = display.getWeatherIconSVG('weather-clear-night', 24);
    assert.ok(clearNightSvg.includes('width="24"'));
    assert.ok(clearNightSvg.includes('mm-icon-weather-clear-night'));

    // Test night partly cloudy helper directly
    const nightCloudySvg = display.getWeatherIconSVG('weather-night-partly-cloudy', 24);
    assert.ok(nightCloudySvg.includes('width="24"'));
    assert.ok(nightCloudySvg.includes('mm-icon-weather-night-partly-cloudy'));

    // Test handleHeaderUpdate with clear night
    display.handleHeaderUpdate({
      weather: {
        temperature: 58.2,
        units: '°F',
        icon: 'weather-clear-night',
        condition: 'Clear',
      },
    });

    const tempEl = elements['weather-temp'];
    const conditionEl = elements['weather-condition'];
    const descEl = elements['weather-desc'];

    assert.equal(tempEl.textContent, '58°F');
    assert.ok(conditionEl.innerHTML.includes('<svg'));
    assert.ok(conditionEl.innerHTML.includes('mm-icon-weather-clear-night'));
    assert.equal(conditionEl.title, 'weather-clear-night');
    assert.equal(descEl.textContent, 'Clear');

    // Test fallback for nighttime tokens does not produce 'Sunny & Mild'
    display.handleHeaderUpdate({
      weather: {
        temperature: 55.0,
        units: '°F',
        icon: 'weather-night-unknown',
      },
    });
    assert.equal(descEl.textContent, 'Clear');
    assert.notEqual(descEl.textContent, 'Sunny & Mild');
  });
});

const { test, describe } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

describe('Fairyfloss Theme Mode Switching', () => {
  const hudCSSPath = path.resolve(__dirname, '../static/css/hud.css');

  function setupMockDOM(initialTheme = 'day') {
    const elements = {
      'mirrormere-app': { dataset: { theme: initialTheme } },
    };
    global.document = {
      documentElement: { dataset: { theme: initialTheme } },
      body: { dataset: { theme: initialTheme } },
      getElementById(id) {
        if (!elements[id]) {
          elements[id] = { dataset: {} };
        }
        return elements[id];
      },
      readyState: 'complete',
      addEventListener() {},
    };
    return elements;
  }

  test('handleThemeUpdate switches document, body, and app datasets to night and back to day', () => {
    setupMockDOM('day');

    delete require.cache[require.resolve('../static/js/display.js')];
    const display = require('../static/js/display.js');

    assert.equal(document.documentElement.dataset.theme, 'day');
    assert.equal(document.body.dataset.theme, 'day');
    assert.equal(document.getElementById('mirrormere-app').dataset.theme, 'day');

    display.handleThemeUpdate({ theme: 'night' });
    assert.equal(document.documentElement.dataset.theme, 'night');
    assert.equal(document.body.dataset.theme, 'night');
    assert.equal(document.getElementById('mirrormere-app').dataset.theme, 'night');

    display.handleThemeUpdate({ theme: 'day' });
    assert.equal(document.documentElement.dataset.theme, 'day');
    assert.equal(document.body.dataset.theme, 'day');
    assert.equal(document.getElementById('mirrormere-app').dataset.theme, 'day');
  });

  test('handleThemeUpdate ignores empty or malformed payload', () => {
    setupMockDOM('night');

    delete require.cache[require.resolve('../static/js/display.js')];
    const display = require('../static/js/display.js');

    display.handleThemeUpdate(null);
    assert.equal(document.documentElement.dataset.theme, 'night');

    display.handleThemeUpdate({});
    assert.equal(document.documentElement.dataset.theme, 'night');
  });

  test('CSS stylesheets contain Sailor HG Fairyfloss night mode tokens', () => {
    for (const cssPath of [hudCSSPath]) {
      assert.ok(fs.existsSync(cssPath), `${path.basename(cssPath)} must exist`);
      const content = fs.readFileSync(cssPath, 'utf8');

      // Must have data-theme="night" selector
      assert.match(content, /\[data-theme=["']?night["']?\]/, `${path.basename(cssPath)} must include [data-theme="night"] selector`);

      // Must include Sailor HG Fairyfloss signature hex colors
      assert.match(content, /#3b364c/i, `${path.basename(cssPath)} must include background canvas #3b364c`);
      assert.match(content, /#5a5475/i, `${path.basename(cssPath)} must include card surface #5a5475`);
      assert.match(content, /#c5a3ff/i, `${path.basename(cssPath)} must include lavender accent #c5a3ff`);
      assert.match(content, /#ff857f/i, `${path.basename(cssPath)} must include coral accent #ff857f`);
    }
  });

  test('CSS stylesheets contain night mode overrides for status-jewel, status-dot, and calendar feed event items', () => {
    for (const cssPath of [hudCSSPath]) {
      const content = fs.readFileSync(cssPath, 'utf8');

      assert.match(content, /\[data-theme=["']?night["']?\]\s+\.status-jewel/, `${path.basename(cssPath)} must include [data-theme="night"] .status-jewel`);
      assert.match(content, /\[data-theme=["']?night["']?\]\s+\.status-dot/, `${path.basename(cssPath)} must include [data-theme="night"] .status-dot`);
      assert.match(content, /\[data-theme=["']?night["']?\]\s+\.header-video-btn/, `${path.basename(cssPath)} must include [data-theme="night"] .header-video-btn`);
      assert.match(content, /\[data-theme=["']?night["']?\]\s+\.weather-pill/, `${path.basename(cssPath)} must include [data-theme="night"] .weather-pill`);
      assert.match(content, /\[data-theme=["']?night["']?\]\s+\.calendar-event-item/, `${path.basename(cssPath)} must include [data-theme="night"] .calendar-event-item`);
      assert.match(content, /\[data-theme=["']?night["']?\]\s+\.calendar-day-card/, `${path.basename(cssPath)} must include [data-theme="night"] .calendar-day-card`);
      assert.match(content, /\[data-theme=["']?night["']?\]\s+\.event-time-col/, `${path.basename(cssPath)} must include [data-theme="night"] .event-time-col`);
      assert.match(content, /\[data-theme=["']?night["']?\]\s+\.calendar-header-chips/, `${path.basename(cssPath)} must include [data-theme="night"] .calendar-header-chips`);
    }
  });

  test('CSS stylesheets contain night mode overrides for calendar view switcher and task checkboxes', () => {
    for (const cssPath of [hudCSSPath]) {
      const content = fs.readFileSync(cssPath, 'utf8');

      assert.match(content, /\.cf-view-switcher/, `${path.basename(cssPath)} must include .cf-view-switcher`);
      assert.match(content, /\.cf-view-btn\.active/, `${path.basename(cssPath)} must include .cf-view-btn.active`);
      assert.match(content, /\[data-theme=["']?night["']?\]\s+\.task-checkbox-ring/, `${path.basename(cssPath)} must include [data-theme="night"] .task-checkbox-ring`);
      assert.match(content, /\[data-theme=["']?night["']?\]\s+\.task-checkbox-done/, `${path.basename(cssPath)} must include [data-theme="night"] .task-checkbox-done`);
      assert.match(content, /\[data-theme=["']?night["']?\]\s+\.tasks-icon-box/, `${path.basename(cssPath)} must include [data-theme="night"] .tasks-icon-box`);
    }
  });

  test('CSS stylesheets contain day and night mode tokens and overrides for calendar grid today highlight', () => {
    for (const cssPath of [hudCSSPath]) {
      const content = fs.readFileSync(cssPath, 'utf8');

      // Day mode tokens & styles
      assert.match(content, /--mm-cal-today-border:\s*var\(--mm-lavender/, `${path.basename(cssPath)} must define --mm-cal-today-border using lavender`);
      assert.match(content, /--mm-cal-today-bg:\s*var\(--mm-lavender-light/, `${path.basename(cssPath)} must define --mm-cal-today-bg using lavender light`);
      assert.match(content, /\.cf-day\.cf-today/, `${path.basename(cssPath)} must style .cf-day.cf-today`);
      assert.match(content, /\.cf-today-badge/, `${path.basename(cssPath)} must style .cf-today-badge`);

      // Night mode overrides
      assert.match(content, /\[data-theme=["']?night["']?\]\s+\.calendar-grid-cell\.today/, `${path.basename(cssPath)} must include [data-theme="night"] .calendar-grid-cell.today`);
      assert.match(content, /\[data-theme=["']?night["']?\]\s+\.cf-day\.cf-today/, `${path.basename(cssPath)} must include [data-theme="night"] .cf-day.cf-today`);
      assert.match(content, /\[data-theme=["']?night["']?\]\s+\.cf-month-day\.cf-today/, `${path.basename(cssPath)} must include [data-theme="night"] .cf-month-day.cf-today`);
      assert.match(content, /\[data-theme=["']?night["']?\]\s+\.cf-today-badge/, `${path.basename(cssPath)} must include [data-theme="night"] .cf-today-badge`);
    }
  });

  test('CSS stylesheets contain night mode overrides for fullscreen video overlay and presentation controls', () => {
    for (const cssPath of [hudCSSPath]) {
      const content = fs.readFileSync(cssPath, 'utf8');

      assert.ok(
        /\[data-theme=["']?night["']?\]\s+\.video-hud-nav-bar/.test(content) ||
        /\[data-theme=["']?night["']?\]\s+\.video-hud-capsule-top-left/.test(content),
        `${path.basename(cssPath)} must include [data-theme="night"] .video-hud-nav-bar or .video-hud-capsule-top-left`
      );
      assert.ok(
        /\[data-theme=["']?night["']?\]\s+\.video-hud-side-rail/.test(content) ||
        /\[data-theme=["']?night["']?\]\s+\.video-hud-capsule-top-right/.test(content),
        `${path.basename(cssPath)} must include [data-theme="night"] .video-hud-side-rail or .video-hud-capsule-top-right`
      );
      assert.ok(
        /\[data-theme=["']?night["']?\]\s+\.video-hud-controls/.test(content) ||
        /\[data-theme=["']?night["']?\]\s+\.video-hud-bottom-bar/.test(content),
        `${path.basename(cssPath)} must include [data-theme="night"] .video-hud-controls or .video-hud-bottom-bar`
      );
      assert.match(
        content,
        /\[data-theme=["']?night["']?\]\s+\.video-hud-title/,
        `${path.basename(cssPath)} must include [data-theme="night"] .video-hud-title`
      );
      assert.match(
        content,
        /\[data-theme=["']?night["']?\]\s+\.video-hud-btn/,
        `${path.basename(cssPath)} must include [data-theme="night"] .video-hud-btn`
      );
      assert.match(
        content,
        /\[data-theme=["']?night["']?\]\s+\.video-hud-play-primary/,
        `${path.basename(cssPath)} must include [data-theme="night"] .video-hud-play-primary`
      );
      assert.match(
        content,
        /\[data-theme=["']?night["']?\]\s+\.video-hud-volume-slider/,
        `${path.basename(cssPath)} must include [data-theme="night"] .video-hud-volume-slider`
      );
      assert.match(
        content,
        /\[data-theme=["']?night["']?\]\s+\.video-pip-slot/,
        `${path.basename(cssPath)} must include [data-theme="night"] .video-pip-slot`
      );
    }
  });
});

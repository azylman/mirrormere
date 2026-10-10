const { test, describe } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

describe('Touch Display Cursor Suppression', () => {
  const hudCSSPath = path.resolve(__dirname, '../static/css/hud.css');
  const defaultHudCSSPath = path.resolve(__dirname, '../../internal/render/default_hud.css');
  const customCSSPath = path.resolve(__dirname, '../../deploy/examples/custom.css');

  test('hud.css scopes cursor suppression to .mm-touch-display and .mm-touch-kiosk', () => {
    assert.ok(fs.existsSync(hudCSSPath), 'hud.css must exist');
    const content = fs.readFileSync(hudCSSPath, 'utf8');

    // Must NOT enforce cursor: none !important on global html, body
    assert.doesNotMatch(
      content,
      /html,\s*body\s*\{[^}]*cursor:\s*none/s,
      'hud.css must not define cursor: none on html, body'
    );

    // Must NOT enforce unconditional universal cursor: none
    assert.doesNotMatch(
      content,
      /(?:^|\n)\*,\s*\*::before,\s*\*::after\s*\{[^}]*cursor:\s*none/m,
      'hud.css must not define unconditional universal cursor: none'
    );

    // Must enforce cursor: none !important scoped to .mm-touch-display
    assert.match(
      content,
      /\.mm-touch-display,\s*\.mm-touch-display\s*\*,/s,
      'hud.css must define cursor: none !important scoped to .mm-touch-display'
    );
    assert.match(
      content,
      /\.mm-touch-kiosk,\s*\.mm-touch-kiosk\s*\*,/s,
      'hud.css must define cursor: none !important scoped to .mm-touch-kiosk'
    );
  });

  test('default_hud.css fallback scopes cursor suppression to .mm-touch-display and .mm-touch-kiosk', () => {
    assert.ok(fs.existsSync(defaultHudCSSPath), 'default_hud.css must exist');
    const content = fs.readFileSync(defaultHudCSSPath, 'utf8');

    assert.doesNotMatch(
      content,
      /html,\s*body\s*\{[^}]*cursor:\s*none/s,
      'default_hud.css must not define cursor: none on html, body'
    );

    assert.doesNotMatch(
      content,
      /(?:^|\n)\*,\s*\*::before,\s*\*::after\s*\{[^}]*cursor:\s*none/m,
      'default_hud.css must not define unconditional universal cursor: none'
    );

    assert.match(
      content,
      /\.mm-touch-display,\s*\.mm-touch-display\s*\*,/s,
      'default_hud.css must define cursor: none !important scoped to .mm-touch-display'
    );
    assert.match(
      content,
      /\.mm-touch-kiosk,\s*\.mm-touch-kiosk\s*\*,/s,
      'default_hud.css must define cursor: none !important scoped to .mm-touch-kiosk'
    );
  });

  test('deploy/examples/custom.css scopes cursor suppression to .mm-touch-display and .mm-touch-kiosk', () => {
    assert.ok(fs.existsSync(customCSSPath), 'deploy/examples/custom.css must exist');
    const content = fs.readFileSync(customCSSPath, 'utf8');

    assert.doesNotMatch(
      content,
      /html,\s*body\s*\{[^}]*cursor:\s*none/s,
      'custom.css must not define cursor: none on html, body'
    );

    assert.doesNotMatch(
      content,
      /(?:^|\n)\*,\s*\*::before,\s*\*::after\s*\{[^}]*cursor:\s*none/m,
      'custom.css must not define unconditional universal cursor: none'
    );

    assert.match(
      content,
      /\.mm-touch-display,\s*\.mm-touch-display\s*\*,/s,
      'custom.css must define cursor: none !important scoped to .mm-touch-display'
    );
    assert.match(
      content,
      /\.mm-touch-kiosk,\s*\.mm-touch-kiosk\s*\*,/s,
      'custom.css must define cursor: none !important scoped to .mm-touch-kiosk'
    );
  });

  test('display.js resolves query parameter cursor overrides', () => {
    const originalWindow = global.window;
    const originalDocument = global.document;

    try {
      const classList = new Set();
      global.document = {
        body: {
          classList: {
            add: (...args) => args.forEach(c => classList.add(c)),
            remove: (...args) => args.forEach(c => classList.delete(c)),
            contains: (c) => classList.has(c),
          },
        },
      };

      // ?hide_cursor=true adds mm-touch-display and mm-touch-kiosk
      global.window = {
        location: { search: '?hide_cursor=true' },
      };
      const display = require('../static/js/display.js');
      display.resolveCursorSuppression();
      assert.ok(global.document.body.classList.contains('mm-touch-display'), '?hide_cursor=true must add mm-touch-display');
      assert.ok(global.document.body.classList.contains('mm-touch-kiosk'), '?hide_cursor=true must add mm-touch-kiosk');

      // ?cursor=visible removes mm-touch-display and mm-touch-kiosk
      global.window.location.search = '?cursor=visible';
      display.resolveCursorSuppression();
      assert.ok(!global.document.body.classList.contains('mm-touch-display'), '?cursor=visible must remove mm-touch-display');
      assert.ok(!global.document.body.classList.contains('mm-touch-kiosk'), '?cursor=visible must remove mm-touch-kiosk');

      // ?cursor=none adds mm-touch-display
      global.window.location.search = '?cursor=none';
      display.resolveCursorSuppression();
      assert.ok(global.document.body.classList.contains('mm-touch-display'), '?cursor=none must add mm-touch-display');
      assert.ok(global.document.body.classList.contains('mm-touch-kiosk'), '?cursor=none must add mm-touch-kiosk');

      // ?hide_cursor=false removes mm-touch-display
      global.window.location.search = '?hide_cursor=false';
      display.resolveCursorSuppression();
      assert.ok(!global.document.body.classList.contains('mm-touch-display'), '?hide_cursor=false must remove mm-touch-display');
      assert.ok(!global.document.body.classList.contains('mm-touch-kiosk'), '?hide_cursor=false must remove mm-touch-kiosk');

      // ?kiosk=true is ignored (not used for cursor suppression)
      classList.clear();
      global.window.location.search = '?kiosk=true';
      display.resolveCursorSuppression();
      assert.ok(!global.document.body.classList.contains('mm-touch-display'), '?kiosk=true must not add mm-touch-display');
      assert.ok(!global.document.body.classList.contains('mm-touch-kiosk'), '?kiosk=true must not add mm-touch-kiosk');
    } finally {
      global.window = originalWindow;
      global.document = originalDocument;
    }
  });
});

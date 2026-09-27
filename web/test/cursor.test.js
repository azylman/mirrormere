const { test, describe } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

describe('Touch Kiosk Cursor Suppression (SPEC-010 §Touch Interaction & UI Polish)', () => {
  const hudCSSPath = path.resolve(__dirname, '../static/css/hud.css');
  const defaultHudCSSPath = path.resolve(__dirname, '../../internal/render/default_hud.css');
  const customCSSPath = path.resolve(__dirname, '../../deploy/examples/custom.css');

  test('hud.css enforces global cursor suppression without pointer or default regressions', () => {
    assert.ok(fs.existsSync(hudCSSPath), 'hud.css must exist');
    const content = fs.readFileSync(hudCSSPath, 'utf8');

    // Must enforce cursor: none !important on html/body and universal selectors
    assert.match(
      content,
      /html,\s*body\s*\{[^}]*cursor:\s*none\s*!important/s,
      'hud.css must define cursor: none !important on html, body'
    );
    assert.match(
      content,
      /\*,\s*\*::before,\s*\*::after\s*\{[^}]*cursor:\s*none\s*!important/s,
      'hud.css must define cursor: none !important on *, *::before, *::after'
    );

    // Must not retain cursor: pointer or cursor: default
    assert.doesNotMatch(content, /cursor:\s*pointer/i, 'hud.css must not contain cursor: pointer');
    assert.doesNotMatch(content, /cursor:\s*default/i, 'hud.css must not contain cursor: default');
  });

  test('default_hud.css fallback enforces global cursor suppression', () => {
    assert.ok(fs.existsSync(defaultHudCSSPath), 'default_hud.css must exist');
    const content = fs.readFileSync(defaultHudCSSPath, 'utf8');

    assert.match(
      content,
      /html,\s*body\s*\{[^}]*cursor:\s*none\s*!important/s,
      'default_hud.css must define cursor: none !important on html, body'
    );
    assert.match(
      content,
      /\*,\s*\*::before,\s*\*::after\s*\{[^}]*cursor:\s*none\s*!important/s,
      'default_hud.css must define cursor: none !important on *, *::before, *::after'
    );

    assert.doesNotMatch(content, /cursor:\s*pointer/i, 'default_hud.css must not contain cursor: pointer');
    assert.doesNotMatch(content, /cursor:\s*default/i, 'default_hud.css must not contain cursor: default');
  });

  test('deploy/examples/custom.css enforces global cursor suppression', () => {
    assert.ok(fs.existsSync(customCSSPath), 'deploy/examples/custom.css must exist');
    const content = fs.readFileSync(customCSSPath, 'utf8');

    assert.match(
      content,
      /html,\s*body\s*\{[^}]*cursor:\s*none\s*!important/s,
      'custom.css must define cursor: none !important on html, body'
    );
    assert.match(
      content,
      /\*,\s*\*::before,\s*\*::after\s*\{[^}]*cursor:\s*none\s*!important/s,
      'custom.css must define cursor: none !important on *, *::before, *::after'
    );

    assert.doesNotMatch(content, /cursor:\s*pointer/i, 'custom.css must not contain cursor: pointer');
  });
});

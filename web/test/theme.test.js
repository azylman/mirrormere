const { test, describe } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

describe('Fairyfloss Theme Mode Switching', () => {
  const hudCSSPath = path.resolve(__dirname, '../static/css/hud.css');
  const defaultHudCSSPath = path.resolve(__dirname, '../../internal/render/default_hud.css');
  const customCSSPath = path.resolve(__dirname, '../../deploy/examples/custom.css');

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
    for (const cssPath of [hudCSSPath, defaultHudCSSPath, customCSSPath]) {
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
});

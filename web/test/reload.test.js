const { test, describe, beforeEach } = require('node:test');
const assert = require('node:assert/strict');

describe('Display Deployment and Daily Maintenance Reload (SPEC-010 §3)', () => {
  let reloadedCount = 0;
  let mockWindow;
  let mockElements;

  function setupMockDOM(dataset = {}) {
    mockElements = {};
    reloadedCount = 0;
    mockWindow = {
      location: {
        reload() {
          reloadedCount++;
        },
      },
    };
    global.window = mockWindow;
    global.document = {
      getElementById(id) {
        if (!mockElements[id]) {
          mockElements[id] = {
            textContent: '',
            innerHTML: '',
            title: '',
            dataset: {},
            classList: {
              classes: new Set(),
              add(...cls) { cls.forEach(c => this.classes.add(c)); },
              remove(...cls) { cls.forEach(c => this.classes.delete(c)); },
              contains(c) { return this.classes.has(c); },
            },
          };
        }
        return mockElements[id];
      },
      body: {
        dataset: { ...dataset },
      },
      readyState: 'complete',
      addEventListener() {},
    };
  }

  beforeEach(() => {
    setupMockDOM({ timezone: 'America/Los_Angeles' });
    delete require.cache[require.resolve('../static/js/display.js')];
  });

  test('records initial boot_id on first system.status without reloading', () => {
    const display = require('../static/js/display.js');
    display.resetBootIdState();

    display.handleSystemStatus({
      online: true,
      config_status: 'ok',
      boot_id: 'boot-12345',
    });

    const state = display.getBootIdState();
    assert.equal(state.currentBootId, 'boot-12345');
    assert.equal(reloadedCount, 0, 'initial boot_id must not trigger reload');
  });

  test('does not reload when system.status carries identical boot_id', () => {
    const display = require('../static/js/display.js');
    display.resetBootIdState();

    display.handleSystemStatus({ online: true, boot_id: 'boot-12345' });
    display.handleSystemStatus({ online: true, boot_id: 'boot-12345' });

    assert.equal(reloadedCount, 0);
  });

  test('triggers reload when server deployment changes boot_id', () => {
    const display = require('../static/js/display.js');
    display.resetBootIdState();

    display.handleSystemStatus({ online: true, boot_id: 'boot-12345' });
    assert.equal(reloadedCount, 0);

    // Simulate new deployment
    display.handleSystemStatus({ online: true, boot_id: 'boot-67890' });
    assert.equal(reloadedCount, 1, 'deployment with new boot_id should trigger reload');
    assert.equal(display.getBootIdState().currentBootId, 'boot-67890');
  });

  test('throttles rapid consecutive deployment reloads (<30s cooldown)', () => {
    const display = require('../static/js/display.js');
    display.resetBootIdState();

    display.handleSystemStatus({ online: true, boot_id: 'boot-1' });
    display.handleSystemStatus({ online: true, boot_id: 'boot-2' });
    assert.equal(reloadedCount, 1);

    // Immediate change (e.g. backend crash loop) within 30s
    display.handleSystemStatus({ online: true, boot_id: 'boot-3' });
    assert.equal(reloadedCount, 1, 'rapid reload should be throttled');

    // Fast-forward lastReloadTimestamp beyond 30s
    display.setBootIdState({ lastReloadTimestamp: Date.now() - 31000 });

    display.handleSystemStatus({ online: true, boot_id: 'boot-4' });
    assert.equal(reloadedCount, 2, 'reload permitted after cooldown expires');
  });

  test('executes daily maintenance reload at 4:00 AM in household timezone', () => {
    const display = require('../static/js/display.js');
    display.resetBootIdState();

    // 4:00 AM PDT (UTC-7) on 2026-10-04 is 11:00 AM UTC
    const fourAmPDT = new Date('2026-10-04T11:00:00Z');
    const triggered = display.checkDailyMaintenanceReload(fourAmPDT);

    assert.equal(triggered, true);
    assert.equal(reloadedCount, 1);

    // Repeated call in the same minute should be deduplicated
    const secondCall = display.checkDailyMaintenanceReload(fourAmPDT);
    assert.equal(secondCall, false);
    assert.equal(reloadedCount, 1);

    // 4:01 AM should not trigger
    const fourOnePDT = new Date('2026-10-04T11:01:00Z');
    assert.equal(display.checkDailyMaintenanceReload(fourOnePDT), false);
    assert.equal(reloadedCount, 1);

    // Next day at 4:00 AM PDT (2026-10-05T11:00:00Z) should trigger
    const nextDayFourAmPDT = new Date('2026-10-05T11:00:00Z');
    assert.equal(display.checkDailyMaintenanceReload(nextDayFourAmPDT), true);
    assert.equal(reloadedCount, 2);
  });

  test('does not trigger daily maintenance reload at non-4:00 AM times', () => {
    const display = require('../static/js/display.js');
    display.resetBootIdState();

    // 3:59 AM PDT
    const threeFiftyNinePDT = new Date('2026-10-04T10:59:00Z');
    assert.equal(display.checkDailyMaintenanceReload(threeFiftyNinePDT), false);

    // 5:00 AM PDT
    const fiveAmPDT = new Date('2026-10-04T12:00:00Z');
    assert.equal(display.checkDailyMaintenanceReload(fiveAmPDT), false);

    assert.equal(reloadedCount, 0);
  });

  test('updateClock automatically calls checkDailyMaintenanceReload', () => {
    const display = require('../static/js/display.js');
    display.resetBootIdState();

    const fourAmPDT = new Date('2026-10-04T11:00:00Z');
    display.updateClock(fourAmPDT);

    assert.equal(reloadedCount, 1);
  });
});

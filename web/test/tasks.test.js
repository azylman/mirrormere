const { test, describe, beforeEach, afterEach } = require('node:test');
const assert = require('node:assert/strict');
const display = require('../static/js/display.js');

describe('Tasks Widget Touch Completion & Optimistic UI', () => {
  let originalFetch;
  let originalDocument;
  let originalWindow;
  let fetchCalls = [];

  beforeEach(() => {
    fetchCalls = [];
    originalFetch = global.fetch;
    originalDocument = global.document;
    originalWindow = global.window;

    global.fetch = async (url, options) => {
      fetchCalls.push({ url, options });
      return {
        ok: true,
        status: 200,
        json: async () => ({ status: 'ok' }),
      };
    };
  });

  afterEach(() => {
    global.fetch = originalFetch;
    global.document = originalDocument;
    global.window = originalWindow;
  });

  function createMockTaskElement({ id = 'task-101', listId = 'family-tasks', done = false, completing = false } = {}) {
    const classList = new Set();
    classList.add('task-item');
    if (done) classList.add('task-item-done');
    else classList.add('task-item-active');
    if (completing) classList.add('completing');

    const ring = {
      className: done ? 'task-checkbox-done' : 'task-checkbox-ring',
      innerHTML: done ? '<svg>done</svg>' : '',
      setAttribute: (k, v) => {},
    };

    const dataset = {
      taskId: id,
      listId: listId,
    };
    if (completing) dataset.completing = 'true';

    const element = {
      dataset,
      classList: {
        contains: (c) => classList.has(c),
        add: (c) => classList.add(c),
        remove: (c) => classList.delete(c),
        has: (c) => classList.has(c),
      },
      querySelector: (selector) => {
        if (selector === '.task-checkbox-ring' || selector === '.task-checkbox-done') {
          return ring;
        }
        return null;
      },
      closest: (selector) => {
        if (selector === '.task-item') return element;
        return null;
      },
    };

    return { element, ring };
  }

  test('successfully marks active task as done optimistically and issues POST request', async () => {
    const { element, ring } = createMockTaskElement({ id: 'task-abc', listId: 'chores' });

    assert.equal(element.classList.contains('task-item-active'), true);
    assert.equal(element.classList.contains('task-item-done'), false);

    await display.completeTask(element);

    // Verify request
    assert.equal(fetchCalls.length, 1);
    assert.equal(fetchCalls[0].url, 'api/lists/chores/items/task-abc/complete');
    assert.equal(fetchCalls[0].options.method, 'POST');

    // Verify final state
    assert.equal(element.classList.contains('task-item-done'), true);
    assert.equal(element.classList.contains('task-item-active'), false);
    assert.equal(ring.className, 'task-checkbox-done');
    assert.equal(ring.innerHTML.includes('<svg'), true);
  });

  test('ignores already-done tasks without making network requests', async () => {
    const { element } = createMockTaskElement({ id: 'task-done', listId: 'chores', done: true });

    await display.completeTask(element);

    assert.equal(fetchCalls.length, 0);
  });

  test('ignores items currently in-flight (debouncing double-taps)', async () => {
    const { element } = createMockTaskElement({ id: 'task-flying', listId: 'chores', completing: true });

    await display.completeTask(element);

    assert.equal(fetchCalls.length, 0);
  });

  test('rolls back optimistic state when network request fails', async () => {
    global.fetch = async () => ({
      ok: false,
      status: 502,
      json: async () => ({ status: 'error', error: 'upstream failed' }),
    });

    const { element, ring } = createMockTaskElement({ id: 'task-fail', listId: 'groceries' });

    await assert.rejects(async () => {
      await display.completeTask(element);
    }, /HTTP 502/);

    // Verify state rolled back
    assert.equal(element.classList.contains('task-item-active'), true);
    assert.equal(element.classList.contains('task-item-done'), false);
    assert.equal(ring.className, 'task-checkbox-ring');
    assert.equal(ring.innerHTML, '');
    assert.equal(element.dataset.completing, undefined);
  });

  test('syncInFlightTasks re-applies completed styling to incoming DOM elements during active requests', async () => {
    const inFlightSet = display.getInFlightCompletions();
    inFlightSet.add('task-inflight');

    try {
      const { element, ring } = createMockTaskElement({ id: 'task-inflight', listId: 'groceries', done: false });
      const mockContainer = {
        querySelectorAll: (sel) => {
          if (sel === '.task-item') return [element];
          return [];
        },
      };

      display.syncInFlightTasks(mockContainer);

      assert.equal(element.classList.contains('task-item-done'), true);
      assert.equal(ring.className, 'task-checkbox-done');
      assert.equal(element.dataset.completing, 'true');
    } finally {
      inFlightSet.delete('task-inflight');
    }
  });

  test('handles URL escaping properly for special characters in task and list IDs', async () => {
    const { element } = createMockTaskElement({ id: 'task 123', listId: 'my list' });

    await display.completeTask(element);

    assert.equal(fetchCalls.length, 1);
    assert.equal(fetchCalls[0].url, 'api/lists/my%20list/items/task%20123/complete');
  });
});

import { act, fireEvent, render } from '@testing-library/react';
import { Profiler } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useDisplayStore } from '../../store';
import { renderWidget, seedWidget, widgetMeta } from '../../test-utils/widget';
import { TASK_UNDO_GRACE_MS, TasksWidget } from './TasksWidget';

/** Seeds tasks widget `id` (healthy, 3x2); `timestamp` overrides seedWidget's fixed entry timestamp. */
function seedTasks(id: string, data: unknown, config: Record<string, unknown> = {}, timestamp?: string, state = 'healthy') {
  seedWidget(id, widgetMeta('tasks', config, [3, 2]), data, state);
  if (timestamp !== undefined) {
    useDisplayStore.setState((s) => ({ widgets: { [id]: { ...s.widgets[id]!, timestamp } } }));
  }
}

describe('TasksWidget', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  // wantText: [selector, exact textContent]; wantCount: [selector, match count]; wantAbsent: selectors that must not match.
  const tests: {
    name: string;
    data: unknown;
    config?: Record<string, unknown>;
    state?: string;
    wantText: [string, string][];
    wantCount: [string, number][];
    wantAbsent: string[];
  }[] = [
    {
      name: 'renders loading state when data is unhydrated ({}) and omits header when unnamed',
      data: {},
      wantText: [['.widget-tasks .tasks-loading', 'Loading checklist...']],
      wantCount: [['.widget-tasks', 1]],
      wantAbsent: ['.tasks-header'],
    },
    {
      name: 'renders loading state when data is undefined',
      data: undefined,
      wantText: [['.widget-tasks .tasks-loading', 'Loading checklist...']],
      wantCount: [],
      wantAbsent: ['.tasks-header'],
    },
    {
      name: 'renders loading state with custom title header from config.list_name',
      data: {},
      config: { list_name: 'Chores Manifest' },
      wantText: [['.tasks-title', 'Chores Manifest']],
      wantCount: [],
      wantAbsent: [],
    },
    {
      name: 'renders loading state with custom title header from config.todoist.project_name',
      data: {},
      config: { todoist: { project_name: 'Work Tasks' } },
      wantText: [['.tasks-title', 'Work Tasks']],
      wantCount: [],
      wantAbsent: [],
    },
    {
      name: 'renders loading state with custom title header from config.title',
      data: {},
      config: { title: 'Groceries' },
      wantText: [['.widget-tasks .tasks-title', 'Groceries'], ['.widget-tasks .tasks-loading', 'Loading checklist...']],
      wantCount: [['.widget-tasks .tasks-header', 1]],
      wantAbsent: [],
    },
    {
      name: 'renders empty state when hydrated with empty items array ([])',
      data: { list: { name: 'Weekend Chores' }, items: [] },
      wantText: [
        ['.tasks-title', 'Weekend Chores'],
        ['.tasks-summary', '0 left'],
        ['.empty-title', 'All tasks completed!'],
        ['.empty-subtitle', 'Time to put your feet up'],
      ],
      wantCount: [['.widget-tasks .tasks-header', 1], ['.widget-tasks .tasks-empty', 1]],
      wantAbsent: [],
    },
    {
      name: 'renders error banner when widget state is error',
      data: {},
      state: 'error',
      wantText: [['.widget-error-banner', '⚠Widget Error']],
      wantCount: [],
      wantAbsent: [],
    },
    {
      name: 'renders individual tags (section only, assignee only, dueDate only) cleanly',
      data: {
        items: [
          { id: 'tag-1', title: 'Only Section', done: false, section: 'Errands' },
          { id: 'tag-2', title: 'Only Assignee', done: false, assignee: 'beta' },
          { id: 'tag-3', title: 'Only Due Date', done: false, due_date: 'Tomorrow' },
        ],
      },
      wantText: [
        ['[data-item-id="tag-1"] .task-tag-section', '#Errands'],
        ['[data-item-id="tag-2"] .task-tag-assignee', '@beta'],
        ['[data-item-id="tag-3"] .task-tag-due', 'Tomorrow'],
      ],
      wantCount: [],
      wantAbsent: [
        '[data-item-id="tag-1"] .task-tag-assignee',
        '[data-item-id="tag-1"] .task-tag-due',
        '[data-item-id="tag-2"] .task-tag-section',
        '[data-item-id="tag-2"] .task-tag-due',
        '[data-item-id="tag-3"] .task-tag-section',
        '[data-item-id="tag-3"] .task-tag-assignee',
      ],
    },
    {
      name: 'handles PascalCase Items with ID/Title/Done, config.ListName and the item-${idx} fallback id',
      data: { Items: [{ ID: 'p-1', Title: 'Pascal Item 1', Done: false }, { Title: 'No ID Item', Done: true }] },
      config: { ListName: 'Chores' },
      wantText: [
        ['.tasks-title', 'Chores'],
        ['[data-item-id="p-1"] .task-title', 'Pascal Item 1'],
        ['[data-item-id="item-1"] .task-title', 'No ID Item'],
      ],
      wantCount: [],
      wantAbsent: [],
    },
    {
      name: 'handles data.list.items with config.name',
      data: { list: { items: [{ id: 'n-1', title: 'Nested Item 1', done: false }] } },
      config: { name: 'Nested List' },
      wantText: [['.tasks-title', 'Nested List'], ['[data-item-id="n-1"] .task-title', 'Nested Item 1']],
      wantCount: [],
      wantAbsent: [],
    },
    {
      name: 'handles data.List.Items and data.List.Name',
      data: { List: { Name: 'Pascal Nested', Items: [{ ID: 'pn-1', Title: 'Pascal Nested Item', Done: false }] } },
      wantText: [['.tasks-title', 'Pascal Nested'], ['[data-item-id="pn-1"] .task-title', 'Pascal Nested Item']],
      wantCount: [],
      wantAbsent: [],
    },
    {
      name: 'handles data.list.name as the title',
      data: { list: { name: 'Data Title List', items: [{ id: 'dt-1', title: 'Item DT', done: false }] } },
      wantText: [['.tasks-title', 'Data Title List']],
      wantCount: [],
      wantAbsent: [],
    },
  ];

  for (const tc of tests) {
    it(tc.name, () => {
      seedTasks('tasks-1', tc.data, tc.config, undefined, tc.state);
      const { container } = renderWidget(TasksWidget, 'tasks-1', 'tasks');
      for (const [selector, text] of tc.wantText) {
        expect(container.querySelector(selector)?.textContent, selector).toBe(text);
      }
      for (const [selector, count] of tc.wantCount) {
        expect(container.querySelectorAll(selector).length, selector).toBe(count);
      }
      for (const selector of tc.wantAbsent) {
        expect(container.querySelector(selector), selector).toBeNull();
      }
    });
  }

  it('renders populated task items with assignees, sections, due dates, and summary', () => {
    seedTasks('tasks-pop', {
      items: [
        {
          id: 'item-1',
          list_id: 'list-123',
          title: 'Clean kitchen counter',
          done: false,
          section: 'Kitchen',
          assignee: 'alpha',
          due_date: '2026-11-01',
        },
        { id: 'item-2', list_id: 'list-123', title: 'Water plants', done: true },
      ],
    });

    const { container } = renderWidget(TasksWidget, 'tasks-pop', 'tasks');
    const root = container.querySelector('.widget-tasks');
    expect(root?.querySelector('.tasks-title')?.textContent).toBe('Tasks');
    expect(root?.querySelector('.tasks-summary')?.textContent).toContain('1 left');

    const items = root?.querySelectorAll('.task-item');
    expect(items?.length).toBe(2);

    // Item 1: active
    const item1 = items?.[0] as HTMLElement;
    expect(item1.className).toContain('task-item-active');
    expect(item1.dataset.itemId).toBe('item-1');
    expect(item1.dataset.listId).toBe('list-123');
    expect(item1.querySelector('.task-title')?.textContent).toBe('Clean kitchen counter');
    expect(item1.querySelector('.task-tag-section')?.textContent).toBe('#Kitchen');
    expect(item1.querySelector('.task-tag-assignee')?.textContent).toBe('@alpha');
    expect(item1.querySelector('.task-tag-due')?.textContent).toBe('2026-11-01');
    expect(item1.querySelector('.task-checkbox-ring')).not.toBeNull();

    // Item 2: done
    const item2 = items?.[1] as HTMLElement;
    expect(item2.className).toContain('task-item-done');
    expect(item2.querySelector('.task-checkbox-done')).not.toBeNull();
  });

  it('handles optimistic task completion, clears data-completing on success, and ignores already completed tasks', async () => {
    let resolveFetch!: (res: Response) => void;
    const fetchPromise = new Promise<Response>((resolve) => {
      resolveFetch = resolve;
    });
    const fetchMock = vi.fn().mockImplementation(() => fetchPromise);
    vi.stubGlobal('fetch', fetchMock);

    seedTasks('tasks-opt', {
      list: { id: 'list-99' },
      items: [
        { id: 't-1', title: 'Sweep floor', done: false },
        { id: 't-2', title: 'Take out trash', done: true },
      ],
    });

    const { container } = renderWidget(TasksWidget, 'tasks-opt', 'tasks');
    const item1 = container.querySelector('[data-item-id="t-1"]') as HTMLElement;
    const item2 = container.querySelector('[data-item-id="t-2"]') as HTMLElement;
    const summary = container.querySelector('.tasks-summary');

    expect(summary?.textContent).toContain('1 left');

    // Clicking already-done item does nothing
    fireEvent.click(item2);
    expect(fetchMock).not.toHaveBeenCalled();

    // Click active item: immediately optimistic
    fireEvent.click(item1);

    expect(fetchMock).not.toHaveBeenCalled();
    expect(item1.dataset.completing).toBe('true');
    expect(item1.dataset.pendingUndo).toBe('true');
    expect(item1.className).toContain('task-item-done');
    expect(item1.className).toContain('pending-undo');
    expect(summary?.textContent).toContain('All done');

    act(() => {
      vi.advanceTimersByTime(TASK_UNDO_GRACE_MS);
    });

    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock).toHaveBeenCalledWith('api/lists/list-99/items/t-1/complete', {
      method: 'POST',
      headers: { Accept: 'application/json' },
    });

    expect(item1.dataset.completing).toBe('true');
    expect(item1.dataset.pendingUndo).toBeUndefined();
    expect(item1.className).toContain('task-item-done');
    expect(summary?.textContent).toContain('All done');

    // Clicking again while in-flight does not send another request
    fireEvent.click(item1);
    expect(fetchMock).toHaveBeenCalledTimes(1);

    // Resolve network request successfully
    await act(async () => {
      resolveFetch(new Response('{}', { status: 200 }));
    });

    // In-flight completing attribute cleared, remains done
    expect(item1.dataset.completing).toBeUndefined();
    expect(item1.className).toContain('task-item-done');
    expect(summary?.textContent).toContain('All done');
  });

  it('preserves optimistic completed state across concurrent SSE widget.update events while in-flight', async () => {
    let resolveFetch!: (res: Response) => void;
    const fetchPromise = new Promise<Response>((resolve) => {
      resolveFetch = resolve;
    });
    vi.stubGlobal('fetch', vi.fn().mockImplementation(() => fetchPromise));

    seedTasks('tasks-sse', { items: [{ id: 'task-concurrent', title: 'Buy milk', done: false }] });

    const { container } = renderWidget(TasksWidget, 'tasks-sse', 'tasks');
    const item = container.querySelector('[data-item-id="task-concurrent"]') as HTMLElement;

    // Trigger completion
    fireEvent.click(item);
    act(() => {
      vi.advanceTimersByTime(TASK_UNDO_GRACE_MS);
    });
    expect(item.dataset.completing).toBe('true');
    expect(item.className).toContain('task-item-done');

    // Simulate concurrent SSE widget.update arriving with old uncompleted item data
    act(() => {
      useDisplayStore.getState().applyEvents([
        {
          type: 'widget.update',
          data: {
            widget_id: 'tasks-sse',
            timestamp: '2026-10-31T12:01:00Z',
            state: 'healthy',
            data: {
              items: [{ id: 'task-concurrent', title: 'Buy milk', done: false }],
            },
          },
        },
      ]);
    });

    // Optimistic done and completing state must be preserved
    const refreshedItem = container.querySelector('[data-item-id="task-concurrent"]') as HTMLElement;
    expect(refreshedItem.dataset.completing).toBe('true');
    expect(refreshedItem.className).toContain('task-item-done');

    // Now resolve fetch
    await act(async () => {
      resolveFetch(new Response('{}', { status: 200 }));
    });

    expect(refreshedItem.dataset.completing).toBeUndefined();
    expect(refreshedItem.className).toContain('task-item-done');
  });

  it('clears optimisticDone when server snapshot updates and reflects reopened or reset recurring tasks', async () => {
    let resolveFetch!: (res: Response) => void;
    const fetchPromise = new Promise<Response>((resolve) => {
      resolveFetch = resolve;
    });
    vi.stubGlobal('fetch', vi.fn().mockImplementation(() => fetchPromise));

    seedTasks('tasks-reopen', { items: [{ id: 'task-recur', title: 'Daily vitamins', done: false }] });

    const { container } = renderWidget(TasksWidget, 'tasks-reopen', 'tasks');
    const item = container.querySelector('[data-item-id="task-recur"]') as HTMLElement;

    // Complete the task
    fireEvent.click(item);
    act(() => {
      vi.advanceTimersByTime(TASK_UNDO_GRACE_MS);
    });
    expect(item.className).toContain('task-item-done');
    expect(item.dataset.completing).toBe('true');

    // Resolve fetch
    await act(async () => {
      resolveFetch(new Response('{}', { status: 200 }));
    });
    expect(item.dataset.completing).toBeUndefined();
    expect(item.className).toContain('task-item-done');

    // Server snapshot arrives confirming the task was completed
    act(() => {
      useDisplayStore.getState().applyEvents([
        {
          type: 'widget.update',
          data: {
            widget_id: 'tasks-reopen',
            timestamp: '2026-10-31T12:01:00Z',
            state: 'healthy',
            data: {
              items: [{ id: 'task-recur', title: 'Daily vitamins', done: true }],
            },
          },
        },
      ]);
    });
    expect(item.className).toContain('task-item-done');

    // Later, recurring task resets / is reopened upstream with done: false
    act(() => {
      useDisplayStore.getState().applyEvents([
        {
          type: 'widget.update',
          data: {
            widget_id: 'tasks-reopen',
            timestamp: '2026-10-31T12:05:00Z',
            state: 'healthy',
            data: {
              items: [{ id: 'task-recur', title: 'Daily vitamins', done: false }],
            },
          },
        },
      ]);
    });

    // The task item should no longer be stuck in optimistic done
    const refreshedItem = container.querySelector('[data-item-id="task-recur"]') as HTMLElement;
    expect(refreshedItem.className).toContain('task-item-active');
  });

  it('rolls back optimistic state and data-completing on 500 response or network error', async () => {
    let rejectFetch!: (err: Error) => void;
    const fetchPromise = new Promise<Response>((_, reject) => {
      rejectFetch = reject;
    });
    vi.stubGlobal('fetch', vi.fn().mockImplementation(() => fetchPromise));

    seedTasks('tasks-rollback', { items: [{ id: 'item-fail', title: 'Mow lawn', done: false }] });

    const { container } = renderWidget(TasksWidget, 'tasks-rollback', 'tasks');
    const item = container.querySelector('[data-item-id="item-fail"]') as HTMLElement;
    const summary = container.querySelector('.tasks-summary');

    expect(summary?.textContent).toContain('1 left');

    fireEvent.click(item);
    act(() => {
      vi.advanceTimersByTime(TASK_UNDO_GRACE_MS);
    });
    expect(item.dataset.completing).toBe('true');
    expect(item.className).toContain('task-item-done');
    expect(summary?.textContent).toContain('All done');

    // Network failure
    await act(async () => {
      rejectFetch(new Error('Network error'));
    });

    // Rolled back to active
    expect(item.dataset.completing).toBeUndefined();
    expect(item.className).toContain('task-item-active');
    expect(summary?.textContent).toContain('1 left');
  });

  it('rolls back optimistic state on non-200 HTTP response', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('Server Error', { status: 500 })));

    seedTasks('tasks-500', { items: [{ id: 'item-500', title: 'Fix bug', done: false }] });

    const { container } = renderWidget(TasksWidget, 'tasks-500', 'tasks');
    const item = container.querySelector('[data-item-id="item-500"]') as HTMLElement;

    fireEvent.click(item);
    await act(async () => {
      vi.advanceTimersByTime(TASK_UNDO_GRACE_MS);
    });

    expect(item.dataset.completing).toBeUndefined();
    expect(item.className).toContain('task-item-active');
  });

  it('enters pending-undo state with undo badge on first tap and delays fetch until grace window elapses', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);

    seedTasks('tasks-undo-1', { items: [{ id: 'task-1', title: 'Buy groceries', done: false }] });

    const { container } = renderWidget(TasksWidget, 'tasks-undo-1', 'tasks');
    const item = container.querySelector('[data-item-id="task-1"]') as HTMLElement;
    const summary = container.querySelector('.tasks-summary');

    expect(summary?.textContent).toContain('1 left');
    expect(item.className).toContain('task-item-active');
    expect(item.querySelector('.task-undo-badge')).toBeNull();

    // First tap: enters pending-undo state immediately
    fireEvent.click(item);

    expect(item.className).toContain('task-item-done');
    expect(item.className).toContain('pending-undo');
    expect(item.dataset.completing).toBe('true');
    expect(item.dataset.pendingUndo).toBe('true');
    expect(item.querySelector('.task-undo-badge')).not.toBeNull();
    expect(item.querySelector('.task-undo-badge')?.textContent).toBe('Undo');
    expect(summary?.textContent).toContain('All done');

    // fetch must not be called yet before grace window elapses
    expect(fetchMock).not.toHaveBeenCalled();

    // Advance halfway through grace window: still no fetch
    act(() => {
      vi.advanceTimersByTime(TASK_UNDO_GRACE_MS / 2);
    });
    expect(fetchMock).not.toHaveBeenCalled();

    // Advance remainder of grace window: fetch is dispatched
    await act(async () => {
      vi.advanceTimersByTime(TASK_UNDO_GRACE_MS / 2);
    });
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock).toHaveBeenCalledWith('api/lists/tasks-undo-1/items/task-1/complete', {
      method: 'POST',
      headers: { Accept: 'application/json' },
    });
    expect(item.dataset.pendingUndo).toBeUndefined();
    expect(item.querySelector('.task-undo-badge')).toBeNull();
  });

  it('cancels completion and restores active state when tapped a second time during grace window', () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);

    seedTasks('tasks-undo-cancel', { items: [{ id: 'task-cancel', title: 'Call dentist', done: false }] });

    const { container } = renderWidget(TasksWidget, 'tasks-undo-cancel', 'tasks');
    const item = container.querySelector('[data-item-id="task-cancel"]') as HTMLElement;
    const summary = container.querySelector('.tasks-summary');

    expect(summary?.textContent).toContain('1 left');

    // First tap: optimistic done with pending-undo badge
    fireEvent.click(item);
    expect(item.dataset.pendingUndo).toBe('true');
    expect(summary?.textContent).toContain('All done');

    // Advance partially into grace window
    act(() => {
      vi.advanceTimersByTime(1000);
    });

    // Second tap: undo completion
    fireEvent.click(item);

    expect(item.className).toContain('task-item-active');
    expect(item.className).not.toContain('task-item-done');
    expect(item.className).not.toContain('pending-undo');
    expect(item.dataset.completing).toBeUndefined();
    expect(item.dataset.pendingUndo).toBeUndefined();
    expect(item.querySelector('.task-undo-badge')).toBeNull();
    expect(summary?.textContent).toContain('1 left');
    expect(fetchMock).not.toHaveBeenCalled();

    // Advance well past initial grace window: fetch must never be called
    act(() => {
      vi.advanceTimersByTime(TASK_UNDO_GRACE_MS * 2);
    });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('clears grace timer on unmount and prevents fetch from dispatching', () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);

    seedTasks('tasks-unmount', { items: [{ id: 'task-unmount', title: 'Unmount test', done: false }] });

    const { container, unmount } = renderWidget(TasksWidget, 'tasks-unmount', 'tasks');
    const item = container.querySelector('[data-item-id="task-unmount"]') as HTMLElement;

    // First tap starts grace timer
    fireEvent.click(item);
    expect(item.dataset.pendingUndo).toBe('true');
    expect(fetchMock).not.toHaveBeenCalled();

    // Unmount while timer is pending
    unmount();

    // Advance time past grace window
    act(() => {
      vi.advanceTimersByTime(TASK_UNDO_GRACE_MS * 2);
    });

    // fetch must not be dispatched
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('memoizes store updates: React Profiler verifies widget.update for tasks-1 commits ONLY tasks-1', () => {
    useDisplayStore.setState({
      widgets: {
        'tasks-1': {
          meta: widgetMeta('tasks', {}, [3, 1]),
          state: 'healthy',
          data: { items: [{ id: 't1', title: 'Task 1', done: false }] },
          timestamp: '2026-10-31T12:00:00Z',
        },
        'tasks-2': {
          meta: { ...widgetMeta('tasks', {}, [3, 1]), origin: [3, 0] },
          state: 'healthy',
          data: { items: [{ id: 't2', title: 'Task 2', done: false }] },
          timestamp: '2026-10-31T12:00:00Z',
        },
      },
    });

    const onRender1 = vi.fn();
    const onRender2 = vi.fn();

    render(
      <div>
        <Profiler id="tasks-1-profiler" onRender={onRender1}>
          <TasksWidget id="tasks-1" type="tasks" />
        </Profiler>
        <Profiler id="tasks-2-profiler" onRender={onRender2}>
          <TasksWidget id="tasks-2" type="tasks" />
        </Profiler>
      </div>,
    );

    // Initial mount commits both once
    expect(onRender1).toHaveBeenCalledTimes(1);
    expect(onRender2).toHaveBeenCalledTimes(1);

    // Dispatch widget.update targeting tasks-1 ONLY
    act(() => {
      useDisplayStore.getState().applyEvents([
        {
          type: 'widget.update',
          data: {
            widget_id: 'tasks-1',
            timestamp: '2026-10-31T12:01:00Z',
            state: 'healthy',
            data: {
              items: [
                { id: 't1', title: 'Task 1', done: true },
                { id: 't1-new', title: 'Task 1 New', done: false },
              ],
            },
          },
        },
      ]);
    });

    // tasks-1 committed a second time, tasks-2 remained untouched
    expect(onRender1).toHaveBeenCalledTimes(2);
    expect(onRender2).toHaveBeenCalledTimes(1);
  });

  it('handles item without any title string and reconciles optimistic done when server marks item done', () => {
    seedTasks('tasks-empty-title', { items: [{ id: 'et-1', done: false }] });

    const { container } = renderWidget(TasksWidget, 'tasks-empty-title', 'tasks');
    expect(container.querySelector('.task-title')?.textContent).toBe('');

    const item = container.querySelector('[data-item-id="et-1"]')!;
    fireEvent.click(item);

    act(() => {
      seedTasks('tasks-empty-title', { items: [{ id: 'et-1', done: true }] }, {}, '2026-10-31T12:01:00Z');
    });

    expect(container.querySelector('.task-item-done')).not.toBeNull();
  });

  it('clears optimistic state when item is removed from server list during update', () => {
    seedTasks('tasks-rem', { items: [{ id: 'rem-1', done: false }] });

    const { container } = renderWidget(TasksWidget, 'tasks-rem', 'tasks');
    const item = container.querySelector('[data-item-id="rem-1"]')!;
    fireEvent.click(item);

    act(() => {
      vi.advanceTimersByTime(TASK_UNDO_GRACE_MS + 10);
    });

    act(() => {
      seedTasks('tasks-rem', { items: [] }, {}, '2026-10-31T12:02:00Z');
    });

    expect(container.querySelector('[data-item-id="rem-1"]')).toBeNull();
  });

  it('handles keyboard navigation on task items with Enter and Space', () => {
    seedTasks('tasks-kb', { items: [{ id: 'kb-1', title: 'Keyboard task', done: false }] });

    const { container } = renderWidget(TasksWidget, 'tasks-kb', 'tasks');
    const item = container.querySelector('[data-item-id="kb-1"]') as HTMLElement;
    expect(item.getAttribute('role')).toBe('button');
    expect(item.getAttribute('tabindex')).toBe('0');

    // Trigger Enter
    fireEvent.keyDown(item, { key: 'Enter' });
    expect(item.getAttribute('data-pending-undo')).toBe('true');

    // Trigger Space to cancel/undo
    fireEvent.keyDown(item, { key: ' ' });
    expect(item.getAttribute('data-pending-undo')).toBeNull();

    // Trigger other key (no-op)
    fireEvent.keyDown(item, { key: 'Escape' });
  });
});

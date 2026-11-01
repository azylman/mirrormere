import { memo, useCallback, useState } from 'react';
import { useWidget } from '../../store/hooks';
import type { WidgetComponentProps } from '../registry';
import { tasksStats } from '../format';

export interface TaskItemRecord {
  id?: string | number;
  ID?: string | number;
  list_id?: string | number;
  ListID?: string | number;
  listId?: string | number;
  title?: string;
  Title?: string;
  done?: boolean;
  Done?: boolean;
  section?: string;
  Section?: string;
  assignee?: string;
  Assignee?: string;
  due_date?: string;
  DueDate?: string;
  due?: string;
  Due?: string;
  [key: string]: unknown;
}

export interface EffectiveTaskItem extends TaskItemRecord {
  itemId: string;
  done: boolean;
  isCompleting: boolean;
}

/**
 * Native React widget component for task checklists.
 * Ports widgets/tasks/views/widget.html.
 */
export const TasksWidget = memo(function TasksWidget({ id }: WidgetComponentProps) {
  const entry = useWidget(id);
  const config = entry?.meta?.config as Record<string, unknown> | undefined;
  const data = entry?.data as Record<string, unknown> | undefined;
  const isError = entry?.state === 'error';

  const isHydrated = Boolean(
    data && ('items' in data || 'Items' in data || 'list' in data || 'List' in data),
  );

  const customTitle =
    (typeof config?.list_name === 'string' &&
      config.list_name.trim() !== '' &&
      config.list_name.trim()) ||
    (typeof config?.ListName === 'string' &&
      config.ListName.trim() !== '' &&
      config.ListName.trim()) ||
    (typeof (config?.todoist as Record<string, unknown> | undefined)?.project_name === 'string' &&
      ((config?.todoist as Record<string, unknown>).project_name as string).trim() !== '' &&
      ((config?.todoist as Record<string, unknown>).project_name as string).trim()) ||
    (typeof config?.name === 'string' && config.name.trim() !== '' && config.name.trim()) ||
    (typeof config?.title === 'string' && config.title.trim() !== '' && config.title.trim()) ||
    (typeof (data?.list as Record<string, unknown> | undefined)?.name === 'string' &&
      ((data?.list as Record<string, unknown>).name as string).trim() !== '' &&
      ((data?.list as Record<string, unknown>).name as string).trim()) ||
    (typeof (data?.List as Record<string, unknown> | undefined)?.Name === 'string' &&
      ((data?.List as Record<string, unknown>).Name as string).trim() !== '' &&
      ((data?.List as Record<string, unknown>).Name as string).trim()) ||
    '';

  const rawItems: TaskItemRecord[] = Array.isArray(data?.items)
    ? (data?.items as TaskItemRecord[])
    : Array.isArray(data?.Items)
      ? (data?.Items as TaskItemRecord[])
      : Array.isArray((data?.list as Record<string, unknown> | undefined)?.items)
        ? ((data?.list as Record<string, unknown>).items as TaskItemRecord[])
        : Array.isArray((data?.List as Record<string, unknown> | undefined)?.Items)
          ? ((data?.List as Record<string, unknown>).Items as TaskItemRecord[])
          : [];

  const [inFlight, setInFlight] = useState<Record<string, boolean>>({});
  const [optimisticDone, setOptimisticDone] = useState<Record<string, boolean>>({});
  const [prevSnapshot, setPrevSnapshot] = useState({ data, timestamp: entry?.timestamp });

  if (entry?.timestamp !== prevSnapshot.timestamp || data !== prevSnapshot.data) {
    setPrevSnapshot({ data, timestamp: entry?.timestamp });
    if (Object.keys(optimisticDone).length > 0) {
      let changed = false;
      const next = { ...optimisticDone };
      for (const itemId of Object.keys(optimisticDone)) {
        const item = rawItems.find((it) => String(it.id ?? it.ID ?? '') === itemId);
        const serverDone = item ? Boolean(item.done ?? item.Done) : false;
        if (serverDone || !inFlight[itemId]) {
          delete next[itemId];
          changed = true;
        }
      }
      if (changed) {
        setOptimisticDone(next);
      }
    }
  }

  const effectiveItems: EffectiveTaskItem[] = rawItems.map((item, idx) => {
    const itemId = String(item.id ?? item.ID ?? `item-${idx}`);
    const isOptimistic = Boolean(optimisticDone[itemId]);
    const serverDone = Boolean(item.done ?? item.Done);
    const isDone = isOptimistic || serverDone;
    const isCompleting = Boolean(inFlight[itemId]);
    return {
      ...item,
      itemId,
      done: isDone,
      isCompleting,
    };
  });

  const stats = tasksStats(effectiveItems);

  const handleItemClick = useCallback(
    async (itemId: string, listId: string, currentDone: boolean) => {
      if (currentDone || inFlight[itemId]) return;

      setInFlight((prev) => ({ ...prev, [itemId]: true }));
      setOptimisticDone((prev) => ({ ...prev, [itemId]: true }));

      try {
        const resp = await fetch(
          `api/lists/${encodeURIComponent(listId)}/items/${encodeURIComponent(itemId)}/complete`,
          {
            method: 'POST',
            headers: { Accept: 'application/json' },
          },
        );
        if (!resp.ok) {
          throw new Error(`HTTP ${resp.status}`);
        }
        setInFlight((prev) => {
          const next = { ...prev };
          delete next[itemId];
          return next;
        });
      } catch {
        setInFlight((prev) => {
          const next = { ...prev };
          delete next[itemId];
          return next;
        });
        setOptimisticDone((prev) => {
          const next = { ...prev };
          delete next[itemId];
          return next;
        });
      }
    },
    [inFlight],
  );

  return (
    <div className="widget-tasks">
      {isError && (
        <div className="widget-error-banner">
          <span className="error-icon">⚠</span>
          <span>Widget Error</span>
        </div>
      )}

      {!isHydrated ? (
        <>
          {customTitle ? (
            <div className="tasks-header">
              <div className="tasks-header-left">
                <div className="tasks-icon-box" aria-hidden="true">
                  <svg
                    width="15"
                    height="15"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="3"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                  >
                    <polyline points="20 6 9 17 4 12" />
                  </svg>
                </div>
                <span className="tasks-title">{customTitle}</span>
              </div>
            </div>
          ) : null}
          <div className="tasks-loading">
            <span>Loading checklist...</span>
          </div>
        </>
      ) : (
        <>
          <div className="tasks-header">
            <div className="tasks-header-left">
              <div className="tasks-icon-box" aria-hidden="true">
                <svg
                  width="15"
                  height="15"
                  viewBox="0 0 24 24"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="3"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                >
                  <polyline points="20 6 9 17 4 12" />
                </svg>
              </div>
              <span className="tasks-title">{customTitle || 'Tasks'}</span>
            </div>
            <span className="tasks-progress tasks-summary">
              {stats.remaining === 0 && stats.total > 0 ? 'All done' : `${stats.remaining} left`}
              <svg
                width="9"
                height="9"
                viewBox="0 0 24 24"
                fill="currentColor"
                className="tasks-sparkle-icon"
                aria-hidden="true"
              >
                <path d="M12 2L14.5 9.5L22 12L14.5 14.5L12 22L9.5 14.5L2 12L9.5 9.5L12 2Z" />
              </svg>
            </span>
          </div>

          {effectiveItems.length === 0 ? (
            <div className="tasks-empty">
              <div className="tasks-empty-icon" aria-hidden="true">
                <svg
                  width="26"
                  height="26"
                  viewBox="0 0 24 24"
                  fill="none"
                  stroke="var(--mm-mint)"
                  strokeWidth="2.5"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                >
                  <path d="M22 11.08V12a10 10 0 1 1-5.93-9.14" />
                  <polyline points="22 4 12 14.01 9 11.01" />
                </svg>
              </div>
              <span className="empty-title">All tasks completed!</span>
              <span className="empty-subtitle">Time to put your feet up</span>
            </div>
          ) : (
            <div className="tasks-items-container">
              <div className="tasks-list">
                {effectiveItems.map((item) => {
                  const itemId = item.itemId;
                  const listId = String(
                    item.list_id ??
                      item.ListID ??
                      item.listId ??
                      (data?.list as Record<string, unknown> | undefined)?.id ??
                      (data?.List as Record<string, unknown> | undefined)?.ID ??
                      id,
                  );
                  const isDone = item.done;
                  const isCompleting = item.isCompleting;
                  const title = String(item.title ?? item.Title ?? '');
                  const section = item.section ?? item.Section;
                  const assignee = item.assignee ?? item.Assignee;
                  const dueDate = item.due_date ?? item.DueDate ?? item.due ?? item.Due;
                  const hasTags = Boolean(section || assignee || dueDate);

                  return (
                    <div
                      key={itemId}
                      className={`task-item ${isDone ? 'task-item-done' : 'task-item-active'}${isCompleting ? ' completing' : ''}`}
                      data-item-id={itemId}
                      data-task-id={itemId}
                      data-list-id={listId}
                      data-completing={isCompleting ? 'true' : undefined}
                      onClick={() => handleItemClick(itemId, listId, isDone)}
                    >
                      <div className="task-check-glyph">
                        {isDone ? (
                          <div className="task-checkbox-done" aria-hidden="true">
                            <svg
                              width="11"
                              height="11"
                              viewBox="0 0 24 24"
                              fill="none"
                              stroke="currentColor"
                              strokeWidth="3.5"
                              strokeLinecap="round"
                              strokeLinejoin="round"
                            >
                              <polyline points="20 6 9 17 4 12" />
                            </svg>
                          </div>
                        ) : (
                          <div className="task-checkbox-ring" aria-hidden="true" />
                        )}
                      </div>
                      <div className="task-body">
                        <span className="task-title">{title}</span>
                        {hasTags && (
                          <div className="task-tags">
                            {section ? (
                              <span className="task-tag-section">#{String(section)}</span>
                            ) : null}
                            {assignee ? (
                              <span className="task-tag-assignee">@{String(assignee)}</span>
                            ) : null}
                            {dueDate ? (
                              <span className="task-tag-due">{String(dueDate)}</span>
                            ) : null}
                          </div>
                        )}
                      </div>
                    </div>
                  );
                })}
              </div>
            </div>
          )}
        </>
      )}
    </div>
  );
});

# ADR: Google Tasks Position Key and Hierarchical Subtask Ordering

## Context & Problem Statement
In Google Tasks API v1, tasks do not return in a guaranteed display order. Instead, Google assigns each task a lexicographical `position` string (e.g. `"00000000000000000001"`) for ordering among siblings, and a `parent` task ID indicating whether the item is a subtask under another parent task.

Previously, `internal/tasks/adapters/gtasks.go` set each item's integer `Position` to its index in the raw API response (`len(items)`), while completely ignoring the `position` string and omitting `parent`. This led to two critical defects (Issue #268):
1. **Visual Ordering Divergence**: Tasks rendered on Mirrormere displays in an arbitrary order different from the user's Google Tasks app.
2. **Spurious Ingestion Churn**: Any non-deterministic variation in API response page ordering between routine polling cycles caused `SyncList` to detect changed item positions (`changed=true`), triggering unnecessary SQLite transactions and spurious SSE `widget.update` pushes.
3. **Subtask Disconnect**: Subtasks were intermixed randomly among top-level tasks with no connection to their parent task.

## Decision
1. **Decode `parent` in `gtasksItem`**:
   - Added `Parent string json:"parent"` to the internal `gtasksItem` model.
2. **Hierarchical Tree Ordering (`orderGTasks`)**:
   - Filtered out soft-deleted items (`gt.Deleted == true`).
   - Grouped active tasks into top-level tasks (`Parent == ""` or parent non-existent/deleted) and parent-to-children mappings (`childrenByParent[parentID]`).
   - Sorted top-level tasks lexicographically by `Position` string.
   - Sorted children under each parent lexicographically by sibling `Position` string.
   - Flattened the tree in depth-first order: emitting each top-level task immediately followed by its subtasks, recursively.
   - Included cyclic reference safeguards to guarantee termination on malformed payloads.
3. **Assign Sequential Integer Positions**:
   - Assigned deterministic integer positions (`0, 1, 2, ...`) following the flattened tree sequence, matching `SPEC-008`'s flat `ListItem` model.

## Consequences & Alternatives Considered
- **Skipping Subtasks vs Flattening**: The flat `ListItem` schema in `SPEC-008` does not include `ParentID`. Skipping subtasks was considered, but rejected because user checklist items (e.g. sub-items on a grocery list) would be silently dropped from smart wall displays. Flattening subtasks directly under their respective parent in sibling order preserves both visibility and logical grouping.
- **Title Prefixing**: Prefixing subtask titles with indentation or bullets was rejected to keep the core data model clean and prevent roundtrip title pollution or false difference detection in `SyncList`.

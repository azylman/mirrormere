# ADR: External Task List Ingestion Adapters (HTTP & Google Tasks)

## Context
Mirrormere operates as a strictly read-only ambient display surface for task lists, grocery checklists, and household chores (SPEC-008). In Chunk 3.4, Mirrormere established the embedded local SQLite storage engine (`internal/tasks/store.go`) and read-only inspection endpoint (`GET /api/lists/{list_id}/items`).

Households keep their authoritative task lists in various external services: one household uses Google Tasks, while another maintains an internal household list service exposed via HTTP endpoints. Chunk 3.5 requires pluggable ingestion adapters (`internal/tasks/adapters/`) to ingest external task lists into the local SQLite store, detect changes, and dispatch real-time SSE updates (`widget.update`) across primary and consumer task widgets.

## Decision
1. **Pluggable Adapter Interface (`internal/tasks/adapters/adapter.go`)**:
   - Standardized `Adapter` interface providing `Name() string` and `FetchList(ctx context.Context) (*tasks.List, []tasks.ListItem, error)`.
   - Hardcoded Slowloris protection: `MaxPayloadBytes = 2 * 1024 * 1024` (2MB ceiling) enforced via `io.LimitReader` on all HTTP requests.
   - Standardized sentinel errors: `ErrUnauthorized` (for 401/403 status codes), `ErrUpstreamNotFound` (for 404), and `ErrPayloadTooLarge`.
   - `UpstreamHTTPError` implementing `HTTPStatusCoder { StatusCode() int }`, integrating seamlessly with Mirrormere's `IsNetworkOrServerError` exponential backoff classifier.

2. **HTTP Adapter (`internal/tasks/adapters/http.go`)**:
   - Ingests generic JSON task lists from a remote HTTP endpoint.
   - Dual endpoint compatibility:
     - Combined payload: decodes `{ id, name, source, sections, updated_at, items: [...] }` in a single HTTP roundtrip.
     - Split endpoints: decodes `GET {base_url}` for `List` metadata and issues secondary `GET {base_url}/items` for `[]ListItem` when `items` is not embedded in the base response.
     - Direct array: handles endpoints returning raw `[]ListItem` directly from `{base_url}`.
   - In-memory bearer token authorization with automatic URL credential and query parameter sanitization.

3. **Google Tasks Adapter (`internal/tasks/adapters/gtasks.go`)**:
   - Ingests task lists from the Google Tasks API v1 (`https://tasks.googleapis.com`).
   - Retrieves list metadata via `GET /tasks/v1/users/@me/lists/{tasklist_id}`, falling back to declared `list_name` or `tasklist_id`.
   - Retrieves task items via `GET /tasks/v1/lists/{tasklist_id}/tasks?showCompleted=true&showHidden=true&maxResults=100`.
   - Follows pagination tokens (`nextPageToken` -> `pageToken`) up to a 10-page ceiling (1,000 items max) with cyclic token loop detection.
   - Filters out soft-deleted tasks (`item.Deleted == true`).
   - Normalizes task status: `status == "completed"` -> `Done = true`, else `false`.
   - Normalizes due dates: extracts `YYYY-MM-DD` from RFC 3339 timestamps (`item.Due`) with length verification (`len >= 10`) and format validation.
   - Hierarchical position ordering: decodes Google's `position` and `parent` fields, sorting top-level tasks by lexicographical `position` and flattening subtasks immediately after their parent in sibling position order before assigning contiguous integer positions (0, 1, 2, ...). Orphaned subtasks whose parent is deleted or absent fall back to top-level positioning.

4. **Single-Transaction SQLite Reconciliation (`SQLiteStore.SyncList`)**:
   - Reconciles external lists and items within a single serialized transaction (`tx, err := s.db.BeginTx(ctx, nil)`).
   - Change detection: compares list metadata and existing item attributes (`title`, `done`, `section`, `position`, `assignee`, `due_date`) before modifying disk. If unchanged, commits read-only transaction and returns `changed=false`, completely preventing unnecessary flash disk writes.
   - Relational ordering: updates `lists` before `list_items` to preserve foreign key constraints, and deletes obsolete items missing from the upstream payload.

5. **Reactive Real-Time Change Notifier (`tasks.ChangeNotifier`)**:
   - Thread-safe pub-sub dispatching mechanism (`internal/tasks/notifier.go`).
   - Non-blocking notification dispatch (`select { case ch <- struct{}{}: default: }`) preventing slow listeners from stalling ingestion.
   - `TasksProvider.Subscribe(ctx, eventSink)` registers for list change events, allowing secondary consumer widgets (e.g. `compact-chores`) to immediately push fresh snapshots over SSE without spinning up redundant polling loops.

## Security & Zero Plaintext Token Invariant
- Authentication tokens are resolved in memory via `InitOptions.GetSecret("http.token")` or `InitOptions.GetSecret("gtasks.token")`, matching environment variables defined in `token_env`.
- Plaintext tokens are strictly forbidden from being written to disk, database tables, or `.git` configurations.
- All URL strings included in error messages pass through `sanitizeURL`, which strips credentials and query parameters.

## Consequences & Alternatives Considered
- **Direct Database Upsert vs Reconciled Sync**: Direct blind upserts without difference detection would cause continuous WAL disk churn on embedded flash nodes. Implementing atomic change detection in `SyncList` ensures disk writes only occur when data actually changes.
- **Polling vs Push**: Google Tasks API does not support webhooks for personal accounts; polling on configured intervals (default 120s) with SWR caching satisfies appliance requirements while respecting Google rate limits.
- **Subtask Hierarchy & Flat Display Model**: Google Tasks represents tasks hierarchically via `parent` and `position` fields, whereas `SPEC-008` defines a flat `ListItem` model with integer positions. Skipping subtasks was rejected to prevent data loss on household displays. Instead, subtasks are flattened in depth-first tree order directly following their respective parent task. This preserves user-defined ordering and avoids false-positive `SyncList` change detection triggers caused by arbitrary API response page ordering.
- **Graceful Degradation**: On upstream network failure or 401/500 errors, `TasksProvider.Fetch` returns an error, allowing the `ProviderCoordinator` SWR cache to transition to `StateDegraded` while preserving the Last Known Good (LKG) cached data on screen.

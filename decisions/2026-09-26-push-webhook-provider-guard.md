# ADR: Push Webhook Provider Guard & Non-HTTP 409 Conflict Rejection

## Context & Problem Statement
The realtime push webhook (`POST /api/widgets/{widget_id}/push`) was designed in SPEC-006 §2 to allow external sidecars and local HTTP services to immediately push domain state to HTTP-provider widgets (SPEC-003 §3) without waiting for background polling cycles. 

Previously, `ProviderCoordinator.recordPushLocked` rejected only `type: tasks` with HTTP 409 Conflict (`ErrListWidgetPushForbidden`). Any other configured widget was permitted to ingest pushes without validation because non-HTTP built-in widgets (`calendar-agenda`, `weather-forecast`, `photo-carousel`, `spacer`) do not declare a `response_schema`. Consequently, arbitrary unvalidated JSON objects could be injected directly into the SWR cache, state sink, and SSE event bus, corrupting widget templates and breaking ambient display rendering until the next polling cycle or reload.

## Decision
1. **Strict Provider Name Gating**:
   - In `ProviderCoordinator.recordPushLocked`, resolve the target widget's provider via `resolveProviderName(targetWidget, c.snapshot)`.
   - If the resolved provider is not `"http"`, reject the push with HTTP 409 Conflict.
   - For `tasks` widgets (`targetWidget.Type == "tasks"` or `providerName == "tasks"`), retain the task-specific error message via `ErrListWidgetPushForbidden`.
   - For all other non-HTTP widgets, return typed `ErrNonHTTPWidgetPushForbidden`.

2. **HTTP Handler Error Mapping**:
   - In `PushHandler.PostWidgetPush`, map `ErrNonHTTPWidgetPushForbidden` to HTTP 409 Conflict (`http.StatusConflict`) with JSON error body:
     `{"status": "error", "error": "cannot push state to widget '<id>'; push webhook is strictly limited to widgets using the http provider"}`.

3. **Specification & Schema Alignment**:
   - Updated SPEC-006 §2 "Scope & Source-of-Truth Guard" documenting that calling `POST /api/widgets/{widget_id}/push` on any widget whose resolved provider is not `http` is rejected with `409 Conflict`.
   - Updated `api/openapi.yaml` 409 response description for `/api/widgets/{widget_id}/push`.

## Technical Rationale
- **Authoritative Provider Ownership**: Built-in in-process providers manage their own domain lifecycles, structured Go data models, and caching strategies. Allowing arbitrary push mutations violates the single-writer invariant and causes temporary state corruption.
- **RFC 9110 §15.5.10 Compliance**: An HTTP 409 Conflict status indicates that the request could not be processed because of a conflict in the current state of the resource. A widget configured with an internal pull driver is in conflict with external push mutations.
- **Automatic Validation Assurance**: Because `response_schema` is mandatory for custom `http` provider widgets under SPEC-003 §3 and LKGC stage 4 validation, restricting pushes exclusively to `http` widgets guarantees every accepted push is validated against its declared schema before entering the cache.

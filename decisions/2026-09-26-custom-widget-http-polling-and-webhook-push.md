# Architecture Decision Record: Custom Widget Data Paths (HTTP Polling and Webhook Push)

- **Date:** 2026-09-26
- **Status:** Accepted
- **Context:** Implementing Issue #228 on `azylman/mirrormere` (SPEC-013 Phase 3, Chunk 3.2), fulfilling SPEC-003 §3, SPEC-006 §2, §3, and SPEC-008 §5.

---

## 1. Problem Statement
Custom widgets in Mirrormere require two primary data ingestion paths:
1. **Outbound HTTP Polling (`provider: "http"`)**: For widgets whose upstream data source exposes a pull API (such as custom sidecars or local REST endpoints).
2. **Inbound Webhook Push (`POST /api/widgets/{widget_id}/push`)**: For widgets whose data is emitted by external event sources, webhooks, or automation scripts (such as Home Assistant webhooks or IoT sensors).

Prior to Chunk 3.2:
- The `http` provider was not registered or implemented in `internal/provider`, preventing widgets configured with `provider: "http"` from polling upstream endpoints.
- No webhook ingestion endpoint existed in the HTTP server or OpenAPI specification to receive pushed widget state.
- Inbound payloads lacked schema validation against package manifests (`response_schema` using JSON Schema Draft 2020-12), risking cache corruption or UI runtime errors from malformed data.
- List-backed widgets (`type: "tasks"`) lacked enforcement against push attempts, violating the read-only contract for task-list providers.

---

## 2. Decision & Architecture

### A. Generic HTTP Polling Provider (`internal/provider/http.go`)
- Registered `"http"` in `NewRegistry()` alongside `"spacer"`.
- Supports `POST` (default) and `GET` methods.
- On `POST`, serializes strictly the instance's domain configuration (`p.domainConfig`), omitting all top-level framework settings per SPEC-003 §3.2.
- Injects standard telemetry headers:
  - `X-Widget-ID: {widget_id}`
  - `X-Widget-Type: {widget_type}`
  - `X-Widget-Dimensions: {w}x{h}` (when dimensions are configured)
  - `Authorization: Bearer {token}` (when token or token secret is configured)
  - `Content-Type: application/json` (on POST)
  - `Accept: application/json`
- Implements 1MB body limit (`io.LimitReader`) for Slowloris defense.
- Validates the response body against the widget's manifest `response_schema` using `santhosh-tekuri/jsonschema/v6` (Draft 2020-12).
- Non-2xx HTTP responses or schema validation failures trigger provider errors, moving the SWR cache entry into `degraded` state and preserving LKG data.

### B. Webhook Push Ingestion (`internal/provider/handler.go` & `server.go`)
- Exposed `POST /api/widgets/{widget_id}/push` routed via `PushHandler`.
- Supports CORS preflight (`OPTIONS` returns `204 No Content`, `Access-Control-Allow-Origin: *`, `Access-Control-Allow-Methods: POST, OPTIONS`).
- Non-POST requests return `405 Method Not Allowed` with `Allow: POST, OPTIONS`.
- Request body limited to 1MB (`io.LimitReader`), requires valid JSON object.
- Rejects push to list-backed widgets (`type: "tasks"`) with `409 Conflict` per SPEC-006 §2.
- Unknown widget IDs return `404 Not Found`.
- Schema validation failures return `400 Bad Request` with structured error details.
- Successful push updates the SWR cache (`RecordPush`) and state sink (`SetWidgetState`), broadcasts `widget.update` over the SSE event bus, and returns `200 OK` with `{"status":"ok","widget_id":...,"updated_at":...}`.

### C. Provider Coordinator Synchronization (`internal/provider/coordinator.go`)
- Implemented `PushWidgetData(ctx, widgetID, data)` on `ProviderCoordinator`.
- Compiled JSON schemas are cached thread-safely in `schemas map[string]*jsonschema.Schema` protected by `schemaMu sync.RWMutex`, and flushed on live configuration reload (`UpdateConfig`).
- Mutual exclusion during push: `recordPushLocked` holds `c.mu.RLock()` to atomically read the widget definition, validate schema, and record push data into SWR cache and state sink, preventing race conditions with live configuration reload.
- SSE event publishing (`c.broadcastUpdate`) executes outside the mutex lock to prevent event bus backpressure from blocking coordinator operations.

### D. OpenAPI Contract & Codegen Alignment
- Updated `api/openapi.yaml` with the `POST /api/widgets/{widget_id}/push` path, response schema, and error schemas.
- Regenerated `internal/api/api.gen.go` using `go generate ./...` (`oapi-codegen`), ensuring zero contract drift.

---

## 3. Consequences & Invariants Maintained

1. **Zero Schema Drift**: OpenAPI specification matches the Go server interface and handler implementations exactly.
2. **Slowloris Defense**: Inbound pushes and outbound polling responses enforce 1MB body reading ceilings.
3. **Draft 2020-12 Schema Compliance**: All ingested payloads are rigorously validated against `response_schema`.
4. **Hermetic Testing**: All tests use isolated HTTP servers (`httptest.Server`) and in-memory coordinator mocks with zero sleeps or network calls.
5. **Coverage Floor**: All modified packages exceed the required 95.0% statement coverage floor (`internal/provider`: 95.1%, `internal/api`: 95.7%, `internal/server`: 98.4%).

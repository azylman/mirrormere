# Architecture Decision Record: Phase 1 OpenAPI Contract & Codegen

- **Date:** 2026-09-25
- **Status:** Accepted
- **Context:** Implementing Phase 1, Chunk 1.2 (#163) of SPEC-013 on `azylman/mirrormere`.

---

## 1. Scoped Phase 1 OpenAPI 3.1 Specification

### Description of Issue / Inconsistency
Earlier exploratory sketches in the repository outlined dozens of prospective endpoints across video ingest, voice interaction, and rotation coordinator before those subsystems were implemented. Attempting to define all future endpoints in `api/openapi.yaml` at once forces premature Go stubs or unimplemented placeholder handlers across the codebase.

### Decision Made
Scoped `api/openapi.yaml` (OpenAPI 3.1) strictly to the four Phase 1 foundation endpoints:
- `GET /healthz`: Primary healthcheck probe returning `{"status": "ok"}` (`HealthResponse`).
- `GET /health`: Healthcheck alias returning `HealthResponse`.
- `GET /api/widgets/{widget_id}/render`: SSR HTML fragment endpoint returning `text/html; charset=utf-8` on 200, or `ErrorResponse` on 404 per SPEC-006 §Realtime SSR Fragment Delivery.
- `GET /widget-types/{type}/assets/{path}`: Static package asset route returning binary file stream on 200, or `ErrorResponse` on 400 (path traversal) / 404 (not found) per SPEC-003 §1.

Added canonical SSE JSON schemas (Draft 2020-12) for hot-reloading:
- `api/schemas/widget.reload.json`: Emitted on widget template/manifest edits.
- `api/schemas/style.reload.json`: Emitted on custom stylesheet updates.

### Technical Rationale & References
- Prevents dead code, premature stubs, and incomplete handlers from cluttering the codebase.
- Serves as the authoritative single source of truth for Phase 1.
- References: `specs/001-architecture-overview.md` §1 & §2, `specs/003-widget-contract.md` §1, `specs/006-realtime-comms-and-mutations.md` §2.H–I & §Realtime SSR Fragment Delivery.

---

## 2. Standard Library `net/http` Codegen (`oapi-codegen`)

### Description of Issue / Inconsistency
`oapi-codegen` supports numerous framework server generators (Chi, Gin, Echo, Fiber, Std-HTTP). Mirrormere strictly uses standard library Go `net/http` (`http.ServeMux`) with Slowloris timeouts, avoiding third-party routing dependencies.

### Decision Made
Configured `internal/api/oapi-codegen.yaml` to generate models (`types`) and standard library Go 1.22+ `http.ServeMux` server interfaces (`std-http`):
- `ServerInterface` provides compile-time interface enforcement for server handlers.
- `ServerInterfaceWrapper` provides automated URL parameter extraction via Go 1.22 `r.PathValue()`.
- Maintained a dedicated `internal/api/api_test.go` suite testing handlers, parameter binding error branches, custom middlewares, and error types, ensuring `>= 95.0%` statement coverage.

### Technical Rationale & References
- Preserves standard library purity and zero-framework design.
- Enforces compile-time type safety across all route contracts.
- References: `specs/001-architecture-overview.md` §2, `specs/013-implementation-roadmap.md` §Phase 1 Chunk 1.2.

---

## 3. Zero-Drift Codegen Gate in Pre-Flight Verification

### Description of Issue / Inconsistency
Manual generation workflows risk drift between `api/openapi.yaml` and generated code in `internal/api/`.

### Decision Made
Integrated `ensure_oapi_codegen` and `run_codegen_drift` into `scripts/verify.sh` ahead of linting and coverage checks:
- Self-heals by resolving `oapi-codegen` from PATH, `$GOPATH/bin`, or `$HOME/go/bin`, auto-installing v2.4.1 if missing and `go` is available.
- Executes `go generate ./...` and verifies `git diff --exit-code internal/api/`.
- Fails immediately if generated code is uncommitted or drifted.

### Technical Rationale & References
- Guarantees zero drift between OpenAPI specifications and Go interfaces across local environments and CI.
- References: `specs/001-architecture-overview.md` §3.

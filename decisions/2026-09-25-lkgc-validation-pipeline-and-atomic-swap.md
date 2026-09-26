# Architecture Decision Record: LKGC 6-Stage Validation Pipeline & Atomic Swap

- **Date:** 2026-09-25
- **Status:** Accepted
- **Context:** Implementing Phase 1, Chunk 1.6 (#167) of SPEC-013 on `azylman/mirrormere`.

---

## 1. Hybrid Synchronization Architecture for LKGC State Management

### Description of Issue / Inconsistency
Live configuration reloads in an ambient smart display must guarantee zero downtime and zero visual disruption. Relying purely on `sync.RWMutex` risks writer starvation or read lock contention during high-frequency display renders, while relying purely on lock-free primitives leaves reload execution open to race conditions when bursts of filesystem events trigger parallel reloads.

### Decision Made
Adopted a **Hybrid Synchronization Architecture**:
- `reloadMu sync.Mutex`: Serializes reload executions so that candidate YAML parsing, package loading, schema compiling, and layout solving happen strictly sequentially with zero race conditions.
- `current atomic.Pointer[Snapshot]`: Holds the currently active immutable `Snapshot` (`Config`, `Layout`, `Packages`, `LoadedAt`), providing lock-free and wait-free reads for HTTP handlers, display renderers, and SSE streams.
- `status atomic.Pointer[Status]`: Tracks current telemetry status (`config_status: "ok"|"error"`, `config_error: *string`, `last_checked`, `last_success`), enabling lock-free state reads for `/healthz` and initial SSE hydration.

### Technical Rationale & References
- Eliminates read contention on the display rendering path.
- Serializes reload pipelines, preventing status race conditions where an outdated failure could overwrite a successful reload.
- References: `specs/012-live-config-reload-and-lkgc.md` §2-3, `specs/006-realtime-comms-and-mutations.md` §3.

---

## 2. Unified Immutable Snapshot Architecture

### Description of Issue / Inconsistency
If configuration state, layout placement, and package manifests are stored or updated in separate fields, concurrent reader goroutines can observe a desynchronized intermediate state (e.g. an updated layout referencing widgets from an old configuration).

### Decision Made
Bundled all validated artifacts into a single immutable `Snapshot` struct:
```go
type Snapshot struct {
    Config   *Config
    Layout   *layout.Layout
    Packages map[string]*domain.Package
    LoadedAt time.Time
}
```
The manager publishes the new state in a single atomic pointer swap (`m.current.Store(newSnapshot)`).

### Technical Rationale & References
- Guarantees reader consistency across all components: layout assignments always align with the active widget configurations and resolved package metadata.
- References: `specs/012-live-config-reload-and-lkgc.md` §2.

---

## 3. Strict 6-Stage Validation Pipeline Orchestration

### Description of Issue / Inconsistency
SPEC-012 defines a 6-stage validation pipeline. The candidate configuration must be validated end-to-end against all stages before any in-memory state or background workers are modified.

### Decision Made
Implemented `ValidatePipeline(data []byte, loader PackageLoader, getenv func(string) string) (*Snapshot, error)` executing:
1. **Stage 1 (YAML Syntax & AST Structure)**: YAML parsing, prohibition of `${VAR}` interpolation, structural key validation (no disallowed keys like `screens`, `providers`, root `header`), top-level whitelist (`timezone`, `display`).
2. **Stage 2 (Core Instance Schemas)**: Timezone validation, 6x2 grid bounds, rotation parameters, header weather, widget ID uniqueness, dimension bounds, refresh intervals, and `ResolveEnv(getenv)`.
3. **Stage 3 (Package Existence & Completeness)**: Resolves packages via `loader.LoadPackage()`, validates manifests (forbidding schema `default` keywords), verifies supported dimensions if declared, applies manifest `default_dimensions` and refresh intervals.
4. **Stage 4 (Manifest `config_schema` Validation)**: Evaluates `w.Config` against `pkg.Manifest.ConfigSchema` using standard JSON Schema Draft 2020-12 via `github.com/santhosh-tekuri/jsonschema/v6`.
5. **Stage 5 (Domain & Source-of-Truth Rules)**: Enforces SPEC-008 tasks/lists integrity rules (canonical `list_id` defaulting, explicit source mandate requiring at least one primary source per `list_id`, conflict validation for duplicate sources, HTTP/GTasks source config validation) and SPEC-003 HTTP provider endpoint validations.
6. **Stage 6 (6×2 Bin-Packing Layout Solver)**: Solves the 6×2 grid via `layout.Solve()`, adhering to the full-tiling screen invariant with deficit/spacer guidance.

### Technical Rationale & References
- Guarantees invalid configurations fail fast without mutating running state.
- Emits structured diagnostic logs matching SPEC-012: `[config.reloader] error="live config validation failed: %s; retaining LKGC"`.
- References: `specs/012-live-config-reload-and-lkgc.md` §2, `specs/003-widget-contract.md`, `specs/005-screen-layout-and-rotation.md`, `specs/008-tasks-and-lists.md`.

# Architecture Decision Record: Internal Layout & Docker Compose Foundation

- **Date:** 2026-09-25
- **Status:** Accepted
- **Context:** Implementing Phase 1, Chunk 1.1 (#162) of SPEC-013 on `azylman/mirrormere`.

---

## 1. Relocation of `pkg/server` to `internal/server`

### Description of Issue / Inconsistency
The initial HTTP server bootstrap and Slowloris test suite were landed under `pkg/server`. However, SPEC-001 §Repository Layout & Monorepo Architecture mandates:
> **Application Encapsulation (`internal/`)**: The Go server is an application binary, not an exported public library. All Go packages reside under `internal/` to prevent external import coupling and grant full freedom for internal refactoring.

Peer review from Amos also noted that SPEC-001 requires all Go application packages to live under `internal/`.

### Decision Made
Moved `pkg/server/server.go` and `pkg/server/server_test.go` to `internal/server/server.go` and `internal/server/server_test.go`. Updated `cmd/server/main.go` and `internal/server/server_test.go` imports to reference `github.com/azylman/mirrormere/internal/server`. Removed the empty `pkg/` directory.

### Technical Rationale & References
- Adheres strictly to Go standard project layout conventions for private binary packages.
- Prevents external Go modules from importing internal daemon internals.
- References: `specs/001-architecture-overview.md` §Monorepo Boundaries & Rules, Invariant 1.

---

## 2. Compose Service Naming & Build Context Resolution

### Description of Issue / Inconsistency
Issue #162 text refers to "defining the `mirrormere` daemon service", whereas SPEC-001 §Standardized Port Allocations and SPEC-002 §Reference Deployment Topology declare the core service as `mirrormere-core` with container name `mirrormere-core`. Furthermore, sidecars (`cast-watcher`, `eink-renderer`) defined in SPEC-002 target `CORE_URL=http://mirrormere-core:8080`.

Additionally, with `compose.yml` located in `deploy/compose.yml`, Docker Compose must reliably resolve the repository root `Dockerfile` whether invoked as `docker compose -f deploy/compose.yml config` from repository root or when run directly inside `deploy/`.

### Decision Made
1. Set the service name and container name in `deploy/compose.yml` to `mirrormere-core`, preserving internal DNS compatibility with future sidecars.
2. Specified `build: { context: .., dockerfile: Dockerfile }` so the build context always resolves to the repository root containing the multi-stage `Dockerfile`.
3. Configured explicit non-root user `10001:10001`, `security_opt: [no-new-privileges:true]`, read-only volume mount `./config:/config:ro`, read-write `./data:/data`, and native Busybox HTTP healthcheck probe.

### Technical Rationale & References
- Prevents inter-container networking breakage when `sidecars/cast-watcher` and `sidecars/eink-renderer` land in later phases.
- Validates cleanly with `docker compose config` across both root and subdirectory execution paths.
- References: `specs/001-architecture-overview.md` §Standardized Port Allocations, `specs/002-hardware-profiles.md` lines 44–66.

---

## 3. Configuration Template Structure (`deploy/examples/config.yaml`)

### Description of Issue / Inconsistency
Early exploratory documentation and prompt summaries mentioned `screens:` and `providers:` blocks. However, SPEC-003 §Declarative Widget Instance Configuration explicitly establishes:
> Mirrormere uses a generic, decoupled configuration model where each widget instance declared in `display.widgets` is a self-contained unit managing both its data ingestion parameters and its UI presentation settings. There is no disconnected top-level providers block; all sync cadences, data sources, and display parameters live directly in the widget declaration.

Furthermore, SPEC-005 specifies that rotation screens are computed dynamically by the 2D bitmask layout solver, rather than statically declared by operators. Finally, SPEC-012 §6 explicitly prohibits arbitrary `${VAR}` string interpolation in favor of explicit `*_env` keys.

### Decision Made
Structured `deploy/examples/config.yaml` to follow the authoritative SPEC-003 and SPEC-005 schema:
- Top-level `timezone: "America/Los_Angeles"` (IANA timezone).
- `display.rotation` and `display.header`.
- `display.widgets` array containing self-contained instances (`calendar-agenda`, `weather-forecast`, `tasks`, `photo-carousel`, and `spacer`).
- Zero top-level `providers:` or `screens:` blocks.
- Secret resolution using explicit `url_env: FAMILY_CALENDAR_URL`.
- Geometry dimensioned to pack cleanly across two 6×2 screens (12 cells each) with 100% bitmask coverage.

### Technical Rationale & References
- Enforces single source of truth and prevents user confusion from deprecated configuration structures.
- Guarantees that the baseline configuration passes all 6 stages of the LKGC validation pipeline.
- References: `specs/003-widget-contract.md` §Declarative Widget Instance Configuration, `specs/005-screen-layout-and-rotation.md`, `specs/012-live-config-reload-and-lkgc.md` §6.

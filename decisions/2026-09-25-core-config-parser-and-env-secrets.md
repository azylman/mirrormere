# Architecture Decision Record: Core Config Parser & Env Secret Resolution

- **Date:** 2026-09-25
- **Status:** Accepted
- **Context:** Implementing Phase 1, Chunk 1.3 (#164) of SPEC-013 on `azylman/mirrormere`.

---

## 1. Canonical Configuration Hierarchy & Deprecated Keys

### Description of Issue / Inconsistency
Earlier exploratory sketches allowed loose top-level keys (`screens:`, `providers:`) and experienced drift regarding the placement of the fixed top-banner header (`header:` at root vs `display.header:`). SPEC-003 and SPEC-005 established that widgets are self-contained declarations under `display.widgets` and rotation screens are dynamically computed by the 6×2 layout solver rather than declared by operators.

### Decision Made
Implemented `internal/config/config.go` with strict schema validation:
- Canonical top-level keys: `timezone` (valid IANA timezone string) and `display` (holding `rotation`, `grid`, `header`, and `widgets`).
- Enforced rejection of deprecated or out-of-spec top-level keys (`screens`, `providers`, `theme`, etc.) with clear line-numbered diagnostic errors.
- Provided backward compatibility for root-level `header:` mapping seamlessly to `display.header` when omitted in `display:`.
- Enforced the SPEC-005 invariant that the lower grid is strictly 6 columns by 2 rows (`columns == 6`, `rows == 2`).

### Technical Rationale & References
- Eliminates dual sources of truth and ensures that configuration errors fail fast with actionable guidance.
- References: `specs/001-architecture-overview.md` §4, `specs/003-widget-contract.md` §1–3, `specs/005-screen-layout-and-rotation.md` §Declarative Widget Configuration Schema, `specs/012-live-config-reload-and-lkgc.md` §2, `specs/013-implementation-roadmap.md` §Task 1.3.

---

## 2. Explicit `*_env` Secret Resolution & Prohibition of `${VAR}` Interpolation

### Description of Issue / Inconsistency
Shell-style `${VAR}` string interpolation creates subtle syntax ambiguities when configuration values naturally contain `$`, requires brittle escaping, obscures secret provenance, and complicates hermetic testing.

### Decision Made
- Strictly prohibited arbitrary `${...}` string interpolation in configuration scalar nodes per SPEC-012 §6, reporting the exact line number on violation.
- Adopted the explicit `*_env` standard:
  - Top-level widget transport keys (e.g. `token_env`) resolve to in-memory `Token`.
  - Nested configuration properties ending in `_env` (e.g. `url_env` in calendar sources) resolve to their base property (e.g. `url`) directly via the injected `getenv` lookup function.
  - Declaring both `<key>` and `<key>_env` on the same mapping is strictly prohibited (SPEC-007 §1).
  - Missing or empty environment variables fail fast with standard diagnostic errors matching SPEC-012 §6.

### Technical Rationale & References
- Preserves the Zero Plaintext Token invariant without risking runtime interpolation failures.
- Supports pure dependency-injected test execution with `mockGetenv` or `t.Setenv()`.
- References: `specs/007-core-data-providers.md` §1 & §4, `specs/012-live-config-reload-and-lkgc.md` §6.

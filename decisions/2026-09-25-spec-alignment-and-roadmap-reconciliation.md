# Architecture Decision Record: Spec Alignment & Roadmap Reconciliation

- **Date:** 2026-09-25
- **Status:** Accepted
- **Context:** Resolving spec drift and cross-specification contradictions flagged in issues #170 and #171 on `azylman/mirrormere`.

---

## 1. Roadmap Task Realignment (SPEC-013 & Implementation Plan)

### Description of Issue / Inconsistency (#170)
`SPEC-013` (Implementation Roadmap) was drafted with summarized shorthand that diverged from merged component specifications:
1. **Profile B Hardware**: Stated "Waveshare 7.5\" black & white e-paper HAT with Adafruit Bonnet pinout", conflicting with SPEC-009's explicit declaration of a raw Waveshare 7.5\" V2 panel mounted to an Adafruit E-Ink Bonnet.
2. **Phase 1 Objectives & Coverage Floor**: Stated "100% test coverage", conflicting with the project-wide `>= 95.0%` statement coverage floor in §1, §4, and `scripts/check-coverage.sh`.
3. **Task 1.3 Config Keys & Ordering**: Referenced `screens:` and `providers:` blocks. SPEC-003 and SPEC-005 explicitly define self-contained widget declarations under `display.widgets` without a top-level `providers:` block, and layout screens are dynamically computed by the 6×2 solver rather than declared in configuration. Additionally, full LKGC pipeline validation (SPEC-012) requires the package loader (Chunk 1.4) and layout solver (Chunk 1.5), so it must be sequenced as Chunk 1.6, with the fsnotify watcher as Chunk 1.7.
4. **Task 1.4 Dimensions & Completeness**: Used `width`/`height` instead of `dimensions: [cols, rows]`. Stated custom widget overrides require `assets/`, while SPEC-003 notes `assets/` is optional.
5. **Task 2.2 `screen.rotate` Payload**: Stated that `screen.rotate` carries SSR HTML fragments, which was previously corrected in PR #146 / SPEC-006 §2.C to carry layout metadata only while clients fetch `GET /api/widgets/{widget_id}/render`.
6. **Task 2.3 Stylesheet Route**: Listed `GET /config/custom.css`, while SPEC-003 §3 and SPEC-006 §2.I establish the authoritative route as `GET /style.css` (serving custom stylesheet if mounted, else `hud.css`).
7. **Task 3.1 Provider Lifecycle Interface**: Listed `ID()`, `Fetch()`, `Interval()`, diverging from SPEC-003's lifecycle interface (`Init`, `Fetch`, `Subscribe`, `Shutdown`).
8. **Task 5.2 Buttons**: Stated hardware button tap on GPIO 5/6 advances screens, while SPEC-009 reserves GPIO 5 for forcing a full panel refresh (to clear ghosting) and GPIO 6 for screen advance.

### Decision Made
Updated `specs/013-implementation-roadmap.md` and `docs/plans/2026-09-25-mirrormere-implementation-plan.md` to resolve all 8 discrepancies and reflect the 7 atomic chunks for Phase 1.

### Technical Rationale & References
- Eliminates dual-source-of-truth confusion between component specs and the implementation roadmap.
- References: `specs/003-widget-contract.md`, `specs/005-screen-layout-and-rotation.md`, `specs/006-realtime-comms-and-mutations.md`, `specs/009-eink-display-node.md`.

---

## 2. Configuration Hierarchy Standardization (SPEC-012)

### Description of Issue / Inconsistency (#171)
SPEC-012 §2 Stage 1 listed `header` at root level (`timezone`, `display`, `header`) and §5 referenced `header.weather`, while SPEC-005 and SPEC-007 nest the persistent header zone under `display.header` (with `display.header.elements` and `display.header.weather`).

### Decision Made
Standardized SPEC-012 §2 Stage 1 on top-level keys (`timezone`, `display`) and updated §5 to reference `display.header.weather`.

### Technical Rationale & References
- Enforces consistency across configuration validation schemas and deployment examples.
- References: `specs/005-screen-layout-and-rotation.md` §Declarative Widget Configuration Schema, `specs/007-core-data-providers.md` §4.

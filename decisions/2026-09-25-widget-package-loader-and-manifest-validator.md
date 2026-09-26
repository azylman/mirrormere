# Architecture Decision Record: Widget Package Loader & Manifest Validator

- **Date:** 2026-09-25
- **Status:** Accepted
- **Context:** Implementing Phase 1, Chunk 1.4 (#165) of SPEC-013 on `azylman/mirrormere`.

---

## 1. Whole-Package Overriding & Incomplete Package Invariants

### Description of Issue / Inconsistency
Earlier architectural sketches explored file-level fallbacks (e.g. falling back to `/app/widgets/<type>/views/widget.html` if `/config/widgets/<type>/` only supplied `manifest.yaml`). However, partial inheritance causes silent schema-template mismatches and leaky abstractions between built-in and user-custom implementations.

### Decision Made
Implemented `internal/widget/loader.go` enforcing the **Whole-Package Overriding Invariant** per SPEC-003 §Directory Structure & Deterministic Dual Paths:
- When `/config/widgets/<type>/` exists, it completely shadows `/app/widgets/<type>/` with zero file-by-file fallback.
- Overrides must be self-contained, supplying both `manifest.yaml` and `views/widget.html`. An incomplete package at startup aborts with a fatal validation error:
  `widget package at <dir> is incomplete: missing required file manifest.yaml (or views/widget.html); overriding a widget type requires a complete package`
- The `assets/` directory remains optional across both built-in and custom packages.

### Technical Rationale & References
- Eliminates state corruption and template rendering crashes caused by partial overrides.
- References: `specs/003-widget-contract.md` §Directory Structure & Deterministic Dual Paths, `specs/012-live-config-reload-and-lkgc.md` §2 Stage 3.

---

## 2. Disallowed `default` Keyword Enforcement in Schemas

### Description of Issue / Inconsistency
Standard JSON Schema allows `default` values. In embedded ambient displays, implicit schema-injected defaults hide missing configuration errors, obscure data provenance, and complicate live reload diffing.

### Decision Made
- Strictly forbade the `default` keyword anywhere within manifest `config_schema` or `response_schema` per SPEC-003 §Disallowed default Keyword.
- Manifests declaring `default` fail validation fast at load time. Configurations and payloads must declare explicit properties, with templates handling optional field absence gracefully.

### Technical Rationale & References
- Adheres strictly to the LKGC predictability contract and prevents silent upstream payload transformations.
- References: `specs/003-widget-contract.md` §Disallowed default Keyword, `specs/013-implementation-roadmap.md` §Task 1.4.

---

## 3. Authoritative Default Dimensions & Cadence Fallback

### Description of Issue / Inconsistency
Widget instances in `config.yaml` can omit `dimensions: [cols, rows]` and `refresh_interval_seconds` to simplify user configuration.

### Decision Made
- Domain model `Dimension` encapsulates `[cols, rows]` on the 6×2 grid, supporting custom sequence unmarshaling and bound enforcement ($1 \le cols \le 6, 1 \le rows \le 2$).
- `ApplyDefaults` maps `pkg.Manifest.DefaultDimensions` into widget instances omitting `dimensions`. If an instance omits dimensions and the manifest lacks valid `default_dimensions`, validation fails fast with an explicit error: `[Mirrormere Config Error] widget '<id>' omits 'dimensions' and package manifest '<type>' has no valid default_dimensions`.
- Defaults `refresh_interval_seconds` from `pkg.Manifest.Refresh.IntervalSeconds` when omitted on instances.

### Technical Rationale & References
- Enforces the 6×2 discrete cell geometry while keeping user configuration files clean and uncluttered.
- References: `specs/003-widget-contract.md` §Manifest Functional Roles, `specs/005-screen-layout-and-rotation.md` §Widget Dimension Resolution & Manifest Fallbacks.

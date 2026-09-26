# Architecture Decision Record: System Status Schema and Provider Health Alignment

- **Date:** 2026-09-25
- **Status:** Accepted
- **Context:** Resolving Issue #160 on `azylman/mirrormere` (`docs(spec-006/008): system.status schema is closed and hard-codes calendar/weather fields that other specs can't satisfy`).

---

## 1. Context & Inconsistency

In commit 49b7ada (PR #151), `api/schemas/system.status.json` and `specs/006-realtime-comms-and-mutations.md` §2.D formalized `system.status` with `additionalProperties: false` while requiring hardcoded `calendar_provider` and `weather_api` string fields.

This created multiple architectural contradictions:
1. **Generic Widget & Deployment Model (SPEC-001, SPEC-005)**: Deployments without a calendar widget were still forced to emit `calendar_provider`, and deployments with multiple calendar instances had no canonical definition of "primary".
2. **Provider Scope Omissions (SPEC-003, SPEC-008)**: SPEC-008 stated that failing list adapters marked the provider degraded in `system.status`, but `system.status.json` had no list fields and forbade extensions via `additionalProperties: false`. The same applied to photos and custom HTTP providers.
3. **Duplication of Instance Health**: Per-instance operational health is already canonically tracked on every instance via `widget.update` (`state: "healthy" | "degraded" | "error"`).

---

## 2. Decision

1. **System-Level State Exclusivity**:
   - `system.status` is scoped strictly to system-wide health and configuration lifecycle:
     - `online` (boolean, required)
     - `time` (RFC 3339 UTC timestamp, required)
     - `config_status` (`"ok"` | `"error"`, required)
     - `config_error` (string or null, required)
2. **Decoupled Optional `providers` Map**:
   - Added an optional `providers` map property keyed by widget instance ID (`additionalProperties: {"type": "string"}`), permitting arbitrary provider summaries (e.g. `{"family-calendar": "connected", "outdoor-weather": "degraded"}`) without rigid top-level property names.
   - Removed hardcoded `calendar_provider` and `weather_api` fields from `required` and top-level schema.
3. **Instance-Level Failure Delegation**:
   - Updated SPEC-008 Consistency Rule 4: when a list adapter fails, the widget instance transitions its state to `degraded` via `widget.update`, leveraging the universal widget lifecycle contract rather than overloading `system.status`.
   - Updated SPEC-006 §2.D and §3.7 hydration examples to reflect the clean system status payload.

---

## 3. Verification & Compliance Evidence

- `api/schemas/system.status.json` validates against JSON Schema Draft 2020-12.
- `./scripts/verify.sh --staged` passes cleanly.

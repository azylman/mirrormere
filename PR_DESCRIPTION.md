## Executive Summary

This PR aligns the `system.status` JSON schema and SPEC-006 with canonical Mirrormere widget state words by restricting optional `providers` map values to `"healthy" | "degraded" | "error"`.

- **Schema Enumeration (`api/schemas/system.status.json`)**: Added `"enum": ["healthy", "degraded", "error"]` to `providers` map `additionalProperties`, preventing invented status terms like `"connected"` or `"offline"`.
- **Specification Alignment (`specs/006-realtime-comms-and-mutations.md`)**: Updated the `providers` documentation and example payload from `"connected"` to `"healthy"`.
- **Decision Record (`decisions/2026-09-25-system-status-schema-open-providers.md`)**: Documented the enum constraint in the ADR.

---

## Architectural & Implementation Details

- Constrains `providers` map value type in `api/schemas/system.status.json`:
  ```json
  "additionalProperties": {
    "type": "string",
    "enum": ["healthy", "degraded", "error"],
    "description": "Operational health status of the provider ('healthy', 'degraded', 'error')"
  }
  ```
- Replaces `"connected"` with `"healthy"` in SPEC-006 §2.D.

---

## Verification & Test Evidence

1. **Pre-flight Verification**:
   - Staged changes verified via `./scripts/verify.sh --staged`.
   - All tests pass with **97.66%** overall repository test statement coverage (exceeding >= 95.0% floor).

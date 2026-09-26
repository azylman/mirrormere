# Architecture Decision Record: Authoritative Household Timezone for Weather-Forecast Provider

- **Date:** 2026-09-26
- **Status:** Accepted
- **Context:** Resolving Issue #258 on `azylman/mirrormere`, aligning `weather-forecast` with SPEC-001 §4 and SPEC-007 §2.

---

## 1. Problem Statement
In commit `0c0e37e` (Chunk 3.3A, #238):
1. The `weather-forecast` widget manifest (`widgets/weather-forecast/manifest.yaml`) declared a per-instance `timezone` property under `config_schema.properties`.
2. When omitted in instance config, `internal/provider/weather.go` defaulted the Open-Meteo `timezone` query parameter to `"auto"`.
3. Open-Meteo's `timezone=auto` parameter resolves timestamps to the geographic coordinates' local timezone rather than the household timezone configured in `config.yaml`.
4. As a result, hourly forecast timestamps and daily min/max day boundaries in the weather card could diverge from the household's authoritative timezone.
5. This violated SPEC-001 §4 (which establishes top-level `timezone` as the authoritative household zone against which all dates and timelines format) and SPEC-007 §2 (whose configuration schema only specifies `latitude`, `longitude`, and `units`).

---

## 2. Decision & Architecture

### A. Manifest Schema Alignment (`widgets/weather-forecast/manifest.yaml`)
- Removed `timezone` from `config_schema.properties`.
- Retained `additionalProperties: false` with required `latitude` and `longitude`, and optional `units` (`imperial` | `metric`), perfectly mirroring SPEC-007 §2.

### B. Timezone Propagation via InitOptions (`internal/provider/provider.go`, `internal/provider/coordinator.go`)
- Added `Timezone string` to `provider.InitOptions`.
- Updated `ProviderCoordinator.buildInitOptions` to pass the household `Timezone` from the active runtime configuration snapshot (`config.Snapshot.Config.Timezone`) into every initialized provider's `InitOptions`.
- On live configuration reload (`UpdateConfig`), if `diff.TimezoneChanged` is detected per SPEC-012 §5, running unchanged widget workers are restarted with the new snapshot, refreshing their `InitOptions.Timezone` and purging stale cache entries.

### C. Weather Ingest Provider Updates (`internal/provider/weather.go`)
- Removed `config["timezone"]` resolution.
- Set `WeatherProvider.timezone` from `opts.Timezone`, falling back to `"UTC"` if empty (e.g., in airgapped unit tests without top-level configuration).
- Passed `p.timezone` in the Open-Meteo REST API query parameter `timezone`, ensuring all hourly timestamp intervals and daily forecast aggregations are calculated directly in the household's timezone.
- Updated `internal/provider/header_poller.go` to use the configured household timezone parameter in its Open-Meteo query rather than `"auto"`.

### D. Specification Synchronization (`specs/007-core-data-providers.md`)
- Updated SPEC-007 §2 upstream endpoint example URL query parameter from `&timezone=auto` to `&timezone=America/Los_Angeles` to match the household configuration standard.

---

## 3. Verification & Compliance
- **Hermetic Testing**:
  - `internal/provider/weather_test.go`: Verified that `opts.Timezone` is sent as the query parameter `timezone` to Open-Meteo, defaults to `UTC` when empty, and verifies that extraneous `config["timezone"]` is ignored.
  - `internal/provider/coordinator_test.go`: Verified that `InitOptions.Timezone` is correctly populated from the snapshot, and that `UpdateConfig` with `diff.TimezoneChanged` restarts workers and updates `InitOptions.Timezone`.
- **Test Coverage**: Maintained 95.6% statement coverage in `internal/provider`, comfortably exceeding the 95.0% threshold floor.

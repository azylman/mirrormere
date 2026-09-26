# Architecture Decision Record: Weather-Forecast Provider and Autonomous Header Weather Poller

- **Date:** 2026-09-26
- **Status:** Accepted
- **Context:** Implementing Issue #230 on `azylman/mirrormere` (SPEC-013 Phase 3, Chunk 3.3A), fulfilling SPEC-007 §2, §4, SPEC-003 §3, and SPEC-006 §2.B, §3.

---

## 1. Problem Statement
Prior to Chunk 3.3A:
1. **Weather Ingest Driver Absent**: No built-in driver existed for the `"weather-forecast"` provider type to query Open-Meteo's keyless REST API, normalize WMO 4501 condition codes into human-readable condition text and vector icon tokens, or return structured `WeatherSnapshot` domain models.
2. **Autonomous Top-Banner Weather Missing**: Top-banner ambient weather (`display.header.weather`) lacked an autonomous background polling loop independent of on-grid widgets. Display clients could not receive real-time ambient conditions without an explicit weather widget placed on the 6×2 grid canvas.
3. **Hardcoded Fallback Placeholder**: In `internal/events/hydration.go` and `internal/events/dispatcher.go`, initial state hydration and reload triggers fell back to a hardcoded `68.5°F` dummy weather struct when weather state was unset.
4. **Built-in Widget Package Missing**: No built-in `widgets/weather-forecast` package existed with declarative `manifest.yaml` and cyber HUD template `views/widget.html`.

---

## 2. Decision & Architecture

### A. Open-Meteo Ingest Driver (`internal/provider/weather.go`)
- Registered `"weather-forecast"` (and alias `"weather"`) in `DefaultRegistry`.
- Implements `Provider` interface: `Init`, `Fetch`, `Subscribe`, and `Shutdown`.
- Queries Open-Meteo's standard forecast REST API with configured latitude, longitude, units (`imperial` / `metric`), and timezone.
- Enforces Slowloris defense on HTTP reads via `io.LimitReader(resp.Body, 1<<20)` (1MB cap).
- Maps numeric WMO 4501 codes to canonical semantic strings (e.g. `0` -> "Clear Sky", `1` -> "Mainly Clear", `95` -> "Thunderstorm") and vector icon tokens (e.g. `weather-sunny`, `weather-partly-cloudy`, `weather-lightning`).
- Normalizes API response into `WeatherSnapshot` domain model containing:
  - `Current`: temperature, feels like, relative humidity, wind speed, units labels, condition code/text, and icon token.
  - `Hourly`: up to next 24 hours of forecasted temperature, precipitation probability, and condition icon.
  - `Daily`: up to 7 days of maximum/minimum temperature, precipitation probability, condition text, and icon.

### B. Autonomous Header Weather Poller (`internal/provider/header_poller.go`)
- Implemented `HeaderWeatherPoller` managed directly by `ProviderCoordinator`.
- Operates independently from on-grid `display.widgets`: active whenever `display.header.weather` is declared in `config.yaml`.
- Fetches strictly current ambient observations (`temperature_2m`, `weather_code`) on a configurable cadence (default: 900s).
- Immediately executes initial fetch upon startup, storing result in `HeaderWeatherSink` (`events.InMemoryStateProvider`) and broadcasting `header.update` SSE events over the event bus (`Broadcaster`).
- Live configuration reload (`UpdateConfig`) dynamically updates poller coordinates, units, and cadence without requiring daemon restart, or gracefully terminates the poller if header weather is removed.

### C. Eviction of Hardcoded 68.5°F Fallback & Schema Alignment
- Evicted the hardcoded `68.5°F` placeholder from `BuildHydrationBatch` (`internal/events/hydration.go`) and `DispatchConfigReload` (`internal/events/dispatcher.go`).
- Updated `HeaderUpdateData` struct with `json:"weather,omitempty"` tag. When weather is unconfigured or cold before first fetch, `weather` is cleanly omitted from JSON payloads.
- Updated `api/schemas/header.update.json` required properties to `["timestamp", "timezone"]`, keeping `weather` optional per schema standard.

### D. Built-in `weather-forecast` Widget Package
- Authored `widgets/weather-forecast/manifest.yaml` specifying `provider: weather-forecast`, capabilities (`ambient-static`, `touch-interactive`), default dimensions `[2, 1]`, and input `config_schema`.
- Authored `widgets/weather-forecast/views/widget.html` rendering with Mirrormere Cyber HUD design tokens: large mono temperature readout, condition text, icon badge, feels-like/humidity/wind metadata row, and horizontal scrollable hourly timeline.
- Gracefully handles cold/unhydrated state with localized loading card placeholder.

---

## 3. Verification & Compliance
- **Hermetic Unit Testing**: Airgapped HTTP mock servers (`httptest.Server`) test imperial and metric unit transformations, WMO code mapping exhaustive coverage, 1MB payload truncation defense, HTTP error statuses, malformed JSON handling, and context cancellations.
- **Statement Coverage**: Maintained 95.3% statement coverage in `internal/provider` and 95.7% in `internal/events`, strictly exceeding the >= 95.0% floor.
- **Live Integration**: Verified widget package discovery and manifest loading in `internal/widget`, and HTML template compilation and rendering in `internal/render`.

# SPEC-012: Live Configuration Reload & Last-Known-Good Configuration (LKGC)

## Status
Approved / Architecture Defined

## Overview
Mirrormere is engineered for ambient smart wall displays and touch kiosks where zero-downtime operation is critical. System reboots, crashed layouts, or blank error screens caused by minor configuration typos degrade the domestic appliance experience.

To ensure continuous, resilient display uptime, the Mirrormere Go backend implements **In-Process Live Configuration Reload** governed by the **Last-Known-Good Configuration (LKGC)** model. When `/config/config.yaml` is modified, the daemon validates the proposed changes end-to-end before applying them. If validation fails at any point, the running configuration remains active and untouched, errors are logged and surfaced via telemetry, and display clients continue rendering without interruption.

---

## 1. Directory-Level Watching & Atomic Save Handling

### Inode Detachment Prevention
Most modern text editors and IDEs (VS Code, Neovim/Vim, JetBrains, Emacs) execute atomic file writes: they write new contents to a temporary file (`config.yaml.tmp` or `4913`) and rename it over `config.yaml`. Under Linux and Unix, single-file Docker bind mounts remain pinned to the original inode, causing the container to miss the update.

To guarantee that file updates are always detected:
1. **Directory-Level Mount**: The host mounts the parent configuration directory (`-v ./config:/config:ro`).
2. **Directory Watch**: The in-process `fsnotify` watcher monitors the `/config/` directory descriptor. When an atomic rename replaces `config.yaml`, the directory watch catches the `Create` and `Rename` events immediately.

### Debounce & Temporary File Filtering
- **Debounce Window (100ms)**: File saves generate clusters of filesystem events within milliseconds (e.g. `Create(config.yaml.tmp)`, `Write(config.yaml.tmp)`, `Rename(config.yaml.tmp -> config.yaml)`, `Chmod(config.yaml)`). The watcher debounces incoming filesystem events with a 100ms timer. Rapid successive events reset the timer.
- **Temporary File Ignore Filter**: Events matching temporary editor patterns (`*.tmp`, `*.swp`, `*~`, `4913`, `.goutputstream-*`) are filtered out and do not trigger reload evaluations.
- **Clean Read Settle**: When the debounce timer fires, the file descriptor has typically been closed and the file is written to disk before the parser opens `/config/config.yaml`. While atomic-rename saves guarantee a complete file on rename, an in-place editor or slow streaming writer could still be mid-write when the 100ms timer fires; in that case, Stage 1 YAML parsing fails cleanly, LKGC retains the running configuration, and the subsequent write event re-triggers evaluation once settled.

---

## 2. Last-Known-Good Configuration (LKGC) Validation Pipeline

Before any running process, widget worker, or layout solver is touched, the candidate configuration must pass through a strict, multi-stage validation pipeline:

```
[ fsnotify Event ]
       │
       ▼
[ 100ms Debounce Settle ]
       │
       ▼
[ 1. YAML Syntax & Structure ] ──(Fail)──┐
       │ (Pass)                          │
       ▼                                 │
[ 2. Core Instance Schemas ]   ──(Fail)──┤
       │ (Pass)                          │
       ▼                                 │
[ 3. Package Existence & Completeness ] ─┤
       │ (Pass)                          │
       ▼                                 │
[ 4. Manifest config_schema Validation ] ┤
       │ (Pass)                          │
       ▼                                 │
[ 5. List Source-of-Truth Rules ] ───────┤
       │ (Pass)                          │
       ▼                                 │
[ 6. 6x2 Bin-Packing Layout Solver ] ────┘
       │ (All Pass)                      │ (Any Fail)
       ▼                                 ▼
[ Atomic Configuration Swap ]   [ Retain Running LKGC ]
- Diff instance list             - Log structured error
- Adjust sync goroutines         - Broadcast system.status error
- Broadcast screen.rotate        - Display continues untouched
```

### Validation Stages
1. **YAML Syntax & Schema**: Validates well-formed YAML structure, top-level keys (`display`, `header`, `timezone`), and standard types.
2. **Standard Instance Keys**: Validates that all items in `display.widgets` contain mandatory framework fields (`id`, `type`), valid `dimensions` (or valid fallback to `manifest.yaml` `default_dimensions`), and positive integer cadences (`refresh_interval_seconds`).
3. **Package Existence & Completeness**: Verifies that every referenced `type` resolves to a complete widget package (`manifest.yaml` and `views/widget.html`) under `/config/widgets/<type>/` or `/app/widgets/<type>/` (per SPEC-003). At runtime, an incomplete package created after boot is not selected, retaining active LKGC until all required files exist on disk.
4. **Manifest `config_schema` Validation**: Validates the nested `config:` mapping for every instance against its package `config_schema` using JSON Schema Draft 2020-12 (`github.com/santhosh-tekuri/jsonschema/v6`).
5. **Domain & Source-of-Truth Rules**: Enforces domain-level integrity (e.g. task widgets reference valid list sources per SPEC-008; HTTP providers declare valid `endpoint` URLs).
6. **Bin-Packing Layout Solver (SPEC-005)**: Executes the exact 6×2 layout solver. Verifies that all declared widgets fit cleanly onto screens adhering to the fully-filled screen invariant with no overflowing tiles.

### Validation Failure Behavior
If any validation stage fails:
1. **Retain Running LKGC**: The in-memory running configuration remains 100% active. Zero running goroutines are terminated; zero widget states are purged.
2. **Structured Diagnostic Logging**: The Go daemon emits a high-visibility structured log:
   ```text
   [config.reloader] error="live config validation failed: manifest config_schema validation error for widget 'living-room-temp': missing required property 'entity_id'; retaining LKGC"
   ```
3. **Telemetry & HUD Alerting**: The daemon broadcasts a `system.status` event over `GET /api/events` (`config_status: "error"`, `config_error: "..."`), allowing connected kiosks to display a subtle diagnostic warning badge in the header without disrupting active widget tiles. Core retains this error state in memory and flushes it during initial connection hydration (SPEC-006 §3) so newly connected or reconnecting displays immediately reflect the diagnostic badge. The error state is cleared and `config_status` transitions back to `"ok"` (with `config_error: null`) as soon as a subsequent valid configuration is successfully applied.

---

## 3. Instance Diffing & Worker Lifecycle Management

When a candidate configuration passes validation, Mirrormere computes a deterministic structural diff between the running instances and the new instances:

| Change Category | Detection Criteria | Reloader Action |
|:---|:---|:---|
| **Unchanged** | Identical `id`, `type`, `config`, `refresh_interval_seconds`, `endpoint` | **Zero interruption**: Goroutine continues running; cache remains valid. |
| **Added** | `id` exists in new config but not in running config | **Initialize & Start**: Instantiate provider, run `Init()`, start dedicated sync goroutine, emit `widget.update`. |
| **Removed** | `id` exists in running config but not in new config | **Graceful Shutdown**: Cancel worker context (`cancel()`), flush background connections, purge in-memory cache, remove from SQLite cache. |
| **Modified** | Same `id` and `type`, but `config`, interval, or transport settings changed | **In-Place Worker Restart**: Cancel existing worker context, re-instantiate provider with updated settings, start new sync loop (see Cache Policy below). |

### Classification Rules & Cache Retention Policies

1. **Type Change on Same ID (Replacement = Removed + Added)**:
   - If an instance in the new configuration shares the same `id` as a running instance but alters its `type` (e.g. `id: hub` changes from `type: weather-forecast` to `type: calendar-agenda`), this is NOT classified as a simple in-place modification.
   - The reloader classifies this as a composite **Removed plus Added** operation: the old provider's sync worker is canceled and its cached state is completely purged from memory and SQLite (preventing schema type contamination across different data providers), followed by a fresh initialization and cold boot of the new provider type.

2. **Cache Retention Policy on Modification**:
   - **Transport & Cadence Changes** (e.g. `refresh_interval_seconds`, `token_env`): When only polling cadence or transport credentials change, existing cached data remains valid and is retained in memory and SQLite as `healthy` until the updated sync worker completes its first successful ingestion cycle under Stale-While-Revalidate.
   - **Domain `config` Changes** (e.g. new `latitude`/`longitude`, different `entity_id`, `list_id`, or `calendars`): When the target domain parameters change, the previous target's data does not represent the new target. To avoid showing outdated target state as `healthy`, the reloader immediately purges the instance's cached data (or transitions the tile state to `degraded` with an empty loading state) and emits a `widget.update` SSE event so connected clients display a clean loading indicator until the new sync loop ingests fresh data for the new target.

---

## 4. Layout Solver Recalculation & Screen State

Following the instance diff, the rotation engine updates the active canvas state:

1. **Packing Solver Recalculation**: Runs the 6×2 bin-packing solver to determine new screen assignments, widget origins `[x, y]`, and total screen count (`total_screens`).
2. **Active Screen Clamping**:
   - If the new layout yields fewer screens than before, and the current screen index exceeds bounds (`current_screen >= total_screens`), `current_screen` is clamped to `0` (or `total_screens - 1`).
   - If the current screen is still within valid bounds, the active screen index is preserved, minimizing jarring visual jumps for viewers currently looking at the wall.
3. **SSE Signal Dispatch (`screen.rotate`)**:
   - The engine immediately broadcasts a fresh `screen.rotate` event over `GET /api/events` carrying the updated layout for the active screen.
   - For any newly placed widgets on the active screen, clients fetch `GET /api/widgets/{widget_id}/render` for each widget on the new layout and mount the updated HTML fragments into the grid canvas.

---

## 5. Live Settings vs. Restart-Required Settings

Mirrormere maximizes live reconfigurability while clearly delineating settings bound to host network sockets or process memory limits:

### Settings Applied Live (Zero Restart)
- **Widget Instances**: Adding, removing, reconfiguring, or resizing any widget instance in `display.widgets`.
- **Screen Rotation Settings (`display.rotation`)**: Changes to `display.rotation.interval_seconds` update the active rotation timer JIT. Associated rotation parameters (`transition` visual hint, `pause_on_touch` client policy, and `pause_duration_seconds` touch pause window) are also applied live without container restart.
- **Top-Banner Weather Poller**: Coordinates (`latitude`, `longitude`), `units`, and polling cadences under `header.weather` restart the background poller immediately.
- **Timezone**: Changing `timezone` updates Go's `time.Local` / location pointer JIT, immediately adjusting digital clock formats, agenda relative times, and rollover timers.
- **Custom Stylesheet (`/config/custom.css`)**: Hot-reloaded live and broadcast via `style.reload`.

### Settings Requiring Container Restart
- **Host & Port Bindings (`HOST`, `PORT`)**: TCP socket listeners bound to the host network interface cannot be rebound without restarting the process.
- **Data Directory Mount (`/data`)**: Path to the underlying persistent SQLite database storage.
- **Host Docker Environment Variables**: Injected environment variables passed into the container at startup.

---

## 6. Secrets & Environment Variable Resolution

### Explicit `*_env` Keys Standard
Mirrormere strictly adopts **explicit `*_env` configuration keys** (e.g. `url_env`, `token_env`, `credentials_env`) rather than arbitrary `${VAR}` string interpolation:
- **Rationale**: String interpolation syntax like `${VAR}` creates subtle syntax ambiguities when configuration values naturally contain `$`, requires brittle shell escaping, and obscures secret provenance.
- **Explicit Binding**: Config keys explicitly state their nature (e.g. `token_env: SENSOR_HUD_TOKEN`). The loader resolves the secret directly via `os.Getenv(key)`.

### Environment Variable Lifecycle on Reload
- In Linux container runtimes, process environment variables (`/proc/self/environ`) are immutable after container start.
- On live reload, `*_env` keys are evaluated against the current process environment. Modifying host `.env` files or adding new environment variables requires a container restart (`docker compose up -d`).
- If an instance declares a `*_env` key whose environment variable is unset or empty, configuration validation fails at startup or reload with a descriptive error:
  ```text
  [config.validator] error="widget 'living-room-temp': environment variable 'SENSOR_HUD_TOKEN' defined in token_env is unset or empty"
  ```

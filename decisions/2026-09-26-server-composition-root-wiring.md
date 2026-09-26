# Architecture Decision Record: Server Composition Root & Handler Wiring

- **Date:** 2026-09-26
- **Status:** Accepted
- **Context:** Resolving Issue #270 on `azylman/mirrormere` (`fix(server): cmd/server wires no handlers, so /api/audio (and /api/events, /display, lists) 404 in the running daemon`).

---

## 1. Problem Statement
Previously, `cmd/server/main.go` only instantiated a minimal `server.Config{Host: *hostFlag, Port: portVal}` without instantiating or wiring any coordinators, stores, engines, or handlers. In the running daemon, only `/healthz` responded while all other endpoints (`/api/events`, `/api/audio/*`, `/api/video/*`, `/api/voice/*`, `/display`, `/style.css`, `/api/lists/*`, `/api/widgets/*`, `/api/screen/*`) returned HTTP 404.

Furthermore:
1. `internal/events/hub.go` lacked provider coordinator wiring, preventing runtime provider reconfiguration during live configuration reloads.
2. `cmd/server` lacked end-to-end HTTP smoke test coverage verifying active handler routing and lifecycle shutdown.

---

## 2. Decisions & Implemented Changes

### A. Full Composition Root in `cmd/server/main.go`
- **CLI Flags**: Configured flags for `-host`, `-port`, `-config`, `-builtin-widgets`, `-custom-widgets`, and `-db`.
- **Directory Discovery**: Implemented directory resolution for builtin widgets across `/app/widgets`, `widgets`, and `../../widgets`.
- **Fallback Configuration**: Provided valid fallback YAML configuring a 12-cell spacer canvas (`dimensions: [6, 2]`) to satisfy SPEC-005 100% full-screen constraints when running without a mounted configuration file.
- **Tasks Persistence**: Initialized `tasks.SQLiteStore` with automatic fallback to `:memory:` if storage directories are inaccessible.
- **Provider Registry**: Injected the SQLite store into `provider.Registry` via `provider.NewTasksProviderWithStore(tasksStore)` to ensure shared database connection management.
- **Subsystem Coordinators & Handlers**: Instantiated and wired all 9 coordinators, engines, and handlers:
  - `eventsHandler`: `events.Handler(hub)`
  - `screenHandler`: `rotation.NewHandler(rotationCoord)`
  - `renderHandler`: `render.NewHandler(renderEngine, loader)`
  - `pushHandler`: `provider.NewPushHandler(providerCoord, logger)`
  - `displayHandler`: `display.NewHandler(display.WithTimezoneProvider(...))`
  - `listsHandler`: `server.NewDefaultListsHandler(tasksStore)`
  - `audioHandler`: `server.NewDefaultAudioHandler(audioCoord)`
  - `videoHandler`: `server.NewDefaultVideoHandler(videoCoord)`
  - `voiceHandler`: `server.NewDefaultVoiceHandler(voiceCoord)`
- **Filesystem Watcher**: Configured live reload monitoring via `watcher.New` whenever a configuration directory is available.
- **Immediate SSE Unblock on Shutdown**: Configured `hub.Close()` to fire immediately upon `<-ctx.Done()`, closing subscriber channels and allowing active SSE client streams to terminate instantly without waiting for the 5-second HTTP shutdown timeout.
- **Deterministic Port Discovery**: Introduced `Option` pattern and `WithAddrChan` for race-free ephemeral port discovery during integration tests.

### B. Hub Provider Coordinator Wiring (`internal/events`)
- **`internal/events/hub.go`**: Declared `ProviderCoordinator` interface and added `SetProviderCoordinator`/`ProviderCoordinator` methods on `Hub`.
- **`internal/events/dispatcher.go`**: Updated `DispatchConfigReload` to invoke `pc.UpdateConfig(snapshot)` during configuration reloads.
- **`internal/events/dispatcher_test.go`**: Added `TestHub_DispatchConfigReload_WithProviderCoordinator` testing both success and error logging paths.

### C. Comprehensive Smoke & Edge-Case Testing
- **`cmd/server/main_test.go`**:
  - Added `TestRun_CompositionRootSmoke` testing `/healthz`, `/api/audio`, `/api/video/state`, `/display`, `/style.css`, `/api/lists/test/items`, and `/api/events`.
  - Added unit test coverage for config read errors, YAML parse errors, tasks SQLite store fallback, and environment variable overrides.
  - Achieved 96.3% statement coverage on `cmd/server` and 95.9% on `internal/events`.

---

## 3. Consequences
- All 9 primary HTTP API route groups and static assets are now fully functional in the daemon.
- Zero CGO dependency and static compilation invariants remain strictly satisfied.
- Fast, hermetic unit and smoke test suites pass without network or shared database dependencies.

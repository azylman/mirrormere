# AGENTS.md

Mirrormere is a local-first smart wall display framework: a Go core server
(`cmd/server`) that aggregates calendar, weather, tasks and smart-home data and
serves it to touch-kiosk and e-ink display profiles, plus a LAN voice pipeline
(edge mic -> STT -> brain -> TTS) coordinated by the hub in `internal/voice`.
See `README.md` for the product overview.

## Build and test

- Build: `make build` (writes `bin/server`). Run: `make run`.
- Tests: `make test` (`go test -v ./...`). For a quick loop use a package, e.g.
  `go test ./internal/voice/... ./internal/config/...`.
- Pre-flight (canonical agent & developer gate): always stage all modified and newly created files first, then run from the repository root:
  `git add -A && sh scripts/verify.sh --staged`
  Staging (`git add -A`) is required so untracked files (such as newly extracted components or packages) are included in path-scoped checks. (`make verify` runs `sh scripts/verify.sh --full` across the entire repo; unrecognized flags fall back to `--staged`.)
  `scripts/verify.sh` runs:
  1. **Hygiene & structure guards**: UTF-8 BOM check, `gofmt`, no imperative DDL, `.github/dependabot.yml` manifest/group coverage, and `web/ui/` per-widget CSS co-location.
  2. **Go static analysis & coverage** (when `.go` or Go config files are staged): ratcheted `code-health` limits (`max-lines` and cyclomatic complexity against `.code-health-baseline.json`, scoped to staged `.go` files), `golangci-lint` (scoped to modified Go packages), dual-pass `deadcode` (`deadcode <main-entrypoints>` without `-test` plus `deadcode -test ./...`, auto-installed via `go install` when missing), and the 95.0% Go coverage floor (`scripts/check-coverage.sh`). Also runs template/manifest Go tests (`./web`, `./internal/render`, `./internal/server`, `./internal/widget`) when `web/` (excluding `web/ui/`) or `widgets/` files are staged.
  3. **React UI (`web/ui/`)** (when `web/ui/` files are staged): automatically runs `npm ci` (if `node_modules` is missing) followed by `eslint` on the staged `web/ui/` `.ts`/`.tsx`/`.js` files (or full `npm run lint` when `eslint.config.js`/`package.json` changes), `npm run deadcode`, `npm run typecheck`, `npm run test:coverage` (Vitest + coverage thresholds), and `npm run build` inside a `(cd web/ui && ...)` subshell.
  4. **Node & Python suites** (scoped to touched paths): Node tests (`web/test`, `sidecars/eink-renderer/test`) and Python suites (`sidecars/voice-fingerprinter`, `client/eink-node`, `client/ear`, `client/remote`, `client/ember`, `deploy/ical-bridge`).
- CI (`.github/workflows/ci.yml`) runs parallel path-scoped jobs (`changes`, `verify`, `lint`, `ui`, `perf`, `verify-gate`) on pull requests to `main`, with failure remediation logs directing agents to run `git add -A && sh scripts/verify.sh --staged` from the repository root.

## Where things live

- `cmd/server/` - the server entrypoint (config load, SIGHUP reload).
- `internal/` - the core packages: `config`, `voice` (hub, coordinator, metrics),
  `server` (HTTP handlers), `api` (HTTP DTO types), `widget`, `render`, `layout`,
  `rotation`, `provider`, `display`, `kiosk`, `events`, `chatlog`, `tasks`, `video`, `audio`, `domain`.
- `sidecars/` - separately deployed helpers: `hass-proxy` (Go, shares the root module),
  `eink-renderer` (Node), and `voice-fingerprinter` (Python).
- `client/` - edge clients: `ear` voice daemon, `eink-node`, `remote` (Python), and `cast-watcher` (Go).
- `web/` - the kiosk front end (static assets and templates) and its Node tests.
- `widgets/` - widget manifests and assets, one directory per widget.
- `deploy/` - compose files, kiosk and e-ink setup, and `deploy/examples/config.yaml`.
- `api/schemas/*.json` - the SSE event payload schemas.
- `docs/decisions.md` - architectural decisions and invariants; read it before changing behavior.

## web/ui/ (React display shell)

`web/ui/` is the Vite + React 19 + TypeScript (strict) source for the React display shell, served at
`/display?ui=react` (or `display.ui: react` in config). Running `git add -A && sh scripts/verify.sh --staged`
from the repository root automatically executes the full `web/ui/` verification pipeline (`npm ci` when
`node_modules` is missing, per-widget CSS co-location check, `npm run lint`, `npm run deadcode`,
`npm run typecheck`, `npm run test:coverage`, and `npm run build`) inside a subshell whenever `web/ui/` files
are staged—do not `cd web/ui` without a subshell or chain redundant `npm run lint` commands outside
`verify.sh`. For interactive local development, use `(cd web/ui && npm run dev)` or `(cd web/ui && npm test)`
(Node version in `web/ui/.nvmrc`).
The build writes to `web/static/ui/` (`app.js`, `app.css`, fixed names, no hashes), which is git-ignored
and compiled automatically inside the multi-stage `Dockerfile` (`ui-builder` stage). Do not commit or
hand-edit `web/static/ui/`.

## Conventions that are easy to miss

- Configuration is one YAML file (`config.yaml`). There are no command-line flags and
  no environment-variable overrides; the only environment variables are secrets,
  referenced from the YAML through explicit `*_env` keys (`docs/decisions.md` section 3).
- Config reloads on `SIGHUP`. A config that fails validation is rejected and the
  last-known-good config stays active (`internal/config/lkgc.go`), so add validation in
  `internal/config/config.go` for any new key, give it a getter with a default, and
  document it in `deploy/examples/config.yaml`.
- Keep the core repo generic: no personal names, hostnames, IP addresses or household-specific
  keywords in code, defaults, docs or examples. Deployment specifics belong in a user's
  own `config.yaml`. (Maintainer convention; it is not written down elsewhere in the repo.)
- The voice hub talks to its STT, brain, TTS and optional fast-path endpoints over HTTP
  (STT may also be Wyoming TCP); the URLs come from the `voice_hub` section of the config.
- Go tests are expected to stay above the 95% coverage floor; new code needs tests.
- `Dockerfile` uses a multi-stage build: `ui-builder` compiles `web/ui/` into `/app/web/static/ui/`,
  `builder` compiles `/app/server`, and the runtime stage packages `/app/server` plus `/app/web/`
  and `/app/widgets/`. Built-in widgets are baked directly into the image (`/app/widgets/`), while
  custom overrides (`/config`) remain volume-mounted.

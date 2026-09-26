# Architecture Decision Record: Multi-Container Compose Profiles & Full Stack Bench Bringup

- **Date:** 2026-09-26
- **Status:** Accepted
- **Context:** Implementing Issue #250 on `azylman/mirrormere` (SPEC-013 Phase 6, Chunk 6.3), fulfilling multi-container Docker Compose profile orchestration, multi-arch build verification across `linux/amd64` and `linux/arm64`, bench smoke testing, and hardware isolation per SPEC-001, SPEC-002, SPEC-004, and SPEC-009.

---

## 1. Problem Statement
Prior to Chunk 6.3:
1. **Profile Isolation & Unbounded Startup**: Running `docker compose up` without profile isolation risked starting hardware-specific sidecars (like `go2rtc` requiring physical `/dev/video0` or `eink-renderer` running headless Chromium) regardless of whether the target host was an Intel N100 Touch Kiosk (Profile A) or a Raspberry Pi 4B Ambient E-Paper display (Profile B).
2. **Local Bench Peripheral Traps**: In `deploy/compose.yml`, `go2rtc` strictly mounted host devices (`/dev/video0`, `/dev/dri`, `/dev/snd`) and mapped hardware groups (`video`, `render`, `audio`). On developer laptops, CI test runners, or bench workstations lacking physical USB capture cards or ALSA audio cards, starting the video stack resulted in immediate daemon errors (`error gathering device information while adding custom device "/dev/video0": no such file or directory`).
3. **Build Context Misalignment for Eink Sidecar**: In `deploy/compose.yml`, `eink-renderer` was configured with `context: ..` and `dockerfile: sidecars/eink-renderer/Dockerfile`. Because the Dockerfile executes `COPY package.json ./`, building from the repository root failed as `package.json` resides strictly inside `sidecars/eink-renderer`.
4. **Multi-Architecture Build Verification Void**: While the root Go `Dockerfile` supported cross-compilation via `TARGETARCH`, GitHub Actions CI only built the `mirrormere` core image, omitting `mirrormere-cast-watcher` and `mirrormere-eink-renderer`. Both Intel N100 (`linux/amd64`) and Raspberry Pi 4B (`linux/arm64`) target environments required validated multi-arch releases in GHCR.
5. **Lack of Automated Bench Connectivity Smoke Tests**: There was no unified, repeatable automated tool to verify that all container endpoints across profiles were responsive and serving valid payloads during hardware bringup.

---

## 2. Decision & Architecture

### A. Multi-Container Compose Profiles (`deploy/compose.yml`)
- **Default Profile (`mirrormere-core`)**:
  - The core daemon runs by default on port 8080 without requiring `--profile`.
  - Serves REST APIs (`/healthz`, `/api/...`), SSE event stream (`/api/events`), and the SSR web HUD (`/display`).
- **Video Profile (`profiles: ["video"]`)**:
  - Activated via `docker compose --profile video up`.
  - Includes `go2rtc` (port 1984 WebRTC/RTSP gateway, 8555 candidate ports) and `cast-watcher` (port 8090 CastV2 monitor and transport action webhook server).
  - Depends on `mirrormere-core` and `go2rtc` health checks.
- **E-Ink Profile (`profiles: ["eink"]`)**:
  - Activated via `docker compose --profile eink up`.
  - Includes `eink-renderer` (port 8081 headless Chromium snapshot and ETag caching server).
  - Depends on `mirrormere-core` health check.

### B. Hardware Device Isolation via Compose Override (`deploy/compose.override.yml`)
- Docker Compose natively auto-merges `compose.override.yml` when present.
- To allow developer workstations and bench test setups without physical USB capture hardware to launch and test the video stack:
  ```yaml
  services:
    go2rtc:
      devices: !reset []
      group_add: !reset []

    cast-watcher:
      environment:
        - CHROMECAST_ADDR=${CHROMECAST_ADDR:-127.0.0.1:8009}
  ```
- The standard Docker Compose `!reset []` directive cleanly replaces the host device and group mappings with empty arrays, allowing `go2rtc` and `cast-watcher` to initialize smoothly in simulation and bench test modes.

### C. Build Context Normalization (`deploy/compose.yml`)
- Updated `eink-renderer` service build definition:
  - `context: ../sidecars/eink-renderer`
  - `dockerfile: Dockerfile`
- This encapsulates the Node.js headless renderer within its own directory context, ensuring `package.json`, `src/`, and `entrypoint.sh` resolve cleanly without copying unrelated repository assets.

### D. Multi-Architecture CI Matrix (`.github/workflows/ci.yml`)
- Refactored the `docker` job in GitHub Actions into a matrix build:
  - `mirrormere`: `context: .`, `file: ./Dockerfile`
  - `mirrormere-cast-watcher`: `context: .`, `file: ./sidecars/cast-watcher/Dockerfile`
  - `mirrormere-eink-renderer`: `context: ./sidecars/eink-renderer`, `file: ./sidecars/eink-renderer/Dockerfile`
- All three container images are compiled for `platforms: linux/amd64,linux/arm64`.
- CI runs QEMU (`docker/setup-qemu-action@v3`) and Buildx (`docker/setup-buildx-action@v3`) with distinct GHA cache scopes (`scope=${{ matrix.name }}`), verifying cross-compilation on every pull request and publishing multi-arch manifests to GHCR on merge to `main`.

### E. Automated Bench Smoke Verifier (`scripts/bench-verify.sh`)
- Authoritative POSIX shell verification engine with zero external dependencies beyond `curl`.
- Supports CLI flags:
  - `--profile <core|video|eink|all>` (default: `all`)
  - `--wait` with configurable `--timeout <seconds>` (default: 30) and `--interval <seconds>` (default: 2)
  - Custom base URL overrides (`--core-url`, `--eink-url`, `--go2rtc-url`, `--cast-url`)
- Probe contracts:
  1. Core Daemon: `GET ${CORE_URL}/healthz` (verifies HTTP 200) and `GET ${CORE_URL}/display` (verifies HTTP 200 HTML content).
  2. E-Ink Renderer: `GET ${EINK_URL}/healthz` (verifies HTTP 200) and `GET ${EINK_URL}/eink.png` (verifies HTTP 200 image/png).
  3. Video Profile: `GET ${GO2RTC_URL}/` (verifies HTTP 200 web gateway), `GET ${CAST_WATCHER_URL}/healthz` (verifies HTTP 200), and `POST ${CAST_WATCHER_URL}/action` (verifies webhook responsiveness).

---

## 3. Invariants Maintained
1. **Zero Runtime Switching Overhead (SPEC-001 §3)**: Profiles remain strictly compile-time/deployment-time constructs via Docker Compose profile assignments, avoiding runtime multi-tenant router complexity inside the Go daemon.
2. **Strict Hardware Isolation & Test Portability**: Developers and CI can run and test all container services on standard machines without requiring physical USB HDMI capture cards or e-paper SPI bonnets.
3. **Multi-Arch Parity**: Identical container artifacts are published for `linux/amd64` (Alex's N100) and `linux/arm64` (Mike's Raspberry Pi 4B).

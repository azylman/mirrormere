# ADR: Kiosk Host Provisioning, Wayland Cage Confinement & Power Lifecycle (SPEC-010, Issue #257)

## Status
Accepted

## Context & Problem Statement
SPEC-002 defines Profile A (Touch Kiosk) hardware: an Intel N100 Mini PC paired with a 15.6-inch 1080p capacitive touchscreen monitor in a VESA sandwich mount. SPEC-010 specifies the dedicated Linux host runtime environment and bootstrapping automation (`deploy/kiosk`).

Deploying an appliance-grade wall calendar kiosk presents several key systems challenges:
1. **Compositor & Client Confinement**: The kiosk must launch directly into fullscreen Wayland without window decorations, desktop environments, or accessibility overlay crashes, while retaining full Intel Iris GPU hardware acceleration for 60Hz CSS transforms and WebRTC video decoding.
2. **Wayland Startup Ordering**: Background daemons (such as `swayidle`) cannot launch before the Wayland compositor creates the display socket, or they crash immediately.
3. **Power Management & Dual Sleep Lifecycle**: The display must sleep after 10 minutes of daytime inactivity (waking instantly on capacitive touch) and enforce a strict night blackout from 23:00 to 06:00.
4. **Issue #257 (03:00 Restart Screen Flash)**: To prevent long-term DOM and memory drift from 24/7 WebRTC and media streaming, the browser process restarts at 03:00 daily during the night window. However, because `cage` boots with its DRM output enabled by default, an uncoordinated restart turns the screen back on at 3 AM.
5. **IPC Security & Privilege Separation**: The kiosk runs as an unprivileged `kiosk` user with hardware group access (`video`, `input`, `audio`, `render`), while system timers execute as root. Root tools like `wlr-randr` must safely communicate with the `kiosk` user's Wayland socket without permission errors.

## Decisions

### 1. Two-Stage Kiosk Session Architecture
We decouple host systemd management from Wayland session initialization into a two-stage launcher:
- **Stage 1 (`deploy/kiosk/launch.sh`)**: Executed by `mirrormere-kiosk.service`. Validates `$XDG_RUNTIME_DIR`, exports Wayland session variables, and executes `exec cage -s -- /opt/mirrormere/kiosk/session.sh`.
- **Stage 2 (`deploy/kiosk/session.sh`)**: Executed *inside* the Cage Wayland compositor. Because `$WAYLAND_DISPLAY` is active, it launches `swayidle` in the background with signal traps (`EXIT INT TERM`), detects the active display output via `wlr-randr`, and `exec`s Chromium with hardware-accelerated kiosk flags. Because `swayidle` creates no Wayland surfaces, Cage confines Chromium as the sole fullscreen window under `-s`.

### 2. Chromium Kiosk Flags & D-Bus Secret Suppression
Chromium is launched with flags enforcing true kiosk mode:
- `--kiosk`, `--noerrdialogs`, `--disable-infobars`, `--disable-session-crashed-bubble`, `--disable-translate`
- `--ozone-platform=wayland`, `--use-gl=egl`, `--enable-features=OverlayScrollbar`
- `--autoplay-policy=no-user-gesture-required` (enables instant WebRTC audio/video playback)
- `--password-store=basic` (prevents hangs and error storms on minimal headless servers lacking GNOME Keyring / KWallet D-Bus secret services)

### 3. Resolution of Issue #257 via Coordinated Nightly Restart
To eliminate the 3 AM screen flash during nightly memory refreshes:
- `mirrormere-kiosk-restart.timer` triggers `mirrormere-kiosk-restart.service` at 03:00 daily.
- The service calls `/opt/mirrormere/kiosk/dpms.sh night-restart`.
- `dpms.sh` restarts `mirrormere-kiosk.service`, polls for Wayland socket readiness (up to 10 seconds), and immediately re-asserts `wlr-randr --output ${OUTPUT} --off`.
- This ensures the display remains completely dark while Chromium reloads, rehydrates state from `/api/events`, and pre-warms off-screen.

### 4. Privilege Isolation via `runuser` in `dpms.sh`
When systemd timer units execute `dpms.sh` as root:
- Commands (`on`, `off`, `status`, `night-restart`) resolve the kiosk UID and invoke `runuser -u kiosk -- env XDG_RUNTIME_DIR=/run/user/<uid> WAYLAND_DISPLAY=wayland-0 wlr-randr ...`.
- This guarantees `libwayland` accesses the correct unprivileged IPC socket without permission rejections.

### 5. Logind Seat Integration & Getty Deconfliction
Rather than running an auto-login getty on TTY1 that races with Wayland, `mirrormere-kiosk.service` declares:
- `Conflicts=getty@tty1.service`
- `TTYPath=/dev/tty1`
- `PAMName=login`
- `StandardInput=tty`
`install.sh` enables logind lingering via `loginctl enable-linger kiosk`, ensuring `/run/user/<uid>` and user PipeWire services stay alive indefinitely.

## Technical Rationale
- **Single-Writer Compositor Model**: Cage provides the minimal possible Wayland footprint (~15MB RAM) with zero desktop bloat, stripping all window chrome and menus.
- **Appliance Reliability**: Nightly browser restarts ensure zero cumulative memory leaks from long-running SSE connections or WebRTC peer connections. Re-asserting DPMS off guarantees bedroom/hallway installations are never disturbed at night.
- **Zero Host Audio Loopback**: Chromium routes WebRTC audio directly to PipeWire HDMI sinks, honoring the 80% DOM software volume ceiling without host ALSA/PipeWire volume hacking.

## Verification
- Validated with ShellCheck on all shell scripts (`launch.sh`, `session.sh`, `dpms.sh`, `install.sh`) with zero warnings.
- Go unit test suite in `internal/kiosk` verifying INI parser, systemd unit definitions, timer schedules, and script syntax with 97.8% statement test coverage.
- Full monorepo pre-flight verification passed via `./scripts/verify.sh --staged`.

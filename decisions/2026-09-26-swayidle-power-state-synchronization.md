# ADR: swayidle Power State Synchronization for Sleep, Wake, and Restart Timers

## Context & Problem Statement
In SPEC-010 ("Power Management & Sleep Lifecycle"), Mirrormere Touch Kiosk defines a dual sleep model:
1. Daytime inactivity sleep after 10 minutes of no user activity via `swayidle`, waking on tap via capacitive touchscreen `libinput` events.
2. A fixed night blackout window (23:00 to 06:00) where display power is cut, while preserving tap-to-wake for 10 minutes during the night.
3. An automated nightly browser memory refresh at 03:00 that restarts `mirrormere-kiosk.service` while keeping the display dark.

In Issue #280, Mike Carmody identified that `dpms.sh` executed `wlr-randr` directly without coordinating with `swayidle`. Because `swayidle` only fires its `resume` command when user activity is detected while in an internally "idled" state (after its `timeout` command has run), state drift occurred across three scenarios:
- **Night Sleep Drift (23:00)**: If activity occurred in the 10 minutes prior to 23:00, `swayidle` was not in an idled state when `dpms.sh off` turned the monitor off. Taps on the screen did not fire `resume`, leaving the screen dark for up to 10 minutes until `swayidle` timed out.
- **Nightly Restart Drift (03:00)**: `mirrormere-kiosk-restart.service` launched a fresh `swayidle` with an active 10-minute timer. Taps between 03:00 and 03:10 occurred while `swayidle` was in the "active" state, failing to fire `resume`.
- **Morning Wake Drift (06:00)**: At 06:00, `dpms.sh on` turned the display on while `swayidle` was in the "idled" state. Because `swayidle` only fires its timeout once per idle cycle, the screen remained powered on indefinitely without entering daytime inactivity sleep if nobody was in the room.

## Decision
1. **Process Supervision in `deploy/kiosk/session.sh`**:
   - Wrapped `swayidle` in a supervisor loop (`run_swayidle`) that runs in the background of the Cage Wayland session.
   - When `swayidle` terminates cleanly or is signaled to reset, the supervisor loop respawns a fresh `swayidle` process after a brief 0.5s backoff.
   - Session `cleanup()` cleanly terminates both the supervisor subshell and any active child `swayidle` daemon instances.

2. **State Synchronization in `deploy/kiosk/dpms.sh`**:
   - **Night Blackout (`dpms.sh off`)**: In addition to powering off the output via `wlr-randr --off`, dispatches `SIGUSR1` to `swayidle` (`trigger_swayidle_idle`). Per `swayidle(1)`, `SIGUSR1` immediately triggers the idle timeout command and transitions `swayidle` into the idle state without terminating it. Subsequent capacitive taps during the night immediately invoke `resume` (`wlr-randr --on`).
   - **Morning Wake (`dpms.sh on`)**: In addition to powering on the output via `wlr-randr --on`, dispatches `SIGTERM` to `swayidle` (`reset_swayidle_active`). The supervisor loop in `session.sh` respawns a fresh `swayidle` starting in the active state with a 10-minute timeout. If the room is unoccupied after 06:00, the display automatically idle-blanks at 06:10.
   - **Nightly Restart (`dpms.sh night-restart`)**: After restarting `mirrormere-kiosk.service` and asserting `wlr-randr --off`, waits for `swayidle` to initialize and immediately dispatches `SIGUSR1`, ensuring night taps between 03:00 and 06:00 wake the screen.

3. **Validation & Automated Tests (`internal/kiosk`)**:
   - Added `ValidateDPMSScript` and `ValidateSessionScript` to verify that `deploy/kiosk/dpms.sh` and `deploy/kiosk/session.sh` contain the required coordination directives (`trigger_swayidle_idle`, `reset_swayidle_active`, `SIGUSR1`, `SIGTERM`, `run_swayidle`, `trap cleanup`).
   - Integrated directive validation into `TestDeployKioskFiles`, preserving 98.1% statement test coverage in `internal/kiosk`.

## Consequences & Alternatives Considered
- **Replacing systemd timers with a monolithic sleep/wake daemon in `session.sh`**: Rejected because systemd timers provide persistent clock-aligned scheduling, persistent journal logging, and easy operational inspection via `systemctl list-timers`.
- **Positive**: Complete convergence between real display output power and `swayidle` internal state. Eliminates black screen lockups on night taps and prevents unmonitored display illumination after 06:00.

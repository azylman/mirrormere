# Architecture Decision Record: E-Ink Node Hardware SPI Driver, Bonnet Buttons, and Offline Guard

- **Date:** 2026-09-26
- **Status:** Accepted
- **Context:** Implementing Issue #247 on `azylman/mirrormere` (SPEC-013 Phase 5, Chunk 5.3), fulfilling SPEC-009 §4-§8.

---

## 1. Problem Statement & Motivation

Operating ambient electronic paper displays on battery-backed or headless embedded hardware (Raspberry Pi Profile B) imposes rigorous hardware and interface constraints:

1. **Hardware Pin Mapping Divergence**: The Adafruit e-paper Bonnet for Raspberry Pi wires hardware control lines to non-standard BCM GPIO pins (`RST=27, DC=22, BUSY=17, CS=CE0/GPIO8, PWR=None`), whereas standard Waveshare HAT reference drivers assume `RST=17, DC=25, CS=8, BUSY=24`. Maintaining a forked copy of vendor C or Python drivers creates unnecessary maintenance debt. Dynamic pin patching is required to configure upstream drivers at runtime.
2. **E-Paper Refresh Lifecycle Invariants**: Waveshare 7.5" V2 panels require strict refresh scheduling to prevent DC balance degradation, physical burnout, and optical ghosting:
   - Initial write on startup must always be a **full refresh** to establish a known baseline waveform.
   - Subsequent writes utilize fast **partial refreshes** for responsive updates.
   - To eliminate cumulative ghosting artifacts, a **full refresh** must be enforced if 30 consecutive partial refreshes occur or if 60 minutes elapse since the last full refresh.
   - Electronic paper panels must enter **deep sleep** (`epd.sleep()`) immediately following every write cycle to cut power consumption and protect the TFT backplane.
3. **Stale Data Ambiguity & Offline Disconnect Guard**: Because electronic paper is bistable and retains images indefinitely without power, an offline node displays static data without any indication that data is hours or days out of date. Transient network interruptions must not cause visual churn, but persistent network outages (> 120s grace period) require a visible, unobtrusive disconnect indicator: an 8×8 black square stamped into the top-right corner (`x ∈ [792, 799]`, `y ∈ [0, 7]`) rendered via a single partial refresh and persisted across reboots. On reconnection, an automatic full refresh removes the dot and clears ghosting.
4. **Hardware Navigation & Diagnostic Telemetry**: Ambient wall displays benefit from physical controls (tactile buttons on the Adafruit Bonnet: GPIO 5 for manual full refresh; GPIO 6 for next-screen rotation) and a lightweight local HTTP health endpoint (`:8099/healthz`) for operational observability without complex external agents.

---

## 2. Decision & Architecture

Mirrormere completes Chunk 5.3 of the E-Ink Display Node with modular, testable components under `clients/eink-node/`:

### A. Dynamic Bonnet Pin Patching & Panel Manager (`panel.py`)
- **Dynamic Pin Patching (`patch_bonnet_pins`)**: Rather than modifying third-party vendor code, the client dynamically inspects and patches pin constants on the underlying Waveshare `epdconfig` module before initialization (`RST_PIN = 27`, `DC_PIN = 22`, `BUSY_PIN = 17`, `CS_PIN = 8`).
- **Pluggable Driver Abstraction (`BasePanel`, `FakePanel`, `WaveshareEPDPanel`)**: Defines an abstract hardware interface with `init()`, `display_full()`, `display_partial()`, `sleep()`, and `clear()`. A high-fidelity in-memory `FakePanel` captures all written frames and tracks lifecycle metrics (`full_refreshes`, `partial_refreshes`, `sleeps`, `clears`) for fast, deterministic unit testing without physical SPI hardware.
- **Refresh Lifecycle & Sleep Enforcement (`PanelManager`)**:
  - Automatically converts writes to full refreshes when `consecutive_partials >= 30` or `now - last_full_refresh_time >= full_refresh_minutes * 60`.
  - Guarantees invocation of `panel.sleep()` in a `finally:` block following every successful or failed write.
  - Honors `clear_on_shutdown: true` by issuing a full clear and deep sleep on daemon termination.

### B. Pure-Python Image Validation & Dot Stamping (`image.py`)
- **Strict 1-Bit PNG Validation (`validate_png`)**: Inspects PNG magic headers and IHDR chunks to enforce exact 800×480 resolution, 1-bit monochrome bit depth, and color type 0 (greyscale). Rejecting non-conforming frames upstream prevents corrupted SPI transfers and hardware lockup.
- **Pure-Python Scanline Dot Stamping (`stamp_offline_dot`)**: Decompresses IDAT scanlines using standard library `zlib`, locates rows 0 through 7, and sets byte 99 (the final 8 pixels of each 800-pixel row) to `0x00` (black). The scanlines are recompressed and reassembled with a valid CRC32 into a compliant 1-bit PNG. This eliminates external runtime dependencies on Pillow/PIL for headless appliances.
- **Atomic Persistence (`save_last_image`, `load_last_image`)**: Persists frame buffers to `/var/lib/mirrormere-eink/last.png` using atomic temporary file renaming (`replace()`), ensuring cold boots during network outages retain the dotted disconnect screen.

### C. Offline Disconnection Guard (`offline.py`)
- **Grace Period Monitoring (`OfflineGuard`)**: Continuously tracks health timestamps from SSE events and HTTP fetches. If connectivity is lost for longer than `offline_grace_seconds` (120s), it stamps the 8×8 black square on the cached frame buffer and dispatches a single partial refresh to the panel.
- **Reconnection Recovery**: When the SSE event bus reconnects or a fresh image is fetched, `record_healthy()` detects the recovery from offline status and triggers a full refresh to clear the dot and refresh all widgets.

### D. Hardware Buttons & Local Observability (`buttons.py`, `health.py`)
- **Tactile Button Handlers (`ButtonHandler`)**: Uses `gpiozero.Button` (with fallback mock support for non-Pi environments) configured with 200ms software debouncing:
  - **Button 1 (GPIO 5)**: Dispatches a forced full panel refresh.
  - **Button 2 (GPIO 6)**: Dispatches an HTTP `POST /api/screen/advance` mutation to Mirrormere Core to advance the active rotation screen.
- **Health Telemetry Server (`HealthServer`, `HealthHTTPServer`)**: Exposes `GET :8099/healthz` responding with JSON state detailing connection status, uptime, partial refresh counts, offline indicators, and last ETag. Implemented via standard library `http.server` with zero external dependencies.

### E. Systemd Service Deployment (`deploy/eink/mirrormere-eink.service`)
- Provides an automated, hardened systemd unit configured with `Restart=always`, `RestartSec=5s`, non-root user execution with `gpio` and `spi` group memberships, and `ReadWritePaths=/var/lib/mirrormere-eink` for unattended appliance operation.

---

## 3. References

- `specs/009-eink-display-node.md` (§4: Refresh Lifecycle, §5: Hardware Pinout, §6: Offline Behavior, §7: Hardware Buttons, §8: Observability)
- `specs/013-implementation-roadmap.md` (Phase 5, Task 5.3)
- `decisions/2026-09-26-eink-node-client-sse-coalescing-and-etag-fetcher.md` (Chunk 5.2 ADR)

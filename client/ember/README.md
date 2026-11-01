# EMBER (**E**mbedded **M**irrormere **B**rowser & **E**dge **R**untime)

**EMBER** is a lightweight, native Android kiosk wrapper and edge hardware runtime purpose-built for **HEARTH** (`azylman/mirrormere`) wall displays (such as the batteryless 21.5" **ApoloSign** Android 16 frame).

Unlike heavy legacy MDM kiosk browsers that hook low-level Wi-Fi roaming APIs and crash Rockchip Android 16 network stacks, **EMBER** uses clean, standard Android platform APIs (`WebView`, `Camera2`, `WindowInsetsController`, and `AudioRecord`) to deliver a zero-chrome wall appliance.

## Core Features

- **Edge-to-Edge Immersive WebView:** Loads the live **HEARTH** display (`/display?hide_cursor=true`) with hardware acceleration, persistent DOM storage, automatic network-drop recovery, and system bar suppression (`WindowInsetsCompat.Type.systemBars()`).
- **On-Device Front-Camera Motion Wake & Night Dimming:** Samples low-resolution luma frames (`160x120` Y-plane via `Camera2` `ImageReader`) every 2 seconds exclusively in memory:
  - **Ambient Darkness & Idle Sleep:** When average room luminance drops below the night threshold or zero frame-to-frame motion is detected for the idle timeout, EMBER fades in a pure-black hardware overlay and drops window screen brightness to `0.01f` (`layoutParams.screenBrightness = 0.01f`) while keeping Wi-Fi and SSE streams alive.
  - **Instant Motion Wake:** When frame-to-frame pixel delta exceeds the motion threshold, EMBER restores normal display brightness immediately.
- **Built-in Night Quiet-Hours Schedule:** Periodic 60-second schedule evaluation in `MainActivity` (`NIGHT_CHECK_INTERVAL_MS = 60_000L`) evaluating local time. Defaults to quiet hours from **00:00 to 06:30** (configurable via `night_start` and `night_end` intent extras in `HH:MM` format and persisted across reboots in `SharedPreferences`). Tailored for camera-less displays (such as the 21.5" ApoloSign) where camera-based darkness detection is unavailable:
  - **Automatic Overnight Dimming:** Dims screen brightness to 1% (`0.01f`) with a black hardware overlay during quiet hours while maintaining `FLAG_KEEP_SCREEN_ON` so Android never kills Wi-Fi or WebView SSE connections.
  - **Touch Wake with Auto-Sleep:** Any touch interaction during quiet hours wakes the display for 15 minutes before automatically re-dimming back to sleep.
  - **Automatic Morning Wake:** Transitions out of night quiet hours at 06:30 AM, automatically waking the display back to full daytime brightness.
- **Guarded JavaScript Hardware Bridge & OTA Installer:** Exposes `window.EmberBridge` for display sleep (`sleep()`), wake (`wake()`), brightness adjustments (`setBrightness(level)`), night schedule updates (`setNightSchedule(start, end)`), and silent over-the-air APK upgrades (`installOta(apkUrl)`). All methods are origin-guarded:
  - **Configurable Origin Allowlist:** Auto-trusts the origin of the configured `start_url` (e.g., `https://hearth.local` or `http://kiosk.local:8080`). External users can configure additional trusted origins via `bridge_origins` intent extras or `--bridge-origins` CLI flags without modifying code.
  - **OTA Enablement Guard:** Over-the-air APK installation is toggled via `ota_enabled` intent extra / CLI flag (default `true`). Untrusted domains or non-APK payloads are rejected.
  - **Mixed Content Security:** Web mixed content mode is controlled via `allow_mixed_content` intent extra / CLI flag (defaults to strict `MIXED_CONTENT_NEVER_ALLOW` unless explicitly enabled).
- **Native Home Launcher & Boot Receiver:** Declares `CATEGORY_HOME` and `BOOT_COMPLETED` intent filters so the display boots straight into **HEARTH** after power outages without manual interaction.
- **Persistent Quad-Mic Voice Bridge:** Pre-grants and bridges WebView `onPermissionRequest` (`RESOURCE_AUDIO_CAPTURE` / `RESOURCE_VIDEO_CAPTURE`) so HEARTH voice interactions work without recurring browser popups.

## Quickstart & Deployment

```bash
# Run local manifest, config, and motion-algorithm verification
pytest tests/test_ember.py

# Deploy the latest CI-built APK to a connected Android frame over ADB
python3 tools/ember_deploy.py --device 198.51.100.10:5555 --url "https://hearth.local/display?hide_cursor=true"

# Deploy with custom start URL, extra bridge/OTA origins, and OTA enabled
python3 tools/ember_deploy.py \
  --device 198.51.100.10:5555 \
  --url "https://frame.example.com:8080/dashboard" \
  --bridge-origins "https://updates.example.com" \
  --ota-enabled \
  --no-allow-mixed-content

# Configure start_url, bridge_origins, ota_enabled, allow_mixed_content, and quiet hours via ADB intent extras:
adb shell am start -n ventures.brock.ember/.MainActivity \
  --es start_url "https://frame.example.com:8080/dashboard" \
  --es bridge_origins "https://updates.example.com" \
  --ez ota_enabled true \
  --ez allow_mixed_content false \
  --es night_start "00:00" \
  --es night_end "06:30"
```

### Hands-Off ADB Disconnect & Operational Safety
- **Immediate Post-Deploy Disconnect:** `tools/ember_deploy.py` executes `disconnect_device` (`adb disconnect <device>`) immediately after APK installation, permission grants, and kiosk launch. This guarantees that wireless ADB sessions never remain attached to the frame.
- **Zero Legacy TCP/IP Mode Switching:** Never executes `adb tcpip 5555` or forces socket re-binds that destabilize Rockchip Android 16 network stacks.
- **Zero Power-Service Polling:** Eliminates all recurring ADB shell battery/power service queries in favor of autonomous on-device schedule management in `MainActivity`.

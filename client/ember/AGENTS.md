# AGENTS.md — EMBER (**E**mbedded **M**irrormere **B**rowser & **E**dge **R**untime)

## Overview
Native Android kiosk wrapper (`ventures.brock.ember`) and ADB deployment controller for **HEARTH** (`azylman/mirrormere`) wall frames.

## Verification & Build Commands
```bash
ruff check tools/ tests/ && pytest tests/test_ember.py && git status -s
```

## Module Map
- `app/src/main/AndroidManifest.xml` — Android manifest declaring `HOME` launcher category, `BOOT_COMPLETED` receiver, and camera/audio/network permissions (never `CHANGE_WIFI_STATE`).
- `app/src/main/java/ventures/brock/ember/MainActivity.kt` — Immersive fullscreen `WebView` host, screen-brightness sleep/wake controller, and runtime URL configuration via intent extras / `SharedPreferences`.
- `app/src/main/java/ventures/brock/ember/MotionLumaMonitor.kt` — `Camera2` YUV_420_888 Y-plane motion and ambient luminance analyzer.
- `app/src/main/java/ventures/brock/ember/BootReceiver.kt` — Auto-launches `MainActivity` on `ACTION_BOOT_COMPLETED`.
- `.github/workflows/ci.yml` — GitHub Actions workflow compiling the debug/release APK via Android SDK on `ubuntu-latest` and running `pytest`.
- `tools/ember_deploy.py` — CLI utility to fetch the built APK artifact, install via ADB, configure the start URL, and verify package state.
- `tests/test_ember.py` — Unit test suite validating manifest permissions (strict ban on Wi-Fi state mutation permissions), motion/luma math, and ADB deployment commands.

## Architectural Invariants
1. **Zero Wi-Fi Stack Mutation:** EMBER must NEVER request or invoke `CHANGE_WIFI_STATE`, `ACCESS_FINE_LOCATION` Wi-Fi scans, or `WifiManager` lock mutations that destabilize Rockchip Android 16 Wi-Fi drivers.
2. **Soft Backlight Sleep over Device Doze:** Night/idle sleep must dim `window.attributes.screenBrightness` to `0.01f` and display a black overlay while keeping `FLAG_KEEP_SCREEN_ON` active so the OS never kills Wi-Fi or WebView SSE connections.
3. **Zero Hardcoded Private IPs:** Default fallback URL in source code must use a generic placeholder (`https://hearth.local/display?hide_cursor=true`); custom LAN URLs are injected at runtime via ADB intent extras (`--es start_url ...`) and persisted in `SharedPreferences`.

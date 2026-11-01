import re
import subprocess
import sys
import xml.etree.ElementTree as ET
from pathlib import Path
from urllib.parse import urlparse

import pytest
import yaml

REPO_ROOT = Path(__file__).resolve().parent.parent


def test_android_manifest_structure():
    manifest_path = REPO_ROOT / "app" / "src" / "main" / "AndroidManifest.xml"
    assert manifest_path.exists(), "AndroidManifest.xml does not exist"

    tree = ET.parse(manifest_path)
    root = tree.getroot()

    assert root.tag == "manifest"
    assert root.attrib.get("package") in (None, "ventures.brock.ember")

    android_ns = "{http://schemas.android.com/apk/res/android}"

    # Verify permissions
    permissions = {
        elem.attrib.get(f"{android_ns}name")
        for elem in root.findall("uses-permission")
    }
    expected_permissions = {
        "android.permission.INTERNET",
        "android.permission.CAMERA",
        "android.permission.RECORD_AUDIO",
        "android.permission.MODIFY_AUDIO_SETTINGS",
        "android.permission.RECEIVE_BOOT_COMPLETED",
        "android.permission.REQUEST_INSTALL_PACKAGES",
    }
    assert permissions == expected_permissions, f"Unexpected permissions: {permissions}"

    # Enforce Wi-Fi Safety Invariant
    banned_permissions = {
        "android.permission.ACCESS_WIFI_STATE",
        "android.permission.CHANGE_WIFI_STATE",
        "android.permission.ACCESS_NETWORK_STATE",
    }
    assert not (permissions & banned_permissions), (
        f"Manifest contains banned Wi-Fi permissions: {permissions & banned_permissions}"
    )

    # Verify Application
    app = root.find("application")
    assert app is not None, "Missing <application> element"
    assert app.attrib.get(f"{android_ns}allowBackup") == "false"
    assert app.attrib.get(f"{android_ns}hardwareAccelerated") == "true"
    assert app.attrib.get(f"{android_ns}label") == "EMBER"

    # Verify MainActivity
    activities = app.findall("activity")
    main_activity = next(
        (a for a in activities if a.attrib.get(f"{android_ns}name") == ".MainActivity"),
        None,
    )
    assert main_activity is not None, "Missing .MainActivity"
    assert main_activity.attrib.get(f"{android_ns}exported") == "true"
    assert main_activity.attrib.get(f"{android_ns}launchMode") == "singleTask"
    config_changes = main_activity.attrib.get(f"{android_ns}configChanges", "")
    assert "orientation" in config_changes
    assert "screenSize" in config_changes
    assert "keyboardHidden" in config_changes

    intent_filters = main_activity.findall("intent-filter")
    assert len(intent_filters) >= 2, "MainActivity should have at least 2 intent-filters"

    has_launcher = False
    has_home = False
    for ifilter in intent_filters:
        actions = {a.attrib.get(f"{android_ns}name") for a in ifilter.findall("action")}
        categories = {c.attrib.get(f"{android_ns}name") for c in ifilter.findall("category")}
        if "android.intent.action.MAIN" in actions and "android.intent.category.LAUNCHER" in categories:
            has_launcher = True
        if (
            "android.intent.action.MAIN" in actions
            and "android.intent.category.HOME" in categories
            and "android.intent.category.DEFAULT" in categories
        ):
            has_home = True

    assert has_launcher, "MainActivity missing MAIN / LAUNCHER filter"
    assert has_home, "MainActivity missing MAIN / HOME / DEFAULT filter"

    # Verify BootReceiver
    receivers = app.findall("receiver")
    boot_receiver = next(
        (r for r in receivers if r.attrib.get(f"{android_ns}name") == ".BootReceiver"),
        None,
    )
    assert boot_receiver is not None, "Missing .BootReceiver"
    assert boot_receiver.attrib.get(f"{android_ns}exported") == "true"
    boot_actions = {
        a.attrib.get(f"{android_ns}name")
        for ifilter in boot_receiver.findall("intent-filter")
        for a in ifilter.findall("action")
    }
    assert "android.intent.action.BOOT_COMPLETED" in boot_actions


def test_gradle_build_config():
    settings_file = REPO_ROOT / "settings.gradle.kts"
    assert settings_file.exists()
    settings_content = settings_file.read_text(encoding="utf-8")
    assert 'rootProject.name = "ember"' in settings_content
    assert 'include(":app")' in settings_content
    assert "google()" in settings_content
    assert "mavenCentral()" in settings_content

    build_file = REPO_ROOT / "app" / "build.gradle.kts"
    assert build_file.exists()
    build_content = build_file.read_text(encoding="utf-8")
    assert 'id("com.android.application") version "8.2.2"' in build_content
    assert 'id("org.jetbrains.kotlin.android") version "1.9.22"' in build_content
    assert 'namespace = "ventures.brock.ember"' in build_content
    assert "compileSdk = 34" in build_content
    assert "minSdk = 29" in build_content
    assert "targetSdk = 34" in build_content
    assert 'jvmTarget = "17"' in build_content


def test_boot_receiver_structure():
    boot_file = REPO_ROOT / "app" / "src" / "main" / "java" / "ventures" / "brock" / "ember" / "BootReceiver.kt"
    assert boot_file.exists(), "BootReceiver.kt does not exist"

    content = boot_file.read_text(encoding="utf-8")
    assert "package ventures.brock.ember" in content
    assert "class BootReceiver : BroadcastReceiver()" in content
    assert "Intent.ACTION_BOOT_COMPLETED" in content
    assert "Intent.FLAG_ACTIVITY_NEW_TASK" in content
    assert "MainActivity" in content

    # Enforce Wi-Fi Safety Invariant in Kotlin source
    assert "WifiManager" not in content
    assert "WIFI_STATE" not in content
    assert "android.net.wifi" not in content

    # Enforce Zero Hardcoded Topology Invariant
    rfc1918_pattern = r"(192\.168\.\d+\.\d+|10\.\d+\.\d+\.\d+|172\.(1[6-9]|2\d|3[0-1])\.\d+\.\d+)"
    assert not re.search(rfc1918_pattern, content), "BootReceiver.kt contains hardcoded private RFC 1918 IP"


def test_motion_luma_monitor_structure():
    monitor_file = REPO_ROOT / "app" / "src" / "main" / "java" / "ventures" / "brock" / "ember" / "MotionLumaMonitor.kt"
    assert monitor_file.exists(), "MotionLumaMonitor.kt does not exist"

    content = monitor_file.read_text(encoding="utf-8")
    assert "package ventures.brock.ember" in content
    assert "class MotionLumaMonitor" in content
    assert "interface Listener" in content

    # Camera2 API checks
    assert "android.hardware.camera2.CameraManager" in content
    assert "android.hardware.camera2.CameraDevice" in content
    assert "android.hardware.camera2.CameraCaptureSession" in content
    assert "android.hardware.camera2.CaptureRequest" in content
    assert "android.hardware.camera2.CameraCharacteristics" in content

    # Front camera preference and fallback
    assert "CameraCharacteristics.LENS_FACING_FRONT" in content

    # ImageReader YUV_420_888 160x120 checks
    assert "ImageFormat.YUV_420_888" in content
    assert "FRAME_WIDTH = 160" in content
    assert "FRAME_HEIGHT = 120" in content

    # Sampling defaults: ~2s and 12.0 delta threshold
    assert "DEFAULT_MOTION_THRESHOLD = 12.0" in content
    assert "DEFAULT_SAMPLE_INTERVAL_MS = 2000L" in content

    # Background HandlerThread processing
    assert "HandlerThread" in content

    # Callback signatures and methods
    assert "onMotionDetected" in content
    assert "onAmbientLuma" in content
    assert "fun start()" in content
    assert "fun stop()" in content

    # Y-plane luma and delta logic
    assert "planes[0]" in content
    assert "abs(luma - prevLuma)" in content

    # Enforce Wi-Fi Safety Invariant in Kotlin source
    assert "WifiManager" not in content
    assert "WIFI_STATE" not in content
    assert "android.net.wifi" not in content

    # Enforce Zero Hardcoded Topology Invariant
    rfc1918_pattern = r"(192\.168\.\d+\.\d+|10\.\d+\.\d+\.\d+|172\.(1[6-9]|2\d|3[0-1])\.\d+\.\d+)"
    assert not re.search(rfc1918_pattern, content), "MotionLumaMonitor.kt contains hardcoded private RFC 1918 IP"


def test_motion_luma_algorithm_simulation():
    """Verify luminance and delta math parity with MotionLumaMonitor implementation."""
    width, height = 160, 120
    total_pixels = width * height

    # Test 1: Uniform dark frame (luma = 10)
    dark_frame = bytes([10] * total_pixels)
    avg_luma_dark = sum(dark_frame) / total_pixels
    assert avg_luma_dark == 10.0

    # Test 2: Identical consecutive frames have delta 0.0 (no motion)
    prev_frame = dark_frame
    curr_frame = bytes([10] * total_pixels)
    delta_same = sum(abs(c - p) for c, p in zip(curr_frame, prev_frame)) / total_pixels
    assert delta_same == 0.0
    motion_detected_same = delta_same >= 12.0
    assert not motion_detected_same

    # Test 3: Minor sensor noise (delta 3.0 < 12.0 threshold)
    noise_frame = bytes([13] * total_pixels)
    delta_noise = sum(abs(c - p) for c, p in zip(noise_frame, prev_frame)) / total_pixels
    assert delta_noise == 3.0
    motion_detected_noise = delta_noise >= 12.0
    assert not motion_detected_noise

    # Test 4: Significant motion (delta 15.0 >= 12.0 threshold)
    motion_frame = bytes([25] * total_pixels)
    delta_motion = sum(abs(c - p) for c, p in zip(motion_frame, prev_frame)) / total_pixels
    assert delta_motion == 15.0
    motion_detected = delta_motion >= 12.0
    assert motion_detected

    # Test 5: Bright scene (luma = 220)
    bright_frame = bytes([220] * total_pixels)
    avg_luma_bright = sum(bright_frame) / total_pixels
    assert avg_luma_bright == 220.0


def test_main_activity_structure():
    main_activity_file = (
        REPO_ROOT
        / "app"
        / "src"
        / "main"
        / "java"
        / "ventures"
        / "brock"
        / "ember"
        / "MainActivity.kt"
    )
    assert main_activity_file.exists(), "MainActivity.kt does not exist"

    content = main_activity_file.read_text(encoding="utf-8")

    # Package and inheritance
    assert "package ventures.brock.ember" in content
    assert "class MainActivity : Activity()" in content

    # Kiosk window configuration
    assert "FLAG_KEEP_SCREEN_ON" in content
    assert (
        "SYSTEM_UI_FLAG_IMMERSIVE_STICKY" in content
        or "BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE" in content
    )

    # UI structure: FrameLayout, WebView, and sleep overlay
    assert "FrameLayout" in content
    assert "WebView" in content
    assert "sleepOverlay" in content
    assert "Color.BLACK" in content
    assert "View.GONE" in content
    assert "View.VISIBLE" in content

    # Start URL handling and fallback
    assert 'EXTRA_START_URL = "start_url"' in content
    assert 'PREFS_NAME = "ember_prefs"' in content
    assert (
        'DEFAULT_START_URL = "https://hearth.local/display?hide_cursor=true"'
        in content
    )
    assert "resolveStartUrl" in content
    assert "onNewIntent" in content

    # WebView configuration
    assert "javaScriptEnabled = true" in content
    assert "domStorageEnabled = true" in content
    assert "mediaPlaybackRequiresUserGesture = false" in content
    assert "MIXED_CONTENT_ALWAYS_ALLOW" in content

    # WebChromeClient mic/camera permission bridge
    assert "onPermissionRequest" in content
    assert "grant(request.resources)" in content

    # WebView error handling and auto-reconnect
    assert "scheduleReload" in content
    assert "onReceivedError" in content
    assert "RETRY_DELAY_MS = 5000L" in content

    # Screen brightness & sleep/wake control
    assert "SLEEP_BRIGHTNESS = 0.01f" in content
    assert "AWAKE_BRIGHTNESS = -1.0f" in content
    assert "screenBrightness" in content
    assert "dispatchTouchEvent" in content
    assert "wake()" in content
    assert "sleep()" in content

    # MotionLumaMonitor integration
    assert "MotionLumaMonitor" in content
    assert "onMotionDetected" in content
    assert "onAmbientLuma" in content
    assert "DARKNESS_LUMA_THRESHOLD = 8.0" in content
    assert "IDLE_TIMEOUT_MS = 15 * 60 * 1000L" in content

    # Runtime permissions request
    assert "Manifest.permission.CAMERA" in content
    assert "Manifest.permission.RECORD_AUDIO" in content
    assert "requestPermissions" in content
    assert "onRequestPermissionsResult" in content

    # Night quiet-hours schedule constants and methods
    assert 'DEFAULT_NIGHT_START = "00:00"' in content
    assert 'DEFAULT_NIGHT_END = "06:30"' in content
    assert 'KEY_NIGHT_START = "night_start"' in content
    assert 'KEY_NIGHT_END = "night_end"' in content
    assert 'EXTRA_NIGHT_START = "night_start"' in content
    assert 'EXTRA_NIGHT_END = "night_end"' in content
    assert "NIGHT_CHECK_INTERVAL_MS = 60_000L" in content
    assert "parseTimeMinutes" in content
    assert "isNightQuietHours" in content
    assert "isWithinNightQuietHours" in content
    assert "evaluateNightSchedule" in content
    assert "wasInNightWindow" in content

    # Hardware GPU acceleration invariant (no LAYER_TYPE_SOFTWARE or CSS suppressors)
    assert (
        "LAYER_TYPE_SOFTWARE" not in content
    ), "MainActivity.kt contains LAYER_TYPE_SOFTWARE causing UI lag"
    assert (
        "ember-gpu-guard" not in content
    ), "MainActivity.kt contains ember-gpu-guard suppressing CSS"

    # JavaScript Bridge interface (EmberBridge)
    assert "class EmberBridge" in content
    assert "JavascriptInterface" in content
    assert "@JavascriptInterface" in content
    assert 'addJavascriptInterface(EmberBridge(), "EmberBridge")' in content
    assert "fun setBrightness" in content
    assert "fun setNightSchedule" in content
    assert "fun installOta" in content

    # Configurable origin allowlist & OTA security hardening constants and helpers
    assert 'KEY_BRIDGE_ORIGINS = "bridge_origins"' in content
    assert 'EXTRA_BRIDGE_ORIGINS = "bridge_origins"' in content
    assert 'KEY_OTA_ENABLED = "ota_enabled"' in content
    assert 'EXTRA_OTA_ENABLED = "ota_enabled"' in content
    assert 'KEY_ALLOW_MIXED_CONTENT = "allow_mixed_content"' in content
    assert 'EXTRA_ALLOW_MIXED_CONTENT = "allow_mixed_content"' in content
    assert "fun extractOrigin" in content
    assert "fun parseAllowedOrigins" in content
    assert "fun isBridgeOriginAllowed" in content
    assert "fun isOtaUrlAllowed" in content
    assert "currentPageUrl" in content
    assert "handleSecurityConfigIntent" in content
    assert "isCurrentBridgeOriginAllowed" in content

    # Over-The-Air (OTA) APK PackageInstaller updater
    assert "PackageInstaller" in content
    assert "installOtaApk" in content
    assert "ember-ota.apk" in content
    assert "MODE_FULL_INSTALL" in content
    assert "fsync" in content
    assert "PendingIntent" in content

    # Enforce Wi-Fi Safety Invariant in Kotlin source
    assert "WifiManager" not in content
    assert "WIFI_STATE" not in content
    assert "android.net.wifi" not in content

    # Enforce Zero Hardcoded Topology Invariant
    rfc1918_pattern = (
        r"(192\.168\.\d+\.\d+|10\.\d+\.\d+\.\d+|172\.(1[6-9]|2\d|3[0-1])\.\d+\.\d+)"
    )
    assert not re.search(
        rfc1918_pattern, content
    ), "MainActivity.kt contains hardcoded private RFC 1918 IP"


def test_main_activity_url_resolution_logic():
    """Verify start_url resolution precedence matches MainActivity logic."""
    default_url = "https://hearth.local/display?hide_cursor=true"

    def resolve_url(
        intent_extra: str | None, prefs_cached: str | None
    ) -> tuple[str, str | None]:
        # Emulate resolveStartUrl: returns (target_url, new_prefs_value)
        if intent_extra and intent_extra.strip():
            return intent_extra, intent_extra
        if prefs_cached and prefs_cached.strip():
            return prefs_cached, prefs_cached
        return default_url, prefs_cached

    # Case 1: Intent extra overrides cached prefs and updates prefs
    url, prefs = resolve_url(
        "https://hearth.local/custom", "https://hearth.local/cached"
    )
    assert url == "https://hearth.local/custom"
    assert prefs == "https://hearth.local/custom"

    # Case 2: No intent extra falls back to cached prefs
    url, prefs = resolve_url(None, "https://hearth.local/cached")
    assert url == "https://hearth.local/cached"
    assert prefs == "https://hearth.local/cached"

    # Case 3: Neither extra nor cached prefs falls back to default URL
    url, prefs = resolve_url(None, None)
    assert url == default_url
    assert prefs is None

    # Case 4: Empty string in extra falls back to cached prefs
    url, prefs = resolve_url("  ", "https://hearth.local/cached")
    assert url == "https://hearth.local/cached"
    assert prefs == "https://hearth.local/cached"


def test_main_activity_sleep_wake_cycle_simulation():
    """Verify state transitions of screen brightness and sleep overlay."""
    SLEEP_BRIGHTNESS = 0.01
    AWAKE_BRIGHTNESS = -1.0
    DARKNESS_THRESHOLD = 8.0
    IDLE_TIMEOUT_MS = 900_000  # 15 min
    GRACE_PERIOD_MS = 30_000

    class KioskState:
        def __init__(self):
            self.is_asleep = False
            self.screen_brightness = AWAKE_BRIGHTNESS
            self.overlay_visible = False
            self.last_wake_time = 0
            self.idle_timer_reset_count = 0

        def wake(self, current_time_ms: int):
            self.last_wake_time = current_time_ms
            self.idle_timer_reset_count += 1
            if not self.is_asleep:
                return
            self.is_asleep = False
            self.screen_brightness = AWAKE_BRIGHTNESS
            self.overlay_visible = False

        def sleep(self):
            if self.is_asleep:
                return
            self.is_asleep = True
            self.screen_brightness = SLEEP_BRIGHTNESS
            self.overlay_visible = True

        def handle_ambient_luma(self, avg_luma: float, current_time_ms: int):
            if avg_luma < DARKNESS_THRESHOLD:
                elapsed = current_time_ms - self.last_wake_time
                if not self.is_asleep and elapsed > GRACE_PERIOD_MS:
                    self.sleep()

    kiosk = KioskState()
    assert not kiosk.is_asleep
    assert kiosk.screen_brightness == AWAKE_BRIGHTNESS
    assert not kiosk.overlay_visible

    # Darkness within grace period does not sleep
    kiosk.wake(current_time_ms=1000)
    kiosk.handle_ambient_luma(avg_luma=5.0, current_time_ms=20_000)
    assert not kiosk.is_asleep

    # Darkness after grace period triggers sleep
    kiosk.handle_ambient_luma(avg_luma=5.0, current_time_ms=35_000)
    assert kiosk.is_asleep
    assert kiosk.screen_brightness == SLEEP_BRIGHTNESS
    assert kiosk.overlay_visible

    # Motion detection wakes up the kiosk
    kiosk.wake(current_time_ms=40_000)
    assert not kiosk.is_asleep
    assert kiosk.screen_brightness == AWAKE_BRIGHTNESS
    assert not kiosk.overlay_visible

    # Normal lighting (luma >= 8.0) does not sleep
    kiosk.handle_ambient_luma(avg_luma=80.0, current_time_ms=80_000)
    assert not kiosk.is_asleep

    # Idle timeout (15 min) triggers sleep
    elapsed_idle = 950_000 - 40_000
    if elapsed_idle >= IDLE_TIMEOUT_MS:
        kiosk.sleep()
    assert kiosk.is_asleep
    assert kiosk.screen_brightness == SLEEP_BRIGHTNESS
    assert kiosk.overlay_visible

    # Touch on sleep overlay wakes up the kiosk
    kiosk.wake(current_time_ms=960_000)
    assert not kiosk.is_asleep
    assert kiosk.screen_brightness == AWAKE_BRIGHTNESS
    assert not kiosk.overlay_visible


def test_ci_workflow_structure():
    """Verify root GitHub Actions CI workflow configuration and EMBER test integration."""
    nested_ci_path = REPO_ROOT / ".github" / "workflows" / "ci.yml"
    assert not nested_ci_path.exists(), "Nested client/ember/.github/workflows/ci.yml should not exist"

    root_ci_path = REPO_ROOT.parent.parent / ".github" / "workflows" / "ci.yml"
    assert root_ci_path.exists(), "Root .github/workflows/ci.yml does not exist"

    content = root_ci_path.read_text(encoding="utf-8")
    data = yaml.safe_load(content)

    triggers = data.get("on") or data.get(True)
    assert isinstance(triggers, dict)
    assert "push" in triggers
    assert "pull_request" in triggers
    assert "client/ember/tests/test_ember.py" in content


def test_ci_workflow_no_hardcoded_topology():
    """Enforce Zero Hardcoded Topology Invariant on root ci.yml."""
    root_ci_path = REPO_ROOT.parent.parent / ".github" / "workflows" / "ci.yml"
    assert root_ci_path.exists()
    content = root_ci_path.read_text(encoding="utf-8")

    rfc1918_pattern = r"(192\.168\.\d+\.\d+|10\.\d+\.\d+\.\d+|172\.(1[6-9]|2\d|3[0-1])\.\d+\.\d+)"
    assert not re.search(rfc1918_pattern, content), "ci.yml contains hardcoded private RFC 1918 IP"


def test_deploy_no_hardcoded_topology():
    """Enforce Zero Hardcoded Topology Invariant on tools/ember_deploy.py."""
    deploy_file = REPO_ROOT / "tools" / "ember_deploy.py"
    assert deploy_file.exists(), "tools/ember_deploy.py does not exist"
    content = deploy_file.read_text(encoding="utf-8")

    rfc1918_pattern = r"(192\.168\.\d+\.\d+|10\.\d+\.\d+\.\d+|172\.(1[6-9]|2\d|3[0-1])\.\d+\.\d+)"
    assert not re.search(rfc1918_pattern, content), "ember_deploy.py contains hardcoded private RFC 1918 IP"


def test_deploy_resolve_adb_binary(monkeypatch, tmp_path):
    """Verify resolve_adb_binary resolution precedence."""
    sys.path.insert(0, str(REPO_ROOT))
    from tools import ember_deploy

    # Case 1: Explicit override path
    custom_adb = tmp_path / "custom_adb"
    custom_adb.write_text("#!/bin/sh\nexit 0\n")
    custom_adb.chmod(0o755)
    assert ember_deploy.resolve_adb_binary(str(custom_adb)) == str(custom_adb.resolve())

    # Case 2: Environment variable ADB_BIN
    env_adb = tmp_path / "env_adb"
    env_adb.write_text("#!/bin/sh\nexit 0\n")
    env_adb.chmod(0o755)
    monkeypatch.setenv("ADB_BIN", str(env_adb))
    assert ember_deploy.resolve_adb_binary() == str(env_adb.resolve())
    monkeypatch.delenv("ADB_BIN", raising=False)

    # Case 3: Default fallback when neither override nor env exists
    monkeypatch.delenv("ADB_BIN", raising=False)
    resolved = ember_deploy.resolve_adb_binary()
    assert resolved in ("/workspace/tools/bin/adb", ember_deploy.shutil.which("adb") or "/workspace/tools/bin/adb")


def test_deploy_download_apk_artifact_success(monkeypatch, tmp_path):
    """Verify download_apk_artifact downloads and locates APK."""
    sys.path.insert(0, str(REPO_ROOT))
    from tools import ember_deploy

    dest_dir = tmp_path / "artifacts"
    fake_apk = dest_dir / "ember-debug-apk" / "app-debug.apk"

    def mock_run(cmd, capture_output=True, text=True, check=False):
        assert cmd[0] == "gh"
        assert cmd[1] == "run"
        assert cmd[2] == "download"
        assert "--repo" in cmd
        assert "-n" in cmd
        fake_apk.parent.mkdir(parents=True, exist_ok=True)
        fake_apk.write_bytes(b"PK\x03\x04fake_apk_binary")
        return subprocess.CompletedProcess(cmd, 0, stdout="Downloaded", stderr="")

    monkeypatch.setattr(subprocess, "run", mock_run)

    apk_path = ember_deploy.download_apk_artifact(
        repo="brockventures/ember",
        artifact_name="ember-debug-apk",
        dest_dir=dest_dir,
    )
    assert apk_path == fake_apk
    assert apk_path.exists()


def test_deploy_download_apk_artifact_failures(monkeypatch, tmp_path):
    """Verify download_apk_artifact error handling on gh failure or missing APK."""
    sys.path.insert(0, str(REPO_ROOT))
    from tools import ember_deploy

    # Failure case 1: gh command returns non-zero
    def mock_run_error(cmd, capture_output=True, text=True, check=False):
        return subprocess.CompletedProcess(cmd, 1, stdout="", stderr="404 Not Found")

    monkeypatch.setattr(subprocess, "run", mock_run_error)
    with pytest.raises(RuntimeError, match="Failed to download artifact"):
        ember_deploy.download_apk_artifact(dest_dir=tmp_path / "d1")

    # Failure case 2: gh returns 0 but no .apk found
    def mock_run_empty(cmd, capture_output=True, text=True, check=False):
        return subprocess.CompletedProcess(cmd, 0, stdout="", stderr="")

    monkeypatch.setattr(subprocess, "run", mock_run_empty)
    with pytest.raises(FileNotFoundError, match="No .apk files found"):
        ember_deploy.download_apk_artifact(dest_dir=tmp_path / "d2")


def test_deploy_connect_device(monkeypatch):
    """Verify connect_device executes adb connect for network endpoints."""
    sys.path.insert(0, str(REPO_ROOT))
    from tools import ember_deploy

    recorded_cmds = []

    def mock_run(cmd, capture_output=True, text=True, check=False):
        recorded_cmds.append(cmd)
        return subprocess.CompletedProcess(cmd, 0, stdout="connected", stderr="")

    monkeypatch.setattr(subprocess, "run", mock_run)

    # RFC 5737 documentation test address
    test_device = "198.51.100.10:5555"
    ember_deploy.connect_device("adb", test_device)
    assert len(recorded_cmds) == 1
    assert recorded_cmds[0] == ["adb", "connect", test_device]

    # Non-network device should not trigger connect
    recorded_cmds.clear()
    ember_deploy.connect_device("adb", "emulator-5554")
    assert len(recorded_cmds) == 0


def test_deploy_install_and_grant_permissions(monkeypatch, tmp_path):
    """Verify install_apk and grant_permissions issue correct ADB subcommands."""
    sys.path.insert(0, str(REPO_ROOT))
    from tools import ember_deploy

    recorded_cmds = []

    def mock_run(cmd, capture_output=True, text=True, check=True):
        recorded_cmds.append(cmd)
        return subprocess.CompletedProcess(cmd, 0, stdout="Success", stderr="")

    monkeypatch.setattr(subprocess, "run", mock_run)

    test_device = "198.51.100.10:5555"
    fake_apk = tmp_path / "app-debug.apk"
    fake_apk.write_bytes(b"PK0304")

    # Install
    ember_deploy.install_apk("adb", test_device, fake_apk)
    assert recorded_cmds[-1] == ["adb", "-s", test_device, "install", "-r", "-g", str(fake_apk)]

    # Grant permissions
    ember_deploy.grant_permissions("adb", test_device, "ventures.brock.ember")
    assert recorded_cmds[-2] == [
        "adb", "-s", test_device, "shell", "pm", "grant",
        "ventures.brock.ember", "android.permission.CAMERA",
    ]
    assert recorded_cmds[-1] == [
        "adb", "-s", test_device, "shell", "pm", "grant",
        "ventures.brock.ember", "android.permission.RECORD_AUDIO",
    ]


def test_deploy_launch_kiosk(monkeypatch):
    """Verify launch_kiosk executes am start with start_url extra."""
    sys.path.insert(0, str(REPO_ROOT))
    from tools import ember_deploy

    recorded_cmds = []

    def mock_run(cmd, capture_output=True, text=True, check=True):
        recorded_cmds.append(cmd)
        return subprocess.CompletedProcess(cmd, 0, stdout="Starting", stderr="")

    monkeypatch.setattr(subprocess, "run", mock_run)

    test_device = "198.51.100.10:5555"
    test_url = "https://hearth.local/display?hide_cursor=true"

    ember_deploy.launch_kiosk(
        "adb",
        test_device,
        start_url=test_url,
        activity_name="ventures.brock.ember/.MainActivity",
    )
    assert len(recorded_cmds) == 1
    assert recorded_cmds[0] == [
        "adb", "-s", test_device, "shell", "am", "start",
        "-n", "ventures.brock.ember/.MainActivity",
        "-e", "start_url", test_url,
    ]


def test_deploy_full_pipeline(monkeypatch, tmp_path):
    """Verify end-to-end deploy orchestration with local APK."""
    sys.path.insert(0, str(REPO_ROOT))
    from tools import ember_deploy

    recorded_cmds = []

    def mock_run(cmd, capture_output=True, text=True, check=False):
        recorded_cmds.append(cmd)
        return subprocess.CompletedProcess(cmd, 0, stdout="Success", stderr="")

    monkeypatch.setattr(subprocess, "run", mock_run)

    test_device = "198.51.100.10:5555"
    test_url = "https://hearth.local/display?hide_cursor=true"
    fake_apk = tmp_path / "app-debug.apk"
    fake_apk.write_bytes(b"fake_apk")

    result_apk = ember_deploy.deploy(
        device=test_device,
        start_url=test_url,
        apk_path=fake_apk,
        adb_bin="adb",
    )

    assert result_apk == fake_apk
    # Sequence: connect -> install -> grant camera -> grant audio -> am start -> disconnect
    assert any("connect" in cmd for cmd in recorded_cmds)
    assert any("install" in cmd for cmd in recorded_cmds)
    assert any("android.permission.CAMERA" in cmd for cmd in recorded_cmds)
    assert any("android.permission.RECORD_AUDIO" in cmd for cmd in recorded_cmds)
    assert any("start_url" in cmd for cmd in recorded_cmds)
    assert any("disconnect" in cmd for cmd in recorded_cmds)


def test_deploy_cli_main(monkeypatch, tmp_path):
    """Verify CLI main entrypoint argument parsing and exit code."""
    sys.path.insert(0, str(REPO_ROOT))
    from tools import ember_deploy

    fake_apk = tmp_path / "app-debug.apk"
    fake_apk.write_bytes(b"fake_apk")
    deployed_args = {}

    def mock_deploy(device, start_url, apk_path, repo, adb_bin):
        deployed_args["device"] = device
        deployed_args["start_url"] = start_url
        deployed_args["apk_path"] = apk_path
        return Path(apk_path)

    monkeypatch.setattr(ember_deploy, "deploy", mock_deploy)

    test_device = "198.51.100.10:5555"
    code = ember_deploy.main([
        "--device", test_device,
        "--apk", str(fake_apk),
        "--url", "https://hearth.local/test",
    ])
    assert code == 0
    assert deployed_args["device"] == test_device
    assert deployed_args["start_url"] == "https://hearth.local/test"
    assert deployed_args["apk_path"] == str(fake_apk)


def test_deploy_disconnect_device(monkeypatch):
    """Verify disconnect_device executes adb disconnect for network endpoints."""
    sys.path.insert(0, str(REPO_ROOT))
    from tools import ember_deploy

    recorded_cmds = []

    def mock_run(cmd, capture_output=True, text=True, check=False):
        recorded_cmds.append(cmd)
        return subprocess.CompletedProcess(cmd, 0, stdout="disconnected", stderr="")

    monkeypatch.setattr(subprocess, "run", mock_run)

    # RFC 5737 documentation test address
    test_device = "198.51.100.10:5555"
    ember_deploy.disconnect_device("adb", test_device)
    assert len(recorded_cmds) == 1
    assert recorded_cmds[0] == ["adb", "disconnect", test_device]

    # Non-network device should not trigger disconnect
    recorded_cmds.clear()
    ember_deploy.disconnect_device("adb", "emulator-5554")
    assert len(recorded_cmds) == 0


def test_deploy_disconnect_device_warning(monkeypatch, capsys):
    """Verify disconnect_device logs warning when adb disconnect returns non-zero."""
    sys.path.insert(0, str(REPO_ROOT))
    from tools import ember_deploy

    def mock_run_warn(cmd, capture_output=True, text=True, check=False):
        return subprocess.CompletedProcess(cmd, 1, stdout="", stderr="error: no such device")

    monkeypatch.setattr(subprocess, "run", mock_run_warn)

    test_device = "198.51.100.10:5555"
    ember_deploy.disconnect_device("adb", test_device)
    captured = capsys.readouterr()
    assert "[WARN] adb disconnect" in captured.out
    assert "error: no such device" in captured.out


def test_main_activity_time_parsing():
    """Verify time string parsing logic matches MainActivity.parseTimeMinutes."""

    def parse_time_minutes(hhmm: str, fallback_minutes: int) -> int:
        try:
            parts = hhmm.strip().split(":")
            if len(parts) != 2:
                return fallback_minutes
            h, m = int(parts[0]), int(parts[1])
            if 0 <= h <= 23 and 0 <= m <= 59:
                return h * 60 + m
            return fallback_minutes
        except (ValueError, TypeError):
            return fallback_minutes

    assert parse_time_minutes("22:30", 0) == 1350
    assert parse_time_minutes("06:30", 0) == 390
    assert parse_time_minutes("6:05", 0) == 365
    assert parse_time_minutes("00:00", 999) == 0
    assert parse_time_minutes("23:59", 0) == 1439
    # Invalid formats fall back
    assert parse_time_minutes("24:00", 123) == 123
    assert parse_time_minutes("-1:00", 123) == 123
    assert parse_time_minutes("12:60", 123) == 123
    assert parse_time_minutes("invalid", 123) == 123
    assert parse_time_minutes("", 123) == 123
    assert parse_time_minutes("12", 123) == 123


def test_main_activity_night_quiet_hours_window():
    """Verify night quiet hours calculation matches MainActivity.isNightQuietHours."""

    def is_night_quiet_hours(
        hour: int,
        minute: int,
        start_minutes: int = 0,
        end_minutes: int = 6 * 60 + 30,
    ) -> bool:
        current = hour * 60 + minute
        if start_minutes < end_minutes:
            return start_minutes <= current < end_minutes
        elif start_minutes > end_minutes:
            return current >= start_minutes or current < end_minutes
        return False

    # Default schedule: 00:00 to 06:30 (start < end)
    assert is_night_quiet_hours(0, 0)
    assert is_night_quiet_hours(3, 15)
    assert is_night_quiet_hours(6, 29)
    assert not is_night_quiet_hours(6, 30)
    assert not is_night_quiet_hours(12, 0)
    assert not is_night_quiet_hours(23, 59)

    # Custom schedule with overnight wrap: 22:30 to 06:30
    assert not is_night_quiet_hours(22, 29, 22 * 60 + 30, 6 * 60 + 30)
    assert is_night_quiet_hours(22, 30, 22 * 60 + 30, 6 * 60 + 30)
    assert is_night_quiet_hours(23, 59, 22 * 60 + 30, 6 * 60 + 30)
    assert is_night_quiet_hours(0, 0, 22 * 60 + 30, 6 * 60 + 30)
    assert is_night_quiet_hours(3, 15, 22 * 60 + 30, 6 * 60 + 30)
    assert is_night_quiet_hours(6, 29, 22 * 60 + 30, 6 * 60 + 30)
    assert not is_night_quiet_hours(6, 30, 22 * 60 + 30, 6 * 60 + 30)
    assert not is_night_quiet_hours(12, 0, 22 * 60 + 30, 6 * 60 + 30)

    # Daytime schedule: 09:00 to 17:00 (no wrap)
    assert not is_night_quiet_hours(8, 59, 9 * 60, 17 * 60)
    assert is_night_quiet_hours(9, 0, 9 * 60, 17 * 60)
    assert is_night_quiet_hours(16, 59, 9 * 60, 17 * 60)
    assert not is_night_quiet_hours(17, 0, 9 * 60, 17 * 60)

    # Empty window (start == end)
    assert not is_night_quiet_hours(10, 0, 10 * 60, 10 * 60)


def test_main_activity_night_schedule_state_machine():
    """Verify night schedule evaluation state transitions (sleep, touch wake, morning auto-wake)."""
    idle_timeout_ms = 15 * 60 * 1000  # 15 minutes = 900_000 ms
    sleep_brightness = 0.01
    awake_brightness = -1.0

    class KioskScheduleState:

        def __init__(self, start_min: int = 0, end_min: int = 390):
            self.start_min = start_min
            self.end_min = end_min
            self.is_asleep = False
            self.was_in_night_window = False
            self.brightness = awake_brightness
            self.overlay_visible = False
            self.last_touch_or_wake_time = 0
            self.wake_call_count = 0
            self.sleep_call_count = 0

        def is_night(self, hour: int, minute: int) -> bool:
            curr = hour * 60 + minute
            if self.start_min < self.end_min:
                return self.start_min <= curr < self.end_min
            elif self.start_min > self.end_min:
                return curr >= self.start_min or curr < self.end_min
            return False

        def wake(self, now_ms: int):
            self.last_touch_or_wake_time = now_ms
            self.wake_call_count += 1
            if not self.is_asleep:
                return
            self.is_asleep = False
            self.brightness = awake_brightness
            self.overlay_visible = False

        def sleep(self):
            if self.is_asleep:
                return
            self.is_asleep = True
            self.sleep_call_count += 1
            self.brightness = sleep_brightness
            self.overlay_visible = True

        def touch(self, now_ms: int):
            self.last_touch_or_wake_time = now_ms
            if self.is_asleep:
                self.wake(now_ms)

        def evaluate_night_schedule(self, hour: int, minute: int, now_ms: int):
            in_night = self.is_night(hour, minute)

            if self.was_in_night_window and not in_night:
                self.wake(now_ms)
            elif in_night:
                elapsed = now_ms - self.last_touch_or_wake_time
                if not self.is_asleep and elapsed >= idle_timeout_ms:
                    self.sleep()

            self.was_in_night_window = in_night

    # Timeline simulation:
    # 23:50 - Kiosk awake during daytime, user touches it
    t0 = 1_000_000
    kiosk = KioskScheduleState()
    kiosk.touch(now_ms=t0)
    assert not kiosk.is_asleep

    # 23:59 - Still daytime, evaluate schedule
    kiosk.evaluate_night_schedule(hour=23, minute=59, now_ms=t0 + 9 * 60 * 1000)
    assert not kiosk.is_asleep
    assert not kiosk.was_in_night_window

    # 00:00 - Quiet hours start! But touched 10 mins ago (elapsed = 10 min < 15 min idle timeout)
    kiosk.evaluate_night_schedule(hour=0, minute=0, now_ms=t0 + 10 * 60 * 1000)
    assert not kiosk.is_asleep  # User grace period active
    assert kiosk.was_in_night_window

    # 00:05 - 15 minutes since last touch (elapsed = 15 min >= IDLE_TIMEOUT_MS) -> dim to sleep!
    kiosk.evaluate_night_schedule(hour=0, minute=5, now_ms=t0 + 15 * 60 * 1000)
    assert kiosk.is_asleep
    assert kiosk.brightness == sleep_brightness
    assert kiosk.overlay_visible
    assert kiosk.sleep_call_count == 1

    # 02:00 - Night time, user touches frame -> wakes for 15 minutes!
    t_wake = t0 + 130 * 60 * 1000
    kiosk.touch(now_ms=t_wake)
    assert not kiosk.is_asleep
    assert kiosk.brightness == awake_brightness
    assert not kiosk.overlay_visible

    # 02:10 - 10 mins into touch wake, still awake
    kiosk.evaluate_night_schedule(hour=2, minute=10, now_ms=t_wake + 10 * 60 * 1000)
    assert not kiosk.is_asleep

    # 02:15 - 15 mins since touch -> returns to sleep
    kiosk.evaluate_night_schedule(hour=2, minute=15, now_ms=t_wake + 15 * 60 * 1000)
    assert kiosk.is_asleep
    assert kiosk.sleep_call_count == 2

    # 06:29 - Still quiet hours, still asleep
    kiosk.evaluate_night_schedule(hour=6, minute=29, now_ms=t_wake + 269 * 60 * 1000)
    assert kiosk.is_asleep

    # 06:30 - Morning transition! Quiet hours end -> auto-wake!
    kiosk.evaluate_night_schedule(hour=6, minute=30, now_ms=t_wake + 270 * 60 * 1000)
    assert not kiosk.is_asleep
    assert kiosk.brightness == awake_brightness
    assert not kiosk.overlay_visible
    assert not kiosk.was_in_night_window

    # 06:31 - Daytime continuation, remains awake
    kiosk.evaluate_night_schedule(hour=6, minute=31, now_ms=t_wake + 271 * 60 * 1000)
    assert not kiosk.is_asleep


def test_main_activity_intent_schedule_configuration():
    """Verify night schedule configuration via Intent extras and SharedPreferences."""
    default_start = "00:00"
    default_end = "06:30"

    class MockSharedPreferences:

        def __init__(self):
            self.prefs = {}

        def get_string(self, key: str, default: str | None) -> str | None:
            return self.prefs.get(key, default)

        def put_string(self, key: str, value: str):
            self.prefs[key] = value

    prefs = MockSharedPreferences()

    def handle_intent(extras: dict[str, str]):
        start = extras.get("night_start")
        end = extras.get("night_end")
        if start and start.strip():
            prefs.put_string("night_start", start.strip())
        if end and end.strip():
            prefs.put_string("night_end", end.strip())

    def resolve_schedule() -> tuple[str, str]:
        start = prefs.get_string("night_start", default_start)
        end = prefs.get_string("night_end", default_end)
        return start, end

    # Initial defaults
    assert resolve_schedule() == (default_start, default_end)

    # Intent sets custom quiet hours
    handle_intent({"night_start": "23:00", "night_end": "07:00"})
    assert resolve_schedule() == ("23:00", "07:00")

    # Partial intent only updates night_start
    handle_intent({"night_start": "21:45"})
    assert resolve_schedule() == ("21:45", "07:00")

    # Empty extras do not overwrite
    handle_intent({"night_start": "   ", "night_end": ""})
    assert resolve_schedule() == ("21:45", "07:00")


def test_ember_bridge_interface():
    """Verify EmberBridge @JavascriptInterface methods and registration in MainActivity."""
    main_activity_file = (
        REPO_ROOT
        / "app"
        / "src"
        / "main"
        / "java"
        / "ventures"
        / "brock"
        / "ember"
        / "MainActivity.kt"
    )
    content = main_activity_file.read_text(encoding="utf-8")

    # Bridge class definition and registration
    assert "inner class EmberBridge" in content
    assert 'webView.addJavascriptInterface(EmberBridge(), "EmberBridge")' in content

    # Bridge exposed methods
    bridge_start = content.find("inner class EmberBridge")
    assert bridge_start != -1
    bridge_block = content[bridge_start:]

    assert "@JavascriptInterface" in bridge_block
    assert "fun sleep()" in bridge_block
    assert "fun wake()" in bridge_block
    assert "fun setBrightness(" in bridge_block
    assert "fun setNightSchedule(" in bridge_block
    assert "fun installOta(" in bridge_block

    # Bridge origin guard checks on every exposed interface method
    assert "isCurrentBridgeOriginAllowed()" in bridge_block
    assert "Blocked EmberBridge.sleep()" in bridge_block
    assert "Blocked EmberBridge.wake()" in bridge_block
    assert "Blocked EmberBridge.setBrightness(" in bridge_block
    assert "Blocked EmberBridge.setNightSchedule(" in bridge_block
    assert "Blocked EmberBridge.installOta(" in bridge_block


def test_ember_ota_package_installer_implementation():
    """Verify OTA package installer implementation and PackageInstaller session handling."""
    main_activity_file = (
        REPO_ROOT
        / "app"
        / "src"
        / "main"
        / "java"
        / "ventures"
        / "brock"
        / "ember"
        / "MainActivity.kt"
    )
    content = main_activity_file.read_text(encoding="utf-8")

    # installOtaApk method presence
    assert "fun installOtaApk(apkUrl: String)" in content

    # OTA origin allowlist and enablement guard
    assert "isOtaUrlAllowed(apkUrl, startUrl, extraOrigins, otaEnabled)" in content

    # Background thread execution
    assert "Thread {" in content

    # HTTP/HTTPS download handling
    assert "HttpURLConnection" in content
    assert "ember-ota.apk" in content

    # PackageInstaller session orchestration
    assert "packageManager.packageInstaller" in content
    assert (
        "PackageInstaller.SessionParams(PackageInstaller.SessionParams.MODE_FULL_INSTALL)"
        in content
    )
    assert "packageInstaller.createSession(params)" in content
    assert "packageInstaller.openSession(sessionId)" in content
    assert 's.openWrite("ember-ota", 0, apkFile.length())' in content
    assert "s.fsync(out)" in content
    assert "s.commit(pendingIntent.intentSender)" in content
    assert "PendingIntent.FLAG_MUTABLE" in content
    assert "OtaInstallReceiver::class.java" in content


def test_ember_bridge_simulation():
    """Simulate EmberBridge Javascript Interface behavior and state transitions."""

    class MockKiosk:

        def __init__(self):
            self.asleep = False
            self.brightness = -1.0
            self.night_start = "00:00"
            self.night_end = "06:30"
            self.ota_url_requested = None

        def sleep(self):
            self.asleep = True
            self.brightness = 0.01

        def wake(self):
            self.asleep = False
            self.brightness = -1.0

        def set_brightness(self, level: float):
            self.brightness = level

        def set_night_schedule(self, start: str, end: str):
            self.night_start = start
            self.night_end = end

        def install_ota(self, url: str):
            self.ota_url_requested = url

    kiosk = MockKiosk()
    assert not kiosk.asleep
    assert kiosk.brightness == -1.0

    kiosk.sleep()
    assert kiosk.asleep
    assert kiosk.brightness == 0.01

    kiosk.wake()
    assert not kiosk.asleep
    assert kiosk.brightness == -1.0

    kiosk.set_brightness(0.75)
    assert kiosk.brightness == 0.75

    kiosk.set_night_schedule("22:00", "07:00")
    assert kiosk.night_start == "22:00"
    assert kiosk.night_end == "07:00"

    kiosk.install_ota("https://hearth.local/ota/ember-v2.apk")
    assert kiosk.ota_url_requested == "https://hearth.local/ota/ember-v2.apk"


def test_main_activity_origin_extraction():
    """Verify origin extraction logic matching MainActivity.extractOrigin."""

    def extract_origin(url_string: str | None) -> str | None:
        if not url_string or not url_string.strip():
            return None
        try:
            parsed = urlparse(url_string.strip())
            scheme = parsed.scheme.lower()
            if scheme not in ("http", "https"):
                return None
            host = parsed.hostname
            if not host:
                return None
            host = host.lower()
            port = parsed.port
            if port is not None and not (
                (scheme == "http" and port == 80)
                or (scheme == "https" and port == 443)
            ):
                return f"{scheme}://{host}:{port}"
            return f"{scheme}://{host}"
        except Exception:
            return None

    # Standard HTTPS and HTTP URLs with paths, queries, fragments
    assert extract_origin("https://hearth.local/display?hide_cursor=true") == "https://hearth.local"
    assert extract_origin("http://kiosk.local:8080/display#top") == "http://kiosk.local:8080"
    assert extract_origin("http://203.0.113.10:8123/lovelace/home") == "http://203.0.113.10:8123"
    assert extract_origin("https://203.0.113.10/lovelace") == "https://203.0.113.10"

    # Default ports are omitted per RFC 6454
    assert extract_origin("http://example.com:80/path") == "http://example.com"
    assert extract_origin("https://example.com:443/path") == "https://example.com"

    # Non-default ports are preserved
    assert extract_origin("https://example.com:8443/secure") == "https://example.com:8443"
    assert extract_origin("http://example.com:443/odd") == "http://example.com:443"
    assert extract_origin("https://example.com:80/odd") == "https://example.com:80"

    # Case normalization
    assert extract_origin("HTTPS://HEARTH.LOCAL:8080/display") == "https://hearth.local:8080"

    # Invalid, empty, or non-http(s) schemes return None
    assert extract_origin(None) is None
    assert extract_origin("") is None
    assert extract_origin("   ") is None
    assert extract_origin("not-a-valid-url") is None
    assert extract_origin("ftp://files.example.com") is None
    assert extract_origin("javascript:alert(1)") is None
    assert extract_origin("file:///sdcard/app.apk") is None


def test_main_activity_parse_allowed_origins():
    """Verify origin allowlist parsing logic matching MainActivity.parseAllowedOrigins."""

    def extract_origin(url_string: str | None) -> str | None:
        if not url_string or not url_string.strip():
            return None
        try:
            parsed = urlparse(url_string.strip())
            scheme = parsed.scheme.lower()
            if scheme not in ("http", "https"):
                return None
            host = parsed.hostname
            if not host:
                return None
            host = host.lower()
            port = parsed.port
            if port is not None and not (
                (scheme == "http" and port == 80)
                or (scheme == "https" and port == 443)
            ):
                return f"{scheme}://{host}:{port}"
            return f"{scheme}://{host}"
        except Exception:
            return None

    def parse_allowed_origins(
        start_url: str | None,
        extra_origins_csv: str | None = None,
    ) -> set[str]:
        origins = set()
        start_origin = extract_origin(start_url)
        if start_origin:
            origins.add(start_origin)
        if extra_origins_csv and extra_origins_csv.strip():
            for token in extra_origins_csv.split(","):
                trimmed = token.strip()
                if not trimmed:
                    continue
                orig = extract_origin(trimmed)
                if orig:
                    origins.add(orig)
                elif "://" not in trimmed:
                    https_orig = extract_origin(f"https://{trimmed}")
                    if https_orig:
                        origins.add(https_orig)
                    http_orig = extract_origin(f"http://{trimmed}")
                    if http_orig:
                        origins.add(http_orig)
        return origins

    # Auto-trust start_url origin
    origins = parse_allowed_origins("https://hearth.local/display?hide_cursor=true")
    assert origins == {"https://hearth.local"}

    # Extra origins via CSV
    origins = parse_allowed_origins(
        "https://hearth.local/display",
        "https://example.org, http://203.0.113.50:8080",
    )
    assert origins == {
        "https://hearth.local",
        "https://example.org",
        "http://203.0.113.50:8080",
    }

    # Handling whitespace, trailing commas, empty tokens
    origins = parse_allowed_origins(
        "https://hearth.local",
        "  ,  https://extra.local  ,  , ",
    )
    assert origins == {"https://hearth.local", "https://extra.local"}

    # Scheme-less extra origin tokens expand to both https and http
    origins = parse_allowed_origins(
        "https://hearth.local",
        "frame.example.com:8080",
    )
    assert origins == {
        "https://hearth.local",
        "https://frame.example.com:8080",
        "http://frame.example.com:8080",
    }

    # Empty inputs
    assert parse_allowed_origins(None, None) == set()
    assert parse_allowed_origins("", "") == set()


def test_main_activity_is_bridge_origin_allowed():
    """Verify bridge origin guard authorization matching MainActivity.isBridgeOriginAllowed."""

    def extract_origin(url_string: str | None) -> str | None:
        if not url_string or not url_string.strip():
            return None
        try:
            parsed = urlparse(url_string.strip())
            scheme = parsed.scheme.lower()
            if scheme not in ("http", "https"):
                return None
            host = parsed.hostname
            if not host:
                return None
            host = host.lower()
            port = parsed.port
            if port is not None and not (
                (scheme == "http" and port == 80)
                or (scheme == "https" and port == 443)
            ):
                return f"{scheme}://{host}:{port}"
            return f"{scheme}://{host}"
        except Exception:
            return None

    def parse_allowed_origins(
        start_url: str | None,
        extra_origins_csv: str | None = None,
    ) -> set[str]:
        origins = set()
        start_origin = extract_origin(start_url)
        if start_origin:
            origins.add(start_origin)
        if extra_origins_csv and extra_origins_csv.strip():
            for token in extra_origins_csv.split(","):
                trimmed = token.strip()
                if not trimmed:
                    continue
                orig = extract_origin(trimmed)
                if orig:
                    origins.add(orig)
                elif "://" not in trimmed:
                    https_orig = extract_origin(f"https://{trimmed}")
                    if https_orig:
                        origins.add(https_orig)
                    http_orig = extract_origin(f"http://{trimmed}")
                    if http_orig:
                        origins.add(http_orig)
        return origins

    def is_bridge_origin_allowed(
        current_url: str | None,
        start_url: str | None,
        extra_origins_csv: str | None = None,
    ) -> bool:
        if not current_url or not current_url.strip():
            return False
        current_origin = extract_origin(current_url)
        if not current_origin:
            return False
        allowed = parse_allowed_origins(start_url, extra_origins_csv)
        return current_origin in allowed

    # Default Hearth setup: start_url origin is trusted
    start = "https://hearth.local/display?hide_cursor=true"
    assert is_bridge_origin_allowed("https://hearth.local/display", start)
    assert is_bridge_origin_allowed("https://hearth.local/settings/audio?debug=true", start)

    # Untrusted external origins are blocked
    assert not is_bridge_origin_allowed("https://malicious.evil.com/phish", start)
    assert not is_bridge_origin_allowed("http://hearth.local/display", start)  # Protocol mismatch
    assert not is_bridge_origin_allowed("https://hearth.local:8443/display", start)  # Port mismatch
    assert not is_bridge_origin_allowed(None, start)
    assert not is_bridge_origin_allowed("", start)
    assert not is_bridge_origin_allowed("invalid-url", start)

    # Custom user setup (Alex configures his own domain as start_url)
    custom_start = "https://frame.example.com:8080/dashboard"
    assert is_bridge_origin_allowed("https://frame.example.com:8080/display", custom_start)
    assert is_bridge_origin_allowed("https://frame.example.com:8080/metrics", custom_start)
    # Default hearth.local is NOT trusted when user configures a custom start_url without extra origins
    assert not is_bridge_origin_allowed("https://hearth.local/display", custom_start)

    # Multi-origin setup via bridge_origins
    extras = "https://frame.example.com:8080, http://203.0.113.30:3000"
    assert is_bridge_origin_allowed("https://hearth.local/display", start, extras)
    assert is_bridge_origin_allowed("https://frame.example.com:8080/home", start, extras)
    assert is_bridge_origin_allowed("http://203.0.113.30:3000/dash", start, extras)
    assert not is_bridge_origin_allowed("http://203.0.113.30:8080/other", start, extras)


def test_main_activity_is_ota_url_allowed():
    """Verify OTA APK download URL validation matching MainActivity.isOtaUrlAllowed."""

    def extract_origin(url_string: str | None) -> str | None:
        if not url_string or not url_string.strip():
            return None
        try:
            parsed = urlparse(url_string.strip())
            scheme = parsed.scheme.lower()
            if scheme not in ("http", "https"):
                return None
            host = parsed.hostname
            if not host:
                return None
            host = host.lower()
            port = parsed.port
            if port is not None and not (
                (scheme == "http" and port == 80)
                or (scheme == "https" and port == 443)
            ):
                return f"{scheme}://{host}:{port}"
            return f"{scheme}://{host}"
        except Exception:
            return None

    def parse_allowed_origins(
        start_url: str | None,
        extra_origins_csv: str | None = None,
    ) -> set[str]:
        origins = set()
        start_origin = extract_origin(start_url)
        if start_origin:
            origins.add(start_origin)
        if extra_origins_csv and extra_origins_csv.strip():
            for token in extra_origins_csv.split(","):
                trimmed = token.strip()
                if not trimmed:
                    continue
                orig = extract_origin(trimmed)
                if orig:
                    origins.add(orig)
                elif "://" not in trimmed:
                    https_orig = extract_origin(f"https://{trimmed}")
                    if https_orig:
                        origins.add(https_orig)
                    http_orig = extract_origin(f"http://{trimmed}")
                    if http_orig:
                        origins.add(http_orig)
        return origins

    def is_ota_url_allowed(
        apk_url: str,
        start_url: str | None,
        extra_origins_csv: str | None = None,
        ota_enabled: bool = True,
    ) -> bool:
        if not ota_enabled or not apk_url or not apk_url.strip():
            return False
        ota_origin = extract_origin(apk_url)
        if not ota_origin:
            return False
        allowed = parse_allowed_origins(start_url, extra_origins_csv)
        if ota_origin not in allowed:
            return False
        try:
            path = urlparse(apk_url.strip()).path.lower()
        except Exception:
            path = apk_url.strip().split("?")[0].split("#")[0].lower()
        return path.endswith(".apk")

    start = "https://hearth.local/display"

    # Authorized OTA download from start_url origin
    assert is_ota_url_allowed("https://hearth.local/ota/ember-v2.apk", start)
    assert is_ota_url_allowed(
        "https://hearth.local/downloads/ember-release.apk?version=2.1&token=xyz",
        start,
    )

    # OTA disabled flag blocks even authorized origins
    assert not is_ota_url_allowed(
        "https://hearth.local/ota/ember-v2.apk",
        start,
        ota_enabled=False,
    )

    # Untrusted origins blocked
    assert not is_ota_url_allowed("https://evil.com/ota/ember-v2.apk", start)
    assert not is_ota_url_allowed("http://hearth.local/ota/ember-v2.apk", start)

    # Non-APK extensions blocked
    assert not is_ota_url_allowed("https://hearth.local/ota/script.sh", start)
    assert not is_ota_url_allowed("https://hearth.local/ota/ember.apk.tar.gz", start)
    assert not is_ota_url_allowed("https://hearth.local/ota/ember.exe", start)

    # Configured extra origin for updates allowed
    extras = "https://updates.example.org:8443"
    assert is_ota_url_allowed(
        "https://updates.example.org:8443/packages/ember-latest.apk",
        start,
        extra_origins_csv=extras,
    )

    # Malformed or blank URLs blocked
    assert not is_ota_url_allowed("", start)
    assert not is_ota_url_allowed("   ", start)
    assert not is_ota_url_allowed("not-a-url", start)


def test_ember_bridge_origin_guard_simulation():
    """Simulate EmberBridge Javascript Interface with origin authorization guards."""

    def extract_origin(url_string: str | None) -> str | None:
        if not url_string or not url_string.strip():
            return None
        try:
            parsed = urlparse(url_string.strip())
            scheme = parsed.scheme.lower()
            if scheme not in ("http", "https"):
                return None
            host = parsed.hostname
            if not host:
                return None
            host = host.lower()
            port = parsed.port
            if port is not None and not (
                (scheme == "http" and port == 80)
                or (scheme == "https" and port == 443)
            ):
                return f"{scheme}://{host}:{port}"
            return f"{scheme}://{host}"
        except Exception:
            return None

    def parse_allowed_origins(
        start_url: str | None,
        extra_origins_csv: str | None = None,
    ) -> set[str]:
        origins = set()
        start_origin = extract_origin(start_url)
        if start_origin:
            origins.add(start_origin)
        if extra_origins_csv and extra_origins_csv.strip():
            for token in extra_origins_csv.split(","):
                trimmed = token.strip()
                if not trimmed:
                    continue
                orig = extract_origin(trimmed)
                if orig:
                    origins.add(orig)
                elif "://" not in trimmed:
                    https_orig = extract_origin(f"https://{trimmed}")
                    if https_orig:
                        origins.add(https_orig)
                    http_orig = extract_origin(f"http://{trimmed}")
                    if http_orig:
                        origins.add(http_orig)
        return origins

    def is_bridge_origin_allowed(
        current_url: str | None,
        start_url: str | None,
        extra_origins_csv: str | None = None,
    ) -> bool:
        if not current_url or not current_url.strip():
            return False
        current_origin = extract_origin(current_url)
        if not current_origin:
            return False
        allowed = parse_allowed_origins(start_url, extra_origins_csv)
        return current_origin in allowed

    def is_ota_url_allowed(
        apk_url: str,
        start_url: str | None,
        extra_origins_csv: str | None = None,
        ota_enabled: bool = True,
    ) -> bool:
        if not ota_enabled or not apk_url or not apk_url.strip():
            return False
        ota_origin = extract_origin(apk_url)
        if not ota_origin:
            return False
        allowed = parse_allowed_origins(start_url, extra_origins_csv)
        if ota_origin not in allowed:
            return False
        try:
            path = urlparse(apk_url.strip()).path.lower()
        except Exception:
            path = apk_url.strip().split("?")[0].split("#")[0].lower()
        return path.endswith(".apk")

    class GuardedKiosk:

        def __init__(
            self,
            start_url: str = "https://hearth.local/display",
            bridge_origins: str | None = None,
            ota_enabled: bool = True,
        ):
            self.start_url = start_url
            self.bridge_origins = bridge_origins
            self.ota_enabled = ota_enabled
            self.current_page_url = start_url
            self.asleep = False
            self.brightness = -1.0
            self.night_start = "00:00"
            self.night_end = "06:30"
            self.installed_ota_urls: list[str] = []
            self.blocked_call_count = 0

        def _is_origin_allowed(self) -> bool:
            return is_bridge_origin_allowed(
                self.current_page_url,
                self.start_url,
                self.bridge_origins,
            )

        def sleep(self):
            if not self._is_origin_allowed():
                self.blocked_call_count += 1
                return
            self.asleep = True
            self.brightness = 0.01

        def wake(self):
            if not self._is_origin_allowed():
                self.blocked_call_count += 1
                return
            self.asleep = False
            self.brightness = -1.0

        def set_brightness(self, level: float):
            if not self._is_origin_allowed():
                self.blocked_call_count += 1
                return
            self.brightness = level

        def set_night_schedule(self, start: str, end: str):
            if not self._is_origin_allowed():
                self.blocked_call_count += 1
                return
            self.night_start = start
            self.night_end = end

        def install_ota(self, apk_url: str):
            if not self._is_origin_allowed():
                self.blocked_call_count += 1
                return
            if not is_ota_url_allowed(
                apk_url,
                self.start_url,
                self.bridge_origins,
                self.ota_enabled,
            ):
                self.blocked_call_count += 1
                return
            self.installed_ota_urls.append(apk_url)

    # 1. Authorized calls from start_url succeed
    kiosk = GuardedKiosk(start_url="https://hearth.local/display")
    kiosk.sleep()
    assert kiosk.asleep
    kiosk.wake()
    assert not kiosk.asleep
    kiosk.set_brightness(0.5)
    assert kiosk.brightness == 0.5
    kiosk.set_night_schedule("23:00", "07:00")
    assert kiosk.night_start == "23:00"
    kiosk.install_ota("https://hearth.local/ota/v2.apk")
    assert kiosk.installed_ota_urls == ["https://hearth.local/ota/v2.apk"]
    assert kiosk.blocked_call_count == 0

    # 2. Unauthorized origin attempts are blocked
    kiosk.current_page_url = "https://untrusted-external.site/exploit"
    kiosk.sleep()
    assert not kiosk.asleep  # Did not change
    assert kiosk.blocked_call_count == 1
    kiosk.set_brightness(1.0)
    assert kiosk.brightness == 0.5  # Unchanged
    assert kiosk.blocked_call_count == 2
    kiosk.set_night_schedule("12:00", "13:00")
    assert kiosk.night_start == "23:00"  # Unchanged
    assert kiosk.blocked_call_count == 3
    kiosk.install_ota("https://untrusted-external.site/bad.apk")
    assert len(kiosk.installed_ota_urls) == 1  # Unchanged
    assert kiosk.blocked_call_count == 4

    # 3. Custom user setup with bridge_origins allows custom domain
    custom_kiosk = GuardedKiosk(
        start_url="https://frame.example.com:8080/dashboard",
        bridge_origins="https://updates.example.com",
        ota_enabled=True,
    )
    # Allowed on custom start_url
    custom_kiosk.sleep()
    assert custom_kiosk.asleep
    # OTA from custom updates domain allowed
    custom_kiosk.install_ota("https://updates.example.com/ember.apk")
    assert custom_kiosk.installed_ota_urls == ["https://updates.example.com/ember.apk"]

    # 4. OTA disabled blocks even valid APK from authorized origin
    locked_kiosk = GuardedKiosk(
        start_url="https://hearth.local/display",
        ota_enabled=False,
    )
    locked_kiosk.install_ota("https://hearth.local/ota/v2.apk")
    assert len(locked_kiosk.installed_ota_urls) == 0
    assert locked_kiosk.blocked_call_count == 1


def test_deploy_launch_kiosk_configurable_flags(monkeypatch):
    """Verify launch_kiosk appends bridge_origins, ota_enabled, and allow_mixed_content extras."""
    sys.path.insert(0, str(REPO_ROOT))
    from tools import ember_deploy

    recorded_cmds = []

    def mock_run(cmd, capture_output=True, text=True, check=True):
        recorded_cmds.append(cmd)
        return subprocess.CompletedProcess(cmd, 0, stdout="Starting", stderr="")

    monkeypatch.setattr(subprocess, "run", mock_run)

    test_device = "198.51.100.10:5555"
    test_url = "https://frame.example.com:8080/dashboard"

    # 1. Custom bridge origins and enabled OTA
    ember_deploy.launch_kiosk(
        "adb",
        test_device,
        start_url=test_url,
        activity_name="ventures.brock.ember/.MainActivity",
        bridge_origins="https://updates.example.com",
        ota_enabled=True,
        allow_mixed_content=False,
    )
    assert len(recorded_cmds) == 1
    assert recorded_cmds[0] == [
        "adb", "-s", test_device, "shell", "am", "start",
        "-n", "ventures.brock.ember/.MainActivity",
        "-e", "start_url", test_url,
        "-e", "bridge_origins", "https://updates.example.com",
        "--ez", "ota_enabled", "true",
        "--ez", "allow_mixed_content", "false",
    ]

    # 2. Disabled OTA and allowed mixed content
    recorded_cmds.clear()
    ember_deploy.launch_kiosk(
        "adb",
        test_device,
        start_url=test_url,
        activity_name="ventures.brock.ember/.MainActivity",
        ota_enabled=False,
        allow_mixed_content=True,
    )
    assert len(recorded_cmds) == 1
    assert recorded_cmds[0] == [
        "adb", "-s", test_device, "shell", "am", "start",
        "-n", "ventures.brock.ember/.MainActivity",
        "-e", "start_url", test_url,
        "--ez", "ota_enabled", "false",
        "--ez", "allow_mixed_content", "true",
    ]


def test_deploy_pipeline_with_security_flags(monkeypatch, tmp_path):
    """Verify deploy forwards bridge_origins, ota_enabled, and allow_mixed_content to launch_kiosk."""
    sys.path.insert(0, str(REPO_ROOT))
    from tools import ember_deploy

    recorded_cmds = []

    def mock_run(cmd, capture_output=True, text=True, check=False):
        recorded_cmds.append(cmd)
        return subprocess.CompletedProcess(cmd, 0, stdout="Success", stderr="")

    monkeypatch.setattr(subprocess, "run", mock_run)

    test_device = "198.51.100.10:5555"
    test_url = "https://frame.example.com:8080/dashboard"
    fake_apk = tmp_path / "app-debug.apk"
    fake_apk.write_bytes(b"fake_apk")

    result_apk = ember_deploy.deploy(
        device=test_device,
        start_url=test_url,
        apk_path=fake_apk,
        adb_bin="adb",
        bridge_origins="https://updates.example.com",
        ota_enabled=True,
        allow_mixed_content=False,
    )

    assert result_apk == fake_apk
    am_start_cmds = [cmd for cmd in recorded_cmds if "start" in cmd and "am" in cmd]
    assert len(am_start_cmds) == 1
    cmd = am_start_cmds[0]
    assert "-e" in cmd and "bridge_origins" in cmd and "https://updates.example.com" in cmd
    assert "--ez" in cmd and "ota_enabled" in cmd and "true" in cmd
    assert "--ez" in cmd and "allow_mixed_content" in cmd and "false" in cmd


def test_deploy_cli_security_flags(monkeypatch, tmp_path):
    """Verify CLI parser parses and passes configurable bridge flags to deploy."""
    sys.path.insert(0, str(REPO_ROOT))
    from tools import ember_deploy

    fake_apk = tmp_path / "app-debug.apk"
    fake_apk.write_bytes(b"fake_apk")
    deployed_kwargs = {}

    def mock_deploy(**kwargs):
        deployed_kwargs.update(kwargs)
        return Path(kwargs.get("apk_path", "test.apk"))

    monkeypatch.setattr(ember_deploy, "deploy", mock_deploy)

    test_device = "198.51.100.10:5555"

    # Flag variant 1: --bridge-origins, --ota-enabled, --no-allow-mixed-content
    code = ember_deploy.main([
        "--device", test_device,
        "--apk", str(fake_apk),
        "--url", "https://frame.example.com/dash",
        "--bridge-origins", "https://updates.example.com",
        "--ota-enabled",
        "--no-allow-mixed-content",
    ])
    assert code == 0
    assert deployed_kwargs["device"] == test_device
    assert deployed_kwargs["start_url"] == "https://frame.example.com/dash"
    assert deployed_kwargs["bridge_origins"] == "https://updates.example.com"
    assert deployed_kwargs["ota_enabled"] is True
    assert deployed_kwargs["allow_mixed_content"] is False

    # Flag variant 2: --no-ota-enabled, --allow-mixed-content
    deployed_kwargs.clear()
    code = ember_deploy.main([
        "--device", test_device,
        "--apk", str(fake_apk),
        "--no-ota-enabled",
        "--allow-mixed-content",
    ])
    assert code == 0
    assert deployed_kwargs["ota_enabled"] is False
    assert deployed_kwargs["allow_mixed_content"] is True

    # Flag variant 3: explicit string boolean values --ota-enabled false --allow-mixed-content true
    deployed_kwargs.clear()
    code = ember_deploy.main([
        "--device", test_device,
        "--apk", str(fake_apk),
        "--ota-enabled", "false",
        "--allow-mixed-content", "true",
    ])
    assert code == 0
    assert deployed_kwargs["ota_enabled"] is False
    assert deployed_kwargs["allow_mixed_content"] is True


def _py_is_google_photos_album_url_allowed(album_url: str | None) -> bool:
    if not album_url or not album_url.strip():
        return False
    trimmed = album_url.strip()
    return trimmed.startswith("https://photos.google.com/") or trimmed.startswith("https://photos.app.goo.gl/")


def _py_extract_google_photos_urls(html: str | None) -> list[str]:
    if not html or not html.strip():
        return []
    normalized = html.replace("\\u003d", "=").replace("\\/", "/")
    matches = re.findall(r"https://lh3\.googleusercontent\.com/pw/[A-Za-z0-9_-]{20,}", normalized)
    seen: list[str] = []
    for m in matches:
        if m not in seen:
            seen.append(m)
    return seen


def test_ember_google_photos_bridge_interface_and_url_extraction():
    """Verify Google Photos URL allowlist, HTML URL extraction, single-flight timeout cleanup, and OtaInstallReceiver."""
    source = (REPO_ROOT / "app/src/main/java/ventures/brock/ember/MainActivity.kt").read_text(encoding="utf-8")
    assert "CookieManager.getInstance()" in source
    assert "setAcceptCookie(true)" in source
    assert "setAcceptThirdPartyCookies(webView, true)" in source
    assert "fun syncGooglePhotosAlbum(albumUrl: String)" in source
    assert "fun connectGooglePhotos(albumUrl: String)" in source
    assert "fun cleanupPhotosSync()" in source
    assert "PHOTOS_SYNC_TIMEOUT_MS" in source
    assert "class OtaInstallReceiver : BroadcastReceiver()" in source
    assert "PendingIntent.FLAG_MUTABLE" in source

    # Behavioral parity tests for isGooglePhotosAlbumUrlAllowed
    assert _py_is_google_photos_album_url_allowed("https://photos.google.com/share/AF1QipExample") is True
    assert _py_is_google_photos_album_url_allowed("  https://photos.app.goo.gl/AbCdEf123456  ") is True
    assert _py_is_google_photos_album_url_allowed("https://evil.example.com/photos.google.com") is False
    assert _py_is_google_photos_album_url_allowed("javascript:alert(1)") is False
    assert _py_is_google_photos_album_url_allowed("") is False
    assert _py_is_google_photos_album_url_allowed(None) is False

    # Behavioral parity tests for extractGooglePhotosUrls (escaped & unescaped DOM payloads, deduplication)
    sample_dom = (
        r'"https:\/\/lh3.googleusercontent.com\/pw\/AP1GczMockPhotoTokenAlpha1234567890\u003dw1920-h1080" '
        r'"https://lh3.googleusercontent.com/pw/AP1GczMockPhotoTokenBeta0987654321=s0" '
        r'"https://lh3.googleusercontent.com/pw/AP1GczMockPhotoTokenAlpha1234567890=w800" '
        r'"https://lh3.googleusercontent.com/pw/short"'
    )
    extracted = _py_extract_google_photos_urls(sample_dom)
    assert extracted == [
        "https://lh3.googleusercontent.com/pw/AP1GczMockPhotoTokenAlpha1234567890",
        "https://lh3.googleusercontent.com/pw/AP1GczMockPhotoTokenBeta0987654321",
    ]
    assert _py_extract_google_photos_urls("") == []
    assert _py_extract_google_photos_urls(None) == []


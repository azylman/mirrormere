import os
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
    assert "unitTests.isReturnDefaultValues = true" in build_content
    assert 'testImplementation("junit:junit:4.13.2")' in build_content

    gradlew_file = REPO_ROOT / "gradlew"
    assert gradlew_file.exists(), "gradlew does not exist"
    assert os.access(gradlew_file, os.X_OK), "gradlew is not executable"


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
    assert "fun computeFrameLumaAndDelta" in content

    # Enforce Wi-Fi Safety Invariant in Kotlin source
    assert "WifiManager" not in content
    assert "WIFI_STATE" not in content
    assert "android.net.wifi" not in content

    # Enforce Zero Hardcoded Topology Invariant
    rfc1918_pattern = r"(192\.168\.\d+\.\d+|10\.\d+\.\d+\.\d+|172\.(1[6-9]|2\d|3[0-1])\.\d+\.\d+)"
    assert not re.search(rfc1918_pattern, content), "MotionLumaMonitor.kt contains hardcoded private RFC 1918 IP"


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
    assert "resolveStartUrlValue" in content
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
    assert "shouldSleepOnAmbientLuma" in content
    assert "shouldSleepOnIdleTimeout" in content

    # Runtime permissions request
    assert "Manifest.permission.CAMERA" in content
    assert "Manifest.permission.RECORD_AUDIO" in content
    assert "requestPermissions" in content
    assert "onRequestPermissionsResult" in content

    # Night quiet-hours schedule constants, decision classes, and methods
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
    assert "decideNightSchedule" in content
    assert "wasInNightWindow" in content
    assert "enum class NightScheduleAction" in content
    assert "data class NightScheduleDecision" in content

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
    assert "./gradlew testDebugUnitTest" in content


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


def test_ember_google_photos_bridge_interface():
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
    assert "fun isGooglePhotosAlbumUrlAllowed(" in source
    assert "fun extractGooglePhotosUrls(" in source


def test_main_activity_test_suite_structure():
    """Verify MainActivityTest.kt exists and defines JUnit 4 companion unit tests."""
    test_file = (
        REPO_ROOT
        / "app"
        / "src"
        / "test"
        / "java"
        / "ventures"
        / "brock"
        / "ember"
        / "MainActivityTest.kt"
    )
    assert test_file.exists(), "MainActivityTest.kt does not exist"
    content = test_file.read_text(encoding="utf-8")
    assert "class MainActivityTest" in content
    assert "@Test" in content
    assert "org.junit.Test" in content
    assert "isGooglePhotosAlbumUrlAllowed" in content
    assert "extractGooglePhotosUrls" in content
    assert "parseTimeMinutes" in content
    assert "isNightQuietHours" in content
    assert "decideNightSchedule" in content
    assert "shouldSleepOnAmbientLuma" in content
    assert "shouldSleepOnIdleTimeout" in content
    assert "resolveStartUrlValue" in content
    assert "extractOrigin" in content
    assert "parseAllowedOrigins" in content
    assert "isBridgeOriginAllowed" in content
    assert "isOtaUrlAllowed" in content
    assert "computeFrameLumaAndDelta" in content

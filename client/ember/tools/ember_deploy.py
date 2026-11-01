"""EMBER Android Kiosk Deployment Utility.

Downloads the compiled EMBER debug APK from GitHub Actions CI workflow artifacts
and installs / configures / launches the application on target Android hardware
(e.g., ApoloSign digital frame) via ADB.
"""

from __future__ import annotations

import argparse
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Any

DEFAULT_REPO = "brockventures/ember"
DEFAULT_ARTIFACT_NAME = "ember-debug-apk"
DEFAULT_PACKAGE_NAME = "ventures.brock.ember"
DEFAULT_ACTIVITY_NAME = "ventures.brock.ember/.MainActivity"
DEFAULT_START_URL = "https://hearth.local/display?hide_cursor=true"
DEFAULT_ADB_PATH = "/workspace/tools/bin/adb"


def resolve_adb_binary(override_path: str | Path | None = None) -> str:
    """Resolve the path to the adb binary.

    Checks:
    1. Explicit override argument or ADB_BIN env var.
    2. Standard container tools directory (/workspace/tools/bin/adb).
    3. System PATH via shutil.which.
    """
    if override_path:
        p = Path(override_path).expanduser().resolve()
        if p.exists() and os.access(p, os.X_OK):
            return str(p)
        return str(override_path)

    env_adb = os.environ.get("ADB_BIN")
    if env_adb:
        p = Path(env_adb).expanduser().resolve()
        if p.exists() and os.access(p, os.X_OK):
            return str(p)

    default_adb = Path(DEFAULT_ADB_PATH)
    if default_adb.exists() and os.access(default_adb, os.X_OK):
        return str(default_adb)

    system_adb = shutil.which("adb")
    if system_adb:
        return system_adb

    return DEFAULT_ADB_PATH


def download_apk_artifact(
    repo: str = DEFAULT_REPO,
    artifact_name: str = DEFAULT_ARTIFACT_NAME,
    dest_dir: Path | None = None,
) -> Path:
    """Download the latest APK artifact from GitHub Actions using gh CLI."""
    if dest_dir is None:
        dest_dir = Path(tempfile.mkdtemp(prefix="ember_artifact_"))
    dest_dir.mkdir(parents=True, exist_ok=True)

    cmd = [
        "gh",
        "run",
        "download",
        "--repo",
        repo,
        "-n",
        artifact_name,
        "--dir",
        str(dest_dir),
    ]

    res = subprocess.run(cmd, capture_output=True, text=True, check=False)
    if res.returncode != 0:
        raise RuntimeError(
            f"Failed to download artifact '{artifact_name}' from repo '{repo}': {res.stderr.strip()}"
        )

    apk_files = list(dest_dir.glob("**/*.apk"))
    if not apk_files:
        raise FileNotFoundError(
            f"No .apk files found in downloaded artifact directory '{dest_dir}'"
        )

    return apk_files[0]


def connect_device(adb_bin: str, device: str) -> None:
    """Connect to network ADB device if IP:port notation is used."""
    if ":" in device:
        cmd = [adb_bin, "connect", device]
        res = subprocess.run(cmd, capture_output=True, text=True, check=False)
        if res.returncode != 0:
            print(f"[WARN] adb connect to {device} returned {res.returncode}: {res.stderr.strip()}")


def disconnect_device(adb_bin: str, device: str) -> None:
    """Disconnect from network ADB device if IP:port notation is used."""
    if ":" in device:
        cmd = [adb_bin, "disconnect", device]
        res = subprocess.run(cmd, capture_output=True, text=True, check=False)
        if res.returncode != 0:
            print(f"[WARN] adb disconnect from {device} returned {res.returncode}: {res.stderr.strip()}")


def install_apk(adb_bin: str, device: str, apk_path: Path | str) -> None:
    """Install APK on the target device with replace and grant-all flags."""
    cmd = [adb_bin, "-s", device, "install", "-r", "-g", str(apk_path)]
    subprocess.run(cmd, capture_output=True, text=True, check=True)


def grant_permissions(
    adb_bin: str,
    device: str,
    package_name: str = DEFAULT_PACKAGE_NAME,
) -> None:
    """Explicitly grant camera and audio recording permissions."""
    permissions = [
        "android.permission.CAMERA",
        "android.permission.RECORD_AUDIO",
    ]
    for perm in permissions:
        cmd = [adb_bin, "-s", device, "shell", "pm", "grant", package_name, perm]
        subprocess.run(cmd, capture_output=True, text=True, check=True)


def launch_kiosk(
    adb_bin: str,
    device: str,
    start_url: str = DEFAULT_START_URL,
    activity_name: str = DEFAULT_ACTIVITY_NAME,
    bridge_origins: str | None = None,
    ota_enabled: bool | None = None,
    allow_mixed_content: bool | None = None,
) -> None:
    """Launch the EMBER MainActivity with the start_url intent extra and optional flags."""
    cmd = [
        adb_bin,
        "-s",
        device,
        "shell",
        "am",
        "start",
        "-n",
        activity_name,
        "-e",
        "start_url",
        start_url,
    ]
    if bridge_origins is not None:
        cmd.extend(["-e", "bridge_origins", bridge_origins])
    if ota_enabled is not None:
        val = ota_enabled if isinstance(ota_enabled, bool) else (str(ota_enabled).lower() in ("true", "1", "yes"))
        cmd.extend(["--ez", "ota_enabled", "true" if val else "false"])
    if allow_mixed_content is not None:
        val = allow_mixed_content if isinstance(allow_mixed_content, bool) else (str(allow_mixed_content).lower() in ("true", "1", "yes"))
        cmd.extend(["--ez", "allow_mixed_content", "true" if val else "false"])
    subprocess.run(cmd, capture_output=True, text=True, check=True)


def deploy(
    device: str,
    start_url: str = DEFAULT_START_URL,
    apk_path: Path | str | None = None,
    repo: str = DEFAULT_REPO,
    adb_bin: str | Path | None = None,
    bridge_origins: str | None = None,
    ota_enabled: bool | None = None,
    allow_mixed_content: bool | None = None,
) -> Path:
    """Coordinate the full deployment lifecycle for EMBER Android kiosk."""
    resolved_adb = resolve_adb_binary(adb_bin)

    if apk_path is None:
        print(f"Downloading latest {DEFAULT_ARTIFACT_NAME} artifact from {repo}...")
        resolved_apk = download_apk_artifact(repo=repo)
    else:
        resolved_apk = Path(apk_path).expanduser().resolve()
        if not resolved_apk.exists():
            raise FileNotFoundError(f"APK path does not exist: {resolved_apk}")

    print(f"Deploying APK: {resolved_apk}")
    print(f"Target device: {device}")
    connect_device(resolved_adb, device)

    try:
        print("Installing APK (-r -g)...")
        install_apk(resolved_adb, device, resolved_apk)

        print("Granting CAMERA and RECORD_AUDIO permissions...")
        grant_permissions(resolved_adb, device)

        print(f"Launching {DEFAULT_ACTIVITY_NAME} with start_url: {start_url}...")
        launch_kiosk(
            resolved_adb,
            device,
            start_url=start_url,
            bridge_origins=bridge_origins,
            ota_enabled=ota_enabled,
            allow_mixed_content=allow_mixed_content,
        )
    finally:
        print(f"Disconnecting ADB session from {device}...")
        disconnect_device(resolved_adb, device)

    print("Deployment completed successfully.")
    return resolved_apk


def parse_bool_flag(val: str | bool) -> bool:
    """Parse string or boolean CLI argument into boolean."""
    if isinstance(val, bool):
        return val
    if val.lower() in ("true", "1", "yes", "y"):
        return True
    if val.lower() in ("false", "0", "no", "n"):
        return False
    raise argparse.ArgumentTypeError(f"Invalid boolean value: {val}")


def build_parser() -> argparse.ArgumentParser:
    """Build CLI argument parser."""
    parser = argparse.ArgumentParser(
        description="Deploy EMBER Android Kiosk APK to target device via ADB."
    )
    parser.add_argument(
        "--device",
        required=True,
        help="Target ADB device address (e.g., 198.51.100.10:5555)",
    )
    parser.add_argument(
        "--url",
        default=DEFAULT_START_URL,
        help=f"Kiosk start URL (default: {DEFAULT_START_URL})",
    )
    parser.add_argument(
        "--apk",
        default=None,
        help="Path to local APK file. If omitted, downloads latest artifact from GitHub Actions.",
    )
    parser.add_argument(
        "--repo",
        default=DEFAULT_REPO,
        help=f"GitHub repository for downloading artifacts (default: {DEFAULT_REPO})",
    )
    parser.add_argument(
        "--adb-bin",
        default=None,
        help="Path to adb executable (default: auto-detected)",
    )
    parser.add_argument(
        "--bridge-origins",
        default=None,
        help="Comma-separated trusted JavaScript bridge and OTA origins (e.g., https://custom.local,https://extra.local)",
    )
    parser.add_argument(
        "--ota-enabled",
        nargs="?",
        const=True,
        default=None,
        type=parse_bool_flag,
        help="Enable or disable Over-The-Air (OTA) APK updates (default: true on device)",
    )
    parser.add_argument(
        "--no-ota-enabled",
        dest="ota_enabled",
        action="store_false",
        help="Disable Over-The-Air (OTA) APK updates",
    )
    parser.add_argument(
        "--allow-mixed-content",
        nargs="?",
        const=True,
        default=None,
        type=parse_bool_flag,
        help="Enable or disable HTTP mixed content in WebView (default: false on device)",
    )
    parser.add_argument(
        "--no-allow-mixed-content",
        dest="allow_mixed_content",
        action="store_false",
        help="Disable HTTP mixed content in WebView",
    )
    return parser


def main(argv: list[str] | None = None) -> int:
    """Main CLI entrypoint."""
    parser = build_parser()
    args = parser.parse_args(argv)

    try:
        kwargs: dict[str, Any] = {
            "device": args.device,
            "start_url": args.url,
            "apk_path": args.apk,
            "repo": args.repo,
            "adb_bin": args.adb_bin,
        }
        if args.bridge_origins is not None:
            kwargs["bridge_origins"] = args.bridge_origins
        if args.ota_enabled is not None:
            kwargs["ota_enabled"] = args.ota_enabled
        if getattr(args, "allow_mixed_content", None) is not None:
            kwargs["allow_mixed_content"] = args.allow_mixed_content

        deploy(**kwargs)
        return 0
    except (RuntimeError, FileNotFoundError, subprocess.SubprocessError, OSError) as exc:
        print(f"[ERROR] Deployment failed: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())

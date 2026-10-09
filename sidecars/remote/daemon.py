"""Unified Bluetooth HID & Android TV Remote sidecar for Mirrormere.

Manages:
1. Android TV Remote v2 (Wi-Fi mTLS) connection on port 6466.
2. Bluetooth LE HID Peripheral (Mirrormere Remote) with multi-report touch, mouse, and consumer keys.
3. Automated BLE pairing confirmation via BlueZ PairingAgent.
4. Physical touch event forwarding from /dev/input.
5. HTTP Control API on port 8092.
"""

import asyncio
import glob
import logging
import os
import signal
import struct
import sys
from typing import Optional

try:
    from aiohttp import web
except ImportError:
    web = None

if web:
    @web.middleware
    async def cors_middleware(request, handler):
        if request.method == "OPTIONS":
            resp = web.Response(status=204)
        else:
            try:
                resp = await handler(request)
            except web.HTTPException as ex:
                resp = ex
        resp.headers["Access-Control-Allow-Origin"] = "*"
        resp.headers["Access-Control-Allow-Methods"] = "GET, POST, OPTIONS"
        resp.headers["Access-Control-Allow-Headers"] = "Content-Type, Accept"
        return resp
else:
    cors_middleware = None


# Import local pure helper modules
try:
    from config import RemoteConfig, load_config
    from coords import normalize_coordinates, to_hid_digitizer
    from keycodes import lookup_consumer_key, lookup_remote_key, resolve_key_dispatch
except ImportError:
    from .config import RemoteConfig, load_config
    from .coords import normalize_coordinates, to_hid_digitizer
    from .keycodes import lookup_consumer_key, lookup_remote_key, resolve_key_dispatch

logging.basicConfig(level=logging.INFO, format="%(asctime)s [%(levelname)s] %(name)s: %(message)s")
logger = logging.getLogger("mirrormere-remote")

# Declarative YAML configuration (no env fallbacks for config values)
_default_config = load_config()
CHROMECAST_HOST = _default_config.chromecast_host
CERT_DIR = _default_config.cert_dir
CERT_FALLBACK_DIR = "/var/lib/kiosk-touch"
TOUCH_DEVICE_PATH = _default_config.touch_device
HTTP_PORT = _default_config.http_port
ADVERT_NAME = _default_config.advert_name
AUTO_CONFIRM_PAIRING = _default_config.auto_confirm_pairing

# Multi-Report HID Descriptor (Mouse + Touch + Consumer Control)
REPORT_MAP = bytes([
    # Report 1: Relative Mouse
    0x05, 0x01,        # Usage Page (Generic Desktop)
    0x09, 0x02,        # Usage (Mouse)
    0xa1, 0x01,        # Collection (Application)
    0x85, 0x01,        #   Report ID (1)
    0x09, 0x01,        #   Usage (Pointer)
    0xa1, 0x00,        #   Collection (Physical)
    0x05, 0x09,        #     Usage Page (Button)
    0x19, 0x01,        #     Usage Minimum (1)
    0x29, 0x03,        #     Usage Maximum (3)
    0x15, 0x00,        #     Logical Minimum (0)
    0x25, 0x01,        #     Logical Maximum (1)
    0x95, 0x03,        #     Report Count (3)
    0x75, 0x01,        #     Report Size (1)
    0x81, 0x02,        #     Input (Data,Var,Abs)
    0x95, 0x01,        #     Report Count (1)
    0x75, 0x05,        #     Report Size (5)
    0x81, 0x03,        #     Input (Cnst,Var,Abs)
    0x05, 0x01,        #     Usage Page (Generic Desktop)
    0x09, 0x30,        #     Usage (X)
    0x09, 0x31,        #     Usage (Y)
    0x15, 0x81,        #     Logical Minimum (-127)
    0x25, 0x7f,        #     Logical Maximum (127)
    0x75, 0x08,        #     Report Size (8)
    0x95, 0x02,        #     Report Count (2)
    0x81, 0x06,        #     Input (Data,Var,Rel)
    0xc0,              #   End Collection
    0xc0,              # End Collection

    # Report 2: Absolute Touch Screen (Digitizer)
    0x05, 0x0d,        # Usage Page (Digitizer)
    0x09, 0x04,        # Usage (Touch Screen)
    0xa1, 0x01,        # Collection (Application)
    0x85, 0x02,        #   Report ID (2)
    0x09, 0x22,        #   Usage (Finger)
    0xa1, 0x02,        #   Collection (Logical)
    0x09, 0x42,        #     Usage (Tip Switch)
    0x09, 0x32,        #     Usage (In Range)
    0x15, 0x00,        #     Logical Minimum (0)
    0x25, 0x01,        #     Logical Maximum (1)
    0x75, 0x01,        #     Report Size (1)
    0x95, 0x02,        #     Report Count (2)
    0x81, 0x02,        #     Input (Data,Var,Abs)
    0x95, 0x01,        #     Report Count (1)
    0x75, 0x06,        #     Report Size (6)
    0x81, 0x03,        #     Input (Cnst,Var,Abs)
    0x05, 0x01,        #     Usage Page (Generic Desktop)
    0x09, 0x30,        #     Usage (X)
    0x15, 0x00,        #     Logical Minimum (0)
    0x26, 0xff, 0x7f,  #     Logical Maximum (32767)
    0x75, 0x10,        #     Report Size (16)
    0x95, 0x01,        #     Report Count (1)
    0x81, 0x02,        #     Input (Data,Var,Abs)
    0x09, 0x31,        #     Usage (Y)
    0x15, 0x00,        #     Logical Minimum (0)
    0x26, 0xff, 0x47,  #     Logical Maximum (18431) - 16:9 Aspect Ratio (32768 x 18432)
    0x75, 0x10,        #     Report Size (16)
    0x95, 0x01,        #     Report Count (1)
    0x81, 0x02,        #     Input (Data,Var,Abs)
    0xc0,              #   End Collection
    0xc0,              # End Collection

    # Report 3: Consumer Keys (Back, Home, Media, Volume)
    0x05, 0x0c,        # Usage Page (Consumer)
    0x09, 0x01,        # Usage (Consumer Control)
    0xa1, 0x01,        # Collection (Application)
    0x85, 0x03,        #   Report ID (3)
    0x19, 0x00,        #   Usage Minimum (0)
    0x2a, 0x9c, 0x02,  #   Usage Maximum (0x029C)
    0x15, 0x00,        #   Logical Minimum (0)
    0x26, 0x9c, 0x02,  #   Logical Maximum (0x029C)
    0x95, 0x01,        #   Report Count (1)
    0x75, 0x10,        #   Report Size (16)
    0x81, 0x00,        #   Input (Data,Ary,Abs)
    0xc0               # End Collection
])

class AndroidRemoteHelper:
    """Manages Wi-Fi Android TV Remote v2 connection and command dispatching."""

    def __init__(self, host: str, cert_dir: str):
        self.host = host
        self.cert_dir = cert_dir
        self.remote = None
        self.connected = False
        self.cert_file = os.path.join(cert_dir, "cert.pem")
        self.key_file = os.path.join(cert_dir, "key.pem")

        # Fallback check
        if not os.path.exists(self.cert_file) and os.path.exists(os.path.join(CERT_FALLBACK_DIR, "cert.pem")):
            self.cert_file = os.path.join(CERT_FALLBACK_DIR, "cert.pem")
            self.key_file = os.path.join(CERT_FALLBACK_DIR, "key.pem")

    def has_credentials(self) -> bool:
        return os.path.exists(self.cert_file) and os.path.exists(self.key_file)

    async def start(self):
        """Asynchronously maintain connection to Android TV Remote."""
        if not self.has_credentials():
            logger.warning(
                "Android TV Remote certificates not found in %s or %s; Wi-Fi remote disabled until paired.",
                self.cert_dir,
                CERT_FALLBACK_DIR,
            )
            return

        try:
            from androidtvremote2.androidtv_remote import AndroidTVRemote
            self.remote = AndroidTVRemote(
                client_name="Mirrormere Remote",
                certfile=self.cert_file,
                keyfile=self.key_file,
                host=self.host,
            )
        except Exception as e:
            logger.error("Failed to initialize AndroidTVRemote client: %s", e)
            return

        backoff = 1.0
        while True:
            try:
                logger.info("Connecting to Android TV Remote at %s:6466...", self.host)
                await self.remote.async_connect()
                self.connected = True
                backoff = 1.0
                logger.info("Android TV Remote connected successfully (power=%s)", self.remote.is_on)

                # Keep connection alive while connected
                while self.remote._remote_message_protocol:
                    await asyncio.sleep(5)
            except Exception as e:
                self.connected = False
                logger.warning("Android TV Remote connection error: %s (retry in %.1fs)", e, backoff)
                await asyncio.sleep(backoff)
                backoff = min(30.0, backoff * 1.5)

    def send_key(self, key_command: str) -> bool:
        """Send a key command string (e.g. DPAD_CENTER, BACK)."""
        if not self.connected or not self.remote:
            logger.warning("Cannot send key %s: Android TV Remote disconnected", key_command)
            return False
        try:
            self.remote.send_key_command(key_command)
            return True
        except Exception as e:
            logger.error("Failed to send key %s: %s", key_command, e)
            return False

    async def auto_approve_pairing(self):
        """Deprecated: No-op. Bluetooth pairing confirmation is handled via BlueZ PairingAgent."""
        logger.debug("auto_approve_pairing skipped (pairing confirmation handled via D-Bus Agent)")

    def launch_app(self, app_link: str) -> bool:
        """Launch an app intent or deep link."""
        if not self.connected or not self.remote:
            return False
        try:
            self.remote.send_launch_app_command(app_link)
            return True
        except Exception as e:
            logger.error("Failed to launch app %s: %s", app_link, e)
            return False

# Initialize BlueZ GATT and Agent if bluez-peripheral is installed
try:
    from bluez_peripheral.util import get_message_bus, Adapter
    from bluez_peripheral.advert import Advertisement
    from bluez_peripheral.agent import BaseAgent, AgentCapability
    from bluez_peripheral.gatt.service import Service, ServiceCollection
    from bluez_peripheral.gatt.characteristic import characteristic, CharacteristicFlags
    from bluez_peripheral.gatt.descriptor import descriptor, DescriptorFlags
    from dbus_next.service import dbus_property, method
    from dbus_next.constants import PropertyAccess

    class DeviceInfoService(Service):
        def __init__(self):
            super().__init__("180A", True)

        @characteristic("2A29", CharacteristicFlags.READ)
        def manufacturer_name(self, options):
            return b"Mirrormere"

        @characteristic("2A24", CharacteristicFlags.READ)
        def model_number(self, options):
            return b"Cockpit-Remote-v1"

    class BatteryService(Service):
        def __init__(self):
            super().__init__("180F", True)

        @characteristic("2A19", CharacteristicFlags.READ | CharacteristicFlags.NOTIFY)
        def battery_level(self, options):
            return bytes([100])

    class HIDService(Service):
        def __init__(self):
            self._mouse_val = bytes([0, 0, 0])
            self._touch_val = bytes([0, 0, 0, 0, 0])
            self._consumer_val = bytes([0, 0])
            self._protocol_mode = bytes([1])
            super().__init__("1812", True)

        @characteristic("2A4A", CharacteristicFlags.READ)
        def hid_information(self, options):
            return bytes([0x01, 0x01, 0x00, 0x02])

        @characteristic("2A4B", CharacteristicFlags.READ)
        def report_map(self, options):
            return REPORT_MAP

        @characteristic("2A4C", CharacteristicFlags.READ | CharacteristicFlags.WRITE_WITHOUT_RESPONSE)
        def hid_control_point(self, options):
            return bytes([0])

        @hid_control_point.setter
        def hid_control_point(self, value, options):
            pass

        @characteristic("2A4E", CharacteristicFlags.READ | CharacteristicFlags.WRITE_WITHOUT_RESPONSE)
        def protocol_mode(self, options):
            return self._protocol_mode

        @protocol_mode.setter
        def protocol_mode(self, value, options):
            self._protocol_mode = value

        @characteristic("2A4D", CharacteristicFlags.READ | CharacteristicFlags.NOTIFY)
        def report_mouse(self, options):
            return self._mouse_val

        @descriptor("2908", report_mouse)
        def report_mouse_ref(self, options):
            return bytes([1, 1])

        @characteristic("2A4D", CharacteristicFlags.READ | CharacteristicFlags.NOTIFY)
        def report_touch(self, options):
            return self._touch_val

        @descriptor("2908", report_touch)
        def report_touch_ref(self, options):
            return bytes([2, 1])

        @characteristic("2A4D", CharacteristicFlags.READ | CharacteristicFlags.NOTIFY)
        def report_consumer(self, options):
            return self._consumer_val

        @descriptor("2908", report_consumer)
        def report_consumer_ref(self, options):
            return bytes([3, 1])

        def send_mouse(self, buttons: int, dx: int = 0, dy: int = 0):
            val = bytes([buttons & 0x07, dx & 0xFF, dy & 0xFF])
            self._mouse_val = val
            if hasattr(self, "report_mouse") and hasattr(self.report_mouse, "changed"):
                self.report_mouse.changed(val)

        def send_touch(self, tip_down: bool, x_ratio: float, y_ratio: float):
            flags = 0x03 if tip_down else 0x00
            x_raw, y_raw = to_hid_digitizer(x_ratio, y_ratio)
            val = bytes([flags]) + struct.pack("<HH", x_raw, y_raw)
            self._touch_val = val
            if hasattr(self, "report_touch") and hasattr(self.report_touch, "changed"):
                self.report_touch.changed(val)

        def send_consumer_key(self, keycode: int):
            val = struct.pack("<H", keycode)
            self._consumer_val = val
            if hasattr(self, "report_consumer") and hasattr(self.report_consumer, "changed"):
                self.report_consumer.changed(val)

    class KioskAdvertisement(Advertisement):
        @dbus_property(PropertyAccess.READWRITE)
        def TxPower(self) -> "n":
            return 0

        @TxPower.setter
        def TxPower(self, val: "n"):
            pass

    class FastKioskAdvertisement(KioskAdvertisement):
        def __init__(
            self,
            *args,
            min_interval: int = 30,
            max_interval: int = 50,
            **kwargs,
        ):
            self._min_interval = int(min_interval)
            self._max_interval = int(max_interval)
            super().__init__(*args, **kwargs)

        @dbus_property(PropertyAccess.READ)
        def MinInterval(self) -> "u":
            return self._min_interval

        @dbus_property(PropertyAccess.READ)
        def MaxInterval(self) -> "u":
            return self._max_interval

    class PairingAgent(BaseAgent):
        capability = AgentCapability.DISPLAY_YES_NO

        def __init__(self, remote_helper: AndroidRemoteHelper, auto_confirm: bool = True):
            self.remote_helper = remote_helper
            self.auto_confirm = auto_confirm
            super().__init__(AgentCapability.DISPLAY_YES_NO)

        @method()
        def Release(self):
            logger.info("Agent released")

        @method()
        def RequestPinCode(self, device: "o") -> "s":
            logger.info("Auto-replying PIN code 0000 for %s", device)
            return "0000"

        @method()
        def RequestPasskey(self, device: "o") -> "u":
            logger.info("RequestPasskey for %s", device)
            return 0

        @method()
        def DisplayPasskey(self, device: "o", passkey: "u", entered: "q"):
            logger.info("DisplayPasskey for %s: %06d", device, passkey)

        @method()
        def RequestConfirmation(self, device: "o", passkey: "u"):
            logger.info("Auto-confirming pairing request for %s (passkey %06d)", device, passkey)
            mac = device.split("/")[-1].replace("dev_", "").replace("_", ":").upper()
            asyncio.create_task(self._trust_device(mac))

        @method()
        def RequestAuthorization(self, device: "o"):
            logger.info("Auto-authorizing device %s", device)
            mac = device.split("/")[-1].replace("dev_", "").replace("_", ":").upper()
            asyncio.create_task(self._trust_device(mac))

        @method()
        def AuthorizeService(self, device: "o", uuid: "s"):
            logger.info("Auto-authorizing service %s for device %s", uuid, device)
            mac = device.split("/")[-1].replace("dev_", "").replace("_", ":").upper()
            asyncio.create_task(self._trust_device(mac))

        @method()
        def Cancel(self):
            logger.info("Pairing cancelled by peer")

        async def _trust_device(self, mac: str):
            for attempt in range(5):
                await asyncio.sleep(0.5)
                try:
                    proc = await asyncio.create_subprocess_exec(
                        "bluetoothctl", "trust", mac,
                        stdout=asyncio.subprocess.PIPE,
                        stderr=asyncio.subprocess.PIPE,
                    )
                    stdout, _ = await proc.communicate()
                    if proc.returncode == 0:
                        logger.info("Successfully trusted device %s", mac)
                        return
                    logger.debug("bluetoothctl trust %s (attempt %d) output: %s", mac, attempt + 1, stdout.decode().strip())
                except Exception as e:
                    logger.warning("Failed to auto-trust %s (attempt %d): %s", mac, attempt + 1, e)

except ImportError:
    logger.warning("bluez-peripheral not installed; running in mock/test mode.")
    HIDService = None
    Advertisement = None
    KioskAdvertisement = None
    FastKioskAdvertisement = None
    PairingAgent = None
    AgentCapability = None


class TouchReader:
    """Reads touch events from physical evdev device non-exclusively."""

    def __init__(self, hid_service, target_path: str):
        self.hid = hid_service
        self.target_path = target_path
        self.touch_active = False
        self.cur_x = 0
        self.cur_y = 0
        self.min_x = 0.0
        self.max_x = 2880.0
        self.min_y = 0.0
        self.max_y = 1620.0

    def resolve_device(self) -> Optional[str]:
        if self.target_path and os.path.exists(self.target_path):
            return self.target_path
        # Search by-id or by touch capability
        matches = glob.glob("/dev/input/by-id/*touch*")
        if matches:
            return matches[0]
        try:
            import evdev
            for p in sorted(glob.glob("/dev/input/event*")):
                try:
                    d = evdev.InputDevice(p)
                    caps = d.capabilities()
                    has_key = evdev.ecodes.EV_KEY in caps
                    has_touch = has_key and (evdev.ecodes.BTN_TOUCH in caps[evdev.ecodes.EV_KEY])
                    has_abs = evdev.ecodes.EV_ABS in caps
                    if has_abs:
                        abs_codes = caps[evdev.ecodes.EV_ABS]
                        if isinstance(abs_codes, dict):
                            keys = list(abs_codes.keys())
                        elif isinstance(abs_codes, list):
                            keys = [c[0] if isinstance(c, (list, tuple)) else c for c in abs_codes]
                        else:
                            keys = []
                        has_x = (evdev.ecodes.ABS_MT_POSITION_X in keys) or (evdev.ecodes.ABS_X in keys)
                        has_y = (evdev.ecodes.ABS_MT_POSITION_Y in keys) or (evdev.ecodes.ABS_Y in keys)
                        if has_touch and has_x and has_y:
                            return p
                except Exception:
                    continue
        except ImportError:
            pass
        return None

    def update_absinfo(self, dev):
        """Query hardware axis limits from evdev absinfo to calibrate scaling."""
        try:
            try:
                import evdev
                x_codes = (evdev.ecodes.ABS_MT_POSITION_X, evdev.ecodes.ABS_X)
                y_codes = (evdev.ecodes.ABS_MT_POSITION_Y, evdev.ecodes.ABS_Y)
            except ImportError:
                x_codes = (0x35, 0x00)
                y_codes = (0x36, 0x01)

            x_info = None
            y_info = None
            if hasattr(dev, "absinfo"):
                for code in x_codes:
                    try:
                        x_info = dev.absinfo(code)
                        if x_info:
                            break
                    except Exception:
                        pass
                for code in y_codes:
                    try:
                        y_info = dev.absinfo(code)
                        if y_info:
                            break
                    except Exception:
                        pass
            if x_info and getattr(x_info, "max", 0) > getattr(x_info, "min", 0):
                self.min_x = float(x_info.min)
                self.max_x = float(x_info.max)
            if y_info and getattr(y_info, "max", 0) > getattr(y_info, "min", 0):
                self.min_y = float(y_info.min)
                self.max_y = float(y_info.max)
            logger.info("TouchReader calibrated: X=[%.1f, %.1f], Y=[%.1f, %.1f]", self.min_x, self.max_x, self.min_y, self.max_y)
        except Exception as e:
            logger.warning("Failed to calibrate touch device absinfo: %s", e)

    def _normalize(self) -> tuple[float, float]:
        return normalize_coordinates(
            self.cur_x,
            self.cur_y,
            min_x=self.min_x,
            max_x=self.max_x,
            min_y=self.min_y,
            max_y=self.max_y,
        )

    async def run(self):
        try:
            import evdev
        except ImportError:
            logger.warning("evdev not available; physical touch reader disabled.")
            return

        while True:
            dev_path = self.resolve_device()
            if not dev_path or not os.path.exists(dev_path):
                logger.debug("Touch device %s not found; retrying in 5s...", self.target_path)
                await asyncio.sleep(5)
                continue

            try:
                dev = evdev.InputDevice(dev_path)
                logger.info("Opened physical touch device: %s (%s)", dev.path, dev.name)
                self.update_absinfo(dev)
                prev_touch_active = False
                async for ev in dev.async_read_loop():
                    if ev.type == evdev.ecodes.EV_ABS:
                        if ev.code in (evdev.ecodes.ABS_MT_POSITION_X, evdev.ecodes.ABS_X):
                            self.cur_x = ev.value
                        elif ev.code in (evdev.ecodes.ABS_MT_POSITION_Y, evdev.ecodes.ABS_Y):
                            self.cur_y = ev.value
                        elif ev.code == evdev.ecodes.ABS_MT_TRACKING_ID:
                            self.touch_active = (ev.value >= 0)
                            if not self.touch_active:
                                prev_touch_active = False
                                if self.hid:
                                    x_r, y_r = self._normalize()
                                    self.hid.send_mouse(0, 0, 0)
                                    self.hid.send_touch(False, x_r, y_r)
                    elif ev.type == evdev.ecodes.EV_KEY and ev.code == evdev.ecodes.BTN_TOUCH:
                        self.touch_active = bool(ev.value)
                        if not self.touch_active:
                            prev_touch_active = False
                            if self.hid:
                                x_r, y_r = self._normalize()
                                self.hid.send_mouse(0, 0, 0)
                                self.hid.send_touch(False, x_r, y_r)
                    elif ev.type == evdev.ecodes.EV_SYN and ev.code == evdev.ecodes.SYN_REPORT:
                        if self.touch_active and self.hid:
                            x_r, y_r = self._normalize()
                            if not prev_touch_active:
                                self.hid.send_mouse(1, 0, 0)
                                prev_touch_active = True
                            self.hid.send_touch(True, x_r, y_r)
            except Exception as e:
                logger.warning("Touch reader disconnected or errored (%s); re-opening in 3s...", e)
                if self.touch_active and self.hid:
                    try:
                        self.hid.send_mouse(0, 0, 0)
                    except Exception:
                        pass
                    try:
                        x_r, y_r = self._normalize()
                        self.hid.send_touch(False, x_r, y_r)
                    except Exception:
                        pass
                self.touch_active = False
                prev_touch_active = False
                await asyncio.sleep(3)


class RemoteDaemon:
    def __init__(self, config: Optional[RemoteConfig] = None):
        self.config = config or load_config()
        self.remote_helper = AndroidRemoteHelper(self.config.chromecast_host, self.config.cert_dir)
        self.hid = HIDService() if HIDService else None
        self.touch_reader = TouchReader(self.hid, self.config.touch_device)
        self.bus = None
        self.adapter = None
        self.agent = None
        self.advert = None
        self.bluetooth_lock = asyncio.Lock()
        self.last_known_paired_devices = []
        self.keepalive_task = None

    async def handle_healthz(self, request):
        return web.json_response({
            "status": "ok",
            "service": "mirrormere-remote",
            "wifi_remote": {
                "connected": self.remote_helper.connected,
                "has_credentials": self.remote_helper.has_credentials(),
                "host": self.remote_helper.host,
            },
            "bluetooth": {
                "active": self.hid is not None,
                "advert_name": self.config.advert_name,
                "keepalive": self.config.bluetooth_keepalive,
                "paired_devices": self.last_known_paired_devices,
            },
        })

    async def handle_tap(self, request):
        try:
            data = await request.json()
        except Exception:
            return web.json_response({"status": "error", "message": "invalid json"}, status=400)

        try:
            raw_x = float(data.get("x", 0.5))
            raw_y = float(data.get("y", 0.5))
        except (ValueError, TypeError):
            return web.json_response({"status": "error", "message": "invalid coordinates"}, status=400)

        x_ratio = max(0.0, min(1.0, raw_x))
        y_ratio = max(0.0, min(1.0, raw_y))
        logger.info("Synthetic tap at (%.4f, %.4f)", x_ratio, y_ratio)

        if self.hid:
            try:
                self.hid.send_mouse(1, 0, 0)
                self.hid.send_touch(True, x_ratio, y_ratio)
                await asyncio.sleep(0.05)
            finally:
                try:
                    self.hid.send_mouse(0, 0, 0)
                except Exception:
                    pass
                try:
                    self.hid.send_touch(False, x_ratio, y_ratio)
                except Exception:
                    pass
            return web.json_response({"status": "ok", "action": "tap", "x": x_ratio, "y": y_ratio})
        return web.json_response({"status": "error", "message": "BLE HID not initialized"}, status=503)

    async def handle_touch(self, request):
        try:
            data = await request.json()
        except Exception:
            return web.json_response({"status": "error", "message": "invalid json"}, status=400)

        action = str(data.get("action", "tap")).lower()
        try:
            raw_x = float(data.get("x", 0.5))
            raw_y = float(data.get("y", 0.5))
        except (ValueError, TypeError):
            return web.json_response({"status": "error", "message": "invalid coordinates"}, status=400)

        x_ratio = max(0.0, min(1.0, raw_x))
        y_ratio = max(0.0, min(1.0, raw_y))

        if not self.hid:
            return web.json_response({"status": "error", "message": "BLE HID not initialized"}, status=503)

        if action in ("down", "move"):
            if action == "down":
                self.hid.send_mouse(1, 0, 0)
            self.hid.send_touch(True, x_ratio, y_ratio)
        elif action == "up":
            self.hid.send_mouse(0, 0, 0)
            self.hid.send_touch(False, x_ratio, y_ratio)
        elif action == "tap":
            try:
                self.hid.send_mouse(1, 0, 0)
                self.hid.send_touch(True, x_ratio, y_ratio)
                await asyncio.sleep(0.05)
            finally:
                try:
                    self.hid.send_mouse(0, 0, 0)
                except Exception:
                    pass
                try:
                    self.hid.send_touch(False, x_ratio, y_ratio)
                except Exception:
                    pass
        else:
            return web.json_response({"status": "error", "message": f"unknown action: {action}"}, status=400)

        return web.json_response({"status": "ok", "action": action, "x": x_ratio, "y": y_ratio})

    async def handle_key(self, request):
        try:
            data = await request.json()
        except Exception:
            return web.json_response({"status": "error", "message": "invalid json"}, status=400)

        key_name = str(data.get("key", "")).strip().lower()
        hid_code, remote_cmd = resolve_key_dispatch(key_name)

        if not hid_code and not remote_cmd:
            return web.json_response({"status": "error", "message": f"unknown key: {key_name}"}, status=400)

        dispatched = []
        # Dispatch via HID consumer key if supported
        if hid_code and self.hid:
            self.hid.send_consumer_key(hid_code)
            await asyncio.sleep(0.05)
            self.hid.send_consumer_key(0x0000)
            dispatched.append("ble_consumer")

        # Also dispatch via Wi-Fi remote if available
        if remote_cmd and self.remote_helper.connected:
            if self.remote_helper.send_key(remote_cmd):
                dispatched.append("wifi_remote")

        if not dispatched:
            return web.json_response({"status": "error", "message": "transport unavailable"}, status=503)

        return web.json_response({"status": "ok", "key": key_name, "dispatched": dispatched})

    async def handle_remote_app(self, request):
        data = await request.json()
        app = data.get("app")
        if not app:
            return web.json_response({"status": "error", "message": "missing app parameter"}, status=400)
        ok = self.remote_helper.launch_app(app)
        return web.json_response({"status": "ok" if ok else "failed", "app": app})

    async def handle_remote_status(self, request):
        return web.json_response({
            "wifi_remote_connected": self.remote_helper.connected,
            "has_credentials": self.remote_helper.has_credentials(),
            "chromecast_host": self.remote_helper.host,
            "ble_active": self.hid is not None,
            "bluetooth_keepalive": self.config.bluetooth_keepalive,
            "paired_devices": self.last_known_paired_devices,
        })

    async def _run_bluetoothctl(self, *args: str, timeout: float = 5.0) -> tuple[int, bytes, bytes]:
        """Runs a bluetoothctl command safely with a timeout, killing and reaping any hanging process to prevent zombie leaks."""
        proc = await asyncio.create_subprocess_exec(
            "bluetoothctl",
            *args,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
        )
        try:
            stdout, stderr = await asyncio.wait_for(proc.communicate(), timeout=timeout)
            return (proc.returncode if proc.returncode is not None else 0), stdout, stderr
        except asyncio.TimeoutError:
            try:
                proc.kill()
            except ProcessLookupError:
                pass
            except Exception as kill_err:
                logger.debug("Error killing timed-out bluetoothctl process: %s", kill_err)
            try:
                await proc.wait()
            except Exception as wait_err:
                logger.debug("Error awaiting timed-out bluetoothctl process: %s", wait_err)
            raise

    async def handle_bluetooth_remove(self, request):
        try:
            data = await request.json()
        except Exception:
            return web.json_response({"status": "error", "message": "invalid json"}, status=400)
        mac = str(data.get("mac", "")).strip().upper()
        if not mac:
            return web.json_response({"status": "error", "message": "missing mac parameter"}, status=400)
        try:
            code, stdout, stderr = await self._run_bluetoothctl("remove", mac, timeout=5.0)
            if code == 0:
                logger.info("Successfully removed/unpaired bluetooth device %s", mac)
                return web.json_response({"status": "ok", "action": "remove", "mac": mac})
            else:
                logger.warning("bluetoothctl remove %s failed with code %d: %s", mac, code, stderr.decode())
                return web.json_response({"status": "error", "message": f"bluetoothctl exit code {code}"}, status=500)
        except Exception as e:
            logger.warning("Failed to remove bluetooth device %s: %s", mac, e)
            return web.json_response({"status": "error", "message": str(e)}, status=500)

    async def handle_bluetooth_reconnect(self, request):
        try:
            data = await request.json()
        except Exception:
            data = {}
        target_mac = data.get("mac")
        force = bool(data.get("force", False))
        results = await self._check_and_reconnect_bluetooth(target_mac=target_mac, force=force)
        return web.json_response({"status": "ok", "reconnected": results})

    async def _cleanup_stale_connections(self):
        """Disconnects paired devices that BlueZ still holds from a previous container lifecycle.

        This clears ghost ACL connections and forces client devices (like Android TV) to
        re-establish the link against our freshly exported GATT table and re-subscribe to notifications.
        """
        async with self.bluetooth_lock:
            try:
                _, stdout, _ = await self._run_bluetoothctl("devices", "Paired", timeout=5.0)
                lines = stdout.decode().splitlines()
            except Exception as e:
                logger.debug("Could not query paired devices for startup cleanup: %s", e)
                return

            for line in lines:
                parts = line.strip().split(maxsplit=2)
                if len(parts) >= 2 and parts[0] == "Device":
                    mac = parts[1].upper()
                    try:
                        _, info_out, _ = await self._run_bluetoothctl("info", mac, timeout=5.0)
                        if any(l.strip() == "Connected: yes" for l in info_out.decode().splitlines()):
                            logger.info("Found pre-existing/stale connection to %s on startup; resetting link...", mac)
                            await self._run_bluetoothctl("disconnect", mac, timeout=5.0)
                    except Exception as e:
                        logger.debug("Failed checking/resetting stale connection for %s: %s", mac, e)

    async def _check_and_reconnect_bluetooth(self, target_mac: Optional[str] = None, force: bool = False) -> list[dict]:
        """Inspects paired Bluetooth devices and attempts reconnection for disconnected devices."""
        reconnect_results = []
        async with self.bluetooth_lock:
            try:
                _, stdout, _ = await self._run_bluetoothctl("devices", "Paired", timeout=5.0)
                lines = stdout.decode().splitlines()
            except Exception as e:
                logger.debug("Failed to list paired devices via bluetoothctl: %s", e)
                return reconnect_results

            current_devices = []
            for line in lines:
                parts = line.strip().split(maxsplit=2)
                if len(parts) >= 2 and parts[0] == "Device":
                    mac = parts[1].upper()
                    name = parts[2] if len(parts) > 2 else "Unknown"
                    if target_mac and mac != target_mac.upper():
                        continue

                    is_connected = False
                    services_resolved = False
                    try:
                        _, info_out, _ = await self._run_bluetoothctl("info", mac, timeout=5.0)
                        for info_line in info_out.decode().splitlines():
                            stripped = info_line.strip()
                            if stripped == "Connected: yes":
                                is_connected = True
                            elif stripped == "ServicesResolved: yes":
                                services_resolved = True
                    except Exception as e:
                        logger.debug("Failed to check info for %s: %s", mac, e)

                    if force and is_connected:
                        logger.info("Forced reconnect requested for %s (%s); disconnecting first...", mac, name)
                        try:
                            await self._run_bluetoothctl("disconnect", mac, timeout=5.0)
                            is_connected = False
                        except Exception as e:
                            logger.debug("Failed to disconnect %s during forced reconnect: %s", mac, e)

                    current_devices.append({
                        "mac": mac,
                        "name": name,
                        "connected": is_connected,
                        "services_resolved": services_resolved,
                    })

                    if not is_connected:
                        logger.info("Paired Bluetooth device %s (%s) is disconnected; attempting auto-reconnect...", mac, name)
                        success = False
                        try:
                            code, _, _ = await self._run_bluetoothctl("connect", mac, timeout=5.0)
                            success = (code == 0)
                            if success:
                                logger.info("Successfully reconnected to Bluetooth device %s (%s)", mac, name)
                                is_connected = True
                            else:
                                logger.debug("Auto-reconnect to %s returned non-zero code (%s)", mac, str(code))
                        except Exception as conn_err:
                            logger.debug("Auto-reconnect attempt to %s timed out or failed: %s", mac, conn_err)

                        reconnect_results.append({"mac": mac, "name": name, "reconnected": success})

            self.last_known_paired_devices = current_devices
            return reconnect_results

    async def _bluetooth_keepalive_loop(self):
        """Background loop to periodically ensure paired devices stay connected."""
        logger.info("Bluetooth keepalive monitor active (interval=%.1fs)", self.config.bluetooth_keepalive_interval)
        try:
            await asyncio.sleep(min(2.0, self.config.bluetooth_keepalive_interval))
            await self._check_and_reconnect_bluetooth()
        except asyncio.CancelledError:
            return
        except Exception as e:
            logger.warning("Error in initial bluetooth keepalive check: %s", e)

        while True:
            try:
                await asyncio.sleep(self.config.bluetooth_keepalive_interval)
                await self._check_and_reconnect_bluetooth()
            except asyncio.CancelledError:
                break
            except Exception as e:
                logger.warning("Error in bluetooth keepalive worker: %s", e)

    async def _configure_adapter(self):
        """Ensure adapter is powered, named, discoverable, and pairable on startup."""
        if self.adapter:
            try:
                await self.adapter.set_powered(True)
                await self.adapter.set_alias(self.config.advert_name)
            except Exception as e:
                logger.warning("Failed to configure adapter alias/power: %s", e)

        for cmd in [
            ["bluetoothctl", "discoverable-timeout", "0"],
            ["bluetoothctl", "discoverable", "on"],
            ["bluetoothctl", "pairable", "on"],
        ]:
            try:
                proc = await asyncio.create_subprocess_exec(*cmd)
                await proc.wait()
            except Exception as e:
                logger.debug("bluetoothctl config '%s' skipped/failed: %s", " ".join(cmd), e)

    async def start(self):
        # Start Wi-Fi remote background worker
        asyncio.create_task(self.remote_helper.start())

        # Setup BlueZ Peripheral if available
        if HIDService:
            try:
                self.bus = await get_message_bus()
                self.adapter = await Adapter.get_first(self.bus)
                self.agent = PairingAgent(self.remote_helper, auto_confirm=self.config.auto_confirm_pairing)
                await self.agent.register(self.bus)

                dev_info = DeviceInfoService()
                battery = BatteryService()
                collection = ServiceCollection([dev_info, battery, self.hid])
                await collection.register(self.bus, path="/com/mirrormere/gatt", adapter=self.adapter)

                await self._configure_adapter()
                try:
                    self.advert = FastKioskAdvertisement(
                        localName=self.config.advert_name,
                        serviceUUIDs=["1812", "180F", "180A"],
                        appearance=0x03C2,
                        timeout=0,
                        min_interval=self.config.advert_min_interval,
                        max_interval=self.config.advert_max_interval,
                    )
                    await self.advert.register(self.bus, adapter=self.adapter)
                    logger.info(
                        "BLE peripheral & fast advertisement registered as '%s' (0x03C2, interval=%d-%dms)",
                        self.config.advert_name,
                        self.config.advert_min_interval,
                        self.config.advert_max_interval,
                    )
                except Exception as adv_err:
                    logger.warning("Fast interval advertisement registration failed (%s); falling back to standard advertisement", adv_err)
                    self.advert = KioskAdvertisement(
                        localName=self.config.advert_name,
                        serviceUUIDs=["1812", "180F", "180A"],
                        appearance=0x03C2,
                        timeout=0,
                    )
                    await self.advert.register(self.bus, adapter=self.adapter)
                    logger.info("BLE peripheral & standard advertisement registered as '%s' (0x03C2)", self.config.advert_name)

                # Reset any stale host connections from prior container lifecycles so peers reconnect fresh
                await self._cleanup_stale_connections()
            except Exception as e:
                logger.error("Failed to register BlueZ peripheral (check D-Bus mount / permissions): %s", e)

        # Start Touch Reader if evdev is explicitly enabled
        if self.config.enable_evdev and self.config.touch_device:
            asyncio.create_task(self.touch_reader.run())
        else:
            logger.info("Physical evdev touch reader disabled (browser-level touch forwarding active)")

        # Start Bluetooth keepalive worker
        if self.config.bluetooth_keepalive:
            self.keepalive_task = asyncio.create_task(self._bluetooth_keepalive_loop())

        # Start HTTP API
        middlewares = [cors_middleware] if cors_middleware else []
        app = web.Application(middlewares=middlewares)
        app.router.add_get("/healthz", self.handle_healthz)
        app.router.add_post("/tap", self.handle_tap)
        app.router.add_post("/touch", self.handle_touch)
        app.router.add_post("/key", self.handle_key)
        app.router.add_post("/remote/app", self.handle_remote_app)
        app.router.add_get("/remote/status", self.handle_remote_status)
        app.router.add_post("/bluetooth/remove", self.handle_bluetooth_remove)
        app.router.add_post("/bluetooth/reconnect", self.handle_bluetooth_reconnect)

        runner = web.AppRunner(app)
        await runner.setup()
        site = web.TCPSite(runner, "0.0.0.0", self.config.http_port)
        await site.start()
        logger.info("Mirrormere Remote HTTP API listening on http://0.0.0.0:%d", self.config.http_port)

        # Keep server running until shutdown signal
        stop_event = asyncio.Event()
        loop = asyncio.get_running_loop()
        for sig in (signal.SIGTERM, signal.SIGINT):
            try:
                loop.add_signal_handler(sig, stop_event.set)
            except NotImplementedError:
                pass

        await stop_event.wait()
        logger.info("Shutting down mirrormere-remote...")
        if self.keepalive_task:
            self.keepalive_task.cancel()
        if self.advert and self.adapter:
            try:
                await self.advert.unregister(self.bus, adapter=self.adapter)
            except Exception:
                pass
        if self.agent and self.bus:
            try:
                await self.agent.unregister(self.bus)
            except Exception:
                pass


def main():
    config_path = os.environ.get("CONFIG_PATH")
    if len(sys.argv) > 1:
        if sys.argv[1] in ("-c", "--config") and len(sys.argv) > 2:
            config_path = sys.argv[2]
        elif not sys.argv[1].startswith("-"):
            config_path = sys.argv[1]
    config = load_config(config_path)
    daemon = RemoteDaemon(config=config)
    asyncio.run(daemon.start())


if __name__ == "__main__":
    main()

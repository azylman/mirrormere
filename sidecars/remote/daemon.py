"""Unified Bluetooth HID & Android TV Remote sidecar for Mirrormere.

Manages:
1. Android TV Remote v2 (Wi-Fi mTLS) connection on port 6466.
2. Bluetooth LE HID Peripheral (Mirrormere Remote) with multi-report touch, mouse, and consumer keys.
3. Automated BLE pairing confirmation via Wi-Fi remote.
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
    0x26, 0xff, 0x7f,  #     Logical Maximum (32767)
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
        """Debounced auto-approval for Bluetooth pairing modal on Google TV."""
        await asyncio.sleep(0.3)
        logger.info("Auto-confirming Bluetooth pairing dialog via Wi-Fi remote (DPAD_RIGHT -> DPAD_CENTER)...")
        self.send_key("DPAD_RIGHT")
        await asyncio.sleep(0.15)
        self.send_key("DPAD_CENTER")

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

        def send_mouse(self, buttons: int, dx: int, dy: int):
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

    class PairingAgent(BaseAgent):
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
            if self.auto_confirm:
                asyncio.create_task(self.remote_helper.auto_approve_pairing())

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
            try:
                proc = await asyncio.create_subprocess_exec("bluetoothctl", "trust", mac)
                await proc.wait()
                logger.info("Successfully trusted device %s", mac)
            except Exception as e:
                logger.warning("Failed to auto-trust %s: %s", mac, e)

except ImportError:
    logger.warning("bluez-peripheral not installed; running in mock/test mode.")
    HIDService = None
    Advertisement = None
    PairingAgent = None


class TouchReader:
    """Reads touch events from physical evdev device non-exclusively."""

    def __init__(self, hid_service, target_path: str):
        self.hid = hid_service
        self.target_path = target_path
        self.touch_active = False
        self.cur_x = 0
        self.cur_y = 0

    def resolve_device(self) -> Optional[str]:
        if os.path.exists(self.target_path):
            return self.target_path
        # Search by-id or event devices
        matches = glob.glob("/dev/input/by-id/*touch*") or glob.glob("/dev/input/event*")
        return matches[0] if matches else None

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
                async for ev in dev.async_read_loop():
                    if ev.type == evdev.ecodes.EV_ABS:
                        if ev.code in (evdev.ecodes.ABS_MT_POSITION_X, evdev.ecodes.ABS_X):
                            self.cur_x = ev.value
                        elif ev.code in (evdev.ecodes.ABS_MT_POSITION_Y, evdev.ecodes.ABS_Y):
                            self.cur_y = ev.value
                        elif ev.code == evdev.ecodes.ABS_MT_TRACKING_ID:
                            self.touch_active = (ev.value >= 0)
                            if not self.touch_active and self.hid:
                                x_r, y_r = normalize_coordinates(self.cur_x, self.cur_y)
                                self.hid.send_touch(False, x_r, y_r)
                    elif ev.type == evdev.ecodes.EV_KEY and ev.code == evdev.ecodes.BTN_TOUCH:
                        self.touch_active = bool(ev.value)
                        if not self.touch_active and self.hid:
                            x_r, y_r = normalize_coordinates(self.cur_x, self.cur_y)
                            self.hid.send_touch(False, x_r, y_r)
                    elif ev.type == evdev.ecodes.EV_SYN and ev.code == evdev.ecodes.SYN_REPORT:
                        if self.touch_active and self.hid:
                            x_r, y_r = normalize_coordinates(self.cur_x, self.cur_y)
                            self.hid.send_touch(True, x_r, y_r)
            except Exception as e:
                logger.warning("Touch reader disconnected or errored (%s); re-opening in 3s...", e)
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
            },
        })

    async def handle_tap(self, request):
        try:
            data = await request.json()
        except Exception:
            return web.json_response({"status": "error", "message": "invalid json"}, status=400)

        x_ratio = float(data.get("x", 0.5))
        y_ratio = float(data.get("y", 0.5))
        logger.info("Synthetic tap at (%.4f, %.4f)", x_ratio, y_ratio)

        if self.hid:
            self.hid.send_touch(True, x_ratio, y_ratio)
            await asyncio.sleep(0.05)
            self.hid.send_touch(False, x_ratio, y_ratio)
            return web.json_response({"status": "ok", "action": "tap", "x": x_ratio, "y": y_ratio})
        return web.json_response({"status": "error", "message": "BLE HID not initialized"}, status=503)

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
        })

    async def handle_bluetooth_remove(self, request):
        try:
            data = await request.json()
        except Exception:
            return web.json_response({"status": "error", "message": "invalid json"}, status=400)
        mac = str(data.get("mac", "")).strip().upper()
        if not mac:
            return web.json_response({"status": "error", "message": "missing mac parameter"}, status=400)
        try:
            proc = await asyncio.create_subprocess_exec("bluetoothctl", "remove", mac)
            await proc.wait()
            logger.info("Successfully removed/unpaired bluetooth device %s", mac)
            return web.json_response({"status": "ok", "action": "remove", "mac": mac})
        except Exception as e:
            logger.warning("Failed to remove bluetooth device %s: %s", mac, e)
            return web.json_response({"status": "error", "message": str(e)}, status=500)

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

                self.advert = Advertisement(
                    self.config.advert_name,
                    ["1812", "180F", "180A"],
                    appearance=0x03C2,
                    timeout=0,
                )
                await self.advert.register(self.bus, adapter=self.adapter)
                logger.info("BLE peripheral & advertisement registered as '%s' (0x03C2)", self.config.advert_name)
            except Exception as e:
                logger.error("Failed to register BlueZ peripheral (check D-Bus mount / permissions): %s", e)

        # Start Touch Reader
        asyncio.create_task(self.touch_reader.run())

        # Start HTTP API
        app = web.Application()
        app.router.add_get("/healthz", self.handle_healthz)
        app.router.add_post("/tap", self.handle_tap)
        app.router.add_post("/key", self.handle_key)
        app.router.add_post("/remote/app", self.handle_remote_app)
        app.router.add_get("/remote/status", self.handle_remote_status)
        app.router.add_post("/bluetooth/remove", self.handle_bluetooth_remove)

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

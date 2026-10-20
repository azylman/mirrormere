# Mirrormere Remote Sidecar

The `remote` sidecar provides unified Bluetooth LE HID input (touchscreen digitizer, relative mouse pointer, consumer media keys) and Wi-Fi mTLS remote control for smart display clients such as Chromecast with Google TV or Android TV.

## Configuration

Configuration is loaded from `/local/config/remote.yaml` or `/etc/mirrormere/remote.yaml`, or specified via the `CONFIG_PATH` environment variable.

```yaml
remote:
  chromecast_host: "chromecast.local"
  cert_dir: "/data/certs"
  touch_device: "/dev/input/event3"
  http_port: 8092
  advert_name: "Mirrormere Remote"
  auto_confirm_pairing: true
  enable_evdev: false
  bluetooth_keepalive: true
  bluetooth_keepalive_interval: 10.0
  advert_min_interval: 30
  advert_max_interval: 50
  tap_duration: 0.015
  bluetooth_heartbeat: true
  bluetooth_heartbeat_interval: 30.0

  # Managed Bluetooth devices (Issue #500)
  # Only devices listed here (or paired through the PairingAgent) will be managed
  # by startup cleanup and periodic reconnection. When empty, standard peripherals
  # (audio, keyboard, mouse, gaming, tablet) are skipped to avoid disrupting host peripherals.
  managed_devices:
    - "DC:E5:5B:A6:30:8B"

  # Pointer mode: "touch" (default) or "mouse" (Issue #503)
  # In "touch" mode, synthetic taps and evdev events emit absolute HID digitizer reports.
  # In "mouse" mode, events emit standard relative HID mouse clicks.
  pointer_mode: "touch"
```

### Environment Variable Overrides

- `MIRRORMERE_REMOTE_MANAGED_DEVICES`: Comma-separated list of MAC addresses to manage (e.g. `DC:E5:5B:A6:30:8B,AA:BB:CC:DD:EE:FF`).
- `MIRRORMERE_REMOTE_POINTER_MODE`: `"touch"` or `"mouse"`.

> **Note on Changing Pointer Mode or HID Descriptors**:
> Bluetooth clients cache BLE GATT characteristics and HID report descriptors upon pairing. If you switch `pointer_mode` or update HID descriptors, client operating systems (like Android TV) may not recognize the descriptor change until the existing pairing is removed and re-established.
> Call the `POST /bluetooth/remove` API endpoint (passing `{"mac": "<DEVICE_MAC>"}`) and re-pair the remote device to refresh GATT services and descriptors.

## HTTP Endpoints

- `GET /healthz`: Service health status, Wi-Fi remote status, and paired BLE device state.
- `POST /tap`: Dispatch a synthetic tap at relative coordinates `{"x": 0.5, "y": 0.5}`.
- `POST /touch`: Dispatch a touch gesture (`{"action": "down"|"move"|"up"|"tap", "x": 0.5, "y": 0.5}`).
- `POST /key`: Dispatch a consumer or remote keypress (`{"key": "home"|"back"|"volume_up"|...}`).
- `POST /bluetooth/remove`: Unpair and remove a Bluetooth device from BlueZ and managed records (`{"mac": "..."}`).
- `POST /bluetooth/reconnect`: Trigger on-demand reconnection to paired devices (`{"mac": "..."}`).
- `POST /voice`: Stream raw audio to the active Android TV voice session.

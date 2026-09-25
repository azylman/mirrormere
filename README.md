# Mirrormere 🪞

> *"There lie the jewels of Durin, beneath the water of Kheled-zâram..."*

**Mirrormere** is an open-source, local-first smart wall display and ambient dashboard framework. Designed as a self-hosted, hackable alternative to proprietary cloud wall calendars (Skylight) and walled-garden smart displays (Google Nest Hub), Mirrormere decouples data aggregation from physical display hardware.

---

## Architectural Philosophy

Most smart display appliances are locked into proprietary cloud ecosystems, subscription paywalls, and abandonware firmware. Mirrormere is engineered around three core tenets:

1. **Decoupled Headless Core**: A shared backend runtime manages calendar sync (CalDAV/Google/iCal), weather pipelines, chore checklists, and smart home feeds (Home Assistant), exposing uniform event and state streams.
2. **Multi-Target Hardware Profiles**: The exact same backend engine powers high-refresh touch kiosks and low-power, zero-glare e-paper radiators.
3. **Pluggable & Extensible Widgets**: A modular manifest system allows developers to build universal data widgets while tailoring presentation adapters for specific screen capabilities.

---

## Hardware Targets

Mirrormere supports two distinct reference hardware profiles:

### 1. Touch Kiosk Profile (Interactive 60Hz)
- **Primary Role**: Interactive family calendar, chore management, smart home control, and video streaming.
- **Compute**: Intel N100 Mini PC (e.g. Beelink Mini S12 Pro, 16GB DDR4, 500GB NVMe SSD, ~6W idle).
- **Display**: 15.6" 1080p capacitive touchscreen monitor with rear 75×75mm VESA mount (UPERFECT).
- **Runtime Environment**: Minimal bare-metal Linux under Wayland kiosk mode (`cage` compositor) running Chromium in `--kiosk` mode.
- **Audio & Media**:
  - Nano USB microphone dongle for room-wide voice pickup behind the drywall.
  - USB 3.0 UVC HDMI capture card inline with a physical Google Chromecast for hardware-negotiated DRM casting and picture-in-picture (PIP) streaming.

### 2. Ambient E-Ink Profile (Low-Power Monochrome)
- **Primary Role**: Passive, zero-glare wall newspaper, agenda glance, and ambient status radiator.
- **Compute**: Raspberry Pi 4 Model B (4GB) with passive low-profile heatsinks.
- **Display**: Waveshare 7.5" e-Paper HAT (V2, 800×480 resolution, SPI, Black & White).
- **Runtime Environment**: Event-driven or periodic cron-based redraws writing directly to the e-paper driver, with strict zero-animation constraints and partial refresh optimization.
- **Audio (Optional)**: reSpeaker XVF3800 4-mic array or USB microphone for ambient voice satellite integration.

---

## Pluggable Widget Architecture

Widgets in Mirrormere are modular packages containing a server-side data provider and display-specific view adapters:

- **Data Provider**: Scheduled or WebSocket-driven worker responsible for fetching and caching state (e.g. Google Calendar OAuth / CalDAV sync, Open-Meteo weather, Home Assistant state streams).
- **View Adapter**:
  - `widget.html`: Unified semantic HTML layout rendered dynamically into the canvas and styled via the volume-mounted server stylesheet (`/config/custom.css` with embedded fallback). E-ink displays are rendered via an optional headless capture sidecar, eliminating dual SVG templates.

### Deployment-Specific Widget Declarations
In alignment with Mirrormere's decoupled deployment topology, each independent instance declares exactly the widgets it needs in its local `config.yaml` (`display.widgets`). Interactive-only widgets (such as Chromecast UVC capture or video streams) are simply omitted from configurations targeting static ambient displays.

---

## Repository Structure

```text
mirrormere/
├── api/                    # OpenAPI 3.1 contract and formal JSON Schemas for SSE payloads
├── cmd/
│   └── server/             # mirrormere-core entrypoint binary (main.go)
├── internal/               # Private Go core application packages (api, config, layout, providers, storage)
├── widgets/                # Core widget packages (manifest.yaml, views/widget.html, assets/)
├── web/                    # Web display client runtime (static CSS/JS HUD tokens, display.html shell)
├── sidecars/               # Custom auxiliary microservices (eink-renderer, cast-watcher)
├── clients/                # Standalone edge display clients (eink-node Python SPI daemon)
├── deploy/                 # Docker Compose manifests, go2rtc config, and kiosk launch units
├── specs/                  # Living architectural specifications (001–011, see specs/README.md)
├── scripts/                # Verification (verify.sh) and development scripts
├── Dockerfile              # Multi-stage, multi-arch build for mirrormere-core
└── Makefile                # Local build, test, and verification shortcuts
```

---

## Getting Started & Roadmap

1. **Hardware Validation**: Complete bench testing of the Intel N100 VESA touch sandwich and the Raspberry Pi 4B e-paper HAT.
2. **Core Data Daemon**: Implement the unified calendar (CalDAV/Google), weather, and task sync services.
3. **PWA Touch Kiosk**: Scaffold the Wayland kiosk frontend with touch navigation and UVC video capture overlays.
4. **E-Paper Rendering Engine**: Deploy the Python/C SPI framebuffer pipeline for the 7.5" Waveshare panel.
5. **Widget SDK**: Document the developer contract for authoring third-party widgets.

---

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.


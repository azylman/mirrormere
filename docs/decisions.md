# Architectural Decisions & Channel Consensus

This document records key architectural decisions and engineering invariants established for Mirrormere.
Normative API schemas and event payloads are governed by `api/openapi.yaml` and `api/schemas/*.json`.

## 1. Hardware Profiles & Deployment Topology
- **Profile A (Touch Kiosk)**: Intel N100 Mini PC, 15.6" 1080p capacitive touchscreen, Wayland `cage` compositor running Chromium in `--kiosk` mode, UVC HDMI capture card (Elgato Cam Link 4K primary, MS2130/MS2109 fallbacks), hardware-accelerated WebRTC via `go2rtc`.
- **Profile B (Ambient E-Ink)**: Raspberry Pi 4 Model B, Waveshare 7.5" black & white e-paper HAT (800x480), Python SPI driver, reSpeaker XVF3800 mic array.
- **Independent Local Instances**: Alex and Mike each run independent local instances on their home LANs. Zero multi-tenant runtime switching.

## 2. Display Adapters & Modality Rules
- **No Video on E-Ink**: Video streaming (`live-view`) and dynamic animation are exclusively scoped to touch displays (`touch-interactive` capability only). E-ink profiles render static monochrome frames via headless capture and Atkinson/Floyd-Steinberg dithering.
- **Appliance Cursor Suppression**: Floating cursor suppression on kiosks is controlled via `?kiosk=true` (or `.mm-touch-kiosk`), preserving standard mouse pointers on desktop browser sessions.

## 3. Configuration & Runtime Lifecycle
- **Declarative YAML & Zero Precedence**: Core and sidecars load declarative YAML configuration (`config.yaml`) as the single source of truth at boot. Command-line flags are prohibited, and legacy environment variable overrides (`HOST`, `PORT`) are removed. Secrets are strictly decoupled and injected via environment variables resolved through explicit `*_env` keys.
- **Host & Port Bindings**: Server network binding is declared via top-level `host` (default: `"0.0.0.0"`) and `port` (default: `8080`) in `config.yaml`.
- **Reload Policy & Restart Warnings**: Server configuration reload is triggered via `SIGHUP` signal (or container restart), avoiding inode detachment issues with editor file renames. Dynamic configuration (widgets, layout, weather, rotation) reloads live. Because open network sockets cannot be rebound at runtime, modifications to `host` or `port` during live reload emit a structured "restart required" warning log and require a server restart to take effect.
- **Last-Known-Good Configuration (LKGC)**: If proposed configuration changes fail validation, the running configuration remains active and untouched.

## 4. Audio & Voice Pipeline
- **Raw PCM Audio**: Edge microphone capture streams raw 16kHz 16-bit mono PCM audio over LAN HTTP/WebSockets to the voice hub.
- **Voice Ingress**: Centralized hub coordination for wake word detection, speech-to-text, and assistant deliberation.
- **Volume Ceiling**: Strict software volume caps to protect domestic audio environments.

## 5. API & Wire Contracts
- **REST & OpenAPI**: `api/openapi.yaml` is the canonical contract for all REST endpoints with automated zero-drift verification in CI.
- **Event Contracts**: SSE event shapes and payload schemas are formally defined in `api/schemas/*.json`.

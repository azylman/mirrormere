# Mirrormere Specifications

This directory contains the foundational technical specifications, architectural blueprints, and interface contracts for the Mirrormere project.

## Index of Specifications

- **[001-architecture-overview.md](001-architecture-overview.md)**: High-level system architecture, client-server topology, data pipelines, and separation of concerns.
- **[002-hardware-profiles.md](002-hardware-profiles.md)**: Hardware BOM, pinouts, physical mounting, and OS configurations for reference devices (Touch Kiosk and E-Ink Ambient).
- **[003-widget-contract.md](003-widget-contract.md)**: Specification for pluggable widgets, data provider schemas, lifecycle hooks, and dual-target view adapters.
- **[004-video-cast-ingest.md](004-video-cast-ingest.md)**: Unified Video Stream API (`POST /api/video/trigger`), display video modes, Last-In Highest-Priority stack with Picture-in-Picture handoff, kiosk video sidecars (stock `go2rtc` + `sidecars/cast-watcher`), and WebRTC audio architecture.
- **[005-screen-layout-and-rotation.md](005-screen-layout-and-rotation.md)**: Declarative 6×2 grid canvas, fixed header zone, and exact bin-packing rotation algorithm with fully-filled screen invariants.
- **[006-realtime-comms-and-mutations.md](006-realtime-comms-and-mutations.md)**: Realtime client-server communication via Server-Sent Events (SSE) state streaming paired with REST mutation endpoints (CQRS).
- **[007-core-data-providers.md](007-core-data-providers.md)**: Core data ingestion providers for multi-calendar iCal/CalDAV feeds, Open-Meteo weather forecasts, and Google Photos shared albums.
- **[008-tasks-and-lists.md](008-tasks-and-lists.md)**: Checklists, grocery lists and chores: local pure-Go SQLite store (`modernc.org/sqlite`, zero CGO), pluggable source-of-truth adapters (Google Tasks, generic HTTP), widget actions over the SPEC-006 protocol, and read-only e-ink rendering.
- **[009-eink-display-node.md](009-eink-display-node.md)**: The Python display-node client for e-paper panels: SSE-triggered coalesced refreshes, partial/full refresh lifecycle, offline retention, configurable pin maps (Waveshare HAT or Adafruit E-Ink Bonnet), and systemd packaging.
- **[010-touch-kiosk-client.md](010-touch-kiosk-client.md)**: The Touch Kiosk host provisioning environment (`deploy/kiosk`): Wayland `cage` compositor, Chromium kiosk flags, daytime idle timeout and night schedule DPMS power management, PipeWire HDMI audio routing, and zero-OSK tap-only input model.
- **[011-voice-pipeline.md](011-voice-pipeline.md)**: Unified voice pipeline: local wake word, LAN GPU STT (Jetson Orin & Desktop CUDA), autonomous agent brain (Aerial & Amos), ElevenLabs/Piper TTS with deterministic fallback, and dock speaker playback with media ducking and barge-in.

# Mirrormere Specifications

This directory contains the foundational technical specifications, architectural blueprints, and interface contracts for the Mirrormere project.

## Index of Specifications

- **[001-architecture-overview.md](001-architecture-overview.md)**: High-level system architecture, client-server topology, data pipelines, and separation of concerns.
- **[002-hardware-profiles.md](002-hardware-profiles.md)**: Hardware BOM, pinouts, physical mounting, and OS configurations for reference devices (Touch Kiosk and E-Ink Ambient).
- **[003-widget-contract.md](003-widget-contract.md)**: Specification for pluggable widgets, data provider schemas, lifecycle hooks, and dual-target view adapters.
- **[004-video-cast-ingest.md](004-video-cast-ingest.md)**: Pipeline for ingesting physical Google Cast video via USB 3.0 UVC capture into HTML5, including picture-in-picture and signal detection.
- **[005-screen-layout-and-rotation.md](005-screen-layout-and-rotation.md)**: Declarative 6×2 grid canvas, fixed header zone, and exact bin-packing rotation algorithm with fully-filled screen invariants.
- **[006-realtime-comms-and-mutations.md](006-realtime-comms-and-mutations.md)**: Realtime client-server communication via Server-Sent Events (SSE) state streaming paired with REST mutation endpoints (CQRS).
- **[007-core-data-providers.md](007-core-data-providers.md)**: Core data ingestion providers for multi-calendar iCal/CalDAV feeds and Open-Meteo weather forecasts.

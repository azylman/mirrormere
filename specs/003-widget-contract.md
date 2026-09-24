# SPEC-003: Pluggable Widget Contract & Extension Model

## Status
Draft / Architecture Defined

## Overview
Mirrormere uses a pluggable, declarative widget model. A widget is a self-contained unit responsible for data ingestion and presentation. To support both 60Hz capacitive touchscreens and low-refresh e-paper panels, widgets decouple the data pipeline from the physical display adapter.

---

## Directory Structure

A widget package lives in `widgets/<widget-id>/` (for standard core widgets) or `custom_widgets/<widget-id>/` (for user-specific extensions):

```
widgets/calendar-agenda/
├── manifest.yaml           # Widget metadata, configuration schema, and capabilities
├── provider.py (or .go)    # Backend data fetcher and event publisher
├── views/
│   ├── touch.html          # Interactive Web Component for 60Hz touch displays
│   └── eink.svg            # Static high-contrast SVG template for 800×480 e-paper
└── assets/                 # Optional static icons or style sheets
```

---

## Manifest Schema (`manifest.yaml`)

Each widget declares its identity, required capabilities, and configuration parameters:

```yaml
id: calendar-agenda
name: Family Calendar Agenda
version: "1.0.0"
description: Multi-calendar agenda view supporting Google Calendar, CalDAV, and iCal feeds.

capabilities:
  - ambient-static
  - touch-interactive

refresh:
  interval_seconds: 300
  push_triggers:
    - calendar_event_changed
    - midnight_rollover

config_schema:
  calendars:
    type: list
    required: true
    items:
      name: string
      url: string
      color: string
  show_relative_time:
    type: boolean
    default: true
```

### Capability Tags
- `ambient-static`: Supports rendering to static monochrome or grayscale displays (e-paper).
- `touch-interactive`: Supports full touch interaction, tapping to view event details, gestures, and animations.
- `video-capture`: Involves live video streaming or UVC ingestion (automatically ignored by e-paper targets).
- `audio-reactive`: Utilizes microphone input or voice triggers.

---

## The Data Provider Contract

The backend data provider runs in the Mirrormere Core service:
1. **Lifecycle**:
   - `Init(config)`: Validates credentials, sets up sync loops or WebSocket listeners.
   - `Fetch()`: Returns an atomic JSON payload representing current state.
   - `Subscribe(eventSink)`: Emits real-time event updates when data changes.
   - `Shutdown()`: Cleans up background listeners and connections.

2. **Standard Payload Envelope**:
   ```json
   {
     "widget_id": "calendar-agenda",
     "timestamp": "2026-09-24T18:00:00Z",
     "state": "healthy",
     "data": {
       "events": [
         {
           "id": "evt_101",
           "title": "Soccer Practice",
           "start": "2026-09-24T18:30:00Z",
           "end": "2026-09-24T19:30:00Z",
           "calendar": "Family",
           "color": "#3b82f6"
         }
       ]
     }
   }
   ```

---

## Dual View Adapters

### 1. Touch Interactive View (`views/touch.html`)
- Delivered as a modern Web Component or HTML5 snippet loaded by the Chromium kiosk PWA.
- Fully supports CSS variables for theming, touch drag-and-drop, tap-to-complete chores, and fluid layout resizing.
- Receives state updates reactively via WebSocket.

### 2. Ambient E-Ink View (`views/eink.svg` or Python renderer)
- Rendered on the Raspberry Pi into a high-contrast 1-bit or 4-bit grayscale buffer.
- Strict design rules:
  - Zero grayscale anti-aliasing on text edges for crisp 1-bit rendering.
  - Solid geometric shapes, high-contrast borders (2px minimum), and heavy font weights (e.g. Inter Bold, Roboto Black).
  - No CSS transitions or keyframe animations.
  - Layout sized explicitly to 800×480 pixels.

---

## Core vs. Private Extensions

Mirrormere enforces a strict separation between core public widgets and private user extensions:

1. **Standard Core Library (`widgets/`)**:
   - 100% generic, reusable, and free of personal identifiers or proprietary hardware dependencies.
   - Includes: Google Calendar / CalDAV, Open-Meteo Weather, Daily Chores & Todo, Clock & World Time, Family Photo Carousel.

2. **Private User Extensions (`custom_widgets/`)**:
   - User-defined integrations that reference personal home configurations or specific hardware peripherals.
   - Includes: Home Assistant entity dashboards, live RTSP doorbell camera feeds, and Chromecast UVC ingest pipelines.

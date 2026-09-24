# SPEC-007: Core Ingestion Providers (iCal Feeds & Open-Meteo Weather)

## Status
Approved / Core Ingestion Contract

## Context & Motivation
Mirrormere decouples physical display clients from data ingestion. To avoid third-party cloud credential friction (such as Google Cloud Console project registration, OAuth consent screens, and refresh token expiration) on Day 1, Mirrormere standardizes its foundational data ingestion on **two zero-credential, local-first protocols**:
1. **Multi-Calendar iCal (.ics) / CalDAV feeds**: Stateless polling of private calendar URLs.
2. **Open-Meteo Forecast Engine**: Public, keyless, rate-limit-free meteorological API.

Both providers run as background worker loops within the Go daemon, normalizing external payloads into typed structs published via the Server-Sent Events (SSE) bus (`GET /api/events`).

---

## 1. Calendar Ingestion Provider (`providers/calendar`)

```mermaid
flowchart LR
    subgraph Upstream [External Calendar Feeds]
        GCal[Google Calendar Private .ics]
        Apple[iCloud Calendar URL]
        CalDAV[Fastmail / Nextcloud CalDAV]
    end

    subgraph GoDaemon [Mirrormere Go Daemon]
        Poller[iCal Fetcher & Sync Loop\nDefault: 300s Cadence]
        Parser[RFC 5545 iCal Parser & RRULE Evaluator]
        Normalizer[Window Filter: Today -1d to +14d]
        SSEHub[SSE Event Hub]
    end

    GCal -->|HTTPS GET .ics| Poller
    Apple -->|HTTPS GET .ics| Poller
    CalDAV -->|HTTPS GET / PROPFIND| Poller

    Poller --> Parser
    Parser --> Normalizer
    Normalizer -->|widget.update: calendar-agenda| SSEHub
```

### Configuration Schema (`config.yaml`)

```yaml
providers:
  calendar:
    refresh_interval_seconds: 300 # 5 minutes
    window_days_past: 1
    window_days_future: 14
    sources:
      - name: "Family Calendar"
        url: "https://calendar.google.com/calendar/ical/example%40group.calendar.google.com/private-token/basic.ics"
        color: "#3b82f6" # Blue
        enabled: true
      - name: "Personal / Work"
        url: "https://p123-caldav.icloud.com/..."
        color: "#10b981" # Green
        enabled: true
```

### Normalized Internal Schema (`CalendarEvent`)

The parser evaluates RFC 5545 VEVENT components, expands recurrence rules (RRULE), and produces normalized JSON emitted on `widget.update`:

```json
{
  "widget_id": "calendar-agenda",
  "timestamp": "2026-09-24T22:20:00Z",
  "data": {
    "last_sync": "2026-09-24T22:20:00Z",
    "sync_status": "ok",
    "events": [
      {
        "id": "evt_20260924_1830_soccer",
        "calendar_name": "Family Calendar",
        "color": "#3b82f6",
        "title": "Soccer Practice",
        "start": "2026-09-24T18:30:00-07:00",
        "end": "2026-09-24T19:30:00-07:00",
        "all_day": false,
        "location": "Community Park Field 2",
        "description": "Bring shin guards and water bottle"
      },
      {
        "id": "evt_20260925_allday_pge",
        "calendar_name": "Personal / Work",
        "color": "#10b981",
        "title": "Trash & Recycling Pickup",
        "start": "2026-09-25",
        "end": "2026-09-25",
        "all_day": true,
        "location": "",
        "description": ""
      }
    ]
  }
}
```

### Advantages Over OAuth 2.0
- **Zero App Registration**: Requires no GCP project, verification audit, or client secret rotation.
- **Universal Interoperability**: Every major calendar system (Google, Apple, Microsoft Outlook 365, Fastmail, Nextcloud) exports a secret private iCal URL natively.
- **Hermetic & Airgapped Testing**: Tests can feed static `.ics` fixtures directly without mocking OAuth token refreshes.

---

## 2. Weather Ingestion Provider (`providers/weather`)

```mermaid
flowchart LR
    OpenMeteo[Open-Meteo Public API\napi.open-meteo.com] -->|HTTPS GET JSON| Poller[Weather Fetcher Loop\nDefault: 900s Cadence]

    subgraph GoDaemon [Mirrormere Go Daemon]
        Poller --> WMO[WMO Weather Code Mapper]
        WMO --> Snap[Weather Snapshot Normalizer]
        Snap -->|widget.update: weather-forecast| SSEHub[SSE Event Hub]
        Snap -->|Header Weather Badge Snapshot| Header[Fixed Header Zone]
    end
```

### Configuration Schema (`config.yaml`)

```yaml
providers:
  weather:
    refresh_interval_seconds: 900 # 15 minutes
    latitude: 37.8044
    longitude: -122.2712
    timezone: "America/Los_Angeles" # "auto" or Olson TZ string
    units: "imperial" # "imperial" (F, mph, in) or "metric" (C, km/h, mm)
```

### Upstream Endpoint
Requests are routed directly to Open-Meteo's standard forecast endpoint:
```
https://api.open-meteo.com/v1/forecast?latitude=37.8044&longitude=-122.2712&current=temperature_2m,relative_humidity_2m,apparent_temperature,precipitation,weather_code,wind_speed_10m&hourly=temperature_2m,precipitation_probability,weather_code&daily=weather_code,temperature_2m_max,temperature_2m_min,precipitation_probability_max&temperature_unit=fahrenheit&wind_speed_unit=mph&precipitation_unit=inch&timezone=auto
```

### Normalized Internal Schema (`WeatherSnapshot`)

Weather codes conform to the World Meteorological Organization (WMO 4501) standard. The Go provider maps numeric codes to semantic strings and vector icon tokens:

```json
{
  "widget_id": "weather-forecast",
  "timestamp": "2026-09-24T22:20:00Z",
  "data": {
    "current": {
      "temperature": 68.4,
      "feels_like": 67.8,
      "humidity": 58,
      "wind_speed": 7.2,
      "units": {
        "temperature": "°F",
        "wind_speed": "mph"
      },
      "condition_code": 1,
      "condition_text": "Mainly Clear",
      "icon": "weather-partly-cloudy"
    },
    "hourly": [
      { "time": "2026-09-24T23:00:00-07:00", "temp": 66.2, "precip_prob": 0, "icon": "weather-partly-cloudy" },
      { "time": "2026-09-25T00:00:00-07:00", "temp": 64.0, "precip_prob": 5, "icon": "weather-cloudy" }
    ],
    "daily": [
      {
        "date": "2026-09-25",
        "temp_max": 72.5,
        "temp_min": 55.0,
        "precip_prob_max": 10,
        "condition_text": "Partly Cloudy",
        "icon": "weather-partly-cloudy"
      }
    ]
  }
}
```

### Dual-Use Target Routing
1. **Grid Widget (`widgets/weather-forecast`)**: Renders full 24-hour hourly timeline and 7-day extended forecasts on the 6×2 grid canvas (`[2, 1]` or `[3, 2]` blocks).
2. **Fixed Header Zone**: Automatically extracts `current.temperature` and `current.icon` into the persistent top banner across all screens without incurring extra API requests.

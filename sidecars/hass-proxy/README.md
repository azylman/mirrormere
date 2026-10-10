# Home Assistant Fast-Path Proxy (`hass-proxy`)

A dedicated Go sidecar microservice implementing the **LAMMAS deterministic voice fast-path contract** (`POST /intent`), adapting voice intents directly to Home Assistant's conversation processing API (`/api/conversation/process`).

## Overview

In ambient voice architectures, common spoken commands (e.g. toggling lights, media controls, temperature adjustments) can be matched deterministically by Home Assistant's local intent engine in under 50ms rather than routing through the full LLM brain loop.

This sidecar acts as a high-performance adapter between the Mirrormere voice hub (or Pi edge ear) and Home Assistant:
- Accepts inbound requests on `POST /intent` conforming to the LAMMAS contract.
- Maps inbound payloads to Home Assistant's `/api/conversation/process` endpoint.
- Evaluates Home Assistant responses:
  - If Home Assistant returns an error or `no_intent_match`, returns **HTTP 204 No Content** (prompting the hub to seamlessly fall back to the brain).
  - If Home Assistant executes the intent, returns **HTTP 200 OK** streaming Server-Sent Events (`turn`, `sentence`, `reply`, `done`).
- Exposes `GET /healthz` returning HTTP 200 for Docker, Compose, and Nomad health probes.

## Configuration

All configuration is declaratively defined in YAML files. Environment variables are strictly reserved for secret tokens:

```yaml
server:
  host: "0.0.0.0"
  port: 8095
  read_timeout_seconds: 10
  write_timeout_seconds: 10

homeassistant:
  url: "http://homeassistant.local:8123"
  token: ""             # If empty, automatically extracted from HASS_TOKEN or HOMEASSISTANT_TOKEN
  agent_id: ""          # Optional conversation agent ID
  language: "en"        # Language code
  timeout_ms: 2000      # Outbound timeout to Home Assistant
  conversation_path: "/api/conversation/process"
```

### CLI Flags

- `-config <path>`: Path to YAML configuration file (default candidates: `/config/config.yaml`, `/config/hass-proxy.yaml`, `config.yaml`).

## API Contract

### `POST /intent`

**Headers:**
- `Content-Type: application/json`
- `Accept: text/event-stream` (optional; proxy streams SSE on match regardless of Accept header)

**Request Body:**
```json
{
  "text": "turn off kitchen lights",
  "speaker": "alex",
  "node": "kitchen-kiosk",
  "turn_id": "turn-123",
  "tts": "none"
}
```

**Responses:**
- `204 No Content`: No intent matched in Home Assistant or upstream error (caller falls back to brain).
- `200 OK (text/event-stream)`:
  ```http
  event: turn
  data: {"turn_id":"turn-123"}

  event: sentence
  data: {"text":"Turned off kitchen lights"}

  event: reply
  data: {"reply":"Turned off kitchen lights","tts_engine":"none"}

  event: done
  data: {"turn_id":"turn-123"}
  ```

### `GET /healthz`

Returns HTTP 200 `OK\n`.

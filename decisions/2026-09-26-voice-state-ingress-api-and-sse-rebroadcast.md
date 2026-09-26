# Architecture Decision Record: Voice State Ingress API and Real-Time Event Rebroadcast

- **Date:** 2026-09-26
- **Status:** Accepted
- **Context:** Implementing Issue #248 on `azylman/mirrormere` (SPEC-013 Phase 6, Chunk 6.1), fulfilling SPEC-006 §2.G, §6 and SPEC-011 §2-§4.

---

## 1. Problem Statement
1. **Edge-to-Display Voice Feedback Disconnect**: The voice pipeline (SPEC-011) decouples edge microphone capture from Voice Hub orchestration (STT, LLM brain, and TTS). The edge dock client (`mirrormere-voice`) requires an authoritative ingress endpoint (`POST /api/voice/state`) on Mirrormere Core to relay interaction lifecycle states (`listening`, `transcribing`, `thinking`, `speaking`, `idle`, `error`) so that display HUDs can update visual indicators, header transcripts, and caption toasts in real time.
2. **Schema & Contract Standardization**: `voice.state` required an authoritative Draft 2020-12 JSON Schema (`api/schemas/voice.state.json`) and OpenAPI 3.1 REST route (`POST /api/voice/state`) with compile-time code generation via `oapi-codegen` to prevent wire drift across services.
3. **State Hydration Synchronization**: Connecting and reconnecting clients (`GET /api/events`) require immediate initial state hydration (`evt_init_06`) with the active voice state so that microphone/thinking indicators and status badges reflect current state on boot without waiting for the next turn.

---

## 2. Decision & Architecture

### A. Authoritative Voice State Coordinator (`internal/voice/coordinator.go`)
- **Thread-Safe In-Memory State**: `Coordinator` tracks `state` (string, default: `"idle"`), `transcript` (*string, default: nil), `reply` (*string, default: nil), and `tts_engine` (*string, default: nil) protected by `sync.RWMutex`.
- **Strict State Validation**: Enforces exact lifecycle states (`idle`, `listening`, `transcribing`, `thinking`, `synthesizing`, `speaking`, `error`) with `ErrInvalidState` returning 400 Bad Request if unapproved states are supplied.
- **Lock Inversion Prevention & Event Dispatch**: Mutates state under lock, releases lock, and then dispatches `voice.state` SSE events across `internal/events/Hub` and synchronizes with the `VoiceStateSink` (`SetVoiceState(*events.VoiceStateData)`).
- **Initial Connection Hydration**: `InMemoryStateProvider` acts as the `VoiceStateSink`. Hub's `Publish` method was updated to synchronize `EventVoiceState` into `InMemoryStateProvider`, ensuring cold-booting clients receive accurate hydrated state during initial connection handshakes (`evt_init_06`).

### B. OpenAPI 3.1 & Schema Contracts (`api/openapi.yaml`, `api/schemas/voice.state.json`)
- **JSON Schema (`api/schemas/voice.state.json`)**: Draft 2020-12 schema requiring `state` enum and declaring nullable/optional `transcript`, `reply`, and `tts_engine` fields with `additionalProperties: false`.
- **OpenAPI Endpoint (`POST /api/voice/state`)**: Added REST endpoint with `VoiceStateRequest` and `VoiceStateResponse` schemas. Regenerated Go types and server interfaces via `oapi-codegen` into `internal/api/api.gen.go` with zero drift.

### C. Server Integration & HTTP Routing (`internal/server/voice.go`, `internal/server/server.go`)
- **`VoiceHandler` Interface**: Defined `PostVoiceState(w http.ResponseWriter, r *http.Request)` on `server.VoiceHandler` and wired it into `server.Config` and `server.Server`.
- **`DefaultVoiceHandler` Implementation**: Implemented with CORS preflight (`OPTIONS` -> 204 No Content), method validation (`POST` enforced with `Allow: POST, OPTIONS`), typed JSON decoding, and standard `ErrorResponse` formatting.
- **Dynamic Registration**: Exposed `RegisterVoiceHandler` and `VoiceHandler()` on `Server` for dynamic runtime handler injection.

---

## 3. Verification & Compliance
- **Hermetic Testing**:
  - `internal/voice`: 100.0% statement coverage.
  - `internal/server`: 97.1% statement coverage.
  - `internal/events`: 95.9% statement coverage.
  - `internal/api`: 95.1% statement coverage.
- **Pre-Flight Verification**: Passed `./scripts/verify.sh --staged` with zero lint errors and statement coverage floor maintained.

# ADR: Video Trigger Default Priority Alignment with SPEC-004

## Context & Problem Statement
In SPEC-004 §1 ("Trigger Video Stream (`POST /api/video/trigger`)"), the specification defines stream priority as:
```text
- priority (string, default "persistent"):
  - "persistent": Top tier. Media streaming that remains active until explicitly stopped or disconnected (e.g. Chromecast). Retains primary fullscreen and audio precedence.
  - "temporary": Alert tier. Automatically dismisses after timeout_seconds (e.g. doorbell ring, motion camera).
```

However, in PR #271 (Chunk 4.2), the OpenAPI schema (`api/openapi.yaml`), server handler (`internal/server/video.go`), and coordinator (`internal/video/coordinator.go`) incorrectly defaulted omitted priority to `temporary`. Consequently, any stream producer (or sidecar) omitting `priority` in `POST /api/video/trigger` received a stream that was demoted to the alert tier and auto-dismissed after 45 seconds (Issue #273).

## Decision
1. **Server Handler (`internal/server/video.go`)**:
   - Default `vPriority` to `video.PriorityPersistent` when `req.Priority` is nil or empty.
2. **Video Coordinator (`internal/video/coordinator.go`)**:
   - Default `stream.Priority` to `PriorityPersistent` when `stream.Priority` is empty in `Trigger()`.
3. **OpenAPI 3.1 Contract (`api/openapi.yaml`)**:
   - Updated `VideoStream.priority` and `VideoTriggerRequest.priority` schema properties from `default: temporary` to `default: persistent`.
4. **Verification**:
   - Added unit test cases in `internal/server/video_test.go` and `internal/video/coordinator_test.go` verifying that omitted priority requests default to persistent without auto-dismiss timers.

## Consequences & Alternatives Considered
- **Leaving `temporary` as default**: Rejected because long-running video streams (e.g. Chromecast or UVC feeds) omitting priority would terminate after 45 seconds, breaking ambient display expectations. Alert streams (doorbells, security cams) explicitly know they are ephemeral alerts and specify `priority: "temporary"`.

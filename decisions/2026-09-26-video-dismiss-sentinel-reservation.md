# ADR: Video Dismiss Sentinel Reservation and OpenAPI Contract Symmetry

## Context & Problem Statement
In SPEC-004 §2 ("Dismiss Video Stream (`POST /api/video/dismiss`)") and `internal/video/coordinator.go` (`Dismiss`), the values `"all"` and `"*"` are treated as global sentinels to dismiss all active streams and unmount video mode immediately, returning to `widgets` mode.

However, two contract defects existed (Issue #304):
1. **Sentinels not reserved at trigger**: `Trigger` (`internal/video/coordinator.go`) and `PostVideoTrigger` (`internal/server/video.go`) accepted any non-empty string, including `"all"` and `"*"`. If a stream producer triggered a stream with ID `"all"`, subsequent calls to `POST /api/video/dismiss {"id": "all"}` tore down both primary and PiP streams rather than just that stream.
2. **Contract disagreement**: `api/openapi.yaml` (and generated `api.gen.go`) documented only `"all"` in the description for `/api/video/dismiss` and `VideoDismissRequest.id`, omitting `"*"`, while `VideoTriggerRequest.id` made no mention of the reserved status of `"all"` or `"*"`.

## Decision
1. **Reserve `"all"` and `"*"` at Trigger**:
   - In `internal/video/coordinator.go` (`Trigger`), reject streams where `stream.ID == "all" || stream.ID == "*"` with `ErrInvalidStream`.
   - In `internal/server/video.go` (`PostVideoTrigger`), reject requests where `req.Id == "all" || req.Id == "*"` with HTTP 400 (`invalid video trigger request: 'all' and '*' are reserved sentinels`).
2. **Contract Symmetry in OpenAPI 3.1 & Generated Code**:
   - Update `api/openapi.yaml` endpoint description for `/api/video/dismiss` and schema description for `VideoDismissRequest.id` to explicitly document both `"all"` and `"*"`.
   - Update `VideoTriggerRequest.id` schema description to note that `"all"` and `"*"` are reserved sentinels.
   - Regenerate Go bindings via `go generate ./...` (`internal/api/api.gen.go`).
3. **Specification Alignment (SPEC-004)**:
   - Updated `specs/004-video-cast-ingest.md` §1 to specify that `"all"` and `"*"` are reserved sentinels rejected with HTTP 400.
4. **Verification**:
   - Added unit test cases to `internal/video/coordinator_test.go` and `internal/server/video_test.go` verifying that `Trigger` and `POST /api/video/trigger` reject `"all"` and `"*"`, and `POST /api/video/dismiss` supports both `"all"` and `"*"`.

## Consequences & Alternatives Considered
- **Dropping `"*"` entirely**: Considered restricting the sentinel strictly to `"all"`. Rejected because wildcard syntax `"*"` is established convention across companion scripts and SPEC-004 §2. Reserving both sentinels at trigger ensures complete symmetry and prevents stream collision without breaking existing callers.

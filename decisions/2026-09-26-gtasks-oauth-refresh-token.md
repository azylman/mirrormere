# Architecture Decision Record: Google Tasks OAuth 2.0 Refresh Token Ingestion

- **Date:** 2026-09-26
- **Status:** Accepted
- **Context:** Resolving Issue #267 on `azylman/mirrormere`: `fix(tasks): gtasks adapter uses a static OAuth access token with no refresh, so it fails after about an hour`.

---

## 1. Problem Statement
The Google Tasks API requires OAuth 2.0 bearer authorization. Access tokens issued by Google expire after 3,600 seconds (1 hour).
Previously, the `gtasks` adapter accepted a static `token` read once during `Init` from config or environment variables. Approximately one hour after daemon startup:
1. Every subsequent polling cycle received HTTP 401 Unauthorized from `https://tasks.googleapis.com`.
2. `doGet` returned `ErrUnauthorized`.
3. The coordinator marked the widget degraded, requiring manual token generation and container restart.
4. Unattended ambient display deployments failed silently after the initial hour.

---

## 2. Decision & Architecture

### A. OAuth 2.0 Token Refresh Exchange (`internal/tasks/adapters/gtasks.go`)
- **Credential Configuration**:
  `GTasksAdapterConfig` accepts:
  - `ClientID`: Google Cloud OAuth Client ID (or via `client_id_env` in provider).
  - `ClientSecret`: Google Cloud OAuth Client Secret (or via `client_secret_env` in provider).
  - `RefreshToken`: Long-lived OAuth 2.0 Refresh Token (or via `refresh_token_env` in provider).
  - `TokenURL`: Token exchange endpoint (defaults to `https://oauth2.googleapis.com/token`; configurable for hermetic test mocking).
  - `Token`: Optional static access token preserved as a test-only fallback.
- **In-Memory Token Cache**:
  - Protected by `sync.Mutex` (`tokenMu`).
  - Mints access tokens by POSTing form-encoded `grant_type=refresh_token&client_id=...&client_secret=...&refresh_token=...` to `tokenURL`.
  - Parses `{ "access_token": "...", "expires_in": 3600 }` and records `tokenExpiry`.
  - Reuses cached token if `time.Now().Add(60*time.Second).Before(tokenExpiry)`, avoiding superfluous token exchange round trips on recurring 120s polling cycles.

### B. Transparent 401 Invalidation & Single Retry
- If an API request to `tasks.googleapis.com` returns HTTP 401 Unauthorized:
  - The adapter forces a token refresh via `getAccessToken(ctx, forceRefresh=true)`.
  - If a fresh access token is successfully minted, the HTTP request is retried once.
  - If the retry also receives 401 (e.g. revoked refresh token), it returns `ErrUnauthorized`.

### C. Provider & Widget Manifest Schema Alignment
- `internal/provider/tasks.go`: Added resolution of `client_id`, `client_secret`, and `refresh_token` from widget instance config, `opts.Secrets`, or environment variables (`*_env`).
- `widgets/tasks/manifest.yaml`: Declared `client_id`, `client_id_env`, `client_secret`, `client_secret_env`, `refresh_token`, `refresh_token_env`, and `token_url` in the `gtasks` config schema.
- `specs/008-tasks-and-lists.md`: Documented OAuth 2.0 refresh token configuration and updated the sample `config.yaml` snippet.

---

## 3. Verification & Compliance
- **Hermetic Testing (`gtasks_test.go`, `tasks_test.go`)**:
  - `TestGTasksAdapter_OAuthRefresh_SuccessAndCaching`: Validates refresh token exchange and verifies that subsequent fetches within the token expiry window reuse the cached token without extra requests.
  - `TestGTasksAdapter_OAuthRefresh_401Retry`: Validates that a 401 on an expired/revoked token forces refresh and retries successfully.
  - `TestGTasksAdapter_OAuthRefresh_Errors`: Validates error handling for invalid token URLs, failed refresh responses, bad JSON, and empty access tokens.
  - `TestTasksProvider_Init_GTasks_OAuthSecretResolution`: Validates credential extraction from secrets vault and environment variable mappings.
- **Coverage**:
  - `internal/tasks/adapters`: 95.7% statement coverage.
  - `internal/provider`: 95.6% statement coverage.

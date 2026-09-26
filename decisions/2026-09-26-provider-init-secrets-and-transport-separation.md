# Architecture Decision Record: Provider Initialization Secrets and Transport Isolation

- **Date:** 2026-09-26
- **Status:** Accepted
- **Context:** Resolving Issue #217 on `azylman/mirrormere`, aligning `internal/provider` with SPEC-003 §1, §3, and §3.2.

---

## 1. Problem Statement
In commit `03b3422` (Phase 3 Chunk 3.1, PR #215), `Provider.Init` received a merged configuration map created by `buildProviderConfig`:
```go
func (c *ProviderCoordinator) buildProviderConfig(w *config.WidgetConfig) map[string]any {
    cfgMap := make(map[string]any)
    for k, v := range w.Config { cfgMap[k] = v }
    cfgMap["endpoint"] = w.Endpoint
    cfgMap["method"] = w.Method
    cfgMap["token"] = w.Token
    cfgMap["token_env"] = w.TokenEnv
    return cfgMap
}
```

This produced three critical defects:
1. **Omission of Resolved `*_env` Secrets**: `WidgetConfig.Secrets` (populated by `config.Config.ResolveEnv` for any `*_env` keys declared in `w.Config`, such as `api_key_env: WEATHER_KEY`) was never passed to the provider. Because direct environment variable reads are prohibited per system invariants, providers had no access to resolved credentials.
2. **Pollution of Domain Configuration**: SPEC-003 §3.2 mandates that Mirrormere serializes ONLY the nested domain `config:` object when dispatching outbound HTTP polling requests to custom sidecars, and never framework transport settings or bearer tokens. Passing a merged map forced downstream providers to hardcode filter denylists to avoid leaking tokens across the network.
3. **Collisions with Domain Keys**: Any domain configuration key legitimately named `endpoint`, `method`, or `token` was silently overwritten by the transport values.

### Authoritative Specifications
- **SPEC-003 §3 (Lifecycle Binding & Background Ingestion in Go)**: "When initializing each widget instance on server boot, the engine passes `w.Config` into `Init(ctx, w.Config)`."
- **SPEC-003 §3.2 (Ingestion Polling Wire Contract)**: "Mirrormere serializes ONLY the custom domain configuration (the exact object declared under the instance's nested `config:` mapping). Top-level framework settings (`id`, `type`, `dimensions`, `pinned`, `refresh_interval_seconds`) and transport keys (`endpoint`, `method`, `token_env`) are never included in the body."

---

## 2. Decision & Architecture

### A. Separation of Domain Config and Transport Options (`InitOptions`)
1. In `internal/provider/provider.go`, defined the `InitOptions` struct:
   ```go
   type InitOptions struct {
       ID         string
       Type       string
       Dimensions []int
       Endpoint   string
       Method     string
       Token      string
       Secrets    map[string]string
   }
   ```
2. Added `GetSecret(key string) string` helper to retrieve secrets safely from `Secrets`.
3. Updated the `Provider` interface:
   ```go
   type Provider interface {
       Init(ctx context.Context, config map[string]any, opts InitOptions) error
       Fetch(ctx context.Context) (any, error)
       Subscribe(ctx context.Context, eventSink chan<- WidgetPayload) error
       Shutdown(ctx context.Context) error
   }
   ```

### B. Coordinator Lifecycle Binding (`internal/provider/coordinator.go`)
1. In `buildProviderConfig(w)`, returned a defensive copy of `w.Config` without injecting any transport keys or tokens. Domain keys named `endpoint`, `method`, or `token` are preserved intact.
2. Implemented `buildInitOptions(w)` extracting `ID`, `Type`, `Dimensions`, `Endpoint`, `Method` (defaulting to `"POST"` if endpoint is set), `Token`, and `Secrets` (ensuring `Secrets["token"]` is populated).
3. In `startSingleWorkerLocked`, invoked `p.Init(initCtx, cfgMap, opts)`.

### C. Testing & Verification
1. In `internal/provider/coordinator_test.go`:
   - Updated `controllableMockProvider` to record `initCfg` and `initOpts`.
   - Enhanced `mockBroadcaster` with `waitForPublish` to eliminate test scheduling races without arbitrary sleeps.
   - Added `TestProviderCoordinator_DomainConfigAndTransportSecretsSeparation` verifying that `config` contains only domain keys, preserves `endpoint` domain override, omits transport keys, and provides all resolved `*_env` secrets and tokens via `InitOptions`.
   - Added `TestProviderCoordinator_InitOptionsFallbacks` testing default POST method and token synchronization.
2. In `internal/provider/provider_test.go`:
   - Updated `dummyProvider` and added `TestInitOptions_GetSecret`.
3. Monorepo statement test coverage remains at **97.02%** overall with `internal/provider` at **96.56%** (> 95.0% floor).

---

## 3. Consequences
- Providers receive pristine domain configuration mappings without secret contamination or key collisions.
- Resolved secrets from `*_env` keys are accessible via `opts.Secrets` and `opts.GetSecret`.
- Chunk 3.2 (Generic HTTP Provider) can directly serialize the `config` argument into outbound request bodies without leaking credentials.

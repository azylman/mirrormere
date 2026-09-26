# Architecture Decision Record: SanitizeConfig Domain Keys Preservation

- **Date:** 2026-09-25
- **Status:** Accepted
- **Context:** Resolving Issue #212, aligning `internal/render` with SPEC-003 §Server-Side Template Execution Context and Issue #176 secret isolation.

---

## 1. Problem Statement
In `internal/render/context.go`, `SanitizeConfig` filtered widget instance configuration using a substring denylist (`strings.Contains`) for patterns like `pass`, `auth`, `token`, `secret`, `password`, `api_key`, `apikey`, and `_env`.

This substring matching erroneously stripped legitimate domain configuration keys from `.Config` before template execution:
- `author` and `authors` were stripped because of `"auth"`.
- `show_passed`, `passengers`, `bypass_cache`, and `compass` were stripped because of `"pass"`.
- `max_tokens` was stripped because of `"token"`.

Per SPEC-003 §Server-Side Template Execution Context:
- `.Config` is defined as "the instance's custom configuration mapping declared under `config:` in `config.yaml`".
- Since Issue #176 (PR #179), resolved secrets live in `WidgetConfig.Secrets` and are isolated at configuration load time. Secrets never enter `WidgetConfig.Config`. The only credential-related fields remaining in `Config` are environment variable name pointers (`*_env`).

---

## 2. Decision & Architecture

### A. Exact Key Matching & `_env` Suffix Pruning
Updated `isSensitiveKey` in `internal/render/context.go`:
1. **Environment Pointer Suffix**: Keys ending in `_env` (or equal to `_env`), such as `token_env` or `api_key_env`, are pruned to prevent leaking environment variable names into client-facing markups.
2. **Exact Credential Match**: Replaced `strings.Contains` with exact key lookup (`token`, `secret`, `password`, `api_key`, `apikey`, `auth`, `auth_token`, `access_token`).
3. **Preservation of Domain Keys**: Ordinary domain keys containing substrings of credential words (e.g. `author`, `show_passed`, `bypass_cache`, `compass`, `passengers`, `max_tokens`) are preserved and passed to templates without alteration.

### B. Deep Immutability
`SanitizeConfig` continues to perform a recursive deep clone of maps and slices to ensure that template execution or helper mutations cannot mutate `WidgetConfig.Config` in memory.

### C. Testing & Verification
- Updated `TestSanitizeConfig` in `internal/render/engine_test.go` to explicitly verify that `author`, `authors`, `show_passed`, `passengers`, `bypass_cache`, `compass`, and `max_tokens` are retained, while exact credentials and `*_env` keys are stripped across root, nested maps, and slices.
- Added `TestEngine_RenderWidget_DomainConfigKeysReachTemplate` verifying that `author`, `show_passed`, `bypass_cache`, and `max_tokens` cleanly reach rendered HTML.
- Monorepo statement test coverage remains well above the 95.0% floor at **97.09%**.

---

## 3. Consequences
- Widget authors can freely declare standard domain keys (`author`, `show_passed`, `bypass_cache`, etc.) in `config.yaml` and reference them via `{{ .Config.<key> }}` without silent omission.
- Strict defense-in-depth against credential leaks is preserved for exact credential keys and `*_env` environment variable pointers.

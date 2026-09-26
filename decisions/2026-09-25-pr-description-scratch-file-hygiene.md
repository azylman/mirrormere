# Architecture Decision Record: Ephemeral PR Description Scratch File Hygiene

- **Date:** 2026-09-25
- **Status:** Accepted
- **Context:** Resolving Issue #195 on `azylman/mirrormere`:
  - `chore: PR_DESCRIPTION.md scratch file committed to repo root and overwritten by each PR`

---

## 1. Context & Inconsistency

Under SPEC-001 §2 (Repository Layout & Directory Structure):
- The canonical root of `azylman/mirrormere` contains specifications (`specs/`), architectural decisions (`decisions/`), core application source code (`cmd/`, `internal/`, `api/`), automation scripts (`scripts/`), container assets (`Dockerfile`, `compose.yaml`), and project metadata (`README.md`, `LICENSE`, `go.mod`, `go.sum`).
- Ephemeral or machine-local workflow artifacts must not be tracked in version control, as committing scratch files produces perpetual diff noise across PRs and violates clean repository layout standards.

Prior to this change:
- PR #191 added `PR_DESCRIPTION.md` at the repository root, and PR #192 subsequently overwrote it.
- Because `PR_DESCRIPTION.md` was tracked on `main`, any developer or agent turn relying on a scratch description file committed changes to it on `main`, polluting git history with ephemeral PR bodies that only describe the single most recent PR.

---

## 2. Decision

1. **Untrack `PR_DESCRIPTION.md` from Version Control**:
   - Removed `PR_DESCRIPTION.md` from git tracking via `git rm`.
2. **Ignore Scratch PR Descriptions in `.gitignore`**:
   - Added `PR_DESCRIPTION.md` to `.gitignore` under `Logs & Temporary Files`.
   - Any local scratch description authored at the repository root will be ignored by git and prevented from being committed or pushed.
3. **Out-of-Tree / In-Memory PR Body Generation**:
   - Automated workflows and agents may author PR descriptions in temporary directories (e.g. `mktemp`) or directly pass in-memory payloads via GitHub MCP / API calls, eliminating repository root pollution.

---

## 3. Verification & Test Evidence

- Executed `git ls-files PR_DESCRIPTION.md` to verify it is no longer tracked.
- Verified `.gitignore` contains `PR_DESCRIPTION.md`.
- Ran full pre-flight verification `./scripts/verify.sh --staged` confirming clean linters, dead code analysis, and >= 95.0% statement test coverage across all packages.

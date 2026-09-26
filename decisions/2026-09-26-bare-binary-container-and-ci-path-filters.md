# ADR: Bare Binary Container Image & CI/CD Path Filtering

## Context & Problem Statement
In previous builds, Mirrormere baked static web assets (`web/`) and core widget packages (`widgets/`) directly into the production container image via `COPY web /app/web` and `COPY widgets /app/widgets` in `Dockerfile`. Furthermore, the GitHub Actions Continuous Delivery workflow (`.github/workflows/ci.yml`) triggered on every push to `main` without path filtering, building and publishing the entire multi-arch container matrix (`mirrormere`, `mirrormere-cast-watcher`, `mirrormere-eink-renderer`) even when only documentation, specifications, or static assets were updated.

This introduced unnecessary build overhead, slow turnaround times for frontend/CSS iteration, and redundant image rebuilds when changes did not impact the compiled Go binaries.

## Decision
1. **Bare Binary Production Container (`Dockerfile`)**:
   - Removed `COPY web /app/web` and `COPY widgets /app/widgets` from `Dockerfile`.
   - The runtime container image now packages strictly the compiled Go binary (`/app/server`), timezone data, and root CA certificates.
   - Initialized mount point directories `/config`, `/data`, `/app/web`, and `/app/widgets` with unprivileged ownership (`10001:10001`).

2. **Host-Mounted Asset Volumes (`deploy/compose.yml`)**:
   - Added host-mounted asset volume definitions to `mirrormere-core` in `deploy/compose.yml`:
     - `../web:/app/web:ro`
     - `../widgets:/app/widgets:ro`
   - This allows instant iteration on web templates, JavaScript, CSS, and widget definitions without container rebuilds or restarts.

3. **Targeted CI/CD Path Filtering (`.github/workflows/ci.yml`)**:
   - Added `paths-ignore` on the `push: branches: [main]` trigger for documentation, specifications, ADRs, and markdown files (`docs/**`, `specs/**`, `decisions/**`, `*.md`, `.gitignore`, `.gitattributes`, `LICENSE`).
   - Integrated `dorny/paths-filter@v3` into the `docker` matrix job to evaluate changed paths per image:
     - `mirrormere`: builds strictly when `Dockerfile`, `go.mod`, `go.sum`, `Makefile`, `cmd/**`, `internal/**`, or `api/**` are modified.
     - `mirrormere-cast-watcher`: builds strictly when `sidecars/cast-watcher/**` is modified.
     - `mirrormere-eink-renderer`: builds strictly when `sidecars/eink-renderer/**` is modified.
   - Build and publish steps are conditionally guarded by `steps.filter.outputs[matrix.name] == 'true'`.

## Consequences
- **Positive**:
  - Image builds are skipped entirely when changes are limited to documentation or static assets.
  - Multi-arch Docker matrix jobs only build and publish the specific containers that were actually modified.
  - Rapid local iteration: template and CSS updates reflect immediately via host-mounted volumes without waiting on CI/CD pipelines.
- **Operational Requirement**:
  - Deployments running the bare binary image must volume-mount `/app/web` and `/app/widgets` from the host repository or asset directory.

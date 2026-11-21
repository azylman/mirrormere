#!/bin/sh
set -eu

# scripts/verify.sh - Fast Pre-Flight & CI Verification Engine for Mirrormere
# Enforces zero-drift between local developer checks and GitHub Actions CI.

# Prevent MSYS2 from mangling path conversions on Windows
export MSYS_NO_PATHCONV=1

# Ensure MinGit / MSYS binaries (/usr/bin, /mingw64/bin, /cmd) and cached lint tools (/data/cache/bin) are in PATH
for p in /data/cache/bin "$HOME/.local/bin" /usr/bin /mingw64/bin /cmd; do
    if [ -d "$p" ]; then
        case ":$PATH:" in
            *:"$p":*) ;;
            *) PATH="$p:$PATH" ;;
        esac
    fi
done

MODE="full"
STAGED_FILES=""
STAGED_FROM_INDEX=0
for arg in "$@"; do
    case "$arg" in
        --staged)
            MODE="staged"
            ;;
        --full)
            MODE="full"
            ;;
        *)
            echo "⚠️ [Mirrormere Verify] Unrecognized flag '$arg'; falling back to --staged verification." >&2
            MODE="staged"
            ;;
    esac
done

case "$0" in
    */*) SCRIPT_DIR="${0%/*}" ;;
    *) SCRIPT_DIR="." ;;
esac
SCRIPT_DIR="$(cd "$SCRIPT_DIR" && (pwd -W 2>/dev/null || pwd))"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && (pwd -W 2>/dev/null || pwd))"
cd "$REPO_ROOT"
chmod 755 "$REPO_ROOT" 2>/dev/null || true

echo "⚡ [Mirrormere Verify] Running $MODE verification checks..."

has_cmd() {
    command -v "$1" >/dev/null 2>&1
}

# 1. UTF-8 BOM Hygiene
check_utf8_bom() {
    if has_cmd git; then
        BOM_HEX=$(printf '\357\273\277')
        if [ "$MODE" = "staged" ]; then
            existing_staged=$(echo "$STAGED_FILES" | while read -r sf; do
                if [ -f "$sf" ]; then echo "$sf"; fi
            done)
            [ -z "$existing_staged" ] && return 0
            # shellcheck disable=SC2086 # POSIX sh has no arrays: intentional word splitting of a newline/space-separated path or package list
            BOM_FILES=$(git grep -I -l "^$BOM_HEX" -- $existing_staged 2>/dev/null || true)
        else
            BOM_FILES=$(git grep -I -l "^$BOM_HEX" 2>/dev/null || true)
        fi
        if [ -n "$BOM_FILES" ]; then
            echo "🚨 [Mirrormere Verify] Error: Invalid UTF-8 BOM detected in tracked files:" >&2
            echo "$BOM_FILES" | sed 's/^/   • /' >&2
            exit 1
        fi
    fi
}

check_gofmt() {
    if has_cmd gofmt; then
        if [ "$MODE" = "staged" ]; then
            staged_go=$(echo "$STAGED_FILES" | grep '\.go$' || true)
            if [ -z "$staged_go" ]; then
                return 0
            fi
            existing_go=$(echo "$staged_go" | while read -r gf; do
                [ -f "$gf" ] && echo "$gf" || true
            done)
            if [ -z "$existing_go" ]; then
                return 0
            fi
            # shellcheck disable=SC2086 # POSIX sh has no arrays: intentional word splitting of a newline/space-separated path or package list
            unformatted=$(gofmt -l $existing_go 2>/dev/null | grep -v -E '(^|/)vendor/' || true)
            if [ -n "$unformatted" ] && [ -z "${GITHUB_ACTIONS:-}" ]; then
                echo "   [gofmt] Auto-formatting staged Go files..."
                # shellcheck disable=SC2086 # POSIX sh has no arrays: intentional word splitting of a newline/space-separated path or package list
                gofmt -w $unformatted
                if [ "${STAGED_FROM_INDEX:-0}" -eq 1 ] && has_cmd git; then
                    # shellcheck disable=SC2086 # POSIX sh has no arrays: intentional word splitting of a newline/space-separated path or package list
                    git add $unformatted
                fi
                # shellcheck disable=SC2086 # POSIX sh has no arrays: intentional word splitting of a newline/space-separated path or package list
                unformatted=$(gofmt -l $existing_go 2>/dev/null | grep -v -E '(^|/)vendor/' || true)
            fi
        else
            echo "   [gofmt] Checking code formatting..."
            unformatted=$(gofmt -l . 2>/dev/null | grep -v -E '(^|/)vendor/' || true)
        fi
        if [ -n "$unformatted" ]; then
            echo "🚨 [Mirrormere Verify] Error: Unformatted Go files detected (run gofmt -w or golangci-lint fmt):" >&2
            echo "$unformatted" | sed 's/^/   • /' >&2
            exit 1
        fi
    fi
}

check_no_imperative_ddl() {
    files=""
    if [ "$MODE" = "staged" ]; then
        files=$(echo "$STAGED_FILES" | grep -E '\.(go|py|ts)$' | grep -v -E '(_test\.go|\.test\.ts|test_.*\.py|/testdata/)' | grep -v -E '^(scripts|vendor|node_modules)/' || true)
    else
        files=$(find . -maxdepth 4 -type f \( -name "*.go" -o -name "*.py" -o -name "*.ts" \) \
            ! -name "*_test.go" ! -name "*.test.ts" ! -name "test_*.py" \
            ! -path "*/vendor/*" ! -path "*/node_modules/*" ! -path "*/.git/*" ! -path "*/scripts/*" 2>/dev/null || true)
    fi

    [ -z "$files" ] && return 0

    violations=$(echo "$files" | while read -r f; do
        [ -f "$f" ] || continue
        grep -H -n -i -E '\b(CREATE|ALTER|DROP)[[:space:]]+TABLE\b' "$f" 2>/dev/null | grep -v -E '^[^:]+:[0-9]+:[[:space:]]*(//|#|\*)' || true
    done)

    if [ -n "$violations" ]; then
        echo "🚨 [Mirrormere Verify] Imperative DDL (CREATE/ALTER/DROP TABLE) detected in application code:" >&2
        echo "$violations" | sed 's/^/   • /' >&2
        echo "   Database schema changes must be declared in schema.sql and generated via scripts/atlas-diff.sh." >&2
        return 1
    fi
    return 0
}

# Verify dependabot configuration coverage and security group pairing
check_dependabot_coverage() {
    dep_file="$REPO_ROOT/.github/dependabot.yml"
    if [ ! -f "$dep_file" ]; then
        echo "🚨 [Mirrormere Verify] Error: Missing .github/dependabot.yml configuration file!" >&2
        exit 1
    fi

    missing_manifests=""
    # Discovered package manifests up to maxdepth 3 excluding vendor, node_modules, testdata, and hidden directories
    discovered_dirs=$(find . -maxdepth 3 -type f \( -name "go.mod" -o -name "Cargo.toml" -o -name "pyproject.toml" -o -name "package.json" -o -name "requirements.txt" \) \
        ! -path "*/vendor/*" ! -path "*/node_modules/*" ! -path "*/testdata/*" ! -path "*/.*" 2>/dev/null | \
        sed 's|^\./||' | while read -r mf; do dirname "$mf"; done | sort -u)

    for dir in $discovered_dirs; do
        if [ "$dir" = "." ]; then
            target_dir="/"
        else
            target_dir="/$dir"
        fi
        if ! grep -q -F "\"${target_dir}\"" "$dep_file" && ! grep -q -F "'${target_dir}'" "$dep_file" && ! grep -q -E "^[[:space:]]*(-[[:space:]]*)?\"?${target_dir}\"?[[:space:]]*$" "$dep_file"; then
            case " $missing_manifests " in
                *" $target_dir "*) ;;
                *) missing_manifests="${missing_manifests} $target_dir" ;;
            esac
        fi
    done

    if [ -n "$missing_manifests" ]; then
        echo "🚨 [Mirrormere Verify] Error: Discovered package directories missing from .github/dependabot.yml:" >&2
        for m in $missing_manifests; do
            echo "   • $m" >&2
        done
        exit 1
    fi

    eco_count=$(grep -c "package-ecosystem:" "$dep_file" || true)
    ver_count=$(grep -c "applies-to:[[:space:]]*version-updates" "$dep_file" || true)
    sec_count=$(grep -c "applies-to:[[:space:]]*security-updates" "$dep_file" || true)

    if [ "$eco_count" -le 0 ]; then
        echo "🚨 [Mirrormere Verify] Error: No package-ecosystem entries defined in .github/dependabot.yml!" >&2
        exit 1
    fi

    if [ "$eco_count" -ne "$ver_count" ] || [ "$eco_count" -ne "$sec_count" ]; then
        echo "🚨 [Mirrormere Verify] Error: Dependabot grouping mismatch in .github/dependabot.yml!" >&2
        echo "   package-ecosystems: $eco_count" >&2
        echo "   applies-to: version-updates: $ver_count" >&2
        echo "   applies-to: security-updates: $sec_count" >&2
        echo "   Every package-ecosystem must have both a version-updates and security-updates group." >&2
        exit 1
    fi
}

# 2. Ratcheted Code Health Gate (Cached Binary)
CODE_HEALTH_BIN=""
CODE_HEALTH_REF="github.com/azylman/aerial/scripts/code-health@fc8e678006b9b84a63486b5cc08cc24bc77c966c"
ensure_code_health() {
    gopath="$(go env GOPATH 2>/dev/null || true)"
    bin_dir="${gopath:-$HOME/go}/bin"
    mkdir -p "$bin_dir"
    CODE_HEALTH_BIN="$bin_dir/aerial-code-health-fc8e678006b9"
    if [ ! -x "$CODE_HEALTH_BIN" ]; then
        GOBIN="$bin_dir" GOFLAGS=-mod=mod go install "$CODE_HEALTH_REF"
        mv "$bin_dir/code-health" "$CODE_HEALTH_BIN"
    fi
}

run_code_health() {
    echo "   [code-health] Checking ratcheted complexity and length limits..."
    if has_cmd go; then
        ensure_code_health
        if [ "$MODE" = "staged" ] && ! echo "$STAGED_FILES" | grep -q -E '^(\.code-health-baseline\.json|\.code-health-config\.json|scripts/verify\.sh$)'; then
            staged_go_files=$(echo "$STAGED_FILES" | grep '\.go$' | while read -r gf; do
                [ -f "$gf" ] && echo "$gf" || true
            done)
            if [ -z "$staged_go_files" ]; then
                return 0
            fi
            if [ -z "${GITHUB_ACTIONS:-}" ] && has_cmd git; then
                staged_clean=""
                for f in $staged_go_files; do
                    if [ -f "$f" ] && git diff --quiet -- "$f" 2>/dev/null; then
                        staged_clean="${staged_clean:+$staged_clean }$f"
                    fi
                done
                if [ -n "$staged_clean" ]; then
                    # shellcheck disable=SC2086 # POSIX sh has no arrays: intentional word splitting of a newline/space-separated path or package list
                    "$CODE_HEALTH_BIN" -fix -root "$REPO_ROOT" -baseline "$REPO_ROOT/.code-health-baseline.json" -config "$REPO_ROOT/.code-health-config.json" $staged_clean >/dev/null 2>&1 || true
                    for f in $staged_clean; do
                        if [ -f "$f" ] && ! git diff --quiet -- "$f" 2>/dev/null; then
                            git add "$f"
                        fi
                    done
                fi
            fi
            # Scope code health checks to all Go files in modified package directories
            # to ensure package-level AST function/struct clone detection compares against sibling files.
            code_health_targets=$(for gf in $staged_go_files; do
                d=$(dirname "$gf")
                if [ -d "$d" ]; then
                    find "$d" -maxdepth 1 -name '*.go' -type f
                fi
            done | sort -u)
            # shellcheck disable=SC2086 # POSIX sh has no arrays: intentional word splitting of a newline/space-separated path or package list
            "$CODE_HEALTH_BIN" -root "$REPO_ROOT" -baseline "$REPO_ROOT/.code-health-baseline.json" -config "$REPO_ROOT/.code-health-config.json" $code_health_targets
        else
            "$CODE_HEALTH_BIN" -root "$REPO_ROOT" -baseline "$REPO_ROOT/.code-health-baseline.json" -config "$REPO_ROOT/.code-health-config.json"
        fi
    else
        echo "🚨 [Mirrormere Verify] Error: go not found in PATH." >&2
        exit 1
    fi
}

# 3. GolangCI-Lint
run_golangci_lint() {
    echo "   [golangci-lint] Running strict linters..."
    lint_bin=""
    if has_cmd golangci-lint; then
        lint_bin="golangci-lint"
    elif [ -x "$(go env GOPATH 2>/dev/null)/bin/golangci-lint" ]; then
        lint_bin="$(go env GOPATH)/bin/golangci-lint"
    elif [ -x "$HOME/go/bin/golangci-lint" ]; then
        lint_bin="$HOME/go/bin/golangci-lint"
    fi

    if [ -n "$lint_bin" ]; then
        if [ "$MODE" = "staged" ] && ! echo "$STAGED_FILES" | grep -q -E '^(go\.(mod|sum)|\.golangci\.yml|scripts/verify\.sh)$'; then
            lint_pkgs=$(echo "$STAGED_FILES" | grep '\.go$' | while read -r f; do
                d=$(dirname "$f")
                [ -d "$d" ] && echo "./$d"
            done | sort -u)
            if [ -n "$lint_pkgs" ]; then
                # shellcheck disable=SC2086 # POSIX sh has no arrays: intentional word splitting of a newline/space-separated path or package list
                "$lint_bin" run --allow-parallel-runners $lint_pkgs
            fi
        else
            "$lint_bin" run --allow-parallel-runners ./...
        fi
    else
        echo "🚨 [Mirrormere Verify] Error: golangci-lint not found in PATH." >&2
        exit 1
    fi
}

# 4. Dead Code Analysis
DEADCODE_BIN=""
ensure_deadcode() {
    if [ -n "$DEADCODE_BIN" ]; then
        return 0
    fi
    if has_cmd deadcode; then
        DEADCODE_BIN="deadcode"
        return 0
    fi
    gopath="$(go env GOPATH 2>/dev/null || true)"
    if [ -n "$gopath" ] && [ -x "$gopath/bin/deadcode" ]; then
        DEADCODE_BIN="$gopath/bin/deadcode"
        return 0
    fi
    if [ -x "$HOME/go/bin/deadcode" ]; then
        DEADCODE_BIN="$HOME/go/bin/deadcode"
        return 0
    fi
    if has_cmd go; then
        echo "   [deadcode] Installing deadcode via go install..."
        go install golang.org/x/tools/cmd/deadcode@v0.52.0
        if [ -n "$gopath" ] && [ -x "$gopath/bin/deadcode" ]; then
            DEADCODE_BIN="$gopath/bin/deadcode"
            return 0
        elif [ -x "$HOME/go/bin/deadcode" ]; then
            DEADCODE_BIN="$HOME/go/bin/deadcode"
            return 0
        fi
    fi
    echo "🚨 [Mirrormere Verify] Error: Neither deadcode nor go found in PATH to perform dead code analysis." >&2
    exit 1
}

run_deadcode() {
    ensure_deadcode
    echo "   [deadcode] Checking for unreachable code (production & test graphs)..."
    main_pkgs=$(go list -f '{{if eq .Name "main"}}{{.ImportPath}}{{end}}' ./...) || {
        echo "🚨 [Mirrormere Verify] Error: failed to list main packages" >&2
        exit 1
    }

    tmp_prod=$(mktemp)
    tmp_test=$(mktemp)

    # shellcheck disable=SC2086 # POSIX sh has no arrays: intentional word splitting of a newline/space-separated path or package list
    "$DEADCODE_BIN" $main_pkgs > "$tmp_prod" 2>&1 &
    pid_prod=$!

    "$DEADCODE_BIN" -test ./... > "$tmp_test" 2>&1 &
    pid_test=$!

    rc_prod=0
    wait "$pid_prod" || rc_prod=$?

    rc=0
    wait "$pid_test" || rc=$?

    dead_prod=$(cat "$tmp_prod" 2>/dev/null || true)
    dead_output=$(cat "$tmp_test" 2>/dev/null || true)
    rm -f "$tmp_prod" "$tmp_test"

    if [ "$rc_prod" -ne 0 ]; then
        echo "🚨 [Mirrormere Verify] Error: deadcode (production) execution failed (exit $rc_prod):" >&2
        echo "$dead_prod" >&2
        exit 1
    fi
    if [ -n "$dead_prod" ]; then
        echo "🚨 [Mirrormere Verify] Unreachable production code or compiled-in test hooks detected:" >&2
        echo "$dead_prod" | sed 's/^/   • /' >&2
        exit 1
    fi

    if [ "$rc" -ne 0 ]; then
        echo "🚨 [Mirrormere Verify] Error: deadcode (-test) execution failed (exit $rc):" >&2
        echo "$dead_output" >&2
        exit 1
    fi
    if [ -n "$dead_output" ]; then
        echo "🚨 [Mirrormere Verify] Dead code detected:" >&2
        echo "$dead_output" | sed 's/^/   • /' >&2
        exit 1
    fi
}

# 5. Test Suite & Coverage Threshold Gating
run_coverage_gate() {
    echo "   [test & coverage] Checking test execution and 95.0% coverage floor..."
    sh "$REPO_ROOT/scripts/check-coverage.sh" --check --summary
}

# 6. Client & Sidecar Runtime Tests (Node.js)
run_node_tests() {
    if has_cmd node; then
        echo "   [node test] Running web client, perf, and sidecar test suites..."
        TZ=UTC node --test scripts/build-metadata.test.cjs scripts/perf/*.test.js sidecars/eink-renderer/test/*.test.js
    fi
}

# 7. Python Client Tests (E-Ink, Voice & Remote)
run_python_tests() {
    if has_cmd python3; then
        echo "   [python test] Running e-ink node client test suite..."
        PYTHONPATH="client/eink-node" python3 -m unittest discover -s client/eink-node/tests -p "test_*.py"
        echo "   [python test] Running edge ear (voice) daemon test suite..."
        PYTHONPATH="." python3 -m unittest discover -s client/ear/tests -p "test_*.py"
        echo "   [python test] Running remote (bluetooth/chromecast) client test suite..."
        PYTHONPATH="client/remote" python3 -m unittest discover -s client/remote/tests -p "test_*.py"
        if python3 -c "import pytest" >/dev/null 2>&1 && [ -d "client/ember/tests" ]; then
            echo "   [python test] Running EMBER Android kiosk test suite..."
            PYTHONPATH="client/ember" python3 -m pytest client/ember/tests/test_ember.py -q
        fi
        echo "   [python test] Running iCal bridge test suite..."
        PYTHONPATH="deploy/ical-bridge" python3 -m unittest discover -s deploy/ical-bridge -p "test_*.py"
    fi
}

# 8. React UI (web/ui): lint, deadcode, typecheck, tests, build.
# Runs when web/ui changed; runs npm ci if node_modules is missing.
run_frontend_checks() {
    echo "   [frontend] Verifying per-widget CSS co-location..."
    if [ -f "$REPO_ROOT/web/ui/src/styles/widgets.css" ]; then
        echo "🚨 [Mirrormere Verify] Error: web/ui/src/styles/widgets.css must not exist (monolithic widget styles are prohibited; co-locate CSS with each widget component)." >&2
        exit 1
    fi
    if [ -d "$REPO_ROOT/web/ui/src/styles" ] && grep -rn -F '[data-widget-type=' "$REPO_ROOT/web/ui/src/styles" >/dev/null 2>&1; then
        echo "🚨 [Mirrormere Verify] Error: Monolithic widget selectors ([data-widget-type=) found under web/ui/src/styles/. Co-locate per-widget styles with each widget component." >&2
        exit 1
    fi
}

run_ui_checks() {
    run_frontend_checks
    if ! has_cmd npm; then
        echo "🚨 [Mirrormere Verify] Error: npm not found in PATH; web/ui checks cannot run." >&2
        exit 1
    fi
    if [ ! -d "$REPO_ROOT/web/ui/node_modules" ]; then
        echo "   [ui] Installing dependencies in web/ui..."
        (cd "$REPO_ROOT/web/ui" && npm ci --no-audit --no-fund)
    fi
    echo "   [ui] Running React UI lint, deadcode, typecheck, coverage tests and build..."
    if [ "$MODE" = "staged" ] && ! echo "$STAGED_FILES" | grep -q -E '^(web/ui/(eslint\.config\.js|package(-lock)?\.json)|scripts/verify\.sh)$'; then
        staged_ui_lint=$(echo "$STAGED_FILES" | grep -E '^web/ui/.*\.(ts|tsx|js|mjs|cjs)$' | sed 's|^web/ui/||' | while read -r uf; do
            [ -f "$REPO_ROOT/web/ui/$uf" ] && echo "$uf"
        done)
        if [ -n "$staged_ui_lint" ]; then
            # shellcheck disable=SC2086 # POSIX sh has no arrays: intentional word splitting of a newline/space-separated path or package list
            (cd "$REPO_ROOT/web/ui" && npx --no-install eslint --max-warnings=0 $staged_ui_lint)
        fi
    else
        (cd "$REPO_ROOT/web/ui" && npm run lint)
    fi
    if [ "$MODE" = "staged" ] && ! echo "$STAGED_FILES" | grep -q -E '^(web/ui/(knip\.json|tsconfig\.json|vite\.config\.ts|package(-lock)?\.json)|scripts/verify\.sh)$'; then
        if echo "$STAGED_FILES" | grep -q -E '^web/ui/.*\.(ts|tsx|js|mjs|cjs)$'; then
            (cd "$REPO_ROOT/web/ui" && npm run deadcode && npm run typecheck)
        fi
    else
        (cd "$REPO_ROOT/web/ui" && npm run deadcode && npm run typecheck)
    fi
    if [ "$MODE" = "staged" ] && ! echo "$STAGED_FILES" | grep -q -E '^(web/ui/(vite\.config\.ts|tsconfig\.json|package(-lock)?\.json)|scripts/verify\.sh)$'; then
        staged_ui_src=$(echo "$STAGED_FILES" | grep -E '^web/ui/src/.*\.(ts|tsx)$' | grep -v '\.d\.ts$' | sed 's|^web/ui/||' | while read -r uf; do
            [ -f "$REPO_ROOT/web/ui/$uf" ] && echo "$uf"
        done)
        if [ -n "$staged_ui_src" ]; then
            staged_ui_dirs=$(echo "$staged_ui_src" | while read -r uf; do dirname "$uf"; done | sort -u)
            staged_ui_tests=$(for d in $staged_ui_dirs; do
                if [ -d "$REPO_ROOT/web/ui/$d" ]; then
                    (cd "$REPO_ROOT/web/ui" && find "$d" -maxdepth 1 \( -name '*.test.ts' -o -name '*.test.tsx' \) -type f)
                fi
            done | sort -u)
            staged_cov_include=$(for d in $staged_ui_dirs; do
                printf '%s\n' "--coverage.include=$d/*.{ts,tsx}"
            done)
            staged_targets=$(printf '%s\n%s\n' "$staged_ui_src" "$staged_ui_tests" | sort -u | grep -v '^$')
            # shellcheck disable=SC2086 # POSIX sh has no arrays: intentional word splitting of a newline/space-separated path or package list
            (cd "$REPO_ROOT/web/ui" && npx --no-install vitest related --run --coverage \
                --coverage.thresholds.statements=85 \
                --coverage.thresholds.branches=75 \
                --coverage.thresholds.functions=70 \
                --coverage.thresholds.lines=85 \
                $staged_cov_include $staged_targets)
        fi
    else
        (cd "$REPO_ROOT/web/ui" && npm run test:coverage)
    fi
    if [ "$MODE" != "staged" ] || echo "$STAGED_FILES" | grep -q -E '^(web/ui/(.*\.css|index\.html|vite\.config\.ts|tsconfig\.json|package(-lock)?\.json)|scripts/verify\.sh)$'; then
        (cd "$REPO_ROOT/web/ui" && npm run build)
    fi
}

# 9. Vanilla JS lint (root eslint.config.js): web/static/js,
# sidecars/eink-renderer and scripts/perf. web/ui is linted by its own config.
# --staged lints only staged files in scope (whole scope when the root lint
# config or this script is staged); --full and CI always lint the whole scope.
JS_LINT_FILES='^(web/static/js|sidecars/eink-renderer|scripts/perf)/.*\.(js|mjs|cjs)$'
JS_LINT_CONFIG='^(eslint\.config\.js|package(-lock)?\.json|scripts/verify\.sh)$'

run_js_lint() {
    staged_js_lint=""
    if [ "$MODE" = "staged" ] && ! echo "$STAGED_FILES" | grep -q -E "$JS_LINT_CONFIG"; then
        staged_js_lint=$(echo "$STAGED_FILES" | grep -E "$JS_LINT_FILES" | while read -r jf; do
            if [ -f "$REPO_ROOT/$jf" ]; then echo "$jf"; fi
        done)
        if [ -z "$staged_js_lint" ]; then
            return 0
        fi
    fi
    if ! has_cmd npm; then
        echo "🚨 [Mirrormere Verify] Error: npm not found in PATH; vanilla JS lint cannot run." >&2
        exit 1
    fi
    if [ ! -d "$REPO_ROOT/node_modules" ]; then
        echo "   [js lint] Installing root lint dependencies..."
        (cd "$REPO_ROOT" && npm ci --no-audit --no-fund)
    fi
    if [ -n "$staged_js_lint" ]; then
        echo "   [js lint] Linting staged vanilla JS with ESLint..."
        # shellcheck disable=SC2086 # POSIX sh has no arrays: intentional word splitting of a newline/space-separated path or package list
        (cd "$REPO_ROOT" && npx --no-install eslint --max-warnings=0 --no-warn-ignored $staged_js_lint)
    else
        echo "   [js lint] Linting all vanilla JS with ESLint..."
        (cd "$REPO_ROOT" && npm run lint)
    fi
}

# 10. Python lint + format (root ruff.toml) over all first-party Python.
# The ruff version is pinned in requirements-lint.txt and enforced here so local
# runs and CI agree on formatter output. --staged checks only staged Python
# files (whole scope when ruff.toml, the pin or this script is staged); --full
# and CI always check the whole scope.
RUFF_CONFIG='^(ruff\.toml|requirements-lint\.txt|scripts/verify\.sh)$'

run_ruff() {
    staged_py=""
    if [ "$MODE" = "staged" ] && ! echo "$STAGED_FILES" | grep -q -E "$RUFF_CONFIG"; then
        staged_py=$(echo "$STAGED_FILES" | grep -E '\.pyi?$' | while read -r pf; do
            if [ -f "$REPO_ROOT/$pf" ]; then echo "$pf"; fi
        done)
        if [ -z "$staged_py" ]; then
            return 0
        fi
    fi
    if ! has_cmd ruff; then
        echo "🚨 [Mirrormere Verify] Error: ruff not found in PATH; install the pinned version with: pip install -r requirements-lint.txt" >&2
        exit 1
    fi
    ruff_pinned=$(sed -n 's/^ruff==//p' "$REPO_ROOT/requirements-lint.txt")
    ruff_installed=$(ruff --version | awk '{print $2}')
    if [ -z "$ruff_pinned" ] || [ "$ruff_installed" != "$ruff_pinned" ]; then
        echo "🚨 [Mirrormere Verify] Error: ruff ${ruff_installed:-unknown} does not match the pin in requirements-lint.txt (${ruff_pinned:-missing}); run: pip install -r requirements-lint.txt" >&2
        exit 1
    fi
    if [ -n "$staged_py" ]; then
        echo "   [ruff] Linting and format-checking staged Python..."
        # --force-exclude keeps ruff.toml/.gitignore exclusions for explicit paths.
        # shellcheck disable=SC2086 # intentional word splitting of the staged file list
        (cd "$REPO_ROOT" && ruff check --force-exclude $staged_py && ruff format --check --force-exclude $staged_py)
    else
        echo "   [ruff] Linting and format-checking all Python..."
        (cd "$REPO_ROOT" && ruff check && ruff format --check)
    fi
}

# 11. Shell lint: shellcheck at style severity over every tracked *.sh file.
# The shellcheck version is pinned in requirements-lint.txt (shellcheck-py wraps
# the upstream binary; its first three version fields are the shellcheck
# release). --staged checks only staged shell scripts (whole scope when
# .shellcheckrc, the pin or this script is staged); --full and CI always check
# the whole scope.
SHELLCHECK_CONFIG='^(\.shellcheckrc|requirements-lint\.txt|scripts/verify\.sh)$'

run_shellcheck() {
    staged_sh=""
    if [ "$MODE" = "staged" ] && ! echo "$STAGED_FILES" | grep -q -E "$SHELLCHECK_CONFIG"; then
        staged_sh=$(echo "$STAGED_FILES" | grep -E '\.sh$' | while read -r sf; do
            if [ -f "$REPO_ROOT/$sf" ]; then echo "$sf"; fi
        done)
        if [ -z "$staged_sh" ]; then
            return 0
        fi
    fi
    if ! has_cmd shellcheck; then
        echo "🚨 [Mirrormere Verify] Error: shellcheck not found in PATH; install the pinned version with: pip install -r requirements-lint.txt" >&2
        exit 1
    fi
    shellcheck_pinned=$(sed -n 's/^shellcheck-py==\([0-9]*\.[0-9]*\.[0-9]*\).*/\1/p' "$REPO_ROOT/requirements-lint.txt")
    shellcheck_installed=$(shellcheck --version | sed -n 's/^version: //p')
    if [ -z "$shellcheck_pinned" ] || [ "$shellcheck_installed" != "$shellcheck_pinned" ]; then
        echo "🚨 [Mirrormere Verify] Error: shellcheck ${shellcheck_installed:-unknown} does not match the pin in requirements-lint.txt (${shellcheck_pinned:-missing}); run: pip install -r requirements-lint.txt" >&2
        exit 1
    fi
    if [ -n "$staged_sh" ]; then
        echo "   [shellcheck] Linting staged shell scripts..."
        # shellcheck disable=SC2086 # POSIX sh has no arrays: intentional word splitting of the staged file list
        (cd "$REPO_ROOT" && shellcheck --severity=style $staged_sh)
    else
        echo "   [shellcheck] Linting all tracked shell scripts..."
        (cd "$REPO_ROOT" && git ls-files -z '*.sh' | xargs -0 shellcheck --severity=style)
    fi
}

if [ "$MODE" = "staged" ]; then
    if has_cmd git; then
        git rm --cached -q PR_DESCRIPTION.md .pr_description.md 2>/dev/null || true
    fi
    STAGED_FROM_INDEX=0
    STAGED_FILES=$(git diff --cached --name-only 2>/dev/null || true)
    if [ -n "$STAGED_FILES" ]; then
        STAGED_FROM_INDEX=1
    elif has_cmd git; then
        STAGED_FILES=$({ git diff --name-only 2>/dev/null; git ls-files --others --exclude-standard 2>/dev/null; } | grep -v -E '^(PR_DESCRIPTION\.md|\.pr_description\.md)$' | sort -u || true)
        if [ -z "$STAGED_FILES" ] && git rev-parse --verify -q origin/main >/dev/null 2>&1; then
            if [ "$(git rev-parse HEAD 2>/dev/null || true)" != "$(git rev-parse origin/main 2>/dev/null || true)" ]; then
                STAGED_FILES=$(git show --pretty="" --name-only HEAD 2>/dev/null | grep -v -E '^(PR_DESCRIPTION\.md|\.pr_description\.md)$' || true)
                if [ -z "$STAGED_FILES" ]; then
                    base=$(git merge-base HEAD origin/main 2>/dev/null || true)
                    if [ -n "$base" ]; then
                        STAGED_FILES=$(git diff --name-only "$base" HEAD 2>/dev/null | grep -v -E '^(PR_DESCRIPTION\.md|\.pr_description\.md)$' || true)
                    fi
                fi
            fi
        fi
    fi
    if [ -z "$STAGED_FILES" ]; then
        echo "✅ [Mirrormere Verify] No staged files to verify."
        exit 0
    fi

    check_utf8_bom
    check_gofmt
    check_no_imperative_ddl
    if echo "$STAGED_FILES" | grep -q -E '^(\.github/dependabot\.yml|scripts/verify\.sh)$|(^|/)(go\.mod|Cargo\.toml|pyproject\.toml|package\.json|requirements\.txt)$'; then
        check_dependabot_coverage
    fi
    if echo "$STAGED_FILES" | grep -q -E '^scripts/verify\.sh$'; then
        run_frontend_checks
    fi

    if echo "$STAGED_FILES" | grep -q -E '\.go$|\.code-health-baseline\.json$|\.code-health-config\.json$|^scripts/verify\.sh$'; then
        run_code_health
    fi

    if echo "$STAGED_FILES" | grep -q -E '\.go$|^go\.(mod|sum)$|^\.golangci\.yml$|^scripts/verify\.sh$'; then
        run_golangci_lint
    fi

    if echo "$STAGED_FILES" | grep -q -E '^go\.(mod|sum)$'; then
        run_coverage_gate
    elif echo "$STAGED_FILES" | grep -q '\.go$'; then
        pkgs=$(echo "$STAGED_FILES" | grep '\.go$' | while read -r f; do dirname "$f"; done | sort -u)
        for p in $pkgs; do
            if [ -d "$p" ]; then
                sh "$REPO_ROOT/scripts/check-coverage.sh" --check --service "$p"
            fi
        done
    fi

    if has_cmd node; then
        staged_node_tests=""
        if echo "$STAGED_FILES" | grep -q -E '^scripts/build-metadata(\.test)?\.cjs$'; then
            staged_node_tests="${staged_node_tests:+$staged_node_tests }scripts/build-metadata.test.cjs"
        fi
        if echo "$STAGED_FILES" | grep -q -E '^scripts/perf/'; then
            staged_node_tests="${staged_node_tests:+$staged_node_tests }scripts/perf/check-budget.test.js scripts/perf/swipe-point.test.js scripts/perf/perf.test.js"
        fi
        if echo "$STAGED_FILES" | grep -q -E '^web/static/js/perf\.js$'; then
            staged_node_tests="${staged_node_tests:+$staged_node_tests }scripts/perf/perf.test.js"
        fi
        if [ -n "$staged_node_tests" ]; then
            # shellcheck disable=SC2086 # POSIX sh has no arrays: intentional word splitting of a newline/space-separated path or package list
            staged_node_tests=$(printf '%s\n' $staged_node_tests | sort -u)
            echo "   [node test] Running scoped web client and perf Node.js tests..."
            # shellcheck disable=SC2086 # POSIX sh has no arrays: intentional word splitting of a newline/space-separated path or package list
            TZ=UTC node --test $staged_node_tests
        fi
    fi

    if echo "$STAGED_FILES" | grep -v -E '^web/ui/' | grep -q -E '^(web/|widgets/)' && has_cmd go; then
        if ! echo "$STAGED_FILES" | grep -q -E '^go\.(mod|sum)$'; then
            candidate_pkgs=""
            if echo "$STAGED_FILES" | grep -v -E '^web/ui/' | grep -q -E '^web/(templates/|static/)'; then
                candidate_pkgs="${candidate_pkgs:+$candidate_pkgs }web internal/server"
            fi
            if echo "$STAGED_FILES" | grep -q -E '^widgets/'; then
                candidate_pkgs="${candidate_pkgs:+$candidate_pkgs }internal/render internal/server internal/widget"
            fi
            needed_pkgs=""
            # shellcheck disable=SC2086 # POSIX sh has no arrays: intentional word splitting of a newline/space-separated path or package list
            for p in $(printf '%s\n' $candidate_pkgs | sort -u); do
                if ! echo "${pkgs:-}" | grep -q -x "$p"; then
                    needed_pkgs="${needed_pkgs:+$needed_pkgs }./$p"
                fi
            done
            if [ -n "$needed_pkgs" ]; then
                echo "   [go test] Validating HTML templates and widget manifests..."
                # shellcheck disable=SC2086 # POSIX sh has no arrays: intentional word splitting of a newline/space-separated path or package list
                go test $needed_pkgs
            fi
        fi
    fi

    if echo "$STAGED_FILES" | grep -q -E '^web/ui/'; then
        run_ui_checks
    fi

    run_js_lint
    run_ruff
    run_shellcheck

    if echo "$STAGED_FILES" | grep -q -E '^sidecars/eink-renderer/' && has_cmd node; then
        echo "   [node test] Running eink-renderer sidecar test suite..."
        TZ=UTC node --test sidecars/eink-renderer/test/*.test.js
    fi

    if echo "$STAGED_FILES" | grep -q -E '^sidecars/voice-fingerprinter/' && has_cmd python3; then
        if python3 -c "import pytest" >/dev/null 2>&1 && [ -d "sidecars/voice-fingerprinter/test" ]; then
            echo "   [python test] Running voice-fingerprinter sidecar test suite..."
            (cd sidecars/voice-fingerprinter && python3 -m pytest test/ -q)
        fi
    fi

    if echo "$STAGED_FILES" | grep -q -E '^client/eink-node/' && has_cmd python3; then
        echo "   [python test] Running e-ink node client test suite..."
        PYTHONPATH="client/eink-node" python3 -m unittest discover -s client/eink-node/tests -p "test_*.py"
    fi

    if echo "$STAGED_FILES" | grep -q -E '^client/ear/' && has_cmd python3; then
        echo "   [python test] Running edge ear (voice) daemon test suite..."
        PYTHONPATH="." python3 -m unittest discover -s client/ear/tests -p "test_*.py"
    fi

    if echo "$STAGED_FILES" | grep -q -E '^client/remote/' && has_cmd python3; then
        echo "   [python test] Running remote (bluetooth/chromecast) client test suite..."
        PYTHONPATH="client/remote" python3 -m unittest discover -s client/remote/tests -p "test_*.py"
    fi

    if echo "$STAGED_FILES" | grep -q -E '^client/ember/' && has_cmd python3; then
        if python3 -c "import pytest" >/dev/null 2>&1 && [ -d "client/ember/tests" ]; then
            echo "   [python test] Running EMBER Android kiosk test suite..."
            PYTHONPATH="client/ember" python3 -m pytest client/ember/tests/test_ember.py -q
        fi
    fi

    if echo "$STAGED_FILES" | grep -q -E '^deploy/ical-bridge/' && has_cmd python3; then
        echo "   [python test] Running iCal bridge test suite..."
        PYTHONPATH="deploy/ical-bridge" python3 -m unittest discover -s deploy/ical-bridge -p "test_*.py"
    fi

    if has_cmd git && git diff --quiet 2>/dev/null; then
        git_dir=$(git rev-parse --git-dir 2>/dev/null || true)
        tree_sha=$(git write-tree 2>/dev/null || true)
        if [ -n "$git_dir" ] && [ -n "$tree_sha" ]; then
            printf '%s\n' "$tree_sha" > "$git_dir/.aerial-verified-tree" 2>/dev/null || true
        fi
    fi

    echo "✨ [Mirrormere Verify] Fast pre-flight checks passed cleanly!"
    exit 0
fi

# Full verification: Run entire CI-equivalent verification suite
check_utf8_bom
check_gofmt
check_no_imperative_ddl
check_dependabot_coverage
run_frontend_checks
run_code_health
run_golangci_lint
run_deadcode
run_coverage_gate
run_js_lint
run_ruff
run_shellcheck
ui_changed=""
if [ -n "$(git status --porcelain -- web/ui 2>/dev/null)" ]; then
    ui_changed=1
fi
if git rev-parse --verify -q origin/main >/dev/null 2>&1; then
    base=$(git merge-base HEAD origin/main 2>/dev/null || true)
    if [ -n "$base" ] && [ -n "$(git diff --name-only "$base" HEAD -- web/ui 2>/dev/null)" ]; then
        ui_changed=1
    fi
fi
if [ -n "$ui_changed" ]; then
    run_ui_checks
fi

run_node_tests
run_python_tests

echo "✨ [Mirrormere Verify] All pre-flight checks passed successfully! Ready for commit/PR."

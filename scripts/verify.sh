#!/bin/sh
set -eu

# scripts/verify.sh - Fast Pre-Flight & CI Verification Engine for Mirrormere
# Enforces zero-drift between local developer checks and GitHub Actions CI.

# Prevent MSYS2 from mangling path conversions on Windows
export MSYS_NO_PATHCONV=1

# Ensure MinGit / MSYS binaries (/usr/bin, /mingw64/bin, /cmd) are in PATH
for p in /usr/bin /mingw64/bin /cmd; do
    if [ -d "$p" ]; then
        case ":$PATH:" in
            *:"$p":*) ;;
            *) PATH="$p:$PATH" ;;
        esac
    fi
done

MODE="full"
for arg in "$@"; do
    case "$arg" in
        --staged)
            MODE="staged"
            ;;
        --lint-only)
            MODE="lint"
            ;;
        --full)
            MODE="full"
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
        BOM_FILES=$(git grep -I -l "^$BOM_HEX" 2>/dev/null || true)
        if [ -n "$BOM_FILES" ]; then
            echo "🚨 [Mirrormere Verify] Error: Invalid UTF-8 BOM detected in tracked files:" >&2
            echo "$BOM_FILES" | sed 's/^/   • /' >&2
            exit 1
        fi
    fi
}

check_gofmt() {
    if has_cmd gofmt; then
        if [ "$MODE" = "staged" ] && has_cmd git; then
            staged_go=$(git diff --cached --name-only --diff-filter=ACM 2>/dev/null | grep '\.go$' || true)
            if [ -z "$staged_go" ]; then
                return 0
            fi
            # shellcheck disable=SC2086
            unformatted=$(gofmt -l $staged_go 2>/dev/null | grep -v -E '(^|/)vendor/' || true)
            if [ -n "$unformatted" ] && [ -z "${GITHUB_ACTIONS:-}" ]; then
                echo "   [gofmt] Auto-formatting staged Go files..."
                # shellcheck disable=SC2086
                gofmt -w $unformatted
                # shellcheck disable=SC2086
                git add $unformatted
                # shellcheck disable=SC2086
                unformatted=$(gofmt -l $staged_go 2>/dev/null | grep -v -E '(^|/)vendor/' || true)
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
        files=$(git diff --cached --name-only 2>/dev/null | grep -E '\.(go|py|ts)$' | grep -v -E '(_test\.go|\.test\.ts|test_.*\.py|/testdata/)' | grep -v -E '^(scripts|vendor|node_modules)/' || true)
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

# 2. Ratcheted Code Health Gate (Cached Binary)
CODE_HEALTH_BIN=""
ensure_code_health() {
    gopath="$(go env GOPATH 2>/dev/null || true)"
    bin_dir="${gopath:-$HOME/go}/bin"
    mkdir -p "$bin_dir"
    CODE_HEALTH_BIN="$bin_dir/mirrormere-code-health"
    src_sum=$(cksum "$REPO_ROOT/scripts/code-health/"*.go 2>/dev/null || true)
    if [ ! -x "$CODE_HEALTH_BIN" ] || [ -z "$src_sum" ] || [ "$(cat "${CODE_HEALTH_BIN}.cksum" 2>/dev/null || true)" != "$src_sum" ]; then
        (cd "$REPO_ROOT/scripts/code-health" && go build -o "$CODE_HEALTH_BIN" .)
        printf '%s\n' "$src_sum" > "${CODE_HEALTH_BIN}.cksum"
    fi
}

run_code_health() {
    echo "   [code-health] Checking ratcheted complexity and length limits..."
    if has_cmd go; then
        ensure_code_health
        "$CODE_HEALTH_BIN" -root "$REPO_ROOT" -baseline "$REPO_ROOT/.code-health-baseline.json"
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
        "$lint_bin" run --allow-parallel-runners ./...
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
    echo "   [deadcode] Checking for unreachable code (production & test graphs)..."
    ensure_deadcode
    main_pkgs=$(go list -f '{{if eq .Name "main"}}{{.ImportPath}}{{end}}' ./...) || {
        echo "🚨 [Mirrormere Verify] Error: failed to list main packages" >&2
        exit 1
    }

    tmp_prod=$(mktemp)
    tmp_test=$(mktemp)

    # shellcheck disable=SC2086
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
        if [ "$rc_prod" -eq 137 ] && [ "$MODE" = "staged" ]; then
            echo "   [deadcode] Warning: deadcode killed by cgroup OOM in local pre-flight (exit 137), deferred to CI." >&2
            return 0
        fi
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
        if [ "$rc" -eq 137 ] && [ "$MODE" = "staged" ]; then
            echo "   [deadcode] Warning: deadcode killed by cgroup OOM in local pre-flight (exit 137), deferred to CI." >&2
            return 0
        fi
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

# 6. Codegen Zero-Drift Gate
OAPI_CODEGEN_BIN=""
ensure_oapi_codegen() {
    if [ -n "$OAPI_CODEGEN_BIN" ]; then
        return 0
    fi
    if has_cmd oapi-codegen; then
        OAPI_CODEGEN_BIN="oapi-codegen"
        return 0
    fi
    gopath="$(go env GOPATH 2>/dev/null || true)"
    if [ -n "$gopath" ] && [ -x "$gopath/bin/oapi-codegen" ]; then
        OAPI_CODEGEN_BIN="$gopath/bin/oapi-codegen"
        export PATH="$gopath/bin:$PATH"
        return 0
    fi
    if [ -x "$HOME/go/bin/oapi-codegen" ]; then
        OAPI_CODEGEN_BIN="$HOME/go/bin/oapi-codegen"
        export PATH="$HOME/go/bin:$PATH"
        return 0
    fi
    if has_cmd go; then
        echo "   [oapi-codegen] Installing oapi-codegen via go install..."
        go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.4.1
        if [ -n "$gopath" ] && [ -x "$gopath/bin/oapi-codegen" ]; then
            OAPI_CODEGEN_BIN="$gopath/bin/oapi-codegen"
            export PATH="$gopath/bin:$PATH"
            return 0
        elif [ -x "$HOME/go/bin/oapi-codegen" ]; then
            OAPI_CODEGEN_BIN="$HOME/go/bin/oapi-codegen"
            export PATH="$HOME/go/bin:$PATH"
            return 0
        fi
    fi
    echo "🚨 [Mirrormere Verify] Error: Neither oapi-codegen nor go found in PATH to perform code generation." >&2
    exit 1
}

run_codegen_drift() {
    echo "   [codegen] Verifying zero-drift for generated Go code..."
    ensure_oapi_codegen
    go generate ./...
    if has_cmd git; then
        if ! git diff --exit-code internal/api/; then
            echo "🚨 [Mirrormere Verify] Error: Generated code drift detected in internal/api/." >&2
            echo "Run 'go generate ./...' and commit the updated files." >&2
            exit 1
        fi
    fi
}

# 7. Client & Sidecar Runtime Tests (Node.js)
run_node_tests() {
    if has_cmd node; then
        echo "   [node test] Running web client and sidecar test suites..."
        TZ=UTC node --test web/test/*.test.js sidecars/eink-renderer/test/*.test.js
    fi
}

# 8. Python Client Tests (E-Ink, Voice & Remote)
run_python_tests() {
    if has_cmd python3; then
        echo "   [python test] Running e-ink node client test suite..."
        PYTHONPATH="client/eink-node" python3 -m unittest discover -s client/eink-node/tests -p "test_*.py"
        echo "   [python test] Running edge ear (voice) daemon test suite..."
        PYTHONPATH="." python3 -m unittest discover -s client/ear/tests -p "test_*.py"
        echo "   [python test] Running remote (bluetooth/chromecast) client test suite..."
        PYTHONPATH="client/remote" python3 -m unittest discover -s client/remote/tests -p "test_*.py"
    fi
}

# 9. React UI (web/ui): lint, typecheck, tests, build, committed-output drift.
# Runs only when web/ui or web/static/ui changed and web/ui/node_modules exists (npm ci done).
run_ui_checks() {
    if ! has_cmd npm; then
        echo "🚨 [Mirrormere Verify] Error: npm not found in PATH; web/ui checks cannot run." >&2
        exit 1
    fi
    echo "   [ui] Running React UI lint, typecheck, tests and build..."
    (cd "$REPO_ROOT/web/ui" && npm run lint && npm run typecheck && npm test && npm run build)
    echo "   [ui] Checking committed web/static/ui output against a fresh build..."
    git diff --exit-code web/static/ui
    untracked=$(git ls-files --others --exclude-standard web/static/ui)
    if [ -n "$untracked" ]; then
        echo "   [ui] Untracked build output in web/static/ui: $untracked"
        return 1
    fi
}

if [ "$MODE" = "staged" ]; then
    if has_cmd git; then
        git rm --cached -q PR_DESCRIPTION.md .pr_description.md 2>/dev/null || true
    fi
    STAGED_FILES=$(git diff --cached --name-only 2>/dev/null || true)
    if [ -z "$STAGED_FILES" ]; then
        echo "✅ [Mirrormere Verify] No staged files to verify."
        exit 0
    fi

    check_utf8_bom
    check_gofmt
    check_no_imperative_ddl

    if echo "$STAGED_FILES" | grep -q -E '\.go$|\.code-health-baseline\.json$'; then
        run_code_health
    fi

    if echo "$STAGED_FILES" | grep -q -E '^(api/|internal/api/)'; then
        run_codegen_drift
    fi

    if echo "$STAGED_FILES" | grep -q -E '\.go$|^go\.(mod|sum)$|^\.golangci\.yml$|^scripts/verify\.sh$'; then
        run_golangci_lint
        run_deadcode
    fi

    if echo "$STAGED_FILES" | grep -q -E '^go\.(mod|sum)$'; then
        run_coverage_gate
    elif echo "$STAGED_FILES" | grep -q '\.go$'; then
        pkgs=$(echo "$STAGED_FILES" | grep '\.go$' | grep -v '^scripts/code-health/' | while read -r f; do dirname "$f"; done | sort -u)
        for p in $pkgs; do
            if [ -d "$p" ]; then
                sh "$REPO_ROOT/scripts/check-coverage.sh" --check --service "$p"
            fi
        done
    fi

    if echo "$STAGED_FILES" | grep -q -E '^web/' && has_cmd node; then
        echo "   [node test] Running web client test suite..."
        TZ=UTC node --test web/test/*.test.js
    fi

    if echo "$STAGED_FILES" | grep -q -E '^web/(ui|static/ui)/' && [ -d "$REPO_ROOT/web/ui/node_modules" ]; then
        run_ui_checks
    fi

    if echo "$STAGED_FILES" | grep -q -E '^sidecars/eink-renderer/' && has_cmd node; then
        echo "   [node test] Running eink-renderer sidecar test suite..."
        TZ=UTC node --test sidecars/eink-renderer/test/*.test.js
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

if [ "$MODE" = "lint" ]; then
    check_utf8_bom
    check_gofmt
    check_no_imperative_ddl
    run_codegen_drift
    ensure_code_health
    ensure_deadcode

    log_health=$(mktemp)
    log_lint=$(mktemp)
    log_deadcode=$(mktemp)

    (run_code_health) > "$log_health" 2>&1 &
    p_health=$!

    (run_golangci_lint) > "$log_lint" 2>&1 &
    p_lint=$!

    (run_deadcode) > "$log_deadcode" 2>&1 &
    p_deadcode=$!

    rc_health=0
    wait "$p_health" || rc_health=$?

    rc_lint=0
    wait "$p_lint" || rc_lint=$?

    rc_deadcode=0
    wait "$p_deadcode" || rc_deadcode=$?

    cat "$log_health"
    cat "$log_lint"
    cat "$log_deadcode"
    rm -f "$log_health" "$log_lint" "$log_deadcode"

    if [ "$rc_health" -ne 0 ] || [ "$rc_lint" -ne 0 ] || [ "$rc_deadcode" -ne 0 ]; then
        exit 1
    fi

    echo "✨ [Mirrormere Verify] All lint and static analysis checks passed!"
    exit 0
fi

# Full verification: Run entire CI-equivalent verification suite
check_utf8_bom
check_gofmt
check_no_imperative_ddl
run_codegen_drift
run_code_health
run_golangci_lint
run_deadcode
run_coverage_gate
ui_changed=""
if [ -n "$(git status --porcelain -- web/ui web/static/ui 2>/dev/null)" ]; then
    ui_changed=1
fi
if git rev-parse --verify -q origin/main >/dev/null 2>&1; then
    base=$(git merge-base HEAD origin/main 2>/dev/null || true)
    if [ -n "$base" ] && [ -n "$(git diff --name-only "$base" HEAD -- web/ui web/static/ui 2>/dev/null)" ]; then
        ui_changed=1
    fi
fi
if [ -n "$ui_changed" ] && [ -d "$REPO_ROOT/web/ui/node_modules" ]; then
    run_ui_checks
fi

run_node_tests
run_python_tests

echo "✨ [Mirrormere Verify] All pre-flight checks passed successfully! Ready for commit/PR."

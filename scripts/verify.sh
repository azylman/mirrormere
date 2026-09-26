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

# 2. Go Vet
run_go_vet() {
    echo "   [go vet] Checking codebase..."
    if has_cmd go; then
        go vet ./...
    else
        echo "🚨 [Mirrormere Verify] Error: go not found in PATH." >&2
        exit 1
    fi
}

# 3. GolangCI-Lint
run_golangci_lint() {
    echo "   [golangci-lint] Running strict linters..."
    if has_cmd golangci-lint; then
        golangci-lint run --allow-parallel-runners ./...
    elif [ -x "$(go env GOPATH 2>/dev/null)/bin/golangci-lint" ]; then
        "$(go env GOPATH)/bin/golangci-lint" run --allow-parallel-runners ./...
    elif [ -x "$HOME/go/bin/golangci-lint" ]; then
        "$HOME/go/bin/golangci-lint" run --allow-parallel-runners ./...
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
        go install golang.org/x/tools/cmd/deadcode@v0.30.0
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
    echo "   [deadcode] Checking for unreachable code..."
    ensure_deadcode
    dead_output=$("$DEADCODE_BIN" -test ./... 2>&1) || {
        echo "🚨 [Mirrormere Verify] Error: deadcode execution failed:" >&2
        echo "$dead_output" >&2
        exit 1
    }
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

# 6. Codegen Zero-Drift Gate (SPEC-001 §2.3)
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

# 8. E-Ink Display Node Client Tests (Python)
run_python_tests() {
    if has_cmd python3; then
        echo "   [python test] Running e-ink node client test suite..."
        PYTHONPATH="clients/eink-node" python3 -m unittest discover -s clients/eink-node/tests -p "test_*.py"
    fi
}

# Execute checks in pipeline order
check_utf8_bom
run_codegen_drift
run_go_vet
run_golangci_lint
run_deadcode
run_coverage_gate
run_node_tests
run_python_tests

echo "✨ [Mirrormere Verify] All pre-flight checks passed successfully! Ready for commit/PR."

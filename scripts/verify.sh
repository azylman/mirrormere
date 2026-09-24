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
        golangci-lint run ./...
    elif [ -x "$(go env GOPATH 2>/dev/null)/bin/golangci-lint" ]; then
        "$(go env GOPATH)/bin/golangci-lint" run ./...
    elif [ -x "$HOME/go/bin/golangci-lint" ]; then
        "$HOME/go/bin/golangci-lint" run ./...
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

# Execute checks in pipeline order
check_utf8_bom
run_go_vet
run_golangci_lint
run_deadcode
run_coverage_gate

echo "✨ [Mirrormere Verify] All pre-flight checks passed successfully! Ready for commit/PR."

#!/bin/sh
set -eu

# scripts/check-coverage.sh - Native Test Coverage & Threshold Gating Engine for Mirrormere
# Zero external SaaS dependencies. Statement-weighted Go coverage.

# Ensure MinGit / MSYS binaries (/usr/bin, /mingw64/bin, /cmd) are in PATH
for p in /usr/bin /mingw64/bin /cmd; do
    if [ -d "$p" ]; then
        case ":$PATH:" in
            *:"$p":*) ;;
            *) PATH="$p:$PATH" ;;
        esac
    fi
done

CHECK_MODE=0
SUMMARY_MODE=0
GAPS_MODE=0
COVERAGE_THRESHOLD=95.0
CUSTOM_PROFILE_DIR=""

usage() {
    cat <<EOF
Usage: $0 [options]

Options:
  --check               Exit with status 1 if any coverage threshold is violated (floor: ${COVERAGE_THRESHOLD}%)
  --summary             Output Markdown summary table (also written to \$GITHUB_STEP_SUMMARY if set)
  --gaps                Print top uncovered functions and statement deficit for below-target packages
  --profile-dir <dir>   Store intermediate coverage profiles in the specified directory
  -h, --help            Show this help message
EOF
    exit 0
}

while [ $# -gt 0 ]; do
    case "$1" in
        --check)
            CHECK_MODE=1
            shift
            ;;
        --summary)
            SUMMARY_MODE=1
            shift
            ;;
        --gaps)
            GAPS_MODE=1
            shift
            ;;
        --profile-dir)
            CUSTOM_PROFILE_DIR="$2"
            shift 2
            ;;
        -h|--help)
            usage
            ;;
        *)
            echo "Unknown argument: $1" >&2
            usage
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

TEMP_CREATED=0
if [ -n "$CUSTOM_PROFILE_DIR" ]; then
    PROF_DIR="$CUSTOM_PROFILE_DIR"
    mkdir -p "$PROF_DIR"
else
    WIN_ROOT="$(pwd -W 2>/dev/null || true)"
    if [ -n "$WIN_ROOT" ]; then
        PROF_DIR="${WIN_ROOT}/.mirrormere-coverage-$$.tmp"
        mkdir -p "$PROF_DIR"
    else
        PROF_DIR=$(mktemp -d /tmp/mirrormere-coverage.XXXXXX)
    fi
    TEMP_CREATED=1
fi

cleanup() {
    if [ "$TEMP_CREATED" -eq 1 ] && [ -d "$PROF_DIR" ]; then
        rm -rf "$PROF_DIR"
    fi
}
trap cleanup EXIT INT TERM

# ANSI Colors
CYAN='\033[0;36m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
BOLD='\033[1m'
NC='\033[0m'

echo "${CYAN}⚡ [Mirrormere Coverage] Starting coverage measurement...${NC}"

PKG_DATA_FILE="${PROF_DIR}/pkg_summary.txt"
: > "$PKG_DATA_FILE"

TOTAL_GO_STMTS=0
COVERED_GO_STMTS=0
VIOLATIONS=""

record_violation() {
    msg="$1"
    if [ -z "$VIOLATIONS" ]; then
        VIOLATIONS="$msg"
    else
        VIOLATIONS="${VIOLATIONS}
$msg"
    fi
}

cgo_val="${CGO_ENABLED:-0}"

GO_TEST_PREFIX=""
if [ "$(id -u)" -eq 0 ] && command -v setpriv >/dev/null 2>&1 && id -u ubuntu >/dev/null 2>&1; then
    if command -v python3 >/dev/null 2>&1; then
        if ! python3 -c "import ctypes; libc=ctypes.CDLL('libc.so.6', use_errno=True); fds=[libc.inotify_init() for _ in range(10)]; exit(0 if all(f >= 0 for f in fds) else 1)" 2>/dev/null; then
            GO_TEST_PREFIX="setpriv --reuid=1000 --regid=1000 --clear-groups env HOME=/tmp PATH=$PATH"
            chmod 777 "$PROF_DIR" 2>/dev/null || true
        fi
    fi
fi

# Discover all Go packages with source files
PACKAGES=$(go list ./... 2>/dev/null || true)

idx=0
for pkg in $PACKAGES; do
    idx=$((idx + 1))
    prof="${PROF_DIR}/cov_${idx}.out"
    pkg_rel=$(echo "$pkg" | sed "s|^github.com/azylman/mirrormere/\?||")
    if [ -z "$pkg_rel" ]; then
        pkg_rel="."
    fi

    # Run tests with coverage profile
    if $GO_TEST_PREFIX env CGO_ENABLED="$cgo_val" go test -coverprofile="$prof" "$pkg" >/dev/null 2>&1; then
        if [ -f "$prof" ] && [ -s "$prof" ]; then
            # Parse statements from profile
            cov_stats=$(awk 'NR>1 { total += $2; if ($3 > 0) covered += $2 } END { printf "%d %d\n", total, covered }' "$prof")
            tot_stmt=$(echo "$cov_stats" | cut -d' ' -f1)
            cov_stmt=$(echo "$cov_stats" | cut -d' ' -f2)

            if [ "$tot_stmt" -gt 0 ]; then
                pct=$(awk "BEGIN { printf \"%.2f\", ($cov_stmt / $tot_stmt) * 100 }")
                echo "$pkg_rel $tot_stmt $cov_stmt $pct" >> "$PKG_DATA_FILE"
                TOTAL_GO_STMTS=$((TOTAL_GO_STMTS + tot_stmt))
                COVERED_GO_STMTS=$((COVERED_GO_STMTS + cov_stmt))

                below=$(awk "BEGIN { print ($pct < $COVERAGE_THRESHOLD) ? 1 : 0 }")
                if [ "$below" -eq 1 ]; then
                    record_violation "Package '$pkg_rel' coverage ${pct}% is below required ${COVERAGE_THRESHOLD}%"
                fi
            else
                echo "$pkg_rel 0 0 100.00" >> "$PKG_DATA_FILE"
            fi
        else
            echo "$pkg_rel 0 0 100.00" >> "$PKG_DATA_FILE"
        fi
    else
        record_violation "Package '$pkg_rel' tests failed"
    fi
done

GLOBAL_PCT="100.00"
if [ "$TOTAL_GO_STMTS" -gt 0 ]; then
    GLOBAL_PCT=$(awk "BEGIN { printf \"%.2f\", ($COVERED_GO_STMTS / $TOTAL_GO_STMTS) * 100 }")
fi

echo ""
echo "${BOLD}================================================================${NC}"
echo "${BOLD}               MIRRORMERE TEST COVERAGE SUMMARY                ${NC}"
echo "${BOLD}================================================================${NC}"
printf "%-35s %-12s %-12s %-10s\n" "Package" "Statements" "Covered" "Coverage"
echo "----------------------------------------------------------------"

while read -r pline; do
    [ -z "$pline" ] && continue
    p_name=$(echo "$pline" | cut -d' ' -f1)
    p_tot=$(echo "$pline" | cut -d' ' -f2)
    p_cov=$(echo "$pline" | cut -d' ' -f3)
    p_pct=$(echo "$pline" | cut -d' ' -f4)

    is_below=$(awk "BEGIN { print ($p_pct < $COVERAGE_THRESHOLD) ? 1 : 0 }")
    if [ "$is_below" -eq 1 ]; then
        printf "%-35s %-12s %-12s ${RED}%-10s${NC}\n" "$p_name" "$p_tot" "$p_cov" "${p_pct}%"
    else
        printf "%-35s %-12s %-12s ${GREEN}%-10s${NC}\n" "$p_name" "$p_tot" "$p_cov" "${p_pct}%"
    fi
done < "$PKG_DATA_FILE"

echo "----------------------------------------------------------------"
global_below=$(awk "BEGIN { print ($GLOBAL_PCT < $COVERAGE_THRESHOLD) ? 1 : 0 }")
if [ "$global_below" -eq 1 ]; then
    printf "${BOLD}%-35s %-12s %-12s ${RED}%-10s${NC}\n" "TOTAL" "$TOTAL_GO_STMTS" "$COVERED_GO_STMTS" "${GLOBAL_PCT}%"
    record_violation "Global statement coverage ${GLOBAL_PCT}% is below required ${COVERAGE_THRESHOLD}%"
else
    printf "${BOLD}%-35s %-12s %-12s ${GREEN}%-10s${NC}\n" "TOTAL" "$TOTAL_GO_STMTS" "$COVERED_GO_STMTS" "${GLOBAL_PCT}%"
fi
echo "${BOLD}================================================================${NC}"
echo ""

# Write GITHUB_STEP_SUMMARY if available
if [ "$SUMMARY_MODE" -eq 1 ] || [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
    MD_OUT=""
    MD_OUT="${MD_OUT}### 📊 Mirrormere Statement Test Coverage\n\n"
    MD_OUT="${MD_OUT}| Package | Statements | Covered | Coverage | Status |\n"
    MD_OUT="${MD_OUT}|:---|:---:|:---:|:---:|:---:|\n"

    while read -r pline; do
        [ -z "$pline" ] && continue
        p_name=$(echo "$pline" | cut -d' ' -f1)
        p_tot=$(echo "$pline" | cut -d' ' -f2)
        p_cov=$(echo "$pline" | cut -d' ' -f3)
        p_pct=$(echo "$pline" | cut -d' ' -f4)
        is_below=$(awk "BEGIN { print ($p_pct < $COVERAGE_THRESHOLD) ? 1 : 0 }")
        if [ "$is_below" -eq 1 ]; then
            p_status="❌ Below ${COVERAGE_THRESHOLD}%"
        else
            p_status="✅ Pass"
        fi
        MD_OUT="${MD_OUT}| \`${p_name}\` | ${p_tot} | ${p_cov} | **${p_pct}%** | ${p_status} |\n"
    done < "$PKG_DATA_FILE"

    if [ "$global_below" -eq 1 ]; then
        tot_status="❌ Below ${COVERAGE_THRESHOLD}%"
    else
        tot_status="✅ Pass"
    fi
    MD_OUT="${MD_OUT}| **Total** | **${TOTAL_GO_STMTS}** | **${COVERED_GO_STMTS}** | **${GLOBAL_PCT}%** | **${tot_status}** |\n"

    if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
        printf "%b" "$MD_OUT" >> "$GITHUB_STEP_SUMMARY"
    fi
    if [ "$SUMMARY_MODE" -eq 1 ]; then
        printf "%b" "$MD_OUT"
    fi
fi

if [ -n "$VIOLATIONS" ]; then
    echo "${RED}🚨 [Mirrormere Coverage] Violations detected:${NC}"
    echo "$VIOLATIONS" | while IFS= read -r line; do
        echo "   • $line"
    done
    if [ "$CHECK_MODE" -eq 1 ]; then
        exit 1
    fi
else
    echo "${GREEN}✅ [Mirrormere Coverage] All packages meet or exceed the ${COVERAGE_THRESHOLD}% coverage floor!${NC}"
fi

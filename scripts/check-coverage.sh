#!/bin/sh
set -eu

# scripts/check-coverage.sh - Native Test Coverage & Threshold Gating Engine for Mirrormere
# Zero external SaaS dependencies. Parallel statement-weighted Go coverage.

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
TARGET_SERVICE=""
CUSTOM_PROFILE_DIR=""

usage() {
    cat <<EOF
Usage: $0 [options]

Options:
  --check               Exit with status 1 if any coverage threshold is violated (floor: ${COVERAGE_THRESHOLD}%)
  --summary             Output Markdown summary table (also written to \$GITHUB_STEP_SUMMARY if set)
  --gaps                Print top uncovered functions and statement deficit for below-target packages
  --service <name>      Target a specific Go package, service, or web/ui by path or basename (e.g., web, internal/server, web/ui)
  --package <name>      Alias for --service
  --profile-dir <dir>   Store intermediate coverage profiles in the specified directory
  --parallel <n>        Parallel package test limit (accepted for CLI compatibility)
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
        --service|--package)
            TARGET_SERVICE="$2"
            shift 2
            ;;
        --profile-dir)
            CUSTOM_PROFILE_DIR="$2"
            shift 2
            ;;
        --parallel)
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

cgo_val="${CGO_ENABLED:-}"
if [ -z "$cgo_val" ]; then
    if command -v gcc >/dev/null 2>&1 || command -v clang >/dev/null 2>&1; then
        cgo_val=1
    else
        cgo_val=0
    fi
fi

race_flag=""
if [ "$cgo_val" = "1" ]; then
    race_flag="-race"
fi

GO_TEST_PREFIX=""
if [ "$(id -u)" -eq 0 ] && command -v setpriv >/dev/null 2>&1 && id -u ubuntu >/dev/null 2>&1; then
    if command -v python3 >/dev/null 2>&1; then
        if ! python3 -c "import ctypes; libc=ctypes.CDLL('libc.so.6', use_errno=True); fds=[libc.inotify_init() for _ in range(50)]; exit(0 if all(f >= 0 for f in fds) else 1)" 2>/dev/null; then
            GO_TEST_PREFIX="setpriv --reuid=1000 --regid=1000 --clear-groups env HOME=/tmp PATH=$PATH"
            chmod 777 "$PROF_DIR" 2>/dev/null || true
        fi
    fi
fi

ALL_PACKAGES=$(go list ./... 2>/dev/null || true)
if [ "$TARGET_SERVICE" = "web/ui" ] || [ "$TARGET_SERVICE" = "ui" ]; then
    echo "${CYAN}⚡ [Mirrormere Coverage] Running React frontend Vitest coverage (web/ui)...${NC}"
    if [ ! -d "$REPO_ROOT/web/ui/node_modules" ]; then
        echo "   [ui] Installing dependencies in web/ui..."
        (cd "$REPO_ROOT/web/ui" && npm ci --no-audit --no-fund)
    fi
    if ! (cd "$REPO_ROOT/web/ui" && npm run test:coverage); then
        record_violation "Frontend coverage in web/ui failed to meet threshold gates (95/90/95/95)"
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
        echo "${GREEN}✅ [Mirrormere Coverage] web/ui Vitest coverage passed!${NC}"
    fi
    exit 0
fi

if [ -n "$TARGET_SERVICE" ]; then
    clean_target="${TARGET_SERVICE#./}"
    if [ "$clean_target" = "." ]; then
        PACKAGES=$(go list . 2>/dev/null || true)
    elif [ -d "$clean_target" ]; then
        PACKAGES=$(go list "./${clean_target}/..." 2>/dev/null || true)
    else
        PACKAGES=$(echo "$ALL_PACKAGES" | awk -v tgt="$clean_target" '
            $0 == tgt || $0 ~ "/" tgt "$" || $0 ~ "/" tgt "/" { print $0 }
        ')
    fi
    if [ -z "$PACKAGES" ]; then
        echo "🚨 [Mirrormere Coverage] Unknown target package or service: $TARGET_SERVICE" >&2
        exit 1
    fi
else
    PACKAGES="$ALL_PACKAGES"
fi

COMBINED_PROF="${PROF_DIR}/coverage.out"
TEST_LOG="${PROF_DIR}/test.log"

# Run all targeted packages in a single parallel Go invocation
# shellcheck disable=SC2086
if $GO_TEST_PREFIX env CGO_ENABLED="$cgo_val" go test ${race_flag:+"$race_flag"} -timeout "${MIRRORMERE_TEST_TIMEOUT:-180s}" -coverprofile="$COMBINED_PROF" $PACKAGES >"$TEST_LOG" 2>&1; then
    for pkg in $PACKAGES; do
        pkg_rel=$(echo "$pkg" | sed "s|^github.com/azylman/mirrormere/\?||")
        if [ -z "$pkg_rel" ]; then
            pkg_rel="."
        fi

        tot_stmt=0
        cov_stmt=0
        if [ -f "$COMBINED_PROF" ] && [ -s "$COMBINED_PROF" ]; then
            cov_stats=$(awk -v target_pkg="$pkg" '
                NR > 1 && NF >= 3 {
                    file = $1
                    sub(/:[0-9]+\.[0-9]+,[0-9]+\.[0-9]+$/, "", file)
                    sub(/\/[^\/]+$/, "", file)
                    if (file == target_pkg) {
                        stmts = $2 + 0
                        cnt = $3 + 0
                        total += stmts
                        if (cnt > 0) covered += stmts
                    }
                }
                END { printf "%d %d\n", total + 0, covered + 0 }
            ' "$COMBINED_PROF")
            tot_stmt=$(echo "$cov_stats" | cut -d' ' -f1)
            cov_stmt=$(echo "$cov_stats" | cut -d' ' -f2)
        fi

        if [ "$tot_stmt" -gt 0 ]; then
            pct=$(awk "BEGIN { printf \"%.2f\", ($cov_stmt / $tot_stmt) * 100 }")
            echo "$pkg_rel $tot_stmt $cov_stmt $pct" >> "$PKG_DATA_FILE"
            TOTAL_GO_STMTS=$((TOTAL_GO_STMTS + tot_stmt))
            COVERED_GO_STMTS=$((COVERED_GO_STMTS + cov_stmt))

            below=$(awk "BEGIN { print ($pct < $COVERAGE_THRESHOLD) ? 1 : 0 }")
            if [ "$below" -eq 1 ]; then
                record_violation "Package '$pkg_rel' coverage ${pct}% is below required ${COVERAGE_THRESHOLD}%"
            fi

            if [ "$below" -eq 1 ] || [ "$GAPS_MODE" -eq 1 ]; then
                needed=$(awk "BEGIN { req = int(($tot_stmt * $COVERAGE_THRESHOLD + 99) / 100); diff = req - $cov_stmt; print (diff > 0) ? diff : 0 }")
                if [ "$below" -eq 1 ] || [ "$needed" -gt 0 ]; then
                    echo "   🔍 [Coverage Audit] $pkg_rel: $cov_stmt / $tot_stmt stmts (${pct}%). Deficit: $needed stmts to reach ${COVERAGE_THRESHOLD}%."
                    pkg_prof="${PROF_DIR}/pkg_gap.out"
                    awk -v target_pkg="$pkg" '
                        NR == 1 { print; next }
                        NF >= 3 {
                            file = $1
                            sub(/:[0-9]+\.[0-9]+,[0-9]+\.[0-9]+$/, "", file)
                            sub(/\/[^\/]+$/, "", file)
                            if (file == target_pkg) print
                        }
                    ' "$COMBINED_PROF" > "$pkg_prof"
                    if [ -s "$pkg_prof" ] && command -v go >/dev/null 2>&1; then
                        echo "      Top uncovered functions in $pkg_rel:"
                        go tool cover -func="$pkg_prof" 2>/dev/null | awk '$1 !~ /^total:/ && $NF != "100.0%"' | sort -k3 -n | head -n 5 | sed 's/^/      • /' || true
                    fi
                    if [ -s "$pkg_prof" ]; then
                        awk '
                            !/^mode:/ && $(NF-1)+0 > 0 && $NF+0 == 0 {
                                col_idx = match($1, /:[0-9]+\.[0-9]+,[0-9]+\.[0-9]+$/)
                                if (col_idx > 0) {
                                    path = substr($1, 1, col_idx - 1)
                                    sub(/.*[\/\\]/, "", path)
                                    range_str = substr($1, col_idx + 1)
                                    split(range_str, b, ",")
                                    split(b[1], s, ".")
                                    split(b[2], e, ".")
                                    print path ":" (s[1]+0) ":" (e[1]+0)
                                }
                            }
                        ' "$pkg_prof" | sort -t: -k1,1 -k2,2n -k3,3n | awk -F: -v pkg="$pkg_rel" '
                            function flush_range() {
                                if (cur_file != "") {
                                    rng = sprintf("L%d-L%d", prev_start, prev_end)
                                    if (file_ranges[cur_file] == "") {
                                        file_ranges[cur_file] = rng
                                    } else {
                                        file_ranges[cur_file] = file_ranges[cur_file] ", " rng
                                    }
                                }
                            }
                            {
                                f = $1; s = $2 + 0; e = $3 + 0
                                if (f != cur_file) {
                                    flush_range()
                                    cur_file = f
                                    prev_start = s
                                    prev_end = e
                                    if (!seen[f]++) {
                                        files[++file_count] = f
                                    }
                                } else {
                                    if (s <= prev_end + 1) {
                                        if (e > prev_end) prev_end = e
                                    } else {
                                        flush_range()
                                        prev_start = s
                                        prev_end = e
                                    }
                                }
                            }
                            END {
                                flush_range()
                                if (file_count > 0) {
                                    printf("      Uncovered line ranges in %s:\n", pkg)
                                    limit = (file_count > 10) ? 10 : file_count
                                    for (i = 1; i <= limit; i++) {
                                        fn = files[i]
                                        printf("      • %s: %s\n", fn, file_ranges[fn])
                                    }
                                }
                            }
                        '
                    fi
                fi
            fi
        else
            echo "$pkg_rel 0 0 100.00" >> "$PKG_DATA_FILE"
        fi
    done
else
    cat "$TEST_LOG" >&2 || true
    record_violation "Go package unit tests failed"
fi

if [ -z "$TARGET_SERVICE" ] && [ -d "$REPO_ROOT/web/ui" ] && command -v npm >/dev/null 2>&1; then
    echo ""
    echo "${CYAN}⚡ [Mirrormere Coverage] Running React frontend Vitest coverage (web/ui)...${NC}"
    if [ ! -d "$REPO_ROOT/web/ui/node_modules" ]; then
        echo "   [ui] Installing dependencies in web/ui..."
        (cd "$REPO_ROOT/web/ui" && npm ci --no-audit --no-fund)
    fi
    if ! (cd "$REPO_ROOT/web/ui" && npm run test:coverage); then
        record_violation "Frontend coverage in web/ui failed to meet threshold gates (95/90/95/95)"
    else
        echo "${GREEN}✅ [Mirrormere Coverage] web/ui Vitest coverage passed!${NC}"
    fi
fi

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

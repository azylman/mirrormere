# Architecture Decision Record: Layout Solver Exact-Cover Anchoring and Shape Grouping

- **Date:** 2026-09-25
- **Status:** Accepted
- **Context:** Resolving Issue #180 on `azylman/mirrormere` (`perf(layout): solver search is exponential on untileable configs; reload can stall for seconds to minutes`).

---

## 1. Exact-Cover Cell Anchoring on the 6×2 Grid Canvas

### Description of Issue / Inconsistency
In the original 6×2 bin-packing solver (PR #178), `placeUnpinned` placed unpinned widgets sequentially by testing each widget at every legal coordinate `(col, row)` across every screen `s \in [0, K-1]`.
When configurations satisfied the total area equality ($A_{\text{unpinned}} = K \times (12 - A_{\text{pinned}})$) but were geometrically untileable (e.g. $6\times [5,1] + 9\times [2,1]$ with area 48, $K=4$), the search walked all placement permutations of partial tilings, visiting over 117 million search nodes and stalling for $>20$ seconds on x86-64 (and minutes on low-power ARM devices like Raspberry Pi 4B). Under SPEC-012, this stalled the daemon configuration reload pipeline under `reloadMu`.

### Decision Made
Refactored `internal/layout/solver.go` to use exact-cover cell anchoring:
- At each recursive step, select the lowest-index screen $s$ where `screenMasks[s] != FullGridMask`.
- Select the lowest-index empty cell on screen $s$:
  `emptyCell := bits.TrailingZeros16(^screenMasks[s])`
  `col := emptyCell % 6, row := emptyCell / 6`.
- **Top-Left Origin Theorem**: Because all cells prior to `(col, row)` in row-major reading order ($idx = r \cdot 6 + c$) are already occupied, any candidate rectangular widget covering `(col, row)` MUST have its top-left corner placed strictly at `(col, row)`. Any placement with $r_0 < r$ or ($r_0 = r \land c_0 < c$) would overlap previously placed widgets, and any placement with $r_0 > r$ or $c_0 > c$ would leave `(col, row)` uncovered.
- Origin coordinate loops are eliminated: origin is strictly anchored to `(col, row)`.

### Technical Rationale & References
- Reduces branching factor from $K \times (\text{maxCol}) \times (\text{maxRow})$ to $1$ (strictly anchored).
- Guarantees each distinct tiling of Screen 0 is generated exactly once before Screen 1 is considered.
- References: Knuth's Algorithm X / Exact Cover, SPEC-005 §Algorithmic Implementation, Issue #180.

---

## 2. Shape Grouping & Post-Solve Deterministic Assignment

### Description of Issue / Inconsistency
When users declare multiple instances of the same widget dimensions (e.g. ten $[5, 1]$ weather banners or five $[2, 1]$ sensor widgets), treating each instance as a distinct search entity causes an $M!$ factorial permutation explosion. Swapping identical tiles produces identical grid occupancies, all of which were explored redundantly.

### Decision Made
- Group unpinned widgets by dimension shape $[cols, rows]$ into $\le 12$ distinct shape classes.
- Track active remaining counts per shape (`shapeGroup.count`).
- Recursion branches only over available shape groups ($count > 0$).
- Sort shape groups deterministically: area descending, rows descending.
- Sort widget instances within each shape group deterministically by `ID asc`.
- On successful solve, assign widget instances deterministically to placed slots in stable input order.

### Technical Rationale & References
- Completely eliminates $M!$ permutation trees for identical dimensions.
- Combines with exact-cover anchoring to reduce search node counts on untileable deadlocks from 117,655,029 down to 14 nodes.
- References: SPEC-005 §Fewest Number of Screens with Pinned Budgeting, Issue #180.

---

## 3. Search Node Budget Ceiling (`SolveWithBudget`)

### Description of Issue / Inconsistency
Under SPEC-012, live reload must never hang or stall the daemon process, even on adversarial layout configurations.

### Decision Made
- Added `defaultMaxSearchNodes = 100_000` ceiling in `SolveWithBudget`.
- Bounded worst-case search execution on a 1.5 GHz Raspberry Pi 4B to $< 2\text{ms}$.
- Exceeding the search budget aborts cleanly and returns the canonical SPEC-005 geometric deadlock diagnostic:
  `[Mirrormere Config Error] Invalid widget layout configuration:\n  Widgets satisfy total cell count but cannot geometrically tile the 6x2 grid without gaps or overlaps...`

### Technical Rationale & References
- Enforces daemon reliability SLA without introducing arbitrary wall-clock timers or background threads.
- Enables hermetic white-box testing of budget abort paths in `internal/layout/solver_test.go` (`TestSolve_NodeBudgetCeiling`) with 100.0% statement coverage.
- References: SPEC-012 §Live Configuration Reload, SPEC-005 §Config Load Diagnostics & Deficit Guidance.

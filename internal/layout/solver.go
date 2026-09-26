package layout

import (
	"fmt"
	"math/bits"
	"sort"
	"strings"

	"github.com/azylman/mirrormere/internal/domain"
)

const (
	// GridColumns is the fixed horizontal cell count of the Mirrormere canvas.
	GridColumns = 6

	// GridRows is the fixed vertical cell count of the Mirrormere canvas below the header.
	GridRows = 2

	// GridTotalCells is the total discrete cells on a single 6x2 grid screen.
	GridTotalCells = GridColumns * GridRows

	// FullGridMask is the 12-bit mask representing 100% cell coverage (0x0FFF = 4095).
	FullGridMask uint16 = (1 << GridTotalCells) - 1
)

// WidgetInput represents a declared widget instance to be packed by the 6x2 layout solver.
type WidgetInput struct {
	ID         string           `json:"id"`
	Type       string           `json:"type,omitempty"`
	Dimensions domain.Dimension `json:"dimensions"`
	Pinned     bool             `json:"pinned,omitempty"`
}

// PlacedWidget represents a placed widget instance with its origin and dimensions on the 6x2 grid.
type PlacedWidget struct {
	WidgetID   string           `json:"widget_id"`
	Type       string           `json:"type,omitempty"`
	Origin     [2]int           `json:"origin"`     // [col, row]
	Dimensions domain.Dimension `json:"dimensions"` // [cols, rows]
	Pinned     bool             `json:"pinned,omitempty"`
}

// Col returns the 0-indexed column coordinate of the widget origin.
func (pw PlacedWidget) Col() int { return pw.Origin[0] }

// Row returns the 0-indexed row coordinate of the widget origin.
func (pw PlacedWidget) Row() int { return pw.Origin[1] }

// Cols returns the horizontal footprint in discrete grid columns.
func (pw PlacedWidget) Cols() int { return pw.Dimensions.Cols }

// Rows returns the vertical footprint in discrete grid rows.
func (pw PlacedWidget) Rows() int { return pw.Dimensions.Rows }

// Screen represents one 6x2 grid screen containing a set of non-overlapping, fully tiled widgets.
type Screen struct {
	Index   int            `json:"index"`
	Bitmask uint16         `json:"bitmask"`
	Widgets []PlacedWidget `json:"widgets"`
}

// Layout encapsulates the complete solved rotation layout across all computed screens.
type Layout struct {
	TotalScreens int      `json:"total_screens"`
	Screens      []Screen `json:"screens"`
}

// CellIndex returns the 1D discrete cell index (0..11) for coordinates (col, row).
func CellIndex(col, row int) int {
	return row*GridColumns + col
}

// WidgetBitmask computes the 12-bit integer bitmask for a widget of size (cols, rows) at origin (originCol, originRow).
// It returns an error if the widget bounds exceed the 6x2 grid.
func WidgetBitmask(cols, rows, originCol, originRow int) (uint16, error) {
	if cols < 1 || cols > GridColumns || rows < 1 || rows > GridRows {
		return 0, fmt.Errorf("invalid dimensions [%d, %d]: cols must be 1..6, rows must be 1..2", cols, rows)
	}
	if originCol < 0 || originCol+cols > GridColumns || originRow < 0 || originRow+rows > GridRows {
		return 0, fmt.Errorf("widget [%d, %d] at origin [%d, %d] exceeds 6x2 grid bounds", cols, rows, originCol, originRow)
	}
	return ComputeWidgetBitmask(cols, rows, originCol, originRow), nil
}

// ComputeWidgetBitmask computes the bitmask assuming bounds have already been validated.
func ComputeWidgetBitmask(cols, rows, originCol, originRow int) uint16 {
	rowMask := uint16(((1 << cols) - 1) << originCol)
	if rows == 1 {
		return rowMask << (originRow * GridColumns)
	}
	return rowMask | (rowMask << GridColumns)
}

// ComputeScreens calculates the minimal number of rotation screens K and cell deficit.
// Math per SPEC-005:
// - Pinned widgets occupy A_pinned cells on every screen.
// - Each screen provides (12 - A_pinned) unpinned cells.
// - K = ceil(A_unpinned / (12 - A_pinned)).
func ComputeScreens(pinnedArea, unpinnedArea int) (int, int, error) {
	if pinnedArea < 0 || unpinnedArea < 0 {
		return 0, 0, fmt.Errorf("widget areas must be non-negative: pinned=%d, unpinned=%d", pinnedArea, unpinnedArea)
	}

	if pinnedArea > GridTotalCells {
		return 0, 0, fmt.Errorf("[Mirrormere Config Error] Invalid widget layout configuration:\n  Pinned widgets occupy %d cells, exceeding single-screen limit of 12 cells.", pinnedArea)
	}

	if pinnedArea == GridTotalCells {
		if unpinnedArea > 0 {
			return 0, 0, fmt.Errorf("[Mirrormere Config Error] Invalid widget layout configuration:\n  Pinned widgets occupy all 12 cells, leaving no screen space for %d unpinned cells.", unpinnedArea)
		}
		return 1, 0, nil
	}

	capacityPerScreen := GridTotalCells - pinnedArea
	if unpinnedArea == 0 {
		return 1, capacityPerScreen, nil
	}

	k := (unpinnedArea + capacityPerScreen - 1) / capacityPerScreen
	targetUnpinned := k * capacityPerScreen
	deficit := targetUnpinned - unpinnedArea
	return k, deficit, nil
}

// FormatDeficitError formats layout configuration errors per SPEC-005 §Config Load Diagnostics & Deficit Guidance.
func FormatDeficitError(totalArea, targetScreens, deficit int) error {
	var b strings.Builder
	fmt.Fprintf(&b, "[Mirrormere Config Error] Invalid widget layout configuration:\n")
	fmt.Fprintf(&b, "  Total widget area is %d cells.\n", totalArea)
	fmt.Fprintf(&b, "  A 6x2 grid requires screen areas in multiples of 12 (Target: %d screens = %d cells).\n", targetScreens, targetScreens*GridTotalCells)
	fmt.Fprintf(&b, "  Deficit: %d cells needed to achieve 100%% fully-filled screens.\n", deficit)
	fmt.Fprintf(&b, "  Suggestions:\n")

	suggestions := buildSuggestions(deficit)
	for _, s := range suggestions {
		fmt.Fprintf(&b, "    - %s\n", s)
	}

	return fmt.Errorf("%s", strings.TrimRight(b.String(), "\n"))
}

func buildSuggestions(deficit int) []string {
	var suggestions []string

	// 1. Concrete widget addition suggestion
	addition := decomposeDeficit(deficit)
	if addition != "" {
		suggestions = append(suggestions, addition)
	}

	// 2. Expansion suggestion
	suggestions = append(suggestions, "Or expand existing [2, 1] widgets to [3, 2] / [4, 2].")

	// 3. Spacer suggestion
	spacerDims := recommendedSpacerDimensions(deficit)
	suggestions = append(suggestions, fmt.Sprintf("Or insert native spacer tiles (e.g. { type: \"spacer\", dimensions: [%d, %d] }) to intentionally leave layout space open.", spacerDims[0], spacerDims[1]))

	return suggestions
}

func decomposeDeficit(deficit int) string {
	type widgetOption struct {
		dim  [2]int
		area int
	}
	options := []widgetOption{
		{dim: [2]int{4, 2}, area: 8},
		{dim: [2]int{3, 2}, area: 6},
		{dim: [2]int{2, 2}, area: 4},
		{dim: [2]int{3, 1}, area: 3},
		{dim: [2]int{2, 1}, area: 2},
		{dim: [2]int{1, 1}, area: 1},
	}

	if deficit == 12 {
		return "Add a [6, 2] widget (12 cells)."
	}

	remaining := deficit
	var parts []string
	for _, opt := range options {
		for remaining >= opt.area && opt.area > 0 {
			parts = append(parts, fmt.Sprintf("a [%d, %d] widget (%d cells)", opt.dim[0], opt.dim[1], opt.area))
			remaining -= opt.area
		}
	}

	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		return "Add " + parts[0] + "."
	}
	if len(parts) == 2 {
		return "Add " + parts[0] + " and " + parts[1] + "."
	}
	return "Add " + strings.Join(parts[:len(parts)-1], ", ") + ", and " + parts[len(parts)-1] + "."
}

func recommendedSpacerDimensions(deficit int) [2]int {
	switch deficit {
	case 1:
		return [2]int{1, 1}
	case 2:
		return [2]int{2, 1}
	case 3:
		return [2]int{3, 1}
	case 4:
		return [2]int{2, 2}
	case 6:
		return [2]int{3, 2}
	case 8:
		return [2]int{4, 2}
	case 10:
		return [2]int{2, 2}
	case 12:
		return [2]int{6, 2}
	default:
		if deficit <= 6 {
			return [2]int{deficit, 1}
		}
		return [2]int{2, 1}
	}
}

const defaultMaxSearchNodes = 100_000

type shapeGroup struct {
	dims    domain.Dimension
	count   int
	widgets []WidgetInput
}

type unpinnedPlacement struct {
	screen int
	origin [2]int
	widget WidgetInput
}

// Solve executes exact 2D recursive backtracking bitmask bin-packing for the 6x2 grid.
// It replicates pinned widgets across all K rotation screens at identical coordinates
// and tiles unpinned widgets across remaining cells, enforcing screen_bitmask == 0x0FFF.
func Solve(widgets []WidgetInput) (*Layout, error) {
	return SolveWithBudget(widgets, defaultMaxSearchNodes)
}

// SolveWithBudget executes the solver with an explicit search node ceiling.
// If the search exceeds maxSearchNodes, it aborts and returns a geometric deadlock error.
func SolveWithBudget(widgets []WidgetInput, maxSearchNodes int) (*Layout, error) {
	if len(widgets) == 0 {
		return nil, FormatDeficitError(0, 1, GridTotalCells)
	}

	seenIDs := make(map[string]int, len(widgets))
	var pinned, unpinned []WidgetInput
	var pinnedArea, unpinnedArea int

	for i, w := range widgets {
		trimmedID := strings.TrimSpace(w.ID)
		if trimmedID == "" {
			return nil, fmt.Errorf("widget at index %d has empty id", i)
		}
		if prevIdx, exists := seenIDs[trimmedID]; exists {
			return nil, fmt.Errorf("duplicate widget id '%s' found (indices %d and %d)", trimmedID, prevIdx, i)
		}
		seenIDs[trimmedID] = i

		if !w.Dimensions.IsValid() {
			return nil, fmt.Errorf("widget '%s' has invalid dimensions [%d, %d]: cols must be 1..6, rows must be 1..2", w.ID, w.Dimensions.Cols, w.Dimensions.Rows)
		}

		area := w.Dimensions.Area()
		if w.Pinned {
			pinned = append(pinned, w)
			pinnedArea += area
		} else {
			unpinned = append(unpinned, w)
			unpinnedArea += area
		}
	}

	k, deficit, err := ComputeScreens(pinnedArea, unpinnedArea)
	if err != nil {
		return nil, err
	}
	if deficit > 0 {
		effectiveTotalArea := (k * GridTotalCells) - deficit
		return nil, FormatDeficitError(effectiveTotalArea, k, deficit)
	}

	// Sort pinned widgets: area desc, rows desc, ID asc
	sort.SliceStable(pinned, func(i, j int) bool {
		if pinned[i].Dimensions.Area() != pinned[j].Dimensions.Area() {
			return pinned[i].Dimensions.Area() > pinned[j].Dimensions.Area()
		}
		if pinned[i].Dimensions.Rows != pinned[j].Dimensions.Rows {
			return pinned[i].Dimensions.Rows > pinned[j].Dimensions.Rows
		}
		return pinned[i].ID < pinned[j].ID
	})

	// Shape grouping for unpinned widgets: eliminates M! permutation explosions
	shapeMap := make(map[[2]int]*shapeGroup)
	var shapeGroups []*shapeGroup
	for _, w := range unpinned {
		key := [2]int{w.Dimensions.Cols, w.Dimensions.Rows}
		sg, exists := shapeMap[key]
		if !exists {
			sg = &shapeGroup{dims: w.Dimensions}
			shapeMap[key] = sg
			shapeGroups = append(shapeGroups, sg)
		}
		sg.count++
		sg.widgets = append(sg.widgets, w)
	}

	// Sort shape groups: area desc, rows desc
	sort.Slice(shapeGroups, func(i, j int) bool {
		if shapeGroups[i].dims.Area() != shapeGroups[j].dims.Area() {
			return shapeGroups[i].dims.Area() > shapeGroups[j].dims.Area()
		}
		return shapeGroups[i].dims.Rows > shapeGroups[j].dims.Rows
	})

	// Deterministic widget order within each shape group: ID asc
	for _, sg := range shapeGroups {
		sort.SliceStable(sg.widgets, func(i, j int) bool {
			return sg.widgets[i].ID < sg.widgets[j].ID
		})
	}

	screenMasks := make([]uint16, k)
	pinnedPlacements := make([]PlacedWidget, len(pinned))
	unpinnedPlacements := make([]unpinnedPlacement, len(unpinned))
	placedCount := 0
	nodeCount := 0

	var placePinned func(pIdx int) bool
	var placeUnpinnedExactCover func() bool

	placePinned = func(pIdx int) bool {
		if pIdx == len(pinned) {
			return placeUnpinnedExactCover()
		}

		pw := pinned[pIdx]
		w, h := pw.Dimensions.Cols, pw.Dimensions.Rows
		maxCol := GridColumns - w
		maxRow := GridRows - h

		for r := 0; r <= maxRow; r++ {
			for c := 0; c <= maxCol; c++ {
				mask := ComputeWidgetBitmask(w, h, c, r)
				if (screenMasks[0] & mask) == 0 {
					for s := 0; s < k; s++ {
						screenMasks[s] |= mask
					}
					pinnedPlacements[pIdx] = PlacedWidget{
						WidgetID:   pw.ID,
						Type:       pw.Type,
						Origin:     [2]int{c, r},
						Dimensions: pw.Dimensions,
						Pinned:     true,
					}

					if placePinned(pIdx + 1) {
						return true
					}

					for s := 0; s < k; s++ {
						screenMasks[s] &= ^mask
					}
				}
			}
		}
		return false
	}

	placeUnpinnedExactCover = func() bool {
		nodeCount++
		if maxSearchNodes > 0 && nodeCount > maxSearchNodes {
			return false
		}

		targetScreen := -1
		for s := 0; s < k; s++ {
			if screenMasks[s] != FullGridMask {
				targetScreen = s
				break
			}
		}
		if targetScreen == -1 {
			return true // All screens 100% full
		}

		sMask := screenMasks[targetScreen]
		emptyCell := bits.TrailingZeros16(^sMask)
		c := emptyCell % GridColumns
		r := emptyCell / GridColumns

		for _, sg := range shapeGroups {
			if sg.count == 0 {
				continue
			}
			w, h := sg.dims.Cols, sg.dims.Rows
			if c+w > GridColumns || r+h > GridRows {
				continue
			}
			mask := ComputeWidgetBitmask(w, h, c, r)
			if (sMask & mask) == 0 {
				screenMasks[targetScreen] |= mask
				sg.count--

				assignedWidget := sg.widgets[len(sg.widgets)-sg.count-1]
				unpinnedPlacements[placedCount] = unpinnedPlacement{
					screen: targetScreen,
					origin: [2]int{c, r},
					widget: assignedWidget,
				}
				placedCount++

				if placeUnpinnedExactCover() {
					return true
				}

				placedCount--
				sg.count++
				screenMasks[targetScreen] &= ^mask
			}
		}

		return false
	}

	if !placePinned(0) {
		return nil, fmt.Errorf("[Mirrormere Config Error] Invalid widget layout configuration:\n  Widgets satisfy total cell count but cannot geometrically tile the 6x2 grid without gaps or overlaps.\n  Suggestions:\n    - Adjust widget dimensions to fit the 6x2 grid columns (e.g. use standard widths 2, 3, 4, or 6).\n    - Insert native spacer tiles (e.g. { type: \"spacer\", dimensions: [2, 1] }) to pad irregular shapes.")
	}

	layout := &Layout{
		TotalScreens: k,
		Screens:      make([]Screen, k),
	}

	for s := 0; s < k; s++ {
		screenWidgets := make([]PlacedWidget, 0, len(pinned)+len(unpinned))
		screenWidgets = append(screenWidgets, pinnedPlacements...)
		for i := 0; i < placedCount; i++ {
			if unpinnedPlacements[i].screen == s {
				screenWidgets = append(screenWidgets, PlacedWidget{
					WidgetID:   unpinnedPlacements[i].widget.ID,
					Type:       unpinnedPlacements[i].widget.Type,
					Origin:     unpinnedPlacements[i].origin,
					Dimensions: unpinnedPlacements[i].widget.Dimensions,
					Pinned:     false,
				})
			}
		}

		// Sort widgets deterministically: row asc, col asc
		sort.Slice(screenWidgets, func(i, j int) bool {
			if screenWidgets[i].Row() != screenWidgets[j].Row() {
				return screenWidgets[i].Row() < screenWidgets[j].Row()
			}
			return screenWidgets[i].Col() < screenWidgets[j].Col()
		})

		layout.Screens[s] = Screen{
			Index:   s,
			Bitmask: screenMasks[s],
			Widgets: screenWidgets,
		}
	}

	return layout, nil
}

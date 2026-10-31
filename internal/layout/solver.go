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

func (pw PlacedWidget) Col() int  { return pw.Origin[0] }
func (pw PlacedWidget) Row() int  { return pw.Origin[1] }
func (pw PlacedWidget) Cols() int { return pw.Dimensions.Cols }
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

// ComputeWidgetBitmask computes the bitmask assuming bounds have already been validated on the default 6x2 grid.
func ComputeWidgetBitmask(cols, rows, originCol, originRow int) uint16 {
	rowMask := uint16(((1 << cols) - 1) << originCol)
	if rows == 1 {
		return rowMask << (originRow * GridColumns)
	}
	return rowMask | (rowMask << GridColumns)
}

func computeWidgetBitmaskForGrid(cols, rows, originCol, originRow, gridCols int) uint16 {
	if gridCols == GridColumns && rows <= 2 {
		return ComputeWidgetBitmask(cols, rows, originCol, originRow)
	}
	rowMask := uint16(((1 << cols) - 1) << originCol)
	var mask uint16
	for r := 0; r < rows; r++ {
		mask |= rowMask << ((originRow + r) * gridCols)
	}
	return mask
}

func normalizeGrid(gridCols, gridRows int) (int, int) {
	if gridCols == 2 && gridRows == 6 {
		return 2, 6
	}
	return GridColumns, GridRows
}

// ComputeScreens calculates the minimal number of rotation screens K and cell deficit.
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
	return k, (k * capacityPerScreen) - unpinnedArea, nil
}

// FormatDeficitError formats layout configuration errors Load Diagnostics & Deficit Guidance.
func FormatDeficitError(totalArea, targetScreens, deficit int) error {
	return formatGridDeficitError(totalArea, targetScreens, deficit, GridColumns, GridRows)
}

func formatDeficitErrorForGrid(totalArea, targetScreens, deficit, gridCols, gridRows int) error {
	if gridCols == GridColumns && gridRows == GridRows {
		return FormatDeficitError(totalArea, targetScreens, deficit)
	}
	return formatGridDeficitError(totalArea, targetScreens, deficit, gridCols, gridRows)
}

func formatGridDeficitError(totalArea, targetScreens, deficit, gridCols, gridRows int) error {
	portrait := gridCols == 2 && gridRows == 6
	var b strings.Builder
	fmt.Fprintf(&b, "[Mirrormere Config Error] Invalid widget layout configuration:\n  Total widget area is %d cells.\n  A %dx%d grid requires screen areas in multiples of 12 (Target: %d screens = %d cells).\n  Deficit: %d cells needed to achieve 100%% fully-filled screens.\n  Suggestions:\n", totalArea, gridCols, gridRows, targetScreens, targetScreens*GridTotalCells, deficit)
	for _, s := range buildSuggestions(deficit, portrait) {
		fmt.Fprintf(&b, "    - %s\n", s)
	}
	return fmt.Errorf("%s", strings.TrimRight(b.String(), "\n"))
}

func buildSuggestions(deficit int, portrait bool) []string {
	sp := recommendedSpacerDimensions(deficit, portrait)
	expandHint := "Or expand existing [2, 1] widgets to [3, 2] / [4, 2]."
	if portrait {
		expandHint = "Or expand existing [1, 2] widgets to [2, 3] / [2, 4]."
	}
	sugs := []string{
		expandHint,
		fmt.Sprintf("Or insert native spacer tiles (e.g. { type: \"spacer\", dimensions: [%d, %d] }) to intentionally leave layout space open.", sp[0], sp[1]),
	}
	if add := decomposeDeficit(deficit, portrait); add != "" {
		return append([]string{add}, sugs...)
	}
	return sugs
}

func decomposeDeficit(deficit int, portrait bool) string {
	if deficit == 12 {
		if portrait {
			return "Add a [2, 6] widget (12 cells)."
		}
		return "Add a [6, 2] widget (12 cells)."
	}
	var parts []string
	for _, opt := range []struct {
		dim  [2]int
		area int
	}{
		{[2]int{4, 2}, 8},
		{[2]int{3, 2}, 6},
		{[2]int{2, 2}, 4},
		{[2]int{3, 1}, 3},
		{[2]int{2, 1}, 2},
		{[2]int{1, 1}, 1},
	} {
		dim := opt.dim
		if portrait {
			dim = [2]int{opt.dim[1], opt.dim[0]}
		}
		for deficit >= opt.area && opt.area > 0 {
			parts = append(parts, fmt.Sprintf("a [%d, %d] widget (%d cells)", dim[0], dim[1], opt.area))
			deficit -= opt.area
		}
	}
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return "Add " + parts[0] + "."
	case 2:
		return "Add " + parts[0] + " and " + parts[1] + "."
	default:
		return "Add " + strings.Join(parts[:len(parts)-1], ", ") + ", and " + parts[len(parts)-1] + "."
	}
}

func recommendedSpacerDimensions(deficit int, portrait bool) [2]int {
	var dim [2]int
	switch deficit {
	case 1, 2, 3:
		dim = [2]int{deficit, 1}
	case 4, 10:
		dim = [2]int{2, 2}
	case 6:
		dim = [2]int{3, 2}
	case 8:
		dim = [2]int{4, 2}
	case 12:
		dim = [2]int{6, 2}
	default:
		if deficit <= 6 {
			dim = [2]int{deficit, 1}
		} else {
			dim = [2]int{2, 1}
		}
	}
	if portrait {
		return [2]int{dim[1], dim[0]}
	}
	return dim
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

// Solve executes exact 2D recursive backtracking bitmask bin-packing for the default 6x2 grid.
func Solve(widgets []WidgetInput) (*Layout, error) {
	return SolveWithBudget(widgets, defaultMaxSearchNodes)
}

// SolveWithBudget executes the solver with an explicit search node ceiling on the default 6x2 grid.
func SolveWithBudget(widgets []WidgetInput, maxSearchNodes int) (*Layout, error) {
	return SolveGridWithBudget(widgets, GridColumns, GridRows, maxSearchNodes)
}

func partitionAndValidateWidgets(widgets []WidgetInput, gridCols, gridRows int) ([]WidgetInput, []WidgetInput, int, error) {
	seenIDs := make(map[string]int, len(widgets))
	var pinned, unpinned []WidgetInput
	var pinnedArea, unpinnedArea int

	for i, w := range widgets {
		trimmedID := strings.TrimSpace(w.ID)
		if trimmedID == "" {
			return nil, nil, 0, fmt.Errorf("widget at index %d has empty id", i)
		}
		if prevIdx, exists := seenIDs[trimmedID]; exists {
			return nil, nil, 0, fmt.Errorf("duplicate widget id '%s' found (indices %d and %d)", trimmedID, prevIdx, i)
		}
		seenIDs[trimmedID] = i

		if !w.Dimensions.IsValidForGrid(gridCols, gridRows) {
			return nil, nil, 0, fmt.Errorf("widget '%s' has invalid dimensions [%d, %d]: cols must be 1..%d, rows must be 1..%d", w.ID, w.Dimensions.Cols, w.Dimensions.Rows, gridCols, gridRows)
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
		return nil, nil, 0, err
	}
	if deficit > 0 {
		effectiveTotalArea := (k * GridTotalCells) - deficit
		return nil, nil, 0, formatDeficitErrorForGrid(effectiveTotalArea, k, deficit, gridCols, gridRows)
	}
	return pinned, unpinned, k, nil
}

func sortAndGroupWidgets(pinned, unpinned []WidgetInput) []*shapeGroup {
	sort.SliceStable(pinned, func(i, j int) bool {
		if pinned[i].Dimensions.Area() != pinned[j].Dimensions.Area() {
			return pinned[i].Dimensions.Area() > pinned[j].Dimensions.Area()
		}
		if pinned[i].Dimensions.Rows != pinned[j].Dimensions.Rows {
			return pinned[i].Dimensions.Rows > pinned[j].Dimensions.Rows
		}
		return pinned[i].ID < pinned[j].ID
	})

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

	sort.Slice(shapeGroups, func(i, j int) bool {
		if shapeGroups[i].dims.Area() != shapeGroups[j].dims.Area() {
			return shapeGroups[i].dims.Area() > shapeGroups[j].dims.Area()
		}
		return shapeGroups[i].dims.Rows > shapeGroups[j].dims.Rows
	})

	for _, sg := range shapeGroups {
		sort.SliceStable(sg.widgets, func(i, j int) bool {
			return sg.widgets[i].ID < sg.widgets[j].ID
		})
	}
	return shapeGroups
}

func assembleSolvedLayout(k int, screenMasks []uint16, pinnedPlacements []PlacedWidget, unpinnedPlacements []unpinnedPlacement, placedCount int) *Layout {
	res := &Layout{TotalScreens: k, Screens: make([]Screen, k)}
	for s := 0; s < k; s++ {
		screenWidgets := make([]PlacedWidget, 0, len(pinnedPlacements)+placedCount)
		screenWidgets = append(screenWidgets, pinnedPlacements...)
		for i := 0; i < placedCount; i++ {
			if unpinnedPlacements[i].screen == s {
				screenWidgets = append(screenWidgets, PlacedWidget{
					WidgetID: unpinnedPlacements[i].widget.ID, Type: unpinnedPlacements[i].widget.Type,
					Origin: unpinnedPlacements[i].origin, Dimensions: unpinnedPlacements[i].widget.Dimensions,
				})
			}
		}
		sort.Slice(screenWidgets, func(i, j int) bool {
			if screenWidgets[i].Row() != screenWidgets[j].Row() {
				return screenWidgets[i].Row() < screenWidgets[j].Row()
			}
			return screenWidgets[i].Col() < screenWidgets[j].Col()
		})
		res.Screens[s] = Screen{Index: s, Bitmask: screenMasks[s], Widgets: screenWidgets}
	}
	return res
}

type gridSolver struct {
	gridCols           int
	gridRows           int
	maxSearchNodes     int
	k                  int
	pinned             []WidgetInput
	unpinned           []WidgetInput
	shapeGroups        []*shapeGroup
	screenMasks        []uint16
	pinnedPlacements   []PlacedWidget
	unpinnedPlacements []unpinnedPlacement
	placedCount        int
	nodeCount          int
}

func (s *gridSolver) placePinned(pIdx int) bool {
	if pIdx == len(s.pinned) {
		return s.placeUnpinnedExactCover()
	}
	pw := s.pinned[pIdx]
	w, h := pw.Dimensions.Cols, pw.Dimensions.Rows
	maxCol, maxRow := s.gridCols-w, s.gridRows-h
	for r := 0; r <= maxRow; r++ {
		for c := 0; c <= maxCol; c++ {
			mask := computeWidgetBitmaskForGrid(w, h, c, r, s.gridCols)
			if (s.screenMasks[0] & mask) != 0 {
				continue
			}
			fillMaskAllScreens(s.screenMasks, s.k, mask, true)
			s.pinnedPlacements[pIdx] = PlacedWidget{
				WidgetID: pw.ID, Type: pw.Type, Origin: [2]int{c, r}, Dimensions: pw.Dimensions, Pinned: true,
			}
			if s.placePinned(pIdx + 1) {
				return true
			}
			fillMaskAllScreens(s.screenMasks, s.k, mask, false)
		}
	}
	return false
}

func (s *gridSolver) tryPlaceShapeGroup(sg *shapeGroup, targetScreen int, sMask uint16, c, r int) bool {
	if sg.count == 0 || c+sg.dims.Cols > s.gridCols || r+sg.dims.Rows > s.gridRows {
		return false
	}
	mask := computeWidgetBitmaskForGrid(sg.dims.Cols, sg.dims.Rows, c, r, s.gridCols)
	if (sMask & mask) != 0 {
		return false
	}
	s.screenMasks[targetScreen] |= mask
	sg.count--
	s.unpinnedPlacements[s.placedCount] = unpinnedPlacement{
		screen: targetScreen, origin: [2]int{c, r}, widget: sg.widgets[len(sg.widgets)-sg.count-1],
	}
	s.placedCount++
	if s.placeUnpinnedExactCover() {
		return true
	}
	s.placedCount--
	sg.count++
	s.screenMasks[targetScreen] &= ^mask
	return false
}

func (s *gridSolver) placeUnpinnedExactCover() bool {
	s.nodeCount++
	if s.nodeCount > s.maxSearchNodes {
		return false
	}
	targetScreen := firstIncompleteScreen(s.screenMasks, s.k)
	if targetScreen == -1 {
		return true
	}

	sMask := s.screenMasks[targetScreen]
	emptyCell := bits.TrailingZeros16(^sMask)
	c, r := emptyCell%s.gridCols, emptyCell/s.gridCols

	for _, sg := range s.shapeGroups {
		if s.tryPlaceShapeGroup(sg, targetScreen, sMask, c, r) {
			return true
		}
	}
	return false
}

// SolveGridWithBudget executes the solver for a 6x2 or 2x6 grid with an explicit search node ceiling.
func SolveGridWithBudget(widgets []WidgetInput, gridCols, gridRows, maxSearchNodes int) (*Layout, error) {
	gridCols, gridRows = normalizeGrid(gridCols, gridRows)
	if maxSearchNodes <= 0 {
		maxSearchNodes = defaultMaxSearchNodes
	}
	if len(widgets) == 0 {
		return nil, formatDeficitErrorForGrid(0, 1, GridTotalCells, gridCols, gridRows)
	}

	pinned, unpinned, k, err := partitionAndValidateWidgets(widgets, gridCols, gridRows)
	if err != nil {
		return nil, err
	}
	shapeGroups := sortAndGroupWidgets(pinned, unpinned)

	solver := &gridSolver{
		gridCols:           gridCols,
		gridRows:           gridRows,
		maxSearchNodes:     maxSearchNodes,
		k:                  k,
		pinned:             pinned,
		unpinned:           unpinned,
		shapeGroups:        shapeGroups,
		screenMasks:        make([]uint16, k),
		pinnedPlacements:   make([]PlacedWidget, len(pinned)),
		unpinnedPlacements: make([]unpinnedPlacement, len(unpinned)),
	}

	if !solver.placePinned(0) {
		return nil, fmt.Errorf("[Mirrormere Config Error] Invalid widget layout configuration:\n  Widgets satisfy total cell count but cannot geometrically tile the %dx%d grid without gaps or overlaps.\n  Suggestions:\n    - Adjust widget dimensions to fit the %dx%d grid.\n    - Insert native spacer tiles (e.g. { type: \"spacer\", dimensions: [2, 1] }) to pad irregular shapes.", gridCols, gridRows, gridCols, gridRows)
	}
	return assembleSolvedLayout(k, solver.screenMasks, solver.pinnedPlacements, solver.unpinnedPlacements, solver.placedCount), nil
}

func firstIncompleteScreen(screenMasks []uint16, k int) int {
	for s := 0; s < k; s++ {
		if screenMasks[s] != FullGridMask {
			return s
		}
	}
	return -1
}

func fillMaskAllScreens(screenMasks []uint16, k int, mask uint16, set bool) {
	for s := 0; s < k; s++ {
		if set {
			screenMasks[s] |= mask
		} else {
			screenMasks[s] &= ^mask
		}
	}
}

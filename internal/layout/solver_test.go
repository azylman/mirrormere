package layout_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/azylman/mirrormere/internal/domain"
	"github.com/azylman/mirrormere/internal/layout"
)

func TestCellIndex(t *testing.T) {
	t.Parallel()

	for r := 0; r < layout.GridRows; r++ {
		for c := 0; c < layout.GridColumns; c++ {
			expected := r*layout.GridColumns + c
			actual := layout.CellIndex(c, r)
			if actual != expected {
				t.Fatalf("CellIndex(%d, %d) = %d; want %d", c, r, actual, expected)
			}
		}
	}
}

func TestWidgetBitmask_Valid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		cols     int
		rows     int
		origCol  int
		origRow  int
		expected uint16
	}{
		{
			name:     "single cell (0, 0)",
			cols:     1,
			rows:     1,
			origCol:  0,
			origRow:  0,
			expected: 0b000000000001,
		},
		{
			name:     "single cell (5, 1)",
			cols:     1,
			rows:     1,
			origCol:  5,
			origRow:  1,
			expected: 0b100000000000,
		},
		{
			name:     "2x1 at (0, 0)",
			cols:     2,
			rows:     1,
			origCol:  0,
			origRow:  0,
			expected: 0b000000000011,
		},
		{
			name:     "2x1 at (4, 1)",
			cols:     2,
			rows:     1,
			origCol:  4,
			origRow:  1,
			expected: 0b110000000000,
		},
		{
			name:     "4x2 hero at (0, 0)",
			cols:     4,
			rows:     2,
			origCol:  0,
			origRow:  0,
			expected: 0b001111001111,
		},
		{
			name:     "6x2 full grid hero at (0, 0)",
			cols:     6,
			rows:     2,
			origCol:  0,
			origRow:  0,
			expected: layout.FullGridMask,
		},
		{
			name:     "6x1 banner row 0",
			cols:     6,
			rows:     1,
			origCol:  0,
			origRow:  0,
			expected: 0b000000111111,
		},
		{
			name:     "6x1 banner row 1",
			cols:     6,
			rows:     1,
			origCol:  0,
			origRow:  1,
			expected: 0b111111000000,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mask, err := layout.WidgetBitmask(tc.cols, tc.rows, tc.origCol, tc.origRow)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if mask != tc.expected {
				t.Fatalf("WidgetBitmask(%d, %d, %d, %d) = %012b; want %012b", tc.cols, tc.rows, tc.origCol, tc.origRow, mask, tc.expected)
			}
		})
	}
}

func TestWidgetBitmask_Invalid(t *testing.T) {
	t.Parallel()

	invalidCases := []struct {
		name    string
		cols    int
		rows    int
		origCol int
		origRow int
	}{
		{"cols zero", 0, 1, 0, 0},
		{"cols too large", 7, 1, 0, 0},
		{"rows zero", 1, 0, 0, 0},
		{"rows too large", 1, 3, 0, 0},
		{"originCol negative", 1, 1, -1, 0},
		{"originRow negative", 1, 1, 0, -1},
		{"originCol overflow", 4, 1, 3, 0},
		{"originRow overflow", 1, 2, 0, 1},
	}

	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := layout.WidgetBitmask(tc.cols, tc.rows, tc.origCol, tc.origRow)
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
		})
	}
}

func TestComputeScreens(t *testing.T) {
	t.Parallel()

	// 1. Single screen unpinned full
	k, def, err := layout.ComputeScreens(0, 12)
	if err != nil || k != 1 || def != 0 {
		t.Fatalf("expected k=1, def=0, got k=%d, def=%d, err=%v", k, def, err)
	}

	// 2. Multi-screen unpinned full
	k, def, err = layout.ComputeScreens(0, 24)
	if err != nil || k != 2 || def != 0 {
		t.Fatalf("expected k=2, def=0, got k=%d, def=%d, err=%v", k, def, err)
	}

	// 3. Unpinned deficit (SPEC-005 example: 14 cells -> K=2, deficit=10)
	k, def, err = layout.ComputeScreens(0, 14)
	if err != nil || k != 2 || def != 10 {
		t.Fatalf("expected k=2, def=10, got k=%d, def=%d, err=%v", k, def, err)
	}

	// 4. Pinned + Unpinned balanced (Pinned 8 + Unpinned 8 -> K=2, def=0)
	k, def, err = layout.ComputeScreens(8, 8)
	if err != nil || k != 2 || def != 0 {
		t.Fatalf("expected k=2, def=0, got k=%d, def=%d, err=%v", k, def, err)
	}

	// 5. Pinned + Unpinned deficit (Pinned 8 + Unpinned 2 -> K=1, def=2)
	k, def, err = layout.ComputeScreens(8, 2)
	if err != nil || k != 1 || def != 2 {
		t.Fatalf("expected k=1, def=2, got k=%d, def=%d, err=%v", k, def, err)
	}

	// 6. Pinned only with deficit (Pinned 8 + Unpinned 0 -> K=1, def=4)
	k, def, err = layout.ComputeScreens(8, 0)
	if err != nil || k != 1 || def != 4 {
		t.Fatalf("expected k=1, def=4, got k=%d, def=%d, err=%v", k, def, err)
	}

	// 7. Pinned full screen (Pinned 12 + Unpinned 0 -> K=1, def=0)
	k, def, err = layout.ComputeScreens(12, 0)
	if err != nil || k != 1 || def != 0 {
		t.Fatalf("expected k=1, def=0, got k=%d, def=%d, err=%v", k, def, err)
	}

	// 8. Negative areas
	_, _, err = layout.ComputeScreens(-1, 10)
	if err == nil {
		t.Fatal("expected error for negative pinned area, got nil")
	}
	_, _, err = layout.ComputeScreens(4, -2)
	if err == nil {
		t.Fatal("expected error for negative unpinned area, got nil")
	}

	// 9. Pinned area > 12
	_, _, err = layout.ComputeScreens(14, 0)
	if err == nil || !strings.Contains(err.Error(), "exceeding single-screen limit") {
		t.Fatalf("expected single-screen limit error, got %v", err)
	}

	// 10. Pinned area == 12 with unpinned > 0
	_, _, err = layout.ComputeScreens(12, 2)
	if err == nil || !strings.Contains(err.Error(), "leaving no screen space") {
		t.Fatalf("expected leaving no space error, got %v", err)
	}
}

func TestFormatDeficitError_Formatting(t *testing.T) {
	t.Parallel()

	err := layout.FormatDeficitError(14, 2, 10)
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	msg := err.Error()

	expectedLines := []string{
		"[Mirrormere Config Error] Invalid widget layout configuration:",
		"Total widget area is 14 cells.",
		"A 6x2 grid requires screen areas in multiples of 12 (Target: 2 screens = 24 cells).",
		"Deficit: 10 cells needed to achieve 100% fully-filled screens.",
		"Suggestions:",
		"Add a [4, 2] widget (8 cells) and a [2, 1] widget (2 cells).",
		"Or expand existing [2, 1] widgets to [3, 2] / [4, 2].",
		"Or insert native spacer tiles (e.g. { type: \"spacer\", dimensions: [2, 2] }) to intentionally leave layout space open.",
	}

	for _, line := range expectedLines {
		if !strings.Contains(msg, line) {
			t.Errorf("error output missing expected line %q;\nFull message:\n%s", line, msg)
		}
	}

	// Test various deficits to verify decomposeDeficit and spacer suggestions
	for d := 0; d <= 13; d++ {
		e := layout.FormatDeficitError(12-d, 1, d)
		if e == nil {
			t.Fatalf("expected error for deficit %d", d)
		}
	}
}

func TestSolve_Archetypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		widgets []layout.WidgetInput
	}{
		{
			name: "2/3 + 1/3 Golden Ratio: [4, 2] + [2, 1] + [2, 1]",
			widgets: []layout.WidgetInput{
				{ID: "calendar", Dimensions: domain.NewDimension(4, 2)},
				{ID: "weather", Dimensions: domain.NewDimension(2, 1)},
				{ID: "chores", Dimensions: domain.NewDimension(2, 1)},
			},
		},
		{
			name: "50/50 Split: [3, 2] + [3, 2]",
			widgets: []layout.WidgetInput{
				{ID: "photos", Dimensions: domain.NewDimension(3, 2)},
				{ID: "hass", Dimensions: domain.NewDimension(3, 2)},
			},
		},
		{
			name: "Tri-Column: [2, 2] + [2, 2] + [2, 2]",
			widgets: []layout.WidgetInput{
				{ID: "col1", Dimensions: domain.NewDimension(2, 2)},
				{ID: "col2", Dimensions: domain.NewDimension(2, 2)},
				{ID: "col3", Dimensions: domain.NewDimension(2, 2)},
			},
		},
		{
			name: "Full Hero Canvas: [6, 2]",
			widgets: []layout.WidgetInput{
				{ID: "hero-photo", Dimensions: domain.NewDimension(6, 2)},
			},
		},
		{
			name: "Horizontal Banners: [6, 1] + [6, 1]",
			widgets: []layout.WidgetInput{
				{ID: "banner-top", Dimensions: domain.NewDimension(6, 1)},
				{ID: "banner-bottom", Dimensions: domain.NewDimension(6, 1)},
			},
		},
		{
			name: "Mixed 4-cell widgets with different rows: [2, 2] + [4, 1] + [2, 1] + [2, 1]",
			widgets: []layout.WidgetInput{
				{ID: "w-2x2", Dimensions: domain.NewDimension(2, 2)},
				{ID: "w-4x1", Dimensions: domain.NewDimension(4, 1)},
				{ID: "w-2x1a", Dimensions: domain.NewDimension(2, 1)},
				{ID: "w-2x1b", Dimensions: domain.NewDimension(2, 1)},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, err := layout.Solve(tc.widgets)
			if err != nil {
				t.Fatalf("Solve failed: %v", err)
			}
			if res.TotalScreens != 1 {
				t.Fatalf("expected 1 screen, got %d", res.TotalScreens)
			}
			if len(res.Screens) != 1 {
				t.Fatalf("expected 1 screen in slice, got %d", len(res.Screens))
			}
			if res.Screens[0].Bitmask != layout.FullGridMask {
				t.Fatalf("screen bitmask %012b; want %012b", res.Screens[0].Bitmask, layout.FullGridMask)
			}
			if len(res.Screens[0].Widgets) != len(tc.widgets) {
				t.Fatalf("expected %d widgets placed, got %d", len(tc.widgets), len(res.Screens[0].Widgets))
			}

			// Validate helper methods
			for _, pw := range res.Screens[0].Widgets {
				if pw.Cols() != pw.Dimensions.Cols || pw.Rows() != pw.Dimensions.Rows {
					t.Errorf("PlacedWidget size helper mismatch for %s", pw.WidgetID)
				}
				if pw.Col() != pw.Origin[0] || pw.Row() != pw.Origin[1] {
					t.Errorf("PlacedWidget origin helper mismatch for %s", pw.WidgetID)
				}
			}
		})
	}
}

func TestSolve_MultiScreen_Unpinned(t *testing.T) {
	t.Parallel()

	// Screen 1: [4, 2] + [2, 1] + [2, 1] = 12
	// Screen 2: [3, 2] + [3, 2] = 12
	// Total 24 cells -> K = 2 screens
	widgets := []layout.WidgetInput{
		{ID: "w-cal", Dimensions: domain.NewDimension(4, 2)},
		{ID: "w-tasks1", Dimensions: domain.NewDimension(2, 1)},
		{ID: "w-tasks2", Dimensions: domain.NewDimension(2, 1)},
		{ID: "w-photo1", Dimensions: domain.NewDimension(3, 2)},
		{ID: "w-photo2", Dimensions: domain.NewDimension(3, 2)},
	}

	res, err := layout.Solve(widgets)
	if err != nil {
		t.Fatalf("Solve failed: %v", err)
	}
	if res.TotalScreens != 2 {
		t.Fatalf("expected 2 screens, got %d", res.TotalScreens)
	}
	for i, s := range res.Screens {
		if s.Index != i {
			t.Errorf("screen %d has index %d", i, s.Index)
		}
		if s.Bitmask != layout.FullGridMask {
			t.Errorf("screen %d bitmask %012b; want %012b", i, s.Bitmask, layout.FullGridMask)
		}
	}
}

func TestSolve_ThreeScreens_Pruning(t *testing.T) {
	t.Parallel()

	// 3 screens = 36 cells.
	// Screen 1: [6, 2] = 12
	// Screen 2: [6, 2] = 12
	// Screen 3: [4, 2] + [2, 1] + [2, 1] = 12
	widgets := []layout.WidgetInput{
		{ID: "hero1", Dimensions: domain.NewDimension(6, 2)},
		{ID: "hero2", Dimensions: domain.NewDimension(6, 2)},
		{ID: "cal", Dimensions: domain.NewDimension(4, 2)},
		{ID: "t1", Dimensions: domain.NewDimension(2, 1)},
		{ID: "t2", Dimensions: domain.NewDimension(2, 1)},
	}

	res, err := layout.Solve(widgets)
	if err != nil {
		t.Fatalf("Solve failed: %v", err)
	}
	if res.TotalScreens != 3 {
		t.Fatalf("expected 3 screens, got %d", res.TotalScreens)
	}
	for i, s := range res.Screens {
		if s.Bitmask != layout.FullGridMask {
			t.Errorf("screen %d bitmask %012b; want %012b", i, s.Bitmask, layout.FullGridMask)
		}
	}
}

func TestSolve_PinnedReplication(t *testing.T) {
	t.Parallel()

	// Pinned 4x2 hero (8 cells).
	// Unpinned: four 2x1 widgets (8 cells).
	// K = 2 screens. Pinned widget replicated across both screens at identical origin.
	widgets := []layout.WidgetInput{
		{ID: "pinned-cal", Dimensions: domain.NewDimension(4, 2), Pinned: true},
		{ID: "task-1", Dimensions: domain.NewDimension(2, 1)},
		{ID: "task-2", Dimensions: domain.NewDimension(2, 1)},
		{ID: "task-3", Dimensions: domain.NewDimension(2, 1)},
		{ID: "task-4", Dimensions: domain.NewDimension(2, 1)},
	}

	res, err := layout.Solve(widgets)
	if err != nil {
		t.Fatalf("Solve failed: %v", err)
	}
	if res.TotalScreens != 2 {
		t.Fatalf("expected 2 screens, got %d", res.TotalScreens)
	}

	var pinnedOrigins [][2]int
	for _, s := range res.Screens {
		if s.Bitmask != layout.FullGridMask {
			t.Errorf("screen %d bitmask %012b; want %012b", s.Index, s.Bitmask, layout.FullGridMask)
		}
		foundPinned := false
		for _, pw := range s.Widgets {
			if pw.WidgetID == "pinned-cal" {
				foundPinned = true
				if !pw.Pinned {
					t.Errorf("expected PlacedWidget.Pinned to be true")
				}
				pinnedOrigins = append(pinnedOrigins, pw.Origin)
			}
		}
		if !foundPinned {
			t.Errorf("screen %d missing pinned widget", s.Index)
		}
	}

	if len(pinnedOrigins) != 2 || pinnedOrigins[0] != pinnedOrigins[1] {
		t.Fatalf("pinned widget origin mismatch across screens: %v", pinnedOrigins)
	}
}

func TestSolve_PinnedFullScreen(t *testing.T) {
	t.Parallel()

	widgets := []layout.WidgetInput{
		{ID: "pinned-hero", Dimensions: domain.NewDimension(6, 2), Pinned: true},
	}

	res, err := layout.Solve(widgets)
	if err != nil {
		t.Fatalf("Solve failed: %v", err)
	}
	if res.TotalScreens != 1 {
		t.Fatalf("expected 1 screen, got %d", res.TotalScreens)
	}
	if res.Screens[0].Bitmask != layout.FullGridMask {
		t.Fatalf("screen bitmask %012b; want %012b", res.Screens[0].Bitmask, layout.FullGridMask)
	}
}

func TestSolve_DeficitGuidance(t *testing.T) {
	t.Parallel()

	// 14 cells -> K=2, deficit 10
	widgets := []layout.WidgetInput{
		{ID: "cal", Dimensions: domain.NewDimension(4, 2)},
		{ID: "weather", Dimensions: domain.NewDimension(3, 2)},
	}

	_, err := layout.Solve(widgets)
	if err == nil {
		t.Fatal("expected deficit error, got nil")
	}
	if !strings.Contains(err.Error(), "Deficit: 10 cells needed") {
		t.Fatalf("expected deficit error message, got: %v", err)
	}
}

func TestSolve_GeometricDeadlocks(t *testing.T) {
	t.Parallel()

	t.Run("Width mismatch: [5, 2] + [2, 1] (area 12, impossible)", func(t *testing.T) {
		t.Parallel()
		widgets := []layout.WidgetInput{
			{ID: "w-5x2", Dimensions: domain.NewDimension(5, 2)},
			{ID: "w-2x1", Dimensions: domain.NewDimension(2, 1)},
		}
		_, err := layout.Solve(widgets)
		if err == nil || !strings.Contains(err.Error(), "cannot geometrically tile the 6x2 grid") {
			t.Fatalf("expected geometric deadlock error, got %v", err)
		}
	})

	t.Run("Row partition mismatch: [5, 1] + [4, 1] + [3, 1] (area 12, impossible)", func(t *testing.T) {
		t.Parallel()
		widgets := []layout.WidgetInput{
			{ID: "w-5x1", Dimensions: domain.NewDimension(5, 1)},
			{ID: "w-4x1", Dimensions: domain.NewDimension(4, 1)},
			{ID: "w-3x1", Dimensions: domain.NewDimension(3, 1)},
		}
		_, err := layout.Solve(widgets)
		if err == nil || !strings.Contains(err.Error(), "cannot geometrically tile the 6x2 grid") {
			t.Fatalf("expected geometric deadlock error, got %v", err)
		}
	})

	t.Run("Pinned conflict: pinned [5, 1] + [3, 2] + [1, 1]", func(t *testing.T) {
		t.Parallel()
		widgets := []layout.WidgetInput{
			{ID: "pin-5x1", Dimensions: domain.NewDimension(5, 1), Pinned: true},
			{ID: "u-3x2", Dimensions: domain.NewDimension(3, 2)},
			{ID: "u-1x1", Dimensions: domain.NewDimension(1, 1)},
		}
		_, err := layout.Solve(widgets)
		if err == nil || !strings.Contains(err.Error(), "cannot geometrically tile the 6x2 grid") {
			t.Fatalf("expected geometric deadlock error, got %v", err)
		}
	})
}

func TestSolve_EdgeCases(t *testing.T) {
	t.Parallel()

	// 1. Zero widgets
	t.Run("zero widgets", func(t *testing.T) {
		t.Parallel()
		_, err := layout.Solve(nil)
		if err == nil || !strings.Contains(err.Error(), "Deficit: 12 cells") {
			t.Fatalf("expected deficit error for zero widgets, got %v", err)
		}
	})

	// 2. Empty ID
	t.Run("empty widget ID", func(t *testing.T) {
		t.Parallel()
		widgets := []layout.WidgetInput{
			{ID: "  ", Dimensions: domain.NewDimension(4, 2)},
		}
		_, err := layout.Solve(widgets)
		if err == nil || !strings.Contains(err.Error(), "empty id") {
			t.Fatalf("expected empty id error, got %v", err)
		}
	})

	// 3. Duplicate ID
	t.Run("duplicate widget ID", func(t *testing.T) {
		t.Parallel()
		widgets := []layout.WidgetInput{
			{ID: "dup", Dimensions: domain.NewDimension(4, 2)},
			{ID: "dup", Dimensions: domain.NewDimension(2, 1)},
		}
		_, err := layout.Solve(widgets)
		if err == nil || !strings.Contains(err.Error(), "duplicate widget id") {
			t.Fatalf("expected duplicate id error, got %v", err)
		}
	})

	// 4. Invalid dimensions
	t.Run("invalid dimensions", func(t *testing.T) {
		t.Parallel()
		widgets := []layout.WidgetInput{
			{ID: "invalid-dims", Dimensions: domain.NewDimension(7, 2)},
		}
		_, err := layout.Solve(widgets)
		if err == nil || !strings.Contains(err.Error(), "invalid dimensions") {
			t.Fatalf("expected invalid dimensions error, got %v", err)
		}
	})

	// 5. Pinned area > 12
	t.Run("pinned area > 12", func(t *testing.T) {
		t.Parallel()
		widgets := []layout.WidgetInput{
			{ID: "p1", Dimensions: domain.NewDimension(4, 2), Pinned: true},
			{ID: "p2", Dimensions: domain.NewDimension(4, 2), Pinned: true},
		}
		_, err := layout.Solve(widgets)
		if err == nil || !strings.Contains(err.Error(), "exceeding single-screen limit") {
			t.Fatalf("expected exceeding limit error, got %v", err)
		}
	})

	// 6. Pinned area == 12 with unpinned > 0
	t.Run("pinned area == 12 with unpinned > 0", func(t *testing.T) {
		t.Parallel()
		widgets := []layout.WidgetInput{
			{ID: "p1", Dimensions: domain.NewDimension(6, 2), Pinned: true},
			{ID: "u1", Dimensions: domain.NewDimension(2, 1)},
		}
		_, err := layout.Solve(widgets)
		if err == nil || !strings.Contains(err.Error(), "leaving no screen space") {
			t.Fatalf("expected leaving no screen space error, got %v", err)
		}
	})
}

func TestPlacedWidget_JSON(t *testing.T) {
	t.Parallel()

	pw := layout.PlacedWidget{
		WidgetID:   "family-calendar",
		Type:       "calendar-agenda",
		Origin:     [2]int{0, 0},
		Dimensions: domain.NewDimension(4, 2),
		Pinned:     true,
	}

	data, err := json.Marshal(pw)
	if err != nil {
		t.Fatalf("failed to marshal PlacedWidget: %v", err)
	}

	jsonStr := string(data)
	if !strings.Contains(jsonStr, `"origin":[0,0]`) {
		t.Errorf("expected 'origin:[0,0]', got %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"dimensions":[4,2]`) {
		t.Errorf("expected 'dimensions:[4,2]', got %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"widget_id":"family-calendar"`) {
		t.Errorf("expected 'widget_id:family-calendar', got %s", jsonStr)
	}
}

func BenchmarkSolve(b *testing.B) {
	widgets := []layout.WidgetInput{
		{ID: "cal", Dimensions: domain.NewDimension(4, 2), Pinned: true},
		{ID: "tasks-1", Dimensions: domain.NewDimension(2, 1)},
		{ID: "tasks-2", Dimensions: domain.NewDimension(2, 1)},
		{ID: "weather-1", Dimensions: domain.NewDimension(2, 1)},
		{ID: "weather-2", Dimensions: domain.NewDimension(2, 1)},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := layout.Solve(widgets)
		if err != nil {
			b.Fatalf("Solve failed: %v", err)
		}
	}
}

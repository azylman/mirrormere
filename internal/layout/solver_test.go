package layout_test

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/azylman/mirrormere/internal/domain"
	"github.com/azylman/mirrormere/internal/layout"
)

func mkW(id string, c, r int, pinned bool) layout.WidgetInput {
	return layout.WidgetInput{ID: id, Dimensions: domain.NewDimension(c, r), Pinned: pinned}
}

func repeatW(prefix string, c, r, n int) []layout.WidgetInput {
	res := make([]layout.WidgetInput, n)
	for i := range n {
		res[i] = mkW(fmt.Sprintf("%s-%d", prefix, i), c, r, false)
	}
	return res
}

func TestComputeScreens(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		pinned   int
		unpinned int
		wantK    int
		wantDef  int
		wantErr  string
	}{
		{"single screen unpinned full", 0, 12, 1, 0, ""},
		{"multi-screen unpinned full", 0, 24, 2, 0, ""},
		{"unpinned deficit", 0, 14, 2, 10, ""},
		{"pinned + unpinned balanced", 8, 8, 2, 0, ""},
		{"pinned + unpinned deficit", 8, 2, 1, 2, ""},
		{"pinned only with deficit", 8, 0, 1, 4, ""},
		{"pinned full screen", 12, 0, 1, 0, ""},
		{"negative pinned area", -1, 10, 0, 0, "non-negative"},
		{"negative unpinned area", 4, -2, 0, 0, "non-negative"},
		{"pinned area > 12", 14, 0, 0, 0, "exceeding single-screen limit"},
		{"pinned area == 12 with unpinned > 0", 12, 2, 0, 0, "leaving no screen space"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			k, def, err := layout.ComputeScreens(tc.pinned, tc.unpinned)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("ComputeScreens(%d, %d) err = %v, want %q", tc.pinned, tc.unpinned, err, tc.wantErr)
				}
				return
			}
			if err != nil || k != tc.wantK || def != tc.wantDef {
				t.Fatalf("ComputeScreens(%d, %d) = (%d, %d, %v), want (%d, %d, nil)", tc.pinned, tc.unpinned, k, def, err, tc.wantK, tc.wantDef)
			}
		})
	}
}

func TestFormatDeficitError_Formatting(t *testing.T) {
	t.Parallel()
	err := layout.FormatDeficitError(14, 2, 10)
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	msg := err.Error()
	for _, line := range []string{
		"[Mirrormere Config Error] Invalid widget layout configuration:",
		"Total widget area is 14 cells.",
		"A 6x2 grid requires screen areas in multiples of 12 (Target: 2 screens = 24 cells).",
		"Deficit: 10 cells needed to achieve 100% fully-filled screens.",
		"Suggestions:",
		"Add a [4, 2] widget (8 cells) and a [2, 1] widget (2 cells).",
		"Or expand existing [2, 1] widgets to [3, 2] / [4, 2].",
		"Or insert native spacer tiles (e.g. { type: \"spacer\", dimensions: [2, 2] }) to intentionally leave layout space open.",
	} {
		if !strings.Contains(msg, line) {
			t.Errorf("error output missing expected line %q;\nFull message:\n%s", line, msg)
		}
	}
	for d := range 14 {
		if e := layout.FormatDeficitError(12-d, 1, d); e == nil {
			t.Fatalf("expected error for deficit %d", d)
		}
	}
}

func TestSolve_Archetypes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		widgets     []layout.WidgetInput
		wantScreens int
	}{
		{"2/3 + 1/3 Golden Ratio", []layout.WidgetInput{mkW("calendar", 4, 2, false), mkW("weather", 2, 1, false), mkW("chores", 2, 1, false)}, 1},
		{"50/50 Split", []layout.WidgetInput{mkW("photos", 3, 2, false), mkW("hass", 3, 2, false)}, 1},
		{"Tri-Column", []layout.WidgetInput{mkW("col1", 2, 2, false), mkW("col2", 2, 2, false), mkW("col3", 2, 2, false)}, 1},
		{"Full Hero Canvas", []layout.WidgetInput{mkW("hero-photo", 6, 2, false)}, 1},
		{"Horizontal Banners", []layout.WidgetInput{mkW("banner-top", 6, 1, false), mkW("banner-bottom", 6, 1, false)}, 1},
		{"Mixed 4-cell widgets", []layout.WidgetInput{mkW("w-2x2", 2, 2, false), mkW("w-4x1", 4, 1, false), mkW("w-2x1a", 2, 1, false), mkW("w-2x1b", 2, 1, false)}, 1},
		{"Multi-screen unpinned", []layout.WidgetInput{mkW("w-cal", 4, 2, false), mkW("w-tasks1", 2, 1, false), mkW("w-tasks2", 2, 1, false), mkW("w-photo1", 3, 2, false), mkW("w-photo2", 3, 2, false)}, 2},
		{"Three screens pruning", []layout.WidgetInput{mkW("hero1", 6, 2, false), mkW("hero2", 6, 2, false), mkW("cal", 4, 2, false), mkW("t1", 2, 1, false), mkW("t2", 2, 1, false)}, 3},
		{"Pinned full screen", []layout.WidgetInput{mkW("pinned-hero", 6, 2, true)}, 1},
		{"Multiple pinned sorting", []layout.WidgetInput{mkW("pinned-b", 2, 1, true), mkW("pinned-a", 2, 1, true), mkW("pinned-c", 4, 1, true), mkW("pinned-d", 2, 2, true)}, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, err := layout.Solve(tc.widgets)
			if err != nil {
				t.Fatalf("Solve failed: %v", err)
			}
			if res.TotalScreens != tc.wantScreens || len(res.Screens) != tc.wantScreens {
				t.Fatalf("TotalScreens = %d, want %d", res.TotalScreens, tc.wantScreens)
			}
			for i, s := range res.Screens {
				if s.Index != i {
					t.Errorf("screen %d has index %d", i, s.Index)
				}
				if s.Bitmask != layout.FullGridMask {
					t.Errorf("screen %d bitmask %012b; want %012b", i, s.Bitmask, layout.FullGridMask)
				}
				for _, pw := range s.Widgets {
					if pw.Cols() != pw.Dimensions.Cols || pw.Rows() != pw.Dimensions.Rows {
						t.Errorf("size mismatch for %s", pw.WidgetID)
					}
					if pw.Col() != pw.Origin[0] || pw.Row() != pw.Origin[1] {
						t.Errorf("origin mismatch for %s", pw.WidgetID)
					}
				}
			}
		})
	}
}

func TestSolve_PinnedReplication(t *testing.T) {
	t.Parallel()
	widgets := []layout.WidgetInput{
		mkW("pinned-cal", 4, 2, true),
		mkW("task-1", 2, 1, false), mkW("task-2", 2, 1, false),
		mkW("task-3", 2, 1, false), mkW("task-4", 2, 1, false),
	}
	res, err := layout.Solve(widgets)
	if err != nil {
		t.Fatalf("Solve failed: %v", err)
	}
	if res.TotalScreens != 2 {
		t.Fatalf("expected 2 screens, got %d", res.TotalScreens)
	}
	var origins [][2]int
	for _, s := range res.Screens {
		found := false
		for _, pw := range s.Widgets {
			if pw.WidgetID == "pinned-cal" {
				found = true
				if !pw.Pinned {
					t.Errorf("expected PlacedWidget.Pinned true")
				}
				origins = append(origins, pw.Origin)
			}
		}
		if !found {
			t.Errorf("screen %d missing pinned widget", s.Index)
		}
	}
	if len(origins) != 2 || origins[0] != origins[1] {
		t.Fatalf("pinned widget origin mismatch across screens: %v", origins)
	}
}

func TestSolve_ErrorsAndDeadlocks(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		widgets []layout.WidgetInput
		budget  int
		wantErr string
	}{
		{"deficit_guidance", []layout.WidgetInput{mkW("cal", 4, 2, false), mkW("weather", 3, 2, false)}, 0, "Deficit: 10 cells needed"},
		{"deadlock_width_mismatch", []layout.WidgetInput{mkW("w-5x2", 5, 2, false), mkW("w-2x1", 2, 1, false)}, 0, "cannot geometrically tile the 6x2 grid"},
		{"deadlock_row_mismatch", []layout.WidgetInput{mkW("w-5x1", 5, 1, false), mkW("w-4x1", 4, 1, false), mkW("w-3x1", 3, 1, false)}, 0, "cannot geometrically tile the 6x2 grid"},
		{"deadlock_pinned_conflict", []layout.WidgetInput{mkW("pin-5x1", 5, 1, true), mkW("u-3x2", 3, 2, false), mkW("u-1x1", 1, 1, false)}, 0, "cannot geometrically tile the 6x2 grid"},
		{"zero_widgets", nil, 0, "Deficit: 12 cells"},
		{"empty_id", []layout.WidgetInput{mkW("  ", 4, 2, false)}, 0, "empty id"},
		{"duplicate_id", []layout.WidgetInput{mkW("dup", 4, 2, false), mkW("dup", 2, 1, false)}, 0, "duplicate widget id"},
		{"invalid_dimensions", []layout.WidgetInput{mkW("invalid-dims", 7, 2, false)}, 0, "invalid dimensions"},
		{"pinned_area_gt_12", []layout.WidgetInput{mkW("p1", 4, 2, true), mkW("p2", 4, 2, true)}, 0, "exceeding single-screen limit"},
		{"pinned_12_with_unpinned", []layout.WidgetInput{mkW("p1", 6, 2, true), mkW("u1", 2, 1, false)}, 0, "leaving no screen space"},
		{"budget_ceiling", slices.Concat(repeatW("w-5x1", 5, 1, 6), repeatW("w-2x1", 2, 1, 9)), 2, "cannot geometrically tile the 6x2 grid"},
		{"multiscreen_backtracking_deadlock", []layout.WidgetInput{mkW("hero-6x2", 6, 2, false), mkW("w-5x1-a", 5, 1, false), mkW("w-5x1-b", 5, 1, false), mkW("w-2x1-a", 2, 1, false)}, 0, "cannot geometrically tile the 6x2 grid"},
		{"issue180_untileable_48_k4", slices.Concat(repeatW("w-5x1", 5, 1, 6), repeatW("w-2x1", 2, 1, 9)), 0, "cannot geometrically tile the 6x2 grid"},
		{"issue180_untileable_60_k5", slices.Concat(repeatW("w-5x1", 5, 1, 10), repeatW("w-2x1", 2, 1, 5)), 0, "cannot geometrically tile the 6x2 grid"},
		{"issue180_untileable_mixed_48_k4", slices.Concat([]layout.WidgetInput{mkW("w-3x2", 3, 2, false)}, repeatW("w-5x1", 5, 1, 6), repeatW("w-2x1", 2, 1, 6)), 0, "cannot geometrically tile the 6x2 grid"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var err error
			if tc.budget > 0 {
				_, err = layout.SolveWithBudget(tc.widgets, tc.budget)
			} else {
				_, err = layout.Solve(tc.widgets)
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("got err = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestPlacedWidget_JSON(t *testing.T) {
	t.Parallel()
	pw := layout.PlacedWidget{WidgetID: "family-calendar", Type: "calendar-feed", Origin: [2]int{0, 0}, Dimensions: domain.NewDimension(4, 2), Pinned: true}
	data, err := json.Marshal(pw)
	if err != nil {
		t.Fatalf("failed to marshal PlacedWidget: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, `"origin":[0,0]`) || !strings.Contains(s, `"dimensions":[4,2]`) || !strings.Contains(s, `"widget_id":"family-calendar"`) {
		t.Errorf("unexpected json: %s", s)
	}
}

func BenchmarkSolve(b *testing.B) {
	widgets := []layout.WidgetInput{
		mkW("cal", 4, 2, true),
		mkW("tasks-1", 2, 1, false), mkW("tasks-2", 2, 1, false),
		mkW("weather-1", 2, 1, false), mkW("weather-2", 2, 1, false),
	}
	b.ResetTimer()
	for range b.N {
		if _, err := layout.Solve(widgets); err != nil {
			b.Fatalf("Solve failed: %v", err)
		}
	}
}

func BenchmarkSolve_Untileable_Issue180(b *testing.B) {
	widgets := slices.Concat(repeatW("w-5x1", 5, 1, 6), repeatW("w-2x1", 2, 1, 9))
	b.ResetTimer()
	for range b.N {
		_, _ = layout.Solve(widgets)
	}
}

func TestSolveGridWithBudget_Portrait2x6(t *testing.T) {
	t.Parallel()
	widgets := []layout.WidgetInput{
		mkW("cal", 2, 4, true),
		mkW("weather", 1, 2, false),
		mkW("tasks", 1, 2, false),
	}
	res, err := layout.SolveGridWithBudget(widgets, 2, 6, 0)
	if err != nil {
		t.Fatalf("SolveGridWithBudget(2, 6) failed: %v", err)
	}
	if res.TotalScreens != 1 || res.Screens[0].Bitmask != layout.FullGridMask {
		t.Fatalf("unexpected 2x6 layout result: %+v", res)
	}

	if _, err := layout.SolveGridWithBudget(nil, 2, 6, 0); err == nil || !strings.Contains(err.Error(), "A 2x6 grid requires") {
		t.Fatalf("expected 2x6 deficit error for empty widgets, got %v", err)
	}
	if _, err := layout.SolveGridWithBudget([]layout.WidgetInput{mkW("wide", 4, 2, false)}, 2, 6, 0); err == nil || !strings.Contains(err.Error(), "cols must be 1..2, rows must be 1..6") {
		t.Fatalf("expected 2x6 dimension error, got %v", err)
	}
	if _, err := layout.SolveGridWithBudget([]layout.WidgetInput{mkW("tall-1", 1, 5, false), mkW("wide-1", 2, 2, false), mkW("wide-2", 2, 1, false), mkW("unit", 1, 1, false)}, 2, 6, 0); err == nil || !strings.Contains(err.Error(), "cannot geometrically tile the 2x6 grid") {
		t.Fatalf("expected 2x6 geometric tiling error, got %v", err)
	}
}

func TestSolveGridWithBudget_PreservesConfigScreenAndWidgetOrder(t *testing.T) {
	t.Parallel()
	widgets := []layout.WidgetInput{
		// Page 1: Calendar (2x4) + Tasks (1x2) + Photos (1x2)
		mkW("hearth-family-grid", 2, 4, false),
		mkW("whole-foods-tasks", 1, 2, false),
		mkW("family-photos", 1, 2, false),
		// Page 2: YouTube Music (2x3) + New on Plex (2x3)
		mkW("youtube-music-card", 2, 3, false),
		mkW("new-on-plex", 2, 3, false),
		// Page 3: Home Assistant (2x6)
		mkW("home-assistant-dashboard", 2, 6, false),
		// Page 4: Auxiliary widgets
		mkW("costco-tasks", 1, 2, false),
		mkW("ryans-tasks", 1, 2, false),
		mkW("emerald-hills-weather", 2, 2, false),
		mkW("aux-spacer", 2, 2, false),
	}
	res, err := layout.SolveGridWithBudget(widgets, 2, 6, 0)
	if err != nil {
		t.Fatalf("SolveGridWithBudget failed: %v", err)
	}
	if res.TotalScreens != 4 {
		t.Fatalf("TotalScreens = %d, want 4", res.TotalScreens)
	}
	wantScreen0 := []string{"hearth-family-grid", "whole-foods-tasks", "family-photos"}
	var gotScreen0 []string
	for _, w := range res.Screens[0].Widgets {
		gotScreen0 = append(gotScreen0, w.WidgetID)
	}
	if !slices.Equal(gotScreen0, wantScreen0) {
		t.Fatalf("Screen 0 widgets = %v, want %v", gotScreen0, wantScreen0)
	}
	wantScreen1 := []string{"youtube-music-card", "new-on-plex"}
	var gotScreen1 []string
	for _, w := range res.Screens[1].Widgets {
		gotScreen1 = append(gotScreen1, w.WidgetID)
	}
	if !slices.Equal(gotScreen1, wantScreen1) {
		t.Fatalf("Screen 1 widgets = %v, want %v", gotScreen1, wantScreen1)
	}
	if len(res.Screens[2].Widgets) != 1 || res.Screens[2].Widgets[0].WidgetID != "home-assistant-dashboard" {
		t.Fatalf("Screen 2 widgets = %+v, want [home-assistant-dashboard]", res.Screens[2].Widgets)
	}
}

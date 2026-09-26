package render_test

import (
	"html/template"
	"strings"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/render"
)

func TestSeq(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []int
		want []int
	}{
		{"empty args", []int{}, []int{}},
		{"seq 0", []int{0}, []int{}},
		{"seq negative", []int{-3}, []int{}},
		{"seq 5", []int{5}, []int{0, 1, 2, 3, 4}},
		{"seq 1 to 5", []int{1, 5}, []int{1, 2, 3, 4, 5}},
		{"seq 5 to 5", []int{5, 5}, []int{5}},
		{"seq 5 to 1 (descending)", []int{5, 1}, []int{}},
		{"seq 0 to 10 step 2", []int{0, 10, 2}, []int{0, 2, 4, 6, 8, 10}},
		{"seq 1 to 5 step 0", []int{1, 5, 0}, []int{}},
		{"seq 1 to 5 negative step", []int{1, 5, -1}, []int{}},
		{"seq 5 to 1 step 2", []int{5, 1, 2}, []int{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := render.Seq(tc.args...)
			if len(got) != len(tc.want) {
				t.Fatalf("expected length %d, got %d (%v)", len(tc.want), len(got), got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("at index %d: expected %d, got %d", i, tc.want[i], got[i])
				}
			}
		})
	}
}

func TestParseTime(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 25, 20, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		val       any
		wantValid bool
	}{
		{"time.Time", now, true},
		{"*time.Time non-nil", &now, true},
		{"*time.Time nil", (*time.Time)(nil), false},
		{"RFC3339 string", "2026-09-25T20:00:00Z", true},
		{"RFC3339Nano string", "2026-09-25T20:00:00.123456789Z", true},
		{"DateTime string", "2026-09-25 20:00:00", true},
		{"DateOnly string", "2026-09-25", true},
		{"TimeOnly string", "20:00:00", true},
		{"ShortTime string", "20:00", true},
		{"12h time string", "8:00 PM", true},
		{"0-padded 12h time", "08:00 PM", true},
		{"int64 unix", int64(1758830400), true},
		{"int unix", int(1758830400), true},
		{"float64 unix", float64(1758830400), true},
		{"invalid string", "not-a-date", false},
		{"unsupported type bool", true, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, ok := render.ParseTime(tc.val)
			if ok != tc.wantValid {
				t.Errorf("expected ok=%v, got %v", tc.wantValid, ok)
			}
		})
	}
}

func TestFormatDate(t *testing.T) {
	t.Parallel()

	target := time.Date(2026, 9, 25, 14, 30, 0, 0, time.UTC)

	tests := []struct {
		name string
		args []any
		want string
	}{
		{"no args", []any{}, ""},
		{"single arg time.Time", []any{target}, "2026-09-25"},
		{"single arg string", []any{"2026-09-25T14:30:00Z"}, "2026-09-25"},
		{"single arg invalid", []any{"not-a-date"}, "not-a-date"},
		{"layout and time", []any{"Jan 02, 2006", target}, "Sep 25, 2026"},
		{"empty layout defaults", []any{"", target}, "2026-09-25"},
		{"default layout keyword", []any{"default", target}, "2026-09-25"},
		{"custom layout with unparseable value", []any{"2006-01-02", "bad-date"}, "bad-date"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := render.FormatDate(tc.args...)
			if got != tc.want {
				t.Errorf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestFormatTime(t *testing.T) {
	t.Parallel()

	target := time.Date(2026, 9, 25, 14, 30, 0, 0, time.UTC)

	tests := []struct {
		name string
		args []any
		want string
	}{
		{"no args", []any{}, ""},
		{"single arg time.Time", []any{target}, "14:30"},
		{"single arg string", []any{"2026-09-25T14:30:00Z"}, "14:30"},
		{"single arg invalid", []any{"bad-time"}, "bad-time"},
		{"12h layout and time", []any{"3:04 PM", target}, "2:30 PM"},
		{"empty layout defaults", []any{"", target}, "14:30"},
		{"default layout keyword", []any{"default", target}, "14:30"},
		{"custom layout with unparseable value", []any{"15:04", "bad-time"}, "bad-time"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := render.FormatTime(tc.args...)
			if got != tc.want {
				t.Errorf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestRelDate(t *testing.T) {
	t.Parallel()

	ref := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		args []any
		want string
	}{
		{"no args", []any{}, ""},
		{"unparseable", []any{"invalid"}, "invalid"},
		{"today same hour", []any{ref}, "Today"},
		{"today earlier hour", []any{time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)}, "Today"},
		{"tomorrow", []any{time.Date(2026, 9, 26, 9, 0, 0, 0, time.UTC)}, "Tomorrow"},
		{"yesterday", []any{time.Date(2026, 9, 24, 23, 0, 0, 0, time.UTC)}, "Yesterday"},
		{"in 3 days", []any{time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)}, "in 3 days"},
		{"4 days ago", []any{time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)}, "4 days ago"},
		{"custom reference parameter", []any{
			time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC),
			"2026-10-01T00:00:00Z",
		}, "Tomorrow"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := render.RelDate(ref, tc.args...)
			if got != tc.want {
				t.Errorf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestJSONHelpers(t *testing.T) {
	t.Parallel()

	data := map[string]any{"key": "value", "count": 42}

	htmlOut, err := render.JSONHelper(data)
	if err != nil {
		t.Fatalf("JSONHelper failed: %v", err)
	}
	if !strings.Contains(string(htmlOut), `"key":"value"`) {
		t.Errorf("unexpected JSONHelper output: %s", htmlOut)
	}

	jsOut, err := render.JSONJSHelper(data)
	if err != nil {
		t.Fatalf("JSONJSHelper failed: %v", err)
	}
	if !strings.Contains(string(jsOut), `"count":42`) {
		t.Errorf("unexpected JSONJSHelper output: %s", jsOut)
	}

	// Unserializable type (channel)
	ch := make(chan int)
	if _, err := render.JSONHelper(ch); err == nil {
		t.Error("expected JSONHelper error for channel, got nil")
	}
	if _, err := render.JSONJSHelper(ch); err == nil {
		t.Error("expected JSONJSHelper error for channel, got nil")
	}
}

func TestStandardFuncMap(t *testing.T) {
	t.Parallel()

	fixedNow := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	funcs := render.StandardFuncMap(func() time.Time { return fixedNow })

	tmpl, err := template.New("test").Funcs(funcs).Parse(`
{{range seq 3}}{{.}}{{end}}
{{formatDate "2006" "2026-09-25"}}
{{formatTime "15:04" "2026-09-25T14:30:00Z"}}
{{relDate "2026-09-26T12:00:00Z"}}
{{json .}}
`)
	if err != nil {
		t.Fatalf("failed to parse template: %v", err)
	}

	var sb strings.Builder
	if err := tmpl.Execute(&sb, map[string]string{"foo": "bar"}); err != nil {
		t.Fatalf("failed to execute template: %v", err)
	}

	out := sb.String()
	if !strings.Contains(out, "012") {
		t.Errorf("expected sequence output '012', got %q", out)
	}
	if !strings.Contains(out, "2026") {
		t.Errorf("expected date output '2026', got %q", out)
	}
	if !strings.Contains(out, "14:30") {
		t.Errorf("expected time output '14:30', got %q", out)
	}
	if !strings.Contains(out, "Tomorrow") {
		t.Errorf("expected relative date 'Tomorrow', got %q", out)
	}

	// Default nowFunc fallback
	defaultFuncs := render.StandardFuncMap(nil)
	if defaultFuncs["seq"] == nil {
		t.Error("expected non-nil seq in defaultFuncs")
	}
}

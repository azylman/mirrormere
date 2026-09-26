package render_test

import (
	"html/template"
	"math"
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
		{"OpenMeteo short ISO string", "2026-09-26T15:00", true},
		{"OpenMeteo space short ISO string", "2026-09-26 15:00", true},
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
	if defaultFuncs["weatherIcon"] == nil {
		t.Error("expected non-nil weatherIcon in defaultFuncs")
	}
}

func TestWeatherIcon(t *testing.T) {
	t.Parallel()

	tokens := []string{
		"weather-sunny",
		"weather-partly-cloudy",
		"weather-cloudy",
		"weather-fog",
		"weather-rainy",
		"weather-pouring",
		"weather-snowy",
		"weather-snowy-rainy",
		"weather-lightning",
		"weather-lightning-rainy",
		"unknown-token",
	}

	for _, token := range tokens {
		t.Run(token, func(t *testing.T) {
			t.Parallel()
			svg := render.WeatherIcon(token)
			if !strings.HasPrefix(string(svg), "<svg") || !strings.HasSuffix(string(svg), "</svg>") {
				t.Fatalf("expected valid SVG element for %s, got: %s", token, svg)
			}
			if !strings.Contains(string(svg), `width="20"`) || !strings.Contains(string(svg), `height="20"`) {
				t.Errorf("expected default 20px dimensions for %s, got: %s", token, svg)
			}
		})
	}

	// Sizing tests
	sizeTests := []struct {
		name     string
		args     []any
		wantSize string
	}{
		{"no args", []any{}, `width="20"`},
		{"nil arg", []any{nil}, `width="20"`},
		{"custom int", []any{"weather-sunny", 32}, `width="32"`},
		{"custom int64", []any{"weather-sunny", int64(28)}, `width="28"`},
		{"custom float64", []any{"weather-sunny", float64(24)}, `width="24"`},
		{"custom string", []any{"weather-sunny", "36"}, `width="36"`},
		{"invalid string", []any{"weather-sunny", "bad-size"}, `width="20"`},
		{"clamp below min", []any{"weather-sunny", 5}, `width="12"`},
		{"clamp above max", []any{"weather-sunny", 120}, `width="64"`},
		{"negative int", []any{"weather-sunny", -10}, `width="20"`},
	}

	for _, tc := range sizeTests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svg := render.WeatherIcon(tc.args...)
			if !strings.Contains(string(svg), tc.wantSize) {
				t.Errorf("expected %s in output, got: %s", tc.wantSize, svg)
			}
		})
	}

	// Template execution test
	tmpl, err := template.New("icon-test").Funcs(render.StandardFuncMap(nil)).Parse(`{{weatherIcon "weather-partly-cloudy" 24}}`)
	if err != nil {
		t.Fatalf("failed to parse template: %v", err)
	}
	var sb strings.Builder
	if err := tmpl.Execute(&sb, nil); err != nil {
		t.Fatalf("failed to execute template: %v", err)
	}
	if !strings.Contains(sb.String(), `width="24"`) || !strings.Contains(sb.String(), `mm-icon-weather-partly-cloudy`) {
		t.Errorf("expected rendered SVG in template output, got: %s", sb.String())
	}
}

type testHourlyItem struct {
	Time       string
	Temp       float64
	PrecipProb int
	Icon       string
}

func TestWeatherHourlyGraph(t *testing.T) {
	t.Parallel()

	// Empty and invalid inputs
	emptyCases := []struct {
		name string
		args []any
	}{
		{"no args", []any{}},
		{"nil arg", []any{nil}},
		{"unsupported type string", []any{"invalid"}},
		{"unsupported type int", []any{123}},
		{"empty slice of any", []any{[]any{}}},
		{"empty typed slice", []any{[]testHourlyItem{}}},
		{"slice with all nil items", []any{[]any{nil, nil}}},
	}

	for _, tc := range emptyCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := render.WeatherHourlyGraph(tc.args...)
			if got != "" {
				t.Errorf("expected empty HTML, got: %s", got)
			}
		})
	}

	// Single item
	t.Run("single item", func(t *testing.T) {
		t.Parallel()
		items := []testHourlyItem{
			{Time: "2026-09-26T14:00", Temp: 72.0, PrecipProb: 0, Icon: "weather-sunny"},
		}
		got := string(render.WeatherHourlyGraph(items))
		if !strings.HasPrefix(got, "<svg") || !strings.HasSuffix(got, "</svg>") {
			t.Fatalf("expected valid SVG, got: %s", got)
		}
		if !strings.Contains(got, `72°`) {
			t.Errorf("expected temp label 72°, got: %s", got)
		}
		if !strings.Contains(got, `Now`) {
			t.Errorf("expected Now time label, got: %s", got)
		}
		// Single item should not render multi-point curve stroke path
		if strings.Contains(got, `<path d="M`) && strings.Contains(got, `stroke-width="2.5"`) {
			t.Errorf("single item should not render multi-point curve path, got: %s", got)
		}
	})

	// Flatline temperatures (max == min, span < 4)
	t.Run("flatline temperatures", func(t *testing.T) {
		t.Parallel()
		items := []testHourlyItem{
			{Time: "2026-09-26T14:00", Temp: 65.0, PrecipProb: 10, Icon: "weather-cloudy"},
			{Time: "2026-09-26T15:00", Temp: 65.0, PrecipProb: 10, Icon: "weather-cloudy"},
			{Time: "2026-09-26T16:00", Temp: 65.0, PrecipProb: 0, Icon: "weather-cloudy"},
		}
		got := string(render.WeatherHourlyGraph(items))
		if !strings.Contains(got, `stroke-width="2.5"`) {
			t.Errorf("expected curve path for multiple points, got: %s", got)
		}
		if !strings.Contains(got, `10%`) {
			t.Errorf("expected precip label 10%%, got: %s", got)
		}
		if !strings.Contains(got, `3 PM`) || !strings.Contains(got, `4 PM`) {
			t.Errorf("expected formatted times 3 PM and 4 PM, got: %s", got)
		}
	})

	// Varied multi-hour with negative and zero-crossing temperatures
	t.Run("negative and zero-crossing temperatures", func(t *testing.T) {
		t.Parallel()
		items := []testHourlyItem{
			{Time: "2026-09-26T00:00", Temp: -5.0, PrecipProb: 50, Icon: "weather-snowy"},
			{Time: "2026-09-26T01:00", Temp: 0.0, PrecipProb: 30, Icon: "weather-snowy-rainy"},
			{Time: "2026-09-26T02:00", Temp: 10.0, PrecipProb: 0, Icon: "weather-sunny"},
		}
		got := string(render.WeatherHourlyGraph(items, "custom-id"))
		if !strings.Contains(got, `id="mm-weather-grad-custom-id"`) {
			t.Errorf("expected custom gradient ID in defs, got: %s", got)
		}
		if !strings.Contains(got, `-5°`) || !strings.Contains(got, `0°`) || !strings.Contains(got, `10°`) {
			t.Errorf("expected negative and positive temp labels, got: %s", got)
		}
		if !strings.Contains(got, `50%`) {
			t.Errorf("expected 50%% precip label, got: %s", got)
		}
	})

	// Map slice and NaN/Inf handling
	t.Run("map slice with NaN and unparseable time", func(t *testing.T) {
		t.Parallel()
		items := []map[string]any{
			{"time": "2026-09-26T14:00", "temp": math.NaN(), "precip_prob": 25.0, "icon": ""},
			{"time": "custom-time-format", "temp": math.Inf(1), "precip_prob": 0, "icon": "weather-partly-cloudy"},
			{"time": "2026-09-26T16:00", "temp": 70, "precip_prob": 40, "icon": "weather-rainy"},
		}
		got := string(render.WeatherHourlyGraph(items))
		if strings.Contains(got, "NaN") || strings.Contains(got, "Inf") {
			t.Errorf("output should never contain literal NaN or Inf, got: %s", got)
		}
		if !strings.Contains(got, "custom-time-format") {
			t.Errorf("expected unparseable time fallback, got: %s", got)
		}
	})

	// Full template execution via StandardFuncMap
	t.Run("template execution", func(t *testing.T) {
		t.Parallel()
		data := map[string]any{
			"Hourly": []testHourlyItem{
				{Time: "2026-09-26T12:00", Temp: 68.0, PrecipProb: 0, Icon: "weather-sunny"},
				{Time: "2026-09-26T13:00", Temp: 71.0, PrecipProb: 20, Icon: "weather-partly-cloudy"},
			},
			"ID": "widget-123",
		}
		tmpl, err := template.New("graph-test").Funcs(render.StandardFuncMap(nil)).Parse(`{{weatherHourlyGraph .Hourly .ID}}`)
		if err != nil {
			t.Fatalf("failed to parse template: %v", err)
		}
		var sb strings.Builder
		if err := tmpl.Execute(&sb, data); err != nil {
			t.Fatalf("failed to execute template: %v", err)
		}
		out := sb.String()
		if !strings.Contains(out, `class="weather-hourly-graph"`) {
			t.Errorf("expected weather-hourly-graph class in template output, got: %s", out)
		}
		if !strings.Contains(out, `id="mm-weather-grad-widget-123"`) {
			t.Errorf("expected namespaced gradient ID, got: %s", out)
		}
		if !strings.Contains(out, `68°`) || !strings.Contains(out, `71°`) {
			t.Errorf("expected temp labels in output, got: %s", out)
		}
	})
}

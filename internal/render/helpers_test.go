package render_test

import (
	"html/template"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/provider"
	"github.com/azylman/mirrormere/internal/render"
	"github.com/azylman/mirrormere/internal/tasks"
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

	t.Run("timezone conversion", func(t *testing.T) {
		t.Parallel()
		laLoc, err := time.LoadLocation("America/Los_Angeles")
		if err != nil {
			t.Fatalf("failed to load America/Los_Angeles: %v", err)
		}

		// Event on Sunday night Oct 11 at 8:30 PM PDT is 2026-10-12T03:30:00Z in UTC
		eventUTC := "2026-10-12T03:30:00Z"

		// On Saturday Oct 10 at 7:00 PM PDT, the event is tomorrow (Sunday Oct 11)
		satNow := time.Date(2026, 10, 10, 19, 0, 0, 0, laLoc)
		if got := render.RelDate(satNow, eventUTC); got != "Tomorrow" {
			t.Errorf("expected Tomorrow on Saturday night, got %q", got)
		}

		// On Sunday Oct 11 at 2:00 PM PDT, the event is today
		sunNow := time.Date(2026, 10, 11, 14, 0, 0, 0, laLoc)
		if got := render.RelDate(sunNow, eventUTC); got != "Today" {
			t.Errorf("expected Today on Sunday afternoon, got %q", got)
		}

		// FormatDateInLocation converts UTC event to local date
		if got := render.FormatDateInLocation(laLoc, "2006-01-02", eventUTC); got != "2026-10-11" {
			t.Errorf("expected 2026-10-11, got %q", got)
		}
		if got := render.FormatDateInLocation(laLoc, "Jan 2", eventUTC); got != "Oct 11" {
			t.Errorf("expected Oct 11, got %q", got)
		}

		// Date-only string is preserved and not shifted
		if got := render.FormatDateInLocation(laLoc, "2006-01-02", "2026-10-12"); got != "2026-10-12" {
			t.Errorf("expected date-only 2026-10-12 to remain 2026-10-12, got %q", got)
		}

		// FormatTimeInLocation converts UTC event to local time
		if got := render.FormatTimeInLocation(laLoc, "3:04", eventUTC); got != "8:30" {
			t.Errorf("expected 8:30, got %q", got)
		}
		if got := render.FormatTimeInLocation(laLoc, "PM", eventUTC); got != "PM" {
			t.Errorf("expected PM, got %q", got)
		}
	})
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

	t.Run("tightened vertical coordinates", func(t *testing.T) {
		t.Parallel()
		items := []testHourlyItem{
			{Time: "2026-09-26T12:00", Temp: 80.0, PrecipProb: 40, Icon: "weather-rainy"},
			{Time: "2026-09-26T13:00", Temp: 50.0, PrecipProb: 0, Icon: "weather-cloudy"},
		}
		got := string(render.WeatherHourlyGraph(items))
		// Check svg height 124.0 in viewBox
		if !strings.Contains(got, `viewBox="0 0 560.0 124.0"`) {
			t.Errorf("expected viewBox with height 124.0, got: %s", got)
		}
		// Check baseline axis y = 96.0
		if !strings.Contains(got, `y1="96.0"`) || !strings.Contains(got, `y2="96.0"`) {
			t.Errorf("expected baseline axis at y=96.0, got: %s", got)
		}
		// Check precip label at y = 13, font-size 10
		if !strings.Contains(got, `y="13" text-anchor="middle" font-size="10"`) {
			t.Errorf("expected precip text at y=13 with font-size 10, got: %s", got)
		}
		// Check icon transform at y=22 scale(1.0)
		if !strings.Contains(got, `, 22) scale(1.0)`) {
			t.Errorf("expected icon transform at y=22 scale(1.0), got: %s", got)
		}
		// Check circle r=3.5
		if !strings.Contains(got, `r="3.5"`) {
			t.Errorf("expected point circle with r=3.5, got: %s", got)
		}
		// Check time label at y = 114, font-size 11
		if !strings.Contains(got, `y="114" text-anchor="middle" font-size="11"`) {
			t.Errorf("expected time label at y=114 with font-size 11, got: %s", got)
		}
		// High temp y-coord is minY = 52.0. Temp label y = pt.y - 7.0 = 45.0.
		if !strings.Contains(got, `y="45.0" text-anchor="middle" font-size="12" font-weight="800"`) {
			t.Errorf("expected high temp label at y=45.0 with font-size 12, got: %s", got)
		}
		// Low temp y-coord is maxY = 82.0. Temp label y = pt.y - 7.0 = 75.0.
		if !strings.Contains(got, `y="75.0" text-anchor="middle" font-size="12" font-weight="800"`) {
			t.Errorf("expected low temp label at y=75.0 with font-size 12, got: %s", got)
		}
	})
}

func TestWeatherDailyForecast(t *testing.T) {
	t.Parallel()

	t.Run("nil and empty inputs", func(t *testing.T) {
		t.Parallel()
		if got := render.WeatherDailyForecast(nil); got != nil {
			t.Errorf("expected nil for nil input, got: %v", got)
		}
		if got := render.WeatherDailyForecast([]provider.WeatherDaily{}); got != nil {
			t.Errorf("expected nil for empty slice, got: %v", got)
		}
		if got := render.WeatherDailyForecast("not-a-slice"); got != nil {
			t.Errorf("expected nil for non-slice, got: %v", got)
		}
	})

	t.Run("single item slice returns nil", func(t *testing.T) {
		t.Parallel()
		single := []provider.WeatherDaily{
			{Date: "2026-10-11", TempMin: 50, TempMax: 70, ConditionText: "Clear", Icon: "weather-sunny"},
		}
		if got := render.WeatherDailyForecast(single); got != nil {
			t.Errorf("expected nil when slice has <= 1 item (today only), got: %v", got)
		}
	})

	t.Run("multi-day forecast with day names and default maxDays", func(t *testing.T) {
		t.Parallel()
		daily := []provider.WeatherDaily{
			{Date: "2026-10-11", TempMin: 55, TempMax: 75, PrecipProbMax: 0},
			{Date: "2026-10-12", TempMin: 50, TempMax: 70, PrecipProbMax: 20, ConditionText: "Sunny", Icon: "weather-sunny"},
			{Date: "2026-10-13", TempMin: 45, TempMax: 65, PrecipProbMax: 0, ConditionText: "Cloudy", Icon: "weather-cloudy"},
			{Date: "2026-10-14", TempMin: 40, TempMax: 60, PrecipProbMax: 80, ConditionText: "Rain", Icon: "weather-rainy"},
			{Date: "2026-10-15", TempMin: 42, TempMax: 62, PrecipProbMax: 10, ConditionText: "Partly Cloudy", Icon: "weather-partly-cloudy"},
			{Date: "2026-10-16", TempMin: 48, TempMax: 68, PrecipProbMax: 0, ConditionText: "Clear", Icon: "weather-sunny"},
			{Date: "2026-10-17", TempMin: 52, TempMax: 72, PrecipProbMax: 0, ConditionText: "Sunny", Icon: "weather-sunny"},
		}

		got := render.WeatherDailyForecast(daily)
		if len(got) != 5 {
			t.Fatalf("expected 5 days, got %d", len(got))
		}

		if got[0].DayName != "Tomorrow" {
			t.Errorf("expected index 0 day name 'Tomorrow', got %q", got[0].DayName)
		}
		if got[1].DayName != "Tue" {
			t.Errorf("expected index 1 day name 'Tue', got %q", got[1].DayName)
		}
		if got[2].DayName != "Wed" {
			t.Errorf("expected index 2 day name 'Wed', got %q", got[2].DayName)
		}
		if got[3].DayName != "Thu" {
			t.Errorf("expected index 3 day name 'Thu', got %q", got[3].DayName)
		}
		if got[4].DayName != "Fri" {
			t.Errorf("expected index 4 day name 'Fri', got %q", got[4].DayName)
		}

		if !got[0].HasPrecip || got[0].PrecipProbMax != 20 {
			t.Errorf("expected HasPrecip=true with 20%%, got %v (%d)", got[0].HasPrecip, got[0].PrecipProbMax)
		}
		if got[1].HasPrecip {
			t.Errorf("expected HasPrecip=false for 0%%, got true")
		}
		if !got[2].HasPrecip || got[2].PrecipProbMax != 80 {
			t.Errorf("expected HasPrecip=true with 80%%, got %v (%d)", got[2].HasPrecip, got[2].PrecipProbMax)
		}

		if math.Abs(got[2].BarLeft-0.0) > 0.01 {
			t.Errorf("expected barLeft ~0.0%% for overallMin, got %f", got[2].BarLeft)
		}
		if math.Abs(got[2].BarWidth-66.666) > 0.1 {
			t.Errorf("expected barWidth ~66.67%%, got %f", got[2].BarWidth)
		}
		for i, item := range got {
			if item.BarLeft < 0 || item.BarLeft > 95 {
				t.Errorf("at index %d: barLeft %f out of bounds [0, 95]", i, item.BarLeft)
			}
			if item.BarWidth < 5 {
				t.Errorf("at index %d: barWidth %f less than minimum 5%%", i, item.BarWidth)
			}
			if item.BarLeft+item.BarWidth > 100.001 {
				t.Errorf("at index %d: barLeft + barWidth (%f + %f = %f) exceeds 100%%", i, item.BarLeft, item.BarWidth, item.BarLeft+item.BarWidth)
			}
		}
	})

	t.Run("flatline temperatures with span < 2", func(t *testing.T) {
		t.Parallel()
		daily := []provider.WeatherDaily{
			{Date: "2026-10-11", TempMin: 68, TempMax: 68},
			{Date: "2026-10-12", TempMin: 68, TempMax: 68},
			{Date: "2026-10-13", TempMin: 68, TempMax: 68},
		}
		got := render.WeatherDailyForecast(daily)
		if len(got) != 2 {
			t.Fatalf("expected 2 days, got %d", len(got))
		}
		for i, item := range got {
			if math.IsNaN(item.BarLeft) || math.IsNaN(item.BarWidth) {
				t.Errorf("index %d: geometry contains NaN", i)
			}
			if item.BarWidth < 5.0 {
				t.Errorf("index %d: expected minimum 5%% barWidth on flat temps, got %f", i, item.BarWidth)
			}
			if item.BarLeft+item.BarWidth > 100.0 {
				t.Errorf("index %d: bar exceeds 100%%", i)
			}
		}
	})

	t.Run("negative temperatures and inverted min max", func(t *testing.T) {
		t.Parallel()
		daily := []provider.WeatherDaily{
			{Date: "2026-01-01", TempMin: 10, TempMax: 20},
			{Date: "2026-01-02", TempMin: -5, TempMax: -15, Icon: "weather-snowy"},
			{Date: "2026-01-03", TempMin: -20, TempMax: -10, Icon: "weather-snowy"},
		}
		got := render.WeatherDailyForecast(daily)
		if len(got) != 2 {
			t.Fatalf("expected 2 items, got %d", len(got))
		}
		if got[0].TempMin != -15 || got[0].TempMax != -5 {
			t.Errorf("expected inverted temperatures to be normalized to min -15, max -5; got min %f, max %f", got[0].TempMin, got[0].TempMax)
		}
		if got[0].DayName != "Tomorrow" {
			t.Errorf("expected Tomorrow, got %s", got[0].DayName)
		}
		if got[1].DayName != "Sat" {
			t.Errorf("expected Sat, got %s", got[1].DayName)
		}
		if math.IsNaN(got[0].BarLeft) || math.IsNaN(got[0].BarWidth) {
			t.Errorf("negative temp geometry contains NaN")
		}
	})

	t.Run("unparseable date fallback and custom maxDays", func(t *testing.T) {
		t.Parallel()
		daily := []provider.WeatherDaily{
			{Date: "today", TempMin: 50, TempMax: 60},
			{Date: "unparseable-date-1", TempMin: 50, TempMax: 60},
			{Date: "unparseable-date-2", TempMin: 52, TempMax: 62},
			{Date: "unparseable-date-3", TempMin: 54, TempMax: 64},
		}
		got := render.WeatherDailyForecast(daily, 2)
		if len(got) != 2 {
			t.Fatalf("expected 2 items, got %d", len(got))
		}
		if got[0].DayName != "Tomorrow" {
			t.Errorf("expected Tomorrow for first upcoming, got %s", got[0].DayName)
		}
		if got[1].DayName != "unparseable-date-2" {
			t.Errorf("expected fallback to raw Date, got %s", got[1].DayName)
		}
	})

	t.Run("pointer and map slice support", func(t *testing.T) {
		t.Parallel()
		slice := []provider.WeatherDaily{
			{Date: "2026-10-11", TempMin: 50, TempMax: 60},
			{Date: "2026-10-12", TempMin: 52, TempMax: 62, Icon: "weather-sunny", ConditionText: "Sunny"},
		}
		gotPtr := render.WeatherDailyForecast(&slice)
		if len(gotPtr) != 1 {
			t.Fatalf("expected 1 item from pointer slice, got %d", len(gotPtr))
		}

		mapSlice := []map[string]any{
			{"date": "2026-10-11", "temp_min": 50, "temp_max": 60},
			{"date": "2026-10-12", "temp_min": 45.0, "temp_max": 65.0, "precip_prob_max": 30, "condition_text": "Rain", "icon": "weather-rainy"},
		}
		gotMap := render.WeatherDailyForecast(mapSlice)
		if len(gotMap) != 1 {
			t.Fatalf("expected 1 item from map slice, got %d", len(gotMap))
		}
		if gotMap[0].DayName != "Tomorrow" || gotMap[0].ConditionText != "Rain" || gotMap[0].Icon != "weather-rainy" || !gotMap[0].HasPrecip {
			t.Errorf("unexpected map slice item: %+v", gotMap[0])
		}
	})

	t.Run("template execution via StandardFuncMap", func(t *testing.T) {
		t.Parallel()
		data := map[string]any{
			"Daily": []provider.WeatherDaily{
				{Date: "2026-10-11", TempMin: 50, TempMax: 60},
				{Date: "2026-10-12", TempMin: 55, TempMax: 65, PrecipProbMax: 20, Icon: "weather-sunny", ConditionText: "Sunny"},
				{Date: "2026-10-13", TempMin: 52, TempMax: 62, PrecipProbMax: 0, Icon: "weather-cloudy", ConditionText: "Cloudy"},
			},
		}
		tmplStr := `{{ $f := weatherDailyForecast .Daily 5 }}{{ range $f }}{{ .DayName }}: {{ printf "%.0f" .TempMin }}°-{{ printf "%.0f" .TempMax }}° ({{ printf "%.1f" .BarLeft }}%, {{ printf "%.1f" .BarWidth }}%) {{ end }}`
		tmpl, err := template.New("forecast-test").Funcs(render.StandardFuncMap(nil)).Parse(tmplStr)
		if err != nil {
			t.Fatalf("template parse error: %v", err)
		}
		var sb strings.Builder
		if err := tmpl.Execute(&sb, data); err != nil {
			t.Fatalf("template exec error: %v", err)
		}
		out := sb.String()
		if !strings.Contains(out, "Tomorrow: 55°-65°") || !strings.Contains(out, "Tue: 52°-62°") {
			t.Errorf("unexpected template output: %s", out)
		}
	})

	t.Run("nil pointer and invalid slice elements", func(t *testing.T) {
		t.Parallel()
		var nilPtr *[]provider.WeatherDaily
		if got := render.WeatherDailyForecast(nilPtr); got != nil {
			t.Errorf("expected nil for nil pointer, got %v", got)
		}

		type customDaily struct {
			Date          string
			TempMax       float32
			TempMin       float32
			PrecipProbMax int64
			ConditionText string
			Icon          string
		}
		type customDailyInt struct {
			Date          string
			TempMax       int
			TempMin       int
			PrecipProbMax float32
			ConditionText string
			Icon          string
		}

		mixedSlice := []any{
			nil,
			(*customDaily)(nil),
			&customDaily{Date: "2026-10-11", TempMin: 50, TempMax: 60, PrecipProbMax: 0, ConditionText: "Clear", Icon: "weather-sunny"},
			customDailyInt{Date: "2026-10-12", TempMin: 40, TempMax: 65, PrecipProbMax: 30, ConditionText: "Rain", Icon: ""},
			&provider.WeatherDaily{Date: "2026-10-13", TempMin: 45, TempMax: math.NaN(), PrecipProbMax: 10, ConditionText: "Cloudy"},
		}
		got := render.WeatherDailyForecast(mixedSlice)
		if len(got) < 2 {
			t.Fatalf("expected at least 2 items from mixed slice, got %d", len(got))
		}
		if got[0].DayName != "Tomorrow" || got[0].Icon != "weather-cloudy" {
			t.Errorf("expected default weather-cloudy icon on empty icon item, got %s", got[0].Icon)
		}
	})

	t.Run("barLeft clamping to 95 and non-positive maxDays", func(t *testing.T) {
		t.Parallel()
		daily := []provider.WeatherDaily{
			{Date: "2026-10-11", TempMin: 50, TempMax: 60}, // today
			{Date: "2026-10-12", TempMin: 0, TempMax: 10},  // upcoming 1 (overallMin = 0)
			{Date: "2026-10-13", TempMin: 98, TempMax: 100}, // upcoming 2 (overallMax = 100, barLeft = 98% -> clamped to 95%)
		}
		got := render.WeatherDailyForecast(daily, 0)
		if len(got) != 2 {
			t.Fatalf("expected 2 items, got %d", len(got))
		}
		if got[1].BarLeft != 95.0 {
			t.Errorf("expected barLeft to clamp to 95.0, got %f", got[1].BarLeft)
		}

		gotNeg := render.WeatherDailyForecast(daily, -5)
		if len(gotNeg) != 2 {
			t.Fatalf("expected 2 items with negative maxDays, got %d", len(gotNeg))
		}
	})

	t.Run("map slice with integer temps and float precip", func(t *testing.T) {
		t.Parallel()
		maps := []map[string]any{
			{"date": "2026-10-11", "temp_min": 50, "temp_max": 70, "precip_prob_max": 10},
			{"date": "2026-10-12", "temp_min": 45, "temp_max": 65, "precip_prob_max": 50.0},
		}
		got := render.WeatherDailyForecast(maps)
		if len(got) != 1 {
			t.Fatalf("expected 1 item, got %d", len(got))
		}
		if got[0].PrecipProbMax != 50 || got[0].TempMin != 45 || got[0].TempMax != 65 {
			t.Errorf("unexpected parsed values from int map: %+v", got[0])
		}
	})
}

func TestTodayPrecipProb(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data any
		want int
	}{
		{"nil data", nil, 0},
		{"WeatherSnapshot with daily", provider.WeatherSnapshot{Daily: []provider.WeatherDaily{{PrecipProbMax: 65}}}, 65},
		{"WeatherSnapshot with current only", provider.WeatherSnapshot{Current: provider.WeatherCurrent{PrecipProbMax: 40}}, 40},
		{"*WeatherSnapshot pointer", &provider.WeatherSnapshot{Daily: []provider.WeatherDaily{{PrecipProbMax: 80}}}, 80},
		{"nil *WeatherSnapshot pointer", (*provider.WeatherSnapshot)(nil), 0},
		{"WeatherCurrent value", provider.WeatherCurrent{PrecipProbMax: 35}, 35},
		{"*WeatherCurrent pointer", &provider.WeatherCurrent{PrecipProbMax: 45}, 45},
		{"nil *WeatherCurrent pointer", (*provider.WeatherCurrent)(nil), 0},
		{"[]WeatherDaily slice", []provider.WeatherDaily{{PrecipProbMax: 90}}, 90},
		{"empty []WeatherDaily slice", []provider.WeatherDaily{}, 0},
		{"map with daily slice", map[string]any{"daily": []any{map[string]any{"precip_prob_max": 50}}}, 50},
		{"map with current map", map[string]any{"current": map[string]any{"precip_prob_max": 25}}, 25},
		{"map with WeatherDaily slice", map[string]any{"daily": []provider.WeatherDaily{{PrecipProbMax: 70}}}, 70},
		{"map with WeatherCurrent", map[string]any{"current": provider.WeatherCurrent{PrecipProbMax: 15}}, 15},
		{"map with direct precip_prob_max", map[string]any{"precip_prob_max": 85}, 85},
		{"map with direct float precip_prob_max", map[string]any{"precip_prob_max": float64(85)}, 85},
		{"unrelated type", "not-weather-data", 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := render.TodayPrecipProb(tc.data); got != tc.want {
				t.Errorf("TodayPrecipProb(%v) = %d, want %d", tc.name, got, tc.want)
			}
		})
	}

	t.Run("template execution via StandardFuncMap", func(t *testing.T) {
		t.Parallel()
		tmpl, err := template.New("precip-test").Funcs(render.StandardFuncMap(nil)).Parse(`{{todayPrecipProb .}}%`)
		if err != nil {
			t.Fatalf("failed to parse template: %v", err)
		}
		var sb strings.Builder
		snap := provider.WeatherSnapshot{Daily: []provider.WeatherDaily{{PrecipProbMax: 42}}}
		if err := tmpl.Execute(&sb, snap); err != nil {
			t.Fatalf("failed to execute template: %v", err)
		}
		if sb.String() != "42%" {
			t.Errorf("expected '42%%', got %q", sb.String())
		}
	})
}

func TestCalendarFeedEvents(t *testing.T) {
	t.Parallel()

	refTime := time.Date(2026, 10, 10, 15, 30, 0, 0, time.UTC)

	tests := []struct {
		name       string
		data       any
		refTime    time.Time
		wantTitles []string
	}{
		{
			name:       "nil data",
			data:       nil,
			refTime:    refTime,
			wantTitles: nil,
		},
		{
			name:       "empty events slice",
			data:       provider.CalendarSnapshot{Events: []provider.CalendarEvent{}},
			refTime:    refTime,
			wantTitles: nil,
		},
		{
			name: "filters out past events from 86 days ago, yesterday, and earlier today",
			data: provider.CalendarSnapshot{
				Events: []provider.CalendarEvent{
					{
						Title:  "Past 86 Days Ago Timed",
						Start:  "2026-07-16T10:00:00Z",
						End:    "2026-07-16T11:00:00Z",
						AllDay: false,
					},
					{
						Title:  "Past 86 Days Ago All-Day",
						Start:  "2026-07-16",
						End:    "2026-07-16",
						AllDay: true,
					},
					{
						Title:  "Yesterday Timed",
						Start:  "2026-10-09T20:00:00Z",
						End:    "2026-10-09T21:00:00Z",
						AllDay: false,
					},
					{
						Title:  "Yesterday All-Day",
						Start:  "2026-10-09",
						End:    "2026-10-09",
						AllDay: true,
					},
					{
						Title:  "Earlier Today Finished Timed",
						Start:  "2026-10-10T13:00:00Z",
						End:    "2026-10-10T14:00:00Z",
						AllDay: false,
					},
					{
						Title:  "Currently Ongoing Timed",
						Start:  "2026-10-10T15:00:00Z",
						End:    "2026-10-10T16:00:00Z",
						AllDay: false,
					},
					{
						Title:  "Starting Exactly Now Timed",
						Start:  "2026-10-10T15:30:00Z",
						End:    "2026-10-10T16:30:00Z",
						AllDay: false,
					},
					{
						Title:  "Later Today Timed",
						Start:  "2026-10-10T18:00:00Z",
						End:    "2026-10-10T19:00:00Z",
						AllDay: false,
					},
					{
						Title:  "Today All-Day",
						Start:  "2026-10-10",
						End:    "2026-10-10",
						AllDay: true,
					},
					{
						Title:  "Multi-Day All-Day Spanning Today",
						Start:  "2026-10-08",
						End:    "2026-10-12",
						AllDay: true,
					},
					{
						Title:  "Tomorrow All-Day",
						Start:  "2026-10-11",
						End:    "2026-10-11",
						AllDay: true,
					},
					{
						Title:  "Future Timed",
						Start:  "2026-10-12T09:00:00Z",
						End:    "2026-10-12T10:00:00Z",
						AllDay: false,
					},
				},
			},
			refTime: refTime,
			wantTitles: []string{
				"Currently Ongoing Timed",
				"Starting Exactly Now Timed",
				"Later Today Timed",
				"Today All-Day",
				"Multi-Day All-Day Spanning Today",
				"Tomorrow All-Day",
				"Future Timed",
			},
		},
		{
			name: "fallback when End time is missing",
			data: &provider.CalendarSnapshot{
				Events: []provider.CalendarEvent{
					{
						Title:  "Past Missing End",
						Start:  "2026-10-10T13:00:00Z",
						AllDay: false,
					},
					{
						Title:  "Future Missing End",
						Start:  "2026-10-10T16:00:00Z",
						AllDay: false,
					},
				},
			},
			refTime:    refTime,
			wantTitles: []string{"Future Missing End"},
		},
		{
			name: "supports map[string]any payload",
			data: map[string]any{
				"events": []any{
					map[string]any{
						"title":   "Map Past",
						"start":   "2026-07-01T10:00:00Z",
						"end":     "2026-07-01T11:00:00Z",
						"all_day": false,
					},
					map[string]any{
						"title":   "Map Future",
						"start":   "2026-10-10T17:00:00Z",
						"end":     "2026-10-10T18:00:00Z",
						"all_day": false,
					},
				},
			},
			refTime:    refTime,
			wantTitles: []string{"Map Future"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := render.CalendarFeedEvents(tc.data, tc.refTime)
			var gotTitles []string
			for _, ev := range got {
				gotTitles = append(gotTitles, ev.Title)
			}
			if len(gotTitles) != len(tc.wantTitles) {
				t.Fatalf("CalendarFeedEvents() got %d events (%v), want %d (%v)",
					len(gotTitles), gotTitles, len(tc.wantTitles), tc.wantTitles)
			}
			for i := range gotTitles {
				if gotTitles[i] != tc.wantTitles[i] {
					t.Errorf("at index %d: got title %q, want %q", i, gotTitles[i], tc.wantTitles[i])
				}
			}
		})
	}

	t.Run("template execution via StandardFuncMap", func(t *testing.T) {
		t.Parallel()
		fnMap := render.StandardFuncMap(func() time.Time { return refTime })
		tmpl, err := template.New("feed-test").Funcs(fnMap).Parse(`{{ range (calendarFeedEvents .) }}{{ .Title }};{{ end }}`)
		if err != nil {
			t.Fatalf("failed to parse template: %v", err)
		}

		snap := provider.CalendarSnapshot{
			Events: []provider.CalendarEvent{
				{Title: "Old", Start: "2026-07-01T10:00:00Z", End: "2026-07-01T11:00:00Z"},
				{Title: "Upcoming", Start: "2026-10-10T16:00:00Z", End: "2026-10-10T17:00:00Z"},
			},
		}

		var sb strings.Builder
		if err := tmpl.Execute(&sb, snap); err != nil {
			t.Fatalf("failed to execute template: %v", err)
		}
		if sb.String() != "Upcoming;" {
			t.Errorf("expected 'Upcoming;', got %q", sb.String())
		}
	})
}

func TestCalendarFeedActiveCalendars(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     any
		wantCount int
		wantNames []string
	}{
		{
			name:      "nil input",
			input:     nil,
			wantCount: 0,
		},
		{
			name:      "empty map",
			input:     map[string]any{},
			wantCount: 0,
		},
		{
			name: "config map with enabled and disabled feeds",
			input: map[string]any{
				"calendars": []any{
					map[string]any{"name": "Alex", "color": "#ff6f59"},
					map[string]any{"name": "Disabled Feed", "color": "#cccccc", "enabled": false},
					map[string]any{"name": "Amelie", "color": "#9d8df1", "enabled": true},
					map[string]any{"name": "Explicit False String", "color": "#000000", "enabled": "false"},
				},
			},
			wantCount: 2,
			wantNames: []string{"Alex", "Amelie"},
		},
		{
			name: "slice of any directly",
			input: []any{
				map[string]any{"name": "Feed 1", "enabled": true},
				map[string]any{"name": "Feed 2", "enabled": false},
			},
			wantCount: 1,
			wantNames: []string{"Feed 1"},
		},
		{
			name: "slice of map[string]any directly",
			input: []map[string]any{
				{"name": "Feed A", "enabled": true},
				{"name": "Feed B", "enabled": false},
			},
			wantCount: 1,
			wantNames: []string{"Feed A"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := render.CalendarFeedActiveCalendars(tt.input)
			if len(got) != tt.wantCount {
				t.Fatalf("CalendarFeedActiveCalendars() returned %d items, want %d", len(got), tt.wantCount)
			}
			for i, name := range tt.wantNames {
				if got[i]["name"] != name {
					t.Errorf("at index %d: got name %v, want %s", i, got[i]["name"], name)
				}
			}
		})
	}

	t.Run("template execution via StandardFuncMap", func(t *testing.T) {
		t.Parallel()
		fnMap := render.StandardFuncMap(nil)
		tmpl, err := template.New("active-test").Funcs(fnMap).Parse(`{{ $c := calendarFeedActiveCalendars . }}{{ len $c }}:{{ range $c }}{{ .name }};{{ end }}`)
		if err != nil {
			t.Fatalf("failed to parse template: %v", err)
		}

		cfg := map[string]any{
			"calendars": []any{
				map[string]any{"name": "Cal 1", "enabled": true},
				map[string]any{"name": "Cal 2", "enabled": false},
			},
		}

		var sb strings.Builder
		if err := tmpl.Execute(&sb, cfg); err != nil {
			t.Fatalf("failed to execute template: %v", err)
		}
		if sb.String() != "1:Cal 1;" {
			t.Errorf("expected '1:Cal 1;', got %q", sb.String())
		}
	})
}

func TestTasksStats(t *testing.T) {
	t.Parallel()

	t.Run("nil input", func(t *testing.T) {
		t.Parallel()
		stats := render.TasksStats(nil)
		if stats.Total != 0 || stats.Done != 0 || stats.Summary != "" {
			t.Errorf("unexpected stats for nil: %+v", stats)
		}
	})

	t.Run("typed ListItem slice", func(t *testing.T) {
		t.Parallel()
		items := []tasks.ListItem{
			{Title: "Task 1", Done: true},
			{Title: "Task 2", Done: true},
			{Title: "Task 3", Done: true},
			{Title: "Task 4", Done: false},
		}
		stats := render.TasksStats(items)
		if stats.Total != 4 {
			t.Errorf("expected Total 4, got %d", stats.Total)
		}
		if stats.Done != 3 {
			t.Errorf("expected Done 3, got %d", stats.Done)
		}
		if stats.Remaining != 1 {
			t.Errorf("expected Remaining 1, got %d", stats.Remaining)
		}
		if stats.Summary != "1 to do" {
			t.Errorf("expected Summary '1 to do', got %q", stats.Summary)
		}
	})

	t.Run("multiple remaining tasks", func(t *testing.T) {
		t.Parallel()
		items := []tasks.ListItem{
			{Title: "Task 1", Done: true},
			{Title: "Task 2", Done: false},
			{Title: "Task 3", Done: false},
		}
		stats := render.TasksStats(items)
		if stats.Summary != "2 to do" {
			t.Errorf("expected '2 to do', got %q", stats.Summary)
		}
	})

	t.Run("all tasks completed", func(t *testing.T) {
		t.Parallel()
		items := []tasks.ListItem{
			{Title: "Task 1", Done: true},
			{Title: "Task 2", Done: true},
		}
		stats := render.TasksStats(items)
		if stats.Summary != "All done" {
			t.Errorf("expected 'All done', got %q", stats.Summary)
		}
	})

	t.Run("zero tasks completed", func(t *testing.T) {
		t.Parallel()
		items := []tasks.ListItem{
			{Title: "Task 1", Done: false},
		}
		stats := render.TasksStats(items)
		if stats.Summary != "1 to do" {
			t.Errorf("expected '1 to do', got %q", stats.Summary)
		}
	})

	t.Run("template execution via StandardFuncMap", func(t *testing.T) {
		t.Parallel()
		fnMap := render.StandardFuncMap(nil)
		tmpl, err := template.New("tasks-test").Funcs(fnMap).Parse(`{{ $s := tasksStats . }}{{ $s.Summary }}`)
		if err != nil {
			t.Fatalf("failed to parse template: %v", err)
		}
		items := []map[string]any{
			{"done": true},
			{"done": false},
		}
		var sb strings.Builder
		if err := tmpl.Execute(&sb, items); err != nil {
			t.Fatalf("failed to execute template: %v", err)
		}
		if sb.String() != "1 to do" {
			t.Errorf("expected '1 to do', got %q", sb.String())
		}
	})
}

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
		{name: "empty args", args: []int{}, want: []int{}},
		{name: "seq 0", args: []int{0}, want: []int{}},
		{name: "seq negative", args: []int{-3}, want: []int{}},
		{name: "seq 5", args: []int{5}, want: []int{0, 1, 2, 3, 4}},
		{name: "seq 1 to 5", args: []int{1, 5}, want: []int{1, 2, 3, 4, 5}},
		{name: "seq 5 to 5", args: []int{5, 5}, want: []int{5}},
		{name: "seq 5 to 1 (descending)", args: []int{5, 1}, want: []int{}},
		{name: "seq 0 to 10 step 2", args: []int{0, 10, 2}, want: []int{0, 2, 4, 6, 8, 10}},
		{name: "seq 1 to 5 step 0", args: []int{1, 5, 0}, want: []int{}},
		{name: "seq 1 to 5 negative step", args: []int{1, 5, -1}, want: []int{}},
		{name: "seq 5 to 1 step 2", args: []int{5, 1, 2}, want: []int{}},
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
		{name: "time.Time", val: now, wantValid: true},
		{name: "*time.Time non-nil", val: &now, wantValid: true},
		{name: "*time.Time nil", val: (*time.Time)(nil), wantValid: false},
		{name: "RFC3339 string", val: "2026-09-25T20:00:00Z", wantValid: true},
		{name: "RFC3339Nano string", val: "2026-09-25T20:00:00.123456789Z", wantValid: true},
		{name: "DateTime string", val: "2026-09-25 20:00:00", wantValid: true},
		{name: "DateOnly string", val: "2026-09-25", wantValid: true},
		{name: "TimeOnly string", val: "20:00:00", wantValid: true},
		{name: "ShortTime string", val: "20:00", wantValid: true},
		{name: "OpenMeteo short ISO string", val: "2026-09-26T15:00", wantValid: true},
		{name: "OpenMeteo space short ISO string", val: "2026-09-26 15:00", wantValid: true},
		{name: "12h time string", val: "8:00 PM", wantValid: true},
		{name: "0-padded 12h time", val: "08:00 PM", wantValid: true},
		{name: "int64 unix", val: int64(1758830400), wantValid: true},
		{name: "int unix", val: int(1758830400), wantValid: true},
		{name: "float64 unix", val: float64(1758830400), wantValid: true},
		{name: "invalid string", val: "not-a-date", wantValid: false},
		{name: "unsupported type bool", val: true, wantValid: false},
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
	laLoc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatalf("failed to load America/Los_Angeles: %v", err)
	}
	eventUTC := "2026-10-12T03:30:00Z"

	tests := []struct {
		name string
		loc  *time.Location
		args []any
		want string
	}{
		{name: "no args", args: []any{}, want: ""},
		{name: "single arg time.Time", args: []any{target}, want: "2026-09-25"},
		{name: "single arg string", args: []any{"2026-09-25T14:30:00Z"}, want: "2026-09-25"},
		{name: "single arg invalid", args: []any{"not-a-date"}, want: "not-a-date"},
		{name: "layout and time", args: []any{"Jan 02, 2006", target}, want: "Sep 25, 2026"},
		{name: "empty layout defaults", args: []any{"", target}, want: "2026-09-25"},
		{name: "default layout keyword", args: []any{"default", target}, want: "2026-09-25"},
		{name: "custom layout with unparseable value", args: []any{"2006-01-02", "bad-date"}, want: "bad-date"},
		{name: "timezone conversion date", loc: laLoc, args: []any{"2006-01-02", eventUTC}, want: "2026-10-11"},
		{name: "timezone conversion layout Jan 2", loc: laLoc, args: []any{"Jan 2", eventUTC}, want: "Oct 11"},
		{name: "timezone conversion date-only preserved", loc: laLoc, args: []any{"2006-01-02", "2026-10-12"}, want: "2026-10-12"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := render.FormatDateInLocation(tc.loc, tc.args...)
			if got != tc.want {
				t.Errorf("FormatDateInLocation(%v) = %q, want %q", tc.args, got, tc.want)
			}
			if tc.loc == nil {
				if gotBase := render.FormatDate(tc.args...); gotBase != tc.want {
					t.Errorf("FormatDate(%v) = %q, want %q", tc.args, gotBase, tc.want)
				}
			}
		})
	}
}

func TestFormatTime(t *testing.T) {
	t.Parallel()

	target := time.Date(2026, 9, 25, 14, 30, 0, 0, time.UTC)
	laLoc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatalf("failed to load America/Los_Angeles: %v", err)
	}
	eventUTC := "2026-10-12T03:30:00Z"

	tests := []struct {
		name string
		loc  *time.Location
		args []any
		want string
	}{
		{name: "no args", args: []any{}, want: ""},
		{name: "single arg time.Time", args: []any{target}, want: "14:30"},
		{name: "single arg string", args: []any{"2026-09-25T14:30:00Z"}, want: "14:30"},
		{name: "single arg invalid", args: []any{"bad-time"}, want: "bad-time"},
		{name: "12h layout and time", args: []any{"3:04 PM", target}, want: "2:30 PM"},
		{name: "empty layout defaults", args: []any{"", target}, want: "14:30"},
		{name: "default layout keyword", args: []any{"default", target}, want: "14:30"},
		{name: "custom layout with unparseable value", args: []any{"15:04", "bad-time"}, want: "bad-time"},
		{name: "date-only returns empty", args: []any{"15:04", "2026-10-12"}, want: ""},
		{name: "timezone conversion 3:04", loc: laLoc, args: []any{"3:04", eventUTC}, want: "8:30"},
		{name: "timezone conversion PM", loc: laLoc, args: []any{"PM", eventUTC}, want: "PM"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := render.FormatTimeInLocation(tc.loc, tc.args...)
			if got != tc.want {
				t.Errorf("FormatTimeInLocation(%v) = %q, want %q", tc.args, got, tc.want)
			}
			if tc.loc == nil {
				if gotBase := render.FormatTime(tc.args...); gotBase != tc.want {
					t.Errorf("FormatTime(%v) = %q, want %q", tc.args, gotBase, tc.want)
				}
			}
		})
	}
}

func TestRelDate(t *testing.T) {
	t.Parallel()

	ref := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	laLoc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatalf("failed to load America/Los_Angeles: %v", err)
	}
	eventUTC := "2026-10-12T03:30:00Z"
	satNow := time.Date(2026, 10, 10, 19, 0, 0, 0, laLoc)
	sunNow := time.Date(2026, 10, 11, 14, 0, 0, 0, laLoc)

	tests := []struct {
		name    string
		refTime time.Time
		args    []any
		want    string
	}{
		{name: "no args", refTime: ref, args: []any{}, want: ""},
		{name: "unparseable", refTime: ref, args: []any{"invalid"}, want: "invalid"},
		{name: "today same hour", refTime: ref, args: []any{ref}, want: "Today"},
		{name: "today earlier hour", refTime: ref, args: []any{time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)}, want: "Today"},
		{name: "tomorrow", refTime: ref, args: []any{time.Date(2026, 9, 26, 9, 0, 0, 0, time.UTC)}, want: "Tomorrow"},
		{name: "yesterday", refTime: ref, args: []any{time.Date(2026, 9, 24, 23, 0, 0, 0, time.UTC)}, want: "Yesterday"},
		{name: "in 3 days", refTime: ref, args: []any{time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)}, want: "in 3 days"},
		{name: "4 days ago", refTime: ref, args: []any{time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)}, want: "4 days ago"},
		{name: "custom reference parameter", refTime: ref, args: []any{
			time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC),
			"2026-10-01T00:00:00Z",
		}, want: "Tomorrow"},
		{name: "date-only today", refTime: ref, args: []any{"2026-09-25"}, want: "Today"},
		{name: "timezone conversion tomorrow", refTime: satNow, args: []any{eventUTC}, want: "Tomorrow"},
		{name: "timezone conversion today", refTime: sunNow, args: []any{eventUTC}, want: "Today"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := render.RelDate(tc.refTime, tc.args...)
			if got != tc.want {
				t.Errorf("RelDate(%v) = %q, want %q", tc.args, got, tc.want)
			}
		})
	}
}

func TestJSONHelpers(t *testing.T) {
	t.Parallel()

	data := map[string]any{"key": "value", "count": 42}
	ch := make(chan int)

	tests := []struct {
		name     string
		val      any
		wantErr  bool
		wantHTML string
		wantJS   string
	}{
		{name: "valid map", val: data, wantHTML: `"key":"value"`, wantJS: `"count":42`},
		{name: "unserializable channel", val: ch, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			htmlOut, htmlErr := render.JSONHelper(tc.val)
			jsOut, jsErr := render.JSONJSHelper(tc.val)
			if tc.wantErr {
				if htmlErr == nil || jsErr == nil {
					t.Fatalf("expected error for %s, got htmlErr=%v, jsErr=%v", tc.name, htmlErr, jsErr)
				}
				return
			}
			if htmlErr != nil || jsErr != nil {
				t.Fatalf("unexpected error for %s: htmlErr=%v, jsErr=%v", tc.name, htmlErr, jsErr)
			}
			if !strings.Contains(string(htmlOut), tc.wantHTML) {
				t.Errorf("JSONHelper output %q missing %q", htmlOut, tc.wantHTML)
			}
			if !strings.Contains(string(jsOut), tc.wantJS) {
				t.Errorf("JSONJSHelper output %q missing %q", jsOut, tc.wantJS)
			}
		})
	}
}

func TestStandardFuncMap(t *testing.T) {
	t.Parallel()

	fixedNow := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	funcs := render.StandardFuncMap(func() time.Time { return fixedNow })

	tests := []struct {
		name         string
		funcMap      template.FuncMap
		tmpl         string
		data         any
		wantContains []string
	}{
		{
			name:         "seq",
			funcMap:      funcs,
			tmpl:         `{{range seq 3}}{{.}}{{end}}`,
			wantContains: []string{"012"},
		},
		{
			name:         "formatDate",
			funcMap:      funcs,
			tmpl:         `{{formatDate "2006" "2026-09-25"}}`,
			wantContains: []string{"2026"},
		},
		{
			name:         "formatTime",
			funcMap:      funcs,
			tmpl:         `{{formatTime "15:04" "2026-09-25T14:30:00Z"}}`,
			wantContains: []string{"14:30"},
		},
		{
			name:         "relDate",
			funcMap:      funcs,
			tmpl:         `{{relDate "2026-09-26T12:00:00Z"}}`,
			wantContains: []string{"Tomorrow"},
		},
		{
			name:         "json",
			funcMap:      funcs,
			tmpl:         `{{json .}}`,
			data:         map[string]string{"foo": "bar"},
			wantContains: []string{`"foo":"bar"`},
		},
		{
			name:         "jsonJS",
			funcMap:      funcs,
			tmpl:         `<script>const data = {{jsonJS .}};</script>`,
			data:         map[string]int{"count": 42},
			wantContains: []string{`"count":42`},
		},
		{
			name:         "weatherIcon",
			funcMap:      funcs,
			tmpl:         `{{weatherIcon "weather-partly-cloudy" 24}}`,
			wantContains: []string{`width="24"`, `mm-icon-weather-partly-cloudy`},
		},
		{
			name:    "weatherHourlyGraph",
			funcMap: funcs,
			tmpl:    `{{weatherHourlyGraph .Hourly .ID}}`,
			data: map[string]any{
				"Hourly": []testHourlyItem{
					{Time: "2026-09-25T12:00", Temp: 68.0, PrecipProb: 0, Icon: "weather-sunny"},
					{Time: "2026-09-25T13:00", Temp: 71.0, PrecipProb: 20, Icon: "weather-partly-cloudy"},
				},
				"ID": "widget-123",
			},
			wantContains: []string{`class="weather-hourly-graph"`, `id="mm-weather-grad-widget-123"`, `68°`, `71°`},
		},
		{
			name:    "weatherDailyForecast",
			funcMap: funcs,
			tmpl:    `{{ $f := weatherDailyForecast .Daily 5 }}{{ range $f }}{{ .DayName }}: {{ printf "%.0f" .TempMin }}°-{{ printf "%.0f" .TempMax }}° {{ end }}`,
			data: map[string]any{
				"Daily": []provider.WeatherDaily{
					{Date: "2026-09-25", TempMin: 50, TempMax: 60},
					{Date: "2026-09-26", TempMin: 55, TempMax: 65, PrecipProbMax: 20, Icon: "weather-sunny", ConditionText: "Sunny"},
					{Date: "2026-09-27", TempMin: 52, TempMax: 62, PrecipProbMax: 0, Icon: "weather-cloudy", ConditionText: "Cloudy"},
				},
			},
			wantContains: []string{"Tomorrow: 55°-65°", "Sun: 52°-62°"},
		},
		{
			name:         "todayPrecipProb",
			funcMap:      funcs,
			tmpl:         `{{todayPrecipProb .}}%`,
			data:         provider.WeatherSnapshot{Daily: []provider.WeatherDaily{{PrecipProbMax: 42}}},
			wantContains: []string{"42%"},
		},
		{
			name:    "calendarFeedEvents",
			funcMap: funcs,
			tmpl:    `{{ range (calendarFeedEvents .) }}{{ .Title }};{{ end }}`,
			data: provider.CalendarSnapshot{
				Events: []provider.CalendarEvent{
					{Title: "Old", Start: "2026-07-01T10:00:00Z", End: "2026-07-01T11:00:00Z"},
					{Title: "Upcoming", Start: "2026-09-25T16:00:00Z", End: "2026-09-25T17:00:00Z"},
				},
			},
			wantContains: []string{"Upcoming;"},
		},
		{
			name:    "calendarFeedDayGroups",
			funcMap: funcs,
			tmpl:    `{{ range (calendarFeedDayGroups .) }}{{ .Date }}:{{ len .Events }};{{ end }}`,
			data: provider.CalendarSnapshot{
				Events: []provider.CalendarEvent{
					{Title: "E1", Start: "2026-09-25T14:00:00Z", End: "2026-09-25T15:00:00Z"},
					{Title: "E2", Start: "2026-09-25T16:00:00Z", End: "2026-09-25T17:00:00Z"},
				},
			},
			wantContains: []string{"2026-09-25:2;"},
		},
		{
			name:    "calendarFeedActiveCalendars",
			funcMap: funcs,
			tmpl:    `{{ $c := calendarFeedActiveCalendars . }}{{ len $c }}:{{ range $c }}{{ .name }};{{ end }}`,
			data: map[string]any{
				"calendars": []any{
					map[string]any{"name": "Cal 1", "enabled": true},
					map[string]any{"name": "Cal 2", "enabled": false},
				},
			},
			wantContains: []string{"1:Cal 1;"},
		},
		{
			name:    "activeCalendars alias",
			funcMap: funcs,
			tmpl:    `{{ $c := activeCalendars . }}{{ len $c }}:{{ range $c }}{{ .name }};{{ end }}`,
			data: map[string]any{
				"calendars": []any{
					map[string]any{"name": "Cal 1", "enabled": true},
					map[string]any{"name": "Cal 2", "enabled": false},
				},
			},
			wantContains: []string{"1:Cal 1;"},
		},
		{
			name:    "tasksStats",
			funcMap: funcs,
			tmpl:    `{{ $s := tasksStats . }}{{ $s.Summary }}`,
			data: []map[string]any{
				{"done": true},
				{"done": false},
			},
			wantContains: []string{"1 to do"},
		},
		{
			name:         "stringOr",
			funcMap:      funcs,
			tmpl:         `{{stringOr . "missing" "fallback"}}`,
			data:         map[string]any{"exists": "value"},
			wantContains: []string{"fallback"},
		},
		{
			name:         "add",
			funcMap:      funcs,
			tmpl:         `{{add 5 3}}`,
			wantContains: []string{"8"},
		},
		{
			name:         "sub",
			funcMap:      funcs,
			tmpl:         `{{sub 5 3}}`,
			wantContains: []string{"2"},
		},
		{
			name:         "nil nowFunc fallback",
			funcMap:      render.StandardFuncMap(nil),
			tmpl:         `{{range seq 2}}{{.}}{{end}}`,
			wantContains: []string{"01"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tmpl, err := template.New(tc.name).Funcs(tc.funcMap).Parse(tc.tmpl)
			if err != nil {
				t.Fatalf("failed to parse template: %v", err)
			}
			var sb strings.Builder
			if err := tmpl.Execute(&sb, tc.data); err != nil {
				t.Fatalf("failed to execute template: %v", err)
			}
			out := sb.String()
			for _, sub := range tc.wantContains {
				if !strings.Contains(out, sub) {
					t.Errorf("template output %q does not contain %q", out, sub)
				}
			}
		})
	}
}

func TestWeatherIcon(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		args         []any
		wantContains []string
	}{
		{name: "weather-sunny", args: []any{"weather-sunny"}, wantContains: []string{`<svg`, `</svg>`, `width="20"`, `height="20"`, `mm-icon-weather-sunny`}},
		{name: "weather-clear-night", args: []any{"weather-clear-night"}, wantContains: []string{`<svg`, `</svg>`, `width="20"`, `height="20"`, `mm-icon-weather-clear-night`}},
		{name: "weather-partly-cloudy", args: []any{"weather-partly-cloudy"}, wantContains: []string{`<svg`, `</svg>`, `width="20"`, `height="20"`, `mm-icon-weather-partly-cloudy`}},
		{name: "weather-night-partly-cloudy", args: []any{"weather-night-partly-cloudy"}, wantContains: []string{`<svg`, `</svg>`, `width="20"`, `height="20"`, `mm-icon-weather-night-partly-cloudy`}},
		{name: "weather-cloudy", args: []any{"weather-cloudy"}, wantContains: []string{`<svg`, `</svg>`, `width="20"`, `height="20"`, `mm-icon-weather-cloudy`}},
		{name: "weather-fog", args: []any{"weather-fog"}, wantContains: []string{`<svg`, `</svg>`, `width="20"`, `height="20"`, `mm-icon-weather-fog`}},
		{name: "weather-rainy", args: []any{"weather-rainy"}, wantContains: []string{`<svg`, `</svg>`, `width="20"`, `height="20"`, `mm-icon-weather-rainy`}},
		{name: "weather-pouring", args: []any{"weather-pouring"}, wantContains: []string{`<svg`, `</svg>`, `width="20"`, `height="20"`, `mm-icon-weather-pouring`}},
		{name: "weather-snowy", args: []any{"weather-snowy"}, wantContains: []string{`<svg`, `</svg>`, `width="20"`, `height="20"`, `mm-icon-weather-snowy`}},
		{name: "weather-snowy-rainy", args: []any{"weather-snowy-rainy"}, wantContains: []string{`<svg`, `</svg>`, `width="20"`, `height="20"`, `mm-icon-weather-snowy-rainy`}},
		{name: "weather-lightning", args: []any{"weather-lightning"}, wantContains: []string{`<svg`, `</svg>`, `width="20"`, `height="20"`, `mm-icon-weather-lightning`}},
		{name: "weather-lightning-rainy", args: []any{"weather-lightning-rainy"}, wantContains: []string{`<svg`, `</svg>`, `width="20"`, `height="20"`, `mm-icon-weather-lightning-rainy`}},
		{name: "unknown-token", args: []any{"unknown-token"}, wantContains: []string{`<svg`, `</svg>`, `width="20"`, `height="20"`, `mm-icon-unknown-token`}},
		{name: "no args", args: []any{}, wantContains: []string{`width="20"`}},
		{name: "nil arg", args: []any{nil}, wantContains: []string{`width="20"`}},
		{name: "custom int", args: []any{"weather-sunny", 32}, wantContains: []string{`width="32"`}},
		{name: "custom int64", args: []any{"weather-sunny", int64(28)}, wantContains: []string{`width="28"`}},
		{name: "custom float64", args: []any{"weather-sunny", float64(24)}, wantContains: []string{`width="24"`}},
		{name: "custom string", args: []any{"weather-sunny", "36"}, wantContains: []string{`width="36"`}},
		{name: "invalid string", args: []any{"weather-sunny", "bad-size"}, wantContains: []string{`width="20"`}},
		{name: "clamp below min", args: []any{"weather-sunny", 5}, wantContains: []string{`width="12"`}},
		{name: "clamp above max", args: []any{"weather-sunny", 120}, wantContains: []string{`width="64"`}},
		{name: "negative int", args: []any{"weather-sunny", -10}, wantContains: []string{`width="20"`}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svg := string(render.WeatherIcon(tc.args...))
			for _, sub := range tc.wantContains {
				if !strings.Contains(svg, sub) {
					t.Errorf("WeatherIcon(%v) missing %q in %s", tc.args, sub, svg)
				}
			}
		})
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

	tests := []struct {
		name            string
		args            []any
		wantEmpty       bool
		wantContains    []string
		wantNotContains []string
	}{
		{name: "no args", args: []any{}, wantEmpty: true},
		{name: "nil arg", args: []any{nil}, wantEmpty: true},
		{name: "unsupported type string", args: []any{"invalid"}, wantEmpty: true},
		{name: "unsupported type int", args: []any{123}, wantEmpty: true},
		{name: "empty slice of any", args: []any{[]any{}}, wantEmpty: true},
		{name: "empty typed slice", args: []any{[]testHourlyItem{}}, wantEmpty: true},
		{name: "slice with all nil items", args: []any{[]any{nil, nil}}, wantEmpty: true},
		{
			name: "single item",
			args: []any{[]testHourlyItem{
				{Time: "2026-09-26T14:00", Temp: 72.0, PrecipProb: 0, Icon: "weather-sunny"},
			}},
			wantContains:    []string{"72°", "Now"},
			wantNotContains: []string{`<path d="M`},
		},
		{
			name: "flatline temperatures",
			args: []any{[]testHourlyItem{
				{Time: "2026-09-26T14:00", Temp: 65.0, PrecipProb: 10, Icon: "weather-cloudy"},
				{Time: "2026-09-26T15:00", Temp: 65.0, PrecipProb: 10, Icon: "weather-cloudy"},
				{Time: "2026-09-26T16:00", Temp: 65.0, PrecipProb: 0, Icon: "weather-cloudy"},
			}},
			wantContains: []string{`stroke-width="2.5"`, "10%", "3 PM", "4 PM"},
		},
		{
			name: "negative and zero-crossing temperatures with custom gradient",
			args: []any{
				[]testHourlyItem{
					{Time: "2026-09-26T00:00", Temp: -5.0, PrecipProb: 50, Icon: "weather-snowy"},
					{Time: "2026-09-26T01:00", Temp: 0.0, PrecipProb: 30, Icon: "weather-snowy-rainy"},
					{Time: "2026-09-26T02:00", Temp: 10.0, PrecipProb: 0, Icon: "weather-sunny"},
				},
				"custom-id",
			},
			wantContains: []string{`id="mm-weather-grad-custom-id"`, "-5°", "0°", "10°", "50%"},
		},
		{
			name: "map slice with NaN Inf int temps and unparseable time",
			args: []any{[]map[string]any{
				{"time": "2026-09-26T14:00", "temp": math.NaN(), "precip_prob": 25.0, "icon": ""},
				{"time": "custom-time-format", "temp": math.Inf(1), "precip_prob": 0, "icon": "weather-partly-cloudy"},
				{"time": "2026-09-26T16:00", "temp": 70, "precip_prob": 40, "icon": "weather-rainy"},
			}},
			wantContains:    []string{"custom-time-format", "70°", "40%"},
			wantNotContains: []string{"NaN", "Inf"},
		},
		{
			name: "scaled vertical coordinates",
			args: []any{[]testHourlyItem{
				{Time: "2026-09-26T12:00", Temp: 80.0, PrecipProb: 40, Icon: "weather-rainy"},
				{Time: "2026-09-26T13:00", Temp: 50.0, PrecipProb: 0, Icon: "weather-cloudy"},
			}},
			wantContains: []string{
				`viewBox="0 0 560.0 300.0"`,
				`y1="240.0"`, `y2="240.0"`,
				`y="20" text-anchor="middle" font-size="11"`,
				`, 32) scale(1.2)`,
				`r="4.0"`,
				`y="275" text-anchor="middle" font-size="13"`,
				`y="80.0" text-anchor="middle" font-size="14" font-weight="800"`,
				`y="195.0" text-anchor="middle" font-size="14" font-weight="800"`,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := string(render.WeatherHourlyGraph(tc.args...))
			if tc.wantEmpty {
				if got != "" {
					t.Fatalf("expected empty HTML, got: %s", got)
				}
				return
			}
			if !strings.HasPrefix(got, "<svg") || !strings.HasSuffix(got, "</svg>") {
				t.Fatalf("expected valid SVG, got: %s", got)
			}
			for _, sub := range tc.wantContains {
				if !strings.Contains(got, sub) {
					t.Errorf("WeatherHourlyGraph() missing %q in %s", sub, got)
				}
			}
			for _, sub := range tc.wantNotContains {
				if strings.Contains(got, sub) {
					t.Errorf("WeatherHourlyGraph() should not contain %q in %s", sub, got)
				}
			}
		})
	}
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

func TestWeatherDailyForecast(t *testing.T) {
	t.Parallel()

	multiDay := []provider.WeatherDaily{
		{Date: "2026-10-11", TempMin: 55, TempMax: 75, PrecipProbMax: 0},
		{Date: "2026-10-12", TempMin: 50, TempMax: 70, PrecipProbMax: 20, ConditionText: "Sunny", Icon: "weather-sunny"},
		{Date: "2026-10-13", TempMin: 45, TempMax: 65, PrecipProbMax: 0, ConditionText: "Cloudy", Icon: "weather-cloudy"},
		{Date: "2026-10-14", TempMin: 40, TempMax: 60, PrecipProbMax: 80, ConditionText: "Rain", Icon: "weather-rainy"},
		{Date: "2026-10-15", TempMin: 42, TempMax: 62, PrecipProbMax: 10, ConditionText: "Partly Cloudy", Icon: "weather-partly-cloudy"},
		{Date: "2026-10-16", TempMin: 48, TempMax: 68, PrecipProbMax: 0, ConditionText: "Clear", Icon: "weather-sunny"},
		{Date: "2026-10-17", TempMin: 52, TempMax: 72, PrecipProbMax: 0, ConditionText: "Sunny", Icon: "weather-sunny"},
	}
	sliceForPtr := []provider.WeatherDaily{
		{Date: "2026-10-11", TempMin: 50, TempMax: 60},
		{Date: "2026-10-12", TempMin: 52, TempMax: 62, Icon: "weather-sunny", ConditionText: "Sunny"},
	}
	var nilDailyPtr *[]provider.WeatherDaily

	tests := []struct {
		name          string
		arg           any
		maxDays       []int
		wantNil       bool
		wantCount     int
		wantDayNames  []string
		wantIcons     []string
		wantConds     []string
		wantMinMax    [][2]float64
		wantPrecip    []int
		wantBarLeft2  float64
		wantBarWidth2 float64
		checkBarLeft2 bool
	}{
		{name: "nil input", arg: nil, wantNil: true},
		{name: "empty slice", arg: []provider.WeatherDaily{}, wantNil: true},
		{name: "non-slice string", arg: "not-a-slice", wantNil: true},
		{name: "nil slice pointer", arg: nilDailyPtr, wantNil: true},
		{
			name: "single item slice returns nil",
			arg: []provider.WeatherDaily{
				{Date: "2026-10-11", TempMin: 50, TempMax: 70, ConditionText: "Clear", Icon: "weather-sunny"},
			},
			wantNil: true,
		},
		{
			name:          "multi-day default 5-day cap and bar metrics",
			arg:           multiDay,
			wantCount:     5,
			wantDayNames:  []string{"Tomorrow", "Tue", "Wed", "Thu", "Fri"},
			wantPrecip:    []int{20, 0, 80, 10, 0},
			checkBarLeft2: true,
			wantBarLeft2:  0.0,
			wantBarWidth2: 66.666,
		},
		{
			name: "flatline temperatures with span < 2",
			arg: []provider.WeatherDaily{
				{Date: "2026-10-11", TempMin: 68, TempMax: 68},
				{Date: "2026-10-12", TempMin: 68, TempMax: 68},
				{Date: "2026-10-13", TempMin: 68, TempMax: 68},
			},
			wantCount: 2,
		},
		{
			name: "negative temperatures and inverted min max swap",
			arg: []provider.WeatherDaily{
				{Date: "2026-01-01", TempMin: 10, TempMax: 20},
				{Date: "2026-01-02", TempMin: -5, TempMax: -15, Icon: "weather-snowy"},
				{Date: "2026-01-03", TempMin: -20, TempMax: -10, Icon: "weather-snowy"},
			},
			wantCount:    2,
			wantDayNames: []string{"Tomorrow", "Sat"},
			wantMinMax:   [][2]float64{{-15, -5}, {-20, -10}},
		},
		{
			name: "unparseable date fallback and custom maxDays 2",
			arg: []provider.WeatherDaily{
				{Date: "today", TempMin: 50, TempMax: 60},
				{Date: "unparseable-date-1", TempMin: 50, TempMax: 60},
				{Date: "unparseable-date-2", TempMin: 52, TempMax: 62},
				{Date: "unparseable-date-3", TempMin: 54, TempMax: 64},
			},
			maxDays:      []int{2},
			wantCount:    2,
			wantDayNames: []string{"Tomorrow", "unparseable-date-2"},
		},
		{
			name:      "slice pointer non-nil",
			arg:       &sliceForPtr,
			wantCount: 1,
			wantConds: []string{"Sunny"},
		},
		{
			name: "map slice with precip and icon",
			arg: []map[string]any{
				{"date": "2026-10-11", "temp_min": 50, "temp_max": 60},
				{"date": "2026-10-12", "temp_min": 45.0, "temp_max": 65.0, "precip_prob_max": 30, "condition_text": "Rain", "icon": "weather-rainy"},
			},
			wantCount:    1,
			wantDayNames: []string{"Tomorrow"},
			wantConds:    []string{"Rain"},
			wantIcons:    []string{"weather-rainy"},
			wantPrecip:   []int{30},
		},
		{
			name: "mixed custom struct slice with reflection",
			arg: []any{
				nil,
				(*customDaily)(nil),
				&customDaily{Date: "2026-10-11", TempMin: 50, TempMax: 60, PrecipProbMax: 0, ConditionText: "Clear", Icon: "weather-sunny"},
				customDailyInt{Date: "2026-10-12", TempMin: 40, TempMax: 65, PrecipProbMax: 30, ConditionText: "Rain", Icon: ""},
				&provider.WeatherDaily{Date: "2026-10-13", TempMin: 45, TempMax: math.NaN(), PrecipProbMax: 10, ConditionText: "Cloudy"},
			},
			wantCount:    2,
			wantDayNames: []string{"Tomorrow", "Tue"},
			wantIcons:    []string{"weather-cloudy", "weather-cloudy"},
		},
		{
			name: "barLeft clamping to 95 and non-positive maxDays zero",
			arg: []provider.WeatherDaily{
				{Date: "2026-10-11", TempMin: 50, TempMax: 60},
				{Date: "2026-10-12", TempMin: 0, TempMax: 10},
				{Date: "2026-10-13", TempMin: 98, TempMax: 100},
			},
			maxDays:       []int{0},
			wantCount:     2,
			checkBarLeft2: true,
			wantBarLeft2:  95.0,
		},
		{
			name: "negative maxDays fallback",
			arg: []provider.WeatherDaily{
				{Date: "2026-10-11", TempMin: 50, TempMax: 60},
				{Date: "2026-10-12", TempMin: 0, TempMax: 10},
				{Date: "2026-10-13", TempMin: 98, TempMax: 100},
			},
			maxDays:   []int{-5},
			wantCount: 2,
		},
		{
			name: "map slice with int temps and float precip",
			arg: []map[string]any{
				{"date": "2026-10-11", "temp_min": 50, "temp_max": 70, "precip_prob_max": 10},
				{"date": "2026-10-12", "temp_min": 45, "temp_max": 65, "precip_prob_max": 50.0},
			},
			wantCount:  1,
			wantMinMax: [][2]float64{{45, 65}},
			wantPrecip: []int{50},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := render.WeatherDailyForecast(tc.arg, tc.maxDays...)
			if tc.wantNil {
				if got != nil {
					t.Fatalf("expected nil forecast, got %+v", got)
				}
				return
			}
			if len(got) != tc.wantCount {
				t.Fatalf("expected %d items, got %d", tc.wantCount, len(got))
			}
			for i, item := range got {
				if math.IsNaN(item.BarLeft) || math.IsNaN(item.BarWidth) {
					t.Fatalf("item %d contains NaN: barLeft=%f, barWidth=%f", i, item.BarLeft, item.BarWidth)
				}
				if item.BarLeft < 0 || item.BarLeft > 95 {
					t.Errorf("item %d: barLeft %f out of bounds [0, 95]", i, item.BarLeft)
				}
				if item.BarWidth < 5 {
					t.Errorf("item %d: barWidth %f < min 5", i, item.BarWidth)
				}
				if item.BarLeft+item.BarWidth > 100.001 {
					t.Errorf("item %d: barLeft + barWidth = %f > 100", i, item.BarLeft+item.BarWidth)
				}
				if i < len(tc.wantDayNames) && item.DayName != tc.wantDayNames[i] {
					t.Errorf("item %d: dayName = %q, want %q", i, item.DayName, tc.wantDayNames[i])
				}
				if i < len(tc.wantIcons) && item.Icon != tc.wantIcons[i] {
					t.Errorf("item %d: icon = %q, want %q", i, item.Icon, tc.wantIcons[i])
				}
				if i < len(tc.wantConds) && item.ConditionText != tc.wantConds[i] {
					t.Errorf("item %d: conditionText = %q, want %q", i, item.ConditionText, tc.wantConds[i])
				}
				if i < len(tc.wantPrecip) {
					if item.PrecipProbMax != tc.wantPrecip[i] {
						t.Errorf("item %d: precip = %d, want %d", i, item.PrecipProbMax, tc.wantPrecip[i])
					}
					if item.HasPrecip != (tc.wantPrecip[i] > 0) {
						t.Errorf("item %d: hasPrecip = %v, want %v", i, item.HasPrecip, tc.wantPrecip[i] > 0)
					}
				}
				if i < len(tc.wantMinMax) {
					if item.TempMin != tc.wantMinMax[i][0] || item.TempMax != tc.wantMinMax[i][1] {
						t.Errorf("item %d: min/max = %f/%f, want %f/%f", i, item.TempMin, item.TempMax, tc.wantMinMax[i][0], tc.wantMinMax[i][1])
					}
				}
			}
			if tc.checkBarLeft2 && len(got) >= 2 {
				targetIdx := len(got) - 1
				if len(got) >= 3 {
					targetIdx = 2
				}
				if math.Abs(got[targetIdx].BarLeft-tc.wantBarLeft2) > 0.01 {
					t.Errorf("barLeft at index %d: got %f, want ~%f", targetIdx, got[targetIdx].BarLeft, tc.wantBarLeft2)
				}
				if tc.wantBarWidth2 > 0 && math.Abs(got[targetIdx].BarWidth-tc.wantBarWidth2) > 0.1 {
					t.Errorf("barWidth at index %d: got %f, want ~%f", targetIdx, got[targetIdx].BarWidth, tc.wantBarWidth2)
				}
			}
		})
	}
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
		{"map with daily float precip", map[string]any{"daily": []any{map[string]any{"precip_prob_max": float64(50)}}}, 50},
		{"map with current map", map[string]any{"current": map[string]any{"precip_prob_max": 25}}, 25},
		{"map with current map float", map[string]any{"current": map[string]any{"precip_prob_max": float64(25)}}, 25},
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
		{
			name:       "nil snapshot pointer",
			data:       (*provider.CalendarSnapshot)(nil),
			refTime:    refTime,
			wantTitles: nil,
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
				t.Fatalf("got %d events (%v), want %d (%v)", len(gotTitles), gotTitles, len(tc.wantTitles), tc.wantTitles)
			}
			for i := range gotTitles {
				if gotTitles[i] != tc.wantTitles[i] {
					t.Errorf("at index %d: got title %q, want %q", i, gotTitles[i], tc.wantTitles[i])
				}
			}
		})
	}
}

func TestCalendarFeedDayGroups(t *testing.T) {
	t.Parallel()

	refTime := time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		data       any
		refTime    time.Time
		wantNil    bool
		wantGroups int
		wantDates  []string
		wantRels   []string
		wantCounts []int
	}{
		{name: "nil data", data: nil, refTime: refTime, wantNil: true},
		{name: "empty snapshot", data: provider.CalendarSnapshot{}, refTime: refTime, wantNil: true},
		{
			name: "groups multiple events occurring on the same day into single group",
			data: provider.CalendarSnapshot{
				Events: []provider.CalendarEvent{
					{ID: "e1", Title: "Morning Meeting", Start: "2026-10-10T09:00:00Z", End: "2026-10-10T10:00:00Z"},
					{ID: "e2", Title: "Afternoon Sync", Start: "2026-10-10T14:00:00Z", End: "2026-10-10T15:00:00Z"},
					{ID: "e3", Title: "Tomorrow Coffee", Start: "2026-10-11T10:00:00Z", End: "2026-10-11T11:00:00Z"},
				},
			},
			refTime:    refTime,
			wantGroups: 2,
			wantDates:  []string{"2026-10-10", "2026-10-11"},
			wantRels:   []string{"Today", "Tomorrow"},
			wantCounts: []int{2, 1},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			groups := render.CalendarFeedDayGroups(tc.data, tc.refTime)
			if tc.wantNil {
				if groups != nil {
					t.Fatalf("expected nil groups, got %+v", groups)
				}
				return
			}
			if len(groups) != tc.wantGroups {
				t.Fatalf("expected %d day groups, got %d", tc.wantGroups, len(groups))
			}
			for i := range groups {
				if groups[i].Date != tc.wantDates[i] {
					t.Errorf("group %d: date = %q, want %q", i, groups[i].Date, tc.wantDates[i])
				}
				if groups[i].Rel != tc.wantRels[i] {
					t.Errorf("group %d: rel = %q, want %q", i, groups[i].Rel, tc.wantRels[i])
				}
				if len(groups[i].Events) != tc.wantCounts[i] {
					t.Errorf("group %d: events count = %d, want %d", i, len(groups[i].Events), tc.wantCounts[i])
				}
			}
		})
	}
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
}

func TestTasksStats(t *testing.T) {
	t.Parallel()

	trueVal := true
	falseVal := false

	snapshot := &tasks.TasksSnapshot{
		Items: []tasks.ListItem{
			{Title: "T1", Done: true},
			{Title: "T2", Done: false},
		},
	}
	itemsSlice := []tasks.ListItem{
		{Title: "Task 1", Done: true},
		{Title: "Task 2", Done: true},
		{Title: "Task 3", Done: true},
		{Title: "Task 4", Done: false},
	}

	tests := []struct {
		name          string
		input         any
		wantTotal     int
		wantDone      int
		wantRemaining int
		wantSummary   string
	}{
		{name: "nil input", input: nil, wantTotal: 0, wantDone: 0, wantRemaining: 0, wantSummary: ""},
		{name: "nil pointer snapshot", input: (*tasks.TasksSnapshot)(nil), wantTotal: 0, wantDone: 0, wantRemaining: 0, wantSummary: ""},
		{name: "typed ListItem slice", input: itemsSlice, wantTotal: 4, wantDone: 3, wantRemaining: 1, wantSummary: "1 to do"},
		{name: "pointer to ListItem slice", input: &itemsSlice, wantTotal: 4, wantDone: 3, wantRemaining: 1, wantSummary: "1 to do"},
		{
			name: "slice with nil item pointer and item pointer",
			input: []*tasks.ListItem{
				nil,
				{Title: "Done Task", Done: true},
				{Title: "Pending Task", Done: false},
			},
			wantTotal:     2,
			wantDone:      1,
			wantRemaining: 1,
			wantSummary:   "1 to do",
		},
		{
			name:          "tasks snapshot pointer",
			input:         snapshot,
			wantTotal:     2,
			wantDone:      1,
			wantRemaining: 1,
			wantSummary:   "1 to do",
		},
		{
			name: "map with items and pointer bool in done",
			input: map[string]any{
				"items": []any{
					map[string]any{"title": "Map 1", "done": &trueVal},
					map[string]any{"title": "Map 2", "done": &falseVal},
					map[string]any{"title": "Map 3", "done": true},
				},
			},
			wantTotal:     3,
			wantDone:      2,
			wantRemaining: 1,
			wantSummary:   "1 to do",
		},
		{
			name: "map with nil items pointer",
			input: map[string]any{
				"items": (*[]tasks.ListItem)(nil),
			},
			wantTotal:     0,
			wantDone:      0,
			wantRemaining: 0,
			wantSummary:   "",
		},
		{
			name: "multiple remaining tasks",
			input: []tasks.ListItem{
				{Title: "Task 1", Done: true},
				{Title: "Task 2", Done: false},
				{Title: "Task 3", Done: false},
			},
			wantTotal:     3,
			wantDone:      1,
			wantRemaining: 2,
			wantSummary:   "2 to do",
		},
		{
			name: "all tasks completed",
			input: []tasks.ListItem{
				{Title: "Task 1", Done: true},
				{Title: "Task 2", Done: true},
			},
			wantTotal:     2,
			wantDone:      2,
			wantRemaining: 0,
			wantSummary:   "All done",
		},
		{
			name: "zero tasks completed",
			input: []tasks.ListItem{
				{Title: "Task 1", Done: false},
			},
			wantTotal:     1,
			wantDone:      0,
			wantRemaining: 1,
			wantSummary:   "1 to do",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			stats := render.TasksStats(tc.input)
			if stats.Total != tc.wantTotal || stats.Done != tc.wantDone || stats.Remaining != tc.wantRemaining || stats.Summary != tc.wantSummary {
				t.Fatalf("TasksStats(%v) = %+v, want Total=%d, Done=%d, Remaining=%d, Summary=%q",
					tc.name, stats, tc.wantTotal, tc.wantDone, tc.wantRemaining, tc.wantSummary)
			}
		})
	}
}

package render

import (
	"encoding/json"
	"fmt"
	"html/template"
	"math"
	"reflect"
	"strings"
	"time"
)

var timeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04",
	"2006-01-02 15:04",
	"2006-01-02",
	"15:04:05",
	"15:04",
	"3:04 PM",
	"03:04 PM",
}

// StandardFuncMap returns the authoritative template helper functions for Mirrormere widget views.
func StandardFuncMap(nowFunc func() time.Time) template.FuncMap {
	if nowFunc == nil {
		nowFunc = time.Now
	}

	return template.FuncMap{
		"seq":        Seq,
		"formatDate": FormatDate,
		"formatTime": FormatTime,
		"relDate": func(args ...any) string {
			return RelDate(nowFunc(), args...)
		},
		"json":               JSONHelper,
		"jsonJS":             JSONJSHelper,
		"weatherIcon":        WeatherIcon,
		"weatherHourlyGraph": WeatherHourlyGraph,
	}
}

// Seq generates integer sequences for template range loops.
// - seq(n): generates [0, 1, ..., n-1]
// - seq(start, end): generates [start, start+1, ..., end]
// - seq(start, end, step): generates sequence stepping by step
func Seq(args ...int) []int {
	switch len(args) {
	case 0:
		return []int{}
	case 1:
		n := args[0]
		if n <= 0 {
			return []int{}
		}
		res := make([]int, n)
		for i := 0; i < n; i++ {
			res[i] = i
		}
		return res
	case 2:
		start, end := args[0], args[1]
		if start > end {
			return []int{}
		}
		res := make([]int, end-start+1)
		for i := range res {
			res[i] = start + i
		}
		return res
	default:
		start, end, step := args[0], args[1], args[2]
		if step <= 0 || start > end {
			return []int{}
		}
		var res []int
		for i := start; i <= end; i += step {
			res = append(res, i)
		}
		return res
	}
}

// ParseTime attempts to parse an unknown time value into a time.Time struct.
func ParseTime(v any) (time.Time, bool) {
	switch val := v.(type) {
	case time.Time:
		return val, true
	case *time.Time:
		if val == nil {
			return time.Time{}, false
		}
		return *val, true
	case string:
		for _, layout := range timeLayouts {
			if t, err := time.Parse(layout, val); err == nil {
				return t, true
			}
		}
		return time.Time{}, false
	case int64:
		return time.Unix(val, 0), true
	case int:
		return time.Unix(int64(val), 0), true
	case float64:
		return time.Unix(int64(val), 0), true
	default:
		return time.Time{}, false
	}
}

// FormatDate formats a time value with the specified date layout.
// If layout is omitted, defaults to "2006-01-02".
func FormatDate(args ...any) string {
	if len(args) == 0 {
		return ""
	}
	layout := "2006-01-02"
	var val any

	if len(args) == 1 {
		val = args[0]
	} else {
		layoutStr := fmt.Sprint(args[0])
		if layoutStr != "" && layoutStr != "default" {
			layout = layoutStr
		}
		val = args[1]
	}

	t, ok := ParseTime(val)
	if !ok {
		return fmt.Sprint(val)
	}
	return t.Format(layout)
}

// FormatTime formats a time value with the specified time layout.
// If layout is omitted, defaults to "15:04".
func FormatTime(args ...any) string {
	if len(args) == 0 {
		return ""
	}
	layout := "15:04"
	var val any

	if len(args) == 1 {
		val = args[0]
	} else {
		layoutStr := fmt.Sprint(args[0])
		if layoutStr != "" && layoutStr != "default" {
			layout = layoutStr
		}
		val = args[1]
	}

	t, ok := ParseTime(val)
	if !ok {
		return fmt.Sprint(val)
	}
	return t.Format(layout)
}

// RelDate returns human-readable relative date expressions ("Today", "Tomorrow", "Yesterday", "in X days", "X days ago").
func RelDate(refTime time.Time, args ...any) string {
	if len(args) == 0 {
		return ""
	}
	val := args[0]
	t, ok := ParseTime(val)
	if !ok {
		return fmt.Sprint(val)
	}

	if len(args) >= 2 {
		if customRef, refOk := ParseTime(args[1]); refOk {
			refTime = customRef
		}
	}

	loc := t.Location()
	refInLoc := refTime.In(loc)

	tDate := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
	refDate := time.Date(refInLoc.Year(), refInLoc.Month(), refInLoc.Day(), 0, 0, 0, 0, loc)

	diffHours := tDate.Sub(refDate).Hours()
	diffDays := int(diffHours / 24)

	switch {
	case diffDays == 0:
		return "Today"
	case diffDays == 1:
		return "Tomorrow"
	case diffDays == -1:
		return "Yesterday"
	case diffDays > 1:
		return fmt.Sprintf("in %d days", diffDays)
	default:
		return fmt.Sprintf("%d days ago", -diffDays)
	}
}

// JSONHelper marshals any value into unescaped template.HTML.
func JSONHelper(v any) (template.HTML, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return template.HTML(b), nil
}

// JSONJSHelper marshals any value into safe template.JS.
func JSONJSHelper(v any) (template.JS, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return template.JS(b), nil
}

func weatherIconPath(token string) string {
	switch token {
	case "weather-sunny":
		return `<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M6.34 17.66l-1.41 1.41M19.07 4.93l-1.41 1.41"/>`
	case "weather-partly-cloudy":
		return `<path d="M12 2v2M4.93 4.93l1.41 1.41M20 12h2M19.07 4.93l-1.41 1.41"/><path d="M17.5 19H9a5 5 0 0 1-1-9.9 6 6 0 0 1 11.8 1.9 4 4 0 0 1-2.3 8z"/>`
	case "weather-cloudy":
		return `<path d="M17.5 19H9a5 5 0 0 1-1-9.9 6 6 0 0 1 11.8 1.9 4 4 0 0 1-2.3 8z"/>`
	case "weather-fog":
		return `<path d="M4 14h16M4 18h16M7 10h10M9 6h6"/>`
	case "weather-rainy":
		return `<path d="M17.5 14H9a5 5 0 0 1-1-9.9 6 6 0 0 1 11.8 1.9 4 4 0 0 1-2.3 8z"/><path d="M8 17v4M12 17v4M16 17v4"/>`
	case "weather-pouring":
		return `<path d="M17.5 13H9a5 5 0 0 1-1-9.9 6 6 0 0 1 11.8 1.9 4 4 0 0 1-2.3 8z"/><path d="M7 16l-2 5M11 16l-2 5M15 16l-2 5M19 16l-2 5"/>`
	case "weather-snowy", "weather-snowy-rainy":
		return `<path d="M17.5 14H9a5 5 0 0 1-1-9.9 6 6 0 0 1 11.8 1.9 4 4 0 0 1-2.3 8z"/><path d="M8 18h.01M12 18h.01M16 18h.01M10 21h.01M14 21h.01"/>`
	case "weather-lightning", "weather-lightning-rainy":
		return `<path d="M17.5 13H9a5 5 0 0 1-1-9.9 6 6 0 0 1 11.8 1.9 4 4 0 0 1-2.3 8z"/><polygon points="13 14 10 19 14 19 11 23 16 16 12 16 13 14"/>`
	default:
		return `<path d="M17.5 19H9a5 5 0 0 1-1-9.9 6 6 0 0 1 11.8 1.9 4 4 0 0 1-2.3 8z"/>`
	}
}

// WeatherIcon returns a sanitized, scalable vector inline SVG for WMO condition tokens.
func WeatherIcon(args ...any) template.HTML {
	token := "weather-cloudy"
	size := 20
	if len(args) > 0 && args[0] != nil {
		s := fmt.Sprint(args[0])
		if s != "" {
			token = s
		}
	}
	if len(args) > 1 {
		switch v := args[1].(type) {
		case int:
			if v > 0 {
				size = v
			}
		case int64:
			if v > 0 {
				size = int(v)
			}
		case float64:
			if v > 0 {
				size = int(v)
			}
		case string:
			var parsedSize int
			if n, err := fmt.Sscanf(v, "%d", &parsedSize); err == nil && n > 0 && parsedSize > 0 {
				size = parsedSize
			}
		}
	}
	if size < 12 {
		size = 12
	} else if size > 64 {
		size = 64
	}

	pathHTML := weatherIconPath(token)
	return template.HTML(fmt.Sprintf(`<svg width="%d" height="%d" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="mm-weather-icon mm-icon-%s">%s</svg>`, size, size, template.HTMLEscapeString(token), pathHTML))
}

type hourlyPoint struct {
	timeStr    string
	temp       float64
	precipProb int
	icon       string
	x          float64
	y          float64
}

func extractHourlyPoints(arg any) []hourlyPoint {
	if arg == nil {
		return nil
	}

	val := reflect.ValueOf(arg)
	if val.Kind() == reflect.Pointer {
		if val.IsNil() {
			return nil
		}
		val = val.Elem()
	}

	if val.Kind() != reflect.Slice && val.Kind() != reflect.Array {
		return nil
	}

	n := val.Len()
	if n == 0 {
		return nil
	}

	pts := make([]hourlyPoint, 0, n)
	for i := 0; i < n; i++ {
		itemVal := val.Index(i)
		if !itemVal.IsValid() {
			continue
		}
		item := itemVal.Interface()
		if item == nil {
			continue
		}

		var pt hourlyPoint
		if m, ok := item.(map[string]any); ok {
			if t, ok := m["time"].(string); ok {
				pt.timeStr = t
			}
			if tmp, ok := m["temp"].(float64); ok {
				pt.temp = tmp
			} else if tmp, ok := m["temp"].(int); ok {
				pt.temp = float64(tmp)
			}
			if p, ok := m["precip_prob"].(int); ok {
				pt.precipProb = p
			} else if p, ok := m["precip_prob"].(float64); ok {
				pt.precipProb = int(p)
			}
			if ic, ok := m["icon"].(string); ok {
				pt.icon = ic
			}
		} else {
			v := reflect.ValueOf(item)
			if v.Kind() == reflect.Pointer {
				if v.IsNil() {
					continue
				}
				v = v.Elem()
			}
			if v.Kind() == reflect.Struct {
				if f := v.FieldByName("Time"); f.IsValid() && f.Kind() == reflect.String {
					pt.timeStr = f.String()
				}
				if f := v.FieldByName("Temp"); f.IsValid() {
					if f.Kind() == reflect.Float64 || f.Kind() == reflect.Float32 {
						pt.temp = f.Float()
					} else if f.Kind() == reflect.Int || f.Kind() == reflect.Int64 {
						pt.temp = float64(f.Int())
					}
				}
				if f := v.FieldByName("PrecipProb"); f.IsValid() {
					if f.Kind() == reflect.Int || f.Kind() == reflect.Int64 {
						pt.precipProb = int(f.Int())
					}
				}
				if f := v.FieldByName("Icon"); f.IsValid() && f.Kind() == reflect.String {
					pt.icon = f.String()
				}
			}
		}

		if math.IsNaN(pt.temp) || math.IsInf(pt.temp, 0) {
			pt.temp = 0.0
		}
		if pt.icon == "" {
			pt.icon = "weather-cloudy"
		}
		pts = append(pts, pt)
	}

	return pts
}

// WeatherHourlyGraph renders an SVG continuous line graph showing hourly temperature trends,
// condition icons, precipitation chances, and time throughout the day.
func WeatherHourlyGraph(args ...any) template.HTML {
	if len(args) == 0 || args[0] == nil {
		return ""
	}

	pts := extractHourlyPoints(args[0])
	if len(pts) == 0 {
		return ""
	}

	gradientID := "mm-weather-hourly-grad"
	if len(args) > 1 && args[1] != nil {
		if idStr := fmt.Sprint(args[1]); idStr != "" {
			gradientID = fmt.Sprintf("mm-weather-grad-%s", template.HTMLEscapeString(idStr))
		}
	}

	const (
		colWidth  = 56.0
		svgHeight = 130.0
		minY      = 56.0 // High temp y-coord (leaving room for temp label at y = 50)
		maxY      = 96.0 // Low temp y-coord
		baseY     = 108.0 // Baseline axis y-coord
	)

	n := len(pts)
	totalWidth := float64(n) * colWidth

	minTemp := pts[0].temp
	maxTemp := pts[0].temp
	for _, p := range pts[1:] {
		if p.temp < minTemp {
			minTemp = p.temp
		}
		if p.temp > maxTemp {
			maxTemp = p.temp
		}
	}

	span := maxTemp - minTemp
	if span < 4.0 {
		mid := (minTemp + maxTemp) / 2.0
		minTemp = mid - 2.0
		span = 4.0
	}

	for i := range pts {
		pts[i].x = float64(i)*colWidth + colWidth/2.0
		ratio := (pts[i].temp - minTemp) / span
		pts[i].y = maxY - ratio*(maxY-minY)
	}

	var sb strings.Builder
	sb.Grow(2048)

	fmt.Fprintf(&sb, `<svg class="weather-hourly-graph" width="%.1f" height="%.1f" viewBox="0 0 %.1f %.1f" style="flex-shrink: 0; display: block; overflow: visible;">`,
		totalWidth, svgHeight, totalWidth, svgHeight)

	fmt.Fprintf(&sb, `<defs><linearGradient id="%s" x1="0" y1="0" x2="0" y2="1">`+
		`<stop offset="0%%" stop-color="#a855f7" stop-opacity="0.35"/>`+
		`<stop offset="100%%" stop-color="#a855f7" stop-opacity="0.0"/>`+
		`</linearGradient></defs>`, gradientID)

	fmt.Fprintf(&sb, `<line x1="0" y1="%.1f" x2="%.1f" y2="%.1f" stroke="rgba(168, 85, 247, 0.2)" stroke-width="1"/>`,
		baseY, totalWidth, baseY)

	if n > 1 {
		var curvePath strings.Builder
		var areaPath strings.Builder

		fmt.Fprintf(&curvePath, "M %.1f %.1f", pts[0].x, pts[0].y)
		fmt.Fprintf(&areaPath, "M %.1f %.1f L %.1f %.1f", pts[0].x, baseY, pts[0].x, pts[0].y)

		for i := 0; i < n-1; i++ {
			dx := (pts[i+1].x - pts[i].x) / 2.0
			seg := fmt.Sprintf(" C %.1f %.1f, %.1f %.1f, %.1f %.1f",
				pts[i].x+dx, pts[i].y,
				pts[i+1].x-dx, pts[i+1].y,
				pts[i+1].x, pts[i+1].y)
			curvePath.WriteString(seg)
			areaPath.WriteString(seg)
		}
		fmt.Fprintf(&areaPath, " L %.1f %.1f Z", pts[n-1].x, baseY)

		fmt.Fprintf(&sb, `<path d="%s" fill="url(#%s)"/>`, areaPath.String(), gradientID)
		fmt.Fprintf(&sb, `<path d="%s" fill="none" stroke="var(--mm-accent-purple, #a855f7)" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"/>`,
			curvePath.String())
	}

	for i, pt := range pts {
		fmt.Fprintf(&sb, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="rgba(168, 85, 247, 0.15)" stroke-dasharray="2 2"/>`,
			pt.x, pt.y+5.0, pt.x, baseY)

		if pt.precipProb > 0 {
			fmt.Fprintf(&sb, `<text x="%.1f" y="12" text-anchor="middle" font-size="10" font-weight="600" fill="var(--mm-accent-cyan, #06b6d4)" style="user-select: none; pointer-events: none;">%d%%</text>`,
				pt.x, pt.precipProb)
		}

		iconPath := weatherIconPath(pt.icon)
		fmt.Fprintf(&sb, `<g transform="translate(%.1f, 17) scale(0.75)" fill="none" stroke="var(--mm-accent-purple, #a855f7)" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">%s</g>`,
			pt.x-9.0, iconPath)

		tempRound := int(math.Round(pt.temp))
		fmt.Fprintf(&sb, `<text x="%.1f" y="%.1f" text-anchor="middle" font-size="11" font-weight="700" fill="var(--mm-text-primary, #f5f3ff)" style="user-select: none; pointer-events: none;">%d°</text>`,
			pt.x, pt.y-6.0, tempRound)

		fmt.Fprintf(&sb, `<circle cx="%.1f" cy="%.1f" r="3.5" fill="var(--mm-bg-surface, #120b22)" stroke="var(--mm-accent-purple, #a855f7)" stroke-width="2"/>`,
			pt.x, pt.y)

		timeLabel := "Now"
		timeWeight := "700"
		timeColor := "var(--mm-accent-purple, #a855f7)"
		if i > 0 {
			timeWeight = "500"
			timeColor = "var(--mm-text-muted, #8b5cf6)"
			if formatted := FormatTime("3 PM", pt.timeStr); formatted != "" {
				timeLabel = formatted
			} else {
				timeLabel = pt.timeStr
			}
		}
		fmt.Fprintf(&sb, `<text x="%.1f" y="124" text-anchor="middle" font-size="11" font-weight="%s" fill="%s" style="user-select: none; pointer-events: none;">%s</text>`,
			pt.x, timeWeight, timeColor, template.HTMLEscapeString(timeLabel))
	}

	sb.WriteString("</svg>")
	return template.HTML(sb.String())
}

package render

import (
	"encoding/json"
	"fmt"
	"html/template"
	"time"
)

var timeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
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
		"json":   JSONHelper,
		"jsonJS": JSONJSHelper,
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

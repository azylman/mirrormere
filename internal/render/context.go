package render

import (
	"strings"

	"github.com/azylman/mirrormere/internal/domain"
)

// Context encapsulates the full unified execution context exposed to widget HTML templates.
// Conforms strictly to SPEC-003 §1 and SPEC-006 §1.A.
type Context struct {
	ID         string           `json:"id"`
	Type       string           `json:"type"`
	Dimensions domain.Dimension `json:"dimensions"`
	Data       any              `json:"data"`
	State      string           `json:"state"`
	Timestamp  string           `json:"timestamp"`
	Config     map[string]any   `json:"config"`
	Origin     [2]int           `json:"origin"`
	Theme      string           `json:"theme"`
	Online     bool             `json:"online"`
	Assets     string           `json:"assets"`
}

// sensitiveKeys defines suffixes or substrings of keys that must be omitted from template context.
var sensitivePatterns = []string{
	"_env",
	"token",
	"secret",
	"password",
	"pass",
	"api_key",
	"apikey",
	"auth",
}

// SanitizeConfig performs a deep clone of the widget instance custom configuration,
// omitting any keys that contain sensitive credential patterns or environment pointers.
func SanitizeConfig(cfg map[string]any) map[string]any {
	if cfg == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(cfg))
	for k, v := range cfg {
		if isSensitiveKey(k) {
			continue
		}
		out[k] = sanitizeValue(v)
	}
	return out
}

func isSensitiveKey(key string) bool {
	lower := strings.ToLower(strings.TrimSpace(key))
	for _, p := range sensitivePatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

func sanitizeValue(v any) any {
	switch val := v.(type) {
	case map[string]any:
		return SanitizeConfig(val)
	case []any:
		clone := make([]any, len(val))
		for i, item := range val {
			clone[i] = sanitizeValue(item)
		}
		return clone
	default:
		return val
	}
}

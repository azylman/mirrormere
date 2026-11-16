package testutil_test

import (
	"testing"

	"github.com/azylman/mirrormere/internal/testutil"
)

// recordingTB captures Fatalf instead of stopping the goroutine so the
// mismatch path can be asserted.
type recordingTB struct {
	testing.TB

	failed bool
}

func (*recordingTB) Helper() {}

func (r *recordingTB) Fatalf(string, ...any) { r.failed = true }

func TestAs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		value      any
		wantValue  string
		wantFailed bool
	}{
		{name: "matching type", value: "kitchen", wantValue: "kitchen", wantFailed: false},
		{name: "mismatched type", value: 42, wantValue: "", wantFailed: true},
		{name: "nil value", value: nil, wantValue: "", wantFailed: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := &recordingTB{TB: t}
			got := testutil.As[string](rec, tc.value)
			if got != tc.wantValue || rec.failed != tc.wantFailed {
				t.Fatalf("As(%v) = %q, failed=%v; want %q, failed=%v", tc.value, got, rec.failed, tc.wantValue, tc.wantFailed)
			}
		})
	}
}

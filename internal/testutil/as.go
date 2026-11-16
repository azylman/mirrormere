// Package testutil holds small helpers shared by mirrormere's tests.
package testutil

import "testing"

// As returns v as a T, failing the test immediately when v holds a different
// dynamic type. It replaces bare v.(T) assertions, which panic and abort the
// whole test binary instead of reporting which value had the wrong type.
func As[T any](tb testing.TB, v any) T {
	tb.Helper()
	got, ok := v.(T)
	if !ok {
		var want T
		tb.Fatalf("value has type %T, want %T", v, want)
	}
	return got
}

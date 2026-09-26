package provider_test

import (
	"errors"
	"net"
	"syscall"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/provider"
)

type customHTTPError struct {
	code int
	msg  string
}

func (e *customHTTPError) Error() string {
	return e.msg
}

func (e *customHTTPError) StatusCode() int {
	return e.code
}

type mockNetTimeoutError struct{}

func (e *mockNetTimeoutError) Error() string   { return "i/o timeout" }
func (e *mockNetTimeoutError) Timeout() bool   { return true }
func (e *mockNetTimeoutError) Temporary() bool { return true }

func TestIsNetworkOrServerError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{name: "nil error", err: nil, expected: false},
		{name: "syscall ECONNREFUSED", err: syscall.ECONNREFUSED, expected: true},
		{name: "syscall ECONNRESET", err: syscall.ECONNRESET, expected: true},
		{name: "syscall ETIMEDOUT", err: syscall.ETIMEDOUT, expected: true},
		{name: "syscall EHOSTUNREACH", err: syscall.EHOSTUNREACH, expected: true},
		{name: "syscall ENETUNREACH", err: syscall.ENETUNREACH, expected: true},
		{name: "wrapped ECONNREFUSED", err: errors.Join(errors.New("dial failed"), syscall.ECONNREFUSED), expected: true},
		{name: "net.Error timeout", err: &mockNetTimeoutError{}, expected: true},
		{name: "net.OpError", err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("socket closed")}, expected: true},
		{name: "HTTP 500 error", err: &customHTTPError{code: 500, msg: "internal server error"}, expected: true},
		{name: "HTTP 502 error", err: &customHTTPError{code: 502, msg: "bad gateway"}, expected: true},
		{name: "HTTP 503 error", err: &customHTTPError{code: 503, msg: "service unavailable"}, expected: true},
		{name: "HTTP 504 error", err: &customHTTPError{code: 504, msg: "gateway timeout"}, expected: true},
		{name: "HTTP 429 rate limit", err: &customHTTPError{code: 429, msg: "too many requests"}, expected: true},
		{name: "HTTP 400 client error", err: &customHTTPError{code: 400, msg: "bad request"}, expected: false},
		{name: "HTTP 401 client error", err: &customHTTPError{code: 401, msg: "unauthorized"}, expected: false},
		{name: "HTTP 403 client error", err: &customHTTPError{code: 403, msg: "forbidden"}, expected: false},
		{name: "HTTP 404 client error", err: &customHTTPError{code: 404, msg: "not found"}, expected: false},
		{name: "string contains connection refused", err: errors.New("dial tcp 127.0.0.1:8080: connect: connection refused"), expected: true},
		{name: "string contains timeout", err: errors.New("request context deadline exceeded (Client.Timeout)"), expected: true},
		{name: "string contains 502 Bad Gateway", err: errors.New("received 502 Bad Gateway from upstream"), expected: true},
		{name: "string contains 429 Too Many Requests", err: errors.New("received 429 Too Many Requests from API"), expected: true},
		{name: "string contains status 503", err: errors.New("upstream failed with status 503"), expected: true},
		{name: "schema validation failure", err: errors.New("data validation failed against response_schema"), expected: false},
		{name: "generic parse error", err: errors.New("json: cannot unmarshal string into Go value"), expected: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := provider.IsNetworkOrServerError(tc.err)
			if got != tc.expected {
				t.Errorf("IsNetworkOrServerError(%v) = %v; want %v", tc.err, got, tc.expected)
			}
		})
	}
}

func TestBackoffPolicy_CalculationAndJitter(t *testing.T) {
	t.Parallel()

	// Deterministic policy with zero jitter (rand returns 0.5 -> (0.5*2 - 1) = 0 jitter)
	boZeroJitter := &provider.BackoffPolicy{
		InitialInterval: 5 * time.Second,
		MaxInterval:     60 * time.Second,
		Multiplier:      2.0,
		JitterFraction:  0.2,
		RandFunc:        func() float64 { return 0.5 },
	}

	intervalCap := 30 * time.Second

	// Attempt 1: 5s
	d1 := boZeroJitter.NextDelay(1, intervalCap)
	if d1 != 5*time.Second {
		t.Errorf("attempt 1: got %v, want 5s", d1)
	}

	// Attempt 2: 10s
	d2 := boZeroJitter.NextDelay(2, intervalCap)
	if d2 != 10*time.Second {
		t.Errorf("attempt 2: got %v, want 10s", d2)
	}

	// Attempt 3: 20s
	d3 := boZeroJitter.NextDelay(3, intervalCap)
	if d3 != 20*time.Second {
		t.Errorf("attempt 3: got %v, want 20s", d3)
	}

	// Attempt 4: 40s -> clamped to intervalCap 30s
	d4 := boZeroJitter.NextDelay(4, intervalCap)
	if d4 != 30*time.Second {
		t.Errorf("attempt 4 clamped: got %v, want 30s", d4)
	}

	// Attempt 5: clamped to intervalCap 30s
	d5 := boZeroJitter.NextDelay(5, intervalCap)
	if d5 != 30*time.Second {
		t.Errorf("attempt 5 clamped: got %v, want 30s", d5)
	}

	// Test with negative failures and fallback defaults
	dDefault := (&provider.BackoffPolicy{}).NextDelay(0, 0)
	if dDefault <= 0 {
		t.Errorf("expected positive default delay, got %v", dDefault)
	}

	// Test Jitter extremes: rand=0.0 (-20% jitter) and rand=1.0 (+20% jitter)
	boMinJitter := &provider.BackoffPolicy{
		InitialInterval: 10 * time.Second,
		MaxInterval:     60 * time.Second,
		Multiplier:      2.0,
		JitterFraction:  0.2,
		RandFunc:        func() float64 { return 0.0 }, // -20%
	}
	dMin := boMinJitter.NextDelay(1, 60*time.Second)
	if dMin != 8*time.Second { // 10s * (1 - 0.20) = 8s
		t.Errorf("min jitter: got %v, want 8s", dMin)
	}

	boMaxJitter := &provider.BackoffPolicy{
		InitialInterval: 10 * time.Second,
		MaxInterval:     60 * time.Second,
		Multiplier:      2.0,
		JitterFraction:  0.2,
		RandFunc:        func() float64 { return 1.0 }, // +20%
	}
	dMax := boMaxJitter.NextDelay(1, 60*time.Second)
	if dMax != 12*time.Second { // 10s * (1 + 0.20) = 12s
		t.Errorf("max jitter: got %v, want 12s", dMax)
	}

	// Hard ceiling test when jitter exceeds intervalCap
	boOverJitter := &provider.BackoffPolicy{
		InitialInterval: 50 * time.Second,
		MaxInterval:     100 * time.Second,
		Multiplier:      2.0,
		JitterFraction:  0.5,
		RandFunc:        func() float64 { return 1.0 },
	}
	dCapped := boOverJitter.NextDelay(1, 60*time.Second)
	if dCapped > 60*time.Second {
		t.Errorf("expected delay clamped to intervalCap 60s, got %v", dCapped)
	}
}

func TestBackoffPolicy_ComputeRetryDelay(t *testing.T) {
	t.Parallel()

	bo := provider.DefaultBackoffPolicy()
	intervalCap := 60 * time.Second

	// 1. Nil error -> regular intervalCap
	if d := bo.ComputeRetryDelay(nil, 1, intervalCap); d != intervalCap {
		t.Fatalf("expected intervalCap %v on nil error, got %v", intervalCap, d)
	}

	// 2. Non-retryable error (e.g. 400 Bad Request) -> regular intervalCap
	clientErr := &customHTTPError{code: 400, msg: "bad request"}
	if d := bo.ComputeRetryDelay(clientErr, 3, intervalCap); d != intervalCap {
		t.Fatalf("expected regular intervalCap %v on client error, got %v", intervalCap, d)
	}

	// 3. Network error -> exponential backoff delay (much less than intervalCap on early failures)
	netErr := syscall.ECONNREFUSED
	dNet := bo.ComputeRetryDelay(netErr, 1, intervalCap)
	if dNet <= 0 || dNet >= intervalCap {
		t.Fatalf("expected backoff delay for network error between 0 and %v, got %v", intervalCap, dNet)
	}
}

func TestBackoffPolicy_EdgeCases(t *testing.T) {
	t.Parallel()

	// InitialInterval <= 0, MaxInterval <= 0, Multiplier < 1.0, JitterFraction < 0
	bo := &provider.BackoffPolicy{
		InitialInterval: -1,
		MaxInterval:     -1,
		Multiplier:      0.5,
		JitterFraction:  -0.5,
	}
	d := bo.NextDelay(1, 10*time.Second)
	if d <= 0 {
		t.Fatalf("expected positive delay, got %v", d)
	}

	// JitterFraction > 1.0
	boOverJitter := &provider.BackoffPolicy{
		InitialInterval: 5 * time.Second,
		MaxInterval:     60 * time.Second,
		Multiplier:      2.0,
		JitterFraction:  1.5,
		RandFunc:        func() float64 { return 0.5 },
	}
	dOver := boOverJitter.NextDelay(1, 60*time.Second)
	if dOver <= 0 {
		t.Fatalf("expected positive delay, got %v", dOver)
	}
}

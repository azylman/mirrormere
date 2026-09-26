package provider_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/provider"
)

func sampleResponseSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"required": []any{
			"temperature",
			"humidity",
		},
		"properties": map[string]any{
			"temperature": map[string]any{"type": "number"},
			"humidity":    map[string]any{"type": "number"},
			"status":      map[string]any{"type": "string"},
		},
	}
}

func TestHTTPProvider_InitValidation(t *testing.T) {
	t.Parallel()

	t.Run("empty endpoint returns error", func(t *testing.T) {
		t.Parallel()
		p := provider.NewHTTPProvider()
		err := p.Init(context.Background(), nil, provider.InitOptions{
			Endpoint:       "",
			ResponseSchema: sampleResponseSchema(),
		})
		if err == nil || !strings.Contains(err.Error(), "requires non-empty endpoint") {
			t.Fatalf("expected empty endpoint error, got: %v", err)
		}
	})

	t.Run("unsupported method returns error", func(t *testing.T) {
		t.Parallel()
		p := provider.NewHTTPProvider()
		err := p.Init(context.Background(), nil, provider.InitOptions{
			Endpoint:       "http://localhost:8080/data",
			Method:         "DELETE",
			ResponseSchema: sampleResponseSchema(),
		})
		if err == nil || !strings.Contains(err.Error(), "unsupported HTTP method") {
			t.Fatalf("expected unsupported method error, got: %v", err)
		}
	})

	t.Run("empty response_schema returns error", func(t *testing.T) {
		t.Parallel()
		p := provider.NewHTTPProvider()
		err := p.Init(context.Background(), nil, provider.InitOptions{
			Endpoint:       "http://localhost:8080/data",
			ResponseSchema: nil,
		})
		if err == nil || !strings.Contains(err.Error(), "requires non-empty response_schema") {
			t.Fatalf("expected empty response_schema error, got: %v", err)
		}
	})

	t.Run("invalid response_schema returns error", func(t *testing.T) {
		t.Parallel()
		p := provider.NewHTTPProvider()
		err := p.Init(context.Background(), nil, provider.InitOptions{
			Endpoint: "http://localhost:8080/data",
			ResponseSchema: map[string]any{
				"type": 12345, // invalid schema type
			},
		})
		if err == nil {
			t.Fatal("expected invalid response_schema error, got nil")
		}
	})
}

func TestHTTPProvider_FetchPOST(t *testing.T) {
	t.Parallel()

	var receivedMethod string
	var receivedHeaders http.Header
	var receivedBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedHeaders = r.Header.Clone()
		_ = json.NewDecoder(r.Body).Decode(&receivedBody)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"temperature": 72.5,
			"humidity":    45.0,
			"status":      "good",
		})
	}))
	defer srv.Close()

	p := provider.NewHTTPProviderWithClient(srv.Client(), nil)
	err := p.Init(context.Background(), map[string]any{
		"entity_id": "sensor.living_room",
		"unit":      "F",
	}, provider.InitOptions{
		ID:             "living-room-temp",
		Type:           "sensor-card",
		Dimensions:     []int{2, 1},
		Endpoint:       srv.URL,
		Method:         "POST",
		Token:          "secret-token-123",
		ResponseSchema: sampleResponseSchema(),
	})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	data, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}

	resMap, ok := data.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", data)
	}
	if resMap["temperature"] != 72.5 || resMap["humidity"] != 45.0 {
		t.Fatalf("unexpected data: %+v", resMap)
	}

	// Verify request headers and method
	if receivedMethod != http.MethodPost {
		t.Errorf("expected POST, got %s", receivedMethod)
	}
	if receivedHeaders.Get("Content-Type") != "application/json" {
		t.Errorf("expected application/json Content-Type, got %s", receivedHeaders.Get("Content-Type"))
	}
	if receivedHeaders.Get("Accept") != "application/json" {
		t.Errorf("expected application/json Accept, got %s", receivedHeaders.Get("Accept"))
	}
	if receivedHeaders.Get("Authorization") != "Bearer secret-token-123" {
		t.Errorf("expected Bearer token, got %s", receivedHeaders.Get("Authorization"))
	}
	if receivedHeaders.Get("X-Widget-ID") != "living-room-temp" {
		t.Errorf("expected X-Widget-ID 'living-room-temp', got %s", receivedHeaders.Get("X-Widget-ID"))
	}
	if receivedHeaders.Get("X-Widget-Type") != "sensor-card" {
		t.Errorf("expected X-Widget-Type 'sensor-card', got %s", receivedHeaders.Get("X-Widget-Type"))
	}
	if receivedHeaders.Get("X-Widget-Dimensions") != "2x1" {
		t.Errorf("expected X-Widget-Dimensions '2x1', got %s", receivedHeaders.Get("X-Widget-Dimensions"))
	}

	// Verify domain config in POST body
	if receivedBody["entity_id"] != "sensor.living_room" || receivedBody["unit"] != "F" {
		t.Errorf("unexpected POST body: %+v", receivedBody)
	}

	// Verify Subscribe and Shutdown
	if err := p.Subscribe(context.Background(), nil); err != nil {
		t.Errorf("Subscribe returned error: %v", err)
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown returned error: %v", err)
	}
}

func TestHTTPProvider_FetchGET(t *testing.T) {
	t.Parallel()

	var receivedMethod string
	var bodyLen int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		b, _ := io.ReadAll(r.Body)
		bodyLen = len(b)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"temperature": 68.0,
			"humidity":    50.0,
		})
	}))
	defer srv.Close()

	p := provider.NewHTTPProviderWithClient(srv.Client(), nil)
	err := p.Init(context.Background(), nil, provider.InitOptions{
		ID:             "outdoor-temp",
		Type:           "sensor-card",
		Endpoint:       srv.URL,
		Method:         "get",
		Secrets:        map[string]string{"token_env": "env-token-456"},
		ResponseSchema: sampleResponseSchema(),
	})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	data, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}

	if receivedMethod != http.MethodGet {
		t.Errorf("expected GET, got %s", receivedMethod)
	}
	if bodyLen > 0 {
		t.Errorf("expected empty body for GET, got %d bytes", bodyLen)
	}

	resMap, ok := data.(map[string]any)
	if !ok || resMap["temperature"] != 68.0 {
		t.Fatalf("unexpected data: %+v", data)
	}
}

func TestHTTPProvider_FetchErrors(t *testing.T) {
	t.Parallel()

	t.Run("non-2xx HTTP status returns error", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "upstream service unavailable", http.StatusServiceUnavailable)
		}))
		defer srv.Close()

		p := provider.NewHTTPProviderWithClient(srv.Client(), nil)
		_ = p.Init(context.Background(), nil, provider.InitOptions{
			Endpoint:       srv.URL,
			ResponseSchema: sampleResponseSchema(),
		})

		_, err := p.Fetch(context.Background())
		if err == nil || !strings.Contains(err.Error(), "HTTP 503") {
			t.Fatalf("expected HTTP 503 error, got: %v", err)
		}
	})

	t.Run("invalid JSON response returns error", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("not valid json"))
		}))
		defer srv.Close()

		p := provider.NewHTTPProviderWithClient(srv.Client(), nil)
		_ = p.Init(context.Background(), nil, provider.InitOptions{
			Endpoint:       srv.URL,
			ResponseSchema: sampleResponseSchema(),
		})

		_, err := p.Fetch(context.Background())
		if err == nil || !strings.Contains(err.Error(), "invalid response JSON") {
			t.Fatalf("expected invalid JSON error, got: %v", err)
		}
	})

	t.Run("non-object JSON response returns error", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[1, 2, 3]`))
		}))
		defer srv.Close()

		p := provider.NewHTTPProviderWithClient(srv.Client(), nil)
		_ = p.Init(context.Background(), nil, provider.InitOptions{
			Endpoint:       srv.URL,
			ResponseSchema: sampleResponseSchema(),
		})

		_, err := p.Fetch(context.Background())
		if err == nil || !strings.Contains(err.Error(), "response body must be a JSON object") {
			t.Fatalf("expected JSON object error, got: %v", err)
		}
	})

	t.Run("response schema validation failure returns error", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			// Missing required "humidity" field
			_ = json.NewEncoder(w).Encode(map[string]any{
				"temperature": 75.0,
			})
		}))
		defer srv.Close()

		p := provider.NewHTTPProviderWithClient(srv.Client(), nil)
		_ = p.Init(context.Background(), nil, provider.InitOptions{
			Endpoint:       srv.URL,
			ResponseSchema: sampleResponseSchema(),
		})

		_, err := p.Fetch(context.Background())
		if err == nil || !strings.Contains(err.Error(), "response_schema validation failed") {
			t.Fatalf("expected response_schema validation error, got: %v", err)
		}
	})

	t.Run("cancelled context returns error", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(100 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		p := provider.NewHTTPProviderWithClient(srv.Client(), nil)
		_ = p.Init(context.Background(), nil, provider.InitOptions{
			Endpoint:       srv.URL,
			ResponseSchema: sampleResponseSchema(),
		})

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		_, err := p.Fetch(ctx)
		if err == nil {
			t.Fatal("expected context cancelled error, got nil")
		}
	})
}

func TestHTTPProvider_LifecycleAndConstructors(t *testing.T) {
	t.Parallel()

	// NewHTTPProvider (default client and logger)
	pDefault := provider.NewHTTPProvider()
	if pDefault == nil {
		t.Fatal("expected non-nil default HTTPProvider")
	}

	// NewHTTPProviderWithClient with nil client and nil logger
	pNilBoth := provider.NewHTTPProviderWithClient(nil, nil)
	if pNilBoth == nil {
		t.Fatal("expected non-nil HTTPProvider from nil args")
	}

	// Subscribe returns nil
	if err := pNilBoth.Subscribe(context.Background(), nil); err != nil {
		t.Errorf("Subscribe() returned unexpected error: %v", err)
	}

	// Shutdown returns nil
	if err := pNilBoth.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown() returned unexpected error: %v", err)
	}
}

func TestHTTPProvider_TokenEnvAndDimensions(t *testing.T) {
	t.Parallel()

	var gotDimensions string
	var gotAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotDimensions = r.Header.Get("X-Widget-Dimensions")
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"temperature": 70.0,
			"humidity":    40.0,
		})
	}))
	defer srv.Close()

	p := provider.NewHTTPProviderWithClient(srv.Client(), nil)
	err := p.Init(context.Background(), nil, provider.InitOptions{
		ID:             "tile-dim",
		Type:           "dim-widget",
		Endpoint:       srv.URL,
		Dimensions:     []int{3, 2},
		Secrets:        map[string]string{"token_env": "secret-from-env"},
		ResponseSchema: sampleResponseSchema(),
	})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	res, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil response")
	}

	if gotDimensions != "3x2" {
		t.Errorf("got X-Widget-Dimensions %q, want '3x2'", gotDimensions)
	}
	if gotAuth != "Bearer secret-from-env" {
		t.Errorf("got Authorization %q, want 'Bearer secret-from-env'", gotAuth)
	}
}

func TestHTTPProvider_MarshalAndURLFailures(t *testing.T) {
	t.Parallel()

	// 1. Init: marshal response_schema error
	p := provider.NewHTTPProvider()
	err := p.Init(context.Background(), nil, provider.InitOptions{
		Endpoint:       "http://example.com/api",
		ResponseSchema: map[string]any{"bad": make(chan int)},
	})
	if err == nil || !strings.Contains(err.Error(), "failed to marshal response_schema") {
		t.Fatalf("expected failed to marshal response_schema error, got: %v", err)
	}

	// 2. Fetch POST: marshal domainConfig error
	pPost := provider.NewHTTPProvider()
	err = pPost.Init(context.Background(), map[string]any{"bad": make(chan int)}, provider.InitOptions{
		Endpoint:       "http://example.com/api",
		Method:         "POST",
		ResponseSchema: sampleResponseSchema(),
	})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	_, err = pPost.Fetch(context.Background())
	if err == nil || !strings.Contains(err.Error(), "failed to marshal domain config") {
		t.Fatalf("expected failed to marshal domain config error, got: %v", err)
	}

	// 3. Fetch: bad request URL error
	pBadURL := provider.NewHTTPProvider()
	err = pBadURL.Init(context.Background(), nil, provider.InitOptions{
		Endpoint:       "://invalid-url",
		Method:         "GET",
		ResponseSchema: sampleResponseSchema(),
	})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	_, err = pBadURL.Fetch(context.Background())
	if err == nil || !strings.Contains(err.Error(), "failed to create HTTP request") {
		t.Fatalf("expected failed to create HTTP request error, got: %v", err)
	}
}



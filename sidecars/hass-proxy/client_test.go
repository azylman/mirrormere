package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_ProcessIntent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		serverMode  string
		reqText     string
		cancelCtx   bool
		wantMatched bool
		wantErr     bool
		wantIntent  string
		wantSpeech  string
	}{
		{"empty_text", "", "   ", false, false, false, "", ""},
		{"success", "ok", "turn off kitchen light", false, true, false, "action_done", "Turned off the kitchen light."},
		{"default_response_type", "default_intent", "test", false, true, false, "action_done", "Done!"},
		{"no_intent_match", "no_match", "unrecognized", false, false, false, "", ""},
		{"empty_speech", "empty_speech", "silent", false, false, false, "", ""},
		{"non_200_error", "status_500", "something", false, false, false, "", ""},
		{"malformed_json", "bad_json", "something", false, false, false, "", ""},
		{"unreachable_server", "unreachable", "turn on lamp", false, false, false, "", ""},
		{"context_canceled", "canceled", "test", true, false, true, "", ""},
		{"invalid_url", "invalid_url", "turn on lamp", false, false, false, "", ""},
		{"truncated_response", "truncated", "test", false, false, false, "", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			url := "http://example.com"
			var srv *httptest.Server
			switch tc.serverMode {
			case "unreachable":
				url = "http://127.0.0.1:54321"
			case "invalid_url":
				url = "http://[invalid-host-bracket"
			case "truncated":
				ln, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatalf("failed to bind listener: %v", err)
				}
				defer ln.Close()
				go func() {
					c, err := ln.Accept()
					if err != nil {
						return
					}
					defer c.Close()
					buf := make([]byte, 1024)
					_, _ = c.Read(buf)
					_, _ = c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nTransfer-Encoding: chunked\r\n\r\n32\r\npartially written content"))
				}()
				url = "http://" + ln.Addr().String()
			case "ok", "default_intent", "no_match", "empty_speech", "status_500", "bad_json", "canceled":
				srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch tc.serverMode {
					case "ok":
						if r.Header.Get("Authorization") != "Bearer test-token" {
							t.Errorf("expected Bearer token, got %q", r.Header.Get("Authorization"))
						}
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusOK)
						_, _ = w.Write([]byte(`{"response":{"speech":{"plain":{"speech":"Turned off the kitchen light."}},"response_type":"action_done"},"conversation_id":"conv-123"}`))
					case "default_intent":
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusOK)
						_, _ = w.Write([]byte(`{"response":{"speech":{"plain":{"speech":"Done!"}}}}`))
					case "no_match":
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusOK)
						_, _ = w.Write([]byte(`{"response":{"response_type":"error","data":{"code":"no_intent_match"}}}`))
					case "empty_speech":
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusOK)
						_, _ = w.Write([]byte(`{"response":{"speech":{"plain":{"speech":"   "}},"response_type":"action_done"}}`))
					case "status_500":
						http.Error(w, "internal server error", http.StatusInternalServerError)
					case "bad_json":
						w.WriteHeader(http.StatusOK)
						_, _ = w.Write([]byte(`{not valid json`))
					case "canceled":
						w.WriteHeader(http.StatusOK)
					}
				}))
				defer srv.Close()
				url = srv.URL
			}

			client := NewHAClient(HomeAssistantConfig{
				URL:       url,
				Token:     "test-token",
				AgentID:   "conversation.home_assistant",
				Language:  "en",
				TimeoutMS: 2000,
			}, nil)

			ctx := context.Background()
			if tc.cancelCtx {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}

			resp, matched, err := client.ProcessIntent(ctx, IntentRequest{Text: tc.reqText, TurnID: "turn-456", Speaker: "alex"})
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v, wantErr=%v", err, tc.wantErr)
			}
			if matched != tc.wantMatched {
				t.Fatalf("matched=%v, wantMatched=%v", matched, tc.wantMatched)
			}
			if tc.wantMatched {
				if resp == nil || resp.Intent != tc.wantIntent || resp.Speech != tc.wantSpeech {
					t.Fatalf("resp=%+v, want Intent=%q Speech=%q", resp, tc.wantIntent, tc.wantSpeech)
				}
			}
		})
	}
}

func TestClient_ProcessIntent_DeviceIDRouting(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		defDev        string
		nodeDevs      map[string]string
		reqNode       string
		expectedDev   string
		expectKeyJSON bool
	}{
		{"mapped_exact", "fallback-dev", map[string]string{"touch-kiosk-kitchen": "kiosk-dev-123"}, "touch-kiosk-kitchen", "kiosk-dev-123", true},
		{"mapped_norm", "fallback-dev", map[string]string{"  touch-kiosk-kitchen  ": "kiosk-dev-123"}, "  TOUCH-KIOSK-KITCHEN  ", "kiosk-dev-123", true},
		{"unmapped_fallback", "fallback-dev", map[string]string{"touch-kiosk-kitchen": "kiosk-dev-123"}, "living-room-display", "fallback-dev", true},
		{"empty_node_fallback", "fallback-dev", map[string]string{"touch-kiosk-kitchen": "kiosk-dev-123"}, "", "fallback-dev", true},
		{"empty_val_fallback", "fallback-dev", map[string]string{"bad-node": "   "}, "bad-node", "fallback-dev", true},
		{"no_default_omits_key", "", map[string]string{"touch-kiosk-kitchen": "kiosk-dev-123"}, "unmapped-node", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var capturedRaw map[string]any
			var capturedReq HAConversationRequest
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				bodyBytes, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(bodyBytes, &capturedRaw)
				_ = json.Unmarshal(bodyBytes, &capturedReq)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"response":{"speech":{"plain":{"speech":"Done"}},"response_type":"action_done"}}`))
			}))
			defer server.Close()

			client := NewHAClient(HomeAssistantConfig{URL: server.URL, DeviceID: tc.defDev, NodeDevices: tc.nodeDevs}, nil)
			resp, matched, err := client.ProcessIntent(context.Background(), IntentRequest{Text: "pause", Node: tc.reqNode})
			if err != nil || !matched || resp == nil {
				t.Fatalf("unexpected result err=%v matched=%v resp=%+v", err, matched, resp)
			}
			if capturedReq.DeviceID != tc.expectedDev {
				t.Errorf("DeviceID=%q, want %q", capturedReq.DeviceID, tc.expectedDev)
			}
			if _, hasKey := capturedRaw["device_id"]; hasKey != tc.expectKeyJSON {
				t.Errorf("hasKey=%v, want %v", hasKey, tc.expectKeyJSON)
			}
		})
	}
}

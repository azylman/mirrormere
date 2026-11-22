package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azylman/mirrormere/internal/testutil"
)

func TestClient_ProcessIntent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		route       testutil.Route
		text        string
		cancelCtx   bool
		wantAuth    string
		wantMatched bool
		wantErr     bool
		wantIntent  string
		wantSpeech  string
	}{
		{name: "empty_text", text: "   "},
		{
			name: "success", text: "turn off kitchen light", wantAuth: "Bearer test-token", wantMatched: true, wantIntent: "action_done", wantSpeech: "Turned off the kitchen light.",
			route: testutil.Route{Body: `{"response":{"speech":{"plain":{"speech":"Turned off the kitchen light."}},"response_type":"action_done"},"conversation_id":"conv-123"}`},
		},
		{name: "default_response_type", route: testutil.Route{Body: `{"response":{"speech":{"plain":{"speech":"Done!"}}}}`}, text: "test", wantAuth: "Bearer test-token", wantMatched: true, wantIntent: "action_done", wantSpeech: "Done!"},
		{name: "no_intent_match", route: testutil.Route{Body: `{"response":{"response_type":"error","data":{"code":"no_intent_match"}}}`}, text: "unrecognized", wantAuth: "Bearer test-token"},
		{name: "empty_speech", route: testutil.Route{Body: `{"response":{"speech":{"plain":{"speech":"   "}},"response_type":"action_done"}}`}, text: "silent", wantAuth: "Bearer test-token"},
		{name: "non_200_error", route: testutil.Route{Status: http.StatusInternalServerError, Body: "internal server error"}, text: "something", wantAuth: "Bearer test-token"},
		{name: "malformed_json", route: testutil.Route{Body: `{not valid json`}, text: "something", wantAuth: "Bearer test-token"},
		{name: "context_canceled", text: "test", cancelCtx: true, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, captured := testutil.RouteServer(t, map[string]testutil.Route{DefaultHAConversationPath: tc.route})
			client := NewHAClient(HomeAssistantConfig{URL: srv.URL, ConversationPath: DefaultHAConversationPath, Token: "test-token", AgentID: "conversation.home_assistant", Language: "en", TimeoutMS: 2000}, nil)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.cancelCtx {
				cancel()
			}
			resp, matched, err := client.ProcessIntent(ctx, IntentRequest{Text: tc.text, TurnID: "turn-456", Speaker: "alpha"})
			if (err != nil) != tc.wantErr || matched != tc.wantMatched {
				t.Fatalf("err=%v matched=%v, want wantErr=%v matched=%v", err, matched, tc.wantErr, tc.wantMatched)
			}
			if tc.wantMatched && (resp == nil || resp.Intent != tc.wantIntent || resp.Speech != tc.wantSpeech) {
				t.Fatalf("resp=%+v, want Intent=%q Speech=%q", resp, tc.wantIntent, tc.wantSpeech)
			}
			if reqs := captured.Requests(); tc.wantAuth != "" && (len(reqs) != 1 || reqs[0].Header.Get("Authorization") != tc.wantAuth) {
				t.Fatalf("requests=%+v, want one with Authorization %q", reqs, tc.wantAuth)
			}
		})
	}
}

// newTruncatedIntentServer answers one request with a chunked 200 whose body
// is cut off mid-chunk, so reading the response fails.
func newTruncatedIntentServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind listener: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
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
	return "http://" + ln.Addr().String()
}

// TestClient_ProcessIntent_Transport covers requests that never yield a usable
// HTTP response; each one is reported as no match without an error.
func TestClient_ProcessIntent_Transport(t *testing.T) {
	t.Parallel()
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	tests := []struct{ name, url string }{
		{"unreachable_server", closed.URL},
		{"invalid_url", "http://[invalid-host-bracket"},
		{"truncated_response", newTruncatedIntentServer(t)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := NewHAClient(HomeAssistantConfig{URL: tc.url, Token: "test-token", TimeoutMS: 2000}, nil)
			resp, matched, err := client.ProcessIntent(t.Context(), IntentRequest{Text: "turn on lamp"})
			if resp != nil || matched || err != nil {
				t.Fatalf("got resp=%+v matched=%v err=%v, want no match and no error", resp, matched, err)
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
			srv, captured := testutil.RouteServer(t, map[string]testutil.Route{"/": {Body: `{"response":{"speech":{"plain":{"speech":"Done"}},"response_type":"action_done"}}`}})
			client := NewHAClient(HomeAssistantConfig{URL: srv.URL + "/", DeviceID: tc.defDev, NodeDevices: tc.nodeDevs}, nil)
			resp, matched, err := client.ProcessIntent(t.Context(), IntentRequest{Text: "pause", Node: tc.reqNode})
			if err != nil || !matched || resp == nil {
				t.Fatalf("unexpected result err=%v matched=%v resp=%+v", err, matched, resp)
			}
			var capturedRaw map[string]any
			var capturedReq HAConversationRequest
			body := []byte(captured.Requests()[0].Body)
			if json.Unmarshal(body, &capturedRaw) != nil || json.Unmarshal(body, &capturedReq) != nil {
				t.Fatalf("request body is not JSON: %s", body)
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

package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClient_ProcessIntent_EmptyText(t *testing.T) {
	client := NewHAClient(HomeAssistantConfig{
		URL: "http://example.com",
	}, nil)

	resp, matched, err := client.ProcessIntent(context.Background(), IntentRequest{Text: "   "})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if matched {
		t.Errorf("expected matched=false for empty text, got true")
	}
	if resp != nil {
		t.Errorf("expected nil response, got %+v", resp)
	}
}

func TestClient_ProcessIntent_Success(t *testing.T) {
	var capturedAuth string
	var capturedBody HAConversationRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		bodyBytes, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(bodyBytes, &capturedBody)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"response": {
				"speech": {
					"plain": {
						"speech": "Turned off the kitchen light."
					}
				},
				"response_type": "action_done"
			},
			"conversation_id": "conv-123"
		}`))
	}))
	defer server.Close()

	client := NewHAClient(HomeAssistantConfig{
		URL:       server.URL,
		Token:     "test-token",
		AgentID:   "conversation.home_assistant",
		Language:  "en",
		TimeoutMS: 2000,
	}, nil)

	resp, matched, err := client.ProcessIntent(context.Background(), IntentRequest{
		Text:    "turn off kitchen light",
		TurnID:  "turn-456",
		Speaker: "alex",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !matched {
		t.Fatalf("expected matched=true, got false")
	}
	if resp == nil {
		t.Fatalf("expected non-nil response")
	}
	if resp.Intent != "action_done" {
		t.Errorf("expected intent action_done, got %q", resp.Intent)
	}
	if resp.Speech != "Turned off the kitchen light." {
		t.Errorf("expected speech match, got %q", resp.Speech)
	}

	if capturedAuth != "Bearer test-token" {
		t.Errorf("expected Authorization header Bearer test-token, got %q", capturedAuth)
	}
	if capturedBody.Text != "turn off kitchen light" {
		t.Errorf("expected forwarded text, got %q", capturedBody.Text)
	}
	if capturedBody.ConversationID != "turn-456" {
		t.Errorf("expected forwarded conversation_id, got %q", capturedBody.ConversationID)
	}
	if capturedBody.AgentID != "conversation.home_assistant" {
		t.Errorf("expected forwarded agent_id, got %q", capturedBody.AgentID)
	}
	if capturedBody.Language != "en" {
		t.Errorf("expected forwarded language, got %q", capturedBody.Language)
	}
}

func TestClient_ProcessIntent_NoIntentMatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"response": {
				"speech": {
					"plain": {
						"speech": "Sorry, I couldn't understand that"
					}
				},
				"response_type": "error",
				"data": {
					"code": "no_intent_match"
				}
			},
			"conversation_id": "conv-999"
		}`))
	}))
	defer server.Close()

	client := NewHAClient(HomeAssistantConfig{
		URL: server.URL,
	}, nil)

	resp, matched, err := client.ProcessIntent(context.Background(), IntentRequest{
		Text: "unrecognized gibberish command",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if matched {
		t.Errorf("expected matched=false for no_intent_match, got true")
	}
	if resp != nil {
		t.Errorf("expected nil response, got %+v", resp)
	}
}

func TestClient_ProcessIntent_EmptySpeech(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"response": {
				"speech": {
					"plain": {
						"speech": "   "
					}
				},
				"response_type": "action_done"
			}
		}`))
	}))
	defer server.Close()

	client := NewHAClient(HomeAssistantConfig{
		URL: server.URL,
	}, nil)

	resp, matched, err := client.ProcessIntent(context.Background(), IntentRequest{
		Text: "trigger silent automation",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if matched {
		t.Errorf("expected matched=false for empty speech, got true")
	}
	if resp != nil {
		t.Errorf("expected nil response, got %+v", resp)
	}
}

func TestClient_ProcessIntent_Non200Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewHAClient(HomeAssistantConfig{
		URL: server.URL,
	}, nil)

	resp, matched, err := client.ProcessIntent(context.Background(), IntentRequest{
		Text: "something",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if matched {
		t.Errorf("expected matched=false for HTTP 500, got true")
	}
	if resp != nil {
		t.Errorf("expected nil response, got %+v", resp)
	}
}

func TestClient_ProcessIntent_MalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{not valid json`))
	}))
	defer server.Close()

	client := NewHAClient(HomeAssistantConfig{
		URL: server.URL,
	}, nil)

	resp, matched, err := client.ProcessIntent(context.Background(), IntentRequest{
		Text: "something",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if matched {
		t.Errorf("expected matched=false on bad JSON, got true")
	}
	if resp != nil {
		t.Errorf("expected nil response, got %+v", resp)
	}
}

func TestClient_ProcessIntent_UnreachableServer(t *testing.T) {
	client := NewHAClient(HomeAssistantConfig{
		URL: "http://127.0.0.1:54321", // unused port
	}, nil)

	resp, matched, err := client.ProcessIntent(context.Background(), IntentRequest{
		Text: "turn on lamp",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if matched {
		t.Errorf("expected matched=false when server unreachable, got true")
	}
	if resp != nil {
		t.Errorf("expected nil response, got %+v", resp)
	}
}

func TestClient_ProcessIntent_ContextCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewHAClient(HomeAssistantConfig{
		URL: server.URL,
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel before call

	_, _, err := client.ProcessIntent(ctx, IntentRequest{Text: "test"})
	if err == nil {
		t.Fatal("expected error on canceled context, got nil")
	}
}

func TestClient_ProcessIntent_DefaultResponseType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"response": {
				"speech": {
					"plain": {
						"speech": "Done!"
					}
				}
			}
		}`))
	}))
	defer server.Close()

	client := NewHAClient(HomeAssistantConfig{
		URL: server.URL,
	}, nil)

	resp, matched, err := client.ProcessIntent(context.Background(), IntentRequest{Text: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !matched {
		t.Fatal("expected matched=true")
	}
	if resp.Intent != "action_done" {
		t.Errorf("expected default intent action_done, got %q", resp.Intent)
	}
}

func TestClient_ProcessIntent_InvalidURL(t *testing.T) {
	client := NewHAClient(HomeAssistantConfig{
		URL: "http://[invalid-host-bracket",
	}, nil)

	resp, matched, err := client.ProcessIntent(context.Background(), IntentRequest{Text: "turn on lamp"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if matched {
		t.Errorf("expected matched=false on invalid URL, got true")
	}
	if resp != nil {
		t.Errorf("expected nil response on invalid URL, got %+v", resp)
	}
}

func TestClient_ProcessIntent_TruncatedResponse(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind listener: %v", err)
	}
	defer listener.Close()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 1024)
		_, _ = conn.Read(buf)
		// Send chunked header promising 50 bytes, but close abruptly
		_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nTransfer-Encoding: chunked\r\n\r\n32\r\npartially written content"))
	}()

	client := NewHAClient(HomeAssistantConfig{
		URL: "http://" + listener.Addr().String(),
	}, nil)

	resp, matched, err := client.ProcessIntent(context.Background(), IntentRequest{Text: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if matched {
		t.Errorf("expected matched=false on truncated response, got true")
	}
	if resp != nil {
		t.Errorf("expected nil response, got %+v", resp)
	}
}

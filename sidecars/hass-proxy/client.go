package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
)

// IntentRequest represents the inbound LAMMAS voice intent payload.
type IntentRequest struct {
	Text    string `json:"text"`
	Speaker string `json:"speaker,omitempty"`
	Node    string `json:"node,omitempty"`
	TurnID  string `json:"turn_id,omitempty"`
	TTS     string `json:"tts,omitempty"`
}

// IntentResponse represents the matched voice intent reply.
type IntentResponse struct {
	Intent string `json:"intent"`
	Speech string `json:"speech"`
}

// HAConversationRequest represents the request payload sent to Home Assistant's conversation API.
type HAConversationRequest struct {
	Text           string `json:"text"`
	ConversationID string `json:"conversation_id,omitempty"`
	AgentID        string `json:"agent_id,omitempty"`
	Language       string `json:"language,omitempty"`
	DeviceID       string `json:"device_id,omitempty"`
}

// HAConversationResponse represents the response returned by Home Assistant's conversation API.
type HAConversationResponse struct {
	Response struct {
		Speech struct {
			Plain struct {
				Speech string `json:"speech"`
			} `json:"plain"`
		} `json:"speech"`
		ResponseType string `json:"response_type"`
		Data         struct {
			Code string `json:"code"`
		} `json:"data"`
	} `json:"response"`
	ConversationID string `json:"conversation_id"`
}

// HAClient defines the interface for communicating with Home Assistant.
type HAClient interface {
	ProcessIntent(ctx context.Context, req IntentRequest) (*IntentResponse, bool, error)
}


// NewHAClient creates a new Home Assistant client with tuned connection pooling and timeouts.
func NewHAClient(cfg HomeAssistantConfig, logger *slog.Logger) *HAClientInstance {
	if logger == nil {
		logger = slog.Default()
	}

	endpoint := fmt.Sprintf("%s%s", cfg.URL, cfg.ConversationPath)

	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   2 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:        50,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 3 * time.Second,
	}

	timeout := time.Duration(cfg.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = DefaultHATimeoutMS * time.Millisecond
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}

	nodeDevs := make(map[string]string)
	for k, v := range cfg.NodeDevices {
		normKey := strings.ToLower(strings.TrimSpace(k))
		normVal := strings.TrimSpace(v)
		if normKey != "" && normVal != "" {
			nodeDevs[normKey] = normVal
		}
	}

	return &HAClientInstance{
		endpoint:        endpoint,
		token:           cfg.Token,
		agentID:         cfg.AgentID,
		language:        cfg.Language,
		defaultDeviceID: strings.TrimSpace(cfg.DeviceID),
		nodeDevices:     nodeDevs,
		httpClient:      client,
		logger:          logger,
	}
}

// HAClientInstance is the concrete implementation of HAClient.
type HAClientInstance struct {
	endpoint        string
	token           string
	agentID         string
	language        string
	defaultDeviceID string
	nodeDevices     map[string]string
	httpClient      *http.Client
	logger          *slog.Logger
}

// ProcessIntent transforms and forwards an IntentRequest to Home Assistant.
// Returns (response, true, nil) on matched intent, (nil, false, nil) on no-match/error, or an error on canceled context.
func (c *HAClientInstance) ProcessIntent(ctx context.Context, req IntentRequest) (*IntentResponse, bool, error) {
	trimmedText := strings.TrimSpace(req.Text)
	if trimmedText == "" {
		c.logger.DebugContext(ctx, "fast-path skip: empty text in request")
		return nil, false, nil
	}

	deviceID := c.defaultDeviceID
	normNode := strings.ToLower(strings.TrimSpace(req.Node))
	if normNode != "" {
		if mappedID, ok := c.nodeDevices[normNode]; ok && mappedID != "" {
			deviceID = mappedID
		}
	}

	haReq := HAConversationRequest{
		Text:           trimmedText,
		ConversationID: req.TurnID,
		AgentID:        c.agentID,
		Language:       c.language,
		DeviceID:       deviceID,
	}

	reqBody, err := json.Marshal(haReq)
	if err != nil {
		c.logger.ErrorContext(ctx, "failed to marshal HA conversation request", "error", err)
		return nil, false, nil
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(reqBody))
	if err != nil {
		c.logger.ErrorContext(ctx, "failed to create HTTP request for HA", "error", err)
		return nil, false, nil
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, false, ctx.Err()
		}
		c.logger.WarnContext(ctx, "home assistant request failed", "error", err)
		return nil, false, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.logger.WarnContext(ctx, "home assistant returned non-200 status", "status", resp.StatusCode)
		return nil, false, nil
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB limit
	if err != nil {
		c.logger.WarnContext(ctx, "failed to read HA response body", "error", err)
		return nil, false, nil
	}

	var haResp HAConversationResponse
	if err := json.Unmarshal(bodyBytes, &haResp); err != nil {
		c.logger.WarnContext(ctx, "failed to parse HA response JSON", "error", err)
		return nil, false, nil
	}

	// Catch error codes or no_intent_match from Home Assistant
	if haResp.Response.ResponseType == "error" || haResp.Response.Data.Code == "no_intent_match" {
		c.logger.DebugContext(ctx, "home assistant indicated no intent match",
			"code", haResp.Response.Data.Code,
			"response_type", haResp.Response.ResponseType,
		)
		return nil, false, nil
	}

	speech := strings.TrimSpace(haResp.Response.Speech.Plain.Speech)
	if speech == "" {
		c.logger.DebugContext(ctx, "home assistant returned empty speech, treating as no match")
		return nil, false, nil
	}

	intent := strings.TrimSpace(haResp.Response.ResponseType)
	if intent == "" {
		intent = "action_done"
	}

	return &IntentResponse{
		Intent: intent,
		Speech: speech,
	}, true, nil
}

# End-to-End Voice Telemetry & Metric Promotion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement end-to-end voice telemetry across the edge kiosk client ("the ear"), Mirrormere Voice Hub, and VictoriaMetrics to provide complete visibility across all 4 pipeline stages (wake extraction, Whisper STT, Aerial deliberation, Kokoro TTS) and speech engine efficiency (RTF, CPS), with false-wake tracking and ambient acoustic floor monitoring.

**Architecture:** Concentrator pattern (Option B) where the edge client remains pure outbound (no open ports or firewall changes), posting ambient heartbeats and turn timings to Mirrormere Voice Hub. Mirrormere Core exports all stage metrics via a single Prometheus `/metrics` endpoint on `aerial-net`. Metrics use strictly bounded low-cardinality labels (`node_id`, `stage`, `status`, `engine`, `voice`), completely excluding `session_id`.

**Tech Stack:** Go 1.24 (`github.com/prometheus/client_golang`), Python 3.11 (`openwakeword`, `urllib`), VictoriaMetrics (`scrape_configs`), PostgreSQL (`aerial-voice-kiosk-hud` Grafana dashboard).

**Spec:** `` and ``.

## Global Constraints
- Pure Go standard library and official `prometheus/client_golang` library for metrics.
- Explicit method signatures: `EdgeTimings` is passed as an explicit parameter to `VoiceHub.Interact`, strictly NO stuffing telemetry timings into `context.Context`.
- Strictly ZERO markdown tables across all artifacts, messages, commits, code comments, and summaries (bulleted lists only).
- Strictly ZERO swallowed errors. Every error path must be logged with structured context or propagated.
- Strictly ZERO host memory inspection.
- Strictly NO `session_id`, `turn_id`, or transcript text as Prometheus metric labels (bounded `node_id`, `stage`, `status`, `engine`, `voice` only).
- Strictly ZERO division-by-zero traps: float casting, `audioSec > 0.001s`, and `ttsDuration > 0.001s` guards for RTF and CPS.
- Bounded cardinality on `node_id`: regex validation (`^[a-zA-Z0-9_-]{1,64}$`), defaulting to `"unknown"` if invalid or empty.
- End-to-end stream timeout safety:
  - In `PostVoiceInteract`: `SetReadDeadline(time.Now().Add(60 * time.Second))` protects multipart audio upload, followed immediately by `SetReadDeadline(time.Time{})` and `SetWriteDeadline(time.Time{})` so the persistent SSE deliberation stream is never terminated prematurely.
  - In `clients/voice/client.py`: `urlopen(req, timeout=120.0)` gives generous room for full multi-step agent tool deliberation and queued processing without timing out.
- Statement coverage floor of strictly >= 95.0% maintained across `internal/voice` and `internal/server`.
- All unit tests must be hermetic and execute in-memory without spawning live OS processes or external network calls.
- Always address Alex directly.

---

### Task 1: Prometheus Metrics Registry in `mirrormere`

**Files:**
- Create: `internal/voice/metrics.go`
- Create: `internal/voice/metrics_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Interfaces:**
- Consumes: `github.com/prometheus/client_golang/prometheus`, `github.com/prometheus/client_golang/prometheus/promauto`
- Produces:
  - `type Metrics struct`
  - `func NewMetrics(reg prometheus.Registerer) *Metrics`
  - `func DefaultMetrics() *Metrics`
  - `func (m *Metrics) RecordStageDuration(nodeID, stage, status string, durationSec float64)`
  - `func (m *Metrics) RecordAmbientRMS(nodeID string, rmsDBFS float64)`
  - `func (m *Metrics) RecordEdgeHeartbeat(nodeID string)`
  - `func (m *Metrics) RecordFalseWake(nodeID string)`
  - `func (m *Metrics) RecordPlaybackDuration(nodeID string, durationSec float64)`
  - `func (m *Metrics) RecordTurn(nodeID, status string)`
  - `func (m *Metrics) RecordError(nodeID, stage, errType string)`
  - `func (m *Metrics) RecordSTTRTF(nodeID, engine string, rtf float64)`
  - `func (m *Metrics) RecordTTSCPS(nodeID, voice string, cps float64)`
  - `func SanitizeNodeID(nodeID string) string`

- [ ] **Step 1: Add prometheus/client_golang to go.mod**

Run: `go get github.com/prometheus/client_golang@v1.21.1` in `C:\Users\alexz\.gemini\antigravity\scratch\mirrormere`
Verify `go.mod` and `go.sum` contain the dependency cleanly.

- [ ] **Step 2: Write failing unit tests for Metrics registry**

Write `internal/voice/metrics_test.go`:
```go
package voice

import (
	"math"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestMetrics_RecordStageDuration(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	m.RecordStageDuration("kitchen-display", "wake_eval", "success", 0.045)
	m.RecordStageDuration("kitchen-display", "stt", "success", 0.320)
	m.RecordStageDuration("kitchen-display", "brain", "success", 1.850)
	m.RecordStageDuration("kitchen-display", "tts", "success", 0.410)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	var found bool
	for _, mf := range mfs {
		if mf.GetName() == "mirrormere_voice_stage_duration_seconds" {
			found = true
			if len(mf.GetMetric()) != 4 {
				t.Fatalf("expected 4 histogram metrics, got %d", len(mf.GetMetric()))
			}
		}
	}
	if !found {
		t.Fatal("mirrormere_voice_stage_duration_seconds metric not found")
	}
}

func TestMetrics_RecordAmbientAndHeartbeat(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	m.RecordAmbientRMS("kitchen-display", -40.5)
	m.RecordEdgeHeartbeat("kitchen-display")
	m.RecordFalseWake("kitchen-display")
	m.RecordPlaybackDuration("kitchen-display", 1.25)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	metricNames := make(map[string]bool)
	for _, mf := range mfs {
		metricNames[mf.GetName()] = true
	}

	expected := []string{
		"mirrormere_voice_ambient_rms_dbfs",
		"mirrormere_voice_edge_last_seen_timestamp_seconds",
		"mirrormere_voice_false_wakes_total",
		"mirrormere_voice_playback_duration_seconds",
	}
	for _, name := range expected {
		if !metricNames[name] {
			t.Fatalf("missing expected metric: %s", name)
		}
	}
}

func TestMetrics_RecordEfficiencyAndZeroDivisions(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	m.RecordTurn("kitchen-display", "success")
	m.RecordError("kitchen-display", "stt", "network_timeout")
	m.RecordSTTRTF("kitchen-display", "whisper_large_v3", 0.18)
	m.RecordTTSCPS("kitchen-display", "kokoro_am_adam", 28.5)

	// Zero division and invalid float protections
	m.RecordSTTRTF("kitchen-display", "whisper_large_v3", math.NaN())
	m.RecordSTTRTF("kitchen-display", "whisper_large_v3", math.Inf(1))
	m.RecordTTSCPS("kitchen-display", "kokoro_am_adam", math.NaN())
	m.RecordTTSCPS("kitchen-display", "kokoro_am_adam", math.Inf(1))
	m.RecordStageDuration("kitchen-display", "stt", "success", math.NaN())
	m.RecordStageDuration("kitchen-display", "stt", "success", -1.0)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	var foundRTF, foundCPS bool
	for _, mf := range mfs {
		if mf.GetName() == "mirrormere_voice_stt_rtf" {
			foundRTF = true
			val := mf.GetMetric()[0].GetGauge().GetValue()
			if val != 0.18 {
				t.Fatalf("expected 0.18, got %f (NaN/Inf should be ignored)", val)
			}
		}
		if mf.GetName() == "mirrormere_voice_tts_chars_per_second" {
			foundCPS = true
			val := mf.GetMetric()[0].GetGauge().GetValue()
			if val != 28.5 {
				t.Fatalf("expected 28.5, got %f (NaN/Inf should be ignored)", val)
			}
		}
	}
	if !foundRTF || !foundCPS {
		t.Fatal("missing RTF or CPS metrics")
	}
}

func TestMetrics_SanitizeNodeID(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"kitchen-display", "kitchen-display"},
		{"living_room_1", "living_room_1"},
		{"", "unknown"},
		{"   ", "unknown"},
		{"node/../../evil", "unknown"},
		{"bad node with spaces", "unknown"},
	}

	for _, tc := range cases {
		actual := SanitizeNodeID(tc.input)
		if actual != tc.expected {
			t.Errorf("SanitizeNodeID(%q) = %q, want %q", tc.input, actual, tc.expected)
		}
	}
}

func TestMetrics_NilSafe(t *testing.T) {
	var m *Metrics
	m.RecordStageDuration("node", "stt", "success", 0.5)
	m.RecordAmbientRMS("node", -30.0)
	m.RecordEdgeHeartbeat("node")
	m.RecordFalseWake("node")
	m.RecordPlaybackDuration("node", 1.0)
	m.RecordTurn("node", "success")
	m.RecordError("node", "tts", "error")
	m.RecordSTTRTF("node", "whisper", 0.2)
	m.RecordTTSCPS("node", "kokoro", 25.0)
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test -v ./internal/voice -run TestMetrics_`
Expected: FAIL with "NewMetrics undefined"

- [ ] **Step 4: Implement Metrics in internal/voice/metrics.go**

```go
package voice

import (
	"math"
	"regexp"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var nodeIDRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// SanitizeNodeID enforces bounded alphanumeric label values.
func SanitizeNodeID(nodeID string) string {
	if !nodeIDRegex.MatchString(nodeID) {
		return "unknown"
	}
	return nodeID
}

// Metrics encapsulates Prometheus collectors for the voice pipeline with bounded cardinality.
type Metrics struct {
	stageDuration    *prometheus.HistogramVec
	ambientRMS       *prometheus.GaugeVec
	lastSeen         *prometheus.GaugeVec
	falseWakes       *prometheus.CounterVec
	playbackDuration *prometheus.HistogramVec
	turnsTotal       *prometheus.CounterVec
	errorsTotal      *prometheus.CounterVec
	sttRTF           *prometheus.GaugeVec
	ttsCPS           *prometheus.GaugeVec
}

var defaultMetrics = NewMetrics(prometheus.DefaultRegisterer)

// DefaultMetrics returns the global default voice metrics collector.
func DefaultMetrics() *Metrics {
	return defaultMetrics
}

// NewMetrics instantiates and registers bounded voice metrics.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	factory := promauto.With(reg)

	return &Metrics{
		stageDuration: factory.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "mirrormere",
				Subsystem: "voice",
				Name:      "stage_duration_seconds",
				Help:      "Duration of voice pipeline stages in seconds.",
				Buckets:   []float64{0.01, 0.025, 0.05, 0.1, 0.15, 0.25, 0.35, 0.5, 0.75, 1.0, 1.5, 2.0, 3.0, 5.0, 10.0},
			},
			[]string{"node_id", "stage", "status"},
		),
		ambientRMS: factory.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "mirrormere",
				Subsystem: "voice",
				Name:      "ambient_rms_dbfs",
				Help:      "Ambient background sound level reported by the edge client in dBFS.",
			},
			[]string{"node_id"},
		),
		lastSeen: factory.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "mirrormere",
				Subsystem: "voice",
				Name:      "edge_last_seen_timestamp_seconds",
				Help:      "Unix timestamp in seconds of the most recent heartbeat from an edge client.",
			},
			[]string{"node_id"},
		),
		falseWakes: factory.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "mirrormere",
				Subsystem: "voice",
				Name:      "false_wakes_total",
				Help:      "Total number of false wake detections aborted on the edge client.",
			},
			[]string{"node_id"},
		),
		playbackDuration: factory.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "mirrormere",
				Subsystem: "voice",
				Name:      "playback_duration_seconds",
				Help:      "Audio playback duration on the edge device in seconds.",
				Buckets:   []float64{0.5, 1.0, 2.0, 3.0, 5.0, 10.0, 15.0, 30.0},
			},
			[]string{"node_id"},
		),
		turnsTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "mirrormere",
				Subsystem: "voice",
				Name:      "turns_total",
				Help:      "Total number of voice interaction turns processed.",
			},
			[]string{"node_id", "status"},
		),
		errorsTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "mirrormere",
				Subsystem: "voice",
				Name:      "errors_total",
				Help:      "Total number of voice pipeline errors partitioned by stage and error type.",
			},
			[]string{"node_id", "stage", "error_type"},
		),
		sttRTF: factory.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "mirrormere",
				Subsystem: "voice",
				Name:      "stt_rtf",
				Help:      "Speech-to-Text Real-Time Factor (inference duration / audio duration).",
			},
			[]string{"node_id", "engine"},
		),
		ttsCPS: factory.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "mirrormere",
				Subsystem: "voice",
				Name:      "tts_chars_per_second",
				Help:      "Text-to-Speech synthesis speed in characters per second.",
			},
			[]string{"node_id", "voice"},
		),
	}
}

// RecordStageDuration logs the duration of a voice pipeline stage.
func (m *Metrics) RecordStageDuration(nodeID, stage, status string, durationSec float64) {
	if m == nil || m.stageDuration == nil {
		return
	}
	if math.IsNaN(durationSec) || math.IsInf(durationSec, 0) || durationSec < 0 || durationSec > 60.0 {
		return
	}
	m.stageDuration.WithLabelValues(SanitizeNodeID(nodeID), stage, status).Observe(durationSec)
}

// RecordAmbientRMS updates the ambient RMS level gauge for a node.
func (m *Metrics) RecordAmbientRMS(nodeID string, rmsDBFS float64) {
	if m == nil || m.ambientRMS == nil {
		return
	}
	if math.IsNaN(rmsDBFS) || math.IsInf(rmsDBFS, 0) {
		return
	}
	m.ambientRMS.WithLabelValues(SanitizeNodeID(nodeID)).Set(rmsDBFS)
}

// RecordEdgeHeartbeat records client heartbeat liveness timestamp.
func (m *Metrics) RecordEdgeHeartbeat(nodeID string) {
	if m == nil || m.lastSeen == nil {
		return
	}
	m.lastSeen.WithLabelValues(SanitizeNodeID(nodeID)).Set(float64(time.Now().Unix()))
}

// RecordFalseWake increments the false wake counter.
func (m *Metrics) RecordFalseWake(nodeID string) {
	if m == nil || m.falseWakes == nil {
		return
	}
	m.falseWakes.WithLabelValues(SanitizeNodeID(nodeID)).Inc()
}

// RecordPlaybackDuration records audio playback duration on the edge.
func (m *Metrics) RecordPlaybackDuration(nodeID string, durationSec float64) {
	if m == nil || m.playbackDuration == nil {
		return
	}
	if math.IsNaN(durationSec) || math.IsInf(durationSec, 0) || durationSec <= 0 || durationSec > 120.0 {
		return
	}
	m.playbackDuration.WithLabelValues(SanitizeNodeID(nodeID)).Observe(durationSec)
}

// RecordTurn increments the interaction turn counter.
func (m *Metrics) RecordTurn(nodeID, status string) {
	if m == nil || m.turnsTotal == nil {
		return
	}
	m.turnsTotal.WithLabelValues(SanitizeNodeID(nodeID), status).Inc()
}

// RecordError increments the error counter for a specific stage.
func (m *Metrics) RecordError(nodeID, stage, errType string) {
	if m == nil || m.errorsTotal == nil {
		return
	}
	m.errorsTotal.WithLabelValues(SanitizeNodeID(nodeID), stage, errType).Inc()
}

// RecordSTTRTF records Whisper Real-Time Factor.
func (m *Metrics) RecordSTTRTF(nodeID, engine string, rtf float64) {
	if m == nil || m.sttRTF == nil {
		return
	}
	if math.IsNaN(rtf) || math.IsInf(rtf, 0) || rtf < 0 {
		return
	}
	if engine == "" {
		engine = "unknown"
	}
	m.sttRTF.WithLabelValues(SanitizeNodeID(nodeID), engine).Set(rtf)
}

// RecordTTSCPS records Kokoro characters-per-second synthesis rate.
func (m *Metrics) RecordTTSCPS(nodeID, voice string, cps float64) {
	if m == nil || m.ttsCPS == nil {
		return
	}
	if math.IsNaN(cps) || math.IsInf(cps, 0) || cps < 0 {
		return
	}
	if voice == "" {
		voice = "unknown"
	}
	m.ttsCPS.WithLabelValues(SanitizeNodeID(nodeID), voice).Set(cps)
}
```

- [ ] **Step 5: Run tests to verify PASS and statement coverage**

Run: `go test -v -cover ./internal/voice -run TestMetrics_`
Expected: PASS with 100% statement coverage on `metrics.go`.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/voice/metrics.go internal/voice/metrics_test.go
git commit -m "feat(voice): implement bounded Prometheus metrics registry with zero-division protections"
```

---

### Task 2: Expose `/metrics` & `/api/voice/heartbeat` in Mirrormere Server

**Files:**
- Modify: `internal/server/server.go`
- Modify: `internal/server/voice.go`
- Modify: `internal/server/voice_test.go`
- Modify: `internal/server/server_test.go`

**Interfaces:**
- Consumes: `promhttp.Handler()` from `github.com/prometheus/client_golang/prometheus/promhttp`
- Produces:
  - Route: `GET /metrics` serves Prometheus metrics.
  - Route: `POST /api/voice/heartbeat` receives `{ "node_id": "...", "ambient_rms_dbfs": -40.6, "false_wakes": 1, "last_playback_sec": 1.2 }`.
  - Interface: `VoiceHandler.PostVoiceHeartbeat(w http.ResponseWriter, r *http.Request)`.
  - Interface: `VoiceHub.Interact(ctx context.Context, audio io.Reader, nodeID, sessionID string, sink voice.SSEEventSink, timings voice.EdgeTimings) error`.

- [ ] **Step 1: Write failing unit test for /metrics and /api/voice/heartbeat**

In `internal/server/server_test.go`:
```go
func TestServer_MetricsEndpoint(t *testing.T) {
	srv := server.New(server.Config{})
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from /metrics, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "# HELP") {
		t.Fatalf("expected prometheus metrics format from /metrics")
	}
}
```

In `internal/server/voice_test.go`:
```go
func TestDefaultVoiceHandler_PostVoiceHeartbeat(t *testing.T) {
	metrics := voice.NewMetrics(prometheus.NewRegistry())
	h := server.NewDefaultVoiceHandler(nil, nil, server.WithVoiceMetrics(metrics))

	// Success case
	payload := `{"node_id":"kitchen-display","ambient_rms_dbfs":-42.3,"false_wakes":2,"last_playback_sec":1.5}`
	req := httptest.NewRequest(http.MethodPost, "/api/voice/heartbeat", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.PostVoiceHeartbeat(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from /api/voice/heartbeat, got %d", rec.Code)
	}

	// Missing node_id case
	badReq := httptest.NewRequest(http.MethodPost, "/api/voice/heartbeat", strings.NewReader(`{}`))
	badReq.Header.Set("Content-Type", "application/json")
	badRec := httptest.NewRecorder()
	h.PostVoiceHeartbeat(badRec, badReq)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request, got %d", badRec.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./internal/server -run "TestServer_MetricsEndpoint|TestDefaultVoiceHandler_PostVoiceHeartbeat"`
Expected: FAIL with missing `/metrics` handler and `PostVoiceHeartbeat` method.

- [ ] **Step 3: Implement /metrics, /api/voice/heartbeat, and explicit Interact signature**

In `internal/server/server.go`:
- Register `/metrics` with `promhttp.Handler()`.
- Wire `/api/voice/heartbeat` to `handleVoiceHeartbeat`.

In `internal/server/voice.go`:
- Update `VoiceHub` interface with explicit `timings` parameter:
```go
type VoiceHub interface {
	IsEnabled() bool
	Interact(ctx context.Context, audio io.Reader, nodeID, sessionID string, sink voice.SSEEventSink, timings voice.EdgeTimings) error
}
```
- Extend `VoiceHandler` interface:
```go
type VoiceHandler interface {
	PostVoiceState(w http.ResponseWriter, r *http.Request)
	PostVoiceInteract(w http.ResponseWriter, r *http.Request)
	PostVoiceHeartbeat(w http.ResponseWriter, r *http.Request)
}
```
- Define `VoiceHeartbeatRequest`:
```go
type VoiceHeartbeatRequest struct {
	NodeID          string   `json:"node_id"`
	AmbientRMSDBFS  *float64 `json:"ambient_rms_dbfs,omitempty"`
	FalseWakes      *int     `json:"false_wakes,omitempty"`
	LastPlaybackSec *float64 `json:"last_playback_sec,omitempty"`
}
```
- Implement `PostVoiceHeartbeat` with zero swallowed errors:
```go
func (h *DefaultVoiceHandler) PostVoiceHeartbeat(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, OPTIONS")
		writeVoiceError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req VoiceHeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeVoiceError(w, http.StatusBadRequest, "invalid heartbeat payload: "+err.Error())
		return
	}

	nodeID := strings.TrimSpace(req.NodeID)
	if nodeID == "" {
		writeVoiceError(w, http.StatusBadRequest, "missing required 'node_id' field")
		return
	}

	if h.metrics != nil {
		h.metrics.RecordEdgeHeartbeat(nodeID)
		if req.AmbientRMSDBFS != nil {
			h.metrics.RecordAmbientRMS(nodeID, *req.AmbientRMSDBFS)
		}
		if req.FalseWakes != nil && *req.FalseWakes > 0 {
			for i := 0; i < *req.FalseWakes; i++ {
				h.metrics.RecordFalseWake(nodeID)
			}
		}
		if req.LastPlaybackSec != nil && *req.LastPlaybackSec > 0 {
			h.metrics.RecordPlaybackDuration(nodeID, *req.LastPlaybackSec)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok"}); err != nil {
		slog.Error("failed to encode heartbeat response", "error", err)
	}
}
```
- In `PostVoiceInteract`:
  - Dynamically set a 60s read deadline for the multipart upload, then disable read and write deadlines for streaming:
    ```go
    rc := http.NewResponseController(w)
    if err := rc.SetReadDeadline(time.Now().Add(60 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
        slog.Debug("failed to set upload read deadline", "error", err)
    }

    r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
    if err := r.ParseMultipartForm(10 << 20); err != nil {
        writeVoiceError(w, http.StatusBadRequest, "invalid multipart form: "+err.Error())
        return
    }

    // Once body is read, disable deadlines completely for the interaction stream
    if err := rc.SetReadDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
        slog.Debug("failed to clear read deadline", "error", err)
    }
    if err := rc.SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
        slog.Debug("failed to clear write deadline", "error", err)
    }
    ```
  - Parse timing fields with explicit validation and logging:
    ```go
    var timings voice.EdgeTimings
    if val := r.FormValue("wake_eval_ms"); val != "" {
        if ms, err := strconv.ParseFloat(val, 64); err == nil && !math.IsNaN(ms) && !math.IsInf(ms, 0) && ms >= 0 && ms <= 60000.0 {
            timings.WakeEvalSec = ms / 1000.0
        } else if err != nil {
            slog.Warn("malformed wake_eval_ms in interact form", "val", val, "error", err)
        }
    }
    if val := r.FormValue("speech_duration_ms"); val != "" {
        if ms, err := strconv.ParseFloat(val, 64); err == nil && !math.IsNaN(ms) && !math.IsInf(ms, 0) && ms >= 0 && ms <= 60000.0 {
            timings.UtteranceSpeechSec = ms / 1000.0
        } else if err != nil {
            slog.Warn("malformed speech_duration_ms in interact form", "val", val, "error", err)
        }
    }
    if val := r.FormValue("silence_duration_ms"); val != "" {
        if ms, err := strconv.ParseFloat(val, 64); err == nil && !math.IsNaN(ms) && !math.IsInf(ms, 0) && ms >= 0 && ms <= 60000.0 {
            timings.UtteranceSilenceSec = ms / 1000.0
        } else if err != nil {
            slog.Warn("malformed silence_duration_ms in interact form", "val", val, "error", err)
        }
    }
    ```
  - Invoke `h.hub.Interact` with explicit `timings`:
    ```go
    if err := h.hub.Interact(r.Context(), file, nodeID, sessionID, sink, timings); err != nil {
        ...
    }
    ```
- Update `mockVoiceHub` and test calls in `internal/server/voice_test.go` to pass `timings voice.EdgeTimings`.

- [ ] **Step 4: Run server tests to verify PASS and statement coverage**

Run: `go test -v -cover ./internal/server`
Expected: PASS with >= 95.0% statement coverage.

- [ ] **Step 5: Commit**

```bash
git add internal/server/server.go internal/server/voice.go internal/server/voice_test.go internal/server/server_test.go
git commit -m "feat(server): expose /metrics endpoint, /api/voice/heartbeat route, and explicit timings signature"
```

---

### Task 3: Instrument Voice Hub Pipeline & Timing Ingestion

**Files:**
- Modify: `internal/voice/hub.go`
- Modify: `internal/voice/hub_test.go`
- Modify: `internal/voice/hub_stream_test.go`
- Modify: `internal/voice/hub_stream_paths_test.go`
- Modify: `internal/voice/speaker_test.go`

**Interfaces:**
- Consumes: `*Metrics` in `Hub`
- Produces:
  - `type EdgeTimings struct { WakeEvalSec, UtteranceSpeechSec, UtteranceSilenceSec float64 }`
  - Explicit signature: `func (h *Hub) Interact(ctx context.Context, audio io.Reader, nodeID, sessionID string, sink SSEEventSink, timings EdgeTimings) error`
  - Robust division-by-zero protected STT RTF and TTS CPS calculation
  - Deferred metric turn and error recording to ensure connection drops are captured

- [ ] **Step 1: Write failing unit test for Voice Hub timing & metrics**

In `internal/voice/hub_test.go`:
```go
func TestHub_MetricsRecording(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewMetrics(reg)

	coord := NewCoordinator(events.NewHub(), nil)
	stt := &mockSTTClient{transcript: "hello world"}
	brain := &mockBrainClient{reply: "hi there"}
	tts := &mockTTSClient{data: []byte("audio"), format: "wav"}

	hub := NewHub(&config.VoiceHubConfig{
		TTSModel: "kokoro",
		TTSSpeaker: "am_adam",
	}, coord, WithSTTClient(stt), WithBrainClient(brain), WithTTSClient(tts), WithMetrics(metrics))

	audioData := createTestWAV(16000, 1.0) // 1.0s of audio
	sink := func(event string, data any) error { return nil }

	timings := EdgeTimings{
		WakeEvalSec:         0.035,
		UtteranceSpeechSec:  1.200,
		UtteranceSilenceSec: 0.500,
	}

	err := hub.Interact(context.Background(), bytes.NewReader(audioData), "kitchen-display", "session-123", sink, timings)
	if err != nil {
		t.Fatalf("expected successful interaction, got %v", err)
	}

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	expectedStages := map[string]bool{
		"wake_eval":         false,
		"utterance_speech":  false,
		"utterance_silence": false,
		"stt":               false,
		"brain":             false,
		"tts":               false,
	}

	for _, mf := range mfs {
		if mf.GetName() == "mirrormere_voice_stage_duration_seconds" {
			for _, m := range mf.GetMetric() {
				for _, lbl := range m.GetLabel() {
					if lbl.GetName() == "stage" {
						expectedStages[lbl.GetValue()] = true
					}
				}
			}
		}
	}

	for stage, seen := range expectedStages {
		if !seen {
			t.Errorf("expected stage metric for %s not found", stage)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./internal/voice -run TestHub_MetricsRecording`
Expected: FAIL with `WithMetrics` or signature mismatch.

- [ ] **Step 3: Implement Hub metrics instrumentation with zero-division protections**

In `internal/voice/hub.go`:
- Define `type EdgeTimings struct { WakeEvalSec, UtteranceSpeechSec, UtteranceSilenceSec float64 }`.
- Update `Interact` signature:
  `func (h *Hub) Interact(ctx context.Context, audio io.Reader, nodeID, sessionID string, sink SSEEventSink, timings EdgeTimings) error`
- In `Interact`:
  - Record edge timings:
    - If `timings.WakeEvalSec > 0`: `h.metrics.RecordStageDuration(nodeID, "wake_eval", "success", timings.WakeEvalSec)`
    - If `timings.UtteranceSpeechSec > 0`: `h.metrics.RecordStageDuration(nodeID, "utterance_speech", "success", timings.UtteranceSpeechSec)`
    - If `timings.UtteranceSilenceSec > 0`: `h.metrics.RecordStageDuration(nodeID, "utterance_silence", "success", timings.UtteranceSilenceSec)`
  - Use `defer` to record `h.metrics.RecordTurn(nodeID, finalStatus)` on return.
  - STT stage:
    - Calculate audio duration cleanly using floats and header subtraction:
      ```go
      pcmBytes := len(wavData) - 44
      if pcmBytes < 0 {
          pcmBytes = 0
      }
      audioSec := float64(pcmBytes) / (16000.0 * 2.0)
      if audioSec > 0.001 {
          rtf := sttDuration / audioSec
          h.metrics.RecordSTTRTF(nodeID, "whisper", rtf)
      }
      ```
  - TTS stage:
    - Guard against division by zero:
      ```go
      if ttsDuration > 0.001 && len(reply) > 0 {
          cps := float64(len(reply)) / ttsDuration
          h.metrics.RecordTTSCPS(nodeID, voiceName, cps)
      }
      ```
    - Streaming TTS: if `AudioStreamingBrainClient` is used, accumulate sentence characters and track elapsed duration between first chunk and final response.
- Update callers in `internal/voice/hub_test.go`, `hub_stream_test.go`, `hub_stream_paths_test.go`, and `speaker_test.go` to pass `EdgeTimings{}`.

- [ ] **Step 4: Run tests to verify PASS and statement coverage**

Run: `go test -v -cover ./internal/voice`
Expected: PASS with >= 95.0% statement coverage.

- [ ] **Step 5: Commit**

```bash
git add internal/voice/hub.go internal/voice/hub_test.go internal/voice/hub_stream_test.go internal/voice/hub_stream_paths_test.go internal/voice/speaker_test.go
git commit -m "feat(voice): instrument Voice Hub pipeline latencies, RTF, and CPS with explicit EdgeTimings"
```

---

### Task 4: Edge Voice Client Instrumentation ("The Ear")

**Files:**
- Modify: `clients/voice/client.py`
- Modify: `clients/voice/tests/test_client.py`

**Interfaces:**
- Consumes: `openwakeword.model.Model`, `urllib.request`
- Produces:
  - Form fields in `POST /api/voice/interact`: `wake_eval_ms`, `speech_duration_ms`, `silence_duration_ms`.
  - Generous interaction timeout: `urlopen(req, timeout=120.0)` for long agent tool deliberation.
  - Non-blocking background heartbeat POSTing `POST /api/voice/heartbeat` with `node_id`, `ambient_rms_dbfs`, `false_wakes`, and `last_playback_sec`.
  - False wake abort tracking.

- [ ] **Step 1: Write failing unit test for edge timing and heartbeat**

In `clients/voice/tests/test_client.py`:
- Add test verifying that `_check_wake_word` records `last_wake_eval_ms`.
- Add test verifying that pre-roll buffer is included in `speech_duration_ms`.
- Add test verifying that `wake_aborted` increments `false_wakes_count`.
- Add test verifying `post_voice_heartbeat` sends JSON payload and catches `URLError` cleanly without raising.

- [ ] **Step 2: Implement edge client instrumentation**

In `clients/voice/client.py`:
- Track `last_wake_eval_ms = 0.0`, `last_speech_duration_ms = 0.0`, `last_silence_duration_ms = 0.0`, `false_wakes_count = 0`, `last_playback_sec = 0.0`.
- In `_check_wake_word`:
  - Measure inference duration via `time.perf_counter()`.
  - `self.last_wake_eval_ms = (time.perf_counter() - t0) * 1000.0`.
- In `process_frame` when utterance completes:
  - Calculate pre-roll duration: `pre_roll_sec = (len(self.pre_roll) * self.cfg.chunk_samples) / float(self.cfg.sample_rate)`.
  - `elapsed = (now - (self.record_start or now)) + pre_roll_sec`.
  - `silence_duration = (now - self.silence_start) if self.silence_start else 0.0`.
  - `self.last_speech_duration_ms = max(0.0, elapsed - silence_duration) * 1000.0`.
  - `self.last_silence_duration_ms = silence_duration * 1000.0`.
- In `process_frame` when `wake_aborted` occurs:
  - `self.false_wakes_count += 1`.
- When preparing an ambient segment:
  - Explicitly reset `self.last_wake_eval_ms = 0.0`.
- In `play_audio`:
  - Measure playback duration: `self.last_playback_sec = time.perf_counter() - play_t0`.
- In `dispatch_hub_interaction` / `_hub_stream_worker`:
  - Increase urlopen timeout to 120.0s:
    ```python
    with urllib.request.urlopen(req, timeout=120.0) as resp:
    ```
  - Attach `wake_eval_ms`, `speech_duration_ms`, `silence_duration_ms` to multipart form.
- In `run()` heartbeat loop:
  - Detach a worker thread to send `POST /api/voice/heartbeat`:
    ```python
    def _send_heartbeat(hub_url: str, payload: dict) -> None:
        try:
            req = urllib.request.Request(
                f"{hub_url.rstrip('/')}/api/voice/heartbeat",
                data=json.dumps(payload).encode("utf-8"),
                headers={"Content-Type": "application/json"},
                method="POST"
            )
            with urllib.request.urlopen(req, timeout=2.0) as resp:
                pass
        except Exception as e:
            logger.debug("Heartbeat failed: %s", e)
    ```

- [ ] **Step 3: Run unit tests to verify PASS**

Run unit tests in `clients/voice/tests/test_client.py`.
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add clients/voice/client.py clients/voice/tests/test_client.py
git commit -m "feat(voice-client): instrument wake latency, extraction duration, false wakes, and ambient heartbeat"
```

---

### Task 5: Windows Socket Reset Fix & Comprehensive Package Verification

**Files:**
- Modify: `internal/voice/hub_test.go`

**Interfaces:**
- Consumes: Wyoming mock TCP server in test fixtures
- Produces: Clean socket drain until `audio-stop` frame before closure on Windows to prevent `WSAECONNRESET`.

- [ ] **Step 1: Inspect and fix Wyoming test server socket drain**

In `internal/voice/hub_test.go`:
In the mock Wyoming server handlers (around line 689 and line 945):
Ensure the mock server parses incoming frames until `"audio-stop"` before sending response and calling `conn.Close()`, preventing Windows winsock unread-buffer reset (`WSAECONNRESET` / 10054).

- [ ] **Step 2: Run all tests in internal/voice and verify 100% PASS**

Run: `go test -v -cover ./internal/voice`
Expected: PASS with >= 95.0% statement coverage across all test runs without flakiness.

- [ ] **Step 3: Run full server and voice package verification**

Run: `go test -v -cover ./internal/server ./internal/voice`
Verify statement coverage is >= 95.0% for both packages.

- [ ] **Step 4: Commit**

```bash
git add internal/voice/hub_test.go
git commit -m "test(voice): drain mock Wyoming socket buffers before closure on Windows"
```

---

### Task 6: VictoriaMetrics Scrape Config in `aerial-config`

**Files:**
- Create: `victoriametrics/mirrormere.yml` in `aerial-config` repo (`C:\Users\alexz\.gemini\antigravity\scratch\aerial-config`)

**Interfaces:**
- Consumes: VictoriaMetrics Prometheus scraper
- Produces: Scrape job targeting `mirrormere-core:8080` on `aerial-net`.

- [ ] **Step 1: Write scrape config**

Create `C:\Users\alexz\.gemini\antigravity\scratch\aerial-config\victoriametrics\mirrormere.yml`:
```yaml
# Mirrormere Core Telemetry Scrape Targets
- job_name: "mirrormere-core"
  scrape_interval: 15s
  static_configs:
    - targets: ["mirrormere-core:8080"]
      labels:
        instance: "mirrormere-core"
        role: "kiosk-hub"
```

- [ ] **Step 2: Verify yaml syntax and git status**

Verify YAML parses cleanly and commit in `aerial-config`.

- [ ] **Step 3: Commit**

```bash
git add victoriametrics/mirrormere.yml
git commit -m "feat(victoriametrics): scrape mirrormere-core voice telemetry"
```

---

### Task 7: Grafana Dashboard Update in PostgreSQL (`aerial-postgres`)

**Files:**
- Database: `aerial-postgres` table `dashboard`, UID `aerial-voice-kiosk-hud`

**Interfaces:**
- Consumes: PostgreSQL connection to `aerial-postgres:5432/aerial`
- Produces: Updated JSON definition for `aerial-voice-kiosk-hud` dashboard with:
  - 4-Stage Waterfall Panel:
    - P95 Latency: `histogram_quantile(0.95, sum(rate(mirrormere_voice_stage_duration_seconds_bucket[5m])) by (le, stage))`
    - Average Latency: `sum(rate(mirrormere_voice_stage_duration_seconds_sum[5m])) by (stage) / sum(rate(mirrormere_voice_stage_duration_seconds_count[5m])) by (stage)`
  - Speech Engine Efficiency Panel:
    - Whisper Real-Time Factor: `mirrormere_voice_stt_rtf`
    - Kokoro Characters Per Second: `mirrormere_voice_tts_chars_per_second`
  - Edge Node Ambient Audio & Liveness Panel:
    - Ambient Sound Floor: `mirrormere_voice_ambient_rms_dbfs`
    - Node Liveness / Last Seen: `time() - mirrormere_voice_edge_last_seen_timestamp_seconds`
    - False Wake Rate: `rate(mirrormere_voice_false_wakes_total[5m])`

- [ ] **Step 1: Fetch current dashboard JSON from PostgreSQL**

Run query on `aerial-postgres` to extract current JSON for `aerial-voice-kiosk-hud`.

- [ ] **Step 2: Add new waterfall and engine efficiency panels**

Add Prometheus queries for `mirrormere_voice_*` metrics using valid histogram quantiles and rate divisions.

- [ ] **Step 3: Update PostgreSQL dashboard row**

Execute update in `aerial-postgres` and verify row updated.

- [ ] **Step 4: Verify metrics querying via VictoriaMetrics MCP**

Query `mirrormere_voice_*` metrics in VictoriaMetrics to verify schema compliance and label cardinality.

---

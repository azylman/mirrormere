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

	// Test boundary condition: durationSec > 300.0 ignored
	m.RecordStageDuration("kitchen-display", "tts", "timeout", 350.0)

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
	// Invalid RMS float guards
	m.RecordAmbientRMS("kitchen-display", math.NaN())
	m.RecordAmbientRMS("kitchen-display", math.Inf(1))

	m.RecordEdgeHeartbeat("kitchen-display")
	m.RecordFalseWake("kitchen-display")
	m.RecordPlaybackDuration("kitchen-display", 1.25)

	// Playback duration boundaries: <= 0, > 120, NaN, Inf
	m.RecordPlaybackDuration("kitchen-display", -0.5)
	m.RecordPlaybackDuration("kitchen-display", 0.0)
	m.RecordPlaybackDuration("kitchen-display", 125.0)
	m.RecordPlaybackDuration("kitchen-display", math.NaN())
	m.RecordPlaybackDuration("kitchen-display", math.Inf(1))

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
	m.RecordSTTRTF("kitchen-display", "whisper_large_v3", -0.5)
	m.RecordSTTRTF("kitchen-display", "", 0.22) // default engine to unknown

	m.RecordTTSCPS("kitchen-display", "kokoro_am_adam", math.NaN())
	m.RecordTTSCPS("kitchen-display", "kokoro_am_adam", math.Inf(1))
	m.RecordTTSCPS("kitchen-display", "kokoro_am_adam", -1.0)
	m.RecordTTSCPS("kitchen-display", "", 24.0) // default voice to unknown

	m.RecordStageDuration("kitchen-display", "stt", "success", math.NaN())
	m.RecordStageDuration("kitchen-display", "stt", "success", math.Inf(1))
	m.RecordStageDuration("kitchen-display", "stt", "success", -1.0)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	var foundRTF, foundCPS, foundTurns, foundErrors bool
	for _, mf := range mfs {
		if mf.GetName() == "mirrormere_voice_turns_total" {
			for _, mMetric := range mf.GetMetric() {
				var nodeID, status string
				for _, label := range mMetric.GetLabel() {
					if label.GetName() == "node_id" {
						nodeID = label.GetValue()
					}
					if label.GetName() == "status" {
						status = label.GetValue()
					}
				}
				if nodeID == "kitchen-display" && status == "success" && mMetric.GetCounter().GetValue() == 1.0 {
					foundTurns = true
				}
			}
		}
		if mf.GetName() == "mirrormere_voice_errors_total" {
			for _, mMetric := range mf.GetMetric() {
				var nodeID, stage, errType string
				for _, label := range mMetric.GetLabel() {
					if label.GetName() == "node_id" {
						nodeID = label.GetValue()
					}
					if label.GetName() == "stage" {
						stage = label.GetValue()
					}
					if label.GetName() == "error_type" {
						errType = label.GetValue()
					}
				}
				if nodeID == "kitchen-display" && stage == "stt" && errType == "network_timeout" && mMetric.GetCounter().GetValue() == 1.0 {
					foundErrors = true
				}
			}
		}
		if mf.GetName() == "mirrormere_voice_stt_rtf" {
			foundRTF = true
			var foundLargeV3, foundUnknown bool
			for _, mMetric := range mf.GetMetric() {
				val := mMetric.GetGauge().GetValue()
				for _, label := range mMetric.GetLabel() {
					if label.GetName() == "engine" && label.GetValue() == "whisper_large_v3" {
						foundLargeV3 = true
						if val != 0.18 {
							t.Fatalf("expected 0.18, got %f (NaN/Inf should be ignored)", val)
						}
					}
					if label.GetName() == "engine" && label.GetValue() == "unknown" {
						foundUnknown = true
						if val != 0.22 {
							t.Fatalf("expected 0.22, got %f", val)
						}
					}
				}
			}
			if !foundLargeV3 || !foundUnknown {
				t.Fatalf("missing expected RTF metric series")
			}
		}
		if mf.GetName() == "mirrormere_voice_tts_chars_per_second" {
			foundCPS = true
			var foundKokoro, foundUnknown bool
			for _, mMetric := range mf.GetMetric() {
				val := mMetric.GetGauge().GetValue()
				for _, label := range mMetric.GetLabel() {
					if label.GetName() == "voice" && label.GetValue() == "kokoro_am_adam" {
						foundKokoro = true
						if val != 28.5 {
							t.Fatalf("expected 28.5, got %f (NaN/Inf should be ignored)", val)
						}
					}
					if label.GetName() == "voice" && label.GetValue() == "unknown" {
						foundUnknown = true
						if val != 24.0 {
							t.Fatalf("expected 24.0, got %f", val)
						}
					}
				}
			}
			if !foundKokoro || !foundUnknown {
				t.Fatalf("missing expected CPS metric series")
			}
		}
	}
	if !foundRTF || !foundCPS {
		t.Fatal("missing RTF or CPS metrics")
	}
	if !foundTurns {
		t.Fatal("missing or incorrect mirrormere_voice_turns_total metric")
	}
	if !foundErrors {
		t.Fatal("missing or incorrect mirrormere_voice_errors_total metric")
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

func TestMetrics_DefaultAndNilReg(t *testing.T) {
	dm := DefaultMetrics()
	if dm == nil {
		t.Fatal("expected DefaultMetrics() to return non-nil Metrics")
	}

	mNil := NewMetrics(nil)
	if mNil == nil {
		t.Fatal("expected NewMetrics(nil) to return non-nil Metrics")
	}
}

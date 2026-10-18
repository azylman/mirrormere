package voice

import (
	"math"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func lbl(kv ...string) map[string]string {
	m := make(map[string]string, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}

func metricVal(t *testing.T, reg *prometheus.Registry, name string, labels map[string]string) (float64, bool) {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			matched := true
			for k, want := range labels {
				var got string
				for _, lp := range m.GetLabel() {
					if lp.GetName() == k {
						got = lp.GetValue()
						break
					}
				}
				if !matched || got != want {
					matched = false
					break
				}
			}
			if !matched {
				continue
			}
			if c := m.GetCounter(); c != nil {
				return c.GetValue(), true
			}
			if g := m.GetGauge(); g != nil {
				return g.GetValue(), true
			}
			if h := m.GetHistogram(); h != nil {
				return float64(h.GetSampleCount()), true
			}
		}
	}
	return 0, false
}

func TestMetrics_Collectors(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	// Stage durations (normal, clamped/ignored > 300, negative, NaN, Inf)
	m.RecordStageDuration("kitchen-display", "wake_eval", "success", 0.045)
	m.RecordStageDuration("kitchen-display", "stt", "success", 0.320)
	m.RecordStageDuration("kitchen-display", "brain", "success", 1.850)
	m.RecordStageDuration("kitchen-display", "tts", "success", 0.410)
	m.RecordStageDuration("kitchen-display", "deliberation", "success", 120.0)
	m.RecordStageDuration("kitchen-display", "deliberation", "timeout", 305.0)
	m.RecordStageDuration("kitchen-display", "tts", "timeout", 350.0)
	m.RecordStageDuration("kitchen-display", "stt", "fail", -1.0)
	m.RecordStageDuration("kitchen-display", "stt", "fail", math.NaN())
	m.RecordStageDuration("kitchen-display", "stt", "fail", math.Inf(1))

	// Ambient RMS & Edge Heartbeat
	m.RecordAmbientRMS("kitchen-display", -40.5)
	m.RecordAmbientRMS("kitchen-display", math.NaN())
	m.RecordAmbientRMS("kitchen-display", math.Inf(1))
	m.RecordEdgeHeartbeat("kitchen-display")

	// False wakes (single, bounded batch, ignored <= 0)
	m.RecordFalseWake("kitchen-display")
	m.RecordFalseWakes("kitchen-display", 0)
	m.RecordFalseWakes("kitchen-display", -5)
	m.RecordFalseWakes("kitchen-display", 5)
	m.RecordFalseWakes("kitchen-display", 1500)

	// Playback duration (normal, bounds: <= 0, > 120, NaN, Inf ignored)
	m.RecordPlaybackDuration("kitchen-display", 1.25)
	m.RecordPlaybackDuration("kitchen-display", -0.5)
	m.RecordPlaybackDuration("kitchen-display", 0.0)
	m.RecordPlaybackDuration("kitchen-display", 125.0)
	m.RecordPlaybackDuration("kitchen-display", math.NaN())
	m.RecordPlaybackDuration("kitchen-display", math.Inf(1))

	// Turns and Errors
	m.RecordTurn("kitchen-display", "success")
	m.RecordError("kitchen-display", "stt", "network_timeout")

	// STT RTF and TTS CPS (with default fallbacks and invalid float protections)
	m.RecordSTTRTF("kitchen-display", "whisper_large_v3", 0.18)
	m.RecordSTTRTF("kitchen-display", "", 0.22)
	m.RecordSTTRTF("kitchen-display", "whisper_large_v3", math.NaN())
	m.RecordSTTRTF("kitchen-display", "whisper_large_v3", math.Inf(1))
	m.RecordSTTRTF("kitchen-display", "whisper_large_v3", -0.5)

	m.RecordTTSCPS("kitchen-display", "kokoro_am_adam", 28.5)
	m.RecordTTSCPS("kitchen-display", "", 24.0)
	m.RecordTTSCPS("kitchen-display", "kokoro_am_adam", math.NaN())
	m.RecordTTSCPS("kitchen-display", "kokoro_am_adam", math.Inf(1))
	m.RecordTTSCPS("kitchen-display", "kokoro_am_adam", -1.0)

	tests := []struct {
		name string
		lbl  map[string]string
		want float64
	}{
		{"mirrormere_voice_stage_duration_seconds", lbl("node_id", "kitchen-display", "stage", "wake_eval", "status", "success"), 1},
		{"mirrormere_voice_stage_duration_seconds", lbl("node_id", "kitchen-display", "stage", "deliberation", "status", "success"), 1},
		{"mirrormere_voice_ambient_rms_dbfs", lbl("node_id", "kitchen-display"), -40.5},
		{"mirrormere_voice_false_wakes_total", lbl("node_id", "kitchen-display"), 1006},
		{"mirrormere_voice_playback_duration_seconds", lbl("node_id", "kitchen-display"), 1},
		{"mirrormere_voice_turns_total", lbl("node_id", "kitchen-display", "status", "success"), 1},
		{"mirrormere_voice_errors_total", lbl("node_id", "kitchen-display", "stage", "stt", "error_type", "network_timeout"), 1},
		{"mirrormere_voice_stt_rtf", lbl("node_id", "kitchen-display", "engine", "whisper_large_v3"), 0.18},
		{"mirrormere_voice_stt_rtf", lbl("node_id", "kitchen-display", "engine", "unknown"), 0.22},
		{"mirrormere_voice_tts_chars_per_second", lbl("node_id", "kitchen-display", "voice", "kokoro_am_adam"), 28.5},
		{"mirrormere_voice_tts_chars_per_second", lbl("node_id", "kitchen-display", "voice", "unknown"), 24.0},
	}
	for _, tc := range tests {
		val, ok := metricVal(t, reg, tc.name, tc.lbl)
		if !ok || val != tc.want {
			t.Errorf("metric %s %v = (%v, %v), want (%v, true)", tc.name, tc.lbl, val, ok, tc.want)
		}
	}

	val, ok := metricVal(t, reg, "mirrormere_voice_edge_last_seen_timestamp_seconds", lbl("node_id", "kitchen-display"))
	if !ok || val <= 0 {
		t.Fatalf("expected last seen timestamp > 0, got %v (%v)", val, ok)
	}

	if _, ok := metricVal(t, reg, "mirrormere_voice_stage_duration_seconds", lbl("node_id", "kitchen-display", "stage", "stt", "status", "fail")); ok {
		t.Error("expected ignored stage duration not to be recorded")
	}
	if _, ok := metricVal(t, reg, "nonexistent_metric", nil); ok {
		t.Error("expected nonexistent metric to return false")
	}
}

func TestMetrics_SanitizeNodeID(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"kitchen-display", "kitchen-display"},
		{"living_room_1", "living_room_1"},
		{"", "unknown"},
		{"   ", "unknown"},
		{"node/../../evil", "unknown"},
		{"bad node with spaces", "unknown"},
	} {
		if got := SanitizeNodeID(tc.in); got != tc.want {
			t.Errorf("SanitizeNodeID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestMetrics_NilSafe(t *testing.T) {
	var m *Metrics
	m.RecordStageDuration("node", "stt", "success", 0.5)
	m.RecordAmbientRMS("node", -30.0)
	m.RecordEdgeHeartbeat("node")
	m.RecordFalseWake("node")
	m.RecordFalseWakes("node", 5)
	m.RecordPlaybackDuration("node", 1.0)
	m.RecordTurn("node", "success")
	m.RecordError("node", "tts", "error")
	m.RecordSTTRTF("node", "whisper", 0.2)
	m.RecordTTSCPS("node", "kokoro", 25.0)
}

func TestMetrics_DefaultAndNilReg(t *testing.T) {
	if DefaultMetrics() == nil || NewMetrics(nil) == nil {
		t.Fatal("expected non-nil Metrics")
	}
}

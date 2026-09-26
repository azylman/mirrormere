package events

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/config"
	"github.com/azylman/mirrormere/internal/domain"
	"github.com/azylman/mirrormere/internal/layout"
)

func TestInMemoryStateProvider_GettersAndSetters(t *testing.T) {
	t.Parallel()

	provider := NewInMemoryStateProvider(nil)

	// 1. Defaults
	if provider.CurrentSnapshot() != nil {
		t.Error("expected nil snapshot initially")
	}
	st := provider.CurrentStatus()
	if st.ConfigStatus != config.ConfigStatusOK {
		t.Errorf("expected default status ok, got %v", st.ConfigStatus)
	}

	vid := provider.GetVideoState()
	if vid.Mode != "widgets" {
		t.Errorf("expected default video mode widgets, got %s", vid.Mode)
	}

	aud := provider.GetAudioState()
	if aud.Volume != 75 || aud.Muted {
		t.Errorf("expected default volume 75, muted false, got %v", aud)
	}

	vc := provider.GetVoiceState()
	if vc.State != "idle" {
		t.Errorf("expected default voice state idle, got %s", vc.State)
	}

	if _, ok := provider.GetHeaderWeather(); ok {
		t.Error("expected no weather initially")
	}

	if _, _, _, ok := provider.GetWidgetState("unknown"); ok {
		t.Error("expected unknown widget to return ok=false")
	}

	// 2. Custom setters
	customSnap := &config.Snapshot{}
	provider.SetSnapshot(customSnap)
	if provider.CurrentSnapshot() != customSnap {
		t.Error("snapshot setter failed")
	}

	errMsg := "reload failed"
	provider.SetStatus(config.Status{
		ConfigStatus: config.ConfigStatusError,
		ConfigError:  &errMsg,
	})
	if provider.CurrentStatus().ConfigStatus != config.ConfigStatusError {
		t.Error("status setter failed")
	}

	provider.SetWidgetState("w1", map[string]string{"foo": "bar"}, "healthy", "2026-09-25T10:00:00Z")
	data, state, ts, ok := provider.GetWidgetState("w1")
	if !ok || state != "healthy" || data == nil || ts != "2026-09-25T10:00:00Z" {
		t.Errorf("widget state setter failed: %v, %v, %v, %v", data, state, ts, ok)
	}
	ts, tsOk := provider.GetWidgetTimestamp("w1")
	if !tsOk || ts != "2026-09-25T10:00:00Z" {
		t.Errorf("expected timestamp '2026-09-25T10:00:00Z', got %q, %v", ts, tsOk)
	}
	if _, emptyOk := provider.GetWidgetTimestamp("unknown-widget"); emptyOk {
		t.Error("expected false for unknown widget timestamp")
	}

	provider.SetHeaderWeather(&HeaderWeather{Temperature: 75.0, Units: "F"})
	weather, ok := provider.GetHeaderWeather()
	if !ok || weather.Temperature != 75.0 {
		t.Errorf("header weather setter failed: %v, %v", weather, ok)
	}

	provider.SetVideoState(&VideoStateData{Mode: "video"})
	if provider.GetVideoState().Mode != "video" {
		t.Error("video setter failed")
	}

	provider.SetAudioState(&AudioStateData{Volume: 50, Muted: true})
	if provider.GetAudioState().Volume != 50 || !provider.GetAudioState().Muted {
		t.Error("audio setter failed")
	}

	transcript := "hello"
	provider.SetVoiceState(&VoiceStateData{State: "listening", Transcript: &transcript})
	if provider.GetVoiceState().State != "listening" || *provider.GetVoiceState().Transcript != "hello" {
		t.Error("voice setter failed")
	}

	// Nil resets
	provider.SetVideoState(nil)
	if provider.GetVideoState().Mode != "widgets" {
		t.Error("expected nil video reset to default")
	}
	provider.SetAudioState(nil)
	if provider.GetAudioState().Volume != 75 {
		t.Error("expected nil audio reset to default")
	}
	provider.SetVoiceState(nil)
	if provider.GetVoiceState().State != "idle" {
		t.Error("expected nil voice reset to default")
	}

	// ScreenRotateData
	if provider.GetScreenRotateData() != nil {
		t.Error("expected nil ScreenRotateData initially")
	}
	provider.SetScreenRotateData(&ScreenRotateData{
		CurrentScreen:   2,
		TotalScreens:    3,
		IntervalSeconds: 15,
	})
	rd := provider.GetScreenRotateData()
	if rd == nil || rd.CurrentScreen != 2 || rd.TotalScreens != 3 || rd.IntervalSeconds != 15 {
		t.Errorf("expected ScreenRotateData to be preserved, got %+v", rd)
	}
	provider.SetScreenRotateData(nil)
	if provider.GetScreenRotateData() != nil {
		t.Error("expected nil ScreenRotateData after nil reset")
	}
}

func TestBuildHydrationBatch_OrderingAndPayloads(t *testing.T) {
	t.Parallel()

	provider := NewInMemoryStateProvider(nil)

	// Mock Snapshot with Layout and Widgets
	cfg := &config.Config{
		Timezone: "America/Los_Angeles",
		Display: config.DisplayConfig{
			Widgets: []config.WidgetConfig{
				{ID: "w1", Type: "clock"},
				{ID: "w2", Type: "weather"},
			},
		},
	}
	lay := &layout.Layout{
		TotalScreens: 1,
		Screens: []layout.Screen{
			{
				Index: 0,
				Widgets: []layout.PlacedWidget{
					{
						WidgetID:   "w1",
						Origin:     [2]int{0, 0},
						Dimensions: domain.NewDimension(3, 2),
					},
				},
			},
		},
	}
	provider.SetSnapshot(&config.Snapshot{
		Config: cfg,
		Layout: lay,
	})

	provider.SetWidgetState("w1", map[string]string{"time": "12:00"}, "healthy")
	// w2 left unset to verify default fallback

	provider.SetHeaderWeather(&HeaderWeather{
		Temperature: 72.0,
		Units:       "F",
		WeatherCode: 1,
		Icon:        "weather-sunny",
	})

	errTxt := "pkg error"
	provider.SetStatus(config.Status{
		ConfigStatus: config.ConfigStatusOK,
	})
	provider.SetProvidersStatus(map[string]string{
		"bad-pkg": errTxt,
	})

	now := time.Date(2026, 9, 25, 20, 0, 0, 0, time.UTC)
	idGen := NewIDGenerator()

	batch := BuildHydrationBatch(provider, idGen, now)

	// Expected items:
	// 1: screen.rotate
	// 2: widget.update (w1)
	// 3: widget.update (w2)
	// 4: header.update
	// 5: video.state
	// 6: audio.state
	// 7: voice.state
	// 8: system.status
	if len(batch) != 8 {
		t.Fatalf("expected 8 hydration events (1 screen + 2 widgets + 5 subsystems), got %d", len(batch))
	}

	// 1. screen.rotate
	if batch[0].Type != EventScreenRotate {
		t.Fatalf("expected item 0 to be %s, got %s", EventScreenRotate, batch[0].Type)
	}
	var rot ScreenRotateData
	if err := json.Unmarshal(batch[0].Data, &rot); err != nil {
		t.Fatalf("failed to unmarshal screen.rotate: %v", err)
	}
	if rot.TotalScreens != 1 || len(rot.Widgets) != 1 || rot.Widgets[0].WidgetID != "w1" {
		t.Errorf("unexpected rotate data: %+v", rot)
	}

	// 2 & 3. widget.update
	if batch[1].Type != EventWidgetUpdate || batch[2].Type != EventWidgetUpdate {
		t.Fatalf("expected items 1 and 2 to be %s, got %s and %s", EventWidgetUpdate, batch[1].Type, batch[2].Type)
	}
	var w1, w2 WidgetUpdateData
	_ = json.Unmarshal(batch[1].Data, &w1)
	_ = json.Unmarshal(batch[2].Data, &w2)
	if w1.WidgetID != "w1" || w1.State != "healthy" {
		t.Errorf("unexpected w1: %+v", w1)
	}
	if w2.WidgetID != "w2" || w2.State != "healthy" {
		t.Errorf("unexpected w2 fallback: %+v", w2)
	}

	// 4. header.update
	if batch[3].Type != EventHeaderUpdate {
		t.Fatalf("expected item 3 to be %s, got %s", EventHeaderUpdate, batch[3].Type)
	}
	var hdr HeaderUpdateData
	if err := json.Unmarshal(batch[3].Data, &hdr); err != nil || hdr.Weather.Temperature != 72.0 {
		t.Errorf("unexpected header update: %+v", hdr)
	}
	if hdr.Timezone != "America/Los_Angeles" {
		t.Errorf("expected header update timezone America/Los_Angeles, got %s", hdr.Timezone)
	}

	// 5. video.state
	if batch[4].Type != EventVideoState {
		t.Fatalf("expected item 4 to be %s, got %s", EventVideoState, batch[4].Type)
	}

	// 6. audio.state
	if batch[5].Type != EventAudioState {
		t.Fatalf("expected item 5 to be %s, got %s", EventAudioState, batch[5].Type)
	}

	// 7. voice.state
	if batch[6].Type != EventVoiceState {
		t.Fatalf("expected item 6 to be %s, got %s", EventVoiceState, batch[6].Type)
	}

	// 8. system.status
	if batch[7].Type != EventSystemStatus {
		t.Fatalf("expected item 7 to be %s, got %s", EventSystemStatus, batch[7].Type)
	}
	var sys SystemStatusData
	if err := json.Unmarshal(batch[7].Data, &sys); err != nil {
		t.Fatalf("failed to unmarshal system.status: %v", err)
	}
	if !sys.Online || sys.ConfigStatus != "ok" || sys.Providers["bad-pkg"] != errTxt {
		t.Errorf("unexpected system status: %+v", sys)
	}
}

func TestBuildHydrationBatch_NilProvider(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 25, 20, 0, 0, 0, time.UTC)
	idGen := NewIDGenerator()

	batch := BuildHydrationBatch(nil, idGen, now)
	// Without widgets, we expect: screen.rotate, header.update, video.state, audio.state, voice.state, system.status (6 events)
	if len(batch) != 6 {
		t.Fatalf("expected 6 events for nil provider, got %d", len(batch))
	}

	expectedTypes := []string{
		EventScreenRotate,
		EventHeaderUpdate,
		EventVideoState,
		EventAudioState,
		EventVoiceState,
		EventSystemStatus,
	}

	for i, exp := range expectedTypes {
		if batch[i].Type != exp {
			t.Errorf("index %d: expected %s, got %s", i, exp, batch[i].Type)
		}
	}
}

type mockLoader struct{}

func (mockLoader) LoadPackage(widgetType string) (*domain.Package, error) {
	return &domain.Package{
		Type: widgetType,
		Manifest: domain.WidgetManifest{
			Name:              "Spacer",
			Version:           "1.0.0",
			Provider:          "spacer",
			DefaultDimensions: domain.NewDimension(6, 2),
		},
	}, nil
}

func TestInMemoryStateProvider_WithManager(t *testing.T) {
	t.Parallel()

	yamlData := []byte(`
timezone: UTC
display:
  grid:
    columns: 6
    rows: 2
  widgets:
    - id: test-w
      type: spacer
      dimensions: [6, 2]
`)
	mgr, err := config.NewManager(yamlData, mockLoader{}, nil, nil)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	provider := NewInMemoryStateProvider(mgr)

	if provider.CurrentSnapshot() == nil {
		t.Error("expected non-nil snapshot from initialized manager")
	}
	st := provider.CurrentStatus()
	if st.ConfigStatus != config.ConfigStatusOK {
		t.Errorf("expected OK status, got %v", st.ConfigStatus)
	}
}

func TestBuildHydrationBatch_WithActiveScreenRotate(t *testing.T) {
	t.Parallel()

	provider := NewInMemoryStateProvider(nil)
	provider.SetScreenRotateData(&ScreenRotateData{
		CurrentScreen:   1,
		TotalScreens:    3,
		IntervalSeconds: 45,
		Widgets: []ScreenRotateWidget{
			{WidgetID: "clock-main", Origin: [2]int{0, 0}, Dimensions: domain.NewDimension(3, 2)},
		},
	})

	idGen := NewIDGenerator()
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	batch := BuildHydrationBatch(provider, idGen, now)

	if len(batch) == 0 {
		t.Fatal("expected non-empty hydration batch")
	}

	first := batch[0]
	if first.Type != EventScreenRotate {
		t.Fatalf("expected first event to be screen.rotate, got %s", first.Type)
	}

	var rotateData ScreenRotateData
	if err := json.Unmarshal(first.Data, &rotateData); err != nil {
		t.Fatalf("failed to decode screen.rotate payload: %v", err)
	}

	if rotateData.CurrentScreen != 1 {
		t.Errorf("expected CurrentScreen 1, got %d", rotateData.CurrentScreen)
	}
	if rotateData.TotalScreens != 3 {
		t.Errorf("expected TotalScreens 3, got %d", rotateData.TotalScreens)
	}
	if rotateData.IntervalSeconds != 45 {
		t.Errorf("expected IntervalSeconds 45, got %d", rotateData.IntervalSeconds)
	}
	if len(rotateData.Widgets) != 1 || rotateData.Widgets[0].WidgetID != "clock-main" {
		t.Errorf("unexpected widgets: %+v", rotateData.Widgets)
	}
}

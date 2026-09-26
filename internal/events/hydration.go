package events

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/azylman/mirrormere/internal/config"
	"github.com/azylman/mirrormere/internal/domain"
)

// ScreenRotateWidget defines placed widget layout geometry in screen.rotate events.
type ScreenRotateWidget struct {
	WidgetID   string           `json:"widget_id"`
	Origin     [2]int           `json:"origin"`
	Dimensions domain.Dimension `json:"dimensions"`
}

// ScreenRotateData matches SPEC-006 §2.C.
type ScreenRotateData struct {
	CurrentScreen   int                  `json:"current_screen"`
	TotalScreens    int                  `json:"total_screens"`
	IntervalSeconds int                  `json:"interval_seconds"`
	Widgets         []ScreenRotateWidget `json:"widgets"`
}

// WidgetUpdateData matches SPEC-006 §2.A.
type WidgetUpdateData struct {
	WidgetID  string `json:"widget_id"`
	Timestamp string `json:"timestamp"`
	State     string `json:"state"`
	Data      any    `json:"data"`
}

// HeaderWeather matches SPEC-006 §2.B and SPEC-007 §4.
type HeaderWeather struct {
	Temperature float64 `json:"temperature"`
	Units       string  `json:"units"`
	WeatherCode int     `json:"weather_code"`
	Icon        string  `json:"icon"`
}

// HeaderUpdateData matches SPEC-006 §2.B and SPEC-012 §5.
type HeaderUpdateData struct {
	Timestamp string         `json:"timestamp"`
	Timezone  string         `json:"timezone,omitempty"`
	Weather   *HeaderWeather `json:"weather"`
}

// VideoStateData matches SPEC-006 §2.E.
type VideoStateData struct {
	Mode    string `json:"mode"`
	Primary any    `json:"primary"`
	Pip     any    `json:"pip"`
}

// AudioStateData matches SPEC-006 §2.F.
type AudioStateData struct {
	Volume int  `json:"volume"`
	Muted  bool `json:"muted"`
}

// VoiceStateData matches SPEC-006 §2.G.
type VoiceStateData struct {
	State      string  `json:"state"`
	Transcript *string `json:"transcript"`
	Reply      *string `json:"reply"`
	TTSEngine  *string `json:"tts_engine"`
}

// SystemStatusData matches api/schemas/system.status.json and SPEC-006 §2.D.
type SystemStatusData struct {
	Online       bool              `json:"online"`
	Time         string            `json:"time"`
	ConfigStatus string            `json:"config_status"`
	ConfigError  *string           `json:"config_error"`
	Providers    map[string]string `json:"providers,omitempty"`
}

// StateProvider supplies active subsystem snapshots for initial connection hydration.
type StateProvider interface {
	CurrentSnapshot() *config.Snapshot
	CurrentStatus() config.Status
	GetWidgetState(widgetID string) (data any, state string, timestamp string, ok bool)
	GetHeaderWeather() (*HeaderWeather, bool)
	GetVideoState() *VideoStateData
	GetAudioState() *AudioStateData
	GetVoiceState() *VoiceStateData
	GetProvidersStatus() map[string]string
	GetScreenRotateData() *ScreenRotateData
}

type widgetStateEntry struct {
	data      any
	state     string
	timestamp string
}

// InMemoryStateProvider maintains thread-safe cached state across subsystems.
type InMemoryStateProvider struct {
	mu           sync.RWMutex
	mgr          *config.Manager
	snapshot     *config.Snapshot
	status       *config.Status
	widgetStates map[string]widgetStateEntry
	weather      *HeaderWeather
	videoState   *VideoStateData
	audioState   *AudioStateData
	voiceState   *VoiceStateData
	providers    map[string]string
	screenRotate *ScreenRotateData
}

// NewInMemoryStateProvider constructs an InMemoryStateProvider.
func NewInMemoryStateProvider(mgr *config.Manager) *InMemoryStateProvider {
	return &InMemoryStateProvider{
		mgr:          mgr,
		widgetStates: make(map[string]widgetStateEntry),
		providers:    make(map[string]string),
		videoState: &VideoStateData{
			Mode:    "widgets",
			Primary: nil,
			Pip:     nil,
		},
		audioState: &AudioStateData{
			Volume: 75,
			Muted:  false,
		},
		voiceState: &VoiceStateData{
			State:      "idle",
			Transcript: nil,
			Reply:      nil,
			TTSEngine:  nil,
		},
	}
}

// SetSnapshot updates the cached snapshot (used when config manager is not provided).
func (p *InMemoryStateProvider) SetSnapshot(snap *config.Snapshot) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.snapshot = snap
}

// CurrentSnapshot returns the active configuration snapshot.
func (p *InMemoryStateProvider) CurrentSnapshot() *config.Snapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.mgr != nil {
		return p.mgr.CurrentSnapshot()
	}
	return p.snapshot
}

// SetStatus updates the cached status.
func (p *InMemoryStateProvider) SetStatus(status config.Status) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status = &status
}

// CurrentStatus returns the latest system status.
func (p *InMemoryStateProvider) CurrentStatus() config.Status {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.mgr != nil {
		return p.mgr.Status()
	}
	if p.status != nil {
		return *p.status
	}
	return config.Status{
		ConfigStatus: config.ConfigStatusOK,
	}
}

// SetWidgetState stores the latest state and domain data for a widget instance.
// Optional timestamp parameter preserves the last successful fetch timestamp for degraded widgets.
func (p *InMemoryStateProvider) SetWidgetState(widgetID string, data any, state string, timestamp ...string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var ts string
	if len(timestamp) > 0 {
		ts = timestamp[0]
	}
	p.widgetStates[widgetID] = widgetStateEntry{data: data, state: state, timestamp: ts}
}

// GetWidgetState returns cached state, domain data, and timestamp for a widget instance.
func (p *InMemoryStateProvider) GetWidgetState(widgetID string) (any, string, string, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	entry, ok := p.widgetStates[widgetID]
	if !ok {
		return nil, "", "", false
	}
	return entry.data, entry.state, entry.timestamp, true
}

// GetWidgetTimestamp returns the recorded timestamp for a widget instance.
func (p *InMemoryStateProvider) GetWidgetTimestamp(widgetID string) (string, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	entry, ok := p.widgetStates[widgetID]
	if !ok || entry.timestamp == "" {
		return "", false
	}
	return entry.timestamp, true
}

// SetHeaderWeather updates the ambient header weather.
func (p *InMemoryStateProvider) SetHeaderWeather(weather *HeaderWeather) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.weather = weather
}

// GetHeaderWeather returns the ambient header weather.
func (p *InMemoryStateProvider) GetHeaderWeather() (*HeaderWeather, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.weather == nil {
		return nil, false
	}
	return p.weather, true
}

// SetVideoState updates video presentation state.
func (p *InMemoryStateProvider) SetVideoState(state *VideoStateData) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.videoState = state
}

// GetVideoState returns video presentation state.
func (p *InMemoryStateProvider) GetVideoState() *VideoStateData {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.videoState == nil {
		return &VideoStateData{Mode: "widgets", Primary: nil, Pip: nil}
	}
	return p.videoState
}

// SetAudioState updates audio volume and mute state.
func (p *InMemoryStateProvider) SetAudioState(state *AudioStateData) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.audioState = state
}

// GetAudioState returns audio state.
func (p *InMemoryStateProvider) GetAudioState() *AudioStateData {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.audioState == nil {
		return &AudioStateData{Volume: 75, Muted: false}
	}
	return p.audioState
}

// SetVoiceState updates voice pipeline state.
func (p *InMemoryStateProvider) SetVoiceState(state *VoiceStateData) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.voiceState = state
}

// GetVoiceState returns voice pipeline state.
func (p *InMemoryStateProvider) GetVoiceState() *VoiceStateData {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.voiceState == nil {
		return &VoiceStateData{State: "idle"}
	}
	return p.voiceState
}

// SetProvidersStatus updates the operational status summary map for providers.
func (p *InMemoryStateProvider) SetProvidersStatus(providers map[string]string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.providers = providers
}

// GetProvidersStatus returns a copy of the operational status summary map for providers.
func (p *InMemoryStateProvider) GetProvidersStatus() map[string]string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.providers) == 0 {
		return nil
	}
	copied := make(map[string]string, len(p.providers))
	for k, v := range p.providers {
		copied[k] = v
	}
	return copied
}

// SetScreenRotateData updates the cached screen rotation data.
func (p *InMemoryStateProvider) SetScreenRotateData(data *ScreenRotateData) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.screenRotate = data
}

// GetScreenRotateData returns the active screen rotation data if set.
func (p *InMemoryStateProvider) GetScreenRotateData() *ScreenRotateData {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.screenRotate == nil {
		return nil
	}
	cp := *p.screenRotate
	return &cp
}

// BuildHydrationBatch constructs the authoritative 7-step initial state hydration batch
// matching the exact sequence mandated by SPEC-006 §3.
func BuildHydrationBatch(provider StateProvider, idGen *IDGenerator, now time.Time) []*Event {
	events := make([]*Event, 0, 8)
	nowStr := now.UTC().Format(time.RFC3339)

	var snap *config.Snapshot
	if provider != nil {
		snap = provider.CurrentSnapshot()
	}

	// 1. Current Screen Configuration (screen.rotate)
	var rotateData ScreenRotateData
	var haveRotateData bool
	if provider != nil {
		if rd := provider.GetScreenRotateData(); rd != nil {
			rotateData = *rd
			haveRotateData = true
		}
	}

	if !haveRotateData {
		rotateData.CurrentScreen = 0
		rotateData.TotalScreens = 1
		rotateData.IntervalSeconds = 30
		rotateData.Widgets = []ScreenRotateWidget{}

		if snap != nil && snap.Config != nil {
			rotateData.IntervalSeconds = snap.Config.Display.Rotation.GetIntervalSeconds()
			if snap.Layout != nil {
				rotateData.TotalScreens = snap.Layout.TotalScreens
				if len(snap.Layout.Screens) > 0 {
					activeScreen := snap.Layout.Screens[0]
					for _, pw := range activeScreen.Widgets {
						rotateData.Widgets = append(rotateData.Widgets, ScreenRotateWidget{
							WidgetID:   pw.WidgetID,
							Origin:     pw.Origin,
							Dimensions: pw.Dimensions,
						})
					}
				}
			}
		}
	}
	rotateBytes, err := json.Marshal(rotateData)
	if err == nil {
		events = append(events, &Event{
			ID:        idGen.Next(now),
			Type:      EventScreenRotate,
			Data:      rotateBytes,
			Timestamp: now,
		})
	}

	// 2. Full Widget State Hydration (widget.update batch across all configured widgets)
	if snap != nil && snap.Config != nil && len(snap.Config.Display.Widgets) > 0 {
		for _, w := range snap.Config.Display.Widgets {
			var wData any = map[string]any{}
			wState := "healthy"
			wTimestamp := nowStr
			if provider != nil {
				if d, s, ts, ok := provider.GetWidgetState(w.ID); ok {
					if d != nil {
						wData = d
					}
					if s != "" {
						wState = s
					}
					if ts != "" {
						wTimestamp = ts
					}
				}
				if tsProvider, ok := provider.(interface{ GetWidgetTimestamp(string) (string, bool) }); ok {
					if ts, ok := tsProvider.GetWidgetTimestamp(w.ID); ok && ts != "" {
						wTimestamp = ts
					}
				}
			}

			payload := WidgetUpdateData{
				WidgetID:  w.ID,
				Timestamp: wTimestamp,
				State:     wState,
				Data:      wData,
			}
			pBytes, err := json.Marshal(payload)
			if err == nil {
				events = append(events, &Event{
					ID:        idGen.Next(now),
					Type:      EventWidgetUpdate,
					Data:      pBytes,
					Timestamp: now,
				})
			}
		}
	}

	// 3. Fixed Header Ambient Weather (header.update)
	var weather *HeaderWeather
	if provider != nil {
		if w, ok := provider.GetHeaderWeather(); ok {
			weather = w
		}
	}
	if weather == nil {
		// Provide default nominal weather if header weather is declared
		weather = &HeaderWeather{
			Temperature: 68.5,
			Units:       "F",
			WeatherCode: 1,
			Icon:        "weather-sunny",
		}
	}
	tz := "UTC"
	if provider != nil {
		if snap := provider.CurrentSnapshot(); snap != nil && snap.Config != nil && snap.Config.Timezone != "" {
			tz = snap.Config.Timezone
		}
	}
	headerBytes, err := json.Marshal(HeaderUpdateData{
		Timestamp: nowStr,
		Timezone:  tz,
		Weather:   weather,
	})
	if err == nil {
		events = append(events, &Event{
			ID:        idGen.Next(now),
			Type:      EventHeaderUpdate,
			Data:      headerBytes,
			Timestamp: now,
		})
	}

	// 4. Active Video Pipeline State (video.state)
	var videoState *VideoStateData
	if provider != nil {
		videoState = provider.GetVideoState()
	}
	if videoState == nil {
		videoState = &VideoStateData{
			Mode:    "widgets",
			Primary: nil,
			Pip:     nil,
		}
	}
	videoBytes, err := json.Marshal(videoState)
	if err == nil {
		events = append(events, &Event{
			ID:        idGen.Next(now),
			Type:      EventVideoState,
			Data:      videoBytes,
			Timestamp: now,
		})
	}

	// 5. Active Audio State (audio.state)
	var audioState *AudioStateData
	if provider != nil {
		audioState = provider.GetAudioState()
	}
	if audioState == nil {
		audioState = &AudioStateData{
			Volume: 75,
			Muted:  false,
		}
	}
	audioBytes, err := json.Marshal(audioState)
	if err == nil {
		events = append(events, &Event{
			ID:        idGen.Next(now),
			Type:      EventAudioState,
			Data:      audioBytes,
			Timestamp: now,
		})
	}

	// 6. Active Voice Pipeline State (voice.state)
	var voiceState *VoiceStateData
	if provider != nil {
		voiceState = provider.GetVoiceState()
	}
	if voiceState == nil {
		voiceState = &VoiceStateData{
			State: "idle",
		}
	}
	voiceBytes, err := json.Marshal(voiceState)
	if err == nil {
		events = append(events, &Event{
			ID:        idGen.Next(now),
			Type:      EventVoiceState,
			Data:      voiceBytes,
			Timestamp: now,
		})
	}

	// 7. System Health Status (system.status)
	var status config.Status
	if provider != nil {
		status = provider.CurrentStatus()
	} else {
		status = config.Status{ConfigStatus: config.ConfigStatusOK}
	}

	configStatus := string(status.ConfigStatus)
	if configStatus == "" {
		configStatus = "ok"
	}

	var providers map[string]string
	if provider != nil {
		providers = provider.GetProvidersStatus()
	}

	sysStatus := SystemStatusData{
		Online:       true,
		Time:         nowStr,
		ConfigStatus: configStatus,
		ConfigError:  status.ConfigError,
		Providers:    providers,
	}
	statusBytes, err := json.Marshal(sysStatus)
	if err == nil {
		events = append(events, &Event{
			ID:        idGen.Next(now),
			Type:      EventSystemStatus,
			Data:      statusBytes,
			Timestamp: now,
		})
	}

	return events
}

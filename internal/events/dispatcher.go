package events

import (
	"encoding/json"
	"time"

	"github.com/azylman/mirrormere/internal/config"
	"github.com/azylman/mirrormere/internal/watcher"
)

// DispatchConfigReload handles configuration updates, layout changes, and domain state resets.
func (h *Hub) DispatchConfigReload(snapshot *config.Snapshot, diff *config.ConfigDiff) error {
	if snapshot == nil {
		return nil
	}

	// 1. Update StateProvider snapshot reference
	if isp, ok := h.stateProvider.(*InMemoryStateProvider); ok {
		isp.SetSnapshot(snapshot)
	}

	// 2. Broadcast screen.rotate via rotation coordinator if registered, or fallback to layout
	if rc := h.RotationCoordinator(); rc != nil {
		rc.UpdateConfig(snapshot)
	} else if snapshot.Layout != nil && len(snapshot.Layout.Screens) > 0 {
		var rotateData ScreenRotateData
		rotateData.CurrentScreen = 0
		rotateData.TotalScreens = snapshot.Layout.TotalScreens
		rotateData.IntervalSeconds = snapshot.Config.Display.Rotation.GetIntervalSeconds()
		rotateData.Widgets = make([]ScreenRotateWidget, 0, len(snapshot.Layout.Screens[0].Widgets))

		for _, pw := range snapshot.Layout.Screens[0].Widgets {
			rotateData.Widgets = append(rotateData.Widgets, ScreenRotateWidget{
				WidgetID:   pw.WidgetID,
				Origin:     pw.Origin,
				Dimensions: pw.Dimensions,
			})
		}

		data, err := json.Marshal(rotateData)
		if err == nil {
			h.Publish(EventScreenRotate, data)
		}
	}

	// 3. For any modified instance whose domain parameters changed, broadcast degraded widget.update per SPEC-012 §3
	if diff != nil {
		now := h.cfg.NowFunc()
		nowStr := now.UTC().Format(time.RFC3339)
		for _, mod := range diff.Modified {
			if mod.DomainChange {
				payload := WidgetUpdateData{
					WidgetID:  mod.ID,
					Timestamp: nowStr,
					State:     "degraded",
					Data:      map[string]any{},
				}
				data, err := json.Marshal(payload)
				if err == nil {
					h.Publish(EventWidgetUpdate, data)
				}
			}
		}

		// 4. If household timezone changed per SPEC-012 §5, broadcast header.update
		if diff.TimezoneChanged && snapshot.Config != nil {
			var weather *HeaderWeather
			if isp, ok := h.stateProvider.(*InMemoryStateProvider); ok {
				if w, ok := isp.GetHeaderWeather(); ok {
					weather = w
				}
			}
			if weather == nil {
				weather = &HeaderWeather{
					Temperature: 68.5,
					Units:       "F",
					WeatherCode: 1,
					Icon:        "weather-sunny",
				}
			}
			tz := snapshot.Config.Timezone
			if tz == "" {
				tz = "UTC"
			}
			headerBytes, err := json.Marshal(HeaderUpdateData{
				Timestamp: nowStr,
				Timezone:  tz,
				Weather:   weather,
			})
			if err == nil {
				h.Publish(EventHeaderUpdate, headerBytes)
			}
		}
	}

	return nil
}

// DispatchStyleReload broadcasts style.reload notifications to connected clients.
func (h *Hub) DispatchStyleReload(event watcher.StyleReloadEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	h.Publish(EventStyleReload, data)
	return nil
}

// DispatchWidgetReload broadcasts widget.reload notifications to connected clients.
func (h *Hub) DispatchWidgetReload(event watcher.WidgetReloadEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	h.Publish(EventWidgetReload, data)
	return nil
}

// DispatchStatus broadcasts system.status telemetry matching api/schemas/system.status.json.
func (h *Hub) DispatchStatus(status config.Status) error {
	// Update in-memory state provider
	if isp, ok := h.stateProvider.(*InMemoryStateProvider); ok {
		isp.SetStatus(status)
	}

	now := h.cfg.NowFunc()
	nowStr := now.UTC().Format(time.RFC3339)

	configStatus := string(status.ConfigStatus)
	if configStatus == "" {
		configStatus = "ok"
	}

	var providers map[string]string
	if h.stateProvider != nil {
		providers = h.stateProvider.GetProvidersStatus()
	}

	sysStatus := SystemStatusData{
		Online:       true,
		Time:         nowStr,
		ConfigStatus: configStatus,
		ConfigError:  status.ConfigError,
		Providers:    providers,
	}

	data, err := json.Marshal(sysStatus)
	if err != nil {
		return err
	}

	h.Publish(EventSystemStatus, data)
	return nil
}

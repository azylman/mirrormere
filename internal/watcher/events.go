package watcher

import (
	"github.com/azylman/mirrormere/internal/config"
)

// StyleReloadEvent matches api/schemas/style.reload.json.
type StyleReloadEvent struct {
	File      string `json:"file"`
	Timestamp string `json:"timestamp"`
}

// WidgetReloadEvent matches api/schemas/widget.reload.json.
type WidgetReloadEvent struct {
	Type string `json:"type"`
}

// Dispatcher receives validated reload notifications and status telemetry updates.
type Dispatcher interface {
	DispatchConfigReload(snapshot *config.Snapshot, diff *config.ConfigDiff) error
	DispatchStyleReload(event StyleReloadEvent) error
	DispatchWidgetReload(event WidgetReloadEvent) error
	DispatchStatus(status config.Status) error
}


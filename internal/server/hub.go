package server

import (
	"errors"
	"home-control/device"
	"home-control/internal/extension"
	"slices"
)

// Hub joins all adapters behind one Devices and Write.
type Hub struct {
	Adapters []device.Adapter
}

// Devices lists every device from every adapter.
func (h *Hub) Devices() []device.Device {
	out := []device.Device{}
	for _, a := range h.Adapters {
		out = append(out, a.Devices()...)
	}
	return out
}

// ErrUnknownDevice means no adapter has the device.
var ErrUnknownDevice = errors.New("unknown device")

// Write routes a write to the adapter that owns the device.
func (h *Hub) Write(id, dp string, value any) error {
	for _, a := range h.Adapters {
		for _, d := range a.Devices() {
			if d.ID == id {
				return a.Write(id, dp, value)
			}
		}
	}
	return ErrUnknownDevice
}

// Integration is one service on the Integrations page, with its devices.
type Integration struct {
	Type        string            `json:"type"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Devices     []IntegrationItem `json:"devices"`
	Extension   *extension.Status `json:"extension,omitempty"`
}

// IntegrationItem is a device as that page lists it.
type IntegrationItem struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Online bool   `json:"online"`
}

// Integrations lists every service that provides devices.
func (h *Hub) Integrations(extensions []extension.Status) []Integration {
	byAdapter := map[string][]IntegrationItem{}
	for _, d := range h.Devices() {
		byAdapter[d.Adapter] = append(byAdapter[d.Adapter], IntegrationItem{d.ID, d.Name, d.Kind, d.Online})
	}
	out := []Integration{}
	take := func(key string) []IntegrationItem {
		items := byAdapter[key]
		delete(byAdapter, key)
		if items == nil {
			return []IntegrationItem{}
		}
		return items
	}
	for _, status := range extensions {
		if status.ID != "" && !slices.Contains(status.Provides, "devices") {
			continue // only data: it belongs on the Plugins page
		}
		status := status
		name, typ := status.Name, status.ID
		if name == "" {
			name, typ = status.Source, status.Source
		}
		out = append(out, Integration{Type: typ, Name: name, Description: status.Description, Devices: take(status.ID), Extension: &status})
	}
	rest := make([]string, 0, len(byAdapter))
	for adapter := range byAdapter {
		rest = append(rest, adapter)
	}
	slices.Sort(rest)
	for _, adapter := range rest {
		out = append(out, Integration{Type: adapter, Name: adapter, Devices: take(adapter)})
	}
	return out
}

// Package plugin holds the latest data of every plugin, which automation scripts read as home.plugin("id").data().
//
// A plugin is an extension that provides data or actions. See docs/plugins.md.
package plugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

// Decode reads an entry in config.json strictly, so a mistyped setting fails at startup instead of being ignored.
func Decode(raw json.RawMessage, out any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(out)
}

// Status is what the page shows about a plugin.
type Status struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Data      json.RawMessage `json:"data,omitempty"`
	UpdatedAt time.Time       `json:"updatedAt,omitzero"`
	Error     string          `json:"error,omitempty"`
	Actions   []string        `json:"actions,omitempty"`
}

// Hub holds every plugin's latest data.
type Hub struct {
	mu      sync.RWMutex
	plugins map[string]*Status
	act     map[string]func(name string, args []byte) error
}

// NewHub returns an empty hub. Extensions claim their plugins as they start.
func NewHub() *Hub {
	return &Hub{plugins: map[string]*Status{}, act: map[string]func(string, []byte) error{}}
}

// Claim adds a plugin whose data something else publishes, such as an extension.
func (h *Hub) Claim(id, typ string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, taken := h.plugins[id]; taken {
		return fmt.Errorf("the plugin id %q is already used", id)
	}
	h.plugins[id] = &Status{ID: id, Type: typ}
	return nil
}

// Remove drops a claimed plugin.
func (h *Hub) Remove(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.plugins, id)
	delete(h.act, id)
}

// SetActions says what scripts can ask a claimed plugin to do. send must queue and return, since scripts wait for it.
func (h *Hub) SetActions(id string, names []string, send func(name string, args []byte) error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if p, ok := h.plugins[id]; ok {
		p.Actions, h.act[id] = names, send
	}
}

// Act asks a plugin to do something, with its arguments as JSON.
func (h *Hub) Act(id, name string, args []byte) error {
	h.mu.RLock()
	p, ok := h.plugins[id]
	send := h.act[id]
	var known bool
	if ok {
		known = slices.Contains(p.Actions, name)
	}
	h.mu.RUnlock()
	if !known || send == nil {
		return fmt.Errorf("plugin %s has no action %q", id, name)
	}
	return send(name, args)
}

// Publish updates a claimed plugin. A failure keeps the last good data, and scripts can tell from its age.
func (h *Hub) Publish(id string, data []byte, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	p, ok := h.plugins[id]
	if !ok {
		return
	}
	if err != nil {
		p.Error = err.Error()
		return
	}
	p.Data, p.UpdatedAt, p.Error = data, time.Now(), ""
}

// Get returns a plugin's latest data as JSON, nil until its first successful fetch. ok is false for an unknown id.
func (h *Hub) Get(id string) (data []byte, updatedAt time.Time, ok bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	p, ok := h.plugins[id]
	if !ok {
		return nil, time.Time{}, false
	}
	return p.Data, p.UpdatedAt, true
}

// Statuses lists the plugins by id.
func (h *Hub) Statuses() []Status {
	h.mu.RLock()
	defer h.mu.RUnlock()
	list := make([]Status, 0, len(h.plugins))
	for _, p := range h.plugins {
		list = append(list, *p)
	}
	slices.SortFunc(list, func(a, b Status) int { return strings.Compare(a.ID, b.ID) })
	return list
}

package store

import (
	"context"
	"maps"
	"slices"
)

func (m *Memory) HomeSettings(context.Context) (HomeSettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.home, m.Fail
}

func (m *Memory) SetHomeSettings(_ context.Context, h HomeSettings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	m.home = h
	return nil
}

func (m *Memory) DevicePins(context.Context) (map[string][]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string][]string, len(m.pins))
	for id, dps := range m.pins {
		out[id] = slices.Clone(dps)
	}
	return out, nil
}

func (m *Memory) SetDevicePins(_ context.Context, device string, dps []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pins == nil {
		m.pins = map[string][]string{}
	}
	if len(dps) == 0 {
		delete(m.pins, device)
	} else {
		m.pins[device] = slices.Clone(dps)
	}
	return nil
}

func (m *Memory) DeviceOverrides(context.Context) ([]DeviceOverride, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]DeviceOverride, 0, len(m.overrides))
	for _, id := range slices.Sorted(maps.Keys(m.overrides)) {
		o := m.overrides[id]
		if o.Room != nil {
			room := *o.Room
			o.Room = &room
		}
		out = append(out, o)
	}
	return out, nil
}

func (m *Memory) SetDeviceOverride(_ context.Context, o DeviceOverride) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.overrides == nil {
		m.overrides = map[string]DeviceOverride{}
	}
	if o.Name == "" && o.Room == nil {
		delete(m.overrides, o.ID)
		return nil
	}
	if o.Room != nil {
		room := *o.Room
		o.Room = &room
	}
	m.overrides[o.ID] = o
	return nil
}

func (m *Memory) Dashboard(context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.dashboard), nil
}

func (m *Memory) SetDashboard(_ context.Context, widgets []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dashboard = append([]string{}, widgets...) // saved empty is not never arranged
	return nil
}

func (m *Memory) Rooms(context.Context) ([]string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.rooms), m.roomsSeeded, nil
}

func (m *Memory) SetRooms(_ context.Context, names []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rooms, m.roomsSeeded = slices.Clone(names), true
	return nil
}

// moveRoom points every device in one room at another; to is empty for none.
func (m *Memory) moveRoom(from, to string) {
	for id, o := range m.overrides {
		if o.Room != nil && *o.Room == from {
			room := to
			o.Room = &room
			m.overrides[id] = o
		}
	}
}

func (m *Memory) RenameRoom(_ context.Context, from, to string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	i := slices.Index(m.rooms, from)
	if i < 0 {
		return ErrNoRoom
	}
	m.rooms[i] = to
	m.moveRoom(from, to)
	return nil
}

func (m *Memory) DeleteRoom(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	i := slices.Index(m.rooms, name)
	if i < 0 {
		return ErrNoRoom
	}
	m.rooms = slices.Delete(m.rooms, i, i+1)
	m.moveRoom(name, "")
	return nil
}

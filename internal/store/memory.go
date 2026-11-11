package store

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"sync"
)

// Memory is a Store that forgets everything when the process ends, for tests.
type Memory struct {
	installKey   []byte
	marketState  MarketState
	removed      []RemovedDevice
	pins         map[string][]string
	home         HomeSettings
	overrides    map[string]DeviceOverride
	rooms        []string
	dashboard    []string
	roomsSeeded  bool
	mu           sync.Mutex
	live         map[string]bool
	events       []Event
	added        []AddedExtension
	grants       map[string][]string
	scriptGrants map[string][]string
	hooks        map[string]string
	violations   []Violation
	activity     []Activity
	removedExt   []RemovedExtension
	storage      map[string]map[string]json.RawMessage
	packages     map[string][]byte
	users        []User
	sessions     map[string]memorySession
	tokens       []memoryToken
	audit        []AuditEntry

	// Fail makes every call return this error, to test what happens when the database is down.
	Fail error
}

// NewMemory returns an empty store.
func NewMemory() *Memory {
	return &Memory{live: map[string]bool{}, grants: map[string][]string{}, scriptGrants: map[string][]string{}, hooks: map[string]string{}, storage: map[string]map[string]json.RawMessage{}, packages: map[string][]byte{}, sessions: map[string]memorySession{}}
}

func (m *Memory) ScriptsLive(context.Context) (map[string]bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return nil, m.Fail
	}
	return maps.Clone(m.live), nil
}

func (m *Memory) SetScriptLive(_ context.Context, name string, live bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	m.live[name] = live
	return nil
}

func (m *Memory) ScriptGrants(context.Context) (map[string][]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return nil, m.Fail
	}
	out := map[string][]string{}
	for script, keys := range m.scriptGrants {
		out[script] = slices.Sorted(slices.Values(keys))
	}
	return out, nil
}

func (m *Memory) SetScriptGrants(_ context.Context, script string, keys []string, granted bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	kept := slices.DeleteFunc(slices.Clone(m.scriptGrants[script]), func(k string) bool { return slices.Contains(keys, k) })
	if granted {
		kept = append(kept, keys...)
		slices.Sort(kept)
		kept = slices.Compact(kept)
	}
	if len(kept) == 0 {
		delete(m.scriptGrants, script)
	} else {
		m.scriptGrants[script] = kept
	}
	return nil
}

func (m *Memory) AppendEvents(_ context.Context, events []Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	m.events = append(m.events, events...)
	return nil
}

func (m *Memory) RecentEvents(_ context.Context, limit int) ([]Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return nil, m.Fail
	}
	return slices.Clone(m.events[max(0, len(m.events)-limit):]), nil
}

func (m *Memory) AddedExtensions(context.Context) ([]AddedExtension, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return nil, m.Fail
	}
	return slices.Clone(m.added), nil
}

func (m *Memory) SaveAddedExtension(_ context.Context, extension AddedExtension) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	m.added = slices.DeleteFunc(m.added, func(a AddedExtension) bool { return a.Source == extension.Source })
	m.added = append(m.added, extension)
	return nil
}

func (m *Memory) DeleteAddedExtension(_ context.Context, source string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	m.added = slices.DeleteFunc(m.added, func(a AddedExtension) bool { return a.Source == source })
	return nil
}

func (m *Memory) Grants(_ context.Context, extension string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return nil, m.Fail
	}
	return slices.Clone(m.grants[extension]), nil
}

func (m *Memory) SetGrants(_ context.Context, extension string, grants []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	if len(grants) == 0 {
		delete(m.grants, extension)
	} else {
		m.grants[extension] = slices.Clone(grants)
	}
	return nil
}

func (m *Memory) WebhookTokenHash(_ context.Context, extension string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return "", m.Fail
	}
	hash, ok := m.hooks[extension]
	if !ok {
		return "", ErrNotFound
	}
	return hash, nil
}

func (m *Memory) SetWebhookTokenHash(_ context.Context, extension, hash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	m.hooks[extension] = hash
	return nil
}

func (m *Memory) DeleteWebhookToken(_ context.Context, extension string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	delete(m.hooks, extension)
	return nil
}

func (m *Memory) RecordViolation(_ context.Context, violation Violation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	m.violations = append(m.violations, violation)
	return nil
}

func (m *Memory) Violations(_ context.Context, extension string, limit int) ([]Violation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return nil, m.Fail
	}
	out := []Violation{}
	for i := len(m.violations) - 1; i >= 0 && len(out) < limit; i-- {
		if m.violations[i].Extension == extension {
			out = append(out, m.violations[i])
		}
	}
	return out, nil
}

func (m *Memory) ClearViolations(_ context.Context, extension string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	m.violations = slices.DeleteFunc(m.violations, func(v Violation) bool { return v.Extension == extension })
	return nil
}

func (m *Memory) RemovedExtensions(context.Context) ([]RemovedExtension, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return nil, m.Fail
	}
	return slices.Clone(m.removedExt), nil
}

func (m *Memory) AddRemovedExtension(_ context.Context, e RemovedExtension) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	m.removedExt = append(slices.DeleteFunc(m.removedExt, func(have RemovedExtension) bool { return have.Source == e.Source }), e)
	slices.SortFunc(m.removedExt, func(a, b RemovedExtension) int { return strings.Compare(a.Source, b.Source) })
	return nil
}

func (m *Memory) DeleteRemovedExtension(_ context.Context, source string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	m.removedExt = slices.DeleteFunc(m.removedExt, func(e RemovedExtension) bool { return e.Source == source })
	return nil
}

func (m *Memory) RecordActivity(_ context.Context, a Activity) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	for n, have := range m.activity {
		if have.Extension == a.Extension && have.Kind == a.Kind && have.Destination == a.Destination {
			m.activity[n].Count += a.Count
			m.activity[n].Last = a.Last
			return nil
		}
	}
	m.activity = append(m.activity, a)
	return nil
}

func (m *Memory) Activity(_ context.Context, extension string, limit int) ([]Activity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return nil, m.Fail
	}
	out := []Activity{}
	for _, a := range m.activity {
		if a.Extension == extension {
			out = append(out, a)
		}
	}
	slices.SortStableFunc(out, func(a, b Activity) int { return b.Last.Compare(a.Last) })
	return out[:min(limit, len(out))], nil
}

func (m *Memory) DeleteActivity(_ context.Context, extension string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	m.activity = slices.DeleteFunc(m.activity, func(a Activity) bool { return a.Extension == extension })
	return nil
}

func (m *Memory) RemovedDevices(context.Context) ([]RemovedDevice, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return nil, m.Fail
	}
	return slices.Clone(m.removed), nil
}

func (m *Memory) AddRemovedDevice(_ context.Context, device RemovedDevice) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	m.removed = slices.DeleteFunc(m.removed, func(d RemovedDevice) bool { return d.ID == device.ID })
	m.removed = append(m.removed, device)
	slices.SortFunc(m.removed, func(a, b RemovedDevice) int { return strings.Compare(a.ID, b.ID) })
	return nil
}

func (m *Memory) DeleteRemovedDevice(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	m.removed = slices.DeleteFunc(m.removed, func(d RemovedDevice) bool { return d.ID == id })
	return nil
}

func (m *Memory) ExtensionStorage(_ context.Context, extension string) (map[string]json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return nil, m.Fail
	}
	out := map[string]json.RawMessage{}
	maps.Copy(out, m.storage[extension])
	return out, nil
}

func (m *Memory) SetExtensionValue(_ context.Context, extension, key string, value json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	if m.storage[extension] == nil {
		m.storage[extension] = map[string]json.RawMessage{}
	}
	m.storage[extension][key] = slices.Clone(value)
	return nil
}

func (m *Memory) DeleteExtensionValue(_ context.Context, extension, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	delete(m.storage[extension], key)
	return nil
}

func (m *Memory) DeleteExtensionStorage(_ context.Context, extension string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	delete(m.storage, extension)
	return nil
}

func (m *Memory) InstallKey(_ context.Context, candidate []byte) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return nil, m.Fail
	}
	if m.installKey == nil {
		m.installKey = slices.Clone(candidate)
	}
	return slices.Clone(m.installKey), nil
}

func (m *Memory) MarketState(context.Context) (MarketState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return MarketState{}, m.Fail
	}
	return m.marketState, nil
}

func (m *Memory) SaveMarketState(_ context.Context, state MarketState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	m.marketState = state
	return nil
}

func (m *Memory) SavePackage(_ context.Context, key string, archive []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	m.packages[key] = slices.Clone(archive)
	return nil
}

func (m *Memory) Package(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return nil, m.Fail
	}
	archive, ok := m.packages[key]
	if !ok {
		return nil, ErrNotFound
	}
	return slices.Clone(archive), nil
}

func (m *Memory) DeletePackage(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	delete(m.packages, key)
	return nil
}

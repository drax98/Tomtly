// Package extension runs third-party code that adds devices or data, pulled from GitHub or a local folder.
//
// An extension is TypeScript in its own VM on its own goroutine. It can only reach the network hosts it declares and
// the settings it's given, and a failure stays on its card on the page rather than affecting the rest of the home.
package extension

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"tomtly/device"
	"tomtly/internal/store"
	"tomtly/plugin"
)

const retryEvery = 5 * time.Minute

// SettingStatus describes a setting on the page. It says whether a value is set, and what it is unless it's a secret.
type SettingStatus struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
	Help     string `json:"help,omitempty"`
	Default  any    `json:"default,omitempty"`
	Set      bool   `json:"set"`
	// Value is what a setting that isn't a secret is set to, so the page can offer it for editing. Never a secret.
	Value any `json:"value,omitempty"`
}

// Status is what the page shows about an extension.
type Status struct {
	Source      string   `json:"source"`
	ID          string   `json:"id,omitempty"`
	Name        string   `json:"name,omitempty"`
	Version     string   `json:"version,omitempty"`
	Description string   `json:"description,omitempty"`
	Homepage    string   `json:"homepage,omitempty"`
	License     string   `json:"license,omitempty"`
	Categories  []string `json:"categories,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Network     []string `json:"network"`
	Access      []string `json:"access"`
	// NeedsApproval means it asks for access that isn't granted yet, or was stopped for breaking its access.
	NeedsApproval bool     `json:"needsApproval"`
	Warnings      []string `json:"warnings"`
	// Risks says what approving could cost, whatever the code looks like. Warnings are what the code scan found.
	Risks      []string          `json:"risks,omitempty"`
	Violations []store.Violation `json:"violations"`
	// Activity is where its allowed traffic went, most recent first.
	Activity []store.Activity `json:"activity,omitempty"`
	Settings []SettingStatus  `json:"settings"`
	Provides []string         `json:"provides"`
	// Widgets are what it offers for Home.
	Widgets   []WidgetInfo `json:"widgets,omitempty"`
	Running   bool         `json:"running"`
	Removable bool         `json:"removable"`
	Error     string       `json:"error,omitempty"`
	// Webhook is the path of a new webhook address. It's set only in the answer to the call that made it, since the
	// service keeps just its hash.
	Webhook string `json:"webhook,omitempty"`
	// Upgrade is what moving to this version would ask of the person. It's set when something inspected has the id of an
	// extension added on the page.
	Upgrade *Upgrade `json:"upgrade,omitempty"`
	// Revoked says the marketplace took this version back. It keeps running until the owner decides otherwise.
	Revoked *MarketRevocation `json:"revoked,omitempty"`
	// MarketUpdate is a newer version of this marketplace copy, from the last catalog the home checked.
	MarketUpdate *MarketUpdate `json:"marketUpdate,omitempty"`
}

// Manager runs the extensions in config.json. It is one adapter to the rest of the service.
type Manager struct {
	configDir, cacheDir string // cacheDir holds unpacked code, which can always be recreated from the store
	store               store.Store
	ctx                 context.Context
	plugins             *plugin.Hub
	archives            string
	market              *marketplace // nil when not set up
	allowLoopback       bool         // tests only
	own                 *ownNetwork
	returning           atomic.Bool   // memory is being handed back to the system
	lastHeap            atomic.Uint64 // the watch's latest sample, so a call can note it without stopping the world

	mu         sync.RWMutex
	instances  []*instance
	configured []entry // what config.json lists, including any the owner took out
	ids        map[string]bool

	removedMu sync.RWMutex // not m.mu: Devices holds that one and asks about each device
	removed   map[string]store.RemovedDevice

	hookRate   hookLimit
	badHooks   hookLimit
	hookHashes sync.Map // extension id to its token's hash, so a flood of wrong tokens never reaches the store
	active     activeCalls
}

type entry struct {
	Source   string         `json:"source"`
	Settings map[string]any `json:"settings"`
}

// Start begins running the extensions. A bad entry in config.json is an error, but an extension that fails to download
// or load only shows on the page: tomtly has to work with no internet and with broken third-party code.
func Start(ctx context.Context, entries []json.RawMessage, configDir, cacheDir string, st store.Store, plugins *plugin.Hub) (*Manager, error) {
	m := newManager(configDir, cacheDir, st, plugins)
	m.ctx = ctx
	m.own = findOwnNetwork(os.Getenv("DATABASE_URL"))
	loading, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	removed, err := st.RemovedExtensions(loading)
	if err != nil {
		return nil, fmt.Errorf("removed extensions: %w", err) // starting anyway would bring back what the owner took out
	}
	for i, raw := range entries {
		var e entry
		if err := plugin.Decode(raw, &e); err != nil {
			return nil, fmt.Errorf("extension %d: %w", i+1, err)
		}
		if e.Source == "" {
			return nil, fmt.Errorf(`extension %d: needs a "source"`, i+1)
		}
		m.configured = append(m.configured, e)
		if !slices.ContainsFunc(removed, func(r store.RemovedExtension) bool { return r.Source == e.Source }) {
			m.add(ctx, e, false)
		}
	}
	m.loadAdded(ctx)
	go m.watchMemory(ctx, heapLimit(), memoryEvery)
	return m, nil
}

func newManager(configDir, cacheDir string, st store.Store, plugins *plugin.Hub) *Manager {
	return &Manager{configDir: configDir, cacheDir: cacheDir, store: st, plugins: plugins, archives: githubArchives, ids: map[string]bool{}, removed: map[string]store.RemovedDevice{}}
}

func (m *Manager) add(ctx context.Context, e entry, removable bool) *instance {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.addLocked(ctx, e, removable)
}

func (m *Manager) addLocked(ctx context.Context, e entry, removable bool) *instance {
	inst := m.launch(ctx, e, removable)
	m.instances = append(m.instances, inst)
	return inst
}

// launch starts an instance running. The caller puts it in m.instances.
func (m *Manager) launch(ctx context.Context, e entry, removable bool) *instance {
	ctx, cancel := context.WithCancel(ctx)
	inst := &instance{manager: m, entry: e, removable: removable, cancel: cancel, done: make(chan struct{}), writes: make(chan pendingWrite, 16), actions: make(chan pendingAction, 16),
		approve: make(chan struct{}, 1), hooks: make(chan pendingHook, 8), finds: make(chan pendingFind),
		status: Status{Source: e.Source, Network: []string{}, Access: []string{}, Warnings: []string{}, Violations: []store.Violation{}, Settings: []SettingStatus{}, Provides: []string{}, Removable: removable}}
	go func() {
		defer close(inst.done)
		inst.run(ctx)
	}()
	return inst
}

func (m *Manager) claim(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ids[id] {
		return m.idClash(id)
	}
	m.ids[id] = true
	return nil
}

// idClash says what's in the way and what to do about it. The caller holds m.mu.
func (m *Manager) idClash(id string) error {
	for _, inst := range m.instances {
		s := inst.currentStatus()
		if s.ID != id {
			continue
		}
		if !s.Removable {
			return fmt.Errorf("%s is already installed, from %s in config.json. Remove that entry and restart to install this one instead", s.Name, s.Source)
		}
		where := s.Source
		if strings.HasPrefix(where, "upload:") {
			where = "an uploaded file"
		} else if strings.HasPrefix(where, "market:") {
			where = "the marketplace"
		}
		return fmt.Errorf("%s is already installed, from %s. Remove it first to install this one instead", s.Name, where)
	}
	return fmt.Errorf("another extension already uses the id %q", id)
}

func (m *Manager) release(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.ids, id)
}

// Statuses lists the extensions in the order of config.json.
func (m *Manager) Statuses() []Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Status, 0, len(m.instances))
	for _, inst := range m.instances {
		out = append(out, m.withMarketUpdate(m.withRevocation(m.withViolations(inst.currentStatus()))))
	}
	return out
}

// withViolations adds what an extension was refused, newest first, and where it did connect. An unreadable store shows none.
func (m *Manager) withViolations(s Status) Status {
	if s.ID == "" {
		return s
	}
	ctx, cancel := context.WithTimeout(context.Background(), storeTimeout)
	defer cancel()
	if v, err := m.store.Violations(ctx, s.ID, violationsShown); err == nil && v != nil {
		s.Violations = v
	}
	if a, err := m.store.Activity(ctx, s.ID, activityShown); err == nil {
		s.Activity = a
	}
	return s
}

// Declared lists the extensions whose types.d.ts already describes their data in PluginMap.
func (m *Manager) Declared() map[string]bool {
	out := map[string]bool{}
	for path, source := range m.Types() {
		if strings.Contains(source, "interface PluginMap") {
			out[strings.Split(path, "/")[1]] = true
		}
	}
	return out
}

// Types returns each loaded extension's types.d.ts, so scripts get autocomplete for its data.
func (m *Manager) Types() map[string]string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := map[string]string{}
	for _, inst := range m.instances {
		id, dir := inst.identity()
		if id == "" {
			continue
		}
		path := filepath.Join(dir, typesFile)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
			continue // a link could expose another file
		}
		if data, err := os.ReadFile(path); err == nil {
			out["extensions/"+id+"/"+typesFile] = string(data)
		}
	}
	return out
}

// Devices implements device.Adapter.
func (m *Manager) Devices() []device.Device {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := []device.Device{}
	for _, inst := range m.instances {
		out = append(out, inst.currentDevices()...)
	}
	return out
}

// Write implements device.Adapter. It validates and queues, and the extension's goroutine sends it.
func (m *Manager) Write(id, dp string, value any) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, inst := range m.instances {
		if d, ok := inst.findDevice(id); ok {
			return inst.queueWrite(d, dp, value)
		}
	}
	return errors.New("unknown device " + id)
}

// startGuarded keeps a panic in one extension from taking the service down. Hostile code can cause one by breaking what
// the host relies on, such as with JSON = undefined. The message stays out of the page's view, since it may hold anything.
func (i *instance) startGuarded(ctx context.Context) (err error) {
	defer func() {
		if r := recover(); r != nil {
			id, _ := i.identity()
			slog.Error("extension crashed", "extension", id, "panic", i.scrub(fmt.Sprint(r)))
			err = errors.New("this extension crashed")
		}
	}()
	return i.start(ctx)
}

// run takes an extension from its source to running. Trouble getting there is retried, since it's often temporary.
func (i *instance) run(ctx context.Context) {
	for {
		err := i.startGuarded(ctx)
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, errRestart) {
			continue
		}
		i.fail(err)
		slog.Warn("extension failed", "source", i.entry.Source, "err", i.scrub(plainError(err)))
		select {
		case <-ctx.Done():
			return
		case <-time.After(retryEvery):
		}
	}
}

func sorted[V any](items map[string]V) []string {
	keys := make([]string, 0, len(items))
	for k := range items {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

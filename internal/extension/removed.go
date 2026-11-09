package extension

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/dop251/goja"

	"home-control/internal/store"
)

const (
	findTimeout = 30 * time.Second
	findWait    = findTimeout + 5*time.Second // it may first wait for a call already running, which usually ends well before
	// A scan spends about 32 operations of the 600 an extension gets in a minute, and going over stops the extension, so
	// reopening the page again and again must not start another.
	findReuse  = 30 * time.Second
	maxFound   = 1000
	maxFoundID = 200
)

// ErrUnknownDevice means nothing in the system has that device or integration.
var ErrUnknownDevice = errors.New("unknown device")

// What a found device is to the system.
const (
	FoundAdded   = "added"   // it's in the system
	FoundRemoved = "removed" // the owner removed it, and can bring it back
	FoundNew     = "new"     // it isn't in the system, and FoundDevice.Note says what that would take
)

// FoundDevice is a device an extension found on the network, or one the owner removed.
type FoundDevice struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Product string `json:"product,omitempty"`
	Address string `json:"address,omitempty"`
	Note    string `json:"note,omitempty"`
	State   string `json:"state"`
	// Seen says whether the scan found it. A removed device can be brought back either way.
	Seen bool `json:"seen"`
}

// Discovery is what the integration's page shows when it's opened.
type Discovery struct {
	// Supported says whether the extension can look on the network at all.
	Supported bool `json:"supported"`
	// Error is why the scan didn't work. Removed devices are still listed.
	Error   string        `json:"error,omitempty"`
	Devices []FoundDevice `json:"devices"`
}

// foundDevice is what devices.discover returns.
type foundDevice struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Product string `json:"product"`
	Address string `json:"address"`
	Note    string `json:"note"`
}

type findResult struct {
	found     []foundDevice
	supported bool
	err       error
}

type pendingFind struct{ done chan findResult }

func (m *Manager) isRemoved(id string) bool {
	m.removedMu.RLock()
	defer m.removedMu.RUnlock()
	_, ok := m.removed[id]
	return ok
}

// removedIDs lists an extension's removed devices by the ids it knows them by.
func (m *Manager) removedIDs(extension string) []string {
	out := []string{}
	for _, d := range m.removedOf(extension) {
		out = append(out, strings.TrimPrefix(d.ID, extension+":"))
	}
	return out
}

func (m *Manager) removedOf(extension string) []store.RemovedDevice {
	m.removedMu.RLock()
	defer m.removedMu.RUnlock()
	out := []store.RemovedDevice{}
	for _, d := range m.removed {
		if d.Extension == extension {
			out = append(out, d)
		}
	}
	slices.SortFunc(out, func(a, b store.RemovedDevice) int { return strings.Compare(a.ID, b.ID) })
	return out
}

func (m *Manager) loadRemoved(ctx context.Context) error {
	m.removedMu.Lock() // held over the read, so a removal can't be lost to an older answer
	defer m.removedMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	list, err := m.store.RemovedDevices(ctx)
	if err != nil {
		return fmt.Errorf("reading the removed devices: %w", err)
	}
	clear(m.removed)
	for _, d := range list {
		m.removed[d.ID] = d
	}
	return nil
}

// RemoveDevice takes a device out of the system: pages, scripts and commands all stop seeing it, until RestoreDevice.
// The record comes first, so a device that shows as removed really is, after a restart too.
func (m *Manager) RemoveDevice(ctx context.Context, id string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, inst := range m.instances {
		d, ok := inst.findDevice(id)
		if !ok {
			continue
		}
		record := store.RemovedDevice{ID: id, Extension: d.Adapter, Name: d.Name, Product: d.Product}
		if err := m.store.AddRemovedDevice(ctx, record); err != nil {
			return err
		}
		m.removedMu.Lock()
		m.removed[id] = record
		m.removedMu.Unlock()
		return nil
	}
	return ErrUnknownDevice
}

// RestoreDevice brings a removed device back. What it reports shows from the extension's next poll.
func (m *Manager) RestoreDevice(ctx context.Context, id string) error {
	if !m.isRemoved(id) {
		return ErrUnknownDevice
	}
	if err := m.store.DeleteRemovedDevice(ctx, id); err != nil {
		return err
	}
	m.removedMu.Lock()
	delete(m.removed, id)
	m.removedMu.Unlock()
	return nil
}

// Discover asks an extension what's on the network, and adds what the owner removed. A scan that can't run only says so,
// because the removed devices can be brought back regardless.
func (m *Manager) Discover(ctx context.Context, id string) (Discovery, error) {
	m.mu.RLock()
	var inst *instance
	for _, candidate := range m.instances {
		if own, _ := candidate.identity(); own == id && id != "" {
			inst = candidate
		}
	}
	m.mu.RUnlock()
	if inst == nil {
		return Discovery{}, ErrUnknownDevice
	}
	out := Discovery{Devices: []FoundDevice{}}
	result := inst.scanNetwork(ctx)
	out.Supported = result.supported
	if result.err != nil {
		out.Error = inst.scrub(plainError(result.err))
	}
	seen := map[string]bool{}
	for _, f := range result.found {
		full := id + ":" + f.ID
		state := FoundNew
		if m.isRemoved(full) {
			state = FoundRemoved
		} else if _, ok := inst.findDevice(full); ok {
			state = FoundAdded
		}
		seen[full] = true
		out.Devices = append(out.Devices, FoundDevice{full, f.Name, f.Product, f.Address, f.Note, state, true})
	}
	for _, d := range m.removedOf(id) {
		if !seen[d.ID] {
			out.Devices = append(out.Devices, FoundDevice{ID: d.ID, Name: d.Name, Product: d.Product, State: FoundRemoved})
		}
	}
	return out, nil
}

// scanNetwork runs the extension's discover on its own thread, like everything else it does.
func (i *instance) scanNetwork(ctx context.Context) findResult {
	status := i.currentStatus()
	if !status.Running || status.NeedsApproval {
		return findResult{err: errors.New("this integration isn't running")}
	}
	i.findMu.Lock()
	defer i.findMu.Unlock()
	if !i.foundAt.IsZero() && time.Since(i.foundAt) < findReuse {
		return findResult{found: i.found, supported: true}
	}
	job := pendingFind{done: make(chan findResult, 1)}
	timer := time.NewTimer(findWait)
	defer timer.Stop()
	select {
	case i.finds <- job:
	case <-ctx.Done():
		return findResult{err: errors.New("the scan was cancelled")}
	case <-timer.C:
		return findResult{err: errors.New("this integration is busy, try again in a moment")}
	}
	select {
	case result := <-job.done:
		if result.err == nil && result.supported {
			i.found, i.foundAt = result.found, time.Now()
		}
		return result
	case <-ctx.Done():
		return findResult{err: errors.New("the scan was cancelled")}
	case <-timer.C:
		return findResult{err: errors.New("the scan took too long")}
	}
}

// find runs on the extension's goroutine.
func (i *instance) find(l *loaded, api goja.Value) findResult {
	if l.discover == nil {
		return findResult{}
	}
	value, err := i.callWithin(findTimeout, l, l.discover, api)
	if err != nil {
		return findResult{supported: true, err: err}
	}
	text, err := i.output(l, value) // the same check as a poll: nothing that holds a secret reaches the page
	if err != nil {
		return findResult{supported: true, err: err}
	}
	found, err := convertFound(text)
	return findResult{found: found, supported: true, err: err}
}

func convertFound(text string) ([]foundDevice, error) {
	var found []foundDevice
	dec := json.NewDecoder(strings.NewReader(text))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&found); err != nil {
		return nil, fmt.Errorf("devices.discover must return a list of devices: %w", err)
	}
	if len(found) > maxFound {
		return nil, fmt.Errorf("devices.discover returned over %d devices", maxFound)
	}
	seen := map[string]bool{}
	out := make([]foundDevice, 0, len(found))
	for _, f := range found {
		if f.ID == "" || len(f.ID) > maxFoundID {
			return nil, errors.New("devices.discover: every device needs a short id")
		}
		if seen[f.ID] {
			continue // a device can answer on two addresses
		}
		seen[f.ID] = true
		if f.Name == "" {
			f.Name = f.ID
		}
		out = append(out, f)
	}
	return out, nil
}

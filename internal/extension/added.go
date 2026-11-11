package extension

import (
	"context"
	"errors"
	"fmt"
	"github.com/dop251/goja"
	"log/slog"
	"slices"
	"strings"
	"time"
	"tomtly/internal/store"
)

const (
	stopWait     = 5 * time.Second
	storeTimeout = 5 * time.Second
)

// loadAdded starts what was added on the page. An unreadable store must not stop the home.
func (m *Manager) loadAdded(ctx context.Context) {
	stored, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	entries, err := m.store.AddedExtensions(stored)
	if err != nil {
		slog.Warn("extensions added on the page could not be read", "err", err)
		return
	}
	for _, e := range entries {
		m.add(ctx, entry{Source: e.Source, Settings: e.Settings}, true)
	}
}

// checkPageSource limits what the page can ask for, which is less than config.json may: any folder on this machine
// could be read as an extension otherwise.
func checkPageSource(source string) error {
	if strings.HasPrefix(source, "upload:") {
		if !uploadSource.MatchString(source) {
			return fmt.Errorf("%q is not an uploaded file", source)
		}
		return nil
	}
	if strings.HasPrefix(source, "market:") {
		if !marketSource.MatchString(source) {
			return fmt.Errorf("%q is not a marketplace package", source)
		}
		return nil
	}
	if strings.HasPrefix(source, "github:") {
		if !githubSource.MatchString(source) {
			return fmt.Errorf("%q must look like github:owner/repo@ref or github:owner/repo/folder@ref", source)
		}
		return nil
	}
	if !strings.HasPrefix(source, "./") || strings.Contains(source, "..") {
		return fmt.Errorf("%q must be github:owner/repo@ref, or ./folder inside your config folder", source)
	}
	return nil
}

// Inspect fetches an extension and says what it is and needs, without running anything but its definition.
func (m *Manager) Inspect(ctx context.Context, source string) (Status, error) {
	status, _, err := m.inspect(ctx, source)
	return status, err
}

// inspect also returns the access as declared, with no addresses filled in, which is what a marketplace listing holds.
func (m *Manager) inspect(ctx context.Context, source string) (Status, []string, error) {
	l, dir, err := m.fetch(ctx, source)
	if err != nil {
		return Status{}, nil, errors.New(plainError(err))
	}
	status := definedStatus(source, l, nil)
	declared := status.Access
	if inst := m.installedAs(l.ID, source); inst != nil {
		plan, carried, err := planFor(inst, l, dir)
		if err != nil {
			return Status{}, nil, err
		}
		status.Upgrade = &plan
		status.Access = l.accessLabels(carried) // the update dialog compares these with what's running
	}
	return status, declared, nil
}

func (m *Manager) fetch(ctx context.Context, source string) (*loaded, string, error) {
	if err := checkPageSource(source); err != nil {
		return nil, "", err
	}
	dir, err := m.resolve(ctx, source)
	if err != nil {
		return nil, "", err
	}
	l, err := loadWatched(dir, func(vm *goja.Runtime) func() { return m.track(vm, nil) }) // the watchdog can stop a bomb in its top-level code
	if err == nil {
		err = checkMarket(source, l)
	}
	return l, dir, err
}

// Add starts an extension from the page, and it comes back after a restart. Blank optional settings count as unset.
// seen is the access the person was shown. A definition is code and can answer differently each time it runs, so what is
// granted has to be what was shown, and from the marketplace what was signed as well.
func (m *Manager) Add(ctx context.Context, source string, settings map[string]any, seen []string) (Status, error) {
	offered, err := m.marketOffer(ctx, source)
	if err != nil {
		return Status{}, err
	}
	l, _, err := m.fetch(ctx, source)
	if err != nil {
		return Status{}, errors.New(plainError(err))
	}
	labels := l.accessLabels(nil)
	if !sameAccess(seen, labels) {
		return Status{}, errAccessChanged
	}
	if offered != nil && !sameAccess(offered.Access, labels) {
		return Status{}, errors.New("the access the marketplace listed isn't what the code really asks for, so it was refused")
	}
	for key, value := range settings {
		if value == "" {
			delete(settings, key)
		}
	}
	if _, err := resolveSettings(l.Settings, settings); err != nil {
		return Status{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, inst := range m.instances {
		if inst.entry.Source == source {
			return Status{}, errors.New("this source is already added")
		}
	}
	if m.ids[l.ID] {
		return Status{}, m.idClash(l.ID)
	}
	stored, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	if err := m.store.SaveAddedExtension(stored, store.AddedExtension{Source: source, Settings: settings}); err != nil {
		return Status{}, fmt.Errorf("could not save it: %w", err)
	}
	// Adding it on the page is the owner's approval of what it asks for.
	if err := m.store.SetGrants(stored, l.ID, l.accessKeys(settings)); err != nil {
		return Status{}, fmt.Errorf("could not save its access: %w", err)
	}
	var webhook string
	if l.access.Webhooks {
		var err error
		if webhook, err = m.newWebhookToken(stored, l.ID); err != nil {
			return Status{}, fmt.Errorf("could not make its webhook address: %w", err)
		}
	}
	m.addLocked(m.ctx, entry{Source: source, Settings: settings}, true)
	status := definedStatus(source, l, settings)
	status.Webhook = webhook
	return status, nil
}

// Remove stops an extension and forgets its approvals and saved data. One added on the page goes with its settings. One
// in config.json is only remembered as removed, since that file is the owner's, and RestoreExtension brings it back.
func (m *Manager) Remove(source string) error {
	m.mu.Lock()
	var removed *instance
	for i, inst := range m.instances {
		if inst.entry.Source != source {
			continue
		}
		if !inst.removable {
			s := inst.currentStatus()
			kind, name := "plugin", s.Name
			if slices.Contains(s.Provides, "devices") {
				kind = "integration"
			}
			if name == "" {
				name = source
			}
			saving, cancel := context.WithTimeout(context.Background(), storeTimeout)
			err := m.store.AddRemovedExtension(saving, store.RemovedExtension{Source: source, ID: s.ID, Name: name, Kind: kind})
			cancel()
			if err != nil { // saved first: otherwise a restart would bring it back
				m.mu.Unlock()
				return fmt.Errorf("could not save that: %w", err)
			}
		}
		removed = inst
		m.instances = append(m.instances[:i:i], m.instances[i+1:]...)
		break
	}
	if removed == nil {
		m.mu.Unlock()
		return errors.New("no extension from that source")
	}
	m.mu.Unlock()
	removed.stop()
	ctx, cancel := context.WithTimeout(context.Background(), storeTimeout)
	defer cancel()
	var err error
	if removed.removable {
		err = m.store.DeleteAddedExtension(ctx, source)
	}
	if id, _ := removed.identity(); id != "" {
		m.hookHashes.Delete(id)
		err = errors.Join(err, m.store.DeleteExtensionStorage(ctx, id), m.store.DeleteWebhookToken(ctx, id), m.store.DeleteActivity(ctx, id), m.store.SetGrants(ctx, id, nil)) // tokens and approvals shouldn't outlive the extension
	}
	// shortcut: removed devices stay recorded, so installing it again keeps the owner's choice; add a cleanup if that surprises anyone.
	if removed.removable {
		err = errors.Join(err, m.dropUnused(source))
	}
	return err
}

// RemovedExtensions lists what the owner took out of config.json's list and can add back. One no longer in the file is gone.
func (m *Manager) RemovedExtensions(ctx context.Context) ([]store.RemovedExtension, error) {
	all, err := m.store.RemovedExtensions(ctx)
	if err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return slices.DeleteFunc(all, func(r store.RemovedExtension) bool {
		return !slices.ContainsFunc(m.configured, func(e entry) bool { return e.Source == r.Source })
	}), nil
}

// RestoreExtension starts one from config.json again. Its approvals went when it was removed, so it waits for the owner.
func (m *Manager) RestoreExtension(ctx context.Context, source string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	at := slices.IndexFunc(m.configured, func(e entry) bool { return e.Source == source })
	if at < 0 {
		return errors.New("that isn't listed in config.json any more")
	}
	if slices.ContainsFunc(m.instances, func(inst *instance) bool { return inst.entry.Source == source }) {
		return errors.New("that is already running")
	}
	if err := m.store.DeleteRemovedExtension(ctx, source); err != nil {
		return fmt.Errorf("could not save that: %w", err)
	}
	m.addLocked(m.ctx, m.configured[at], false)
	return nil
}

func (i *instance) stop() {
	i.cancel()
	select {
	case <-i.done:
	case <-time.After(stopWait):
		slog.Warn("extension is slow to stop", "source", i.entry.Source)
	}
}

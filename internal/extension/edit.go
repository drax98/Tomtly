package extension

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"tomtly/internal/store"
)

const editConfigFile = "This one is listed in config.json, so edit the file."

// pageInstance finds an extension added on the page, which is the only kind the page may change.
func (m *Manager) pageInstance(source string) (*instance, error) {
	inst := m.bySource(source)
	switch {
	case inst == nil:
		return nil, errors.New("no extension from that source")
	case !inst.removable:
		return nil, errors.New(editConfigFile)
	}
	return inst, nil
}

// swap stops an instance and starts one in its place, so the order on the page stays.
func (m *Manager) swap(old *instance, e entry) error {
	old.stop()
	m.mu.Lock()
	defer m.mu.Unlock()
	at := slices.Index(m.instances, old)
	if at < 0 {
		return errors.New("it was removed while this was happening")
	}
	m.instances[at] = m.launch(m.ctx, e, true)
	return nil
}

// UpdateSettings changes the settings of an extension added on the page, and restarts it with them. A secret that is
// left out or blank keeps the value it has, so the page never needs to know it. Other settings that are left out keep
// theirs too, and a blank one is unset.
func (m *Manager) UpdateSettings(ctx context.Context, source string, given map[string]any) (Status, error) {
	inst, err := m.pageInstance(source)
	if err != nil {
		return Status{}, err
	}
	l, _, err := m.fetch(ctx, source)
	if err != nil {
		return Status{}, errors.New(plainError(err))
	}
	merged := maps.Clone(inst.entry.Settings)
	if merged == nil {
		merged = map[string]any{}
	}
	for key, value := range given {
		if value == nil || value == "" {
			if l.Settings[key].Type != "secret" {
				delete(merged, key)
			}
			continue
		}
		merged[key] = value
	}
	if _, err := resolveSettings(l.Settings, merged); err != nil {
		return Status{}, err
	}
	stored, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	if err := m.store.SaveAddedExtension(stored, store.AddedExtension{Source: source, Settings: merged}); err != nil {
		return Status{}, fmt.Errorf("could not save it: %w", err)
	}
	if err := m.approveTypedHosts(stored, inst, l, inst.entry.Settings, merged, given); err != nil {
		return Status{}, fmt.Errorf("could not save its access: %w", err)
	}
	if err := m.swap(inst, entry{Source: source, Settings: merged}); err != nil {
		return Status{}, err
	}
	return definedStatus(source, l, merged), nil
}

// Replace swaps an extension added on the page for a new version of it, keeping its saved data and, as its migrations
// say, its settings. given holds what the person typed for the settings the update asks for. It must be the same
// extension, by id. What the new version asks for beyond what was approved waits for approval, as always. That includes
// an address that changed: a migration or the update's form is the author's wording, unlike the settings page. seen is the
// access the person was shown for the new version. One from the marketplace has to be on offer now, and not an older one.
func (m *Manager) Replace(ctx context.Context, source, newSource string, given map[string]any, seen []string) (Status, error) {
	inst, err := m.pageInstance(source)
	if err != nil {
		return Status{}, err
	}
	// An upload that can't be used is forgotten, as when it fails to check.
	refuse := func(err error) (Status, error) {
		m.dropUnused(newSource)
		return Status{}, err
	}
	id, _ := inst.identity()
	switch {
	case id == "":
		return refuse(errors.New("this one hasn't loaded, so there is nothing to carry over. Remove it and add the new version"))
	case newSource == source:
		return refuse(errors.New("that is the version already running"))
	case m.bySource(newSource) != nil:
		return refuse(errors.New("that version is already added"))
	}
	if _, err := m.marketOffer(ctx, newSource); err != nil {
		return refuse(err)
	}
	l, dir, err := m.fetch(ctx, newSource)
	if err != nil {
		return refuse(errors.New(plainError(err)))
	}
	if l.ID != id {
		return refuse(fmt.Errorf("that is %q, not %q. A different id is a different extension, so add it instead", l.ID, id))
	}
	if originOf(newSource) != originOf(source) {
		return refuse(fmt.Errorf("that comes from %s, and the one running comes from %s. Anyone can give their code the same id, so an update can't change where it comes from: it would get your keys and saved data. Remove this one and add the new one instead", originOf(newSource), originOf(source)))
	}
	if have := inst.currentStatus().Version; marketSource.MatchString(newSource) && newerVersion(have, l.Version) {
		return refuse(fmt.Errorf("that is version %s, older than the %s running. The marketplace doesn't go back a version", l.Version, have))
	}
	plan, settings, err := planFor(inst, l, dir)
	if err != nil {
		return refuse(err)
	}
	if !sameAccess(seen, l.accessLabels(settings)) {
		return refuse(errAccessChanged)
	}
	// Not refuse: the download has to stay, so the person can correct what they typed and press Update again.
	// shortcut: a download nobody goes on with stays stored, as a cancelled one already does; sweep unused packages if they pile up.
	if err := applyAnswers(plan, l.Settings, settings, given); err != nil {
		return Status{}, err
	}
	stored, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	if err := m.store.SaveAddedExtension(stored, store.AddedExtension{Source: newSource, Settings: settings}); err != nil {
		return refuse(fmt.Errorf("could not save it: %w", err))
	}
	if err := m.store.DeleteAddedExtension(stored, source); err != nil {
		m.store.DeleteAddedExtension(stored, newSource)
		return refuse(fmt.Errorf("could not save it: %w", err))
	}
	if err := m.swap(inst, entry{Source: newSource, Settings: settings}); err != nil {
		return Status{}, err
	}
	m.dropUnused(source)
	return definedStatus(newSource, l, settings), nil
}

// originOf is who a source belongs to. shortcut: uploads and local folders have no author to compare, so they count as the owner's own choice of file; sign packages if that isn't enough.
func originOf(source string) string {
	if strings.HasPrefix(source, "github:") || strings.HasPrefix(source, "market:") {
		name, _, _ := strings.Cut(source, "@")
		return name
	}
	return "your own files"
}

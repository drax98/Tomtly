package extension

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"tomtly/internal/store"
)

const (
	// marketPollEvery is how soon an installed copy hears that it was taken back.
	marketPollEvery = time.Hour
	stoppedByOwner  = "owner"
	maxReasonShown  = 500
)

// MarketRevocation is a version the marketplace took back, in the part of the catalog its web key signs. It can only raise a
// warning, since a stranger who gets hold of that key mustn't be able to switch off a home.
type MarketRevocation struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	State   string `json:"state"` // withdrawn or suspended
	Reason  string `json:"reason"`
}

func clipText(s string, limit int) string { return capRunes(s, limit) }

func suspendedError(reason string) error {
	return fmt.Errorf("the marketplace has suspended this, so it can't be added right now: %s", clipText(reason, maxReasonShown))
}

// load reads what this home last saw. A home that can't remember has no way to spot an old list, so it refuses. The caller holds mu.
func (mp *marketplace) load(ctx context.Context) error {
	if mp.loaded {
		return nil
	}
	reading, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	state, err := mp.store.MarketState(reading)
	if err != nil {
		return fmt.Errorf("%w: what this home last saw can't be read", errUnverified)
	}
	if len(state.Revoked) > 0 && json.Unmarshal(state.Revoked, &mp.revoked) != nil {
		mp.revoked = nil
	}
	mp.newest, mp.newestSigned, mp.loaded = state.Newest, state.NewestSigned, true
	return nil
}

// remember keeps the newest list's dates and what it took back, so a restart can't be used to replay an old one. The caller holds mu.
func (mp *marketplace) remember(ctx context.Context, newest, signed time.Time, revoked []MarketRevocation) error {
	if newest.Equal(mp.newest) && signed.Equal(mp.newestSigned) && slices.Equal(revoked, mp.revoked) {
		return nil
	}
	data, err := json.Marshal(revoked)
	if err != nil {
		return err
	}
	saving, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	if err := mp.store.SaveMarketState(saving, store.MarketState{Newest: newest, NewestSigned: signed, Revoked: data}); err != nil {
		return fmt.Errorf("%w: what this home saw can't be saved", errUnverified)
	}
	mp.newest, mp.newestSigned, mp.revoked = newest, signed, revoked
	return nil
}

func (mp *marketplace) revocation(id, version string) *MarketRevocation {
	mp.mu.Lock()
	defer mp.mu.Unlock()
	if i := slices.IndexFunc(mp.revoked, func(r MarketRevocation) bool { return r.ID == id && r.Version == version }); i >= 0 {
		found := mp.revoked[i]
		found.Reason = clipText(found.Reason, maxReasonShown)
		return &found
	}
	return nil
}

// suspended is why new installs of a version are stopped, or "" if they aren't.
func (mp *marketplace) suspended(id, version string) string {
	if r := mp.revocation(id, version); r != nil && r.State == "suspended" {
		return r.Reason
	}
	return ""
}

// marketOffer is the entry a market: source is on offer as, in a catalog verified just now. Anything else that claims to be
// from the marketplace is refused, so a withdrawn or suspended package stays out even if it is still stored here. It returns
// nil for a source that isn't from the marketplace.
func (m *Manager) marketOffer(ctx context.Context, source string) (*MarketEntry, error) {
	match := marketSource.FindStringSubmatch(source)
	if match == nil {
		return nil, nil
	}
	if m.market == nil {
		return nil, errors.New("the marketplace isn't set up, so nothing can be added from it")
	}
	entries, err := m.market.catalog(ctx)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(entries, func(e MarketEntry) bool { return e.source() == source })
	if i < 0 {
		return nil, errors.New("the marketplace no longer offers this version, so it can't be added")
	}
	if reason := m.market.suspended(match[1], match[2]); reason != "" {
		return nil, suspendedError(reason)
	}
	return &entries[i], nil
}

// watchMarket checks the catalog in the background, so a copy that's installed hears when it is taken back without anyone
// opening the Marketplace page. What it last heard comes back after a restart, even if the marketplace is down by then.
func (m *Manager) watchMarket(ctx context.Context, every time.Duration) {
	m.market.mu.Lock()
	err := m.market.load(ctx)
	m.market.mu.Unlock()
	if err != nil {
		slog.Warn("the marketplace state can't be read", "err", err)
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if _, err := m.market.catalog(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("could not check the marketplace", "err", err)
			}
		}
	}
}

// MarketUpdate is the version an installed marketplace copy can move to, and what each version on the way says.
type MarketUpdate struct {
	Version string      `json:"version"`
	Steps   []Migration `json:"steps,omitempty"`
}

// withMarketUpdate adds to a status that the marketplace has a newer version of this copy. It reads the catalog the home last
// verified, which the background check keeps fresh, so listing extensions never waits on the marketplace.
func (m *Manager) withMarketUpdate(s Status) Status {
	match := marketSource.FindStringSubmatch(s.Source)
	if match == nil || m.market == nil {
		return s
	}
	m.market.mu.Lock()
	entries := m.market.entries
	m.market.mu.Unlock()
	i := slices.IndexFunc(entries, func(e MarketEntry) bool { return e.ID == match[1] })
	if i < 0 || !newerVersion(entries[i].Version, s.Version) || m.market.suspended(entries[i].ID, entries[i].Version) != "" {
		return s
	}
	s.MarketUpdate = &MarketUpdate{Version: entries[i].Version, Steps: migrationsBetween(entries[i].Migrations, s.Version, entries[i].Version)}
	return s
}

// withRevocation adds to a status that the marketplace took this copy back.
func (m *Manager) withRevocation(s Status) Status {
	match := marketSource.FindStringSubmatch(s.Source)
	if match == nil || m.market == nil {
		return s
	}
	s.Revoked = m.market.revocation(match[1], match[2])
	return s
}

// StopExtension stops one because its owner chose to, and takes its approval back so it stays stopped until they approve it.
// shortcut: one that asks for no access has no approval to take back, so a restart starts it again; persist a stopped flag if anyone relies on that.
func (m *Manager) StopExtension(source string) error {
	inst := m.bySource(source)
	if inst == nil {
		return errors.New("no extension from that source")
	}
	inst.quarantine(stoppedByOwner, "")
	return nil
}

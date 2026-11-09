package extension

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"home-control/internal/store"
)

const (
	maxActivityKept  = 200                  // destinations for one extension, so a sweep of addresses can't fill the database
	activityShown    = maxActivityKept + 20 // all of them: a destination pushed out of view would be a way to hide
	activityFlush    = time.Minute
	activityOverflow = "other addresses"
)

// activityLog counts where an extension's traffic goes. A violation only records what was refused, and this is the record
// of what was allowed, so the owner can see every destination it really used.
type activityLog struct {
	mu        sync.Mutex
	pending   map[[2]string]*store.Activity
	seen      map[[2]string]bool
	lastFlush time.Time
}

// note records one use. A destination seen for the first time is saved at once, before anything is sent to it.
func (i *instance) note(kind, destination string) {
	id, _ := i.identity()
	destination = i.scrub(destination)
	now := time.Now()
	a := &i.activity
	a.mu.Lock()
	overflowed := false
	defer func() {
		a.mu.Unlock()
		if overflowed { // past the lock, since this can stop the extension
			i.violate("flood", fmt.Sprintf("used over %d different destinations, which could hide where its data went", maxActivityKept))
		}
	}()
	if a.seen == nil {
		a.seen, a.pending, a.lastFlush = map[[2]string]bool{}, map[[2]string]*store.Activity{}, now
	}
	key := [2]string{kind, destination}
	if !a.seen[key] && len(a.seen) >= maxActivityKept {
		key[1] = activityOverflow
		overflowed = !a.seen[key]
	}
	first := !a.seen[key]
	a.seen[key] = true
	entry := a.pending[key]
	if entry == nil {
		entry = &store.Activity{Extension: id, Kind: key[0], Destination: key[1], First: now}
		a.pending[key] = entry
	}
	entry.Count++
	entry.Last = now
	if first || now.Sub(a.lastFlush) >= activityFlush {
		i.flushLocked(now)
	}
}

func (i *instance) flushActivity() {
	i.activity.mu.Lock()
	defer i.activity.mu.Unlock()
	i.flushLocked(time.Now())
}

// flushLocked saves the counts. An entry that can't be saved stays to try again, and the traffic isn't held up for it.
func (i *instance) flushLocked(now time.Time) {
	a := &i.activity
	a.lastFlush = now
	ctx, cancel := context.WithTimeout(context.Background(), violationStoreMax)
	defer cancel()
	for key, entry := range a.pending {
		if err := i.manager.store.RecordActivity(ctx, *entry); err != nil {
			slog.Warn("extension activity could not be saved", "extension", entry.Extension, "err", err)
			return
		}
		delete(a.pending, key)
	}
}

package extension

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"sync"
	"time"

	"github.com/dop251/goja"
)

const (
	defaultHeapLimitMB = 512
	memoryEvery        = 200 * time.Millisecond
	minRunawayCall     = 300 * time.Millisecond
	unattributedAfter  = 5 * time.Second
)

// The VM can't cap its own memory, so the service watches the whole process's heap, and when it stays over the limit
// stops the extension most likely to be the cause: see culprits.
// shortcut: a single builtin that allocates gigabytes in one step (such as "x".repeat(2**30)) can't be interrupted and
// can exceed the limit before the next sample; upgrade by running extensions in a child process with a real memory limit.

// activeCalls is the VMs that are running code right now and whose instance they belong to.
type activeCalls struct {
	mu  sync.Mutex
	vms map[*goja.Runtime]activeCall
}

type activeCall struct {
	owner *instance
	since time.Time
	// heapAtStart is the lowest the heap has been sampled at during the call, reset when someone else takes the blame.
	heapAtStart uint64
	// stopping means it has been interrupted but hasn't noticed yet, since that only happens between native steps. Blaming
	// it again would leave another runaway free to keep growing.
	stopping bool
}

// track marks vm as running code until the returned function is called. owner is nil while the code is only loading,
// when it can be interrupted but there is no running extension yet to quarantine.
func (m *Manager) track(vm *goja.Runtime, owner *instance) func() {
	m.active.mu.Lock()
	if m.active.vms == nil {
		m.active.vms = map[*goja.Runtime]activeCall{}
	}
	m.active.vms[vm] = activeCall{owner: owner, since: time.Now(), heapAtStart: m.lastHeap.Load()}
	m.active.mu.Unlock()
	return func() {
		m.active.mu.Lock()
		delete(m.active.vms, vm)
		m.active.mu.Unlock()
	}
}

// heapLimit reads EXTENSION_HEAP_LIMIT_MB.
func heapLimit() uint64 {
	mb := uint64(defaultHeapLimitMB)
	if text := os.Getenv("EXTENSION_HEAP_LIMIT_MB"); text != "" {
		if n, err := strconv.ParseUint(text, 10, 32); err == nil && n > 0 {
			mb = n
		} else {
			slog.Warn("EXTENSION_HEAP_LIMIT_MB is not a whole number of megabytes; using the default", "default", defaultHeapLimitMB)
		}
	}
	return mb << 20
}

// watchMemory samples the heap until ctx ends.
func (m *Manager) watchMemory(ctx context.Context, limit uint64, every time.Duration) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	var overSince time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		used := heapNow()
		m.lastHeap.Store(used)
		m.noteHeap(used)
		if used > limit && used < 2*limit {
			// Slightly over may just be what a stopped extension left behind. Far over, there's no time for this: a
			// collection takes longer the bigger the heap, and a runaway keeps allocating during it.
			runtime.GC()
			used = heapNow()
		}
		if used <= limit {
			overSince = time.Time{}
			continue
		}
		if overSince.IsZero() {
			overSince = time.Now()
		}
		m.overMemory(used, limit, time.Since(overSince))
	}
}

func heapNow() uint64 {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

// culprits picks who to stop: the call the heap grew most during. Age alone isn't evidence, because under this much
// pressure everything stalls and a healthy call looks long too, while one that began after memory was already high grew
// it by nothing. Memory can also pile up between short calls, where nothing tells them apart, so if it stays high with
// nobody to blame and nobody already stopping, every running call is stopped.
func culprits(running map[*goja.Runtime]activeCall, now time.Time, overFor time.Duration, heap, limit uint64) map[*goja.Runtime]activeCall {
	var worst *goja.Runtime
	var worstGrowth uint64
	stopping := false
	for vm, call := range running {
		if call.stopping {
			stopping = true
			delete(running, vm)
			continue
		}
		if now.Sub(call.since) < minRunawayCall || heap <= call.heapAtStart {
			continue
		}
		growth := heap - call.heapAtStart
		if growth < limit/4 {
			continue
		}
		if worst == nil || growth > worstGrowth || (growth == worstGrowth && call.since.Before(running[worst].since)) {
			worst, worstGrowth = vm, growth
		}
	}
	if worst != nil {
		return map[*goja.Runtime]activeCall{worst: running[worst]}
	}
	if overFor >= unattributedAfter && !stopping {
		return running
	}
	return nil
}

// noteHeap lowers each call's starting point when the heap falls, so growth is measured from its lowest.
func (m *Manager) noteHeap(used uint64) {
	m.active.mu.Lock()
	defer m.active.mu.Unlock()
	for vm, call := range m.active.vms {
		if used < call.heapAtStart {
			call.heapAtStart = used
			m.active.vms[vm] = call
		}
	}
}

// overMemory stops the extension to blame, then records it and has the memory returned.
func (m *Manager) overMemory(used, limit uint64, overFor time.Duration) {
	m.active.mu.Lock()
	running := make(map[*goja.Runtime]activeCall, len(m.active.vms))
	for vm, call := range m.active.vms {
		running[vm] = call
	}
	m.active.mu.Unlock()
	stop := culprits(running, time.Now(), overFor, used, limit)
	if len(stop) == 0 {
		m.returnMemory() // likely what a stopped extension left behind, so it shouldn't count against anyone still running
		return
	}
	m.active.mu.Lock()
	for vm, call := range m.active.vms {
		if _, blamed := stop[vm]; blamed {
			call.stopping = true
		}
		// Growth so far is explained now. A call stalled while a runaway grew would otherwise look guilty next.
		call.heapAtStart = used
		m.active.vms[vm] = call
	}
	m.active.mu.Unlock()
	for vm := range stop { // first, before anything slow
		vm.Interrupt("extension used too much memory")
	}
	detail := fmt.Sprintf("the service's heap passed %d MB while it was running", limit>>20)
	for _, call := range stop {
		if call.owner != nil {
			call.owner.overMemory(detail)
		}
	}
	slog.Warn("extension heap limit passed", "usedMB", used>>20, "limitMB", limit>>20, "stopped", len(stop))
	m.returnMemory()
}

// returnMemory collects in the background, since the watch has to keep sampling while a big heap is handled.
func (m *Manager) returnMemory() {
	if m.returning.CompareAndSwap(false, true) {
		go func() {
			defer m.returning.Store(false)
			debug.FreeOSMemory()
		}()
	}
}

// overMemory counts as a violation, and stops the extension however many it already had.
func (i *instance) overMemory(detail string) {
	i.violate("memory", detail)
	i.mu.RLock()
	stopped := i.quarantined
	i.mu.RUnlock()
	if !stopped {
		i.quarantine("memory", detail)
	}
}

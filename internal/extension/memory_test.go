package extension

import (
	"context"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dop251/goja"

	"tomtly/internal/store"
)

// Not parallel, and the only test with a watchdog: it samples the whole process's heap, so it would stop whatever else
// was running in a call.
func TestMemoryBombsAreStopped(t *testing.T) {
	// A bomb can jump past a container's memory limit in one uninterruptible step, which kills the test and would block
	// image builds, so this runs on request: TOMTLY_MEMORY_TEST=1 go test -run MemoryBombs ./internal/extension/
	if os.Getenv("TOMTLY_MEMORY_TEST") == "" {
		t.Skip("set TOMTLY_MEMORY_TEST=1 to run the real memory bombs")
	}
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	limit := before.HeapAlloc + 200<<20

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m := newManager(t.TempDir(), t.TempDir(), store.NewMemory(), hub(t))
	t.Cleanup(func() { stopAll(m) })
	m.ctx = ctx
	bombs := map[string]string{
		// Goja joins these strings lazily and refuses the length itself, so this one never gets to use memory.
		"lazy": `let s = "x"; while (true) { s += s; }`,
		// Reading a character makes each doubled string real.
		"flat":   `let s = "x"; while (true) { s += s; s.charCodeAt(s.length - 1); }`,
		"arrays": `const a = []; while (true) { a.push(new Array(1e6).fill(0)); }`,
	}
	for id, code := range bombs {
		m.add(ctx, entry{Source: folder(t, `defineExtension({id:"`+id+`",name:"b",version:"1",data:{fetch(){`+code+`}}});`)}, false)
	}
	// Another extension, running all the while. It is rarely inside a call when the watchdog fires.
	m.add(ctx, entry{Source: folder(t, `defineExtension({id:"fine",name:"f",version:"1",background:{everySeconds:0.05,run(c){ n++; c.publish(n); }}}); let n = 0;`)}, false)
	go m.watchMemory(ctx, limit, 20_000_000)
	var peak atomic.Uint64
	go func() {
		for ctx.Err() == nil {
			if now := heapNow(); now > peak.Load() {
				peak.Store(now)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()

	for id := range bombs {
		if id == "lazy" {
			continue
		}
		// Stopped one at a time now, and each has to reach the limit by itself first, which takes a while on a busy machine.
		for deadline := time.Now().Add(60 * time.Second); ; time.Sleep(50 * time.Millisecond) {
			if s := statusOf(m, id); s.NeedsApproval && !s.Running {
				break
			}
			if time.Now().After(deadline) {
				m.active.mu.Lock()
				ages := []string{}
				for _, call := range m.active.vms {
					name := "loading"
					if call.owner != nil {
						name, _ = call.owner.identity()
					}
					ages = append(ages, name+":"+time.Since(call.since).Round(time.Second).String())
				}
				m.active.mu.Unlock()
				t.Fatalf("%s was never stopped: heap %d MB, limit %d MB, active %v: %+v", id, heapNow()>>20, limit>>20, ages, statusOf(m, id))
			}
		}
		var memory bool
		for _, v := range violationsOf(t, m, id) {
			memory = memory || v.Kind == "memory"
		}
		if !memory {
			t.Errorf("%s: no memory violation was recorded", id)
		}
		if grants, _ := m.store.Grants(ctx, id); len(grants) != 0 {
			t.Errorf("%s: grants should be revoked: %v", id, grants)
		}
	}
	// Not exact, since a single step of a bomb can allocate a lot, but a guard that lets it reach many gigabytes has failed.
	if got := peak.Load() >> 20; got > 20000 {
		t.Errorf("the heap reached %d MB before the bombs were stopped", got)
	}
	eventually(t, "the lazy bomb to end", func() bool { return statusOf(m, "lazy").Error != "" })
	counted := func() int { d, _, _ := m.plugins.Get("fine"); n, _ := strconv.Atoi(string(d)); return n }
	seen := counted()
	for deadline := time.Now().Add(20 * time.Second); counted() <= seen+5; time.Sleep(50 * time.Millisecond) { // a loaded build machine is slow
		if time.Now().After(deadline) {
			t.Fatalf("the other extension stopped running: %+v", statusOf(m, "fine"))
		}
	}
	if s := statusOf(m, "fine"); !s.Running || s.NeedsApproval || len(violationsOf(t, m, "fine")) != 0 {
		t.Errorf("the well-behaved extension was touched: %+v", s)
	}
	for deadline := time.Now().Add(30 * time.Second); heapNow() >= limit; time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the memory never came back: %d MB", heapNow()>>20)
		}
	}
}

func statusOf(m *Manager, id string) Status {
	for _, s := range m.Statuses() {
		if s.ID == id {
			return s
		}
	}
	return Status{}
}

func TestStringifyIsCapped(t *testing.T) {
	vm := goja.New()
	big, err := vm.RunString(`"x".repeat(5000000)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stringify(vm, big); err == nil {
		t.Error("a 5 MB string was turned into JSON")
	}
	small, _ := vm.RunString(`({a: [1, 2]})`)
	if text, err := stringify(vm, small); err != nil || text != `{"a":[1,2]}` {
		t.Errorf("got %q, %v", text, err)
	}
}

func TestTheCallTheHeapGrewDuringIsBlamed(t *testing.T) {
	const mb = 1 << 20
	limit, heap := uint64(512*mb), uint64(4000*mb)
	now := time.Now()
	long := now.Add(-5 * time.Second)
	calls := func() (*goja.Runtime, *goja.Runtime, map[*goja.Runtime]activeCall) {
		runaway, healthy := goja.New(), goja.New()
		return runaway, healthy, map[*goja.Runtime]activeCall{
			runaway: {since: now.Add(-2 * time.Second), heapAtStart: 100 * mb},
			healthy: {since: long, heapAtStart: 3990 * mb}, // older, but it began after memory was already high
		}
	}
	runaway, healthy, running := calls()
	if got := culprits(running, now, 0, heap, limit); len(got) != 1 || got[runaway].since.IsZero() {
		t.Errorf("the call the heap grew during should be stopped, not the older one: %d", len(got))
	}
	_ = healthy

	runaway, _, running = calls()
	second := goja.New()
	running[second] = activeCall{since: long, heapAtStart: 2000 * mb}
	call := running[runaway]
	call.stopping = true
	running[runaway] = call
	if got := culprits(running, now, 0, heap, limit); len(got) != 1 || got[second].since.IsZero() {
		t.Errorf("a call that's already stopping must not shield the next runaway: %d", len(got))
	}

	only := goja.New()
	short := map[*goja.Runtime]activeCall{only: {since: now.Add(-10 * time.Millisecond), heapAtStart: 100 * mb}}
	if got := culprits(short, now, time.Second, heap, limit); len(got) != 0 {
		t.Errorf("a brand new call shouldn't be blamed: %d", len(got))
	}
	flat := map[*goja.Runtime]activeCall{only: {since: long, heapAtStart: 3990 * mb}}
	if got := culprits(flat, now, time.Second, heap, limit); len(got) != 0 {
		t.Errorf("a call the heap didn't grow during shouldn't be blamed straight away: %d", len(got))
	}
	if got := culprits(flat, now, unattributedAfter, heap, limit); len(got) != 1 {
		t.Errorf("memory that stays high with nothing to blame stops what's running: %d", len(got))
	}
	stuck := map[*goja.Runtime]activeCall{only: {since: long, heapAtStart: 3990 * mb}, goja.New(): {since: long, stopping: true}}
	if got := culprits(stuck, now, unattributedAfter, heap, limit); len(got) != 0 {
		t.Errorf("wait for what's already stopping before blaming everything: %d", len(got))
	}
}

package automation

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"home-control/device"
	"home-control/internal/store"
)

// These pin the exact edges of the engine's limits and timers, where an off-by-one would go unnoticed otherwise.

func countKind(e *Engine, kind, contains string) int {
	n := 0
	for _, ev := range e.Status().Events {
		if ev.Kind == kind && strings.Contains(ev.Message, contains) {
			n++
		}
	}
	return n
}

func TestLogLimitIsExactlyPerMinute(t *testing.T) {
	e, clock := harness(t, &fakeHub{devices: []device.Device{lamp()}}, `
		home.reconcile(() => { for (let i = 0; i < 120; i++) home.log("line " + i); });`)
	e.Tick()
	if got, refused := countKind(e, "log", "line "), countKind(e, "refused", "logged over"); got != maxLogsPerMinute || refused != 0 {
		t.Fatalf("a full minute kept %d lines and refused %d times", got, refused)
	}
	*clock = clock.Add(time.Minute) // exactly a minute later the count starts again
	e.Tick()
	if refused := countKind(e, "refused", "logged over"); refused != 0 {
		t.Errorf("the next minute was refused %d times", refused)
	}
}

func TestLogLimitDropsTheRestAndSaysSoOnce(t *testing.T) {
	e, _ := harness(t, &fakeHub{devices: []device.Device{lamp()}}, `
		home.reconcile(() => { for (let i = 0; i < 125; i++) home.log("line " + i); });`)
	e.Tick()
	if got := countKind(e, "log", "line "); got != maxLogsPerMinute {
		t.Errorf("kept %d lines, want %d", got, maxLogsPerMinute)
	}
	if got := countKind(e, "refused", "logged over"); got != 1 {
		t.Errorf("said so %d times, want once", got)
	}
}

func TestMessageAtTheLimitIsKeptWhole(t *testing.T) {
	e, _ := harness(t, &fakeHub{devices: []device.Device{lamp()}}, `home.log("x".repeat(2000)); home.log("y".repeat(2001));`)
	lines := logged(e)
	if len(lines) != 2 || lines[1] != strings.Repeat("x", maxMessage) || lines[0] != strings.Repeat("y", maxMessage)+"..." {
		t.Errorf("got lengths %d", len(lines))
	}
}

func TestEventLogKeepsExactlyItsSize(t *testing.T) {
	e, _ := harness(t, &fakeHub{}, ``)
	for range maxEvents + 5 {
		e.record("test", "log", "entry")
	}
	if got := len(e.Status().Events); got != maxEvents {
		t.Errorf("kept %d events, want %d", got, maxEvents)
	}
}

func TestAccessKeyLengthLimit(t *testing.T) {
	gated(t)
	fits := strings.Repeat("a", maxKeyLength-len(readDevice+":"))
	long := strings.Repeat("b", maxKeyLength-len(readDevice+":")+1)
	e, _ := harness(t, &fakeHub{devices: []device.Device{lamp()}}, `home.device("`+fits+`").get("switch"); home.device("`+long+`").get("switch");`)
	access := accessOf(t, e)
	if _, ok := access[key(readDevice, fits)]; !ok {
		t.Error("a key of exactly the limit wasn't listed")
	}
	if _, ok := access[key(readDevice, long)]; ok {
		t.Error("a key over the limit was listed")
	}
}

func TestActionLabelWithEmptyPluginID(t *testing.T) {
	e, _ := harness(t, &fakeHub{}, ``)
	if got := e.describe(key(pluginAction, ":go")); !strings.Contains(got, `Run "go"`) {
		t.Errorf("got %q", got)
	}
}

func TestActionRepeatsExactlyAtTheResendTime(t *testing.T) {
	dir := t.TempDir()
	source := `const w = home.plugin("weather"); home.reconcile(() => w.action("alert", { text: "cold" }));`
	if err := os.WriteFile(filepath.Join(dir, "test.ts"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	e := New(&fakeHub{}, dir, t.TempDir(), store.NewMemory(), time.UTC)
	start := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	clock := start
	e.now = func() time.Time { return clock }
	e.Plugins = &fakePlugins{}
	e.Reload()
	for _, at := range []time.Duration{0, resendAfter - time.Second, resendAfter} {
		clock = start.Add(at)
		e.Tick()
	}
	if got := countKind(e, "shadow", "would call"); got != 2 {
		t.Errorf("%d shadow calls, want one at the start and one exactly at the resend time", got)
	}
}

func TestLiveResendsExactlyAtTheResendTime(t *testing.T) {
	hub := &fakeHub{devices: []device.Device{lamp()}}
	e, clock := harness(t, hub, lampOn)
	if err := e.SetLive("test", true); err != nil {
		t.Fatal(err)
	}
	e.Tick()
	*clock = clock.Add(resendAfter - time.Second)
	e.Tick()
	if len(hub.writes) != 1 {
		t.Fatalf("resent early: %v", hub.writes)
	}
	*clock = clock.Add(time.Second)
	e.Tick()
	if len(hub.writes) != 2 {
		t.Errorf("not resent at the resend time: %v", hub.writes)
	}
}

func TestMismatchIsFlaggedOnlyAfterTheWait(t *testing.T) {
	e, clock := harness(t, &fakeHub{devices: []device.Device{lamp()}}, lampOn)
	start := *clock
	e.Tick()
	*clock = start.Add(mismatchAfter)
	e.Tick()
	if countKind(e, "mismatch", "") != 0 {
		t.Fatal("flagged before the wait was over")
	}
	*clock = start.Add(mismatchAfter + time.Second)
	e.Tick()
	if countKind(e, "mismatch", "") != 1 {
		t.Error("not flagged after the wait")
	}
}

func TestFailedScriptIsRetriedAfterTheWaitAndReportedOnce(t *testing.T) {
	hub := &fakeHub{devices: []device.Device{lamp()}}
	e, clock := harness(t, hub, `home.device("late");`)
	start := *clock
	if e.Status().Scripts[0].Error == "" {
		t.Fatal("no error for a missing device")
	}
	errorsBefore := countKind(e, "error", "")
	for _, after := range []time.Duration{setupRetry + time.Second, 2*setupRetry + 2*time.Second} {
		*clock = start.Add(after)
		e.Tick()
	}
	if got := countKind(e, "error", ""); got != errorsBefore {
		t.Errorf("a script that keeps failing was reported %d more times", got-errorsBefore)
	}

	hub.devices = append(hub.devices, device.Device{ID: "late", Name: "Late", Online: true})
	retried := *clock
	*clock = retried.Add(setupRetry)
	e.Tick()
	if e.Status().Scripts[0].Error == "" {
		t.Fatal("retried before the wait was over")
	}
	*clock = retried.Add(setupRetry + time.Second)
	e.Tick()
	if err := e.Status().Scripts[0].Error; err != "" {
		t.Errorf("not retried once the device appeared: %s", err)
	}
}

// twoScripts loads scripts a and b, both live, each turning on its own lamp.
func twoScripts(t *testing.T, hub *fakeHub) (*Engine, *time.Time) {
	t.Helper()
	hub.devices = []device.Device{lamp(), lamp()}
	hub.devices[1].ID = "lamp2"
	hub.devices[1].Name = "Lamp 2"
	e, clock := harness(t, hub, `const lamp = home.device("lamp"); home.reconcile(() => lamp.set("switch", true));`)
	if _, err := e.SaveSource("b", `const lamp = home.device("lamp2"); home.reconcile(() => lamp.set("switch", true));`, "", false, true); err != nil {
		t.Fatal(err)
	}
	return e, clock
}

func TestSwitchingOneScriptLeavesTheOthersCommandsAlone(t *testing.T) {
	hub := &fakeHub{}
	e, _ := twoScripts(t, hub)
	if err := e.SetLive("test", true); err != nil {
		t.Fatal(err)
	}
	e.Tick()
	if err := e.SetLive("b", true); err != nil {
		t.Fatal(err)
	}
	e.Tick()
	lampWrites := 0
	for _, w := range hub.writes {
		if w.id == "lamp" {
			lampWrites++
		}
	}
	if lampWrites != 1 {
		t.Errorf("switching b made a resend for test's lamp: %v", hub.writes)
	}
}

func TestApprovingOneScriptKeepsItsRuntimeRequestsToItself(t *testing.T) {
	gated(t)
	hub := &fakeHub{devices: []device.Device{lamp(), sensor()}}
	e, _ := harness(t, hub, `home.device("lamp").get("switch");
		let ran = false; home.reconcile(() => { if (ran) home.device("tuya:sensor").get("temperature"); ran = true; });`)
	if _, err := e.SaveSource("b", `home.device("lamp").get("switch");`, "", false, true); err != nil {
		t.Fatal(err)
	}
	e.Tick()
	e.Tick() // the sensor is only asked for from the second run
	approve(t, e, key(readDevice, "lamp"))
	sensorKey := key(readDevice, "tuya:sensor")
	for _, st := range e.Status().Scripts {
		has := slices.ContainsFunc(st.Access, func(a Access) bool { return a.Key == sensorKey })
		if st.Name == "b" && has {
			t.Error("b was handed test's request for the sensor")
		}
		if st.Name == "test" && !has {
			t.Error("test lost its request for the sensor when it reloaded")
		}
	}
}

func TestSaveReportsTheAccessOfTheOnlyScript(t *testing.T) {
	gated(t)
	e := New(&fakeHub{devices: []device.Device{sensor()}}, t.TempDir(), t.TempDir(), store.NewMemory(), time.UTC)
	e.Reload()
	got, err := e.SaveSource("only", `home.device("tuya:sensor").get("temperature");`, "", false, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Access) != 1 || got.Access[0].Key != key(readDevice, "tuya:sensor") {
		t.Errorf("saving reported %+v", got.Access)
	}
}

func TestTrialStopsAfterTheFirstCallbackRunsOutOfTime(t *testing.T) {
	e, _ := harness(t, &fakeHub{}, ``)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = e.check("loop", `home.reconcile(() => { for (;;) {} }); home.reconcile(() => { for (;;) {} });`)
	}()
	select {
	case <-done:
	case <-time.After(3 * callTimeout):
		t.Fatal("the trial kept running callbacks after the time limit")
	}
}

// batchStore records the size of every batch it is asked to save.
type batchStore struct {
	store.Store
	mu      sync.Mutex
	batches []int
}

func (b *batchStore) AppendEvents(ctx context.Context, events []store.Event) error {
	b.mu.Lock()
	b.batches = append(b.batches, len(events))
	b.mu.Unlock()
	return b.Store.AppendEvents(ctx, events)
}

func (b *batchStore) sizes() []int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]int(nil), b.batches...)
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for range 400 {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out")
}

func TestOneEventIsSavedOnItsOwn(t *testing.T) {
	st := &batchStore{Store: store.NewMemory()}
	e := New(&fakeHub{}, t.TempDir(), t.TempDir(), st, time.UTC)
	stop := make(chan struct{})
	defer close(stop)
	go e.saveEvents(stop)
	e.record("test", "log", "only")
	waitFor(t, func() bool { return len(st.sizes()) == 1 })
}

func TestSavedBatchesNeverExceedTheLogSize(t *testing.T) {
	st := &batchStore{Store: store.NewMemory()}
	e := New(&fakeHub{}, t.TempDir(), t.TempDir(), st, time.UTC)
	e.saving = make(chan store.Event, 3*maxEvents)
	for range 2*maxEvents + 1 {
		e.saving <- store.Event{Script: "test", Kind: "log", Message: "x"}
	}
	stop := make(chan struct{})
	defer close(stop)
	go e.saveEvents(stop)
	waitFor(t, func() bool {
		total := 0
		for _, n := range st.sizes() {
			total += n
		}
		return total == 2*maxEvents+1
	})
	if sizes := st.sizes(); sizes[0] != maxEvents || sizes[1] != maxEvents || sizes[2] != 1 {
		t.Errorf("batches %v", sizes)
	}
}

type brokenStore struct{ store.Store }

func (brokenStore) AppendEvents(context.Context, []store.Event) error { return errors.New("disk full") }

func TestUnsavedActivityIsReported(t *testing.T) {
	var buf syncBuffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	e := New(&fakeHub{}, t.TempDir(), t.TempDir(), brokenStore{store.NewMemory()}, time.UTC)
	stop := make(chan struct{})
	defer close(stop)
	go e.saveEvents(stop)
	e.record("test", "log", "lost")
	waitFor(t, func() bool { return strings.Contains(buf.String(), "activity log not saved") })
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestOnlyNotableEventsReachTheServiceLog(t *testing.T) {
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	e, _ := harness(t, &fakeHub{}, ``)
	for _, kind := range []string{"set", "error", "mismatch", "refused", "log", "shadow", "matched"} {
		e.record("test", kind, "kind-"+kind)
	}
	for _, kind := range []string{"set", "error", "mismatch", "refused"} {
		if !strings.Contains(buf.String(), "kind-"+kind) {
			t.Errorf("%s isn't in the service log", kind)
		}
	}
	for _, kind := range []string{"log", "shadow", "matched"} {
		if strings.Contains(buf.String(), "kind-"+kind) {
			t.Errorf("%s is in the service log", kind)
		}
	}
}

func TestStartupLoadFailureIsQuietWhileDevicesConnect(t *testing.T) {
	hub := &fakeHub{devices: []device.Device{lamp()}}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "test.ts"), []byte(`home.device("late");`), 0o644); err != nil {
		t.Fatal(err)
	}
	e := New(hub, dir, t.TempDir(), store.NewMemory(), time.UTC)
	start := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	clock := start
	e.now = func() time.Time { return clock }
	e.quietUntil = start.Add(startupGrace) // what Run sets
	e.Reload()
	if countKind(e, "error", "") != 0 {
		t.Fatal("a device not connected yet was reported during startup")
	}

	hub.devices = append(hub.devices, device.Device{ID: "late", Name: "Late", Online: true})
	clock = start.Add(time.Second)
	e.Tick()
	if err := e.Status().Scripts[0].Error; err != "" || countKind(e, "error", "") != 0 {
		t.Fatalf("not loaded quietly once the device connected: %q", err)
	}

	hub.devices = hub.devices[:1]
	if err := os.WriteFile(filepath.Join(dir, "test.ts"), []byte(`home.device("never");`), 0o644); err != nil {
		t.Fatal(err)
	}
	e.Reload()
	clock = start.Add(startupGrace)
	e.Tick()
	if got := countKind(e, "error", ""); got != 1 {
		t.Errorf("a device still missing after startup was reported %d times, want 1", got)
	}
}

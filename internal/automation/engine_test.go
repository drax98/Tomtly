package automation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"home-control/device"
	"home-control/internal/store"
	"home-control/plugin"
)

type written struct {
	id, dp string
	value  any
}

type fakeHub struct {
	devices []device.Device
	writes  []written
	apply   bool // writes take effect, like a real device
}

func (h *fakeHub) Devices() []device.Device { return h.devices }

func (h *fakeHub) Write(id, dp string, value any) error {
	h.writes = append(h.writes, written{id, dp, value})
	if h.apply {
		h.setDP(id, dp, value)
	}
	return nil
}

func (h *fakeHub) setDP(id, dp string, value any) {
	for i := range h.devices {
		if h.devices[i].ID != id {
			continue
		}
		for j := range h.devices[i].DPs {
			if h.devices[i].DPs[j].DP == dp {
				h.devices[i].DPs[j].Value = value
			}
		}
	}
}

func lamp() device.Device {
	return device.Device{ID: "lamp", Name: "Lamp", Online: true, DPs: []device.DP{
		{DP: "1", Code: "switch", Type: device.Boolean, Writable: true, Value: false},
		{DP: "2", Code: "watts", Type: device.Integer, Values: map[string]any{}, Value: 5.0},
	}}
}

// harness writes one script and returns an engine with a controllable clock.
func harness(t *testing.T, hub *fakeHub, source string) (*Engine, *time.Time) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "test.ts"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	e := New(hub, dir, t.TempDir(), store.NewMemory(), time.UTC)
	clock := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return clock }
	e.Reload()
	return e, &clock
}

const lampOn = `const lamp = home.device("lamp"); home.reconcile(() => lamp.set("switch", true));`

func TestShadowSendsNothing(t *testing.T) {
	hub := &fakeHub{devices: []device.Device{lamp()}}
	e, _ := harness(t, hub, lampOn)
	e.Tick()
	e.Tick()
	if len(hub.writes) != 0 {
		t.Fatalf("shadow mode wrote %v", hub.writes)
	}
	st := e.Status()
	if len(st.Events) != 1 || st.Events[0].Kind != "shadow" || !strings.Contains(st.Events[0].Message, "Lamp switch = true") {
		t.Errorf("events %+v", st.Events)
	}
}

func TestShadowFlagsDisagreementAndRecovery(t *testing.T) {
	hub := &fakeHub{devices: []device.Device{lamp()}}
	e, clock := harness(t, hub, lampOn)
	e.Tick()
	*clock = clock.Add(mismatchAfter + time.Second)
	e.Tick()
	if st := e.Status(); st.Events[0].Kind != "mismatch" {
		t.Fatalf("want a mismatch, got %+v", st.Events)
	}
	hub.setDP("lamp", "1", true) // the old automation did it after all
	e.Tick()
	if st := e.Status(); st.Events[0].Kind != "matched" {
		t.Errorf("want matched, got %+v", st.Events)
	}
}

func TestShadowDecisionThatComesTrueIsNotFlagged(t *testing.T) {
	hub := &fakeHub{devices: []device.Device{lamp()}}
	e, clock := harness(t, hub, lampOn)
	e.Tick()
	hub.setDP("lamp", "1", true)
	e.Tick()
	*clock = clock.Add(mismatchAfter * 2)
	e.Tick()
	for _, ev := range e.Status().Events {
		if ev.Kind == "mismatch" {
			t.Errorf("unexpected %+v", ev)
		}
	}
}

func TestLiveSendsOnceThenRetries(t *testing.T) {
	hub := &fakeHub{devices: []device.Device{lamp()}}
	e, clock := harness(t, hub, lampOn)
	if err := e.SetLive("test", true); err != nil {
		t.Fatal(err)
	}
	e.Tick()
	e.Tick()
	if len(hub.writes) != 1 || hub.writes[0] != (written{"lamp", "1", true}) {
		t.Fatalf("writes %v", hub.writes)
	}
	*clock = clock.Add(resendAfter + time.Second)
	e.Tick()
	if len(hub.writes) != 2 {
		t.Errorf("a command with no effect must be retried, writes %v", hub.writes)
	}
}

func TestLiveSendsNothingWhenAlreadyRight(t *testing.T) {
	hub := &fakeHub{devices: []device.Device{lamp()}, apply: true}
	e, clock := harness(t, hub, lampOn)
	if err := e.SetLive("test", true); err != nil {
		t.Fatal(err)
	}
	e.Tick()
	*clock = clock.Add(time.Hour)
	e.Tick()
	if len(hub.writes) != 1 {
		t.Errorf("writes %v", hub.writes)
	}
}

func TestNoCommandQueuedForUnreachableDevice(t *testing.T) {
	hub := &fakeHub{devices: []device.Device{lamp()}}
	hub.devices[0].Online = false
	e, _ := harness(t, hub, lampOn)
	if err := e.SetLive("test", true); err != nil {
		t.Fatal(err)
	}
	e.Tick()
	if len(hub.writes) != 0 {
		t.Errorf("writes %v", hub.writes)
	}
}

func TestLiveSettingSurvivesRestart(t *testing.T) {
	hub := &fakeHub{devices: []device.Device{lamp()}}
	e, _ := harness(t, hub, lampOn)
	if err := e.SetLive("test", true); err != nil {
		t.Fatal(err)
	}
	again := New(hub, e.dir, e.sdkDir, e.store, time.UTC)
	again.Reload()
	if !again.Status().Scripts[0].Live {
		t.Error("live setting was lost")
	}
	if err := e.SetLive("nope", true); err == nil {
		t.Error("an unknown script was accepted")
	}
}

func TestUnreadableStateFallsBackToShadow(t *testing.T) {
	hub := &fakeHub{devices: []device.Device{lamp()}}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "test.ts"), []byte(lampOn), 0o644); err != nil {
		t.Fatal(err)
	}
	st := store.NewMemory()
	st.SetScriptLive(context.Background(), "test", true)
	st.Fail = errors.New("database is down")
	e := New(hub, dir, t.TempDir(), st, time.UTC)
	e.Reload()
	e.Tick()
	if len(hub.writes) != 0 || e.Status().Scripts[0].Live {
		t.Error("an unreadable database must leave scripts in shadow mode")
	}
}

func TestActivityLogSurvivesRestart(t *testing.T) {
	hub := &fakeHub{devices: []device.Device{lamp()}}
	e, _ := harness(t, hub, lampOn)
	stop := make(chan struct{})
	defer close(stop)
	go e.saveEvents(stop)
	e.Tick()
	for range 200 {
		if saved, _ := e.store.RecentEvents(context.Background(), 10); len(saved) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	again := New(hub, e.dir, e.sdkDir, e.store, time.UTC)
	if len(again.Status().Events) == 0 {
		t.Fatal("the activity log was lost")
	}
}

func TestBadScriptsAreIsolatedAndReported(t *testing.T) {
	for name, source := range map[string]string{
		"syntax":         `const x = ;`,
		"unknown device": `home.device("missing");`,
		"bad time":       `home.daily("25:00", () => {});`,
		"throws on load": `throw new Error("boom");`,
	} {
		e, _ := harness(t, &fakeHub{devices: []device.Device{lamp()}}, source)
		e.Tick()
		if e.Status().Scripts[0].Error == "" {
			t.Errorf("%s: no error reported", name)
		}
	}
}

func TestRuntimeErrorIsReportedOnceAndClears(t *testing.T) {
	hub := &fakeHub{devices: []device.Device{lamp()}}
	e, _ := harness(t, hub, `let fail = true; home.reconcile(() => { if (fail) { fail = false; throw new Error("boom"); } });`)
	e.Tick()
	if e.Status().Scripts[0].Error == "" {
		t.Fatal("error not reported")
	}
	e.Tick()
	if e.Status().Scripts[0].Error != "" {
		t.Error("error should clear once the script runs cleanly")
	}
}

func TestScriptCannotWriteBadValuesOrReadOnly(t *testing.T) {
	for _, source := range []string{
		`const l = home.device("lamp"); home.reconcile(() => l.set("switch", "yes"));`,
		`const l = home.device("lamp"); home.reconcile(() => l.set("watts", 1));`,
		`const l = home.device("lamp"); home.reconcile(() => l.set("nope", 1));`,
	} {
		hub := &fakeHub{devices: []device.Device{lamp()}}
		e, _ := harness(t, hub, source)
		if err := e.SetLive("test", true); err != nil {
			t.Fatal(err)
		}
		e.Tick()
		if len(hub.writes) != 0 || e.Status().Scripts[0].Error == "" {
			t.Errorf("%s: writes %v, status %+v", source, hub.writes, e.Status().Scripts[0])
		}
	}
}

func TestEndlessLoopIsStopped(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for the script time limit")
	}
	e, _ := harness(t, &fakeHub{devices: []device.Device{lamp()}}, `home.reconcile(() => { while (true) {} });`)
	done := make(chan struct{})
	go func() { e.Tick(); close(done) }()
	select {
	case <-done:
	case <-time.After(callTimeout + 3*time.Second):
		t.Fatal("the engine froze")
	}
	if !strings.Contains(e.Status().Scripts[0].Error, "too long") {
		t.Errorf("status %+v", e.Status().Scripts[0])
	}
}

func TestImportingNodeModulesFails(t *testing.T) {
	e, _ := harness(t, &fakeHub{devices: []device.Device{lamp()}}, `import { readFileSync } from "fs"; home.log(readFileSync("/etc/passwd", "utf8"));`)
	if e.Status().Scripts[0].Error == "" {
		t.Fatal("a script could import a system module")
	}
}

func TestDailyFiresOnceAtLocalTime(t *testing.T) {
	warsaw, err := time.LoadLocation("Europe/Warsaw")
	if err != nil {
		t.Fatal(err)
	}
	hub := &fakeHub{devices: []device.Device{lamp()}}
	e, clock := harness(t, hub, `const l = home.device("lamp"); home.daily("08:30", () => l.set("switch", true));`)
	e.location = warsaw
	if err := e.SetLive("test", true); err != nil {
		t.Fatal(err)
	}
	*clock = time.Date(2026, 11, 1, 8, 30, 0, 0, time.UTC) // 09:30 in Warsaw
	e.Tick()
	if len(hub.writes) != 0 {
		t.Fatal("fired at the UTC time instead of local")
	}
	*clock = time.Date(2026, 11, 1, 8, 30, 0, 0, warsaw)
	e.Tick()
	e.Tick()
	if len(hub.writes) != 1 {
		t.Fatalf("writes %v", hub.writes)
	}
	hub.setDP("lamp", "1", false)
	*clock = clock.Add(24 * time.Hour)
	e.Tick()
	if len(hub.writes) != 2 {
		t.Errorf("should fire again the next day, writes %v", hub.writes)
	}
}

func TestScriptCannotReachTheSystem(t *testing.T) {
	e, _ := harness(t, &fakeHub{devices: []device.Device{lamp()}}, `
		const g = globalThis as any;
		home.log(typeof g.require + " " + typeof g.process + " " + typeof g.fetch + " " + typeof g.setTimeout);`)
	found := false
	for _, ev := range e.Status().Events {
		if ev.Kind == "log" {
			found = true
			if ev.Message != "undefined undefined undefined undefined" {
				t.Errorf("a script can reach: %s", ev.Message)
			}
		}
	}
	if !found {
		t.Fatal("script did not run")
	}
}

func TestScriptsAreOrdinaryPrograms(t *testing.T) {
	restore := time.Local
	time.Local = time.UTC
	defer func() { time.Local = restore }()

	hub := &fakeHub{devices: []device.Device{lamp()}}
	e, clock := harness(t, hub, `
		class Counter {
			private runs = 0;
			next(): number { return ++this.runs; }
		}
		const counter = new Counter();
		const hour = (): number => new Date().getHours();
		home.reconcile(() => home.log("run " + counter.next() + " at " + hour()));
	`)
	*clock = time.Date(2026, 11, 2, 7, 0, 0, 0, time.UTC)
	e.Tick()
	e.Tick()
	st := e.Status()
	if st.Scripts[0].Error != "" {
		t.Fatal(st.Scripts[0].Error)
	}
	if got := st.Events[0].Message; got != "run 2 at 7" {
		t.Errorf("state or clock wrong: %q", got)
	}
}

type fakePlugins struct {
	data string
	at   time.Time
	acts []string
}

func (f *fakePlugins) Get(id string) ([]byte, time.Time, bool) {
	if id != "weather" {
		return nil, time.Time{}, false
	}
	if f.data == "" {
		return nil, time.Time{}, true
	}
	return []byte(f.data), f.at, true
}

func (f *fakePlugins) Statuses() []plugin.Status { return []plugin.Status{{ID: "weather"}} }

func (f *fakePlugins) Act(id, name string, args []byte) error {
	f.acts = append(f.acts, id+"."+name+" "+string(args))
	return nil
}

func TestScriptsDecideOnPluginData(t *testing.T) {
	hub := &fakeHub{devices: []device.Device{lamp()}, apply: true}
	dir := t.TempDir()
	source := `
		const weather = home.plugin<{ temperature: number; hourly: number[] }>("weather");
		const lamp = home.device("lamp");
		home.reconcile(() => {
			const now = weather.data();
			if (!now) { home.log("no data yet"); return; }
			now.temperature += 100; // must not reach the next read
			lamp.set("switch", now.temperature > 100 && weather.ageSeconds()! < 60 && now.hourly.length === 2);
		});`
	if err := os.WriteFile(filepath.Join(dir, "test.ts"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	e := New(hub, dir, t.TempDir(), store.NewMemory(), time.UTC)
	clock := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return clock }
	plugins := &fakePlugins{}
	e.Plugins = plugins
	e.Reload()
	if err := e.SetLive("test", true); err != nil {
		t.Fatal(err)
	}

	e.Tick()
	if st := e.Status(); st.Scripts[0].Error != "" || st.Events[0].Message != "no data yet" {
		t.Fatalf("before the first fetch: %+v", st.Events[0])
	}
	plugins.data, plugins.at = `{"temperature":-5,"hourly":[1,2]}`, clock.Add(-10*time.Second)
	e.Tick()
	e.Tick()
	if err := e.Status().Scripts[0].Error; err != "" {
		t.Fatal(err)
	}
	if hub.value("lamp", "1") != false {
		t.Fatal("-5 + 100 is not over 100: the script's change leaked into a later read or it misread the data")
	}
	plugins.data = `{"temperature":5,"hourly":[1,2]}`
	e.Tick()
	if hub.value("lamp", "1") != true {
		t.Fatal("the script should act on the new data")
	}
	if len(e.Status().Plugins) != 1 {
		t.Error("the page should see plugins")
	}
}

func TestUnknownPluginFailsTheScript(t *testing.T) {
	e, _ := harness(t, &fakeHub{devices: []device.Device{lamp()}}, `home.plugin("nope");`)
	if err := e.Status().Scripts[0].Error; !strings.Contains(err, "unknown plugin") {
		t.Fatalf("got %q", err)
	}
}

func TestActionsRespectShadowAndDontRepeat(t *testing.T) {
	hub := &fakeHub{devices: []device.Device{lamp()}}
	dir := t.TempDir()
	source := `const w = home.plugin("weather"); home.reconcile(() => w.action("alert", { text: "cold" }));`
	if err := os.WriteFile(filepath.Join(dir, "test.ts"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	e := New(hub, dir, t.TempDir(), store.NewMemory(), time.UTC)
	clock := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return clock }
	plugins := &fakePlugins{}
	e.Plugins = plugins
	e.Reload()
	e.Tick()
	e.Tick()
	if len(plugins.acts) != 0 {
		t.Fatalf("shadow mode called the plugin: %v", plugins.acts)
	}
	if st := e.Status(); st.Events[0].Kind != "shadow" || !strings.Contains(st.Events[0].Message, "would call weather.alert") {
		t.Fatalf("got %+v", st.Events[0])
	}
	if err := e.SetLive("test", true); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(resendAfter + time.Second)
	e.Tick()
	e.Tick()
	if len(plugins.acts) != 1 || plugins.acts[0] != `weather.alert {"text":"cold"}` {
		t.Fatalf("got %v", plugins.acts)
	}
	clock = clock.Add(resendAfter + time.Second)
	e.Tick()
	if len(plugins.acts) != 2 {
		t.Fatalf("a repeat after a while should go through: %v", plugins.acts)
	}
}

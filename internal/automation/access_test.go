package automation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"home-control/device"
	"home-control/internal/store"
)

func TestMain(m *testing.M) {
	approveAll.Store(true) // the other tests are about what scripts do once approved
	os.Exit(m.Run())
}

// gated turns on the owner's approval for one test.
func gated(t *testing.T) {
	t.Helper()
	approveAll.Store(false)
	t.Cleanup(func() { approveAll.Store(true) })
}

// sensor reads "temperature" and can't be written.
func sensor() device.Device {
	return device.Device{ID: "tuya:sensor", Name: "Sensor", Online: true, DPs: []device.DP{{DP: "1", Code: "temperature", Type: device.Integer, Value: int64(21)}}}
}

func accessOf(t *testing.T, e *Engine) map[string]Access {
	t.Helper()
	out := map[string]Access{}
	for _, a := range e.Status().Scripts[0].Access {
		out[a.Key] = a
	}
	return out
}

func logged(e *Engine) []string {
	var out []string
	for _, ev := range e.Status().Events {
		if ev.Kind == "log" {
			out = append(out, ev.Message)
		}
	}
	return out
}

func approve(t *testing.T, e *Engine, keys ...string) {
	t.Helper()
	if err := e.SetAccess("test", keys, true); err != nil {
		t.Fatal(err)
	}
}

func TestUnapprovedCallsAreRefusedOneByOne(t *testing.T) {
	gated(t)
	hub := &fakeHub{devices: []device.Device{lamp(), sensor()}}
	e, _ := harness(t, hub, `
		const lamp = home.device("lamp");
		const sensor = home.device("tuya:sensor");
		home.reconcile(() => {
			home.log("temperature " + sensor.get("temperature") + " online " + sensor.online() + " offline " + sensor.offlineSeconds());
			lamp.set("switch", true);
			home.log("still running");
		});`)
	if err := e.SetLive("test", true); err != nil {
		t.Fatal(err)
	}
	e.Tick()
	e.Tick()
	if e.Status().Scripts[0].Error != "" {
		t.Fatalf("a refusal stopped the script: %q", e.Status().Scripts[0].Error)
	}
	if len(hub.writes) != 0 {
		t.Fatalf("an unapproved write reached the device: %v", hub.writes)
	}
	lines := logged(e)
	if !slices.Contains(lines, "temperature undefined online false offline 0") || !slices.Contains(lines, "still running") {
		t.Fatalf("got %v", lines)
	}
	got := accessOf(t, e)
	if a := got["device.write:lamp"]; a.Approved || a.Refused == 0 || !strings.Contains(a.Label, "Change Lamp") {
		t.Errorf("write: %+v", a)
	}
	if a := got["device.read:tuya:sensor"]; a.Approved || a.Refused == 0 || !strings.Contains(a.Label, "Read Sensor") {
		t.Errorf("read: %+v", a)
	}
	refusals := 0
	for _, ev := range e.Status().Events {
		if ev.Kind == "refused" {
			refusals++
		}
	}
	if refusals != 2 {
		t.Errorf("each refused thing should reach the log once, got %d", refusals)
	}
}

func TestApprovalIsPerThing(t *testing.T) {
	gated(t)
	hub := &fakeHub{devices: []device.Device{lamp(), sensor()}}
	e, _ := harness(t, hub, `
		const lamp = home.device("lamp");
		const sensor = home.device("tuya:sensor");
		home.reconcile(() => {
			home.log("temperature " + sensor.get("temperature"));
			lamp.set("switch", true);
		});`)
	if err := e.SetLive("test", true); err != nil {
		t.Fatal(err)
	}
	e.Tick()
	approve(t, e, "device.read:tuya:sensor")
	e.Tick()
	if !slices.Contains(logged(e), "temperature 21") {
		t.Errorf("the approved read should work: %v", logged(e))
	}
	if len(hub.writes) != 0 {
		t.Fatal("approving a read allowed a write")
	}
	if !e.Status().Scripts[0].Live {
		t.Error("an approval must not change whether the script is live")
	}
	approve(t, e, "device.write:lamp")
	e.Tick()
	if len(hub.writes) != 1 {
		t.Fatalf("the approved write should go through: %v", hub.writes)
	}
	if err := e.SetAccess("test", []string{"device.write:lamp"}, false); err != nil {
		t.Fatal(err)
	}
	hub.setDP("lamp", "1", false)
	e.Tick()
	if len(hub.writes) != 1 {
		t.Fatal("a withdrawn approval still works")
	}
}

func TestApprovalsSurviveARestartAndAskingForMoreNeedsANewOne(t *testing.T) {
	gated(t)
	hub := &fakeHub{devices: []device.Device{lamp(), sensor()}}
	e, _ := harness(t, hub, lampOn)
	if err := e.SetLive("test", true); err != nil {
		t.Fatal(err)
	}
	approve(t, e, "device.write:lamp")

	again := New(hub, e.dir, e.sdkDir, e.store, time.UTC)
	again.Reload()
	again.Tick()
	if len(hub.writes) != 1 {
		t.Fatalf("the approval was lost: %v", hub.writes)
	}

	src, err := again.ReadSource("test")
	if err != nil {
		t.Fatal(err)
	}
	wider := `home.reconcile(() => { home.device("lamp").set("switch", true); home.log(String(home.device("tuya:sensor").get("temperature"))); });`
	saved, err := again.SaveSource("test", wider, src.Version, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(saved.Access, func(a Access) bool { return a.Key == "device.read:tuya:sensor" && !a.Approved }) {
		t.Errorf("the save should say what is waiting: %+v", saved.Access)
	}
	again.Tick()
	if slices.Contains(logged(again), "21") {
		t.Fatal("a script read what it had just added, with no approval")
	}
}

func TestReusedNameEarnsNothing(t *testing.T) {
	gated(t)
	hub := &fakeHub{devices: []device.Device{lamp()}}
	e, _ := harness(t, hub, lampOn)
	approve(t, e, "device.write:lamp")
	if err := os.Remove(filepath.Join(e.dir, "test.ts")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.SaveSource("test", lampOn, "", false, true); err != nil {
		t.Fatal(err)
	}
	if a := accessOf(t, e)["device.write:lamp"]; a.Approved {
		t.Fatal("a new script inherited the approvals of an old one with the same name")
	}
	if grants, _ := e.store.ScriptGrants(context.Background()); len(grants) != 0 {
		t.Fatalf("still stored: %v", grants)
	}
}

func TestOnlyWhatWasAskedForCanBeApproved(t *testing.T) {
	gated(t)
	e, _ := harness(t, &fakeHub{devices: []device.Device{lamp()}}, lampOn)
	if err := e.SetAccess("test", []string{"device.write:lamp", "device.read:other"}, true); err == nil {
		t.Fatal("approved something nobody asked for")
	}
	if accessOf(t, e)["device.write:lamp"].Approved {
		t.Error("a refused approval changed something")
	}
	if err := e.SetAccess("nope", []string{"device.write:lamp"}, true); err == nil {
		t.Error("approved for an unknown script")
	}
	if err := e.SetAccess("test", nil, true); err == nil {
		t.Error("approved nothing")
	}
}

func TestUnsavedApprovalChangesNothing(t *testing.T) {
	gated(t)
	st := store.NewMemory()
	hub := &fakeHub{devices: []device.Device{lamp()}}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "test.ts"), []byte(lampOn), 0o644); err != nil {
		t.Fatal(err)
	}
	e := New(hub, dir, t.TempDir(), st, time.UTC)
	e.Reload()
	st.Fail = errors.New("database down")
	if err := e.SetAccess("test", []string{"device.write:lamp"}, true); err == nil {
		t.Fatal("reported an approval it could not save")
	}
	st.Fail = nil
	if err := e.SetLive("test", true); err != nil {
		t.Fatal(err)
	}
	e.Tick()
	if len(hub.writes) != 0 {
		t.Fatal("an unsaved approval took effect")
	}
}

func TestUnreadableApprovalsGrantNothing(t *testing.T) {
	gated(t)
	st := store.NewMemory()
	if err := st.SetScriptGrants(context.Background(), "test", []string{"device.write:lamp"}, true); err != nil {
		t.Fatal(err)
	}
	st.Fail = errors.New("database down")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "test.ts"), []byte(lampOn), 0o644); err != nil {
		t.Fatal(err)
	}
	e := New(&fakeHub{devices: []device.Device{lamp()}}, dir, t.TempDir(), st, time.UTC)
	if e.grants["test"]["device.write:lamp"] {
		t.Fatal("approvals appeared from an unreadable store")
	}
}

// What a script could only learn from a refusal is the same for a device that exists and one that doesn't.
func TestUnapprovedDeviceCantBeProbed(t *testing.T) {
	gated(t)
	e, _ := harness(t, &fakeHub{devices: []device.Device{lamp()}}, `
		for (const id of ["lamp", "no-such-device"]) {
			const d = home.device(id);
			home.log([d.id, d.name, d.get("switch"), d.online(), d.offlineSeconds()].join("|"));
			try { d.get("no-such-value"); d.set("no-such-value", 1); home.log("no throw"); } catch (err) { home.log("threw " + err); }
		}`)
	lines := logged(e)
	if !slices.Contains(lines, "lamp|lamp||false|0") || !slices.Contains(lines, "no-such-device|no-such-device||false|0") {
		t.Fatalf("got %v", lines)
	}
	if slices.ContainsFunc(lines, func(l string) bool { return strings.HasPrefix(l, "threw") }) {
		t.Fatalf("got %v", lines)
	}
}

func TestPluginsGoThroughTheGate(t *testing.T) {
	gated(t)
	dir := t.TempDir()
	source := `
		const w = home.plugin("weather");
		const none = home.plugin("no-such-plugin");
		home.reconcile(() => {
			home.log("data " + JSON.stringify(w.data()) + " age " + w.ageSeconds() + " other " + none.data());
			w.action("alert", { text: "cold" });
			w.action("siren");
		});`
	if err := os.WriteFile(filepath.Join(dir, "test.ts"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	e := New(&fakeHub{}, dir, t.TempDir(), store.NewMemory(), time.UTC)
	clock := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return clock }
	plugins := &fakePlugins{data: `{"secret":1}`, at: clock}
	e.Plugins = plugins
	e.Reload()
	if err := e.SetLive("test", true); err != nil {
		t.Fatal(err)
	}
	e.Tick()
	if !slices.Contains(logged(e), "data undefined age undefined other undefined") || len(plugins.acts) != 0 {
		t.Fatalf("got %v and %v", logged(e), plugins.acts)
	}
	approve(t, e, "plugin.action:weather:alert")
	e.Tick()
	if len(plugins.acts) != 1 || !strings.HasPrefix(plugins.acts[0], "weather.alert") {
		t.Fatalf("one action approved, got %v", plugins.acts)
	}
	if slices.Contains(logged(e), `data {"secret":1} age 0 other undefined`) {
		t.Fatal("an action's approval let the data through")
	}
	approve(t, e, "plugin.data:weather")
	e.Tick()
	if !slices.Contains(logged(e), `data {"secret":1} age 0 other undefined`) {
		t.Fatalf("got %v", logged(e))
	}
}

// A script doing its worst: nothing here may reach a device, a plugin or the owner's data, or hide that it tried.
func TestHostileScript(t *testing.T) {
	gated(t)
	hub := &fakeHub{devices: []device.Device{lamp()}}
	dir := t.TempDir()
	source := `
		const g = globalThis as any;
		const leak: string[] = [];
		const attempt = (fn: () => any) => { try { const v = fn(); if (v !== undefined && v !== false) leak.push(String(v)); } catch {} };
		const id = ["la", "mp"].join("");
		attempt(() => eval('home.device("lamp").get("switch")'));
		attempt(() => new Function('return home.device("lamp").get("switch")')());
		attempt(() => (function () {}).constructor('return home.device("' + id + '").set("switch", true)')());
		attempt(() => home.device(id).set("switch", true));
		attempt(() => (home as any).device.call(null, id).set("switch", true));
		attempt(() => { const d = home.device(id) as any; d.id = "other"; d.set = () => {}; });
		attempt(() => home.plugin("weather").data());
		attempt(() => { home.plugin("weather").action("alert"); });
		attempt(() => { (home as any).allow = () => true; });
		attempt(() => { JSON.parse = (() => ({ stolen: true })) as any; return home.plugin("weather").data(); });
		attempt(() => { g.JSON = undefined; });
		attempt(() => { Object.prototype.toString = () => "x"; });
		for (let i = 0; i < 1000; i++) attempt(() => home.device("computed-" + i).get("x"));
		attempt(() => home.device("x".repeat(100000)).get("x"));
		home.reconcile(() => {
			try { home.device(id).set("switch", true); } catch {}
			home.log("leaked: " + leak.join(","));
		});`
	if err := os.WriteFile(filepath.Join(dir, "test.ts"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	e := New(hub, dir, t.TempDir(), store.NewMemory(), time.UTC)
	plugins := &fakePlugins{data: `{"secret":1}`, at: time.Now()}
	e.Plugins = plugins
	e.Reload()
	if err := e.SetLive("test", true); err != nil {
		t.Fatal(err)
	}
	e.Tick()
	if len(hub.writes) != 0 || len(plugins.acts) != 0 {
		t.Fatalf("reached out: %v %v", hub.writes, plugins.acts)
	}
	st := e.Status()
	if !slices.Contains(logged(e), "leaked: ") {
		t.Fatalf("a read got through: %v (%q)", logged(e), st.Scripts[0].Error)
	}
	got := accessOf(t, e)
	if got["device.write:lamp"].Refused == 0 || got["device.read:lamp"].Refused == 0 || got["plugin.data:weather"].Refused == 0 || got["plugin.action:weather:alert"].Refused == 0 {
		t.Errorf("a caught refusal wasn't recorded: %+v", got)
	}
	if len(got) > maxRequests {
		t.Errorf("%d requests kept", len(got))
	}
	for _, a := range got {
		if len(a.Key) > maxKeyLength {
			t.Errorf("a %d byte key was kept", len(a.Key))
		}
	}
}

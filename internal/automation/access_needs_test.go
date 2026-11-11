package automation

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"tomtly/device"
	"tomtly/internal/store"
)

func TestShadowShowsWhatItWouldNeed(t *testing.T) {
	gated(t)
	hub := &fakeHub{devices: []device.Device{lamp()}}
	e, _ := harness(t, hub, lampOn)
	e.Tick()
	if a := accessOf(t, e)["device.write:lamp"]; a.Approved || a.Refused == 0 {
		t.Fatalf("got %+v", a)
	}
	if len(hub.writes) != 0 {
		t.Fatal("a shadow script wrote")
	}
	approve(t, e, "device.write:lamp")
	e.Tick()
	if len(hub.writes) != 0 {
		t.Fatal("an approval made a shadow script write")
	}
	if st := e.Status(); st.Events[0].Kind != "shadow" {
		t.Errorf("once approved it should record what it would do, got %+v", st.Events[0])
	}
}

func TestTrialFindsWhatADailyJobNeeds(t *testing.T) {
	gated(t)
	e, _ := harness(t, &fakeHub{devices: []device.Device{lamp()}}, `
		const plug = home.device("lamp");
		home.daily("08:30", () => plug.set("switch", true));`)
	if _, asked := accessOf(t, e)["device.write:lamp"]; !asked {
		t.Fatal("the page should know before 08:30")
	}
	if len(e.Status().Events) != 0 {
		t.Errorf("a trial isn't activity: %+v", e.Status().Events)
	}
}

func TestNeedsListsAccessAhead(t *testing.T) {
	gated(t)
	hub := &fakeHub{devices: []device.Device{lamp()}}
	dir := t.TempDir()
	source := `home.needs({ read: ["tuya:sensor"], write: ["lamp"], data: ["weather"], actions: { weather: ["alert"] } });
		home.reconcile(() => { if (new Date().getFullYear() === 1999) home.device("lamp").set("switch", true); });`
	if err := os.WriteFile(filepath.Join(dir, "test.ts"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	e := New(hub, dir, t.TempDir(), store.NewMemory(), time.UTC)
	e.Plugins = &fakePlugins{}
	e.Reload()
	got := accessOf(t, e)
	for _, k := range []string{"device.read:tuya:sensor", "device.write:lamp", "plugin.data:weather", "plugin.action:weather:alert"} {
		if a, ok := got[k]; !ok || a.Approved || a.Refused != 0 {
			t.Errorf("%s: %+v", k, a)
		}
	}
	if e.grants["test"]["device.write:lamp"] {
		t.Error("listing something granted it")
	}
	approve(t, e, "device.write:lamp")
	if !accessOf(t, e)["device.write:lamp"].Approved {
		t.Error("what a script lists can be approved")
	}
}

func TestLabelsSayWhatTheyMean(t *testing.T) {
	e := &Engine{devices: map[string]device.Device{"gree:aa": {Name: "Heat pump"}}, Plugins: &fakePlugins{}}
	for k, want := range map[string]string{
		"device.read:gree:aa":         "Read Heat pump (gree:aa)",
		"device.write:gree:aa":        "Change Heat pump (gree:aa)",
		"device.read:nope":            `Read the unknown device "nope"`,
		"plugin.data:weather":         "Read data from the weather plugin",
		"plugin.action:weather:alert": `Run "alert" on the weather plugin`,
		"plugin.action:a:b:c":         `Run "c" on the unknown "a:b" plugin`,
	} {
		if got := e.describe(k); got != want {
			t.Errorf("%s: got %q, want %q", k, got, want)
		}
	}
}

// How the owner's scripts come up after this change: refused at first, and working once what they ask for is approved.
// Writes behind a read appear only after the read is approved, which takes a second approval.
func TestExampleScriptsWorkOnceApproved(t *testing.T) {
	gated(t)
	hub := house()
	e, _ := liveEngine(t, hub)
	settle(e)
	if len(hub.writes) != 0 {
		t.Fatalf("wrote before any approval: %v", hub.writes)
	}
	rounds := 0
	for {
		settle(e)
		waiting := false
		for _, s := range e.Status().Scripts {
			if s.Error != "" {
				t.Fatalf("%s: %s", s.Name, s.Error)
			}
			var keys []string
			for _, a := range s.Access {
				if !a.Approved {
					keys = append(keys, a.Key)
				}
			}
			if len(keys) > 0 {
				waiting = true
				if err := e.SetAccess(s.Name, keys, true); err != nil {
					t.Fatal(err)
				}
			}
		}
		if !waiting {
			break
		}
		if rounds++; rounds > 4 {
			t.Fatal("it keeps asking for more")
		}
	}
	t.Logf("settled after %d rounds of approval", rounds)
	for _, s := range e.Status().Scripts {
		t.Logf("%s asks for %d things", s.Name, len(s.Access))
		if !slices.ContainsFunc(s.Access, func(a Access) bool { return strings.HasPrefix(a.Key, writeDevice+":") && a.Approved }) {
			t.Errorf("%s should have asked to change a device: %+v", s.Name, s.Access)
		}
	}
}

func TestNeedsRefusesNonsense(t *testing.T) {
	gated(t)
	for _, arg := range []string{"null", "42", `"text"`, "undefined", `{ read: 7 }`, `{ actions: 7 }`, `{ read: [{}] }`} {
		e, _ := harness(t, &fakeHub{devices: []device.Device{lamp()}}, `home.needs(`+arg+` as any); home.log("after");`)
		if st := e.Status(); st.Scripts[0].Error == "" && !slices.Contains(logged(e), "after") {
			t.Errorf("%s: neither ran nor failed", arg)
		}
		if got := accessOf(t, e); len(got) > 0 && arg != `{ read: [{}] }` {
			t.Errorf("%s asked for %+v", arg, got)
		}
	}
}

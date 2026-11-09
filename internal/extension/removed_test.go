package extension

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"home-control/internal/store"
)

const removableSource = `
let scans = 0;
defineExtension({
  id: "x", name: "X", version: "1.0.0",
  settings: { key: { label: "Key", type: "secret" } },
  devices: {
    everySeconds: 1,
    poll(ctx) {
      return [
        { id: "lamp", name: "Lamp", product: "L1", values: [{ code: "on", type: "Boolean", writable: true, value: true }] },
        { id: "fan", name: "Fan", values: [{ code: "gone", type: "String", value: ctx.removedDevices().join(",") }] },
      ];
    },
    write() {},
    discover(ctx) {
      scans++;
      return [
        { id: "lamp", name: "Lamp", address: "10.0.0.2" },
        { id: "stranger", address: "10.0.0.9", note: "pair it, scan " + scans },
        { id: "stranger", address: "10.0.0.10" },
      ];
    },
  },
});`

func removableManager(t *testing.T, st store.Store, source string) *Manager {
	t.Helper()
	m := run(t, folder(t, source), map[string]any{"key": "hunter2hunter2"}, func(m *Manager) { m.store = st })
	eventually(t, "devices", func() bool { return len(m.Devices()) > 0 })
	return m
}

func deviceIDs(m *Manager) []string {
	out := []string{}
	for _, d := range m.Devices() {
		out = append(out, d.ID)
	}
	return out
}

func TestARemovedDeviceIsGoneUntilItIsBack(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	m := removableManager(t, st, removableSource)
	if err := m.RemoveDevice(ctx, "x:nothing"); !errors.Is(err, ErrUnknownDevice) {
		t.Fatalf("removing what isn't there = %v", err)
	}
	if err := m.RestoreDevice(ctx, "x:lamp"); !errors.Is(err, ErrUnknownDevice) {
		t.Fatalf("restoring what wasn't removed = %v", err)
	}
	if err := m.RemoveDevice(ctx, "x:lamp"); err != nil {
		t.Fatal(err)
	}
	if got := deviceIDs(m); !slices.Equal(got, []string{"x:fan"}) {
		t.Fatalf("after removing = %v", got)
	}
	if err := m.Write("x:lamp", "on", false); err == nil {
		t.Fatal("a removed device took a command")
	}
	eventually(t, "the extension to be told", func() bool {
		for _, d := range m.Devices() {
			if d.ID == "x:fan" {
				return d.DPs[0].Value == "lamp"
			}
		}
		return false
	})

	again := removableManager(t, st, removableSource) // a restart, with the same database
	if got := deviceIDs(again); !slices.Equal(got, []string{"x:fan"}) {
		t.Fatalf("it came back after a restart: %v", got)
	}
	if err := again.RestoreDevice(ctx, "x:lamp"); err != nil {
		t.Fatal(err)
	}
	if got := deviceIDs(again); len(got) != 2 {
		t.Fatalf("after adding it back = %v", got)
	}
	if err := again.Write("x:lamp", "on", false); err != nil {
		t.Fatalf("a device added back refused a command: %v", err)
	}
	if list, _ := st.RemovedDevices(ctx); len(list) != 0 {
		t.Fatalf("the record stayed: %v", list)
	}
}

func TestARemovalThatCantBeSavedChangesNothing(t *testing.T) {
	st := store.NewMemory()
	m := removableManager(t, st, removableSource)
	st.Fail = errors.New("database down")
	defer func() { st.Fail = nil }()
	if err := m.RemoveDevice(context.Background(), "x:lamp"); err == nil {
		t.Fatal("no error")
	}
	if len(m.Devices()) != 2 {
		t.Fatal("it was hidden with no record, so a restart would bring it back")
	}
}

func TestDiscoverSaysWhatEachDeviceIs(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	m := removableManager(t, st, removableSource)
	if err := m.RemoveDevice(ctx, "x:fan"); err != nil {
		t.Fatal(err)
	}
	got, err := m.Discover(ctx, "x")
	if err != nil || !got.Supported || got.Error != "" {
		t.Fatalf("Discover = %+v, %v", got, err)
	}
	want := []FoundDevice{
		{ID: "x:lamp", Name: "Lamp", Address: "10.0.0.2", State: FoundAdded, Seen: true},
		{ID: "x:stranger", Name: "stranger", Address: "10.0.0.9", Note: "pair it, scan 1", State: FoundNew, Seen: true},
		{ID: "x:fan", Name: "Fan", State: FoundRemoved}, // not found on the network, and still offered back
	}
	if !slices.Equal(got.Devices, want) {
		t.Fatalf("Discover = %+v", got.Devices)
	}
	if err := m.RemoveDevice(ctx, "x:lamp"); err != nil {
		t.Fatal(err)
	}
	got, _ = m.Discover(ctx, "x")
	if got.Devices[0].State != FoundRemoved || !got.Devices[0].Seen {
		t.Fatalf("a removed device on the network = %+v", got.Devices[0])
	}
	if got.Devices[1].Note != "pair it, scan 1" {
		t.Fatalf("opening the page again scanned again: %+v", got.Devices[1])
	}
	if _, err := m.Discover(ctx, "nope"); !errors.Is(err, ErrUnknownDevice) {
		t.Fatalf("an unknown integration = %v", err)
	}
}

func TestDiscoverWithoutAScanStillOffersWhatWasRemoved(t *testing.T) {
	ctx := context.Background()
	m := removableManager(t, store.NewMemory(), strings.ReplaceAll(removableSource, "discover(ctx)", "other(ctx)"))
	if err := m.RemoveDevice(ctx, "x:lamp"); err != nil {
		t.Fatal(err)
	}
	got, err := m.Discover(ctx, "x")
	if err != nil || got.Supported || len(got.Devices) != 1 || got.Devices[0].State != FoundRemoved {
		t.Fatalf("Discover = %+v, %v", got, err)
	}
}

func TestDiscoverFailures(t *testing.T) {
	ctx := context.Background()
	for name, c := range map[string]struct{ body, want string }{
		"a secret":  {`return [{ id: "a", name: ctx.settings.key }];`, "secret"},
		"a throw":   {`throw new Error("no route, key " + ctx.settings.key);`, "no route"},
		"bad shape": {`return [{ id: "a", key: "x" }];`, "list of devices"},
		"no id":     {`return [{ name: "a" }];`, "short id"},
	} {
		t.Run(name, func(t *testing.T) {
			source := strings.Replace(removableSource, `scans++;`, c.body, 1)
			m := removableManager(t, store.NewMemory(), source)
			if err := m.RemoveDevice(ctx, "x:fan"); err != nil {
				t.Fatal(err)
			}
			got, err := m.Discover(ctx, "x")
			if err != nil || !strings.Contains(got.Error, c.want) || strings.Contains(got.Error, "hunter2") {
				t.Fatalf("Discover = %+v, %v", got, err)
			}
			if len(got.Devices) != 1 || got.Devices[0].ID != "x:fan" {
				t.Fatalf("a failed scan lost what was removed: %+v", got.Devices)
			}
		})
	}
}

func TestDiscoverWaitsForNothingWhenStopped(t *testing.T) {
	source := `defineExtension({ id: "x", name: "X", version: "1.0.0", settings: { need: { label: "N", type: "string", required: true } },
		devices: { poll() { return []; }, discover() { return []; } } });`
	m := run(t, folder(t, source), map[string]any{})
	eventually(t, "it to stop", func() bool { return status(m).ID == "x" && !status(m).Running })
	done := time.Now()
	got, err := m.Discover(context.Background(), "x")
	if err != nil || got.Error == "" || time.Since(done) > 5*time.Second {
		t.Fatalf("Discover = %+v, %v after %s", got, err, time.Since(done))
	}
}

func TestXiaomiFindsDevicesAndLeavesRemovedOnesAlone(t *testing.T) {
	ctx := context.Background()
	f := startMiio(t, answerProperties(map[string]any{"2-1": true}))
	m := runXiaomi(t, example(t, "xiaomi"), xiaomiFile(xiaomiDevice("zhimi.airp.mb5")))
	eventually(t, "the device", func() bool { return len(m.Devices()) == 1 })
	got, err := m.Discover(ctx, "xiaomi")
	if err != nil || got.Error != "" || len(got.Devices) != 1 || got.Devices[0].State != FoundAdded || got.Devices[0].Address != "127.0.0.1" || got.Devices[0].Name != "Test device" {
		t.Fatalf("Discover = %+v, %v", got, err)
	}
	if err := m.RemoveDevice(ctx, "xiaomi:"+xiaomiDID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond) // anything already on its way
	before := len(f.methods("get_properties"))
	if before == 0 {
		t.Fatal("it was never read, so this proves nothing")
	}
	time.Sleep(11 * time.Second) // a poll comes every 10
	if after := len(f.methods("get_properties")); after != before {
		t.Fatalf("a removed device was still read: %d more", after-before)
	}
	if err := m.RestoreDevice(ctx, "xiaomi:"+xiaomiDID); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(12 * time.Second); len(f.methods("get_properties")) == before; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("a device added back wasn't read again")
		}
	}
}

func TestXiaomiReportsADeviceItHasNoTokenFor(t *testing.T) {
	startMiio(t, answerProperties(nil))
	m := runXiaomi(t, example(t, "xiaomi"), xiaomiFile(map[string]any{"did": "42", "name": "Other", "model": "zhimi.airp.mb5", "localip": "127.0.0.3", "token": xiaomiToken}))
	eventually(t, "the device", func() bool { return len(m.Devices()) == 1 })
	got, err := m.Discover(context.Background(), "xiaomi")
	if err != nil || got.Error != "" || len(got.Devices) != 1 {
		t.Fatalf("Discover = %+v, %v", got, err)
	}
	d := got.Devices[0]
	if d.ID != "xiaomi:"+xiaomiDID || d.State != FoundNew || !strings.Contains(d.Note, "token") {
		t.Fatalf("found = %+v", d)
	}
}

func TestGreeFindsUnitsAndStopsTalkingToARemovedOne(t *testing.T) {
	ctx := context.Background()
	startFake(t, false)
	m := run(t, sweepable(t), map[string]any{"host": "127.0.0.1"}, allowLoopback)
	eventually(t, "the unit", func() bool { return len(m.Devices()) == 1 })
	got, err := m.Discover(ctx, "gree")
	if err != nil || got.Error != "" || len(got.Devices) != 1 || got.Devices[0].State != FoundAdded || got.Devices[0].ID != "gree:aabbccddeeff" {
		t.Fatalf("Discover = %+v, %v", got, err)
	}
	if err := m.RemoveDevice(ctx, "gree:aabbccddeeff"); err != nil {
		t.Fatal(err)
	}
	if err := m.RestoreDevice(ctx, "gree:aabbccddeeff"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the unit back", func() bool { return len(m.Devices()) == 1 })
}

func TestGreeReportsAnotherUnit(t *testing.T) {
	startFake(t, false)
	m := run(t, sweepable(t), map[string]any{"host": "127.0.0.1", "mac": "000000000000"}, allowLoopback)
	eventually(t, "it to run", func() bool { return status(m).Running && len(m.Devices()) > 0 })
	got, err := m.Discover(context.Background(), "gree")
	if err != nil || got.Error != "" {
		t.Fatalf("Discover = %+v, %v", got, err)
	}
	if len(got.Devices) != 1 || !strings.Contains(got.Devices[0].Note, "one air conditioner") {
		t.Fatalf("found = %+v", got.Devices)
	}
}

func TestTuyaParksARemovedLinkAndReportsStrangers(t *testing.T) {
	ctx := context.Background()
	f := startTuyaFake(t, "3.4", tuyaKey, map[string]map[string]any{"": {"1": true}})
	port := f.port()
	shorter := []string{"const RECONNECT_MS = 10_000;", "const RECONNECT_MS = 50;"}
	m := runTuya(t, tuyaSource(t, port, shorter...), "plug1=127.0.0.1@3.4")
	eventuallyValue(t, m, "plug1", "switch_1", true)

	got, err := m.Discover(ctx, "tuya")
	if err != nil || got.Error != "" {
		t.Fatalf("Discover = %+v, %v", got, err)
	}
	byID := map[string]FoundDevice{}
	for _, d := range got.Devices {
		byID[d.ID] = d
	}
	if d := byID["tuya:plug1"]; d.State != FoundAdded || d.Address != "127.0.0.1" {
		t.Fatalf("the connected device = %+v of %+v", d, got.Devices)
	}
	for _, d := range got.Devices {
		if strings.Contains(d.Name+d.Note+d.Product, tuyaKey) {
			t.Fatalf("a key reached the page: %+v", d)
		}
	}

	dials := len(f.commands())
	if err := m.RemoveDevice(ctx, "tuya:plug1"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2500 * time.Millisecond) // the link closes on the next run, and it would have redialled well within this
	settled := len(f.commands())
	time.Sleep(1500 * time.Millisecond)
	if len(f.commands()) != settled {
		t.Fatalf("it kept dialling a removed device: %d, %d after %d", settled, len(f.commands()), dials)
	}
	if err := m.RestoreDevice(ctx, "tuya:plug1"); err != nil {
		t.Fatal(err)
	}
	tuyaEventually(t, "the link back", func() bool { return len(f.commands()) > settled })
}

// Most addresses on a network don't answer at all, and each of those waits until the end of the sweep.
func TestASweepReachesEveryAddressWhenMostAreSilent(t *testing.T) {
	hosts := make([]string, maxSweepHosts)
	for n := range hosts {
		hosts[n] = fmt.Sprintf("10.0.0.%d", n+1)
	}
	found := sweep(context.Background(), hosts, 200*time.Millisecond, func(ctx context.Context, host string) (string, bool) {
		if strings.HasSuffix(host, "7") {
			return host, true
		}
		<-ctx.Done()
		return "", false
	})
	want := 0
	for _, host := range hosts {
		if strings.HasSuffix(host, "7") {
			want++
		}
	}
	if len(found) != want {
		t.Fatalf("found %d of %d", len(found), want)
	}
}

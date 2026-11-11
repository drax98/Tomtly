package extension

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"tomtly/internal/store"
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

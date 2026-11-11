package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"
)

func TestMemoryContract(t *testing.T) {
	contract(t, NewMemory())
	pinsContract(t, NewMemory())
	overridesContract(t, NewMemory())
	homeContract(t, NewMemory())
	roomsContract(t, NewMemory())
	dashboardContract(t, NewMemory())
}

// dashboardContract: never arranged is nil, arranged empty is empty, and the order is kept.
func dashboardContract(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()
	if got, err := s.Dashboard(ctx); err != nil || got != nil {
		t.Fatalf("never arranged: %v, %v", got, err)
	}
	for _, want := range [][]string{{"script:heat/panel", "builtin/devices"}, {}} {
		if err := s.SetDashboard(ctx, want); err != nil {
			t.Fatal(err)
		}
		if got, err := s.Dashboard(ctx); err != nil || got == nil || !slices.Equal(got, want) {
			t.Fatalf("got %v, want %v (%v)", got, want, err)
		}
	}
}

func roomsContract(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()
	if names, seeded, err := s.Rooms(ctx); err != nil || len(names) != 0 || seeded {
		t.Fatalf("a new store has no rooms and wasn't seeded: %v %v %v", names, seeded, err)
	}
	if err := s.SetRooms(ctx, []string{"Kitchen", "Hall", "Loft"}); err != nil {
		t.Fatal(err)
	}
	kitchen, hall := "Kitchen", "Hall"
	s.SetDeviceOverride(ctx, DeviceOverride{ID: "kettle", Name: "Kettle", Room: &kitchen})
	s.SetDeviceOverride(ctx, DeviceOverride{ID: "lamp", Room: &hall})
	if err := s.RenameRoom(ctx, "Kitchen", "Cooking"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteRoom(ctx, "Hall"); err != nil {
		t.Fatal(err)
	}
	if err := s.RenameRoom(ctx, "Nowhere", "X"); !errors.Is(err, ErrNoRoom) {
		t.Errorf("rename unknown: %v", err)
	}
	if err := s.DeleteRoom(ctx, "Nowhere"); !errors.Is(err, ErrNoRoom) {
		t.Errorf("delete unknown: %v", err)
	}
	names, seeded, err := s.Rooms(ctx)
	if err != nil || !seeded || !slices.Equal(names, []string{"Cooking", "Loft"}) {
		t.Errorf("rooms %v %v %v", names, seeded, err)
	}
	overrides, _ := s.DeviceOverrides(ctx)
	byID := map[string]DeviceOverride{}
	for _, o := range overrides {
		byID[o.ID] = o
	}
	if o := byID["kettle"]; o.Room == nil || *o.Room != "Cooking" || o.Name != "Kettle" {
		t.Errorf("the kettle moved with its room: %+v", o)
	}
	if o := byID["lamp"]; o.Room == nil || *o.Room != "" {
		t.Errorf("the lamp is in no room once its room is gone: %+v", o)
	}
}

// homeContract: empty until set, replaced whole, and a location can be taken away again.
func homeContract(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()
	if got, err := s.HomeSettings(ctx); err != nil || got != (HomeSettings{}) {
		t.Fatalf("not set yet: %+v, %v", got, err)
	}
	lat, lon := 52.23, 21.01
	for _, want := range []HomeSettings{{Language: "pl", Timezone: "Europe/Warsaw", Latitude: &lat, Longitude: &lon}, {Language: "en"}} {
		if err := s.SetHomeSettings(ctx, want); err != nil {
			t.Fatal(err)
		}
		got, err := s.HomeSettings(ctx)
		if err != nil || got.Language != want.Language || got.Timezone != want.Timezone || (got.Latitude == nil) != (want.Latitude == nil) ||
			(want.Latitude != nil && (*got.Latitude != lat || *got.Longitude != lon)) {
			t.Fatalf("got %+v, want %+v (%v)", got, want, err)
		}
	}
}

func overridesContract(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()
	kitchen, none := "Kitchen", ""
	for _, o := range []DeviceOverride{{ID: "b", Name: "Kettle"}, {ID: "a", Room: &kitchen}, {ID: "c", Name: "Old", Room: &none}, {ID: "c"}} {
		if err := s.SetDeviceOverride(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.DeviceOverrides(ctx)
	if err != nil || len(got) != 2 || got[0].ID != "a" || got[0].Name != "" || got[0].Room == nil || *got[0].Room != "Kitchen" ||
		got[1].ID != "b" || got[1].Name != "Kettle" || got[1].Room != nil {
		t.Fatalf("overrides %+v, %v", got, err)
	}
	if err := s.SetDeviceOverride(ctx, DeviceOverride{ID: "a", Room: &none}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.DeviceOverrides(ctx); got[0].Room == nil || *got[0].Room != "" {
		t.Errorf("no room is kept apart from following config.json: %+v", got[0])
	}
}

// pinsContract is what both stores do with pins: replaced whole, kept in order, cleared by none.
func pinsContract(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()
	if got, err := s.DevicePins(ctx); err != nil || len(got) != 0 {
		t.Fatalf("no pins yet: %v, %v", got, err)
	}
	for _, step := range []struct {
		device string
		dps    []string
	}{{"tuya:ac", []string{"2", "1"}}, {"lamp", []string{"5"}}, {"tuya:ac", []string{"3", "2", "1"}}, {"lamp", nil}} {
		if err := s.SetDevicePins(ctx, step.device, step.dps); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.DevicePins(ctx)
	if err != nil || len(got) != 1 || !slices.Equal(got["tuya:ac"], []string{"3", "2", "1"}) {
		t.Errorf("pins %v, %v", got, err)
	}
	got["tuya:ac"][0] = "changed"
	if again, _ := s.DevicePins(ctx); again["tuya:ac"][0] != "3" {
		t.Error("the answer shares memory with the store")
	}
}

func TestPostgresContract(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	p, err := Open(ctx, url, testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if _, err := p.pool.Exec(ctx, `TRUNCATE script_live, script_grants, events, added_extensions, grants, webhook_tokens, violations, extension_storage, packages, market_state, audit, users, device_pins, device_overrides, rooms, rooms_seeded, home_settings, dashboard CASCADE`); err != nil {
		t.Fatal(err)
	}
	contract(t, p)
	dashboardContract(t, p)
	pinsContract(t, p)
	overridesContract(t, p)
	roomsContract(t, p)
	homeContract(t, p)

	// Running the migrations again, and at the same time, changes nothing.
	errs := make(chan error, 3)
	for range 3 {
		go func() { errs <- p.migrate(ctx) }()
	}
	for range 3 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	files, err := os.ReadDir("migrations") // each file is applied once, however often or concurrently migrate runs
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil || n != len(files) {
		t.Fatalf("schema_migrations rows = %d, want %d, err %v", n, len(files), err)
	}
}

// contract is what every Store must do, so Memory and Postgres can't drift apart.
func contract(t *testing.T, s Store) {
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	at := func(i int) time.Time { return time.Date(2026, 1, 1, 0, 0, i, 0, time.UTC) }

	live, err := s.ScriptsLive(ctx)
	must(err)
	if len(live) != 0 {
		t.Fatalf("fresh ScriptsLive = %v", live)
	}
	must(s.SetScriptLive(ctx, "a", true))
	must(s.SetScriptLive(ctx, "b", true))
	must(s.SetScriptLive(ctx, "b", false))
	live, _ = s.ScriptsLive(ctx)
	if len(live) != 2 || !live["a"] || live["b"] {
		t.Fatalf("ScriptsLive = %v", live)
	}

	scriptGrants, err := s.ScriptGrants(ctx)
	must(err)
	if len(scriptGrants) != 0 {
		t.Fatalf("fresh ScriptGrants = %v", scriptGrants)
	}
	must(s.SetScriptGrants(ctx, "a", []string{"device.read:tuya:1", "device.write:tuya:1"}, true))
	must(s.SetScriptGrants(ctx, "a", []string{"device.read:tuya:1"}, true)) // approving twice changes nothing
	must(s.SetScriptGrants(ctx, "b", []string{"plugin.data:w"}, true))
	must(s.SetScriptGrants(ctx, "a", []string{"device.write:tuya:1", "never-granted"}, false))
	must(s.SetScriptGrants(ctx, "b", []string{"plugin.data:w"}, false))
	scriptGrants, _ = s.ScriptGrants(ctx)
	if len(scriptGrants) != 1 || !slices.Equal(scriptGrants["a"], []string{"device.read:tuya:1"}) {
		t.Fatalf("ScriptGrants = %v", scriptGrants)
	}
	must(s.SetScriptGrants(ctx, "a", []string{"device.read:tuya:1"}, false))

	must(s.AppendEvents(ctx, nil))
	must(s.AppendEvents(ctx, []Event{{at(1), "s", "k", "one"}, {at(2), "s", "k", "two"}}))
	must(s.AppendEvents(ctx, []Event{{at(3), "s", "k", "three"}}))
	events, err := s.RecentEvents(ctx, 2)
	must(err)
	if len(events) != 2 || events[0].Message != "two" || events[1].Message != "three" || !events[1].Time.Equal(at(3)) {
		t.Fatalf("RecentEvents(2) = %v", events)
	}
	if events, _ = s.RecentEvents(ctx, 10); len(events) != 3 || events[0].Message != "one" {
		t.Fatalf("RecentEvents(10) = %v", events)
	}
	if events, _ = s.RecentEvents(ctx, 0); len(events) != 0 {
		t.Fatalf("RecentEvents(0) = %v", events)
	}

	added, err := s.AddedExtensions(ctx)
	must(err)
	if len(added) != 0 {
		t.Fatalf("fresh AddedExtensions = %v", added)
	}
	must(s.SaveAddedExtension(ctx, AddedExtension{Source: "x", Settings: map[string]any{"k": "v", "n": 1.0}}))
	must(s.SaveAddedExtension(ctx, AddedExtension{Source: "y", Settings: map[string]any{}}))
	must(s.SaveAddedExtension(ctx, AddedExtension{Source: "x", Settings: map[string]any{"k": "w"}}))
	added, _ = s.AddedExtensions(ctx)
	if len(added) != 2 || added[0].Source != "y" || added[1].Source != "x" || added[1].Settings["k"] != "w" || len(added[1].Settings) != 1 {
		t.Fatalf("AddedExtensions = %v", added)
	}
	must(s.DeleteAddedExtension(ctx, "y"))
	must(s.DeleteAddedExtension(ctx, "missing"))
	if added, _ = s.AddedExtensions(ctx); len(added) != 1 || added[0].Source != "x" {
		t.Fatalf("after delete = %v", added)
	}

	grants, err := s.Grants(ctx, "x")
	must(err)
	if len(grants) != 0 {
		t.Fatalf("fresh Grants = %v", grants)
	}
	must(s.SetGrants(ctx, "x", []string{"net", "fs"}))
	if grants, _ = s.Grants(ctx, "x"); !slices.Equal(grants, []string{"net", "fs"}) {
		t.Fatalf("Grants = %v", grants)
	}
	must(s.SetGrants(ctx, "x", []string{"net"}))
	if grants, _ = s.Grants(ctx, "x"); !slices.Equal(grants, []string{"net"}) {
		t.Fatalf("replaced Grants = %v", grants)
	}
	must(s.SetGrants(ctx, "x", nil))
	if grants, _ = s.Grants(ctx, "x"); len(grants) != 0 {
		t.Fatalf("Grants after nil = %v", grants)
	}

	if _, err := s.WebhookTokenHash(ctx, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("fresh WebhookTokenHash err = %v", err)
	}
	must(s.SetWebhookTokenHash(ctx, "x", "one"))
	must(s.SetWebhookTokenHash(ctx, "x", "two"))
	if hash, err := s.WebhookTokenHash(ctx, "x"); err != nil || hash != "two" {
		t.Fatalf("WebhookTokenHash = %q, %v", hash, err)
	}
	must(s.DeleteWebhookToken(ctx, "x"))
	must(s.DeleteWebhookToken(ctx, "x"))
	if _, err := s.WebhookTokenHash(ctx, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted WebhookTokenHash err = %v", err)
	}

	lamp := RemovedDevice{"x:lamp", "x", "Lamp", "L1"}
	must(s.AddRemovedDevice(ctx, lamp))
	must(s.AddRemovedDevice(ctx, RemovedDevice{"x:fan", "x", "Fan", ""}))
	lamp.Name = "Desk lamp"
	must(s.AddRemovedDevice(ctx, lamp)) // removing again keeps one record, with the name it has now
	removed, err := s.RemovedDevices(ctx)
	must(err)
	if len(removed) != 2 || removed[0].ID != "x:fan" || removed[1] != lamp {
		t.Fatalf("RemovedDevices = %v", removed)
	}
	must(s.DeleteRemovedDevice(ctx, "x:fan"))
	must(s.DeleteRemovedDevice(ctx, "x:fan"))
	if removed, _ = s.RemovedDevices(ctx); len(removed) != 1 {
		t.Fatalf("after delete = %v", removed)
	}

	for i := 1; i <= 3; i++ {
		must(s.RecordViolation(ctx, Violation{at(i), "x", "net", fmt.Sprint("v", i)}))
	}
	must(s.RecordViolation(ctx, Violation{at(9), "other", "net", "o"}))
	vs, err := s.Violations(ctx, "x", 2)
	must(err)
	if len(vs) != 2 || vs[0].Detail != "v3" || vs[1].Detail != "v2" || !vs[0].Time.Equal(at(3)) {
		t.Fatalf("Violations = %v", vs)
	}
	must(s.ClearViolations(ctx, "x"))
	if vs, _ = s.Violations(ctx, "x", 10); len(vs) != 0 {
		t.Fatalf("after clear = %v", vs)
	}
	if vs, _ = s.Violations(ctx, "other", 10); len(vs) != 1 {
		t.Fatalf("other's violations = %v", vs)
	}

	data, err := s.ExtensionStorage(ctx, "x")
	must(err)
	if data == nil || len(data) != 0 {
		t.Fatalf("fresh ExtensionStorage = %#v", data)
	}
	must(s.SetExtensionValue(ctx, "x", "a", json.RawMessage(`{"n":1}`)))
	must(s.SetExtensionValue(ctx, "x", "b", json.RawMessage(`"s"`)))
	must(s.SetExtensionValue(ctx, "x", "b", json.RawMessage(`[true]`)))
	must(s.SetExtensionValue(ctx, "z", "a", json.RawMessage(`1`)))
	data, _ = s.ExtensionStorage(ctx, "x")
	if len(data) != 2 || string(data["a"]) != `{"n": 1}` && string(data["a"]) != `{"n":1}` || string(data["b"]) != `[true]` {
		t.Fatalf("ExtensionStorage = %s", data)
	}
	must(s.DeleteExtensionValue(ctx, "x", "a"))
	if data, _ = s.ExtensionStorage(ctx, "x"); len(data) != 1 {
		t.Fatalf("after value delete = %s", data)
	}
	must(s.DeleteExtensionStorage(ctx, "x"))
	if data, _ = s.ExtensionStorage(ctx, "x"); len(data) != 0 {
		t.Fatalf("after storage delete = %s", data)
	}
	if data, _ = s.ExtensionStorage(ctx, "z"); len(data) != 1 {
		t.Fatalf("z lost its storage: %s", data)
	}

	if _, err := s.Package(ctx, "p"); err != ErrNotFound {
		t.Fatalf("missing Package err = %v", err)
	}
	must(s.SavePackage(ctx, "p", []byte{0, 1, 2, 255}))
	must(s.SavePackage(ctx, "p", []byte{9, 0}))
	if got, err := s.Package(ctx, "p"); err != nil || !bytes.Equal(got, []byte{9, 0}) {
		t.Fatalf("Package = %v, %v", got, err)
	}
	must(s.DeletePackage(ctx, "p"))
	must(s.DeletePackage(ctx, "p"))
	if _, err := s.Package(ctx, "p"); err != ErrNotFound {
		t.Fatalf("deleted Package err = %v", err)
	}

	first, err := s.InstallKey(ctx, []byte("one"))
	if err != nil {
		t.Fatal(err)
	}
	if again, err := s.InstallKey(ctx, []byte("two")); err != nil || !bytes.Equal(again, first) {
		t.Fatalf("the install key changed: %q then %q, %v", first, again, err)
	}

	contractAuth(t, s)
}

// Memory keeps everything, so this only checks Postgres's pruning.
func TestPostgresPrunes(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	p, err := Open(ctx, url, testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if _, err := p.pool.Exec(ctx, `TRUNCATE events, violations, audit`); err != nil {
		t.Fatal(err)
	}
	batch := make([]Event, maxEvents+50)
	for i := range batch {
		batch[i] = Event{time.Now(), "s", "k", fmt.Sprint(i)}
	}
	if err := p.AppendEvents(ctx, batch); err != nil {
		t.Fatal(err)
	}
	events, _ := p.RecentEvents(ctx, maxEvents+100)
	if len(events) != maxEvents || events[0].Message != "50" || events[maxEvents-1].Message != fmt.Sprint(maxEvents+49) {
		t.Fatalf("kept %d events, first %q", len(events), events[0].Message)
	}
	for i := range maxViolations + 5 {
		if err := p.RecordViolation(ctx, Violation{time.Now(), "x", "k", fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.RecordViolation(ctx, Violation{time.Now(), "y", "k", "keep"}); err != nil {
		t.Fatal(err)
	}
	vs, _ := p.Violations(ctx, "x", maxViolations+100)
	other, _ := p.Violations(ctx, "y", 10)
	if len(vs) != maxViolations || vs[0].Detail != fmt.Sprint(maxViolations+4) || len(other) != 1 {
		t.Fatalf("kept %d violations, %d for y", len(vs), len(other))
	}

	for i := range maxAudit + 20 {
		if err := p.AppendAudit(ctx, AuditEntry{Time: time.Now(), User: "u", Action: "a", Detail: fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
	}
	audit, _ := p.RecentAudit(ctx, maxAudit+100)
	if len(audit) != maxAudit || audit[0].Detail != fmt.Sprint(maxAudit+19) || audit[maxAudit-1].Detail != "20" {
		t.Fatalf("kept %d audit rows", len(audit))
	}
}

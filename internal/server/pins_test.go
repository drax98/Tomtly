package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"tomtly/device"
	"tomtly/internal/store"
)

// lampAdapter has one device with three values.
type lampAdapter struct{ fakeAdapter }

func (lampAdapter) Devices() []device.Device {
	return []device.Device{{ID: "lamp", Name: "Lamp", DPs: []device.DP{{DP: "1", Code: "switch"}, {DP: "2", Code: "bright"}, {DP: "3", Code: "watts"}}}}
}

func TestPinsArePerDeviceAndOnlyForValuesItHas(t *testing.T) {
	h := New(&Hub{Adapters: []device.Adapter{&lampAdapter{}}}, &fakeEngine{live: map[string]bool{}}, fakeExtensions{}, seeded(t), t.TempDir(), nil, []string{"home.example"}, nil)
	if w := do(h, "GET", "/api/pins", "", ""); w.Code != 200 || strings.TrimSpace(w.Body.String()) != "{}" {
		t.Fatalf("no pins yet: %d %s", w.Code, w.Body)
	}
	for body, code := range map[string]int{
		`{"dps":["9"]}`:     400, // the device has no value 9
		`{"dps":["1","1"]}`: 400,
		`{"dps":["1","1","1","1","1","1","1","1","1","1","1","1","1"]}`: 400,
		`{"dps":`: 400,
	} {
		if w := do(h, "PUT", "/api/devices/lamp/pins", "application/json", body); w.Code != code {
			t.Errorf("%s: %d %s", body, w.Code, w.Body)
		}
	}
	if w := do(h, "PUT", "/api/devices/nope/pins", "application/json", `{"dps":["1"]}`); w.Code != 404 {
		t.Errorf("unknown device: %d", w.Code)
	}
	if w := do(h, "PUT", "/api/devices/lamp/pins", "application/json", `{"dps":["3","2"]}`); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var pins map[string][]string
	if w := do(h, "GET", "/api/pins", "", ""); json.Unmarshal(w.Body.Bytes(), &pins) != nil || strings.Join(pins["lamp"], ",") != "3,2" {
		t.Errorf("pins %s", w.Body)
	}
	if w := do(h, "PUT", "/api/devices/lamp/pins", "application/json", `{"dps":[]}`); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := do(h, "GET", "/api/pins", "", ""); strings.TrimSpace(w.Body.String()) != "{}" {
		t.Errorf("unpinning everything leaves %s", w.Body)
	}
}

// roomsRig is a service with two devices, lamp in Hall and b in Attic as config.json says, over one store.
func roomsRig(t *testing.T, st store.Store, rooms map[string][]string) http.Handler {
	t.Helper()
	return New(&Hub{Adapters: []device.Adapter{&lampAdapter{}, &fakeAdapter{id: "b"}}}, &fakeEngine{live: map[string]bool{}}, fakeExtensions{}, st, t.TempDir(),
		rooms, []string{"home.example"}, nil)
}

func roomsOf(h http.Handler) string {
	return strings.TrimSpace(do(h, "GET", "/api/rooms", "", "").Body.String())
}

func TestRenameAndMoveDevices(t *testing.T) {
	h := roomsRig(t, seeded(t), map[string][]string{"Hall": {"lamp"}, "Attic": {"b"}})
	lamp := func() map[string]any {
		var out []map[string]any
		json.Unmarshal(do(h, "GET", "/api/devices", "", "").Body.Bytes(), &out)
		for _, d := range out {
			if d["id"] == "lamp" {
				return d
			}
		}
		return nil
	}
	for body, code := range map[string]int{
		`{"name":"` + strings.Repeat("n", 81) + `"}`: 400,
		`{"name":"two\nlines"}`:                      400,
		`{"room":5}`:                                 400,
		`{"room":"` + strings.Repeat("r", 61) + `"}`: 400,
	} {
		if w := do(h, "PATCH", "/api/devices/lamp", "application/json", body); w.Code != code {
			t.Errorf("%s: %d %s", body, w.Code, w.Body)
		}
	}
	if w := do(h, "PATCH", "/api/devices/nope", "application/json", `{"name":"x"}`); w.Code != 404 {
		t.Errorf("unknown device: %d", w.Code)
	}
	if got := roomsOf(h); got != `{"Attic":["b"],"Hall":["lamp"]}` {
		t.Errorf("config.json's rooms come in first: %s", got)
	}
	if w := do(h, "PATCH", "/api/devices/lamp", "application/json", `{"name":"  Desk light ","room":"Office"}`); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if d := lamp(); d["name"] != "Desk light" || d["originalName"] != "Lamp" {
		t.Errorf("renamed: %v", d)
	}
	if got := roomsOf(h); got != `{"Attic":["b"],"Hall":[],"Office":["lamp"]}` {
		t.Errorf("a new room goes at the end, and the old one stays: %s", got)
	}
	do(h, "PATCH", "/api/devices/lamp", "application/json", `{"room":""}`)
	if got := roomsOf(h); got != `{"Attic":["b"],"Hall":[],"Office":[]}` || lamp()["name"] != "Desk light" {
		t.Errorf("no room: %s", got)
	}
	do(h, "PATCH", "/api/devices/lamp", "application/json", `{"name":""}`)
	if d := lamp(); d["name"] != "Lamp" || d["originalName"] != nil {
		t.Errorf("an empty name is the integration's: %v", d)
	}
}

func TestRoomsAreManagedOnThePageAfterTheFirstCopy(t *testing.T) {
	st := seeded(t)
	h := roomsRig(t, st, map[string][]string{"Hall": {"lamp"}, "Attic": {"b"}})
	for method, path := range map[string]string{"POST": "/api/rooms", "PATCH": "/api/rooms/Hall"} {
		for _, body := range []string{`{"name":""}`, `{"name":"a\tb"}`, `{"name":"` + strings.Repeat("r", 61) + `"}`} {
			if w := do(h, method, path, "application/json", body); w.Code != 400 {
				t.Errorf("%s %s %s: %d", method, path, body, w.Code)
			}
		}
	}
	if w := do(h, "POST", "/api/rooms", "application/json", `{"name":"Hall"}`); w.Code != 409 {
		t.Errorf("a second Hall: %d", w.Code)
	}
	if w := do(h, "POST", "/api/rooms", "application/json", `{"name":"Kitchen"}`); w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"Attic":["b"],"Hall":["lamp"],"Kitchen":[]}` {
		t.Fatalf("add: %d %s", w.Code, w.Body)
	}
	if w := do(h, "PATCH", "/api/rooms/Kitchen", "application/json", `{"position":0}`); strings.TrimSpace(w.Body.String()) != `{"Kitchen":[],"Attic":["b"],"Hall":["lamp"]}` {
		t.Errorf("move to the top: %s", w.Body)
	}
	if w := do(h, "PATCH", "/api/rooms/Hall", "application/json", `{"name":"Attic"}`); w.Code != 409 {
		t.Errorf("rename onto another room: %d", w.Code)
	}
	if w := do(h, "PATCH", "/api/rooms/Hall", "application/json", `{"name":"Living room","position":99}`); strings.TrimSpace(w.Body.String()) != `{"Kitchen":[],"Attic":["b"],"Living room":["lamp"]}` {
		t.Errorf("rename keeps the devices: %s", w.Body)
	}
	if w := do(h, "DELETE", "/api/rooms/Attic", "", ""); strings.TrimSpace(w.Body.String()) != `{"Kitchen":[],"Living room":["lamp"]}` {
		t.Errorf("delete: %s", w.Body)
	}
	for _, r := range []struct{ method, path string }{{"DELETE", "/api/rooms/Nowhere"}, {"PATCH", "/api/rooms/Nowhere"}} {
		if w := do(h, r.method, r.path, "application/json", `{}`); w.Code != 404 {
			t.Errorf("%s %s: %d", r.method, r.path, w.Code)
		}
	}
	// A restart with config.json changed doesn't undo any of it: the copy is made once.
	again := roomsRig(t, st, map[string][]string{"Garage": {"b"}})
	if got := roomsOf(again); got != `{"Kitchen":[],"Living room":["lamp"]}` {
		t.Errorf("after a restart: %s", got)
	}
	if w := do(again, "PATCH", "/api/rooms/Living%20room", "application/json", `{"name":"Lounge"}`); w.Code != 200 {
		t.Errorf("a room with a space in its name: %d %s", w.Code, w.Body)
	}
}

func TestTheFirstCopyKeepsConfigOrder(t *testing.T) {
	h := New(&Hub{Adapters: []device.Adapter{&lampAdapter{}, &fakeAdapter{id: "b"}}}, &fakeEngine{live: map[string]bool{}}, fakeExtensions{}, seeded(t), t.TempDir(),
		map[string][]string{"Living room": {"lamp"}, "Bedroom": {"b"}, "Attic": {}}, []string{"home.example"}, nil, RoomOrder([]string{"Living room", "Bedroom"}))
	if got := roomsOf(h); got != `{"Living room":["lamp"],"Bedroom":["b"],"Attic":[]}` {
		t.Errorf("got %s", got)
	}
}

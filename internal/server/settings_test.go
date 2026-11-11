package server

import (
	"encoding/json"
	"testing"

	"tomtly/device"
	"tomtly/internal/store"
)

func TestHomeSettingsAreCheckedAndKept(t *testing.T) {
	h := New(&Hub{Adapters: []device.Adapter{&fakeAdapter{}}}, &fakeEngine{live: map[string]bool{}}, fakeExtensions{}, seeded(t), t.TempDir(), nil, []string{"home.example"}, nil)
	for body, code := range map[string]int{
		`{"language":"english"}`:                        400,
		`{"timezone":"Mars/Olympus"}`:                   400,
		`{"timezone":"Local"}`:                          400,
		`{"latitude":52}`:                               400,
		`{"latitude":91,"longitude":0}`:                 400,
		`{"latitude":0,"longitude":-181}`:               400,
		`{"language":"pt-BR","timezone":"UTC"}`:         200,
		`{"language":"","timezone":"","latitude":null}`: 200,
	} {
		if w := do(h, "PUT", "/api/settings", "application/json", body); w.Code != code {
			t.Errorf("%s: %d %s", body, w.Code, w.Body)
		}
	}
	if w := do(h, "PUT", "/api/settings", "application/json", `{"language":"pl","timezone":"Europe/Warsaw","latitude":52.23,"longitude":21.01}`); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var got store.HomeSettings
	if w := do(h, "GET", "/api/settings", "", ""); json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Language != "pl" || got.Timezone != "Europe/Warsaw" || got.Longitude == nil || *got.Longitude != 21.01 {
		t.Errorf("settings %s", w.Body)
	}
}

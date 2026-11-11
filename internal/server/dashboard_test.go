package server

import (
	"encoding/json"
	"strings"
	"testing"

	"tomtly/device"
)

func TestDashboardKeepsWidgetsInOrderAndRunsOnlyShownButtons(t *testing.T) {
	engine := &fakeEngine{live: map[string]bool{}}
	h := New(&Hub{Adapters: []device.Adapter{&lampAdapter{}}}, engine, fakeExtensions{}, seeded(t), t.TempDir(), nil, []string{"home.example"}, nil)
	var got struct {
		Layout  []string `json:"layout"`
		Widgets []struct {
			Key        string `json:"key"`
			Practising bool   `json:"practising"`
		} `json:"widgets"`
	}
	if w := do(h, "GET", "/api/widgets", "", ""); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Layout == nil || len(got.Layout) != 0 {
		t.Fatalf("a new Home is empty, not missing: %d %s", w.Code, w.Body)
	}
	if len(got.Widgets) != 1 || got.Widgets[0].Key != "script:heat/panel" || !got.Widgets[0].Practising {
		t.Errorf("the catalogue lists what scripts offer: %+v", got.Widgets)
	}
	for body, code := range map[string]int{
		`{"widgets":["builtin/devices","builtin/devices"]}`:                   400,
		`{"widgets":["nowhere/x"]}`:                                           400,
		`{"widgets":["script:heat/1panel"]}`:                                  400,
		`{"widgets":["ext:Weather/today"]}`:                                   400,
		`{"widgets":[` + strings.Repeat(`"builtin/a",`, 30) + `"builtin/b"]}`: 400,
	} {
		if w := do(h, "PUT", "/api/dashboard", "application/json", body); w.Code != code {
			t.Errorf("%s: %d %s", body, w.Code, w.Body)
		}
	}
	if w := do(h, "PUT", "/api/dashboard", "application/json", `{"widgets":["script:heat/panel","builtin/needs-you","ext:weather/today"]}`); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := do(h, "GET", "/api/widgets", "", ""); json.Unmarshal(w.Body.Bytes(), &got) != nil || strings.Join(got.Layout, ",") != "script:heat/panel,builtin/needs-you,ext:weather/today" {
		t.Errorf("order kept, and a widget whose extension is gone stays placed: %s", w.Body)
	}

	if w := do(h, "POST", "/api/widgets/script:heat/panel/actions/go", "application/json", `{"input":{"temp":21}}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"live":false`) {
		t.Errorf("press: %d %s", w.Code, w.Body)
	}
	if engine.pressed != `{"temp":21}` {
		t.Errorf("the script gets what the inputs held: %q", engine.pressed)
	}
	for path, code := range map[string]int{
		"/api/widgets/script:heat/panel/actions/stop":  404, // no such button
		"/api/widgets/ext:weather/today/actions/go":    404,
		"/api/widgets/builtin/devices/actions/go":      404, // the page draws these; nothing to run
		"/api/widgets/script:heat/panel/actions/a:b":   404,
		"/api/widgets/script:heat/panel/actions/go?x=": 200,
	} {
		if w := do(h, "POST", path, "application/json", `{}`); w.Code != code {
			t.Errorf("%s: %d %s", path, w.Code, w.Body)
		}
	}
	for _, body := range []string{`{"input":[1]}`, `{"input":"x"}`, `{"input":{"x":"` + strings.Repeat("a", 4200) + `"}}`} {
		if w := do(h, "POST", "/api/widgets/script:heat/panel/actions/go", "application/json", body); w.Code != 400 {
			t.Errorf("input %.30s: %d", body, w.Code)
		}
	}
}

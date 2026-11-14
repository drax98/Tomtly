package automation

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"tomtly/device"
)

const heaterPanel = `
const lamp = home.device("lamp");
home.widget("panel", {
  title: "Lamp",
  render: () => [
    { type: "stat", label: "Watts", value: lamp.get("watts"), unit: "W" },
    { type: "toggle", name: "on", label: "On" },
    { type: "button", label: "Set", action: "apply" },
  ],
  actions: { apply: (input) => lamp.set("switch", input.on === true) },
});`

func widgetsOf(e *Engine) map[string]string {
	out := map[string]string{}
	for _, w := range e.Widgets() {
		out[w.Key] = string(w.Blocks) + w.Error
	}
	return out
}

func TestScriptWidgetDrawsAndItsButtonFollowsShadowMode(t *testing.T) {
	approveAll.Store(true)
	defer approveAll.Store(false)
	hub := &fakeHub{devices: []device.Device{lamp()}}
	e, clock := harness(t, hub, heaterPanel)
	e.Tick()
	got := widgetsOf(e)["script:test/panel"]
	if !strings.Contains(got, `"label":"Watts","value":5,"unit":"W"`) {
		t.Fatalf("drawn: %s", got)
	}
	if ws := e.Widgets(); !ws[0].Practising || ws[0].From != "test" || ws[0].Title != "Lamp" {
		t.Errorf("widget: %+v", ws[0])
	}

	live, err := e.WidgetAction("test", "panel", "apply", json.RawMessage(`{"on":true}`))
	if err != nil || live || len(hub.writes) != 0 {
		t.Fatalf("practising sends nothing: %v %v %v", live, err, hub.writes)
	}
	if !hasEvent(e, "shadow", "would set Lamp switch = true") || !hasEvent(e, "log", "Lamp: apply pressed") {
		t.Errorf("the press and what it would do are in the log: %+v", e.Status().Events)
	}

	if err := e.SetLive("test", true); err != nil {
		t.Fatal(err)
	}
	if live, err := e.WidgetAction("test", "panel", "apply", json.RawMessage(`{"on":true}`)); err != nil || !live || len(hub.writes) != 1 {
		t.Fatalf("live sends it: %v %v %v", live, err, hub.writes)
	}

	for _, c := range [][3]string{{"test", "panel", "nope"}, {"test", "other", "apply"}, {"gone", "panel", "apply"}} {
		if _, err := e.WidgetAction(c[0], c[1], c[2], json.RawMessage(`{}`)); !errors.Is(err, ErrNoWidget) {
			t.Errorf("%v: %v", c, err)
		}
	}

	hub.setDP("lamp", "2", 7.0)
	*clock = clock.Add(time.Second)
	e.Tick()
	if !strings.Contains(widgetsOf(e)["script:test/panel"], `"value":5`) {
		t.Error("drawn again only every few seconds")
	}
	*clock = clock.Add(widgetEvery)
	e.Tick()
	if !strings.Contains(widgetsOf(e)["script:test/panel"], `"value":7`) {
		t.Error("and then it shows the new value")
	}
}

func hasEvent(e *Engine, kind, message string) bool {
	for _, ev := range e.Status().Events {
		if ev.Kind == kind && ev.Message == message {
			return true
		}
	}
	return false
}

func TestScriptWidgetMistakes(t *testing.T) {
	hub := &fakeHub{devices: []device.Device{lamp()}}
	for source, want := range map[string]string{
		`home.widget("1x", { title: "A", render: () => [] })`:                                                    "needs a key",
		`home.widget(undefined, { title: "A", render: () => [] })`:                                               "needs a key",
		`home.widget("a", { render: () => [] })`:                                                                 "needs a title",
		`home.widget("a", { title: "A" })`:                                                                       "render function",
		`home.widget("a", { title: "A", render: () => [] }); home.widget("a", { title: "B", render: () => [] })`: "already a widget",
		`home.widget("a", { title: "A", render: () => [], actions: { go: 1 } })`:                                 "must be a function",
		`home.widget("a", { title: "A", device: 7, render: () => [] })`:                                          "device is a device id",
		`home.widget("a", { title: "A", device: "x".repeat(201), render: () => [] })`:                            "device is a device id",
	} {
		e, _ := harness(t, hub, source)
		if st := e.Status(); len(st.Scripts) != 1 || !strings.Contains(st.Scripts[0].Error, want) {
			t.Errorf("%s: %+v", source, st.Scripts)
		}
	}

	// A render that goes wrong shows on the widget, not in the log, and the script keeps running.
	e, _ := harness(t, hub, `home.widget("a", { title: "A", render: () => [{ type: "html", text: "<b>" }] });
home.widget("b", { title: "B", render: () => { throw new Error("boom") } });`)
	e.Tick()
	got := widgetsOf(e)
	if !strings.Contains(got["script:test/a"], `unknown type "html"`) || !strings.Contains(got["script:test/b"], "boom") {
		t.Errorf("errors: %v", got)
	}
	for _, ev := range e.Status().Events {
		if ev.Kind == "error" {
			t.Errorf("a broken widget logged %q", ev.Message)
		}
	}
}

func TestScriptWidgetBelongsToADevice(t *testing.T) {
	e, _ := harness(t, &fakeHub{}, `home.widget("a", { title: "A", device: "tuya:abc", render: () => [] });
home.widget("b", { title: "B", render: () => [] });`)
	e.Tick()
	ws := e.Widgets()
	if len(ws) != 2 || ws[0].Device != "tuya:abc" || ws[1].Device != "" {
		t.Fatalf("widgets: %+v", ws)
	}
	if out, _ := json.Marshal(ws[1]); strings.Contains(string(out), "device") {
		t.Errorf("a widget without a device doesn't say one: %s", out)
	}
}

// A button's access is asked for when the script is saved, not first when someone presses it.
func TestWidgetAccessIsListedUpFront(t *testing.T) {
	hub := &fakeHub{devices: []device.Device{lamp()}}
	e, _ := harness(t, hub, heaterPanel)
	keys := map[string]bool{}
	for _, a := range e.Status().Scripts[0].Access {
		keys[a.Key] = true
	}
	if !keys["device.read:lamp"] || !keys["device.write:lamp"] {
		t.Errorf("access: %+v", e.Status().Scripts[0].Access)
	}
}

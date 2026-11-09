package extension

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"home-control/internal/widget"
)

const counter = `
let count = 0;
defineExtension({
  id: "counter", name: "Counter", version: "1.0.0",
  settings: { key: { label: "Key", type: "secret" } },
  actions: { add: (ctx, args) => { count += args.by; }, leak: () => {} },
  widgets: {
    total: { title: "Total", render: () => [
      { type: "stat", label: "Count", value: count },
      { type: "number", name: "by", label: "By" },
      { type: "button", label: "Add", action: "add" },
    ] },
    secret: { title: "Oops", render: (ctx) => [{ type: "text", text: ctx.settings.key }] },
  },
});`

func widgetMap(m *Manager) map[string]widget.Rendered {
	out := map[string]widget.Rendered{}
	for _, w := range m.Widgets() {
		out[w.Key] = w
	}
	return out
}

func TestExtensionWidgetsDrawAndPressOnlyTheirButtons(t *testing.T) {
	m := run(t, folder(t, counter), map[string]any{"key": "hunter2-very-secret"})
	eventually(t, "the widgets", func() bool { return len(widgetMap(m)["ext:counter/total"].Blocks) > 0 })
	got := widgetMap(m)
	if w := got["ext:counter/total"]; w.Title != "Total" || w.From != "Counter" || !strings.Contains(string(w.Blocks), `"value":0`) {
		t.Errorf("total: %+v %s", w, w.Blocks)
	}
	if w := got["ext:counter/secret"]; len(w.Blocks) != 0 || !strings.Contains(w.Error, "secret") || strings.Contains(w.Error, "hunter2") {
		t.Errorf("a widget can't show a secret: %+v", w)
	}
	if s := status(m); !strings.Contains(strings.Join(s.Provides, ","), "widgets") || len(s.Widgets) != 2 || s.Widgets[1] != (WidgetInfo{"total", "Total"}) {
		t.Errorf("status: %v %+v", s.Provides, s.Widgets)
	}

	if err := m.WidgetAction("counter", "total", "add", json.RawMessage(`{"by":3}`)); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the new count", func() bool { return strings.Contains(string(widgetMap(m)["ext:counter/total"].Blocks), `"value":3`) })
	for _, c := range [][3]string{{"counter", "total", "leak"}, {"counter", "nope", "add"}, {"other", "total", "add"}} {
		if err := m.WidgetAction(c[0], c[1], c[2], json.RawMessage(`{}`)); !errors.Is(err, ErrNoWidget) {
			t.Errorf("%v: %v", c, err)
		}
	}
}

func TestExtensionWidgetMistakes(t *testing.T) {
	for want, def := range map[string]string{
		"a key is":         `widgets: { "1x": { title: "A", render() { return [] } } }`,
		"needs a title":    `widgets: { a: { render() { return [] } } }`,
		"render function":  `widgets: { a: { title: "A" } }`,
		"takes { title":    `widgets: { a: 1 }`,
		"up to 10 widgets": `widgets: {` + strings.Repeat(`a: { title: "A", render() { return [] } },`, 1) + `b1:{title:"A",render(){return []}},b2:{title:"A",render(){return []}},b3:{title:"A",render(){return []}},b4:{title:"A",render(){return []}},b5:{title:"A",render(){return []}},b6:{title:"A",render(){return []}},b7:{title:"A",render(){return []}},b8:{title:"A",render(){return []}},b9:{title:"A",render(){return []}},b10:{title:"A",render(){return []}} }`,
	} {
		_, err := load(folder(t, `defineExtension({id:"a",name:"a",version:"1",`+def+`});`))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
	}
	if _, err := load(folder(t, `defineExtension({id:"a",name:"a",version:"1",widgets:{a:{title:"A",render(){return []}}}});`)); err != nil {
		t.Errorf("widgets alone make an extension: %v", err)
	}
}

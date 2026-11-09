package extension

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/dop251/goja"

	"home-control/internal/widget"
)

const (
	widgetEvery    = 15 * time.Second // how often an extension's widgets are drawn again
	maxWidgets     = 10
	maxWidgetTitle = 60
)

// WidgetInfo is a widget an extension offers for Home.
type WidgetInfo struct {
	Key   string `json:"key"`
	Title string `json:"title"`
}

type extWidget struct {
	title  string
	render goja.Callable
}

type widgetState struct {
	rendered json.RawMessage
	err      string
}

// defineWidgets reads widgets: { key: { title, render(ctx) } }. A button on one calls the extension's own actions.
func (l *loaded) defineWidgets(definition *goja.Object) error {
	section, ok := definition.Get("widgets").(*goja.Object)
	if !ok {
		return nil
	}
	keys := section.Keys()
	if len(keys) > maxWidgets {
		return fmt.Errorf("an extension can offer up to %d widgets", maxWidgets)
	}
	l.widgets = map[string]extWidget{}
	for _, key := range keys {
		if !widget.Name.MatchString(key) {
			return fmt.Errorf("widget %q: a key is letters, digits, - and _, starting with a letter", key)
		}
		def, ok := section.Get(key).(*goja.Object)
		if !ok {
			return fmt.Errorf("widget %s takes { title, render }", key)
		}
		title, _ := widget.Export(def.Get("title")).(string)
		if title == "" || utf8.RuneCountInString(title) > maxWidgetTitle {
			return fmt.Errorf("widget %s needs a title of up to %d characters", key, maxWidgetTitle)
		}
		render, ok := goja.AssertFunction(def.Get("render"))
		if !ok {
			return fmt.Errorf("widget %s needs a render function", key)
		}
		l.widgets[key] = extWidget{title, render}
	}
	return nil
}

func (l *loaded) widgetInfo() []WidgetInfo {
	out := []WidgetInfo{}
	for _, key := range sorted(l.widgets) {
		out = append(out, WidgetInfo{key, l.widgets[key].title})
	}
	return out
}

// renderWidgets draws every widget on the extension's own thread, with the same size and secret checks as its data.
func (i *instance) renderWidgets(l *loaded, api goja.Value) {
	states := map[string]widgetState{}
	for _, key := range sorted(l.widgets) {
		value, err := i.call(l, l.widgets[key].render, api)
		var text string
		if err == nil {
			text, err = i.output(l, value)
		}
		var blocks json.RawMessage
		if err == nil {
			blocks, err = widget.Clean([]byte(text))
		}
		if err != nil {
			states[key] = widgetState{err: i.scrub(plainError(err))}
			continue
		}
		states[key] = widgetState{rendered: blocks}
	}
	i.mu.Lock()
	i.widgets = states
	i.mu.Unlock()
}

// Widgets is every running extension's widgets as they were last drawn.
func (m *Manager) Widgets() []widget.Rendered {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := []widget.Rendered{}
	for _, inst := range m.instances {
		inst.mu.RLock()
		status, states := inst.status, inst.widgets
		inst.mu.RUnlock()
		if !status.Running || status.ID == "" {
			continue
		}
		for _, w := range status.Widgets {
			state := states[w.Key]
			out = append(out, widget.Rendered{Key: "ext:" + status.ID + "/" + w.Key, Title: w.Title, From: status.Name, Blocks: state.rendered, Error: state.err})
		}
	}
	return out
}

// ErrNoWidget is a widget or button that isn't there now.
var ErrNoWidget = errors.New("that widget or button isn't there any more")

// WidgetAction asks the extension to run the action a button on its widget names, with what the widget's inputs held.
// Like a script's call, it waits in line on the extension's thread, and the widget is drawn again once it has run.
func (m *Manager) WidgetAction(id, key, action string, input json.RawMessage) error {
	m.mu.RLock()
	var state widgetState
	found := false
	for _, inst := range m.instances {
		inst.mu.RLock()
		if inst.status.ID == id && inst.status.Running {
			state, found = inst.widgets[key]
		}
		inst.mu.RUnlock()
	}
	m.mu.RUnlock()
	if !found || !widget.HasAction(state.rendered, action) {
		return ErrNoWidget
	}
	return m.plugins.Act(id, action, input)
}

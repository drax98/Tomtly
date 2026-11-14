package automation

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/dop251/goja"

	"tomtly/internal/widget"
)

const (
	widgetEvery      = 5 * time.Second // how often a script's widgets are drawn again
	maxScriptWidgets = 10
	maxWidgetTitle   = 60
	maxWidgetDevice  = 200
)

// scriptWidget is one widget a script made with home.widget. Its blocks are drawn on the engine's tick and kept, so
// how many pages are open never decides how often script code runs.
type scriptWidget struct {
	key, title string
	device     string // a device id: the widget shows on that device's page, and isn't offered for Home
	render     goja.Callable
	actions    map[string]goja.Callable
	rendered   json.RawMessage
	err        string
}

// defineWidget is home.widget(key, { title, device, render, actions }).
func (e *Engine) defineWidget(s *script, call goja.FunctionCall) {
	key, _ := call.Argument(0).Export().(string)
	if !widget.Name.MatchString(key) {
		panic(scriptError(s.vm, "home.widget needs a key first: letters, digits, - and _, starting with a letter"))
	}
	for _, w := range s.widgets {
		if w.key == key {
			panic(scriptError(s.vm, fmt.Sprintf("there is already a widget called %q", key)))
		}
	}
	if len(s.widgets) >= maxScriptWidgets {
		panic(scriptError(s.vm, fmt.Sprintf("a script can make up to %d widgets", maxScriptWidgets)))
	}
	def, ok := call.Argument(1).(*goja.Object)
	if !ok {
		panic(scriptError(s.vm, "home.widget takes { title, render }"))
	}
	title, _ := widget.Export(def.Get("title")).(string)
	if title == "" || utf8.RuneCountInString(title) > maxWidgetTitle {
		panic(scriptError(s.vm, fmt.Sprintf("a widget needs a title of up to %d characters", maxWidgetTitle)))
	}
	dev, ok := widgetDevice(def.Get("device"))
	if !ok {
		panic(scriptError(s.vm, fmt.Sprintf("a widget's device is a device id of up to %d characters", maxWidgetDevice)))
	}
	render, ok := goja.AssertFunction(def.Get("render"))
	if !ok {
		panic(scriptError(s.vm, "a widget needs a render function"))
	}
	w := &scriptWidget{key: key, title: title, device: dev, render: render, actions: map[string]goja.Callable{}}
	if section, ok := def.Get("actions").(*goja.Object); ok {
		for _, name := range section.Keys() {
			fn, ok := goja.AssertFunction(section.Get(name))
			if !ok || !widget.Name.MatchString(name) {
				panic(scriptError(s.vm, fmt.Sprintf("action %q must be a function, named with letters, digits, - and _", name)))
			}
			w.actions[name] = fn
		}
	}
	s.widgets = append(s.widgets, w)
}

// widgetDevice reads a widget's optional device: missing, or a string id.
func widgetDevice(v goja.Value) (string, bool) {
	raw := widget.Export(v)
	if raw == nil {
		return "", true
	}
	id, ok := raw.(string)
	return id, ok && utf8.RuneCountInString(id) <= maxWidgetDevice
}

// renderWidgets draws a script's widgets again. A render that fails keeps its error on the widget, not in the log, so a
// broken widget doesn't fill the activity log every few seconds.
func (e *Engine) renderWidgets(s *script) {
	for _, w := range s.widgets {
		var out goja.Value
		err := e.guard(s, func() error {
			var err error
			out, err = w.render(goja.Undefined())
			return err
		})
		var text string
		if err == nil {
			text, err = stringify(s.vm, out)
		}
		var blocks json.RawMessage
		if err == nil {
			blocks, err = widget.Clean([]byte(text))
		}
		if err != nil {
			w.err = err.Error()
			continue
		}
		w.rendered, w.err = blocks, ""
	}
}

func stringify(vm *goja.Runtime, v goja.Value) (string, error) {
	fn, ok := goja.AssertFunction(vm.Get("JSON").ToObject(vm).Get("stringify"))
	if !ok {
		return "", errors.New("JSON.stringify was replaced")
	}
	out, err := fn(goja.Undefined(), v)
	if err != nil {
		return "", err
	}
	if goja.IsUndefined(out) {
		return "null", nil
	}
	return out.String(), nil
}

// Widgets is every script's widgets as they were last drawn.
func (e *Engine) Widgets() []widget.Rendered {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := []widget.Rendered{}
	for _, s := range e.scripts {
		for _, w := range s.widgets {
			out = append(out, widget.Rendered{Key: "script:" + s.name + "/" + w.key, Title: w.title, From: s.name, Device: w.device,
				Blocks: w.rendered, Error: w.err, Practising: !e.live[s.name]})
		}
	}
	return out
}

// ErrNoWidget is a widget or action that isn't there now.
var ErrNoWidget = errors.New("that widget or button isn't there any more")

// WidgetAction runs what a button on a script's widget does, with what its inputs held, and draws the widget again.
// It goes through the same approvals and shadow mode as everything else the script does. It answers whether the
// script is live, so the page can say that a practising one only logged what it would do.
func (e *Engine) WidgetAction(scriptName, key, action string, input json.RawMessage) (live bool, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, s := range e.scripts {
		if s.name != scriptName {
			continue
		}
		for _, w := range s.widgets {
			fn, ok := w.actions[action]
			if w.key != key || !ok || !widget.HasAction(w.rendered, action) {
				continue
			}
			e.refreshDevices()
			e.record(s.name, "log", fmt.Sprintf("%s: %s pressed", w.title, action))
			err := e.guard(s, func() error {
				arg, err := parseInto(s.vm, string(input))
				if err != nil {
					return err
				}
				_, err = fn(goja.Undefined(), arg)
				return err
			})
			e.renderWidgets(s)
			if err != nil {
				e.record(s.name, "error", err.Error())
				return e.live[s.name], fmt.Errorf("the script failed: %w", err)
			}
			return e.live[s.name], nil
		}
	}
	return false, ErrNoWidget
}

func parseInto(vm *goja.Runtime, text string) (goja.Value, error) {
	parse, ok := goja.AssertFunction(vm.Get("JSON").ToObject(vm).Get("parse"))
	if !ok {
		return nil, errors.New("JSON.parse was replaced")
	}
	return parse(goja.Undefined(), vm.ToValue(text))
}

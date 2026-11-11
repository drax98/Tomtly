package automation

import (
	"fmt"
	"log/slog"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/dop251/goja"

	"tomtly/device"
)

var dailyFormat = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// homeObject builds the `home` global a script sees. Keep sdk/home.d.ts in step with it. Everything here that reaches
// outside the VM asks e.allow first. Asking for a device or plugin is not itself access: most scripts do it on their
// first lines, and an unapproved one has to get a handle whose calls are refused, or the whole script would stop.
// That handle also looks the same whether the device exists, so a script can't test ids it wasn't approved for.
func (e *Engine) homeObject(s *script) map[string]any {
	return map[string]any{
		"device": func(id string) map[string]any {
			if _, ok := e.devices[id]; !ok && e.approved(s, key(readDevice, id), key(writeDevice, id)) {
				panic(scriptError(s.vm, fmt.Sprintf("unknown device %q", id)))
			}
			return e.deviceObject(s, id)
		},
		"plugin": func(id string) map[string]any {
			known := false
			if e.Plugins != nil {
				_, _, known = e.Plugins.Get(id)
			}
			if !known && e.pluginApproved(s, id) {
				panic(scriptError(s.vm, fmt.Sprintf("unknown plugin %q: add it to plugins in config.json", id)))
			}
			return e.pluginObject(s, id)
		},
		"reconcile": func(fn goja.Callable) {
			if fn == nil { // null arrives as a nil function, and calling that would take the service down
				panic(scriptError(s.vm, "home.reconcile takes a function"))
			}
			s.reconciles = append(s.reconciles, fn)
		},
		"daily": func(at string, fn goja.Callable) {
			if !dailyFormat.MatchString(at) {
				panic(scriptError(s.vm, fmt.Sprintf("daily time %q must look like 08:30", at)))
			}
			if fn == nil {
				panic(scriptError(s.vm, "home.daily takes a function"))
			}
			s.dailies = append(s.dailies, &dailyJob{at: at, fn: fn})
		},
		"widget": func(call goja.FunctionCall) goja.Value {
			e.defineWidget(s, call)
			return goja.Undefined()
		},
		"log": func(message string) {
			if e.allowLog(s) {
				e.record(s.name, "log", message)
			}
		},
		"needs": func(call goja.FunctionCall) goja.Value {
			list, ok := call.Argument(0).(*goja.Object)
			if !ok {
				panic(scriptError(s.vm, "home.needs takes an object"))
			}
			e.needs(s, list)
			return goja.Undefined()
		},
	}
}

// approved answers without recording anything, since asking for a handle isn't a call to refuse.
func (e *Engine) approved(s *script, keys ...string) bool {
	return approveAll.Load() || slices.ContainsFunc(keys, func(k string) bool { return e.grants[s.name][k] })
}

func (e *Engine) pluginApproved(s *script, id string) bool {
	return e.approved(s, key(readPlugin, id)) || slices.ContainsFunc(slices.Collect(maps.Keys(e.grants[s.name])), func(k string) bool {
		return strings.HasPrefix(k, key(pluginAction, id)+":")
	})
}

// value hands a device value to a script. The types here are all a device can report; anything else would reach the
// script as a live view of the service's own memory.
func value(vm *goja.Runtime, v any) goja.Value {
	switch v.(type) {
	case bool, string, int, int32, int64, float32, float64:
		return vm.ToValue(v)
	}
	return goja.Undefined()
}

func (e *Engine) pluginObject(s *script, id string) map[string]any {
	return map[string]any{
		"id": id,
		// Parsed afresh into this script's VM, so one script changing what it got can't affect another.
		"data": func() goja.Value {
			if !e.allow(s, key(readPlugin, id)) {
				return goja.Undefined()
			}
			raw, _, _ := e.Plugins.Get(id)
			if raw == nil {
				return goja.Undefined()
			}
			parse, ok := goja.AssertFunction(s.vm.Get("JSON").ToObject(s.vm).Get("parse"))
			if !ok {
				panic(scriptError(s.vm, "JSON.parse was replaced"))
			}
			value, err := parse(goja.Undefined(), s.vm.ToValue(string(raw)))
			if err != nil {
				panic(err)
			}
			return value
		},
		// Like set, this does nothing real in shadow mode, and a repeat is dropped for a while.
		"action": func(name string, args goja.Value) {
			if err := e.action(s, id, name, args); err != nil {
				panic(scriptError(s.vm, err.Error()))
			}
		},
		"ageSeconds": func() goja.Value {
			if !e.allow(s, key(readPlugin, id)) {
				return goja.Undefined()
			}
			_, at, _ := e.Plugins.Get(id)
			if at.IsZero() {
				return goja.Undefined()
			}
			return s.vm.ToValue(e.now().Sub(at).Seconds())
		},
	}
}

func (e *Engine) action(s *script, id, name string, args goja.Value) error {
	if strings.Contains(name, ":") {
		return fmt.Errorf("an action's name can't hold a colon")
	}
	if !e.allow(s, actionKey(id, name)) {
		return nil
	}
	text := "null"
	if args != nil && !goja.IsUndefined(args) {
		stringify, ok := goja.AssertFunction(s.vm.Get("JSON").ToObject(s.vm).Get("stringify"))
		if !ok {
			return fmt.Errorf("JSON.stringify was replaced")
		}
		out, err := stringify(goja.Undefined(), args)
		if err != nil {
			return err
		}
		if !goja.IsUndefined(out) {
			text = out.String()
		}
	}
	if s.dry {
		return nil
	}
	label := fmt.Sprintf("%s.%s(%s)", id, name, text)
	if at, ok := s.acted[label]; ok && e.now().Sub(at) < resendAfter {
		return nil
	}
	if !e.live[s.name] {
		s.acted[label] = e.now()
		e.record(s.name, "shadow", "would call "+label)
		return nil
	}
	if err := e.Plugins.Act(id, name, []byte(text)); err != nil {
		// The detail can hold addresses and keys, so it goes to the service's log and not to the script.
		slog.Warn("plugin action failed", "script", s.name, "plugin", id, "action", name, "err", err)
		return fmt.Errorf("%s.%s failed", id, name)
	}
	s.acted[label] = e.now()
	e.record(s.name, "set", label)
	return nil
}

func (e *Engine) deviceObject(s *script, id string) map[string]any {
	name := id // its real name waits for an approval, which reloads the script
	if e.approved(s, key(readDevice, id), key(writeDevice, id)) {
		name = e.devices[id].Name
	}
	return map[string]any{
		"id":   id,
		"name": name,
		"online": func() bool {
			return e.allow(s, key(readDevice, id)) && e.devices[id].Online
		},
		// Refused, this is 0: a script treats a long gap as a lost device and may act on it, and nothing is known here.
		"offlineSeconds": func() int64 {
			if !e.allow(s, key(readDevice, id)) || e.devices[id].Online {
				return 0
			}
			return int64(e.now().Sub(e.seen[id]).Seconds())
		},
		// get returns undefined while the value is unknown, which sleeping sensors can be for a while after a restart.
		"get": func(code string) goja.Value {
			if !e.allow(s, key(readDevice, id)) {
				return goja.Undefined()
			}
			dp, ok := findCode(e.devices[id], code)
			if !ok {
				panic(scriptError(s.vm, fmt.Sprintf("%s has no value %q", e.devices[id].Name, code)))
			}
			return value(s.vm, dp.Value)
		},
		"set": func(code string, value any) {
			if !e.allow(s, key(writeDevice, id)) {
				return
			}
			if err := e.set(s, id, code, value); err != nil {
				panic(scriptError(s.vm, err.Error()))
			}
		},
	}
}

func findCode(d device.Device, code string) (device.DP, bool) {
	for _, dp := range d.DPs {
		if dp.Code == code {
			return dp, true
		}
	}
	return device.DP{}, false
}

// set is the only way a script changes a device. It sends nothing when the device already has the value, and in
// shadow mode it only records the decision.
func (e *Engine) set(s *script, id, code string, value any) error {
	d := e.devices[id]
	dp, ok := findCode(d, code)
	if !ok {
		return fmt.Errorf("%s has no value %q", d.Name, code)
	}
	if !dp.Writable {
		return fmt.Errorf("%s: %s is not writable", d.Name, code)
	}
	v, err := device.Check(dp.Type, dp.Values, value)
	if err != nil {
		return fmt.Errorf("%s: %s: %w", d.Name, code, err)
	}
	// A command queued for an unreachable device would fire stale whenever it comes back.
	if !d.Online || s.dry {
		return nil
	}
	key, label := dpKey{id, dp.DP}, fmt.Sprintf("%s %s = %v", d.Name, code, v)
	if dp.Type != device.Action && dp.Value != nil && device.Equal(dp.Value, v) {
		delete(s.sent, key)
		return nil // a pending shadow decision is settled by checkShadow, which reports it
	}
	if !e.live[s.name] {
		if prev, ok := s.shadow[key]; ok && device.Equal(prev.value, v) {
			return nil
		}
		s.shadow[key] = &shadowDecision{value: v, label: label, at: e.now()}
		e.record(s.name, "shadow", "would set "+label)
		return nil
	}
	if prev, ok := s.sent[key]; ok && device.Equal(prev.value, v) && e.now().Sub(prev.at) < resendAfter {
		return nil
	}
	if err := e.hub.Write(id, dp.DP, v); err != nil {
		// The detail can hold addresses and keys, so it goes to the service's log and not to the script.
		slog.Warn("device write failed", "script", s.name, "device", id, "err", err)
		return fmt.Errorf("could not send %s", label)
	}
	s.sent[key] = sentCommand{value: v, at: e.now()}
	e.record(s.name, "set", label)
	return nil
}

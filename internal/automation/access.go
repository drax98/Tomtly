package automation

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"
)

// approveAll skips the owner's approval. Only tests set it.
var approveAll atomic.Bool

const (
	// The kinds of access, as the first part of a key. Ids hold colons (tuya:abc), so an id always comes last, except
	// before an action's name, which can't hold one.
	readDevice   = "device.read"
	writeDevice  = "device.write"
	readPlugin   = "plugin.data"
	pluginAction = "plugin.action"

	maxRequests      = 200 // different things one script can have waiting, so computed ids can't fill memory or the log
	maxKeyLength     = 300
	maxMessage       = 2000
	maxLogsPerMinute = 120
)

func key(kind, id string) string { return kind + ":" + id }

func actionKey(id, name string) string { return key(pluginAction, id+":"+name) }

// Access is one thing a script asked for, for the page. A key isn't meant to be read: Label says it in words.
type Access struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Approved bool   `json:"approved"`
	Refused  int    `json:"refused,omitempty"` // calls refused since the script last loaded
}

// allow is the one gate for everything a script reaches outside its VM. It answers from the owner's approvals and
// records what it was asked here in Go, so nothing a script does can hide it. A refused call acts as if the value isn't
// known yet rather than throwing: an unapproved sensor must not stop the rest of the script, which already has to cope
// with that, and there is nothing for a catch to swallow.
//
// shortcut: a call on a branch that hasn't run yet can't be reported until it runs, unless the script lists it with
// home.needs. The trial at load finds what runs when everything is unknown; a declaration can't be enforced anyway.
func (e *Engine) allow(s *script, key string) bool {
	if approveAll.Load() || e.grants[s.name][key] {
		e.note(s, key)
		return true
	}
	if r := e.note(s, key); r != nil && !s.dry {
		r.refused++
		if r.refused == 1 {
			e.record(s.name, "refused", e.describe(key)+": not approved, so it was refused")
		}
	}
	return false
}

// note remembers that a script asked for key. It returns nil once a script has asked for too much.
func (e *Engine) note(s *script, key string) *request {
	if r, ok := s.requests[key]; ok {
		return r
	}
	if len(s.requests) >= maxRequests || len(key) > maxKeyLength {
		if !s.flooded && !s.dry {
			s.flooded = true
			e.record(s.name, "refused", "it asked for too many things, so the rest aren't listed")
		}
		return nil
	}
	r := &request{}
	s.requests[key] = r
	return r
}

// allowLog is the gate's answer for the activity log, which needs no approval because only the owner reads it, but
// must stay readable: a flood would push real entries, like refusals, out of it.
func (e *Engine) allowLog(s *script) bool {
	if s.dry {
		return false
	}
	if now := e.now(); now.Sub(s.logStart) >= time.Minute {
		s.logStart, s.logs = now, 0
	}
	s.logs++
	if s.logs == maxLogsPerMinute+1 {
		e.record(s.name, "refused", fmt.Sprintf("it logged over %d lines in a minute, so the rest of this minute's are dropped", maxLogsPerMinute))
	}
	return s.logs <= maxLogsPerMinute
}

// needs lists access in advance, for calls on a branch that may not run for months. It only asks: it grants nothing.
func (e *Engine) needs(s *script, list *goja.Object) {
	for field, kind := range map[string]string{"read": readDevice, "write": writeDevice, "data": readPlugin} {
		if value := list.Get(field); value != nil {
			var ids []string
			if err := s.vm.ExportTo(value, &ids); err != nil {
				panic(scriptError(s.vm, "home.needs: "+field+" must be a list of ids"))
			}
			for _, id := range ids {
				e.note(s, key(kind, id))
			}
		}
	}
	if value := list.Get("actions"); value != nil {
		var actions map[string][]string
		if err := s.vm.ExportTo(value, &actions); err != nil {
			panic(scriptError(s.vm, "home.needs: actions must list action names by plugin id"))
		}
		for id, names := range actions {
			for _, name := range names {
				if !strings.Contains(name, ":") {
					e.note(s, actionKey(id, name))
				}
			}
		}
	}
}

// scriptError is an error for a script to catch. NewGoError alone also hands over the Go value, as err.value.
func scriptError(vm *goja.Runtime, message string) *goja.Object {
	err := vm.NewGoError(fmt.Errorf("%s", message))
	err.Delete("value")
	return err
}

// describe says what a key allows, in words. Ids come from the script, so they are quoted when nothing vouches for them.
func (e *Engine) describe(k string) string {
	kind, id, _ := strings.Cut(k, ":")
	switch kind {
	case readDevice:
		return "Read " + e.deviceLabel(id)
	case writeDevice:
		return "Change " + e.deviceLabel(id)
	case readPlugin:
		return "Read data from the " + e.pluginLabel(id) + " plugin"
	case pluginAction:
		if i := strings.LastIndex(id, ":"); i >= 0 {
			return fmt.Sprintf("Run %q on the %s plugin", id[i+1:], e.pluginLabel(id[:i]))
		}
	}
	return fmt.Sprintf("Unknown access %q", k)
}

func (e *Engine) deviceLabel(id string) string {
	if d, ok := e.devices[id]; ok {
		return fmt.Sprintf("%s (%s)", d.Name, id)
	}
	return fmt.Sprintf("the unknown device %q", id)
}

func (e *Engine) pluginLabel(id string) string {
	if e.Plugins != nil {
		if _, _, ok := e.Plugins.Get(id); ok {
			return id
		}
	}
	return fmt.Sprintf("unknown %q", id)
}

// accessOf lists what a script asked for and what is approved for it, even if it hasn't asked for that since it loaded.
func (e *Engine) accessOf(s *script) []Access {
	out := []Access{}
	for _, k := range slices.Sorted(maps.Keys(s.requests)) {
		out = append(out, Access{Key: k, Label: e.describe(k), Approved: e.grants[s.name][k], Refused: s.requests[k].refused})
	}
	for _, k := range slices.Sorted(maps.Keys(e.grants[s.name])) {
		if _, asked := s.requests[k]; !asked {
			out = append(out, Access{Key: k, Label: e.describe(k), Approved: true})
		}
	}
	return out
}

// SetAccess approves or withdraws access for a script, and reloads it so the change applies from its first line.
// Only what the script asked for can be approved, so an approval always answers something the owner was shown.
func (e *Engine) SetAccess(name string, keys []string, approved bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	i := slices.IndexFunc(e.scripts, func(s *script) bool { return s.name == name })
	if i < 0 {
		return fmt.Errorf("unknown script %s", name)
	}
	s := e.scripts[i]
	if len(keys) == 0 {
		return fmt.Errorf("send the access to change as keys")
	}
	for _, k := range keys {
		if _, asked := s.requests[k]; approved && !asked {
			return fmt.Errorf("%s hasn't asked for %q", name, k)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), storeTimeout)
	defer cancel()
	if err := e.store.SetScriptGrants(ctx, name, keys, approved); err != nil {
		return fmt.Errorf("could not save the approval: %w", err)
	}
	if e.grants[name] == nil {
		e.grants[name] = map[string]bool{}
	}
	verb := "withdrawn"
	if approved {
		verb = "approved"
	}
	for _, k := range keys {
		if approved {
			e.grants[name][k] = true
		} else {
			delete(e.grants[name], k)
		}
		e.record(name, "log", verb+": "+e.describe(k))
	}
	e.replaceScript(name)
	// What only showed up while running would otherwise wait for the same branch to run again.
	for _, reloaded := range e.scripts {
		if reloaded.name == name {
			for k, r := range s.requests {
				if _, ok := reloaded.requests[k]; !ok && !e.grants[name][k] {
					reloaded.requests[k] = r
				}
			}
		}
	}
	return nil
}

// forget withdraws everything from a name that is about to be used by a new script, which earned none of it.
func (e *Engine) forget(name string) error {
	keys := slices.Collect(maps.Keys(e.grants[name]))
	if len(keys) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), storeTimeout)
	defer cancel()
	if err := e.store.SetScriptGrants(ctx, name, keys, false); err != nil {
		return fmt.Errorf("could not clear the old approvals: %w", err)
	}
	delete(e.grants, name)
	return nil
}

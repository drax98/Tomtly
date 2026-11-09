package extension

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/dop251/goja"

	"home-control/internal/tsbuild"
)

const (
	entryFile   = "extension.ts"
	typesFile   = "types.d.ts"
	loadTimeout = 2 * time.Second

	defaultDataEvery    = 300.0
	defaultDevicesEvery = 30.0
	// defaultBackgroundEvery is how long to wait between runs of background.run, which usually hold a socket read.
	defaultBackgroundEvery = 1.0
	minBackgroundEvery     = 0.05
	// maxStringify caps one JSON.stringify. Goja can't stop one part-way, so this stops what it made from going further.
	maxStringify = 4 << 20
)

var (
	idFormat     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	hostFormat   = regexp.MustCompile(`^[A-Za-z0-9.-]+(:[0-9]+)?$`)
	hostSetting  = regexp.MustCompile(`^\{([A-Za-z0-9_]+)\}$`)
	settingTypes = []string{"string", "number", "boolean", "secret", "file"}
)

// Setting is one value an extension declares that it needs from the person running it.
type Setting struct {
	Label    string `json:"label"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
	Default  any    `json:"default"`
	Help     string `json:"help"`
	// SendTo limits where a secret may be sent, to entries of access.network. Without it any listed host may get it.
	SendTo []string `json:"sendTo"`
	// SecretFields names the JSON fields in a file setting whose values are secrets of their own, such as "key".
	SecretFields []string `json:"secretFields"`
}

// definition is the plain-data part of what an extension passes to defineExtension.
type definition struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Homepage    string `json:"homepage"`
	// License is an SPDX id from Licenses. Optional here; the marketplace requires it.
	License string `json:"license"`
	// Categories and Tags are for finding it on the marketplace.
	Categories []string `json:"categories"`
	Tags       []string `json:"tags"`
	// Network moved into access; it is decoded only to refuse it with a useful message.
	Network  json.RawMessage    `json:"network"`
	Access   json.RawMessage    `json:"access"`
	Settings map[string]Setting `json:"settings"`
	Data     *struct {
		EverySeconds float64 `json:"everySeconds"`
	} `json:"data"`
	Devices *struct {
		EverySeconds float64 `json:"everySeconds"`
	} `json:"devices"`
	Webhooks   *struct{} `json:"webhooks"`
	Background *struct {
		EverySeconds float64 `json:"everySeconds"`
	} `json:"background"`
}

// loaded is an extension whose top-level code has run. Nothing in it can reach the network yet, because only the ctx
// passed to its functions can.
type loaded struct {
	definition
	access      accessDecl
	warnings    []string
	vm          *goja.Runtime
	fetchData   goja.Callable
	pollDevices goja.Callable
	actions     map[string]goja.Callable
	widgets     map[string]extWidget
	writeDevice goja.Callable // may be nil: read-only devices
	discover    goja.Callable // may be nil: it can't look on the network
	receive     goja.Callable // webhooks.receive
	background  goja.Callable
	// violate records something the extension tried and wasn't allowed. Until the service takes over it only collects
	// them, since the extension's id isn't known while its top-level code runs.
	violate func(kind, detail string) error
	early   [][2]string
}

const maxCallStack = 4096

func load(dir string) (*loaded, error) { return loadWatched(dir, nil) }

// loadWatched is load with the VM reported to track while its top-level code runs, so the memory watchdog can stop it.
func loadWatched(dir string, track func(*goja.Runtime) func()) (*loaded, error) {
	source, err := os.ReadFile(filepath.Join(dir, entryFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("no %s in this folder", entryFile)
	}
	if err != nil {
		return nil, err
	}
	code, err := tsbuild.Bundle(dir, "extension", string(source))
	if err != nil {
		return nil, err
	}
	vm := goja.New()
	vm.SetMaxCallStackSize(maxCallStack) // recursion through native calls grows the Go stack, and overflowing that kills the service
	l := &loaded{vm: vm}
	l.violate = func(kind, detail string) error {
		l.early = append(l.early, [2]string{kind, detail})
		return errors.New(detail)
	}
	if err := l.lockDynamicCode(); err != nil {
		return nil, err
	}
	var defined goja.Value
	err = vm.Set("defineExtension", func(call goja.FunctionCall) goja.Value {
		if defined != nil {
			panic(goError(vm, errors.New("defineExtension was called twice")))
		}
		defined = call.Argument(0)
		return defined
	})
	if err != nil {
		return nil, err
	}
	timer := time.AfterFunc(loadTimeout, func() { vm.Interrupt("extension took too long to load") })
	defer timer.Stop()
	if track != nil {
		defer track(vm)()
	}
	if _, err := vm.RunString(code); err != nil {
		return nil, err
	}
	if defined == nil {
		return nil, errors.New("the code never called defineExtension")
	}
	if err := l.define(defined); err != nil { // under the timer: a getter in the definition runs here
		return nil, err
	}
	timer.Stop()
	vm.ClearInterrupt()
	l.warnings = scan(code, l.access.Network, l.Homepage)
	return l, nil
}

func (l *loaded) define(value goja.Value) error {
	vm := l.vm
	text, err := stringify(vm, value)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(text), &l.definition); err != nil {
		return fmt.Errorf("defineExtension: %w", err)
	}
	if l.definition.Network != nil {
		return errors.New(`"network" moved to access.network: write access: { network: [...] }`)
	}
	if l.definition.Access != nil {
		dec := json.NewDecoder(bytes.NewReader(l.definition.Access))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&l.access); err != nil {
			return fmt.Errorf("access: %w (it takes network, lan, sockets, storage, webhooks and listen)", err)
		}
	}
	if err := l.access.checkListen(); err != nil {
		return err
	}
	if !idFormat.MatchString(l.ID) {
		return fmt.Errorf("id %q must be lowercase letters, digits and -", l.ID)
	}
	if l.Name == "" || l.Version == "" {
		return errors.New("defineExtension needs a name and a version")
	}
	if err := checkListingWords(l.Categories, l.Tags, l.License); err != nil {
		return err
	}
	for key, s := range l.Settings {
		if !slices.Contains(settingTypes, s.Type) {
			return fmt.Errorf("setting %s: type must be one of %s", key, strings.Join(settingTypes, ", "))
		}
		if s.Label == "" {
			return fmt.Errorf("setting %s needs a label", key)
		}
		if s.Type == "secret" && s.Default != nil {
			return fmt.Errorf("setting %s: a secret can't have a default", key)
		}
		// The owner picks the file. An extension naming one itself could read any secret in the folder.
		if s.Type == "file" && s.Default != nil {
			return fmt.Errorf("setting %s: a file can't have a default", key)
		}
		if len(s.SecretFields) > 0 && s.Type != "file" {
			return fmt.Errorf("setting %s: secretFields only applies to a file", key)
		}
		if len(s.SendTo) > 0 && s.Type != "secret" {
			return fmt.Errorf("setting %s: sendTo only applies to a secret", key)
		}
		for _, host := range s.SendTo {
			if !slices.Contains(l.access.Network, host) {
				return fmt.Errorf("setting %s: sendTo entry %q must also be in access.network", key, host)
			}
		}
	}
	for _, host := range l.access.Network {
		if host == "lan" {
			return errors.New(`access.network can't hold "lan"; use access.lan instead: access: { lan: true }`)
		}
		if m := hostSetting.FindStringSubmatch(host); m != nil {
			if l.Settings[m[1]].Type != "string" {
				return fmt.Errorf(`network entry %s needs a "string" setting called %s`, host, m[1])
			}
			// The value gets the local network and may be in sendTo, and a default would let the author choose it, in any update.
			if l.Settings[m[1]].Default != nil {
				return fmt.Errorf("setting %s can't have a default, because access.network or sendTo names it: the person running this has to give the address", m[1])
			}
		} else if !hostFormat.MatchString(host) {
			return fmt.Errorf("network entry %q must be a host name or {setting}", host)
		}
	}
	definitionObject := value.ToObject(vm)
	if l.Data != nil {
		if l.fetchData, err = callable(vm, definitionObject, "data", "fetch"); err != nil {
			return err
		}
		if l.Data.EverySeconds == 0 {
			l.Data.EverySeconds = defaultDataEvery
		}
	}
	if l.Devices != nil {
		if l.pollDevices, err = callable(vm, definitionObject, "devices", "poll"); err != nil {
			return err
		}
		if l.writeDevice, err = callable(vm, definitionObject, "devices", "write"); err != nil {
			l.writeDevice = nil // optional
		}
		if l.discover, err = callable(vm, definitionObject, "devices", "discover"); err != nil {
			l.discover = nil // optional
		}
		if l.Devices.EverySeconds == 0 {
			l.Devices.EverySeconds = defaultDevicesEvery
		}
	}
	if section := definitionObject.Get("actions"); section != nil && !goja.IsUndefined(section) {
		l.actions = map[string]goja.Callable{}
		object := section.ToObject(vm)
		for _, name := range object.Keys() {
			fn, ok := goja.AssertFunction(object.Get(name))
			if !ok {
				return fmt.Errorf("action %s must be a function", name)
			}
			l.actions[name] = fn
		}
	}
	if err := l.defineWidgets(definitionObject); err != nil {
		return err
	}
	if l.Webhooks != nil {
		if l.receive, err = callable(vm, definitionObject, "webhooks", "receive"); err != nil {
			return err
		}
		if !l.access.Webhooks {
			// Loading goes on, but no address is made and nothing can reach it; the attempt is on its card.
			l.violate("webhooks", `webhooks.receive is defined without "webhooks" in access`)
		}
	} else if l.access.Webhooks {
		return errors.New(`access.webhooks needs a webhooks.receive function`)
	}
	if l.Background != nil {
		if l.background, err = callable(vm, definitionObject, "background", "run"); err != nil {
			return err
		}
		if l.Background.EverySeconds == 0 {
			l.Background.EverySeconds = defaultBackgroundEvery
		}
		if l.Background.EverySeconds < minBackgroundEvery {
			return fmt.Errorf("background.everySeconds must be at least %g", minBackgroundEvery)
		}
	}
	if l.Data == nil && l.Devices == nil && len(l.actions) == 0 && l.Webhooks == nil && l.Background == nil && len(l.widgets) == 0 {
		return errors.New(`defineExtension needs "data", "devices", "actions", "webhooks", "background" or "widgets"`)
	}
	if (l.Data != nil && l.Data.EverySeconds < 1) || (l.Devices != nil && l.Devices.EverySeconds < 1) {
		return errors.New("everySeconds must be at least 1")
	}
	return nil
}

func callable(vm *goja.Runtime, object *goja.Object, section, name string) (goja.Callable, error) {
	fn, ok := goja.AssertFunction(object.Get(section).ToObject(vm).Get(name))
	if !ok {
		return nil, fmt.Errorf("%s needs a %s function", section, name)
	}
	return fn, nil
}

// stringify is JSON.stringify inside the VM. It returns "" for what JSON can't represent, like undefined.
func stringify(vm *goja.Runtime, value goja.Value) (string, error) {
	fn, ok := goja.AssertFunction(vm.Get("JSON").ToObject(vm).Get("stringify"))
	if !ok {
		return "", errors.New("JSON.stringify was replaced")
	}
	out, err := fn(goja.Undefined(), value)
	if err != nil {
		return "", err
	}
	if goja.IsUndefined(out) {
		return "", nil
	}
	if text, ok := out.(goja.String); ok && text.Length() > maxStringify {
		return "", fmt.Errorf("that is over %d bytes as JSON", maxStringify)
	}
	return out.String(), nil
}

// resolveSettings checks what config.json gave against what the extension declared, and applies defaults.
func resolveSettings(declared map[string]Setting, given map[string]any) (map[string]any, error) {
	out := map[string]any{}
	for key := range given {
		if _, ok := declared[key]; !ok {
			return nil, fmt.Errorf("unknown setting %q", key)
		}
	}
	for key, s := range declared {
		value, ok := given[key]
		if !ok {
			if s.Default != nil {
				out[key] = s.Default
			} else if s.Required {
				return nil, fmt.Errorf("the setting %q is required: %s", key, s.Label)
			}
			continue
		}
		var valid bool
		switch s.Type {
		case "number":
			_, valid = value.(float64)
		case "boolean":
			_, valid = value.(bool)
		default:
			var text string
			text, valid = value.(string)
			if valid && text == "" && s.Required {
				return nil, fmt.Errorf("the setting %q is required: %s", key, s.Label)
			}
			if valid && s.Type == "file" && text != "" && !filepath.IsLocal(text) {
				return nil, fmt.Errorf("the setting %q must be a path inside your config folder, like secrets/devices.json", key)
			}
		}
		if !valid {
			return nil, fmt.Errorf("the setting %q must be a %s", key, s.Type)
		}
		out[key] = value
	}
	return out, nil
}

package extension

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"tomtly/device"

	"github.com/dop251/goja"
)

// declaredDevice is what devices.poll returns: the device model, simplified for extension authors.
type declaredDevice struct {
	ID      string          `json:"id"`
	Name    string          `json:"name"`
	Kind    string          `json:"kind"`
	Product string          `json:"product"`
	Online  *bool           `json:"online"`
	Values  []declaredValue `json:"values"`
}

type declaredValue struct {
	Code     string   `json:"code"`
	Type     string   `json:"type"`
	Writable bool     `json:"writable"`
	Value    any      `json:"value"`
	Range    []string `json:"range"`
	Min      *float64 `json:"min"`
	Max      *float64 `json:"max"`
	Step     *float64 `json:"step"`
	Unit     string   `json:"unit"`
	Scale    int      `json:"scale"`
}

func (i *instance) pollDevices(l *loaded, api goja.Value) {
	var devices []device.Device
	value, err := i.call(l, l.pollDevices, api)
	var text string
	if err == nil {
		text, err = i.output(l, value)
	}
	if err == nil {
		devices, err = convertDevices(l.ID, text)
	}
	if err != nil {
		i.setError(err)
		i.mu.Lock()
		for j := range i.devices {
			i.devices[j].Online = false
		}
		i.mu.Unlock()
		return
	}
	i.mu.Lock()
	i.devices = devices
	i.mu.Unlock()
	i.setError(nil)
}

func convertDevices(extension, text string) ([]device.Device, error) {
	var declared []declaredDevice
	dec := json.NewDecoder(strings.NewReader(text))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&declared); err != nil {
		return nil, fmt.Errorf("devices.poll must return a list of devices: %w", err)
	}
	devices := make([]device.Device, 0, len(declared))
	seen := map[string]bool{}
	for _, d := range declared {
		if d.ID == "" || d.Name == "" {
			return nil, errors.New("every device needs an id and a name")
		}
		if seen[d.ID] {
			return nil, fmt.Errorf("two devices use the id %s", d.ID)
		}
		seen[d.ID] = true
		kind := d.Kind
		if kind == "" {
			kind = "other"
		}
		out := device.Device{ID: extension + ":" + d.ID, Adapter: extension, Name: d.Name, Kind: kind, Product: d.Product, Online: d.Online == nil || *d.Online}
		codes := map[string]bool{}
		for _, v := range d.Values {
			if v.Code == "" || codes[v.Code] {
				return nil, fmt.Errorf("device %s: value codes must be present and unique", d.ID)
			}
			codes[v.Code] = true
			if !slices.Contains([]string{device.Boolean, device.Enum, device.Integer, device.Action, "String", "Number"}, v.Type) {
				return nil, fmt.Errorf("device %s: %s has an unknown type %q", d.ID, v.Code, v.Type)
			}
			if v.Writable && (v.Type == "String" || v.Type == "Number") {
				return nil, fmt.Errorf("device %s: %s: a %s value can't be writable", d.ID, v.Code, v.Type)
			}
			values := map[string]any{}
			if v.Range != nil {
				values["range"] = v.Range
			}
			if v.Min != nil {
				values["min"] = *v.Min
			}
			if v.Max != nil {
				values["max"] = *v.Max
			}
			if v.Step != nil {
				values["step"] = *v.Step
			}
			if v.Unit != "" {
				values["unit"] = v.Unit
			}
			out.DPs = append(out.DPs, device.DP{DP: v.Code, Code: v.Code, Type: v.Type, Values: values, Scale: v.Scale, Writable: v.Writable, Value: v.Value})
		}
		devices = append(devices, out)
	}
	return devices, nil
}

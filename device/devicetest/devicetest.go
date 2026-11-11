// Package devicetest checks that an adapter keeps the rules everything above it relies on.
// Call Conformance from your adapter's tests, with an adapter holding at least one device.
package devicetest

import (
	"encoding/json"
	"strings"
	"testing"

	"tomtly/device"
)

// Conformance fails if the adapter breaks the contract. Pass any credentials it holds as secrets.
func Conformance(t *testing.T, a device.Adapter, secrets ...string) {
	t.Helper()
	devices := a.Devices()
	if len(devices) == 0 {
		t.Fatal("give the adapter at least one device, or this test checks nothing")
	}
	data, err := json.Marshal(devices)
	if err != nil {
		t.Fatalf("devices must encode to JSON: %v", err)
	}
	for _, s := range secrets {
		if s != "" && strings.Contains(string(data), s) {
			t.Error("a credential leaked into Devices(), which the API returns as is")
		}
	}
	ids := map[string]bool{}
	for _, d := range devices {
		if d.ID == "" || d.Name == "" || d.Kind == "" {
			t.Errorf("device %+v needs an id, a name and a kind", d)
		}
		if ids[d.ID] {
			t.Errorf("duplicate device id %s", d.ID)
		}
		ids[d.ID] = true
		dps, codes := map[string]bool{}, map[string]bool{}
		for _, dp := range d.DPs {
			if dps[dp.DP] {
				t.Errorf("%s: duplicate dp %s", d.Name, dp.DP)
			}
			dps[dp.DP] = true
			if codes[dp.Code] {
				t.Errorf("%s: duplicate code %s; scripts address values by code", d.Name, dp.Code)
			}
			codes[dp.Code] = true
			if dp.Writable {
				switch dp.Type {
				case device.Boolean, device.Enum, device.Integer, device.Action:
				default:
					t.Errorf("%s: writable %s has type %s, which cannot be validated", d.Name, dp.Code, dp.Type)
				}
				if err := a.Write(d.ID, dp.DP, struct{}{}); err == nil {
					t.Errorf("%s: %s accepted a nonsense value", d.Name, dp.Code)
				}
			} else if err := a.Write(d.ID, dp.DP, true); err == nil {
				t.Errorf("%s: read-only %s accepted a write", d.Name, dp.Code)
			}
		}
		if err := a.Write(d.ID, "no-such-dp", true); err == nil {
			t.Errorf("%s: an unknown dp accepted a write", d.Name)
		}
	}
	if err := a.Write("no-such-device", "1", true); err == nil {
		t.Error("an unknown device accepted a write")
	}
}

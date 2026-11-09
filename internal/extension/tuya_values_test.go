package extension

import (
	"encoding/json"

	"testing"
)

func TestTuyaColourIsReadable(t *testing.T) {
	file, _ := json.Marshal([]map[string]any{
		{"id": "gw1", "name": "Gateway", "key": tuyaKey, "category": "wg2", "product_name": "GW", "mapping": map[string]any{
			"3": map[string]any{"code": "colour_data", "type": "Json", "values": map[string]any{"h": map[string]any{"max": 360}}},
			"4": map[string]any{"code": "scene", "type": "Json", "values": map[string]any{"h": map[string]any{"max": 360}}},
		}},
	})
	f := startTuyaFake(t, "3.5", tuyaKey, map[string]map[string]any{"": {"3": "00dc000003e8", "4": "zz"}})
	m := run(t, folder(t, tuyaSource(t, f.port())), map[string]any{"devices": "tuya.json", "hosts": "gw1=127.0.0.1@3.5"}, allowLoopback, func(m *Manager) {
		configFile(t, m, "tuya.json", string(file))
	})
	eventuallyValue(t, m, "gw1", "scene", "zz")
	d, _ := tuyaDevice(m, "gw1")
	if dp, _ := tuyaValue(d, "colour_data"); dp.Value != "hue 220, saturation 0%, brightness 100%" {
		t.Errorf("colour_data = %v", dp.Value)
	}
}

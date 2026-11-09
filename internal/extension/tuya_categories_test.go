package extension

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"testing"

	"home-control/device"
)

var tuyaCategoryLine = regexp.MustCompile(`(?m)^  "?([a-z0-9_.]+)"?: "([a-z]+)", //`)

// One file listing a device for each category and code asked about, behind one gateway.
func runTuyaTable(t *testing.T, devices []map[string]any) *Manager {
	t.Helper()
	f := startTuyaFake(t, "3.4", tuyaKey, nil)
	file := append([]map[string]any{{"id": "gw1", "name": "Gateway", "key": tuyaKey, "category": "wg2", "product_name": "GW", "mapping": map[string]any{}}}, devices...)
	m := runTuyaFile(t, tuyaSource(t, f.port()), "gw1=127.0.0.1@3.4", file)
	tuyaEventually(t, "the devices", func() bool { return len(m.Devices()) == len(file) })
	return m
}

func tuyaTableDevice(id, category string, mapping map[string]any) map[string]any {
	return map[string]any{"id": id, "name": id, "key": "kkkkkkkkkkkkkkk1", "category": category, "product_name": "P", "node_id": "cid-" + id, "mapping": mapping}
}

func TestTuyaEveryCategoryHasAKnownKind(t *testing.T) {
	data, err := os.ReadFile(example(t, "tuya") + "/categories.ts")
	if err != nil {
		t.Fatal(err)
	}
	known := []string{"light", "switch", "plug", "cover", "climate", "air", "fan", "vacuum", "sensor", "safety", "lock", "appliance", "infrastructure"}
	var devices []map[string]any
	want := map[string]string{}
	for _, match := range tuyaCategoryLine.FindAllStringSubmatch(string(data), -1) {
		if !slices.Contains(known, match[2]) {
			t.Errorf("%s has the kind %s", match[1], match[2])
		}
		want[match[1]] = match[2]
		devices = append(devices, tuyaTableDevice("d-"+match[1], match[1], map[string]any{}))
	}
	if len(want) != 141 {
		t.Fatalf("%d categories, the sources list 141 (Tuya's 103, Home Assistant's 37 more, and sxd)", len(want))
	}
	m := runTuyaTable(t, devices)
	for category, kind := range want {
		if d, ok := tuyaDevice(m, "d-"+category); !ok || d.Kind != kind {
			t.Errorf("%s: %+v, want %s", category, d, kind)
		}
	}
	for _, category := range []string{"qt", "zz"} {
		m := runTuyaTable(t, []map[string]any{tuyaTableDevice("u", category, map[string]any{})})
		if d, _ := tuyaDevice(m, "u"); d.Kind != "other" {
			t.Errorf("%s: %s", category, d.Kind)
		}
	}
	m = runTuyaTable(t, []map[string]any{tuyaTableDevice("u", "wgsxj", map[string]any{})})
	if d, _ := tuyaDevice(m, "u"); d.Kind != "infrastructure" {
		t.Errorf("any wg* is a gateway: %s", d.Kind)
	}
}

func TestTuyaWritableRules(t *testing.T) {
	boolean := map[string]any{"type": "Boolean", "values": map[string]any{}}
	number := map[string]any{"type": "Integer", "values": map[string]any{"min": 0, "max": 1000}}
	enum := map[string]any{"type": "Enum", "values": map[string]any{"range": []string{"a", "b"}}}
	json := map[string]any{"type": "Json", "values": ""}
	for _, c := range []struct {
		family, category, code string
		shape                  map[string]any
		writable               bool
	}{
		// Lighting
		{"light", "dj", "switch_led", boolean, true},
		{"light", "dj", "bright_value_v2", number, true},
		{"light", "dj", "temp_value", number, true},
		{"light", "dj", "work_mode", enum, true},
		{"light", "dj", "scene_data", json, false},
		// Electrical
		{"socket", "cz", "switch_1", boolean, true},
		{"socket", "cz", "cur_power", number, false},
		{"switch", "kg", "switch_2", boolean, true},
		{"switch", "kg", "cur_voltage", number, false},
		{"meter", "zndb", "forward_energy_total", number, false},
		{"meter", "zndb", "unknown_thing", boolean, false},
		{"breaker", "dlq", "switch_1", boolean, true},
		{"breaker", "dlq", "total_forward_energy", number, false},
		// Climate
		{"ac", "kt", "temp_set", number, true},
		{"ac", "kt", "temp_current", number, false},
		{"heater", "qn", "work_power", number, false},
		{"humidifier", "jsq", "humidity_set", number, true},
		{"humidifier", "jsq", "humidity_current", number, false},
		{"purifier", "kj", "pm25", number, false},
		{"purifier", "kj", "filter_life", number, false},
		{"fan", "fs", "fan_speed_percent", number, true},
		// Sensors keep every code to themselves, even one they've never been seen with
		{"contact", "mcs", "temper_alarm", boolean, false},
		{"motion", "pir", "pir", enum, false},
		{"climate sensor", "wsdcg", "temp_unit_convert", enum, false},
		{"leak", "sj", "watersensor_state", enum, false},
		{"gas", "rqbj", "gas_sensor_status", enum, false},
		{"smoke", "ywbj", "muffling", boolean, false},
		{"vibration", "zd", "vibration_state", enum, false},
		{"presence", "hps", "presence_state", enum, false},
		{"luminance", "ldcg", "bright_state", enum, false},
		{"button", "wxkg", "switch_mode1", enum, false},
		// Security
		{"siren", "sgbj", "alarm_switch", boolean, true},
		{"alarm host", "mal", "master_mode", enum, true},
		{"alarm host", "mal", "alarm_msg", json, false},
		{"lock", "ms", "unlock_fingerprint", number, false},
		{"lock", "ms", "anything_else", boolean, false},
		{"camera", "sp", "basic_flip", boolean, false},
		// Covers
		{"curtain", "cl", "control", enum, true},
		{"curtain", "cl", "percent_control", number, true},
		{"curtain", "cl", "percent_state", number, false},
		{"garage", "ckmkzq", "switch_1", boolean, true},
		{"garage", "ckmkzq", "doorcontact_state", boolean, false},
		// Appliances
		{"vacuum", "sd", "power_go", boolean, true},
		{"vacuum", "sd", "status", enum, false},
		{"vacuum", "sd", "electricity_left", number, false},
		{"feeder", "cwwsq", "manual_feed", number, true},
		{"feeder", "cwwsq", "feed_state", enum, false},
		{"irrigator", "ggq", "switch_1", boolean, true},
		{"coffee", "kfj", "cup_number", number, true},
		// IR and scenes have no write the host can carry
		{"ir", "wnykq", "ir_send", json, false},
		// Nobody documented it, so only what's plainly a command
		{"unknown", "zz", "switch_1", boolean, true},
		{"unknown", "zz", "mystery", boolean, false},
		{"unknown", "zz", "mystery", number, false},
	} {
		t.Run(c.family+" "+c.category+" "+c.code, func(t *testing.T) {
			shape := map[string]any{"code": c.code, "type": c.shape["type"], "values": c.shape["values"]}
			m := runTuyaTable(t, []map[string]any{tuyaTableDevice("d", c.category, map[string]any{"5": shape})})
			d, _ := tuyaDevice(m, "d")
			if dp, ok := tuyaValue(d, c.code); !ok || dp.Writable != c.writable {
				t.Fatalf("writable = %v in %+v", dp.Writable, d)
			}
		})
	}
}

// What the rule before it was generalised gave these categories, odd ones included.
func TestTuyaTunedCategoriesKeepTheirFlags(t *testing.T) {
	boolean := map[string]any{"type": "Boolean", "values": map[string]any{}}
	number := map[string]any{"type": "Integer", "values": map[string]any{"min": 0, "max": 1000}}
	enum := map[string]any{"type": "Enum", "values": map[string]any{"range": []string{"a", "b"}}}
	for _, c := range []struct {
		category, kind string
		code           string
		shape          map[string]any
		writable       bool
	}{
		{"wk", "climate", "switch", boolean, true},
		{"wk", "climate", "window_state", enum, true},
		{"wk", "climate", "valve_state", number, true},
		{"wk", "climate", "work_state", enum, false},
		{"dd", "light", "switch_led", boolean, true},
		{"dd", "light", "bright_value", number, true},
		{"cz", "plug", "relay_status", enum, false},
		{"cz", "plug", "cur_power", number, false},
		{"cl", "cover", "percent_control", number, true},
		{"cl", "cover", "work_state", enum, false},
		{"pc", "infrastructure", "switch_1", boolean, true},
		{"tdq", "infrastructure", "switch_1", boolean, true},
		{"tdq", "infrastructure", "add_ele", number, false},
		{"mcs", "sensor", "battery_percentage", number, false},
		{"cobj", "safety", "co_status", enum, false},
		{"ywbj", "safety", "smoke_sensor_status", enum, false},
	} {
		shape := map[string]any{"code": c.code, "type": c.shape["type"], "values": c.shape["values"]}
		m := runTuyaTable(t, []map[string]any{tuyaTableDevice("d", c.category, map[string]any{"5": shape})})
		d, _ := tuyaDevice(m, "d")
		dp, ok := tuyaValue(d, c.code)
		if !ok || d.Kind != c.kind || dp.Writable != c.writable {
			t.Errorf("%s %s: kind %s, writable %v", c.category, c.code, d.Kind, dp.Writable)
		}
	}
}

func TestTuyaDestructiveCodesAreRefused(t *testing.T) {
	mapping := map[string]any{}
	codes := []string{"factory_reset", "reset_map", "reset_filter", "filter_reset", "device_restart", "reboot", "ota_upgrade", "firmware_update", "wifi_config", "unbind", "pair_start", "zigbee_pairing", "sd_format", "clear_data", "remote_unlock"}
	for i, code := range codes {
		mapping[string(rune('a'+i))] = map[string]any{"code": code, "type": "Boolean", "values": map[string]any{}}
	}
	// In a category that writes, and one nobody documented, on the protocol's real write path.
	for _, category := range []string{"sd", "dj", "zz"} {
		f := startTuyaFake(t, "3.4", tuyaKey, nil)
		m := runTuyaFile(t, tuyaSource(t, f.port()), "gw1=127.0.0.1@3.4", []map[string]any{
			{"id": "gw1", "name": "Gateway", "key": tuyaKey, "category": "wg2", "product_name": "GW", "mapping": map[string]any{}},
			tuyaTableDevice("d", category, mapping),
		})
		tuyaEventually(t, "the device", func() bool { _, ok := tuyaDevice(m, "d"); return ok })
		d, _ := tuyaDevice(m, "d")
		for _, code := range codes {
			if dp, ok := tuyaValue(d, code); !ok || dp.Writable {
				t.Errorf("%s %s: %+v", category, code, dp)
			}
			if err := m.Write("tuya:d", code, true); err == nil {
				t.Errorf("%s %s was accepted", category, code)
			}
		}
		if n := len(f.controlFrames()); n != 0 {
			t.Errorf("%d commands went out", n)
		}
	}
}

func TestTuyaScale(t *testing.T) {
	value := func(code string, values map[string]any) map[string]any {
		return map[string]any{"code": code, "type": "Integer", "values": values}
	}
	m := runTuyaTable(t, []map[string]any{
		tuyaTableDevice("trv", "wk", map[string]any{
			"1": value("temp_current", map[string]any{"min": 0, "max": 500, "scale": 0}), // tenths, whatever the cloud says
			"2": value("temp_set", map[string]any{"min": 50, "max": 300, "scale": 1}),
		}),
		tuyaTableDevice("whole", "kt", map[string]any{"1": value("temp_current", map[string]any{"min": 0, "max": 50, "scale": 0})}),
		tuyaTableDevice("tuned", "cz", map[string]any{"1": value("cur_voltage", map[string]any{"min": 0, "max": 300, "scale": 0})}),
		tuyaTableDevice("oven", "kqzg", map[string]any{"1": value("temp_current", map[string]any{"min": 0, "max": 300, "scale": 0})}),
		tuyaTableDevice("plug", "cz", map[string]any{
			"1": value("cur_voltage", map[string]any{"min": 0, "max": 5000, "scale": 0}),
			"2": value("cur_power", map[string]any{"min": 0, "max": 50000, "scale": 1}),
		}),
		tuyaTableDevice("meter", "zndb", map[string]any{
			"1": value("cur_voltage", map[string]any{"min": 0, "max": 500, "scale": 0}),
			"2": value("forward_energy_total", map[string]any{"min": 0, "max": 99999999, "scale": 2}),
		}),
	})
	for _, c := range []struct {
		id, code string
		scale    int
	}{{"trv", "temp_current", 1}, {"trv", "temp_set", 1}, {"whole", "temp_current", 0}, {"tuned", "cur_voltage", 1}, {"oven", "temp_current", 0}, {"plug", "cur_voltage", 1}, {"plug", "cur_power", 1}, {"meter", "cur_voltage", 0}, {"meter", "forward_energy_total", 2}} {
		d, _ := tuyaDevice(m, c.id)
		if dp, _ := tuyaValue(d, c.code); dp.Scale != c.scale {
			t.Errorf("%s %s scale %d, want %d", c.id, c.code, dp.Scale, c.scale)
		}
	}
}

// One odd device in someone's file mustn't take the whole list down, since the host refuses a bad list outright.
func TestTuyaAwkwardFiles(t *testing.T) {
	m := runTuyaTable(t, []map[string]any{
		{"id": "noname", "key": "k", "category": "cz", "node_id": "c1", "mapping": map[string]any{
			"1": map[string]any{"code": "switch_1", "type": "bool"},
			"2": map[string]any{"code": "switch_1", "type": "bool"},
			"3": map[string]any{"type": "value", "values": `{"min":0,"max":10,"scale":1.5}`},
			"4": map[string]any{"code": "mode", "type": "enum", "values": `{"range":["a",7]}`},
			"5": map[string]any{"code": "blob", "type": "raw", "values": "not json"},
			"6": map[string]any{"code": "noranges", "type": "Enum", "values": map[string]any{}},
			"7": "nonsense",
		}},
		{"id": "nomap", "name": "No mapping", "key": "k", "category": "zz", "node_id": "c2"},
	})
	d, ok := tuyaDevice(m, "noname")
	if !ok || d.Name != "noname" {
		t.Fatalf("%+v", d)
	}
	if dp, _ := tuyaValue(d, "switch_1"); dp.Type != device.Boolean || !dp.Writable {
		t.Errorf("thing model types: %+v", dp)
	}
	if _, ok := tuyaValue(d, "switch_1_2"); !ok {
		t.Errorf("a repeated code should get its own name: %+v", d.DPs)
	}
	if dp, ok := tuyaValue(d, "dp_3"); !ok || dp.Scale != 0 || dp.Values["max"] != float64(10) {
		t.Errorf("no code, JSON values, a scale that isn't whole: %+v", dp)
	}
	if dp, _ := tuyaValue(d, "mode"); !device.Equal(dp.Values["range"], []string{"a"}) {
		t.Errorf("mode: %+v", dp)
	}
	if dp, _ := tuyaValue(d, "noranges"); dp.Writable {
		t.Errorf("an Enum with no choices can't be written: %+v", dp)
	}
	if dp, _ := tuyaValue(d, "blob"); dp.Type != "String" || dp.Writable {
		t.Errorf("blob: %+v", dp)
	}
}

func tuyaLightFile(values map[string]any) []map[string]any {
	return []map[string]any{{"id": "bulb", "name": "Bulb", "key": tuyaKey, "category": "dj", "product_name": "Bulb", "mapping": map[string]any{
		"20": map[string]any{"code": "switch_led", "type": "Boolean", "values": map[string]any{}},
		"21": map[string]any{"code": "work_mode", "type": "Enum", "values": map[string]any{"range": []string{"white", "colour", "scene"}}},
		"24": map[string]any{"code": "colour_data", "type": "Json", "values": values},
	}}}
}

// A write comes back in the form the light uses, read after the push as the same numbers.
func TestTuyaColourRoundTrip(t *testing.T) {
	for _, c := range []struct {
		name       string
		version    string
		first      any
		values     map[string]any
		hue, sat   int
		max        int
		words      string
		afterHue   any
		afterWords string
	}{
		{"hsv16", "3.3", "00dc03e801f4", map[string]any{}, 220, 1000, 1000, "hue 220, saturation 100%, brightness 50%", "012c03e801f4", "hue 300, saturation 100%, brightness 50%"},
		{"rgb8", "3.3", "2a007f00dcff7f", map[string]any{}, 220, 255, 255, "hue 220, saturation 100%, brightness 49.8%", "7f007f012cff7f", "hue 300, saturation 100%, brightness 49.8%"},
		{"json string", "3.4", `{"h":220,"s":1000,"v":500}`, map[string]any{"s": map[string]any{"max": 1000}}, 220, 1000, 1000, "hue 220, saturation 100%, brightness 50%", `{"h":300,"s":1000,"v":500}`, "hue 300, saturation 100%, brightness 50%"},
		{"object, on 3.5", "3.5", map[string]any{"h": 220, "s": 255, "v": 255}, map[string]any{"s": map[string]any{"max": 255}}, 220, 255, 255, "hue 220, saturation 100%, brightness 100%", map[string]any{"h": float64(300), "s": float64(255), "v": float64(255)}, "hue 300, saturation 100%, brightness 100%"},
		{"3.1", "3.1", "00dc03e801f4", map[string]any{}, 220, 1000, 1000, "hue 220, saturation 100%, brightness 50%", "012c03e801f4", "hue 300, saturation 100%, brightness 50%"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := startTuyaPeer(t, c.version, tuyaKey, map[string]map[string]any{"": {"20": true, "21": "white", "24": c.first}})
			m := runTuyaFile(t, tuyaSource(t, f.port()), "bulb=127.0.0.1@"+c.version, tuyaLightFile(c.values))
			eventuallyValue(t, m, "bulb", "colour_hue", c.hue)
			d, _ := tuyaDevice(m, "bulb")
			if dp, _ := tuyaValue(d, "colour_data"); dp.Value != c.words || dp.Writable {
				t.Fatalf("colour_data: %+v", dp)
			}
			if dp, _ := tuyaValue(d, "colour_saturation"); !dp.Writable || !device.Equal(dp.Value, c.sat) || !device.Equal(dp.Values["max"], c.max) {
				t.Fatalf("colour_saturation: %+v", dp)
			}
			if err := m.Write("tuya:bulb", "colour_hue", float64(300)); err != nil {
				t.Fatal(err)
			}
			dps := tuyaSentDps(t, tuyaWaitControls(t, f, 1))
			if !device.Equal(dps["24"], c.afterHue) || dps["21"] != "colour" {
				t.Fatalf("sent %#v, want %#v in colour mode", dps, c.afterHue)
			}
			f.push("", map[string]any{"24": dps["24"]})
			eventuallyValue(t, m, "bulb", "colour_data", c.afterWords)
			eventuallyValue(t, m, "bulb", "colour_hue", 300)
			if err := m.Write("tuya:bulb", "colour_hue", float64(361)); err == nil {
				t.Fatal("a hue over 360 should be refused")
			}
		})
	}
	t.Run("not before the light has said which form it uses", func(t *testing.T) {
		f := startTuyaFake(t, "3.4", tuyaKey, map[string]map[string]any{"": {"20": true}})
		m := runTuyaFile(t, tuyaSource(t, f.port()), "bulb=127.0.0.1@3.4", tuyaLightFile(map[string]any{}))
		eventuallyValue(t, m, "bulb", "switch_led", true)
		d, _ := tuyaDevice(m, "bulb")
		if dp, _ := tuyaValue(d, "colour_hue"); dp.Writable || dp.Value != nil {
			t.Fatalf("%+v", dp)
		}
		if err := m.Write("tuya:bulb", "colour_hue", float64(10)); err == nil {
			t.Fatal("it should be refused")
		}
	})
}

// Scripts read "open" from a window sensor, whichever way its mapping names it.
func TestTuyaContactSensorIsOpen(t *testing.T) {
	contact := map[string]any{"code": "doorcontact_state", "type": "Boolean", "values": map[string]any{}}
	m := runTuyaTable(t, []map[string]any{tuyaTableDevice("d", "mcs", map[string]any{"1": contact})})
	d, _ := tuyaDevice(m, "d")
	if dp, ok := tuyaValue(d, "open"); !ok || dp.Writable {
		t.Fatalf("%+v", d)
	}
	both := map[string]any{"1": contact, "101": map[string]any{"code": "prm_switch", "type": "Boolean", "values": map[string]any{}}}
	m = runTuyaTable(t, []map[string]any{tuyaTableDevice("d", "mcs", both)})
	d, _ = tuyaDevice(m, "d")
	if _, ok := tuyaValue(d, "doorcontact_state"); !ok {
		t.Fatalf("two can't both be open: %+v", d)
	}
}

// A multi-channel device writes the channel asked for, and a Json value reads as sent.
func TestTuyaChannelsAndTextValues(t *testing.T) {
	f := startTuyaCRCFake(t, "3.3", tuyaKey, map[string]map[string]any{"": {"1": true, "2": false, "9": map[string]any{"a": 1}, "10": "aGVsbG8=", "11": 3}})
	m := runTuyaFile(t, tuyaSource(t, f.port()), "s=127.0.0.1@3.3", []map[string]any{{"id": "s", "name": "S", "key": tuyaKey, "category": "kg", "product_name": "P", "mapping": map[string]any{
		"1":  map[string]any{"code": "switch_1", "type": "Boolean", "values": map[string]any{}},
		"2":  map[string]any{"code": "switch_2", "type": "Boolean", "values": map[string]any{}},
		"9":  map[string]any{"code": "cycle_timing", "type": "Json", "values": ""},
		"10": map[string]any{"code": "random_timing", "type": "Raw", "values": ""},
		"11": map[string]any{"code": "fault", "type": "Bitmap", "values": map[string]any{"label": []string{"overheat", "overcurrent"}}},
	}}})
	eventuallyValue(t, m, "s", "switch_2", false)
	eventuallyValue(t, m, "s", "fault", "overheat, overcurrent")
	eventuallyValue(t, m, "s", "cycle_timing", `{"a":1}`)
	eventuallyValue(t, m, "s", "random_timing", "aGVsbG8=")
	if err := m.Write("tuya:s", "switch_2", true); err != nil {
		t.Fatal(err)
	}
	sent, _ := json.Marshal(tuyaSentDps(t, tuyaWaitControls(t, f, 1)))
	if string(sent) != `{"2":true}` {
		t.Fatalf("sent %s", sent)
	}
}

// A real sensor's mapping calls data point 101 "switch", and scripts read it as "open".
func TestTuyaWindowSensorNamedSwitchIsOpen(t *testing.T) {
	m := runTuyaTable(t, []map[string]any{tuyaTableDevice("d", "mcs", map[string]any{"101": map[string]any{"code": "switch", "type": "Boolean", "values": map[string]any{}}})})
	d, _ := tuyaDevice(m, "d")
	if dp, ok := tuyaValue(d, "open"); !ok || dp.Writable {
		t.Fatalf("%+v", d)
	}
}

package extension

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"home-control/device"
)

// With many devices each is read less often, none is left out, and the service's limit is never reached.
func TestXiaomiPollingStaysWithinItsBudget(t *testing.T) {
	miio := startMiio(t, answerProperties(map[string]any{"2.1": true}))
	var devices []map[string]any
	const count = 40
	for i := range count {
		devices = append(devices, xiaomiSpecDevice(fmt.Sprint(i+1), "dmaker.fan.1c", ""))
	}
	// One minute passes every real second, so 60 operations per "minute" allows 30 calls a second, and a poll's share is 5.
	source := xiaomiCopy(t, xiaomiFastClock, "everySeconds: 10", "everySeconds: 1", "TIMEOUT_MS = 1500", "TIMEOUT_MS = 100", "OPS_PER_MINUTE = 400", "OPS_PER_MINUTE = 60")
	m := runXiaomi(t, source, xiaomiFile(devices...))
	start := time.Now()
	xiaomiWait(t, "every device to get a turn", func() bool {
		list := m.Devices()
		if len(list) != count {
			return false
		}
		for _, d := range list {
			if !d.Online {
				return false
			}
		}
		return true
	})
	calls := len(miio.methods("get_properties"))
	if limit := int(time.Since(start).Seconds()+2) * 30; calls > limit {
		t.Errorf("%d calls in %v, over %d", calls, time.Since(start), limit)
	}
	noViolations(t, m)
}

// The list stops being refused as a whole when it would pass 1 MiB: the longest give up their values and say so.
func TestXiaomiOutputStaysUnderTheLimit(t *testing.T) {
	startMiio(t, answerProperties(heaterReadings))
	var devices []map[string]any
	for i := range 4 {
		devices = append(devices, xiaomiSpecDevice(fmt.Sprint(i+1), "acme.heater.h1", xiaomiHeaterUrn))
	}
	specs := startSpecs(t, heaterSpec(t))
	m := runXiaomiSpec(t, xiaomiSpecSource(t, specs, false, "MAX_OUTPUT = 900 * 1024", "MAX_OUTPUT = 6000"), specs, xiaomiFile(devices...))
	xiaomiWait(t, "a trimmed device", func() bool {
		for _, d := range m.Devices() {
			if note, ok := xiaomiValue(d, "unavailable").(string); ok && strings.Contains(note, "left out") {
				return true
			}
		}
		return false
	})
	noViolations(t, m)
}

func TestXiaomiLegacyFamily(t *testing.T) {
	values := map[string]any{"power": "on", "bright": "40", "ct": "4000", "rgb": "16711680", "color_mode": "2", "flowing": "0"}
	miio := startMiio(t, func(c miioCall) (any, any) {
		if c.Method == "get_prop" {
			var names []string
			json.Unmarshal(c.Params, &names)
			out := []any{}
			for _, n := range names {
				out = append(out, values[n])
			}
			return out, nil
		}
		return []string{"ok"}, nil
	})
	m := runXiaomi(t, example(t, "xiaomi"), xiaomiFile(xiaomiDevice("yeelink.light.color1")))
	eventually(t, "the light", func() bool { d, ok := xiaomiOne(m); return ok && d.Online })
	d, _ := xiaomiOne(m)
	if d.Kind != "light" {
		t.Errorf("kind %s", d.Kind)
	}
	for code, want := range map[string]any{"power": true, "brightness": 40.0, "color_temperature": 4000.0, "color": "#ff0000"} {
		if got := xiaomiValue(d, code); got != want {
			t.Errorf("%s = %v, want %v", code, got, want)
		}
	}
	if dp, _ := dpOf(d, "color"); dp.Writable {
		t.Error("colour is only shown")
	}
	for code, sent := range map[string]struct {
		value  any
		method string
		params string
	}{
		"power":             {false, "set_power", `["off","smooth",500]`},
		"brightness":        {float64(70), "set_bright", `[70,"smooth",500]`},
		"color_temperature": {float64(3000), "set_ct_abx", `[3000,"smooth",500]`},
	} {
		if err := m.Write("xiaomi:"+xiaomiDID, code, sent.value); err != nil {
			t.Fatal(err)
		}
		eventually(t, code, func() bool {
			calls := miio.methods(sent.method)
			return len(calls) == 1 && string(calls[0].Params) == sent.params
		})
	}
	if len(miio.methods("get_properties")) != 0 {
		t.Error("asked for MIoT properties with no spec")
	}
}

func TestXiaomiChuangmiPlugHasItsOwnOnAndOff(t *testing.T) {
	miio := startMiio(t, func(c miioCall) (any, any) {
		if c.Method == "get_prop" {
			return []any{"on", true, 40}, nil
		}
		return []string{"ok"}, nil
	})
	m := runXiaomi(t, example(t, "xiaomi"), xiaomiFile(xiaomiDevice("chuangmi.plug.v3")))
	eventually(t, "the plug", func() bool { d, ok := xiaomiOne(m); return ok && d.Online })
	d, _ := xiaomiOne(m)
	if xiaomiValue(d, "power") != true || xiaomiValue(d, "usb") != true || xiaomiValue(d, "temperature") != 40.0 || d.Kind != "plug" {
		t.Errorf("got %+v", d)
	}
	if err := m.Write("xiaomi:"+xiaomiDID, "usb", false); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the command", func() bool { return len(miio.methods("set_usb_off")) == 1 })
}

// Some models have a spec on the server and don't take MIoT calls on the network. An older family's commands take over.
func TestXiaomiSpecFallsBackWhenMiotIsRefused(t *testing.T) {
	notMiot := func() (any, any) { return nil, map[string]any{"code": -32601, "message": "Method not found."} }
	t.Run("to its family", func(t *testing.T) {
		urn := "urn:miot-spec-v2:device:air-purifier:0000A007:zhimi-v6:1"
		startMiio(t, func(c miioCall) (any, any) {
			switch c.Method {
			case "get_properties":
				return notMiot()
			case "get_prop":
				return []any{"on", "auto"}, nil
			}
			return []string{"ok"}, nil
		})
		specs := startSpecs(t, map[string]string{urn: `{"services":[{"iid":2,"type":"urn:miot-spec-v2:service:air-purifier:00007811:x:1","properties":[{"iid":1,"type":"urn:miot-spec-v2:property:on:00000006:x:1","format":"bool","access":["read","write"]}]}]}`})
		m := runXiaomiSpec(t, xiaomiSpecSource(t, specs, true), specs, xiaomiFile(xiaomiSpecDevice(xiaomiDID, "zhimi.airpurifier.v6", urn)))
		xiaomiWait(t, "its older commands", func() bool {
			d, ok := xiaomiOne(m)
			return ok && d.Online && xiaomiValue(d, "mode") == "auto"
		})
	})
	// The second is what a real Roborock S5 does: an ordinary result that isn't a list.
	for name, answer := range map[string]func(miioCall) (any, any){
		"or says so":                     func(miioCall) (any, any) { return notMiot() },
		"or says so, for a plain result": func(miioCall) (any, any) { return "unknown_method", nil },
	} {
		t.Run(name, func(t *testing.T) {
			miio := startMiio(t, answer)
			specs := startSpecs(t, heaterSpec(t))
			m := runXiaomiSpec(t, xiaomiSpecSource(t, specs, true), specs, xiaomiFile(xiaomiSpecDevice(xiaomiDID, "acme.heater.h1", xiaomiHeaterUrn)))
			xiaomiWait(t, "the note", func() bool {
				d, ok := xiaomiOne(m)
				note, _ := xiaomiValue(d, "unavailable").(string)
				return ok && strings.Contains(note, "not to MIoT")
			})
			n := len(miio.methods("get_properties"))
			time.Sleep(1200 * time.Millisecond)
			if got := len(miio.methods("get_properties")); got != n {
				t.Errorf("asked %d more times", got-n)
			}
		})
	}
}

// Every Roborock keeps its own commands. Fan numbers differ by generation, so another model's speed is only shown.
func TestXiaomiOtherRoborocksUseTheirDriver(t *testing.T) {
	startMiio(t, func(c miioCall) (any, any) {
		if c.Method == "get_status" {
			return []map[string]any{{"fan_power": 60, "state": 8, "battery": 95, "error_code": 0}}, nil
		}
		return nil, map[string]any{"code": -32601, "message": "unknown_method"}
	})
	m := runXiaomi(t, example(t, "xiaomi"), xiaomiFile(xiaomiDevice("rockrobo.vacuum.v1")))
	eventually(t, "the vacuum", func() bool { d, ok := xiaomiOne(m); return ok && d.Online })
	d, _ := xiaomiOne(m)
	speed, _ := dpOf(d, "fan_speed")
	if d.Kind != "vacuum" || speed.Writable || speed.Value != 60.0 || xiaomiValue(d, "state") != "charging" {
		t.Errorf("got %+v", d)
	}
}

// Every type on the official list has a card. A device with no local address is enough to see its kind, with no request.
func TestXiaomiEveryDeviceTypeHasAKnownKind(t *testing.T) {
	data, err := os.ReadFile(example(t, "xiaomi") + "/kinds.ts")
	if err != nil {
		t.Fatal(err)
	}
	known := []string{"light", "switch", "plug", "cover", "climate", "air", "fan", "vacuum", "sensor", "safety", "lock", "appliance", "infrastructure"}
	want := map[string]string{}
	var devices []map[string]any
	for _, match := range regexp.MustCompile(`(?m)^  "([a-z0-9-]+)": "([a-z]+)",$`).FindAllStringSubmatch(string(data), -1) {
		if !slices.Contains(known, match[2]) {
			t.Errorf("%s has the kind %s", match[1], match[2])
		}
		want[match[1]] = match[2]
		d := xiaomiSpecDevice("blt.3."+match[1], "x.y.z", "urn:miot-spec-v2:device:"+match[1]+":0000A001:x-y:1")
		d["localip"] = ""
		devices = append(devices, d)
	}
	if len(want) != 200 {
		t.Fatalf("%d types: the list had 202 on 2026-11-01, without other and test-xjpl", len(want))
	}
	other := xiaomiSpecDevice("blt.3.mystery", "x.y.z", "urn:miot-spec-v2:device:mystery:0000A001:x-y:1")
	m := runXiaomi(t, example(t, "xiaomi"), xiaomiFile(append(devices, other)...))
	eventually(t, "the devices", func() bool { return len(m.Devices()) == 201 })
	for name, kind := range want {
		if d := xiaomiById(m, "blt.3."+name); d.Kind != kind {
			t.Errorf("%s: %s, want %s", name, d.Kind, kind)
		}
	}
	if d := xiaomiById(m, "blt.3.mystery"); d.Kind != "other" {
		t.Errorf("a type nobody listed: %s", d.Kind)
	}
	noViolations(t, m)
}

// A wrong guess must not open a door: a lock reports, whatever its spec says it can do.
func TestXiaomiSpecLocksOnlyReport(t *testing.T) {
	urn := "urn:miot-spec-v2:device:lock:0000A038:x-l1:1"
	spec := `{"services":[{"iid":2,"type":"urn:miot-spec-v2:service:lock:00007816:x:1","properties":[
		{"iid":1,"type":"urn:miot-spec-v2:property:status:00000007:x:1","format":"uint8","access":["read"]},
		{"iid":2,"type":"urn:miot-spec-v2:property:on:00000006:x:1","format":"bool","access":["read","write"]}],
		"actions":[{"iid":1,"type":"urn:miot-spec-v2:action:open:00002899:x:1","in":[]}]}]}`
	startMiio(t, answerProperties(map[string]any{"2.1": 1, "2.2": true}))
	specs := startSpecs(t, map[string]string{urn: spec})
	m := runXiaomiSpec(t, xiaomiSpecSource(t, specs, false), specs, xiaomiFile(xiaomiSpecDevice(xiaomiDID, "x.lock.l1", urn)))
	xiaomiWait(t, "the lock", func() bool {
		d, ok := xiaomiOne(m)
		return ok && xiaomiValue(d, "power") == true
	})
	d, _ := xiaomiOne(m)
	if d.Kind != "lock" {
		t.Errorf("kind %s", d.Kind)
	}
	for _, dp := range d.DPs {
		if dp.Writable || dp.Type == device.Action {
			t.Errorf("%+v", dp)
		}
	}
}

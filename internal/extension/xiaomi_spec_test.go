package extension

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"home-control/device"
	"home-control/internal/store"
)

const xiaomiHeaterUrn = "urn:miot-spec-v2:device:heater:0000A01A:acme-h1:1"

// fakeSpecs is miot-spec.org, served to the real host's fetch.
type fakeSpecs struct {
	server *httptest.Server
	mu     sync.Mutex
	specs  map[string]string
	status int
	asked  []string
}

func startSpecs(t *testing.T, specs map[string]string) *fakeSpecs {
	t.Helper()
	f := &fakeSpecs{specs: specs}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.asked = append(f.asked, r.URL.RawQuery)
		if f.status != 0 {
			w.WriteHeader(f.status)
			return
		}
		spec, ok := f.specs[r.URL.Query().Get("type")]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		fmt.Fprint(w, spec)
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeSpecs) setStatus(status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status = status
}

func (f *fakeSpecs) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.asked)
}

func heaterSpec(t *testing.T) map[string]string {
	t.Helper()
	data, err := os.ReadFile("testdata/xiaomi_spec_heater.json")
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{xiaomiHeaterUrn: string(data)}
}

// The extension pointed at the fake: the only way to reach a local server over http is a host from a setting.
func xiaomiSpecSource(t *testing.T, f *fakeSpecs, fast bool, replace ...string) string {
	t.Helper()
	all := append([]string{
		`"https://miot-spec.org/miot-spec-v2/instance?type="`, `"` + f.server.URL + `/instance?type="`,
		`network: ["miot-spec.org"]`, `network: ["{host}"]`,
		`const settings = {`, `const settings = { host: { label: "Spec host", type: "string", required: true },`,
	}, replace...)
	prefix := ""
	if fast {
		prefix = xiaomiFastClock
		all = append(all, "everySeconds: 10", "everySeconds: 1", "TIMEOUT_MS = 1500", "TIMEOUT_MS = 100", "SWEEP_TIMEOUT_MS = 1000", "SWEEP_TIMEOUT_MS = 100")
	}
	return xiaomiCopy(t, prefix, all...)
}

func runXiaomiSpec(t *testing.T, source string, f *fakeSpecs, file string, setup ...func(*Manager)) *Manager {
	t.Helper()
	return run(t, source, map[string]any{"devices": "secrets/xiaomi.json", "host": strings.TrimPrefix(f.server.URL, "http://")}, append([]func(*Manager){allowLoopback, func(m *Manager) {
		write(t, m.configDir, "secrets/xiaomi.json", file)
	}}, setup...)...)
}

func xiaomiSpecDevice(did, model, urn string) map[string]any {
	d := xiaomiDevice(model)
	d["did"] = did
	if urn != "" {
		d["spec_type"] = urn
	}
	return d
}

func xiaomiWait(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for " + what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func dpOf(d device.Device, code string) (device.DP, bool) {
	for _, dp := range d.DPs {
		if dp.Code == code {
			return dp, true
		}
	}
	return device.DP{}, false
}

func noViolations(t *testing.T, m *Manager) {
	t.Helper()
	if s := status(m); len(s.Violations) != 0 || s.Error != "" {
		t.Errorf("violations %v, error %q", s.Violations, s.Error)
	}
}

var heaterReadings = map[string]any{
	"2.1": true, "2.2": 1, "2.3": 1, "2.4": 20.5, "2.5": 2, "3.1": 21.04, "3.2": 40, "3.3": 1, "4.1": false, "4.2": 0xff8000,
	"5.1": 80, "7.1": -1.5, "7.2": 0.123456, "7.3": "hello", "7.6": 300,
}

func runHeater(t *testing.T) (*fakeMiio, *fakeSpecs, *Manager) {
	t.Helper()
	miio := startMiio(t, answerProperties(heaterReadings))
	specs := startSpecs(t, heaterSpec(t))
	m := runXiaomiSpec(t, xiaomiSpecSource(t, specs, false), specs, xiaomiFile(xiaomiSpecDevice(xiaomiDID, "acme.heater.h1", xiaomiHeaterUrn)))
	// The first poll takes the spec and one property, which proves the token. The rest come straight after, in the same read.
	xiaomiWait(t, "the heater to read", func() bool {
		d, ok := xiaomiOne(m)
		return ok && d.Online && xiaomiValue(d, "power") == true
	})
	return miio, specs, m
}

func TestXiaomiSpecDeviceReads(t *testing.T) {
	miio, specs, m := runHeater(t)
	d, _ := xiaomiOne(m)
	if d.Kind != "climate" || d.Product != "acme.heater.h1" {
		t.Errorf("got %+v", d)
	}
	for code, want := range map[string]any{
		"power": true, "fault": "tilted", "mode": "low", "temp_set": 205.0, "temp_current": 21.04, "humidity": 40.0, "environment_mode": "indoor",
		"indicator_light_on": false, "indicator_light_color": "#ff8000", "indicator_light_color_hue": 30.0, "indicator_light_color_saturation": 100.0,
		"filter_life": 80.0, "custom_offset": -15.0, "custom_fine": 0.1235, "custom_label": "hello", "custom_counter": 300.0, "custom_boost": nil, "preset": 2.0,
	} {
		if got := xiaomiValue(d, code); got != want {
			t.Errorf("%s = %v, want %v", code, got, want)
		}
	}
	// Repeated and empty names can still be told apart.
	fault, _ := dpOf(d, "fault")
	if !device.Equal(fault.Values["range"], []string{"no faults", "tilted", "2", "tilted (3)"}) {
		t.Errorf("fault choices %v", fault.Values["range"])
	}
	for code, writable := range map[string]bool{
		"power": true, "mode": true, "temp_set": true, "preset": true, "custom_offset": true, "custom_boost": true, "indicator_light_color_hue": true,
		"temp_current": false, "fault": false, "custom_fine": false, "custom_label": false, "custom_counter": false, "filter_life": false, "humidity": false,
	} {
		if dp, ok := dpOf(d, code); !ok || dp.Writable != writable {
			t.Errorf("%s: %+v, want writable %v", code, dp, writable)
		}
	}
	target, _ := dpOf(d, "temp_set")
	if target.Scale != 1 || target.Values["min"] != 160.0 || target.Values["max"] != 310.0 || target.Values["step"] != 5.0 || target.Values["unit"] != "°C" {
		t.Errorf("temp_set: %+v", target)
	}
	for _, code := range []string{"firmware_revision", "manufacturer", "filter_rfid_tag", "auto_upgrade", "firmware_upgrade_auto_upgrade", "custom_factory_mode", "custom_mystery", "custom_blob", "custom_event_only"} {
		if _, ok := dpOf(d, code); ok {
			t.Errorf("%s should stay hidden", code)
		}
	}
	for _, c := range miio.methods("get_properties") {
		text := string(c.Params)
		for _, did := range []string{`"1.1"`, `"1.4"`, `"5.2"`, `"6.1"`, `"7.4"`, `"7.7"`, `"7.8"`, `"7.9"`, `"7.10"`} {
			if strings.Contains(text, did) {
				t.Errorf("%s was asked for: %s", did, text)
			}
		}
	}
	// The urn goes to the spec server, and nothing else.
	if got := specs.requests(); len(got) != 1 || got[0] != "type="+xiaomiHeaterUrn {
		t.Errorf("asked %v", got)
	}
	noViolations(t, m)
}

func TestXiaomiSpecWrites(t *testing.T) {
	miio, _, m := runHeater(t)
	for _, w := range []struct {
		code  string
		value any
		sent  string
	}{
		{"power", false, `[{"did":"2.1","siid":2,"piid":1,"value":false}]`},
		{"mode", "auto", `[{"did":"2.3","siid":2,"piid":3,"value":0}]`},
		{"temp_set", float64(205), `[{"did":"2.4","siid":2,"piid":4,"value":20.5}]`},
		{"custom_offset", float64(3), `[{"did":"7.1","siid":7,"piid":1,"value":0.3}]`},
		{"custom_offset", float64(-15), `[{"did":"7.1","siid":7,"piid":1,"value":-1.5}]`},
		{"custom_boost", float64(4), `[{"did":"7.4","siid":7,"piid":4,"value":4}]`},
		{"indicator_light_color_hue", float64(240), `[{"did":"4.2","siid":4,"piid":2,"value":255}]`}, // the saturation it last read stays
	} {
		before := len(miio.methods("set_properties"))
		if err := m.Write("xiaomi:"+xiaomiDID, w.code, w.value); err != nil {
			t.Fatal(w.code, err)
		}
		eventually(t, w.code+" to be sent", func() bool { return len(miio.methods("set_properties")) > before })
		if got := string(miio.methods("set_properties")[before].Params); got != w.sent {
			t.Errorf("%s sent %s, want %s", w.code, got, w.sent)
		}
	}
	if err := m.Write("xiaomi:"+xiaomiDID, "command", "toggle"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the action", func() bool { return len(miio.methods("action")) == 1 })
	before := len(miio.methods("set_properties"))
	for _, refused := range []struct {
		code  string
		value any
	}{
		{"temp_current", 1}, {"fault", "tilted"}, {"filter_life", 100}, {"custom_label", "x"}, {"custom_fine", 1}, {"custom_counter", 1},
		{"firmware_revision", "1"}, {"custom_factory_mode", true}, {"custom_mystery", true}, {"auto_upgrade", true},
		{"mode", "factory reset"}, // a device may report that choice, but not be asked for it
		{"temp_set", 999},
		{"command", "factory_reset"}, {"command", "reset_filter_life"}, {"command", "set_program"},
	} {
		if err := m.Write("xiaomi:"+xiaomiDID, refused.code, refused.value); err == nil {
			t.Errorf("%s = %v was accepted", refused.code, refused.value)
		}
	}
	time.Sleep(200 * time.Millisecond)
	if n := len(miio.methods("set_properties")); n != before {
		t.Errorf("%d refused writes reached the device", n-before)
	}
	if n := len(miio.methods("action")); n != 1 {
		t.Errorf("%d actions reached the device", n)
	}
}

func TestXiaomiSpecCardsNeverLie(t *testing.T) {
	// A device sends what it likes. Text where a number belongs is unknown, not a number on a slider.
	values := map[string]any{"2.1": "maybe", "2.4": "hot", "3.2": 41.6, "2.3": 7}
	startMiio(t, answerProperties(values))
	specs := startSpecs(t, heaterSpec(t))
	m := runXiaomiSpec(t, xiaomiSpecSource(t, specs, false), specs, xiaomiFile(xiaomiSpecDevice(xiaomiDID, "acme.heater.h1", xiaomiHeaterUrn)))
	xiaomiWait(t, "a full read", func() bool {
		d, ok := xiaomiOne(m)
		return ok && d.Online && xiaomiValue(d, "mode") == "7"
	})
	d, _ := xiaomiOne(m)
	if xiaomiValue(d, "power") != nil || xiaomiValue(d, "temp_set") != nil || xiaomiValue(d, "humidity") != 42.0 {
		t.Errorf("got %+v", d.DPs)
	}
}

// -4001 and its kin are a property the device doesn't have. It leaves the card, and the device stays online.
func TestXiaomiUnsupportedPropertiesAreHidden(t *testing.T) {
	values := map[string]any{}
	for k, v := range heaterReadings {
		values[k] = v
	}
	miio := startMiio(t, answerProperties(values)) // anything not in there answers -4001, such as 7.5
	specs := startSpecs(t, heaterSpec(t))
	m := runXiaomiSpec(t, xiaomiSpecSource(t, specs, true), specs, xiaomiFile(xiaomiSpecDevice(xiaomiDID, "acme.heater.h1", xiaomiHeaterUrn)))
	xiaomiWait(t, "custom_gone to leave", func() bool {
		d, ok := xiaomiOne(m)
		if !ok || !d.Online {
			return false
		}
		_, there := dpOf(d, "custom_gone")
		return !there && xiaomiValue(d, "power") == true
	})
	miio.set(func() { miio.calls = nil })
	time.Sleep(1500 * time.Millisecond)
	for _, c := range miio.methods("get_properties") {
		if strings.Contains(string(c.Params), `"7.5"`) {
			t.Fatal("it's still asked for")
		}
	}
	if len(miio.methods("get_properties")) == 0 {
		t.Fatal("reads stopped")
	}
	if err := m.Write("xiaomi:"+xiaomiDID, "custom_gone", 1); err == nil {
		t.Error("a hidden property was written")
	}
}

func TestXiaomiKnownModelsAskNobody(t *testing.T) {
	startMiio(t, answerProperties(map[string]any{"2.1": true}))
	specs := startSpecs(t, heaterSpec(t))
	file := xiaomiFile(xiaomiSpecDevice(xiaomiDID, "dmaker.fan.1c", "urn:miot-spec-v2:device:fan:0000A005:dmaker-1c:1"))
	m := runXiaomiSpec(t, xiaomiSpecSource(t, specs, false), specs, file)
	eventually(t, "the fan", func() bool { d, ok := xiaomiOne(m); return ok && d.Online })
	if got := specs.requests(); len(got) != 0 {
		t.Errorf("asked %v about a model with a table", got)
	}
}

func TestXiaomiSpecUnavailableLeavesOthersFine(t *testing.T) {
	startMiio(t, answerProperties(map[string]any{"2.1": true}))
	specs := startSpecs(t, heaterSpec(t))
	specs.setStatus(http.StatusBadGateway)
	file := xiaomiFile(xiaomiSpecDevice("1", "acme.heater.h1", xiaomiHeaterUrn), xiaomiSpecDevice(xiaomiDID, "dmaker.fan.1c", ""))
	m := runXiaomiSpec(t, xiaomiSpecSource(t, specs, true), specs, file)
	xiaomiWait(t, "the fan to read", func() bool { return xiaomiValue(xiaomiById(m, xiaomiDID), "power") == true })
	heater := xiaomiById(m, "1")
	note, _ := xiaomiValue(heater, "unavailable").(string)
	if heater.Online || !strings.Contains(note, "Waiting") || heater.Kind != "climate" || len(heater.DPs) != 1 {
		t.Errorf("got %+v", heater)
	}
	noViolations(t, m)
	specs.setStatus(0)
	xiaomiWait(t, "it to retry", func() bool { return xiaomiValue(xiaomiById(m, "1"), "power") == true })
	if xiaomiValue(xiaomiById(m, "1"), "unavailable") != "missing" {
		t.Error("the note stayed")
	}
	noViolations(t, m)
}

func TestXiaomiSpecBadAnswersDontGuess(t *testing.T) {
	for name, spec := range map[string]string{"not JSON": "<html>busy</html>", "not a spec": `{"services":"none"}`, "nonsense": `{"services":[null,{"iid":"x"},{"iid":2,"type":"urn:x:service:a:1","properties":[null,{"iid":1}],"actions":"no"}]}`} {
		t.Run(name, func(t *testing.T) {
			miio := startMiio(t, answerProperties(nil))
			specs := startSpecs(t, map[string]string{xiaomiHeaterUrn: spec})
			m := runXiaomiSpec(t, xiaomiSpecSource(t, specs, false), specs, xiaomiFile(xiaomiSpecDevice(xiaomiDID, "acme.heater.h1", xiaomiHeaterUrn)))
			eventually(t, "the device", func() bool { _, ok := xiaomiOne(m); return ok })
			d, _ := xiaomiOne(m)
			if _, ok := xiaomiValue(d, "unavailable").(string); !ok || d.Online {
				t.Errorf("got %+v", d)
			}
			if n := len(miio.methods("get_properties")); n != 0 {
				t.Errorf("%d reads from no spec", n)
			}
			noViolations(t, m)
		})
	}
}

func TestXiaomiSpecNotOnServer(t *testing.T) {
	startMiio(t, answerProperties(nil))
	specs := startSpecs(t, map[string]string{})
	m := runXiaomiSpec(t, xiaomiSpecSource(t, specs, false), specs, xiaomiFile(xiaomiSpecDevice(xiaomiDID, "acme.heater.h1", xiaomiHeaterUrn)))
	eventually(t, "the note", func() bool {
		d, ok := xiaomiOne(m)
		note, _ := xiaomiValue(d, "unavailable").(string)
		return ok && strings.Contains(note, "has no description")
	})
}

func storedBytes(t *testing.T, s store.Store) int {
	t.Helper()
	saved, err := s.ExtensionStorage(t.Context(), "xiaomi")
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for key, value := range saved {
		total += len(key) + len(value)
	}
	return total
}

// A restart with no internet still knows the model, and however many there are it stays inside what it may keep.
func TestXiaomiSpecCache(t *testing.T) {
	startMiio(t, answerProperties(heaterReadings))
	spec := heaterSpec(t)[xiaomiHeaterUrn]
	specs := map[string]string{}
	var devices []map[string]any
	for i := range 8 {
		urn := fmt.Sprintf("urn:miot-spec-v2:device:heater:0000A01A:acme-h%d:1", i)
		specs[urn] = spec
		devices = append(devices, xiaomiSpecDevice(fmt.Sprint(i+1), "acme.heater.h", urn))
	}
	server := startSpecs(t, specs)
	shared := store.NewMemory()
	useStore := func(m *Manager) { m.store = shared }
	const limit = 12000
	source := xiaomiSpecSource(t, server, true, "SPEC_STORE_LIMIT = 900 * 1024", fmt.Sprintf("SPEC_STORE_LIMIT = %d", limit), "64 * 1024", "1024")
	m := runXiaomiSpec(t, source, server, xiaomiFile(devices...), useStore)
	xiaomiWait(t, "every spec", func() bool {
		for i := range 8 {
			if _, ok := dpOf(xiaomiById(m, fmt.Sprint(i+1)), "power"); !ok {
				return false
			}
		}
		return true
	})
	if got := storedBytes(t, shared); got == 0 || got > limit {
		t.Errorf("stored %d bytes with a limit of %d", got, limit)
	}
	noViolations(t, m)
	stopAll(m)

	// The newest specs are still saved, and need nothing from a server that's gone.
	server.setStatus(http.StatusBadGateway)
	before := len(server.requests())
	again := runXiaomiSpec(t, source, server, xiaomiFile(devices[7]), useStore)
	xiaomiWait(t, "the saved spec", func() bool {
		_, ok := dpOf(xiaomiById(again, "8"), "power")
		return ok
	})
	if n := len(server.requests()); n != before {
		t.Errorf("%d more requests", n-before)
	}
}

func TestXiaomiSpecMultiGangAndRepeats(t *testing.T) {
	gang := func(iid int) string {
		return fmt.Sprintf(`{"iid":%d,"type":"urn:miot-spec-v2:service:switch:0000780C:x:1","properties":[
			{"iid":1,"type":"urn:miot-spec-v2:property:on:00000006:x:1","format":"bool","access":["read","write"]},
			{"iid":2,"type":"urn:miot-spec-v2:property:mode:00000008:x:1","format":"uint8","access":["read"]},
			{"iid":3,"type":"urn:miot-spec-v2:property:mode:00000008:x:1","format":"uint8","access":["read"]}]}`, iid)
	}
	urn := "urn:miot-spec-v2:device:switch:0000A003:x-sw2:1:0000C810"
	spec := `{"services":[` + gang(2) + `,` + gang(3) + `,{"iid":4,"type":"urn:miot-spec-v2:service:indicator-light:00007803:x:1","properties":[{"iid":1,"type":"urn:miot-spec-v2:property:on:00000006:x:1","format":"bool","access":["read","write"]}]}]}`
	startMiio(t, answerProperties(map[string]any{"2.1": true, "3.1": false}))
	specs := startSpecs(t, map[string]string{urn: spec})
	m := runXiaomiSpec(t, xiaomiSpecSource(t, specs, false), specs, xiaomiFile(xiaomiSpecDevice(xiaomiDID, "x.switch.sw2", urn)))
	xiaomiWait(t, "both channels", func() bool {
		d, ok := xiaomiOne(m)
		return ok && xiaomiValue(d, "switch_1") == true && xiaomiValue(d, "switch_2") == false
	})
	d, _ := xiaomiOne(m)
	if d.Kind != "switch" {
		t.Errorf("kind %s", d.Kind)
	}
	if _, ok := dpOf(d, "power"); ok {
		t.Errorf("one channel of several isn't the device's power: %+v", d.DPs)
	}
	noViolations(t, m) // the host refuses a device with a repeated code
}

// A list too long for a device gets no answer, or a refusal. It's read in smaller calls, and the device stays.
func TestXiaomiBatchSizeFollowsTheDevice(t *testing.T) {
	for name, tooLong := range map[string]func() (any, any){
		"silence": nil,
		"refused": func() (any, any) { return nil, map[string]any{"code": -5001, "message": "too long"} },
	} {
		t.Run(name, func(t *testing.T) {
			miio := startMiio(t, nil)
			plain := answerProperties(heaterReadings)
			miio.answer = func(c miioCall) (any, any) {
				var params []any
				json.Unmarshal(c.Params, &params)
				if len(params) > 4 {
					if tooLong == nil {
						return nil, "silent"
					}
					return tooLong()
				}
				return plain(c)
			}
			if tooLong == nil {
				miio.dropOver = 4
			}
			specs := startSpecs(t, heaterSpec(t))
			m := runXiaomiSpec(t, xiaomiSpecSource(t, specs, true), specs, xiaomiFile(xiaomiSpecDevice(xiaomiDID, "acme.heater.h1", xiaomiHeaterUrn)))
			xiaomiWait(t, "a full read", func() bool {
				d, ok := xiaomiOne(m)
				return ok && d.Online && xiaomiValue(d, "custom_counter") == 300.0 && xiaomiValue(d, "power") == true
			})
			noViolations(t, m)
		})
	}
}

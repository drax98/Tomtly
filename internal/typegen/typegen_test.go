package typegen

import (
	"strings"
	"testing"

	"tomtly/device"
	"tomtly/plugin"
)

func TestDevicesBecomeTypes(t *testing.T) {
	out := Generate([]device.Device{{ID: "tv", Name: "Living room */ TV", Kind: "plug", DPs: []device.DP{
		{DP: "1", Code: "switch", Type: device.Boolean, Writable: true},
		{DP: "2", Code: "mode", Type: device.Enum, Writable: true, Values: map[string]any{"range": []any{"cold", "heat"}}},
		{DP: "3", Code: "temp", Type: device.Integer, Writable: true, Scale: 1, Values: map[string]any{"min": 50.0, "max": 300.0, "unit": "°C"}},
		{DP: "4", Code: "battery", Type: device.Integer},
		{DP: "5", Code: "start", Type: device.Action, Writable: true, Values: map[string]any{"range": []string{"go"}}},
	}}}, nil, nil)
	read, write, found := strings.Cut(out, "}, {")
	if !found {
		t.Fatalf("no read and write parts: %s", out)
	}
	for _, want := range []string{`"switch": boolean`, `"mode": "cold" | "heat"`, `"temp": number`, `"battery": number`, "50 to 300, °C", "divide by 10"} {
		if !strings.Contains(read, want) {
			t.Errorf("reading should have %s: %s", want, read)
		}
	}
	if strings.Contains(read, `"start"`) {
		t.Error("an action has nothing to read")
	}
	if strings.Contains(write, `"battery"`) || !strings.Contains(write, `"start": "go"`) || !strings.Contains(write, `"temp": number`) {
		t.Errorf("writing should only list writable codes: %s", write)
	}
	if strings.Contains(read, "*/ TV") {
		t.Error("a device name must not be able to end the comment")
	}
}

func TestPluginDataBecomesTypes(t *testing.T) {
	out := Generate(nil, []plugin.Status{
		{ID: "prices", Data: []byte(`{"current":0.5,"name":"secret-looking","hours":[{"at":1,"rain":null},{"at":2,"rain":3,"extra":true}],"none":[]}`)},
		{ID: "waiting"},
		{ID: "typed", Data: []byte(`{"x":1}`)},
	}, map[string]bool{"typed": true})
	for _, want := range []string{`"prices": {`, `"current": number`, `"name": string`, `"at": number`, `"rain": number | null`, `"extra"?: boolean`, `"none": (unknown)[]`, `"waiting": unknown`} {
		if !strings.Contains(out, want) {
			t.Errorf("should have %s:\n%s", want, out)
		}
	}
	if strings.Contains(out, "secret-looking") {
		t.Error("values must not reach the types")
	}
	if strings.Contains(out, `"typed"`) {
		t.Error("a plugin with its own types shouldn't get generated ones")
	}
}

func TestEnumWithoutChoicesAndUnscaledNumbers(t *testing.T) {
	out := Generate([]device.Device{{ID: "d", Name: "D", Kind: "plug", DPs: []device.DP{
		{DP: "1", Code: "mode", Type: device.Enum, Values: map[string]any{"range": []any{}}},
		{DP: "2", Code: "level", Type: device.Integer, Scale: 0},
		{DP: "3", Code: "fine", Type: device.Integer, Scale: 2},
	}}}, nil, nil)
	if !strings.Contains(out, `"mode": string;`) {
		t.Errorf("an enum with no listed choices is a plain string:\n%s", out)
	}
	if strings.Contains(out, "scale 0") || strings.Contains(out, "divide by 1 ") || strings.Contains(out, "divide by 1\n") {
		t.Errorf("an unscaled number needs no scale note:\n%s", out)
	}
	if !strings.Contains(out, "scale 2, so divide by 100") {
		t.Errorf("a scaled number says how to read it:\n%s", out)
	}
}

func nested(open, close string, levels int, leaf string) string {
	return strings.Repeat(open, levels) + leaf + strings.Repeat(close, levels)
}

func TestDeepPluginDataStopsAtTheDepthLimit(t *testing.T) {
	gen := func(data string) string {
		return Generate(nil, []plugin.Status{{ID: "p", Data: []byte(data)}}, nil)
	}
	// Eight levels down is still described; the ninth is not.
	if out := gen(nested(`{"a":`, "}", 8, "1")); !strings.Contains(out, "number") || strings.Contains(out, "any") {
		t.Errorf("8 levels:\n%s", out)
	}
	if out := gen(nested(`{"a":`, "}", 9, "1")); !strings.Contains(out, "any") || strings.Contains(out, "number") {
		t.Errorf("9 levels:\n%s", out)
	}
	if out := gen(nested("[", "]", 8, "1")); !strings.Contains(out, "number") || strings.Contains(out, "any") {
		t.Errorf("8 list levels:\n%s", out)
	}
	if out := gen(nested("[", "]", 9, "1")); !strings.Contains(out, "any") || strings.Contains(out, "number") {
		t.Errorf("9 list levels:\n%s", out)
	}
}

func TestNullComesLastInAUnion(t *testing.T) {
	out := Generate(nil, []plugin.Status{{ID: "p", Data: []byte(`{"v":[null,1,"x",null]}`)}}, nil)
	if !strings.Contains(out, `"p": {`) || !strings.Contains(out, `"v": (number | string | null)[]`) {
		t.Errorf("want null last, other types in the order seen:\n%s", out)
	}
	out = Generate(nil, []plugin.Status{{ID: "p", Data: []byte(`{"v":[1,null,2]}`)}}, nil)
	if !strings.Contains(out, `"v": (number | null)[]`) {
		t.Errorf("%s", out)
	}
}

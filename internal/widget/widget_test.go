package widget

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCleanKeepsOnlyWhatThePageKnows(t *testing.T) {
	out, err := Clean([]byte(`[{"type":"stat","label":"T","value":21.5,"unit":"°C","onclick":"alert(1)"},{"type":"button","label":"Go","action":"go","args":{"room":"Hall"}}]`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "onclick") || !strings.Contains(string(out), `"args":{"room":"Hall"}`) {
		t.Errorf("cleaned: %s", out)
	}
	if !HasAction(out, "go") || HasAction(out, "stop") {
		t.Error("HasAction")
	}
	if out, err := Clean([]byte(`[{"type":"group","label":"Szafka"},{"type":"text","text":"a"}]`)); err != nil || string(out) != `[{"type":"group","label":"Szafka"},{"type":"text","text":"a"}]` {
		t.Errorf("a group keeps its label: %s %v", out, err)
	}
	if out, err := Clean([]byte(`[{"type":"device","device":"t","code":"temp_set","min":5,"max":35}]`)); err != nil || !strings.Contains(string(out), `"min":5,"max":35`) {
		t.Errorf("a device block keeps its range: %s %v", out, err)
	}
	if out, err := Clean([]byte(`null`)); err != nil || string(out) != "[]" {
		t.Errorf("nothing to show is no blocks: %s %v", out, err)
	}
}

func TestCleanRefuses(t *testing.T) {
	long := strings.Repeat("x", 1001)
	for want, raw := range map[string]string{
		"list of blocks":   `{"type":"text"}`,
		"unknown type":     `[{"type":"html","text":"<b>"}]`,
		"needs text":       `[{"type":"text"}]`,
		"stat needs":       `[{"type":"stat"}]`,
		"group needs":      `[{"type":"group"}]`,
		"value is":         `[{"type":"stat","label":"a","value":{"x":1}}]`,
		"tone is":          `[{"type":"text","text":"a","tone":"red"}]`,
		"text is up to":    `[{"type":"text","text":"` + long + `"}]`,
		"a row is":         `[{"type":"rows","rows":[{"label":"a","value":[1]}]}]`,
		"device and code":  `[{"type":"device","device":"lamp"}]`,
		"min is below":     `[{"type":"device","device":"t","code":"temp_set","min":30,"max":5}]`,
		"needs a name":     `[{"type":"number","name":"1x"}]`,
		"needs 1 to":       `[{"type":"select","name":"a"}]`,
		"needs a label":    `[{"type":"button","label":"Go","action":"a:b"}]`,
		"args is":          `[{"type":"button","label":"Go","action":"go","args":[1]}]`,
		"holds up to 40":   `[` + strings.TrimSuffix(strings.Repeat(`{"type":"text","text":"a"},`, 41), ",") + `]`,
		"over 32768 bytes": `[` + strings.TrimSuffix(strings.Repeat(`{"type":"text","text":"`+long[:999]+`"},`, 35), ",") + `]`,
	} {
		if _, err := Clean([]byte(raw)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
	}
}

func TestDialogAndTime(t *testing.T) {
	raw := `[{"type":"dialog","label":"Edit program","title":"Salon","action":"save","args":{"room":"Salon"},"blocks":[` +
		`{"type":"group","label":"Workdays"},{"type":"time","name":"w1","value":"06:30","step":15},{"type":"number","name":"t1","value":21}]}]`
	out, err := Clean([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"title":"Salon"`) || !strings.Contains(string(out), `{"type":"time","value":"06:30","name":"w1","step":15}`) {
		t.Errorf("a dialog keeps its title and form: %s", out)
	}
	if !HasAction(out, "save") {
		t.Error("a dialog's action is one the widget shows")
	}
	if _, err := Clean([]byte(`[{"type":"time","name":"at"}]`)); err != nil {
		t.Errorf("a time on its own: %v", err)
	}
}

func TestDialogAndTimeRefuse(t *testing.T) {
	for want, raw := range map[string]string{
		"a dialog needs a label":    `[{"type":"dialog","action":"save","blocks":[{"type":"text","text":"a"}]}]`,
		"holds 1 to 40":             `[{"type":"dialog","label":"E","action":"save"}]`,
		`can't hold a "button"`:     `[{"type":"dialog","label":"E","action":"save","blocks":[{"type":"button","label":"x","action":"y"}]}]`,
		`can't hold a "dialog"`:     `[{"type":"dialog","label":"E","action":"save","blocks":[{"type":"dialog","label":"x","action":"y","blocks":[{"type":"text","text":"a"}]}]}]`,
		`can't hold a "device"`:     `[{"type":"dialog","label":"E","action":"save","blocks":[{"type":"device","device":"d","code":"c"}]}]`,
		"dialog block 1: an input":  `[{"type":"dialog","label":"E","action":"save","blocks":[{"type":"number"}]}]`,
		"only a dialog holds":       `[{"type":"text","text":"a","blocks":[{"type":"text","text":"b"}]}]`,
		`a time's value is "HH:MM"`: `[{"type":"time","name":"a","value":"24:00"}]`,
		"whole minutes":             `[{"type":"time","name":"a","step":7.5}]`,
	} {
		if _, err := Clean([]byte(raw)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
	}
}

func TestScheduleAndChoice(t *testing.T) {
	raw := `[{"type":"choice","name":"mode","label":"Mode","value":"Auto","options":["Auto","Manual"],"action":"mode","args":{"room":"Salon"}},` +
		`{"type":"dialog","label":"Program","action":"save","blocks":[` +
		`{"type":"schedule","name":"week","label":"Mon–Fri","now":1,"min":5,"max":30,"step":15,"unit":"°C","periods":[{"time":"06:00","temp":21},{"time":"22:00","temp":18},{}]},` +
		`{"type":"choice","name":"fan","label":"Fan","options":["Low","High"],"action":"fan"}]}]`
	out, err := Clean([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"periods":[{"time":"06:00","temp":21},{"time":"22:00","temp":18},{}],"now":1`) {
		t.Errorf("a schedule keeps its periods and now: %s", out)
	}
	if !HasAction(out, "mode") || !HasAction(out, "fan") || !HasAction(out, "save") || HasAction(out, "other") {
		t.Error("HasAction finds a choice at the top and in a dialog")
	}
}

func TestScheduleAndChoiceRefuse(t *testing.T) {
	inDialog := func(b string) string { return `[{"type":"dialog","label":"E","action":"save","blocks":[` + b + `]}]` }
	thirteen := strings.TrimSuffix(strings.Repeat(`{},`, 13), ",")
	for want, raw := range map[string]string{
		"goes in a dialog":            `[{"type":"schedule","name":"w","label":"W","periods":[{}]}]`,
		"a schedule needs a name":     inDialog(`{"type":"schedule","name":"1w","label":"W","periods":[{}]}`),
		"needs a name: letters":       inDialog(`{"type":"schedule","name":"w","periods":[{}]}`),
		"holds 1 to 12 periods":       inDialog(`{"type":"schedule","name":"w","label":"W","periods":[` + thirteen + `]}`),
		"schedule holds 1 to 12":      inDialog(`{"type":"schedule","name":"w","label":"W"}`),
		`period 1: a time is "HH:MM"`: inDialog(`{"type":"schedule","name":"w","label":"W","periods":[{"time":"6:00"}]}`),
		"period 2: a temperature":     inDialog(`{"type":"schedule","name":"w","label":"W","periods":[{},{"temp":20.5}]}`),
		"period 1: a temperature is":  inDialog(`{"type":"schedule","name":"w","label":"W","min":5,"max":30,"periods":[{"temp":31}]}`),
		"min is below its max":        inDialog(`{"type":"schedule","name":"w","label":"W","min":30,"max":5,"periods":[{}]}`),
		"whole minutes":               inDialog(`{"type":"schedule","name":"w","label":"W","step":0,"periods":[{}]}`),
		"now is the index":            inDialog(`{"type":"schedule","name":"w","label":"W","now":1,"periods":[{}]}`),
		"a choice needs a label":      `[{"type":"choice","name":"m","options":["a","b"],"action":"m"}]`,
		"a choice needs a name":       `[{"type":"choice","label":"M","options":["a","b"],"action":"m"}]`,
		"2 to 4 options":              `[{"type":"choice","name":"m","label":"M","options":["a"],"action":"m"}]`,
		"an option is up to":          `[{"type":"choice","name":"m","label":"M","options":["a","` + strings.Repeat("x", 201) + `"],"action":"m"}]`,
		"value is one of":             `[{"type":"choice","name":"m","label":"M","value":"c","options":["a","b"],"action":"m"}]`,
	} {
		if _, err := Clean([]byte(raw)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
	}
}

func TestInput(t *testing.T) {
	for raw, ok := range map[string]bool{``: true, `null`: true, `{"a":1}`: true, `[1]`: false, `"x"`: false, `{"a":"` + strings.Repeat("a", MaxInput) + `"}`: false} {
		if _, err := Input(json.RawMessage(raw)); (err == nil) != ok {
			t.Errorf("%.20s: %v", raw, err)
		}
	}
}

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
	raw := `[{"type":"dialog","label":"Edit program","title":"Salon","action":"save","columns":2,"args":{"room":"Salon"},"blocks":[` +
		`{"type":"group","label":"Workdays"},{"type":"time","name":"w1","value":"06:30","step":15},{"type":"number","name":"t1","value":21}]}]`
	out, err := Clean([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"title":"Salon"`) || !strings.Contains(string(out), `"columns":2`) || !strings.Contains(string(out), `{"type":"time","value":"06:30","name":"w1","step":15}`) {
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
		"columns is 1 or 2":         `[{"type":"dialog","label":"E","action":"save","columns":3,"blocks":[{"type":"text","text":"a"}]}]`,
		"holds blocks and columns":  `[{"type":"text","text":"a","columns":2}]`,
		`a time's value is "HH:MM"`: `[{"type":"time","name":"a","value":"24:00"}]`,
		"whole minutes":             `[{"type":"time","name":"a","step":7.5}]`,
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

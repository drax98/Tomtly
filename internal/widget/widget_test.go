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

func TestInput(t *testing.T) {
	for raw, ok := range map[string]bool{``: true, `null`: true, `{"a":1}`: true, `[1]`: false, `"x"`: false, `{"a":"` + strings.Repeat("a", MaxInput) + `"}`: false} {
		if _, err := Input(json.RawMessage(raw)); (err == nil) != ok {
			t.Errorf("%.20s: %v", raw, err)
		}
	}
}

package extension

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"testing"
)

const xiaomiTablesFile = "testdata/xiaomi_tables.json"

// What each hand-written model offers: tuned on real hardware, and scripts may use these names and limits. The file was
// written from the tables before the driver for any other model existed, so a change here is a change for those scripts.
func TestXiaomiKnownModelsKeepTheirTables(t *testing.T) {
	source, err := os.ReadFile(example(t, "xiaomi") + "/models.ts")
	if err != nil {
		t.Fatal(err)
	}
	models := []string{"roborock.vacuum.s5"}
	for _, match := range regexp.MustCompile(`(?m)^  "([a-z0-9.]+)": \{$`).FindAllStringSubmatch(string(source), -1) {
		models = append(models, match[1])
	}
	if len(models) != 6 {
		t.Fatalf("%v: a new table needs adding to %s", models, xiaomiTablesFile)
	}
	var devices []map[string]any
	for i, model := range models {
		d := xiaomiDevice(model)
		d["did"] = fmt.Sprint(i + 1)
		devices = append(devices, d)
	}
	m := runXiaomi(t, xiaomiCopy(t, "", "TIMEOUT_MS = 1500", "TIMEOUT_MS = 20"), xiaomiFile(devices...))
	eventually(t, "the devices", func() bool { return len(m.Devices()) == len(models) })
	got := map[string]any{}
	for _, d := range m.Devices() {
		var values []map[string]any
		for _, dp := range d.DPs {
			values = append(values, map[string]any{"code": dp.Code, "type": dp.Type, "writable": dp.Writable, "scale": dp.Scale, "values": dp.Values})
		}
		got[d.Product] = map[string]any{"kind": d.Kind, "values": values}
	}
	text, err := json.MarshalIndent(got, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(xiaomiTablesFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != string(text) {
		t.Errorf("a known model changed:\n%s", text)
	}
}

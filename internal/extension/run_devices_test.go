package extension

import "testing"

func TestConvertDevicesKeepsAdvanced(t *testing.T) {
	devices, err := convertDevices("tuya", `[{"id":"t","name":"Salon","values":[`+
		`{"code":"temp_set","type":"Integer","writable":true,"value":210,"min":90,"max":300,"scale":1},`+
		`{"code":"calibration","type":"Integer","writable":true,"value":0,"min":-90,"max":90,"scale":1,"advanced":true}]}]`)
	if err != nil {
		t.Fatal(err)
	}
	dps := devices[0].DPs
	if dps[0].Advanced || !dps[1].Advanced {
		t.Errorf("advanced: %+v", dps)
	}
	if _, err := convertDevices("tuya", `[{"id":"t","name":"x","values":[{"code":"a","type":"Integer","folded":true}]}]`); err == nil {
		t.Error("an unknown field is still refused")
	}
}

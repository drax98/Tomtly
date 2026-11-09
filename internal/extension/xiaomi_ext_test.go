package extension

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"home-control/device"
)

const (
	xiaomiToken = "00112233445566778899aabbccddeeff"
	xiaomiDID   = "1234567"
)

// The miIO wire format, copied here so the integration is checked against the protocol, not against itself.
func miioSum(data ...[]byte) []byte {
	h := md5.New()
	for _, d := range data {
		h.Write(d)
	}
	return h.Sum(nil)
}

func miioEncrypt(token, plain []byte) []byte {
	key := miioSum(token)
	block, _ := aes.NewCipher(key)
	n := aes.BlockSize - len(plain)%aes.BlockSize
	data := append(bytes.Clone(plain), bytes.Repeat([]byte{byte(n)}, n)...)
	cipher.NewCBCEncrypter(block, miioSum(key, token)).CryptBlocks(data, data)
	return data
}

func miioDecrypt(token, data []byte) []byte {
	key := miioSum(token)
	block, _ := aes.NewCipher(key)
	out := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, miioSum(key, token)).CryptBlocks(out, data)
	return out[:len(out)-int(out[len(out)-1])]
}

type miioCall struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	ID     int             `json:"id"`
}

// fakeMiio is one device on 127.0.0.1:54321.
type fakeMiio struct {
	conn  net.PacketConn
	token []byte
	did   uint32

	mu        sync.Mutex
	calls     []miioCall
	answer    func(c miioCall) (result, failure any)
	dropOnce  map[string]bool // methods whose next reply is lost
	corrupt   bool            // replies fail their checksum
	wrongID   bool            // replies answer a different request
	nulSuffix bool
	dropOver  int // get_properties calls longer than this get no reply, as some firmware does
}

func startMiio(t *testing.T, answer func(c miioCall) (result, failure any)) *fakeMiio {
	t.Helper()
	conn, err := net.ListenPacket("udp4", "127.0.0.1:54321")
	if err != nil {
		t.Skip("port 54321 busy: ", err)
	}
	token, _ := hex.DecodeString(xiaomiToken)
	f := &fakeMiio{conn: conn, token: token, did: 1234567, answer: answer, dropOnce: map[string]bool{}}
	t.Cleanup(func() { conn.Close() })
	go f.serve()
	return f
}

func (f *fakeMiio) serve() {
	buf := make([]byte, 65535)
	for {
		n, from, err := f.conn.ReadFrom(buf)
		if err != nil {
			return
		}
		data := buf[:n]
		if n == 32 && bytes.Equal(data[4:], bytes.Repeat([]byte{0xff}, 28)) {
			head := binary.BigEndian.AppendUint16(nil, 0x2131)
			head = binary.BigEndian.AppendUint16(head, 32)
			head = binary.BigEndian.AppendUint32(head, 0)
			head = binary.BigEndian.AppendUint32(head, f.did)
			head = binary.BigEndian.AppendUint32(head, 1000)
			f.conn.WriteTo(append(head, make([]byte, 16)...), from)
			continue
		}
		// Anything not sealed with this device's token is ignored, as a real one does.
		if n <= 32 || !bytes.Equal(miioSum(data[:16], f.token, data[32:]), data[16:32]) || binary.BigEndian.Uint32(data[12:]) != 1001 {
			continue
		}
		var c miioCall
		if json.Unmarshal(miioDecrypt(f.token, data[32:]), &c) != nil {
			continue
		}
		f.mu.Lock()
		f.calls = append(f.calls, c)
		dropped := f.dropOnce[c.Method]
		delete(f.dropOnce, c.Method)
		corrupt, wrongID, nul := f.corrupt, f.wrongID, f.nulSuffix
		f.mu.Unlock()
		if dropped {
			continue
		}
		if f.dropOver > 0 && c.Method == "get_properties" {
			var params []any
			if json.Unmarshal(c.Params, &params) == nil && len(params) > f.dropOver {
				continue
			}
		}
		result, failure := f.answer(c)
		reply := map[string]any{"id": c.ID, "result": result}
		if wrongID {
			reply["id"] = c.ID + 1000
		}
		if failure != nil {
			delete(reply, "result")
			reply["error"] = failure
		}
		plain, _ := json.Marshal(reply)
		if nul {
			plain = append(plain, 0)
		}
		payload := miioEncrypt(f.token, plain)
		head := binary.BigEndian.AppendUint16(nil, 0x2131)
		head = binary.BigEndian.AppendUint16(head, uint16(32+len(payload)))
		head = binary.BigEndian.AppendUint32(head, 0)
		head = binary.BigEndian.AppendUint32(head, f.did)
		head = binary.BigEndian.AppendUint32(head, 1001)
		sum := miioSum(head, f.token, payload)
		if corrupt {
			sum[0] ^= 0xff
		}
		f.conn.WriteTo(append(append(head, sum...), payload...), from)
	}
}

func (f *fakeMiio) methods(want string) []miioCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []miioCall
	for _, c := range f.calls {
		if c.Method == want {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeMiio) set(change func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change()
}

func xiaomiFile(devices ...map[string]any) string {
	out, _ := json.Marshal([]any{map[string]any{"server": "de", "homes": []any{map[string]any{"home_id": "1", "devices": devices}}}})
	return string(out)
}

func xiaomiDevice(model string) map[string]any {
	return map[string]any{"did": xiaomiDID, "name": "Test device", "model": model, "localip": "127.0.0.1", "token": xiaomiToken}
}

// xiaomiCopy is the extension's folder with the given text replaced, to speed up its clock or shrink a limit.
func xiaomiCopy(t *testing.T, prefix string, replace ...string) string {
	t.Helper()
	dir := t.TempDir()
	files, err := os.ReadDir(example(t, "xiaomi"))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join(example(t, "xiaomi"), file.Name()))
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		if strings.HasSuffix(file.Name(), ".ts") {
			text = strings.NewReplacer(replace...).Replace(text)
		}
		if file.Name() == entryFile {
			text = prefix + text
		}
		write(t, dir, file.Name(), text)
	}
	return dir
}

func runXiaomi(t *testing.T, source, file string) *Manager {
	t.Helper()
	return run(t, source, map[string]any{"devices": "secrets/xiaomi.json"}, allowLoopback, func(m *Manager) {
		write(t, m.configDir, "secrets/xiaomi.json", file)
	})
}

func xiaomiOne(m *Manager) (device.Device, bool) {
	list := m.Devices()
	if len(list) != 1 {
		return device.Device{}, false
	}
	return list[0], true
}

func xiaomiValue(d device.Device, code string) any {
	for _, dp := range d.DPs {
		if dp.Code == code {
			return dp.Value
		}
	}
	return "missing"
}

func answerProperties(values map[string]any) func(c miioCall) (any, any) {
	return func(c miioCall) (any, any) {
		var params []struct {
			DID string `json:"did"`
		}
		if c.Method == "set_properties" {
			return []map[string]any{{"code": 0}}, nil
		}
		json.Unmarshal(c.Params, &params)
		out := []map[string]any{}
		for _, p := range params {
			if v, ok := values[p.DID]; ok {
				out = append(out, map[string]any{"did": p.DID, "code": 0, "value": v})
			} else {
				out = append(out, map[string]any{"did": p.DID, "code": -4001})
			}
		}
		return out, nil
	}
}

func TestXiaomiMiotRead(t *testing.T) {
	f := startMiio(t, answerProperties(map[string]any{"2.1": true, "2.4": 2, "3.7": 21.04, "3.1": 55, "2.5": 9, "2.2": 1, "4.3": 3549, "4.4": 143}))
	f.nulSuffix = true
	m := runXiaomi(t, example(t, "xiaomi"), xiaomiFile(xiaomiDevice("zhimi.airp.mb5")))
	var d device.Device
	eventually(t, "the device to read", func() bool {
		var ok bool
		d, ok = xiaomiOne(m)
		return ok && d.Online
	})
	if d.ID != "xiaomi:"+xiaomiDID || d.Kind != "air" || d.Product != "zhimi.airp.mb5" || d.Name != "Test device" {
		t.Errorf("got %+v", d)
	}
	for code, want := range map[string]any{"power": true, "mode": "favorite", "temperature": 210.0, "humidity": 55.0, "fan_level": "9", "fault": "PM sensor", "pm25": nil, "filter_days_left": 143.0, "filter_hours_used": 3549.0, "command": nil} {
		if got := xiaomiValue(d, code); got != want {
			t.Errorf("%s = %v, want %v", code, got, want)
		}
	}
	if calls := f.methods("get_properties"); len(calls) != 2 {
		t.Errorf("24 properties should take two calls, took %d", len(calls))
	}
	for _, c := range f.methods("get_properties") {
		if strings.Contains(string(c.Params), `"0.0"`) {
			t.Error("an Action was read as a property")
		}
	}
	for _, dp := range d.DPs {
		if dp.Code == "temperature" && (dp.Scale != 1 || dp.Writable || dp.Values["unit"] != "°C") {
			t.Errorf("temperature: %+v", dp)
		}
		if dp.Code == "favorite_level" && (!dp.Writable || dp.Values["max"] != 11.0) {
			t.Errorf("favorite_level: %+v", dp)
		}
	}
}

func TestXiaomiReadsInBatches(t *testing.T) {
	f := startMiio(t, answerProperties(map[string]any{"2.1": true}))
	m := runXiaomi(t, xiaomiCopy(t, "", "MIOT_BATCH = 15", "MIOT_BATCH = 5"), xiaomiFile(xiaomiDevice("zhimi.airp.mb5")))
	eventually(t, "three batches", func() bool { return len(f.methods("get_properties")) >= 3 })
	_ = m
	for _, c := range f.methods("get_properties")[:3] {
		var params []any
		json.Unmarshal(c.Params, &params)
		if len(params) > 5 {
			t.Errorf("a batch of %d", len(params))
		}
	}
}

func TestXiaomiMiotWrite(t *testing.T) {
	f := startMiio(t, answerProperties(map[string]any{"2.1": false, "2.4": 0}))
	m := runXiaomi(t, example(t, "xiaomi"), xiaomiFile(xiaomiDevice("zhimi.airp.mb5")))
	eventually(t, "the device to read", func() bool { d, ok := xiaomiOne(m); return ok && d.Online })
	for _, w := range []struct {
		code  string
		value any
		sent  string
	}{
		{"mode", "manual", `[{"did":"2.4","piid":4,"siid":2,"value":3}]`},
		{"power", true, `[{"did":"2.1","piid":1,"siid":2,"value":true}]`},
		{"favorite_level", float64(7), `[{"did":"9.5","piid":5,"siid":9,"value":7}]`},
	} {
		before := len(f.methods("set_properties"))
		if err := m.Write("xiaomi:"+xiaomiDID, w.code, w.value); err != nil {
			t.Fatal(w.code, err)
		}
		eventually(t, w.code+" to be sent", func() bool { return len(f.methods("set_properties")) > before })
		var got any
		json.Unmarshal(f.methods("set_properties")[before].Params, &got)
		normal, _ := json.Marshal(got)
		if string(normal) != w.sent {
			t.Errorf("%s sent %s, want %s", w.code, normal, w.sent)
		}
	}
	if err := m.Write("xiaomi:"+xiaomiDID, "temperature", 1); err == nil {
		t.Error("a reading must not be writable")
	}
}

func TestXiaomiRoborock(t *testing.T) {
	f := startMiio(t, func(c miioCall) (any, any) {
		if c.Method == "get_status" {
			return []map[string]any{{"fan_power": 102, "state": 8, "battery": 95, "error_code": 0, "clean_time": 2040, "clean_area": 35000000, "dnd_enabled": 1, "map_present": 0}}, nil
		}
		switch c.Method {
		case "get_consumable":
			return []map[string]any{{"main_brush_work_time": 237600, "side_brush_work_time": 640800, "filter_work_time": 237600, "sensor_dirty_time": 3600}}, nil
		case "get_clean_summary":
			return []any{1162800, 18747000000, 1102, []int{1}}, nil // older firmware's list
		case "get_sound_volume":
			return []int{90}, nil
		}
		return []string{"ok"}, nil
	})
	m := runXiaomi(t, example(t, "xiaomi"), xiaomiFile(xiaomiDevice("roborock.vacuum.s5")))
	var d device.Device
	eventually(t, "the vacuum to read", func() bool {
		var ok bool
		d, ok = xiaomiOne(m)
		return ok && d.Online
	})
	for code, want := range map[string]any{"fan_speed": "balanced", "state": "charging", "battery": 95.0, "error_code": 0.0, "command": nil,
		"clean_time": 34.0, "clean_area": 35.0, "do_not_disturb": true, "map_present": false, "main_brush_hours": 66.0, "sensor_dirty_hours": 1.0,
		"total_clean_hours": 323.0, "total_clean_area": 18747.0, "clean_count": 1102.0, "sound_volume": 90.0} {
		if got := xiaomiValue(d, code); got != want {
			t.Errorf("%s = %v, want %v", code, got, want)
		}
	}
	if d.Kind != "vacuum" {
		t.Errorf("kind %s", d.Kind)
	}
	if err := m.Write("xiaomi:"+xiaomiDID, "command", "dock"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "dock to pause then charge", func() bool { return len(f.methods("app_pause")) == 1 && len(f.methods("app_charge")) == 1 })
	if err := m.Write("xiaomi:"+xiaomiDID, "fan_speed", "max"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the fan speed", func() bool {
		calls := f.methods("set_custom_mode")
		return len(calls) == 1 && string(calls[0].Params) == "[104]"
	})
	if err := m.Write("xiaomi:"+xiaomiDID, "sound_volume", 40); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the volume", func() bool {
		calls := f.methods("change_sound_volume")
		return len(calls) == 1 && string(calls[0].Params) == "[40]"
	})
	for action, method := range map[string]string{"spot": "app_spot", "stop": "app_stop"} {
		if err := m.Write("xiaomi:"+xiaomiDID, "command", action); err != nil {
			t.Fatal(err)
		}
		eventually(t, action, func() bool { return len(f.methods(method)) == 1 })
	}
}

func TestXiaomiRoborockStaysOnlineWithoutExtras(t *testing.T) {
	startMiio(t, func(c miioCall) (any, any) {
		if c.Method == "get_status" {
			return []map[string]any{{"fan_power": 102, "state": 8, "battery": 95, "error_code": 0}}, nil
		}
		return nil, map[string]any{"code": -32601, "message": "unknown_method"}
	})
	m := runXiaomi(t, example(t, "xiaomi"), xiaomiFile(xiaomiDevice("roborock.vacuum.s5")))
	eventually(t, "the vacuum to read", func() bool { d, ok := xiaomiOne(m); return ok && d.Online })
}

func TestXiaomiWaterLevelHasNames(t *testing.T) {
	startMiio(t, answerProperties(map[string]any{"2.7": 1}))
	m := runXiaomi(t, example(t, "xiaomi"), xiaomiFile(xiaomiDevice("zhimi.humidifier.ca6")))
	eventually(t, "the device to read", func() bool { d, ok := xiaomiOne(m); return ok && d.Online })
	d, _ := xiaomiOne(m)
	if got := xiaomiValue(d, "water_level"); got != "normal" {
		t.Errorf("water_level = %v", got)
	}
}

func answerActions(code int) func(c miioCall) (any, any) {
	return func(c miioCall) (any, any) {
		if c.Method == "action" {
			return map[string]any{"code": code}, nil
		}
		return answerProperties(nil)(c)
	}
}

func TestXiaomiMiotAction(t *testing.T) {
	f := startMiio(t, answerActions(0))
	m := runXiaomi(t, example(t, "xiaomi"), xiaomiFile(xiaomiDevice("zhimi.airp.mb5")))
	eventually(t, "the device to read", func() bool { d, ok := xiaomiOne(m); return ok && d.Online })
	if err := m.Write("xiaomi:"+xiaomiDID, "command", "next_mode"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the action", func() bool { return len(f.methods("action")) == 1 })
	var sent struct {
		DID        string `json:"did"`
		SIID, AIID int
		In         []any `json:"in"`
	}
	if err := json.Unmarshal(f.methods("action")[0].Params, &sent); err != nil {
		t.Fatal(err)
	}
	if sent.DID != "call-9-1" || sent.SIID != 9 || sent.AIID != 1 || sent.In == nil {
		t.Errorf("sent %+v", sent)
	}
	if err := m.Write("xiaomi:"+xiaomiDID, "command", "nonsense"); err == nil {
		t.Error("an action the device doesn't have was accepted")
	}
}

func TestXiaomiMiotActionRefused(t *testing.T) {
	startMiio(t, answerActions(-4004))
	m := runXiaomi(t, example(t, "xiaomi"), xiaomiFile(xiaomiDevice("dmaker.fan.1c")))
	eventually(t, "the device to read", func() bool { d, ok := xiaomiOne(m); return ok && d.Online })
	if err := m.Write("xiaomi:"+xiaomiDID, "command", "toggle"); err != nil {
		t.Fatal(err)
	}
	failsWith(t, m, "device refused")
}

// A lost reply may mean the toggle worked, so sending it again could undo it.
func TestXiaomiLostActionIsNotRepeated(t *testing.T) {
	f := startMiio(t, answerActions(0))
	m := runXiaomi(t, example(t, "xiaomi"), xiaomiFile(xiaomiDevice("dmaker.fan.1c")))
	eventually(t, "the device to read", func() bool { d, ok := xiaomiOne(m); return ok && d.Online })
	f.set(func() { f.dropOnce["action"] = true })
	if err := m.Write("xiaomi:"+xiaomiDID, "command", "toggle"); err != nil {
		t.Fatal(err)
	}
	failsWith(t, m, "timeout")
	if n := len(f.methods("action")); n != 1 {
		t.Errorf("sent %d times", n)
	}
}

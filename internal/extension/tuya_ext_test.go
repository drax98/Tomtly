package extension

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"io"

	"net"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"home-control/device"
)

const (
	tuyaKey   = "0123456789abcdef"
	tuyaOther = "fedcba9876543210"
)

// tuyaFake is one device or gateway. Its protocol code is a copy of the Go adapter this extension replaced.
type tuyaFake struct {
	ln          net.Listener
	version     string
	key         string
	replyAnyway bool // answers with its own key even when it can't read the request, as a stranger would
	mu          sync.Mutex
	dps         map[string]map[string]any // by cid, "" for the root
	conns       [][]uint32                // the commands each connection sent
	controls    []map[string]any
	conn        net.Conn
	session     []byte
}

func startTuyaFake(t *testing.T, version, key string, dps map[string]map[string]any) *tuyaFake {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &tuyaFake{ln: ln, version: version, key: key, dps: dps}
	t.Cleanup(func() { ln.Close(); f.closeConn() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.handle(conn)
		}
	}()
	return f
}

func (f *tuyaFake) port() int { return f.ln.Addr().(*net.TCPAddr).Port }

func (f *tuyaFake) closeConn() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.conn != nil {
		f.conn.Close()
	}
}

func (f *tuyaFake) commands() [][]uint32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.conns)
}

func (f *tuyaFake) controlFrames() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.controls)
}

func tuyaPad(data []byte) []byte {
	n := aes.BlockSize - len(data)%aes.BlockSize
	return append(bytes.Clone(data), bytes.Repeat([]byte{byte(n)}, n)...)
}

func tuyaECB(key, data []byte, encrypt bool) []byte {
	block, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}
	out := make([]byte, len(data))
	for i := 0; i+aes.BlockSize <= len(data); i += aes.BlockSize {
		if encrypt {
			block.Encrypt(out[i:], data[i:])
		} else {
			block.Decrypt(out[i:], data[i:])
		}
	}
	return out
}

func tuyaGCM(key []byte) cipher.AEAD {
	block, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	return aead
}

func tuyaMAC(key []byte, parts ...[]byte) []byte {
	mac := hmac.New(sha256.New, key)
	for _, p := range parts {
		mac.Write(p)
	}
	return mac.Sum(nil)
}

// read returns one request's command and plain payload, or ok=false if it doesn't authenticate.
func (f *tuyaFake) read(r io.Reader, key []byte) (cmd uint32, plain []byte, ok bool, err error) {
	head := make([]byte, 16)
	if _, err = io.ReadFull(r, head); err != nil {
		return
	}
	if binary.BigEndian.Uint32(head) == 0x6699 {
		rest := make([]byte, 2)
		if _, err = io.ReadFull(r, rest); err != nil {
			return
		}
		head = append(head, rest...)
		cmd = binary.BigEndian.Uint32(head[10:])
		body := make([]byte, binary.BigEndian.Uint32(head[14:])+4)
		if _, err = io.ReadFull(r, body); err != nil {
			return
		}
		var openErr error
		plain, openErr = tuyaGCM(key).Open(nil, body[:12], body[12:len(body)-4], head[4:])
		return cmd, plain, openErr == nil, nil
	}
	cmd = binary.BigEndian.Uint32(head[8:])
	body := make([]byte, binary.BigEndian.Uint32(head[12:]))
	if _, err = io.ReadFull(r, body); err != nil {
		return
	}
	n := len(body) - 36
	if !hmac.Equal(tuyaMAC(key, head, body[:n]), body[n:n+32]) {
		return cmd, nil, false, nil
	}
	plain = tuyaECB(key, body[:n], false)
	if len(plain) > 0 {
		plain = plain[:len(plain)-int(plain[len(plain)-1])]
	}
	return cmd, plain, true, nil
}

// frame builds what a device sends: a return code, then the payload.
func (f *tuyaFake) frame(key []byte, cmd uint32, payload []byte) []byte {
	if f.version == "3.5" {
		out := binary.BigEndian.AppendUint32(nil, 0x6699)
		out = binary.BigEndian.AppendUint16(out, 0)
		out = binary.BigEndian.AppendUint32(out, 1)
		out = binary.BigEndian.AppendUint32(out, cmd)
		plain := append(make([]byte, 4), payload...)
		out = binary.BigEndian.AppendUint32(out, uint32(12+len(plain)+16))
		aad := bytes.Clone(out[4:])
		iv := make([]byte, 12)
		rand.Read(iv)
		out = append(out, iv...)
		out = tuyaGCM(key).Seal(out, iv, plain, aad)
		return binary.BigEndian.AppendUint32(out, 0x9966)
	}
	var enc []byte
	if len(payload) > 0 {
		enc = tuyaECB(key, tuyaPad(payload), true)
	}
	out := binary.BigEndian.AppendUint32(nil, 0x55AA)
	out = binary.BigEndian.AppendUint32(out, 1)
	out = binary.BigEndian.AppendUint32(out, cmd)
	out = binary.BigEndian.AppendUint32(out, uint32(4+len(enc)+36))
	out = append(append(out, 0, 0, 0, 0), enc...)
	out = append(out, tuyaMAC(key, out)...)
	return binary.BigEndian.AppendUint32(out, 0xAA55)
}

func (f *tuyaFake) sendJSON(conn net.Conn, key []byte, value any) {
	data, _ := json.Marshal(value)
	f.mu.Lock()
	defer f.mu.Unlock()
	conn.Write(f.frame(key, 8, data))
}

// push sends a change as a device does on its own.
func (f *tuyaFake) push(cid string, dps map[string]any) {
	f.mu.Lock()
	conn, key := f.conn, f.session
	f.mu.Unlock()
	body := map[string]any{"dps": dps}
	if cid != "" {
		body = map[string]any{"data": map[string]any{"dps": dps, "cid": cid}}
	}
	f.sendJSON(conn, key, body)
}

func (f *tuyaFake) handle(conn net.Conn) {
	defer conn.Close()
	f.mu.Lock()
	f.conns = append(f.conns, nil)
	index := len(f.conns) - 1
	f.mu.Unlock()
	real := []byte(f.key)
	key := real
	var remote []byte
	for {
		cmd, plain, ok, err := f.read(conn, key)
		if err != nil {
			return
		}
		f.mu.Lock()
		f.conns[index] = append(f.conns[index], cmd)
		f.mu.Unlock()
		if !ok && !(f.replyAnyway && cmd == 3) {
			return
		}
		switch cmd {
		case 3:
			remote = make([]byte, 16)
			rand.Read(remote)
			nonce := plain
			if len(nonce) < 16 {
				nonce = make([]byte, 16)
			}
			conn.Write(f.frame(real, 4, append(bytes.Clone(remote), tuyaMAC(real, nonce[:16])...)))
			if !ok {
				continue
			}
			f.mu.Lock()
			f.session = nil
			f.mu.Unlock()
			x := make([]byte, 16)
			for i := range x {
				x[i] = nonce[i] ^ remote[i]
			}
			var derived []byte
			if f.version == "3.5" {
				derived = tuyaGCM(real).Seal(nil, nonce[:12], x, nil)[:16]
			} else {
				derived = tuyaECB(real, x, true)
			}
			f.mu.Lock()
			f.session = derived
			f.mu.Unlock()
		case 5:
			if !hmac.Equal(plain, tuyaMAC(real, remote)) {
				return
			}
			f.mu.Lock()
			key = f.session
			f.conn = conn
			f.mu.Unlock()
		case 9:
			f.mu.Lock()
			conn.Write(f.frame(key, 9, nil))
			f.mu.Unlock()
		case 0x10:
			var query struct {
				CID string `json:"cid"`
			}
			json.Unmarshal(plain, &query)
			f.mu.Lock()
			dps := f.dps[query.CID]
			f.mu.Unlock()
			if dps != nil {
				if query.CID == "" {
					f.sendJSON(conn, key, map[string]any{"dps": dps})
				} else {
					f.sendJSON(conn, key, map[string]any{"data": map[string]any{"dps": dps, "cid": query.CID}})
				}
			}
		case 0x0d:
			if !bytes.HasPrefix(plain, []byte(f.version)) || len(plain) < 15 {
				return
			}
			var msg map[string]any
			json.Unmarshal(plain[15:], &msg)
			f.mu.Lock()
			f.controls = append(f.controls, msg)
			f.mu.Unlock()
		}
	}
}

func tuyaDevices() string {
	data, _ := json.Marshal([]map[string]any{
		{"id": "gw1", "name": "Gateway", "key": tuyaKey, "category": "wg2", "product_name": "GW", "mapping": map[string]any{}},
		{"id": "th1", "name": "Thermostat", "key": "kkkkkkkkkkkkkkk1", "category": "wk", "product_name": "TRV", "node_id": "cid-th", "mapping": map[string]any{
			"1":  map[string]any{"code": "switch", "type": "Boolean", "values": map[string]any{}},
			"2":  map[string]any{"code": "temp_set", "type": "Integer", "values": map[string]any{"min": 50, "max": 300, "scale": 1, "step": 5, "unit": "℃"}},
			"3":  map[string]any{"code": "temp_current", "type": "Integer", "values": map[string]any{"min": 0, "max": 500, "scale": 0, "step": 1, "unit": "℃"}},
			"4":  map[string]any{"code": "mode", "type": "Enum", "values": map[string]any{"range": []string{"auto", "manual"}}},
			"5":  map[string]any{"code": "factory_reset", "type": "Boolean", "values": map[string]any{}},
			"6":  map[string]any{"code": "add_ele", "type": "Integer", "values": map[string]any{"unit": "度", "min": 0, "max": 100}},
			"7":  map[string]any{"code": "schedule", "type": "Json", "values": ""},
			"8":  map[string]any{"code": "fault", "type": "Bitmap", "values": map[string]any{"label": []string{"a"}}},
			"10": map[string]any{"code": "c_f", "type": "Enum", "values": map[string]any{"range": []string{"c", "f"}}},
		}},
		{"id": "door1", "name": "Door", "key": "kkkkkkkkkkkkkkk2", "category": "mcs", "product_name": "Door", "node_id": "cid-door", "mapping": map[string]any{
			"101": map[string]any{"code": "prm_switch", "type": "Boolean", "values": map[string]any{}},
		}},
		{"id": "plug1", "name": "Plug", "key": tuyaKey, "category": "cz", "product_name": "Plug", "mapping": map[string]any{
			"1": map[string]any{"code": "switch_1", "type": "Boolean", "values": map[string]any{}},
		}},
	})
	return string(data)
}

// tuyaSource is the real extension, as one file on the fake's port and with shorter waits. replacements must each still
// match. TestTuyaLoadsFromItsFolder covers the real files bundled as they are.
func tuyaSource(t *testing.T, port int, replacements ...string) string {
	t.Helper()
	var joined strings.Builder
	for _, name := range []string{"categories.ts", "rules.ts", "colour.ts", "protocol.ts", "values.ts", "extension.ts"} {
		data, err := os.ReadFile(example(t, "tuya") + "/" + name)
		if err != nil {
			t.Fatal(err)
		}
		joined.WriteString(tuyaModuleSyntax.ReplaceAllString(string(data), "") + "\n")
	}
	source := joined.String()
	pairs := append([]string{"const PORT = 6668;", "const PORT = " + strconv.Itoa(port) + ";"}, replacements...)
	for i := 0; i < len(pairs); i += 2 {
		if !strings.Contains(source, pairs[i]) {
			t.Fatalf("the extension no longer has %q", pairs[i])
		}
		source = strings.ReplaceAll(source, pairs[i], pairs[i+1])
	}
	return source
}

var tuyaModuleSyntax = regexp.MustCompile(`(?m)^import .*$|^export `)

func runTuya(t *testing.T, source, hosts string) *Manager {
	t.Helper()
	return run(t, folder(t, source), map[string]any{"devices": "tuya.json", "hosts": hosts}, allowLoopback, func(m *Manager) {
		configFile(t, m, "tuya.json", tuyaDevices())
	})
}

func tuyaDevice(m *Manager, id string) (device.Device, bool) {
	for _, d := range m.Devices() {
		if d.ID == "tuya:"+id {
			return d, true
		}
	}
	return device.Device{}, false
}

func tuyaValue(d device.Device, code string) (device.DP, bool) {
	for _, dp := range d.DPs {
		if dp.Code == code {
			return dp, true
		}
	}
	return device.DP{}, false
}

// tuyaEventually allows for the extension's one-second background runs.
func tuyaEventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for range 300 {
		if ok() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out waiting for " + what)
}

func eventuallyValue(t *testing.T, m *Manager, id, code string, want any) {
	t.Helper()
	tuyaEventually(t, id+" "+code, func() bool {
		d, ok := tuyaDevice(m, id)
		if !ok || !d.Online {
			return false
		}
		dp, ok := tuyaValue(d, code)
		return ok && device.Equal(dp.Value, want)
	})
}

func TestTuyaGatewayReadsPushesAndWrites(t *testing.T) {
	for _, version := range []string{"3.4", "3.5"} {
		t.Run(version, func(t *testing.T) {
			f := startTuyaFake(t, version, tuyaKey, map[string]map[string]any{
				"cid-th": {"1": true, "2": 210, "3": 205, "6": 7, "7": map[string]any{"a": 1}, "8": 2, "10": false},
			})
			m := runTuya(t, tuyaSource(t, f.port()), "gw1=127.0.0.1@"+version)
			eventuallyValue(t, m, "th1", "temp_set", 210)
			// Its code scans clean, and the owner is told it holds the devices file and reaches the whole network.
			if w, r := status(m).Warnings, status(m).Risks; len(w) != 0 || len(r) != 1 || !strings.Contains(r[0], "any device on your local network") {
				t.Fatalf("warnings %v, risks %v", w, r)
			}

			d, _ := tuyaDevice(m, "th1")
			if d.Kind != "climate" || d.Name != "Thermostat" || d.Product != "TRV" {
				t.Fatalf("device: %+v", d)
			}
			var codes []string
			for _, dp := range d.DPs {
				codes = append(codes, dp.Code)
			}
			if want := []string{"switch", "c_f", "heating_state", "temp_set", "temp_current", "mode", "factory_reset", "add_ele", "schedule", "fault"}; !slices.Equal(codes, want) {
				t.Fatalf("the values should be in data point order: %v", codes)
			}
			for code, writable := range map[string]bool{"switch": true, "temp_set": true, "mode": true, "temp_current": false, "factory_reset": false, "add_ele": false, "heating_state": false, "schedule": false, "c_f": false} {
				if dp, _ := tuyaValue(d, code); dp.Writable != writable {
					t.Errorf("%s writable = %v", code, dp.Writable)
				}
			}
			if dp, _ := tuyaValue(d, "temp_current"); dp.Scale != 1 || dp.Values["unit"] != "℃" {
				t.Errorf("temp_current: %+v", dp)
			}
			if dp, _ := tuyaValue(d, "add_ele"); dp.Values["unit"] != "kWh" {
				t.Errorf("add_ele unit: %+v", dp)
			}
			if dp, _ := tuyaValue(d, "schedule"); dp.Type != "String" || dp.Value != `{"a":1}` {
				t.Errorf("schedule: %+v", dp)
			}
			if dp, _ := tuyaValue(d, "fault"); dp.Type != "String" || dp.Value != "2" {
				t.Errorf("fault: %+v", dp)
			}
			if dp, _ := tuyaValue(d, "c_f"); dp.Value != "c" || dp.Writable {
				t.Errorf("c_f sent as a boolean should read as its first choice: %+v", dp)
			}

			door, _ := tuyaDevice(m, "door1")
			if door.Kind != "sensor" || door.Online {
				t.Fatalf("a sensor that hasn't reported should be offline: %+v", door)
			}
			if dp, ok := tuyaValue(door, "open"); !ok || dp.Writable {
				t.Fatalf("door open: %+v", dp)
			}
			f.push("cid-door", map[string]any{"101": true})
			eventuallyValue(t, m, "door1", "open", true)

			if err := m.Write("tuya:th1", "temp_set", float64(225)); err != nil {
				t.Fatal(err)
			}
			tuyaEventually(t, "the control frame", func() bool { return len(f.controlFrames()) == 1 })
			msg := f.controlFrames()[0]
			data := msg["data"].(map[string]any)
			if msg["cid"] != "cid-th" || data["cid"] != "cid-th" || data["ctype"] != float64(0) || msg["protocol"] != float64(5) {
				t.Fatalf("control: %v", msg)
			}
			if dps := data["dps"].(map[string]any); len(dps) != 1 || dps["2"] != float64(225) {
				t.Fatalf("control dps: %v", dps)
			}
			if sent := msg["t"].(float64); time.Since(time.Unix(int64(sent), 0)) > time.Minute {
				t.Fatalf("control time: %v", sent)
			}
			f.push("cid-th", map[string]any{"2": 225})
			eventuallyValue(t, m, "th1", "temp_set", 225)

			for _, code := range []string{"temp_current", "factory_reset"} {
				if err := m.Write("tuya:th1", code, float64(1)); err == nil {
					t.Errorf("%s should be read-only", code)
				}
			}
		})
	}
}

type writerFunc func([]byte) (int, error)

func (w writerFunc) Write(p []byte) (int, error) { return w(p) }

var tuyaCandidates = regexp.MustCompile(`(?s)function candidates\(.*?\n}\n`)

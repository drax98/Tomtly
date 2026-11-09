package extension

import (
	"bytes"
	"crypto/md5"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"net"
	"slices"
	"sync"
	"testing"
)

// tuyaCRCFake is a device on protocol 3.1, 3.2 or 3.3, built from TinyTuya's description of them: 55AA frames with a CRC-32,
// no session, AES-ECB with the local key (3.1 only for controls and pushes), and the version header outside the encryption.
type tuyaCRCFake struct {
	ln       net.Listener
	version  string
	key      string
	device22 bool // answers a normal status read with "data unvalid"
	liar     bool // answers everything in plain text, as something without the key would have to
	mu       sync.Mutex
	dps      map[string]map[string]any // by cid, "" for the root
	conns    [][]uint32
	reads    []map[string]any // every status read received
	controls []map[string]any
	broken   []string // what the device couldn't accept
	conn     net.Conn
}

func startTuyaCRCFake(t *testing.T, version, key string, dps map[string]map[string]any) *tuyaCRCFake {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &tuyaCRCFake{ln: ln, version: version, key: key, dps: dps, device22: version == "3.2"}
	t.Cleanup(func() {
		ln.Close()
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.conn != nil {
			f.conn.Close()
		}
		if len(f.broken) > 0 {
			t.Errorf("the device got frames it couldn't accept: %v", f.broken)
		}
	})
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

func (f *tuyaCRCFake) port() int { return f.ln.Addr().(*net.TCPAddr).Port }

func (f *tuyaCRCFake) closeConn() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.conn != nil {
		f.conn.Close()
	}
}

func (f *tuyaCRCFake) commands() [][]uint32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.conns)
}

func (f *tuyaCRCFake) controlFrames() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.controls)
}

func (f *tuyaCRCFake) statusReads() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.reads)
}

func (f *tuyaCRCFake) problem(what string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.broken = append(f.broken, what)
}

func (f *tuyaCRCFake) header() []byte {
	return append([]byte(f.version), make([]byte, 12)...)
}

func (f *tuyaCRCFake) seal31(plain []byte) []byte {
	sealed := base64.StdEncoding.EncodeToString(tuyaECB([]byte(f.key), tuyaPad(plain), true))
	return []byte("3.1" + tuyaMD5Part(sealed, f.key) + sealed)
}

func tuyaMD5Part(sealed, key string) string {
	sum := md5.Sum([]byte("data=" + sealed + "||lpv=3.1||" + key))
	return hex.EncodeToString(sum[:])[8:24]
}

func tuyaCRCFrame(cmd uint32, payload []byte, retcode bool) []byte {
	if retcode {
		payload = append(make([]byte, 4), payload...)
	}
	out := binary.BigEndian.AppendUint32(nil, 0x55AA)
	out = binary.BigEndian.AppendUint32(out, 1)
	out = binary.BigEndian.AppendUint32(out, cmd)
	out = binary.BigEndian.AppendUint32(out, uint32(len(payload)+8))
	out = append(out, payload...)
	out = binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(out))
	return binary.BigEndian.AppendUint32(out, 0xAA55)
}

// reply is an answer to a request: a return code, then plain text on 3.1 and ECB from 3.2 up.
func (f *tuyaCRCFake) reply(conn net.Conn, cmd uint32, plain []byte) {
	if f.version != "3.1" && !f.liar && len(plain) > 0 {
		plain = tuyaECB([]byte(f.key), tuyaPad(plain), true)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	conn.Write(tuyaCRCFrame(cmd, plain, true))
}

// push is a change sent on its own: no return code, in the form each version uses.
func (f *tuyaCRCFake) push(cid string, dps map[string]any) {
	body := map[string]any{"dps": dps}
	if cid != "" {
		body["cid"] = cid
	}
	plain, _ := json.Marshal(body)
	var payload []byte
	if f.version == "3.1" {
		payload = f.seal31(plain)
	} else {
		payload = append(f.header(), tuyaECB([]byte(f.key), tuyaPad(plain), true)...)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.conn.Write(tuyaCRCFrame(8, payload, false))
}

// open returns the JSON of a request, in whichever form its command and version must arrive.
func (f *tuyaCRCFake) open(cmd uint32, payload []byte) ([]byte, bool) {
	if len(payload) == 0 {
		return nil, true
	}
	key := []byte(f.key)
	headed := cmd == 7 || cmd == 0x0d
	if f.version == "3.1" {
		if cmd != 7 {
			return payload, payload[0] == '{'
		}
		if len(payload) < 19 || string(payload[:3]) != "3.1" || string(payload[3:19]) != tuyaMD5Part(string(payload[19:]), f.key) {
			return nil, false
		}
		sealed, err := base64.StdEncoding.DecodeString(string(payload[19:]))
		if err != nil {
			return nil, false
		}
		payload = sealed
	} else if headed {
		if !bytes.HasPrefix(payload, f.header()) {
			return nil, false
		}
		payload = payload[len(f.header()):]
	}
	if len(payload)%16 != 0 {
		return nil, false
	}
	plain := tuyaECB(key, payload, false)
	n := int(plain[len(plain)-1])
	if n < 1 || n > 16 || n > len(plain) {
		return nil, false
	}
	return plain[:len(plain)-n], true
}

func (f *tuyaCRCFake) handle(conn net.Conn) {
	defer conn.Close()
	f.mu.Lock()
	f.conns = append(f.conns, nil)
	index := len(f.conns) - 1
	f.conn = conn
	f.mu.Unlock()
	for {
		head := make([]byte, 16)
		if _, err := io.ReadFull(conn, head); err != nil {
			return
		}
		cmd := binary.BigEndian.Uint32(head[8:])
		rest := make([]byte, binary.BigEndian.Uint32(head[12:]))
		if _, err := io.ReadFull(conn, rest); err != nil {
			return
		}
		f.mu.Lock()
		f.conns[index] = append(f.conns[index], cmd)
		f.mu.Unlock()
		if binary.BigEndian.Uint32(head) != 0x55AA || binary.BigEndian.Uint32(rest[len(rest)-4:]) != 0xAA55 {
			f.problem("bad prefix or suffix")
			return
		}
		body := rest[:len(rest)-8]
		if crc32.ChecksumIEEE(append(bytes.Clone(head), body...)) != binary.BigEndian.Uint32(rest[len(rest)-8:]) {
			f.problem("bad CRC")
			return
		}
		f.serve(conn, cmd, body)
	}
}

func (f *tuyaCRCFake) serve(conn net.Conn, cmd uint32, body []byte) {
	plain, ok := f.open(cmd, body)
	var msg map[string]any
	if ok && len(plain) > 0 {
		ok = json.Unmarshal(plain, &msg) == nil
	}
	if !ok {
		// A stranger can't read it. It answers anyway, as well as it can, for the sake of the tests that trust no answer.
		if f.key != tuyaKey || f.liar {
			f.reply(conn, cmd, []byte(`{"dps":{"1":true}}`))
		} else {
			f.problem(fmt.Sprintf("unreadable command %d", cmd))
		}
		return
	}
	switch cmd {
	case 9:
		f.reply(conn, 9, nil)
	case 7:
		f.mu.Lock()
		f.controls = append(f.controls, msg)
		f.mu.Unlock()
	case 0x0a, 0x0d:
		f.mu.Lock()
		f.reads = append(f.reads, msg)
		f.mu.Unlock()
		if cmd == 0x0a && f.device22 {
			f.reply(conn, 0x0a, []byte("json obj data unvalid"))
			return
		}
		cid, _ := msg["cid"].(string)
		f.mu.Lock()
		dps := f.dps[cid]
		f.mu.Unlock()
		if dps == nil {
			f.reply(conn, cmd, nil)
			return
		}
		reply := map[string]any{"dps": dps}
		if cid != "" {
			reply["cid"] = cid
		} else {
			reply["devId"] = msg["devId"]
		}
		data, _ := json.Marshal(reply)
		f.reply(conn, cmd, data)
	}
}

// tuyaPeer is a fake on any protocol, for the tests that treat them all alike.
type tuyaPeer interface {
	port() int
	push(cid string, dps map[string]any)
	controlFrames() []map[string]any
}

// startTuyaPeer starts the right fake for a version.
func startTuyaPeer(t *testing.T, version, key string, dps map[string]map[string]any) tuyaPeer {
	t.Helper()
	if version == "3.4" || version == "3.5" {
		return startTuyaFake(t, version, key, dps)
	}
	return startTuyaCRCFake(t, version, key, dps)
}

// tuyaSentDps is what a control frame sets, wherever its version keeps it.
func tuyaSentDps(t *testing.T, msg map[string]any) map[string]any {
	t.Helper()
	if data, ok := msg["data"].(map[string]any); ok {
		return data["dps"].(map[string]any)
	}
	dps, ok := msg["dps"].(map[string]any)
	if !ok {
		t.Fatalf("a control with no dps: %v", msg)
	}
	return dps
}

// runTuyaFile runs the extension with a devices file of the test's own.
func runTuyaFile(t *testing.T, source, hosts string, file any) *Manager {
	t.Helper()
	data, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	return run(t, folder(t, source), map[string]any{"devices": "tuya.json", "hosts": hosts}, allowLoopback, func(m *Manager) {
		configFile(t, m, "tuya.json", string(data))
	})
}

func tuyaWaitControls(t *testing.T, f tuyaPeer, n int) map[string]any {
	t.Helper()
	tuyaEventually(t, "the control frame", func() bool { return len(f.controlFrames()) >= n })
	return f.controlFrames()[n-1]
}

package extension

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	devKey     = "0123456789abcdef"
	greeECBKey = "a3K8Bx%2r8Y7#xDh"
	greeGCMKey = "{yxAHAY_Lm6pbC/<"
	greeAAD    = "qualcomm-test"
)

var greeNonce = []byte{0x54, 0x40, 0x78, 0x44, 0x49, 0x67, 0x5a, 0x51, 0x6c, 0x5e, 0x63, 0x13}

func greeSeal(key string, gcm bool, plain []byte) (pack, tag string, err error) {
	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		return "", "", err
	}
	if gcm {
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return "", "", err
		}
		out := aead.Seal(nil, greeNonce, plain, []byte(greeAAD))
		split := len(out) - aead.Overhead()
		return base64.StdEncoding.EncodeToString(out[:split]), base64.StdEncoding.EncodeToString(out[split:]), nil
	}
	n := aes.BlockSize - len(plain)%aes.BlockSize
	out := append(bytes.Clone(plain), bytes.Repeat([]byte{byte(n)}, n)...)
	for i := 0; i < len(out); i += aes.BlockSize {
		block.Encrypt(out[i:], out[i:])
	}
	return base64.StdEncoding.EncodeToString(out), "", nil
}

func greeUnseal(key, pack, tag string) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(pack)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		return nil, err
	}
	if tag != "" {
		rawTag, err := base64.StdEncoding.DecodeString(tag)
		if err != nil {
			return nil, err
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
		return aead.Open(nil, greeNonce, append(data, rawTag...), []byte(greeAAD))
	}
	if len(data) == 0 || len(data)%aes.BlockSize != 0 {
		return nil, errors.New("not whole blocks")
	}
	for i := 0; i < len(data); i += aes.BlockSize {
		block.Decrypt(data[i:], data[i:])
	}
	n := int(data[len(data)-1])
	if n == 0 || n > aes.BlockSize {
		return nil, errors.New("bad padding")
	}
	return data[:len(data)-n], nil
}

type fakeAC struct {
	conn  net.PacketConn
	gcm   bool
	mu    sync.Mutex
	cmds  []map[string]any
	binds int
	lacks []string // columns this unit has no answer for
}

func startFake(t *testing.T, gcm bool) *fakeAC {
	conn, err := net.ListenPacket("udp4", "127.0.0.1:7000")
	if err != nil {
		t.Skip("port 7000 busy: ", err)
	}
	f := &fakeAC{conn: conn, gcm: gcm}
	t.Cleanup(func() { conn.Close() })
	go f.serve()
	return f
}

func (f *fakeAC) reply(to net.Addr, key string, body map[string]any) {
	plain, _ := json.Marshal(body)
	pack, tag, _ := greeSeal(key, f.gcm, plain)
	out, _ := json.Marshal(map[string]any{"t": "pack", "i": 0, "uid": 0, "cid": "mac1", "tcid": "app", "pack": pack, "tag": tag})
	f.conn.WriteTo(out, to)
}

func (f *fakeAC) serve() {
	buf := make([]byte, 8192)
	for {
		n, from, err := f.conn.ReadFrom(buf)
		if err != nil {
			return
		}
		var env struct{ T, Pack, Tag string }
		if json.Unmarshal(buf[:n], &env) != nil {
			continue
		}
		if env.T == "scan" {
			f.reply(from, map[bool]string{false: greeECBKey, true: greeGCMKey}[f.gcm], map[string]any{"t": "dev", "mac": "aabbccddeeff", "cid": "aabbccddeeff"})
			continue
		}
		if (env.Tag != "") != f.gcm {
			continue // the other scheme gets silence
		}
		generic := map[bool]string{false: greeECBKey, true: greeGCMKey}[f.gcm]
		key, plain := devKey, []byte(nil)
		if plain, err = greeUnseal(devKey, env.Pack, env.Tag); err != nil {
			key = generic
			if plain, err = greeUnseal(generic, env.Pack, env.Tag); err != nil {
				continue
			}
		}
		var req map[string]any
		json.Unmarshal(plain, &req)
		f.mu.Lock()
		switch req["t"] {
		case "bind":
			f.binds++
			f.mu.Unlock()
			f.reply(from, key, map[string]any{"t": "bindok", "mac": "aabbccddeeff", "key": devKey})
			continue
		case "status":
			known := map[string]any{"Pow": 1, "Mod": 4, "SetTem": 24, "WdSpd": 3, "Quiet": 0, "Tur": 0, "Lig": 1, "TemSen": 63, "SwUpDn": 1, "SwingLfRig": 6, "Blo": 1, "Buzzer_ON_OFF": 0}
			cols, dat := []any{}, []any{}
			for _, col := range req["cols"].([]any) {
				if v, ok := known[col.(string)]; ok && !slices.Contains(f.lacks, col.(string)) {
					cols, dat = append(cols, col), append(dat, v)
				}
			}
			f.mu.Unlock()
			f.reply(from, key, map[string]any{"t": "dat", "mac": "aabbccddeeff", "cols": cols, "dat": dat})
			continue
		case "cmd":
			f.cmds = append(f.cmds, req)
			f.mu.Unlock()
			f.reply(from, key, map[string]any{"t": "res", "r": 200})
			continue
		}
		f.mu.Unlock()
	}
}

func startExt(t *testing.T, dir string, settings map[string]any) *Manager {
	t.Helper()
	return run(t, dir, settings, allowLoopback)
}

func TestGreeFake(t *testing.T) {
	for _, gcm := range []bool{false, true} {
		t.Run(fmt.Sprint("gcm=", gcm), func(t *testing.T) {
			f := startFake(t, gcm)
			m := startExt(t, example(t, "gree"), map[string]any{"host": "127.0.0.1"})
			eventually(t, "device", func() bool { return len(m.Devices()) == 1 })
			d := m.Devices()[0]
			if d.ID != "gree:aabbccddeeff" || d.Kind != "climate" || !d.Online {
				t.Fatalf("%+v", d)
			}
			got := map[string]any{}
			for _, dp := range d.DPs {
				got[dp.DP] = dp.Value
			}
			want := map[string]any{"power": true, "mode": "heat", "temp_set": float64(24), "fan_speed": "medium", "quiet": false, "turbo": false, "light": true, "temp_current": float64(23),
				"swing_vertical": "full", "swing_horizontal": "right", "x_fan": true, "beep": true}
			for k, v := range want {
				if fmt.Sprint(got[k]) != fmt.Sprint(v) {
					t.Errorf("%s = %v, want %v", k, got[k], v)
				}
			}
			t.Logf("values %v", got)
			if _, ok := got["health"]; ok {
				t.Error("a column the unit doesn't report should stay out")
			}

			for _, c := range []struct {
				dp    string
				value any
				opt   []any
				p     []any
			}{
				{"temp_set", 22, []any{"TemUn", "SetTem"}, []any{0.0, 22.0}},
				{"mode", "cool", []any{"Mod"}, []any{1.0}},
				{"power", false, []any{"Pow"}, []any{0.0}},
				{"fan_speed", "high", []any{"WdSpd"}, []any{5.0}},
				{"swing_vertical", "middle", []any{"SwUpDn"}, []any{4.0}},
				{"swing_horizontal", "left", []any{"SwingLfRig"}, []any{2.0}},
				{"x_fan", false, []any{"Blo"}, []any{0.0}},
				{"beep", false, []any{"BuzzerCtrl", "Buzzer_ON_OFF"}, []any{0.0, 1.0}},
				{"beep", true, []any{"BuzzerCtrl", "Buzzer_ON_OFF"}, []any{1.0, 0.0}},
			} {
				before := len(f.cmds)
				if err := m.Write(d.ID, c.dp, c.value); err != nil {
					t.Fatal(err)
				}
				eventually(t, "cmd "+c.dp, func() bool { f.mu.Lock(); defer f.mu.Unlock(); return len(f.cmds) > before })
				f.mu.Lock()
				cmd := f.cmds[before]
				f.mu.Unlock()
				if !reflect.DeepEqual(cmd["opt"], c.opt) || !reflect.DeepEqual(cmd["p"], c.p) {
					t.Errorf("%s: sent %v", c.dp, cmd)
				}
			}
		})
	}
}

// sweepable is the extension with its waits removed and loopback counted as a private network, so a sweep runs here.
func sweepable(t *testing.T) string {
	t.Helper()
	source, err := os.ReadFile(filepath.Join(example(t, "gree"), "extension.ts"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.NewReplacer("SWEEP_AFTER_MS = 60_000", "SWEEP_AFTER_MS = 0", "/^(10\\.", "/^(127\\.|10\\.").Replace(string(source))
	if text == string(source) {
		t.Fatal("the extension changed, so this test no longer applies")
	}
	return folder(t, text)
}

func TestGreeIsFoundAtANewAddress(t *testing.T) {
	startFake(t, false)
	m := run(t, sweepable(t), map[string]any{"host": "127.0.0.2", "mac": "aabbccddeeff"}, allowLoopback)
	// The old address gets its three tries first, which is longer than eventually waits.
	for deadline := time.Now().Add(12 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if devices := m.Devices(); len(devices) == 1 && devices[0].ID == "gree:aabbccddeeff" && devices[0].Online {
			return
		}
	}
	t.Fatal("the unit wasn't found at its new address: ", status(m).Error)
}

func TestGreeIgnoresAnotherUnit(t *testing.T) {
	startFake(t, false)
	m := run(t, sweepable(t), map[string]any{"host": "127.0.0.2", "mac": "000000000000"}, allowLoopback)
	time.Sleep(8 * time.Second) // the first poll's tries and its one sweep
	if devices := m.Devices(); len(devices) != 0 {
		t.Fatalf("it adopted a unit with another MAC: %+v", devices)
	}
}

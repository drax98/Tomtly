package extension

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"home-control/device"
)

func TestXiaomiRefusalIsNotRepeated(t *testing.T) {
	want := map[string]string{"error reply": "set_properties", "bad code": "device refused"}
	for name, answer := range map[string]func(c miioCall) (any, any){
		"error reply": func(c miioCall) (any, any) {
			if c.Method == "set_properties" {
				return nil, map[string]any{"code": -5001, "message": "no"}
			}
			return answerProperties(nil)(c)
		},
		"bad code": func(c miioCall) (any, any) {
			if c.Method == "set_properties" {
				return []map[string]any{{"code": -4004}}, nil
			}
			return answerProperties(nil)(c)
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := startMiio(t, answer)
			m := runXiaomi(t, example(t, "xiaomi"), xiaomiFile(xiaomiDevice("dmaker.fan.1c")))
			slowly(t, "the device to read", func() bool { d, ok := xiaomiOne(m); return ok && d.Online })
			if err := m.Write("xiaomi:"+xiaomiDID, "power", true); err != nil {
				t.Fatal(err)
			}
			failsWith(t, m, want[name])
			if strings.Contains(status(m).Error, xiaomiToken) {
				t.Error("a token in an error")
			}
			time.Sleep(300 * time.Millisecond)
			if n := len(f.methods("set_properties")); n != 1 {
				t.Errorf("sent %d times", n)
			}
		})
	}
}

func TestXiaomiLostPacketIsRepeatedOnce(t *testing.T) {
	f := startMiio(t, answerProperties(nil))
	m := runXiaomi(t, example(t, "xiaomi"), xiaomiFile(xiaomiDevice("dmaker.fan.1c")))
	slowly(t, "the device to read", func() bool { d, ok := xiaomiOne(m); return ok && d.Online })
	f.set(func() { f.dropOnce["set_properties"] = true })
	if err := m.Write("xiaomi:"+xiaomiDID, "power", true); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the repeat", func() bool { return len(f.methods("set_properties")) == 2 })
	time.Sleep(200 * time.Millisecond)
	if status(m).Error != "" {
		t.Errorf("the repeat worked, but: %s", status(m).Error)
	}

	// Lost twice shows as an error, and stops there.
	f.set(func() {
		f.corrupt = true
		f.calls = nil
	})
	if err := m.Write("xiaomi:"+xiaomiDID, "power", false); err != nil {
		t.Fatal(err)
	}
	failsWith(t, m, "no authentic answer")
	if n := len(f.methods("set_properties")); n != 2 {
		t.Errorf("sent %d times", n)
	}
}

func TestXiaomiRepliesThatArentAuthenticDontCount(t *testing.T) {
	for name, change := range map[string]func(f *fakeMiio){
		"bad checksum": func(f *fakeMiio) { f.corrupt = true },
		"another id":   func(f *fakeMiio) { f.wrongID = true },
	} {
		t.Run(name, func(t *testing.T) {
			f := startMiio(t, answerProperties(map[string]any{"2.1": true}))
			change(f)
			m := runXiaomi(t, example(t, "xiaomi"), xiaomiFile(xiaomiDevice("dmaker.fan.1c")))
			eventually(t, "the call to be made", func() bool { return len(f.methods("get_properties")) > 0 })
			time.Sleep(200 * time.Millisecond)
			d, ok := xiaomiOne(m)
			if !ok || d.Online || xiaomiValue(d, "power") != nil {
				t.Errorf("got %+v", d)
			}
		})
	}
}

func TestXiaomiWrongTokenIsOffline(t *testing.T) {
	f := startMiio(t, answerProperties(map[string]any{"2.1": true}))
	wrong := xiaomiDevice("dmaker.fan.1c")
	wrong["token"] = "ffeeddccbbaa99887766554433221100"
	m := runXiaomi(t, example(t, "xiaomi"), xiaomiFile(wrong))
	slowly(t, "the device to be listed", func() bool { _, ok := xiaomiOne(m); return ok })
	if d, _ := xiaomiOne(m); d.Online {
		t.Error("online with the wrong token")
	}
	if n := len(f.methods("get_properties")); n != 0 {
		t.Errorf("%d calls were accepted", n)
	}
}

func TestXiaomiKeepsValuesWhileOffline(t *testing.T) {
	f := startMiio(t, answerProperties(map[string]any{"2.1": true}))
	m := runXiaomi(t, xiaomiCopy(t, "", "everySeconds: 10", "everySeconds: 1", "TIMEOUT_MS = 1500", "TIMEOUT_MS = 100"), xiaomiFile(xiaomiDevice("dmaker.fan.1c")))
	slowly(t, "the device to read", func() bool { d, ok := xiaomiOne(m); return ok && d.Online })
	f.set(func() { f.corrupt = true })
	eventually(t, "the device to go offline", func() bool { d, ok := xiaomiOne(m); return ok && !d.Online })
	if d, _ := xiaomiOne(m); xiaomiValue(d, "power") != true {
		t.Errorf("power = %v", xiaomiValue(d, "power"))
	}
}

// Everything in the file gets a card, and only what can be reached is asked anything.
func TestXiaomiFileFiltering(t *testing.T) {
	f := startMiio(t, answerProperties(nil))
	noToken := xiaomiDevice("dmaker.fan.1c")
	noToken["did"], noToken["token"] = "2", ""
	noAddress := xiaomiDevice("dmaker.fan.1c")
	noAddress["did"], noAddress["localip"] = "3", ""
	unknown := xiaomiDevice("acme.toaster.v1")
	unknown["did"] = "4"
	bluetooth := xiaomiDevice("dmaker.fan.1c")
	bluetooth["did"] = "blt.3.abc"
	numeric := xiaomiDevice("dmaker.fan.1c")
	numeric["did"] = 5
	prototypeName := xiaomiDevice("constructor")
	prototypeName["did"] = "6"
	zigbee := xiaomiDevice("lumi.sensor_magnet.v2")
	zigbee["did"] = "lumi.158d0001"
	public := xiaomiDevice("acme.toaster.v1")
	public["did"], public["localip"] = "7", "8.8.8.8"
	m := runXiaomi(t, example(t, "xiaomi"), xiaomiFile(xiaomiDevice("dmaker.fan.1c"), xiaomiDevice("dmaker.fan.1c"), noToken, noAddress, unknown, bluetooth, numeric, prototypeName, zigbee, public))
	eventually(t, "the devices to be listed", func() bool { return len(m.Devices()) > 0 && xiaomiById(m, xiaomiDID).Online })
	for id, want := range map[string]string{"2": "no local address", "3": "no local address", "4": "spec_type", "6": "spec_type", "blt.3.abc": "Bluetooth", "lumi.158d0001": "Zigbee", "7": "outside your local network"} {
		d := xiaomiById(m, id)
		note, _ := xiaomiValue(d, "unavailable").(string)
		if d.Online || !strings.Contains(note, want) {
			t.Errorf("%s: %+v, want a note saying %q", id, d, want)
		}
	}
	if len(m.Devices()) != 9 {
		t.Errorf("%d devices", len(m.Devices()))
	}
	if d := xiaomiById(m, "5"); d.ID == "" || xiaomiValue(d, "unavailable") != "missing" {
		t.Errorf("a did written as a number: %+v", d)
	}
	// A public address would be a violation, and ten of them stop the extension.
	if s := status(m); len(s.Violations) != 0 || s.Error != "" {
		t.Errorf("violations %v, error %q", s.Violations, s.Error)
	}
	if n := len(f.methods("get_properties")); n == 0 {
		t.Error("the fan wasn't read")
	}
}

func xiaomiById(m *Manager, id string) device.Device {
	for _, d := range m.Devices() {
		if d.ID == "xiaomi:"+id {
			return d
		}
	}
	return device.Device{}
}

func TestXiaomiBadFilesAreExplained(t *testing.T) {
	for want, file := range map[string]string{
		"isn't valid JSON": `{"token": "` + xiaomiToken + `"`,
		"should be a list": `{}`,
		"isn't hex":        xiaomiFile(map[string]any{"did": "1", "name": "Fan", "model": "dmaker.fan.1c", "localip": "127.0.0.1", "token": "zz" + xiaomiToken}),
	} {
		t.Run(want, func(t *testing.T) {
			m := runXiaomi(t, example(t, "xiaomi"), file)
			failsWith(t, m, want)
			if strings.Contains(status(m).Error, xiaomiToken) {
				t.Error("a token in an error")
			}
		})
	}
}

func TestXiaomiTokensStayOut(t *testing.T) {
	var logs syncBuffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	f := startMiio(t, answerProperties(map[string]any{"2.1": true}))
	second := xiaomiDevice("dmaker.fan.1c")
	second["did"], second["token"] = "2", "ffeeddccbbaa99887766554433221100"
	unsupported := xiaomiDevice("acme.toaster.v1")
	unsupported["did"] = "3"
	m := runXiaomi(t, example(t, "xiaomi"), xiaomiFile(xiaomiDevice("dmaker.fan.1c"), second, unsupported))
	slowly(t, "the device to read", func() bool { list := m.Devices(); return len(list) == 3 && list[0].Online })
	if err := m.Write("xiaomi:"+xiaomiDID, "power", false); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the write", func() bool { return len(f.methods("set_properties")) == 1 })
	devices, _ := json.Marshal(m.Devices())
	statuses, _ := json.Marshal(m.Statuses())
	all := logs.String() + string(devices) + string(statuses)
	for _, token := range []string{xiaomiToken, "ffeeddccbbaa99887766554433221100"} {
		if strings.Contains(all, token) {
			t.Errorf("a token got out: %s", all)
		}
	}
	if s := status(m); len(s.Violations) != 0 || s.Error != "" {
		t.Errorf("violations %v, error %q", s.Violations, s.Error)
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Time runs 60 times as fast, and a poll comes every second, so the waits before a sweep pass in seconds.
const xiaomiFastClock = `const realNow = Date.now; const started = realNow(); Date.now = () => started + (realNow() - started) * 60;
`

func xiaomiFast(t *testing.T) string {
	return xiaomiCopy(t, xiaomiFastClock, "everySeconds: 10", "everySeconds: 1", "TIMEOUT_MS = 1500", "TIMEOUT_MS = 200", "SWEEP_TIMEOUT_MS = 1000", "SWEEP_TIMEOUT_MS = 300")
}

// A device moved: the file says 127.0.0.9, and the fake answers on 127.0.0.1, inside the same /24.
func TestXiaomiRediscovery(t *testing.T) {
	moved := xiaomiDevice("dmaker.fan.1c")
	moved["localip"] = "127.0.0.9"

	t.Run("adopts the device that proves itself", func(t *testing.T) {
		f := startMiio(t, answerProperties(map[string]any{"2.1": true}))
		m := runXiaomi(t, xiaomiFast(t), xiaomiFile(moved))
		eventually(t, "the device to be found", func() bool { d, ok := xiaomiOne(m); return ok && d.Online })
		if d, _ := xiaomiOne(m); xiaomiValue(d, "power") != true {
			t.Errorf("power = %v", xiaomiValue(d, "power"))
		}
		if n := len(f.methods("set_properties")); n != 0 {
			t.Errorf("a sweep wrote %d times", n)
		}
		if s := status(m); len(s.Violations) != 0 {
			t.Errorf("violations %v", s.Violations)
		}
	})

	t.Run("ignores another device", func(t *testing.T) {
		f := startMiio(t, answerProperties(map[string]any{"2.1": true}))
		f.did = 999
		m := runXiaomi(t, xiaomiFast(t), xiaomiFile(moved))
		slowly(t, "the device to be listed", func() bool { _, ok := xiaomiOne(m); return ok })
		time.Sleep(3 * time.Second) // several sweeps' worth at this clock
		if d, _ := xiaomiOne(m); d.Online {
			t.Error("adopted a device with another id")
		}
		if n := len(f.methods("get_properties")); n != 0 {
			t.Errorf("%d reads of a device with another id", n)
		}
	})

	t.Run("ignores the right id without the token", func(t *testing.T) {
		f := startMiio(t, answerProperties(map[string]any{"2.1": true}))
		f.token = []byte("a device that copied the id")
		m := runXiaomi(t, xiaomiFast(t), xiaomiFile(moved))
		slowly(t, "the device to be listed", func() bool { _, ok := xiaomiOne(m); return ok })
		time.Sleep(3 * time.Second)
		if d, _ := xiaomiOne(m); d.Online {
			t.Error("adopted a device that can't prove it has the token")
		}
	})

	t.Run("doesn't look for a did it can't recognise", func(t *testing.T) {
		startMiio(t, answerProperties(map[string]any{"2.1": true}))
		big := xiaomiDevice("dmaker.fan.1c")
		big["localip"], big["did"] = "127.0.0.9", fmt.Sprint(uint64(1)<<33)
		m := runXiaomi(t, xiaomiFast(t), xiaomiFile(big))
		slowly(t, "the device to be listed", func() bool { _, ok := xiaomiOne(m); return ok })
		time.Sleep(3 * time.Second)
		if d, _ := xiaomiOne(m); d.Online {
			t.Error("found a device whose id can't be in the header")
		}
	})
}

// slowly waits for what comes after a lost read and its one retry, which is longer than eventually waits.
func slowly(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatal("timed out waiting for " + what)
}

// A lost reply to a read is asked again, so one lost packet doesn't mark a working device offline.
func TestXiaomiLostReadIsRetried(t *testing.T) {
	f := startMiio(t, answerProperties(map[string]any{"2.1": true}))
	f.set(func() { f.dropOnce["get_properties"] = true })
	m := runXiaomi(t, example(t, "xiaomi"), xiaomiFile(xiaomiDevice("dmaker.fan.1c")))
	slowly(t, "the device to read", func() bool { d, ok := xiaomiOne(m); return ok && d.Online })
	if got := len(f.methods("get_properties")); got < 2 {
		t.Fatalf("get_properties sent %d times", got)
	}
}

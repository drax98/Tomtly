package extension

import (
	"bytes"

	"encoding/json"

	"log/slog"

	"os"

	"slices"

	"strings"
	"sync"
	"testing"
	"time"

	"home-control/device"
)

// A sweep adopts only a peer holding the key, and sends it nothing but the handshake. candidates is replaced because the
// real one only offers private addresses, and those can't be listened on here; TestTuyaCandidates covers it.
func TestTuyaRediscoveryAdoptsOnlyTheKeyedPeer(t *testing.T) {
	shorter := []string{"const RECONNECT_MS = 10_000;", "const RECONNECT_MS = 50;", "const MIN_FAILING_MS = 60_000;", "const MIN_FAILING_MS = 0;", "const MIN_SWEEP_GAP_MS = 300_000;", "const MIN_SWEEP_GAP_MS = 0;", "const DIAL_MS = 2000;", "const DIAL_MS = 50;"}
	source := func(t *testing.T, port int) string {
		text := tuyaSource(t, port, shorter...)
		if !tuyaCandidates.MatchString(text) {
			t.Fatal("no candidates function")
		}
		return tuyaCandidates.ReplaceAllString(text, `function candidates(old: string, used: string[]): string[] { return old === "127.0.0.1" ? [] : ["127.0.0.1"]; }`+"\n")
	}
	for _, version := range []string{"3.4", "3.5"} {
		t.Run("stranger "+version, func(t *testing.T) {
			f := startTuyaFake(t, version, tuyaOther, nil)
			f.replyAnyway = true
			m := runTuya(t, source(t, f.port()), "plug1=127.0.0.2@"+version)
			tuyaEventually(t, "a probe", func() bool {
				return slices.ContainsFunc(f.commands(), func(c []uint32) bool { return len(c) > 0 })
			})
			time.Sleep(1500 * time.Millisecond)
			if d, _ := tuyaDevice(m, "plug1"); d.Online {
				t.Fatal("a peer without the key must not be adopted")
			}
			for _, c := range f.commands() {
				if slices.Contains(c, 0x10) || slices.Contains(c, 0x0d) {
					t.Fatalf("a probe sent more than the handshake: %v", c)
				}
			}
		})
		t.Run("the device "+version, func(t *testing.T) {
			f := startTuyaFake(t, version, tuyaKey, map[string]map[string]any{"": {"1": true}})
			m := runTuya(t, source(t, f.port()), "plug1=127.0.0.2@"+version)
			eventuallyValue(t, m, "plug1", "switch_1", true)
			var talked [][]uint32
			for _, c := range f.commands() {
				if len(c) > 0 {
					talked = append(talked, c)
				}
			}
			if len(talked) < 2 || !slices.Equal(talked[0], []uint32{3, 5}) {
				t.Fatalf("the first connection should be the bare handshake, then the real link: %v", talked)
			}
		})
	}
}

func TestTuyaCandidates(t *testing.T) {
	data, err := os.ReadFile(example(t, "tuya") + "/extension.ts")
	if err != nil {
		t.Fatal(err)
	}
	function := tuyaCandidates.FindString(string(data))
	m := run(t, folder(t, function+`defineExtension({id:"a",name:"a",version:"1",data:{everySeconds:60,fetch(){return {
		home: candidates("192.168.1.5", ["192.168.1.7"]), ten: candidates("10.0.0.1", []), mid: candidates("172.16.3.9", []),
		pub: candidates("8.8.8.8", []), notPrivate: candidates("172.32.0.1", []), name: candidates("gateway.local", []), loop: candidates("127.0.0.1", [])};}}});`), nil)
	tuyaEventually(t, "the result", func() bool { raw, _, _ := m.plugins.Get("a"); return raw != nil })
	raw, _, _ := m.plugins.Get("a")
	var got map[string][]string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"pub", "notPrivate", "name", "loop"} {
		if len(got[name]) != 0 {
			t.Errorf("%s: %v", name, got[name])
		}
	}
	home := got["home"]
	if len(home) != 252 || slices.Contains(home, "192.168.1.5") || slices.Contains(home, "192.168.1.7") || slices.Contains(home, "192.168.1.0") || slices.Contains(home, "192.168.1.255") || !slices.Contains(home, "192.168.1.254") || !slices.Contains(home, "192.168.1.1") {
		t.Fatalf("home: %d %v", len(home), home)
	}
	if len(got["ten"]) != 253 || len(got["mid"]) != 253 {
		t.Fatalf("ten %d mid %d", len(got["ten"]), len(got["mid"]))
	}
}

func TestTuyaSilenceEndsTheLink(t *testing.T) {
	f := startTuyaFake(t, "3.4", tuyaKey, map[string]map[string]any{"": {"1": true}})
	m := runTuya(t, tuyaSource(t, f.port(), "const SILENCE_MS = 30_000;", "const SILENCE_MS = 300;", "const HEARTBEAT_MS = 10_000;", "const HEARTBEAT_MS = 60_000;"), "plug1=127.0.0.1@3.4")
	tuyaEventually(t, "the link to end", func() bool {
		d, _ := tuyaDevice(m, "plug1")
		return len(f.commands()) >= 1 && len(f.commands()[0]) >= 3 && !d.Online
	})
}

func TestTuyaWrongKeyIsOfflineAndKeysStayIn(t *testing.T) {
	var logged bytes.Buffer
	var mu sync.Mutex
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(writerFunc(func(p []byte) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		return logged.Write(p)
	}), nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	f := startTuyaFake(t, "3.4", tuyaOther, nil)
	f.replyAnyway = true
	m := runTuya(t, tuyaSource(t, f.port(), "const RECONNECT_MS = 10_000;", "const RECONNECT_MS = 100;"), "gw1=127.0.0.1@3.4")
	tuyaEventually(t, "a refused handshake", func() bool { return len(f.commands()) >= 2 })
	for _, d := range m.Devices() {
		if d.Online {
			t.Fatalf("%s should be offline", d.ID)
		}
	}
	shown, _ := json.Marshal(struct {
		D []device.Device
		S []Status
	}{m.Devices(), m.Statuses()})
	mu.Lock()
	defer mu.Unlock()
	for _, text := range []string{string(shown), logged.String()} {
		for _, key := range []string{tuyaKey, "kkkkkkkkkkkkkkk1"} {
			if strings.Contains(text, key) {
				t.Fatalf("a key got out: %s", text)
			}
		}
	}
	if !strings.Contains(logged.String(), "link down") {
		t.Fatalf("the failure should be logged: %s", logged.String())
	}
}

func TestTuyaDirectDevice(t *testing.T) {
	for _, version := range []string{"3.4", "3.5"} {
		t.Run(version, func(t *testing.T) {
			f := startTuyaFake(t, version, tuyaKey, map[string]map[string]any{"": {"1": true}})
			m := runTuya(t, tuyaSource(t, f.port()), "plug1=127.0.0.1@"+version)
			eventuallyValue(t, m, "plug1", "switch_1", true)
			if _, ok := tuyaDevice(m, "th1"); ok {
				t.Fatal("a device with no link should not be listed")
			}
			if err := m.Write("tuya:plug1", "switch_1", false); err != nil {
				t.Fatal(err)
			}
			tuyaEventually(t, "the control frame", func() bool { return len(f.controlFrames()) == 1 })
			msg := f.controlFrames()[0]
			if _, ok := msg["cid"]; ok {
				t.Fatalf("a direct device takes no cid: %v", msg)
			}
			if dps := msg["data"].(map[string]any)["dps"].(map[string]any); dps["1"] != false {
				t.Fatalf("control dps: %v", dps)
			}
		})
	}
}

func TestTuyaReconnectsAndSendsHeartbeats(t *testing.T) {
	f := startTuyaFake(t, "3.4", tuyaKey, map[string]map[string]any{"": {"1": true}})
	m := runTuya(t, tuyaSource(t, f.port(), "const RECONNECT_MS = 10_000;", "const RECONNECT_MS = 100;", "const HEARTBEAT_MS = 10_000;", "const HEARTBEAT_MS = 200;"), "plug1=127.0.0.1@3.4")
	eventuallyValue(t, m, "plug1", "switch_1", true)
	tuyaEventually(t, "a heartbeat", func() bool {
		c := f.commands()
		return slices.Contains(c[len(c)-1], 9)
	})
	f.closeConn()
	tuyaEventually(t, "the device to go offline", func() bool { d, _ := tuyaDevice(m, "plug1"); return !d.Online })
	tuyaEventually(t, "a second connection", func() bool { return len(f.commands()) >= 2 })
	eventuallyValue(t, m, "plug1", "switch_1", true)
}

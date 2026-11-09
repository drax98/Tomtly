package extension

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"testing"
	"time"
)

var tuyaAllVersions = []string{"3.1", "3.2", "3.3", "3.4", "3.5"}

func tuyaPlugFile(id string) []map[string]any {
	return []map[string]any{{"id": id, "name": "Plug", "key": tuyaKey, "category": "cz", "product_name": "Plug", "mapping": map[string]any{
		"1": map[string]any{"code": "switch_1", "type": "Boolean", "values": map[string]any{}},
		"4": map[string]any{"code": "cur_current", "type": "Integer", "values": map[string]any{"min": 0, "max": 30000}},
	}}}
}

func TestTuyaEveryProtocolReadsPushesAndWrites(t *testing.T) {
	for _, version := range tuyaAllVersions {
		t.Run(version, func(t *testing.T) {
			f := startTuyaPeer(t, version, tuyaKey, map[string]map[string]any{"": {"1": true}})
			m := runTuyaFile(t, tuyaSource(t, f.port()), "plug=127.0.0.1@"+version, tuyaPlugFile("plug"))
			eventuallyValue(t, m, "plug", "switch_1", true)
			f.push("", map[string]any{"4": 150})
			eventuallyValue(t, m, "plug", "cur_current", 150)
			if err := m.Write("tuya:plug", "switch_1", false); err != nil {
				t.Fatal(err)
			}
			msg := tuyaWaitControls(t, f, 1)
			if dps := tuyaSentDps(t, msg); len(dps) != 1 || dps["1"] != false {
				t.Fatalf("the write decrypted to %v", msg)
			}
			if _, ok := msg["cid"]; ok {
				t.Fatalf("a direct device takes no cid: %v", msg)
			}
			if version < "3.4" && (msg["devId"] != "plug" || msg["uid"] != "plug") {
				t.Fatalf("3.1 to 3.3 name the device: %v", msg)
			}
			if err := m.Write("tuya:plug", "cur_current", float64(1)); err == nil {
				t.Fatal("a reading can't be written")
			}
		})
	}
}

func TestTuyaHeartbeatsOnOldProtocols(t *testing.T) {
	for _, version := range []string{"3.1", "3.3"} {
		t.Run(version, func(t *testing.T) {
			f := startTuyaCRCFake(t, version, tuyaKey, map[string]map[string]any{"": {"1": true}})
			m := runTuyaFile(t, tuyaSource(t, f.port(), "const HEARTBEAT_MS = 10_000;", "const HEARTBEAT_MS = 200;", "const RECONNECT_MS = 10_000;", "const RECONNECT_MS = 100;"), "plug=127.0.0.1@"+version, tuyaPlugFile("plug"))
			eventuallyValue(t, m, "plug", "switch_1", true)
			tuyaEventually(t, "a heartbeat", func() bool { return slices.Contains(f.commands()[0], 9) })
			f.closeConn()
			tuyaEventually(t, "a second connection", func() bool { return len(f.commands()) >= 2 })
			eventuallyValue(t, m, "plug", "switch_1", true)
		})
	}
}

// A gateway on an old protocol reads and writes its children by cid.
func TestTuyaOldGatewayChildren(t *testing.T) {
	for _, version := range []string{"3.2", "3.3"} {
		t.Run(version, func(t *testing.T) {
			f := startTuyaCRCFake(t, version, tuyaKey, map[string]map[string]any{"cid-th": {"2": 210}})
			m := runTuya(t, tuyaSource(t, f.port()), "gw1=127.0.0.1@"+version)
			eventuallyValue(t, m, "th1", "temp_set", 210)
			if err := m.Write("tuya:th1", "temp_set", float64(225)); err != nil {
				t.Fatal(err)
			}
			msg := tuyaWaitControls(t, f, 1)
			if msg["cid"] != "cid-th" || tuyaSentDps(t, msg)["2"] != float64(225) {
				t.Fatalf("control: %v", msg)
			}
		})
	}
}

const tuya22CharID = "bfa1b2c3d4e5f6a7b8c9d0"

// The reply decides, not the id: 22 characters read normally until the device says "data unvalid", and 3.2 starts that way.
func TestTuyaDevice22(t *testing.T) {
	t.Run("3.3 switches when told", func(t *testing.T) {
		f := startTuyaCRCFake(t, "3.3", tuyaKey, map[string]map[string]any{"": {"1": true}})
		f.device22 = true
		m := runTuyaFile(t, tuyaSource(t, f.port()), tuya22CharID+"=127.0.0.1@3.3", tuyaPlugFile(tuya22CharID))
		eventuallyValue(t, m, tuya22CharID, "switch_1", true)
		cmds := f.commands()[0]
		if len(cmds) < 2 || cmds[0] != 0x0a || cmds[1] != 0x0d {
			t.Fatalf("commands: %v", cmds)
		}
		reads := f.statusReads()
		dps := reads[len(reads)-1]["dps"].(map[string]any)
		if len(dps) != 2 || dps["1"] != nil || dps["4"] != nil {
			t.Fatalf("it should list the mapping's data points as nulls: %v", reads)
		}
		if err := m.Write("tuya:"+tuya22CharID, "switch_1", false); err != nil {
			t.Fatal(err)
		}
		if tuyaSentDps(t, tuyaWaitControls(t, f, 1))["1"] != false {
			t.Fatal("control")
		}
	})
	t.Run("a 22 character id alone changes nothing", func(t *testing.T) {
		f := startTuyaCRCFake(t, "3.3", tuyaKey, map[string]map[string]any{"": {"1": true}})
		m := runTuyaFile(t, tuyaSource(t, f.port()), tuya22CharID+"=127.0.0.1@3.3", tuyaPlugFile(tuya22CharID))
		eventuallyValue(t, m, tuya22CharID, "switch_1", true)
		if slices.Contains(f.commands()[0], 0x0d) {
			t.Fatalf("commands: %v", f.commands())
		}
	})
	t.Run("3.2 starts that way", func(t *testing.T) {
		f := startTuyaCRCFake(t, "3.2", tuyaKey, map[string]map[string]any{"": {"1": true}})
		m := runTuyaFile(t, tuyaSource(t, f.port()), "plug=127.0.0.1@3.2", tuyaPlugFile("plug"))
		eventuallyValue(t, m, "plug", "switch_1", true)
		if cmds := f.commands()[0]; cmds[0] != 0x0d {
			t.Fatalf("commands: %v", cmds)
		}
	})
}

// Without a handshake, only a message that decrypts brings a link up, so a wrong key can't look online or be written to.
func TestTuyaOldProtocolWithoutTheKeyStaysOffline(t *testing.T) {
	shorter := []string{"const SILENCE_MS = 30_000;", "const SILENCE_MS = 300;", "const RECONNECT_MS = 10_000;", "const RECONNECT_MS = 100;", "const MIN_FAILING_MS = 60_000;", "const MIN_FAILING_MS = 600_000;"}
	for name, liar := range map[string]bool{"wrong key": false, "plain text": true} {
		for _, version := range []string{"3.2", "3.3"} {
			t.Run(name+" "+version, func(t *testing.T) {
				key := tuyaOther
				if liar {
					key = tuyaKey
				}
				f := startTuyaCRCFake(t, version, key, nil)
				f.liar = liar
				m := runTuyaFile(t, tuyaSource(t, f.port(), shorter...), "plug=127.0.0.1@"+version, tuyaPlugFile("plug"))
				tuyaEventually(t, "a reconnect after nothing readable", func() bool { return len(f.commands()) >= 2 })
				d, ok := tuyaDevice(m, "plug")
				if !ok || d.Online {
					t.Fatalf("it should be listed and offline: %+v", d)
				}
				_ = m.Write("tuya:plug", "switch_1", true) // the host queues it, so the refusal only shows in the frames
				time.Sleep(300 * time.Millisecond)
				if len(f.controlFrames()) != 0 || slices.ContainsFunc(f.commands(), func(c []uint32) bool { return slices.Contains(c, 7) }) {
					t.Fatalf("a command reached something unproven: %v", f.commands())
				}
			})
		}
	}
}

func tuyaSweepSource(t *testing.T, port int) string {
	text := tuyaSource(t, port, "const RECONNECT_MS = 10_000;", "const RECONNECT_MS = 50;", "const MIN_FAILING_MS = 60_000;", "const MIN_FAILING_MS = 0;", "const MIN_SWEEP_GAP_MS = 300_000;", "const MIN_SWEEP_GAP_MS = 0;", "const DIAL_MS = 2000;", "const DIAL_MS = 50;")
	return tuyaCandidates.ReplaceAllString(text, `function candidates(old: string, used: string[]): string[] { return old === "127.0.0.1" ? [] : ["127.0.0.1"]; }`+"\n")
}

// A status read is the proof from 3.3 down, and nothing but a read goes to an address that hasn't proved itself.
func TestTuyaOldProtocolRediscovery(t *testing.T) {
	for _, version := range []string{"3.2", "3.3"} {
		t.Run("stranger "+version, func(t *testing.T) {
			f := startTuyaCRCFake(t, version, tuyaOther, nil)
			m := runTuyaFile(t, tuyaSweepSource(t, f.port()), "plug=127.0.0.2@"+version, tuyaPlugFile("plug"))
			tuyaEventually(t, "a probe", func() bool {
				return slices.ContainsFunc(f.commands(), func(c []uint32) bool { return len(c) > 0 })
			})
			time.Sleep(1500 * time.Millisecond)
			if d, _ := tuyaDevice(m, "plug"); d.Online {
				t.Fatal("a peer without the key must not be adopted")
			}
			for _, c := range f.commands() {
				if len(c) > 1 || slices.Contains(c, 7) {
					t.Fatalf("a probe sent more than one read: %v", c)
				}
			}
		})
		t.Run("plain text "+version, func(t *testing.T) {
			f := startTuyaCRCFake(t, version, tuyaKey, nil)
			f.liar = true
			m := runTuyaFile(t, tuyaSweepSource(t, f.port()), "plug=127.0.0.2@"+version, tuyaPlugFile("plug"))
			tuyaEventually(t, "a probe", func() bool {
				return slices.ContainsFunc(f.commands(), func(c []uint32) bool { return len(c) > 0 })
			})
			time.Sleep(1500 * time.Millisecond)
			if d, _ := tuyaDevice(m, "plug"); d.Online {
				t.Fatal("an answer in plain text proves nothing")
			}
		})
		t.Run("the device "+version, func(t *testing.T) {
			f := startTuyaCRCFake(t, version, tuyaKey, map[string]map[string]any{"": {"1": true}})
			m := runTuyaFile(t, tuyaSweepSource(t, f.port()), "plug=127.0.0.2@"+version, tuyaPlugFile("plug"))
			eventuallyValue(t, m, "plug", "switch_1", true)
			var talked [][]uint32
			for _, c := range f.commands() {
				if len(c) > 0 {
					talked = append(talked, c)
				}
			}
			if len(talked) < 2 || len(talked[0]) != 1 {
				t.Fatalf("the first connection should be a single read, then the real link: %v", talked)
			}
			for _, read := range f.statusReads() {
				if dps, ok := read["dps"].(map[string]any); ok {
					for dp, value := range dps {
						if value != nil {
							t.Fatalf("a probe set %s: %v", dp, read)
						}
					}
				}
			}
			if len(f.controlFrames()) != 0 {
				t.Fatalf("a probe sent a command: %v", f.controlFrames())
			}
		})
	}
	t.Run("3.1 can't prove itself, so it doesn't look", func(t *testing.T) {
		f := startTuyaCRCFake(t, "3.1", tuyaKey, map[string]map[string]any{"": {"1": true}})
		m := runTuyaFile(t, tuyaSweepSource(t, f.port()), "plug=127.0.0.2@3.1", tuyaPlugFile("plug"))
		time.Sleep(1500 * time.Millisecond)
		if d, _ := tuyaDevice(m, "plug"); d.Online || len(f.commands()) != 0 {
			t.Fatalf("online %v, connections %v", d.Online, f.commands())
		}
	})
}

func tuyaGatewayFile(children ...map[string]any) []map[string]any {
	child := func(id, cid string) map[string]any {
		return map[string]any{"id": id, "name": id, "key": "kkkkkkkkkkkkkkk1", "category": "cz", "product_name": "Plug", "node_id": cid, "mapping": map[string]any{
			"1": map[string]any{"code": "switch_1", "type": "Boolean", "values": map[string]any{}},
		}}
	}
	a, b := child("ca", "cid-a"), child("cb", "cid-b")
	for i, extra := range children {
		for k, v := range extra {
			[]map[string]any{a, b}[i][k] = v
		}
	}
	gateway := func(id, key string) map[string]any {
		return map[string]any{"id": id, "name": id, "key": key, "category": "wg2", "product_name": "GW", "mapping": map[string]any{}}
	}
	return []map[string]any{gateway("gwa", tuyaKey), gateway("gwb", tuyaOther), a, b}
}

// Two fakes on one port number can't be, so the test's copy of the extension picks the port by address.
func tuyaTwoGatewaySource(t *testing.T, a, b tuyaPeer) string {
	return tuyaSource(t, a.port(), "conn = ctx.tcp.connect(link.host, PORT, { timeoutMs: DIAL_MS });",
		`conn = ctx.tcp.connect("127.0.0.1", link.host === "10.0.0.2" ? `+strconv.Itoa(b.port())+` : PORT, { timeoutMs: DIAL_MS });`)
}

func TestTuyaTwoGateways(t *testing.T) {
	hosts := "gwa=10.0.0.1@3.4,gwb=10.0.0.2@3.3"
	dps := map[string]map[string]any{"cid-a": {"1": true}, "cid-b": {"1": true}}
	for name, assign := range map[string][]map[string]any{
		"by parent":     {{"parent": "gwa"}, {"parent": "gwb"}},
		"by gateway_id": {{"gateway_id": "gwa"}, {"gateway_id": "gwb"}},
		"by shared key": {{"key": tuyaKey}, {"key": tuyaOther}},
	} {
		t.Run(name, func(t *testing.T) {
			a := startTuyaFake(t, "3.4", tuyaKey, dps)
			b := startTuyaCRCFake(t, "3.3", tuyaOther, dps)
			m := runTuyaFile(t, tuyaTwoGatewaySource(t, a, b), hosts, tuyaGatewayFile(assign...))
			eventuallyValue(t, m, "ca", "switch_1", true)
			eventuallyValue(t, m, "cb", "switch_1", true)
			if err := m.Write("tuya:cb", "switch_1", false); err != nil {
				t.Fatal(err)
			}
			if msg := tuyaWaitControls(t, b, 1); msg["cid"] != "cid-b" {
				t.Fatalf("control: %v", msg)
			}
			if err := m.Write("tuya:ca", "switch_1", false); err != nil {
				t.Fatal(err)
			}
			if msg := tuyaWaitControls(t, a, 1); msg["cid"] != "cid-a" {
				t.Fatalf("control: %v", msg)
			}
			if len(b.controlFrames()) != 1 {
				t.Fatalf("the other gateway got a child's command: %v", b.controlFrames())
			}
		})
	}
	t.Run("no clue falls back to the first gateway", func(t *testing.T) {
		a := startTuyaFake(t, "3.4", tuyaKey, dps)
		b := startTuyaCRCFake(t, "3.3", tuyaOther, dps)
		m := runTuyaFile(t, tuyaTwoGatewaySource(t, a, b), hosts, tuyaGatewayFile(map[string]any{"parent": ""}, map[string]any{"parent": "not-a-host"}))
		eventuallyValue(t, m, "cb", "switch_1", true)
		if err := m.Write("tuya:cb", "switch_1", false); err != nil {
			t.Fatal(err)
		}
		tuyaWaitControls(t, a, 1)
	})
}

// A battery sensor sleeps through the query: unknown stays unknown, its first report brings it online, and it stays so over a reconnect.
func TestTuyaSleepingSensor(t *testing.T) {
	f := startTuyaFake(t, "3.4", tuyaKey, map[string]map[string]any{})
	m := runTuya(t, tuyaSource(t, f.port(), "const RECONNECT_MS = 10_000;", "const RECONNECT_MS = 100;"), "gw1=127.0.0.1@3.4")
	tuyaEventually(t, "the gateway", func() bool { d, ok := tuyaDevice(m, "gw1"); return ok && d.Online })
	door, _ := tuyaDevice(m, "door1")
	if dp, _ := tuyaValue(door, "open"); door.Online || dp.Value != nil {
		t.Fatalf("a sleeper that never reported has no value and isn't online: %+v", door)
	}
	f.push("cid-door", map[string]any{"101": false})
	eventuallyValue(t, m, "door1", "open", false)
	f.closeConn()
	tuyaEventually(t, "offline with the link", func() bool { d, _ := tuyaDevice(m, "door1"); return !d.Online })
	tuyaEventually(t, "back, without it having said anything", func() bool { d, _ := tuyaDevice(m, "door1"); return d.Online })
	if d, _ := tuyaDevice(m, "th1"); d.Online {
		t.Fatal("one that never reported stays offline")
	}
}

// The real files, as esbuild bundles them from the folder. Nothing listens on this port, and nothing is sent.
func TestTuyaLoadsFromItsFolder(t *testing.T) {
	m := run(t, example(t, "tuya"), map[string]any{"devices": "tuya.json", "hosts": "plug1=127.0.0.1@3.3"}, allowLoopback, func(m *Manager) {
		configFile(t, m, "tuya.json", tuyaDevices())
	})
	tuyaEventually(t, "the device", func() bool { _, ok := tuyaDevice(m, "plug1"); return ok })
	// The version the folder declares, so bumping it doesn't break this test.
	source, _ := os.ReadFile(filepath.Join("..", "..", "extensions", "tuya", "extension.ts"))
	want := regexp.MustCompile(`\n  version: "([^"]+)"`).FindSubmatch(source)
	if s := status(m); s.Error != "" || want == nil || s.Version != string(want[1]) {
		t.Fatalf("status: %+v", s)
	}
}

package extension

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const key = "abcdef0123456789"

func fileExtension(body string) string {
	return `defineExtension({id:"a",name:"a",version:"1",access:{sockets:true,lan:true},
		settings:{devices:{label:"Devices",type:"file",required:true,secretFields:["key"]},host:{label:"h",type:"string"}},
		data:{everySeconds:60,fetch(ctx){` + body + `}}});`
}

func configFile(t *testing.T, m *Manager, name, text string) {
	t.Helper()
	path := filepath.Join(m.configDir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFileSettingsGiveTextAndStayOutOfThePage(t *testing.T) {
	m := run(t, folder(t, fileExtension(`return {size: JSON.parse(ctx.settings.devices).length}`)), map[string]any{"devices": "secrets/d.json"}, allowLoopback, func(m *Manager) {
		configFile(t, m, "secrets/d.json", `[{"id":"one","key":"`+key+`"}]`)
	})
	eventually(t, "the plugin to publish", func() bool { data, _, _ := m.plugins.Get("a"); return strings.Contains(string(data), `"size":1`) })
	status := m.Statuses()[0]
	if status.Settings[0].Value != "secrets/d.json" {
		t.Fatalf("the page should see the path, got %v", status.Settings[0].Value)
	}
}

func TestAFileFieldCantBeReturned(t *testing.T) {
	m := run(t, folder(t, fileExtension(`return {leak: JSON.parse(ctx.settings.devices)[0].key}`)), map[string]any{"devices": "d.json"}, allowLoopback, func(m *Manager) {
		configFile(t, m, "d.json", `[{"id":"one","key":"`+key+`"}]`)
	})
	failsWith(t, m, "secret")
}

func TestAFileFieldCantBeSent(t *testing.T) {
	udp, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	port := udp.LocalAddr().(*net.UDPAddr).Port
	m := run(t, folder(t, fileExtension(`ctx.udp.request("127.0.0.1", `+strconv.Itoa(port)+`, ctx.crypto.hex(JSON.parse(ctx.settings.devices)[0].key), {timeoutMs: 100})`)),
		map[string]any{"devices": "d.json"}, allowLoopback, func(m *Manager) { configFile(t, m, "d.json", `[{"key":"`+key+`"}]`) })
	failsWith(t, m, "can't be sent")
}

func TestAFilePathMustStayInTheConfigFolder(t *testing.T) {
	for _, path := range []string{"../outside.json", "/etc/hosts"} {
		failsWith(t, run(t, folder(t, fileExtension(`return 1`)), map[string]any{"devices": path}, allowLoopback), "inside your config folder")
	}
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	failsWith(t, run(t, folder(t, fileExtension(`return 1`)), map[string]any{"devices": "link.json"}, allowLoopback, func(m *Manager) {
		if err := os.Symlink(outside, filepath.Join(m.configDir, "link.json")); err != nil {
			t.Fatal(err)
		}
	}), "inside your config folder")
}

func TestAFileCantHaveADefault(t *testing.T) {
	failsWith(t, run(t, folder(t, `defineExtension({id:"a",name:"a",version:"1",settings:{f:{label:"f",type:"file",default:"secrets/x"}},data:{fetch(){return 1}}});`), nil), "can't have a default")
}

func TestScanFindsWhatAnswers(t *testing.T) {
	udp, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	go func() {
		buf := make([]byte, 64)
		for {
			n, from, err := udp.ReadFrom(buf)
			if err != nil {
				return
			}
			udp.WriteTo(buf[:n], from)
		}
	}()
	tcp, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	udpPort, tcpPort := udp.LocalAddr().(*net.UDPAddr).Port, tcp.Addr().(*net.TCPAddr).Port
	m := run(t, folder(t, `defineExtension({id:"a",name:"a",version:"1",access:{sockets:true,lan:true},data:{everySeconds:60,fetch(ctx){
		return {u: ctx.udp.scan(["127.0.0.1"], `+strconv.Itoa(udpPort)+`, "6869", {timeoutMs: 300}), t: ctx.tcp.scan(["127.0.0.1"], `+strconv.Itoa(tcpPort)+`, {timeoutMs: 300})};
	}}});`), nil, allowLoopback)
	eventually(t, "the plugin to publish", func() bool {
		data, _, _ := m.plugins.Get("a")
		return strings.Contains(string(data), `"data":"6869"`) && strings.Contains(string(data), `"t":["127.0.0.1"]`)
	})
}

func TestScanStaysPrivateAndNeedsLan(t *testing.T) {
	failsWith(t, run(t, folder(t, `defineExtension({id:"a",name:"a",version:"1",access:{sockets:true},data:{fetch(ctx){return ctx.tcp.scan(["192.168.1.1"], 80)}}});`), nil), `"lan"`)
	failsWith(t, run(t, folder(t, `defineExtension({id:"a",name:"a",version:"1",access:{sockets:true,lan:true},data:{fetch(ctx){return ctx.tcp.scan(["8.8.8.8"], 80)}}});`), nil), "private")
}

package extension

import (
	"net"
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

func TestFileSettingsGiveTextAndStayOutOfThePage(t *testing.T) {
	text := `[{"id":"one","key":"` + key + `"}]`
	m := run(t, folder(t, fileExtension(`return {size: JSON.parse(ctx.settings.devices).length}`)), map[string]any{"devices": text}, allowLoopback)
	eventually(t, "the plugin to publish", func() bool { data, _, _ := m.plugins.Get("a"); return strings.Contains(string(data), `"size":1`) })
	status := m.Statuses()[0]
	if !status.Settings[0].Set || status.Settings[0].Value != nil {
		t.Fatalf("the page may know it is set, never what it holds: %+v", status.Settings[0])
	}
}

func TestAFileFieldCantBeReturned(t *testing.T) {
	m := run(t, folder(t, fileExtension(`return {leak: JSON.parse(ctx.settings.devices)[0].key}`)), map[string]any{"devices": `[{"id":"one","key":"` + key + `"}]`}, allowLoopback)
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
		map[string]any{"devices": `[{"key":"` + key + `"}]`}, allowLoopback)
	failsWith(t, m, "can't be sent")
}

func TestAFileOverOneMiBIsRefused(t *testing.T) {
	failsWith(t, run(t, folder(t, fileExtension(`return 1`)), map[string]any{"devices": strings.Repeat("x", 1<<20+1)}, allowLoopback), "over 1 MiB")
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

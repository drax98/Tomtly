package extension

import (
	"context"
	"encoding/hex"
	"fmt"
	"github.com/dop251/goja"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"tomtly/internal/store"
)

// freePorts finds ports above 1024 that nothing holds right now.
func freePorts(t *testing.T, n int) []int {
	t.Helper()
	var ports []int
	for range n {
		conn, err := net.ListenPacket("udp", ":0")
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close() // held until all are found, so they differ
		ports = append(ports, conn.LocalAddr().(*net.UDPAddr).Port)
	}
	return ports
}

func portList(ports []int) string {
	var text []string
	for _, p := range ports {
		text = append(text, strconv.Itoa(p))
	}
	return strings.Join(text, ",")
}

func TestListenIsLimitedToDeclaredPorts(t *testing.T) {
	port := freePorts(t, 1)[0]
	other := strconv.Itoa(port + 1)
	head := `defineExtension({id:"a",name:"a",version:"1",`
	for name, c := range map[string]struct{ source, want, definitionError string }{
		"undeclared udp port":  {source: head + `access:{listen:[` + strconv.Itoa(port) + `]},data:{fetch(c){c.udp.listen(` + other + `)}}});`, want: "needs that port in access.listen"},
		"undeclared tcp port":  {source: head + `access:{listen:[` + strconv.Itoa(port) + `]},data:{fetch(c){c.tcp.listen(` + other + `)}}});`, want: "needs that port in access.listen"},
		"no listen access":     {source: head + `data:{fetch(c){c.udp.listen(` + strconv.Itoa(port) + `)}}});`, want: "needs that port in access.listen"},
		"declared as zero":     {source: head + `access:{listen:[0]},data:{fetch(){}}});`, definitionError: "from 1 to 65535"},
		"declared above range": {source: head + `access:{listen:[70000]},data:{fetch(){}}});`, definitionError: "from 1 to 65535"},
		"declared twice":       {source: head + `access:{listen:[5000,5000]},data:{fetch(){}}});`, definitionError: "listed twice"},
	} {
		t.Run(name, func(t *testing.T) {
			m := run(t, folder(t, c.source), nil)
			if c.definitionError != "" {
				failsWith(t, m, c.definitionError)
				return
			}
			failsWith(t, m, c.want)
			expectViolation(t, m, "listen")
		})
	}
	t.Run("a port it didn't list is refused even if it asks at run time", func(t *testing.T) {
		m := run(t, folder(t, head+`access:{listen:[`+strconv.Itoa(port)+`]},data:{fetch(c){c.tcp.listen(80)}}});`), nil)
		failsWith(t, m, "needs that port in access.listen")
		expectViolation(t, m, "listen")
	})
}

func TestListenLabelsThePorts(t *testing.T) {
	m := run(t, folder(t, `defineExtension({id:"a",name:"a",version:"1",access:{listen:[40001,40002],webhooks:true},webhooks:{receive(){}}});`), nil)
	eventually(t, "it to load", func() bool { return len(status(m).Access) > 0 })
	got := strings.Join(status(m).Access, "|")
	if !strings.Contains(got, "port 40001") || !strings.Contains(got, "port 40002") || !strings.Contains(got, "Receive messages from outside") {
		t.Fatalf("got %s", got)
	}
}

func TestListenReceivesDatagramsAndConnections(t *testing.T) {
	ports := freePorts(t, 2)
	udp, tcp := ports[0], ports[1]
	m := run(t, folder(t, `defineExtension({id:"a",name:"a",version:"1",access:{listen:[`+portList(ports)+`]},background:{everySeconds:0.05,run(c){
		sock = sock || c.udp.listen(`+strconv.Itoa(udp)+`);
		server = server || c.tcp.listen(`+strconv.Itoa(tcp)+`);
		const d = sock.read({timeoutMs: 20});
		if (d) got.push("udp:" + d.data);
		const conn = server.accept({timeoutMs: 20});
		if (conn) { const data = conn.read({timeoutMs: 500}); got.push("tcp:" + data); conn.write(data); conn.close(); }
		c.publish(got);
	}}}); let sock, server; const got = [];`), nil)
	eventually(t, "the listeners", func() bool {
		conn, err := net.Dial("udp", "127.0.0.1:"+strconv.Itoa(udp))
		if err != nil {
			return false
		}
		defer conn.Close()
		conn.Write([]byte{1, 2, 3})
		d, _, _ := m.plugins.Get("a")
		return strings.Contains(string(d), "udp:010203")
	})
	conn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(tcp))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.Write([]byte("hi"))
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	echo := make([]byte, 2)
	if _, err := conn.Read(echo); err != nil || string(echo) != "hi" {
		t.Fatalf("echo %q %v", echo, err)
	}
	eventually(t, "the tcp message", func() bool {
		d, _, _ := m.plugins.Get("a")
		return strings.Contains(string(d), "tcp:"+hex.EncodeToString([]byte("hi")))
	})
}

func TestListenersCloseWhenTheExtensionStops(t *testing.T) {
	port := freePorts(t, 1)[0]
	ctx, cancel := context.WithCancel(context.Background())
	m := newManager(t.TempDir(), t.TempDir(), store.NewMemory(), hub(t))
	t.Cleanup(func() { stopAll(m) })
	inst := m.add(ctx, entry{Source: folder(t, `defineExtension({id:"a",name:"a",version:"1",access:{listen:[`+strconv.Itoa(port)+`]},background:{everySeconds:0.05,run(c){ sock = sock || c.udp.listen(`+strconv.Itoa(port)+`); }}}); let sock;`)})
	eventually(t, "it to listen", func() bool {
		conn, err := net.ListenPacket("udp", ":"+strconv.Itoa(port))
		if err == nil {
			conn.Close()
			return false
		}
		return true
	})
	cancel()
	<-inst.done
	eventually(t, "the port to be free", func() bool {
		conn, err := net.ListenPacket("udp", ":"+strconv.Itoa(port))
		if err != nil {
			return false
		}
		conn.Close()
		return true
	})
}

func TestListenersCountTowardsTheConnectionLimit(t *testing.T) {
	ports := freePorts(t, maxConnections+1)
	m := run(t, folder(t, `defineExtension({id:"a",name:"a",version:"1",access:{listen:[`+portList(ports)+`]},data:{fetch(c){
		for (const p of [`+portList(ports)+`]) c.udp.listen(p);
	}}});`), nil)
	failsWith(t, m, "connections are open")
}

func TestTCPSpeaksTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "over-tls") }))
	defer server.Close()
	host, port, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "https://"))
	if err != nil {
		t.Fatal(err)
	}
	source := func(options string) string {
		return `defineExtension({id:"a",name:"a",version:"1",access:{network:["{host}"],sockets:true},settings:{host:{label:"H",type:"string",required:true}},
			data:{fetch(c){const k=c.crypto,s=c.tcp.connect(c.settings.host,` + port + `,` + options + `);s.write(k.hex("GET / HTTP/1.0\r\n\r\n"));const r=k.text(s.read());s.close();return r.includes("over-tls");}}});`
	}
	m := run(t, folder(t, source(`{tls:true,insecure:true}`)), map[string]any{"host": host}, allowLoopback)
	eventually(t, "data over TLS", func() bool { d, _, _ := m.plugins.Get("a"); return string(d) == "true" })

	failsWith(t, run(t, folder(t, source(`{tls:true}`)), map[string]any{"host": host}, allowLoopback), "certificate")

	vm := goja.New()
	if _, err := wrapTLS(context.Background(), vm, &network{}, nopConn{}, "public.example.com", false, vm.ToValue(map[string]any{"tls": true, "insecure": true})); err == nil || !strings.Contains(err.Error(), "local network") {
		t.Errorf("insecure TLS to an internet host must be refused: %v", err)
	}
}

type nopConn struct{ net.Conn }

func (nopConn) Close() error { return nil }

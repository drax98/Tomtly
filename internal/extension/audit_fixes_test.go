package extension

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"testing"
	"time"

	"tomtly/internal/store"
	"tomtly/plugin"
)

// A definition is code, so it can stop calling a setting a secret once it has been approved as one.
func TestASecretCantStopBeingOneAfterApproval(t *testing.T) {
	needApproval(t)
	st := store.NewMemory()
	m := newManager(t.TempDir(), t.TempDir(), st, plugin.NewHub())
	ctx := context.Background()
	if err := st.SetGrants(ctx, "x", []string{"secret:token", "network:a.example.com"}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	write(t, dir, entryFile, `defineExtension({id:"x",name:"x",version:"1",access:{network:["a.example.com"]},settings:{token:{label:"T",type:"string"}},data:{fetch(){return 1}}});`)
	l, err := load(dir)
	if err != nil {
		t.Fatal(err)
	}
	settings := map[string]any{"token": "correct-horse-battery-staple"}
	i := &instance{manager: m, removable: true, approve: make(chan struct{}, 1)}
	i.describe(l, dir, settings)
	if i.granted(ctx) {
		t.Fatal("it runs with a key the owner approved as a secret now in plain view")
	}
	i.guardSecrets(ctx, l, settings)
	s := i.currentStatus()
	for _, setting := range s.Settings {
		if setting.Value != nil {
			t.Errorf("the page is shown %v", setting.Value)
		}
	}
	if !strings.Contains(strings.Join(s.Risks, " "), "no longer treats it as one") {
		t.Errorf("the owner isn't told: %v", s.Risks)
	}
	if v, _ := st.Violations(ctx, "x", 5); len(v) != 1 {
		t.Errorf("not recorded: %v", v)
	}
	if err := i.leak("a.example.com", "correct-horse-battery-staple"); err == nil {
		t.Error("the key can leave while waiting")
	}
}

func TestAnUpdateCantChangeWhereItComesFrom(t *testing.T) {
	for _, c := range []struct {
		from, to string
		same     bool
	}{
		{"github:a/b@v1", "github:a/b@v2", true},
		{"market:weather@1.0.0#aa", "market:weather@1.1.0#bb", true},
		{"./extensions/v1", "./extensions/v2", true},
		{"upload:aa", "upload:bb", true},
		{"./extensions/v1", "upload:aa", true},
		{"github:a/b@v1", "github:attacker/b@v1", false},
		{"github:a/b@v1", "upload:aa", false},
		{"market:weather@1.0.0#aa", "market:other@1.0.0#aa", false},
		{"market:weather@1.0.0#aa", "./extensions/weather", false},
	} {
		if got := originOf(c.from) == originOf(c.to); got != c.same {
			t.Errorf("%s to %s: same origin is %v", c.from, c.to, got)
		}
	}
}

func TestLanDoesntReachPortsThatPassDataOn(t *testing.T) {
	lan := newNetwork(accessDecl{Lan: true, Sockets: true}, nil, false)
	for _, port := range relayPorts {
		if _, err := lan.socketRule("192.168.1.1", port); err == nil {
			t.Errorf("port %d is open to any lan extension", port)
		}
	}
	if _, err := lan.socketRule("192.168.1.1", 6668); err != nil {
		t.Errorf("a device's own port: %v", err)
	}
	if _, err := newNetwork(accessDecl{Lan: true}, nil, false).clientFor(&url.URL{Scheme: "http", Host: "192.168.1.1:1900"}); err == nil {
		t.Error("fetch reaches a port-mapping service")
	}
	named := newNetwork(accessDecl{Network: []string{"{host}"}}, map[string]any{"host": "192.168.1.1"}, false)
	if _, err := named.socketRule("192.168.1.1", 53); err != nil {
		t.Errorf("an address the owner typed is theirs to allow: %v", err)
	}
}

func TestASocketKeepsToAPortItNames(t *testing.T) {
	n := newNetwork(accessDecl{Network: []string{"{host}"}}, map[string]any{"host": "10.0.0.5:9000"}, false)
	if _, err := n.socketRule("10.0.0.5", 9000); err != nil {
		t.Error(err)
	}
	if _, err := n.socketRule("10.0.0.5", 22); err == nil {
		t.Error("another port was reached")
	}
}

func TestTLSNameCantFrontForAnotherSite(t *testing.T) {
	n := newNetwork(accessDecl{}, nil, false)
	if _, err := n.serverName("api.vendor.com", "evil.example.net", false); err == nil {
		t.Error("another name on a public host")
	}
	if got, err := n.serverName("api.vendor.com", "API.vendor.com", false); err != nil || got == "" {
		t.Errorf("the host itself: %q %v", got, err)
	}
	if got, err := n.serverName("10.0.0.5", "lamp.local", true); err != nil || got != "lamp.local" {
		t.Errorf("a device's own name: %q %v", got, err)
	}
}

func TestSpecialUseAddressesArentPublic(t *testing.T) {
	for _, ip := range []string{"100.100.100.200", "192.0.0.192", "198.18.0.1", "240.0.0.1", "64:ff9b::1"} {
		if !isSpecialUse(net.ParseIP(ip)) {
			t.Errorf("%s counts as public", ip)
		}
	}
	if isSpecialUse(net.ParseIP("93.184.216.34")) {
		t.Error("a public address was refused")
	}
}

// Past the stack limit this would be a fatal error for the whole service, which recover can't catch.
func TestDeepRecursionOnlyFailsTheExtension(t *testing.T) {
	m := run(t, folder(t, `defineExtension({id:"a",name:"a",version:"1",data:{fetch(){function f(){return [1].map(f)}; f(); return 1}}});`), nil)
	failsWith(t, m, "too many times")
}

func TestFloodingDestinationsIsRecorded(t *testing.T) {
	st := store.NewMemory()
	m := newManager(t.TempDir(), t.TempDir(), st, plugin.NewHub())
	i := &instance{manager: m, status: Status{ID: "x"}}
	for n := range maxActivityKept + 5 {
		i.note("tcp", fmt.Sprint("10.0.0.1:", n))
	}
	v, _ := st.Violations(context.Background(), "x", 10)
	if len(v) != 1 || v[0].Kind != "flood" {
		t.Errorf("violations: %v", v)
	}
}

func TestViolationsCarryOnAfterARestart(t *testing.T) {
	st := store.NewMemory()
	m := newManager(t.TempDir(), t.TempDir(), st, plugin.NewHub())
	ctx := context.Background()
	for range 4 {
		if err := st.RecordViolation(ctx, store.Violation{Extension: "x", Kind: "network"}); err != nil {
			t.Fatal(err)
		}
	}
	i := &instance{manager: m}
	i.resumeViolations(ctx, "x")
	if i.violations != 4 {
		t.Errorf("count is %d", i.violations)
	}
}

func TestRiskIsSpelledOut(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, entryFile, `defineExtension({id:"x",name:"x",version:"1",access:{network:["{other}"],lan:true},
		settings:{other:{label:"Other",type:"string"},apiKey:{label:"API key",type:"string"},token:{label:"Token",type:"secret"}},data:{fetch(){return 1}}});`)
	l, err := load(dir)
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(l.riskWarnings(), " ")
	for _, want := range []string{"the address in the setting other", "including your router", "It asks for API key but doesn't treat it as a secret"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in %q", want, all)
		}
	}
}

// What's checked is the address really dialled, so a listed public name that resolves to one of these gets no further.
func TestAPublicHostCantDialAnythingPrivate(t *testing.T) {
	public := newNetwork(accessDecl{}, nil, false).dialer(false)
	for _, address := range []string{"10.0.0.1:80", "192.168.1.1:80", "127.0.0.1:80", "169.254.169.254:80", "[fe80::1]:80", "100.100.100.200:80", "192.0.0.192:80", "224.0.0.1:80", "0.0.0.0:80"} {
		if err := public.Control("tcp", address, nil); err == nil {
			t.Errorf("%s can be dialled", address)
		}
	}
	if err := public.Control("tcp", "93.184.216.34:443", nil); err != nil {
		t.Errorf("a public address: %v", err)
	}
	// An address the owner typed may be on the LAN or a VPN, but never metadata or this machine.
	local := newNetwork(accessDecl{}, nil, false).dialer(true)
	for _, address := range []string{"192.168.1.1:80", "100.100.100.200:80"} {
		if err := local.Control("tcp", address, nil); err != nil {
			t.Errorf("%s: %v", address, err)
		}
	}
	for _, address := range []string{"169.254.169.254:80", "127.0.0.1:80"} {
		if err := local.Control("tcp", address, nil); err == nil {
			t.Errorf("%s can be dialled", address)
		}
	}
}

func TestReplaceRefusesAnotherOrigin(t *testing.T) {
	m := pageManager(t)
	write(t, m.configDir, "extensions/v2/"+entryFile, versionTwo)
	inst := &instance{manager: m, entry: entry{Source: "github:owner/repo@v1"}, removable: true, status: Status{ID: "keep"}, cancel: func() {}, done: make(chan struct{})}
	close(inst.done)
	m.mu.Lock()
	m.instances = append(m.instances, inst)
	m.mu.Unlock()
	_, err := replaceSeen(m, context.Background(), "github:owner/repo@v1", "./extensions/v2", nil)
	if err == nil || !strings.Contains(err.Error(), "can't change where it comes from") {
		t.Fatalf("a stranger's folder with the same id: %v", err)
	}
}

func TestAScanCantUseRelayPorts(t *testing.T) {
	m := run(t, folder(t, `defineExtension({id:"a",name:"a",version:"1",access:{lan:true,sockets:true},data:{fetch(c){c.udp.scan(["192.168.1.1"],53,"00",{timeoutMs:20});return 1}}});`), nil)
	failsWith(t, m, "can pass data out of your network")
	expectViolation(t, m, "lan")
}

func TestARawDatagramCantCarryASecret(t *testing.T) {
	m := run(t, folder(t, `defineExtension({id:"a",name:"a",version:"1",access:{lan:true,sockets:true},settings:{key:{label:"K",type:"secret",required:true}},
		data:{fetch(c){c.udp.request("192.168.1.9",7000,c.crypto.hex(c.settings.key),{timeoutMs:20});return 1}}});`), map[string]any{"key": "abcdefgh12"})
	failsWith(t, m, "can't be sent")
	expectViolation(t, m, "secret")
}

// The switch flips back at the next poll, and without this nothing would ever say why.
func TestAFailedCommandStaysOnTheCardPastAGoodPoll(t *testing.T) {
	m := run(t, folder(t, `let fail = true; defineExtension({id:"a",name:"a",version:"1",devices:{everySeconds:1,
		poll(){return [{id:"d",name:"D",values:[{code:"on",type:"Boolean",writable:true,value:false}]}]},
		write(){ if (fail) { fail = false; throw new Error("the device refused"); } }}});`), nil)
	eventually(t, "the device", func() bool { return len(m.Devices()) == 1 })
	if err := m.Write("a:d", "on", true); err != nil {
		t.Fatal(err)
	}
	failsWith(t, m, "the device refused")
	time.Sleep(1200 * time.Millisecond) // past a poll
	if !strings.Contains(status(m).Error, "the device refused") {
		t.Errorf("a good poll hid it: %q", status(m).Error)
	}
	if d := m.Devices()[0]; !strings.Contains(d.Error, "the device refused") {
		t.Errorf("the device doesn't say why: %+v", d)
	}
	if err := m.Write("a:d", "on", true); err != nil {
		t.Fatal(err)
	}
	eventually(t, "a working command to clear it", func() bool { return status(m).Error == "" })
}

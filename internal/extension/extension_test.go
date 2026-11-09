package extension

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"

	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"home-control/device"
	"home-control/device/devicetest"
	"home-control/internal/store"
	"home-control/plugin"
)

func init() { approveAll.Store(true) }

func hub(t *testing.T) *plugin.Hub {
	t.Helper()
	return plugin.NewHub()
}

func write(t *testing.T, dir, name, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for range 400 {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for " + what)
}

// run starts one extension from a folder, or the given source, and returns it once it has settled.
func run(t *testing.T, source string, settings map[string]any, setup ...func(*Manager)) *Manager {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m := newManager(t.TempDir(), t.TempDir(), store.NewMemory(), hub(t))
	t.Cleanup(func() { stopAll(m) })
	for _, f := range setup {
		f(m)
	}
	m.add(ctx, entry{Source: source, Settings: settings}, false)
	return m
}

func example(t *testing.T, name string) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "extensions", name))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func allowLoopback(m *Manager) { m.allowLoopback = true }

func folder(t *testing.T, source string) string {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, entryFile, source)
	return dir
}

func status(m *Manager) Status { return m.Statuses()[0] }

func failsWith(t *testing.T, m *Manager, want string) {
	t.Helper()
	eventually(t, "the error "+want, func() bool { return strings.Contains(status(m).Error, want) })
}

func TestDefinitionMistakesAreExplained(t *testing.T) {
	for want, source := range map[string]string{
		"never called":         `const x = 1;`,
		"called twice":         `const d = {id:"a",name:"a",version:"1",data:{fetch(){}}}; defineExtension(d); defineExtension(d);`,
		"lowercase":            `defineExtension({id:"A B",name:"a",version:"1",data:{fetch(){}}});`,
		"name and a":           `defineExtension({id:"a",data:{fetch(){}}});`,
		"needs \"data\"":       `defineExtension({id:"a",name:"a",version:"1"});`,
		"fetch function":       `defineExtension({id:"a",name:"a",version:"1",data:{}});`,
		"poll function":        `defineExtension({id:"a",name:"a",version:"1",devices:{}});`,
		"type must be":         `defineExtension({id:"a",name:"a",version:"1",data:{fetch(){}},settings:{k:{label:"k",type:"nope"}}});`,
		"can't have a default": `defineExtension({id:"a",name:"a",version:"1",data:{fetch(){}},settings:{k:{label:"k",type:"secret",default:"x"}}});`,
		"network entry":        `defineExtension({id:"a",name:"a",version:"1",data:{fetch(){}},access:{network:["http://x/y"]}});`,
		"called host":          `defineExtension({id:"a",name:"a",version:"1",data:{fetch(){}},access:{network:["{host}"]}});`,
		"too long":             `while (true) {}`,
	} {
		t.Run(want, func(t *testing.T) { failsWith(t, run(t, folder(t, source), nil), want) })
	}
}

func TestExtensionsCantImportOutsideTheirFolder(t *testing.T) {
	parent := t.TempDir()
	write(t, parent, "secrets.json", `{"key":"x"}`)
	write(t, parent, "ext/"+entryFile, `import s from "../secrets.json"; defineExtension({id:"a",name:"a",version:"1",data:{fetch(){return s}}});`)
	failsWith(t, run(t, filepath.Join(parent, "ext"), nil), "own folder")
}

func TestSettingsAreCheckedAgainstTheDefinition(t *testing.T) {
	source := `defineExtension({id:"a",name:"a",version:"1",data:{fetch:(c)=>[c.settings.city,c.settings.units]},settings:{
		city:{label:"City",type:"string",required:true},
		units:{label:"Units",type:"string",default:"metric"},
		limit:{label:"Limit",type:"number"}}});`
	for want, given := range map[string]map[string]any{
		`"city" is required`:  {},
		`unknown setting "x"`: {"city": "a", "x": 1},
		`"limit" must be`:     {"city": "a", "limit": "3"},
	} {
		t.Run(want, func(t *testing.T) { failsWith(t, run(t, folder(t, source), given), want) })
	}
	m := run(t, folder(t, source), map[string]any{"city": "Warsaw"})
	var got string
	eventually(t, "data", func() bool { d, _, _ := m.plugins.Get("a"); got = string(d); return d != nil })
	if got != `["Warsaw","metric"]` {
		t.Fatalf("got %s", got)
	}
}

func TestSecretsNeverLeave(t *testing.T) {
	const secret = "s3cr3t/key"
	definition := `id:"a",name:"a",version:"1",settings:{key:{label:"Key",type:"secret",required:true}}`

	t.Run("not in an error, even inside a URL", func(t *testing.T) {
		m := run(t, folder(t, `defineExtension({`+definition+`,data:{fetch(c){
			throw new Error("bad " + c.settings.key + " and " + encodeURIComponent(c.settings.key));
		}}});`), map[string]any{"key": secret})
		failsWith(t, m, "bad ***")
		if text := fmt.Sprint(m.Statuses(), m.plugins.Statuses()); strings.Contains(text, secret) || strings.Contains(text, "s3cr3t") {
			t.Fatalf("leaked: %s", text)
		}
	})
	t.Run("not in data", func(t *testing.T) {
		m := run(t, folder(t, `defineExtension({`+definition+`,data:{fetch(c){return {k:c.settings.key}}}});`), map[string]any{"key": secret})
		failsWith(t, m, "secret settings")
		if d, _, _ := m.plugins.Get("a"); d != nil {
			t.Fatalf("published %s", d)
		}
	})
	t.Run("not in devices", func(t *testing.T) {
		m := run(t, folder(t, `defineExtension({`+definition+`,devices:{poll(c){return [{id:"d",name:c.settings.key,values:[]}]}}});`), map[string]any{"key": secret})
		failsWith(t, m, "secret settings")
		if len(m.Devices()) != 0 {
			t.Fatal("published a device")
		}
	})
	t.Run("the page only hears whether it is set", func(t *testing.T) {
		m := run(t, folder(t, `defineExtension({`+definition+`,data:{fetch(){return 1}}});`), map[string]any{"key": secret})
		eventually(t, "status", func() bool { return status(m).Running })
		data, _ := json.Marshal(status(m))
		if strings.Contains(string(data), secret) || !status(m).Settings[0].Set {
			t.Fatalf("got %s", data)
		}
	})
}

func TestNetworkIsLimitedToWhatWasDeclared(t *testing.T) {
	var hits int
	local := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ }))
	defer local.Close()
	host := strings.TrimPrefix(local.URL, "http://")
	try := func(target string) string {
		return `defineExtension({id:"a",name:"a",version:"1",access:{network:["api.example.com","{host}"]},settings:{host:{label:"H",type:"string"}},
			data:{fetch(c){c.fetch(` + fmt.Sprintf("%q", target) + `);return 1}}});`
	}
	for want, c := range map[string]struct {
		target   string
		settings map[string]any
	}{
		"network list":   {"https://elsewhere.example.com/", nil},
		"must use https": {"http://api.example.com/", nil},
		"own services":   {local.URL, map[string]any{"host": host}}, // loopback stays closed, even from a setting
		"isn't in this":  {local.URL, nil},
	} {
		t.Run(want, func(t *testing.T) { failsWith(t, run(t, folder(t, try(c.target)), c.settings), want) })
	}
	if hits != 0 {
		t.Fatalf("%d requests got through", hits)
	}
}

func TestPrivateAddressOnlyThroughASetting(t *testing.T) {
	n := newNetwork(accessDecl{Network: []string{"public.example.com", "{host}"}}, map[string]any{"host": "lan.example.com"}, false)
	for _, c := range []struct {
		name    string
		client  *http.Client
		address string
		allowed bool
	}{
		{"public name resolving to a private address", n.publicClient, "192.168.1.5:80", false},
		{"setting on the LAN", n.localClient, "192.168.1.5:80", true},
		{"metadata service", n.localClient, "169.254.169.254:80", false},
		{"this machine", n.localClient, "127.0.0.1:8099", false},
	} {
		dial := n.client(c.client == n.localClient).Transport.(*http.Transport).DialContext
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		conn, err := dial(ctx, "tcp", c.address)
		cancel()
		if conn != nil {
			conn.Close()
		}
		refused := err != nil && strings.Contains(err.Error(), "extensions can't") || err != nil && strings.Contains(err.Error(), "local network")
		if refused == c.allowed {
			t.Errorf("%s: allowed=%v, got %v", c.name, c.allowed, err)
		}
	}
}

func TestRedirectIsNotFollowed(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { t.Error("followed a redirect") }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer redirect.Close()
	m := run(t, folder(t, `defineExtension({id:"a",name:"a",version:"1",access:{network:["{host}"]},settings:{host:{label:"H",type:"string",required:true}},
		data:{fetch(c){return c.fetch("http://"+c.settings.host+"/").status}}});`),
		map[string]any{"host": strings.TrimPrefix(redirect.URL, "http://")}, allowLoopback)
	eventually(t, "data", func() bool { d, _, _ := m.plugins.Get("a"); return string(d) == "302" })
}

func TestFailedFetchKeepsLastData(t *testing.T) {
	m := run(t, folder(t, `let n = 0; defineExtension({id:"a",name:"a",version:"1",data:{everySeconds:1,fetch(){ if (++n > 1) throw new Error("down"); return {n}; }}});`), nil)
	failsWith(t, m, "down")
	if d, _, _ := m.plugins.Get("a"); string(d) != `{"n":1}` {
		t.Fatalf("got %s", d)
	}
	if ps := m.plugins.Statuses(); ps[0].Error == "" || ps[0].Type != "extension" {
		t.Fatalf("got %+v", ps[0])
	}
}

func TestTwoExtensionsCantShareAnID(t *testing.T) {
	source := `defineExtension({id:"a",name:"a",version:"1",data:{fetch(){return 1}}});`
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newManager(t.TempDir(), t.TempDir(), store.NewMemory(), hub(t))
	t.Cleanup(func() { stopAll(m) })
	m.add(ctx, entry{Source: folder(t, source)}, false)
	m.add(ctx, entry{Source: folder(t, source)}, false)
	eventually(t, "one to be refused", func() bool {
		for _, s := range m.Statuses() {
			if strings.Contains(s.Error, "already") {
				return true
			}
		}
		return false
	})
}

// A fake Hue bridge, which is what the example integration talks to.
func bridge(t *testing.T, refused *atomic.Bool) (*httptest.Server, *[]string) {
	t.Helper()
	on, puts := false, &[]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if refused.Load() {
			fmt.Fprint(w, `[{"error":{"description":"unauthorized user"}}]`)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/api/the-key/lights") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Method == http.MethodPut {
			var body bytes.Buffer
			body.ReadFrom(r.Body)
			*puts = append(*puts, r.URL.Path+" "+body.String())
			on = strings.Contains(body.String(), "true")
			return
		}
		fmt.Fprintf(w, `{"1":{"name":"Desk","productname":"Bulb","state":{"on":%v,"bri":100,"reachable":true}},"2":{"name":"Hall","state":{"on":false,"reachable":false}}}`, on)
	}))
	t.Cleanup(server.Close)
	return server, puts
}

func TestHueExample(t *testing.T) {
	var refused atomic.Bool
	server, puts := bridge(t, &refused)
	settings := map[string]any{"host": strings.TrimPrefix(server.URL, "http://"), "apiKey": "the-key"}
	m := run(t, example(t, "hue"), settings, allowLoopback)
	eventually(t, "devices", func() bool { return len(m.Devices()) == 2 })

	devicetest.Conformance(t, m, "the-key")
	desk, hall := m.Devices()[0], m.Devices()[1]
	if desk.ID != "hue:1" || desk.Kind != "light" || !desk.Online || hall.Online {
		t.Fatalf("got %+v %+v", desk, hall)
	}
	if len(hall.DPs) != 1 {
		t.Errorf("a light without brightness shouldn't get a brightness: %+v", hall.DPs)
	}
	if err := m.Write("hue:1", "brightness", 999); err == nil {
		t.Error("a value over the declared maximum must be refused")
	}
	if err := m.Write("hue:1", "switch", true); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the new value", func() bool { return device.Equal(m.Devices()[0].DPs[0].Value, true) })
	if len(*puts) != 1 || (*puts)[0] != `/api/the-key/lights/1/state {"on":true}` {
		t.Errorf("got %v", *puts)
	}

	// A write is followed by a poll straight away, so these don't wait for the 10 second schedule.
	refused.Store(true)
	if err := m.Write("hue:1", "switch", false); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the failure", func() bool { return strings.Contains(status(m).Error, "unauthorized") })
	eventually(t, "offline", func() bool { return !m.Devices()[0].Online })
	refused.Store(false)
	if err := m.Write("hue:1", "switch", false); err != nil {
		t.Fatal(err)
	}
	eventually(t, "recovery", func() bool { return status(m).Error == "" && m.Devices()[0].Online })
}

func TestWeatherExample(t *testing.T) {
	dir := example(t, "weather")
	l, err := load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if l.ID != "weather" || l.Data == nil || l.access.Network[0] != "api.open-meteo.com" {
		t.Fatalf("got %+v", l.definition)
	}
	if _, err := resolveSettings(l.Settings, map[string]any{"latitude": 1.0}); err == nil {
		t.Error("longitude must be required")
	}
	m := newManager(t.TempDir(), t.TempDir(), store.NewMemory(), hub(t))
	t.Cleanup(func() { stopAll(m) })
	if types := func() map[string]string {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		m.add(ctx, entry{Source: dir, Settings: map[string]any{"latitude": 1.0, "longitude": 2.0}}, false)
		eventually(t, "status", func() bool { return status(m).ID == "weather" })
		return m.Types()
	}(); !strings.Contains(types["extensions/weather/types.d.ts"], "interface Weather") {
		t.Errorf("got %v", types)
	}
}

func archive(t *testing.T, entries map[string]string, extra ...tar.Header) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	w := tar.NewWriter(gz)
	for name, text := range entries {
		if err := w.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(text))}); err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(text))
	}
	for _, h := range extra {
		if err := w.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
	}
	w.Close()
	gz.Close()
	return out.Bytes()
}

func pageManager(t *testing.T) *Manager {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m := newManager(t.TempDir(), t.TempDir(), store.NewMemory(), hub(t))
	t.Cleanup(func() { stopAll(m) })
	m.ctx = ctx
	write(t, m.configDir, "extensions/mine/"+entryFile, `defineExtension({id:"mine",name:"Mine",version:"2",access:{network:["a.example.com"]},data:{fetch(c){return {on:c.settings.on}}},
		settings:{key:{label:"Key",type:"secret",required:true},on:{label:"On",type:"boolean",default:true},note:{label:"Note",type:"string"}}});`)
	return m
}

const hostSettings = `settings:{host:{label:"H",type:"string",required:true},port:{label:"P",type:"number",required:true}}`

// violationsOf lists what an extension was refused, newest first.
func violationsOf(t *testing.T, m *Manager, id string) []store.Violation {
	t.Helper()
	v, err := m.store.Violations(context.Background(), id, 100)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func hasViolation(t *testing.T, m *Manager, kind string) bool {
	t.Helper()
	for _, v := range violationsOf(t, m, "a") {
		if v.Kind == kind {
			return true
		}
	}
	return false
}

func expectViolation(t *testing.T, m *Manager, kind string) {
	t.Helper()
	eventually(t, "a "+kind+" violation", func() bool { return hasViolation(t, m, kind) })
}

type brokenGrants struct{ store.Store }

func (brokenGrants) Grants(context.Context, string) ([]string, error) {
	return nil, fmt.Errorf("disk gone")
}

// stopAll stops a manager's extensions and waits, so none is still writing to a temp folder as it's removed. Cleanups run
// last registered first, so register this after the manager's folders are made.
func stopAll(m *Manager) {
	m.mu.RLock()
	instances := slices.Clone(m.instances)
	m.mu.RUnlock()
	for _, inst := range instances {
		inst.stop()
	}
}

package extension

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"

	"os"
	"path/filepath"
	"reflect"

	"strings"
	"sync/atomic"
	"testing"
	"time"

	"home-control/internal/store"
)

// credentialsFile is a device list as a tool writes it, indented, with a key per device, long enough to hold a chunk.
func credentialsFile() string {
	var devices []map[string]string
	for n := range 12 {
		devices = append(devices, map[string]string{
			"name": fmt.Sprintf("Lamp %d", n), "id": fmt.Sprintf("device-id-%020d", n), "key": fmt.Sprintf("local-key-%06d", n), "sn": fmt.Sprintf("serial-%010d", n),
		})
	}
	data, _ := json.MarshalIndent(devices, "", "    ")
	return string(data)
}

func withFile(text string) func(*Manager) {
	return func(m *Manager) {
		os.MkdirAll(filepath.Join(m.configDir, "secrets"), 0o755)
		os.WriteFile(filepath.Join(m.configDir, "secrets/d.json"), []byte(text), 0o600)
	}
}

var fileGiven = map[string]any{"devices": "secrets/d.json"}

func TestAFileCantLeaveThroughOutput(t *testing.T) {
	f := `ctx.settings.devices`
	for name, body := range map[string]string{
		"whole, as a value":        `return {stolen:` + f + `}`,
		"whole, as the result":     `return ` + f,
		"trimmed":                  `return {s:` + f + `.trim()}`,
		"parsed and returned":      `return JSON.parse(` + f + `)`,
		"parsed and serialised":    `return {s:JSON.stringify(JSON.parse(` + f + `))}`,
		"escaped twice":            `return JSON.stringify(JSON.stringify(` + f + `))`,
		"a long slice":             `return {s:` + f + `.slice(7, 1200)}`,
		"a long slice, serialised": `return {s:JSON.stringify(JSON.parse(` + f + `).slice(1, 8))}`,
	} {
		t.Run(name, func(t *testing.T) {
			// No secretFields: a hostile author leaves them out, and this has to hold anyway.
			m := run(t, folder(t, strings.Replace(fileExtension(body), `secretFields:["key"]`, "", 1)), fileGiven, withFile(credentialsFile()))
			failsWith(t, m, "secret settings")
			if d, _, _ := m.plugins.Get("a"); d != nil {
				t.Fatalf("published %.200s", d)
			}
		})
	}
	t.Run("a declared field", func(t *testing.T) {
		failsWith(t, run(t, folder(t, fileExtension(`return JSON.parse(`+f+`).map(d => d.key)`)), fileGiven, withFile(credentialsFile())), "secret settings")
	})
	t.Run("as a device's name", func(t *testing.T) {
		m := run(t, folder(t, `defineExtension({id:"a",name:"a",version:"1",settings:{devices:{label:"D",type:"file",required:true}},
			devices:{poll(c){return [{id:"d",name:c.settings.devices,values:[]}]}}});`), fileGiven, withFile(credentialsFile()))
		failsWith(t, m, "secret settings")
		if len(m.Devices()) != 0 {
			t.Fatal("published a device")
		}
	})
}

// What an extension legitimately takes from its file has to keep working, and what it says about undeclared fields has to be true.
func TestAFileStillGivesUpWhatItsDevicesPublish(t *testing.T) {
	m := run(t, folder(t, fileExtension(`return JSON.parse(ctx.settings.devices).map(d => ({id:d.id,name:d.name,sn:d.sn}))`)), fileGiven, withFile(credentialsFile()))
	eventually(t, "the data", func() bool { d, _, _ := m.plugins.Get("a"); return strings.Contains(string(d), "Lamp 11") })
}

func TestAFileCantLeaveInARequest(t *testing.T) {
	address, hits := counted(t)
	source := func(body string) string {
		return `defineExtension({id:"a",name:"a",version:"1",access:{network:["{host}"]},settings:{host:{label:"H",type:"string",required:true},devices:{label:"D",type:"file",required:true}},
			data:{fetch(c){const f = c.settings.devices; c.fetch("http://"+c.settings.host+"/",{method:"POST",body:` + body + `});return 1}}});`
	}
	for name, body := range map[string]string{
		"escaped":     `JSON.stringify({f:f})`,
		"re-encoded":  `JSON.stringify({f:JSON.parse(f)})`,
		"a slice":     `JSON.stringify({f:f.slice(3, 1000)})`,
		"not JSON":    `"x=" + JSON.stringify(f) + "&y=1"`,
		"as it is":    `f`,
		"in a string": `"before " + f`,
	} {
		t.Run(name, func(t *testing.T) {
			hits.Store(0)
			m := run(t, folder(t, source(body)), map[string]any{"host": address, "devices": "secrets/d.json"}, allowLoopback, withFile(credentialsFile()))
			expectViolation(t, m, "secret")
			if hits.Load() != 0 {
				t.Fatal("it reached the host")
			}
		})
	}
}

func capturedLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var out bytes.Buffer
	before := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&out, nil)))
	t.Cleanup(func() { slog.SetDefault(before) })
	return &out
}

func TestEscapedSecretsStayOutOfLogsAndErrors(t *testing.T) {
	const tricky = `pa"ss\word-123`
	source := `defineExtension({id:"a",name:"a",version:"1",settings:{key:{label:"K",type:"secret",required:true},devices:{label:"D",type:"file",required:true,secretFields:["key"]}},
		data:{fetch(c){
			c.log("said " + JSON.stringify(c.settings.key));
			c.log("said " + JSON.stringify({k:{k:JSON.stringify(c.settings.key)}}));
			c.log("said " + JSON.stringify(c.settings.devices));
			throw new Error("bad " + JSON.stringify(c.settings.devices));
		}}});`
	logged := capturedLog(t)
	file := credentialsFile()
	m := run(t, folder(t, source), map[string]any{"key": tricky, "devices": "secrets/d.json"}, withFile(file))
	eventually(t, "the error", func() bool { return status(m).Error != "" })
	stopAll(m)
	escaped, _ := json.Marshal(tricky)
	text := logged.String() + status(m).Error
	for _, leak := range []string{"local-key-", "device-id-", string(escaped), `ss\\`} {
		if strings.Contains(text, leak) {
			t.Errorf("%q leaked: %.400s", leak, text)
		}
	}
}

func TestASecretWithQuotesCantBeReturned(t *testing.T) {
	m := run(t, folder(t, `defineExtension({id:"a",name:"a",version:"1",settings:{key:{label:"K",type:"secret",required:true}},data:{fetch(c){return {k:c.settings.key}}}});`), map[string]any{"key": `pa"ss\word-123`})
	failsWith(t, m, "secret settings")
}

func TestAWebhookCantAnswerWithAFile(t *testing.T) {
	for name, body := range map[string]string{
		"parsed":  `JSON.parse(c.settings.devices)`,
		"escaped": `{x:JSON.stringify(c.settings.devices)}`,
		"a slice": `{x:c.settings.devices.slice(5, 1200)}`,
	} {
		t.Run(name, func(t *testing.T) {
			m := run(t, folder(t, `defineExtension({id:"hook",name:"Hook",version:"1",access:{webhooks:true},settings:{devices:{label:"D",type:"file",required:true}},
				webhooks:{receive(c){return {body:`+body+`}}}});`), fileGiven, withFile(credentialsFile()))
			eventually(t, "it to run", func() bool { return status(m).Running })
			path, err := m.NewWebhookToken(context.Background(), status(m).Source)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := send(m, path, WebhookRequest{Method: "POST", Path: "/"})
			if err == nil && strings.Contains(resp.Body, "local-key-") {
				t.Fatalf("answered %.200s", resp.Body)
			}
		})
	}
}

type leakyError struct{ Config struct{ Password string } }

func (leakyError) Error() string {
	return "failed to connect to postgres://home:hunter22@db.internal:5432"
}

type brokenStorage struct{ store.Store }

func leaky() error {
	var e leakyError
	e.Config.Password = "hunter22"
	return e
}

func (brokenStorage) SetExtensionValue(context.Context, string, string, json.RawMessage) error {
	return leaky()
}
func (brokenStorage) DeleteExtensionValue(context.Context, string, string) error { return leaky() }

func TestAStorageFailureGivesTheVMNothing(t *testing.T) {
	logged := capturedLog(t)
	m := run(t, folder(t, `defineExtension({id:"a",name:"a",version:"1",access:{storage:true},data:{fetch(c){
		const out = [];
		for (const attempt of [() => c.storage.set("k", 1), () => c.storage.delete("k")]) {
			try { attempt(); } catch (e) { out.push({value: e.value === undefined ? "none" : "there", password: e.value && e.value.Config && e.value.Config.Password, message: String(e.message)}); }
		}
		return out;
	}}});`), nil, func(m *Manager) { m.store = brokenStorage{m.store} })
	eventually(t, "the result", func() bool { d, _, _ := m.plugins.Get("a"); return d != nil })
	d, _, _ := m.plugins.Get("a")
	if text := string(d); strings.Contains(text, "hunter22") || strings.Contains(text, "db.internal") || strings.Contains(text, "there") || strings.Count(text, `"none"`) != 2 {
		t.Fatalf("got %s", text)
	}
	stopAll(m)
	if !strings.Contains(logged.String(), "db.internal") {
		t.Error("the detail belongs in the log")
	}
}

func TestErrorsCarryNoGoValue(t *testing.T) {
	m := run(t, folder(t, `defineExtension({id:"a",name:"a",version:"1",data:{fetch(c){
		const out = [];
		for (const attempt of [
			() => c.crypto.text("zz"),
			() => c.crypto.hash("nope", "00"),
			() => c.crypto.encrypt({mode:"nope",key:"00",data:"00"}),
			() => c.crypto.random(0),
			() => c.fetch("https://nowhere.example/"),
			() => c.fetch("https://nowhere.example/", {headers: 5}),
			() => c.udp.request("1.2.3.4", 1, "00"),
			() => c.tcp.scan([], 1),
			() => c.storage.get("k"),
			() => c.publish(() => 1),
			() => c.refreshDevices(),
			() => eval("1"),
		]) {
			try { attempt(); out.push("no error"); } catch (e) { out.push(typeof e.value); }
		}
		return out;
	}}});`), nil)
	eventually(t, "the result", func() bool { d, _, _ := m.plugins.Get("a"); return d != nil })
	d, _, _ := m.plugins.Get("a")
	var got []string
	json.Unmarshal(d, &got)
	for n, kind := range got {
		if kind != "undefined" {
			t.Errorf("attempt %d: e.value is %s", n, kind)
		}
	}
}

func TestNoGoErrorOutsideTheHelper(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	total := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		n := strings.Count(string(source), ".NewGoError(")
		total += n
		if n > 0 && file != "guard.go" {
			t.Errorf("%s calls NewGoError, which hands the VM the live Go error as err.value: use goError", file)
		}
	}
	if total != 1 {
		t.Errorf("%d calls to NewGoError, want the one in goError", total)
	}
}

// goja answers a Go function's returned error with its own NewGoError, which no helper can catch.
func TestNoHostFunctionReturnsAnError(t *testing.T) {
	l, err := load(folder(t, `defineExtension({id:"a",name:"a",version:"1",data:{fetch(){return 1}}});`))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newManager(t.TempDir(), t.TempDir(), store.NewMemory(), hub(t))
	api := (&instance{manager: m}).context(ctx, l, map[string]any{})
	errorType := reflect.TypeOf((*error)(nil)).Elem()
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch v := v.(type) {
		case map[string]any:
			for name, item := range v {
				walk(path+"."+name, item)
			}
		default:
			if typ := reflect.TypeOf(v); typ != nil && typ.Kind() == reflect.Func && typ.NumOut() > 0 && typ.Out(typ.NumOut()-1) == errorType {
				t.Errorf("%s returns an error", path)
			}
		}
	}
	walk("ctx", api.Export())
}

// A setting's nested default is plain data the page also reads, so the VM can't be holding the same Go map.
func TestSettingsAreACopyInTheVM(t *testing.T) {
	m := run(t, folder(t, `defineExtension({id:"a",name:"a",version:"1",settings:{d:{label:"D",type:"string",default:{a:"untouched"}}},
		data:{fetch(c){c.settings.d.a = "mutated"; return 1}}});`), nil)
	eventually(t, "it to run", func() bool { d, _, _ := m.plugins.Get("a"); return d != nil })
	if text, _ := json.Marshal(status(m).Settings); strings.Contains(string(text), "mutated") {
		t.Fatalf("the VM wrote to Go's own map: %s", text)
	}
}

// A Go slice's length can be set from the VM, which allocates the memory in one step.
func TestScansReturnPlainValues(t *testing.T) {
	m := run(t, folder(t, `defineExtension({id:"a",name:"a",version:"1",access:{sockets:true,lan:true},data:{fetch(c){
		const r = c.tcp.scan([], 1); r.extra = 1; return r.extra === 1;
	}}});`), nil)
	eventually(t, "the result", func() bool { d, _, _ := m.plugins.Get("a"); return d != nil })
	if d, _, _ := m.plugins.Get("a"); string(d) != "true" {
		t.Fatalf("got %s, so it is a Go slice", d)
	}
}

// listener takes connections and counts the bytes it is sent.
func listener(t *testing.T) (int, *atomic.Int32) {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	got := &atomic.Int32{}
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			buf := make([]byte, 4096)
			conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
			n, _ := conn.Read(buf)
			got.Add(int32(n))
			conn.Close()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port, got
}

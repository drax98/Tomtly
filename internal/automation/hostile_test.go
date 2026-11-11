package automation

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"tomtly/device"
	"tomtly/internal/store"
)

const secretText = "s3cret-tuya-key"

// folder is an automations folder inside a parent holding a credentials file, as the real config folder does.
func folder(t *testing.T, files map[string]string) (*fakeHub, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "tuya.json"), []byte(`{"key":"`+secretText+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "automations")
	for name, text := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return &fakeHub{devices: []device.Device{lamp()}}, dir
}

func saving(t *testing.T, files map[string]string, source string) error {
	t.Helper()
	hub, dir := folder(t, files)
	e := New(hub, dir, t.TempDir(), storeForTest(), timeUTC)
	e.Reload()
	_, err := e.SaveSource("probe", source, "", false, true)
	if err != nil && strings.Contains(err.Error(), secretText) {
		t.Errorf("the error shows the secret: %v", err)
	}
	return err
}

func TestScriptImportsOnlyItsOwnCode(t *testing.T) {
	helpers := map[string]string{
		"lib/helper.ts": `export const answer = 42;`,
		"data.json":     `{"key":"` + secretText + `"}`,
		"notes.txt":     secretText,
		"style.css":     `a{}`,
	}
	allowed := map[string]string{
		"a helper":          `import { answer } from "./lib/helper"; home.log(String(answer));`,
		"a helper with .ts": `import { answer } from "./lib/helper.ts"; home.log(String(answer));`,
	}
	for what, source := range allowed {
		if err := saving(t, helpers, source); err != nil {
			t.Errorf("%s should import: %v", what, err)
		}
	}
	refused := map[string]string{
		"a file above":       `import s from "../tuya.json"; home.log(JSON.stringify(s));`,
		"an absolute path":   `import s from "/etc/passwd"; home.log(String(s));`,
		"json in the folder": `import s from "./data.json"; home.log(JSON.stringify(s));`,
		"json by attribute":  `import s from "./data.json" with { type: "json" }; home.log(JSON.stringify(s));`,
		"text in the folder": `import s from "./notes.txt"; home.log(s);`,
		"css in the folder":  `import "./style.css";`,
		"a system module":    `import { readFileSync } from "fs"; home.log(readFileSync("/etc/passwd", "utf8"));`,
		"a data address":     `import s from "data:text/plain,hello"; home.log(s);`,
		"a web address":      `import s from "https://example.com/x.js"; home.log(String(s));`,
		"a glob above":       `const n = "tuya"; home.log(JSON.stringify(require("../" + n + ".json")));`,
		"a dynamic import":   `import("../tuya.json").then((s) => home.log(JSON.stringify(s)));`,
		"a query suffix":     `import s from "../tuya.json?x"; home.log(JSON.stringify(s));`,
	}
	for what, source := range refused {
		if err := saving(t, helpers, source); err == nil {
			t.Errorf("%s could be imported", what)
		}
	}
}

func TestImportErrorsDontTellWhichFilesExist(t *testing.T) {
	there := saving(t, nil, `import "../tuya.json";`)
	missing := saving(t, nil, `import "../missing.json";`)
	if there == nil || missing == nil {
		t.Fatalf("got %v and %v", there, missing)
	}
	strip := func(err error, name string) string { return strings.ReplaceAll(err.Error(), name, "") }
	if strip(there, "tuya.json") != strip(missing, "missing.json") {
		t.Errorf("a script can test for files outside its folder:\n%v\n%v", there, missing)
	}
}

func TestImportThroughLinkIsRefused(t *testing.T) {
	hub, dir := folder(t, map[string]string{"lib/ok.ts": `export const x = 1;`})
	if err := os.Symlink(filepath.Join(dir, "..", "tuya.json"), filepath.Join(dir, "linked.ts")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(dir), filepath.Join(dir, "up")); err != nil {
		t.Fatal(err)
	}
	e := New(hub, dir, t.TempDir(), storeForTest(), timeUTC)
	e.Reload()
	for _, source := range []string{`import s from "./linked.ts"; home.log(JSON.stringify(s));`, `import s from "./up/tuya.json"; home.log(JSON.stringify(s));`} {
		if _, err := e.SaveSource("probe", source, "", false, true); err == nil {
			t.Errorf("a link led out of the folder: %s", source)
		}
	}
}

func TestTsconfigCantRedirectImports(t *testing.T) {
	files := map[string]string{
		"helper.ts":     `export const answer = 42;`,
		"tsconfig.json": `{"compilerOptions":{"baseUrl":".","paths":{"inside":["./helper.ts"],"outside":["../tuya.json"]}}}`,
	}
	for _, name := range []string{"inside", "outside"} {
		if err := saving(t, files, `import s from "`+name+`"; home.log(JSON.stringify(s));`); err == nil {
			t.Errorf("tsconfig paths resolved %q", name)
		}
	}
}

func TestEveryGlobalIsPlainJavaScript(t *testing.T) {
	e, _ := harness(t, &fakeHub{devices: []device.Device{lamp()}}, `home.log(Object.getOwnPropertyNames(globalThis).join(" "));`)
	standard := " globalThis Object Function Array String Number BigInt RegExp Date Boolean Proxy Reflect Promise Map Set WeakMap WeakSet Symbol Error TypeError ReferenceError SyntaxError RangeError EvalError URIError GoError AggregateError eval Math JSON ArrayBuffer DataView Uint8Array Uint8ClampedArray Uint16Array Uint32Array Int8Array Int16Array Int32Array Float32Array Float64Array BigInt64Array BigUint64Array NaN Infinity undefined isNaN isFinite parseFloat parseInt encodeURI encodeURIComponent decodeURI decodeURIComponent escape unescape home WeakRef FinalizationRegistry Iterator SharedArrayBuffer Atomics "
	found := false
	for _, ev := range e.Status().Events {
		if ev.Kind != "log" {
			continue
		}
		found = true
		for _, name := range strings.Fields(ev.Message) {
			if !strings.Contains(standard, " "+name+" ") {
				t.Errorf("unexpected global %q", name)
			}
		}
	}
	if !found {
		t.Fatal("script did not run")
	}
}

func TestScriptCantImportOrRequireAtRunTime(t *testing.T) {
	e, _ := harness(t, &fakeHub{devices: []device.Device{lamp()}}, `
		const name = "/etc/" + "passwd";
		const g = globalThis as any;
		let reached = "nothing";
		try { reached = String(g.require(name)); } catch {}
		try { reached = String(new Function("return typeof require + typeof process + typeof console")()); } catch {}
		home.log(reached);`)
	st := e.Status()
	if len(st.Events) == 0 || st.Events[0].Message != "undefinedundefinedundefined" && st.Events[0].Message != "nothing" {
		t.Fatalf("got %+v", st.Events)
	}
}

func TestErrorsDontHandOverGoValues(t *testing.T) {
	e, _ := harness(t, &fakeHub{devices: []device.Device{lamp()}}, `
		try { home.device("lamp").get("nope"); } catch (err) { home.log(typeof (err as any).value); }`)
	if got := e.Status().Events[0].Message; got != "undefined" {
		t.Fatalf("a caught error carries a Go value: %q", got)
	}
}

type failingHub struct{ fakeHub }

func (h *failingHub) Write(string, string, any) error {
	return errors.New("dial http://192.168.1.5/?token=" + secretText)
}

func TestWriteFailureDetailStaysOutOfTheScript(t *testing.T) {
	hub := &failingHub{fakeHub{devices: []device.Device{lamp()}}}
	dir := t.TempDir()
	source := `home.reconcile(() => { try { home.device("lamp").set("switch", true); } catch (err) { home.log(String(err)); throw err; } });`
	if err := os.WriteFile(filepath.Join(dir, "test.ts"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	e := New(hub, dir, t.TempDir(), storeForTest(), timeUTC)
	e.Reload()
	if err := e.SetLive("test", true); err != nil {
		t.Fatal(err)
	}
	e.Tick()
	st := e.Status()
	for _, ev := range st.Events {
		if strings.Contains(ev.Message, secretText) {
			t.Errorf("the script or the page saw the adapter's error: %q", ev.Message)
		}
	}
	if !strings.Contains(st.Scripts[0].Error, "Lamp") {
		t.Errorf("the page should still say what failed: %q", st.Scripts[0].Error)
	}
}

type valuePlugins struct{ fakePlugins }

func (f *valuePlugins) Act(string, string, []byte) error {
	return errors.New("POST https://api.example.com/?key=" + secretText)
}

func TestActionFailureDetailStaysOutOfTheScript(t *testing.T) {
	dir := t.TempDir()
	source := `home.reconcile(() => { try { home.plugin("weather").action("alert"); } catch (err) { home.log(String(err)); } });`
	if err := os.WriteFile(filepath.Join(dir, "test.ts"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	e := New(&fakeHub{}, dir, t.TempDir(), storeForTest(), timeUTC)
	e.Plugins = &valuePlugins{}
	e.Reload()
	if err := e.SetLive("test", true); err != nil {
		t.Fatal(err)
	}
	e.Tick()
	for _, ev := range e.Status().Events {
		if strings.Contains(ev.Message, secretText) {
			t.Errorf("the script saw the plugin's error: %q", ev.Message)
		}
	}
}

func TestFileErrorsDontShowPaths(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root can write anywhere")
	}
	hub, dir := folder(t, nil)
	e := New(hub, dir, filepath.Join(dir, "no-sdk"), storeForTest(), timeUTC)
	if _, err := e.SDK(); err == nil || strings.Contains(err.Error(), dir) {
		t.Errorf("the SDK error shows a path: %v", err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	if _, err := e.SaveSource("probe", `home.log("x");`, "", false, true); err == nil || strings.Contains(err.Error(), dir) {
		t.Errorf("the save error shows a path: %v", err)
	}
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ReadSource("probe"); err == nil || strings.Contains(err.Error(), dir) {
		t.Errorf("the read error shows a path: %v", err)
	}
}

func TestScriptCantChangeTheDeviceModel(t *testing.T) {
	d := lamp()
	shared := map[string]any{"a": 1}
	d.DPs = append(d.DPs, device.DP{DP: "9", Code: "shape", Type: "Json", Value: shared})
	e, _ := harness(t, &fakeHub{devices: []device.Device{d}}, `
		const v = home.device("lamp").get("shape") as any;
		if (v) v.a = 2;
		home.log(String(typeof v));`)
	if shared["a"] != 1 {
		t.Fatal("a script changed a device's value in the service's own memory")
	}
	if e.Status().Scripts[0].Error != "" {
		t.Fatalf("got %q", e.Status().Scripts[0].Error)
	}
}

func TestRunawayRecursionIsStopped(t *testing.T) {
	e, _ := harness(t, &fakeHub{devices: []device.Device{lamp()}}, `function f(): any { return [1].map(f); } f();`)
	if e.Status().Scripts[0].Error == "" {
		t.Fatal("the script should fail")
	}
}

func TestLogCantFloodOrBloat(t *testing.T) {
	e, _ := harness(t, &fakeHub{devices: []device.Device{lamp()}}, `
		home.log("bad\u0000byte");
		home.log("x".repeat(1 << 20));
		for (let i = 0; i < 100000; i++) home.log("line " + i);`)
	st := e.Status()
	lines := 0
	for _, ev := range st.Events {
		if strings.ContainsRune(ev.Message, 0) || !utf8.ValidString(ev.Message) {
			t.Errorf("Postgres would refuse this line, and the batch around it: %q", ev.Message)
		}
		if len(ev.Message) > maxMessage+len("...") {
			t.Errorf("a %d byte message was kept", len(ev.Message))
		}
		if strings.HasPrefix(ev.Message, "line ") {
			lines++
		}
	}
	if lines > maxLogsPerMinute {
		t.Errorf("%d log lines in a minute", lines)
	}
}

func TestSaveRouteStaysInTheFolder(t *testing.T) {
	hub, dir := folder(t, map[string]string{"test.ts": `home.log("x");`})
	e := New(hub, dir, t.TempDir(), storeForTest(), timeUTC)
	e.Reload()
	for _, name := range []string{"../tuya", "..", "a/b", "/etc/passwd", ".hidden", "x.d", "x\x00y", ""} {
		if _, err := e.ReadSource(name); err == nil {
			t.Errorf("read %q", name)
		}
		if _, err := e.SaveSource(name, `home.log("x");`, "", false, true); err == nil {
			t.Errorf("saved %q", name)
		}
	}
	if err := os.Symlink(filepath.Join(dir, "..", "tuya.json"), filepath.Join(dir, "linked.ts")); err != nil {
		t.Fatal(err)
	}
	if src, err := e.ReadSource("linked"); err == nil || strings.Contains(src.Source, secretText) {
		t.Error("read through a link")
	}
	if _, err := e.SaveSource("linked", `home.log("x");`, "", false, true); err == nil {
		t.Error("saved through a link")
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "..", "tuya.json")); !strings.Contains(string(data), secretText) {
		t.Error("the credentials file changed")
	}
}

func storeForTest() store.Store { return store.NewMemory() }

var timeUTC = time.UTC

// A Go panic that isn't a script error would take the whole service down, so wrong arguments must stay script errors.
func TestWrongArgumentsDontCrashTheService(t *testing.T) {
	for _, call := range []string{
		`home.device(null as any)`, `home.device(undefined as any)`, `home.device({} as any)`, `home.device(Symbol() as any)`,
		`home.plugin(null as any)`, `home.reconcile(null as any)`, `home.reconcile(1 as any)`, `home.daily(null as any, null as any)`,
		`home.daily("08:30", 1 as any)`, `home.daily("08:30", null as any)`, `home.log(null as any)`, `home.log(Symbol() as any)`, `home.log({ toString() { throw new Error("x"); } } as any)`,
		`home.device("lamp").get(null as any)`, `home.device("lamp").set(null as any, null as any)`, `home.device("lamp").set("switch", {} as any)`,
		`home.device("lamp").set("switch", (() => 1) as any)`, `home.plugin("weather").action(null as any, Symbol() as any)`,
		`home.plugin("weather").action("a", { toJSON() { throw new Error("x"); } })`, `new (home.device as any)("lamp")`,
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "test.ts"), []byte(`try { `+call+` } catch {}`), 0o644); err != nil {
			t.Fatal(err)
		}
		e := New(&fakeHub{devices: []device.Device{lamp()}}, dir, t.TempDir(), storeForTest(), timeUTC)
		e.Plugins = &fakePlugins{}
		e.Reload()
		e.Tick()
	}
}

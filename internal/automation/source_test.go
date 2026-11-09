package automation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"home-control/device"
	"home-control/internal/store"
)

func TestReadAndSaveRoundTrip(t *testing.T) {
	hub := &fakeHub{devices: []device.Device{lamp()}}
	e, _ := harness(t, hub, lampOn)
	src, err := e.ReadSource("test")
	if err != nil || src.Source != lampOn {
		t.Fatalf("%+v %v", src, err)
	}
	next := `const l = home.device("lamp"); home.reconcile(() => l.set("switch", false));`
	saved, err := e.SaveSource("test", next, src.Version, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Version == src.Version {
		t.Error("the version must change with the text")
	}
	if data, _ := os.ReadFile(filepath.Join(e.dir, "test.ts")); string(data) != next {
		t.Errorf("file holds %s", data)
	}
}

func TestSaveRunsTheNewVersion(t *testing.T) {
	hub := &fakeHub{devices: []device.Device{lamp()}}
	e, _ := harness(t, hub, `home.device("lamp");`)
	src, _ := e.ReadSource("test")
	if _, err := e.SaveSource("test", lampOn, src.Version, false, true); err != nil {
		t.Fatal(err)
	}
	e.Tick()
	if st := e.Status(); st.Events[0].Kind != "shadow" {
		t.Errorf("the new version isn't running: %+v", st.Events)
	}
}

func TestBadSaveChangesNothing(t *testing.T) {
	for name, bad := range map[string]string{
		"syntax":         `const x = ;`,
		"throws":         `throw new Error("boom");`,
		"unknown device": `home.device("missing");`,
		"bad schedule":   `home.daily("25:00", () => {});`,
		"endless loop":   `while (true) {}`,
	} {
		t.Run(name, func(t *testing.T) {
			hub := &fakeHub{devices: []device.Device{lamp()}}
			e, _ := harness(t, hub, lampOn)
			if err := e.SetLive("test", true); err != nil {
				t.Fatal(err)
			}
			src, _ := e.ReadSource("test")
			if _, err := e.SaveSource("test", bad, src.Version, false, true); err == nil {
				t.Fatal("a broken script was accepted")
			}
			if data, _ := os.ReadFile(filepath.Join(e.dir, "test.ts")); string(data) != lampOn {
				t.Error("a rejected save changed the file")
			}
			if !e.Status().Scripts[0].Live {
				t.Error("a rejected save changed the mode")
			}
			e.Tick()
			if len(hub.writes) != 1 {
				t.Errorf("the old version must keep running, writes %v", hub.writes)
			}
		})
	}
}

func TestCheckingAScriptTouchesNothing(t *testing.T) {
	hub := &fakeHub{devices: []device.Device{lamp()}}
	e, _ := harness(t, hub, `home.device("lamp");`)
	if err := e.SetLive("test", true); err != nil {
		t.Fatal(err)
	}
	before := len(e.Status().Events)
	if _, err := e.check("test", `home.device("lamp").set("switch", true); home.log("hello");`); err != nil {
		t.Fatal(err)
	}
	if len(hub.writes) != 0 {
		t.Errorf("a check reached a device: %v", hub.writes)
	}
	if got := len(e.Status().Events); got != before {
		t.Error("a check wrote to the activity log")
	}
}

func TestSavingLiveScriptReturnsToShadowUnlessKept(t *testing.T) {
	hub := &fakeHub{devices: []device.Device{lamp()}}
	e, _ := harness(t, hub, lampOn)
	if err := e.SetLive("test", true); err != nil {
		t.Fatal(err)
	}
	src, _ := e.ReadSource("test")
	saved, err := e.SaveSource("test", lampOn+" // edited", src.Version, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if e.Status().Scripts[0].Live {
		t.Fatal("an edit must not stay live by default")
	}
	e.Tick()
	if len(hub.writes) != 0 {
		t.Errorf("shadow mode wrote %v", hub.writes)
	}
	if again := New(hub, e.dir, e.sdkDir, e.store, time.UTC); again.live["test"] {
		t.Error("the mode change must survive a restart")
	}

	if err := e.SetLive("test", true); err != nil {
		t.Fatal(err)
	}
	if _, err := e.SaveSource("test", lampOn+" // again", saved.Version, true, true); err != nil {
		t.Fatal(err)
	}
	if !e.Status().Scripts[0].Live {
		t.Error("keepLive must keep it live")
	}
	e.Tick()
	if len(hub.writes) != 1 {
		t.Errorf("writes %v", hub.writes)
	}
}

func TestKeepLiveDoesNotPromoteAShadowScript(t *testing.T) {
	e, _ := harness(t, &fakeHub{devices: []device.Device{lamp()}}, lampOn)
	src, _ := e.ReadSource("test")
	if _, err := e.SaveSource("test", lampOn+" // edited", src.Version, true, true); err != nil {
		t.Fatal(err)
	}
	if e.Status().Scripts[0].Live {
		t.Error("a shadow script must stay in shadow")
	}
}

func TestStaleSaveIsRefused(t *testing.T) {
	e, _ := harness(t, &fakeHub{devices: []device.Device{lamp()}}, lampOn)
	src, _ := e.ReadSource("test")
	if err := os.WriteFile(filepath.Join(e.dir, "test.ts"), []byte(lampOn+" // from my editor"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := e.SaveSource("test", lampOn, src.Version, false, true); !errors.Is(err, ErrConflict) {
		t.Fatalf("got %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(e.dir, "test.ts")); !strings.Contains(string(data), "from my editor") {
		t.Error("a newer change was overwritten")
	}
}

func TestCreateScript(t *testing.T) {
	e, _ := harness(t, &fakeHub{devices: []device.Device{lamp()}}, lampOn)
	if _, err := e.SaveSource("fresh", lampOn, "", false, true); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range e.Status().Scripts {
		if s.Name == "fresh" {
			found = true
			if s.Live {
				t.Error("a new script must start in shadow mode")
			}
		}
	}
	if !found {
		t.Fatal("the new script isn't running")
	}
	if _, err := e.SaveSource("fresh", lampOn, "", false, true); !errors.Is(err, ErrConflict) {
		t.Errorf("creating over an existing script: got %v", err)
	}
	if _, err := e.SaveSource("missing", lampOn, "some-version", false, true); !errors.Is(err, ErrConflict) {
		t.Errorf("saving a deleted script: got %v", err)
	}
}

func TestNamesCannotLeaveTheFolder(t *testing.T) {
	e, _ := harness(t, &fakeHub{devices: []device.Device{lamp()}}, lampOn)
	outside := filepath.Join(filepath.Dir(e.dir), "escaped.ts")
	for _, name := range []string{"../escaped", "a/b", "..", "", ".hidden", "home.d", `a\b`, "x\x00y", strings.Repeat("a", 65)} {
		if _, err := e.SaveSource(name, lampOn, "", false, true); err == nil {
			t.Errorf("accepted name %q", name)
		}
		if _, err := e.ReadSource(name); err == nil {
			t.Errorf("read name %q", name)
		}
	}
	if _, err := os.Stat(outside); err == nil {
		t.Fatal("a save escaped the scripts folder")
	}
}

func TestLinkIsNotReadAsAScript(t *testing.T) {
	e, _ := harness(t, &fakeHub{devices: []device.Device{lamp()}}, lampOn)
	secret := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(secret, []byte("hunter2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(e.dir, "linked.ts")); err != nil {
		t.Fatal(err)
	}
	if src, err := e.ReadSource("linked"); err == nil {
		t.Fatalf("read through a link: %+v", src)
	}
}

// TestScriptsCannotImportSecrets is what makes the editor safe: a script's log is readable through the API.
func TestScriptsCannotImportSecrets(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "automations")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(root, "secrets.json")
	if err := os.WriteFile(secret, []byte(`{"key":"hunter2"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "helper.ts"), []byte(`export const x = 1;`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "shared"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "shared", "util.ts"), []byte(`export const y = 2;`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, "link.json")); err != nil {
		t.Fatal(err)
	}
	hub := &fakeHub{devices: []device.Device{lamp()}}
	e := New(hub, dir, filepath.Join("..", "..", "sdk"), store.NewMemory(), time.UTC)

	for name, source := range map[string]string{
		"parent folder":  `import s from "../secrets.json"; home.log(JSON.stringify(s));`,
		"absolute path":  `import s from "` + secret + `"; home.log(JSON.stringify(s));`,
		"through a link": `import s from "./link.json"; home.log(JSON.stringify(s));`,
	} {
		if _, err := e.SaveSource("evil", source, "", false, true); err == nil {
			t.Errorf("%s: a script imported a secret", name)
		}
	}
	for name, source := range map[string]string{
		"helper":   `import { y } from "./shared/util"; home.log(String(y));`,
		"same dir": `import { x } from "./helper"; home.log(String(x));`,
	} {
		if _, err := e.check("fine", source); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestFailedScriptIsRetried(t *testing.T) {
	// A device from an HTTP adapter only appears after its first poll, so its script must recover by itself.
	hub := &fakeHub{}
	e, clock := harness(t, hub, lampOn)
	if e.Status().Scripts[0].Error == "" {
		t.Fatal("expected a load error with no devices yet")
	}
	hub.devices = []device.Device{lamp()}
	*clock = clock.Add(setupRetry + time.Second)
	e.Tick()
	if err := e.Status().Scripts[0].Error; err != "" {
		t.Fatalf("still failing: %s", err)
	}
	e.Tick()
	if st := e.Status(); st.Events[0].Kind != "shadow" {
		t.Errorf("the recovered script isn't running: %+v", st.Events[0])
	}
}

func TestSDKListsTypes(t *testing.T) {
	e := New(&fakeHub{}, t.TempDir(), filepath.Join("..", "..", "sdk"), store.NewMemory(), time.UTC)
	files, err := e.SDK()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(files["home.d.ts"], "interface Home") {
		t.Errorf("got %d files", len(files))
	}
}

// Someone who may edit a script but not approve access can't reuse what was approved for the old text.
func TestAnEditByAnEditorGoesBackForReview(t *testing.T) {
	hub := &fakeHub{devices: []device.Device{lamp()}}
	prepare := func(t *testing.T) (*Engine, string) {
		e, _ := harness(t, hub, lampOn)
		for _, a := range e.Status().Scripts[0].Access {
			if err := e.SetAccess("test", []string{a.Key}, true); err != nil {
				t.Fatal(err)
			}
		}
		if err := e.SetLive("test", true); err != nil {
			t.Fatal(err)
		}
		src, _ := e.ReadSource("test")
		return e, src.Version
	}
	approved := func(e *Engine) bool {
		for _, a := range e.Status().Scripts[0].Access {
			if a.Approved {
				return true
			}
		}
		return false
	}
	t.Run("an editor's change", func(t *testing.T) {
		e, version := prepare(t)
		if !approved(e) {
			t.Fatal("nothing was approved to begin with")
		}
		if _, err := e.SaveSource("test", lampOn+" // changed", version, true, false); err != nil {
			t.Fatal(err)
		}
		if e.Status().Scripts[0].Live {
			t.Error("keepLive left it live")
		}
		if approved(e) {
			t.Error("the new code kept the old approvals")
		}
		grants, _ := e.store.ScriptGrants(context.Background())
		if len(grants["test"]) != 0 {
			t.Errorf("still saved: %v", grants)
		}
	})
	t.Run("saving the same text", func(t *testing.T) {
		e, version := prepare(t)
		if _, err := e.SaveSource("test", lampOn, version, true, false); err != nil {
			t.Fatal(err)
		}
		if !approved(e) || !e.Status().Scripts[0].Live {
			t.Error("nothing changed, so nothing needs review")
		}
	})
	t.Run("someone who may approve", func(t *testing.T) {
		e, version := prepare(t)
		if _, err := e.SaveSource("test", lampOn+" // changed", version, true, true); err != nil {
			t.Fatal(err)
		}
		if !approved(e) || !e.Status().Scripts[0].Live {
			t.Error("they are the review")
		}
	})
}

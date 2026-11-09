package extension

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"home-control/internal/store"
)

func savedSettings(t *testing.T, m *Manager, source string) map[string]any {
	t.Helper()
	added, err := m.store.AddedExtensions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range added {
		if a.Source == source {
			return a.Settings
		}
	}
	t.Fatalf("%s is not saved", source)
	return nil
}

func TestEditSettings(t *testing.T) {
	const mine = "./extensions/mine"
	ctx := context.Background()
	for name, c := range map[string]struct {
		given     map[string]any
		want      map[string]any
		wantError string
	}{
		"a blank secret keeps its value":     {given: map[string]any{"key": "", "note": "n2"}, want: map[string]any{"key": "the-secret", "note": "n2", "on": false}},
		"a left-out secret keeps its value":  {given: map[string]any{"note": "n2"}, want: map[string]any{"key": "the-secret", "note": "n2", "on": false}},
		"a new secret replaces it":           {given: map[string]any{"key": "other"}, want: map[string]any{"key": "other", "note": "n1", "on": false}},
		"a blank setting is unset":           {given: map[string]any{"note": ""}, want: map[string]any{"key": "the-secret", "on": false}},
		"a left-out setting keeps its value": {given: map[string]any{"on": true}, want: map[string]any{"key": "the-secret", "note": "n1", "on": true}},
		"an unknown setting is refused":      {given: map[string]any{"bogus": "x"}, wantError: `unknown setting "bogus"`},
		"the wrong type is refused":          {given: map[string]any{"note": 5.0}, wantError: "must be a string"},
	} {
		t.Run(name, func(t *testing.T) {
			m := pageManager(t)
			if _, err := addSeen(m, ctx, mine, map[string]any{"key": "the-secret", "note": "n1", "on": false}); err != nil {
				t.Fatal(err)
			}
			eventually(t, "it to run", func() bool { return status(m).Running })
			before := m.bySource(mine)
			got, err := m.UpdateSettings(ctx, mine, c.given)
			if c.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantError) {
					t.Fatalf("got %v", err)
				}
				if m.bySource(mine) != before {
					t.Error("a refused edit restarted it")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			saved := savedSettings(t, m, mine)
			if len(saved) != len(c.want) {
				t.Fatalf("saved %v, want %v", saved, c.want)
			}
			for key, want := range c.want {
				if saved[key] != want {
					t.Errorf("%s = %v, want %v", key, saved[key], want)
				}
			}
			if m.bySource(mine) == before {
				t.Error("it wasn't restarted")
			}
			eventually(t, "it to run again", func() bool { return status(m).Running })
			if len(m.Statuses()) != 1 {
				t.Errorf("%d extensions", len(m.Statuses()))
			}
			data, _ := json.Marshal([]any{got, m.Statuses()})
			for _, secret := range []string{"the-secret", "other"} {
				if strings.Contains(string(data), secret) {
					t.Errorf("the page is shown %s: %s", secret, data)
				}
			}
			if want, ok := c.want["note"].(string); ok && !strings.Contains(string(data), `"value":"`+want+`"`) {
				t.Errorf("the page isn't told the note, so it can't offer it for editing: %s", data)
			}
		})
	}
}

func TestEditingAConfigExtensionIsRefused(t *testing.T) {
	m := pageManager(t)
	source := m.configDir + "/extensions/mine"
	m.add(m.ctx, entry{Source: source, Settings: map[string]any{"key": "x"}}, false)
	if _, err := m.UpdateSettings(context.Background(), source, map[string]any{"note": "n"}); err == nil || !strings.Contains(err.Error(), "edit the file") {
		t.Fatalf("got %v", err)
	}
	if _, err := replaceSeen(m, context.Background(), source, "./extensions/mine", nil); err == nil || !strings.Contains(err.Error(), "edit the file") {
		t.Fatalf("got %v", err)
	}
	if _, err := m.UpdateSettings(context.Background(), "./extensions/nobody", nil); err == nil {
		t.Fatal("an unknown source was edited")
	}
}

const (
	versionOne = `defineExtension({id:"keep",name:"Keep",version:"1",access:{storage:true},settings:{key:{label:"K",type:"secret",required:true}},
		data:{fetch(c){ c.storage.set("seen", (c.storage.get("seen") || 0) + 1); return {v:1,key:c.settings.key.length,seen:c.storage.get("seen")}; }}});`
	versionTwo = `defineExtension({id:"keep",name:"Keep",version:"2",access:{storage:true},settings:{key:{label:"K",type:"secret",required:true}},
		data:{fetch(c){ return {v:2,key:c.settings.key.length,seen:c.storage.get("seen")}; }}});`
)

func TestReplaceKeepsSettingsAndStorage(t *testing.T) {
	m := pageManager(t)
	write(t, m.configDir, "extensions/v1/"+entryFile, versionOne)
	write(t, m.configDir, "extensions/v2/"+entryFile, versionTwo)
	ctx := context.Background()
	if _, err := addSeen(m, ctx, "./extensions/v1", map[string]any{"key": "12345"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "version 1", func() bool { d, _, _ := m.plugins.Get("keep"); return strings.Contains(string(d), `"v":1`) })
	got, err := replaceSeen(m, ctx, "./extensions/v1", "./extensions/v2", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "2" || got.Source != "./extensions/v2" {
		t.Fatalf("got %+v", got)
	}
	eventually(t, "version 2 with what version 1 saved", func() bool {
		d, _, _ := m.plugins.Get("keep")
		return strings.Contains(string(d), `"v":2`) && strings.Contains(string(d), `"key":5`) && strings.Contains(string(d), `"seen":1`)
	})
	if saved := savedSettings(t, m, "./extensions/v2"); saved["key"] != "12345" {
		t.Errorf("settings %v", saved)
	}
	if added, _ := m.store.AddedExtensions(ctx); len(added) != 1 {
		t.Errorf("%d saved: the old version must not come back after a restart", len(added))
	}
	if len(m.Statuses()) != 1 || !status(m).Running {
		t.Errorf("got %+v", m.Statuses())
	}
}

func TestReplaceRefusals(t *testing.T) {
	m := pageManager(t)
	write(t, m.configDir, "extensions/v1/"+entryFile, versionOne)
	write(t, m.configDir, "extensions/other/"+entryFile, strings.Replace(versionTwo, `id:"keep"`, `id:"other"`, 1))
	write(t, m.configDir, "extensions/broken/"+entryFile, `nope(`)
	ctx := context.Background()
	if _, err := addSeen(m, ctx, "./extensions/v1", map[string]any{"key": "12345"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "it to run", func() bool { return status(m).Running })
	for want, newSource := range map[string]string{
		"not \"keep\"":    "./extensions/other",
		"already running": "./extensions/v1",
		"":                "./extensions/broken",
		"must be":         "/etc",
	} {
		t.Run(want, func(t *testing.T) {
			_, err := replaceSeen(m, ctx, "./extensions/v1", newSource, nil)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("got %v", err)
			}
			if s := status(m); s.Source != "./extensions/v1" || !s.Running {
				t.Fatalf("the old one must go on: %+v", s)
			}
		})
	}
}

func TestReplaceWithADifferentIDForgetsTheUpload(t *testing.T) {
	m := pageManager(t)
	ctx := context.Background()
	write(t, m.configDir, "extensions/v1/"+entryFile, versionOne)
	if _, err := addSeen(m, ctx, "./extensions/v1", map[string]any{"key": "12345"}); err != nil {
		t.Fatal(err)
	}
	up, err := m.Upload(ctx, "x.zip", zipOf(t, map[string]string{entryFile: strings.Replace(versionTwo, `id:"keep"`, `id:"stranger"`, 1)}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replaceSeen(m, ctx, "./extensions/v1", up.Source, nil); err == nil {
		t.Fatal("a different id was accepted")
	}
	if stored(t, m, up.Source) {
		t.Error("the refused upload is still stored")
	}
}

func TestReplaceAskingForMoreWaitsForApproval(t *testing.T) {
	approveAll.Store(false)
	t.Cleanup(func() { approveAll.Store(true) })
	m := pageManager(t)
	write(t, m.configDir, "extensions/v1/"+entryFile, versionOne)
	write(t, m.configDir, "extensions/same/"+entryFile, versionTwo) // asks for what version 1 did
	write(t, m.configDir, "extensions/more/"+entryFile, strings.Replace(versionTwo, `access:{storage:true}`, `access:{storage:true,lan:true}`, 1))
	ctx := context.Background()
	if _, err := addSeen(m, ctx, "./extensions/v1", map[string]any{"key": "12345"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "version 1", func() bool { return status(m).Running })

	if _, err := replaceSeen(m, ctx, "./extensions/v1", "./extensions/same", nil); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the same access to need no approval", func() bool { return status(m).Running && status(m).Version == "2" })

	if _, err := replaceSeen(m, ctx, "./extensions/same", "./extensions/more", nil); err != nil {
		t.Fatal(err)
	}
	eventually(t, "it to wait", func() bool { return status(m).NeedsApproval })
	time.Sleep(50 * time.Millisecond)
	if s := status(m); s.Running || s.Source != "./extensions/more" {
		t.Fatalf("got %+v", s)
	}
	if _, err := approveSeen(m, "./extensions/more"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "it to run once approved", func() bool { return status(m).Running })
	if grants, _ := m.store.Grants(ctx, "keep"); !strings.Contains(strings.Join(grants, ","), "lan") {
		t.Errorf("grants %v", grants)
	}
}

func TestApproveShowsTheWebhookAddressOnce(t *testing.T) {
	approveAll.Store(false)
	t.Cleanup(func() { approveAll.Store(true) })
	source := folder(t, `defineExtension({id:"hook",name:"Hook",version:"1",access:{webhooks:true},webhooks:{receive(){ return {body:"ok"}; }}});`)
	m := run(t, source, nil)
	eventually(t, "it to wait", func() bool { return status(m).NeedsApproval })
	if _, err := send(m, "/hooks/hook/guess", WebhookRequest{}); err != ErrNoWebhook {
		t.Fatalf("an address worked before approval: %v", err)
	}
	path, err := approveSeen(m, source)
	if err != nil || !strings.HasPrefix(path, "/hooks/hook/") {
		t.Fatalf("got %q %v", path, err)
	}
	eventually(t, "it to run", func() bool { return status(m).Running })
	if resp, err := send(m, path, WebhookRequest{}); err != nil || resp.Body != "ok" {
		t.Fatalf("got %+v %v", resp, err)
	}
	// Approving it again later (after being stopped) must not make a new address, or show the old one.
	before, _ := m.store.WebhookTokenHash(context.Background(), "hook")
	m.store.SetGrants(context.Background(), "hook", nil)
	inst := m.bySource(source)
	inst.mu.Lock()
	inst.status.NeedsApproval, inst.status.Running = true, false
	inst.mu.Unlock()
	if again, err := approveSeen(m, source); err != nil || again != "" {
		t.Fatalf("got %q %v", again, err)
	}
	if after, _ := m.store.WebhookTokenHash(context.Background(), "hook"); after != before {
		t.Fatal("the address changed")
	}
}

func TestAddingShowsTheWebhookAddressAndRemovingForgetsIt(t *testing.T) {
	m := pageManager(t)
	write(t, m.configDir, "extensions/hook/"+entryFile, `defineExtension({id:"hook",name:"Hook",version:"1",access:{webhooks:true},webhooks:{receive(){ return {body:"ok"}; }}});`)
	ctx := context.Background()
	added, err := addSeen(m, ctx, "./extensions/hook", nil)
	if err != nil || added.Webhook == "" {
		t.Fatalf("got %+v %v", added, err)
	}
	eventually(t, "it to run", func() bool { return status(m).Running })
	if resp, err := send(m, added.Webhook, WebhookRequest{}); err != nil || resp.Body != "ok" {
		t.Fatalf("got %+v %v", resp, err)
	}
	if err := m.Remove("./extensions/hook"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.store.WebhookTokenHash(ctx, "hook"); err == nil || err != store.ErrNotFound {
		t.Fatalf("the token outlived the extension: %v", err)
	}
	if data, _ := json.Marshal(m.Statuses()); strings.Contains(string(data), added.Webhook) {
		t.Fatal("the address is in a status")
	}
}

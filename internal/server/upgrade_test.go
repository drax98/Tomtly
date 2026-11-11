package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tomtly/internal/extension"
	"tomtly/plugin"
)

// Through the routes and the real manager: an update's secrets reach nobody, and the audit holds no values.
func TestUpdateRoutesNeverShowASecret(t *testing.T) {
	const secret, typed = "hunter2-the-secret", "the-new-token-value"
	config := t.TempDir()
	put := func(name, text string) {
		path := filepath.Join(config, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	code := func(version, settings string) string {
		return `defineExtension({id:"upg",name:"Upg",version:"` + version + `",settings:{` + settings + `},data:{fetch(){return {ok:true}}}});`
	}
	const token = `token:{label:"Token",type:"secret",required:true}`
	put("extensions/one/extension.ts", code("1.0.0", token))
	put("extensions/two/extension.ts", code("1.1.0", token+`,pat:{label:"PAT",type:"secret",required:true},city:{label:"City",type:"string"}`))
	put("extensions/two/migrations/1.1.0.json", `{"version":"1.1.0","note":"Needs a PAT","ask":["token","pat"]}`)

	g := newRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m, err := extension.Start(ctx, nil, config, t.TempDir(), g.st, plugin.NewHub())
	if err != nil {
		t.Fatal(err)
	}
	g.a.extensions = m
	g.h = g.a.handler()
	root := g.admin()
	shown, err := m.Inspect(ctx, "./extensions/one")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Add(ctx, "./extensions/one", map[string]any{"token": secret}, shown.Access); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); !m.Statuses()[0].Running; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("it didn't start: %+v", m.Statuses()[0])
		}
	}

	bodies := []string{}
	call := func(method, path, body string, code int) string {
		t.Helper()
		w := g.do(opts{cookie: root}, method, path, body)
		g.want(w, code)
		bodies = append(bodies, w.Body.String())
		return w.Body.String()
	}
	inspected := call("POST", "/api/extensions/inspect", `{"source":"./extensions/two"}`, 200)
	if !strings.Contains(inspected, `"upgrade"`) || !strings.Contains(inspected, "Needs a PAT") {
		t.Fatalf("no plan: %s", inspected)
	}
	var found extension.Status
	if err := json.Unmarshal([]byte(inspected), &found); err != nil {
		t.Fatal(err)
	}
	access, _ := json.Marshal(found.Access)
	refused := call("POST", "/api/extensions/replace", `{"source":"./extensions/one","newSource":"./extensions/two","access":`+string(access)+`,"settings":{"city":"`+typed+`","pat":"`+typed+`"}}`, 400)
	if !strings.Contains(refused, `\"city\"`) {
		t.Errorf("refusal: %s", refused)
	}
	missing := call("POST", "/api/extensions/replace", `{"source":"./extensions/one","newSource":"./extensions/two","access":`+string(access)+`}`, 400)
	if !strings.Contains(missing, "PAT") {
		t.Errorf("refusal: %s", missing)
	}
	call("POST", "/api/extensions/replace", `{"source":"./extensions/one","newSource":"./extensions/two","access":`+string(access)+`,"settings":{"pat":"`+typed+`","token":""}}`, 200)
	call("GET", "/api/extensions", "", 200)
	call("GET", "/api/integrations", "", 200)
	for _, e := range g.audit() {
		bodies = append(bodies, e.Detail)
	}
	if !g.audited("extension.replace", "./extensions/one -> ./extensions/two") {
		t.Error("the update wasn't audited")
	}
	for _, body := range bodies {
		if strings.Contains(body, secret) || strings.Contains(body, typed) {
			t.Errorf("a secret came back: %s", body)
		}
	}
	g.user("v", "viewer")
	g.want(g.do(opts{cookie: g.login("v")}, "POST", "/api/extensions/replace", `{"source":"x","newSource":"y"}`), 403)
}

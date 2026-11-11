package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"tomtly/internal/auth"
)

func TestTokens(t *testing.T) {
	g := newRig(t)
	root := g.admin()
	g.user("pat", "operator")
	pat := g.login("pat")

	g.want(g.do(opts{cookie: pat}, "POST", "/api/tokens", `{"name":"too much","role":"admin"}`), 403)
	g.want(g.do(opts{cookie: pat}, "POST", "/api/tokens", `{"name":"","role":"viewer"}`), 400)
	g.want(g.do(opts{cookie: pat}, "POST", "/api/tokens", `{"name":"x","role":"nope"}`), 400)
	w := g.do(opts{cookie: pat}, "POST", "/api/tokens", `{"name":"phone","role":"operator"}`)
	g.want(w, 201)
	var made struct{ ID, Token, Role string }
	json.Unmarshal(w.Body.Bytes(), &made)
	if !strings.HasPrefix(made.Token, "hct_") || len(made.Token) < 40 {
		t.Fatalf("token = %q", made.Token)
	}
	if !strings.Contains(w.Body.String(), `"owner":"pat"`) {
		t.Errorf("response: %s", w.Body)
	}
	viewerTok := g.do(opts{cookie: pat}, "POST", "/api/tokens", `{"name":"read only","role":"viewer"}`)
	var ro struct{ Token string }
	json.Unmarshal(viewerTok.Body.Bytes(), &ro)

	bearer := opts{bearer: made.Token}
	g.want(g.do(bearer, "GET", "/api/devices", ""), 200)
	g.want(g.do(bearer, "POST", "/api/devices/a/dps/1", `{"value":true}`), 202)
	g.want(g.do(opts{bearer: ro.Token}, "POST", "/api/devices/a/dps/1", `{"value":true}`), 403)
	g.want(g.do(bearer, "POST", "/api/automations/reload", `{}`), 403)
	g.want(g.do(bearer, "GET", "/api/tokens", ""), 403) // a token can't manage tokens, or it could mint more
	g.want(g.do(opts{bearer: "hct_wrong"}, "GET", "/api/devices", ""), 401)
	g.want(g.do(opts{bearer: "wrong", cookie: pat}, "GET", "/api/devices", ""), 401) // a bad token isn't rescued by a cookie
	var status statusBody
	json.Unmarshal(g.do(bearer, "GET", "/api/auth/status", "").Body.Bytes(), &status)
	if status.User == nil || status.User.Username != "pat" {
		t.Errorf("status with a token: %+v", status)
	}

	// Listing: your own; the admin sees everyone's.
	w = g.do(opts{cookie: pat}, "GET", "/api/tokens", "")
	var mine []tokenRecord
	json.Unmarshal(w.Body.Bytes(), &mine)
	if len(mine) != 2 || mine[0].LastUsed == nil {
		t.Errorf("own tokens: %s", w.Body)
	}
	g.want(g.do(opts{cookie: root}, "POST", "/api/tokens", `{"name":"root's","role":"admin"}`), 201)
	var all []tokenRecord
	json.Unmarshal(g.do(opts{cookie: root}, "GET", "/api/tokens", "").Body.Bytes(), &all)
	json.Unmarshal(g.do(opts{cookie: pat}, "GET", "/api/tokens", "").Body.Bytes(), &mine)
	if len(all) != 3 || len(mine) != 2 {
		t.Errorf("admin sees %d, pat sees %d", len(all), len(mine))
	}
	var rootTok string
	for _, tk := range all {
		if tk.Owner == "root" {
			rootTok = tk.ID
		}
	}
	g.want(g.do(opts{cookie: pat}, "DELETE", "/api/tokens/"+rootTok, ""), 404)

	// The owner losing access takes the token's with it: the intersection.
	g.want(g.do(opts{cookie: root}, "PATCH", "/api/users/id-pat", `{"role":"viewer"}`), 200)
	g.want(g.do(bearer, "GET", "/api/devices", ""), 200)
	g.want(g.do(bearer, "POST", "/api/devices/a/dps/1", `{"value":true}`), 403)
	g.want(g.do(opts{cookie: root}, "PATCH", "/api/users/id-pat", `{"role":"operator"}`), 200)
	g.want(g.do(bearer, "POST", "/api/devices/a/dps/1", `{"value":true}`), 202) // back to what the token allows
	g.want(g.do(opts{cookie: root}, "PATCH", "/api/users/id-pat", `{"disabled":true}`), 200)
	g.want(g.do(bearer, "GET", "/api/devices", ""), 401)
	g.want(g.do(opts{cookie: root}, "PATCH", "/api/users/id-pat", `{"disabled":false}`), 200)
	g.want(g.do(bearer, "GET", "/api/devices", ""), 200)

	g.want(g.do(opts{cookie: root}, "DELETE", "/api/tokens/"+made.ID, ""), 200) // an admin removes anyone's
	g.want(g.do(bearer, "GET", "/api/devices", ""), 401)
	if !g.audited("token.create", "phone") || !g.audited("token.delete", made.ID) {
		t.Error("token changes weren't audited")
	}
}

func TestCookieFlags(t *testing.T) {
	g := newRig(t)
	g.admin()
	g.user("pat", "viewer")
	login := func(o opts) *http.Cookie {
		w := g.do(o, "POST", "/api/auth/login", fmt.Sprintf(`{"username":"pat","password":%q}`, strongPassword))
		g.want(w, 200)
		for _, c := range w.Result().Cookies() {
			if c.Name == cookieName {
				return c
			}
		}
		t.Fatal("no cookie")
		return nil
	}
	plain := login(opts{})
	if !plain.HttpOnly || plain.SameSite != http.SameSiteStrictMode || plain.Path != "/" || plain.Secure || plain.MaxAge != 30*24*3600 {
		t.Errorf("plain: %+v", plain)
	}
	if c := login(opts{tls: true}); !c.Secure {
		t.Error("TLS must set Secure")
	}
	if c := login(opts{host: "home.example", header: http.Header{"X-Forwarded-Proto": {"https"}}}); !c.Secure {
		t.Error("a listed host behind an https proxy must set Secure")
	}
	if c := login(opts{header: http.Header{"X-Forwarded-Proto": {"https"}}}); c.Secure {
		t.Error("X-Forwarded-Proto must not be believed for a host that isn't in allowedHosts")
	}
}

func TestOriginGuardStillApplies(t *testing.T) {
	g := newRig(t)
	cookie := g.admin()
	g.want(g.do(opts{host: "evil.example"}, "POST", "/api/auth/login", `{"username":"root","password":"x"}`), 403)
	g.want(g.do(opts{host: "evil.example", cookie: cookie}, "GET", "/api/devices", ""), 403)
	g.want(g.do(opts{origin: "http://evil.example"}, "POST", "/api/auth/login", `{"username":"root","password":"x"}`), 403)
	g.want(g.do(opts{cookie: cookie, origin: "http://evil.example"}, "POST", "/api/devices/a/dps/1", `{"value":1}`), 403)
	r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"root","password":"x"}`))
	r.Host = "localhost:8099"
	r.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	g.h.ServeHTTP(w, r)
	g.want(w, 415)
	huge := `{"username":"root","password":"` + strings.Repeat("x", 5000) + `"}`
	g.want(g.do(opts{}, "POST", "/api/auth/login", huge), 413)
}

func TestNoSecretsInResponsesOrAudit(t *testing.T) {
	g := newRig(t)
	root := g.admin()
	w := g.do(opts{cookie: root}, "POST", "/api/users", `{"username":"pat","password":"pats own secret phrase","role":"viewer"}`)
	g.want(w, 201)
	var created userRecord
	json.Unmarshal(w.Body.Bytes(), &created)
	pat := sessionCookie(g.do(opts{}, "POST", "/api/auth/login", `{"username":"pat","password":"pats own secret phrase"}`))
	w = g.do(opts{cookie: pat}, "POST", "/api/tokens", `{"name":"t","role":"viewer"}`)
	var made struct{ Token string }
	json.Unmarshal(w.Body.Bytes(), &made)
	g.want(g.do(opts{cookie: root}, "PATCH", "/api/users/"+created.ID, `{"password":"reset by the admin!"}`), 200)
	g.do(opts{}, "POST", "/api/auth/login", `{"username":"pat","password":"guessing a password"}`)
	g.do(opts{cookie: root}, "POST", "/api/devices/a/dps/1", `{"value":"private value"}`)

	secrets := []string{"pats own secret phrase", "reset by the admin!", "guessing a password", strongPassword, made.Token, root, pat, auth.HashSecret(root), auth.HashSecret(pat), "$argon2", "private value"}
	user, _ := g.st.UserByName(context.Background(), "pat")
	secrets = append(secrets, user.Hash)
	var bodies []string
	for _, path := range []string{"/api/users", "/api/tokens", "/api/audit?limit=1000", "/api/auth/status"} {
		bodies = append(bodies, g.do(opts{cookie: root}, "GET", path, "").Body.String())
	}
	for _, e := range g.audit() {
		bodies = append(bodies, fmt.Sprint(e))
	}
	for _, body := range bodies {
		for _, s := range secrets {
			if strings.Contains(body, s) {
				t.Errorf("%q leaked in %.200s", s, body)
			}
		}
		if strings.Contains(strings.ToLower(body), `"hash"`) || strings.Contains(strings.ToLower(body), `"password"`) {
			t.Errorf("a secret field in %.200s", body)
		}
	}
	// The audit endpoint keeps its shape and order.
	var entries []map[string]string
	json.Unmarshal(g.do(opts{cookie: root}, "GET", "/api/audit?limit=2", "").Body.Bytes(), &entries)
	if len(entries) != 2 || entries[0]["action"] != "device.write" || entries[0]["detail"] != "a 1" || entries[0]["user"] != "root" || entries[0]["time"] == "" {
		t.Errorf("audit: %v", entries)
	}
	g.want(g.do(opts{cookie: root}, "GET", "/api/audit?limit=0", ""), 400)
}

func TestOnlyAnAdminApprovesScriptAccess(t *testing.T) {
	g := newRig(t)
	root := g.admin()
	body := `{"keys":["device.write:lamp"],"approved":true}`
	for _, role := range []string{"viewer", "operator", "editor"} {
		g.user(role, role)
		g.want(g.do(opts{cookie: g.login(role)}, "POST", "/api/automations/heating/access", body), 403)
	}
	if g.audited("script.access", "heating") {
		t.Error("a refused approval was audited as done")
	}
	g.want(g.do(opts{cookie: root}, "POST", "/api/automations/heating/access", `{"keys":["x"]}`), 400)
	g.want(g.do(opts{cookie: root}, "POST", "/api/automations/unknown/access", body), 400)
	g.want(g.do(opts{cookie: root}, "POST", "/api/automations/heating/access", body), 200)
	if !g.audited("script.access", "heating device.write:lamp approved=true") {
		t.Error("the approval wasn't audited")
	}
}

func TestOnlyAnAdminRemovesDevicesOrScans(t *testing.T) {
	g := newRig(t)
	root := g.admin()
	for _, role := range []string{"viewer", "operator", "editor"} {
		g.user(role, role)
		cookie := g.login(role)
		g.want(g.do(opts{cookie: cookie}, "DELETE", "/api/devices/fake:lamp", ""), 403)
		g.want(g.do(opts{cookie: cookie}, "POST", "/api/devices/fake:lamp/restore", ""), 403)
		g.want(g.do(opts{cookie: cookie}, "POST", "/api/integrations/fake/discover", ""), 403)
	}
	g.want(g.do(opts{cookie: root}, "DELETE", "/api/devices/fake:other", ""), 404)
	g.want(g.do(opts{cookie: root}, "DELETE", "/api/devices/fake:lamp", ""), 200)
	g.want(g.do(opts{cookie: root}, "POST", "/api/devices/fake:lamp/restore", ""), 200)
	g.want(g.do(opts{cookie: root}, "POST", "/api/integrations/fake/discover", ""), 200)
	g.want(g.do(opts{cookie: root}, "POST", "/api/integrations/nope/discover", ""), 404)
	if !g.audited("device.remove", "fake:lamp") || !g.audited("device.restore", "fake:lamp") {
		t.Error("the change wasn't audited")
	}
}

func TestWritesAreAudited(t *testing.T) {
	g := newRig(t)
	root := g.admin()
	g.want(g.do(opts{cookie: root}, "GET", "/api/devices", ""), 200)
	g.want(g.do(opts{cookie: root}, "POST", "/api/devices/a/dps/switch", `{"value":true}`), 202)
	g.want(g.do(opts{cookie: root}, "POST", "/api/automations/heating/source", `{"source":"x","version":"v1"}`), 200)
	g.want(g.do(opts{cookie: root}, "POST", "/api/automations/heating/live", `{"live":true}`), 200)
	g.want(g.do(opts{cookie: root}, "POST", "/api/automations/heating/access", `{"keys":["device.write:tuya:1","device.read:tuya:1"],"approved":true}`), 200)
	g.want(g.do(opts{cookie: root}, "POST", "/api/extensions", `{"source":"github.com/a/b"}`), 200)
	g.want(g.do(opts{cookie: root}, "POST", "/api/extensions/approve", `{"source":"github.com/a/b"}`), 200)
	g.want(g.do(opts{cookie: root}, "DELETE", "/api/extensions?source=github.com/a/b", ""), 200)
	for action, detail := range map[string]string{
		"device.write": "a switch", "script.save": "heating", "script.live": "heating true",
		"script.access": "heating device.read:tuya:1 approved=true",
		"extension.add": "github.com/a/b", "extension.approve": "github.com/a/b", "extension.remove": "github.com/a/b",
	} {
		if !g.audited(action, detail) {
			t.Errorf("%s %q wasn't audited", action, detail)
		}
	}
	for _, e := range g.audit() {
		if e.User == "" || e.IP == "" {
			continue
		}
		if e.Action == "device.write" && e.Detail != "a switch" {
			t.Errorf("device audit: %+v", e)
		}
	}
	if n := len(g.audit()); n != 9 { // setup, the six writes and one row for each of the two approved keys
		t.Errorf("%d audit rows, reads must not add any", n)
	}
}

func TestAuditFailureDoesNotBlock(t *testing.T) {
	g := newRig(t)
	root := g.admin()
	g.a.store = auditDown{g.st}
	g.h = g.a.handler()
	g.want(g.do(opts{cookie: root}, "POST", "/api/devices/a/dps/1", `{"value":true}`), 202)
	g.want(g.do(opts{}, "POST", "/api/auth/login", fmt.Sprintf(`{"username":"root","password":%q}`, strongPassword)), 200)
}

func TestStoreDownFailsClosed(t *testing.T) {
	g := newRig(t)
	root := g.admin()
	g.st.Fail = errors.New("connection refused")
	g.want(g.do(opts{cookie: root}, "GET", "/api/devices", ""), 503)
	g.want(g.do(opts{bearer: "hct_x"}, "GET", "/api/devices", ""), 503)
	g.want(g.do(opts{}, "POST", "/api/auth/login", fmt.Sprintf(`{"username":"root","password":%q}`, strongPassword)), 503)
	g.want(g.do(opts{}, "GET", "/api/auth/status", ""), 503)
	g.want(g.do(opts{}, "GET", "/", ""), 200) // the service itself carries on
	g.st.Fail = nil
	g.want(g.do(opts{cookie: root}, "GET", "/api/devices", ""), 200)
}

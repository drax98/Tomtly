package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"tomtly/device"
	"tomtly/internal/auth"
	"tomtly/internal/store"
)

const strongPassword = "correct horse battery"

// rig is a server over an empty store with a clock the test moves.
type rig struct {
	t      *testing.T
	a      *api
	h      http.Handler
	st     *store.Memory
	now    time.Time
	device *fakeAdapter
}

func newRig(t *testing.T) *rig {
	t.Helper()
	g := &rig{t: t, st: store.NewMemory(), now: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC), device: &fakeAdapter{id: "a"}}
	g.a = newAPI(&Hub{Adapters: []device.Adapter{g.device}}, &fakeEngine{live: map[string]bool{}}, fakeExtensions{}, g.st, t.TempDir(), nil, []string{"home.example"})
	g.a.now = func() time.Time { return g.now }
	g.a.limiter.Now = g.a.now
	g.h = g.a.handler()
	return g
}

type opts struct {
	cookie, bearer, host, origin, remote string
	tls                                  bool
	header                               http.Header
}

func (g *rig) do(o opts, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Host, r.RemoteAddr = "localhost:8099", "127.0.0.1:5000"
	if o.remote != "" {
		r.RemoteAddr = o.remote
	}
	if o.host != "" {
		r.Host = o.host
	}
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if o.cookie != "" {
		r.AddCookie(&http.Cookie{Name: cookieName, Value: o.cookie})
	}
	if o.bearer != "" {
		r.Header.Set("Authorization", "Bearer "+o.bearer)
	}
	if o.origin != "" {
		r.Header.Set("Origin", o.origin)
	}
	if o.tls {
		r.TLS = &tls.ConnectionState{}
	}
	for k, v := range o.header {
		r.Header[k] = v
	}
	w := httptest.NewRecorder()
	g.h.ServeHTTP(w, r)
	return w
}

func sessionCookie(w *httptest.ResponseRecorder) string {
	for _, c := range w.Result().Cookies() {
		if c.Name == cookieName && c.MaxAge >= 0 {
			return c.Value
		}
	}
	return ""
}

func (g *rig) want(w *httptest.ResponseRecorder, code int) {
	g.t.Helper()
	if w.Code != code {
		g.t.Fatalf("got %d %s, want %d", w.Code, w.Body, code)
	}
}

// admin sets the service up and returns the admin's cookie.
func (g *rig) admin() string {
	g.t.Helper()
	w := g.do(opts{}, "POST", "/api/auth/setup", `{"username":"root","password":"`+strongPassword+`"}`)
	g.want(w, 200)
	return sessionCookie(w)
}

// user adds a user directly and returns their id.
func (g *rig) user(name, role string) string {
	g.t.Helper()
	hash, err := auth.Hash(strongPassword)
	if err != nil {
		g.t.Fatal(err)
	}
	u := store.User{ID: "id-" + name, Username: name, Hash: hash, Role: role, Created: g.now}
	if err := g.st.CreateUser(context.Background(), u); err != nil {
		g.t.Fatal(err)
	}
	return u.ID
}

// login returns the cookie of a user made by user.
func (g *rig) login(name string) string {
	g.t.Helper()
	w := g.do(opts{}, "POST", "/api/auth/login", fmt.Sprintf(`{"username":%q,"password":%q}`, name, strongPassword))
	g.want(w, 200)
	return sessionCookie(w)
}

func (g *rig) audit() []store.AuditEntry {
	entries, err := g.st.RecentAudit(context.Background(), 1000)
	if err != nil {
		g.t.Fatal(err)
	}
	return entries
}

func (g *rig) audited(action, detail string) bool {
	return slices.ContainsFunc(g.audit(), func(e store.AuditEntry) bool { return e.Action == action && strings.Contains(e.Detail, detail) })
}

func TestSetupOnlyOnce(t *testing.T) {
	g := newRig(t)
	var status statusBody
	w := g.do(opts{}, "GET", "/api/auth/status", "")
	g.want(w, 200)
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil || !status.SetupRequired || status.User != nil || len(status.Permissions) != 0 {
		t.Fatalf("status before setup: %s", w.Body)
	}
	g.want(g.do(opts{}, "GET", "/api/devices", ""), 401) // nothing is open while nobody is set up
	g.want(g.do(opts{}, "POST", "/api/auth/setup", `{"username":"root","password":"short"}`), 400)
	g.want(g.do(opts{}, "POST", "/api/auth/setup", `{"username":"bad name","password":"`+strongPassword+`"}`), 400)
	cookie := g.admin()
	if cookie == "" {
		t.Fatal("setup didn't log in")
	}
	g.want(g.do(opts{cookie: cookie}, "GET", "/api/devices", ""), 200)
	g.want(g.do(opts{}, "POST", "/api/auth/setup", `{"username":"intruder","password":"`+strongPassword+`"}`), 403)
	w = g.do(opts{cookie: cookie}, "GET", "/api/auth/status", "")
	status = statusBody{}
	if json.Unmarshal(w.Body.Bytes(), &status); status.SetupRequired || status.User == nil || status.User.Username != "root" || status.User.Role != "admin" || !slices.Equal(status.Permissions, auth.Permissions("admin")) {
		t.Fatalf("status after setup: %s", w.Body)
	}
	if !g.audited("setup", "") {
		t.Error("setup wasn't audited")
	}
}

func TestSetupConcurrent(t *testing.T) {
	g := newRig(t)
	var wg sync.WaitGroup
	codes := make(chan int, 12)
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- g.do(opts{}, "POST", "/api/auth/setup", fmt.Sprintf(`{"username":"user%d","password":%q}`, i, strongPassword)).Code
		}()
	}
	wg.Wait()
	close(codes)
	won := 0
	for c := range codes {
		if c == 200 {
			won++
		} else if c != 403 {
			t.Errorf("a setup got %d", c)
		}
	}
	if users, _ := g.st.ListUsers(context.Background()); won != 1 || len(users) != 1 {
		t.Fatalf("%d setups won, %d users", won, len(users))
	}
}

func TestLoginLogoutAndExpiry(t *testing.T) {
	g := newRig(t)
	g.want(g.do(opts{}, "POST", "/api/auth/setup", `{"username":"Root","password":"`+strongPassword+`"}`), 200)

	wrong := g.do(opts{}, "POST", "/api/auth/login", `{"username":"root","password":"not the password"}`)
	unknown := g.do(opts{}, "POST", "/api/auth/login", `{"username":"nobody","password":"not the password"}`)
	g.want(wrong, 401)
	g.want(unknown, 401)
	if wrong.Body.String() != unknown.Body.String() || sessionCookie(wrong) != "" {
		t.Errorf("a wrong password and an unknown user must look the same: %s / %s", wrong.Body, unknown.Body)
	}
	if !g.audited("login.failure", "") {
		t.Error("failed login wasn't audited")
	}

	cookie := g.login("ROOT") // names ignore case
	g.want(g.do(opts{cookie: cookie}, "GET", "/api/devices", ""), 200)
	g.want(g.do(opts{cookie: cookie}, "POST", "/api/auth/logout", `{}`), 200)
	g.want(g.do(opts{cookie: cookie}, "GET", "/api/devices", ""), 401)
	if !g.audited("login.success", "") || !g.audited("logout", "") {
		t.Error("login or logout wasn't audited")
	}

	// Seven idle days end a session; using it keeps it going until thirty days from login.
	cookie = g.login("root")
	g.now = g.now.Add(6 * 24 * time.Hour)
	g.want(g.do(opts{cookie: cookie}, "GET", "/api/devices", ""), 200)
	g.now = g.now.Add(6 * 24 * time.Hour)
	g.want(g.do(opts{cookie: cookie}, "GET", "/api/devices", ""), 200)
	g.now = g.now.Add(8 * 24 * time.Hour)
	g.want(g.do(opts{cookie: cookie}, "GET", "/api/devices", ""), 401)
	cookie = g.login("root")
	for range 4 {
		g.now = g.now.Add(6 * 24 * time.Hour)
		g.want(g.do(opts{cookie: cookie}, "GET", "/api/devices", ""), 200)
	}
	g.now = g.now.Add(6 * 24 * time.Hour) // 30 days since login, though never idle
	g.want(g.do(opts{cookie: cookie}, "GET", "/api/devices", ""), 401)
}

func TestLoginRateLimit(t *testing.T) {
	g := newRig(t)
	g.admin()
	bad := func(name string) int {
		return g.do(opts{}, "POST", "/api/auth/login", fmt.Sprintf(`{"username":%q,"password":"wrong wrong wrong"}`, name)).Code
	}
	for range 5 {
		if c := bad("root"); c != 401 {
			t.Fatalf("got %d", c)
		}
	}
	w := g.do(opts{}, "POST", "/api/auth/login", fmt.Sprintf(`{"username":"root","password":%q}`, strongPassword))
	g.want(w, 429) // the right password doesn't get through while backing off
	if w.Header().Get("Retry-After") == "" {
		t.Error("no Retry-After")
	}
	g.now = g.now.Add(time.Minute)
	g.want(g.do(opts{}, "POST", "/api/auth/login", fmt.Sprintf(`{"username":"root","password":%q}`, strongPassword)), 200)

	// The address is limited too, across names, and a forwarded address is ignored from a non-loopback caller.
	for i := range 5 {
		bad(fmt.Sprint("name", i))
	}
	forged := http.Header{"X-Forwarded-For": {"10.9.9.9"}}
	g.want(g.do(opts{header: forged}, "POST", "/api/auth/login", `{"username":"another","password":"wrong wrong wrong"}`), 429)
}

func TestForwardedAddressTrust(t *testing.T) {
	g := newRig(t)
	g.a.trustedProxies, _ = auth.ParseProxies("10.0.0.1, 127.0.0.1")
	ip := func(remote string, forwarded ...string) string {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		for _, f := range forwarded {
			r.Header.Add("X-Forwarded-For", f)
		}
		return g.a.clientIP(r)
	}
	for _, c := range []struct {
		remote    string
		forwarded []string
		want      string
	}{
		{"10.0.0.1:5000", []string{"203.0.113.7"}, "203.0.113.7"}, // a proxy on another machine or container
		{"127.0.0.1:5000", []string{"6.6.6.6, 203.0.113.7"}, "203.0.113.7"},
		{"10.0.0.1:5000", []string{"6.6.6.6", "203.0.113.7"}, "203.0.113.7"}, // a second header line is not missed
		{"10.0.0.1:5000", []string{"203.0.113.7, 127.0.0.1"}, "203.0.113.7"}, // past each trusted proxy
		{"192.168.1.9:5000", []string{"203.0.113.7"}, "192.168.1.9"},         // peer isn't trusted
		{"127.0.0.1:5000", []string{"garbage"}, "127.0.0.1"},
	} {
		if got := ip(c.remote, c.forwarded...); got != c.want {
			t.Errorf("%+v: got %s", c, got)
		}
	}
	g.a.trustedProxies = nil
	if got := ip("127.0.0.1:5000", "203.0.113.7"); got != "127.0.0.1" {
		t.Errorf("loopback isn't trusted by itself any more: %s", got)
	}
}

func TestRoutesNeedAPermission(t *testing.T) {
	g := newRig(t)
	valid := append([]string{public, session}, auth.Permissions("admin")...)
	seen := map[string]bool{}
	for _, rt := range g.a.routes() {
		if !slices.Contains(valid, rt.perm) {
			t.Errorf("%s has permission %q", rt.pattern, rt.perm)
		}
		if seen[rt.pattern] {
			t.Errorf("%s is listed twice", rt.pattern)
		}
		seen[rt.pattern] = true
	}
	// What the spec promises, so a loosened table shows up as a failure here.
	want := map[string]string{
		"GET /api/devices": "devices.read", "GET /api/rooms": "devices.read", "GET /api/integrations": "devices.read",
		"POST /api/devices/{id}/dps/{dp}": "devices.write",
		"GET /api/automations":            "automations.read", "GET /api/automations/sdk": "automations.read", "GET /api/automations/{name}/source": "automations.read",
		"POST /api/automations/{name}/source": "automations.edit", "POST /api/automations/reload": "automations.edit",
		"POST /api/automations/{name}/live": "automations.live", "POST /api/automations/{name}/access": "automations.approve",
		"GET /api/extensions": "extensions.read",
		"GET /api/widgets":    "devices.read", "PUT /api/dashboard": "devices.write", "POST /api/widgets/{owner}/{key}/actions/{action}": "devices.write",
		"GET /api/pins": "devices.read", "PUT /api/devices/{id}/pins": "devices.write", "PATCH /api/devices/{id}": "extensions.manage", "POST /api/rooms": "extensions.manage", "PATCH /api/rooms/{name}": "extensions.manage", "DELETE /api/rooms/{name}": "extensions.manage", "GET /api/marketplace": "extensions.read", "POST /api/marketplace/inspect": "extensions.manage",
		"GET /api/marketplace/{id}/assets/{name}": "extensions.read", "GET /api/marketplace/{id}/reviews": "extensions.read", "POST /api/marketplace/{id}/reviews": "extensions.manage",
		"POST /api/extensions/inspect": "extensions.manage", "POST /api/extensions/upload": "extensions.manage", "POST /api/extensions": "extensions.manage",
		"POST /api/extensions/approve": "extensions.manage", "DELETE /api/extensions": "extensions.manage",
		"POST /api/extensions/settings": "extensions.manage", "POST /api/extensions/replace": "extensions.manage", "POST /api/extensions/webhook-token": "extensions.manage",
		"POST /hooks/{id}/{token}": public, "GET /hooks/{id}/{token}": public, "PUT /hooks/{id}/{token}": public, "POST /hooks/{id}/{token}/{rest...}": public,
		"GET /api/users": "users.manage", "POST /api/users": "users.manage", "PATCH /api/users/{id}": "users.manage", "DELETE /api/users/{id}": "users.manage",
		"GET /api/audit": "audit.read", "GET /api/settings": "devices.read", "PUT /api/settings": "settings.manage",
		"GET /api/auth/status": public, "POST /api/auth/setup": public, "POST /api/auth/login": public,
	}
	for pattern, perm := range want {
		if g.a.perms[pattern] != perm {
			t.Errorf("%s: got %q, want %q", pattern, g.a.perms[pattern], perm)
		}
	}
	for _, rt := range g.a.routes() {
		if strings.HasPrefix(rt.pattern, "POST /api/auth/logout") || strings.Contains(rt.pattern, "/api/tokens") || strings.HasSuffix(rt.pattern, "/password") {
			if rt.perm != session {
				t.Errorf("%s should need a login", rt.pattern)
			}
		}
	}
}

func TestRoleMatrix(t *testing.T) {
	g := newRig(t)
	g.admin()
	placeholder := regexp.MustCompile(`\{[^}]*\}`)
	n := 0
	for _, role := range auth.Roles() {
		for _, rt := range g.a.routes() {
			method, path, _ := strings.Cut(rt.pattern, " ")
			path = placeholder.ReplaceAllString(path, "x")
			n++
			name := fmt.Sprintf("u%d", n)
			g.user(name, role)
			secret, hash := auth.NewSecret("")
			if err := g.st.CreateSession(context.Background(), hash, "id-"+name, g.now); err != nil {
				t.Fatal(err)
			}
			code := g.do(opts{cookie: secret}, method, path, "").Code
			allowed := rt.perm == public || rt.perm == session || auth.Has(role, rt.perm)
			if allowed && (code == 401 || code == 403) {
				t.Errorf("%s %s: refused with %d", role, rt.pattern, code)
			}
			if !allowed && code != 403 {
				t.Errorf("%s %s: got %d, want 403", role, rt.pattern, code)
			}
		}
	}
	// And the 403 says what was missing.
	g.user("v", "viewer")
	secret, hash := auth.NewSecret("")
	_ = g.st.CreateSession(context.Background(), hash, "id-v", g.now)
	if w := g.do(opts{cookie: secret}, "POST", "/api/devices/a/dps/1", `{"value":1}`); w.Code != 403 || !strings.Contains(w.Body.String(), "devices.write") {
		t.Errorf("%d %s", w.Code, w.Body)
	}
}

func TestDenyByDefault(t *testing.T) {
	g := newRig(t)
	cookie := g.admin()
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/nothing"}, {"DELETE", "/api/devices"}, {"GET", "/api/auth/login"}, {"GET", "/api/"}, {"GET", "/api/devices/a/dps/1"},
	} {
		if w := g.do(opts{}, c.method, c.path, ""); w.Code != 401 {
			t.Errorf("%s %s without login: %d", c.method, c.path, w.Code)
		}
		if w := g.do(opts{cookie: cookie}, c.method, c.path, ""); w.Code != 404 && w.Code != 405 {
			t.Errorf("%s %s as admin: %d", c.method, c.path, w.Code)
		}
	}
	if w := g.do(opts{}, "GET", "/", ""); w.Code != 200 {
		t.Errorf("the page itself must stay public, got %d", w.Code)
	}
}

func TestPageAddressesServeThePage(t *testing.T) {
	g := newRig(t)
	if err := os.WriteFile(filepath.Join(g.a.webDir, "index.html"), []byte("the page"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/devices", "/automations/heating.ts", "/integrations/xiaomi", "/tokens"} {
		if w := g.do(opts{}, "GET", path, ""); w.Code != 200 || w.Body.String() != "the page" {
			t.Errorf("%s: %d %q", path, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/nothing", "/devicesx", "/assets/gone.js"} {
		if w := g.do(opts{}, "GET", path, ""); w.Code != 404 {
			t.Errorf("%s must stay missing, got %d", path, w.Code)
		}
	}
	if w := g.do(opts{}, "GET", "/api/devices", ""); w.Code != 401 {
		t.Errorf("the API must keep its login, got %d", w.Code)
	}
}

// auditDown fails only the audit log.
type auditDown struct{ store.Store }

func (auditDown) AppendAudit(context.Context, store.AuditEntry) error { return errors.New("disk full") }

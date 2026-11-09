package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"home-control/device"
	"home-control/internal/auth"
	"home-control/internal/extension"
	"home-control/internal/store"
)

// flaky fails the named store methods and leaves the rest working, so a handler's own store error path can be reached
// after the caller has authenticated.
type flaky struct {
	store.Store
	down map[string]bool
}

func (f *flaky) err(name string) error {
	if f.down[name] {
		return errors.New(name + " is down")
	}
	return nil
}

func (f *flaky) CreateFirstUser(ctx context.Context, u store.User) error {
	if err := f.err("CreateFirstUser"); err != nil {
		return err
	}
	return f.Store.CreateFirstUser(ctx, u)
}
func (f *flaky) CreateUser(ctx context.Context, u store.User) error {
	if err := f.err("CreateUser"); err != nil {
		return err
	}
	return f.Store.CreateUser(ctx, u)
}
func (f *flaky) UserByName(ctx context.Context, n string) (store.User, error) {
	if err := f.err("UserByName"); err != nil {
		return store.User{}, err
	}
	return f.Store.UserByName(ctx, n)
}
func (f *flaky) UserByID(ctx context.Context, id string) (store.User, error) {
	if err := f.err("UserByID"); err != nil {
		return store.User{}, err
	}
	return f.Store.UserByID(ctx, id)
}
func (f *flaky) ListUsers(ctx context.Context) ([]store.User, error) {
	if err := f.err("ListUsers"); err != nil {
		return nil, err
	}
	return f.Store.ListUsers(ctx)
}
func (f *flaky) UpdateUser(ctx context.Context, id string, c store.UserChange) (store.User, error) {
	if err := f.err("UpdateUser"); err != nil {
		return store.User{}, err
	}
	return f.Store.UpdateUser(ctx, id, c)
}
func (f *flaky) DeleteUser(ctx context.Context, id string) error {
	if err := f.err("DeleteUser"); err != nil {
		return err
	}
	return f.Store.DeleteUser(ctx, id)
}
func (f *flaky) CreateSession(ctx context.Context, h, id string, now time.Time) error {
	if err := f.err("CreateSession"); err != nil {
		return err
	}
	return f.Store.CreateSession(ctx, h, id, now)
}
func (f *flaky) DeleteSession(ctx context.Context, h string) error {
	if err := f.err("DeleteSession"); err != nil {
		return err
	}
	return f.Store.DeleteSession(ctx, h)
}
func (f *flaky) DeleteExpiredSessions(ctx context.Context, now time.Time, idle, abs time.Duration) error {
	if err := f.err("DeleteExpiredSessions"); err != nil {
		return err
	}
	return f.Store.DeleteExpiredSessions(ctx, now, idle, abs)
}
func (f *flaky) ListTokens(ctx context.Context, id string) ([]store.Token, error) {
	if err := f.err("ListTokens"); err != nil {
		return nil, err
	}
	return f.Store.ListTokens(ctx, id)
}
func (f *flaky) CreateToken(ctx context.Context, t store.Token, h string) error {
	if err := f.err("CreateToken"); err != nil {
		return err
	}
	return f.Store.CreateToken(ctx, t, h)
}
func (f *flaky) DeleteToken(ctx context.Context, id, user string) error {
	if err := f.err("DeleteToken"); err != nil {
		return err
	}
	return f.Store.DeleteToken(ctx, id, user)
}
func (f *flaky) RecentAudit(ctx context.Context, n int) ([]store.AuditEntry, error) {
	if err := f.err("RecentAudit"); err != nil {
		return nil, err
	}
	return f.Store.RecentAudit(ctx, n)
}

func (g *rig) breakStore(methods ...string) *flaky {
	f := &flaky{Store: g.st, down: map[string]bool{}}
	for _, m := range methods {
		f.down[m] = true
	}
	g.a.store = f
	g.h = g.a.handler()
	return f
}

func login(g *rig, name, password string) *httpResult {
	w := g.do(opts{}, "POST", "/api/auth/login", fmt.Sprintf(`{"username":%q,"password":%q}`, name, password))
	return &httpResult{w.Code, w.Header(), w.Body.String()}
}

type httpResult struct {
	code   int
	header http.Header
	body   string
}

func TestStoreFailureAnswers503NotSuccess(t *testing.T) {
	post := func(path, body string) func(*rig, string) int {
		return func(g *rig, c string) int { return g.do(opts{cookie: c}, "POST", path, body).Code }
	}
	for _, c := range []struct {
		name, method string
		do           func(g *rig, root string) int
	}{
		{"CreateFirstUser", "", nil}, // set up below: it needs an empty store
		{"ListUsers", "ListUsers", func(g *rig, root string) int { return g.do(opts{cookie: root}, "GET", "/api/users", "").Code }},
		{"CreateUser", "CreateUser", post("/api/users", `{"username":"new","password":"`+strongPassword+`","role":"viewer"}`)},
		{"UpdateUser", "UpdateUser", func(g *rig, root string) int {
			return g.do(opts{cookie: root}, "PATCH", "/api/users/id-pat", `{"role":"viewer"}`).Code
		}},
		{"DeleteUser", "DeleteUser", func(g *rig, root string) int {
			return g.do(opts{cookie: root}, "DELETE", "/api/users/id-pat", "").Code
		}},
		{"UserByID", "UserByID", func(g *rig, root string) int {
			return g.do(opts{cookie: root}, "DELETE", "/api/users/id-pat", "").Code
		}},
		{"ListTokens", "ListTokens", func(g *rig, root string) int { return g.do(opts{cookie: root}, "GET", "/api/tokens", "").Code }},
		{"CreateToken", "CreateToken", post("/api/tokens", `{"name":"t","role":"viewer"}`)},
		{"DeleteToken", "DeleteToken", func(g *rig, root string) int {
			return g.do(opts{cookie: root}, "DELETE", "/api/tokens/x", "").Code
		}},
		{"RecentAudit", "RecentAudit", func(g *rig, root string) int { return g.do(opts{cookie: root}, "GET", "/api/audit", "").Code }},
		{"DeleteSession", "DeleteSession", post("/api/auth/logout", `{}`)},
		{"ChangePassword", "UpdateUser", post("/api/auth/password", `{"current":"`+strongPassword+`","new":"a brand new password"}`)},
		{"Login", "UserByName", func(g *rig, _ string) int { return login(g, "pat", strongPassword).code }},
		{"LoginSession", "CreateSession", func(g *rig, _ string) int { return login(g, "pat", strongPassword).code }},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := newRig(t)
			if c.do == nil {
				g.breakStore("CreateFirstUser")
				g.want(g.do(opts{}, "POST", "/api/auth/setup", `{"username":"root","password":"`+strongPassword+`"}`), 503)
				g.breakStore("CreateSession")
				g.want(g.do(opts{}, "POST", "/api/auth/setup", `{"username":"root","password":"`+strongPassword+`"}`), 503)
				return
			}
			root := g.admin()
			g.user("pat", "admin") // a second admin, so the changes themselves are allowed
			g.breakStore(c.method)
			if code := c.do(g, root); code != 503 {
				t.Errorf("got %d, want 503", code)
			}
		})
	}
}

func TestSetupListUsersDown(t *testing.T) {
	g := newRig(t)
	g.breakStore("ListUsers")
	g.want(g.do(opts{}, "POST", "/api/auth/setup", `{"username":"root","password":"`+strongPassword+`"}`), 503)
}

func TestExpiredSessionCleanupFailureDoesNotBlockLogin(t *testing.T) {
	g := newRig(t)
	g.admin()
	g.breakStore("DeleteExpiredSessions")
	if r := login(g, "root", strongPassword); r.code != 200 {
		t.Errorf("login = %d %s", r.code, r.body)
	}
}

func TestRetryAfterCountsWholeSeconds(t *testing.T) {
	g := newRig(t)
	g.admin()
	for range 5 {
		login(g, "root", "wrong wrong wrong")
	}
	r := login(g, "root", strongPassword)
	if r.code != 429 || r.header.Get("Retry-After") != "31" {
		t.Errorf("got %d Retry-After=%q, want 429 and 31 (30s lock, rounded up)", r.code, r.header.Get("Retry-After"))
	}
}

func TestLogoutClearsTheCookie(t *testing.T) {
	g := newRig(t)
	root := g.admin()
	w := g.do(opts{cookie: root}, "POST", "/api/auth/logout", `{}`)
	g.want(w, 200)
	for _, c := range w.Result().Cookies() {
		if c.Name == cookieName {
			if c.MaxAge != -1 || c.Value != "" || !c.HttpOnly {
				t.Errorf("logout cookie = %+v", c)
			}
			return
		}
	}
	t.Error("logout sent no cookie to clear the old one")
}

func TestPasswordLengthEdges(t *testing.T) {
	long := strings.Repeat("p", auth.MaxPassword)
	g := newRig(t)
	g.admin()
	g.user("pat", "operator")
	cookie := g.login("pat")
	g.want(g.do(opts{cookie: cookie}, "POST", "/api/auth/password", fmt.Sprintf(`{"current":%q,"new":%q}`, strongPassword, long)), 200)
	// A password of exactly the maximum still signs in and can be given as the current one.
	if r := login(g, "pat", long); r.code != 200 {
		t.Fatalf("login with a %d-byte password = %d", len(long), r.code)
	}
	g.want(g.do(opts{cookie: cookie}, "POST", "/api/auth/password", fmt.Sprintf(`{"current":%q,"new":"another new password"}`, long)), 200)
	// One byte over is refused outright.
	if r := login(g, "pat", long+"p"); r.code != 401 {
		t.Errorf("login with an oversized password = %d", r.code)
	}
	g.want(g.do(opts{cookie: cookie}, "POST", "/api/auth/password", fmt.Sprintf(`{"current":%q,"new":"x"}`, "another new password"+strings.Repeat("p", 300))), 403)
}

func TestUpdateUserRefusesAnUnknownRole(t *testing.T) {
	g := newRig(t)
	root := g.admin()
	g.user("pat", "operator")
	w := g.do(opts{cookie: root}, "PATCH", "/api/users/id-pat", `{"role":"owner"}`)
	g.want(w, 400)
	if !strings.Contains(w.Body.String(), "viewer, operator, editor, admin") {
		t.Errorf("the refusal doesn't list the roles: %s", w.Body)
	}
	g.want(g.do(opts{cookie: root}, "PATCH", "/api/users/id-pat", `{}`), 400)
}

func TestTokenNameLimits(t *testing.T) {
	g := newRig(t)
	root := g.admin()
	create := func(name string) int {
		return g.do(opts{cookie: root}, "POST", "/api/tokens", fmt.Sprintf(`{"name":%q,"role":"viewer"}`, name)).Code
	}
	for name, want := range map[string]int{
		"":                      400,
		"a":                     201,
		strings.Repeat("a", 64): 201,
		strings.Repeat("a", 65): 400,
		strings.Repeat("é", 64): 201, // 64 characters, 128 bytes
		strings.Repeat("é", 65): 400,
	} {
		if got := create(name); got != want {
			t.Errorf("name of %d characters: %d, want %d", len([]rune(name)), got, want)
		}
	}
}

func TestAuditLimit(t *testing.T) {
	g := newRig(t)
	root := g.admin()
	for range 3 {
		login(g, "root", "wrong wrong wrong")
	}
	count := func(q string) int {
		w := g.do(opts{cookie: root}, "GET", "/api/audit"+q, "")
		g.want(w, 200)
		return strings.Count(w.Body.String(), `"action"`)
	}
	if n := count("?limit=1"); n != 1 {
		t.Errorf("limit=1 returned %d", n)
	}
	if n := count(""); n != 4 {
		t.Errorf("default returned %d, want all 4", n)
	}
	g.want(g.do(opts{cookie: root}, "GET", "/api/audit?limit=0", ""), 400)
	g.want(g.do(opts{cookie: root}, "GET", "/api/audit?limit=-5", ""), 400)
	g.want(g.do(opts{cookie: root}, "GET", "/api/audit?limit=x", ""), 400)
}

// A user switched off in the store is refused even when a session or token for them still exists.
func TestDisabledUserWithLiveCredentialsIsRefused(t *testing.T) {
	g := newRig(t)
	g.admin()
	id := g.user("pat", "operator")
	secret, hash := auth.NewSecret("")
	if err := g.st.CreateSession(context.Background(), hash, id, g.now); err != nil {
		t.Fatal(err)
	}
	g.want(g.do(opts{cookie: secret}, "GET", "/api/devices", ""), 200)
	off := true
	if _, err := g.st.UpdateUser(context.Background(), id, store.UserChange{Disabled: &off}); err != nil {
		t.Fatal(err)
	}
	if err := g.st.CreateSession(context.Background(), hash, id, g.now); err != nil {
		t.Fatal(err)
	}
	g.want(g.do(opts{cookie: secret}, "GET", "/api/devices", ""), 401)
	g.want(g.do(opts{cookie: secret}, "GET", "/api/auth/status", ""), 200) // status itself answers, as logged out
}

// integrationsAdapter reports devices that name their adapter.
type integrationsAdapter []device.Device

func (a integrationsAdapter) Devices() []device.Device       { return a }
func (a integrationsAdapter) Write(_, _ string, _ any) error { return nil }

func TestIntegrationsGroupDevicesByProvider(t *testing.T) {
	hub := &Hub{Adapters: []device.Adapter{integrationsAdapter{
		{ID: "d1", Adapter: "tuya", Name: "Lamp", Kind: "light", Online: true},
		{ID: "d2", Adapter: "legacy", Name: "Old"},
		{ID: "d0", Adapter: "alpha", Name: "First"},
	}}}
	got := hub.Integrations([]extension.Status{
		{ID: "tuya", Name: "Tuya", Description: "cloud", Provides: []string{"devices"}},
		{ID: "empty", Name: "Nothing", Provides: []string{"devices"}},
		{ID: "dataonly", Name: "Data", Provides: []string{"data"}},
		{Source: "github.com/x/y", Provides: nil}, // not yet inspected: no id, so it is listed under its source
	})
	if len(got) != 5 {
		t.Fatalf("%d integrations: %+v", len(got), got)
	}
	if got[0].Type != "tuya" || got[0].Name != "Tuya" || got[0].Description != "cloud" || len(got[0].Devices) != 1 || got[0].Devices[0] != (IntegrationItem{"d1", "Lamp", "light", true}) || got[0].Extension == nil || got[0].Extension.ID != "tuya" {
		t.Errorf("tuya: %+v", got[0])
	}
	if got[1].Type != "empty" || got[1].Devices == nil || len(got[1].Devices) != 0 {
		t.Errorf("an extension without devices must list an empty array, not null: %+v", got[1])
	}
	if got[2].Type != "github.com/x/y" || got[2].Name != "github.com/x/y" {
		t.Errorf("an extension without an id: %+v", got[2])
	}
	// What no extension claims follows, by name; a data-only extension does not appear at all.
	if got[3].Type != "alpha" || got[4].Type != "legacy" || got[3].Extension != nil || len(got[4].Devices) != 1 {
		t.Errorf("unclaimed adapters: %+v %+v", got[3], got[4])
	}
	for _, i := range got {
		if i.Type == "dataonly" {
			t.Error("a data-only extension is on the integrations page")
		}
	}
}

type brokenSDK struct{ fakeEngine }

func (brokenSDK) SDK() (map[string]string, error) { return nil, errors.New("sdk unreadable") }

func TestSDKEndpoint(t *testing.T) {
	g := newRig(t)
	root := g.admin()
	w := g.do(opts{cookie: root}, "GET", "/api/automations/sdk", "")
	g.want(w, 200)
	if !strings.Contains(w.Body.String(), `"home.d.ts"`) || !strings.Contains(w.Body.String(), `"generated.d.ts"`) {
		t.Errorf("the SDK answer lacks the engine's files or the generated device types: %.200s", w.Body)
	}
	g.a.engine = &brokenSDK{}
	g.h = g.a.handler()
	w = g.do(opts{cookie: root}, "GET", "/api/automations/sdk", "")
	g.want(w, 500)
	if !strings.Contains(w.Body.String(), "sdk unreadable") {
		t.Errorf("%s", w.Body)
	}
}

// market answers the marketplace and webhook calls with what the test sets.
type market struct {
	fakeExtensions
	err error
}

func (m market) MarketInspect(_ context.Context, id string) (extension.Status, error) {
	return extension.Status{Source: "market:" + id, Version: "1.2.0"}, m.err
}
func (m market) NewWebhookToken(context.Context, string) (string, error) { return "/hooks/x/t", m.err }

func TestMarketplaceInspectAndWebhookTokenAreAuditedOrRefused(t *testing.T) {
	g := newRig(t)
	root := g.admin()
	g.a.extensions = market{}
	g.h = g.a.handler()
	g.want(g.do(opts{cookie: root}, "POST", "/api/marketplace/inspect", `{"id":"tuya"}`), 200)
	if !g.audited("marketplace.inspect", "tuya@1.2.0") {
		t.Errorf("inspect not audited with its id and version: %+v", g.audit())
	}
	w := g.do(opts{cookie: root}, "POST", "/api/extensions/webhook-token", `{"source":"s"}`)
	g.want(w, 200)
	if !strings.Contains(w.Body.String(), "/hooks/x/t") || !g.audited("extension.webhook-token", "s") {
		t.Errorf("webhook token: %s", w.Body)
	}

	g.a.extensions = market{err: errors.New("no such extension")}
	g.h = g.a.handler()
	n := len(g.audit())
	for _, p := range []string{"/api/marketplace/inspect", "/api/extensions/webhook-token"} {
		w := g.do(opts{cookie: root}, "POST", p, `{"id":"x","source":"x"}`)
		g.want(w, 400)
		if !strings.Contains(w.Body.String(), "no such extension") {
			t.Errorf("%s: %s", p, w.Body)
		}
	}
	if len(g.audit()) != n {
		t.Error("a refused call was audited as if it happened")
	}
}

package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"home-control/internal/auth"
)

func loginBody(name, password string) string {
	return fmt.Sprintf(`{"username":%q,"password":%q}`, name, password)
}

// A stranger guessing from the same address can't lock out someone who knows their password.
func TestARightPasswordGetsPastALockedAddress(t *testing.T) {
	g := newRig(t)
	g.admin()
	shared := opts{remote: "198.51.100.1:1"}
	for n := range 5 {
		g.do(shared, "POST", "/api/auth/login", loginBody(fmt.Sprintf("guess%d", n), "wrong wrong wrong"))
	}
	if g.a.limiter.Wait("ip:198.51.100.1") == 0 {
		t.Fatal("the address should be locked by now")
	}
	g.want(g.do(shared, "POST", "/api/auth/login", loginBody("another", "wrong wrong wrong")), 429)
	g.want(g.do(shared, "POST", "/api/auth/login", loginBody("root", strongPassword)), 200)

	// The name's own lock does stop it, even from elsewhere, and even with the right password.
	for range 5 {
		g.want(g.do(opts{remote: "198.51.100.2:1"}, "POST", "/api/auth/login", loginBody("root", "wrong wrong wrong")), 401)
	}
	g.want(g.do(opts{remote: "198.51.100.3:1"}, "POST", "/api/auth/login", loginBody("root", strongPassword)), 429)
}

func TestASuccessDoesNotCountAgainstTheAddress(t *testing.T) {
	g := newRig(t)
	g.admin()
	for range 10 {
		g.want(g.do(opts{}, "POST", "/api/auth/login", loginBody("root", strongPassword)), 200)
	}
	if wait := g.a.limiter.Wait("ip:127.0.0.1"); wait != 0 {
		t.Errorf("locked for %v after only successes", wait)
	}
}

func TestProxiesInTheSettingGiveEachClientItsOwnLimit(t *testing.T) {
	g := newRig(t)
	g.admin()
	g.a.trustedProxies, _ = auth.ParseProxies("10.0.0.0/8")
	through := func(client string) opts {
		return opts{remote: "10.0.0.9:1", header: http.Header{"X-Forwarded-For": {client}}}
	}
	for n := range 5 {
		g.do(through("203.0.113.1"), "POST", "/api/auth/login", loginBody(fmt.Sprintf("guess%d", n), "wrong wrong wrong"))
	}
	g.want(g.do(through("203.0.113.1"), "POST", "/api/auth/login", loginBody("other", "wrong wrong wrong")), 429)
	g.want(g.do(through("203.0.113.2"), "POST", "/api/auth/login", loginBody("other", "wrong wrong wrong")), 401)
}

func TestParallelLoginsCantBeatTheLimit(t *testing.T) {
	g := newRig(t)
	g.admin()
	var mu sync.Mutex
	codes := map[int]int{}
	var wg sync.WaitGroup
	for range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := g.do(opts{remote: "198.51.100.1:1"}, "POST", "/api/auth/login", loginBody("root", "wrong wrong wrong"))
			mu.Lock()
			codes[w.Code]++
			mu.Unlock()
		}()
	}
	wg.Wait()
	if codes[401] > 5 {
		t.Errorf("%d guesses were checked: %v", codes[401], codes)
	}
}

func TestAFullQueueAnswersBusy(t *testing.T) {
	g := newRig(t)
	g.admin()
	g.a.verify = func(string, string) (bool, error) { return false, auth.ErrBusy }
	g.a.spendTime = func(string) error { return auth.ErrBusy }
	for _, name := range []string{"root", "nobody"} {
		g.want(g.do(opts{}, "POST", "/api/auth/login", loginBody(name, "wrong wrong wrong")), 429)
	}
	if g.a.limiter.Wait("user:root", "ip:127.0.0.1") != 0 {
		t.Error("a request that was turned away counted as a guess")
	}
	for range 10 {
		g.want(g.do(opts{}, "POST", "/api/auth/login", loginBody("root", "wrong wrong wrong")), 429)
	}
	if g.a.limiter.Wait("user:root") != 0 {
		t.Error("nor did ten of them")
	}
	g.a.verify = auth.Verify
	g.want(g.do(opts{}, "POST", "/api/auth/login", loginBody("root", strongPassword)), 200)
}

// An over-long password must cost what any other wrong password does, or it shows which names exist.
func TestAnOverLongPasswordCostsTheSameForAnyName(t *testing.T) {
	g := newRig(t)
	g.admin()
	spent := 0
	g.a.spendTime = func(string) error { spent++; return nil }
	long := strings.Repeat("x", auth.MaxPassword+1)
	for _, name := range []string{"root", "nobody"} {
		spent = 0
		g.want(g.do(opts{}, "POST", "/api/auth/login", loginBody(name, long)), 401)
		if spent != 1 {
			t.Errorf("%s: spent time %d times", name, spent)
		}
	}
}

func createToken(g *rig, cookie string) string {
	g.t.Helper()
	w := g.do(opts{cookie: cookie}, "POST", "/api/tokens", `{"name":"t","role":"viewer"}`)
	g.want(w, 201)
	var made struct{ Token string }
	json.Unmarshal(w.Body.Bytes(), &made)
	return made.Token
}

func TestChangingAPasswordEndsTokens(t *testing.T) {
	const next = "another strong password"
	t.Run("an admin's reset", func(t *testing.T) {
		g := newRig(t)
		root := g.admin()
		g.user("ann", "viewer")
		ann := g.login("ann")
		token := createToken(g, ann)
		var id string
		users, _ := g.st.ListUsers(t.Context())
		for _, u := range users {
			if u.Username == "ann" {
				id = u.ID
			}
		}
		g.want(g.do(opts{bearer: token}, "GET", "/api/devices", ""), 200)
		g.want(g.do(opts{cookie: root}, "PATCH", "/api/users/"+id, `{"password":"`+next+`"}`), 200)
		g.want(g.do(opts{bearer: token}, "GET", "/api/devices", ""), 401)
	})
	t.Run("your own, by default", func(t *testing.T) {
		g := newRig(t)
		root := g.admin()
		token := createToken(g, root)
		g.want(g.do(opts{cookie: root}, "POST", "/api/auth/password", `{"current":"`+strongPassword+`","new":"`+next+`"}`), 200)
		g.want(g.do(opts{bearer: token}, "GET", "/api/devices", ""), 401)
	})
	t.Run("your own, keeping them", func(t *testing.T) {
		g := newRig(t)
		root := g.admin()
		token := createToken(g, root)
		g.want(g.do(opts{cookie: root}, "POST", "/api/auth/password", `{"current":"`+strongPassword+`","new":"`+next+`","keepTokens":true}`), 200)
		g.want(g.do(opts{bearer: token}, "GET", "/api/devices", ""), 200)
	})
	t.Run("a wrong current password takes nothing", func(t *testing.T) {
		g := newRig(t)
		root := g.admin()
		token := createToken(g, root)
		g.want(g.do(opts{cookie: root}, "POST", "/api/auth/password", `{"current":"wrong wrong wrong","new":"`+next+`"}`), 403)
		g.want(g.do(opts{bearer: token}, "GET", "/api/devices", ""), 200)
	})
}

func TestAPasswordChangeDoesNotCountAsAGuess(t *testing.T) {
	g := newRig(t)
	root := g.admin()
	for range 10 {
		g.want(g.do(opts{cookie: root}, "POST", "/api/auth/password", `{"current":"`+strongPassword+`","new":"`+strongPassword+`","keepTokens":true}`), 200)
	}
}

func TestFirstRunSetupCode(t *testing.T) {
	setup := func(code string) string {
		return fmt.Sprintf(`{"username":"root","password":%q,"setupToken":%q}`, strongPassword, code)
	}
	status := func(g *rig, o opts) statusBody {
		g.t.Helper()
		w := g.do(o, "GET", "/api/auth/status", "")
		g.want(w, 200)
		var s statusBody
		json.Unmarshal(w.Body.Bytes(), &s)
		return s
	}
	asked := map[string]opts{
		"another host name":          {host: "home.example", remote: "192.168.1.9:1"},
		"an address on the network":  {host: "192.168.1.5:8099", remote: "192.168.1.9:1"},
		"typed Host from far away":   {remote: "203.0.113.9:1"},
		"a proxy passing Host along": {remote: "127.0.0.1:1", header: http.Header{"X-Forwarded-For": {"203.0.113.9"}}},
	}
	for name, o := range asked {
		t.Run(name, func(t *testing.T) {
			g := newRig(t)
			if s := status(g, o); !s.SetupRequired || !s.SetupTokenRequired {
				t.Fatalf("status: %+v", s)
			}
			w := g.do(o, "POST", "/api/auth/setup", setup(""))
			g.want(w, 403)
			if !strings.Contains(w.Body.String(), "logs") || !strings.Contains(w.Body.String(), `"setupTokenRequired":true`) {
				t.Errorf("answer: %s", w.Body)
			}
			g.want(g.do(o, "POST", "/api/auth/setup", setup("wrong")), 403)
			if users, _ := g.st.ListUsers(t.Context()); len(users) != 0 {
				t.Fatal("an account was made")
			}
			g.want(g.do(o, "POST", "/api/auth/setup", setup(g.a.setupToken())), 200)
		})
	}
	for name, o := range map[string]opts{
		"localhost":        {},
		"127.0.0.1":        {host: "127.0.0.1:8099"},
		"through docker":   {remote: "172.17.0.1:1"},
		"an IPv6 loopback": {host: "[::1]:8099", remote: "[::1]:1"},
	} {
		t.Run(name, func(t *testing.T) {
			g := newRig(t)
			if s := status(g, o); !s.SetupRequired || s.SetupTokenRequired {
				t.Fatalf("status: %+v", s)
			}
			g.want(g.do(o, "POST", "/api/auth/setup", setup("")), 200)
		})
	}
	t.Run("once set up nobody is asked", func(t *testing.T) {
		g := newRig(t)
		g.admin()
		if s := status(g, opts{host: "home.example", remote: "192.168.1.9:1"}); s.SetupRequired || s.SetupTokenRequired {
			t.Errorf("status: %+v", s)
		}
		g.want(g.do(opts{host: "home.example", remote: "192.168.1.9:1"}, "POST", "/api/auth/setup", setup(g.a.setupToken())), 403)
	})
}

func TestSecurityHeaders(t *testing.T) {
	g := newRig(t)
	for _, path := range []string{"/", "/devices", "/api/auth/status"} {
		h := g.do(opts{}, "GET", path, "").Header()
		csp := h.Get("Content-Security-Policy")
		for _, want := range []string{"frame-ancestors 'none'", "default-src 'self'", "script-src 'self'"} {
			if !strings.Contains(csp, want) {
				t.Errorf("%s: %q lacks %s", path, csp, want)
			}
		}
		if h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Referrer-Policy") != "no-referrer" {
			t.Errorf("%s: %v", path, h)
		}
	}
	// A webhook's stricter policy stays, whoever answers.
	if csp := g.do(opts{}, "POST", "/hooks/nobody/token", "").Header().Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") {
		t.Errorf("hook: %q", csp)
	}
}

func TestSavingAScriptPassesOnWhoIsAsking(t *testing.T) {
	g := newRig(t)
	engine := g.a.engine.(*fakeEngine)
	root := g.admin()
	g.user("ed", "editor")
	body := `{"source":"new","version":"v1","keepLive":true}`
	g.want(g.do(opts{cookie: g.login("ed")}, "POST", "/api/automations/heating/source", body), 200)
	if engine.mayApprove || !engine.keepLive {
		t.Errorf("an editor: may approve %v, keep live %v", engine.mayApprove, engine.keepLive)
	}
	g.want(g.do(opts{cookie: root}, "POST", "/api/automations/heating/source", body), 200)
	if !engine.mayApprove || !engine.keepLive {
		t.Errorf("an admin: may approve %v, keep live %v", engine.mayApprove, engine.keepLive)
	}
}

type stopRecorder struct {
	fakeExtensions
	stopped string
}

func (s *stopRecorder) StopExtension(source string) error { s.stopped = source; return nil }

func TestStoppingAnExtensionIsForManagersAndAudited(t *testing.T) {
	g := newRig(t)
	ext := &stopRecorder{}
	g.a.extensions = ext
	g.h = g.a.handler()
	root := g.admin()
	g.user("v", "viewer")
	g.want(g.do(opts{cookie: g.login("v")}, "POST", "/api/extensions/stop", `{"source":"market:x"}`), 403)
	g.want(g.do(opts{cookie: root}, "POST", "/api/extensions/stop", `{"source":"market:x"}`), 200)
	if ext.stopped != "market:x" || !g.audited("extension.stop", "market:x") {
		t.Errorf("stopped %q", ext.stopped)
	}
}

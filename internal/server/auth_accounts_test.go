package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
)

func TestLastAdminAndSessionsEnd(t *testing.T) {
	g := newRig(t)
	root := g.admin()
	var me statusBody
	w := g.do(opts{cookie: root}, "GET", "/api/auth/status", "")
	json.Unmarshal(w.Body.Bytes(), &me)
	self := "/api/users/" + me.User.ID
	g.want(g.do(opts{cookie: root}, "PATCH", self, `{"role":"viewer"}`), 409)
	g.want(g.do(opts{cookie: root}, "PATCH", self, `{"disabled":true}`), 409)
	g.want(g.do(opts{cookie: root}, "DELETE", self, ""), 409)
	g.want(g.do(opts{cookie: root}, "GET", "/api/devices", ""), 200)

	// A second admin: both can't be demoted at once.
	w = g.do(opts{cookie: root}, "POST", "/api/users", `{"username":"second","password":"`+strongPassword+`","role":"admin"}`)
	g.want(w, 201)
	var second userRecord
	json.Unmarshal(w.Body.Bytes(), &second)
	secondCookie := g.login("second")
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for _, target := range []string{me.User.ID, second.ID} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- g.do(opts{cookie: root}, "PATCH", "/api/users/"+target, `{"role":"viewer"}`).Code
		}()
	}
	wg.Wait()
	close(codes)
	ok := 0
	for c := range codes {
		if c == 200 {
			ok++
		}
	}
	users, _ := g.st.ListUsers(context.Background())
	admins := 0
	for _, u := range users {
		if u.Role == "admin" && !u.Disabled {
			admins++
		}
	}
	if ok != 1 || admins != 1 {
		t.Fatalf("%d demotions succeeded, %d admins left", ok, admins)
	}
	_ = secondCookie

	// Changes to a user end their sessions at once.
	g2 := newRig(t)
	rootCookie := g2.admin()
	w = g2.do(opts{cookie: rootCookie}, "POST", "/api/users", `{"username":"pat","password":"`+strongPassword+`","role":"editor"}`)
	g2.want(w, 201)
	var pat userRecord
	json.Unmarshal(w.Body.Bytes(), &pat)
	patch := func(body string) {
		cookie := g2.login("pat")
		g2.want(g2.do(opts{cookie: cookie}, "GET", "/api/devices", ""), 200)
		g2.want(g2.do(opts{cookie: rootCookie}, "PATCH", "/api/users/"+pat.ID, body), 200)
		g2.want(g2.do(opts{cookie: cookie}, "GET", "/api/devices", ""), 401)
	}
	patch(`{"role":"operator"}`)
	patch(`{"password":"another long password"}`)
	g2.want(g2.do(opts{cookie: rootCookie}, "PATCH", "/api/users/"+pat.ID, `{"password":"`+strongPassword+`"}`), 200)
	patch(`{"disabled":true}`)
	g2.want(g2.do(opts{}, "POST", "/api/auth/login", fmt.Sprintf(`{"username":"pat","password":%q}`, strongPassword)), 401) // disabled
	g2.want(g2.do(opts{cookie: rootCookie}, "PATCH", "/api/users/"+pat.ID, `{"role":"emperor"}`), 400)
	g2.want(g2.do(opts{cookie: rootCookie}, "PATCH", "/api/users/"+pat.ID, `{}`), 400)
	g2.want(g2.do(opts{cookie: rootCookie}, "PATCH", "/api/users/nope", `{"role":"viewer"}`), 404)
	g2.want(g2.do(opts{cookie: rootCookie}, "POST", "/api/users", `{"username":"PAT","password":"`+strongPassword+`","role":"viewer"}`), 409)
	g2.want(g2.do(opts{cookie: rootCookie}, "DELETE", "/api/users/"+pat.ID, ""), 200)
	g2.want(g2.do(opts{cookie: rootCookie}, "DELETE", "/api/users/"+pat.ID, ""), 404)
	for _, a := range []string{"user.create", "user.update", "user.delete"} {
		if !g2.audited(a, "pat") {
			t.Errorf("%s wasn't audited", a)
		}
	}
}

func TestPasswordChange(t *testing.T) {
	g := newRig(t)
	g.admin()
	g.user("pat", "operator")
	one, two := g.login("pat"), g.login("pat")
	g.want(g.do(opts{cookie: one}, "POST", "/api/auth/password", `{"current":"wrong wrong wrong","new":"a brand new password"}`), 403)
	g.want(g.do(opts{cookie: one}, "POST", "/api/auth/password", `{"current":"`+strongPassword+`","new":"short"}`), 400)
	g.want(g.do(opts{cookie: one}, "POST", "/api/auth/password", `{"current":"`+strongPassword+`","new":"a brand new password"}`), 200)
	g.want(g.do(opts{cookie: one}, "GET", "/api/devices", ""), 200)
	g.want(g.do(opts{cookie: two}, "GET", "/api/devices", ""), 401) // other sessions end
	g.want(g.do(opts{}, "POST", "/api/auth/login", fmt.Sprintf(`{"username":"pat","password":%q}`, strongPassword)), 401)
	g.want(g.do(opts{}, "POST", "/api/auth/login", `{"username":"pat","password":"a brand new password"}`), 200)
	if !g.audited("password.change", "") {
		t.Error("password change wasn't audited")
	}
}

package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"home-control/internal/extension"
)

// hookExt is an extension manager with one webhook address, which records what it was given.
type hookExt struct {
	fakeExtensions
	token   string
	reply   extension.WebhookResponse
	failure error
	got     extension.WebhookRequest
	settled map[string]any
	source  string
}

func (h *hookExt) Webhook(id, token, _ string) (extension.Deliver, error) {
	if id != "hook" || token != h.token {
		return nil, extension.ErrNoWebhook
	}
	return func(_ context.Context, req extension.WebhookRequest) (extension.WebhookResponse, error) {
		h.got = req
		return h.reply, h.failure
	}, nil
}

func (h *hookExt) UpdateSettings(_ context.Context, source string, settings map[string]any) (extension.Status, error) {
	h.source, h.settled = source, settings
	return extension.Status{}, nil
}

func hookRig(t *testing.T, ext *hookExt) *rig {
	g := newRig(t)
	g.a.extensions = ext
	g.h = g.a.handler()
	return g
}

func TestHookRoutesAreOutsideTheLogin(t *testing.T) {
	ext := &hookExt{token: "right", reply: extension.WebhookResponse{Status: 202, ContentType: "text/plain; charset=utf-8", Body: "thanks", Headers: map[string]string{"X-Seen": "1"}}}
	g := hookRig(t, ext)
	for _, c := range []struct {
		name, method, path, host, origin string
		code                             int
	}{
		{"post", "POST", "/hooks/hook/right", "", "", 202},
		{"get", "GET", "/hooks/hook/right", "", "", 202},
		{"put with a path", "PUT", "/hooks/hook/right/a/b", "", "", 202},
		{"a trailing slash", "POST", "/hooks/hook/right/", "", "", 202},
		{"from an outside host name", "POST", "/hooks/hook/right", "mine.example.net", "", 202},
		{"from another website", "POST", "/hooks/hook/right", "mine.example.net", "https://other.example", 202},
		{"delete", "DELETE", "/hooks/hook/right", "", "", 404},
		{"wrong token", "POST", "/hooks/hook/wrong", "", "", 404},
		{"unknown extension", "POST", "/hooks/nobody/right", "", "", 404},
		{"the api is still guarded from outside", "POST", "/api/extensions/approve", "mine.example.net", "", 403},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := g.do(opts{host: c.host, origin: c.origin}, c.method, c.path, "")
			if w.Code != c.code {
				t.Fatalf("got %d %s", w.Code, w.Body)
			}
		})
	}
	a, b := g.do(opts{}, "POST", "/hooks/hook/wrong", ""), g.do(opts{}, "POST", "/hooks/nobody/right", "")
	if a.Body.String() != b.Body.String() || a.Header().Get("Content-Type") != b.Header().Get("Content-Type") {
		t.Errorf("a wrong token and an unknown extension differ: %q %q", a.Body, b.Body)
	}
}

func TestHookRequestAndAnswer(t *testing.T) {
	ext := &hookExt{token: "right", reply: extension.WebhookResponse{Status: 201, ContentType: "application/json", Body: `{"a":1}`, Headers: map[string]string{"X-Seen": "1"}}}
	g := hookRig(t, ext)
	w := g.do(opts{cookie: "a-session", header: http.Header{"X-Sig": {"s1"}}}, "POST", "/hooks/hook/right/deep/er?x=1&x=2&y=3", "payload")
	if w.Code != 201 || w.Body.String() != `{"a":1}` || w.Header().Get("X-Seen") != "1" || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("got %d %s %v", w.Code, w.Body, w.Header())
	}
	got := ext.got
	if got.Method != "POST" || got.Path != "/deep/er" || got.Query["x"] != "1" || got.Query["y"] != "3" || got.Body != "payload" || got.BodyHex != "7061796c6f6164" || got.Headers["x-sig"] != "s1" {
		t.Fatalf("got %+v", got)
	}
	if _, has := got.Headers["cookie"]; has {
		t.Error("the cookie reached the extension")
	}
	if w.Header().Get("Set-Cookie") != "" {
		t.Error("a cookie was set")
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("headers %v", w.Header())
	}
	if g.do(opts{}, "GET", "/hooks/hook/right", "").Code != 201 || ext.got.Path != "/" {
		t.Errorf("a path with nothing after the token is %q", ext.got.Path)
	}
}

func TestHookBodyLimit(t *testing.T) {
	ext := &hookExt{token: "right", reply: extension.WebhookResponse{Status: 200}}
	g := hookRig(t, ext)
	if w := g.do(opts{}, "POST", "/hooks/hook/right", strings.Repeat("x", extension.MaxWebhookBody)); w.Code != 200 || len(ext.got.Body) != extension.MaxWebhookBody {
		t.Fatalf("a 1 MiB body: %d", w.Code)
	}
	ext.got = extension.WebhookRequest{}
	if w := g.do(opts{}, "POST", "/hooks/hook/right", strings.Repeat("x", extension.MaxWebhookBody+1)); w.Code != 413 || ext.got.Body != "" {
		t.Fatalf("a body over 1 MiB: %d", w.Code)
	}
	// Nothing is read before the address is right.
	if w := g.do(opts{}, "POST", "/hooks/hook/wrong", strings.Repeat("x", extension.MaxWebhookBody+1)); w.Code != 404 {
		t.Fatalf("a wrong address with a big body: %d", w.Code)
	}
}

func TestHookFailuresAreNotExplained(t *testing.T) {
	for want, err := range map[int]error{
		429: extension.ErrWebhookLimit,
		503: extension.ErrWebhookBusy,
		504: extension.ErrWebhookTimeout,
		502: errors.New("it broke at /home/me/.ssh with the key hunter2"),
	} {
		ext := &hookExt{token: "right", failure: err}
		w := hookRig(t, ext).do(opts{}, "POST", "/hooks/hook/right", "")
		if w.Code != want || strings.Contains(w.Body.String(), "hunter2") {
			t.Errorf("%v: got %d %s", err, w.Code, w.Body)
		}
	}
}

func TestSettingsEditIsAuditedWithoutValues(t *testing.T) {
	ext := &hookExt{}
	g := hookRig(t, ext)
	root := g.admin()
	w := g.do(opts{cookie: root}, "POST", "/api/extensions/settings", `{"source":"./extensions/mine","settings":{"key":"hunter2-the-secret","note":"private note"}}`)
	g.want(w, 200)
	if ext.source != "./extensions/mine" || ext.settled["key"] != "hunter2-the-secret" {
		t.Fatalf("got %q %v", ext.source, ext.settled)
	}
	var found bool
	for _, e := range g.audit() {
		if e.Action == "extension.settings" {
			found = true
			if e.Detail != "./extensions/mine key,note" {
				t.Errorf("detail %q", e.Detail)
			}
		}
		if strings.Contains(e.Detail, "hunter2") || strings.Contains(e.Detail, "private note") {
			t.Errorf("audit holds a value: %v", e)
		}
	}
	if !found {
		t.Error("the edit wasn't audited")
	}
	// Only someone allowed to manage extensions may.
	g.user("v", "viewer")
	viewer := g.login("v")
	g.want(g.do(opts{cookie: viewer}, "POST", "/api/extensions/settings", `{"source":"x","settings":{}}`), 403)
	g.want(g.do(opts{}, "POST", "/api/extensions/settings", `{"source":"x","settings":{}}`), 401)
}

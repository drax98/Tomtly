package extension

import (
	"context"
	"encoding/json"
	"errors"
	"home-control/internal/store"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// hooked runs an extension and gives it a webhook address, as approving it would.
func hooked(t *testing.T, source string, settings map[string]any) (*Manager, string) {
	t.Helper()
	m := run(t, folder(t, source), settings)
	eventually(t, "it to run", func() bool { return status(m).Running })
	path, err := m.NewWebhookToken(context.Background(), status(m).Source)
	if err != nil {
		t.Fatal(err)
	}
	return m, path
}

func splitHook(path string) (id, token string) {
	parts := strings.Split(strings.TrimPrefix(path, "/hooks/"), "/")
	return parts[0], parts[1]
}

func send(m *Manager, path string, req WebhookRequest) (WebhookResponse, error) {
	id, token := splitHook(path)
	deliver, err := m.Webhook(id, token, testClient)
	if err != nil {
		return WebhookResponse{}, err
	}
	return deliver(context.Background(), req)
}

const echoHook = `defineExtension({id:"hook",name:"Hook",version:"1",access:{webhooks:true},
	webhooks:{receive(c, r){ return {status:201, body:{method:r.method,path:r.path,query:r.query,headers:r.headers,body:r.body,hex:r.bodyHex}, headers:{"X-Seen":"yes","Set-Cookie":"a=b","Content-Length":"1"}}; }}});`

func TestWebhookDeliversTheMessage(t *testing.T) {
	m, path := hooked(t, echoHook, nil)
	resp, err := send(m, path, WebhookRequest{Method: "PUT", Path: "/a/b", Query: map[string]string{"x": "1"}, Headers: map[string]string{"x-sig": "s"}, Body: "hi", BodyHex: "6869"})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Method, Path, Body, Hex string
		Query, Headers          map[string]string
	}
	json.Unmarshal([]byte(resp.Body), &got)
	if resp.Status != 201 || resp.ContentType != "application/json" || got.Method != "PUT" || got.Path != "/a/b" || got.Query["x"] != "1" || got.Headers["x-sig"] != "s" || got.Body != "hi" || got.Hex != "6869" {
		t.Fatalf("got %+v %s", resp, resp.Body)
	}
	if resp.Headers["X-Seen"] != "yes" || len(resp.Headers) != 1 {
		t.Fatalf("only a harmless header may pass, and never a cookie or a length: %v", resp.Headers)
	}
}

func TestWebhookAddressIsKeptOnlyAsAHash(t *testing.T) {
	m, path := hooked(t, echoHook, nil)
	_, token := splitHook(path)
	if len(token) < 43 { // 256 bits
		t.Fatalf("token %q is too short", token)
	}
	stored, err := m.store.WebhookTokenHash(context.Background(), "hook")
	if err != nil || strings.Contains(stored, token) || stored != hashToken(token) {
		t.Fatalf("stored %q, %v", stored, err)
	}
	data, _ := json.Marshal(m.Statuses())
	if strings.Contains(string(data), token) {
		t.Fatalf("the status shows the token: %s", data)
	}
	again, err := m.NewWebhookToken(context.Background(), status(m).Source)
	if err != nil || again == path {
		t.Fatalf("a new address must differ: %q %v", again, err)
	}
	if _, err := send(m, path, WebhookRequest{}); !errors.Is(err, ErrNoWebhook) {
		t.Fatalf("the old address still works: %v", err)
	}
	if _, err := send(m, again, WebhookRequest{}); err != nil {
		t.Fatalf("the new one doesn't: %v", err)
	}
}

func TestWebhookRefusalsLookTheSame(t *testing.T) {
	m, path := hooked(t, echoHook, nil)
	id, token := splitHook(path)
	plain := run(t, folder(t, `defineExtension({id:"plain",name:"p",version:"1",data:{fetch(){return 1}}});`), nil)
	eventually(t, "it to run", func() bool { return status(plain).Running })
	undeclared := run(t, folder(t, `defineExtension({id:"loose",name:"l",version:"1",webhooks:{receive(){}}});`), nil)
	eventually(t, "it to run", func() bool { return status(undeclared).Running })
	for name, c := range map[string]struct {
		m         *Manager
		id, token string
	}{
		"wrong token":                    {m, id, token + "x"},
		"empty token":                    {m, id, ""},
		"another extension's address":    {m, "other", token},
		"no such extension":              {plain, "nobody", token},
		"extension without webhooks":     {plain, "plain", token},
		"webhooks defined, not declared": {undeclared, "loose", token},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := c.m.Webhook(c.id, c.token, testClient); err != ErrNoWebhook {
				t.Fatalf("got %v", err)
			}
		})
	}
	if _, err := undeclared.NewWebhookToken(context.Background(), status(undeclared).Source); err == nil {
		t.Error("an address was made for one that never declared webhooks")
	}
}

func TestWebhooksNeedingAccessAreCounted(t *testing.T) {
	m := run(t, folder(t, `defineExtension({id:"a",name:"a",version:"1",webhooks:{receive(){}}});`), nil)
	expectViolation(t, m, "webhooks")
}

func TestWebhookWithoutAccessOrHandlerFailsToLoad(t *testing.T) {
	failsWith(t, run(t, folder(t, `defineExtension({id:"a",name:"a",version:"1",access:{webhooks:true},data:{fetch(){}}});`), nil), "webhooks.receive")
	failsWith(t, run(t, folder(t, `defineExtension({id:"a",name:"a",version:"1",access:{webhooks:true},webhooks:{}});`), nil), "needs a receive function")
}

func TestWebhookSecretsAreNotEchoed(t *testing.T) {
	const secret = "s3cr3t/key+ü"
	m, path := hooked(t, `defineExtension({id:"hook",name:"Hook",version:"1",access:{webhooks:true},settings:{key:{label:"K",type:"secret"}},
		webhooks:{receive(c, r){
			if (r.path === "/text") return {body: "key=" + c.settings.key};
			if (r.path === "/json") return {body: {deep: [{key: c.settings.key}]}};
			return {headers: {"X-Key": c.settings.key}};
		}}});`, map[string]any{"key": secret})
	for _, route := range []string{"/text", "/json", "/header"} {
		resp, err := send(m, path, WebhookRequest{Path: route})
		if err != nil {
			t.Fatal(route, err)
		}
		all, _ := json.Marshal(resp)
		if strings.Contains(string(all), secret) || !strings.Contains(string(all), "***") {
			t.Errorf("%s answered %s", route, all)
		}
	}
}

func TestWebhookAnswerRules(t *testing.T) {
	for name, c := range map[string]struct {
		receive, wantError string
		status             int
	}{
		"nothing":         {receive: `return;`, status: 204},
		"just a status":   {receive: `return {status: 202};`, status: 202},
		"a string":        {receive: `return "ok";`, wantError: "must return nothing"},
		"a bad status":    {receive: `return {status: 99};`, wantError: "status must be"},
		"throwing":        {receive: `throw new Error("no good");`, wantError: "no good"},
		"too large":       {receive: `return {body: "x".repeat(2000000)};`, wantError: "returned over"},
		"returns forever": {receive: `while (true) {}`, wantError: "took too long"},
	} {
		t.Run(name, func(t *testing.T) {
			if strings.Contains(name, "forever") {
				old := webhookTimeout
				webhookTimeout = 300 * time.Millisecond
				t.Cleanup(func() { webhookTimeout = old })
			}
			m, path := hooked(t, `defineExtension({id:"hook",name:"Hook",version:"1",access:{webhooks:true},webhooks:{receive(c, r){`+c.receive+`}}});`, nil)
			resp, err := send(m, path, WebhookRequest{})
			switch {
			case c.wantError != "" && (err == nil || !strings.Contains(err.Error(), c.wantError)):
				t.Fatalf("got %v %+v", err, resp)
			case c.wantError == "" && (err != nil || resp.Status != c.status):
				t.Fatalf("got %v %+v", err, resp)
			}
			if c.wantError != "" && strings.Contains(name, "forever") {
				// The extension was interrupted, so it isn't stuck for a minute and can take the next message.
				if _, err := send(m, path, WebhookRequest{}); err == nil || !strings.Contains(err.Error(), "took too long") {
					t.Fatalf("second message: %v", err)
				}
			}
		})
	}
}

const testClient = "203.0.113.5"

func TestAStrangersWrongTokensCantStopTheRealSender(t *testing.T) {
	m, path := hooked(t, `defineExtension({id:"hook",name:"Hook",version:"1",access:{webhooks:true},webhooks:{receive(){}}});`, nil)
	id, token := splitHook(path)
	var refused int
	for range webhookRate + 20 {
		if _, err := m.Webhook(id, token+"wrong", testClient); errors.Is(err, ErrWebhookLimit) {
			refused++
		}
	}
	if refused != webhookRate+20-badHookRate {
		t.Fatalf("%d refused", refused)
	}
	// From the same address, over its limit, and past what the extension's own allowance would have been.
	if _, err := m.Webhook(id, token, testClient); err != nil {
		t.Fatalf("the real sender was refused: %v", err)
	}
	// Another address has its own count, and a wrong token only hears not found.
	if _, err := m.Webhook(id, token+"wrong", "203.0.113.6"); !errors.Is(err, ErrNoWebhook) {
		t.Fatalf("got %v", err)
	}
	// An id nobody has is limited the same way, so being refused says nothing about it.
	if _, err := m.Webhook("nobody", token, "203.0.113.7"); !errors.Is(err, ErrNoWebhook) {
		t.Fatalf("got %v", err)
	}
	if _, err := m.Webhook("nobody", token, testClient); !errors.Is(err, ErrWebhookLimit) {
		t.Fatalf("got %v", err)
	}
}

func TestTheRightTokenStillHasALimit(t *testing.T) {
	m, path := hooked(t, `defineExtension({id:"hook",name:"Hook",version:"1",access:{webhooks:true},webhooks:{receive(){}}});`, nil)
	id, token := splitHook(path)
	for range webhookRate {
		if _, err := m.Webhook(id, token, testClient); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.Webhook(id, token, testClient); !errors.Is(err, ErrWebhookLimit) {
		t.Fatalf("got %v", err)
	}
}

type countingStore struct {
	store.Store
	reads atomic.Int32
}

func (c *countingStore) WebhookTokenHash(ctx context.Context, id string) (string, error) {
	c.reads.Add(1)
	return c.Store.WebhookTokenHash(ctx, id)
}

func TestWrongTokensDontReachTheStore(t *testing.T) {
	m, path := hooked(t, `defineExtension({id:"hook",name:"Hook",version:"1",access:{webhooks:true},webhooks:{receive(){}}});`, nil)
	id, token := splitHook(path)
	counting := &countingStore{Store: m.store}
	m.store = counting
	m.hookHashes.Delete(id)
	for range 100 {
		m.Webhook(id, "wrong", testClient)
	}
	if got := counting.reads.Load(); got != 1 {
		t.Errorf("%d reads of the store", got)
	}
	if _, err := m.Webhook(id, token, testClient); err != nil {
		t.Errorf("the right token after the read was cached: %v", err)
	}
}

func TestWebhookCanPublishAndRefreshDevices(t *testing.T) {
	m, path := hooked(t, `defineExtension({id:"hook",name:"Hook",version:"1",access:{webhooks:true},
		devices:{everySeconds:3600, poll(c){ return [{id:"d",name:"state "+state,values:[]}]; }},
		webhooks:{receive(c, r){ state = r.body; c.publish({state: r.body}); c.refreshDevices(); }}});
		let state = "none";`, nil)
	eventually(t, "the first poll", func() bool { d := m.Devices(); return len(d) == 1 && d[0].Name == "state none" })
	if _, err := send(m, path, WebhookRequest{Body: "on"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the poll after the message", func() bool { return m.Devices()[0].Name == "state on" })
	if d, _, ok := m.plugins.Get("hook"); !ok || string(d) != `{"state":"on"}` {
		t.Fatalf("webhook-only plugin was not claimed or didn't publish: %s %v", d, ok)
	}
}

func TestPublishRules(t *testing.T) {
	const head = `defineExtension({id:"a",name:"a",version:"1",settings:{key:{label:"K",type:"secret"}},`
	for name, c := range map[string]struct{ source, want, violation string }{
		"not a plugin": {source: head + `devices:{poll(c){ c.publish(1); return []; }}});`, want: "needs data, webhooks, or background", violation: "publish"},
		"too big":      {source: head + `background:{run(c){ c.publish("x".repeat(1100000)); }}});`, want: "returned over"},
		"a secret":     {source: head + `background:{run(c){ c.publish({k: c.settings.key}); }}});`, want: "secret"},
		"not JSON":     {source: head + `background:{run(c){ c.publish(undefined); }}});`, want: "anything JSON can hold"},
	} {
		t.Run(name, func(t *testing.T) {
			m := run(t, folder(t, c.source), map[string]any{"key": "hunter2-hunter2"})
			failsWith(t, m, c.want)
			if c.violation != "" {
				expectViolation(t, m, c.violation)
			}
		})
	}
}

func TestBackgroundRuns(t *testing.T) {
	failsWith(t, run(t, folder(t, `defineExtension({id:"a",name:"a",version:"1",background:{everySeconds:0.01,run(){}}});`), nil), "at least 0.05")
	m := run(t, folder(t, `defineExtension({id:"a",name:"a",version:"1",background:{everySeconds:0.05,run(c){ n++; c.publish(n); }}}); let n = 0;`), nil)
	eventually(t, "several runs", func() bool { d, _, _ := m.plugins.Get("a"); n, _ := strconv.Atoi(string(d)); return n >= 5 })
}

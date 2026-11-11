package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"tomtly/device"
	"tomtly/internal/auth"
	"tomtly/internal/automation"
	"tomtly/internal/extension"
	"tomtly/internal/store"
	"tomtly/internal/widget"
)

type fakeAdapter struct {
	id    string
	calls []any
}

func (f *fakeAdapter) Devices() []device.Device { return []device.Device{{ID: f.id, Name: f.id}} }
func (f *fakeAdapter) Write(_, dp string, v any) error {
	if dp == "bad" {
		return errors.New("rejected")
	}
	f.calls = append(f.calls, v)
	return nil
}

type fakeEngine struct {
	live       map[string]bool
	saved      string
	keepLive   bool
	mayApprove bool
	pressed    string
}

func (f *fakeEngine) Widgets() []widget.Rendered {
	return []widget.Rendered{{Key: "script:heat/panel", Title: "Heating", From: "heat", Blocks: json.RawMessage(`[{"type":"button","label":"Go","action":"go"}]`), Practising: true}}
}
func (f *fakeEngine) WidgetAction(script, key, action string, input json.RawMessage) (bool, error) {
	if script != "heat" || key != "panel" || action != "go" {
		return false, automation.ErrNoWidget
	}
	f.pressed = string(input)
	return false, nil
}

func (f *fakeEngine) Status() automation.Status { return automation.Status{} }
func (f *fakeEngine) Reload()                   {}
func (f *fakeEngine) SDK() (map[string]string, error) {
	return map[string]string{"home.d.ts": "types"}, nil
}
func (f *fakeEngine) ReadSource(name string) (automation.Source, error) {
	if name == "missing" {
		return automation.Source{}, automation.ErrScriptNotFound
	}
	return automation.Source{Name: name, Source: "src", Version: "v1"}, nil
}
func (f *fakeEngine) SaveSource(name, source, version string, keepLive, mayApprove bool) (automation.Source, error) {
	switch {
	case version == "stale":
		return automation.Source{}, automation.ErrConflict
	case source == "broken":
		return automation.Source{}, errors.New("test.ts:1: bad")
	}
	f.saved, f.keepLive, f.mayApprove = source, keepLive, mayApprove
	return automation.Source{Name: name, Source: source, Version: "v2"}, nil
}
func (f *fakeEngine) SetAccess(name string, keys []string, approved bool) error {
	if name == "unknown" {
		return errors.New("unknown script")
	}
	return nil
}
func (f *fakeEngine) SetLive(name string, live bool) error {
	if name == "unknown" {
		return errors.New("unknown script")
	}
	f.live[name] = live
	return nil
}

func TestMain(m *testing.M) {
	auth.Params.Memory, auth.Params.Time = 64, 1 // real costs would make every login take a visible fraction of a second
	os.Exit(m.Run())
}

// testSession is the cookie of an admin that every helper request carries.
const testSession = "test-admin-session"

// seeded returns a store holding an admin with a live session, so tests of other features can just make requests.
func seeded(t *testing.T) *store.Memory {
	t.Helper()
	st := store.NewMemory()
	ctx := context.Background()
	if err := st.CreateFirstUser(ctx, store.User{ID: "admin-id", Username: "admin", Hash: "x", Role: "admin", Created: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateSession(ctx, auth.HashSecret(testSession), "admin-id", time.Now()); err != nil {
		t.Fatal(err)
	}
	return st
}

func setup(t *testing.T) (http.Handler, *fakeAdapter, *fakeAdapter, *fakeEngine) {
	t.Helper()
	a, b := &fakeAdapter{id: "a"}, &fakeAdapter{id: "b"}
	engine := &fakeEngine{live: map[string]bool{}}
	rooms := map[string][]string{"Living room": {"a"}}
	return New(&Hub{Adapters: []device.Adapter{a, b}}, engine, fakeExtensions{}, seeded(t), t.TempDir(), rooms, []string{"home.example"}, nil), a, b, engine
}

func do(h http.Handler, method, path, ctype, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Host = "localhost:8099"
	r.AddCookie(&http.Cookie{Name: cookieName, Value: testSession})
	if ctype != "" {
		r.Header.Set("Content-Type", ctype)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestDevicesCombineAdapters(t *testing.T) {
	h, _, _, _ := setup(t)
	w := do(h, "GET", "/api/devices", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"id":"a"`) || !strings.Contains(w.Body.String(), `"id":"b"`) {
		t.Errorf("%d %s", w.Code, w.Body)
	}
}

func TestWriteRoutesToOwningAdapter(t *testing.T) {
	h, a, b, _ := setup(t)
	w := do(h, "POST", "/api/devices/b/dps/1", "application/json", `{"value":true}`)
	if w.Code != 202 || len(b.calls) != 1 || len(a.calls) != 0 {
		t.Errorf("%d a=%v b=%v", w.Code, a.calls, b.calls)
	}
}

func TestWriteErrors(t *testing.T) {
	h, _, _, _ := setup(t)
	for _, c := range []struct {
		path, ctype, body string
		code              int
	}{
		{"/api/devices/nope/dps/1", "application/json", `{"value":1}`, 404},
		{"/api/devices/a/dps/bad", "application/json", `{"value":1}`, 400},
		{"/api/devices/a/dps/1", "text/plain", `{"value":1}`, 415},
		{"/api/devices/a/dps/1", "", `{"value":1}`, 415},
		{"/api/devices/a/dps/1", "application/json", `{broken`, 400},
		{"/api/devices/a/dps/1", "application/json", `{"value":"` + strings.Repeat("x", 2000) + `"}`, 413},
	} {
		if w := do(h, "POST", c.path, c.ctype, c.body); w.Code != c.code {
			t.Errorf("%s %q: got %d, want %d", c.path, c.ctype, w.Code, c.code)
		}
	}
}

func TestAutomationSwitch(t *testing.T) {
	h, _, _, e := setup(t)
	if w := do(h, "POST", "/api/automations/heating/live", "application/json", `{"live":true}`); w.Code != 200 || !e.live["heating"] {
		t.Errorf("%d %v", w.Code, e.live)
	}
	if w := do(h, "POST", "/api/automations/heating/live", "text/plain", `{"live":false}`); w.Code != 415 || !e.live["heating"] {
		t.Error("a cross-site form must not be able to switch automations")
	}
	if w := do(h, "POST", "/api/automations/heating/live", "application/json", `{}`); w.Code != 400 {
		t.Errorf("a missing value must not count as false, got %d", w.Code)
	}
	if w := do(h, "POST", "/api/automations/unknown/live", "application/json", `{"live":true}`); w.Code != 400 {
		t.Errorf("got %d", w.Code)
	}
}

func TestRooms(t *testing.T) {
	h, _, _, _ := setup(t)
	if w := do(h, "GET", "/api/rooms", "", ""); !strings.Contains(w.Body.String(), "Living room") {
		t.Error(w.Body.String())
	}
}

func TestScriptSource(t *testing.T) {
	h, _, _, e := setup(t)
	if w := do(h, "GET", "/api/automations/heating/source", "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"version":"v1"`) {
		t.Errorf("%d %s", w.Code, w.Body)
	}
	if w := do(h, "GET", "/api/automations/missing/source", "", ""); w.Code != 404 {
		t.Errorf("got %d", w.Code)
	}
	if w := do(h, "POST", "/api/automations/heating/source", "application/json", `{"source":"x","version":"v1","keepLive":true}`); w.Code != 200 || e.saved != "x" || !e.keepLive {
		t.Errorf("%d %v", w.Code, e)
	}
	for _, c := range []struct {
		body string
		code int
	}{
		{`{"source":"x","version":"stale"}`, 409},
		{`{"source":"broken","version":"v1"}`, 400},
		{`{"version":"v1"}`, 400},
	} {
		if w := do(h, "POST", "/api/automations/heating/source", "application/json", c.body); w.Code != c.code {
			t.Errorf("%s: got %d, want %d", c.body, w.Code, c.code)
		}
	}
	if w := do(h, "POST", "/api/automations/heating/source", "text/plain", `{"source":"evil"}`); w.Code != 415 || e.saved == "evil" {
		t.Error("a cross-site form must not be able to save a script")
	}
	big := `{"source":"` + strings.Repeat("x", 100_000) + `","version":"v1"}`
	if w := do(h, "POST", "/api/automations/heating/source", "application/json", big); w.Code != 200 {
		t.Errorf("a normal large script was refused: %d", w.Code)
	}
	huge := `{"source":"` + strings.Repeat("x", maxScript) + `"}`
	if w := do(h, "POST", "/api/automations/heating/source", "application/json", huge); w.Code != 413 {
		t.Errorf("got %d", w.Code)
	}
}

func TestOtherWebsitesAreRefused(t *testing.T) {
	h, _, _, e := setup(t)
	request := func(method, host, origin string) int {
		r := httptest.NewRequest(method, "/api/automations/heating/live", strings.NewReader(`{"live":true}`))
		r.Host = host
		r.AddCookie(&http.Cookie{Name: cookieName, Value: testSession})
		r.Header.Set("Content-Type", "application/json")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	for _, c := range []struct {
		host, origin string
		code         int
	}{
		{"localhost:8099", "http://localhost:8099", 200},
		{"127.0.0.1:8099", "http://127.0.0.1:8099", 200},
		{"192.168.1.5:8099", "http://192.168.1.5:8099", 200},
		{"[::1]:8099", "", 200},
		{"home.example", "https://home.example", 200},
		{"localhost:8099", "", 200},                            // curl and other non-browser clients
		{"evil.example:8099", "http://evil.example:8099", 403}, // DNS rebinding
		{"localhost:8099", "http://evil.example", 403},
		{"localhost:8099", "null", 403},
	} {
		e.live = map[string]bool{}
		if got := request("POST", c.host, c.origin); got != c.code {
			t.Errorf("host %s origin %s: got %d, want %d", c.host, c.origin, got, c.code)
		}
		if c.code == 403 && e.live["heating"] {
			t.Errorf("host %s origin %s changed state", c.host, c.origin)
		}
	}
}

type fakeExtensions struct{}

func (fakeExtensions) Widgets() []widget.Rendered { return []widget.Rendered{} }
func (fakeExtensions) WidgetAction(string, string, string, json.RawMessage) error {
	return extension.ErrNoWidget
}

func (fakeExtensions) Statuses() []extension.Status { return []extension.Status{} }
func (fakeExtensions) Types() map[string]string     { return map[string]string{} }
func (fakeExtensions) Declared() map[string]bool    { return map[string]bool{} }
func (fakeExtensions) Inspect(context.Context, string) (extension.Status, error) {
	return extension.Status{}, nil
}
func (fakeExtensions) Add(context.Context, string, map[string]any, []string) (extension.Status, error) {
	return extension.Status{}, nil
}
func (fakeExtensions) Remove(string) error { return nil }
func (fakeExtensions) RemovedExtensions(context.Context) ([]store.RemovedExtension, error) {
	return []store.RemovedExtension{}, nil
}
func (fakeExtensions) RestoreExtension(context.Context, string) error { return nil }
func (fakeExtensions) Upload(context.Context, string, []byte) (extension.Status, error) {
	return extension.Status{}, nil
}
func (fakeExtensions) Approve(string, []string) (string, error) { return "", nil }
func (fakeExtensions) StopExtension(string) error               { return nil }
func (fakeExtensions) UpdateSettings(context.Context, string, map[string]any) (extension.Status, error) {
	return extension.Status{}, nil
}
func (fakeExtensions) Replace(context.Context, string, string, map[string]any, []string) (extension.Status, error) {
	return extension.Status{}, nil
}
func (fakeExtensions) Marketplace(context.Context) extension.MarketView {
	return extension.MarketView{Entries: []extension.MarketListing{}}
}

func (fakeExtensions) MarketInspect(context.Context, string) (extension.Status, error) {
	return extension.Status{}, nil
}
func (fakeExtensions) MarketAsset(context.Context, string, string) ([]byte, string, error) {
	return nil, "", extension.ErrMarketNotListed
}
func (fakeExtensions) MarketReviews(context.Context, string, int) (extension.MarketReviews, error) {
	return extension.MarketReviews{Reviews: []extension.MarketReview{}}, nil
}
func (fakeExtensions) MarketPostReview(context.Context, string, int, string, string) error {
	return nil
}
func (fakeExtensions) RemoveDevice(_ context.Context, id string) error {
	if id != "fake:lamp" {
		return extension.ErrUnknownDevice
	}
	return nil
}
func (fakeExtensions) RestoreDevice(context.Context, string) error { return nil }
func (fakeExtensions) Discover(_ context.Context, id string) (extension.Discovery, error) {
	if id != "fake" {
		return extension.Discovery{}, extension.ErrUnknownDevice
	}
	return extension.Discovery{Supported: true, Devices: []extension.FoundDevice{}}, nil
}
func (fakeExtensions) NewWebhookToken(context.Context, string) (string, error) { return "", nil }
func (fakeExtensions) Webhook(string, string, string) (extension.Deliver, error) {
	return nil, extension.ErrNoWebhook
}

// uploads records what reached the extensions.
type uploads struct {
	fakeExtensions
	name     string
	size     int
	approved string
}

func (u *uploads) Upload(_ context.Context, name string, data []byte) (extension.Status, error) {
	u.name, u.size = name, len(data)
	return extension.Status{Source: "upload:x", Name: "Up"}, nil
}
func (u *uploads) Approve(source string, _ []string) (string, error) {
	u.approved = source
	return "", nil
}

func TestUploadAndApprove(t *testing.T) {
	ext := &uploads{}
	h := New(&Hub{}, &fakeEngine{live: map[string]bool{}}, ext, seeded(t), t.TempDir(), nil, nil, nil)
	post := func(path, contentType, origin, body string) int {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		r.Host = "localhost:8099"
		r.AddCookie(&http.Cookie{Name: cookieName, Value: testSession})
		r.Header.Set("Content-Type", contentType)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	const octet = "application/octet-stream"
	if got := post("/api/extensions/upload?name=a.zip", octet, "http://localhost:8099", "data"); got != 200 || ext.name != "a.zip" || ext.size != 4 {
		t.Errorf("got %d %q %d", got, ext.name, ext.size)
	}
	for _, c := range []string{"application/json", "text/plain", "multipart/form-data", ""} {
		ext.name = ""
		if got := post("/api/extensions/upload?name=a.zip", c, "", "data"); got != 415 || ext.name != "" {
			t.Errorf("content type %q: got %d", c, got)
		}
	}
	if got := post("/api/extensions/upload?name=a.zip", octet, "http://evil.example", "data"); got != 403 || ext.name != "" {
		t.Errorf("another website: got %d", got)
	}
	ext.name = ""
	if got := post("/api/extensions/upload?name=a.zip", octet, "", strings.Repeat("x", extension.MaxUpload+1)); got != 413 || ext.name != "" {
		t.Errorf("too large: got %d", got)
	}
	if got := post("/api/extensions/upload?name=a.zip", octet, "", strings.Repeat("x", extension.MaxUpload)); got != 200 {
		t.Errorf("exactly the limit: got %d", got)
	}

	if got := post("/api/extensions/approve", "text/plain", "", `{"source":"s"}`); got != 415 || ext.approved != "" {
		t.Errorf("approve needs JSON: got %d", got)
	}
	if got := post("/api/extensions/approve", "application/json", "", `{"source":"s"}`); got != 200 || ext.approved != "s" {
		t.Errorf("approve: got %d %q", got, ext.approved)
	}
}

func TestHealthzAnswersWithoutAnAccountFromAnyHost(t *testing.T) {
	h, _, _, _ := setup(t)
	r := httptest.NewRequest("GET", "/healthz", nil)
	r.Host = "not-allowed.example"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || w.Body.String() != "ok\n" {
		t.Errorf("%d %q", w.Code, w.Body.String())
	}
	if w := do(h, "POST", "/healthz", "", ""); w.Code == 200 && w.Body.String() == "ok\n" {
		t.Error("only GET is the health check")
	}
}

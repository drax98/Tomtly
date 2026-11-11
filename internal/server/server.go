// Package server is the JSON API and static page over any number of adapters and the automation engine.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"

	"net/http"
	"net/netip"

	"path/filepath"
	"slices"

	"sync"
	"time"

	"tomtly/internal/auth"
	"tomtly/internal/automation"
	"tomtly/internal/extension"
	"tomtly/internal/store"
	"tomtly/internal/typegen"
	"tomtly/internal/widget"
)

const (
	maxBody       = 1024
	maxReviewBody = 32 << 10
	maxScript     = 256 << 10
	maxExtension  = 64 << 10
	// maxSettings fits a file setting, which may be 1 MiB, once JSON has escaped its quotes and line breaks.
	maxSettings = 3 << 20
)

// Automations is what the server needs from the engine.
type Automations interface {
	Status() automation.Status
	SetLive(name string, live bool) error
	SetAccess(name string, keys []string, approved bool) error
	Reload()
	ReadSource(name string) (automation.Source, error)
	SaveSource(name, source, version string, keepLive, mayApprove bool) (automation.Source, error)
	SDK() (map[string]string, error)
	Widgets() []widget.Rendered
	// WidgetAction answers whether the script is live; a practising one only logs what it would do.
	WidgetAction(script, key, action string, input json.RawMessage) (live bool, err error)
}

// Extensions is what the server needs to show and manage third-party code.
type Extensions interface {
	Statuses() []extension.Status
	Types() map[string]string
	Declared() map[string]bool
	Inspect(ctx context.Context, source string) (extension.Status, error)
	Add(ctx context.Context, source string, settings map[string]any, seen []string) (extension.Status, error)
	Remove(source string) error
	Upload(ctx context.Context, filename string, data []byte) (extension.Status, error)
	// Approve returns the path of a webhook address it made, which is shown once.
	Approve(source string, seen []string) (webhook string, err error)
	StopExtension(source string) error
	UpdateSettings(ctx context.Context, source string, settings map[string]any) (extension.Status, error)
	Replace(ctx context.Context, source, newSource string, settings map[string]any, seen []string) (extension.Status, error)
	NewWebhookToken(ctx context.Context, source string) (webhook string, err error)
	Webhook(id, token, client string) (extension.Deliver, error)
	Marketplace(ctx context.Context) extension.MarketView
	MarketInspect(ctx context.Context, id string) (extension.Status, error)
	MarketAsset(ctx context.Context, id, name string) ([]byte, string, error)
	MarketReviews(ctx context.Context, id string, offset int) (extension.MarketReviews, error)
	MarketPostReview(ctx context.Context, id string, rating int, text, name string) error
	RemoveDevice(ctx context.Context, id string) error
	RestoreDevice(ctx context.Context, id string) error
	Discover(ctx context.Context, id string) (extension.Discovery, error)
	Widgets() []widget.Rendered
	WidgetAction(id, key, action string, input json.RawMessage) error
}

// changeDevice answers a request that takes a device out of the system or brings one back.
func (a *api) changeDevice(w http.ResponseWriter, r *http.Request, action string, change func(context.Context, string) error) {
	id := r.PathValue("id")
	switch err := change(r.Context(), id); {
	case errors.Is(err, extension.ErrUnknownDevice):
		reply(w, http.StatusNotFound, errorBody("not found"))
	case err != nil:
		slog.Warn("device change failed", "action", action, "device", id, "err", err)
		reply(w, http.StatusInternalServerError, errorBody("could not save the change"))
	default:
		a.audit(r, action, id)
		reply(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

// New builds the HTTP handler. webDir holds the built page; rooms maps room names to device ids.
// allowedHosts lists host names that may reach the API, besides localhost and IP addresses.
// Every /api/ route needs a login or API token held in st, except the few the route table marks public.
// trustedProxies are the addresses whose X-Forwarded-For is believed.
func New(hub *Hub, engine Automations, extensions Extensions, st store.Store, webDir string, rooms map[string][]string, allowedHosts []string, trustedProxies []netip.Prefix, options ...Option) http.Handler {
	a := newAPI(hub, engine, extensions, st, webDir, rooms, allowedHosts)
	a.trustedProxies = trustedProxies
	for _, o := range options {
		o(a)
	}
	checking, cancel := context.WithTimeout(context.Background(), storeTimeout)
	defer cancel()
	if users, err := st.ListUsers(checking); err == nil && len(users) == 0 {
		a.setupToken()
	}
	return a.handler()
}

// Option changes how New builds the handler.
type Option func(*api)

// RoomOrder is the order config.json lists its rooms in, for the one time they are copied into the database.
func RoomOrder(names []string) Option { return func(a *api) { a.roomOrder = names } }

type api struct {
	hub          *Hub
	engine       Automations
	extensions   Extensions
	store        store.Store
	webDir       string
	rooms        map[string][]string
	allowedHosts []string
	perms        map[string]string
	limiter      *auth.Limiter
	now          func() time.Time
	verify       func(hash, password string) (bool, error)
	spendTime    func(string) error
	setupOnce    sync.Once
	setupCode    string

	trustedProxies []netip.Prefix

	overridesMu sync.Mutex
	overrides   map[string]store.DeviceOverride // nil until read
	roomList    []string                        // in order; nil until read
	roomOrder   []string                        // config.json's, for the first copy
}

func newAPI(hub *Hub, engine Automations, extensions Extensions, st store.Store, webDir string, rooms map[string][]string, allowedHosts []string) *api {
	if rooms == nil {
		rooms = map[string][]string{} // rooms are optional; the page falls back to device types
	}
	return &api{hub: hub, engine: engine, extensions: extensions, store: st, webDir: webDir, rooms: rooms, allowedHosts: allowedHosts, limiter: auth.NewLimiter(), now: time.Now, verify: auth.Verify, spendTime: auth.SpendTime}
}

// route is one API endpoint and what it takes to use it: one of the permissions in package auth, or public (no login)
// or session (any logged-in person, using the login and not a token, so a token can't mint more power).
type route struct {
	pattern string
	perm    string
	handler http.HandlerFunc
}

const (
	public  = "public"
	session = "session"
)

// routes is the whole API. A route without a permission here is refused when the server is built.
func (a *api) routes() []route {
	hub, engine, extensions := a.hub, a.engine, a.extensions
	routes := []route{
		{"GET /api/auth/status", public, a.status},
		{"POST /api/auth/setup", public, a.setup},
		{"POST /api/auth/login", public, a.login},
		{"POST /api/auth/logout", session, a.logout},
		{"POST /api/auth/password", session, a.changePassword},
		{"GET /api/users", auth.UsersManage, a.listUsers},
		{"POST /api/users", auth.UsersManage, a.createUser},
		{"PATCH /api/users/{id}", auth.UsersManage, a.updateUser},
		{"DELETE /api/users/{id}", auth.UsersManage, a.deleteUser},
		{"GET /api/tokens", session, a.listTokens},
		{"POST /api/tokens", session, a.createToken},
		{"DELETE /api/tokens/{id}", session, a.deleteToken},
		{"GET /api/audit", auth.AuditRead, a.listAudit},
		{"GET /api/settings", auth.DevicesRead, a.getHomeSettings},
		{"PUT /api/settings", auth.SettingsManage, a.setHomeSettings},

		{"GET /api/devices", auth.DevicesRead, a.getDevices},
		{"GET /api/rooms", auth.DevicesRead, a.getRooms},
		{"POST /api/rooms", auth.ExtensionsManage, a.addRoom},
		{"PATCH /api/rooms/{name}", auth.ExtensionsManage, a.editRoom},
		{"DELETE /api/rooms/{name}", auth.ExtensionsManage, a.deleteRoom},
		{"PATCH /api/devices/{id}", auth.ExtensionsManage, a.editDevice},
		{"GET /api/pins", auth.DevicesRead, a.getPins},
		{"GET /api/widgets", auth.DevicesRead, a.getWidgets},
		{"PUT /api/dashboard", auth.DevicesWrite, a.setDashboard},
		{"POST /api/widgets/{owner}/{key}/actions/{action}", auth.DevicesWrite, a.widgetAction},
		{"PUT /api/devices/{id}/pins", auth.DevicesWrite, a.setPins},
		{"POST /api/devices/{id}/dps/{dp}", auth.DevicesWrite, func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Value any `json:"value"`
			}
			if !readJSON(w, r, &body, maxBody) {
				return
			}
			err := hub.Write(r.PathValue("id"), r.PathValue("dp"), body.Value)
			switch {
			case errors.Is(err, ErrUnknownDevice):
				reply(w, http.StatusNotFound, errorBody("not found"))
			case err != nil:
				reply(w, http.StatusBadRequest, errorBody(err.Error()))
			default:
				a.audit(r, "device.write", r.PathValue("id")+" "+r.PathValue("dp"))
				reply(w, http.StatusAccepted, map[string]bool{"queued": true})
			}
		}},
		{"GET /api/automations", auth.AutomationsRead, func(w http.ResponseWriter, _ *http.Request) { reply(w, http.StatusOK, engine.Status()) }},
		{"POST /api/automations/reload", auth.AutomationsEdit, func(w http.ResponseWriter, r *http.Request) {
			var body struct{}
			if !readJSON(w, r, &body, maxBody) {
				return
			}
			engine.Reload()
			a.audit(r, "script.reload", "")
			reply(w, http.StatusOK, engine.Status())
		}},
		{"POST /api/automations/{name}/live", auth.AutomationsLive, func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Live *bool `json:"live"`
			}
			if !readJSON(w, r, &body, maxBody) {
				return
			}
			if body.Live == nil {
				reply(w, http.StatusBadRequest, errorBody(`send {"live": true} or {"live": false}`))
				return
			}
			if err := engine.SetLive(r.PathValue("name"), *body.Live); err != nil {
				reply(w, http.StatusBadRequest, errorBody(err.Error()))
				return
			}
			a.audit(r, "script.live", fmt.Sprintf("%s %t", r.PathValue("name"), *body.Live))
			reply(w, http.StatusOK, engine.Status())
		}},
		{"POST /api/automations/{name}/access", auth.AutomationsApprove, func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Keys     []string `json:"keys"`
				Approved *bool    `json:"approved"`
			}
			if !readJSON(w, r, &body, maxScript) {
				return
			}
			if body.Approved == nil {
				reply(w, http.StatusBadRequest, errorBody(`send {"keys": [...], "approved": true} or false`))
				return
			}
			if err := engine.SetAccess(r.PathValue("name"), body.Keys, *body.Approved); err != nil {
				reply(w, http.StatusBadRequest, errorBody(err.Error()))
				return
			}
			for _, key := range body.Keys {
				a.audit(r, "script.access", fmt.Sprintf("%s %s approved=%t", r.PathValue("name"), key, *body.Approved))
			}
			reply(w, http.StatusOK, engine.Status())
		}},
		{"GET /api/automations/sdk", auth.AutomationsRead, func(w http.ResponseWriter, _ *http.Request) {
			files, err := engine.SDK()
			if err == nil {
				maps.Copy(files, extensions.Types())
				files["generated.d.ts"] = typegen.Generate(hub.Devices(), engine.Status().Plugins, extensions.Declared())
			}
			if err != nil {
				reply(w, http.StatusInternalServerError, errorBody(err.Error()))
				return
			}
			reply(w, http.StatusOK, files)
		}},
		{"GET /api/automations/{name}/source", auth.AutomationsRead, func(w http.ResponseWriter, r *http.Request) {
			src, err := engine.ReadSource(r.PathValue("name"))
			if err != nil {
				replyScriptError(w, err)
				return
			}
			reply(w, http.StatusOK, src)
		}},
		{"POST /api/automations/{name}/source", auth.AutomationsEdit, func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Source   *string `json:"source"`
				Version  string  `json:"version"`
				KeepLive bool    `json:"keepLive"`
			}
			if !readJSON(w, r, &body, maxScript) {
				return
			}
			if body.Source == nil {
				reply(w, http.StatusBadRequest, errorBody("send the script as source"))
				return
			}
			perms := principalOf(r).perms
			// Staying live is a live decision, and new code keeping old approvals is an approval decision.
			keepLive := body.KeepLive && slices.Contains(perms, auth.AutomationsLive)
			src, err := engine.SaveSource(r.PathValue("name"), *body.Source, body.Version, keepLive, slices.Contains(perms, auth.AutomationsApprove))
			if err != nil {
				replyScriptError(w, err)
				return
			}
			a.audit(r, "script.save", r.PathValue("name"))
			reply(w, http.StatusOK, src)
		}},
	}
	routes = append(routes, a.extensionRoutes()...)
	return append(routes, a.hookRoutes()...)
}

// hookRoutes is the one way in from outside that isn't under /api: messages to an extension's webhook address. They are
// public because the address holds the secret, not a login.
func (a *api) hookRoutes() []route {
	var out []route
	for _, method := range []string{"POST", "GET", "PUT"} {
		out = append(out, route{method + " /hooks/{id}/{token}", public, a.hook}, route{method + " /hooks/{id}/{token}/{rest...}", public, a.hook})
	}
	return out
}

// webPages are the addresses the page itself handles, the same list as PAGES in web/src/lib/route.ts. Opening or
// refreshing one has to get the page; anything else missing stays a 404.
var webPages = []string{"home", "devices", "automations", "integrations", "plugins", "marketplace", "users", "audit", "tokens", "settings", "rooms"}

// handler serves the page, and the API behind the origin guard and the route table's permissions.
func (a *api) handler() http.Handler {
	mux := http.NewServeMux()
	a.perms = map[string]string{}
	for _, rt := range a.routes() {
		if rt.perm != public && rt.perm != session && !slices.Contains(allPermissions, rt.perm) {
			panic("route " + rt.pattern + " has no valid permission")
		}
		if _, dup := a.perms[rt.pattern]; dup {
			panic("route " + rt.pattern + " is listed twice")
		}
		a.perms[rt.pattern] = rt.perm
		mux.HandleFunc(rt.pattern, rt.handler)
	}
	mux.Handle("/", http.FileServer(http.Dir(a.webDir)))
	for _, page := range webPages {
		app := func(w http.ResponseWriter, r *http.Request) {
			http.ServeFile(w, r, filepath.Join(a.webDir, "index.html"))
		}
		mux.HandleFunc("GET /"+page, app)
		mux.HandleFunc("GET /"+page+"/{rest...}", app)
	}
	guarded := securityHeaders(guardOrigin(a.authorize(mux), a.allowedHosts))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// For Docker's health check and uptime monitors: it only says the service answers, so it needs no account.
		if r.URL.Path == "/healthz" && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			io.WriteString(w, "ok\n") //nolint:errcheck // the client went away
			return
		}
		guarded.ServeHTTP(w, r)
	})
}

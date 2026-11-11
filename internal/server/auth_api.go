package server

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"tomtly/internal/auth"
	"tomtly/internal/store"
)

const maxAuthBody = 4 << 10

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,64}$`)

// These are the only shapes users and tokens leave the service in. Hashes, secrets and session ids have no field here.
type userInfo struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

type userRecord struct {
	userInfo
	Disabled bool      `json:"disabled"`
	Created  time.Time `json:"created"`
}

func recordOf(u store.User) userRecord {
	return userRecord{userInfo{u.ID, u.Username, u.Role}, u.Disabled, u.Created}
}

type tokenRecord struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Role     string     `json:"role"`
	Owner    string     `json:"owner"`
	Created  time.Time  `json:"created"`
	LastUsed *time.Time `json:"lastUsed"`
}

func tokenOf(t store.Token) tokenRecord {
	rec := tokenRecord{t.ID, t.Name, t.Role, t.Owner, t.Created, nil}
	if !t.LastUsed.IsZero() {
		rec.LastUsed = &t.LastUsed
	}
	return rec
}

type statusBody struct {
	SetupRequired bool      `json:"setupRequired"`
	User          *userInfo `json:"user"`
	Permissions   []string  `json:"permissions"`
	// SetupTokenRequired asks for the one-time code from the service's logs.
	SetupTokenRequired bool `json:"setupTokenRequired,omitempty"`
}

func (a *api) status(w http.ResponseWriter, r *http.Request) {
	p, err := a.authenticate(r)
	switch {
	case err == nil:
		reply(w, http.StatusOK, statusBody{User: &userInfo{p.user.ID, p.user.Username, p.user.Role}, Permissions: p.perms})
	case !errors.Is(err, errUnauthorized):
		unavailable(w, err)
	default:
		users, err := a.store.ListUsers(r.Context())
		if err != nil {
			unavailable(w, err)
			return
		}
		none := len(users) == 0
		reply(w, http.StatusOK, statusBody{SetupRequired: none, Permissions: []string{}, SetupTokenRequired: none && a.needsSetupToken(r)})
	}
}

// signIn starts a session and sets its cookie.
func (a *api) signIn(w http.ResponseWriter, r *http.Request, user store.User) error {
	secret, hash := auth.NewSecret("")
	now := a.now()
	if err := a.store.CreateSession(r.Context(), hash, user.ID, now); err != nil {
		return err
	}
	if err := a.store.DeleteExpiredSessions(r.Context(), now, idleLimit, absoluteLimit); err != nil {
		slog.Warn("expired sessions not cleaned", "err", err)
	}
	http.SetCookie(w, a.sessionCookie(r, secret, int(absoluteLimit/time.Second)))
	reply(w, http.StatusOK, statusBody{User: &userInfo{user.ID, user.Username, user.Role}, Permissions: auth.Permissions(user.Role)})
	return nil
}

// setupToken makes the one-time setup code and prints it to the logs, once.
func (a *api) setupToken() string {
	a.setupOnce.Do(func() {
		a.setupCode, _ = auth.NewSecret("")
		slog.Warn("no account exists yet; whoever opens the page from another machine needs this one-time setup code to create the first one", "setupCode", a.setupCode)
	})
	return a.setupCode
}

// needsSetupToken says whether first-run setup has to prove it can read the service's logs. Inside a container the service
// can't tell if it is only published on this machine, so a browser on localhost goes free and anything else is asked.
// The peer has to be near as well, because anyone can type Host: localhost by hand, and a proxy may send it for everyone.
// shortcut: someone on the same private network can still type Host: localhost; only a network restriction closes that, and the first account is all they could get.
func (a *api) needsSetupToken(r *http.Request) bool {
	a.setupToken()
	host, peer := hostName(r.Host), r.RemoteAddr
	if h, _, err := net.SplitHostPort(peer); err == nil {
		peer = h
	}
	ip, nearby := net.ParseIP(peer), false
	if ip != nil {
		nearby = ip.IsLoopback() || ip.IsPrivate()
	}
	hostIP := net.ParseIP(host)
	local := strings.EqualFold(host, "localhost") || hostIP != nil && hostIP.IsLoopback()
	return !local || !nearby || r.Header.Get("X-Forwarded-For") != ""
}

func (a *api) setup(w http.ResponseWriter, r *http.Request) {
	var body struct{ Username, Password, SetupToken string }
	if !readJSON(w, r, &body, maxAuthBody) {
		return
	}
	if !usernamePattern.MatchString(body.Username) {
		reply(w, http.StatusBadRequest, errorBody("the username must be 1 to 64 letters, digits or . _ @ -"))
		return
	}
	if err := auth.ValidatePassword(body.Password); err != nil {
		reply(w, http.StatusBadRequest, errorBody(err.Error()))
		return
	}
	users, err := a.store.ListUsers(r.Context())
	if err != nil {
		unavailable(w, err)
		return
	}
	if len(users) > 0 {
		reply(w, http.StatusForbidden, errorBody("setup is already done"))
		return
	}
	if a.needsSetupToken(r) && subtle.ConstantTimeCompare([]byte(body.SetupToken), []byte(a.setupToken())) != 1 {
		reply(w, http.StatusForbidden, map[string]any{"error": "That setup code isn't right. It's in the service's logs, where it was printed when the service started.", "setupTokenRequired": true})
		return
	}
	hash, err := auth.Hash(body.Password)
	if err != nil {
		unavailable(w, err)
		return
	}
	user := store.User{ID: auth.NewID(), Username: body.Username, Hash: hash, Role: "admin", Created: a.now()}
	switch err := a.store.CreateFirstUser(r.Context(), user); {
	case errors.Is(err, store.ErrExists):
		reply(w, http.StatusForbidden, errorBody("setup is already done"))
		return
	case err != nil:
		unavailable(w, err)
		return
	}
	a.auditAs(r, user.Username, "setup", "first admin created")
	if err := a.signIn(w, r, user); err != nil {
		unavailable(w, err)
	}
}

// throttleKeys are what repeated failures count against: the name, and the caller's address.
func (a *api) throttleKeys(r *http.Request, username string) []string {
	return []string{"user:" + strings.ToLower(username), "ip:" + a.clientIP(r)}
}

func tooMany(w http.ResponseWriter, wait time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
	reply(w, http.StatusTooManyRequests, errorBody("too many attempts; try again later"))
}

func (a *api) login(w http.ResponseWriter, r *http.Request) {
	var body struct{ Username, Password string }
	if !readJSON(w, r, &body, maxAuthBody) {
		return
	}
	keys := a.throttleKeys(r, body.Username)
	// The address's lock only changes what a wrong password hears. A right one still works, or a stranger behind the same
	// address could lock everyone out. Only the name's own lock stops an attempt before it is checked.
	addressLock := a.limiter.Wait(keys[1])
	if wait := a.limiter.Reserve(keys[:1], keys...); wait > 0 {
		tooMany(w, wait)
		return
	}
	user, err := a.store.UserByName(r.Context(), body.Username)
	good := false
	var busy error
	switch {
	case errors.Is(err, store.ErrNotFound):
		busy = a.spendTime(body.Password) // an unknown name costs the same as a wrong password
	case err != nil:
		a.limiter.Refund(keys...)
		unavailable(w, err)
		return
	case len(body.Password) > auth.MaxPassword:
		busy = a.spendTime(body.Password) // and so does one too long to be anyone's
	default:
		good, busy = a.verify(user.Hash, body.Password)
		good = good && !user.Disabled
	}
	if busy != nil {
		a.limiter.Refund(keys...)
		tooMany(w, auth.SlotWait)
		return
	}
	if !good {
		a.auditAs(r, body.Username, "login.failure", "")
		if addressLock > 0 {
			tooMany(w, addressLock)
			return
		}
		reply(w, http.StatusUnauthorized, errorBody("wrong username or password"))
		return
	}
	a.limiter.Clear(keys[0])
	a.limiter.Refund(keys[1]) // only this attempt: one's own account mustn't wash away guessing at others
	a.auditAs(r, user.Username, "login.success", "")
	if err := a.signIn(w, r, user); err != nil {
		unavailable(w, err)
	}
}

func (a *api) logout(w http.ResponseWriter, r *http.Request) {
	p := principalOf(r)
	if err := a.store.DeleteSession(r.Context(), p.session); err != nil {
		unavailable(w, err)
		return
	}
	a.audit(r, "logout", "")
	http.SetCookie(w, a.sessionCookie(r, "", -1))
	reply(w, http.StatusOK, map[string]bool{"loggedOut": true})
}

func (a *api) changePassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current, New string
		// KeepTokens is the choice to leave API tokens working. Left out, they go, since one could be why this is changing.
		KeepTokens bool
	}
	if !readJSON(w, r, &body, maxAuthBody) {
		return
	}
	p := principalOf(r)
	keys := a.throttleKeys(r, p.user.Username)
	if wait := a.limiter.Reserve(keys[:1], keys...); wait > 0 {
		tooMany(w, wait)
		return
	}
	good, busy := false, error(nil)
	if len(body.Current) <= auth.MaxPassword {
		good, busy = a.verify(p.user.Hash, body.Current)
	}
	if busy != nil {
		a.limiter.Refund(keys...)
		tooMany(w, auth.SlotWait)
		return
	}
	if !good {
		a.audit(r, "password.failure", "")
		reply(w, http.StatusForbidden, errorBody("the current password is wrong"))
		return
	}
	a.limiter.Refund(keys...)
	if err := auth.ValidatePassword(body.New); err != nil {
		reply(w, http.StatusBadRequest, errorBody(err.Error()))
		return
	}
	hash, err := auth.Hash(body.New)
	if err != nil {
		unavailable(w, err)
		return
	}
	if _, err := a.store.UpdateUser(r.Context(), p.user.ID, store.UserChange{Hash: &hash, KeepSession: p.session, DropTokens: !body.KeepTokens}); err != nil {
		unavailable(w, err)
		return
	}
	a.audit(r, "password.change", "")
	reply(w, http.StatusOK, map[string]bool{"changed": true})
}

func (a *api) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := a.store.ListUsers(r.Context())
	if err != nil {
		unavailable(w, err)
		return
	}
	out := make([]userRecord, len(users))
	for i, u := range users {
		out[i] = recordOf(u)
	}
	reply(w, http.StatusOK, out)
}

func (a *api) createUser(w http.ResponseWriter, r *http.Request) {
	var body struct{ Username, Password, Role string }
	if !readJSON(w, r, &body, maxAuthBody) {
		return
	}
	switch {
	case !usernamePattern.MatchString(body.Username):
		reply(w, http.StatusBadRequest, errorBody("the username must be 1 to 64 letters, digits or . _ @ -"))
		return
	case !auth.Valid(body.Role):
		reply(w, http.StatusBadRequest, errorBody("the role must be one of "+strings.Join(auth.Roles(), ", ")))
		return
	}
	if err := auth.ValidatePassword(body.Password); err != nil {
		reply(w, http.StatusBadRequest, errorBody(err.Error()))
		return
	}
	hash, err := auth.Hash(body.Password)
	if err != nil {
		unavailable(w, err)
		return
	}
	user := store.User{ID: auth.NewID(), Username: body.Username, Hash: hash, Role: body.Role, Created: a.now()}
	switch err := a.store.CreateUser(r.Context(), user); {
	case errors.Is(err, store.ErrExists):
		reply(w, http.StatusConflict, errorBody("that username is taken"))
	case err != nil:
		unavailable(w, err)
	default:
		a.audit(r, "user.create", user.Username+" "+user.Role)
		reply(w, http.StatusCreated, recordOf(user))
	}
}

func (a *api) updateUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Role     *string `json:"role"`
		Disabled *bool   `json:"disabled"`
		Password *string `json:"password"`
	}
	if !readJSON(w, r, &body, maxAuthBody) {
		return
	}
	change := store.UserChange{Role: body.Role, Disabled: body.Disabled}
	var what []string
	if body.Role != nil {
		if !auth.Valid(*body.Role) {
			reply(w, http.StatusBadRequest, errorBody("the role must be one of "+strings.Join(auth.Roles(), ", ")))
			return
		}
		what = append(what, "role="+*body.Role)
	}
	if body.Disabled != nil {
		what = append(what, fmt.Sprintf("disabled=%t", *body.Disabled))
	}
	if body.Password != nil {
		if err := auth.ValidatePassword(*body.Password); err != nil {
			reply(w, http.StatusBadRequest, errorBody(err.Error()))
			return
		}
		hash, err := auth.Hash(*body.Password)
		if err != nil {
			unavailable(w, err)
			return
		}
		change.Hash, change.DropTokens = &hash, true
		what = append(what, "password reset")
	}
	if len(what) == 0 {
		reply(w, http.StatusBadRequest, errorBody("send role, disabled or password"))
		return
	}
	user, err := a.store.UpdateUser(r.Context(), r.PathValue("id"), change)
	switch {
	case errors.Is(err, store.ErrNotFound):
		reply(w, http.StatusNotFound, errorBody("no such user"))
	case errors.Is(err, store.ErrLastAdmin):
		reply(w, http.StatusConflict, errorBody("there must stay at least one enabled admin"))
	case err != nil:
		unavailable(w, err)
	default:
		a.audit(r, "user.update", user.Username+" "+strings.Join(what, " "))
		reply(w, http.StatusOK, recordOf(user))
	}
}

func (a *api) deleteUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	user, err := a.store.UserByID(r.Context(), id)
	if err == nil {
		err = a.store.DeleteUser(r.Context(), id)
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		reply(w, http.StatusNotFound, errorBody("no such user"))
	case errors.Is(err, store.ErrLastAdmin):
		reply(w, http.StatusConflict, errorBody("there must stay at least one enabled admin"))
	case err != nil:
		unavailable(w, err)
	default:
		a.audit(r, "user.delete", user.Username)
		reply(w, http.StatusOK, map[string]bool{"deleted": true})
	}
}

// ownerOnly narrows token lists and deletes to the caller's own, unless they manage users.
func ownerOnly(p *principal) string {
	if auth.Has(p.user.Role, auth.UsersManage) {
		return ""
	}
	return p.user.ID
}

func (a *api) listTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := a.store.ListTokens(r.Context(), ownerOnly(principalOf(r)))
	if err != nil {
		unavailable(w, err)
		return
	}
	out := make([]tokenRecord, len(tokens))
	for i, t := range tokens {
		out[i] = tokenOf(t)
	}
	reply(w, http.StatusOK, out)
}

func (a *api) createToken(w http.ResponseWriter, r *http.Request) {
	var body struct{ Name, Role string }
	if !readJSON(w, r, &body, maxAuthBody) {
		return
	}
	p := principalOf(r)
	switch {
	case body.Name == "" || len([]rune(body.Name)) > 64:
		reply(w, http.StatusBadRequest, errorBody("the name must be 1 to 64 characters"))
		return
	case !auth.Valid(body.Role):
		reply(w, http.StatusBadRequest, errorBody("the role must be one of "+strings.Join(auth.Roles(), ", ")))
		return
	case !auth.AtMost(body.Role, p.user.Role):
		reply(w, http.StatusForbidden, errorBody("a token can't have more access than you do"))
		return
	}
	secret, hash := auth.NewSecret(tokenPrefix)
	token := store.Token{ID: auth.NewID(), UserID: p.user.ID, Owner: p.user.Username, Name: body.Name, Role: body.Role, Created: a.now()}
	if err := a.store.CreateToken(r.Context(), token, hash); err != nil {
		unavailable(w, err)
		return
	}
	a.audit(r, "token.create", token.Name+" "+token.Role)
	reply(w, http.StatusCreated, struct {
		tokenRecord
		Token string `json:"token"`
	}{tokenOf(token), secret})
}

func (a *api) deleteToken(w http.ResponseWriter, r *http.Request) {
	err := a.store.DeleteToken(r.Context(), r.PathValue("id"), ownerOnly(principalOf(r)))
	switch {
	case errors.Is(err, store.ErrNotFound):
		reply(w, http.StatusNotFound, errorBody("no such token"))
	case err != nil:
		unavailable(w, err)
	default:
		a.audit(r, "token.delete", r.PathValue("id"))
		reply(w, http.StatusOK, map[string]bool{"deleted": true})
	}
}

func (a *api) listAudit(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			reply(w, http.StatusBadRequest, errorBody("limit must be a positive number"))
			return
		}
		limit = min(n, 1000)
	}
	entries, err := a.store.RecentAudit(r.Context(), limit)
	if err != nil {
		unavailable(w, err)
		return
	}
	reply(w, http.StatusOK, entries)
}

// sessionCookie is the one shape of the session cookie: script can't read it, other sites don't send it, and it only
// travels over HTTPS when the request came that way (a plain-HTTP localhost install still has to work).
func (a *api) sessionCookie(r *http.Request, value string, maxAge int) *http.Cookie {
	return &http.Cookie{Name: cookieName, Value: value, Path: "/", MaxAge: maxAge, HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: a.secure(r)} // #nosec G124 -- Secure follows the request's own scheme
}
